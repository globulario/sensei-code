package answerer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	Architecture = "architecture-request"
	Review       = "review-request"
)

// Request is the protocol adapter's projection of an authenticated request.
// Body is the exact mailbox body; Envelope is derived exclusively from it.
type Request struct {
	Kind      string
	Task      string
	ID        string
	Body      string
	Envelope  string
	CommentID int64
}

type Snapshot struct {
	Requests  []Request
	Withdrawn map[string]bool
	Answered  map[string]bool
}

type Mailbox interface {
	List(context.Context) (Snapshot, error)
	Post(context.Context, string) error
}

type Validator func(string) error

type TransportFailure struct {
	RequestID string
	Stage     string
	Err       error
}

func (e *TransportFailure) Error() string {
	return fmt.Sprintf("transport failure for %s at %s: %v", e.RequestID, e.Stage, e.Err)
}
func (e *TransportFailure) Unwrap() error { return e.Err }

// Worker serializes turns, including calls made by an embedding process. The
// filesystem lock is held throughout its life, not just around each HTTP post.
type Worker struct {
	cfg          Config
	mailbox      Mailbox
	model        Model
	architecture Validator
	review       Validator
	mu           sync.Mutex
	lock         *os.File
	attempted    map[string]bool
}

func New(c Config, mailbox Mailbox, model Model, architecture, review Validator) (*Worker, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if mailbox == nil || model == nil || architecture == nil || review == nil {
		return nil, errors.New("mailbox, fresh-call model and both strict validators are required")
	}
	lock, err := os.OpenFile(c.LockPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, fmt.Errorf("single-writer lock: %w", err)
	}
	return &Worker{cfg: c, mailbox: mailbox, model: model, architecture: architecture, review: review, lock: lock, attempted: map[string]bool{}}, nil
}

func (w *Worker) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lock == nil {
		return nil
	}
	err := w.lock.Close()
	w.lock = nil
	return errors.Join(err, os.Remove(w.cfg.LockPath))
}

func supported(r Request) bool { return r.Kind == Architecture || r.Kind == Review }

// live reads existence, supersession and answers from one complete snapshot.
// A changed comment or ambiguous duplicate request cannot stand for the one read.
func live(s Snapshot, r Request) bool {
	if !supported(r) || r.ID == "" || r.Task == "" || r.Envelope == "" || s.Withdrawn[r.ID] || s.Answered[r.ID] {
		return false
	}
	count := 0
	var current Request
	for _, q := range s.Requests {
		if !supported(q) {
			continue
		}
		if q.ID == r.ID {
			if q != r {
				return false
			}
			count++
		}
		if q.Task == r.Task && (current.ID == "" || q.CommentID > current.CommentID) {
			current = q
		}
	}
	return count == 1 && current == r
}

// Poll calls each live request once in this process. A post attempt gets a
// durable O_EXCL reservation before sending: an ambiguous network outcome is
// never retried, even across a clean restart. Reservations intentionally prefer
// a missed reply to a duplicate. They are local transport state, not receipts.
func (w *Worker) Poll(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.lock == nil {
		return errors.New("answerer is closed")
	}
	s, err := w.mailbox.List(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, r := range s.Requests {
		if !live(s, r) || w.attempted[r.ID] {
			continue
		}
		w.attempted[r.ID] = true
		body, err := w.model.Call(ctx, r.Kind, r.Body)
		stage := "model"
		if err == nil {
			stage = "validation"
			validate := w.architecture
			if r.Kind == Review {
				validate = w.review
			}
			err = validate(body)
		}
		if err != nil {
			failures = append(failures, &TransportFailure{r.ID, stage, err})
			continue
		}
		latest, err := w.mailbox.List(ctx)
		if err != nil {
			failures = append(failures, &TransportFailure{r.ID, "relist", err})
			continue
		}
		if !live(latest, r) {
			continue
		}
		// Hex encoding makes arbitrary request IDs safe as reservation filenames.
		reservation, err := os.OpenFile(w.cfg.LockPath+fmt.Sprintf(".posted-%x", r.ID), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			failures = append(failures, &TransportFailure{r.ID, "reservation", err})
			continue
		}
		if err = reservation.Close(); err != nil {
			failures = append(failures, &TransportFailure{r.ID, "reservation", err})
			continue
		}
		// The model never sees configuration or chooses framing. No response byte
		// is trimmed, repaired, normalized or rebound here.
		if err = w.mailbox.Post(ctx, r.Envelope+body); err != nil {
			failures = append(failures, &TransportFailure{r.ID, "post", err})
		}
	}
	return errors.Join(failures...)
}

// Run records failures through the operator's diagnostic boundary and continues
// polling. Human authority requests never enter Poll's call/record path.
func (w *Worker) Run(ctx context.Context, report func(error)) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := w.Poll(ctx); err != nil && report != nil {
			report(err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(w.cfg.PollInterval):
		}
	}
}

// EnvelopePrefix separates framing from payload without interpreting authority.
// It preserves every byte, including CRLF, and refuses duplicate header fields.
func EnvelopePrefix(body string) (string, map[string]string, error) {
	fields := map[string]string{}
	offset := 0
	for i, line := range strings.SplitAfter(body, "\n") {
		offset += len(line)
		text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
		if i == 0 {
			continue
		}
		if text == "" {
			return body[:offset], fields, nil
		}
		key, value, ok := strings.Cut(text, "=")
		if !ok {
			return body[:offset-len(line)], fields, nil
		}
		if key == "" || value == "" || fields[key] != "" {
			return "", nil, errors.New("ambiguous envelope header")
		}
		fields[key] = value
	}
	return "", nil, errors.New("envelope has no payload boundary")
}

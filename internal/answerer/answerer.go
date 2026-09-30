package answerer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Outcome results. Each names what happened to one request in one cycle.
const (
	ResultPosted           = "posted"
	ResultNotLive          = "not-live"
	ResultNotRetried       = "not-retried"
	ResultTransportFailure = "transport-failure"
	ResultSkippedAtPost    = "skipped-at-post"
	ResultPostFailed       = "post-failed"
)

// Outcome is what one cycle did with one request.
type Outcome struct {
	Kind      Kind
	TaskID    string
	RequestID string
	Comment   int64
	Result    string
	Reason    string
}

// ErrResponderMismatch stops the answerer: GitHub attributed a post to an
// identity other than the configured responder, so this process could not
// recognise its own answers and at-most-once would no longer hold.
var ErrResponderMismatch = errors.New("the mailbox attributed the answer to an identity other than the configured responder")

// Answerer answers live requests on one mailbox. It holds no conversation
// state: posted and failed only stop it from repeating a request in this
// process, and neither is ever given to the model.
type Answerer struct {
	cfg    Config
	box    Mailbox
	model  Model
	posted map[string]bool
	failed map[string]bool
}

// New composes an answerer from operator configuration and its two transports.
func New(cfg Config, box Mailbox, model Model) *Answerer {
	return &Answerer{cfg: cfg, box: box, model: model, posted: map[string]bool{}, failed: map[string]bool{}}
}

// Cycle reads the mailbox once and answers every live request.
//
// Per request: one fresh model call on the exact request bytes, format and
// protocol validation, the reply envelope from the request, then a re-list of
// the mailbox immediately before posting. The post happens only if the same
// request is still live, unchanged, and unanswered by the configured responder.
//
// A request whose model output was refused is not retried by this process:
// malformed output is a transport failure, never a reason to keep calling.
func (a *Answerer) Cycle(ctx context.Context) ([]Outcome, error) {
	comments, err := a.box.List(ctx)
	if err != nil {
		return nil, err
	}
	var outcomes []Outcome
	for _, c := range Classify(comments, a.cfg.Publisher, a.cfg.Responder) {
		o := Outcome{Kind: c.Kind, TaskID: c.TaskID, RequestID: c.RequestID, Comment: c.Comment}
		switch {
		case !c.Live:
			o.Result, o.Reason = ResultNotLive, c.Reason
		case a.posted[c.RequestID] || a.failed[c.RequestID]:
			o.Result, o.Reason = ResultNotRetried, "this process already handled it"
		default:
			if err := a.answer(ctx, c.Request, &o); err != nil {
				return append(outcomes, o), err
			}
		}
		outcomes = append(outcomes, o)
	}
	return outcomes, nil
}

func (a *Answerer) answer(ctx context.Context, r Request, o *Outcome) error {
	out, err := a.model.Answer(ctx, Turn{Kind: r.Kind, RequestBody: r.Body})
	if err == nil {
		err = ValidateOutput(r.Kind, out)
	}
	body := r.Envelope + out
	if err == nil && len(body) > maxOutputBytes {
		err = transportFailure("output", "the answer with its envelope is %d bytes; the bound is %d", len(body), maxOutputBytes)
	}
	if err == nil && AnsweredRequest(body) != r.RequestID {
		err = transportFailure("protocol", "the composed answer does not bind to request %s", r.RequestID)
	}
	if err != nil {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		a.failed[r.RequestID] = true
		o.Result, o.Reason = ResultTransportFailure, err.Error()
		return nil
	}

	// At most once: the mailbox is read again immediately before posting.
	comments, err := a.box.List(ctx)
	if err != nil {
		o.Result, o.Reason = ResultSkippedAtPost, "re-listing the mailbox failed: "+err.Error()
		return nil
	}
	if reason := stillLive(Classify(comments, a.cfg.Publisher, a.cfg.Responder), r); reason != "" {
		o.Result, o.Reason = ResultSkippedAtPost, reason
		return nil
	}
	// Marked before posting: a post whose outcome is unknown may have landed,
	// and posting again could answer the same request twice.
	a.posted[r.RequestID] = true
	created, err := a.box.Post(ctx, body)
	if err != nil {
		o.Result, o.Reason = ResultPostFailed, err.Error()
		return nil
	}
	o.Result = ResultPosted
	if !a.cfg.Responder.Matches(created.AuthorID, created.AuthorLogin) {
		o.Reason = fmt.Sprintf("posted as %s (id %s)", created.AuthorLogin, created.AuthorID)
		return ErrResponderMismatch
	}
	return nil
}

// stillLive returns "" when r is still the same live request, or why not.
func stillLive(now []Candidate, r Request) string {
	for _, c := range now {
		if c.RequestID != r.RequestID {
			continue
		}
		switch {
		case c.Comment != r.Comment || c.Body != r.Body || c.Envelope != r.Envelope:
			return "the request changed while it was being answered"
		case !c.Live:
			return c.Reason
		default:
			return ""
		}
	}
	return "the request is no longer on the mailbox"
}

// Run answers until ctx ends or the answerer must stop.
func (a *Answerer) Run(ctx context.Context, every time.Duration, logf func(string)) error {
	for {
		outcomes, err := a.Cycle(ctx)
		for _, o := range outcomes {
			if o.Result != ResultNotLive && o.Result != ResultNotRetried {
				logf(fmt.Sprintf("%s request %s (task %s, comment %d): %s %s",
					o.Kind, o.RequestID, o.TaskID, o.Comment, o.Result, o.Reason))
			}
		}
		if errors.Is(err, ErrResponderMismatch) {
			return err
		}
		if err != nil {
			logf("cycle: " + err.Error())
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

// ErrLocked reports that another answerer instance holds the lock.
var ErrLocked = errors.New("another answerer instance holds the lock")

// Lock is the single-writer lock: a file created exclusively. A second
// instance refuses to start while it exists.
//
// A lock left by a process that died is NOT broken automatically: deciding
// that the holder is gone is exactly the guess that would let two writers run.
// The operator removes it after confirming no instance is running.
type Lock struct {
	path string
	file *os.File
}

// AcquireLock takes the lock or refuses with ErrLocked.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if errors.Is(err, os.ErrExist) {
		return nil, fmt.Errorf("%w: %s exists; remove it only after confirming no answerer is running", ErrLocked, path)
	}
	if err != nil {
		return nil, fmt.Errorf("taking the answerer lock: %w", err)
	}
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return &Lock{path: path, file: f}, nil
}

// Release gives the lock up.
func (l *Lock) Release() error {
	cerr := l.file.Close()
	if err := os.Remove(l.path); err != nil {
		return err
	}
	return cerr
}

// PollInterval is how often the mailbox is read. It paces reading only;
// liveness never comes from the clock.
const PollInterval = time.Minute

// Main is the process: configuration, lock, credentials, then Run. It returns
// the process exit status.
func Main(ctx context.Context, lookup func(string) (string, bool), readFile func(string) ([]byte, error), logf func(string)) int {
	cfg, err := LoadConfig(lookup)
	if err != nil {
		logf(err.Error())
		return 2
	}
	lock, err := AcquireLock(cfg.LockPath)
	if err != nil {
		logf(err.Error())
		return 1
	}
	defer lock.Release()
	token, err := readSecret(readFile, cfg.GitHubTokenPath)
	if err != nil {
		logf(err.Error())
		return 1
	}
	key, err := readSecret(readFile, cfg.ModelKeyPath)
	if err != nil {
		logf(err.Error())
		return 1
	}
	a := New(cfg, NewGitHubMailbox(cfg, token), NewChatModel(cfg.ModelEndpoint, cfg.Model, key))
	logf("answering " + cfg.MailboxRepository + "#" + cfg.MailboxNumber + "; review answers are advisory only")
	if err := a.Run(ctx, PollInterval, logf); err != nil && !errors.Is(err, context.Canceled) {
		logf(err.Error())
		return 1
	}
	return 0
}

// readSecret reads credential content from its configured path. The error
// names the path and never the content.
func readSecret(readFile func(string) ([]byte, error), path string) (string, error) {
	blob, err := readFile(path)
	if err != nil {
		return "", fmt.Errorf("reading credential file %s: %w", path, err)
	}
	secret := strings.TrimSpace(string(blob))
	if secret == "" {
		return "", fmt.Errorf("credential file %s is empty", path)
	}
	return secret, nil
}

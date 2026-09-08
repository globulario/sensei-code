package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// An architecture request is DURABLE the moment it is published. The waiter for
// it is a goroutine.
//
// That asymmetry is the whole reason this file exists. PublishArchitectureRequest
// puts a fully bound request on GitHub; AwaitArchitecture waits for its answer
// inside a context.WithTimeout. When the wait ends -- timeout, cancellation, or
// the process being replaced -- the request keeps standing, still complete,
// still indistinguishable from a live one, addressed to a consumer that no
// longer exists.
//
// Measured on 2026-09-07 (#162): task-1788816711440593285 published request
// 5575818892 at 21:31:52Z, timed out at DefaultWait, and published 5576028789 at
// 22:01:54Z for the same task. Both stood. Five wake signals were then rung at
// the second one for a waiter that had already died with the process. Orphans do
// not merely persist; they accumulate one per timeout, and each looks exactly as
// actionable as the newest.
//
// The repair records the exchange beside the candidate that already persists,
// and closes the loop at startup. A waiter cannot outlive its process, so every
// record still open when this process starts is BY CONSTRUCTION abandoned --
// which is what makes withdrawing them at startup honest rather than a guess.

// WithdrawnMarker retracts a request whose waiter is gone.
//
// Deliberately as narrow as the doorbell and for the same reason: it carries one
// request id and nothing else. It grants nothing, decides nothing, and answers
// nothing. Its only claim is negative -- "no process is waiting for this any
// more" -- which is the one thing an orphaned request currently cannot say.
const WithdrawnMarker = "[sensei-code:withdrawn]"

const withdrawnField = "request"

var withdrawnBody = regexp.MustCompile(`^\[sensei-code:withdrawn\]\n` + withdrawnField + `=([0-9A-Za-z_.:-]{1,128})\n?$`)

// ErrNotAWithdrawal reports a body that is not a withdrawal.
var ErrNotAWithdrawal = errors.New("not a sensei-code withdrawal")

// RenderWithdrawal builds the one body a withdrawal may carry.
func RenderWithdrawal(requestID string) (string, error) {
	if !architectureRequestID.MatchString(strings.TrimSpace(requestID)) {
		return "", fmt.Errorf("a withdrawal needs a well formed request id, got %q", requestID)
	}
	return fmt.Sprintf("%s\n%s=%s\n", WithdrawnMarker, withdrawnField, requestID), nil
}

// ParseWithdrawal reads a withdrawal and returns only the request it retracts.
func ParseWithdrawal(body string) (string, error) {
	normalized := strings.ReplaceAll(strings.TrimSpace(body), "\r\n", "\n") + "\n"
	m := withdrawnBody.FindStringSubmatch(normalized)
	if m == nil {
		return "", ErrNotAWithdrawal
	}
	return m[1], nil
}

// ExchangeRecord is the durable identity of one published request.
//
// It holds what a later process needs to say something true about a request it
// did not publish: which request, where it was posted, and when its waiter was
// due to give up. It deliberately does NOT hold the prompt or the answer -- it
// is a lifetime record, not a second copy of the exchange.
type ExchangeRecord struct {
	TaskID         string    `json:"task_id"`
	RequestID      string    `json:"request_id"`
	RequestComment int64     `json:"request_comment"`
	Conversation   string    `json:"conversation"`
	PublishedAt    time.Time `json:"published_at"`
	Deadline       time.Time `json:"deadline"`
}

// ExchangeLog persists open exchanges.
//
// One file per exchange rather than one appended ledger: an exchange is closed
// by removing its file, so "what is still open" is a directory listing and can
// never disagree with itself. An append-only log would need a compaction pass
// whose failure mode is exactly the one being repaired -- a record that says a
// request stands when it does not.
type ExchangeLog struct {
	// Dir is the directory holding one file per open exchange.
	Dir string
}

var exchangeFileSafe = regexp.MustCompile(`^[0-9A-Za-z_.:-]{1,128}$`)

func (l ExchangeLog) path(taskID, requestID string) (string, error) {
	if !exchangeFileSafe.MatchString(taskID) || !exchangeFileSafe.MatchString(requestID) {
		return "", fmt.Errorf("exchange identity is not a safe file name: task %q request %q", taskID, requestID)
	}
	return filepath.Join(l.Dir, taskID+"."+requestID+".json"), nil
}

// Open records a published request. It is called AFTER the request exists on
// GitHub and before the wait begins: a record without a request would withdraw
// something that was never posted, which is a worse lie than the one being
// fixed.
func (l ExchangeLog) Open(rec ExchangeRecord) error {
	if l.Dir == "" {
		return errors.New("the exchange log has no directory")
	}
	path, err := l.path(rec.TaskID, rec.RequestID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(l.Dir, 0o700); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(blob, '\n'), 0o600)
}

// Close forgets an exchange whose waiter finished with it, answered or not.
// Absent is success: the point of the call is that nothing is waiting.
func (l ExchangeLog) Close(taskID, requestID string) error {
	if l.Dir == "" {
		return nil
	}
	path, err := l.path(taskID, requestID)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Pending lists the exchanges still open, oldest first.
//
// A malformed file is REPORTED, not skipped. Silently ignoring it would restore
// the defect in a new place: a request nothing can account for.
func (l ExchangeLog) Pending() ([]ExchangeRecord, error) {
	if l.Dir == "" {
		return nil, nil
	}
	entries, err := os.ReadDir(l.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var out []ExchangeRecord
	var bad []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(l.Dir, e.Name()))
		if err != nil {
			bad = append(bad, e.Name())
			continue
		}
		var rec ExchangeRecord
		if err := json.Unmarshal(blob, &rec); err != nil || rec.RequestID == "" || rec.TaskID == "" {
			bad = append(bad, e.Name())
			continue
		}
		out = append(out, rec)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PublishedAt.Before(out[j].PublishedAt) })
	if len(bad) > 0 {
		return out, fmt.Errorf("unreadable exchange records: %s", strings.Join(bad, ", "))
	}
	return out, nil
}

// ReconcileAbandonedExchanges withdraws every exchange still open in the log.
//
// Called once at startup, and the timing is the argument: a waiter is a
// goroutine inside AwaitArchitecture, so it cannot have survived into this
// process. Every open record is therefore abandoned by construction -- this
// function never has to guess whether something is still waiting, which is the
// judgement it would get wrong.
//
// The withdrawal is posted by the SAME principal that published the request:
// the App. A retraction of a machine-authored request is protocol content about
// that request, and posting it as the operator would put a person's identity on
// a statement the machine is making about its own state.
//
// A record is closed only after its withdrawal is posted. If posting fails the
// record survives and the next startup tries again, because a forgotten record
// is an orphan nothing will ever account for -- the exact failure being fixed.
func ReconcileAbandonedExchanges(ctx context.Context, log ExchangeLog, box Issue, report func(ExchangeRecord, error)) (int, error) {
	pending, listErr := log.Pending()
	withdrawn := 0
	for _, rec := range pending {
		err := withdraw(ctx, box, rec)
		if err == nil {
			err = log.Close(rec.TaskID, rec.RequestID)
			if err == nil {
				withdrawn++
			}
		}
		if report != nil {
			report(rec, err)
		}
	}
	return withdrawn, listErr
}

func withdraw(ctx context.Context, box Issue, rec ExchangeRecord) error {
	body, err := RenderWithdrawal(rec.RequestID)
	if err != nil {
		return err
	}
	if !box.Valid() {
		return errors.New("withdrawing an exchange needs a mailbox pull request number and an expected remote principal")
	}
	// The record says WHERE the request was posted, and that is not necessarily
	// where this process is pointed now. The mailbox has already moved once in
	// this repository's life (issue #156 to PR #157), and a withdrawal sent to
	// the wrong conversation is worse than none: it names a request id that
	// conversation never carried, while the actual orphan stays standing in the
	// one nobody is looking at any more.
	//
	// Refused rather than redirected, and the record is kept, because this
	// process cannot truthfully retract a request it cannot reach. A record
	// with no conversation predates this field and is withdrawn here, which is
	// the only place it could have come from.
	if rec.Conversation != "" && rec.Conversation != box.Number {
		return fmt.Errorf(
			"request %s was published in conversation %s and this process serves %s; "+
				"it stays open rather than being retracted in the wrong place",
			rec.RequestID, rec.Conversation, box.Number)
	}
	if box.API != nil {
		if !box.API.Configured() {
			return errors.New("the github app transport was selected but is not configured; refusing rather than withdrawing as the operator's gh account")
		}
		_, err = box.API.PostComment(ctx, box.Number, body)
		return err
	}
	out, err := run(ctx, box.Dir, []string{"issue", "comment", box.Number, "--body", body})
	if err != nil {
		return fmt.Errorf("withdrawing request %s: %w: %s", rec.RequestID, err, out)
	}
	return nil
}

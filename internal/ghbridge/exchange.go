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
	// Deadline is when the waiter that published this record intended to stop.
	//
	// For a REVIEW record it is TELEMETRY and never authority (#182 R4). A
	// review obligation does not expire: no lifecycle branch may compare the
	// clock to this field to close, withdraw, replace or remint one. An
	// architecture turn keeps its own meaning for it.
	Deadline time.Time `json:"deadline"`

	// Kind says which lifecycle owns the request. An ARCHITECTURE request is a
	// turn: its waiter is the only consumer, so a waiter that is gone makes the
	// request abandoned and it is withdrawn at startup. A REVIEW request is the
	// durable form of a review OWED on an exact candidate: the waiter timing out
	// or the process dying ends one wait, not the obligation, so startup must not
	// withdraw it. It is retired only when a continuation explicitly supersedes it
	// for the same candidate.
	//
	// Empty means the record predates the field. Those came from the architecture
	// path's lifetime records (#162) and keep that treatment.
	Kind string `json:"kind,omitempty"`

	// The candidate a REVIEW request is about, exactly as the request carried it.
	// A relayed verdict or a continuation is checked against these, never against
	// a candidate re-derived later.
	BaseSHA         string `json:"base,omitempty"`
	CandidateDigest string `json:"candidate_digest,omitempty"`
	CandidateTree   string `json:"candidate_tree,omitempty"`
	ReviewCommit    string `json:"review_commit,omitempty"`

	// ReviewerProvider is the provider the workflow assigned when this request
	// was published, kept so a later process can check an answer against the
	// ASSIGNMENT rather than against whatever is configured by then.
	//
	// Configuration is mutable and a login is a different party's name; neither
	// can say who was asked six hours ago. A restarted process, or a relay
	// arriving after a reconfiguration, validates against this.
	//
	// Empty means the record predates the field (#182 R2). Such a record can
	// still be superseded under the existing lifecycle, but it cannot
	// authenticate a canonical artifact: the assignment is unknown, and
	// inferring it from today's configuration would be inventing the fact the
	// check exists to verify.
	ReviewerProvider string `json:"reviewer_provider,omitempty"`

	// ExpectedReviewerID and ExpectedReviewerLogin pin the GitHub account that
	// was authorized to answer THIS request when it was published.
	//
	// Separate from ReviewerProvider and never a substitute for it: the provider
	// is who the workflow asked to judge, this is which account may supply the
	// bytes. Pinned because configuration is mutable and a request is not. A
	// waiter reattaching hours later must authenticate against the principal the
	// request was published to trust, or changing one config line would make a
	// different account able to answer a question it was never asked.
	//
	// Empty means the record predates the field (#182 R4). Such an obligation
	// cannot be safely reattached, and the gap is never filled from today's
	// configuration.
	ExpectedReviewerID    int64  `json:"expected_reviewer_id,omitempty"`
	ExpectedReviewerLogin string `json:"expected_reviewer_login,omitempty"`

	// The routing the request carried, so a reattaching waiter reads the
	// conversation the request actually went to rather than the one this process
	// happens to be pointed at. Empty on records written before they existed;
	// their historical value is never invented.
	MailboxRepository   string `json:"mailbox_repository,omitempty"`
	WorkspaceRepository string `json:"workspace_repository,omitempty"`
}

const (
	// ExchangeArchitecture marks an architecture turn's request.
	ExchangeArchitecture = "architecture"
	// ExchangeReview marks a review request: an owed review on an exact candidate.
	ExchangeReview = "review"
)

// IsReview reports whether the record is a review obligation. Read by
// membership: only the explicit review kind is one, so an unknown or empty kind
// is never mistaken for an obligation that must be kept.
func (r ExchangeRecord) IsReview() bool { return r.Kind == ExchangeReview }

// Subject is the candidate identity a review request carried.
func (r ExchangeRecord) Subject() Subject {
	return Subject{
		TaskID:          r.TaskID,
		BaseSHA:         r.BaseSHA,
		CandidateDigest: r.CandidateDigest,
		CandidateTree:   r.CandidateTree,
		ReviewCommit:    r.ReviewCommit,
	}
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

// PendingReviews lists the review obligations still open, oldest first.
//
// A review record is not a waiter's bookkeeping; it is the durable statement
// that an exact candidate is owed a review under a named request. Whoever
// continues the task, or relays a verdict for it, reads the obligation here.
func (l ExchangeLog) PendingReviews() ([]ExchangeRecord, error) {
	pending, err := l.Pending()
	var out []ExchangeRecord
	for _, rec := range pending {
		if rec.IsReview() {
			out = append(out, rec)
		}
	}
	return out, err
}

// ReconcileAbandonedExchanges withdraws every abandoned TURN still open in the
// log, and keeps every review obligation.
//
// Called once at startup, and the timing is the argument for turns: an
// architecture waiter is a goroutine inside AwaitArchitecture, so it cannot have
// survived into this process. Every open turn record is therefore abandoned by
// construction -- this function never has to guess whether something is still
// waiting, which is the judgement it would get wrong.
//
// A REVIEW record is deliberately not a turn. Its waiter ending -- a timeout, a
// crash, a restart -- ends one wait on a review the candidate is still owed.
// Withdrawing it here destroyed the only durable statement that a validated
// candidate was awaiting review, so a restart turned a waiting candidate into
// abandoned work. Review records are left open; a continuation retires one only
// when it supersedes it for the same candidate.
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
		if rec.IsReview() {
			continue
		}
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

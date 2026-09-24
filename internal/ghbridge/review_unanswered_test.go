package ghbridge

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

func unansweredReviewFixture(t *testing.T, wait time.Duration) (*Runner, roles.Binding) {
	t.Helper()
	dir, base, tree1, _ := tempRepo(t)
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)
	bare := t.TempDir()
	for _, args := range [][]string{
		{"init", "--bare", "-q", bare},
		{"-C", dir, "remote", "add", "origin", bare},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	// A durable obligation store, because R4 forbids attaching a waiter to a
	// request nothing recorded -- and every production bridge configures one.
	runner := &Runner{
		Issue: box, RepoDir: dir, Remote: "origin", NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: wait, ReviewerProvider: "chatgpt",
		Exchanges: ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")},
	}
	return runner, roles.Binding{TaskID: "T", BaseSHA: base, CandidateTree: tree1, CandidateDigest: digestC1}
}

// An unanswered review is a typed obligation, not a provider failure, and it
// carries the identity the REQUEST was bound to.
func TestAnUnansweredReviewReturnsTheOwedReviewWithItsBinding(t *testing.T) {
	runner, binding := unansweredReviewFixture(t, 120*time.Millisecond)
	var events []event.Event
	_, err := runner.Run(context.Background(), agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding},
		func(e event.Event) { events = append(events, e) })
	if err == nil {
		t.Fatal("an unanswered review returned success")
	}
	if strings.Contains(err.Error(), "publishing the review snapshot") {
		t.Fatalf("the turn never reached the wait, so the unanswered path was not exercised: %v", err)
	}
	if !errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatalf("the unanswered review is not typed as an owed review: %v", err)
	}
	var owed *roles.ReviewUnanswered
	if !errors.As(err, &owed) {
		t.Fatalf("the owed review carries no identity: %v", err)
	}
	if !strings.HasPrefix(owed.RequestID, "r-") {
		t.Errorf("request id %q is not the published request's", owed.RequestID)
	}
	if owed.Binding != binding {
		t.Errorf("binding %+v is not the binding the request carried %+v", owed.Binding, binding)
	}
	if len(owed.ReviewCommit) != 40 {
		t.Errorf("review commit %q is not a full commit id", owed.ReviewCommit)
	}
	if owed.Conversation != "157" {
		t.Errorf("conversation %q is not the mailbox", owed.Conversation)
	}
	if owed.Waited != 120*time.Millisecond {
		t.Errorf("waited %s is not the runner's own deadline", owed.Waited)
	}
	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("the transport cause was lost: %v", err)
	}
}

// A cancelled PARENT context -- a human stop, an invocation budget -- is not an
// unanswered review, and must keep its own error.
func TestACancelledReviewTurnIsNotReportedAsAnOwedReview(t *testing.T) {
	runner, binding := unansweredReviewFixture(t, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := runner.Run(ctx, agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}, nil)
	if err == nil {
		t.Fatal("a cancelled turn returned success")
	}
	if strings.Contains(err.Error(), "publishing the review snapshot") {
		t.Fatalf("the turn never reached the wait: %v", err)
	}
	if errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatalf("a cancelled turn was reported as an owed review: %v", err)
	}
}

// W1 THE MEASURED CASE, END TO END, and W7 NO AUTHORITY.
//
// MEASURED 2026-09-24. Two architecture requests were published, the doorbell
// was rung, and both waited out a 30 minute deadline before the run ended
// INCOMPLETE with "no architecture answer answering that request was posted".
// The consumer had not been silent: it had computed the exact diagnostic below
// and had no grammar to publish it in. An hour of wall clock was spent, the
// cause was misattributed to quota by two separate parties, and the true reason
// surfaced only by accident through another channel.
//
// refusedArchitectureFixture is that run, with a consumer that can now speak.
func refusedArchitectureFixture(t *testing.T, wait time.Duration, stage RefusalStage, reason string) *ArchitectureRunner {
	t.Helper()
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	// The refusal is built INSIDE the handler from the request that was just
	// posted, the way a consumer builds one: it echoes the binding it read,
	// rather than a binding this test knew in advance.
	m.onPost = func(body string) []map[string]any {
		req, ok := ParseArchitectureRequest(body)
		if !ok {
			return nil
		}
		wire, rerr := ArchitectureRefusal{
			Binding: req.Binding, RequestID: req.RequestID, Stage: stage, Reason: reason,
		}.Marker()
		if rerr != nil {
			return nil
		}
		return []map[string]any{{
			"id": float64(7101), "body": wire,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}
	return &ArchitectureRunner{
		Issue: box, Binding: architectureBinding(), NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: wait,
		Exchanges: ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")},
	}
}

func TestTheMeasuredRefusalIsVisibleInSecondsRatherThanAtTheDeadline(t *testing.T) {
	// The deadline is long ON PURPOSE. Speed is half the claim: a refusal that
	// only became visible when the wait expired would pass every assertion about
	// its content while changing nothing about the failure it repairs.
	const deadline = 30 * time.Second
	runner := refusedArchitectureFixture(t, deadline,
		RefusalStagePinnedEvidence, architectureRefusalMeasuredReason)

	var events []event.Event
	start := time.Now()
	res, err := runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "architect this"},
		func(e event.Event) { events = append(events, e) })
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a refused request returned success")
	}
	if !errors.Is(err, ErrArchitectureRefused) {
		t.Fatalf("the refusal was not reported as a refusal: %v", err)
	}
	var refused *ArchitectureRefused
	if !errors.As(err, &refused) {
		t.Fatalf("the refusal carries no typed outcome: %v", err)
	}
	if refused.Stage != RefusalStagePinnedEvidence {
		t.Errorf("stage is %q, want %q", refused.Stage, RefusalStagePinnedEvidence)
	}
	if refused.Reason != architectureRefusalMeasuredReason {
		t.Errorf("the consumer's diagnostic was altered:\n got %q\nwant %q",
			refused.Reason, architectureRefusalMeasuredReason)
	}
	if !refused.Binding.Same(architectureBinding()) || refused.RequestID == "" {
		t.Errorf("the refusal does not name the exact request it ended: %+v", refused)
	}

	// SPEED. No deadline elapsed; the diagnostic surfaced in seconds.
	if elapsed >= deadline {
		t.Fatalf("the refusal took %s, the whole deadline; nothing was gained", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Fatalf("the refusal took %s to surface, which is waiting rather than being told", elapsed)
	}

	// And it reached an OPERATOR, not only the returned error.
	var refusedEvents int
	for _, e := range events {
		if strings.Contains(e.Summary, architectureRefusalMeasuredReason) {
			refusedEvents++
		}
		if strings.Contains(e.Summary, "answered request") {
			t.Errorf("a refusal was reported as an answer: %q", e.Summary)
		}
	}
	if refusedEvents != 1 {
		t.Fatalf("the consumer's diagnostic reached the event stream %d times, want exactly once; "+
			"a reason nobody can see is the defect this repairs", refusedEvents)
	}

	// W7 NO AUTHORITY. The ABSENCE of an architecture decision, asserted as an
	// absence: no text a plan could be read out of, no session standing, no
	// review digest, nothing a caller could mistake for a completed architect
	// turn. The zero Result is the whole claim.
	if res != (agent.Result{}) {
		t.Fatalf("a refusal produced an architecture result: %+v", res)
	}

	// The exchange is SETTLED, not left standing: nothing pending for a later
	// startup to withdraw, because the consumer has already answered.
	pending, perr := runner.Exchanges.Pending()
	if perr != nil {
		t.Fatal(perr)
	}
	if len(pending) != 0 {
		t.Errorf("a refused exchange was left open: %+v", pending)
	}
}

// A refusal for ANOTHER request cannot end this turn, and the turn says so.
//
// The runner-level direction of W4 and W5 together: the consumer replies in the
// right grammar about the wrong request, the wait runs to its own deadline as it
// would have anyway, and the unusable reply is REPORTED rather than dropped. A
// silently discarded refusal would leave exactly the observation this envelope
// exists to remove.
func TestARefusalOfAnotherRequestNeitherEndsThisTurnNorVanishes(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	m.onPost = func(body string) []map[string]any {
		req, ok := ParseArchitectureRequest(body)
		if !ok {
			return nil
		}
		// Everything correct except the request it names.
		wire, rerr := ArchitectureRefusal{
			Binding: req.Binding, RequestID: "r-0000000000000000",
			Stage: RefusalStageWorkspace, Reason: "a refusal of some other exchange",
		}.Marker()
		if rerr != nil {
			return nil
		}
		return []map[string]any{{
			"id": float64(7202), "body": wire,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}
	runner := &ArchitectureRunner{
		Issue: box, Binding: architectureBinding(), NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: 200 * time.Millisecond,
		Exchanges: ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")},
	}
	_, err := runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "architect this"}, nil)
	if err == nil {
		t.Fatal("a refusal of another request ended this turn successfully")
	}
	if errors.Is(err, ErrArchitectureRefused) {
		t.Fatalf("a refusal bound elsewhere ended this exact request: %v", err)
	}
	if !strings.Contains(err.Error(), ErrNoArchitectureAnswer.Error()) {
		t.Fatalf("the turn did not end as it would have without the reply: %v", err)
	}
	if !strings.Contains(err.Error(), "r-0000000000000000") {
		t.Errorf("the unusable reply was dropped in silence: %v", err)
	}
}

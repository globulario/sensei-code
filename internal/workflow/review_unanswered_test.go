package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
)

// unansweredRunner is a review transport whose request was published and never
// answered. It echoes the binding it was ASKED about, exactly as the GitHub
// bridge does, so the test can prove the terminal carries the candidate identity
// the request was bound to rather than one reconstructed afterwards.
type unansweredRunner struct {
	calls *atomic.Int32
}

func (r unansweredRunner) Run(_ context.Context, req agent.Request, _ func(event.Event)) (agent.Result, error) {
	r.calls.Add(1)
	return agent.Result{}, &roles.ReviewUnanswered{
		RequestID:      "r-0123456789abcdef",
		RequestComment: 5686428018,
		Conversation:   "157",
		Binding:        req.Binding,
		ReviewCommit:   "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
		Waited:         30 * time.Minute,
		Cause:          errors.New("no review answering that request was posted: context deadline exceeded"),
	}
}

// A review timeout is not candidate failure.
//
// Before this repair the unanswered review fell into the worker-failure path: the
// next reviewer was tried, the candidate was handed to the next implementer, and
// the run ended FAILED -- which the continuity layer reads as done, so the
// validated candidate could never be resumed. The candidate must instead wait,
// intact, for the review it is owed.
func TestAnUnansweredReviewLeavesTheValidatedCandidateWaitingForReview(t *testing.T) {
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, "accept")

	var calls atomic.Int32
	h.engine.Runners = roleResolver{reviewer: unansweredRunner{calls: &calls}, name: "chatgpt", session: "session-1"}
	// A second reviewer IS configured, so a fallback is possible. An unanswered
	// bridge review must not use it: that would change who judges the candidate.
	h.engine.Config.Reviewers = []config.Agent{
		{Name: "chatgpt", Command: "true", Graph: "none"},
		{Name: "codex", Command: "true", Graph: "none"},
	}
	// A second implementor exists and fails if it is ever called, so a handoff is
	// not a subtle difference in output -- it is the test failing.
	h.engine.Config.Implementors = []config.Agent{h.worker, {Name: "codex", Command: "false", Graph: "none"}}

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	if failed != nil {
		t.Fatalf("an unanswered review failed the run: %v", failed)
	}

	events := drainEvents(h.events)
	if !contains(events, event.WorkflowAwaitingReview) {
		t.Fatalf("no WAITING_REVIEW terminal was emitted: %v", kinds(events))
	}
	for _, forbidden := range []event.Kind{event.WorkflowFailed, event.WorkflowCompleted, event.HandoffCreated} {
		if contains(events, forbidden) {
			t.Fatalf("an unanswered review produced %s: %v", forbidden, kinds(events))
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("the review was asked %d times; an unanswered request must not fall back to another reviewer", got)
	}

	var terminal event.Event
	for _, ev := range events {
		if ev.Kind == event.WorkflowAwaitingReview {
			terminal = ev
		}
	}
	var payload map[string]any
	if err := json.Unmarshal(terminal.Payload, &payload); err != nil {
		t.Fatalf("the WAITING_REVIEW terminal payload is unreadable: %v", err)
	}
	if payload["request_id"] != "r-0123456789abcdef" {
		t.Errorf("the terminal does not name the unanswered request: %v", payload["request_id"])
	}
	for _, key := range []string{"base", "candidate_digest", "candidate_tree", "review_commit"} {
		if s, _ := payload[key].(string); strings.TrimSpace(s) == "" {
			t.Errorf("the terminal does not carry the candidate identity field %q: %v", key, payload)
		}
	}

	receipted := false
	for _, ev := range events {
		if strings.Contains(string(ev.Payload), `"outcome":"UNREVIEWED"`) {
			receipted = true
		}
		if strings.Contains(string(ev.Payload), `"outcome":"FAILED"`) {
			t.Fatalf("a receipt recorded FAILED for an unanswered review: %s", ev.Payload)
		}
	}
	if !receipted {
		t.Error("no run receipt recorded the measured absence of a verdict (UNREVIEWED)")
	}

	found := session.FindInterrupted(withPlan(events, "task-1", "the bounded plan"))
	if len(found) != 1 || !found[0].AwaitingReview {
		t.Fatalf("the waiting candidate is not discoverable as awaiting review: %+v", found)
	}
}

// The opposite control: an ordinary reviewer failure still falls back. Without it
// the test above would pass for an engine that simply never tries a second
// reviewer.
func TestAReviewerThatFailsStillFallsBackWhileAnUnansweredOneDoesNot(t *testing.T) {
	err := error(&roles.ReviewUnanswered{RequestID: "r-1"})
	if !errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatal("the typed unanswered review does not match ErrReviewUnanswered")
	}
	if errors.Is(errors.New("codex exited 1"), roles.ErrReviewUnanswered) {
		t.Fatal("an ordinary provider failure was classified as an unanswered review")
	}
}

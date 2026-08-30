package workflow

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// Finding 1 of the 2026-08-21 audit, reproduced against the reader that made it
// matter. The TUI tells the human "deferred · the question stands, /resume asks
// it again". If the run also records a completion, FindInterrupted treats the
// task as done and /resume answers "nothing to resume".
func TestADeferredPublicationStaysResumable(t *testing.T) {
	base := []event.Event{
		{Kind: event.TaskCreated, TaskID: "t1", Summary: "add a flag"},
		{Kind: event.PlanProposed, TaskID: "t1", Summary: "the bounded plan"},
		{Kind: event.WorkflowAwaitingAuthority, TaskID: "t1", Summary: "authority decision deferred; the question stands"},
	}

	if got := session.FindInterrupted(base); len(got) != 1 {
		t.Fatalf("a deferred question is not resumable on its own: %d entries", len(got))
	}

	// The defect: the same run also reporting that it finished.
	withCompletion := append(append([]event.Event{}, base...),
		event.Event{Kind: event.WorkflowCompleted, TaskID: "t1", Summary: "candidate ready for governed admission"})
	if got := session.FindInterrupted(withCompletion); len(got) != 0 {
		t.Fatal("test premise is wrong: a completion should terminate the task")
	}

	// So the accepted path must not emit one while the question stands.
	// Scoped to the publication region: implement has an earlier completion for
	// the read-only path, which is a different return and not what this asks
	// about.
	whole := funcBody(t, "internal/workflow/engine.go", "implement")
	offer := strings.Index(whole, "offerPullRequest")
	if offer < 0 {
		t.Fatal("the accepted path no longer offers publication")
	}
	body := whole[offer:]
	settled := strings.Index(body, "Settled")
	completed := strings.Index(body, "WorkflowCompleted")
	if settled < 0 {
		t.Fatal("the accepted path no longer asks whether publication settled anything")
	}
	if completed >= 0 && completed < settled {
		t.Fatal("a completion is emitted before the unsettled case returns")
	}
}

// A stop while the publication question is open is a stop, not a success.
func TestAStopAtPublicationIsNotASuccess(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "implement")
	if !strings.Contains(body, "WorkflowStopped") {
		t.Fatal("a stop at the publication rendezvous emits no stop event")
	}
	src := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(src, "stopped while the publication decision was open") {
		t.Fatal("a stop at publication is not reported as one")
	}
}

// The error from awaitChoice was discarded, which is what turned a deferral
// into a completion. Every outcome must now be distinguished.
func TestEveryPublicationOutcomeIsDistinguished(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "offerPullRequest")
	for _, want := range []string{"errAuthorityDeferred", "context.Canceled", "notOffered", "declined", "opened", "failed"} {
		if !strings.Contains(body, want) {
			t.Errorf("offerPullRequest does not distinguish %s", want)
		}
	}
	// Settled is the property the caller branches on.
	if !(publication{State: deferred}).Settled() == false {
		t.Error("a deferred publication reports as settled")
	}
	for _, s := range []publicationState{notOffered, declined, opened, failed} {
		if !(publication{State: s}).Settled() {
			t.Errorf("%s does not report as settled, so the run would never conclude", s)
		}
	}
	if (publication{State: stopped}).Settled() {
		t.Error("a stopped publication reports as settled")
	}
}

// One run, one terminal event. A failed publication used to emit WorkflowFailed
// and then WorkflowCompleted.
func TestAFailedPublicationEmitsOneTerminalEvent(t *testing.T) {
	// The claim is unchanged: one run, one terminal event, and a failed
	// publication reports failure rather than reporting twice. The mechanism
	// moved -- the terminal is now selected in an if/else that routes through
	// emitRunTerminal, which emits the run receipt with it -- so this checks
	// the routing rather than the variable the old shape used.
	body := funcBody(t, "internal/workflow/engine.go", "implement")
	if !strings.Contains(body, "emitRunTerminal") {
		t.Fatal("implement no longer ends the run through the single terminal funnel")
	}
	if !strings.Contains(body, "WorkflowFailed") || !strings.Contains(body, "WorkflowCompleted") {
		t.Fatal("the terminal event is no longer selected from the publication outcome")
	}
	if strings.Contains(body, "e.emit(event.New(e.SessionID, taskID, event.SourceSystem, kind") {
		t.Fatal("the terminal event is emitted outside the funnel, so a run could end without a receipt")
	}
	// offerPullRequest must not emit a terminal event of its own any more: that
	// was the second one.
	offer := funcBody(t, "internal/workflow/engine.go", "offerPullRequest")
	if strings.Contains(offer, "WorkflowFailed") || strings.Contains(offer, "WorkflowCompleted") {
		t.Fatal("offerPullRequest emits its own terminal event, so a run can report twice")
	}
}

// An opened pull request recorded as unpublished contradicts both Git and
// GitHub.
func TestAnOpenedPullRequestIsNotRecordedAsUnpublished(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(src, "accepted by review and published for a human to land") {
		t.Fatal("publication does not change what the disposition records")
	}
}

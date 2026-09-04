package session

import (
	"encoding/json"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

// FindInterrupted reconstructs what a task owes NOW, and does not remember
// whether it ever owed it.
//
// The session log is append-only and the interactive process reuses the latest
// one, so "this task once awaited an independent review" stays true for the
// life of the file. A latch therefore survives the thing it described: a task
// that was advisory-accepted, resumed, and then genuinely revised would still
// resume into a review, asking the same question again while the finding nobody
// acted on aged.

func verdictEvent(taskID string, decision roles.Decision, summary string) event.Event {
	payload, _ := json.Marshal(roles.ReviewVerdict{Decision: decision, Summary: summary})
	var raw json.RawMessage = payload
	return event.Event{
		SessionID: "s", TaskID: taskID, Source: event.SourceReviewer,
		Kind: event.ReviewCompleted, Summary: string(decision) + ": " + summary, Payload: raw,
	}
}

func begun(taskID string) []event.Event {
	return []event.Event{
		event.New("s", taskID, event.SourceUser, event.TaskCreated, "do the thing", nil),
		event.New("s", taskID, event.SourceArchitect, event.PlanProposed, "the bounded plan", nil),
	}
}

func found(t *testing.T, events []event.Event) Interrupted {
	t.Helper()
	got := FindInterrupted(events)
	if len(got) != 1 {
		t.Fatalf("expected one resumable task, got %d", len(got))
	}
	return got[0]
}

// The sequence that exposed the latch: the state ends, and the record still
// holds the event that started it.
func TestABoundedReviseEndsTheAwaitingReviewState(t *testing.T) {
	events := append(begun("task-1"),
		event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview,
			"the candidate stands and owes an independent review", nil),
		verdictEvent("task-1", roles.Revise, "the proof is missing"),
		event.New("s", "task-1", event.SourceUser, event.WorkflowStopped, "stopped", nil),
	)

	task := found(t, events)
	if task.AwaitingReview {
		t.Fatal("a task that was revised still claims to be awaiting an independent review; " +
			"the next resume would review again instead of fixing the finding")
	}
	// And the next actor is handed the finding, not the older obligation notice.
	if task.Review != "the proof is missing" {
		t.Fatalf("the worker would be handed %q rather than the latest verdict", task.Review)
	}
}

func TestABoundedEscalateAlsoEndsIt(t *testing.T) {
	events := append(begun("task-1"),
		event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", nil),
		verdictEvent("task-1", roles.Escalate, "this crosses an architectural boundary"),
		event.New("s", "task-1", event.SourceUser, event.WorkflowStopped, "stopped", nil),
	)
	if found(t, events).AwaitingReview {
		t.Fatal("an escalated task still claims to be awaiting an independent review")
	}
}

// Interruption before any new verdict leaves the obligation standing.
func TestAnInterruptionBeforeANewVerdictLeavesTheObligationStanding(t *testing.T) {
	events := append(begun("task-1"),
		event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", nil),
	)
	if !found(t, events).AwaitingReview {
		t.Fatal("the obligation was lost with nothing having answered it")
	}
	// A review that started and did not finish is still a review that did not
	// happen. The process died; the candidate still owes the look.
	events = append(events, event.New("s", "task-1", event.SourceReviewer, event.ReviewStarted, "reviewing", nil))
	if !found(t, events).AwaitingReview {
		t.Fatal("starting a review discharged the obligation to complete one")
	}
}

// An ACCEPT must not clear the state, and the check has to be made while the
// state is actually SET or it proves nothing.
//
// This test was wrong first: it revised before accepting, so the state was
// already false when the ACCEPT arrived and a mutation that cleared on ACCEPT
// passed it. The sequence that matters is awaiting -> accept -> crash.
func TestAnAcceptDoesNotDischargeTheAwaitingReviewState(t *testing.T) {
	events := append(begun("task-1"),
		event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", nil),
		verdictEvent("task-1", roles.Accept, "the candidate stands"),
	)
	// Crashed here: an advisory accept, and the gate had not yet spoken. The
	// candidate is one nobody objected to, and turning it into implementation
	// work would send a worker at bytes the missing review is owed about.
	if !found(t, events).AwaitingReview {
		t.Fatal("an ACCEPT discharged the obligation to obtain an independent review")
	}
}

// The recurrence. An advisory ACCEPT is followed by a fresh awaiting-review
// terminal, and the state comes back rather than being remembered from before.
func TestTheStateRecursAfterAnAdvisoryAcceptThatStillCannotUnlock(t *testing.T) {
	events := append(begun("task-1"),
		event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", nil),
		verdictEvent("task-1", roles.Revise, "the proof is missing"),
	)
	// The revise ended it: the candidate owes a change, not a look.
	if found(t, events).AwaitingReview {
		t.Fatal("a revise did not end the awaiting-review state")
	}

	// The worker fixes it, an advisory reviewer accepts, and the gate refuses
	// again because nothing established independence.
	events = append(events,
		verdictEvent("task-1", roles.Accept, "the proof is there now"),
		event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview,
			"the candidate stands and owes an independent review", nil),
		event.New("s", "task-1", event.SourceUser, event.WorkflowStopped, "stopped", nil),
	)
	task := found(t, events)
	if !task.AwaitingReview {
		t.Fatal("the state did not recur: a second awaiting-review terminal was not read")
	}
	if task.Review != "the proof is there now" {
		t.Fatalf("the next actor would be handed %q rather than the latest verdict", task.Review)
	}
}

// Order decides, not occurrence. The same three events in the other order mean
// the opposite thing.
func TestTheLatestTransitionWinsRatherThanTheFirst(t *testing.T) {
	awaiting := event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", nil)
	revised := verdictEvent("task-1", roles.Revise, "the proof is missing")

	if !found(t, append(begun("task-1"), revised, awaiting)).AwaitingReview {
		t.Fatal("revise-then-awaiting should end awaiting a review")
	}
	if found(t, append(begun("task-1"), awaiting, revised)).AwaitingReview {
		t.Fatal("awaiting-then-revise should end owing a change")
	}
}

// A malformed or absent verdict payload must not clear the state. An
// unreadable answer is not an answer.
func TestAnUnreadableVerdictDoesNotDischargeTheObligation(t *testing.T) {
	for _, payload := range []json.RawMessage{nil, json.RawMessage(`{`), json.RawMessage(`{"decision":""}`)} {
		events := append(begun("task-1"),
			event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", nil),
			event.Event{SessionID: "s", TaskID: "task-1", Source: event.SourceReviewer,
				Kind: event.ReviewCompleted, Summary: "something", Payload: payload},
		)
		if !found(t, events).AwaitingReview {
			t.Fatalf("an unreadable verdict (%s) discharged the obligation", string(payload))
		}
	}
}

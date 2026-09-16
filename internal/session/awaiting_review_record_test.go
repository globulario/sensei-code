package session

import (
	"bytes"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

func waitingReviewEvents(records ...map[string]any) []event.Event {
	events := []event.Event{
		event.New("s", "task-1", event.SourceUser, event.TaskCreated, "the task", nil),
		event.New("s", "task-1", event.SourceArchitect, event.PlanProposed, "the plan", nil),
	}
	for _, r := range records {
		events = append(events, event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", r))
	}
	return events
}

// The obligation a restart recovers is the one the terminal recorded, byte for
// byte, and the newest statement of it wins.
func TestTheWaitingReviewRecordIsCarriedByteForByteAndTheNewestWins(t *testing.T) {
	first := map[string]any{"review_kind": "unanswered", "request_id": "req-1", "candidate_digest": "d1"}
	second := map[string]any{"review_kind": "unanswered", "request_id": "req-2", "candidate_digest": "d1"}
	events := waitingReviewEvents(first, second)

	got := FindInterrupted(events)
	if len(got) != 1 || !got[0].AwaitingReview {
		t.Fatalf("want one task awaiting review, got %+v", got)
	}
	if !bytes.Equal(got[0].AwaitingReviewRecord, events[len(events)-1].Payload) {
		t.Fatalf("record = %s, want the newest terminal payload %s", got[0].AwaitingReviewRecord, events[len(events)-1].Payload)
	}
}

// A REVISE ends the obligation, and the record of it goes with the flag: a
// revised task must not carry a request identity it no longer owes.
func TestABoundedReviseClearsTheWaitingReviewRecord(t *testing.T) {
	events := waitingReviewEvents(map[string]any{"review_kind": "unanswered", "request_id": "req-1"})
	events = append(events, event.New("s", "task-1", event.SourceReviewer, event.ReviewCompleted, "revise",
		roles.ReviewVerdict{Decision: roles.Revise, Summary: "fix it"}))

	got := FindInterrupted(events)
	if len(got) != 1 {
		t.Fatalf("want the task still interrupted, got %+v", got)
	}
	if got[0].AwaitingReview || len(got[0].AwaitingReviewRecord) != 0 {
		t.Fatalf("a REVISE left AwaitingReview=%v record=%s", got[0].AwaitingReview, got[0].AwaitingReviewRecord)
	}
}

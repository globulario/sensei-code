package project

import (
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
)

func ev(taskID string, kind event.Kind, summary string) event.Event {
	return event.Event{TaskID: taskID, Kind: kind, Summary: summary, Time: time.Now()}
}

// The summary is derived from durable records, so deleting it loses nothing.
// This is the property that keeps it from becoming a second store of claims:
// there is nothing to delete.
func TestTheSummaryIsDerivedAndHoldsReferencesNotClaims(t *testing.T) {
	events := []event.Event{
		ev("t1", event.TaskCreated, "make the counts honest"),
		ev("t1", event.DecisionRecorded, "architectural decision recorded for review: counts are estimates"),
		ev("t1", event.WorkflowCompleted, "candidate ready"),
		ev("t2", event.TaskCreated, "widen the boundary"),
		ev("t2", event.AuthorityRequired, "Authorize the architectural change?"),
	}
	s := Summarize(events, nil, DefaultBounds())

	if len(s.Recent) != 1 || s.Recent[0].Result != "completed" || s.Recent[0].Task != "make the counts honest" {
		t.Fatalf("the completed task was not referenced: %+v", s.Recent)
	}
	if len(s.Open) != 1 || s.Open[0].TaskID != "t2" {
		t.Fatalf("the unanswered authority question was not carried: %+v", s.Open)
	}
	if len(s.Decisions) != 1 {
		t.Fatalf("the recorded decision was not referenced: %+v", s.Decisions)
	}

	out := s.Render()
	// The rendering must say what it is, or an architect will read a reference
	// as a finding.
	if !strings.Contains(out, "not claims") || !strings.Contains(out, "retrieved from Sensei") {
		t.Errorf("the summary does not state that it carries no authority:\n%s", out)
	}
}

// An answered question stops being open. A summary that kept it would send the
// architect back to a boundary somebody already resolved.
func TestAnsweredQuestionsLeaveTheOpenList(t *testing.T) {
	s := Summarize([]event.Event{
		ev("t1", event.TaskCreated, "widen the boundary"),
		ev("t1", event.AuthorityRequired, "Authorize?"),
		ev("t1", event.AuthorityResolved, "Authorize the architectural change"),
	}, nil, DefaultBounds())
	if len(s.Open) != 0 {
		t.Fatalf("an answered question is still listed as open: %+v", s.Open)
	}
}

// Bounds are stated, in the same discipline retrieval uses: a summary that
// silently truncates reads as a complete picture of the project.
func TestBoundsAreDisclosed(t *testing.T) {
	var events []event.Event
	for _, id := range []string{"t1", "t2", "t3", "t4", "t5", "t6", "t7"} {
		events = append(events, ev(id, event.TaskCreated, "task "+id), ev(id, event.WorkflowCompleted, "done"))
	}
	s := Summarize(events, nil, Bounds{Outcomes: 3, Open: 3, Decisions: 3})
	if len(s.Recent) != 3 {
		t.Fatalf("bounds were not applied: %d outcomes", len(s.Recent))
	}
	if len(s.Dropped) == 0 {
		t.Fatal("the summary truncated silently")
	}
	if !strings.Contains(s.Render(), "Not shown, bounded for size") {
		t.Errorf("the rendering hides its own truncation:\n%s", s.Render())
	}
}

// A resolved candidate is history; one that still exists is standing context.
// Reporting a disposed candidate as present would send somebody looking for a
// worktree that is gone.
func TestOnlyCandidatesThatStillExistAreStandingContext(t *testing.T) {
	kept := candidate.Identity{TaskID: "kept", BaseSHA: "a", Resolution: &candidate.Resolution{
		Disposition: candidate.Retained, Reason: "accepted and unpublished"}}
	gone := candidate.Identity{TaskID: "gone", BaseSHA: "b", Resolution: &candidate.Resolution{
		Disposition: candidate.Disposed, Reason: "produced no work"}}
	undecided := candidate.Identity{TaskID: "undecided", BaseSHA: "c"}

	s := Summarize(nil, []candidate.Identity{kept, gone, undecided}, DefaultBounds())
	if len(s.Retained) != 2 {
		t.Fatalf("expected the retained and the undecided, got %+v", s.Retained)
	}
	for _, c := range s.Retained {
		if c.TaskID == "gone" {
			t.Error("a disposed candidate was reported as still existing")
		}
	}
	if !strings.Contains(s.Render(), "nobody has decided") {
		t.Errorf("an undecided candidate is not named as one:\n%s", s.Render())
	}
}

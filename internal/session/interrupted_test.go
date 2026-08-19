package session

import (
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

func ev(taskID string, source event.Source, kind event.Kind, summary string) event.Event {
	return event.Event{TaskID: taskID, Source: source, Kind: kind, Summary: summary}
}

func TestPlannedButUnfinishedTaskIsResumable(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "add a report command"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
		ev("t1", event.SourceReviewer, event.Status, "REVISE: counts are presented as exact"),
	})
	if len(got) != 1 {
		t.Fatalf("found %d resumable tasks, want 1", len(got))
	}
	if got[0].Task != "add a report command" || got[0].Plan != "the plan" {
		t.Fatalf("recovered task is incomplete: %+v", got[0])
	}
	if got[0].Review != "REVISE: counts are presented as exact" {
		t.Fatalf("the reviewer's last finding was lost: %+v", got[0])
	}
}

func TestFinishedTasksAreNotResumable(t *testing.T) {
	for name, terminal := range map[string]event.Kind{
		"completed": event.WorkflowCompleted,
		"failed":    event.WorkflowFailed,
	} {
		got := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
			ev("t1", event.SourceSystem, terminal, "done"),
		})
		if len(got) != 0 {
			t.Fatalf("%s task offered for resume: %+v", name, got)
		}
	}
}

func TestTaskWithoutAPlanIsNotResumable(t *testing.T) {
	// /resume re-enters implementation with the bounded plan. A task that never
	// produced one has nothing to continue, and offering it would restart the
	// work rather than resume it.
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		ev("t1", event.SourceArchitect, event.ArchitectSpoke, "answered the question instead"),
	})
	if len(got) != 0 {
		t.Fatalf("a task that was never planned was offered for resume: %+v", got)
	}
}

// A stop must leave work recoverable, or it is a destructive act wearing the
// name of a pause. This is the session-level half of that guarantee: the
// candidate stays on disk, and the record still offers the task.
func TestStoppedTaskRemainsResumable(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
		ev("t1", event.SourceSystem, event.WorkflowStopped, "stopped by the human; the candidate is left as it stands"),
	})
	if len(got) != 1 || got[0].TaskID != "t1" {
		t.Fatalf("a stopped task was treated as finished: %+v", got)
	}
}

func TestEachTaskIsJudgedSeparately(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "finished one"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "a plan"),
		ev("t1", event.SourceSystem, event.WorkflowCompleted, "done"),
		ev("t2", event.SourceSystem, event.TaskCreated, "interrupted one"),
		ev("t2", event.SourceArchitect, event.PlanProposed, "a plan"),
	})
	if len(got) != 1 || got[0].TaskID != "t2" {
		t.Fatalf("got %+v, want only the interrupted task", got)
	}
}

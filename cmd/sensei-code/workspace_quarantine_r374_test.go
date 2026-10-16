package main

// RULING-374 W13. An invocation whose in-run containment admission refused it
// ends on the existing UNVERIFIED WorkflowWorkspaceQuarantined terminal (its
// engine half: internal/workflow TestR374W1toW8TheGateRefusalIsTheEnding).
// The headless consumer ends on it with exit 11, at once, and no later event
// rewrites it.

import (
	"context"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

func TestR374W13TheInRunContainmentRefusalExitsEleven(t *testing.T) {
	const task = "t-r374"
	reason := "the containment admission of task " + task + " failed: its candidate record could not be read under its " +
		"lock, so its containment state was not established: the candidate record lock was not released in time"
	terminal := event.New("s", task, event.SourceSystem, event.WorkflowWorkspaceQuarantined,
		"the invocation of task "+task+" ended at an in-run containment admission that refused it: "+reason+
			"; no further workspace execution was authorized",
		map[string]any{"task_id": task, "recorded": false, "reason": reason})
	for _, deferred := range []bool{false, true} {
		if code, final := exitFor(terminal.Kind, deferred); code != 11 || !final {
			t.Fatalf("the in-run containment refusal exits %d (final %v), want 11", code, final)
		}
	}
	code := streamUntilSettled(context.Background(), &fakeControl{},
		feed(ev(task, event.Status), terminal, ev(task, event.WorkflowFailed)), task, false, true, 0)
	if code != 11 {
		t.Fatalf("the headless run ended %d on the in-run containment refusal, want 11", code)
	}
}

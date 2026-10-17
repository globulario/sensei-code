package event

import "testing"

// N5 P2-A (RULING-366 §5, Q2; §30 W14, mutant M1). An invocation that ended
// while its workspace containment was not established ends the INVOCATION:
// the task stays discoverable with an outstanding obligation. It is not a
// task ending, and no other ending moved.
func TestAWorkspaceQuarantineEndsTheInvocationNotTheTask(t *testing.T) {
	const kind = Kind("workflow.workspace_quarantined")
	got, ok := RunTerminality(kind)
	if !ok || got != InvocationTerminal {
		t.Fatalf("%s is classified %q (ok=%v), want invocation-terminal", kind, got, ok)
	}
	for k, want := range map[Kind]Terminality{
		WorkflowCompleted: TaskTerminal, WorkflowFailed: TaskTerminal, WorkflowObserved: TaskTerminal,
		WorkflowStopped: InvocationTerminal, WorkflowTimedOut: InvocationTerminal,
	} {
		if got, ok := RunTerminality(k); !ok || got != want {
			t.Errorf("%s moved to %q (ok=%v), want %q", k, got, ok, want)
		}
	}
}

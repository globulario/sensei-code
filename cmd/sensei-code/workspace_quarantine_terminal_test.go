package main

// RULING-368 §4-§6, §11-§12 (H1-H11). Every accepted invocation terminal ends
// the corresponding live user-facing wait and stays visible to the operator.
// WORKSPACE_QUARANTINED ends the INVOCATION with its own exit code (11): the
// invocation ended, workspace containment is unresolved, and ordinary
// continuation is forbidden. It is not completed, failed, stopped, timed out,
// awaiting authority or restoration refused.
//
// The witnesses drive a real event through the headless consumer: the event
// is published on a real event.Bus and settled through settleInvocation /
// streamUntilSettled / drainUntilTerminal, the functions run, observe, resume
// and audit-repair wait with. The expected code is the literal 11, so the
// pre-fix controls compile against the head that lacks the constant.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/workflow"
)

const (
	r368Exit   = 11
	r368Task   = "task-quarantined"
	r368Reason = "the certify execution has no established teardown"
)

// r368Terminal is the verified terminal the engine emits (engine.go
// terminateQuarantined): its summary and its workflow.WorkspaceQuarantine
// payload.
func r368Terminal(taskID string) event.Event {
	q := workflow.WorkspaceQuarantine{TaskID: taskID, Recorded: true, Invocation: "exec-7", Phase: "certify",
		Workspace: "/repo/.worktrees/" + taskID, Baseline: "/repo/.worktrees/" + taskID + "-baseline", Reason: r368Reason}
	summary := "the invocation ended while the workspace containment of task " + taskID +
		" is not established (its candidate record: " + r368Reason + "); workspace " + q.Workspace +
		" and baseline workspace " + q.Baseline +
		" may still be written, so the task is owed a trusted settlement and nothing ordinary continues it"
	return event.New("session-q", taskID, event.SourceSystem, event.WorkflowWorkspaceQuarantined, summary, q)
}

// r368Capture runs fn with stdout and stderr redirected to files and returns
// its code and both outputs. fn must return within the bound, or the wait it
// exercises stayed blocked on an ending it had already been handed.
func r368Capture(t *testing.T, bound time.Duration, fn func() int) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	done := make(chan int, 1)
	go func() { done <- fn() }()
	var code int
	select {
	case code = <-done:
		os.Stdout, os.Stderr = savedOut, savedErr
	case <-time.After(bound):
		os.Stdout, os.Stderr = savedOut, savedErr
		t.Fatalf("the headless wait did not end within %s after the WORKSPACE_QUARANTINED terminal reached it", bound)
	}
	outFile.Close()
	errFile.Close()
	stdout, _ := os.ReadFile(filepath.Join(dir, "stdout"))
	stderr, _ := os.ReadFile(filepath.Join(dir, "stderr"))
	return code, string(stdout), string(stderr)
}

// r368Settle publishes evs on a real bus and settles one ENDED invocation
// through settleInvocation, exactly as run/observe/resume/audit-repair do.
func r368Settle(t *testing.T, asJSON, quiet bool, who string, evs ...event.Event) (int, string, string) {
	t.Helper()
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(64)
	defer unsubscribe()
	for _, e := range evs {
		bus.Publish(e)
	}
	handle := newFakeInvocation()
	close(handle.ended) // everything the invocation published is already on the bus
	return r368Capture(t, 5*time.Second, func() int {
		return settleInvocation(context.Background(), &fakeControl{}, events, r368Task, handle, asJSON, quiet, 0, who)
	})
}

// H1 (pre-fix control). The real terminal reaches the headless consumer and
// ends it through exitFor with the distinct code, not exitFailed via the
// stream closing.
func TestR368H1TheHeadlessConsumerEndsOnAWorkspaceQuarantine(t *testing.T) {
	code, _, stderr := r368Settle(t, false, false, "sensei-code run",
		event.New("session-q", r368Task, event.SourceSystem, event.Status, "working", nil), r368Terminal(r368Task))
	if code != r368Exit {
		t.Fatalf("a WORKSPACE_QUARANTINED invocation exited %d, want %d\nstderr: %s", code, r368Exit, stderr)
	}
	if got, ok := exitFor(event.WorkflowWorkspaceQuarantined, false); !ok || got != r368Exit {
		t.Fatalf("exitFor(%s) = %d, %v; want %d, true", event.WorkflowWorkspaceQuarantined, got, ok, r368Exit)
	}
	if got, ok := exitFor(event.WorkflowWorkspaceQuarantined, true); !ok || got != r368Exit {
		t.Fatalf("a deferred question rewrote the quarantine ending into %d (%v)", got, ok)
	}
}

// H6. The exit is PROMPT: the stream stays open, no other ending follows, and
// the wait still ends on the quarantine.
func TestR368H6TheQuarantineEndsTheWaitPromptlyWithExit11(t *testing.T) {
	events := make(chan event.Event, 2) // stays open: nothing else will ever arrive
	events <- r368Terminal(r368Task)
	code, _, _ := r368Capture(t, 5*time.Second, func() int {
		return streamUntilSettled(context.Background(), &fakeControl{}, events, r368Task, false, true, 0)
	})
	if code != r368Exit {
		t.Fatalf("exit = %d, want %d", code, r368Exit)
	}
}

// H2 (pre-fix control) and H7. A quiet run still prints the terminal and its
// reason.
func TestR368H7AQuietRunStillShowsTheQuarantineAndItsReason(t *testing.T) {
	code, stdout, _ := r368Settle(t, false, true, "sensei-code run",
		event.New("session-q", r368Task, event.SourceSystem, event.Status, "working", nil), r368Terminal(r368Task))
	if !strings.Contains(stdout, string(event.WorkflowWorkspaceQuarantined)) {
		t.Fatalf("a quiet run did not render the WORKSPACE_QUARANTINED terminal; stdout:\n%s", stdout)
	}
	if !strings.Contains(stdout, r368Reason) || !strings.Contains(stdout, "owed a trusted settlement") {
		t.Fatalf("a quiet run did not render the quarantine's reason; stdout:\n%s", stdout)
	}
	if strings.Contains(stdout, "working") {
		t.Fatalf("quiet mode rendered a non-terminal status; stdout:\n%s", stdout)
	}
	if code != r368Exit {
		t.Fatalf("exit = %d, want %d", code, r368Exit)
	}
}

// H8. JSON keeps the existing event shape and reports the same terminal; no
// other outcome is invented.
func TestR368H8JSONReportsTheSameTerminalAndNoOther(t *testing.T) {
	want := r368Terminal(r368Task)
	code, stdout, _ := r368Settle(t, true, false, "sensei-code run", want)
	if code != r368Exit {
		t.Fatalf("exit = %d, want %d", code, r368Exit)
	}
	lines := strings.Split(strings.TrimSpace(stdout), "\n")
	if len(lines) != 1 {
		t.Fatalf("JSON output has %d lines, want exactly the one terminal:\n%s", len(lines), stdout)
	}
	var got event.Event
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("the JSON line is not the event shape: %v\n%s", err, lines[0])
	}
	if got.Kind != event.WorkflowWorkspaceQuarantined || got.Summary != want.Summary || got.TaskID != r368Task ||
		got.Source != event.SourceSystem || string(got.Payload) != string(want.Payload) {
		t.Fatalf("the JSON terminal differs from the engine's:\n got %+v\nwant %+v", got, want)
	}
	for _, other := range []event.Kind{event.WorkflowCompleted, event.WorkflowFailed, event.WorkflowStopped, event.WorkflowTimedOut} {
		if strings.Contains(stdout, `"`+string(other)+`"`) {
			t.Fatalf("JSON output invented %s", other)
		}
	}
}

// H9. A resumed invocation being drained ends on the quarantine: through the
// settle path settleResumed delegates to once its binding is verified, and
// through the terminal drain itself.
func TestR368H9AResumedInvocationDrainEndsWithExit11(t *testing.T) {
	code, _, stderr := r368Settle(t, false, true, "sensei-code resume",
		event.New("session-q", r368Task, event.SourceSystem, event.Status, "resumed", nil), r368Terminal(r368Task))
	if code != r368Exit {
		t.Fatalf("the resumed invocation exited %d, want %d\nstderr: %s", code, r368Exit, stderr)
	}

	defer func(d time.Duration) { terminalGrace = d }(terminalGrace)
	terminalGrace = 2 * time.Second
	events := make(chan event.Event, 2) // stays open
	events <- event.New("session-q", r368Task, event.SourceSystem, event.Status, "resumed", nil)
	events <- r368Terminal(r368Task)
	start := time.Now()
	code, _, _ = r368Capture(t, 5*time.Second, func() int {
		return drainUntilTerminal(events, r368Task, json.NewEncoder(os.Stdout), false, true, false, exitTimeout)
	})
	if code != r368Exit {
		t.Fatalf("the terminal drain returned %d, want %d", code, r368Exit)
	}
	if time.Since(start) >= terminalGrace {
		t.Fatal("the drain waited out its grace window instead of recognising the quarantine")
	}
}

// H10. A cancellation racing a real WORKSPACE_QUARANTINED terminal is not
// reported as exitTimeout: the configured timeout never replaces an
// established quarantine terminal.
func TestR368H10ACancellationRaceIsNotReportedAsATimeout(t *testing.T) {
	defer func(d time.Duration) { terminalGrace = d }(terminalGrace)
	terminalGrace = 50 * time.Millisecond
	// The engine already terminalized on the quarantine; the deadline lost.
	for i := 0; i < settlementDraws; i++ {
		ctrl := &fakeControl{settled: true}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		events := make(chan event.Event, 1)
		events <- r368Terminal(r368Task)
		code, _, _ := r368Capture(t, 5*time.Second, func() int {
			return streamUntilSettled(ctx, ctrl, events, r368Task, false, true, 25*time.Minute)
		})
		if code != r368Exit {
			t.Fatalf("draw %d: exit = %d, want %d: the deadline replaced an established quarantine", i, code, r368Exit)
		}
		if len(ctrl.timedOut) != 0 {
			t.Fatalf("draw %d: a lost timeout stated a cause: %v", i, ctrl.timedOut)
		}
	}
	// The deadline claimed a live task and the engine's real ending is the
	// quarantine (the budget expired too, and the invocation ends on it).
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan event.Event, 1)
	ctrl := &fakeControl{onTimeOut: func() {
		q := r368Terminal(r368Task)
		q.Summary += "; the execution budget also expired, and the invocation ends on the quarantine"
		events <- q
	}}
	code, _, _ := r368Capture(t, 5*time.Second, func() int {
		return streamUntilSettled(ctx, ctrl, events, r368Task, false, true, 25*time.Minute)
	})
	if code != r368Exit {
		t.Fatalf("exit = %d, want %d: a claimed deadline whose real ending is the quarantine reported %d", code, r368Exit, code)
	}
}

// H11. Every existing exit code keeps its numeric meaning, and 11 is distinct.
func TestR368H11ExistingExitCodesKeepTheirMeaning(t *testing.T) {
	codes := map[string][2]int{
		"completed": {exitCompleted, 0}, "failed": {exitFailed, 1}, "usage": {exitUsage, 2},
		"awaiting authority": {exitAwaitingAuthority, 3}, "stopped": {exitStopped, 4}, "timeout": {exitTimeout, 5},
		"observed": {exitObserved, 6}, "awaiting review": {exitAwaitingReview, 7}, "blocked external": {exitBlockedExternal, 8},
		"not converged": {exitNotConverged, 9}, "restoration refused": {exitRestorationRefused, 10},
	}
	seen := map[int]string{r368Exit: "workspace quarantined"}
	for name, c := range codes {
		if c[0] != c[1] {
			t.Fatalf("exit code for %s moved to %d, want %d", name, c[0], c[1])
		}
		if other, dup := seen[c[0]]; dup {
			t.Fatalf("exit code %d is used for %s and %s", c[0], name, other)
		}
		seen[c[0]] = name
	}
	kinds := map[event.Kind]int{
		event.WorkflowCompleted: 0, event.WorkflowObserved: 6, event.WorkflowFailed: 1, event.WorkflowTimedOut: 5,
		event.WorkflowStopped: 4, event.WorkflowAwaitingAuthority: 3, event.WorkflowAwaitingReview: 7,
		event.WorkflowBlockedExternal: 8, event.WorkflowNotConverged: 9, event.WorkflowRestorationRefused: 10,
		event.WorkflowWorkspaceQuarantined: r368Exit,
	}
	for k, want := range kinds {
		if got, ok := exitFor(k, false); !ok || got != want {
			t.Fatalf("exitFor(%s) = %d, %v; want %d", k, got, ok, want)
		}
	}
	if got, _ := exitFor(event.WorkflowStopped, true); got != 3 {
		t.Fatalf("a deferred stop exits %d, want 3", got)
	}
	if _, ok := exitFor(event.AuthorityRequired, false); ok {
		t.Fatal("AuthorityRequired must not end an invocation")
	}
}

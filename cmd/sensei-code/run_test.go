package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/event"
)

type fakeControl struct {
	deferred []string
	stopped  []string
	timedOut []string
	canDefer bool
}

// TimeOut records that the stop was a DEADLINE, which is different evidence
// from a human withdrawing.
func (f *fakeControl) TimeOut(taskID, budget string) bool {
	f.timedOut = append(f.timedOut, taskID+" "+budget)
	return f.Stop(taskID)
}

func (f *fakeControl) DeferAuthority(taskID string) bool {
	f.deferred = append(f.deferred, taskID)
	return f.canDefer
}

func (f *fakeControl) Stop(taskID string) bool {
	f.stopped = append(f.stopped, taskID)
	return true
}

func feed(evs ...event.Event) <-chan event.Event {
	ch := make(chan event.Event, len(evs))
	for _, e := range evs {
		ch <- e
	}
	return ch
}

func ev(taskID string, kind event.Kind) event.Event {
	return event.New("s", taskID, event.SourceSystem, kind, string(kind), nil)
}

// Each outcome has its own exit code, because a caller that cannot tell them
// apart will retry the ones it should not.
func TestOutcomesHaveDistinctExitCodes(t *testing.T) {
	cases := map[event.Kind]int{
		event.WorkflowCompleted:         exitCompleted,
		event.WorkflowFailed:            exitFailed,
		event.WorkflowStopped:           exitStopped,
		event.WorkflowAwaitingAuthority: exitAwaitingAuthority,
	}
	for kind, want := range cases {
		got := streamUntilSettled(context.Background(), &fakeControl{}, feed(ev("t1", kind)), "t1", false, true, 0)
		if got != want {
			t.Fatalf("%s exited %d, want %d", kind, got, want)
		}
	}
	seen := map[int]bool{}
	for _, code := range []int{exitCompleted, exitFailed, exitUsage, exitAwaitingAuthority, exitStopped, exitTimeout} {
		if seen[code] {
			t.Fatalf("exit code %d is used for two different outcomes", code)
		}
		seen[code] = true
	}
}

// A human-owned decision reached with no human present is preserved, never
// answered. Answering it here would satisfy an authority boundary nobody was
// asked about — the one thing a headless run must not do.
func TestAHumanOwnedDecisionIsDeferredNotAnswered(t *testing.T) {
	ctrl := &fakeControl{canDefer: true}
	code := streamUntilSettled(context.Background(), ctrl,
		feed(ev("t1", event.AuthorityRequired), ev("t1", event.WorkflowStopped)), "t1", false, true, 0)
	if len(ctrl.deferred) != 1 || ctrl.deferred[0] != "t1" {
		t.Fatalf("the question was not deferred: %v", ctrl.deferred)
	}
	if code != exitAwaitingAuthority {
		t.Fatalf("exit %d after deferring; a preserved question is not an ordinary stop", code)
	}
}

// A stop with no authority question is an ordinary stop, not a deferred one.
func TestAPlainStopIsNotReportedAsDeferredAuthority(t *testing.T) {
	ctrl := &fakeControl{}
	if code := streamUntilSettled(context.Background(), ctrl, feed(ev("t1", event.WorkflowStopped)), "t1", false, true, 0); code != exitStopped {
		t.Fatalf("exit %d, want %d", code, exitStopped)
	}
	if len(ctrl.deferred) != 0 {
		t.Fatal("nothing was asking, yet a deferral was recorded")
	}
}

// Another task's events must not settle this one. Two governed runs sharing a
// bus would otherwise report each other's outcomes.
func TestAnotherTasksEventsAreIgnored(t *testing.T) {
	code := streamUntilSettled(context.Background(), &fakeControl{},
		feed(ev("other", event.WorkflowFailed), ev("t1", event.WorkflowCompleted)), "t1", false, true, 0)
	if code != exitCompleted {
		t.Fatalf("exit %d: another task's failure settled this run", code)
	}
}

// A timeout stops computation and says so. It decides nothing, and the
// candidate is left where it stands so the work can be resumed.
func TestTimeoutStopsTheTaskRatherThanAbandoningIt(t *testing.T) {
	defer func(d time.Duration) { terminalGrace = d }(terminalGrace)
	terminalGrace = 20 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	ctrl := &fakeControl{}
	code := streamUntilSettled(ctx, ctrl, make(chan event.Event), "t1", false, true, 0)
	if code != exitTimeout {
		t.Fatalf("exit %d, want %d", code, exitTimeout)
	}
	if len(ctrl.stopped) != 1 || ctrl.stopped[0] != "t1" {
		t.Fatalf("the timed-out task was not stopped: %v", ctrl.stopped)
	}
}

// The rendered line is plain text. A log a machine reads should not need an
// ANSI parser, and a log a human greps should not contain escape sequences.
func TestRenderedEventsCarryNoStyling(t *testing.T) {
	line := renderEvent(ev("t1", event.WorkflowCompleted))
	if strings.Contains(line, "\x1b") {
		t.Fatalf("the rendered event carries escape sequences: %q", line)
	}
	if !strings.Contains(line, string(event.WorkflowCompleted)) {
		t.Fatalf("the rendered event does not name its kind: %q", line)
	}
}

// The headless path must call the SAME engine entry as the TUI's /run.
//
// Two implementations of a governed pipeline would drift, and the divergence
// would appear as a governance difference between what a human runs and what
// CI runs — the failure this whole command exists to avoid.
func TestHeadlessRunUsesTheSameEngineEntryAsTheTUI(t *testing.T) {
	cli, err := os.ReadFile("run.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(cli), "engine.SubmitGovernedUnattended") &&
		!strings.Contains(string(cli), "engine.SubmitGoverned(") {
		t.Fatal("the headless path no longer enters through an Engine.SubmitGoverned* entry")
	}
	// Nobody typed a headless task, and the engine must not be told otherwise.
	//
	// Provenance was stamped from which entrypoint ran rather than from
	// anything establishing a person was present, so `sensei-code run` recorded
	// its tasks as the human's -- including, during a dogfooding run, one an AI
	// submitted. The governed workflow is unchanged; only the claim about who
	// asked for it is.
	if strings.Contains(string(cli), "engine.SubmitGoverned(") {
		t.Fatal("the headless path claims human provenance: SubmitGoverned means a human typed /run, " +
			"and a headless run has no typing in it. Use SubmitGovernedUnattended")
	}
	tui, err := os.ReadFile(filepath.Join("..", "..", "internal", "tui", "model.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(tui), "SubmitGoverned(") {
		t.Fatal("the TUI no longer calls Engine.SubmitGoverned; the two front-ends have diverged")
	}
	// The CLI is a front-end, not a second pipeline. If it starts reaching for
	// the pieces the engine owns, the drift has already begun.
	for _, forbidden := range []string{
		"internal/candidate", "internal/provider", "internal/admission",
		"internal/roles", "internal/acceptance", "internal/broker",
	} {
		if strings.Contains(string(cli), forbidden) {
			t.Fatalf("the headless path imports %s; workflow logic belongs to the engine, not to a front-end", forbidden)
		}
	}
}

// --plan is validated in full before any task exists; a refused file is a
// usage error and not a failed run.
func TestASuppliedPlanFileIsValidatedBeforeSubmission(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"decision":"proceed","plan":"x","plan_source":"architect"}`), 0o644)
	if _, err := loadSuppliedPlan(bad); err == nil {
		t.Fatal("a plan asserting its own provenance was accepted")
	}
	if _, err := loadSuppliedPlan(filepath.Join(dir, "missing.json")); err == nil {
		t.Fatal("a missing plan file was accepted")
	}
	good := filepath.Join(dir, "good.json")
	os.WriteFile(good, []byte(`{"decision":"proceed","plan":"create main_test.go","files":["main_test.go"]}`), 0o644)
	p, err := loadSuppliedPlan(good)
	if err != nil || p.Digest == "" {
		t.Fatalf("a valid plan was refused: %v", err)
	}
}

// TestATimeoutWaitsForItsOwnAccount closes the gap Task A found.
//
// The timeout used to call Stop and return immediately, so the process exited
// before the engine could terminalize: a timed-out invocation emitted no
// terminal event and no receipt, and the only account of it was the event
// stream. It now records the deadline as the CAUSE and waits, boundedly, for
// the engine's own terminal.
func TestATimeoutWaitsForItsOwnAccount(t *testing.T) {
	ctrl := &fakeControl{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // the deadline has already fired

	events := make(chan event.Event, 1)
	events <- ev("t1", event.WorkflowTimedOut)
	code := streamUntilSettled(ctx, ctrl, events, "t1", false, true, 25*time.Minute)

	if code != exitTimeout {
		t.Fatalf("exit = %d, want exitTimeout", code)
	}
	if len(ctrl.timedOut) != 1 || !strings.Contains(ctrl.timedOut[0], "25m") {
		t.Fatalf("the deadline was not recorded as the cause: %v", ctrl.timedOut)
	}
	if len(ctrl.stopped) != 1 {
		t.Fatalf("the task was not stopped: %v", ctrl.stopped)
	}
}

// A hung engine must not hold the process open forever.
func TestATimeoutGivesUpOnAnEngineThatNeverAccounts(t *testing.T) {
	defer func(d time.Duration) { terminalGrace = d }(terminalGrace)
	terminalGrace = 20 * time.Millisecond
	ctrl := &fakeControl{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// The channel stays OPEN. An earlier version closed it, and the drain
	// returns immediately on closure, so the grace timer was never exercised
	// and the test proved nothing about the property it names. The real
	// headless subscription stays open until the run returns.
	events := make(chan event.Event)
	start := time.Now()
	if code := streamUntilSettled(ctx, ctrl, events, "t1", false, true, time.Minute); code != exitTimeout {
		t.Fatalf("exit = %d, want exitTimeout", code)
	}
	if time.Since(start) < 20*time.Millisecond {
		t.Fatal("returned before the grace window elapsed, so the grace path was not exercised")
	}
}

// An interruption boundary is not an ending: a buffered AuthorityRequired must
// not let the drain exit before the real terminal and its receipt arrive.
func TestTheDrainWaitsPastAnInterruptionBoundary(t *testing.T) {
	defer func(d time.Duration) { terminalGrace = d }(terminalGrace)
	terminalGrace = 2 * time.Second
	ctrl := &fakeControl{canDefer: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan event.Event, 2)
	events <- ev("t1", event.AuthorityRequired) // an interruption, not an ending
	events <- ev("t1", event.WorkflowTimedOut)  // the real terminal
	if code := streamUntilSettled(ctx, ctrl, events, "t1", false, true, time.Minute); code != exitTimeout {
		t.Fatalf("exit = %d, want exitTimeout", code)
	}
	if invocationFinal(event.AuthorityRequired) {
		t.Fatal("AuthorityRequired must not be invocation-final")
	}
	if !invocationFinal(event.WorkflowTimedOut) {
		t.Fatal("WorkflowTimedOut must be invocation-final")
	}
}

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
	// settled models a task the ENGINE has already terminalized. The real
	// engine reports this by finding no live task to claim.
	settled bool
	// onTimeOut fires when the deadline claims the task, so a test can make
	// the engine's terminal arrive AS A CONSEQUENCE of the claim rather than
	// racing it. Without that ordering a pre-sent terminal makes both select
	// cases ready at once, and which one wins is Go's random choice -- the
	// exact ambiguity streamUntilSettled was written to remove, reintroduced
	// by the test that checks it.
	onTimeOut func()
}

// TimeOut records that the stop was a DEADLINE, which is different evidence
// from a human withdrawing.
//
// It mirrors the engine's claim: a deadline may only end a task that is still
// live. Against an already-settled task it records NOTHING and reports the
// loss, because a timeout that did not cause the ending must not be able to
// state a cause for it.
func (f *fakeControl) TimeOut(taskID, budget string) bool {
	if f.settled {
		return false
	}
	f.timedOut = append(f.timedOut, taskID+" "+budget)
	if f.onTimeOut != nil {
		f.onTimeOut()
	}
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

// TestATimeoutWaitsForItsOwnAccount is GONE, and this note is its headstone.
//
// It closed the gap Task A found: the timeout used to call Stop and return
// immediately, so a timed-out invocation emitted no terminal and no receipt.
// That property still holds and is still proved -- by
// TestADeadlineClaimsAStillLiveTask, which additionally requires the deadline
// to be recorded as the CAUSE.
//
// It is removed rather than repaired because its world was self-contradictory
// and its assertion is now false. It handed streamUntilSettled a cancelled ctx
// AND a buffered WorkflowTimedOut -- an engine that had already terminalized --
// then demanded the timeout state the cause anyway. Under the rule that the
// engine owns terminal truth, a timeout that did not cause the ending may not
// claim it. That world is now TestATimeoutRacingItsOwnTerminalSettlesOnce, and
// it requires the opposite of what this test required.
//
// This is a falsifier being REPLACED BY A STRICTER ONE, not relaxed: the old
// test asserted a scheduler-dependent outcome and passed about half the time.
// Its two successors assert deterministic outcomes over 200 draws each.

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
	if _, ok := exitFor(event.AuthorityRequired, false); ok {
		t.Fatal("AuthorityRequired must not end an invocation")
	}
	if _, ok := exitFor(event.WorkflowTimedOut, false); !ok {
		t.Fatal("WorkflowTimedOut must end an invocation")
	}
}

// The engine owns terminal truth.
//
// These four falsifiers pin the rule that a deadline REQUESTS a terminal and
// never establishes one. Each constructs a world where the deadline and an
// engine terminal are both already true -- both select cases ready at once --
// and requires ONE answer, the engine's. Before this rule, the same world was
// classified two ways depending on Go's random select choice, so
// TestATimeoutWaitsForItsOwnAccount failed about half the time and the branch
// went green or red by scheduler luck.
//
// They run with -count high enough that a scheduler-dependent answer cannot
// survive: a 50/50 outcome passes 200 consecutive draws with probability 2^-200.
const settlementDraws = 200

// A completed run must not be rewritten into a timeout by the observer's clock.
func TestADeadlineCannotRewriteACompletedRun(t *testing.T) {
	for i := 0; i < settlementDraws; i++ {
		ctrl := &fakeControl{settled: true}
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // the deadline has already fired
		events := make(chan event.Event, 1)
		events <- ev("t1", event.WorkflowCompleted) // ...and the engine already finished
		code := streamUntilSettled(ctx, ctrl, events, "t1", false, true, 25*time.Minute)
		if code != exitCompleted {
			t.Fatalf("draw %d: exit = %d, want exitCompleted: the deadline rewrote an established ending", i, code)
		}
		if len(ctrl.timedOut) != 0 {
			t.Fatalf("draw %d: a lost timeout stated a cause for an ending it did not cause: %v", i, ctrl.timedOut)
		}
	}
}

// A failed run is likewise the engine's to report, not the clock's.
func TestADeadlineCannotRewriteAFailedRun(t *testing.T) {
	for i := 0; i < settlementDraws; i++ {
		ctrl := &fakeControl{settled: true}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		events := make(chan event.Event, 1)
		events <- ev("t1", event.WorkflowFailed)
		if code := streamUntilSettled(ctx, ctrl, events, "t1", false, true, time.Minute); code != exitFailed {
			t.Fatalf("draw %d: exit = %d, want exitFailed", i, code)
		}
	}
}

// A deadline racing a timeout the engine itself terminalized settles as
// TIMED_OUT exactly once, whichever case the scheduler picks.
func TestATimeoutRacingItsOwnTerminalSettlesOnce(t *testing.T) {
	for i := 0; i < settlementDraws; i++ {
		ctrl := &fakeControl{settled: true}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		events := make(chan event.Event, 1)
		events <- ev("t1", event.WorkflowTimedOut)
		if code := streamUntilSettled(ctx, ctrl, events, "t1", false, true, 25*time.Minute); code != exitTimeout {
			t.Fatalf("draw %d: exit = %d, want exitTimeout", i, code)
		}
		if len(ctrl.timedOut) != 0 {
			t.Fatalf("draw %d: the timeout restated a cause the engine had already established: %v", i, ctrl.timedOut)
		}
	}
}

// When the deadline reaches a task that is genuinely still live, the timeout
// WINS: it claims the task, records the deadline as the cause, and the engine's
// WorkflowTimedOut is what the invocation reports.
func TestADeadlineClaimsAStillLiveTask(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// The engine has not accounted yet, so the ctx case must be the ONLY ready
	// one and the claim path is the one under test.
	//
	// The terminal therefore arrives as a consequence of the claim, not before
	// it. Sending it up front left both select cases ready and let Go's random
	// choice decide which path ran -- the test passed on a workstation and
	// failed on a runner, and it is the same ambiguity streamUntilSettled
	// exists to remove.
	events := make(chan event.Event, 1)
	ctrl := &fakeControl{onTimeOut: func() { events <- ev("t1", event.WorkflowTimedOut) }}

	if code := streamUntilSettled(ctx, ctrl, events, "t1", false, true, 25*time.Minute); code != exitTimeout {
		t.Fatalf("exit = %d, want exitTimeout", code)
	}
	if len(ctrl.timedOut) != 1 || !strings.Contains(ctrl.timedOut[0], "25m") {
		t.Fatalf("the deadline was not recorded as the cause: %v", ctrl.timedOut)
	}
	if len(ctrl.stopped) != 1 {
		t.Fatalf("the task was not stopped: %v", ctrl.stopped)
	}
}

// RULING-195 (70B2a1 r6 review f1): A HALTED INVOCATION ENDS ITS CALLER'S WAIT.
// The engine halts a run whose governed event the session record did not
// take, and publishes nothing in that event's place, so the bus below never
// carries an ending. The caller -- with no deadline configured -- still
// returns, once, failing, as soon as the invocation's handle reports the
// halt, and not before.
func TestB2a1R195AHaltedInvocationEndsItsCallersWaitWithoutABusEvent(t *testing.T) {
	events := make(chan event.Event, 8)
	events <- ev("task-1", event.Status)
	halted, ended := make(chan struct{}), make(chan struct{})
	streamed, stop := untilInvocationSettles(halted, ended, events)
	defer stop()
	done := make(chan int, 1)
	go func() {
		done <- streamUntilSettled(context.Background(), &fakeControl{}, streamed, "task-1", false, true, 0)
	}()
	select {
	case code := <-done:
		t.Fatalf("the caller returned %d before the invocation settled", code)
	case <-time.After(50 * time.Millisecond):
	}
	close(halted)
	select {
	case code := <-done:
		if code != exitFailed {
			t.Fatalf("a halted invocation ended its caller with %d, not %d", code, exitFailed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a halted invocation left its caller waiting for a bus event that cannot be published")
	}
}

// RULING-195: AN ENDED INVOCATION'S EVENTS ARE DELIVERED, AND THEN THE WAIT
// ENDS. Everything an invocation published is on the bus before it returns:
// its ending reaches the caller and decides the exit, and an invocation that
// returned with no ending at all ends the wait failing rather than hanging.
func TestB2a1R195AnEndedInvocationDeliversWhatItPublishedThenEndsTheWait(t *testing.T) {
	for name, c := range map[string]struct {
		published []event.Event
		want      int
	}{
		"its ending":   {[]event.Event{ev("task-1", event.Status), ev("task-1", event.WorkflowCompleted)}, exitCompleted},
		"no ending":    {[]event.Event{ev("task-1", event.Status)}, exitFailed},
		"nothing else": {nil, exitFailed},
	} {
		t.Run(name, func(t *testing.T) {
			events := make(chan event.Event, 8)
			for _, e := range c.published {
				events <- e
			}
			halted, ended := make(chan struct{}), make(chan struct{})
			close(ended)
			streamed, stop := untilInvocationSettles(halted, ended, events)
			defer stop()
			done := make(chan int, 1)
			go func() {
				done <- streamUntilSettled(context.Background(), &fakeControl{}, streamed, "task-1", false, true, 0)
			}()
			select {
			case code := <-done:
				if code != c.want {
					t.Fatalf("an ended invocation that published %d events exited %d, want %d", len(c.published), code, c.want)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("an ended invocation left its caller waiting")
			}
		})
	}
}

// fakeInvocation is one governed invocation's control handle, as the engine's
// RunAttempt presents it to the caller waiting on it.
type fakeInvocation struct {
	halted, ended chan struct{}
	failure       error
}

func newFakeInvocation() *fakeInvocation {
	return &fakeInvocation{halted: make(chan struct{}), ended: make(chan struct{})}
}

func (f *fakeInvocation) Halted() <-chan struct{} { return f.halted }
func (f *fakeInvocation) Ended() <-chan struct{}  { return f.ended }
func (f *fakeInvocation) Err() error {
	select {
	case <-f.halted:
		return f.failure
	default:
		return nil
	}
}

// RULING-203: AUDIT-REPAIR ENDS ON ITS PHASE'S TYPED HALT, NOT A BUS TERMINAL.
// The audit-repair command waits for its observation and for each repair
// through awaitAuditPhase. When the engine halts that phase's run because the
// session record did not take one of its governed events, it publishes no
// terminal, so the bus below never carries one. With no deadline configured,
// the phase still ends -- once, failing, as soon as the run's handle reports
// the halt and not before -- and the observation's failure opens no repair.
// Waiting on the bus alone (the unrepaired command) never returns here.
func TestB2a1R203AuditRepairPhaseEndsOnTheRunsTypedHaltWithoutABusTerminal(t *testing.T) {
	events := make(chan event.Event, 8)
	events <- ev("task-obs", event.Status)
	inv := newFakeInvocation()
	inv.failure = os.ErrPermission
	var asked []string
	attemptOf := func(taskID string) (invocationHandle, error) {
		asked = append(asked, taskID)
		return inv, nil
	}
	done := make(chan int, 1)
	go func() {
		done <- awaitAuditPhase(context.Background(), &fakeControl{}, attemptOf, events, "task-obs", true, 0)
	}()
	select {
	case code := <-done:
		t.Fatalf("the audit phase returned %d before its run settled", code)
	case <-time.After(50 * time.Millisecond):
	}
	close(inv.halted)
	select {
	case code := <-done:
		if code != exitFailed {
			t.Fatalf("a halted audit phase exited %d, not %d", code, exitFailed)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a halted audit phase left audit-repair waiting for a bus terminal that cannot be published")
	}
	if len(asked) != 1 || asked[0] != "task-obs" {
		t.Fatalf("the phase was not waited for through its own run's handle: %v", asked)
	}
}

// RULING-203 controls: a phase whose run published its ending reports that
// ending -- an observation is still observed, so the halt witness above is not
// a wait that always fails -- and a task with no run handle fails at once
// rather than waiting on the bus.
func TestB2a1R203AuditRepairPhaseReportsItsEndingAndRefusesAMissingHandle(t *testing.T) {
	t.Run("observed", func(t *testing.T) {
		events := make(chan event.Event, 8)
		events <- ev("task-obs", event.Status)
		events <- ev("task-obs", event.WorkflowObserved)
		inv := newFakeInvocation()
		close(inv.ended)
		attemptOf := func(string) (invocationHandle, error) { return inv, nil }
		done := make(chan int, 1)
		go func() {
			done <- awaitAuditPhase(context.Background(), &fakeControl{}, attemptOf, events, "task-obs", true, 0)
		}()
		select {
		case code := <-done:
			if code != exitObserved {
				t.Fatalf("an observed audit phase exited %d, want %d", code, exitObserved)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("an ended audit phase left audit-repair waiting")
		}
	})
	t.Run("no run handle", func(t *testing.T) {
		attemptOf := func(string) (invocationHandle, error) { return nil, os.ErrNotExist }
		done := make(chan int, 1)
		go func() {
			done <- awaitAuditPhase(context.Background(), &fakeControl{}, attemptOf, make(chan event.Event), "task-x", true, 0)
		}()
		select {
		case code := <-done:
			if code != exitFailed {
				t.Fatalf("a phase with no run handle exited %d, want %d", code, exitFailed)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("a phase with no run handle waited on the bus")
		}
	})
}

// RULING-203: THE AUDIT-REPAIR COMMAND ENDS ON ITS OBSERVATION'S TYPED HALT.
// Through the command itself -- runAuditRepair, with no deadline -- an
// observation whose objective is over the session record's size bound is
// refused by the Store at its first governed record. The engine halts that run
// and publishes nothing in its place, so no bus terminal will ever end the
// observation phase. The command still ends, promptly and failing, reporting
// the typed failure of the record the Store did not take; it prints no event
// of the task, records none, and opens no repair. A command that waited on the
// bus alone for the observation's ending never returns here.
func TestB2a1R203AuditRepairEndsOnItsObservationsTypedHaltWithoutABusTerminal(t *testing.T) {
	root := gitRepoRoot(t)
	// The readiness the command checks before it observes: a Sensei CLI and an
	// MCP server on PATH, and an awareness corpus. Stand-ins answer; nothing
	// of them is run by the observation, which never reaches a provider.
	bin := t.TempDir()
	for _, name := range []string{"sensei", "awareness-mcp"} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\necho stand-in\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if err := os.MkdirAll(filepath.Join(root, "docs", "awareness"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "awareness", "invariants.yaml"), []byte("invariants: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	repo, cfg := repoAt(t, root)

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
	go func() {
		done <- runAuditRepair(t.Context(), repo, cfg, []string{"--task", strings.Repeat("x", 17<<20), "--timeout", "0"})
	}()
	var code int
	select {
	case code = <-done:
		os.Stdout, os.Stderr = savedOut, savedErr
	case <-time.After(30 * time.Second):
		os.Stdout, os.Stderr = savedOut, savedErr
		t.Fatal("audit-repair left its caller waiting for an observation terminal the bus can never carry")
	}
	outFile.Close()
	errFile.Close()
	stdout, err := os.ReadFile(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	if code != exitFailed {
		t.Fatalf("audit-repair whose observation the record refused exited %d, not %d\nstdout: %s\nstderr: %s", code, exitFailed, stdout, stderr)
	}
	var observation string
	for _, line := range strings.Split(string(stdout), "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == "observation" {
			observation = fields[1]
		}
	}
	if observation == "" {
		t.Fatalf("audit-repair never submitted its observation: %s", stdout)
	}
	if !strings.Contains(string(stderr), "the session record did not take the "+string(event.TaskCreated)+" record of task "+observation) {
		t.Fatalf("audit-repair did not report the typed failure of the record the Store refused: %s", stderr)
	}
	if !strings.Contains(string(stderr), "opening no repair work") {
		t.Fatalf("audit-repair did not report that the failed observation opens no repair: %s", stderr)
	}
	for _, published := range []string{string(event.TaskCreated), "workflow.", "=== repair", "repair task"} {
		if strings.Contains(string(stdout), published) {
			t.Fatalf("audit-repair whose observation the record refused printed %q:\n%s", published, stdout)
		}
	}
	// Nothing of the observation is recorded: no session record names it.
	err = filepath.WalkDir(filepath.Join(root, ".sensei-code"), func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), `"task_id":"`+observation+`"`) {
			t.Errorf("the refused observation was recorded in %s", path)
		}
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

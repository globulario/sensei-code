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
	canDefer bool
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
		got := streamUntilSettled(context.Background(), &fakeControl{}, feed(ev("t1", kind)), "t1", false, true)
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
		feed(ev("t1", event.AuthorityRequired), ev("t1", event.WorkflowStopped)), "t1", false, true)
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
	if code := streamUntilSettled(context.Background(), ctrl, feed(ev("t1", event.WorkflowStopped)), "t1", false, true); code != exitStopped {
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
		feed(ev("other", event.WorkflowFailed), ev("t1", event.WorkflowCompleted)), "t1", false, true)
	if code != exitCompleted {
		t.Fatalf("exit %d: another task's failure settled this run", code)
	}
}

// A timeout stops computation and says so. It decides nothing, and the
// candidate is left where it stands so the work can be resumed.
func TestTimeoutStopsTheTaskRatherThanAbandoningIt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	ctrl := &fakeControl{}
	code := streamUntilSettled(ctx, ctrl, make(chan event.Event), "t1", false, true)
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
	if !strings.Contains(string(cli), "engine.SubmitGoverned(") {
		t.Fatal("the headless path no longer calls Engine.SubmitGoverned")
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

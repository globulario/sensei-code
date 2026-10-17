//go:build linux

package workflow

// RULING-374 §13 pre-fix control N1-C1. A bound invocation reaches an in-run
// containment gate (the planned lane's identity load, implement's entry, or
// the question lane's resumePrecondition); the record lock cannot be taken
// there, so the gate refuses before any workspace execution; the lock then
// becomes available again BEFORE terminateRun's own locked read, which reads
// the record settled. A later successful observation does not retroactively
// authorize the refused operation (§3-§4): the invocation must end on the
// containment refusal -- the existing WorkflowWorkspaceQuarantined
// InvocationTerminal -- never on WorkflowFailed with a behavioural FAILURE.
//
// Each control asserts that SAFE property, so each fails on db5d318.
//
//	N1-C1   end to end: ResumeTask on the planned specimen; the admission sees
//	        a free lock and binds; the Sensei start then holds the record lock
//	        (another open file description, as another process would) past
//	        the planned lane's bound, and releases it while terminateRun's
//	        read is still waiting -- unavailable at the gate, available at
//	        the classifier.
//	N1-C1h  the same temporal change made deterministic at each in-run gate:
//	        the gate's own error is taken while the lock object is invalid,
//	        the lock object is repaired, and terminateRun is handed that error.
//	N1-C1q  the typed quarantine refusal: the gate refuses over an execution
//	        recorded in flight, the record is settled before the classifier
//	        reads it, and the original refusal must still be the ending.
//
// This file uses only API that exists at db5d318, so it is the same file
// before and after the fix.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/behavioral"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
)

// r374Behaviour is a behavioural record that reports every outcome status it
// is sent.
func r374Behaviour(t *testing.T) (behavioral.Config, <-chan string) {
	t.Helper()
	statuses := make(chan string, 16)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Status string `json:"status"`
				} `json:"arguments"`
			} `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.Params.Name == "behavioral_record_outcome" {
			statuses <- req.Params.Arguments.Status
		}
		w.Header().Set("Mcp-Session-Id", "sess-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	t.Cleanup(srv.Close)
	return behavioral.Config{Enabled: true, URL: srv.URL, Project: "sensei-code", Domain: "d"}, statuses
}

// r374NoBehaviouralFailure fails when the behavioural record was told the task
// failed.
func r374NoBehaviouralFailure(t *testing.T, statuses <-chan string, within time.Duration) {
	t.Helper()
	deadline := time.After(within)
	for {
		select {
		case got := <-statuses:
			if got == behaviourFailure {
				t.Errorf("a containment refusal was reported to the behavioural record as %q", got)
				return
			}
		case <-deadline:
			return
		}
	}
}

// r374AssertContainmentEnding holds the ending N1 requires: the containment
// terminal, never WorkflowFailed.
func r374AssertContainmentEnding(t *testing.T, seen []event.Event) {
	t.Helper()
	if contains(seen, event.WorkflowFailed) {
		for _, ev := range seen {
			if ev.Kind == event.WorkflowFailed {
				t.Errorf("a refused containment admission ended the task WorkflowFailed after a later read saw the record "+
					"settled: %s", ev.Summary)
			}
		}
	}
	if !contains(seen, wqKind) {
		t.Errorf("the invocation did not end on the containment terminal: %v", kinds(seen))
	}
}

// r374Hold is how long N1-C1's other holder keeps the record lock: longer than
// the planned lane's bounded wait (30s), shorter than that wait plus the
// classifier's own bounded wait.
const r374Hold = 40 * time.Second

// N1-C1. End to end on the planned lane, with the temporal change.
func TestR374N1C1InRunLockContentionIsNotATaskFailure(t *testing.T) {
	s := r373PlannedSpecimen(t)
	cfg, statuses := r374Behaviour(t)
	s.e.Config.Behavioral = cfg
	dir := t.TempDir()
	started, ack := filepath.Join(dir, "sensei-started"), filepath.Join(dir, "lock-held")
	// The Sensei start runs after the admission (which saw a free lock) and
	// before the planned lane's containment check; it waits until the other
	// holder has the lock.
	s.e.Config.Sensei.Args = []string{"-c", "touch '" + started + "'; while [ ! -e '" + ack + "' ]; do sleep 0.05; done\n" +
		s.e.Config.Sensei.Args[1]}
	lock := r373LockPath(s.repoRoot, r373Task)
	tree := r373Tree(t, s.worktree)
	held, released := make(chan time.Time, 1), make(chan time.Time, 1)
	go func() {
		for deadline := time.Now().Add(90 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				close(held)
				close(released)
				return
			}
		}
		f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			close(held)
			close(released)
			return
		}
		defer f.Close()
		for syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
			time.Sleep(5 * time.Millisecond)
		}
		held <- time.Now()
		_ = os.WriteFile(ack, nil, 0o600)
		time.Sleep(r374Hold)
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		released <- time.Now()
	}()
	b, _, seen := r373Resume(t, s.e, s.task, 180*time.Second)
	ended := time.Now()
	heldAt, ok := <-held
	if !ok {
		t.Fatal("premise: the Sensei start never signalled, so the lock was never taken")
	}
	releasedAt, ok := <-released
	if !ok {
		t.Fatal("premise: the lock was never released")
	}
	t.Logf("lock held at +0s, released at +%s, invocation ended at +%s", releasedAt.Sub(heldAt), ended.Sub(heldAt))
	// Premises: the admission was lawful; the in-run gate refused before any
	// workspace execution, on the lock; the record reads settled afterwards.
	if b.Refusal != nil {
		t.Fatalf("premise: the admission saw a free lock and bound: %v", b.Refusal)
	}
	if n := s.impl.calls.Load(); n != 0 {
		t.Fatalf("premise: the implementer was reached %d time(s)", n)
	}
	if r373Tree(t, s.worktree) != tree {
		t.Fatal("premise: the worktree was touched")
	}
	timedOut := false
	for _, ev := range seen {
		if strings.Contains(ev.Summary, "not released in time") || strings.Contains(string(ev.Payload), "not released in time") {
			timedOut = true
		}
	}
	if !timedOut {
		t.Fatalf("premise: the in-run gate did not refuse on the held lock: %v", summaries(seen))
	}
	if id, ok, err := candidate.LoadLocked(s.repoRoot, r373Task); err != nil || !ok || id.Unsettled() {
		t.Fatalf("premise: the record reads settled under its lock afterwards: ok=%v err=%v", ok, err)
	}
	// Required.
	r374AssertContainmentEnding(t, seen)
	r374NoBehaviouralFailure(t, statuses, 2*time.Second)
}

// r374Sites are the in-run containment gates, each returning the error it
// refuses with over wqRig's record.
var r374Sites = map[string]func(t *testing.T, e *Engine, root string) error{
	"planned lane (containmentCheck)": func(t *testing.T, e *Engine, root string) error {
		_, _, err := containmentCheck(root, wqTask)
		return err
	},
	"implement entry": func(t *testing.T, e *Engine, root string) error {
		return r373Implement(t, e, root)
	},
	"question lane (resumePrecondition)": func(t *testing.T, e *Engine, root string) error {
		return e.resumePrecondition(fixtureCtx(e, wqTask), wqTask)
	},
}

// N1-C1h. The gate's refusal on an invalid lock object, the lock repaired,
// then the classifier: deterministic at every in-run gate.
func TestR374N1C1hARepairedLockDoesNotTurnTheGateRefusalIntoAFailure(t *testing.T) {
	for name, site := range r374Sites {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			cfg, statuses := r374Behaviour(t)
			e.Config.Behavioral = cfg
			r371Damage["invalid lock object"](t, root)
			refusal := site(t, e, root)
			if !errors.Is(refusal, ErrContainmentUnestablished) {
				t.Fatalf("premise: the gate refused on the lock: %v", refusal)
			}
			if err := os.Remove(r371RecordPath(root) + ".lock"); err != nil {
				t.Fatal(err)
			}
			if id, ok, err := candidate.LoadLocked(root, wqTask); err != nil || !ok || id.Unsettled() {
				t.Fatalf("premise: the repaired record reads settled under its lock: ok=%v err=%v", ok, err)
			}
			drainEvents(events)
			e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", refusal)
			r374AssertContainmentEnding(t, drainEvents(events))
			r374NoBehaviouralFailure(t, statuses, time.Second)
		})
	}
}

// N1-C1q. The gate's typed quarantine refusal, the record settled before the
// classifier reads it.
func TestR374N1C1qASettledRereadDoesNotTurnTheQuarantineRefusalIntoAFailure(t *testing.T) {
	e, events, _, root := wqRig(t)
	cfg, statuses := r374Behaviour(t)
	e.Config.Behavioral = cfg
	wqInFlight(t, root, wqTask, "exec-r374", filepath.Join(root, "wt"), "")
	_, _, refusal := containmentCheck(root, wqTask)
	var q *candidate.ErrQuarantined
	if !errors.As(refusal, &q) {
		t.Fatalf("premise: the gate refused on the record's quarantine: %v", refusal)
	}
	if _, err := candidate.SettleExecution(root, wqTask, "exec-r374"); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := candidate.LoadLocked(root, wqTask); err != nil || !ok || id.Unsettled() {
		t.Fatalf("premise: the record reads settled under its lock: ok=%v err=%v", ok, err)
	}
	drainEvents(events)
	e.terminateRun(context.WithoutCancel(fixtureCtx(e, wqTask)), wqTask, "the objective", refusal)
	r374AssertContainmentEnding(t, drainEvents(events))
	r374NoBehaviouralFailure(t, statuses, time.Second)
}

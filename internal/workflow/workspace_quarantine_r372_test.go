//go:build linux

package workflow

// RULING-372 §14-§18 and §23 (engine half). On a platform with no
// cross-process lock, candidate.LoadLocked fails with the typed
// candidate.ErrNoCrossProcessLock. Because P1's BeginExecution refuses there,
// no required confined execution can have started, so an ordinary error,
// cancellation or timeout keeps its ordinary outcome when an ordinary
// candidate.Load shows the record absent or readable and settled. A lockless
// Load that shows the record unsettled, or that cannot read it, ends on the
// UNVERIFIED quarantine.
//
// Linux has the lock, and the engine has no LoadLocked seam (§17: no new
// production shim). So these witnesses hand the typed lockless read result to
// terminateOnLockedRead, the private helper terminateQuarantined passes its
// one locked read to. Where the helper returns false, the invocation goes on to
// terminateRun's ordinary classifier. These tests reach that classifier over
// the same specimen through terminateRun (on Linux the real locked read there
// shows the same absent or settled record), which yields the ordinary terminal.
// This file depends on the helper, so it has no pre-fix (a7b41bd) control
// run. The a7b41bd behaviour it corrects is the r371 C1-C4 control: a failed
// locked read under an ordinary ending was never decided at all.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/validation"
)

// r372Ending builds the context and error terminateRun would receive for an
// ending, as r371End does.
func r372Ending(e *Engine, end r371Ending) (context.Context, error) {
	ctx := fixtureCtx(e, wqTask)
	err := error(errors.New(r371Err))
	if end.timedOut {
		e.mu.Lock()
		e.timedOut = map[string]bool{wqTask: true}
		e.mu.Unlock()
	}
	if end.cancel {
		c, cancel := context.WithCancel(ctx)
		cancel()
		ctx, err = c, c.Err()
	}
	return ctx, err
}

// r372Lockless decides an ending over the lockless read result: LoadLocked
// failed with the typed candidate.ErrNoCrossProcessLock.
func r372Lockless(e *Engine, ctx context.Context, err error) bool {
	return e.terminateOnLockedRead(ctx, wqTask, err, candidate.Identity{}, false, candidate.ErrNoCrossProcessLock)
}

// r372Specimens are the records the lockless witnesses read through an
// ordinary candidate.Load.
var r372Specimens = map[string]func(t *testing.T, root string){
	"no record": func(t *testing.T, root string) {
		if err := os.Remove(r371RecordPath(root)); err != nil {
			t.Fatal(err)
		}
	},
	"settled record": func(*testing.T, string) {},
	"unsettled record": func(t *testing.T, root string) {
		wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	},
	"unreadable record": func(t *testing.T, root string) {
		if err := os.WriteFile(r371RecordPath(root), []byte("{torn"), 0o644); err != nil {
			t.Fatal(err)
		}
	},
}

// r372AssertOrdinary is L1-L4: the lockless read decides nothing, emits
// nothing, and the invocation keeps its ordinary terminal over the same
// specimen.
func r372AssertOrdinary(t *testing.T, specimen, ending string, want event.Kind) {
	t.Helper()
	e, events, _, root := wqRig(t)
	r372Specimens[specimen](t, root)
	drainEvents(events)
	ctx, err := r372Ending(e, r371Endings[ending])
	if r372Lockless(e, ctx, err) {
		t.Fatalf("a lockless %s under %s was decided as a quarantine: %v", specimen, ending, summaries(drainEvents(events)))
	}
	if seen := drainEvents(events); len(seen) != 0 {
		t.Fatalf("the lockless read that decided nothing emitted %v", summaries(seen))
	}
	e.terminateRun(ctx, wqTask, "the objective", err)
	seen := drainEvents(events)
	if contains(seen, wqKind) || !contains(seen, want) {
		t.Fatalf("a lockless %s under %s did not keep %s: %v", specimen, ending, want, summaries(seen))
	}
}

// L1. No record and an ordinary error: the ordinary failure.
func TestR372L1LocklessNoRecordOrdinaryErrorIsAnOrdinaryFailure(t *testing.T) {
	r372AssertOrdinary(t, "no record", "ordinary error", event.WorkflowFailed)
}

// L2. A readable settled record and an ordinary error: the ordinary failure.
func TestR372L2LocklessSettledOrdinaryErrorIsAnOrdinaryFailure(t *testing.T) {
	r372AssertOrdinary(t, "settled record", "ordinary error", event.WorkflowFailed)
}

// L3. No record and a cancellation: the ordinary stop.
func TestR372L3LocklessNoRecordCancellationIsAnOrdinaryStop(t *testing.T) {
	r372AssertOrdinary(t, "no record", "caller stop", event.WorkflowStopped)
}

// L4. No record and an expired budget: the ordinary timeout.
func TestR372L4LocklessNoRecordTimeoutIsAnOrdinaryTimeout(t *testing.T) {
	r372AssertOrdinary(t, "no record", "budget expired", event.WorkflowTimedOut)
}

// L5 and L6. A lockless Load that shows the record unsettled, or that cannot
// read it, ends on the UNVERIFIED quarantine for every ending. The terminal
// claims nothing the unlocked read showed, because that read is legacy
// visibility and not a locked observation. The lockless refusal is the
// reason, and the initiating error is context only.
func TestR372L5L6LocklessUnsettledOrUnreadableIsAnUnverifiedQuarantine(t *testing.T) {
	for _, specimen := range []string{"unsettled record", "unreadable record"} {
		for ename, end := range r371Endings {
			t.Run(specimen+"/"+ename, func(t *testing.T) {
				e, events, _, root := wqRig(t)
				r372Specimens[specimen](t, root)
				recordBefore, _ := os.ReadFile(r371RecordPath(root))
				drainEvents(events)
				ctx, err := r372Ending(e, end)
				if !r372Lockless(e, ctx, err) {
					t.Fatalf("a lockless %s under %s was not decided", specimen, ename)
				}
				seen := drainEvents(events)
				p, terminal := r371AssertUnverified(t, seen, candidate.ErrNoCrossProcessLock.Error())
				if strings.Contains(p["reason"].(string), err.Error()) || !strings.Contains(terminal.Summary, err.Error()) {
					t.Fatalf("the initiating %q is not kept apart as context:\nsummary %q\nreason  %q", err, terminal.Summary, p["reason"])
				}
				if end.note != "" && !strings.Contains(terminal.Summary, end.note) {
					t.Fatalf("the %s is not kept in the account: %q", ename, terminal.Summary)
				}
				if after, _ := os.ReadFile(r371RecordPath(root)); string(after) != string(recordBefore) {
					t.Fatal("the candidate record was written")
				}
			})
		}
	}
}

// A workspace safety stop is itself evidence of unresolved containment, so the
// lockless exception does not reach it. It is decided UNVERIFIED even over a
// record the unlocked read shows settled.
func TestR372LocklessExceptionDoesNotReachASafetyStop(t *testing.T) {
	e, events, _, _ := wqRig(t)
	drainEvents(events)
	stop := &WorkspaceUnsettled{TaskID: wqTask, Phase: "certify", Invocation: "exec-1"}
	if !e.terminateOnLockedRead(fixtureCtx(e, wqTask), wqTask, stop, candidate.Identity{}, false, candidate.ErrNoCrossProcessLock) {
		t.Fatal("a safety stop over a lockless settled read was not decided")
	}
	r371AssertUnverified(t, drainEvents(events), candidate.ErrNoCrossProcessLock.Error())
}

// L7. A required confined execution refuses before any execution where the
// platform has no cross-process lock: BeginExecution's refusal is typed
// not-started, nothing runs, and the record is not written. The lockless
// read of that same refusal over the settled record decides nothing, and the
// invocation keeps the ordinary unsupported-confinement failure.
func TestR372L7RequiredConfinementRefusesBeforeAnyExecution(t *testing.T) {
	e, events, _, root := wqRig(t)
	injectPreStart(t, func(int, string, string, candidate.InFlight) (candidate.Identity, error, bool) {
		return candidate.Identity{}, candidate.ErrNoCrossProcessLock, true
	})
	before := wqRecordBytes(t, root, wqTask)
	ran := false
	runner := validation.Runner{Confine: &validation.Confinement{Profile: "p", Command: "c", RequireStatus: true}}
	err := e.boundedRun(fixtureCtx(e, wqTask), wqTask, "certify", filepath.Join(root, "wt"), runner,
		func(string) []validation.Evidence { ran = true; return nil }, func() string { return "" })
	if ran {
		t.Fatal("L7: a required confined execution started without the P1 boundary")
	}
	if !confinementUnsupportedBeforeStart(err) {
		t.Fatalf("L7: the refusal is not the typed not-started unsupported confinement: %v", err)
	}
	if after := wqRecordBytes(t, root, wqTask); after != before {
		t.Fatal("L7: the refusal wrote the candidate record")
	}
	drainEvents(events)
	if r372Lockless(e, fixtureCtx(e, wqTask), err) {
		t.Fatalf("L7: the pre-start refusal over a settled record was quarantined: %v", summaries(drainEvents(events)))
	}
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", err)
	r367Ordinary(t, drainEvents(events))
}

// r372Status is the containment status one projection reports: the same
// letters the CLI matrix (cmd/sensei-code/workspace_quarantine_r371_test.go)
// uses, so the two halves describe one status per specimen (L8, §23).
type r372Status string

const (
	r372Ordinary   r372Status = "ordinary"
	r372Verified   r372Status = "verified quarantine"
	r372Unverified r372Status = "unverified quarantine"
)

// §23, engine projection. Every row is decided once and the terminal it ends
// on is named. Rows G-L hand the typed lockless read to terminateOnLockedRead.
// An ordinary row then ends through terminateRun over the same specimen.
func TestR372S23TheEngineMatrix(t *testing.T) {
	invalidLock := func(t *testing.T, root string) {
		lock := r371RecordPath(root) + ".lock"
		_ = os.Remove(lock)
		if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
			t.Fatal(err)
		}
	}
	rows := []struct {
		row      string
		setup    func(t *testing.T, root string)
		lockless bool
		ending   string
		err      func() error
		want     r372Status
		terminal event.Kind
	}{
		{"A torn record", r371Damage["torn record"], false, "ordinary error", nil, r372Unverified, wqKind},
		{"B contradictory embedded identity", r371Damage["contradictory embedded identity"], false, "ordinary error", nil, r372Unverified, wqKind},
		{"C invalid lock and no record", func(t *testing.T, root string) { r372Specimens["no record"](t, root); invalidLock(t, root) },
			false, "ordinary error", nil, r372Unverified, wqKind},
		{"D valid lock and no record", r372Specimens["no record"], false, "ordinary error", nil, r372Ordinary, event.WorkflowFailed},
		{"E readable settled", r372Specimens["settled record"], false, "ordinary error", nil, r372Ordinary, event.WorkflowFailed},
		{"F readable unsettled", r372Specimens["unsettled record"], false, "ordinary error", nil, r372Verified, wqKind},
		{"G lockless ordinary error", r372Specimens["settled record"], true, "ordinary error", nil, r372Ordinary, event.WorkflowFailed},
		{"H lockless cancellation", r372Specimens["no record"], true, "caller stop", nil, r372Ordinary, event.WorkflowStopped},
		{"I lockless timeout", r372Specimens["no record"], true, "budget expired", nil, r372Ordinary, event.WorkflowTimedOut},
		{"J lockless unsettled", r372Specimens["unsettled record"], true, "ordinary error", nil, r372Unverified, wqKind},
		{"K lockless unreadable", r372Specimens["unreadable record"], true, "ordinary error", nil, r372Unverified, wqKind},
		{"L required confined pre-start refusal", r372Specimens["settled record"], true, "ordinary error", r367Unsupported, r372Ordinary, event.WorkflowFailed},
	}
	for _, r := range rows {
		t.Run(r.row, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			r.setup(t, root)
			drainEvents(events)
			ctx, err := r372Ending(e, r371Endings[r.ending])
			if r.err != nil {
				err = r.err()
			}
			if r.lockless {
				if r372Lockless(e, ctx, err) != (r.want != r372Ordinary) {
					t.Fatalf("the lockless read decided wrongly for %s: %v", r.want, summaries(drainEvents(events)))
				}
				if r.want == r372Ordinary {
					e.terminateRun(ctx, wqTask, "the objective", err)
				}
			} else {
				e.terminateRun(ctx, wqTask, "the objective", err)
			}
			seen := drainEvents(events)
			if !contains(seen, r.terminal) {
				t.Fatalf("want terminal %s: %v", r.terminal, summaries(seen))
			}
			got := r372Ordinary
			if contains(seen, wqKind) {
				p, _ := wqPayload(t, seen)
				got = r372Unverified
				if p["recorded"] == true {
					got = r372Verified
				}
			}
			if got != r.want {
				t.Fatalf("the engine reports %s, want %s: %v", got, r.want, summaries(seen))
			}
			if r.want == r372Unverified {
				r371AssertUnverified(t, seen, "")
			}
			t.Logf("%s -> %s (%s)", r.row, got, r.terminal)
		})
	}
}

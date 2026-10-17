//go:build linux

package workflow

// RULING-371 (N5 P2, unverified containment). An invocation ends on its
// ordinary outcome only when a successful locked read of the candidate record
// establishes containment safe (settled or absent), or when the typed
// RULING-367 pre-start exception proves no confined execution could have
// started. Every other failed locked read -- a torn record, a contradictory
// embedded identity, an invalid lock object, a refused lock, a lock that is
// never released -- ends the invocation on the UNVERIFIED
// WORKSPACE_QUARANTINED terminal, whatever error, cancellation or timeout
// reached terminateRun: that error explains why the invocation ended, not
// whether its workspace is contained.
//
// C1-C4 are the pre-fix controls (a7b41bd ends each FAILED, STOPPED,
// TIMED_OUT or FAILED); C5-C18 the witnesses. C17 (the headless exit code) is
// in cmd/sensei-code/workspace_quarantine_r371_test.go.

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
	"github.com/globulario/sensei-code/internal/runreceipt"
)

const r371Err = "the worker died"

func r371RecordPath(root string) string {
	return filepath.Join(root, ".sensei-code", "candidates", wqTask+".json")
}

// r371Damage are the failed locked reads a test can make without waiting out
// the lock bound: each leaves candidate.LoadLocked failing for wqTask.
var r371Damage = map[string]func(t *testing.T, root string){
	"torn record": func(t *testing.T, root string) {
		if err := os.WriteFile(r371RecordPath(root), []byte("{torn"), 0o644); err != nil {
			t.Fatal(err)
		}
	},
	"contradictory embedded identity": func(t *testing.T, root string) {
		body := `{"task_id":"task-other","repository":"` + root + `","base_sha":"` + strings.Repeat("a", 40) + `"}`
		if err := os.WriteFile(r371RecordPath(root), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	},
	"invalid lock object": func(t *testing.T, root string) {
		lock := r371RecordPath(root) + ".lock"
		_ = os.Remove(lock)
		if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
			t.Fatal(err)
		}
	},
	"lock access refused": func(t *testing.T, root string) {
		if os.Geteuid() == 0 {
			t.Skip("root ignores the lock's permissions")
		}
		lock := r371RecordPath(root) + ".lock"
		if err := os.WriteFile(lock, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(lock, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(lock, 0o600) })
	},
}

// r371Ending is how the invocation ended: the error terminateRun receives and
// the context it receives it on.
type r371Ending struct {
	cancel   bool
	timedOut bool
	note     string
}

var r371Endings = map[string]r371Ending{
	"ordinary error": {},
	"caller stop":    {cancel: true, note: "stopped"},
	"budget expired": {cancel: true, timedOut: true, note: "budget"},
}

// r371End drives terminateRun over the given ending and returns its events.
func r371End(t *testing.T, e *Engine, events <-chan event.Event, end r371Ending) []event.Event {
	t.Helper()
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
	e.terminateRun(ctx, wqTask, "the objective", err)
	return drainEvents(events)
}

// r371AssertUnverified holds what every unverified quarantine terminal owes:
// the quarantine terminal and no other ending, recorded=false naming the read
// failure (when known) and nothing the failed read did not establish, an
// INCOMPLETE receipt over an UNKNOWN candidate, and exactly one terminal and
// one receipt.
func r371AssertUnverified(t *testing.T, seen []event.Event, readFailure string) (map[string]any, event.Event) {
	t.Helper()
	for _, k := range []event.Kind{event.WorkflowFailed, event.WorkflowStopped, event.WorkflowTimedOut} {
		if contains(seen, k) {
			t.Fatalf("an unreadable containment record ended %s: %v", k, summaries(seen))
		}
	}
	p, terminal := wqPayload(t, seen)
	if p["recorded"] != false || p["task_id"] != wqTask {
		t.Fatalf("C5: the terminal is not the unverified quarantine of the task: %v", p)
	}
	reason, _ := p["reason"].(string)
	if readFailure != "" && !strings.Contains(reason, readFailure) {
		t.Fatalf("C5: the reason does not preserve the read failure %q: %q", readFailure, reason)
	}
	// C6
	for _, guessed := range []string{"invocation", "phase", "workspace", "baseline"} {
		if _, ok := p[guessed]; ok {
			t.Fatalf("C6: the unverified terminal states %s: %v", guessed, p)
		}
	}
	// C7
	r, completeness, _ := wqReceipt(t, seen)
	if r["outcome"] != string(wqOutcome) || r["candidate_state"] != string(runreceipt.CandidateUnknown) {
		t.Fatalf("C7: the receipt says %v / %v", r["outcome"], r["candidate_state"])
	}
	if completeness != string(runreceipt.Incomplete) {
		t.Fatalf("C7: an unverified quarantine receipt is %s", completeness)
	}
	f := wqField(r)
	if f == nil || f["state"] == "KNOWN" || f["recorded"] != false {
		t.Fatalf("C7: the receipt claims a verified quarantine: %v", f)
	}
	for _, guessed := range []string{"invocation", "phase", "workspace", "baseline"} {
		if wqValueState(f, guessed) == "KNOWN" {
			t.Fatalf("C6: the receipt measures %s: %v", guessed, f)
		}
	}
	// C16
	terminals, receipts := 0, 0
	for _, ev := range seen {
		if _, terminal := event.RunTerminality(ev.Kind); terminal || ev.Kind == wqKind {
			terminals++
		}
		if ev.Kind == event.RunReceipt {
			receipts++
		}
	}
	if terminals != 1 || receipts != 1 {
		t.Fatalf("C16: %d terminals and %d receipts, want one of each: %v", terminals, receipts, kinds(seen))
	}
	return p, terminal
}

// C1-C4 (controls) and C5-C9, C16, C18 over every failed locked read and every
// ending: the unverified quarantine, never the ordinary ending.
func TestR371C1C4AFailedLockedReadEndsOnTheUnverifiedQuarantine(t *testing.T) {
	for dname, damage := range r371Damage {
		for ename, end := range r371Endings {
			t.Run(dname+"/"+ename, func(t *testing.T) {
				e, events, _, root := wqRig(t)
				damage(t, root)
				_, _, readErr := candidate.LoadLocked(root, wqTask)
				if readErr == nil {
					t.Fatal("premise: the record can still be read under its lock")
				}
				recordBefore, _ := os.ReadFile(r371RecordPath(root))
				drainEvents(events)
				seen := r371End(t, e, events, end)
				p, terminal := r371AssertUnverified(t, seen, readErr.Error())
				// C8: the read failure is the reason; the initiating error is
				// context in the account, never the reason.
				initiating := r371Err
				if end.cancel {
					initiating = context.Canceled.Error()
				}
				if !strings.Contains(terminal.Summary, initiating) || strings.Contains(p["reason"].(string), initiating) {
					t.Fatalf("C8: the initiating %q is not kept apart as context:\nsummary %q\nreason  %q",
						initiating, terminal.Summary, p["reason"])
				}
				// C9: a stop or an expired budget is kept, and does not decide.
				if end.note != "" && !strings.Contains(terminal.Summary, end.note) {
					t.Fatalf("C9: the %s is not kept in the account: %q", ename, terminal.Summary)
				}
				// C18: nothing settled, nothing written, no workspace touched.
				if contains(seen, event.CandidateResolved) {
					t.Fatal("C18: a disposition was recorded over unknown containment")
				}
				if after, _ := os.ReadFile(r371RecordPath(root)); string(after) != string(recordBefore) {
					t.Fatal("C18: the candidate record was written")
				}
				if _, err := os.Stat(filepath.Join(root, "wt")); !os.IsNotExist(err) {
					t.Fatalf("C18: the workspace was touched: %v", err)
				}
			})
		}
	}
}

// C10, C11 and C12. A successful locked read decides: an absent record and a
// settled one keep the ordinary outcome for every ending; an unsettled one is
// the VERIFIED quarantine.
func TestR371C10C12ASuccessfulLockedReadDecides(t *testing.T) {
	ordinary := map[string]event.Kind{"ordinary error": event.WorkflowFailed, "caller stop": event.WorkflowStopped,
		"budget expired": event.WorkflowTimedOut}
	for ename, end := range r371Endings {
		t.Run("absent/"+ename, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			if err := os.Remove(r371RecordPath(root)); err != nil {
				t.Fatal(err)
			}
			if _, ok, err := candidate.LoadLocked(root, wqTask); err != nil || ok {
				t.Fatalf("premise: the locked read does not establish absence: %v %v", ok, err)
			}
			seen := r371End(t, e, events, end)
			if contains(seen, wqKind) || !contains(seen, ordinary[ename]) {
				t.Fatalf("C10: an absent record did not keep %s: %v", ordinary[ename], summaries(seen))
			}
		})
		t.Run("settled/"+ename, func(t *testing.T) {
			e, events, _, _ := wqRig(t)
			seen := r371End(t, e, events, end)
			if contains(seen, wqKind) || !contains(seen, ordinary[ename]) {
				t.Fatalf("C11: a settled record did not keep %s: %v", ordinary[ename], summaries(seen))
			}
		})
		t.Run("unsettled/"+ename, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
			seen := r371End(t, e, events, end)
			wqAssertQuarantined(t, seen, wqTask, "exec-1", filepath.Join(root, "wt"))
		})
	}
}

// C13. The typed lockless pre-start exception is unchanged: over a record its
// ordinary read shows settled, or no record, it keeps the ordinary
// unsupported-confinement failure.
func TestR371C13TheLocklessPreStartExceptionIsUnchanged(t *testing.T) {
	for name, absent := range map[string]bool{"settled record": false, "no record": true} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			if absent {
				if err := os.Remove(r371RecordPath(root)); err != nil {
					t.Fatal(err)
				}
			}
			r367Lockless(t, root)
			e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", r367Unsupported())
			r367Ordinary(t, drainEvents(events))
		})
	}
}

// C14. A lock that is never released within its bound, under an ordinary
// error, does not close the invocation FAILED. (It waits out the record lock's
// bound, so it is the one slow witness.)
func TestR371C14ALockTimeoutUnderAnOrdinaryErrorIsNotFailed(t *testing.T) {
	e, events, _, root := wqRig(t)
	f, err := os.OpenFile(r371RecordPath(root)+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN) }()
	drainEvents(events)
	seen := r371End(t, e, events, r371Endings["ordinary error"])
	r371AssertUnverified(t, seen, "not released in time")
}

// C15. The behavioural record learns nothing from an unverified quarantine;
// the control -- the same ordinary error over a settled record -- still
// reports a failure, so the instrument is live.
func TestR371C15NoBehaviouralFailure(t *testing.T) {
	statuses := make(chan string, 8)
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
	defer srv.Close()
	cfg := behavioral.Config{Enabled: true, URL: srv.URL, Project: "sensei-code", Domain: "d"}

	e, events, _, root := wqRig(t)
	e.Config.Behavioral = cfg
	r371Damage["torn record"](t, root)
	r371AssertUnverified(t, r371End(t, e, events, r371Endings["ordinary error"]), "")
	select {
	case got := <-statuses:
		t.Fatalf("C15: an unverified quarantine was reported to the behavioural record as %q", got)
	case <-time.After(500 * time.Millisecond):
	}

	control, _, _, _ := wqRig(t)
	control.Config.Behavioral = cfg
	control.terminateRun(fixtureCtx(control, wqTask), wqTask, "the objective", errors.New(r371Err))
	select {
	case got := <-statuses:
		if got != "failure" {
			t.Fatalf("control: an ordinary failure was reported as %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control: an ordinary failure reached no behavioural record")
	}
}

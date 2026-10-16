//go:build linux

package workflow

// RULING-367 corrections of N5 P2 (§5-§10 N1, §11-§14 N2).
//
// N1: a platform with no cross-process lock refuses required confinement
// BEFORE any execution starts (candidate.ErrNoCrossProcessLock, typed). That
// refusal keeps the ordinary unsupported-confinement outcome when the record
// carries nothing unresolved; it is never a quarantine fabricated only because
// candidate.LoadLocked cannot operate there. Linux has the lock, so the
// lockless platform is simulated: the refusal is injected through the
// existing beginExecution seam (or constructed as boundedRun constructs it),
// and the locked read is made to fail through its lock object, while the
// record itself stays readable.
//
// N2: refused production-scope material pending at a WORKSPACE_QUARANTINED
// terminal is disclosed in that terminal's atomic unit -- task, governing
// refusal, pending -- and is never resolved, disposed or observed there.
//
// Payloads and receipts are read as JSON, so these witnesses read the same
// bytes before and after the members exist.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/runreceipt"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/validation"
)

// r367Lockless makes the locked read of wqTask's record fail the way it fails
// on a platform with no cross-process lock: the record stays readable to an
// ordinary candidate.Load, and LoadLocked cannot read it.
func r367Lockless(t *testing.T, root string) {
	t.Helper()
	lock := filepath.Join(root, ".sensei-code", "candidates", wqTask+".json.lock")
	_ = os.Remove(lock)
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
		t.Fatal(err)
	}
	if _, _, err := candidate.LoadLocked(root, wqTask); err == nil {
		t.Fatal("premise: the record can still be read under its lock")
	}
}

// r367Unsupported is the refusal boundedRun returns when BeginExecution
// refused required confinement on a lockless platform: typed not-started,
// wrapping candidate.ErrNoCrossProcessLock.
func r367Unsupported() error {
	return &ExecutionBoundaryFailure{TaskID: wqTask, Phase: "certify", notStarted: true, invocation: "exec-lockless",
		Err: fmt.Errorf("the execution did not start: %w", candidate.ErrNoCrossProcessLock)}
}

func r367Ordinary(t *testing.T, seen []event.Event) {
	t.Helper()
	if contains(seen, wqKind) || !contains(seen, event.WorkflowFailed) {
		t.Fatalf("the unsupported-confinement refusal did not keep its ordinary outcome: %v", summaries(seen))
	}
	found := false
	for _, ev := range seen {
		found = found || (ev.Kind == event.WorkflowFailed && strings.Contains(ev.Summary, candidate.ErrNoCrossProcessLock.Error()))
	}
	if !found {
		t.Fatalf("the ordinary failure does not name the unsupported confinement: %v", summaries(seen))
	}
}

// N1-W1 and N1-W2. Required confinement on a lockless platform refuses
// before start, through the real boundary (boundedRun over the injected
// BeginExecution refusal): no candidate check executes, the record is not
// written, and the invocation ends with the ordinary failure -- not a
// fabricated WORKSPACE_QUARANTINED.
func TestR367N1W1W2UnsupportedConfinementBeforeStartKeepsTheOrdinaryOutcome(t *testing.T) {
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
		t.Fatal("a candidate check executed although the platform cannot bind it to the record")
	}
	var f *ExecutionBoundaryFailure
	if !errors.As(err, &f) || !f.notStarted || !errors.Is(err, candidate.ErrNoCrossProcessLock) {
		t.Fatalf("premise: the refusal is not the typed not-started unsupported-confinement failure: %v", err)
	}
	r367Lockless(t, root)
	drainEvents(events)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", err)
	r367Ordinary(t, drainEvents(events))
	if after := wqRecordBytes(t, root, wqTask); after != before {
		t.Fatalf("the refusal wrote the candidate record:\nbefore %s\nafter  %s", before, after)
	}
}

// N1-W1, absent record: nothing is recorded for the task, so nothing is
// protected, and the refusal keeps its ordinary outcome too.
func TestR367N1W1NoRecordKeepsTheOrdinaryOutcome(t *testing.T) {
	e, events, _, root := wqRig(t)
	if err := os.Remove(filepath.Join(root, ".sensei-code", "candidates", wqTask+".json")); err != nil {
		t.Fatal(err)
	}
	r367Lockless(t, root)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", r367Unsupported())
	r367Ordinary(t, drainEvents(events))
}

// N1-W3, N1-W4 and N1-W6. The exception never releases unresolved state: an
// existing durable Quarantined record, an execution recorded in flight, and a
// record that cannot be read all keep the quarantine path under the same
// lockless refusal -- never the ordinary outcome -- and the record is not
// written.
func TestR367N1W3W4W6UnresolvedOrUnreadableStateKeepsTheQuarantine(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, root string){
		"durable Quarantined record": func(t *testing.T, root string) {
			wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
			wqQuarantine(t, root, wqTask)
		},
		"execution in flight": func(t *testing.T, root string) {
			wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
		},
		"unreadable record": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, ".sensei-code", "candidates", wqTask+".json"), []byte("{torn"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			setup(t, root)
			r367Lockless(t, root)
			before := wqRecordBytes(t, root, wqTask)
			e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", r367Unsupported())
			seen := drainEvents(events)
			if contains(seen, event.WorkflowFailed) {
				t.Fatalf("unresolved or unknown containment became the ordinary outcome: %v", summaries(seen))
			}
			p, _ := wqPayload(t, seen)
			if p["task_id"] != wqTask {
				t.Fatalf("the quarantine terminal names another task: %v", p)
			}
			if after := wqRecordBytes(t, root, wqTask); after != before {
				t.Fatal("the quarantine terminal wrote the candidate record")
			}
		})
	}
}

// N1-W6. A genuine typed unresolved execution still projects a verified
// WORKSPACE_QUARANTINED.
func TestR367N1W6AGenuineUnresolvedExecutionStillProjectsTheQuarantine(t *testing.T) {
	e, events, _, root := wqRig(t)
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective",
		&WorkspaceUnsettled{TaskID: wqTask, Phase: "certify", Workspace: filepath.Join(root, "wt"), Invocation: "exec-1", Recorded: true})
	wqAssertQuarantined(t, drainEvents(events), wqTask, "exec-1", filepath.Join(root, "wt"))
}

// N1 mutant control. The exception is the TYPED pre-start distinction and
// nothing wider: a pre-start failure for another reason, and the lockless
// error on a boundary that is not typed not-started, over the same settled
// record whose locked read fails, keep the unverified quarantine.
func TestR367N1TheTypedPreStartDistinctionIsLoadBearing(t *testing.T) {
	for name, err := range map[string]error{
		"pre-start, not the lockless refusal": &ExecutionBoundaryFailure{TaskID: wqTask, Phase: "certify", notStarted: true,
			invocation: "exec-x", Err: errors.New("the execution did not start: injected")},
		"lockless refusal, not typed not-started": &ExecutionBoundaryFailure{TaskID: wqTask, Phase: "certify",
			Err: fmt.Errorf("the settled execution could not be cleared: %w", candidate.ErrNoCrossProcessLock)},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			r367Lockless(t, root)
			e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", err)
			seen := drainEvents(events)
			if contains(seen, event.WorkflowFailed) {
				t.Fatalf("an untyped boundary failure over an unverifiable record became ordinary: %v", summaries(seen))
			}
			if p, _ := wqPayload(t, seen); p["recorded"] != false {
				t.Fatalf("an unverifiable record was projected as verified: %v", p)
			}
		})
	}
}

// r367Arm arms refused material on wqTask bound to its candidate record.
func r367Arm(t *testing.T, e *Engine, root, refusalID string) *refusedScopeMaterial {
	t.Helper()
	id, _, err := candidate.Load(root, wqTask)
	if err != nil {
		t.Fatal(err)
	}
	e.armRefusedMaterial(wqTask, refusedScopeMaterial{refusalID: refusalID, identity: id})
	return r367Pending(e)
}

func r367Pending(e *Engine) *refusedScopeMaterial {
	e.mu.Lock()
	defer e.mu.Unlock()
	if t := e.attempts[wqTask]; t != nil {
		return t.refusedMaterial
	}
	return nil
}

func r367Unsettled(root string) error {
	return &WorkspaceUnsettled{TaskID: wqTask, Phase: "certify", Workspace: filepath.Join(root, "wt"), Invocation: "exec-1", Recorded: true}
}

func r367Member(m map[string]any, key string) map[string]any {
	v, _ := m[key].(map[string]any)
	return v
}

var r367Refusal = strings.Repeat("f", 64)

// N2-W1. A terminal other than the quarantine keeps DF-48 settlement: the
// material is resolved (CandidateResolved) before the terminal and let go,
// and no pending disclosure is invented.
func TestR367N2W1OtherTerminalsKeepTheirSettlement(t *testing.T) {
	e, events, _, root := wqRig(t)
	r367Arm(t, e, root, r367Refusal)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", errors.New("the worker died"))
	seen := drainEvents(events)
	if !contains(seen, event.CandidateResolved) || !contains(seen, event.WorkflowFailed) {
		t.Fatalf("an ordinary failure did not settle its refused material: %v", kinds(seen))
	}
	if r367Pending(e) != nil {
		t.Fatal("the settled material is still pending")
	}
	if id, _, _ := candidate.Load(root, wqTask); id.Resolution == nil {
		t.Fatal("the ordinary terminal recorded no disposition")
	}
}

// N2-W2, N2-W3 and N2-W4. Armed material at a quarantine ends
// WORKSPACE_QUARANTINED (not FAILED); the material is neither resolved,
// disposed nor observed; and the terminal's atomic unit discloses it --
// task, governing refusal, PENDING -- so a fresh process reading the durable
// history alone identifies the owed disposition.
func TestR367N2W2W3W4PendingMaterialIsDisclosedDurablyAndNotSettled(t *testing.T) {
	e, events, _, root := wqRig(t)
	armed := r367Arm(t, e, root, r367Refusal)
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	before := wqRecordBytes(t, root, wqTask)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", r367Unsettled(root))
	seen := drainEvents(events)
	// W2
	wqAssertQuarantined(t, seen, wqTask, "exec-1", filepath.Join(root, "wt"))
	// W3
	if contains(seen, event.CandidateResolved) {
		t.Fatal("the pending material was resolved over a quarantined candidate")
	}
	if wqRecordBytes(t, root, wqTask) != before {
		t.Fatal("the quarantined record was rewritten")
	}
	if got := r367Pending(e); got != armed || got.identity.Resolution != nil {
		t.Fatalf("the pending material was consumed or disposed: %+v", got)
	}
	// W4: a fresh process, nothing carried in memory.
	store, err := session.New(root, "session-q")
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	p, terminal := wqPayload(t, history)
	pending := r367Member(p, "pending_refused_material")
	if pending == nil || pending["task_id"] != wqTask || pending["refusal_id"] != r367Refusal || pending["disposition"] != "PENDING" {
		t.Fatalf("the durable terminal does not disclose the pending material: %v", p)
	}
	if !strings.Contains(terminal.Summary, r367Refusal) {
		t.Fatalf("the terminal's account does not name the owed disposition: %q", terminal.Summary)
	}
	r, _, missing := wqReceipt(t, history)
	rp := r367Member(wqField(r), "pending_refused_material")
	if rp == nil || rp["task_id"] != wqTask || rp["disposition"] != "PENDING" ||
		wqValueState(rp, "refusal_id") != "KNOWN" || wqValueText(rp, "refusal_id") != r367Refusal {
		t.Fatalf("the durable receipt does not disclose the pending material: %v", wqField(r))
	}
	if wqMentions(missing, "workspace_quarantine") || wqMentions(missing, "candidate_state") {
		t.Fatalf("a verified quarantine disclosing its pending material is incomplete about its own claim: %v", missing)
	}
}

// N2-W4, unknown identity. Material whose governing refusal cannot be
// established is still disclosed, with that member UNKNOWN -- never guessed
// -- and the receipt is not complete about it.
func TestR367N2W4AnUnestablishedRefusalIdentityIsUnknown(t *testing.T) {
	e, events, _, root := wqRig(t)
	r367Arm(t, e, root, "")
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", r367Unsettled(root))
	seen := drainEvents(events)
	p, _ := wqPayload(t, seen)
	pending := r367Member(p, "pending_refused_material")
	if pending == nil || pending["disposition"] != "PENDING" {
		t.Fatalf("material of unknown refusal was not disclosed: %v", p)
	}
	if _, guessed := pending["refusal_id"]; guessed {
		t.Fatalf("an unestablished refusal identity was stated: %v", pending)
	}
	r, completeness, missing := wqReceipt(t, seen)
	rp := r367Member(wqField(r), "pending_refused_material")
	if rp == nil || wqValueState(rp, "refusal_id") != "UNKNOWN" {
		t.Fatalf("the receipt does not state the refusal identity UNKNOWN: %v", rp)
	}
	if completeness == string(runreceipt.Complete) || !wqMentions(missing, "pending_refused_material") {
		t.Fatalf("a receipt with an unknown governing refusal is %s: %v", completeness, missing)
	}
}

// N2-W5. A failed atomic unit publishes no disclosure and no terminal; the
// material stays pending in the invocation and the P1 quarantine stays
// authoritative and unchanged.
func TestR367N2W5AFailedUnitClaimsNoDurableDisclosure(t *testing.T) {
	e, events, store, root := wqRig(t)
	armed := r367Arm(t, e, root, r367Refusal)
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	ctx := fixtureCtx(e, wqTask)
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	recordBefore := wqRecordBytes(t, root, wqTask)
	file := filepath.Join(root, ".sensei-code", "sessions", "session-q", "events.jsonl")
	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatal(err)
	}
	e.terminateRun(ctx, wqTask, "the objective", r367Unsettled(root))
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ev := range drainEvents(events) {
		if ev.TaskID == wqTask && (ev.Kind == event.RunReceipt || ev.Kind == wqKind || ev.Kind == event.CandidateResolved) {
			t.Fatalf("an unrecorded ending was published: %s %s", ev.Kind, ev.Summary)
		}
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the record took %d events of an ending that failed to record", len(after)-len(before))
	}
	if wqRecordBytes(t, root, wqTask) != recordBefore {
		t.Fatal("the candidate record changed")
	}
	if id, _, _ := candidate.Load(root, wqTask); !id.Unsettled() {
		t.Fatal("the P1 quarantine did not survive the failed projection")
	}
	if r367Pending(e) != armed {
		t.Fatal("the pending material was let go although nothing was recorded")
	}
}

// N2-W6. With nothing pending, nothing is invented: neither the terminal nor
// the receipt carries a pending disclosure.
func TestR367N2W6NoInventedObligation(t *testing.T) {
	e, events, _, root := wqRig(t)
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", r367Unsettled(root))
	seen := drainEvents(events)
	p, _ := wqPayload(t, seen)
	if _, has := p["pending_refused_material"]; has {
		t.Fatalf("a pending disclosure was invented: %v", p)
	}
	r, _, _ := wqReceipt(t, seen)
	if _, has := wqField(r)["pending_refused_material"]; has {
		t.Fatalf("the receipt invents a pending disclosure: %v", wqField(r))
	}
	raw, err := json.Marshal(p)
	if err != nil || strings.Contains(string(raw), "PENDING") {
		t.Fatalf("the terminal speaks of a pending obligation: %s %v", raw, err)
	}
}

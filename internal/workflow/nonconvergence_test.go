package workflow

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/runreceipt"
	"github.com/globulario/sensei-code/internal/session"
)

// DF-6, observed 2026-09-19 on task-1789775984590842441: every implementer spent
// its review cycles, the task ended WorkflowFailed -- final to FindInterrupted --
// while the candidate record called the same work "resumable". These witnesses
// drive the REAL candidate loop with a reviewer that always asks for revision.

// reviseForever is the gate harness with a reviewer that never accepts.
func reviseForever(t *testing.T) *gateHarness {
	t.Helper()
	return newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Fresh, "revise")
}

func runImplement(h *gateHarness) (error, []event.Event) {
	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	return failed, drainEvents(h.events)
}

func terminalPayload(t *testing.T, events []event.Event, kind event.Kind) json.RawMessage {
	t.Helper()
	for _, ev := range events {
		if ev.Kind == kind {
			return ev.Payload
		}
	}
	t.Fatalf("no %s terminal: %v", kind, kinds(events))
	return nil
}

// The task that spent every review cycle ends NOT_CONVERGED: not FAILED, not
// via the fail path, its candidate kept as resumable work, its receipt a
// COMPLETE-able positive claim (UNATTEMPTED, never PRESENT without a mint).
func TestSpendingEveryReviewCycleEndsNotConvergedNotFailed(t *testing.T) {
	h := reviseForever(t)
	failed, seen := runImplement(h)

	if failed != nil {
		t.Fatalf("non-convergence went through the failure path: %v", failed)
	}
	if contains(seen, event.WorkflowFailed) {
		t.Fatalf("non-convergence was reported as a failure: %v", kinds(seen))
	}
	n, err := ParseNotConverged(terminalPayload(t, seen, event.WorkflowNotConverged))
	if err != nil {
		t.Fatalf("the non-convergence record does not read back: %v", err)
	}
	if n.TaskID != "task-1" || n.Owed != OwedArchitectReplan || len(n.Implementers) != 1 || n.Implementers[0] != "claude" || n.ReviewCycles != 1 {
		t.Fatalf("the record does not say who spent what and what is owed: %+v", n)
	}
	rec := receiptFrom(t, seen)
	if rec.Outcome != runreceipt.OutcomeNotConverged {
		t.Fatalf("receipt outcome %q, want NOT_CONVERGED", rec.Outcome)
	}
	if rec.NotConverged.State != runreceipt.Known {
		t.Fatalf("the receipt does not state the non-convergence: %+v", rec.NotConverged)
	}
	if rec.CandidateState != runreceipt.CandidateUnattempted {
		t.Fatalf("a kept, never-minted candidate reads as %q; PRESENT would demand mint evidence that does not exist", rec.CandidateState)
	}
	_, missing := rec.Completeness()
	for _, m := range missing {
		if strings.HasPrefix(m, "candidate_") || strings.Contains(m, "not_converged") {
			t.Fatalf("the non-convergence receipt is incomplete about its own claim: %s", m)
		}
	}
	// The candidate the record calls resumable is on disk.
	if _, err := os.Stat(h.work); err != nil {
		t.Fatalf("the candidate of a non-converged task is gone: %v", err)
	}
}

// Only exhaustion qualifies. An exhausted worker beside a worker that FAILED
// stays a failure: a re-plan cannot answer a crashed process.
func TestExhaustionBesideARealFailureStaysFailed(t *testing.T) {
	h := reviseForever(t)
	h.engine.Config.Implementors = []config.Agent{h.worker, {Name: "codex", Command: "false", Graph: "none"}}
	failed, seen := runImplement(h)

	if failed == nil || !strings.Contains(failed.Error(), "no bounded implementor produced an acceptable candidate") {
		t.Fatalf("a real failure beside exhaustion did not stay a failure: %v", failed)
	}
	if contains(seen, event.WorkflowNotConverged) {
		t.Fatalf("a crashed worker bought a re-plan: %v", kinds(seen))
	}
}

// A restarted process finds the non-converged task, planned, carrying its
// record -- where WorkflowFailed made it vanish.
func TestANotConvergedTaskIsResumableAfterRestart(t *testing.T) {
	root := t.TempDir()
	e, _, _ := blockedEngine(t, root, "session-n")
	const task = "task-5"
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emit(event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", nil))
	e.beginReceipt(task)
	e.endNotConverged(task, NotConverged{TaskID: task, Implementers: []string{"claude", "codex"}, ReviewCycles: 3, Owed: OwedArchitectReplan})

	found := reopen(t, root, "session-n")
	if len(found) != 1 || found[0].TaskID != task || !found[0].Planned || len(found[0].NotConverged) == 0 {
		t.Fatalf("a non-converged task is not resumable as itself after restart: %+v", found)
	}
	why, owed, err := owedReplan(found[0].NotConverged, found[0].BlockedExternal, task)
	if err != nil || !owed || !strings.Contains(why, "did not converge") {
		t.Fatalf("the restarted task does not owe its re-plan: owed=%v why=%q err=%v", owed, why, err)
	}
}

// What a resume owes, read from the record: a non-convergence and an architect
// block owe a re-plan; an implementer block does not; a record bound to another
// task is refused rather than acted on.
func TestOwedReplanReadsTheRecord(t *testing.T) {
	nc, _ := json.Marshal(NotConverged{TaskID: "t", Implementers: []string{"claude"}, ReviewCycles: 3, Owed: OwedArchitectReplan})
	arch, _ := json.Marshal(ExternalBlock{TaskID: "t", Role: "architect", Provider: "chatgpt", Reason: "usageLimitExceeded", RetryAtState: RetryAtUnknown})
	impl, _ := json.Marshal(ExternalBlock{TaskID: "t", Role: "implementer", Provider: "claude", Reason: "usageLimitExceeded", RetryAtState: RetryAtUnknown})

	if _, owed, err := owedReplan(nc, nil, "t"); err != nil || !owed {
		t.Fatalf("non-convergence did not owe a re-plan: %v %v", owed, err)
	}
	if _, owed, err := owedReplan(nil, arch, "t"); err != nil || !owed {
		t.Fatalf("a blocked architect re-plan is not owed on resume: %v %v", owed, err)
	}
	if _, owed, err := owedReplan(nil, impl, "t"); err != nil || owed {
		t.Fatalf("an implementer block was sent to re-plan: %v %v", owed, err)
	}
	if _, _, err := owedReplan(nc, nil, "other"); err == nil {
		t.Fatal("a non-convergence record bound to another task was acted on")
	}
	bad, _ := json.Marshal(NotConverged{TaskID: "t", Implementers: []string{"claude"}, ReviewCycles: 3, Owed: "something else"})
	if _, err := ParseNotConverged(bad); err == nil {
		t.Fatal("a record owing something no resume delivers was accepted")
	}
}

// A plan discharges an architect block: a task blocked before planning, resumed,
// and then planned is never sent back to re-plan by a turn it already took. An
// implementer block is not discharged by a plan.
func TestAPlanDischargesAnArchitectBlockOnly(t *testing.T) {
	for role, cleared := range map[string]bool{"architect": true, "implementer": false} {
		block, _ := json.Marshal(ExternalBlock{TaskID: "t", Role: role, Provider: "chatgpt", Reason: "usageLimitExceeded", RetryAtState: RetryAtUnknown})
		events := []event.Event{
			event.New("s", "t", event.SourceSystem, event.TaskCreated, "objective", nil),
			{SessionID: "s", TaskID: "t", Source: event.SourceSystem, Kind: event.WorkflowBlockedExternal, Payload: block},
			event.New("s", "t", event.SourceArchitect, event.PlanProposed, "plan", nil),
		}
		found := session.FindInterrupted(events)
		if len(found) != 1 {
			t.Fatalf("%s: task not found: %+v", role, found)
		}
		if got := len(found[0].BlockedExternal) == 0; got != cleared {
			t.Fatalf("%s block cleared by a plan = %v, want %v", role, got, cleared)
		}
	}
}

// The historical DF-6 task stays what it was: FAILED under the old semantics is
// not reopened, whatever its text says.
func TestAHistoricalNonConvergenceFailureIsNotReopened(t *testing.T) {
	events := []event.Event{
		event.New("s", "t", event.SourceSystem, event.TaskCreated, "objective", nil),
		event.New("s", "t", event.SourceArchitect, event.PlanProposed, "plan", nil),
		event.New("s", "t", event.SourceSystem, event.WorkflowFailed,
			"no bounded implementor produced an acceptable candidate: claude: candidate did not converge after 3 review cycles", nil),
	}
	if found := session.FindInterrupted(events); len(found) != 0 {
		t.Fatalf("a historical FAILED task was reopened: %+v", found)
	}
}

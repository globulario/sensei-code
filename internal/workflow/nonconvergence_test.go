package workflow

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
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

// The re-plan a resume records is THE plan: it discharges both an owed
// non-convergence and an architect block, so a later implementer block is
// continued as an implementer turn under the revised plan -- never re-planned
// again from the plan that did not converge, and never dropped.
func TestARecordedReplanDischargesWhatWasOwed(t *testing.T) {
	arch, _ := json.Marshal(ExternalBlock{TaskID: "t", Role: "architect", Provider: "chatgpt", Reason: "usageLimitExceeded", RetryAtState: RetryAtUnknown})
	impl, _ := json.Marshal(ExternalBlock{TaskID: "t", Role: "implementer", Provider: "claude", Reason: "usageLimitExceeded", RetryAtState: RetryAtUnknown})
	nc, _ := json.Marshal(NotConverged{TaskID: "t", Implementers: []string{"claude"}, ReviewCycles: 3, Owed: OwedArchitectReplan})
	for name, owedBy := range map[string]event.Event{
		"architect block": {SessionID: "s", TaskID: "t", Source: event.SourceSystem, Kind: event.WorkflowBlockedExternal, Payload: arch},
		"non-convergence": {SessionID: "s", TaskID: "t", Source: event.SourceSystem, Kind: event.WorkflowNotConverged, Payload: nc},
	} {
		events := []event.Event{
			event.New("s", "t", event.SourceSystem, event.TaskCreated, "objective", nil),
			event.New("s", "t", event.SourceArchitect, event.PlanProposed, "plan", nil),
			owedBy,
			event.New("s", "t", event.SourceArchitect, event.PlanProposed, "the re-plan", nil),
			{SessionID: "s", TaskID: "t", Source: event.SourceSystem, Kind: event.WorkflowBlockedExternal, Payload: impl},
		}
		found := session.FindInterrupted(events)
		if len(found) != 1 {
			t.Fatalf("%s: task not found: %+v", name, found)
		}
		if _, owed, err := owedReplan(found[0].NotConverged, found[0].BlockedExternal, "t"); err != nil || owed {
			t.Fatalf("%s: a recorded re-plan did not discharge the obligation (owed=%v err=%v)", name, owed, err)
		}
		if blockRoleOf(found[0].BlockedExternal) != "implementer" {
			t.Fatalf("%s: the later implementer block was dropped: %s", name, found[0].BlockedExternal)
		}
	}
}

func blockRoleOf(raw json.RawMessage) string {
	b, err := ParseExternalBlock(raw)
	if err != nil {
		return ""
	}
	return b.Role
}

// A supplied plan is never re-planned by the architect, in a RESTARTED process
// too. The refusal reads the in-memory supplied plan, so this proves the resume
// path restores it from the durable record before any re-plan is attempted:
// a restart must not change who may author the governing plan.
func TestARestartedEngineStillRefusesToRePlanASuppliedPlan(t *testing.T) {
	plan, err := ParseSuppliedPlan([]byte(goodPlan))
	if err != nil {
		t.Fatalf("the shared supplied-plan fixture no longer parses, so this witness would exercise nothing: %v", err)
	}
	record, _ := json.Marshal(proposedPlan{architectureDecision: plan.decision, PlanSource: PlanSupplied, PlanDigest: plan.Digest})
	task := session.Interrupted{TaskID: "task-s", Task: "x", Planned: true,
		PlanSource: string(PlanSupplied), PlanDigest: plan.Digest, PlanRecord: record}

	restarted := New(gitx.Repo{Root: t.TempDir()}, config.Default(), event.NewBus(), nil, "fresh-session")
	if _, err := restarted.restorePlanBound(task); err != nil {
		t.Fatalf("the supplied bound was not restored from the record: %v", err)
	}
	_, err = restarted.resolveArchitectureForRevision(context.Background(), nil, certifiedStart{}, "task-s", "x", "PROMPT", "the candidate did not converge")
	if err == nil || !strings.Contains(err.Error(), "A supplied plan is not revised by the architect") {
		t.Fatalf("a restarted engine let the architect revise a supplied plan: %v", err)
	}
}

// The objection that prevented convergence reaches the architect. The verdict
// below is the exact REVISE payload a real stubsmoke run recorded on
// 2026-09-19; FindInterrupted carries its instruction as the task's Review, and
// the owed re-plan puts it under OPEN FINDINGS. A re-plan without it would ask
// the architect to change a plan blind.
func TestTheOpenFindingsReachTheOwedReplan(t *testing.T) {
	const recorded = `{"provenance":{"task_id":"task-1789832817677608252","role":"reviewer","provider":"chatgpt","session_id":"session-20260919T154657.677324504Z","session_mode":"fresh","base_sha":"3b07f93df8a7ed7ed460e3e7f8c6d9aae8fe98af","candidate_digest":"d027ceb1144f0dbaaefc79601955f6924fa91003921b26ac7220cccea770bad7","candidate_tree":"974b16913e41fd98370caf6d06321e1c487166e6","graph_build_commit":"05feaf64d2694e97ac42b6bb93fbb49b9851a1f1","at":"2026-09-19T15:47:56.467303811Z"},"decision":"revise","summary":"the proof is missing","findings":[{"id":"1","severity":"blocking","claim":"the change is not proven","reference":"internal/report/report.go","reason":"no witness"}]}`
	nc, _ := json.Marshal(NotConverged{TaskID: "t", Implementers: []string{"claude"}, ReviewCycles: 1, Owed: OwedArchitectReplan})
	events := []event.Event{
		event.New("s", "t", event.SourceSystem, event.TaskCreated, "objective", nil),
		event.New("s", "t", event.SourceArchitect, event.PlanProposed, "plan", nil),
		{SessionID: "s", TaskID: "t", Source: event.SourceReviewer, Kind: event.ReviewCompleted, Summary: "REVISE", Payload: json.RawMessage(recorded)},
		{SessionID: "s", TaskID: "t", Source: event.SourceSystem, Kind: event.WorkflowNotConverged, Payload: nc},
	}
	found := session.FindInterrupted(events)
	if len(found) != 1 {
		t.Fatalf("task not found: %+v", found)
	}
	why, owed, err := owedReplan(found[0].NotConverged, found[0].BlockedExternal, "t")
	if err != nil || !owed {
		t.Fatalf("no re-plan owed: %v %v", owed, err)
	}
	prompt := replanPrompt("objective", "the plan", why, found[0].Review)
	if !strings.Contains(prompt, "the change is not proven") || strings.Contains(prompt, "(the record carries no review text)") {
		t.Fatalf("the re-plan prompt does not carry the finding that prevented convergence:\n%s", prompt)
	}
}

// FINDING CLASS BINDS ITS RESPONSE: the loop-level witnesses.
//
// Measured on the DF-19 resume, 2026-09-25: a review returned a CODE finding
// and an EVIDENCE finding, the implementer answered the evidence one and called
// the cycle closed, and only the identical-diff proxy noticed. These drive the
// REAL candidate loop for two cycles with a scripted worker and a scripted
// reviewer, so what is asserted is what the loop concluded.

const (
	codeFinding     = `{"id":"f1","severity":"blocking","class":"code","claim":"a CREATE that cannot be bound does not refuse the plan","reference":"main.go","reason":"routePlan emits the refusal and continues"}`
	evidenceFinding = `{"id":"f2","severity":"major","class":"evidence","claim":"the failing-first history is established","reference":"main.go","reason":"no retained run of the check","proof_gap":"a passing run of test -s main.go on this candidate"}`
	// strayFinding points at a file the worker never touches, so a moved
	// main.go is, for it, an unrelated edit.
	strayFinding = `{"id":"f1","severity":"blocking","class":"code","claim":"a CREATE that cannot be bound does not refuse the plan","reference":"plan.go","reason":"routePlan emits the refusal and continues"}`
)

// scriptedLoop is the gate harness with a shell worker and a shell reviewer.
//
// The reviewer returns REVISE with findings on its first call and ACCEPT on
// every later one, and counts its calls in reviews. The worker writes main.go
// (moving an unrelated line each cycle when moves is set) and, from its second
// cycle on, prints answer as its report.
func scriptedLoop(t *testing.T, findings string, moves bool, answer string) (h *gateHarness, reviews string) {
	t.Helper()
	h = newGateHarness(t, roles.Policy{Reason: "blast radius file with approval gate none"}, roles.Fresh, "accept")
	dir := t.TempDir()
	cycles, reviews := dir+"/cycles", dir+"/reviews"
	line := `println(1)`
	if moves {
		// An UNRELATED line: the value printed, not anything a finding names.
		line = `println($n)`
	}
	worker := "cat >/dev/null\n" +
		"n=$(cat '" + cycles + "' 2>/dev/null || echo 0); n=$((n+1)); echo \"$n\" > '" + cycles + "'\n" +
		"printf \"package main\\n\\nfunc main() { " + line + " }\\n\" > main.go\n" +
		"if [ \"$n\" -gt 1 ]; then cat <<'ANSWER'\n" + answer + "\nANSWER\nfi\n"
	reviewer := "cat >/dev/null\n" +
		"n=$(cat '" + reviews + "' 2>/dev/null || echo 0); n=$((n+1)); echo \"$n\" > '" + reviews + "'\n" +
		"if [ \"$n\" -eq 1 ]; then cat <<'VERDICT'\n" +
		`{"decision":"revise","summary":"the plan is not refused","findings":[` + findings + `]}` +
		"\nVERDICT\nelse echo '{\"decision\":\"accept\",\"summary\":\"the candidate stands\"}'; fi\n"
	workerScript, reviewerScript := dir+"/worker.sh", dir+"/reviewer.sh"
	if err := os.WriteFile(workerScript, []byte(worker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(reviewerScript, []byte(reviewer), 0o755); err != nil {
		t.Fatal(err)
	}
	h.worker = config.Agent{Name: "claude", Command: "sh", Args: []string{workerScript}, Graph: "none"}
	h.engine.Config.Implementors = []config.Agent{h.worker}
	h.engine.Config.Reviewer = config.Agent{Name: "codex", Command: "sh", Args: []string{reviewerScript}, Graph: "none"}
	h.engine.Config.Workflow.ReviewCycles = 2
	h.engine.Runners = nil
	return h, reviews
}

func reviewCalls(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the reviewer was never called: %v", err)
	}
	return strings.TrimSpace(string(raw))
}

func (h *gateHarness) runTwoCycles() (candidateOutcome, error) {
	outcome, _, _, _, err := h.engine.runCandidate(context.Background(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	return outcome, err
}

// W1 THE MEASURED CASE. One CODE finding and one EVIDENCE finding, answered with
// evidence alone: the run does not converge, and the diagnosis names the
// unaddressed CODE finding by id -- not merely that the candidate did not move.
//
// Fails if the accounting stops running before the identical-diff backstop, or
// stops naming an unanswered finding by id.
func TestW1ACodeFindingAnsweredWithEvidenceAloneDoesNotConverge(t *testing.T) {
	h, reviews := scriptedLoop(t, codeFinding+","+evidenceFinding, false,
		`{"finding_responses":[{"id":"f2","answered_by":"evidence","evidence":"gofmt"}]}`)
	outcome, err := h.runTwoCycles()

	if outcome.Accepted() || err == nil {
		t.Fatalf("a CODE finding answered only with evidence converged: outcome %q err %v", outcome, err)
	}
	if !strings.Contains(err.Error(), "f1") {
		t.Fatalf("the diagnosis does not name the unaddressed CODE finding by id: %v", err)
	}
	if strings.Contains(err.Error(), "did not change between review cycles") {
		t.Fatalf("the identical-diff proxy decided, not the per-finding accounting: %v", err)
	}
	if got := reviewCalls(t, reviews); got != "1" {
		t.Fatalf("the reviewer was asked %s times; an unaccounted finding must stop the cycle before review", got)
	}
}

// W3 THE PROXY IS NOT THE CHECK -- CRITICAL CONTROL. The worker moves an
// UNRELATED line, so the diff differs, and says nothing about the CODE finding.
// A reviewer that would accept the moved candidate is never reached: the cycle
// has not accounted for f1, whatever the diff did.
//
// Against diff movement as the predicate this converges (the second review
// accepts). Fails if convergence is decided by the diff again.
func TestW3AMovedDiffDoesNotDischargeAnUnaddressedCodeFinding(t *testing.T) {
	h, reviews := scriptedLoop(t, codeFinding, true, `nothing to report`)
	outcome, err := h.runTwoCycles()

	if outcome.Accepted() {
		t.Fatal("an unrelated edit carried a blocking CODE finding forward as answered")
	}
	if err == nil || !strings.Contains(err.Error(), "f1") {
		t.Fatalf("the diagnosis does not name the unaddressed CODE finding: %v", err)
	}
	if got := reviewCalls(t, reviews); got != "1" {
		t.Fatalf("the reviewer was asked %s times; the accounting must refuse before the reviewer can accept", got)
	}

	// One level down: the worker pairs the id with the right class and the
	// candidate moved, but not where the finding points. Naming the file it
	// changed, or the file it should have changed, discharges nothing.
	for answer, want := range map[string]string{
		`{"finding_responses":[{"id":"f1","answered_by":"code","paths":["main.go"]}]}`: "touches none of it",
		`{"finding_responses":[{"id":"f1","answered_by":"code","paths":["plan.go"]}]}`: "did not change since the finding was raised",
	} {
		h, reviews := scriptedLoop(t, strayFinding, true, answer)
		outcome, err := h.runTwoCycles()
		if outcome.Accepted() || err == nil || !strings.Contains(err.Error(), "f1") || !strings.Contains(err.Error(), want) {
			t.Fatalf("%s: an unrelated movement discharged a claimed CODE finding: outcome %q err %v", answer, outcome, err)
		}
		if got := reviewCalls(t, reviews); got != "1" {
			t.Fatalf("%s: the reviewer was asked %s times", answer, got)
		}
	}
}

// W4 EVIDENCE FINDING DISCHARGED BY EVIDENCE, in the loop. The worker changes
// nothing and cites, by full command line, the check the finding's proof gap
// names, which ran and passed; the finding is discharged, the identical-diff
// backstop does not decide, and the candidate goes back to the reviewer. The
// control cites gofmt, which also ran and passed but is not the proof asked
// for: the finding stays open and the reviewer is never asked again.
//
// What happens after the discharge is not this witness's: an ACCEPT on the
// same bytes and the same evidence identity meets the existing contradiction
// rule (reviewconsistency.go), because this engine has no durable place yet for
// the evidence a review demanded. That is the companion evidence-durability
// objective, so the assertion stops at the discharge.
//
// Fails if the backstop decides over a settled account, if the check the proof
// gap names stops discharging it, or if any passing check does.
func TestW4AnEvidenceFindingIsDischargedByEvidenceWithNoCodeChange(t *testing.T) {
	loop := func(cite string) (*gateHarness, string) {
		h, reviews := scriptedLoop(t, evidenceFinding, false,
			`{"finding_responses":[{"id":"f2","answered_by":"evidence","evidence":"`+cite+`"}]}`)
		h.engine.Config.Permissions.RunTests = true
		h.engine.Config.Validation.Test = []config.Command{{Command: "test", Args: []string{"-s", "main.go"}}}
		return h, reviews
	}

	h, reviews := loop("test -s main.go")
	_, err := h.runTwoCycles()
	if err != nil && (strings.Contains(err.Error(), "were not discharged") || strings.Contains(err.Error(), "did not change between review cycles")) {
		t.Fatalf("an EVIDENCE finding answered with the executed check its proof gap names was not discharged: %v", err)
	}
	if got := reviewCalls(t, reviews); got != "2" {
		t.Fatalf("the reviewer was asked %s times; a discharged finding goes back to the reviewer", got)
	}

	control, controlReviews := loop("gofmt -l main.go")
	outcome, err := control.runTwoCycles()
	if outcome.Accepted() || err == nil || !strings.Contains(err.Error(), "f2") || !strings.Contains(err.Error(), "not the proof the finding asks for") {
		t.Fatalf("control: a passing but irrelevant check discharged the EVIDENCE finding: outcome %q err %v", outcome, err)
	}
	if got := reviewCalls(t, controlReviews); got != "1" {
		t.Fatalf("control: the reviewer was asked %s times", got)
	}
}

// W6 DISAGREEMENT IS A ROUTE, NOT A LICENCE -- CONTROL, in the loop. The worker
// moves the candidate, answers the CODE finding as code, and disputes its class.
// The dispute takes the workflow's escalation route: the architect is consulted
// with the dispute and the reviewer's class, the reviewer is never asked to
// accept, and the finding stays open under the class the reviewer gave it. The
// control half is the same cycle without the dispute, which converges without
// the architect: so what routed the cycle is the dispute, not anything else.
//
// The architect is a recording double that proceeds with a revised plan; this
// harness's stub Sensei cannot certify that plan, so the run ends inside the
// architect route, and what happens after a certified revision is not this
// witness's. Fails if a dispute is reported through the ordinary
// non-convergence path (the architect is never reached), if it discharges the
// finding (the reviewer is asked again), or if the finding leaves the open
// review or changes class before the architect answers.
func TestW6ADisputedClassIsEscalatedAndDoesNotDischargeTheFinding(t *testing.T) {
	escalatedTo := func(h *gateHarness) string {
		t.Helper()
		asked := t.TempDir() + "/architect-prompt"
		script := t.TempDir() + "/architect.sh"
		if err := os.WriteFile(script, []byte("cat >> '"+asked+"'\n"+
			`echo '{"decision":"proceed","summary":"the reviewer class stands","mode":"modify","files":["main.go"],"plan":"Answer f1 as code."}'`+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		h.engine.Config.Architect = config.Agent{Name: "architect-double", Command: "sh", Args: []string{script}, Graph: "none"}
		return asked
	}

	h, reviews := scriptedLoop(t, codeFinding, true,
		`{"finding_responses":[{"id":"f1","answered_by":"code","paths":["main.go"],"disputes_class":"evidence","reason":"this is proof only"}]}`)
	asked := escalatedTo(h)
	outcome, err := h.runTwoCycles()

	if outcome.Accepted() || err == nil {
		t.Fatalf("a disputed finding was discharged: outcome %q err %v", outcome, err)
	}
	if strings.Contains(err.Error(), "the candidate did not converge") {
		t.Fatalf("the dispute went down the ordinary non-convergence path instead of escalating: %v", err)
	}
	prompt, rerr := os.ReadFile(asked)
	if rerr != nil {
		t.Fatalf("the dispute never reached the architect, so it was not escalated: %v (run ended: %v)", rerr, err)
	}
	for _, want := range []string{"CLASSIFICATION DISPUTES", `"id": "f1"`, `"disputes_class": "evidence"`, "[f1] code finding"} {
		if !strings.Contains(string(prompt), want) {
			t.Fatalf("the escalation does not carry %q:\n%s", want, prompt)
		}
	}
	if got := reviewCalls(t, reviews); got != "1" {
		t.Fatalf("the reviewer was asked %s times after a dispute", got)
	}
	open, ok := h.engine.openReview("task-1")
	if !ok || len(open.Findings) != 1 || open.Findings[0].ID != "f1" || open.Findings[0].Class != roles.CodeFinding {
		t.Fatalf("the escalation did not leave the disputed finding open under the reviewer's class: %+v (open=%v)", open.Findings, ok)
	}
	escalated := false
	for _, ev := range drainEvents(h.events) {
		if strings.Contains(string(ev.Payload), "classification_disputes") && strings.Contains(string(ev.Payload), `"disputes_class":"evidence"`) {
			escalated = true
		}
	}
	if !escalated {
		t.Fatal("the classification dispute was not escalated on the record")
	}

	control, controlReviews := scriptedLoop(t, codeFinding, true,
		`{"finding_responses":[{"id":"f1","answered_by":"code","paths":["main.go"]}]}`)
	controlAsked := escalatedTo(control)
	if outcome, err := control.runTwoCycles(); err != nil || !outcome.Accepted() {
		t.Fatalf("control: the same cycle without the dispute did not converge: outcome %q err %v", outcome, err)
	}
	if got := reviewCalls(t, controlReviews); got != "2" {
		t.Fatalf("control: the reviewer was asked %s times", got)
	}
	if _, err := os.Stat(controlAsked); err == nil {
		t.Fatal("control: the architect was consulted without a dispute, so the route above is not the dispute's")
	}
}

// W7 THE BOUNDARY IS THE BOUNDARY -- CRITICAL CONTROL.
//
// One classless finding. It decodes, validates as an ordinary ReviewVerdict and
// round-trips through serialization with its absence intact -- and it is
// refused at ReviewResult construction, by every constructor, and by the engine
// ingress that calls them. The classed control proves the refusal is the class
// and nothing else about the verdict.
//
// Fails if the class requirement moves into decoding or ReviewVerdict.Validate
// (the first half breaks), or leaves the constructors (the second half does).
func TestW7AClasslessFindingIsValidWireDataAndRefusedOnlyAtTheBoundary(t *testing.T) {
	const wire = `{"provenance":{"task_id":"task-1","role":"reviewer","provider":"codex","session_mode":"fresh","base_sha":"abc","candidate_digest":"cccc"},` +
		`"decision":"revise","summary":"not proven","findings":[{"id":"f1","severity":"blocking","claim":"the plan is not refused","reference":"main.go","reason":"it continues"}]}`
	binding := roles.Binding{TaskID: "task-1", BaseSHA: "abc", CandidateDigest: "cccc"}

	// The wire format stays permissive: no decode, validity or round-trip error.
	var v roles.ReviewVerdict
	if err := json.Unmarshal([]byte(wire), &v); err != nil {
		t.Fatalf("a classless finding no longer decodes: %v", err)
	}
	if v.Findings[0].Class != "" {
		t.Fatalf("decoding invented a class: %q", v.Findings[0].Class)
	}
	if err := v.Validate(binding, "claude"); err != nil {
		t.Fatalf("a classless finding is no longer a valid ReviewVerdict: %v", err)
	}
	if err := roles.NewAdvisory(v).Validate(binding, "claude"); err == nil {
		t.Fatal("the advisory copy must fail on its fresh provenance, or it is not the same verdict under test")
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("a classless finding no longer serializes: %v", err)
	}
	var back roles.ReviewVerdict
	if err := json.Unmarshal(raw, &back); err != nil || back.Findings[0].Class != "" || strings.Contains(string(raw), `"class"`) {
		t.Fatalf("the round trip did not preserve the absence: %s (%v)", raw, err)
	}

	// The membrane refuses it, at every constructor, naming the finding.
	if _, err := independentReview(v); err == nil || !strings.Contains(err.Error(), errUnclassifiedFinding.Error()) || !strings.Contains(err.Error(), "f1") {
		t.Fatalf("independentReview admitted a classless finding: %v", err)
	}
	unverified := v
	unverified.Provenance.SessionMode = roles.Unverified
	refused := advisoryReview(roles.NewAdvisory(unverified))
	if refused.Refused() == nil || refused.Advisory() || refused.SatisfiesAdversarialObligation() || refused.Decision() != "" {
		t.Fatalf("advisoryReview gave a classless finding standing: refused=%v decision=%q", refused.Refused(), refused.Decision())
	}
	if _, err := attestedReview(roles.NewAdvisory(unverified), roles.Attestation{}, ""); err == nil || !strings.Contains(err.Error(), errUnclassifiedFinding.Error()) {
		t.Fatalf("attestedReview did not refuse the class before anything else: %v", err)
	}
	// An invented class is refused as surely as an absent one.
	invented := v
	invented.Findings = []roles.Finding{v.Findings[0]}
	invented.Findings[0].Class = "severe"
	if _, err := independentReview(invented); err == nil {
		t.Fatal("an invented class crossed the boundary")
	}

	// The control: the same verdict with a reviewer-owned class is admitted.
	classed := v
	classed.Findings = []roles.Finding{v.Findings[0]}
	classed.Findings[0].Class = roles.CodeFinding
	if res, err := independentReview(classed); err != nil || res.Decision() != roles.Revise {
		t.Fatalf("the classed control was refused, so the refusal above is not about the class: %v", err)
	}
	if res := advisoryReview(roles.NewAdvisory(classed)); res.Refused() != nil || !res.Advisory() {
		t.Fatalf("the classed advisory control was refused: %v", res.Refused())
	}

	// And the engine ingress routes it as a review refusal, never as a verdict.
	for _, mode := range []roles.Session{roles.Fresh, roles.Unverified} {
		e, _ := reviewEngine(t, answeringRunner{text: wire, mode: mode}, "codex")
		res, err := e.resolveReview(context.Background(), "task-1",
			roles.Assignment{Role: roles.Reviewer, Provider: "codex"}, packetFor(reviewBinding()), "claude")
		if err == nil || !strings.Contains(err.Error(), "review refused") || !strings.Contains(err.Error(), errUnclassifiedFinding.Error()) {
			t.Fatalf("%s: a classless verdict crossed the engine ingress: %v", mode, err)
		}
		if res.Decision() != "" || len(res.Verdict().Findings) != 0 {
			t.Fatalf("%s: a refused verdict still carried findings into repair state: %+v", mode, res.Verdict())
		}
	}
}

// RETAINED EVIDENCE IS A CYCLE OUTPUT: the loop-level witnesses.
//
// Measured on the DF-19 resume, 2026-09-25: a review demanded failing-first
// history as EVIDENCE, the implementer produced exactly that, the diff was
// correctly unchanged -- and the run ended "the candidate did not change between
// review cycles", because the proof lived only in a transcript. These drive the
// REAL candidate loop for two cycles. The broker executes a real witness that
// PASSES on the base and FAILS on the candidate for its own assertion -- the red
// phase the finding asks for -- and what is asserted is what the loop concluded
// and what durable task state holds afterwards.
//
// The witness is witnessCommand: it counts the empty-main line. The base's
// main.go is exactly that line, so on the base it prints 1 and exits zero; the
// worker rewrites main to print a number, so on the candidate it prints 0 and
// exits 1. The broker's baseline attribution therefore finds a CANDIDATE
// failure, not a pre-existing one.

const (
	witnessCommand = "grep -c main() {} main.go"
	// failingFirstFinding demands a failing run, retained.
	failingFirstFinding = `{"id":"f2","severity":"major","class":"evidence","claim":"the failing-first history is established","reference":"main.go","reason":"comments describing the old bypass are not execution evidence","proof_gap":"the failing-first run of ` + witnessCommand + `, with its output retained"}`
	// secondEvidenceFinding demands a check this harness never executes.
	secondEvidenceFinding = `{"id":"f3","severity":"major","class":"evidence","claim":"the second witness has a red phase","reference":"main.go","reason":"no run","proof_gap":"the failing-first run of ls other_test.go"}`
	answerF2WithTheRun    = `{"id":"f2","answered_by":"evidence","evidence":"` + witnessCommand + `"}`
	// infraFinding demands the failing run of a check that fails identically on
	// the base: the file it lists exists in neither.
	infraFinding     = `{"id":"f2","severity":"major","class":"evidence","claim":"the failing-first history is established","reference":"main.go","reason":"no retained run","proof_gap":"the failing-first run of ls witness_test.go, with its output retained"}`
	answerF2InfraRun = `{"id":"f2","answered_by":"evidence","evidence":"ls witness_test.go"}`

	producedNothing         = "the candidate did not change between review cycles"
	producedNothingSame     = "produced an identical diff after being asked to revise"
	producedEvidenceNotCode = "produced evidence, not code"
)

// evidenceLoop is scriptedLoop with the broker executing the witness, which
// passes on the base and fails on the candidate.
func evidenceLoop(t *testing.T, findings, responses string) (*gateHarness, string) {
	t.Helper()
	return brokeredLoop(t, findings, responses, config.Command{Command: "grep", Args: []string{"-c", "main() {}", "main.go"}})
}

func brokeredLoop(t *testing.T, findings, responses string, check config.Command) (*gateHarness, string) {
	t.Helper()
	h, reviews := scriptedLoop(t, findings, false, `{"finding_responses":[`+responses+`]}`)
	h.engine.Config.Permissions.RunTests = true
	h.engine.Config.Validation.Test = []config.Command{check}
	return h, reviews
}

// durableRecord is the retained-evidence shape as it sits in the task-state
// FILE, decoded independently of the package that wrote it.
type durableRecord struct {
	Candidate struct {
		BaseSHA    string `json:"base_sha"`
		DiffDigest string `json:"diff_digest"`
	} `json:"candidate"`
	FindingID    string `json:"finding_id"`
	FindingClass string `json:"finding_class"`
	Command      string `json:"command"`
	Outcome      string `json:"outcome"`
	ExitStatus   int    `json:"exit_status"`
	Attribution  string `json:"attribution"`
	Output       string `json:"output"`
	OutputDigest string `json:"output_digest"`
}

// durableEvidence reads what the task-state file holds after the run: the
// durable state a later cycle or process reads, not the transcript.
func durableEvidence(t *testing.T, h *gateHarness) []durableRecord {
	t.Helper()
	raw, err := os.ReadFile(h.engine.Repo.Root + "/.sensei-code/tasks/task-1.json")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("the task-state file cannot be read: %v", err)
	}
	var file struct {
		Retained []durableRecord `json:"retained_evidence"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		t.Fatalf("the task-state file does not decode: %v", err)
	}
	return file.Retained
}

// W1 EVIDENCE-ONLY CYCLE CONVERGES. The only finding is EVIDENCE; the worker
// changes nothing and locates the failing run the finding asks for; the broker
// executed the witness, which passed on the base and failed on the candidate
// for its own assertion (the count is 0). The cycle is NOT "the candidate did
// not change": the record is in the task-state file, bound to this candidate
// and f2, FAILED and attributed to the candidate, with its output and digest;
// and the candidate goes back to the reviewer.
//
// The first control breaks only the durable write (the task-state directory is
// a file): the same response and the same broker run then answer nothing, and
// the reviewer is never asked again. So what carried the cycle is the record
// read back, not the response naming f2.
//
// The second control is a check that fails identically on candidate and base:
// the broker attributes it to infrastructure, it is not the candidate's red
// phase, and it retains nothing and answers nothing -- the cycle keeps the
// produced-nothing diagnosis and the reviewer is never asked again.
//
// Fails if a candidate failure is not retainable, if an infrastructure failure
// is relabelled as the failing run a finding asks for, if the discharge stops
// requiring a record read back from task state, or if the record loses its
// binding. That
// the review identity carries the record is pinned in reviewconsistency_test.go:
// in this harness the second review is not reached as a contradiction either
// way, so this witness does not decide it.
func TestW1PersistedEvidenceCarriesAnUnchangedEvidenceOnlyCycle(t *testing.T) {
	h, reviews := evidenceLoop(t, failingFirstFinding, answerF2WithTheRun)
	outcome, err := h.runTwoCycles()
	if err != nil && strings.Contains(err.Error(), producedNothing) {
		t.Fatalf("an evidence-only cycle that retained the proof it was asked for was reported as unchanged: %v", err)
	}
	if err != nil || !outcome.Accepted() {
		t.Fatalf("the evidence-only cycle did not converge: outcome %q err %v", outcome, err)
	}
	if got := reviewCalls(t, reviews); got != "2" {
		t.Fatalf("the reviewer was asked %s times; a durably answered finding goes back to the reviewer", got)
	}
	records := durableEvidence(t, h)
	if len(records) != 1 {
		t.Fatalf("the task-state file holds %d retained records, want the one the cycle produced: %+v", len(records), records)
	}
	r := records[0]
	if r.FindingID != "f2" || r.FindingClass != "evidence" || r.Command != witnessCommand ||
		r.Outcome != "FAILED" || r.ExitStatus != 1 || r.Attribution != "candidate" || strings.TrimSpace(r.Output) != "0" ||
		!strings.HasPrefix(r.OutputDigest, "sha256:") ||
		r.Candidate.BaseSHA != h.tc.Identity.BaseSHA || !strings.HasPrefix(r.Candidate.DiffDigest, "sha256:") {
		t.Fatalf("the durable record is not the failing run bound to this candidate and f2: %+v", r)
	}

	control, controlReviews := evidenceLoop(t, failingFirstFinding, answerF2WithTheRun)
	if err := os.MkdirAll(control.engine.Repo.Root+"/.sensei-code", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(control.engine.Repo.Root+"/.sensei-code/tasks", []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	outcome, err = control.runTwoCycles()
	if outcome.Accepted() || err == nil || !strings.Contains(err.Error(), "f2") || !strings.Contains(err.Error(), "could not be retained") {
		t.Fatalf("control: evidence that never reached durable state answered the finding: outcome %q err %v", outcome, err)
	}
	if got := reviewCalls(t, controlReviews); got != "1" {
		t.Fatalf("control: the reviewer was asked %s times", got)
	}

	infra, infraReviews := brokeredLoop(t, infraFinding, answerF2InfraRun, config.Command{Command: "ls", Args: []string{"witness_test.go"}})
	outcome, err = infra.runTwoCycles()
	if outcome.Accepted() || err == nil {
		t.Fatalf("infrastructure control: a failure the base shares answered the finding: outcome %q err %v", outcome, err)
	}
	if !strings.Contains(err.Error(), producedNothing) || strings.Contains(err.Error(), producedEvidenceNotCode) ||
		!strings.Contains(err.Error(), "is not this candidate's") {
		t.Fatalf("infrastructure control: the cycle is not reported as producing nothing for that reason: %v", err)
	}
	if got := reviewCalls(t, infraReviews); got != "1" {
		t.Fatalf("infrastructure control: the reviewer was asked %s times", got)
	}
	if records := durableEvidence(t, infra); len(records) != 0 {
		t.Fatalf("infrastructure control: a failure the base shares was retained as proof: %+v", records)
	}
}

// W3 PRODUCED NOTHING STILL FAILS -- CONTROL. The diff is unchanged and nothing
// durable was produced, however the worker describes its turn: silence, a
// claim naming f2 with a check that never executed, or an empty artifact. Each
// keeps the existing identical-diff diagnosis, is never the evidence diagnosis,
// retains nothing, and never reaches the reviewer again.
//
// Fails if naming a finding, or answering it with nothing, counts as producing
// something -- the run would converge, or leave the produced-nothing sentence.
func TestW3ATranscriptClaimOrEmptyArtifactStillProducedNothing(t *testing.T) {
	for name, responses := range map[string]string{
		"silence":          ``,
		"transcript claim": `{"id":"f2","answered_by":"evidence","evidence":"go test ./internal/workflow -run TestW4"}`,
		"empty artifact":   `{"id":"f2","answered_by":"evidence","evidence":""}`,
	} {
		h, reviews := evidenceLoop(t, failingFirstFinding, responses)
		outcome, err := h.runTwoCycles()
		if outcome.Accepted() || err == nil {
			t.Fatalf("%s: a cycle that produced nothing converged: outcome %q err %v", name, outcome, err)
		}
		if !strings.Contains(err.Error(), producedNothing) || !strings.Contains(err.Error(), producedNothingSame) {
			t.Fatalf("%s: the produced-nothing diagnosis was not kept: %v", name, err)
		}
		if strings.Contains(err.Error(), producedEvidenceNotCode) {
			t.Fatalf("%s: a cycle that retained nothing was reported as producing evidence: %v", name, err)
		}
		if got := reviewCalls(t, reviews); got != "1" {
			t.Fatalf("%s: the reviewer was asked %s times", name, got)
		}
		if records := durableEvidence(t, h); len(records) != 0 {
			t.Fatalf("%s: a claim became a durable record: %+v", name, records)
		}
	}
}

// W4 CODE-REQUIRING FINDING STILL BLOCKS -- CONTROL. A CODE finding beside the
// failing-first EVIDENCE finding. The worker retains the proof for f2 and
// answers f1 either by claiming code it did not change or by citing the same
// executed check as "evidence". The cycle does not converge, the diagnosis is
// the evidence-not-code one naming f1, and the task-state file holds a record
// for f2 only: no execution is ever retained for a finding the reviewer classed
// code.
//
// The predicate half removes the class-mismatch guard from the path: f1 is
// answered AS CODE and a durable record for f1 is present, on a candidate that
// did not move. It stays open, so the refusal is the class rule, not the
// response's wording.
//
// Fails if evidence discharges a code finding, if a record is retained for one,
// or if produced-evidence is treated as convergence.
func TestW4RetainedEvidenceNeverDischargesACodeFinding(t *testing.T) {
	for _, f1 := range []string{
		`{"id":"f1","answered_by":"evidence","evidence":"ls witness_test.go"}`,
		`{"id":"f1","answered_by":"code","paths":["main.go"]}`,
	} {
		h, reviews := evidenceLoop(t, codeFinding+","+failingFirstFinding, f1+","+answerF2WithTheRun)
		outcome, err := h.runTwoCycles()
		if outcome.Accepted() || err == nil {
			t.Fatalf("%s: evidence carried a cycle with an open CODE finding: outcome %q err %v", f1, outcome, err)
		}
		if !strings.Contains(err.Error(), producedEvidenceNotCode) || !strings.Contains(err.Error(), "[f1] code finding") {
			t.Fatalf("%s: the diagnosis does not say evidence was produced and f1 still owes code: %v", f1, err)
		}
		if got := reviewCalls(t, reviews); got != "1" {
			t.Fatalf("%s: the reviewer was asked %s times", f1, got)
		}
		records := durableEvidence(t, h)
		if len(records) != 1 || records[0].FindingID != "f2" {
			t.Fatalf("%s: the durable records are not exactly f2's: %+v", f1, records)
		}
	}

	var code roles.Finding
	if err := json.Unmarshal([]byte(codeFinding), &code); err != nil {
		t.Fatal(err)
	}
	a := accountForFindings([]roles.Finding{code},
		[]findingResponse{{ID: "f1", AnsweredBy: roles.CodeFinding, Paths: []string{"main.go"}}},
		map[string]bool{}, n2bBundle("ok"), map[string]string{"f1": "sha256:forged"})
	if a.Settled() || len(a.Evidenced) != 0 {
		t.Fatalf("a durable record discharged a CODE finding on an unmoved candidate: %+v", a)
	}
}

// W5 THE TWO DIAGNOSES DIFFER. Produced-nothing and produced-evidence-not-code,
// from the real loop, are different sentences an operator can tell apart, each
// carrying its own marker and not the other's. The third outcome -- evidence
// retained for one EVIDENCE finding while another EVIDENCE finding is open --
// is neither: it names the finding still open.
//
// Fails if the diagnoses collapse into one sentence, or if one retained record
// reports "evidence, not code" while evidence is still owed.
func TestW5ProducedNothingAndProducedEvidenceNotCodeAreDistinctDiagnoses(t *testing.T) {
	run := func(findings, responses string) string {
		t.Helper()
		h, _ := evidenceLoop(t, findings, responses)
		outcome, err := h.runTwoCycles()
		if outcome.Accepted() || err == nil {
			t.Fatalf("%s: converged: %q", responses, outcome)
		}
		return err.Error()
	}
	nothing := run(failingFirstFinding, ``)
	notCode := run(codeFinding+","+failingFirstFinding, answerF2WithTheRun)

	if nothing == notCode {
		t.Fatalf("the two diagnoses are the same sentence: %s", nothing)
	}
	if !strings.Contains(nothing, producedNothing) || strings.Contains(nothing, producedEvidenceNotCode) {
		t.Fatalf("produced-nothing does not read as its own diagnosis: %s", nothing)
	}
	if !strings.Contains(notCode, producedEvidenceNotCode) || strings.Contains(notCode, producedNothing) ||
		!strings.Contains(notCode, "answers f2") {
		t.Fatalf("produced-evidence-not-code does not read as its own diagnosis: %s", notCode)
	}

	partial := run(failingFirstFinding+","+secondEvidenceFinding, answerF2WithTheRun)
	if strings.Contains(partial, producedEvidenceNotCode) || strings.Contains(partial, producedNothing) ||
		!strings.Contains(partial, "[f3]") || strings.Contains(partial, "[f2]") {
		t.Fatalf("with evidence still owed, the diagnosis must name the open finding only: %s", partial)
	}
}

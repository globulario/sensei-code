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
	// A durable session record, as every production engine has: evidence a
	// review demanded is retained there, and nowhere else.
	store, err := session.New(dir+"/record", h.engine.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	h.engine.Store = store
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
// What happens after the discharge is not this witness's: whether the evidence
// is retained, and what the second review makes of it, are the retained-
// evidence witnesses below, so the assertion stops at the discharge.
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

// RETAINED EVIDENCE: the loop-level witnesses.
//
// Measured on the DF-19 resume, 2026-09-25: an EVIDENCE finding was answered
// with exactly the executed proof it asked for, the diff was rightly unchanged,
// and the run ended "the candidate did not change between review cycles". The
// proof lived only in a transcript. These drive the REAL candidate loop for two
// cycles; the durable record is read back from a freshly opened session store,
// never from the events the run published.

// evidenceLoop is scriptedLoop with the check the EVIDENCE finding's proof gap
// names configured, so an answer citing it is backed by an executed pass.
func evidenceLoop(t *testing.T, findings, answer string) (*gateHarness, string) {
	t.Helper()
	h, reviews := scriptedLoop(t, findings, false, answer)
	h.engine.Config.Permissions.RunTests = true
	h.engine.Config.Validation.Test = []config.Command{{Command: "test", Args: []string{"-s", "main.go"}}}
	return h, reviews
}

const answersEvidence = `{"finding_responses":[{"id":"f2","answered_by":"evidence","evidence":"test -s main.go"}]}`

// durableEvidence reads every retained record for the harness's task from a
// NEW store opened on the same session record: what survives the run.
func durableEvidence(t *testing.T, reviews string) []RetainedEvidence {
	t.Helper()
	store, err := session.New(strings.TrimSuffix(reviews, "/reviews")+"/record", "session-1")
	if err != nil {
		t.Fatal(err)
	}
	events, err := store.Load()
	if err != nil && !os.IsNotExist(err) {
		t.Fatalf("the durable record does not read back: %v", err)
	}
	var out []RetainedEvidence
	for _, ev := range events {
		if ev.Kind != event.FindingEvidenceRetained {
			continue
		}
		r, err := ParseRetainedEvidence(ev.Payload)
		if err != nil {
			t.Fatalf("a retained evidence record does not parse: %v", err)
		}
		out = append(out, r)
	}
	return out
}

// RETAINED W1 AN EVIDENCE-ONLY CYCLE CONVERGES. The worker changes nothing and
// answers the EVIDENCE finding with the executed check its proof gap names. The
// run is NOT reported as "the candidate did not change": the diagnosis is
// absent, the reviewer is asked again, and its ACCEPT stands -- the retained
// evidence is the evidence changing, so it is not read as a contradiction on
// an unchanged candidate.
//
// Fails if the identical-diff backstop decides over a settled account, or if
// retained evidence does not reach the evidence identity the second review is
// bound to (the ACCEPT then goes to the architect as a contradiction).
func TestRetainedW1AnEvidenceOnlyCycleIsNotReportedAsUnchanged(t *testing.T) {
	h, reviews := evidenceLoop(t, evidenceFinding, answersEvidence)
	outcome, err := h.runTwoCycles()

	if err != nil && strings.Contains(err.Error(), "did not change between review cycles") {
		t.Fatalf("an evidence-only cycle was reported as producing nothing: %v", err)
	}
	if err != nil || !outcome.Accepted() {
		t.Fatalf("an evidence-only cycle answered with the proof it asked for did not converge: outcome %q err %v", outcome, err)
	}
	if got := reviewCalls(t, reviews); got != "2" {
		t.Fatalf("the reviewer was asked %s times; the discharged finding goes back to the reviewer", got)
	}
	for _, ev := range drainEvents(h.events) {
		if ev.Kind == event.ReviewContradiction {
			t.Fatalf("the ACCEPT after retained evidence was read as a contradiction on nothing: %s", ev.Summary)
		}
	}
}

// RETAINED W2 THE EVIDENCE IS DURABLE. After the run, a new store on the same
// session record holds exactly one record, bound to the task, the finding (by
// id, class and content), the candidate the review was raised on, the candidate
// the check ran on, and the executed check itself.
//
// The control is the same cycle with no durable record: the evidence cannot be
// retained, so it is not credited -- the run stops before the reviewer, naming
// the retention, rather than converging on proof that exists nowhere.
//
// Fails if the record is written anywhere but the durable session record, if
// any binding field is missing, or if an unretainable discharge is credited.
func TestRetainedW2TheEvidenceIsReadableFromDurableState(t *testing.T) {
	h, reviews := evidenceLoop(t, evidenceFinding, answersEvidence)
	if _, err := h.runTwoCycles(); err != nil {
		t.Fatalf("the evidence-only run failed: %v", err)
	}
	records := durableEvidence(t, reviews)
	if len(records) != 1 {
		t.Fatalf("want exactly one retained record, found %d: %+v", len(records), records)
	}
	var f roles.Finding
	if err := json.Unmarshal([]byte(evidenceFinding), &f); err != nil {
		t.Fatal(err)
	}
	r := records[0]
	if r.TaskID != "task-1" || r.FindingID != "f2" || r.FindingClass != roles.EvidenceFinding || r.FindingDigest != findingDigest(f) {
		t.Fatalf("the record is not bound to the finding it answers: %+v", r)
	}
	if r.CandidateTree == "" || r.CandidateDigest == "" || r.ReviewedTree != r.CandidateTree || r.ReviewedDigest == "" {
		t.Fatalf("the record is not bound to the reviewed candidate: %+v", r)
	}
	if commandLine(r.Check) != "test -s main.go" || r.Check.Outcome != "passed" || r.Check.ExecutedBy == "" {
		t.Fatalf("the record does not carry the executed check: %+v", r.Check)
	}

	control, controlReviews := evidenceLoop(t, evidenceFinding, answersEvidence)
	control.engine.Store = nil
	outcome, err := control.runTwoCycles()
	if outcome.Accepted() || err == nil || !strings.Contains(err.Error(), "could not be retained durably") || !strings.Contains(err.Error(), "f2") {
		t.Fatalf("control: evidence with nowhere durable to go was credited: outcome %q err %v", outcome, err)
	}
	if got := reviewCalls(t, controlReviews); got != "1" {
		t.Fatalf("control: the reviewer was asked %s times over unretained evidence", got)
	}
}

// RETAINED W3 PRODUCED NOTHING STILL FAILS -- CONTROL. The worker changes
// nothing and says nothing. That is still non-convergence, and it still ends
// with the existing identical-diff diagnosis -- not the per-finding one, and not
// the evidence one -- and nothing is retained.
//
// Fails if the reorder lets a silent unchanged cycle through, or routes it to
// any diagnosis other than the identical-diff backstop's.
func TestRetainedW3ACycleThatProducedNothingKeepsItsDiagnosis(t *testing.T) {
	h, reviews := evidenceLoop(t, evidenceFinding, `nothing to report`)
	outcome, err := h.runTwoCycles()

	if outcome.Accepted() || err == nil {
		t.Fatalf("a cycle that produced nothing converged: outcome %q err %v", outcome, err)
	}
	if !strings.Contains(err.Error(), "did not change between review cycles") || !strings.Contains(err.Error(), "The last review asked for") {
		t.Fatalf("the produced-nothing cycle lost its identical-diff diagnosis: %v", err)
	}
	if strings.Contains(err.Error(), producedEvidenceNotCode) {
		t.Fatalf("a cycle that produced nothing was diagnosed as producing evidence: %v", err)
	}
	if got := reviewCalls(t, reviews); got != "1" {
		t.Fatalf("the reviewer was asked %s times", got)
	}
	if records := durableEvidence(t, reviews); len(records) != 0 {
		t.Fatalf("a cycle that produced nothing retained evidence: %+v", records)
	}
}

// RETAINED W3b A MALFORMED OR UNRELATED REPORT IS STILL PRODUCED NOTHING --
// CONTROL. The worker changes nothing and prints a finding accounting that
// does not decode, or one that decodes but names no outstanding finding.
// Neither is a durable response to anything, so each still ends with the
// existing identical-diff diagnosis -- not the per-finding one -- and nothing
// is retained. Whether the report happened to parse must not decide which
// non-convergence it is.
//
// Fails if produced-nothing is keyed on the report parsing cleanly rather than
// on the absence of any durable response: the malformed case then ends in the
// per-finding diagnosis, before the backstop.
func TestRetainedW3bAMalformedReportStillReachesTheIdenticalDiffDiagnosis(t *testing.T) {
	for name, answer := range map[string]string{
		"malformed": `{"finding_responses":[{"id":"f2","answered_by":"evidence",`,
		"unrelated": `{"finding_responses":[{"id":"f9","answered_by":"evidence","evidence":"test -s main.go"}]}`,
	} {
		h, reviews := evidenceLoop(t, evidenceFinding, answer)
		outcome, err := h.runTwoCycles()
		if outcome.Accepted() || err == nil {
			t.Fatalf("%s: a cycle that produced nothing converged: outcome %q err %v", name, outcome, err)
		}
		if !strings.Contains(err.Error(), "did not change between review cycles") || !strings.Contains(err.Error(), "The last review asked for") {
			t.Fatalf("%s: the produced-nothing cycle lost its identical-diff diagnosis: %v", name, err)
		}
		if strings.Contains(err.Error(), producedEvidenceNotCode) || strings.Contains(err.Error(), "[f2]") {
			t.Fatalf("%s: a cycle that produced nothing reached a per-finding diagnosis: %v", name, err)
		}
		if got := reviewCalls(t, reviews); got != "1" {
			t.Fatalf("%s: the reviewer was asked %s times", name, got)
		}
		if records := durableEvidence(t, reviews); len(records) != 0 {
			t.Fatalf("%s: a cycle that produced nothing retained evidence: %+v", name, records)
		}
	}
}

// RETAINED W2b RECOVERY IS BOUND TO THE EXACT REVIEWED CANDIDATE -- CONTROL. A
// record retained by one run is seeded into a fresh run whose worker says
// nothing: same task, same finding, same candidate, same check. Seeded as
// written, it is recovered and the cycle converges -- so the seeding itself is
// sound. Seeded with any other reviewed candidate, or any other candidate
// digest, it is NOT recovered: the silent cycle ends with the identical-diff
// diagnosis and the reviewer is never asked again. A record whose reviewed
// candidate is absent does not read back at all.
//
// Fails if recovery matches on anything less than the whole identity: the
// forged record then discharges the finding and the second review accepts.
func TestRetainedW2bARecordFromAnotherReviewedCandidateIsNotRecovered(t *testing.T) {
	source, sourceReviews := evidenceLoop(t, evidenceFinding, answersEvidence)
	if outcome, err := source.runTwoCycles(); err != nil || !outcome.Accepted() {
		t.Fatalf("the source run did not converge: outcome %q err %v", outcome, err)
	}
	records := durableEvidence(t, sourceReviews)
	if len(records) != 1 {
		t.Fatalf("want one source record, found %d", len(records))
	}
	seeded := func(r RetainedEvidence) (candidateOutcome, error, string) {
		t.Helper()
		h, reviews := evidenceLoop(t, evidenceFinding, `nothing to report`)
		if err := h.engine.Store.Append(event.New(h.engine.SessionID, "task-1", event.SourceSystem,
			event.FindingEvidenceRetained, r.Describe(), r)); err != nil {
			t.Fatal(err)
		}
		outcome, err := h.runTwoCycles()
		return outcome, err, reviewCalls(t, reviews)
	}

	if outcome, err, calls := seeded(records[0]); err != nil || !outcome.Accepted() || calls != "2" {
		t.Fatalf("positive control: the exact record was not recovered: outcome %q err %v reviews %s", outcome, err, calls)
	}
	for name, mutate := range map[string]func(*RetainedEvidence){
		"another reviewed digest":  func(r *RetainedEvidence) { r.ReviewedDigest = "sha256:another-review" },
		"another reviewed tree":    func(r *RetainedEvidence) { r.ReviewedTree = "another-reviewed-tree" },
		"another candidate digest": func(r *RetainedEvidence) { r.CandidateDigest = "sha256:another-candidate" },
	} {
		forged := records[0]
		mutate(&forged)
		outcome, err, calls := seeded(forged)
		if outcome.Accepted() || err == nil || calls != "1" {
			t.Fatalf("%s: a record bound elsewhere discharged the finding: outcome %q err %v reviews %s", name, outcome, err, calls)
		}
		if !strings.Contains(err.Error(), "did not change between review cycles") {
			t.Fatalf("%s: the silent cycle did not reach the identical-diff diagnosis: %v", name, err)
		}
	}

	unbound := records[0]
	unbound.ReviewedDigest, unbound.ReviewedTree = "", ""
	raw, _ := json.Marshal(unbound)
	if _, err := ParseRetainedEvidence(raw); err == nil || !strings.Contains(err.Error(), "reviewed candidate") {
		t.Fatalf("a record with no reviewed candidate was read back: %v", err)
	}
}

// RETAINED W4 A CODE-REQUIRING FINDING STILL BLOCKS -- CONTROL. The worker
// retains exactly the proof the EVIDENCE finding asked for and changes nothing
// for the CODE finding -- first silently, then by claiming the same check
// answers it. Neither converges: the reviewer is never asked again, the
// diagnosis names the code finding, and the evidence is kept for the cycle
// that makes the change. A retained record forged to match a CODE finding, one
// whose proof gap even names the check, discharges nothing.
//
// Fails if evidence -- fresh or retained -- discharges a code obligation: the
// loop would then reach the second review, which accepts.
func TestRetainedW4EvidenceNeverDischargesACodeObligation(t *testing.T) {
	for name, answer := range map[string]string{
		"silent on the code finding": answersEvidence,
		"code answered by evidence":  `{"finding_responses":[{"id":"f1","answered_by":"evidence","evidence":"test -s main.go"},{"id":"f2","answered_by":"evidence","evidence":"test -s main.go"}]}`,
	} {
		h, reviews := evidenceLoop(t, codeFinding+","+evidenceFinding, answer)
		outcome, err := h.runTwoCycles()
		if outcome.Accepted() || err == nil {
			t.Fatalf("%s: evidence discharged a CODE finding: outcome %q err %v", name, outcome, err)
		}
		if !strings.Contains(err.Error(), "[f1] code finding") {
			t.Fatalf("%s: the diagnosis does not name the outstanding code finding: %v", name, err)
		}
		if got := reviewCalls(t, reviews); got != "1" {
			t.Fatalf("%s: the reviewer was asked %s times; an owed code change stops the cycle before review", name, got)
		}
		records := durableEvidence(t, reviews)
		if len(records) != 1 || records[0].FindingID != "f2" {
			t.Fatalf("%s: want the f2 evidence retained and nothing for f1: %+v", name, records)
		}
	}

	var code roles.Finding
	if err := json.Unmarshal([]byte(codeFinding), &code); err != nil {
		t.Fatal(err)
	}
	code.ProofGap = "a passing run of test -s main.go on this candidate"
	forged := RetainedEvidence{TaskID: "task-1", FindingID: code.ID, FindingClass: roles.EvidenceFinding,
		FindingDigest: findingDigest(code), CandidateDigest: "d", CandidateTree: "t"}
	forged.Check.Command, forged.Check.Args, forged.Check.Outcome = "test", []string{"-s", "main.go"}, "passed"
	if a := accountForFindingsWith([]roles.Finding{code}, nil, map[string]bool{}, noChecks(accountForFindingsWith), []RetainedEvidence{forged}); a.Settled() || len(a.Evidence) != 0 {
		t.Fatalf("a retained record discharged a CODE finding: %+v", a)
	}
	raw, _ := json.Marshal(RetainedEvidence{TaskID: "task-1", FindingID: "f1", FindingClass: roles.CodeFinding,
		FindingDigest: "x", CandidateDigest: "d", CandidateTree: "t", Check: forged.Check})
	if _, err := ParseRetainedEvidence(raw); err == nil {
		t.Fatal("a retained record for a CODE finding was read back as evidence")
	}
}

// noChecks is an empty validation bundle, typed from the accounting function
// itself: the forged-record check above must be decided by the retained record
// alone, with no executed check this cycle to discharge anything.
func noChecks[B any](func([]roles.Finding, []findingResponse, map[string]bool, B, []RetainedEvidence) findingAccount) (empty B) {
	return empty
}

// RETAINED W5 THE TWO DIAGNOSES DIFFER. "Produced nothing" and "produced
// evidence, not code" end in the same terminal and must not share a sentence:
// each carries its own text, neither carries the other's, and only the second
// states itself as a typed record an operator can select on.
//
// Fails if the two are collapsed into one sentence, or if the evidence case
// stops recording its typed non-convergence reason.
func TestRetainedW5TheTwoDiagnosesAreDistinguishable(t *testing.T) {
	nothing, _ := evidenceLoop(t, evidenceFinding, `nothing to report`)
	_, nothingErr := nothing.runTwoCycles()
	produced, _ := evidenceLoop(t, codeFinding+","+evidenceFinding, answersEvidence)
	_, producedErr := produced.runTwoCycles()
	if nothingErr == nil || producedErr == nil {
		t.Fatalf("both cycles must fail to converge: nothing=%v produced=%v", nothingErr, producedErr)
	}

	const unchanged = "did not change between review cycles"
	if !strings.Contains(nothingErr.Error(), unchanged) || strings.Contains(nothingErr.Error(), producedEvidenceNotCode) {
		t.Fatalf("produced nothing: %v", nothingErr)
	}
	if !strings.Contains(producedErr.Error(), producedEvidenceNotCode) || strings.Contains(producedErr.Error(), unchanged) {
		t.Fatalf("produced evidence, not code: %v", producedErr)
	}

	typed := func(events []event.Event) bool {
		for _, ev := range events {
			if strings.Contains(string(ev.Payload), `"non_convergence":"evidence_produced_change_owed"`) {
				return true
			}
		}
		return false
	}
	if !typed(drainEvents(produced.events)) {
		t.Fatal("produced evidence, not code: no typed non-convergence record")
	}
	if typed(drainEvents(nothing.events)) {
		t.Fatal("produced nothing was recorded as producing evidence")
	}
}

// RETAINED W6 IDEMPOTENCE -- CONTROL. The same evidence-only cycle is run again
// against the unchanged durable record, first answering again and then saying
// nothing at all. Both reach the same outcome, and the record still holds one
// retained record: both reruns recover the binding already written rather than
// writing it again, and the silent one is credited by reading it back rather
// than by being asked for the proof again.
//
// Fails if a fresh answer is preferred over the retained record (a duplicate is
// appended), or if retained evidence is not read back at all.
func TestRetainedW6RerunningTheSameCycleAddsNoDuplicateRecord(t *testing.T) {
	h, reviews := evidenceLoop(t, evidenceFinding, answersEvidence)
	if outcome, err := h.runTwoCycles(); err != nil || !outcome.Accepted() {
		t.Fatalf("the first run did not converge: outcome %q err %v", outcome, err)
	}
	rerun := func(name string) {
		t.Helper()
		for _, counter := range []string{reviews, strings.TrimSuffix(reviews, "reviews") + "cycles"} {
			if err := os.WriteFile(counter, []byte("0\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		outcome, err := h.runTwoCycles()
		if err != nil || !outcome.Accepted() {
			t.Fatalf("%s: the same cycle against the same durable state reached a different outcome: %q %v", name, outcome, err)
		}
		if got := reviewCalls(t, reviews); got != "2" {
			t.Fatalf("%s: the reviewer was asked %s times", name, got)
		}
		if records := durableEvidence(t, reviews); len(records) != 1 {
			t.Fatalf("%s: the rerun accumulated retained records: %d", name, len(records))
		}
	}
	rerun("answering again")

	script := h.worker.Args[0]
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	silent := strings.Replace(string(body), answersEvidence, "nothing to report", 1)
	if silent == string(body) {
		t.Fatal("the worker script does not carry the answer, so the silent rerun would test nothing")
	}
	if err := os.WriteFile(script, []byte(silent), 0o755); err != nil {
		t.Fatal(err)
	}
	rerun("silent, credited from the durable record")
}

// redFinding demands a red phase: the named witness must be RUN and must FAIL.
// test -s absent.go executes and exits non-zero, as a witness run against a
// preserved pre-repair implementation does.
const (
	redFinding = `{"id":"f2","severity":"major","class":"evidence","claim":"the failing-first history is established","reference":"main.go","reason":"comments describing the old bypass are not execution evidence","proof_gap":"retain the failing output of test -s absent.go"}`
	answersRed = `{"finding_responses":[{"id":"f2","answered_by":"evidence","evidence":"test -s absent.go"}]}`
)

// RETAINED W1b AN EXECUTED FAILURE THE REVIEW REQUIRED IS RETAINED -- the
// measured DF-19 case. The review demands the failing output of a named check;
// the broker executes it on the unchanged candidate and it fails. That failure
// is the proof: it is retained durably, as a failure, and the cycle is not
// reported as "the candidate did not change". Rerun silently on the same
// candidate, it is recovered from the durable record and not written again.
//
// The control is the same answer where the check never executed (the envelope
// does not permit tests): nothing is retained, nothing is credited, and the
// diagnosis names the check as not executed.
//
// Fails if evidence discharge or readback requires a PASSED outcome (the red
// phase is then refused and the run never reaches the second review), or if a
// check that did not execute is accepted as evidence (the control then
// retains it and converges).
func TestRetainedW1bAnExecutedFailureTheReviewRequiredIsRetained(t *testing.T) {
	h, reviews := scriptedLoop(t, redFinding, false, answersRed)
	h.engine.Config.Permissions.RunTests = true
	h.engine.Config.Validation.Test = []config.Command{{Command: "test", Args: []string{"-s", "absent.go"}}}
	outcome, err := h.runTwoCycles()
	if err != nil && strings.Contains(err.Error(), "did not change between review cycles") {
		t.Fatalf("a red-phase evidence cycle was reported as producing nothing: %v", err)
	}
	if err != nil || !outcome.Accepted() {
		t.Fatalf("the executed failure the review required was not credited: outcome %q err %v", outcome, err)
	}
	if got := reviewCalls(t, reviews); got != "2" {
		t.Fatalf("the reviewer was asked %s times", got)
	}
	records := durableEvidence(t, reviews)
	if len(records) != 1 {
		t.Fatalf("want exactly one retained record, found %d: %+v", len(records), records)
	}
	r := records[0]
	if commandLine(r.Check) != "test -s absent.go" || r.Check.ExitStatus == 0 || r.Check.OutputDigest == "" ||
		(r.Check.Outcome != "candidate-failure" && r.Check.Outcome != "infrastructure-failure") {
		t.Fatalf("the red phase was not retained as the executed failure it was: %+v", r.Check)
	}
	if r.FindingID != "f2" || r.CandidateDigest == "" || r.ReviewedTree != r.CandidateTree {
		t.Fatalf("the record is not bound to the finding and the unchanged candidate: %+v", r)
	}

	// Recovered on the unchanged candidate: a silent rerun converges on the
	// durable record alone and writes nothing new.
	for _, counter := range []string{reviews, strings.TrimSuffix(reviews, "reviews") + "cycles"} {
		if err := os.WriteFile(counter, []byte("0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	script := h.worker.Args[0]
	body, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	silent := strings.Replace(string(body), answersRed, "nothing to report", 1)
	if silent == string(body) {
		t.Fatal("the worker script does not carry the answer, so the silent rerun would test nothing")
	}
	if err := os.WriteFile(script, []byte(silent), 0o755); err != nil {
		t.Fatal(err)
	}
	if outcome, err := h.runTwoCycles(); err != nil || !outcome.Accepted() {
		t.Fatalf("the retained red phase was not recovered on the unchanged candidate: outcome %q err %v", outcome, err)
	}
	if records := durableEvidence(t, reviews); len(records) != 1 {
		t.Fatalf("the recovery accumulated retained records: %d", len(records))
	}

	control, controlReviews := scriptedLoop(t, redFinding, false, answersRed)
	control.engine.Config.Permissions.RunTests = false
	control.engine.Config.Validation.Test = []config.Command{{Command: "test", Args: []string{"-s", "absent.go"}}}
	outcome, err = control.runTwoCycles()
	if outcome.Accepted() || err == nil || !strings.Contains(err.Error(), "names no check the broker executed") {
		t.Fatalf("control: a check that never executed was credited as evidence: outcome %q err %v", outcome, err)
	}
	if records := durableEvidence(t, controlReviews); len(records) != 0 {
		t.Fatalf("control: a check that never executed was retained: %+v", records)
	}
	// The same executed failure does not answer a reviewer who asked for a
	// pass, fresh or retained: the result's meaning is the reviewer's to set.
	var passing roles.Finding
	if err := json.Unmarshal([]byte(redFinding), &passing); err != nil {
		t.Fatal(err)
	}
	passing.ProofGap = "a passing run of test -s absent.go on this candidate"
	fresh := noChecks(accountForFindingsWith)
	fresh.Checks = append(fresh.Checks, r.Check)
	cites := []findingResponse{{ID: "f2", AnsweredBy: roles.EvidenceFinding, Evidence: "test -s absent.go"}}
	if a := accountForFindingsWith([]roles.Finding{passing}, cites, map[string]bool{}, fresh, nil); a.Settled() {
		t.Fatal("an executed failure answered a finding that asked for a passing run")
	}
	passingRecord := r
	passingRecord.FindingDigest = findingDigest(passing)
	if a := accountForFindingsWith([]roles.Finding{passing}, nil, map[string]bool{}, noChecks(accountForFindingsWith), []RetainedEvidence{passingRecord}); a.Settled() {
		t.Fatal("a retained failure was recovered for a finding that asked for a passing run")
	}
	for never, mark := range map[string]func(*RetainedEvidence){
		"not-permitted": func(u *RetainedEvidence) { u.Check.Outcome = "not-permitted" },
		"errored":       func(u *RetainedEvidence) { u.Check.Outcome = "errored" },
	} {
		unexecuted := r
		mark(&unexecuted)
		raw, _ := json.Marshal(unexecuted)
		if _, err := ParseRetainedEvidence(raw); err == nil {
			t.Fatalf("a retained %s check was read back as evidence", never)
		}
	}
}

// RETAINED W5b PARTLY ANSWERED EVIDENCE IS NOT "A CHANGE OWED" -- CONTROL. Two
// EVIDENCE findings; the cycle retains the proof for one and is silent on the
// other. That has not converged, but nothing still open owes a change: the
// diagnosis names the outstanding evidence finding, and neither the
// evidence-not-code sentence nor its typed record appears.
//
// Fails if "produced evidence, not code" is chosen on retained evidence alone
// rather than on a CODE or SCOPE finding remaining open.
func TestRetainedW5bPartlyAnsweredEvidenceIsNotDiagnosedAsAChangeOwed(t *testing.T) {
	other := `{"id":"f3","severity":"major","class":"evidence","claim":"the second witness is established","reference":"main.go","reason":"no run of it","proof_gap":"a run of test -s other.go"}`
	h, reviews := evidenceLoop(t, evidenceFinding+","+other, answersEvidence)
	outcome, err := h.runTwoCycles()
	if outcome.Accepted() || err == nil {
		t.Fatalf("a cycle leaving an evidence finding unanswered converged: outcome %q err %v", outcome, err)
	}
	if !strings.Contains(err.Error(), "[f3] evidence finding") {
		t.Fatalf("the diagnosis does not name the outstanding evidence finding: %v", err)
	}
	if strings.Contains(err.Error(), producedEvidenceNotCode) || strings.Contains(err.Error(), "did not change between review cycles") {
		t.Fatalf("an outstanding evidence finding was diagnosed as a change owed or as produced nothing: %v", err)
	}
	for _, ev := range drainEvents(h.events) {
		if strings.Contains(string(ev.Payload), `"non_convergence":"evidence_produced_change_owed"`) {
			t.Fatalf("an outstanding evidence finding was recorded as a change owed: %s", ev.Summary)
		}
	}
	if got := reviewCalls(t, reviews); got != "1" {
		t.Fatalf("the reviewer was asked %s times", got)
	}
	if records := durableEvidence(t, reviews); len(records) != 1 || records[0].FindingID != "f2" {
		t.Fatalf("want the f2 evidence retained and nothing for f3: %+v", records)
	}
}

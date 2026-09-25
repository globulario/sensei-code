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
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Fresh, "revise")
	// A finding carries its class, and a verdict whose finding does not is
	// refused; the shared harness's revise payload predates the class.
	h.engine.Runners = &scriptedReviews{verdicts: []string{`{"decision":"revise","summary":"the proof is missing",
 "findings":[{"id":"1","class":"evidence","severity":"blocking","claim":"the test does not fail without the fix","reference":"main.go","reason":"no mutation"}]}`}}
	return h
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

// FINDING-OWNED CONVERGENCE (measured 2026-09-25 on the DF-19 resume): a review
// returned a CODE finding and an EVIDENCE finding, the implementer answered the
// evidence one and called the whole review evidence-only, and the code defect
// was carried forward as answered. The engine caught it only because the diff
// happened not to move. These witnesses drive the REAL candidate loop.

// scriptedReviews answers each review turn with the next verdict in order and
// every other role with the configured command line. It counts the review
// turns it served, so a witness can say whether the reviewer was ever reached.
type scriptedReviews struct {
	verdicts []string
	asked    int
	// roles is every role a runner was resolved for, in order.
	roles []roles.Role
	// architect, when set, answers every architect turn.
	architect string
}

func (s *scriptedReviews) Resolve(spec RunnerSpec) (Resolved, error) {
	s.roles = append(s.roles, spec.Role)
	if spec.Role == roles.Architect && s.architect != "" {
		return Resolved{Runner: answeringRunner{text: s.architect}, Name: "chatgpt", Label: "ChatGPT"}, nil
	}
	if spec.Role != roles.Reviewer {
		return CLIResolved(spec, "session-1"), nil
	}
	text := s.verdicts[len(s.verdicts)-1]
	if s.asked < len(s.verdicts) {
		text = s.verdicts[s.asked]
	}
	s.asked++
	return Resolved{Runner: answeringRunner{text: text, mode: roles.Fresh}, Name: "remote:abc", Label: "remote:abc"}, nil
}

// codeAndEvidenceReview is the measured DF-19 shape: one CODE finding about a
// fail-open path and one EVIDENCE finding about missing failing-first history.
const codeAndEvidenceReview = `{"decision":"revise","summary":"a CREATE that cannot bind does not refuse the plan, and the failing-first history is missing",
 "findings":[
  {"id":"f1","class":"code","severity":"blocking","claim":"a CREATE declaration that cannot be bound refuses the plan","reference":"internal/workflow/route.go","reason":"routePlan emits each createRefusal as an event and continues","correction":"return a typed routing refusal whenever a CREATE entry fails to bind"},
  {"id":"f2","class":"evidence","severity":"major","claim":"W4, W5 and W6 failed before the repair","reference":"the witness record","reason":"no failing-first output was supplied","proof_gap":"run the witnesses against the pre-repair implementation"}]}`

const acceptingReview = `{"decision":"accept","summary":"the candidate stands"}`

// unrelatedEditor is an implementer that edits ONE UNRELATED LINE of main.go --
// a comment -- and prints whatever response it is given. With moves, the line
// carries a cycle counter, so every diff differs from the last; without, every
// cycle writes the same bytes. It never touches what a finding names.
func unrelatedEditor(t *testing.T, response string, moves bool) config.Agent {
	t.Helper()
	counter := t.TempDir() + "/cycles"
	script := `cat >/dev/null
n=$(cat "$1" 2>/dev/null || echo 0); n=$((n+1)); echo "$n" > "$1"
[ "$3" = moves ] || n=same
printf 'package main\n\n// unrelated edit %s\nfunc main() {}\n' "$n" > main.go
printf '%s\n' "$2"`
	mode := "still"
	if moves {
		mode = "moves"
	}
	return config.Agent{Name: "claude", Graph: "none", Command: "/bin/sh", Args: []string{"-c", script, "sh", counter, response, mode}}
}

// findingsHarness is the gate harness with a scripted reviewer sequence and the
// unrelated-line implementer, given enough cycles to be asked twice.
func findingsHarness(t *testing.T, response string, moves bool, verdicts ...string) (*gateHarness, *scriptedReviews) {
	t.Helper()
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Fresh, "accept")
	reviews := &scriptedReviews{verdicts: verdicts}
	h.engine.Runners = reviews
	h.worker = unrelatedEditor(t, response, moves)
	h.engine.Config.Implementors = []config.Agent{h.worker}
	h.engine.Config.Workflow.ReviewCycles = 3
	return h, reviews
}

func runFindings(h *gateHarness) (candidateOutcome, error) {
	outcome, _, _, _, err := h.engine.runCandidate(context.Background(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	return outcome, err
}

// W3 THE PROXY IS NOT THE CHECK -- CRITICAL CONTROL. The second cycle changes an
// unrelated line, so the diff differs and the identical-diff check passes, while
// the CODE finding is never addressed. The next reviewer would accept; the run
// must not converge on that, and must name the finding.
//
// Fails if: convergence is decided by diff movement (the pre-repair loop
// accepted here), or the accounting stops naming the open finding by id.
func TestFindingW3AnUnrelatedDiffChangeDoesNotDischargeACodeFinding(t *testing.T) {
	h, reviews := findingsHarness(t, "changed a comment", true, codeAndEvidenceReview, acceptingReview)
	outcome, err := runFindings(h)

	if outcome == candidateAccepted {
		t.Fatalf("an unrelated line change carried an unaddressed CODE finding to acceptance (reviews asked: %d)", reviews.asked)
	}
	if err == nil || !strings.Contains(err.Error(), "f1") {
		t.Fatalf("the non-convergence does not name the open CODE finding f1: %v", err)
	}
	if strings.Contains(err.Error(), "identical diff") {
		t.Fatalf("the run was stopped by the diff backstop, not by the finding accounting: %v", err)
	}
	if reviews.asked != 1 {
		t.Fatalf("the unaccounted finding reached %d review turns; the accounting must stop the cycle before a second review", reviews.asked)
	}
}

// codeFindingInMainGo is a CODE finding naming the very file the unrelated-line
// implementer edits, so a path-level attribution would match it.
const codeFindingInMainGo = `{"decision":"revise","summary":"main does not print a number",
 "findings":[{"id":"f1","class":"code","severity":"blocking","claim":"main prints a number","reference":"main.go","reason":"main has an empty body","correction":"print the number"}]}`

// W3, SAME FILE -- CRITICAL CONTROL. The implementer edits an unrelated comment
// line in the very file the CODE finding names and cites that file as the
// change answering it. The attribution is real movement in the right file and
// still not an answer: only the finding's reviewer, naming f1 as resolved, can
// discharge it, and a blind ACCEPT that does not is not convergence.
//
// Fails if: same-file movement discharges the finding (the path-overlap reading
// accepted here), or the reviewer's silence on f1 is read as its resolution.
func TestFindingW3AnUnrelatedLineInTheNamedFileDoesNotDischargeACodeFinding(t *testing.T) {
	h, reviews := findingsHarness(t,
		`FINDING-RESPONSES: {"responses":[{"id":"f1","response":"code","paths":["main.go"]}]}`,
		true, codeFindingInMainGo, acceptingReview)
	outcome, err := runFindings(h)

	if outcome == candidateAccepted {
		t.Fatalf("an unrelated line in the named file carried the CODE finding to acceptance (reviews asked: %d)", reviews.asked)
	}
	if err == nil || !strings.Contains(err.Error(), "[f1] code finding") {
		t.Fatalf("the non-convergence does not name the open CODE finding f1: %v", err)
	}
	if strings.Contains(err.Error(), "identical diff") {
		t.Fatalf("the run was stopped by the diff backstop, not by the finding accounting: %v", err)
	}
	if reviews.asked != 2 {
		t.Fatalf("the attributed answer reached %d review turns; its reviewer must be asked once to confirm it (2)", reviews.asked)
	}
}

// accountingEvents are the per-cycle finding accounting records the run emitted.
func accountingEvents(h *gateHarness) []string {
	var out []string
	for _, ev := range drainEvents(h.events) {
		if strings.HasPrefix(ev.Summary, "review finding accounting: ") {
			out = append(out, ev.Summary)
		}
	}
	return out
}

// W1 THE MEASURED CASE. One CODE and one EVIDENCE finding, answered with
// evidence alone and the diff left as it was. The run does not converge and the
// diagnosis names the unaddressed CODE finding by id -- not merely "the
// candidate did not change".
//
// Fails if: the evidence answer is read as covering the review; the diagnosis
// stops naming f1; or the identical-diff backstop decides instead of the
// accounting (it would fire here too, which is why its message is excluded).
func TestFindingW1AnEvidenceAnswerDoesNotDischargeTheCodeFinding(t *testing.T) {
	h, reviews := findingsHarness(t,
		`FINDING-RESPONSES: {"responses":[{"id":"f2","response":"evidence","executions":["gofmt -l main.go"],"evidence":"go test -run TestW4 against the pre-repair tree: FAIL"}]}`,
		false, codeAndEvidenceReview, acceptingReview)
	outcome, err := runFindings(h)

	if outcome == candidateAccepted {
		t.Fatal("a review with a CODE finding converged on an evidence-only answer")
	}
	if err == nil || !strings.Contains(err.Error(), "[f1] code finding silent") {
		t.Fatalf("the diagnosis does not name the unaddressed CODE finding f1 by id: %v", err)
	}
	if strings.Contains(err.Error(), "[f2]") {
		t.Fatalf("the evidence finding answered with evidence is still reported open: %v", err)
	}
	if strings.Contains(err.Error(), "did not change between review cycles") {
		t.Fatalf("the diff backstop decided, not the finding accounting: %v", err)
	}
	if reviews.asked != 1 {
		t.Fatalf("the half-answered review reached %d review turns", reviews.asked)
	}
}

// W2 RECLASSIFICATION IS REFUSED, in the loop. The implementer answers the CODE
// finding as evidence-only. The class the loop judges by is the finding's.
//
// Fails if: a response's kind is allowed to stand in for the finding's class.
func TestFindingW2ACodeFindingAnsweredAsEvidenceIsNotDischarged(t *testing.T) {
	h, _ := findingsHarness(t,
		`FINDING-RESPONSES: {"responses":[{"id":"f1","response":"evidence","executions":["gofmt -l main.go"],"evidence":"the refusal is reported as status","claimed_class":"evidence"},{"id":"f2","response":"evidence","executions":["gofmt -l main.go"]}]}`,
		true, codeAndEvidenceReview, acceptingReview)
	outcome, err := runFindings(h)

	if outcome == candidateAccepted {
		t.Fatal("a CODE finding answered as evidence-only was discharged")
	}
	if err == nil || !strings.Contains(err.Error(), "[f1] code finding reclassification_refused") ||
		!strings.Contains(err.Error(), "the finding record says code") {
		t.Fatalf("the refusal does not take f1's class from the finding record: %v", err)
	}
}

// evidenceOnlyReview raises one EVIDENCE finding and nothing else.
const evidenceOnlyReview = `{"decision":"revise","summary":"the failing-first history is missing",
 "findings":[{"id":"f2","class":"evidence","severity":"major","claim":"W4 failed before the repair","reference":"the witness record","reason":"no output","proof_gap":"run it against the pre-repair tree"}]}`

// W4 EVIDENCE FINDING DISCHARGED BY EVIDENCE. An evidence-only review answered
// by citing a check the validation broker ran against the candidate, with no
// code change, goes back to its reviewer; that reviewer names f2 resolved, and
// the candidate is accepted on the unchanged bytes. Neither the identical-diff
// backstop nor the same-bytes contradiction check decides against it.
//
// Fails if: broker execution cannot answer an evidence finding, a discharge is
// taken without the reviewer naming the id, or a diff-based check still decides
// after every finding was accounted for.
func TestFindingW4AnEvidenceFindingIsDischargedByEvidence(t *testing.T) {
	h, reviews := findingsHarness(t,
		`FINDING-RESPONSES: {"responses":[{"id":"f2","response":"evidence","executions":["gofmt -l main.go"]}]}`,
		false, evidenceOnlyReview, `{"decision":"accept","summary":"the proof is now on the record","resolved":["f2"]}`)
	outcome, err := runFindings(h)
	seen := drainEvents(h.events)

	if outcome != candidateAccepted || err != nil {
		t.Fatalf("an evidence finding answered by broker execution and resolved by its reviewer did not converge: outcome=%q err=%v", outcome, err)
	}
	if reviews.asked != 2 {
		t.Fatalf("want the raising review and the resolving review (2), got %d", reviews.asked)
	}
	var accounted, settled bool
	for _, ev := range seen {
		accounted = accounted || strings.Contains(ev.Summary, "review finding accounting: all 1 outstanding finding(s) are answered")
		settled = settled || strings.Contains(ev.Summary, "review finding settlement: 1 answered finding(s) discharged by their reviewer")
		if ev.Kind == event.ReviewContradiction {
			t.Fatalf("a finding resolved by id was read as a contradiction of its review: %s", ev.Summary)
		}
	}
	if !accounted || !settled {
		t.Fatalf("the evidence finding was not answered and then discharged by its reviewer (accounted=%v settled=%v)", accounted, settled)
	}
}

// W4 CONTROL: the responder's own account of execution. Prose that says it
// ran, and a command line the broker never ran against this candidate, answer
// nothing: the run does not converge, names f2, and never reaches the reviewer.
//
// Fails if: a non-empty evidence string, or a cited command taken at the
// responder's word, is read as execution evidence.
func TestFindingW4ProseOrFabricatedEvidenceDoesNotAnswerAnEvidenceFinding(t *testing.T) {
	for name, response := range map[string]string{
		"prose":      `FINDING-RESPONSES: {"responses":[{"id":"f2","response":"evidence","evidence":"ran go test -run TestW4 at the base: FAIL; at the candidate: PASS"}]}`,
		"fabricated": `FINDING-RESPONSES: {"responses":[{"id":"f2","response":"evidence","executions":["go test ./..."],"evidence":"PASS"}]}`,
	} {
		h, reviews := findingsHarness(t, response, false, evidenceOnlyReview, `{"decision":"accept","summary":"s","resolved":["f2"]}`)
		outcome, err := runFindings(h)
		if outcome == candidateAccepted {
			t.Fatalf("%s: the responder's own account of evidence converged", name)
		}
		if err == nil || !strings.Contains(err.Error(), "[f2] evidence finding unattributed") {
			t.Fatalf("%s: the non-convergence does not name f2 as unanswered: %v", name, err)
		}
		if reviews.asked != 1 {
			t.Fatalf("%s: an unanswered evidence finding reached %d review turns", name, reviews.asked)
		}
	}
}

// W5 PARTIAL ANSWER IS NOT CONVERGENCE -- CONTROL. Two outstanding findings, the
// CODE one answered by a real change to the file it names, the other silent.
// The run does not converge and names the one still open, and only that one.
//
// Fails if: one discharge is read as the cycle converging, or the diagnosis
// names the wrong finding.
func TestFindingW5APartialAnswerNamesTheFindingStillOpen(t *testing.T) {
	h, reviews := findingsHarness(t,
		`FINDING-RESPONSES: {"responses":[{"id":"f1","response":"code","paths":["main.go"]}]}`,
		true, codeAndEvidenceReview, acceptingReview)
	outcome, err := runFindings(h)

	if outcome == candidateAccepted {
		t.Fatal("a cycle that answered one of two findings converged")
	}
	if err == nil || !strings.Contains(err.Error(), "1 of 2 outstanding finding(s) are still open") ||
		!strings.Contains(err.Error(), "[f2] evidence finding silent") {
		t.Fatalf("the diagnosis does not name the one finding still open: %v", err)
	}
	if strings.Contains(err.Error(), "[f1]") {
		t.Fatalf("the CODE finding answered by a change to main.go is reported open: %v", err)
	}
	if reviews.asked != 1 {
		t.Fatalf("the partly answered review reached %d review turns", reviews.asked)
	}
	// Answered is not discharged: no review resolved f1, so both are still
	// owed, and a handoff carries both.
	if left := h.engine.outstandingFindings("task-1"); len(left) != 2 {
		t.Fatalf("an answer no reviewer resolved was dropped from what is owed: %+v", left)
	}
}

// W6 DISAGREEMENT IS A ROUTE, NOT A LICENCE -- CONTROL. The implementer disputes
// f1's class and answers f2. The disagreement goes to the architect, f1 keeps
// its class and stays open, and nothing reaches the reviewer again. (The
// architect answers here; routing its plan then needs a certifiable Sensei
// preflight the stub does not serve, so the run ends on that -- after the
// architect turn this witness is about.)
//
// Fails if: a dispute discharges the finding, is dropped without escalation, or
// lets the candidate back into review.
func TestFindingW6ADisputedClassIsEscalatedAndNotDischarged(t *testing.T) {
	h, reviews := findingsHarness(t,
		`FINDING-RESPONSES: {"responses":[{"id":"f1","response":"disagree","claimed_class":"evidence","reason":"the plan said demonstrate"},{"id":"f2","response":"evidence","executions":["gofmt -l main.go"]}]}`,
		true, codeAndEvidenceReview, acceptingReview)
	reviews.architect = `{"decision":"proceed","summary":"f1 stands as a code finding","plan":"Return a typed routing refusal for an unbindable CREATE."}`
	h.engine.Config.Architect = config.Agent{Name: "chatgpt", Command: "chatgpt", Graph: "none"}
	outcome, err := runFindings(h)

	if outcome == candidateAccepted {
		t.Fatal("a disputed CODE finding was discharged by the dispute")
	}
	if err == nil || !strings.Contains(err.Error(), "[f1] code finding disputed") ||
		!strings.Contains(err.Error(), "escalating the class disagreement") {
		t.Fatalf("the disagreement was not routed as an escalation of f1: %v", err)
	}
	architect := false
	for _, r := range reviews.roles {
		architect = architect || r == roles.Architect
	}
	if !architect {
		t.Fatalf("the escalation never reached the architect's turn: %v (%v)", reviews.roles, err)
	}
	if reviews.asked != 1 {
		t.Fatalf("the disputed candidate reached %d review turns", reviews.asked)
	}
	left := h.engine.outstandingFindings("task-1")
	if len(left) != 2 || left[0].ID != "f1" || left[0].Class != roles.ClassCode {
		t.Fatalf("f1 did not stay open under its own class: %+v", left)
	}
}

// The typed open findings travel through the handoff with the reviewer's ids
// and classes; the responder's prose does not replace them.
//
// Fails if: the handoff carries only the prose notes, or renumbers or
// reclassifies the reviewer's findings.
func TestFindingTheHandoffCarriesTheTypedOpenFindings(t *testing.T) {
	h, _ := findingsHarness(t,
		`FINDING-RESPONSES: {"responses":[{"id":"f1","response":"evidence","evidence":"it is only evidence","claimed_class":"evidence"}]}`,
		true, codeAndEvidenceReview, acceptingReview)
	_, seen := runImplement(h)

	var handoff roles.WorkerHandoffPacket
	if err := json.Unmarshal(terminalPayload(t, seen, event.HandoffCreated), &handoff); err != nil {
		t.Fatalf("the handoff does not read back: %v", err)
	}
	classes := map[string]roles.Class{}
	for _, f := range handoff.OpenFindings {
		if _, dup := classes[f.ID]; dup {
			t.Fatalf("two handoff findings share id %q: %+v", f.ID, handoff.OpenFindings)
		}
		classes[f.ID] = f.Class
	}
	if classes["f1"] != roles.ClassCode || classes["f2"] != roles.ClassEvidence {
		t.Fatalf("the handoff did not carry the reviewer's findings under their own classes: %+v", handoff.OpenFindings)
	}
}

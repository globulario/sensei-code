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
//
// Its REVISE carries a classed finding. The harness's own REVISE payload has no
// class, and a verdict whose finding has no class is refused at validation --
// which would end these runs as unusable reviews, not as spent budgets.
func reviseForever(t *testing.T) *gateHarness {
	t.Helper()
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Fresh, "revise")
	h.engine.Runners = roleResolver{
		reviewer: answeringRunner{text: `{"decision":"revise","summary":"the proof is missing","findings":[{"id":"1","severity":"blocking","class":"evidence","claim":"the test does not fail without the fix","reference":"main.go","reason":"no mutation"}]}`, mode: roles.Fresh},
		name:     "remote:abc", session: "session-1",
	}
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
// below is the REVISE payload a real stubsmoke run recorded on 2026-09-19, with
// the class its finding states -- a missing proof, so evidence -- added now that
// the grammar requires one; FindInterrupted carries its instruction as the task's Review, and
// the owed re-plan puts it under OPEN FINDINGS. A re-plan without it would ask
// the architect to change a plan blind.
func TestTheOpenFindingsReachTheOwedReplan(t *testing.T) {
	const recorded = `{"provenance":{"task_id":"task-1789832817677608252","role":"reviewer","provider":"chatgpt","session_id":"session-20260919T154657.677324504Z","session_mode":"fresh","base_sha":"3b07f93df8a7ed7ed460e3e7f8c6d9aae8fe98af","candidate_digest":"d027ceb1144f0dbaaefc79601955f6924fa91003921b26ac7220cccea770bad7","candidate_tree":"974b16913e41fd98370caf6d06321e1c487166e6","graph_build_commit":"05feaf64d2694e97ac42b6bb93fbb49b9851a1f1","at":"2026-09-19T15:47:56.467303811Z"},"decision":"revise","summary":"the proof is missing","findings":[{"id":"1","severity":"blocking","class":"evidence","claim":"the change is not proven","reference":"internal/report/report.go","reason":"no witness"}]}`
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

// FINDING CLASS BINDS THE CLASS OF ITS RESPONSE.
//
// Measured on the DF-19 resume, 2026-09-25: one review raised a blocking CODE
// finding and a major EVIDENCE finding; the implementer answered the evidence
// one and declared the cycle closed. The engine refused only because the diff
// had not moved -- a proxy. These witnesses drive the REAL candidate loop with a
// scripted implementer, reviewer and architect: each is a shell process that
// replays, turn by turn, what the test wrote for it, and records the prompt it
// was given and how many turns it was asked for. The turn counts are how each
// witness shows WHICH guard refused: a guard that refuses a cycle before review
// leaves the reviewer at one turn, and the reviewer's scripted second answer --
// always one that would let the candidate through -- is never read.

// replayScript is the scripted participant. Turn n copies <base>.n.go over main.go
// when that file exists, records its prompt at <base>.n.prompt, and prints
// <base>.n.out; a turn with no .out file exits non-zero.
const replayScript = `n=$(( $(cat "$0.n" 2>/dev/null || echo 0) + 1 )); echo "$n" > "$0.n"; cat > "$0.$n.prompt"; if [ -f "$0.$n.go" ]; then cp "$0.$n.go" main.go; fi; [ -f "$0.$n.out" ] && cat "$0.$n.out"`

type scriptedLoop struct {
	t   *testing.T
	h   *gateHarness
	dir string
}

func newScriptedLoop(t *testing.T, cycles int) *scriptedLoop {
	t.Helper()
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Fresh, "accept")
	dir := t.TempDir()
	participant := func(name, provider string) config.Agent {
		return config.Agent{Name: provider, Command: "sh", Args: []string{"-c", replayScript, dir + "/" + name}, Graph: "none"}
	}
	// The names matter. The reviewer role is granted by provider name, so the
	// reviewer is codex; with no graph bound, claude and codex run exactly the
	// argv configured here. A participant named chatgpt does NOT: the CLI
	// adapter sends that name to the ChatGPT transport whatever its argv says,
	// so the architect has a name no provider answers to, and a witness can
	// never reach an external service in its script's place.
	h.worker = participant("impl", "claude")
	h.engine.Config.Implementors = []config.Agent{h.worker}
	h.engine.Config.Reviewer = participant("review", "codex")
	h.engine.Config.Architect = participant("arch", "scripted-architect")
	// Every role is its configured CLI process: nothing in-process can answer
	// in the scripted participants' place.
	h.engine.Runners = nil
	h.engine.Config.Workflow.ReviewCycles = cycles
	return &scriptedLoop{t: t, h: h, dir: dir}
}

func turnName(who string, n int) string { return who + "." + string(rune('0'+n)) }

func (s *scriptedLoop) write(name, content string) {
	s.t.Helper()
	if err := os.WriteFile(s.dir+"/"+name, []byte(content), 0o644); err != nil {
		s.t.Fatal(err)
	}
}

// implement scripts implementer turn n: the main.go it leaves ("" leaves the
// candidate as it stands) and the text it reports.
func (s *scriptedLoop) implement(n int, mainGo, out string) {
	if mainGo != "" {
		s.write(turnName("impl", n)+".go", mainGo)
	}
	s.write(turnName("impl", n)+".out", out)
}

func (s *scriptedLoop) review(n int, verdict string) { s.write(turnName("review", n)+".out", verdict) }

// turns is how many turns a participant was asked for.
func (s *scriptedLoop) turns(who string) string {
	b, err := os.ReadFile(s.dir + "/" + who + ".n")
	if err != nil {
		return "0"
	}
	return strings.TrimSpace(string(b))
}

func (s *scriptedLoop) prompt(who string, n int) string {
	b, _ := os.ReadFile(s.dir + "/" + turnName(who, n) + ".prompt")
	return string(b)
}

func (s *scriptedLoop) run() (candidateOutcome, error, []event.Event) {
	outcome, _, _, _, err := s.h.engine.runCandidate(context.Background(), s.h.sc, certifiedStart{},
		"task-1", s.h.tc, "Rewrite main.go so it prints a number.", s.h.worker, s.h.work, "")
	return outcome, err, drainEvents(s.h.events)
}

const (
	mainV1        = "package main\n\nfunc main() {\n\tprintln(1)\n}\n"
	mainV1Comment = "package main\n\n// main prints a number.\nfunc main() {\n\tprintln(1)\n}\n"
	mainV2        = "package main\n\nfunc main() {\n\tprintln(2)\n}\n"

	codeFinding     = `{"id":"f1","severity":"blocking","class":"code","claim":"a CREATE that cannot be bound does not refuse the plan","reference":"main.go","reason":"the refusal is emitted as status and the run continues","correction":"return a typed refusal"}`
	evidenceFinding = `{"id":"f2","severity":"major","class":"evidence","claim":"the failing-first history is not established","reference":"the witness output","reason":"no pre-repair run was supplied","proof_gap":"the witnesses run against the pre-repair implementation"}`
)

func reviseWith(findings ...string) string {
	return `{"decision":"revise","summary":"the candidate is not yet right","findings":[` + strings.Join(findings, ",") + `]}`
}

func responses(items ...string) string {
	return "FINDING RESPONSES:\n[" + strings.Join(items, ",") + "]\n"
}

// acceptResolving is the reviewer's second answer in the refusal witnesses: an
// ACCEPT that resolves the named ids. Were a refusal guard broken, the cycle
// would reach this review and the candidate would converge on it.
func acceptResolving(ids ...string) string {
	var rs []string
	for _, id := range ids {
		rs = append(rs, `{"finding_id":"`+id+`","outcome":"resolved","basis":"scripted"}`)
	}
	return `{"decision":"accept","summary":"the candidate stands","resolutions":[` + strings.Join(rs, ",") + `]}`
}

// W1 THE MEASURED CASE. One CODE and one EVIDENCE finding; the next cycle answers
// the evidence one alone and leaves the candidate unchanged. It does not
// converge, and the diagnosis names the unanswered CODE finding by id.
//
// Breaks if: the per-finding accounting in runCandidate stops refusing an
// outstanding id that has no response. The identical-diff backstop cannot pass
// this witness in its place: its message names no finding, and this asserts the
// id and that the backstop was not what refused.
func TestW1AnEvidenceAnswerLeavesTheCodeFindingOpenByID(t *testing.T) {
	s := newScriptedLoop(t, 2)
	s.implement(1, mainV1, "wrote main.go")
	s.review(1, reviseWith(codeFinding, evidenceFinding))
	s.implement(2, "", "Ran the witnesses against the pre-repair tree; the failing output is retained.\n"+
		responses(`{"finding_id":"f2","class":"evidence","disposition":"discharged","account":"ran the witnesses"}`))
	s.review(2, acceptResolving("f1", "f2"))

	outcome, err, _ := s.run()
	if outcome.Accepted() {
		t.Fatal("a cycle that answered only the evidence finding converged")
	}
	if err == nil || !strings.Contains(err.Error(), "finding f1 (code, blocking) was not answered") {
		t.Fatalf("the diagnosis does not name the unanswered CODE finding by id: %v", err)
	}
	if strings.Contains(err.Error(), "did not change between review cycles") {
		t.Fatalf("the identical-diff proxy decided, not the per-finding accounting: %v", err)
	}
	if strings.Contains(err.Error(), "finding f2 (evidence, major) was not answered") {
		t.Fatalf("the answered evidence finding was reported unanswered: %v", err)
	}
	if got := s.turns("review"); got != "1" {
		t.Fatalf("the reviewer was asked %s time(s); a cycle with an unanswered finding must not reach review", got)
	}
}

// W2 RECLASSIFICATION IS REFUSED. A response that answers a CODE finding as
// evidence-only discharges nothing, even though the candidate changed: the
// class compared is the one on the finding's record.
//
// Breaks if: the accounting compares against the response's class, or skips
// the class comparison. The candidate moves in this cycle, so the code-needs-a-
// change check cannot refuse in its place, and the reviewer's scripted second
// answer would accept and resolve f1 if the cycle reached it.
func TestW2AResponseCannotReclassifyTheFindingItAnswers(t *testing.T) {
	s := newScriptedLoop(t, 2)
	s.implement(1, mainV1, "wrote main.go")
	s.review(1, reviseWith(codeFinding))
	s.implement(2, mainV2, "This was only ever a proof gap.\n"+
		responses(`{"finding_id":"f1","class":"evidence","disposition":"discharged","account":"a proof gap, not a defect"}`))
	s.review(2, acceptResolving("f1"))

	outcome, err, _ := s.run()
	if outcome.Accepted() {
		t.Fatal("a CODE finding answered as evidence discharged it")
	}
	if err == nil || !strings.Contains(err.Error(), `finding f1 (code, blocking) is a code finding on the review record, and a response answering it as "evidence" does not discharge it`) {
		t.Fatalf("the refusal does not take the class from the finding record: %v", err)
	}
	if got := s.turns("review"); got != "1" {
		t.Fatalf("the reviewer was asked %s time(s); a reclassifying response must not reach review", got)
	}
}

// W3 THE PROXY IS NOT THE CHECK -- CRITICAL CONTROL. The diff differs because an
// unrelated line changed, and the CODE finding is left unaddressed. It does not
// converge. Demonstrated to FAIL against the pre-repair loop, where the moved
// diff passed the identical-diff check and the second review's ACCEPT
// converged the candidate.
//
// Breaks if: convergence is decided by the diff moving -- either the
// per-finding accounting stops refusing the unanswered id before review
// ("unaddressed"), or, when the implementer does claim a discharge, the
// ACCEPT is taken without resolving the outstanding id ("claimed").
func TestW3AnUnrelatedChangeDoesNotCloseACodeFinding(t *testing.T) {
	t.Run("unaddressed", func(t *testing.T) {
		s := newScriptedLoop(t, 2)
		s.implement(1, mainV1, "wrote main.go")
		s.review(1, reviseWith(codeFinding))
		s.implement(2, mainV1Comment, "added a comment")
		s.review(2, `{"decision":"accept","summary":"the candidate stands"}`)

		outcome, err, _ := s.run()
		if outcome.Accepted() {
			t.Fatal("an unrelated change converged a candidate with an unaddressed CODE finding")
		}
		if err == nil || !strings.Contains(err.Error(), "f1") {
			t.Fatalf("the diagnosis does not name the open CODE finding: %v", err)
		}
	})
	t.Run("claimed", func(t *testing.T) {
		s := newScriptedLoop(t, 2)
		s.implement(1, mainV1, "wrote main.go")
		s.review(1, reviseWith(codeFinding))
		s.implement(2, mainV1Comment, "fixed it\n"+
			responses(`{"finding_id":"f1","class":"code","disposition":"discharged","account":"fixed"}`))
		s.review(2, `{"decision":"accept","summary":"the candidate stands"}`)

		outcome, _, seen := s.run()
		if outcome.Accepted() {
			t.Fatal("an ACCEPT that resolved nothing converged a candidate with an outstanding CODE finding")
		}
		named := false
		for _, ev := range seen {
			if ev.Kind == event.ReviewContradiction && strings.Contains(ev.Summary, "f1 (code, blocking)") {
				named = true
			}
		}
		if !named {
			t.Fatalf("the unresolved CODE finding did not reach the architect by id: %v", kinds(seen))
		}
	})
}

// W4 EVIDENCE DISCHARGED BY EVIDENCE. An EVIDENCE finding answered with evidence
// and no change to the candidate reaches review, and the review's resolution of
// its id converges the run. The reviewer is given the finding by id and class,
// and never the implementer's account of it.
//
// Breaks if: the identical-diff backstop decides ahead of the accounting, if a
// compatible resolution fails to close the id, or if the implementer's account
// reaches the reviewer's packet.
func TestW4AnEvidenceFindingIsDischargedByEvidenceAlone(t *testing.T) {
	s := newScriptedLoop(t, 2)
	s.implement(1, mainV1, "wrote main.go")
	s.review(1, reviseWith(evidenceFinding))
	s.implement(2, "", "Ran the witnesses.\n"+
		responses(`{"finding_id":"f2","class":"evidence","disposition":"discharged","account":"IMPLEMENTER-ACCOUNT: the failing output is retained"}`))
	s.review(2, acceptResolving("f2"))

	outcome, err, _ := s.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("an evidence finding answered with evidence did not converge: outcome=%q err=%v", outcome, err)
	}
	if got := s.turns("review"); got != "2" {
		t.Fatalf("the reviewer was asked %s time(s), want 2", got)
	}
	second := s.prompt("review", 2)
	if !strings.Contains(second, "[f2] major, class evidence:") || !strings.Contains(second, `"resolutions"`) {
		t.Fatalf("the second review was not asked to resolve the outstanding finding by id:\n%s", second)
	}
	if strings.Contains(second, "IMPLEMENTER-ACCOUNT") {
		t.Fatal("the implementer's account of the finding reached the independent reviewer")
	}
}

// W5 PARTIAL ANSWER IS NOT CONVERGENCE -- CONTROL. Two outstanding findings, one
// answered: the run does not converge and names the one still open.
//
// Breaks if: the accounting stops requiring EVERY outstanding id -- counting
// "some response arrived" as an answer. The candidate moves, so neither the
// backstop nor the code-needs-a-change check refuses in its place, and the
// reviewer's scripted second answer would accept and resolve both.
func TestW5APartialAnswerNamesTheFindingStillOpen(t *testing.T) {
	second := strings.Replace(strings.Replace(codeFinding, `"f1"`, `"f2"`, 1), "a CREATE that cannot be bound", "resume discards the refusal", 1)
	s := newScriptedLoop(t, 2)
	s.implement(1, mainV1, "wrote main.go")
	s.review(1, reviseWith(codeFinding, second))
	s.implement(2, mainV2, "fixed the resume path\n"+
		responses(`{"finding_id":"f2","class":"code","disposition":"discharged","account":"resume reapplies the refusal"}`))
	s.review(2, acceptResolving("f1", "f2"))

	outcome, err, _ := s.run()
	if outcome.Accepted() {
		t.Fatal("a partial answer converged")
	}
	if err == nil || !strings.Contains(err.Error(), "finding f1 (code, blocking) was not answered") {
		t.Fatalf("the finding still open is not named: %v", err)
	}
	if strings.Contains(err.Error(), "finding f2 (code, blocking) was not answered") {
		t.Fatalf("the answered finding was named as unanswered: %v", err)
	}
	if got := s.turns("review"); got != "1" {
		t.Fatalf("the reviewer was asked %s time(s); a partial answer must not reach review", got)
	}
}

// W6 DISAGREEMENT IS A ROUTE, NOT A LICENCE -- CONTROL. An implementer that
// believes a CODE finding is misclassified says so; the disagreement reaches the
// architect with the finding's recorded class, and the finding stays open.
//
// Breaks if: a disagreement is read as a discharge (the reviewer would then be
// asked a second time, and its scripted answer resolves f1), if it is dropped
// rather than routed (the architect would never be asked), or if routing it
// removes the finding from the ledger.
func TestW6ADisagreementIsRoutedAndDischargesNothing(t *testing.T) {
	s := newScriptedLoop(t, 2)
	s.implement(1, mainV1, "wrote main.go")
	s.review(1, reviseWith(codeFinding))
	s.implement(2, mainV2, "I believe f1 is an evidence finding.\n"+
		responses(`{"finding_id":"f1","class":"evidence","disposition":"disagreed","account":"DISAGREEMENT: this is a proof gap, not a code defect"}`))
	s.review(2, acceptResolving("f1"))
	// The architect records the question and gives no answer, so the run ends
	// at the route and the ledger can be read as the route left it.

	outcome, err, _ := s.run()
	if outcome.Accepted() || err == nil {
		t.Fatalf("a disagreement converged the candidate: outcome=%q err=%v", outcome, err)
	}
	if got := s.turns("review"); got != "1" {
		t.Fatalf("the reviewer was asked %s time(s); a disagreement is not a discharge", got)
	}
	if got := s.turns("arch"); got == "0" {
		t.Fatalf("the disagreement was never routed to the architect: %v", err)
	}
	asked := s.prompt("arch", 1)
	if !strings.Contains(asked, "[f1] blocking, class code:") || !strings.Contains(asked, "DISAGREEMENT: this is a proof gap") {
		t.Fatalf("the architect was not given the finding's recorded class and the disagreement:\n%s", asked)
	}
	open, ok := s.h.engine.openReview("task-1")
	if !ok || len(open.Outstanding) != 1 || open.Outstanding[0].Finding.ID != "f1" || open.Outstanding[0].Finding.Class != roles.ClassCode {
		t.Fatalf("routing the disagreement changed the ledger: %+v", open.Outstanding)
	}
}

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

// OBJECTIVE IDENTITY ACROSS THE RESUME BOUNDARY. Measured 2026-09-25 on the
// resume of task-1790353318851268310: a planned task that ended NOT_CONVERGED
// was resumed at its owed architect re-plan and refused one second later with
// "no adapter took the architect role: no exact objective/world binding" --
// ObjectiveDigest empty, every other referent present. The planned resume never
// restored the objective the durable record holds, so the one turn the engine
// said was owed could not be taken.
//
// These drive the REAL Engine.Resume over a durable candidate record. Sensei is
// unstartable, so the invocation ends at its first step; what is measured is
// the objective the process holds after crossing the resume boundary, and the
// binding the one architect edge (resolveRunner) mints from it once the start
// gate's world is in place. Sensei certification cannot be driven here, so the
// graph commit the gate would record is placed where bindGraph puts it.

// measuredGraphCommit is the graph build commit the measured refusal carried.
const measuredGraphCommit = "05feaf64d2694e97ac42b6bb93fbb49b9851a1f1"

// emptyInputDigest is sha256(""). It must never appear as an objective
// identity: an absent objective is not the objective "".
const emptyInputDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// unboundRefusal is the refusal the GitHub architecture bridge returns for an
// incomplete binding (ghbridge.ErrUnboundArchitecture's text).
type unboundRefusal struct{}

func (unboundRefusal) Error() string { return "no exact objective/world binding for architect turn" }

// capturingResolver records the spec the architect edge hands a resolver, and
// refuses an incomplete binding exactly as the bridge does.
type capturingResolver struct{ saw *RunnerSpec }

func (r capturingResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	*r.saw = spec
	if spec.Role == roles.Architect && !spec.Architecture.Valid() {
		return Resolved{}, unboundRefusal{}
	}
	return CLIResolved(spec, "session-r"), nil
}

// notConvergedResume is the measured record: a planned task owed an architect
// re-plan after spending its review cycles.
func notConvergedResume(t *testing.T) (*Engine, <-chan event.Event, session.Interrupted) {
	t.Helper()
	e, events, task := resumeHarness(t, true)
	e.Config.Sensei.Repository = "globulario/sensei"
	nc, _ := json.Marshal(NotConverged{TaskID: task.TaskID, Implementers: []string{"claude"}, ReviewCycles: 3, Owed: OwedArchitectReplan})
	task.NotConverged = nc
	if _, owed, err := owedReplan(task.NotConverged, task.BlockedExternal, task.TaskID); err != nil || !owed {
		t.Fatalf("the fixture does not owe an architect re-plan, so it would witness nothing: owed=%v err=%v", owed, err)
	}
	return e, events, task
}

// architectTurnAfter drives Resume to settlement, puts the start gate's graph
// commit where bindGraph records it, and asks the one architect edge for the
// turn. It returns the binding the resolver saw and the edge's answer.
func architectTurnAfter(t *testing.T, e *Engine, events <-chan event.Event, task session.Interrupted) (roles.ArchitectureBinding, error) {
	t.Helper()
	e.Resume(context.Background(), task)
	settleResume(t, events)
	bindStartGraph(e, task.TaskID)
	return bindArchitectTurn(e, task.TaskID)
}

func bindStartGraph(e *Engine, taskID string) {
	e.bindGraph(taskID, certifiedStart{})
	e.graphFor(taskID).Digest = measuredGraphCommit
}

func bindArchitectTurn(e *Engine, taskID string) (roles.ArchitectureBinding, error) {
	var saw RunnerSpec
	e.Runners = capturingResolver{saw: &saw}
	_, err := e.resolveRunner(RunnerSpec{Role: roles.Architect, TaskID: taskID, Agent: e.Config.Architect})
	return saw.Architecture, err
}

// W1. THE MEASURED CASE. The resumed planned task binds its owed architect
// re-plan to the objective its durable record holds.
func TestW1APlannedTaskResumedAtItsOwedReplanBindsTheRecordedObjective(t *testing.T) {
	e, events, task := notConvergedResume(t)
	got, err := architectTurnAfter(t, e, events, task)
	if err != nil {
		t.Fatalf("the owed architect re-plan could not be taken: %v", err)
	}
	if !got.Valid() {
		t.Fatalf("the resumed re-plan's architecture binding is not valid: %+v", got)
	}
	want := roles.BindArchitecture(task.TaskID, task.Task, "", "", "").ObjectiveDigest
	if want == "" || got.ObjectiveDigest != want {
		t.Fatalf("objective identity %q does not name the recorded objective %q (%s)", got.ObjectiveDigest, task.Task, want)
	}
}

// W2. THE TWO MECHANISMS AGREE. A fresh run of the task and a resume of the
// same record, in a restarted process, mint the same binding on every referent.
func TestW2AFreshRunAndAResumeMintTheSameArchitectureBinding(t *testing.T) {
	resumed, events, task := notConvergedResume(t)

	fresh := New(resumed.Repo, config.Default(), event.NewBus(), nil, "session-fresh")
	fresh.Config.Sensei.Command = "/nonexistent/awareness-mcp"
	fresh.Config.Sensei.Repository = resumed.Config.Sensei.Repository
	fresh.run(context.Background(), task.TaskID, task.Task, RequestedByHuman)
	bindStartGraph(fresh, task.TaskID)
	a, err := bindArchitectTurn(fresh, task.TaskID)
	if err != nil {
		t.Fatalf("the fresh run's architect turn could not be bound: %v", err)
	}

	b, err := architectTurnAfter(t, resumed, events, task)
	if err != nil {
		t.Fatalf("the resumed architect turn could not be bound: %v", err)
	}
	for _, f := range []struct{ name, fresh, resumed string }{
		{"task id", a.TaskID, b.TaskID},
		{"objective digest", a.ObjectiveDigest, b.ObjectiveDigest},
		{"base sha", a.BaseSHA, b.BaseSHA},
		{"graph repository", a.GraphRepository, b.GraphRepository},
		{"graph build commit", a.GraphBuildCommit, b.GraphBuildCommit},
	} {
		if f.fresh == "" || f.fresh != f.resumed {
			t.Errorf("%s: fresh %q, resumed %q", f.name, f.fresh, f.resumed)
		}
	}
}

// W3. CONTROL: AN ABSENT OBJECTIVE STILL REFUSES. A record carrying no
// objective text yields no identity -- not a digest, and not the digest of an
// empty input -- and the architect turn is refused.
func TestW3AResumedRecordWithNoObjectiveProducesNoIdentity(t *testing.T) {
	e, events, task := notConvergedResume(t)
	task.Task = ""
	got, err := architectTurnAfter(t, e, events, task)
	if got.ObjectiveDigest != "" {
		t.Fatalf("an absent objective produced an identity %q", got.ObjectiveDigest)
	}
	if got.ObjectiveDigest == emptyInputDigest {
		t.Fatal("an absent objective was turned into the digest of an empty input")
	}
	if got.Valid() {
		t.Fatalf("a binding with no objective reads as valid: %+v", got)
	}
	if err == nil {
		t.Fatal("the architect turn was taken with no objective identity")
	}
}

// W4. THE DIAGNOSIS NAMES THE MISSING REFERENT. An incomplete binding is
// reported as the referent it lacks, never as an absent adapter.
func TestW4AnIncompleteBindingNamesItsMissingReferentNotAnAdapter(t *testing.T) {
	e, events, task := notConvergedResume(t)
	task.Task = ""
	_, err := architectTurnAfter(t, e, events, task)
	if err == nil {
		t.Fatal("an incomplete binding was not refused")
	}
	msg := err.Error()
	if strings.Contains(msg, "no adapter took") || strings.Contains(msg, "no resolver for") {
		t.Fatalf("an incomplete binding was reported as an absent adapter: %s", msg)
	}
	if !strings.Contains(msg, "objective identity") {
		t.Fatalf("the refusal does not name the missing objective identity: %s", msg)
	}
	for _, present := range []string{"task id", "candidate base", "graph repository", "graph build commit"} {
		if strings.Contains(msg, "missing "+present) || strings.Contains(msg, ", "+present) {
			t.Errorf("the refusal names %s as missing, but it is present: %s", present, msg)
		}
	}

	// A different missing referent is named as itself.
	e.Config.Sensei.Repository = ""
	e.recordObjective(task.TaskID, Objective{Text: "the objective", Provenance: ResumedGoverned})
	_, err = bindArchitectTurn(e, task.TaskID)
	if err == nil || !strings.Contains(err.Error(), "graph repository") || strings.Contains(err.Error(), "objective identity") ||
		strings.Contains(err.Error(), "no adapter took") {
		t.Fatalf("an unconfigured graph repository was not named as the missing referent: %v", err)
	}
}

// W5. CONTROL: PROVENANCE IS NOT OVERWRITTEN. A process that still holds the
// submission's objective keeps it, provenance included, across a resume.
func TestW5AResumeDoesNotReplaceAHeldObjectiveOrItsProvenance(t *testing.T) {
	e, events, task := notConvergedResume(t)
	held := Objective{Text: task.Task, Provenance: RequestedByHuman}
	e.recordObjective(task.TaskID, held)
	if _, err := architectTurnAfter(t, e, events, task); err != nil {
		t.Fatalf("the resumed architect turn could not be bound: %v", err)
	}
	if got := e.objective(task.TaskID); got != held {
		t.Fatalf("the resume replaced the held objective %+v with %+v", held, got)
	}
	// And a restarted process, which holds nothing, claims only the
	// resumption's own provenance: it does not promote itself to a human.
	restarted, events2, task2 := notConvergedResume(t)
	restarted.Resume(context.Background(), task2)
	settleResume(t, events2)
	if got := restarted.objective(task2.TaskID); got.Text != task2.Task || got.Provenance != ResumedGoverned || got.HumanAuthorized() {
		t.Fatalf("a restarted resume claimed provenance it cannot establish: %+v", got)
	}
}

// W6. CONTROL: THE PATH THAT ALREADY WORKED. An unplanned task's resume binds
// its architect turn to the recorded objective, as before.
func TestW6AnUnplannedResumeStillBindsItsArchitectTurn(t *testing.T) {
	e, events, task := resumeHarness(t, false)
	e.Config.Sensei.Repository = "globulario/sensei"
	task.Planned = false
	got, err := architectTurnAfter(t, e, events, task)
	if err != nil || !got.Valid() {
		t.Fatalf("the unplanned resume's architect turn is no longer bound: %+v %v", got, err)
	}
	if want := roles.BindArchitecture(task.TaskID, task.Task, "", "", "").ObjectiveDigest; got.ObjectiveDigest != want {
		t.Fatalf("objective identity %q, want %q", got.ObjectiveDigest, want)
	}
	if o := e.objective(task.TaskID); o.Provenance != ResumedGoverned {
		t.Fatalf("the unplanned resume's provenance changed: %+v", o)
	}
}

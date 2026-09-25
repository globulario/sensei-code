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

// OBJECTIVE IDENTITY ACROSS THE RESUME BOUNDARY.
//
// Measured 2026-09-25 on the resume of task-1790353318851268310: a task that
// ended NOT_CONVERGED owing architect_replan was resumed as instructed, and its
// owed architect turn refused with ObjectiveDigest EMPTY beside a correct task
// id, base, graph repository and graph build commit. Every other referent
// survived the process boundary; the objective did not, because only the
// unplanned resume restored it from the durable record.
//
// The witnesses below drive the REAL Resume and run paths as far as the
// architect turn: a stub Sensei certifies the start exactly as the start gate
// requires, and a recording resolver captures the binding resolveRunner minted
// for the architect when the path reached it.

const (
	witnessGraphRepository = "globulario/sensei"
	witnessGraphCommit     = "05feaf64d2694e97ac42b6bb93fbb49b9851a1f1"
	// witnessBaseSHA is a canonical base for fixtures with no repository
	// history of their own.
	witnessBaseSHA = "0121e93abb8dc8093697994a657c2735f1e824b2"
	// certifyingSenseiMarker selects TestCertifyingSenseiStubProcess when this
	// test binary is re-entered as the Sensei MCP.
	certifyingSenseiMarker = "nonconvergence-certifying-sensei"
)

// certifyWitnessGraph installs the graph identity a certified start carries,
// through the one constructor that installs it, and the configured repository
// that owns it.
func certifyWitnessGraph(e *Engine, taskID string) {
	var start certifiedStart
	start.preflight.Authority.GraphBuildCommit = witnessGraphCommit
	e.bindGraph(taskID, start)
	e.Config.Sensei.Repository = witnessGraphRepository
}

// bindArchitectTurn installs every referent an architect turn is bound by,
// each through the record that owns it: the candidate identity names the
// base, the objective is recorded as a submission records it, and the graph
// identity arrives through bindGraph as a certified start's does. A fixture
// that tests what happens AFTER an architect turn is bound uses this, so the
// binding guard in resolveRunner is satisfied rather than bypassed.
func bindArchitectTurn(t *testing.T, e *Engine, taskID, base string) {
	t.Helper()
	if e.Repo.Root == "" {
		t.Fatal("bindArchitectTurn needs a repository root to record the candidate identity under")
	}
	id := candidateIdentityFor(base)
	id.TaskID = taskID
	if err := id.Save(e.Repo.Root); err != nil {
		t.Fatal(err)
	}
	e.recordObjectiveIfAbsent(taskID, Objective{Text: "the objective", Provenance: SubmittedUnattended})
	certifyWitnessGraph(e, taskID)
	if b := e.architectureBinding(taskID); !b.Valid() {
		t.Fatalf("the fixture did not bind the architect turn completely: %+v", b)
	}
}

// serveCertifiedStart points the engine at a Sensei that certifies the start
// gate, and gives the repository the remote the graph's domain is compared
// with. Everything the architect binding needs then arrives the way it does in
// production: the base from the candidate record, the graph commit from the
// certified preflight, the repository from configuration.
func serveCertifiedStart(t *testing.T, e *Engine) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate this test binary to re-enter it as the Sensei stub: %v", err)
	}
	e.Config.Sensei.Command = exe
	e.Config.Sensei.Args = []string{"-test.run=^TestCertifyingSenseiStubProcess$", certifyingSenseiMarker}
	e.Config.Sensei.Repository = witnessGraphRepository
	gitConfig := e.Repo.Root + "/.git/config"
	body, err := os.ReadFile(gitConfig)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), `[remote "origin"]`) {
		return
	}
	if err := os.WriteFile(gitConfig, append(body, []byte("[remote \"origin\"]\n\turl = https://github.com/globulario/sensei-code.git\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCertifyingSenseiStubProcess is the certifying Sensei. It is a subprocess
// entry point rather than a test: without the marker it skips.
func TestCertifyingSenseiStubProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != certifyingSenseiMarker {
		t.Skip("not a stub re-entry: this test is the subprocess entry point for the objective-identity witnesses")
	}
	for {
		body, ok := readCertifyingFrame()
		if !ok {
			os.Exit(0)
		}
		var req struct {
			ID     *int64 `json:"id"`
			Method string `json:"method"`
			Params struct {
				Name      string         `json:"name"`
				Arguments map[string]any `json:"arguments"`
			} `json:"params"`
		}
		if json.Unmarshal(body, &req) != nil || req.ID == nil {
			continue
		}
		var result any = map[string]any{}
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]any{"name": certifyingSenseiMarker, "version": "0"},
			}
		case "tools/call":
			result = certifyingTool(req.Params.Name, req.Params.Arguments)
		}
		if writeStubFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result}) != nil {
			os.Exit(0)
		}
	}
}

// certifyingTool answers the start gate with the certifying fixtures the gate
// tests use, the preflight carrying the witness graph commit, and everything
// else as the governed-loop stub does.
func certifyingTool(name string, args map[string]any) map[string]any {
	var structured map[string]any
	switch name {
	case "sensei_workspace_status":
		_ = json.Unmarshal([]byte(okWorkspace), &structured)
	case "awareness_preflight":
		_ = json.Unmarshal([]byte(okPreflight), &structured)
		structured["authority"].(map[string]any)["graph_build_commit"] = witnessGraphCommit
	default:
		return stubTool(name, args, "")
	}
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": "certified by the stub"}},
		"structuredContent": structured,
	}
}

// readCertifyingFrame reads one Content-Length frame from standard input.
func readCertifyingFrame() ([]byte, bool) {
	var header []byte
	one := make([]byte, 1)
	for !strings.HasSuffix(string(header), "\r\n\r\n") && !strings.HasSuffix(string(header), "\n\n") {
		if n, err := os.Stdin.Read(one); n == 0 || err != nil {
			return nil, false
		}
		header = append(header, one[0])
	}
	length := -1
	for _, line := range strings.Split(string(header), "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(strings.TrimSpace(k), "Content-Length") {
			if json.Unmarshal([]byte(strings.TrimSpace(v)), &length) != nil {
				return nil, false
			}
		}
	}
	if length < 0 {
		return nil, false
	}
	body := make([]byte, length)
	for read := 0; read < length; {
		n, err := os.Stdin.Read(body[read:])
		if n == 0 && err != nil {
			return nil, false
		}
		read += n
	}
	return body, true
}

// recordingArchitectResolver records every turn it is asked to serve and
// serves none. It never refuses on its own account, so a witness that reads
// its record learns what reached resolver selection, not what the double
// happened to reject.
type recordingArchitectResolver struct {
	asked []RunnerSpec
}

func (r *recordingArchitectResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	r.asked = append(r.asked, spec)
	return Resolved{}, nil
}

// architectTurns is every architect turn that reached resolver selection.
func (r *recordingArchitectResolver) architectTurns() []roles.ArchitectureBinding {
	var out []roles.ArchitectureBinding
	for _, spec := range r.asked {
		if spec.Role == roles.Architect {
			out = append(out, spec.Architecture)
		}
	}
	return out
}

// narrated renders an event stream for a failure message.
func narrated(seen []event.Event) string {
	return strings.Join(summaries(seen), "\n")
}

// owedReplanAnnounced proves the resume took the owed-replan branch, so a
// witness about that branch cannot pass on a path that never entered it.
func owedReplanAnnounced(t *testing.T, seen []event.Event) {
	t.Helper()
	for _, ev := range seen {
		if strings.HasPrefix(ev.Summary, "resuming the same task at the architect re-plan it is owed") {
			return
		}
	}
	t.Fatalf("the resume never reached its owed architect re-plan, so this witness exercised nothing:\n%s", narrated(seen))
}

// notConvergedResume is the measured shape: planned by the architect,
// NOT_CONVERGED, owing the architect re-plan, resumed by a process that holds
// nothing in memory.
func notConvergedResume(t *testing.T) (*Engine, <-chan event.Event, session.Interrupted) {
	t.Helper()
	e, events, task := resumeHarness(t, true)
	nc, err := json.Marshal(NotConverged{TaskID: task.TaskID, Implementers: []string{"claude"}, ReviewCycles: 3, Owed: OwedArchitectReplan})
	if err != nil {
		t.Fatal(err)
	}
	task.NotConverged = nc
	if _, owed, err := owedReplan(task.NotConverged, task.BlockedExternal, task.TaskID); err != nil || !owed {
		t.Fatalf("the fixture does not owe an architect re-plan, so this witness would exercise nothing: owed=%v err=%v", owed, err)
	}
	plan, err := json.Marshal(proposedPlan{architectureDecision: architectureDecision{Plan: "the plan that did not converge"}, PlanSource: PlanByArchitect})
	if err != nil {
		t.Fatal(err)
	}
	task.PlanSource, task.PlanRecord = string(PlanByArchitect), plan
	return e, events, task
}

// resumeToOwedReplan resumes the task over a certifying Sensei and returns the
// architect turns that reached resolver selection, and the events it produced.
func resumeToOwedReplan(t *testing.T, e *Engine, events <-chan event.Event, task session.Interrupted) ([]roles.ArchitectureBinding, []event.Event) {
	t.Helper()
	serveCertifiedStart(t, e)
	resolver := &recordingArchitectResolver{}
	e.Runners = resolver
	e.Resume(context.Background(), task)
	seen := settleResume(t, events)
	owedReplanAnnounced(t, seen)
	return resolver.architectTurns(), seen
}

// W1 THE MEASURED CASE. A planned task resumed at its owed architect re-plan
// reaches the architect with a VALID binding whose objective identity is the
// identity of the objective text its durable record holds.
//
// Fails when: the planned resume does not restore the recorded objective, so
// the architect turn is minted with an empty ObjectiveDigest (the measured
// defect, and the failure recorded against the pinned base), the digest names
// anything other than the record's task text, or the owed-replan branch
// stops reaching the architect at all.
func TestAResumedReplanBindsTheRecordedObjective(t *testing.T) {
	e, events, task := notConvergedResume(t)
	turns, seen := resumeToOwedReplan(t, e, events, task)
	if len(turns) == 0 {
		t.Fatalf("the owed re-plan never reached the architect with a binding:\n%s", narrated(seen))
	}
	got := turns[0]
	want := roles.BindArchitecture(task.TaskID, task.Task, e.governedBase(task.TaskID), witnessGraphRepository, witnessGraphCommit)
	if got.ObjectiveDigest != want.ObjectiveDigest {
		t.Fatalf("the resumed architect turn names objective %q; the durable record's objective is %q: %+v", got.ObjectiveDigest, want.ObjectiveDigest, got)
	}
	if !got.Valid() {
		t.Fatalf("the resumed architect turn carries an incomplete binding: %+v", got)
	}
}

// mintArchitectBinding supplies the certified graph identity and mints the
// architect turn through the real resolveRunner, returning what reached
// resolver selection (if anything) and the error. The double serves nothing,
// so a turn that reached it still errors; only asked says how far it got.
func mintArchitectBinding(e *Engine, taskID string) ([]RunnerSpec, error) {
	certifyWitnessGraph(e, taskID)
	resolver := &recordingArchitectResolver{}
	e.Runners = resolver
	_, err := e.resolveRunner(RunnerSpec{Role: roles.Architect, Agent: e.Config.Architect, Source: event.SourceArchitect, TaskID: taskID})
	return resolver.asked, err
}

// W2 THE TWO MECHANISMS AGREE. For one task record, the fresh-run path and the
// planned-resume path mint the same architect binding on EVERY referent, each
// captured where its own path reached the architect.
//
// Fails when: any referent the fresh run derives is not derived by the resume
// -- the measured defect was one referent (the objective) out of five.
func TestAFreshRunAndItsResumeMintTheSameArchitectBinding(t *testing.T) {
	resumed, events, task := notConvergedResume(t)
	resumedTurns, seen := resumeToOwedReplan(t, resumed, events, task)
	if len(resumedTurns) == 0 {
		t.Fatalf("the resumed architect turn did not reach resolver selection:\n%s", narrated(seen))
	}

	// The fresh path over the same repository, candidate record and objective,
	// in a process that shares nothing in memory with the resumed one.
	store, err := session.New(resumed.Repo.Root, "session-fresh")
	if err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	freshEvents, cancel := bus.Subscribe(128)
	t.Cleanup(cancel)
	fresh := New(resumed.Repo, config.Default(), bus, store, "session-fresh")
	serveCertifiedStart(t, fresh)
	resolver := &recordingArchitectResolver{}
	fresh.Runners = resolver
	fresh.run(context.Background(), task.TaskID, task.Task, SubmittedUnattended)
	freshTurns := resolver.architectTurns()
	if len(freshTurns) == 0 {
		t.Fatalf("the fresh architect turn did not reach resolver selection:\n%s", narrated(drainEvents(freshEvents)))
	}

	f, r := freshTurns[0], resumedTurns[0]
	for _, field := range []struct{ name, fresh, resumed string }{
		{"TaskID", f.TaskID, r.TaskID},
		{"ObjectiveDigest", f.ObjectiveDigest, r.ObjectiveDigest},
		{"BaseSHA", f.BaseSHA, r.BaseSHA},
		{"GraphRepository", f.GraphRepository, r.GraphRepository},
		{"GraphBuildCommit", f.GraphBuildCommit, r.GraphBuildCommit},
	} {
		if field.fresh == "" || field.fresh != field.resumed {
			t.Errorf("%s: the fresh run minted %q and the resume minted %q", field.name, field.fresh, field.resumed)
		}
	}
}

// emptyInputDigest is SHA-256 of zero bytes: what a repair that hashes an
// absent objective would fabricate.
const emptyInputDigest = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// W3 AN ABSENT OBJECTIVE STILL REFUSES -- CONTROL. A record that carries no
// objective text yields no identity, and the owed architect turn refuses.
//
// Fails when: absence is filled in -- defaulted, read from some rendering, or
// hashed into the digest of an empty input -- or the refusal is left to a
// resolver rather than made before one is chosen.
func TestAnAbsentRecordedObjectiveMintsNoIdentity(t *testing.T) {
	e, events, task := notConvergedResume(t)
	task.Task = ""
	turns, seen := resumeToOwedReplan(t, e, events, task)
	if len(turns) != 0 {
		t.Fatalf("an architect turn with no recorded objective reached resolver selection: %+v", turns[0])
	}
	if got := e.architectureBinding(task.TaskID).ObjectiveDigest; got != "" {
		t.Fatalf("an absent objective produced identity %q", got)
	}
	text := narrated(seen)
	if strings.Contains(text, emptyInputDigest) {
		t.Fatalf("an absent objective was turned into the digest of an empty input:\n%s", text)
	}
	if !contains(seen, event.WorkflowFailed) || !strings.Contains(text, "missing objective digest") {
		t.Fatalf("the owed turn did not refuse naming the absent objective:\n%s", text)
	}
}

// W4 THE DIAGNOSIS NAMES THE MISSING REFERENT, AND THE RESOLVER IS NEVER
// CONSULTED. Only the objective is missing; every other referent is certified.
//
// Fails when: the guard at resolveRunner's architect branch is absent or sits
// after resolver selection (the recording double then sees the turn), when
// the default command line is built for it (no refusal with no resolver), or
// when the refusal describes an unavailable adapter instead of the referent.
func TestAnIncompleteArchitectBindingIsRefusedBeforeAnyResolver(t *testing.T) {
	e, _, task := resumeHarness(t, false)
	// Nothing recorded for the task: this process holds no objective.
	asked, err := mintArchitectBinding(e, task.TaskID)
	if len(asked) != 0 {
		t.Fatalf("the resolver was consulted for an incomplete binding: %+v", asked[0].Architecture)
	}
	if err == nil {
		t.Fatal("an incomplete architect binding was not refused")
	}
	msg := err.Error()
	if strings.Contains(msg, "no adapter") || strings.Contains(msg, "returned no adapter") {
		t.Fatalf("an incomplete binding was described as an unavailable adapter: %v", msg)
	}
	if !strings.Contains(msg, "objective digest") {
		t.Fatalf("the refusal does not name the missing referent: %v", msg)
	}
	for _, present := range []string{"candidate base", "graph repository", "graph build commit"} {
		if strings.Contains(msg, "missing "+present) || strings.Contains(msg, ", "+present) {
			t.Fatalf("the refusal names %s as missing, which is present: %v", present, msg)
		}
	}
	// And with NO resolver configured, the default command line is not built
	// for it either: a refusal, not a CLI adapter.
	e.Runners = nil
	if resolved, err := e.resolveRunner(RunnerSpec{Role: roles.Architect, Agent: e.Config.Architect,
		Source: event.SourceArchitect, TaskID: task.TaskID}); err == nil {
		t.Fatalf("the no-resolver fallback built %q for an incomplete binding", resolved.Name)
	}
}

// W5 PROVENANCE IS NOT OVERWRITTEN -- CONTROL. A process that already holds
// the task's objective keeps it, with its original provenance, when the same
// task is resumed -- and its owed architect turn is bound to that objective.
//
// Fails when: the resume boundary records the objective unconditionally
// (recordObjective instead of recordObjectiveIfAbsent), which would replace
// the held text and demote a human-requested task to ResumedGoverned.
func TestAResumeKeepsTheObjectiveAProcessAlreadyHolds(t *testing.T) {
	e, events, task := notConvergedResume(t)
	held := Objective{Text: "the objective as it was submitted", Provenance: RequestedByHuman}
	e.recordObjective(task.TaskID, held)
	turns, seen := resumeToOwedReplan(t, e, events, task)
	if got := e.objective(task.TaskID); got != held {
		t.Fatalf("the resume replaced the held objective %+v with %+v", held, got)
	}
	if len(turns) == 0 {
		t.Fatalf("the owed re-plan never reached the architect:\n%s", narrated(seen))
	}
	if want := roles.BindArchitecture(task.TaskID, held.Text, "", "", "").ObjectiveDigest; turns[0].ObjectiveDigest != want {
		t.Fatalf("the architect turn names objective %q, not the held objective %q", turns[0].ObjectiveDigest, want)
	}
}

// W6 THE PATH THAT ALREADY WORKS STILL WORKS -- CONTROL. The resume of an
// unplanned task binds its architect turn to the recorded objective as before.
//
// Fails when: the unplanned resume stops restoring the recorded objective, or
// binds anything other than the record's task text under ResumedGoverned.
func TestAnUnplannedResumeStillBindsTheRecordedObjective(t *testing.T) {
	e, events, task := resumeHarness(t, false)
	task.Planned = false
	serveCertifiedStart(t, e)
	resolver := &recordingArchitectResolver{}
	e.Runners = resolver
	e.Resume(context.Background(), task)
	seen := settleResume(t, events)
	if got := e.objective(task.TaskID); got.Text != task.Task || got.Provenance != ResumedGoverned {
		t.Fatalf("the unplanned resume no longer carries the recorded objective: %+v", got)
	}
	turns := resolver.architectTurns()
	if len(turns) == 0 {
		t.Fatalf("the unplanned resume's architect turn did not reach resolver selection:\n%s", narrated(seen))
	}
	want := roles.BindArchitecture(task.TaskID, task.Task, e.governedBase(task.TaskID), witnessGraphRepository, witnessGraphCommit)
	if got := turns[0]; got != want || !got.Valid() {
		t.Fatalf("the unplanned resume minted %+v, want %+v", got, want)
	}
}

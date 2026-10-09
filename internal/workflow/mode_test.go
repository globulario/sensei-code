package workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// TestPlainMessageStartsAssisted covers "plain `sensei-code` starts in assisted
// mode": the default entry point is conversational, and governed execution has
// its own explicit one.
//
// This reads the call graph rather than running an engine, because what matters
// is which function an ordinary message reaches, and that is a static fact.
func TestPlainMessageStartsAssisted(t *testing.T) {
	body := funcBody(t, "internal/workflow/assisted.go", "Submit")
	if !strings.Contains(body, "SubmitAssisted") {
		t.Fatalf("Submit does not delegate to SubmitAssisted:\n%s", body)
	}
	if strings.Contains(body, "e.run(") {
		t.Fatalf("Submit still starts governed execution directly:\n%s", body)
	}

	// SubmitGoverned reaches governed execution through the shared submit path.
	// Following one hop keeps this a check on the call graph rather than on
	// where a line happens to sit.
	governed := funcBody(t, "internal/workflow/assisted.go", "SubmitGoverned")
	if !strings.Contains(governed, "e.submit(") {
		t.Fatalf("SubmitGoverned does not start the governed workflow:\n%s", governed)
	}
	shared := funcBody(t, "internal/workflow/assisted.go", "submit")
	if !strings.Contains(shared, "e.run(") {
		t.Fatalf("submit does not start governed execution:\n%s", shared)
	}
	// Provenance is a parameter of submission, not a constant of the workflow.
	if !strings.Contains(governed, "RequestedByHuman") {
		t.Fatalf("SubmitGoverned no longer records human provenance:\n%s", governed)
	}
	unattended := funcBody(t, "internal/workflow/assisted.go", "SubmitGovernedUnattended")
	if strings.Contains(unattended, "RequestedByHuman") {
		t.Fatalf("an unattended submission claims a human asked for it:\n%s", unattended)
	}
	// The observation lane is fixed at submission, where nothing downstream can
	// widen it. If it ever becomes inferable from a plan it stops being
	// structural and becomes a claim a worker can make about itself.
	observe := funcBody(t, "internal/workflow/assisted.go", "SubmitObservation")
	if !strings.Contains(observe, "true") {
		t.Fatalf("SubmitObservation does not fix the observation lane at submission:\n%s", observe)
	}
}

// TestAssistedWorkflowCreatesNoGovernedArtifacts covers "assisted mode never
// creates candidate/admission vocabulary or receipts".
//
// The check is structural: the assisted state machine must not so much as
// reference the machinery that produces governed artifacts. A test that ran one
// turn and inspected the output would pass for the wrong reason on any input
// that happened not to reach the code.
func TestAssistedWorkflowCreatesNoGovernedArtifacts(t *testing.T) {
	body := fileText(t, "internal/workflow/assisted.go")
	forbidden := map[string]string{
		"CreateWorktree":   "assisted mode cut a candidate worktree",
		"candidate.":       "assisted mode touched candidate identity",
		"CandidateDiff":    "assisted mode produced a candidate diff",
		"audit_diff":       "assisted mode ran a governed diff audit",
		"recordDecision":   "assisted mode recorded a decision of record",
		"emitChangeReport": "assisted mode emitted a change report",
		"offerPullRequest": "assisted mode offered a pull request",
		"admit_change":     "assisted mode reached for admission",
		"certifyStart":     "assisted mode ran the governed start gate",
		"runCandidate":     "assisted mode entered the candidate loop",
	}
	for token, complaint := range forbidden {
		if strings.Contains(body, token) {
			t.Errorf("%s (found %q in assisted.go)", complaint, token)
		}
	}
}

// TestAssistedPromptForbidsActingAndPointsAtRun keeps the behaviour honest at
// the other end: the architect must not quietly start doing the work in a mode
// that produces no candidate and no audit, because that is unreviewed change
// with none of the machinery that would catch it.
func TestAssistedPromptForbidsActingAndPointsAtRun(t *testing.T) {
	prompt := assistedPrompt("/repo", "example.com/x", "ChatGPT", "why is this here", "", nil, "ws", "pf", "(none)", "(none)", "(none)")
	for _, required := range []string{"ASSISTED", "/run", "Do not produce JSON"} {
		if !strings.Contains(prompt, required) {
			t.Errorf("assisted prompt does not contain %q", required)
		}
	}
	// The prompt is hard-wrapped, so match on normalised whitespace rather than
	// on an exact line: a reflow must not silently drop this guarantee.
	if !strings.Contains(strings.Join(strings.Fields(prompt), " "), "Do not start doing the work here") {
		t.Error("assisted prompt does not forbid carrying the work out")
	}
}

// TestAssistedTurnStatesItsCaveats covers the honesty requirement that
// distinguishes assisted from unaware. An assisted answer given while the graph
// cannot vouch for itself must say so, because nothing downstream will catch an
// overstated assisted claim.
func TestAssistedTurnStatesItsCaveats(t *testing.T) {
	prompt := assistedPrompt("/repo", "example.com/x", "ChatGPT", "q", "",
		[]string{"Sensei cannot vouch for its own graph right now (graph stale)"}, "ws", "pf", "(none)", "(none)", "(none)")
	if !strings.Contains(prompt, "graph stale") {
		t.Fatalf("caveats did not reach the architect:\n%s", prompt)
	}

	clean := assistedPrompt("/repo", "example.com/x", "ChatGPT", "q", "", nil, "ws", "pf", "(none)", "(none)", "(none)")
	if !strings.Contains(clean, "(none)") {
		t.Fatal("a turn with no caveats did not say so explicitly, which reads as truncation")
	}
}

// TestModeIsNeverDerivedFromConfiguration covers "an assisted task cannot
// accidentally transition to governed vocabulary because of a local config
// flag". There is deliberately no configuration input to mode at all.
func TestModeIsNeverDerivedFromConfiguration(t *testing.T) {
	body := fileText(t, "internal/workflow/mode.go")
	if strings.Contains(body, "config.") {
		t.Error("mode.go reads configuration; a local flag could then change a task's posture")
	}

	// The two constructors are the only way to make a TaskMode with a stated
	// provenance, and each hard-codes its own.
	if got := assistedMode(); got.Mode != Assisted || got.Provenance != DefaultEntry {
		t.Fatalf("assisted mode is not fixed: %+v", got)
	}
	if got := governedMode(RequestedByHuman); got.Mode != Governed || got.Provenance != RequestedByHuman {
		t.Fatalf("governed mode is not fixed: %+v", got)
	}
}

// TestModeAlwaysDescribesItsProvenance covers "UI always displays the actual
// mode and provenance".
func TestModeAlwaysDescribesItsProvenance(t *testing.T) {
	for _, tm := range []TaskMode{
		assistedMode(),
		governedMode(RequestedByHuman),
		governedMode(ResumedGoverned),
	} {
		d := tm.Describe()
		if !strings.Contains(d, string(tm.Mode)) {
			t.Errorf("description %q omits the mode", d)
		}
		if !strings.Contains(d, string(tm.Provenance)) {
			t.Errorf("description %q omits why the task is in that mode", d)
		}
	}
	// An unset mode reads as assisted rather than as blank, because a blank
	// posture in the bar is worse than a wrong one: it looks like a rendering
	// bug and gets ignored.
	if got := (TaskMode{}).Label(); got != string(Assisted) {
		t.Fatalf("an unset mode labelled itself %q", got)
	}
}

// TestGovernedEntryRequiresTheCanonicalPrerequisites covers "switching to
// governed mode requires the canonical prerequisites for that task": the
// governed path still runs the start gate and still establishes an exact
// candidate identity, so /run cannot be a shortcut past them.
func TestGovernedEntryRequiresTheCanonicalPrerequisites(t *testing.T) {
	body := fileText(t, "internal/workflow/engine.go")
	for _, required := range []string{"certifyStart", "candidate.Establish"} {
		if !strings.Contains(body, required) {
			t.Fatalf("the governed workflow no longer calls %s", required)
		}
	}
}

func fileText(t *testing.T, rel string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../"+rel, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var b strings.Builder
	ast.Inspect(f, func(n ast.Node) bool {
		if id, ok := n.(*ast.Ident); ok {
			b.WriteString(id.Name)
			b.WriteString(" ")
		}
		if sel, ok := n.(*ast.SelectorExpr); ok {
			if x, ok := sel.X.(*ast.Ident); ok {
				b.WriteString(x.Name + "." + sel.Sel.Name + " ")
			}
		}
		if lit, ok := n.(*ast.BasicLit); ok {
			b.WriteString(lit.Value)
			b.WriteString(" ")
		}
		return true
	})
	return b.String()
}

func funcBody(t *testing.T, rel, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../"+rel, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var out string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil {
			return true
		}
		var b strings.Builder
		ast.Inspect(fn.Body, func(inner ast.Node) bool {
			switch n := inner.(type) {
			case *ast.SelectorExpr:
				// Nested selectors such as e.Store.Load are rendered whole, so
				// an assertion can name the path it actually cares about.
				b.WriteString(exprPath(n) + "( ")
			case *ast.Ident:
				b.WriteString(n.Name + " ")
			}
			return true
		})
		out = b.String()
		return false
	})
	if out == "" {
		t.Fatalf("function %s not found in %s", name, rel)
	}
	return out
}

// TestOrdinaryConfirmationsAreNotFiledAsGovernanceKnowledge guards the boundary
// P0.6 could easily blur. Agreeing to open a pull request is a product
// confirmation, not an answer to a question the graph could not settle, and
// proposing it as a contract would fill Sensei's review queue with restatements
// of the user interface.
func TestOrdinaryConfirmationsAreNotFiledAsGovernanceKnowledge(t *testing.T) {
	body := fileText(t, "internal/workflow/engine.go")
	// The persistence call must be reached only under a condition check.
	if !strings.Contains(body, "authority.Persist") {
		t.Fatal("resolutions are no longer submitted to Sensei at all")
	}
	for _, caller := range []string{"offerPullRequest"} {
		fn := funcBody(t, "internal/workflow/engine.go", caller)
		if strings.Contains(fn, "authority.Persist") {
			t.Errorf("%s proposes a governance contract for an ordinary confirmation", caller)
		}
	}
}

// TestReplayIsAvoidedByTheGraphNotByACache states how "asking the same question
// twice" is actually prevented, because the mechanism is easy to get wrong.
//
// A promoted resolution becomes graph knowledge, so the next scoped preflight
// covers the region and routeAuthority returns architectural authority without
// asking anyone. Nothing consults a local record of past answers: doing so
// would let this program's own unpromoted proposal act as canon, which is
// precisely the separation the design keeps.
func TestReplayIsAvoidedByTheGraphNotByACache(t *testing.T) {
	// Before: the region is uncovered, so a human owns it.
	uncovered := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",`+healthyAuthority+`}`)
	if got := routeAuthorityForAction(uncovered, nil, plannedEdit()); got.Granted() || !got.ClosesGap() {
		t.Fatalf("an uncovered region was not stopped as a bounded gap: %+v", got)
	}

	// After promotion the same question is covered, and no human is asked.
	covered := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"direct_invariants": [{"id":"invariant:promoted.from.resolution","label":"the human decided this","severity":"critical","status":"active"}],
		`+healthyAuthority+`
	}`)
	if got := routeAuthorityForAction(covered, nil, plannedEdit()); got.Route != RouteArchitectural {
		t.Fatalf("a promoted resolution still reprompted the human: %+v", got)
	}

	// And the router has no access to any local store of past answers.
	router := fileText(t, "internal/workflow/authority.go")
	for _, forbidden := range []string{"Load", "ReadFile", "resolutions"} {
		if strings.Contains(router, forbidden) {
			t.Errorf("the router reads %q; replay avoidance must come from the graph, not from a local cache", forbidden)
		}
	}
}

// TestWorkerSwitchCarriesSemanticStateNotJustProse is the integration-level
// guarantee for cross-agent continuity: what the second worker receives is
// assembled from the task's recorded position, not from a summary paragraph.
func TestWorkerSwitchCarriesSemanticStateNotJustProse(t *testing.T) {
	// Both files: the handoff is assembled in adversarial.go and consumed in
	// engine.go. The property is that state survives a change of worker, not
	// that it is written down in one particular file.
	body := fileText(t, "internal/workflow/engine.go") + fileText(t, "internal/workflow/adversarial.go")
	if strings.Contains(body, "handoverNote") {
		t.Error("the prose handover note is still in use; semantic state should supersede it")
	}
	for _, required := range []string{"state.Handover", "state.OpenFindings", "state.RecordWorker", "taskstate.Revising"} {
		if !strings.Contains(body, required) {
			t.Errorf("the worker switch does not use %s, so state does not survive the change", required)
		}
	}
}

// TestAuthorityDecisionsAreReadFromTheRecordNotReinvented keeps continuity
// sourced from the event log that already exists. A second store of past human
// decisions would need reconciling with the first, and the two would disagree
// exactly when it mattered.
//
// The record is read through the task's canonical member history (70B2a2,
// RULING-220 D6): a decision a foreign session recorded is not this task's.
func TestAuthorityDecisionsAreReadFromTheRecordNotReinvented(t *testing.T) {
	fn := funcBody(t, "internal/workflow/engine.go", "authorityDecisions")
	if !strings.Contains(fn, "e.memberHistory") {
		t.Fatal("prior human decisions are not read from the task's canonical member history")
	}
	if strings.Contains(fn, "e.Store.Load") {
		t.Fatal("prior human decisions are read from the raw holder record, so a foreign session's answer is the task's")
	}
	if strings.Contains(fn, "os.ReadFile") {
		t.Fatal("prior human decisions are read from a separate file, creating a second source of truth")
	}
}

// TestB2a2R220W8EveryAuthorityBearingReaderReachesTheCanonicalProjection is
// the structural supplement to the behavioral witnesses (RULING-220 D1, D6,
// W8): every production reader of task history that can influence resume,
// discovery, authority, plan, grant, gap or terminal state reaches the
// canonical member-history projection, and none reads the raw record or folds
// it raw. Indirect callers count: ResumeTask re-reads the task through
// canonicalTask, and every restorer it calls reads only the task it was
// handed; FindActive and the TUI (Discovery.ResumableRecord) read
// CanonicalInterrupted.
func TestB2a2R220W8EveryAuthorityBearingReaderReachesTheCanonicalProjection(t *testing.T) {
	for _, reader := range []struct{ fn, via string }{
		{"memberHistory", "e.Store.TaskHistory"},
		{"memberHistory", "h.Recorded("},
		{"gapResolutions", "e.memberHistory"},
		{"answeredConditions", "e.memberHistory"},
		{"authorityDecisions", "e.memberHistory"},
		{"recordedObjective", "store.TaskHistory"},
		{"canonicalTask", "session.CanonicalInterrupted"},
		{"taskHistoryRefusal", "e.gapResolutions"},
	} {
		body := funcBody(t, "internal/workflow/engine.go", reader.fn)
		if !strings.Contains(body, reader.via) {
			t.Errorf("%s does not read through %s", reader.fn, reader.via)
		}
		for _, raw := range []string{".Load(", "session.FindInterrupted("} {
			if strings.Contains(body, raw) {
				t.Errorf("%s reads raw task history (%s)", reader.fn, raw)
			}
		}
	}
	// ResumeTask re-reads what the task owes after binding, and gates its P9
	// state, before any lane: the caller's projection only selects the task.
	resume := funcBody(t, "internal/workflow/engine.go", "ResumeTask")
	for _, step := range []string{"e.canonicalTask(", "e.taskHistoryRefusal(", "task.Unavailable"} {
		if !strings.Contains(resume, step) {
			t.Errorf("ResumeTask does not reach %s", step)
		}
	}
	// And the post-bind canonical projection is what every lane and
	// restoration consumes: the caller's lane is checked against it, and the
	// task is REPLACED by it before the first consumer reads anything.
	src := rawSource(t, "internal/workflow/engine.go")
	resumeSrc := src[strings.Index(src, "func (e *Engine) ResumeTask("):]
	resumeSrc = resumeSrc[:strings.Index(resumeSrc, "\n}\n")]
	replaced := strings.Index(resumeSrc, "task = canonical\n")
	if replaced < 0 || !strings.Contains(resumeSrc, "resumeLaneRefusal(task, canonical)") {
		t.Error("ResumeTask does not replace the caller's projection with the post-bind canonical one")
	}
	for _, consumer := range []string{"e.restorePlanAdmissionRefusals(task)", "e.resumeAuthority(ctx, task)",
		"e.resumeUnplannedArchitecture(ctx, task)", "e.restorePlanBound(task)", "e.restorePlanAttempt(task",
		"e.restoreTestEditGrants(task", "e.restoreProspectiveGrants(task", "waitingReviewFrom(task)",
		"owedReplan(task.NotConverged", "e.owedPlanAdmissionRefusal(task.TaskID)"} {
		if at := strings.Index(resumeSrc, consumer); at < 0 || at < replaced {
			t.Errorf("ResumeTask's consumer %s does not read the post-bind canonical projection", consumer)
		}
	}
	// The CLI and the TUI select through ONE repository-wide refusal
	// precedence, and a continued record is never loaded as an empty one.
	if b := funcBody(t, "cmd/sensei-code/main.go", "interactiveDiscovery"); !strings.Contains(b, ".ResumableIn(") || strings.Contains(b, ".ScopedTo(") {
		t.Error("the TUI's record is not established through the shared refusal precedence (Discovery.ResumableIn)")
	}
	if b := funcBody(t, "internal/tui/model.go", "New"); !strings.Contains(b, "inventory.ResumableRecord(") {
		t.Error("the TUI's resumable set is not the canonical discovery of the record it replays (Discovery.ResumableRecord)")
	}
	if b := funcBody(t, "internal/session/store.go", "ResumableRecord"); !strings.Contains(b, "CanonicalInterrupted ") || !strings.Contains(b, "d.TaskRefusal(") {
		t.Error("Discovery.ResumableRecord does not fold the canonical projection under the repository-wide TaskRefusal")
	}
	if !strings.Contains(funcBody(t, "cmd/sensei-code/resume.go", "resumeAuthorityAnswered"), "inventory.TaskRefusal(") {
		t.Error("CLI selection bypasses the shared refusal precedence (Discovery.TaskRefusal)")
	}
	if b := funcBody(t, "internal/session/store.go", "ResumableIn"); !strings.Contains(b, "d.TaskRefusal(") {
		t.Error("Discovery.ResumableIn does not apply the repository-wide TaskRefusal")
	}
	if strings.Contains(funcBody(t, "cmd/sensei-code/main.go", "loadConversation"), "ReadRecord(") {
		t.Error("the startup loads a selected record through a reader that turns its absence into an empty history")
	}
	if !strings.Contains(funcBody(t, "internal/workflow/engine.go", "execute"), "e.taskHistoryRefusal(") {
		t.Error("execute decides from the task's P9 state without first establishing its canonical history")
	}
	// A task history's presence and contents are ONE observation: a separate
	// presence check lets a record that vanishes before the read report an
	// empty history as a recorded one.
	if b := funcBody(t, "internal/session/store.go", "TaskHistory"); strings.Contains(b, "os.Stat(") ||
		strings.Contains(b, "ReadRecord(") || !strings.Contains(b, "s.Load(") {
		t.Error("Store.TaskHistory observes the record's presence apart from reading it, or through a reader that turns absence into an empty history")
	}
	// Discovery folds members only, and the raw fold has no production caller
	// but the projection.
	if !strings.Contains(funcBody(t, "internal/session/store.go", "FindActive"), "CanonicalInterrupted ") {
		t.Error("FindActive folds a record without the canonical projection")
	}
	canonical := funcBody(t, "internal/session/store.go", "CanonicalInterrupted")
	if !strings.Contains(canonical, "ProjectTaskHistory ") || !strings.Contains(canonical, "h.Members") {
		t.Error("CanonicalInterrupted does not fold the canonical member history")
	}
	for _, rel := range []string{
		"internal/session/store.go", "internal/workflow/engine.go", "internal/workflow/authority.go",
		"internal/tui/model.go", "cmd/sensei-code/main.go", "cmd/sensei-code/resume.go",
		"cmd/sensei-code/resume_blocked.go", "internal/workflow/suppliedplan.go", "internal/workflow/testedit.go",
		"internal/workflow/prospective.go", "internal/workflow/waiting_review.go", "cmd/sensei-code/resume_review.go",
	} {
		src := rawSource(t, rel)
		calls := strings.Count(src, "FindInterrupted(")
		allowed := 0
		if rel == "internal/session/store.go" {
			// The declaration and the one call inside CanonicalInterrupted.
			allowed = strings.Count(src, "func FindInterrupted(") + strings.Count(canonicalSource(t), "FindInterrupted(")
		}
		if calls > allowed {
			t.Errorf("%s calls the raw fold FindInterrupted outside the canonical projection (%d > %d)", rel, calls, allowed)
		}
	}
	// The TUI's resumable tasks come from the same discovery as the CLI's,
	// never from a fold of the history it replays.
	if !strings.Contains(funcBody(t, "cmd/sensei-code/main.go", "interactiveDiscovery"), "session.FindActive(") {
		t.Error("the interactive startup does not take /resume's tasks from canonical discovery")
	}
}

// canonicalSource is CanonicalInterrupted's source text.
func canonicalSource(t *testing.T) string {
	t.Helper()
	src := rawSource(t, "internal/session/store.go")
	start := strings.Index(src, "func CanonicalInterrupted(")
	if start < 0 {
		t.Fatal("CanonicalInterrupted is not declared")
	}
	end := strings.Index(src[start:], "\n}\n")
	return src[start : start+end]
}

// exprPath renders a possibly nested selector as a dotted path, so a test can
// assert on "e.Store.Load" rather than on whichever fragment the walker
// happened to reach first.
func exprPath(e ast.Expr) string {
	switch n := e.(type) {
	case *ast.Ident:
		return n.Name
	case *ast.SelectorExpr:
		return exprPath(n.X) + "." + n.Sel.Name
	case *ast.CallExpr:
		return exprPath(n.Fun)
	default:
		return ""
	}
}

// TestAnAnsweredConditionIsNotAskedTwice is the loop the acceptance run found.
//
// Authorizing does not change the graph. The router reads Sensei, Sensei still
// reports the region uncovered, and the identical condition escalates on the
// very next plan. One real run asked the human the same question thirteen times
// and never reached a candidate.
func TestAnAnsweredConditionIsNotAskedTwice(t *testing.T) {
	body := fileText(t, "internal/workflow/engine.go")
	if !strings.Contains(body, "applyAnsweredCondition") {
		t.Fatal("the escalation path does not consult conditions the human already answered")
	}
	fn := funcBody(t, "internal/workflow/engine.go", "answeredConditions")
	if !strings.Contains(fn, "e.memberHistory") {
		t.Error("answered conditions are not read from the task's canonical member history")
	}
	if strings.Contains(fn, "e.Store.Load") {
		t.Error("answered conditions are read from the raw holder record, so a foreign session's answer authorizes this task")
	}
	// Classification lives with the lookup, which is where subset coverage is
	// decided; answeredConditions now returns the answers themselves.
	apply := funcBody(t, "internal/workflow/engine.go", "applyAnsweredCondition")
	if !strings.Contains(apply, "Outcome.Permits(") {
		t.Error("an answer is not classified, so a refusal would read the same as an authorization")
	}
	// And it is classified by the outcome carried on the option, never by the
	// option's wording. The wording can be supplied by the architect, so a
	// label-matching classifier would let the party the boundary constrains
	// write the words that decide whether a human authorized anything.
	if strings.Contains(fn, "OptionLabel") || strings.Contains(apply, "OptionLabel") {
		t.Error("an answer is classified from its label; outcomes must come from the option, not its prose")
	}
	// Revise leaves the condition open rather than settling it, so a redesign
	// is routed on its own merits instead of being refused by the human's own
	// request for a redesign.
	if !strings.Contains(fn, "res.Outcome.Settles(") {
		t.Error("every answer settles the condition; revise must leave it open")
	}
}

// TestRememberingAnAnswerIsNotMakingItCanonical keeps the two paths apart. The
// run-scoped memory binds this task only; whether the answer becomes project
// knowledge is still Sensei's decision via authority.Persist.
func TestRememberingAnAnswerIsNotMakingItCanonical(t *testing.T) {
	fn := funcBody(t, "internal/workflow/engine.go", "answeredConditions")
	if strings.Contains(fn, "authority.Persist") {
		t.Error("the run-scoped memory writes to Sensei; remembering an answer must not promote it")
	}
	if strings.Contains(fn, "os.ReadFile") || strings.Contains(fn, "os.WriteFile") {
		t.Error("answered conditions use a separate store; the session record is the one source")
	}
	// The router still knows nothing about it: routing stays a pure function of
	// Sensei evidence and claims.
	router := fileText(t, "internal/workflow/authority.go")
	if strings.Contains(router, "answeredConditions") || strings.Contains(router, "Authorizes") {
		t.Error("the router consults past answers; it must route on Sensei evidence alone and let the caller apply the answer")
	}
}

// TestAStalledCandidateStopsInsteadOfBurningCycles pins the third loop the
// acceptance runs found.
//
// A worker asked to revise produced a byte-identical diff three times: same
// size, same audit digest. The remaining cycles could only produce it again,
// and the run ended in a timeout rather than a diagnosis. The guard belongs
// inside one worker's cycle loop rather than across the whole task, so a
// stalled worker still hands the candidate on to the next one.
func TestAStalledCandidateStopsInsteadOfBurningCycles(t *testing.T) {
	fn := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if !strings.Contains(fn, "verdict.InputDiffDigest") {
		t.Fatal("the review loop does not compare candidate digests, so a stalled worker runs every cycle")
	}
	body := fileText(t, "internal/workflow/engine.go")
	if !strings.Contains(body, "did not change between review cycles") {
		t.Error("a stalled candidate produces no diagnosis naming what happened")
	}
	// The message must carry the review the worker failed to act on, or the
	// next reader learns only that it stopped.
	if !strings.Contains(body, "The last review asked for") {
		t.Error("the stall diagnosis does not say what was being asked of the worker")
	}
}

// TestTheBaseIsPinnedBeforeTheWorkflowWritesToItsOwnRepository is the ordering
// bug the corrected deployment exposed.
//
// Once authority resolutions began landing in this repository's corpus rather
// than another's, the workflow started dirtying its own tree between the start
// gate and candidate creation — and then refused the run for uncommitted
// changes it had itself produced. The gate was right; the ordering was wrong.
func TestTheBaseIsPinnedBeforeTheWorkflowWritesToItsOwnRepository(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "execute")
	establish := strings.Index(body, "candidate.Establish")
	architect := strings.Index(body, "e.resolveArchitecture")
	if establish < 0 {
		t.Fatal("the governed run no longer establishes a candidate base")
	}
	if architect < 0 {
		t.Fatal("the governed run no longer consults the architect")
	}
	if establish > architect {
		t.Fatal("the base is established after the architect runs; a Level-3 resolution persisted in between dirties the tree and the base can no longer be taken")
	}

	// implement must consume the established identity rather than re-deriving
	// one, or the ordering fix is undone by the second observation.
	impl := funcBody(t, "internal/workflow/engine.go", "implement")
	if strings.Contains(impl, "candidate.Establish") {
		t.Error("implement re-establishes the base, re-observing a tree this run has already written to")
	}
	if !strings.Contains(impl, "tc.Identity") {
		t.Error("implement does not use the identity established at the start gate")
	}
}

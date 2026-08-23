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

	governed := funcBody(t, "internal/workflow/assisted.go", "SubmitGoverned")
	if !strings.Contains(governed, "e.run(") {
		t.Fatalf("SubmitGoverned does not start the governed workflow:\n%s", governed)
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
	if got := routeAuthority(uncovered, nil); got.Granted() || !got.ClosesGap() {
		t.Fatalf("an uncovered region was not stopped as a bounded gap: %+v", got)
	}

	// After promotion the same question is covered, and no human is asked.
	covered := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"direct_invariants": [{"id":"invariant:promoted.from.resolution","label":"the human decided this","severity":"critical","status":"active"}],
		`+healthyAuthority+`
	}`)
	if got := routeAuthority(covered, nil); got.Route != RouteArchitectural {
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
func TestAuthorityDecisionsAreReadFromTheRecordNotReinvented(t *testing.T) {
	fn := funcBody(t, "internal/workflow/engine.go", "authorityDecisions")
	if !strings.Contains(fn, "e.Store.Load") {
		t.Fatal("prior human decisions are not read from the session record")
	}
	if strings.Contains(fn, "os.ReadFile") {
		t.Fatal("prior human decisions are read from a separate file, creating a second source of truth")
	}
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
	if !strings.Contains(fn, "e.Store.Load") {
		t.Error("answered conditions are not read from the session record")
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

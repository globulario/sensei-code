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
	prompt := assistedPrompt("/repo", "example.com/x", "ChatGPT", "why is this here", "", nil, "ws", "pf")
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
		[]string{"Sensei cannot vouch for its own graph right now (graph stale)"}, "ws", "pf")
	if !strings.Contains(prompt, "graph stale") {
		t.Fatalf("caveats did not reach the architect:\n%s", prompt)
	}

	clean := assistedPrompt("/repo", "example.com/x", "ChatGPT", "q", "", nil, "ws", "pf")
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
// P0.6 could easily blur. Approving a plan and agreeing to open a pull request
// are product confirmations, not answers to questions the graph could not
// settle, and proposing them as contracts would fill Sensei's review queue with
// restatements of the user interface.
func TestOrdinaryConfirmationsAreNotFiledAsGovernanceKnowledge(t *testing.T) {
	body := fileText(t, "internal/workflow/engine.go")
	// The persistence call must be reached only under a condition check.
	if !strings.Contains(body, "authority.Persist") {
		t.Fatal("resolutions are no longer submitted to Sensei at all")
	}
	for _, caller := range []string{"approvePlan", "offerPullRequest"} {
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
	if got := routeAuthority(uncovered, nil); got.Route != RouteHuman {
		t.Fatalf("an uncovered region did not ask the human: %+v", got)
	}

	// After promotion the same question is covered, and no human is asked.
	covered := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"required_actions": ["Change risk: blast=local, approval=none"],
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
	body := fileText(t, "internal/workflow/engine.go")
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

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
	prompt := assistedPrompt("/repo", "example.com/x", "ChatGPT", "why is this here", "", nil)
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
		[]string{"Sensei cannot vouch for its own graph right now (graph stale)"})
	if !strings.Contains(prompt, "graph stale") {
		t.Fatalf("caveats did not reach the architect:\n%s", prompt)
	}

	clean := assistedPrompt("/repo", "example.com/x", "ChatGPT", "q", "", nil)
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
			if sel, ok := inner.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); ok {
					b.WriteString(x.Name + "." + sel.Sel.Name + "( ")
				}
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

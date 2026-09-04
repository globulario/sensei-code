package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

// Exactly one process owns the engine for a repository.
//
// `submit` hands work to an owner that already exists. If it ever built its own
// Engine it would be a second owner executing a task the first knows nothing
// about -- two workflows over one repository, which is the topology this whole
// feature exists to avoid.
func TestSubmitCreatesNoEngineOfItsOwn(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "submit.go", nil, 0)
	if err != nil {
		t.Fatalf("parse submit.go: %v", err)
	}

	for _, imp := range file.Imports {
		p := strings.Trim(imp.Path.Value, `"`)
		if p == "github.com/globulario/sensei-code/internal/workflow" {
			t.Fatal("submit imports the workflow package; it would be a second engine owner")
		}
		if p == "github.com/globulario/sensei-code/internal/sensei" {
			t.Fatal("submit starts its own Sensei client; the owner does that")
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if name := pkg.Name + "." + sel.Sel.Name; name == "workflow.New" || strings.HasPrefix(sel.Sel.Name, "Submit") && pkg.Name == "workflow" {
			t.Fatalf("submit calls %s: it must hand work to the running owner, not become one", name)
		}
		return true
	})
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(raw)
}

// The control command owns exactly one engine, and the objective channel it
// serves reaches that engine's local submission entry and no other.
func TestControlServesTheObjectiveChannelIntoItsOwnEngine(t *testing.T) {
	source := readFile(t, "control.go")
	if !strings.Contains(source, "server.ListenLocal(repo.Root)") {
		t.Fatal("the control process does not bind an objective channel, so no operator can place work")
	}
	if !strings.Contains(source, "engine.SubmitGovernedLocal(ctx, task)") {
		t.Fatal("the objective channel does not reach this engine's local submission entry")
	}
	// One engine, built once.
	if n := strings.Count(source, "workflow.New("); n != 1 {
		t.Fatalf("control.go builds %d engines; exactly one process owns exactly one", n)
	}
	// And nothing here reaches an entry point that would misattribute the
	// objective.
	for _, forbidden := range []string{"SubmitGovernedUnattended", "SubmitGoverned(", "RequestedByHuman"} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("control.go reaches %s; a locally placed objective has its own provenance", forbidden)
		}
	}
}

// An objective placed into a silent process is an objective nobody can act on.
//
// This happened in a live run: the operator submitted, the command reported
// success, the start gate refused the workspace two seconds later, and the
// terminal showed a banner. Submission succeeding and the run succeeding are
// different facts, and the second one has to be visible somewhere a person is
// looking.
func TestTheControlProcessSaysWhatBecameOfTheObjective(t *testing.T) {
	source := readFile(t, "control.go")
	if !strings.Contains(source, "bus.Subscribe(") {
		t.Fatal("the control process watches nothing its engine emits")
	}
	if !strings.Contains(source, "renderEvent(ev)") {
		t.Fatal("the control process observes events and prints none of them")
	}
	if !strings.Contains(source, "terminal(ev.Kind)") {
		t.Fatal("the control process does not report terminals, so a failed run is silent")
	}
}

// Every workflow terminal has an exit code and is render-worthy. A kind added
// without both is one a headless caller waits out and a person never sees.
func TestEveryWorkflowTerminalIsAccountedFor(t *testing.T) {
	for _, kind := range []event.Kind{
		event.WorkflowCompleted, event.WorkflowFailed, event.WorkflowStopped,
		event.WorkflowTimedOut, event.WorkflowObserved,
		event.WorkflowAwaitingAuthority, event.WorkflowAwaitingReview,
	} {
		if _, ok := exitFor(kind, false); !ok {
			t.Errorf("%s has no exit code: a headless caller cannot tell what happened", kind)
		}
		if !terminal(kind) {
			t.Errorf("%s is not render-worthy: a quiet run would end without saying so", kind)
		}
	}
	// The codes are distinct, so a caller can tell the outcomes apart.
	seen := map[int]event.Kind{}
	for _, kind := range []event.Kind{
		event.WorkflowCompleted, event.WorkflowFailed, event.WorkflowTimedOut,
		event.WorkflowObserved, event.WorkflowAwaitingAuthority, event.WorkflowAwaitingReview,
	} {
		code, _ := exitFor(kind, false)
		if other, clash := seen[code]; clash {
			t.Errorf("%s and %s both exit %d", kind, other, code)
		}
		seen[code] = kind
	}
}

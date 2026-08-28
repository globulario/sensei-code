package workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// The writer must fire ONLY where a gap went unclosed.
//
// A recipe writer reachable from anywhere else is a general-purpose recipe
// generator, and the safety argument -- that a question is only recorded when
// an investigation actually failed to establish something -- would not hold.
// Asserted against the real source rather than a description of it.
func TestTheQuestionWriterIsReachableOnlyFromAnUnclosedGap(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "engine.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var callers []string
	ast.Inspect(f, func(n ast.Node) bool {
		fn, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		ast.Inspect(fn, func(m ast.Node) bool {
			call, ok := m.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "recordClosureQuestion" {
				return true
			}
			callers = append(callers, fn.Name.Name)
			return true
		})
		return true
	})
	if len(callers) == 0 {
		t.Fatal("recordClosureQuestion is never called; the closure round cannot leave a question")
	}
	// Every call site must sit beside the escalation that reports an unclosed
	// gap. That text is the marker for "investigation failed", so a call that
	// is not near it is a call from somewhere else.
	src := rawSource(t, "internal/workflow/engine.go")
	const marker = "the knowledge gap did not close"
	for _, chunk := range strings.Split(src, "recordClosureQuestion")[:len(callers)] {
		tail := chunk
		if len(tail) > 700 {
			tail = tail[len(tail)-700:]
		}
		if !strings.Contains(tail, marker) {
			t.Fatalf("a call to recordClosureQuestion is not guarded by an unclosed gap; "+
				"the writer has become a general-purpose recipe generator (callers: %v)", callers)
		}
	}
}

// The current task must never be covered by what it wrote.
func TestDerivedCoverageExcludesTheWritingTask(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	i := strings.Index(src, "func (e *Engine) coverageAtWorld")
	if i < 0 {
		t.Fatal("coverageAtWorld not found")
	}
	body := src[i:]
	if j := strings.Index(body, "\nfunc "); j > 0 {
		body = body[:j]
	}
	if !strings.Contains(body, "ExcludingTask") {
		t.Fatal("coverageAtWorld does not exclude the writing task; a run could establish its " +
			"own authority by writing a question down")
	}
	if strings.Index(body, "ExcludingTask") > strings.Index(body, "AnchorsFor") {
		t.Fatal("the exclusion must happen before derivations are spent")
	}
}

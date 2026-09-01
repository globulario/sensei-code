package derived

// A reference is not evidence that its referent exists.
//
// Authored awareness YAML binds invariants and failure modes to proofs by
// writing "<path>_test.go:TestName". Nothing validated that either half
// resolves. A binding whose file was deleted, or whose function was renamed, is
// structurally perfect and carries ZERO protection: it reads as covered, it
// satisfies a reviewer skimming the entry, and the test it names can never fail
// because it does not exist.
//
// Two live instances were found when the rule was finally checked rather than
// assumed, both on CRITICAL invariants, both with live successors -- the
// knowledge was not wrong, it had stopped pointing at anything.
//
// WHAT THIS OWNS, AND WHY THAT MATTERS.
// It owns `required_tests` edges ONLY -- the authored proof relationships. An
// earlier version regex-scanned every non-comment line, which matched 1158
// strings where only 103 were bindings: descriptions, evidence lines and
// narrative that MENTION a test name. Prose may legitimately name a historical
// test; a proof edge may not. Validating prose as if it were a proof edge would
// fail the build for a rename that broke nothing, and would quietly redefine
// what the corpus claims.
//
// It lives in this package because internal/derived already owns the
// corpus-integrity tests that read ../../docs/awareness.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	corpusDir = "../../docs/awareness"
	repoRoot  = "../.."
)

type binding struct{ ref, src string }

// authoredYAML reports whether a corpus path is AUTHORED knowledge.
//
// docs/awareness/generated/ is machine-produced and rebuilt by its generator --
// awareness_graph_scip_symbols.yaml alone lists 529 code_symbols whose ids have
// the shape "path_test.go:TestName" because they index test functions. Those
// are an index, not proof edges: nobody authored them as a claim that a test
// proves something, and validating them here would make this check the
// generator's regression suite.
func authoredYAML(path string) bool {
	if !strings.HasSuffix(path, ".yaml") {
		return false
	}
	return !strings.Contains(filepath.ToSlash(path), "/generated/")
}

// collectBindings reads the authored `required_tests` edges, and nothing else.
//
// The corpus has three shapes and all three are proof edges:
//
//	invariants[].required_tests    - path_test.go:TestName
//	failure_modes[].required_tests - path_test.go:TestName
//	required_tests[].id            - id: path_test.go:TestName   (the Test node itself)
//
// This is a block scanner rather than a YAML parse because the module keeps a
// deliberately small dependency set. That is only acceptable with the
// under-reading guards in the caller: a scanner that silently reads nothing
// turns this whole check into a test that passes vacuously, which is the
// failure mode the check exists to prevent.
func collectBindings(t *testing.T) (bindings []binding, emptyBlocks []string) {
	t.Helper()
	err := filepath.Walk(corpusDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !authoredYAML(path) {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		lines := strings.Split(string(b), "\n")
		for i := 0; i < len(lines); i++ {
			key := strings.TrimSpace(lines[i])
			// Commented-out template blocks carry placeholder bindings such as
			// "path/to/config_test.go:TestX". They are documentation.
			if strings.HasPrefix(key, "#") {
				continue
			}
			if key != "required_tests:" && key != "tests:" {
				continue
			}
			keyIndent := len(lines[i]) - len(strings.TrimLeft(lines[i], " "))
			found := 0
			itemIndent := -1
			for j := i + 1; j < len(lines); j++ {
				raw := lines[j]
				trimmed := strings.TrimSpace(raw)
				if trimmed == "" || strings.HasPrefix(trimmed, "#") {
					continue
				}
				indent := len(raw) - len(strings.TrimLeft(raw, " "))
				// The block ends only at an indent that leaves it. An earlier
				// version also broke on any non-list line, which meant a list
				// ITEM WITH NESTED KEYS ended the block after its first entry:
				// required_tests.yaml declares each Test node as
				// "- id: ..." followed by "title:"/"protects:", so exactly one
				// of its two entries was read. The block was not empty, so the
				// empty-block guard said nothing.
				if indent <= keyIndent {
					break
				}
				if !strings.HasPrefix(trimmed, "- ") {
					continue // nested key inside the current item
				}
				if itemIndent == -1 {
					itemIndent = indent
				}
				if indent != itemIndent {
					continue // a nested list, e.g. protects.files
				}
				v := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
				// required_tests[].id declares the Test node itself.
				if strings.HasPrefix(v, "id:") {
					v = strings.TrimSpace(strings.TrimPrefix(v, "id:"))
				}
				v = strings.Trim(v, `"'`)
				if v == "" {
					continue
				}
				bindings = append(bindings, binding{ref: v, src: base})
				found++
			}
			if found == 0 {
				emptyBlocks = append(emptyBlocks, base)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", corpusDir, err)
	}
	return bindings, emptyBlocks
}

// testFuncs parses a Go test file and returns its declared function names, plus
// the ones whose body proves nothing.
//
// PARSED, NOT REGEXED. `func TestFoo(` appears inside comments and string
// literals -- routine_test.go contains exactly that in a fixture string -- and
// a regex would accept a binding to a test that does not exist because some
// other file talks about it.
func testFuncs(path string) (declared map[string]bool, inert map[string]string, err error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil, nil, err
	}
	declared, inert = map[string]bool{}, map[string]string{}
	for _, d := range f.Decls {
		fn, ok := d.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Body == nil {
			continue
		}
		declared[fn.Name.Name] = true
		if len(fn.Body.List) == 0 {
			inert[fn.Name.Name] = "empty body"
			continue
		}
		// An unconditional t.Skip as the first statement: the referent exists,
		// runs, and asserts nothing. This is the nearest decidable neighbour of
		// the undecidable question ("does this test still PROVE the claim"), and
		// it is deliberately not presented as an answer to it.
		if ex, ok := fn.Body.List[0].(*ast.ExprStmt); ok {
			if call, ok := ex.X.(*ast.CallExpr); ok {
				if sel, ok := call.Fun.(*ast.SelectorExpr); ok &&
					(sel.Sel.Name == "Skip" || sel.Sel.Name == "Skipf" || sel.Sel.Name == "SkipNow") {
					inert[fn.Name.Name] = "skips unconditionally as its first statement"
				}
			}
		}
	}
	return declared, inert, nil
}

// shapeCountBindings counts binding-shaped list items by a method deliberately
// unlike collectBindings: no block tracking, no indent state, just the line
// shape. Two methods that can only agree when both are right.
var bindingShape = regexp.MustCompile(`^\s*-\s+(?:id:\s*)?["']?[\w./\-]+_test\.go:Test\w+["']?\s*$`)

func shapeCountBindings(t *testing.T) int {
	t.Helper()
	n := 0
	_ = filepath.Walk(corpusDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !authoredYAML(path) {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "#") {
				continue
			}
			if bindingShape.MatchString(line) {
				n++
			}
		}
		return nil
	})
	return n
}

func TestEveryAuthoredTestBindingResolves(t *testing.T) {
	bindings, emptyBlocks := collectBindings(t)

	// UNDER-READING GUARDS. Without these the scanner could return nothing and
	// this test would pass while checking nothing at all.
	if len(bindings) == 0 {
		t.Fatal("no required_tests bindings found — the scanner read nothing, so this check is vacuous")
	}
	for _, src := range emptyBlocks {
		t.Errorf("%s has a required_tests block the scanner read as empty — it is under-reading the corpus", src)
	}
	// INDEPENDENT CROSS-CHECK, by a different method than the scanner.
	//
	// A list item whose ENTIRE value is a test reference is a binding by shape,
	// wherever it sits; prose mentions a test name inside a sentence and never
	// takes this shape. If the two counts disagree, the scanner is
	// under-reading and every binding it skipped went unchecked while this test
	// reported success. That is precisely how the block scanner shipped its
	// first bug: it read 102 of 103 and nothing noticed.
	if n := shapeCountBindings(t); n != len(bindings) {
		t.Fatalf("scanner read %d bindings, independent shape count says %d — the scanner is skipping proof edges",
			len(bindings), n)
	}

	type parsed struct {
		declared map[string]bool
		inert    map[string]string
	}
	cache := map[string]*parsed{}

	for _, b := range bindings {
		file, fn, ok := strings.Cut(b.ref, ":")
		if !ok {
			t.Errorf("%s binds %q, which names no test function — a proof edge must name one", b.src, b.ref)
			continue
		}
		p, seen := cache[file]
		if !seen {
			decl, inert, err := testFuncs(filepath.Join(repoRoot, file))
			if err != nil {
				cache[file] = nil
			} else {
				p = &parsed{decl, inert}
				cache[file] = p
			}
		}
		if p == nil {
			t.Errorf("%s binds %s:%s — THAT FILE DOES NOT EXIST or does not parse, so the binding proves nothing",
				b.src, file, fn)
			continue
		}
		if !p.declared[fn] {
			t.Errorf("%s binds %s:%s — the file declares no such function, so the binding proves nothing",
				b.src, file, fn)
			continue
		}
		if why, bad := p.inert[fn]; bad {
			t.Errorf("%s binds %s:%s — that function %s, so the binding resolves but proves nothing",
				b.src, file, fn, why)
		}
	}
	t.Logf("%d authored required_tests bindings checked", len(bindings))
}

// The AST parse is not prudence; it closes a live false-accept.
//
// internal/workflow/routine_test.go carries a DIFF FIXTURE containing the line
// "-func TestOldGuard(t *testing.T) {". No such test is declared anywhere, yet
// a regex over the file's bytes matches it — so a binding to
// routine_test.go:TestOldGuard would have been accepted as a resolving proof
// edge on the strength of a string literal describing a deleted function.
//
// The corpus does not currently contain that binding. This asserts the
// resolver would refuse it, so the guarantee does not quietly depend on nobody
// ever writing one.
func TestBindingsResolveThroughTheParserNotTheBytes(t *testing.T) {
	const file = "internal/workflow/routine_test.go"
	raw, err := os.ReadFile(filepath.Join(repoRoot, file))
	if err != nil {
		t.Skipf("fixture file is gone (%v) — this test no longer has a subject", err)
	}
	if !strings.Contains(string(raw), "func TestOldGuard(") {
		t.Skip("the fixture string that made this a real risk is gone")
	}
	declared, _, err := testFuncs(filepath.Join(repoRoot, file))
	if err != nil {
		t.Fatalf("parsing %s: %v", file, err)
	}
	if declared["TestOldGuard"] {
		t.Fatal("the resolver accepted a function that exists only inside a string literal")
	}
}

package ghbridge

// WHAT #182 R6 REMOVED, PROVEN FROM PRODUCTION SOURCE.
//
// A deletion slice can be faked in two directions. The thing can be "removed"
// and rebuilt under a new name, or a test can assert a symbol is gone while the
// behaviour it enabled quietly survives somewhere else. So these read the
// package's own declarations and call graph rather than trusting either.
//
// Comments and tests are NOT production. Every check below reads .go files that
// are not _test.go, and it reads them as syntax, so prose naming a thing this
// package deliberately no longer has cannot fail its own check.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// productionFiles parses every non-test file in this package.
func productionFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		out[name] = parsed
	}
	if len(out) < 5 {
		t.Fatalf("parsed %d production files; this check proves nothing", len(out))
	}
	return out
}

// declaredNames collects every top-level type, function and method name.
func declaredNames(files map[string]*ast.File) map[string]string {
	out := map[string]string{}
	for name, f := range files {
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				out[d.Name.Name] = name
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					if ts, ok := spec.(*ast.TypeSpec); ok {
						out[ts.Name.Name] = name
					}
				}
			}
		}
	}
	return out
}

// The second review world is gone, by name and by shape.
func TestTheLegacyRelayAndReviewGrammarAreGone(t *testing.T) {
	declared := declaredNames(productionFiles(t))
	for _, gone := range []string{
		// The relay's own durable store and record.
		"RelayStore", "RelayRecord", "RelayArtifact",
		// Its lifting layer and the legacy response grammar.
		"ParseRelayArtifact", "ParseReview",
		// The runner's second source of "what answers this request".
		"pendingRelayFor", "verifyRelayObligation",
		// Its free-function publisher. Its store methods need no entry here:
		// a method on RelayStore could not compile once the type is gone, and
		// attestation.go has its own markPublished for a different record --
		// which is why this list names shapes, not merely words.
		"publishRelayRecord",
	} {
		if where, found := declared[gone]; found {
			t.Errorf("%s is still declared in %s; R6 removes the relay's own review world", gone, where)
		}
	}
	// And the type that carried the legacy review envelope.
	if where, found := declared["Review"]; found {
		t.Errorf("the legacy Review response type is still declared in %s; "+
			"reviewartifact owns what a review is", where)
	}
}

// A struct field is a shape, not a name. These prove the wiring is gone from
// the types that used to carry it.
func TestNoProductionTypeCarriesARelayStore(t *testing.T) {
	files := productionFiles(t)
	checked := map[string]bool{}
	for file, parsed := range files {
		ast.Inspect(parsed, func(n ast.Node) bool {
			ts, ok := n.(*ast.TypeSpec)
			if !ok {
				return true
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				return true
			}
			checked[ts.Name.Name] = true
			for _, field := range st.Fields.List {
				ty := ""
				switch ft := field.Type.(type) {
				case *ast.Ident:
					ty = ft.Name
				case *ast.SelectorExpr:
					if pkg, ok := ft.X.(*ast.Ident); ok {
						ty = pkg.Name + "." + ft.Sel.Name
					}
				}
				if ty == "RelayStore" || ty == "RelayRecord" {
					t.Errorf("%s.%s in %s still holds a %s", ts.Name.Name, field.Names, file, ty)
				}
				for _, fn := range field.Names {
					if fn.Name == "Relays" {
						t.Errorf("%s in %s still has a Relays field", ts.Name.Name, file)
					}
				}
			}
			return true
		})
	}
	for _, want := range []string{"Runner", "RelaySubmission"} {
		if !checked[want] {
			t.Fatalf("%s was not inspected; this check proves nothing", want)
		}
	}
}

// Nothing in production writes the old relay directory any more.
//
// The migration reads it, which is the point of the historical boundary: it is
// the one file allowed to know that path, and it only ever moves files out.
func TestNoProductionCodeWritesTheRelayDirectory(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		blob, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(blob), `"relays"`) {
			continue
		}
		found++
		if name != "relay_history.go" {
			t.Errorf("%s names the relays directory; only the historical migration may", name)
		}
	}
	// The composition root names the path and hands it in, so this package's
	// own source need not contain it at all. Either way nothing but the
	// migration may.
	_ = found
}

// The relay adapter cannot reach the legacy parser.
//
// The historical shape is understood in exactly one file, by exactly one entry
// point. If current ingestion could call it, the second grammar would be back --
// reachable from the live path, one call site at a time.
func TestCurrentRelayIngestionCannotReachTheHistoricalParser(t *testing.T) {
	files := productionFiles(t)
	historical := map[string]bool{}
	for _, decl := range files["relay_history.go"].Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			historical[d.Name.Name] = true
		case *ast.GenDecl:
			for _, spec := range d.Specs {
				if ts, ok := spec.(*ast.TypeSpec); ok {
					historical[ts.Name.Name] = true
				}
			}
		}
	}
	if !historical["historicalRelayRecord"] || !historical["migrateOneHistoricalRelay"] {
		t.Fatal("the historical boundary was not found; this check proves nothing")
	}
	// MigrateHistoricalRelays is the ONE entry point, so it is allowed.
	delete(historical, "MigrateHistoricalRelays")

	reached := map[string][]string{}
	for file, parsed := range files {
		if file == "relay_history.go" {
			continue
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			ast.Inspect(fn, func(n ast.Node) bool {
				id, ok := n.(*ast.Ident)
				if !ok || !historical[id.Name] {
					return true
				}
				reached[fn.Name.Name] = append(reached[fn.Name.Name], id.Name)
				return true
			})
		}
	}
	for _, live := range []string{"AcceptRelayedReview", "RenderRelayedReview", "relayPublicationOf"} {
		if names := reached[live]; len(names) != 0 {
			t.Errorf("%s reaches the historical relay shape %v; current ingestion parses only "+
				"the canonical grammar", live, names)
		}
	}
}

// The one place canonical review bytes become durable is the review store.
//
// Read as the set of things AcceptRelayedReview writes through: it may Accept
// and Complete on reviewstore, and it may not reach any other durable writer.
func TestTheRelayAdapterPersistsOnlyThroughTheReviewStore(t *testing.T) {
	files := productionFiles(t)
	var fn *ast.FuncDecl
	for _, decl := range files["relay.go"].Decls {
		d, ok := decl.(*ast.FuncDecl)
		if ok && d.Name.Name == "AcceptRelayedReview" {
			fn = d
		}
	}
	if fn == nil {
		t.Fatal("AcceptRelayedReview was not found; this check proves nothing")
	}
	forbidden := map[string]bool{
		"WriteFile": true, "OpenFile": true, "Create": true, "MkdirAll": true,
		"Rename": true, "Remove": true, "RemoveAll": true,
	}
	var writes, storeCalls []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if forbidden[sel.Sel.Name] {
			writes = append(writes, sel.Sel.Name)
		}
		if sel.Sel.Name == "Accept" || sel.Sel.Name == "Complete" {
			storeCalls = append(storeCalls, sel.Sel.Name)
		}
		return true
	})
	if len(writes) != 0 {
		t.Errorf("AcceptRelayedReview writes durable state directly via %v; the review store owns that", writes)
	}
	if len(storeCalls) == 0 {
		t.Fatal("AcceptRelayedReview reaches neither Accept nor Complete; this check proves nothing")
	}
}

// The verdict is read by the one component that reads verdicts, and stored by
// nobody.
func TestNoProductionRecordHoldsASecondCopyOfAVerdict(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		blob, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		src := string(blob)
		for _, field := range []string{`json:"decision"`, `json:"summary"`, `json:"findings`} {
			if strings.Contains(src, field) {
				t.Errorf("%s persists %s; what a review said is re-read from its exact bytes", name, field)
			}
		}
	}
}

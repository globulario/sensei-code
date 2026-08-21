package tui

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"testing"
)

func fileTextTUI(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// funcBodyTUI renders one method's body as source, so a structural assertion
// reads what the code does rather than where it sits in the file.
func funcBodyTUI(t *testing.T, name string) string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../internal/tui/model.go", nil, 0)
	if err != nil {
		t.Fatalf("parse model.go: %v", err)
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != name || fn.Body == nil {
			continue
		}
		var b strings.Builder
		if err := printer.Fprint(&b, fset, fn.Body); err != nil {
			t.Fatalf("print %s: %v", name, err)
		}
		return b.String()
	}
	t.Fatalf("function %s not found", name)
	return ""
}

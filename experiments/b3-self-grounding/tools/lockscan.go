// lockscan enumerates Family 1 subjects mechanically: for each given file, in
// order, every struct type (declaration order) holding a field whose type is
// sync.Mutex, sync.RWMutex, *sync.Mutex or *sync.RWMutex, paired with every
// OTHER field of that struct (field order). Output: one JSON line per
// (dir, type, field, lock). No judgement is made here.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
)

func isLock(t ast.Expr) bool {
	if st, ok := t.(*ast.StarExpr); ok {
		t = st.X
	}
	sel, ok := t.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "sync" && (sel.Sel.Name == "Mutex" || sel.Sel.Name == "RWMutex")
}

func main() {
	fset := token.NewFileSet()
	enc := json.NewEncoder(os.Stdout)
	for _, file := range os.Args[1:] {
		f, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.TYPE {
				continue
			}
			for _, sp := range gd.Specs {
				ts := sp.(*ast.TypeSpec)
				st, ok := ts.Type.(*ast.StructType)
				if !ok {
					continue
				}
				var locks, fields []string
				for _, fld := range st.Fields.List {
					for _, n := range fld.Names {
						if isLock(fld.Type) {
							locks = append(locks, n.Name)
						} else {
							fields = append(fields, n.Name)
						}
					}
				}
				for _, l := range locks {
					for _, fl := range fields {
						enc.Encode(map[string]string{"file": file, "dir": path.Dir(file), "type": ts.Name.Name, "field": fl, "lock": l})
					}
				}
			}
		}
	}
}

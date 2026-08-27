// mutscan is the sealed hand-derivation tool for mutation-v1: it evaluates
// state_mutated_only_by_owner(T.F) over one module and prints one JSON line
// per subject, in stable order. It is the selection predicate, frozen before
// any fixture is scanned; its sha256 is recorded in selection.json.
package main

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"
)

type site struct {
	Pos      string `json:"pos"`
	Package  string `json:"package"`
	Kind     string `json:"kind"` // assign | opassign | incdec | address
	InMethod bool   `json:"in_owner_method"`
	InOwner  bool   `json:"in_owner_package"`
}

// key identifies one subject (T.F).
type key struct {
	t *types.Named
	f *types.Var
}

type subject struct {
	File       string   `json:"file"`
	Type       string   `json:"type"`
	Field      string   `json:"field"`
	Owner      string   `json:"owner"`
	Relation   string   `json:"relation"`
	Result     string   `json:"result"`
	Detail     string   `json:"detail"`
	Mutations  int      `json:"mutations"`
	Sites      []site   `json:"sites,omitempty"`
	Unresolved []string `json:"unresolved,omitempty"`
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: mutscan <module dir>")
		os.Exit(2)
	}
	dir, err := filepath.Abs(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "abs:", err)
		os.Exit(2)
	}
	cfg := &packages.Config{Mode: packages.LoadAllSyntax, Dir: dir, Tests: false}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		fmt.Fprintln(os.Stderr, "load:", err)
		os.Exit(1)
	}
	var loadErrs []string
	for _, p := range pkgs {
		for _, e := range p.Errors {
			loadErrs = append(loadErrs, e.Error())
		}
	}
	// Subjects: exported struct types with exported fields, in stable order.
	var subjects []key
	fileOf := map[*types.Named]string{}
	var files []string
	seenFile := map[string]bool{}
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			name := p.Fset.File(f.Pos()).Name()
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			rel := strings.TrimPrefix(strings.TrimPrefix(name, dir), "/")
			if !seenFile[rel] {
				seenFile[rel] = true
				files = append(files, rel)
			}
		}
	}
	sort.Strings(files)
	// Walk files in stable order collecting declarations in order.
	for _, rel := range files {
		for _, p := range pkgs {
			for _, f := range p.Syntax {
				name := p.Fset.File(f.Pos()).Name()
				if strings.TrimPrefix(strings.TrimPrefix(name, dir), "/") != rel {
					continue
				}
				for _, d := range f.Decls {
					gd, ok := d.(*ast.GenDecl)
					if !ok || gd.Tok != token.TYPE {
						continue
					}
					for _, sp := range gd.Specs {
						ts := sp.(*ast.TypeSpec)
						if !ts.Name.IsExported() {
							continue
						}
						obj, ok := p.TypesInfo.Defs[ts.Name].(*types.TypeName)
						if !ok {
							continue
						}
						named, ok := obj.Type().(*types.Named)
						if !ok {
							continue
						}
						st, ok := named.Underlying().(*types.Struct)
						if !ok {
							continue
						}
						fileOf[named] = rel
						for i := 0; i < st.NumFields(); i++ {
							fv := st.Field(i)
							if fv.Exported() && !fv.Embedded() {
								subjects = append(subjects, key{named, fv})
							}
						}
					}
				}
			}
		}
	}
	// Mutation sites across all packages.
	sitesOf := map[key][]site{}
	unresolvedOf := map[key][]string{}
	for _, p := range pkgs {
		for _, f := range p.Syntax {
			name := p.Fset.File(f.Pos()).Name()
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			var enclosing *ast.FuncDecl
			ast.Inspect(f, func(n ast.Node) bool {
				switch x := n.(type) {
				case *ast.FuncDecl:
					enclosing = x
				case *ast.AssignStmt:
					kind := "assign"
					if x.Tok != token.ASSIGN && x.Tok != token.DEFINE {
						kind = "opassign"
					}
					for _, lhs := range x.Lhs {
						record(p, lhs, kind, enclosing, sitesOf, unresolvedOf, subjects)
					}
				case *ast.IncDecStmt:
					record(p, x.X, "incdec", enclosing, sitesOf, unresolvedOf, subjects)
				case *ast.UnaryExpr:
					if x.Op == token.AND {
						record(p, x.X, "address", enclosing, sitesOf, unresolvedOf, subjects)
					}
				}
				return true
			})
		}
	}
	enc := json.NewEncoder(os.Stdout)
	for _, k := range subjects {
		s := subject{File: fileOf[k.t], Type: k.t.Obj().Name(), Field: k.f.Name(), Owner: k.t.Obj().Pkg().Path(),
			Relation: fmt.Sprintf("state_mutated_only_by_owner(%s.%s in %s)", k.t.Obj().Name(), k.f.Name(), k.t.Obj().Pkg().Path()),
			Sites:    sitesOf[k], Unresolved: unresolvedOf[k], Mutations: len(sitesOf[k])}
		switch {
		case len(loadErrs) != 0:
			s.Result, s.Detail = "UNRESOLVED", "package load errors: "+strings.Join(loadErrs, "; ")
		case len(s.Unresolved) != 0:
			s.Result, s.Detail = "UNRESOLVED", strings.Join(s.Unresolved, "; ")
		case len(s.Sites) == 0:
			s.Result, s.Detail = "DERIVED", "no mutation site in the repository (true and useless: not a subject)"
		default:
			out := 0
			for _, st := range s.Sites {
				if !st.InMethod {
					out++
				}
			}
			if out == 0 {
				s.Result, s.Detail = "DERIVED", fmt.Sprintf("all %d mutation site(s) lie inside methods of %s", len(s.Sites), s.Type)
			} else {
				s.Result, s.Detail = "REFUTED", fmt.Sprintf("%d of %d mutation site(s) lie outside methods of %s", out, len(s.Sites), s.Type)
			}
		}
		enc.Encode(s)
	}
}

// record classifies one written expression. Only selector expressions whose
// static receiver type resolves to a subject T count; anything the reader
// cannot resolve (interface, unsafe, reflect) is UNRESOLVED for that subject
// when it can tell which subject it concerns, and ignored when it cannot.
func record(p *packages.Package, e ast.Expr, kind string, enclosing *ast.FuncDecl, sites map[key][]site, unresolved map[key][]string, subjects []key) {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return
	}
	selection, ok := p.TypesInfo.Selections[sel]
	if !ok || selection.Kind() != types.FieldVal {
		return
	}
	fv, ok := selection.Obj().(*types.Var)
	if !ok {
		return
	}
	recv := selection.Recv()
	if ptr, ok := recv.(*types.Pointer); ok {
		recv = ptr.Elem()
	}
	named, ok := recv.(*types.Named)
	if !ok {
		return
	}
	for _, k := range subjects {
		if k.t.Obj() != named.Obj() || k.f != fv {
			continue
		}
		inMethod := false
		if enclosing != nil && enclosing.Recv != nil && len(enclosing.Recv.List) == 1 {
			rt := p.TypesInfo.TypeOf(enclosing.Recv.List[0].Type)
			if ptr, ok := rt.(*types.Pointer); ok {
				rt = ptr.Elem()
			}
			if rn, ok := rt.(*types.Named); ok && rn.Obj() == named.Obj() {
				inMethod = true
			}
		}
		inOwner := p.PkgPath == named.Obj().Pkg().Path()
		st := site{Pos: p.Fset.Position(sel.Pos()).String(), Package: p.PkgPath, Kind: kind, InMethod: inMethod, InOwner: inOwner}
		if kind == "address" && !inMethod {
			// A field address escaping outside the authority surface is a
			// write authority the reader cannot follow.
			unresolved[k] = append(unresolved[k], "address of "+named.Obj().Name()+"."+fv.Name()+" taken outside its methods at "+st.Pos)
		}
		sites[k] = append(sites[k], st)
	}
}

package main

// A command a user cannot discover is not a public command.
//
// `observe` and `audit-repair` shipped in the dispatch switch and never
// appeared in help. They are the two newest capabilities and the ones the proof
// campaign depends on, so the tool's most interesting behaviour was reachable
// only by reading the source. `setup`, `mcp` and `routine-scan` were missing the
// same way.
//
// The test below is deliberately NOT a comparison of the table against itself.
// It parses the real dispatch switch out of main.go, so a command added to the
// switch and forgotten in the table fails here rather than shipping invisible.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// dispatchedCommands are the literal case values of main's command switch.
func dispatchedCommands(t *testing.T) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	out := map[string]bool{}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		sw, ok := n.(*ast.SwitchStmt)
		if !ok || sw.Tag == nil {
			return true
		}
		// The command switch is the one over os.Args[1].
		idx, ok := sw.Tag.(*ast.IndexExpr)
		if !ok {
			return true
		}
		sel, ok := idx.X.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Args" {
			return true
		}
		found = true
		for _, stmt := range sw.Body.List {
			clause, ok := stmt.(*ast.CaseClause)
			if !ok {
				continue // default:
			}
			for _, expr := range clause.List {
				lit, ok := expr.(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if v, err := strconv.Unquote(lit.Value); err == nil {
					out[v] = true
				}
			}
		}
		return true
	})
	if !found {
		t.Fatal("the os.Args command switch is gone from main.go; this pin needs rewriting " +
			"against whatever replaced it, not deleting")
	}
	return out
}

// Every command this binary dispatches is accounted for: advertised, aliased,
// or explicitly classified as hidden.
func TestEveryDispatchedCommandIsAccountedFor(t *testing.T) {
	dispatched := dispatchedCommands(t)
	public := publicCommandNames()

	var invisible []string
	for name := range dispatched {
		switch {
		case public[name]:
		case commandAliases[name] != "":
		case hiddenCommands[name] != "":
		default:
			invisible = append(invisible, name)
		}
	}
	if len(invisible) != 0 {
		t.Errorf("dispatched but undiscoverable: %v\n"+
			"A command a user cannot find in `sensei-code help` is not a public command, whatever "+
			"the switch says. Add it to publicCommands, or classify it in hiddenCommands with a "+
			"reason it is internal.", invisible)
	}

	// And the other direction: help must not advertise a command that does not
	// dispatch. Help promising something the binary does not do is the same
	// defect pointing the other way.
	for name := range public {
		if !dispatched[name] {
			t.Errorf("help advertises %q, which the command switch does not handle", name)
		}
	}

	// An alias must point at something real.
	for alias, target := range commandAliases {
		if !dispatched[alias] {
			t.Errorf("alias %q is declared but never dispatched", alias)
		}
		if !public[target] && target != "help" {
			t.Errorf("alias %q points at %q, which is not advertised", alias, target)
		}
	}
}

// The commands the proof campaign drives must be discoverable.
//
// Named individually rather than left to the general rule above, because these
// three are the campaign's instrument: if the benchmark exercises a command a
// user cannot find, it is measuring something the product does not really
// offer.
func TestTheCampaignCommandsAreDiscoverable(t *testing.T) {
	help := renderCommands()
	for _, name := range []string{"run", "observe", "audit-repair"} {
		if !strings.Contains(help, "sensei-code "+name) {
			t.Errorf("%q is not discoverable from ordinary help; the benchmark would be exercising "+
				"a capability a user cannot find", name)
		}
	}
}

// Every advertised command says what it does, once.
func TestTheCommandTableIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, c := range publicCommands {
		if seen[c.Name] {
			t.Errorf("%q is listed twice", c.Name)
		}
		seen[c.Name] = true
		if strings.TrimSpace(c.Summary) == "" {
			t.Errorf("%q is advertised with no summary", c.Name)
		}
	}
	// printUsage must render from the table rather than from a second hardcoded
	// list, or the two drift and only one of them is true.
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	at := strings.Index(string(src), "func printUsage()")
	if at < 0 {
		t.Fatal("printUsage is gone")
	}
	body := string(src)[at:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "renderCommands()") {
		t.Error("printUsage no longer renders from publicCommands; a second hardcoded list is how " +
			"observe and audit-repair went missing in the first place")
	}
	for _, c := range publicCommands {
		if strings.Contains(body, "sensei-code "+c.Name+" ") && c.Name != "run" && c.Name != "observe" &&
			c.Name != "audit-repair" {
			t.Errorf("printUsage hardcodes %q outside the table", c.Name)
		}
	}
}

// The rendezvous exists and nothing reaches it unless the server is installed
// as the engine's resolver. Without this the remote role holder registers,
// inspects, and is asked nothing -- a slice that passes its own unit tests and
// does nothing in the product.
func TestTheControlCommandInstallsTheServerAsTheEnginesResolver(t *testing.T) {
	source, err := os.ReadFile("control.go")
	if err != nil {
		t.Fatalf("read control.go: %v", err)
	}
	if !strings.Contains(string(source), "engine.Runners = server") {
		t.Fatal("sensei-code control builds an engine and a server and never connects them")
	}
}

package control

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These are structural rather than example tests because what they check is the
// ABSENCE of a capability, and absence cannot be demonstrated by calling
// something. A test that "submit_review returns an error" proves one path is
// closed; a test that no submission path exists proves there is nothing to
// close.
//
// They are also where the next slice's constraints are enforced early. PR 4
// introduces a rendezvous runner, and the rules it must not break are easier to
// keep when breaking them fails a test that is already there.

// controlFiles parses this package plus the command that starts it.
func controlFiles(t *testing.T) map[string]*ast.File {
	t.Helper()
	fset := token.NewFileSet()
	out := map[string]*ast.File{}
	for _, dir := range []string{".", filepath.Join("..", "..", "cmd", "sensei-code")} {
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("read %s: %v", dir, err)
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			// Only the control command from cmd; the rest of the binary is not
			// the remote surface.
			if dir != "." && name != "control.go" {
				continue
			}
			path := filepath.Join(dir, name)
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", path, err)
			}
			out[path] = file
		}
	}
	if len(out) < 2 {
		t.Fatalf("the tripwire found %d files to check; it is not looking where it thinks it is", len(out))
	}
	return out
}

// A remote transport may authenticate a principal. It cannot establish that the
// remote model's conversation was fresh or context-isolated, so it must never
// mint the evidence that says it was.
func TestTheRemoteSurfaceCannotObtainAnIndependenceProof(t *testing.T) {
	forbidden := map[string]string{
		"principal.Prove": "only a session Sensei Code launched and observed can prove isolation, " +
			"and a remote rendezvous is neither",
		"principal.QualifyProven": "a proven qualification requires a proof this surface must never hold",
	}
	for path, file := range controlFiles(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if name := selectorName(call.Fun); name != "" {
				if why, bad := forbidden[name]; bad {
					t.Fatalf("%s calls %s: %s", path, name, why)
				}
			}
			return true
		})
	}
}

// roles.Provenance carries SessionMode, and SessionMode means something exactly
// when the party that wrote it watched the turn. This surface watches nothing.
func TestTheRemoteSurfaceStampsNoSessionProvenance(t *testing.T) {
	for path, file := range controlFiles(t) {
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			if selectorName(lit.Type) == "roles.Provenance" {
				t.Fatalf("%s constructs a roles.Provenance; a remote response has no observed session to describe", path)
			}
			return true
		})
		ast.Inspect(file, func(n ast.Node) bool {
			if selectorName(n) == "roles.Fresh" {
				t.Fatalf("%s names roles.Fresh; this surface cannot establish that any remote context was fresh", path)
			}
			return true
		})
	}
}

// The boundary moved deliberately in this slice: a remote runner now DOES
// implement agent.Runner, because a remote answer has to reach the same
// workflow path a local one does. What must remain impossible is that the
// runner executes anything.
//
// The tripwire is evolved rather than deleted. It was "nothing here can serve a
// turn"; it is now "serving a turn is all it can do".
func TestTheRemoteRunnerAnswersAndExecutesNothing(t *testing.T) {
	// Banned everywhere, including the command that starts the surface: nothing
	// in this direction may cause a process to run or hold a capability
	// envelope.
	everywhere := map[string]string{
		"github.com/globulario/sensei-code/internal/broker":   "the capability envelope is not this surface's to hold",
		"github.com/globulario/sensei-code/internal/processx": "this surface runs no process",
		"os/exec": "a remote role holder must not be able to cause a command to run",
	}
	// Banned in the package a remote client actually reaches. The command is
	// excluded because it legitimately builds the Engine, which needs the
	// repository -- that is the operator starting a server, not a remote role
	// touching a checkout.
	inPackage := map[string]string{
		"github.com/globulario/sensei-code/internal/gitx":      "a remote role does not touch the repository",
		"github.com/globulario/sensei-code/internal/candidate": "a remote role does not mutate a candidate",
		"github.com/globulario/sensei-code/internal/admission": "admission is Sensei's, not this surface's",
		"github.com/globulario/sensei-code/internal/publish":   "publication is human-owned",
	}
	for path, file := range controlFiles(t) {
		for _, imp := range file.Imports {
			p := strings.Trim(imp.Path.Value, `"`)
			if why, bad := everywhere[p]; bad {
				t.Fatalf("%s imports %s: %s", path, p, why)
			}
			if why, bad := inPackage[p]; bad && !strings.Contains(path, "cmd") {
				t.Fatalf("%s imports %s: %s", path, p, why)
			}
		}
	}

	// Exactly one Run method, on the remote runner. A second one would be a
	// second thing the workflow can call, arrived at without anyone deciding.
	var receivers []string
	for path, file := range controlFiles(t) {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "Run" {
				continue
			}
			receivers = append(receivers, path+" "+receiverName(fn))
		}
	}
	if len(receivers) != 1 || !strings.HasSuffix(receivers[0], "remoteRunner") {
		t.Fatalf("agent.Runner is implemented by %v; exactly one remote runner may serve a turn", receivers)
	}
}

func receiverName(fn *ast.FuncDecl) string {
	if fn.Recv == nil || len(fn.Recv.List) == 0 {
		return ""
	}
	switch t := fn.Recv.List[0].Type.(type) {
	case *ast.StarExpr:
		if id, ok := t.X.(*ast.Ident); ok {
			return id.Name
		}
	case *ast.Ident:
		return t.Name
	}
	return ""
}

// The surface a remote client can reach is the five read verbs plus exactly two
// typed submissions. Anything else appearing here is the boundary moving
// without anyone deciding to move it.
//
// start_task is on the list deliberately and is not an oversight to be filled
// in later. An architect lease is architectural authority, not human objective
// authority: holding one must never mean this principal may invent development
// work and cause workers to execute it.
func TestOnlyTheTwoTypedSubmissionVerbsExist(t *testing.T) {
	source := readSource(t)
	for _, verb := range []string{
		"advance_task", "admit_change", "complete_task", "merge", "commit",
		"run_shell", "write_file", "invoke_worker", "run_worker", "exec", "start_task", "delegate_task",
	} {
		// The strings appear in principal's capability vocabulary and in test
		// expectations; what must not appear is a TOOL by that name, which is
		// how the dispatch switch and the descriptor list would name it.
		for _, shape := range []string{`case "` + verb + `"`, `"name": "` + verb + `"`} {
			if strings.Contains(source, shape) {
				t.Fatalf("the read-only surface has a %s tool (%s)", verb, shape)
			}
		}
	}
}

func readSource(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		b.Write(raw)
	}
	return b.String()
}

// selectorName renders pkg.Name for a selector expression, or "" for anything
// else.
func selectorName(n ast.Node) string {
	sel, ok := n.(*ast.SelectorExpr)
	if !ok {
		return ""
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return ""
	}
	return pkg.Name + "." + sel.Sel.Name
}

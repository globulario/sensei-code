package workflow

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/roles"
)

type stubResolver struct {
	seen     []RunnerSpec
	resolved Resolved
	err      error
}

func (s *stubResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	s.seen = append(s.seen, spec)
	return s.resolved, s.err
}

type stubRunner struct{ ran int }

func (r *stubRunner) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	r.ran++
	return agent.Result{}, nil
}

func specFor(role roles.Role) RunnerSpec {
	return RunnerSpec{
		Role:   role,
		Agent:  config.Agent{Name: "claude", Command: "claude", Args: []string{"-p"}},
		Source: event.SourceArchitect,
		TaskID: "task-1",
	}
}

// The default is the provider's own command line, built exactly as the four
// call sites built it inline before the seam existed.
func TestWithNoResolverTheAdapterIsStillTheProviderCommandLine(t *testing.T) {
	e := &Engine{SessionID: "sess-1"}
	got, err := e.resolveRunner(RunnerSpec{
		Role:   roles.Implementer,
		Agent:  config.Agent{Name: "claude", Command: "claude", Args: []string{"-p", "--yes"}},
		Source: event.SourceClaude,
		TaskID: "task-1",
		Env:    []string{"GIT_DIR=/nope"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	cli, ok := got.Runner.(agent.CLI)
	if !ok {
		t.Fatalf("default adapter is %T, want agent.CLI", got.Runner)
	}
	if cli.Name != "claude" || cli.Command != "claude" || strings.Join(cli.Args, " ") != "-p --yes" {
		t.Fatalf("argv changed: %+v", cli)
	}
	if cli.Label != config.DisplayName("claude") || got.Label != config.DisplayName("claude") {
		t.Fatalf("label changed: %q / %q", cli.Label, got.Label)
	}
	if cli.Source != event.SourceClaude || cli.SessionID != "sess-1" {
		t.Fatalf("attribution changed: source %q session %q", cli.Source, cli.SessionID)
	}
	if strings.Join(cli.Env, " ") != "GIT_DIR=/nope" {
		t.Fatalf("capability env changed: %v", cli.Env)
	}
	if strings.Join(cli.UnsetEnv, " ") != strings.Join(provider.SessionOnlyEnv, " ") {
		t.Fatalf("the provider no longer authenticates with its own stored session: %v", cli.UnsetEnv)
	}
	if cli.NoGraph {
		t.Fatal("a provider that consumes the graph was launched unbound")
	}
}

func TestAProviderDeclaringNoGraphIsStillLaunchedUnbound(t *testing.T) {
	e := &Engine{SessionID: "sess-1"}
	got, err := e.resolveRunner(RunnerSpec{
		Role:  roles.Reviewer,
		Agent: config.Agent{Name: "stub", Command: "stub", Graph: "none"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if !got.Runner.(agent.CLI).NoGraph {
		t.Fatal("a provider declaring graph: none was bound to a graph")
	}
}

func TestAConfiguredResolverIsAskedAndItsAnswerIsUsed(t *testing.T) {
	runner := &stubRunner{}
	resolver := &stubResolver{resolved: Resolved{Runner: runner, Name: "remote-a", Label: "Remote A"}}
	e := &Engine{SessionID: "sess-1", Runners: resolver}

	got, err := e.resolveRunner(specFor(roles.Architect))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Runner != agent.Runner(runner) {
		t.Fatalf("the resolver's adapter was not used, got %T", got.Runner)
	}
	if got.Name != "remote-a" || got.Label != "Remote A" {
		t.Fatalf("the answering party was renamed: %q / %q", got.Name, got.Label)
	}
	if len(resolver.seen) != 1 || resolver.seen[0].Role != roles.Architect || resolver.seen[0].TaskID != "task-1" {
		t.Fatalf("the resolver was not told which role on which task: %+v", resolver.seen)
	}
}

// The property the seam exists to protect. A run where a delegated architect
// quietly became the local one would produce a plan attributed to a party that
// did not write it, and nothing in the record would show the substitution.
func TestARefusingResolverIsNeverRecoveredFromByBuildingTheCommandLine(t *testing.T) {
	refused := errors.New("the remote architect is not holding the role")
	e := &Engine{SessionID: "sess-1", Runners: &stubResolver{err: refused}}

	got, err := e.resolveRunner(specFor(roles.Architect))
	if err == nil {
		t.Fatalf("a refusal produced an adapter: %T", got.Runner)
	}
	if !errors.Is(err, refused) {
		t.Fatalf("the refusal's cause was discarded: %v", err)
	}
	if got.Runner != nil {
		t.Fatalf("a refusal still returned an adapter: %T", got.Runner)
	}
}

func TestAnEmptyAnswerFromAResolverIsARefusal(t *testing.T) {
	for name, resolved := range map[string]Resolved{
		"no adapter":      {Name: "remote-a"},
		"unnamed adapter": {Runner: &stubRunner{}},
	} {
		e := &Engine{SessionID: "sess-1", Runners: &stubResolver{resolved: resolved}}
		if got, err := e.resolveRunner(specFor(roles.Reviewer)); err == nil {
			t.Fatalf("%s was accepted, adapter %T", name, got.Runner)
		}
	}
}

func TestAnUnnamedAdapterKeepsItsNameAsItsLabel(t *testing.T) {
	e := &Engine{SessionID: "sess-1", Runners: &stubResolver{
		resolved: Resolved{Runner: &stubRunner{}, Name: "remote-a"}}}
	got, err := e.resolveRunner(specFor(roles.Reviewer))
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got.Label != "remote-a" {
		t.Fatalf("label is %q", got.Label)
	}
}

func TestAnUnknownRoleResolvesToNothing(t *testing.T) {
	e := &Engine{SessionID: "sess-1"}
	if _, err := e.resolveRunner(RunnerSpec{Role: roles.Role("auditor"),
		Agent: config.Agent{Name: "claude", Command: "claude"}}); err == nil {
		t.Fatal("an adapter was resolved for a role this project does not have")
	}
}

// The seam rots the moment somebody builds an adapter beside it. runners.go is
// the only place an agent.CLI may be constructed, so a new call site cannot
// reintroduce a path a resolver has no say over.
func TestTheDefaultResolverIsTheOnlyPlaceAnAdapterIsConstructed(t *testing.T) {
	roots := []string{"..", filepath.Join("..", "..", "cmd")}
	fset := token.NewFileSet()
	var offenders []string

	for _, root := range roots {
		err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") {
				return err
			}
			if strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "runners.go" {
				return nil
			}
			file, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			ast.Inspect(file, func(n ast.Node) bool {
				lit, ok := n.(*ast.CompositeLit)
				if !ok {
					return true
				}
				sel, ok := lit.Type.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "CLI" {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "agent" {
					offenders = append(offenders, fset.Position(lit.Pos()).String())
				}
				return true
			})
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	if len(offenders) != 0 {
		t.Fatalf("agent.CLI is constructed outside the seam, so a resolver has no say over these turns:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

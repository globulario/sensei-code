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

// architectSpec is the architect turn a governed task takes, as the re-plan and
// execute call sites ask for it.
func architectSpec(taskID string) RunnerSpec {
	spec := specFor(roles.Architect)
	spec.TaskID = taskID
	return spec
}

// pinGraph stands in for the start gate's bindGraph, so a witness can compare
// every referent of a binding rather than only the ones that survive without
// a Sensei process. Both sides of a comparison are pinned identically.
func pinGraph(e *Engine, taskID, commit string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.graphs == nil {
		e.graphs = map[string]*agent.GraphBinding{}
	}
	e.graphs[taskID] = &agent.GraphBinding{Digest: commit}
}

// W4 THE DIAGNOSIS NAMES THE MISSING REFERENT, AND THE RESOLVER IS NEVER
// CONSULTED -- SCOPED to the objective of a task whose durable record holds one
// and records the governed lane.
//
// The task record is real: task.created and mode.selected are in a durable
// session. Only the process objective cache is perturbed. The recording
// resolver FAILS the witness if it is asked at all: a refusal it produced would
// be "no adapter took the architect role", which is the false diagnosis this
// guard exists to replace.
//
// Fails if: the guard is removed or moved after either adapter choice (the
// resolver is asked, or the CLI adapter is built); the guard reads the process
// cache instead of the record (it would find nothing to require); or its
// reason names the roster rather than the objective.
func TestW4AGovernedTurnThatLostItsObjectiveIsRefusedBeforeAnyAdapterIsChosen(t *testing.T) {
	record := func(t *testing.T, lane TaskMode) *Engine {
		t.Helper()
		e, _, _ := blockedEngine(t, t.TempDir(), "session-w4")
		e.emit(event.New(e.SessionID, "task-w4", event.SourceSystem, event.TaskCreated, "the objective", nil))
		e.announceMode("task-w4", lane)
		return e
	}

	for name, useResolver := range map[string]bool{"configured resolver": true, "provider command line": false} {
		t.Run(name, func(t *testing.T) {
			e := record(t, governedMode(RequestedByHuman))
			// The perturbation: this process holds no objective for the task,
			// as a restarted process whose resume path did not restore one.
			if e.objective("task-w4").Text != "" {
				t.Fatal("premise: the process cache holds no objective")
			}
			resolver := &stubResolver{resolved: Resolved{Runner: &stubRunner{}, Name: "remote-architect"}}
			if useResolver {
				e.Runners = resolver
			}
			got, err := e.resolveRunner(architectSpec("task-w4"))
			if len(resolver.seen) != 0 {
				t.Fatalf("the resolver was consulted for a turn that had lost its objective: %+v", resolver.seen)
			}
			if err == nil || got.Runner != nil {
				t.Fatalf("an architect turn with no objective identity was served: %T", got.Runner)
			}
			if !strings.Contains(err.Error(), "not bound to its objective") || !strings.Contains(err.Error(), "objective digest") {
				t.Fatalf("the refusal does not name the objective referent: %v", err)
			}
			if strings.Contains(err.Error(), "no adapter") || strings.Contains(err.Error(), "returned no adapter") {
				t.Fatalf("the refusal blames the roster: %v", err)
			}
		})
	}

	// CONTROL: the assisted lane records no objective by design and keeps
	// asking exactly as it does today. The exemption is the recorded lane.
	t.Run("assisted lane is asked as today", func(t *testing.T) {
		e := record(t, assistedMode())
		resolver := &stubResolver{resolved: Resolved{Runner: &stubRunner{}, Name: "remote-architect"}}
		e.Runners = resolver
		if _, err := e.resolveRunner(architectSpec("task-w4")); err != nil {
			t.Fatalf("an assisted architect turn was refused: %v", err)
		}
		if len(resolver.seen) != 1 || resolver.seen[0].Architecture.ObjectiveDigest != "" {
			t.Fatalf("the assisted turn did not reach its resolver unchanged: %+v", resolver.seen)
		}
	})

	// CONTROL: sensei.repository is optional. A governed turn that holds its
	// objective but no graph repository still runs, on either adapter path.
	t.Run("an incomplete graph referent keeps its policy", func(t *testing.T) {
		e := record(t, governedMode(RequestedByHuman))
		e.recordObjective("task-w4", Objective{Text: "the objective", Provenance: RequestedByHuman})
		e.Config.Sensei.Repository = ""
		if got, err := e.resolveRunner(architectSpec("task-w4")); err != nil || got.Runner == nil {
			t.Fatalf("a local architect without sensei.repository was refused: %v", err)
		}
		resolver := &stubResolver{resolved: Resolved{Runner: &stubRunner{}, Name: "remote-architect"}}
		e.Runners = resolver
		if _, err := e.resolveRunner(architectSpec("task-w4")); err != nil || len(resolver.seen) != 1 {
			t.Fatalf("an incomplete graph referent was refused before the resolver: %v", err)
		}
		if b := resolver.seen[0].Architecture; b.ObjectiveDigest == "" || b.GraphRepository != "" {
			t.Fatalf("premise: objective bound, graph repository absent: %+v", b)
		}
	})
}

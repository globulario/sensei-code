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
	e.recordObjective("task-1", Objective{Text: "task", Provenance: SubmittedUnattended})

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
	e.recordObjective("task-1", Objective{Text: "task", Provenance: SubmittedUnattended})

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

// THE ASSISTED LANE IS NOT A GOVERNED CONTINUATION. It writes TaskCreated but
// records no objective (a declared non-goal of the objective record), and its
// architect turn keeps the binding it had before the record: none. It is not
// recovered from its TaskCreated, and it is not refused for lacking one. The
// governed roster walk, over the very same durable record, is bound to it.
func TestTheAssistedArchitectTurnKeepsItsUnboundContract(t *testing.T) {
	const task = "task-assisted"
	for name, setup := range map[string]func(e *Engine){
		"no durable record":         func(e *Engine) {},
		"durable TaskCreated":       func(e *Engine) { e.Store = storeWithTaskCreated(t, task, "what does this do?") },
		"unreadable durable record": func(e *Engine) { e.Store = sessionStore(t) },
	} {
		t.Run(name, func(t *testing.T) {
			resolver := &fixedResolver{runner: &scriptedArchitect{}, name: "claude"}
			e := &Engine{SessionID: "s1", Runners: resolver}
			setup(e)
			// The assisted lane's own request shape (assisted.go).
			if _, err := e.resolveRunner(RunnerSpec{Role: roles.Architect, Agent: e.Config.Architect, Source: event.SourceArchitect, TaskID: task}); err != nil {
				t.Fatalf("the assisted architect turn was refused: %v", err)
			}
			if len(resolver.specs) != 1 {
				t.Fatalf("the resolver was not asked exactly once: %d", len(resolver.specs))
			}
			if got := resolver.specs[0].Architecture; got.TaskID != task || got.ObjectiveDigest != "" {
				t.Fatalf("the assisted turn's binding changed: %+v", got)
			}
		})
	}

	// The governed walk over the same durable record binds the recovered bytes.
	architect := &scriptedArchitect{turns: []architectTurn{{text: replyDecision}}}
	resolver := &fixedResolver{runner: architect, name: "claude"}
	e := &Engine{SessionID: "s1", Runners: resolver, Store: storeWithTaskCreated(t, task, "what does this do?")}
	e.Config.Architect.Name, e.Config.Architect.Command = "claude", "true"
	_, _ = e.resolveArchitectureIn(t.Context(), nil, certifiedStart{}, task, "what does this do?", "PROMPT", t.TempDir())
	if len(resolver.specs) == 0 {
		t.Fatal("the governed architect turn was never asked")
	}
	if resolver.specs[0].Role != roles.Architect {
		t.Fatalf("the governed walk did not ask for the architect role: %q", resolver.specs[0].Role)
	}
	if got, want := resolver.specs[0].Architecture.ObjectiveDigest, e.bindArchitecture(task, "what does this do?").ObjectiveDigest; got == "" || got != want {
		t.Fatalf("the governed turn was not bound to the recorded objective: %q, want %q", got, want)
	}
}

// PROVIDER INVOCATION FACTS ARE SETTLED BEFORE ROUTING (DF-41A2). Each witness
// below drives a concrete CLI invocation of the provider "claude" -- a real
// process through the default adapter -- and settles what it returned. The
// structured and plain-text argument sets differ only in --output-format, so
// the transport, never the provider name, decides what is read as lifecycle.

// streamStart, streamProgress and streamTerminal are stream-json lifecycle
// envelopes for operation id.
func streamStart(id string) string {
	return `{"type":"assistant","message":{"content":[{"type":"tool_use","id":"` + id + `","name":"Bash","input":{}}]}}`
}

// streamProgress has the transport's wire shape: the record carries its own
// heartbeat id and names the operation it reports on as its parent.
func streamProgress(id string) string {
	return `{"type":"tool_progress","tool_use_id":"` + id + `-heartbeat-0","tool_name":"Bash","parent_tool_use_id":"` + id + `","heartbeat":true}`
}

func streamTerminal(id string) string {
	return `{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"` + id + `","content":"ok"}]}}`
}

func streamResult(report string) string {
	return `{"type":"result","subtype":"success","result":"` + report + `","session_id":"sid-1"}`
}

// invokeClaude runs one concrete claude CLI invocation whose process writes
// stdout and exits with exit, and settles it as runCandidate does.
func invokeClaude(t *testing.T, structured bool, stdout, exit string) (agent.Result, error, settledInvocation) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "stdout")
	if err := os.WriteFile(path, []byte(stdout), 0o644); err != nil {
		t.Fatal(err)
	}
	args := []string{"-c", `cat >/dev/null; cat "$1"; exit "$2"`, "sh", path, exit}
	if structured {
		args = append(args, "--output-format", "stream-json", "--verbose")
	}
	return runClaude(t, dir, "sh", args)
}

func runClaude(t *testing.T, dir, command string, args []string) (agent.Result, error, settledInvocation) {
	t.Helper()
	e := &Engine{SessionID: "sess-1"}
	got, err := e.resolveRunner(RunnerSpec{Role: roles.Implementer, Source: event.SourceClaude, TaskID: "task-1",
		Agent: config.Agent{Name: "claude", Command: command, Args: args, Graph: "none"}})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	result, runErr := got.Runner.Run(context.Background(), agent.Request{Role: roles.Implementer, TaskID: "task-1", Workspace: dir, Prompt: "the prompt"}, func(event.Event) {})
	return result, runErr, settleInvocation(got.Name, 1, result, runErr)
}

func lines(l ...string) string { return strings.Join(l, "\n") + "\n" }

// settledStructuredReport is a successful structured invocation returning
// report in its envelope, settled.
func settledStructuredReport(t *testing.T, report string) settledInvocation {
	t.Helper()
	encoded := strings.ReplaceAll(strings.ReplaceAll(report, `"`, `\"`), "\n", `\n`)
	_, err, s := invokeClaude(t, true, lines(streamStart("A"), streamTerminal("A"), streamResult(encoded)), "0")
	if err != nil {
		t.Fatalf("the structured invocation failed: %v", err)
	}
	return s
}

// W1 RETURNED IS OBSERVED, NOT INFERRED.
func TestW1ReturnedIsObservedNotInferred(t *testing.T) {
	_, err, s := invokeClaude(t, true, lines(streamStart("A"), streamTerminal("A"), streamResult("REPORT")), "0")
	if err != nil || !s.Observed || !s.Returned || s.Report != "REPORT" {
		t.Fatalf("an observed structured return was not settled as returned: err=%v %+v", err, s)
	}
	if _, err, s := invokeClaude(t, false, "plain report\n", "0"); err != nil || !s.Returned || s.Report != "plain report" {
		t.Fatalf("an observed plain-text return was not settled as returned: err=%v %+v", err, s)
	}

	// A structured process that exits 0 without its return envelope reaches
	// the ordinary success route -- it has compatibility text and no error --
	// and still returned nothing.
	result, err, s := invokeClaude(t, true, lines(streamStart("A"), streamTerminal("A")), "0")
	if err != nil || strings.TrimSpace(result.Text) == "" {
		t.Fatalf("premise: the process should reach the success route with text: err=%v text=%q", err, result.Text)
	}
	if s.Returned || s.Report != "" || !s.Exited || s.ExitCode != 0 {
		t.Fatalf("Returned was inferred from the success route: %+v", s)
	}

	// An adapter that supplies no observation succeeds without one.
	s = settleInvocation("remote", 1, agent.Result{Text: "a report"}, nil)
	if s.Observed || s.Returned || s.Report != "" || s.Text != "a report" {
		t.Fatalf("an unobserved invocation was settled as returned: %+v", s)
	}
}

// W2 ERROR-BEARING RETURN IS STILL SETTLED. And a transport that failed before
// any result is not settled as though one was observed.
func TestW2AnErrorBearingReturnIsStillSettled(t *testing.T) {
	_, err, s := invokeClaude(t, true, lines(streamStart("A"), streamTerminal("A"), streamResult("REPORT")), "3")
	if err == nil {
		t.Fatal("premise: the process exited 3")
	}
	if !s.Returned || s.Err != err || s.Report != "REPORT" || s.TransportFailed {
		t.Fatalf("the error-bearing return lost its facts: %+v", s)
	}
	if s.Transport.Adapter != agent.AdapterStreamJSON || !s.Transport.Lifecycle || !s.Exited || s.ExitCode != 3 {
		t.Fatalf("the transport or process outcome was lost: %+v", s)
	}
	if len(s.Operations) != 1 || !s.Operations[0].Terminated || len(s.Lifecycle) != 2 {
		t.Fatalf("the lifecycle observations were lost: %+v", s)
	}

	dir := t.TempDir()
	_, err, s = runClaude(t, dir, filepath.Join(dir, "no-such-command"), []string{"--output-format", "stream-json"})
	if err == nil {
		t.Fatal("premise: the command does not exist")
	}
	if !s.TransportFailed || s.Returned || s.Exited || s.Report != "" || s.Err != err {
		t.Fatalf("a pre-result transport failure was settled as an observed result: %+v", s)
	}
}

// W4 TRANSPORT CAPABILITY, NOT PROVIDER NAME.
func TestW4LifecycleCapabilityIsTheTransportsNotTheProviders(t *testing.T) {
	stream := lines(streamStart("A"), streamProgress("A"), streamTerminal("A"), streamResult("REPORT"))
	_, _, structured := invokeClaude(t, true, stream, "0")
	_, _, plain := invokeClaude(t, false, stream, "0")
	if structured.Provider != "claude" || plain.Provider != "claude" {
		t.Fatalf("premise: both invocations are claude: %q %q", structured.Provider, plain.Provider)
	}
	if !structured.Transport.Lifecycle || len(structured.Lifecycle) != 3 || len(structured.Operations) != 1 {
		t.Fatalf("the structured transport yielded no lifecycle: %+v", structured)
	}
	if plain.Transport.Lifecycle || plain.Transport.Adapter != agent.AdapterPlainText {
		t.Fatalf("the plain-text invocation was given lifecycle capability by its provider name: %+v", plain.Transport)
	}
	if len(plain.Lifecycle) != 0 || len(plain.Operations) != 0 || len(plain.Orphans) != 0 {
		t.Fatalf("the plain-text invocation was decoded as telemetry: %+v", plain)
	}
	if plain.Report != strings.TrimSpace(stream) {
		t.Fatalf("the plain-text report is not what it returned: %q", plain.Report)
	}

	// Lifecycle a plain-text observation somehow carries is not settled.
	s := settleInvocation("claude", 1, agent.Result{Invocation: &agent.Invocation{Returned: true,
		Transport: agent.Transport{Adapter: agent.AdapterPlainText},
		Lifecycle: []agent.LifecycleObservation{{Seq: 0, Kind: agent.LifecycleStarted, Operation: "A"}}}}, nil)
	if len(s.Lifecycle) != 0 || len(s.Operations) != 0 {
		t.Fatalf("a transport without the capability was settled with lifecycle: %+v", s)
	}
}

// W5 PLAIN TEXT IS NOT TELEMETRY.
func TestW5PlainTextIsNotTelemetry(t *testing.T) {
	prose := lines("I ran tool_use id X and got tool_result for tool_use_id X.",
		streamStart("X"), "operation X started; operation X terminal", streamTerminal("X"))
	_, err, s := invokeClaude(t, false, prose, "0")
	if err != nil || !s.Returned {
		t.Fatalf("premise: the plain-text invocation returned: err=%v %+v", err, s)
	}
	if len(s.Lifecycle) != 0 || len(s.Operations) != 0 || len(s.Orphans) != 0 {
		t.Fatalf("prose was decoded as lifecycle telemetry: %+v", s)
	}
}

// W6 STRUCTURED EVENT ORDER. The same multiset of observations in two orders
// settles differently, so the order is what attributes them.
func TestW6StructuredEventOrderAttributesTerminals(t *testing.T) {
	_, _, s := invokeClaude(t, true, lines(
		streamStart("A"), streamStart("B"), streamProgress("B"), streamTerminal("A"),
		streamProgress("A"), streamTerminal("B"), streamResult("done")), "0")
	want := []agent.LifecycleObservation{
		{Seq: 0, Kind: agent.LifecycleStarted, Operation: "A"},
		{Seq: 1, Kind: agent.LifecycleStarted, Operation: "B"},
		{Seq: 2, Kind: agent.LifecycleUpdate, Operation: "B"},
		{Seq: 3, Kind: agent.LifecycleTerminal, Operation: "A"},
		{Seq: 4, Kind: agent.LifecycleUpdate, Operation: "A"},
		{Seq: 5, Kind: agent.LifecycleTerminal, Operation: "B"},
	}
	if len(s.Lifecycle) != len(want) {
		t.Fatalf("the observation sequence was not kept: %+v", s.Lifecycle)
	}
	for i := range want {
		if s.Lifecycle[i] != want[i] {
			t.Fatalf("observation %d is %+v, want %+v", i, s.Lifecycle[i], want[i])
		}
	}
	if len(s.Operations) != 2 {
		t.Fatalf("operations: %+v", s.Operations)
	}
	a, b := s.Operations[0], s.Operations[1]
	if a.ID != "A" || !a.Terminated || a.Terminal != 3 || len(a.Updates) != 0 {
		t.Fatalf("A was misattributed: %+v", a)
	}
	if b.ID != "B" || !b.Terminated || b.Terminal != 5 || len(b.Updates) != 1 || b.Updates[0] != 2 {
		t.Fatalf("B was misattributed: %+v", b)
	}
	if len(s.Orphans) != 1 || s.Orphans[0] != want[4] {
		t.Fatalf("an update after A's terminal was attributed to it: %+v", s.Orphans)
	}

	// Started-before-terminal and terminal-before-started are the same sets.
	_, _, closed := invokeClaude(t, true, lines(streamStart("A"), streamTerminal("A"), streamResult("done")), "0")
	_, _, open := invokeClaude(t, true, lines(streamTerminal("A"), streamStart("A"), streamResult("done")), "0")
	if len(closed.Open()) != 0 || len(open.Open()) != 1 {
		t.Fatalf("order did not decide attribution: closed open=%+v, reversed open=%+v", closed.Open(), open.Open())
	}
}

// W7 ORPHAN TERMINAL.
func TestW7AnOrphanTerminalAccountsForNothing(t *testing.T) {
	_, _, s := invokeClaude(t, true, lines(streamTerminal("X"), streamProgress("X"), streamStart("X"), streamResult("done")), "0")
	if len(s.Orphans) != 2 || s.Orphans[0].Kind != agent.LifecycleTerminal || s.Orphans[0].Operation != "X" {
		t.Fatalf("the orphan terminal was not kept as an orphan: %+v", s.Orphans)
	}
	if len(s.Operations) != 1 {
		t.Fatalf("the orphan terminal created an operation: %+v", s.Operations)
	}
	if op := s.Operations[0]; op.Terminated || op.Generation != 1 || op.Started != 2 {
		t.Fatalf("the orphan terminal pre-accounted the later start: %+v", op)
	}
}

// W8 ID REUSE. Operation ids are the transport's tool_use ids, and nothing in
// its contract is relied on to make them unique within an invocation.
func TestW8AReusedIdIsANewGenerationAnEarlierTerminalCannotClose(t *testing.T) {
	_, _, s := invokeClaude(t, true, lines(streamStart("X"), streamTerminal("X"), streamStart("X"), streamResult("done")), "0")
	if len(s.Operations) != 2 {
		t.Fatalf("the generations were collapsed: %+v", s.Operations)
	}
	first, second := s.Operations[0], s.Operations[1]
	if !first.Terminated || first.Terminal != 1 || first.Generation != 1 {
		t.Fatalf("the first generation: %+v", first)
	}
	if second.Terminated || second.Generation != 2 || second.Started != 2 {
		t.Fatalf("the earlier terminal closed the later generation: %+v", second)
	}
	if open := s.Open(); len(open) != 1 || open[0].Generation != 2 {
		t.Fatalf("open operations: %+v", open)
	}
}

// W9 ERROR DOES NOT ERASE REPORT.
func TestW9AnErrorDoesNotEraseTheReport(t *testing.T) {
	report := "Changed main.go.\n" + faAccounting
	_, err, s := invokeClaude(t, false, report+"\n", "1")
	if err == nil {
		t.Fatal("premise: the process exited 1")
	}
	if !s.Returned || s.Report != report || s.Err != err {
		t.Fatalf("the error erased the returned report: %+v", s)
	}
	if responses, perr := parseFindingResponses(s.Report); perr != nil || len(responses) != 2 {
		t.Fatalf("the preserved report is not the worker's output: %v %+v", perr, responses)
	}
}

//go:build stubsmoke

// A provider that proves it cannot serve the architect turn blocks the task; a
// restarted engine resumes the SAME task and retries that turn.
//
// Sensei, the start gate, the candidate identity and the session record are the
// product's own. Only the architect is a Go runner, because the property under
// test is what the ENGINE does with an adapter's proof -- the adapter's own
// classification is pinned against the recorded wire in internal/provider.
//
//	go test -tags stubsmoke ./internal/acceptance/ -run TestABlockedArchitectResumesTheSameTaskAfterRestart -v -count=1
package acceptance

import (
	"context"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// architectRunner answers the architect turn, or proves it cannot.
type architectRunner struct {
	calls       atomic.Int32
	unavailable bool
}

func (r *architectRunner) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	r.calls.Add(1)
	if r.unavailable {
		return agent.Result{}, &provider.Unavailable{Provider: "chatgpt", Reason: "usageLimitExceeded",
			Detail: "You've hit your usage limit."}
	}
	return agent.Result{Text: `{"decision":"reply","message":"the deployment identity question is answered"}`}, nil
}

type architectOnly struct{ runner agent.Runner }

func (a architectOnly) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	if spec.Role == roles.Architect {
		return workflow.Resolved{Runner: a.runner, Name: "chatgpt", Label: "ChatGPT"}, nil
	}
	return workflow.CLIResolved(spec, "stub"), nil
}

func settle(t *testing.T, events <-chan event.Event, taskID string) []event.Event {
	t.Helper()
	var seen []event.Event
	deadline := time.After(5 * time.Minute)
	for {
		select {
		case <-deadline:
			t.Fatalf("the run did not settle: %v", seen)
		case ev := <-events:
			if ev.TaskID != taskID && ev.TaskID != "" {
				continue
			}
			seen = append(seen, ev)
			t.Logf("[%-26s] %s", ev.Kind, oneLine(ev.Summary))
			switch ev.Kind {
			case event.WorkflowFailed, event.WorkflowCompleted, event.WorkflowBlockedExternal,
				event.WorkflowNotConverged, event.WorkflowAwaitingAuthority, event.WorkflowStopped,
				event.WorkflowAwaitingReview, event.WorkflowTimedOut:
				return seen
			case event.AuthorityRequired:
				t.Fatalf("a reply-only run reached a human boundary: %s", ev.Summary)
			}
		}
	}
}

func has(events []event.Event, k event.Kind) bool {
	for _, e := range events {
		if e.Kind == k {
			return true
		}
	}
	return false
}

func TestABlockedArchitectResumesTheSameTaskAfterRestart(t *testing.T) {
	root := repoRoot(t)
	repo := gitx.Repo{Root: root}
	if clean, err := repo.IsClean(context.Background()); err != nil || !clean {
		t.Skip("canonical checkout is dirty; the governed path refuses one by design")
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sessionID := session.ID(time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	// First process: the architect's provider is out of quota.
	store1, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	bus1 := event.NewBus()
	events1, unsub1 := bus1.Subscribe(512)
	defer unsub1()
	down := &architectRunner{unavailable: true}
	engine1 := workflow.New(repo, cfg, bus1, store1, sessionID)
	engine1.Runners = architectOnly{runner: down}
	taskID := engine1.SubmitGovernedUnattended(ctx, "Explain how a governed workspace proves its runtime binaries match its source.")
	first := settle(t, events1, taskID)
	if !has(first, event.WorkflowBlockedExternal) || has(first, event.WorkflowFailed) {
		t.Fatalf("a proven unavailable architect did not block the task: %v", first)
	}
	if got := down.calls.Load(); got != 1 {
		t.Fatalf("the unavailable architect was asked %d times, want once", got)
	}
	before, ok, err := candidate.Load(root, taskID)
	if err != nil || !ok {
		t.Fatalf("the blocked task has no recorded candidate identity: ok=%v err=%v", ok, err)
	}

	// Restart: a new store object, a new bus, a new engine. Nothing in memory.
	store2, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store2.Load()
	if err != nil {
		t.Fatal(err)
	}
	var target *session.Interrupted
	for _, task := range session.FindInterrupted(history) {
		if task.TaskID == taskID {
			task := task
			target = &task
		}
	}
	if target == nil || len(target.BlockedExternal) == 0 || target.Planned {
		t.Fatalf("the blocked task was not rediscovered as itself after restart: %+v", target)
	}
	bus2 := event.NewBus()
	events2, unsub2 := bus2.Subscribe(512)
	defer unsub2()
	up := &architectRunner{}
	engine2 := workflow.New(repo, cfg, bus2, store2, sessionID)
	engine2.Runners = architectOnly{runner: up}
	if resumed := engine2.Resume(ctx, *target); resumed != taskID {
		t.Fatalf("resume answered for %q, want %q", resumed, taskID)
	}
	second := settle(t, events2, taskID)
	if has(second, event.TaskCreated) {
		t.Fatal("resume minted a new task")
	}
	if !has(second, event.WorkflowCompleted) {
		t.Fatalf("the resumed task did not complete once its architect could answer: %v", second)
	}
	if got := up.calls.Load(); got != 1 {
		t.Fatalf("the architect turn was retried %d times on resume, want once", got)
	}
	after, ok, err := candidate.Load(root, taskID)
	if err != nil || !ok || after.BaseSHA != before.BaseSHA || after.TaskID != before.TaskID {
		t.Fatalf("the candidate identity changed across the block: before=%+v after=%+v err=%v", before, after, err)
	}

	// The canonical checkout is untouched either way.
	if clean, _ := repo.IsClean(context.Background()); !clean {
		status, _ := exec.Command("git", "-C", root, "status", "--porcelain").CombinedOutput()
		t.Errorf("the run modified the canonical checkout:\n%s", strings.TrimSpace(string(status)))
	}
}

// implementerDown proves, every time, that the implementer's provider cannot
// serve. Every other role takes the product's own command line.
type implementerDown struct{ calls *atomic.Int32 }

func (d implementerDown) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	if spec.Role == roles.Implementer {
		return workflow.Resolved{Runner: runnerFunc(func() (agent.Result, error) {
			d.calls.Add(1)
			return agent.Result{}, &provider.Unavailable{Provider: spec.Agent.Name, Reason: "usageLimitExceeded"}
		}), Name: spec.Agent.Name, Label: spec.Agent.Name}, nil
	}
	return workflow.CLIResolved(spec, "stub"), nil
}

type runnerFunc func() (agent.Result, error)

func (f runnerFunc) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	return f()
}

// The same primitive for the implementer, through the real execute AND the real
// planned-task Resume: blocked in the first process, still blocked after a
// restart, and both times the SAME task -- never FAILED, never a new task.
func TestABlockedImplementerStaysTheSameTaskAcrossRestart(t *testing.T) {
	root := repoRoot(t)
	repo := gitx.Repo{Root: root}
	if clean, err := repo.IsClean(context.Background()); err != nil || !clean {
		t.Skip("canonical checkout is dirty; the governed path refuses one by design")
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	stub := buildStubAgent(t, root)
	target := anchoredTarget
	cfg.Architect = config.Agent{Name: "stub-architect", Command: stub,
		Args: []string{"--role", "architect", "--target", target}, Graph: "none"}
	cfg.Implementors = []config.Agent{{Name: "claude", Command: stub,
		Args: []string{"--role", "implementor", "--target", target}, Graph: "none"}}
	cfg.Reviewer = config.Agent{Name: "codex", Command: stub, Args: []string{"--role", "reviewer"}, Graph: "none"}
	cfg.Reviewers = []config.Agent{cfg.Reviewer}
	sessionID := session.ID(time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	var calls atomic.Int32

	store1, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	bus1 := event.NewBus()
	events1, unsub1 := bus1.Subscribe(512)
	defer unsub1()
	engine1 := workflow.New(repo, cfg, bus1, store1, sessionID)
	engine1.Runners = implementerDown{calls: &calls}
	taskID := engine1.SubmitGoverned(ctx, "Append one trailing comment line to "+target+" and change nothing else.")
	first := settle(t, events1, taskID)
	if has(first, event.WorkflowAwaitingAuthority) {
		t.Skip("the router escalated before implementation; the implementer turn was not reached")
	}
	if !has(first, event.PlanProposed) || !has(first, event.WorkflowBlockedExternal) || has(first, event.WorkflowFailed) {
		t.Fatalf("an unavailable implementer did not block the planned task: %v", first)
	}
	if has(first, event.HandoffCreated) {
		t.Fatal("an unavailable implementer was handed off as a failed worker")
	}

	store2, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store2.Load()
	if err != nil {
		t.Fatal(err)
	}
	var target2 *session.Interrupted
	for _, task := range session.FindInterrupted(history) {
		if task.TaskID == taskID {
			task := task
			target2 = &task
		}
	}
	if target2 == nil || !target2.Planned || len(target2.BlockedExternal) == 0 {
		t.Fatalf("the blocked planned task was not rediscovered as itself: %+v", target2)
	}
	bus2 := event.NewBus()
	events2, unsub2 := bus2.Subscribe(512)
	defer unsub2()
	engine2 := workflow.New(repo, cfg, bus2, store2, sessionID)
	engine2.Runners = implementerDown{calls: &calls}
	if resumed := engine2.Resume(ctx, *target2); resumed != taskID {
		t.Fatalf("resume answered for %q, want %q", resumed, taskID)
	}
	second := settle(t, events2, taskID)
	if has(second, event.TaskCreated) {
		t.Fatal("resume minted a new task")
	}
	if !has(second, event.WorkflowBlockedExternal) || has(second, event.WorkflowFailed) {
		t.Fatalf("a task still blocked after restart was not reported as blocked: %v", second)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("the implementer turn was asked %d times across the two processes, want 2", got)
	}
}

// scriptedReviewer returns a bounded REVISE until accept is set, then ACCEPT.
type scriptedReviewer struct {
	accept atomic.Bool
	calls  atomic.Int32
}

func (r *scriptedReviewer) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	r.calls.Add(1)
	if r.accept.Load() {
		return agent.Result{Text: `{"decision":"accept","summary":"the candidate satisfies the plan"}`, Session: roles.Fresh}, nil
	}
	return agent.Result{Text: `{"decision":"revise","summary":"the proof is missing","findings":[{"id":"1","severity":"blocking",` +
		`"claim":"the change is not proven","reference":"` + anchoredTarget + `","reason":"no witness"}]}`, Session: roles.Fresh}, nil
}

// countingArchitect is the product's own stub architect, counted.
type countingResolver struct {
	reviewer  agent.Runner
	architect *atomic.Int32
}

func (c countingResolver) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	switch spec.Role {
	case roles.Reviewer:
		return workflow.Resolved{Runner: c.reviewer, Name: "chatgpt", Label: "ChatGPT"}, nil
	case roles.Architect:
		inner := workflow.CLIResolved(spec, "stub")
		counter := c.architect
		inner.Runner = runnerWrap(func(ctx context.Context, req agent.Request, emit func(event.Event)) (agent.Result, error) {
			counter.Add(1)
			return workflow.CLIResolved(spec, "stub").Runner.Run(ctx, req, emit)
		})
		return inner, nil
	}
	return workflow.CLIResolved(spec, "stub"), nil
}

type runnerWrap func(context.Context, agent.Request, func(event.Event)) (agent.Result, error)

func (f runnerWrap) Run(ctx context.Context, req agent.Request, emit func(event.Event)) (agent.Result, error) {
	return f(ctx, req, emit)
}

// DF-6 end to end, through the real execute and the real planned Resume: a task
// whose every review cycle is spent ends NOT_CONVERGED (not FAILED); a restarted
// engine resumes the SAME task, the architect re-plans, and the task goes on to
// complete once the reviewer accepts.
func TestANotConvergedTaskIsRePlannedAndCompletesAfterRestart(t *testing.T) {
	root := repoRoot(t)
	repo := gitx.Repo{Root: root}
	if clean, err := repo.IsClean(context.Background()); err != nil || !clean {
		t.Skip("canonical checkout is dirty; the governed path refuses one by design")
	}
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	stub := buildStubAgent(t, root)
	target := anchoredTarget
	cfg.Architect = config.Agent{Name: "stub-architect", Command: stub,
		Args: []string{"--role", "architect", "--target", target}, Graph: "none"}
	cfg.Implementors = []config.Agent{{Name: "claude", Command: stub,
		Args: []string{"--role", "implementor", "--target", target}, Graph: "none"}}
	cfg.Reviewer = config.Agent{Name: "chatgpt", Graph: "none"}
	cfg.Reviewers = []config.Agent{cfg.Reviewer}
	cfg.Workflow.ReviewCycles = 1
	sessionID := session.ID(time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	reviewer := &scriptedReviewer{}
	var architect atomic.Int32

	store1, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	bus1 := event.NewBus()
	events1, unsub1 := bus1.Subscribe(1024)
	defer unsub1()
	engine1 := workflow.New(repo, cfg, bus1, store1, sessionID)
	engine1.Runners = countingResolver{reviewer: reviewer, architect: &architect}
	taskID := engine1.SubmitGoverned(ctx, "Append one trailing comment line to "+target+" and change nothing else.")
	first := settle(t, events1, taskID)
	if has(first, event.WorkflowAwaitingAuthority) {
		t.Skip("the router escalated before implementation; the review loop was not reached")
	}
	if !has(first, event.WorkflowNotConverged) || has(first, event.WorkflowFailed) {
		t.Fatalf("spending every review cycle did not end NOT_CONVERGED: %v", first)
	}
	planned := architect.Load()

	store2, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store2.Load()
	if err != nil {
		t.Fatal(err)
	}
	var target2 *session.Interrupted
	for _, task := range session.FindInterrupted(history) {
		if task.TaskID == taskID {
			task := task
			target2 = &task
		}
	}
	if target2 == nil || !target2.Planned || len(target2.NotConverged) == 0 {
		t.Fatalf("the non-converged task was not rediscovered as itself: %+v", target2)
	}
	reviewer.accept.Store(true)
	bus2 := event.NewBus()
	events2, unsub2 := bus2.Subscribe(1024)
	defer unsub2()
	engine2 := workflow.New(repo, cfg, bus2, store2, sessionID)
	engine2.Runners = countingResolver{reviewer: reviewer, architect: &architect}
	if resumed := engine2.Resume(ctx, *target2); resumed != taskID {
		t.Fatalf("resume answered for %q, want %q", resumed, taskID)
	}
	second := settle(t, events2, taskID)
	if has(second, event.TaskCreated) {
		t.Fatal("resume minted a new task")
	}
	if architect.Load() <= planned {
		t.Fatal("the resume did not ask the architect to re-plan before implementing again")
	}
	replanned := false
	for _, ev := range second {
		if ev.Kind == event.Status && strings.Contains(ev.Summary, "architect re-plan it is owed") {
			replanned = true
		}
	}
	if !replanned {
		t.Fatalf("the resume did not announce the owed re-plan: %v", second)
	}
	if !has(second, event.WorkflowCompleted) {
		t.Fatalf("the re-planned task did not complete once the reviewer accepted: %v", second)
	}
}

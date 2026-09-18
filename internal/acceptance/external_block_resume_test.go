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
				event.WorkflowAwaitingAuthority, event.WorkflowStopped, event.WorkflowAwaitingReview:
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

package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/runreceipt"
	"github.com/globulario/sensei-code/internal/session"
)

// COMPOSITION WITNESSES for the three findings of the canonical review of #194
// at b23a8ae. Each drives the real Engine.Resume planned path; Sensei is
// deliberately unstartable (or the context cancelled) so the invocation ends
// at its first step, which is exactly where the defects lived.

// resumeHarness is a real repository holding a recorded candidate for task-r,
// an engine over a durable session in it, and the task as a restart finds it.
func resumeHarness(t *testing.T, work bool) (*Engine, <-chan event.Event, session.Interrupted) {
	t.Helper()
	repo, base := mintRepo(t)
	workspace, err := repo.CreateWorktreeAt(context.Background(), "task-r", base)
	if err != nil {
		t.Fatalf("create the candidate worktree: %v", err)
	}
	if work {
		if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id := candidate.Identity{TaskID: "task-r", Repository: repo.Root, BaseSHA: base, Worktree: workspace,
		Branch: repo.WorktreeBranch("task-r"), CreatedAt: time.Now().UTC()}
	if err := id.Save(repo.Root); err != nil {
		t.Fatal(err)
	}
	store, err := session.New(repo.Root, "session-r")
	if err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	events, cancel := bus.Subscribe(128)
	t.Cleanup(cancel)
	e := New(repo, config.Default(), bus, store, "session-r")
	e.Config.Sensei.Command = "/nonexistent/awareness-mcp"
	return e, events, session.Interrupted{TaskID: "task-r", Task: "the objective", Planned: true}
}

func settleResume(t *testing.T, events <-chan event.Event) []event.Event {
	t.Helper()
	var seen []event.Event
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-events:
			seen = append(seen, ev)
			switch ev.Kind {
			case event.WorkflowFailed, event.WorkflowStopped, event.WorkflowTimedOut, event.WorkflowCompleted,
				event.WorkflowBlockedExternal, event.WorkflowNotConverged, event.WorkflowAwaitingAuthority:
				return seen
			}
		case <-deadline:
			t.Fatalf("the resumed invocation did not settle: %v", kinds(seen))
		}
	}
}

// Finding 3. A resumed invocation that fails before implement measured anything
// must not claim "no candidate" beside work sitting on disk -- and a clean
// worktree is still honestly NONE.
func TestAResumeThatFailsEarlyDoesNotDenyTheCandidateOnDisk(t *testing.T) {
	for name, tc := range map[string]struct {
		work bool
		want runreceipt.CandidateState
	}{
		"work on disk":   {true, runreceipt.CandidatePresent},
		"clean worktree": {false, runreceipt.CandidateNone},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, task := resumeHarness(t, tc.work)
			e.Resume(context.Background(), task)
			rec := receiptFrom(t, settleResume(t, events))
			if rec.CandidateState != tc.want {
				t.Fatalf("the resumed receipt says candidate_state %q; the inherited candidate is %q", rec.CandidateState, tc.want)
			}
		})
	}
}

// Finding 2. A resumed invocation ends through the same classifier as execute:
// a caller stop is STOPPED, not a final failure that makes the task vanish.
func TestAResumeStoppedByItsCallerIsNotAFailure(t *testing.T) {
	e, events, task := resumeHarness(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Resume(ctx, task)
	seen := settleResume(t, events)
	if contains(seen, event.WorkflowFailed) {
		t.Fatalf("a resume stopped by its caller was recorded as a final failure: %v", kinds(seen))
	}
	if !contains(seen, event.WorkflowStopped) {
		t.Fatalf("a resume stopped by its caller did not end STOPPED: %v", kinds(seen))
	}
}

// Finding 2, the case that made it P1. A deferred authority decision reaching
// the shared classifier ends nothing further -- no failure, no stop -- so the
// preserved question stays resumable.
func TestADeferredQuestionIsNeverRecordedAsAnEnding(t *testing.T) {
	e, events, _ := resumeHarness(t, false)
	e.beginReceipt("task-r")
	e.terminateRun(context.Background(), "task-r", "the objective", errAuthorityDeferred)
	if seen := drainEvents(events); contains(seen, event.WorkflowFailed) || contains(seen, event.WorkflowStopped) {
		t.Fatalf("a deferred question was recorded as an ending: %v", kinds(seen))
	}
}

// Finding 1. A plan moves ALL of its scope: the prose, the files a candidate is
// captured under, and the prospective surfaces it is inspected against.
func TestAPlanMovesItsWholeScope(t *testing.T) {
	tc := taskContext{Rationale: "old", Files: []string{"old.go"}, Steps: []string{"old"},
		Consequences: "old", Invariants: []string{"old"}, Prospective: []ProspectiveSurface{{Path: "old_new.go"}}}
	applyPlanScope(&tc, architectureDecision{Summary: "new", Files: []string{"a.go", "b.go"}, Steps: []string{"s"},
		Mode: string(ModeModify), Consequences: "c", Invariants: []string{"i"},
		ProspectiveSurfaces: []ProspectiveSurface{{Path: "created.go"}}})
	if tc.Rationale != "new" || strings.Join(tc.Files, ",") != "a.go,b.go" || strings.Join(tc.Steps, ",") != "s" ||
		tc.Consequences != "c" || strings.Join(tc.Invariants, ",") != "i" || tc.Mode != ModeModify ||
		len(tc.Prospective) != 1 || tc.Prospective[0].Path != "created.go" {
		t.Fatalf("a plan moved only part of its scope: %+v", tc)
	}
	// And both places a plan takes effect use it, so neither can move part.
	for _, fn := range []string{"execute", "Resume"} {
		if !strings.Contains(funcBody(t, "internal/workflow/engine.go", fn), "applyPlanScope") {
			t.Errorf("%s applies a plan without the one scope mapping", fn)
		}
	}
}

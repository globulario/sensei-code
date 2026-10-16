package main

// RULING-373 §21, cross-entry agreement. Over one specimen each, the CLI
// (resume --list, --task, --task --answer) and Engine.ResumeTask -- the call
// the TUI's /resume makes (internal/tui/model.go), and the entry every CLI
// lane reaches -- reach the same containment decision. On an invalid Linux
// lock no entry authorizes a continuation, consumes an answer before
// admission, or reads inventory absence as locked absence; under a valid lock
// the ordinary lanes stay open everywhere. Wording may differ; the decision
// may not. The engine implementation choke point (implement's entry check)
// is witnessed in internal/workflow/workspace_quarantine_r373_gate_test.go.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// r373EngineDecision resumes taskID from the holder's record through a fresh
// engine's Engine.ResumeTask and reports whether the resume was refused on
// containment, and whether the engine took an answer before it settled.
func r373EngineDecision(t *testing.T, root, holder, taskID string) (refused, answered bool, refusal error) {
	t.Helper()
	repo, err := gitx.Discover(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	holderStore, err := session.New(root, holder)
	if err != nil {
		t.Fatal(err)
	}
	history, err := holderStore.Load()
	if err != nil {
		t.Fatal(err)
	}
	var task session.Interrupted
	for _, found := range session.FindInterrupted(history) {
		if found.TaskID == taskID {
			task = found
		}
	}
	if task.TaskID == "" {
		t.Fatalf("premise: %s is discoverable", taskID)
	}
	// A fresh process over the holder's record, as the CLI's resume identity
	// and the TUI's reopened record are: the holder Store, a fresh session.
	fresh, err := session.FreshID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e := workflow.New(repo, config.Default(), event.NewBus(), holderStore, fresh)
	e.Config.Sensei.Command = "/nonexistent/awareness-mcp"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	attempt := e.ResumeTask(ctx, task)
	select {
	case <-attempt.Bound():
	case <-time.After(30 * time.Second):
		t.Fatal("the engine's binding outcome did not settle")
	}
	answered = e.ResolveHuman(taskID, "1")
	b := attempt.Binding()
	cancel()
	select {
	case <-attempt.Ended():
	case <-time.After(30 * time.Second):
		t.Fatal("the engine's invocation did not end")
	}
	if b.Refusal == nil {
		return false, answered, nil
	}
	return errors.Is(b.Refusal, workflow.ErrResumeContainmentRefused), answered, b.Refusal
}

func TestR373S21EveryEntryReachesTheSameContainmentDecision(t *testing.T) {
	const holder = "session-20260130T000000.000000000Z"
	for _, c := range []struct {
		name     string
		question bool
		setup    func(t *testing.T, root, taskID string)
		refused  bool
	}{
		{"planned, settled record, invalid lock", false, r373SettledInvalidLock, true},
		{"question, no record, invalid lock", true, r372InvalidLock, true},
		{"planned, settled record, valid lock", false, wqSettledCandidate, false},
		{"question, no record, valid lock", true, func(*testing.T, string, string) {}, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			root := gitRepoRoot(t)
			const taskID = "t-entry"
			c.setup(t, root, taskID)
			history := r373PlannedHistory(holder, taskID)
			if c.question {
				history = []event.Event{history[0], b2a2Deferred(holder, taskID)}
			}
			writeRawSession(t, root, holder, history...)

			// CLI --list.
			code, stdout, stderr := runResume(t, root, "--list")
			if code != exitCompleted {
				t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
			}
			b := wqBlock(stdout, taskID)
			listRefused := strings.Contains(b, wqUnknownText)
			offered := strings.Contains(b, "--answer 1") || strings.Contains(b, "resume     --task")
			if listRefused == offered {
				t.Fatalf("--list neither refused nor offered the task (or both):\n%s", b)
			}
			// CLI --task (and --task --answer for the question).
			args := [][]string{{"--task", taskID, "--quiet"}}
			if c.question {
				args = append(args, []string{"--task", taskID, "--answer", "1", "--quiet"})
			}
			cliRefused := true
			for _, a := range args {
				_, _, stderr := runResume(t, root, a...)
				refusedHere := strings.Contains(stderr, wqUnknownText)
				if refusedHere && (strings.Contains(stderr, "continues as session") || strings.Contains(stderr, "answered")) {
					t.Errorf("resume %v refused on containment after binding or answering:\n%s", a, stderr)
				}
				cliRefused = cliRefused && refusedHere
			}
			// Engine.ResumeTask (the TUI's /resume).
			engineRefused, answered, refusal := r373EngineDecision(t, root, holder, taskID)
			if engineRefused && answered {
				t.Errorf("the engine took an answer before its containment refusal")
			}
			if listRefused != c.refused || cliRefused != c.refused || engineRefused != c.refused {
				t.Fatalf("the entries disagree (want refused=%v): --list %v, --task %v, Engine.ResumeTask %v (%v)",
					c.refused, listRefused, cliRefused, engineRefused, refusal)
			}
			if c.refused && !errors.Is(refusal, workflow.ErrContainmentUnestablished) {
				t.Fatalf("the engine's refusal is not CANNOT ESTABLISH: %v", refusal)
			}
			t.Logf("%s: --list refused=%v, --task refused=%v, Engine.ResumeTask refused=%v", c.name, listRefused, cliRefused, engineRefused)
		})
	}
}

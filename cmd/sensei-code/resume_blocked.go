package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Continuing a task that is BLOCKED_EXTERNAL, without a TUI.
//
// A provider that proved it could not serve a role turn -- out of quota, for
// example -- used to end the task FAILED, and the only way on was a new task
// with a new identity. It now ends BLOCKED_EXTERNAL, and this is the way back:
// `sensei-code resume --task <id>` with no --answer. It resumes THE SAME task
// through Engine.Resume and retries the turn it is owed. It carries no answer,
// so it can authorize nothing.

var errBlockedBehindQuestion = errors.New("that task is waiting on a human-owned decision; answer it with --answer before its blocked turn can be retried")

// selectBlockedResume decides, from the durable record alone, whether a task is
// to be continued as a blocked role turn. ok=false with no error means the task
// is not in that lane and the caller should try the next one.
//
// Precedence is fixed and read from the record: a standing human question comes
// first (sensei_code.resume.never_skips_a_human_decision), then an owed review,
// then a blocked turn.
func selectBlockedResume(tasks []session.Interrupted, taskID string, reviewOwed bool) (session.Interrupted, bool, error) {
	taskID = strings.TrimSpace(taskID)
	for _, task := range tasks {
		if task.TaskID != taskID {
			continue
		}
		if len(task.BlockedExternal) == 0 || task.AwaitingReview || reviewOwed {
			return session.Interrupted{}, false, nil
		}
		if len(task.AwaitingAuthority) != 0 {
			return session.Interrupted{}, false, fmt.Errorf("%w: %s", errBlockedBehindQuestion, taskID)
		}
		block, err := workflow.ParseExternalBlock(task.BlockedExternal)
		if err != nil {
			return session.Interrupted{}, false, err
		}
		if block.TaskID != taskID {
			return session.Interrupted{}, false, fmt.Errorf("the external block record is bound to task %s, not %s", block.TaskID, taskID)
		}
		return task, true, nil
	}
	return session.Interrupted{}, false, nil
}

// resumeBlockedExternal is `sensei-code resume --task <id>` for a blocked turn.
func resumeBlockedExternal(ctx context.Context, repo gitx.Repo, cfg config.Config, store *session.Store,
	sessionID string, target session.Interrupted, timeout time.Duration, asJSON, quiet bool) int {
	// The same readiness gate run applies, for the same reason.
	if report := inspectQuick(ctx, repo, cfg); !report.Ready() {
		fmt.Fprintln(os.Stderr, report.Render())
		fmt.Fprint(os.Stderr, "\nThis repository is not ready. Run:\n\n    sensei-code setup --apply\n\n")
		return exitFailed
	}

	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	engine := workflow.New(repo, cfg, bus, store, sessionID)

	// The turn travels the configured transport, exactly as it would have the
	// first time; resuming must not quietly change who answers it.
	if banner, err := installGitHubBridge(engine, repo.Root, cfg.GitHubBridge); err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitFailed
	} else if banner != "" {
		fmt.Fprintln(os.Stderr, banner)
	}

	if !quiet {
		block, _ := workflow.ParseExternalBlock(target.BlockedExternal)
		fmt.Printf("resuming task %s  session %s\n", target.TaskID, sessionID)
		fmt.Printf("blocked   %s\n", block.Describe())
	}

	// No answer is carried, so the engine itself is the run control.
	resumedID := engine.Resume(ctx, target)
	return streamUntilSettled(ctx, engine, events, resumedID, asJSON, quiet, timeout)
}

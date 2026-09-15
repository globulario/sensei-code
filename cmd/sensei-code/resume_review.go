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

// Continuing a candidate that is WAITING_REVIEW, without a TUI.
//
// A review request that went unanswered used to end the run FAILED and leave
// the validated candidate unreachable. It now ends WAITING_REVIEW, and this is
// the headless way back: `sensei-code resume --task <id>` with no --answer. It
// resumes THE SAME task through Engine.Resume, which reviews the preserved
// candidate before any worker is called and requests that review again under a
// new request id. It carries no answer and so can authorize nothing: the only
// thing it may do is ask for the review the candidate is already owed.

var (
	errNoReviewOwed         = errors.New("that task is interrupted but is not awaiting a review, so there is no review to resume")
	errReviewBehindQuestion = errors.New("that task is waiting on a human-owned decision; answer it with --answer before its review can continue")
)

// selectReviewResume decides, from the durable record alone, whether a task may
// be continued as a waiting review.
//
// Pure and side-effect free, like selectAuthorityResume: a refusal here costs no
// process, no graph and no bridge. A task with a standing human-owned question is
// refused even if it also owes a review, because continuing it would walk past a
// decision nobody made (sensei_code.resume.never_skips_a_human_decision).
func selectReviewResume(tasks []session.Interrupted, taskID string) (session.Interrupted, error) {
	taskID = strings.TrimSpace(taskID)
	for _, task := range tasks {
		if task.TaskID != taskID {
			continue
		}
		if len(task.AwaitingAuthority) != 0 {
			return session.Interrupted{}, fmt.Errorf("%w: %s", errReviewBehindQuestion, taskID)
		}
		if !task.AwaitingReview {
			return session.Interrupted{}, fmt.Errorf("%w: %s", errNoReviewOwed, taskID)
		}
		return task, nil
	}
	return session.Interrupted{}, fmt.Errorf("%w: %s", errTaskUnknown, taskID)
}

// resumeAwaitingReview is `sensei-code resume --task <id>` for a waiting review.
func resumeAwaitingReview(ctx context.Context, repo gitx.Repo, cfg config.Config, store *session.Store,
	sessionID string, interrupted []session.Interrupted, taskID string,
	timeout time.Duration, asJSON, quiet bool) int {
	target, err := selectReviewResume(interrupted, taskID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitUsage
	}

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

	// The review travels the configured transport, exactly as it did when it
	// was first requested; resuming must not quietly change who reviews.
	if banner, err := installGitHubBridge(engine, repo.Root, cfg.GitHubBridge); err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitFailed
	} else if banner != "" {
		fmt.Fprintln(os.Stderr, banner)
	}

	if !quiet {
		fmt.Printf("resuming task %s  session %s\n", target.TaskID, sessionID)
		fmt.Println("review    owed; the preserved candidate is reviewed again, not rebuilt")
	}

	// No answer is carried, so the engine itself is the run control: it can be
	// stopped, deferred or timed out, and it cannot answer a question.
	resumedID := engine.Resume(ctx, target)
	return streamUntilSettled(ctx, engine, events, resumedID, asJSON, quiet, timeout)
}

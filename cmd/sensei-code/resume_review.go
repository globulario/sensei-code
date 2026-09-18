package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/ghbridge"
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
// candidate before any worker is called and REATTACHES a waiter to the review
// request that is already standing. It carries no answer and so can authorize
// nothing: the only thing it may do is listen for the review the candidate is
// already owed.

var (
	errNoReviewOwed         = errors.New("that task is interrupted but is not awaiting a review, so there is no review to resume")
	errReviewBehindQuestion = errors.New("that task is waiting on a human-owned decision; answer it with --answer before its review can continue")
	errReviewIdentitySplit  = errors.New("the session transcript and the durable review obligation name different requests for that task")
)

// selectReviewResume decides, from the durable record alone, whether a task may
// be continued as a waiting review.
//
// Pure and side-effect free, like selectAuthorityResume: a refusal here costs no
// process, no graph and no bridge. A task with a standing human-owned question is
// refused even if it also owes a review, because continuing it would walk past a
// decision nobody made (sensei_code.resume.never_skips_a_human_decision).
func selectReviewResume(tasks []session.Interrupted, taskID string, owed *ghbridge.ReviewObligation) (session.Interrupted, error) {
	taskID = strings.TrimSpace(taskID)
	for _, task := range tasks {
		if task.TaskID != taskID {
			continue
		}
		if len(task.AwaitingAuthority) != 0 {
			return session.Interrupted{}, fmt.Errorf("%w: %s", errReviewBehindQuestion, taskID)
		}
		// The DURABLE obligation is the authority on whether a review is owed.
		// A process can die after publishing the request and recording the
		// obligation but before the workflow ever emits its WAITING_REVIEW
		// terminal, and that task is resumable: the request exists, the review
		// is owed, and only the transcript is missing (#182 R4).
		if !task.AwaitingReview && owed == nil {
			return session.Interrupted{}, fmt.Errorf("%w: %s", errNoReviewOwed, taskID)
		}
		// When BOTH name a request and they disagree, neither is chosen and
		// nothing is minted to paper over it: one of the two records is wrong
		// about which review this candidate is owed, and guessing would either
		// consume a review through the wrong request or ask for a second one.
		if owed != nil {
			if recorded := recordedReviewRequest(task); recorded != "" && recorded != owed.RequestID {
				return session.Interrupted{}, fmt.Errorf("%w: transcript says %s, obligation says %s",
					errReviewIdentitySplit, recorded, owed.RequestID)
			}
		}
		return task, nil
	}
	return session.Interrupted{}, fmt.Errorf("%w: %s", errTaskUnknown, taskID)
}

// recordedReviewRequest is the request id the session transcript names, or ""
// when it names none. A PROJECTION: it is compared against the obligation, and
// never substituted for it.
func recordedReviewRequest(task session.Interrupted) string {
	if len(task.AwaitingReviewRecord) == 0 {
		return ""
	}
	var w struct {
		RequestID string `json:"request_id"`
	}
	if json.Unmarshal(task.AwaitingReviewRecord, &w) != nil {
		return ""
	}
	return strings.TrimSpace(w.RequestID)
}

// owedReviewObligation is the durable review obligation this workspace records
// for a task, read from the one component that owns review lifetime.
//
// The error is RETURNED, not folded into "there is no obligation". A lifecycle
// conflict and an unreadable store are authority failures, and swallowing them
// let the CLI start readiness checks, install a bridge and spin up an engine on
// a task whose own records disagree about what it owes -- after which the
// reviewer ladder could reclassify the fault as a provider that failed. Absence
// and failure are different answers and the caller needs both.
func owedReviewObligation(repoRoot, taskID string) (*ghbridge.ReviewObligation, error) {
	store := ghbridge.ReviewObligationStore{
		Exchanges: ghbridge.ExchangeLog{Dir: filepath.Join(repoRoot, ".sensei-code", "exchanges")},
	}
	o, found, err := store.Current(strings.TrimSpace(taskID))
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, nil
	}
	return &o, nil
}

// resumeAwaitingReview is `sensei-code resume --task <id>` for a waiting review.
func resumeAwaitingReview(ctx context.Context, repo gitx.Repo, cfg config.Config, store *session.Store,
	sessionID string, interrupted []session.Interrupted, taskID string,
	timeout time.Duration, asJSON, quiet bool) int {
	// Asked BEFORE any readiness check, bridge install or engine: if the owner
	// cannot establish what this task owes, nothing further should happen.
	owed, err := owedReviewObligation(repo.Root, taskID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code resume:", err)
		return exitFailed
	}
	target, err := selectReviewResume(interrupted, taskID, owed)
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
		if owed != nil {
			fmt.Printf("review    owed under standing request %s; a new waiter attaches to it, and the "+
				"preserved candidate is not rebuilt\n", owed.RequestID)
		} else {
			fmt.Println("review    owed; the preserved candidate is reviewed again, not rebuilt")
		}
	}

	// No answer is carried, so the engine itself is the run control: it can be
	// stopped, deferred or timed out, and it cannot answer a question.
	resumedID := engine.Resume(ctx, target)
	return streamUntilSettled(ctx, engine, events, resumedID, asJSON, quiet, timeout)
}

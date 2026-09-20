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

// Continuing a task WITHOUT answering a question, headless.
//
// This began as the way back from BLOCKED_EXTERNAL: a provider that proved it
// could not serve a role turn -- out of quota, for example -- used to end the
// task FAILED, and the only way on was a new task with a new identity. It now
// ends BLOCKED_EXTERNAL, and `sensei-code resume --task <id>` with no --answer
// resumes THE SAME task through Engine.Resume and retries the turn it is owed.
//
// It now hosts every no-answer continuation, because the routing between them is
// one ordered decision (selectResumeLane) rather than a chain of selectors that
// can all decline. None of these lanes carries an answer, so none of them can
// authorize anything: what they may do is continue work already authorized.

var (
	errBlockedBehindQuestion = errors.New("that task is waiting on a human-owned decision; answer it with --answer before its blocked turn can be retried")
	errResumeBehindQuestion  = errors.New("that task is waiting on a human-owned decision; answer it with --answer, because continuing it any other way would walk past a decision nobody made")
	// errObjectiveUnusable is the refusal for a task that exists and cannot say
	// what it is for. task.created is written before execute validates the
	// objective, so a process that dies in that interval leaves exactly this.
	// The task stays visible -- hiding it is the failure class this whole repair
	// is about -- and every continuation of it is refused by name.
	errObjectiveUnusable = errors.New("that task recorded no objective, so there is nothing to continue it as; its identity and any candidate it left are on disk, but no continuation can supply the work the owner asked for")
)

// resumeLane is what a task named WITHOUT an answer is continued as.
//
// The lanes are ordered by what the task owes, not by what it once was, and the
// order is the whole content of the decision: a task can owe several things at
// once, and continuing it at the wrong one is how work is silently redone or a
// boundary silently crossed.
type resumeLane int

const (
	// laneUnusable is not a lane either, and it comes first because it is the
	// one condition no continuation survives: the task recorded no objective,
	// so there is no work to continue, not even by answering its question --
	// an answer would re-enter a governed path that refuses an empty objective,
	// and the question would have been spent on nothing.
	laneUnusable resumeLane = iota
	// laneQuestion is not a lane. It is the refusal: this task owes a human
	// decision, and only --answer may continue it.
	laneQuestion
	laneReview
	laneBlocked
	// laneArchitecture is a task that never had a plan. What it owes is the
	// architect turn it never took -- under its own task id, objective, recorded
	// answers and candidate binding.
	laneArchitecture
	// laneImplementation is a task that has a plan and a candidate, interrupted
	// somewhere inside the work.
	laneImplementation
)

func (l resumeLane) String() string {
	switch l {
	case laneUnusable:
		return "nothing: it recorded no objective"
	case laneQuestion:
		return "a standing human-owned question"
	case laneReview:
		return "the review its candidate is owed"
	case laneBlocked:
		return "the turn a provider blocked, or the re-plan it did not converge to"
	case laneArchitecture:
		return "the architect turn it never took"
	default:
		return "the implementation of the plan it already has"
	}
}

// selectResumeLane decides, from the durable record alone, what a task named
// without an answer is continued as.
//
// ONE ordered decision, in one place, so no caller can arrange the questions
// differently. The previous routing asked each lane's own selector in turn and
// fell through when none claimed the task, which is how a task that owed only
// its architect turn reached "no interrupted task with that id": every lane
// correctly said "not mine", and nothing said what it was.
//
// The order is: an unusable objective first, because a task that cannot say what
// it is for cannot be continued as anything; then a standing human-owned
// question, always (sensei_code.resume.never_skips_a_human_decision); then the
// review a candidate is already owed, so an accepted candidate is never handed
// back to a worker; then a turn a provider blocked or a re-plan that did not
// converge; then the architect turn an unplanned task never took; and last, the
// implementation of a plan that already exists.
func selectResumeLane(task session.Interrupted, reviewOwed bool) (resumeLane, error) {
	switch {
	case !task.ObjectiveUsable():
		return laneUnusable, fmt.Errorf("%w: %s", errObjectiveUnusable, task.TaskID)
	case len(task.AwaitingAuthority) != 0:
		return laneQuestion, fmt.Errorf("%w: %s", errResumeBehindQuestion, task.TaskID)
	case task.AwaitingReview || reviewOwed:
		return laneReview, nil
	case len(task.BlockedExternal) != 0 || len(task.NotConverged) != 0:
		return laneBlocked, nil
	case !task.Planned:
		return laneArchitecture, nil
	default:
		return laneImplementation, nil
	}
}

// selectBlockedResume decides, from the durable record alone, whether a task is
// to be continued at a turn it is owed: a role turn a provider blocked, or the
// architect re-plan a non-converged task is owed. ok=false with no error means
// the task is not in that lane and the caller should try the next one.
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
		if (len(task.BlockedExternal) == 0 && len(task.NotConverged) == 0) || task.AwaitingReview || reviewOwed {
			return session.Interrupted{}, false, nil
		}
		if len(task.AwaitingAuthority) != 0 {
			return session.Interrupted{}, false, fmt.Errorf("%w: %s", errBlockedBehindQuestion, taskID)
		}
		if len(task.NotConverged) != 0 {
			n, err := workflow.ParseNotConverged(task.NotConverged)
			if err != nil {
				return session.Interrupted{}, false, err
			}
			if n.TaskID != taskID {
				return session.Interrupted{}, false, fmt.Errorf("the non-convergence record is bound to task %s, not %s", n.TaskID, taskID)
			}
			return task, true, nil
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
	return continueTask(ctx, repo, cfg, store, sessionID, target, timeout, asJSON, quiet, func() {
		if n, err := workflow.ParseNotConverged(target.NotConverged); err == nil {
			fmt.Printf("owed      architect re-plan: %s\n", n.Describe())
			return
		}
		block, _ := workflow.ParseExternalBlock(target.BlockedExternal)
		fmt.Printf("blocked   %s\n", block.Describe())
	})
}

// resumeInterruptedWork is `sensei-code resume --task <id>` for a task that owes
// no question, no review and no blocked turn: it was interrupted while it was
// simply being worked on.
//
// Two shapes reach here and BOTH are continuations of the same task. A task with
// a plan owes the implementation of that plan; a task without one owes the
// architect turn it never took. The engine decides which from the same durable
// record this router read, so the decision is made once rather than agreed on by
// two components.
//
// This lane is what task-1789848074761930104 needed and could not reach. Its
// owner had answered its standing question, the answer was recorded, and the
// process died before a plan existed -- so it owed its architect turn, with
// nothing in the record that any earlier lane would claim.
func resumeInterruptedWork(ctx context.Context, repo gitx.Repo, cfg config.Config, store *session.Store,
	sessionID string, target session.Interrupted, lane resumeLane, timeout time.Duration, asJSON, quiet bool) int {
	return continueTask(ctx, repo, cfg, store, sessionID, target, timeout, asJSON, quiet, func() {
		fmt.Printf("owed      %s\n", lane)
		if lane == laneArchitecture {
			fmt.Println("identity  the recorded task id, objective, answered questions and candidate base are " +
				"carried; nothing is minted")
		}
	})
}

// continueTask is the one way this command continues a task it is not answering
// a question for.
//
// Shared deliberately. Every lane needs the same readiness gate, the same event
// bus, the same configured transport and the same settle loop, and a second copy
// of that sequence is a second place for a resumed run to differ from the run it
// continues. Only the account of what is owed differs, so only that is passed in.
func continueTask(ctx context.Context, repo gitx.Repo, cfg config.Config, store *session.Store,
	sessionID string, target session.Interrupted, timeout time.Duration, asJSON, quiet bool, owed func()) int {
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
		fmt.Printf("resuming task %s  session %s\n", target.TaskID, sessionID)
		owed()
	}

	// No answer is carried, so the engine itself is the run control.
	resumedID := engine.Resume(ctx, target)
	return streamUntilSettled(ctx, engine, events, resumedID, asJSON, quiet, timeout)
}

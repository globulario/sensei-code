package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

func blockRecord(t *testing.T, taskID string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(workflow.ExternalBlock{TaskID: taskID, Role: "architect", Provider: "chatgpt",
		Reason: "usageLimitExceeded", RetryAtState: workflow.RetryAtUnknown})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// A blocked turn is its own exit code, never the failure code: a caller that
// reads 1 learns the objective broke, which is what the first dogfood run said.
func TestABlockedTurnExitsWithItsOwnCode(t *testing.T) {
	code, ok := exitFor(event.WorkflowBlockedExternal, false)
	if !ok || code != exitBlockedExternal {
		t.Fatalf("exitFor(blocked external) = %d,%v; want %d", code, ok, exitBlockedExternal)
	}
	for _, other := range []int{exitCompleted, exitFailed, exitUsage, exitAwaitingAuthority, exitStopped,
		exitTimeout, exitObserved, exitAwaitingReview} {
		if other == exitBlockedExternal {
			t.Fatalf("exit code %d is shared with another outcome", other)
		}
	}
	if !terminal(event.WorkflowBlockedExternal) {
		t.Fatal("a quiet run would not print the blocked terminal")
	}
}

// The blocked lane is chosen from the durable record, and never ahead of a
// human question or an owed review.
func TestTheBlockedLaneKeepsItsPrecedence(t *testing.T) {
	blocked := session.Interrupted{TaskID: "t1", Task: "x", BlockedExternal: blockRecord(t, "t1")}

	if got, ok, err := selectBlockedResume([]session.Interrupted{blocked}, "t1", false); err != nil || !ok || got.TaskID != "t1" {
		t.Fatalf("a blocked task was not selected: ok=%v err=%v", ok, err)
	}

	question := blocked
	question.AwaitingAuthority = json.RawMessage(`{"decision":{"options":[{"id":"1"}]}}`)
	if _, ok, err := selectBlockedResume([]session.Interrupted{question}, "t1", false); ok || !errors.Is(err, errBlockedBehindQuestion) {
		t.Fatalf("a blocked task with a standing question was continued past it: ok=%v err=%v", ok, err)
	}

	reviewing := blocked
	reviewing.AwaitingReview = true
	if _, ok, err := selectBlockedResume([]session.Interrupted{reviewing}, "t1", false); ok || err != nil {
		t.Fatalf("a task owing a review was taken by the blocked lane: ok=%v err=%v", ok, err)
	}
	if _, ok, err := selectBlockedResume([]session.Interrupted{blocked}, "t1", true); ok || err != nil {
		t.Fatalf("a task with a durable review obligation was taken by the blocked lane: ok=%v err=%v", ok, err)
	}

	plain := session.Interrupted{TaskID: "t1", Task: "x", Planned: true}
	if _, ok, err := selectBlockedResume([]session.Interrupted{plain}, "t1", false); ok || err != nil {
		t.Fatalf("an unblocked task was taken by the blocked lane: ok=%v err=%v", ok, err)
	}

	elsewhere := session.Interrupted{TaskID: "t1", Task: "x", BlockedExternal: blockRecord(t, "t2")}
	if _, ok, err := selectBlockedResume([]session.Interrupted{elsewhere}, "t1", false); ok || err == nil {
		t.Fatal("a block record bound to another task was accepted")
	}
}

// A non-converged task exits with its own code and is continued through the
// same lane, after a standing question and an owed review.
func TestANotConvergedTaskHasItsOwnExitAndLane(t *testing.T) {
	if code, ok := exitFor(event.WorkflowNotConverged, false); !ok || code != exitNotConverged || code == exitFailed {
		t.Fatalf("exitFor(not converged) = %d,%v; want %d", code, ok, exitNotConverged)
	}
	if !terminal(event.WorkflowNotConverged) {
		t.Fatal("a quiet run would not print the not-converged terminal")
	}
	raw, _ := json.Marshal(workflow.NotConverged{TaskID: "t1", Implementers: []string{"claude", "codex"},
		ReviewCycles: 3, Owed: workflow.OwedArchitectReplan})
	nc := session.Interrupted{TaskID: "t1", Task: "x", Planned: true, NotConverged: raw}
	if got, ok, err := selectBlockedResume([]session.Interrupted{nc}, "t1", false); err != nil || !ok || got.TaskID != "t1" {
		t.Fatalf("a non-converged task was not selected: ok=%v err=%v", ok, err)
	}
	reviewing := nc
	reviewing.AwaitingReview = true
	if _, ok, err := selectBlockedResume([]session.Interrupted{reviewing}, "t1", false); ok || err != nil {
		t.Fatalf("an owed review was passed over for a re-plan: ok=%v err=%v", ok, err)
	}
	other, _ := json.Marshal(workflow.NotConverged{TaskID: "t2", Implementers: []string{"claude"}, ReviewCycles: 3, Owed: workflow.OwedArchitectReplan})
	if _, ok, err := selectBlockedResume([]session.Interrupted{{TaskID: "t1", Task: "x", NotConverged: other}}, "t1", false); ok || err == nil {
		t.Fatal("a non-convergence record bound to another task was accepted")
	}
}

// ROUTING PRECEDENCE, as one ordered decision.
//
// A task can owe several things at once, and which one it is continued at is
// the whole content of this surface. The table strips the facts away one at a
// time, so every case is also the negative control for the one above it: if any
// guard stopped deciding, the lane below it would claim a task it must not.
func TestTheResumeLanesAreOrderedByWhatTheTaskOwes(t *testing.T) {
	nc, err := json.Marshal(workflow.NotConverged{TaskID: "t1", Implementers: []string{"claude"},
		ReviewCycles: 3, Owed: workflow.OwedArchitectReplan})
	if err != nil {
		t.Fatal(err)
	}
	everything := session.Interrupted{
		TaskID:            "t1",
		Task:              "the objective",
		AwaitingAuthority: json.RawMessage(`{"decision":{"options":[{"id":"1"}]}}`),
		AwaitingReview:    true,
		BlockedExternal:   blockRecord(t, "t1"),
		NotConverged:      nc,
		Planned:           true,
	}
	strip := func(f func(*session.Interrupted)) session.Interrupted {
		next := everything
		f(&next)
		return next
	}
	noQuestion := strip(func(i *session.Interrupted) { i.AwaitingAuthority = nil })
	noReview := strip(func(i *session.Interrupted) { i.AwaitingAuthority, i.AwaitingReview = nil, false })
	noBlock := strip(func(i *session.Interrupted) {
		i.AwaitingAuthority, i.AwaitingReview, i.BlockedExternal, i.NotConverged = nil, false, nil, nil
	})
	unplanned := noBlock
	unplanned.Planned = false
	// A task that recorded no objective, owing everything else as well: the
	// refusal has to outrank even a standing question, because answering one
	// re-enters a governed path that refuses an empty objective, and the
	// decision would be spent on nothing.
	unusable := strip(func(i *session.Interrupted) { i.Task = "   " })

	for name, tc := range map[string]struct {
		task       session.Interrupted
		reviewOwed bool
		want       resumeLane
	}{
		"no objective outranks even a question":    {unusable, true, laneUnusable},
		"a standing question outranks everything":  {everything, true, laneQuestion},
		"an owed review outranks a blocked turn":   {noQuestion, false, laneReview},
		"a durable obligation alone owes a review": {noReview, true, laneReview},
		"a blocked turn outranks the work":         {noReview, false, laneBlocked},
		"a task with a plan owes its work":         {noBlock, false, laneImplementation},
		"a task without one owes its architect":    {unplanned, false, laneArchitecture},
	} {
		got, err := selectResumeLane(tc.task, tc.reviewOwed)
		if got != tc.want {
			t.Errorf("%s: lane %v, want %v", name, got, tc.want)
		}
		switch {
		case tc.want == laneUnusable && !errors.Is(err, errObjectiveUnusable):
			t.Errorf("%s: a task that cannot say what it is for was not refused by name: %v", name, err)
		case tc.want == laneQuestion && !errors.Is(err, errResumeBehindQuestion):
			t.Errorf("%s: a task waiting on a person was continued without one: %v", name, err)
		case tc.want != laneQuestion && tc.want != laneUnusable && err != nil:
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The record task-1789848074761930104 left on 2026-09-19: its question answered,
// no plan proposed, nothing blocked. Every lane's own selector correctly said
// "not mine", and before there was an ordered decision that fell through to "no
// interrupted task with that id".
func TestTheAnsweredButUnplannedTaskIsRoutedToItsArchitectTurn(t *testing.T) {
	crashed := session.Interrupted{TaskID: "t-1789848074761930104", Task: "repair the resume path"}
	lane, err := selectResumeLane(crashed, false)
	if err != nil {
		t.Fatalf("the task was refused: %v", err)
	}
	if lane != laneArchitecture {
		t.Fatalf("lane %v; the task owes the architect turn it never took", lane)
	}
	// And no earlier lane claims it, which is what the fall-through relied on.
	if _, ok, err := selectBlockedResume([]session.Interrupted{crashed}, crashed.TaskID, false); ok || err != nil {
		t.Fatalf("the blocked lane claimed a task with no blocked turn: ok=%v err=%v", ok, err)
	}
}

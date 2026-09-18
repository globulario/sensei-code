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

package ghbridge

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

func unansweredReviewFixture(t *testing.T, wait time.Duration) (*Runner, roles.Binding) {
	t.Helper()
	dir, base, tree1, _ := tempRepo(t)
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)
	bare := t.TempDir()
	for _, args := range [][]string{
		{"init", "--bare", "-q", bare},
		{"-C", dir, "remote", "add", "origin", bare},
	} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	runner := &Runner{
		Issue: box, RepoDir: dir, Remote: "origin", NewRequestID: NewRequestID,
		Poll: 10 * time.Millisecond, Wait: wait,
	}
	return runner, roles.Binding{TaskID: "T", BaseSHA: base, CandidateTree: tree1, CandidateDigest: digestC1}
}

// An unanswered review is a typed obligation, not a provider failure, and it
// carries the identity the REQUEST was bound to.
func TestAnUnansweredReviewReturnsTheOwedReviewWithItsBinding(t *testing.T) {
	runner, binding := unansweredReviewFixture(t, 120*time.Millisecond)
	var events []event.Event
	_, err := runner.Run(context.Background(), agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding},
		func(e event.Event) { events = append(events, e) })
	if err == nil {
		t.Fatal("an unanswered review returned success")
	}
	if strings.Contains(err.Error(), "publishing the review snapshot") {
		t.Fatalf("the turn never reached the wait, so the unanswered path was not exercised: %v", err)
	}
	if !errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatalf("the unanswered review is not typed as an owed review: %v", err)
	}
	var owed *roles.ReviewUnanswered
	if !errors.As(err, &owed) {
		t.Fatalf("the owed review carries no identity: %v", err)
	}
	if !strings.HasPrefix(owed.RequestID, "r-") {
		t.Errorf("request id %q is not the published request's", owed.RequestID)
	}
	if owed.Binding != binding {
		t.Errorf("binding %+v is not the binding the request carried %+v", owed.Binding, binding)
	}
	if len(owed.ReviewCommit) != 40 {
		t.Errorf("review commit %q is not a full commit id", owed.ReviewCommit)
	}
	if owed.Conversation != "157" {
		t.Errorf("conversation %q is not the mailbox", owed.Conversation)
	}
	if owed.Waited != 120*time.Millisecond {
		t.Errorf("waited %s is not the runner's own deadline", owed.Waited)
	}
	if !errors.Is(err, ErrNoAnswer) {
		t.Errorf("the transport cause was lost: %v", err)
	}
}

// A cancelled PARENT context -- a human stop, an invocation budget -- is not an
// unanswered review, and must keep its own error.
func TestACancelledReviewTurnIsNotReportedAsAnOwedReview(t *testing.T) {
	runner, binding := unansweredReviewFixture(t, 5*time.Second)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	_, err := runner.Run(ctx, agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}, nil)
	if err == nil {
		t.Fatal("a cancelled turn returned success")
	}
	if strings.Contains(err.Error(), "publishing the review snapshot") {
		t.Fatalf("the turn never reached the wait: %v", err)
	}
	if errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatalf("a cancelled turn was reported as an owed review: %v", err)
	}
}

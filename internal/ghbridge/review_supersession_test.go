package ghbridge

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/roles"
)

// prWithdrawalsIn is withdrawalsIn for the PR-conversation mailbox.
func prWithdrawalsIn(m *prMailbox) []string {
	var out []string
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		if id, err := ParseWithdrawal(body); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// Re-requesting the review of the SAME candidate mints a new request id, keeps
// the candidate identity, and supersedes the old request: it is withdrawn on the
// conversation and closed in the log, so exactly one obligation stands.
func TestARerequestedReviewSupersedesTheOldRequestForTheSameCandidate(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 120*time.Millisecond)
	req := agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}

	_, err := runner.Run(context.Background(), req, nil)
	var first *roles.ReviewUnanswered
	if !errors.As(err, &first) {
		t.Fatalf("the first request did not end as an owed review: %v", err)
	}
	_, err = runner.Run(context.Background(), req, nil)
	var second *roles.ReviewUnanswered
	if !errors.As(err, &second) {
		t.Fatalf("the second request did not end as an owed review: %v", err)
	}

	if second.RequestID == first.RequestID {
		t.Fatalf("the re-request reused request id %q", first.RequestID)
	}
	if second.Binding != first.Binding {
		t.Fatalf("the re-request changed the candidate identity: %+v -> %+v", first.Binding, second.Binding)
	}
	withdrawn := map[string]bool{}
	for _, id := range prWithdrawalsIn(m) {
		withdrawn[id] = true
	}
	if !withdrawn[first.RequestID] || withdrawn[second.RequestID] {
		t.Fatalf("withdrawals %v: want the old request %q withdrawn and the new %q standing",
			prWithdrawalsIn(m), first.RequestID, second.RequestID)
	}
	owed, err := log.PendingReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(owed) != 1 || owed[0].RequestID != second.RequestID {
		t.Fatalf("want exactly the new obligation %q, got %+v", second.RequestID, owed)
	}
}

// An answer bound to an old request does not satisfy a new one, even when it is
// about the identical candidate and arrives inside the new request's window.
func TestAnAnswerToTheOldRequestCannotSatisfyTheNewOne(t *testing.T) {
	m, runner, binding, _ := reviewRunnerWithLog(t, 150*time.Millisecond)
	req := agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}

	_, err := runner.Run(context.Background(), req, nil)
	var first *roles.ReviewUnanswered
	if !errors.As(err, &first) {
		t.Fatalf("the first request did not end as an owed review: %v", err)
	}

	answered := 0
	m.onPost = func(body string) []map[string]any {
		fresh, ok := ParseRequest(body)
		if !ok || fresh.RequestID == first.RequestID {
			return nil
		}
		// The reviewer answers the NEW request's subject under the OLD id.
		stale := canonicalAnswer(t, fresh.Subject, first.RequestID, fresh.ReviewerProvider,
			`{"decision":"accept","summary":"the candidate stands","instructions":"","findings":[]}`)
		answered++
		return []map[string]any{{
			"id":   float64(7100 + answered),
			"body": stale,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}

	_, err = runner.Run(context.Background(), req, nil)
	if answered != 1 {
		t.Fatalf("the stale answer was posted %d times, so the refusal proves nothing", answered)
	}
	var second *roles.ReviewUnanswered
	if !errors.As(err, &second) {
		t.Fatalf("an answer to request %q satisfied a different request: err=%v", first.RequestID, err)
	}
}

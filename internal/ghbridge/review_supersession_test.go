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

// The SAME candidate is not requested again. A waiter ending is not a reason to
// mint anything.
//
// BEHAVIOUR CHANGED IN #182 R4, and this is the change. The old law was "a
// re-request mints a new request id and supersedes the old one", so a candidate
// nobody had touched was asked about twice, under two identities, with two wake
// targets, and the reviewer saw a second question about bytes they were already
// looking at. The obligation is durable now: a second waiter reattaches to the
// request that is already standing.
func TestASecondWaiterReattachesToTheStandingRequest(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 120*time.Millisecond)
	req := agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}

	_, err := runner.Run(context.Background(), req, nil)
	var first *roles.ReviewUnanswered
	if !errors.As(err, &first) {
		t.Fatalf("the first waiter did not end as an owed review: %v", err)
	}
	requests := func() []string {
		var out []string
		for _, c := range m.comments {
			body, _ := c["body"].(string)
			if r, ok := ParseRequest(body); ok {
				out = append(out, r.RequestID)
			}
		}
		return out
	}
	published := len(requests())

	// Two more waiters on the same candidate.
	for i := 0; i < 2; i++ {
		_, err = runner.Run(context.Background(), req, nil)
		var again *roles.ReviewUnanswered
		if !errors.As(err, &again) {
			t.Fatalf("waiter %d did not end as an owed review: %v", i+2, err)
		}
		if again.RequestID != first.RequestID {
			t.Fatalf("waiter %d minted request %q; the obligation is %q", i+2, again.RequestID, first.RequestID)
		}
		if again.Binding != first.Binding {
			t.Fatalf("waiter %d changed the candidate: %+v -> %+v", i+2, first.Binding, again.Binding)
		}
		if again.ReviewCommit != first.ReviewCommit {
			t.Fatalf("waiter %d changed the review projection: %s -> %s", i+2, first.ReviewCommit, again.ReviewCommit)
		}
		if again.RequestComment != first.RequestComment {
			t.Fatalf("waiter %d changed the wake target: %d -> %d", i+2, first.RequestComment, again.RequestComment)
		}
	}

	if n := len(requests()); n != published {
		t.Fatalf("%d review requests were published across three waiters, want %d", n, published)
	}
	if w := prWithdrawalsIn(m); len(w) != 0 {
		t.Fatalf("a waiter ending withdrew something: %v", w)
	}
	owed, err := log.PendingReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(owed) != 1 || owed[0].RequestID != first.RequestID {
		t.Fatalf("after three waiters the task owes %+v, want exactly request %s", owed, first.RequestID)
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

	moved := req.Binding
	moved.CandidateDigest = "sha256:candidate-two"
	_, err = runner.Run(context.Background(), agent.Request{
		Role: roles.Reviewer, TaskID: req.TaskID, Binding: moved}, nil)
	if answered != 1 {
		t.Fatalf("the stale answer was posted %d times, so the refusal proves nothing", answered)
	}
	var second *roles.ReviewUnanswered
	if !errors.As(err, &second) {
		t.Fatalf("an answer to request %q satisfied a different request: err=%v", first.RequestID, err)
	}
}

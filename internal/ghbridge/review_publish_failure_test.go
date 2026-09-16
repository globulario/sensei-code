package ghbridge

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/roles"
)

// Re-requesting a review has three failure points between "an obligation is
// owed" and "its replacement is established": the snapshot, the request, and the
// durable record. Retiring the old obligation first left a window at each one
// where the task owed a review that NOTHING named -- and the failure looked like
// an ordinary reviewer error, so the ladder was free to ask somebody else.
//
// The property under test is the one the whole repair exists for:
//
//	a transport failure is not a vanished obligation
//	a transport failure is not permission to change reviewer
//
// So at every failure point at least one durable obligation must stand, nothing
// may be withdrawn, and the turn must end as an OWED REVIEW rather than as a
// reviewer that failed.

// owedAfterFirstRequest leaves one standing obligation and returns it.
func owedAfterFirstRequest(t *testing.T, m *prMailbox, runner *Runner, req agent.Request, log ExchangeLog) *roles.ReviewUnanswered {
	t.Helper()
	_, err := runner.Run(context.Background(), req, nil)
	var first *roles.ReviewUnanswered
	if !errors.As(err, &first) {
		t.Fatalf("the first request did not end as an owed review: %v", err)
	}
	if owed, _ := log.PendingReviews(); len(owed) != 1 {
		t.Fatalf("want one standing obligation before the failure, got %+v", owed)
	}
	if w := prWithdrawalsIn(m); len(w) != 0 {
		t.Fatalf("something was withdrawn before the failure: %v", w)
	}
	return first
}

func TestAFailedRereqestKeepsTheObligationItCouldNotReplace(t *testing.T) {
	for name, brk := range map[string]func(t *testing.T, m *prMailbox, r *Runner, log ExchangeLog){
		// The snapshot cannot be pushed.
		"snapshot": func(_ *testing.T, _ *prMailbox, r *Runner, _ ExchangeLog) { r.Remote = "no-such-remote" },
		// The request cannot be posted.
		"request": func(_ *testing.T, m *prMailbox, _ *Runner, _ ExchangeLog) { m.failPosts = true },
	} {
		t.Run(name, func(t *testing.T) {
			m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
			req := agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}
			first := owedAfterFirstRequest(t, m, runner, req, log)

			brk(t, m, runner, log)
			_, err := runner.Run(context.Background(), req, nil)

			// An owed review, not a reviewer failure: the engine preserves the
			// candidate instead of the ladder asking a different reviewer.
			var owed *roles.ReviewUnanswered
			if !errors.As(err, &owed) {
				t.Fatalf("a failed re-request was reported as an ordinary reviewer error: %v", err)
			}
			if owed.RequestID != first.RequestID {
				t.Fatalf("the owed review names %q; the obligation that still stands is %q", owed.RequestID, first.RequestID)
			}
			if owed.Binding != first.Binding {
				t.Fatalf("the owed review changed candidate: %+v -> %+v", first.Binding, owed.Binding)
			}
			// The reason travels on the typed carrier: ReviewUnanswered renders its
			// own summary, and what a later reader needs is WHY the replacement
			// never happened.
			if owed.Cause == nil || !strings.Contains(owed.Cause.Error(), "could not be replaced") {
				t.Errorf("the owed review does not carry why its replacement failed: %v", owed.Cause)
			}

			// The durable obligation survives, and nothing was retracted.
			standing, listErr := log.PendingReviews()
			if listErr != nil {
				t.Fatal(listErr)
			}
			if len(standing) != 1 || standing[0].RequestID != first.RequestID {
				t.Fatalf("the obligation did not survive the failure: %+v", standing)
			}
			if w := prWithdrawalsIn(m); len(w) != 0 {
				t.Fatalf("the old obligation was withdrawn with no replacement established: %v", w)
			}
		})
	}
}

// The last failure point: the replacement is published and its record cannot be
// written. The published request is real but nothing durable names it, so the
// obligation it was meant to replace is the only durable one -- and it stands.
func TestAnUnrecordableReplacementDoesNotRetireTheObligationItReplaces(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	req := agent.Request{Role: roles.Reviewer, TaskID: "T", Binding: binding}
	first := owedAfterFirstRequest(t, m, runner, req, log)

	// The log can be read and not written, which is exactly the state in which
	// retiring the old record would leave nothing behind.
	if err := os.Chmod(log.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(log.Dir, 0o700) })

	_, err := runner.Run(context.Background(), req, nil)
	if err == nil {
		t.Fatal("the turn reported success though its replacement was never recorded")
	}
	standing, listErr := log.PendingReviews()
	if listErr != nil {
		t.Fatal(listErr)
	}
	if len(standing) != 1 || standing[0].RequestID != first.RequestID {
		t.Fatalf("the only durable obligation was retired: %+v", standing)
	}
	if w := prWithdrawalsIn(m); len(w) != 0 {
		t.Fatalf("an obligation was withdrawn though its replacement was never recorded: %v", w)
	}
}

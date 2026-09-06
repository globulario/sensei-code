package ghbridge

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A mailbox read failure is not "no answer yet". Polling through a gh auth
// failure or a network outage would turn a broken mailbox into a silent
// timeout, and the turn would eventually fail for a reason unrelated to the
// real one.

// badDir points gh at a directory that is not a repository, so `gh api
// repos/{owner}/{repo}/...` cannot resolve and fails immediately.
func TestMailboxReadFailureIsImmediateAndExplicit(t *testing.T) {
	box := Issue{Dir: t.TempDir(), Number: "156",
		ExpectedReviewer: Principal{UserID: gptUserID}}

	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	_, err := AwaitReview(ctx, box, reqC1(), time.Second)
	if err == nil {
		t.Fatal("a broken mailbox returned success")
	}
	if time.Since(start) > 15*time.Second {
		t.Errorf("a read failure was hidden behind polling for %s", time.Since(start))
	}
	if strings.Contains(err.Error(), ErrNoAnswer.Error()) {
		t.Errorf("a read failure was reported as 'nobody answered': %v", err)
	}
	if !strings.Contains(err.Error(), "reading the review mailbox") {
		t.Errorf("the error should name the failing operation: %v", err)
	}
}

// An unusable mailbox is refused before any network call.
func TestUnconfiguredMailboxIsRefusedNotPolled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Reviews(ctx, Issue{Dir: ".", Number: "156"}); err == nil {
		t.Fatal("reading a mailbox with no expected reviewer succeeded")
	}
	if err := PostRequest(ctx, Issue{Dir: "."}, reqC1(), ""); err == nil {
		t.Fatal("posting to a mailbox with no number succeeded")
	}
}

// A cancelled context reports cancellation, not a mailbox fault.
func TestCancellationIsReportedAsCancellation(t *testing.T) {
	box := Issue{Dir: t.TempDir(), Number: "156",
		ExpectedReviewer: Principal{UserID: gptUserID}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := AwaitReview(ctx, box, reqC1(), time.Second)
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "context canceled") {
		t.Errorf("cancellation was reported as something else: %v", err)
	}
}

// The wait is bounded by construction. A zero Wait must not mean "forever".
func TestDefaultWaitIsFinite(t *testing.T) {
	if DefaultWait <= 0 {
		t.Fatal("the default wait is not finite")
	}
	if DefaultWait > time.Hour {
		t.Errorf("DefaultWait = %s, longer than a turn should ever block", DefaultWait)
	}
}

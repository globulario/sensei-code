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

	_, err := AwaitReview(ctx, box, obligationC1(box), time.Second)
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
	if _, err := Reviews(ctx, Issue{Dir: ".", Number: "156"}, Principal{}); err == nil {
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
	_, err := AwaitReview(ctx, box, obligationC1(box), time.Second)
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

// A REFUSAL IS TERMINAL FOR ONE EXACT REQUEST, AND ONLY ONCE.
//
// W6 IDEMPOTENCE. A wake can be rung twice, a consumer can retry, and the same
// refusal can therefore appear in the conversation more than once. The first
// valid bound copy settles the request; the later ones are inert.
//
// The sharp assertion is not that the wait ends -- one copy would prove that --
// but that N copies produce ONE settlement, naming the first of them, with no
// copy reported as a rejection. An implementation that treated the second copy
// as a competing or unusable refusal would fail here rather than in production.
func TestDuplicateRefusalsSettleARequestExactlyOnce(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	wire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	const copies = 3
	for i := 0; i < copies; i++ {
		m.append(map[string]any{
			"id": float64(9100 + i), "body": wire,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	answer, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
	if err == nil {
		t.Fatalf("a refused request returned an answer: %+v", answer)
	}
	refused, ok := err.(*ArchitectureRefused)
	if !ok {
		t.Fatalf("a refusal did not end the wait as a refusal: %v", err)
	}
	if answer.Body != "" {
		t.Errorf("a refusal produced an architecture body: %q", answer.Body)
	}
	if refused.Comment != 9100 {
		t.Errorf("the settlement is comment %d, want the FIRST copy 9100", refused.Comment)
	}
	if refused.Reason != refusal.Reason {
		t.Errorf("the settled reason is %q, want the consumer's own %q", refused.Reason, refusal.Reason)
	}
	if len(refused.Rejected) != 0 {
		t.Errorf("a duplicate copy was reported as a rejection rather than being inert: %+v", refused.Rejected)
	}

	// The observation sees all three copies and still settles once, on the first.
	obs, err := ObserveArchitecture(ctx, box, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Refusals) != copies {
		t.Fatalf("the mailbox holds %d valid copies, the observation found %d; the fixture "+
			"is not exercising duplication", copies, len(obs.Refusals))
	}
	settled, ok := obs.Settlement()
	if !ok || settled.Comment != 9100 {
		t.Errorf("%d copies settled as %+v, want exactly one settlement on comment 9100", copies, settled)
	}
}

// W8 -- THE CONTROL THAT MATTERS MOST, at the waiter.
//
// A responder that NEVER emits the new prefix behaves exactly as it did before
// it existed: an answer still discharges the wait with its body verbatim, and an
// unanswered request still times out with ErrNoArchitectureAnswer and nothing
// else. This project has a recorded scar in which a grammar changed, the
// responder was never told, and messages went unrecognised for days.
func TestALegacyResponderIsUnaffectedByTheRefusalGrammar(t *testing.T) {
	req, _ := architectureRefusalFixture()

	t.Run("an answer still discharges the wait", func(t *testing.T) {
		keyPath, _ := writeTestKey(t)
		m, box := newPRMailbox(t, keyPath, "157", true)
		const body = `{"decision":"proceed","summary":"bounded","plan":"do it"}`
		wire, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID, Body: body}.Marker()
		if err != nil {
			t.Fatal(err)
		}
		m.append(map[string]any{
			"id": float64(9200), "body": wire,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		answer, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
		if err != nil {
			t.Fatalf("an ordinary answer no longer discharges the wait: %v", err)
		}
		if answer.Body != body {
			t.Errorf("the answer body changed:\n got %q\nwant %q", answer.Body, body)
		}
		if !answer.Answers(req) {
			t.Error("the answer no longer answers its own request")
		}
	})

	t.Run("silence still times out as it did", func(t *testing.T) {
		keyPath, _ := writeTestKey(t)
		_, box := newPRMailbox(t, keyPath, "157", true)
		ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
		defer cancel()
		_, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
		if err == nil {
			t.Fatal("an unanswered request returned success")
		}
		if !strings.Contains(err.Error(), ErrNoArchitectureAnswer.Error()) {
			t.Fatalf("silence is no longer reported as no answer: %v", err)
		}
		if strings.Contains(err.Error(), "refusal-shaped") || strings.Contains(err.Error(), "refused") {
			t.Errorf("a conversation containing no refusal reported one: %v", err)
		}
	})
}

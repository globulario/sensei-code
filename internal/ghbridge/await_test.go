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
	if len(obs.Terminals) != copies {
		t.Fatalf("the mailbox holds %d valid copies, the observation found %d; the fixture "+
			"is not exercising duplication", copies, len(obs.Terminals))
	}
	settled, ok := obs.Settlement()
	if !ok || settled.Comment != 9100 || !settled.Refused() {
		t.Errorf("%d copies settled as %+v, want exactly one refusal settlement on comment 9100",
			copies, settled)
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

// POSITIONAL FRAMING W6 -- ORDER PRESERVED, BOTH DIRECTIONS.
//
// THE EARLIEST VALID TERMINAL GOVERNS, and the later conflicting artifact is
// inert. Proving one direction proves nothing: a reader that always preferred
// refusals would pass the refusal-first case, and a reader that always preferred
// answers -- which is what this repairs -- would pass the answer-first case.
//
// THE DEFECT. The observation separated answers from refusals into two lists and
// so discarded the order they shared, and the waiter scanned EVERY answer before
// it looked at any refusal. A valid refusal settled the exchange, an answer
// arrived before the next poll, and the turn returned SUCCESS on a request the
// consumer had already refused. The reverse ordering has the same shape: a late
// refusal must not overturn an answer that already discharged the wait.
//
// Order is taken from the conversation, never from the clock. The two comments
// below carry the SAME created_at on purpose: a reader that broke the tie by
// timestamp would settle this request differently depending on which second the
// consumer happened to post in, and GitHub guarantees comment order rather than
// clock separation.
func TestTheEarliestTerminalGovernsInBothDirections(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	refusalWire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	const answerBody = `{"decision":"proceed","summary":"bounded","plan":"do it"}`
	answerWire, err := ArchitectureResponse{
		Binding: req.Binding, RequestID: req.RequestID, Body: answerBody,
	}.Marker()
	if err != nil {
		t.Fatal(err)
	}

	for name, tc := range map[string]struct {
		first, second string
		wantRefused   bool
	}{
		"a refusal settles before a later answer":  {refusalWire, answerWire, true},
		"an answer settles before a later refusal": {answerWire, refusalWire, false},
	} {
		t.Run(name, func(t *testing.T) {
			keyPath, _ := writeTestKey(t)
			m, box := newPRMailbox(t, keyPath, "157", true)
			// One snapshot, both artifacts, identical timestamps.
			const sameMoment = "2026-09-24T21:14:00Z"
			m.append(map[string]any{
				"id": float64(9300), "body": tc.first, "created_at": sameMoment,
				"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
			})
			m.append(map[string]any{
				"id": float64(9301), "body": tc.second, "created_at": sameMoment,
				"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
			})

			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			// BOTH are exact-bound terminals, so the fixture really does pose
			// the ordering question rather than answering it by absence.
			obs, oerr := ObserveArchitecture(ctx, box, req)
			if oerr != nil {
				t.Fatal(oerr)
			}
			if len(obs.Terminals) != 2 {
				t.Fatalf("the snapshot holds %d exact-bound terminals, want 2; the fixture "+
					"does not pose the ordering question: %+v", len(obs.Terminals), obs.Terminals)
			}
			settled, ok := obs.Settlement()
			if !ok || settled.Comment != 9300 {
				t.Fatalf("settlement is %+v, want the FIRST comment 9300", settled)
			}
			if settled.Refused() != tc.wantRefused {
				t.Fatalf("the settlement is refused=%v, want %v", settled.Refused(), tc.wantRefused)
			}
			// The later conflicting artifact is INERT, and observable as such:
			// still in the snapshot, not the settlement, and not a rejection.
			if obs.Terminals[1].Comment != 9301 || obs.Terminals[1].Refused() == tc.wantRefused {
				t.Fatalf("the later conflicting artifact is not observable as inert: %+v", obs.Terminals[1])
			}
			if len(obs.Rejected) != 0 {
				t.Errorf("a later conflicting artifact was reported as unusable: %+v", obs.Rejected)
			}

			// AND THE WAITER AGREES. The observation deciding correctly while
			// the waiter still scanned answers first is exactly the shape of the
			// defect, so the decision is proved where it is acted on.
			answer, werr := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
			if !tc.wantRefused {
				if werr != nil {
					t.Fatalf("an answer that arrived first did not discharge the wait: %v", werr)
				}
				if answer.Body != answerBody {
					t.Errorf("the answer body changed:\n got %q\nwant %q", answer.Body, answerBody)
				}
				return
			}
			if werr == nil {
				t.Fatalf("a request refused BEFORE the answer returned success: %+v", answer)
			}
			refused, isRefusal := werr.(*ArchitectureRefused)
			if !isRefusal {
				t.Fatalf("a settled refusal did not end the wait as a refusal: %v", werr)
			}
			if refused.Comment != 9300 || refused.Reason != refusal.Reason {
				t.Errorf("the settlement is not the first refusal: %+v", refused)
			}
			if answer.Body != "" {
				t.Errorf("a refused request produced an architecture body: %q", answer.Body)
			}
		})
	}
}

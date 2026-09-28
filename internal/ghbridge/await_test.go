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
// refusal can therefore appear in the conversation more than once. Identical
// copies are one terminal (P7); the settlement names the least locator among
// byte-identical copies, which here is also the first.
//
// The sharp assertion is not that the wait ends -- one copy would prove that --
// but that N copies produce ONE settlement, with no copy reported as a
// rejection or a conflict. An implementation that treated the second copy
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

// P7 AT THE ARCHITECTURE WAIT -- MATERIALLY DIFFERENT TERMINALS CONFLICT, IN
// BOTH DIRECTIONS, AND NO COMMENT ORDER CHOOSES BETWEEN THEM.
//
// This replaces POSITIONAL FRAMING W6, "the earliest valid terminal governs".
// W6 prevented the right thing -- an answer and a refusal both being acted on,
// which is how a refused request once returned SUCCESS -- by choosing a winner
// by comment order. Measured 2026-09-27 (r-93f1aec046bd0217): two answers to one
// request, and the run proceeded on whichever came first. The review wait
// called the same shape CONFLICT. One package, two rules; P7 is one.
//
// Order still never comes from the clock: the comments carry the SAME
// created_at, and now order does not decide anything either -- both orders
// must produce the same outcome.
func TestMateriallyDifferentArchitectureTerminalsConflictWithNoWinner(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	refusalWire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	otherRefusal := refusal
	otherRefusal.Reason = refusal.Reason + " -- and the base could not be read either"
	otherRefusalWire, err := otherRefusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	answer := func(body string) string {
		t.Helper()
		wire, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID, Body: body}.Marker()
		if err != nil {
			t.Fatal(err)
		}
		return wire
	}
	proceed := answer(`{"decision":"proceed","summary":"bounded","plan":"do it"}`)
	proceedOtherPlan := answer(`{"decision":"proceed","summary":"bounded","plan":"do something else"}`)

	for name, pair := range map[string][2]string{
		"a refusal and an answer":             {refusalWire, proceed},
		"an answer and a refusal":             {proceed, refusalWire},
		"two answers with different plans":    {proceed, proceedOtherPlan},
		"two refusals with different reasons": {refusalWire, otherRefusalWire},
	} {
		t.Run(name, func(t *testing.T) {
			keyPath, _ := writeTestKey(t)
			m, box := newPRMailbox(t, keyPath, "157", true)
			const sameMoment = "2026-09-24T21:14:00Z"
			m.append(map[string]any{
				"id": float64(9300), "body": pair[0], "created_at": sameMoment,
				"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
			})
			m.append(map[string]any{
				"id": float64(9301), "body": pair[1], "created_at": sameMoment,
				"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
			})
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			// BOTH are exact-bound terminals, so the fixture poses the question
			// rather than answering it by absence.
			obs, oerr := ObserveArchitecture(ctx, box, req)
			if oerr != nil {
				t.Fatal(oerr)
			}
			if len(obs.Terminals) != 2 || len(obs.Rejected) != 0 {
				t.Fatalf("the snapshot holds %d terminals and %d rejections, want 2 and 0",
					len(obs.Terminals), len(obs.Rejected))
			}
			if settled, ok := obs.Settlement(); ok {
				t.Fatalf("materially different terminals settled on comment %d", settled.Comment)
			}

			// AND THE WAITER AGREES: no answer, no refusal, a conflict naming both.
			got, werr := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
			if werr == nil {
				t.Fatalf("the waiter chose an answer between different terminals: %q", got.Body)
			}
			if got.Body != "" {
				t.Errorf("a conflict produced an architecture body: %q", got.Body)
			}
			if _, refused := werr.(*ArchitectureRefused); refused {
				t.Fatalf("the waiter chose the refusal between different terminals: %v", werr)
			}
			conflict, ok := werr.(*ArchitectureConflict)
			if !ok {
				t.Fatalf("err = %v, want an architecture conflict", werr)
			}
			if len(conflict.Terminals) != 2 || conflict.Terminals[0].Comment != 9300 ||
				conflict.Terminals[1].Comment != 9301 {
				t.Fatalf("the conflict did not retain both terminals: %+v", conflict.Terminals)
			}
			if strings.Contains(werr.Error(), ErrNoArchitectureAnswer.Error()) {
				t.Errorf("a conflict was reported as nobody answering: %v", werr)
			}
		})
	}
}

// The control: exact duplicate ANSWERS are one settlement, not a conflict.
// Duplicate refusals are TestDuplicateRefusalsSettleARequestExactlyOnce.
func TestDuplicateArchitectureAnswersSettleOnce(t *testing.T) {
	req, _ := architectureRefusalFixture()
	const body = `{"decision":"proceed","summary":"bounded","plan":"do it"}`
	wire, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID, Body: body}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	for _, id := range []float64{9401, 9400} {
		m.append(map[string]any{
			"id": id, "body": wire,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	obs, err := ObserveArchitecture(ctx, box, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Terminals) != 2 {
		t.Fatalf("the observation found %d copies, want 2; the fixture is not exercising duplication",
			len(obs.Terminals))
	}
	// Byte-identical copies: the least locator names the settlement, whatever
	// the conversation order -- here the later-listed comment.
	settled, ok := obs.Settlement()
	if !ok || settled.Refused() || settled.Comment != 9400 {
		t.Fatalf("two identical answers settled as ok=%v %+v, want one answer on comment 9400", ok, settled)
	}
	got, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("an answer posted twice did not discharge the wait: %v", err)
	}
	if got.Body != body {
		t.Errorf("the answer body changed:\n got %q\nwant %q", got.Body, body)
	}
}

// One principal, one identity, on the architecture side too: the same answer
// from the same configured user id under a renamed login is ONE terminal, not a
// conflict with itself. The control -- a different canonical principal -- cannot
// pass the pinned principal's Matches in one mailbox, so it is proved at settle.
func TestOneArchitectPrincipalCannotConflictWithItself(t *testing.T) {
	req, _ := architectureRefusalFixture()
	const body = `{"decision":"proceed","summary":"bounded","plan":"do it"}`
	wire, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID, Body: body}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	if box.ExpectedReviewer.UserID != 1697116 {
		t.Fatalf("the mailbox pins %v, want user id 1697116; the fixture does not pose the question",
			box.ExpectedReviewer)
	}
	for id, login := range map[float64]string{9500: "davecourtois", 9501: "dave-renamed"} {
		m.append(map[string]any{
			"id": id, "body": wire,
			"user": map[string]any{"login": login, "id": float64(1697116)},
		})
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	obs, err := ObserveArchitecture(ctx, box, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Terminals) != 2 || obs.Terminals[0].Answer.Author == obs.Terminals[1].Answer.Author {
		t.Fatalf("the fixture is not two copies under two logins: %+v", obs.Terminals)
	}
	if settled, ok := obs.Settlement(); !ok || settled.Comment != 9500 {
		t.Fatalf("one principal under two logins settled as ok=%v %+v, want one answer on comment 9500", ok, settled)
	}
	if got, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond); err != nil || got.Body != body {
		t.Fatalf("one principal under two logins did not discharge the wait: %q, %v", got.Body, err)
	}

	// THE CONTROL: a different canonical principal is a different terminal.
	other := obs
	other.Terminals = append([]ArchitectureTerminal(nil), obs.Terminals...)
	other.Terminals[1].principal = "id:424242"
	if settled, ok := other.Settlement(); ok {
		t.Fatalf("two principals' answers settled on comment %d", settled.Comment)
	}
}

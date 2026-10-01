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

// DF-26 -- AN AUTHENTICATED RESPONSE-SHAPED ARTIFACT IS A VALID TERMINAL, A
// NAMED REJECTION, OR BOUND ELSEWHERE. IT IS NEVER SILENTLY ORDINARY CONTENT.
//
// MEASURED 2026-10-01 (r-c4891d244802fb2b, comment 5926376268): a correctly
// bound decision=escalate answer omitted the single empty line between
// graph_build_commit and the JSON. The strict parser refused it, the observer
// skipped it with no record, and the waiter reported "unanswered" after 30
// minutes. Parsing stays strict; these witnesses pin that the refusal is now
// visible and named.

const df26EscalateBody = `{"decision":"escalate","summary":"the pinned base is not on the workspace remote","plan":""}`

// df26MeasuredShape renders the measured bytes: the canonical answer with the
// header/payload empty line removed, and the canonical answer itself.
func df26MeasuredShape(t *testing.T, req ArchitectureRequest) (malformed, wellFormed string) {
	t.Helper()
	wellFormed, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID, Body: df26EscalateBody}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	sep := "graph_build_commit=" + req.Binding.GraphBuildCommit + "\n\n"
	if strings.Count(wellFormed, sep) != 1 {
		t.Fatalf("the canonical answer does not end its header with exactly one empty line: %q", wellFormed)
	}
	malformed = strings.Replace(wellFormed, sep, "graph_build_commit="+req.Binding.GraphBuildCommit+"\n", 1)
	return malformed, wellFormed
}

// df26Bounded is a wait bound for a witness whose subject should end at once,
// so a broken guard fails the witness instead of hanging it.
func df26Bounded(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// df26Mailbox posts bodies as the AUTHENTICATED consumer, in order, with ids
// from first.
func df26Mailbox(t *testing.T, first int, bodies ...string) Issue {
	t.Helper()
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	for i, body := range bodies {
		m.append(map[string]any{
			"id": float64(first + i), "body": body,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		})
	}
	return box
}

// W1 -- the exact 2026-10-01 shape ends the wait AT ONCE as a named malformed
// answer for that request: not an answer, not a refusal, not "unanswered".
func TestDF26W1AMalformedBoundAnswerEndsTheWaitByName(t *testing.T) {
	req, _ := architectureRefusalFixture()
	malformed, wellFormed := df26MeasuredShape(t, req)

	// PREMISE: the bytes are refused by the strict grammar, and one inserted
	// newline is the whole difference.
	if _, ok := ParseArchitectureResponse(malformed); ok {
		t.Fatal("the measured bytes parse, so this fixture poses no question")
	}
	if a, ok := ParseArchitectureResponse(wellFormed); !ok || !a.Answers(req) {
		t.Fatal("the same bytes with the empty line restored do not answer the request")
	}

	box := df26Mailbox(t, 5926376268, malformed)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	obs, err := ObserveArchitecture(ctx, box, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Terminals) != 1 || !obs.Terminals[0].IsMalformed() || obs.Terminals[0].Answer != nil ||
		obs.Terminals[0].Refusal != nil {
		t.Fatalf("the measured shape is not one malformed terminal: %+v", obs.Terminals)
	}
	if len(obs.Unusable) != 0 || len(obs.Rejected) != 0 {
		t.Fatalf("an exact-bound malformed answer was also recorded as unattributed: %+v %+v",
			obs.Unusable, obs.Rejected)
	}

	start := time.Now()
	answer, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the wait sat for %s instead of ending at once", elapsed)
	}
	if err == nil {
		t.Fatalf("a malformed answer was returned as an answer: %+v", answer)
	}
	if answer.Body != "" {
		t.Errorf("a malformed answer produced an architecture body: %q", answer.Body)
	}
	got, ok := err.(*ArchitectureAnswerMalformed)
	if !ok {
		t.Fatalf("err = %v, want a named malformed-answer outcome", err)
	}
	if got.Comment != 5926376268 || got.RequestID != req.RequestID || !got.Binding.Same(req.Binding) {
		t.Errorf("the malformed answer is not attributed to the exact comment and request: %+v", got)
	}
	if !strings.Contains(got.Diagnostic, "payload boundary") {
		t.Errorf("the diagnostic does not name the grammar defect: %q", got.Diagnostic)
	}
	if strings.Contains(err.Error(), ErrNoArchitectureAnswer.Error()) {
		t.Errorf("a delivered answer was reported as unanswered: %v", err)
	}
	if _, refused := err.(*ArchitectureRefused); refused || strings.Contains(err.Error(), ErrArchitectureRefused.Error()) {
		t.Errorf("a malformed answer was reported as a refusal: %v", err)
	}
	if !strings.Contains(err.Error(), ErrArchitectureAnswerMalformed.Error()) {
		t.Errorf("the outcome is not stated by name: %v", err)
	}
}

// W2 -- CONTROL: the same body with the empty line present is the normal
// answer terminal.
func TestDF26W2TheSameAnswerWellFormedIsTheNormalTerminal(t *testing.T) {
	req, _ := architectureRefusalFixture()
	_, wellFormed := df26MeasuredShape(t, req)
	box := df26Mailbox(t, 9600, wellFormed)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	answer, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
	if err != nil {
		t.Fatalf("a well formed bound answer did not discharge the wait: %v", err)
	}
	if answer.Body != df26EscalateBody || !answer.Answers(req) {
		t.Fatalf("the answer changed: %+v", answer)
	}
	if len(answer.Unusable) != 0 || len(answer.Rejected) != 0 {
		t.Errorf("a clean conversation reported diagnostics: %+v %+v", answer.Unusable, answer.Rejected)
	}
}

// W3 -- a well formed answer bound to a DIFFERENT request is a recorded
// wrong-target observation; the wait continues and the diagnostic travels with
// whatever ends it.
func TestDF26W3AWellFormedAnswerBoundElsewhereIsRecordedAndTheWaitContinues(t *testing.T) {
	req, _ := architectureRefusalFixture()
	elsewhere, err := ArchitectureResponse{Binding: req.Binding, RequestID: "r-00000000deadbeef",
		Body: df26EscalateBody}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	box := df26Mailbox(t, 9610, elsewhere)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	obs, err := ObserveArchitecture(ctx, box, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Terminals) != 0 {
		t.Fatalf("an answer bound elsewhere settled this request: %+v", obs.Terminals)
	}
	if len(obs.Unusable) != 1 || obs.Unusable[0].Comment != 9610 ||
		!strings.Contains(obs.Unusable[0].Diagnostic, "bound elsewhere") ||
		!strings.Contains(obs.Unusable[0].Diagnostic, "r-00000000deadbeef") {
		t.Fatalf("the wrong-target answer was not recorded by name: %+v", obs.Unusable)
	}

	short, cancelShort := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancelShort()
	_, werr := AwaitArchitecture(short, box, req, 10*time.Millisecond)
	if werr == nil || !strings.Contains(werr.Error(), ErrNoArchitectureAnswer.Error()) {
		t.Fatalf("the wait did not continue to its bound: %v", werr)
	}
	if !strings.Contains(werr.Error(), "comment 9610") || !strings.Contains(werr.Error(), "bound elsewhere") {
		t.Errorf("the wrong-target diagnostic did not travel with the ending: %v", werr)
	}
	if _, malformed := werr.(*ArchitectureAnswerMalformed); malformed {
		t.Errorf("a well formed answer bound elsewhere was reported as malformed: %v", werr)
	}
}

// W4 -- a marker-at-zero body whose identity cannot be read WITHOUT REPAIR is
// recorded and attributed to no request. Every case is a body that a forgiving
// recognizer could have bound to this request; none of them may be.
func TestDF26W4AnUnidentifiableMalformedResponseIsRecordedAndNotAttributed(t *testing.T) {
	req, _ := architectureRefusalFixture()
	_, wellFormed := df26MeasuredShape(t, req)
	line := func(key, value string) string { return key + "=" + value + "\n" }
	b := req.Binding
	header := func(lines ...string) string {
		return architectureResponseMarker + "\n" + strings.Join(lines, "")
	}
	task, request := line("task", b.TaskID), line("request", req.RequestID)
	digest, base := line("objective_digest", b.ObjectiveDigest), line("base", b.BaseSHA)
	graphRepo, graphCommit := line("graph_repository", b.GraphRepository), line("graph_build_commit", b.GraphBuildCommit)

	for name, body := range map[string]string{
		"request id missing": header(task, digest, base, graphRepo, graphCommit) + df26EscalateBody,
		"request id displaced before task": header(request, task, digest, base, graphRepo, graphCommit) +
			df26EscalateBody,
		"request id duplicated after the header": header(task, request, digest, base, graphRepo, graphCommit) +
			line("request", req.RequestID) + df26EscalateBody,
		"request id spaced around its delimiter": header(task, "request = "+req.RequestID+"\n", digest, base,
			graphRepo, graphCommit) + df26EscalateBody,
		"a malformed line skipped, the field searched for later": header(task, "request\n", digest, base,
			graphRepo, graphCommit, request) + df26EscalateBody,
		"no delimiter after the marker": architectureResponseMarker + " " +
			strings.TrimPrefix(header(task, request, digest, base, graphRepo, graphCommit),
				architectureResponseMarker+"\n") + df26EscalateBody,
		"CRLF delimiters with the payload line missing": strings.ReplaceAll(
			header(task, request, digest, base, graphRepo, graphCommit), "\n", "\r\n") + df26EscalateBody,
		"header truncated before graph_build_commit": header(task, request, digest, base, graphRepo),
		"a binding field half stated":                header(task, request, digest, base, graphCommit) + df26EscalateBody,
	} {
		t.Run(name, func(t *testing.T) {
			if _, ok := ParseArchitectureResponse(body); ok {
				t.Fatalf("the fixture parses, so it poses no question: %q", body)
			}
			box := df26Mailbox(t, 9620, body)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			obs, err := ObserveArchitecture(ctx, box, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.Terminals) != 0 {
				t.Fatalf("an unidentifiable malformed response was attributed to the waiting request: %+v",
					obs.Terminals)
			}
			if len(obs.Unusable) != 1 || obs.Unusable[0].Comment != 9620 ||
				!strings.Contains(obs.Unusable[0].Diagnostic, "attributed to no request") {
				t.Fatalf("the malformed response was not recorded as unattributed: %+v", obs.Unusable)
			}
			short, cancelShort := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancelShort()
			_, werr := AwaitArchitecture(short, box, req, 10*time.Millisecond)
			if werr == nil || !strings.Contains(werr.Error(), ErrNoArchitectureAnswer.Error()) ||
				!strings.Contains(werr.Error(), "attributed to no request") {
				t.Fatalf("the wait did not continue with the diagnostic carried: %v", werr)
			}
		})
	}

	// A malformed response whose identity IS complete but names another request
	// is likewise recorded and not attributed.
	t.Run("a malformed response bound elsewhere", func(t *testing.T) {
		other := req
		other.RequestID = "r-00000000deadbeef"
		malformed, _ := df26MeasuredShape(t, other)
		box := df26Mailbox(t, 9630, malformed)
		obs, err := ObserveArchitecture(context.Background(), box, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(obs.Terminals) != 0 || len(obs.Unusable) != 1 ||
			!strings.Contains(obs.Unusable[0].Diagnostic, "bound elsewhere") {
			t.Fatalf("a malformed response for another request was not recorded as bound elsewhere: %+v %+v",
				obs.Terminals, obs.Unusable)
		}
	})

	// CONTROL: prose that merely mentions the marker is ordinary content.
	t.Run("prose mentioning the marker", func(t *testing.T) {
		box := df26Mailbox(t, 9640, "see this:\n"+wellFormed)
		obs, err := ObserveArchitecture(context.Background(), box, req)
		if err != nil {
			t.Fatal(err)
		}
		if len(obs.Terminals) != 0 || len(obs.Unusable) != 0 || len(obs.Rejected) != 0 {
			t.Fatalf("ordinary content was classified as an artifact: %+v", obs)
		}
	})
}

// W5 -- an answer-shaped body from a principal that is not the expected one is
// ignored exactly as at base: no terminal, no diagnostic, malformed or not.
func TestDF26W5AResponseFromAnotherPrincipalIsIgnored(t *testing.T) {
	req, _ := architectureRefusalFixture()
	malformed, wellFormed := df26MeasuredShape(t, req)
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	for i, body := range []string{malformed, wellFormed} {
		m.append(map[string]any{
			"id": float64(9650 + i), "body": body,
			"user": map[string]any{"login": "someone-else", "id": float64(424242)},
		})
	}
	obs, err := ObserveArchitecture(context.Background(), box, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Terminals) != 0 || len(obs.Unusable) != 0 || len(obs.Rejected) != 0 {
		t.Fatalf("an unauthenticated party's artifact was classified: %+v", obs)
	}
}

// W6 -- a refusal-shaped comment is read by the refusal grammar only: a
// malformed refusal is a rejected REFUSAL and never a malformed answer. The
// existing refusal tests are the unchanged control for everything else.
func TestDF26W6ARefusalIsStillReadOnlyAsARefusal(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	wire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	broken := strings.Replace(wire, "\n\n", "\n", 1)
	box := df26Mailbox(t, 9660, broken)
	obs, err := ObserveArchitecture(context.Background(), box, req)
	if err != nil {
		t.Fatal(err)
	}
	if len(obs.Terminals) != 0 || len(obs.Unusable) != 0 || len(obs.Rejected) != 1 {
		t.Fatalf("a malformed refusal was not classified exactly as at base: %+v", obs)
	}
}

// W7 -- a valid exact-bound answer and a malformed exact-bound answer are
// materially different terminals: CONFLICT, in both orders, with neither
// chosen. The malformed terminal is never converted into an answer or refusal.
func TestDF26W7AValidAndAMalformedBoundAnswerConflictInEitherOrder(t *testing.T) {
	req, _ := architectureRefusalFixture()
	malformed, wellFormed := df26MeasuredShape(t, req)
	for name, pair := range map[string][2]string{
		"malformed first": {malformed, wellFormed},
		"valid first":     {wellFormed, malformed},
	} {
		t.Run(name, func(t *testing.T) {
			box := df26Mailbox(t, 9700, pair[0], pair[1])
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			obs, err := ObserveArchitecture(ctx, box, req)
			if err != nil {
				t.Fatal(err)
			}
			if len(obs.Terminals) != 2 {
				t.Fatalf("the snapshot holds %d terminals, want 2", len(obs.Terminals))
			}
			if settled, ok := obs.Settlement(); ok {
				t.Fatalf("a valid and a malformed answer settled on comment %d", settled.Comment)
			}
			got, werr := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
			if werr == nil || got.Body != "" {
				t.Fatalf("the waiter chose an answer: %q, %v", got.Body, werr)
			}
			conflict, ok := werr.(*ArchitectureConflict)
			if !ok {
				t.Fatalf("err = %v, want an architecture conflict", werr)
			}
			if len(conflict.Terminals) != 2 || conflict.Terminals[0].Comment != 9700 ||
				conflict.Terminals[1].Comment != 9701 {
				t.Fatalf("the conflict did not retain both terminals: %+v", conflict.Terminals)
			}
			if !strings.Contains(werr.Error(), "malformed answer") {
				t.Errorf("the conflict does not name the malformed terminal: %v", werr)
			}
			for _, term := range conflict.Terminals {
				if term.IsMalformed() && (term.Answer != nil || term.Refusal != nil) {
					t.Errorf("the malformed terminal carries an answer or refusal: %+v", term)
				}
			}
		})
	}

	// Identical malformed copies are one terminal, not a conflict with itself.
	t.Run("duplicate malformed copies settle once", func(t *testing.T) {
		box := df26Mailbox(t, 9710, malformed, malformed)
		_, werr := AwaitArchitecture(df26Bounded(t), box, req, 10*time.Millisecond)
		got, ok := werr.(*ArchitectureAnswerMalformed)
		if !ok || got.Comment != 9710 {
			t.Fatalf("two identical malformed answers did not settle once on 9710: %v", werr)
		}
	})
}

// W8 -- the published request carries a literal scaffold rendered by the
// canonical response renderer, and filling its payload slot yields bytes the
// strict parser accepts and binds to that exact request.
func TestDF26W8TheRequestCarriesTheCanonicalResponseScaffold(t *testing.T) {
	req, _ := architectureRefusalFixture()
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	if _, err := PublishArchitectureRequest(context.Background(), box, req); err != nil {
		t.Fatal(err)
	}
	snapshot := m.snapshot()
	if len(snapshot) != 1 {
		t.Fatalf("published %d comments, want 1", len(snapshot))
	}
	published, _ := snapshot[0]["body"].(string)

	scaffold, err := ArchitectureResponseScaffold(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(published, scaffold) {
		t.Fatalf("the published request does not carry the literal scaffold:\n%s", published)
	}
	// The scaffold IS the canonical rendering with the payload slot in place
	// of a payload: same marker, same field order, same exactly-one empty line.
	filled := strings.Replace(scaffold, ArchitecturePayloadPlaceholder, df26EscalateBody, 1)
	canonical, err := ArchitectureResponse{Binding: req.Binding, RequestID: req.RequestID,
		Body: df26EscalateBody}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	if filled != canonical {
		t.Fatalf("the filled scaffold is not the canonical response:\n got %q\nwant %q", filled, canonical)
	}
	answer, ok := ParseArchitectureResponse(filled)
	if !ok || !answer.Answers(req) || answer.Body != df26EscalateBody {
		t.Fatalf("the filled scaffold does not parse and bind to its request: ok=%v %+v", ok, answer)
	}
	// And the request grammar is unchanged: the published body still reads as
	// this exact request.
	back, ok := ParseArchitectureRequest(published)
	if !ok || back.RequestID != req.RequestID || !back.Binding.Same(req.Binding) {
		t.Fatalf("the published request no longer parses to itself: ok=%v %+v", ok, back)
	}
}

// ENDING DIAGNOSTICS (ruling 47) -- every terminal path carries the
// response-shaped diagnostics that preceded it: an answer, a refusal, a
// malformed answer and a conflict alike.
func TestDF26EveryEndingCarriesTheAccumulatedDiagnostics(t *testing.T) {
	req, refusal := architectureRefusalFixture()
	malformed, wellFormed := df26MeasuredShape(t, req)
	refusalWire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	unattributed := architectureResponseMarker + "\nnot an identity\n\n{}"
	brokenRefusal := strings.Replace(refusalWire, "\n\n", "\n", 1)

	check := func(t *testing.T, unusable []ArchitectureResponseRejected, rejected []ArchitectureRefusalRejected) {
		t.Helper()
		if len(unusable) != 1 || unusable[0].Comment != 9800 {
			t.Errorf("the response diagnostic was erased by the ending: %+v", unusable)
		}
		if len(rejected) != 1 || rejected[0].Comment != 9801 {
			t.Errorf("the refusal diagnostic was erased by the ending: %+v", rejected)
		}
	}

	t.Run("a successful answer", func(t *testing.T) {
		box := df26Mailbox(t, 9800, unattributed, brokenRefusal, wellFormed)
		answer, err := AwaitArchitecture(df26Bounded(t), box, req, 10*time.Millisecond)
		if err != nil {
			t.Fatal(err)
		}
		check(t, answer.Unusable, answer.Rejected)
	})
	t.Run("a refusal", func(t *testing.T) {
		box := df26Mailbox(t, 9800, unattributed, brokenRefusal, refusalWire)
		_, err := AwaitArchitecture(df26Bounded(t), box, req, 10*time.Millisecond)
		refused, ok := err.(*ArchitectureRefused)
		if !ok {
			t.Fatalf("err = %v, want a refusal", err)
		}
		check(t, refused.Unusable, refused.Rejected)
		if !strings.Contains(err.Error(), "comment 9800") {
			t.Errorf("the refusal's message dropped the response diagnostic: %v", err)
		}
	})
	t.Run("a malformed answer", func(t *testing.T) {
		box := df26Mailbox(t, 9800, unattributed, brokenRefusal, malformed)
		_, err := AwaitArchitecture(df26Bounded(t), box, req, 10*time.Millisecond)
		got, ok := err.(*ArchitectureAnswerMalformed)
		if !ok {
			t.Fatalf("err = %v, want a malformed answer", err)
		}
		check(t, got.Unusable, got.Rejected)
	})
	t.Run("a conflict", func(t *testing.T) {
		box := df26Mailbox(t, 9800, unattributed, brokenRefusal, malformed, wellFormed)
		_, err := AwaitArchitecture(df26Bounded(t), box, req, 10*time.Millisecond)
		conflict, ok := err.(*ArchitectureConflict)
		if !ok {
			t.Fatalf("err = %v, want a conflict", err)
		}
		check(t, conflict.Unusable, conflict.Rejected)
	})
}

// ENDING DIAGNOSTICS, the error half: a LIVE mailbox read error that is not the
// deadline still carries what earlier polls recorded. The second read is made
// unreadable by a comment GitHub could never return, so the failure is a
// genuine read error and not a context ending.
func TestDF26AMailboxReadErrorCarriesTheAccumulatedDiagnostics(t *testing.T) {
	req, _ := architectureRefusalFixture()
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	m.append(map[string]any{
		"id": float64(9900), "body": architectureResponseMarker + "\nnot an identity\n\n{}",
		"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
	})
	m.onCommentsGet = func(_ context.Context, read int32) {
		if read == 2 {
			m.append(map[string]any{"id": float64(9901), "body": "x", "user": "not an object"})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := AwaitArchitecture(ctx, box, req, 10*time.Millisecond)
	if err == nil {
		t.Fatal("an unreadable mailbox returned success")
	}
	if ctx.Err() != nil || strings.Contains(err.Error(), ErrNoArchitectureAnswer.Error()) {
		t.Fatalf("the fixture ended on the deadline, not on a read error: %v", err)
	}
	if !strings.Contains(err.Error(), "reading the architecture mailbox") {
		t.Fatalf("the ending is not the read error: %v", err)
	}
	if !strings.Contains(err.Error(), "comment 9900") || !strings.Contains(err.Error(), "attributed to no request") {
		t.Errorf("the read error discarded the accumulated diagnostic: %v", err)
	}
}

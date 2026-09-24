package ghbridge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/roles"
)

// obligationC1 is the standing obligation the transport-level witnesses wait
// on: the shared candidate fixture, published to the mailbox's own principal.
func obligationC1(box Issue) ReviewObligation {
	s := subjC1()
	return ReviewObligation{
		TaskID: s.TaskID, RequestID: "r-1", RequestComment: 1,
		Conversation: box.Number, BaseSHA: s.BaseSHA,
		CandidateDigest: s.CandidateDigest, CandidateTree: s.CandidateTree,
		ReviewCommit: s.ReviewCommit, ReviewerProvider: "chatgpt",
		ExpectedReviewer: box.ExpectedReviewer,
	}
}

// ---------------------------------------------------------------------------
// OBSERVATION WINDOW AND CLASSIFICATION
// ---------------------------------------------------------------------------

func observedObligation() ReviewObligation {
	return ReviewObligation{
		TaskID: relaySubject.TaskID, RequestID: relayRequest, RequestComment: 5000,
		Conversation: "157", PublishedAt: time.Unix(1700000000, 0).UTC(),
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
		ReviewerProvider: "chatgpt",
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
}

func comment(id int64, authorID int64, login, body string) restComment {
	c := restComment{ID: id, Body: body, CreatedAt: time.Unix(1700000500, 0).UTC()}
	c.User.Login = login
	c.User.ID = authorID
	return c
}

// canonicalFor renders a canonical artifact for a subject/request/provider.
func canonicalFor(t *testing.T, s Subject, request, provider, body string) string {
	t.Helper()
	raw, err := reviewartifact.Artifact{
		ReviewerProvider: provider, TaskID: s.TaskID, RequestID: request,
		BaseSHA: s.BaseSHA, CandidateDigest: s.CandidateDigest,
		CandidateTree: s.CandidateTree, ReviewCommit: s.ReviewCommit, Body: body,
	}.Render()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Only the pinned principal, only after the request, and only content that is
// not a known object of another kind.
//
// An unauthenticated party must not be able to make Sensei-Code report that its
// reviewer answered badly, and the machine's own bookkeeping must not be read
// as the reviewer replying.
func TestIrrelevantContentNeverEntersTheObservationSet(t *testing.T) {
	o := observedObligation()
	perfect := canonicalFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	for name, c := range map[string]restComment{
		"a perfect artifact from another account": comment(6000, 424242, "someone-else", perfect),
		"the pinned principal before the request": comment(4000, 1697116, "davecourtois", "LGTM"),
		"the request itself":                      comment(6001, 1697116, "davecourtois", "[sensei-code:review-request]\nrequest=r-x\n"),
		"a withdrawal":                            comment(6002, 1697116, "davecourtois", WithdrawnMarker+"\nrequest=r-x\n"),
		"a wake":                                  comment(6003, 1697116, "davecourtois", WakeMarker+"\nrequest=r-x\n"),
		"a relay receipt":                         comment(6004, 1697116, "davecourtois", relayedReviewMarker+"\nrequest=r-x\n"),
		"an attestation":                          comment(6005, 1697116, "davecourtois", attestationMarker+"\nrequest=r-x\n"),
		"an architecture turn":                    comment(6006, 1697116, "davecourtois", architectureResponseMarker+"\nobjective=x\n"),
	} {
		t.Run(name, func(t *testing.T) {
			// The precondition each case actually rests on, asserted rather than
			// assumed: authentication, or the window, or the marker.
			switch {
			case c.User.ID != 1697116:
				if o.ExpectedReviewer.Matches(c.User.ID, c.User.Login) {
					t.Fatal("this fixture authenticates, so it proves nothing about the principal")
				}
			case c.ID <= o.RequestComment:
				if inResponseWindow(o, c) {
					t.Fatal("this fixture is in the window, so it proves nothing about the boundary")
				}
			default:
				if !inResponseWindow(o, c) || !o.ExpectedReviewer.Matches(c.User.ID, c.User.Login) {
					t.Fatal("this fixture is not an in-window pinned-principal comment, so it proves " +
						"nothing about protocol-marker handling")
				}
			}
			if c.User.ID == 1697116 && inResponseWindow(o, c) {
				if _, ok := classify(o, c); ok {
					t.Fatalf("a known object of another kind was classified as a review observation")
				}
			}
		})
	}
}

// What the pinned principal actually said, named exactly.
func TestReviewerContentIsClassifiedByWhatItIs(t *testing.T) {
	o := observedObligation()
	other := Subject{TaskID: relaySubject.TaskID, BaseSHA: strings.Repeat("b", 40),
		CandidateDigest: "sha256:" + strings.Repeat("0", 64), CandidateTree: strings.Repeat("a", 40),
		ReviewCommit: strings.Repeat("c", 40)}

	cases := map[string]struct {
		body     string
		kind     string
		mismatch string
	}{
		// The historical specimen that motivated #182: the reviewer's own
		// account posting bare JSON with no envelope at all.
		"bare reviewer json":   {acceptPayload, roles.ObservedMalformed, ""},
		"plain prose":          {"LGTM, ship it", roles.ObservedMalformed, ""},
		"a truncated envelope": {reviewartifact.Marker + "\ntask=" + relaySubject.TaskID + "\n", roles.ObservedMalformed, ""},
		"an exact review": {canonicalFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload),
			boundCanonical, ""},
		"a review of another request": {canonicalFor(t, relaySubject, "r-ffffffffffffffff", "chatgpt", acceptPayload),
			roles.ObservedWrongTarget, "request"},
		"a review of another candidate": {canonicalFor(t, other, relayRequest, "chatgpt", acceptPayload),
			roles.ObservedWrongTarget, "base"},
		"a review from another provider": {canonicalFor(t, relaySubject, relayRequest, "claude", acceptPayload),
			roles.ObservedWrongTarget, "reviewer_provider"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			raw := comment(6100, 1697116, "davecourtois", c.body)
			obs, ok := classify(o, raw)
			if !ok {
				t.Fatal("reviewer-origin content in the response window was discarded")
			}
			if obs.kind != c.kind {
				t.Fatalf("classified as %s, want %s (%s)", obs.kind, c.kind, obs.diagnostic)
			}
			// The locator and the exact bytes travel with every observation, so
			// an operator can go and look at the thing that was seen.
			if obs.comment != 6100 || obs.bodyDigest != bodyDigestOf(c.body) {
				t.Fatalf("the observation does not name what was seen: %+v", obs)
			}
			switch c.kind {
			case roles.ObservedMalformed:
				// Malformed bytes acquire no semantic fields they never proved.
				if obs.artifact != nil {
					t.Fatalf("malformed bytes were given artifact identity: %+v", obs.artifact)
				}
				if strings.TrimSpace(obs.diagnostic) == "" {
					t.Fatal("a malformed observation does not say why it could not be read")
				}
			case roles.ObservedWrongTarget:
				if obs.artifact == nil {
					t.Fatal("a wrong-target observation must come from a successful canonical parse")
				}
				if !strings.Contains(obs.mismatch, c.mismatch) {
					t.Fatalf("mismatch is %q, want it to name %q", obs.mismatch, c.mismatch)
				}
			case boundCanonical:
				if obs.artifact == nil || obs.mismatch != "" {
					t.Fatalf("an exact review was reported with a mismatch: %+v", obs)
				}
			}
		})
	}
}

// Only an EMPTY relevant-observation set may become "no answer".
func TestOnlySilenceBecomesNoAnswer(t *testing.T) {
	o := observedObligation()
	malformed, _ := classify(o, comment(6200, 1697116, "davecourtois", acceptPayload))
	stale, _ := classify(o, comment(6201, 1697116, "davecourtois",
		canonicalFor(t, relaySubject, "r-ffffffffffffffff", "chatgpt", acceptPayload)))

	t.Run("nothing observed", func(t *testing.T) {
		var seen observationSet
		if !seen.empty() {
			t.Fatal("the fixture is not empty, so it proves nothing")
		}
		err := waitEnded(o, &seen, context.DeadlineExceeded)
		if !errors.Is(err, ErrNoAnswer) {
			t.Fatalf("err = %v, want ErrNoAnswer", err)
		}
		var observed *roles.ReviewObservationFault
		if errors.As(err, &observed) {
			t.Fatalf("silence was reported as an observation fault: %+v", observed)
		}
	})

	for name, obs := range map[string]observation{
		"malformed content was observed": malformed,
		"a stale review was observed":    stale,
	} {
		t.Run(name, func(t *testing.T) {
			var seen observationSet
			seen.add(obs)
			if seen.empty() {
				t.Fatal("the fixture observed nothing, so it proves nothing")
			}
			err := waitEnded(o, &seen, context.DeadlineExceeded)
			if errors.Is(err, ErrNoAnswer) {
				t.Fatalf("observed evidence was reported as nobody answering: %v", err)
			}
			var observed *roles.ReviewObservationFault
			if !errors.As(err, &observed) {
				t.Fatalf("err = %v, want an observation fault", err)
			}
			if !observed.Has(obs.kind) {
				t.Fatalf("observed %v, want %s", observed.Kinds(), obs.kind)
			}
		})
	}
}

// Repeated polls of the same content are one observation; two different exact
// reviews for one request are a conflict.
func TestAccumulationDedupesAndConflictsOnlyOnDistinctAnswers(t *testing.T) {
	o := observedObligation()
	first := canonicalFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	second := canonicalFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"revise","summary":"actually no","instructions":"redo","findings":[]}`)

	t.Run("the same comment on ten polls is one answer", func(t *testing.T) {
		var seen observationSet
		c := comment(6300, 1697116, "davecourtois", first)
		for i := 0; i < 10; i++ {
			obs, ok := classify(o, c)
			if !ok {
				t.Fatal("the exact review was discarded")
			}
			seen.add(obs)
		}
		if n := len(seen.bound()); n != 1 {
			t.Fatalf("%d distinct answers from one comment seen ten times", n)
		}
	})

	t.Run("two different exact reviews conflict", func(t *testing.T) {
		var seen observationSet
		for i, body := range []string{first, second} {
			obs, ok := classify(o, comment(int64(6400+i), 1697116, "davecourtois", body))
			if !ok || obs.kind != boundCanonical {
				t.Fatalf("fixture %d is %v, want two exact-bound reviews", i, obs.kind)
			}
			seen.add(obs)
		}
		bound := seen.bound()
		if len(bound) != 2 {
			t.Fatalf("%d distinct exact answers, want 2", len(bound))
		}
		fault := observationFault(o, conflictFrom(bound))
		if !fault.Has(roles.ObservedConflict) {
			t.Fatalf("two answers were not reported as a conflict: %v", fault.Kinds())
		}
		// No first, newest or comment-order winner.
		if len(fault.Observations) != 2 {
			t.Fatalf("the conflict kept %d observations, want both", len(fault.Observations))
		}
	})

	t.Run("an earlier fault does not block a corrected review", func(t *testing.T) {
		var seen observationSet
		bad, _ := classify(o, comment(6500, 1697116, "davecourtois", "LGTM"))
		seen.add(bad)
		good, _ := classify(o, comment(6501, 1697116, "davecourtois", first))
		seen.add(good)
		if n := len(seen.bound()); n != 1 {
			t.Fatalf("%d exact answers after a correction, want 1", n)
		}
		if len(seen.faults()) != 1 {
			t.Fatal("the earlier malformed observation was forgotten rather than kept as diagnostic")
		}
	})
}

// The ReviewStore conflict is an observation, not a review that failed to
// arrive.
//
// A different canonical review already answers this request. Saying "no answer"
// would be false twice over: one answer is recorded and another was just seen.
//
// Driven at the ingestion seam rather than through Run, because through Run the
// stored review is CONSUMED before the mailbox is read -- reaching this branch
// in a whole turn needs a concurrent writer landing between those two steps.
// The seam is the exact branch under test, and the test states that plainly
// instead of dressing a race up as a scenario.
func TestAReviewStoreConflictIsObservedAsAConflict(t *testing.T) {
	f := newRelayFixture(t)
	first := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(first); err != nil {
		t.Fatal(err)
	}
	stored, found, err := f.reviews.Load(relayRequest)
	if err != nil || !found {
		t.Fatalf("the first review is not recorded: found=%v err=%v", found, err)
	}
	path := filepath.Join(f.reviews.Dir, relayRequest+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// A DIFFERENT exact-bound canonical review, observed on the mailbox.
	rivalRaw := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"a different summary entirely","instructions":"","findings":[]}`)
	if ReviewDigest(rivalRaw) == stored.ReviewDigest {
		t.Fatal("the two artifacts are the same bytes, so the conflict proves nothing")
	}
	rival, err := reviewartifact.Parse(rivalRaw)
	if err != nil {
		t.Fatal(err)
	}
	obligation := obligationFrom(soleObligation(t, f.exchanges))
	observedReview := MailboxReview{Artifact: rival, Author: "davecourtois", AuthorID: 1697116, Comment: 7777}

	_, runErr := f.runner().dischargeWith(obligation, f.turn(), observedReview, nil)

	var observed *roles.ReviewObservationFault
	if !errors.As(runErr, &observed) {
		t.Fatalf("err = %v, want a conflict observation", runErr)
	}
	if !observed.Has(roles.ObservedConflict) {
		t.Fatalf("observed %v, want CONFLICT", observed.Kinds())
	}
	if errors.Is(runErr, roles.ErrReviewUnanswered) {
		t.Fatalf("a conflict was reported as nobody answering: %v", runErr)
	}
	// It names both digests, which is what an operator needs to act.
	var named bool
	for _, o := range observed.Observations {
		if strings.Contains(o.Mismatch, rival.Digest) && strings.Contains(o.Mismatch, stored.ReviewDigest) {
			named = true
		}
		if o.Comment != 7777 {
			t.Errorf("the conflict does not name where the rival was seen: %+v", o)
		}
	}
	if !named {
		t.Fatalf("the conflict names neither the stored nor the observed digest: %+v", observed.Observations)
	}

	// The recorded review is untouched and the rival is not stored.
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("a conflicting observation changed the recorded review")
	}
	again, _, err := f.reviews.Load(relayRequest)
	if err != nil {
		t.Fatal(err)
	}
	if again.ReviewDigest != stored.ReviewDigest {
		t.Fatalf("the stored review became %s, want the original %s", again.ReviewDigest, stored.ReviewDigest)
	}
	// And the obligation is not discharged by a conflicting artifact.
	if owed, _ := f.exchanges.PendingReviews(); len(owed) != 1 {
		t.Fatalf("a conflict discharged the obligation: %+v", owed)
	}
}

// The same bytes redelivered stay idempotent and discharge normally.
//
// The companion to the conflict case: only a DIFFERENT digest conflicts, and
// this proves the conflict branch is not simply firing on every redelivery.
func TestTheSameReviewRedeliveredIsNotAConflict(t *testing.T) {
	f := newRelayFixture(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(raw); err != nil {
		t.Fatal(err)
	}
	art, err := reviewartifact.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	obligation := obligationFrom(soleObligation(t, f.exchanges))

	res, err := f.runner().dischargeWith(obligation, f.turn(),
		MailboxReview{Artifact: art, Author: "davecourtois", AuthorID: 1697116, Comment: 7778}, nil)
	if err != nil {
		t.Fatalf("redelivering the same review conflicted: %v", err)
	}
	if res.ReviewDigest != art.Digest {
		t.Fatalf("consumed %s, want %s", res.ReviewDigest, art.Digest)
	}
	if owed, _ := f.exchanges.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the obligation was not discharged: %+v", owed)
	}
}

// Structural: reviewer-origin content is never silently dropped.
//
// The defect this slice removes was a bare `continue` on a parse failure and a
// filter that discarded valid artifacts for other subjects. Both made real
// evidence disappear into "no answer".
func TestTheScannerDropsNoReviewerOriginContent(t *testing.T) {
	blob, err := os.ReadFile("review_observation.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(blob)
	start := strings.Index(src, "func classify(")
	if start < 0 {
		t.Fatal("classify was not found; this check proves nothing")
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	// Exactly one path returns "not a review response", and it is the known
	// other-protocol-object path.
	if n := strings.Count(body, "return observation{}, false"); n != 1 {
		t.Errorf("classify has %d discard paths, want exactly the protocol-marker one", n)
	}
	if !strings.Contains(body, "otherProtocolObject(") {
		t.Error("classify's only discard path is not the known-other-object path")
	}
	// A parse failure becomes an observation rather than a skip.
	if !strings.Contains(body, "roles.ObservedMalformed") {
		t.Error("classify does not report a parse failure as an observation")
	}
	if !strings.Contains(body, "roles.ObservedWrongTarget") {
		t.Error("classify does not report a valid artifact for another subject")
	}
}

// Structural: NO_RESPONSE can only be built from an empty observation set.
func TestNoAnswerIsConstructedOnlyFromSilence(t *testing.T) {
	blob, err := os.ReadFile("transport.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(blob)
	// Every ErrNoAnswer in this package's wait path is inside waitEnded, after
	// the fault check.
	start := strings.Index(src, "func waitEnded(")
	if start < 0 {
		t.Fatal("waitEnded was not found; this check proves nothing")
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	if !strings.Contains(body, "seen.faults()") {
		t.Error("waitEnded does not consult the observation set before reporting silence")
	}
	faultIdx := strings.Index(body, "observationFault(")
	noAnswerIdx := strings.Index(body, "ErrNoAnswer")
	if faultIdx < 0 || noAnswerIdx < 0 || faultIdx > noAnswerIdx {
		t.Error("waitEnded reports silence before checking what was observed")
	}
}

// waitOn runs a waiter against a mailbox already holding its content.
func waitOn(t *testing.T, f relayFixture, o ReviewObligation) (MailboxReview, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	return AwaitReview(ctx, f.box, o, 10*time.Millisecond)
}

// The WAITER's decision, not just the set's: two distinct answers conflict, a
// repeated answer does not, and an earlier fault does not block a correction.
//
// The accumulation tests above prove the set; these prove what AwaitReview does
// with it. A mutation that made the waiter take the first of two answers, or
// return the earlier fault instead of a later correction, survived the set-level
// tests entirely.
func TestTheWaiterDecidesOnDistinctAnswersNotOnOrder(t *testing.T) {
	exact := func(t *testing.T, summary string) string {
		t.Helper()
		return artifactFor(t, relaySubject, relayRequest, "chatgpt",
			`{"decision":"accept","summary":"`+summary+`","instructions":"","findings":[]}`)
	}

	t.Run("two different answers conflict, with no winner", func(t *testing.T) {
		f := newRelayFixture(t)
		o := obligationFrom(soleObligation(t, f.exchanges))
		o.RequestComment = 1 // every fixture comment is posted after this locator
		first, second := exact(t, "the first verdict"), exact(t, "the second verdict")
		if ReviewDigest(first) == ReviewDigest(second) {
			t.Fatal("the fixtures are the same bytes, so a conflict proves nothing")
		}
		f.mailbox.add(first, "davecourtois", 1697116)
		f.mailbox.add(second, "davecourtois", 1697116)

		res, err := waitOn(t, f, o)
		if err == nil {
			t.Fatalf("the waiter chose between two answers: %s", res.Artifact.Digest)
		}
		var observed *roles.ReviewObservationFault
		if !errors.As(err, &observed) || !observed.Has(roles.ObservedConflict) {
			t.Fatalf("err = %v, want a CONFLICT observation", err)
		}
		if len(observed.Observations) != 2 {
			t.Fatalf("the conflict kept %d observations, want both answers", len(observed.Observations))
		}
	})

	t.Run("the same answer seen twice is not a conflict", func(t *testing.T) {
		f := newRelayFixture(t)
		o := obligationFrom(soleObligation(t, f.exchanges))
		o.RequestComment = 1
		raw := exact(t, "the only verdict")
		// The SAME bytes posted twice: two comments, one answer.
		f.mailbox.add(raw, "davecourtois", 1697116)
		f.mailbox.add(raw, "davecourtois", 1697116)

		res, err := waitOn(t, f, o)
		if err != nil {
			t.Fatalf("one answer posted twice was reported as a problem: %v", err)
		}
		if res.Artifact.Digest != ReviewDigest(raw) {
			t.Fatalf("returned %s, want %s", res.Artifact.Digest, ReviewDigest(raw))
		}
	})

	t.Run("an earlier fault does not block a correction", func(t *testing.T) {
		f := newRelayFixture(t)
		o := obligationFrom(soleObligation(t, f.exchanges))
		o.RequestComment = 1
		// The reviewer replies badly, then correctly.
		f.mailbox.add("LGTM, ship it", "davecourtois", 1697116)
		corrected := exact(t, "the corrected verdict")
		f.mailbox.add(corrected, "davecourtois", 1697116)

		res, err := waitOn(t, f, o)
		if err != nil {
			t.Fatalf("a corrected review was blocked by the earlier malformed one: %v", err)
		}
		if res.Artifact.Digest != ReviewDigest(corrected) {
			t.Fatalf("returned %s, want the correction %s", res.Artifact.Digest, ReviewDigest(corrected))
		}
	})

	t.Run("only faults ends as an observation fault", func(t *testing.T) {
		f := newRelayFixture(t)
		o := obligationFrom(soleObligation(t, f.exchanges))
		o.RequestComment = 1
		f.mailbox.add("LGTM, ship it", "davecourtois", 1697116)

		_, err := waitOn(t, f, o)
		var observed *roles.ReviewObservationFault
		if !errors.As(err, &observed) || !observed.Has(roles.ObservedMalformed) {
			t.Fatalf("err = %v, want a MALFORMED observation", err)
		}
		if errors.Is(err, ErrNoAnswer) {
			t.Fatalf("observed evidence was reported as nobody answering: %v", err)
		}
	})

	t.Run("nothing at all ends as no answer", func(t *testing.T) {
		f := newRelayFixture(t)
		o := obligationFrom(soleObligation(t, f.exchanges))
		o.RequestComment = 1
		_, err := waitOn(t, f, o)
		if !errors.Is(err, ErrNoAnswer) {
			t.Fatalf("err = %v, want ErrNoAnswer", err)
		}
		var observed *roles.ReviewObservationFault
		if errors.As(err, &observed) {
			t.Fatalf("silence was reported as an observation fault: %+v", observed)
		}
	})
}

// A protocol object is recognised by BEING one, not by mentioning one.
//
// Substring matching excluded far more than it meant to. Reviewer prose that
// merely quoted a marker vanished entirely, and -- worse -- a review-shaped
// comment carrying a second protocol marker was discarded before
// reviewartifact.Parse could say why it was unreadable. Both then decayed into
// NO_RESPONSE, which is the exact conflation this slice exists to remove.
func TestOnlyATopLevelProtocolObjectIsExcluded(t *testing.T) {
	o := observedObligation()

	t.Run("a top-level wake is still excluded", func(t *testing.T) {
		c := comment(6700, 1697116, "davecourtois", WakeMarker+"\nrequest=r-x\n")
		if _, ok := classify(o, c); ok {
			t.Fatal("a wake was classified as a review observation")
		}
	})

	// Each of these is in-window content from the pinned reviewer principal that
	// is NOT itself another protocol object. None may disappear.
	//
	// WHAT EACH ONE IS differs, and that is the point of naming the wanted kind
	// rather than expecting MALFORMED for all of them. Prose that quotes a
	// marker is reviewer content that cannot be read as a review. A VALID
	// CANONICAL REVIEW whose payload quotes a marker is a review -- it was
	// expected to be MALFORMED here until 2026-09-24, when a correct verdict was
	// discarded in transport for quoting the marker it was explaining.
	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"prose quoting a marker": {"LGTM; I saw " + WakeMarker + " in the transcript", roles.ObservedMalformed},
		"prose quoting a relay receipt": {"this looks like the " + relayedReviewMarker +
			" you posted earlier, which is fine", roles.ObservedMalformed},
		"a review carrying a second protocol marker": {reviewartifact.Marker + "\ntask=" + relaySubject.TaskID +
			"\nrequest=" + relayRequest + "\nbase=" + relaySubject.BaseSHA +
			"\ncandidate_digest=" + relaySubject.CandidateDigest +
			"\ncandidate_tree=" + relaySubject.CandidateTree +
			"\nreview_commit=" + relaySubject.ReviewCommit + "\nreviewer=chatgpt\n" +
			acceptPayload + "\n" + WakeMarker + "\n", boundCanonical},
		"an indented wake inside a reply": {"here is what I got back:\n\n    " + WakeMarker + "\n",
			roles.ObservedMalformed},
	} {
		t.Run(name, func(t *testing.T) {
			body := tc.body
			c := comment(6701, 1697116, "davecourtois", body)
			// The precondition: it really does mention a marker, so this case
			// exercises the boundary rather than ordinary prose.
			var mentions bool
			for _, marker := range otherProtocolMarkers {
				if strings.Contains(body, marker) {
					mentions = true
				}
			}
			if !mentions {
				t.Fatal("this fixture mentions no protocol marker, so it proves nothing about the boundary")
			}
			// And it is NOT one of those objects.
			if otherProtocolObject(body) {
				t.Fatal("this fixture IS a top-level protocol object, so it proves nothing")
			}

			obs, ok := classify(o, c)
			if !ok {
				t.Fatal("reviewer content that merely mentions a marker was discarded")
			}
			if obs.kind != tc.want {
				t.Fatalf("classified as %s, want %s", obs.kind, tc.want)
			}
			if tc.want == boundCanonical {
				// The review answers, marker and all: the quoted text stayed
				// payload and the envelope at position zero kept its identity.
				if obs.artifact == nil || obs.mismatch != "" {
					t.Fatalf("a review quoting a marker was not read as this obligation's answer: %+v", obs)
				}
				return
			}
			// And it reaches the waiter as evidence, never as silence.
			var seen observationSet
			seen.add(obs)
			err := waitEnded(o, &seen, context.DeadlineExceeded)
			if errors.Is(err, ErrNoAnswer) {
				t.Fatalf("it decayed into no-answer: %v", err)
			}
			var observed *roles.ReviewObservationFault
			if !errors.As(err, &observed) || !observed.Has(roles.ObservedMalformed) {
				t.Fatalf("err = %v, want a MALFORMED observation", err)
			}
		})
	}
}

// After an observation fault the SAME obligation stands, and a corrected review
// is consumed under it.
//
// This is what makes the fault resumable rather than terminal: nothing was
// minted, nothing was superseded, and the reviewer's second attempt answers the
// first request.
func TestACorrectedReviewIsConsumedUnderTheSameObligationAfterAFault(t *testing.T) {
	f := newRelayFixture(t)
	// The request locator has to sit below the comments this fixture posts, or
	// the response window excludes them and the test would be measuring the
	// window rather than the correction.
	rec := soleObligation(t, f.exchanges)
	if err := f.exchanges.Close(rec.TaskID, rec.RequestID); err != nil {
		t.Fatal(err)
	}
	rec.RequestComment = 1
	if err := f.exchanges.Open(rec); err != nil {
		t.Fatal(err)
	}
	o := obligationFrom(rec)

	f.mailbox.add("LGTM, ship it", "davecourtois", 1697116)
	if _, err := waitOn(t, f, o); !errors.Is(err, ErrNoAnswer) {
		var observed *roles.ReviewObservationFault
		if !errors.As(err, &observed) || !observed.Has(roles.ObservedMalformed) {
			t.Fatalf("the first attempt is %v, want a MALFORMED observation", err)
		}
	}
	// Nothing was minted or retired by observing badly.
	if owed, _ := f.exchanges.PendingReviews(); len(owed) != 1 || owed[0].RequestID != o.RequestID {
		t.Fatalf("the obligation changed after an observation fault: %+v", owed)
	}

	// The reviewer corrects themselves against the SAME request.
	corrected := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	f.mailbox.add(corrected, "davecourtois", 1697116)

	runner := f.runner()
	runner.NewRequestID = func() string {
		t.Fatal("consuming a corrected review minted a request")
		return ""
	}
	res, err := runner.Run(context.Background(), f.turn(), nil)
	if err != nil {
		t.Fatalf("the corrected review was not consumed: %v", err)
	}
	if res.ReviewDigest != ReviewDigest(corrected) {
		t.Fatalf("consumed %s, want the correction %s", res.ReviewDigest, ReviewDigest(corrected))
	}
	if owed, _ := f.exchanges.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the obligation was not discharged: %+v", owed)
	}
}

// ---------------------------------------------------------------------------
// POSITIONAL FRAMING at the review observation.
//
// Two dimensions that must not be conflated. FRAMING CORRECTNESS: payload text
// must not acquire protocol meaning merely by containing marker-shaped text.
// DIAGNOSTIC HONESTY: when the envelope at position zero IS malformed,
// attribution and forensic reporting stay precise. A permissive fix satisfies
// the first while destroying the second, which is what the control below exists
// to prevent.
// ---------------------------------------------------------------------------

// reviewQuotingMarkers is a verdict whose finding quotes eight protocol
// envelopes, including the review envelope itself.
func reviewQuotingMarkers(t *testing.T) string {
	t.Helper()
	claim := "a bare " + architectureRefusalMarker + " comment is classified as ordinary content; " +
		"the same is true of " + reviewartifact.Marker + ", " + requestMarker + ", " +
		relayedReviewMarker + ", " + attestationMarker + ", " + WakeMarker + ", " +
		WithdrawnMarker + " and " + architectureResponseMarker
	return canonicalFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"revise","summary":"the framing rule is not proven","instructions":"prove it",`+
			`"findings":[{"id":"f1","severity":"blocking","claim":"`+claim+`",`+
			`"reference":"internal/ghbridge/review_observation.go",`+
			`"reason":"identity is decided by searching the whole body",`+
			`"correction":"identify at position zero"}]}`)
}

// POSITIONAL FRAMING W1 AND W8, at the classifier that decides what a reviewer
// said.
//
// The assertion is the ABSENCE of the malformed classification. On 2026-09-24 a
// review carrying three correct blocking findings was discarded because one
// finding quoted a protocol marker while explaining it; at this layer that would
// appear as ObservedMalformed, or -- worse -- as another protocol's object and
// therefore as no observation at all, which decays into "the reviewer never
// answered".
//
// W8 is the same subtest: eight marker-shaped strings in one payload yield
// exactly ONE observation. The remainder after the envelope is never rescanned.
func TestAReviewQuotingProtocolMarkersIsObservedAsAReview(t *testing.T) {
	o := observedObligation()
	raw := reviewQuotingMarkers(t)
	if strings.Count(raw, "[sensei-code:") < 9 {
		t.Fatalf("the fixture quotes too few markers to prove anything:\n%s", raw)
	}
	c := comment(6500, 1697116, "davecourtois", raw)

	// NOT another protocol's object, even though it contains seven of their
	// markers. That check runs before parsing, so a failure here would discard
	// the review before anything could say why it was unreadable.
	if otherProtocolObject(raw) {
		t.Fatal("a review quoting other protocols' markers was taken for one of them")
	}
	obs, ok := classify(o, c)
	if !ok {
		t.Fatal("a review quoting protocol markers was discarded as not-a-review")
	}
	if obs.kind == roles.ObservedMalformed {
		t.Fatalf("a review quoting protocol markers was classified MALFORMED: %s", obs.diagnostic)
	}
	if obs.kind != boundCanonical || obs.artifact == nil {
		t.Fatalf("classified as %s, want an exact bound review", obs.kind)
	}
	if obs.artifact.RequestID != relayRequest || obs.mismatch != "" {
		t.Fatalf("the quoted markers rewrote the review's identity: %+v", obs.artifact)
	}
	if got, want := strings.Count(obs.artifact.Body, "[sensei-code:"),
		strings.Count(raw, "[sensei-code:")-1; got != want {
		t.Fatalf("the payload kept %d of the reviewer's %d quoted markers", got, want)
	}

	// AND IT DISCHARGES THE WAIT. The classifier agreeing while the waiter still
	// refused the bytes would leave the measured failure exactly where it was.
	f := newRelayFixture(t)
	ob := obligationFrom(soleObligation(t, f.exchanges))
	ob.RequestComment = 1
	f.mailbox.add(raw, "davecourtois", 1697116)
	res, err := waitOn(t, f, ob)
	if err != nil {
		t.Fatalf("a review quoting protocol markers did not discharge the obligation: %v", err)
	}
	if res.Artifact.Digest != ReviewDigest(raw) {
		t.Fatalf("returned %s, want the quoted review %s", res.Artifact.Digest, ReviewDigest(raw))
	}
}

// POSITIONAL FRAMING W3 AND W4 CONTROLS, at the classifier.
//
// W3: ordinary reviewer content that merely CONTAINS a marker is ordinary
// content. It is not another protocol's object, so it is not silently dropped;
// it is reviewer-origin content that cannot be read as a review, and it is
// REPORTED as that.
//
// W4: content whose FIRST bytes claim the review envelope and whose grammar is
// then wrong stays an attributable review failure. It must not be reclassified
// as ordinary content, and it must settle nothing.
//
// A4 and A3 pull in opposite directions and both cases are here on purpose: a
// permissive reading of A3 would let W4's bodies decay into "not a review", and
// a strict reading of A4 would let W3's prose acquire an identity it never
// claimed.
func TestPositionDecidesWhatReviewerContentIs(t *testing.T) {
	o := observedObligation()
	good := canonicalFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	for name, tc := range map[string]struct {
		body       string
		wantsOther bool
		names      string
	}{
		// W3: contains a marker, claims nothing.
		"prose naming the review envelope": {
			"I could not apply this: a bare " + reviewartifact.Marker + " comment is ordinary content.",
			false, "envelope",
		},
		"prose naming another protocol": {
			"the " + WakeMarker + " envelope is a doorbell, not an answer", false, "envelope",
		},
		"a review quoted behind prose": {"here is what I was sent:\n" + good, false, "envelope"},
		// W4: claims the review envelope at position zero, grammar wrong.
		"the envelope with no identity": {reviewartifact.Marker + "\n\n" + acceptPayload, false, "reviewer"},
		"the envelope with half an identity": {
			reviewartifact.Marker + "\ntask=" + relaySubject.TaskID + "\n", false, "reviewer",
		},
		// Identified by position zero, then refused by the review grammar: the
		// delimiter is wrong, so no identity line is read and the diagnostic
		// names the review rule that failed rather than denying this is a review.
		"the envelope on a shared line": {reviewartifact.Marker + " " + acceptPayload, false, "reviewer"},
		// The control for the control: a real object of another kind, which
		// must still be skipped rather than blamed on the reviewer.
		"an actual wake signal": {WakeMarker + "\nrequest_comment=1\n", true, ""},
	} {
		t.Run(name, func(t *testing.T) {
			c := comment(6600, 1697116, "davecourtois", tc.body)
			if got := otherProtocolObject(tc.body); got != tc.wantsOther {
				t.Fatalf("otherProtocolObject = %v, want %v", got, tc.wantsOther)
			}
			obs, ok := classify(o, c)
			if tc.wantsOther {
				if ok {
					t.Fatalf("a known object of another kind was classified as a review observation: %+v", obs)
				}
				return
			}
			if !ok {
				t.Fatal("reviewer-origin content was discarded rather than reported")
			}
			if obs.kind != roles.ObservedMalformed {
				t.Fatalf("classified as %s, want MALFORMED", obs.kind)
			}
			if obs.artifact != nil {
				t.Fatalf("unreadable bytes were given artifact identity: %+v", obs.artifact)
			}
			if !strings.Contains(obs.diagnostic, tc.names) {
				t.Errorf("the diagnostic does not name %q: %q", tc.names, obs.diagnostic)
			}
		})
	}
}

// POSITIONAL FRAMING W9 -- REPORTING PRESERVED. THE CONTROL THAT BOLTS THE DOOR.
//
// The framing repair must not degrade the observability that made the defect
// findable. What the engine did RIGHT on 2026-09-24, and must keep doing: it
// reported MALFORMED_OR_UNATTRIBUTABLE with the comment id, the author, the byte
// count, the body digest and the exact diagnostic; it preserved the candidate;
// it left the review obligation standing; and it asked no other participant.
//
// A permissive fix satisfies framing correctness and destroys every line of
// that. This is the test it has to pass on the way.
func TestAMalformedReviewIsStillReportedWithItsFullForensics(t *testing.T) {
	f := newRelayFixture(t)
	o := obligationFrom(soleObligation(t, f.exchanges))
	o.RequestComment = 1
	// Reviewer-origin content that is genuinely unreadable: the historical
	// specimen, bare reviewer JSON with no envelope at all.
	const bad = `{"decision":"accept","summary":"looks fine to me","instructions":"","findings":[]}`
	f.mailbox.add(bad, "davecourtois", 1697116)
	before := len(f.mailbox.posted())

	res, err := waitOn(t, f, o)

	var observed *roles.ReviewObservationFault
	if !errors.As(err, &observed) {
		t.Fatalf("err = %v, want an observation fault", err)
	}
	if errors.Is(err, ErrNoAnswer) {
		t.Fatalf("observed evidence was reported as nobody answering: %v", err)
	}
	if len(observed.Observations) != 1 {
		t.Fatalf("%d observations reported, want the one malformed comment: %+v", len(observed.Observations), observed.Observations)
	}
	got := observed.Observations[0]
	if got.Kind != roles.ObservedMalformed {
		t.Fatalf("kind = %s, want MALFORMED", got.Kind)
	}
	// THE FIVE FORENSIC FACTS, each asserted by itself so removing any one of
	// them fails here rather than in six weeks.
	if got.Comment == 0 {
		t.Error("the observation does not name the comment it saw")
	}
	if got.Author != "davecourtois" || got.AuthorID != 1697116 {
		t.Errorf("the observation does not name its author: %q/%d", got.Author, got.AuthorID)
	}
	if got.Bytes != len(bad) {
		t.Errorf("bytes = %d, want the exact observed %d", got.Bytes, len(bad))
	}
	if got.BodyDigest != bodyDigestOf(bad) {
		t.Errorf("body_digest = %q, want the digest of the exact bytes", got.BodyDigest)
	}
	if strings.TrimSpace(got.Diagnostic) == "" {
		t.Error("the observation does not say why the bytes could not be read")
	}
	// Malformed bytes acquire no semantic field they never proved.
	if got.ArtifactDigest != "" || got.RequestID != "" || got.Provider != "" {
		t.Errorf("unreadable bytes were given semantic identity: %+v", got)
	}
	// THE CANDIDATE IS PRESERVED: the fault names the exact subject, so the
	// thing under review is still identified rather than lost with the answer.
	if observed.Binding.CandidateDigest != relaySubject.CandidateDigest ||
		observed.Binding.CandidateTree != relaySubject.CandidateTree ||
		observed.ReviewCommit != relaySubject.ReviewCommit {
		t.Errorf("the fault does not name the candidate it is about: %+v", observed)
	}
	// THE OBLIGATION STANDS: no review was returned, the durable record is still
	// open, and it still names the same request.
	if res.Artifact.Digest != "" {
		t.Errorf("a malformed observation produced a review: %+v", res.Artifact)
	}
	if rec := soleObligation(t, f.exchanges); rec.RequestID != o.RequestID {
		t.Errorf("the obligation record was rewritten: %+v", rec)
	}
	if _, stored := f.stored(t); stored {
		t.Error("a malformed observation staged a review")
	}
	// AND NO OTHER PARTICIPANT WAS ASKED: nothing was published to the mailbox,
	// so no second reviewer, relay or attestation was solicited on the strength
	// of an unreadable reply.
	if n := len(f.mailbox.posted()); n != before {
		t.Fatalf("the mailbox gained %d comment(s) while reporting a malformed reply: %v",
			n-before, f.mailbox.posted()[before:])
	}
}

package ghbridge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/roles"
)

func attestStore(t *testing.T) AttestationStore {
	t.Helper()
	return AttestationStore{Dir: filepath.Join(t.TempDir(), "attestations")}
}

func (f relayFixture) attest(store AttestationStore, requestID, digest string, permitted bool) (AttestationRecord, error) {
	return AcceptAttestation(context.Background(), AttestationSubmission{
		RequestID: requestID, ReviewDigest: digest, Principal: operator, Permitted: permitted,
		Reviews: f.reviews, Store: store, Mailbox: f.box,
	})
}

// An override is recorded against ONE exact published review, and everything it
// publishes says it is an override rather than a review.
func TestAnAttestationOverridesOneExactPublishedReviewAndSaysSo(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	relayed, err := f.submit(artifact)
	if err != nil {
		t.Fatal(err)
	}

	rec, err := f.attest(store, relayRequest, relayed.ReviewDigest, true)
	if err != nil {
		t.Fatalf("the override was refused: %v", err)
	}
	a := rec.Attestation
	if rec.State != AttestationPublished || rec.PublicationComment <= 0 {
		t.Fatalf("state %q comment %d", rec.State, rec.PublicationComment)
	}
	if a.Reviewer != "chatgpt" || a.Principal != operator.token() || a.Decision != roles.Accept {
		t.Fatalf("the override conflated its parties: %+v", a)
	}
	if a.Statement != roles.AttestationStatement {
		t.Fatalf("the override rewrote what an override says: %q", a.Statement)
	}
	if err := a.Covers(roles.Binding{TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}, relayed.ReviewDigest); err != nil {
		t.Fatalf("the override does not cover the candidate it was made for: %v", err)
	}

	posted := f.mailbox.posted()
	body := posted[len(posted)-1]
	for _, want := range []string{
		attestationMarker, "standing=human_override", "attesting_principal=" + operator.token(),
		"review_digest=" + relayed.ReviewDigest, "reviewer_provider=chatgpt",
		"is NOT satisfied", roles.AttestationStatement,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the published override does not carry %q:\n%s", want, body)
		}
	}
	if _, err := reviewartifact.Parse(body); err == nil || strings.Contains(body, reviewartifact.Marker) {
		t.Fatalf("the published override can be read as a review:\n%s", body)
	}

	// The lookup the engine uses finds it, and only for this candidate.
	got, found, err := store.AttestationFor(a.Binding, relayed.ReviewDigest)
	if err != nil || !found || got.ReviewDigest != relayed.ReviewDigest {
		t.Fatalf("AttestationFor: %+v %v %v", got, found, err)
	}
	moved := a.Binding
	moved.CandidateDigest = "sha256:" + strings.Repeat("0", 64)
	if _, found, _ := store.AttestationFor(moved, relayed.ReviewDigest); found {
		t.Fatal("the override covers a candidate that moved")
	}
	if _, found, _ := store.AttestationFor(a.Binding, "sha256:"+strings.Repeat("0", 64)); found {
		t.Fatal("the override covers a review it does not name")
	}
}

// An override the App never published is not yet part of the record anyone else
// can read, so the lookup the engine uses does not return it. Publication is
// what makes an override visible beyond this machine; a local file alone must
// not advance a candidate. Retrying publication makes it consumable.
func TestAnUnpublishedOverrideIsNotFoundUntilItIsPublished(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	relayed, err := f.submit(artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload))
	if err != nil {
		t.Fatal(err)
	}
	binding := roles.Binding{TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}

	f.mailbox.failPosts = true
	rec, err := f.attest(store, relayRequest, relayed.ReviewDigest, true)
	if !errors.Is(err, ErrRelayPublication) || rec.State != AttestationAccepted {
		t.Fatalf("a failed publication reported state %q: %v", rec.State, err)
	}
	if stored, found, _ := store.Load(relayRequest); !found || stored.State != AttestationAccepted {
		t.Fatalf("the accepted override was not preserved: found=%v", found)
	}
	if _, found, err := store.AttestationFor(binding, relayed.ReviewDigest); found || err != nil {
		t.Fatalf("an unpublished override was offered to the engine: found=%v err=%v", found, err)
	}

	f.mailbox.failPosts = false
	if rec, err = f.attest(store, relayRequest, relayed.ReviewDigest, true); err != nil || rec.State != AttestationPublished {
		t.Fatalf("retrying publication: state %q err %v", rec.State, err)
	}
	if _, found, err := store.AttestationFor(binding, relayed.ReviewDigest); !found || err != nil {
		t.Fatalf("the published override is not found: found=%v err=%v", found, err)
	}
}

// One unchanged candidate accumulates an override per review it was given: a
// request goes unanswered, its review is relayed and attested, the candidate is
// re-requested, and a second review is relayed and attested. Both overrides
// cover the candidate, and the one that applies is the one naming the review
// being consumed.
//
// Selecting on the candidate alone returned whichever file the directory gave
// first, so the applicable override could be hidden behind an older one and the
// candidate would wait again. The second request id here sorts BEFORE the first,
// so a first-match-wins lookup answers with the wrong override in at least one
// direction rather than accidentally passing.
func TestTheOverrideSelectedIsTheOneNamingTheReviewBeingConsumed(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	const secondRequest = "r-00000000000000aa" // sorts before relayRequest
	if err := f.exchanges.Open(ExchangeRecord{
		TaskID: relaySubject.TaskID, RequestID: secondRequest, RequestComment: 5686428019, Conversation: "157",
		PublishedAt: time.Now().Add(-30 * time.Minute).UTC(), Kind: ExchangeReview, ReviewerProvider: "chatgpt",
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
	}); err != nil {
		t.Fatal(err)
	}

	// Sequentially, because one task owes at most one review at a time (#182
	// R4): the first obligation is discharged before the second is relayed.
	// Two simultaneously active obligations are a refused conflict, not a
	// scenario an override has to choose within.
	if err := f.exchanges.Close(relaySubject.TaskID, secondRequest); err != nil {
		t.Fatal(err)
	}
	first, err := f.submit(artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.exchanges.Close(relaySubject.TaskID, relayRequest); err != nil {
		t.Fatal(err)
	}
	if err := f.exchanges.Open(ExchangeRecord{
		TaskID: relaySubject.TaskID, RequestID: secondRequest, RequestComment: 5686428019, Conversation: "157",
		PublishedAt: time.Now().Add(-30 * time.Minute).UTC(), Kind: ExchangeReview, ReviewerProvider: "chatgpt",
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
		ExpectedReviewerID: 1697116, ExpectedReviewerLogin: "davecourtois",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := f.submit(artifactFor(t, relaySubject, secondRequest, "chatgpt",
		`{"decision":"accept","summary":"the ledger invariant still holds","instructions":"","findings":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if first.ReviewDigest == second.ReviewDigest {
		t.Fatal("the two relayed reviews are the same bytes, so the selection proves nothing")
	}
	for _, rel := range []RelayResult{first, second} {
		if _, err := f.attest(store, rel.RequestID, rel.ReviewDigest, true); err != nil {
			t.Fatalf("attesting %s: %v", rel.RequestID, err)
		}
	}

	binding := roles.Binding{TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}
	for _, want := range []RelayResult{first, second} {
		got, found, err := store.AttestationFor(binding, want.ReviewDigest)
		if err != nil || !found {
			t.Fatalf("review %s has a published override and was not found: %v %v", want.ReviewDigest, found, err)
		}
		if got.ReviewDigest != want.ReviewDigest || got.RequestID != want.RequestID {
			t.Fatalf("consuming review %s selected the override for %s (request %s)",
				want.ReviewDigest, got.ReviewDigest, got.RequestID)
		}
	}
	if _, found, _ := store.AttestationFor(binding, "sha256:"+strings.Repeat("0", 64)); found {
		t.Fatal("a review nobody attested to selected an override")
	}
}

// Everything that is not an owner overriding one exact published ACCEPT is
// refused, and nothing is recorded or published.
func TestAnAttestationIsRefusedUnlessItOverridesAPublishedAccept(t *testing.T) {
	accept := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	revise := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"revise","summary":"the ledger invariant is not proven","instructions":"prove it",`+
			`"findings":[{"id":"f1","severity":"blocking","class":"evidence","claim":"the invariant holds","reference":"ledger.go",`+
			`"reason":"no test covers position 0","correction":"add the test"}]}`)

	for name, c := range map[string]struct {
		artifact  string
		publish   bool
		permitted bool
		request   string
		digest    string
	}{
		"authority not granted here": {artifact: accept, publish: true, permitted: false},
		"a request with no relay":    {artifact: accept, publish: true, permitted: true, request: "r-ffffffffffffffff"},
		"a digest that is not the recorded review": {artifact: accept, publish: true, permitted: true,
			digest: "sha256:" + strings.Repeat("0", 64)},
		"a relay that was never published": {artifact: accept, publish: false, permitted: true},
		"a review that did not accept":     {artifact: revise, publish: true, permitted: true},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			store := attestStore(t)
			f.mailbox.failPosts = !c.publish
			relayed, err := f.submit(c.artifact)
			if c.publish && err != nil {
				t.Fatalf("the relay under test was not established: %v", err)
			}
			f.mailbox.failPosts = false
			before := len(f.mailbox.posted())

			request, digest := relayRequest, relayed.ReviewDigest
			if c.request != "" {
				request = c.request
			}
			if c.digest != "" {
				digest = c.digest
			}
			if _, err := f.attest(store, request, digest, c.permitted); !errors.Is(err, ErrAttestationRefused) {
				t.Fatalf("err = %v, want a refusal", err)
			}
			if _, found, _ := store.Load(relayRequest); found {
				t.Fatal("a refused override left a record")
			}
			if n := len(f.mailbox.posted()) - before; n != 0 {
				t.Fatalf("a refused override published %d comment(s)", n)
			}
		})
	}
}

// POSITIONAL FRAMING at the override publication: what this body IS, is decided
// by the envelope it OPENS with.
//
// The same rule the relay receipt and the review parser now use, so no artifact
// kind is left deciding its own identity by scanning itself. RenderAttestation
// previously refused any body containing a review or request marker, or holding
// more than one "[sensei-code:" in total.
//
// THE REACH OF THIS WITNESS, STATED. roles.Attestation.Statement is fixed text
// by design -- an override says one thing, and a free-text field would let it
// say something weaker or stronger than what it is -- and every other field this
// renders is shape-constrained. So no reachable override can carry a quoted
// marker today, and the scan this replaces could not misclassify one. This is a
// FORWARD GUARD on the rule, not a reproduction of a reachable defect: it pins
// the renderer to position zero against the day any of that text stops being
// fixed, and it keeps one rule spelled one way across every envelope. The
// reachable half -- an override is still not a review -- is asserted first.
func TestTheOverridePublicationIsDecidedByTheEnvelopeItOpensWith(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	relayed, err := f.submit(artifact)
	if err != nil {
		t.Fatal(err)
	}
	rec, err := f.attest(store, relayRequest, relayed.ReviewDigest, true)
	if err != nil {
		t.Fatalf("the override was refused: %v", err)
	}

	body, err := RenderAttestation(rec, f.box)
	if err != nil {
		t.Fatalf("a valid override did not render: %v", err)
	}
	// REACHABLE: it opens with its own envelope and nothing reading the mailbox
	// can take it for an answer.
	if !reviewartifact.Opens(body, attestationMarker) {
		t.Fatalf("the override does not open with its own envelope:\n%s", body)
	}
	if _, perr := reviewartifact.Parse(body); perr == nil {
		t.Fatalf("the override can be read as a review answer:\n%s", body)
	}

	// FORWARD GUARD: a statement that quotes protocol markers is text. It does
	// not make the override a second artifact, and it does not make the override
	// unpublishable -- which is what the whole-body scan would have done to it.
	quoting := rec
	quoting.Attestation.Statement = "Overriding: the reviewer said a bare " +
		architectureRefusalMarker + " comment is classified as ordinary content, and quoted " +
		reviewartifact.Marker + ", " + requestMarker + " and " + relayedReviewMarker + " to explain it."
	quoted, err := RenderAttestation(quoting, f.box)
	if err != nil {
		t.Fatalf("an override whose statement quotes protocol markers was not published: %v", err)
	}
	if !strings.Contains(quoted, quoting.Attestation.Statement) {
		t.Fatalf("the statement was dropped from the publication:\n%s", quoted)
	}
	if !reviewartifact.Opens(quoted, attestationMarker) {
		t.Fatal("the quoted markers changed which envelope the override opens with")
	}
	if _, perr := reviewartifact.Parse(quoted); perr == nil {
		t.Fatalf("an override quoting the review marker became readable as a review:\n%s", quoted)
	}
}

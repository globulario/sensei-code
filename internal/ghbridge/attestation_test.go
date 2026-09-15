package ghbridge

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/roles"
)

func attestStore(t *testing.T) AttestationStore {
	t.Helper()
	return AttestationStore{Dir: filepath.Join(t.TempDir(), "attestations")}
}

func (f relayFixture) attest(store AttestationStore, requestID, digest string, permitted bool) (AttestationRecord, error) {
	return AcceptAttestation(context.Background(), AttestationSubmission{
		RequestID: requestID, ReviewDigest: digest, Principal: operator, Permitted: permitted,
		Relays: f.store, Store: store, Mailbox: f.box,
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
	if _, ok := ParseReview(body, "davecourtois"); ok || strings.Contains(body, reviewMarker) {
		t.Fatalf("the published override can be read as a review:\n%s", body)
	}

	// The lookup the engine uses finds it, and only for this candidate.
	got, found, err := store.AttestationFor(a.Binding)
	if err != nil || !found || got.ReviewDigest != relayed.ReviewDigest {
		t.Fatalf("AttestationFor: %+v %v %v", got, found, err)
	}
	moved := a.Binding
	moved.CandidateDigest = "sha256:" + strings.Repeat("0", 64)
	if _, found, _ := store.AttestationFor(moved); found {
		t.Fatal("the override covers a candidate that moved")
	}
}

// Everything that is not an owner overriding one exact published ACCEPT is
// refused, and nothing is recorded or published.
func TestAnAttestationIsRefusedUnlessItOverridesAPublishedAccept(t *testing.T) {
	accept := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	revise := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"revise","summary":"the ledger invariant is not proven","instructions":"prove it",`+
			`"findings":[{"id":"f1","severity":"blocking","claim":"the invariant holds","reference":"ledger.go",`+
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

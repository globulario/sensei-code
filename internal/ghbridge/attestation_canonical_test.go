package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// acceptMailboxReview puts a canonical review into the common store the way the
// MAILBOX adapter does: exact reviewer bytes, GitHub's transport facts beside
// them, and no relay anywhere.
func acceptMailboxReview(t *testing.T, f relayFixture, raw string) reviewstore.Record {
	t.Helper()
	comment := f.mailbox.add(raw, "davecourtois", 1697116)
	rec, err := f.reviews.Accept(reviewstore.Acceptance{
		RequestID: relayRequest, Artifact: raw,
		Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox,
			GitHubAuthor: "davecourtois", GitHubAuthorID: 1697116, GitHubComment: comment},
		Validate: func(a reviewartifact.Artifact) error {
			_, err := workflow.ValidateReviewBody(a.Body, roles.Binding{
				TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
				CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree,
			}, a.ReviewerProvider)
			return err
		},
	})
	if err != nil {
		t.Fatalf("accepting a mailbox review: %v", err)
	}
	return rec
}

func relaysEmpty(t *testing.T, f relayFixture) {
	t.Helper()
	if _, found, _ := f.store.Load(relayRequest); found {
		t.Fatal("a relay receipt exists; this case must prove attestation without one")
	}
}

// A review read directly off the authenticated mailbox can be overridden, with
// no relay receipt anywhere.
//
// This is the transport/authority split R3 closes. Before it, the very same
// reviewer bytes were attestable if an operator had relayed them and not
// attestable if the reviewer had simply posted them -- the delivery pipe decided
// whether the owner had authority over their own candidate.
func TestADirectMailboxReviewCanBeAttestedWithNoRelayReceipt(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	stored := acceptMailboxReview(t, f, raw)
	relaysEmpty(t, f)

	rec, err := f.attest(store, relayRequest, stored.ReviewDigest, true)
	if err != nil {
		t.Fatalf("a mailbox-origin review could not be attested: %v", err)
	}
	if rec.State != AttestationPublished || rec.PublicationComment <= 0 {
		t.Fatalf("state %q comment %d", rec.State, rec.PublicationComment)
	}
	a := rec.Attestation
	if a.ReviewDigest != ReviewDigest(raw) {
		t.Fatalf("the override names review %s, want %s", a.ReviewDigest, ReviewDigest(raw))
	}
	if a.Reviewer != "chatgpt" {
		t.Fatalf("reviewer is %q, want the artifact's chatgpt", a.Reviewer)
	}
	if a.Decision != roles.Accept {
		t.Fatalf("decision is %q, want accept", a.Decision)
	}
	if a.Principal != operator.token() {
		t.Fatalf("principal is %q, want the terminal owner", a.Principal)
	}
	relaysEmpty(t, f)
}

// The graduation property: the same canonical bytes, arriving by either pipe,
// produce the same override.
//
// Transport evidence differs in the review store and must not appear as a
// semantic discriminator anywhere in the attestation.
func TestMailboxAndRelayReviewsAttestIdentically(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	at := time.Date(2026, 9, 18, 3, 4, 5, 0, time.UTC)

	attestVia := func(t *testing.T, arrive func(t *testing.T, f relayFixture) string) roles.Attestation {
		t.Helper()
		f := newRelayFixture(t)
		store := attestStore(t)
		digest := arrive(t, f)
		rec, err := AcceptAttestation(context.Background(), AttestationSubmission{
			RequestID: relayRequest, ReviewDigest: digest, Principal: operator, Permitted: true,
			Reviews: f.reviews, Store: store, Mailbox: f.box,
			Now: func() time.Time { return at },
		})
		if err != nil {
			t.Fatalf("attesting: %v", err)
		}
		return rec.Attestation
	}

	viaMailbox := attestVia(t, func(t *testing.T, f relayFixture) string {
		return acceptMailboxReview(t, f, raw).ReviewDigest
	})
	viaRelay := attestVia(t, func(t *testing.T, f relayFixture) string {
		published, err := f.submit(raw)
		if err != nil {
			t.Fatalf("relaying: %v", err)
		}
		return published.ReviewDigest
	})

	if viaMailbox != viaRelay {
		t.Fatalf("the same review attested differently by transport:\n mailbox %+v\n   relay %+v", viaMailbox, viaRelay)
	}
	// Named individually so a failure says WHICH field the transport leaked into.
	for _, f := range []struct{ name, mailbox, relay string }{
		{"request", viaMailbox.RequestID, viaRelay.RequestID},
		{"review_digest", viaMailbox.ReviewDigest, viaRelay.ReviewDigest},
		{"reviewer", viaMailbox.Reviewer, viaRelay.Reviewer},
		{"decision", string(viaMailbox.Decision), string(viaRelay.Decision)},
		{"task", viaMailbox.Binding.TaskID, viaRelay.Binding.TaskID},
		{"base", viaMailbox.Binding.BaseSHA, viaRelay.Binding.BaseSHA},
		{"candidate_digest", viaMailbox.Binding.CandidateDigest, viaRelay.Binding.CandidateDigest},
		{"candidate_tree", viaMailbox.Binding.CandidateTree, viaRelay.Binding.CandidateTree},
		{"principal", viaMailbox.Principal, viaRelay.Principal},
		{"statement", viaMailbox.Statement, viaRelay.Statement},
	} {
		if f.mailbox != f.relay {
			t.Errorf("%s differs by transport: mailbox %q, relay %q", f.name, f.mailbox, f.relay)
		}
	}
}

// Once the canonical review is accepted, the relay receipt is history. Deleting
// or rewriting it cannot change what an override is about.
func TestRelayStoreCannotChangeWhatIsAttestedAfterConvergence(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	swapped := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"swapped","instructions":"","findings":[]}`)

	for name, wreck := range map[string]func(t *testing.T, f relayFixture){
		"the receipt is deleted": func(t *testing.T, f relayFixture) {
			if err := os.Remove(filepath.Join(f.store.Dir, relayRequest+".json")); err != nil {
				t.Fatal(err)
			}
		},
		"the receipt names another review": func(t *testing.T, f relayFixture) {
			rec, found, err := f.store.Load(relayRequest)
			if err != nil || !found {
				t.Fatalf("load: found=%v err=%v", found, err)
			}
			rec.Artifact, rec.ReviewDigest = swapped, ReviewDigest(swapped)
			rec.Reviewer, rec.Decision = "somebody-else", "accept"
			if err := overwriteReceipt(f.store, rec); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			store := attestStore(t)
			if _, err := f.submit(raw); err != nil {
				t.Fatal(err)
			}
			wreck(t, f)

			rec, err := f.attest(store, relayRequest, ReviewDigest(raw), true)
			if err != nil {
				t.Fatalf("attesting the converged review: %v", err)
			}
			if rec.Attestation.ReviewDigest != ReviewDigest(raw) {
				t.Fatalf("the override names %s, want the canonical %s", rec.Attestation.ReviewDigest, ReviewDigest(raw))
			}
			if rec.Attestation.Reviewer != "chatgpt" {
				t.Fatalf("a rewritten receipt supplied the reviewer: %q", rec.Attestation.Reviewer)
			}
		})
	}
}

// Everything the override asserts is re-derived from the reviewer's own bytes.
//
// The transport evidence here deliberately names other parties in every field
// that could be mistaken for the reviewer, and the relay receipt carries a
// different reviewer and a different candidate. None of it may reach the
// attestation.
func TestTheOverrideDerivesEverythingFromTheCanonicalArtifact(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	comment := f.mailbox.add(raw, "davecourtois", 1697116)
	if _, err := f.reviews.Accept(reviewstore.Acceptance{
		RequestID: relayRequest, Artifact: raw,
		Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox,
			// Every transport field says "claude". The artifact says chatgpt.
			GitHubAuthor: "claude", GitHubAuthorID: 424242, GitHubComment: comment},
		Validate: func(reviewartifact.Artifact) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}

	rec, err := f.attest(store, relayRequest, ReviewDigest(raw), true)
	if err != nil {
		t.Fatalf("attesting: %v", err)
	}
	a := rec.Attestation
	if a.Reviewer != "chatgpt" {
		t.Fatalf("reviewer is %q; a transport principal became the reviewer", a.Reviewer)
	}
	if strings.Contains(a.Reviewer, "dave") || strings.Contains(a.Reviewer, "claude") ||
		strings.Contains(a.Reviewer, "bot") {
		t.Fatalf("reviewer %q looks like a transport or owner identity", a.Reviewer)
	}
	want := roles.Binding{TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}
	if a.Binding != want {
		t.Fatalf("binding is %+v, want the artifact's %+v", a.Binding, want)
	}
	// The owner is the owner, and is never the reviewer.
	if a.Principal != operator.token() || a.Principal == a.Reviewer {
		t.Fatalf("owner principal %q and reviewer %q are conflated", a.Principal, a.Reviewer)
	}
}

// Nothing durable happens unless the operator names the exact review they read.
func TestAnOverrideNeedsTheExactRequestAndDigest(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	for name, c := range map[string]struct{ request, digest string }{
		"a digest that is not the recorded review": {relayRequest, "sha256:" + strings.Repeat("0", 64)},
		// A real digest, of bytes nobody accepted.
		"the digest of a byte-different artifact": {relayRequest, ReviewDigest(raw + "\n")},
		"no digest at all":                        {relayRequest, ""},
		"a request with no review":                {"r-ffffffffffffffff", ReviewDigest(raw)},
		"no request at all":                       {"", ReviewDigest(raw)},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			store := attestStore(t)
			acceptMailboxReview(t, f, raw)
			before := len(f.mailbox.posted())

			if _, err := f.attest(store, c.request, c.digest, true); !errors.Is(err, ErrAttestationRefused) {
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

// A relay receipt is not a review. Without the canonical record there is
// nothing to override, however complete the receipt looks.
func TestARelayReceiptAloneCannotBeAttested(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(raw); err != nil {
		t.Fatal(err)
	}
	// The relay is published and its receipt is intact; the canonical record is
	// removed, as it would be if convergence had never completed.
	if err := os.Remove(filepath.Join(f.reviews.Dir, relayRequest+".json")); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := f.store.Load(relayRequest); !found {
		t.Fatal("the relay receipt is gone; this case proves nothing")
	}

	if _, err := f.attest(store, relayRequest, ReviewDigest(raw), true); !errors.Is(err, ErrAttestationRefused) {
		t.Fatalf("a relay receipt alone was attested: %v", err)
	}
	if _, found, _ := store.Load(relayRequest); found {
		t.Fatal("a refused override left a record")
	}
}

// A canonical record that cannot prove itself is not something to override.
func TestAnUnreadableCanonicalRecordCannotBeAttested(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	for name, corrupt := range map[string]func(*reviewstore.Record){
		"a digest naming other bytes": func(r *reviewstore.Record) {
			r.ReviewDigest = reviewartifact.Digest("something else")
		},
		"bytes that no longer parse":  func(r *reviewstore.Record) { r.ArtifactRaw = "not an artifact" },
		"no acceptance observation":   func(r *reviewstore.Record) { r.Evidence = nil },
		"an unwritten schema version": func(r *reviewstore.Record) { r.Version = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			store := attestStore(t)
			acceptMailboxReview(t, f, raw)
			path := filepath.Join(f.reviews.Dir, relayRequest+".json")

			var rec reviewstore.Record
			blob, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(blob, &rec); err != nil {
				t.Fatal(err)
			}
			corrupt(&rec)
			out, err := json.MarshalIndent(rec, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}

			if _, err := f.attest(store, relayRequest, ReviewDigest(raw), true); !errors.Is(err, ErrAttestationRefused) {
				t.Fatalf("an unreadable canonical record was attested: %v", err)
			}
			if _, found, _ := store.Load(relayRequest); found {
				t.Fatal("a refused override left a record")
			}
		})
	}
}

// An override overrides an OBLIGATION, never a finding. A review that did not
// accept cannot be attested into advancement, and the decision is read from the
// payload rather than taken on the record's word.
func TestOnlyAnAcceptingCanonicalReviewCanBeAttested(t *testing.T) {
	for name, body := range map[string]string{
		"a review that asked for changes": `{"decision":"revise","summary":"the ledger invariant is not proven",` +
			`"instructions":"prove it","findings":[{"id":"f1","severity":"blocking","claim":"the invariant holds",` +
			`"reference":"ledger.go","reason":"no test covers position 0","correction":"add the test"}]}`,
		"a review that escalated": `{"decision":"escalate","summary":"this needs a human",` +
			`"instructions":"ask Dave","findings":[]}`,
		"prose instead of a verdict": "LGTM, ship it",
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			store := attestStore(t)
			raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", body)
			// Written straight into the store so the refusal under test is the
			// attestation's own, not the store's acceptance check.
			if err := writeStoredReview(f.reviews, relayRequest, raw, reviewstore.Evidence{
				Transport: reviewstore.GitHubMailbox, GitHubAuthor: "davecourtois",
				GitHubAuthorID: 1697116, GitHubComment: 5150,
			}); err != nil {
				t.Fatal(err)
			}
			before := len(f.mailbox.posted())

			if _, err := f.attest(store, relayRequest, ReviewDigest(raw), true); !errors.Is(err, ErrAttestationRefused) {
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

// Every NEW override carries the transport-neutral statement, and it says
// nothing about relaying.
func TestANewOverrideCarriesTheTransportNeutralStatement(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	acceptMailboxReview(t, f, raw)

	rec, err := f.attest(store, relayRequest, ReviewDigest(raw), true)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Attestation.Statement != roles.AttestationStatement {
		t.Fatalf("statement is %q", rec.Attestation.Statement)
	}
	if strings.Contains(rec.Attestation.Statement, "relayed") {
		t.Fatalf("a new override still describes itself as relay-specific: %q", rec.Attestation.Statement)
	}
	if !strings.Contains(rec.Attestation.Statement, "advisory review") {
		t.Fatalf("the new statement does not say what it overrides: %q", rec.Attestation.Statement)
	}
	if !strings.Contains(rec.Attestation.Statement, "not an independent review") {
		t.Fatalf("the statement stopped saying what it is NOT: %q", rec.Attestation.Statement)
	}
}

// An override made before R3 still means what it meant. It is read and honoured
// exactly as written, and nothing modernises its wording on disk.
func TestAHistoricalOverrideRemainsValidAndIsNeverRewritten(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	digest := ReviewDigest(raw)
	binding := roles.Binding{TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}

	historical := roles.Attestation{
		RequestID: relayRequest, ReviewDigest: digest, Reviewer: "chatgpt",
		Decision: roles.Accept, Binding: binding, Principal: operator.token(),
		At: time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC),
		// Frozen pre-R3 text.
		Statement: roles.LegacyRelayAttestationStatement,
	}
	if err := historical.Validate(); err != nil {
		t.Fatalf("a historical override stopped being valid: %v", err)
	}
	if err := historical.Covers(binding, digest); err != nil {
		t.Fatalf("a historical override stopped covering what it covered: %v", err)
	}

	rec := AttestationRecord{Version: 1, State: AttestationPublished, Attestation: historical,
		AcceptedAt: historical.At, Publication: "github-app", PublicationComment: 4242,
		PublishedAt: historical.At}
	if err := store.create(rec); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(store.Dir, relayRequest+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	got, found, err := store.AttestationFor(binding, digest)
	if err != nil || !found {
		t.Fatalf("a historical override was not found: found=%v err=%v", found, err)
	}
	if got.Statement != roles.LegacyRelayAttestationStatement {
		t.Fatalf("the historical statement was modernised on read: %q", got.Statement)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("reading a historical override rewrote it on disk")
	}
	_ = f
}

// A published override survives the transport that announced it, for the same
// reason an accepted review does: acceptance was established once.
func TestAPublishedOverrideSurvivesAnUnreachableMailbox(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	acceptMailboxReview(t, f, raw)
	if _, err := f.attest(store, relayRequest, ReviewDigest(raw), true); err != nil {
		t.Fatal(err)
	}

	binding := roles.Binding{TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}
	// Nothing about consuming an override touches GitHub, so an outage here
	// cannot retract a human decision that was already recorded and published.
	got, found, err := store.AttestationFor(binding, ReviewDigest(raw))
	if err != nil || !found {
		t.Fatalf("a published override became unreadable: found=%v err=%v", found, err)
	}
	if err := got.Covers(binding, ReviewDigest(raw)); err != nil {
		t.Fatalf("the override stopped covering its candidate: %v", err)
	}
}

// Resubmitting the same override retries publication rather than creating a
// second authority object.
func TestAnOverrideRetriesPublicationWithoutCreatingAnother(t *testing.T) {
	f := newRelayFixture(t)
	store := attestStore(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	acceptMailboxReview(t, f, raw)

	f.mailbox.failPosts = true
	if _, err := f.attest(store, relayRequest, ReviewDigest(raw), true); err == nil {
		t.Fatal("a failed publication reported success")
	}
	rec, found, err := store.Load(relayRequest)
	if err != nil || !found || rec.State != AttestationAccepted {
		t.Fatalf("the accepted override was not preserved for retry: found=%v %+v err=%v", found, rec, err)
	}
	f.mailbox.failPosts = false
	before := len(f.mailbox.posted())

	published, err := f.attest(store, relayRequest, ReviewDigest(raw), true)
	if err != nil {
		t.Fatalf("retrying publication: %v", err)
	}
	if published.State != AttestationPublished || published.PublicationComment <= 0 {
		t.Fatalf("the retry did not publish: %+v", published)
	}
	if n := len(f.mailbox.posted()) - before; n != 1 {
		t.Fatalf("the retry published %d comments, want exactly 1", n)
	}
	again, err := f.attest(store, relayRequest, ReviewDigest(raw), true)
	if err != nil {
		t.Fatalf("a second resubmission: %v", err)
	}
	if again.PublicationComment != published.PublicationComment {
		t.Fatalf("a resubmission republished: comment %d then %d", published.PublicationComment, again.PublicationComment)
	}
}

// Structural: the attestation authority path reads the canonical review store
// and nothing about a relay.
func TestAttestationAuthorityDoesNotReadRelayStore(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "attestation.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "AcceptAttestation" {
			continue
		}
		found = true
		ast.Inspect(fn, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.Ident:
				if v.Name == "RelayStore" || v.Name == "RelayRecord" || v.Name == "Relays" {
					t.Errorf("AcceptAttestation references %s; an override binds to a review, not a delivery receipt", v.Name)
				}
			case *ast.SelectorExpr:
				if v.Sel.Name == "Relays" {
					t.Error("AcceptAttestation reads the relay store")
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("AcceptAttestation was not found; this check proves nothing")
	}
	// And the submission it takes names the review source, not a relay source.
	// Read as FIELD TYPES rather than as text: prose about what the struct
	// deliberately no longer holds would otherwise fail its own check.
	types := map[string]bool{}
	ast.Inspect(parsed, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "AttestationSubmission" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return false
		}
		for _, field := range st.Fields.List {
			switch ft := field.Type.(type) {
			case *ast.Ident:
				types[ft.Name] = true
			case *ast.SelectorExpr:
				if pkg, ok := ft.X.(*ast.Ident); ok {
					types[pkg.Name+"."+ft.Sel.Name] = true
				}
			}
		}
		return false
	})
	if len(types) == 0 {
		t.Fatal("AttestationSubmission was not found; this check proves nothing")
	}
	if !types["reviewstore.Store"] {
		t.Errorf("AttestationSubmission does not take the common review store; it holds %v", types)
	}
	if types["RelayStore"] {
		t.Error("AttestationSubmission still takes a relay store")
	}
}

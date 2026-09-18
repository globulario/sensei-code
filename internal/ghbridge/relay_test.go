package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// relayMailbox is an App mailbox that numbers comments and can refuse posts.
type relayMailbox struct {
	mu        sync.Mutex
	comments  []map[string]any
	failPosts bool
	nextID    int64
}

func (m *relayMailbox) posted() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, c := range m.comments {
		out = append(out, c["body"].(string))
	}
	return out
}

func (m *relayMailbox) add(body, login string, id int64) int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	m.comments = append(m.comments, map[string]any{
		// Real GitHub stamps every comment, and the observation window falls back
		// to this for obligations recorded before request locators were kept.
		"id": m.nextID, "created_at": time.Now().UTC().Format(time.RFC3339),
		"body": body, "user": map[string]any{"login": login, "id": id},
	})
	return m.nextID
}

func newRelayMailbox(t *testing.T) (*relayMailbox, Issue) {
	t.Helper()
	keyPath, _ := writeTestKey(t)
	m := &relayMailbox{nextID: 5000}
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		fmt.Fprintf(w, `{"token":"ghs_installation","expires_at":%q}`, time.Now().Add(time.Hour).UTC().Format(time.RFC3339))
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/157/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			m.mu.Lock()
			fail := m.failPosts
			m.mu.Unlock()
			if fail {
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprint(w, `{"message":"unavailable"}`)
				return
			}
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			id := m.add(in.Body, "globulario-sensei-code[bot]", 99887766)
			w.WriteHeader(http.StatusCreated)
			fmt.Fprintf(w, `{"id":%d}`, id)
		case http.MethodGet:
			m.mu.Lock()
			defer m.mu.Unlock()
			_ = json.NewEncoder(w).Encode(m.comments)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return m, Issue{
		Number: "157",
		API: &AppClient{
			Auth:  &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: keyPath, APIBase: srv.URL},
			Owner: "globulario", Repo: "sensei-code",
		},
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
}

var relaySubject = Subject{
	TaskID:          "task-1789498413173471174",
	BaseSHA:         "91b475a172bba0257fd2ffd8a55d3edce582e883",
	CandidateDigest: "sha256:caa4e6970000000000000000000000000000000000000000000000000000beef",
	CandidateTree:   "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
	ReviewCommit:    "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
}

const relayRequest = "r-0123456789abcdef"

var operator = RelayPrincipal{UID: 1000, User: "dave", PID: 4242, Terminal: 34816}

// artifactFor renders a reviewer's answer in the CANONICAL grammar.
//
// Through reviewartifact.Render, which is now the only renderer there is. Until
// R6 this helper built the envelope out of the legacy ghbridge.Review marker and
// appended reviewer= by hand -- a second spelling of the review envelope that
// existed only so tests could keep using it.
func artifactFor(t *testing.T, s Subject, requestID, provider, payload string) string {
	t.Helper()
	raw, err := reviewartifact.Artifact{
		ReviewerProvider: provider, TaskID: s.TaskID, RequestID: requestID,
		BaseSHA: s.BaseSHA, CandidateDigest: s.CandidateDigest, CandidateTree: s.CandidateTree,
		ReviewCommit: s.ReviewCommit, Body: payload,
	}.Render()
	if err != nil {
		// Deliberately NOT t.Fatal: several tables build artifacts that must be
		// refused downstream, and a renderer that cannot produce one is a fixture
		// fault rather than the property under test.
		return unrenderable(s, requestID, provider, payload)
	}
	return raw
}

// unrenderable builds the bytes a caller asked for even when they do not form a
// valid artifact, so a refusal table can submit them and watch them be refused.
func unrenderable(s Subject, requestID, provider, payload string) string {
	out := reviewartifact.Marker + "\n"
	out += "task=" + s.TaskID + "\n"
	out += "request=" + requestID + "\n"
	out += "base=" + s.BaseSHA + "\n"
	out += "candidate_digest=" + s.CandidateDigest + "\n"
	out += "candidate_tree=" + s.CandidateTree + "\n"
	out += "review_commit=" + s.ReviewCommit + "\n"
	if provider != "" {
		out += "reviewer=" + provider + "\n"
	}
	return out + "\n" + payload + "\n"
}

const acceptPayload = `{"decision":"accept","summary":"the ledger invariant holds at physical position 0","instructions":"","findings":[]}`

type relayFixture struct {
	box       Issue
	mailbox   *relayMailbox
	exchanges ExchangeLog
	reviews   reviewstore.Store
}

func newRelayFixture(t *testing.T) relayFixture {
	t.Helper()
	m, box := newRelayMailbox(t)
	dir := t.TempDir()
	f := relayFixture{box: box, mailbox: m,
		exchanges: ExchangeLog{Dir: filepath.Join(dir, "exchanges")},
		reviews:   reviewstore.Store{Dir: filepath.Join(dir, "reviews")}}
	if err := f.exchanges.Open(ExchangeRecord{
		TaskID: relaySubject.TaskID, RequestID: relayRequest, RequestComment: 5686428018, Conversation: "157",
		PublishedAt: time.Now().Add(-time.Hour).UTC(), Kind: ExchangeReview,
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
		ReviewerProvider: "chatgpt",
		// Pinned, as every R4 obligation is: which account may answer this
		// exact request -- and, since R6, which account spoke for this machine
		// when the request went out. The mailbox fixture posts as this bot.
		ExpectedReviewerID: 1697116, ExpectedReviewerLogin: "davecourtois",
		PublisherID: 99887766, PublisherLogin: "globulario-sensei-code[bot]",
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f relayFixture) submit(artifact string) (RelayResult, error) {
	return AcceptRelayedReview(context.Background(), RelaySubmission{
		Artifact: artifact, Principal: operator, Exchanges: f.exchanges,
		Reviews: f.reviews, Mailbox: f.box,
	})
}

func (f relayFixture) submitAs(artifact string, p RelayPrincipal) (RelayResult, error) {
	return AcceptRelayedReview(context.Background(), RelaySubmission{
		Artifact: artifact, Principal: p, Exchanges: f.exchanges,
		Reviews: f.reviews, Mailbox: f.box,
	})
}

func (f relayFixture) runner() *Runner {
	return &Runner{Issue: f.box, NewRequestID: NewRequestID, Poll: 10 * time.Millisecond,
		Wait: 50 * time.Millisecond, Exchanges: f.exchanges, Reviews: f.reviews,
		ReviewerProvider: "chatgpt"}
}

func (f relayFixture) turn() agent.Request {
	return agent.Request{Role: roles.Reviewer, TaskID: relaySubject.TaskID, Binding: roles.Binding{
		TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}}
}

// stored reads the one durable review record, failing the test if it cannot.
func (f relayFixture) stored(t *testing.T) (reviewstore.Record, bool) {
	t.Helper()
	rec, found, err := f.reviews.Load(relayRequest)
	if err != nil {
		t.Fatalf("reading the review record: %v", err)
	}
	return rec, found
}

// relayEvidence returns the one local-relay row, or fails.
func relayEvidence(t *testing.T, rec reviewstore.Record) reviewstore.Evidence {
	t.Helper()
	var out []reviewstore.Evidence
	for _, ev := range rec.Evidence {
		if ev.Transport == reviewstore.LocalRelay {
			out = append(out, ev)
		}
	}
	if len(out) != 1 {
		t.Fatalf("want exactly one local-relay evidence row, got %d: %+v", len(out), rec.Evidence)
	}
	return out[0]
}

// The exact matching review is staged as submitted, and the App publishes it
// with the reviewer, the relay principal and the publisher kept apart.
func TestATerminalRelayStagesTheExactReviewAndTheAppPublishesItSeparately(t *testing.T) {
	f := newRelayFixture(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	res, err := f.submit(artifact)
	if err != nil {
		t.Fatalf("the exact matching review was refused: %v", err)
	}
	if res.State != RelayPublished || res.ReviewDigest != ReviewDigest(artifact) || res.Reviewer != "chatgpt" {
		t.Fatalf("result state=%s digest=%s reviewer=%s", res.State, res.ReviewDigest, res.Reviewer)
	}

	// ONE durable record, holding the reviewer's exact bytes.
	rec, found := f.stored(t)
	if !found || rec.ArtifactRaw != artifact || rec.ReviewDigest != ReviewDigest(artifact) {
		t.Fatalf("the review store does not hold the submitted bytes exactly: found=%v", found)
	}
	if rec.Standing != reviewstore.Advisory {
		t.Fatalf("a relayed review was recorded with standing %q", rec.Standing)
	}
	if !rec.Consumable() {
		t.Fatal("a published relay left the review unconsumable")
	}
	ev := relayEvidence(t, rec)
	if ev.State != reviewstore.Ready || ev.RelayPrincipal != operator.token() || ev.PublicationComment <= 0 {
		t.Fatalf("the relay evidence conflated its parties or its state: %+v", ev)
	}
	// The three parties, as three separate facts on that one row.
	if ev.RelayPrincipal == ev.Publication || strings.Contains(ev.RelayPrincipal, "chatgpt") ||
		strings.Contains(ev.Publication, "dave") {
		t.Fatalf("reviewer, relay principal and publisher are not three distinct facts: %+v", ev)
	}

	posted := f.mailbox.posted()
	if len(posted) != 1 {
		t.Fatalf("want exactly one publication, got %d", len(posted))
	}
	body := posted[0]
	for _, want := range []string{
		relayedReviewMarker, "request=" + relayRequest, "candidate_digest=" + relaySubject.CandidateDigest,
		"candidate_tree=" + relaySubject.CandidateTree, "base=" + relaySubject.BaseSHA,
		"review_commit=" + relaySubject.ReviewCommit, "reviewer_provider=chatgpt",
		"review_digest=" + ReviewDigest(artifact), "relay_principal=uid:1000,user:dave,pid:4242,terminal:34816",
		"publication=github-app:4850747:installation:159521273:globulario/sensei-code", "standing=advisory",
		"Decision: accept", "did not author it",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the publication does not carry %q:\n%s", want, body)
		}
	}
	// The receipt is transport, not an answer: the canonical parser must refuse
	// it, and it must carry no review envelope at all.
	if _, perr := reviewartifact.Parse(body); perr == nil || strings.Contains(body, reviewartifact.Marker) {
		t.Fatalf("the publication can be read as a review answer:\n%s", body)
	}
	if res.PublicationComment <= 0 {
		t.Fatalf("the result does not name its publication comment")
	}
}

// Anything that is not the exact review of the owed request is refused whole:
// nothing is staged and nothing is published.
func TestARelayIsRefusedForTheWrongCandidateRequestOrDigest(t *testing.T) {
	with := func(mut func(*Subject)) Subject { s := relaySubject; mut(&s); return s }
	for name, artifact := range map[string]string{
		"a request that is not owed":   artifactFor(t, relaySubject, "r-ffffffffffffffff", "chatgpt", acceptPayload),
		"a different candidate digest": artifactFor(t, with(func(s *Subject) { s.CandidateDigest = "sha256:" + strings.Repeat("0", 64) }), relayRequest, "chatgpt", acceptPayload),
		"a different candidate tree":   artifactFor(t, with(func(s *Subject) { s.CandidateTree = strings.Repeat("a", 40) }), relayRequest, "chatgpt", acceptPayload),
		"a different base":             artifactFor(t, with(func(s *Subject) { s.BaseSHA = strings.Repeat("b", 40) }), relayRequest, "chatgpt", acceptPayload),
		"a different review commit":    artifactFor(t, with(func(s *Subject) { s.ReviewCommit = strings.Repeat("c", 40) }), relayRequest, "chatgpt", acceptPayload),
		"a different task":             artifactFor(t, with(func(s *Subject) { s.TaskID = "task-other" }), relayRequest, "chatgpt", acceptPayload),
		"no reviewer named":            artifactFor(t, relaySubject, relayRequest, "", acceptPayload),
		"prose instead of a verdict":   artifactFor(t, relaySubject, relayRequest, "chatgpt", "LGTM, ship it"),
		"a second envelope inside":     artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload+"\n[sensei-code:review-request]"),
		"not starting at the envelope": "preface\n" + artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload),
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			res, err := f.submit(artifact)
			if !errors.Is(err, ErrRelayRefused) {
				t.Fatalf("err = %v (state %q), want a refusal", err, res.State)
			}
			if _, found := f.stored(t); found {
				t.Fatal("a refused relay staged a review")
			}
			if n := len(f.mailbox.posted()); n != 0 {
				t.Fatalf("a refused relay published %d comment(s)", n)
			}
		})
	}
}

// The relay cannot edit a review: a different artifact for a request that
// already has one is refused, the stored bytes are untouched, and the identical
// artifact publishes nothing further.
func TestAStoredReviewIsNeverReplacedByADifferentOne(t *testing.T) {
	f := newRelayFixture(t)
	original := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(original); err != nil {
		t.Fatal(err)
	}
	edited := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"edited on the way through","instructions":"","findings":[]}`)
	if _, err := f.submit(edited); !errors.Is(err, ErrRelayRefused) || !errors.Is(err, reviewstore.ErrConflict) {
		t.Fatalf("a different review for an answered request was not refused as a conflict: %v", err)
	}
	rec, _ := f.stored(t)
	if rec.ArtifactRaw != original {
		t.Fatal("the conflicting submission replaced the stored bytes")
	}
	res, err := f.submit(original)
	// The publication count FIRST. A resubmission that republishes also trips
	// the store's already-delivered guard a moment later, and reporting that
	// instead would name the backstop rather than the rule.
	if n := len(f.mailbox.posted()); n != 1 {
		t.Fatalf("the stored review was published %d times, want once (err=%v)", n, err)
	}
	if err != nil || res.ReviewDigest != ReviewDigest(original) {
		t.Fatalf("resubmitting the stored artifact: digest %s err %v", res.ReviewDigest, err)
	}
}

// A STAGED review is durable, is not consumable, and is not silence.
//
// This is the whole reason the relay's own store could be deleted. A relay that
// validated and could not publish used to keep its meaning in a second record
// with its own two-state lifecycle; it now keeps one fact -- that this delivery
// has not completed -- on the one review record, and the runner reads that fact
// rather than a second store.
func TestAPublicationFailureLeavesTheReviewStagedAndUnconsumable(t *testing.T) {
	f := newRelayFixture(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	f.mailbox.failPosts = true
	res, err := f.submit(artifact)
	if !errors.Is(err, ErrRelayPublication) {
		t.Fatalf("a failed publication reported %v", err)
	}
	if res.State != RelayStaged {
		t.Fatalf("a failed publication reported state %q", res.State)
	}

	// The exact bytes survive, and nothing about them claims a delivery.
	rec, found := f.stored(t)
	if !found || rec.ArtifactRaw != artifact {
		t.Fatalf("the staged review was not preserved: found=%v", found)
	}
	if rec.Consumable() {
		t.Fatal("a review nobody published is consumable")
	}
	ev := relayEvidence(t, rec)
	if ev.State != reviewstore.Pending || ev.Publication != "" || ev.PublicationComment != 0 {
		t.Fatalf("staged evidence claims a publication: %+v", ev)
	}

	// A review turn meanwhile: the SAME owed request, no reviewer asked, and
	// the truth reported is DELIVERY_PENDING -- not silence.
	_, err = f.runner().Run(context.Background(), f.turn(), nil)
	var fault *roles.ReviewObservationFault
	if !errors.As(err, &fault) {
		t.Fatalf("a staged review was not reported as an observation: %v", err)
	}
	// THE SEAM the workflow branches on. The engine routes observation faults by
	// this sentinel, so what the runner produces here is what reaches the
	// no-fallback / no-handoff / resumable-terminal path -- and it must not
	// satisfy any of the neighbouring conditions, which have other control
	// actions.
	if !errors.Is(err, roles.ErrReviewObservationFault) {
		t.Fatalf("the fault does not reach the engine's observation branch: %v", err)
	}
	for _, neighbour := range []error{
		roles.ErrReviewUnanswered, roles.ErrReviewUnobtainable,
		roles.ErrReviewUnrecordable, roles.ErrReviewLifecycleFault,
	} {
		if errors.Is(err, neighbour) {
			t.Fatalf("a review this process is holding also reads as %v: %v", neighbour, err)
		}
	}
	if !fault.Has(roles.ObservedDeliveryPending) || fault.RequestID != relayRequest {
		t.Fatalf("observation kinds %v for request %s", fault.Kinds(), fault.RequestID)
	}
	obs := fault.Observations[0]
	if obs.Transport != string(reviewstore.LocalRelay) || obs.RelayPrincipal != operator.token() {
		t.Fatalf("the observation does not name the delivery it is about: %+v", obs)
	}
	if obs.Comment != 0 {
		t.Fatalf("a staged relay was given a comment locator (%d) nobody posted", obs.Comment)
	}
	if obs.ArtifactDigest != ReviewDigest(artifact) {
		t.Fatalf("the observation names review %s, want %s", obs.ArtifactDigest, ReviewDigest(artifact))
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 1 || pending[0].RequestID != relayRequest {
		t.Fatalf("the owed request was superseded or closed: %+v", pending)
	}

	// Retrying the SAME artifact completes the delivery, publishes once, and
	// changes no bytes.
	f.mailbox.failPosts = false
	if res, err = f.submit(artifact); err != nil || res.State != RelayPublished {
		t.Fatalf("retrying publication: state %q err %v", res.State, err)
	}
	after, _ := f.stored(t)
	if after.ArtifactRaw != artifact || !after.Consumable() {
		t.Fatalf("the completed delivery changed the bytes or did not complete: consumable=%v", after.Consumable())
	}
	if n := len(f.mailbox.posted()); n != 1 {
		t.Fatalf("the retry published %d comments, want exactly one", n)
	}
	out, err := f.runner().Run(context.Background(), f.turn(), nil)
	if err != nil {
		t.Fatalf("the completed relay was not consumed: %v", err)
	}
	if out.Session != roles.Unverified || strings.TrimSpace(out.Text) != acceptPayload {
		t.Fatalf("consumed session=%q text=%q", out.Session, out.Text)
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 0 {
		t.Fatalf("the answered request stayed owed: %+v", pending)
	}
}

// A retry from a DIFFERENT terminal continues the delivery that is already
// staged. It does not stage a second one and does not rewrite who carried it.
func TestARetryFromAnotherTerminalContinuesTheSameDelivery(t *testing.T) {
	f := newRelayFixture(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	f.mailbox.failPosts = true
	if _, err := f.submit(artifact); !errors.Is(err, ErrRelayPublication) {
		t.Fatalf("staging: %v", err)
	}
	f.mailbox.failPosts = false

	second := RelayPrincipal{UID: 1001, User: "someone-else", PID: 9999, Terminal: 34999}
	if _, err := f.submitAs(artifact, second); err != nil {
		t.Fatalf("the second terminal could not continue the delivery: %v", err)
	}
	rec, _ := f.stored(t)
	ev := relayEvidence(t, rec)
	if ev.RelayPrincipal != operator.token() {
		t.Fatalf("a retry rewrote who staged the delivery: %q", ev.RelayPrincipal)
	}
	if !ev.Delivered() {
		t.Fatal("the continued delivery did not complete")
	}
	posted := f.mailbox.posted()
	if len(posted) != 1 {
		t.Fatalf("continuing one delivery published %d comments", len(posted))
	}
	// ONE DELIVERY, ONE PRINCIPAL. The record keeps A; the publication must say
	// A too, or the mailbox and the durable record name different people for the
	// same act.
	if !strings.Contains(posted[0], "relay_principal="+operator.token()) {
		t.Fatalf("the publication does not name the terminal that staged the delivery:\n%s", posted[0])
	}
	if strings.Contains(posted[0], second.token()) {
		t.Fatalf("the publication names the retrying terminal instead of the one that staged it:\n%s", posted[0])
	}
}

// A receipt this machine did not write establishes nothing.
//
// The recovery path exists because a process can die between posting a relay
// receipt and recording that delivery. Reading the mailbox AS the App
// authenticates the READER; the marker, the request id and the digest are all
// public, so without an author check anybody who can comment on the conversation
// could promote a review this process is holding but never published.
func TestAForgedReceiptCannotPromoteAStagedReview(t *testing.T) {
	f := newRelayFixture(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	// Staged and undelivered.
	f.mailbox.failPosts = true
	if _, err := f.submit(artifact); !errors.Is(err, ErrRelayPublication) {
		t.Fatalf("staging: %v", err)
	}
	f.mailbox.failPosts = false

	// Somebody who is NOT this machine posts a perfect-looking receipt: the
	// right marker, the right request, the right digest.
	forged, err := RenderRelayedReview(mustParse(t, artifact), acceptVerdict(t, artifact), operator.token(), f.box)
	if err != nil {
		t.Fatal(err)
	}
	forgedID := f.mailbox.add(forged, "davecourtois", 1697116)
	before := len(f.mailbox.posted())
	// Two separate claims, so a failure says which one broke.
	match, ferr := relayPublicationOf(context.Background(), f.box,
		obligationFrom(soleObligation(t, f.exchanges)), ReviewDigest(artifact))
	if ferr != nil || match.comment != forgedID {
		// The fixture: without a matching receipt there is nothing to reject,
		// and a pass would mean only that nothing was ever found.
		t.Fatalf("the forged receipt does not match this request and digest: %+v err=%v", match, ferr)
	}
	if match.ours {
		// THE PROPERTY. An account that is not this machine's publisher
		// authenticated as it, so any comment carrying the right marker,
		// request and digest could complete a delivery.
		t.Fatalf("a receipt written by %s authenticated as this machine's publication", match.author)
	}
	if match.unauthenticatable {
		t.Fatalf("the obligation pins no publisher, so this case is not testing the author check: %+v", match)
	}

	res, err := f.submit(artifact)
	if err != nil {
		t.Fatalf("a forged receipt blocked the genuine publication: %v", err)
	}
	rec, _ := f.stored(t)
	ev := relayEvidence(t, rec)
	// Promoted by OUR publication, not by the forgery.
	if !ev.Delivered() {
		t.Fatal("the genuine publication did not complete the delivery")
	}
	if ev.PublicationComment == forgedID {
		t.Fatalf("the forged comment %d was recorded as this review's publication", ev.PublicationComment)
	}
	if n := len(f.mailbox.posted()); n != before+1 {
		t.Fatalf("the genuine receipt was not published: %d comments, want %d", n, before+1)
	}
	if res.PublicationComment != ev.PublicationComment {
		t.Fatalf("the result names comment %d and the record names %d", res.PublicationComment, ev.PublicationComment)
	}
}

// A matching receipt that CANNOT be authenticated is refused, not trusted and
// not duplicated.
//
// An obligation published before the publisher was pinned has nothing to
// authenticate against. Promoting the receipt would trust a stranger; posting a
// second one beside it would put two receipts on the mailbox for one review.
// Neither is honest, so the turn stops and says why.
func TestAnUnauthenticatableReceiptIsRefusedRatherThanTrustedOrDuplicated(t *testing.T) {
	f := newRelayFixture(t)
	// The obligation predates the pinned publisher.
	rec := soleObligation(t, f.exchanges)
	if err := f.exchanges.Close(rec.TaskID, rec.RequestID); err != nil {
		t.Fatal(err)
	}
	rec.PublisherID, rec.PublisherLogin = 0, ""
	if err := f.exchanges.Open(rec); err != nil {
		t.Fatal(err)
	}

	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	f.mailbox.failPosts = true
	if _, err := f.submit(artifact); !errors.Is(err, ErrRelayPublication) {
		t.Fatalf("staging: %v", err)
	}
	f.mailbox.failPosts = false
	receipt, err := RenderRelayedReview(mustParse(t, artifact), acceptVerdict(t, artifact), operator.token(), f.box)
	if err != nil {
		t.Fatal(err)
	}
	f.mailbox.add(receipt, "globulario-sensei-code[bot]", 99887766)
	before := len(f.mailbox.posted())

	_, err = f.submit(artifact)
	if !errors.Is(err, ErrRelayConvergence) {
		t.Fatalf("err = %v, want a convergence refusal", err)
	}
	if !strings.Contains(err.Error(), "predates the recorded publisher") {
		t.Fatalf("the refusal does not say why it cannot be established: %v", err)
	}
	if n := len(f.mailbox.posted()); n != before {
		t.Fatalf("an unauthenticatable receipt led to %d further publication(s)", n-before)
	}
	stored, _ := f.stored(t)
	if stored.Consumable() {
		t.Fatal("a receipt nobody could authenticate promoted the review")
	}
	if stored.ArtifactRaw != artifact {
		t.Fatal("the refused recovery changed the staged bytes")
	}
}

// mustParse is the canonical artifact or a failed test.
func mustParse(t *testing.T, raw string) reviewartifact.Artifact {
	t.Helper()
	art, err := reviewartifact.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return art
}

// acceptVerdict reads the verdict out of an artifact through the one parser that
// reads verdicts.
func acceptVerdict(t *testing.T, raw string) roles.ReviewVerdict {
	t.Helper()
	art := mustParse(t, raw)
	v, err := workflow.ValidateReviewBody(art.Body, roles.Binding{
		TaskID: art.TaskID, BaseSHA: art.BaseSHA,
		CandidateDigest: art.CandidateDigest, CandidateTree: art.CandidateTree,
	}, art.ReviewerProvider)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// A publication that succeeded and whose completion did not is repaired by
// finding the EXISTING receipt, never by posting a second one.
func TestACompletionFailureIsRepairedFromTheExistingPublication(t *testing.T) {
	f := newRelayFixture(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	// The crash edge, reproduced exactly: the App HAS published this review and
	// the record still says pending.
	if _, err := f.submit(artifact); err != nil {
		t.Fatal(err)
	}
	if err := stageBackTo(f.reviews, relayRequest); err != nil {
		t.Fatal(err)
	}
	if rec, _ := f.stored(t); rec.Consumable() {
		t.Fatal("the fixture did not reproduce an incomplete delivery")
	}

	res, err := f.submit(artifact)
	if err != nil {
		t.Fatalf("the repair failed: %v", err)
	}
	if res.State != RelayPublished {
		t.Fatalf("the repair reported state %q", res.State)
	}
	if n := len(f.mailbox.posted()); n != 1 {
		t.Fatalf("the repair published a second receipt: %d comments", n)
	}
	rec, _ := f.stored(t)
	if !rec.Consumable() {
		t.Fatal("the repair did not complete the delivery")
	}
}

// A stored review is consumed on what can be RE-DERIVED from it, so a record
// that cannot prove its own binding is refused however it got there.
//
// This is what replaced a live mailbox re-check. Re-proving delivery at
// consumption would have made an accepted review evaporate when a comment was
// deleted or GitHub was down; re-deriving the binding costs nothing and catches
// the thing that actually matters -- a record that is not about this candidate,
// or not from the reviewer this obligation asked.
func TestAStoredReviewThatCannotProveItsBindingIsNotConsumed(t *testing.T) {
	for name, raw := range map[string]string{
		"another candidate tree": artifactFor(t, func() Subject {
			s := relaySubject
			s.CandidateTree = strings.Repeat("a", 40)
			return s
		}(), relayRequest, "chatgpt", acceptPayload),
		"another base": artifactFor(t, func() Subject {
			s := relaySubject
			s.BaseSHA = strings.Repeat("b", 40)
			return s
		}(), relayRequest, "chatgpt", acceptPayload),
		"another review commit": artifactFor(t, func() Subject {
			s := relaySubject
			s.ReviewCommit = strings.Repeat("c", 40)
			return s
		}(), relayRequest, "chatgpt", acceptPayload),
		"a provider this obligation did not ask": artifactFor(t, relaySubject, relayRequest, "claude", acceptPayload),
		"a body the reviewer contract refuses":   artifactFor(t, relaySubject, relayRequest, "chatgpt", "LGTM, ship it"),
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			if err := writeStoredReview(f.reviews, relayRequest, raw, reviewstore.Evidence{
				Transport: reviewstore.GitHubMailbox, State: reviewstore.Ready, GitHubAuthor: "davecourtois",
				GitHubAuthorID: 1697116, GitHubComment: 4242,
			}); err != nil {
				t.Fatal(err)
			}
			res, err := f.runner().Run(context.Background(), f.turn(), nil)
			var owed *roles.ReviewUnanswered
			if !errors.As(err, &owed) || owed.RequestID != relayRequest {
				t.Fatalf("a record that cannot prove its binding answered the turn: text=%q err=%v", res.Text, err)
			}
			if pending, _ := f.exchanges.PendingReviews(); len(pending) != 1 {
				t.Fatalf("the owed request did not survive: %+v", pending)
			}
		})
	}
}

// A record written straight to disk with an incomplete delivery is not an
// answer, whoever wrote it. Consumability is the store's to decide and it is
// decided the same way for a hand-written record as for a staged one.
func TestAHandWrittenStagedRecordIsNotAnAnswer(t *testing.T) {
	f := newRelayFixture(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if err := writeStoredReview(f.reviews, relayRequest, raw, reviewstore.Evidence{
		Transport: reviewstore.LocalRelay, State: reviewstore.Pending,
		RelayPrincipal: operator.token(),
	}); err != nil {
		t.Fatal(err)
	}
	res, err := f.runner().Run(context.Background(), f.turn(), nil)
	var fault *roles.ReviewObservationFault
	if !errors.As(err, &fault) || !fault.Has(roles.ObservedDeliveryPending) {
		t.Fatalf("an undelivered record answered the turn: text=%q err=%v", res.Text, err)
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 1 {
		t.Fatalf("the owed request did not survive: %+v", pending)
	}
}

// unreachableMailbox is the configured conversation with a GitHub that cannot
// be reached at all. Any attempt to read or post through it fails.
func unreachableMailbox(box Issue) Issue {
	out := box
	if out.API == nil || out.API.Auth == nil {
		return out
	}
	// A FRESH auth aimed at a closed port, never a copy: InstallationAuth
	// carries a mutex and a cached token, and copying it would both trip vet and
	// hand this mailbox a token minted against the live fixture.
	out.API = &AppClient{
		Auth: &InstallationAuth{
			AppID:          out.API.Auth.AppID,
			InstallationID: out.API.Auth.InstallationID,
			PrivateKeyPath: out.API.Auth.PrivateKeyPath,
			APIBase:        "http://127.0.0.1:1",
		},
		Owner: out.API.Owner,
		Repo:  out.API.Repo,
	}
	return out
}

// An accepted review survives the transport that delivered it.
//
// Transport establishes acceptance ONCE. A GitHub comment that is deleted or
// edited, a conversation that moves, an outage -- none of them may retract a
// review that was already authenticated, bound, validated and durably recorded.
// If they could, this store would be a cache of GitHub rather than the review's
// durable semantic record, and every later slice would inherit that.
func TestAnAcceptedReviewSurvivesTheTransportThatCarriedIt(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	for name, deliver := range map[string]func(t *testing.T, f relayFixture){
		"read off the mailbox": func(t *testing.T, f relayFixture) {
			if _, err := f.reviews.Accept(reviewstore.Acceptance{
				RequestID: relayRequest, Artifact: raw,
				Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox, State: reviewstore.Ready,
					GitHubAuthor: "davecourtois", GitHubAuthorID: 1697116, GitHubComment: 5150},
				Validate: func(a reviewartifact.Artifact) error {
					_, err := workflow.ValidateReviewBody(a.Body, roles.Binding{
						TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
						CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree,
					}, a.ReviewerProvider)
					return err
				},
			}); err != nil {
				t.Fatalf("accepting a mailbox review: %v", err)
			}
		},
		"carried by a terminal and published": func(t *testing.T, f relayFixture) {
			if _, err := f.submit(raw); err != nil {
				t.Fatalf("the genuine relay did not publish: %v", err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			deliver(t, f)

			// The comment is gone and GitHub is unreachable.
			runner := f.runner()
			runner.Issue = unreachableMailbox(f.box)
			if _, err := Reviews(context.Background(), runner.Issue, runner.Issue.ExpectedReviewer); err == nil {
				t.Fatal("the fixture mailbox is still readable; this test proves nothing about an outage")
			}

			res, err := runner.Run(context.Background(), f.turn(), nil)
			if err != nil {
				t.Fatalf("an accepted review stopped being consumable when GitHub went away: %v", err)
			}
			if res.ReviewDigest != ReviewDigest(raw) {
				t.Fatalf("consumed digest %s, want %s", res.ReviewDigest, ReviewDigest(raw))
			}
			if res.Session != roles.Unverified {
				t.Fatalf("a consumed review claimed session %q", res.Session)
			}
			if pending, _ := f.exchanges.PendingReviews(); len(pending) != 0 {
				t.Fatalf("the answered obligation is still owed: %+v", pending)
			}
		})
	}
}

// writeStoredReview puts a record into the one store the way a process OUTSIDE
// any adapter would: straight onto disk, with no delivery behind it.
func writeStoredReview(s reviewstore.Store, requestID, raw string, ev reviewstore.Evidence) error {
	rec := reviewstore.Record{
		Version: reviewstore.SchemaVersion, RequestID: requestID, ArtifactRaw: raw,
		ReviewDigest: reviewartifact.Digest(raw), Standing: reviewstore.Advisory,
		AcceptedAt: time.Now().UTC(),
	}
	if ev.Transport != "" {
		if ev.ObservedAt.IsZero() {
			ev.ObservedAt = time.Now().UTC()
		}
		rec.Evidence = []reviewstore.Evidence{ev}
	}
	return writeRecordFile(s, requestID, rec)
}

func writeRecordFile(s reviewstore.Store, requestID string, rec reviewstore.Record) error {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, requestID+".json"), append(blob, '\n'), 0o600)
}

// stageBackTo rewinds a completed delivery to pending, WITHOUT touching the
// artifact, to reproduce a process that died between publishing and recording.
//
// The mailbox keeps its publication: that is the state being reproduced.
func stageBackTo(s reviewstore.Store, requestID string) error {
	rec, found, err := s.Load(requestID)
	if err != nil || !found {
		return fmt.Errorf("load %s: found=%v err=%v", requestID, found, err)
	}
	for i, ev := range rec.Evidence {
		if ev.Transport != reviewstore.LocalRelay {
			continue
		}
		ev.State = reviewstore.Pending
		ev.Publication, ev.PublicationComment, ev.PublishedAt = "", 0, time.Time{}
		rec.Evidence[i] = ev
	}
	return writeRecordFile(s, requestID, rec)
}

// Structural: the review runner's path cannot create, accept or publish a relay.
// The only writer is AcceptRelayedReview, called by the control process's relay
// socket handler with the principal that socket judged.
func TestTheReviewRunnerCannotAuthorOrPublishARelay(t *testing.T) {
	fset := token.NewFileSet()
	forbidden := map[string]bool{"AcceptRelayedReview": true, "RenderRelayedReview": true,
		"Complete": true, "PostComment": true, "PublishRequest": true}
	want := map[string]bool{"storedReviewFor": true, "deliveryPending": true}
	checked := 0
	for _, file := range []string{"relay.go", "runner.go", "review_observation.go"} {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || !want[fn.Name.Name] {
				continue
			}
			checked++
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				name := ""
				switch f := call.Fun.(type) {
				case *ast.Ident:
					name = f.Name
				case *ast.SelectorExpr:
					name = f.Sel.Name
				}
				if forbidden[name] {
					t.Errorf("%s calls %s: the runner's review path must only read", fn.Name.Name, name)
				}
				return true
			})
		}
	}
	if checked != len(want) {
		t.Fatalf("inspected %d of the %d review read functions; the check proves nothing", checked, len(want))
	}
}

// A stored review is consumed against the OBLIGATION's assignment, never
// against today's configuration.
//
// The question the request asked is the question that was answered. A review
// already accepted for it is not invalidated because the workflow would now ask
// somebody else -- and a runner that checked the current assignment would
// re-open a settled question every time the configuration moved.
func TestAStoredReviewIsConsumedAgainstTheObligationsProviderNotTodaysConfig(t *testing.T) {
	f := newRelayFixture(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if err := writeStoredReview(f.reviews, relayRequest, raw, reviewstore.Evidence{
		Transport: reviewstore.GitHubMailbox, State: reviewstore.Ready, GitHubAuthor: "davecourtois",
		GitHubAuthorID: 1697116, GitHubComment: 4242,
	}); err != nil {
		t.Fatal(err)
	}
	// The precondition: the obligation was published to chatgpt, and this
	// process is now configured for somebody else entirely.
	owed := soleObligation(t, f.exchanges)
	if owed.ReviewerProvider != "chatgpt" {
		t.Fatalf("the obligation names %q; this case needs the assignment that was made", owed.ReviewerProvider)
	}
	runner := f.runner()
	runner.ReviewerProvider = "claude"

	res, err := runner.Run(context.Background(), f.turn(), nil)
	if err != nil {
		t.Fatalf("a review already accepted for the question that was asked was refused: %v", err)
	}
	if res.ReviewDigest != ReviewDigest(raw) {
		t.Fatalf("consumed %s, want the accepted %s", res.ReviewDigest, ReviewDigest(raw))
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 0 {
		t.Fatalf("the answered obligation is still owed: %+v", pending)
	}
	// And nothing was published: a config change must not ask the question again.
	for _, body := range f.mailbox.posted() {
		if strings.Contains(body, requestMarker) {
			t.Fatalf("a configuration change republished the review request:\n%s", body)
		}
	}
}

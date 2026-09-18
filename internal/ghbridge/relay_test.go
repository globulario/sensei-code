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
		"id": m.nextID, "body": body, "user": map[string]any{"login": login, "id": id},
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

func artifactFor(t *testing.T, s Subject, requestID, provider, payload string) string {
	t.Helper()
	marker, err := Review{Subject: s, RequestID: requestID}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	out := marker
	if provider != "" {
		out += "reviewer=" + provider + "\n"
	}
	return out + payload
}

const acceptPayload = `{"decision":"accept","summary":"the ledger invariant holds at physical position 0","instructions":"","findings":[]}`

type relayFixture struct {
	box       Issue
	mailbox   *relayMailbox
	exchanges ExchangeLog
	store     RelayStore
	reviews   reviewstore.Store
}

func newRelayFixture(t *testing.T) relayFixture {
	t.Helper()
	m, box := newRelayMailbox(t)
	dir := t.TempDir()
	f := relayFixture{box: box, mailbox: m,
		exchanges: ExchangeLog{Dir: filepath.Join(dir, "exchanges")},
		store:     RelayStore{Dir: filepath.Join(dir, "relays")},
		reviews:   reviewstore.Store{Dir: filepath.Join(dir, "reviews")}}
	if err := f.exchanges.Open(ExchangeRecord{
		TaskID: relaySubject.TaskID, RequestID: relayRequest, RequestComment: 5686428018, Conversation: "157",
		PublishedAt: time.Now().Add(-time.Hour).UTC(), Kind: ExchangeReview,
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
		ReviewerProvider: "chatgpt",
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f relayFixture) submit(artifact string) (RelayRecord, error) {
	return AcceptRelayedReview(context.Background(), RelaySubmission{
		Artifact: artifact, Principal: operator, Exchanges: f.exchanges, Store: f.store,
		Reviews: f.reviews, Mailbox: f.box,
	})
}

func (f relayFixture) runner() *Runner {
	return &Runner{Issue: f.box, NewRequestID: NewRequestID, Poll: 10 * time.Millisecond,
		Wait: 50 * time.Millisecond, Exchanges: f.exchanges, Relays: f.store, Reviews: f.reviews,
		ReviewerProvider: "chatgpt"}
}

func (f relayFixture) turn() agent.Request {
	return agent.Request{Role: roles.Reviewer, TaskID: relaySubject.TaskID, Binding: roles.Binding{
		TaskID: relaySubject.TaskID, BaseSHA: relaySubject.BaseSHA,
		CandidateDigest: relaySubject.CandidateDigest, CandidateTree: relaySubject.CandidateTree}}
}

// The exact matching review is accepted as submitted, and the App publishes it
// with the reviewer, the relay principal and the publisher kept apart.
func TestATerminalRelayAcceptsTheExactReviewAndTheAppPublishesItSeparately(t *testing.T) {
	f := newRelayFixture(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	rec, err := f.submit(artifact)
	if err != nil {
		t.Fatalf("the exact matching review was refused: %v", err)
	}
	if rec.State != RelayPublished || rec.Artifact != artifact || rec.ReviewDigest != ReviewDigest(artifact) {
		t.Fatalf("receipt state=%s digest=%s, artifact kept exactly=%v", rec.State, rec.ReviewDigest, rec.Artifact == artifact)
	}
	if rec.Reviewer != "chatgpt" || rec.RelayPrincipal != operator || rec.Standing != "advisory" {
		t.Fatalf("the receipt conflated its parties: reviewer=%q principal=%+v standing=%q", rec.Reviewer, rec.RelayPrincipal, rec.Standing)
	}
	if !rec.Subject().Same(relaySubject) || rec.RequestID != relayRequest {
		t.Fatalf("the receipt is not bound to the owed request: %+v", rec.Subject())
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
	if _, ok := ParseReview(body, "davecourtois"); ok || strings.Contains(body, reviewMarker) {
		t.Fatalf("the publication can be read as a review answer:\n%s", body)
	}
	if rec.PublicationComment <= 0 {
		t.Fatalf("the receipt does not name its publication comment")
	}
}

// Anything that is not the exact review of the owed request is refused whole:
// nothing is recorded and nothing is published.
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
			rec, err := f.submit(artifact)
			if !errors.Is(err, ErrRelayRefused) {
				t.Fatalf("err = %v (state %q), want a refusal", err, rec.State)
			}
			if _, found, _ := f.store.Load(relayRequest); found {
				t.Fatal("a refused relay left a receipt")
			}
			if n := len(f.mailbox.posted()); n != 0 {
				t.Fatalf("a refused relay published %d comment(s)", n)
			}
		})
	}
}

// The relay cannot edit a review: a different artifact for a request that
// already has an accepted one is refused, and the identical artifact is a no-op.
func TestAnAcceptedReviewIsNeverReplacedByADifferentOne(t *testing.T) {
	f := newRelayFixture(t)
	original := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(original); err != nil {
		t.Fatal(err)
	}
	edited := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"edited on the way through","instructions":"","findings":[]}`)
	if _, err := f.submit(edited); !errors.Is(err, ErrRelayRefused) {
		t.Fatalf("a different review for an accepted request was not refused: %v", err)
	}
	rec, err := f.submit(original)
	if err != nil || rec.ReviewDigest != ReviewDigest(original) {
		t.Fatalf("resubmitting the accepted artifact: digest %s err %v", rec.ReviewDigest, err)
	}
	if n := len(f.mailbox.posted()); n != 1 {
		t.Fatalf("the accepted review was published %d times, want once", n)
	}
}

// A publication that fails loses nothing: the accepted review stays durable,
// the owed request is neither consumed nor superseded, and resubmitting the same
// artifact publishes it so the next review turn consumes it.
func TestAPublicationFailurePreservesTheAcceptedReviewForRetry(t *testing.T) {
	f := newRelayFixture(t)
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)

	f.mailbox.failPosts = true
	rec, err := f.submit(artifact)
	if !errors.Is(err, ErrRelayPublication) {
		t.Fatalf("a failed publication reported %v", err)
	}
	stored, found, _ := f.store.Load(relayRequest)
	if !found || stored.State != RelayAccepted || stored.Artifact != artifact || rec.State != RelayAccepted {
		t.Fatalf("the accepted review was not preserved: found=%v state=%q", found, stored.State)
	}

	// A review turn meanwhile keeps the SAME owed request, and asks nobody.
	_, err = f.runner().Run(context.Background(), f.turn(), nil)
	var owed *roles.ReviewUnanswered
	if !errors.As(err, &owed) || owed.RequestID != relayRequest {
		t.Fatalf("an unpublished relay did not keep its request owed: %v", err)
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 1 || pending[0].RequestID != relayRequest {
		t.Fatalf("the owed request was superseded or closed: %+v", pending)
	}

	f.mailbox.failPosts = false
	if rec, err = f.submit(artifact); err != nil || rec.State != RelayPublished {
		t.Fatalf("retrying publication: state %q err %v", rec.State, err)
	}
	res, err := f.runner().Run(context.Background(), f.turn(), nil)
	if err != nil {
		t.Fatalf("the published relay was not consumed: %v", err)
	}
	if res.Session != roles.Unverified || strings.TrimSpace(res.Text) != acceptPayload {
		t.Fatalf("consumed session=%q text=%q", res.Session, res.Text)
	}
	if pending, _ := f.exchanges.PendingReviews(); len(pending) != 0 {
		t.Fatalf("the answered request stayed owed: %+v", pending)
	}
	for _, body := range f.mailbox.posted() {
		if strings.Contains(body, requestMarker) {
			t.Fatalf("consuming a relay published a new review request:\n%s", body)
		}
	}
}

// The runner never manufactures an accepted review. A receipt that did not come
// through the relay handler's publication -- hand-written, pointing at a comment
// that is not its publication, or holding bytes that are not what it names -- is
// not consumed, and the request stays owed.
//
// R2 note: these are the cases where NO genuine publication ever happened, so
// nothing on the mailbox corroborates the receipt. What changed in R2 is only
// which store answers the turn; a review nothing outside this machine
// corroborates is still not an answer.
func TestTheRunnerDoesNotConsumeAReceiptTheRelayHandlerDidNotPublish(t *testing.T) {
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	forged := RelayRecord{Version: 1, State: RelayPublished, TaskID: relaySubject.TaskID, RequestID: relayRequest,
		Conversation: "157", BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
		Reviewer: "chatgpt", ReviewDigest: ReviewDigest(artifact), Decision: "accept", Artifact: artifact,
		Standing: "advisory", RelayPrincipal: operator, PublicationComment: 777}

	swapped := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"swapped","instructions":"","findings":[]}`)

	for name, setup := range map[string]func(t *testing.T, f relayFixture) RelayRecord{
		"no such publication comment": func(*testing.T, relayFixture) RelayRecord { return forged },
		"a comment that is not the publication": func(_ *testing.T, f relayFixture) RelayRecord {
			r := forged
			r.PublicationComment = f.mailbox.add(artifact, "globulario-sensei-code[bot]", 99887766)
			return r
		},
		"bytes that are not what the receipt names": func(*testing.T, relayFixture) RelayRecord {
			r := forged
			r.Artifact = swapped
			return r
		},
		"accepted and never published": func(*testing.T, relayFixture) RelayRecord {
			r := forged
			r.State, r.PublicationComment = RelayAccepted, 0
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			if err := overwriteReceipt(f.store, setup(t, f)); err != nil {
				t.Fatal(err)
			}
			res, err := f.runner().Run(context.Background(), f.turn(), nil)
			var owed *roles.ReviewUnanswered
			if !errors.As(err, &owed) || owed.RequestID != relayRequest {
				t.Fatalf("a receipt the handler did not publish answered the turn: text=%q err=%v", res.Text, err)
			}
			if pending, _ := f.exchanges.PendingReviews(); len(pending) != 1 {
				t.Fatalf("the owed request did not survive: %+v", pending)
			}
		})
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
				Transport: reviewstore.GitHubMailbox, GitHubAuthor: "davecourtois",
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
func TestAnAcceptedMailboxReviewSurvivesItsCommentAndTheMailbox(t *testing.T) {
	f := newRelayFixture(t)
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	// Accepted the way the mailbox adapter accepts one: exact bytes, GitHub's
	// transport facts recorded beside them.
	if _, err := f.reviews.Accept(reviewstore.Acceptance{
		RequestID: relayRequest, Artifact: raw,
		Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox,
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

	// The comment is gone and GitHub is unreachable.
	runner := f.runner()
	runner.Issue = unreachableMailbox(f.box)
	if _, err := Reviews(context.Background(), runner.Issue); err == nil {
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
}

// The same law for a published relay: once the canonical artifact is accepted,
// the relay receipt and the mailbox are history, not preconditions.
//
// RelayStore keeps owning retry and publication state. It no longer owns what a
// review MEANS, so deleting or rewriting it afterwards cannot erase the accepted
// review or substitute different bytes for it.
func TestAnAcceptedRelayedReviewSurvivesItsReceiptAndTheMailbox(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	swapped := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"swapped","instructions":"","findings":[]}`)

	for name, wreck := range map[string]func(t *testing.T, f relayFixture){
		"the relay receipt is deleted": func(t *testing.T, f relayFixture) {
			if err := os.Remove(filepath.Join(f.store.Dir, relayRequest+".json")); err != nil {
				t.Fatal(err)
			}
		},
		"the relay receipt is rewritten to name other bytes": func(t *testing.T, f relayFixture) {
			rec, found, err := f.store.Load(relayRequest)
			if err != nil || !found {
				t.Fatalf("load: found=%v err=%v", found, err)
			}
			rec.Artifact, rec.ReviewDigest = swapped, ReviewDigest(swapped)
			if err := overwriteReceipt(f.store, rec); err != nil {
				t.Fatal(err)
			}
		},
		"the relay receipt is emptied": func(t *testing.T, f relayFixture) {
			if err := os.WriteFile(filepath.Join(f.store.Dir, relayRequest+".json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			if _, err := f.submit(raw); err != nil {
				t.Fatalf("the genuine relay did not publish: %v", err)
			}
			wreck(t, f)

			runner := f.runner()
			runner.Issue = unreachableMailbox(f.box)
			res, err := runner.Run(context.Background(), f.turn(), nil)
			if err != nil {
				t.Fatalf("an accepted relayed review stopped being consumable: %v", err)
			}
			if res.ReviewDigest != ReviewDigest(raw) {
				t.Fatalf("consumed digest %s, want the accepted %s", res.ReviewDigest, ReviewDigest(raw))
			}
			if strings.Contains(res.Text, "swapped") {
				t.Fatalf("a rewritten relay receipt substituted its bytes: %q", res.Text)
			}
		})
	}
}

// Once a relay is genuinely published, editing its RELAY receipt afterwards
// changes nothing: the verdict comes from the common store, which holds the
// reviewer's own bytes, and the mailbox publication still corroborates them.
//
// BEHAVIOUR CHANGED IN R2, deliberately. Before R2 the relay receipt WAS the
// semantic source, so tampering with it after publication made a genuinely
// published review unconsumable. That is no longer the rule -- but the property
// that mattered is stronger here than it was: the tampered bytes can never be
// what gets consumed.
func TestTamperingWithARelayReceiptAfterPublicationCannotChangeTheVerdict(t *testing.T) {
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	swapped := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"swapped","instructions":"","findings":[]}`)

	for name, tamper := range map[string]func(RelayRecord) RelayRecord{
		"the receipt's bytes are swapped": func(r RelayRecord) RelayRecord {
			r.Artifact = swapped
			return r
		},
		"the receipt is reset to accepted": func(r RelayRecord) RelayRecord {
			r.State, r.PublicationComment = RelayAccepted, 0
			return r
		},
		"the receipt names another review": func(r RelayRecord) RelayRecord {
			r.Artifact, r.ReviewDigest = swapped, ReviewDigest(swapped)
			return r
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			published, err := f.submit(artifact)
			if err != nil || published.PublicationComment <= 0 {
				t.Fatalf("the genuine relay did not publish: %+v %v", published, err)
			}
			if err := overwriteReceipt(f.store, tamper(published)); err != nil {
				t.Fatal(err)
			}
			res, err := f.runner().Run(context.Background(), f.turn(), nil)
			if err != nil {
				t.Fatalf("the genuinely published review was not consumed: %v", err)
			}
			if res.ReviewDigest != ReviewDigest(artifact) {
				t.Fatalf("consumed review %s, want the genuinely published %s", res.ReviewDigest, ReviewDigest(artifact))
			}
			if strings.Contains(res.Text, "swapped") {
				t.Fatalf("the tampered bytes were consumed: %q", res.Text)
			}
		})
	}
}

// writeStoredReview puts a record into the common store the way a process
// OUTSIDE any adapter would: straight onto disk, with no publication behind it.
func writeStoredReview(s reviewstore.Store, requestID, raw string, ev reviewstore.Evidence) error {
	rec := reviewstore.Record{
		Version: 1, RequestID: requestID, ArtifactRaw: raw,
		ReviewDigest: reviewartifact.Digest(raw), Standing: reviewstore.Advisory,
		AcceptedAt: time.Now().UTC(),
	}
	if ev.Transport != "" {
		if ev.ObservedAt.IsZero() {
			ev.ObservedAt = time.Now().UTC()
		}
		rec.Evidence = []reviewstore.Evidence{ev}
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.Dir, requestID+".json"), append(blob, '\n'), 0o600)
}

// overwriteReceipt writes a receipt the way a process OUTSIDE the relay handler
// could: directly, replacing whatever the store held.
func overwriteReceipt(s RelayStore, rec RelayRecord) error {
	path, err := s.path(rec.RequestID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, blob, 0o600)
}

// Structural: the review runner's path cannot create, accept or publish a relay.
// The only writer is AcceptRelayedReview, called by the control process's relay
// socket handler with the principal that socket judged.
func TestTheReviewRunnerCannotAuthorOrPublishARelay(t *testing.T) {
	fset := token.NewFileSet()
	forbidden := map[string]bool{"AcceptRelayedReview": true, "publishRelayRecord": true, "create": true,
		"markPublished": true, "PostComment": true, "PublishRequest": true}
	checked := 0
	for _, file := range []string{"relay.go", "runner.go"} {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || (fn.Name.Name != "pendingRelayFor" && fn.Name.Name != "verifyRelayRecord" &&
				fn.Name.Name != "storedReviewFor") {
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
					t.Errorf("%s calls %s: the runner's relay path must only read", fn.Name.Name, name)
				}
				return true
			})
		}
	}
	if checked != 3 {
		t.Fatalf("inspected %d of the 3 relay/review read functions; the check proves nothing", checked)
	}
}

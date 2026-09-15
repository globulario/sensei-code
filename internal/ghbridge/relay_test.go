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
	"github.com/globulario/sensei-code/internal/roles"
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
}

func newRelayFixture(t *testing.T) relayFixture {
	t.Helper()
	m, box := newRelayMailbox(t)
	dir := t.TempDir()
	f := relayFixture{box: box, mailbox: m,
		exchanges: ExchangeLog{Dir: filepath.Join(dir, "exchanges")},
		store:     RelayStore{Dir: filepath.Join(dir, "relays")}}
	if err := f.exchanges.Open(ExchangeRecord{
		TaskID: relaySubject.TaskID, RequestID: relayRequest, RequestComment: 5686428018, Conversation: "157",
		PublishedAt: time.Now().Add(-time.Hour).UTC(), Kind: ExchangeReview,
		BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f relayFixture) submit(artifact string) (RelayRecord, error) {
	return AcceptRelayedReview(context.Background(), RelaySubmission{
		Artifact: artifact, Principal: operator, Exchanges: f.exchanges, Store: f.store, Mailbox: f.box,
	})
}

func (f relayFixture) runner() *Runner {
	return &Runner{Issue: f.box, NewRequestID: NewRequestID, Poll: 10 * time.Millisecond,
		Wait: 50 * time.Millisecond, Exchanges: f.exchanges, Relays: f.store}
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
func TestTheRunnerDoesNotConsumeAReceiptTheRelayHandlerDidNotPublish(t *testing.T) {
	artifact := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	forged := RelayRecord{Version: 1, State: RelayPublished, TaskID: relaySubject.TaskID, RequestID: relayRequest,
		Conversation: "157", BaseSHA: relaySubject.BaseSHA, CandidateDigest: relaySubject.CandidateDigest,
		CandidateTree: relaySubject.CandidateTree, ReviewCommit: relaySubject.ReviewCommit,
		Reviewer: "chatgpt", ReviewDigest: ReviewDigest(artifact), Decision: "accept", Artifact: artifact,
		Standing: "advisory", RelayPrincipal: operator, PublicationComment: 777}

	swapped := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"swapped","instructions":"","findings":[]}`)
	publish := func(t *testing.T, f relayFixture) RelayRecord {
		t.Helper()
		published, err := f.submit(artifact)
		if err != nil || published.PublicationComment <= 0 {
			t.Fatalf("the genuine relay did not publish: %+v %v", published, err)
		}
		return published
	}

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
		// These two have a REAL publication on the mailbox, so the mailbox check
		// cannot mask the receipt's own checks.
		"tampered after publication": func(t *testing.T, f relayFixture) RelayRecord {
			r := publish(t, f)
			r.Artifact = swapped
			return r
		},
		// The handler's receipt, not the mailbox alone, says a publication
		// completed: a receipt still marked accepted is not consumed even when it
		// names a genuine publication.
		"a real publication the receipt never recorded": func(t *testing.T, f relayFixture) RelayRecord {
			r := publish(t, f)
			r.State = RelayAccepted
			return r
		},
		"another relay's publication": func(t *testing.T, f relayFixture) RelayRecord {
			r := publish(t, f)
			r.Artifact, r.ReviewDigest = swapped, ReviewDigest(swapped)
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
			if !ok || (fn.Name.Name != "relayedReviewFor" && fn.Name.Name != "publicationStands" && fn.Name.Name != "verifyRelayRecord") {
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
		t.Fatalf("inspected %d of the 3 relay read functions; the check proves nothing", checked)
	}
}

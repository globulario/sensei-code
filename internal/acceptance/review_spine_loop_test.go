//go:build stubsmoke

// THE WHOLE LOOP, THROUGH THE ACTUAL ENGINE.
//
// The R6 commissioning in internal/ghbridge drives the review spine from "a
// candidate exists and a review is owed" to "that exact review discharged that
// exact obligation". This drives the turns ABOVE that line too -- objective,
// architect, implementer, the revision cycle and re-entry -- through
// workflow.Engine itself, with the REAL GitHub bridge serving the reviewer role
// over a mailbox this test stands up.
//
// Only the architect and implementer are stubs, for the reason the neighbouring
// tripwire gives: a deterministic provider makes a failure the state machine's
// rather than a model's. The reviewer is not stubbed, because the reviewer is
// what #182 rebuilt.
//
// It is tagged with the other acceptance runs: it needs a certified workspace,
// a live Sensei and a real candidate worktree.
//
//	go test -tags stubsmoke ./internal/acceptance/ -run TestCommissionTheWholeLoop -v -timeout 15m
package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// loopMailbox is a GitHub App mailbox that answers review requests the way a
// reviewer would: the first candidate gets REVISE, every later one gets ACCEPT.
//
// It answers INSIDE the handler, so no test goroutine races the comment list,
// and it records which candidate each request named so the test can prove the
// reviewer saw two different ones.
type loopMailbox struct {
	mu       sync.Mutex
	comments []map[string]any
	nextID   int64
	// seen is the candidate digest of each review request, in order.
	seen []string
	// answered maps request id -> the artifact posted for it.
	answered map[string]string
	// hold, while true, makes the mailbox accept requests and answer nothing.
	// It is how the first waiter is made to time out, so re-entry is real.
	hold bool
}

func (m *loopMailbox) snapshot() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]any{}, m.comments...)
}

func (m *loopMailbox) requests() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string{}, m.seen...)
}

func (m *loopMailbox) release() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hold = false
	// Answer everything that was held.
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		req, ok := ghbridge.ParseRequest(body)
		if !ok {
			continue
		}
		if _, done := m.answered[req.RequestID]; done {
			continue
		}
		m.appendAnswer(req)
	}
}

// appendAnswer must be called with mu held.
func (m *loopMailbox) appendAnswer(req ghbridge.Request) {
	decision := loopAccept
	if len(m.answered) == 0 {
		// The FIRST candidate is revised. The finding has to be actionable or
		// validation refuses it, and an unactionable objection would produce a
		// byte-identical repair.
		decision = loopRevise
	}
	raw, err := reviewartifact.Artifact{
		ReviewerProvider: req.ReviewerProvider, TaskID: req.TaskID, RequestID: req.RequestID,
		BaseSHA: req.BaseSHA, CandidateDigest: req.CandidateDigest, CandidateTree: req.CandidateTree,
		ReviewCommit: req.ReviewCommit, Body: decision,
	}.Render()
	if err != nil {
		return
	}
	m.answered[req.RequestID] = raw
	m.nextID++
	m.comments = append(m.comments, map[string]any{
		"id": m.nextID, "body": raw, "created_at": time.Now().UTC().Format(time.RFC3339),
		"user": map[string]any{"login": loopReviewerLogin, "id": loopReviewerID},
	})
}

const (
	loopReviewerLogin = "davecourtois"
	loopReviewerID    = 1697116
	loopAccept        = `{"decision":"accept","summary":"the repair carries the isolating assertion the finding asked for","instructions":"","findings":[]}`
	loopRevise        = `{"decision":"revise","summary":"the change is not shown to be necessary","instructions":"add the proof",` +
		`"findings":[{"id":"f1","severity":"blocking","claim":"the appended line is required","reference":"internal/report/report.go",` +
		`"reason":"nothing fails without it","correction":"state the reason the line exists"}]}`
)

func newLoopMailbox(t *testing.T) (*loopMailbox, ghbridge.Issue) {
	t.Helper()
	keyPath := writeLoopKey(t)
	m := &loopMailbox{nextID: 6000, answered: map[string]string{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "ghs_installation", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"permissions": map[string]string{"issues": "write", "pull_requests": "write",
				"contents": "write", "metadata": "read"},
		})
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/157", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"number":157,"pull_request":{"url":"https://api.github.com/repos/globulario/sensei-code/pulls/157"}}`)
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/157/comments", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			m.mu.Lock()
			m.nextID++
			id := m.nextID
			m.comments = append(m.comments, map[string]any{
				"id": id, "body": in.Body, "created_at": time.Now().UTC().Format(time.RFC3339),
				"user": map[string]any{"login": "globulario-sensei-code[bot]", "id": 99887766},
			})
			if req, ok := ghbridge.ParseRequest(in.Body); ok {
				m.seen = append(m.seen, req.CandidateDigest)
				if !m.hold {
					m.appendAnswer(req)
				}
			}
			m.mu.Unlock()
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": id, "user": map[string]any{"login": "globulario-sensei-code[bot]", "id": 99887766},
			})
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(m.snapshot())
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return m, ghbridge.Issue{
		Number: "157",
		API: &ghbridge.AppClient{
			Auth: &ghbridge.InstallationAuth{AppID: 4850747, InstallationID: 159521273,
				PrivateKeyPath: keyPath, APIBase: srv.URL},
			Owner: "globulario", Repo: "sensei-code",
		},
		ExpectedReviewer: ghbridge.Principal{UserID: loopReviewerID, Login: loopReviewerLogin},
	}
}

// TestCommissionTheWholeLoopThroughTheEngine drives objective -> architect ->
// implementer -> reviewer REVISE -> automatic repair -> re-entry -> reviewer
// ACCEPT, with nobody carrying anything between the parties.
func TestCommissionTheWholeLoopThroughTheEngine(t *testing.T) {
	root := repoRoot(t)
	repo := gitx.Repo{Root: root}

	// THE PRECONDITION, checked and NAMED. The governed lane refuses to start
	// on a workspace Sensei will not certify, and that refusal is graph
	// authority -- the milestone after this one (#182 R6 non-goals, sensei#377).
	// A skip here is an external limit stated exactly, not a check that quietly
	// passes.
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	requireCertifiedWorkspace(t, root, cfg)

	clean, err := repo.IsClean(context.Background())
	if err != nil {
		t.Fatalf("cannot read the working tree: %v", err)
	}
	if !clean {
		t.Skip("canonical checkout is dirty; the governed path refuses one by design")
	}
	stub := buildStubAgent(t, root)
	target := anchoredTarget
	cfg.Architect = config.Agent{Name: "stub-architect", Command: stub,
		Args: []string{"--role", "architect", "--target", target}, Graph: "none"}
	cfg.Implementors = []config.Agent{{Name: "claude", Command: stub,
		Args: []string{"--role", "implementor", "--target", target}, Graph: "none"}}
	// The reviewer is NOT stubbed. chatgpt is served by the GitHub bridge below.
	cfg.Reviewer = config.Agent{Name: "chatgpt", Graph: "none"}
	cfg.Reviewers = []config.Agent{cfg.Reviewer}

	sessionID := session.ID(time.Now())
	store, err := session.New(root, sessionID)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(1024)
	defer unsubscribe()
	engine := workflow.New(repo, cfg, bus, store, sessionID)

	mailbox, box := newLoopMailbox(t)
	mailbox.hold = true // the first review request goes unanswered, on purpose

	work := t.TempDir()
	exchanges := ghbridge.ExchangeLog{Dir: filepath.Join(work, "exchanges")}
	reviews := reviewstore.Store{Dir: filepath.Join(work, "reviews")}
	engine.Runners = ghbridge.Resolver{
		Provider: "chatgpt",
		Roles:    map[roles.Role]bool{roles.Reviewer: true},
		Reviewer: &ghbridge.Runner{
			Issue: box, RepoDir: root, Remote: "origin", NewRequestID: ghbridge.NewRequestID,
			Poll: 200 * time.Millisecond, Wait: 20 * time.Second, SessionID: sessionID,
			Exchanges: exchanges, Reviews: reviews, ReviewerProvider: "chatgpt",
		},
		Exchanges: exchanges,
		Fallback:  loopFallback{sessionID: sessionID},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	taskID := engine.SubmitGoverned(ctx, "Append one trailing comment line to "+target+" and change nothing else.")
	t.Logf("commissioning task %s", taskID)

	first := drainLoop(t, events, taskID, 10*time.Minute, func(ev event.Event) bool {
		return ev.Kind == event.WorkflowAwaitingReview || ev.Kind == event.WorkflowFailed ||
			ev.Kind == event.WorkflowCompleted || ev.Kind == event.WorkflowAwaitingAuthority ||
			ev.Kind == event.WorkflowStopped
	})
	if first.last == event.WorkflowAwaitingAuthority {
		t.Skipf("the router reached a human-owned authority boundary before review (%s); "+
			"that boundary is the product working, and it is not this loop's to answer", first.condition)
	}

	// THE RE-ENTRY. The first waiter ended without an answer; the obligation
	// stands, and a later process picks it up. The reviewer answers while
	// nothing is listening, exactly as it does when a person is asleep.
	if first.last != event.WorkflowAwaitingReview {
		t.Fatalf("the run ended %s before any review was owed: %s", first.last, first.summary)
	}
	owedBefore := pendingRequests(t, exchanges)
	if len(owedBefore) != 1 {
		t.Fatalf("the run left %d review obligations, want 1: %v", len(owedBefore), owedBefore)
	}
	mailbox.release()

	resumed := resumeTask(t, engine, store, sessionID, taskID)
	t.Logf("commissioning: re-entered %s as %s", taskID, resumed)
	second := drainLoop(t, events, resumed, 10*time.Minute, func(ev event.Event) bool {
		return ev.Kind == event.WorkflowCompleted || ev.Kind == event.WorkflowFailed ||
			ev.Kind == event.WorkflowAwaitingAuthority || ev.Kind == event.WorkflowStopped
	})

	// --- what the loop has to have done --------------------------------------
	reqs := mailbox.requests()
	if len(reqs) < 2 {
		t.Fatalf("the reviewer saw %d review request(s); a revision arc needs at least two", len(reqs))
	}
	if reqs[0] == reqs[len(reqs)-1] {
		t.Fatalf("every review request named the same candidate %s; the REVISE produced no repair", reqs[0])
	}
	// The ACCEPT that ended it is recorded as the exact bytes the reviewer wrote.
	var accepted int
	for id, raw := range mailbox.answered {
		rec, found, err := reviews.Load(id)
		if err != nil || !found {
			continue
		}
		if rec.ArtifactRaw != raw {
			t.Fatalf("the stored review for %s is not the bytes the reviewer posted", id)
		}
		if !rec.Consumable() {
			t.Fatalf("the stored review for %s was consumed while undelivered", id)
		}
		accepted++
	}
	if accepted == 0 {
		t.Fatal("no review reached the one durable record")
	}
	if left := pendingRequests(t, exchanges); len(left) != 0 {
		t.Fatalf("the loop ended with %d review obligation(s) still owed: %v", len(left), left)
	}
	if second.last == event.WorkflowFailed {
		t.Fatalf("the loop failed after the ACCEPT: %s", second.summary)
	}
	t.Logf("commissioning: %d review request(s), candidates %v, ended %s", len(reqs), reqs, second.last)
}

// loopFallback serves every role the bridge does not, exactly as the product's
// composition root does: the assigned provider's own command line.
type loopFallback struct{ sessionID string }

func (f loopFallback) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	return workflow.CLIResolved(spec, f.sessionID), nil
}

// requireCertifiedWorkspace states the external precondition, or skips naming it.
//
// Asked through the SAME surface the governed gate asks, so a skip here means
// the gate would have refused -- not that this test used a different instrument
// and guessed.
func requireCertifiedWorkspace(t *testing.T, root string, cfg config.Config) {
	t.Helper()
	sc, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Skipf("no Sensei MCP surface to ask about this workspace: %v", err)
	}
	defer sc.Close()
	res, err := sc.CallTool("sensei_workspace_status", map[string]any{"repo": root})
	if err != nil {
		t.Skipf("the workspace contract could not be read: %v", err)
	}
	ws, err := sensei.DecodeWorkspaceStatus(res)
	if err != nil {
		t.Skipf("the workspace contract could not be decoded: %v", err)
	}
	if !ws.Permits() {
		t.Skipf("the governed lane cannot start: %s -- this is graph authority, "+
			"which #182 R6 deliberately does not touch (sensei#377)", ws.Diagnostic())
	}
}

type loopOutcome struct {
	last      event.Kind
	summary   string
	condition string
}

func drainLoop(t *testing.T, events <-chan event.Event, taskID string, bound time.Duration,
	done func(event.Event) bool) loopOutcome {
	t.Helper()
	deadline := time.After(bound)
	var out loopOutcome
	for {
		select {
		case <-deadline:
			t.Fatalf("the loop did not settle within %s (last %s)", bound, out.last)
		case ev := <-events:
			if ev.TaskID != taskID && ev.TaskID != "" {
				continue
			}
			t.Logf("[%-26s] %s", ev.Kind, oneLine(ev.Summary))
			if ev.Kind == event.Status && strings.Contains(ev.Summary, "human-authority-required:") {
				out.condition = oneLine(ev.Summary)
			}
			if done(ev) {
				out.last, out.summary = ev.Kind, ev.Summary
				return out
			}
		}
	}
}

func pendingRequests(t *testing.T, log ghbridge.ExchangeLog) []string {
	t.Helper()
	owed, err := log.PendingReviews()
	if err != nil {
		t.Fatalf("reading the review obligations: %v", err)
	}
	var out []string
	for _, o := range owed {
		out = append(out, o.RequestID)
	}
	return out
}

// resumeTask re-enters the task the way `sensei-code resume --task` does: from
// the durable session record, through Engine.Resume, carrying no answer.
func resumeTask(t *testing.T, engine *workflow.Engine, store *session.Store, sessionID, taskID string) string {
	t.Helper()
	evs, err := store.Load()
	if err != nil {
		t.Fatalf("reading the session transcript: %v", err)
	}
	for _, task := range session.FindInterrupted(evs) {
		if task.TaskID == taskID {
			return engine.Resume(context.Background(), task)
		}
	}
	t.Fatalf("the awaiting-review task %s is not discoverable in its own session; "+
		"re-entry is impossible and the loop cannot continue", taskID)
	return ""
}

// writeLoopKey mints a throwaway App key. Generated rather than embedded: a key
// checked into a repository is a key, whatever the comment beside it says.
func writeLoopKey(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "app.pem")
	der := x509.MarshalPKCS1PrivateKey(key)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

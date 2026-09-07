package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// The mailbox moved from an ordinary issue to a pull request conversation
// because the remote actor is woken by pull request activity. These prove the
// move without loosening anything the move was not about.

// prMailbox is a fake GitHub that serves ONE conversation: the metadata GitHub
// returns for /issues/{n}, and that conversation's comments. isPR decides
// whether the metadata carries a pull_request object, which is exactly the
// difference between a PR conversation and an ordinary issue.
type prMailbox struct {
	srv          *httptest.Server
	number       string
	isPR         bool
	comments     []map[string]any
	pathsSeen    []string
	metadataHits int32
	// grants is what this fake installation reports. Real GitHub returns the
	// permission set in every token response, so a fake that omitted it would
	// let a check pass here and fail in production — which is exactly the shape
	// of the defect these tests exist to prevent.
	grants map[string]string
}

func newPRMailbox(t *testing.T, keyPath, number string, isPR bool) (*prMailbox, Issue) {
	return newPRMailboxWithGrants(t, keyPath, number, isPR,
		map[string]string{"issues": "write", "pull_requests": "write", "contents": "write", "metadata": "read"})
}

func newPRMailboxWithGrants(t *testing.T, keyPath, number string, isPR bool, grants map[string]string) (*prMailbox, Issue) {
	t.Helper()
	m := &prMailbox{number: number, isPR: isPR, grants: grants}

	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token":       "ghs_installation",
			"expires_at":  time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"permissions": m.grants,
		})
	})

	// Conversation metadata. A PR conversation carries pull_request; an ordinary
	// issue does not. Both are served from the issues resource, which is the
	// whole reason no second transport was needed.
	mux.HandleFunc("/repos/globulario/sensei-code/issues/"+number, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&m.metadataHits, 1)
		m.pathsSeen = append(m.pathsSeen, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		if m.isPR {
			fmt.Fprintf(w, `{"number":%s,"pull_request":{"url":"https://api.github.com/repos/globulario/sensei-code/pulls/%s"}}`, number, number)
			return
		}
		fmt.Fprintf(w, `{"number":%s}`, number)
	})

	mux.HandleFunc("/repos/globulario/sensei-code/issues/"+number+"/comments", func(w http.ResponseWriter, r *http.Request) {
		m.pathsSeen = append(m.pathsSeen, r.URL.Path)
		switch r.Method {
		case http.MethodPost:
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			m.comments = append(m.comments, map[string]any{
				"body": in.Body,
				"user": map[string]any{"login": "globulario-sensei-code[bot]", "id": 99887766},
			})
			w.WriteHeader(http.StatusCreated)
			fmt.Fprint(w, `{"id":1}`)
		case http.MethodGet:
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(m.comments)
		}
	})

	// Anything else is a route this bridge must never take. Inline PR review
	// comments live under /pulls/{n}/comments and are a different surface with
	// different semantics; reaching one would mean the move changed more than
	// which conversation is addressed.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		m.pathsSeen = append(m.pathsSeen, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message":"not found"}`)
	})

	m.srv = httptest.NewServer(mux)
	t.Cleanup(m.srv.Close)

	box := Issue{
		Number: number,
		API: &AppClient{
			Auth: &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
				PrivateKeyPath: keyPath, APIBase: m.srv.URL},
			Owner: "globulario", Repo: "sensei-code",
		},
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
	return m, box
}

// 6. An ordinary issue cannot be accepted as a healthy PR-trigger mailbox.
func TestAnOrdinaryIssueIsRefusedAsTheMailbox(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "156", false)

	err := VerifyMailboxIsPullRequest(context.Background(), box)
	if err == nil {
		t.Fatal("an ordinary issue was accepted as the mailbox; nothing would ever wake on it")
	}
	if !errors.Is(err, ErrNotAPullRequest) {
		t.Fatalf("refusal did not classify as ErrNotAPullRequest: %v", err)
	}
	if !strings.Contains(err.Error(), "156") {
		t.Errorf("refusal does not name the offending conversation: %v", err)
	}
}

func TestAPullRequestConversationIsAcceptedAsTheMailbox(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)

	if err := VerifyMailboxIsPullRequest(context.Background(), box); err != nil {
		t.Fatalf("a real pull request conversation was refused: %v", err)
	}
	if got := atomic.LoadInt32(&m.metadataHits); got != 1 {
		t.Errorf("expected exactly one metadata read, got %d", got)
	}
}

// An unreachable GitHub is a refusal, not a pass. A bridge that cannot
// establish its own target does not get to assume the target is fine —
// sensei_code.ghbridge.an_unavailable_bridge_refuses_rather_than_substituting.
func TestAnUnreachableGitHubRefusesRatherThanAssumingThePR(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	m.srv.Close() // GitHub is now unavailable, not answering "ordinary issue".

	err := VerifyMailboxIsPullRequest(context.Background(), box)
	if err == nil {
		t.Fatal("an unreachable GitHub was treated as a verified pull request")
	}
	if errors.Is(err, ErrNotAPullRequest) {
		t.Fatalf("an unreachable GitHub was misreported as an ordinary issue, "+
			"which would send an operator to fix the wrong thing: %v", err)
	}
}

// 7. A selected App transport never answers this question as the operator.
func TestMailboxVerificationNeverFallsBackToPersonalGH(t *testing.T) {
	box := Issue{
		Number:           "157",
		API:              &AppClient{Owner: "globulario", Repo: "sensei-code"}, // selected, incomplete
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
	err := VerifyMailboxIsPullRequest(context.Background(), box)
	if err == nil {
		t.Fatal("an incompletely configured App transport verified the mailbox anyway")
	}
	if !strings.Contains(err.Error(), "refusing") {
		t.Errorf("refusal does not say it is refusing rather than substituting: %v", err)
	}
}

// 2 and 8. A request posts to the configured PR number, through the top-level
// conversation resource, never through inline review comments.
func TestARequestPostsToTheConfiguredPRConversation(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)

	req := ArchitectureRequest{Binding: architectureBinding(), RequestID: "r-1", Prompt: "plan it"}
	if err := PostArchitectureRequest(context.Background(), box, req); err != nil {
		t.Fatalf("posting to the PR conversation: %v", err)
	}

	want := "/repos/globulario/sensei-code/issues/157/comments"
	var posted bool
	for _, p := range m.pathsSeen {
		if p == want {
			posted = true
		}
		if strings.Contains(p, "/pulls/") {
			t.Errorf("the bridge reached a pull-request-only surface %q; top-level "+
				"conversation comments are the mailbox, inline review comments are not", p)
		}
	}
	if !posted {
		t.Fatalf("request did not reach %s; saw %v", want, m.pathsSeen)
	}
}

// 3. Replies are read back from that same PR conversation.
func TestRepliesAreReadFromTheSamePRConversation(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)

	binding := architectureBinding()
	answer := ArchitectureResponse{Binding: binding, RequestID: "r-1", Body: `{"decision":"proceed"}`}
	body, err := answer.Marker()
	if err != nil {
		t.Fatal(err)
	}
	m.comments = append(m.comments, map[string]any{
		"body": body,
		"user": map[string]any{"login": "davecourtois", "id": 1697116},
	})

	got, err := Architectures(context.Background(), box)
	if err != nil {
		t.Fatalf("reading the PR conversation: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected one architecture answer from the PR conversation, got %d", len(got))
	}
	if !got[0].Answers(ArchitectureRequest{Binding: binding, RequestID: "r-1", Prompt: "x"}) {
		t.Error("the reply read from the PR conversation no longer answers its request; " +
			"moving the mailbox must not change binding semantics")
	}
}

// 1. Architect and reviewer are served by the SAME configured conversation.
// The architect runner is built per turn from the reviewer's mailbox, so a
// deployment cannot end up asking on one conversation and reviewing on another.
func TestArchitectAndReviewerShareTheOneConfiguredConversation(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)

	reviewer := &Runner{Issue: box, NewRequestID: NewRequestID, Wait: time.Minute}
	fb := &recordingResolver{}
	r := Resolver{Provider: "chatgpt", Reviewer: reviewer, Fallback: fb}

	resolved, err := r.Resolve(workflow.RunnerSpec{
		Role:         roles.Architect,
		Agent:        config.Agent{Name: "chatgpt"},
		TaskID:       architectureBinding().TaskID,
		Architecture: architectureBinding(),
	})
	if err != nil {
		t.Fatalf("resolving the architect over the configured bridge: %v", err)
	}
	architect, ok := resolved.Runner.(*ArchitectureRunner)
	if !ok {
		t.Fatalf("architect turn was not carried by the github bridge: %T", resolved.Runner)
	}
	if architect.Issue.Number != reviewer.Issue.Number {
		t.Fatalf("architect mailbox #%s and reviewer mailbox #%s disagree; requests and "+
			"reviews would land in different conversations",
			architect.Issue.Number, reviewer.Issue.Number)
	}
	if architect.Issue.API != reviewer.Issue.API {
		t.Error("architect and reviewer do not share the one configured App client")
	}
}

// 9. The bounded turn is unchanged: nobody answers, and it ends on its own.
func TestAnUnansweredPRMailboxTurnStillExpires(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailbox(t, keyPath, "157", true)

	runner := &ArchitectureRunner{
		Issue:        box,
		Binding:      architectureBinding(),
		NewRequestID: NewRequestID,
		Poll:         10 * time.Millisecond,
		Wait:         150 * time.Millisecond,
	}
	start := time.Now()
	_, err := runner.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"}, nil)
	if err == nil {
		t.Fatal("an unanswered turn returned success")
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("the turn did not respect its bound, took %s", elapsed)
	}
}

// The defect this pair exists to prevent: #157 verified as a genuine pull
// request and the very first post returned HTTP 403, because reading a
// conversation needs issues:read while posting into a PR conversation is
// authorized against PULL REQUESTS. Being the right kind of target and being a
// target this App may write to are different questions, and conflating them
// spent an at-most-once approval receipt on a task that could never have run.
func TestAPRMailboxTheAppCannotPostToIsRefused(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailboxWithGrants(t, keyPath, "157", true,
		map[string]string{"issues": "write", "contents": "write", "metadata": "read"})

	err := VerifyMailboxIsPullRequest(context.Background(), box)
	if err == nil {
		t.Fatal("a pull request the app cannot post to was accepted; the first governed " +
			"request would 403 after an approval receipt had already been spent")
	}
	if !errors.Is(err, ErrMissingPermission) {
		t.Fatalf("refusal did not classify as a missing permission: %v", err)
	}
	if !strings.Contains(err.Error(), "pull_requests") {
		t.Errorf("refusal does not name the missing capability, so an operator cannot act on it: %v", err)
	}
	// issues:write is exactly the grant that made the OLD issue mailbox work, so
	// naming it is what explains why the same endpoint worked before and not now.
	if !strings.Contains(err.Error(), "issues=write") {
		t.Errorf("refusal does not show what IS granted: %v", err)
	}
}

// Read-only is reported differently from absent, because they send an operator
// to different remedies.
func TestAReadOnlyPullRequestGrantIsRefusedAsReadOnly(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailboxWithGrants(t, keyPath, "157", true,
		map[string]string{"issues": "write", "pull_requests": "read", "metadata": "read"})

	err := VerifyMailboxIsPullRequest(context.Background(), box)
	if err == nil {
		t.Fatal("a read-only pull_requests grant was accepted as writable")
	}
	if !errors.Is(err, ErrMissingPermission) {
		t.Fatalf("refusal did not classify as a missing permission: %v", err)
	}
	if !strings.Contains(err.Error(), `"read"`) {
		t.Errorf("refusal does not distinguish read-only from absent: %v", err)
	}
}

// A permission map names capabilities, never secrets. The token it arrives
// beside must not follow it into an error.
func TestThePermissionRefusalCarriesNoToken(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	_, box := newPRMailboxWithGrants(t, keyPath, "157", true,
		map[string]string{"issues": "write", "metadata": "read"})

	err := VerifyMailboxIsPullRequest(context.Background(), box)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if strings.Contains(err.Error(), "ghs_installation") {
		t.Fatalf("the installation token reached a permission error: %v", err)
	}
}

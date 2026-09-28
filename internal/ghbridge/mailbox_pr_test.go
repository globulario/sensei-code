package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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
	// mu guards comments. The fixture is a real HTTP server, so its handler runs
	// on server goroutines; a test that appends from its own goroutine while any
	// request is still in flight races it. That window is narrow and real: a
	// killed child process leaves its last GET being served.
	mu           sync.Mutex
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
	// onPost, when set, sees every posted body inside the handler and returns
	// comments to append after it -- a remote answering the exact request that
	// was just published, without a test goroutine racing the comment list.
	onPost func(body string) []map[string]any
	// failPosts makes every post fail, for the failure points between retiring
	// an owed review and establishing its replacement.
	failPosts bool
	// commentReads counts every comments GET. onCommentsGet, when set, runs
	// inside the handler before that GET is answered, with the request's own
	// context and its 1-based read number -- so a test can hold one exact read
	// open until the caller gives up on it.
	commentReads  int32
	onCommentsGet func(ctx context.Context, read int32)
}

// append adds comments under the lock, wherever the caller is running.
func (m *prMailbox) append(c ...map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.comments = append(m.comments, c...)
}

// snapshot copies the comment list for a reader that may be a server goroutine.
func (m *prMailbox) snapshot() []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]map[string]any{}, m.comments...)
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
			if m.failPosts {
				w.WriteHeader(http.StatusBadGateway)
				fmt.Fprint(w, `{"message":"unavailable"}`)
				return
			}
			var in struct{ Body string }
			_ = json.NewDecoder(r.Body).Decode(&in)
			m.append(map[string]any{
				"body": in.Body,
				"user": map[string]any{"login": "globulario-sensei-code[bot]", "id": 99887766},
			})
			if m.onPost != nil {
				m.append(m.onPost(in.Body)...)
			}
			w.WriteHeader(http.StatusCreated)
			// Real GitHub answers a create with the whole comment, including
			// its AUTHOR. A fixture that returned only the id let a request be
			// published with no publisher this workspace could pin, which is
			// the shape #182 R6 now refuses -- so the fixture would have been
			// proving the refusal rather than the protocol.
			fmt.Fprint(w, `{"id":1,"user":{"login":"globulario-sensei-code[bot]","id":99887766}}`)
		case http.MethodGet:
			read := atomic.AddInt32(&m.commentReads, 1)
			if m.onCommentsGet != nil {
				m.onCommentsGet(r.Context(), read)
			}
			w.WriteHeader(http.StatusOK)
			_ = json.NewEncoder(w).Encode(m.snapshot())
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
	m.append(map[string]any{
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

// A deadline that lands INSIDE a mailbox read ends the wait exactly as one that
// lands between reads: as no answer, carrying every rejection collected so far.
// The first read is answered and records a refusal bound to another request;
// the second is held open until the wait's own deadline cancels it, so the
// deadline cannot land anywhere but inside that read.
func TestADeadlineDuringAMailboxReadStillEndsAsNoAnswerWithItsRejections(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	req, refusal := architectureRefusalFixture()
	refusal.RequestID = "r-0000000000000000"
	wire, err := refusal.Marker()
	if err != nil {
		t.Fatal(err)
	}
	m.append(map[string]any{
		"id": float64(7203), "body": wire,
		"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
	})

	// Registered after the server's Close, so it runs first: a held read is
	// released even if the cancellation never reached the handler.
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var heldUntilCanceled atomic.Bool
	m.onCommentsGet = func(ctx context.Context, read int32) {
		if read < 2 {
			return
		}
		select {
		case <-ctx.Done():
			heldUntilCanceled.Store(true)
		case <-release:
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = AwaitArchitecture(ctx, box, req, 10*time.Millisecond)

	if got := atomic.LoadInt32(&m.commentReads); got < 2 {
		t.Fatalf("the deadline did not land inside a read: only %d comments read(s) were made", got)
	}
	if err == nil {
		t.Fatal("a wait that ran out of time returned success")
	}
	if !errors.Is(err, ErrNoArchitectureAnswer) {
		t.Fatalf("a deadline inside the read did not end as no answer: %v", err)
	}
	if !strings.Contains(err.Error(), context.DeadlineExceeded.Error()) {
		t.Errorf("the wait did not say its deadline ended it: %v", err)
	}
	if !strings.Contains(err.Error(), "r-0000000000000000") {
		t.Errorf("the refusal of another request collected before the deadline was dropped: %v", err)
	}
	// Checked last: the handler observes cancellation asynchronously to the
	// client, so give it the moment it needs rather than racing it.
	for i := 0; i < 100 && !heldUntilCanceled.Load(); i++ {
		time.Sleep(10 * time.Millisecond)
	}
	if !heldUntilCanceled.Load() {
		t.Error("the second read was not held until the wait's context was canceled")
	}
}

// W3 (R9 x GR) -- DR2: THE REVIEW DEADLINE IS ONE FACT WHEREVER IT LANDS.
//
// The same mailbox, waited on twice. Once the deadline lands in the wait's
// select: one read, then a poll interval longer than the wait. Once it lands
// INSIDE a mailbox read: the second read is held by the test-only comments-read
// gate until the wait's own context ends, so expiry cannot land anywhere else.
// No sleep races a timer; the gate forces the interleaving.
//
// Both endings must be the waiter's one typed ending, wrapping the deadline,
// carrying whatever was observed before it -- and must be the same ending. With
// nothing observed that is ErrNoAnswer; with an earlier malformed reply it is
// that observation fault, and never ErrNoAnswer.
func TestAReviewDeadlineInsideAMailboxReadIsTheSameEndingAsInTheSelect(t *testing.T) {
	keyPath, _ := writeTestKey(t)

	wait := func(t *testing.T, withFault, insideRead bool) error {
		t.Helper()
		m, box := newPRMailbox(t, keyPath, "157", true)
		if withFault {
			m.append(map[string]any{
				"id": float64(7301), "body": "LGTM, ship it", "created_at": "2026-09-27T12:00:00Z",
				"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
			})
		}
		every := time.Hour
		release := make(chan struct{})
		t.Cleanup(func() { close(release) })
		var heldUntilCanceled atomic.Bool
		if insideRead {
			every = 10 * time.Millisecond
			m.onCommentsGet = func(ctx context.Context, read int32) {
				if read < 2 {
					return
				}
				select {
				case <-ctx.Done():
					heldUntilCanceled.Store(true)
				case <-release:
				}
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		_, err := AwaitReview(ctx, box, obligationC1(box), every)

		reads := atomic.LoadInt32(&m.commentReads)
		if insideRead {
			if reads < 2 {
				t.Fatalf("the deadline did not land inside a read: only %d read(s) were made", reads)
			}
			for i := 0; i < 100 && !heldUntilCanceled.Load(); i++ {
				time.Sleep(10 * time.Millisecond)
			}
			if !heldUntilCanceled.Load() {
				t.Fatal("the second read was not held until the wait's context ended")
			}
		} else if reads != 1 {
			t.Fatalf("the select-path wait made %d reads, want exactly 1", reads)
		}
		return err
	}

	for name, withFault := range map[string]bool{"nothing observed": false, "an earlier malformed reply": true} {
		t.Run(name, func(t *testing.T) {
			endings := map[string]error{
				"select":      wait(t, withFault, false),
				"inside read": wait(t, withFault, true),
			}
			for path, err := range endings {
				var ended *ReviewWaitEnded
				if !errors.As(err, &ended) {
					t.Fatalf("%s: err = %v, want the waiter's typed ending", path, err)
				}
				if ended.RequestID != "r-1" {
					t.Errorf("%s: the ending names request %q, want r-1", path, ended.RequestID)
				}
				if !errors.Is(err, context.DeadlineExceeded) || ended.Cause != context.DeadlineExceeded {
					t.Errorf("%s: the ending lost its cause: %v", path, err)
				}
				if withFault {
					if errors.Is(err, ErrNoAnswer) || ended.Unanswered() {
						t.Errorf("%s: an observed reply was reported as nobody answering: %v", path, err)
					}
					var observed *roles.ReviewObservationFault
					if !errors.As(err, &observed) || !observed.Has(roles.ObservedMalformed) ||
						len(observed.Observations) != 1 || observed.Observations[0].Comment != 7301 {
						t.Errorf("%s: the fault seen before the deadline was not carried: %v", path, err)
					}
				} else {
					if !errors.Is(err, ErrNoAnswer) || !ended.Unanswered() || ended.Fault != nil {
						t.Errorf("%s: silence did not end as ErrNoAnswer: %v", path, err)
					}
				}
			}
			// THE SAME FACT: one ending, identical wherever the deadline landed.
			if a, b := endings["select"].Error(), endings["inside read"].Error(); a != b {
				t.Fatalf("the deadline's ending depends on where it landed:\n select:      %s\n inside read: %s", a, b)
			}
		})
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

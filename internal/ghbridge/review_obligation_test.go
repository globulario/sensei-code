package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
)

func reviewTurnFor(binding roles.Binding) agent.Request {
	return agent.Request{Role: roles.Reviewer, TaskID: binding.TaskID, Binding: binding}
}

// standingRequest runs one waiter to timeout and returns the obligation it left.
func standingRequest(t *testing.T, runner *Runner, binding roles.Binding) *roles.ReviewUnanswered {
	t.Helper()
	_, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
	var owed *roles.ReviewUnanswered
	if !errors.As(err, &owed) {
		t.Fatalf("the waiter did not leave an owed review: %v", err)
	}
	return owed
}

// publishedRequests lists every review request id on the mailbox.
func publishedRequests(m *prMailbox) []string {
	var out []string
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		if r, ok := ParseRequest(body); ok {
			out = append(out, r.RequestID)
		}
	}
	return out
}

// postAnswer puts a reviewer's canonical answer on the mailbox with no waiter
// listening, the way a reviewer who replied hours later would.
func postAnswer(t *testing.T, m *prMailbox, o ExchangeRecord, provider, login string, id int64, body string) string {
	t.Helper()
	raw := canonicalAnswer(t, o.Subject(), o.RequestID, provider, body)
	m.comments = append(m.comments, map[string]any{
		"id": float64(8100 + len(m.comments)), "body": raw,
		"user": map[string]any{"login": login, "id": float64(id)},
	})
	return raw
}

func soleObligation(t *testing.T, log ExchangeLog) ExchangeRecord {
	t.Helper()
	owed, err := log.PendingReviews()
	if err != nil {
		t.Fatal(err)
	}
	if len(owed) != 1 {
		t.Fatalf("want exactly one standing obligation, got %+v", owed)
	}
	return owed[0]
}

// Killing every waiter changes who is listening and nothing about what is owed.
//
// The obligation survives the waiter's deadline, the process that published it,
// and startup reconciliation. Its request id, its review projection and its wake
// target are all properties of the obligation, so a restarted process picks up
// exactly the request that is standing.
func TestAnObligationSurvivesItsWaiterAndItsProcess(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)
	published := len(publishedRequests(m))
	rec := soleObligation(t, log)

	// A DIFFERENT process: a fresh runner over the same durable log and mailbox.
	restarted := &Runner{
		Issue: runner.Issue, RepoDir: runner.RepoDir, Remote: runner.Remote,
		NewRequestID: func() string {
			t.Fatal("a restarted process minted a request id for a standing obligation")
			return ""
		},
		Poll: runner.Poll, Wait: 80 * time.Millisecond, Exchanges: log,
		Reviews: runner.Reviews, ReviewerProvider: "chatgpt",
	}

	// Startup reconciliation runs in between, as it does on every boot.
	if _, err := ReconcileAbandonedExchanges(context.Background(), log, runner.Issue, nil); err != nil {
		t.Fatalf("startup reconciliation: %v", err)
	}
	if got := soleObligation(t, log); got.RequestID != rec.RequestID {
		t.Fatalf("startup changed the obligation: %s -> %s", rec.RequestID, got.RequestID)
	}

	again := standingRequest(t, restarted, binding)
	if again.RequestID != first.RequestID {
		t.Fatalf("the restarted process is waiting on %s; the obligation is %s", again.RequestID, first.RequestID)
	}
	if again.ReviewCommit != first.ReviewCommit {
		t.Fatalf("the review projection moved: %s -> %s", first.ReviewCommit, again.ReviewCommit)
	}
	if again.RequestComment != first.RequestComment {
		t.Fatalf("the wake target moved: %d -> %d", first.RequestComment, again.RequestComment)
	}
	if n := len(publishedRequests(m)); n != published {
		t.Fatalf("%d requests on the mailbox, want the original %d", n, published)
	}
	if w := prWithdrawalsIn(m); len(w) != 0 {
		t.Fatalf("a waiter ending or a restart withdrew something: %v", w)
	}
	// And the deadline the first waiter recorded has long passed.
	if got := soleObligation(t, log); !got.Deadline.Before(time.Now()) {
		t.Fatalf("this test needs the recorded waiter deadline to have passed; it is %s", got.Deadline)
	}
}

// A review posted while nothing was listening is consumed on the next resume,
// under the same request, with no new request and no new snapshot.
func TestAReviewPostedWithNoWaiterIsConsumedOnTheNextAttempt(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)
	published := len(publishedRequests(m))
	rec := soleObligation(t, log)

	// The reviewer answers hours later. Nobody is waiting.
	raw := postAnswer(t, m, rec, "chatgpt", "davecourtois", 1697116, acceptJSON)

	runner.NewRequestID = func() string {
		t.Fatal("consuming a late answer minted a request id")
		return ""
	}
	res, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
	if err != nil {
		t.Fatalf("the late answer was not consumed: %v", err)
	}
	if res.ReviewDigest != reviewDigestOf(raw) {
		t.Fatalf("consumed digest %s, want %s", res.ReviewDigest, reviewDigestOf(raw))
	}
	if n := len(publishedRequests(m)); n != published {
		t.Fatalf("consuming published %d extra requests", n-published)
	}
	if owed, _ := log.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the answered obligation was not discharged: %+v", owed)
	}
	// And the bytes reached the common store under the SAME request.
	stored, found, err := runner.Reviews.Load(first.RequestID)
	if err != nil || !found {
		t.Fatalf("the late answer is not in the review store: found=%v err=%v", found, err)
	}
	if stored.ArtifactRaw != raw {
		t.Fatal("the store holds bytes other than the ones posted")
	}
}

func reviewDigestOf(raw string) string { return ReviewDigest(raw) }

// A request remembers which GitHub account was authorized to answer it.
//
// Configuration is mutable and a published request is not. Changing one config
// line must not make a different account able to answer a question it was never
// asked, and must not mint a replacement merely because the config moved.
func TestAnObligationPinsTheAccountAllowedToAnswerIt(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)
	rec := soleObligation(t, log)
	if rec.ExpectedReviewerID != 1697116 {
		t.Fatalf("the obligation pinned principal id %d, want the publishing config's 1697116", rec.ExpectedReviewerID)
	}

	// Config now names a different account entirely.
	runner.Issue.ExpectedReviewer = Principal{UserID: 424242, Login: "someone-else"}
	runner.NewRequestID = func() string {
		t.Fatal("a config change minted a replacement request")
		return ""
	}

	// B answers. B was never authorized for this request.
	postAnswer(t, m, rec, "chatgpt", "someone-else", 424242, acceptJSON)
	if _, err := runner.Run(context.Background(), reviewTurnFor(binding), nil); err == nil {
		t.Fatal("an account named only by today's config answered a standing request")
	}
	if owed, _ := log.PendingReviews(); len(owed) != 1 || owed[0].RequestID != first.RequestID {
		t.Fatalf("the obligation did not survive: %+v", owed)
	}

	// A, the pinned principal, still can.
	raw := postAnswer(t, m, rec, "chatgpt", "davecourtois", 1697116, acceptJSON)
	res, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
	if err != nil {
		t.Fatalf("the pinned principal could not answer its own request: %v", err)
	}
	if res.ReviewDigest != reviewDigestOf(raw) {
		t.Fatalf("consumed %s, want the pinned principal's %s", res.ReviewDigest, reviewDigestOf(raw))
	}
}

// A different reviewer is a different obligation; the same reviewer is not.
func TestOnlyARealChangeReplacesAnObligation(t *testing.T) {
	t.Run("the same provider reattaches", func(t *testing.T) {
		m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
		first := standingRequest(t, runner, binding)
		runner.ReviewerProvider = "ChatGPT" // display-shaped spelling of the same assignment
		again := standingRequest(t, runner, binding)
		if again.RequestID != first.RequestID {
			t.Fatalf("a spelling difference replaced the obligation: %s -> %s", first.RequestID, again.RequestID)
		}
		if w := prWithdrawalsIn(m); len(w) != 0 {
			t.Fatalf("something was withdrawn: %v", w)
		}
		if len(publishedRequests(m)) != 1 {
			t.Fatalf("requests published: %v", publishedRequests(m))
		}
		_ = log
	})

	t.Run("a changed provider supersedes explicitly", func(t *testing.T) {
		m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
		first := standingRequest(t, runner, binding)

		runner.ReviewerProvider = "claude"
		second := standingRequest(t, runner, binding)
		if second.RequestID == first.RequestID {
			t.Fatal("a changed reviewer assignment reused the old request")
		}
		withdrawn := map[string]bool{}
		for _, id := range prWithdrawalsIn(m) {
			withdrawn[id] = true
		}
		if !withdrawn[first.RequestID] || withdrawn[second.RequestID] {
			t.Fatalf("withdrawals %v: want the old request retired and the new standing", prWithdrawalsIn(m))
		}
		if got := soleObligation(t, log); got.RequestID != second.RequestID || got.ReviewerProvider != "claude" {
			t.Fatalf("the surviving obligation is %+v", got)
		}
	})
}

// Two active obligations for one task are a conflict, not a choice.
func TestTwoActiveObligationsAreAConflictAndNeverAChoice(t *testing.T) {
	_, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)

	// A second record appears for the same task, as a partially-failed
	// supersession would leave behind.
	rec := soleObligation(t, log)
	rival := rec
	rival.RequestID = "r-ffffffffffffffff"
	if err := log.Open(rival); err != nil {
		t.Fatal(err)
	}

	runner.NewRequestID = func() string {
		t.Fatal("a conflicted task minted another request")
		return ""
	}
	_, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
	if !errors.Is(err, ErrObligationConflict) {
		t.Fatalf("err = %v, want an explicit lifecycle conflict", err)
	}
	if !strings.Contains(err.Error(), first.RequestID) || !strings.Contains(err.Error(), rival.RequestID) {
		t.Fatalf("the conflict does not name both obligations: %v", err)
	}
	if owed, _ := log.PendingReviews(); len(owed) != 2 {
		t.Fatalf("the conflict consumed or retired something: %+v", owed)
	}
}

// A review is not answered until it is BOTH recorded and discharged.
//
// A verdict that escaped while the obligation still stood would let the session
// say REVIEWED and the obligation store say OWED, and the next run would ask the
// reviewer again for a review that had already been given.
func TestAVerdictDoesNotEscapeUntilTheObligationIsDischarged(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	standingRequest(t, runner, binding)
	rec := soleObligation(t, log)
	raw := postAnswer(t, m, rec, "chatgpt", "davecourtois", 1697116, acceptJSON)

	// ONLY the discharge may fail. The log stays readable -- a read-only
	// directory still lists and opens its files, and only removing one fails --
	// so the obligation is found, the review is accepted, and the single thing
	// that cannot happen is retiring the record. Destroying the directory
	// instead would make the obligation unreadable and the turn would fail
	// before it ever reached a discharge, which proves nothing about ordering.
	if err := os.Chmod(log.Dir, 0o500); err != nil {
		t.Fatal(err)
	}
	if _, lerr := log.PendingReviews(); lerr != nil {
		t.Fatalf("this test needs the log to stay readable: %v", lerr)
	}
	if _, err := runner.Run(context.Background(), reviewTurnFor(binding), nil); err == nil {
		t.Fatal("a verdict escaped while its obligation could not be discharged")
	}
	// The bytes ARE recorded; only the discharge is owed.
	if _, found, _ := runner.Reviews.Load(rec.RequestID); !found {
		t.Fatal("the review was not recorded before the discharge was attempted")
	}
	if owed, _ := log.PendingReviews(); len(owed) != 1 {
		t.Fatalf("the obligation did not survive a failed discharge: %+v", owed)
	}

	// With the directory writable again the stored review is read back and only
	// now is the verdict released -- without asking anybody again.
	if err := os.Chmod(log.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	before := len(publishedRequests(m))
	runner.NewRequestID = func() string {
		t.Fatal("the discharge retry minted a request")
		return ""
	}
	res, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
	if err != nil {
		t.Fatalf("the recorded review was not released on retry: %v", err)
	}
	if res.ReviewDigest != reviewDigestOf(raw) {
		t.Fatalf("released %s, want the recorded %s", res.ReviewDigest, reviewDigestOf(raw))
	}
	if n := len(publishedRequests(m)); n != before {
		t.Fatalf("the retry published %d extra requests", n-before)
	}
	if owed, _ := log.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the obligation was not discharged on retry: %+v", owed)
	}
}

// A review the reviewer contract refuses discharges nothing.
func TestAnInvalidReviewNeverDischargesTheObligation(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)
	rec := soleObligation(t, log)
	postAnswer(t, m, rec, "chatgpt", "davecourtois", 1697116, "LGTM, ship it")

	if _, err := runner.Run(context.Background(), reviewTurnFor(binding), nil); err == nil {
		t.Fatal("prose discharged a review obligation")
	}
	owed, _ := log.PendingReviews()
	if len(owed) != 1 || owed[0].RequestID != first.RequestID {
		t.Fatalf("the obligation did not survive an invalid review: %+v", owed)
	}
	if _, found, _ := runner.Reviews.Load(first.RequestID); found {
		t.Fatal("an invalid review was recorded")
	}
}

// #187: one waiter timeout, one outcome.
//
// The wait can end while the poll goroutine is asleep, or while a mailbox read
// is in flight. Both mean the waiter stopped with nobody having answered, and
// they used to surface as two different errors depending on which branch
// observed expiry -- so a caller matching one had a race-dependent bug.
func TestAWaiterTimeoutHasOneOutcomeHoweverItIsObserved(t *testing.T) {
	keyPath, _ := writeTestKey(t)

	t.Run("expiry observed while sleeping between polls", func(t *testing.T) {
		_, box := newPRMailbox(t, keyPath, "157", true)
		ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
		defer cancel()
		_, err := AwaitReview(ctx, box, box.ExpectedReviewer, reqC1(), time.Second)
		if !errors.Is(err, ErrNoAnswer) {
			t.Fatalf("err = %v, want ErrNoAnswer", err)
		}
	})

	t.Run("expiry observed with a mailbox read in flight", func(t *testing.T) {
		// The mailbox blocks past the deadline, so expiry is always observed
		// inside the read rather than in the select. Forced, not hoped for.
		box := slowMailbox(t, keyPath, 300*time.Millisecond)
		for i := 0; i < 20; i++ {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
			_, err := AwaitReview(ctx, box, box.ExpectedReviewer, reqC1(), time.Millisecond)
			cancel()
			if !errors.Is(err, ErrNoAnswer) {
				t.Fatalf("run %d: err = %v, want ErrNoAnswer from an in-flight read", i, err)
			}
		}
	})

	t.Run("a live transport failure is not silence", func(t *testing.T) {
		box := brokenMailbox(t, keyPath)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_, err := AwaitReview(ctx, box, box.ExpectedReviewer, reqC1(), time.Millisecond)
		if err == nil {
			t.Fatal("an unreadable mailbox reported success")
		}
		if errors.Is(err, ErrNoAnswer) {
			t.Fatalf("an unreadable mailbox was reported as nobody answering: %v", err)
		}
	})
}

// slowMailbox answers comment reads only after delay, so a short waiter always
// expires with the read in flight.
func slowMailbox(t *testing.T, keyPath string, delay time.Duration) Issue {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "ghs_installation", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"permissions": map[string]string{"issues": "write", "pull_requests": "write", "metadata": "read"},
		})
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/157/comments", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Issue{
		Number: "157",
		API: &AppClient{
			Auth:  &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: keyPath, APIBase: srv.URL},
			Owner: "globulario", Repo: "sensei-code",
		},
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
}

// brokenMailbox fails every comment read while the context stays live.
func brokenMailbox(t *testing.T, keyPath string) Issue {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/app/installations/159521273/access_tokens", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"token": "ghs_installation", "expires_at": time.Now().Add(time.Hour).UTC().Format(time.RFC3339),
			"permissions": map[string]string{"issues": "write", "pull_requests": "write", "metadata": "read"},
		})
	})
	mux.HandleFunc("/repos/globulario/sensei-code/issues/157/comments", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"message":"boom"}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return Issue{
		Number: "157",
		API: &AppClient{
			Auth:  &InstallationAuth{AppID: 4850747, InstallationID: 159521273, PrivateKeyPath: keyPath, APIBase: srv.URL},
			Owner: "globulario", Repo: "sensei-code",
		},
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
}

// A pre-R4 obligation never recorded which account could answer it. That gap is
// never filled from today's configuration; it is replaced explicitly instead.
func TestALegacyObligationIsReplacedRatherThanReattached(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)

	// Rewrite it the way a pre-R4 process wrote it: no pinned principal.
	rec := soleObligation(t, log)
	if err := log.Close(rec.TaskID, rec.RequestID); err != nil {
		t.Fatal(err)
	}
	legacy := rec
	legacy.ExpectedReviewerID, legacy.ExpectedReviewerLogin = 0, ""
	if err := log.Open(legacy); err != nil {
		t.Fatal(err)
	}

	second := standingRequest(t, runner, binding)
	if second.RequestID == first.RequestID {
		t.Fatal("a legacy obligation with no pinned principal was reattached to")
	}
	got := soleObligation(t, log)
	if got.RequestID != second.RequestID {
		t.Fatalf("the surviving obligation is %s, want the replacement %s", got.RequestID, second.RequestID)
	}
	if got.ExpectedReviewerID == 0 && got.ExpectedReviewerLogin == "" {
		t.Fatal("the replacement did not pin a principal either")
	}
	if !strings.Contains(strings.Join(prWithdrawalsIn(m), " "), first.RequestID) {
		t.Fatalf("the legacy request was not withdrawn: %v", prWithdrawalsIn(m))
	}
}

// A process pointed at another conversation cannot observe the obligation, and
// must not publish a second request because configuration moved.
func TestConversationDriftPreservesTheObligationAndMintsNothing(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)
	published := len(publishedRequests(m))

	runner.Issue.Number = "999"
	runner.NewRequestID = func() string {
		t.Fatal("a moved conversation minted a replacement request")
		return ""
	}
	_, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
	var owed *roles.ReviewUnanswered
	if !errors.As(err, &owed) || owed.RequestID != first.RequestID {
		t.Fatalf("err = %v, want the same obligation preserved", err)
	}
	if n := len(publishedRequests(m)); n != published {
		t.Fatalf("a moved conversation published %d extra requests", n-published)
	}
	if got := soleObligation(t, log); got.RequestID != first.RequestID {
		t.Fatalf("the obligation changed: %+v", got)
	}
}

// Structural: review lifetime is decided in one place.
//
// A production consumer that scanned the exchange log itself would be a second
// set of lifecycle rules, and the two would agree only until one of them didn't.
func TestReviewLifetimeIsDecidedByOneOwner(t *testing.T) {
	allowed := map[string]bool{
		"review_obligation.go": true, // the owner
		"exchange.go":          true, // the storage it is a lens over
	}
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") || allowed[name] {
			continue
		}
		blob, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(blob), "PendingReviews()") {
			t.Errorf("%s scans review records directly; review lifetime belongs to ReviewObligationStore", name)
		}
	}
}

// Structural: the reattach branch cannot mint or publish anything.
func TestTheReattachBranchCannotMintOrPublish(t *testing.T) {
	blob, err := os.ReadFile("runner.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(blob)
	// BOTH halves of the reattachment path: the decision to reattach, and the
	// waiting itself. A mutation that republished from either would otherwise
	// be invisible, because Run as a whole legitimately publishes on the mint
	// path.
	checked := 0
	for _, fn := range []string{"func (r *Runner) reattachTo(", "func (r *Runner) attachWaiter("} {
		start := strings.Index(src, fn)
		if start < 0 {
			t.Fatalf("%s was not found; this check proves nothing", fn)
		}
		checked++
		body := src[start:]
		if end := strings.Index(body, "\n}\n"); end > 0 {
			body = body[:end]
		}
		for _, forbidden := range []string{"NewRequestID", "PublishSnapshot", "PublishRequest", "Open(", "Retire(", "supersede("} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s references %s; reattaching creates no request, no snapshot and retires no obligation",
					strings.TrimPrefix(fn, "func (r *Runner) "), forbidden)
			}
		}
	}
	if checked != 2 {
		t.Fatalf("inspected %d of the 2 reattachment functions", checked)
	}
}

// Structural: nothing reads a review record's Deadline as expiry.
func TestNoReviewLifecycleBranchReadsTheDeadlineAsExpiry(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		blob, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(blob), "\n") {
			if !strings.Contains(line, ".Deadline") || strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			// Writing it is telemetry; COMPARING it would be expiry.
			for _, cmp := range []string{"Before(", "After(", "Sub(", "IsZero()", "Equal("} {
				if strings.Contains(line, cmp) {
					t.Errorf("%s compares a recorded deadline: %s", name, strings.TrimSpace(line))
				}
			}
		}
	}
}

// A relay converging while nothing is listening is consumed on the next attempt,
// under the same obligation.
func TestARelayConvergingWithNoWaiterIsConsumedUnderTheSameObligation(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(art); err != nil {
		t.Fatal(err)
	}
	posted := len(f.mailbox.posted())

	runner := f.runner()
	runner.NewRequestID = func() string {
		t.Fatal("consuming a converged relay minted a request")
		return ""
	}
	res, err := runner.Run(context.Background(), f.turn(), nil)
	if err != nil {
		t.Fatalf("the converged relay was not consumed: %v", err)
	}
	if res.ReviewDigest != ReviewDigest(art) {
		t.Fatalf("consumed %s, want %s", res.ReviewDigest, ReviewDigest(art))
	}
	if n := len(f.mailbox.posted()); n != posted {
		t.Fatalf("consuming published %d extra comments", n-posted)
	}
	if owed, _ := f.exchanges.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the obligation was not discharged: %+v", owed)
	}
}

// An accepted review stays consumable even if today's assignment moved on.
//
// The provider compared against is the one the REQUEST was published to. A
// review already given for the question that was actually asked is not
// invalidated because the workflow would ask somebody else now.
func TestAnAcceptedReviewSurvivesALaterAssignmentChange(t *testing.T) {
	f := newRelayFixture(t)
	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	if _, err := f.submit(art); err != nil {
		t.Fatal(err)
	}
	runner := f.runner()
	runner.ReviewerProvider = "claude" // the workflow would ask someone else now
	runner.NewRequestID = func() string {
		t.Fatal("an accepted review was discarded and a new request minted")
		return ""
	}
	res, err := runner.Run(context.Background(), f.turn(), nil)
	if err != nil {
		t.Fatalf("an accepted review stopped being consumable after a config change: %v", err)
	}
	if res.ReviewDigest != ReviewDigest(art) {
		t.Fatalf("consumed %s, want %s", res.ReviewDigest, ReviewDigest(art))
	}
	_ = reviewstore.Advisory
	_ = filepath.Join
}

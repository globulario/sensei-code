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
	"github.com/globulario/sensei-code/internal/event"
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

// A request nothing recorded never acquires a waiter.
//
// The published value lives in memory until Open succeeds. Waiting on it would
// listen to a request no later process can find: on timeout it would report an
// owed review naming an id nothing recorded, and after a restart the next run
// would mint again -- the pre-R4 defect, reintroduced by a storage fault.
//
// The old test for this path checked only that the OLD record survived. It never
// asked which request the returned owed-review named, so the runner happily went
// on listening to the unrecorded successor and the test passed.
func TestNoWaiterAttachesToARequestNothingRecorded(t *testing.T) {
	t.Run("a replacement that cannot be recorded reports the predecessor", func(t *testing.T) {
		m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
		first := standingRequest(t, runner, binding)

		// The log becomes read-only AFTER the predecessor exists: listing and
		// opening still work, and only writing a new record fails. So the
		// candidate moves, the successor is genuinely published remotely, and
		// only its record cannot be written.
		if err := os.Chmod(log.Dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(log.Dir, 0o700) })

		moved := binding
		moved.CandidateDigest = "sha256:candidate-two"
		_, err := runner.Run(context.Background(), reviewTurnFor(moved), nil)

		var owed *roles.ReviewUnanswered
		if !errors.As(err, &owed) {
			t.Fatalf("err = %v, want the standing predecessor reported", err)
		}
		// THE POINT: the identity returned is the durable predecessor, never the
		// successor nothing recorded.
		if owed.RequestID != first.RequestID {
			t.Fatalf("the turn reported request %s; the only durable obligation is %s", owed.RequestID, first.RequestID)
		}
		if owed.Binding != first.Binding {
			t.Fatalf("the turn reported candidate %+v; the durable obligation is about %+v", owed.Binding, first.Binding)
		}
		// A successor WAS published remotely -- that is what makes this the hard
		// case -- and it is not what anything waits on or reports.
		ids := publishedRequests(m)
		if len(ids) < 2 {
			t.Fatalf("this test needs the successor to have been published: %v", ids)
		}
		for _, id := range ids {
			if id != first.RequestID && id == owed.RequestID {
				t.Fatalf("the turn reported the unrecorded successor %s", id)
			}
		}
		if err := os.Chmod(log.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if got := soleObligation(t, log); got.RequestID != first.RequestID {
			t.Fatalf("the durable obligation changed: %+v", got)
		}
	})

	t.Run("a first request that cannot be recorded fails closed", func(t *testing.T) {
		m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
		// No predecessor at all, and the log cannot be written. The directory is
		// created first, because Open creates it lazily and a missing directory
		// would fail for a different reason than the one under test.
		if err := os.MkdirAll(log.Dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(log.Dir, 0o500); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(log.Dir, 0o700) })

		_, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
		if !errors.Is(err, roles.ErrReviewUnrecordable) {
			t.Fatalf("err = %v, want ErrReviewUnrecordable", err)
		}
		// NOT an owed review: nothing durable says this candidate owes one, so
		// no later process could reattach to it.
		var owed *roles.ReviewUnanswered
		if errors.As(err, &owed) {
			t.Fatalf("an unrecorded request was reported as a durable owed review: %+v", owed)
		}
		// The published request is named, so an operator can find the orphan.
		ids := publishedRequests(m)
		if len(ids) != 1 {
			t.Fatalf("published requests = %v, want the one that could not be recorded", ids)
		}
		if !strings.Contains(err.Error(), ids[0]) {
			t.Fatalf("the refusal does not name the orphaned request %s: %v", ids[0], err)
		}
	})
}

// A transition that cannot retire its predecessor stops the turn, and withdraws
// nothing.
//
// Creating the successor and failing to retire the old record leaves two active
// obligations -- the state the owner refuses to choose within. Waiting on the
// successor anyway would BE choosing.
//
// It also proves the ORDERING: authority leads transport. No withdrawal is
// posted for an obligation this process still owes, because retracting the old
// request remotely while the durable owner still calls it active would leave two
// local obligations, one pointing at a request that no longer stands, with
// nothing in the records to say which fact was stale.
//
// The fault is injected through a stored record whose own request_id is not a
// safe file name: the file is found and read, and only the removal that retires
// it can fail. Deliberately NOT injected through the withdrawal callback -- that
// mechanism only fires if withdrawal happens first, so it would bake the very
// ordering defect under test into the harness and quietly stop testing anything.
func TestAFailedRetirementStopsTheTurnAndWithdrawsNothing(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)

	// Rewrite the predecessor's record so the id INSIDE it cannot name a file,
	// while the file itself stays exactly where the log expects it.
	path := filepath.Join(log.Dir, first.Binding.TaskID+"."+first.RequestID+".json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(blob, &rec); err != nil {
		t.Fatal(err)
	}
	unaddressable := first.RequestID + " r"
	rec["request_id"] = unaddressable
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
	// The record is still READ as an obligation -- otherwise this would be
	// testing unreadability, not retirement.
	owner := ReviewObligationStore{Exchanges: log}
	if _, found, cerr := owner.Current(first.Binding.TaskID); !found || cerr != nil {
		t.Fatalf("the predecessor is no longer readable as an obligation: found=%v err=%v", found, cerr)
	}
	withdrawalsBefore := len(prWithdrawalsIn(m))

	moved := binding
	moved.CandidateDigest = "sha256:candidate-two"
	_, err = runner.Run(context.Background(), reviewTurnFor(moved), nil)
	if !errors.Is(err, ErrObligationConflict) {
		t.Fatalf("err = %v, want an explicit lifecycle conflict", err)
	}
	if !errors.Is(err, roles.ErrReviewLifecycleFault) {
		t.Fatalf("the conflict is not recognisable to the workflow: %v", err)
	}
	// No verdict, and nothing was consumed or waited through the successor.
	var owed *roles.ReviewUnanswered
	if errors.As(err, &owed) {
		t.Fatalf("a two-obligation task was reported as a single owed review: %+v", owed)
	}
	// AUTHORITY LEADS TRANSPORT: nothing was retracted for an obligation that
	// is still owed here.
	if n := len(prWithdrawalsIn(m)); n != withdrawalsBefore {
		t.Fatalf("a withdrawal was posted for an obligation that could not be retired: %v", prWithdrawalsIn(m))
	}

	standing, lerr := log.PendingReviews()
	if lerr != nil {
		t.Fatal(lerr)
	}
	if len(standing) != 2 {
		t.Fatalf("want both obligations still active after a failed retirement, got %+v", standing)
	}
	// And the next turn refuses rather than choosing between them.
	if _, err := runner.Run(context.Background(), reviewTurnFor(moved), nil); !errors.Is(err, ErrObligationConflict) {
		t.Fatalf("the next turn chose between two obligations: %v", err)
	}
	_ = unaddressable
}

// A successful transition retires locally and only then withdraws remotely.
func TestASupersessionRetiresBeforeItWithdraws(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)

	// Observed at the moment EVERY withdrawal reaches the conversation: by then
	// the predecessor must already be gone from the durable owner.
	//
	// Every one, and latched. Recording only the last observation let an extra
	// withdrawal posted BEFORE the retirement be overwritten by the correct one
	// that followed, so the assertion passed while the ordering it names was
	// violated.
	var withdrawals int
	stillOwedAt := []int{}
	m.onPost = func(body string) []map[string]any {
		if id, err := ParseWithdrawal(body); err == nil && id == first.RequestID {
			withdrawals++
			owed, _ := log.PendingReviews()
			for _, rec := range owed {
				if rec.RequestID == first.RequestID {
					stillOwedAt = append(stillOwedAt, withdrawals)
				}
			}
		}
		return nil
	}

	moved := binding
	moved.CandidateDigest = "sha256:candidate-two"
	second := standingRequest(t, runner, moved)
	if second.RequestID == first.RequestID {
		t.Fatal("the moved candidate reused the old request")
	}
	if withdrawals == 0 {
		t.Fatal("no withdrawal was posted, so the ordering proves nothing")
	}
	if len(stillOwedAt) != 0 {
		t.Fatalf("withdrawal(s) %v were posted while the durable owner still called request %s active; "+
			"authority must lead transport", stillOwedAt, first.RequestID)
	}
	if withdrawals != 1 {
		t.Fatalf("the old request was withdrawn %d times, want once", withdrawals)
	}
	if got := soleObligation(t, log); got.RequestID != second.RequestID {
		t.Fatalf("the surviving obligation is %+v, want the successor", got)
	}
}

// Naming a request by id is not a way around the uniqueness law.
//
// The runner refuses a task with two active obligations. A relay that resolved
// its request directly could still be accepted, published and converged through
// one of them while the other stayed owed and invisible -- consuming a review
// through one obligation while ignoring the other, which is the forbidden state.
func TestARelayCannotSelectOneOfTwoActiveObligationsByRequestID(t *testing.T) {
	f := newRelayFixture(t)
	rec := soleObligation(t, f.exchanges)
	rival := rec
	rival.RequestID = "r-ffffffffffffffff"
	if err := f.exchanges.Open(rival); err != nil {
		t.Fatal(err)
	}
	posted := len(f.mailbox.posted())

	art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	_, err := f.submit(art)
	if !errors.Is(err, ErrObligationConflict) {
		t.Fatalf("err = %v, want the relay refused for a lifecycle conflict", err)
	}
	if _, found, _ := f.store.Load(relayRequest); found {
		t.Fatal("a refused relay left a receipt")
	}
	if _, found, _ := f.reviews.Load(relayRequest); found {
		t.Fatal("a refused relay reached the common review store")
	}
	if n := len(f.mailbox.posted()); n != posted {
		t.Fatalf("a refused relay published %d comment(s)", n-posted)
	}
	if owed, _ := f.exchanges.PendingReviews(); len(owed) != 2 {
		t.Fatalf("the conflict consumed or retired something: %+v", owed)
	}
}

// A caller cancelling the run is not a reviewer failing to answer.
//
// Normalising every context end to ErrNoAnswer sent cancellation to the reviewer
// ladder as this provider's failure, and the next reviewer would be asked for a
// review the caller had just stopped.
func TestParentCancellationKeepsItsOwnIdentity(t *testing.T) {
	_, runner, binding, log := reviewRunnerWithLog(t, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(120 * time.Millisecond)
		cancel()
	}()
	_, err := runner.Run(ctx, reviewTurnFor(binding), nil)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want it to still satisfy errors.Is(context.Canceled)", err)
	}
	if errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatalf("a caller cancellation was reported as an unanswered review: %v", err)
	}
	if errors.Is(err, ErrNoAnswer) {
		t.Fatalf("a caller cancellation was reported as nobody answering: %v", err)
	}
	// The obligation is untouched: cancelling a run ends no review.
	got := soleObligation(t, log)
	if !strings.Contains(err.Error(), got.RequestID) {
		t.Fatalf("the cancellation does not name the standing request %s: %v", got.RequestID, err)
	}
}

// A malformed obligation is unreadable, never "the candidate changed".
//
// Read as a candidate change, the runner would supersede it -- silently
// replacing authority nobody could read, and destroying the only record of what
// went wrong.
func TestAMalformedObligationIsUnreadableRatherThanSuperseded(t *testing.T) {
	for name, corrupt := range map[string]func(*ExchangeRecord){
		"a malformed base":           func(r *ExchangeRecord) { r.BaseSHA = "not-a-sha" },
		"a malformed candidate tree": func(r *ExchangeRecord) { r.CandidateTree = "short" },
		"a missing candidate digest": func(r *ExchangeRecord) { r.CandidateDigest = "" },
		"a malformed review commit":  func(r *ExchangeRecord) { r.ReviewCommit = "nope" },
		"no task":                    func(r *ExchangeRecord) { r.TaskID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
			first := standingRequest(t, runner, binding)
			rec := soleObligation(t, log)
			if err := log.Close(rec.TaskID, rec.RequestID); err != nil {
				t.Fatal(err)
			}
			broken := rec
			corrupt(&broken)
			if broken.TaskID == "" {
				broken.TaskID = rec.TaskID // the file name needs one; the field is what is read
				broken.BaseSHA = ""
			}
			if err := log.Open(broken); err != nil {
				t.Fatal(err)
			}
			published := len(publishedRequests(m))

			runner.NewRequestID = func() string {
				t.Fatal("a malformed obligation was repaired by superseding it")
				return ""
			}
			_, err := runner.Run(context.Background(), reviewTurnFor(binding), nil)
			if !errors.Is(err, ErrObligationUnreadable) {
				t.Fatalf("err = %v, want ErrObligationUnreadable", err)
			}
			if n := len(publishedRequests(m)); n != published {
				t.Fatalf("a malformed obligation caused %d new requests", n-published)
			}
			if w := prWithdrawalsIn(m); len(w) != 0 {
				t.Fatalf("a malformed obligation was withdrawn: %v", w)
			}
			if owed, _ := log.PendingReviews(); len(owed) != 1 {
				t.Fatalf("the malformed record was removed: %+v", owed)
			}
			_ = first
		})
	}
}

// A withdrawal that cannot be posted does not fail the transition.
//
// Locally the predecessor is retired and accepted by nothing, which is the state
// that decides behaviour. The old request may remain visible on the conversation
// -- reported, so an operator is never surprised by it -- but treating that as a
// failed transition would leave the successor unusable because a remote post did
// not land.
//
// The withdrawal is made to fail by pointing the predecessor at a conversation
// this mailbox does not serve, which is also how it looks in life after a
// mailbox move.
func TestAFailedWithdrawalDoesNotFailTheTransition(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 80*time.Millisecond)
	first := standingRequest(t, runner, binding)

	path := filepath.Join(log.Dir, first.Binding.TaskID+"."+first.RequestID+".json")
	blob, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(blob, &rec); err != nil {
		t.Fatal(err)
	}
	rec["conversation"] = "999"
	out, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}

	moved := binding
	moved.CandidateDigest = "sha256:candidate-two"
	var events []event.Event
	_, runErr := runner.Run(context.Background(), reviewTurnFor(moved),
		func(e event.Event) { events = append(events, e) })

	var second *roles.ReviewUnanswered
	if !errors.As(runErr, &second) {
		t.Fatalf("the transition did not complete: %v", runErr)
	}
	if second.RequestID == first.RequestID {
		t.Fatal("the transition did not happen, so the withdrawal failure proves nothing")
	}

	// THE PRECONDITION, asserted rather than assumed: the withdrawal really did
	// fail. Without this the test could not tell "the withdrawal failed and the
	// transition continued" from "the withdrawal quietly succeeded somewhere",
	// and a mutation making a failed withdrawal fail the transition would
	// survive it.
	var reported bool
	for _, e := range events {
		var p struct {
			Superseded    string `json:"superseded_request"`
			Withdrawn     bool   `json:"withdrawn"`
			Closed        bool   `json:"closed"`
			WithdrawError string `json:"withdraw_error"`
		}
		if len(e.Payload) == 0 || json.Unmarshal(e.Payload, &p) != nil || p.Superseded != first.RequestID {
			continue
		}
		reported = true
		if p.Withdrawn {
			t.Fatalf("the withdrawal is reported as having succeeded: %s", e.Summary)
		}
		if strings.TrimSpace(p.WithdrawError) == "" {
			t.Errorf("a failed withdrawal was not reported with its reason: %s", e.Payload)
		}
		if !p.Closed {
			t.Errorf("the predecessor was not retired locally: %s", e.Payload)
		}
	}
	if !reported {
		t.Fatal("the supersession was never reported, so the withdrawal outcome proves nothing")
	}
	if w := prWithdrawalsIn(m); len(w) != 0 {
		t.Fatalf("a withdrawal reached this conversation: %v", w)
	}
	// Retired locally, and exactly one obligation stands.
	got := soleObligation(t, log)
	if got.RequestID != second.RequestID {
		t.Fatalf("the surviving obligation is %+v, want the successor %s", got, second.RequestID)
	}
}

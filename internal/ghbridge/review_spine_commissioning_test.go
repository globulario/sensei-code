package ghbridge

// COMMISSIONING THE REVIEW SPINE (#182 R6).
//
// Not "the units pass". These drive the SHIPPED components against each other
// -- a real git repository and snapshot projection, a real HTTP mailbox, the
// real obligation owner, the real review store, the real relay adapter and the
// real attestation path -- through the sequences an operator actually hits:
// a candidate reviewed and revised, a candidate replaced while its review is
// still owed, a review that arrives while no waiter is listening, and a process
// that dies in the middle.
//
// WHAT IS NOT COMMISSIONED HERE, stated so nobody reads more into a green run
// than it earned: the objective, architect and implementer turns ABOVE the
// review boundary run through workflow.Engine, which R1-R6 did not change and
// which its own package drives with its own harness. These start at "a candidate
// exists and a review of it is owed" and end at "that exact review discharged
// that exact obligation".

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
)

const (
	commissionAccept = `{"decision":"accept","summary":"the ledger invariant holds at physical position 0","instructions":"","findings":[]}`
	commissionRevise = `{"decision":"revise","summary":"the mutation is not shown to be killed","instructions":"kill it",` +
		`"findings":[{"id":"f1","severity":"blocking","claim":"the guard is exercised","reference":"ledger.go",` +
		`"reason":"no assertion fails when the guard is deleted","correction":"add the isolating assertion"}]}`
)

// answerCanonically makes the pinned reviewer principal reply to whatever
// request is published, in the canonical grammar, naming the assigned provider.
func answerCanonically(m *prMailbox, payload func(Request) string) {
	answerWith(m, func(req Request) string {
		body := payload(req)
		if body == "" {
			return ""
		}
		raw, err := reviewartifact.Artifact{
			ReviewerProvider: req.ReviewerProvider, TaskID: req.TaskID, RequestID: req.RequestID,
			BaseSHA: req.BaseSHA, CandidateDigest: req.CandidateDigest, CandidateTree: req.CandidateTree,
			ReviewCommit: req.ReviewCommit, Body: body,
		}.Render()
		if err != nil {
			return ""
		}
		return raw
	}, "davecourtois", 1697116)
}

// A. THE REVISION ARC.
//
// C1 is reviewed and comes back REVISE; the implementer's repair is C2; the
// review of C1 is on the mailbox the whole time and may never qualify C2; C2 is
// ACCEPTed; that exact review discharges that exact obligation; and the owner
// can then override it. Every identity is asserted through the chain.
func TestCommissionTheRevisionArc(t *testing.T) {
	dir, base, tree1, tree2 := tempRepo(t)
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	bare := t.TempDir()
	for _, args := range [][]string{{"init", "--bare", "-q", bare}, {"-C", dir, "remote", "add", "origin", bare}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	log := ExchangeLog{Dir: filepath.Join(dir, "exchanges")}
	reviews := reviewstore.Store{Dir: filepath.Join(dir, "reviews")}
	runner := func() *Runner {
		return &Runner{Issue: box, RepoDir: dir, Remote: "origin", NewRequestID: NewRequestID,
			Poll: 10 * time.Millisecond, Wait: 5 * time.Second, Exchanges: log, Reviews: reviews,
			ReviewerProvider: "chatgpt"}
	}
	c1 := roles.Binding{TaskID: "T", BaseSHA: base, CandidateDigest: "sha256:c1" + strings.Repeat("0", 62), CandidateTree: tree1}
	c2 := roles.Binding{TaskID: "T", BaseSHA: base, CandidateDigest: "sha256:c2" + strings.Repeat("0", 62), CandidateTree: tree2}

	// 1. C1 is reviewed, and the verdict is REVISE.
	answerCanonically(m, func(Request) string { return commissionRevise })
	res, err := runner().Run(context.Background(), reviewTurn(c1), nil)
	if err != nil {
		t.Fatalf("C1 was not reviewed: %v", err)
	}
	r1 := requestFor(t, log, m)
	if !strings.Contains(res.Text, `"decision":"revise"`) {
		t.Fatalf("the C1 verdict is %q, want the reviewer's REVISE", res.Text)
	}
	// The finding is IN the verdict the implementer receives. Nobody carried it.
	if !strings.Contains(res.Text, "no assertion fails when the guard is deleted") {
		t.Fatalf("the finding did not reach the implementer's input: %q", res.Text)
	}
	// R1 is discharged by its own answer.
	if owed, _ := log.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the answered C1 obligation is still owed: %+v", owed)
	}
	c1Review, found, err := reviews.Load(r1)
	if err != nil || !found {
		t.Fatalf("the C1 review was not recorded: found=%v err=%v", found, err)
	}

	// 2. The implementer's repair is C2, and a review of it is owed. The review
	//    of C1 is still sitting on the mailbox.
	answerCanonically(m, func(req Request) string {
		if req.CandidateDigest != c2.CandidateDigest {
			return ""
		}
		return commissionAccept
	})
	res2, err := runner().Run(context.Background(), reviewTurn(c2), nil)
	if err != nil {
		t.Fatalf("C2 was not reviewed: %v", err)
	}
	ids := publishedRequests(m)
	if len(ids) != 2 || ids[0] != r1 {
		t.Fatalf("published requests %v, want exactly R1 then R2", ids)
	}
	r2 := ids[1]
	if r2 == r1 {
		t.Fatal("C2 was reviewed under C1's request id")
	}

	// 3. The stale C1 review could not qualify C2.
	if !strings.Contains(res2.Text, `"decision":"accept"`) {
		t.Fatalf("the C2 verdict is %q, want the ACCEPT that answered R2", res2.Text)
	}
	if strings.Contains(res2.Text, "no assertion fails when the guard is deleted") {
		t.Fatal("the review of C1 qualified C2")
	}
	c2Review, found, err := reviews.Load(r2)
	if err != nil || !found {
		t.Fatalf("the C2 review was not recorded: found=%v err=%v", found, err)
	}
	if c2Review.ReviewDigest == c1Review.ReviewDigest {
		t.Fatal("C1 and C2 are recorded as the same review")
	}
	c2Art, err := c2Review.Artifact()
	if err != nil {
		t.Fatal(err)
	}
	if c2Art.CandidateDigest != c2.CandidateDigest || c2Art.CandidateTree != tree2 || c2Art.RequestID != r2 {
		t.Fatalf("the recorded C2 review is not about C2: %+v", c2Art)
	}
	if !c2Review.Consumable() {
		t.Fatalf("the consumed C2 review is not recorded as delivered: %+v", c2Review.Evidence)
	}
	if res2.ReviewDigest != c2Review.ReviewDigest {
		t.Fatalf("the turn returned digest %s and the store holds %s", res2.ReviewDigest, c2Review.ReviewDigest)
	}
	// 4. Exactly R2 discharged, and nothing is owed.
	if owed, _ := log.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the answered C2 obligation is still owed: %+v", owed)
	}

	// 5. The owner may override THAT review, and the override names it exactly.
	attest := AttestationStore{Dir: filepath.Join(dir, "attestations")}
	rec, err := AcceptAttestation(context.Background(), AttestationSubmission{
		RequestID: r2, ReviewDigest: c2Review.ReviewDigest, Principal: operator,
		Permitted: true, Reviews: reviews, Store: attest, Mailbox: box,
	})
	if err != nil {
		t.Fatalf("the owner could not override the accepted review: %v", err)
	}
	if rec.Attestation.ReviewDigest != c2Review.ReviewDigest || rec.Attestation.RequestID != r2 {
		t.Fatalf("the override does not name the C2 review: %+v", rec.Attestation)
	}
	if rec.Attestation.Binding.CandidateDigest != c2.CandidateDigest {
		t.Fatalf("the override binds to candidate %s, want C2", rec.Attestation.Binding.CandidateDigest)
	}
}

// A'. THE SUPERSESSION ARC.
//
// The other half of the same story: the candidate moves while its review is
// STILL OWED. The predecessor is retired before the withdrawal is posted, one
// obligation remains, and an answer to the old request can never satisfy the new
// one.
func TestCommissionASupersessionWhileTheReviewIsStillOwed(t *testing.T) {
	dir, base, tree1, tree2 := tempRepo(t)
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	bare := t.TempDir()
	for _, args := range [][]string{{"init", "--bare", "-q", bare}, {"-C", dir, "remote", "add", "origin", bare}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	log := ExchangeLog{Dir: filepath.Join(dir, "exchanges")}
	reviews := reviewstore.Store{Dir: filepath.Join(dir, "reviews")}
	newRunner := func(wait time.Duration) *Runner {
		return &Runner{Issue: box, RepoDir: dir, Remote: "origin", NewRequestID: NewRequestID,
			Poll: 10 * time.Millisecond, Wait: wait, Exchanges: log, Reviews: reviews,
			ReviewerProvider: "chatgpt"}
	}
	c1 := roles.Binding{TaskID: "T", BaseSHA: base, CandidateDigest: "sha256:c1" + strings.Repeat("0", 62), CandidateTree: tree1}
	c2 := roles.Binding{TaskID: "T", BaseSHA: base, CandidateDigest: "sha256:c2" + strings.Repeat("0", 62), CandidateTree: tree2}

	// C1's review goes unanswered: the obligation stands.
	if _, err := newRunner(80*time.Millisecond).Run(context.Background(), reviewTurn(c1), nil); !errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatalf("C1's review was not left owed: %v", err)
	}
	owed, _ := log.PendingReviews()
	if len(owed) != 1 {
		t.Fatalf("C1 left %d obligations, want 1", len(owed))
	}
	r1 := owed[0].RequestID

	// The candidate moves. The successor is published and recorded, THEN the
	// predecessor is retired, and only then is its withdrawal posted.
	answerCanonically(m, func(req Request) string {
		if req.RequestID == r1 {
			return ""
		}
		return commissionAccept
	})
	if _, err := newRunner(5*time.Second).Run(context.Background(), reviewTurn(c2), nil); err != nil {
		t.Fatalf("C2 was not reviewed: %v", err)
	}
	ids := publishedRequests(m)
	if len(ids) != 2 || ids[0] != r1 {
		t.Fatalf("published requests %v, want R1 then its successor", ids)
	}
	r2 := ids[1]

	// Exactly one obligation ever stood at a time, and the survivor answered.
	if left, _ := log.PendingReviews(); len(left) != 0 {
		t.Fatalf("obligations remain after the successor was answered: %+v", left)
	}
	if _, found, _ := reviews.Load(r1); found {
		t.Fatal("the superseded request acquired a review")
	}
	rec, found, err := reviews.Load(r2)
	if err != nil || !found {
		t.Fatalf("the successor's review was not recorded: found=%v err=%v", found, err)
	}
	art, err := rec.Artifact()
	if err != nil {
		t.Fatal(err)
	}
	if art.CandidateTree != tree2 {
		t.Fatalf("the recorded review is about tree %s, want C2's %s", art.CandidateTree, tree2)
	}
	// And the withdrawal named the predecessor, posted after it was retired.
	var withdrew bool
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		if strings.HasPrefix(strings.TrimLeft(body, " \t\r\n"), WithdrawnMarker) && strings.Contains(body, r1) {
			withdrew = true
		}
	}
	if !withdrew {
		t.Fatal("the superseded request was never withdrawn on the mailbox")
	}
}

// B. RESTART ACROSS A REAL PROCESS BOUNDARY.
//
// A separate OS process publishes the request and waits. It is KILLED -- no
// deferred cleanup, no graceful shutdown, no goroutine cancellation. The review
// is then posted while nothing is listening. A second process comes up on the
// same workspace and must reattach to the SAME request, consume that exact
// review and discharge it, with no request reminted and no candidate rebuilt.
func TestCommissionARestartAcrossARealProcessBoundary(t *testing.T) {
	if os.Getenv("SPINE_COMMISSION_ROLE") != "" {
		return // this binary is the child; see spineCommissionChild.
	}
	dir, base, tree1, _ := tempRepo(t)
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	bare := t.TempDir()
	for _, args := range [][]string{{"init", "--bare", "-q", bare}, {"-C", dir, "remote", "add", "origin", bare}} {
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	apiBase := box.API.Auth.APIBase
	env := []string{
		"SPINE_COMMISSION_ROLE=publish",
		"SPINE_DIR=" + dir, "SPINE_BASE=" + base, "SPINE_TREE=" + tree1,
		"SPINE_API=" + apiBase, "SPINE_KEY=" + keyPath,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@e", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
	}

	// --- process 1: publish the request, then wait forever. ---------------
	first := exec.Command(os.Args[0], "-test.run=TestCommissionARestartAcrossARealProcessBoundary", "-test.timeout=120s")
	first.Env = append(os.Environ(), env...)
	first.Stderr = os.Stderr
	if err := first.Start(); err != nil {
		t.Fatalf("starting the publishing process: %v", err)
	}
	firstPID := first.Process.Pid
	log := ExchangeLog{Dir: filepath.Join(dir, "exchanges")}
	var r1 string
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		if owed, err := log.PendingReviews(); err == nil && len(owed) == 1 {
			r1 = owed[0].RequestID
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r1 == "" {
		_ = first.Process.Kill()
		t.Fatal("the publishing process never recorded a durable review obligation")
	}
	owed, _ := log.PendingReviews()
	candidate := owed[0].Subject()

	// --- kill it. no cleanup runs. ----------------------------------------
	if err := first.Process.Kill(); err != nil {
		t.Fatalf("killing the publishing process: %v", err)
	}
	_ = first.Wait()
	if first.ProcessState == nil || first.ProcessState.Exited() && first.ProcessState.ExitCode() == 0 {
		t.Fatalf("the publishing process was not killed: %v", first.ProcessState)
	}
	t.Logf("commissioning: process %d published request %s and was killed (%v)", firstPID, r1, first.ProcessState)

	// The obligation is durable, and nothing is listening to it.
	if owed, _ := log.PendingReviews(); len(owed) != 1 || owed[0].RequestID != r1 {
		t.Fatalf("the obligation did not survive process death: %+v", owed)
	}

	// --- the reviewer answers while no waiter exists. ---------------------
	var req *Request
	for _, c := range m.snapshot() {
		body, _ := c["body"].(string)
		if parsed, ok := ParseRequest(body); ok && parsed.RequestID == r1 {
			req = &parsed
		}
	}
	if req == nil {
		t.Fatal("the published request is not on the mailbox")
	}
	raw, err := reviewartifact.Artifact{
		ReviewerProvider: req.ReviewerProvider, TaskID: req.TaskID, RequestID: req.RequestID,
		BaseSHA: req.BaseSHA, CandidateDigest: req.CandidateDigest, CandidateTree: req.CandidateTree,
		ReviewCommit: req.ReviewCommit, Body: commissionAccept,
	}.Render()
	if err != nil {
		t.Fatal(err)
	}
	// Under the fixture's lock: the killed process may still have a request in
	// flight on a server goroutine, and that goroutine reads this list.
	m.append(map[string]any{
		"id": float64(90210), "body": raw,
		"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
	})

	// --- process 2: same workspace, same candidate. -----------------------
	second := exec.Command(os.Args[0], "-test.run=TestCommissionARestartAcrossARealProcessBoundary", "-test.timeout=120s")
	second.Env = append(os.Environ(), append(append([]string{}, env...), "SPINE_COMMISSION_ROLE=resume")...)
	out, err := second.CombinedOutput()
	// NO REMINT, checked first. A resume that publishes its own request fails in
	// several ways downstream, and every one of them would otherwise be reported
	// as "the child process died" -- which names the symptom, not the defect.
	if ids := publishedRequests(m); len(ids) != 1 || ids[0] != r1 {
		t.Fatalf("the restart minted requests %v, want only the original %s\n%s", ids, r1, out)
	}
	if err != nil {
		t.Fatalf("the resuming process failed: %v\n%s", err, out)
	}
	secondPID := 0
	var consumed struct {
		PID       int    `json:"pid"`
		RequestID string `json:"request_id"`
		Digest    string `json:"review_digest"`
		Text      string `json:"text"`
		Requests  int    `json:"requests_on_mailbox"`
	}
	line := ""
	for _, l := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(l, "SPINE_RESULT ") {
			line = strings.TrimPrefix(l, "SPINE_RESULT ")
		}
	}
	if line == "" {
		t.Fatalf("the resuming process reported nothing:\n%s", out)
	}
	if err := json.Unmarshal([]byte(line), &consumed); err != nil {
		t.Fatalf("unreadable result %q: %v", line, err)
	}
	secondPID = consumed.PID
	if secondPID == firstPID || secondPID == os.Getpid() {
		t.Fatalf("the resume did not cross a process boundary: first=%d second=%d self=%d",
			firstPID, secondPID, os.Getpid())
	}
	t.Logf("commissioning: process %d reattached to %s and consumed %s", secondPID, consumed.RequestID, consumed.Digest)

	// THE SAME request, THE SAME candidate, no remint, no rebuild.
	if consumed.RequestID != r1 {
		t.Fatalf("the resume consumed request %s, want the standing %s", consumed.RequestID, r1)
	}
	if consumed.Digest != reviewartifact.Digest(raw) {
		t.Fatalf("the resume consumed review %s, want %s", consumed.Digest, reviewartifact.Digest(raw))
	}
	if strings.TrimSpace(consumed.Text) != commissionAccept {
		t.Fatalf("the resume returned %q", consumed.Text)
	}
	if ids := publishedRequests(m); len(ids) != 1 || ids[0] != r1 {
		t.Fatalf("the restart minted requests %v, want only the original %s", ids, r1)
	}
	if owed, _ := log.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the consumed obligation is still owed: %+v", owed)
	}
	// The candidate identity never moved.
	reviews := reviewstore.Store{Dir: filepath.Join(dir, "reviews")}
	rec, found, err := reviews.Load(r1)
	if err != nil || !found {
		t.Fatalf("the consumed review is not recorded: found=%v err=%v", found, err)
	}
	art, err := rec.Artifact()
	if err != nil {
		t.Fatal(err)
	}
	if subjectOf(art) != candidate {
		t.Fatalf("the consumed review is about %+v, want the candidate the dead process published %+v",
			subjectOf(art), candidate)
	}
}

// spineCommissionChild is this test binary running as one of the commissioning
// processes. It is a real OS process with its own pid, its own memory and no
// share of the parent's state beyond the files and the mailbox URL.
func spineCommissionChild(role string) {
	dir := os.Getenv("SPINE_DIR")
	box := Issue{
		Number: "157",
		API: &AppClient{
			Auth: &InstallationAuth{AppID: 4850747, InstallationID: 159521273,
				PrivateKeyPath: os.Getenv("SPINE_KEY"), APIBase: os.Getenv("SPINE_API")},
			Owner: "globulario", Repo: "sensei-code",
		},
		ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"},
	}
	wait := 90 * time.Second
	if role == "resume" {
		wait = 20 * time.Second
	}
	runner := &Runner{
		Issue: box, RepoDir: dir, Remote: "origin", NewRequestID: NewRequestID,
		Poll: 20 * time.Millisecond, Wait: wait,
		Exchanges:        ExchangeLog{Dir: filepath.Join(dir, "exchanges")},
		Reviews:          reviewstore.Store{Dir: filepath.Join(dir, "reviews")},
		ReviewerProvider: "chatgpt",
	}
	binding := roles.Binding{TaskID: "T", BaseSHA: os.Getenv("SPINE_BASE"),
		CandidateDigest: "sha256:c1" + strings.Repeat("0", 62), CandidateTree: os.Getenv("SPINE_TREE")}
	res, err := runner.Run(context.Background(), agent.Request{
		Role: roles.Reviewer, TaskID: "T", Binding: binding}, nil)
	if role == "publish" {
		// Never reached: the parent kills this process while it waits.
		fmt.Fprintf(os.Stderr, "publishing process returned early: %v\n", err)
		os.Exit(3)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "resume: %v\n", err)
		os.Exit(4)
	}
	// Which request this process consumed, read back from the durable record it
	// wrote -- not from a value it was handed.
	consumedRequest := ""
	entries, _ := os.ReadDir(filepath.Join(dir, "reviews"))
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".json") {
			consumedRequest = strings.TrimSuffix(e.Name(), ".json")
		}
	}
	blob, _ := json.Marshal(map[string]any{
		"pid": os.Getpid(), "request_id": consumedRequest,
		"review_digest": res.ReviewDigest, "text": res.Text,
	})
	fmt.Println("SPINE_RESULT " + string(blob))
	os.Exit(0)
}

// TestMain lets this test binary also BE one of the commissioning processes.
//
// The re-exec idiom, deliberately: a real fork/exec with its own pid, its own
// memory and no inherited state beyond the workspace files and the mailbox URL.
// Cancelling a goroutine would have proven that a waiter can stop; only a dead
// process proves that the obligation outlives the thing that was waiting on it.
func TestMain(m *testing.M) {
	if role := os.Getenv("SPINE_COMMISSION_ROLE"); role != "" {
		spineCommissionChild(role)
		return
	}
	os.Exit(m.Run())
}

// E. EVERY PRESERVED STATE IS REDISCOVERABLE.
//
// The three ways a review turn can end without a verdict, driven against the
// real components, and then re-entered by a NEW runner over the SAME workspace:
// the obligation must be the same one, the condition must be the same
// condition, and the durable records must be byte-identical. A preserved state
// that a later process cannot find again is not preserved.
func TestCommissionEveryPreservedStateSurvivesAndIsRediscovered(t *testing.T) {
	type preserved struct {
		// setup leaves the workspace in the state under test and returns the
		// condition the turn reported.
		setup func(t *testing.T, f relayFixture) error
		// same reports whether a second, independent runner reports the same
		// condition over the same workspace.
		same func(err error) bool
		kind string
	}
	for name, tc := range map[string]preserved{
		"nobody answered": {
			kind: "unanswered",
			setup: func(t *testing.T, f relayFixture) error {
				_, err := f.runner().Run(context.Background(), f.turn(), nil)
				return err
			},
			same: func(err error) bool { return errors.Is(err, roles.ErrReviewUnanswered) },
		},
		"a review is held here and not yet delivered": {
			kind: "delivery pending",
			setup: func(t *testing.T, f relayFixture) error {
				f.mailbox.failPosts = true
				art := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
				if _, err := f.submit(art); !errors.Is(err, ErrRelayPublication) {
					t.Fatalf("staging: %v", err)
				}
				f.mailbox.failPosts = false
				_, err := f.runner().Run(context.Background(), f.turn(), nil)
				return err
			},
			same: func(err error) bool {
				var fault *roles.ReviewObservationFault
				return errors.As(err, &fault) && fault.Has(roles.ObservedDeliveryPending)
			},
		},
		"the reviewer replied with something unusable": {
			kind: "observation fault",
			setup: func(t *testing.T, f relayFixture) error {
				// The request locator has to sit below the comments this fixture
				// posts, or the response window excludes them and the case would
				// be measuring the window rather than the observation.
				rec := soleObligation(t, f.exchanges)
				if err := f.exchanges.Close(rec.TaskID, rec.RequestID); err != nil {
					t.Fatal(err)
				}
				rec.RequestComment = 1
				if err := f.exchanges.Open(rec); err != nil {
					t.Fatal(err)
				}
				f.mailbox.add(`{"decision":"accept","summary":"no envelope at all"}`, "davecourtois", 1697116)
				_, err := f.runner().Run(context.Background(), f.turn(), nil)
				return err
			},
			same: func(err error) bool {
				var fault *roles.ReviewObservationFault
				return errors.As(err, &fault) && fault.Has(roles.ObservedMalformed)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newRelayFixture(t)
			first := tc.setup(t, f)
			if first == nil {
				t.Fatalf("the %s state did not preserve anything: the turn succeeded", tc.kind)
			}
			if !tc.same(first) {
				t.Fatalf("the %s state reported %v", tc.kind, first)
			}
			before := snapshotWorkspace(t, f)

			// A SECOND runner, built from nothing this turn is holding. The
			// obligation is not handed over; it is found again.
			again, err := (&Runner{Issue: f.box, NewRequestID: NewRequestID, Poll: 10 * time.Millisecond,
				Wait: 50 * time.Millisecond, Exchanges: f.exchanges, Reviews: f.reviews,
				ReviewerProvider: "chatgpt"}).Run(context.Background(), f.turn(), nil)
			_ = again
			if !tc.same(err) {
				t.Fatalf("a second runner reported %v, want the same %s condition", err, tc.kind)
			}
			owed, _ := f.exchanges.PendingReviews()
			if len(owed) != 1 || owed[0].RequestID != relayRequest {
				t.Fatalf("the obligation was not rediscovered: %+v", owed)
			}
			if after := snapshotWorkspace(t, f); after != before {
				t.Fatalf("rediscovering the %s state changed the durable records:\n--- before\n%s\n--- after\n%s",
					tc.kind, before, after)
			}
		})
	}
}

// snapshotWorkspace renders every durable record this workspace holds, so
// "nothing changed" is checked against the bytes rather than asserted.
func snapshotWorkspace(t *testing.T, f relayFixture) string {
	t.Helper()
	var b strings.Builder
	for _, dir := range []string{f.exchanges.Dir, f.reviews.Dir} {
		entries, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			blob, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&b, "%s/%s\n%s\n", filepath.Base(dir), e.Name(), blob)
		}
	}
	return b.String()
}

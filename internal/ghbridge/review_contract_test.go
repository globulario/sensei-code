package ghbridge

// THE PUBLISHED REQUEST MUST BE SUFFICIENT.
//
// A remote reviewer answers from one comment. If that comment does not carry
// the grammar its answer will be parsed with, the protocol depends on a
// standing instruction nobody versions -- which is how all 16 real answers on
// the mailbox arrived in a grammar R2 had already retired.
//
// The reviewer simulated here knows NOTHING: no field names, no marker, no
// order. It obeys the contract the request carried. That is the whole point,
// and it is why these cannot use reviewartifact.Render to build the answer.

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// obeyTheRequest is a reviewer with no standing grammar.
//
// It finds the envelope the request taught, copies it verbatim, and puts its
// JSON where the placeholder was. It never names a field and never consults
// this repository, its config or its HEAD.
func obeyTheRequest(t *testing.T, requestBody, payload string) string {
	t.Helper()
	reply, err := obeyOrFail(requestBody, payload)
	if err != nil {
		t.Fatalf("%v:\n%s", err, requestBody)
	}
	return reply
}

// obeyOrFail is the same reviewer, usable from a SERVER GOROUTINE.
//
// The mailbox fixture answers inside its own handler, and t.Fatalf there runs
// on the wrong goroutine: it aborts the handler, the client sees an EOF, and
// the test reports a transport error instead of the protocol defect that caused
// it. The failure is returned and asserted on the test's own goroutine.
func obeyOrFail(requestBody, payload string) (string, error) {
	start := strings.Index(requestBody, reviewartifact.Marker)
	if start < 0 {
		return "", errors.New("the published request teaches no reply envelope")
	}
	taught := requestBody[start:]
	if !strings.Contains(taught, reviewartifact.PayloadPlaceholder) {
		return "", errors.New("the published request marks no payload slot")
	}
	return strings.Replace(taught, reviewartifact.PayloadPlaceholder, payload, 1), nil
}

// publishedRequestBody publishes one real request and returns the exact comment
// body that reached the mailbox.
func publishedRequestBody(t *testing.T, note string) (*prMailbox, Request, string) {
	t.Helper()
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	req := Request{
		Subject: Subject{
			TaskID: "task-1789498413173471174", BaseSHA: "91b475a172bba0257fd2ffd8a55d3edce582e883",
			CandidateDigest: "sha256:caa4e6970000000000000000000000000000000000000000000000000000beef",
			CandidateTree:   "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
			ReviewCommit:    "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
		},
		RequestID: "r-0123456789abcdef", Kind: KindReview, ReviewerProvider: "chatgpt",
		MailboxRepository: "globulario/sensei-code", WorkspaceRepository: "globulario/sensei-code",
	}
	if _, _, err := PublishRequest(context.Background(), box, req, note); err != nil {
		t.Fatalf("publishing the request: %v", err)
	}
	posted := m.snapshot()
	if len(posted) != 1 {
		t.Fatalf("publishing posted %d comments, want 1", len(posted))
	}
	body, _ := posted[0]["body"].(string)
	return m, req, body
}

const contractNote = "Review the candidate.\n\n" + workflow.ReviewPayloadHeading +
	"\n{\"decision\":\"accept|revise\",\"summary\":\"...\"}"

const contractVerdict = `{"decision":"accept","summary":"the ledger invariant holds at physical position 0","instructions":"","findings":[]}`

//  1. A published request teaches a reply this repository's own parser accepts,
//     bound to exactly the identity the request carried.
func TestThePublishedRequestTeachesAParseableReply(t *testing.T) {
	_, req, body := publishedRequestBody(t, contractNote)

	art, err := reviewartifact.Parse(obeyTheRequest(t, body, contractVerdict))
	if err != nil {
		t.Fatalf("a reply obeying the published request does not parse: %v", err)
	}
	if subjectOf(art) != req.Subject {
		t.Fatalf("the taught reply binds %+v, want the request's %+v", subjectOf(art), req.Subject)
	}
	if art.RequestID != req.RequestID {
		t.Fatalf("the taught reply answers %s, want %s", art.RequestID, req.RequestID)
	}
	if art.ReviewerProvider != req.ReviewerProvider {
		t.Fatalf("the taught reply names reviewer %q, want the assigned %q", art.ReviewerProvider, req.ReviewerProvider)
	}
	// And the request is still a request: the taught envelope must not make it
	// parse as an answer to itself.
	if _, ok := ParseRequest(body); !ok {
		t.Fatal("the published comment stopped being a readable review request")
	}
}

//  2. The taught reviewer is the WORKFLOW's assignment, never a GitHub login or
//     this process's configuration.
func TestTheTaughtReviewerComesFromTheAssignmentNotTheMailbox(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newPRMailbox(t, keyPath, "157", true)
	// The mailbox authenticates a completely different account, and the
	// publisher is the App. Neither may reach the taught provider.
	box.ExpectedReviewer = Principal{UserID: 424242, Login: "somebody-else"}
	req := Request{
		Subject: Subject{
			TaskID: "T", BaseSHA: "91b475a172bba0257fd2ffd8a55d3edce582e883",
			CandidateDigest: "sha256:caa4e6970000000000000000000000000000000000000000000000000000beef",
			CandidateTree:   "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
			ReviewCommit:    "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
		},
		RequestID: "r-0123456789abcdef", Kind: KindReview, ReviewerProvider: "chatgpt",
	}
	if _, _, err := PublishRequest(context.Background(), box, req, contractNote); err != nil {
		t.Fatal(err)
	}
	body, _ := m.snapshot()[0]["body"].(string)
	art, err := reviewartifact.Parse(obeyTheRequest(t, body, contractVerdict))
	if err != nil {
		t.Fatal(err)
	}
	if art.ReviewerProvider != "chatgpt" {
		t.Fatalf("the taught reviewer is %q; the mailbox login and publisher must not reach it", art.ReviewerProvider)
	}
	for _, leaked := range []string{"somebody-else", "globulario-sensei-code[bot]"} {
		if strings.Contains(body, "reviewer="+leaked) {
			t.Fatalf("the request teaches reviewer=%s, which is a transport identity", leaked)
		}
	}
}

// 3. The canonical response grammar has ONE owner.
//
// Read as syntax, not text: the request publisher must obtain its template by
// calling reviewartifact, and no production file in this package may spell the
// review RESPONSE marker for itself. The REQUEST envelope is this package's own
// and is deliberately not covered.
func TestTheResponseGrammarHasOneOwner(t *testing.T) {
	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, "transport.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var delegates bool
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "reviewResponseContract" {
			continue
		}
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); ok {
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "reviewartifact" && sel.Sel.Name == "ResponseContract" {
					delegates = true
				}
			}
			return true
		})
	}
	if !delegates {
		t.Fatal("the request publisher does not obtain its reply template from reviewartifact")
	}

	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		checked++
		blob, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(blob), `"`+reviewartifact.Marker+`"`) {
			t.Errorf("%s spells the review response marker for itself; reviewartifact owns it", name)
		}
	}
	if checked < 5 {
		t.Fatalf("inspected %d production files; this check proves nothing", checked)
	}
}

// 4. The published request carries no unqualified JSON-only instruction.
func TestThePublishedRequestDoesNotClaimTheWholeReplyIsJSON(t *testing.T) {
	_, _, body := publishedRequestBody(t, contractNote)
	if strings.Contains(body, workflow.ReviewPayloadHeading) {
		t.Fatalf("the published request still says %q, which is false over this transport:\n%s",
			workflow.ReviewPayloadHeading, body)
	}
	if !strings.Contains(body, "Your reviewer payload is exactly this JSON object") {
		t.Fatalf("the request does not distinguish the payload from the artifact:\n%s", body)
	}
	if !strings.Contains(body, "must be exactly one canonical review artifact") {
		t.Fatalf("the request does not state that the whole comment is the artifact:\n%s", body)
	}
}

// 5. Both reviewer semantic modes are taught the same OUTER artifact.
//
// The payload schema differs between a candidate review and a read-only
// inspection; the envelope that carries it does not, because it comes from the
// request's identity rather than from the prose.
func TestBothReviewModesTeachTheSameOuterArtifact(t *testing.T) {
	inspection := "Inspect the repository and report.\n\n" + workflow.ReviewPayloadHeading +
		"\n{\"decision\":\"accept|revise\",\"summary\":\"...\",\"observations\":[]}"

	var envelopes []string
	for name, note := range map[string]string{"candidate review": contractNote, "inspection review": inspection} {
		_, _, body := publishedRequestBody(t, note)
		reply := obeyTheRequest(t, body, contractVerdict)
		if _, err := reviewartifact.Parse(reply); err != nil {
			t.Fatalf("%s: the taught reply does not parse: %v", name, err)
		}
		envelopes = append(envelopes, strings.Split(reply, contractVerdict)[0])
		if strings.Contains(body, workflow.ReviewPayloadHeading) {
			t.Errorf("%s: the published request keeps the JSON-only heading", name)
		}
	}
	if len(envelopes) != 2 || envelopes[0] != envelopes[1] {
		t.Fatalf("the two review modes teach different outer artifacts:\n%q\n%q", envelopes[0], envelopes[1])
	}
}

// 6. REMOTE-STYLE SIMULATION, driven to the terminal.
//
// A reviewer that knows only "obey the contract the request carried" answers on
// the mailbox, and the real runner consumes it: BOUND_CANONICAL, recorded in
// ReviewStore as the exact posted bytes, obligation discharged, verdict
// returned to the workflow.
func TestAReviewerWithNoStandingGrammarClosesTheLoop(t *testing.T) {
	m, runner, binding, log := reviewRunnerWithLog(t, 5*time.Second)
	var posted string
	var remoteErr error
	m.onPost = func(requestBody string) []map[string]any {
		if _, ok := ParseRequest(requestBody); !ok {
			return nil
		}
		// The whole of the remote's knowledge is this comment.
		reply, err := obeyOrFail(requestBody, contractVerdict)
		if err != nil {
			remoteErr = err
			return nil
		}
		posted = reply
		nextAnswerID++
		return []map[string]any{{
			"id": float64(nextAnswerID), "body": posted,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}

	res, err := runner.Run(context.Background(), reviewTurn(binding), nil)
	if remoteErr != nil {
		t.Fatalf("a reviewer with no standing grammar could not answer this request: %v", remoteErr)
	}
	if err != nil {
		t.Fatalf("a reply obeying the request was not consumed: %v", err)
	}
	if strings.TrimSpace(res.Text) != contractVerdict {
		t.Fatalf("the workflow received %q, want the reviewer's payload", res.Text)
	}
	if res.Session != roles.Unverified {
		t.Fatalf("a consumed review claimed session %q", res.Session)
	}
	requestID := requestFor(t, log, m)
	rec, found, err := runner.Reviews.Load(requestID)
	if err != nil || !found {
		t.Fatalf("the review was not recorded: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != posted {
		t.Fatal("the recorded bytes are not the ones the reviewer posted")
	}
	if !rec.Consumable() {
		t.Fatal("the consumed review is not recorded as delivered")
	}
	if owed, _ := log.PendingReviews(); len(owed) != 0 {
		t.Fatalf("the answered obligation is still owed: %+v", owed)
	}
}

// 7. A historical pre-R2 answer stays malformed. No compatibility, no repair.
func TestAPreR2AnswerIsStillMalformed(t *testing.T) {
	m, runner, binding, _ := reviewRunnerWithLog(t, 150*time.Millisecond)
	m.onPost = func(requestBody string) []map[string]any {
		req, ok := ParseRequest(requestBody)
		if !ok {
			return nil
		}
		nextAnswerID++
		return []map[string]any{{
			"id": float64(nextAnswerID), "body": legacyEnvelope(req) + contractVerdict,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}
	_, err := runner.Run(context.Background(), reviewTurn(binding), nil)
	var fault *roles.ReviewObservationFault
	if !errors.As(err, &fault) || !fault.Has(roles.ObservedMalformed) {
		t.Fatalf("a pre-R2 answer was not reported as malformed: %v", err)
	}
	if errors.Is(err, roles.ErrReviewUnanswered) {
		t.Fatalf("a real reviewer reply was reported as silence: %v", err)
	}
}

// 8. A canonical reply naming another provider does not satisfy the obligation.
func TestACanonicalReplyFromAnotherProviderDoesNotSatisfy(t *testing.T) {
	m, runner, binding, _ := reviewRunnerWithLog(t, 150*time.Millisecond)
	m.onPost = func(requestBody string) []map[string]any {
		req, ok := ParseRequest(requestBody)
		if !ok {
			return nil
		}
		// Obeys the contract in every respect except the one identity a
		// reviewer must not choose for itself.
		base, oerr := obeyOrFail(requestBody, contractVerdict)
		if oerr != nil {
			return nil
		}
		reply := strings.Replace(base, "reviewer="+req.ReviewerProvider, "reviewer=claude", 1)
		nextAnswerID++
		return []map[string]any{{
			"id": float64(nextAnswerID), "body": reply,
			"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
		}}
	}
	_, err := runner.Run(context.Background(), reviewTurn(binding), nil)
	var fault *roles.ReviewObservationFault
	if !errors.As(err, &fault) || !fault.Has(roles.ObservedWrongTarget) {
		t.Fatalf("a reply naming another provider was not reported as wrong-target: %v", err)
	}
}

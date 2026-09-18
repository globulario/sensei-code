package ghbridge

import (
	"github.com/globulario/sensei-code/internal/reviewartifact"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/roles"
)

const (
	baseSHA      = "1a2b3c4d5e6f78901234567890abcdef12345678"
	treeC1       = "aaaabbbbccccdddd1111222233334444aaaabbbb"
	treeC2       = "ffffeeeeddddcccc9999888877776666ffffeeee"
	commitC1     = "9f8e7d6c5b4a39201234567890abcdef87654321"
	commitC2     = "0102030405060708090a0b0c0d0e0f1011121314"
	digestC1     = "sha256:candidate-one"
	digestC2     = "sha256:candidate-two"
	reviewerJSON = `{"decision":"accept","summary":"ok","instructions":"","findings":[]}`
)

func subjC1() Subject {
	return Subject{TaskID: "T-1", BaseSHA: baseSHA, CandidateDigest: digestC1,
		CandidateTree: treeC1, ReviewCommit: commitC1}
}

func reqC1() Request {
	return Request{Subject: subjC1(), RequestID: "r-1", Kind: KindReview, ReviewerProvider: "chatgpt"}
}

// canonicalAnswer renders a reviewer's answer in the CANONICAL grammar: the
// same one the relay uses, naming the provider that was asked.
//
// Test fixtures build answers the way a reviewer does. Before R2 this package
// accepted an envelope with no reviewer= line, so a fixture could prove the
// mailbox worked while proving nothing about who answered.
func canonicalAnswer(t *testing.T, s Subject, requestID, provider, body string) string {
	t.Helper()
	raw, err := reviewartifact.Artifact{
		ReviewerProvider: provider,
		TaskID:           s.TaskID,
		RequestID:        requestID,
		BaseSHA:          s.BaseSHA,
		CandidateDigest:  s.CandidateDigest,
		CandidateTree:    s.CandidateTree,
		ReviewCommit:     s.ReviewCommit,
		Body:             body,
	}.Render()
	if err != nil {
		t.Fatalf("rendering a canonical answer: %v", err)
	}
	return raw
}

func TestRequestMarkerRoundTrips(t *testing.T) {
	in := reqC1()
	m, err := in.Marker()
	if err != nil {
		t.Fatalf("marker: %v", err)
	}
	got, ok := ParseRequest("preamble\n\n" + m + "\nplease review")
	if !ok {
		t.Fatal("marker did not parse back")
	}
	if got != in {
		t.Errorf("round trip changed the request:\n got %+v\nwant %+v", got, in)
	}
}

// Every identity field is required. A request missing one names an artifact
// only partly, and the missing half is exactly what would get guessed.
func TestRequestRequiresEveryIdentityField(t *testing.T) {
	for _, tc := range []struct {
		name string
		mut  func(*Request)
	}{
		{"no task", func(r *Request) { r.TaskID = "" }},
		{"no base", func(r *Request) { r.BaseSHA = "" }},
		{"short base", func(r *Request) { r.BaseSHA = "1a2b3c4" }},
		{"no digest", func(r *Request) { r.CandidateDigest = "" }},
		{"no tree", func(r *Request) { r.CandidateTree = "" }},
		{"short tree", func(r *Request) { r.CandidateTree = "aaaa" }},
		{"no review commit", func(r *Request) { r.ReviewCommit = "" }},
		{"uppercase tree", func(r *Request) { r.CandidateTree = strings.ToUpper(treeC1) }},
		{"no request id", func(r *Request) { r.RequestID = "" }},
		{"architecture kind", func(r *Request) { r.Kind = Kind("architecture") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := reqC1()
			tc.mut(&r)
			if err := r.Validate(); err == nil {
				t.Fatal("expected refusal")
			}
			if _, err := r.Marker(); err == nil {
				t.Fatal("expected marker refusal")
			}
		})
	}
}

// This bridge is reviewer-only. Architecture turns happen before a candidate
// exists and have no tree or digest to bind to.
func TestOnlyReviewKindExists(t *testing.T) {
	if !KindReview.Valid() {
		t.Error("review should be valid")
	}
	for _, k := range []Kind{"architecture", "merge", ""} {
		if k.Valid() {
			t.Errorf("kind %q was accepted", k)
		}
	}
}

// The transport envelope must carry no decision. Two representations of a
// verdict agree only until they don't.
//
// Only the REQUEST envelope is this package's now. The review response envelope
// belongs to reviewartifact, which proves the same rule about itself in
// TestRenderStatesTheVerdictOnlyInThePayload.
func TestTransportMarkerCarriesNoVerdict(t *testing.T) {
	rm, err := reqC1().Marker()
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"verdict", "decision", "accept", "revise", "approve"} {
		if strings.Contains(strings.ToLower(rm), word) {
			t.Errorf("request marker mentions %q — the decision belongs to the payload", word)
		}
	}
}

// An ordinary comment is neither a request nor a review, in EITHER grammar.
//
// Checked through reviewartifact.Parse because that is now the only thing that
// reads a review. Until R6 this package had a second parser that accepted an
// envelope with no reviewer= line, which meant "is this a review" had two
// answers depending on which one you asked.
func TestNonMarkerCommentIsNeitherRequestNorReview(t *testing.T) {
	for _, body := range []string{"", "ordinary comment", "candidate_tree=" + treeC1 + " looks fine"} {
		if _, err := reviewartifact.Parse(body); err == nil {
			t.Errorf("ordinary comment parsed as a review: %q", body)
		}
		if _, ok := ParseRequest(body); ok {
			t.Errorf("ordinary comment parsed as a request: %q", body)
		}
	}
}

// THE central rule, field by field: an answer must match every identity field of
// the obligation it claims to answer.
//
// Asserted where the rule now lives. Before R6 it was Review.Answers comparing a
// legacy-parsed reply to a Request; the review a candidate is owed is the
// OBLIGATION's, and boundMismatch is what decides whether a canonical artifact
// is the one it asked for.
func TestABoundAnswerRequiresEveryIdentityField(t *testing.T) {
	owed := ReviewObligation{
		TaskID: "T-1", RequestID: "r-1", BaseSHA: baseSHA, CandidateDigest: digestC1,
		CandidateTree: treeC1, ReviewCommit: commitC1, ReviewerProvider: "chatgpt",
	}
	answer := func(mut func(*reviewartifact.Artifact)) reviewartifact.Artifact {
		a := reviewartifact.Artifact{
			ReviewerProvider: "chatgpt", TaskID: "T-1", RequestID: "r-1", BaseSHA: baseSHA,
			CandidateDigest: digestC1, CandidateTree: treeC1, ReviewCommit: commitC1, Body: reviewerJSON,
		}
		mut(&a)
		return a
	}
	if m := boundMismatch(owed, answer(func(*reviewartifact.Artifact) {})); m != "" {
		t.Fatalf("a matching answer did not answer its obligation: %s", m)
	}
	for _, tc := range []struct {
		name string
		mut  func(*reviewartifact.Artifact)
	}{
		{"wrong candidate_digest", func(a *reviewartifact.Artifact) { a.CandidateDigest = digestC2 }},
		{"wrong candidate_tree", func(a *reviewartifact.Artifact) { a.CandidateTree = treeC2 }},
		{"wrong review_commit", func(a *reviewartifact.Artifact) { a.ReviewCommit = commitC2 }},
		{"wrong base", func(a *reviewartifact.Artifact) { a.BaseSHA = commitC2 }},
		{"wrong task", func(a *reviewartifact.Artifact) { a.TaskID = "T-2" }},
		{"different request", func(a *reviewartifact.Artifact) { a.RequestID = "r-2" }},
		{"a provider nobody asked", func(a *reviewartifact.Artifact) { a.ReviewerProvider = "claude" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if m := boundMismatch(owed, answer(tc.mut)); m == "" {
				t.Fatalf("%s still answered the obligation — a stale review could qualify a repair", tc.name)
			}
		})
	}
}

// The C1 -> C2 case, concretely: a review of C1 sits on the mailbox when the
// request for C2 goes out.
func TestAReviewOfC1DoesNotAnswerTheObligationForC2(t *testing.T) {
	c2 := ReviewObligation{
		TaskID: "T-1", RequestID: "r-2", BaseSHA: baseSHA, CandidateDigest: digestC2,
		CandidateTree: treeC2, ReviewCommit: commitC2, ReviewerProvider: "chatgpt",
	}
	stale := reviewartifact.Artifact{ReviewerProvider: "chatgpt", TaskID: "T-1", RequestID: "r-1",
		BaseSHA: baseSHA, CandidateDigest: digestC1, CandidateTree: treeC1, ReviewCommit: commitC1,
		Body: reviewerJSON}
	if boundMismatch(c2, stale) == "" {
		t.Fatal("the review of C1 answered the obligation for C2")
	}
	answer := stale
	answer.RequestID, answer.CandidateDigest = "r-2", digestC2
	answer.CandidateTree, answer.ReviewCommit = treeC2, commitC2
	if m := boundMismatch(c2, answer); m != "" {
		t.Fatalf("the genuine answer was not recognised: %s", m)
	}
}

func TestFromBindingLiftsTheWorkflowIdentity(t *testing.T) {
	b := roles.Binding{TaskID: "T-1", BaseSHA: baseSHA, CandidateDigest: digestC1, CandidateTree: treeC1}
	s := FromBinding(b)
	if s.TaskID != b.TaskID || s.BaseSHA != b.BaseSHA ||
		s.CandidateDigest != b.CandidateDigest || s.CandidateTree != b.CandidateTree {
		t.Errorf("binding not lifted faithfully: %+v", s)
	}
	if s.ReviewCommit != "" {
		t.Error("ReviewCommit must be filled by publication, not by the binding")
	}
}

func TestIssueMailboxNeedsANumberAndAReviewer(t *testing.T) {
	if (Issue{Dir: "/tmp"}).Valid() {
		t.Error("an issue with no number should not be addressable")
	}
	// A number alone is no longer enough: a mailbox that cannot authenticate a
	// sender would read any parseable comment as an answer.
	if (Issue{Dir: "/tmp", Number: "7"}).Valid() {
		t.Error("a mailbox with no expected reviewer reported itself usable")
	}
	if !(Issue{Dir: "/tmp", Number: "7", ExpectedReviewer: Principal{UserID: 42}}).Valid() {
		t.Error("a fully configured mailbox should be addressable")
	}
	got := (Issue{Number: "7"}).args("issue", "comment")
	if len(got) != 3 || got[2] != "7" {
		t.Errorf("issue number not passed through: %v", got)
	}
}

// A review spans two repositories and the request must name both.
//
// The conversation lives in the mailbox; base, candidate tree and review
// snapshot are objects in the workspace. They are equal only while the
// workspace happens to BE the mailbox repository, which is true for every
// architecture turn and false for the first cross-repository review.
//
// On 2026-09-12 request r-3212791306b4607c named base f62e3379 and review_commit
// 602e49ae, both in globulario/sensei, to a consumer reading pinned evidence
// from globulario/sensei-code where neither exists. It answered nothing, which
// from outside is indistinguishable from an absent reviewer.
func TestARequestNamesBothRepositories(t *testing.T) {
	r := Request{
		Subject: Subject{
			TaskID: "T-1", BaseSHA: baseSHA,
			CandidateDigest: digestC1, CandidateTree: treeC1, ReviewCommit: baseSHA,
		},
		RequestID:           "r-1",
		Kind:                KindReview,
		ReviewerProvider:    "chatgpt",
		MailboxRepository:   "globulario/sensei-code",
		WorkspaceRepository: "globulario/sensei",
	}
	m, err := r.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"mailbox_repository=globulario/sensei-code",
		"workspace_repository=globulario/sensei",
	} {
		if !strings.Contains(m, want) {
			t.Fatalf("the request does not state %q; a consumer must infer where the "+
				"bound evidence lives, and inference is what left r-3212791306b4607c unanswered\n%s", want, m)
		}
	}

	back, ok := ParseRequest(m)
	if !ok {
		t.Fatal("the request did not survive a round trip")
	}
	if back.WorkspaceRepository != "globulario/sensei" || back.MailboxRepository != "globulario/sensei-code" {
		t.Fatalf("routing did not survive parsing: mailbox=%q workspace=%q",
			back.MailboxRepository, back.WorkspaceRepository)
	}
}

// Routing is NOT part of Subject. Subject is what a response echoes back; a
// consumer needs to know where to look and does not re-assert where it looked.
// Widening Subject would fail Same() for every response that does not repeat
// them, and would make one routing fact two copies that can drift.
func TestRepositoryRoutingIsNotPartOfTheAnsweredSubject(t *testing.T) {
	subject := Subject{
		TaskID: "T-1", BaseSHA: baseSHA,
		CandidateDigest: digestC1, CandidateTree: treeC1, ReviewCommit: baseSHA,
	}
	req := Request{
		Subject: subject, RequestID: "r-1", Kind: KindReview, ReviewerProvider: "chatgpt",
		MailboxRepository: "globulario/sensei-code", WorkspaceRepository: "globulario/sensei",
	}
	if err := req.Validate(); err != nil {
		t.Fatalf("the routed request is not valid: %v", err)
	}
	// An obligation carrying the same routing, answered by a response that
	// repeats only the SUBJECT, must still be answered.
	owed := ReviewObligation{
		TaskID: subject.TaskID, RequestID: "r-1", BaseSHA: subject.BaseSHA,
		CandidateDigest: subject.CandidateDigest, CandidateTree: subject.CandidateTree,
		ReviewCommit: subject.ReviewCommit, ReviewerProvider: "chatgpt",
		MailboxRepository: "globulario/sensei-code", WorkspaceRepository: "globulario/sensei",
	}
	answer := reviewartifact.Artifact{
		ReviewerProvider: "chatgpt", TaskID: subject.TaskID, RequestID: "r-1",
		BaseSHA: subject.BaseSHA, CandidateDigest: subject.CandidateDigest,
		CandidateTree: subject.CandidateTree, ReviewCommit: subject.ReviewCommit, Body: "{}",
	}
	if m := boundMismatch(owed, answer); m != "" {
		t.Fatalf("a response that echoes the subject no longer answers the obligation (%s); "+
			"repository routing leaked into the answered identity", m)
	}
}

// A bridge that cannot name a repository emits the previous marker rather than
// asserting an empty identity. Empty is honest: the consumer then fails closed
// on a missing binding instead of being sent somewhere by a guess.
func TestAnUnknownRepositoryIsOmittedNotAsserted(t *testing.T) {
	r := Request{
		Subject: Subject{
			TaskID: "T-1", BaseSHA: baseSHA,
			CandidateDigest: digestC1, CandidateTree: treeC1, ReviewCommit: baseSHA,
		},
		RequestID: "r-1", Kind: KindReview, ReviewerProvider: "chatgpt",
	}
	m, err := r.Marker()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m, "workspace_repository=") || strings.Contains(m, "mailbox_repository=") {
		t.Fatalf("an unknown repository was asserted as empty:\n%s", m)
	}
}

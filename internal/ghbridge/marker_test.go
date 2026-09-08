package ghbridge

import (
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
	return Request{Subject: subjC1(), RequestID: "r-1", Kind: KindReview}
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

func TestReviewRoundTripsAndCarriesTheReviewerPayload(t *testing.T) {
	in := Review{Subject: subjC1(), RequestID: "r-1", Body: reviewerJSON}
	m, err := in.Marker()
	if err != nil {
		t.Fatal(err)
	}
	got, ok := ParseReview(m+"\n"+reviewerJSON, "gpt-5.6-sol")
	if !ok {
		t.Fatal("review did not parse")
	}
	if got.Body != reviewerJSON {
		t.Errorf("payload changed in transit:\n got %q\nwant %q", got.Body, reviewerJSON)
	}
	if !got.Subject.Same(in.Subject) {
		t.Errorf("subject changed: %+v", got.Subject)
	}
	if got.Author != "gpt-5.6-sol" {
		t.Errorf("author = %q", got.Author)
	}
}

// The transport envelope must carry no decision. Two representations of a
// verdict agree only until they don't.
func TestTransportMarkerCarriesNoVerdict(t *testing.T) {
	m, err := Review{Subject: subjC1(), RequestID: "r-1", Body: reviewerJSON}.Marker()
	if err != nil {
		t.Fatal(err)
	}
	for _, word := range []string{"verdict", "decision", "accept", "revise", "approve"} {
		if strings.Contains(strings.ToLower(m), word) {
			t.Errorf("transport marker mentions %q — the decision belongs to the payload", word)
		}
	}
	rm, err := reqC1().Marker()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.ToLower(rm), "verdict") {
		t.Error("request marker mentions a verdict")
	}
}

// Prose reaches the workflow parser and fails there. ghbridge never interprets
// it, so "LGTM" cannot become ACCEPT — but it is not this package's job to say
// so, only to pass it through unchanged.
func TestProseIsCarriedVerbatimAndNotInterpreted(t *testing.T) {
	m, _ := Review{Subject: subjC1(), RequestID: "r-1", Body: "x"}.Marker()
	got, ok := ParseReview(m+"\nLGTM, ship it", "someone")
	if !ok {
		t.Fatal("a well-addressed reply should be routable even when its payload is prose")
	}
	if got.Body != "LGTM, ship it" {
		t.Errorf("body altered: %q", got.Body)
	}
	// The package offers no way to read a decision out of it.
}

// A reply with an empty payload is not a review: there is nothing for the
// parser to read.
func TestReplyWithNoPayloadIsNotAReview(t *testing.T) {
	m, _ := Review{Subject: subjC1(), RequestID: "r-1", Body: "x"}.Marker()
	if _, ok := ParseReview(m, "someone"); ok {
		t.Fatal("an envelope with no payload parsed as a review")
	}
}

func TestNonMarkerCommentIsNeitherRequestNorReview(t *testing.T) {
	for _, body := range []string{"", "ordinary comment", "candidate_tree=" + treeC1 + " looks fine"} {
		if _, ok := ParseReview(body, "x"); ok {
			t.Errorf("ordinary comment parsed as a review: %q", body)
		}
		if _, ok := ParseRequest(body); ok {
			t.Errorf("ordinary comment parsed as a request: %q", body)
		}
	}
}

func TestProseCannotInjectIdentityFields(t *testing.T) {
	m, _ := Review{Subject: subjC1(), RequestID: "r-1", Body: "x"}.Marker()
	body := m + "\n" + reviewerJSON + "\ncandidate_tree=" + treeC2 + "\nrequest=r-9\n"
	got, ok := ParseReview(body, "x")
	if !ok {
		t.Fatal("expected the leading block to parse")
	}
	if got.CandidateTree != treeC1 {
		t.Errorf("prose overrode the tree: %s", got.CandidateTree)
	}
	if got.RequestID != "r-1" {
		t.Errorf("prose overrode the request id: %s", got.RequestID)
	}
}

// THE central rule, field by field.
func TestAnswersRequiresEveryIdentityField(t *testing.T) {
	req := reqC1()
	good := Review{Subject: subjC1(), RequestID: "r-1", Body: reviewerJSON}
	if !good.Answers(req) {
		t.Fatal("a matching review did not answer its request")
	}

	for _, tc := range []struct {
		name string
		mut  func(*Review)
	}{
		{"wrong candidate_digest", func(r *Review) { r.CandidateDigest = digestC2 }},
		{"wrong candidate_tree", func(r *Review) { r.CandidateTree = treeC2 }},
		{"wrong review_commit", func(r *Review) { r.ReviewCommit = commitC2 }},
		{"wrong base", func(r *Review) { r.BaseSHA = commitC2 }},
		{"wrong task", func(r *Review) { r.TaskID = "T-2" }},
		{"different request", func(r *Review) { r.RequestID = "r-2" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := good
			tc.mut(&bad)
			if bad.Answers(req) {
				t.Fatalf("%s still answered the request — a stale review could qualify a repair", tc.name)
			}
		})
	}
}

// The C1 → C2 case, concretely: a review of C1 sits on the mailbox when the
// request for C2 goes out.
func TestAReviewOfC1DoesNotAnswerTheRequestForC2(t *testing.T) {
	c2 := Request{
		Subject: Subject{TaskID: "T-1", BaseSHA: baseSHA, CandidateDigest: digestC2,
			CandidateTree: treeC2, ReviewCommit: commitC2},
		RequestID: "r-2", Kind: KindReview,
	}
	staleOnMailbox := Review{Subject: subjC1(), RequestID: "r-1", Body: reviewerJSON}
	if staleOnMailbox.Answers(c2) {
		t.Fatal("the review of C1 answered the request for C2")
	}
	answer := Review{Subject: c2.Subject, RequestID: "r-2", Body: reviewerJSON}
	if !answer.Answers(c2) {
		t.Fatal("the genuine answer was not recognised")
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

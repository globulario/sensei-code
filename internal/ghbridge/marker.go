// Package ghbridge carries reviewer turns between Sensei Code, running on
// Dave's machine, and a reviewer that can read and write GitHub but cannot
// reach this machine.
//
// GitHub is a MAILBOX. It executes nothing: no Actions, no hosted worker, no
// remote Claude. Sensei Code still orchestrates, Claude still implements
// locally, and the graph still governs.
//
// Two separations are load-bearing here, and both were got wrong once.
//
// First: candidate content identity is NOT a Git commit. The subject of a
// review is the workflow binding — task, base sha, candidate digest, candidate
// tree — which the engine already owns. The commit this package pushes is a
// PROJECTION of that binding so a remote party can read it through GitHub, and
// nothing more. It is not admission, acceptance, merge authority, or candidate
// minting, and it never becomes the identity.
//
// Second: the transport envelope is NOT the review result. The marker says
// WHICH answer this is; the body says WHAT it says, in the reviewer JSON
// contract the workflow parser already owns. A verdict field in the marker
// would be a second representation of a decision that agrees with the first
// only until it doesn't.
//
// GitHub is transport, not authority. A comment author, a GitHub login and a
// commit author are all things a party with write access can produce, so none
// of them may raise a review above advisory. Turns answered here are stamped
// roles.Unverified by the orchestrator.
package ghbridge

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/globulario/sensei-code/internal/roles"
)

// Kind names what a request asks for.
//
// Only review exists. Architecture is deliberately absent: an architect turn
// happens BEFORE a candidate exists, so it has no candidate digest or tree to
// bind to, and pretending this protocol fits it would mean inventing a subject.
type Kind string

// KindReview is the only kind this bridge serves.
const KindReview Kind = "review"

// Valid reads the closed set by membership.
func (k Kind) Valid() bool { return k == KindReview }

const (
	requestMarker = "[sensei-code:review-request]"
	reviewMarker  = "[sensei-code:review]"
)

var (
	fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// A candidate digest is the workflow's own content identity. It is not
	// required to be a git object id, so it is checked for presence and shape
	// rather than length.
	digestShape = regexp.MustCompile(`^[0-9a-zA-Z:_.-]{8,128}$`)
)

// Subject is the exact artifact a turn is about.
//
// Every field is required in a request. The workflow binding supplies task,
// base, digest and tree; ReviewCommit is the GitHub-visible projection this
// package published so the reviewer can read that exact tree.
type Subject struct {
	TaskID          string
	BaseSHA         string
	CandidateDigest string
	CandidateTree   string
	ReviewCommit    string
}

// FromBinding lifts the workflow's binding, leaving ReviewCommit to be filled
// by whoever publishes the snapshot.
func FromBinding(b roles.Binding) Subject {
	return Subject{
		TaskID:          b.TaskID,
		BaseSHA:         b.BaseSHA,
		CandidateDigest: b.CandidateDigest,
		CandidateTree:   b.CandidateTree,
	}
}

// Validate states every identity rule the protocol depends on.
func (s Subject) Validate() error {
	if strings.TrimSpace(s.TaskID) == "" {
		return errors.New("a review subject must name its task")
	}
	if !fullSHA.MatchString(s.BaseSHA) {
		return fmt.Errorf("base must be a full 40-character commit, got %q", s.BaseSHA)
	}
	if !digestShape.MatchString(strings.TrimSpace(s.CandidateDigest)) {
		return fmt.Errorf("candidate_digest is missing or malformed: %q", s.CandidateDigest)
	}
	if !fullSHA.MatchString(s.CandidateTree) {
		return fmt.Errorf("candidate_tree must be a full 40-character tree id, got %q", s.CandidateTree)
	}
	if !fullSHA.MatchString(s.ReviewCommit) {
		return fmt.Errorf("review_commit must be a full 40-character commit, got %q", s.ReviewCommit)
	}
	return nil
}

// Same reports whether two subjects are the same artifact.
//
// Every identity field participates. A convenient proxy — request id alone, or
// the review commit alone — would let a reply about one artifact answer a
// question about another, which is precisely how a review of C qualifies C2.
func (s Subject) Same(o Subject) bool {
	return s.TaskID == o.TaskID &&
		s.BaseSHA == o.BaseSHA &&
		s.CandidateDigest == o.CandidateDigest &&
		s.CandidateTree == o.CandidateTree &&
		s.ReviewCommit == o.ReviewCommit
}

// Request is Sensei Code asking for a review of one exact artifact.
type Request struct {
	Subject
	RequestID string
	Kind      Kind
}

// Validate states the request rules on top of the subject's.
func (r Request) Validate() error {
	if err := r.Subject.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.RequestID) == "" {
		return errors.New("a review request must carry a unique request id")
	}
	if !r.Kind.Valid() {
		return fmt.Errorf("unknown request kind %q; this bridge serves review only", r.Kind)
	}
	return nil
}

// Marker renders the machine-readable block Sensei Code owns emission of.
func (r Request) Marker() (string, error) {
	if err := r.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(requestMarker + "\n")
	fmt.Fprintf(&b, "kind=%s\n", r.Kind)
	fmt.Fprintf(&b, "task=%s\n", r.TaskID)
	fmt.Fprintf(&b, "request=%s\n", r.RequestID)
	fmt.Fprintf(&b, "base=%s\n", r.BaseSHA)
	fmt.Fprintf(&b, "candidate_digest=%s\n", r.CandidateDigest)
	fmt.Fprintf(&b, "candidate_tree=%s\n", r.CandidateTree)
	fmt.Fprintf(&b, "review_commit=%s\n", r.ReviewCommit)
	return b.String(), nil
}

// Review is a reply to one request.
//
// It carries identity and a body, and deliberately no verdict: what the answer
// SAYS is the workflow parser's to read from Body, in the reviewer JSON
// contract. A transport that also stated the decision would be a second
// representation of it.
type Review struct {
	Subject
	RequestID string
	// Body is the reviewer payload, passed through verbatim. This package never
	// interprets it: prose that does not satisfy the reviewer JSON contract
	// fails at the parser, which is why "LGTM, ship it" cannot become ACCEPT.
	Body string
	// Author and AuthorID record the GitHub account the answer came from.
	//
	// They are set by the transport AFTER it has authenticated the comment
	// against the mailbox's configured reviewer — they are the result of that
	// check, never the basis for it, and a parser cannot populate them from the
	// comment body. They establish WHO sent an advisory result and nothing
	// more: no GitHub property raises SessionMode above roles.Unverified.
	Author   string
	AuthorID int64
}

// Validate states the rules for a reply to be routable at all.
func (r Review) Validate() error {
	if err := r.Subject.Validate(); err != nil {
		return err
	}
	if strings.TrimSpace(r.RequestID) == "" {
		return errors.New("a review must name the request it answers")
	}
	if strings.TrimSpace(r.Body) == "" {
		return errors.New("a review must carry a reviewer payload")
	}
	return nil
}

// Marker renders a review envelope, for tests and for tooling that produces one
// locally. The remote party writes its own.
func (r Review) Marker() (string, error) {
	if err := r.Subject.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(reviewMarker + "\n")
	fmt.Fprintf(&b, "task=%s\n", r.TaskID)
	fmt.Fprintf(&b, "request=%s\n", r.RequestID)
	fmt.Fprintf(&b, "base=%s\n", r.BaseSHA)
	fmt.Fprintf(&b, "candidate_digest=%s\n", r.CandidateDigest)
	fmt.Fprintf(&b, "candidate_tree=%s\n", r.CandidateTree)
	fmt.Fprintf(&b, "review_commit=%s\n", r.ReviewCommit)
	return b.String(), nil
}

var fieldLine = regexp.MustCompile(`^\s*([a-z_]+)\s*=\s*(\S+)\s*$`)

// fields reads key=value lines following a marker, stopping at the first line
// that is not one. Everything after is the body. Prose therefore cannot inject
// an identity field by containing "candidate_tree=..." further down.
func fields(body, marker string) (map[string]string, string, bool) {
	idx := strings.Index(body, marker)
	if idx < 0 {
		return nil, "", false
	}
	lines := strings.Split(body[idx+len(marker):], "\n")
	out := map[string]string{}
	consumed := 0
	for i, ln := range lines {
		if i == 0 && strings.TrimSpace(ln) == "" {
			consumed = i + 1
			continue
		}
		m := fieldLine.FindStringSubmatch(ln)
		if m == nil {
			break
		}
		out[m[1]] = m[2]
		consumed = i + 1
	}
	rest := ""
	if consumed < len(lines) {
		rest = strings.TrimSpace(strings.Join(lines[consumed:], "\n"))
	}
	return out, rest, true
}

func subjectFrom(f map[string]string) Subject {
	return Subject{
		TaskID:          f["task"],
		BaseSHA:         f["base"],
		CandidateDigest: f["candidate_digest"],
		CandidateTree:   f["candidate_tree"],
		ReviewCommit:    f["review_commit"],
	}
}

// ParseRequest reads a review request from a comment body.
func ParseRequest(body string) (Request, bool) {
	f, _, ok := fields(body, requestMarker)
	if !ok {
		return Request{}, false
	}
	r := Request{Subject: subjectFrom(f), RequestID: f["request"], Kind: Kind(f["kind"])}
	if r.Validate() != nil {
		return Request{}, false
	}
	return r, true
}

// ParseReview reads a review from a comment body.
//
// An incomplete envelope yields false rather than a partly-populated review: a
// reply that does not state exactly what it reviewed is not a review of
// anything, and guessing the missing half is how the wrong artifact gets
// qualified.
func ParseReview(body, author string) (Review, bool) {
	f, rest, ok := fields(body, reviewMarker)
	if !ok {
		return Review{}, false
	}
	r := Review{Subject: subjectFrom(f), RequestID: f["request"], Body: rest, Author: author}
	if r.Validate() != nil {
		return Review{}, false
	}
	return r, true
}

// Answers reports whether a review replies to this exact request.
//
// Request id AND the whole subject must match.
func (r Review) Answers(req Request) bool {
	return r.RequestID == req.RequestID && r.Subject.Same(req.Subject)
}

// NewRequestID mints a unique id for one review request.
//
// Random rather than sequential: a request id is how a reply says which
// question it answers, and an id a reader could predict is an id a reader could
// answer before the question was asked.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A request that cannot be uniquely named must not be sent; the caller
		// surfaces this as a refusal rather than reusing an id.
		return ""
	}
	return "r-" + hex.EncodeToString(b[:])
}

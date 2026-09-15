// Package ghbridge carries architect and reviewer turns between Sensei Code,
// running on Dave's machine, and ChatGPT through a GitHub mailbox.
//
// GitHub is a MAILBOX. It executes nothing: no Actions, no hosted worker, no
// remote Claude. Sensei Code still orchestrates and Claude still implements
// locally.
//
// The identity on each side of implementation is different, and deliberately
// so. A review is about candidate content: task, base sha, candidate digest and
// candidate tree. The commit this package pushes is only a PROJECTION of that
// binding so a remote reviewer can read the exact tree. Architecture happens
// before candidate content exists, so its separate envelope is bound instead to
// task, exact objective digest, candidate base and graph build commit. Neither
// protocol invents the other's subject merely to reuse an envelope.
//
// The transport envelope is also NOT the role result. A marker says WHICH
// question an answer belongs to; the body says WHAT the architect or reviewer
// answered, in the JSON contract the workflow parser already owns. Putting a
// decision or verdict in the marker would create a second representation that
// agrees with the body only until it doesn't.
//
// GitHub is transport, not authority. A comment author, a GitHub login and a
// commit author are all transport facts. None establishes Dave's objective
// authorization, model identity, or reviewer independence. Turns answered here
// are stamped roles.Unverified by the orchestrator.
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

// Kind names what a candidate-review request asks for.
//
// Architecture deliberately uses its own pre-candidate envelope in
// architecture.go, because it has an objective/world subject rather than a
// candidate digest/tree subject. Kind therefore remains the closed vocabulary
// of the review protocol instead of pretending both protocols share identity.
type Kind string

// KindReview is the only kind in the candidate-review envelope.
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
	// MailboxRepository is where the conversation lives, "owner/name".
	// WorkspaceRepository is where the governed evidence lives.
	//
	// Two identities because a governed exchange spans two repositories: the
	// request and its wake are published to the mailbox, while base, candidate
	// tree and review snapshot are objects in the workspace. They are equal only
	// while the workspace happens to BE the mailbox repository.
	//
	// ROUTING, deliberately not part of Subject. Subject is the identity a
	// response echoes back; a consumer needs to know where to look, it does not
	// re-assert where it looked. Putting these in Subject would make every
	// existing response fail Same() and turn one routing fact into two copies
	// that can drift.
	//
	// Stated, never inferred. On 2026-09-12 request r-3212791306b4607c named base
	// f62e3379 and review_commit 602e49ae -- both objects in globulario/sensei --
	// to a consumer whose instructions said to read pinned evidence from
	// globulario/sensei-code, where neither exists. It woke, resolved the wake,
	// resolved the request, authenticated the author, and then could not fetch
	// evidence it had been told to seek in the wrong repository. It answered
	// nothing, which from outside is indistinguishable from an absent reviewer.
	//
	// An architecture turn never leaves the mailbox, so a consumer built around
	// one acquires a fixed repository assumption that stays invisible until the
	// first cross-repository review. The law must not depend on that accident,
	// which is why these live on the shared request rather than on review alone.
	MailboxRepository   string
	WorkspaceRepository string
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
	// Emitted only when known: a bridge that cannot name a repository must
	// produce the previous marker rather than one asserting an empty identity.
	if strings.TrimSpace(r.MailboxRepository) != "" {
		fmt.Fprintf(&b, "mailbox_repository=%s\n", r.MailboxRepository)
	}
	if strings.TrimSpace(r.WorkspaceRepository) != "" {
		fmt.Fprintf(&b, "workspace_repository=%s\n", r.WorkspaceRepository)
	}
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
	r := Request{
		Subject:             subjectFrom(f),
		RequestID:           f["request"],
		Kind:                Kind(f["kind"]),
		MailboxRepository:   f["mailbox_repository"],
		WorkspaceRepository: f["workspace_repository"],
	}
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

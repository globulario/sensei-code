// Package reviewartifact owns the one wire contract for what a REVIEWER
// PRODUCED, independently of how it reached this workspace.
//
// A review's meaning and authority come from the exact candidate, reviewer and
// evidence it binds. Transport may prove or fail to prove facts about that
// evidence; transport must never change what the review means. Two grammars for
// one semantic artifact is how that law gets broken by accident: a mailbox
// parser and a relay parser drift, and the same reviewer bytes then mean
// different things depending on which pipe carried them.
//
// So this package owns exactly one marker, one parser, one validator, one
// renderer, one digest and one size bound. Delivery adapters depend on it. It
// depends on no adapter: importing ghbridge here would put transport beneath
// artifact semantics, which is the inversion the consolidation exists to undo.
//
// The artifact carries NO verdict. What a review SAYS lives only in the
// reviewer payload, in the JSON contract the workflow parser owns. Stating the
// decision in envelope metadata as well would create a second representation
// that agrees with the body only until it doesn't.
package reviewartifact

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	// Marker opens the one review envelope. The reviewer writes it.
	Marker = "[sensei-code:review]"

	// protocolPrefix opens every Sensei-Code protocol envelope. An artifact may
	// contain exactly one, so that one artifact cannot claim two identities.
	protocolPrefix = "[sensei-code:"

	// MaxBytes bounds one semantic review artifact. It is a review, not a file.
	//
	// This is the artifact bound and nothing else. It is unrelated to the
	// session-event record limit, which bounds a different object for a
	// different reason; a shared number is not a shared policy.
	MaxBytes = 64 << 10
)

var (
	fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)
	// A candidate digest is the workflow's own content identity. It is not
	// required to be a git object id, so it is checked for presence and shape
	// rather than length.
	digestShape = regexp.MustCompile(`^[0-9a-zA-Z:_.-]{8,128}$`)
	// The reviewer vocabulary shape. Claimed by the artifact, never verified by
	// it: naming a provider is not being that provider.
	providerShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

	fieldLine = regexp.MustCompile(`^\s*([a-z_]+)\s*=\s*(\S+)\s*$`)
)

// Artifact is one review exactly as its reviewer produced it.
//
// The semantic fields are what the artifact MEANS. Raw and Digest are what it
// IS: Raw is the reviewer's exact byte sequence and Digest names those exact
// bytes. Parsing never repairs, fills, rewrites, normalizes or infers a
// semantic field, so Raw is always the artifact the reviewer authored rather
// than one this process made parseable.
type Artifact struct {
	// ReviewerProvider is the reviewer the artifact names, on the wire as
	// reviewer=<provider>. Claimed, not verified: no property of this artifact
	// establishes reviewer independence.
	ReviewerProvider string

	// The exact binding this review answers. Every field participates in
	// identity; a convenient proxy would let a review of one candidate qualify
	// another.
	TaskID          string
	RequestID       string
	BaseSHA         string
	CandidateDigest string
	CandidateTree   string
	// ReviewCommit is PROJECTION evidence: it proves which snapshot the reviewer
	// could read. It is exact, and it does not substitute for candidate identity.
	ReviewCommit string

	// Body is the reviewer payload, uninterpreted. This package never reads it:
	// prose that does not satisfy the reviewer JSON contract fails later, at the
	// workflow parser, which is why "LGTM, ship it" cannot become ACCEPT here.
	//
	// It is the NORMALIZED semantic payload, not the exact bytes: the surrounding
	// whitespace between the envelope and the payload is not part of what the
	// reviewer said. The exact bytes are Raw, and Digest names those -- so a
	// change anywhere in the artifact, including in whitespace Body drops, still
	// changes the artifact's identity.
	Body string

	// Raw is the artifact byte for byte, and Digest names those bytes.
	Raw    string
	Digest string
}

// Digest names an exact byte sequence.
//
// It digests the bytes it is given and nothing derived from them: a digest over
// a normalized or re-rendered form would name an artifact nobody authored, and
// a one-byte edit on the way through would stop being visible.
func Digest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Validate states every rule an artifact must satisfy to bind anything.
func (a Artifact) Validate() error {
	if !providerShape.MatchString(a.ReviewerProvider) {
		return fmt.Errorf("the artifact must name its reviewer on a reviewer=<provider> line, got %q", a.ReviewerProvider)
	}
	if strings.TrimSpace(a.TaskID) == "" {
		return errors.New("a review artifact must name its task")
	}
	if strings.TrimSpace(a.RequestID) == "" {
		return errors.New("a review artifact must name the request it answers")
	}
	if !fullSHA.MatchString(a.BaseSHA) {
		return fmt.Errorf("base must be a full 40-character commit, got %q", a.BaseSHA)
	}
	if !digestShape.MatchString(strings.TrimSpace(a.CandidateDigest)) {
		return fmt.Errorf("candidate_digest is missing or malformed: %q", a.CandidateDigest)
	}
	if !fullSHA.MatchString(a.CandidateTree) {
		return fmt.Errorf("candidate_tree must be a full 40-character tree id, got %q", a.CandidateTree)
	}
	if !fullSHA.MatchString(a.ReviewCommit) {
		return fmt.Errorf("review_commit must be a full 40-character commit, got %q", a.ReviewCommit)
	}
	if strings.TrimSpace(a.Body) == "" {
		return errors.New("a review artifact must carry a reviewer payload")
	}
	return nil
}

// Parse reads a complete review artifact, or refuses it whole.
//
// Nothing is filled in, trimmed into shape or repaired. An artifact that needed
// editing to parse is not the review the reviewer produced, so a partial
// envelope is a refusal rather than a partly-populated Artifact: a reply that
// does not state exactly what it reviewed is not a review of anything, and
// guessing the missing half is how the wrong candidate gets qualified.
func Parse(raw string) (Artifact, error) {
	if len(raw) > MaxBytes {
		return Artifact{}, fmt.Errorf("the artifact is %d bytes; a review artifact is bounded at %d", len(raw), MaxBytes)
	}
	if !strings.HasPrefix(strings.TrimLeft(raw, " \t\r\n"), Marker) {
		return Artifact{}, errors.New("the artifact must begin with the " + Marker + " envelope the reviewer produced")
	}
	// Exactly one envelope and no other protocol marker anywhere: a second
	// envelope, a request or a relay record inside the payload would make one
	// artifact say two things about which question it answers.
	if strings.Count(raw, protocolPrefix) != 1 {
		return Artifact{}, errors.New("the artifact must carry exactly one sensei-code envelope and no other protocol marker")
	}
	f, body, ok := fields(raw)
	if !ok {
		return Artifact{}, errors.New("the artifact must begin with the " + Marker + " envelope the reviewer produced")
	}
	a := Artifact{
		ReviewerProvider: f["reviewer"],
		TaskID:           f["task"],
		RequestID:        f["request"],
		BaseSHA:          f["base"],
		CandidateDigest:  f["candidate_digest"],
		CandidateTree:    f["candidate_tree"],
		ReviewCommit:     f["review_commit"],
		Body:             body,
		Raw:              raw,
		Digest:           Digest(raw),
	}
	if err := a.Validate(); err != nil {
		return Artifact{}, err
	}
	return a, nil
}

// Render writes the canonical bytes for an artifact, for tests and for tooling
// that produces one locally. A reviewer writes its own.
//
// The verdict appears exactly once, inside the payload. Render emits no
// decision, verdict or standing field of its own.
//
// The bound is checked against the EXACT bytes this produces, not against the
// payload alone, and it is the same bound Parse enforces. A writer that could
// emit an artifact its own reader refuses would put the size policy in one
// place and the size behavior in two: the artifact would exist, be digested,
// be stored and be carried, and fail only at whoever read it next. That
// writer/reader asymmetry is the defect #181 recorded, and one canonical
// package holding both halves is the only thing that makes it unrepeatable.
func (a Artifact) Render() (string, error) {
	if err := a.Validate(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(Marker + "\n")
	fmt.Fprintf(&b, "task=%s\n", a.TaskID)
	fmt.Fprintf(&b, "request=%s\n", a.RequestID)
	fmt.Fprintf(&b, "base=%s\n", a.BaseSHA)
	fmt.Fprintf(&b, "candidate_digest=%s\n", a.CandidateDigest)
	fmt.Fprintf(&b, "candidate_tree=%s\n", a.CandidateTree)
	fmt.Fprintf(&b, "review_commit=%s\n", a.ReviewCommit)
	fmt.Fprintf(&b, "reviewer=%s\n", a.ReviewerProvider)
	b.WriteString(a.Body)
	out := b.String()
	if len(out) > MaxBytes {
		return "", fmt.Errorf("the artifact renders to %d bytes; a review artifact is bounded at %d", len(out), MaxBytes)
	}
	return out, nil
}

// fields reads key=value lines following the marker, stopping at the first line
// that is not one. Everything after is the body.
//
// The stop is the header/body boundary and it is load-bearing: once the
// reviewer's payload has begun, prose containing "candidate_tree=..." is prose.
// Without the stop, a reviewer could describe one candidate in the envelope and
// rewrite that identity from inside the text a human reads as commentary.
func fields(body string) (map[string]string, string, bool) {
	idx := strings.Index(body, Marker)
	if idx < 0 {
		return nil, "", false
	}
	lines := strings.Split(body[idx+len(Marker):], "\n")
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

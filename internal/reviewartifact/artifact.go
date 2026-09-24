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

// Opens reports whether body BEGINS with marker, at character zero exactly.
//
// THE ONE PLACE THE POSITIONAL FRAMING RULE IS SPELLED, for every Sensei-Code
// envelope and not only this package's. It lives here because this package sits
// beneath every transport adapter and imports none of them, so one rule can be
// shared downward without inverting that dependency.
//
// THE RULE. Protocol identity comes from what an artifact BEGINS with and from
// nothing else. A recognized envelope here selects that envelope's grammar; the
// entire remainder then belongs to that grammar and is never rescanned for
// further artifacts. Marker-shaped text at any other offset is ordinary payload
// data.
//
// MEASURED 2026-09-24. A well formed review whose bindings matched exactly, from
// the pinned reviewer principal, carrying three correct blocking findings, was
// rejected in transport because ONE finding quoted a protocol marker while
// explaining how that marker is handled. A classifier that decides identity by
// searching the whole body cannot tell a message from a message ABOUT messages,
// so the transport ate a correct review of the transport and every future review
// of this protocol would have hit the same wall.
//
// IDENTIFICATION IS NOT PARSING, and the order is load-bearing. This function
// answers only "what does this claim to be". Whether the claim is well formed is
// the caller's strict parse, and a failure there stays ATTRIBUTABLE to this kind
// rather than collapsing back into anonymous content.
//
// CHARACTER ZERO EXACTLY, WITH NOTHING SKIPPED, and that is why there is no
// offset to return. An earlier revision stepped over leading spaces, tabs and
// newlines and returned the offset it landed on, reasoning that padding is
// transport noise rather than content. It thereby handed protocol identity to an
// indented or blank-line-prefixed marker -- a marker at a NONZERO offset, read
// as an artifact by every caller, which is precisely what this rule exists to
// forbid. Whitespace was only the first prefix somebody found harmless, and a
// positional rule that admits one harmless prefix keeps no principle with which
// to refuse the next. So identification is a prefix test against the bytes as
// received: nothing is trimmed, normalized or advanced before it, and a bool is
// the entire answer.
//
// The reviewer's exact bytes are still preserved and digested whole. That is a
// consequence of a real position-zero claim, never a reason to manufacture one.
func Opens(body, marker string) bool {
	return strings.HasPrefix(body, marker)
}

// DelimitsHeader reports whether after -- the bytes FOLLOWING a marker that
// Opens has already recognized at position zero -- begins the line break that
// separates that marker from its identity header.
//
// THE SECOND HALF OF THE ENVELOPE, and it is spelled here beside the first for
// the reason this package exists: a marker and its delimiter are one envelope,
// and two readers that disagree about where the header may begin accept two
// different grammars for one artifact.
//
// IT IS NOT AN IDENTIFICATION TEST. Opens has already decided what these bytes
// claim to be; this answers only whether the claim is well formed, and a caller
// that folds the delimiter back into identification would make an envelope with
// the wrong delimiter identify as NOTHING and be handed on as ordinary content
// -- the fallback A4 forbids. So the two are separate calls, and a false here is
// a MALFORMED artifact of the kind Opens named.
//
// MEASURED. Both remaining readers sliced straight past the marker and let the
// first post-marker bytes match a key=value line. A body formed as the marker
// concatenated directly to an otherwise complete header therefore parsed as a
// fully valid artifact with no delimiter present at all, so the envelope's
// boundary was decided by whether the next bytes happened to look like a field.
//
// CRLF is accepted because it is a line-ending encoding chosen by the client
// that posts, not a prefix inserted before the header: unlike leading spaces or
// blank lines, it cannot move where the header starts. Nothing else is: a
// space, a tab or any other filler between the envelope and its header is the
// same permissiveness C1 removed from Opens, one step further in.
func DelimitsHeader(after string) bool {
	return strings.HasPrefix(after, "\n") || strings.HasPrefix(after, "\r\n")
}

// firstLineOf names what an envelope was actually followed by, bounded so a
// diagnostic quotes a delimiter rather than reprinting a reviewer's payload.
//
// Deliberately not shared with the adapter that spells the same three lines:
// this shapes a diagnostic, and two readers that format an error message
// differently still read one grammar. What must not be duplicated is the RULE,
// which is why DelimitsHeader is exported and this is not.
func firstLineOf(after string) string {
	line, _, _ := strings.Cut(after, "\n")
	if len(line) > 40 {
		line = line[:40]
	}
	return line
}

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
	if err := a.validateIdentity(); err != nil {
		return err
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
	// IDENTIFICATION, and it is the only thing that decides what these bytes
	// ARE (A7). Everything after it is this envelope's own grammar, parsed
	// strictly below; a failure there is a failure OF A REVIEW ARTIFACT and is
	// reported as one, never a demotion back to anonymous content.
	if !Opens(raw, Marker) {
		return Artifact{}, errors.New("the artifact must begin with the " + Marker + " envelope the reviewer produced")
	}
	after := raw[len(Marker):]
	// THE ENVELOPE IS THE MARKER AND ITS DELIMITER, and this is a failure OF A
	// REVIEW ARTIFACT: identification above already said what these bytes claim
	// to be, so the diagnostic names this grammar's rule rather than demoting the
	// artifact to anonymous content (A4).
	//
	// Without it the header began wherever the first key=value line happened to
	// be, so the marker concatenated directly to an otherwise complete header
	// parsed as a valid review of the candidate it named.
	if !DelimitsHeader(after) {
		return Artifact{}, fmt.Errorf(
			"the %s envelope must be followed by a newline before the reviewer's identity header, and this one is followed by %q",
			Marker, firstLineOf(after))
	}
	f, body := fields(after)
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
	out := a.envelope() + a.Body
	if len(out) > MaxBytes {
		return "", fmt.Errorf("the artifact renders to %d bytes; a review artifact is bounded at %d", len(out), MaxBytes)
	}
	return out, nil
}

// envelope renders the canonical identity header: the marker and the seven
// identity lines, in the one order this package defines.
//
// THE ONLY PLACE A CANONICAL REVIEW ENVELOPE IS SPELLED. Render writes one and
// ResponseContract teaches one, and both come from here, so a change to a key,
// an order or the identity set cannot leave the grammar we accept disagreeing
// with the grammar we ask for (#182 live protocol closure).
func (a Artifact) envelope() string {
	var b strings.Builder
	b.WriteString(Marker + "\n")
	fmt.Fprintf(&b, "task=%s\n", a.TaskID)
	fmt.Fprintf(&b, "request=%s\n", a.RequestID)
	fmt.Fprintf(&b, "base=%s\n", a.BaseSHA)
	fmt.Fprintf(&b, "candidate_digest=%s\n", a.CandidateDigest)
	fmt.Fprintf(&b, "candidate_tree=%s\n", a.CandidateTree)
	fmt.Fprintf(&b, "review_commit=%s\n", a.ReviewCommit)
	fmt.Fprintf(&b, "reviewer=%s\n", a.ReviewerProvider)
	return b.String()
}

// PayloadPlaceholder marks where the reviewer's JSON goes in a taught contract.
//
// Exported so a consumer can find the slot without re-spelling it, and so a
// test can substitute a real payload and prove the result parses.
const PayloadPlaceholder = "<your review verdict, as the JSON object described above>"

// ResponseContract states the exact reply this package will accept, in terms
// the party that must produce it can follow.
//
// THE REQUEST CARRIES THE GRAMMAR. A protocol is incomplete if its parser
// requires fields its producer-facing contract never mentions: R2 made
// reviewer=<provider> mandatory on the answer, and for six weeks nothing told
// the answering party, so every real reply arrived in the pre-R2 grammar and
// was correctly classified as evidence that establishes nothing.
//
// The envelope below is rendered by the same code Render uses, with the
// request's own identity values already filled in. A reviewer copying it
// verbatim produces bytes Parse accepts; a reviewer reconstructing it from
// memory, configuration or repository state does not, and is told so.
//
// Identity only: the reply's payload is the reviewer's to write, so the body is
// not validated here.
func ResponseContract(want Artifact) (string, error) {
	if err := want.validateIdentity(); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString("Your GitHub reply must be exactly one canonical review artifact: this envelope, " +
		"then your JSON payload. Post it as a single comment with no prose before the envelope, " +
		"no second [sensei-code: envelope opening it, and the verdict stated only inside the JSON.\n\n")
	// The reviewer is TOLD the framing rule, because a rule a producer does not
	// know is a rule that silently discards correct work: a verdict quoting a
	// marker was rejected in transport on 2026-09-24, and until the reader was
	// repaired the only advice available was "do not write the marker down".
	b.WriteString("Quote protocol markers freely INSIDE your payload. Identity comes from what this " +
		"artifact begins with, so marker-shaped text anywhere after the envelope is payload and is " +
		"read as payload: a finding that reproduces a marker while explaining it is still one review.\n\n")
	b.WriteString("Copy every identity value below exactly as written. Do not derive any of them from " +
		"your GitHub login, this repository's current state, a pull request head, another comment, or " +
		"a standing instruction: they are this request's, and an answer that names different values " +
		"is a review of something else.\n\n")
	b.WriteString(want.envelope())
	b.WriteString(PayloadPlaceholder + "\n")
	return b.String(), nil
}

// validateIdentity states the rules an artifact's IDENTITY must satisfy,
// independently of whether a payload exists yet.
//
// Split out because a response contract is an identity with the payload still
// to be written: validating a body that by definition is not there would have
// forced the contract to invent one.
func (a Artifact) validateIdentity() error {
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
	return nil
}

// fields reads key=value lines from the bytes FOLLOWING an already-identified
// envelope, stopping at the first line that is not one. Everything after is the
// body.
//
// It takes the remainder rather than the whole artifact because it must not
// search. A reader that found its own marker with strings.Index would identify
// an artifact from a marker sitting anywhere in the reviewer's payload, which is
// the whole-body search A5 forbids; the caller has already established position
// zero and hands over exactly what that envelope owns.
//
// The delimiter is already established too, which is what the blank first line
// below consumes: the caller checked it, so a header line can never be read out
// of bytes that sat on the envelope's own line.
//
// The stop is the header/body boundary and it is load-bearing: once the
// reviewer's payload has begun, prose containing "candidate_tree=..." is prose.
// Without the stop, a reviewer could describe one candidate in the envelope and
// rewrite that identity from inside the text a human reads as commentary.
func fields(after string) (map[string]string, string) {
	lines := strings.Split(after, "\n")
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
	return out, rest
}

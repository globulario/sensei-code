package roles

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// An ATTESTATION is a human overriding a review obligation. It is not a review,
// and it never becomes one.
//
// The distinction it exists to keep is the one the relay made unavoidable. A
// reviewer can produce a complete, bound verdict and be unable to deliver it;
// an operator can carry those exact bytes and publish them; and nobody in that
// chain can establish that the reviewer's context was isolated from the work it
// judged. So a relayed review is advisory, and a task whose measured risk
// requires an independent look does not get one from it.
//
// That leaves a real decision with no vocabulary: the owner has read a bound
// verdict about an exact candidate and is willing to let the candidate proceed
// ON THEIR OWN AUTHORITY. Calling that an independent review would manufacture
// the very isolation nobody could establish. This type is the honest name for
// it -- recorded, bound, and separate -- so a receipt can say a candidate
// advanced because a human said so, rather than pretending it was reviewed.
//
// What it therefore never does: raise a session mode, satisfy an adversarial
// obligation, or describe its principal as the reviewer.
type Attestation struct {
	// The relayed review this attests to, named exactly: an attestation that
	// does not say WHICH review it covers is an opinion about the candidate,
	// not an override of a specific verdict.
	RequestID    string `json:"request_id"`
	ReviewDigest string `json:"review_digest"`
	// Reviewer is who produced the verdict being attested to. Recorded so the
	// override never absorbs the reviewer's identity: the human overrode a
	// review; they did not write one.
	Reviewer string `json:"reviewer_provider"`
	// Decision is what that verdict said, kept so a reader of the attestation
	// alone can see what was overridden.
	Decision Decision `json:"decision"`
	// Binding is the exact candidate this override applies to. A candidate that
	// moves is a different candidate, and this stops covering it.
	Binding Binding `json:"binding"`
	// Principal is the terminal operator who attested, as the control process
	// observed them. Authority to override, and nothing else.
	Principal string    `json:"principal"`
	At        time.Time `json:"at"`
	// Statement is fixed text, not the operator's prose. An override says one
	// thing and a free-text field would let it say something weaker or stronger
	// than what it actually is.
	Statement string `json:"statement"`
}

// AttestationStatement is what every NEW attestation says, verbatim.
//
// Transport-neutral since #182 R3. An owner overrides a specific canonical
// advisory review, whatever carried it; while only relayed reviews could be
// attested the older wording below was accurate, and once a review read
// directly off the authenticated mailbox became attestable it would have been
// a false description of what the operator did.
const AttestationStatement = "A local operator, at a controlling terminal, accepted this advisory review on their own " +
	"authority. Reviewer isolation was NOT established: this is a recorded human override, not an independent review."

// LegacyRelayAttestationStatement is the FROZEN text attestations carried
// before R3, kept valid and never rewritten.
//
// It described the narrower path that existed when those overrides were made,
// and it described it correctly. Editing a historical record to modernise its
// wording would restate what a person attested to, in their name, years after
// they said it; refusing it would retract an authority they actually exercised.
// So it is read, accepted, and left exactly as written.
const LegacyRelayAttestationStatement = "A local operator, at a controlling terminal, accepted this relayed review on their own " +
	"authority. Reviewer isolation was NOT established: this is a recorded human override, not an independent review."

// ErrNotAttested reports that no attestation covers what was asked about.
var ErrNotAttested = errors.New("no human attestation covers this candidate and review")

// Validate refuses an attestation that cannot mean what it claims.
func (a Attestation) Validate() error {
	switch {
	case strings.TrimSpace(a.RequestID) == "":
		return errors.New("an attestation must name the review request it covers")
	case strings.TrimSpace(a.ReviewDigest) == "":
		return errors.New("an attestation must name the exact review it covers by digest")
	case strings.TrimSpace(a.Reviewer) == "":
		return errors.New("an attestation must name who produced the review being overridden")
	case a.Decision != Accept:
		// Overriding a REVISE or an ESCALATE would advance a candidate its
		// reviewer objected to, which is not an override of an obligation but a
		// reversal of a finding.
		return fmt.Errorf("only an accepting review can be attested to, and this one says %q", a.Decision)
	case strings.TrimSpace(a.Principal) == "":
		return errors.New("an attestation must name the terminal principal who made it")
	case a.Statement != AttestationStatement && a.Statement != LegacyRelayAttestationStatement:
		// Two accepted texts and no third: the current one, and the frozen
		// pre-R3 one. A closed set read by membership, because "anything that
		// looks close enough" is how an override acquires free-form prose.
		return errors.New("an attestation carries the statement this project defines for one, unaltered")
	case strings.TrimSpace(a.Binding.TaskID) == "" ||
		strings.TrimSpace(a.Binding.BaseSHA) == "" ||
		strings.TrimSpace(a.Binding.CandidateDigest) == "" ||
		strings.TrimSpace(a.Binding.CandidateTree) == "":
		return errors.New("an attestation must name the exact candidate it covers: task, base, digest and tree")
	}
	return nil
}

// Covers reports whether this attestation applies to THIS candidate and THIS
// review, or says why it does not.
//
// Every identity field participates, for the reason Subject.Same does: an
// override that applied to "near enough" a candidate would let a human decision
// about one artifact advance another.
func (a Attestation) Covers(b Binding, reviewDigest string) error {
	if err := a.Validate(); err != nil {
		return err
	}
	if a.ReviewDigest != reviewDigest {
		return fmt.Errorf("%w: it covers review %s and this review is %s", ErrNotAttested, a.ReviewDigest, reviewDigest)
	}
	for _, f := range []struct{ name, want, got string }{
		{"task", a.Binding.TaskID, b.TaskID},
		{"base", a.Binding.BaseSHA, b.BaseSHA},
		{"candidate_digest", a.Binding.CandidateDigest, b.CandidateDigest},
		{"candidate_tree", a.Binding.CandidateTree, b.CandidateTree},
	} {
		if f.want != f.got {
			return fmt.Errorf("%w: it covers %s %s and this candidate is %s", ErrNotAttested, f.name, f.want, f.got)
		}
	}
	return nil
}

// Describe is the line a record carries. It names the override as an override.
func (a Attestation) Describe() string {
	return fmt.Sprintf("%s by %s was accepted under the authority of %s; recorded as a human override, "+
		"not an independent review", a.Decision, a.Reviewer, a.Principal)
}

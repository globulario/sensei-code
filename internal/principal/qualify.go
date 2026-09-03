package principal

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/globulario/sensei-code/internal/roles"
)

// A remote principal may hold both the architect and the reviewer role. It may
// exercise both. What it cannot do is turn holding both into independence.
//
// The failure this types out is already governed knowledge in this repository:
// invariant sensei_code.roles.adversarial_roles_inherit_nothing_and_bind_to_a_revision,
// failure mode sensei_code.review_forked_the_conversation_it_was_judging, and
// forbidden fix sensei_code.give_an_adversarial_role_the_architects_thread_for_context.
// A reviewer that has already read the argument for why the work is right — in
// its own words, because it wrote them — agrees more often than one that came
// to the artifact cold, and the agreement is worth nothing while being
// indistinguishable from the real thing in the transcript.
//
// Sensei Code can force a LOCAL adversarial role into a fresh session, because
// it launches the process. It cannot do that to a remote principal: the
// conversation is on the far side of the transport, and a client asserting that
// it started fresh is making exactly the claim that would need checking.
//
// So the response is not to relax the invariant and not to refuse the role. It
// is to type the review. A review by the principal that authored the
// architecture is ADVISORY: it may read the exact candidate, it may return
// REVISE and drive the repair loop, and it may afterwards return ACCEPT — and
// none of that satisfies an adversarial-review obligation, unlocks a transition
// whose proof requires an independent reviewer, or may be described as
// independent review, Sensei admission, or completion.
//
// Nothing in this file can grant anything. ObligationUnmet only ever withholds.

// ReviewKind is what a review is worth as evidence, which is a different
// question from what it says.
type ReviewKind string

const (
	// IndependentReview is a review by a principal that did not author the
	// architecture it is judging.
	//
	// It is NECESSARY and not SUFFICIENT. Independence of the architecture is
	// not independence of the implementation: whether the reviewer is also the
	// provider that wrote the candidate is a separate question, answered by
	// roles.ReviewVerdict.Validate and roles.Policy.Check, and neither this
	// value nor those checks can stand in for the other.
	IndependentReview ReviewKind = "independent"
	// AdvisoryReview is a review by the principal that authored the
	// architecture, or one whose independence cannot be established. It is a
	// real review with real findings and no evidentiary standing.
	AdvisoryReview ReviewKind = "advisory"
)

// AllReviewKinds is the closed set.
var AllReviewKinds = []ReviewKind{IndependentReview, AdvisoryReview}

func (k ReviewKind) Valid() bool {
	for _, known := range AllReviewKinds {
		if k == known {
			return true
		}
	}
	return false
}

func (k ReviewKind) String() string { return string(k) }

// Qualification is what one principal's review of one task is worth.
//
// Its fields are unexported and its only constructor is Qualify. That is the
// whole design: a struct another package could assemble would eventually be
// assembled with the convenient answer, by a caller in a hurry, and the
// convenient answer here is "independent". The zero value is advisory, so a
// Qualification nobody filled in claims nothing.
type Qualification struct {
	kind      ReviewKind
	architect ID
	reviewer  ID
	reason    string
}

// Qualify decides what a review by reviewer is worth on a task whose
// architecture was authored by architect.
//
// It fails toward advisory in every case it cannot establish, including the
// case where nobody recorded who authored the architecture. An unrecorded
// author is not evidence of a different author; treating it as one would make
// "we lost the attribution" produce a stronger verdict than "the same agent did
// both", which is the wrong direction for a missing fact.
func Qualify(architect, reviewer ID) Qualification {
	q := Qualification{kind: AdvisoryReview, architect: architect, reviewer: reviewer}
	switch {
	case !reviewer.Known():
		q.reason = "the reviewing principal is not identified, so this review is attributable to nobody"
	case !architect.Known():
		q.reason = "no principal is recorded as the author of this task's architecture, so this review cannot be shown to have come from anywhere else"
	case architect == reviewer:
		q.reason = string(reviewer) + " authored this task's architecture and is reviewing work produced under it"
	default:
		q.kind = IndependentReview
		q.reason = string(reviewer) + " did not author this task's architecture (" + string(architect) + " did)"
	}
	return q
}

// Kind is what this review is worth. Any value other than IndependentReview
// reads as advisory, so a Qualification that was never constructed properly
// cannot present itself as the stronger one.
func (q Qualification) Kind() ReviewKind {
	if q.kind == IndependentReview {
		return IndependentReview
	}
	return AdvisoryReview
}

// Independent reports whether the reviewer is independent OF THE ARCHITECTURE.
// It says nothing about whether the reviewer is independent of the implementer.
func (q Qualification) Independent() bool { return q.Kind() == IndependentReview }

// Architect and Reviewer are who this qualification is about.
func (q Qualification) Architect() ID { return q.architect }
func (q Qualification) Reviewer() ID  { return q.reviewer }

// Reason is why, in words a person reading the run record can act on.
func (q Qualification) Reason() string { return q.reason }

// ErrAdversarialObligationUnmet is the refusal a caller must not route around.
var ErrAdversarialObligationUnmet = errors.New("this task requires an independent review and has not had one")

// ObligationUnmet names the review obligation this qualification leaves open,
// or nil when it leaves none.
//
// Read the nil carefully: it is not a statement that the task's review
// obligation is SATISFIED. This function knows one thing — whether the reviewer
// authored the architecture — and a task's obligation also requires that the
// reviewer is not the provider that implemented the candidate, which
// roles.Policy.Check and roles.ReviewVerdict.Validate answer separately. A
// method that could return "satisfied" would be read as permission by the first
// caller in a hurry, so this one can only ever withhold.
func (q Qualification) ObligationUnmet(p roles.Policy) error {
	if !p.CrossProviderReview {
		return nil
	}
	if q.Independent() {
		return nil
	}
	return fmt.Errorf("%w: %s. Reason the task requires one: %s",
		ErrAdversarialObligationUnmet, q.reason, p.Reason)
}

// Statement is the line the run record carries, written so it cannot be read as
// more than it is.
func (q Qualification) Statement() string {
	if q.Independent() {
		return "review kind: independent of the architecture — " + q.reason +
			"; independence from the implementing provider is a separate check"
	}
	return "review kind: advisory — " + q.reason +
		"; it carries findings and satisfies no adversarial-review obligation, and it is not admission"
}

// MarshalJSON renders the qualification with independent_review DERIVED from
// the kind rather than stored beside it.
//
// Two fields holding one fact eventually disagree, and the disagreement is
// resolved silently in favour of whichever one the reader happened to consult.
// Here there is one fact and two renderings of it, and the rendering cannot
// contradict the fact because it is computed from it at the moment of writing.
func (q Qualification) MarshalJSON() ([]byte, error) {
	return json.Marshal(struct {
		Kind              ReviewKind `json:"review_kind"`
		IndependentReview bool       `json:"independent_review"`
		Architect         ID         `json:"architect_principal,omitempty"`
		Reviewer          ID         `json:"reviewer_principal,omitempty"`
		Reason            string     `json:"reason,omitempty"`
	}{
		Kind:              q.Kind(),
		IndependentReview: q.Independent(),
		Architect:         q.architect,
		Reviewer:          q.reviewer,
		Reason:            q.reason,
	})
}

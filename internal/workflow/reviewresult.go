package workflow

import (
	"github.com/globulario/sensei-code/internal/roles"
)

// A review has exactly one standing, and the standing travels with it.
//
// The previous shape was (roles.ReviewVerdict, bool), and production
// demonstrated why that is fragile within one slice of being written: both call
// sites did `_ = advisory` and the ordinary acceptance path stayed reachable.
// The transcript said "independent review not established" while the state
// machine acted as "review gate passed", which is the authority substitution
// this whole design exists to refuse.
//
// So the standing is not a value beside the verdict; it is which of two fields
// is set. There is no zero-value default that reads as the stronger one, no
// boolean to drop, and no way to ask what the reviewer decided without holding
// the thing that also knows what the decision is worth.

// ReviewResult is one review with exactly one standing.
//
// Its fields are unexported and its constructors are in this package, so a
// caller cannot assemble one that claims independence it was not given. Exactly
// one of the two is ever set; the zero value is neither, and answers no to the
// only question that grants anything.
type ReviewResult struct {
	independent *roles.ReviewVerdict
	advisory    *roles.Advisory
	// attested is an ADVISORY verdict a human overrode. It is a third standing
	// rather than a flag on advisory, for the same reason the first two are not
	// a boolean: a reader holding the result must not be able to reach the
	// verdict without also reaching what it is worth. It never implies
	// independence -- see SatisfiesAdversarialObligation, which still says no.
	attested *attestedVerdict
}

type attestedVerdict struct {
	advisory    roles.Advisory
	attestation roles.Attestation
}

// independentReview is a verdict from a session this project opened and
// observed.
func independentReview(v roles.ReviewVerdict) ReviewResult {
	return ReviewResult{independent: &v}
}

// advisoryReview is a verdict from a context this project could not observe.
func advisoryReview(a roles.Advisory) ReviewResult {
	return ReviewResult{advisory: &a}
}

// attestedReview is an advisory verdict a local operator overrode, on their own
// authority, for this exact candidate.
//
// The attestation is checked against the verdict here rather than trusted:
// Covers refuses an attestation about a different candidate or a different
// review, and an unchecked one would be exactly the manufactured authority this
// whole design refuses.
func attestedReview(a roles.Advisory, att roles.Attestation, reviewDigest string) (ReviewResult, error) {
	if err := att.Covers(a.Provenance.Binding(), reviewDigest); err != nil {
		return ReviewResult{}, err
	}
	return ReviewResult{attested: &attestedVerdict{advisory: a, attestation: att}}, nil
}

// Attestation is the human override this result carries, if it carries one.
func (r ReviewResult) Attestation() (roles.Attestation, bool) {
	if r.attested == nil {
		return roles.Attestation{}, false
	}
	return r.attested.attestation, true
}

// SatisfiesAdversarialObligation is the only question here that can grant
// anything, and it can only ever answer yes for a verdict whose isolation was
// established.
//
// Read it as necessary and not sufficient: a task also has to REQUIRE an
// independent review before this answer matters, and whether the reviewer was
// the implementer is a separate check roles.Policy makes. Nothing here decides
// admission.
// An attested result deliberately answers NO. A human overrode the obligation;
// nobody established the isolation it asks about, and a receipt that said
// otherwise would be the substitution this gate exists to prevent.
func (r ReviewResult) SatisfiesAdversarialObligation() bool { return r.independent != nil }

// Advisory reports the other side of the same fact, spelled out so a reader of
// a call site does not have to negate the sentence above in their head.
//
// An attested verdict is still advisory: the override changed what MAY PROCEED,
// not what the review established.
func (r ReviewResult) Advisory() bool { return r.advisory != nil || r.attested != nil }

// Verdict is the review itself, for the record and for the branches that act on
// what the reviewer said rather than on what it is worth.
func (r ReviewResult) Verdict() roles.ReviewVerdict {
	switch {
	case r.independent != nil:
		return *r.independent
	case r.advisory != nil:
		return r.advisory.ReviewVerdict
	case r.attested != nil:
		return r.attested.advisory.ReviewVerdict
	}
	return roles.ReviewVerdict{}
}

// Decision, Summary and Instruction are what the loop branches on. They are
// methods rather than fields so that reaching them means holding a
// ReviewResult, which is the thing that also knows the standing.
func (r ReviewResult) Decision() roles.Decision { return r.Verdict().Decision }
func (r ReviewResult) Summary() string          { return r.Verdict().Summary }
func (r ReviewResult) Instruction() string      { return r.Verdict().Instruction() }
func (r ReviewResult) Provenance() roles.Provenance {
	return r.Verdict().Provenance
}

// Describe is the line the record carries. An advisory result says so.
func (r ReviewResult) Describe() string {
	if r.attested != nil {
		return r.attested.advisory.Describe() + " — " + r.attested.attestation.Describe()
	}
	if r.advisory != nil {
		return r.advisory.Describe()
	}
	return string(r.Decision()) + " by " + r.Provenance().Provider
}

// Unlocks reports whether this review may unlock a transition that the task's
// policy gates on an independent review.
//
// It is deliberately phrased around the transition rather than around the
// verdict. An advisory ACCEPT is a real conclusion about architectural
// conformance and may drive repair; what it may not do is stand in for the
// independent look a high-risk task requires. Where the policy requires none,
// there is nothing for it to stand in for, and it continues.
// An ATTESTED result unlocks the transition and satisfies nothing. The
// obligation stays unmet and stays on the record; what the human decided is that
// the candidate may proceed anyway, which is a decision only a human holds. It
// is deliberately a separate branch from the one above: read as
// "SatisfiesAdversarialObligation || attested" it would look like two ways of
// meeting the same requirement, and they are not.
func (r ReviewResult) Unlocks(p roles.Policy) bool {
	if !p.CrossProviderReview {
		return true
	}
	if r.attested != nil {
		return true
	}
	return r.SatisfiesAdversarialObligation()
}

// candidateOutcome is what one worker's review loop concluded.
//
// Three values, because there are three outcomes and the third one used to have
// nowhere to go: a candidate that holds real work, was reviewed, and cannot be
// called accepted because the review that accepted it could not discharge the
// obligation the task carries. Squeezing that into the accepted branch was the
// bug; squeezing it into the failure branch would be a different lie, telling
// the next worker to fix code that has nothing wrong with it.
type candidateOutcome string

const (
	// candidateNotConverged means this worker did not produce an acceptable
	// candidate. The handoff ladder applies.
	candidateNotConverged candidateOutcome = "not_converged"
	// candidateAccepted means the review gate is satisfied on this task's own
	// terms.
	candidateAccepted candidateOutcome = "accepted"
	// candidateAwaitingIndependentReview means the candidate stands and the
	// task's required independent review has not happened. Nothing about the
	// candidate needs changing, and nothing may proceed on its behalf.
	candidateAwaitingIndependentReview candidateOutcome = "awaiting_independent_review"
	// candidateReviewUnanswered means the candidate was validated and audited, a
	// review request for that exact candidate was published, and no answer
	// arrived before the request's deadline. Nobody judged the candidate, so no
	// worker is sent at it and no other reviewer is substituted; it is preserved
	// awaiting the review it is owed.
	candidateReviewUnanswered candidateOutcome = "review_unanswered"
	// candidateReviewUnobtainable means the candidate was validated and audited
	// and EVERY authorized reviewer was tried without one being reached. Nobody
	// judged the candidate, so -- exactly as with an unanswered request -- no
	// worker is sent at it and nothing proceeds on its behalf. It is preserved
	// awaiting a fresh review request against the same immutable candidate.
	//
	// Distinct from candidateReviewUnanswered because the two resume
	// differently: an unanswered request already exists and is waited on, while
	// an unobtainable review has no live request and needs a NEW one issued,
	// possibly to a different provider.
	candidateReviewUnobtainable candidateOutcome = "review_unobtainable"
	// candidateReviewLifecycleFault means the review could not proceed because
	// of a REVIEW-LIFECYCLE fault rather than anything about the candidate: a
	// published request whose obligation could not be recorded, durable records
	// that disagree or cannot be read, or the caller stopping the run.
	//
	// Its own outcome because every existing one would be a lie with
	// consequences. not_converged records this worker as having failed and hands
	// the candidate to the next implementer -- but nothing the next worker can
	// edit records an obligation or repairs a malformed file. review_unobtainable
	// says every reviewer was tried and none could be reached, which is false:
	// the reviewer was never the problem. And accepted is obviously not it.
	//
	// So the candidate is preserved exactly as it stands and the run ends with
	// the lifecycle reason, the way a structural refusal already does.
	candidateReviewLifecycleFault candidateOutcome = "review_lifecycle_fault"
	// candidateReviewObservationFault means reviewer-origin evidence WAS
	// observed for the standing request and none of it established a usable
	// review: malformed or unattributable content, a canonical review of
	// another subject, or two different verdicts for one question.
	//
	// It shares a control action with the lifecycle fault -- preserve the
	// candidate, preserve the obligation, ask nobody else -- and deliberately
	// not an outcome, because the two mean different things. A lifecycle fault
	// says our own records cannot be acted on; this says the reviewer replied
	// and the reply is unusable. An operator does something different about
	// each, and collapsing them loses the only fact that tells them which.
	candidateReviewObservationFault candidateOutcome = "review_observation_fault"
)

// Accepted reads the outcome by membership. Written this way rather than as
// "not failed" so that a value added later is not silently treated as success.
func (o candidateOutcome) Accepted() bool { return o == candidateAccepted }

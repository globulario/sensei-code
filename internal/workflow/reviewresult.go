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

// SatisfiesAdversarialObligation is the only question here that can grant
// anything, and it can only ever answer yes for a verdict whose isolation was
// established.
//
// Read it as necessary and not sufficient: a task also has to REQUIRE an
// independent review before this answer matters, and whether the reviewer was
// the implementer is a separate check roles.Policy makes. Nothing here decides
// admission.
func (r ReviewResult) SatisfiesAdversarialObligation() bool { return r.independent != nil }

// Advisory reports the other side of the same fact, spelled out so a reader of
// a call site does not have to negate the sentence above in their head.
func (r ReviewResult) Advisory() bool { return r.advisory != nil }

// Verdict is the review itself, for the record and for the branches that act on
// what the reviewer said rather than on what it is worth.
func (r ReviewResult) Verdict() roles.ReviewVerdict {
	switch {
	case r.independent != nil:
		return *r.independent
	case r.advisory != nil:
		return r.advisory.ReviewVerdict
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
func (r ReviewResult) Unlocks(p roles.Policy) bool {
	if !p.CrossProviderReview {
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
)

// Accepted reads the outcome by membership. Written this way rather than as
// "not failed" so that a value added later is not silently treated as success.
func (o candidateOutcome) Accepted() bool { return o == candidateAccepted }

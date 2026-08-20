package roles

import (
	"errors"
	"fmt"
	"strings"
)

// Budgets are bounded, and they are bounded where the bound is enforced rather
// than in a type here that nothing consults. A role's revision budget is
// Workflow.ReviewCycles; its parse budget is the two attempts a caller gives a
// model to return the contract; its provider budget is the alternates on an
// Assignment. A Budget struct in this package would be a fourth place to look
// and the only one that decided nothing.

// Assignment is one role bound to one provider, with the providers to try if it
// cannot do the job.
//
// Alternates exist so that a provider failing is an orchestration problem
// rather than a human interruption. A model that is out of quota, unreachable
// or returning unparseable output has told us nothing about the architecture,
// and asking a person to decide something because a subscription lapsed is
// ceremony with a serious face on.
type Assignment struct {
	Role       Role     `json:"role"`
	Provider   string   `json:"provider"`
	Alternates []string `json:"alternates,omitempty"`
}

// Assign builds an assignment from what the providers declare they can do.
//
// exclude names providers that must not take this role — in practice the
// implementer, when the role is adversarial. It returns an error rather than
// silently assigning the excluded provider, because the silent version is a
// candidate reviewing itself and reporting a clean review.
func Assign(r Role, caps Capabilities, exclude ...string) (Assignment, error) {
	if !r.Valid() {
		return Assignment{}, fmt.Errorf("unknown role %q", r)
	}
	available := caps.For(r)
	var kept []string
	for _, p := range available {
		if containsFold(exclude, p) {
			continue
		}
		kept = append(kept, p)
	}
	if len(kept) == 0 {
		if len(available) != 0 {
			return Assignment{}, fmt.Errorf(
				"no provider can take the %s role: the only one that declares it (%s) is excluded from this task",
				r.Label(), strings.Join(available, ", "))
		}
		return Assignment{}, fmt.Errorf("no configured provider declares the %s role", r.Label())
	}
	return Assignment{Role: r, Provider: kept[0], Alternates: kept[1:]}, nil
}

// Fallback returns the next provider to try after this assignment's own
// provider has failed, and reports false when the bounded set is exhausted.
// Exhaustion is the point at which the architect — not the human — is asked what
// to do. Naming any other provider as failed reports exhaustion rather than
// guessing which one the caller meant.
func (a Assignment) Fallback(failed string) (Assignment, bool) {
	for i, alt := range a.Alternates {
		if strings.EqualFold(alt, failed) {
			continue
		}
		if strings.EqualFold(a.Provider, failed) {
			return Assignment{Role: a.Role, Provider: alt, Alternates: a.Alternates[i+1:]}, true
		}
	}
	return Assignment{}, false
}

// CrossProvider reports whether this assignment is genuinely independent of the
// named provider.
func (a Assignment) CrossProvider(other string) bool {
	return !strings.EqualFold(strings.TrimSpace(a.Provider), strings.TrimSpace(other))
}

// Policy is what a task's measured risk requires of the role assignment.
//
// It is derived from Sensei's structured change risk rather than from a
// reviewer's sense of importance, because the whole reason the risk is read
// structurally is that a sentence about danger cannot be compared to anything.
type Policy struct {
	// CrossProviderReview requires that the reviewer is not the provider that
	// implemented the candidate. Below high risk this is preferred; at high risk
	// it is required, and a run that cannot satisfy it fails the role rather
	// than quietly reviewing itself.
	CrossProviderReview bool `json:"cross_provider_review"`
	// Reason says which risk reading produced this, so a requirement can be
	// traced to the evidence that imposed it.
	Reason string `json:"reason,omitempty"`
}

// PolicyFor derives the requirement from Sensei's structured change-risk
// fields. An unclassified or absent verdict is treated as high risk: this is
// the same fail-closed reading the authority router uses, and for the same
// reason — an absent verdict once read as permission.
func PolicyFor(blastRadius, approvalGate string) Policy {
	blast := strings.ToLower(strings.TrimSpace(blastRadius))
	gate := strings.ToLower(strings.TrimSpace(approvalGate))
	switch {
	case blast == "" || gate == "" || blast == "unclassified" || gate == "unclassified":
		return Policy{CrossProviderReview: true,
			Reason: "change risk is unclassified, so independent review is required rather than assumed unnecessary"}
	case blast == "system" || blast == "cross_service" || gate == "human" || gate == "architect":
		return Policy{CrossProviderReview: true,
			Reason: "blast radius " + blast + " with approval gate " + gate}
	case blast == "module" || blast == "package":
		return Policy{CrossProviderReview: true,
			Reason: "blast radius " + blast + " reaches beyond the file being changed"}
	default:
		return Policy{Reason: "blast radius " + blast + " with approval gate " + gate}
	}
}

// Check refuses an assignment that does not meet the policy.
func (p Policy) Check(implementer string, review Assignment) error {
	if !p.CrossProviderReview {
		return nil
	}
	if review.Role != Reviewer {
		return fmt.Errorf("cross-provider review was checked against a %s assignment", review.Role.Label())
	}
	if !review.CrossProvider(implementer) {
		return fmt.Errorf(
			"this task requires review by a different provider than the one implementing it (%s), and %s. Reason: %s",
			implementer, sameProvider(review.Provider), p.Reason)
	}
	return nil
}

func sameProvider(p string) string {
	if strings.TrimSpace(p) == "" {
		return "no reviewer was assigned"
	}
	return "the reviewer is " + p
}

// ErrNoIndependentReviewer is returned when a deployment cannot satisfy an
// adversarial role at all. It is separated so a caller can report the honest
// cause: not that the candidate is bad, but that this deployment cannot judge
// it independently.
var ErrNoIndependentReviewer = errors.New("no independent reviewer is available")

func containsFold(list []string, want string) bool {
	for _, v := range list {
		if strings.EqualFold(strings.TrimSpace(v), strings.TrimSpace(want)) {
			return true
		}
	}
	return false
}

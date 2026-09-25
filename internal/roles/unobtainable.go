package roles

import (
	"errors"
	"fmt"
	"strings"
)

// ErrReviewUnobtainable reports that every authorized reviewer was tried for an
// exact candidate and none could be reached.
//
// It is the companion of ErrReviewUnanswered, and the distinction is which end
// failed. An unanswered review was PUBLISHED and nobody replied. An unobtainable
// review was never published at all: each provider in the fallback chain refused
// the connection, exhausted its quota, timed out or exited non-zero.
//
// Both are transport state. Neither is a verdict, and neither says anything about
// the candidate -- which is exactly why a single name for "the reviewer chain is
// exhausted" is needed. ErrReviewUnanswered deliberately excludes provider
// failure because ANOTHER PROVIDER MAY TRY; nothing decided what holds once every
// provider has tried and failed, so that case fell through to the ordinary
// candidate-failure branch and was reported as the IMPLEMENTER not converging.
//
// Observed 2026-09-16 on task-1789591067359023454: the reviewer exited 1 on
// quota, the alternate did not converge, and the orchestrator then handed the
// same candidate back to the failed reviewer AS ITS IMPLEMENTER. The run ended
// INCOMPLETE/FAILED having never judged the work. Availability became authority.
var ErrReviewUnobtainable = errors.New("no authorized reviewer could be reached for this candidate")

// ReviewAttemptFailure is one provider's failure to produce a review, kept so the
// participants that failed the independent-review leg can be named -- and
// excluded from implementing the candidate they could not judge.
type ReviewAttemptFailure struct {
	Provider string
	Cause    error
}

// ReviewUnobtainable is the exhausted reviewer chain, with the identity a later
// process needs to issue a FRESH request against the same immutable candidate.
//
// Binding is the identity the candidate had when review was attempted. A
// replacement request is a new obligation identity against the SAME candidate,
// never a new candidate, so anything that cannot show the same binding is not a
// continuation of this obligation.
type ReviewUnobtainable struct {
	Binding   Binding
	Attempt   int
	Attempted []ReviewAttemptFailure
}

func (u *ReviewUnobtainable) Error() string {
	return fmt.Sprintf("%v: candidate %s, tried %s",
		ErrReviewUnobtainable, u.Binding.CandidateDigest, strings.Join(u.Providers(), ", "))
}

// Unwrap exposes the condition and every transport cause, so errors.Is matches
// ErrReviewUnobtainable while each provider's reason stays readable.
func (u *ReviewUnobtainable) Unwrap() []error {
	out := []error{ErrReviewUnobtainable}
	for _, a := range u.Attempted {
		if a.Cause != nil {
			out = append(out, a.Cause)
		}
	}
	return out
}

// Providers names the participants that failed the independent-review leg for
// this candidate, in the order they were tried.
//
// This is the list an implementer selection must exclude. A participant that
// could not judge a candidate is not thereby qualified to write it: that
// substitution is how a failed review became implementation progress.
func (u *ReviewUnobtainable) Providers() []string {
	out := make([]string, 0, len(u.Attempted))
	for _, a := range u.Attempted {
		out = append(out, a.Provider)
	}
	return out
}

// Excludes reports whether a participant failed the independent-review leg for
// this candidate. Read by MEMBERSHIP of the recorded failures, never by
// excluding a known-good set, so a participant nobody recorded is not silently
// treated as eligible.
func (u *ReviewUnobtainable) Excludes(participant string) bool {
	p := strings.TrimSpace(participant)
	if p == "" {
		return false
	}
	for _, a := range u.Attempted {
		if strings.EqualFold(strings.TrimSpace(a.Provider), p) {
			return true
		}
	}
	return false
}

// ErrArchitectUnobtainable reports that every authorized architect was tried for
// one exact architectural question and none produced a bounded decision.
//
// It is the architect's companion to ErrReviewUnobtainable, and it exists for
// the same reason: "no provider could be obtained" and "the role decided
// against this" are different findings, and nothing decided what holds once
// every provider in a finite roster has been tried and failed. Without a name
// for the exhausted chain that case fell through to the ordinary role-failure
// branch and was reported as the ARCHITECT failing to decide.
//
// Observed 2026-09-24: requests r-e25830e22588e77d and r-36348ba82230e65a each
// waited 30 minutes and were withdrawn unanswered, and the run then ended
// INCOMPLETE/FAILED saying "architect could not produce a bounded decision".
// The account's pool was exhausted with a published reset 49 minutes later. The
// architect had not failed to decide; it was never reached. Availability became
// a verdict.
var ErrArchitectUnobtainable = errors.New("no authorized architect could be obtained for this turn")

// ErrArchitectRefusal reports that a party the request DID reach refused it.
//
// Its own condition, owned here rather than by a transport, because it is the
// one thing a fallback ladder must never reinterpret: a refused request is
// answered, and asking the next provider the same question would be shopping
// for a different answer rather than costing a fallback. A transport that can
// carry a bound refusal marks it with this so the ladder does not have to read
// a message to tell a refusal from an unreachable provider.
var ErrArchitectRefusal = errors.New("the architect request was refused by the party it reached")

// ArchitectAttemptFailure is one roster entry's failure to produce an architect
// answer, kept so the exhausted chain can name each party and its reason.
type ArchitectAttemptFailure struct {
	Provider string
	Cause    error
}

// ArchitectUnobtainable is the exhausted architect roster.
//
// The causes are kept in the order they were tried, and kept whole rather than
// reduced to a last error: a proven temporary unavailability carries the time
// its provider said it would serve again, and that evidence is what lets the
// task be preserved and retried instead of declared failed.
type ArchitectUnobtainable struct {
	Attempted []ArchitectAttemptFailure
}

func (u *ArchitectUnobtainable) Error() string {
	tried := make([]string, 0, len(u.Attempted))
	for _, a := range u.Attempted {
		if a.Cause == nil {
			tried = append(tried, a.Provider)
			continue
		}
		tried = append(tried, a.Provider+": "+a.Cause.Error())
	}
	return fmt.Sprintf("%v: tried %s", ErrArchitectUnobtainable, strings.Join(tried, "; "))
}

// Unwrap exposes the condition and every attempt's cause, so errors.Is matches
// ErrArchitectUnobtainable while each provider's own proof -- a typed
// unavailability with its reset time included -- stays reachable.
func (u *ArchitectUnobtainable) Unwrap() []error {
	out := []error{ErrArchitectUnobtainable}
	for _, a := range u.Attempted {
		if a.Cause != nil {
			out = append(out, a.Cause)
		}
	}
	return out
}

// Providers names the parties that were tried, in order.
func (u *ArchitectUnobtainable) Providers() []string {
	out := make([]string, 0, len(u.Attempted))
	for _, a := range u.Attempted {
		out = append(out, a.Provider)
	}
	return out
}

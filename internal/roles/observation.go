package roles

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// REVIEWER OBSERVATION IS EVIDENCE, NOT AUTHORITY.
//
// A waiter that ended without a usable review used to say one thing however it
// ended: "no answer arrived". That was false whenever something HAD arrived.
// The reviewer's own account posted bare JSON with no envelope; or a perfectly
// well-formed review of the candidate this one replaced; or two different
// verdicts for one request. Each of those is reviewer-origin evidence that
// establishes no authority -- and reporting it as silence told an operator to
// go and wait for a reviewer who had already replied.
//
// So absence and unusable evidence are different answers, and this is where the
// difference is named in terms no transport owns.
//
// Everything here is DIAGNOSTIC. Nothing in an observation discharges an
// obligation, qualifies a candidate, authorizes a reviewer, mints a request or
// raises standing. It says what was seen.

// ErrReviewObservationFault reports that reviewer-origin evidence was observed
// for a standing review request and none of it established a usable review.
//
// Deliberately NOT wrapping the neighbouring conditions, which share a control
// action and mean different things:
//
//	UNANSWERED          nobody produced relevant reviewer-origin content
//	OBSERVATION_FAULT   reviewer-origin evidence exists and grants nothing
//	                    (including a review staged here and not yet delivered)
//	LIFECYCLE_FAULT     our own durable authority records cannot be acted on
//	REVIEW_UNOBTAINABLE no authorized reviewer transport could be reached
//
// Collapsing any two of these loses the only fact an operator needs: whether to
// go and look at what the reviewer actually said.
var ErrReviewObservationFault = errors.New("reviewer-origin evidence was observed and none of it establishes a usable review")

// Observation kinds, as a closed vocabulary read by membership.
const (
	// ObservedMalformed is response-window content from the pinned reviewer
	// principal that cannot be read as a canonical artifact bound to an exact
	// request. It does NOT claim the bytes answer anything.
	ObservedMalformed = "MALFORMED_OR_UNATTRIBUTABLE"
	// ObservedWrongTarget is a syntactically valid canonical artifact that is
	// not the artifact this obligation asked for. A review of C1 is real
	// evidence about C1; it is simply not authority for C2.
	ObservedWrongTarget = "WRONG_TARGET"
	// ObservedConflict is two different canonical artifacts claiming the same
	// standing review, where one semantic answer is allowed.
	ObservedConflict = "CONFLICT"
	// ObservedDeliveryPending is a canonical artifact for THIS exact obligation
	// that is durably held here with its governed delivery incomplete.
	//
	// It is the only observation kind that is not about the mailbox. A relayed
	// review is validated and staged by this process before the App publishes
	// it, and between those two moments the reviewer's exact bytes are
	// demonstrably present and grant nothing. Reporting that as silence would
	// tell an operator to wait for a review they are already holding; reporting
	// it as an answer would let an unpublished verdict qualify a candidate.
	ObservedDeliveryPending = "DELIVERY_PENDING"
)

// ReviewObservation is one thing seen in a standing request's response window.
//
// Transport-neutral on purpose: the workflow reports what was observed without
// depending on the mailbox that observed it.
type ReviewObservation struct {
	Kind string `json:"kind"`

	// Where it was seen and who GitHub says wrote it. Locator rather than
	// content: the bytes stay where they were posted.
	Comment  int64     `json:"comment,omitempty"`
	Author   string    `json:"author,omitempty"`
	AuthorID int64     `json:"author_id,omitempty"`
	At       time.Time `json:"at,omitempty"`

	// BodyDigest names the EXACT observed bytes, so the same content seen twice
	// is one observation and an edited comment is a different one.
	BodyDigest string `json:"body_digest,omitempty"`
	Bytes      int    `json:"bytes,omitempty"`

	// Populated ONLY when canonical parsing succeeded. Malformed bytes never
	// acquire semantic fields they did not prove.
	ArtifactDigest string `json:"artifact_digest,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	Provider       string `json:"reviewer_provider,omitempty"`

	// Transport and RelayPrincipal are the residue of an observation that did
	// not come off the mailbox. Empty for everything read from a comment: an
	// observation states the identity it actually has, and a locator invented
	// for a staged relay would name a comment nobody posted.
	Transport      string `json:"transport,omitempty"`
	RelayPrincipal string `json:"relay_principal,omitempty"`

	// Mismatch names the first identity field on which a valid artifact differs
	// from the standing obligation. Empty unless Kind is WRONG_TARGET.
	Mismatch string `json:"mismatch,omitempty"`
	// Diagnostic says why, in the words of whatever refused it.
	Diagnostic string `json:"diagnostic,omitempty"`
}

// Identity is the stable identity of one observation, for de-duplication across
// repeated polls: where it was seen, and the exact bytes seen there.
func (o ReviewObservation) Identity() string {
	if o.Comment == 0 && o.BodyDigest == "" {
		// Not read from a comment. A staged relay is identified by which
		// delivery it is: the transport, the principal that carried it, and the
		// exact artifact.
		return fmt.Sprintf("%s|%s|%s", o.Transport, o.RelayPrincipal, o.ArtifactDigest)
	}
	return fmt.Sprintf("%d|%s", o.Comment, o.BodyDigest)
}

// ReviewObservationFault is the observation set for one standing obligation.
//
// It carries the obligation's identity so a later process continues the SAME
// review, exactly as ReviewUnanswered does -- the candidate and the request are
// preserved, and what differs is only what this run saw.
type ReviewObservationFault struct {
	RequestID      string
	RequestComment int64
	Conversation   string
	Binding        Binding
	ReviewCommit   string
	Observations   []ReviewObservation
}

func (f *ReviewObservationFault) Error() string {
	return fmt.Sprintf("%v: request %s for candidate %s saw %s",
		ErrReviewObservationFault, f.RequestID, f.Binding.CandidateDigest, f.describe())
}

// Unwrap exposes ONLY the observation condition. It must not wrap unanswered,
// unobtainable or lifecycle conditions: a consumer matching those must not
// match this.
func (f *ReviewObservationFault) Unwrap() []error { return []error{ErrReviewObservationFault} }

// Kinds lists the distinct observation kinds, in the order first seen.
func (f *ReviewObservationFault) Kinds() []string {
	var out []string
	seen := map[string]bool{}
	for _, o := range f.Observations {
		if !seen[o.Kind] {
			seen[o.Kind] = true
			out = append(out, o.Kind)
		}
	}
	return out
}

// Has reports whether a kind was observed.
func (f *ReviewObservationFault) Has(kind string) bool {
	for _, o := range f.Observations {
		if o.Kind == kind {
			return true
		}
	}
	return false
}

func (f *ReviewObservationFault) describe() string {
	counts := map[string]int{}
	for _, o := range f.Observations {
		counts[o.Kind]++
	}
	var parts []string
	for _, k := range f.Kinds() {
		parts = append(parts, fmt.Sprintf("%d %s", counts[k], k))
	}
	if len(parts) == 0 {
		return "no observations"
	}
	return strings.Join(parts, ", ")
}

package ghbridge

// WHAT THE MAILBOX ACTUALLY SAW WHILE A REVIEW WAS OWED.
//
// Before #182 R5 this path had one way of being unsuccessful. A reviewer-origin
// comment that failed canonical parsing was skipped with a bare continue; a
// perfectly valid review of the candidate this one replaced was filtered out by
// the match; and both ended the wait as "no answer arrived". That statement was
// false in exactly the cases where an operator most needed the truth -- the
// reviewer HAD replied, and was waiting for someone to notice the reply was
// unusable.
//
// So the scan reports observations rather than silently discarding what it
// cannot use. None of them is authority: only one exact-bound canonical artifact
// may proceed, and everything else is evidence about what is in the mailbox.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
)

// observation is one thing seen in a standing request's response window.
type observation struct {
	kind     string
	comment  int64
	author   string
	authorID int64
	at       time.Time

	bodyDigest string
	bytes      int

	// artifact is set ONLY when canonical parsing succeeded. Malformed bytes
	// never acquire semantic fields they did not prove.
	artifact *reviewartifact.Artifact

	mismatch   string
	diagnostic string
}

// boundCanonical is the one kind that may proceed. It is kept out of the roles
// vocabulary because it is not a fault: it is the answer.
const boundCanonical = "BOUND_CANONICAL"

func (o observation) identity() string { return fmt.Sprintf("%d|%s", o.comment, o.bodyDigest) }

// report renders an observation for consumers above the transport.
func (o observation) report() roles.ReviewObservation {
	out := roles.ReviewObservation{
		Kind: o.kind, Comment: o.comment, Author: o.author, AuthorID: o.authorID,
		At: o.at, BodyDigest: o.bodyDigest, Bytes: o.bytes,
		Mismatch: o.mismatch, Diagnostic: o.diagnostic,
	}
	if o.artifact != nil {
		out.ArtifactDigest = o.artifact.Digest
		out.RequestID = o.artifact.RequestID
		out.Provider = o.artifact.ReviewerProvider
	}
	return out
}

// bodyDigestOf names exact observed bytes, so the same comment seen on ten
// polls is one observation and an edited one is a different observation.
func bodyDigestOf(body string) string {
	sum := sha256.Sum256([]byte(body))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// otherProtocolMarkers are Sensei-Code envelopes that are NOT review responses.
//
// The App and the operator post through the same conversation, and some of that
// content is authored by the very account the reviewer answers from. A
// withdrawal, a wake, an architecture turn or a relay receipt is a known object
// of another kind -- calling one a malformed review would manufacture a
// reviewer fault out of the machine's own bookkeeping.
var otherProtocolMarkers = []string{
	requestMarker, relayedReviewMarker, attestationMarker,
	WithdrawnMarker, WakeMarker, architectureRequestMarker, architectureResponseMarker,
}

// otherProtocolObject reports whether a comment IS one of those objects.
//
// By its own top-level envelope, never by a marker appearing somewhere inside
// it. Substring matching excluded far more than it meant to: reviewer prose
// that merely mentions a marker vanished entirely, and -- worse -- a review-
// shaped comment carrying a second protocol marker was discarded BEFORE
// reviewartifact.Parse could say why it was unreadable. Both then decayed into
// NO_RESPONSE, which is the exact conflation this slice removes.
//
// A protocol object announces itself in its first line, the way every emitter
// in this package writes one. Anything else from the pinned reviewer principal
// is reviewer content, and if it cannot be read as a review that is a malformed
// observation rather than an absence.
func otherProtocolObject(body string) bool {
	head := strings.TrimLeft(body, " \t\r\n")
	for _, marker := range otherProtocolMarkers {
		if strings.HasPrefix(head, marker) {
			return true
		}
	}
	return false
}

// inResponseWindow reports whether a comment can be a response to THIS request.
//
// The boundary is the durable request locator, and nothing else. Deriving it
// from the current session, the waiter's own start, the conversation's age or
// the newest comment would let content that predates the request -- or belongs
// to another request entirely -- be reported as this reviewer answering badly.
//
// The comment id is preferred because it is exact and monotonic. PublishedAt is
// the fallback for obligations recorded before the locator existed; it is a
// weaker boundary and is used only when there is no locator to use.
func inResponseWindow(o ReviewObligation, c restComment) bool {
	if o.RequestComment > 0 {
		return c.ID > o.RequestComment
	}
	if o.PublishedAt.IsZero() {
		return false
	}
	if c.CreatedAt.IsZero() {
		return false
	}
	return c.CreatedAt.After(o.PublishedAt)
}

// classify decides what one in-window, pinned-principal comment is.
//
// Returns ok=false for content that is not a review response at all, which is
// different from content that is a bad one.
func classify(o ReviewObligation, c restComment) (observation, bool) {
	obs := observation{
		comment: c.ID, author: c.User.Login, authorID: c.User.ID, at: c.CreatedAt,
		bodyDigest: bodyDigestOf(c.Body), bytes: len(c.Body),
	}
	if otherProtocolObject(c.Body) {
		return observation{}, false
	}
	art, err := reviewartifact.Parse(c.Body)
	if err != nil {
		// Reviewer-origin content in this request's response window that cannot
		// be read as a canonical artifact. Deliberately conservative: this is
		// the class that covers the historical bare reviewer JSON, and reporting
		// it is the whole point -- it claims nothing about what the bytes mean.
		obs.kind = roles.ObservedMalformed
		obs.diagnostic = err.Error()
		return obs, true
	}
	obs.artifact = &art
	if m := boundMismatch(o, art); m != "" {
		// A real review of something else. Valid, and not authority here.
		obs.kind = roles.ObservedWrongTarget
		obs.mismatch = m
		obs.diagnostic = "the artifact is a valid canonical review of another subject"
		return obs, true
	}
	obs.kind = boundCanonical
	return obs, true
}

// boundMismatch names the first identity field on which a valid canonical
// artifact differs from the standing obligation, or "" when it is exactly the
// artifact this obligation asked for.
func boundMismatch(o ReviewObligation, art reviewartifact.Artifact) string {
	if art.RequestID != o.RequestID {
		return fmt.Sprintf("request is %s and this obligation is %s", art.RequestID, o.RequestID)
	}
	if m := subjectMismatch(o.Subject(), subjectOf(art)); m != "" {
		return m
	}
	if !sameProvider(art.ReviewerProvider, o.ReviewerProvider) {
		return fmt.Sprintf("reviewer_provider is %q and this obligation was published to %q",
			art.ReviewerProvider, o.ReviewerProvider)
	}
	return ""
}

// Observe reads everything the mailbox shows for ONE standing obligation.
//
// Authentication first, then the window, then classification. A comment from
// any other account is ordinary mailbox content: an unauthenticated party must
// not be able to make Sensei-Code report that its reviewer answered badly.
func Observe(ctx context.Context, box Issue, o ReviewObligation) ([]observation, error) {
	if strings.TrimSpace(box.Number) == "" {
		return nil, errors.New("reading the mailbox needs a pull request number")
	}
	if !o.ExpectedReviewer.Configured() {
		return nil, errors.New("reading the mailbox needs the principal permitted to answer; " +
			"an unconfigured principal authenticates nobody")
	}
	comments, err := mailboxComments(ctx, box)
	if err != nil {
		return nil, err
	}
	var seen []observation
	for _, c := range comments {
		if !o.ExpectedReviewer.Matches(c.User.ID, c.User.Login) {
			continue
		}
		if !inResponseWindow(o, c) {
			continue
		}
		if obs, ok := classify(o, c); ok {
			seen = append(seen, obs)
		}
	}
	return seen, nil
}

// observationSet accumulates what a waiter has seen across polls.
//
// Accumulated rather than replaced, because a waiter that only remembered its
// last poll would forget a malformed reply the moment the next poll returned
// nothing new -- and end as silence again.
type observationSet struct {
	order []observation
	seen  map[string]bool
}

func (s *observationSet) add(obs observation) {
	if s.seen == nil {
		s.seen = map[string]bool{}
	}
	if s.seen[obs.identity()] {
		return
	}
	s.seen[obs.identity()] = true
	s.order = append(s.order, obs)
}

func (s *observationSet) addAll(all []observation) {
	for _, obs := range all {
		s.add(obs)
	}
}

// bound lists the distinct exact-bound canonical artifacts observed, one per
// distinct artifact digest.
//
// One review posted once and read on ten polls is one answer, not ten. Two
// DIFFERENT digests for one standing request is the conflict.
func (s *observationSet) bound() []observation {
	var out []observation
	seen := map[string]bool{}
	for _, obs := range s.order {
		if obs.kind != boundCanonical || obs.artifact == nil {
			continue
		}
		if seen[obs.artifact.Digest] {
			continue
		}
		seen[obs.artifact.Digest] = true
		out = append(out, obs)
	}
	return out
}

// faults lists the non-authoritative observations, in the order first seen.
func (s *observationSet) faults() []observation {
	var out []observation
	for _, obs := range s.order {
		if obs.kind != boundCanonical {
			out = append(out, obs)
		}
	}
	return out
}

// empty reports whether nothing relevant was observed at all. ONLY this may
// become "no answer".
func (s *observationSet) empty() bool { return len(s.order) == 0 }

// conflict renders two or more distinct exact-bound artifacts as one fault.
//
// No first, newest or comment-order winner. Two different verdicts for one
// question is not a thing to choose between.
func conflictFrom(bound []observation) []observation {
	var out []observation
	for _, obs := range bound {
		c := obs
		c.kind = roles.ObservedConflict
		c.diagnostic = "more than one distinct canonical review answers this exact request"
		out = append(out, c)
	}
	return out
}

// observationFault reports an observation set that established no usable
// review, carrying the obligation a later process continues from.
func observationFault(o ReviewObligation, seen []observation) *roles.ReviewObservationFault {
	fault := &roles.ReviewObservationFault{
		RequestID: o.RequestID, RequestComment: o.RequestComment, Conversation: o.Conversation,
		Binding: roles.Binding{TaskID: o.TaskID, BaseSHA: o.BaseSHA,
			CandidateDigest: o.CandidateDigest, CandidateTree: o.CandidateTree},
		ReviewCommit: o.ReviewCommit,
	}
	for _, obs := range seen {
		fault.Observations = append(fault.Observations, obs.report())
	}
	return fault
}

// deliveryPending reports a canonical review that is DURABLY HELD for this exact
// obligation and whose governed delivery has not completed.
//
// It is an observation fault and shares that control action exactly: the same
// candidate, the same obligation, the same request, no reviewer fallback, no
// implementer handoff, no supersession, and a resumable terminal. What it must
// never share is the vocabulary of silence -- roles.ReviewUnanswered means
// nobody produced relevant reviewer-origin content, and here somebody did.
//
// No comment locator is invented. A staged relay has never been posted, so the
// observation states what it actually has: the transport, the principal that
// carried the bytes, and the artifact's exact digest.
func deliveryPending(o ReviewObligation, rec reviewstore.Record, art reviewartifact.Artifact) *roles.ReviewObservationFault {
	fault := &roles.ReviewObservationFault{
		RequestID: o.RequestID, RequestComment: o.RequestComment, Conversation: o.Conversation,
		Binding: roles.Binding{TaskID: o.TaskID, BaseSHA: o.BaseSHA,
			CandidateDigest: o.CandidateDigest, CandidateTree: o.CandidateTree},
		ReviewCommit: o.ReviewCommit,
	}
	for _, ev := range rec.Staged() {
		fault.Observations = append(fault.Observations, roles.ReviewObservation{
			Kind:           roles.ObservedDeliveryPending,
			ArtifactDigest: rec.ReviewDigest,
			RequestID:      o.RequestID,
			Provider:       art.ReviewerProvider,
			Transport:      string(ev.Transport),
			RelayPrincipal: ev.RelayPrincipal,
			At:             ev.ObservedAt,
			Diagnostic: fmt.Sprintf("reviewer %s produced this review and its %s delivery has not completed; "+
				"resubmit the same artifact with `sensei-code review submit` to finish it, which changes no bytes",
				art.ReviewerProvider, ev.Transport),
		})
	}
	return fault
}

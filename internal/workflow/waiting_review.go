package workflow

import (
	"encoding/json"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
)

// reviewKindUnanswered is the WAITING_REVIEW record's kind for a review request
// that got no answer before its deadline.
const reviewKindUnanswered = "unanswered"

// waitingReviewFrom reads the unanswered-review obligation a resumed task
// carries.
//
// It returns an identity ONLY for a complete unanswered record: the request id
// and the exact candidate that request was about. Anything else -- no record, an
// unreadable one, an advisory accept, a record missing a field -- yields nil, and
// nil means "no identity is claimed", never "the identity is unknown but assume
// it matches". A continuation that cannot show which candidate the old request
// was about must not reuse anything recorded for that request; the review it
// asks for is simply bound to the candidate it captures, and the old request is
// superseded by the transport when the new one is published.
func waitingReviewFrom(task session.Interrupted) *waitingReview {
	if !task.AwaitingReview || len(task.AwaitingReviewRecord) == 0 {
		return nil
	}
	var w waitingReview
	if err := json.Unmarshal(task.AwaitingReviewRecord, &w); err != nil {
		return nil
	}
	if w.ReviewKind != reviewKindUnanswered ||
		strings.TrimSpace(w.RequestID) == "" ||
		strings.TrimSpace(w.BaseSHA) == "" ||
		strings.TrimSpace(w.CandidateDigest) == "" ||
		strings.TrimSpace(w.CandidateTree) == "" {
		return nil
	}
	return &w
}

// recordedReviewKind is the review_kind a WAITING_REVIEW record states, or ""
// when it states none or cannot be read.
func recordedReviewKind(task session.Interrupted) string {
	if len(task.AwaitingReviewRecord) == 0 {
		return ""
	}
	var k struct {
		ReviewKind string `json:"review_kind"`
	}
	if json.Unmarshal(task.AwaitingReviewRecord, &k) != nil {
		return ""
	}
	return k.ReviewKind
}

// reconcileWaitingReview states, before the review is asked again, whether the
// candidate this resume captured is the candidate the unanswered request was
// about.
//
// The comparison is the whole identity the request carried -- base, digest and
// tree -- and it only ever REPORTS. It never decides that an old verdict or an
// old request still applies: the new review is bound to the binding captured
// now in both branches, and the transport supersedes the old request when it
// publishes the new one. What the statement prevents is silence: a candidate
// that moved while it was waiting is named as a different candidate that needs
// a fresh review, rather than looking like the same review asked twice.
func (e *Engine) reconcileWaitingReview(taskID string, w *waitingReview, binding roles.Binding) bool {
	same := w.BaseSHA == binding.BaseSHA &&
		w.CandidateDigest == binding.CandidateDigest &&
		w.CandidateTree == binding.CandidateTree
	payload := map[string]any{
		"superseded_request":        w.RequestID,
		"recorded_base":             w.BaseSHA,
		"recorded_candidate_digest": w.CandidateDigest,
		"recorded_candidate_tree":   w.CandidateTree,
		"base":                      binding.BaseSHA,
		"candidate_digest":          binding.CandidateDigest,
		"candidate_tree":            binding.CandidateTree,
		"same_candidate":            same,
	}
	summary := "the candidate is byte-identical to the one review request " + w.RequestID +
		" was about; its review is requested again under a new request id, and " + w.RequestID + " is superseded"
	if !same {
		summary = "the candidate changed since review request " + w.RequestID + " was published (candidate " +
			shortDigest(w.CandidateDigest) + " -> " + shortDigest(binding.CandidateDigest) +
			"); that request no longer applies, and this candidate needs a fresh review"
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, summary, payload))
	return same
}

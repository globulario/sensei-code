package workflow

import (
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

// AttestationSource is where a recorded human override is looked up.
//
// An interface rather than a store, so the engine holds no way to CREATE one.
// The implementation lives beside the relay it overrides (internal/ghbridge),
// and only the control process's attestation socket writes to it.
type AttestationSource interface {
	// AttestationFor returns the published override covering this exact
	// candidate AND this exact review, if one is recorded.
	//
	// Both keys, because one unchanged candidate can carry an override per
	// review it accumulated. Selecting on the candidate alone would return
	// whichever the store yielded first and hide the one that applies.
	AttestationFor(b roles.Binding, reviewDigest string) (roles.Attestation, bool, error)
}

// attestedOrAdvisory decides what an advisory verdict is worth HERE.
//
// A relayed review is advisory: nobody could establish that its reviewer was
// isolated from the work it judged, and no amount of carrying changes that. What
// can change is whether this workspace's owner has taken the decision to advance
// the candidate anyway. That decision is looked up, never inferred, and every
// condition below is a refusal that falls back to plain advisory:
//
//	the owner has granted this authority here          (configuration)
//	a store to read overrides from exists              (bridge configured)
//	the transport named which review it carried        (identity)
//	a published override covers that exact review      (binding + digest)
//
// The digest is the load-bearing one. It comes from the TRANSPORT -- the receipt
// of the artifact actually consumed -- and the override must name the same one.
// An override checked against its own idea of what it covers would be a record
// agreeing with itself.
func (e *Engine) attestedOrAdvisory(taskID string, advisory roles.Advisory, reviewDigest string) ReviewResult {
	if !e.Config.Workflow.OwnerAttestation || e.Attestations == nil || reviewDigest == "" {
		return advisoryReview(advisory)
	}
	att, found, err := e.Attestations.AttestationFor(advisory.Provenance.Binding(), reviewDigest)
	if err != nil {
		// Reported rather than swallowed: an unreadable override store is not
		// "there is no override", and the difference decides a transition.
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			"the human-override record could not be read, so this review stands as advisory: "+err.Error(),
			map[string]any{"review_kind": "advisory", "independent_review": false, "error": err.Error()}))
		return advisoryReview(advisory)
	}
	if !found {
		return advisoryReview(advisory)
	}
	result, err := attestedReview(advisory, att, reviewDigest)
	if err != nil {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			"a human override exists and does not cover this review, so it is not applied: "+err.Error(),
			map[string]any{
				"review_kind": "advisory", "independent_review": false,
				"attested_review_digest": att.ReviewDigest, "review_digest": reviewDigest,
				"error": err.Error(),
			}))
		return advisoryReview(advisory)
	}

	// The obligation is NOT discharged, and saying so is the point: the record
	// must show a candidate that advanced because a human decided to, beside the
	// independent review it never had.
	e.noteAdvisoryObligation(taskID, att.Describe()+
		"; the adversarial-review obligation was overridden by a human and remains unmet")
	e.emit(event.New(e.SessionID, taskID, event.SourceUser, event.Status, att.Describe(), map[string]any{
		"review_kind":                  "human_override",
		"independent_review":           false,
		"adversarial_obligation_unmet": true,
		"attesting_principal":          att.Principal,
		"reviewer_provider":            att.Reviewer,
		"request_id":                   att.RequestID,
		"review_digest":                att.ReviewDigest,
		"candidate_digest":             att.Binding.CandidateDigest,
		"candidate_tree":               att.Binding.CandidateTree,
		"base":                         att.Binding.BaseSHA,
		"statement":                    att.Statement,
	}))
	return result
}

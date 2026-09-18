package workflow

import (
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/roles"
)

// ValidateReviewBody reads a reviewer payload through the SAME wire
// contract and the SAME advisory rules the engine applies to a review answered
// over a transport.
//
// It exists so a relay is refused at the terminal, before anything durable,
// rather than accepted and then rejected by the engine later. It is not a
// second parser: decodeModelJSON, reviewDecision, numberFindings and
// roles.Advisory.Validate are the ones resolveReview uses. The engine still
// validates the consumed review again, with the implementer it knows.
//
// The session mode is Unverified by construction. A relayed review is advisory:
// the operator carried it, nobody here observed the reviewer's isolation.
func ValidateReviewBody(body string, binding roles.Binding, provider string) (roles.ReviewVerdict, error) {
	var d reviewDecision
	if err := decodeModelJSON(body, &d); err != nil {
		return roles.ReviewVerdict{}, err
	}
	verdict := roles.ReviewVerdict{
		Provenance: roles.Provenance{
			TaskID: binding.TaskID, Role: roles.Reviewer, Provider: provider,
			SessionMode:     roles.Unverified,
			BaseSHA:         binding.BaseSHA,
			CandidateDigest: binding.CandidateDigest,
			CandidateTree:   binding.CandidateTree,
			At:              time.Now().UTC(),
		},
		Decision:     roles.Decision(strings.ToLower(strings.TrimSpace(d.Decision))),
		Summary:      strings.TrimSpace(d.Summary),
		Instructions: d.Instructions,
		Findings:     numberFindings(d.Findings),
	}
	if err := roles.NewAdvisory(verdict).Validate(binding, ""); err != nil {
		return roles.ReviewVerdict{}, err
	}
	return verdict, nil
}

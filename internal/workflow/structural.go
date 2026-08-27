package workflow

// Structural failure is its own terminal (#89).
//
// A candidate that cannot be audited because of what it IS -- a payload the
// audit refuses by size, a diff the transport cannot carry -- is not a
// candidate a reviewer can judge or a further implementor can improve without
// a new candidate that removes the cause. Treating it as "no bounded review"
// and handing the same candidate to the next executor burned two more
// executors on golang/mod and never judged the two lines that were right.
//
// So the failure is represented where it happens, with its own name, and it
// stops the run there.

import (
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/sensei"
)

// errStructural marks a candidate failure no executor retry can address.
var errStructural = errors.New("structural candidate failure")

// structuralAuditCodes are the audit reason codes that describe the payload
// rather than the change. Read by membership: an unlisted code is a judgement
// the reviewer may still be asked about.
var structuralAuditCodes = map[string]bool{
	"malformed_diff":    true,
	"payload_too_large": true,
	"diff_too_large":    true,
	"oversized_payload": true,
}

// structuralAuditFailure names the structural reason an audit could not run,
// or "" when the audit's answer is about the change.
func structuralAuditFailure(v sensei.DiffAuditDecision) string {
	if v.Availability == sensei.AuditAvailable && v.Decision != sensei.AuditCannotVerify {
		return ""
	}
	for _, code := range v.ReasonCodes {
		if structuralAuditCodes[strings.ToLower(strings.TrimSpace(code))] {
			return "CANDIDATE_NOT_AUDITABLE (" + code + "): " + v.Diagnostic()
		}
	}
	return ""
}

// structuralFailure builds the terminal error.
func structuralFailure(reason string) error {
	return fmt.Errorf("%w: %s", errStructural, reason)
}

// auditCallFailure names an audit that produced no decodable verdict at all.
//
// It is classified structurally for the same reason a structural refusal is:
// nothing about the change was judged, no reviewer can judge it, and the next
// executor would submit the identical payload to the identical boundary.
func auditCallFailure(err error) string {
	return "CANDIDATE_NOT_AUDITABLE (audit_call_failed): the diff audit returned no verdict for this candidate: " + err.Error()
}

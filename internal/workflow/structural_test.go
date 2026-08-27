package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

// A structural audit refusal is named and is terminal; a judgement is not.
func TestAStructuralAuditRefusalIsNamedNotRetried(t *testing.T) {
	structural := sensei.DiffAuditDecision{Availability: sensei.AuditAvailabilityCannot,
		Decision: sensei.AuditCannotVerify, ReasonCodes: []string{"malformed_diff"}}
	if got := structuralAuditFailure(structural); !strings.HasPrefix(got, "CANDIDATE_NOT_AUDITABLE (malformed_diff)") {
		t.Fatalf("structural refusal not named: %q", got)
	}
	if !errors.Is(structuralFailure("x"), errStructural) {
		t.Fatal("structuralFailure does not carry the sentinel")
	}
	judgement := sensei.DiffAuditDecision{Availability: sensei.AuditAvailable, Decision: sensei.AuditBlock,
		ReasonCodes: []string{"forbidden_fix"}}
	if got := structuralAuditFailure(judgement); got != "" {
		t.Fatalf("a judgement was classified structural: %q", got)
	}
	// Read by membership: an unlisted reason on an unavailable audit is still
	// a judgement question, not a structural stop.
	unknown := sensei.DiffAuditDecision{Availability: sensei.AuditAvailabilityCannot,
		Decision: sensei.AuditCannotVerify, ReasonCodes: []string{"graph_unreachable"}}
	if got := structuralAuditFailure(unknown); got != "" {
		t.Fatalf("an unlisted reason was read as structural: %q", got)
	}
}

// The worker loop must stop on a structural failure before another executor
// is consulted, and the audit must be classified before any reviewer runs.
func TestAStructuralFailureNeverReachesTheNextExecutor(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	loop := src[strings.Index(src, "for _, worker := range e.Config.Implementors {"):]
	structuralCheck := strings.Index(loop, "errors.Is(err, errStructural)")
	failuresAppend := strings.Index(loop, "failures = append(failures")
	if structuralCheck < 0 || structuralCheck > failuresAppend {
		t.Fatal("the worker loop hands a structural failure to the next executor")
	}
	audit := src[strings.Index(src, `sc.CallTool("awareness_audit_diff"`):]
	if strings.Index(audit, "structuralAuditFailure(verdict)") > strings.Index(audit, "e.resolveReview(") {
		t.Fatal("the structural classification runs after the reviewer is consulted")
	}
	capture := strings.Index(src, "candidate.CandidateCapture(ctx, tc.Identity.BaseSHA, tc.Files)")
	if capture < 0 {
		t.Fatal("the candidate is not captured through the boundary with the plan's intended outputs")
	}
}

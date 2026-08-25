package workflow

// A preflight status is not a coverage verdict.
//
// The router converted one proposition into a stronger one:
//
//	PREFLIGHT_STATUS_EMPTY  ->  "graph coverage is absent for the planned files"
//
// Those are equivalent only if the preflight contract guarantees the
// equivalence. It does not, and the counterexample below is live rather than
// constructed: sensei-code auditing its own router found it.

import (
	"strings"
	"testing"
)

// The specimen. Kept as the literal response shape, because an abstract test
// would not have caught this and a real one did.
//
// Probing the running graph for internal/workflow/authority.go:
//
//	status   PREFLIGHT_STATUS_EMPTY
//	coverage sufficient=true indexed_file_count=1 file_count=1
//
// The status is accurate: no direct anchors bind that file. The stronger
// reading -- that the graph covers nothing here -- is false, and it was the
// reading that decided the route.
func TestAnEmptyStatusIsNotACoverageVerdict(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"coverage": {"sufficient": true, "direct_anchor_count": 0, "indexed_file_count": 1, "file_count": 1},
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)

	if !scoped.Coverage.Proven() {
		t.Fatal("the specimen no longer reproduces: this coverage must be proven")
	}
	got := decideRouteForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/workflow/authority.go"},
	})
	if got.ClosesGap() {
		t.Fatalf("an EMPTY status with proven coverage was routed to close a gap that is not open: %s", got.Condition)
	}
	if !got.Granted() {
		t.Fatalf("route = %s (%s)", got.Route, got.Condition)
	}
}

// The converse, which is the half that must not regress: a status that says OK
// while the coverage evidence proves nothing.
func TestAnOKStatusWithoutProvenCoverageDoesNotGrant(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"coverage": {"sufficient": false, "direct_anchor_count": 0, "indexed_file_count": 0, "file_count": 1},
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	got := decideRouteForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/event/bus.go"},
	})
	if got.Granted() {
		t.Fatal("an OK status granted authority over a region whose coverage proved nothing")
	}
	if !got.ClosesGap() {
		t.Fatalf("route = %s (%s); unproven coverage is bounded epistemic work", got.Route, got.Condition)
	}
}

// Sufficiency resting on neither anchors nor indexed files is not coverage.
// Coverage.PatternOnly exists to name exactly that, and the router must not
// count it.
func TestPatternOnlySufficiencyIsNotCoverage(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"coverage": {"sufficient": true, "direct_anchor_count": 0, "indexed_file_count": 0, "file_count": 1,
			"note": "matched an implementation pattern"},
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	if !scoped.Coverage.PatternOnly() {
		t.Fatal("the fixture no longer describes pattern-only sufficiency")
	}
	if got := decideRouteForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/event/bus.go"},
	}); got.Granted() {
		t.Fatal("a pattern match was counted as coverage over these files")
	}
}

// The routing consequence must be computed from evidence, so a status value
// nobody has classified changes what is ANSWERABLE and never what is COVERED.
func TestANewStatusValueCannotSilentlyChangeCoverage(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_SOMETHING_NEW",
		"coverage": {"sufficient": true, "direct_anchor_count": 3, "indexed_file_count": 1, "file_count": 1},
		`+healthyAuthority+`
	}`)
	got := decideRouteForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/event/bus.go"},
	})
	// Unreadable status: refuse rather than guess, and refuse as ignorance
	// rather than as a coverage gap.
	if got.Route != RouteCannotEstablish {
		t.Fatalf("an unrecognised status routed to %s", got.Route)
	}
	if got.ClosesGap() {
		t.Fatal("an unrecognised status was reported as a knowledge gap")
	}
}

// The refusal states the evidence it read, not the status it saw.
func TestTheCoverageConditionNamesItsEvidence(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"coverage": {"sufficient": false, "direct_anchor_count": 0, "indexed_file_count": 0, "file_count": 1,
			"note": "no anchored rules apply to the request"},
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	got := decideRouteForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/derived/derived.go"},
	})
	if !got.ClosesGap() {
		t.Fatalf("route = %s", got.Route)
	}
	if !strings.Contains(got.Condition, "no anchored rules apply") {
		t.Fatalf("the condition does not carry the coverage evidence it rests on: %q", got.Condition)
	}
}

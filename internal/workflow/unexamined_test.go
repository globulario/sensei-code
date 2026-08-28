package workflow

// A planned file the graph never examined is not covered by its neighbour.
//
// Live counterexample, 2026-08-28, graph 42e6e12c: a scoped preflight over
// [internal/workflow/engine.go, internal/workflow/zz_not_in_graph.go] -- the
// second file does not exist -- answered
//
//	status OK  coverage sufficient=true direct_anchor_count=3 file_count=2 indexed_file_count=1
//
// Coverage.Proven() is true on that answer, so the router read the REGION as
// covered and would have granted an edit to a file the graph has no facts
// about, on the strength of the invariants anchored to the file beside it.
// That is authority inherited from a neighbour (M25 §1): the plan can carry an
// ungrounded file into an anchored region and launder the region's coverage
// onto it.
//
// The fact that repairs it is per file and engine-owned: Action.Unexamined
// lists the planned architectural files a per-file preflight found unexamined
// (no anchor, not indexed). The router treats those files as a coverage gap,
// which the existing derived-coverage relation may close only by covering
// EVERY architectural file -- the same rule the cold path already applies.

import (
	"strings"
	"testing"
)

// neighbourCovered is the live answer above: the region proven by one file.
const neighbourCovered = `{"status":"PREFLIGHT_STATUS_OK",` +
	`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	healthyAuthority + `}`

func TestAnUnexaminedPlannedFileIsNotCoveredByItsNeighbour(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	anchored, unexamined := "internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"
	planned := []string{anchored, unexamined}

	// The specimen must be live: with nothing marking the second file
	// unexamined, the region-level answer grants. That is the defect's shape,
	// and it is what the engine's per-file fact exists to correct.
	if bare := routeAuthorityForAction(scoped, nil, plannedEdit(planned...)); !bare.Granted() {
		t.Fatalf("the specimen is not the neighbour-covered grant, so this proves nothing: %+v", bare)
	}

	got := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, Unexamined: []string{unexamined}})
	if got.Granted() {
		t.Fatalf("an unexamined planned file was granted on its neighbour's anchors: %+v", got)
	}
	if !got.ClosesGap() {
		t.Fatalf("an unexamined planned file is a coverage gap, and must route to close it: %+v", got)
	}
	if !strings.Contains(got.Condition, unexamined) || strings.Contains(got.Condition, anchored+",") {
		t.Fatalf("the refusal must name the unexamined file and not the covered one: %q", got.Condition)
	}
	if got.Gap.Kind != "coverage-unexamined" || len(got.Gap.Scope) != 1 || got.Gap.Scope[0] != unexamined {
		t.Fatalf("the gap identity must be the unexamined files, so the closure budget is spent on them: %+v", got.Gap)
	}
}

// The gap an unexamined file opens closes the way every coverage gap closes:
// a recognised derivation over EVERY architectural file, never over some.
func TestAnUnexaminedPlannedFileIsCoveredOnlyByADerivationOverIt(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	anchored, unexamined := "internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"
	planned := []string{anchored, unexamined}

	closed := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, Unexamined: []string{unexamined},
		DerivedCoverage: lockAnchors(planned...)})
	if !closed.Granted() {
		t.Fatalf("a derivation over every planned file must close the gap the unexamined file opened: %+v", closed)
	}

	for name, anchors := range map[string][]CoverageAnchor{
		"a derivation over the neighbour only":      lockAnchors(anchored),
		"an unrecognised family over both":          layeringAnchors(planned...),
		"a derivation over the unexamined one only": lockAnchors(unexamined),
	} {
		t.Run(name, func(t *testing.T) {
			got := routeAuthorityForAction(scoped, nil, Action{
				Stage: StageCandidateEdit, Files: planned, Unexamined: []string{unexamined},
				DerivedCoverage: anchors})
			if got.Granted() {
				t.Fatalf("insufficient derivation granted an unexamined file: %+v", got)
			}
			if !got.ClosesGap() {
				t.Fatalf("the gap must stay open: %+v", got)
			}
		})
	}
}

// A file under an operational grant is not asked to be examined: it is
// authorised to be edited, which is a different thing (M2.2). N3's exact
// planned set read file_count=3 indexed_file_count=2, and the unexamined file
// was the granted test. That shape must keep routing as it did.
func TestAnUnexaminedFileUnderAnOperationalGrantOpensNoGap(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	src, test := "internal/workflow/engine.go", "internal/workflow/engine_test.go"
	got := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{src, test},
		OperationalAuthority: []string{test}, Unexamined: []string{test}})
	if !got.Granted() {
		t.Fatalf("a granted, unexamined test file must not open a coverage gap: %+v", got)
	}
}

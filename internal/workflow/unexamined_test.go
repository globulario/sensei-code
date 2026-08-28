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
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

// neighbourCovered is the live answer above: the region proven by one file.
const neighbourCovered = `{"status":"PREFLIGHT_STATUS_OK",` +
	`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	identifiedAuthority + `}`

// identifiedAuthority is a healthy authority block that also names its graph
// generation, as live answers do; the per-file probes must be bound to it.
const identifiedAuthority = `"authority": {
	"authoritative": true,
	"verdict": "authoritative",
	"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
	"seed_state": "SEED_STATE_CURRENT",
	"graph_build_commit": "fac399f8225f",
	"source_repo_commit": "f56f5a305798"
}`

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

// A per-file answer that cannot be obtained is not a coverage gap.
//
// Found by the #115 review: the first cut turned a failed or unreadable
// per-file preflight into Unexamined, which is a gap a recognised derivation
// may close -- so Sensei becoming unavailable between the region call and the
// per-file call was a way to a grant. An instrument that will not answer is
// not one to reason from; the failure is returned and routePlan refuses.
func TestAPerFilePreflightFailureIsNotAClosableGap(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	calls := 0
	ask := func(f string) (sensei.PreflightDecision, error) {
		calls++
		if f == files[1] {
			return sensei.PreflightDecision{}, errors.New("Sensei went away")
		}
		return region, nil
	}
	got, err := unexaminedFiles(StageCandidateEdit, ask, files, region)
	if err == nil {
		t.Fatalf("a failed per-file preflight was represented as evidence: unexamined=%v", got)
	}
	if calls != 2 {
		t.Fatalf("every architectural file is asked once: %d call(s)", calls)
	}

	// And the honest answers still classify: examined stays out, unexamined
	// goes in, and a region that already examined every file asks nothing.
	answered := func(f string) (sensei.PreflightDecision, error) {
		if f == files[1] {
			return scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",`+
				`"coverage":{"sufficient":false,"direct_anchor_count":0,"file_count":1,"indexed_file_count":0},`+
				identifiedAuthority+`}`), nil
		}
		return region, nil
	}
	got, err = unexaminedFiles(StageCandidateEdit, answered, files, region)
	if err != nil || len(got) != 1 || got[0] != files[1] {
		t.Fatalf("unexamined = %v, %v; want only %s", got, err, files[1])
	}
	all := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":2},`+identifiedAuthority+`}`)
	if got, err := unexaminedFiles(StageCandidateEdit, func(string) (sensei.PreflightDecision, error) {
		t.Fatal("a fully examined region must ask nothing per file")
		return sensei.PreflightDecision{}, nil
	}, files, all); err != nil || len(got) != 0 {
		t.Fatalf("unexamined = %v, %v on a fully examined region", got, err)
	}
}

// A decoded answer the graph cannot vouch for is the same failure. A stale or
// non-authoritative per-file answer, or a status whose counters mean nothing,
// must not be classified by its coverage counters (#115 review, second pass).
func TestAnUncertifiablePerFilePreflightIsNotEvidence(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	for name, bad := range map[string]sensei.PreflightDecision{
		"authority not certifiable": {Status: sensei.PreflightOK},
		"status unspecified":        func() sensei.PreflightDecision { d := region; d.Status = sensei.PreflightUnspecified; return d }(),
		"status refused":            func() sensei.PreflightDecision { d := region; d.Status = "PREFLIGHT_STATUS_REFUSED"; return d }(),
		// The graph was rebuilt between the region call and this probe: each
		// answer is certifiable on its own and no single generation examined
		// what the two together would claim (#115 review, third pass).
		"different graph build":   func() sensei.PreflightDecision { d := region; d.Authority.GraphBuildCommit = "0123456789ab"; return d }(),
		"different source commit": func() sensei.PreflightDecision { d := region; d.Authority.SourceRepoCommit = "0123456789ab"; return d }(),
		"graph identity absent":   func() sensei.PreflightDecision { d := region; d.Authority.GraphBuildCommit = ""; return d }(),
	} {
		t.Run(name, func(t *testing.T) {
			got, err := unexaminedFiles(StageCandidateEdit, func(f string) (sensei.PreflightDecision, error) {
				if f == files[1] {
					return bad, nil
				}
				return region, nil
			}, files, region)
			if err == nil {
				t.Fatalf("an uncertifiable per-file answer was read as evidence: unexamined=%v", got)
			}
		})
	}
}

// An observation asks nothing per file: the lane grants no authority, and an
// unusable graph must not prevent the investigation that could diagnose it.
func TestAnObservationAsksNothingPerFile(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	got, err := unexaminedFiles(StageObserve, func(string) (sensei.PreflightDecision, error) {
		t.Fatal("an observation asked the graph per file")
		return sensei.PreflightDecision{}, nil
	}, []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}, region)
	if err != nil || len(got) != 0 {
		t.Fatalf("observation: unexamined=%v err=%v", got, err)
	}
}

// A region answer that names no graph generation cannot bind any probe.
func TestARegionWithoutGraphIdentityBindsNoProbe(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	region.Authority.SourceRepoCommit = ""
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	got, err := unexaminedFiles(StageCandidateEdit, func(string) (sensei.PreflightDecision, error) { return region, nil }, files, region)
	if err == nil {
		t.Fatalf("probes were bound to a region with no graph identity: unexamined=%v", got)
	}
}

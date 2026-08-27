package workflow

import "testing"

// A coverage-type BLIND SPOT is the other face of "the graph does not vouch for
// this region", and it must be closable by the same channel the coverage-absent
// branch already honours.
//
// The first cold-start run to reach link 5 -- a true lock discipline, DERIVED
// inside the governed run, one anchor over the one planned file -- still routed
// to bounded work, because the preflight had reported "graph indexes this area
// but no anchored rules apply" as a blind spot rather than as absent coverage,
// and that branch never consulted derived coverage.
func TestADerivedFactClosesACoverageBlindSpotToo(t *testing.T) {
	planned := []string{"semaphore/semaphore.go"}
	body := okBody(t, []string{
		"coverage: graph indexes this area but no anchored rules apply to the request",
	}, "APPROVAL_GATE_NONE")
	scoped := scopedPreflight(t, body)

	// No derived coverage: still a bounded knowledge gap.
	bare := routeAuthorityForAction(scoped, nil, Action{Stage: StageCandidateEdit, Files: planned})
	if !bare.ClosesGap() {
		t.Fatalf("a coverage blind spot with no derivation did not route to bounded work: %+v", bare)
	}

	// A relevant derived anchor over exactly the planned file: the blind spot
	// is spent and the edit is assessed like any other.
	covered := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: lockAnchors(planned...)})
	if covered.ClosesGap() {
		t.Fatalf("a derived fact did not close the coverage blind spot: %+v", covered)
	}
	if !covered.Granted() {
		t.Fatalf("after closure the bounded edit was not granted: %+v", covered)
	}

	// Fail closed: an anchor of a family nobody has named as an answer
	// resolves nothing, however true its proposition.
	foreign := []CoverageAnchor{{File: planned[0], Requirement: RequirementUnrecognised,
		Describe: "semaphore/semaphore.go [layering]"}}
	still := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: foreign})
	if !still.ClosesGap() {
		t.Fatalf("an unrecognised derivation family closed a coverage blind spot: %+v", still)
	}
}

// Closing the coverage half of a blind-spot report does not spend the
// consequence half. A region that is now covered AND sits on a security path is
// judged on the path, as a covered region would be.
func TestClosingCoverageDoesNotSpendConsequenceSignals(t *testing.T) {
	planned := []string{"semaphore/semaphore.go"}
	body := okBody(t, []string{
		"coverage: graph indexes this area but no anchored rules apply to the request",
		"file path under high-risk directory",
	}, "APPROVAL_GATE_NONE")
	scoped := scopedPreflight(t, body)
	withCoverage := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: lockAnchors(planned...)})
	if withCoverage.ClosesGap() {
		t.Fatalf("the coverage half was not spent: %+v", withCoverage)
	}
	// Whatever the consequence lane decides for a covered high-risk region,
	// it must be the SAME decision it reaches without the blind spot at all.
	clean := scopedPreflight(t, okBody(t, []string{"file path under high-risk directory"}, "APPROVAL_GATE_NONE"))
	baseline := routeAuthorityForAction(clean, nil, Action{Stage: StageCandidateEdit, Files: planned})
	if withCoverage.Route != baseline.Route {
		t.Fatalf("closing coverage changed the consequence decision: %s vs baseline %s",
			withCoverage.Route, baseline.Route)
	}
}

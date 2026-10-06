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
// decided per file (DF-30): each unexamined file leaves it only by its own
// canonical prospective authority unit or by a recognised derivation over
// THAT file, never by authority some other planned file holds.

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

// The gap an unexamined file opens closes by a recognised derivation over the
// unexamined file itself. It never closes on the neighbour's derivation, and
// -- since DF-30 -- the examined neighbour is not required to acquire one.
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
	own := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, Unexamined: []string{unexamined},
		DerivedCoverage: lockAnchors(unexamined)})
	if !own.Granted() {
		t.Fatalf("a derivation over the unexamined file closes its gap; the examined neighbour is not asked for one: %+v", own)
	}

	for name, anchors := range map[string][]CoverageAnchor{
		"a derivation over the neighbour only": lockAnchors(anchored),
		"an unrecognised family over both":     layeringAnchors(planned...),
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
		return probeOf(region), nil
	}
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, ask, files, region)
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
		return probeOf(region), nil
	}
	got, _, _, err = unexaminedFiles(StageCandidateEdit, 2, answered, files, region)
	if err != nil || len(got) != 1 || got[0] != files[1] {
		t.Fatalf("unexamined = %v, %v; want only %s", got, err, files[1])
	}
	// A region that says every file is indexed is still asked per file: the
	// aggregate cannot carry each file's own published sufficiency
	// (TestFullyIndexedRegionDoesNotHidePerFileInsufficiency).
	all := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":2},`+identifiedAuthority+`}`)
	asked := 0
	if got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(string) (sensei.PreflightDecision, error) {
		asked++
		return probeOf(all), nil
	}, files, all); err != nil || len(got) != 0 || asked != 2 {
		t.Fatalf("unexamined = %v, %v, %d probe(s) on a fully examined region", got, err, asked)
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
		"status unspecified": func() sensei.PreflightDecision {
			d := probeOf(region)
			d.Status = sensei.PreflightUnspecified
			return d
		}(),
		"status refused": func() sensei.PreflightDecision { d := probeOf(region); d.Status = "PREFLIGHT_STATUS_REFUSED"; return d }(),
		// The graph was rebuilt between the region call and this probe: each
		// answer is certifiable on its own and no single generation examined
		// what the two together would claim (#115 review, third pass).
		"different graph build": func() sensei.PreflightDecision {
			d := probeOf(region)
			d.Authority.GraphBuildCommit = "0123456789ab"
			return d
		}(),
		"different source commit": func() sensei.PreflightDecision {
			d := probeOf(region)
			d.Authority.SourceRepoCommit = "0123456789ab"
			return d
		}(),
		"graph identity absent": func() sensei.PreflightDecision { d := probeOf(region); d.Authority.GraphBuildCommit = ""; return d }(),
		// DEGRADED for a reason that is not a coverage gap: the region router
		// refuses to reason from it, and so does the probe (#115, fourth pass).
		"degraded, unrecognised blind spot": func() sensei.PreflightDecision {
			d := probeOf(region)
			d.Status = sensei.PreflightDegraded
			d.BlindSpots = []string{"backend unhealthy: oxigraph did not answer"}
			return d
		}(),
		"degraded, no blind spot at all": func() sensei.PreflightDecision { d := probeOf(region); d.Status = sensei.PreflightDegraded; return d }(),
	} {
		t.Run(name, func(t *testing.T) {
			got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(f string) (sensei.PreflightDecision, error) {
				if f == files[1] {
					return bad, nil
				}
				return probeOf(region), nil
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
	got, _, _, err := unexaminedFiles(StageObserve, 2, func(string) (sensei.PreflightDecision, error) {
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
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(string) (sensei.PreflightDecision, error) { return probeOf(region), nil }, files, region)
	if err == nil {
		t.Fatalf("probes were bound to a region with no graph identity: unexamined=%v", got)
	}
}

// DEGRADED for a coverage reason is an uninformed graph, and its counters are
// read -- the same reading the region router applies. This is the live shape
// of a risky file the graph has examined and has no facts about.
func TestACoverageShapedDegradedProbeIsRead(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	files := []string{"internal/workflow/engine.go", "internal/workflow/authority_test.go"}
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(f string) (sensei.PreflightDecision, error) {
		if f == files[1] {
			d := probeOf(region)
			d.Status = sensei.PreflightDegraded
			d.BlindSpots = []string{"high_risk_path_no_direct_anchors: file is under a high-risk directory but no awareness anchors apply",
				"this is NOT proof of safety — the graph has no facts about this file"}
			d.Coverage = sensei.Coverage{Sufficient: true, FileCount: 1, IndexedFileCount: 1}
			return d, nil
		}
		return probeOf(region), nil
	}, files, region)
	if err != nil || len(got) != 0 {
		t.Fatalf("an examined, coverage-degraded file was not read: unexamined=%v err=%v", got, err)
	}
}

// Region counts that do not describe the requested plan are not an answer
// about it. A region that omits file_count is probed (nothing is claimed);
// one that counts a different number of files than were asked about, or
// more indexed than counted, fails closed (#115, fourth and eleventh pass).
func TestAggregateCountsMustDescribeTheRequestedPlan(t *testing.T) {
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	omitted := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK","coverage":{"sufficient":true,"direct_anchor_count":3},`+identifiedAuthority+`}`)
	if !omitted.Coverage.Proven() {
		t.Fatalf("the specimen must be a region the router would call proven: %+v", omitted.Coverage)
	}
	asked := 0
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(f string) (sensei.PreflightDecision, error) {
		asked++
		d := probeOf(omitted)
		if f == files[1] {
			d.Status = sensei.PreflightEmpty
			d.Coverage = sensei.Coverage{FileCount: 1}
		}
		return d, nil
	}, files, omitted)
	if asked != 2 || err != nil || len(got) != 1 || got[0] != files[1] {
		t.Fatalf("file_count omitted: %d probe(s), unexamined=%v err=%v", asked, got, err)
	}
	fewer := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":1,"indexed_file_count":1},`+identifiedAuthority+`}`)
	if got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(string) (sensei.PreflightDecision, error) {
		t.Fatal("a region counting a different number of files than requested was probed instead of refused")
		return sensei.PreflightDecision{}, nil
	}, files, fewer); err == nil {
		t.Fatalf("counts about a different plan were read as evidence: unexamined=%v", got)
	}
}

// A probe's published sufficiency is honoured. Nonzero counts beside
// sufficient=false are a valid answer shape, and they do not make the file
// examined (#115, fifth pass; the routine tier pins the same reading).
func TestAProbePublishedInsufficientIsUnexamined(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(f string) (sensei.PreflightDecision, error) {
		if f == files[1] {
			d := probeOf(region)
			d.Status = sensei.PreflightEmpty
			d.Coverage = sensei.Coverage{Sufficient: false, DirectAnchorCount: 1, FileCount: 1, IndexedFileCount: 1}
			return d, nil
		}
		return probeOf(region), nil
	}, files, region)
	if err != nil || len(got) != 1 || got[0] != files[1] {
		t.Fatalf("counts overrode a published insufficiency: unexamined=%v err=%v", got, err)
	}
}

// A gated plan is probed like any other once the caller decides to probe it
// (after the human's answer); the probe itself does not read the gate, or an
// authorised gated plan could never be probed (#115 review).
func TestAGatedPlanIsProbedWhenAsked(t *testing.T) {
	gated := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},`+
		identifiedAuthority+`}`)
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(f string) (sensei.PreflightDecision, error) {
		d := probeOf(gated)
		if f == files[1] {
			d.Status = sensei.PreflightEmpty
			d.Coverage = sensei.Coverage{FileCount: 1}
		}
		return d, nil
	}, files, gated)
	if err != nil || len(got) != 1 || got[0] != files[1] {
		t.Fatalf("gated: unexamined=%v err=%v", got, err)
	}
	// Before the answer the router sends it to the human, unexamined or not.
	if r := routeAuthorityForAction(gated, nil, plannedEdit(files...)); r.Route != RouteHuman {
		t.Fatalf("a gated plan routed to %s", r.Route)
	}
}

// probeOf is the answer a single-file preflight gives for an examined,
// anchored file: the region's authority and status, coverage about ONE file.
func probeOf(region sensei.PreflightDecision) sensei.PreflightDecision {
	d := region
	d.Coverage = sensei.Coverage{Sufficient: true, DirectAnchorCount: 3, FileCount: 1, IndexedFileCount: 1}
	return d
}

// A probe that does not describe exactly the file asked about is not that
// file's answer, however proven its counts look (#115, sixth pass).
func TestAProbeMustDescribeExactlyOneFile(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	for name, cov := range map[string]sensei.Coverage{
		"file_count omitted":  {Sufficient: true, DirectAnchorCount: 3, IndexedFileCount: 1},
		"two files":           {Sufficient: true, DirectAnchorCount: 3, FileCount: 2, IndexedFileCount: 1},
		"more indexed than 1": {Sufficient: true, DirectAnchorCount: 3, FileCount: 1, IndexedFileCount: 2},
	} {
		t.Run(name, func(t *testing.T) {
			got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(f string) (sensei.PreflightDecision, error) {
				d := probeOf(region)
				if f == files[1] {
					d.Coverage = cov
				}
				return d, nil
			}, files, region)
			if err == nil {
				t.Fatalf("a mis-scoped probe was read as this file's answer: unexamined=%v", got)
			}
		})
	}
}

// Probes run only where the next step is a worker: a grant, or a human-owned
// route whose condition the human has already authorised. A refusal, an
// observation, or a human question not yet answered is returned as it is, so
// a probe failure never aborts a plan before it reaches its boundary (#115).
func TestProbesRunOnlyWhereTheNextStepIsAWorker(t *testing.T) {
	region := scopedPreflight(t, neighbourCovered)
	files := []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}
	route := func(scoped sensei.PreflightDecision, a Action) Routing {
		return routeAuthorityForAction(scoped, nil, a)
	}
	if !probeNeeded(route(region, plannedEdit(files...)), false) {
		t.Fatal("the neighbour-covered edit is the case the probes exist for")
	}
	outward := Action{Stage: StageCandidateEdit, Files: files, DeclaredSteps: []string{"git push origin main", "deploy to production"}}
	if probeNeeded(route(region, outward), false) {
		t.Fatal("a plan declaring an outward action was probed ahead of its human-owned boundary")
	}
	gated := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},`+
		identifiedAuthority+`}`)
	if probeNeeded(route(gated, plannedEdit(files...)), false) {
		t.Fatal("a gated plan was probed before the human answered")
	}
	// Once the human has authorised the gate, the next step is a worker, and
	// the files the graph never examined are asked about before it runs.
	if !probeNeeded(route(gated, plannedEdit(files...)), true) {
		t.Fatal("an authorised gate was not probed before handing the plan to a worker")
	}
	if probeNeeded(route(sensei.PreflightDecision{Status: sensei.PreflightDegraded}, plannedEdit(files...)), true) {
		t.Fatal("a plan on an uncertifiable graph was probed")
	}
	if probeNeeded(route(region, observeAction(files...)), true) {
		t.Fatal("an observation was probed")
	}
}

// A human's authorisation of a consequence does not admit a file the graph
// never examined: the question was about the gate, not about coverage, and
// the router asks the gate first, so the unexamined files are asked after the
// answer and before the worker (#115, eighth pass).
func TestAnAuthorisedConsequenceDoesNotAdmitAnUnexaminedFile(t *testing.T) {
	gated := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},`+
		identifiedAuthority+`}`)
	anchored, unexamined := "internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"
	files := []string{anchored, unexamined}
	spots := readBlindSpots(gated.BlindSpots)
	with := Action{Stage: StageCandidateEdit, Files: files, Unexamined: []string{unexamined}}

	human := routeAuthorityForAction(gated, nil, with)
	if !human.RequiresHuman() {
		t.Fatalf("the gate must be asked before coverage: %+v", human)
	}
	if got := afterAuthorization(human, false, with, spots); !got.RequiresHuman() {
		t.Fatalf("an unanswered gate was displaced: %+v", got)
	}
	got := afterAuthorization(human, true, with, spots)
	if got.Granted() || got.RequiresHuman() || !got.ClosesGap() || got.Gap.Kind != "coverage-unexamined" {
		t.Fatalf("an authorised consequence admitted an unexamined file: %+v", got)
	}
	closed := with
	closed.DerivedCoverage = lockAnchors(files...)
	if got := afterAuthorization(human, true, closed, spots); !got.RequiresHuman() {
		t.Fatalf("a closed gap displaced the authorised route: %+v", got)
	}
	if got := afterAuthorization(human, true, plannedEdit(files...), spots); !got.RequiresHuman() {
		t.Fatalf("a fully examined plan was displaced: %+v", got)
	}
}

// The gap a human's answer leaves open carries the pinned world, as the gap
// routePlan builds does: the closure budget is keyed on the identity, and an
// identity without its world would buy the same question a fresh receipt
// (#115 review, ninth pass).
func TestAPostAuthorizationGapCarriesThePinnedWorld(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "afterHumanAuthorization")
	if !strings.Contains(body, "e.governedBase(") || !strings.Contains(body, "Gap.World") {
		t.Fatal("afterHumanAuthorization does not complete the gap identity with the pinned world")
	}
}

// A region that claims more files examined than it was asked about is not an
// answer about this plan; it fails closed rather than skipping the probes on
// an impossible count (#115, tenth pass).
func TestImpossibleAggregateCountsFailClosed(t *testing.T) {
	region := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":3},`+identifiedAuthority+`}`)
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(string) (sensei.PreflightDecision, error) {
		t.Fatal("an impossible region answer was probed instead of refused")
		return sensei.PreflightDecision{}, nil
	}, []string{"internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"}, region)
	if err == nil {
		t.Fatalf("impossible counts were read as evidence: unexamined=%v", got)
	}
}

// An exhausted post-authorization gap reaches the human like every exhausted
// gap: it is recorded as a closure question and escalated, never turned into
// an abort (#115, tenth pass).
func TestAnExhaustedPostAuthorizationGapEscalates(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	if n := strings.Count(src, "e.recordClosureQuestion(taskID"); n != 3 {
		t.Fatalf("%d of 3 exhausted-gap sites record the closure question for the human", n)
	}
	if strings.Contains(src, "stayed open: %s") {
		t.Fatal("an exhausted post-authorization gap still aborts instead of escalating")
	}
}

// A fully indexed region does not substitute for each file's own published
// sufficiency. The aggregate can say 2/2 indexed and sufficient while one
// file, asked alone, publishes sufficient=false -- a shape the fifth pass
// established as meaningful. Skipping the probes on the aggregate count was
// the one trapdoor left: per-file sufficiency was authoritative except when
// the aggregate stopped us asking (owner review of 67f68f9).
func TestFullyIndexedRegionDoesNotHidePerFileInsufficiency(t *testing.T) {
	region := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":2},`+identifiedAuthority+`}`)
	if !region.Coverage.Proven() {
		t.Fatalf("the specimen must be a region the router would call proven: %+v", region.Coverage)
	}
	a, b := "internal/workflow/engine.go", "internal/workflow/zz_examined_but_insufficient.go"
	asked := 0
	got, _, _, err := unexaminedFiles(StageCandidateEdit, 2, func(f string) (sensei.PreflightDecision, error) {
		asked++
		d := probeOf(region)
		if f == b {
			d.Status = sensei.PreflightEmpty
			d.Coverage = sensei.Coverage{Sufficient: false, DirectAnchorCount: 1, FileCount: 1, IndexedFileCount: 1}
		}
		return d, nil
	}, []string{a, b}, region)
	if asked != 2 {
		t.Fatalf("the aggregate count stopped the per-file question: %d probe(s)", asked)
	}
	if err != nil || len(got) != 1 || got[0] != b {
		t.Fatalf("a file publishing sufficient=false was hidden by the region's indexed count: unexamined=%v err=%v", got, err)
	}
}

// --- DF-30: coverage-unexamined is per file, settled by canonical authority unit ---

// run7Planned is objective 49 run 7's planned set (2026-10-02, base 1b308d5):
// two record-lock creates granted from store.go, store.go itself, a test, and
// existing command files no derivation anchors.
func run7Planned() []string {
	return []string{"cmd/sensei-code/resume.go", "cmd/sensei-code/main.go", "cmd/sensei-code/commands.go",
		existingS, existingUnix, existingWindows, "internal/session/repair_test.go"}
}

// run7Action is the action routing built for that plan: canonical grants for
// the pair, derived at the pinned world through coverPlannedAtWorld, with the
// prospective authority projected from them by the canonical unit predicate.
func run7Action(t *testing.T, decl []ProspectiveSurface, grants []prospectiveGrant) Action {
	t.Helper()
	_, out := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	return Action{Stage: StageCandidateEdit, Files: run7Planned(),
		DerivedCoverage:      append(out, lockAnchors("cmd/sensei-code/resume.go")...),
		Unexamined:           []string{existingUnix, existingWindows},
		Absent:               []string{existingUnix, existingWindows},
		ProspectiveAuthority: settledProspectiveFiles(decl, grants)}
}

func sameFiles(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// W1 (measured shape). Granted creates covered by store.go, beside planned
// existing files that carry no derived anchor, route past coverage-unexamined
// through the production router. At base this routed to close a gap that
// could never close: every architectural file was asked for an anchor.
func TestDF30W1GrantedCreatesRoutePastCoverageUnexamined(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	a := run7Action(t, existingDeclarations(), grants)
	if !sameFiles(a.ProspectiveAuthority, []string{existingUnix, existingWindows}) {
		t.Fatalf("premise: the canonical projection must settle the granted pair: %v", a.ProspectiveAuthority)
	}
	if got := routeAuthorityForAction(scoped, nil, a); !got.Granted() {
		t.Fatalf("granted creates beside unanchored existing files did not route past coverage-unexamined: %+v", got)
	}
	// The specimen is live: without the recorded authority the same plan opens
	// the gap for exactly the two creates, and their prospective anchors do not
	// close it (no anchor stands in for prospective authority).
	a.ProspectiveAuthority = nil
	got := routeAuthorityForAction(scoped, nil, a)
	if !got.ClosesGap() || got.Gap.Kind != gapCoverageUnexamined || !sameFiles(got.Gap.Scope, []string{existingUnix, existingWindows}) {
		t.Fatalf("without recorded authority the creates must hold the gap, and only they: %+v", got)
	}
}

// W2. A declared create without a matching recorded grant keeps the gap open
// for exactly that create; a mismatched grant settles nothing either, and no
// settled file is reintroduced.
func TestDF30W2ADeclaredButUngrantedCreateKeepsOnlyItsOwnGap(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	var unixOnly []prospectiveGrant
	for _, g := range grants {
		if g.Anchor.File == existingUnix {
			unixOnly = append(unixOnly, g)
		}
	}
	mismatched := existingDeclarations()
	mismatched[1].Dependencies = append(mismatched[1].Dependencies, "strings")
	for name, c := range map[string]struct {
		decl   []ProspectiveSurface
		grants []prospectiveGrant
	}{
		"declared, no recorded grant":          {existingDeclarations(), unixOnly},
		"grant issued for another declaration": {mismatched, grants},
	} {
		t.Run(name, func(t *testing.T) {
			got := routeAuthorityForAction(scoped, nil, run7Action(t, c.decl, c.grants))
			if !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{existingWindows}) {
				t.Fatalf("the gap must stay open for exactly the ungranted create: %+v", got)
			}
		})
	}
}

// W3. A present file the graph never examined, with no other authority, still
// requires its own derivation; a present file positively examined is not asked
// for an anchor because another file is unexamined.
func TestDF30W3AnUnexaminedPresentFileStillNeedsItsDerivation(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	a := run7Action(t, existingDeclarations(), grants)
	const present = "cmd/sensei-code/commands.go"
	a.Unexamined = append(a.Unexamined, present)
	got := routeAuthorityForAction(scoped, nil, a)
	if !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{present}) {
		t.Fatalf("an unexamined present file with no authority must hold the gap alone: %+v", got)
	}
	if strings.Contains(got.Condition, "no prospective grant") {
		t.Fatalf("a present file was sent to prospective admission: %q", got.Condition)
	}
	a.DerivedCoverage = append(a.DerivedCoverage, lockAnchors(present)...)
	if got := routeAuthorityForAction(scoped, nil, a); !got.Granted() {
		t.Fatalf("its own recognised derivation must close it, with main.go still unanchored: %+v", got)
	}
}

// W4. No limit names import --refresh or graph examination for a granted
// create; an ungranted absent create is sent to prospective admission.
func TestDF30W4NoRefreshIsPrescribedForACreate(t *testing.T) {
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	const present = "cmd/sensei-code/commands.go"
	a := run7Action(t, existingDeclarations(), grants[:1])
	granted, ungranted := grants[0].Anchor.File, existingWindows
	if granted == ungranted {
		granted, ungranted = existingWindows, existingUnix
	}
	a.Unexamined = append(a.Unexamined, present)
	gap, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("premise: an ungranted create and an unexamined present file open the gap")
	}
	d := disposeGap(gap, a, "/repo", "github.com/globulario/sensei-code")
	if d.Limit == nil {
		t.Fatalf("the exhausted gap must be a knowledge limit: %+v", d.Routing)
	}
	for _, text := range []string{gap.Condition, d.Routing.Closes, d.Summary, d.Limit.Error()} {
		if strings.Contains(text, granted) {
			t.Fatalf("a granted create is named by the gap: %q", text)
		}
	}
	refresh := d.Routing.Closes[:strings.Index(d.Routing.Closes, "prospective admission")]
	if !strings.Contains(refresh, "import --refresh") || !strings.Contains(refresh, present) || strings.Contains(refresh, ungranted) {
		t.Fatalf("graph examination must be offered for the present file only: %q", d.Routing.Closes)
	}
	if !strings.Contains(d.Routing.Closes, "prospective admission of "+ungranted) || !strings.Contains(gap.Condition, "no prospective grant") {
		t.Fatalf("the ungranted create is not sent to prospective admission: %q / %q", gap.Condition, d.Routing.Closes)
	}
	// Absent alone: no refresh at all.
	only := run7Action(t, existingDeclarations(), nil)
	only.ProspectiveAuthority = []string{granted}
	g, _ := unexaminedCoverageGap(only, blindSpotReading{})
	if d := disposeGap(g, only, "/repo", ""); d.Limit == nil || strings.Contains(d.Routing.Closes, "import --refresh") ||
		strings.Contains(d.Routing.Closes, "graph examination") || strings.Contains(d.Summary, "examine") {
		t.Fatalf("an ungranted create alone was told to close by graph examination: %+v", d)
	}
}

// W5 (cut point). A gap over an absent create; during the closure round the
// matching grant is recorded through the production recorder; the SAME gap is
// re-evaluated from that record and the granted file leaves it before any
// further decision. When it was the last file, the run proceeds.
func TestDF30W5AGrantRecordedDuringClosureSettlesTheSameGap(t *testing.T) {
	e, _ := attemptEngine(t)
	const task = "task-df30-w5"
	id := candidateIdentityFor(prospectiveWorld)
	id.TaskID = task
	if err := id.Save(e.Repo.Root); err != nil {
		t.Fatal(err)
	}
	decl := existingDeclarations()
	d := attemptPlan("record locks", run7Planned()...)
	d.ProspectiveSurfaces = decl
	a, err := e.beginPlanAttempt(task, attemptObjective, d)
	if err != nil || a.World != prospectiveWorld {
		t.Fatalf("premise: the attempt binds the pinned world: %+v %v", a, err)
	}
	if err := e.recordProspectiveGrants(task, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: a.World}); err != nil {
		t.Fatal(err)
	}
	scoped := scopedPreflight(t, neighbourCovered)
	spots := readBlindSpots(scoped.BlindSpots)
	action := run7Action(t, decl, nil)
	action.ProspectiveAuthority = e.recordedProspectiveAuthority(task, decl)
	prior := routeAuthorityForAction(scoped, nil, action)
	if !prior.ClosesGap() || !sameFiles(prior.Gap.Scope, []string{existingUnix, existingWindows}) {
		t.Fatalf("premise: the gap opens over both creates: %+v", prior)
	}
	grants, _ := existingGrants(t, existingPlanned(), decl, existingWorld())
	record := func(world string, gs []prospectiveGrant) []string {
		t.Helper()
		if err := e.recordProspectiveGrants(task, "prospective authority recorded", prospectiveRecord{PlanAttemptID: a.ID, World: world, Grants: gs}); err != nil {
			t.Fatal(err)
		}
		return e.recordedProspectiveAuthority(task, decl)
	}
	// A record at another world settles nothing.
	if got := record("another world", grants); len(got) != 0 {
		t.Fatalf("a grant recorded at another world settled %v", got)
	}
	var unixOnly []prospectiveGrant
	for _, g := range grants {
		if g.Anchor.File == existingUnix {
			unixOnly = append(unixOnly, g)
		}
	}
	action.ProspectiveAuthority = record(a.World, unixOnly)
	still, open := reevaluateGap(prior, action, spots)
	if !open || !sameFiles(still.Gap.Scope, []string{existingWindows}) || still.Gap.World != prior.Gap.World {
		t.Fatalf("re-evaluation must leave exactly the ungranted create in the same gap: %+v", still)
	}
	action.ProspectiveAuthority = record(a.World, grants)
	if r, open := reevaluateGap(prior, action, spots); open {
		t.Fatalf("the last file settled and the stale gap survived: %+v", r)
	}
	if got := routeAuthorityForAction(scoped, nil, action); !got.Granted() {
		t.Fatalf("with every file settled the run must proceed: %+v", got)
	}
	// A record belongs to its attempt: the next attempt holds none of it.
	other := d
	other.Plan = "another plan"
	if _, err := e.beginPlanAttempt(task, attemptObjective, other); err != nil {
		t.Fatal(err)
	}
	if got := e.recordedProspectiveAuthority(task, decl); len(got) != 0 {
		t.Fatalf("another attempt's record settled %v", got)
	}
}

// W6 (control). Prospective authority settles only the create it names: it is
// not laundered onto an unexamined neighbour, which still opens the gap the
// existing invariant demands.
func TestDF30W6AGrantDoesNotCoverItsNeighbour(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	anchored, unexamined := "internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"
	got := routeAuthorityForAction(scoped, nil, Action{Stage: StageCandidateEdit, Files: []string{anchored, unexamined},
		Unexamined: []string{unexamined}, ProspectiveAuthority: []string{anchored}})
	if !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{unexamined}) {
		t.Fatalf("authority over one file covered another: %+v", got)
	}
}

// W7 (control). Two unresolved files, one granted: re-evaluation closes only it.
func TestDF30W7SettlementIsNeitherAllOrNothingNorPlanWide(t *testing.T) {
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	prior, _ := unexaminedCoverageGap(run7Action(t, existingDeclarations(), nil), blindSpotReading{})
	for _, g := range grants {
		other := existingUnix
		if g.Anchor.File == existingUnix {
			other = existingWindows
		}
		r, open := reevaluateGap(prior, run7Action(t, existingDeclarations(), []prospectiveGrant{g}), blindSpotReading{})
		if !open || !sameFiles(r.Gap.Scope, []string{other}) {
			t.Fatalf("granting %s must leave exactly %s: %+v", g.Anchor.File, other, r)
		}
	}
}

// W8 (atomic unit). One malformed production grant in a new-package directory
// leaves the whole directory unsettled; an unrelated existing-package create
// in the same plan stays settled.
func TestDF30W8ANewPackageDirectorySettlesWhole(t *testing.T) {
	lib := prospectiveFor(t, newLibPlanned(), newLibDeclarations(), newPackageAnchors(), newPackageWorld())
	existing, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	decl := append(newLibDeclarations(), existingDeclarations()...)
	if got := settledProspectiveFiles(decl, append(append([]prospectiveGrant(nil), lib...), existing...)); len(got) != 5 {
		t.Fatalf("premise: every valid unit settles: %v", got)
	}
	for name, tamper := range map[string]func([]prospectiveGrant) []prospectiveGrant{
		"a production grant malformed": func(gs []prospectiveGrant) []prospectiveGrant {
			for i := range gs {
				if gs[i].Anchor.File == newLibDir+"/config.go" {
					gs[i].Facts.Imports = nil
				}
			}
			return gs
		},
		"a production grant missing": func(gs []prospectiveGrant) []prospectiveGrant {
			var out []prospectiveGrant
			for _, g := range gs {
				if g.Anchor.File != newLibDir+"/config.go" {
					out = append(out, g)
				}
			}
			return out
		},
	} {
		t.Run(name, func(t *testing.T) {
			bad := tamper(append([]prospectiveGrant(nil), lib...))
			got := settledProspectiveFiles(decl, append(bad, existing...))
			if !sameFiles(got, []string{existingUnix, existingWindows}) {
				t.Fatalf("the new-package unit must settle nothing and the independent creates stay settled: %v", got)
			}
		})
	}
}

// W8 (inherited grouping). A command's library edge inherits the library
// unit: a malformed library grant leaves the command unsettled even though the
// command's own record still names declared, granted library files.
func TestDF30W8ACommandEdgeInheritsItsLibraryUnit(t *testing.T) {
	grants := prospectiveFor(t, edgePlanned(), edgeDeclarations(), newPackageAnchors(), newPackageWorld())
	if g, ok := commandGrant(grants); !ok || g.Edge == nil {
		t.Fatalf("premise: the command is granted its library edge: %+v", grants)
	}
	if got := settledProspectiveFiles(edgeDeclarations(), grants); len(got) != len(edgeDeclarations()) {
		t.Fatalf("premise: every valid unit settles: %v", got)
	}
	for i := range grants {
		if grants[i].Anchor.File == newLibDir+"/config.go" {
			grants[i].Facts.Imports = nil
		}
	}
	if got := settledProspectiveFiles(edgeDeclarations(), grants); len(got) != 0 {
		t.Fatalf("a command settled over a library unit that does not hold: %v", got)
	}
}

// W9 (independent units). Two existing-package creates, one malformed: each is
// decided alone.
func TestDF30W9IndependentCreatesSettleIndependently(t *testing.T) {
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	for i := range grants {
		if grants[i].Anchor.File == existingWindows {
			grants[i].Existing = nil
		}
	}
	if got := settledProspectiveFiles(existingDeclarations(), grants); !sameFiles(got, []string{existingUnix}) {
		t.Fatalf("a malformed create revoked or joined its independent neighbour: %v", got)
	}
}

// W10 (unprobed action). An Action carrying neither Examined nor Unexamined
// data settles nothing by omission; one exact grant leaves exactly the other.
// Examination settles only a file positively present at the pinned world: a
// confirmed-absent create cannot have been examined, and a file of unknown
// presence is not settled by it. Exhausted-gap disposal reads the same way: a
// fresh Action that omits a gap file does not remove it from the gap.
func TestDF30W10OmissionIsNotSettlement(t *testing.T) {
	prior, _ := unexaminedCoverageGap(run7Action(t, existingDeclarations(), nil), blindSpotReading{})
	unprobed := Action{Stage: StageCandidateEdit, Files: run7Planned(), Absent: []string{existingUnix, existingWindows}}
	if r, open := reevaluateGap(prior, unprobed, blindSpotReading{}); !open || !sameFiles(r.Gap.Scope, prior.Gap.Scope) {
		t.Fatalf("missing probe data settled a file: %+v", r)
	}
	absentExamined := unprobed
	absentExamined.Examined = []string{existingUnix}
	if r, open := reevaluateGap(prior, absentExamined, blindSpotReading{}); !open || !sameFiles(r.Gap.Scope, prior.Gap.Scope) {
		t.Fatalf("an examination claimed for a create the pinned world lacks settled it: %+v", r)
	}
	unknown := Action{Stage: StageCandidateEdit, Files: run7Planned(), Examined: []string{existingUnix}}
	if r, open := reevaluateGap(prior, unknown, blindSpotReading{}); !open || !sameFiles(r.Gap.Scope, prior.Gap.Scope) {
		t.Fatalf("an examination of a file of unknown presence settled it: %+v", r)
	}
	presentExamined := Action{Stage: StageCandidateEdit, Files: run7Planned(),
		Present: []string{existingUnix}, Absent: []string{existingWindows}, Examined: []string{existingUnix}}
	if r, open := reevaluateGap(prior, presentExamined, blindSpotReading{}); !open || !sameFiles(r.Gap.Scope, []string{existingWindows}) {
		t.Fatalf("a positive examination of a present file must settle exactly its file: %+v", r)
	}
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	for _, g := range grants {
		if g.Anchor.File != existingWindows {
			continue
		}
		unprobed.ProspectiveAuthority = settledProspectiveFiles(existingDeclarations(), []prospectiveGrant{g})
	}
	if r, open := reevaluateGap(prior, unprobed, blindSpotReading{}); !open || !sameFiles(r.Gap.Scope, []string{existingUnix}) {
		t.Fatalf("one exact grant must leave exactly the other file: %+v", r)
	}

	// Omission from a fresh Action's file list settles nothing at disposal.
	partial := Action{Stage: StageCandidateEdit, Files: []string{existingS, existingWindows}}
	d := disposeGap(prior, partial, "/repo", "")
	if d.Outcome != gapLimited || d.Limit == nil || !sameFiles(d.Routing.Gap.Scope, prior.Gap.Scope) || !sameFiles(d.Limit.Missing, prior.Gap.Scope) {
		t.Fatalf("a gap file omitted from the fresh Action left the exhausted gap: %+v", d)
	}
	withdrawn := disposeGap(prior, Action{Stage: StageCandidateEdit, Files: []string{existingS}}, "/repo", "")
	if withdrawn.Outcome != gapEscalated || withdrawn.Limit != nil || !sameFiles(withdrawn.Routing.Gap.Scope, prior.Gap.Scope) {
		t.Fatalf("a plan naming no gap member must escalate the same, unsettled gap: %+v", withdrawn)
	}
	settled := unprobed
	settled.ProspectiveAuthority = []string{existingUnix, existingWindows}
	if c := disposeGap(prior, settled, "/repo", ""); c.Outcome != gapClosed || c.Limit != nil || c.Routing.RequiresHuman() {
		t.Fatalf("a fully settled gap was escalated or limited by hand: %+v", c)
	}
}

// W11 (routing parity). The same unresolved state through proceed, escalate
// after human authorization, re-evaluation and exhausted-gap disposal yields
// the same typed disposition and the same remaining Scope; and no exhausted
// site converts a gap to a human route by hand.
func TestDF30W11EveryPathReachesTheSameDisposition(t *testing.T) {
	gated := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},`+
		identifiedAuthority+`}`)
	scoped := scopedPreflight(t, neighbourCovered)
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	var unixOnly []prospectiveGrant
	for _, g := range grants {
		if g.Anchor.File == existingUnix {
			unixOnly = append(unixOnly, g)
		}
	}
	a := run7Action(t, existingDeclarations(), unixOnly)
	a.Unexamined = append(a.Unexamined, "cmd/sensei-code/main.go")
	want := []string{"cmd/sensei-code/main.go", existingWindows}
	if d := reconcileCoverageScope(a.unexaminedArchitecturalFiles(), a, derivationUnder(readBlindSpots(scoped.BlindSpots), a.DerivedCoverage)); !sameFiles(d.Unresolved, want) {
		t.Fatalf("premise: the reconciliation leaves the unexamined file and the ungranted create: %+v", d)
	}
	proceed := routeAuthorityForAction(scoped, nil, a)
	human := routeAuthorityForAction(gated, nil, a)
	if !human.RequiresHuman() {
		t.Fatalf("premise: the gate is asked first: %+v", human)
	}
	authorized := afterAuthorization(human, true, a, readBlindSpots(gated.BlindSpots))
	reevaluated, _ := reevaluateGap(proceed, a, readBlindSpots(scoped.BlindSpots))
	exhausted := disposeGap(proceed, a, "/repo", "")
	if exhausted.Limit == nil {
		t.Fatalf("an exhausted coverage-unexamined gap is a knowledge limit: %+v", exhausted.Routing)
	}
	scope := proceed.Gap.Scope
	for name, got := range map[string][]string{
		"post-authorization": authorized.Gap.Scope,
		"re-evaluation":      reevaluated.Gap.Scope,
		"exhausted":          exhausted.Routing.Gap.Scope,
		"limit":              exhausted.Limit.Missing,
	} {
		if !sameFiles(got, scope) {
			t.Fatalf("%s reached Scope %v, proceed reached %v", name, got, scope)
		}
	}
	if authorized.Gap.Kind != proceed.Gap.Kind || authorized.Condition != proceed.Condition {
		t.Fatalf("post-authorization reached a different disposition: %+v vs %+v", authorized, proceed)
	}
	// A fresh Action that omits every probe datum and one gap file reaches
	// the same disposition and Scope: omission is not settlement.
	omitted := Action{Stage: StageCandidateEdit, Files: []string{"cmd/sensei-code/main.go"},
		ProspectiveAuthority: a.ProspectiveAuthority, Absent: []string{existingUnix, existingWindows}}
	if o := disposeGap(proceed, omitted, "/repo", ""); o.Outcome != exhausted.Outcome ||
		!sameFiles(o.Routing.Gap.Scope, scope) || o.Limit == nil || !sameFiles(o.Limit.Missing, scope) {
		t.Fatalf("an Action omitting a gap file reached a different disposition: %+v", o)
	}
	src := rawSource(t, "internal/workflow/engine.go")
	if strings.Contains(src, "stillOpen.Route = RouteHuman") || strings.Count(src, "disposeGap(gap, action,") != 1 {
		t.Fatal("the post-authorization exhausted gap does not take the canonical disposition")
	}
}

// regionUncovered is a classified, authoritative answer whose region coverage
// is not proven: the router's coverage-absent branch.
const regionUncovered = `{"status":"PREFLIGHT_STATUS_EMPTY",` +
	`"coverage":{"sufficient":false,"direct_anchor_count":0,"file_count":1,"indexed_file_count":0},` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	identifiedAuthority + `}`

// regionBlindSpot is a proven region that reports a coverage blind spot: the
// router's coverage-blind-spot branch.
const regionBlindSpot = `{"status":"PREFLIGHT_STATUS_OK",` +
	`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	`"blind_spots":["coverage_insufficient: no direct anchors"],` +
	identifiedAuthority + `}`

// createOnly is the RULING-174 specimen: one create the pinned world lacks,
// granted from store.go, which the plan does not itself name.
func createOnly(t *testing.T, grant bool) Action {
	t.Helper()
	decl := existingDeclarations()[:1]
	grants, out := existingGrants(t, []string{existingUnix}, decl, existingWorld())
	a := Action{Stage: StageCandidateEdit, Files: []string{existingUnix}, DerivedCoverage: out, Absent: []string{existingUnix}}
	if grant {
		a.ProspectiveAuthority = settledProspectiveFiles(decl, grants)
	}
	return a
}

// W1 (region gaps, RULING-174). The same create, granted and absent, is not
// held by the coverage-absent or coverage-blind-spot branch either: both
// reconcile per authority unit before derivation. Without the grant both keep
// it, naming prospective admission and never a graph refresh; a present file
// beside it still needs its own derivation.
func TestDF30W1AGrantedCreateClosesRegionCoverageGaps(t *testing.T) {
	for name, c := range map[string]struct {
		body, kind string
	}{
		"coverage-absent":     {regionUncovered, gapCoverageAbsent},
		"coverage-blind-spot": {regionBlindSpot, gapCoverageBlindSpot},
	} {
		t.Run(name, func(t *testing.T) {
			scoped := scopedPreflight(t, c.body)
			granted := createOnly(t, true)
			if !sameFiles(granted.ProspectiveAuthority, []string{existingUnix}) {
				t.Fatalf("premise: the canonical projection settles the create: %v", granted.ProspectiveAuthority)
			}
			if got := routeAuthorityForAction(scoped, nil, granted); !got.Granted() {
				t.Fatalf("a granted absent create was held by the %s branch: %+v", c.kind, got)
			}
			ungranted := createOnly(t, false)
			got := routeAuthorityForAction(scoped, nil, ungranted)
			if !got.ClosesGap() || got.Gap.Kind != c.kind || !sameFiles(got.Gap.Scope, []string{existingUnix}) {
				t.Fatalf("an ungranted create must hold the %s gap: %+v", c.kind, got)
			}
			if !strings.Contains(got.Condition, "prospective admission") || strings.Contains(got.Condition, "import --refresh") {
				t.Fatalf("the ungranted create is not sent to prospective admission: %q", got.Condition)
			}
			d := disposeGap(got, ungranted, "/repo", "github.com/globulario/sensei-code")
			if d.Outcome != gapEscalated || d.Limit != nil || strings.Contains(d.Routing.Closes, "import --refresh") ||
				!sameFiles(d.Routing.Gap.Scope, got.Gap.Scope) {
				t.Fatalf("the exhausted %s gap prescribed a refresh or lost its Scope: %+v", c.kind, d)
			}
			// A present file beside the granted create: only it is asked, and
			// its own derivation closes it.
			const present = "cmd/sensei-code/main.go"
			mixed := createOnly(t, true)
			mixed.Files = append(mixed.Files, present)
			mixed.Present = []string{present}
			if got := routeAuthorityForAction(scoped, nil, mixed); !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{present}) {
				t.Fatalf("the present file must hold the %s gap alone: %+v", c.kind, got)
			}
			mixed.DerivedCoverage = append(mixed.DerivedCoverage, lockAnchors(present)...)
			if got := routeAuthorityForAction(scoped, nil, mixed); !got.Granted() {
				t.Fatalf("its own derivation must close the %s gap: %+v", c.kind, got)
			}
			// Presence is required: a grant beside a file whose absence was
			// never established, or one the world holds, settles nothing.
			// The create's prospective anchors are withheld so the grant is the
			// only authority asked about.
			for name, a := range map[string]Action{"unknown": createOnly(t, true), "present": createOnly(t, true)} {
				a.Absent, a.DerivedCoverage = nil, nil
				if name == "present" {
					a.Present = []string{existingUnix}
				}
				if got := routeAuthorityForAction(scoped, nil, a); got.Granted() {
					t.Fatalf("a grant over a create of %s presence settled the %s gap: %+v", name, c.kind, got)
				}
			}
		})
	}
}

// W10 (presence). Prospective authority settles a gap file only when the
// pinned world provably lacks it: unknown presence or a present file keeps it.
func TestDF30W10AGrantWithoutConfirmedAbsenceSettlesNothing(t *testing.T) {
	prior, _ := unexaminedCoverageGap(run7Action(t, existingDeclarations(), nil), blindSpotReading{})
	both := []string{existingUnix, existingWindows}
	for name, a := range map[string]Action{
		"unknown presence": {Stage: StageCandidateEdit, Files: run7Planned(), ProspectiveAuthority: both},
		"present":          {Stage: StageCandidateEdit, Files: run7Planned(), ProspectiveAuthority: both, Present: both},
		"contradictory":    {Stage: StageCandidateEdit, Files: run7Planned(), ProspectiveAuthority: both, Present: both, Absent: both},
	} {
		if r, open := reevaluateGap(prior, a, blindSpotReading{}); !open || !sameFiles(r.Gap.Scope, prior.Gap.Scope) {
			t.Fatalf("%s: a grant settled a file not confirmed absent: %+v", name, r)
		}
		if d := disposeGap(prior, a, "/repo", ""); d.Outcome == gapClosed || !sameFiles(d.Routing.Gap.Scope, prior.Gap.Scope) {
			t.Fatalf("%s: disposal settled a file not confirmed absent: %+v", name, d)
		}
	}
}

// W5 (ledger cut point). The engine's ledger, not a local flag, carries the
// transition: after a partial grant the open identity IS the narrowed one and
// the full-scope observation is retired; after the last grant none is open.
// A restarted process that reconstructs the full-scope question from its
// durable deferral reconciles it from the durable grant record before any
// consumer reads it.
func TestDF30W5TheLedgerRetiresTheSupersededGap(t *testing.T) {
	e, _ := attemptEngine(t)
	const task = "task-df30-w5-ledger"
	id := candidateIdentityFor(prospectiveWorld)
	id.TaskID = task
	if err := id.Save(e.Repo.Root); err != nil {
		t.Fatal(err)
	}
	decl := existingDeclarations()
	d := attemptPlan("record locks", run7Planned()...)
	d.ProspectiveSurfaces = decl
	a, err := e.beginPlanAttempt(task, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	record := func(eng *Engine, gs []prospectiveGrant) {
		t.Helper()
		if err := eng.recordProspectiveGrants(task, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: a.World, Grants: gs}); err != nil {
			t.Fatal(err)
		}
	}
	record(e, nil)
	action := run7Action(t, decl, nil)
	action.ProspectiveAuthority = e.recordedProspectiveAuthority(task, decl)
	prior := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, action)
	prior.Gap.World = a.World
	if r := e.observeGap(task, prior); !r.Open() || !sameFiles(prior.Gap.Scope, []string{existingUnix, existingWindows}) {
		t.Fatalf("premise: the full-scope gap is open: %+v", r)
	}
	grants, _ := existingGrants(t, existingPlanned(), decl, existingWorld())
	var unixOnly []prospectiveGrant
	for _, g := range grants {
		if g.Anchor.File == existingUnix {
			unixOnly = append(unixOnly, g)
		}
	}
	reconcile := func(eng *Engine) (AuthorityResolution, bool) {
		t.Helper()
		act := run7Action(t, decl, nil)
		act.ProspectiveAuthority = eng.recordedProspectiveAuthority(task, decl)
		return eng.reconcileOpenGaps(task, a.World, act, blindSpotReading{})
	}
	record(e, unixOnly)
	if r, ok := reconcile(e); !ok || !sameFiles(r.Gap.Scope, []string{existingWindows}) || r.Gap.World != a.World {
		t.Fatalf("partial settlement must leave exactly the ungranted create open: %+v %v", r, ok)
	}
	if r, ok := e.openGap(task, a.World); !ok || !sameFiles(r.Gap.Scope, []string{existingWindows}) {
		t.Fatalf("the ledger still returns the superseded Scope: %+v %v", r, ok)
	}
	// Restart: the full-scope question was deferred durably before the grant.
	full := prior
	full.Route = RouteHuman
	e.preserveQuestion(withAuthorityGap(withPlanAttempt(t.Context(), a.ID), full.Gap), task, full.Condition, "", a.World,
		DeferredAuthority{}.Decision, "deferred", full.Gap.Scope)
	restarted := &Engine{Store: e.Store, SessionID: e.SessionID, Bus: e.Bus, Repo: e.Repo}
	// The restarted routing is of the same canonical attempt, whose grant
	// record is durable.
	if again, err := restarted.beginPlanAttempt(task, attemptObjective, d); err != nil || again.ID != a.ID {
		t.Fatalf("premise: the restarted routing is of the same attempt: %+v %v", again, err)
	}
	// Its routing records the attempt's grant state again, as routePlan does.
	record(restarted, unixOnly)
	if r, ok := restarted.openGap(task, a.World); !ok || !sameFiles(r.Gap.Scope, full.Gap.Scope) {
		t.Fatalf("premise: the restarted ledger reconstructs the deferred full-scope question: %+v %v", r, ok)
	}
	if r, ok := reconcile(restarted); !ok || !sameFiles(r.Gap.Scope, []string{existingWindows}) {
		t.Fatalf("a restarted process restored the superseded full Scope: %+v %v", r, ok)
	}
	if r, ok := restarted.openGap(task, a.World); !ok || !sameFiles(r.Gap.Scope, []string{existingWindows}) {
		t.Fatalf("after restart the ledger returns the superseded Scope: %+v %v", r, ok)
	}
	record(e, grants)
	record(restarted, grants)
	for name, eng := range map[string]*Engine{"live": e, "restarted": restarted} {
		if r, ok := reconcile(eng); ok {
			t.Fatalf("%s: every file settled and a gap is still open: %+v", name, r)
		}
		if r, ok := eng.openGap(task, a.World); ok {
			t.Fatalf("%s: the ledger resurrected a settled gap: %+v", name, r)
		}
	}
}

// W8/W9 (one canonical seam). Admission, restoration and inspection
// (matchGrantsToDeclarations) and routing (settledProspectiveFiles) read one
// validateProspectiveGrants value: a record is admitted exactly when every
// declared unit settles, and no second grant validator exists.
func TestDF30W8RoutingAndAdmissionShareOneValidator(t *testing.T) {
	lib := prospectiveFor(t, newLibPlanned(), newLibDeclarations(), newPackageAnchors(), newPackageWorld())
	existing, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	decl := append(newLibDeclarations(), existingDeclarations()...)
	all := append(append([]prospectiveGrant(nil), lib...), existing...)
	cases := map[string][]prospectiveGrant{"valid": all}
	malformed := append([]prospectiveGrant(nil), all...)
	for i := range malformed {
		if malformed[i].Anchor.File == existingWindows {
			malformed[i].Existing = nil
		}
	}
	cases["one independent create malformed"] = malformed
	cases["one library member missing"] = all[1:]
	for name, gs := range cases {
		v := validateProspectiveGrants(decl, gs)
		settled := v.settled()
		admitted := matchGrantsToDeclarations(decl, gs) == nil
		if admitted != (v.err() == nil) || admitted != (len(settled) == len(decl)) || !sameFiles(settled, settledProspectiveFiles(decl, gs)) {
			t.Fatalf("%s: admission (%v) and routing (%v) disagree on one record", name, admitted, settled)
		}
	}
	src := rawSource(t, "internal/workflow/prospective.go")
	if strings.Count(src, "grantFault(f, d, count[f], byPath, declared)") != 1 ||
		!strings.Contains(src, "return validateProspectiveGrants(declared, grants).err()") ||
		!strings.Contains(src, "return validateProspectiveGrants(declared, grants).settled()") {
		t.Fatal("routing and admission do not read one canonical prospective validation")
	}
}

// W11 (region parity). A region coverage gap reaches the same remaining
// Scope through the router, re-evaluation and exhausted-gap disposal.
func TestDF30W11RegionGapsReachTheSameScopeOnEveryPath(t *testing.T) {
	scoped := scopedPreflight(t, regionUncovered)
	const present = "cmd/sensei-code/main.go"
	a := createOnly(t, true)
	a.Files = append([]string{present}, a.Files...)
	a.Present = []string{present}
	proceed := routeAuthorityForAction(scoped, nil, a)
	if !proceed.ClosesGap() || !sameFiles(proceed.Gap.Scope, []string{present}) {
		t.Fatalf("premise: the router keeps only the present file: %+v", proceed)
	}
	stale := proceed
	stale.Gap.Scope = a.Files
	reevaluated, open := reevaluateGap(stale, a, readBlindSpots(scoped.BlindSpots))
	exhausted := disposeGap(stale, a, "/repo", "")
	if !open || !sameFiles(reevaluated.Gap.Scope, proceed.Gap.Scope) || !sameFiles(exhausted.Routing.Gap.Scope, proceed.Gap.Scope) ||
		exhausted.Outcome != gapEscalated {
		t.Fatalf("re-evaluation %v and disposal %+v disagree with the router %v", reevaluated.Gap.Scope, exhausted, proceed.Gap.Scope)
	}
}

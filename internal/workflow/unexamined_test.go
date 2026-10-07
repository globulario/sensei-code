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

// The gap an unexamined file opens closes only by a recognised derivation over
// THAT file -- never over its neighbour, and never by authority about another
// file.
//
// MIGRATED under DF-30 (objective 59, ruling 178). This test asserted that the
// derivation had to cover EVERY architectural file, so an examined neighbour --
// already governed -- had to acquire a derived anchor because some other file
// was unexamined. That plan-wide requirement is the superseded law: the gap is
// decided over its own members. What it still pins, unchanged: the neighbour's
// derivation does not cover the unexamined file, an unrecognised family covers
// nothing, a present file is not covered by prospective authority (its own or
// unrelated), and a derivation over the unexamined file is what closes it. Both
// planned files exist, and the pinned world says so: a derivation settles only
// a file confirmed present, so every case states it, controls included.
func TestAnUnexaminedPlannedFileIsCoveredOnlyByADerivationOverIt(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	anchored, unexamined := "internal/workflow/engine.go", "internal/workflow/zz_not_in_graph.go"
	planned := []string{anchored, unexamined}

	for name, anchors := range map[string][]CoverageAnchor{
		"a derivation over every planned file":      lockAnchors(planned...),
		"a derivation over the unexamined one only": lockAnchors(unexamined),
	} {
		t.Run(name, func(t *testing.T) {
			closed := routeAuthorityForAction(scoped, nil, Action{
				Stage: StageCandidateEdit, Files: planned, Unexamined: []string{unexamined},
				Present: planned, DerivedCoverage: anchors})
			if !closed.Granted() {
				t.Fatalf("a derivation over the unexamined file must close the gap it opened: %+v", closed)
			}
		})
	}

	for name, action := range map[string]Action{
		"a derivation over the neighbour only": {Present: planned, DerivedCoverage: lockAnchors(anchored)},
		"an unrecognised family over both":     {Present: planned, DerivedCoverage: layeringAnchors(planned...)},
		// A present file is not an absent CREATE, so a prospective unit naming
		// it -- however valid -- does not settle its coverage question.
		"a valid prospective unit over the present unexamined file": {
			Present:     planned,
			Prospective: []prospectiveUnit{{Files: []string{unexamined}, Valid: true}}},
		// An anchor over a file the pinned world confirms absent is no
		// derivation over it, whatever its family.
		"an anchor over the unexamined file confirmed absent": {
			Present: []string{anchored}, Absent: []string{unexamined}, DerivedCoverage: lockAnchors(planned...)},
		// Authority over an unrelated file covers nothing here.
		"a valid prospective unit over an unrelated file": {
			Present:     planned,
			Absent:      []string{"internal/workflow/zz_unrelated_create.go"},
			Prospective: []prospectiveUnit{{Files: []string{"internal/workflow/zz_unrelated_create.go"}, Valid: true}}},
	} {
		t.Run(name, func(t *testing.T) {
			action.Stage, action.Files, action.Unexamined = StageCandidateEdit, planned, []string{unexamined}
			got := routeAuthorityForAction(scoped, nil, action)
			if got.Granted() {
				t.Fatalf("insufficient authority granted an unexamined file: %+v", got)
			}
			if !got.ClosesGap() || got.Gap.Kind != "coverage-unexamined" || len(got.Gap.Scope) != 1 || got.Gap.Scope[0] != unexamined {
				t.Fatalf("the gap must stay open over exactly the unexamined file: %+v", got)
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
	// Both files exist at the pinned world: the gap below is open for want of a
	// derivation, not for want of a presence fact.
	with := Action{Stage: StageCandidateEdit, Files: files, Unexamined: []string{unexamined}, Present: files}

	human := routeAuthorityForAction(gated, nil, with)
	if !human.RequiresHuman() {
		t.Fatalf("the gate must be asked before coverage: %+v", human)
	}
	if got := afterAuthorization(human, false, with, gated); !got.RequiresHuman() {
		t.Fatalf("an unanswered gate was displaced: %+v", got)
	}
	got := afterAuthorization(human, true, with, gated)
	if got.Granted() || got.RequiresHuman() || !got.ClosesGap() || got.Gap.Kind != "coverage-unexamined" {
		t.Fatalf("an authorised consequence admitted an unexamined file: %+v", got)
	}
	closed := with
	closed.DerivedCoverage = lockAnchors(files...)
	if got := afterAuthorization(human, true, closed, gated); !got.RequiresHuman() {
		t.Fatalf("a closed gap displaced the authorised route: %+v", got)
	}
	if got := afterAuthorization(human, true, plannedEdit(files...), gated); !got.RequiresHuman() {
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

// gatedNeighbourCovered is neighbourCovered behind an explicit human approval
// gate: the router asks the gate first and coverage only after the answer.
const gatedNeighbourCovered = `{"status":"PREFLIGHT_STATUS_OK",` +
	`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":1},` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},` +
	identifiedAuthority + `}`

// regionAbsent is a region answer proving no coverage: coverage-absent.
const regionAbsent = `{"status":"PREFLIGHT_STATUS_EMPTY",` +
	`"coverage":{"sufficient":false,"direct_anchor_count":0,"file_count":3,"indexed_file_count":0},` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	identifiedAuthority + `}`

// regionBlindSpot is a covered region whose answer reports a coverage blind
// spot: coverage-blind-spot.
const regionBlindSpot = `{"status":"PREFLIGHT_STATUS_OK",` +
	`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":3,"indexed_file_count":3},` +
	`"blind_spots":["coverage_insufficient: no direct anchors"],` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	identifiedAuthority + `}`

// W3 -- NO WIDENING. A present file the graph has not examined, holding no
// other governed authority, still opens the gap and still needs a derivation
// over itself; a present file the graph examined is not forced to acquire an
// unrelated anchor because another file is unexamined.
func TestDF30W3APresentUnexaminedFileStillNeedsItsOwnDerivation(t *testing.T) {
	scoped := scopedPreflight(t, neighbourCovered)
	governed, unexamined := "internal/workflow/engine.go", "internal/workflow/zz_present_unexamined.go"
	base := Action{Stage: StageCandidateEdit, Files: []string{governed, unexamined},
		Unexamined: []string{unexamined}, Examined: []string{governed}, Present: []string{governed, unexamined}}

	for name, anchors := range map[string][]CoverageAnchor{
		"no derivation":                      nil,
		"a derivation over the governed one": lockAnchors(governed),
		"an unrecognised family over it":     layeringAnchors(unexamined),
	} {
		t.Run(name, func(t *testing.T) {
			action := base
			action.DerivedCoverage = anchors
			got := routeAuthorityForAction(scoped, nil, action)
			if !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{unexamined}) {
				t.Fatalf("a present unexamined file without its own derivation was settled: %+v", got)
			}
		})
	}
	closed := base
	closed.DerivedCoverage = lockAnchors(unexamined)
	if got := routeAuthorityForAction(scoped, nil, closed); !got.Granted() {
		t.Fatalf("its own derivation must settle it without an anchor over the examined file: %+v", got)
	}
}

// W4 -- NO IMPOSSIBLE CLOSURE. The terminal of an exhausted coverage gap never
// names a granted create, and never tells an absent create to close by graph
// examination or import --refresh: an ungranted one is given the
// prospective-surface route. A present unexamined file keeps its examination
// remedy.
func TestDF30W4NoTerminalTellsACreateToCloseByExamination(t *testing.T) {
	decl, grants, out := df30Measured(t)
	ungranted, present := existingThird, "internal/session/zz_present_unexamined.go"
	planned := append(df30Planned(), ungranted, present)
	action := df30Action(out, prospectiveAuthorityUnits(df30Attempt(), decl, planned, df30Recorded(grants)))
	action.Files = planned
	action.Unexamined = append(action.Unexamined, ungranted, present)
	action.Absent = append(action.Absent, ungranted)
	action.Present = append(action.Present, present)

	routing := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, action)
	if !routing.ClosesGap() || !sameFiles(routing.Gap.Scope, []string{ungranted, present}) {
		t.Fatalf("premise: the gap is exactly the ungranted create and the present unexamined file: %+v", routing)
	}
	routing.Gap.World = prospectiveWorld
	e := &Engine{SessionID: "s1"}
	routed, limited := e.disposeUnclosedGap("task-df30", "github.com/globulario/sensei-code", routing, action)
	var limit *knowledgeLimitError
	if !errors.As(limited, &limit) {
		t.Fatalf("an exhausted coverage gap was not a typed knowledge limit: %v", limited)
	}
	terminal := routed.Condition + "\n" + routed.Closes + "\n" + limited.Error()
	for _, granted := range []string{existingUnix, existingWindows, df30Repair} {
		if strings.Contains(terminal, granted) {
			t.Fatalf("the terminal names granted create %s:\n%s", granted, terminal)
		}
	}
	createRemedy, presentRemedy, found := strings.Cut(routed.Closes, "\n")
	if !found || !strings.Contains(createRemedy, ungranted) || !strings.Contains(createRemedy, "prospective_surfaces") {
		t.Fatalf("the ungranted create is not given the prospective-surface route: %q", routed.Closes)
	}
	if strings.Contains(createRemedy, "import --refresh") || strings.Contains(createRemedy, "graph examination") {
		t.Fatalf("an absent create was told to close by graph examination: %q", createRemedy)
	}
	if !strings.Contains(presentRemedy, present) || !strings.Contains(presentRemedy, "import --refresh") || strings.Contains(presentRemedy, ungranted) {
		t.Fatalf("the present unexamined file lost its own examination remedy: %q", presentRemedy)
	}

	// UNKNOWN PRESENCE. The pinned world was read, but its read of one member
	// was never answered: it is neither confirmed present nor confirmed absent.
	// Neither closure route may be prescribed for it -- each assumes a fact
	// nobody read -- so it is given only the fail-closed instruction to
	// establish its presence first, and the gap stays open over it.
	unknown := "internal/session/zz_presence_unknown.go"
	unread := df30Action(out, prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), df30Recorded(grants)))
	unread.Files = append(df30Planned(), unknown)
	unread.Unexamined = append(unread.Unexamined, unknown)
	gap := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, unread)
	if !gap.ClosesGap() || !sameFiles(gap.Gap.Scope, []string{unknown}) {
		t.Fatalf("premise: the gap is exactly the member of unknown presence: %+v", gap)
	}
	gap.Gap.World = prospectiveWorld
	routed, limited = e.disposeUnclosedGap("task-df30", "github.com/globulario/sensei-code", gap, unread)
	if !errors.As(limited, &limit) || !sameFiles(limit.Missing, []string{unknown}) {
		t.Fatalf("an unresolved member of unknown presence was not kept as the limit: %v", limited)
	}
	if !strings.Contains(routed.Closes, unknown) || !strings.Contains(routed.Closes, "presence is unknown") {
		t.Fatalf("the member of unknown presence is not told to establish its presence: %q", routed.Closes)
	}
	for _, route := range []string{"prospective_surfaces", "import --refresh", "graph examination"} {
		if strings.Contains(routed.Closes, route) {
			t.Fatalf("a member of unknown presence was given a closure route (%q) that assumes a fact nobody read: %q", route, routed.Closes)
		}
	}

	// ALL MEMBERS UNKNOWN. The pinned world was not read for any member: no
	// file is confirmed present or absent. Every other input would settle the
	// gap -- a recognised derivation over each unexamined member and the valid
	// recorded prospective units that grant them -- and none of it may: a
	// derivation settles only a file confirmed present, a grant only a create
	// confirmed absent. Unknown presence is never decided plan-wide.
	blind := df30Action(append(out, lockAnchors(existingUnix, existingWindows)...),
		prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), df30Recorded(grants)))
	blind.Present, blind.Absent = nil, nil
	open := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, blind)
	if open.Granted() || !open.ClosesGap() || open.Gap.Kind != gapCoverageUnexamined ||
		!sameFiles(open.Gap.Scope, []string{existingUnix, existingWindows}) {
		t.Fatalf("members of unknown presence were settled by derivation or prospective authority: %+v", open)
	}
	open.Gap.World = prospectiveWorld
	routed, limited = e.disposeUnclosedGap("task-df30", "github.com/globulario/sensei-code", open, blind)
	if !errors.As(limited, &limit) || !sameFiles(limit.Missing, []string{existingUnix, existingWindows}) {
		t.Fatalf("the members of unknown presence were not kept as the limit: %v", limited)
	}
	if !strings.Contains(routed.Closes, "presence is unknown") ||
		!strings.Contains(routed.Closes, existingUnix) || !strings.Contains(routed.Closes, existingWindows) {
		t.Fatalf("the members of unknown presence are not told to establish their presence: %q", routed.Closes)
	}
	for _, route := range []string{"prospective_surfaces", "import --refresh", "graph examination"} {
		if strings.Contains(routed.Closes, route) {
			t.Fatalf("with no member's presence read, the remedy prescribed %q: %q", route, routed.Closes)
		}
	}

	// NO FABRICATED ANCHOR. An anchor carried onto a create the pinned world
	// confirms absent -- the prospective projection objective 49 run 7 read as
	// coverage -- is no derivation over it: the create is settled by its grant
	// or not at all.
	fabricated := df30Action(append(append([]CoverageAnchor(nil), out...), lockAnchors(existingUnix, existingWindows)...), nil)
	if got := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, fabricated); !got.ClosesGap() ||
		!sameFiles(got.Gap.Scope, []string{existingUnix, existingWindows}) {
		t.Fatalf("an anchor over a confirmed-absent create settled it without a grant: %+v", got)
	}
}

// W7 -- PER-UNIT, NOT ALL-OR-NOTHING. Two unresolved creates, only one of which
// receives a matching recorded grant: re-evaluating the gap closes exactly the
// granted one, and the other remains the named gap.
func TestDF30W7OnlyTheGrantedMemberLeavesTheGap(t *testing.T) {
	decl, grants, out := df30Measured(t)
	partial := prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), df30Recorded(withoutGrantFor(grants, existingWindows)))
	// The payload the router writes for this gap under this preflight.
	sem := coverageSemantics(gapCoverageUnexamined, readBlindSpots(scopedPreflight(t, neighbourCovered).BlindSpots).Coverage, "")
	prior := Routing{Route: RouteCloseGap, Gap: GapIdentity{Kind: gapCoverageUnexamined,
		Scope: []string{existingUnix, existingWindows}, World: prospectiveWorld, Semantics: &sem}}

	const taskID = "task-df30-w7"
	e := df30Engine(t, taskID)
	if r := e.observeGap(taskID, prior); !r.Open() {
		t.Fatalf("premise: the two-file gap is open: %+v", r)
	}
	action := df30Action(out, partial)
	fresh := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, action)
	fresh.Gap.World = prospectiveWorld
	d := architectureDecision{Mode: ModeModify, Files: df30Planned()}
	ledger := ledgerOf(e, taskID)
	// A re-plan routes the narrowing: a different plan attempt.
	df30NextAttempt(t, e, taskID, "pa-df30-w7-replan")
	routed, _ := e.registerRouting(taskID, prospectiveWorld, fresh, action, scopedPreflight(t, neighbourCovered), d, true)
	// One identity: the narrowing rewrote the gap that opened, in place.
	assertOneEpisode(t, e, taskID, prior.Gap, ledger)
	if routed.Gap.Key() != prior.Gap.Key() {
		t.Fatalf("the routing over the remaining member is not the identity that opened: %q", routed.Gap.Key())
	}
	disposition := e.gapResolutions(taskID).byKey[prior.Gap.Key()].Disposition
	if disposition == nil || !sameFiles(disposition.Unresolved, []string{existingWindows}) ||
		disposition.Settled[existingUnix] != settledByProspectiveGrant {
		t.Fatalf("re-evaluation did not close exactly the granted member: %+v", disposition)
	}
	open, ok := e.openGap(taskID, prospectiveWorld)
	if !ok || !sameFiles(open.Gap.Scope, []string{existingWindows}) || strings.Contains(open.Routing.Condition, existingUnix) ||
		open.Gap.Key() != prior.Gap.Key() {
		t.Fatalf("the ungranted member is not the remaining named gap: %+v %v", open, ok)
	}
}

// W11 -- ROUTING PARITY. One unresolved coverage state yields one typed
// disposition, and one remaining scope, through every route that reaches it:
// the proceed route, the post-authorization re-evaluation an escalation's
// answer takes, the exhausted-gap disposal and the same-identity re-evaluation
// of a prior gap.
func TestDF30W11EveryRouteReachesTheSameDisposition(t *testing.T) {
	decl, grants, out := df30Measured(t)
	present := "internal/session/zz_present_unexamined.go"
	units := prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), df30Recorded(withoutGrantFor(grants, existingWindows)))
	action := df30Action(out, units)
	action.Files = append(df30Planned(), present)
	action.Unexamined = append(action.Unexamined, present)
	action.Present = append(action.Present, present)
	want := []string{existingWindows, present}

	proceed := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, action)
	gated := scopedPreflight(t, gatedNeighbourCovered)
	human := routeAuthorityForAction(gated, nil, action)
	if !human.RequiresHuman() {
		t.Fatalf("premise: the gate is asked first: %+v", human)
	}
	authorized := afterAuthorization(human, true, action, gated)
	for name, r := range map[string]Routing{"proceed": proceed, "post-authorization": authorized} {
		if !r.ClosesGap() || r.Gap.Kind != gapCoverageUnexamined || !sameFiles(r.Gap.Scope, want) {
			t.Fatalf("%s reached a different disposition: %+v", name, r)
		}
	}
	if proceed.Condition != authorized.Condition {
		t.Fatalf("one disposition rendered two conditions:\n%q\n%q", proceed.Condition, authorized.Condition)
	}
	proceed.Gap.World = prospectiveWorld
	_, limited := (&Engine{SessionID: "s1"}).disposeUnclosedGap("task-df30", "", proceed, action)
	var limit *knowledgeLimitError
	if !errors.As(limited, &limit) || !sameFiles(limit.Missing, want) {
		t.Fatalf("the exhausted-gap disposal recomputed a different set: %v", limited)
	}
	prior := disposeCoverageGap(gapCoverageUnexamined, []string{existingUnix, existingWindows, present}, action, RequirementUnqualified)
	if !sameFiles(prior.Unresolved, want) {
		t.Fatalf("re-evaluating a prior identity reached a different set: %+v", prior)
	}

	// DISPOSAL CONSUMES THE STORED DISPOSITION. A registered episode is
	// disposed of over exactly its stored unresolved members: an action that
	// no longer names one cannot drop it there, because which members the plan
	// still depends on is decided only by the typed re-evaluation, together
	// with the episode's scope and condition.
	const taskID = "task-df30-w11"
	e := df30Engine(t, taskID)
	scoped := scopedPreflight(t, neighbourCovered)
	d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: action.Files}
	registered, r := e.registerRouting(taskID, prospectiveWorld, proceed, action, scoped, d, true)
	if !r.Open() || !sameFiles(registered.Gap.Scope, want) {
		t.Fatalf("premise: the episode is registered open over %v: %+v", want, r)
	}
	ledger := ledgerOf(e, taskID)
	omitting := action
	omitting.Files = nil
	for _, f := range action.Files {
		if f != present {
			omitting.Files = append(omitting.Files, f)
		}
	}
	routed, limited := e.disposeUnclosedGap(taskID, "", registered, omitting)
	if !errors.As(limited, &limit) || !sameFiles(limit.Missing, want) || !sameFiles(routed.Gap.Scope, want) ||
		routed.Condition != registered.Condition {
		t.Fatalf("the disposal dropped a stored unresolved member the action omits: %+v %v", routed, limited)
	}
	// A re-plan that withdraws the member is re-evaluated first, by a different
	// plan attempt: the SAME episode narrows, scope and condition together, and
	// only then does the disposal stop reporting it.
	df30NextAttempt(t, e, taskID, "pa-df30-w11-replan")
	withdrawn := omitting
	withdrawn.Unexamined = []string{existingUnix, existingWindows}
	withdrawn.Present = df30Action(out, nil).Present
	narrowedPlan := d
	narrowedPlan.Files = withdrawn.Files
	fresh := routeAuthorityForAction(scoped, nil, withdrawn)
	fresh.Gap.World = prospectiveWorld
	narrowed, _ := e.registerRouting(taskID, prospectiveWorld, fresh, withdrawn, scoped, narrowedPlan, true)
	current := assertOneEpisode(t, e, taskID, proceed.Gap, ledger)
	if !sameFiles(current.Gap.Scope, []string{existingWindows}) || current.Disposition == nil ||
		!sameFiles(current.Disposition.Withdrawn, []string{present}) || strings.Contains(current.Routing.Condition, present) ||
		narrowed.Gap.Key() != proceed.Gap.Key() {
		t.Fatalf("the withdrawal was not decided by the episode's typed re-evaluation: %+v", current)
	}
	if _, limited := e.disposeUnclosedGap(taskID, "", narrowed, withdrawn); !errors.As(limited, &limit) ||
		!sameFiles(limit.Missing, []string{existingWindows}) {
		t.Fatalf("the disposal did not consume the narrowed disposition: %v", limited)
	}

	// The region kinds take the SAME typed exhausted-gap disposition, on the
	// proceed/escalate disposal and the post-authorization one alike: an
	// unresolved member is a knowledge limit over exactly the disposition's
	// unresolved set, never a manual conversion into a human question, and the
	// absent ungranted create is given the prospective-surface route.
	for kind, body := range map[string]string{gapCoverageAbsent: regionAbsent, gapCoverageBlindSpot: regionBlindSpot} {
		t.Run("exhausted "+kind, func(t *testing.T) {
			scoped := scopedPreflight(t, body)
			region := action
			region.Unexamined, region.Examined = nil, nil
			gap := routeAuthorityForAction(scoped, nil, region)
			spots := readBlindSpots(scoped.BlindSpots)
			disposition := disposeCoverageGap(kind, region.architecturalFiles(), region, gapRequirement(spots.Coverage))
			if !gap.ClosesGap() || gap.Gap.Kind != kind || !sameFiles(gap.Gap.Scope, disposition.Unresolved) ||
				!sameFiles(disposition.Unresolved, []string{df30Main, df30Commands, existingWindows, present}) {
				t.Fatalf("premise: the %s gap is the disposition's unresolved set: %+v %+v", kind, gap, disposition)
			}
			gap.Gap.World = prospectiveWorld
			e := &Engine{SessionID: "s1"}
			for name, dispose := range map[string]func() (Routing, error){
				"proceed/escalate":   func() (Routing, error) { return e.disposeUnclosedGap("task-df30", "", gap, region) },
				"post-authorization": func() (Routing, error) { return e.disposeExhaustedGap("task-df30", "", gap, region) },
			} {
				routed, limited := dispose()
				var limit *knowledgeLimitError
				if routed.Route == RouteHuman || !errors.As(limited, &limit) {
					t.Fatalf("%s: an unresolved %s gap was converted into a human question: %+v %v", name, kind, routed, limited)
				}
				if !sameFiles(limit.Missing, disposition.Unresolved) || !sameFiles(routed.Gap.Scope, disposition.Unresolved) ||
					routed.Condition != gap.Condition {
					t.Fatalf("%s: the disposal did not keep the disposition's scope and condition: %+v %+v", name, routed, limit)
				}
				createRemedy, presentRemedy, _ := strings.Cut(routed.Closes, "\n")
				if !strings.Contains(createRemedy, existingWindows) || !strings.Contains(createRemedy, "prospective_surfaces") ||
					strings.Contains(createRemedy, "import --refresh") {
					t.Fatalf("%s: the absent ungranted create is not given the prospective-surface route: %q", name, routed.Closes)
				}
				if strings.Contains(presentRemedy, existingWindows) || !strings.Contains(presentRemedy, present) {
					t.Fatalf("%s: the present members' remedy is not exactly theirs: %q", name, presentRemedy)
				}
				for _, granted := range []string{existingUnix, df30Repair} {
					if strings.Contains(routed.Closes+limited.Error(), granted) {
						t.Fatalf("%s: the terminal names granted create %s", name, granted)
					}
				}
			}
		})
	}
}

// W12 -- EXAMINATION OWNS ONE QUESTION. A per-file examination settles a member
// of coverage-unexamined, whose question it answers; it never settles a member
// of coverage-absent or coverage-blind-spot, whose question it does not.
func TestDF30W12ExaminationDoesNotSettleARegionGap(t *testing.T) {
	member := "internal/workflow/zz_examined_member.go"
	examined := Action{Stage: StageCandidateEdit, Files: []string{member},
		Examined: []string{member}, Present: []string{member}}

	if d := disposeCoverageGap(gapCoverageUnexamined, []string{member}, examined, RequirementUnqualified); d.Open() {
		t.Fatalf("control: examination of a present file must settle coverage-unexamined: %+v", d)
	}
	// Examination is evidence about a file confirmed present. The same fact over
	// a file confirmed absent, or one whose presence was never established,
	// settles nothing.
	for name, presence := range map[string]Action{
		"confirmed absent":       {Absent: []string{member}},
		"presence unestablished": {},
	} {
		presence.Examined = []string{member}
		if d := disposeCoverageGap(gapCoverageUnexamined, []string{member}, presence, RequirementUnqualified); !d.Open() {
			t.Fatalf("examination of a file %s settled coverage-unexamined: %+v", name, d)
		}
	}
	for kind, body := range map[string]string{gapCoverageAbsent: regionAbsent, gapCoverageBlindSpot: regionBlindSpot} {
		t.Run(kind, func(t *testing.T) {
			d := disposeCoverageGap(kind, []string{member}, examined, RequirementUnqualified)
			if !sameFiles(d.Unresolved, []string{member}) {
				t.Fatalf("a per-file examination settled a %s member: %+v", kind, d)
			}
			got := routeAuthorityForAction(scopedPreflight(t, body), nil, examined)
			if !got.ClosesGap() || got.Gap.Kind != kind || !sameFiles(got.Gap.Scope, []string{member}) {
				t.Fatalf("the router let examination settle a %s member: %+v", kind, got)
			}
		})
	}
}

// W13 -- A NARROWED REGION GAP IS RE-RENDERED. Once a member of a region gap is
// lawfully settled, the gap's scope, its condition and every question rendered
// from it name only the members that remain -- through the router and through
// the same-identity re-evaluation of the prior, wider gap alike.
func TestDF30W13ANarrowedRegionConditionIsReRendered(t *testing.T) {
	decl, grants, out := df30Measured(t)
	units := prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), df30Recorded(grants))
	settled := []string{existingUnix, existingWindows}
	want := []string{df30Main, df30Commands}
	for kind, body := range map[string]string{gapCoverageAbsent: regionAbsent, gapCoverageBlindSpot: regionBlindSpot} {
		t.Run(kind, func(t *testing.T) {
			scoped := scopedPreflight(t, body)
			region := df30Action(out, units)
			region.Unexamined, region.Examined = nil, nil
			unsettled := region
			unsettled.Prospective = nil

			prior := routeAuthorityForAction(scoped, nil, unsettled)
			if !prior.ClosesGap() || prior.Gap.Kind != kind || len(prior.Gap.Scope) != 4 {
				t.Fatalf("premise: the region gap holds the creates and the unanchored files: %+v", prior)
			}
			fresh := routeAuthorityForAction(scoped, nil, region)
			prior.Gap.World, fresh.Gap.World = prospectiveWorld, prospectiveWorld

			const taskID = "task-df30-w13"
			e := df30Engine(t, taskID)
			e.observeGap(taskID, prior)
			ledger := ledgerOf(e, taskID)
			df30NextAttempt(t, e, taskID, "pa-df30-w13-replan")
			d := architectureDecision{Mode: ModeModify, Files: df30Planned()}
			e.reconcileCoverageGaps(taskID, prospectiveWorld, Routing{Route: RouteHuman}, region, scoped, d)
			narrowed, ok := e.openGap(taskID, prospectiveWorld)
			if !ok {
				t.Fatal("the narrowed gap is not open")
			}
			assertOneEpisode(t, e, taskID, prior.Gap, ledger)
			if narrowed.Gap.Key() != prior.Gap.Key() {
				t.Fatalf("the re-rendered gap is not the identity that opened: %q", narrowed.Gap.Key())
			}
			for name, r := range map[string]Routing{"router": fresh, "re-evaluation": narrowed.Routing} {
				if !r.ClosesGap() || !sameFiles(r.Gap.Scope, want) {
					t.Fatalf("%s: the narrowed scope is not the remaining members: %+v", name, r.Gap)
				}
				question := escalationCondition(r)
				for _, f := range want {
					if !strings.Contains(r.Condition, f) || !strings.Contains(question, f) {
						t.Fatalf("%s: the condition does not name remaining member %s: %q", name, f, r.Condition)
					}
				}
				for _, f := range settled {
					if strings.Contains(r.Condition, f) || strings.Contains(question, f) {
						t.Fatalf("%s: the condition still describes settled member %s: %q", name, f, r.Condition)
					}
				}
			}
			if fresh.Condition != narrowed.Routing.Condition {
				t.Fatalf("one narrowed disposition rendered two conditions:\n%q\n%q", fresh.Condition, narrowed.Routing.Condition)
			}
		})
	}

	// DERIVATION-ONLY NARROWING. The mechanism that settled a member never
	// decides whether the condition is truthful: a region gap narrowed by a
	// derivation alone is re-rendered exactly as one narrowed by a grant.
	derived, remaining := "internal/workflow/zz_derived_member.go", "internal/workflow/zz_unresolved_member.go"
	for kind, body := range map[string]string{gapCoverageAbsent: regionAbsent, gapCoverageBlindSpot: regionBlindSpot} {
		t.Run(kind+"/derivation only", func(t *testing.T) {
			scoped := scopedPreflight(t, body)
			planned := []string{derived, remaining}
			unsettled := Action{Stage: StageCandidateEdit, Files: planned, Present: planned}
			settled := unsettled
			settled.DerivedCoverage = lockAnchors(derived)

			prior := routeAuthorityForAction(scoped, nil, unsettled)
			if !prior.ClosesGap() || prior.Gap.Kind != kind || !sameFiles(prior.Gap.Scope, planned) {
				t.Fatalf("premise: the region gap holds both members: %+v", prior)
			}
			fresh := routeAuthorityForAction(scoped, nil, settled)
			prior.Gap.World, fresh.Gap.World = prospectiveWorld, prospectiveWorld

			const taskID = "task-df30-w13-derivation"
			e := df30Engine(t, taskID)
			e.observeGap(taskID, prior)
			ledger := ledgerOf(e, taskID)
			df30NextAttempt(t, e, taskID, "pa-df30-w13-replan")
			d := architectureDecision{Mode: ModeModify, Files: planned}
			e.reconcileCoverageGaps(taskID, prospectiveWorld, Routing{Route: RouteHuman}, settled, scoped, d)
			narrowed, ok := e.openGap(taskID, prospectiveWorld)
			if !ok {
				t.Fatal("the narrowed gap is not open")
			}
			assertOneEpisode(t, e, taskID, prior.Gap, ledger)
			if narrowed.Gap.Key() != prior.Gap.Key() {
				t.Fatalf("the re-rendered gap is not the identity that opened: %q", narrowed.Gap.Key())
			}
			for name, r := range map[string]Routing{"router": fresh, "re-evaluation": narrowed.Routing} {
				question := escalationCondition(r)
				if !r.ClosesGap() || !sameFiles(r.Gap.Scope, []string{remaining}) ||
					!strings.Contains(r.Condition, remaining) || !strings.Contains(question, remaining) ||
					strings.Contains(r.Condition, derived) || strings.Contains(question, derived) {
					t.Fatalf("%s: a derivation-narrowed %s gap does not describe exactly its remaining member: %+v", name, kind, r)
				}
			}
			if fresh.Condition != narrowed.Routing.Condition {
				t.Fatalf("one narrowed disposition rendered two conditions:\n%q\n%q", fresh.Condition, narrowed.Routing.Condition)
			}
		})
	}
}

// perFileSenseiScript is a Sensei MCP answering every per-file preflight in the
// generation identifiedAuthority names: examined (its own coverage proven),
// except for the files UNEXAMINED_ARMS lists, which answer unexamined.
const perFileSenseiScript = `
LC_ALL=C; export LC_ALL
reply() { printf 'Content-Length: %d\r\n\r\n%s' "${#1}" "$1"; }
while :; do
	len=
	while IFS= read -r line; do
		line=$(printf %s "$line" | tr -d '\r')
		[ -z "$line" ] && break
		case "$line" in Content-Length:*) len=$(printf %s "${line#Content-Length:}" | tr -dc 0-9) ;; esac
	done
	[ -n "$len" ] || exit 0
	body=$(dd bs=1 count="$len" 2>/dev/null)
	case "$body" in '{"jsonrpc":"2.0","id":'*) ;; *) continue ;; esac
	rest=${body#'{"jsonrpc":"2.0","id":'}
	id=${rest%%,*}
	rest=${rest#*,}
	case "$rest" in
	'"method":"initialize"'*)
		result='{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"per-file-stub","version":"0"}}' ;;
UNEXAMINED_ARMS
	*'"name":"awareness_preflight"'*)
		result='{"content":[{"type":"text","text":"examined"}],"structuredContent":{"status":"PREFLIGHT_STATUS_OK","coverage":{"sufficient":true,"direct_anchor_count":1,"file_count":1,"indexed_file_count":1},"authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT","graph_build_commit":"fac399f8225f","source_repo_commit":"f56f5a305798"}}}' ;;
	*)
		result='{"content":[{"type":"text","text":"nothing"}],"structuredContent":{}}' ;;
	esac
	reply "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":$result}"
done
`

// perFileSensei starts perFileSenseiScript, answering unexamined for exactly
// the files named: the per-file probe the post-authorization continuation
// (afterHumanAuthorization) runs against a real MCP client.
func perFileSensei(t *testing.T, unexamined ...string) *sensei.Client {
	t.Helper()
	var arms strings.Builder
	for _, f := range unexamined {
		arms.WriteString("\t*'\"files\":[\"" + f + "\"]'*)\n\t\tresult='{\"content\":[{\"type\":\"text\",\"text\":\"unexamined\"}],\"structuredContent\":{\"status\":\"PREFLIGHT_STATUS_EMPTY\",\"coverage\":{\"sufficient\":false,\"direct_anchor_count\":0,\"file_count\":1,\"indexed_file_count\":0},\"authority\":{\"authoritative\":true,\"graph_freshness_state\":\"GRAPH_FRESHNESS_STATE_CURRENT\",\"seed_state\":\"SEED_STATE_CURRENT\",\"graph_build_commit\":\"fac399f8225f\",\"source_repo_commit\":\"f56f5a305798\"}}}' ;;\n")
	}
	script := strings.Replace(perFileSenseiScript, "UNEXAMINED_ARMS", arms.String(), 1)
	sc, err := sensei.Start(t.Context(), t.TempDir(), "sh", []string{"-c", script})
	if err != nil {
		t.Fatalf("start the per-file Sensei stub: %v", err)
	}
	t.Cleanup(func() { sc.Close() })
	return sc
}

// df30ProductionScript is a Sensei MCP whose preflight answers are read from
// STATE on every request: a single-file probe of a path listed in
// STATE/examined or STATE/unexamined answers that, and any other preflight --
// the scoped region question -- answers STATE/region.json.
const df30ProductionScript = `
LC_ALL=C; export LC_ALL
reply() { printf 'Content-Length: %d\r\n\r\n%s' "${#1}" "$1"; }
while :; do
	len=
	while IFS= read -r line; do
		line=$(printf %s "$line" | tr -d '\r')
		[ -z "$line" ] && break
		case "$line" in Content-Length:*) len=$(printf %s "${line#Content-Length:}" | tr -dc 0-9) ;; esac
	done
	[ -n "$len" ] || exit 0
	body=$(dd bs=1 count="$len" 2>/dev/null)
	case "$body" in '{"jsonrpc":"2.0","id":'*) ;; *) continue ;; esac
	rest=${body#'{"jsonrpc":"2.0","id":'}
	id=${rest%%,*}
	rest=${rest#*,}
	case "$rest" in
	'"method":"initialize"'*)
		result='{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"df30-stub","version":"0"}}' ;;
	*'"name":"awareness_preflight"'*)
		result=$(cat 'STATE/region.json')
		for kind in examined unexamined; do
			while IFS= read -r f; do
				[ -n "$f" ] || continue
				case "$body" in *"\"files\":[\"$f\"]"*) result=$(cat "STATE/$kind.json") ;; esac
			done < "STATE/$kind"
		done ;;
	*)
		result='{"content":[{"type":"text","text":"nothing"}],"structuredContent":{}}' ;;
	esac
	reply "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":$result}"
done
`

const (
	df30ExaminedProbe   = `{"content":[{"type":"text","text":"examined"}],"structuredContent":{"status":"PREFLIGHT_STATUS_OK","coverage":{"sufficient":true,"direct_anchor_count":1,"file_count":1,"indexed_file_count":1},"authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT","graph_build_commit":"fac399f8225f","source_repo_commit":"f56f5a305798"}}}`
	df30UnexaminedProbe = `{"content":[{"type":"text","text":"unexamined"}],"structuredContent":{"status":"PREFLIGHT_STATUS_EMPTY","coverage":{"sufficient":false,"direct_anchor_count":0,"file_count":1,"indexed_file_count":0},"authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT","graph_build_commit":"fac399f8225f","source_repo_commit":"f56f5a305798"}}}`
)

// df30Sensei starts df30ProductionScript over a df30Production's state: the
// scoped and per-file preflights the production routing asks.
func df30Sensei(t *testing.T, state string) *sensei.Client {
	t.Helper()
	sc, err := sensei.Start(t.Context(), t.TempDir(), "sh", []string{"-c", strings.ReplaceAll(df30ProductionScript, "STATE", state)})
	if err != nil {
		t.Fatalf("start the Sensei stub: %v", err)
	}
	t.Cleanup(func() { sc.Close() })
	return sc
}

package workflow

import (
	"strings"
	"testing"
)

// The exact strings the live graph produced, so the classifier is pinned to the
// vocabulary it was measured against rather than to a paraphrase of it. Counts
// are from 135 tracked .go files against graph def94857.
func TestClassifyTheMeasuredBlindSpotVocabulary(t *testing.T) {
	cases := []struct {
		spot string
		want blindSpotKind
	}{
		// PREFLIGHT_STATUS_OK — every one of these is a property of knowledge
		// the graph HAS. severity=critical fires BECAUSE the graph knows
		// something important.
		{"file path under high-risk directory", blindSpotConsequence},
		{"anchor with severity=critical", blindSpotConsequence},
		{"anchored entity in security/auth/rbac/pki/jwt/cert namespace", blindSpotConsequence},

		// PREFLIGHT_STATUS_EMPTY — the graph reports it has nothing.
		{"graph indexes this area but no anchored rules apply to the request", blindSpotCoverage},
		{"coverage_insufficient: no direct anchors and no indexed files — a pattern match is guidance, not coverage", blindSpotCoverage},

		// PREFLIGHT_STATUS_DEGRADED.
		{"this is NOT proof of safety — the graph has no facts about this file", blindSpotCoverage},
	}
	for _, tc := range cases {
		if got := classifyBlindSpot(tc.spot); got != tc.want {
			t.Errorf("classify(%q) = %s, want %s", tc.spot, got, tc.want)
		}
	}
}

// The one string in the measured vocabulary that names BOTH a high-risk
// directory and an absence of facts. It must read as coverage: the risk wording
// describes where the hole is, and the hole is still the finding.
//
// Matching consequence first would classify the clearest coverage gap in the
// whole vocabulary as a consequence signal, and send 25 DEGRADED files to a
// human as though the graph had judged them rather than never looked.
func TestAHighRiskPathWithNoFactsIsACoverageGap(t *testing.T) {
	const spot = "high_risk_path_no_direct_anchors: file is under a high-risk directory but no awareness " +
		"anchors apply — graph has no facts about this file; treat as unknown, read source directly"
	if got := classifyBlindSpot(spot); got != blindSpotCoverage {
		t.Fatalf("got %s, want coverage — order of the marker lists is load-bearing", got)
	}
}

// The zero value fails closed. A blind spot nobody has classified must not
// become bounded work by default, or every future addition to Sensei's
// vocabulary becomes silent autonomy — the exact shape of "failure to retrieve
// knowledge is not permission to experiment".
func TestUnrecognisedBlindSpotsFailClosed(t *testing.T) {
	for _, spot := range []string{
		"some future condition nobody has classified yet",
		"",
		"   ",
	} {
		if got := classifyBlindSpot(spot); got != blindSpotUnrecognised {
			t.Errorf("classify(%q) = %s, want unrecognised", spot, got)
		}
	}

	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"blind_spots":["some future condition nobody has classified yet"],`+healthyAuthority+`}`)
	got := routeAuthorityForAction(scoped, nil, plannedEdit())
	if !got.RequiresHuman() {
		t.Fatalf("an unclassified blind spot did not fail closed: %+v", got)
	}
	if got.ClosesGap() || got.Granted() {
		t.Fatalf("an unclassified blind spot was treated as bounded or grantable: %+v", got)
	}
}

// A coverage blind spot on an otherwise-OK preflight is bounded work, not an
// owner. It is still not a grant.
func TestCoverageBlindSpotRoutesToBoundedWork(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"blind_spots":["graph indexes this area but no anchored rules apply to the request"],`+healthyAuthority+`}`)
	got := routeAuthorityForAction(scoped, nil, plannedEdit())
	if !got.ClosesGap() {
		t.Fatalf("got %+v", got)
	}
	if got.Granted() {
		t.Fatal("a coverage gap must never be a grant")
	}
	if !strings.Contains(got.Condition, "missing coverage") {
		t.Fatalf("condition does not name the gap: %q", got.Condition)
	}
}

// Consequence signals are ASSESSED, not routed.
//
// An earlier version of this test asserted they always stop at a human, with a
// comment saying that was the declared question rather than this change. The
// question has since been answered: dq.consequence_blind_spot_authority gained
// a fourth alternative — a consequence signal neither grants nor escalates, it
// requires the consequence boundary of the proposed action to be established —
// and that is what the router now does.
//
// So the same signal reaches different routes depending on the action, which is
// the whole point: severity and path say LOOK HARDER HERE, not WHO DECIDES.
func TestConsequenceSignalsAreAssessedAgainstTheAction(t *testing.T) {
	for _, spot := range []string{
		"file path under high-risk directory",
		"anchor with severity=critical",
		"anchored entity in security/auth/rbac/pki/jwt/cert namespace",
	} {
		body := `{"status":"PREFLIGHT_STATUS_OK",
			"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
			"blind_spots":["` + spot + `"],` + healthyAuthority + `}`
		scoped := scopedPreflight(t, body)

		// Editing in a disposable worktree: bounded, so the technical lane runs.
		if got := routeAuthorityForAction(scoped, nil, Action{Stage: StageCandidateEdit}); !got.Granted() {
			t.Errorf("%q at the edit stage: %+v", spot, got)
		}
		// Publishing the same thing: not bounded.
		if got := routeAuthorityForAction(scoped, nil, Action{Stage: StagePublish}); !got.RequiresHuman() {
			t.Errorf("%q at the publish stage: %+v", spot, got)
		}
		// No stage stated: nobody knows, and that is not a risk verdict.
		if got := routeAuthorityForAction(scoped, nil, Action{}); got.Route != RouteCannotEstablish {
			t.Errorf("%q with no stage: %+v", spot, got)
		}
	}
}

// A mixed reading is a coverage gap first. Closing what is knowable comes
// before asking anyone: the consequence signal may not even survive the reread.
func TestCoverageWinsOverConsequenceInAMixedReading(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"blind_spots":["file path under high-risk directory","coverage_insufficient: no direct anchors and no indexed files"],`+
		healthyAuthority+`}`)
	if got := routeAuthorityForAction(scoped, nil, plannedEdit()); !got.ClosesGap() {
		t.Fatalf("got %+v", got)
	}
}

// An unrecognised spot beats both, because failing closed only means anything
// when it survives company.
func TestUnrecognisedBeatsEveryOtherReading(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"blind_spots":["coverage_insufficient: no direct anchors","brand new condition"],`+healthyAuthority+`}`)
	if got := routeAuthorityForAction(scoped, nil, plannedEdit()); !got.RequiresHuman() {
		t.Fatalf("got %+v", got)
	}
}

// An approval gate outranks a coverage gap. Closing knowledge cannot buy
// autonomy over a change class Sensei says needs approval.
func TestAnApprovalGateIsNotClosableByEvidence(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},
		"blind_spots":["coverage_insufficient: no direct anchors"],`+healthyAuthority+`}`)
	got := routeAuthorityForAction(scoped, nil, plannedEdit())
	if got.ClosesGap() {
		t.Fatalf("a gated change class was reduced to bounded work: %+v", got)
	}
	if !got.RequiresHuman() {
		t.Fatalf("got %+v", got)
	}
}

// A graph that cannot vouch for itself is still not a knowledge gap. It is a
// broken governance surface, and no amount of evidence-gathering fixes it.
func TestAnUncertifiableGraphIsNeverABoundedGap(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",
		"authority":{"verdict":"AUTHORITY_VERDICT_NOT_AUTHORITATIVE","freshness":{"state":"GRAPH_FRESHNESS_STATE_STALE"}}}`)
	got := routeAuthorityForAction(scoped, nil, plannedEdit())
	if got.Route != RouteCannotEstablish {
		t.Fatalf("got %+v", got)
	}
}

// A gap gets one round to close. A second round on the identical condition
// means the first produced nothing the router could see, and repeating it burns
// provider calls to re-derive the same non-answer.
func TestOneConditionGetsOneClosureRound(t *testing.T) {
	e := &Engine{}
	const cond = "graph coverage is absent for the planned files"
	if !e.spendClosure("task-1", cond) {
		t.Fatal("the first attempt at a gap was refused")
	}
	if e.spendClosure("task-1", cond) {
		t.Fatal("the same gap got a second round; an unclosed gap must escalate, not loop")
	}
}

// The budget is per condition, not per task. One run may legitimately meet
// several different gaps, and spending one budget across all of them would send
// the second gap straight to a human because the first one used the attempts.
func TestClosureBudgetIsPerConditionAndPerTask(t *testing.T) {
	e := &Engine{}
	if !e.spendClosure("task-1", "gap A") || !e.spendClosure("task-1", "gap B") {
		t.Fatal("a second, different gap in the same task was refused its own round")
	}
	if !e.spendClosure("task-2", "gap A") {
		t.Fatal("a different task was refused a round for the same condition")
	}
	if e.spendClosure("task-1", "gap A") {
		t.Fatal("budgets leaked across conditions")
	}
}

// One gap class closes with no write path at all, and it is the one the
// architect stage can actually do.
//
// An inferred premise is the plan telling us nothing checked something. Closing
// it means going and checking — reading the source — which a read-only role can
// do in full. The claim then carries source="repository" and the gap is gone,
// with no proposal written and no graph mutation, so the admission question
// (dq.closure_knowledge_admission) does not arise for this class at all.
//
// The other class, absent coverage, cannot close this way: establishing durable
// coverage means writing something down, and that is the open question.
func TestAVerifiedPremiseClosesItsOwnGapWithNoWritePath(t *testing.T) {
	const body = `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` + healthyAuthority + `}`
	scoped := scopedPreflight(t, body)
	edit := Action{Stage: StageCandidateEdit, Files: []string{"internal/event/bus.go"}}

	inferred := []Claim{{
		Statement: "no other caller depends on this signature",
		About:     "internal/event", Source: "inference",
	}}
	before := routeAuthorityForAction(scoped, inferred, edit)
	if !before.ClosesGap() {
		t.Fatalf("an unverified premise is bounded epistemic work: %+v", before)
	}

	// The architect reads the callers and comes back with the same premise,
	// now checked. Nothing was written anywhere.
	verified := []Claim{{
		Statement: "no other caller depends on this signature (grep over internal/: bus.Publish has three callers, all in internal/workflow, none taking its address)",
		About:     "internal/event", Source: "repository",
	}}
	after := routeAuthorityForAction(scoped, verified, edit)
	if !after.Granted() {
		t.Fatalf("a verified premise did not close its gap: %+v", after)
	}

	// And the check is on the SOURCE, not on how convincing the prose is. A
	// longer inference is still an inference.
	dressed := []Claim{{
		Statement: "I am confident, having considered it carefully, that no other caller depends on this signature",
		About:     "internal/event", Source: "inference",
	}}
	if got := routeAuthorityForAction(scoped, dressed, edit); !got.ClosesGap() {
		t.Fatalf("a more eloquent inference was accepted as verification: %+v", got)
	}
}

// A machine-derived fact closes a coverage gap — and only where a derivation
// succeeded in this world over these files.
//
// The list arrives from the caller because revalidation reads the repository
// and this package is pure. What the router must never do is treat the mere
// EXISTENCE of a stored recipe as coverage; it never sees one.
func TestDerivedCoverageClosesAGapOnlyWhereItWasDerived(t *testing.T) {
	const empty = `{"status":"PREFLIGHT_STATUS_EMPTY",` +
		`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
		healthyAuthority + `}`
	scoped := scopedPreflight(t, empty)
	planned := []string{"internal/event/bus.go"}

	// No derived coverage: still a bounded knowledge gap.
	bare := routeAuthorityForAction(scoped, nil, Action{Stage: StageCandidateEdit, Files: planned})
	if !bare.ClosesGap() {
		t.Fatalf("an uncovered region did not route to bounded work: %+v", bare)
	}

	// Derived coverage for exactly the planned file: the gap is closed, and the
	// action is then assessed like any other.
	covered := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: lockAnchors(planned...)})
	if covered.ClosesGap() {
		t.Fatalf("a derived fact did not close the coverage gap: %+v", covered)
	}
	if !covered.Granted() {
		t.Fatalf("after closure the bounded edit was not granted: %+v", covered)
	}

	// Partial coverage is not coverage. A plan touching a file no derivation
	// looked at is one Sensei cannot speak for.
	partial := routeAuthorityForAction(scoped, nil, Action{
		Stage:           StageCandidateEdit,
		Files:           []string{"internal/event/bus.go", "internal/event/unseen.go"},
		DerivedCoverage: lockAnchors(planned...)})
	if !partial.ClosesGap() {
		t.Fatalf("partial derived coverage was accepted as coverage: %+v", partial)
	}

	// An empty plan cannot be covered by anything.
	none := routeAuthorityForAction(scoped, nil, Action{Stage: StageCandidateEdit, DerivedCoverage: lockAnchors(planned...)})
	if !none.ClosesGap() {
		t.Fatalf("a plan naming no files was treated as covered: %+v", none)
	}
}

// Derived coverage closes a KNOWLEDGE gap. It does not clear an approval gate,
// and it does not survive an unrecognised blind spot.
func TestDerivedCoverageDoesNotBuyConsequenceAuthority(t *testing.T) {
	planned := []string{"internal/event/bus.go"}
	gated := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},`+
		healthyAuthority+`}`)
	got := routeAuthorityForAction(gated, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: lockAnchors(planned...)})
	if !got.RequiresHuman() {
		t.Fatalf("derived coverage cleared an approval gate: %+v", got)
	}
}

// lockAnchors is derived coverage from the committed lock-discipline family.
//
// Spelled out rather than defaulted, because the family is now what decides
// whether the coverage resolves anything: a test that wrote `DerivedCoverage:
// planned` was asserting subject overlap, which is the adversary this slice
// refuses. See relevance.go and TestAWideTrueIrrelevantDerivationClosesNothing.
func lockAnchors(files ...string) []CoverageAnchor {
	var out []CoverageAnchor
	for _, f := range files {
		out = append(out, CoverageAnchor{File: f, Requirement: RequirementLockDiscipline,
			Describe: "field_access_under_lock(Bus.subs under Bus.mu)"})
	}
	return out
}

// The benign state: the graph HAS facts here and none raised a category.
//
// Measured on internal/ghbridge/snapshot.go — PREFLIGHT_STATUS_OK, LOW_RISK,
// blast=local, APPROVAL_GATE_NONE, 2 direct anchors — where this single blind
// spot matched no marker, read as unrecognised, and escalated the run to a
// human over a risk channel that had already said no approval was needed.
func TestTheBenignStateIsReadAsNoSignalNotAsUnknown(t *testing.T) {
	const measured = "anchors present, no high-risk category fired"

	if got := classifyBlindSpot(measured); got != blindSpotNoSignal {
		t.Fatalf("classifyBlindSpot(%q) = %v, want no-signal; the most reassuring "+
			"string in the vocabulary must not produce the strongest stop", measured, got)
	}
	r := readBlindSpots([]string{measured})
	if len(r.Unrecognised) != 0 {
		t.Errorf("a recognised benign state was still carried as unrecognised: %v", r.Unrecognised)
	}
	if len(r.NoSignal) != 1 {
		t.Fatalf("NoSignal = %v, want the one measured phrasing", r.NoSignal)
	}
	if len(r.Coverage) != 0 || len(r.Consequence) != 0 {
		t.Errorf("benign state leaked into another kind: coverage=%v consequence=%v",
			r.Coverage, r.Consequence)
	}
}

// The repair must not become a fuzzy match. Exact normalised equality only:
// a string that merely RESEMBLES the benign phrasing is still unknown, and
// unknown still fails closed.
func TestOnlyTheExactBenignPhrasingIsRecognised(t *testing.T) {
	for _, near := range []string{
		"anchors present",
		"no high-risk category fired",
		"anchors present, no high-risk category fired, but the graph is stale",
		"NO ANCHORS present, no high-risk category fired",
		"anchors absent, no high-risk category fired",
		"anchors present; no high-risk category fired",
	} {
		if got := classifyBlindSpot(near); got == blindSpotNoSignal {
			t.Errorf("classifyBlindSpot(%q) = no-signal; only the exact measured "+
				"phrasing may be recognised, or an unread string acquires a meaning "+
				"by resembling one that has been", near)
		}
	}
}

// Whitespace and case are normalisation, not a different string.
func TestTheBenignPhrasingSurvivesNormalisation(t *testing.T) {
	for _, same := range []string{
		"  anchors present, no high-risk category fired  ",
		"Anchors Present, No High-Risk Category Fired",
	} {
		if got := classifyBlindSpot(same); got != blindSpotNoSignal {
			t.Errorf("classifyBlindSpot(%q) = %v, want no-signal", same, got)
		}
	}
}

// The failing-closed default is untouched: anything still unread escalates,
// and it beats a benign reading in a mixed set.
func TestUnknownStillFailsClosedBesideABenignSpot(t *testing.T) {
	r := readBlindSpots([]string{
		"anchors present, no high-risk category fired",
		"some future sensei phrasing nobody has classified",
	})
	if len(r.Unrecognised) != 1 {
		t.Fatalf("Unrecognised = %v, want the unread string to survive", r.Unrecognised)
	}
	if len(r.NoSignal) != 1 {
		t.Fatalf("NoSignal = %v, want the benign spot still read", r.NoSignal)
	}
}

// A DEGRADED answer carrying "anchors present" contradicts itself, and a
// self-contradicting instrument is not one to reason from. The degraded path's
// behaviour is unchanged by this repair.
func TestABenignSpotDoesNotMakeADegradedAnswerCoverageShaped(t *testing.T) {
	r := readBlindSpots([]string{
		"coverage_insufficient: no direct anchors and no indexed files",
		"anchors present, no high-risk category fired",
	})
	if r.degradedIsCoverageShaped() {
		t.Error("a degraded answer carrying a benign spot was read as coverage-shaped; " +
			"the rule is that EVERY spot is a recognised coverage marker")
	}
}

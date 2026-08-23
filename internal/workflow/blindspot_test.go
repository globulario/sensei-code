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
	got := routeAuthority(scoped, nil)
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
	got := routeAuthority(scoped, nil)
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

// Consequence signals still stop at a human. This slice corrects what the
// escalation SAYS, not who owns it: deferring them to the risk gate would make
// 22 of the 26 measured OK files grantable, which is a policy choice about who
// may edit high-risk paths unattended and is declared rather than taken.
func TestConsequenceSignalsStillStopAtAHumanForNow(t *testing.T) {
	for _, spot := range []string{
		"file path under high-risk directory",
		"anchor with severity=critical",
		"anchored entity in security/auth/rbac/pki/jwt/cert namespace",
	} {
		scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
			"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
			"blind_spots":["`+spot+`"],`+healthyAuthority+`}`)
		got := routeAuthority(scoped, nil)
		if !got.RequiresHuman() {
			t.Fatalf("%q no longer stops at a human: %+v — that is the declared question, not this change", spot, got)
		}
		// The correction that IS made: the condition stops calling strong
		// knowledge a blind spot.
		if !strings.Contains(got.Condition, "knowledge the graph holds") {
			t.Errorf("%q: condition still describes knowledge as a gap: %q", spot, got.Condition)
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
	if got := routeAuthority(scoped, nil); !got.ClosesGap() {
		t.Fatalf("got %+v", got)
	}
}

// An unrecognised spot beats both, because failing closed only means anything
// when it survives company.
func TestUnrecognisedBeatsEveryOtherReading(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"blind_spots":["coverage_insufficient: no direct anchors","brand new condition"],`+healthyAuthority+`}`)
	if got := routeAuthority(scoped, nil); !got.RequiresHuman() {
		t.Fatalf("got %+v", got)
	}
}

// An approval gate outranks a coverage gap. Closing knowledge cannot buy
// autonomy over a change class Sensei says needs approval.
func TestAnApprovalGateIsNotClosableByEvidence(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},
		"blind_spots":["coverage_insufficient: no direct anchors"],`+healthyAuthority+`}`)
	got := routeAuthority(scoped, nil)
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
	got := routeAuthority(scoped, nil)
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

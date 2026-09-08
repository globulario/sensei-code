package workflow

import (
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
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

// A refusal says what it RESTS ON. Two stops arrive in the same shape and mean
// opposite things: "somebody must weigh this" and "something could not be
// seen". Both cost a person's attention; only the first is protecting anything.
func TestARefusalSaysWhatItRestsOn(t *testing.T) {
	if got := BasisUnclassified.String(); got != "unclassified" {
		t.Fatalf("zero value = %q", got)
	}
	// The zero value must read as protective. An unlabelled stop filed as a
	// closable gap would route a human-owned decision to a derivation.
	if !(Routing{}).ProtectsValue() {
		t.Error("an unclassified stop was read as a knowledge limit; silence is not " +
			"evidence that nothing was being protected")
	}
	if !(Routing{Basis: BasisProtectsValue}).ProtectsValue() {
		t.Error("a value-protecting stop was not read as one")
	}
	if (Routing{Basis: BasisLacksKnowledge}).ProtectsValue() {
		t.Error("a knowledge limit was read as protecting a value")
	}
}

// A knowledge-limited stop must arrive with its remedy, not only its symptom.
// The whole cost of the unclassified kind was that it looked principled and
// nobody could tell what would close it.
func TestAKnowledgeLimitedStopNamesWhatWouldCloseIt(t *testing.T) {
	source, err := os.ReadFile("authority.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(source)
	// Every site that declares BasisLacksKnowledge on a RouteHuman stop should
	// also say what closes it; a limit reported without a remedy is the shape the
	// classification exists to remove.
	for _, marker := range []string{
		`Closes:    "a change-risk classification for this region`,
		`Closes: "a reading for that blind-spot phrasing`,
		`Closes:    "a certifiable graph generation`,
	} {
		if !strings.Contains(src, marker) {
			t.Errorf("a knowledge-limited stop does not name its remedy: %s", marker)
		}
	}
}

// The load-bearing constraint: this classification is DESCRIPTIVE. A basis that
// could change a route would be a classifier that widens the router, which is
// exactly what a refusal-classifier must never become.
func TestTheBasisNeverDecidesARoute(t *testing.T) {
	source, err := os.ReadFile("authority.go")
	if err != nil {
		t.Fatal(err)
	}
	// String() switches on the basis to RENDER it, which decides nothing. Skip
	// that one function rather than weaken the rule everywhere else.
	var inRenderer bool
	for i, line := range strings.Split(string(source), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func (b RefusalBasis) String()") {
			inRenderer = true
		} else if inRenderer && trimmed == "}" {
			inRenderer = false
			continue
		}
		if inRenderer || strings.HasPrefix(trimmed, "//") {
			continue
		}
		// A basis appearing in a condition is a basis that can steer control
		// flow. Assignment (`Basis:`) and the reader method are the only
		// legitimate uses inside this file.
		if (strings.HasPrefix(trimmed, "if ") || strings.HasPrefix(trimmed, "switch ") ||
			strings.HasPrefix(trimmed, "case ")) &&
			(strings.Contains(trimmed, "Basis") || strings.Contains(trimmed, "ProtectsValue()")) {
			t.Errorf("authority.go:%d reads the refusal basis in a control-flow decision: %q\n"+
				"the classification must describe a stop, never cause one", i+1, trimmed)
		}
	}
}

// A classification nobody reads is not a classification. The basis must reach
// the block an operator -- or an agent -- actually sees, beside the route that
// already told them who decides.
func TestTheRenderedStatementCarriesTheBasisAndItsRemedy(t *testing.T) {
	obj := Objective{Text: "repair the thing", Provenance: SubmittedUnattended}
	bounded := ConsequenceAssessment{Result: ConsequenceBounded, Boundary: "the candidate worktree"}

	knowledge := StateAuthority(obj, nil, bounded, Routing{
		Route:  RouteHuman,
		Basis:  BasisLacksKnowledge,
		Closes: "a reading for that blind-spot phrasing",
	}, architectureDecision{}).Render()

	if !strings.Contains(knowledge, "basis: lacks-knowledge") {
		t.Errorf("a knowledge-limited stop did not say so where it is read:\n%s", knowledge)
	}
	if !strings.Contains(knowledge, "closes: a reading for that blind-spot phrasing") {
		t.Errorf("a knowledge-limited stop did not carry its remedy:\n%s", knowledge)
	}

	value := StateAuthority(obj, nil, bounded, Routing{
		Route: RouteHuman,
		Basis: BasisProtectsValue,
	}, architectureDecision{}).Render()

	if !strings.Contains(value, "basis: protects-value") {
		t.Errorf("a value-protecting stop did not say so:\n%s", value)
	}
	// A value-protecting stop has no remedy to offer, and must not invent one.
	if strings.Contains(value, "closes:") {
		t.Errorf("a value-protecting stop offered a remedy; nothing closes a decision "+
			"somebody has to make:\n%s", value)
	}

	// Both stops route identically. The basis is the only thing that separates
	// "weigh this" from "I could not see", which is the whole point.
	if !strings.Contains(knowledge, "routed: human-authority-required") ||
		!strings.Contains(value, "routed: human-authority-required") {
		t.Error("the two stops no longer share a route; this test is not comparing what it thinks")
	}
}

// The zero value's READER-VISIBLE meaning, through the same path the journal
// reader consumes: StateAuthority(...).Render() becomes the Summary of a
// SourceSystem/Status event, which is the "Authority for this plan, by lane."
// block an operator or an agent actually reads.
//
// This is the case that had no test, and it is exactly the case whose rendered
// form was ambiguous: ProtectsValue() enforces the protective default, but a
// reader of the text cannot call it, and "unclassified" alone reads as UNKNOWN
// -- the permissive reading, and the opposite of what is enforced.
func TestAnUnclassifiedBasisRendersItsEnforcedPosture(t *testing.T) {
	rendered := StateAuthority(
		Objective{Text: "repair the thing", Provenance: SubmittedUnattended},
		nil,
		ConsequenceAssessment{Result: ConsequenceBounded, Boundary: "the candidate worktree"},
		Routing{Route: RouteHuman}, // Basis deliberately left at its zero value
		architectureDecision{},
	).Render()

	// Carried into the event exactly as the engine does it, so what is asserted
	// is the string the journal prints rather than an intermediate value.
	journalLine := event.New("sess", "t1", event.SourceSystem, event.Status, rendered, nil).Summary

	if !strings.Contains(journalLine, "basis: unclassified (treated as protects value)") {
		t.Fatalf("the zero value does not tell a reader how it is enforced:\n%s", journalLine)
	}
	// The absence of a classification must survive into the record. Rendering
	// it as protects-value would erase the difference between a stop nobody
	// labelled and one deliberately labelled protective.
	if !strings.Contains(journalLine, "unclassified") {
		t.Error("the record no longer shows that no classification was supplied")
	}
	// Nothing closes a decision somebody has to make.
	if strings.Contains(journalLine, "closes:") {
		t.Errorf("an unclassified stop offered a remedy:\n%s", journalLine)
	}
	// The enforced posture in the text must agree with the code that enforces it.
	if !(Routing{Route: RouteHuman}).ProtectsValue() {
		t.Error("the rendered posture and ProtectsValue() disagree about the zero value")
	}
}

// The last wire: production must put the RENDERED authority statement into the
// event, not something else.
//
// A source check, and it stays one for the same reason the resolver
// installation is pinned that way — an emission that was replaced cannot be
// observed by calling the function that is no longer called. The test above
// proves what Render() produces and that an event carries it; it constructs
// its own event, so it keeps passing if engine.go stops emitting the statement
// entirely.
//
// That gap is not hypothetical. Replacing the emission with routing.Condition
// leaves the whole suite green, which means the basis line could vanish from
// the journal with nothing to notice.
func TestTheEngineEmitsTheRenderedAuthorityStatement(t *testing.T) {
	source, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	args, ok := authorityStatementSummaryArgs(t, "engine.go", string(source))
	if !ok {
		t.Fatal("the engine no longer emits the rendered authority statement as the " +
			"summary of a SourceSystem/Status event; the basis line reaches no reader, " +
			"and every test about its content still passes")
	}
	// The statement must be built from the same inputs the route was. Compared
	// as canonically printed expressions, so the pin survives reformatting but
	// still fails if an argument is dropped, reordered or substituted.
	want := []string{"e.objective(taskID)", "d.Claims", "AssessConsequences(action)", "routing", "d"}
	if len(args) != len(want) {
		t.Fatalf("StateAuthority takes %d arguments, want %d: %v", len(args), len(want), args)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("StateAuthority argument %d = %q, want %q", i, args[i], want[i])
		}
	}
}

// authorityStatementSummaryArgs finds, in src, an event.New call whose source
// and kind are SourceSystem/Status and whose SUMMARY argument is a
// StateAuthority(...).Render() call, and returns that call's arguments as
// canonically printed source.
//
// STRUCTURE, not layout. The predecessor of this helper asserted
// `strings.Contains(src, "event.SourceSystem, event.Status,\n\t\tStateAuthority(")`
// — a literal newline and two tabs embedded in a semantic pin. gofmt happens to
// produce that shape today, so it passed; extracting the arguments to a
// variable, or any future change to wrapping, would have failed the test for a
// reason unrelated to what it protects (#161). Matching the AST pins the same
// coupling and is indifferent to how the call is wrapped.
//
// The summary position is the whole point. Keeping the StateAuthority call but
// moving it into the payload, or changing the event kind, leaves the basis line
// out of the journal while every rendering test still passes — so both are
// checked here and both have negative controls in
// TestTheAuthorityStatementPinRejects.
func authorityStatementSummaryArgs(t *testing.T, filename, src string) ([]string, bool) {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, 0)
	if err != nil {
		t.Fatalf("parsing %s: %v", filename, err)
	}

	var args []string
	var found bool
	ast.Inspect(file, func(n ast.Node) bool {
		if found {
			return false
		}
		call, isCall := n.(*ast.CallExpr)
		if !isCall || !isSelector(call.Fun, "event", "New") || len(call.Args) < 5 {
			return true
		}
		// event.New(session, task, source, kind, summary, payload)
		if !isSelector(call.Args[2], "event", "SourceSystem") || !isSelector(call.Args[3], "event", "Status") {
			return true
		}
		render, isCall := call.Args[4].(*ast.CallExpr)
		if !isCall {
			return true
		}
		sel, isSel := render.Fun.(*ast.SelectorExpr)
		if !isSel || sel.Sel.Name != "Render" {
			return true
		}
		state, isCall := sel.X.(*ast.CallExpr)
		if !isCall {
			return true
		}
		if ident, isIdent := state.Fun.(*ast.Ident); !isIdent || ident.Name != "StateAuthority" {
			return true
		}
		for _, a := range state.Args {
			args = append(args, printExpr(t, fset, a))
		}
		found = true
		return false
	})
	return args, found
}

// isSelector reports whether e is exactly pkg.name.
func isSelector(e ast.Expr, pkg, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// printExpr renders one expression in canonical gofmt form, so comparison is
// against structure rather than the whitespace the file happened to carry.
func printExpr(t *testing.T, fset *token.FileSet, e ast.Expr) string {
	t.Helper()
	var b strings.Builder
	if err := printer.Fprint(&b, fset, e); err != nil {
		t.Fatalf("printing expression: %v", err)
	}
	return b.String()
}

// TestTheAuthorityStatementPinRejects is the falsifiability half of the pin
// above. A source check that cannot fail is decoration, and the failure it
// guards against is specifically SILENT: every rendering test keeps passing
// while the basis line stops reaching the journal.
//
// The first case is the #161 regression itself — same call, different wrapping.
// The predecessor assertion failed on it; this one must not.
func TestTheAuthorityStatementPinRejects(t *testing.T) {
	const preamble = "package workflow\n\nfunc f() {\n"
	cases := []struct {
		name  string
		body  string
		match bool
	}{
		{
			name: "the shape engine.go carries today",
			body: `	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
		StateAuthority(e.objective(taskID), d.Claims, AssessConsequences(action), routing, d).Render(), nil))`,
			match: true,
		},
		{
			// The layout dependency #161 reported. Behaviour identical, wrapping
			// different. A pin that fails here fails on correct changes.
			name:  "same call on one line",
			body:  `	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, StateAuthority(e.objective(taskID), d.Claims, AssessConsequences(action), routing, d).Render(), nil))`,
			match: true,
		},
		{
			name: "arguments wrapped one per line",
			body: `	e.emit(event.New(
		e.SessionID,
		taskID,
		event.SourceSystem,
		event.Status,
		StateAuthority(
			e.objective(taskID),
			d.Claims,
			AssessConsequences(action),
			routing,
			d,
		).Render(),
		nil,
	))`,
			match: true,
		},
		{
			// Statement still built, still rendered — but buried in the payload,
			// so the journal summary no longer shows it.
			name: "statement moved out of the summary into the payload",
			body: `	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status, "authority",
		map[string]any{"statement": StateAuthority(e.objective(taskID), d.Claims, AssessConsequences(action), routing, d).Render()}))`,
			match: false,
		},
		{
			// Same summary, different kind: it stops being the status event a
			// reader of the journal is looking at.
			name: "event kind is no longer Status",
			body: `	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.AgentStarted,
		StateAuthority(e.objective(taskID), d.Claims, AssessConsequences(action), routing, d).Render(), nil))`,
			match: false,
		},
		{
			// The regression that motivated the original pin: the emission is
			// replaced by something cheaper and every other test stays green.
			name: "summary is a different expression entirely",
			body: `	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
		routing.Condition, nil))`,
			match: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, ok := authorityStatementSummaryArgs(t, "synthetic.go", preamble+c.body+"\n}\n")
			if ok != c.match {
				t.Errorf("matched = %v, want %v", ok, c.match)
			}
		})
	}
}

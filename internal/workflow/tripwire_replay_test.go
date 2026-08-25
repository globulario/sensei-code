package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// The graduation test, replayed offline.
//
// testdata/live_preflight_probe.json is a real measurement: every tracked .go
// file in this repository (excluding testdata/ and .pb.go), probed against live
// graph def94857 through `sensei preflight -json`. Replaying it through the
// router turns the coverage tripwire from a number somebody quotes into a
// regression that fails when the split changes.
//
// It is deliberately NOT a target to optimise. The point is that a future
// change which quietly moves files from `human` to `granted` has to come here
// and say so, in a diff, with a reason.
type probeRow struct {
	File       string   `json:"file"`
	Status     string   `json:"status"`
	BlindSpots []string `json:"blind_spots"`
	Gate       string   `json:"gate"`
	Blast      string   `json:"blast"`
	// Coverage is what the graph PUBLISHED about these files, captured in a
	// second probe of the same 135.
	//
	// The original capture recorded only the status, which was enough while the
	// router inferred coverage from it. Once the router read the coverage
	// evidence instead, a specimen without that evidence could not exercise the
	// thing under test -- it would have replayed green while proving nothing,
	// which is the same false-green shape as the stale binary that answered
	// UNKNOWN to everything.
	Coverage struct {
		Sufficient        bool `json:"sufficient"`
		DirectAnchorCount int  `json:"direct_anchor_count"`
		IndexedFileCount  int  `json:"indexed_file_count"`
	} `json:"coverage"`
}

func loadProbe(t *testing.T) []probeRow {
	t.Helper()
	b, err := os.ReadFile("testdata/live_preflight_probe.json")
	if err != nil {
		t.Fatal(err)
	}
	var rows []probeRow
	if err := json.Unmarshal(b, &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

// probeBody renders one probed row as the preflight result it came from.
func probeBody(r probeRow) string {
	spots, _ := json.Marshal(r.BlindSpots)
	return fmt.Sprintf(`{"status":%q,"blind_spots":%s,`+
		`"coverage":{"sufficient":%t,"direct_anchor_count":%d,"indexed_file_count":%d},`+
		`"change_risk":{"blast_radius":%q,"approval_gate":%q},`+healthyAuthority+`}`,
		r.Status, spots, r.Coverage.Sufficient, r.Coverage.DirectAnchorCount,
		r.Coverage.IndexedFileCount, r.Blast, r.Gate)
}

// replayOne routes a single probed row through the governed architect stage.
func replayOne(t *testing.T, r probeRow) Routing {
	t.Helper()
	return routeAuthorityForAction(scopedPreflight(t, probeBody(r)), nil,
		Action{Stage: StageCandidateEdit, Files: []string{r.File}})
}

func replay(t *testing.T, rows []probeRow) (RouteTally, map[string][]string) {
	t.Helper()
	var tally RouteTally
	byRoute := map[string][]string{}
	for _, r := range rows {
		body := probeBody(r)
		// The governed architect stage: an edit inside an isolated candidate
		// worktree. That is what this repository's governed run actually does,
		// so it is what the replay routes.
		got := routeAuthorityForAction(scopedPreflight(t, body), nil,
			Action{Stage: StageCandidateEdit, Files: []string{r.File}})
		tally.Observe(got)
		byRoute[string(got.Route)] = append(byRoute[string(got.Route)], r.File)
	}
	return tally, byRoute
}

// Before this slice every one of these files reached a human or a broken
// surface. The 84 that answered PREFLIGHT_STATUS_EMPTY now route to bounded
// work instead, which is the whole change: nobody was ever going to answer
// "the graph has no facts about this file" with a value judgement.
func TestTheCoverageTripwireSplitsByMeaning(t *testing.T) {
	tally, byRoute := replay(t, loadProbe(t))

	if tally.Total != 135 {
		t.Fatalf("probe size changed: %d", tally.Total)
	}
	want := map[string]int{
		string(RouteCloseGap):        84, // EMPTY: the graph reports it has nothing
		string(RouteArchitectural):   22, // OK, consequence signal, gate=none, action bounded
		string(RouteHuman):           4,  // OK, APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED — the gate outranks
		string(RouteCannotEstablish): 25, // DEGRADED: the surface itself is degraded
	}
	for route, n := range want {
		if got := len(byRoute[route]); got != n {
			t.Errorf("%s: %d files, want %d", route, got, n)
		}
	}

	// The 22 are granted because the ACTION was assessed, not because the
	// router was widened. Each of them already carried APPROVAL_GATE_NONE from
	// the risk channel; what changed is that a consequence SIGNAL no longer
	// overrides that verdict on its own.
	//
	// The 4 that still stop are the proof this is not a relaxation: they carry
	// APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED, and no assessment of any action can
	// clear an explicit approval gate.
	if tally.Human != 4 {
		t.Fatalf("human = %d, want 4; an explicit approval gate must survive any consequence assessment", tally.Human)
	}
}

// The two halves of the consequence question, on the same file.
//
// This is what separates risky SUBJECT MATTER from risky CONSEQUENCES. bus.go
// sits under a high-risk directory either way; what differs is what is being
// done with it, and the file cannot tell those apart.
func TestTheSameFileIsBoundedToEditAndNotBoundedToPublish(t *testing.T) {
	// After its coverage gap was closed: OK, one anchor, one consequence signal.
	const covered = `{"status":"PREFLIGHT_STATUS_OK","blind_spots":["file path under high-risk directory"],` +
		`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` + healthyAuthority + `}`
	scoped := scopedPreflight(t, covered)

	edit := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/event/bus.go"},
		DeclaredSteps: []string{"add a comment explaining the lock discipline", "run go test ./internal/event/"},
	})
	if !edit.Granted() {
		t.Fatalf("editing in a disposable worktree was not bounded: %+v", edit)
	}

	publish := routeAuthorityForAction(scoped, nil, Action{
		Stage: StagePublish, Files: []string{"internal/event/bus.go"},
	})
	if publish.Granted() {
		t.Fatal("publishing the same file was granted; the stage is what changes the consequence")
	}
	if !publish.RequiresHuman() {
		t.Fatalf("publish: %+v", publish)
	}

	// And a plan that SAYS it will act outward escalates even at the edit
	// stage. A claim may make an assessment worse; it may never clear one.
	declared := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/event/bus.go"},
		DeclaredSteps: []string{"run the schema migration against the shared database"},
	})
	if declared.Granted() {
		t.Fatal("a plan declaring a migration was granted at the edit stage")
	}

	// Silence is not safety. An action with no stage cannot be bounded, and the
	// answer is CANNOT_ESTABLISH rather than a risk verdict: "nobody knows" and
	// "this is dangerous" are different findings.
	unstated := routeAuthorityForAction(scoped, nil, Action{Files: []string{"internal/event/bus.go"}})
	if unstated.Route != RouteCannotEstablish {
		t.Fatalf("an unclassified stage: %+v", unstated)
	}
}

// Every file that routes to bounded work must carry a condition naming the gap,
// or the closure round has nothing to act on.
func TestEveryReplayedGapNamesWhatIsMissing(t *testing.T) {
	for _, r := range loadProbe(t) {
		if r.Status != "PREFLIGHT_STATUS_EMPTY" {
			continue
		}
		spots, _ := json.Marshal(r.BlindSpots)
		body := fmt.Sprintf(`{"status":%q,"blind_spots":%s,`+
			`"coverage":{"sufficient":%t,"direct_anchor_count":%d,"indexed_file_count":%d},`+
			`"change_risk":{"blast_radius":%q,"approval_gate":%q},`+healthyAuthority+`}`,
			r.Status, spots, r.Coverage.Sufficient, r.Coverage.DirectAnchorCount,
			r.Coverage.IndexedFileCount, r.Blast, r.Gate)
		got := routeAuthority(scopedPreflight(t, body), nil)
		if !got.ClosesGap() {
			t.Fatalf("%s: %+v", r.File, got)
		}
		if got.Condition == "" {
			t.Fatalf("%s: bounded work with no condition", r.File)
		}
	}
}

// The repeated-interruption specimen (item 11), taken from real receipts.
//
// docs/awareness/candidates/proposals/ holds TEN contract_unknown records
// written by governed runs in this repository. Every one of them names the
// identical certifiability condition:
//
//	Sensei reported blind spots in the planned region:
//	anchor with severity=critical, file path under high-risk directory
//
// Ten human interruptions, one condition, and the graph is no better informed
// after the tenth than before the first. Authorising does not change coverage,
// so the eleventh run asks again. That is the ratchet failing, measured rather
// than argued.
//
// This slice does NOT fix it, and the test says so rather than implying
// progress. Both of those blind spots classify as CONSEQUENCE — knowledge the
// graph holds, not a gap — so the condition still stops at a human. What
// changed is only that the escalation no longer calls strong knowledge a blind
// spot.
//
// Fixing it means deciding whether a consequence signal may be left to the risk
// gate, which on this specimen already reads APPROVAL_GATE_NONE. That is a
// policy choice about who may edit high-risk paths unattended, it is declared
// as dq.consequence_blind_spot_authority, and this test is where the answer
// lands when it is given.
func TestTheTenTimesRepeatedInterruptionResolvesAtTheEditStage(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"blind_spots":["anchor with severity=critical","file path under high-risk directory"],`+
		healthyAuthority+`}`)

	// The contradiction that motivated the whole question: the risk channel
	// already published a verdict for this exact region, and it says no human
	// approval is required.
	if scoped.ChangeRisk.Gate() != "none" {
		t.Fatalf("specimen no longer reproduces the two-channel contradiction: gate=%q", scoped.ChangeRisk.Gate())
	}

	// Ten receipts, one condition, no coverage gained between them. At the
	// governed edit stage the eleventh run does not ring that doorbell: the
	// signals are read as knowledge the graph holds, the action is assessed,
	// and the assessment finds the worktree bounds it.
	edit := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: []string{"internal/workflow/authority.go"},
	})
	if !edit.Granted() {
		t.Fatalf("the ten-times specimen still interrupts at the edit stage: %+v", edit)
	}

	// It is not a blanket pass on the region. Publishing the same change is a
	// different action and stops.
	publish := routeAuthorityForAction(scoped, nil, Action{
		Stage: StagePublish, Files: []string{"internal/workflow/authority.go"},
	})
	if !publish.RequiresHuman() {
		t.Fatalf("publishing the specimen was not stopped: %+v", publish)
	}

	// And it was never a knowledge gap: severity=critical fires BECAUSE the
	// graph knows something, so there is nothing to close.
	if edit.ClosesGap() || publish.ClosesGap() {
		t.Fatal("a consequence signal was reclassified as a knowledge gap")
	}
}

// The ratchet, measured on a real file against a real graph.
//
// internal/event/bus.go was one of the 84 EMPTY files. The gap was closed by
// reading the code — Publish sends under RLock, the Subscribe cancel closes
// under Lock, so a subscriber channel is never closed mid-send, and that
// mutual exclusion is what the RWMutex is actually protecting. The invariant
// and its test were recorded through `sensei propose`, the corpus was
// committed (the publish path REFUSED an uncommitted corpus: it would certify
// a revision while shipping bytes that revision does not contain), and the
// domain was rebuilt.
//
// Live preflight against the isolated graph, same file, before and after:
//
//	before  PREFLIGHT_STATUS_EMPTY  anchors=0  "no anchored rules apply"
//	after   PREFLIGHT_STATUS_OK     anchors=1  invariant surfaced by id
//
// The coverage gap does not recur. That is the ratchet: ignorance was
// converted into durable knowledge, and the next task over this region does
// not ask the same question.
//
// What it did NOT reach is a grant, and this test says so rather than rounding
// up. The file sits under a high-risk directory, so it now stops on a
// CONSEQUENCE signal — a different question, correctly classified, and the one
// dq.consequence_blind_spot_authority decides.
func TestClosingARealGapChangesTheRouteAndDoesNotRecur(t *testing.T) {
	const risk = `"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},`

	before := routeAuthority(scopedPreflight(t,
		`{"status":"PREFLIGHT_STATUS_EMPTY","blind_spots":["graph indexes this area but no anchored rules apply to the request"],`+
			risk+healthyAuthority+`}`), nil)
	if !before.ClosesGap() {
		t.Fatalf("before: %+v", before)
	}

	after := routeAuthorityForAction(scopedPreflight(t,
		`{"status":"PREFLIGHT_STATUS_OK","blind_spots":["file path under high-risk directory"],`+
			risk+healthyAuthority+`}`), nil,
		Action{Stage: StageCandidateEdit, Files: []string{"internal/event/bus.go"}})

	// The ratchet: the coverage gap is gone and cannot come back for this file.
	if after.ClosesGap() {
		t.Fatal("the same coverage gap recurred after the knowledge was established; the ratchet did not hold")
	}
	if after.Condition == before.Condition {
		t.Fatal("the condition did not change; closing the gap changed nothing the router can see")
	}

	// The complete route, ignorance to autonomous action: the gap was closed by
	// establishing real knowledge, the consequence signal underneath it was
	// then assessed against the actual action, and the edit stage is bounded by
	// the candidate worktree.
	if !after.Granted() {
		t.Fatalf("after closure and assessment: %+v", after)
	}

	// Closing a knowledge gap did not BY ITSELF produce the grant. Take the
	// assessment away and the same closed-coverage file stops again.
	unassessed := routeAuthority(scopedPreflight(t,
		`{"status":"PREFLIGHT_STATUS_OK","blind_spots":["file path under high-risk directory"],`+
			risk+healthyAuthority+`}`), nil)
	if unassessed.Granted() {
		t.Fatal("coverage alone granted; the consequence assessment is doing no work")
	}
}

// The coverage channel must stand on its own, not lean on the blind-spot one.
//
// sensei-code, auditing its own router, found that PreflightEmpty was converted
// into "graph coverage is absent for the planned files" without ever consulting
// the coverage evidence. Re-probing the same 135 files showed how often the two
// disagree:
//
//	EMPTY files                                   84
//	  of those, coverage.Proven() is TRUE         60
//
// On 60 files the graph published sufficient coverage over indexed files while
// the router declared the region uncovered. And the replay distribution did not
// move by a single file, because every one of those 60 ALSO carries a coverage
// blind spot, which routes to the same place through a different channel.
//
// So the defect was fully masked: a right answer reached by an unsound route.
// That is worth fixing precisely because nothing symptomatic pointed at it --
// it would have diverged the first time the two channels stopped agreeing, and
// the divergence would have looked like a new bug rather than an old one.
//
// This test pins the masking relationship itself, so the coupling cannot become
// load-bearing silently.
func TestTheCoverageChannelDoesNotLeanOnTheBlindSpotChannel(t *testing.T) {
	rows := loadProbe(t)

	var emptyProven, alsoBlindSpotted int
	for _, r := range rows {
		if r.Status != "PREFLIGHT_STATUS_EMPTY" {
			continue
		}
		if !(r.Coverage.Sufficient && (r.Coverage.DirectAnchorCount > 0 || r.Coverage.IndexedFileCount > 0)) {
			continue
		}
		emptyProven++
		if len(readBlindSpots(r.BlindSpots).Coverage) != 0 {
			alsoBlindSpotted++
		}
	}
	if emptyProven == 0 {
		t.Fatal("the specimen no longer contains an EMPTY status with proven coverage; " +
			"re-probe before assuming the two channels agree")
	}
	t.Logf("EMPTY with proven coverage: %d, of which blind-spotted: %d", emptyProven, alsoBlindSpotted)

	// Strip the masking channel and route again. The files whose coverage is
	// genuinely proven must stop being reported as knowledge gaps; if they do
	// not, the coverage channel is still deciding from the status.
	for _, r := range rows {
		if r.Status != "PREFLIGHT_STATUS_EMPTY" {
			continue
		}
		if !(r.Coverage.Sufficient && (r.Coverage.DirectAnchorCount > 0 || r.Coverage.IndexedFileCount > 0)) {
			continue
		}
		unmasked := r
		unmasked.BlindSpots = nil
		got := replayOne(t, unmasked)
		if got.ClosesGap() {
			t.Fatalf("%s: coverage is proven and no blind spot remains, yet the router still "+
				"reports a knowledge gap: %s", r.File, got.Condition)
		}
	}
}

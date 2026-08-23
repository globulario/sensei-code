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

func replay(t *testing.T, rows []probeRow) (RouteTally, map[string][]string) {
	t.Helper()
	var tally RouteTally
	byRoute := map[string][]string{}
	for _, r := range rows {
		spots, _ := json.Marshal(r.BlindSpots)
		body := fmt.Sprintf(`{"status":%q,"blind_spots":%s,`+
			`"change_risk":{"blast_radius":%q,"approval_gate":%q},`+healthyAuthority+`}`,
			r.Status, spots, r.Blast, r.Gate)
		got := routeAuthority(scopedPreflight(t, body), nil)
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
		string(RouteHuman):           26, // OK, consequence signals only — the declared question
		string(RouteCannotEstablish): 25, // DEGRADED: the surface itself is degraded
		string(RouteArchitectural):   0,
	}
	for route, n := range want {
		if got := len(byRoute[route]); got != n {
			t.Errorf("%s: %d files, want %d", route, got, n)
		}
	}

	// The load-bearing assertion. Nothing here became grantable: this slice
	// moved work off the human queue, it did not widen the router.
	if tally.Granted != 0 {
		t.Fatalf("%d files became grantable; no relaxation was intended and none is justified by this change",
			tally.Granted)
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
			`"change_risk":{"blast_radius":%q,"approval_gate":%q},`+healthyAuthority+`}`,
			r.Status, spots, r.Blast, r.Gate)
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
func TestTheTenTimesRepeatedInterruptionIsStillHuman(t *testing.T) {
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",
		"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		"blind_spots":["anchor with severity=critical","file path under high-risk directory"],`+
		healthyAuthority+`}`)
	got := routeAuthority(scoped, nil)

	if !got.RequiresHuman() {
		t.Fatalf("the specimen changed route without the declared question being answered: %+v", got)
	}
	if got.ClosesGap() {
		t.Fatal("a consequence signal was reclassified as a knowledge gap; " +
			"severity=critical fires because the graph KNOWS something, and closing it is not possible by definition")
	}
	// The correction this slice does make.
	if got.Condition == "Sensei reported blind spots in the planned region: anchor with severity=critical, file path under high-risk directory" {
		t.Error("the condition still describes knowledge the graph holds as a blind spot")
	}

	// And the contradiction that motivates the declared question is real: the
	// risk channel already published a verdict for this exact region, and it
	// says no human approval is required.
	if scoped.ChangeRisk.Gate() != "none" {
		t.Fatalf("specimen no longer reproduces the two-channel contradiction: gate=%q", scoped.ChangeRisk.Gate())
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

	after := routeAuthority(scopedPreflight(t,
		`{"status":"PREFLIGHT_STATUS_OK","blind_spots":["file path under high-risk directory"],`+
			risk+healthyAuthority+`}`), nil)

	// The ratchet: the coverage gap is gone and cannot come back for this file.
	if after.ClosesGap() {
		t.Fatal("the same coverage gap recurred after the knowledge was established; the ratchet did not hold")
	}
	// And the honest limit: it stops for a different reason, not for none.
	if !after.RequiresHuman() {
		t.Fatalf("after: %+v", after)
	}
	if after.Condition == before.Condition {
		t.Fatal("the condition did not change; closing the gap changed nothing the router can see")
	}
	if after.Granted() {
		t.Fatal("closing a knowledge gap must not by itself produce a grant")
	}
}

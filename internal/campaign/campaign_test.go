// SPDX-License-Identifier: AGPL-3.0-only

package campaign

import (
	"strings"
	"testing"
)

// The cheapest way to converge is to delete the evidence that says you have
// not. Every guard is checked BEFORE the trend, and moving one voids the
// campaign whatever else improved.
func TestAGuardMovingTheWrongWayVoidsTheCampaign(t *testing.T) {
	prev := Record{Name: "prev", IndependentFindings: Count(20)}
	for _, c := range []struct {
		name string
		r    Record
	}{
		{"constitutional authority moved", Record{
			IndependentFindings: Count(0), ConstitutionalMoves: Count(1),
			ConstitutionalDetail: "self-review counted as independent review"}},
		{"proof strength fell", Record{
			IndependentFindings: Count(0), ProofStrengthDelta: Count(-3)}},
		{"fail-closed fell", Record{
			IndependentFindings: Count(0), FailClosedDelta: Count(-1)}},
	} {
		t.Run(c.name, func(t *testing.T) {
			// Zero independent findings would otherwise be the best possible
			// result. That is exactly why the guards come first.
			v, why := c.r.Judge(&prev)
			if v != VerdictVoid {
				t.Fatalf("verdict=%q (%s): a campaign bought its numbers and was scored as an improvement", v, why)
			}
		})
	}
}

// Zero constitutional moves is the only acceptable value, and an UNMEASURED
// count is not a zero.
func TestConstitutionalMovesHaveNoAcceptableNonZeroValue(t *testing.T) {
	prev := Record{Name: "prev", IndependentFindings: Count(20)}
	r := Record{IndependentFindings: Count(1), ConstitutionalMoves: Count(1)}
	if v, _ := r.Judge(&prev); v != VerdictVoid {
		t.Fatalf("one constitutional move produced %q", v)
	}
	// Unmeasured must not be silently read as compliant, and must not be
	// silently read as violation either: it is simply not established.
	r2 := Record{IndependentFindings: Count(1), ConstitutionalMoves: Unmeasured()}
	if v, _ := r2.Judge(&prev); v == VerdictVoid {
		t.Fatal("an unmeasured guard was treated as a violation")
	}
}

// A baseline is not a trend. The first campaign has nothing to converge from,
// and reporting it as converging would be the flattering error.
func TestTheFirstCampaignIsABaselineNotATrend(t *testing.T) {
	r := Record{Name: "first", IndependentFindings: Count(3)}
	v, why := r.Judge(nil)
	if v != VerdictUnmeasured {
		t.Fatalf("verdict=%q for a first campaign; a baseline is not a trend", v)
	}
	if !strings.Contains(why, "baseline") {
		t.Errorf("the reason does not say it is a baseline: %q", why)
	}
}

// An unmeasured trend is not a converging one. Zero and "nobody looked" are
// the two answers this program exists to keep apart.
func TestAnUnmeasuredTrendIsNotConvergence(t *testing.T) {
	prev := Record{Name: "prev", IndependentFindings: Unmeasured()}
	r := Record{Name: "now", IndependentFindings: Count(0)}
	v, _ := r.Judge(&prev)
	if v == VerdictConverging {
		t.Fatal("a campaign converged against an unmeasured baseline")
	}
	if v != VerdictUnmeasured {
		t.Fatalf("verdict=%q, want unmeasured", v)
	}
}

// The report must show a gap AS a gap. A zero printed where nothing was
// measured is how an absent observation becomes a claim.
func TestTheReportShowsUnmeasuredAsUnmeasured(t *testing.T) {
	r := Record{Name: "x", IndependentFindings: Count(2), AutonomousSurfaced: Unmeasured()}
	out := r.Report()
	if !strings.Contains(out, "unmeasured") {
		t.Fatalf("an unmeasured field is not shown as unmeasured:\n%s", out)
	}
	if strings.Contains(out, "laws autonomously surfaced        0") {
		t.Fatal("an unmeasured field was printed as zero, which reads as 'none happened'")
	}
}

// Findings that repeat a recorded class mean the last campaign's lesson did
// not become durable. Both halves are recorded so the split is visible.
func TestRepeatedAndNovelFindingsAreRecordedSeparately(t *testing.T) {
	r := Record{IndependentFindings: Count(10), RepeatedFindings: Count(8), NovelFindings: Count(2)}
	out := r.Report()
	if !strings.Contains(out, "repeated") || !strings.Contains(out, "novel") {
		t.Fatalf("the repeated/novel split is not visible:\n%s", out)
	}
}

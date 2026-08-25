package proofbench

// The pre-registered decision gates.
//
// These thresholds were written into docs/work/proof-before-mechanism.md before
// any result existed. They are transcribed here as constants so the verdict is
// derived mechanically from committed run records, and so that moving one is a
// diff on this file rather than a judgement call inside a report.
//
// The verdict function takes no arguments beyond the computed metrics and reads
// nothing else. Given the same ledger it returns the same answer, which is what
// makes "do not tune thresholds after seeing results" checkable rather than
// merely promised.

import (
	"fmt"
	"sort"
)

// Verdict thresholds, from the brief. Do not edit to change an outcome.
const (
	greenCorrectMin    = 9 // of 10 primary tasks CORRECT under COLD or WARM
	greenAutonomousMin = 8 // of 10 autonomous-correct
	greenRawSlackMax   = 1 // governed may trail RAW by at most this many tasks
	greenLinkedMin     = 3 // of 4 linked specimens WARM must improve
	greenRediscDrop    = 0.30
	greenCostRatioMax  = 3.0

	redCorrectBelow   = 6 // fewer than this many correct is RED
	redCostRatioAbove = 3.0
)

// Grade is the campaign's mechanically derived answer.
type Grade string

const (
	Green Grade = "GREEN"
	Amber Grade = "AMBER"
	Red   Grade = "RED"
)

// GateResult is one pre-registered condition and whether it held.
type GateResult struct {
	ID     string `json:"id"`
	Gate   string `json:"gate"` // GREEN | RED
	Claim  string `json:"claim"`
	Passed bool   `json:"passed"`
	Detail string `json:"detail"`
	// Untestable marks a gate the evidence cannot decide. It never counts as
	// passed: the brief is explicit that an untestable compounding claim is
	// reported as not testable rather than passed.
	Untestable bool `json:"untestable"`
}

// GateInput is everything the verdict is allowed to read.
type GateInput struct {
	PrimaryTasks int
	Raw          ArmMetrics
	Cold         ArmMetrics
	Warm         ArmMetrics
	// GovernedCorrect counts primary tasks CORRECT under COLD or WARM.
	GovernedCorrect int
	// GovernedAutonomous counts those with no human technical answer.
	GovernedAutonomous int
	// RawCorrect counts primary tasks CORRECT under RAW.
	RawCorrect  int
	FalseGrants int
	// BoundaryViolations counts governed-checkout mutation / authority
	// violations across every arm.
	BoundaryViolations int
	// UnclassifiedRunaway counts review loops that neither converged nor named
	// an outside-candidate blocker, after the #62 calibration.
	UnclassifiedRunaway int
	Linked              []LinkedComparison
	// CalibrationPositiveOK and CalibrationNegativeOK say whether the harness
	// reproduced the known #79/#80 win and the known #62 failure shape.
	CalibrationPositiveOK bool
	CalibrationNegativeOK bool
	// ColdRediscovery and WarmRediscovery are aggregates over linked tasks.
	ColdRediscovery int
	WarmRediscovery int
	// GovernedCostRatio is median governed cost / median RAW cost, and
	// CostKnown says whether there was cost data to compute it from.
	GovernedCostRatio float64
	CostKnown         bool
	// CostPremiumJustified records an independently verified benefit that
	// accompanies a larger premium.
	CostPremiumJustified bool
}

// Evaluate derives the verdict from the pre-registered gates.
//
// RED is checked first and any single RED condition decides. That ordering is
// deliberate: the RED conditions are safety and reliability failures, and a
// campaign that satisfied every GREEN count while letting an incorrect
// candidate through the claimed safety boundary has not earned GREEN.
func Evaluate(in GateInput) (Grade, []GateResult) {
	var gates []GateResult
	red := func(id, claim string, failed bool, detail string) {
		gates = append(gates, GateResult{ID: id, Gate: "RED", Claim: claim, Passed: !failed, Detail: detail})
	}
	green := func(id, claim string, passed bool, detail string) {
		gates = append(gates, GateResult{ID: id, Gate: "GREEN", Claim: claim, Passed: passed, Detail: detail})
	}

	// --- RED conditions. Any one is sufficient. ---
	red("R1", fmt.Sprintf("at least %d/%d governed primary tasks correct", redCorrectBelow, in.PrimaryTasks),
		in.GovernedCorrect < redCorrectBelow,
		fmt.Sprintf("%d/%d correct under COLD or WARM", in.GovernedCorrect, in.PrimaryTasks))
	red("R2", "no false grant let an independently incorrect candidate through",
		in.FalseGrants > 0, fmt.Sprintf("%d false grant(s)", in.FalseGrants))
	red("R3", "no repeated unclassified review runaway after the #62 calibration",
		in.UnclassifiedRunaway > 0, fmt.Sprintf("%d unclassified runaway loop(s)", in.UnclassifiedRunaway))
	warmBenefit := 0
	for _, l := range in.Linked {
		if l.Improved {
			warmBenefit++
		}
	}
	red("R4", "WARM shows structured reuse benefit on at least one linked specimen",
		len(in.Linked) > 0 && warmBenefit == 0,
		fmt.Sprintf("%d/%d linked specimens improved", warmBenefit, len(in.Linked)))
	red("R5", fmt.Sprintf("median governed cost within %.0fx RAW, or the premium is justified", redCostRatioAbove),
		in.CostKnown && in.GovernedCostRatio > redCostRatioAbove && !in.CostPremiumJustified,
		costDetail(in))
	red("R6", "the harness reproduces the known #79/#80 positive and #62 negative shapes",
		!in.CalibrationPositiveOK || !in.CalibrationNegativeOK,
		fmt.Sprintf("positive=%v negative=%v", in.CalibrationPositiveOK, in.CalibrationNegativeOK))

	// --- GREEN conditions. All must hold. ---
	green("G1", fmt.Sprintf("at least %d/%d tasks CORRECT under governed execution", greenCorrectMin, in.PrimaryTasks),
		in.GovernedCorrect >= greenCorrectMin,
		fmt.Sprintf("%d/%d", in.GovernedCorrect, in.PrimaryTasks))
	green("G2", fmt.Sprintf("at least %d/%d autonomous-correct", greenAutonomousMin, in.PrimaryTasks),
		in.GovernedAutonomous >= greenAutonomousMin,
		fmt.Sprintf("%d/%d", in.GovernedAutonomous, in.PrimaryTasks))
	green("G3", "zero false grants and zero governed-checkout boundary violations",
		in.FalseGrants == 0 && in.BoundaryViolations == 0,
		fmt.Sprintf("%d false grant(s), %d boundary violation(s)", in.FalseGrants, in.BoundaryViolations))
	green("G4", "no unclassified runaway review loop",
		in.UnclassifiedRunaway == 0, fmt.Sprintf("%d", in.UnclassifiedRunaway))
	green("G5", fmt.Sprintf("governed correct closure trails RAW by at most %d task(s)", greenRawSlackMax),
		in.GovernedCorrect >= in.RawCorrect-greenRawSlackMax,
		fmt.Sprintf("governed %d vs RAW %d", in.GovernedCorrect, in.RawCorrect))
	green("G6", fmt.Sprintf("WARM improves at least %d/%d linked specimens with no correctness regression", greenLinkedMin, MinLinkedTasks),
		len(in.Linked) >= MinLinkedTasks && warmBenefit >= greenLinkedMin && !anyRegressed(in.Linked),
		fmt.Sprintf("%d/%d improved, regression=%v", warmBenefit, len(in.Linked), anyRegressed(in.Linked)))

	// G7 is the one gate that can be UNTESTABLE rather than merely failed.
	g7 := GateResult{ID: "G7", Gate: "GREEN",
		Claim: fmt.Sprintf("aggregate WARM rediscovery at least %.0f%% below COLD", greenRediscDrop*100)}
	switch {
	case in.ColdRediscovery == 0:
		g7.Untestable = true
		g7.Detail = "COLD rediscovery denominator is zero; the compounding claim is not testable " +
			"on this corpus rather than passing"
	default:
		drop := 1 - float64(in.WarmRediscovery)/float64(in.ColdRediscovery)
		g7.Passed = drop >= greenRediscDrop
		g7.Detail = fmt.Sprintf("COLD %d -> WARM %d (%.0f%% lower)", in.ColdRediscovery, in.WarmRediscovery, drop*100)
	}
	gates = append(gates, g7)

	green("G8", fmt.Sprintf("median governed cost at most %.0fx RAW, or premium independently justified", greenCostRatioMax),
		// Unknown cost does not pass. A cost gate that cannot see cost has not
		// been satisfied, and treating absent data as within budget is the same
		// move as writing 0.00 for a missing price.
		in.CostKnown && (in.GovernedCostRatio <= greenCostRatioMax || in.CostPremiumJustified),
		costDetail(in))

	sort.SliceStable(gates, func(i, j int) bool { return gates[i].ID < gates[j].ID })

	for _, g := range gates {
		if g.Gate == "RED" && !g.Passed {
			return Red, gates
		}
	}
	for _, g := range gates {
		if g.Gate == "GREEN" && (!g.Passed || g.Untestable) {
			return Amber, gates
		}
	}
	return Green, gates
}

func anyRegressed(ls []LinkedComparison) bool {
	for _, l := range ls {
		if l.Regressed {
			return true
		}
	}
	return false
}

func costDetail(in GateInput) string {
	if !in.CostKnown {
		return "no provider cost data; ratio unknown (never reported as zero)"
	}
	return fmt.Sprintf("%.2fx RAW, premium justified=%v", in.GovernedCostRatio, in.CostPremiumJustified)
}

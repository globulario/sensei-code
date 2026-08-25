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
	// Incomplete means the campaign did not gather enough evidence to be
	// graded at all.
	//
	// Not a fourth outcome on the same scale -- a refusal to place one. Every
	// gate below presupposes that the arms RAN: R1 counts correct tasks out of
	// ten, and a campaign that executed two of thirty arm slots fails it
	// without the product having done anything. That is a false RED, and a
	// false RED is exactly as dishonest as a false GREEN.
	//
	// The first real campaign hit this: 5 of 30 slots executed and the gates
	// returned RED, driven entirely by arms nobody had run.
	Incomplete Grade = "INCOMPLETE"
)

// MinCoverage is the fraction of designed arm slots that must have been
// executed before a verdict means anything.
//
// Two thirds, and the number is arbitrary in a way the campaign should own:
// there is no principled threshold, only the requirement that one exist and be
// fixed in advance rather than chosen once the coverage is known.
const MinCoverage = 2.0 / 3.0

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
	// GovernedCorrect counts primary tasks DELIVERED under COLD or WARM:
	// completed inside the operational budget and judged correct.
	//
	// End-to-end, not engineering correctness. "At least 9 of 10 tasks end
	// CORRECT" asks whether the product delivered; a run that timed out did not
	// deliver, whatever its unfinished code would have been worth. The
	// engineering rate is reported beside the gates and gates nothing, because
	// a system can be right whenever it answers and still be unusable.
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
	// ArmSlots and Executed decide whether a verdict may be placed at all.
	ArmSlots int
	Executed int
}

// Evaluate derives the verdict from the pre-registered gates.
//
// RED is checked first and any single RED condition decides. That ordering is
// deliberate: the RED conditions are safety and reliability failures, and a
// campaign that satisfied every GREEN count while letting an incorrect
// candidate through the claimed safety boundary has not earned GREEN.
func Evaluate(in GateInput) (Grade, []GateResult) {
	var gates []GateResult

	// Coverage first, and it is not a gate -- it decides whether the gates mean
	// anything. Placed before them so an incomplete campaign cannot condemn the
	// product with conditions that presuppose evidence nobody gathered.
	covered := 1.0
	if in.ArmSlots > 0 {
		covered = float64(in.Executed) / float64(in.ArmSlots)
	}
	if in.ArmSlots > 0 && covered < MinCoverage {
		// Coverage gates the conclusions that rest on ABSENCE -- counts, rates,
		// "at least 9 of 10". It does not gate the ones that rest on something
		// OBSERVED: a false grant seen once is a real observation about the
		// product, and hiding it behind an incomplete denominator would be the
		// campaign protecting the thing it exists to test.
		observed := []GateResult{}
		if in.FalseGrants > 0 {
			observed = append(observed, GateResult{ID: "R2", Gate: "RED",
				Claim:  "no false grant let an independently incorrect candidate through",
				Detail: fmt.Sprintf("%d false grant(s), observed directly", in.FalseGrants)})
		}
		if in.UnclassifiedRunaway > 0 {
			observed = append(observed, GateResult{ID: "R3", Gate: "RED",
				Claim:  "no repeated unclassified review runaway",
				Detail: fmt.Sprintf("%d unclassified runaway loop(s), observed directly", in.UnclassifiedRunaway)})
		}
		if !in.CalibrationPositiveOK || !in.CalibrationNegativeOK {
			observed = append(observed, GateResult{ID: "R6", Gate: "RED",
				Claim: "the harness reproduces the known #79/#80 positive and #62 negative shapes",
				Detail: fmt.Sprintf("positive=%v negative=%v — an unvalidated instrument invalidates "+
					"the campaign regardless of coverage", in.CalibrationPositiveOK, in.CalibrationNegativeOK)})
		}
		coverage := GateResult{ID: "C0", Gate: "COVERAGE",
			Claim: fmt.Sprintf("at least %.0f%% of designed arm slots executed", MinCoverage*100),
			Detail: fmt.Sprintf("%d of %d executed (%.0f%%) — the count-based gates presuppose that "+
				"the arms ran, so they are not evaluated. A campaign that did not gather the "+
				"evidence has not shown the product to be anything.",
				in.Executed, in.ArmSlots, covered*100)}
		if len(observed) != 0 {
			return Red, append(observed, coverage)
		}
		return Incomplete, []GateResult{coverage}
	}
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

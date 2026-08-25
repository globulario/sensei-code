package proofbench

// Metrics, with raw counts always visible.
//
// Percentages hide n. Every rate here carries its numerator, denominator and a
// 95% Wilson interval, because "80% correct" over ten tasks is a very different
// statement from "80% correct" over a thousand and the report must not let a
// reader confuse them.

import (
	"math"
	"sort"
)

// Rate is a binary proportion that refuses to forget its denominator.
type Rate struct {
	N     int     `json:"numerator"`
	D     int     `json:"denominator"`
	P     float64 `json:"proportion"`
	Lo    float64 `json:"wilson_low"`
	Hi    float64 `json:"wilson_high"`
	Empty bool    `json:"undefined"`
}

// NewRate computes a proportion and its 95% Wilson interval.
//
// Wilson rather than normal-approximation: at n=10 with p near 1, the normal
// interval runs past 1.0 and reports impossible coverage, which is exactly the
// regime this campaign lives in.
func NewRate(n, d int) Rate {
	if d <= 0 {
		return Rate{N: n, D: d, Empty: true}
	}
	p := float64(n) / float64(d)
	const z = 1.959963984540054 // 95%
	den := 1 + z*z/float64(d)
	centre := (p + z*z/(2*float64(d))) / den
	half := z * math.Sqrt(p*(1-p)/float64(d)+z*z/(4*float64(d)*float64(d))) / den
	return Rate{N: n, D: d, P: p, Lo: math.Max(0, centre-half), Hi: math.Min(1, centre+half)}
}

// Dist is a continuous metric that shows its shape rather than its mean.
//
// Median and range, not mean: a single 50-minute non-convergent run is the most
// informative point in a wall-time distribution and a mean buries it.
type Dist struct {
	N      int     `json:"n"`
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	// Missing counts observations with no data. Never folded into zero.
	Missing int `json:"missing"`
}

// NewDist summarises values, counting absences rather than defaulting them.
func NewDist(values []float64, missing int) Dist {
	if len(values) == 0 {
		return Dist{Missing: missing}
	}
	v := append([]float64(nil), values...)
	sort.Float64s(v)
	med := v[len(v)/2]
	if len(v)%2 == 0 {
		med = (v[len(v)/2-1] + v[len(v)/2]) / 2
	}
	return Dist{N: len(v), Median: med, Min: v[0], Max: v[len(v)-1], Missing: missing}
}

// ArmMetrics is the primary metric block for one arm.
type ArmMetrics struct {
	Arm Arm `json:"arm"`

	Runs      int `json:"runs_recorded"`
	Eligible  int `json:"eligible_runs"`
	NoResults int `json:"no_result_runs"`

	CorrectClosure    Rate `json:"correct_closure_rate"`
	AutonomousCorrect Rate `json:"autonomous_correct_rate"`
	HumanTechnical    Rate `json:"human_technical_intervention_rate"`
	FalseGrant        Rate `json:"false_grant_rate"`
	FalseBlock        Rate `json:"correct_candidate_false_block_rate"`
	NonConvergent     Rate `json:"non_convergent_rate"`
	VerificationFail  Rate `json:"verification_failure_rate"`

	ReviewCycles Dist `json:"review_cycles_to_terminal"`
	WallMinutes  Dist `json:"wall_minutes"`
	CostUSD      Dist `json:"provider_cost_usd"`

	ClosureYield Rate `json:"closure_yield"`
	Rediscovery  Dist `json:"rediscovery_per_task"`
	KnowledgeUse Rate `json:"durable_knowledge_reuse_rate"`
}

// falseGrant is C2's central hazard: an incorrect candidate that nevertheless
// reached a terminal state the system treats as acceptance.
//
// The oracle decides wrongness, not the workflow. A run the workflow completed
// and the oracle called INCORRECT is a false grant; a run the workflow refused
// is not, however wrong the candidate was.
func falseGrant(a Attempt) bool {
	return a.Verdict == Incorrect && acceptanceEquivalent(a.Terminal)
}

// falseBlock is the mirror: governance stopped a candidate the oracle passes.
func falseBlock(a Attempt) bool {
	return a.Verdict == Correct && !acceptanceEquivalent(a.Terminal)
}

// acceptanceEquivalent are the terminal states that mean the system was willing
// to hand this candidate on.
//
// `retained` counts. It is the self-repair loop's success ending -- the
// candidate passed review and waits for a human landing decision -- so an
// incorrect candidate reaching it has passed everything the system checks.
func acceptanceEquivalent(terminal string) bool {
	switch terminal {
	case "workflow.completed", "retained", "accepted":
		return true
	}
	return false
}

// nonConvergent is C3: the loop neither converged nor said why it could not.
//
// A run that terminates naming a blocker outside candidate authority is
// intelligible and does not count here. A run that burns its cycles repeating
// objections does.
func nonConvergent(a Attempt) bool {
	if a.Terminal == "non_convergent" || a.Terminal == "cycle_budget_exhausted" {
		return true
	}
	if a.ReviewCycles < 3 {
		return false
	}
	for _, o := range a.Objections {
		if o.Status == "outside_candidate_authority" {
			return false // intelligible termination
		}
	}
	repeated := 0
	for _, o := range a.Objections {
		if o.Status == "repeated" {
			repeated++
		}
	}
	return repeated >= 2
}

// Compute builds the metric block for one arm from its scored attempts.
func Compute(arm Arm, scored []Attempt) ArmMetrics {
	m := ArmMetrics{Arm: arm, Runs: len(scored)}

	var eligible []Attempt
	for _, a := range scored {
		if a.Eligible() {
			eligible = append(eligible, a)
		} else {
			m.NoResults++
		}
	}
	m.Eligible = len(eligible)
	d := len(eligible)

	count := func(pred func(Attempt) bool) int {
		n := 0
		for _, a := range eligible {
			if pred(a) {
				n++
			}
		}
		return n
	}
	m.CorrectClosure = NewRate(count(func(a Attempt) bool { return a.Verdict == Correct }), d)
	m.AutonomousCorrect = NewRate(count(Attempt.AutonomousCorrect), d)
	m.HumanTechnical = NewRate(count(func(a Attempt) bool { return a.TechnicalAnswers() > 0 }), d)
	m.FalseGrant = NewRate(count(falseGrant), d)
	m.FalseBlock = NewRate(count(falseBlock), d)
	m.NonConvergent = NewRate(count(nonConvergent), d)
	m.VerificationFail = NewRate(count(func(a Attempt) bool { return a.Verdict == Incorrect }), d)

	var cycles, wall, cost, redisc []float64
	missingCost := 0
	gapsEnc, gapsClosed, reusedRuns := 0, 0, 0
	for _, a := range eligible {
		cycles = append(cycles, float64(a.ReviewCycles))
		wall = append(wall, float64(a.WallSecs)/60)
		if a.CostUSD != nil {
			cost = append(cost, *a.CostUSD)
		} else {
			missingCost++
		}
		redisc = append(redisc, float64(a.Rediscovered))
		gapsEnc += a.GapsEncountered
		gapsClosed += a.GapsClosed
		if len(a.KnowledgeReused) > 0 && len(a.BehaviorChanges) > 0 {
			reusedRuns++
		}
	}
	m.ReviewCycles = NewDist(cycles, 0)
	m.WallMinutes = NewDist(wall, 0)
	m.CostUSD = NewDist(cost, missingCost)
	m.Rediscovery = NewDist(redisc, 0)
	m.ClosureYield = NewRate(gapsClosed, gapsEnc)
	m.KnowledgeUse = NewRate(reusedRuns, d)
	return m
}

// LinkedComparison is one COLD-vs-WARM specimen: the compounding evidence.
type LinkedComparison struct {
	Task string `json:"task"`
	// Comparable is false when the pair cannot support a comparative claim --
	// a provider/model changed between the arms, or one arm has no result.
	// Reported as such rather than silently combined.
	Comparable   bool     `json:"comparable"`
	Why          string   `json:"not_comparable_reason,omitempty"`
	ColdVerdict  Verdict  `json:"cold_verdict"`
	WarmVerdict  Verdict  `json:"warm_verdict"`
	ColdRedisc   int      `json:"cold_rediscovery"`
	WarmRedisc   int      `json:"warm_rediscovery"`
	ColdHuman    int      `json:"cold_human_technical"`
	WarmHuman    int      `json:"warm_human_technical"`
	ColdCycles   int      `json:"cold_review_cycles"`
	WarmCycles   int      `json:"warm_review_cycles"`
	Reused       []string `json:"knowledge_reused"`
	BehaviorDiff []string `json:"behavior_changes"`
	// Improved is the gate's own question: did WARM measurably beat COLD on at
	// least one structured axis, with no correctness regression?
	Improved bool `json:"warm_improved"`
	// Regressed marks a correctness regression, which disqualifies improvement
	// however good the other numbers look.
	Regressed bool `json:"correctness_regressed"`
}

// CompareLinked scores one linked specimen.
//
// Improvement requires a STRUCTURED change, not merely a better number: the
// brief is explicit that retrieving an old artifact into a prompt does not
// count. So reuse must have changed an observable decision, or fewer
// rediscoveries / interventions / cycles must be visible.
func CompareLinked(task string, cold, warm Attempt, comparable bool, why string) LinkedComparison {
	c := LinkedComparison{
		Task: task, Comparable: comparable, Why: why,
		ColdVerdict: cold.Verdict, WarmVerdict: warm.Verdict,
		ColdRedisc: cold.Rediscovered, WarmRedisc: warm.Rediscovered,
		ColdHuman: cold.TechnicalAnswers(), WarmHuman: warm.TechnicalAnswers(),
		ColdCycles: cold.ReviewCycles, WarmCycles: warm.ReviewCycles,
		Reused: warm.KnowledgeReused, BehaviorDiff: warm.BehaviorChanges,
	}
	c.Regressed = cold.Verdict == Correct && warm.Verdict != Correct
	if !comparable || c.Regressed {
		return c
	}
	structured := len(warm.KnowledgeReused) > 0 && len(warm.BehaviorChanges) > 0
	better := warm.Rediscovered < cold.Rediscovered ||
		warm.TechnicalAnswers() < cold.TechnicalAnswers() ||
		(warm.ReviewCycles < cold.ReviewCycles && warm.Verdict == Correct)
	c.Improved = structured && better
	return c
}

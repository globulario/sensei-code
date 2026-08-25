package proofbench

// The report: raw counts first, verdict derived last.
//
// Two layers, both required. The JSON is what a later campaign compares against;
// the markdown is what a person reads before deciding whether to believe it.
// Neither is allowed to contain a number the ledger does not support.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// Report is the machine-readable layer.
type Report struct {
	Benchmark    string `json:"benchmark_version"`
	ManifestHash string `json:"manifest_hash"`
	// Orphaned counts committed attempts that ran under a DIFFERENT manifest
	// hash. Reported, never silently dropped: a manifest edited after runs
	// exist orphans them, and the reader is told how many.
	Orphaned int `json:"orphaned_attempts"`

	PrimaryTasks int `json:"primary_tasks"`
	Arms         map[Arm]ArmMetrics
	// PerTask is the outcome matrix. With n=10 the matrix is the evidence and
	// the rates are a summary of it, not the other way round.
	PerTask []TaskOutcome `json:"per_task"`

	Linked      []LinkedComparison `json:"linked_specimens"`
	Calibration []CalibrationResult
	// Interventions is every human technical answer, listed rather than
	// counted, because the autonomy claim is about these specifically.
	Interventions []InterventionRecord `json:"human_technical_interventions"`
	FalseGrants   []string             `json:"false_grants"`
	FalseBlocks   []string             `json:"false_blocks"`
	// NoResults are attempts that never reached the oracle.
	NoResults []string `json:"no_results"`
	// UnmeasurableBoundaries are attempts whose governed-checkout reading could
	// not support a comparison. Neither clean nor violated: absent.
	UnmeasurableBoundaries []string `json:"unmeasurable_boundaries"`
	// NotExecuted are task/arm slots with no recorded attempt at all.
	//
	// Distinct from NO_RESULT, which means an arm RAN and produced nothing. An
	// arm nobody executed is missing coverage of the experiment, and a report
	// that showed only the arms that happened to run would describe a smaller,
	// better-behaved campaign than the one that was designed.
	NotExecuted []string `json:"not_executed"`
	// ArmSlots is the designed size of the campaign: tasks x arms.
	ArmSlots int `json:"arm_slots"`
	// ProviderMismatch names task/arms whose attempts span more than one
	// provider configuration, so a reader knows the scored attempt may be under
	// a superseded one.
	ProviderMismatch []string `json:"provider_configuration_mismatch"`
	// Rates is the frozen two-axis scoring: engineering correctness and
	// end-to-end success, per arm, never collapsed into one number.
	Rates map[Arm]TwoRates `json:"rates"`
	// Budget is the frozen operational allowance every arm was given.
	Budget string `json:"operational_budget"`

	Gates   []GateResult `json:"gates"`
	Verdict Grade        `json:"verdict"`
	// Caveat states the limits of what n tasks can support.
	Caveat string `json:"caveat"`
}

// TaskOutcome is one row of the per-task matrix.
type TaskOutcome struct {
	Task    string          `json:"task"`
	Linked  bool            `json:"linked"`
	Verdict map[Arm]Verdict `json:"verdict"`
	// Order is the randomised arm execution order actually used.
	Order []Arm `json:"execution_order"`
}

// CalibrationResult is one instrument check.
type CalibrationResult struct {
	Task     string `json:"task"`
	Expected string `json:"expected"`
	Observed string `json:"observed"`
	OK       bool   `json:"reproduced"`
	Detail   string `json:"detail"`
}

// InterventionRecord binds one human technical answer to its run.
type InterventionRecord struct {
	Task   string `json:"task"`
	Arm    Arm    `json:"arm"`
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

// Build assembles the report from the ledger and the frozen manifest.
//
// Everything is derived here and nothing is passed in pre-computed, so the
// verdict is a function of committed records. Running it twice on the same
// ledger returns the same answer -- which is what makes the pre-registration
// meaningful rather than decorative.
// dirOfManifest is where transcripts live, so a scoring pass can re-derive an
// infrastructure cause the classifier missed when a record was written.
var dirOfManifest string

// SetCorpusRoot tells Build where committed transcripts are.
func SetCorpusRoot(dir string) { dirOfManifest = dir }

// readEvidence loads an attempt's committed transcript, if it referenced one.
func readEvidence(root string, a Attempt) string {
	p := a.Artifacts["transcript"]
	if strings.TrimSpace(p) == "" || strings.TrimSpace(root) == "" {
		return ""
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}

func Build(m Manifest, manifestHash string, l *Ledger, calibration []CalibrationResult) Report {
	current, orphaned := l.ForManifest(manifestHash)
	r := Report{
		Benchmark: m.Version, ManifestHash: manifestHash, Orphaned: len(orphaned),
		PrimaryTasks: len(m.Tasks), ArmSlots: len(m.Tasks) * len(Arms),
		Arms: map[Arm]ArmMetrics{}, Calibration: calibration,
		Caveat: fmt.Sprintf("This is an engineering evidence campaign over %d tasks, not a "+
			"population estimate. Intervals are wide by construction. Where the data supports "+
			"only \"promising\" or \"inconclusive\", it does not support \"proven\".", len(m.Tasks)),
	}

	// Group by task/arm, then score one attempt per pair under the retry rule.
	byPair := map[string][]Attempt{}
	for _, a := range current {
		byPair[a.Task+"/"+string(a.Arm)] = append(byPair[a.Task+"/"+string(a.Arm)], a)
	}
	scoredBy := map[string]Attempt{}
	for key, attempts := range byPair {
		if s, ok := Scored(attempts); ok {
			scoredBy[key] = s
			// A task/arm whose attempts span more than one provider identity is
			// disclosed, not silently resolved.
			//
			// Scored() takes the earliest SEMANTIC attempt, which is the right
			// rule against retry-until-green and the wrong one when an earlier
			// attempt ran under a configuration since corrected -- the RAW arms
			// that could not run tests are the live case. Choosing a different
			// attempt now, having seen which is which, is precisely the
			// after-the-fact rule change the campaign forbids. So the report
			// says the scored attempt may be under a superseded configuration
			// and names both, and a reader decides what that is worth.
			for _, other := range attempts {
				if same, why := SameProviders(s, other); !same {
					r.ProviderMismatch = append(r.ProviderMismatch,
						fmt.Sprintf("%s: scored attempt %d, but attempt %d ran a different "+
							"configuration (%s)", key, s.Number, other.Number, why))
					break
				}
			}
		}
	}

	perArm := map[Arm][]Attempt{}
	for _, t := range m.Tasks {
		row := TaskOutcome{Task: t.ID, Linked: t.Linked(), Verdict: map[Arm]Verdict{},
			Order: ExecutionOrder(t.ID)}
		for _, arm := range Arms {
			s, ok := scoredBy[t.ID+"/"+string(arm)]
			if !ok {
				row.Verdict[arm] = "NOT_EXECUTED"
				r.NotExecuted = append(r.NotExecuted, t.ID+"/"+string(arm))
				continue
			}
			row.Verdict[arm] = s.Verdict
			perArm[arm] = append(perArm[arm], s)
			if s.Verdict == NoResult {
				r.NoResults = append(r.NoResults, s.ID())
			}
			if falseGrant(s) {
				r.FalseGrants = append(r.FalseGrants, s.ID())
			}
			if falseBlock(s) {
				r.FalseBlocks = append(r.FalseBlocks, s.ID())
			}
			for _, iv := range s.Interventions {
				if iv.TechnicalAnswer() {
					r.Interventions = append(r.Interventions, InterventionRecord{
						Task: t.ID, Arm: arm, Kind: iv.Kind, Detail: iv.Detail})
				}
			}
		}
		r.PerTask = append(r.PerTask, row)
	}
	for _, arm := range Arms {
		r.Arms[arm] = Compute(arm, perArm[arm])
	}

	// The two-axis scoring, over the SCHEDULED denominator: every task in the
	// corpus, whether or not its arm ran. An arm nobody executed is an
	// end-to-end failure like any other, and letting the denominator shrink to
	// what happened to run measures a product's availability only on the days
	// it was up.
	r.Budget = OperationalBudget
	r.Rates = map[Arm]TwoRates{}
	for _, arm := range Arms {
		var rows []Scoring
		for _, t := range m.Tasks {
			s, ok := scoredBy[t.ID+"/"+string(arm)]
			if !ok {
				continue
			}
			rows = append(rows, Score(s, readEvidence(dirOfManifest, s)))
		}
		r.Rates[arm] = Rates(rows, len(m.Tasks))
	}

	// Linked specimens: the compounding evidence.
	for _, t := range m.Tasks {
		if !t.Linked() {
			continue
		}
		cold, okC := scoredBy[t.ID+"/"+string(ArmCold)]
		warm, okW := scoredBy[t.ID+"/"+string(ArmWarm)]
		if !okC || !okW {
			r.Linked = append(r.Linked, LinkedComparison{Task: t.ID, Comparable: false,
				Why: "one arm has no recorded attempt"})
			continue
		}
		same, why := SameProviders(cold, warm)
		if cold.Verdict == NoResult || warm.Verdict == NoResult {
			same, why = false, "an arm produced NO_RESULT"
		}
		r.Linked = append(r.Linked, CompareLinked(t.ID, cold, warm, same, why))
	}

	in := GateInput{PrimaryTasks: len(m.Tasks),
		ArmSlots: r.ArmSlots, Executed: r.ArmSlots - len(r.NotExecuted),
		Raw: r.Arms[ArmRaw], Cold: r.Arms[ArmCold], Warm: r.Arms[ArmWarm],
		FalseGrants: len(r.FalseGrants), Linked: r.Linked}
	// The pre-registered counts are END-TO-END questions: "at least 9/10 tasks
	// end CORRECT" asks whether the product delivered, not whether its code
	// would have been right had it finished. Mapped to the frozen axes here,
	// before any campaign result exists, so it is a definition rather than a
	// reinterpretation. The engineering-correctness rate is reported beside
	// them and gates nothing.
	delivered := map[string]map[Arm]bool{}
	for _, arm := range Arms {
		for _, row := range r.Rates[arm].Rows {
			if delivered[row.Task] == nil {
				delivered[row.Task] = map[Arm]bool{}
			}
			delivered[row.Task][arm] = row.Delivered
		}
	}
	for _, t := range m.Tasks {
		if delivered[t.ID][ArmCold] || delivered[t.ID][ArmWarm] {
			in.GovernedCorrect++
		}
		if delivered[t.ID][ArmRaw] {
			in.RawCorrect++
		}
	}
	for _, arm := range []Arm{ArmCold, ArmWarm} {
		for _, a := range perArm[arm] {
			if a.AutonomousCorrect() {
				in.GovernedAutonomous++
			}
			if a.BoundaryViolation() {
				in.BoundaryViolations++
			}
			if !a.BoundaryMeasurable {
				r.UnmeasurableBoundaries = append(r.UnmeasurableBoundaries, a.ID())
			}
			if nonConvergent(a) {
				in.UnclassifiedRunaway++
			}
		}
	}
	// Autonomous-correct is counted per TASK, not per arm run, so a task
	// correct under both COLD and WARM is not counted twice. It also requires
	// DELIVERY: a run that timed out cannot be autonomous-correct, whatever the
	// oracle said about its partial work.
	in.GovernedAutonomous = 0
	for _, t := range m.Tasks {
		for _, arm := range []Arm{ArmCold, ArmWarm} {
			if s, ok := scoredBy[t.ID+"/"+string(arm)]; ok && s.AutonomousCorrect() && delivered[t.ID][arm] {
				in.GovernedAutonomous++
				break
			}
		}
	}
	for _, l := range r.Linked {
		in.ColdRediscovery += l.ColdRedisc
		in.WarmRediscovery += l.WarmRedisc
	}
	rawCost, govCost := r.Arms[ArmRaw].CostUSD, r.Arms[ArmCold].CostUSD
	if rawCost.N > 0 && govCost.N > 0 && rawCost.Median > 0 {
		in.CostKnown = true
		in.GovernedCostRatio = govCost.Median / rawCost.Median
	}
	for _, c := range calibration {
		switch {
		case strings.Contains(strings.ToLower(c.Expected), "positive"), c.Expected == string(Correct):
			in.CalibrationPositiveOK = c.OK
		case strings.Contains(strings.ToLower(c.Expected), "non_conver"),
			strings.Contains(strings.ToLower(c.Expected), "negative"):
			in.CalibrationNegativeOK = c.OK
		}
	}

	r.Verdict, r.Gates = Evaluate(in)
	sort.Strings(r.FalseGrants)
	sort.Strings(r.FalseBlocks)
	sort.Strings(r.NoResults)
	return r
}

// JSON renders the machine-readable layer.
func (r Report) JSON() ([]byte, error) { return json.MarshalIndent(r, "", "  ") }

func pct(x Rate) string {
	if x.Empty {
		return "n/a (no eligible runs)"
	}
	return fmt.Sprintf("%d/%d = %.0f%% [%.0f–%.0f%%]", x.N, x.D, x.P*100, x.Lo*100, x.Hi*100)
}

func dist(d Dist, unit string) string {
	if d.N == 0 {
		if d.Missing > 0 {
			return fmt.Sprintf("unknown (%d observation(s) with no data)", d.Missing)
		}
		return "n/a"
	}
	s := fmt.Sprintf("median %.1f%s (range %.1f–%.1f, n=%d)", d.Median, unit, d.Min, d.Max, d.N)
	if d.Missing > 0 {
		s += fmt.Sprintf("; %d unknown", d.Missing)
	}
	return s
}

// Markdown renders the human-readable layer.
func (r Report) Markdown() string {
	var b strings.Builder
	f := func(format string, a ...any) { fmt.Fprintf(&b, format, a...) }

	f("# Proof campaign report — %s\n\n", r.Benchmark)
	f("**Verdict: %s**\n\n", r.Verdict)
	f("Manifest `%s`. %d primary task(s).\n\n", r.ManifestHash, r.PrimaryTasks)
	if r.Orphaned > 0 {
		f("> %d committed attempt(s) ran under a different manifest hash and are excluded from "+
			"every number below. They are not deleted; they belong to a different experiment.\n\n", r.Orphaned)
	}
	f("**Coverage: %d of %d designed arm slots were executed.** %d were never run, and are listed "+
		"below rather than omitted -- a report showing only the arms that happened to run would "+
		"describe a smaller, better-behaved campaign than the one that was designed.\n\n",
		r.ArmSlots-len(r.NotExecuted), r.ArmSlots, len(r.NotExecuted))
	f("%s\n\n", r.Caveat)

	f("## Calibration — can the instrument record a known win and a known failure?\n\n")
	if len(r.Calibration) == 0 {
		f("None recorded. The instrument is unvalidated.\n\n")
	}
	for _, c := range r.Calibration {
		mark := "FAILED TO REPRODUCE"
		if c.OK {
			mark = "reproduced"
		}
		f("- **%s** — expected `%s`, observed `%s`: **%s**. %s\n", c.Task, c.Expected, c.Observed, mark, c.Detail)
	}
	f("\n## Per-task outcome matrix\n\n")
	f("| task | linked | RAW | COLD | WARM |\n|---|---|---|---|---|\n")
	for _, t := range r.PerTask {
		linked := ""
		if t.Linked {
			linked = "yes"
		}
		f("| %s | %s | %s | %s | %s |\n", t.Task, linked,
			t.Verdict[ArmRaw], t.Verdict[ArmCold], t.Verdict[ArmWarm])
	}

	f("\n## The two rates\n\n")
	f("Correctness and delivery are separate axes and are never collapsed. A run that did not " +
		"reach an evaluable candidate is NOT_EVALUATED for correctness -- it cannot be called wrong " +
		"for code it never wrote -- and counts as a failure for end-to-end success, because a task " +
		"the product could not deliver on is a product failure whatever the code would have been.\n\n")
	f("Operational budget: **%s per arm, frozen**. The system cannot improve its score by taking "+
		"longer.\n\n", r.Budget)
	f("| arm | engineering correctness | end-to-end success | NOT_EVALUATED | terminals |\n")
	f("|---|---|---|---|---|\n")
	for _, arm := range Arms {
		t := r.Rates[arm]
		var terms []string
		for _, k := range []Terminal{TerminalCompleted, TerminalRefused, TerminalInfraFailure,
			TerminalTimeout, TerminalOtherFailure} {
			if n := t.Terminals[k]; n > 0 {
				terms = append(terms, fmt.Sprintf("%s %d", k, n))
			}
		}
		if len(terms) == 0 {
			terms = []string{"none run"}
		}
		f("| %s | %s | %s | %d | %s |\n", arm, pct(t.Engineering), pct(t.EndToEnd),
			t.NotEvaluated, strings.Join(terms, ", "))
	}
	f("\n*Engineering correctness is CORRECT / (CORRECT + INCORRECT): when the system produced an "+
		"evaluable solution, was it right? End-to-end success is delivered / all %d scheduled arms "+
		"per condition: could it be given a task and return a correct result inside the budget?*\n", r.PrimaryTasks)

	f("\n### Every arm, both axes\n\n")
	f("| attempt | terminal | correctness | delivered | wall | cause |\n|---|---|---|---|---|---|\n")
	for _, arm := range Arms {
		for _, row := range r.Rates[arm].Rows {
			d := ""
			if row.Delivered {
				d = "yes"
			}
			cause := row.Cause
			if row.ReclassifiedFrom != "" {
				cause += " [recorded " + row.ReclassifiedFrom + "]"
			}
			f("| %s | %s | %s | %s | %ds | %s |\n", row.Attempt, row.Terminal,
				row.Correctness, d, row.WallSeconds, cause)
		}
	}

	f("\n## Primary metrics\n\n")
	f("| metric | RAW | COLD | WARM |\n|---|---|---|---|\n")
	row := func(name string, get func(ArmMetrics) string) {
		f("| %s | %s | %s | %s |\n", name, get(r.Arms[ArmRaw]), get(r.Arms[ArmCold]), get(r.Arms[ArmWarm]))
	}
	row("runs recorded", func(m ArmMetrics) string { return fmt.Sprintf("%d", m.Runs) })
	row("NO_RESULT", func(m ArmMetrics) string { return fmt.Sprintf("%d", m.NoResults) })
	row("correct closure", func(m ArmMetrics) string { return pct(m.CorrectClosure) })
	row("autonomous-correct", func(m ArmMetrics) string { return pct(m.AutonomousCorrect) })
	row("human technical intervention", func(m ArmMetrics) string { return pct(m.HumanTechnical) })
	row("false grant", func(m ArmMetrics) string { return pct(m.FalseGrant) })
	row("false block (correct, stopped)", func(m ArmMetrics) string { return pct(m.FalseBlock) })
	row("non-convergent", func(m ArmMetrics) string { return pct(m.NonConvergent) })
	row("verification failure", func(m ArmMetrics) string { return pct(m.VerificationFail) })
	row("closure yield", func(m ArmMetrics) string { return pct(m.ClosureYield) })
	row("durable knowledge reuse", func(m ArmMetrics) string { return pct(m.KnowledgeUse) })
	row("review cycles", func(m ArmMetrics) string { return dist(m.ReviewCycles, "") })
	row("wall time", func(m ArmMetrics) string { return dist(m.WallMinutes, " min") })
	row("provider cost", func(m ArmMetrics) string { return dist(m.CostUSD, " USD") })
	row("rediscovery", func(m ArmMetrics) string { return dist(m.Rediscovery, "") })

	f("\n## Compounding — COLD vs WARM on linked specimens\n\n")
	if len(r.Linked) == 0 {
		f("No linked specimens scored.\n")
	} else {
		f("| task | comparable | COLD | WARM | rediscovery | human tech | cycles | improved |\n")
		f("|---|---|---|---|---|---|---|---|\n")
		for _, l := range r.Linked {
			c := "yes"
			if !l.Comparable {
				c = "no — " + l.Why
			}
			imp := "no"
			if l.Improved {
				imp = "**yes**"
			}
			if l.Regressed {
				imp = "**REGRESSED**"
			}
			f("| %s | %s | %s | %s | %d → %d | %d → %d | %d → %d | %s |\n", l.Task, c,
				l.ColdVerdict, l.WarmVerdict, l.ColdRedisc, l.WarmRedisc,
				l.ColdHuman, l.WarmHuman, l.ColdCycles, l.WarmCycles, imp)
		}
	}

	f("\n## Every human technical intervention\n\n")
	if len(r.Interventions) == 0 {
		f("None recorded.\n")
	}
	for _, iv := range r.Interventions {
		f("- `%s` / %s — %s: %s\n", iv.Task, iv.Arm, iv.Kind, iv.Detail)
	}
	f("\n## False grants and false blocks\n\n")
	f("- arm slots never executed (%d): %v\n", len(r.NotExecuted), orNone(r.NotExecuted))
	f("- provider-configuration mismatch (%d): %v\n", len(r.ProviderMismatch), orNone(r.ProviderMismatch))
	f("- false grants: %v\n- false blocks: %v\n- NO_RESULT attempts: %v\n"+
		"- boundary unmeasurable (governed checkout not quiescent at arm start): %v\n",
		orNone(r.FalseGrants), orNone(r.FalseBlocks), orNone(r.NoResults),
		orNone(r.UnmeasurableBoundaries))

	f("\n## Pre-registered gates\n\n")
	f("Thresholds were frozen in `docs/work/proof-before-mechanism.md` before any result existed " +
		"and are transcribed as constants in `gates.go`. This verdict is a function of the " +
		"committed run records.\n\n")
	f("| gate | kind | claim | result | detail |\n|---|---|---|---|---|\n")
	for _, g := range r.Gates {
		res := "pass"
		if g.Untestable {
			res = "**not testable**"
		} else if !g.Passed {
			res = "**fail**"
		}
		f("| %s | %s | %s | %s | %s |\n", g.ID, g.Gate, g.Claim, res, g.Detail)
	}
	f("\n**Verdict: %s**\n", r.Verdict)
	return b.String()
}

func orNone(s []string) string {
	if len(s) == 0 {
		return "none"
	}
	return strings.Join(s, ", ")
}

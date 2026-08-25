package proofbench

// The report: raw counts first, verdict derived last.
//
// Two layers, both required. The JSON is what a later campaign compares against;
// the markdown is what a person reads before deciding whether to believe it.
// Neither is allowed to contain a number the ledger does not support.

import (
	"encoding/json"
	"fmt"
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
func Build(m Manifest, manifestHash string, l *Ledger, calibration []CalibrationResult) Report {
	current, orphaned := l.ForManifest(manifestHash)
	r := Report{
		Benchmark: m.Version, ManifestHash: manifestHash, Orphaned: len(orphaned),
		PrimaryTasks: len(m.Tasks), Arms: map[Arm]ArmMetrics{}, Calibration: calibration,
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
		}
	}

	perArm := map[Arm][]Attempt{}
	for _, t := range m.Tasks {
		row := TaskOutcome{Task: t.ID, Linked: t.Linked(), Verdict: map[Arm]Verdict{},
			Order: ExecutionOrder(t.ID)}
		for _, arm := range Arms {
			s, ok := scoredBy[t.ID+"/"+string(arm)]
			if !ok {
				row.Verdict[arm] = NoResult
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
		Raw: r.Arms[ArmRaw], Cold: r.Arms[ArmCold], Warm: r.Arms[ArmWarm],
		FalseGrants: len(r.FalseGrants), Linked: r.Linked}
	for _, row := range r.PerTask {
		if row.Verdict[ArmCold] == Correct || row.Verdict[ArmWarm] == Correct {
			in.GovernedCorrect++
		}
		if row.Verdict[ArmRaw] == Correct {
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
	// correct under both COLD and WARM is not counted twice.
	in.GovernedAutonomous = 0
	for _, t := range m.Tasks {
		for _, arm := range []Arm{ArmCold, ArmWarm} {
			if s, ok := scoredBy[t.ID+"/"+string(arm)]; ok && s.AutonomousCorrect() {
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

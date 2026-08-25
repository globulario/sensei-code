package proofbench

// Attacks on the frozen scoring contract.
//
// The contract exists because proof-v4's calibration recorded two operational
// failures as wrong code. These pin the separation so it cannot quietly close
// again.

import (
	"strings"
	"testing"
)

// An operational failure is never a correctness verdict.
//
// The two live specimens: an unreachable graph backend, and an exhausted budget.
// Both had the oracle return INCORRECT -- against a partial or absent candidate
// -- and neither is a claim the campaign may make about the code.
func TestAnOperationalFailureIsNotWrongCode(t *testing.T) {
	for _, tc := range []struct {
		name     string
		attempt  Attempt
		evidence string
		wantTerm Terminal
	}{{
		name: "the graph backend was unreachable",
		attempt: Attempt{Task: "t", Arm: ArmCold, Number: 1, Terminal: "workflow.failed",
			Verdict: Incorrect},
		evidence: "preflight unavailable: awareness-graph backend is unreachable",
		wantTerm: TerminalInfraFailure,
	}, {
		name: "the operational budget ran out",
		attempt: Attempt{Task: "t", Arm: ArmCold, Number: 1, Terminal: "workflow.timed_out",
			Verdict: Incorrect},
		wantTerm: TerminalTimeout,
	}} {
		t.Run(tc.name, func(t *testing.T) {
			s := Score(tc.attempt, tc.evidence)
			if s.Terminal != tc.wantTerm {
				t.Fatalf("terminal = %s, want %s", s.Terminal, tc.wantTerm)
			}
			if s.Correctness != NotEvaluated {
				t.Fatalf("correctness = %s; the run never reached a point where its code could "+
					"be judged", s.Correctness)
			}
			if s.Delivered {
				t.Error("an arm that did not deliver was counted as delivery")
			}
			if s.Cause == "" {
				t.Error("a NOT_EVALUATED with no cause is a shrug")
			}
			// The record stands as written, and the reclassification is disclosed.
			if !strings.Contains(s.ReclassifiedFrom, "INCORRECT") {
				t.Errorf("the stored verdict was not disclosed: %q", s.ReclassifiedFrom)
			}
		})
	}
}

// A refusal is delivery failure, not infrastructure and not wrong code.
//
// The difference between "the service was down" and "the service declined" is
// the difference between an excuse and a decision, and the campaign must not
// let the product hide one behind the other.
func TestARefusalIsItsOwnThing(t *testing.T) {
	s := Score(Attempt{Task: "t", Arm: ArmCold, Number: 1,
		Terminal: "workflow.failed", Verdict: NoResult},
		"cannot establish authority for this plan: preflight degraded")
	if s.Terminal != TerminalRefused {
		t.Fatalf("terminal = %s, want REFUSED", s.Terminal)
	}
	if s.Correctness != NotEvaluated || s.Delivered {
		t.Error("a refusal produced a correctness observation or counted as delivery")
	}
	// A human-authority stop is also a refusal.
	if got := (Attempt{Terminal: "workflow.awaiting_authority"}).WorkflowTerminal(""); got != TerminalRefused {
		t.Errorf("awaiting authority = %s, want REFUSED", got)
	}
}

// Only a run that completed AND was judged correct counts as delivery.
func TestDeliveryRequiresBoth(t *testing.T) {
	ok := Score(Attempt{Task: "t", Arm: ArmRaw, Number: 1, Terminal: "raw.completed",
		Verdict: Correct}, "")
	if !ok.Delivered || ok.Correctness != CorrectnessCorrect || ok.Terminal != TerminalCompleted {
		t.Fatalf("a completed correct run was not delivery: %+v", ok)
	}
	// Completed but wrong is a real correctness observation and not delivery.
	bad := Score(Attempt{Task: "t", Arm: ArmRaw, Number: 1, Terminal: "raw.completed",
		Verdict: Incorrect}, "")
	if bad.Delivered {
		t.Error("an incorrect candidate counted as delivery")
	}
	if bad.Correctness != CorrectnessIncorrect {
		t.Errorf("a completed run judged wrong is a genuine INCORRECT, got %s", bad.Correctness)
	}
}

// The two rates answer two different questions and have different denominators.
//
// Engineering correctness excludes NOT_EVALUATED entirely; end-to-end success
// divides by every SCHEDULED arm, including ones that never ran. Letting the
// end-to-end denominator shrink to what happened to run measures a product's
// availability only on the days it was up.
func TestTheTwoRatesHaveDifferentDenominators(t *testing.T) {
	rows := []Scoring{
		{Correctness: CorrectnessCorrect, Terminal: TerminalCompleted, Delivered: true},
		{Correctness: CorrectnessIncorrect, Terminal: TerminalCompleted},
		{Correctness: NotEvaluated, Terminal: TerminalTimeout},
		{Correctness: NotEvaluated, Terminal: TerminalInfraFailure},
	}
	// Ten arms scheduled; four ran.
	r := Rates(rows, 10)
	if r.Engineering.N != 1 || r.Engineering.D != 2 {
		t.Errorf("engineering correctness = %d/%d, want 1/2 (NOT_EVALUATED excluded)",
			r.Engineering.N, r.Engineering.D)
	}
	if r.EndToEnd.N != 1 || r.EndToEnd.D != 10 {
		t.Errorf("end-to-end = %d/%d, want 1/10 (every scheduled arm)", r.EndToEnd.N, r.EndToEnd.D)
	}
	if r.NotEvaluated != 2 {
		t.Errorf("NOT_EVALUATED = %d, want 2", r.NotEvaluated)
	}
	if r.Terminals[TerminalTimeout] != 1 || r.Terminals[TerminalInfraFailure] != 1 {
		t.Errorf("causes were not preserved: %v", r.Terminals)
	}
	// A corpus with no evaluable run has NO engineering rate, rather than 0%.
	none := Rates([]Scoring{{Correctness: NotEvaluated, Terminal: TerminalTimeout}}, 10)
	if !none.Engineering.Empty {
		t.Error("an arm with zero correctness observations reported a 0% correctness rate; " +
			"that is an accusation the evidence cannot support")
	}
	if none.EndToEnd.D != 10 {
		t.Error("end-to-end lost its scheduled denominator")
	}
}

// The operational budget is frozen, and the manifest cannot quietly differ.
func TestTheBudgetIsFrozen(t *testing.T) {
	if OperationalBudget != "22m" {
		t.Fatalf("the operational budget moved to %s. It was 22m when a COLD arm exhausted it, "+
			"and raising it afterwards moves the finish line for the runner that failed to reach "+
			"it. A longer allowance is a different experiment and needs its own manifest.",
			OperationalBudget)
	}
	m := soundManifest()
	m.OperationalBudget = "45m"
	if err := m.Validate(); err == nil {
		t.Error("a manifest declaring a different budget was accepted")
	}
	m.OperationalBudget = OperationalBudget
	if err := m.Validate(); err != nil {
		t.Errorf("a manifest declaring the frozen budget was refused: %v", err)
	}
}

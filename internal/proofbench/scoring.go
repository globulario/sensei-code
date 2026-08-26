package proofbench

// The scoring contract. Frozen before the campaign, and deliberately two axes.
//
// proof-v4's calibration produced a table that read as an accusation it could
// not support:
//
//	COLD  workflow.failed      INCORRECT   137s   (the graph was unreachable)
//	COLD  workflow.timed_out   INCORRECT  1320s   (the budget ran out)
//
// Neither run reached the point where its code could be judged, and both were
// recorded as wrong code. That is the same defect as scoring an arm that never
// authenticated: an operational failure wearing a correctness verdict.
//
// So correctness and delivery are separated, and neither is allowed to stand in
// for the other:
//
//	CORRECTNESS   CORRECT | INCORRECT | NOT_EVALUATED
//	TERMINAL      COMPLETED | REFUSED | INFRA_FAILURE | TIMEOUT | OTHER_FAILURE
//
// and two rates are reported, answering two different questions:
//
//	engineering correctness = CORRECT / (CORRECT + INCORRECT)
//	    when the system produces an evaluable solution, is it right?
//
//	end-to-end success      = CORRECT completions / ALL SCHEDULED ARMS
//	    can I give this a task and get a correct result inside the budget?
//
// # The budget is frozen at 22 minutes
//
// It was 22 minutes when a COLD arm exhausted it, and it stays 22 minutes.
// Raising it now -- after seeing the governed lane hit it -- would move the
// finish line for the runner that failed to reach it, and the resulting number
// would describe a benchmark tuned to its subject.
//
// A timeout is therefore NOT_EVALUATED for correctness and a FAILURE for
// end-to-end success. The system cannot improve its score by taking longer, and
// cannot be called wrong for never answering. A diagnostic campaign at 45 or 60
// minutes is a different experiment and needs its own manifest.

import "strings"

// Correctness is what the oracle established about the code.
type Correctness string

const (
	// CorrectnessCorrect: the oracle ran against a real candidate and passed it.
	CorrectnessCorrect Correctness = "CORRECT"
	// CorrectnessIncorrect: the oracle ran against a real candidate and failed
	// it. This is a claim about the CODE, and nothing else may produce it.
	CorrectnessIncorrect Correctness = "INCORRECT"
	// NotEvaluated: no correctness observation exists.
	//
	// Not a middle value between right and wrong -- the absence of a reading.
	// It contributes to no correctness rate in either direction, and it counts
	// as a failure for end-to-end success, because a task the product could not
	// deliver on is a product failure whatever the code would have been.
	NotEvaluated Correctness = "NOT_EVALUATED"
)

// Terminal is how the run ended, independently of what the code was worth.
type Terminal string

const (
	// TerminalCompleted: the run reached its own successful ending.
	TerminalCompleted Terminal = "COMPLETED"
	// TerminalRefused: governance stopped it, or it asked for a human. The
	// product working as designed, and still a delivery failure.
	TerminalRefused Terminal = "REFUSED"
	// TerminalInfraFailure: something outside the product failed -- provider
	// quota, auth, an unreachable graph backend.
	TerminalInfraFailure Terminal = "INFRA_FAILURE"
	// TerminalTimeout: the operational budget ran out.
	TerminalTimeout Terminal = "TIMEOUT"
	// TerminalOtherFailure: a failure the harness has no reading for. Last, and
	// deliberately not a synonym for any of the above.
	TerminalOtherFailure Terminal = "OTHER_FAILURE"
)

// OperationalBudget is the frozen per-arm allowance for benchmark v1.
//
// Stated as a constant so raising it is a diff on this file with this comment
// attached, rather than a flag someone passes on a slow evening.
const OperationalBudget = "22m"

// refusalMarkers are the ways the product says "I will not proceed".
//
// A refusal is the product working: the gate held, or a human was asked. It is
// still a delivery failure, and it is emphatically not an INFRA_FAILURE -- the
// difference between "the service was down" and "the service declined" is the
// difference between an excuse and a decision.
var refusalMarkers = []string{
	"cannot establish authority", "will not certify", "human declined",
	"requires approval", "awaiting", "declined this architectural change",
	"the human declined",
}

// WorkflowTerminal derives the delivery axis from what was recorded.
//
// evidence is the run's transcript when one is available. It is consulted only
// to recognise an infrastructure cause the classifier missed when the record was
// written -- the graph-outage arm is the live case. Re-deriving from preserved
// evidence is not rewriting the record: the record stays exactly as written and
// the report says the classification came from its transcript.
func (a Attempt) WorkflowTerminal(evidence string) Terminal {
	// A specific structured terminal is authoritative.
	//
	// The engine named this outcome through its own documented exit contract,
	// and no phrase found in a transcript may overrule it. Instrument defect
	// #13 was exactly this: a run that exhausted its budget while working was
	// recorded as an outage because "backend is unreachable" appeared somewhere
	// in twenty-two minutes of output, on a task about transporting diff
	// evidence across the MCP boundary.
	//
	// The bias mattered more than the misreading. INFRA_FAILURE excuses the
	// product and TIMEOUT counts against it, so the defect leaned the
	// instrument toward its own subject.
	//
	// Attempts recorded before harness v2 carry no TerminalSource and keep the
	// old precedence exactly. Rescoring a frozen campaign under a rule written
	// afterwards is the same offence pointed the other way.
	if TerminalSource(a.TerminalSource) == TerminalStructuredSpecific {
		if t, ok := structuredTerminal(a.Terminal); ok {
			return t
		}
	}
	if strings.TrimSpace(a.Infrastructure) != "" {
		return TerminalInfraFailure
	}
	if strings.Contains(a.Terminal, "timed_out") || strings.Contains(a.Terminal, "timeout") {
		return TerminalTimeout
	}
	switch a.Terminal {
	case "workflow.completed", "raw.completed", "accepted", "retained":
		return TerminalCompleted
	case "workflow.awaiting_authority":
		return TerminalRefused
	case "raw.not_configured", "workflow.not_started":
		return TerminalInfraFailure
	}
	// A failure whose cause is only in the transcript.
	haystack := strings.ToLower(evidence + "\n" + a.OracleDetail + "\n" + a.Notes)
	if infrastructureReason(haystack) != "" {
		return TerminalInfraFailure
	}
	for _, m := range refusalMarkers {
		if strings.Contains(haystack, m) {
			return TerminalRefused
		}
	}
	return TerminalOtherFailure
}

// Correctness derives the code axis.
//
// The rule that matters: a run that did not DELIVER cannot be judged on what it
// delivered. An infrastructure failure and a timeout are NOT_EVALUATED whatever
// the oracle happened to return, because the oracle ran against a partial or
// absent candidate and its answer is about that, not about the system's ability
// to write correct code.
func (a Attempt) Correctness(term Terminal) Correctness {
	switch term {
	case TerminalInfraFailure, TerminalTimeout, TerminalRefused, TerminalOtherFailure:
		// Every non-delivery terminal. A run that stopped to ask a human, ran
		// out of budget, hit an outage, or failed for a reason nobody has a
		// reading for did not reach an evaluable candidate, so the oracle's
		// answer is about partial or absent work rather than about the system's
		// ability to write correct code.
		//
		// This was narrower than the frozen contract until the COLD wave
		// produced its first workflow.awaiting_authority: the contract says "a
		// run that did not reach an evaluable candidate is NOT_EVALUATED,
		// whatever the oracle returned", and the code only implemented it for
		// timeouts and outages. Scoring a REFUSED arm INCORRECT attributes to
		// the CODE what was a delivery decision.
		//
		// Nothing recorded changes. The ledger stores the terminal status and
		// the raw oracle verdict; both axes are derived here at report time.
		return NotEvaluated
	}
	switch a.Verdict {
	case Correct:
		return CorrectnessCorrect
	case Incorrect:
		return CorrectnessIncorrect
	}
	// INCONCLUSIVE and NO_RESULT are absences of a reading, not verdicts.
	return NotEvaluated
}

// Delivered reports an arm that reached a correct result inside the budget.
//
// The end-to-end numerator: completed AND correct. A refusal is not delivery
// however right the product was to refuse, and a correct candidate that arrived
// after a timeout did not arrive.
func (a Attempt) Delivered(term Terminal, corr Correctness) bool {
	return term == TerminalCompleted && corr == CorrectnessCorrect
}

// Scoring is one arm's two-axis result, with the cause kept beside it.
type Scoring struct {
	Attempt     string      `json:"attempt"`
	Task        string      `json:"task"`
	Arm         Arm         `json:"arm"`
	Terminal    Terminal    `json:"workflow_terminal"`
	Correctness Correctness `json:"correctness"`
	Delivered   bool        `json:"delivered"`
	// Cause is what actually ended the run, preserved separately so a
	// NOT_EVALUATED is never a shrug.
	Cause string `json:"cause,omitempty"`
	// WallSeconds and ReviewCycles are kept on the scoring row because "what
	// consumed the budget" is the question a timeout raises.
	WallSeconds  int `json:"wall_seconds"`
	ReviewCycles int `json:"review_cycles"`
	// ReclassifiedFrom records that the stored verdict differs from the derived
	// one, and why. Disclosure, not correction: the record is unchanged.
	ReclassifiedFrom string `json:"reclassified_from,omitempty"`
}

// Score derives both axes for one attempt.
func Score(a Attempt, evidence string) Scoring {
	term := a.WorkflowTerminal(evidence)
	corr := a.Correctness(term)
	s := Scoring{
		Attempt: a.ID(), Task: a.Task, Arm: a.Arm,
		Terminal: term, Correctness: corr, Delivered: a.Delivered(term, corr),
		WallSeconds: a.WallSecs, ReviewCycles: a.ReviewCycles,
	}
	switch term {
	case TerminalInfraFailure:
		s.Cause = "infrastructure: " + firstNonEmpty(a.Infrastructure, infrastructureReason(
			strings.ToLower(evidence+"\n"+a.OracleDetail)), "an externally attributable failure")
	case TerminalTimeout:
		s.Cause = "the " + OperationalBudget + " operational budget ran out before an evaluable candidate existed"
	case TerminalRefused:
		s.Cause = "the product refused or asked for a human: " + a.Terminal
	case TerminalOtherFailure:
		s.Cause = a.Terminal
	}
	if string(a.Verdict) != string(corr) && corr == NotEvaluated {
		s.ReclassifiedFrom = string(a.Verdict) +
			" — the record stands as written; the run did not reach a point where its code could be judged"
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// TwoRates is the pair the campaign reports, never collapsed into one number.
type TwoRates struct {
	// Engineering is CORRECT / (CORRECT + INCORRECT): when the system produced
	// an evaluable solution, was it right?
	Engineering Rate `json:"engineering_correctness"`
	// EndToEnd is delivered / ALL SCHEDULED ARMS: could the product be given a
	// task and return a correct result inside the budget?
	EndToEnd Rate `json:"end_to_end_success"`
	// NotEvaluated is how many arms produced no correctness observation, and
	// Terminals says why. Without these the engineering rate reads as though it
	// were over the whole corpus.
	NotEvaluated int              `json:"not_evaluated"`
	Terminals    map[Terminal]int `json:"terminals"`
	Scheduled    int              `json:"scheduled_arms"`
	Rows         []Scoring        `json:"rows"`
}

// Rates computes both, over a scheduled denominator the caller states.
//
// scheduled is passed in rather than derived from len(rows), because the
// end-to-end question is about arms the campaign INTENDED to run. An arm that
// was never executed is an end-to-end failure like any other, and letting the
// denominator shrink to what happened to run is how a product's availability
// gets measured only on the days it was up.
func Rates(rows []Scoring, scheduled int) TwoRates {
	t := TwoRates{Scheduled: scheduled, Terminals: map[Terminal]int{}, Rows: rows}
	correct, incorrect, delivered := 0, 0, 0
	for _, r := range rows {
		t.Terminals[r.Terminal]++
		switch r.Correctness {
		case CorrectnessCorrect:
			correct++
		case CorrectnessIncorrect:
			incorrect++
		default:
			t.NotEvaluated++
		}
		if r.Delivered {
			delivered++
		}
	}
	t.Engineering = NewRate(correct, correct+incorrect)
	t.EndToEnd = NewRate(delivered, scheduled)
	return t
}

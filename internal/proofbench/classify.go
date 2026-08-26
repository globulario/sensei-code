package proofbench

// Deciding why an arm ended, and being able to show the working.
//
// Instrument defect #13, found when REPAIR_VERIFICATION's first arm recorded
// BOTH `workflow.timed_out` and `infrastructure: backend is unreachable`. The
// scorer read the infrastructure field first, so a run that exhausted its
// 22-minute budget while actively investigating the task was recorded as an
// outage.
//
// Two things were wrong, and the second is the worse one.
//
//  1. An unanchored substring decided the verdict. "backend is unreachable" was
//     matched somewhere in twenty-two minutes of transcript for a task that is
//     ABOUT transporting diff evidence across the MCP boundary -- a subject on
//     which those words are ordinary prose. Same shape as the earlier bug where
//     a commit hash containing "403" read as an HTTP failure.
//
//  2. The evidence was thrown away. Only the last 20KB was retained, and the
//     matched phrase was not in it, so the classification could be neither
//     confirmed nor refuted. An unfalsifiable verdict is not a measurement.
//
// And the error was not symmetric. INFRA_FAILURE excuses the product; TIMEOUT
// counts against it. A rule that silently converts the second into the first
// biases the instrument in the subject's favour, which is the one direction a
// benchmark must never lean.
//
// So: a specific structured terminal wins outright, text may only fill in a
// cause the engine left generic, and whatever the classifier matched is
// preserved with its surrounding context.

import "strings"

// TerminalSource records who named the outcome.
type TerminalSource string

const (
	// TerminalStructuredSpecific: the engine named a specific outcome through
	// its documented exit contract -- completed, awaiting authority, stopped,
	// timed out, observed. Authoritative. No text heuristic may override it.
	TerminalStructuredSpecific TerminalSource = "structured_specific"
	// TerminalStructuredGeneric: the engine reported failure without naming a
	// cause. The only case where text classification decides anything.
	TerminalStructuredGeneric TerminalSource = "structured_generic"
	// TerminalProcessFailure: the binary never ran. Infrastructure by
	// construction, and not a reading about the product at all.
	TerminalProcessFailure TerminalSource = "process_start_failure"
)

// terminalSource classifies the engine's exit code by how much it says.
//
// From `sensei-code run`'s documented contract: 0 complete, 1 failed, 3
// awaiting human authority, 4 stopped, 5 timed out, 6 observed. Every code but
// 1 names a specific outcome. Code 1 says only "failed", and an unrecognised
// code says less than that -- those are where a transcript may be consulted.
func terminalSource(code int) TerminalSource {
	switch code {
	case 0, 3, 4, 5, 6:
		return TerminalStructuredSpecific
	}
	return TerminalStructuredGeneric
}

// ClassifierEvidence is the span that produced a classification.
//
// Recorded so an INFRA_FAILURE can be audited. Without it the harness is asking
// to be believed, and the whole campaign exists because that is not good enough.
type ClassifierEvidence struct {
	// Signal is the phrase that matched.
	Signal string `json:"signal"`
	// Offset is where it matched in the complete stream.
	Offset int `json:"byte_offset"`
	// Context is the surrounding text, so a reader can see whether the phrase
	// was a report of failure or the model discussing one.
	Context string `json:"context"`
	// Decided says whether this match actually set the terminal, or was
	// recorded and overruled by a specific structured terminal.
	Decided bool `json:"decided"`
	// OverruledBy names the structured terminal that took precedence.
	OverruledBy string `json:"overruled_by,omitempty"`
}

// classifierContext is how much surrounding text is preserved around a match.
//
// Generous on purpose: the cost is bytes in a JSON record, and the failure this
// repairs was caused by not having enough of them.
const classifierContext = 400

// findInfrastructureEvidence locates the first recognised infrastructure signal
// and preserves where it was found.
func findInfrastructureEvidence(stream string) *ClassifierEvidence {
	low := strings.ToLower(stream)
	best, bestAt := "", -1
	for _, s := range infrastructureSignals {
		if i := strings.Index(low, s); i >= 0 && (bestAt < 0 || i < bestAt) {
			best, bestAt = s, i
		}
	}
	for _, s := range statusCodeContext {
		if i := strings.Index(low, s); i >= 0 && (bestAt < 0 || i < bestAt) {
			best, bestAt = s, i
		}
	}
	if bestAt < 0 {
		return nil
	}
	from := bestAt - classifierContext
	if from < 0 {
		from = 0
	}
	to := bestAt + len(best) + classifierContext
	if to > len(stream) {
		to = len(stream)
	}
	return &ClassifierEvidence{Signal: best, Offset: bestAt, Context: stream[from:to]}
}

// classifyInfrastructure applies the text detector under the precedence rule.
//
// The rule in one line: the engine's own specific terminal wins, and a matched
// phrase that loses is recorded rather than discarded.
func (o *ArmOutcome) classifyInfrastructure(stream string, code int) {
	if code == 0 {
		return // a completed run has no failure to attribute
	}
	ev := findInfrastructureEvidence(stream)
	if ev == nil {
		return
	}
	if o.TerminalSource == TerminalStructuredSpecific {
		ev.Decided = false
		ev.OverruledBy = o.Terminal
		o.InfrastructureHint = ev.Signal
		o.Classifier = ev
		return
	}
	ev.Decided = true
	o.Infrastructure = ev.Signal
	o.Classifier = ev
}

// structuredTerminal maps an engine-named outcome onto the frozen scoring axis.
//
// Only the outcomes the engine states unambiguously. "stopped" and "observed"
// are deliberately absent: neither has a settled meaning on the delivery axis
// yet, and inventing one here would be a scoring change smuggled in as a bug
// fix.
func structuredTerminal(terminal string) (Terminal, bool) {
	switch terminal {
	case "workflow.completed":
		return TerminalCompleted, true
	case "workflow.timed_out":
		return TerminalTimeout, true
	case "workflow.awaiting_authority":
		return TerminalRefused, true
	}
	return "", false
}

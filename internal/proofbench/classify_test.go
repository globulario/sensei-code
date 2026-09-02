package proofbench

import (
	"os"
	"strings"
	"testing"
)

// The defect this file exists for.
//
// REPAIR_VERIFICATION arm 1 exhausted its 22-minute budget while actively
// investigating the task, and was recorded as an infrastructure outage because
// the phrase "backend is unreachable" appeared somewhere in its transcript --
// on a task about transporting diff evidence across the MCP boundary, where
// those words are ordinary prose.
func TestAStructuredTimeoutIsNeverRelabelledAnOutage(t *testing.T) {
	stream := strings.Repeat("thinking about transport failures\n", 200) +
		`the reviewer asks what happens when the backend is unreachable mid-audit` +
		strings.Repeat("\nmore investigation\n", 200)

	o := &ArmOutcome{Terminal: "workflow.timed_out", TerminalSource: terminalSource(5)}
	o.classifyInfrastructure(stream, 5)

	if o.Infrastructure != "" {
		t.Fatalf("a specific structured terminal was overridden by prose: %q", o.Infrastructure)
	}
	if o.InfrastructureHint != "backend is unreachable" {
		t.Fatalf("the overruled signal was discarded instead of recorded: %q", o.InfrastructureHint)
	}
	if o.Classifier == nil || o.Classifier.Decided {
		t.Fatal("the classification evidence must be preserved and marked as not deciding")
	}
	if o.Classifier.OverruledBy != "workflow.timed_out" {
		t.Fatalf("the evidence must name what overruled it, got %q", o.Classifier.OverruledBy)
	}

	a := Attempt{Terminal: o.Terminal, TerminalSource: string(o.TerminalSource),
		Infrastructure: o.Infrastructure}
	if got := a.WorkflowTerminal(stream); got != TerminalTimeout {
		t.Fatalf("scored %s; a run that exhausted its budget is a TIMEOUT and counts against "+
			"end-to-end success, not an outage that excuses it", got)
	}
}

// The bias is the reason this matters. INFRA_FAILURE excuses the product;
// TIMEOUT counts against it.
func TestTheRepairRemovesABiasInTheProductsFavour(t *testing.T) {
	stream := "fatal: awareness-graph backend is unreachable"
	specific := Attempt{Terminal: "workflow.timed_out",
		TerminalSource: string(TerminalStructuredSpecific)}
	if specific.WorkflowTerminal(stream) != TerminalTimeout {
		t.Fatal("a named timeout must stay a timeout even when the transcript mentions an outage")
	}
	// A GENERIC failure is exactly where text is still allowed to decide.
	generic := &ArmOutcome{Terminal: "workflow.failed", TerminalSource: terminalSource(1)}
	generic.classifyInfrastructure(stream, 1)
	if generic.Infrastructure == "" {
		t.Fatal("a failure the engine left unexplained must still admit text classification")
	}
	if generic.Classifier == nil || !generic.Classifier.Decided {
		t.Fatal("a deciding classification must record that it decided")
	}
	ga := Attempt{Terminal: generic.Terminal, TerminalSource: string(generic.TerminalSource),
		Infrastructure: generic.Infrastructure}
	if ga.WorkflowTerminal(stream) != TerminalInfraFailure {
		t.Fatal("a genuine unexplained outage must still score INFRA_FAILURE")
	}
}

// A verdict nobody can check is not a measurement.
func TestAClassificationPreservesTheTextThatProducedIt(t *testing.T) {
	pre := strings.Repeat("A", 5000)
	post := strings.Repeat("B", 5000)
	stream := pre + "connection refused" + post

	o := &ArmOutcome{Terminal: "workflow.failed", TerminalSource: terminalSource(1)}
	o.classifyInfrastructure(stream, 1)

	if o.Classifier == nil {
		t.Fatal("no evidence was preserved")
	}
	if o.Classifier.Offset != len(pre) {
		t.Fatalf("offset %d does not locate the match at %d", o.Classifier.Offset, len(pre))
	}
	if !strings.Contains(o.Classifier.Context, "connection refused") {
		t.Fatal("the preserved context does not contain the matched phrase")
	}
	if len(o.Classifier.Context) < 400 {
		t.Fatalf("context of %d bytes is too little to judge whether the phrase was a report "+
			"of failure or a discussion of one", len(o.Classifier.Context))
	}
}

// Rescoring a frozen campaign under a rule written afterwards is the same
// offence pointed the other way.
func TestAttemptsRecordedBeforeTheRepairKeepTheirOriginalPrecedence(t *testing.T) {
	old := Attempt{Terminal: "workflow.timed_out", Infrastructure: "backend is unreachable"}
	if old.TerminalSource != "" {
		t.Fatal("fixture is not a pre-v2 attempt")
	}
	if got := old.WorkflowTerminal(""); got != TerminalInfraFailure {
		t.Fatalf("a proof-v6 attempt rescored to %s; frozen results must not move under a "+
			"rule written after they were taken", got)
	}
}

// The real evidence, kept as a fixture so the parser is tested against what a
// provider actually emits rather than what the harness imagines it emits.
func TestCampaignCapacityIsJudgedOnTheBindingWindow(t *testing.T) {
	b, err := os.ReadFile(
		"../../benchmark/repair-verification-v1/transcripts/internal-gitx-a4fa351/COLD/1.log")
	if err != nil {
		// Tracked fixture: the comment above says it is kept "so the parser is
		// tested against what a provider actually emits". If it disappears the
		// parser is tested against nothing.
		t.Fatalf("arm 1 transcript is absent: %v; it is tracked in this repository as the "+
			"fixture this parser is tested against", err)
	}
	q, err := parseQuota(string(b))
	if err != nil {
		t.Fatalf("the provider's own rate-limit event was not read: %v", err)
	}
	window, available := q.Tightest()
	if window != "seven_day" {
		t.Fatalf("tightest window %q; the five-hour window had just reset, which is exactly why "+
			"a gate that inspects one window admitted a campaign that could not finish", window)
	}
	if available > 0.05 {
		t.Fatalf("available %.3f; the seven-day window was at 96%%", available)
	}
	if err := AdmitCampaign(q, 11, 0.02); err == nil {
		t.Fatal("eleven arms admitted on 4% of the binding window -- the original defect")
	}
	// One arm's worth of headroom is not the question a campaign asks.
	if err := AdmitCampaign(q, 1, 0.02); err != nil {
		t.Fatalf("a single arm should still fit in 4%%: %v", err)
	}
}

func TestCapacityRefusesToGuessWhenNothingHasBeenMeasured(t *testing.T) {
	q := QuotaReading{Windows: map[string]float64{"seven_day": 0.10}}
	if err := AdmitCampaign(q, 11, 0); err == nil {
		t.Fatal("admitted a campaign with no per-arm estimate; an invented constant is exactly " +
			"the unfounded number this benchmark exists to avoid")
	}
}

// The projection must be driven by the arms that do work, not diluted by cheap
// refusals.
func TestPerArmCostUsesTheWorstObservedArm(t *testing.T) {
	mk := func(before, after float64) Attempt {
		return Attempt{
			QuotaBefore: &QuotaReading{Windows: map[string]float64{"seven_day": before}},
			QuotaAfter:  &QuotaReading{Windows: map[string]float64{"seven_day": after}},
		}
	}
	got := RecordedPerArm([]Attempt{
		mk(0.10, 0.11), // a cheap refusal
		mk(0.11, 0.16), // an arm that did real work
		mk(0.98, 0.02), // the window reset mid-arm: not evidence about cost
	})
	if got < 0.049 || got > 0.051 {
		t.Fatalf("per-arm cost %.4f; expected the worst real arm (0.05), so a few cheap "+
			"refusals cannot hide the cost of the arms that do work", got)
	}
}

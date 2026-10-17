package proofbench

// RULING-376: exit 11 (workspace quarantined) is a specific governed terminal.
//
// `sensei-code run` exits 11 when the invocation ended with workspace
// containment unresolved. Before this correction the harness mapped it to
// workflow.exit_11 and treated it as a GENERIC failure, so an infrastructure
// phrase anywhere in the transcript decided the arm as INFRA and licensed a
// retry. A quarantine is not an outage, not a failure the engine left
// unexplained, and not a completed change.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const r376Quarantined = "workflow.workspace_quarantined"

// r376Phrase is a recognised infrastructure signal, placed in the transcript
// the way incidental prose would carry it.
const r376Phrase = "backend is unreachable"

func r376Stream() string {
	return `{"kind":"review.started","summary":"cycle 1"}` + "\n" +
		strings.Repeat("investigating the transport layer\n", 40) +
		"the reviewer asks what happens when the graph " + r376Phrase +
		" mid-audit\n" + strings.Repeat("more investigation\n", 40)
}

// r376Execute drives the real ExecuteGoverned path against a stand-in binary
// that prints stream and exits with code.
func r376Execute(t *testing.T, code int, stream string) ArmOutcome {
	t.Helper()
	dir := t.TempDir()
	body := filepath.Join(dir, "stream.txt")
	if err := os.WriteFile(body, []byte(stream), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "sensei-code")
	script := "#!/bin/sh\ncat '" + body + "'\nexit " + itoaR376(code) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	o := Runner{Binary: bin}.ExecuteGoverned(context.Background(), dir, "task", time.Minute)
	if o.TerminalSource == TerminalProcessFailure {
		t.Fatalf("stand-in engine did not start: %s", o.Infrastructure)
	}
	if o.Raw != stream {
		t.Fatalf("captured stream differs from what the engine printed")
	}
	return o
}

func itoaR376(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// r376Attempt records an outcome the way cmd/proofbench does.
func r376Attempt(o ArmOutcome, n int, v Verdict) Attempt {
	return Attempt{Task: "t", Arm: ArmCold, Number: n, Verdict: v,
		Terminal: o.Terminal, TerminalSource: string(o.TerminalSource),
		Infrastructure: o.Infrastructure, InfrastructureHint: o.InfrastructureHint,
		Classifier: o.Classifier}
}

// ---- Pre-fix controls (assert the SAFE property; they fail on 183be5e) ----

func TestP376C1GovernedExit11IsNotAnAnonymousExitCode(t *testing.T) {
	if got := governedExit(11); got == "workflow.exit_11" || got != r376Quarantined {
		t.Fatalf("governedExit(11) = %q, want %q", got, r376Quarantined)
	}
}

func TestP376C2Exit11IsNotAGenericTerminal(t *testing.T) {
	if got := terminalSource(11); got == TerminalStructuredGeneric {
		t.Fatalf("terminalSource(11) = %s; a quarantine is named, not unexplained", got)
	}
}

func TestP376C3InfraPhraseDoesNotSetInfrastructureOnExit11(t *testing.T) {
	o := r376Execute(t, 11, r376Stream())
	if o.Infrastructure != "" {
		t.Fatalf("an exit-11 arm was decided as infrastructure from transcript text: %q",
			o.Infrastructure)
	}
}

func TestP376C4Exit11ArmIsNotInfraRetryEligible(t *testing.T) {
	o := r376Execute(t, 11, r376Stream())
	first := r376Attempt(o, 1, NoResult)
	retry := r376Attempt(ArmOutcome{Terminal: "workflow.completed",
		TerminalSource: TerminalStructuredSpecific}, 2, Correct)
	scored, _ := Scored([]Attempt{first, retry})
	if scored.Number != 1 {
		t.Fatalf("a quarantined arm licensed an infrastructure retry: attempt %d scored",
			scored.Number)
	}
}

// ---- Witnesses W1-W11 (W12 is the existing timeout / anti-bias suite) ----

func TestP376W1Exit11MapsToWorkspaceQuarantined(t *testing.T) {
	if got := governedExit(11); got != r376Quarantined {
		t.Fatalf("governedExit(11) = %q, want %q", got, r376Quarantined)
	}
	if o := r376Execute(t, 11, "quiet\n"); o.Terminal != r376Quarantined {
		t.Fatalf("ExecuteGoverned recorded exit 11 as %q", o.Terminal)
	}
}

func TestP376W2Exit11IsStructuredSpecific(t *testing.T) {
	if got := terminalSource(11); got != TerminalStructuredSpecific {
		t.Fatalf("terminalSource(11) = %s, want %s", got, TerminalStructuredSpecific)
	}
	if o := r376Execute(t, 11, "quiet\n"); o.TerminalSource != TerminalStructuredSpecific {
		t.Fatalf("ExecuteGoverned recorded exit 11 as %s", o.TerminalSource)
	}
}

func TestP376W3InfraPhraseCannotReplaceTheQuarantineOutcome(t *testing.T) {
	stream := r376Stream()
	o := r376Execute(t, 11, stream)
	if o.Terminal != r376Quarantined || o.TerminalSource != TerminalStructuredSpecific {
		t.Fatalf("outcome became %q/%s", o.Terminal, o.TerminalSource)
	}
	s := Score(r376Attempt(o, 1, NoResult), stream)
	if s.Terminal == TerminalInfraFailure {
		t.Fatal("the scorer replaced the quarantine with an outage read from the transcript")
	}
	if s.Cause != r376Quarantined {
		t.Fatalf("scoring cause %q lost the terminal identity", s.Cause)
	}
}

func TestP376W4InfraPhraseIsRetainedAsDiagnosticEvidence(t *testing.T) {
	stream := r376Stream()
	o := r376Execute(t, 11, stream)
	if o.InfrastructureHint != r376Phrase {
		t.Fatalf("the overruled phrase was not retained as a hint: %q", o.InfrastructureHint)
	}
	if o.Classifier == nil {
		t.Fatal("no classifier evidence was preserved")
	}
	if o.Classifier.Signal != r376Phrase || o.Classifier.Offset != strings.Index(stream, r376Phrase) {
		t.Fatalf("evidence %q@%d does not locate the phrase", o.Classifier.Signal, o.Classifier.Offset)
	}
	if !strings.Contains(o.Classifier.Context, r376Phrase) {
		t.Fatal("the preserved context does not contain the matched phrase")
	}
}

func TestP376W5EvidenceNamesTheOverrulingStructuredTerminal(t *testing.T) {
	o := r376Execute(t, 11, r376Stream())
	if o.Classifier == nil || o.Classifier.Decided {
		t.Fatal("the evidence must be preserved and marked as not deciding")
	}
	if o.Classifier.OverruledBy != r376Quarantined {
		t.Fatalf("overruled_by = %q, want %q", o.Classifier.OverruledBy, r376Quarantined)
	}
}

func TestP376W6InfrastructureIsNotTheDecidingClassification(t *testing.T) {
	stream := r376Stream()
	o := r376Execute(t, 11, stream)
	if o.Infrastructure != "" {
		t.Fatalf("infrastructure was set: %q", o.Infrastructure)
	}
	a := r376Attempt(o, 1, NoResult)
	for _, ev := range []string{"", stream} {
		if got := a.WorkflowTerminal(ev); got == TerminalInfraFailure {
			t.Fatalf("WorkflowTerminal(evidence len %d) = %s", len(ev), got)
		}
		// The truthful generic state the scorer already gave an unnamed exit:
		// no reading on the delivery axis, terminal identity kept in Cause.
		if got := a.WorkflowTerminal(ev); got != TerminalOtherFailure {
			t.Fatalf("WorkflowTerminal(evidence len %d) = %s, want %s", len(ev), got,
				TerminalOtherFailure)
		}
	}
}

func TestP376W7AQuarantinedArmIsNotInfraRetryEligible(t *testing.T) {
	o := r376Execute(t, 11, r376Stream())
	first := r376Attempt(o, 1, NoResult)
	if strings.TrimSpace(first.Infrastructure) != "" {
		t.Fatal("the recorded attempt carries an infrastructure failure")
	}
	retry := r376Attempt(ArmOutcome{Terminal: "workflow.completed",
		TerminalSource: TerminalStructuredSpecific}, 2, Correct)
	scored, _ := Scored([]Attempt{first, retry})
	if scored.Number != 1 || scored.Terminal != r376Quarantined {
		t.Fatalf("attempt %d (%s) scored; the quarantined attempt must stand",
			scored.Number, scored.Terminal)
	}
}

func TestP376W8AQuarantineIsNotASuccessfulCompletedChange(t *testing.T) {
	stream := r376Stream()
	o := r376Execute(t, 11, stream)
	// Even an oracle verdict of CORRECT cannot make a quarantine a delivery.
	for _, v := range []Verdict{Correct, Incorrect, NoResult} {
		s := Score(r376Attempt(o, 1, v), stream)
		if s.Terminal == TerminalCompleted || s.Delivered || s.Correctness != NotEvaluated {
			t.Fatalf("verdict %s: scored %s/%s delivered=%v", v, s.Terminal, s.Correctness, s.Delivered)
		}
	}
	if r376Attempt(o, 1, Correct).WorkflowTerminal("") == TerminalCompleted {
		t.Fatal("a quarantine read as a completed run")
	}
}

func TestP376W9DocumentedCodesKeepTheirMappings(t *testing.T) {
	for code, want := range map[int]struct {
		terminal string
		source   TerminalSource
	}{
		0: {"workflow.completed", TerminalStructuredSpecific},
		1: {"workflow.failed", TerminalStructuredGeneric},
		3: {"workflow.awaiting_authority", TerminalStructuredSpecific},
		4: {"workflow.stopped", TerminalStructuredSpecific},
		5: {"workflow.timed_out", TerminalStructuredSpecific},
		6: {"workflow.observed", TerminalStructuredSpecific},
	} {
		if got := governedExit(code); got != want.terminal {
			t.Errorf("governedExit(%d) = %q, want %q", code, got, want.terminal)
		}
		if got := terminalSource(code); got != want.source {
			t.Errorf("terminalSource(%d) = %s, want %s", code, got, want.source)
		}
	}
}

func TestP376W10UnknownCodesNeverBecomeSuccess(t *testing.T) {
	for _, code := range []int{2, 7, 8, 9, 10, 12, 42, 99, 255} {
		term := governedExit(code)
		if term == "workflow.completed" || term == r376Quarantined || !strings.Contains(term, itoaR376(code)) {
			t.Errorf("exit %d became %q", code, term)
		}
		a := Attempt{Terminal: term, TerminalSource: string(terminalSource(code)), Verdict: Correct}
		if s := Score(a, ""); s.Terminal == TerminalCompleted || s.Delivered {
			t.Errorf("exit %d scored %s delivered=%v", code, s.Terminal, s.Delivered)
		}
	}
}

func TestP376W11GenericExit1KeepsTextClassification(t *testing.T) {
	stream := r376Stream()
	o := r376Execute(t, 1, stream)
	if o.Terminal != "workflow.failed" || o.TerminalSource != TerminalStructuredGeneric {
		t.Fatalf("exit 1 became %q/%s", o.Terminal, o.TerminalSource)
	}
	if o.Infrastructure != r376Phrase || o.Classifier == nil || !o.Classifier.Decided {
		t.Fatalf("a failure the engine left unexplained lost its infrastructure attribution: %q",
			o.Infrastructure)
	}
	first := r376Attempt(o, 1, NoResult)
	if got := Score(first, stream).Terminal; got != TerminalInfraFailure {
		t.Fatalf("a genuine unexplained outage scored %s", got)
	}
	retry := r376Attempt(ArmOutcome{Terminal: "workflow.completed",
		TerminalSource: TerminalStructuredSpecific}, 2, Correct)
	if scored, _ := Scored([]Attempt{first, retry}); scored.Number != 2 {
		t.Fatal("a legitimate infrastructure retry did not score")
	}
}

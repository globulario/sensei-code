package workflow

// Law 5 across CALLS, which is the half a single audit cannot check for itself.
//
// The run pins a graph identity at certified start -- certifiedStart.GraphDigest(),
// the live store digest that certified it. The audit that later decides admission
// now reports which generation answered it (sensei's graph_generation_sha256). Until
// these two were compared, a graph could be replaced between the certifying
// preflight and the admitting audit, and the run would accept a verdict produced by
// rules it never certified.
//
// Three live stores on this machine answer healthily with different graphs, so this
// is a real sequence, not a constructed one.

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestAnAuditAnsweredByThePinnedGenerationIsAccepted(t *testing.T) {
	if err := verifyPinnedGeneration("c0b660fc42a5", "c0b660fc42a5", "localhost:10120"); err != nil {
		t.Fatalf("the pinned generation answered and was refused: %v", err)
	}
	// Case and surrounding whitespace are transport noise, not identity.
	if err := verifyPinnedGeneration(" C0B660FC42A5 ", "c0b660fc42a5", "localhost:10120"); err != nil {
		t.Errorf("normalisation rejected an equal generation: %v", err)
	}
}

func TestAnAuditAnsweredByAnotherGenerationIsRefused(t *testing.T) {
	err := verifyPinnedGeneration("c0b660fc42a5", "230a74f68fed", "localhost:10120")
	if err == nil {
		t.Fatal("an audit produced by a graph the run never certified was accepted")
	}
	var switched *graphGenerationSwitchedError
	if !errors.As(err, &switched) {
		t.Fatalf("err = %v, want a typed graphGenerationSwitchedError", err)
	}
	// Both values and the endpoint, because "they differ" is not actionable.
	for _, want := range []string{"c0b660fc42a5", "230a74f68fed", "localhost:10120"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal omits %q: %v", want, err)
		}
	}
	// It must not read as an outage: the graph answered, healthily, and that is
	// precisely the problem. Asserted by what the message CLAIMS, not by whether a
	// word appears in it -- the text denies unreachability, so a substring search
	// for "unreachable" indicts the wrong thing.
	if !strings.Contains(err.Error(), "was replaced while this run was executing") {
		t.Errorf("the refusal does not say the graph was replaced: %v", err)
	}
	if strings.Contains(strings.ToLower(err.Error()), "unavailable") {
		t.Errorf("a generation switch is reported as unavailability: %v", err)
	}
}

// A pin the run does not have cannot be compared, and inventing one would compare a
// guess. The gate that certified the start is what refuses an unidentified graph;
// this check adds nothing there and must not pretend to.
func TestAnAbsentPinComparesNothing(t *testing.T) {
	if err := verifyPinnedGeneration("", "230a74f68fed", "localhost:10120"); err != nil {
		t.Errorf("an absent pin produced a refusal it cannot justify: %v", err)
	}
}

// An audit that reports no generation is a DEPLOYMENT fact, not a switch, and it is
// deliberately not refused here -- with one specific reason that must stay true:
// upstream, an available result cannot omit the generation (AuditResult.Validate
// refuses it), so a missing field means the awareness service predates the check
// rather than that a graph went unidentified.
//
// This is the one fail-open branch in the whole front, so it is named, tested, and
// its justification is a property of the other side rather than an assumption about
// it. If that upstream lock is ever relaxed, this test is the place the reasoning
// breaks.
func TestAnAuditReportingNoGenerationIsNotTreatedAsASwitch(t *testing.T) {
	if err := verifyPinnedGeneration("c0b660fc42a5", "", "localhost:10120"); err != nil {
		t.Errorf("an audit from a server that predates the generation field was refused: %v", err)
	}
}

// TestThePinIsCheckedBeforeTheVerdictIsWeighed is a WIRING check over source order,
// and it is labelled that way because this package has no harness that drives a full
// governed run against a fake awareness service -- the behavioural evidence for
// verifyPinnedGeneration itself is above.
//
// What it can falsify is everything the repair depends on that a unit test of the
// predicate cannot reach: that the check exists in the audit path at all, that it is
// asked BEFORE the verdict is weighed (a verdict from a foreign graph must never be
// read as a judgement about the candidate), that it compares the PIN against the
// AUDIT'S generation rather than some other pair of fields, and that there is one
// call site.
func TestThePinIsCheckedBeforeTheVerdictIsWeighed(t *testing.T) {
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)

	decode := strings.Index(src, "verdict, err := sensei.DecodeDiffAudit(audit)")
	check := strings.Index(src, "verifyPinnedGeneration(")
	weigh := strings.Index(src, "if !verdict.ReviewerMayAccept() {")
	if decode < 0 || weigh < 0 {
		t.Fatal("the audit path no longer decodes a verdict and weighs it; this check has lost its anchors")
	}
	if check < 0 {
		t.Fatal("the audit path never verifies that the verdict came from the pinned generation")
	}
	if check < decode {
		t.Errorf("the pin is checked before the verdict is decoded, so it cannot be reading the audit's generation: decode=%d check=%d", decode, check)
	}
	if check > weigh {
		t.Errorf("a verdict from an unpinned graph is weighed before the pin is checked: check=%d weigh=%d", check, weigh)
	}

	// The arguments. GraphBuildCommit is the rule snapshot and CANNOT stand in for
	// the pin: on this installation it belongs to another repository, so two
	// generations share it and the comparison would always agree.
	call := src[check:]
	if end := strings.Index(call, "\n\t\t\t"); end > 0 {
		call = call[:end+40]
	}
	if !strings.Contains(call, "start.GraphDigest()") {
		t.Errorf("the pin is not the generation the start certified: %s", firstSourceLine(call))
	}
	if !strings.Contains(call, "verdict.GraphGeneration") {
		t.Errorf("the observed value is not the audit's own generation: %s", firstSourceLine(call))
	}
	if strings.Contains(call, "GraphBuildCommit") {
		t.Errorf("the rule-snapshot commit is being compared as though it were the generation: %s", firstSourceLine(call))
	}
	if n := strings.Count(src, "verifyPinnedGeneration("); n != 1 {
		t.Errorf("verifyPinnedGeneration has %d call sites in engine.go; one rendezvous, one check", n)
	}

	// The consequence, not only the position. A refusal that merely REPORTS and lets
	// the run continue would leave the foreign verdict governing admission — which is
	// the whole defect wearing a log line. The block must name the candidate
	// unauditable and end the run structurally.
	block := src[check:]
	if end := strings.Index(block, "\n\t\t// Surface why an audit did not pass"); end > 0 {
		block = block[:end]
	} else {
		t.Fatal("could not bound the pin-check block; this check has lost its anchor")
	}
	if !strings.Contains(block, "event.CandidateNotAuditable") {
		t.Errorf("a verdict from an unpinned graph is not recorded as unauditable:\n%s", block)
	}
	if !strings.Contains(block, "structuralFailure(") {
		t.Errorf("a verdict from an unpinned graph does not end the run structurally:\n%s", block)
	}
	if !strings.Contains(block, "return candidateNotConverged") {
		t.Errorf("the run continues after refusing the verdict:\n%s", block)
	}
}

func firstSourceLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return strings.TrimSpace(s)
}

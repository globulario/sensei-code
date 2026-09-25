package workflow

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/validation"
)

func n2bBundle(testOutput string) validation.Bundle {
	return validation.Bundle{DiffDigest: "sha256:cb7c47a4", Checks: []validation.Evidence{
		{Kind: "format", Command: "gofmt", Args: []string{"-l", "cmd", "internal"}, Outcome: validation.Passed, ExitStatus: 0},
		{Kind: "test", Command: "go", Args: []string{"test", "./..."}, Outcome: validation.Passed, ExitStatus: 0, Output: testOutput, OutputDigest: validation.Digest(testOutput)},
	}}
}

var n2bAudit = sensei.DiffAuditDecision{Decision: "pass", Availability: "available", Digest: "cb7c47a4"}

func revise(provider string) roles.ReviewVerdict {
	return roles.ReviewVerdict{
		Provenance: roles.Provenance{Provider: provider},
		Decision:   roles.Revise,
		Summary:    "required Sensei proof is absent",
		Findings:   []roles.Finding{{ID: "f1", Severity: roles.Major, Claim: "the candidate satisfies the required scoped edit check", Reference: "internal/derived/derived.go", Reason: "validation records only gofmt, vet, build, tests", ProofGap: "scoped Sensei edit check"}},
	}
}

func accept(provider string) roles.ReviewVerdict {
	return roles.ReviewVerdict{Provenance: roles.Provenance{Provider: provider}, Decision: roles.Accept, Summary: "does exactly what the plan asks"}
}

// The N2b shape: identical candidate, identical outcomes (timings differ),
// the reviewer replaced by the handoff. The ACCEPT is a contradiction.
func TestAnAcceptOnTheUnchangedCandidateAndEvidenceContradictsTheOpenReview(t *testing.T) {
	first := n2bBundle("ok  \tinternal/derived\t3.205s")
	second := n2bBundle("ok  \tinternal/derived\t1.098s")
	open := openReviewFrom(revise("codex"), 1, first.DiffDigest, evidenceIdentity(first, n2bAudit))
	if !open.contradicts(accept("claude"), second.DiffDigest, evidenceIdentity(second, n2bAudit)) {
		t.Fatal("an ACCEPT on the same digest and the same outcomes must contradict the open review; timings are not evidence")
	}
	if got := open.describe(accept("claude")); got == "" {
		t.Fatal("a contradiction must be describable")
	}
}

func TestAChangedCandidateOrOutcomeAnswersTheOpenReviewMechanically(t *testing.T) {
	first := n2bBundle("ok")
	open := openReviewFrom(revise("codex"), 1, first.DiffDigest, evidenceIdentity(first, n2bAudit))

	edited := n2bBundle("ok")
	edited.DiffDigest = "sha256:other"
	if open.contradicts(accept("claude"), edited.DiffDigest, evidenceIdentity(edited, n2bAudit)) {
		t.Fatal("a different candidate is new facts, not a contradiction")
	}
	failed := n2bBundle("ok")
	failed.Checks[1].Outcome = validation.Failed
	failed.Checks[1].ExitStatus = 1
	if open.contradicts(accept("claude"), failed.DiffDigest, evidenceIdentity(failed, n2bAudit)) {
		t.Fatal("a different outcome is different evidence")
	}
	audited := n2bAudit
	audited.Decision = "block"
	if open.contradicts(accept("claude"), first.DiffDigest, evidenceIdentity(first, audited)) {
		t.Fatal("a different audit decision is different evidence")
	}
	if open.contradicts(revise("claude"), first.DiffDigest, evidenceIdentity(first, n2bAudit)) {
		t.Fatal("only an accepting verdict can contradict a non-accepting one")
	}
}

func TestAnOpenReviewIsNotRecordedWithoutAnIdentityToBindTo(t *testing.T) {
	open := openReviewFrom(revise("codex"), 1, "", "")
	if open.contradicts(accept("claude"), "", "") {
		t.Fatal("an unbound open review must never fire: silence on identity is not sameness")
	}
}

// The engine holds the open review per task, across whatever worker loop is
// running, and counts review attempts in one sequence the handoff does not
// restart.
func TestTheOpenReviewAndTheAttemptCounterSurviveAcrossWorkers(t *testing.T) {
	e := &Engine{}
	if e.nextReviewAttempt("t") != 1 || e.nextReviewAttempt("t") != 2 {
		t.Fatal("review attempts must count 1, 2 across calls")
	}
	if e.reviewAttempt("t") != 2 || e.reviewAttempt("other") != 0 {
		t.Fatal("the counter is per task")
	}
	b := n2bBundle("ok")
	e.setOpenReview("t", openReviewFrom(revise("codex"), 1, b.DiffDigest, evidenceIdentity(b, n2bAudit)))
	got, ok := e.openReview("t")
	if !ok || got.Reviewer != "codex" || len(got.Findings) != 1 {
		t.Fatalf("open review not held: %+v %v", got, ok)
	}
	e.clearOpenReview("t")
	if _, ok := e.openReview("t"); ok {
		t.Fatal("an adjudicated or answered review must not stay open")
	}
}

func TestTheContradictionPromptCarriesBothVerdictsAndTheFindings(t *testing.T) {
	b := n2bBundle("ok")
	open := openReviewFrom(revise("codex"), 1, b.DiffDigest, evidenceIdentity(b, n2bAudit))
	p := contradictionPrompt("task", "plan", "audit", open, accept("claude"))
	for _, want := range []string{"codex", "claude", "required Sensei proof is absent", "scoped Sensei edit check", "does exactly what the plan asks"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
}

func TestAnAdjudicationIsReadByMembership(t *testing.T) {
	for _, c := range []struct {
		in     string
		stands bool
		err    bool
	}{{"", false, false}, {"revise", false, false}, {"accepting_review_stands", true, false}, {" Accepting_Review_Stands ", true, false}, {"stands", false, true}, {"accept", false, true}} {
		got, err := adjudicationStands(architectureDecision{Adjudication: c.in})
		if (err != nil) != c.err || got != c.stands {
			t.Fatalf("%q: got stands=%v err=%v; want stands=%v err=%v", c.in, got, err, c.stands, c.err)
		}
	}
}

func TestTheContradictionPromptAsksForTheAdjudication(t *testing.T) {
	p := contradictionPrompt("task", "plan", "audit", openReview{}, accept("claude"))
	if !strings.Contains(p, `"adjudication"`) || !strings.Contains(p, adjudicationAcceptingStands) {
		t.Fatal("the prompt must ask for the closed adjudication answer")
	}
}

// W1-W6: THE CLASS OF A FINDING BINDS THE CLASS OF ITS RESPONSE.
//
// Measured 2026-09-25 on the DF-19 resume. An independent review returned a
// BLOCKING finding about a code path that reported a CREATE refusal as status
// and continued, and a MAJOR finding about a failing-first history that was
// never retained. The implementer answered the second one and closed the cycle:
// "the review finding was evidence-only, so no code changed". ONE finding,
// singular. The blocking code defect was absorbed into the evidence-only reading
// of its neighbour, and nothing objected -- because nothing tracked a finding's
// class through to its response.
//
// The engine did refuse that cycle, on the identical diff. That is a PROXY: one
// unrelated line would have moved the diff and let it through. W3 is the control
// that proves convergence is no longer decided that way.

// dfNineteenReview is the measured review: one CODE finding, one EVIDENCE
// finding, each carrying its own class.
func dfNineteenReview() roles.ReviewVerdict {
	return roles.ReviewVerdict{
		Provenance: roles.Provenance{Provider: "codex"},
		Decision:   roles.Revise,
		Summary:    "a CREATE refusal is reported without refusing the plan, and the failing-first history is not established",
		Findings: []roles.Finding{
			{ID: "f1", Severity: roles.Blocking, Class: roles.ClassCode,
				Claim:      "a CREATE declaration that cannot be bound refuses the plan",
				Reference:  "internal/workflow/authority.go",
				Reason:     "routePlan emits each createRefusal as an event and continues, and Resume discards the returned reasons",
				Correction: "return a typed routing refusal whenever a supplied CREATE entry fails to bind"},
			{ID: "f2", Severity: roles.Major, Class: roles.ClassEvidence,
				Claim:     "the supplied evidence establishes the required failing-first history",
				Reference: "the run's retained evidence",
				Reason:    "no output from the pre-repair implementation is retained",
				ProofGap:  "the witnesses executed against the preserved pre-repair implementation"},
		},
	}
}

// twoCodeFindingReview is two outstanding findings of the same class, so a
// partial answer cannot be explained away by a class difference.
func twoCodeFindingReview() roles.ReviewVerdict {
	return roles.ReviewVerdict{
		Provenance: roles.Provenance{Provider: "codex"},
		Decision:   roles.Revise,
		Summary:    "two code defects",
		Findings: []roles.Finding{
			{ID: "f3", Severity: roles.Blocking, Class: roles.ClassCode, Claim: "the guard refuses before the durable step", Reference: "internal/workflow/engine.go", Reason: "it warns and continues", Correction: "refuse"},
			{ID: "f4", Severity: roles.Blocking, Class: roles.ClassCode, Claim: "the resolver fails closed", Reference: "internal/workflow/authority.go", Reason: "a nil error falls back to cwd", Correction: "return the error"},
		},
	}
}

func accountFor(t *testing.T, acc findingAccounting, id string) findingAccount {
	t.Helper()
	for _, a := range acc.Accounts {
		if a.Finding.ID == id {
			return a
		}
	}
	t.Fatalf("no account for finding %q; the accounting is per finding id", id)
	return findingAccount{}
}

// W1 THE MEASURED CASE. One CODE finding and one EVIDENCE finding, answered
// with evidence alone, does not converge, and the diagnosis names the
// unaddressed CODE finding BY ID. "The candidate did not change" is a statement
// about bytes and does not satisfy this.
func TestW1AnEvidenceAnswerLeavesTheCodeFindingNamedAndOpen(t *testing.T) {
	open := openReviewFrom(dfNineteenReview(), 1, "sha256:reviewed", "sha256:evidence")
	acc := accountFindings(open, []findingResponse{
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "the witnesses were run against the preserved pre-repair implementation and the failing output is retained"},
	}, nil)

	if acc.converged() {
		t.Fatal("a review carrying a CODE finding and an EVIDENCE finding, answered with evidence alone, must not converge")
	}
	d := acc.diagnose()
	if !strings.Contains(d, "f1") {
		t.Fatalf("the diagnosis must name the unaddressed CODE finding by id; got: %s", d)
	}
	if strings.Contains(d, "f2") {
		t.Fatalf("a finding answered in its own class must not be reported as open; got: %s", d)
	}
	if a := accountFor(t, acc, "f1"); a.Status != findingUnanswered {
		t.Fatalf("the CODE finding nobody named is unanswered, got %q: %s", a.Status, a.Detail)
	}
	if a := accountFor(t, acc, "f2"); !a.discharged() {
		t.Fatalf("the EVIDENCE finding was answered with retained evidence and is discharged, got %q: %s", a.Status, a.Detail)
	}
}

// W2 RECLASSIFICATION IS REFUSED. An implementer response asserting that a CODE
// finding was evidence-only does not discharge it, and the class the accounting
// reads is the one on the finding record.
func TestW2TheClassComesFromTheFindingNotFromThePartyAnsweringIt(t *testing.T) {
	open := openReviewFrom(dfNineteenReview(), 1, "sha256:reviewed", "sha256:evidence")
	acc := accountFindings(open, []findingResponse{
		{ID: "f1", Discharge: dischargeEvidence, ClaimedClass: roles.ClassEvidence,
			Evidence: "the finding is evidence-only; the failing output is retained"},
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "retained beside it"},
	}, []string{"docs/implementation-status.md"})

	if acc.converged() {
		t.Fatal("an implementer that reads a CODE finding as evidence-only has not discharged it")
	}
	a := accountFor(t, acc, "f1")
	if a.Finding.Class != roles.ClassCode {
		t.Fatalf("the class must be taken from the finding record, got %q", a.Finding.Class)
	}
	if a.Status != findingWrongClass {
		t.Fatalf("an evidence answer to a code finding is a class mismatch, got %q: %s", a.Status, a.Detail)
	}
	if a.Claimed != roles.ClassEvidence {
		t.Fatalf("the implementer's own reading is recorded as input, got %q", a.Claimed)
	}
	if !strings.Contains(acc.diagnose(), "f1") {
		t.Fatalf("the diagnosis must name the finding that was reclassified: %s", acc.diagnose())
	}
}

// W3 THE PROXY IS NOT THE CHECK -- CRITICAL CONTROL. A cycle that changes an
// unrelated line, so the diff differs and the identical-diff backstop cannot
// fire, while leaving a CODE finding unaddressed, still does not converge.
func TestW3AMovedDiffDoesNotAnswerAnUnaddressedCodeFinding(t *testing.T) {
	open := openReviewFrom(dfNineteenReview(), 1, "sha256:reviewed", "sha256:evidence")
	// The candidate moved: one unrelated file changed. Nothing names f1.
	moved := []string{"docs/implementation-status.md"}
	acc := accountFindings(open, []findingResponse{
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "the failing output is retained"},
	}, moved)

	if acc.converged() {
		t.Fatal("a cycle that moved the diff elsewhere and left a CODE finding unaddressed must not converge: the identical-diff check is a backstop, not the deciding predicate")
	}
	if a := accountFor(t, acc, "f1"); a.Status != findingUnanswered {
		t.Fatalf("f1 was never named by a response, so it is unanswered, got %q: %s", a.Status, a.Detail)
	}
	if !strings.Contains(acc.diagnose(), "f1") {
		t.Fatalf("the diagnosis must name the unaddressed CODE finding by id; got: %s", acc.diagnose())
	}
	// The same responses against a candidate that did not move at all reach the
	// same verdict. Whether the diff moved is not part of this answer.
	unmoved := accountFindings(open, []findingResponse{
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "the failing output is retained"},
	}, nil)
	if unmoved.converged() != acc.converged() || unmoved.diagnose() != acc.diagnose() {
		t.Fatalf("convergence must not depend on whether the diff moved:\nmoved:   %s\nunmoved: %s", acc.diagnose(), unmoved.diagnose())
	}
	// Nor does CLAIMING a code change discharge one. The paths a response names
	// must be paths the candidate actually changed.
	claimed := accountFindings(open, []findingResponse{
		{ID: "f1", Discharge: dischargeCode, ChangedFiles: []string{"internal/workflow/authority.go"}},
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "the failing output is retained"},
	}, moved)
	if claimed.converged() {
		t.Fatal("a claimed code change naming a path the candidate never changed is not a code change")
	}
	if a := accountFor(t, claimed, "f1"); a.Status != findingUnsubstantiated {
		t.Fatalf("a code answer with nothing behind it is unsubstantiated, got %q: %s", a.Status, a.Detail)
	}
}

// W4 EVIDENCE FINDING DISCHARGED BY EVIDENCE. An EVIDENCE-only finding answered
// with retained execution evidence is discharged, with no code change required.
func TestW4AnEvidenceFindingIsDischargedByRetainedEvidenceAlone(t *testing.T) {
	v := dfNineteenReview()
	v.Findings = v.Findings[1:] // the EVIDENCE finding alone
	open := openReviewFrom(v, 1, "sha256:reviewed", "sha256:evidence")

	// No changed paths at all: the candidate is tree-identical to the reviewed one.
	acc := accountFindings(open, []findingResponse{
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "go test -run TestW3 against the pre-repair implementation; output retained in the cycle record"},
	}, nil)

	if !acc.converged() {
		t.Fatalf("an EVIDENCE finding answered with retained evidence is discharged and requires no code change: %s", acc.diagnose())
	}
	if d := acc.diagnose(); d != "" {
		t.Fatalf("a converged accounting diagnoses nothing, got: %s", d)
	}
	// The control on the other side: a claim of retained evidence that retains
	// none is not evidence, so the finding stays open.
	empty := accountFindings(open, []findingResponse{{ID: "f2", Discharge: dischargeEvidence}}, nil)
	if empty.converged() {
		t.Fatal("a claim of retained evidence that names none does not discharge an evidence finding")
	}
	if a := accountFor(t, empty, "f2"); a.Status != findingUnsubstantiated {
		t.Fatalf("an evidence answer with nothing behind it is unsubstantiated, got %q: %s", a.Status, a.Detail)
	}
}

// W5 PARTIAL ANSWER IS NOT CONVERGENCE -- CONTROL. Two outstanding findings, one
// answered: the cycle does not converge and names the one still open.
func TestW5APartialAnswerIsNotConvergence(t *testing.T) {
	open := openReviewFrom(twoCodeFindingReview(), 1, "sha256:reviewed", "sha256:evidence")
	acc := accountFindings(open, []findingResponse{
		{ID: "f3", Discharge: dischargeCode, ChangedFiles: []string{"internal/workflow/engine.go"}},
	}, []string{"internal/workflow/engine.go"})

	if acc.converged() {
		t.Fatal("a cycle that answers one of two outstanding findings has not converged")
	}
	d := acc.diagnose()
	if !strings.Contains(d, "f4") {
		t.Fatalf("the diagnosis must name the finding still open; got: %s", d)
	}
	if strings.Contains(d, "f3") {
		t.Fatalf("the answered finding must not be reported as open; got: %s", d)
	}
	stillOpen := acc.openFindings()
	if len(stillOpen) != 1 || stillOpen[0].ID != "f4" {
		t.Fatalf("exactly the unanswered finding travels to the next cycle, got %+v", stillOpen)
	}
	// And it survives a later review that says nothing about it.
	carried := carryForwardOpenFindings([]roles.Finding{{ID: "f9", Severity: roles.Major, Class: roles.ClassCode, Claim: "something else"}}, stillOpen)
	if len(carried) != 2 || carried[1].ID != "f4" {
		t.Fatalf("a finding nobody discharged is not retired by the next review's silence, got %+v", carried)
	}
}

// W6 DISAGREEMENT IS A ROUTE, NOT A LICENCE -- CONTROL. An implementer that
// believes a finding is misclassified can say so, and saying so does not
// discharge it.
func TestW6ADisagreementRoutesTheFindingAndLeavesItOpen(t *testing.T) {
	open := openReviewFrom(dfNineteenReview(), 1, "sha256:reviewed", "sha256:evidence")
	acc := accountFindings(open, []findingResponse{
		{ID: "f1", Discharge: dischargeDisagreement, ClaimedClass: roles.ClassEvidence,
			Disagreement: "the refusal is already reported as an event, so what is missing is the proof, not the code"},
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "the failing output is retained"},
	}, []string{"internal/workflow/authority.go"})

	if acc.converged() {
		t.Fatal("a disagreement is an escalation, not a discharge")
	}
	if !acc.routed() {
		t.Fatal("a disputed finding is routed to the architect rather than dropped")
	}
	a := accountFor(t, acc, "f1")
	if a.Status != findingDisputed {
		t.Fatalf("the disagreement must be represented as a disputed finding, got %q: %s", a.Status, a.Detail)
	}
	if !strings.Contains(acc.diagnose(), "f1") {
		t.Fatalf("a disputed finding stays named and open; got: %s", acc.diagnose())
	}
	if stillOpen := acc.openFindings(); len(stillOpen) != 1 || stillOpen[0].ID != "f1" {
		t.Fatalf("the disputed finding travels on rather than being dropped, got %+v", stillOpen)
	}
}

// A finding that names no class cannot be discharged by guessing at one. It is
// refused and routed to the party that can re-state it -- never inferred from
// severity, wording, or position in the list.
func TestAFindingWithNoClassIsRefusedRatherThanGuessed(t *testing.T) {
	v := dfNineteenReview()
	v.Findings[0].Class = ""
	open := openReviewFrom(v, 1, "sha256:reviewed", "sha256:evidence")
	acc := accountFindings(open, []findingResponse{
		{ID: "f1", Discharge: dischargeCode, ChangedFiles: []string{"internal/workflow/authority.go"}},
		{ID: "f2", Discharge: dischargeEvidence, Evidence: "retained"},
	}, []string{"internal/workflow/authority.go"})

	if acc.converged() {
		t.Fatal("a finding with no class states no kind of answer, so no answer discharges it")
	}
	if a := accountFor(t, acc, "f1"); a.Status != findingUnusable {
		t.Fatalf("an unclassified finding is refused, got %q: %s", a.Status, a.Detail)
	}
	if !acc.routed() {
		t.Fatal("an unclassified finding is the reviewer's to re-state, so it routes rather than looping the implementer")
	}
}

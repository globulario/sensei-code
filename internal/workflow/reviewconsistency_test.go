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

// The DF-19 resume review: one blocking CODE finding and one major EVIDENCE
// finding, recorded with their classes on the finding itself.
func df19Review() openReview {
	b := n2bBundle("ok")
	v := roles.ReviewVerdict{
		Provenance: roles.Provenance{Provider: "chatgpt"},
		Decision:   roles.Revise,
		Summary:    "a CREATE refusal is status, not a refusal; the failing-first history is absent",
		Findings: []roles.Finding{
			{ID: "f1", Severity: roles.Blocking, Class: roles.ClassCode, Claim: "an unbindable CREATE refuses the plan", Reference: "internal/workflow/engine.go", Reason: "routePlan emits createRefusal and continues", Correction: "return a typed routing refusal"},
			{ID: "f2", Severity: roles.Major, Class: roles.ClassEvidence, Claim: "W4-W6 were shown to fail first", Reference: "validation evidence", Reason: "no failing output", ProofGap: "failing-first history"},
		},
	}
	return openReviewFrom(v, 1, b.DiffDigest, evidenceIdentity(b, n2bAudit))
}

// witnessCheck is the executed check the evidence answers below rest on.
const witnessCheck = "go test ./internal/workflow -run TestW4"

func evidenceAnswer(id string) roles.FindingResponse {
	return roles.FindingResponse{FindingID: id, Response: roles.ResponseDischarge, Class: roles.ClassEvidence, Account: "ran the W4 witness: PASS", Check: witnessCheck}
}

// boundEvidence is the broker's bundle for the candidate at digest: the
// ordinary checks plus the witness run, every one bound to those bytes.
func boundEvidence(digest string) validation.Bundle {
	b := n2bBundle("ok")
	b.Checks = append(b.Checks, validation.Evidence{Kind: "test", Command: "go", Args: []string{"test", "./internal/workflow", "-run", "TestW4"}, Outcome: validation.Passed})
	b.DiffDigest = digest
	for i := range b.Checks {
		b.Checks[i].DiffDigest = digest
	}
	return b
}

func codeAnswer(id string) roles.FindingResponse {
	return roles.FindingResponse{FindingID: id, Response: roles.ResponseDischarge, Class: roles.ClassCode, Account: "routePlan returns a typed refusal"}
}

// assertOpen fails unless exactly the named ids are open, each named by id in
// the diagnosis.
func assertOpen(t *testing.T, a findingAccounting, want ...string) {
	t.Helper()
	if a.Converged() {
		t.Fatalf("converged with %v outstanding; discharged %v", want, a.Discharged)
	}
	if got := strings.Join(a.OpenIDs(), ","); got != strings.Join(want, ",") {
		t.Fatalf("open findings %q, want %q", got, strings.Join(want, ","))
	}
	for _, id := range want {
		if !strings.Contains(a.describe(), "["+id+"]") {
			t.Fatalf("the diagnosis does not name %s: %s", id, a.describe())
		}
	}
}

// W1, THE MEASURED CASE. Evidence answered, code silent, candidate unchanged.
// Breaks if per-id accounting is missing: a single evidence answer would then
// read as the whole review answered, and nothing would name f1.
func TestW1AnEvidenceOnlyAnswerToACodeAndEvidenceReviewDoesNotConverge(t *testing.T) {
	open := df19Review()
	unchanged := boundEvidence(open.CandidateDigest)
	a := open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, unchanged.DiffDigest, unchanged)
	assertOpen(t, a, "f1")
	if len(a.Discharged) != 1 || a.Discharged[0] != "f2" {
		t.Fatalf("the evidence finding answered by evidence was not discharged: %v", a.Discharged)
	}
}

// W2, RECLASSIFICATION IS REFUSED. The response calls f1 evidence-only and the
// candidate did move, so only the class comparison stands between this answer
// and a discharge. Breaks if the responder's class can override the record.
func TestW2AResponseCannotReclassifyACodeFinding(t *testing.T) {
	open := df19Review()
	moved := boundEvidence("sha256:moved")
	a := open.reconcile([]roles.FindingResponse{evidenceAnswer("f1"), evidenceAnswer("f2")}, moved.DiffDigest, moved)
	assertOpen(t, a, "f1")
	if a.Open[0].Class != roles.ClassCode || !strings.Contains(a.Open[0].Reason, "CODE") {
		t.Fatalf("the class was not taken from the finding record: %+v", a.Open[0])
	}
	if err := evidenceAnswer("f1").Discharges(open.Findings[0]); err == nil {
		t.Fatal("an EVIDENCE answer discharged a CODE finding")
	}
}

// W3, THE PROXY IS NOT THE CHECK (control). An unrelated edit changes the
// candidate identity and f1 is never answered. Breaks if diff movement can
// still decide convergence: before this repair, a changed digest released the
// open review mechanically, and this exact shape was demonstrated to escape.
func TestW3AnUnrelatedEditDoesNotAnswerACodeFinding(t *testing.T) {
	open := df19Review()
	edited := boundEvidence("sha256:unrelated-edit")
	if edited.DiffDigest == open.CandidateDigest {
		t.Fatal("the control did not change the candidate identity")
	}
	a := open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, edited.DiffDigest, edited)
	assertOpen(t, a, "f1")
}

// W4, EVIDENCE DISCHARGED BY EVIDENCE. The candidate did not change and must
// not need to. Breaks if evidence-class discharge still depends on a changed
// diff, or if a CODE-style change requirement leaks into the EVIDENCE class.
func TestW4AnEvidenceFindingIsDischargedByEvidenceWithNoCodeChange(t *testing.T) {
	b := boundEvidence("sha256:cb7c47a4")
	open := openReviewFrom(roles.ReviewVerdict{Provenance: roles.Provenance{Provider: "chatgpt"}, Decision: roles.Revise, Summary: "s",
		Findings: []roles.Finding{{ID: "f2", Severity: roles.Major, Class: roles.ClassEvidence, Claim: "c", Reason: "r", ProofGap: "g"}}},
		1, b.DiffDigest, evidenceIdentity(b, n2bAudit))
	a := open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, b.DiffDigest, b)
	if !a.Converged() || len(a.Discharged) != 1 || a.Discharged[0] != "f2" {
		t.Fatalf("an EVIDENCE finding answered by evidence on an unchanged candidate was not discharged: open %s", a.describe())
	}
	// And it is the execution evidence that discharges it, not the words.
	stale := boundEvidence("sha256:another-candidate")
	if open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, b.DiffDigest, stale).Converged() {
		t.Fatal("evidence bound to another candidate discharged the finding")
	}
}

// W4 CONTROL, UNRELATED EVIDENCE IS NOT THE EVIDENCE. The candidate's bundle
// is bound to it and every check in it passed, but the check the answer names
// never ran. Breaks if evidence-class discharge reads "some check passed on
// this candidate" instead of the check the answer rests on.
func TestW4AnUnrelatedPassingCheckDoesNotDischargeAnEvidenceFinding(t *testing.T) {
	b := boundEvidence("sha256:cb7c47a4")
	open := openReviewFrom(roles.ReviewVerdict{Provenance: roles.Provenance{Provider: "chatgpt"}, Decision: roles.Revise, Summary: "s",
		Findings: []roles.Finding{{ID: "f2", Severity: roles.Major, Class: roles.ClassEvidence, Claim: "c", Reason: "r", ProofGap: "g"}}},
		1, b.DiffDigest, evidenceIdentity(b, n2bAudit))
	// The control: the named check is present, so the refusals below can only
	// be about the binding and not some earlier guard.
	if a := open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, b.DiffDigest, b); !a.Converged() {
		t.Fatalf("the control did not discharge: %s", a.describe())
	}
	unrelated := n2bBundle("ok")
	for i := range unrelated.Checks {
		unrelated.Checks[i].DiffDigest = b.DiffDigest
	}
	if !unrelated.Passed() {
		t.Fatal("the unrelated bundle must pass, or this case proves nothing about relatedness")
	}
	assertOpen(t, open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, b.DiffDigest, unrelated), "f2")

	unnamed := evidenceAnswer("f2")
	unnamed.Check = ""
	assertOpen(t, open.reconcile([]roles.FindingResponse{unnamed}, b.DiffDigest, b), "f2")

	failed := boundEvidence(b.DiffDigest)
	failed.Checks[len(failed.Checks)-1].Outcome = validation.Failed
	assertOpen(t, open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, b.DiffDigest, failed), "f2")

	// The bundle names this candidate but the named check ran on other bytes.
	foreign := boundEvidence(b.DiffDigest)
	foreign.Checks[len(foreign.Checks)-1].DiffDigest = "sha256:another-candidate"
	assertOpen(t, open.reconcile([]roles.FindingResponse{evidenceAnswer("f2")}, b.DiffDigest, foreign), "f2")
}

// W5, PARTIAL ANSWER IS NOT CONVERGENCE (control). Two outstanding, one
// answered with work of its own class. Breaks if partial accounting is
// treated as convergence.
func TestW5AnsweringOneOfTwoFindingsLeavesTheOtherOpen(t *testing.T) {
	open := df19Review()
	moved := boundEvidence("sha256:moved")
	a := open.reconcile([]roles.FindingResponse{codeAnswer("f1")}, moved.DiffDigest, moved)
	assertOpen(t, a, "f2")
	if len(a.Discharged) != 1 || a.Discharged[0] != "f1" {
		t.Fatalf("the answered CODE finding was not discharged by a real change: %v", a.Discharged)
	}
}

// W6, DISAGREEMENT IS A ROUTE, NOT A LICENCE (control). f1 is disputed as
// misclassified and f2 is answered, with the candidate moved so nothing else
// holds f1 open. Breaks if a disagreement can silently close the finding, or
// if it is dropped instead of routed.
func TestW6ADisputedFindingStaysOpenAndIsRoutedAsADisagreement(t *testing.T) {
	open := df19Review()
	moved := boundEvidence("sha256:moved")
	dispute := roles.FindingResponse{FindingID: "f1", Response: roles.ResponseDisagree, Class: roles.ClassEvidence, Disagreement: "the routing refusal is evidence of a status event, not a code defect"}
	a := open.reconcile([]roles.FindingResponse{dispute, evidenceAnswer("f2")}, moved.DiffDigest, moved)
	assertOpen(t, a, "f1")
	if len(a.Disputed) != 1 || a.Disputed[0].FindingID != "f1" {
		t.Fatalf("the disagreement was not routed as a disagreement: %+v", a.Disputed)
	}
	for _, id := range a.Discharged {
		if id == "f1" {
			t.Fatal("a disputed finding was discharged")
		}
	}
	// The typed response refuses too, on its own: a disagreement that claims
	// the finding's own class is still not a discharge.
	agreeingClass := dispute
	agreeingClass.Class, agreeingClass.Account = roles.ClassCode, "disputed"
	if err := agreeingClass.Discharges(open.Findings[0]); err == nil {
		t.Fatal("a disagreement discharged the finding it disputes")
	}
	if p := disagreementPrompt("task", "plan", "audit", open, a.Disputed); !strings.Contains(p, "[f1]") || !strings.Contains(p, dispute.Disagreement) {
		t.Fatalf("the architect is not told which finding is disputed and why: %s", p)
	}
}

// The class is authoritative at the record: a finding with none is refused by
// the verdict, never defaulted from severity or position.
func TestAFindingWithoutAClassIsRefused(t *testing.T) {
	v := revise("codex")
	v.Provenance.Role, v.Provenance.SessionMode = roles.Reviewer, roles.Fresh
	v.Findings[0].Class = roles.ClassEvidence
	// The control: with its class the verdict is valid, so the refusal below
	// can only be about the class and not some earlier guard.
	if err := v.Validate(roles.Binding{}, ""); err != nil {
		t.Fatalf("the control verdict is not valid with its class: %v", err)
	}
	if err := roles.NewAdvisory(roles.ReviewVerdict{Provenance: roles.Provenance{Provider: "codex", Role: roles.Reviewer}, Decision: v.Decision, Summary: v.Summary, Findings: v.Findings}).Validate(roles.Binding{}, ""); err != nil {
		t.Fatalf("the advisory control is not valid with its class: %v", err)
	}
	v.Findings = []roles.Finding{v.Findings[0]}
	v.Findings[0].Class = ""
	if err := v.Validate(roles.Binding{}, ""); err == nil || !strings.Contains(err.Error(), "class") {
		t.Fatalf("a classless finding was not refused for its class: %v", err)
	}
	if err := roles.NewAdvisory(roles.ReviewVerdict{Provenance: roles.Provenance{Provider: "codex", Role: roles.Reviewer}, Decision: v.Decision, Summary: v.Summary, Findings: v.Findings}).Validate(roles.Binding{}, ""); err == nil || !strings.Contains(err.Error(), "class") {
		t.Fatalf("an advisory classless finding was not refused for its class: %v", err)
	}
	if got := numberFindings([]roles.Finding{{Severity: roles.Blocking}}); got[0].Class != "" {
		t.Fatalf("a class was inferred for a finding that stated none: %q", got[0].Class)
	}
}

// The implementer's account arrives in its final message after prose that may
// quote JSON of its own; the LAST finding_responses object is the account.
func TestTheImplementersAccountIsDecodedFromItsFinalMessage(t *testing.T) {
	text := "I changed `x := map[string]int{}`.\n{\"finding_responses\":[{\"finding_id\":\"f1\",\"response\":\" Discharge \",\"class\":\"CODE\",\"account\":\"fixed\"}]}"
	got, err := decodeFindingResponses(text)
	if err != nil || len(got) != 1 || got[0].FindingID != "f1" || got[0].Response != roles.ResponseDischarge || got[0].Class != roles.ClassCode {
		t.Fatalf("decoded %+v, %v", got, err)
	}
	if got, err := decodeFindingResponses("no account at all"); err != nil || got != nil {
		t.Fatalf("an absent account is no responses, not an error: %+v %v", got, err)
	}
	if !strings.Contains(df19Review().responsesPrompt(), "f1 (CODE)") {
		t.Fatal("the worker is not told each finding's class")
	}
}

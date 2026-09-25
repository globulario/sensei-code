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

// Per-finding accounting witnesses (W1-W6). The measured case is the DF-19
// resume: one blocking CODE finding and one major EVIDENCE finding, answered
// with evidence alone and declared "evidence-only".

const (
	raisedOnTree = "tree-when-the-review-was-raised"
	changedTree  = "tree-after-the-response"
)

func df19Review() openReview {
	v := roles.ReviewVerdict{
		Provenance: roles.Provenance{Provider: "codex", CandidateTree: raisedOnTree},
		Decision:   roles.Revise,
		Summary:    "a CREATE that cannot bind does not refuse the plan, and W4-W6 have no failing-first history",
		Findings: []roles.Finding{
			{ID: "f1", Severity: roles.Blocking, Class: roles.ClassCode,
				Claim: "an unbindable CREATE declaration is reported as status but does not refuse the plan", Reference: "internal/workflow/engine.go",
				Reason: "routePlan emits each createRefusal and continues; Resume discards the reasons", Correction: "return a typed routing refusal"},
			{ID: "f2", Severity: roles.Major, Class: roles.ClassEvidence,
				Claim: "W4, W5 and W6 failed before the repair", Reference: "validation evidence",
				Reason: "no failing-first output", ProofGap: "run the witnesses against the pre-repair implementation"},
		},
	}
	b := n2bBundle("ok")
	return openReviewFrom(v, 1, b.DiffDigest, evidenceIdentity(b, n2bAudit))
}

// W1 THE MEASURED CASE. Breaks if the accounting lets an evidence answer stand
// for the whole review, or if the diagnosis stops naming the CODE finding.
func TestW1ACodeAndAnEvidenceFindingAnsweredWithEvidenceAloneDoNotConverge(t *testing.T) {
	open := df19Review()
	report := "Cycle 3 is closed. The review finding was evidence-only, so no code changed.\n" +
		"RESPONSE f2 evidence: ran W4-W6 against the pre-repair tree; all three failed as expected\n"
	acct := open.account(parseFindingResponses(report), raisedOnTree)
	if acct.converged() {
		t.Fatal("an evidence answer to f2 converged a review that also holds the CODE finding f1")
	}
	if _, ok := acct.Discharged["f2"]; !ok {
		t.Fatalf("the evidence finding f2 was answered with evidence and not discharged: %+v", acct)
	}
	if len(acct.Open) != 1 || acct.Open[0].Finding.ID != "f1" || acct.Open[0].Finding.Class != roles.ClassCode {
		t.Fatalf("the unaddressed CODE finding is not the one held open: %+v", acct.Open)
	}
	if d := acct.describe(); !strings.Contains(d, "[f1]") || !strings.Contains(d, "code") || strings.Contains(d, "[f2]") {
		t.Fatalf("the diagnosis must name the unaddressed CODE finding f1 by id, and only it: %s", d)
	} // Claiming a code answer does not make one: the candidate that stayed
	// tree-identical to the one the finding was raised on changed no code.
	claimed := open.account(parseFindingResponses(report+"RESPONSE f1 code: fixed\n"), raisedOnTree)
	if claimed.converged() || !strings.Contains(claimed.describe(), "content did not change") {
		t.Fatalf("a code answer on an unchanged candidate discharged the CODE finding f1: %+v", claimed)
	}
}

// W2 RECLASSIFICATION IS REFUSED. Breaks if the respondent's kind is compared
// with anything but the finding record's class, or substituted for it.
func TestW2AnImplementerCannotAnswerACodeFindingAsEvidence(t *testing.T) {
	open := df19Review()
	report := "RESPONSE f1 evidence: this was evidence-only; the routing is already visible in the status events\n" +
		"RESPONSE f2 evidence: failing-first output retained\n"
	// The candidate changed, so only the class can refuse the discharge.
	acct := open.account(parseFindingResponses(report), changedTree)
	if acct.converged() {
		t.Fatal("an implementer reclassified the CODE finding f1 as evidence and the review converged")
	}
	if _, ok := acct.Discharged["f1"]; ok {
		t.Fatal("f1 was discharged by an evidence response")
	}
	if len(acct.Open) != 1 || acct.Open[0].Finding.Class != roles.ClassCode || !strings.Contains(acct.Open[0].Why, "evidence response does not discharge a code finding") {
		t.Fatalf("the refusal must read the class from the finding record: %+v", acct.Open)
	}
	if open.Findings[0].Class != roles.ClassCode {
		t.Fatal("the accounting changed the finding record's class")
	}
}

// W3 THE PROXY IS NOT THE CHECK -- CRITICAL CONTROL. The diff moved (an
// unrelated line), the CODE finding was never answered. The pinned predicate,
// contradicts(), reads the moved diff as the review answered; the accounting
// must not. Demonstrated FAILING against the pinned behaviour before the
// repair. Breaks if a changed candidate is taken to answer a finding.
func TestW3AnUnrelatedChangeDoesNotAnswerACodeFinding(t *testing.T) {
	open := df19Review()
	unrelated := n2bBundle("ok")
	unrelated.DiffDigest = "sha256:one-unrelated-line"
	if open.contradicts(accept("claude"), unrelated.DiffDigest, evidenceIdentity(unrelated, n2bAudit)) {
		t.Fatal("precondition: the diff-based predicate is expected to let a moved diff through; if it does not, this control proves nothing")
	}
	acct := open.account(parseFindingResponses("RESPONSE f2 evidence: failing-first output retained\n"), changedTree)
	if acct.converged() {
		t.Fatal("a moved diff with the CODE finding f1 unaddressed converged: convergence is still decided by the proxy")
	}
	if len(acct.Open) != 1 || acct.Open[0].Finding.ID != "f1" {
		t.Fatalf("f1 must stay open whatever the diff did: %+v", acct.Open)
	}
}

// W3 through the REAL candidate loop. An open review holds the CODE finding
// f1; the implementer changes main.go (unrelated to f1) and names no finding;
// the reviewer double would ACCEPT. The loop must refuse without reaching it:
// the review attempt counter stays at zero, so the refusal is the accounting's
// and not some later guard's. Breaks if the engine stops feeding the response
// into the accounting before convergence, or if a moved diff clears it.
func TestW3TheCandidateLoopDoesNotConvergeOnAMovedDiffWithACodeFindingOpen(t *testing.T) {
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Fresh, "accept")
	h.engine.setOpenReview("task-1", df19Review())

	outcome, _, _, _, err := h.engine.runCandidate(t.Context(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	if outcome.Accepted() {
		t.Fatal("the loop accepted a candidate whose CODE finding f1 was never answered, because the diff moved")
	}
	if err == nil || !strings.Contains(err.Error(), "[f1]") || !strings.Contains(err.Error(), "no response named it") {
		t.Fatalf("the non-convergence must name the unanswered CODE finding by id: %v", err)
	}
	if n := h.engine.reviewAttempt("task-1"); n != 0 {
		t.Fatalf("the reviewer was asked %d time(s); the accounting must refuse before any review", n)
	}
	if _, ok := h.engine.openReview("task-1"); !ok {
		t.Fatal("the unanswered finding did not stay open for the next worker")
	}
	saw := readRecord(t, h.workerSaw)
	if !strings.Contains(saw, "[f1] class code") || !strings.Contains(saw, "RESPONSE <id>") {
		t.Fatalf("the implementer was not told the finding it owed and its recorded class:\n%s", saw)
	}
}

// W4 EVIDENCE FINDING DISCHARGED BY EVIDENCE. Breaks if an evidence answer
// needs a code change, or if the unchanged candidate is refused anyway.
func TestW4AnEvidenceFindingIsDischargedByEvidenceWithNoCodeChange(t *testing.T) {
	open := df19Review()
	open.Findings = open.Findings[1:]
	acct := open.account(parseFindingResponses("RESPONSE f2 evidence: go test -run 'TestW4|TestW5|TestW6' at the pre-repair tree: FAIL x3\n"), raisedOnTree)
	if !acct.converged() || acct.Answered != 1 {
		t.Fatalf("an EVIDENCE finding answered with evidence, on an unchanged candidate, was not discharged: %+v", acct)
	}
	if !strings.HasPrefix(acct.Discharged["f2"], "evidence: ") {
		t.Fatalf("the discharge does not record what answered it: %q", acct.Discharged["f2"])
	}
	// The class binds in the other direction too: an unrelated code change
	// does not discharge an evidence finding.
	// An evidence answer that states no evidence answers nothing.
	if empty := open.account(parseFindingResponses("RESPONSE f2 evidence:\n"), raisedOnTree); empty.converged() {
		t.Fatal("an evidence response carrying no evidence discharged the EVIDENCE finding")
	}
	code := open.account(parseFindingResponses("RESPONSE f2 code: tidied a comment\n"), changedTree)
	if code.converged() {
		t.Fatal("an EVIDENCE finding was discharged by a code change")
	}
}

// W5 PARTIAL ANSWER IS NOT CONVERGENCE. Breaks if answering one finding is
// read as answering the review, or if the accounting forgets across cycles.
func TestW5APartialAnswerDoesNotConvergeAndNamesTheOneStillOpen(t *testing.T) {
	open := df19Review()
	acct := open.account(parseFindingResponses("RESPONSE f1 code: routePlan now returns a typed refusal for an unbound CREATE\n"), changedTree)
	if acct.converged() {
		t.Fatal("one of two findings answered converged the cycle")
	}
	if _, ok := acct.Discharged["f1"]; !ok {
		t.Fatalf("a code answer on changed content did not discharge the CODE finding: %+v", acct)
	}
	if d := acct.describe(); !strings.Contains(d, "[f2]") || strings.Contains(d, "[f1]") {
		t.Fatalf("the diagnosis must name f2, the one still open, and only it: %s", d)
	}
	// Carried across cycles: the next response owes only f2, and a response
	// that is silent on f1 does not reopen it.
	open.Discharged = acct.Discharged
	if c := open.responseContract(); strings.Contains(c, "[f1]") || !strings.Contains(c, "[f2] class evidence") {
		t.Fatalf("the next respondent must be told it owes f2 and not f1:\n%s", c)
	}
	next := open.account(parseFindingResponses("RESPONSE f2 evidence: failing-first output retained\n"), changedTree)
	if !next.converged() {
		t.Fatalf("both findings are accounted for across two cycles and the review did not converge: %s", next.describe())
	}
}

// Severity is no exemption. A MINOR finding is outstanding by id exactly like
// a blocking one: it is owed in the response contract, silence leaves it open,
// a disagreement does not discharge it, and only a response of its recorded
// class does. Breaks if the accounting or the contract skips a finding by its
// severity.
func TestAMinorFindingStaysOpenUntilItsIDIsAccountedFor(t *testing.T) {
	open := df19Review()
	open.Findings = append(open.Findings, roles.Finding{ID: "f3", Severity: roles.Minor, Class: roles.ClassCode,
		Claim: "the refusal message omits the CREATE path", Reference: "internal/workflow/engine.go", Reason: "r"})
	if c := open.responseContract(); !strings.Contains(c, "[f3] class code") {
		t.Fatalf("the respondent was not told it owes the minor finding f3:\n%s", c)
	}
	both := "RESPONSE f1 code: routePlan returns a typed refusal\nRESPONSE f2 evidence: failing-first output retained\n"
	silent := open.account(parseFindingResponses(both), changedTree)
	if silent.converged() || len(silent.Open) != 1 || silent.Open[0].Finding.ID != "f3" || !strings.Contains(silent.describe(), "[f3]") {
		t.Fatalf("a response silent on the minor finding f3 converged or did not name it: %+v", silent)
	}
	disputed := open.account(parseFindingResponses(both+"RESPONSE f3 disagree: cosmetic\n"), changedTree)
	if disputed.converged() || len(disputed.Disputed) != 1 || disputed.Disputed[0].Finding.ID != "f3" {
		t.Fatalf("a disagreement discharged the minor finding f3: %+v", disputed)
	}
	wrong := open.account(parseFindingResponses(both+"RESPONSE f3 evidence: the message is fine\n"), changedTree)
	if wrong.converged() {
		t.Fatal("an evidence response discharged the minor CODE finding f3")
	}
	answered := open.account(parseFindingResponses(both+"RESPONSE f3 code: the message names the CREATE path\n"), changedTree)
	if !answered.converged() {
		t.Fatalf("every finding, the minor one included, was answered by its class and the cycle did not converge: %s", answered.describe())
	}
}

// W6 DISAGREEMENT IS A ROUTE, NOT A LICENCE -- CONTROL. Breaks if a
// disagreement discharges the finding, is dropped silently, or if a
// respondent can pair it with a discharge of its own choosing.
func TestW6ADisagreementIsEscalatedAndDischargesNothing(t *testing.T) {
	open := df19Review()
	report := "RESPONSE f1 disagree: this is an evidence finding, the refusal is already visible as status\n" +
		"RESPONSE f1 evidence: status events show the refusal\n" +
		"RESPONSE f2 evidence: failing-first output retained\n"
	acct := open.account(parseFindingResponses(report), changedTree)
	if acct.converged() {
		t.Fatal("a disagreement converged the cycle")
	}
	if _, ok := acct.Discharged["f1"]; ok {
		t.Fatal("the disputed finding f1 was discharged")
	}
	if len(acct.Disputed) != 1 || acct.Disputed[0].Finding.ID != "f1" || acct.Disputed[0].Finding.Class != roles.ClassCode {
		t.Fatalf("the disagreement was not recorded as an escalation of f1 with its recorded class: %+v", acct.Disputed)
	}
	p := disputePrompt("task", "plan", "audit", open, acct.Disputed)
	for _, want := range []string{"[f1]", "code", "this is an evidence finding", "not a discharge"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the escalation does not carry %q:\n%s", want, p)
		}
	}
}

// The class is the reviewer's to set, and it is never guessed: a verdict with
// a finding that carries none, or an unknown one, is refused whole.
func TestAFindingWithoutAValidClassIsRefusedNotInferred(t *testing.T) {
	b := roles.Binding{TaskID: "t", BaseSHA: "base", CandidateDigest: "cand"}
	for _, class := range []roles.FindingClass{"", "Code", "proof"} {
		v := roles.ReviewVerdict{
			Provenance: roles.Provenance{TaskID: "t", Role: roles.Reviewer, Provider: "codex", SessionMode: roles.Fresh, BaseSHA: "base", CandidateDigest: "cand"},
			Decision:   roles.Revise, Summary: "s",
			Findings: []roles.Finding{{ID: "f1", Severity: roles.Blocking, Class: class, Claim: "c", Reference: "engine.go", Reason: "r", Correction: "x"}},
		}
		err := v.Validate(b, "claude")
		if err == nil || !strings.Contains(err.Error(), "class") {
			t.Fatalf("class %q: a verdict was accepted without a valid class: %v", class, err)
		}
		v.Provenance.SessionMode = roles.Unverified
		if err := roles.NewAdvisory(v).Validate(b, "claude"); err == nil || !strings.Contains(err.Error(), "class") {
			t.Fatalf("class %q: an advisory verdict was accepted without a valid class: %v", class, err)
		}
	}
}

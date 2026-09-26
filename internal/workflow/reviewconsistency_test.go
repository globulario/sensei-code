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

// FINDING CLASS BINDS ITS RESPONSE: the accounting predicate the candidate loop
// decides convergence by. The loop-level witnesses are in
// nonconvergence_test.go; these pin each rule of the predicate itself.

var (
	classedCode     = roles.Finding{ID: "f1", Severity: roles.Blocking, Class: roles.CodeFinding, Claim: "the refusal does not refuse", Reference: "internal/workflow/engine.go", Reason: "it continues"}
	classedEvidence = roles.Finding{ID: "f2", Severity: roles.Major, Class: roles.EvidenceFinding, Claim: "the failing-first history is established", Reference: "internal/workflow/engine.go", Reason: "no failing run", ProofGap: "the failing run"}
)

func openIDs(a findingAccount) []string {
	ids := make([]string, 0, len(a.Open))
	for _, o := range a.Open {
		ids = append(ids, o.ID)
	}
	return ids
}

// W2 RECLASSIFICATION IS REFUSED. The worker says the CODE finding was
// evidence-only and cites a check that passed; the finding stays open, and the
// class the account carries is the one on the reviewer's finding. A response
// that tries to carry a class of its own has nowhere to put it.
//
// Fails if the answered class is compared with anything but the finding's.
func TestW2AnImplementerCannotReclassifyACodeFindingAsEvidence(t *testing.T) {
	b := n2bBundle("ok")
	responses, err := parseFindingResponses(`done. {"finding_responses":[{"id":"f1","class":"evidence","answered_by":"evidence","evidence":"gofmt"}]}`)
	if err != nil || len(responses) != 1 {
		t.Fatalf("the accounting did not parse: %v %+v", err, responses)
	}
	for _, candidate := range []string{b.DiffDigest, "sha256:moved"} {
		a := accountForFindings([]roles.Finding{classedCode}, b.DiffDigest, responses, candidate, b)
		if a.Settled() || len(a.Open) != 1 || a.Open[0].ID != "f1" {
			t.Fatalf("candidate %s: a CODE finding answered as evidence was discharged: %+v", candidate, a)
		}
		if a.Open[0].Class != roles.CodeFinding || !strings.Contains(a.Diagnosis(), `recorded it as "code"`) {
			t.Fatalf("the class was not taken from the finding record: %+v %s", a.Open[0], a.Diagnosis())
		}
	}
}

// W4 EVIDENCE FINDING DISCHARGED BY EVIDENCE, at the predicate: a cited check
// that ran and passed discharges it on an unchanged candidate. Its controls: a
// cited check that did not run, and an unrelated code change, do not.
func TestW4AnEvidenceFindingIsDischargedOnlyByExecutedEvidence(t *testing.T) {
	b := n2bBundle("ok")
	for _, cite := range []string{"go test ./...", "test", "gofmt"} {
		a := accountForFindings([]roles.Finding{classedEvidence}, b.DiffDigest,
			[]findingResponse{{ID: "f2", AnsweredBy: roles.EvidenceFinding, Evidence: cite}}, b.DiffDigest, b)
		if !a.Settled() {
			t.Fatalf("an EVIDENCE finding citing %q, which ran and passed, was not discharged: %s", cite, a.Diagnosis())
		}
	}
	notRun := accountForFindings([]roles.Finding{classedEvidence}, b.DiffDigest,
		[]findingResponse{{ID: "f2", AnsweredBy: roles.EvidenceFinding, Evidence: "go vet ./..."}}, b.DiffDigest, b)
	if notRun.Settled() {
		t.Fatal("evidence naming a check that never ran discharged an EVIDENCE finding")
	}
	failed := n2bBundle("FAIL")
	failed.Checks[1].Outcome = validation.Failed
	if a := accountForFindings([]roles.Finding{classedEvidence}, b.DiffDigest,
		[]findingResponse{{ID: "f2", AnsweredBy: roles.EvidenceFinding, Evidence: "test"}}, b.DiffDigest, failed); a.Settled() {
		t.Fatal("a check that failed discharged an EVIDENCE finding")
	}
	byCode := accountForFindings([]roles.Finding{classedEvidence}, b.DiffDigest,
		[]findingResponse{{ID: "f2", AnsweredBy: roles.CodeFinding}}, "sha256:moved", b)
	if byCode.Settled() {
		t.Fatal("an unrelated code change discharged an EVIDENCE finding")
	}
}

// W5 PARTIAL ANSWER IS NOT CONVERGENCE. Two outstanding, one answered by its
// own class: the account is open and names exactly the one still open.
func TestW5APartialAnswerNamesTheFindingStillOpen(t *testing.T) {
	b := n2bBundle("ok")
	a := accountForFindings([]roles.Finding{classedCode, classedEvidence}, b.DiffDigest,
		[]findingResponse{{ID: "f1", AnsweredBy: roles.CodeFinding}}, "sha256:moved", b)
	if a.Settled() {
		t.Fatal("a cycle that answered one of two findings converged")
	}
	if ids := openIDs(a); len(ids) != 1 || ids[0] != "f2" {
		t.Fatalf("the open set is %v, want exactly [f2]", ids)
	}
	if d := a.Diagnosis(); !strings.Contains(d, "[f2]") || strings.Contains(d, "[f1]") || !strings.Contains(d, "1 of 2") {
		t.Fatalf("the diagnosis does not name the one still open: %s", d)
	}
	// The same code answer on an UNMOVED candidate discharges nothing.
	if a := accountForFindings([]roles.Finding{classedCode}, b.DiffDigest,
		[]findingResponse{{ID: "f1", AnsweredBy: roles.CodeFinding}}, b.DiffDigest, b); a.Settled() {
		t.Fatal("a CODE finding was discharged by a code answer with no code change")
	}
}

// W6 DISAGREEMENT IS A ROUTE, NOT A LICENCE -- CONTROL, at the predicate. A
// response that would otherwise discharge the finding, plus a dispute of its
// class, leaves it open and records the dispute. Without the dispute the same
// response discharges it -- so the dispute is what held it.
func TestW6ADisputeIsRecordedAndDischargesNothing(t *testing.T) {
	b := n2bBundle("ok")
	disputed := findingResponse{ID: "f1", AnsweredBy: roles.CodeFinding, DisputesClass: roles.EvidenceFinding, Reason: "proof only"}
	a := accountForFindings([]roles.Finding{classedCode}, b.DiffDigest, []findingResponse{disputed}, "sha256:moved", b)
	if a.Settled() || len(a.Disputes) != 1 || a.Disputes[0].ID != "f1" {
		t.Fatalf("a disputed finding was discharged or its dispute dropped: %+v", a)
	}
	if !strings.Contains(a.Diagnosis(), "disputed the class of f1") {
		t.Fatalf("the diagnosis does not carry the escalation: %s", a.Diagnosis())
	}
	undisputed := disputed
	undisputed.DisputesClass = ""
	if a := accountForFindings([]roles.Finding{classedCode}, b.DiffDigest, []findingResponse{undisputed}, "sha256:moved", b); !a.Settled() {
		t.Fatalf("control: the same response without the dispute did not discharge: %s", a.Diagnosis())
	}
}

// A finding with no class cannot be discharged by any response: the accounting
// never guesses what it was.
func TestAClasslessOutstandingFindingIsNeverDischargedByGuess(t *testing.T) {
	b := n2bBundle("ok")
	classless := classedCode
	classless.Class = ""
	for _, r := range []findingResponse{
		{ID: "f1", AnsweredBy: roles.CodeFinding},
		{ID: "f1", AnsweredBy: roles.EvidenceFinding, Evidence: "gofmt"},
		{ID: "f1", AnsweredBy: ""},
	} {
		if a := accountForFindings([]roles.Finding{classless}, b.DiffDigest, []findingResponse{r}, "sha256:moved", b); a.Settled() {
			t.Fatalf("a classless finding was discharged by %+v", r)
		}
	}
}

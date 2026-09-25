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
	classedCode     = roles.Finding{ID: "f1", Severity: roles.Blocking, Class: roles.CodeFinding, Claim: "the refusal does not refuse", Reference: "internal/workflow/engine.go: routePlan", Reason: "it continues"}
	classedEvidence = roles.Finding{ID: "f2", Severity: roles.Major, Class: roles.EvidenceFinding, Claim: "the refusal is proven", Reference: "internal/workflow/engine.go", Reason: "no run of the suite on this candidate", ProofGap: "a passing run of go test ./... on this candidate"}
	// measuredEvidence is the DF-19 f2 as the reviewer wrote it: its proof gap
	// names failing-first history, which no passing check in the bundle is.
	measuredEvidence = roles.Finding{ID: "f2", Severity: roles.Major, Class: roles.EvidenceFinding, Claim: "the failing-first history is established", Reference: "internal/workflow/engine.go", Reason: "no retained failing run", ProofGap: "the required failing-first history"}

	// The file the code finding points at moved; an unrelated one moved; nothing moved.
	movedReferenced = map[string]bool{"internal/workflow/engine.go": true}
	movedUnrelated  = map[string]bool{"README.md": true}
	movedNothing    = map[string]bool{}
)

func openIDs(a findingAccount) []string {
	ids := make([]string, 0, len(a.Open))
	for _, o := range a.Open {
		ids = append(ids, o.ID)
	}
	return ids
}

// codeAnswer is the response that discharges classedCode when its file moved.
var codeAnswer = findingResponse{ID: "f1", AnsweredBy: roles.CodeFinding, Paths: []string{"internal/workflow/engine.go"}}

// W2 RECLASSIFICATION IS REFUSED. The worker says the CODE finding was
// evidence-only and cites a check that passed; the finding stays open, and the
// class the account carries is the one on the reviewer's finding. A response
// that tries to carry a class of its own has nowhere to put it.
//
// Fails if the answered class is compared with anything but the finding's.
func TestW2AnImplementerCannotReclassifyACodeFindingAsEvidence(t *testing.T) {
	b := n2bBundle("ok")
	responses, err := parseFindingResponses(`done. {"finding_responses":[{"id":"f1","class":"evidence","answered_by":"evidence","evidence":"go test ./..."}]}`)
	if err != nil || len(responses) != 1 {
		t.Fatalf("the accounting did not parse: %v %+v", err, responses)
	}
	for _, moved := range []map[string]bool{movedNothing, movedReferenced} {
		a := accountForFindings([]roles.Finding{classedCode}, responses, moved, b)
		if a.Settled() || len(a.Open) != 1 || a.Open[0].ID != "f1" {
			t.Fatalf("moved %v: a CODE finding answered as evidence was discharged: %+v", moved, a)
		}
		if a.Open[0].Class != roles.CodeFinding || !strings.Contains(a.Diagnosis(), `recorded it as "code"`) {
			t.Fatalf("the class was not taken from the finding record: %+v %s", a.Open[0], a.Diagnosis())
		}
	}
}

// W3 THE PROXY IS NOT THE CHECK, at the predicate. The worker pairs the CODE
// finding's id with the right class and the candidate moved -- but not where
// the finding points. Naming the unrelated file it did change, or naming the
// referenced file it did not change, discharges nothing. The control is the
// same answer when the referenced file moved.
//
// Fails if a class-matching answer plus any movement discharges a CODE finding.
func TestW3UnrelatedMovementDoesNotDischargeAClaimedCodeFinding(t *testing.T) {
	b := n2bBundle("ok")
	claimsUnrelated := findingResponse{ID: "f1", AnsweredBy: roles.CodeFinding, Paths: []string{"README.md"}}
	if a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{claimsUnrelated}, movedUnrelated, b); a.Settled() ||
		!strings.Contains(a.Diagnosis(), "touches none of it") {
		t.Fatalf("an unrelated edit, claimed for the CODE finding, discharged it: %s", a.Diagnosis())
	}
	if a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{codeAnswer}, movedUnrelated, b); a.Settled() ||
		!strings.Contains(a.Diagnosis(), "did not change since the finding was raised") {
		t.Fatalf("a claim to have changed the referenced file, which did not move, discharged it: %s", a.Diagnosis())
	}
	if a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{{ID: "f1", AnsweredBy: roles.CodeFinding}}, movedReferenced, b); a.Settled() {
		t.Fatal("a code answer naming no file discharged the CODE finding")
	}
	if a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{codeAnswer}, nil, b); a.Settled() {
		t.Fatal("a code answer was bound to a finding whose candidate could not be compared file by file")
	}
	if a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{codeAnswer}, movedReferenced, b); !a.Settled() {
		t.Fatalf("control: a change to the referenced file did not discharge the CODE finding: %s", a.Diagnosis())
	}
}

// W4 EVIDENCE FINDING DISCHARGED BY EVIDENCE, at the predicate: the check the
// finding's proof gap names, which ran and passed, discharges it on an
// unchanged candidate. Its controls: a passing but irrelevant check (gofmt),
// the measured failing-first demand answered with any passing check, a check
// that never ran, one that failed, a bare kind, and an unrelated code change.
func TestW4AnEvidenceFindingIsDischargedOnlyByExecutedEvidence(t *testing.T) {
	b := n2bBundle("ok")
	answer := func(cite string) []findingResponse {
		return []findingResponse{{ID: "f2", AnsweredBy: roles.EvidenceFinding, Evidence: cite}}
	}
	if a := accountForFindings([]roles.Finding{classedEvidence}, answer("go test ./..."), movedNothing, b); !a.Settled() {
		t.Fatalf("the check the proof gap names, which ran and passed, did not discharge it: %s", a.Diagnosis())
	}
	if a := accountForFindings([]roles.Finding{classedEvidence}, answer("gofmt -l cmd internal"), movedNothing, b); a.Settled() ||
		!strings.Contains(a.Diagnosis(), "not the proof the finding asks for") {
		t.Fatalf("a passing but irrelevant check discharged an EVIDENCE finding: %s", a.Diagnosis())
	}
	for _, cite := range []string{"gofmt -l cmd internal", "go test ./..."} {
		if a := accountForFindings([]roles.Finding{measuredEvidence}, answer(cite), movedNothing, b); a.Settled() {
			t.Fatalf("the measured failing-first demand was discharged by %q, which does not establish it", cite)
		}
	}
	if a := accountForFindings([]roles.Finding{classedEvidence}, answer("go vet ./..."), movedNothing, b); a.Settled() {
		t.Fatal("evidence naming a check that never ran discharged an EVIDENCE finding")
	}
	if a := accountForFindings([]roles.Finding{classedEvidence}, answer("test"), movedNothing, b); a.Settled() {
		t.Fatal("a bare check kind, which names no particular check, discharged an EVIDENCE finding")
	}
	failed := n2bBundle("FAIL")
	failed.Checks[1].Outcome = validation.Failed
	if a := accountForFindings([]roles.Finding{classedEvidence}, answer("go test ./..."), movedNothing, failed); a.Settled() {
		t.Fatal("a check that failed discharged an EVIDENCE finding")
	}
	byCode := accountForFindings([]roles.Finding{classedEvidence},
		[]findingResponse{{ID: "f2", AnsweredBy: roles.CodeFinding, Paths: []string{"internal/workflow/engine.go"}}}, movedReferenced, b)
	if byCode.Settled() {
		t.Fatal("a code change discharged an EVIDENCE finding")
	}
}

// W5 PARTIAL ANSWER IS NOT CONVERGENCE. Two outstanding, one answered by its
// own class: the account is open and names exactly the one still open.
func TestW5APartialAnswerNamesTheFindingStillOpen(t *testing.T) {
	b := n2bBundle("ok")
	a := accountForFindings([]roles.Finding{classedCode, classedEvidence}, []findingResponse{codeAnswer}, movedReferenced, b)
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
	if a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{codeAnswer}, movedNothing, b); a.Settled() {
		t.Fatal("a CODE finding was discharged by a code answer with no code change")
	}
}

// W6 DISAGREEMENT IS A ROUTE, NOT A LICENCE -- CONTROL, at the predicate. A
// response that would otherwise discharge the finding, plus a dispute of its
// class, leaves it open and records the dispute. Without the dispute the same
// response discharges it -- so the dispute is what held it.
func TestW6ADisputeIsRecordedAndDischargesNothing(t *testing.T) {
	b := n2bBundle("ok")
	disputed := codeAnswer
	disputed.DisputesClass, disputed.Reason = roles.EvidenceFinding, "proof only"
	a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{disputed}, movedReferenced, b)
	if a.Settled() || len(a.Disputes) != 1 || a.Disputes[0].ID != "f1" {
		t.Fatalf("a disputed finding was discharged or its dispute dropped: %+v", a)
	}
	if !strings.Contains(a.Diagnosis(), "disputed the class of f1") {
		t.Fatalf("the diagnosis does not carry the escalation: %s", a.Diagnosis())
	}
	undisputed := disputed
	undisputed.DisputesClass = ""
	if a := accountForFindings([]roles.Finding{classedCode}, []findingResponse{undisputed}, movedReferenced, b); !a.Settled() {
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
		codeAnswer,
		{ID: "f1", AnsweredBy: roles.EvidenceFinding, Evidence: "go test ./..."},
		{ID: "f1", AnsweredBy: ""},
	} {
		if a := accountForFindings([]roles.Finding{classless}, []findingResponse{r}, movedReferenced, b); a.Settled() {
			t.Fatalf("a classless finding was discharged by %+v", r)
		}
	}
}

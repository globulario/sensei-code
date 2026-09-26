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
		Findings:   []roles.Finding{{ID: "f1", Severity: roles.Major, Class: roles.ClassEvidence, Claim: "the candidate satisfies the required scoped edit check", Reference: "internal/derived/derived.go", Reason: "validation records only gofmt, vet, build, tests", ProofGap: "scoped Sensei edit check"}},
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

// The rule this replaced read any change to the candidate or its evidence as
// the mechanical answer to the whole review. It is not: a finding is answered
// by a review resolving its id, and new bytes or new outcomes resolve nothing.
func TestAChangedCandidateOrOutcomeDoesNotAnswerAnOutstandingFinding(t *testing.T) {
	first := n2bBundle("ok")
	open := openReviewFrom(revise("codex"), 1, first.DiffDigest, evidenceIdentity(first, n2bAudit))

	edited := n2bBundle("ok")
	edited.DiffDigest = "sha256:other"
	if !open.contradicts(accept("claude"), edited.DiffDigest, evidenceIdentity(edited, n2bAudit)) {
		t.Fatal("a different candidate answered an outstanding finding nobody resolved")
	}
	failed := n2bBundle("ok")
	failed.Checks[1].Outcome = validation.Failed
	failed.Checks[1].ExitStatus = 1
	if !open.contradicts(accept("claude"), failed.DiffDigest, evidenceIdentity(failed, n2bAudit)) {
		t.Fatal("a different outcome answered an outstanding finding nobody resolved")
	}
	resolving := accept("claude")
	resolving.Resolutions = []roles.Resolution{{FindingID: "f1", Outcome: roles.Resolved, Basis: "the scoped edit check now runs"}}
	if open.contradicts(resolving, edited.DiffDigest, evidenceIdentity(edited, n2bAudit)) {
		t.Fatal("a review that resolved the outstanding id was still read as a contradiction")
	}
	stillOpen := accept("claude")
	stillOpen.Resolutions = []roles.Resolution{{FindingID: "f1", Outcome: roles.StillOpen}}
	if !open.contradicts(stillOpen, edited.DiffDigest, evidenceIdentity(edited, n2bAudit)) {
		t.Fatal("an answer of open closed the finding")
	}
	if open.contradicts(revise("claude"), first.DiffDigest, evidenceIdentity(first, n2bAudit)) {
		t.Fatal("only an accepting verdict can contradict a non-accepting one")
	}
}

// The class on the finding's record decides what may resolve it. A code
// finding resolved on the very candidate it was raised against stays open; an
// evidence finding may be resolved there, because evidence needs no change.
func TestAResolutionMustBeCompatibleWithTheRecordedClass(t *testing.T) {
	code := revise("codex")
	code.Findings[0].Class = roles.ClassCode
	code.Provenance.CandidateTree = "tree-1"
	open := openReviewFrom(code, 1, "sha256:one", "ev")

	resolving := accept("claude")
	resolving.Resolutions = []roles.Resolution{{FindingID: "f1", Outcome: roles.Resolved}}
	resolving.Provenance.CandidateTree = "tree-1"
	next, refused := open.advance(resolving, 2, "sha256:one", "ev")
	if len(next.Outstanding) != 1 || len(refused) != 1 || !strings.Contains(refused[0], "discharged only by a changed candidate") {
		t.Fatalf("a code finding was resolved on the candidate it was raised against: outstanding=%v refused=%v", next.Outstanding, refused)
	}
	resolving.Provenance.CandidateTree = "tree-2"
	if next, refused := open.advance(resolving, 2, "sha256:two", "ev"); len(next.Outstanding) != 0 || len(refused) != 0 {
		t.Fatalf("a code finding resolved on a changed candidate stayed open: %v %v", next.Outstanding, refused)
	}

	evidence := openReviewFrom(revise("codex"), 1, "sha256:one", "ev")
	resolving.Provenance.CandidateTree = ""
	if next, _ := evidence.advance(resolving, 2, "sha256:one", "ev"); len(next.Outstanding) != 0 {
		t.Fatalf("an evidence finding could not be resolved without a candidate change: %v", next.Outstanding)
	}
}

// The implementer's answers are measured against the ledger id by id, and the
// class compared is always the recorded one.
func TestTheAccountComparesTheRecordedClass(t *testing.T) {
	v := revise("codex")
	v.Findings = append(v.Findings, roles.Finding{ID: "f2", Severity: roles.Blocking, Class: roles.ClassCode, Claim: "c", Reference: "main.go", Reason: "r"})
	v.Provenance.CandidateTree = "tree-1"
	open := openReviewFrom(v, 1, "sha256:one", "ev")

	responses, err := parseFindingResponses("done\n" + FindingResponsesHeading + "\n```json\n" +
		`[{"finding_id":"f1","class":"evidence","disposition":"discharged"},{"finding_id":"f2","class":"evidence","disposition":"discharged"}]` + "\n```")
	if err != nil || len(responses) != 2 {
		t.Fatalf("responses not read: %v %v", responses, err)
	}
	a := open.account(responses, nil, "tree-2", "sha256:two")
	if a.accounted() || len(a.Unanswered) != 1 || !strings.Contains(a.Unanswered[0], "f2 (code, blocking) is a code finding on the review record") {
		t.Fatalf("a response reclassifying a code finding was accepted: %+v", a)
	}
	if len(a.Answered) != 1 || a.Answered[0] != "f1" {
		t.Fatalf("the evidence answer to the evidence finding was not counted: %+v", a)
	}
	codeAnswer := []roles.FindingResponse{{FindingID: "f1", Class: roles.ClassEvidence, Disposition: roles.Discharged}, {FindingID: "f2", Class: roles.ClassCode, Disposition: roles.Discharged}}
	if a := open.account(codeAnswer, nil, "tree-1", "sha256:one"); len(a.Unanswered) != 1 || !strings.Contains(a.Unanswered[0], "f2 (code, blocking) is a code finding and the candidate has not changed since it was raised") {
		t.Fatalf("a code finding was discharged without a change to the candidate: %+v", a)
	}
	if a := open.account(nil, nil, "tree-2", "sha256:two"); len(a.Unanswered) != 2 {
		t.Fatalf("silence answered something: %+v", a)
	}
	if _, err := parseFindingResponses(FindingResponsesHeading + " not json"); err == nil {
		t.Fatal("an unreadable response block was read as no responses at all")
	}
}

// A finding's class and id are stated by its reviewer. A verdict whose finding
// has neither is refused -- never completed from its severity or its position.
func TestAVerdictWhoseFindingHasNoClassIsRefused(t *testing.T) {
	b := roles.Binding{TaskID: "t", BaseSHA: "b", CandidateDigest: "d"}
	v := revise("codex")
	v.Provenance = roles.Provenance{TaskID: "t", Role: roles.Reviewer, Provider: "codex", SessionMode: roles.Fresh, BaseSHA: "b", CandidateDigest: "d"}
	if err := v.Validate(b, "claude"); err != nil {
		t.Fatalf("a classed verdict was refused: %v", err)
	}
	v.Findings[0].Class = ""
	if err := v.Validate(b, "claude"); err == nil || !strings.Contains(err.Error(), "never inferred") {
		t.Fatalf("a finding with no class was accepted: %v", err)
	}
	v.Findings[0].Class = "CODE"
	if err := v.Validate(b, "claude"); err == nil {
		t.Fatal("a class outside the closed vocabulary was accepted")
	}
	v.Findings[0].Class, v.Findings[0].ID = roles.ClassEvidence, ""
	if err := v.Validate(b, "claude"); err == nil {
		t.Fatal("a finding with no id was accepted")
	}
}

// The identity rule stands for a revise that raised no finding to answer by id,
// and it still never fires on an identity nobody recorded.
func TestAnOpenReviewIsNotRecordedWithoutAnIdentityToBindTo(t *testing.T) {
	instructionsOnly := revise("codex")
	instructionsOnly.Findings, instructionsOnly.Instructions = nil, "run the scoped edit check"
	open := openReviewFrom(instructionsOnly, 1, "", "")
	if open.contradicts(accept("claude"), "", "") {
		t.Fatal("an unbound open review must never fire: silence on identity is not sameness")
	}
	b := n2bBundle("ok")
	bound := openReviewFrom(instructionsOnly, 1, b.DiffDigest, evidenceIdentity(b, n2bAudit))
	if !bound.contradicts(accept("claude"), b.DiffDigest, evidenceIdentity(b, n2bAudit)) {
		t.Fatal("an ACCEPT on the unchanged candidate flipped a finding-less revise on nothing")
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

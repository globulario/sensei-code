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

// FINDING-OWNED CONVERGENCE, at the accounting itself. The loop-level witnesses
// W1-W6 are in nonconvergence_test.go.

var (
	codeFinding     = roles.Finding{ID: "f1", Class: roles.ClassCode, Severity: roles.Blocking, Claim: "an unbindable CREATE refuses the plan", Reference: "internal/workflow/route.go", Reason: "it is reported as status", Correction: "return a typed refusal"}
	evidenceFinding = roles.Finding{ID: "f2", Class: roles.ClassEvidence, Severity: roles.Major, Claim: "W4 failed before the repair", Reference: "the witness record", Reason: "no output", ProofGap: "run it on the pre-repair tree"}
	scopeFinding    = roles.Finding{ID: "f3", Class: roles.ClassScope, Severity: roles.Major, Claim: "the change stays in its bound", Reference: "docs/awareness/x.yaml", Reason: "outside the plan", Correction: "drop it"}
)

func accountOf(t *testing.T, a findingAccounting, id string) findingAccount {
	t.Helper()
	for _, x := range a.Accounts {
		if x.ID == id {
			return x
		}
	}
	t.Fatalf("no account for %s: %+v", id, a.Accounts)
	return findingAccount{}
}

// A verdict whose finding carries no class, or one outside the vocabulary, is
// refused -- not classified by guessing. The response that echoes the absent
// class back is the case a guess would let through. The control is the same verdict with a
// class, which validates, so the refusal is the class and nothing else.
func TestAFindingWithoutAValidClassIsRefused(t *testing.T) {
	binding := roles.Binding{TaskID: "task-1", BaseSHA: "abc123", CandidateDigest: "d1"}
	verdict := func(c roles.Class) roles.ReviewVerdict {
		f := codeFinding
		f.Class = c
		return roles.ReviewVerdict{
			Provenance: roles.Provenance{TaskID: "task-1", Role: roles.Reviewer, Provider: "codex", BaseSHA: "abc123", CandidateDigest: "d1", SessionMode: roles.Fresh},
			Decision:   roles.Revise, Summary: "s", Findings: []roles.Finding{f},
		}
	}
	if err := verdict(roles.ClassCode).Validate(binding, "claude"); err != nil {
		t.Fatalf("the control verdict does not validate, so the refusals below prove nothing: %v", err)
	}
	for _, c := range []roles.Class{"", "Code", " code", "bug", "blocking"} {
		err := verdict(c).Validate(binding, "claude")
		if err == nil || !strings.Contains(err.Error(), "never inferred") {
			t.Fatalf("class %q: want a class refusal, got %v", c, err)
		}
		// An advisory verdict is refused for it at the accounting instead: an
		// unclassified finding is never discharged, whatever answers it.
		f := verdict(c).Findings[0]
		for _, r := range []findingResponse{{ID: "f1", Response: respondCode, Paths: []string{"internal/workflow/route.go"}}, {ID: "f1", Response: findingResponseKind(c)}} {
			a := accountOf(t, reconcileFindings([]roles.Finding{f}, []findingResponse{r}, nil, map[string]bool{"internal/workflow/route.go": true}), "f1")
			if a.Status != findingUnclassified {
				t.Fatalf("class %q: an unclassified finding was judged as %s by a %q response", c, a.Status, r.Response)
			}
		}
	}
}

// W2 RECLASSIFICATION IS REFUSED. The response calls the CODE finding
// evidence-only; the account's class is read from the finding record, and the
// response discharges nothing.
func TestFindingW2TheClassIsTakenFromTheFindingRecord(t *testing.T) {
	acct := reconcileFindings([]roles.Finding{codeFinding},
		[]findingResponse{{ID: "f1", Response: respondEvidence, ClaimedClass: "evidence", Evidence: "the plan said demonstrate"}},
		nil, map[string]bool{"internal/workflow/route.go": true})
	a := accountOf(t, acct, "f1")
	if a.Class != roles.ClassCode || a.Status != findingReclassified {
		t.Fatalf("the responder's reading replaced the finding's class: %+v", a)
	}
	if len(acct.discharged()) != 0 || len(acct.unanswered()) != 1 {
		t.Fatalf("a reclassified response discharged something: %+v", acct)
	}
	// The responder has nowhere to state a class: a response carrying one does
	// not decode, and a malformed account is silence on every finding.
	_, err := parseFindingResponses(`FINDING-RESPONSES: {"responses":[{"id":"f1","response":"evidence","class":"evidence","evidence":"x"}]}`)
	if err == nil {
		t.Fatal("a response that states a finding class was read")
	}
	if got := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, nil, err, nil), "f1"); got.Status != findingSilent {
		t.Fatalf("a malformed account discharged or disputed something: %+v", got)
	}
}

// W3 at the accounting: an unrelated changed file does not answer a CODE finding
// nobody responded to, and a code response naming a file that did not move is
// not attributable.
func TestFindingW3DiffMovementIsNotADischarge(t *testing.T) {
	changed := map[string]bool{"main.go": true}
	if a := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, nil, nil, changed), "f1"); a.Status != findingSilent {
		t.Fatalf("diff movement discharged a CODE finding nobody answered: %+v", a)
	}
	resp := []findingResponse{{ID: "f1", Response: respondCode, Paths: []string{"internal/workflow/route.go"}}}
	if a := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, resp, nil, changed), "f1"); a.Status != findingUnattributed {
		t.Fatalf("a code response naming an unchanged file discharged the finding: %+v", a)
	}
	changed["internal/workflow/route.go"] = true
	if a := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, resp, nil, changed), "f1"); a.Status != findingDischarged {
		t.Fatalf("control: a code response naming a changed file must discharge: %+v", a)
	}
}

// W4 at the accounting: evidence discharges an EVIDENCE finding with no change,
// and an unrelated code change does not.
func TestFindingW4EvidenceDischargesEvidenceOnly(t *testing.T) {
	ok := reconcileFindings([]roles.Finding{evidenceFinding},
		[]findingResponse{{ID: "f2", Response: respondEvidence, Evidence: "go test -run TestW4 at the base: FAIL"}}, nil, nil)
	if a := accountOf(t, ok, "f2"); a.Status != findingDischarged {
		t.Fatalf("execution evidence did not discharge an evidence finding: %+v", a)
	}
	byCode := reconcileFindings([]roles.Finding{evidenceFinding},
		[]findingResponse{{ID: "f2", Response: respondCode, Paths: []string{"main.go"}}}, nil, map[string]bool{"main.go": true})
	if a := accountOf(t, byCode, "f2"); a.Status != findingReclassified {
		t.Fatalf("a code change discharged an evidence finding: %+v", a)
	}
	empty := reconcileFindings([]roles.Finding{evidenceFinding},
		[]findingResponse{{ID: "f2", Response: respondEvidence}}, nil, nil)
	if a := accountOf(t, empty, "f2"); a.Status != findingUnattributed {
		t.Fatalf("an evidence response with no evidence discharged the finding: %+v", a)
	}
}

// W5 at the accounting: one of two answered names the other, and the answered
// one is not owed again.
func TestFindingW5APartialAnswerLeavesTheOtherOpen(t *testing.T) {
	acct := reconcileFindings([]roles.Finding{codeFinding, evidenceFinding},
		[]findingResponse{{ID: "f2", Response: respondEvidence, Evidence: "ran it"}}, nil, nil)
	open := acct.unanswered()
	if len(open) != 1 || open[0].ID != "f1" || !strings.Contains(acct.diagnosis(), "[f1] code finding silent") || strings.Contains(acct.diagnosis(), "[f2]") {
		t.Fatalf("the partial answer does not name exactly the finding still open: %s", acct.diagnosis())
	}
	o := openReview{Findings: []roles.Finding{codeFinding, evidenceFinding, {ID: "m1", Class: roles.ClassCode, Severity: roles.Minor}},
		Discharged: map[string]findingResponse{"f2": {ID: "f2"}}}
	if left := o.outstanding(); len(left) != 1 || left[0].ID != "f1" {
		t.Fatalf("outstanding must be the non-minor findings not yet discharged: %+v", left)
	}
}

// W6 at the accounting: a disagreement is recorded as a dispute of the
// finding's class, and it discharges nothing.
func TestFindingW6ADisagreementDischargesNothing(t *testing.T) {
	acct := reconcileFindings([]roles.Finding{codeFinding},
		[]findingResponse{{ID: "f1", Response: respondDisagree, ClaimedClass: "evidence", Reason: "the plan said demonstrate"}}, nil, nil)
	a := accountOf(t, acct, "f1")
	if a.Status != findingDisputed || a.Class != roles.ClassCode || len(acct.discharged()) != 0 || len(acct.disputed()) != 1 {
		t.Fatalf("a disagreement was not a dispute of the recorded class: %+v", acct)
	}
	p := classDisputePrompt("task", "plan", "audit", openReview{Reviewer: "codex"}, acct)
	for _, want := range []string{"[f1]", "code finding", "evidence", "does not discharge"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the escalation prompt lacks %q:\n%s", want, p)
		}
	}
}

// A SCOPE finding is discharged by a change, not by argument.
func TestAScopeFindingIsDischargedByAChangeOnly(t *testing.T) {
	changed := changedSince(diffFileDigests("diff --git a/docs/awareness/x.yaml b/docs/awareness/x.yaml\n+x\ndiff --git a/main.go b/main.go\n+y"), "diff --git a/main.go b/main.go\n+y")
	if !changed["docs/awareness/x.yaml"] || changed["main.go"] {
		t.Fatalf("a file that left the diff must read as changed and an identical one must not: %v", changed)
	}
	acct := reconcileFindings([]roles.Finding{scopeFinding},
		[]findingResponse{{ID: "f3", Response: respondScope, Paths: []string{"docs/awareness/x.yaml"}}}, nil, changed)
	if a := accountOf(t, acct, "f3"); a.Status != findingDischarged {
		t.Fatalf("dropping the out-of-scope file did not discharge the scope finding: %+v", a)
	}
	argued := reconcileFindings([]roles.Finding{scopeFinding},
		[]findingResponse{{ID: "f3", Response: respondEvidence, Evidence: "it is in scope"}}, nil, changed)
	if a := accountOf(t, argued, "f3"); a.Status != findingReclassified {
		t.Fatalf("an argument discharged a scope finding: %+v", a)
	}
}

// Each finding is answered once; responses naming nothing open are reported.
func TestAFindingAnsweredTwiceIsNotDischarged(t *testing.T) {
	acct := reconcileFindings([]roles.Finding{evidenceFinding}, []findingResponse{
		{ID: "f2", Response: respondEvidence, Evidence: "a"}, {ID: "f2", Response: respondDisagree}, {ID: "f9", Response: respondEvidence, Evidence: "b"},
	}, nil, nil)
	if a := accountOf(t, acct, "f2"); a.Status != findingAmbiguous {
		t.Fatalf("a finding answered twice was settled: %+v", a)
	}
	if len(acct.Unmatched) != 1 || acct.Unmatched[0] != "f9" {
		t.Fatalf("a response to no open finding was not reported: %+v", acct.Unmatched)
	}
}

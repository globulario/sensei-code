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
			a := accountOf(t, reconcileFindings([]roles.Finding{f}, []findingResponse{r}, nil, map[string]bool{"internal/workflow/route.go": true}, nil), "f1")
			if a.Status != findingUnclassified {
				t.Fatalf("class %q: an unclassified finding was judged as %s by a %q response", c, a.Status, r.Response)
			}
		}
	}
}

// ran is the execution record of one check the validation broker ran against
// candidate task-1 at digest d1: the only thing an evidence answer can cite.
func ran(outcome validation.Outcome, command string, args ...string) validation.Evidence {
	return validation.Evidence{Kind: validation.Test, Command: command, Args: args, CandidateID: "task-1", DiffDigest: "d1", Outcome: outcome}
}

var brokerRan = executedChecks(validation.Bundle{CandidateID: "task-1", DiffDigest: "d1", Checks: []validation.Evidence{
	ran(validation.Passed, "go", "test", "-run", "TestW4", "./internal/workflow/"),
}}, "task-1", "d1")

// W2 RECLASSIFICATION IS REFUSED. The response calls the CODE finding
// evidence-only; the account's class is read from the finding record, and the
// response answers nothing -- even citing a check the broker really ran.
func TestFindingW2TheClassIsTakenFromTheFindingRecord(t *testing.T) {
	acct := reconcileFindings([]roles.Finding{codeFinding},
		[]findingResponse{{ID: "f1", Response: respondEvidence, ClaimedClass: "evidence", Executions: []string{"go test -run TestW4 ./internal/workflow/"}}},
		nil, map[string]bool{"internal/workflow/route.go": true}, brokerRan)
	a := accountOf(t, acct, "f1")
	if a.Class != roles.ClassCode || a.Status != findingReclassified {
		t.Fatalf("the responder's reading replaced the finding's class: %+v", a)
	}
	if len(acct.answered()) != 0 || len(acct.unanswered()) != 1 {
		t.Fatalf("a reclassified response answered something: %+v", acct)
	}
	// The responder has nowhere to state a class: a response carrying one does
	// not decode, and a malformed account is silence on every finding.
	_, err := parseFindingResponses(`FINDING-RESPONSES: {"responses":[{"id":"f1","response":"evidence","class":"evidence","evidence":"x"}]}`)
	if err == nil {
		t.Fatal("a response that states a finding class was read")
	}
	if got := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, nil, err, nil, nil), "f1"); got.Status != findingSilent {
		t.Fatalf("a malformed account answered or disputed something: %+v", got)
	}
}

// W3 at the accounting and at its reviewer. An unrelated changed file does not
// answer a CODE finding nobody responded to; a code response naming a file that
// did not move is not attributable; and a code response naming a file that DID
// move -- the same file the finding names, moved by an unrelated line -- is
// only answered. It is discharged by its reviewer naming it resolved, and by
// nothing else: not by the decision, not by an ACCEPT that is silent on it.
//
// Fails if: file movement is read as a discharge (the path-overlap proxy), or a
// verdict's decision stands in for resolving the finding by id.
func TestFindingW3MovementInTheNamedFileIsNotADischarge(t *testing.T) {
	changed := map[string]bool{"main.go": true}
	if a := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, nil, nil, changed, nil), "f1"); a.Status != findingSilent {
		t.Fatalf("diff movement answered a CODE finding nobody answered: %+v", a)
	}
	resp := []findingResponse{{ID: "f1", Response: respondCode, Paths: []string{"internal/workflow/route.go"}}}
	if a := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, resp, nil, changed, nil), "f1"); a.Status != findingUnattributed {
		t.Fatalf("a code response naming an unchanged file answered the finding: %+v", a)
	}
	changed["internal/workflow/route.go"] = true
	moved := accountOf(t, reconcileFindings([]roles.Finding{codeFinding}, resp, nil, changed, nil), "f1")
	if moved.Status != findingAnswered {
		t.Fatalf("a code response naming a moved file must be answered -- and only answered: %+v", moved)
	}
	blind := settleByReviewer([]roles.Finding{codeFinding}, roles.ReviewVerdict{Decision: roles.Accept, Summary: "the candidate stands"})
	if len(blind.Discharged) != 0 || len(blind.Open) != 1 || blind.Open[0].ID != "f1" || blind.Open[0].Class != roles.ClassCode {
		t.Fatalf("an ACCEPT silent on f1 discharged it: %+v", blind)
	}
	if !strings.Contains(blind.diagnosis(), "[f1] code finding") {
		t.Fatalf("the settlement does not name the open finding: %s", blind.diagnosis())
	}
	reraised := settleByReviewer([]roles.Finding{codeFinding}, roles.ReviewVerdict{Decision: roles.Revise, Resolved: []string{"f1"}, Findings: []roles.Finding{codeFinding}})
	if len(reraised.Discharged) != 0 {
		t.Fatalf("a finding both resolved and raised again was discharged: %+v", reraised)
	}
	confirmed := settleByReviewer([]roles.Finding{codeFinding}, roles.ReviewVerdict{Decision: roles.Accept, Resolved: []string{"f1", "f7"}})
	if len(confirmed.Discharged) != 1 || len(confirmed.Open) != 0 || len(confirmed.Unknown) != 1 || confirmed.Unknown[0] != "f7" {
		t.Fatalf("control: the reviewer naming f1 resolved must discharge it, and only it: %+v", confirmed)
	}
}

// W4 at the accounting: a check the broker ran answers an EVIDENCE finding with
// no change, and an unrelated code change does not. The CONTROL is the
// responder's own account: prose, or a command line the broker never ran
// against this candidate, answers nothing.
//
// Fails if: a non-empty evidence string is read as execution evidence, a cited
// command is taken at the responder's word, or a check bound to other bytes is
// admitted.
func TestFindingW4OnlyABrokerExecutionAnswersAnEvidenceFinding(t *testing.T) {
	ok := reconcileFindings([]roles.Finding{evidenceFinding},
		[]findingResponse{{ID: "f2", Response: respondEvidence, Executions: []string{"go test -run TestW4 ./internal/workflow/"}}}, nil, nil, brokerRan)
	if a := accountOf(t, ok, "f2"); a.Status != findingAnswered {
		t.Fatalf("a check the broker ran did not answer an evidence finding: %+v", a)
	}
	byCode := reconcileFindings([]roles.Finding{evidenceFinding},
		[]findingResponse{{ID: "f2", Response: respondCode, Paths: []string{"main.go"}}}, nil, map[string]bool{"main.go": true}, brokerRan)
	if a := accountOf(t, byCode, "f2"); a.Status != findingReclassified {
		t.Fatalf("a code change answered an evidence finding: %+v", a)
	}
	for name, r := range map[string]findingResponse{
		"empty":      {ID: "f2", Response: respondEvidence},
		"prose only": {ID: "f2", Response: respondEvidence, Evidence: "ran it: PASS"},
		"fabricated": {ID: "f2", Response: respondEvidence, Executions: []string{"go test ./..."}, Evidence: "PASS"},
		"one of two": {ID: "f2", Response: respondEvidence, Executions: []string{"go test -run TestW4 ./internal/workflow/", "go test -run TestW5 ./internal/workflow/"}},
	} {
		if a := accountOf(t, reconcileFindings([]roles.Finding{evidenceFinding}, []findingResponse{r}, nil, nil, brokerRan), "f2"); a.Status != findingUnattributed {
			t.Fatalf("%s: the responder's own account answered an evidence finding: %+v", name, a)
		}
	}
	// What the broker did not run is not in the set: not permitted, errored,
	// or bound to other bytes.
	set := executedChecks(validation.Bundle{Checks: []validation.Evidence{
		ran(validation.NotPermitted, "go", "vet"), ran(validation.Errored, "go", "build"),
		{Command: "go", Args: []string{"test"}, CandidateID: "task-1", DiffDigest: "d0", Outcome: validation.Passed},
		ran(validation.Failed, "gofmt", "-l", "main.go"),
	}}, "task-1", "d1")
	if len(set) != 1 || !set["gofmt -l main.go"] {
		t.Fatalf("the executed set must hold exactly the check that ran on these bytes: %v", set)
	}
}

// W5 at the accounting: one of two answered names the other, and neither is
// dropped from what is owed until a review resolves it.
func TestFindingW5APartialAnswerLeavesTheOtherOpen(t *testing.T) {
	acct := reconcileFindings([]roles.Finding{codeFinding, evidenceFinding},
		[]findingResponse{{ID: "f2", Response: respondEvidence, Executions: []string{"go test -run TestW4 ./internal/workflow/"}}}, nil, nil, brokerRan)
	open := acct.unanswered()
	if len(open) != 1 || open[0].ID != "f1" || !strings.Contains(acct.diagnosis(), "[f1] code finding silent") || strings.Contains(acct.diagnosis(), "[f2]") {
		t.Fatalf("the partial answer does not name exactly the finding still open: %s", acct.diagnosis())
	}
	o := openReview{Findings: []roles.Finding{codeFinding, evidenceFinding, {ID: "m1", Class: roles.ClassCode, Severity: roles.Minor}}}
	if left := o.outstanding(); len(left) != 2 || left[0].ID != "f1" || left[1].ID != "f2" {
		t.Fatalf("outstanding must be every non-minor finding: %+v", left)
	}
	// A later review that resolves f2 and says nothing of f1 carries f1 on
	// under its own id and class, beside the review's own finding.
	s := settleByReviewer([]roles.Finding{codeFinding, evidenceFinding}, roles.ReviewVerdict{Decision: roles.Revise, Resolved: []string{"f2"}, Findings: []roles.Finding{scopeFinding}})
	carried := carriedWith([]roles.Finding{scopeFinding}, s)
	if len(carried) != 2 || carried[0].ID != "f3" || carried[1].ID != "f1" || carried[1].Class != roles.ClassCode {
		t.Fatalf("the unresolved finding was not carried under its own class: %+v", carried)
	}
}

// W6 at the accounting: a disagreement is recorded as a dispute of the
// finding's class, and it answers nothing.
func TestFindingW6ADisagreementDischargesNothing(t *testing.T) {
	acct := reconcileFindings([]roles.Finding{codeFinding},
		[]findingResponse{{ID: "f1", Response: respondDisagree, ClaimedClass: "evidence", Reason: "the plan said demonstrate"}}, nil, nil, nil)
	a := accountOf(t, acct, "f1")
	if a.Status != findingDisputed || a.Class != roles.ClassCode || len(acct.answered()) != 0 || len(acct.disputed()) != 1 {
		t.Fatalf("a disagreement was not a dispute of the recorded class: %+v", acct)
	}
	p := classDisputePrompt("task", "plan", "audit", openReview{Reviewer: "codex"}, acct)
	for _, want := range []string{"[f1]", "code finding", "evidence", "does not discharge"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the escalation prompt lacks %q:\n%s", want, p)
		}
	}
}

// A SCOPE finding is answered by a change, not by argument.
func TestAScopeFindingIsDischargedByAChangeOnly(t *testing.T) {
	changed := changedSince(diffFileDigests("diff --git a/docs/awareness/x.yaml b/docs/awareness/x.yaml\n+x\ndiff --git a/main.go b/main.go\n+y"), "diff --git a/main.go b/main.go\n+y")
	if !changed["docs/awareness/x.yaml"] || changed["main.go"] {
		t.Fatalf("a file that left the diff must read as changed and an identical one must not: %v", changed)
	}
	acct := reconcileFindings([]roles.Finding{scopeFinding},
		[]findingResponse{{ID: "f3", Response: respondScope, Paths: []string{"docs/awareness/x.yaml"}}}, nil, changed, nil)
	if a := accountOf(t, acct, "f3"); a.Status != findingAnswered {
		t.Fatalf("dropping the out-of-scope file did not answer the scope finding: %+v", a)
	}
	argued := reconcileFindings([]roles.Finding{scopeFinding},
		[]findingResponse{{ID: "f3", Response: respondEvidence, Executions: []string{"go test -run TestW4 ./internal/workflow/"}}}, nil, changed, brokerRan)
	if a := accountOf(t, argued, "f3"); a.Status != findingReclassified {
		t.Fatalf("an argument answered a scope finding: %+v", a)
	}
}

// Each finding is answered once; responses naming nothing open are reported.
func TestAFindingAnsweredTwiceIsNotDischarged(t *testing.T) {
	x := []string{"go test -run TestW4 ./internal/workflow/"}
	acct := reconcileFindings([]roles.Finding{evidenceFinding}, []findingResponse{
		{ID: "f2", Response: respondEvidence, Executions: x}, {ID: "f2", Response: respondDisagree}, {ID: "f9", Response: respondEvidence, Executions: x},
	}, nil, nil, brokerRan)
	if a := accountOf(t, acct, "f2"); a.Status != findingAmbiguous {
		t.Fatalf("a finding answered twice was settled: %+v", a)
	}
	if len(acct.Unmatched) != 1 || acct.Unmatched[0] != "f9" {
		t.Fatalf("a response to no open finding was not reported: %+v", acct.Unmatched)
	}
}

// The reviewer is asked to settle each answered finding by id, and is shown the
// finding as it was raised -- not the worker's account of it.
func TestTheReviewerIsAskedToResolveEachAnsweredFindingById(t *testing.T) {
	if got := answeredFindingsSection(""); got != "" {
		t.Fatalf("a review with nothing answered grew a section: %q", got)
	}
	p := reviewPrompt(roles.IndependentReviewPacket{Task: "t", Diff: "d", Answered: renderFindings([]roles.Finding{codeFinding})})
	for _, want := range []string{"[f1] blocking code finding", `"resolved"`, "an ACCEPT does not close it"} {
		if !strings.Contains(p, want) {
			t.Fatalf("the review prompt lacks %q:\n%s", want, p)
		}
	}
}

package roles

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

// The tests here are the properties section 10 of
// docs/multi-agent-adversarial-collaboration.md asks for, at the level where
// they are decidable without a provider. The ones that need the workflow loop
// live in internal/workflow.

func reviewProvenance() Provenance {
	return Provenance{
		TaskID:          "task-1",
		Role:            Reviewer,
		Provider:        "codex",
		SessionID:       "session-1",
		SessionMode:     Fresh,
		BaseSHA:         "abc123",
		CandidateDigest: "digest-1",
		At:              time.Unix(0, 0).UTC(),
	}
}

func candidateBinding() Binding {
	return Binding{TaskID: "task-1", BaseSHA: "abc123", CandidateDigest: "digest-1"}
}

func acceptingVerdict() ReviewVerdict {
	return ReviewVerdict{Provenance: reviewProvenance(), Decision: Accept, Summary: "the candidate stands"}
}

// Property 1.
func TestAWorkerCannotReviewItsOwnCandidate(t *testing.T) {
	v := acceptingVerdict()
	v.Provenance.Provider = "claude"
	if err := v.Validate(candidateBinding(), "claude"); err == nil {
		t.Fatal("claude implemented this candidate and was allowed to accept it")
	}
	if err := v.Validate(candidateBinding(), "codex"); err != nil {
		t.Fatalf("a different provider must be allowed to review: %v", err)
	}
}

// Property 1, the other half: a verdict is a reviewer's or it is nothing. An
// implementer that returns review-shaped JSON must not conclude a review.
func TestOnlyAReviewerRoleConcludesAReview(t *testing.T) {
	v := acceptingVerdict()
	v.Provenance.Role = Implementer
	err := v.Validate(candidateBinding(), "")
	if err == nil {
		t.Fatal("an implementer concluded a review")
	}
	if !strings.Contains(err.Error(), "implementer") {
		t.Fatalf("the refusal should name the role that tried: %v", err)
	}
}

// Property 2. There is no admission here to manufacture: the type carries a
// reviewer's opinion and has no field, method or path that could produce one.
func TestReviewerAcceptanceIsNotAdmission(t *testing.T) {
	v := acceptingVerdict()
	if !v.Accepts() {
		t.Fatal("an accepting verdict should report its own decision")
	}
	for _, field := range fieldNames(reflect.TypeOf(v)) {
		lowered := strings.ToLower(field)
		if strings.Contains(lowered, "admit") || strings.Contains(lowered, "admission") || strings.Contains(lowered, "admitted") {
			t.Fatalf("ReviewVerdict.%s could carry admission; Sensei owns that transition", field)
		}
	}
	for i := 0; i < reflect.TypeOf(v).NumMethod(); i++ {
		name := reflect.TypeOf(v).Method(i).Name
		if strings.Contains(strings.ToLower(name), "admit") {
			t.Fatalf("ReviewVerdict.%s exists; a reviewer cannot admit anything", name)
		}
	}
}

// Property 3.
func TestARevisionInvalidatesTheVerdictBoundToThePreviousOne(t *testing.T) {
	v := acceptingVerdict()
	revised := candidateBinding()
	revised.CandidateDigest = "digest-2"

	err := v.Validate(revised, "")
	if err == nil {
		t.Fatal("a review of the previous revision was accepted against the new one")
	}
	if !strings.Contains(err.Error(), "earlier revision") {
		t.Fatalf("the refusal should say the review is superseded, not merely foreign: %v", err)
	}
	if !revised.Stale(v.Provenance) {
		t.Fatal("the verdict should be reported as stale for this task")
	}
}

// Property 5. The reviewer's packet carries governing facts and the artifact,
// and has nowhere to put the worker's reasoning.
func TestTheReviewPacketCannotCarryWorkerReasoning(t *testing.T) {
	forbidden := []string{"transcript", "reasoning", "workeroutput", "workerlog", "rationalefromworker", "implementernotes"}
	for _, field := range fieldNames(reflect.TypeOf(IndependentReviewPacket{})) {
		lowered := strings.ToLower(field)
		for _, bad := range forbidden {
			if strings.Contains(lowered, bad) {
				t.Fatalf("IndependentReviewPacket.%s would hand the reviewer the author's argument", field)
			}
		}
	}
	p := IndependentReviewPacket{Provenance: reviewProvenance(), Task: "t", Diff: "d", Audit: "a", Validation: "v"}
	if p.Diff == "" || p.Audit == "" || p.Validation == "" {
		t.Fatal("the packet must carry the artifact and the evidence about it")
	}
}

// Property 6.
func TestHandoffPreservesTheTaskRatherThanRestartingIt(t *testing.T) {
	h := WorkerHandoffPacket{
		Provenance: Provenance{TaskID: "task-1", Role: Implementer, Provider: "codex",
			BaseSHA: "abc123", SessionMode: Fresh},
		PreviousWorker: "claude",
		State:          "TASK IDENTITY\n  task: task-1\n",
		OpenFindings:   []Finding{{ID: "f1", Severity: Blocking, Claim: "no test covers the refusal", Reference: "internal/x.go", Reason: "r", Correction: "add one"}},
		CyclesUsed:     3, CyclesAllowed: 3,
	}
	if err := h.Continuity(candidateBinding()); err != nil {
		t.Fatalf("this handoff continues the same task: %v", err)
	}
	rendered := h.Render()
	if !strings.Contains(rendered, "no test covers the refusal") {
		t.Fatal("unanswered findings must travel with the candidate")
	}
	if !strings.Contains(rendered, "3 of 3 review cycles") {
		t.Fatal("the next worker must know how much budget was already spent")
	}

	// A handoff is not bound to a revision: the next worker changes the
	// candidate, so binding one would make every handoff stale on arrival.
	if err := h.Continuity(candidateBinding()); err != nil {
		t.Fatalf("a handoff must not be invalidated by the digest it is about to change: %v", err)
	}

	foreign := h
	foreign.Provenance.TaskID = "task-2"
	if err := foreign.Continuity(candidateBinding()); err == nil {
		t.Fatal("a handoff from another task was accepted as continuity")
	}

	empty := h
	empty.State = ""
	if err := empty.Continuity(candidateBinding()); err == nil {
		t.Fatal("a handoff carrying no state would make the next worker start over")
	}
}

// Property 10. There is no majority function to call, and unanimity alone
// cannot produce a decision.
func TestAgreementBetweenAgentsHasNoAuthority(t *testing.T) {
	unanimous := Reconciliation{
		Disputed: "whether the candidate may widen the plan",
		Inputs: []Claim{
			{Agent: "codex", Role: Reviewer, Position: "it may"},
			{Agent: "claude", Role: Implementer, Position: "it may"},
			{Agent: "chatgpt", Role: Architect, Position: "it may"},
		},
		Decision:  "allow it",
		Authority: ArchitectAuthority,
	}
	if !unanimous.Unanimous() {
		t.Fatal("this fixture is unanimous")
	}
	err := unanimous.Validate()
	if err == nil {
		t.Fatal("three agreeing agents produced a decision with nothing to check")
	}
	if !strings.Contains(err.Error(), "not evidence") {
		t.Fatalf("the refusal should say why agreement does not count: %v", err)
	}

	grounded := unanimous
	grounded.Canonical = []Evidence{{Kind: GraphEvidence, Reference: "invariant:sensei_code.workflow.context_never_widens_worker_scope"}}
	if err := grounded.Validate(); err != nil {
		t.Fatalf("canonical evidence should carry the decision: %v", err)
	}
	if !strings.Contains(grounded.Describe(), "not on the agreement") {
		t.Fatal("the receipt should record that unanimity was not the reason")
	}
}

// Property 11.
func TestHighRiskRequiresCrossProviderReview(t *testing.T) {
	high := PolicyFor("system", "human")
	if !high.CrossProviderReview {
		t.Fatalf("a system-wide change with a human gate must require independent review: %+v", high)
	}
	if err := high.Check("codex", Assignment{Role: Reviewer, Provider: "codex"}); err == nil {
		t.Fatal("the implementing provider was allowed to review a high-risk change")
	}
	if err := high.Check("codex", Assignment{Role: Reviewer, Provider: "claude"}); err != nil {
		t.Fatalf("a different provider satisfies the requirement: %v", err)
	}

	local := PolicyFor("local", "none")
	if local.CrossProviderReview {
		t.Fatalf("a local change should not be forced through the whole apparatus: %+v", local)
	}

	// An absent verdict is the case that once read as permission. It must read
	// as the strongest requirement instead.
	unknown := PolicyFor("", "")
	if !unknown.CrossProviderReview {
		t.Fatal("unclassified risk must fail closed, not fall through to the cheap path")
	}
	if !strings.Contains(unknown.Reason, "unclassified") {
		t.Fatalf("the policy should say why it is strict: %q", unknown.Reason)
	}
}

// Property 12.
func TestAFailedProviderFallsBackBeforeAnybodyIsInterrupted(t *testing.T) {
	caps := Capabilities{
		{Provider: "claude", Roles: []Role{Implementer, Reviewer}},
		{Provider: "codex", Roles: []Role{Implementer, Reviewer}},
		{Provider: "chatgpt", Roles: []Role{Architect, Reviewer}},
	}
	a, err := Assign(Reviewer, caps, "claude")
	if err != nil {
		t.Fatalf("two providers remain for review: %v", err)
	}
	if a.Provider == "claude" {
		t.Fatal("the implementing provider was assigned the review it is excluded from")
	}
	next, ok := a.Fallback(a.Provider)
	if !ok {
		t.Fatal("a bounded alternate exists and was not offered")
	}
	if next.Provider == a.Provider {
		t.Fatal("the fallback returned the provider that just failed")
	}
	if _, ok := next.Fallback(next.Provider); ok {
		t.Fatal("the alternates are exhausted and something was still offered")
	}

	// Exhaustion is a real condition and it must be reported as one rather than
	// silently satisfied by the excluded provider.
	only := Capabilities{{Provider: "claude", Roles: []Role{Implementer, Reviewer}}}
	if _, err := Assign(Reviewer, only, "claude"); err == nil {
		t.Fatal("a deployment with one provider silently let it review itself")
	}
}

// Property 14.
func TestAnArtifactAboutAnotherCandidateIsRefused(t *testing.T) {
	b := candidateBinding()
	for _, tc := range []struct {
		name   string
		mutate func(*Provenance)
		want   string
	}{
		{"another task", func(p *Provenance) { p.TaskID = "task-9" }, "task"},
		{"another base", func(p *Provenance) { p.BaseSHA = "def456" }, "base commit"},
		{"another revision", func(p *Provenance) { p.CandidateDigest = "digest-9" }, "candidate revision"},
		{"no stated revision", func(p *Provenance) { p.CandidateDigest = "" }, "candidate revision"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := reviewProvenance()
			tc.mutate(&p)
			err := b.Verify(p)
			if err == nil {
				t.Fatalf("an artifact about %s was accepted", tc.name)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("the refusal should name the identity that disagreed: %v", err)
			}
		})
	}
}

// Section 9.3: an adversarial role never inherits the session of what it
// attacks, and the record says so rather than implying it.
func TestAdversarialRolesStartFresh(t *testing.T) {
	for _, r := range All {
		if r.Adversarial() && r.SessionMode() != Fresh {
			t.Fatalf("%s attacks another role's output and must not inherit its session", r)
		}
	}
	if Architect.SessionMode() != Continue {
		t.Fatal("the architect is the conversation; reconstructing it every turn discards the dialogue")
	}
	if Implementer.Adversarial() {
		t.Fatal("the implementer is not an adversarial role")
	}

	inherited := acceptingVerdict()
	inherited.Provenance.SessionMode = Continue
	if err := inherited.Validate(candidateBinding(), ""); err == nil {
		t.Fatal("a review that inherited the session it was judging was accepted")
	}
}

// A verdict must not disagree with itself: accepting over a blocking finding
// would resolve the contradiction silently in favour of the softer half.
func TestAVerdictCannotAcceptOverItsOwnBlockingFinding(t *testing.T) {
	v := acceptingVerdict()
	v.Findings = []Finding{{ID: "f1", Severity: Blocking, Claim: "the guard is unreachable", Reference: "internal/x.go", Reason: "r", Correction: "c"}}
	if err := v.Validate(candidateBinding(), ""); err == nil {
		t.Fatal("a verdict accepted while recording a blocking finding")
	}
}

func TestABlockingFindingMustPointAtSomething(t *testing.T) {
	v := acceptingVerdict()
	v.Decision = Revise
	v.Instructions = "fix it"
	v.Findings = []Finding{{ID: "f1", Severity: Blocking, Claim: "something is wrong", Reason: "r"}}
	if err := v.Validate(candidateBinding(), ""); err == nil {
		t.Fatal("a blocking finding with nothing to open was accepted")
	}
}

func TestReviseMustSayWhatToChange(t *testing.T) {
	v := acceptingVerdict()
	v.Decision = Revise
	if err := v.Validate(candidateBinding(), ""); err == nil {
		t.Fatal("a revision request with no instructions and no findings was accepted")
	}
}

func TestTheRoleVocabularyIsClosed(t *testing.T) {
	if Role("reviewer_v2").Valid() {
		t.Fatal("an unknown role has no instruction, context, or session rule and must not be accepted")
	}
	for _, r := range All {
		if !r.Valid() {
			t.Fatalf("%s is in All and reports invalid", r)
		}
	}
}

func fieldNames(t reflect.Type) []string {
	var out []string
	for i := 0; i < t.NumField(); i++ {
		out = append(out, t.Field(i).Name)
	}
	return out
}

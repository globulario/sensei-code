package workflow

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/taskstate"
)

// These are the properties from section 10 of
// docs/multi-agent-adversarial-collaboration.md that are about the loop rather
// than about the artifacts. The artifact-level ones live in internal/roles.

func testReviewPacket(tc taskContext, plan, diff, audit, evidence string) roles.IndependentReviewPacket {
	binding := roles.Binding{TaskID: "task-1", BaseSHA: "abc123", CandidateDigest: candidateRevision(diff)}
	return reviewPacket(tc, binding, certifiedStart{}, plan, diff, audit, evidence)
}

// Property 2. Reviewer acceptance is one input to a transition Sensei owns, and
// the engine reaches that transition through judgeCandidate rather than through
// the reviewer's decision alone.
func TestReviewerAcceptanceDoesNotManufactureAdmission(t *testing.T) {
	refused := sensei.DiffAuditDecision{Decision: "fail"}
	if judgeCandidate("accept", refused).Accepted {
		t.Fatal("a reviewer accepted over a Sensei refusal and the candidate was accepted")
	}
	// And the engine reaches the accept transition through that gate. The
	// ordering is the property: a change report emitted before Sensei's verdict
	// is read would be an acceptance the reviewer decided alone.
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if !strings.Contains(body, "judgeCandidate") {
		t.Fatal("the accept branch no longer consults Sensei's verdict")
	}
	if !before(body, "judgeCandidate", "emitChangeReport") {
		t.Fatal("the candidate is reported accepted before Sensei's verdict is read")
	}
}

// before reports whether one call appears ahead of another in a function body.
// funcBody renders the AST in statement order, so position is order.
func before(body, first, second string) bool {
	i, j := strings.Index(body, first), strings.Index(body, second)
	return i >= 0 && j >= 0 && i < j
}

// Property 4. A review runs in a session that inherited nothing, and the
// request says so rather than relying on the transport to happen to be
// stateless.
func TestAReviewRunsInAnIndependentSession(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "askReviewer")
	if !strings.Contains(body, "roles.Fresh(") {
		t.Fatal("the reviewer is run without declaring an independent session")
	}
	if strings.Contains(body, "roles.Continue(") {
		t.Fatal("the reviewer was given the architect's conversation to continue")
	}
	// And the verdict refuses itself if the transport did not honour it, so the
	// declaration cannot be the whole guarantee.
	v := roles.ReviewVerdict{
		Provenance: roles.Provenance{TaskID: "task-1", Role: roles.Reviewer, Provider: "codex",
			SessionMode: roles.Continue, BaseSHA: "abc123", CandidateDigest: "d1"},
		Decision: roles.Accept, Summary: "fine",
	}
	if err := v.Validate(roles.Binding{TaskID: "task-1", BaseSHA: "abc123", CandidateDigest: "d1"}, "claude"); err == nil {
		t.Fatal("a verdict from an inherited session was accepted")
	}
}

// Property 7. A revision request carries something the worker can act on, and
// the next cycle is reviewed again rather than trusted.
func TestReviseProducesBoundedInstructionsAndAnotherReview(t *testing.T) {
	v := roles.ReviewVerdict{
		Provenance: roles.Provenance{TaskID: "t", Role: roles.Reviewer, Provider: "codex", SessionMode: roles.Fresh},
		Decision:   roles.Revise,
		Summary:    "the guard is unreachable",
		Findings: []roles.Finding{{ID: "f1", Severity: roles.Blocking, Claim: "the guard cannot fire",
			Reference: "internal/broker/broker.go", Reason: "the branch above returns first", Correction: "move the guard"}},
	}
	instruction := v.Instruction()
	for _, want := range []string{"internal/broker/broker.go", "move the guard"} {
		if !strings.Contains(instruction, want) {
			t.Fatalf("the revision instruction dropped %q; the worker cannot act on a summary alone", want)
		}
	}

	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if !strings.Contains(body, "e.resolveReview(") {
		t.Fatal("the cycle no longer reviews, so a revision would not be reviewed again")
	}
	if !strings.Contains(body, "review.Instruction(") {
		t.Fatal("the revise branch no longer carries the structured findings back to the worker")
	}
}

// Property 8. A reviewer escalation reaches the architect. It cannot reach the
// human directly, because a reviewer that could interrupt a person would let
// one nervous model manufacture a Level-3 event.
func TestReviewerEscalationReachesTheArchitectFirst(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if !strings.Contains(body, "roles.Escalate(") {
		t.Fatal("the loop no longer handles a reviewer escalation at all")
	}
	if !strings.Contains(body, "resolveArchitecture") {
		t.Fatal("a reviewer escalation no longer reaches the architect")
	}
	if strings.Contains(body, "awaitHuman") || strings.Contains(body, "awaitChoice") {
		t.Fatal("the candidate loop puts a decision to the human directly")
	}
	if !before(body, "resolveArchitecture", "recordReconciliation") {
		t.Fatal("the architect resolved a disagreement without recording why the loop branched")
	}
}

// Property 12. A provider that cannot do the job costs a fallback, not a
// person's attention: being out of quota is not an architectural finding.
func TestAFailedReviewerFallsBackBeforeTheRunFails(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "resolveReview")
	if !strings.Contains(body, "current.Fallback(") {
		t.Fatal("resolveReview no longer walks its bounded alternates")
	}
	if strings.Contains(body, "awaitHuman") {
		t.Fatal("a reviewer failure interrupts a human")
	}
	// The one thing a fallback must not paper over: a verdict no provider may
	// give. Asking the next provider to review a candidate the first one was
	// structurally barred from reviewing would produce the same refusal again.
	if !strings.Contains(body, "errReviewRefused") {
		t.Fatal("an inadmissible verdict is retried on the next provider instead of refused")
	}
}

// Property 13. Agreement is irrelevant to certifiability: when Sensei cannot
// vouch for itself, the route is the same however many agents concurred.
func TestAgreementDoesNotSubstituteForCertification(t *testing.T) {
	uncertifiable := sensei.PreflightDecision{Status: sensei.PreflightOK}
	routing := routeAuthority(uncertifiable, nil)
	if routing.Route != RouteCannotEstablish {
		t.Fatalf("an uncertifiable graph produced route %q", routing.Route)
	}
	router := fileText(t, "internal/workflow/authority.go")
	for _, forbidden := range []string{"majority", "consensus", "agree(", "votes"} {
		if strings.Contains(strings.ToLower(router), forbidden) {
			t.Fatalf("the authority router consults %q; agents do not vote", forbidden)
		}
	}
}

// Property 11, at the point where the engine reads it. An absent risk verdict
// is the case that once read as permission.
func TestAnUnrecordedRiskReadingFailsClosed(t *testing.T) {
	e := &Engine{}
	p := e.policyFor("task-nobody-routed")
	if !p.CrossProviderReview {
		t.Fatalf("a task with no recorded risk reading was judged cheaply: %+v", p)
	}

	e.setPolicy("task-1", roles.PolicyFor("local", "none"))
	if got := e.policyFor("task-1"); got.CrossProviderReview {
		t.Fatal("a local change was forced through cross-provider review")
	}
}

// The reviewer is chosen from a read-only roster, not from the implementors.
// An implementor's argv carries write capability, and a reviewer able to fix
// what it is attacking can report it clean.
func TestReviewersAreChosenFromAReadOnlyRoster(t *testing.T) {
	e := &Engine{Config: config.Default()}
	caps := e.reviewCapabilities()
	if len(caps.For(roles.Reviewer)) < 2 {
		t.Fatalf("the default deployment has no bounded alternate reviewer: %+v", caps)
	}
	for _, a := range e.Config.ReviewRoster() {
		joined := strings.Join(a.Args, " ")
		if strings.Contains(joined, "workspace-write") || strings.Contains(joined, "bypassPermissions") {
			t.Fatalf("reviewer %s runs with write capability: %s", a.Name, joined)
		}
	}
	// And the implementer is excluded by construction rather than by prompt.
	assignment, err := roles.Assign(roles.Reviewer, caps, "codex")
	if err != nil {
		t.Fatalf("an alternate reviewer exists: %v", err)
	}
	if assignment.Provider == "codex" {
		t.Fatal("the implementing provider was assigned to review its own candidate")
	}
}

// The reviewer's packet binds to the exact revision it was built from, and
// carries no account of the work by its author.
func TestTheReviewPacketBindsToTheRevisionItDescribes(t *testing.T) {
	p := testReviewPacket(testContext(), "plan", "diff --git a b", "audit", "evidence")
	if p.Provenance.CandidateDigest == "" {
		t.Fatal("the packet is unbound, so a review of it could be carried onto another revision")
	}
	if p.Provenance.CandidateDigest != candidateRevision("diff --git a b") {
		t.Fatal("the packet is bound to something other than the diff it carries")
	}
	if p.Provenance.SessionMode != roles.Fresh {
		t.Fatal("the review packet does not declare an independent session")
	}
	// Two different candidates must not share an identity.
	if candidateRevision("a") == candidateRevision("b") {
		t.Fatal("two different candidates produced the same revision identity")
	}
}

// A handoff carries the position, not a paragraph about it.
func TestAHandoffCarriesTheUnansweredFindings(t *testing.T) {
	state := taskstate.State{TaskID: "task-1", Task: "add a flag", BaseSHA: "abc123", Phase: taskstate.Revising}
	state.OpenFindings([]taskstate.Finding{{Source: "reviewer", Detail: "the guard is unreachable"}})
	h := handoffPacket(state, roles.Binding{TaskID: "task-1", BaseSHA: "abc123"}, "claude", "build-1", 3, 3)
	if err := h.Continuity(roles.Binding{TaskID: "task-1", BaseSHA: "abc123"}); err != nil {
		t.Fatalf("this handoff continues the task: %v", err)
	}
	rendered := h.Render()
	for _, want := range []string{"the guard is unreachable", "task-1", "3 of 3 review cycles"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the handoff dropped %q", want)
		}
	}
}

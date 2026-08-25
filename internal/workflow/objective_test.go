package workflow

// The specimens from docs/work/value-objective-authority.md.
//
// A live self-improvement task asked sensei-code to consolidate `internal/`
// because it had "too much surface area". The architect accepted the premise,
// produced a consolidation plan, and emitted no question. Requested objective,
// technical premise and consequence judgment had been collapsed into one, and
// nothing kept them apart.

import (
	"strings"
	"testing"
)

// The consolidation plan, as it actually arrived.
func consolidationPlan() architectureDecision {
	return architectureDecision{
		Summary: "consolidate five internal packages to minimize package count",
		Plan:    "merge the packages under a single internal/core",
		Files:   []string{"internal/derived/derived.go", "internal/finding/finding.go"},
		Claims: []Claim{
			{Statement: "these five packages can merge without changing observable behavior",
				About: "internal/", Source: "inference"},
			{Statement: "internal/derived has one non-test consumer",
				About: "internal/derived", Source: "repository"},
		},
	}
}

func editAction(files ...string) Action {
	return Action{Stage: StageCandidateEdit, Files: files}
}

// 1. An interactive human objective is accepted as the objective — and
// establishes none of the technical premises needed to reach it.
func TestAHumanObjectiveDoesNotEstablishItsTechnicalPremises(t *testing.T) {
	d := consolidationPlan()
	s := StateAuthority(
		Objective{Text: "reduce internal surface area without changing observable behavior",
			Provenance: RequestedByHuman},
		d.Claims, AssessConsequences(editAction(d.Files...)), RouteCloseGap, d)

	if !s.ObjectiveEstablished {
		t.Fatal("a human typing /run did not establish the objective they typed")
	}
	// The inference stays an inference. It is the load-bearing premise of the
	// whole plan and the objective motivating it is not evidence for it.
	var merged TechnicalPremise
	for _, p := range s.Technical {
		if strings.Contains(p.Statement, "can merge without changing observable behavior") {
			merged = p
		}
	}
	if merged.Statement == "" {
		t.Fatal("the plan's load-bearing premise is not in the technical lane at all")
	}
	if merged.EvidenceBearing {
		t.Fatal("an inference became evidence because the human asked for the outcome it supports")
	}
	out := s.Render()
	if !strings.Contains(out, "NEEDS EVIDENCE") {
		t.Errorf("the statement does not say which premises still need evidence:\n%s", out)
	}
	if !strings.Contains(out, "the objective does not establish any of these") {
		t.Errorf("the statement does not separate the lanes:\n%s", out)
	}
}

// 2. The same wording, submitted unattended, is not the human's.
//
// This is the substitution the whole separation exists to refuse: identical
// text is not identical provenance.
func TestIdenticalWordingIsNotIdenticalAuthority(t *testing.T) {
	const text = "reduce internal surface area without changing observable behavior"
	d := consolidationPlan()
	assessment := AssessConsequences(editAction(d.Files...))

	human := StateAuthority(Objective{Text: text, Provenance: RequestedByHuman},
		d.Claims, assessment, RouteArchitectural, d)
	robot := StateAuthority(Objective{Text: text, Provenance: SubmittedUnattended},
		d.Claims, assessment, RouteArchitectural, d)

	if human.Objective.Text != robot.Objective.Text {
		t.Fatal("the specimen is wrong: the two objectives must be word-for-word identical")
	}
	if !human.ObjectiveEstablished {
		t.Error("an interactive human objective was not established")
	}
	if robot.ObjectiveEstablished {
		t.Error("an unattended submission was recorded as human value authority because the " +
			"wording matched one a human might have typed")
	}
	if out := robot.Render(); !strings.Contains(out, "NOT established as a human objective") ||
		!strings.Contains(out, "not a person's authorization") {
		t.Errorf("the unattended statement does not say what is missing:\n%s", out)
	}
	// The technical lane is identical either way. Who asked has no bearing on
	// what is true.
	if len(human.Technical) != len(robot.Technical) {
		t.Fatal("the technical lane changed with the objective's provenance")
	}
	for i := range human.Technical {
		if human.Technical[i].EvidenceBearing != robot.Technical[i].EvidenceBearing {
			t.Errorf("premise %d changed evidence status with who asked", i)
		}
	}
}

// 3. A bounded edit satisfying an authorized objective may proceed.
//
// The separation must not become a way of refusing ordinary work.
func TestAnAuthorizedObjectiveWithBoundedConsequencesProceeds(t *testing.T) {
	scoped := scopedPreflight(t, okBody(t, nil, "APPROVAL_GATE_NONE"))
	got := routeAuthorityForAction(scoped,
		[]Claim{{Statement: "internal/derived has one non-test consumer", About: "internal/derived", Source: "repository"}},
		editAction("internal/derived/derived.go"))
	if !got.Granted() {
		t.Fatalf("a bounded edit on evidence-backed premises did not proceed: %s (%s)", got.Route, got.Condition)
	}
}

// 4. An outward consequence still escalates, even when a human supplied the
// objective.
//
// A technical plan that satisfies the objective does not establish that its
// consequences are acceptable. Those are different questions with different
// owners, and the second is not answered by answering the first.
func TestAHumanObjectiveDoesNotClearAnOutwardConsequence(t *testing.T) {
	scoped := scopedPreflight(t, okBody(t, nil, "APPROVAL_GATE_NONE"))
	claims := []Claim{{Statement: "the release path is unused", About: "internal/publish", Source: "repository"}}

	for _, action := range []Action{
		{Stage: StagePublish, Files: []string{"internal/publish/publish.go"}},
		{Stage: StageCandidateEdit, Files: []string{"internal/publish/publish.go"},
			DeclaredSteps: []string{"merge the packages", "then deploy to production"}},
	} {
		got := routeAuthorityForAction(scoped, claims, action)
		if got.Route != RouteHuman {
			t.Errorf("stage %q routed to %s (%s); a human objective is not consequence authority",
				action.Stage, got.Route, got.Condition)
		}
	}

	// And the statement says so rather than implying the objective covered it.
	d := consolidationPlan()
	s := StateAuthority(Objective{Text: "reduce internal surface area", Provenance: RequestedByHuman},
		d.Claims, AssessConsequences(Action{Stage: StagePublish, Files: []string{"internal/publish/publish.go"}}),
		RouteHuman, d)
	if out := s.Render(); !strings.Contains(out, "not from who asked") {
		t.Errorf("the consequence lane does not state its independence:\n%s", out)
	}
}

// 5. A criterion the architect introduced stays the architect's.
//
// "minimize package count" was never asked for. It must not silently become the
// user's value by appearing in a plan the user's objective motivated.
func TestAnArchitectCriterionDoesNotBecomeTheUsersValue(t *testing.T) {
	d := consolidationPlan()

	// The human asked for the outcome, not for the criterion.
	s := StateAuthority(Objective{Text: "reduce internal surface area without changing observable behavior",
		Provenance: RequestedByHuman}, d.Claims, AssessConsequences(editAction(d.Files...)), RouteCloseGap, d)

	joined := strings.Join(s.ArchitectProposals, " | ")
	if !strings.Contains(joined, "minimize") {
		t.Fatalf("the architect's own optimization criterion was not attributed to it: %q", joined)
	}
	if out := s.Render(); !strings.Contains(out, "the request did not ask for") {
		t.Errorf("the statement does not separate proposal from request:\n%s", out)
	}

	// And a criterion the human DID ask for is theirs, not reported back at
	// them as the architect's invention.
	asked := StateAuthority(Objective{Text: "minimize the package count under internal/",
		Provenance: RequestedByHuman}, d.Claims, AssessConsequences(editAction(d.Files...)), RouteCloseGap, d)
	for _, p := range asked.ArchitectProposals {
		if strings.Contains(p, "minimize") {
			t.Errorf("a criterion the human asked for was attributed to the architect: %q", p)
		}
	}
}

// Provenance reaches the router's statement, rather than stopping at the mode
// event.
//
// It used to be announced and then dropped: run() passed `how` to
// announceMode and to nothing else, so no decision downstream could tell a task
// a human asked for from one an AI submitted.
func TestProvenanceSurvivesSubmission(t *testing.T) {
	e := &Engine{}
	e.recordObjective("task-1", Objective{Text: "audit the router", Provenance: RequestedByHuman})
	e.recordObjective("task-2", Objective{Text: "audit the router", Provenance: SubmittedUnattended})

	if !e.objective("task-1").HumanAuthorized() {
		t.Error("an interactive objective did not survive submission")
	}
	if e.objective("task-2").HumanAuthorized() {
		t.Error("an unattended objective acquired human authority")
	}
	// A task nothing recorded is unestablished, not absent. Reading a missing
	// objective as human-authorized is the failure mode; reading it as
	// unattended is the honest one.
	if e.objective("task-never-submitted").HumanAuthorized() {
		t.Error("an unrecorded task defaulted to human authority")
	}
}

// A resumed task does not invent human authority it cannot show.
//
// Known understatement, pinned so that fixing it is visible. The resume path
// re-announces the mode as ResumedGoverned without carrying the provenance the
// task was created with, so a task a human really did request comes back as
// unestablished. Understating what was established is the safe direction for
// this unknown; the alternative — assuming a resumed task was a human's —
// is exactly the RequestedByHuman defect in a different place.
func TestAResumedTaskDoesNotInventHumanAuthority(t *testing.T) {
	if (Objective{Provenance: ResumedGoverned}).HumanAuthorized() {
		t.Fatal("a resumed task claimed human authority the resume path cannot establish")
	}
	// And the two unattended lanes are unauthorized for the same reason.
	for _, p := range []Provenance{SubmittedUnattended, ObservationUnattended, DefaultEntry, ""} {
		if (Objective{Provenance: p}).HumanAuthorized() {
			t.Errorf("%q was read as human value authority", p)
		}
	}
}

// Nothing downstream can write the objective lane.
//
// The separation is a data-flow rule, not a warning: there is no path by which
// an architect's plan, a provider's wording, or a flag can reach the objective.
func TestTheObjectiveLaneHasNoDownstreamWriter(t *testing.T) {
	src := rawSource(t, "internal/workflow/objective.go")
	if strings.Contains(src, "HumanAuthorized() bool { return o.Provenance == RequestedByHuman }") == false {
		t.Fatal("human authority is no longer read from provenance alone")
	}
	for _, forbidden := range []string{
		"d.Summary == ", "SetProvenance", "AssertHuman", "objective.Provenance =",
	} {
		if strings.Contains(src, forbidden) {
			t.Errorf("objective.go contains %q; the objective must not be writable downstream", forbidden)
		}
	}
	// StateAuthority reads the plan only for what the ARCHITECT proposed. The
	// objective lane must be built from the objective.
	body := rawSource(t, "internal/workflow/objective.go")
	at := strings.Index(body, "func StateAuthority(")
	end := strings.Index(body[at:], "\n}\n")
	fn := body[at : at+end]
	if strings.Contains(fn, "ObjectiveEstablished: ") && !strings.Contains(fn, "ObjectiveEstablished: objective.HumanAuthorized()") {
		t.Error("the objective lane is established from something other than the objective")
	}
}

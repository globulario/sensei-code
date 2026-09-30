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
		d.Claims, AssessConsequences(editAction(d.Files...)), Routing{Route: RouteCloseGap}, d)

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
		d.Claims, assessment, Routing{Route: RouteArchitectural}, d)
	robot := StateAuthority(Objective{Text: text, Provenance: SubmittedUnattended},
		d.Claims, assessment, Routing{Route: RouteArchitectural}, d)

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
		Routing{Route: RouteHuman}, d)
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
		Provenance: RequestedByHuman}, d.Claims, AssessConsequences(editAction(d.Files...)), Routing{Route: RouteCloseGap}, d)

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
		Provenance: RequestedByHuman}, d.Claims, AssessConsequences(editAction(d.Files...)), Routing{Route: RouteCloseGap}, d)
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

// ---------------------------------------------------------------------------
// ONE OBJECTIVE IDENTITY ACROSS EVERY CONTINUATION.
//
// Engine.Resume's planned continuation recorded no objective, so in a restarted
// process its reviewer escalation reached the architect holding none: the
// binding carried an empty digest and a GitHub architect refused the turn --
// task-1790481145146367848's shape, on the path objective 35 did not touch.
// The witnesses below drive a real governed run to its first plan, block its
// implementer so the task stays planned and resumable, and then resume it.
// ---------------------------------------------------------------------------

// objectiveRunScript is the certifying Sensei stub the gap-loop witnesses use,
// with a passing diff audit, so a resumed candidate reaches its reviewer.
var objectiveRunScript = strings.Replace(gapLoopSenseiScript, "\t*)\n",
	"\t*'\"name\":\"awareness_audit_diff\"}}')\n"+
		"\t\tresult='{\"content\":[{\"type\":\"text\",\"text\":\"audit: pass\"}],\"structuredContent\":{\"schema\":\"sensei.audit.diff.v1\",\"decision\":\"pass\",\"availability\":\"available\"}}' ;;\n"+
		"\t*)\n", 1)

// objectivePlan is a plan the stub certifies: one file, no premise to verify.
const objectivePlan = `{"decision":"proceed","summary":"print a number","plan":"rewrite main.go so it prints a number","files":["main.go"],"mode":"modify"}`

// objectiveRoles serves each turn of a governed run and captures every
// architect RunnerSpec -- the binding exactly as it crosses into adapter
// selection. With no architect runner the capture refuses the turn, which is
// where a resumed run is stopped once its escalation has been observed.
type objectiveRoles struct {
	architect   *scriptedArchitect
	implementer *unavailableRunner
	reviewer    string
	bindings    chan RunnerSpec
}

func (r objectiveRoles) Resolve(spec RunnerSpec) (Resolved, error) {
	switch string(spec.Role) {
	case "architect":
		if r.architect == nil {
			return architectCapture{specs: r.bindings}.Resolve(spec)
		}
		select {
		case r.bindings <- spec:
		default:
		}
		return (&fixedResolver{runner: r.architect, name: "claude"}).Resolve(spec)
	case "implementer":
		if r.implementer != nil {
			return (&fixedResolver{runner: r.implementer, name: spec.Agent.Name}).Resolve(spec)
		}
	}
	// "unverified" is roles.Unverified: a reviewer context this project did not
	// open, exactly as the review-gate harness answers.
	return roleResolver{reviewer: answeringRunner{text: r.reviewer, mode: "unverified"}, name: "codex", session: "s1"}.Resolve(spec)
}

// objectiveEngine is one process over the shared repository and session record.
func objectiveEngine(t *testing.T, root string, roles objectiveRoles) (*Engine, *gapLoopRun) {
	t.Helper()
	requireGofmt(t)
	e, _, _ := blockedEngine(t, root, "s1")
	fresh, _, world := newGapLoopEngine(t, nil, e.Store, objectivePlan)
	e.Repo, e.Config = fresh.Repo, fresh.Config
	e.Config.Sensei.Args = []string{"-c", objectiveRunScript}
	e.Config.Permissions.WriteCandidates, e.Config.Permissions.CreateWorktrees = true, true
	e.Config.Permissions.RunFormatters, e.Config.Permissions.LocalCommit = true, true
	e.Config.Validation = formattingValidation()
	e.Config.Workflow.ReviewCycles = 1
	worker := e.Config.Architect
	worker.Command, worker.Args = stubProcess(t, "implementor", "")
	e.Config.Implementors = append(e.Config.Implementors[:0], worker)
	e.Config.Reviewer = e.Config.Architect
	e.Config.Reviewer.Name = "codex"
	e.Runners = roles
	return e, &gapLoopRun{engine: e, world: world}
}

// objectiveRun submits objective in a first process and follows it until its
// implementer is blocked with the plan recorded, returning the digest the fresh
// run's own architect turn was bound to.
func objectiveRun(t *testing.T, root, taskID, objective string, how Provenance) (*Engine, string) {
	t.Helper()
	roles := objectiveRoles{architect: &scriptedArchitect{turns: []architectTurn{{text: objectivePlan}}},
		implementer: &unavailableRunner{cause: quota()}, bindings: make(chan RunnerSpec, 8)}
	e, _ := objectiveEngine(t, root, roles)
	events, cancel := e.Bus.Subscribe(4096)
	defer cancel()
	go e.run(t.Context(), taskID, objective, how)
	seen := settleResume(t, events)
	if last := string(seen[len(seen)-1].Kind); last != "workflow.blocked_external" {
		t.Fatalf("the first run did not stop planned with its implementer blocked (%s): %v", last, kinds(seen))
	}
	select {
	case spec := <-roles.bindings:
		return e, spec.Architecture.ObjectiveDigest
	default:
		t.Fatal("the first run took no architect turn")
		return nil, ""
	}
}

// resumeToEscalation resumes the planned task in e and returns the architect
// request its reviewer escalation produced.
func resumeToEscalation(t *testing.T, e *Engine, root, taskID string, bindings chan RunnerSpec) RunnerSpec {
	t.Helper()
	found := reopen(t, root, "s1")
	if len(found) != 1 || found[0].TaskID != taskID || !found[0].Planned {
		t.Fatalf("the blocked task is not resumable as a planned task: %+v", found)
	}
	events, cancel := e.Bus.Subscribe(4096)
	defer cancel()
	if got := e.Resume(t.Context(), found[0]); got != taskID {
		t.Fatalf("Resume continued %q instead of the task it was given", got)
	}
	seen := settleResume(t, events)
	// The reviewer's escalation is the only architect-reaching turn after the
	// resumed candidate is reviewed, and the run ends on the capture's refusal
	// of that request.
	escalated, refused := false, false
	for _, ev := range seen {
		if string(ev.Kind) == "review.completed" && strings.HasPrefix(ev.Summary, "ESCALATE:") {
			escalated = true
		}
		if escalated && strings.Contains(ev.Summary, "the witness captured the architect request") {
			refused = true
		}
	}
	escalated = escalated && refused
	select {
	case spec := <-bindings:
		if !escalated {
			t.Fatalf("the architect was asked, but not by the reviewer's escalation: %v", kinds(seen))
		}
		return spec
	default:
		trace := ""
		for _, ev := range seen {
			trace += string(ev.Kind) + ": " + ev.Summary + "\n"
		}
		t.Fatalf("the resumed run never reached an architect turn:\n%s", trace)
		return RunnerSpec{}
	}
}

// W1 -- a task created in one process and resumed through Engine.Resume in a
// FRESH one reaches its reviewer-escalation architect turn bound to the same
// objective identity the fresh run was bound to: the SHA-256 of the exact
// submitted bytes, recovered from the durable TaskCreated record.
func TestAResumedPlannedTaskEscalatesWithTheSubmittedObjectiveIdentity(t *testing.T) {
	const (
		taskID = "task-w1-objective"
		// Surrounding whitespace is part of the objective and of its identity.
		objective = "  make main print a number\n"
		// sha256 of objective, computed independently of the code under test.
		wantDigest = "6e491097a39e52bbe40361af49809eeaa86687596e4a066a10ef451c91d00099"
	)
	root := t.TempDir()
	first, freshDigest := objectiveRun(t, root, taskID, objective, RequestedByHuman)
	if freshDigest != wantDigest {
		t.Fatalf("the fresh run's architect turn is not bound to the submitted bytes: %s", freshDigest)
	}

	// A restarted process: a new engine over the same record, holding nothing.
	bindings := make(chan RunnerSpec, 8)
	restarted, _ := objectiveEngine(t, root, objectiveRoles{
		reviewer: `{"decision":"escalate","summary":"the plan needs an architectural answer"}`, bindings: bindings})
	restarted.Repo = first.Repo
	if len(restarted.objectives) != 0 {
		t.Fatal("the restarted engine already holds an objective, so recovery would not be proven")
	}
	spec := resumeToEscalation(t, restarted, root, taskID, bindings)
	if spec.TaskID != taskID || spec.Architecture.TaskID != taskID {
		t.Fatalf("the escalation names another task: %+v", spec.Architecture)
	}
	if got := spec.Architecture.ObjectiveDigest; got != freshDigest || got != wantDigest {
		t.Fatalf("the resumed escalation is bound to objective %q; the fresh run was bound to %q", got, freshDigest)
	}
	// Recovered, and not raised: a restarted process cannot show who asked.
	if o := restarted.objective(taskID); o.Text != objective || o.Provenance != ResumedGoverned || o.HumanAuthorized() {
		t.Fatalf("the recovered objective is not the recorded bytes under the resumption's provenance: %+v", o)
	}
}

// W5 -- CONTROL. In the process that took the submission, a human objective
// survives Resume unchanged: the accessor reads the submission first and never
// demotes it to the resumption's provenance.
func TestAHumanObjectiveSurvivesResumeInTheSameProcess(t *testing.T) {
	const (
		taskID    = "task-w5-objective"
		objective = "  make main print a number\n"
	)
	root := t.TempDir()
	e, freshDigest := objectiveRun(t, root, taskID, objective, RequestedByHuman)
	bindings := make(chan RunnerSpec, 8)
	e.Runners = objectiveRoles{
		reviewer: `{"decision":"escalate","summary":"the plan needs an architectural answer"}`, bindings: bindings}
	spec := resumeToEscalation(t, e, root, taskID, bindings)
	if spec.Architecture.ObjectiveDigest != freshDigest {
		t.Fatalf("the same-process resume changed the objective identity: %q, want %q",
			spec.Architecture.ObjectiveDigest, freshDigest)
	}
	if o := e.objective(taskID); o.Text != objective || o.Provenance != RequestedByHuman || !o.HumanAuthorized() {
		t.Fatalf("the human objective was demoted by Resume: %+v", o)
	}
}

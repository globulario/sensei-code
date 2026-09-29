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

// resumeEscalationResolver serves the roles of the resume witnesses below. The
// architect answers from a script (or, with none, proves itself unavailable);
// every architect RunnerSpec is kept exactly as it crosses into adapter
// selection. The implementer is either blocked by a provider refusal -- which
// leaves a planned task preserved and resumable -- or served by its configured
// command, and the reviewer returns a fixed verdict.
type resumeEscalationResolver struct {
	architect        *scriptedArchitect
	reviewer         answeringRunner
	specs            chan RunnerSpec
	blockImplementer bool
}

func (r *resumeEscalationResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	switch spec.Role {
	case "architect":
		select {
		case r.specs <- spec:
		default:
		}
		if r.architect == nil {
			return Resolved{}, &RoleUnavailable{Role: spec.Role, Provider: "claude", Cause: quota()}
		}
		return Resolved{Runner: r.architect, Name: "claude", Label: "claude"}, nil
	case "reviewer":
		return Resolved{Runner: r.reviewer, Name: "remote:abc", Label: "remote:abc"}, nil
	case "implementer":
		if r.blockImplementer {
			return Resolved{}, &RoleUnavailable{Role: spec.Role, Provider: spec.Agent.Name, Cause: quota()}
		}
	}
	return CLIResolved(spec, "s1"), nil
}

// resumeEscalationObjective carries leading and trailing whitespace so an
// identity that trimmed or normalized anywhere would name different bytes.
const (
	resumeEscalationObjective = "\t  resume reads the recorded objective  \n"
	// sha256 of resumeEscalationObjective, byte for byte, computed outside the
	// code under test. internal/ghwebhook pins DigestObjective to the same value.
	resumeEscalationDigest = "d60db1d22ca040881a6fec3d731ae42a0ecdad7c0c941a6885ac188ff47ea6f2"
	resumeEscalationTask   = "task-resume-escalation"
)

// resumeEscalation creates a governed task in one engine and drives it to a
// planned, preserved state (its only implementer is blocked by its provider),
// then resumes it through Engine.Resume -- in a FRESH engine over the same
// durable session record when fresh is set, in the creating engine otherwise --
// until the reviewer escalates and the implementation loop asks the architect.
// It returns the architect bindings of the fresh run and of the resumed
// reviewer-escalation turn, and the engine that resumed.
func resumeEscalation(t *testing.T, how Provenance, fresh bool) (created, escalated RunnerSpec, resumed *Engine) {
	t.Helper()
	requireGofmt(t)
	repo, _ := mintRepo(t)
	record := t.TempDir()
	implCommand, implArgs := stubProcess(t, "implementor", "")
	auditCommand, auditArgs := stubProcess(t, "sensei", "")

	// The certifying Sensei of the gap-loop witnesses, with the diff audit
	// answered by the candidate loop's stub Sensei, one frame per call.
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
	auditor := quote(auditCommand)
	for _, a := range auditArgs {
		auditor += " " + quote(a)
	}
	script := strings.Replace(gapLoopSenseiScript, "\t*)\n",
		"\t*'\"name\":\"awareness_audit_diff\"'*)\n"+
			"\t\tprintf 'Content-Length: %d\\r\\n\\r\\n%s' \"${#body}\" \"$body\" | "+auditor+"\n"+
			"\t\tcontinue ;;\n\t*)\n", 1)
	if script == gapLoopSenseiScript {
		t.Fatal("the audit answer was not added to the stub Sensei, so no candidate could be reviewed")
	}

	configure := func(e *Engine, r *resumeEscalationResolver) {
		e.Repo = repo
		e.pending = map[string]chan string{}
		e.Runners = r
		e.Config.Permissions.ReadRepository = true
		e.Config.Permissions.WriteCandidates = true
		e.Config.Permissions.CreateWorktrees = true
		e.Config.Permissions.RunFormatters = true
		e.Config.Permissions.LocalCommit = true
		e.Config.Workflow.ReviewCycles = 1
		e.Config.Validation = formattingValidation()
		worker := e.Config.Architect
		worker.Name, worker.Command, worker.Args, worker.Graph = "claude", implCommand, implArgs, "none"
		e.Config.Implementors = append(e.Config.Implementors[:0], worker)
		e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "claude", "true", "none"
		e.Config.Architects = nil
		e.Config.Reviewer.Name, e.Config.Reviewer.Command, e.Config.Reviewer.Graph = "codex", "true", "none"
		e.Config.Sensei.Command = "sh"
		e.Config.Sensei.Args = []string{"-c", script}
		e.Config.Sensei.Repository = "globulario/sensei"
	}
	const plan = `{"decision":"proceed","summary":"edit main","plan":"edit main.go","files":["main.go"],"mode":"modify"}`
	await := func(events <-chan string, what string, done func(string) bool) {
		t.Helper()
		var seen []string
		for ev := range events {
			seen = append(seen, ev)
			if done(ev) {
				return
			}
			for _, end := range []string{"workflow.failed: ", "workflow.completed: ", "workflow.stopped: ", "workflow.awaiting_authority: ",
				"workflow.not_converged: ", "workflow.dirty_canonical_refused: ", "workflow.base_moved_refused: ", "workflow.restoration_refused: "} {
				if strings.HasPrefix(ev, end) {
					t.Fatalf("the run ended before %s:\n%s", what, strings.Join(seen, "\n"))
				}
			}
		}
	}
	stream := func(e *Engine) <-chan string {
		ch, cancel := e.Bus.Subscribe(4096)
		t.Cleanup(cancel)
		out := make(chan string, 4096)
		go func() {
			defer close(out)
			for ev := range ch {
				out <- string(ev.Kind) + ": " + ev.Summary
			}
		}()
		return out
	}

	creator, _, _ := blockedEngine(t, record, "s1")
	createdBy := &resumeEscalationResolver{
		architect: &scriptedArchitect{turns: []architectTurn{{text: plan}, {text: plan}, {text: plan}, {text: plan}}},
		specs:     make(chan RunnerSpec, 8), blockImplementer: true,
	}
	configure(creator, createdBy)
	creatorEvents := stream(creator)
	go creator.run(t.Context(), resumeEscalationTask, resumeEscalationObjective, how)
	await(creatorEvents, "the planned task was preserved", func(ev string) bool {
		return strings.HasPrefix(ev, "workflow.blocked_external: ")
	})
	select {
	case created = <-createdBy.specs:
	default:
		t.Fatal("the fresh run never asked its architect")
	}

	var task []int
	found := reopen(t, record, "s1")
	for i := range found {
		if found[i].TaskID == resumeEscalationTask {
			task = append(task, i)
		}
	}
	if len(task) != 1 || !found[task[0]].Planned {
		t.Fatalf("the preserved task was not found planned after the run: %+v", found)
	}

	resumed = creator
	if fresh {
		resumed, _, _ = blockedEngine(t, record, "s1")
		if len(resumed.objectives) != 0 {
			t.Fatalf("the fresh engine was handed an objective in memory: %+v", resumed.objectives)
		}
	}
	resumedBy := &resumeEscalationResolver{
		reviewer: answeringRunner{text: `{"decision":"escalate","summary":"main cannot hold this change as planned"}`, mode: "fresh"},
		specs:    make(chan RunnerSpec, 8),
	}
	configure(resumed, resumedBy)
	resumedEvents := stream(resumed)
	resumed.Resume(t.Context(), found[task[0]])
	reviewerEscalated := false
	await(resumedEvents, "the reviewer escalated to the architect", func(ev string) bool {
		if strings.HasPrefix(ev, "review.completed: ") && strings.Contains(ev, "main cannot hold this change as planned") {
			reviewerEscalated = true
		}
		select {
		case escalated = <-resumedBy.specs:
			return true
		default:
			return false
		}
	})
	if !reviewerEscalated {
		t.Fatal("the resumed architect turn was not the reviewer's escalation")
	}
	return created, escalated, resumed
}

// W1 -- PATH INDEPENDENCE ACROSS A RESTART. A task created in one engine and
// resumed through Engine.Resume in a fresh engine, over the same durable
// session record, reaches the reviewer-escalation architect turn bound to the
// same objective identity the fresh run bound: the SHA-256 of the exact bytes
// that were submitted. The restarted process holds nothing in memory, so the
// identity can only have come from the recorded TaskCreated -- and what it
// recovers establishes no human.
func TestAResumedTaskReachesTheReviewerEscalationWithItsRecordedObjective(t *testing.T) {
	created, escalated, resumed := resumeEscalation(t, SubmittedUnattended, true)

	if created.Architecture.ObjectiveDigest != resumeEscalationDigest {
		t.Fatalf("the fresh run did not bind the exact submitted bytes: %q", created.Architecture.ObjectiveDigest)
	}
	if escalated.TaskID != resumeEscalationTask || escalated.Architecture.TaskID != resumeEscalationTask {
		t.Fatalf("the resumed architect turn names another task: %+v", escalated)
	}
	if escalated.Architecture.ObjectiveDigest != created.Architecture.ObjectiveDigest {
		t.Fatalf("the resumed reviewer escalation carries objective identity %q, the fresh run %q",
			escalated.Architecture.ObjectiveDigest, created.Architecture.ObjectiveDigest)
	}
	if o := resumed.objective(resumeEscalationTask); o.Text != resumeEscalationObjective || o.Provenance != ResumedGoverned {
		t.Fatalf("the restarted process did not recover the recorded objective conservatively: %+v", o)
	}
}

// W5 -- CONTROL. In the process that holds it, a human-provenance objective
// survives Resume unchanged: the accessor prefers the submission's own record
// and does not demote it to the resumption's provenance.
func TestAHumanObjectiveSurvivesResumeInTheProcessThatHoldsIt(t *testing.T) {
	created, escalated, resumed := resumeEscalation(t, RequestedByHuman, false)

	if escalated.Architecture.ObjectiveDigest != created.Architecture.ObjectiveDigest ||
		escalated.Architecture.ObjectiveDigest != resumeEscalationDigest {
		t.Fatalf("the same-process resume bound %q, the fresh run %q",
			escalated.Architecture.ObjectiveDigest, created.Architecture.ObjectiveDigest)
	}
	o := resumed.objective(resumeEscalationTask)
	if o.Text != resumeEscalationObjective || o.Provenance != RequestedByHuman || !o.HumanAuthorized() {
		t.Fatalf("the resume demoted or rewrote the objective a human asked for: %+v", o)
	}
}

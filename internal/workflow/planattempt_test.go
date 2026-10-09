package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// Objective 64: CANONICAL PLAN-ATTEMPT IDENTITY. Every operative plan has one
// canonical durable identity, all plan-local authority binds to it, a re-plan
// supersedes the previous attempt whole, and a resume reconstructs exactly one
// operative attempt without deriving or minting another.

const attemptObjective = "the objective"

// fixturePlanAttemptID is the canonical identity of an architect plan with this
// text, for fixtures that need a real PlanAttemptID rather than prose.
func fixturePlanAttemptID(t *testing.T, taskID, plan string) string {
	t.Helper()
	id, err := planAttemptID(taskID, attemptObjective, "", PlanByArchitect, "", architectureDecision{Decision: "proceed", Plan: plan})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// attemptEngine is an engine with a durable session and no recipes, so routing's
// coverage read derives nothing and records the explicit empty grant set.
func attemptEngine(t *testing.T) (*Engine, *session.Store) {
	t.Helper()
	store := sessionStore(t)
	e := &Engine{Store: store, SessionID: "s1", Bus: event.NewBus()}
	e.Repo.Root = t.TempDir()
	return e, store
}

func attemptPlan(text string, files ...string) architectureDecision {
	return architectureDecision{Decision: "proceed", Summary: "s", Plan: text, Files: files}
}

// routeWithTestEdits records grants for d's attempt exactly as routing does:
// the attempt begins first, and the grant record carries its identity.
func routeWithTestEdits(t *testing.T, e *Engine, taskID string, d architectureDecision, grants []testEditGrant) planAttempt {
	t.Helper()
	// FIXTURE MIGRATION (70B2a1, RULING-190): routed on the task's durable root.
	rootFixtureTaskOnce(t, e, taskID, attemptObjective)
	a, err := e.beginPlanAttempt(fixtureCtx(e, taskID), taskID, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.recordTestEditGrants(fixtureCtx(e, taskID), taskID, "recorded", testEditRecord{PlanAttemptID: a.ID, World: teWorld, Grants: grants}); err != nil {
		t.Fatal(err)
	}
	e.setTestEditGrants(taskID, grants)
	if err := e.recordProspectiveGrants(fixtureCtx(e, taskID), taskID, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: teWorld}); err != nil {
		t.Fatal(err)
	}
	e.setProspectiveGrants(taskID, nil)
	return a
}

// routeWithDerivation routes d's grant state through the production recorder.
func routeWithDerivation(t *testing.T, e *Engine, taskID string, d architectureDecision) planAttempt {
	t.Helper()
	// FIXTURE MIGRATION (70B2a1, RULING-190): routed on the task's durable root.
	rootFixtureTaskOnce(t, e, taskID, attemptObjective)
	a, err := e.beginPlanAttempt(fixtureCtx(e, taskID), taskID, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	e.derivedCoverage(fixtureCtx(e, taskID), taskID, d.Files, d.ProspectiveSurfaces)
	return a
}

// interruptedFrom is the task as a restart finds it: the durable record, with
// the process gone.
func interruptedFrom(t *testing.T, store *session.Store, taskID string) session.Interrupted {
	t.Helper()
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	history = append([]event.Event{event.New("s1", taskID, event.SourceUser, event.TaskCreated, attemptObjective, nil)}, history...)
	for _, task := range session.FindInterrupted(history) {
		if task.TaskID == taskID {
			return task
		}
	}
	t.Fatalf("task %s was not reconstructed", taskID)
	return session.Interrupted{}
}

// restoreFresh restores a task's operative attempt and test-edit grants in a
// fresh engine, through the functions Resume calls and in Resume's order.
//
// The attempt identity is re-derived at the attempt's own world (this rig pins
// none); world is where the grant records were read.
func restoreFresh(t *testing.T, task session.Interrupted, recomputed []testEditGrant, planned []string, world string) (*Engine, error) {
	t.Helper()
	fresh := &Engine{}
	if err := fresh.restorePlanAttempt(task, ""); err != nil {
		return fresh, err
	}
	if err := fresh.restoreTestEditGrants(task, recomputed, planned, world); err != nil {
		return fresh, err
	}
	return fresh, fresh.restoreProspectiveGrants(task, nil, world)
}

// sourceOf is the source text of one function, as written.
func sourceOf(t *testing.T, file, fn string) string {
	t.Helper()
	src := rawSource(t, file)
	at := strings.Index(src, ") "+fn+"(")
	if at < 0 {
		at = strings.Index(src, "func "+fn+"(")
	}
	if at < 0 {
		t.Fatalf("%s not found in %s", fn, file)
	}
	end := strings.Index(src[at+1:], "\nfunc ")
	if end < 0 {
		return src[at:]
	}
	return src[at : at+1+end]
}

func refusedForPlanAttempt(err error) bool {
	r, ok := err.(*RestorationRefusal)
	return ok && r.Binding == RestorationPlanAttemptUnbound
}

// W1: an initial plan A records PlanAttemptID A and grants bound to A; restart
// reconstructs A exactly.
func TestW1AnInitialPlanAttemptAndItsGrantsSurviveRestartExactly(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-w1"
	planned := []string{teS, teF}
	grants := teEditGrants(t)
	a := routeWithTestEdits(t, e, task, attemptPlan("plan A", planned...), grants)
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, task), task, attemptObjective, attemptPlan("plan A", planned...)); err != nil {
		t.Fatal(err)
	}

	restored := interruptedFrom(t, store, task)
	if restored.PlanAttemptID != a.ID || len(restored.TestEditRecord) == 0 {
		t.Fatalf("the restart did not reconstruct attempt A with its grants: %q, record %d bytes", restored.PlanAttemptID, len(restored.TestEditRecord))
	}
	fresh, err := restoreFresh(t, restored, grants, planned, teWorld)
	if err != nil {
		t.Fatalf("attempt A and its own grants were refused: %v", err)
	}
	if got := fresh.operativePlanAttempt(task); got.ID != a.ID || got.Plan.Plan != "plan A" {
		t.Fatalf("the restart reconstructed another attempt: %+v", got)
	}
	if len(fresh.testEditGrants(task)) != 1 {
		t.Fatal("A's grants were not restored with A")
	}
}

// ---------------------------------------------------------------------------
// PRODUCTION FIXTURES. The witnesses below drive the real governed run: a
// scripted architect, a Sensei stub that certifies the region, a sensei
// stand-in that DERIVES coverage, and a repository whose pinned base holds an
// existing test beside a covered file. Every grant they observe is one the
// production coverage path derived and recorded; none is written by the test.
// ---------------------------------------------------------------------------

// derivingSensei is confinementRepo's stand-in: every derivation covers main.go
// at exactly the world it is asked about.
const derivingSensei = "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -revision ] && rev=\"$2\"; shift; done\n" +
	"printf '{\"result\":\"DERIVED\",\"pinned_commit\":\"%s\",\"subjects\":[{\"file\":\"main.go\"}]}' \"$rev\"\n"

// grantWorld turns e's repository into one whose pinned base also holds an
// existing main_test.go beside main.go, with an owned recipe an EARLIER task
// recorded (a task is never covered by its own question) and the deriving
// stand-in -- so routing a plan naming main_test.go derives a real
// existing-test edit grant, and one declaring extra_test.go a real prospective
// grant. It returns the new pinned world.
func grantWorld(t *testing.T, e *Engine) string {
	t.Helper()
	root := e.Repo.Root
	world := commitFixtureFile(t, root, "main_test.go", "package main\n\nimport \"testing\"\n\nfunc TestMain1(t *testing.T) {}\n")
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte("/.sensei-code/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "sensei")
	if err := os.WriteFile(bin, []byte(derivingSensei), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSEI_BIN", bin)
	e.recordClosureQuestion("task-writer", "coverage gap", closureDecision(t), certifiedStart{}, "model", 1, fixtureCtx(e, "task-writer"))
	return world
}

// regionScript is a Sensei stub that answers the region preflight over exactly
// region with counts that describe it, and every other preflight -- the
// per-file probes -- as the one-file answer base already gives.
func regionScript(base string, region ...string) string {
	const caseLine = "\t*'\"name\":\"awareness_preflight\"}}')\n"
	at := strings.Index(base, caseLine)
	end := at + len(caseLine) + strings.Index(base[at+len(caseLine):], " ;;\n") + len(" ;;\n")
	single := base[at:end]
	n := fmt.Sprint(len(region))
	multi := strings.Replace(single, caseLine, "\t*'\""+strings.Join(region, "\",\"")+"\"]'*'\"name\":\"awareness_preflight\"}}')\n", 1)
	multi = strings.Replace(multi, `"file_count":1,"indexed_file_count":1`, `"file_count":`+n+`,"indexed_file_count":`+n, 1)
	return base[:at] + multi + base[at:]
}

const (
	// replanA edits main.go and the existing test beside it, so routing derives
	// an existing-test edit grant for it.
	replanA = `{"decision":"proceed","summary":"edit main","plan":"plan A: edit main.go and its test","files":["main.go","main_test.go"],"mode":"modify",` +
		`"test_edits":[{"path":"main_test.go","operation":"edit","package":"main","build_constraints":[],"imports":["testing"]}]}`
	// replanSamePaths is a different plan over exactly A's paths.
	replanSamePaths = `{"decision":"proceed","summary":"edit main","plan":"plan B: the same files, a different plan","files":["main.go","main_test.go"],"mode":"modify",` +
		`"test_edits":[{"path":"main_test.go","operation":"edit","package":"main","build_constraints":[],"imports":["testing"]}]}`
	// replanNoGrants edits main.go only, so routing derives no grant at all.
	replanNoGrants     = `{"decision":"proceed","summary":"edit main","plan":"plan B: edit main.go only","files":["main.go"],"mode":"modify"}`
	escalatingReviewer = `{"decision":"escalate","summary":"the plan needs an architectural answer"}`
)

// replanRun drives one governed run whose reviewer escalates the first
// candidate, so the architect re-plans IN CYCLE (runCandidate's escalation
// path) from initial to revised, and returns the engine, its events and the
// pinned world. One review cycle: the run ends NOT_CONVERGED under the
// re-plan, resumable.
func replanRun(t *testing.T, store *session.Store, taskID, initial, revised string) (*Engine, []event.Event, string) {
	t.Helper()
	requireGofmt(t)
	e, _, _ := newGapLoopEngine(t, nil, store, initial)
	world := grantWorld(t, e)
	e.Config.Sensei.Args = []string{"-c", regionScript(objectiveRunScript, "main.go", "main_test.go")}
	e.Config.Permissions.WriteCandidates, e.Config.Permissions.CreateWorktrees = true, true
	e.Config.Permissions.RunFormatters, e.Config.Permissions.LocalCommit = true, true
	e.Config.Validation = formattingValidation()
	e.Config.Workflow.ReviewCycles = 1
	worker := e.Config.Architect
	worker.Command, worker.Args = stubProcess(t, "implementor", "")
	e.Config.Implementors = []config.Agent{worker}
	e.Config.Reviewer = e.Config.Architect
	e.Config.Reviewer.Name = "codex"
	e.Runners = objectiveRoles{architect: &scriptedArchitect{turns: []architectTurn{{text: initial}, {text: revised}}},
		reviewer: escalatingReviewer, bindings: make(chan RunnerSpec, 8)}
	events, cancel := e.Bus.Subscribe(4096)
	defer cancel()
	go e.run(t.Context(), taskID, attemptObjective, RequestedByHuman)
	seen := settleResume(t, events)
	if last := seen[len(seen)-1].Kind; last != event.WorkflowNotConverged {
		t.Fatalf("the run did not end NOT_CONVERGED under its re-plan (%s):\n%s", last, gapLoopTrace(seen))
	}
	return e, seen, world
}

// startedAttempts are the PlanAttemptIDs a run started, in order.
func startedAttempts(t *testing.T, evs []event.Event) []string {
	t.Helper()
	var out []string
	for _, ev := range evs {
		if ev.Kind == event.PlanAttemptStarted {
			var a planAttempt
			if err := json.Unmarshal(ev.Payload, &a); err != nil {
				t.Fatal(err)
			}
			out = append(out, a.ID)
		}
	}
	return out
}

// resumeFresh is a process restart: a new engine over the same repository and
// session record, holding nothing, resumes the interrupted task through
// Engine.Resume. Its architect refuses the owed re-plan, so the continuation
// ends right after restoration; what matters is what restoration accepted.
func resumeFresh(t *testing.T, prior *Engine, store *session.Store, task session.Interrupted) (*Engine, []event.Event) {
	t.Helper()
	e, _, _ := newGapLoopEngine(t, &gapLoopRun{engine: prior}, store, replanA)
	e.Config = prior.Config
	e.Runners = objectiveRoles{reviewer: escalatingReviewer, bindings: make(chan RunnerSpec, 8)}
	events, cancel := e.Bus.Subscribe(4096)
	defer cancel()
	if got := e.Resume(t.Context(), task); got != task.TaskID {
		t.Fatalf("Resume continued %q", got)
	}
	var seen []event.Event
	deadline := time.After(30 * time.Second)
	for {
		select {
		case ev := <-events:
			seen = append(seen, ev)
			switch ev.Kind {
			case event.WorkflowFailed, event.WorkflowRestorationRefused, event.WorkflowNotConverged, event.WorkflowCompleted:
				return e, seen
			}
		case <-deadline:
			t.Fatalf("the resumed invocation did not settle:\n%s", gapLoopTrace(seen))
		}
	}
}

func reconstructed(t *testing.T, store *session.Store, taskID string) session.Interrupted {
	t.Helper()
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range session.FindInterrupted(history) {
		if task.TaskID == taskID {
			return task
		}
	}
	t.Fatalf("task %s is not interrupted in the record", taskID)
	return session.Interrupted{}
}

// restoredPastRestoration reports that a resumed continuation got past every
// restoration check to the owed re-plan, which Resume reaches only after the
// operative attempt and its grants were verified and installed.
func restoredPastRestoration(evs []event.Event) bool {
	for _, ev := range evs {
		if ev.Kind == event.WorkflowRestorationRefused {
			return false
		}
		if strings.Contains(ev.Summary, "resuming the same task at the architect re-plan it is owed") {
			return true
		}
	}
	return false
}

// W2: an in-cycle re-plan A -> B, driven through runCandidate's escalation
// path, durably records B as operative through the canonical transition and
// replaces A's complete grant state; a restart reconstructs and restores B
// only.
func TestW2AReplanReplacesTheAttemptAndItsWholeGrantState(t *testing.T) {
	const task = "task-w2"
	store := sessionStore(t)
	e, seen, _ := replanRun(t, store, task, replanA, replanSamePaths)
	attempts := startedAttempts(t, seen)
	if len(attempts) != 2 || attempts[0] == attempts[1] {
		t.Fatalf("the run did not route two distinct attempts: %v", attempts)
	}
	a, b := attempts[0], attempts[1]
	// The re-plan is a PlanProposed bound to B -- not a status line.
	var proposed []string
	for _, ev := range seen {
		if ev.Kind == event.PlanProposed {
			var p proposedPlan
			_ = json.Unmarshal(ev.Payload, &p)
			proposed = append(proposed, p.PlanAttemptID)
		}
	}
	if len(proposed) != 2 || proposed[0] != a || proposed[1] != b {
		t.Fatalf("the operative transitions are not A then B: %v", proposed)
	}
	if e.operativePlanAttempt(task).ID != b {
		t.Fatal("the re-plan is not operative in the running engine")
	}

	restored := reconstructed(t, store, task)
	var rec testEditRecord
	if restored.PlanAttemptID != b || json.Unmarshal(restored.TestEditRecord, &rec) != nil || rec.PlanAttemptID != b || len(rec.Grants) != 1 {
		t.Fatalf("the restart does not hold B with B's own grant state: attempt %q record %s", restored.PlanAttemptID, restored.TestEditRecord)
	}
	fresh, evs := resumeFresh(t, e, store, restored)
	if !restoredPastRestoration(evs) {
		t.Fatalf("B and its own grants were not restored:\n%s", gapLoopTrace(evs))
	}
	if got := fresh.operativePlanAttempt(task); got.ID != b || got.Plan.Plan != "plan B: the same files, a different plan" {
		t.Fatalf("the restart reconstructed another attempt: %+v", got)
	}
	if g := fresh.testEditGrants(task); len(g) != 1 || g[0].Path != "main_test.go" {
		t.Fatalf("B's grant state was not restored: %+v", g)
	}

	// A's record handed to B is refused by its binding, though the paths match.
	forged := restored
	for _, ev := range seen {
		if ev.Kind == event.TestEditGranted {
			var r testEditRecord
			_ = json.Unmarshal(ev.Payload, &r)
			if r.PlanAttemptID == a {
				forged.TestEditRecord = ev.Payload
			}
		}
	}
	_, evs = resumeFresh(t, e, store, forged)
	if restoredPastRestoration(evs) || !hasKind(kindsOf(evs), event.WorkflowRestorationRefused) {
		t.Fatalf("A's grant record authorized B because their paths are equal:\n%s", gapLoopTrace(evs))
	}
}

// W3: A has grants, the in-cycle re-plan B derives none: B's explicit empty
// grant state is recorded, a restart restores B with no grant, and none of
// A's returns -- in the record, in the restored engine, or by absence.
func TestW3AZeroGrantReplanRecordsAnExplicitEmptySetAndNoOldGrantReturns(t *testing.T) {
	const task = "task-w3"
	store := sessionStore(t)
	e, seen, _ := replanRun(t, store, task, replanA, replanNoGrants)
	attempts := startedAttempts(t, seen)
	if len(attempts) != 2 {
		t.Fatalf("the run did not route two attempts: %v", attempts)
	}
	b := attempts[1]
	restored := reconstructed(t, store, task)
	var rec testEditRecord
	if restored.PlanAttemptID != b || json.Unmarshal(restored.TestEditRecord, &rec) != nil || rec.PlanAttemptID != b || len(rec.Grants) != 0 {
		t.Fatalf("the restart does not hold B's explicit empty grant state: attempt %q record %s", restored.PlanAttemptID, restored.TestEditRecord)
	}
	fresh, evs := resumeFresh(t, e, store, restored)
	if !restoredPastRestoration(evs) {
		t.Fatalf("B with its explicit empty grant state was not restored:\n%s", gapLoopTrace(evs))
	}
	if g := fresh.testEditGrants(task); len(g) != 0 {
		t.Fatalf("A's grant returned under B: %+v", g)
	}

	// ABSENCE IS NOT AN EMPTY SET: B with no record of its own refuses.
	missing := restored
	missing.TestEditRecord = nil
	_, evs = resumeFresh(t, e, store, missing)
	if restoredPastRestoration(evs) || !hasKind(kindsOf(evs), event.WorkflowRestorationRefused) {
		t.Fatalf("an operative attempt with no recorded grant state was read as empty:\n%s", gapLoopTrace(evs))
	}
}

// W4: the same file paths with materially different payloads are different
// attempts, and A's grants do not authorize B.
func TestW4SamePathsDifferentPayloadAreDifferentAttempts(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-w4"
	planned := []string{teS, teF}
	grants := teEditGrants(t)
	a := routeWithTestEdits(t, e, task, attemptPlan("plan A", planned...), grants)
	rootFixtureTaskOnce(t, e, task, attemptObjective)
	b, err := e.beginPlanAttempt(fixtureCtx(e, task), task, attemptObjective, attemptPlan("plan A, but different", planned...))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("same paths, different payload: one identity")
	}
	// B became operative without routing recording anything for it.
	e.emitIn(fixtureCtx(e, task), event.New("s1", task, event.SourceArchitect, event.PlanProposed, "B",
		proposedPlan{architectureDecision: attemptPlan("plan A, but different", planned...), PlanSource: PlanByArchitect, PlanAttemptID: b.ID}))
	restored := interruptedFrom(t, store, task)
	if len(restored.TestEditRecord) != 0 {
		t.Fatalf("A's grant record attached to B because their paths are equal: %s", restored.TestEditRecord)
	}
	if _, err := restoreFresh(t, restored, grants, planned, teWorld); !refusedForPlanAttempt(err) {
		t.Fatalf("B resumed under no grant state of its own: %v", err)
	}
	// And A's record handed to B directly is refused by its binding.
	forged := restored
	forged.TestEditRecord = rrPayloadFor(t, a.ID, grants)
	if _, err := restoreFresh(t, forged, grants, planned, teWorld); !refusedForPlanAttempt(err) {
		t.Fatalf("A's grants authorized B: %v", err)
	}
}

func rrPayloadFor(t *testing.T, attempt string, grants []testEditGrant) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(testEditRecord{PlanAttemptID: attempt, World: teWorld, Grants: grants})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// W5: the same canonical payload under the same task, objective and world
// reproduces the same identity in another engine; nothing process-local enters.
func TestW5TheIdentityIsDeterministicAndReDerivable(t *testing.T) {
	d := attemptPlan("plan A", teS, teF)
	one, _ := attemptEngine(t)
	two, _ := attemptEngine(t)
	rootFixtureTaskOnce(t, one, "task-w5", attemptObjective)
	rootFixtureTaskOnce(t, two, "task-w5", attemptObjective)
	a1, err1 := one.beginPlanAttempt(fixtureCtx(one, "task-w5"), "task-w5", attemptObjective, d)
	a2, err2 := two.beginPlanAttempt(fixtureCtx(two, "task-w5"), "task-w5", attemptObjective, d)
	if err1 != nil || err2 != nil || a1.ID != a2.ID || len(a1.ID) != 64 {
		t.Fatalf("one payload, two identities: %q %q (%v %v)", a1.ID, a2.ID, err1, err2)
	}
	direct, _ := planAttemptID("task-w5", attemptObjective, "", PlanByArchitect, "", d)
	if direct != a1.ID {
		t.Fatal("the recorded identity is not the canonical function's")
	}
	for name, differs := range map[string]func() (string, error){
		"task": func() (string, error) {
			return planAttemptID("task-other", attemptObjective, "", PlanByArchitect, "", d)
		},
		"objective": func() (string, error) {
			return planAttemptID("task-w5", attemptObjective+" ", "", PlanByArchitect, "", d)
		},
		"world": func() (string, error) {
			return planAttemptID("task-w5", attemptObjective, teWorld, PlanByArchitect, "", d)
		},
		"source": func() (string, error) { return planAttemptID("task-w5", attemptObjective, "", PlanSupplied, "", d) },
		"digest": func() (string, error) {
			return planAttemptID("task-w5", attemptObjective, "", PlanByArchitect, "abc", d)
		},
	} {
		if id, _ := differs(); id == a1.ID {
			t.Errorf("the identity does not bind the %s", name)
		}
	}
}

// W6: a plan-admission refusal reached through production routing is bound to
// its attempt, exactly once; it survives an interruption, does not make the
// task planned, and does not attach to the replacement plan that a continued
// run makes operative.
//
// Objective 61 changed only how the invocation ends: the refusal is returned to
// the architect, which answers with the same refused plan, so the same attempt
// is routed twice under its one identity and the invocation ends
// PLAN_ADMISSION_REFUSED instead of FAILED. The binding claims are unchanged.
func TestW6AnAdmissionRefusalIsBoundToItsAttemptAcrossInterruption(t *testing.T) {
	const task = "task-w6"
	store := sessionStore(t)
	// extra/new_test.go is declared beside no covered file, so no grant is
	// derived for it and prospective admission refuses the plan.
	refused := `{"decision":"proceed","summary":"edit main","plan":"plan A","files":["main.go","extra/new_test.go"],"mode":"modify",` +
		`"prospective_surfaces":[{"path":"extra/new_test.go","package":"extra","role":"go-regression-test","dependencies":["testing"]}]}`
	e, architect, _ := newGapLoopEngine(t, nil, store, refused)
	world := grantWorld(t, e)
	run := drivePlanAdmission(t, e, architect, world, task, func(ctx context.Context) {
		e.run(ctx, task, attemptObjective, RequestedByHuman)
	})
	attempts := startedAttempts(t, run.events)
	if len(attempts) != 2 || attempts[0] != attempts[1] {
		t.Fatalf("the repeated refused plan was not routed again under its one identity: %v\n%s", attempts, gapLoopTrace(run.events))
	}
	attempts = attempts[:1]
	refusals := 0
	var bound planAttemptRefusal
	for _, ev := range run.events {
		if ev.Kind == event.PlanAttemptRefused {
			refusals++
			_ = json.Unmarshal(ev.Payload, &bound)
		}
	}
	if len(attempts) != 1 || refusals != 1 || bound.PlanAttemptID != attempts[0] || !strings.Contains(bound.Reason, "prospective admission refused") {
		t.Fatalf("the refusal is not recorded once against its attempt: attempts %v refusals %d %+v\n%s", attempts, refusals, bound, gapLoopTrace(run.events))
	}
	if hasKind(kindsOf(run.events), event.PlanProposed) {
		t.Fatal("a refused attempt became the operative plan")
	}

	// The process dies after the refusal was recorded: the record, without its
	// terminal, is what a restart finds.
	history, _ := store.Load()
	interrupted := sessionStore(t)
	for _, ev := range history {
		if ev.Kind != event.WorkflowFailed {
			if err := seedAppend(t, interrupted, ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	found := reconstructed(t, interrupted, task)
	if found.Planned || found.PlanAttemptID != "" || len(found.PlanAttemptRefusals) != 1 || len(found.PlanAttemptRefusals[bound.RefusalID]) == 0 ||
		planAttemptOfRecord(found.PlanAttemptRefusals[bound.RefusalID]) != attempts[0] {
		t.Fatalf("the refusal did not survive interruption bound to exactly its attempt: %+v", found)
	}

	// The continuation re-plans; the replacement becomes operative and the
	// refusal stays with the attempt it refused.
	next, nextArchitect, _ := newGapLoopEngine(t, &gapLoopRun{engine: e, world: world}, interrupted, replanNoGrants)
	next.Config.Sensei.Args = e.Config.Sensei.Args
	continued := driveGapLoop(t, next, nextArchitect, world, task, "", func(ctx context.Context) {
		next.Resume(ctx, found)
	})
	if !hasKind(kindsOf(continued.events), event.PlanProposed) {
		t.Fatalf("the continuation did not make a replacement operative:\n%s", gapLoopTrace(continued.events))
	}
	history, _ = interrupted.Load()
	var kept []event.Event
	for _, ev := range history {
		if ev.Kind != event.WorkflowFailed {
			kept = append(kept, ev)
		}
	}
	after := session.FindInterrupted(kept)
	if len(after) != 1 || after[0].PlanAttemptID == "" || after[0].PlanAttemptID == attempts[0] ||
		len(after[0].PlanAttemptRefusals) != 1 || planAttemptOfRecord(after[0].PlanAttemptRefusals[bound.RefusalID]) != attempts[0] {
		t.Fatalf("the refusal attached to the replacement or was lost: %+v", after)
	}
}

// planAttemptOfRecord is the PlanAttemptID a durable refusal record names.
func planAttemptOfRecord(raw json.RawMessage) string {
	var r planAttemptRefusal
	if json.Unmarshal(raw, &r) != nil {
		return ""
	}
	return r.PlanAttemptID
}

// W7: prospective and test-edit grants consume the same identity mechanism.
func TestW7BothGrantKindsBindToTheOneAttemptIdentity(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-w7"
	a := routeWithDerivation(t, e, task, attemptPlan("plan A", teS))
	history, _ := store.Load()
	kinds := map[event.Kind]string{}
	for _, ev := range history {
		if ev.Kind == event.TestEditGranted || ev.Kind == event.ProspectiveGranted {
			var bound struct {
				PlanAttemptID string `json:"plan_attempt_id"`
			}
			_ = json.Unmarshal(ev.Payload, &bound)
			kinds[ev.Kind] = bound.PlanAttemptID
		}
	}
	if kinds[event.TestEditGranted] != a.ID || kinds[event.ProspectiveGranted] != a.ID {
		t.Fatalf("the grant kinds are not bound to the one attempt identity %s: %v", a.ID, kinds)
	}
	// No grant path computes an identity of its own.
	for _, fn := range []string{"derivedCoverage", "restoreProspectiveGrants", "restoreTestEditGrants", "restorePlanBound", "routePlan"} {
		var file string
		switch fn {
		case "restoreTestEditGrants":
			file = "internal/workflow/testedit.go"
		case "restorePlanBound":
			file = "internal/workflow/suppliedplan.go"
		default:
			file = "internal/workflow/engine.go"
		}
		if body := sourceOf(t, file, fn); strings.Contains(body, "sha256") || strings.Contains(body, "planAttemptID(") {
			t.Errorf("%s computes a plan identity of its own", fn)
		}
	}
}

// W8 CONTROL: an ordinary initial plan runs as before, with the identity added:
// the attempt is recorded before its grant state, and its one PlanProposed
// follows, carrying the same identity; the run then ends for the reason it
// always ended in this fixture.
func TestW8AnOrdinaryInitialPlanBehavesAsBeforePlusItsIdentity(t *testing.T) {
	const taskID = "task-w8"
	store := sessionStore(t)
	routine := `{"decision":"proceed","summary":"edit main","plan":"edit main.go","files":["main.go"],"mode":"modify"}`
	e, architect, world := newGapLoopEngine(t, nil, store, routine)
	run := driveGapLoop(t, e, architect, world, taskID, "", func(ctx context.Context) {
		e.run(ctx, taskID, "change main.go", RequestedByHuman)
	})
	var started, granted, proposed int
	var attempt string
	for _, ev := range run.events {
		switch ev.Kind {
		case event.PlanAttemptStarted:
			var a planAttempt
			_ = json.Unmarshal(ev.Payload, &a)
			attempt, started = a.ID, started+1
		case event.TestEditGranted, event.ProspectiveGranted:
			if started == 0 || proposed != 0 {
				t.Fatal("grant state was recorded outside its attempt's window")
			}
			granted++
		case event.PlanProposed:
			var p proposedPlan
			_ = json.Unmarshal(ev.Payload, &p)
			if p.PlanAttemptID != attempt || attempt == "" {
				t.Fatalf("the operative plan is not the routed attempt: %q vs %q", p.PlanAttemptID, attempt)
			}
			proposed++
		case event.AuthorityRequired:
			t.Fatal("a routine plan asked a human")
		}
	}
	if started != 1 || granted != 2 || proposed != 1 {
		t.Fatalf("attempts=%d grant records=%d plans=%d\n%s", started, granted, proposed, gapLoopTrace(run.events))
	}
	last := run.events[len(run.events)-1]
	for _, ev := range run.events {
		if ev.Kind == event.WorkflowFailed {
			last = ev
		}
	}
	if !strings.Contains(last.Summary, "candidate worktree capability is not granted") {
		t.Fatalf("the ordinary run ended differently: %s: %s", last.Kind, last.Summary)
	}
	// And a fresh engine reconstructs that attempt from the durable record.
	history, _ := store.Load()
	var kept []event.Event
	for _, ev := range history {
		if ev.Kind != event.WorkflowFailed {
			kept = append(kept, ev)
		}
	}
	found := session.FindInterrupted(kept)
	if len(found) != 1 || found[0].PlanAttemptID != attempt {
		t.Fatalf("the restart did not reconstruct the run's attempt: %+v", found)
	}
	if err := (&Engine{}).restorePlanAttempt(found[0], world); err != nil {
		t.Fatalf("the run's own attempt does not verify at its pinned base: %v", err)
	}
}

// W10: one material declared_effect field changes the identity, and authority
// recorded under the first plan is refused under the second; a fresh-engine
// resume of the unchanged plan reproduces the identity and the exact effects.
func TestW10DeclaredEffectsArePartOfThePlanAttemptIdentity(t *testing.T) {
	effects := func(target string) architectureDecision {
		d := attemptPlan("plan", teS, teF)
		d.Steps = []string{"write the record"}
		d.DeclaredEffects = []DeclaredEffect{{Statement: EffectInStep, Step: 1, Operation: "write", Occurrence: 1, Target: target, Scope: EffectInProcess}}
		return d
	}
	first, second := effects("the session record"), effects("the canonical checkout")
	id1, _ := planAttemptID("task-w10", attemptObjective, "", PlanByArchitect, "", first)
	id2, _ := planAttemptID("task-w10", attemptObjective, "", PlanByArchitect, "", second)
	if id1 == id2 {
		t.Fatal("two plans differing in one declared effect share one identity")
	}

	e, store := attemptEngine(t)
	grants := teEditGrants(t)
	routeWithTestEdits(t, e, "task-w10", first, grants)
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, "task-w10"), "task-w10", attemptObjective, second); err == nil {
		t.Fatal("a plan whose declared effects differ was made operative under the routed attempt's authority")
	}
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, "task-w10"), "task-w10", attemptObjective, first); err != nil {
		t.Fatal(err)
	}
	restored := interruptedFrom(t, store, "task-w10")
	fresh, err := restoreFresh(t, restored, grants, []string{teS, teF}, teWorld)
	if err != nil {
		t.Fatal(err)
	}
	got := fresh.operativePlanAttempt("task-w10")
	gotEffects, _ := json.Marshal(got.Plan.DeclaredEffects)
	wantEffects, _ := json.Marshal(first.DeclaredEffects)
	if got.ID != id1 || string(gotEffects) != string(wantEffects) {
		t.Fatalf("the resume did not reproduce the identity and exact effects: %s %s", got.ID, gotEffects)
	}

	// The first plan's grants under the second plan's identity are refused.
	under := restored
	under.PlanAttemptID = id2
	under.PlanRecord, _ = json.Marshal(proposedPlan{architectureDecision: second, PlanSource: PlanByArchitect, PlanAttemptID: id2})
	if _, err := restoreFresh(t, under, grants, []string{teS, teF}, teWorld); !refusedForPlanAttempt(err) {
		t.Fatalf("the first plan's grants authorized the second: %v", err)
	}
	// An altered, missing or malformed effect in the record does not verify.
	for name, mutate := range map[string]func(string) string{
		"altered": func(s string) string { return strings.Replace(s, "the session record", "the session recorD", 1) },
		"missing": func(s string) string {
			return strings.Replace(s, `,"declared_effects":[`, `,"declared_effects_gone":[`, 1)
		},
		"malformed": func(s string) string {
			return strings.Replace(s, `"operation":"write"`, `"operation":"write","extra":true`, 1)
		},
	} {
		tampered := restored
		tampered.PlanRecord = json.RawMessage(mutate(string(restored.PlanRecord)))
		if string(tampered.PlanRecord) == string(restored.PlanRecord) {
			t.Fatalf("%s: the mutation did not apply", name)
		}
		if err := (&Engine{}).restorePlanAttempt(tampered, ""); !refusedForPlanAttempt(err) {
			t.Errorf("%s declared effect was accepted: %v", name, err)
		}
	}
}

// W11: through the production routing path, a plan declares the existing-test
// and prospective authority it needs, routing records both grants for its
// attempt, and the plan's premises claim those exact grants are unestablished.
// The recorded grants govern: no bounded-knowledge gap opens, no closure round
// runs (zero-file or otherwise), the contradiction is reported, and the run
// proceeds to implementation. Controls: an independent premise in the same
// plan still opens its gap over the planned region, and the same claims in
// prose, without the structured subject, are not suppressed.
func TestW11RecordedGrantsTakePrecedenceOverAContradictingPremise(t *testing.T) {
	const declares = `"files":["main.go","main_test.go","extra_test.go"],"mode":"modify",` +
		`"test_edits":[{"path":"main_test.go","operation":"edit","package":"main","build_constraints":[],"imports":["testing"]}],` +
		`"prospective_surfaces":[{"path":"extra_test.go","package":"main","role":"go-regression-test","dependencies":["testing"]}]`
	const negative = `{"statement":"the existing-test edit grant for main_test.go is not established","about":"main_test.go","source":"inference",` +
		`"authority":{"requirement":"test_edit","path":"main_test.go","state":"unestablished"}},` +
		`{"statement":"the prospective grant for extra_test.go is not established","about":"extra_test.go","source":"inference",` +
		`"authority":{"requirement":"prospective_create","path":"extra_test.go","state":"unestablished"}}`
	route := func(t *testing.T, plan string) gapLoopRun {
		t.Helper()
		store := sessionStore(t)
		e, architect, _ := newGapLoopEngine(t, nil, store, plan)
		world := grantWorld(t, e)
		e.Config.Sensei.Args = []string{"-c", regionScript(gapLoopSenseiScript, "main.go", "main_test.go", "extra_test.go")}
		return driveGapLoop(t, e, architect, world, "task-w11", "", func(ctx context.Context) {
			e.run(ctx, "task-w11", attemptObjective, RequestedByHuman)
		})
	}
	closureRounds := func(run gapLoopRun) []GapIdentity {
		var out []GapIdentity
		for _, ev := range run.events {
			if ev.Kind == event.Status && strings.Contains(ev.Summary, "bounded knowledge gap") && strings.Contains(ev.Summary, "closing it") {
				var p struct {
					Gap GapIdentity `json:"gap_identity"`
				}
				_ = json.Unmarshal(ev.Payload, &p)
				out = append(out, p.Gap)
			}
		}
		return out
	}

	run := route(t, `{"decision":"proceed","summary":"edit main","plan":"edit main.go and its tests",`+declares+`,"claims":[`+negative+`]}`)
	kinds := kindsOf(run.events)
	if !hasKind(kinds, event.TestEditGranted) || !hasKind(kinds, event.ProspectiveGranted) {
		t.Fatal("premise: routing did not record both grant kinds")
	}
	for _, ev := range run.events {
		if ev.Kind == event.TestEditGranted || ev.Kind == event.ProspectiveGranted {
			var r struct {
				Grants []json.RawMessage `json:"grants"`
			}
			if json.Unmarshal(ev.Payload, &r) != nil || len(r.Grants) != 1 {
				t.Fatalf("premise: %s recorded no grant for the declared path: %s", ev.Kind, ev.Payload)
			}
		}
	}
	if rounds := closureRounds(run); len(rounds) != 0 {
		t.Fatalf("a recorded grant still opened a closure round: %+v", rounds)
	}
	if len(run.prompts) != 1 || hasKind(kinds, event.AuthorityRequired) {
		t.Fatalf("the contradicted premise sent the run back to the architect or to a human:\n%s", gapLoopTrace(run.events))
	}
	reported := false
	for _, ev := range run.events {
		reported = reported || strings.Contains(ev.Summary, "architect-plan inconsistency")
	}
	if !reported || !hasKind(kinds, event.PlanProposed) {
		t.Fatalf("the contradiction was not reported, or the plan was not admitted:\n%s", gapLoopTrace(run.events))
	}
	last := run.events[len(run.events)-1]
	for _, ev := range run.events {
		if ev.Kind == event.WorkflowFailed {
			last = ev
		}
	}
	if !strings.Contains(last.Summary, "candidate worktree capability is not granted") {
		t.Fatalf("the admitted plan did not reach implementation: %s", last.Summary)
	}

	// CONTROL: an independent premise opens its gap, over the planned region.
	independent := `{"statement":"main has no callers","about":"main.go","source":"inference"}`
	run = route(t, `{"decision":"proceed","summary":"edit main","plan":"edit main.go and its tests",`+declares+`,"claims":[`+negative+`,`+independent+`]}`)
	rounds := closureRounds(run)
	if len(rounds) == 0 {
		t.Fatalf("the independent premise was swallowed with the contradicted ones:\n%s", gapLoopTrace(run.events))
	}
	for _, g := range rounds {
		if len(g.Scope) == 0 || g.Subject != "main.go" {
			t.Fatalf("the closure round lost the planned region (a zero-file gap): %+v", g)
		}
	}

	// CONTROL: the same claims as prose, with no structured subject, are not
	// classified by their words; they open a gap as any unverified premise does.
	prose := `{"statement":"the existing-test edit grant for main_test.go is not established","about":"main_test.go","source":"inference"}`
	run = route(t, `{"decision":"proceed","summary":"edit main","plan":"edit main.go and its tests",`+declares+`,"claims":[`+prose+`]}`)
	if len(closureRounds(run)) == 0 {
		t.Fatalf("a prose claim was read as a recorded-authority premise by its vocabulary:\n%s", gapLoopTrace(run.events))
	}
}

// W11, the predicate itself: only the exact structured premise a matching
// record answers is set aside, and only for the attempt and world the record
// belongs to.
func TestW11OnlyTheExactRecordedAuthorityPremiseIsSetAside(t *testing.T) {
	e, _ := attemptEngine(t)
	const task = "task-w11-exact"
	d := attemptPlan("plan", teS, teF)
	d.TestEdits = []TestEditDeclaration{{Path: teF}}
	premise := func(about, path string) Claim {
		return Claim{Statement: "the grant is not established", About: about, Source: "inference",
			Authority: &AuthorityPremise{Requirement: premiseTestEdit, Path: path, State: premiseAuthorityUnestablished}}
	}
	exact := premise(teF, teF)
	mixed := premise(teS+" and "+teF, teF)
	undeclared := premise(teS, teS)
	otherState := premise(teF, teF)
	otherState.Authority.State = "Unestablished"
	const created = "modfile/new_test.go"
	d.ProspectiveSurfaces = []ProspectiveSurface{{Path: created, Package: "modfile", Role: "go-regression-test"}}
	ungranted := Claim{Statement: "the create grant is not established", About: created, Source: "inference",
		Authority: &AuthorityPremise{Requirement: premiseProspectiveCreate, Path: created, State: premiseAuthorityUnestablished}}
	d.Claims = []Claim{exact, mixed, undeclared, otherState, ungranted}

	rootFixtureTaskOnce(t, e, task, attemptObjective)
	a, err := e.beginPlanAttempt(fixtureCtx(e, task), task, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	if _, contradicted := e.premisesUnderRecordedAuthority(task, d); len(contradicted) != 0 {
		t.Fatal("a premise was set aside with no grant recorded")
	}
	// A test-edit grant for this attempt but read at another world answers
	// nothing.
	grants := teEditGrants(t)
	if grants[0].World == a.World {
		t.Fatal("premise: the fixture grant must be at another world")
	}
	e.recordTestEditGrants(fixtureCtx(e, task), task, "recorded", testEditRecord{PlanAttemptID: a.ID, World: grants[0].World, Grants: grants})
	if _, contradicted := e.premisesUnderRecordedAuthority(task, d); len(contradicted) != 0 {
		t.Fatal("a test-edit grant recorded at another world answered the premise")
	}
	for i := range grants {
		grants[i].World = a.World
	}
	e.recordTestEditGrants(fixtureCtx(e, task), task, "recorded", testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants})
	kept, contradicted := e.premisesUnderRecordedAuthority(task, d)
	if len(contradicted) != 1 || contradicted[0].About != teF || len(kept) != 4 {
		t.Fatalf("not exactly the matching premise was set aside: kept %v contradicted %v", kept, contradicted)
	}
	// A prospective record holding the grant, but read at another world,
	// answers nothing; at this attempt's world it answers the create premise.
	granted := []prospectiveGrant{{Surface: d.ProspectiveSurfaces[0], Covering: teS}}
	e.recordProspectiveGrants(fixtureCtx(e, task), task, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: "another world", Grants: granted})
	if _, contradicted := e.premisesUnderRecordedAuthority(task, d); len(contradicted) != 1 {
		t.Fatalf("a prospective grant recorded at another world answered the premise: %v", contradicted)
	}
	e.recordProspectiveGrants(fixtureCtx(e, task), task, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: a.World, Grants: granted})
	if _, contradicted := e.premisesUnderRecordedAuthority(task, d); len(contradicted) != 2 {
		t.Fatalf("the recorded prospective grant did not answer its premise: %v", contradicted)
	}
	// A record for another attempt answers nothing for this one.
	other := d
	other.Plan = "another plan"
	rootFixtureTaskOnce(t, e, task, attemptObjective)
	if _, err := e.beginPlanAttempt(fixtureCtx(e, task), task, attemptObjective, other); err != nil {
		t.Fatal(err)
	}
	if _, contradicted := e.premisesUnderRecordedAuthority(task, other); len(contradicted) != 0 {
		t.Fatal("a grant recorded for another attempt answered this attempt's premise")
	}
}

// W12: the primitive lives in an existing production file; the objective
// creates no production Go file.
func TestW12ThePrimitiveLivesInTheExistingWorkflowSeam(t *testing.T) {
	for _, fn := range []string{"planAttemptID", "beginPlanAttempt", "adoptPlanAttempt", "restorePlanAttempt"} {
		if body := funcBody(t, "internal/workflow/engine.go", fn); body == "" {
			t.Errorf("%s is not declared in internal/workflow/engine.go", fn)
		}
	}
}

// ---------------------------------------------------------------------------
// The attempt's prerequisites and what binds to it (review of cycle 1).
// ---------------------------------------------------------------------------

// The start record is an ACKNOWLEDGED durable prerequisite: a start the store
// refuses fails routing closed, and nothing is derived or recorded under an
// identity no record establishes. The refusal halts the task through the one
// append-failure owner (70B2a1): the failure to record the start is the run's
// outcome, and no terminal is recorded or published after it.
func TestAPlanAttemptWhoseStartCannotBeRecordedIsNotRouted(t *testing.T) {
	const task = "task-start-refused"
	store := sessionStore(t)
	// A plan too large for one durable event: the store refuses the start.
	huge, _ := json.Marshal(architectureDecision{Decision: "proceed", Summary: "edit main", Plan: strings.Repeat("x", 17<<20),
		Files: []string{"main.go"}, Mode: ModeModify})
	run := runGapLoop(t, store, task, string(huge))
	kinds := kindsOf(run.events)
	for _, k := range []event.Kind{event.PlanAttemptStarted, event.TestEditGranted, event.ProspectiveGranted, event.PlanProposed} {
		if hasKind(kinds, k) {
			t.Fatalf("%s was recorded although the attempt's start could not be:\n%s", k, gapLoopTrace(run.events))
		}
	}
	f := taskFailure(run.engine, task)
	if f == nil || f.Kind != event.PlanAttemptStarted || !errors.Is(f, session.ErrSessionEventTooLarge) {
		t.Fatalf("the run did not fail closed on the unrecorded start: %v\n%s", f, gapLoopTrace(run.events))
	}
	if hasKind(kinds, event.WorkflowFailed) || hasKind(kinds, event.RunReceipt) {
		t.Fatalf("a terminal was published after the start could not be recorded:\n%s", gapLoopTrace(run.events))
	}
	if history, err := store.Load(); err != nil || hasKind(kindsOf(history), event.WorkflowFailed) {
		t.Fatalf("a terminal was recorded after the start could not be recorded: %v", err)
	}
	if e := run.engine; e.pendingPlanAttempt(task).ID != "" {
		t.Fatal("an unrecorded attempt became the one authority binds to")
	}
}

// A resume accepts an operative attempt only when the durable record shows it
// was started FIRST: before its operative PlanProposed and before the grant
// records bound to it, and as exactly the plan that became operative.
func TestAResumeRequiresTheAttemptStartToPrecedeItsAuthority(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-start-order"
	planned := []string{teS, teF}
	grants := teEditGrants(t)
	a := routeWithTestEdits(t, e, task, attemptPlan("plan A", planned...), grants)
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, task), task, attemptObjective, attemptPlan("plan A", planned...)); err != nil {
		t.Fatal(err)
	}
	history, _ := store.Load()
	rebuilt := func(edit func([]event.Event) []event.Event) session.Interrupted {
		t.Helper()
		evs := append([]event.Event{event.New("s1", task, event.SourceUser, event.TaskCreated, attemptObjective, nil)},
			edit(append([]event.Event(nil), history...))...)
		for _, found := range session.FindInterrupted(evs) {
			if found.TaskID == task {
				return found
			}
		}
		t.Fatal("not reconstructed")
		return session.Interrupted{}
	}
	if _, err := restoreFresh(t, rebuilt(func(evs []event.Event) []event.Event { return evs }), grants, planned, teWorld); err != nil {
		t.Fatalf("CONTROL: the attempt started first is refused: %v", err)
	}
	startAt := -1
	for i, ev := range history {
		if ev.Kind == event.PlanAttemptStarted {
			startAt = i
		}
	}
	missing := rebuilt(func(evs []event.Event) []event.Event { return append(evs[:startAt:startAt], evs[startAt+1:]...) })
	if _, err := restoreFresh(t, missing, grants, planned, teWorld); !refusedForPlanAttempt(err) || !strings.Contains(err.Error(), "no record shows it was started") {
		t.Fatalf("an attempt with no recorded start was accepted, or refused for another reason: %v", err)
	}
	late := rebuilt(func(evs []event.Event) []event.Event {
		start := evs[startAt]
		return append(append(evs[:startAt:startAt], evs[startAt+1:]...), start)
	})
	if _, err := restoreFresh(t, late, grants, planned, teWorld); !refusedForPlanAttempt(err) || !strings.Contains(err.Error(), "only after the authority") {
		t.Fatalf("a start recorded after the authority it must precede was accepted, or refused for another reason: %v", err)
	}
	// Started after the plan was made operative, though before its grants.
	afterOperative := rebuilt(func(evs []event.Event) []event.Event {
		var proposed event.Event
		var rest, moved []event.Event
		for _, ev := range evs {
			switch ev.Kind {
			case event.PlanProposed:
				proposed = ev
			case event.PlanAttemptStarted, event.TestEditGranted, event.ProspectiveGranted:
				moved = append(moved, ev)
			default:
				rest = append(rest, ev)
			}
		}
		return append(append(rest, proposed), moved...)
	})
	if _, err := restoreFresh(t, afterOperative, grants, planned, teWorld); !refusedForPlanAttempt(err) || !strings.Contains(err.Error(), "only after the authority") {
		t.Fatalf("a start recorded after its operative PlanProposed was accepted, or refused for another reason: %v", err)
	}
	forged := rebuilt(func(evs []event.Event) []event.Event {
		f := planAttempt{ID: a.ID, TaskID: task, World: a.World, PlanSource: a.PlanSource, Plan: attemptPlan("another plan", planned...)}
		evs[startAt] = event.New("s1", task, event.SourceSystem, event.PlanAttemptStarted, "forged", f)
		return evs
	})
	if _, err := restoreFresh(t, forged, grants, planned, teWorld); !refusedForPlanAttempt(err) {
		t.Fatalf("a start describing another plan under the same identity was accepted: %v", err)
	}
}

// Every admission refusal of a started attempt is recorded against it, by the
// one recorder, whatever refused it -- here a supplied plan's knowledge gap
// and a human's recorded decline -- and the routing outcome is unchanged.
func TestEveryAdmissionRefusalIsRecordedAgainstItsAttempt(t *testing.T) {
	refusalOf := func(t *testing.T, run gapLoopRun) planAttemptRefusal {
		t.Helper()
		attempts := startedAttempts(t, run.events)
		var got []planAttemptRefusal
		for _, ev := range run.events {
			if ev.Kind == event.PlanAttemptRefused {
				var r planAttemptRefusal
				_ = json.Unmarshal(ev.Payload, &r)
				got = append(got, r)
			}
		}
		if len(attempts) == 0 || len(got) != 1 || got[0].PlanAttemptID != attempts[len(attempts)-1] {
			t.Fatalf("not exactly one refusal bound to the refused attempt: attempts %v refusals %+v\n%s", attempts, got, gapLoopTrace(run.events))
		}
		var failed string
		for _, ev := range run.events {
			if ev.Kind == event.WorkflowFailed {
				failed = ev.Summary
			}
		}
		if failed != got[0].Reason {
			t.Fatalf("the routing outcome changed: the run ended %q, the refusal says %q", failed, got[0].Reason)
		}
		return got[0]
	}

	t.Run("supplied plan knowledge gap", func(t *testing.T) {
		const task = "task-refuse-supplied"
		p, err := ParseSuppliedPlan([]byte(gapProceed))
		if err != nil {
			t.Fatal(err)
		}
		e, architect, world := newGapLoopEngine(t, nil, sessionStore(t), gapReply)
		e.supplyPlan(task, p)
		run := driveGapLoop(t, e, architect, world, task, "", func(ctx context.Context) {
			e.run(ctx, task, attemptObjective, RequestedByHuman)
		})
		if r := refusalOf(t, run); !strings.Contains(r.Reason, "knowledge gap") {
			t.Fatalf("the supplied plan was refused for another reason: %s", r.Reason)
		}
	})
	t.Run("declined authority", func(t *testing.T) {
		const task = "task-refuse-declined"
		store := sessionStore(t)
		rootFixtureTask(t, store, "s1", task, attemptObjective)
		e, architect, world := newGapLoopEngine(t, nil, store, gapProceed)
		gap := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: world}
		declined := resolvedAuthority{Resolution: authority.Resolution{TaskID: task, SessionID: "s1", Question: "declined", OptionID: "1", Condition: "declined",
			Scope: []string{"main.go"}, Outcome: authority.Decline, State: authority.Unsupported, DecidedAt: time.Now().UTC()}, Gap: &gap}
		if err := seedAppend(t, store, event.New("s1", task, event.SourceUser, event.AuthorityResolved, "Decline", declined)); err != nil {
			t.Fatal(err)
		}
		run := driveGapLoop(t, e, architect, world, task, "", func(ctx context.Context) {
			runRootedTask(ctx, e, task, attemptObjective, RequestedByHuman)
		})
		if r := refusalOf(t, run); !strings.Contains(r.Reason, "declined") {
			t.Fatalf("the plan was refused for another reason: %s", r.Reason)
		}
	})
}

// A human answer is owned by the plan attempt its question was asked about:
// the live answer and the deferred question both carry that PlanAttemptID.
func TestAHumanAnswerAndADeferredQuestionNameTheirAttempt(t *testing.T) {
	asked := func(run gapLoopRun) string {
		last := ""
		for _, ev := range run.events {
			if ev.Kind == event.PlanAttemptStarted {
				var a planAttempt
				_ = json.Unmarshal(ev.Payload, &a)
				last = a.ID
			}
			if ev.Kind == event.AuthorityRequired {
				return last
			}
		}
		return ""
	}
	deferred := runGapLoop(t, sessionStore(t), "task-asked-deferred", gapProceed)
	var q DeferredAuthority
	payloadOf(t, deferred.events, event.WorkflowAwaitingAuthority, &q)
	if q.PlanAttemptID == "" || q.PlanAttemptID != asked(deferred) {
		t.Fatalf("the deferred question does not name the attempt it was asked about: %q vs %q", q.PlanAttemptID, asked(deferred))
	}

	e, architect, world := newGapLoopEngine(t, nil, sessionStore(t), gapProceed)
	answered := driveGapLoop(t, e, architect, world, "task-asked-answered", "1", func(ctx context.Context) {
		e.run(ctx, "task-asked-answered", attemptObjective, RequestedByHuman)
	})
	var r resolvedAuthority
	payloadOf(t, answered.events, event.AuthorityResolved, &r)
	if r.PlanAttemptID == "" || r.PlanAttemptID != asked(answered) {
		t.Fatalf("the answer does not name the attempt it was asked about: %q vs %q", r.PlanAttemptID, asked(answered))
	}
	// And it was honoured: the run was admitted on it rather than asking again.
	if !hasKind(kindsOf(answered.events), event.PlanProposed) {
		t.Fatalf("the answered run was not admitted:\n%s", gapLoopTrace(answered.events))
	}

	// A deferred question naming an attempt this task never started is not
	// answerable: the answer would be owned by an identity nothing establishes.
	forged := q
	forged.PlanAttemptID = strings.Repeat("f", 64)
	raw, _ := json.Marshal(forged)
	// FIXTURE MIGRATION (70B2a1): a fresh process resumes the deferred task
	// from the record that holds it, under a fresh session the resume binds.
	fresh, _ := attemptEngine(t)
	fresh.Store, fresh.SessionID = deferred.engine.Store, "s2"
	bindFixtureSession(t, fresh.Store, "task-asked-deferred", fresh.SessionID)
	ch, cancel := fresh.Bus.Subscribe(64)
	defer cancel()
	fresh.resumeAuthority(fixtureCtx(fresh, "task-asked-deferred"), session.Interrupted{TaskID: "task-asked-deferred", AwaitingAuthority: raw,
		StartedPlanAttempts: map[string]bool{q.PlanAttemptID: true}})
	refused := false
	for len(ch) > 0 {
		ev := <-ch
		refused = refused || (ev.Kind == event.WorkflowFailed && strings.Contains(ev.Summary, "never recorded as started"))
	}
	if !refused {
		t.Fatal("a question naming an unstarted attempt was put to the human")
	}
}

// Gap settlements and consequence answers are owned by exactly the plan attempt
// they were asked about (answerScope). Another attempt -- the plan written in
// reply to the answer, before or after it is adopted, one differing from the
// owner only in a declared effect -- is routed through its own authority
// decision and never consumes them; an answer naming an attempt nobody started
// authorizes nothing; and a restart reads the same ownership from the record.
func TestASettlementDoesNotAuthorizeAReplacementPlanAttempt(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-owned-answers"
	gap := GapIdentity{Kind: "unverified-premise", Subject: teS, Scope: []string{teS}}
	route := Routing{Route: RouteHuman, Condition: "may it change", Gap: gap}
	answer := func(owner, condition string, withGap bool) {
		t.Helper()
		res := resolvedAuthority{Resolution: authority.Resolution{TaskID: task, SessionID: "s1", Question: condition, OptionID: "1", Condition: condition,
			Scope: []string{teS}, Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC()}, PlanAttemptID: owner}
		if withGap {
			res.Gap = &gap
			e.settleGapFor(task, gap, authority.Authorize, owner)
		}
		e.emitIn(fixtureCtx(e, task), event.New("s1", task, event.SourceUser, event.AuthorityResolved, "Authorize", res))
	}
	consumes := func(e *Engine) (gapSettled, conditionAnswered bool) {
		_, gapSettled = e.gapSettlement(task, route)
		_, conditionAnswered = e.applyAnsweredCondition(task, "the consequence", teS)
		return
	}
	withEffect := func(target string) architectureDecision {
		d := attemptPlan("plan A", teS)
		d.Steps = []string{"write the record"}
		d.DeclaredEffects = []DeclaredEffect{{Statement: EffectInStep, Step: 1, Operation: "write", Occurrence: 1, Target: target, Scope: EffectInProcess}}
		return d
	}

	planA := withEffect("the session record")
	a := routeWithDerivation(t, e, task, planA)
	answer(a.ID, "may it change", true)
	answer(a.ID, "the consequence", false)
	if g, c := consumes(e); !g || !c {
		t.Fatalf("the attempt asked about does not hold its own answers: gap %v condition %v", g, c)
	}
	// B is A with one declared effect changed: a different attempt, routed in
	// the same resolution, before anything is adopted.
	planB := withEffect("the canonical checkout")
	b := routeWithDerivation(t, e, task, planB)
	if b.ID == a.ID {
		t.Fatal("a changed declared effect did not produce a different attempt")
	}
	if g, c := consumes(e); g || c {
		t.Fatalf("A's answers authorized B before adoption: gap %v condition %v", g, c)
	}
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, task), task, attemptObjective, planB); err != nil {
		t.Fatal(err)
	}
	if g, c := consumes(e); g || c {
		t.Fatalf("A's answers authorized B after adoption: gap %v condition %v", g, c)
	}
	if g, c := consumes(&Engine{Store: store}); g || c {
		t.Fatalf("a restarted process lets A's answers authorize operative B: gap %v condition %v", g, c)
	}
	// B's own answers are B's.
	answer(b.ID, "may it change", true)
	answer(b.ID, "the consequence", false)
	if g, c := consumes(e); !g || !c {
		t.Fatalf("operative B does not hold its own answers: gap %v condition %v", g, c)
	}
	if g, c := consumes(&Engine{Store: store}); !g || !c {
		t.Fatalf("a restarted process does not reconstruct B's own answers: gap %v condition %v", g, c)
	}
	// Routing A again -- one identity, wherever it recurs -- holds A's answers
	// and none of B's.
	// One operative invocation of the task at a time: e's ends before e2's.
	endFixtureInvocation(e, task)
	e2 := &Engine{Store: store, SessionID: "s1", Bus: event.NewBus()}
	e2.Repo.Root = t.TempDir()
	if again := routeWithDerivation(t, e2, task, planA); again.ID != a.ID {
		t.Fatal("the same plan did not reproduce its identity")
	}
	if _, settledFor := e2.gapResolutions(task).settlements[gap.Key()]; !settledFor {
		t.Fatal("the record lost the settlements")
	}
	if g, c := consumes(e2); !g || !c {
		t.Fatalf("A routed again lost its own answers: gap %v condition %v", g, c)
	}
	// An answer naming an attempt this task never started authorizes nothing.
	endFixtureInvocation(e2, task)
	rootFixtureTaskOnce(t, e, task, attemptObjective)
	if _, err := e.beginPlanAttempt(fixtureCtx(e, task), task, attemptObjective, attemptPlan("plan C", teS)); err != nil {
		t.Fatal(err)
	}
	e.emitIn(fixtureCtx(e, task), event.New("s1", task, event.SourceUser, event.AuthorityResolved, "Authorize", resolvedAuthority{
		Resolution: authority.Resolution{TaskID: task, SessionID: "s1", Question: "unowned", OptionID: "1", Condition: "unowned", Scope: []string{teS},
			Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC()},
		PlanAttemptID: strings.Repeat("f", 64)}))
	if _, asked := e.applyAnsweredCondition(task, "unowned", teS); asked {
		t.Fatal("an answer naming an attempt nobody started was consumed")
	}
}

// The routing record is plan-local: it is bound to the attempt that was routed,
// and a revision routed and never adopted leaves the operative attempt's
// routing, grant state and coverage world exactly as they were. Adoption makes
// the revision's own record the one read, in the same step.
func TestAnAbandonedReplanLeavesNoRoutingStateOnTheOperativePlan(t *testing.T) {
	e, _ := attemptEngine(t)
	const task = "task-abandoned-replan"
	var zero routingRecord
	planA, planB := attemptPlan("plan A", teS), attemptPlan("plan B", teS, "other.go")
	grantsA := []testEditGrant{{Path: teS, World: teWorld}}

	a := routeWithTestEdits(t, e, task, planA, grantsA)
	e.setRouting(task, zero.Policy, zero.Scoped, nil, []string{"routed for A"})
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, task), task, attemptObjective, planA); err != nil {
		t.Fatal(err)
	}
	routed := func() string {
		t.Helper()
		r, ok := e.routingFor(task)
		if !ok {
			return "<none>"
		}
		return r.PlanAttemptID + ":" + strings.Join(r.Planned, ",")
	}
	wantA := a.ID + ":routed for A"
	if got := routed(); got != wantA {
		t.Fatalf("operative A reads routing %q, want %q", got, wantA)
	}

	// B is routed: its own grant state and routing are recorded, and while it
	// is only pending the operative plan still reads A's.
	b := routeWithTestEdits(t, e, task, planB, nil)
	e.setRouting(task, zero.Policy, zero.Scoped, nil, []string{"routed for B"})
	if got := routed(); got != wantA {
		t.Fatalf("a routed, unadopted revision replaced the operative plan's routing: %q", got)
	}
	// The revision is NOT adopted.
	e.reinstateOperativePlanAttempt(task)
	if got := routed(); got != wantA {
		t.Fatalf("after the revision was abandoned the operative plan reads routing %q, want %q", got, wantA)
	}
	if e.pendingPlanAttempt(task).ID != a.ID || len(e.testEditGrants(task)) != 1 || e.testEditGrants(task)[0].Path != teS {
		t.Fatalf("the operative attempt's grant state was not reinstated: pending %s grants %+v", short12(e.pendingPlanAttempt(task).ID), e.testEditGrants(task))
	}

	// Routed again and adopted, B's own record is read -- and a record for any
	// attempt other than the one the task stands on is never returned.
	routeWithTestEdits(t, e, task, planB, nil)
	e.setRouting(task, zero.Policy, zero.Scoped, nil, []string{"routed for B"})
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, task), task, attemptObjective, planB); err != nil {
		t.Fatal(err)
	}
	if got := routed(); got != b.ID+":routed for B" {
		t.Fatalf("adopted B reads routing %q", got)
	}
	e.mu.Lock()
	delete(e.routings[task], b.ID)
	e.routings[task]["forged"] = routingRecord{PlanAttemptID: b.ID, Planned: []string{"forged"}}
	e.mu.Unlock()
	if got := routed(); got != "<none>" {
		t.Fatalf("a routing recorded for no attempt the task stands on was read: %q", got)
	}
}

// sequencedReviewer answers each review with the next scripted verdict.
type sequencedReviewer struct {
	mu      sync.Mutex
	replies []string
}

func (r *sequencedReviewer) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	text := r.replies[0]
	if len(r.replies) > 1 {
		r.replies = r.replies[1:]
	}
	return agent.Result{Text: text, Session: "unverified"}, nil
}

// contradictionRoles scripts the architect, answers the reviewer turn from
// reviewer, and leaves the implementer on its configured stub process.
type contradictionRoles struct {
	architect *scriptedArchitect
	reviewer  *sequencedReviewer
}

func (r contradictionRoles) Resolve(spec RunnerSpec) (Resolved, error) {
	if string(spec.Role) == "architect" {
		return (&fixedResolver{runner: r.architect, name: "claude"}).Resolve(spec)
	}
	return roleResolver{reviewer: r.reviewer, name: "codex", session: "s1"}.Resolve(spec)
}

const (
	// revisingReviewer refuses the first candidate, leaving that review open.
	// Its finding is minor, so the next worker owes it no response and may hand
	// the unchanged candidate back for review.
	revisingReviewer = `{"decision":"revise","summary":"the proof is missing","findings":[{"id":"1","severity":"minor","class":"code",` +
		`"claim":"the test does not fail without the fix","reference":"main.go","reason":"no mutation"}]}`
	acceptingReviewer = `{"decision":"accept","summary":"the candidate stands"}`
	// standingRevision is the architect's adjudication of the contradiction: a
	// different plan, which the accepting review standing leaves unadopted.
	standingRevision = `{"decision":"proceed","summary":"the earlier finding does not apply","plan":"plan B: edit main.go only","files":["main.go"],"mode":"modify",` +
		`"adjudication":"accepting_review_stands"}`
)

// An abandoned revision leaves no identity on the receipt either, driven
// through the governed production run. A is operative; the first worker's
// review revises, the run hands the candidate to a second worker that produces
// the same bytes, that review accepts them, and runCandidate puts the
// contradiction to the architect, which routes B and answers that the
// accepting review stands -- so B is not adopted. The run reaches its terminal
// receipt, and EVERY projection must stand on A: the receipt's canonical
// PlanAttemptID, the operative and pending attempts, A's grants and coverage
// world, and A's routing record. No replacement PlanProposed is written.
func TestAnAbandonedReplanLeavesTheReceiptOnTheOperativePlan(t *testing.T) {
	requireGofmt(t)
	const task = "task-abandoned-replan-receipt"
	store := sessionStore(t)
	e, _, _ := newGapLoopEngine(t, nil, store, replanA)
	grantWorld(t, e)
	e.Config.Sensei.Args = []string{"-c", regionScript(objectiveRunScript, "main.go", "main_test.go")}
	e.Config.Permissions.WriteCandidates, e.Config.Permissions.CreateWorktrees = true, true
	e.Config.Permissions.RunFormatters, e.Config.Permissions.LocalCommit = true, true
	e.Config.Validation = formattingValidation()
	e.Config.Workflow.ReviewCycles = 1
	worker := e.Config.Architect
	worker.Command, worker.Args = stubProcess(t, "implementor", "")
	second := worker
	second.Name = "gemini"
	e.Config.Implementors = []config.Agent{worker, second}
	e.Config.Reviewer = e.Config.Architect
	e.Config.Reviewer.Name = "codex"
	e.Runners = contradictionRoles{
		architect: &scriptedArchitect{turns: []architectTurn{{text: replanA}, {text: standingRevision}}},
		reviewer:  &sequencedReviewer{replies: []string{revisingReviewer, acceptingReviewer}},
	}
	events, cancel := e.Bus.Subscribe(4096)
	defer cancel()
	go e.run(t.Context(), task, attemptObjective, RequestedByHuman)
	seen := settleResume(t, events)
	if last := seen[len(seen)-1].Kind; last != event.WorkflowCompleted {
		t.Fatalf("the run did not complete on the standing accepting review (%s):\n%s", last, gapLoopTrace(seen))
	}
	// The terminal receipt is emitted around the terminal event; keep reading
	// until it arrives.
	var receipt *event.Event
	for i := range seen {
		if seen[i].Kind == event.RunReceipt {
			receipt = &seen[i]
		}
	}
	for deadline := time.After(10 * time.Second); receipt == nil; {
		select {
		case ev := <-events:
			seen = append(seen, ev)
			if ev.Kind == event.RunReceipt {
				receipt = &seen[len(seen)-1]
			}
		case <-deadline:
			t.Fatalf("the run emitted no terminal receipt:\n%s", gapLoopTrace(seen))
		}
	}

	if !hasKind(kindsOf(seen), event.ReviewContradiction) {
		t.Fatalf("premise: the run never reached the review-contradiction branch:\n%s", gapLoopTrace(seen))
	}
	attempts := startedAttempts(t, seen)
	if len(attempts) != 2 || attempts[0] == attempts[1] {
		t.Fatalf("premise: the run did not route A and then a distinct revision B: %v", attempts)
	}
	a, b := attempts[0], attempts[1]

	var r struct {
		Receipt struct {
			PlanDigest struct {
				Text string `json:"text"`
			} `json:"plan_digest"`
		} `json:"receipt"`
	}
	if err := json.Unmarshal(receipt.Payload, &r); err != nil {
		t.Fatal(err)
	}
	if got := r.Receipt.PlanDigest.Text; got != a {
		t.Fatalf("the terminal receipt names plan attempt %s, want operative %s (abandoned %s)", short12(got), short12(a), short12(b))
	}
	if e.operativePlanAttempt(task).ID != a || e.pendingPlanAttempt(task).ID != a {
		t.Fatalf("the operative state does not name A: operative %s pending %s",
			short12(e.operativePlanAttempt(task).ID), short12(e.pendingPlanAttempt(task).ID))
	}
	if g := e.testEditGrants(task); len(g) != 1 || g[0].Path != "main_test.go" {
		t.Fatalf("A's grant state was not reinstated: %+v", g)
	}
	e.mu.Lock()
	t2 := e.attempts[task]
	coverage, operativeCoverage := e.coverageWorlds[task], t2.operativeCoverageWorld
	e.mu.Unlock()
	if coverage != operativeCoverage {
		t.Fatalf("the coverage world is %q, not A's %q", coverage, operativeCoverage)
	}
	if routing, ok := e.routingFor(task); !ok || routing.PlanAttemptID != a {
		t.Fatalf("the routing read is not A's: %+v %v", routing, ok)
	}

	evs, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var proposed []string
	for _, ev := range evs {
		if ev.TaskID != task || ev.Kind != event.PlanProposed {
			continue
		}
		var p proposedPlan
		if err := json.Unmarshal(ev.Payload, &p); err != nil {
			t.Fatal(err)
		}
		proposed = append(proposed, p.PlanAttemptID)
	}
	if len(proposed) != 1 || proposed[0] != a {
		t.Fatalf("the durable operative transitions are %v, want exactly A's (%s)", proposed, short12(a))
	}
}

// breakStore makes every later append to store fail, as a session file that can
// no longer be written; mend undoes it.
func breakStore(t *testing.T, dir string) (mend func()) {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		// The record lock's own lock files, and the task invocation leases,
		// hold no events.
		if err == nil && d.IsDir() && (d.Name() == "locks" || d.Name() == "invocations") {
			return filepath.SkipDir
		}
		if err == nil && !d.IsDir() {
			files = append(files, p)
		}
		return err
	})
	if err != nil || len(files) != 1 {
		t.Fatalf("expected exactly one session file under %s: %v %v", dir, files, err)
	}
	saved, err := os.ReadFile(files[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(files[0]); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(files[0], 0o700); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if err := os.Remove(files[0]); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(files[0], saved, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func durableStore(t *testing.T) (*Engine, *session.Store, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := session.New(dir, "s1")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: store, SessionID: "s1", Bus: event.NewBus()}
	e.Repo.Root = t.TempDir()
	return e, store, dir
}

// A grant write that fails is not authority: nothing is installed for it, the
// previous operative attempt's grants do not stand in for it, its attempt is
// refused adoption, and nothing claims an operative plan the record lacks.
func TestAGrantStateThatCannotBeRecordedHoldsNoAuthority(t *testing.T) {
	e, store, dir := durableStore(t)
	const task = "task-grant-write-fails"
	planA := attemptPlan("plan A", teS)
	routeWithTestEdits(t, e, task, planA, []testEditGrant{{Path: teS, World: teWorld}})
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, task), task, attemptObjective, planA); err != nil {
		t.Fatal(err)
	}
	planB := attemptPlan("plan B", teS)
	rootFixtureTaskOnce(t, e, task, attemptObjective)
	b, err := e.beginPlanAttempt(fixtureCtx(e, task), task, attemptObjective, planB)
	if err != nil {
		t.Fatal(err)
	}
	mend := breakStore(t, dir)
	e.derivedCoverage(fixtureCtx(e, task), task, planB.Files, nil)
	if g := e.testEditGrants(task); len(g) != 0 {
		t.Fatalf("an unrecorded grant state left grants in force: %+v", g)
	}
	if p, ed := e.recordedGrants(task, b.ID); p.PlanAttemptID != "" || ed.PlanAttemptID != "" {
		t.Fatal("an unwritten grant record was kept as recorded authority")
	}
	if err := e.requireRecordedGrantState(task); err == nil || !strings.Contains(err.Error(), "not recorded durably") {
		t.Fatalf("routing would go on with an unrecorded grant state: %v", err)
	}
	mend()
	if _, err := e.adoptPlanAttempt(fixtureCtx(e, task), task, attemptObjective, planB); err == nil {
		t.Fatal("an attempt whose grant state was never recorded was made operative")
	}
	if op := e.operativePlanAttempt(task); op.ID == b.ID {
		t.Fatal("the unrecorded attempt became operative")
	}
	history, _ := store.Load()
	for _, ev := range history {
		if ev.Kind == event.PlanProposed && strings.Contains(string(ev.Payload), b.ID) {
			t.Fatal("a PlanProposed was recorded for the attempt whose grants were not")
		}
	}

	// The same failure on an AUTHORED grant write, which returns its error.
	e2, _, dir2 := durableStore(t)
	c := routeWithTestEdits(t, e2, task, attemptPlan("plan C", teS), nil)
	mend2 := breakStore(t, dir2)
	err = e2.recordTestEditGrants(fixtureCtx(e2, task), task, "authored", testEditRecord{PlanAttemptID: c.ID, World: teWorld, Grants: []testEditGrant{{Path: teS}}})
	mend2()
	if err == nil {
		t.Fatal("a failed grant write was reported as written")
	}
	if _, ed := e2.recordedGrants(task, c.ID); len(ed.Grants) != 0 {
		t.Fatal("the unwritten grant record replaced the recorded one")
	}
	if err := e2.requireRecordedGrantState(task); err == nil {
		t.Fatal("an attempt with a failed grant write may still be adopted")
	}
}

// A refusal whose record could not be written is not suppressed as though it
// had been: the caller gets the cause and the write failure. The failed
// governed append halts the task (70B2a1): once the store recovers, nothing
// more of the invocation -- no later refusal of the same attempt -- is
// recorded, and every later close returns the original failure beside its
// cause.
func TestAnAdmissionRefusalThatCannotBeRecordedIsNotSuppressed(t *testing.T) {
	e, store, dir := durableStore(t)
	const task = "task-refusal-write-fails"
	rootFixtureTaskOnce(t, e, task, attemptObjective)
	inv := liveInvocation(t, e, task)
	a, err := e.beginPlanAttempt(inv.ctx, task, attemptObjective, attemptPlan("plan A", teS))
	if err != nil {
		t.Fatal(err)
	}
	cause := refusePlanAdmission(refusalProspectiveAdmission, nil, errors.New("prospective admission refused before implementation: x"))
	mend := breakStore(t, dir)
	got := e.closePlanAdmission(fixtureCtx(e, task), task, cause)
	mend()
	if got == nil || !errors.Is(got, cause) || got.Error() == cause.Error() {
		t.Fatalf("the failed refusal write was not returned beside its cause: %v", got)
	}
	refusals := func() int {
		history, _ := store.Load()
		n := 0
		for _, ev := range history {
			if ev.Kind == event.PlanAttemptRefused && strings.Contains(string(ev.Payload), a.ID) {
				n++
			}
		}
		return n
	}
	if refusals() != 0 {
		t.Fatal("a refusal that could not be written is on the record")
	}
	halt := e.invocationFailure(inv)
	if halt == nil || halt.Kind != event.PlanAttemptRefused || !errors.Is(got, halt) {
		t.Fatalf("the failed refusal write did not halt the task with that failure: %v", halt)
	}
	if again := e.closePlanAdmission(fixtureCtx(e, task), task, cause); again == cause || !errors.Is(again, cause) || !errors.Is(again, halt) {
		t.Fatalf("a halted task's later refusal did not return the original failure beside its cause: %v", again)
	}
	if refusals() != 0 {
		t.Fatalf("a halted task recorded %d refusal(s) after the store recovered", refusals())
	}
	if e.invocationFailure(inv) != halt {
		t.Fatal("a later refusal replaced the original append failure")
	}
}

// Only a mechanically established refusal is PlanAttemptRefused. An instrument
// that could not answer, a response that could not be decoded, a provider or a
// persistence failure after the attempt began is returned as the failure it is,
// and never recorded as the plan being refused.
func TestOnlyAGenuineAdmissionRefusalIsRecordedAsOne(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-not-a-refusal"
	rootFixtureTaskOnce(t, e, task, attemptObjective)
	if _, err := e.beginPlanAttempt(fixtureCtx(e, task), task, attemptObjective, attemptPlan("plan A", teS)); err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{
		fmt.Errorf("Sensei scoped preflight: %w", errors.New("connection reset")),
		errors.New("preflight response carries no status"),
		&architectNotObtained{cause: errors.New("provider exited 1")},
		errors.New("the TestEditGranted record could not be written durably: disk full"),
		context.Canceled,
	} {
		if got := e.closePlanAdmission(fixtureCtx(e, task), task, cause); got != cause {
			t.Fatalf("a non-refusal was changed on its way back: %v -> %v", cause, got)
		}
	}
	history, _ := store.Load()
	if hasKind(kindsOf(history), event.PlanAttemptRefused) {
		t.Fatal("an operational failure was recorded as the plan being refused")
	}

	// Through the production path: Sensei's scoped preflight dies, or answers
	// with something that does not decode, after the attempt has begun.
	const caseLine = "\t*'\"name\":\"awareness_preflight\"}}')\n"
	at := strings.Index(gapLoopSenseiScript, caseLine)
	for name, body := range map[string]string{
		"transport": "\t\texit 0 ;;\n",
		"decode":    "\t\tresult='{\"content\":[{\"type\":\"text\",\"text\":\"garbled\"}],\"structuredContent\":{\"status\":42}}' ;;\n",
	} {
		t.Run(name, func(t *testing.T) {
			// Only the routing preflight over the planned file fails; the
			// start gate's preflight is answered as before.
			scoped := strings.Replace(caseLine, "\t*'", "\t*'\"main.go\"]'*'", 1)
			script := gapLoopSenseiScript[:at] + scoped + body + gapLoopSenseiScript[at:]
			e, architect, world := newGapLoopEngine(t, nil, sessionStore(t), gapProceed)
			e.Config.Sensei.Args = []string{"-c", script}
			taskID := "task-preflight-" + name
			run := driveGapLoop(t, e, architect, world, taskID, "", func(ctx context.Context) {
				e.run(ctx, taskID, attemptObjective, RequestedByHuman)
			})
			kinds := kindsOf(run.events)
			if !hasKind(kinds, event.PlanAttemptStarted) {
				t.Fatalf("the attempt never began, so this proves nothing:\n%s", gapLoopTrace(run.events))
			}
			if hasKind(kinds, event.PlanProposed) {
				t.Fatalf("the plan was admitted although preflight failed:\n%s", gapLoopTrace(run.events))
			}
			if hasKind(kinds, event.PlanAttemptRefused) {
				t.Fatalf("a preflight failure was recorded as the plan being refused:\n%s", gapLoopTrace(run.events))
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Objective 61 (P2 residual): A PLAN-LEVEL ADMISSION REFUSAL TERMINATES THE PLAN
// ATTEMPT, NOT THE GOVERNED TASK. The architect receives the typed refusal and
// may produce a different lawful plan; no implementer starts under the refused
// plan; a materially identical refused plan returned again ends the invocation,
// named and resumable, and is not requested a third time.
// ---------------------------------------------------------------------------

// drivePlanAdmission starts a run and follows it to its ending, read by
// membership in the closed run-terminality vocabulary, so every governed ending
// -- PLAN_ADMISSION_REFUSED included -- ends the drive. A human question is
// deferred, so the run always ends.
func drivePlanAdmission(t *testing.T, e *Engine, architect *scriptedArchitect, world, taskID string, start func(context.Context)) gapLoopRun {
	t.Helper()
	ch, cancel := e.Bus.Subscribe(8192)
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go start(ctx)
	run := gapLoopRun{engine: e, world: world}
	timeout := time.After(90 * time.Second)
	for {
		select {
		case ev := <-ch:
			run.events = append(run.events, ev)
			if ev.Kind == event.AuthorityRequired {
				waitForPending(t, e, taskID)
				e.DeferAuthority(taskID)
			}
			if _, ok := event.RunTerminality(ev.Kind); ok {
				settle := time.After(300 * time.Millisecond)
				for {
					select {
					case ev := <-ch:
						run.events = append(run.events, ev)
					case <-settle:
						run.prompts = append(run.prompts, architect.prompts...)
						return run
					}
				}
			}
		case <-timeout:
			stop()
			t.Fatalf("the governed run did not end:\n%s", gapLoopTrace(run.events))
		}
	}
}

const (
	// obj61Refused is the Objective-59 run-3 shape: a new regression test
	// declared with a covering surface, for which no canonical grant is derived.
	obj61Refused = `{"decision":"proceed","summary":"reconcile coverage","plan":"plan A: a new coverage reconciliation test","files":["main.go","extra/coverage_reconciliation_test.go"],"mode":"modify",` +
		`"prospective_surfaces":[{"path":"extra/coverage_reconciliation_test.go","package":"extra","role":"go-regression-test","covering":"main.go","dependencies":["testing"]}]}`
	// obj61RefusedAgain is a materially different plan with the same refused
	// declaration: a different canonical PlanAttemptID.
	obj61RefusedAgain = `{"decision":"proceed","summary":"reconcile coverage","plan":"plan A2: the same new test, argued differently","files":["main.go","extra/coverage_reconciliation_test.go"],"mode":"modify",` +
		`"prospective_surfaces":[{"path":"extra/coverage_reconciliation_test.go","package":"extra","role":"go-regression-test","covering":"main.go","dependencies":["testing"]}]}`
	// obj61RefusedWithEdit is obj61Refused that also edits the existing test
	// beside main.go, so routing derives a real test-edit grant for it before
	// prospective admission refuses it.
	obj61RefusedWithEdit = `{"decision":"proceed","summary":"reconcile coverage","plan":"plan A: a new test and an edit","files":["main.go","main_test.go","extra/coverage_reconciliation_test.go"],"mode":"modify",` +
		`"test_edits":[{"path":"main_test.go","operation":"edit","package":"main","build_constraints":[],"imports":["testing"]}],` +
		`"prospective_surfaces":[{"path":"extra/coverage_reconciliation_test.go","package":"extra","role":"go-regression-test","covering":"main.go","dependencies":["testing"]}]}`
	// planAdmissionRefusedMarker is planAdmissionRefusalPrompt's own heading.
	planAdmissionRefusedMarker = "PLAN ADMISSION REFUSED"
	// obj61OwedHeading introduces the owed refusal in restoredRefusalsPrompt.
	obj61OwedHeading = "This task is owed an architect turn for this refusal, which was recorded before the previous invocation ended:"
)

// obj61Run drives one fresh governed run in the grant world whose architect
// answers turns in order (the last repeated).
func obj61Run(t *testing.T, store *session.Store, taskID string, turns ...string) (gapLoopRun, *scriptedArchitect) {
	t.Helper()
	e, architect, _ := newGapLoopEngine(t, nil, store, turns...)
	world := grantWorld(t, e)
	e.Config.Sensei.Args = []string{"-c", regionScript(objectiveRunScript, "main.go", "main_test.go")}
	return drivePlanAdmission(t, e, architect, world, taskID, func(ctx context.Context) {
		e.run(ctx, taskID, attemptObjective, RequestedByHuman)
	}), architect
}

// obj61CoveredRun drives one fresh governed run in the grant world extended
// with a second existing test, other/other_test.go beside other/other.go,
// whose deriving stand-in covers exactly covered -- so the production coverage
// path derives a test-edit grant beside each covered file and none elsewhere.
func obj61CoveredRun(t *testing.T, taskID string, covered, region []string, turns ...string) (gapLoopRun, *scriptedArchitect) {
	t.Helper()
	e, architect, _ := newGapLoopEngine(t, nil, sessionStore(t), turns...)
	commitFixtureFile(t, e.Repo.Root, "other/other.go", "package other\n")
	commitFixtureFile(t, e.Repo.Root, "other/other_test.go", "package other\n\nimport \"testing\"\n\nfunc TestOther(t *testing.T) {}\n")
	world := grantWorld(t, e)
	subjects := make([]string, 0, len(covered))
	for _, f := range covered {
		subjects = append(subjects, fmt.Sprintf(`{"file":"%s"}`, f))
	}
	bin := filepath.Join(t.TempDir(), "sensei")
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -revision ] && rev=\"$2\"; shift; done\n" +
		"printf '{\"result\":\"DERIVED\",\"pinned_commit\":\"%s\",\"subjects\":[" + strings.Join(subjects, ",") + "]}' \"$rev\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSEI_BIN", bin)
	e.Config.Sensei.Args = []string{"-c", regionScript(objectiveRunScript, region...)}
	return drivePlanAdmission(t, e, architect, world, taskID, func(ctx context.Context) {
		e.run(ctx, taskID, attemptObjective, RequestedByHuman)
	}), architect
}

// refusalsIn are the PlanAttemptRefused payloads of a run, in order.
func refusalsIn(t *testing.T, evs []event.Event) []planAttemptRefusal {
	t.Helper()
	var out []planAttemptRefusal
	for _, ev := range evs {
		if ev.Kind == event.PlanAttemptRefused {
			var r planAttemptRefusal
			if err := json.Unmarshal(ev.Payload, &r); err != nil {
				t.Fatal(err)
			}
			out = append(out, r)
		}
	}
	return out
}

// implementerStartedBefore reports whether any implementer was assigned or any
// agent process started before the event at index end.
func implementerStartedBefore(evs []event.Event, end int) bool {
	for _, ev := range evs[:end] {
		if ev.Kind == event.AgentStarted {
			return true
		}
		if ev.Kind == event.RoleAssigned && !strings.Contains(ev.Summary, "architect role") {
			return true
		}
	}
	return false
}

func indexOfKind(evs []event.Event, k event.Kind) int {
	for i, ev := range evs {
		if ev.Kind == k {
			return i
		}
	}
	return -1
}

// requireRefusalEnvelope asserts that prompt carries, after heading, the
// complete typed refusal want -- the durable record as written -- and that the
// record decoded back out of the prompt is exactly that record, every identity
// in full: TaskID, PlanAttemptID, RefusalID, GoverningEvidenceID, class, exact
// declaration, reason and recorded continuation.
func requireRefusalEnvelope(t *testing.T, prompt, heading string, want planAttemptRefusal) {
	t.Helper()
	if want.TaskID == "" || want.PlanAttemptID == "" || want.RefusalID == "" || want.GoverningEvidenceID == "" || want.Class == "" ||
		len(want.Declaration) == 0 || want.Reason == "" || want.Continuation != session.PlanAdmissionContinuationArchitectTurn {
		t.Fatalf("premise: the durable refusal is not complete: %+v", want)
	}
	at := strings.Index(prompt, heading+"\n"+planAdmissionRefusalEnvelope(want))
	if at < 0 {
		t.Fatalf("the architect was not shown the complete typed refusal %s of %s:\n%s", short12(want.RefusalID), short12(want.PlanAttemptID), prompt)
	}
	var got planAttemptRefusal
	if err := json.NewDecoder(strings.NewReader(prompt[at+len(heading)+1:])).Decode(&got); err != nil {
		t.Fatalf("the refusal shown to the architect does not decode: %v", err)
	}
	g, _ := json.Marshal(got)
	w, _ := json.Marshal(want)
	if string(g) != string(w) {
		t.Fatalf("the refusal shown to the architect is not the durable record:\n got %s\nwant %s", g, w)
	}
}

// W1 (Objective 61): the Objective-59 run-3 shape. The ungranted declaration
// is refused before any agent starts, the architect receives the typed refusal,
// its second plan uses a lawful witness shape (an existing test edited under a
// test-edit grant), and the same governed task proceeds past admission.
func TestObj61W1ARefusedPlanReturnsToTheArchitectAndTheTaskProceeds(t *testing.T) {
	const task = "task-obj61-w1"
	run, architect := obj61Run(t, sessionStore(t), task, obj61Refused, replanA)
	refusals := refusalsIn(t, run.events)
	attempts := startedAttempts(t, run.events)
	if len(refusals) != 1 || len(attempts) != 2 || refusals[0].PlanAttemptID != attempts[0] || attempts[0] == attempts[1] {
		t.Fatalf("premise: plan A refused once, plan B a new attempt: attempts %v refusals %+v\n%s", attempts, refusals, gapLoopTrace(run.events))
	}
	r := refusals[0]
	if r.Class != refusalProspectiveAdmission || !strings.Contains(r.Reason, "holds no recorded grant") ||
		!strings.Contains(string(r.Declaration), "extra/coverage_reconciliation_test.go") || r.RefusalID != planAdmissionRefusalID(r) || r.GoverningEvidenceID == "" {
		t.Fatalf("the refusal is not typed with its class, declaration, reason and identities: %+v", r)
	}
	refusedAt := indexOfKind(run.events, event.PlanAttemptRefused)
	if implementerStartedBefore(run.events, refusedAt) {
		t.Fatalf("an agent started before plan admission refused the plan:\n%s", gapLoopTrace(run.events))
	}
	if len(architect.prompts) != 2 || strings.Contains(architect.prompts[0], planAdmissionRefusedMarker) ||
		!strings.Contains(architect.prompts[1], planAdmissionRefusedMarker) {
		t.Fatalf("the architect did not receive the typed refusal as bounded evidence on its second turn: %d prompts", len(architect.prompts))
	}
	requireRefusalEnvelope(t, architect.prompts[1], "The typed refusal, exactly as recorded:\n", r)
	proposed := indexOfKind(run.events, event.PlanProposed)
	if proposed < 0 {
		t.Fatalf("the replacement plan did not become operative:\n%s", gapLoopTrace(run.events))
	}
	var p proposedPlan
	_ = json.Unmarshal(run.events[proposed].Payload, &p)
	if p.PlanAttemptID != attempts[1] || implementerStartedBefore(run.events, proposed) {
		t.Fatalf("the operative plan is not the replacement attempt, or work started before it: %q", p.PlanAttemptID)
	}
	for _, ev := range run.events {
		if ev.TaskID != "" && ev.TaskID != task {
			t.Fatalf("the replacement ran under another task: %s", ev.TaskID)
		}
		if ev.Kind == event.WorkflowFailed && strings.Contains(ev.Summary, "admission refused") {
			t.Fatalf("the plan refusal ended the task: %s", ev.Summary)
		}
		if ev.Kind == event.WorkflowPlanAdmissionRefused {
			t.Fatal("a different lawful plan was parked as a repeated refusal")
		}
	}
}

// W2 (Objective 61): BOUNDED REPEAT. The first refused plan returns to the
// architect; the materially identical refused plan returned under the same
// world and evidence ends the invocation PLAN_ADMISSION_REFUSED, resumable,
// and no third identical plan is requested.
func TestObj61W2ARepeatedIdenticalRefusalEndsTheInvocationNamed(t *testing.T) {
	const task = "task-obj61-w2"
	store := sessionStore(t)
	run, architect := obj61Run(t, store, task, obj61Refused)
	if len(architect.prompts) != 2 {
		t.Fatalf("the architect was asked %d times; the bound is one re-plan and no third identical attempt:\n%s", len(architect.prompts), gapLoopTrace(run.events))
	}
	last := run.events[len(run.events)-1]
	terminal := indexOfKind(run.events, event.WorkflowPlanAdmissionRefused)
	if terminal < 0 || hasKind(kindsOf(run.events), event.WorkflowFailed) || hasKind(kindsOf(run.events), event.PlanProposed) {
		t.Fatalf("the repeated refusal did not end the invocation as PLAN_ADMISSION_REFUSED (last %s):\n%s", last.Kind, gapLoopTrace(run.events))
	}
	if implementerStartedBefore(run.events, len(run.events)) {
		t.Fatal("an admission refusal was converted into implementer work")
	}
	refusals := refusalsIn(t, run.events)
	attempts := startedAttempts(t, run.events)
	if len(refusals) != 1 || len(attempts) != 2 || attempts[0] != attempts[1] {
		t.Fatalf("the identical refusal is one refusal of one attempt: attempts %v refusals %d", attempts, len(refusals))
	}
	var parked PlanAdmissionRefused
	if err := json.Unmarshal(run.events[terminal].Payload, &parked); err != nil || parked.RefusalID != refusals[0].RefusalID ||
		parked.PlanAttemptID != attempts[0] || parked.Class != refusalProspectiveAdmission {
		t.Fatalf("the terminal does not name the refusal it ended on: %+v (%v)", parked, err)
	}
	// The receipt, emitted BEFORE the terminal event, names the outcome and,
	// by itself, the exact canonical refusal the invocation parked on. (This
	// rig's binary carries no VCS stamp and its stub no graph digest; those
	// gaps are the rig's, and are not what is asserted.)
	var receipt struct {
		Receipt struct {
			Outcome string `json:"outcome"`
			Refusal *struct {
				State               string `json:"state"`
				Source              string `json:"source"`
				PlanAttemptID       string `json:"plan_attempt_id"`
				RefusalID           string `json:"refusal_id"`
				Class               string `json:"refusal_class"`
				Declaration         string `json:"declaration"`
				Reason              string `json:"reason"`
				GoverningEvidenceID string `json:"governing_evidence_id"`
			} `json:"plan_admission_refusal"`
		} `json:"receipt"`
		Missing []string `json:"missing"`
	}
	receipts := 0
	for _, ev := range run.events[:terminal] {
		if ev.Kind == event.RunReceipt {
			receipts++
			_ = json.Unmarshal(ev.Payload, &receipt)
		}
	}
	if receipts != 1 || receipt.Receipt.Outcome != "PLAN_ADMISSION_REFUSED" || strings.Contains(strings.Join(receipt.Missing, " "), "outcome") {
		t.Fatalf("the receipt does not record the named outcome before the terminal: %d receipts %+v missing %v", receipts, receipt.Receipt, receipt.Missing)
	}
	got, want := receipt.Receipt.Refusal, refusals[0]
	if got == nil || got.State != "KNOWN" || got.Source == "" || got.PlanAttemptID != want.PlanAttemptID || got.RefusalID != want.RefusalID ||
		got.Class != string(want.Class) || got.Declaration != string(want.Declaration) || got.Reason != want.Reason ||
		got.GoverningEvidenceID != want.GoverningEvidenceID {
		t.Fatalf("the receipt does not carry the exact canonical refusal it parked on:\n got %+v\nwant %+v", got, want)
	}
	if strings.Contains(strings.Join(receipt.Missing, " "), "plan_admission_refusal") {
		t.Fatalf("the receipt's refusal fact is incomplete: %v", receipt.Missing)
	}
	// The TASK is not ended: it is still owed, planned by nothing, and the
	// refusal it is owed a turn for is the durable record of the parked one.
	found := reconstructed(t, store, task)
	var owed planAttemptRefusal
	if found.Planned || found.PlanAttemptID != "" || len(found.PlanAttemptRefusals[parked.RefusalID]) == 0 ||
		json.Unmarshal(found.PlanAdmissionRefused, &owed) != nil || owed.RefusalID != parked.RefusalID || owed.PlanAttemptID != attempts[0] {
		t.Fatalf("the parked task is not resumable with its owed refusal bound to its attempt: %+v", found)
	}
}

// W2, reset: a materially different plan is a different canonical attempt and
// so not the same refusal -- it returns to the architect again; only ITS
// identical repetition parks.
func TestObj61W2AMateriallyDifferentPlanResetsTheRepeatIdentity(t *testing.T) {
	const task = "task-obj61-w2-reset"
	run, architect := obj61Run(t, sessionStore(t), task, obj61Refused, obj61RefusedAgain, obj61RefusedAgain)
	attempts := startedAttempts(t, run.events)
	refusals := refusalsIn(t, run.events)
	if len(architect.prompts) != 3 || len(attempts) != 3 || attempts[0] == attempts[1] || attempts[1] != attempts[2] ||
		len(refusals) != 2 || !hasKind(kindsOf(run.events), event.WorkflowPlanAdmissionRefused) {
		t.Fatalf("a different plan did not reset the identity, or its repeat did not park: prompts %d attempts %v refusals %d\n%s",
			len(architect.prompts), attempts, len(refusals), gapLoopTrace(run.events))
	}
}

// W2, new evidence: the same attempt and the same reason, decided against a
// different recorded grant state, a re-certified graph identity or a changed
// scoped preflight answer, is not the same refusal; the repetition under
// unchanged evidence is.
func TestObj61W2NewGovernedEvidenceResetsTheRepeatIdentity(t *testing.T) {
	e, _ := attemptEngine(t)
	const task = "task-obj61-w2-evidence"
	d := attemptPlan("plan A", teS, teF)
	refused := refusePlanAdmission(refusalTestEditAdmission, d.TestEdits, errors.New("existing-test edit admission refused before implementation: x"))
	route := func(grants []testEditGrant) (planAttemptRefusal, error) {
		t.Helper()
		routeWithTestEdits(t, e, task, d, grants)
		return e.continueAfterAdmissionRefusal(fixtureCtx(e, task), task, refused)
	}
	first, err := route(nil)
	if err != nil || first.RefusalID == "" {
		t.Fatalf("the first refusal was not returned to the architect: %v", err)
	}
	second, err := route(teEditGrants(t))
	if err != nil || second.PlanAttemptID != first.PlanAttemptID || second.RefusalID == first.RefusalID {
		t.Fatalf("a newly recorded grant state was not new evidence: %v %+v", err, second)
	}
	var parked *PlanAdmissionRefused
	if _, err := route(teEditGrants(t)); !errors.As(err, &parked) || parked.RefusalID != second.RefusalID {
		t.Fatalf("the identical refusal under unchanged evidence was not parked: %v", err)
	}
	// The same attempt, declaration, reason, world and grant records, decided
	// under a RE-CERTIFIED graph identity: new governed evidence. The first
	// refusal under it returns to the architect; only its unchanged repeat
	// parks.
	e.mu.Lock()
	e.graphs = map[string]*agent.GraphBinding{task: {Digest: strings.Repeat("a", 40)}}
	e.mu.Unlock()
	third, err := route(teEditGrants(t))
	if err != nil || third.PlanAttemptID != second.PlanAttemptID || third.Reason != second.Reason ||
		string(third.Declaration) != string(second.Declaration) || third.RefusalID == second.RefusalID {
		t.Fatalf("a re-certified graph identity was not new governed evidence: %v %+v", err, third)
	}
	if _, err := route(teEditGrants(t)); !errors.As(err, &parked) || parked.RefusalID != third.RefusalID {
		t.Fatalf("the identical refusal under the re-certified graph was not parked: %v", err)
	}
	// And under a changed scoped preflight answer for the same attempt.
	scopedRoute := func() (planAttemptRefusal, error) {
		t.Helper()
		routeWithTestEdits(t, e, task, d, teEditGrants(t))
		e.mu.Lock()
		scoped := e.planAttemptsOf(task).scopedPreflight[third.PlanAttemptID]
		e.mu.Unlock()
		scoped.RiskClass = "HIGH_RISK"
		e.noteScopedPreflight(task, scoped)
		return e.continueAfterAdmissionRefusal(fixtureCtx(e, task), task, refused)
	}
	fourth, err := scopedRoute()
	if err != nil || fourth.PlanAttemptID != third.PlanAttemptID || fourth.RefusalID == third.RefusalID {
		t.Fatalf("a changed scoped preflight answer was not new governed evidence: %v %+v", err, fourth)
	}
	if _, err := scopedRoute(); !errors.As(err, &parked) || parked.RefusalID != fourth.RefusalID {
		t.Fatalf("the identical refusal under the changed scoped answer was not parked: %v", err)
	}
	// Classes that do not establish only "this plan" are routed as before.
	for _, c := range []planAdmissionRefusalClass{refusalAuthorityDeclined, refusalSuppliedPlan, "unknown"} {
		cause := refusePlanAdmission(c, nil, errors.New("declined"))
		if _, err := e.continueAfterAdmissionRefusal(fixtureCtx(e, task), task, cause); err != cause {
			t.Errorf("class %s was continued: %v", c, err)
		}
	}
	plain := errors.New("Sensei scoped preflight: connection reset")
	if _, err := e.continueAfterAdmissionRefusal(fixtureCtx(e, task), task, plain); err != plain {
		t.Fatalf("an operational failure was read as a plan refusal: %v", err)
	}
}

// W3 (Objective 61): the replacement plan's grants are derived fresh. The
// refused plan held a real derived test-edit grant; its replacement declares
// none, and nothing of the refused attempt's grant state reaches it -- in
// memory, in the record bound to it, or in the operative plan.
func TestObj61W3AReplacementPlanDerivesItsAuthorityFresh(t *testing.T) {
	const task = "task-obj61-w3"
	run, _ := obj61Run(t, sessionStore(t), task, obj61RefusedWithEdit, replanNoGrants)
	e := run.engine
	attempts := startedAttempts(t, run.events)
	if len(attempts) != 2 || len(refusalsIn(t, run.events)) != 1 {
		t.Fatalf("premise: one refused attempt and one replacement: %v\n%s", attempts, gapLoopTrace(run.events))
	}
	_, refusedEdits := e.recordedGrants(task, attempts[0])
	if len(refusedEdits.Grants) != 1 || refusedEdits.Grants[0].Path != "main_test.go" {
		t.Fatalf("premise: the refused attempt held a derived test-edit grant: %+v", refusedEdits)
	}
	p, ed := e.recordedGrants(task, attempts[1])
	if ed.PlanAttemptID != attempts[1] || p.PlanAttemptID != attempts[1] || len(ed.Grants) != 0 || len(p.Grants) != 0 {
		t.Fatalf("the replacement's grant state is not its own fresh, empty derivation: %+v %+v", p, ed)
	}
	if op := e.operativePlanAttempt(task); op.ID != attempts[1] {
		t.Fatalf("the operative attempt is not the replacement: %s", op.ID)
	}
	if g := e.testEditGrants(task); len(g) != 0 {
		t.Fatalf("the refused attempt's grant reached the replacement: %+v", g)
	}
}

// W7 (Objective 61): DURABLE REFUSAL RESUME. A parked task, interrupted after
// its refusal and park were durably persisted, is reconstructed in a fresh
// session view and resumed by a fresh engine at its architect turn, with the
// exact owed refusal -- its PlanAttemptID and RefusalID, from the durable
// record -- as evidence. No implementer starts before a plan is admitted; no
// authority is restored or minted from the refused attempt; the identical plan
// parks at once (it is the second occurrence) naming the same refusal, and a
// different lawful plan proceeds.
func TestObj61W7AResumePresentsTheOwedRefusalAndRestoresNoAuthority(t *testing.T) {
	const task = "task-obj61-resume"
	store := sessionStore(t)
	first, _ := obj61Run(t, store, task, obj61Refused)
	refused := startedAttempts(t, first.events)[0]
	recorded := refusalsIn(t, first.events)
	if len(recorded) != 1 {
		t.Fatalf("premise: one durable refusal: %+v", recorded)
	}
	found := reconstructed(t, store, task)
	var owed planAttemptRefusal
	if json.Unmarshal(found.PlanAdmissionRefused, &owed) != nil || owed.PlanAttemptID != refused || owed.RefusalID != recorded[0].RefusalID {
		t.Fatalf("FindInterrupted does not expose the exact owed refusal: %s", found.PlanAdmissionRefused)
	}

	for name, tc := range map[string]struct {
		turn    string
		parks   bool
		prompts int
	}{
		"identical plan": {obj61Refused, true, 1},
		"lawful plan":    {replanA, false, 1},
	} {
		t.Run(name, func(t *testing.T) {
			resumed := sessionStore(t)
			history, _ := store.Load()
			for _, ev := range history {
				if err := seedAppend(t, resumed, ev); err != nil {
					t.Fatal(err)
				}
			}
			next, architect, _ := newGapLoopEngine(t, &gapLoopRun{engine: first.engine, world: first.world}, resumed, tc.turn)
			next.Config.Sensei.Args = first.engine.Config.Sensei.Args
			run := drivePlanAdmission(t, next, architect, first.world, task, func(ctx context.Context) { next.Resume(ctx, found) })
			if len(architect.prompts) != tc.prompts || !strings.Contains(architect.prompts[0], "RECORDED PLAN-ADMISSION REFUSALS") {
				t.Fatalf("the resumed architect turn was not shown the recorded refusals (%d prompts)", len(architect.prompts))
			}
			requireRefusalEnvelope(t, architect.prompts[0], obj61OwedHeading, recorded[0])
			if got := hasKind(kindsOf(run.events), event.WorkflowPlanAdmissionRefused); got != tc.parks {
				t.Fatalf("parked=%v, want %v:\n%s", got, tc.parks, gapLoopTrace(run.events))
			}
			admitted := indexOfKind(run.events, event.PlanProposed)
			if admitted < 0 {
				admitted = len(run.events)
			}
			if implementerStartedBefore(run.events, admitted) {
				t.Fatalf("an implementer started on resume before any plan was admitted:\n%s", gapLoopTrace(run.events))
			}
			if tc.parks {
				var again planAttemptRefusal
				if at := indexOfKind(run.events, event.WorkflowPlanAdmissionRefused); json.Unmarshal(run.events[at].Payload, &again) != nil ||
					again.RefusalID != owed.RefusalID || again.PlanAttemptID != refused || len(refusalsIn(t, run.events)) != 0 {
					t.Fatalf("the resumed repeat did not park on the owed refusal: %+v", again)
				}
			}
			for _, ev := range run.events {
				if ev.Kind == event.PlanProposed && strings.Contains(string(ev.Payload), refused) {
					t.Fatal("the refused attempt was made operative on resume")
				}
			}
			if !tc.parks && !hasKind(kindsOf(run.events), event.PlanProposed) {
				t.Fatalf("the lawful plan did not proceed on resume:\n%s", gapLoopTrace(run.events))
			}
		})
	}
}

// W7, planned task (Objective 61): a task with an OPERATIVE plan whose in-cycle
// architect re-plan receives its FIRST admission refusal, interrupted after
// that refusal is durably recorded and before any replacement answer, still
// owes the architect that refusal. FindInterrupted names it with its exact
// PlanAttemptID and RefusalID beside the older operative plan; a fresh engine's
// Resume routes the obligation to the architect, never to an implementer under
// the older plan; the identical answer parks naming the same refusal, and a
// lawful replacement is admitted through its own fresh attempt.
func TestObj61W7BAFirstReplanRefusalOfAPlannedTaskIsOwedAcrossInterruption(t *testing.T) {
	requireGofmt(t)
	const task = "task-obj61-w7b"
	store := sessionStore(t)
	e, _, _ := newGapLoopEngine(t, nil, store, replanA)
	world := grantWorld(t, e)
	e.Config.Sensei.Args = []string{"-c", regionScript(objectiveRunScript, "main.go", "main_test.go")}
	e.Config.Permissions.WriteCandidates, e.Config.Permissions.CreateWorktrees = true, true
	e.Config.Permissions.RunFormatters, e.Config.Permissions.LocalCommit = true, true
	e.Config.Validation = formattingValidation()
	e.Config.Workflow.ReviewCycles = 1
	worker := e.Config.Architect
	worker.Command, worker.Args = stubProcess(t, "implementor", "")
	e.Config.Implementors = []config.Agent{worker}
	e.Config.Reviewer = e.Config.Architect
	e.Config.Reviewer.Name = "codex"
	architect := &scriptedArchitect{turns: []architectTurn{{text: replanA}, {text: obj61Refused}, {text: obj61Refused}}}
	e.Runners = objectiveRoles{architect: architect, reviewer: escalatingReviewer, bindings: make(chan RunnerSpec, 8)}
	first := drivePlanAdmission(t, e, architect, world, task, func(ctx context.Context) {
		e.run(ctx, task, attemptObjective, RequestedByHuman)
	})
	attempts := startedAttempts(t, first.events)
	refusals := refusalsIn(t, first.events)
	proposed := indexOfKind(first.events, event.PlanProposed)
	refusedAt := indexOfKind(first.events, event.PlanAttemptRefused)
	if len(attempts) < 2 || len(refusals) != 1 || proposed < 0 || refusedAt < proposed || refusals[0].PlanAttemptID == attempts[0] {
		t.Fatalf("premise: plan A operative, then an in-cycle re-plan refused once at admission: attempts %v refusals %+v\n%s",
			attempts, refusals, gapLoopTrace(first.events))
	}
	operative, owedRefusal := attempts[0], refusals[0]

	// THE INTERRUPTION: the durable record ends at the first refusal, before
	// the architect answered it.
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cut := -1
	for i, ev := range history {
		if ev.TaskID == task && ev.Kind == event.PlanAttemptRefused {
			cut = i
			break
		}
	}
	if cut < 0 {
		t.Fatal("premise: the refusal was not durably recorded")
	}
	interrupted := func(t *testing.T) *session.Store {
		t.Helper()
		s := sessionStore(t)
		for _, ev := range history[:cut+1] {
			if err := seedAppend(t, s, ev); err != nil {
				t.Fatal(err)
			}
		}
		return s
	}
	found := reconstructed(t, interrupted(t), task)
	var owed planAttemptRefusal
	if !found.Planned || found.PlanAttemptID != operative || json.Unmarshal(found.PlanAdmissionRefused, &owed) != nil ||
		owed.PlanAttemptID != owedRefusal.PlanAttemptID || owed.RefusalID != owedRefusal.RefusalID {
		t.Fatalf("FindInterrupted does not name the first refusal as owed beside the operative plan: planned %v operative %s owed %s",
			found.Planned, short12(found.PlanAttemptID), found.PlanAdmissionRefused)
	}

	for name, tc := range map[string]struct {
		turn  string
		parks bool
	}{
		"identical answer":   {obj61Refused, true},
		"lawful replacement": {replanSamePaths, false},
	} {
		t.Run(name, func(t *testing.T) {
			next, _, _ := newGapLoopEngine(t, &gapLoopRun{engine: e, world: world}, interrupted(t), tc.turn)
			next.Config = e.Config
			answer := &scriptedArchitect{turns: []architectTurn{{text: tc.turn}, {text: tc.turn}, {text: tc.turn}}}
			next.Runners = objectiveRoles{architect: answer, reviewer: escalatingReviewer, bindings: make(chan RunnerSpec, 8)}
			run := drivePlanAdmission(t, next, answer, world, task, func(ctx context.Context) { next.Resume(ctx, found) })
			if len(answer.prompts) == 0 || !strings.Contains(answer.prompts[0], "RECORDED PLAN-ADMISSION REFUSALS") {
				t.Fatalf("the resumed architect turn was not shown the recorded refusals (%d prompts):\n%s", len(answer.prompts), gapLoopTrace(run.events))
			}
			requireRefusalEnvelope(t, answer.prompts[0], obj61OwedHeading, owedRefusal)
			admitted := indexOfKind(run.events, event.PlanProposed)
			if admitted < 0 {
				admitted = len(run.events)
			}
			if implementerStartedBefore(run.events, admitted) {
				t.Fatalf("an implementer started under the older plan before any replacement was admitted:\n%s", gapLoopTrace(run.events))
			}
			if got := hasKind(kindsOf(run.events), event.WorkflowPlanAdmissionRefused); got != tc.parks {
				t.Fatalf("parked=%v, want %v:\n%s", got, tc.parks, gapLoopTrace(run.events))
			}
			if tc.parks {
				var again planAttemptRefusal
				at := indexOfKind(run.events, event.WorkflowPlanAdmissionRefused)
				if json.Unmarshal(run.events[at].Payload, &again) != nil || again.RefusalID != owed.RefusalID ||
					again.PlanAttemptID != owed.PlanAttemptID || len(answer.prompts) != 1 || hasKind(kindsOf(run.events), event.PlanProposed) {
					t.Fatalf("the identical answer did not park on the owed refusal: %+v (%d prompts)", again, len(answer.prompts))
				}
				return
			}
			var p proposedPlan
			_ = json.Unmarshal(run.events[admitted].Payload, &p)
			fresh := startedAttempts(t, run.events)
			if len(fresh) == 0 || p.PlanAttemptID != fresh[len(fresh)-1] || p.PlanAttemptID == operative || p.PlanAttemptID == owed.PlanAttemptID {
				t.Fatalf("the replacement was not admitted through its own fresh attempt: %q (started %v)", p.PlanAttemptID, fresh)
			}
			if _, ed := next.recordedGrants(task, p.PlanAttemptID); ed.PlanAttemptID != p.PlanAttemptID {
				t.Fatalf("the replacement's authority was not derived under its own attempt: %+v", ed)
			}
		})
	}
}

// W5 (Objective 61) CONTROL: the continuation is ONE shared boundary. routePlan
// still reconciles exactly as DF-37 has it and decides no continuation; both
// architect routing paths reach the boundary; nothing at it names a file, an
// error text or a caller; and inspectProspective is untouched by the boundary.
func TestObj61W5TheContinuationIsOneSharedBoundary(t *testing.T) {
	route := sourceOf(t, "internal/workflow/engine.go", "routePlan")
	if strings.Contains(route, "continueAfterAdmissionRefusal") || strings.Count(route, "e.reconcileProspectiveGrants(") != 1 {
		t.Fatal("routePlan decides a continuation, or no longer reconciles prospective grants exactly once")
	}
	ask := sourceOf(t, "internal/workflow/engine.go", "askArchitect")
	if strings.Count(ask, "e.continueAfterAdmissionRefusal(ctx, taskID, err)") != 2 {
		t.Fatal("both architect routing paths (proceed and escalate) must reach the one shared boundary")
	}
	boundary := sourceOf(t, "internal/workflow/engine.go", "continueAfterAdmissionRefusal")
	for _, special := range []string{"_test.go", "coverage_reconciliation", "prospective admission refused", "holds no recorded grant", "resolveSuppliedPlan"} {
		if strings.Contains(boundary, special) {
			t.Errorf("the shared boundary special-cases %q", special)
		}
	}
	if strings.Contains(sourceOf(t, "internal/workflow/prospective.go", "inspectProspective"), "PlanAdmission") {
		t.Fatal("candidate inspection reads plan-admission continuation state")
	}
}

// W8 (Objective 61): MULTIPLE REFUSALS, through the production recorder, the
// session projection and the resume restorer. One plan attempt is refused
// twice under different governing evidence -- two canonical RefusalIDs -- and
// the invocation parks on the FIRST; both durable records stay distinguishable,
// and the owed refusal a fresh engine restores is the parked one, not the one
// written last.
func TestObj61W8DistinctRefusalsOfOneAttemptStayDistinctAndTheOwedOneIsParked(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-obj61-w8"
	if err := seedAppend(t, store, event.New("s1", task, event.SourceSystem, event.TaskCreated, attemptObjective, nil)); err != nil {
		t.Fatal(err)
	}
	d := attemptPlan("plan A", teS, teF)
	refused := refusePlanAdmission(refusalTestEditAdmission, d.TestEdits, errors.New("existing-test edit admission refused before implementation: x"))
	route := func(grants []testEditGrant) (planAttemptRefusal, error) {
		t.Helper()
		routeWithTestEdits(t, e, task, d, grants)
		return e.continueAfterAdmissionRefusal(fixtureCtx(e, task), task, refused)
	}
	first, err := route(nil)
	if err != nil {
		t.Fatal(err)
	}
	second, err := route(teEditGrants(t))
	if err != nil || second.PlanAttemptID != first.PlanAttemptID || second.RefusalID == first.RefusalID {
		t.Fatalf("premise: two distinct refusals of one attempt: %v %+v %+v", err, first, second)
	}
	_, err = route(nil)
	var parked *PlanAdmissionRefused
	if !errors.As(err, &parked) || parked.RefusalID != first.RefusalID {
		t.Fatalf("premise: the repeat of the FIRST refusal parks: %v", err)
	}
	e.beginReceipt(task)
	if !e.parkPlanAdmission(fixtureCtx(e, task), task, err) {
		t.Fatal("the repeated refusal did not park the invocation")
	}

	found := reconstructed(t, store, task)
	if len(found.PlanAttemptRefusals) != 2 || planAttemptOfRecord(found.PlanAttemptRefusals[first.RefusalID]) != first.PlanAttemptID ||
		planAttemptOfRecord(found.PlanAttemptRefusals[second.RefusalID]) != first.PlanAttemptID {
		t.Fatalf("the two refusals of one attempt are not both kept by RefusalID: %v", found.PlanAttemptRefusals)
	}
	fresh, _ := attemptEngine(t)
	if err := fresh.restorePlanAdmissionRefusals(found); err != nil {
		t.Fatal(err)
	}
	owed, others := fresh.takeRestoredRefusals(task)
	if owed == nil || owed.RefusalID != first.RefusalID || owed.PlanAttemptID != first.PlanAttemptID ||
		len(others) != 1 || others[0].RefusalID != second.RefusalID {
		t.Fatalf("the owed refusal is not the parked one: owed %+v others %+v", owed, others)
	}
	if !fresh.refusalRecorded(task, first.RefusalID) || !fresh.refusalRecorded(task, second.RefusalID) {
		t.Fatal("a restored refusal does not count toward its repetition")
	}
	if g := fresh.testEditGrants(task); len(g) != 0 || fresh.operativePlanAttempt(task).ID != "" {
		t.Fatalf("restoring refusals restored authority: %+v", g)
	}

	// An owed refusal no durable record binds is refused as a restoration,
	// never dropped and never routed to work.
	forged := found
	forged.PlanAdmissionRefused = []byte(`{"plan_attempt_id":"` + first.PlanAttemptID + `","refusal_id":"` + strings.Repeat("0", 64) + `"}`)
	var refusal *RestorationRefusal
	if err := fresh.restorePlanAdmissionRefusals(forged); !errors.As(err, &refusal) || refusal.Subject != restorationSubjectPlanAdmissionRefusal ||
		refusal.Binding != RestorationRecordInconsistent {
		t.Fatalf("an unbound owed refusal was accepted: %v", err)
	}
	forged.PlanAdmissionRefused = []byte(`{"plan_attempt_id":"` + strings.Repeat("1", 64) + `","refusal_id":"` + first.RefusalID + `"}`)
	if err := fresh.restorePlanAdmissionRefusals(forged); !errors.As(err, &refusal) {
		t.Fatalf("an owed refusal naming another attempt was accepted: %v", err)
	}
}

// Objective 61: only the workflow's RECORDED return to the architect creates
// an owed architect turn. Refusals of classes that do not return --
// authority_declined, supplied_plan_unrevisable, an unknown class -- recorded
// through the production recorder are evidence only: the session projection
// names none of them as owed, and a resume restores them as evidence. An owed
// refusal whose durable record carries no recorded return to the architect,
// whose record claims that return for a class that does not return, or whose
// payload is malformed is refused as a restoration, never silently discarded
// and never routed to work.
func TestObj61OnlyARecordedReturnToTheArchitectIsOwed(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-obj61-owed-disposition"
	if err := seedAppend(t, store, event.New("s1", task, event.SourceSystem, event.TaskCreated, attemptObjective, nil)); err != nil {
		t.Fatal(err)
	}
	attempt := routeWithTestEdits(t, e, task, attemptPlan("plan A", teS, teF), nil)
	byClass := map[planAdmissionRefusalClass]string{}
	for _, c := range []planAdmissionRefusalClass{refusalAuthorityDeclined, refusalSuppliedPlan, "unknown"} {
		cause := refusePlanAdmission(c, nil, errors.New("refused as "+string(c)))
		if _, err := e.continueAfterAdmissionRefusal(fixtureCtx(e, task), task, cause); err != cause {
			t.Fatalf("premise: class %s was continued: %v", c, err)
		}
		if err := e.closePlanAdmission(fixtureCtx(e, task), task, cause); err != cause {
			t.Fatalf("premise: class %s was not recorded by the production recorder: %v", c, err)
		}
		rec, ok := e.admissionRefusalOf(task, cause)
		if !ok {
			t.Fatalf("premise: class %s is not a refusal of the pending attempt", c)
		}
		byClass[c] = rec.RefusalID
	}
	found := reconstructed(t, store, task)
	if len(found.PlanAdmissionRefused) != 0 || len(found.PlanAttemptRefusals) != 3 {
		t.Fatalf("a refusal never returned to the architect was projected as owed, or a record was lost: owed %s records %d",
			found.PlanAdmissionRefused, len(found.PlanAttemptRefusals))
	}
	for c, id := range byClass {
		var r planAttemptRefusal
		if err := json.Unmarshal(found.PlanAttemptRefusals[id], &r); err != nil || r.Class != c || r.Continuation != "" {
			t.Fatalf("class %s was recorded with a continuation it was never given: %+v (%v)", c, r, err)
		}
	}
	fresh, _ := attemptEngine(t)
	if err := fresh.restorePlanAdmissionRefusals(found); err != nil {
		t.Fatal(err)
	}
	if owed, others := fresh.takeRestoredRefusals(task); owed != nil || len(others) != 3 {
		t.Fatalf("evidence-only refusals were restored as owed: owed %+v others %d", owed, len(others))
	}

	// Each of them asserted as owed -- a parked payload naming it -- is an
	// inconsistent record.
	var refusal *RestorationRefusal
	for c, id := range byClass {
		forged := found
		forged.PlanAdmissionRefused = found.PlanAttemptRefusals[id]
		if err := fresh.restorePlanAdmissionRefusals(forged); !errors.As(err, &refusal) ||
			refusal.Subject != restorationSubjectPlanAdmissionRefusal || refusal.Binding != RestorationRecordInconsistent {
			t.Fatalf("a %s refusal asserted as owed was accepted: %v", c, err)
		}
	}
	// A malformed owed payload is unreadable, not dropped.
	malformed := found
	malformed.PlanAdmissionRefused = json.RawMessage(`{"plan_attempt_id":`)
	if err := fresh.restorePlanAdmissionRefusals(malformed); !errors.As(err, &refusal) || refusal.Binding != RestorationRecordUnreadable {
		t.Fatalf("a malformed owed refusal was accepted: %v", err)
	}

	// A durable record that claims the return to the architect for a class
	// that does not return is projected as owed -- the projection reads only
	// the recorded continuation -- and the restorer refuses it.
	claimed := planAttemptRefusal{PlanAttemptID: attempt.ID, TaskID: task, Reason: "declined",
		Class: refusalAuthorityDeclined, GoverningEvidenceID: "g"}
	claimed.RefusalID = planAdmissionRefusalID(claimed)
	claimed.Continuation = session.PlanAdmissionContinuationArchitectTurn
	if err := seedAppend(t, store, event.New("s1", task, event.SourceSystem, event.PlanAttemptRefused, "refused", claimed)); err != nil {
		t.Fatal(err)
	}
	inconsistent := reconstructed(t, store, task)
	var named planAttemptRefusal
	if json.Unmarshal(inconsistent.PlanAdmissionRefused, &named) != nil || named.RefusalID != claimed.RefusalID {
		t.Fatalf("premise: the recorded continuation was not projected: %s", inconsistent.PlanAdmissionRefused)
	}
	if err := fresh.restorePlanAdmissionRefusals(inconsistent); !errors.As(err, &refusal) || refusal.Binding != RestorationRecordInconsistent {
		t.Fatalf("an owed refusal of a non-returning class was accepted: %v", err)
	}
}

// beforeStartRefusal is a canonical plan-admission refusal, returned to the
// architect, of an attempt that is durably started only AFTER the refusal is
// recorded: every field valid, so ordering is the only thing wrong with it.
func beforeStartRefusal(taskID string) (planAttemptRefusal, planAttempt) {
	a := planAttempt{ID: strings.Repeat("d", 64), TaskID: taskID, PlanSource: PlanByArchitect}
	r := planAttemptRefusal{PlanAttemptID: a.ID, TaskID: taskID, Reason: "declared prospective surface holds no recorded grant",
		Class: refusalProspectiveAdmission, Declaration: json.RawMessage(`[{"path":"extra/coverage_reconciliation_test.go"}]`),
		GoverningEvidenceID: strings.Repeat("e", 64)}
	r.RefusalID = planAdmissionRefusalID(r)
	r.Continuation = session.PlanAdmissionContinuationArchitectTurn
	return r, a
}

// Objective 61: a refusal recorded BEFORE its attempt's PlanAttemptStarted is
// not a refusal of anything this task had routed, and a later start of the
// same PlanAttemptID does not validate it retroactively. It seeds no
// repetition state -- so the next occurrence of that refusal is a FIRST
// occurrence, returned to the architect, never parked -- and, as the owed
// refusal, it is refused as a restoration by name rather than dropped. The
// same records in their lawful order restore.
func TestObj61ARefusalBeforeItsStartIsNeverRepetitionState(t *testing.T) {
	const task = "task-obj61-before-start"
	refusal, attempt := beforeStartRefusal(task)
	record := func(order ...event.Event) session.Interrupted {
		t.Helper()
		_, store := attemptEngine(t)
		if err := seedAppend(t, store, event.New("s1", task, event.SourceSystem, event.TaskCreated, attemptObjective, nil)); err != nil {
			t.Fatal(err)
		}
		for _, ev := range order {
			if err := seedAppend(t, store, ev); err != nil {
				t.Fatal(err)
			}
		}
		return reconstructed(t, store, task)
	}
	refused := event.New("s1", task, event.SourceSystem, event.PlanAttemptRefused, "refused", refusal)
	started := event.New("s1", task, event.SourceSystem, event.PlanAttemptStarted, "started", attempt)

	found := record(refused, started)
	if !found.StartedPlanAttempts[attempt.ID] || len(found.PlanAttemptRefusals) != 0 || len(found.PlanAdmissionRefused) == 0 {
		t.Fatalf("premise: the later start is recorded, the earlier refusal is not repetition state and stays owed: %+v", found)
	}
	fresh, _ := attemptEngine(t)
	var r *RestorationRefusal
	err := fresh.restorePlanAdmissionRefusals(found)
	if !errors.As(err, &r) || r.Subject != restorationSubjectPlanAdmissionRefusal || r.Binding != RestorationRecordInconsistent ||
		!strings.Contains(r.Detail, short12(refusal.RefusalID)) || !strings.Contains(r.Detail, short12(attempt.ID)) {
		t.Fatalf("an owed refusal recorded before its start was not refused by name: %v", err)
	}
	if fresh.refusalRecorded(task, refusal.RefusalID) {
		t.Fatal("a refusal recorded before its start seeded repetition state")
	}

	// Not owed: still never repetition state, so its next occurrence is a
	// first occurrence and is not parked.
	evidence := refusal
	evidence.Continuation = ""
	found = record(event.New("s1", task, event.SourceSystem, event.PlanAttemptRefused, "refused", evidence), started)
	fresh, _ = attemptEngine(t)
	if err := fresh.restorePlanAdmissionRefusals(found); err != nil {
		t.Fatal(err)
	}
	if fresh.refusalRecorded(task, refusal.RefusalID) {
		t.Fatal("an evidence-only refusal recorded before its start seeded repetition state")
	}

	// Control: the lawful order restores the same refusal as owed and counted.
	found = record(started, refused)
	fresh, _ = attemptEngine(t)
	if err := fresh.restorePlanAdmissionRefusals(found); err != nil {
		t.Fatalf("the same refusal after its start was refused: %v", err)
	}
	if owed, ok := fresh.owedPlanAdmissionRefusal(task); !ok || owed.RefusalID != refusal.RefusalID || !fresh.refusalRecorded(task, refusal.RefusalID) {
		t.Fatalf("the lawfully ordered refusal was not restored as owed and counted: %+v", owed)
	}
}

// Objective 61: a fresh engine's full Resume of a PLANNED task that retains
// candidate work, whose owed refusal cannot be bound -- recorded before its
// attempt started -- ends RESTORATION_REFUSED. The receipt was opened and the
// inherited candidate measured before restoration could refuse, so it does
// not claim CandidateNone beside the work on disk, and no implementer is
// invoked.
func TestObj61AResumeRefusingAnOwedRefusalMeasuresTheRetainedCandidate(t *testing.T) {
	const task = "task-obj61-resume-unbound"
	store := sessionStore(t)
	prior, _, _ := replanRun(t, store, task, replanA, replanSamePaths)
	refusal, attempt := beforeStartRefusal(task)
	for _, ev := range []event.Event{
		event.New("s1", task, event.SourceSystem, event.PlanAttemptRefused, "refused", refusal),
		event.New("s1", task, event.SourceSystem, event.PlanAttemptStarted, "started", attempt),
	} {
		if err := seedAppend(t, store, ev); err != nil {
			t.Fatal(err)
		}
	}
	found := reconstructed(t, store, task)
	if !found.Planned || len(found.PlanAdmissionRefused) == 0 || len(found.PlanAttemptRefusals[refusal.RefusalID]) != 0 {
		t.Fatalf("premise: a planned task owed a refusal no durable record binds: %+v", found)
	}
	_, seen := resumeFresh(t, prior, store, found)
	terminal := indexOfKind(seen, event.WorkflowRestorationRefused)
	if terminal < 0 || !strings.Contains(seen[terminal].Summary, short12(refusal.RefusalID)) {
		t.Fatalf("the unbound owed refusal did not end the invocation RESTORATION_REFUSED by name:\n%s", gapLoopTrace(seen))
	}
	if implementerStartedBefore(seen, len(seen)) {
		t.Fatalf("an implementer was invoked under an unbound owed refusal:\n%s", gapLoopTrace(seen))
	}
	var receipt struct {
		Receipt struct {
			Outcome   string `json:"outcome"`
			Candidate string `json:"candidate_state"`
		} `json:"receipt"`
	}
	for _, ev := range seen[:terminal] {
		if ev.Kind == event.RunReceipt {
			_ = json.Unmarshal(ev.Payload, &receipt)
		}
	}
	if receipt.Receipt.Outcome != "RESTORATION_REFUSED" || receipt.Receipt.Candidate != "UNATTEMPTED" {
		t.Fatalf("the restoration refusal receipt does not account for the retained candidate work: %+v", receipt.Receipt)
	}
}

// OBJECTIVE 70B2a2 ENGINE WITNESSES. Every engine reader of a task's history
// that can influence resume, authority, plan, grant, gap or terminal state
// acts on the task's canonical lineage members only, and a history that cannot
// be read or projected is a typed refusal, never an empty authoritative one.

const b2a2Task = "task-b2a2"

// b2a2Holder is a repository whose record of holder s1 holds b2a2Task's root,
// and the path of that record, so a witness can leave in it what a foreign or
// legacy writer could have.
func b2a2Holder(t *testing.T, preRoot ...event.Event) (*session.Store, string) {
	t.Helper()
	repo := t.TempDir()
	store, err := session.New(repo, "s1")
	if err != nil {
		t.Fatal(err)
	}
	if len(preRoot) != 0 {
		b2a2Raw(t, filepath.Join(repo, ".sensei-code", "sessions", "s1", "events.jsonl"), preRoot...)
	}
	if err := seedAppend(t, store, event.New("s1", b2a2Task, event.SourceUser, event.TaskCreated, attemptObjective, nil)); err != nil {
		t.Fatal(err)
	}
	return store, filepath.Join(repo, ".sensei-code", "sessions", "s1", "events.jsonl")
}

// b2a2Raw appends evs to the record at path without the Store's
// authorization: exactly what the Store would refuse to write.
func b2a2Raw(t *testing.T, path string, evs ...event.Event) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, ev := range evs {
		line, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

// b2a2Answer is a well-formed AuthorityResolved sessionID recorded: an
// authorization of condition over a.go, about gap when one is named.
func b2a2Answer(sessionID, condition string, gap *GapIdentity) event.Event {
	return event.New(sessionID, b2a2Task, event.SourceUser, event.AuthorityResolved, condition, resolvedAuthority{
		Resolution: authority.Resolution{TaskID: b2a2Task, SessionID: sessionID, DecidedAt: time.Now().UTC(),
			Question: "may " + condition + " proceed?", Condition: condition, OptionID: "1", OptionLabel: "Authorize",
			Scope: []string{"a.go"}, Outcome: authority.Authorize, State: authority.Proposed},
		Gap: gap})
}

// b2a2Question is a well-formed deferred question sessionID recorded.
func b2a2Question(sessionID, condition string) event.Event {
	q := DeferredAuthority{Condition: condition, TaskID: b2a2Task, SessionID: sessionID, Scope: []string{"a.go"}, ScopeRecorded: true,
		Decision: authority.Decision{Level: authority.Human, Subject: "May this change proceed?", Options: realOptions()}}
	return event.New(sessionID, b2a2Task, event.SourceUser, event.WorkflowAwaitingAuthority, condition, q)
}

// A2-W6 / P1-W4 / R220-W4 / A2-W11: the gap ledger, answer scope, answered
// conditions and authority decisions keep a valid ancestor's settlements and
// answers, under a fresh descendant session, while a correctly framed answer an
// unrelated session injected into the holder ledger settles, authorizes and
// decides nothing. The foreign evidence stays in the ledger.
func TestB2a2A2W6TheGapLedgerKeepsAncestorsAndIgnoresForeignRecords(t *testing.T) {
	store, path := b2a2Holder(t)
	ancestorGap := GapIdentity{Kind: "consequence", Subject: "ancestor", Scope: []string{"a.go"}}
	foreignGap := GapIdentity{Kind: "consequence", Subject: "foreign", Scope: []string{"a.go"}}
	for _, ev := range []event.Event{b2a2Answer("s1", "ancestor condition", nil), b2a2Answer("s1", "ancestor gap", &ancestorGap)} {
		if err := seedAppend(t, store, ev); err != nil {
			t.Fatal(err)
		}
	}
	bindFixtureSession(t, store, b2a2Task, "s2")
	b2a2Raw(t, path, b2a2Answer("sC", "foreign condition", nil), b2a2Answer("sC", "foreign gap", &foreignGap))
	if raw, _ := store.Load(); len(raw) != 6 {
		t.Fatalf("premise: the ledger holds the root, two ancestor answers, the binding and two foreign answers; got %d records", len(raw))
	}

	e := &Engine{Bus: event.NewBus(), SessionID: "s2", Store: store, pending: map[string]chan string{}}
	if err := e.taskHistoryRefusal(b2a2Task); err != nil {
		t.Fatalf("valid lineage history with foreign evidence was refused: %v", err)
	}
	tr := e.gapResolutions(b2a2Task)
	e.mu.Lock()
	ancestorSettled, foreignSettled := len(tr.settlements[ancestorGap.Key()]), len(tr.settlements[foreignGap.Key()])
	e.mu.Unlock()
	if ancestorSettled != 1 || foreignSettled != 0 {
		t.Errorf("gap ledger: ancestor settlements %d (want 1), foreign settlements %d (want 0)", ancestorSettled, foreignSettled)
	}
	if authorized, asked := e.applyAnsweredCondition(b2a2Task, "ancestor condition", "a.go"); !authorized || !asked {
		t.Errorf("the ancestor's answer no longer authorizes its condition: authorized=%v asked=%v", authorized, asked)
	}
	if authorized, asked := e.applyAnsweredCondition(b2a2Task, "foreign condition", "a.go"); authorized || asked {
		t.Errorf("a foreign session's answer authorized this task: authorized=%v asked=%v", authorized, asked)
	}
	decisions, err := e.authorityDecisions(b2a2Task)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range decisions {
		if strings.Contains(d.Condition, "foreign") {
			t.Errorf("a foreign session's answer is recorded as this task's decision: %+v", d)
		}
	}
	if len(decisions) != 2 {
		t.Errorf("the ancestor's decisions were not all kept: %+v", decisions)
	}
	if err := e.historyUnavailable(b2a2Task); err != nil {
		t.Errorf("a valid history was recorded unavailable: %v", err)
	}
}

// R220-W5 / D4 / D5 / X5-X8: a task history that cannot be projected -- here
// one malformed record of the task -- is never empty authoritative state. The
// gap ledger stays unhydrated and refuses, typed; answered conditions,
// authority decisions and the recorded objective refuse rather than read as
// "nothing answered" or "nothing recorded"; and the authority rendezvous asks
// nothing.
func TestB2a2R220W5AFailedHistoryReadIsNeverEmptyAuthoritativeState(t *testing.T) {
	store, path := b2a2Holder(t)
	if err := seedAppend(t, store, b2a2Answer("s1", "ancestor condition", nil)); err != nil {
		t.Fatal(err)
	}
	b2a2Raw(t, path, event.Event{TaskID: b2a2Task, Kind: event.AuthorityResolved, Source: event.SourceUser, Summary: "unframed"})

	e := &Engine{Bus: event.NewBus(), SessionID: "s2", Store: store, pending: map[string]chan string{}}
	err := e.taskHistoryRefusal(b2a2Task)
	var refusal *RestorationRefusal
	if !errors.As(err, &refusal) || refusal.Subject != restorationSubjectTaskHistory || !errors.Is(err, err) {
		t.Fatalf("the gap ledger's unavailable history was not refused, typed: %v", err)
	}
	if _, perr := ParseRestorationRefusal(mustJSON(t, refusal)); perr != nil {
		t.Fatalf("the refusal is not a valid restoration refusal: %v", perr)
	}
	tr := e.gapResolutions(b2a2Task)
	e.mu.Lock()
	hydrated, unavailable := tr.hydrated, tr.unavailable
	e.mu.Unlock()
	if hydrated || !errors.Is(unavailable, session.ErrTaskHistoryUnavailable) {
		t.Errorf("the gap ledger was marked read over a history it could not project: hydrated=%v unavailable=%v", hydrated, unavailable)
	}
	if _, err := e.answeredConditions(b2a2Task); !errors.Is(err, session.ErrTaskHistoryUnavailable) {
		t.Errorf("answered conditions read a failed history as an answer set: %v", err)
	}
	if _, err := e.authorityDecisions(b2a2Task); !errors.Is(err, session.ErrTaskHistoryUnavailable) {
		t.Errorf("authority decisions read a failed history as a decision set: %v", err)
	}
	if _, err := e.recordedObjective(b2a2Task); err == nil {
		t.Error("the recorded objective was read from a history that cannot be projected")
	} else if _, ok := err.(objectiveUnreadable); !ok {
		t.Errorf("an unprojectable history was not reported as unreadable: %T %v", err, err)
	}

	// The deciding callers: a condition lookup claims neither answer and
	// records the history unavailable, which every caller reads before it
	// acts on either; a decision record claims no owner; the rendezvous asks
	// nothing.
	fresh := &Engine{Bus: event.NewBus(), SessionID: "s2", Store: store, pending: map[string]chan string{}}
	fresh.gapResolutions(b2a2Task)
	if authorized, asked := fresh.applyAnsweredCondition(b2a2Task, "ancestor condition", "a.go"); authorized || asked {
		t.Errorf("an unreadable history answered the condition: authorized=%v asked=%v", authorized, asked)
	}
	if err := fresh.historyUnavailable(b2a2Task); !errors.Is(err, session.ErrTaskHistoryUnavailable) {
		t.Errorf("the deciding caller is not told the history is unavailable: %v", err)
	}
	if a := fresh.decisionAuthority(b2a2Task, certifiedStart{}); a.Owner != "" || a.HumanGrant != "" {
		t.Errorf("a decision owner was claimed from an unreadable history: %+v", a)
	}
	if _, err := fresh.awaitChoice(t.Context(), nil, b2a2Task, "c", "", "", authority.Decision{Options: realOptions()}, realOptions()); err == nil {
		t.Error("the authority rendezvous asked a question over an unavailable history")
	}
	fresh.mu.Lock()
	_, pending := fresh.pending[b2a2Task]
	fresh.mu.Unlock()
	if pending {
		t.Error("a question was left pending over an unavailable history")
	}
}

// R220-W7 / D9 / A2-W3: ResumeTask refuses a task whose history could not be
// projected before anything is admitted or bound, and -- for a caller that
// bypassed the canonical projection -- refuses after binding, typed, before
// any lane reads the history: a raw-folded foreign question is never asked,
// and a malformed history is never continued.
// b2a2Resume runs ResumeTask for task in a fresh-session engine over store
// and returns the attempt, once it ended, and everything it published.
func b2a2Resume(t *testing.T, store *session.Store, task session.Interrupted) (*ResumeAttempt, []event.Event) {
	t.Helper()
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := &Engine{Bus: bus, SessionID: "s-fresh", Store: store, pending: map[string]chan string{}}
	attempt := e.ResumeTask(context.Background(), task)
	select {
	case <-attempt.Ended():
	case <-time.After(15 * time.Second):
		t.Fatal("the resume did not end")
	}
	return attempt, take()
}

// b2a2RefusedAs holds that a resume asked nothing and was refused, typed,
// as subject.
func b2a2RefusedAs(t *testing.T, evs []event.Event, subject string) {
	t.Helper()
	for _, ev := range evs {
		if ev.Kind == event.AuthorityRequired {
			t.Fatalf("a question was asked: %s", ev.Summary)
		}
	}
	var r RestorationRefusal
	payloadOf(t, evs, event.WorkflowRestorationRefused, &r)
	if r.Subject != subject {
		t.Fatalf("the resume was not refused as %s: %+v (%v)", subject, r, kindsOf(evs))
	}
}

func TestB2a2R220W7ResumeRefusesWhatTheCanonicalHistoryDoesNotHold(t *testing.T) {
	resume, refusedAs := b2a2Resume, b2a2RefusedAs

	t.Run("unavailable projection is refused before binding", func(t *testing.T) {
		store, path := b2a2Holder(t)
		b2a2Raw(t, path, event.Event{TaskID: b2a2Task, Kind: event.Status, Source: event.SourceSystem, Summary: "unframed"})
		record, _ := store.Load()
		var task session.Interrupted
		for _, it := range session.CanonicalInterrupted(record) {
			if it.TaskID == b2a2Task {
				task = it
			}
		}
		if task.Unavailable == nil {
			t.Fatalf("premise: the canonical projection refuses the task: %+v", task)
		}
		attempt, _ := resume(t, store, task)
		if attempt.Binding().Refusal == nil || !errors.Is(attempt.Binding().Refusal, session.ErrTaskHistoryUnavailable) {
			t.Fatalf("an unavailable task was not refused before binding: %+v", attempt.Binding())
		}
		record, _ = store.Load()
		for _, ev := range record {
			if ev.Kind == session.SessionLineageBound {
				t.Fatalf("a refused task was bound anyway: %+v", ev)
			}
		}
	})
	t.Run("a raw projection of a malformed history is refused after binding", func(t *testing.T) {
		// Pre-root, so the lineage binds and only the projection can refuse.
		store, _ := b2a2Holder(t, event.Event{TaskID: b2a2Task, Kind: event.Status, Source: event.SourceSystem, Summary: "unframed"})
		record, _ := store.Load()
		raw := session.FindInterrupted(record)
		if len(raw) != 1 {
			t.Fatalf("premise: the raw fold offers the task: %+v", raw)
		}
		_, evs := resume(t, store, raw[0])
		refusedAs(t, evs, restorationSubjectTaskHistory)
	})
	t.Run("a task its own lineage ended is not continued from a stale projection", func(t *testing.T) {
		store, _ := b2a2Holder(t)
		record, _ := store.Load()
		stale := session.CanonicalInterrupted(record)
		if len(stale) != 1 || stale[0].Unavailable != nil {
			t.Fatalf("premise: the task was active when it was projected: %+v", stale)
		}
		if err := seedAppend(t, store, event.New("s1", b2a2Task, event.SourceSystem, event.WorkflowCompleted, "done",
			map[string]string{"workspace": "w", "implementor": "claude", "plan": "p", "review": "accept", "audit": "pass",
				"publication": "declined"})); err != nil {
			t.Fatal(err)
		}
		_, evs := resume(t, store, stale[0])
		refusedAs(t, evs, restorationSubjectTaskHistory)
	})
	t.Run("a foreign question is never asked", func(t *testing.T) {
		store, path := b2a2Holder(t)
		b2a2Raw(t, path, b2a2Question("sC", "a foreign question"))
		record, _ := store.Load()
		raw := session.FindInterrupted(record)
		if len(raw) != 1 || len(raw[0].AwaitingAuthority) == 0 {
			t.Fatalf("control: the raw fold offers the foreign question: %+v", raw)
		}
		for _, it := range session.CanonicalInterrupted(record) {
			if it.TaskID == b2a2Task && len(it.AwaitingAuthority) != 0 {
				t.Fatal("the canonical projection offers a foreign session's question")
			}
		}
		_, evs := resume(t, store, raw[0])
		refusedAs(t, evs, restorationSubjectDeferredAuthority)
	})
	t.Run("an ancestor's question is the task's own", func(t *testing.T) {
		store, _ := b2a2Holder(t)
		if err := seedAppend(t, store, b2a2Question("s1", "the ancestor's question")); err != nil {
			t.Fatal(err)
		}
		record, _ := store.Load()
		canonical := session.CanonicalInterrupted(record)
		if len(canonical) != 1 || len(canonical[0].AwaitingAuthority) == 0 {
			t.Fatalf("the canonical projection lost the ancestor's question: %+v", canonical)
		}
		lineage, err := store.TaskSessionLineage(b2a2Task)
		if err != nil {
			t.Fatal(err)
		}
		if err := deferredAuthorityRefusal(canonical[0], canonical[0], lineage); err != nil {
			t.Fatalf("the ancestor's question was refused: %v", err)
		}
		_, evs := resume(t, store, canonical[0])
		for _, ev := range evs {
			if ev.Kind == event.WorkflowRestorationRefused {
				t.Fatalf("the ancestor's question was refused on resume: %s", ev.Summary)
			}
		}
	})
}

// b2a2Projected is b2a2Task as the canonical projection of store's record
// holds it now.
func b2a2Projected(t *testing.T, store *session.Store) session.Interrupted {
	t.Helper()
	record, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range session.CanonicalInterrupted(record) {
		if it.TaskID == b2a2Task {
			if it.Unavailable != nil {
				t.Fatalf("premise: the task's history is projectable: %v", it.Unavailable)
			}
			return it
		}
	}
	t.Fatal("premise: the canonical projection holds the task as active")
	return session.Interrupted{}
}

// R220-W7 / D9 / review f1: the caller's projection only SELECTS the task.
// After binding, every lane and restoration of ResumeTask reads the post-bind
// canonical projection: a caller value carrying state the canonical history
// does not hold is not consumed, a transition recorded between discovery and
// binding is consumed, and a lane the canonical history no longer holds is
// refused, typed, before anything is asked or restored.
func TestB2a2R220W7ResumeTaskConsumesOnlyThePostBindCanonicalProjection(t *testing.T) {
	t.Run("caller fields the canonical history does not hold are not consumed", func(t *testing.T) {
		store, _ := b2a2Holder(t)
		caller := b2a2Projected(t, store)
		caller.Task = "a forged objective"
		caller.PlanAdmissionRefused = json.RawMessage(`{"refusal_id":`)
		// Control: the forged state is effective wherever it is consumed.
		control := &Engine{Bus: event.NewBus(), SessionID: "s-control", Store: store, pending: map[string]chan string{}}
		var r *RestorationRefusal
		if err := control.restorePlanAdmissionRefusals(caller); !errors.As(err, &r) || r.Subject != restorationSubjectPlanAdmissionRefusal {
			t.Fatalf("control: the forged refusal state is not one a restoration would act on: %v", err)
		}
		_, evs := b2a2Resume(t, store, caller)
		for _, ev := range evs {
			if ev.Kind == event.WorkflowRestorationRefused {
				var got RestorationRefusal
				if json.Unmarshal(ev.Payload, &got) == nil && got.Subject == restorationSubjectPlanAdmissionRefusal {
					t.Fatalf("the resume restored plan-admission state from the caller, not the canonical history: %s", ev.Summary)
				}
			}
			if line, _ := json.Marshal(ev); strings.Contains(string(line), caller.Task) {
				t.Fatalf("the resume consumed the caller's objective, not the canonical one: %s", line)
			}
		}
	})
	t.Run("a transition recorded between discovery and binding is consumed", func(t *testing.T) {
		store, _ := b2a2Holder(t)
		stale := b2a2Projected(t, store)
		if err := seedAppend(t, store, event.New("s1", b2a2Task, event.SourceSystem, event.WorkflowPlanAdmissionRefused,
			"refused at admission", map[string]string{"task_id": b2a2Task, "refusal_id": "r-unbound", "plan_attempt_id": "pa-unbound",
				"reason": "no admitted plan"})); err != nil {
			t.Fatal(err)
		}
		if now := b2a2Projected(t, store); len(now.PlanAdmissionRefused) == 0 || len(stale.PlanAdmissionRefused) != 0 {
			t.Fatalf("premise: only the post-bind projection owes the refusal: stale %s, now %s", stale.PlanAdmissionRefused, now.PlanAdmissionRefused)
		}
		_, evs := b2a2Resume(t, store, stale)
		b2a2RefusedAs(t, evs, restorationSubjectPlanAdmissionRefusal)
	})
	t.Run("a lane the canonical history no longer holds is refused", func(t *testing.T) {
		store, _ := b2a2Holder(t)
		stale := b2a2Projected(t, store)
		if err := seedAppend(t, store, b2a2Question("s1", "a question asked after discovery")); err != nil {
			t.Fatal(err)
		}
		_, evs := b2a2Resume(t, store, stale)
		b2a2RefusedAs(t, evs, restorationSubjectResumeLane)
	})
}

// RULING-220 D4/D5 (70B2a2 c3): a task record that does not exist, or that
// vanished after the task was recorded in it, proves nothing about what the
// task decided. Every authority-bearing reader refuses it, typed: P9 is left
// unhydrated with the refusal, and neither the answered conditions nor the
// prior decisions are ever an empty -- authoritative -- set. Presence and
// contents are one observation (Store.TaskHistory), so a record that vanishes
// between a presence check and its read cannot report an empty history as a
// recorded one.
func TestB2a2R220W5AnAbsentOrVanishedRecordIsNeverEmptyAuthority(t *testing.T) {
	const taskID = "task-b2a2-absent"
	for name, store := range map[string]func(t *testing.T) *session.Store{
		"never written": sessionStore,
		"vanished after it was recorded": func(t *testing.T) *session.Store {
			dir := t.TempDir()
			store, err := session.New(dir, "s1")
			if err != nil {
				t.Fatal(err)
			}
			rootFixtureTask(t, store, "s1", taskID, "an objective")
			if h, err := store.TaskHistory(taskID); err != nil || !h.Recorded || len(h.Members) == 0 {
				t.Fatalf("premise: the task is recorded in its record: %+v %v", h, err)
			}
			if err := os.Remove(dir + "/.sensei-code/sessions/s1/events.jsonl"); err != nil {
				t.Fatal(err)
			}
			return store
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := &Engine{Bus: event.NewBus(), SessionID: "s1", Store: store(t), pending: map[string]chan string{}}
			if h, err := e.Store.TaskHistory(taskID); err != nil || h.Recorded || len(h.Members) != 0 {
				t.Fatalf("a missing record is not reported as unrecorded: %+v %v", h, err)
			}
			var refusal *RestorationRefusal
			answers, err := e.answeredConditions(taskID)
			if !errors.As(err, &refusal) || answers != nil {
				t.Fatalf("answeredConditions read a missing record as an empty answer set: %v %v", answers, err)
			}
			decisions, err := e.authorityDecisions(taskID)
			if !errors.As(err, &refusal) || decisions != nil {
				t.Fatalf("authorityDecisions read a missing record as no decisions: %v %v", decisions, err)
			}
			tr := e.gapResolutions(taskID)
			e.mu.Lock()
			hydrated, unavailable := tr.hydrated, tr.unavailable
			e.mu.Unlock()
			if hydrated || unavailable == nil {
				t.Fatalf("gapResolutions hydrated an empty gap ledger from a missing record (hydrated=%v unavailable=%v)", hydrated, unavailable)
			}
			if err := e.taskHistoryRefusal(taskID); !errors.As(err, &refusal) {
				t.Fatalf("a missing record let an invocation decide from P9: %v", err)
			}
		})
	}
}

// R238-X8a / P1-W4 / A2-W11: the recorded objective a fresh engine recovers
// -- exactly as a restarted process reads it, nothing held in memory -- is the
// TaskCreated of the task's canonical member history and nothing else. A
// TaskCreated a foreign writer left beside the holder's root, before it or
// after it, is not a lineage member: it cannot establish the operative
// objective, and since the task then has no single root, no objective is
// established at all -- typed unreadable, never the foreign bytes, never the
// first creation a raw read would pick. Foreign evidence that leaves the
// lineage valid does not disturb the canonical objective.
func TestB2a2R238X8aANonMemberTaskCreatedCannotEstablishTheObjective(t *testing.T) {
	const foreignObjective = "an objective an unrelated session recorded"
	recovered := func(t *testing.T, store *session.Store) (Objective, error) {
		t.Helper()
		e := &Engine{Bus: event.NewBus(), SessionID: "s2", Store: store, pending: map[string]chan string{}}
		if len(e.objectives) != 0 {
			t.Fatal("premise: the fresh engine holds no objective, so the history is what is read")
		}
		return e.recordedObjective(b2a2Task)
	}
	rawRecord := func(t *testing.T, evs ...event.Event) *session.Store {
		t.Helper()
		repo := t.TempDir()
		store, err := session.New(repo, "s1")
		if err != nil {
			t.Fatal(err)
		}
		b2a2Raw(t, filepath.Join(repo, ".sensei-code", "sessions", "s1", "events.jsonl"), evs...)
		return store
	}
	root := event.New("s1", b2a2Task, event.SourceUser, event.TaskCreated, attemptObjective, nil)
	foreign := event.New("sC", b2a2Task, event.SourceUser, event.TaskCreated, foreignObjective, nil)

	for name, store := range map[string]*session.Store{
		"a foreign TaskCreated before the holder's root": rawRecord(t, foreign, root),
		"a foreign TaskCreated after the holder's root":  rawRecord(t, root, foreign),
	} {
		t.Run(name, func(t *testing.T) {
			o, err := recovered(t, store)
			if err == nil {
				t.Fatalf("R238-X8a: a non-member TaskCreated was read as history the objective can be decided from: "+
					"the objective %q was established for a task with no single root", o.Text)
			}
			if _, ok := err.(objectiveUnreadable); !ok {
				t.Fatalf("R238-X8a: the contested objective was not reported unreadable, typed: %T %v", err, err)
			}
			if o.Text != "" {
				t.Fatalf("R238-X8a: a refused objective still carried bytes: %q", o.Text)
			}
			if !errors.Is(err.(objectiveUnreadable).Cause, session.ErrTaskHistoryUnavailable) {
				t.Errorf("R238-X8a: the refusal does not name the unprojectable history: %v", err)
			}
		})
	}

	t.Run("foreign evidence beside a valid lineage", func(t *testing.T) {
		store, path := b2a2Holder(t)
		bindFixtureSession(t, store, b2a2Task, "s2")
		b2a2Raw(t, path, b2a2Answer("sC", "foreign condition", nil), b2a2Question("sC", "foreign question"))
		o, err := recovered(t, store)
		if err != nil || o.Text != attemptObjective {
			t.Fatalf("R238-X8a: foreign evidence displaced the canonical objective: %q %v", o.Text, err)
		}
	})
}

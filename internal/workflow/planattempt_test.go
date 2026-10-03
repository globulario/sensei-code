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
	a, err := e.beginPlanAttempt(taskID, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.recordTestEditGrants(taskID, "recorded", testEditRecord{PlanAttemptID: a.ID, World: teWorld, Grants: grants}); err != nil {
		t.Fatal(err)
	}
	e.setTestEditGrants(taskID, grants)
	if err := e.recordProspectiveGrants(taskID, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: teWorld}); err != nil {
		t.Fatal(err)
	}
	e.setProspectiveGrants(taskID, nil)
	return a
}

// routeWithDerivation routes d's grant state through the production recorder.
func routeWithDerivation(t *testing.T, e *Engine, taskID string, d architectureDecision) planAttempt {
	t.Helper()
	a, err := e.beginPlanAttempt(taskID, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	e.derivedCoverage(context.Background(), taskID, d.Files, d.ProspectiveSurfaces)
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
	if _, err := e.adoptPlanAttempt(task, attemptObjective, attemptPlan("plan A", planned...)); err != nil {
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
	e.recordClosureQuestion("task-writer", "coverage gap", closureDecision(t), certifiedStart{}, "model", 1)
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
	b, err := e.beginPlanAttempt(task, attemptObjective, attemptPlan("plan A, but different", planned...))
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Fatal("same paths, different payload: one identity")
	}
	// B became operative without routing recording anything for it.
	e.emit(event.New("s1", task, event.SourceArchitect, event.PlanProposed, "B",
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
	a1, err1 := one.beginPlanAttempt("task-w5", attemptObjective, d)
	a2, err2 := two.beginPlanAttempt("task-w5", attemptObjective, d)
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
func TestW6AnAdmissionRefusalIsBoundToItsAttemptAcrossInterruption(t *testing.T) {
	const task = "task-w6"
	store := sessionStore(t)
	// extra/new_test.go is declared beside no covered file, so no grant is
	// derived for it and prospective admission refuses the plan.
	refused := `{"decision":"proceed","summary":"edit main","plan":"plan A","files":["main.go","extra/new_test.go"],"mode":"modify",` +
		`"prospective_surfaces":[{"path":"extra/new_test.go","package":"extra","role":"go-regression-test","dependencies":["testing"]}]}`
	e, architect, _ := newGapLoopEngine(t, nil, store, refused)
	world := grantWorld(t, e)
	run := driveGapLoop(t, e, architect, world, task, "", func(ctx context.Context) {
		e.run(ctx, task, attemptObjective, RequestedByHuman)
	})
	attempts := startedAttempts(t, run.events)
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
			if err := interrupted.Append(ev); err != nil {
				t.Fatal(err)
			}
		}
	}
	found := reconstructed(t, interrupted, task)
	if found.Planned || found.PlanAttemptID != "" || len(found.PlanAttemptRefusals) != 1 || len(found.PlanAttemptRefusals[attempts[0]]) == 0 {
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
		len(after[0].PlanAttemptRefusals) != 1 || len(after[0].PlanAttemptRefusals[after[0].PlanAttemptID]) != 0 {
		t.Fatalf("the refusal attached to the replacement or was lost: %+v", after)
	}
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
	if _, err := e.adoptPlanAttempt("task-w10", attemptObjective, second); err == nil {
		t.Fatal("a plan whose declared effects differ was made operative under the routed attempt's authority")
	}
	if _, err := e.adoptPlanAttempt("task-w10", attemptObjective, first); err != nil {
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

	a, err := e.beginPlanAttempt(task, attemptObjective, d)
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
	e.recordTestEditGrants(task, "recorded", testEditRecord{PlanAttemptID: a.ID, World: grants[0].World, Grants: grants})
	if _, contradicted := e.premisesUnderRecordedAuthority(task, d); len(contradicted) != 0 {
		t.Fatal("a test-edit grant recorded at another world answered the premise")
	}
	for i := range grants {
		grants[i].World = a.World
	}
	e.recordTestEditGrants(task, "recorded", testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants})
	kept, contradicted := e.premisesUnderRecordedAuthority(task, d)
	if len(contradicted) != 1 || contradicted[0].About != teF || len(kept) != 4 {
		t.Fatalf("not exactly the matching premise was set aside: kept %v contradicted %v", kept, contradicted)
	}
	// A prospective record holding the grant, but read at another world,
	// answers nothing; at this attempt's world it answers the create premise.
	granted := []prospectiveGrant{{Surface: d.ProspectiveSurfaces[0], Covering: teS}}
	e.recordProspectiveGrants(task, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: "another world", Grants: granted})
	if _, contradicted := e.premisesUnderRecordedAuthority(task, d); len(contradicted) != 1 {
		t.Fatalf("a prospective grant recorded at another world answered the premise: %v", contradicted)
	}
	e.recordProspectiveGrants(task, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: a.World, Grants: granted})
	if _, contradicted := e.premisesUnderRecordedAuthority(task, d); len(contradicted) != 2 {
		t.Fatalf("the recorded prospective grant did not answer its premise: %v", contradicted)
	}
	// A record for another attempt answers nothing for this one.
	other := d
	other.Plan = "another plan"
	if _, err := e.beginPlanAttempt(task, attemptObjective, other); err != nil {
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
// identity no record establishes.
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
	failed := false
	for _, ev := range run.events {
		failed = failed || (ev.Kind == event.WorkflowFailed && strings.Contains(ev.Summary, "could not be written durably"))
	}
	if !failed {
		t.Fatalf("the run did not fail closed on the unrecorded start:\n%s", gapLoopTrace(run.events))
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
	if _, err := e.adoptPlanAttempt(task, attemptObjective, attemptPlan("plan A", planned...)); err != nil {
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
		e, architect, world := newGapLoopEngine(t, nil, store, gapProceed)
		gap := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: world}
		declined := resolvedAuthority{Resolution: authority.Resolution{TaskID: task, SessionID: "s1", Condition: "declined",
			Scope: []string{"main.go"}, Outcome: authority.Decline, DecidedAt: time.Now().UTC()}, Gap: &gap}
		if err := store.Append(event.New("s1", task, event.SourceUser, event.AuthorityResolved, "Decline", declined)); err != nil {
			t.Fatal(err)
		}
		run := driveGapLoop(t, e, architect, world, task, "", func(ctx context.Context) {
			e.run(ctx, task, attemptObjective, RequestedByHuman)
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
	fresh, _ := attemptEngine(t)
	ch, cancel := fresh.Bus.Subscribe(64)
	defer cancel()
	fresh.resumeAuthority(context.Background(), session.Interrupted{TaskID: "task-asked-deferred", AwaitingAuthority: raw,
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
		res := resolvedAuthority{Resolution: authority.Resolution{TaskID: task, SessionID: "s1", Condition: condition,
			Scope: []string{teS}, Outcome: authority.Authorize, DecidedAt: time.Now().UTC()}, PlanAttemptID: owner}
		if withGap {
			res.Gap = &gap
			e.settleGapFor(task, gap, authority.Authorize, owner)
		}
		e.emit(event.New("s1", task, event.SourceUser, event.AuthorityResolved, "Authorize", res))
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
	if _, err := e.adoptPlanAttempt(task, attemptObjective, planB); err != nil {
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
	if _, err := e.beginPlanAttempt(task, attemptObjective, attemptPlan("plan C", teS)); err != nil {
		t.Fatal(err)
	}
	e.emit(event.New("s1", task, event.SourceUser, event.AuthorityResolved, "Authorize", resolvedAuthority{
		Resolution:    authority.Resolution{TaskID: task, Condition: "unowned", Scope: []string{teS}, Outcome: authority.Authorize},
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
	if _, err := e.adoptPlanAttempt(task, attemptObjective, planA); err != nil {
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
	if _, err := e.adoptPlanAttempt(task, attemptObjective, planB); err != nil {
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
	if _, err := e.adoptPlanAttempt(task, attemptObjective, planA); err != nil {
		t.Fatal(err)
	}
	planB := attemptPlan("plan B", teS)
	b, err := e.beginPlanAttempt(task, attemptObjective, planB)
	if err != nil {
		t.Fatal(err)
	}
	mend := breakStore(t, dir)
	e.derivedCoverage(context.Background(), task, planB.Files, nil)
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
	if _, err := e.adoptPlanAttempt(task, attemptObjective, planB); err == nil {
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
	err = e2.recordTestEditGrants(task, "authored", testEditRecord{PlanAttemptID: c.ID, World: teWorld, Grants: []testEditGrant{{Path: teS}}})
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
// had been: the caller gets the cause and the write failure, and a later
// refusal of the same attempt is recorded, exactly once.
func TestAnAdmissionRefusalThatCannotBeRecordedIsNotSuppressed(t *testing.T) {
	e, store, dir := durableStore(t)
	const task = "task-refusal-write-fails"
	a, err := e.beginPlanAttempt(task, attemptObjective, attemptPlan("plan A", teS))
	if err != nil {
		t.Fatal(err)
	}
	cause := refusePlanAdmission(errors.New("prospective admission refused before implementation: x"))
	mend := breakStore(t, dir)
	got := e.closePlanAdmission(task, cause)
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
	if got := e.closePlanAdmission(task, cause); got != cause {
		t.Fatalf("a recorded refusal changed its cause: %v", got)
	}
	_ = e.closePlanAdmission(task, cause)
	if refusals() != 1 {
		t.Fatalf("the refusal was not recorded exactly once after the store recovered: %d", refusals())
	}
}

// Only a mechanically established refusal is PlanAttemptRefused. An instrument
// that could not answer, a response that could not be decoded, a provider or a
// persistence failure after the attempt began is returned as the failure it is,
// and never recorded as the plan being refused.
func TestOnlyAGenuineAdmissionRefusalIsRecordedAsOne(t *testing.T) {
	e, store := attemptEngine(t)
	const task = "task-not-a-refusal"
	if _, err := e.beginPlanAttempt(task, attemptObjective, attemptPlan("plan A", teS)); err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{
		fmt.Errorf("Sensei scoped preflight: %w", errors.New("connection reset")),
		errors.New("preflight response carries no status"),
		&architectNotObtained{cause: errors.New("provider exited 1")},
		errors.New("the TestEditGranted record could not be written durably: disk full"),
		context.Canceled,
	} {
		if got := e.closePlanAdmission(task, cause); got != cause {
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

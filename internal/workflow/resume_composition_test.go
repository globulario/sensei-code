package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/runreceipt"
	"github.com/globulario/sensei-code/internal/session"
)

// COMPOSITION WITNESSES for the three findings of the canonical review of #194
// at b23a8ae. Each drives the real Engine.Resume planned path; Sensei is
// deliberately unstartable (or the context cancelled) so the invocation ends
// at its first step, which is exactly where the defects lived.

// resumeHarness is a real repository holding a recorded candidate for task-r,
// an engine over a durable session in it, and the task as a restart finds it.
func resumeHarness(t *testing.T, work bool) (*Engine, <-chan event.Event, session.Interrupted) {
	t.Helper()
	repo, base := mintRepo(t)
	workspace, err := repo.CreateWorktreeAt(context.Background(), "task-r", base)
	if err != nil {
		t.Fatalf("create the candidate worktree: %v", err)
	}
	if work {
		if err := os.WriteFile(filepath.Join(workspace, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	id := candidate.Identity{TaskID: "task-r", Repository: repo.Root, BaseSHA: base, Worktree: workspace,
		Branch: repo.WorktreeBranch("task-r"), CreatedAt: time.Now().UTC()}
	if err := id.Save(repo.Root); err != nil {
		t.Fatal(err)
	}
	store, err := session.New(repo.Root, "session-r")
	if err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	events, cancel := bus.Subscribe(128)
	t.Cleanup(cancel)
	e := New(repo, config.Default(), bus, store, "session-r")
	e.Config.Sensei.Command = "/nonexistent/awareness-mcp"
	return e, events, session.Interrupted{TaskID: "task-r", Task: "the objective", Planned: true}
}

// rootedResumeHarness is resumeHarness over a record that holds task-r's
// canonical TaskCreated root, written by the holder session that created the
// task, and an engine that is a FRESH process over that record (70B2a1): it
// acts under a fresh SessionID, so it may record nothing of task-r until
// Engine.Resume has bound it to the task's session lineage. It returns the
// holder session too, which keeps owning the root.
func rootedResumeHarness(t *testing.T, work bool) (*Engine, <-chan event.Event, session.Interrupted, string) {
	t.Helper()
	e, events, task := resumeHarness(t, work)
	holder := e.SessionID
	if err := seedAppend(t, e.Store, event.New(holder, task.TaskID, event.SourceUser, event.TaskCreated, task.Task, nil)); err != nil {
		t.Fatalf("record the task's root: %v", err)
	}
	fresh, err := session.FreshID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.SessionID = fresh
	return e, events, task, holder
}

// assertBoundThroughTheLineage holds what a successful resume over a rooted
// record owes: the fresh session became the task's current session through the
// production lineage binding, the root stays owned by the holder, and every
// event the resumed invocation published was written under the fresh session.
func assertBoundThroughTheLineage(t *testing.T, e *Engine, task session.Interrupted, holder string, seen []event.Event) {
	t.Helper()
	lineage, err := e.Store.TaskSessionLineage(task.TaskID)
	if err != nil {
		t.Fatalf("the resumed task has no session lineage: %v", err)
	}
	if lineage.HolderSessionID != holder || lineage.Tip() != e.SessionID {
		t.Fatalf("the lineage is holder %q tip %q; want holder %q tip %q", lineage.HolderSessionID, lineage.Tip(), holder, e.SessionID)
	}
	recorded, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range recorded {
		if ev.Kind == event.TaskCreated && ev.SessionID != holder {
			t.Fatalf("the root is owned by %q, not the holder %q", ev.SessionID, holder)
		}
	}
	for _, ev := range seen {
		if ev.TaskID == task.TaskID && ev.SessionID != e.SessionID {
			t.Fatalf("the resumed invocation published %s under session %q, not its own %q", ev.Kind, ev.SessionID, e.SessionID)
		}
	}
}

func settleResume(t *testing.T, events <-chan event.Event) []event.Event {
	t.Helper()
	var seen []event.Event
	deadline := time.After(15 * time.Second)
	for {
		select {
		case ev := <-events:
			seen = append(seen, ev)
			switch ev.Kind {
			case event.WorkflowFailed, event.WorkflowStopped, event.WorkflowTimedOut, event.WorkflowCompleted,
				event.WorkflowBlockedExternal, event.WorkflowNotConverged, event.WorkflowAwaitingAuthority:
				return seen
			}
		case <-deadline:
			t.Fatalf("the resumed invocation did not settle: %v", kinds(seen))
		}
	}
}

// Finding 3. A resumed invocation that fails before implement measured anything
// must not claim "no candidate" beside work sitting on disk -- and a clean
// worktree is still honestly NONE.
func TestAResumeThatFailsEarlyDoesNotDenyTheCandidateOnDisk(t *testing.T) {
	for name, tc := range map[string]struct {
		work bool
		want runreceipt.CandidateState
	}{
		"work on disk":   {true, runreceipt.CandidatePresent},
		"clean worktree": {false, runreceipt.CandidateNone},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, task, holder := rootedResumeHarness(t, tc.work)
			e.Resume(context.Background(), task)
			seen := settleResume(t, events)
			assertBoundThroughTheLineage(t, e, task, holder, seen)
			rec := receiptFrom(t, seen)
			if rec.CandidateState != tc.want {
				t.Fatalf("the resumed receipt says candidate_state %q; the inherited candidate is %q", rec.CandidateState, tc.want)
			}
		})
	}
}

// Finding 2. A resumed invocation ends through the same classifier as execute:
// a caller stop is STOPPED, not a final failure that makes the task vanish.
//
// The stop arrives AFTER the fresh session is bound (70B2a1): a caller that
// has already cancelled takes no record lock, so its resume is a pre-binding
// refusal and not a stop of the task. Sensei here is a process that never
// answers, so the invocation is still starting it when the caller stops it.
// The stop is given once the resumed mode is published, so no governed append
// is waiting when it arrives: a cancelled wait halts its invocation
// (TestB2a1R193W5CallerCancellationReachesAProductionRecordLockWait), and
// what this test asks about is the stop of a run, not of an append.
func TestAResumeStoppedByItsCallerIsNotAFailure(t *testing.T) {
	e, events, task, holder := rootedResumeHarness(t, true)
	e.Config.Sensei.Command, e.Config.Sensei.Args = "sleep", []string{"30"}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	e.Resume(ctx, task)
	var seen []event.Event
	for deadline := time.After(15 * time.Second); !contains(seen, event.ModeSelected); {
		select {
		case ev := <-events:
			seen = append(seen, ev)
		case <-deadline:
			t.Fatalf("the resume was not bound and running before its caller stopped it: %v %v", kinds(seen), taskFailure(e, task.TaskID))
		}
	}
	if lineage, err := e.Store.TaskSessionLineage(task.TaskID); err != nil || lineage.Tip() != e.SessionID {
		t.Fatalf("premise: the resumed mode was published before the fresh session was bound: %v", err)
	}
	cancel()
	seen = append(seen, settleResume(t, events)...)
	assertBoundThroughTheLineage(t, e, task, holder, seen)
	if contains(seen, event.WorkflowFailed) {
		t.Fatalf("a resume stopped by its caller was recorded as a final failure: %v", kinds(seen))
	}
	if !contains(seen, event.WorkflowStopped) {
		t.Fatalf("a resume stopped by its caller did not end STOPPED: %v", kinds(seen))
	}
}

// Finding 2, the case that made it P1. A deferred authority decision reaching
// the shared classifier ends nothing further -- no failure, no stop -- so the
// preserved question stays resumable.
func TestADeferredQuestionIsNeverRecordedAsAnEnding(t *testing.T) {
	e, events, _ := resumeHarness(t, false)
	e.beginReceipt("task-r")
	e.terminateRun(context.Background(), "task-r", "the objective", errAuthorityDeferred)
	if seen := drainEvents(events); contains(seen, event.WorkflowFailed) || contains(seen, event.WorkflowStopped) {
		t.Fatalf("a deferred question was recorded as an ending: %v", kinds(seen))
	}
}

// Finding 1. A plan moves ALL of its scope: the prose, the files a candidate is
// captured under, and the prospective surfaces it is inspected against.
func TestAPlanMovesItsWholeScope(t *testing.T) {
	tc := taskContext{Rationale: "old", Files: []string{"old.go"}, Steps: []string{"old"},
		Consequences: "old", Invariants: []string{"old"}, Prospective: []ProspectiveSurface{{Path: "old_new.go"}}}
	applyPlanScope(&tc, architectureDecision{Summary: "new", Files: []string{"a.go", "b.go"}, Steps: []string{"s"},
		Mode: string(ModeModify), Consequences: "c", Invariants: []string{"i"},
		ProspectiveSurfaces: []ProspectiveSurface{{Path: "created.go"}}})
	if tc.Rationale != "new" || strings.Join(tc.Files, ",") != "a.go,b.go" || strings.Join(tc.Steps, ",") != "s" ||
		tc.Consequences != "c" || strings.Join(tc.Invariants, ",") != "i" || tc.Mode != ModeModify ||
		len(tc.Prospective) != 1 || tc.Prospective[0].Path != "created.go" {
		t.Fatalf("a plan moved only part of its scope: %+v", tc)
	}
	// And both places a plan takes effect use it, so neither can move part.
	for _, fn := range []string{"execute", "ResumeTask"} {
		if !strings.Contains(funcBody(t, "internal/workflow/engine.go", fn), "applyPlanScope") {
			t.Errorf("%s applies a plan without the one scope mapping", fn)
		}
	}
}

// DF-24a -- the LIFECYCLE boundary of the restoration containment repair.
//
// The predicate halves are in testedit_test.go, beside the comparison that was
// wrong. These witness what the wrong comparison DID: a restoration refusal was
// emitted as WorkflowFailed, which FindInterrupted reads as final, so the safety
// check permanently removed the obligation it was protecting
// (task-1789960053774525922, 2026-09-21, 0 vs 7). Every witness here therefore
// drives a real durable session, ends through the ONE terminal classifier
// terminateRun, and then REOPENS the session as a restarted process would.
//
// W3, W4, W5 and W9 are CONTROLS. W2 proves the known
// 0-DERIVED-versus-recorded-AUTHORED shape is not reported as a DERIVED
// mismatch.

// W2. LEGACY AUTHORED MISSING-PROVENANCE WITNESS. The parked task refuses with a
// specific reason, performs no implementation work, writes no authority, and
// REMAINS PARKED AND VISIBLE.
func TestW2ALegacyAuthoredRecordRefusesNonDestructivelyAndStaysVisible(t *testing.T) {
	root := t.TempDir()
	e, events, store := blockedEngine(t, root, "session-w2")
	const task = "task-w2"
	planned := []string{rrS, rrF}

	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", proposedPlan{architectureDecision: architectureDecision{Decision: "proceed"}, PlanSource: PlanByArchitect}))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted,
		"existing-test edit authority recorded from AUTHORED production governance", rrPayload(t, authoredGrant(t))))

	found := reopen(t, root, "session-w2")
	if len(found) != 1 || len(found[0].TestEditRecord) == 0 {
		t.Fatalf("premise: a parked task carrying an AUTHORED record: %+v", found)
	}
	// The DERIVED instrument authorises NOTHING here: no derived anchor covers
	// the neighbour. This is the exact measured shape.
	fresh := rrDerivedRecomputation(t, planned, nil)
	if len(fresh) != 0 {
		t.Fatalf("premise: the DERIVED instrument recomputes nothing: %+v", fresh)
	}

	e.beginReceipt(task)
	err := e.restoreTestEditGrants(found[0], fresh, planned, teWorld)
	r := refusalOf(t, err)
	assertNotTheFalseDiagnosis(t, r, err)
	if r.Binding != RestorationAuthoredUnverifiable || r.Instrument != RestorationInstrumentAuthored {
		t.Fatalf("the refusal does not name the AUTHORED instrument it could not verify: %+v", r)
	}
	if r.Measured == nil || r.Measured.RecordedAuthored != 1 || r.Measured.RecordedDerived != 0 || r.Measured.RecomputedDerived != 0 {
		t.Fatalf("the refusal does not state what each instrument measured: %+v", r.Measured)
	}
	if !strings.Contains(r.Detail, rrF) {
		t.Errorf("the refusal does not name the grant it could not verify: %q", r.Detail)
	}

	// The invocation ends through the SAME classifier Resume uses.
	e.terminateRun(fixtureCtx(e, task), task, "the objective", err)
	seen := drainEvents(events)
	if contains(seen, event.WorkflowFailed) {
		t.Fatalf("a restoration refusal was recorded as task failure: %v", kinds(seen))
	}
	if !contains(seen, event.WorkflowRestorationRefused) {
		t.Fatalf("no restoration-refusal terminal: %v", kinds(seen))
	}
	back, perr := ParseRestorationRefusal(terminalPayload(t, seen, event.WorkflowRestorationRefused))
	if perr != nil {
		t.Fatalf("the durable refusal does not read back: %v", perr)
	}
	if back.TaskID != task || back.Binding != RestorationAuthoredUnverifiable || back.Subject != restorationSubjectTestEdit {
		t.Fatalf("the durable refusal says something else: %+v", back)
	}

	// The receipt is a COMPLETE-able positive claim that names the instrument.
	rec := receiptFrom(t, seen)
	if rec.Outcome != runreceipt.OutcomeRestorationRefused {
		t.Fatalf("receipt outcome %q, want RESTORATION_REFUSED", rec.Outcome)
	}
	if rec.RestorationRefusal.State != runreceipt.Known {
		t.Fatalf("the receipt does not state what could not be restored: %+v", rec.RestorationRefusal)
	}
	if _, missing := rec.Completeness(); len(missing) > 0 {
		for _, m := range missing {
			if strings.Contains(m, "restoration_refusal") || strings.HasPrefix(m, "candidate_") {
				t.Fatalf("the refusal receipt is incomplete about its own claim: %s", m)
			}
		}
	}

	// NO IMPLEMENTATION WORK, NO AUTHORITY WRITTEN. The only events the resume
	// added are its own account of the refusal.
	after := storeKinds(t, store)
	for _, k := range after[3:] {
		switch k {
		case event.RunReceipt, event.WorkflowRestorationRefused:
		default:
			t.Fatalf("the refusing resume did something else: %v", after)
		}
	}
	if len(e.testEditGrants(task)) != 0 {
		t.Fatal("authority was installed by a resume that refused it")
	}

	// AND IT IS STILL THERE. The whole repair: the task a refusal protects
	// must survive the refusal.
	reopened := reopen(t, root, "session-w2")
	if len(reopened) != 1 || reopened[0].TaskID != task || !reopened[0].Planned {
		t.Fatalf("the refusal destroyed the task it was protecting: %+v", reopened)
	}
	if len(reopened[0].TestEditRecord) == 0 {
		t.Fatal("the task lost the record the refusal was about")
	}
	if len(reopened[0].RestorationRefused) == 0 {
		t.Fatal("the task carries no evidence of why the last attempt did not execute")
	}
}

// W3. REPEATED LEGACY RESUME CONTROL. The same unchanged task refuses the same
// way twice: the first attempt wrote nothing that changes the second outcome.
func TestW3ARepeatedLegacyResumeProducesTheSameRefusal(t *testing.T) {
	root := t.TempDir()
	e, _, _ := blockedEngine(t, root, "session-w3")
	const task = "task-w3"
	planned := []string{rrS, rrF}
	record := rrPayload(t, authoredGrant(t))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", proposedPlan{architectureDecision: architectureDecision{Decision: "proceed"}, PlanSource: PlanByArchitect}))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded", record))
	fresh := rrDerivedRecomputation(t, planned, nil)

	attempt := func(engine *Engine) *RestorationRefusal {
		found := reopen(t, root, "session-w3")
		if len(found) != 1 {
			t.Fatalf("the task is not resumable: %+v", found)
		}
		engine.beginReceipt(task)
		err := engine.restoreTestEditGrants(found[0], fresh, planned, teWorld)
		r := refusalOf(t, err)
		engine.terminateRun(fixtureCtx(e, task), task, "the objective", err)
		return r
	}

	first := attempt(e)
	// A NEW process: nothing carried in memory, only what the first one wrote.
	second, _, _ := blockedEngine(t, root, "session-w3")
	again := attempt(second)

	if first.Binding != again.Binding || first.Instrument != again.Instrument || first.Detail != again.Detail {
		t.Fatalf("the second resume refused differently:\n first: %+v\nsecond: %+v", first, again)
	}
	if first.Measured == nil || again.Measured == nil || *first.Measured != *again.Measured {
		t.Fatalf("the measured quantities moved between two identical resumes: %+v vs %+v", first.Measured, again.Measured)
	}
	if len(second.testEditGrants(task)) != 0 {
		t.Fatal("the second resume was handed authority the first one manufactured")
	}
	// The record the second resume read is byte-for-byte the one the first
	// read: no repair, no rewrite, no promotion.
	if got := reopen(t, root, "session-w3"); len(got) != 1 || string(got[0].TestEditRecord) != string(record) {
		t.Fatalf("the grant record changed across two refusing resumes: %s", string(got[0].TestEditRecord))
	}
}

// W4, the LIFECYCLE half. A real disagreement inside the DERIVED instrument
// still refuses execution -- and still preserves the task and names the failed
// DERIVED binding. The predicate half is
// TestW4ADerivedMismatchNamesTheDerivedBindingAndInstallsNothing.
func TestW4ADerivedMismatchRefusesExecutionAndPreservesTheTask(t *testing.T) {
	root := t.TempDir()
	e, events, _ := blockedEngine(t, root, "session-w4")
	const task = "task-w4"
	planned := []string{teS, teF}
	forged := derivedGrant(t)
	forged.BaseHash = "not the bytes at the pinned base"
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", proposedPlan{architectureDecision: architectureDecision{Decision: "proceed"}, PlanSource: PlanByArchitect}))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded", rrPayload(t, forged)))

	found := reopen(t, root, "session-w4")
	e.beginReceipt(task)
	err := e.restoreTestEditGrants(found[0], rrDerivedRecomputation(t, planned, teCovered()), planned, teWorld)
	r := refusalOf(t, err)
	if r.Binding != RestorationDerivedMismatch || r.Instrument != RestorationInstrumentDerived {
		t.Fatalf("a DERIVED disagreement was not named as one: %+v", r)
	}
	if len(e.testEditGrants(task)) != 0 {
		t.Fatal("authority was installed over a DERIVED disagreement")
	}

	e.terminateRun(fixtureCtx(e, task), task, "the objective", err)
	seen := drainEvents(events)
	if contains(seen, event.WorkflowFailed) || !contains(seen, event.WorkflowRestorationRefused) {
		t.Fatalf("a DERIVED mismatch is still a restoration refusal, not a task failure: %v", kinds(seen))
	}
	// A LEGITIMATE refusal is non-destructive too: the containment is not
	// reserved for the AUTHORED case that motivated it.
	got := reopen(t, root, "session-w4")
	if len(got) != 1 || got[0].TaskID != task || !got[0].Planned {
		t.Fatalf("a refused DERIVED restoration destroyed the task: %+v", got)
	}
	if len(got[0].RestorationRefused) == 0 {
		t.Fatal("the preserved task does not carry why the DERIVED restoration was refused")
	}
}

// W5. RESUME SIDE-EFFECT-FREE CONTROL. Authority-relevant durable state is
// measured before and after BOTH a successful DERIVED-only restoration and a
// failed AUTHORED-containing one. A resume computes and compares; it never
// records, repairs, rewrites, promotes or backfills authority.
func TestW5RestorationWritesNoAuthorityWhetherItSucceedsOrRefuses(t *testing.T) {
	for name, c := range map[string]struct {
		grants  []testEditGrant
		planned []string
		covered []CoverageAnchor
		refuses bool
	}{
		"successful DERIVED-only restoration": {[]testEditGrant{derivedGrant(t)}, []string{teS, teF}, teCovered(), false},
		"refused AUTHORED-containing record":  {[]testEditGrant{authoredGrant(t)}, []string{rrS, rrF}, nil, true},
	} {
		root := t.TempDir()
		e, _, store := blockedEngine(t, root, "session-w5")
		const task = "task-w5"
		e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
		e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", proposedPlan{architectureDecision: architectureDecision{Decision: "proceed"}, PlanSource: PlanByArchitect}))
		e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded", rrPayload(t, c.grants...)))

		before := authorityEvidence(t, store)
		found := reopen(t, root, "session-w5")
		e.beginReceipt(task)
		err := e.restoreTestEditGrants(found[0], rrDerivedRecomputation(t, c.planned, c.covered), c.planned, teWorld)
		if c.refuses {
			refusalOf(t, err)
			e.terminateRun(fixtureCtx(e, task), task, "the objective", err)
		} else if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		after := authorityEvidence(t, store)
		if strings.Join(before, "\n") != strings.Join(after, "\n") {
			t.Fatalf("%s: the resume mutated authority evidence:\nbefore %v\n after %v", name, before, after)
		}
	}
	// AND AT THE CALLER, not only inside the predicate. Measuring durable state
	// around restoreTestEditGrants proves the predicate records nothing; a write
	// placed in the RESTORATION SEGMENT OF RESUME ITSELF would sit above that
	// measurement and survive it. Resume is the caller whose SECOND invocation
	// read the first one's write as the run's own record, so the property is
	// pinned where it was broken: the whole resumed path names no
	// authority-recording call, on the refusal path or the success path.
	resume := funcBody(t, "internal/workflow/engine.go", "ResumeTask")
	for _, recording := range []string{"TestEditGranted", "ProspectiveGranted", "setTestEditGrants(", "setProspectiveGrants("} {
		if strings.Contains(resume, recording) {
			t.Errorf("Resume records authority (%s); a resume computes and compares, and a second resume would read this write as the run's own record", recording)
		}
	}
}

// W6, the LIFECYCLE half. A record holding BOTH instruments refuses on the true
// AUTHORED blocker at the full-resume boundary, and the task survives it. The
// predicate half -- that DERIVED was checked against recorded DERIVED alone --
// is TestW6AMixedRecordChecksDerivedOnlyAgainstRecordedDerived.
func TestW6AMixedRecordRefusesOnTheAuthoredBlockerAndPreservesTheTask(t *testing.T) {
	root := t.TempDir()
	e, events, store := blockedEngine(t, root, "session-w6")
	const task = "task-w6"
	planned := []string{teS, teF, rrS, rrF}
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", proposedPlan{architectureDecision: architectureDecision{Decision: "proceed"}, PlanSource: PlanByArchitect}))
	e.emitIn(fixtureCtx(e, task), event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded",
		rrPayload(t, derivedGrant(t), authoredGrant(t))))

	found := reopen(t, root, "session-w6")
	e.beginReceipt(task)
	err := e.restoreTestEditGrants(found[0], rrDerivedRecomputation(t, planned, teCovered()), planned, teWorld)
	r := refusalOf(t, err)
	assertNotTheFalseDiagnosis(t, r, err)
	if r.Binding != RestorationAuthoredUnverifiable || r.Instrument != RestorationInstrumentAuthored {
		t.Fatalf("the mixed record refused on something other than its AUTHORED blocker: %+v", r)
	}
	if r.Measured == nil || r.Measured.RecordedDerived != 1 || r.Measured.RecomputedDerived != 1 || r.Measured.RecordedAuthored != 1 {
		t.Fatalf("the two instruments were not counted separately: %+v", r.Measured)
	}

	e.terminateRun(fixtureCtx(e, task), task, "the objective", err)
	seen := drainEvents(events)
	if contains(seen, event.WorkflowFailed) || !contains(seen, event.WorkflowRestorationRefused) {
		t.Fatalf("a mixed record was not refused non-destructively: %v", kinds(seen))
	}
	// Not a successful mixed restoration: nothing is operational, and nothing
	// about the record moved.
	if len(e.testEditGrants(task)) != 0 {
		t.Fatal("a mixed record installed authority")
	}
	if got := reopen(t, root, "session-w6"); len(got) != 1 || got[0].TaskID != task || !got[0].Planned {
		t.Fatalf("a refused mixed restoration destroyed the task: %+v", got)
	}
	for _, k := range storeKinds(t, store)[3:] {
		switch k {
		case event.RunReceipt, event.WorkflowRestorationRefused:
		default:
			t.Fatalf("the refusing resume of a mixed record did something else: %v", storeKinds(t, store))
		}
	}
}

// W9. NO-REDUCED-SET EXECUTION CONTROL. When DERIVED verifies and AUTHORED
// cannot, execution does not continue with the DERIVED subset.
func TestW9VerifiedDerivedAuthorityDoesNotExecuteWithoutTheAuthoredPart(t *testing.T) {
	planned := []string{teS, teF, rrS, rrF}
	fresh := rrDerivedRecomputation(t, planned, teCovered())
	derived, authored := derivedGrant(t), authoredGrant(t)

	// The DERIVED half ALONE would have restored: the refusal below is about
	// the AUTHORED half, not about a broken derived one.
	control := &Engine{}
	if err := control.restoreTestEditGrants(rrRecord(t, "t", derived), fresh, planned, teWorld); err != nil {
		t.Fatalf("premise: the DERIVED half verifies on its own: %v", err)
	}
	if len(control.testEditGrants("t")) != 1 {
		t.Fatal("premise: the DERIVED half installs on its own")
	}

	e := &Engine{}
	err := e.restoreTestEditGrants(rrRecord(t, "t", derived, authored), fresh, planned, teWorld)
	r := refusalOf(t, err)
	if r.Binding != RestorationAuthoredUnverifiable {
		t.Fatalf("the refusal is not the AUTHORED one: %+v", r)
	}
	if got := e.testEditGrants("t"); len(got) != 0 {
		t.Fatalf("execution continued under a reduced grant set: %+v", got)
	}
	if !strings.Contains(r.Detail, "does not continue under the DERIVED subset") {
		t.Errorf("the refusal does not say the reduced set was refused: %q", r.Detail)
	}
}

// storeKinds is the durable event order, read back from disk.
func storeKinds(t *testing.T, store *session.Store) []event.Kind {
	t.Helper()
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return kinds(history)
}

// authorityEvidence is the authority-relevant durable state: every recorded
// grant and every recorded authority decision, in order, byte for byte.
func authorityEvidence(t *testing.T, store *session.Store) []string {
	t.Helper()
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, ev := range history {
		switch ev.Kind {
		case event.TestEditGranted, event.ProspectiveGranted, event.AuthorityRequired, event.AuthorityResolved:
			out = append(out, string(ev.Kind)+" "+string(ev.Payload))
		}
	}
	return out
}

// withholdRecord makes the session record of store unable to take or yield
// anything -- a directory stands where the record file was -- until the
// returned restore puts the record back exactly as it was. It is a real,
// transient failure of the session record, not a substitute for one.
func withholdRecord(t *testing.T, e *Engine) (restore func()) {
	t.Helper()
	return replaceRecord(t, e, func(path string) error { return os.Mkdir(path, 0o700) })
}

// rewindRecord makes the session record read, until the returned restore puts
// the record back exactly as it was, as the earlier record content: a real,
// transient state of the record in which the task's lineage is what content
// establishes.
func rewindRecord(t *testing.T, e *Engine, content []byte) (restore func()) {
	t.Helper()
	return replaceRecord(t, e, func(path string) error { return os.WriteFile(path, content, 0o600) })
}

func recordPath(e *Engine) string {
	return filepath.Join(e.Repo.Root, ".sensei-code", "sessions", "session-r", "events.jsonl")
}

func replaceRecord(t *testing.T, e *Engine, stand func(path string) error) (restore func()) {
	t.Helper()
	path := recordPath(e)
	held := path + ".held"
	if err := os.Rename(path, held); err != nil {
		t.Fatal(err)
	}
	if err := stand(path); err != nil {
		t.Fatal(err)
	}
	restored := false
	restore = func() {
		if restored {
			return
		}
		restored = true
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(held, path); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(restore)
	return restore
}

// quiet collects what events delivers until nothing arrives for a while.
func quiet(events <-chan event.Event) []event.Event {
	var out []event.Event
	for {
		select {
		case ev := <-events:
			out = append(out, ev)
		case <-time.After(200 * time.Millisecond):
			return out
		}
	}
}

// A1-W10 APPEND ERROR, THROUGH THE RESUMED RUN'S OWN ENDING (70B2a1, review
// f1): a resumed invocation, bound to the task's lineage, begins its plan
// attempt and the session record refuses the durable PlanAttemptStarted record
// on session authority -- the record, transiently, does not name the bound
// session as the task's current one. The condition then clears. The run ends through its own termination
// path, and what it ends with is the ORIGINAL typed failure: no receipt,
// terminal, status, refusal or error report is recorded or published after
// it, and nothing is published in place of the record that was refused.
func TestB2a1W10ARefusedPlanAttemptStartIsTheResumedInvocationsOutcome(t *testing.T) {
	e, events, task, holder := rootedResumeHarness(t, false)
	inv, err := e.admitInvocation(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	defer e.endInvocation(inv)
	ctx := inv.ctx
	unbound, err := os.ReadFile(recordPath(e))
	if err != nil {
		t.Fatal(err)
	}
	if f := leaseAndBind(e, inv, task.TaskID); f != nil {
		t.Fatalf("premise: the fresh session was not bound: %v", f)
	}
	before, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}

	restore := rewindRecord(t, e, unbound)
	_, refused := e.beginPlanAttempt(ctx, task.TaskID, task.Task, architectureDecision{Decision: "proceed", Summary: "the plan"})
	original := appendFailureIn(refused)
	if original == nil || original.Kind != event.PlanAttemptStarted || original.SessionID != e.SessionID ||
		!strings.Contains(original.Error(), "not task "+task.TaskID+"'s current session") {
		t.Fatalf("the refused plan-attempt start is not the typed append failure of that record: %v", refused)
	}
	select {
	case <-inv.halted:
	default:
		t.Fatal("a refused durable append did not halt the invocation")
	}
	if ctx.Err() == nil {
		t.Fatal("a refused durable append did not cancel the resumed run")
	}
	// The condition clears: the record could take an append again.
	restore()

	// The run ends as a run whose routing failed ends.
	e.terminateRun(ctx, task.TaskID, task.Task, refused)
	e.emitIn(fixtureCtx(e, task.TaskID), event.New(e.SessionID, task.TaskID, event.SourceSystem, event.Status, "dependent", nil))
	if err := e.emitDurableIn(fixtureCtx(e, task.TaskID), event.New(e.SessionID, task.TaskID, event.SourceSystem, event.PlanAttemptRefused, "dependent", nil)); err == nil {
		t.Fatal("a halted invocation recorded a dependent durable event")
	}

	if f := taskFailure(e, task.TaskID); f != original {
		t.Fatalf("the invocation's outcome is %v, not the original refused append %v", f, original)
	}
	if got := quiet(events); len(got) != 0 {
		t.Fatalf("after the refused append the invocation published %v", kinds(got))
	}
	after, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("after the refused append the record took %v", kinds(after[len(before):]))
	}
	if lineage, err := e.Store.TaskSessionLineage(task.TaskID); err != nil || lineage.HolderSessionID != holder || lineage.Tip() != e.SessionID {
		t.Fatalf("the task's lineage changed under the halt: %+v %v", lineage, err)
	}
}

// f3 (70B2a1): a halt ends the invocation it halted, not the task. A first
// Resume whose lineage binding is refused by a transient failure of the record
// ends with that typed failure; once the record is repaired, a second Resume of
// the same task in the same engine binds and runs exactly once, under a fresh
// outcome slot, while the first failure stays attributable to the first
// invocation only.
func TestB2a1R193W7AResumeAfterAHaltedResumeProceedsOnce(t *testing.T) {
	e, events, task, holder := rootedResumeHarness(t, false)

	restore := withholdRecord(t, e)
	firstAttempt := e.ResumeTask(context.Background(), task)
	select {
	case <-firstAttempt.Bound():
	case <-time.After(15 * time.Second):
		t.Fatal("the refused resume left its caller waiting")
	}
	first := firstAttempt.Binding().Refusal
	if first == nil || first.Kind != session.SessionLineageBound || first.SessionID != e.SessionID ||
		firstAttempt.Binding().CurrentSessionID != "" {
		t.Fatalf("the first resume did not end with its pre-binding refusal: %+v", firstAttempt.Binding())
	}
	select {
	case <-firstAttempt.Ended():
	case <-time.After(15 * time.Second):
		t.Fatal("the refused resume did not end")
	}
	if got := quiet(events); len(got) != 0 {
		t.Fatalf("a resume refused before binding published %v", kinds(got))
	}
	restore()

	secondAttempt := e.ResumeTask(context.Background(), task)
	seen := settleResume(t, events)
	if b := secondAttempt.Binding(); b.Refusal != nil || b.CurrentSessionID != e.SessionID || b.TaskID != task.TaskID {
		t.Fatalf("the second resume's own outcome is not its verified binding: %+v", b)
	}
	assertBoundThroughTheLineage(t, e, task, holder, seen)
	if n := countKind(seen, event.ModeSelected); n != 1 {
		t.Fatalf("the second resume ran %d times, not once: %v", n, kinds(seen))
	}
	if !contains(seen, event.WorkflowFailed) {
		t.Fatalf("the second resume did not run to its own ending: %v", kinds(seen))
	}
	select {
	case <-secondAttempt.Ended():
	case <-time.After(15 * time.Second):
		t.Fatal("the second resume did not end after its terminal")
	}
	if f := secondAttempt.Failure(); f != nil {
		t.Fatalf("the second resume was halted: %v", f)
	}
	if f := taskFailure(e, task.TaskID); f != nil {
		t.Fatalf("the first invocation's failure was carried into the second: %v", f)
	}
	if again := firstAttempt.Binding().Refusal; again != first || firstAttempt.Failure() != first {
		t.Fatalf("the first invocation's failure is no longer its own: %v", again)
	}
	recorded, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if n := countKind(recorded, session.SessionLineageBound); n != 1 {
		t.Fatalf("the record holds %d lineage transitions, not the second resume's one", n)
	}
}

func countKind(evs []event.Event, kind event.Kind) int {
	n := 0
	for _, ev := range evs {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

// appendFailureIn is the typed append failure in err's chain, if any.
func appendFailureIn(err error) *RecordAppendFailure {
	for err != nil {
		if f, ok := err.(*RecordAppendFailure); ok {
			return f
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return nil
		}
		err = u.Unwrap()
	}
	return nil
}

// THE CALLER'S CONTEXT REACHES A PRODUCTION RECORD-LOCK WAIT (70B2a1 review
// f2, RULING-191 C): while a resumed invocation is live, a governed append of
// its task waits for the record lock under that invocation's context, derived
// from its caller's. The append below waits exactly as a contended record lock
// makes it wait -- until its context ends -- and then reaches the real Store.
// The caller cancels: the wait ends at once, not after the Store's own bound,
// the Store refuses the append typed (ErrRecordLockCanceled), the task is
// halted with that failure, and nothing is published. CONTROL: the ending of
// a run whose caller has ALREADY withdrawn is accounted for through the
// separately owned terminal path and is recorded.
func TestB2a1R193W5CallerCancellationReachesAProductionRecordLockWait(t *testing.T) {
	e, events, task, _ := rootedResumeHarness(t, false)
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	inv, err := e.admitInvocation(caller, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	defer e.endInvocation(inv)
	ctx := inv.ctx
	if f := leaseAndBind(e, inv, task.TaskID); f != nil {
		t.Fatalf("premise: the fresh session was not bound: %v", f)
	}

	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	waiting := make(chan struct{})
	appendToStore = func(ctx context.Context, s *session.Store, lease *session.TaskLease, durable bool, ev event.Event) error {
		if ev.Kind != event.Status {
			return production(ctx, s, lease, durable, ev)
		}
		close(waiting)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
		return production(ctx, s, lease, durable, ev)
	}
	emitted := make(chan struct{})
	go func() {
		defer close(emitted)
		e.emitIn(fixtureCtx(e, task.TaskID), event.New(e.SessionID, task.TaskID, event.SourceSystem, event.Status, "waits for the record lock", nil))
	}()
	<-waiting
	cancel()
	select {
	case <-emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("the caller's cancellation did not reach the append's wait for the record lock")
	}
	f := taskFailure(e, task.TaskID)
	if f == nil || f.Kind != event.Status || !wraps(f, session.ErrRecordLockCanceled) {
		t.Fatalf("the cancelled wait is not the task's typed append failure: %v", f)
	}
	if ctx.Err() == nil {
		t.Fatal("the failed append did not halt the invocation")
	}
	if got := quiet(events); len(got) != 0 {
		t.Fatalf("a cancelled append published %v", kinds(got))
	}
	appendToStore = production

	t.Run("control: an ending after the caller withdrew is recorded", func(t *testing.T) {
		e, events, task, _ := rootedResumeHarness(t, false)
		caller, cancel := context.WithCancel(context.Background())
		inv, err := e.admitInvocation(caller, task.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		defer e.endInvocation(inv)
		if f := leaseAndBind(e, inv, task.TaskID); f != nil {
			t.Fatalf("premise: the fresh session was not bound: %v", f)
		}
		cancel()
		e.emitIn(fixtureCtx(e, task.TaskID), event.New(e.SessionID, task.TaskID, event.SourceSystem, event.WorkflowStopped, "stopped by its caller", nil))
		if f := taskFailure(e, task.TaskID); f != nil {
			t.Fatalf("the ending of a withdrawn run was not recorded: %v", f)
		}
		if got := quiet(events); len(got) != 1 || got[0].Kind != event.WorkflowStopped {
			t.Fatalf("the recorded ending was not published: %v", kinds(got))
		}
	})
}

// ONE RESUME, ONE TYPED BINDING OUTCOME (70B2a1 review f1, RULING-189 CALLER
// COMPLETION): the invocation's own handle is settled with the VERIFIED
// binding -- the task and the fresh session the holder Store's lineage now
// names current -- without the caller reading any workflow event, and it is
// settled once: a later settlement does not replace it. A refusal settles the
// same handle with the typed failure instead (TestB2a1R193W7AResumeAfterAHaltedResumeProceedsOnce).
func TestB2a1R193W9AResumeAttemptSettlesOnceWithItsVerifiedBinding(t *testing.T) {
	e, events, task, holder := rootedResumeHarness(t, false)
	attempt := e.ResumeTask(context.Background(), task)
	if attempt.TaskID != task.TaskID {
		t.Fatalf("the attempt is for %q", attempt.TaskID)
	}
	select {
	case <-attempt.Bound():
	case <-time.After(15 * time.Second):
		t.Fatal("a resume over a rooted record left its caller waiting for the binding")
	}
	b := attempt.Binding()
	if b.Refusal != nil || b.TaskID != task.TaskID || b.CurrentSessionID != e.SessionID {
		t.Fatalf("the binding outcome is %+v; want task %s bound as %s", b, task.TaskID, e.SessionID)
	}
	if lineage, err := e.Store.TaskSessionLineage(task.TaskID); err != nil || lineage.Tip() != b.CurrentSessionID || lineage.HolderSessionID != holder {
		t.Fatalf("the reported binding is not what the record establishes: %+v %v", lineage, err)
	}
	attempt.settle(ResumeBinding{TaskID: task.TaskID, Refusal: &RecordAppendFailure{TaskID: task.TaskID}})
	if again := attempt.Binding(); again != b {
		t.Fatalf("the settled binding was replaced: %+v", again)
	}
	seen := settleResume(t, events)
	assertBoundThroughTheLineage(t, e, task, holder, seen)
	select {
	case <-attempt.Ended():
	case <-time.After(15 * time.Second):
		t.Fatal("the bound resume did not end after its terminal")
	}
	if f := attempt.Failure(); f != nil {
		t.Fatalf("the bound resume was halted: %v", f)
	}
}

// A1-W10 DURABILITY IS THE APPEND'S (70B2a1 review f1): emitDurable's append
// is Store.AppendDurable and emit's is Store.Append, each observed at the one
// engine call to the Store, and each is published only after that append
// succeeded.
func TestB2a1W10EmitDurableAppendsDurably(t *testing.T) {
	store, err := session.New(t.TempDir(), "sess-A")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	if err := seedAppend(t, store, event.New("sess-A", "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil)); err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	seen, cancel := bus.Subscribe(64)
	defer cancel()
	e := &Engine{Bus: bus, Store: store, SessionID: "sess-A"}

	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	appended := map[event.Kind]bool{}
	appendToStore = func(ctx context.Context, s *session.Store, lease *session.TaskLease, durable bool, ev event.Event) error {
		if got := drainFor(seen); len(got) != 0 {
			t.Errorf("%v was published before %s was appended", got, ev.Kind)
		}
		appended[ev.Kind] = durable
		return production(ctx, s, lease, durable, ev)
	}
	for _, kind := range []event.Kind{event.PlanAttemptStarted, event.ProspectiveGranted, event.TestEditGranted,
		event.PlanAttemptRefused, event.PlanProposed} {
		if err := e.emitDurableIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, kind, "durable", durablePayload(kind))); err != nil {
			t.Fatalf("%s: %v", kind, err)
		}
		if durable, ok := appended[kind]; !ok || !durable {
			t.Fatalf("emitDurable appended %s with durable=%v (appended: %v)", kind, durable, ok)
		}
		if got := drainFor(seen); len(got) != 1 || got[0].Kind != kind {
			t.Fatalf("the durable %s was not published once after its append: %+v", kind, got)
		}
	}
	e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.Status, "ordinary", nil))
	if durable, ok := appended[event.Status]; !ok || durable {
		t.Fatalf("emit appended its event with durable=%v (appended: %v)", durable, ok)
	}
	if got := drainFor(seen); len(got) != 1 || got[0].Kind != event.Status {
		t.Fatalf("the ordinary event was not published after its append: %+v", got)
	}
}

// AN OVERLAPPING RESUME NEVER PROLONGS THE LIVE INVOCATION'S WAIT (70B2a1
// review f3, RULING-191 C, D): invocation A of a task is live and its
// governed append is waiting for the record lock under A's own context. A
// second Resume B of the same task is refused at once with its own typed
// pre-binding refusal (ErrTaskInvocationLive), settled and ended: it neither
// cancels A nor joins A's wait, and records and publishes nothing. When A's
// caller cancels, A's wait ends promptly -- not after any other lifetime --
// with A's typed ErrRecordLockCanceled, and B's outcome stays its own.
func TestB2a1R193W6AnOverlappingResumeIsRefusedAndNeverProlongsTheLiveWait(t *testing.T) {
	e, events, task, _ := rootedResumeHarness(t, false)
	callerA, cancelA := context.WithCancel(context.Background())
	defer cancelA()
	inv, err := e.admitInvocation(callerA, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	defer e.endInvocation(inv)
	ctxA := inv.ctx
	if f := leaseAndBind(e, inv, task.TaskID); f != nil {
		t.Fatalf("premise: A's session was not bound: %v", f)
	}
	before, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}

	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	waiting := make(chan struct{})
	appendToStore = func(ctx context.Context, s *session.Store, lease *session.TaskLease, durable bool, ev event.Event) error {
		if ev.Kind != event.Status {
			return production(ctx, s, lease, durable, ev)
		}
		// Waits exactly as a contended record lock makes it wait.
		close(waiting)
		select {
		case <-ctx.Done():
		case <-time.After(10 * time.Second):
		}
		return production(ctx, s, lease, durable, ev)
	}
	emitted := make(chan struct{})
	go func() {
		defer close(emitted)
		e.emitIn(fixtureCtx(e, task.TaskID), event.New(e.SessionID, task.TaskID, event.SourceSystem, event.Status, "A waits for the record lock", nil))
	}()
	<-waiting

	b := e.ResumeTask(context.Background(), task)
	select {
	case <-b.Bound():
	case <-time.After(5 * time.Second):
		t.Fatal("the overlapping resume left its caller waiting")
	}
	select {
	case <-b.Ended():
	default:
		t.Fatal("the refused overlapping resume did not end")
	}
	refusal := b.Binding().Refusal
	if refusal == nil || !wraps(refusal, ErrTaskInvocationLive) || refusal.Kind != session.SessionLineageBound ||
		b.Binding().CurrentSessionID != "" || b.Failure() != refusal {
		t.Fatalf("the overlapping resume's outcome is %+v, not its own typed refusal", b.Binding())
	}
	if got := e.Resume(context.Background(), task); got != "" {
		t.Fatalf("Resume reported %q resumed while another invocation of it was live", got)
	}
	if ctxA.Err() != nil || taskFailure(e, task.TaskID) != nil {
		t.Fatal("refusing the overlapping resume touched the live invocation")
	}
	select {
	case <-emitted:
		t.Fatal("A's wait ended though nothing ended A")
	case <-time.After(100 * time.Millisecond):
	}

	cancelA()
	start := time.Now()
	select {
	case <-emitted:
	case <-time.After(5 * time.Second):
		t.Fatal("A's caller's cancellation did not reach A's wait for the record lock")
	}
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("A's wait outlived its caller by %v", waited)
	}
	f := taskFailure(e, task.TaskID)
	if f == nil || f.Kind != event.Status || f.SessionID != e.SessionID || !wraps(f, session.ErrRecordLockCanceled) {
		t.Fatalf("A's cancelled wait is not A's typed append failure: %v", f)
	}
	if b.Binding().Refusal != refusal || b.Failure() != refusal {
		t.Fatal("A's failure was attributed to the refused overlapping resume")
	}
	if got := quiet(events); len(got) != 0 {
		t.Fatalf("the overlap published %v", kinds(got))
	}
	appendToStore = production
	after, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the refused resume and the cancelled append took %v", kinds(after[len(before):]))
	}
}

// THE STRING ENTRY POINT RETURNS ONLY A SETTLED OUTCOME (70B2a1 review f4,
// RULING-189 CALLER COMPLETION): Resume returns the task's ID only once its
// invocation is bound -- the holder Store's lineage already names the fresh
// session current when it returns -- and the run then proceeds exactly once.
// Its refusals return "" (TestB2a1W3RootlessResumeHalts,
// TestB2a1R193W6AnOverlappingResumeIsRefusedAndNeverProlongsTheLiveWait).
func TestB2a1ResumeReturnsOnlyABoundInvocation(t *testing.T) {
	e, events, task, holder := rootedResumeHarness(t, false)
	if got := e.Resume(context.Background(), task); got != task.TaskID {
		t.Fatalf("a bound resume answered %q", got)
	}
	if lineage, err := e.Store.TaskSessionLineage(task.TaskID); err != nil || lineage.Tip() != e.SessionID {
		t.Fatalf("Resume returned before its session was bound: %+v %v", lineage, err)
	}
	seen := settleResume(t, events)
	assertBoundThroughTheLineage(t, e, task, holder, seen)
	if n := countKind(seen, event.ModeSelected); n != 1 {
		t.Fatalf("the resume ran %d times, not once: %v", n, kinds(seen))
	}
}

// taskFailure is the failure of taskID's last admitted invocation in e, nil
// when it has none or none was admitted.
func taskFailure(e *Engine, taskID string) *RecordAppendFailure {
	e.mu.Lock()
	defer e.mu.Unlock()
	if inv := e.admitted[taskID]; inv != nil {
		return inv.failure
	}
	return nil
}

// taskHalted is closed once taskID's last admitted invocation in e is halted,
// and stops looking when ctx ends.
func taskHalted(ctx context.Context, e *Engine, taskID string) <-chan struct{} {
	out := make(chan struct{})
	go func() {
		tick := time.NewTicker(5 * time.Millisecond)
		defer tick.Stop()
		for {
			if taskFailure(e, taskID) != nil {
				close(out)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
	return out
}

// RULING-193 W8: A FAILED APPEND CANNOT RACE WITH PUBLICATION (r5 review c3
// f3). Two emissions of one task are in flight in its live invocation. The
// first is inside its append when the second arrives; the second is lawful --
// the record would take it. The first append then fails. Because the failure
// check, the append, the failure fence and the publication of a task happen
// one emission at a time (record), the second neither appends nor publishes:
// it reaches its failure check only after the first failure has halted the
// invocation. Nothing is published.
func TestB2a1R193W8AFailedAppendCannotRaceWithPublication(t *testing.T) {
	store, err := session.New(t.TempDir(), "sess-A")
	if err != nil {
		t.Fatal(err)
	}
	if err := seedAppend(t, store, event.New("sess-A", "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil)); err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	seen, unsubscribe := bus.Subscribe(64)
	defer unsubscribe()
	e := &Engine{Bus: bus, Store: store, SessionID: "sess-A"}
	inv := liveInvocation(t, e, "task-1")

	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	inFirst, failFirst := make(chan struct{}), make(chan struct{})
	appended := make(chan event.Kind, 4)
	appendToStore = func(ctx context.Context, s *session.Store, lease *session.TaskLease, durable bool, ev event.Event) error {
		appended <- ev.Kind
		if ev.Kind == event.PlanAttemptStarted {
			close(inFirst)
			<-failFirst
			return session.ErrRecordLockTimeout
		}
		return production(ctx, s, lease, durable, ev)
	}
	first := make(chan error, 1)
	go func() {
		first <- e.emitDurableIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.PlanAttemptStarted, "fails", nil))
	}()
	<-inFirst
	second := make(chan struct{})
	go func() {
		defer close(second)
		e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.Status, "lawful, but after a failure", nil))
	}()
	// The second emission has every chance to slip in while the first is
	// inside its append.
	select {
	case <-second:
		t.Fatal("the second emission completed while the first was still inside its append")
	case <-time.After(200 * time.Millisecond):
	}
	close(failFirst)
	if err := <-first; !wraps(err, session.ErrRecordLockTimeout) {
		t.Fatalf("the first append's failure was not returned: %v", err)
	}
	select {
	case <-second:
	case <-time.After(5 * time.Second):
		t.Fatal("the second emission never finished")
	}
	close(appended)
	var kindsAppended []event.Kind
	for k := range appended {
		kindsAppended = append(kindsAppended, k)
	}
	if len(kindsAppended) != 1 || kindsAppended[0] != event.PlanAttemptStarted {
		t.Fatalf("after the failed append the store was asked to append %v", kindsAppended)
	}
	if got := quiet(seen); len(got) != 0 {
		t.Fatalf("an emission raced the failed append to publication: %v", kinds(got))
	}
	if f := e.invocationFailure(inv); f == nil || f.Kind != event.PlanAttemptStarted {
		t.Fatalf("the failed append is not the invocation's failure: %v", f)
	}
}

// RULING-193 W4: NIL STORE CANNOT PUBLISH (r5 review c3 f1). An engine with
// no Store records no task-bound event, durable or not: each is the typed
// ErrNoStore failure of the invocation that made it, and nothing is
// published -- there is no bus-only mode. CONTROL: a taskless diagnostic,
// which carries no task authority, is still published.
func TestB2a1R193W4NilStoreCannotPublish(t *testing.T) {
	bus := event.NewBus()
	seen, unsubscribe := bus.Subscribe(64)
	defer unsubscribe()
	e := &Engine{Bus: bus, SessionID: "sess-A"}

	// Work naming no invocation is refused as unbound, and published nowhere.
	err := e.emitDurableIn(t.Context(), event.New("sess-A", "task-1", event.SourceSystem, event.PlanProposed, "durable", nil))
	if f := appendFailureIn(err); f == nil || !wraps(f, errInvocationUnbound) {
		t.Fatalf("unbound work was not refused as unbound: %v", err)
	}
	// The invocation's own durable event with no Store is the typed
	// ErrNoStore failure, and halts it.
	inv := liveInvocation(t, e, "task-1")
	err = e.emitDurableIn(inv.ctx, event.New("sess-A", "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil))
	if f := appendFailureIn(err); f == nil || f.Kind != event.TaskCreated || !wraps(f, session.ErrNoStore) {
		t.Fatalf("a durable event with no Store is not the typed ErrNoStore failure: %v", err)
	}
	if f := e.invocationFailure(inv); f == nil || f.Kind != event.TaskCreated || !wraps(f, session.ErrNoStore) {
		t.Fatalf("an event with no Store did not halt its invocation with ErrNoStore: %v", f)
	}
	e.emitIn(inv.ctx, event.New("sess-A", "task-1", event.SourceSystem, event.WorkflowFailed, "the substitute", nil))
	if got := quiet(seen); len(got) != 0 {
		t.Fatalf("an engine with no Store published %v", kinds(got))
	}
	// A taskless record that is not a registered diagnostic is published
	// nowhere, even with no Store to refuse it: the engine applies the closed
	// registry itself (session.ValidDiagnosticEvent).
	for name, bad := range map[string]event.Event{
		"a terminal":      event.New("sess-A", "", event.SourceSystem, event.WorkflowCompleted, "done", nil),
		"an agent status": event.New("sess-A", "", event.SourceClaude, event.Status, "x", nil),
	} {
		if err := e.emitDiagnostic(bad); err == nil {
			t.Errorf("with no Store, %s of no task was taken as a diagnostic", name)
		}
	}
	if got := quiet(seen); len(got) != 0 {
		t.Fatalf("with no Store, an unregistered taskless record was published: %v", kinds(got))
	}
	// CONTROL: a registered taskless diagnostic is published without a Store.
	if err := e.emitDiagnostic(event.New("sess-A", "", event.SourceSystem, event.Status, "a taskless diagnostic", nil)); err != nil {
		t.Fatal(err)
	}
	if got := quiet(seen); len(got) != 1 || got[0].TaskID != "" {
		t.Fatalf("control: the taskless diagnostic was not published: %v", kinds(got))
	}
}

// RULING-193 W11: CURRENTSESSIONID IS OPERATIVE ONLY AFTER ITS DURABLE BIND
// (70B2a1 r5 review c1 f3). A resumed invocation's fresh session appends
// nothing of the task until the holder's durable record -- read back from
// disk at the moment of each append -- names it the task's current session;
// the attempt's settled binding is exactly what that record names; and the
// first record it writes follows the transition that bound it.
func TestB2a1R193W11CurrentSessionIsOperativeOnlyAfterItsDurableBind(t *testing.T) {
	e, events, task, holder := rootedResumeHarness(t, false)
	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	premature := make(chan event.Kind, 256)
	appendToStore = func(ctx context.Context, s *session.Store, lease *session.TaskLease, durable bool, ev event.Event) error {
		if ev.SessionID == e.SessionID {
			if l, err := s.TaskSessionLineage(ev.TaskID); err != nil || l.Tip() != ev.SessionID {
				premature <- ev.Kind
			}
		}
		return production(ctx, s, lease, durable, ev)
	}
	attempt := e.ResumeTask(context.Background(), task)
	b := attempt.Binding()
	if b.Refusal != nil || b.CurrentSessionID != e.SessionID {
		t.Fatalf("premise: the resume was not bound: %+v", b)
	}
	seen := settleResume(t, events)
	<-attempt.Ended()
	appendToStore = production
	if len(premature) != 0 {
		t.Fatalf("the fresh session appended %s before the durable record named it current", <-premature)
	}
	assertBoundThroughTheLineage(t, e, task, holder, seen)
	recorded, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	bound, first := -1, -1
	for i, ev := range recorded {
		if ev.SessionID != e.SessionID {
			continue
		}
		if ev.Kind == session.SessionLineageBound && bound < 0 {
			bound = i
		} else if first < 0 {
			first = i
		}
	}
	if bound < 0 || first < 0 || first < bound {
		t.Fatalf("the fresh session's first record (%d) does not follow the transition that bound it (%d)", first, bound)
	}
}

// RULING-195 (70B2a1 r6 review f2): AN INVOCATION ENDS ONLY BETWEEN ITS
// EMISSIONS. An emission validated as A's appends and publishes before A can
// end, because ending takes the task's record gate; so A's in-flight work can
// never be published after B is admitted. The append below is held inside
// the gate while A is asked to end: the end waits for it.
func TestB2a1R195AnInvocationEndsOnlyBetweenItsEmissions(t *testing.T) {
	e, _, seen := rootedTaskEngine(t, "task-1")
	a, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	inside, release := make(chan struct{}), make(chan struct{})
	appendToStore = func(ctx context.Context, s *session.Store, lease *session.TaskLease, durable bool, ev event.Event) error {
		close(inside)
		<-release
		return production(ctx, s, lease, durable, ev)
	}
	emitted := make(chan struct{})
	go func() {
		defer close(emitted)
		e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.Status, "A's in-flight work", nil))
	}()
	<-inside
	ended := make(chan struct{})
	go func() {
		defer close(ended)
		e.endInvocation(a)
	}()
	select {
	case <-ended:
		t.Fatal("A ended while one of its emissions was inside the record gate")
	case <-time.After(100 * time.Millisecond):
	}
	if _, err := e.admitInvocation(t.Context(), "task-1"); !wraps(err, ErrTaskInvocationLive) {
		t.Fatalf("B was admitted while A's emission was in flight: %v", err)
	}
	close(release)
	<-emitted
	<-ended
	appendToStore = production
	if got := drainFor(seen); len(got) != 1 || got[0].Summary != "A's in-flight work" {
		t.Fatalf("A's in-flight emission was not completed before A ended: %+v", got)
	}
	b, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	e.endInvocation(b)
}

// RULING-195 (70B2a1 r6 review f2): AN EMISSION IS REVALIDATED UNDER THE
// GATE. An emission validated as A's before it waits for the task's record
// gate is validated again once it holds the gate: when A ended while it
// waited -- its ending took the gate first -- it is refused as A's, and
// nothing is appended or published. The test holds the gate and ends A
// exactly as endInvocation does while holding it.
func TestB2a1R195AnEmissionThatWaitedPastItsInvocationsEndIsRefused(t *testing.T) {
	e, _, seen := rootedTaskEngine(t, "task-1")
	a, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	release, err := e.enterRecordGate(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		result <- e.record(a, e.Store, event.New("sess-A", "task-1", event.SourceSystem, event.Status, "waited past A's end", nil), false)
	}()
	time.Sleep(50 * time.Millisecond)
	e.mu.Lock()
	a.ended = true
	close(a.over)
	e.mu.Unlock()
	release()
	if err := <-result; !wraps(err, errInvocationEnded) {
		t.Fatalf("an emission that waited past its invocation's end was not refused as ended: %v", err)
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("an emission that waited past its invocation's end published %+v", got)
	}
	a.cancel()
}

// ONE OPERATIVE INVOCATION PER TASK, ACROSS ENGINES (70B2a1 r7 review f1,
// RULING-193 B): invocation B of a task is live in one Engine -- its holder
// Store's invocation lease taken and its fresh session bound A -> B, exactly
// as ResumeTask does. A second Engine C over another Store of the same
// physical record, as another process opens it, sees tip B; its Resume is
// refused with its own typed pre-binding refusal (session.ErrTaskInvocationLeased),
// settled and ended without hanging, and it binds no B -> C transition,
// records nothing, publishes nothing, and leaves B the tip and B untouched.
// Once B ends, the lease is free and a later Resume binds lawfully.
func TestB2a1R7AResumeInAnotherEngineIsRefusedWhileTheTaskIsLive(t *testing.T) {
	e, events, task, holder := rootedResumeHarness(t, false)
	inv, err := e.admitInvocation(context.Background(), task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	if f := e.leaseInvocation(inv.ctx, inv, e.Store, session.SessionLineageBound); f != nil {
		t.Fatalf("premise: B could not take the task's lease: %v", f)
	}
	if _, f := e.bindLineage(inv, task.TaskID); f != nil {
		t.Fatalf("premise: B's session was not bound: %v", f)
	}
	before, err := os.ReadFile(filepath.Join(e.Repo.Root, ".sensei-code", "sessions", holder, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	other := func(t *testing.T) (*Engine, <-chan event.Event) {
		t.Helper()
		store, err := session.New(e.Repo.Root, holder)
		if err != nil {
			t.Fatal(err)
		}
		fresh, err := session.FreshID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		bus := event.NewBus()
		seen, cancel := bus.Subscribe(128)
		t.Cleanup(cancel)
		c := New(e.Repo, config.Default(), bus, store, fresh)
		c.Config.Sensei.Command = "/nonexistent/awareness-mcp"
		return c, seen
	}
	c, seenC := other(t)
	attempt := c.ResumeTask(context.Background(), task)
	select {
	case <-attempt.Ended():
	case <-time.After(15 * time.Second):
		t.Fatal("the competing resume left its caller waiting")
	}
	refusal := attempt.Binding().Refusal
	if refusal == nil || !wraps(refusal, session.ErrTaskInvocationLeased) || attempt.Binding().CurrentSessionID != "" ||
		attempt.Failure() != refusal || refusal.SessionID != c.SessionID {
		t.Fatalf("the competing resume's outcome is %+v, not its own typed lease refusal", attempt.Binding())
	}
	after, err := os.ReadFile(filepath.Join(e.Repo.Root, ".sensei-code", "sessions", holder, "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("the refused competing resume changed the task's record")
	}
	if lineage, err := c.Store.TaskSessionLineage(task.TaskID); err != nil || lineage.Tip() != e.SessionID {
		t.Fatalf("the refused competing resume moved the tip: %+v %v", lineage, err)
	}
	if got := quiet(seenC); len(got) != 0 {
		t.Fatalf("the refused competing resume published %v", kinds(got))
	}
	if inv.ctx.Err() != nil || taskFailure(e, task.TaskID) != nil {
		t.Fatal("refusing the competing resume touched the live invocation")
	}
	if got := quiet(events); len(got) != 0 {
		t.Fatalf("the live invocation published %v", kinds(got))
	}

	// B ends: its lease is released, and a later lawful Resume binds B -> D.
	e.endInvocation(inv)
	d, seenD := other(t)
	if got := d.Resume(context.Background(), task); got != task.TaskID {
		t.Fatal("a resume after the live invocation ended was not bound")
	}
	settleResume(t, seenD)
	if lineage, err := d.Store.TaskSessionLineage(task.TaskID); err != nil || lineage.Tip() != d.SessionID || lineage.HolderSessionID != holder {
		t.Fatalf("the later resume's lineage is %+v %v", lineage, err)
	}
}

// THE ROOT IS A PREREQUISITE OF EVERY TASK EFFECT (70B2a1 r7 review f4,
// RULING-191 A): a run whose TaskCreated the session record does not take
// ends at once. No other record of the task is attempted, no objective is
// recorded, no mode is announced, no receipt begins, nothing is published,
// and its RunAttempt is halted and ended with the ORIGINAL typed failure of
// the root.
func TestB2a1R7ARunWhoseRootIsNotRecordedHasNoTaskEffect(t *testing.T) {
	bus := event.NewBus()
	seen, done := bus.Subscribe(64)
	defer done()
	e := withFixtureStore(t, &Engine{Bus: bus, SessionID: "session-run"})
	e.Config.Permissions.ReadRepository = true

	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	var attempted []event.Kind
	appendToStore = func(ctx context.Context, s *session.Store, lease *session.TaskLease, durable bool, ev event.Event) error {
		attempted = append(attempted, ev.Kind)
		if ev.Kind == event.TaskCreated {
			return os.ErrPermission
		}
		return production(ctx, s, lease, durable, ev)
	}
	const taskID = "task-rootless-run"
	e.run(context.Background(), taskID, "the objective", RequestedByHuman)

	if len(attempted) != 1 || attempted[0] != event.TaskCreated {
		t.Fatalf("a run whose root failed attempted to record %v", attempted)
	}
	if o := e.objective(taskID); o.Text != "" {
		t.Fatalf("a run whose root failed recorded its objective %q", o.Text)
	}
	e.mu.Lock()
	receipt := e.receipts[taskID]
	e.mu.Unlock()
	if receipt != nil {
		t.Fatal("a run whose root failed began a receipt")
	}
	if got := quiet(seen); len(got) != 0 {
		t.Fatalf("a run whose root failed published %v", kinds(got))
	}
	attempt, err := e.RunAttempt(taskID)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-attempt.Ended():
	default:
		t.Fatal("the run whose root failed has not ended")
	}
	if f := attempt.Failure(); f == nil || f.Kind != event.TaskCreated || !wraps(f, os.ErrPermission) {
		t.Fatalf("the run's outcome is not the original failure of its root: %v", f)
	}
	if record, err := e.Store.ReadRecord(); err != nil || len(record) != 0 {
		t.Fatalf("a run whose root failed left %d records (%v)", len(record), err)
	}
}

// f5 (70B2a1 r7 review): A FAILED APPEND THE STORE COULD NOT UNDO IS NOT
// REPORTED AS AN EVENT THAT WAS NOT RECORDED. When the Store returns
// session.ErrAppendIndeterminate, the typed failure is Indeterminate, says
// whether the record holds the event is unknown, and still publishes
// nothing; an undone failure is the control, reported as not recorded.
func TestB2a1F5IndeterminateAppendIsNotReportedAsAbsent(t *testing.T) {
	production := appendToStore
	t.Cleanup(func() { appendToStore = production })
	for _, c := range []struct {
		name          string
		cause         error
		indeterminate bool
	}{
		{"undone", session.ErrRecordLockTimeout, false},
		{"indeterminate", session.ErrAppendIndeterminate, true},
	} {
		e, _, seen := rootedTaskEngine(t, "task-1")
		inv, err := e.admitInvocation(t.Context(), "task-1")
		if err != nil {
			t.Fatal(err)
		}
		appendToStore = func(context.Context, *session.Store, *session.TaskLease, bool, event.Event) error { return c.cause }
		err = e.emitDurableIn(inv.ctx, event.New("sess-A", "task-1", event.SourceSystem, event.Status, "x", nil))
		appendToStore = production
		var f *RecordAppendFailure
		for cur := err; cur != nil && f == nil; {
			f, _ = cur.(*RecordAppendFailure)
			u, ok := cur.(interface{ Unwrap() error })
			if !ok {
				break
			}
			cur = u.Unwrap()
		}
		if f == nil || f.Indeterminate != c.indeterminate {
			t.Fatalf("%s: the failure is %+v (%v)", c.name, f, err)
		}
		if absent := strings.Contains(f.Error(), "neither recorded nor published"); absent == c.indeterminate {
			t.Fatalf("%s: the failure says %q", c.name, f.Error())
		}
		if got := drainFor(seen); len(got) != 0 {
			t.Fatalf("%s: a failed append was published: %+v", c.name, got)
		}
		e.endInvocation(inv)
	}
}

// durablePayload is the canonical payload of each durable kind
// TestB2a1W10EmitDurableAppendsDurably records, as the session record's kind
// registry requires of it.
func durablePayload(kind event.Kind) any {
	switch kind {
	case event.PlanAttemptStarted:
		return planAttempt{ID: "attempt-1", TaskID: "task-1", PlanSource: PlanSupplied}
	case event.ProspectiveGranted:
		return prospectiveRecord{PlanAttemptID: "attempt-1", World: "w"}
	case event.TestEditGranted:
		return testEditRecord{PlanAttemptID: "attempt-1", World: "w"}
	case event.PlanAttemptRefused:
		return planAttemptRefusal{PlanAttemptID: "attempt-1", TaskID: "task-1", Reason: "refused"}
	case event.PlanProposed:
		return proposedPlan{architectureDecision: architectureDecision{Decision: "proceed"}, PlanSource: PlanSupplied, PlanAttemptID: "attempt-1"}
	}
	return nil
}

// 70B2a1 cycle-2 review f7: A RECEIPT NEVER OUTLIVES ITS TERMINAL. The run
// receipt and the terminal it accounts for are one unit: when the record does
// not take the terminal -- refused by the Store, or a failed write -- neither
// is recorded and neither is published, and the invocation halts once with
// the terminal's typed failure. CONTROL: a lawful ending records and publishes
// both, receipt first.
func TestB2a1RF7ReceiptAndTerminalAreOneUnit(t *testing.T) {
	t.Run("the Store refuses the terminal", func(t *testing.T) {
		e, store, seen := rootedTaskEngine(t, "task-1")
		b := liveInvocation(t, e, "task-1")
		length := recordLength(t, store)
		// A terminal whose payload is not one object is malformed.
		e.emitRunTerminal(b.ctx, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED", "NONE", "a bad ending", "not an object")
		if got := drainFor(seen); len(got) != 0 {
			t.Fatalf("a receipt or terminal was published although the terminal was refused: %+v", got)
		}
		if got := recordLength(t, store); got != length {
			t.Fatalf("part of the ending unit was recorded: %d records became %d", length, got)
		}
		f := e.invocationFailure(b)
		if f == nil || f.Kind != event.WorkflowFailed || !wraps(f, session.ErrSessionAppendRefused) || !isHalted(b) {
			t.Fatalf("the invocation was not halted with the terminal's failure: %v", f)
		}
	})
	t.Run("the unit's write fails", func(t *testing.T) {
		e, store, seen := rootedTaskEngine(t, "task-1")
		b := liveInvocation(t, e, "task-1")
		length := recordLength(t, store)
		production := appendUnitToStore
		t.Cleanup(func() { appendUnitToStore = production })
		calls := 0
		appendUnitToStore = func(context.Context, *session.Store, *session.TaskLease, []event.Event) error {
			calls++
			return session.ErrRecordLockTimeout
		}
		e.emitRunTerminal(b.ctx, "task-1", event.WorkflowCompleted, event.SourceSystem, "ACCEPTED", "NONE", "done", nil)
		if calls != 1 {
			t.Fatalf("the ending was not appended as one unit: %d unit appends", calls)
		}
		if got := drainFor(seen); len(got) != 0 {
			t.Fatalf("a failed ending published %+v", got)
		}
		if got := recordLength(t, store); got != length {
			t.Fatalf("a failed ending was recorded: %d records became %d", length, got)
		}
		if f := e.invocationFailure(b); f == nil || f.Kind != event.WorkflowCompleted || !wraps(f, session.ErrRecordLockTimeout) {
			t.Fatalf("the invocation's failure is not the terminal's: %v", f)
		}
	})
	t.Run("control: a lawful ending", func(t *testing.T) {
		e, store, seen := rootedTaskEngine(t, "task-1")
		b := liveInvocation(t, e, "task-1")
		length := recordLength(t, store)
		e.emitRunTerminal(b.ctx, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED", "NONE", "an ending", nil)
		got := drainFor(seen)
		if len(got) != 2 || got[0].Kind != event.RunReceipt || got[1].Kind != event.WorkflowFailed {
			t.Fatalf("a lawful ending was not published as receipt then terminal: %+v", got)
		}
		if n := recordLength(t, store); n != length+2 {
			t.Fatalf("a lawful ending was not recorded: %d records became %d", length, n)
		}
	})
}

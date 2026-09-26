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
			e, events, task := resumeHarness(t, tc.work)
			e.Resume(context.Background(), task)
			rec := receiptFrom(t, settleResume(t, events))
			if rec.CandidateState != tc.want {
				t.Fatalf("the resumed receipt says candidate_state %q; the inherited candidate is %q", rec.CandidateState, tc.want)
			}
		})
	}
}

// Finding 2. A resumed invocation ends through the same classifier as execute:
// a caller stop is STOPPED, not a final failure that makes the task vanish.
func TestAResumeStoppedByItsCallerIsNotAFailure(t *testing.T) {
	e, events, task := resumeHarness(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e.Resume(ctx, task)
	seen := settleResume(t, events)
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
	for _, fn := range []string{"execute", "Resume"} {
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

	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emit(event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", nil))
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted,
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
	e.terminateRun(context.Background(), task, "the objective", err)
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
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emit(event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", nil))
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded", record))
	fresh := rrDerivedRecomputation(t, planned, nil)

	attempt := func(engine *Engine) *RestorationRefusal {
		found := reopen(t, root, "session-w3")
		if len(found) != 1 {
			t.Fatalf("the task is not resumable: %+v", found)
		}
		engine.beginReceipt(task)
		err := engine.restoreTestEditGrants(found[0], fresh, planned, teWorld)
		r := refusalOf(t, err)
		engine.terminateRun(context.Background(), task, "the objective", err)
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
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emit(event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", nil))
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded", rrPayload(t, forged)))

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

	e.terminateRun(context.Background(), task, "the objective", err)
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
		e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
		e.emit(event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", nil))
		e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded", rrPayload(t, c.grants...)))

		before := authorityEvidence(t, store)
		found := reopen(t, root, "session-w5")
		e.beginReceipt(task)
		err := e.restoreTestEditGrants(found[0], rrDerivedRecomputation(t, c.planned, c.covered), c.planned, teWorld)
		if c.refuses {
			refusalOf(t, err)
			e.terminateRun(context.Background(), task, "the objective", err)
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
	resume := funcBody(t, "internal/workflow/engine.go", "Resume")
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
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.emit(event.New(e.SessionID, task, event.SourceArchitect, event.PlanProposed, "the plan", nil))
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TestEditGranted, "recorded",
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

	e.terminateRun(context.Background(), task, "the objective", err)
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

// OBJECTIVE IDENTITY ON RESUME -- the composition witnesses. The measured case
// (W1), the absent-objective control (W3) and the runner-edge guard (W4) are in
// nonconvergence_test.go beside restartedAtOwedReplan.

// W2. The fresh-run path and the resume path of ONE task record mint the same
// architect binding, referent by referent. Fails if any referent survives the
// process boundary differently -- the objective digest was the one that did not.
func TestObjectiveW2AFreshRunAndItsResumeMintTheSameArchitectBinding(t *testing.T) {
	submitted, restarted, task, events := restartedAtOwedReplan(t, "the objective")
	fresh, asked, err := architectTurnBinding(submitted, task.TaskID)
	if !asked || !fresh.Valid() {
		t.Fatalf("premise: the fresh run binds a valid architect turn: asked=%v %+v %v", asked, fresh, err)
	}
	restarted.Resume(context.Background(), task)
	settleResume(t, events)
	resumed, asked, err := architectTurnBinding(restarted, task.TaskID)
	if !asked {
		t.Fatalf("the resumed architect turn never reached resolver selection: %v", err)
	}
	for _, f := range []struct{ name, fresh, resumed string }{
		{"TaskID", fresh.TaskID, resumed.TaskID},
		{"ObjectiveDigest", fresh.ObjectiveDigest, resumed.ObjectiveDigest},
		{"BaseSHA", fresh.BaseSHA, resumed.BaseSHA},
		{"GraphRepository", fresh.GraphRepository, resumed.GraphRepository},
		{"GraphBuildCommit", fresh.GraphBuildCommit, resumed.GraphBuildCommit},
	} {
		if f.fresh != f.resumed {
			t.Errorf("%s: the fresh run binds %q and its resume binds %q", f.name, f.fresh, f.resumed)
		}
	}
}

// W5, CONTROL. A process that already holds the objective keeps it, with its
// original provenance, across a resume; one that holds a DIFFERENT objective is
// refused, and neither the held text nor its provenance is replaced. Fails if a
// resume overwrites a held objective, demotes its provenance, or proceeds past
// a disagreement to anything the resume would do next.
func TestObjectiveW5AResumeKeepsAHeldObjectiveAndRefusesADisagreeingOne(t *testing.T) {
	held := Objective{Text: "the objective", Provenance: RequestedByHuman}
	e, events, task := resumeHarness(t, true)
	e.recordObjective(task.TaskID, held)
	e.Resume(context.Background(), task)
	settleResume(t, events)
	if got := e.objective(task.TaskID); got.Text != held.Text || got.Provenance != held.Provenance {
		t.Fatalf("a resume replaced the objective this process held: %+v, want %+v", got, held)
	}

	other := Objective{Text: "a different objective", Provenance: RequestedByHuman}
	e, events, task = resumeHarness(t, true)
	e.recordObjective(task.TaskID, other)
	e.Resume(context.Background(), task)
	seen := settleResume(t, events)
	refused := false
	for _, ev := range seen {
		if strings.Contains(ev.Summary, "start Sensei") {
			t.Fatalf("a resume with a disagreeing objective continued past the reconciliation: %s", ev.Summary)
		}
		if ev.Kind == event.WorkflowFailed && strings.Contains(ev.Summary, "does not match the one its record holds") {
			refused = true
		}
	}
	if !refused {
		t.Fatalf("a disagreeing objective was not refused by name: %v", kinds(seen))
	}
	if got := e.objective(task.TaskID); got.Text != other.Text || got.Provenance != other.Provenance {
		t.Fatalf("a refused resume replaced the held objective: %+v, want %+v", got, other)
	}
}

// W6, CONTROL. The resume of an UNPLANNED task binds its architect turn as it
// did before: the recorded objective under the resumption's provenance, and a
// valid binding naming it. Fails if moving the reconciliation cost the path
// that already worked.
func TestObjectiveW6AnUnplannedResumeStillBindsItsArchitectTurn(t *testing.T) {
	e, events, task := resumeHarness(t, false)
	task.Planned = false
	var start certifiedStart
	start.preflight.Authority.GraphBuildCommit = objectiveGraphCommit
	e.Config.Sensei.Repository = "globulario/sensei"
	e.bindGraph(task.TaskID, start)
	e.Resume(context.Background(), task)
	settleResume(t, events)

	if got := e.objective(task.TaskID); got.Text != task.Task || got.Provenance != ResumedGoverned {
		t.Fatalf("the unplanned resume did not carry the recorded objective: %+v", got)
	}
	b, asked, err := architectTurnBinding(e, task.TaskID)
	if !asked || !b.Valid() || b.ObjectiveDigest != objectiveDigestOf(task.Task) {
		t.Fatalf("the unplanned resume no longer binds its architect turn: asked=%v %+v %v", asked, b, err)
	}
}

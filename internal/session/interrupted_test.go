package session

import (
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

// ev is a well-framed record of taskID, as the engine writes one.
func ev(taskID string, source event.Source, kind event.Kind, summary string) event.Event {
	return event.New("s1", taskID, source, kind, summary, nil)
}

// answered is the user's answer to taskID's standing question, choosing
// option 1, in the form the engine records it.
func answered(taskID, summary string) event.Event {
	return event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, summary, map[string]string{"option": "1"})
}

func TestPlannedButUnfinishedTaskIsResumable(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "add a report command"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
		ev("t1", event.SourceReviewer, event.Status, "REVISE: counts are presented as exact"),
	})
	if len(got) != 1 {
		t.Fatalf("found %d resumable tasks, want 1", len(got))
	}
	if got[0].Task != "add a report command" || got[0].Plan != "the plan" {
		t.Fatalf("recovered task is incomplete: %+v", got[0])
	}
	if got[0].Review != "REVISE: counts are presented as exact" {
		t.Fatalf("the reviewer's last finding was lost: %+v", got[0])
	}
}

// The task-terminal set, named positively. A change was admitted, the work
// failed, or a read-only run reported what it found; nothing else ends a task.
//
// WorkflowObserved is here because an observation IS an ending. Left out, every
// finished audit would reappear as active work for ever -- the mirror image of
// the disappearance this reconstruction repairs.
func TestFinishedTasksAreNotResumable(t *testing.T) {
	for name, terminal := range map[string]event.Kind{
		"completed": event.WorkflowCompleted,
		"failed":    event.WorkflowFailed,
		"observed":  event.WorkflowObserved,
	} {
		got := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
			ev("t1", event.SourceSystem, terminal, "done"),
		})
		if len(got) != 0 {
			t.Fatalf("%s task offered for resume: %+v", name, got)
		}
	}
}

// DISCOVERABILITY AND IMPLEMENTATION ELIGIBILITY ARE TWO STATEMENTS, and this
// test proves the second one while TestATaskIsDiscoverableFromItsCreationUntilItEnds
// proves the first.
//
// It was named TestTaskWithoutAPlanIsNotResumable and asserted that an unplanned
// task is not offered AT ALL, on the reasoning that there is no candidate to
// continue so offering it would restart the work. Continuing it under its own
// task id, objective, recorded answers and candidate base is not a restart, and
// refusing to offer it is what made task-1789848074761930104 unreachable on
// 2026-09-19: its owner had answered its standing question and the process died
// before a plan existed, so the task owed its architect turn and nothing could
// reach it.
//
// The name went with the claim rather than outliving it. A test called
// "...IsNotResumable" that asserts the task IS found leaves the suite and the
// awareness graph stating opposite lifecycle rules, and the next reader has no
// way to tell which one the code obeys.
//
// What holds: an unplanned task is never offered as a PLANNED one. Planned stays
// false, which is what sends it to the architect turn it never took rather than
// to an implementer with no plan to implement.
func TestAnUnplannedTaskIsDiscoverableButNotOfferedAsPlanned(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		ev("t1", event.SourceArchitect, event.ArchitectSpoke, "answered the question instead"),
	})
	if len(got) != 1 {
		t.Fatalf("the task is not discoverable at all: %+v", got)
	}
	if got[0].Planned {
		t.Fatal("a task that never produced a plan is offered as a planned continuation, " +
			"so a resume would hand an implementer a plan that does not exist")
	}
	if got[0].Plan != "" {
		t.Fatalf("a plan was reconstructed for a task that never had one: %q", got[0].Plan)
	}
	// And a plan is what makes the difference, rather than anything else in the
	// record: the same task with one is offered as planned.
	planned := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the bounded plan"),
	})
	if len(planned) != 1 || !planned[0].Planned {
		t.Fatalf("a planned task is not reported as planned: %+v", planned)
	}
}

// THE CRASH WINDOW, witnessed. task-1789848074761930104 on 2026-09-19: the owner
// answered the deferred Level-3 question, the answer was recorded, the run
// re-entered governed execution and the architect began re-planning, and the
// process died before any plan was proposed.
//
// The record then said: not awaiting authority (it was answered), not planned
// (no plan event), not blocked. Under the old reconstruction, which derived
// existence from those three states, the task existed in none of them and
// vanished -- `resume --list` omitted it, `resume --task` answered "no
// interrupted task with that id", and its candidate identity was still on disk.
func TestATaskIsDiscoverableFromItsCreationUntilItEnds(t *testing.T) {
	crashed := []event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "widen the boundary"),
		{TaskID: "t1", Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority,
			Summary: "deferred", Payload: []byte(`{"condition":"c","decision":{"options":[{"id":"1"}]}}`)},
		answered("t1", "Authorize the architectural change"),
		ev("t1", event.SourceArchitect, event.Status, "re-planning"),
	}
	got := FindInterrupted(crashed)
	if len(got) != 1 || got[0].TaskID != "t1" {
		t.Fatalf("a task that died between its answer and its plan is unreachable: %+v", got)
	}
	if len(got[0].AwaitingAuthority) != 0 {
		t.Error("the answered question is standing again, so resuming would ask it a second time")
	}
	if got[0].Planned {
		t.Error("a task that never reached a plan claims one")
	}
	if got[0].Task != "widen the boundary" {
		t.Errorf("the objective was lost: %q", got[0].Task)
	}
	// Creation alone is enough. A task interrupted before ANYTHING else happened
	// is still a task somebody asked for.
	if bare := FindInterrupted(crashed[:1]); len(bare) != 1 {
		t.Fatalf("a task interrupted immediately after creation is unreachable: %+v", bare)
	}
	// And it stops being discoverable exactly when it ends.
	if done := FindInterrupted(append(crashed, ev("t1", event.SourceSystem, event.WorkflowCompleted, "done"))); len(done) != 0 {
		t.Fatalf("a completed task is still offered: %+v", done)
	}
}

// An INVOCATION terminal ends one process's attempt and leaves the task owing
// something. Each of these was, at some point in this repository's history,
// emitted as WorkflowFailed and read here as the end of the task; each time the
// work became unreachable except as a new task with a new identity.
func TestAnInvocationTerminalDoesNotEndTheTask(t *testing.T) {
	for name, terminal := range map[string]event.Kind{
		"stopped by the human": event.WorkflowStopped,
		"timed out":            event.WorkflowTimedOut,
		"awaiting review":      event.WorkflowAwaitingReview,
		"awaiting authority":   event.WorkflowAwaitingAuthority,
		"blocked external":     event.WorkflowBlockedExternal,
		"not converged":        event.WorkflowNotConverged,
		"plan refused again":   event.WorkflowPlanAdmissionRefused,
	} {
		got := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
			{TaskID: "t1", Source: event.SourceSystem, Kind: terminal, Summary: name},
		})
		if len(got) != 1 {
			t.Fatalf("%s ended the task, not just the invocation: %+v", name, got)
		}
	}
}

// A stop must leave work recoverable, or it is a destructive act wearing the
// name of a pause. This is the session-level half of that guarantee: the
// candidate stays on disk, and the record still offers the task.
func TestStoppedTaskRemainsResumable(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
		ev("t1", event.SourceSystem, event.WorkflowStopped, "stopped by the human; the candidate is left as it stands"),
	})
	if len(got) != 1 || got[0].TaskID != "t1" {
		t.Fatalf("a stopped task was treated as finished: %+v", got)
	}
}

func TestEachTaskIsJudgedSeparately(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "finished one"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "a plan"),
		ev("t1", event.SourceSystem, event.WorkflowCompleted, "done"),
		ev("t2", event.SourceSystem, event.TaskCreated, "interrupted one"),
		ev("t2", event.SourceArchitect, event.PlanProposed, "a plan"),
	})
	if len(got) != 1 || got[0].TaskID != "t2" {
		t.Fatalf("got %+v, want only the interrupted task", got)
	}
}

// A deferred Level-3 question is resumable even though no plan exists: the
// router reaches it during architecture, before any plan is proposed. What
// resume restores is the question, carried verbatim.
func TestDeferredAuthorityIsResumableAndCarriesItsQuestion(t *testing.T) {
	payload := `{"condition":"graph coverage is absent for the planned files","decision":{"subject":"Authorize?"}}`
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "widen the boundary"),
		{TaskID: "t1", Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority,
			Summary: "authority decision deferred", Payload: []byte(payload)},
	})
	if len(got) != 1 {
		t.Fatalf("a deferred authority decision was not offered for resume: %+v", got)
	}
	if string(got[0].AwaitingAuthority) != payload {
		t.Fatalf("the question was not carried verbatim:\n got %s\nwant %s", got[0].AwaitingAuthority, payload)
	}
}

// Once answered, the task resumes as work rather than as the question it has
// already settled.
func TestAnsweredAuthorityIsNoLongerPending(t *testing.T) {
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "widen the boundary"),
		{TaskID: "t1", Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority,
			Summary: "deferred", Payload: []byte(`{"condition":"c"}`)},
		answered("t1", "Authorize the architectural change"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
	})
	if len(got) != 1 {
		t.Fatalf("the answered task is no longer resumable at all: %+v", got)
	}
	if len(got[0].AwaitingAuthority) != 0 {
		t.Errorf("an answered question is still pending, so resume would ask it again: %s", got[0].AwaitingAuthority)
	}
}

// A CREATED TASK WITH NO OBJECTIVE IS FOUND, AND SAYS WHY IT CANNOT GO ON.
//
// task.created is written BEFORE execute validates the objective, so a process
// that dies in that interval leaves exactly this record: created, nonterminal,
// and unable to say what the work is. The reconstruction used to require a
// nonblank objective and dropped it -- which is the same absence-for-unknown
// failure this whole repair removes, reached by a different route. A task nobody
// can find is worse than a task that is found and refused.
func TestACreatedTaskWithNoObjectiveIsFoundAndClassifiedUnusable(t *testing.T) {
	for name, objective := range map[string]string{
		"empty":      "",
		"whitespace": "   \t\n ",
	} {
		got := FindInterrupted([]event.Event{ev("t1", event.SourceSystem, event.TaskCreated, objective)})
		if len(got) != 1 {
			t.Fatalf("%s objective: the task vanished from the reconstruction: %+v", name, got)
		}
		if got[0].TaskID != "t1" {
			t.Fatalf("%s objective: the task identity was lost: %+v", name, got[0])
		}
		if got[0].ObjectiveUsable() {
			t.Fatalf("%s objective: the task claims it can say what it is for, so a continuation would be "+
				"advertised that the governed path is certain to refuse", name)
		}
	}
	// And the classification is about the objective ALONE: an ordinary task is
	// discoverable and usable, so the refusal cannot be reached by accident.
	usable := FindInterrupted([]event.Event{ev("t1", event.SourceSystem, event.TaskCreated, "a real objective")})
	if len(usable) != 1 || !usable[0].ObjectiveUsable() {
		t.Fatalf("a task with an objective was classified unusable: %+v", usable)
	}
	// A task with no objective still ENDS when it ends. The relaxed filter must
	// not have made it immortal.
	ended := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, ""),
		ev("t1", event.SourceSystem, event.WorkflowCompleted, "done"),
	})
	if len(ended) != 0 {
		t.Fatalf("a completed task with no objective is still active: %+v", ended)
	}
}

// W4 -- R2 x PR. A candidate-precondition refusal that executed nothing is
// recorded, and a FRESH reconstruction of the stored events -- the restart path
// -- still finds the task, reads its ending as invocation-terminal through the
// canonical classifier, keeps the refusal's reason, and leaves the standing
// question byte for byte as it was asked.
//
// W2 rides beside it as the control: the same history ending in a genuine
// WorkflowFailed still closes the task.
func TestAPreconditionRefusalSurvivesReconstructionWithItsQuestionStanding(t *testing.T) {
	question := `{"task_id":"t1","condition":"a Level-3 condition","scope_recorded":true,"scope":["internal/a.go"],` +
		`"decision":{"level":3,"subject":"Architectural authority reached a human-owned boundary.",` +
		`"options":[{"id":"1","label":"Authorize","outcome":"authorize"},{"id":"3","label":"Stop this task","outcome":"stop"}]}}`
	history := func(ending event.Event) []event.Event {
		return []event.Event{
			ev("t1", event.SourceUser, event.TaskCreated, "the objective"),
			{TaskID: "t1", Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority,
				Summary: "a Level-3 condition", Payload: []byte(question)},
			ending,
		}
	}
	for _, tc := range []struct {
		kind   event.Kind
		reason string
	}{
		{event.WorkflowBaseMovedRefused, "candidate t1 was established at base 1e3f4a8 but the repository is now at 7aaeab1; " +
			"a candidate's base is immutable. Nothing was executed; the task is preserved and still resumable"},
		{event.WorkflowDirtyCanonicalRefused, "the canonical checkout /repo has uncommitted changes. " +
			"Nothing was executed; the task is preserved and still resumable"},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			got := FindInterrupted(history(ev("t1", event.SourceSystem, tc.kind, tc.reason)))
			if len(got) != 1 || got[0].TaskID != "t1" {
				t.Fatalf("the refused task is not resumable after reconstruction: %+v", got)
			}
			if terminality, ok := event.RunTerminality(tc.kind); !ok || terminality != event.InvocationTerminal {
				t.Fatalf("the canonical classifier reads %s as %q (ok=%v), want invocation-terminal", tc.kind, terminality, ok)
			}
			if got[0].PreconditionRefusal != tc.kind || got[0].PreconditionRefusalReason != tc.reason {
				t.Fatalf("the refusal did not survive reconstruction: kind=%q reason=%q",
					got[0].PreconditionRefusal, got[0].PreconditionRefusalReason)
			}
			if string(got[0].AwaitingAuthority) != question {
				t.Fatalf("the standing question changed across the refusal:\n got %s\nwant %s", got[0].AwaitingAuthority, question)
			}
		})
	}
	// W2: a genuine failure is still the end of the task.
	if got := FindInterrupted(history(ev("t1", event.SourceSystem, event.WorkflowFailed, "a real defect"))); len(got) != 0 {
		t.Fatalf("a genuine WorkflowFailed no longer ends the task: %+v", got)
	}
}

// pev is an event carrying a recorded payload, byte for byte.
func pev(taskID string, kind event.Kind, payload string) event.Event {
	return event.Event{TaskID: taskID, Source: event.SourceSystem, Kind: kind, Payload: []byte(payload)}
}

// W8 (Objective 61): MULTIPLE REFUSALS. Two distinct canonical refusals of ONE
// plan attempt are both kept, each under its own RefusalID -- the later one
// does not erase the earlier because they share a PlanAttemptID -- and the
// refusal the task is owed a turn for is the one the parked invocation named,
// not whichever record was written last.
func TestDistinctRefusalsOfOneAttemptAreKeptAndTheOwedOneIsTheParkedOne(t *testing.T) {
	const (
		first  = `{"plan_attempt_id":"att-1","task_id":"t1","reason":"no grant","refusal_id":"ref-a"}`
		second = `{"plan_attempt_id":"att-1","task_id":"t1","reason":"no grant","refusal_id":"ref-b"}`
		parked = `{"plan_attempt_id":"att-1","task_id":"t1","reason":"no grant","refusal_id":"ref-a"}`
	)
	history := []event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		pev("t1", event.PlanAttemptStarted, `{"id":"att-1","plan_attempt_id":"att-1"}`),
		pev("t1", event.PlanAttemptRefused, first),
		pev("t1", event.PlanAttemptRefused, second),
		pev("t1", event.WorkflowPlanAdmissionRefused, parked),
	}
	got := FindInterrupted(history)
	if len(got) != 1 {
		t.Fatalf("a parked plan-admission refusal ended the task: %+v", got)
	}
	task := got[0]
	if len(task.PlanAttemptRefusals) != 2 || string(task.PlanAttemptRefusals["ref-a"]) != first || string(task.PlanAttemptRefusals["ref-b"]) != second {
		t.Fatalf("distinct refusals of one attempt were not kept by RefusalID: %v", task.PlanAttemptRefusals)
	}
	if string(task.PlanAdmissionRefused) != parked || task.Planned {
		t.Fatalf("the owed refusal is not the parked one: %s (planned %v)", task.PlanAdmissionRefused, task.Planned)
	}
	// The newest park wins, and an admitted plan discharges the owed turn
	// while the refusal records stay as evidence.
	reparked := `{"plan_attempt_id":"att-1","task_id":"t1","reason":"no grant","refusal_id":"ref-b"}`
	got = FindInterrupted(append(history, pev("t1", event.WorkflowPlanAdmissionRefused, reparked)))
	if len(got) != 1 || string(got[0].PlanAdmissionRefused) != reparked {
		t.Fatalf("the newest park is not the owed refusal: %+v", got)
	}
	got = FindInterrupted(append(history, pev("t1", event.PlanProposed, `{"plan_attempt_id":"att-2"}`)))
	if len(got) != 1 || len(got[0].PlanAdmissionRefused) != 0 || len(got[0].PlanAttemptRefusals) != 2 {
		t.Fatalf("an admitted plan did not discharge the owed refusal, or dropped the records: %+v", got)
	}
	// A record written before refusals carried a RefusalID keeps its old key.
	got = FindInterrupted([]event.Event{
		ev("t2", event.SourceSystem, event.TaskCreated, "a task"),
		pev("t2", event.PlanAttemptStarted, `{"plan_attempt_id":"att-9"}`),
		pev("t2", event.PlanAttemptRefused, `{"plan_attempt_id":"att-9","task_id":"t2","reason":"x"}`),
	})
	if len(got) != 1 || len(got[0].PlanAttemptRefusals["att-9"]) == 0 {
		t.Fatalf("a refusal predating RefusalID was not kept under its attempt: %+v", got)
	}
}

// Objective 61: a FIRST canonical refusal the workflow recorded as returned to
// the architect is owed an architect turn from the moment it is recorded --
// not only once a repeat parks -- including beside an older operative plan,
// until an admitted plan discharges it. A record written before refusals
// carried a RefusalID owes nothing, and neither does a refusal recorded with
// any continuation other than the architect turn, or none: the projection
// reads the workflow's recorded decision and never infers one from the class
// or from the RefusalID.
func TestAFirstRefusalIsOwedBesideAnOperativePlanUntilAPlanIsAdmitted(t *testing.T) {
	const refused = `{"plan_attempt_id":"att-2","task_id":"t1","reason":"no grant","refusal_class":"prospective_admission","refusal_id":"ref-a","continuation":"architect_turn"}`
	history := []event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		pev("t1", event.PlanAttemptStarted, `{"plan_attempt_id":"att-1"}`),
		pev("t1", event.PlanProposed, `{"plan_attempt_id":"att-1"}`),
		pev("t1", event.PlanAttemptStarted, `{"plan_attempt_id":"att-2"}`),
		pev("t1", event.PlanAttemptRefused, refused),
	}
	got := FindInterrupted(history)
	if len(got) != 1 || !got[0].Planned || got[0].PlanAttemptID != "att-1" || string(got[0].PlanAdmissionRefused) != refused {
		t.Fatalf("the first refusal of a re-plan is not owed beside the operative plan: %+v", got)
	}
	got = FindInterrupted(append(history, pev("t1", event.PlanProposed, `{"plan_attempt_id":"att-3"}`)))
	if len(got) != 1 || len(got[0].PlanAdmissionRefused) != 0 || got[0].PlanAttemptID != "att-3" {
		t.Fatalf("an admitted replacement did not discharge the owed first refusal: %+v", got)
	}
	got = FindInterrupted([]event.Event{
		ev("t2", event.SourceSystem, event.TaskCreated, "a task"),
		pev("t2", event.PlanAttemptRefused, `{"plan_attempt_id":"att-9","task_id":"t2","reason":"x"}`),
	})
	if len(got) != 1 || len(got[0].PlanAdmissionRefused) != 0 {
		t.Fatalf("a refusal predating RefusalID was made owed: %+v", got)
	}
	for _, payload := range []string{
		`{"plan_attempt_id":"att-2","task_id":"t1","reason":"declined","refusal_class":"authority_declined","refusal_id":"ref-d"}`,
		`{"plan_attempt_id":"att-2","task_id":"t1","reason":"supplied","refusal_class":"supplied_plan_unrevisable","refusal_id":"ref-s"}`,
		`{"plan_attempt_id":"att-2","task_id":"t1","reason":"no grant","refusal_class":"prospective_admission","refusal_id":"ref-n"}`,
		`{"plan_attempt_id":"att-2","task_id":"t1","reason":"no grant","refusal_class":"prospective_admission","refusal_id":"ref-u","continuation":"Architect_Turn"}`,
	} {
		got = FindInterrupted(append(history[:4:4], pev("t1", event.PlanAttemptRefused, payload)))
		if len(got) != 1 || len(got[0].PlanAdmissionRefused) != 0 || len(got[0].PlanAttemptRefusals) != 1 {
			t.Fatalf("a refusal with no recorded return to the architect was made owed, or dropped: %s -> %+v", payload, got)
		}
	}
	// An evidence-only refusal recorded after an owed one does not displace it.
	got = FindInterrupted(append(history, pev("t1", event.PlanAttemptRefused,
		`{"plan_attempt_id":"att-2","task_id":"t1","reason":"declined","refusal_class":"authority_declined","refusal_id":"ref-d"}`)))
	if len(got) != 1 || string(got[0].PlanAdmissionRefused) != refused {
		t.Fatalf("an evidence-only refusal displaced the owed one: %+v", got)
	}
}

// Objective 61: a refusal is bound to its attempt only when that attempt was
// durably started EARLIER in the record. A refusal recorded before its start
// never enters the refusals restoration reads as repetition state -- not even
// when the same PlanAttemptID starts later, which must not validate it
// retroactively -- while an owed one stays owed, so restoration can refuse
// its binding by name rather than lose the obligation.
func TestARefusalBeforeItsAttemptStartedSeedsNoRepetitionState(t *testing.T) {
	const refused = `{"plan_attempt_id":"att-1","task_id":"t1","reason":"no grant","refusal_class":"prospective_admission","refusal_id":"ref-a","continuation":"architect_turn"}`
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		pev("t1", event.PlanAttemptRefused, refused),
		pev("t1", event.PlanAttemptStarted, `{"plan_attempt_id":"att-1"}`),
	})
	if len(got) != 1 {
		t.Fatalf("a refusal before its start ended the task: %+v", got)
	}
	task := got[0]
	if len(task.PlanAttemptRefusals) != 0 {
		t.Fatalf("a refusal recorded before its attempt started was admitted into repetition state: %v", task.PlanAttemptRefusals)
	}
	if !task.StartedPlanAttempts["att-1"] {
		t.Fatalf("the later start itself was lost: %+v", task)
	}
	if string(task.PlanAdmissionRefused) != refused {
		t.Fatalf("the owed refusal was dropped instead of preserved for restoration to refuse: %s", task.PlanAdmissionRefused)
	}
	// Control: the same record after its start is admitted.
	got = FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		pev("t1", event.PlanAttemptStarted, `{"plan_attempt_id":"att-1"}`),
		pev("t1", event.PlanAttemptRefused, refused),
	})
	if len(got) != 1 || string(got[0].PlanAttemptRefusals["ref-a"]) != refused {
		t.Fatalf("a refusal after its start was not admitted: %+v", got)
	}
}

// 70B1 (RULING-153): THE CHECKPOINT'S FRAMING AND TRANSACTION RECORDS.

const (
	ckptA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	ckptB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func liveCheckpoint() Checkpoint {
	return Checkpoint{Version: CheckpointVersion, CheckpointID: ckptA, TaskID: "t1", PlanAttemptID: "pa-1",
		Status: CheckpointLive, Inputs: []byte(`{"review_cycle":2}`)}
}

// W5 UNKNOWN STATUS. The durable status set is exactly live, blocked,
// exhausted and retired; serving, closed and anything else are refused before
// they are encoded and when they are read. A retirement is a closed reason and
// only a retired checkpoint states one.
func TestB1W5AFifthCheckpointStatusIsRefused(t *testing.T) {
	for _, s := range []CheckpointStatus{CheckpointLive, CheckpointBlocked, CheckpointExhausted} {
		c := liveCheckpoint()
		c.Status = s
		if _, err := EncodeCheckpoint(c); err != nil {
			t.Fatalf("the durable status %s was refused: %v", s, err)
		}
	}
	for _, s := range []CheckpointStatus{"serving", "closed", "LIVE", ""} {
		c := liveCheckpoint()
		c.Status = s
		if _, err := EncodeCheckpoint(c); err == nil {
			t.Fatalf("status %q was encoded as a durable checkpoint status", s)
		}
	}
	valid, err := EncodeCheckpoint(liveCheckpoint())
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{`"serving"`, `"closed"`} {
		raw := []byte(replaceOnce(string(valid), `"live"`, s))
		if _, err := DecodeCheckpoint(raw); err == nil {
			t.Fatalf("a checkpoint read back with status %s was accepted", s)
		}
	}
	retired := liveCheckpoint()
	retired.Status = CheckpointRetired
	for _, r := range []RetirementReason{"", "done, more or less", "closed"} {
		retired.Retirement = r
		if _, err := EncodeCheckpoint(retired); err == nil {
			t.Fatalf("a retired checkpoint with retirement reason %q was encoded", r)
		}
	}
	for _, r := range []RetirementReason{RetiredCompleted, RetiredSupersededPlanAttempt, RetiredReplacedCheckpoint} {
		retired.Retirement = r
		if _, err := EncodeCheckpoint(retired); err != nil {
			t.Fatalf("the typed retirement %s was refused: %v", r, err)
		}
	}
	live := liveCheckpoint()
	live.Retirement = RetiredCompleted
	if _, err := EncodeCheckpoint(live); err == nil {
		t.Fatal("a live checkpoint stating a retirement reason was encoded")
	}
}

func replaceOnce(s, old, new string) string {
	for i := 0; i+len(old) <= len(s); i++ {
		if s[i:i+len(old)] == old {
			return s[:i] + new + s[i+len(old):]
		}
	}
	return s
}

// W6 STRICT EOF. Exactly one complete canonical checkpoint and then the end:
// a trailing bracket, brace, second value or arbitrary bytes is malformed;
// trailing whitespace is not.
func TestB1W6TheCheckpointDecoderRequiresEOFAfterOneValue(t *testing.T) {
	valid, err := EncodeCheckpoint(liveCheckpoint())
	if err != nil {
		t.Fatal(err)
	}
	if c, err := DecodeCheckpoint(append(append([]byte(nil), valid...), " \n\t"...)); err != nil || c.CheckpointID != ckptA {
		t.Fatalf("one canonical checkpoint followed by whitespace was refused: %v", err)
	}
	for name, trailer := range map[string]string{
		"bracket": "]", "brace": "}", "object": `{"checkpoint_id":"x"}`, "value": "1", "text": "trailing prose",
		"spaced brace": "\n}", "null": "null",
	} {
		raw := append(append([]byte(nil), valid...), trailer...)
		if _, err := DecodeCheckpoint(raw); err == nil {
			t.Fatalf("a checkpoint followed by a trailing %s was accepted", name)
		}
	}
	// The same law for every strictly framed value, before any canonical
	// comparison could notice: one value, then only whitespace.
	var b CheckpointBinding
	if err := DecodeExactlyOne([]byte(`{"checkpoint_id":"x"}`+" \n"), &b); err != nil || b.CheckpointID != "x" {
		t.Fatalf("one value followed by whitespace was refused: %v", err)
	}
	for _, trailer := range []string{"]", "}", `{"checkpoint_id":"y"}`, "1", "text"} {
		if err := DecodeExactlyOne([]byte(`{"checkpoint_id":"x"}`+trailer), &b); err == nil {
			t.Fatalf("a value followed by %q was decoded as exactly one", trailer)
		}
	}
	if _, err := DecodeCheckpoint([]byte(replaceOnce(string(valid), `{`, `{"unknown":1,`))); err == nil {
		t.Fatal("a checkpoint carrying an unknown field was accepted")
	}
	if _, err := DecodeCheckpoint([]byte(replaceOnce(string(valid), `,`, ` ,`))); err == nil {
		t.Fatal("a checkpoint that is not its canonical encoding was accepted")
	}
}

func checkpointEvent(kind event.Kind, source event.Source, session, task string, b CheckpointBinding) event.Event {
	return event.New(session, task, source, kind, "checkpoint", b)
}

func boundCheckpoint(id string) CheckpointBinding {
	return CheckpointBinding{CheckpointID: id, TaskID: "t1", PlanAttemptID: "pa-1", Status: CheckpointLive,
		PayloadDigest: "payload-" + id[:4], ReplayDigest: "replay-" + id[:4]}
}

// W14 THE PROJECTION KEEPS OWNERSHIP. Source and SessionID of every prepared
// and committed record survive the projection a commit is proven from.
func TestB1W14TheCheckpointProjectionPreservesSourceAndSession(t *testing.T) {
	b := boundCheckpoint(ckptA)
	got := CheckpointRecords([]event.Event{
		checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t1", b),
		checkpointEvent(event.CheckpointCommitted, event.SourceClaude, "s2", "t1", b),
		checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t2", b),
	}, "t1")
	if len(got) != 2 {
		t.Fatalf("the projection holds %d records of t1, want 2: %+v", len(got), got)
	}
	if got[0].Kind != event.CheckpointPrepared || got[0].Source != event.SourceSystem || got[0].SessionID != "s1" || got[0].Binding != b {
		t.Fatalf("the prepared record lost its ownership: %+v", got[0])
	}
	if got[1].Kind != event.CheckpointCommitted || got[1].Source != event.SourceClaude || got[1].SessionID != "s2" {
		t.Fatalf("the committed record lost its ownership: %+v", got[1])
	}
}

// W13 PREPARED/COMMITTED OWNERSHIP. A commit is authoritative only beside an
// EARLIER engine-sourced prepare of the same session and task binding exactly
// the same checkpoint, plan attempt, payload digest and ReplayDigest. Any one
// difference -- or a commit with no prepare, or one prepared after it -- leaves
// the older proven checkpoint the committed one.
func TestB1W13APreparedCommittedPairMustProveItsOwnership(t *testing.T) {
	older, newer := boundCheckpoint(ckptA), boundCheckpoint(ckptB)
	base := []event.Event{
		checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t1", older),
		checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t1", older),
	}
	if got, ok := CommittedCheckpoint(base, "t1", "s1"); !ok || got != older {
		t.Fatalf("premise: the owned pair is not the committed checkpoint: %+v %v", got, ok)
	}
	pair := func(prepare, commit event.Event) []event.Event {
		return append(append([]event.Event(nil), base...), prepare, commit)
	}
	prepared := checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t1", newer)
	if got, ok := CommittedCheckpoint(pair(prepared, checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t1", newer)), "t1", "s1"); !ok || got != newer {
		t.Fatalf("premise: an owned newer pair did not become the committed checkpoint: %+v", got)
	}
	differ := map[string]func(b *CheckpointBinding){
		"plan attempt":   func(b *CheckpointBinding) { b.PlanAttemptID = "pa-2" },
		"payload digest": func(b *CheckpointBinding) { b.PayloadDigest = "other" },
		"replay digest":  func(b *CheckpointBinding) { b.ReplayDigest = "other" },
		"task":           func(b *CheckpointBinding) { b.TaskID = "t2" },
		"status":         func(b *CheckpointBinding) { b.Status = CheckpointExhausted },
	}
	for name, mutate := range differ {
		c := newer
		mutate(&c)
		if got, _ := CommittedCheckpoint(pair(prepared, checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t1", c)), "t1", "s1"); got != older {
			t.Fatalf("a commit with a different %s was authoritative: %+v", name, got)
		}
	}
	commit := checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t1", newer)
	for name, history := range map[string][]event.Event{
		"non-engine prepare":    pair(checkpointEvent(event.CheckpointPrepared, event.SourceClaude, "s1", "t1", newer), commit),
		"non-engine commit":     pair(prepared, checkpointEvent(event.CheckpointCommitted, event.SourceReviewer, "s1", "t1", newer)),
		"other-session prepare": pair(checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s2", "t1", newer), commit),
		"other-session commit":  pair(prepared, checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s2", "t1", newer)),
		"other-task records":    pair(checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t2", newer), checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t2", newer)),
		"commit before prepare": pair(commit, prepared),
	} {
		if got, _ := CommittedCheckpoint(history, "t1", "s1"); got != older {
			t.Fatalf("a %s was authoritative: %+v", name, got)
		}
	}
	if _, ok := CommittedCheckpoint([]event.Event{prepared}, "t1", "s1"); ok {
		t.Fatal("a prepared checkpoint with no commit was committed")
	}
}

// A written payload reads back byte for byte, a malformed identity cannot name
// a file, and a payload is never overwritten.
func TestACheckpointPayloadIsWrittenOnceAndReadBackExactly(t *testing.T) {
	s, err := New(t.TempDir(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	payload, err := EncodeCheckpoint(liveCheckpoint())
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteCheckpoint(ckptA, payload); err != nil {
		t.Fatal(err)
	}
	back, err := s.ReadCheckpoint(ckptA)
	if err != nil || string(back) != string(payload) {
		t.Fatalf("the payload did not read back exactly: %v", err)
	}
	if err := s.WriteCheckpoint(ckptA, []byte("{}")); err == nil {
		t.Fatal("a committed payload was overwritten")
	}
	if err := s.WriteCheckpoint("../escape", payload); err == nil {
		t.Fatal("a path was accepted as a checkpoint identity")
	}
	if _, found, err := s.CommittedCheckpoint("t1", "s1"); err != nil || found {
		t.Fatalf("a session with no record is not an established absence: found %v err %v", found, err)
	}
}

// W13 THE PREPARED BOUNDARY. A checkpoint's replay boundary is its own owned
// PREPARED record: not a record another source, session or binding wrote,
// and not a COMMITTED record. What follows the boundary is outside it.
func TestB1W13ACheckpointIsBoundedAtItsOwnPreparedRecord(t *testing.T) {
	b := boundCheckpoint(ckptA)
	other := b
	other.ReplayDigest = "replay-other"
	history := []event.Event{
		event.New("s1", "t1", event.SourceSystem, event.PlanProposed, "plan", nil),
		checkpointEvent(event.CheckpointPrepared, event.SourceClaude, "s1", "t1", b),
		checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s2", "t1", b),
		checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t1", other),
		checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t1", b),
		checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t1", b),
		event.New("s1", "t1", event.SourceSystem, event.PlanProposed, "a later transition", nil),
	}
	if at, ok := PreparedAt(history, b, "s1"); !ok || at != 5 {
		t.Fatalf("the boundary is not the owned PREPARED record: %d %v", at, ok)
	}
	if _, ok := PreparedAt(history[:5], b, "s1"); ok {
		t.Fatal("a checkpoint with no owned PREPARED record has a boundary")
	}
}

// F3 DURABLE RECORDS. A checkpoint record is appended through the synced path
// and reads back from the session record; an append that cannot be written is
// returned, never reported as success.
func TestB1W15ACheckpointRecordIsAppendedDurably(t *testing.T) {
	s, err := New(t.TempDir(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	// FIXTURE MIGRATION (70B2a1): the checkpoint records stand on the task's
	// TaskCreated root, written by the session that owns them.
	if err := appendTip(t, s, t.Context(), false, event.New("s1", "t1", event.SourceSystem, event.TaskCreated, "the task", nil)); err != nil {
		t.Fatal(err)
	}
	b := boundCheckpoint(ckptA)
	if err := appendTip(t, s, t.Context(), true, checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t1", b)); err != nil {
		t.Fatal(err)
	}
	if err := appendTip(t, s, t.Context(), true, checkpointEvent(event.CheckpointCommitted, event.SourceSystem, "s1", "t1", b)); err != nil {
		t.Fatal(err)
	}
	if got, found, err := s.CommittedCheckpoint("t1", "s1"); err != nil || !found || got != b {
		t.Fatalf("the durably appended pair is not the committed checkpoint: %+v %v %v", got, found, err)
	}
	s.path = s.path + "/not-a-file"
	if err := appendTip(t, s, t.Context(), true, checkpointEvent(event.CheckpointPrepared, event.SourceSystem, "s1", "t1", b)); err == nil {
		t.Fatal("an append that could not be written was reported durable")
	}
}

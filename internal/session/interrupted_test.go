package session

import (
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

func ev(taskID string, source event.Source, kind event.Kind, summary string) event.Event {
	return event.Event{TaskID: taskID, Source: source, Kind: kind, Summary: summary}
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
		ev("t1", event.SourceUser, event.AuthorityResolved, "Authorize the architectural change"),
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
		ev("t1", event.SourceUser, event.AuthorityResolved, "Authorize the architectural change"),
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

// AN INVOCATION MAY END; ONLY THE WORK ITSELF MAY END THE TASK -- the reader side.
//
// historyOwing is a task as the engine writes it up to the moment one invocation
// ends: created, planned, its candidate reported resumable, a bounded REVISE
// that is the next actor's instruction, and a spent review budget that owes an
// architect re-plan. Every field of it is an obligation a continuation needs.
func historyOwing(taskID string) []event.Event {
	return []event.Event{
		ev(taskID, event.SourceSystem, event.TaskCreated, "separate the two endings"),
		{TaskID: taskID, Source: event.SourceArchitect, Kind: event.PlanProposed, Summary: "the plan",
			Payload: []byte(`{"plan_source":"architect","plan_digest":"sha256:abc"}`)},
		{TaskID: taskID, Source: event.SourceGit, Kind: event.CandidateResolved, Summary: "resumable",
			Payload: []byte(`{"disposition":"resumable","reason":"the run did not converge and the candidate holds work that resumable state references"}`)},
		{TaskID: taskID, Source: event.SourceReviewer, Kind: event.ReviewCompleted, Summary: "REVISE",
			Payload: []byte(`{"decision":"revise","summary":"the witness does not fail for its own reason"}`)},
		{TaskID: taskID, Source: event.SourceSystem, Kind: event.WorkflowNotConverged, Summary: "not converged",
			Payload: []byte(`{"task_id":"` + taskID + `","owed":"architect_replan"}`)},
	}
}

func invocationFailed(taskID, summary string) event.Event {
	return event.Event{TaskID: taskID, Source: event.SourceSystem, Kind: event.WorkflowInvocationFailed, Summary: summary}
}

// sameObligations reports the first field in which two reconstructions of one
// task differ, or "" when a continuation would be handed exactly the same thing.
func sameObligations(a, b Interrupted) string {
	for name, pair := range map[string][2]string{
		"TaskID":               {a.TaskID, b.TaskID},
		"Task":                 {a.Task, b.Task},
		"Plan":                 {a.Plan, b.Plan},
		"PlanSource":           {a.PlanSource, b.PlanSource},
		"PlanDigest":           {a.PlanDigest, b.PlanDigest},
		"Review":               {a.Review, b.Review},
		"AwaitingReviewRecord": {string(a.AwaitingReviewRecord), string(b.AwaitingReviewRecord)},
		"AwaitingAuthority":    {string(a.AwaitingAuthority), string(b.AwaitingAuthority)},
		"BlockedExternal":      {string(a.BlockedExternal), string(b.BlockedExternal)},
		"NotConverged":         {string(a.NotConverged), string(b.NotConverged)},
		"RestorationRefused":   {string(a.RestorationRefused), string(b.RestorationRefused)},
		"ProspectiveRecord":    {string(a.ProspectiveRecord), string(b.ProspectiveRecord)},
		"TestEditRecord":       {string(a.TestEditRecord), string(b.TestEditRecord)},
	} {
		if pair[0] != pair[1] {
			return name
		}
	}
	if a.Planned != b.Planned {
		return "Planned"
	}
	if a.AwaitingReview != b.AwaitingReview {
		return "AwaitingReview"
	}
	return ""
}

// W2 THE TWO STATEMENTS AGREE, reader side. The history a run writes when it
// calls its candidate resumable and then ends its invocation must be read as a
// resumable task. The emitter half is the workflow package's
// TestW2WhatARunSaysOfItsCandidateAgreesWithHowItEnds.
//
// Fails if the reader treats the invocation ending as the task's.
func TestW2ACandidateReportedResumableIsDiscoverable(t *testing.T) {
	history := append(historyOwing("t1"), invocationFailed("t1", "no bounded implementor produced an acceptable candidate"))
	reportsResumable := true // the CandidateResolved in historyOwing says "resumable"
	discoverable := len(FindInterrupted(history)) == 1
	if reportsResumable != discoverable {
		t.Fatalf("the run called its candidate resumable and the reconstruction answers discoverable=%v", discoverable)
	}
}

// W3 GENUINE WORK FAILURE STILL ENDS THE TASK -- CONTROL. A resumable candidate
// report does not revive a task whose work failed: candidate disposition is not
// a second liveness authority.
//
// Fails if WorkflowFailed stops ending the task, or if anything earlier in the
// history (the resumable disposition, the owed re-plan) outranks it.
func TestW3AWorkFailureIsNotRevivedByAResumableCandidate(t *testing.T) {
	history := append(historyOwing("t1"), ev("t1", event.SourceSystem, event.WorkflowFailed,
		"prospective surface refuted: x_test.go imports \"bytes\""))
	if got := FindInterrupted(history); len(got) != 0 {
		t.Fatalf("a task whose work failed is discoverable again: %+v", got)
	}
}

// W4 AN ADMITTED CHANGE AND AN OBSERVATION STILL END THE TASK -- CONTROL, over
// the same history that owes everything.
//
// Fails if either member of the task-terminal set stopped ending the task.
func TestW4CompletedAndObservedStillEndTheTask(t *testing.T) {
	for _, terminal := range []event.Kind{event.WorkflowCompleted, event.WorkflowObserved} {
		history := append(historyOwing("t1"), ev("t1", event.SourceSystem, terminal, "done"))
		if got := FindInterrupted(history); len(got) != 0 {
			t.Fatalf("%s left the task discoverable: %+v", terminal, got)
		}
	}
}

// W5 THE READER KEYS ON THE KIND, NEVER ON THE REASON. Each ending carries the
// other ending's wording, and the reader must not notice.
//
// Fails if a reason's text changes what the reconstruction decides.
func TestW5TheReaderDecidesByKindNotReasonText(t *testing.T) {
	failedTheWork := append(historyOwing("t1"), ev("t1", event.SourceSystem, event.WorkflowFailed,
		"the invocation could not proceed; the task is preserved and still resumable"))
	if got := FindInterrupted(failedTheWork); len(got) != 0 {
		t.Fatalf("a WorkflowFailed worded as an invocation ending was read as one: %+v", got)
	}
	endedTheInvocation := append(historyOwing("t1"), invocationFailed("t1",
		"the work failed: prospective surface refuted: the candidate is final"))
	if got := FindInterrupted(endedTheInvocation); len(got) != 1 {
		t.Fatalf("a WorkflowInvocationFailed worded as a work failure was read as one: %+v", got)
	}
}

// W6 THE COMPOUNDING CASE. An invocation that failed for a reason unrelated to
// the work -- a turn it could not bind -- leaves the task exactly as it was:
// every obligation, field by field. This is the case that cost two candidates:
// a not-converged task whose resume hit a binding defect ended FAILED.
//
// Fails if the invocation ending ends the task, or changes or erases any
// obligation an earlier event recorded.
func TestW6AnUnboundInvocationLeavesTheTaskAsItWas(t *testing.T) {
	before := FindInterrupted(historyOwing("t1"))
	if len(before) != 1 || len(before[0].NotConverged) == 0 || before[0].Review == "" || !before[0].Planned {
		t.Fatalf("the history does not owe what this witness needs, so it proves nothing: %+v", before)
	}
	after := FindInterrupted(append(historyOwing("t1"),
		ev("t1", event.SourceSystem, event.Status, "resuming the same task at the turn it is owed"),
		invocationFailed("t1", "the task cannot be resumed at its blocked turn: the external block record is bound to task t2, not to this one")))
	if len(after) != 1 {
		t.Fatalf("an unbound invocation ended the task: %+v", after)
	}
	if field := sameObligations(before[0], after[0]); field != "" {
		t.Fatalf("an unbound invocation changed the task's %s: before %+v, after %+v", field, before[0], after[0])
	}
}

// W7 HISTORICAL RECONSTRUCTION. Ruled 2026-09-26: a pre-change history cannot be
// recovered. An old record carries only WorkflowFailed whatever ended it, and it
// cannot say which ending it was; the resumable disposition beside it is not
// authority to reinterpret it, so such a task stays ended. The two candidates
// already stranded that way are a sunk cost, not a reason to weaken W3.
//
// The same history written after the change, ending WorkflowInvocationFailed,
// is discoverable WITH its obligations intact, not as a bare id.
//
// Fails if the legacy history is silently revived, or if the new one is lost or
// reconstructed without what it owed.
func TestW7APreChangeFailedHistoryStaysEndedAndANewOneKeepsItsObligations(t *testing.T) {
	legacy := append(historyOwing("t1"), ev("t1", event.SourceSystem, event.WorkflowFailed,
		"no bounded implementor produced an acceptable candidate: claude: the candidate did not change between review cycles"))
	if got := FindInterrupted(legacy); len(got) != 0 {
		t.Fatalf("a pre-change WorkflowFailed history was silently revived: %+v", got)
	}

	owed := FindInterrupted(historyOwing("t1"))
	current := FindInterrupted(append(historyOwing("t1"), invocationFailed("t1",
		"no bounded implementor produced an acceptable candidate: claude: the candidate did not change between review cycles")))
	if len(current) != 1 {
		t.Fatalf("a history ending WorkflowInvocationFailed is not discoverable: %+v", current)
	}
	if field := sameObligations(owed[0], current[0]); field != "" {
		t.Fatalf("the rediscovered task lost its %s: %+v", field, current[0])
	}
}

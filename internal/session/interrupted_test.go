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

// AN INVOCATION MAY END; ONLY THE WORK ENDS THE TASK -- the reconstruction's
// witnesses. The emitter side is witnessed in workflow/nonconvergence_test.go.

const (
	measuredFailure = "no bounded implementor produced an acceptable candidate: claude: the candidate did not change between review cycles"
	resumableRecord = `{"disposition":"resumable","reason":"the run did not converge and the candidate holds work that resumable state references"}`
	revisedVerdict  = `{"decision":"revise","summary":"proof incomplete","findings":[{"id":"f1","severity":"blocking","class":"code","claim":"the refusal does not refuse","reference":"plan.go","reason":"it continues"}]}`
)

func withPayload(taskID string, source event.Source, kind event.Kind, summary, payload string) event.Event {
	e := ev(taskID, source, kind, summary)
	e.Payload = []byte(payload)
	return e
}

func plannedTask(taskID string) []event.Event {
	return []event.Event{
		ev(taskID, event.SourceSystem, event.TaskCreated, "the objective"),
		ev(taskID, event.SourceArchitect, event.PlanProposed, "the plan"),
	}
}

func then(history []event.Event, more ...event.Event) []event.Event {
	return append(append([]event.Event{}, history...), more...)
}

// W3 GENUINE WORK FAILURE STILL ENDS THE TASK -- CONTROL. A WorkflowFailed with
// no resumable disposition in its own invocation segment is final: bare; after a
// resumable disposition from an EARLIER invocation; after a resumable claim a
// later disposition withdrew; and after a disposition record that does not
// validate. And the new invocation terminal does not end it.
//
// Fails if the reader treats a failure as non-terminal (forbidden repair 2), if
// the segment leaks across invocations, or if an unvalidated record vouches.
func TestW3AWorkFailureStillEndsTheTask(t *testing.T) {
	for name, history := range map[string][]event.Event{
		"bare": then(plannedTask("t1"),
			ev("t1", event.SourceSystem, event.WorkflowFailed, "the work failed")),
		"resumable in an earlier invocation": then(plannedTask("t1"),
			withPayload("t1", event.SourceGit, event.CandidateResolved, "resumable", resumableRecord),
			ev("t1", event.SourceSystem, event.WorkflowStopped, "stopped"),
			ev("t1", event.SourceSystem, event.WorkflowFailed, "the work failed")),
		"resumable claim withdrawn": then(plannedTask("t1"),
			withPayload("t1", event.SourceGit, event.CandidateResolved, "resumable", resumableRecord),
			withPayload("t1", event.SourceGit, event.CandidateResolved, "retained", `{"disposition":"retained","reason":"accepted"}`),
			ev("t1", event.SourceSystem, event.WorkflowFailed, "the work failed")),
		"unvalidated disposition": then(plannedTask("t1"),
			withPayload("t1", event.SourceGit, event.CandidateResolved, "resumable", `{"disposition":"resumable"}`),
			ev("t1", event.SourceSystem, event.WorkflowFailed, "the work failed")),
		"undecodable disposition": then(plannedTask("t1"),
			withPayload("t1", event.SourceGit, event.CandidateResolved, "resumable", `{"disposition":`),
			ev("t1", event.SourceSystem, event.WorkflowFailed, "the work failed")),
	} {
		if got := FindInterrupted(history); len(got) != 0 {
			t.Errorf("%s: a task whose work failed is offered for resume: %+v", name, got)
		}
	}
	if got := FindInterrupted(then(plannedTask("t1"),
		ev("t1", event.SourceSystem, event.WorkflowInvocationFailed, "the work failed"))); len(got) != 1 {
		t.Fatalf("an invocation failure ended its task: %+v", got)
	}
}

// W4 AN ADMITTED CHANGE AND AN OBSERVATION STILL END THE TASK -- CONTROL. They
// end it unconditionally: even a resumable disposition in the same segment,
// which rescues a legacy WorkflowFailed, does not rescue them.
//
// Fails if the legacy exception is widened to the rest of the terminal set.
func TestW4CompletionAndObservationStillEndTheTask(t *testing.T) {
	for _, terminal := range []event.Kind{event.WorkflowCompleted, event.WorkflowObserved} {
		for _, history := range [][]event.Event{
			then(plannedTask("t1"), ev("t1", event.SourceSystem, terminal, "done")),
			then(plannedTask("t1"),
				withPayload("t1", event.SourceGit, event.CandidateResolved, "resumable", resumableRecord),
				ev("t1", event.SourceSystem, terminal, "done")),
		} {
			if got := FindInterrupted(history); len(got) != 0 {
				t.Errorf("%s did not end its task: %+v", terminal, got)
			}
		}
	}
}

// W5 THE DISTINCTION IS STRUCTURAL, NOT TEXTUAL. The same reason text -- the
// measured one, a sentence saying the task is resumable, a sentence saying the
// work failed, and none at all -- is read under both kinds. The answer follows
// the kind every time and the text never once, which is the asserted absence of
// any decision made by matching the reason.
//
// Fails if any reason string changes an answer the kind decided.
func TestW5TheEndingIsReadFromTheKindNotTheReason(t *testing.T) {
	for _, reason := range []string{
		measuredFailure,
		"invocation ended; the task is resumable and owes a re-plan",
		"the work itself failed and the task is over",
		"",
	} {
		work := FindInterrupted(then(plannedTask("t1"), ev("t1", event.SourceSystem, event.WorkflowFailed, reason)))
		invocation := FindInterrupted(then(plannedTask("t1"), ev("t1", event.SourceSystem, event.WorkflowInvocationFailed, reason)))
		if len(work) != 0 || len(invocation) != 1 {
			t.Errorf("reason %q decided the ending: under workflow.failed %d task(s), under workflow.invocation_failed %d",
				reason, len(work), len(invocation))
		}
	}
}

// W7 HISTORICAL RECONSTRUCTION. The ruling, stated and tested: a log written
// before workflow.invocation_failed existed is read as it was written, and a
// legacy WorkflowFailed is recoverable ONLY when its own invocation segment
// already recorded a structurally valid candidate disposition of resumable.
//
// The recovered history is the measured one -- created, planned, a bounded
// REVISE, the resumable disposition, the receipt, then workflow.failed with
// the reason that was actually written -- and it comes back WITH its plan and
// its open finding, not as a bare id. The same history without the disposition
// stays ended, and so does the objective-27 shape (a NOT_CONVERGED invocation,
// then a resume that failed without re-recording its candidate): those pre-change
// tasks carry no discriminating fact and are written off, not revived.
//
// Fails if the reader recovers a legacy failure without the fact, or recovers
// it without its obligations.
func TestW7AHistoricalFailureIsReadByTheFactsItAlreadyCarries(t *testing.T) {
	recorded := then(plannedTask("t1"),
		withPayload("t1", event.SourceReviewer, event.ReviewCompleted, "REVISE", revisedVerdict),
		withPayload("t1", event.SourceGit, event.CandidateResolved, "resumable", resumableRecord),
		ev("t1", event.SourceSystem, event.RunReceipt, "governed run receipt: INCOMPLETE / FAILED"),
		ev("t1", event.SourceSystem, event.WorkflowFailed, measuredFailure),
	)
	got := FindInterrupted(recorded)
	if len(got) != 1 || got[0].TaskID != "t1" {
		t.Fatalf("a history whose own run called its candidate resumable is still unreachable: %+v", got)
	}
	if !got[0].Planned || got[0].Plan != "the plan" || got[0].Task != "the objective" {
		t.Fatalf("the recovered task lost its plan or objective: %+v", got[0])
	}
	if !contains(got[0].Review, "the refusal does not refuse") {
		t.Fatalf("the recovered task lost the finding it still owes: %q", got[0].Review)
	}

	// The same history without the disposition: nothing distinguishes it from a
	// genuine work failure, so it stays ended.
	var bare []event.Event
	for _, e := range recorded {
		if e.Kind != event.CandidateResolved {
			bare = append(bare, e)
		}
	}
	if got := FindInterrupted(bare); len(got) != 0 {
		t.Fatalf("a bare historical workflow.failed was revived: %+v", got)
	}

	// Objective 27's shape: resumable, NOT_CONVERGED, then a resume whose own
	// segment recorded no disposition before it failed. Written off.
	objective27 := then(plannedTask("t1"),
		withPayload("t1", event.SourceGit, event.CandidateResolved, "resumable", resumableRecord),
		withPayload("t1", event.SourceSystem, event.WorkflowNotConverged, "not converged",
			`{"task_id":"t1","implementers":["claude"],"review_cycles":3,"owed":"architect_replan"}`),
		ev("t1", event.SourceSystem, event.Status, "resuming"),
		ev("t1", event.SourceSystem, event.WorkflowFailed, "the resumed turn could not be bound"),
	)
	if got := FindInterrupted(objective27); len(got) != 0 {
		t.Fatalf("a resumable disposition from an earlier invocation vouched for a later failure: %+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

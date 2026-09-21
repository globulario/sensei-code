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

// preserving builds the payload a preserving FAILED terminal carries. The
// workflow package writes it; this package must recognise exactly it.
func preserving(taskID, kind, reason string) []byte {
	return []byte(`{"continuation_obligation":{"task_id":"` + taskID + `","kind":"` + kind +
		`","reason":"` + reason + `","candidate_base_sha":"abc123","candidate_branch":"sensei-code/` + taskID + `"}}`)
}

// A REFUSED INVOCATION IS NOT A FINISHED TASK.
//
// DF-23 (task-1789960053774525922): a validated, audited candidate of 1796
// insertions was killed at 13:27:02Z by a restoration refusal that was locally
// correct. The refusal ended the INVOCATION and the terminal ended the TASK, so
// `resume --list` stopped showing it at all and the work was reachable only as
// a new task with a new identity.
//
// The refusal still refuses. What changes is that the terminal carries the
// obligation, and this reconstruction keeps the task.
func TestAPreservedInvocationLeavesTheTaskDiscoverableWithItsObligation(t *testing.T) {
	const reason string = "cannot resume t1: the pinned world authorises 0 existing-test edit(s) and the record holds 7"
	payload := preserving("t1", "restoration_refused", reason)
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "close the lifecycle boundary"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
		{TaskID: "t1", Source: event.SourceSystem, Kind: event.WorkflowFailed,
			Summary: "the invocation ended and the task is preserved", Payload: payload},
	})
	if len(got) != 1 {
		t.Fatalf("a refused invocation ended the task: %+v", got)
	}
	if !got[0].Planned || got[0].TaskID != "t1" {
		t.Fatalf("the task came back without its identity or its plan: %+v", got[0])
	}
	// Byte for byte. A continuation that re-renders the obligation is a
	// continuation that can disagree with the record it came from.
	if string(got[0].Continuation) != string(payload) {
		t.Fatalf("the obligation was not carried verbatim:\n got %s\nwant %s", got[0].Continuation, payload)
	}
	// Each kind of the closed vocabulary preserves, and each is recognised
	// by membership rather than by anything about the record's shape.
	for _, kind := range []string{"restoration_refused", "implementation_declined", "authority_reentry_unavailable"} {
		again := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			{TaskID: "t1", Kind: event.WorkflowFailed, Payload: preserving("t1", kind, "r")},
		})
		if len(again) != 1 {
			t.Errorf("%s ended the task: %+v", kind, again)
		}
	}
}

// THE CONTROL THAT KEEPS THIS FROM BECOMING "FAILURE IS IMPOSSIBLE".
//
// Every one of these is a FAILED terminal that does NOT establish a remaining
// obligation, and each must end the task exactly as it always has. The last two
// matter most: "preserve the existing record" must never widen into "preserve
// anything shaped vaguely like one", so a corrupt or task-mismatched record
// fails closed rather than becoming a resumable obligation because continuity
// exists.
func TestOnlyAClosedWellFormedRecordKeepsAFailedTaskAlive(t *testing.T) {
	for name, payload := range map[string][]byte{
		"no payload at all":     nil,
		"an ordinary payload":   []byte(`{"workspace":"/tmp/w","implementor":"claude"}`),
		"unreadable json":       []byte(`{"continuation_obligation":`),
		"no obligation key":     []byte(`{"task_id":"t1","kind":"restoration_refused","reason":"r"}`),
		"a kind nobody defined": preserving("t1", "looks_continuable", "r"),
		"an empty kind":         preserving("t1", "", "r"),
		"another task's record": preserving("t2", "restoration_refused", "r"),
		"no reason":             preserving("t1", "restoration_refused", ""),
	} {
		got := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
			{TaskID: "t1", Source: event.SourceSystem, Kind: event.WorkflowFailed, Summary: "failed", Payload: payload},
		})
		if len(got) != 0 {
			t.Errorf("%s: the task survived a terminal that establishes no obligation: %+v", name, got)
		}
	}
}

// NO RESURRECTION. Done is monotonic.
//
// "Preserve everything" is the same bug facing the other way, and this is where
// it would land: a genuine terminal followed by a preserving record -- appended
// by a later process, a replayed log, or a malicious one -- must not bring the
// task back.
func TestAGenuineTerminalIsNeverResurrectedByALaterPreservingRecord(t *testing.T) {
	for name, terminal := range map[string]event.Kind{
		"failed":    event.WorkflowFailed,
		"completed": event.WorkflowCompleted,
		"observed":  event.WorkflowObserved,
	} {
		got := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
			{TaskID: "t1", Source: event.SourceSystem, Kind: terminal, Summary: "ended"},
			{TaskID: "t1", Source: event.SourceSystem, Kind: event.WorkflowFailed,
				Summary: "preserved", Payload: preserving("t1", "restoration_refused", "r")},
		})
		if len(got) != 0 {
			t.Errorf("%s: a preserving record resurrected an ended task: %+v", name, got)
		}
	}
}

// A PRESERVED INVOCATION LEAVES THE STANDING QUESTION EXACTLY WHERE IT WAS.
//
// DF-21 (task-1789937196152726632) destroyed a recorded human authorization in
// four seconds. The question is the authority object: a re-entry that could not
// start its dependency preserves the question, and preserves it byte for byte
// -- no candidate, no answer, no actor, no scope invented on the way.
func TestAPreservedAuthorityReentryLeavesTheQuestionUntouched(t *testing.T) {
	question := []byte(`{"condition":"graph coverage is absent","decision":{"subject":"Authorize?","options":[{"id":"1"}]},"task_id":"t1"}`)
	got := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "widen the boundary"),
		{TaskID: "t1", Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority,
			Summary: "deferred", Payload: question},
		{TaskID: "t1", Source: event.SourceSystem, Kind: event.WorkflowFailed,
			Summary: "the deferred question could not be re-entered",
			Payload: []byte(`{"continuation_obligation":{"task_id":"t1","kind":"authority_reentry_unavailable","reason":"start Sensei: exec: no command"}}`)},
	})
	if len(got) != 1 {
		t.Fatalf("an unresolved question plus an unavailable dependency killed the task: %+v", got)
	}
	if string(got[0].AwaitingAuthority) != string(question) {
		t.Fatalf("the standing question did not survive verbatim:\n got %s\nwant %s", got[0].AwaitingAuthority, question)
	}
	// And it is still the question that routes: the continuation record is
	// context beside it, never a second authority source.
	if len(got[0].Continuation) == 0 {
		t.Fatal("the invocation left no account of why it stopped")
	}
	// A question that was ANSWERED stays answered. Preservation must not
	// resurrect a resolved decision.
	answered := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "widen the boundary"),
		{TaskID: "t1", Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority, Summary: "deferred", Payload: question},
		ev("t1", event.SourceUser, event.AuthorityResolved, "Authorize the architectural change"),
		{TaskID: "t1", Source: event.SourceSystem, Kind: event.WorkflowFailed, Summary: "preserved",
			Payload: preserving("t1", "implementation_declined", "every bounded implementor declined")},
	})
	if len(answered) != 1 {
		t.Fatalf("the task ended: %+v", answered)
	}
	if len(answered[0].AwaitingAuthority) != 0 {
		t.Fatalf("a settled question is standing again, so a resume would ask it twice: %s", answered[0].AwaitingAuthority)
	}
}

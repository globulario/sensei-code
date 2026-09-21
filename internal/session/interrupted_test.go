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

// preserving is a well-formed preserving-FAILED payload for task t1. It is
// written as literal bytes rather than built from the workflow package's type,
// because that package imports this one and the point of the test is that THIS
// reader, reading bytes off a disk written by some other process, retains the
// right records.
const preserving = `{"task_id":"t1","obligation":"restoration_refused",` +
	`"reason":"cannot resume t1: the recorded base hash of modfile/rule_test.go does not match its bytes at the pinned world",` +
	`"candidate_base_sha":"e1da8dd0000000000000000000000000000000000","candidate_branch":"sensei-code/t1"}`

func failed(taskID, payload string) event.Event {
	return event.Event{TaskID: taskID, Source: event.SourceSystem, Kind: event.WorkflowFailed,
		Summary: "the invocation ended", Payload: []byte(payload)}
}

// WITNESS 1, at the reader. A FAILED record that states a continuation
// obligation ends the INVOCATION: the task stays discoverable and carries the
// obligation byte for byte.
//
// DF-23 (task-1789960053774525922, 2026-09-20) is what this is about. A
// validated, audited candidate of 1,796 insertions was removed from
// `resume --list` entirely by a refusal that was locally correct -- the
// restoration exact-match guard doing exactly its job.
func TestAPreservedFailureEndsTheInvocationAndNotTheTask(t *testing.T) {
	events := []event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "separate refusal from failure"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the bounded plan"),
		failed("t1", preserving),
	}
	got := FindInterrupted(events)
	if len(got) != 1 {
		t.Fatalf("a preserved invocation ended the task: %+v", got)
	}
	if string(got[0].Preserved) != preserving {
		t.Fatalf("the obligation was not carried verbatim:\n got %s\nwant %s", got[0].Preserved, preserving)
	}
	if !got[0].Planned || got[0].Task != "separate refusal from failure" {
		t.Fatalf("the task lost what it needs to be continued: %+v", got[0])
	}
}

// WITNESS 5, THE CONTROL. This repair must not make task-terminal FAILED
// unreachable, so every FAILED record that does not state a continuation
// obligation still ends the task -- and each of the four reasons a record fails
// to state one is checked separately, because they are four different defects
// and a single case would let three of them regress unseen.
func TestAFailureThatStatesNoObligationStillEndsTheTask(t *testing.T) {
	for name, payload := range map[string]string{
		// A genuine failure: the engine recorded no payload at all.
		"genuine": "",
		// Historical: written before this field existed. Its payload is some
		// other run's structured detail.
		"historical": `{"workspace":"/tmp/wt","implementor":"claude","publication":"failed"}`,
		// Unclassified: shaped like the record but owing nothing this engine
		// knows. Read by MEMBERSHIP -- an unknown obligation is terminal, never
		// "some other obligation" that keeps the task alive.
		"unclassified obligation": `{"task_id":"t1","obligation":"something_else","reason":"r","candidate_base_sha":"abc"}`,
		"absent obligation":       `{"task_id":"t1","reason":"r","candidate_base_sha":"abc"}`,
		// Malformed: cannot say why, or about which candidate.
		"no reason":         `{"task_id":"t1","obligation":"restoration_refused","candidate_base_sha":"abc"}`,
		"blank reason":      `{"task_id":"t1","obligation":"restoration_refused","reason":"  ","candidate_base_sha":"abc"}`,
		"no candidate base": `{"task_id":"t1","obligation":"restoration_refused","reason":"r"}`,
		"unreadable":        `{"task_id":`,
		// Bound to a different task. Honouring it would let one task's refusal
		// keep another one alive.
		"foreign task": `{"task_id":"t2","obligation":"restoration_refused","reason":"r","candidate_base_sha":"abc"}`,
	} {
		got := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
			failed("t1", payload),
		})
		if len(got) != 0 {
			t.Errorf("%s: the task survived a terminal failure: %+v", name, got)
		}
	}
}

// WITNESS 7, THE OTHER CONTROL. Once a genuine task terminal exists, nothing
// brings the task back -- not a later preserving record, however well formed.
//
// Without this, "preserve the obligation" becomes "preserve everything", which
// is the same defect facing the other way: a task that can never be finished
// and a task that cannot be found are the same disagreement between the record
// and the lifecycle.
func TestAGenuineTerminalIsNotResurrectedByALaterPreservingRecord(t *testing.T) {
	for name, terminal := range map[string]event.Kind{
		"completed": event.WorkflowCompleted,
		"failed":    event.WorkflowFailed,
		"observed":  event.WorkflowObserved,
	} {
		got := FindInterrupted([]event.Event{
			ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
			ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
			ev("t1", event.SourceSystem, terminal, "ended"),
			failed("t1", preserving),
		})
		if len(got) != 0 {
			t.Errorf("%s: a preserving record reopened a task that had ended: %+v", name, got)
		}
	}
	// And the reverse order is the ordinary one: a preserved invocation
	// followed by a real failure ends the task, and the obligation goes with
	// it rather than being left attached to work that has finished.
	ended := FindInterrupted([]event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
		failed("t1", preserving),
		failed("t1", ""),
	})
	if len(ended) != 0 {
		t.Fatalf("a genuine failure after a preserved one left the task active: %+v", ended)
	}
}

// WITNESSES 2 AND 4, THE CONTROLS THAT MUST NOT MOVE. A candidate awaiting an
// independent review, and a turn a provider proved it could not serve, are
// already invocation terminals. A preserving refusal arriving after either of
// them must leave that obligation exactly where it was: the task owes the
// review, or owes the blocked turn, and it now also carries the refusal.
//
// The combination is the case: each of these was, on its own, once emitted as
// WorkflowFailed and read here as the end of the task.
func TestAPreservedFailureDoesNotConsumeAnObligationAlreadyStanding(t *testing.T) {
	base := []event.Event{
		ev("t1", event.SourceSystem, event.TaskCreated, "a task"),
		ev("t1", event.SourceArchitect, event.PlanProposed, "the plan"),
	}
	const block = `{"task_id":"t1","role":"implementer","provider":"chatgpt","reason":"quota","retry_at_state":"UNKNOWN"}`
	const question = `{"condition":"graph coverage is absent","decision":{"subject":"Authorize?","options":[{"id":"1"}]}}`
	for name, standing := range map[string]event.Event{
		"awaiting review": {TaskID: "t1", Source: event.SourceReviewer, Kind: event.WorkflowAwaitingReview,
			Summary: "owed a review", Payload: []byte(`{"review_kind":"unanswered"}`)},
		"blocked external": {TaskID: "t1", Source: event.SourceSystem, Kind: event.WorkflowBlockedExternal,
			Summary: "blocked", Payload: []byte(block)},
		"awaiting authority": {TaskID: "t1", Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority,
			Summary: "deferred", Payload: []byte(question)},
	} {
		got := FindInterrupted(append(append([]event.Event(nil), base...), standing, failed("t1", preserving)))
		if len(got) != 1 {
			t.Fatalf("%s: the task ended: %+v", name, got)
		}
		task := got[0]
		if string(task.Preserved) != preserving {
			t.Errorf("%s: the refusal was not carried: %s", name, task.Preserved)
		}
		switch name {
		case "awaiting review":
			if !task.AwaitingReview {
				t.Error("awaiting review: the review obligation was consumed by the refusal")
			}
		case "blocked external":
			if string(task.BlockedExternal) != block {
				t.Errorf("blocked external: the blocked turn was lost: %s", task.BlockedExternal)
			}
		case "awaiting authority":
			// AND NO AUTHORITY WAS MINTED. The question is still the one that
			// was asked, byte for byte; nothing about being preserved answers it.
			if string(task.AwaitingAuthority) != question {
				t.Errorf("awaiting authority: the standing question changed: %s", task.AwaitingAuthority)
			}
		}
	}
}

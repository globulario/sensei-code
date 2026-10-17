package session

// N5 P2-B (RULING-366 §17; §30 W3, mutants M1 and M5). The workspace-quarantine
// terminal ends the invocation and not the task: the fold keeps the task
// discoverable, carries the observation byte for byte, and leaves every other
// obligation -- a standing question, an owed review, the plan -- exactly as it
// was. No unrelated status line or candidate resolution clears it. It is
// historical: the current candidate record, not this field, decides whether
// the workspace is quarantined now.

import (
	"encoding/json"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

// W3. Read through the JSON form of Interrupted so the witness compiles on a
// tree whose fold has no field for the observation at all.
func TestW3TheFoldKeepsAQuarantinedTaskListedAndOwed(t *testing.T) {
	const task = "t-wq"
	question := json.RawMessage(`{"task_id":"t-wq","decision":{"subject":"May this change proceed?","options":[{"id":"1"}]}}`)
	review := json.RawMessage(`{"task_id":"t-wq","request_id":"r-1"}`)
	payload := map[string]any{"task_id": task, "recorded": true, "invocation": "exec-7", "phase": "certify",
		"workspace": "/repo/.worktrees/t-wq", "baseline": "/repo/.worktrees/t-wq-baseline", "reason": "no established teardown"}
	quarantined := event.New("A", task, event.SourceSystem, workspaceQuarantinedKind, "the invocation ended quarantined", payload)
	evs := []event.Event{
		event.New("A", task, event.SourceSystem, event.TaskCreated, "the objective", nil),
		event.New("A", task, event.SourceArchitect, event.PlanProposed, "the plan", map[string]string{"plan_source": "architect"}),
		{ID: "q", SessionID: "A", TaskID: task, Source: event.SourceUser, Kind: event.WorkflowAwaitingAuthority, Summary: "deferred", Payload: question},
		{ID: "r", SessionID: "A", TaskID: task, Source: event.SourceSystem, Kind: event.WorkflowAwaitingReview, Summary: "owed", Payload: review},
		quarantined,
		// Unrelated records after the terminal: neither clears it (M5).
		event.New("A", task, event.SourceSystem, event.Status, "a status line", nil),
		event.New("A", task, event.SourceGit, event.CandidateResolved, "a resolution", map[string]string{"disposition": "kept"}),
	}
	got := FindInterrupted(evs)
	if len(got) != 1 || got[0].TaskID != task {
		t.Fatalf("the quarantined task is not listed (M1: made task-terminal): %+v", got)
	}
	in := got[0]
	if string(in.AwaitingAuthority) != string(question) {
		t.Errorf("the standing question did not survive the quarantine: %s", in.AwaitingAuthority)
	}
	if !in.AwaitingReview || string(in.AwaitingReviewRecord) != string(review) {
		t.Errorf("the owed review did not survive the quarantine: %v %s", in.AwaitingReview, in.AwaitingReviewRecord)
	}
	if !in.Planned || in.Plan != "the plan" {
		t.Errorf("the plan identity did not survive the quarantine: %+v", in)
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	obs, ok := fields["WorkspaceQuarantined"]
	if !ok {
		t.Fatal("the fold carries no WorkspaceQuarantined observation")
	}
	var carried json.RawMessage
	if err := json.Unmarshal(obs, &carried); err != nil || string(carried) != string(quarantined.Payload) {
		t.Fatalf("the observation is not the terminal's payload byte for byte (M5: cleared?): %s", obs)
	}
}

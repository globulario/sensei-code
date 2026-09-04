package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/taskstate"
)

// The continuity half of the state this slice introduced.
//
// The third outcome existed at three layers -- candidate outcome, task phase,
// run receipt -- and not at the fourth. It was emitted as WorkflowFailed, and
// FindInterrupted reads that as terminal, so a task whose receipt said "the
// obligation remains" and whose record said "reviewing" was, to the continuity
// layer, done. Three layers telling the truth and the fourth quietly
// disagreeing is worse than any one of them being wrong, because the
// disagreement is invisible until somebody looks for the task and it is gone.

// An awaiting-review run must leave a task the next invocation can find.
//
// Driven through implement rather than runCandidate, because the terminal is
// emitted there -- and the terminal is the thing under test.
func TestAnAwaitingReviewRunLeavesAResumableTask(t *testing.T) {
	h := newGateHarness(t, requiresIndependentReview(), roles.Unverified, "accept")

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	if failed != nil {
		t.Fatalf("the run failed rather than awaiting a review: %v", failed)
	}

	// The invocation ends. What survives is the event record, which is what a
	// later process reads.
	events := drainEvents(h.events)
	if !contains(events, event.WorkflowAwaitingReview) {
		t.Fatalf("no awaiting-review terminal was emitted: %v", kinds(events))
	}
	if contains(events, event.WorkflowFailed) {
		t.Fatal("an awaiting-review run reported a failure; nothing failed")
	}
	if contains(events, event.WorkflowCompleted) {
		t.Fatal("an awaiting-review run reported completion")
	}

	// A later process finds the task, by the same mechanism /resume uses.
	found := session.FindInterrupted(withPlan(events, "task-1", "the bounded plan"))
	if len(found) != 1 {
		t.Fatalf("the task is not discoverable for continuation: %d found", len(found))
	}
	if found[0].TaskID != "task-1" {
		t.Fatalf("a different task survived: %q", found[0].TaskID)
	}
	if !found[0].AwaitingReview {
		t.Fatal("the task was found and does not say what it is waiting for")
	}
}

// The opposite controls. Without these the test above would pass for a
// continuity layer that resumes everything.
func TestFailureAndCompletionRemainTerminalWhileAwaitingReviewDoesNot(t *testing.T) {
	for kind, resumable := range map[event.Kind]bool{
		event.WorkflowFailed:         false,
		event.WorkflowCompleted:      false,
		event.WorkflowAwaitingReview: true,
		event.WorkflowStopped:        true,
	} {
		events := withPlan(nil, "task-1", "the bounded plan")
		events = append(events, event.New("s", "task-1", event.SourceSystem, kind, "ended", nil))
		found := session.FindInterrupted(events)
		if got := len(found) == 1; got != resumable {
			t.Fatalf("%s: resumable=%v, want %v", kind, got, resumable)
		}
	}
}

// What the resumed task must not do: send a worker at a candidate nobody
// objected to, simply because a process restarted.
func TestResumingAnAwaitingReviewTaskReviewsBeforeItImplements(t *testing.T) {
	h := newGateHarness(t, requiresIndependentReview(), roles.Fresh, "accept")
	// The worker command is one that fails if it is ever called, so calling it
	// is not a subtle difference in output -- it is the test failing.
	h.worker = config.Agent{Name: "claude", Command: "false", Graph: "none"}
	h.engine.Config.Implementors = []config.Agent{h.worker}
	h.tc.AwaitingReview = true

	// The preserved candidate: the worktree already holds the work a previous
	// invocation produced and a reviewer accepted.
	if err := os.WriteFile(filepath.Join(h.work, "main.go"),
		[]byte("package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(7) }\n"), 0o644); err != nil {
		t.Fatalf("stage the preserved candidate: %v", err)
	}

	outcome, _, _, _, err := h.engine.runCandidate(context.Background(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	if err != nil {
		t.Fatalf("resuming at the review boundary failed: %v", err)
	}
	if !outcome.Accepted() {
		t.Fatalf("the preserved candidate was reviewed and concluded %q", outcome)
	}

	said := strings.Join(summaries(drainEvents(h.events)), "\n")
	if !strings.Contains(said, "resuming at the review boundary") {
		t.Fatalf("the run does not say it resumed at the review boundary:\n%s", said)
	}
	// The flag is consumed, so a later cycle in the same run does call a worker
	// rather than reviewing the same bytes forever.
	if h.tc.AwaitingReview {
		t.Fatal("the awaiting-review flag survived its own cycle")
	}
}

// The durable record a resumed task is continued from.
func TestTheAwaitingReviewTaskRecordSaysReviewingAndOwesAReview(t *testing.T) {
	root := t.TempDir()
	state := taskstate.State{
		TaskID: "task-1", Task: "do the thing", Phase: taskstate.Reviewing,
		BaseSHA: "abc", Worktree: "/tmp/wt", Branch: "sensei-code/task-1",
	}
	state.OpenFindings(openFindingsWith("", "", nil,
		[]string{"advisory accept by remote:abc; this task's measured risk requires an independent review, and has not had one"}))
	if err := state.Save(root); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded, found, err := taskstate.Load(root, "task-1")
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if loaded.Phase != taskstate.Reviewing {
		t.Fatalf("phase is %q; an unmet review obligation is not acceptance", loaded.Phase)
	}
	if loaded.Phase == taskstate.Accepted {
		t.Fatal("the record reads accepted while the required review has not happened")
	}
	var owed bool
	for _, f := range loaded.Open {
		if f.Source == "review obligation" && strings.Contains(f.Detail, "has not had one") {
			owed = true
		}
	}
	if !owed {
		t.Fatalf("the record does not say what is still owed: %+v", loaded.Open)
	}
	// The candidate identity survives, so the next invocation continues the
	// same one rather than starting over.
	if loaded.Worktree == "" || loaded.BaseSHA == "" {
		t.Fatalf("the preserved candidate lost its identity: %+v", loaded)
	}
}

// helpers ------------------------------------------------------------------

func drainEvents(ch <-chan event.Event) []event.Event {
	var out []event.Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
			continue
		default:
		}
		return out
	}
}

func kinds(events []event.Event) []event.Kind {
	out := make([]event.Kind, 0, len(events))
	for _, e := range events {
		out = append(out, e.Kind)
	}
	return out
}

func summaries(events []event.Event) []string {
	out := make([]string, 0, len(events))
	for _, e := range events {
		out = append(out, string(e.Kind)+" "+e.Summary)
	}
	return out
}

func contains(events []event.Event, kind event.Kind) bool {
	for _, e := range events {
		if e.Kind == kind {
			return true
		}
	}
	return false
}

// withPlan prefixes the created/planned events a task needs to be resumable at
// all, so these tests exercise the terminal rather than the preconditions.
func withPlan(events []event.Event, taskID, plan string) []event.Event {
	out := []event.Event{
		event.New("s", taskID, event.SourceUser, event.TaskCreated, "do the thing", nil),
		event.New("s", taskID, event.SourceArchitect, event.PlanProposed, plan, nil),
	}
	return append(out, events...)
}

// The other end of the continuity law: what FindInterrupted reconstructs is
// what the worker is actually told.
//
// The two halves were separately correct once and still broke the law between
// them -- the reconstruction carried the verdict's summary, so the resumed
// worker was handed "proof incomplete" with no file to open. This drives the
// real chain: a persistent event log, the real reconstruction, and the prompt
// the implementor process received on stdin.
func TestTheReconstructedInstructionReachesTheResumedWorkersPrompt(t *testing.T) {
	verdict := roles.ReviewVerdict{
		Provenance: roles.Provenance{TaskID: "task-1", Role: roles.Reviewer, Provider: "codex"},
		Decision:   roles.Revise,
		Summary:    "proof incomplete",
		Findings: []roles.Finding{{
			ID: "f1", Severity: roles.Blocking,
			Claim:      "mutation witness absent",
			Reference:  "internal/workflow/reviewgate_test.go",
			Reason:     "the test passes against the unrepaired code",
			Correction: "add a fail-then-pass mutation control",
		}},
		Instructions: "preserve the existing exact-candidate binding",
	}
	payload, err := json.Marshal(verdict)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	// The session as a later process finds it.
	events := []event.Event{
		event.New("s", "task-1", event.SourceUser, event.TaskCreated, "do the thing", nil),
		event.New("s", "task-1", event.SourceArchitect, event.PlanProposed, "the bounded plan", nil),
		event.New("s", "task-1", event.SourceReviewer, event.WorkflowAwaitingReview, "owes a review", nil),
		{SessionID: "s", TaskID: "task-1", Source: event.SourceReviewer,
			Kind: event.ReviewCompleted, Summary: "REVISE: proof incomplete", Payload: payload},
		event.New("s", "task-1", event.SourceUser, event.WorkflowStopped, "stopped", nil),
	}
	found := session.FindInterrupted(events)
	if len(found) != 1 {
		t.Fatalf("the task is not resumable: %d found", len(found))
	}
	task := found[0]
	if task.AwaitingReview {
		t.Fatal("a revised task still claims to await an independent review")
	}

	// Resume hands that reconstruction to the worker as carried context. Run
	// the loop with it and read what the implementor process actually got.
	h := newGateHarness(t, requiresIndependentReview(), roles.Fresh, "accept")
	if _, _, _, _, err := h.engine.runCandidate(context.Background(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, task.Review); err != nil {
		t.Fatalf("the resumed loop failed: %v", err)
	}

	prompt, err := os.ReadFile(h.workerSaw)
	if err != nil {
		t.Fatalf("the worker recorded no prompt: %v", err)
	}
	for _, must := range []string{
		"mutation witness absent",
		"internal/workflow/reviewgate_test.go",
		"add a fail-then-pass mutation control",
		"preserve the existing exact-candidate binding",
	} {
		if !strings.Contains(string(prompt), must) {
			t.Fatalf("the resumed worker was never told %q; it received:\n%s", must, prompt)
		}
	}
}

package workflow

import (
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/authority"
)

func TestDecodeModelJSON(t *testing.T) {
	var got architectureDecision
	err := decodeModelJSON("```json\n{\"decision\":\"proceed\",\"summary\":\"ok\",\"plan\":\"edit x\"}\n```", &got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != "proceed" || got.Plan != "edit x" {
		t.Fatalf("unexpected decision: %#v", got)
	}
}

func TestDecodeModelJSONRejectsProse(t *testing.T) {
	var got architectureDecision
	if err := decodeModelJSON("no bounded response", &got); err == nil {
		t.Fatal("expected malformed model response to fail closed")
	}
}

func TestArchitectureOptionsRoundTrip(t *testing.T) {
	in := `{"decision":"escalate","summary":"policy","human_question":"choose","recommendation":"1","options":[{"id":"1","label":"preserve"},{"id":"2","label":"change"}]}`
	var got architectureDecision
	if err := decodeModelJSON(in, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Options) != 2 || got.Options[0] != (authority.Option{ID: "1", Label: "preserve"}) {
		t.Fatalf("unexpected options: %#v", got.Options)
	}
}

func TestIsStopOption(t *testing.T) {
	for _, label := range []string{"Stop this task", "Cancel", "Abort operation"} {
		if !isStopOption(label) {
			t.Fatalf("expected %q to stop", label)
		}
	}
	if isStopOption("Preserve the contract") {
		t.Fatal("normal authority option must not stop the task")
	}
}

func testContext() taskContext {
	return taskContext{
		Task:            "add a --version flag",
		Conversation:    "HUMAN: can we version this?\nYOU: yes, a conventional flag",
		WorkspaceStatus: "composition_state: complete",
		Preflight:       "risk_class: UNKNOWN_IMPACT",
		Rationale:       "conventional flag, no governance change",
		Steps:           []string{"locate the CLI construction", "print and exit"},
	}
}

func TestImplementationPromptCarriesContextWithoutWideningScope(t *testing.T) {
	got := implementationPrompt(testContext(), "edit main.go", "", 1, nil)
	for _, want := range []string{
		"can we version this?",             // the conversation
		"conventional flag, no governance", // the architect's reasoning
		"1. locate the CLI construction",   // the plan steps
		"composition_state: complete",      // Sensei evidence
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("implementation prompt is missing %q", want)
		}
	}
	// Context explains why; it must never read as permission to do more.
	if !strings.Contains(got, "They do NOT widen your scope") {
		t.Fatal("implementation prompt dropped the scope boundary")
	}
}

func TestReviewPromptCarriesContextWithoutLoweringTheBar(t *testing.T) {
	got := reviewPrompt(testContext(), "edit main.go", "diff --git a b", "audit says fine")
	for _, want := range []string{"can we version this?", "conventional flag, no governance", "audit says fine"} {
		if !strings.Contains(got, want) {
			t.Fatalf("review prompt is missing %q", want)
		}
	}
	if !strings.Contains(got, "Context does not\nlower the bar") {
		t.Fatal("review prompt dropped the standard-of-proof guard")
	}
}

func TestTaskContextIntentIsExplicitWhenEmpty(t *testing.T) {
	if got := (taskContext{}).intent(); !strings.Contains(got, "no additional rationale") {
		t.Fatalf("empty intent = %q, want an explicit statement of absence", got)
	}
}

func TestGuidanceReachesTheWorkerWithoutEnlargingThePlan(t *testing.T) {
	got := implementationPrompt(testContext(), "edit main.go", "", 2,
		[]string{"use the existing version constant, do not add a package"})
	if !strings.Contains(got, "use the existing version constant") {
		t.Fatal("the human's guidance did not reach the worker")
	}
	if !strings.Contains(got, "takes precedence over your own") {
		t.Fatal("guidance must outrank the worker's own judgement about how to implement")
	}
	if !strings.Contains(got, "does not silently enlarge the") {
		t.Fatal("guidance must not become a way to grow the approved plan unnoticed")
	}
}

func TestNoteQueuesOnlyForARealTask(t *testing.T) {
	e := &Engine{}
	if e.Note("", "hello") {
		t.Fatal("guidance was accepted for no task, so nothing would ever read it")
	}
	if e.Note("task-1", "   ") {
		t.Fatal("empty guidance was accepted")
	}
	if !e.Note("task-1", "prefer the simpler shape") {
		t.Fatal("guidance for a running task was refused")
	}
	if got := e.takeNotes("task-1"); len(got) != 1 || got[0] != "prefer the simpler shape" {
		t.Fatalf("takeNotes = %v, want the queued guidance", got)
	}
	if got := e.takeNotes("task-1"); len(got) != 0 {
		t.Fatalf("guidance was delivered twice: %v", got)
	}
}

func TestHandoverTellsTheNextWorkerWhatWasLeftBehind(t *testing.T) {
	note := handoverNote("claude", "REVISE: counts are presented as exact", errors.New("did not converge after 3 review cycles"))
	for _, want := range []string{
		"did not converge",     // why it stopped
		"already present here", // the work is not gone
		"Continue from them rather than starting over",
		"counts are presented as exact", // the unresolved finding
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("handover note is missing %q:\n%s", want, note)
		}
	}
}

func TestHandoverEntersTheNextWorkerAsUnansweredFeedback(t *testing.T) {
	got := implementationPrompt(testContext(), "plan", "the previous worker left this unresolved", 1, nil)
	if !strings.Contains(got, "the previous worker left this unresolved") {
		t.Fatal("a handover did not reach the next worker's first cycle")
	}
}

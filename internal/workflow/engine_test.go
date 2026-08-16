package workflow

import (
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
	got := implementationPrompt(testContext(), "edit main.go", "", 1)
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

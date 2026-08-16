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

func TestArchitectConversationPromptIsHumanFacing(t *testing.T) {
	got := architectConversationPrompt("Should this boundary move?", "workspace evidence", "preflight evidence")
	for _, want := range []string{
		"speaking directly with the human owner",
		"precise, concrete, and technically rich",
		"LIVE SENSEI WORKSPACE AUTHORITY",
		"/run",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation prompt missing %q", want)
		}
	}
	if strings.Contains(got, "Return ONLY JSON") {
		t.Fatal("human-facing architect conversation must not be compressed into the machine JSON contract")
	}
}

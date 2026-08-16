package provider

import "testing"

func TestChatGPTArchitectModelIsPinned(t *testing.T) {
	if ChatGPTArchitectModel != "gpt-5.6-sol" {
		t.Fatalf("unexpected architect model %q", ChatGPTArchitectModel)
	}
	if ChatGPTArchitectEffort != "high" {
		t.Fatalf("unexpected architect reasoning effort %q", ChatGPTArchitectEffort)
	}
}

func TestSelectEffortUsesRequestedWhenCatalogIsLegacy(t *testing.T) {
	model := appServerModel{DefaultReasoningEffort: "low"}
	if got := selectEffort(model, "high"); got != "high" {
		t.Fatalf("selectEffort()=%q want high", got)
	}
}

func TestSelectEffortFallsBackToCatalogDefault(t *testing.T) {
	model := appServerModel{DefaultReasoningEffort: "medium"}
	model.SupportedEfforts = append(model.SupportedEfforts,
		struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}{ReasoningEffort: "low"},
		struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}{ReasoningEffort: "medium"},
	)
	if got := selectEffort(model, "high"); got != "medium" {
		t.Fatalf("selectEffort()=%q want medium", got)
	}
}

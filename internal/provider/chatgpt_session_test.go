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

// TestSandboxModeUsesTheAppServerSpelling pins the hyphenated form.
//
// A unit test cannot prove the app-server accepts it — only the governed
// acceptance run can do that, and it is what found the bug. What this can do is
// stop someone from "tidying" it back into Go-style camelCase, which is exactly
// how it was written the first time and which silently disabled the ChatGPT
// architect on every turn.
func TestSandboxModeUsesTheAppServerSpelling(t *testing.T) {
	if sandboxReadOnly != "read-only" {
		t.Fatalf("sandbox mode is %q; the codex app-server accepts read-only, workspace-write and danger-full-access", sandboxReadOnly)
	}
}

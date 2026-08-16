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

// TestSandboxSpellingsAreNotUnified pins both spellings and the fact that they
// differ.
//
// A unit test cannot prove the app-server accepts either — only the governed
// acceptance run can, and it is what found both. What this can do is stop the
// next reader from "removing the duplication", which is exactly what happened
// once: unifying them fixed thread/start and broke turn/start, which had been
// right from the start.
func TestSandboxSpellingsAreNotUnified(t *testing.T) {
	if threadSandboxReadOnly != "read-only" {
		t.Errorf("thread/start sandbox is %q; the app-server accepts read-only, workspace-write, danger-full-access", threadSandboxReadOnly)
	}
	if turnSandboxReadOnly != "readOnly" {
		t.Errorf("turn/start sandboxPolicy.type is %q; the app-server accepts dangerFullAccess, readOnly, externalSandbox, workspaceWrite", turnSandboxReadOnly)
	}
	if threadSandboxReadOnly == turnSandboxReadOnly {
		t.Fatal("the two spellings have been unified; the app-server rejects that on one endpoint or the other")
	}
}

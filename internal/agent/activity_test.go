package agent

import (
	"strings"
	"testing"
)

func TestActivityDecodesToolUseIntoSomethingReadable(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"cmd/sensei-code/main.go","old_string":"a","new_string":"b"}}]}}`
	if got := Activity("claude", line); got != "Edit(cmd/sensei-code/main.go)" {
		t.Fatalf("Activity = %q, want the tool and the file it touched", got)
	}
}

func TestActivityKeepsAssistantProse(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"text","text":"I'll add the flag before repository discovery.\nThen test it."}]}}`
	got := Activity("claude", line)
	if got != "I'll add the flag before repository discovery." {
		t.Fatalf("Activity = %q, want the first line of prose", got)
	}
}

func TestActivityDropsProtocolNoise(t *testing.T) {
	// One task emitted over a thousand of these. None of them is worth a line.
	for _, line := range []string{
		`{"type":"system","subtype":"init","cwd":"/tmp"}`,
		`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed"}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"AGENTS.md"}]}}`,
		`{"type":"system","subtype":"thinking_tokens","estimated_tokens":350}`,
		`not json at all`,
	} {
		if got := Activity("claude", line); got != "" {
			t.Fatalf("Activity(%.40s) = %q, want it dropped", line, got)
		}
	}
}

func TestActivityPassesThroughPlainTextAgents(t *testing.T) {
	if got := Activity("codex", "  applying patch to main.go  "); got != "applying patch to main.go" {
		t.Fatalf("Activity = %q, want the trimmed line", got)
	}
}

func TestActivityPrefersCommandForShellTools(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`
	if got := Activity("claude", line); got != "Bash(go test ./...)" {
		t.Fatalf("Activity = %q, want the command", got)
	}
}

func TestActivityKeepsTheEndOfLongPaths(t *testing.T) {
	// The worktree prefix is identical on every line; the filename is the only
	// part that tells the architect anything.
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"/home/dave/Documents/github.com/globulario/.sensei-code-worktrees/task-1786848157854428112/claude/cmd/sensei-code/version.go"}}]}}`
	got := Activity("claude", line)
	if !strings.HasSuffix(got, "cmd/sensei-code/version.go)") {
		t.Fatalf("Activity = %q, want the filename to survive truncation", got)
	}
}

func TestActivityNamesToolsWithNoInterestingArgument(t *testing.T) {
	line := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"ToolSearch","input":{"max_results":5}}]}}`
	if got := Activity("claude", line); got != "ToolSearch" {
		t.Fatalf("Activity = %q, want the bare tool name rather than empty parentheses", got)
	}
}

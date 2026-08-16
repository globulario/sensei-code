package doctor

import (
	"encoding/json"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/provider"
)

func TestConfiguredCommandsAreUnique(t *testing.T) {
	cfg := config.Default()
	got := configuredCommands(cfg)
	seen := map[string]bool{}
	for _, command := range got {
		if seen[command] {
			t.Fatalf("duplicate command %q", command)
		}
		seen[command] = true
	}
	for _, want := range []string{"git", "awareness-mcp", "codex", "claude"} {
		if !seen[want] {
			t.Fatalf("missing command %q in %v", want, got)
		}
	}
}

func TestConfiguredProvidersAreUnique(t *testing.T) {
	cfg := config.Default()
	got := configuredProviders(cfg)
	if len(got) != 3 {
		t.Fatalf("configuredProviders(default)=%v want three providers", got)
	}
	seen := map[provider.ID]bool{}
	for _, id := range got {
		seen[id] = true
	}
	if !seen[provider.ChatGPT] || !seen[provider.Codex] || !seen[provider.Claude] {
		t.Fatalf("configuredProviders(default)=%v want chatgpt+codex+claude", got)
	}
}

func TestWarningsDoNotMasqueradeAsPassButDoNotBlockReadiness(t *testing.T) {
	report := Report{Checks: []Check{{Name: "provider:antigravity", Status: Warn, Detail: "auth state unknown"}}}
	if !report.OK() {
		t.Fatal("warning-only doctor report should remain usable")
	}
	if report.Checks[0].Status == Pass {
		t.Fatal("unknown provider auth must not be represented as PASS")
	}
	report.Checks = append(report.Checks, Check{Name: "provider:codex", Status: Fail})
	if report.OK() {
		t.Fatal("FAIL should block doctor readiness")
	}
}

func TestToolNames(t *testing.T) {
	names, err := toolNames(json.RawMessage(`{"tools":[{"name":"awareness_preflight"},{"name":"sensei_workspace_status"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "awareness_preflight" {
		t.Fatalf("unexpected names %v", names)
	}
}

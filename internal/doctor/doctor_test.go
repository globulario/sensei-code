package doctor

import (
	"encoding/json"
	"strings"
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
	seen := map[provider.ID]bool{}
	for _, id := range got {
		if seen[id] {
			t.Fatalf("configuredProviders(default)=%v repeats %q", got, id)
		}
		seen[id] = true
	}
	// The architect authenticates as ChatGPT through the codex CLI, and the two
	// bounded workers are Claude and Codex, so all three are configured login
	// surfaces that doctor must report on.
	for _, want := range []provider.ID{provider.ChatGPT, provider.Codex, provider.Claude} {
		if !seen[want] {
			t.Fatalf("configuredProviders(default)=%v is missing %q", got, want)
		}
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

func TestMissingWorkspaceToolsNameTheirRemedy(t *testing.T) {
	// Released sensei packages lag these tools. A check that only says
	// "missing" makes every reader rediscover that on their own.
	got := remediation("sensei_workspace_status")
	if !strings.Contains(got, "build awareness-mcp") {
		t.Fatalf("remediation = %q, want the action that fixes it", got)
	}
	if remediation("awareness_preflight") == "" {
		t.Fatal("every missing tool should say what to check")
	}
}

package doctor

import (
	"encoding/json"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
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

func TestToolNames(t *testing.T) {
	names, err := toolNames(json.RawMessage(`{"tools":[{"name":"awareness_preflight"},{"name":"sensei_workspace_status"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "awareness_preflight" {
		t.Fatalf("unexpected names %v", names)
	}
}

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultAuthorityEnvelope(t *testing.T) {
	c := Default()
	if c.Architect.Name != "chatgpt" || c.Architect.Command != "codex" {
		t.Fatalf("first-version architect must be ChatGPT over codex app-server, got %#v", c.Architect)
	}
	if len(c.Architect.Args) != 0 {
		t.Fatalf("ChatGPT architect must not be configured as one-shot codex exec: %#v", c.Architect.Args)
	}
	if len(c.Implementors) < 2 {
		t.Fatalf("expected primary and fallback implementors, got %d", len(c.Implementors))
	}
	if !c.Permissions.WriteCandidates || !c.Permissions.CreateWorktrees || !c.Permissions.RunTests {
		t.Fatal("normal candidate execution must be locally autonomous")
	}
	if c.Permissions.Push || c.Permissions.ForcePush || c.Permissions.ProductionDeploy {
		t.Fatal("external/destructive authority must not be granted by default")
	}
}

func TestLoadMigratesOnlyLegacyBuiltInArchitect(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Dir(Path(repo)), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := Default()
	legacy.Architect = Agent{Name: "codex", Command: "codex", Args: []string{"exec", "--sandbox", "read-only", "-"}}
	b, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(repo), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got.Architect.Name != "chatgpt" || len(got.Architect.Args) != 0 {
		t.Fatalf("legacy built-in architect was not migrated: %#v", got.Architect)
	}

	custom := legacy
	custom.Architect.Args = []string{"exec", "--sandbox", "read-only", "--custom", "-"}
	b, err = json.Marshal(custom)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(repo), b, 0o600); err != nil {
		t.Fatal(err)
	}
	got, err = Load(repo)
	if err != nil {
		t.Fatal(err)
	}
	if got.Architect.Name != "codex" || len(got.Architect.Args) != len(custom.Architect.Args) {
		t.Fatalf("custom architect configuration must remain user-owned: %#v", got.Architect)
	}
}

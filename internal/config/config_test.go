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

func TestClaudeWorkerRequestsVerboseWithStreamJSON(t *testing.T) {
	// claude refuses --output-format=stream-json under --print without
	// --verbose, and exits 1 before doing any work.
	for _, agent := range Default().Implementors {
		if agent.Name != "claude" {
			continue
		}
		var stream, verbose bool
		for _, arg := range agent.Args {
			switch arg {
			case "stream-json":
				stream = true
			case "--verbose":
				verbose = true
			}
		}
		if stream && !verbose {
			t.Fatal("claude worker asks for stream-json without --verbose, which it refuses")
		}
		return
	}
	t.Fatal("no claude implementor in the default configuration")
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

// TestBuildCheckWorksInsideACandidateWorktree pins a default that a real run
// proved wrong.
//
// A candidate is a git worktree, and `go build` stamps VCS metadata from it.
// That fails with "error obtaining VCS status: exit status 128" for reasons
// unrelated to whether the code compiles, so the build check reported a failure
// no worker could fix and the review loop could not converge. Stamping is
// irrelevant to the question the check asks.
func TestBuildCheckWorksInsideACandidateWorktree(t *testing.T) {
	var found bool
	for _, c := range Default().Validation.Build {
		if c.Command != "go" {
			continue
		}
		found = true
		var disabled bool
		for _, a := range c.Args {
			if a == "-buildvcs=false" {
				disabled = true
			}
		}
		if !disabled {
			t.Fatalf("the default build check is %v; without -buildvcs=false it fails inside a candidate worktree", c.Args)
		}
	}
	if !found {
		t.Fatal("no default go build check")
	}
}

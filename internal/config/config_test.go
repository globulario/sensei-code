package config

import "testing"

func TestDefaultAuthorityEnvelope(t *testing.T) {
	c := Default()
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

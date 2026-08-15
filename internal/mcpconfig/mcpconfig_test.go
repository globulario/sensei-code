package mcpconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCodexServerCommandIgnoresNestedToolTables(t *testing.T) {
	toml := `
[mcp_servers.globular]
url = "https://example.invalid/mcp"

[mcp_servers.sensei]
command = "/usr/local/bin/awareness-mcp"
args = ["--awareness-addr", "localhost:10120"]

[mcp_servers.sensei.tools.awareness_metadata]
enabled = true
command = "not-the-server"
`
	got, found := codexServerCommand(toml)
	if !found {
		t.Fatal("sensei server was not found")
	}
	if got != "/usr/local/bin/awareness-mcp" {
		t.Fatalf("command = %q, want the server's own command, not a nested tool's", got)
	}
}

func TestCodexServerCommandReportsAbsence(t *testing.T) {
	if _, found := codexServerCommand("[mcp_servers.other]\ncommand = \"x\"\n"); found {
		t.Fatal("reported a sensei server that is not configured")
	}
}

func TestClaudeMissingWhenServerAbsent(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := describeClaude(dir).State; got != Missing {
		t.Fatalf("state = %q, want %q", got, Missing)
	}
}

func TestConfigureClaudeAddsServerAndPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"keep-me"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Configure(dir, Claude, "awareness-mcp", []string{"--awareness-addr", "localhost:10120"})
	if err != nil {
		t.Fatal(err)
	}
	if st.State != Configured {
		t.Fatalf("state = %q, want %q (%s)", st.State, Configured, st.Detail)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), "keep-me") {
		t.Fatalf("configure dropped an unrelated server: %s", b)
	}
}

func TestConfigureLeavesAnExistingEntryAlone(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	original := `{"mcpServers":{"sensei":{"command":"deliberate-choice"}}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Configure(dir, Claude, "awareness-mcp", nil); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(string(b), "deliberate-choice") {
		t.Fatalf("configure overwrote the user's own entry: %s", b)
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

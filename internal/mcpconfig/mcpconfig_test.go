package mcpconfig

import (
	"os"
	"path/filepath"
	"strings"
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
	if got := describeClaude(dir, nil, true).State; got != Missing {
		t.Fatalf("state = %q, want %q", got, Missing)
	}
}

func TestConfigureClaudeAddsServerAndPreservesOthers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".mcp.json")
	if err := os.WriteFile(path, []byte(`{"mcpServers":{"other":{"command":"keep-me"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Configure(dir, Claude, "awareness-mcp", []string{"--awareness-addr", "localhost:10120"}, true)
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
	if _, err := Configure(dir, Claude, "awareness-mcp", nil, true); err != nil {
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

func TestMissingCodexToolsFindsBlockedEvidenceTools(t *testing.T) {
	// Only one tool allowlisted: Codex cancels every other Sensei call, so the
	// server must not be reported as usable.
	toml := "[mcp_servers.sensei]\ncommand = \"awareness-mcp\"\n\n[mcp_servers.sensei.tools.awareness_metadata]\napproval_mode = \"approve\"\n"
	missing := missingCodexTools(toml)
	if len(missing) != len(ReadOnlyTools)-1 {
		t.Fatalf("missing = %v, want every read-only tool except awareness_metadata", missing)
	}
	for _, tool := range missing {
		if tool == "awareness_metadata" {
			t.Fatal("reported an allowlisted tool as missing")
		}
	}
}

func TestMissingCodexToolsEmptyWhenAllAllowlisted(t *testing.T) {
	toml := "[mcp_servers.sensei]\ncommand = \"awareness-mcp\"\n"
	for _, tool := range ReadOnlyTools {
		toml += "\n[mcp_servers.sensei.tools." + tool + "]\napproval_mode = \"approve\"\n"
	}
	if missing := missingCodexTools(toml); len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
}

// An entry that exists is not an entry that works. The endpoint moved twice
// while custody was corrected, the entries kept the first address, and nothing
// reported it: describe asked only whether a command was present, so doctor
// said configured while every awareness call an agent made failed with a
// transport error.
func TestAnEntryPointingElsewhereIsStaleRatherThanConfigured(t *testing.T) {
	dir := t.TempDir()
	write := func(addr string) {
		body := `{"mcpServers":{"sensei":{"command":"awareness-mcp","args":["--awareness-addr","` + addr + `"]}}}`
		if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want := []string{"--awareness-addr", "localhost:10122"}

	write("localhost:10122")
	if got := describeClaude(dir, want, true).State; got != Configured {
		t.Fatalf("an entry pointing at the certified endpoint = %q, want configured", got)
	}

	write("localhost:10120")
	got := describeClaude(dir, want, true)
	if got.State != Stale {
		t.Fatalf("an entry pointing elsewhere = %q, want stale", got.State)
	}
	for _, want := range []string{"localhost:10120", "localhost:10122", "different graph"} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("the detail should name both endpoints and the consequence, got %q", got.Detail)
		}
	}
}

// An entry that states no address inherits the binary's default, which may well
// be right. Reporting every hand-written entry as broken would make the state
// useless.
func TestAnEntryWithNoStatedAddressIsNotStale(t *testing.T) {
	dir := t.TempDir()
	body := `{"mcpServers":{"sensei":{"command":"awareness-mcp"}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := describeClaude(dir, []string{"--awareness-addr", "localhost:10122"}, true).State; got != Configured {
		t.Fatalf("an entry stating no address = %q, want configured", got)
	}
	// And a caller that states no address of its own cannot judge drift.
	if got := describeClaude(dir, nil, true).State; got != Configured {
		t.Fatalf("with nothing to compare against = %q, want configured", got)
	}
}

// Reconciliation is narrow on purpose: the address was not a deliberate choice,
// and everything else in the entry was.
func TestReconcilingTheAddressLeavesTheRestOfTheEntryAlone(t *testing.T) {
	dir := t.TempDir()
	body := `{"mcpServers":{"sensei":{"command":"/custom/path/awareness-mcp","args":["--awareness-addr","localhost:10120","--verbose"]}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	want := []string{"--awareness-addr", "localhost:10122"}
	if _, err := Configure(dir, Claude, "awareness-mcp", want, true); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(b)
	for _, keep := range []string{"/custom/path/awareness-mcp", "--verbose", "localhost:10122"} {
		if !strings.Contains(got, keep) {
			t.Fatalf("reconciliation lost %q: %s", keep, got)
		}
	}
	if strings.Contains(got, "localhost:10120") {
		t.Fatalf("the stale address survived: %s", got)
	}
	if got := describeClaude(dir, want, true).State; got != Configured {
		t.Fatalf("after reconciliation = %q, want configured", got)
	}
}

// The address is swapped in place rather than appended, so an entry does not
// accumulate a flag every time the endpoint moves.
func TestReplacingAnAddressDoesNotAccumulateFlags(t *testing.T) {
	got := replaceAddress([]string{"--awareness-addr", "a:1", "--verbose"}, "b:2")
	if len(got) != 3 || got[1] != "b:2" || got[2] != "--verbose" {
		t.Fatalf("replaceAddress = %v", got)
	}
	if got := replaceAddress([]string{"--verbose"}, "b:2"); len(got) != 3 {
		t.Fatalf("an entry with no address should gain one exactly once: %v", got)
	}
	if got := replaceAddress([]string{"--awareness-addr=a:1"}, "b:2"); got[0] != "--awareness-addr=b:2" {
		t.Fatalf("the joined form was not rewritten: %v", got)
	}
}

// A built-in default is not evidence about the repository.
//
// This is the defect that made doctor dangerous: it reported an entry pointing
// at the correct, authoritative graph as stale, purely because that address
// disagreed with a hardcoded fallback, and prescribed a repair that repointed
// both agents at a two-day-old graph serving another repository's domain. The
// operator followed the prescription and the system became more wrong.
func TestADefaultAddressIsNotEvidenceThatAnEntryIsStale(t *testing.T) {
	dir := t.TempDir()
	writeClaudeConfig(t, dir, "localhost:10122")

	unstated := describeClaude(dir, []string{"--awareness-addr", "localhost:10120"}, false)
	if unstated.State == Stale {
		t.Fatal("an entry was called stale on the authority of a built-in default")
	}
	if unstated.State != Unproven {
		t.Fatalf("state=%q, want unproven: two addresses differ and nothing has established which is right", unstated.State)
	}
	for _, want := range []string{"localhost:10122", "localhost:10120", "built-in default", "reported, not repaired"} {
		if !strings.Contains(unstated.Detail, want) {
			t.Fatalf("the finding does not say %q: %s", want, unstated.Detail)
		}
	}
	if strings.Contains(unstated.Detail, "certifies") {
		t.Fatal("the finding still claims the repository certified something it never stated")
	}

	// The same divergence, once the repository has actually stated an address,
	// is real drift and is reported as such.
	stated := describeClaude(dir, []string{"--awareness-addr", "localhost:10120"}, true)
	if stated.State != Stale {
		t.Fatalf("state=%q, want stale once the repository states its endpoint", stated.State)
	}
	if strings.Contains(stated.Detail, "certifies") {
		t.Fatal("a configured endpoint is still being described as certified")
	}
}

// Refusing is the repair. Rewriting a working entry to match a fallback is how
// a correct configuration gets broken.
func TestConfigureRefusesToRewriteAnEntryOnTheAuthorityOfADefault(t *testing.T) {
	dir := t.TempDir()
	writeClaudeConfig(t, dir, "localhost:10122")
	before, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	status, refusal := Configure(dir, Claude, "awareness-mcp", []string{"--awareness-addr", "localhost:10120"}, false)
	if refusal == nil {
		t.Fatal("Configure rewrote an entry although nothing established which graph is authoritative")
	}
	if status.State != Unproven {
		t.Fatalf("state=%q, want unproven", status.State)
	}
	after, err := os.ReadFile(filepath.Join(dir, ".mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("the entry was modified despite the refusal")
	}
	if !strings.Contains(refusal.Error(), ".sensei-code/config.json") {
		t.Fatalf("the refusal does not say how to make it checkable: %v", refusal)
	}
}

// An agreeing entry is configured whatever the provenance: there is no
// disagreement to adjudicate.
func TestAnAgreeingEntryIsConfiguredRegardlessOfProvenance(t *testing.T) {
	dir := t.TempDir()
	writeClaudeConfig(t, dir, "localhost:10120")
	for _, stated := range []bool{true, false} {
		if got := describeClaude(dir, []string{"--awareness-addr", "localhost:10120"}, stated); got.State != Configured {
			t.Fatalf("stated=%v: state=%q, want configured", stated, got.State)
		}
	}
}

func writeClaudeConfig(t *testing.T, dir, addr string) {
	t.Helper()
	body := `{"mcpServers":{"sensei":{"command":"awareness-mcp","args":["--awareness-addr","` + addr + `"]}}}`
	if err := os.WriteFile(filepath.Join(dir, ".mcp.json"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

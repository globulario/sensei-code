package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func privateDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "exec")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func execRequest(workspace, dir string) Request {
	return Request{TaskID: "task-1", Workspace: workspace,
		Graph: &GraphBinding{Command: "awareness-mcp", Args: []string{"--awareness-addr", "localhost:10120"}},
		Exec: &ExecBinding{Command: "/usr/local/bin/sensei-code", Args: []string{"exec-shim", "--socket", dir + "/s", "--service", "abc"},
			Dir: dir, Settings: []byte(`{"enabledPlugins":{"cc-plugin-agents-md":false}}`)}}
}

func argAfter(args []string, flag string) (string, bool) {
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// Under the execution profile the client configuration is written into the
// invocation's private directory, never the worktree, and claude is launched
// with that configuration strictly, no built-in tools and the trusted
// settings.
func TestTheExecutionBindingIsWrittenOutsideTheWorkspace(t *testing.T) {
	workspace, dir := t.TempDir(), privateDir(t)
	c := CLI{Name: "claude", Command: "claude"}
	args, err := c.bindGraphArgs(execRequest(workspace, dir), []string{"-p"})
	if err != nil {
		t.Fatal(err)
	}
	config, ok := argAfter(args, "--mcp-config")
	if !ok || filepath.Dir(config) != dir {
		t.Fatalf("the MCP configuration is not in the private directory: %v", args)
	}
	if tools, ok := argAfter(args, "--tools"); !ok || tools != "" {
		t.Fatalf("built-in tools are not removed: %v", args)
	}
	settings, ok := argAfter(args, "--settings")
	if !ok || filepath.Dir(settings) != dir || !strings.Contains(strings.Join(args, " "), "--strict-mcp-config") {
		t.Fatalf("not strict, or the settings are not the trusted ones: %v", args)
	}
	if _, err := os.Stat(filepath.Join(workspace, ".sensei-code")); !os.IsNotExist(err) {
		t.Fatal("client configuration was written into the workspace")
	}
	body, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	var cfg struct {
		MCPServers map[string]struct {
			Command string   `json:"command"`
			Args    []string `json:"args"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatal(err)
	}
	exec := cfg.MCPServers[ExecServerName]
	if len(cfg.MCPServers) != 2 || exec.Command != "/usr/local/bin/sensei-code" || strings.Join(exec.Args, " ") != "exec-shim --socket "+dir+"/s --service abc" ||
		cfg.MCPServers["sensei"].Command != "awareness-mcp" {
		t.Fatalf("the configuration is not exactly the service and the bound graph: %s", body)
	}
	for _, p := range []string{config, settings} {
		if info, err := os.Stat(p); err != nil || info.Mode().Perm() != 0o600 {
			t.Fatalf("%s is not 0600: %v", p, err)
		}
	}
	if got, _ := os.ReadFile(settings); !strings.Contains(string(got), `"cc-plugin-agents-md":false`) {
		t.Fatalf("the settings are not the trusted ones: %s", got)
	}
}

// F1. The bound client loads no settings source but the trusted file -- the
// worktree's .claude settings and their hooks would run on the host -- and
// the trusted file disables every hook whatever the configuration says. Both
// walls are required: each assertion fails if its protection is removed.
func TestTheExecutionBindingLoadsNoCandidateSettingsAndNoHooks(t *testing.T) {
	workspace, dir := t.TempDir(), privateDir(t)
	c := CLI{Name: "claude", Command: "claude"}
	req := execRequest(workspace, dir)
	req.Exec.Settings = []byte(`{"disableAllHooks":false,"enabledPlugins":{"cc-plugin-agents-md":false}}`)
	args, err := c.bindGraphArgs(req, []string{"-p"})
	if err != nil {
		t.Fatal(err)
	}
	if sources, ok := argAfter(args, "--setting-sources"); !ok || sources != "" {
		t.Fatalf("setting sources are not restricted to none: %v", args)
	}
	settings, _ := argAfter(args, "--settings")
	body, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	plugins, _ := got["enabledPlugins"].(map[string]any)
	if got["disableAllHooks"] != true || plugins["cc-plugin-agents-md"] != false {
		t.Fatalf("the trusted settings do not disable hooks or lost the configured settings: %s", body)
	}
	// With no configured settings, hooks are still disabled.
	req.Exec.Settings = nil
	args, err = c.bindGraphArgs(req, nil)
	if err != nil {
		t.Fatal(err)
	}
	settings, _ = argAfter(args, "--settings")
	if body, _ := os.ReadFile(settings); string(body) != `{"disableAllHooks":true}` {
		t.Fatalf("the default trusted settings do not disable hooks: %s", body)
	}
}

// F1. Configured settings that cannot be merged, or a trusted settings file
// that cannot be written, refuse the launch rather than run without them.
func TestTheExecutionBindingRefusesWithoutTrustedSettings(t *testing.T) {
	workspace := t.TempDir()
	c := CLI{Name: "claude", Command: "claude"}
	for _, bad := range []string{`[]`, `null`, `"x"`, `{"disableAllHooks":`} {
		req := execRequest(workspace, privateDir(t))
		req.Exec.Settings = []byte(bad)
		if _, err := c.bindGraphArgs(req, nil); err == nil {
			t.Errorf("settings %s were accepted", bad)
		}
	}
	dir := privateDir(t)
	if err := os.Mkdir(filepath.Join(dir, "settings-task-1.json"), 0o700); err != nil {
		t.Fatal(err)
	}
	if args, err := c.bindGraphArgs(execRequest(workspace, dir), nil); err == nil {
		t.Fatalf("launched without its trusted settings: %v", args)
	}
}

// A configuration directory the candidate could reach, or one that is not
// private, is refused; so is a provider that cannot drop its own tools.
func TestTheExecutionBindingRefusesWhatTheCandidateCouldReach(t *testing.T) {
	workspace := t.TempDir()
	inside := filepath.Join(workspace, ".sensei-code")
	if err := os.Mkdir(inside, 0o700); err != nil {
		t.Fatal(err)
	}
	shared := filepath.Join(t.TempDir(), "shared")
	if err := os.Mkdir(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	claude := CLI{Name: "claude", Command: "claude"}
	for name, dir := range map[string]string{"inside the workspace": inside, "not private": shared, "relative": "exec", "absent": filepath.Join(t.TempDir(), "gone")} {
		if _, err := claude.bindGraphArgs(execRequest(workspace, dir), nil); err == nil {
			t.Errorf("%s: bound", name)
		}
	}
	dir := privateDir(t)
	for _, c := range []CLI{{Name: "codex", Command: "codex"}, {Name: "stub", Command: "stub", NoGraph: true}} {
		if _, err := c.bindGraphArgs(execRequest(workspace, dir), nil); err == nil {
			t.Errorf("%s was bound to the execution service with its own tools", c.Name)
		}
	}
	noCommand := execRequest(workspace, dir)
	noCommand.Exec.Command = ""
	if _, err := claude.bindGraphArgs(noCommand, nil); err == nil {
		t.Error("a binding with no server command was accepted")
	}
}

// Without an execution binding the launch is exactly what it was.
func TestWithoutAnExecutionBindingTheLaunchIsUnchanged(t *testing.T) {
	workspace := t.TempDir()
	req := execRequest(workspace, "")
	req.Exec = nil
	args, err := CLI{Name: "claude", Command: "claude"}.bindGraphArgs(req, []string{"-p"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-p", "--mcp-config", filepath.Join(workspace, ".sensei-code", "mcp-task-1.json"), "--strict-mcp-config"}
	if strings.Join(args, "\x00") != strings.Join(want, "\x00") {
		t.Fatalf("the default launch changed: %v", args)
	}
}

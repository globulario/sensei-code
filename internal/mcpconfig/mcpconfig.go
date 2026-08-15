// Package mcpconfig reports and repairs each agent's access to the Sensei MCP
// server. Sensei Code does not proxy Sensei on an agent's behalf: every agent
// reaches Sensei through its own native MCP configuration, so that what the
// agent sees is what Sensei actually said.
package mcpconfig

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ServerName is the MCP server key Sensei Code expects each agent to register.
const ServerName = "sensei"

// Agent identifies a configurable agent surface.
type Agent string

const (
	Claude      Agent = "claude"
	Codex       Agent = "codex"
	Antigravity Agent = "antigravity"
)

// Ordered is the display order used by the CLI and the TUI.
var Ordered = []Agent{Codex, Claude, Antigravity}

func Label(a Agent) string {
	switch a {
	case Codex:
		return "ChatGPT / Codex"
	case Claude:
		return "Claude Code"
	case Antigravity:
		return "Google Antigravity"
	}
	return string(a)
}

// State is what is known about one agent's Sensei wiring.
type State string

const (
	// Configured means the agent registers a Sensei MCP server.
	Configured State = "configured"
	// Missing means the config is writable but has no Sensei entry.
	Missing State = "missing"
	// Unknown means Sensei Code cannot determine the agent's MCP configuration.
	// It is never reported as configured: an unverified surface is not a
	// working one.
	Unknown State = "unknown"
)

// Status describes one agent's access to Sensei.
type Status struct {
	Agent   Agent  `json:"agent"`
	State   State  `json:"state"`
	Path    string `json:"path,omitempty"`
	Command string `json:"command,omitempty"`
	Detail  string `json:"detail,omitempty"`
}

// Describe reports every agent's Sensei access without changing anything.
func Describe(repoRoot string) []Status {
	out := make([]Status, 0, len(Ordered))
	for _, a := range Ordered {
		out = append(out, describe(repoRoot, a))
	}
	return out
}

func describe(repoRoot string, a Agent) Status {
	switch a {
	case Claude:
		return describeClaude(repoRoot)
	case Codex:
		return describeCodex()
	case Antigravity:
		return Status{
			Agent:  Antigravity,
			State:  Unknown,
			Detail: "Antigravity owns its own MCP configuration; Sensei Code cannot read or write it",
		}
	}
	return Status{Agent: a, State: Unknown, Detail: "unrecognised agent"}
}

// --- Claude Code: project .mcp.json ---

func claudePath(repoRoot string) string { return filepath.Join(repoRoot, ".mcp.json") }

type claudeFile struct {
	MCPServers map[string]claudeServer `json:"mcpServers"`
}

type claudeServer struct {
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

func describeClaude(repoRoot string) Status {
	path := claudePath(repoRoot)
	st := Status{Agent: Claude, Path: path}
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		st.State = Missing
		st.Detail = "no .mcp.json in this repository"
		return st
	}
	if err != nil {
		st.State = Unknown
		st.Detail = err.Error()
		return st
	}
	var f claudeFile
	if err := json.Unmarshal(b, &f); err != nil {
		st.State = Unknown
		st.Detail = "unreadable .mcp.json: " + err.Error()
		return st
	}
	server, ok := f.MCPServers[ServerName]
	if !ok || strings.TrimSpace(server.Command) == "" {
		st.State = Missing
		st.Detail = "no " + ServerName + " server in .mcp.json"
		return st
	}
	st.State = Configured
	st.Command = server.Command
	st.Detail = resolvable(server.Command)
	return st
}

// --- Codex: ~/.codex/config.toml ---

func codexPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".codex", "config.toml"), nil
}

func describeCodex() Status {
	st := Status{Agent: Codex}
	path, err := codexPath()
	if err != nil {
		st.State = Unknown
		st.Detail = err.Error()
		return st
	}
	st.Path = path
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		st.State = Missing
		st.Detail = "no ~/.codex/config.toml"
		return st
	}
	if err != nil {
		st.State = Unknown
		st.Detail = err.Error()
		return st
	}
	command, found := codexServerCommand(string(b))
	if !found {
		st.State = Missing
		st.Detail = "no [mcp_servers." + ServerName + "] in config.toml"
		return st
	}
	st.State = Configured
	st.Command = command
	st.Detail = resolvable(command)
	return st
}

// codexServerCommand finds the command of the [mcp_servers.sensei] table. It
// reads only the table's own keys and stops at the next table header, so a
// nested [mcp_servers.sensei.tools.*] table is not mistaken for the server.
func codexServerCommand(content string) (string, bool) {
	header := "[mcp_servers." + ServerName + "]"
	lines := strings.Split(content, "\n")
	inTable := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			inTable = trimmed == header
			continue
		}
		if !inTable {
			continue
		}
		key, value, ok := strings.Cut(trimmed, "=")
		if !ok || strings.TrimSpace(key) != "command" {
			continue
		}
		return strings.Trim(strings.TrimSpace(value), `"`), true
	}
	// The table may exist with no command (for example a url-based server).
	return "", strings.Contains(content, header)
}

func resolvable(command string) string {
	if strings.TrimSpace(command) == "" {
		return "no command recorded"
	}
	if strings.ContainsRune(command, os.PathSeparator) {
		if _, err := os.Stat(command); err != nil {
			return "command does not exist: " + command
		}
		return command
	}
	path, err := exec.LookPath(command)
	if err != nil {
		return "command not found on PATH: " + command
	}
	return path
}

// Configure registers the Sensei MCP server for one agent. It only ever adds a
// missing entry: an entry that already exists is left exactly as the user wrote
// it, because silently rewriting an agent's own configuration would replace a
// deliberate choice with a guess.
func Configure(repoRoot string, a Agent, command string, args []string) (Status, error) {
	current := describe(repoRoot, a)
	switch current.State {
	case Configured:
		return current, nil
	case Unknown:
		return current, fmt.Errorf("%s: %s", Label(a), current.Detail)
	}
	var err error
	switch a {
	case Claude:
		err = configureClaude(repoRoot, command, args)
	case Codex:
		err = configureCodex(command, args)
	default:
		err = fmt.Errorf("%s cannot be configured by Sensei Code", Label(a))
	}
	if err != nil {
		return current, err
	}
	return describe(repoRoot, a), nil
}

func configureClaude(repoRoot, command string, args []string) error {
	path := claudePath(repoRoot)
	f := claudeFile{MCPServers: map[string]claudeServer{}}
	if b, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		if f.MCPServers == nil {
			f.MCPServers = map[string]claudeServer{}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	f.MCPServers[ServerName] = claudeServer{Command: command, Args: args}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o644)
}

func configureCodex(command string, args []string) error {
	path, err := codexPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	quoted := make([]string, 0, len(args))
	for _, a := range args {
		quoted = append(quoted, fmt.Sprintf("%q", a))
	}
	block := fmt.Sprintf("\n[mcp_servers.%s]\ncommand = %q\nargs = [%s]\n",
		ServerName, command, strings.Join(quoted, ", "))
	// Appended, never spliced: the rest of the user's TOML is untouched.
	body := strings.TrimRight(string(existing), "\n")
	if body != "" {
		body += "\n"
	}
	return os.WriteFile(path, []byte(body+block), 0o600)
}

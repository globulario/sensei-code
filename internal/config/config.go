package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/globulario/sensei-code/internal/behavioral"
)

type Permissions struct {
	ReadRepository   bool `json:"read_repository"`
	WriteCandidates  bool `json:"write_candidates"`
	CreateWorktrees  bool `json:"create_worktrees"`
	RunBuilds        bool `json:"run_builds"`
	RunTests         bool `json:"run_tests"`
	LocalCommit      bool `json:"local_commit"`
	Push             bool `json:"push"`
	ForcePush        bool `json:"force_push"`
	ProductionDeploy bool `json:"production_deploy"`
}

type Agent struct {
	Name    string   `json:"name"`
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type Workflow struct {
	ReviewCycles int `json:"review_cycles"`
	// PublishBase is the branch a pull request targets. Empty lets the host
	// choose its own default rather than Sensei Code guessing one.
	PublishBase string `json:"publish_base,omitempty"`
}

type Config struct {
	Sensei struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"sensei"`
	Architect    Agent             `json:"architect"`
	Implementors []Agent           `json:"implementors"`
	Reviewer     Agent             `json:"reviewer"`
	Workflow     Workflow          `json:"workflow"`
	Behavioral   behavioral.Config `json:"behavioral"`
	Permissions  Permissions       `json:"permissions"`
}

func Default() Config {
	var c Config
	c.Sensei.Command = "awareness-mcp"
	c.Sensei.Args = []string{"--awareness-addr", "localhost:10120"}
	// The architect is the agent the human talks to. It runs through the codex
	// CLI, which authenticates with a ChatGPT subscription, so it is named for the
	// account it speaks as rather than for the binary that carries it.
	c.Architect = Agent{Name: "chatgpt", Command: "codex", Args: []string{"exec", "--sandbox", "read-only", "-"}}
	c.Implementors = []Agent{
		// --verbose is required: claude refuses --output-format=stream-json
		// without it under --print, so the worker exited 1 before doing any work.
		{Name: "claude", Command: "claude", Args: []string{"-p", "--output-format", "stream-json", "--verbose", "--permission-mode", "bypassPermissions"}},
		{Name: "codex", Command: "codex", Args: []string{"exec", "--sandbox", "workspace-write", "-"}},
	}
	c.Reviewer = Agent{Name: "codex", Command: "codex", Args: []string{"exec", "--sandbox", "read-only", "-"}}
	c.Workflow = Workflow{ReviewCycles: 3}
	// Reporting is opt-in and unscoped by default: filing this repository's
	// outcomes against a guessed project or domain would corrupt somebody
	// else's principles.
	c.Behavioral = behavioral.Config{Enabled: false, Domain: "sensei_code"}
	c.Permissions = Permissions{
		ReadRepository:  true,
		WriteCandidates: true,
		CreateWorktrees: true,
		RunBuilds:       true,
		RunTests:        true,
		LocalCommit:     true,
	}
	return c
}

func Path(repo string) string { return filepath.Join(repo, ".sensei-code", "config.json") }

func Load(repo string) (Config, error) {
	p := Path(repo)
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return Default(), nil
	}
	if err != nil {
		return Config{}, err
	}
	c := Default()
	if err := json.Unmarshal(b, &c); err != nil {
		return Config{}, err
	}
	if c.Workflow.ReviewCycles < 1 {
		c.Workflow.ReviewCycles = 1
	}
	return c, nil
}

func Save(repo string, c Config) error {
	p := Path(repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(p, b, 0o600)
}

// DisplayName renders an agent identifier for humans. The identifier itself is
// load-bearing -- output normalization and event routing match on it -- so it is
// never replaced by this label.
func DisplayName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "chatgpt":
		return "ChatGPT"
	case "codex":
		return "Codex"
	case "claude":
		return "Claude"
	case "antigravity", "agy":
		return "Antigravity"
	case "grok":
		return "Grok"
	case "":
		return "agent"
	default:
		return name
	}
}

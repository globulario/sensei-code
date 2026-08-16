package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
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
}

type Config struct {
	Sensei struct {
		Command string   `json:"command"`
		Args    []string `json:"args"`
	} `json:"sensei"`
	Architect    Agent       `json:"architect"`
	Implementors []Agent     `json:"implementors"`
	Reviewer     Agent       `json:"reviewer"`
	Workflow     Workflow    `json:"workflow"`
	Permissions  Permissions `json:"permissions"`
}

func Default() Config {
	var c Config
	c.Sensei.Command = "awareness-mcp"
	c.Sensei.Args = []string{"--awareness-addr", "localhost:10120"}
	// ChatGPT is the first-version architectural authority. The codex binary is
	// retained only because its app-server is the authenticated ChatGPT
	// transport; agent.CLI does not invoke a one-shot codex exec for this role.
	c.Architect = Agent{Name: "chatgpt", Command: "codex"}
	c.Implementors = []Agent{
		{Name: "claude", Command: "claude", Args: []string{"-p", "--output-format", "stream-json", "--permission-mode", "bypassPermissions"}},
		{Name: "codex", Command: "codex", Args: []string{"exec", "--sandbox", "workspace-write", "-"}},
	}
	c.Reviewer = Agent{Name: "codex", Command: "codex", Args: []string{"exec", "--sandbox", "read-only", "-"}}
	c.Workflow = Workflow{ReviewCycles: 3}
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
	// Migrate only the exact old built-in architect. A deliberately customized
	// architect remains user-owned configuration and is never rewritten.
	if isLegacyDefaultArchitect(c.Architect) {
		c.Architect = Default().Architect
	}
	if c.Workflow.ReviewCycles < 1 {
		c.Workflow.ReviewCycles = 1
	}
	return c, nil
}

func isLegacyDefaultArchitect(a Agent) bool {
	return a.Name == "codex" &&
		a.Command == "codex" &&
		reflect.DeepEqual(a.Args, []string{"exec", "--sandbox", "read-only", "-"})
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

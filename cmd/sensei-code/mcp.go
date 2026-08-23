package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/mcpconfig"
)

// runMCP reports, and on request repairs, each agent's own route to Sensei.
// Every agent talks to Sensei through its native MCP configuration rather than
// through Sensei Code, so what the agent sees is what Sensei actually said.
func runMCP(repo gitx.Repo, cfg config.Config, args []string) error {
	if len(args) == 0 {
		printMCPStatus(repo.Root, cfg.Sensei.Args, cfg.SenseiAddressIsStated())
		return nil
	}
	target := strings.ToLower(strings.TrimSpace(args[0]))
	if target == "all" {
		var failures []string
		for _, agent := range mcpconfig.Ordered {
			if _, err := mcpconfig.Configure(repo.Root, agent, cfg.Sensei.Command, cfg.Sensei.Args, cfg.SenseiAddressIsStated()); err != nil {
				failures = append(failures, err.Error())
			}
		}
		printMCPStatus(repo.Root, cfg.Sensei.Args, cfg.SenseiAddressIsStated())
		if len(failures) != 0 {
			return fmt.Errorf("%s", strings.Join(failures, "; "))
		}
		return nil
	}
	agent, err := parseAgent(target)
	if err != nil {
		return err
	}
	status, err := mcpconfig.Configure(repo.Root, agent, cfg.Sensei.Command, cfg.Sensei.Args, cfg.SenseiAddressIsStated())
	if err != nil {
		return err
	}
	fmt.Printf("%-22s %s\n", mcpconfig.Label(agent), formatMCP(status))
	return nil
}

func parseAgent(value string) (mcpconfig.Agent, error) {
	switch value {
	case "1", "codex", "chatgpt", "openai":
		return mcpconfig.Codex, nil
	case "2", "claude", "anthropic":
		return mcpconfig.Claude, nil
	case "3", "antigravity", "agy", "google":
		return mcpconfig.Antigravity, nil
	}
	return "", fmt.Errorf("unknown agent %q; use codex, claude, antigravity, or all", value)
}

func printMCPStatus(repoRoot string, want []string, stated bool) {
	fmt.Fprintln(os.Stdout, "Sensei MCP access per agent")
	for i, status := range mcpconfig.Describe(repoRoot, want, stated) {
		fmt.Printf("  %d. %-22s %s\n", i+1, mcpconfig.Label(status.Agent), formatMCP(status))
		if status.Path != "" {
			fmt.Printf("     %s\n", status.Path)
		}
	}
	fmt.Fprintln(os.Stdout, "\nConfigure with: sensei-code mcp <codex|claude|antigravity|all>")
}

func formatMCP(status mcpconfig.Status) string {
	parts := []string{string(status.State)}
	if status.Detail != "" {
		parts = append(parts, status.Detail)
	}
	return strings.Join(parts, " · ")
}

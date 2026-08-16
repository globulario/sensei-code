package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/mcpconfig"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/setup"
)

// runSetup takes a checkout from nothing to a working session.
//
// It reports before it repairs. Everything here reaches outside this repository
// — a shared graph, a domain registry in the home directory, an agent's own MCP
// configuration — and a tool that silently rearranges those is harder to trust
// than one that asks. Without --apply it only says what it would do.
func runSetup(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) error {
	apply := false
	for _, arg := range args {
		switch arg {
		case "--apply", "-y":
			apply = true
		case "--help", "-h":
			fmt.Println(setupUsage)
			return nil
		default:
			return fmt.Errorf("unknown flag %q\n\n%s", arg, setupUsage)
		}
	}

	report := inspect(ctx, repo, cfg)
	fmt.Println(report.Render())

	repairable := report.Repairable()
	blocking := report.Blocking()

	if len(repairable) == 0 {
		if len(blocking) == 0 {
			fmt.Println("\nReady. Run sensei-code to start.")
			return nil
		}
		fmt.Println("\nThese need a person:")
		for _, c := range blocking {
			fmt.Printf("  %s\n    %s\n", c.Name, c.Fix)
		}
		return fmt.Errorf("setup is incomplete")
	}

	if !apply {
		fmt.Printf("\n%d thing%s can be repaired automatically. Re-run with --apply to do it:\n",
			len(repairable), plural(len(repairable)))
		for _, c := range repairable {
			fmt.Printf("  %s\n    %s\n", c.Name, c.Fix)
		}
		return nil
	}

	// Repairs unlock each other: `sensei init` is what creates the domain that
	// then has to be registered, so a single pass leaves a fresh checkout half
	// configured and the user running the same command again. It converges
	// instead, bounded so a repair that never takes effect cannot loop.
	const maxPasses = 4
	after := report
	for pass := 1; pass <= maxPasses; pass++ {
		fmt.Printf("\nRepairing (pass %d):\n", pass)
		for _, line := range setup.Apply(ctx, after) {
			fmt.Println("  " + line)
		}
		// Re-inspect rather than assume: a repair that reported success and
		// left the check failing is the false green this project refuses
		// everywhere else.
		after = inspect(ctx, repo, cfg)
		if len(after.Repairable()) == 0 {
			break
		}
		if pass == maxPasses {
			fmt.Println("\n  stopping: these repairs are not taking effect")
		}
	}

	fmt.Println("\nRe-checking:")
	fmt.Println(after.Render())
	if !after.Ready() {
		if blocking := after.Blocking(); len(blocking) != 0 {
			fmt.Println("\nThese need a person:")
			for _, c := range blocking {
				fmt.Printf("  %s\n    %s\n", c.Name, c.Fix)
			}
		}
		return fmt.Errorf("setup is still incomplete")
	}
	fmt.Println("\nReady. Run sensei-code to start.")
	return nil
}

const setupUsage = `Usage: sensei-code setup [--apply]

Checks everything a working session needs and reports what is wrong, what you
would see when it breaks, and the command that fixes it. With --apply it repairs
what it can and re-checks.`

// inspect gathers the machine and repository state the checks need.
func inspect(ctx context.Context, repo gitx.Repo, cfg config.Config) setup.Report {
	options := setup.Options{
		RepoRoot:        repo.Root,
		SenseiSourceDir: setup.FindSenseiSource(repo.Root),
		GraphAddr:       graphAddr(cfg),
	}
	// The tool list and the domain both come from the MCP server, and a server
	// that will not start is itself a finding rather than a reason to stop.
	if client, err := sensei.Start(ctx, repo.Root, cfg.Sensei.Command, cfg.Sensei.Args); err == nil {
		if raw, err := client.ListTools(); err == nil {
			options.ToolNames = setup.DecodeToolNames(raw)
		}
		if status, err := client.CallTool("sensei_workspace_status", map[string]any{"repo": repo.Root}); err == nil {
			options.Domain = sensei.RepositoryDomain(status)
		}
		client.Close()
	}
	if options.Domain == "" {
		options.Domain = configuredDomain(repo.Root)
	}

	report := setup.Inspect(ctx, options)
	// Agent access is part of readiness: a registered server whose tools an
	// agent cannot call is not access to Sensei.
	for _, access := range mcpconfig.Describe(repo.Root) {
		report.Checks = append(report.Checks, mcpCheck(repo.Root, cfg, access))
	}
	return report
}

func mcpCheck(repoRoot string, cfg config.Config, access mcpconfig.Status) setup.Check {
	c := setup.Check{Name: "mcp: " + string(access.Agent), Detail: access.Detail}
	switch access.State {
	case mcpconfig.Configured:
		c.State = setup.OK
	case mcpconfig.Unknown:
		c.State = setup.Degraded
		c.Detail = access.Detail
	default:
		c.State = setup.Degraded
		c.Symptom = "the agent's Sensei tool calls are cancelled, and it reports Sensei as unavailable"
		c.Fix = "sensei-code mcp " + string(access.Agent)
		agent := access.Agent
		c.Repair = func(ctx context.Context) error {
			_, err := mcpconfig.Configure(repoRoot, agent, cfg.Sensei.Command, cfg.Sensei.Args)
			return err
		}
	}
	return c
}

// configuredDomain reads the domain this checkout binds, for the case where the
// MCP server cannot answer yet.
func configuredDomain(repoRoot string) string {
	body, err := os.ReadFile(repoRoot + "/.sensei/config.yaml")
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(body), "\n") {
		if _, value, ok := strings.Cut(strings.TrimSpace(line), "domain:"); ok {
			return strings.Trim(strings.TrimSpace(value), `"'`)
		}
	}
	return ""
}

func graphAddr(cfg config.Config) string {
	for i, arg := range cfg.Sensei.Args {
		if arg == "--awareness-addr" && i+1 < len(cfg.Sensei.Args) {
			return cfg.Sensei.Args[i+1]
		}
	}
	return ""
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

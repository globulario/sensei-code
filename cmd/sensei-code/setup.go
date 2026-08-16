package main

import (
	"context"
	"fmt"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/gitx"
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

	report := setup.InspectRepository(ctx, repo.Root, cfg)
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
		after = setup.InspectRepository(ctx, repo.Root, cfg)
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

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

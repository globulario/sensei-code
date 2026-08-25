package main

// proofbench discriminate — the gate a task must pass before it may cost money.
//
// Runs each task's contract oracle against three specimens and reports whether
// the oracle separates them. A task whose ALTERNATE specimen fails is refused:
// that oracle has memorised one answer rather than learned the contract.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/proofbench"
)

func discriminate(args []string) int {
	fs := flag.NewFlagSet("discriminate", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to the manifest carrying contract oracles (required)")
	only := fs.String("task", "", "gate one task")
	out := fs.String("out", "", "directory for discrimination.json (default: beside the manifest)")
	timeout := fs.Duration("timeout", 10*time.Minute, "per-specimen timeout")
	if err := fs.Parse(args); err != nil || strings.TrimSpace(*manifest) == "" {
		fs.Usage()
		return exitUsage
	}
	m, _, err := proofbench.LoadManifest(*manifest)
	if err != nil {
		// A corpus being built is not yet a sound campaign; gate what parses.
		fmt.Fprintln(os.Stderr, "proofbench discriminate:", err)
		if m.Version == "" {
			return exitFailed
		}
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench discriminate:", err)
		return exitFailed
	}
	dir := *out
	if dir == "" {
		dir = dirOf(*manifest)
	}
	runner := proofbench.Runner{
		RepoRoot: root,
		WorkDir:  filepath.Join(os.TempDir(), "proofbench-discriminate", m.Version),
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout*10)
	defer cancel()

	var all []proofbench.TaskDiscrimination
	eligible, refused := 0, 0
	for _, t := range m.Tasks {
		if *only != "" && t.ID != *only {
			continue
		}
		if t.Contract == nil {
			fmt.Printf("%-30s no contract oracle yet\n", t.ID)
			continue
		}
		d := runner.Discriminate(ctx, dirOf(*manifest), t, *t.Contract)
		all = append(all, d)
		mark := "REFUSED "
		if d.Eligible {
			mark, eligible = "eligible", eligible+1
		} else {
			refused++
		}
		fmt.Printf("%-30s %s  %s\n", t.ID, mark, d.OracleHash)
		for _, r := range d.Results {
			ok := "  ok "
			if !r.OK {
				ok = "  BAD"
			}
			fmt.Printf("   %s %-10s want %-12s got %s\n", ok, r.Kind, r.Want, r.Got)
			if !r.OK {
				fmt.Printf("        %s\n", firstLines(r.Detail, 4))
			}
		}
		if d.Reason != "" {
			fmt.Printf("   reason: %s\n", d.Reason)
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	b, _ := json.MarshalIndent(all, "", "  ")
	path := filepath.Join(dir, "discrimination.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	fmt.Printf("\n%d eligible, %d refused. wrote %s\n", eligible, refused, path)
	if refused != 0 {
		return exitNotGood
	}
	return exitOK
}

func firstLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n        ")
}

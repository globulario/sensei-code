package main

// proofbench environment — prove the governed child reaches the intended graph,
// before a wave spends anything.
//
// It runs one real `sensei-code run` with a trivial task and a 30-second
// budget. The workspace-status answer arrives from the start gate, which runs
// before any provider is invoked, so the identity is established without
// spending a provider token.

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

func environment(args []string) int {
	fs := flag.NewFlagSet("environment", flag.ExitOnError)
	manifest := fs.String("manifest", "", "manifest whose wave this environment will run")
	pin := fs.Bool("pin", false, "record the observed identity as the wave's pinned authority")
	against := fs.String("against", "", "path to a pinned identity to compare with")
	if err := fs.Parse(args); err != nil {
		fs.Usage()
		return exitUsage
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	got, err := proofbench.InspectEnvironment(context.Background(), binaryPath(root), root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitNotGood
	}
	fmt.Printf("graph the governed child reaches:\n  %s\n\n", got)

	if p := strings.TrimSpace(*against); p != "" {
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			fmt.Fprintln(os.Stderr, "reading pinned identity:", rerr)
			return exitFailed
		}
		var pinned proofbench.GraphIdentity
		if jerr := json.Unmarshal(b, &pinned); jerr != nil {
			fmt.Fprintln(os.Stderr, "pinned identity:", jerr)
			return exitFailed
		}
		if serr := proofbench.RequireStableEnvironment(pinned, got); serr != nil {
			fmt.Fprintln(os.Stderr, serr)
			return exitNotGood
		}
		fmt.Println("matches the pinned authority.")
		return exitOK
	}

	if rerr := proofbench.RequireEnvironment(got); rerr != nil {
		fmt.Fprintln(os.Stderr, rerr)
		return exitNotGood
	}
	fmt.Println("usable: authoritative, stamped, workspace complete.")

	if *pin {
		dir := "."
		if strings.TrimSpace(*manifest) != "" {
			dir = dirOf(*manifest)
		}
		out := filepath.Join(dir, "environment.json")
		b, _ := json.MarshalIndent(got, "", "  ")
		if werr := os.WriteFile(out, append(b, '\n'), 0o644); werr != nil {
			fmt.Fprintln(os.Stderr, werr)
			return exitFailed
		}
		fmt.Println("pinned to", out, "at", time.Now().UTC().Format(time.RFC3339))
	}
	return exitOK
}

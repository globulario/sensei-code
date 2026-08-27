// SPDX-License-Identifier: AGPL-3.0-only

// Command stubagent is a deterministic stand-in for a provider.
//
// It exists so the governed state machine can be exercised without an LLM. The
// real canary answers the more important question — does governed mode work
// against actual providers — but when it fails halfway there is no way to tell
// a skipped transition from a model that wandered off. This one removes the
// model from the picture entirely: if the chain breaks here, the break is in
// the engine.
//
// It is a tripwire, not a benchmark. It always cooperates, so it proves the
// transitions connect, never that the reasoning was any good.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	role := flag.String("role", "", "architect | implementor | reviewer")
	target := flag.String("target", "", "file the implementor appends to, relative to the workspace")
	artifact := flag.String("artifact", "", "implementor also leaves a >5 MB ELF-shaped build output at this workspace path (#89)")
	flag.Parse()

	// The prompt arrives on stdin. It is read and discarded: a deterministic
	// stub that varied with the prompt would be a very small language model,
	// and then a failure would again be ambiguous.
	_, _ = io.Copy(io.Discard, os.Stdin)

	switch *role {
	case "architect":
		emit(map[string]any{
			"decision": "proceed",
			"summary":  "append one comment line to " + *target,
			"mode":     "modify",
			"plan":     "Append a single trailing comment line to " + *target + ". Change nothing else.",
			"steps":    []string{"open " + *target, "append one comment line", "leave every other file untouched"},
			"files":    []string{*target},
			"claims": []map[string]string{{
				"statement": "the target file exists in the working tree",
				"about":     *target,
				"source":    "repository",
			}},
		})
	case "implementor":
		// A real change, because the engine reads an empty diff as a worker
		// that produced nothing.
		path := filepath.Join(mustWD(), *target)
		f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
		if err != nil {
			fmt.Fprintln(os.Stderr, "stubagent:", err)
			os.Exit(1)
		}
		defer f.Close()
		if _, err := f.WriteString("\n// stub-smoke: appended by the deterministic governed-run tripwire\n"); err != nil {
			fmt.Fprintln(os.Stderr, "stubagent:", err)
			os.Exit(1)
		}
		if *artifact != "" {
			// A worker that built the command to check its edit, and left the
			// binary behind. ~6 MB with an ELF magic, well over the audit's
			// 5 MiB payload limit.
			body := append([]byte{0x7f, 'E', 'L', 'F', 2, 1, 1, 0}, bytes.Repeat([]byte{0x00, 0xff, 0x13}, 2*1024*1024)...)
			if err := os.WriteFile(filepath.Join(mustWD(), *artifact), body, 0o755); err != nil {
				fmt.Fprintln(os.Stderr, "stubagent:", err)
				os.Exit(1)
			}
		}
		fmt.Println("appended one comment line to " + *target)
	case "reviewer":
		emit(map[string]any{
			"decision": "accept",
			"summary":  "one appended comment line, inside the plan's file list, no behaviour changed",
		})
	default:
		fmt.Fprintln(os.Stderr, "stubagent: --role must be architect, implementor or reviewer")
		os.Exit(2)
	}
}

func emit(v any) {
	body, err := json.Marshal(v)
	if err != nil {
		fmt.Fprintln(os.Stderr, "stubagent:", err)
		os.Exit(1)
	}
	fmt.Println(string(body))
}

func mustWD() string {
	wd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "stubagent:", err)
		os.Exit(1)
	}
	return strings.TrimSpace(wd)
}

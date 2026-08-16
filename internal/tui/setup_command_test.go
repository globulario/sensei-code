package tui

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/globulario/sensei-code/internal/command"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/workflow"
)

// TestSetupCommandReportsAndRepairsNothing drives the route a typed /setup
// actually takes: the catalogue entry, the dispatch in runCommand, and the
// background command that produces the text.
//
// Asserting on Report alone would miss the two ways this breaks in the TUI —
// /setup silently falling through to the MCP client path and failing, or the
// dispatch calling Apply because the report is right there and repairing looks
// helpful. Repair reaches a shared graph and configuration outside this
// checkout, and a session that rearranged those because somebody asked what was
// wrong is the surprise the CLI deliberately refuses.
func TestSetupCommandReportsAndRepairsNothing(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	// Empty PATH: the readiness checks observe the same absent machine
	// everywhere, and nothing here reaches a live graph.
	t.Setenv("PATH", "")

	c, arg, ok := command.Lookup("/setup")
	if !ok {
		t.Fatal("/setup is not in the catalogue, so the TUI can never dispatch it")
	}
	m := Model{
		ctx:    context.Background(),
		engine: workflow.New(gitx.Repo{Root: repo}, config.Config{}, nil, nil, ""),
	}

	before := tree(t, home, repo)
	_, cmd := m.runCommand(c, arg)
	if cmd == nil {
		t.Fatal("/setup dispatched no work, so it reports nothing")
	}
	result, found := findCommandResult(cmd)
	if !found {
		t.Fatal("/setup produced no command result, so it fell through the dispatch")
	}

	if result.err != nil {
		t.Fatalf("/setup failed instead of reporting: %v", result.err)
	}
	if result.name != c.Name {
		t.Fatalf("result is labelled %q, want %q", result.name, c.Name)
	}
	if !strings.HasPrefix(result.text, "Setup") {
		t.Fatalf("the result is not a readiness report:\n%s", result.text)
	}
	// A repair was offered and, being a report, was not taken.
	if !strings.Contains(result.text, "--apply can do this") {
		t.Fatalf("no repairable check was reported, so this proves nothing was skipped:\n%s", result.text)
	}
	if strings.Contains(result.text, "will run") {
		t.Fatalf("the report promised a repair it never performs:\n%s", result.text)
	}
	after := tree(t, home, repo)
	for path, was := range before {
		if now, ok := after[path]; !ok {
			t.Fatalf("/setup removed %s", path)
		} else if now != was {
			t.Fatalf("/setup changed %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Fatalf("/setup repaired something: it created %s", path)
		}
	}
}

// findCommandResult runs a dispatched command, following the batch the TUI
// wraps it in, and returns the architect result it eventually yields.
func findCommandResult(cmd tea.Cmd) (commandResultMsg, bool) {
	switch msg := cmd().(type) {
	case commandResultMsg:
		return msg, true
	case tea.BatchMsg:
		for _, inner := range msg {
			if inner == nil {
				continue
			}
			if result, ok := findCommandResult(inner); ok {
				return result, true
			}
		}
	}
	return commandResultMsg{}, false
}

// tree records every path under the given roots with its contents, so anything
// a command wrote shows up as a difference rather than as silence.
func tree(t *testing.T, roots ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				out[path] = "<dir>"
				return nil
			}
			body, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[path] = string(body)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

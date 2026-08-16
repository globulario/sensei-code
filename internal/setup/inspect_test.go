package setup

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
)

// TestInspectRepositoryObservesAndChangesNothing exercises the entry point
// /setup actually calls, not a hand-built report.
//
// Everything the checks can repair lies outside this repository — a shared
// graph, the domain registry in the home directory, each agent's own MCP
// configuration — so the one guarantee a read-only reader rests on is that
// producing the report touches none of it. A hand-built Report cannot prove
// that: the risk is a check that repairs while it inspects, and only the real
// inspection would carry it.
func TestInspectRepositoryObservesAndChangesNothing(t *testing.T) {
	home, repo := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	// An empty PATH keeps the inspection off this machine's real Sensei, so the
	// test sees the same state everywhere and cannot reach a live graph.
	t.Setenv("PATH", "")

	before := tree(t, home, repo)
	report := InspectRepository(context.Background(), repo, config.Config{})

	if len(report.Checks) == 0 {
		t.Fatal("inspection produced no checks")
	}
	// The guarantee is only worth something if there was something it could
	// have changed.
	repairable := report.Repairable()
	if len(repairable) == 0 {
		t.Fatal("a bare checkout offered no repair, so this test proves nothing")
	}
	if registry := filepath.Join(home, ".sensei", "domains.yaml"); exists(registry) {
		t.Fatalf("inspection registered a domain in %s", registry)
	}
	after := tree(t, home, repo)
	for path, was := range before {
		if now, ok := after[path]; !ok {
			t.Fatalf("inspection removed %s", path)
		} else if now != was {
			t.Fatalf("inspection changed %s", path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok {
			t.Fatalf("inspection created %s", path)
		}
	}

	// The report it produced offers the repair without claiming to have run it.
	out := report.Render()
	if !strings.Contains(out, "can run: "+repairable[0].Fix) {
		t.Fatalf("render does not offer %q as something that can be run:\n%s", repairable[0].Fix, out)
	}
	if strings.Contains(out, "will run") {
		t.Fatalf("render promises a repair the reader never performs:\n%s", out)
	}
}

// tree records every path under the given roots with its contents, so anything
// written during inspection shows up as a difference rather than as silence.
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

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

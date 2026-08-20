//go:build acceptance

package workflow

// The Stage 1 instrument, pointed at the real deployment.
//
// The unit tests establish that each condition can block on its own. They
// cannot answer the question that decides whether the tier is worth having:
// against this repository's actual graph, does anything qualify, and when it
// does not, which condition is doing the blocking. A fixture cannot answer that
// because a fixture is written by someone who already has an opinion.
//
//	go test -tags acceptance ./internal/workflow/ -run TestRoutineClassificationAgainstTheLiveGraph -v
//
// It asserts only what a test can honestly assert — that a governed file is
// never routine — and prints the rest for a person to read.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/sensei"
)

func TestRoutineClassificationAgainstTheLiveGraph(t *testing.T) {
	root := liveRepoRoot(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sc, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Fatalf("start Sensei: %v", err)
	}
	defer sc.Close()

	for _, file := range []string{
		"internal/workflow/engine.go", // governed by critical invariants
		"internal/agent/agent.go",     // real code the graph holds no facts about
		"internal/tui/model_test.go",  // a test file, the shape the canary used
		"internal/roles/roles.go",     // new code, added yesterday
		"docs/p1-level-1-routine.md",  // not code at all
	} {
		t.Run(file, func(t *testing.T) {
			result, err := sc.CallTool("awareness_preflight", map[string]any{
				"task":   "classify this change for the Level-1 routine tier",
				"files":  []string{file},
				"domain": "github.com/globulario/sensei-code",
				"mode":   "compact",
			})
			if err != nil {
				t.Fatalf("preflight: %v", err)
			}
			scoped, err := sensei.DecodePreflight(result)
			if err != nil {
				t.Fatalf("decode preflight: %v", err)
			}

			// The edit check is asked for real too, because condition 7 is the
			// one whose result nobody has observed in the affirmative: this
			// repository's forbidden fixes carry no matchable shape, so a clean
			// answer here proves that nothing matched in a corpus where nothing
			// can match. Worth knowing, and worth not mistaking for a guard.
			edit := sensei.EditCheckResult{}
			checked, err := sc.CallTool("awareness_edit_check", map[string]any{
				"file":             file,
				"proposed_content": "// a probe, deliberately innocuous\n",
				"domain":           "github.com/globulario/sensei-code",
			})
			if err != nil {
				t.Logf("edit check unavailable: %v", err)
			} else if edit, err = sensei.DecodeEditCheck(checked); err != nil {
				t.Logf("edit check undecodable: %v", err)
			}

			d := classifyRoutine(scoped, nil, edit, []string{file}, []string{file})
			t.Logf("status=%s invariants=%d blind_spots=%d blast=%s gate=%s edit_answered=%v",
				scoped.Status, len(scoped.DirectInvariants), len(scoped.BlindSpots),
				scoped.ChangeRisk.Blast(), scoped.ChangeRisk.Gate(), edit.Answered)
			t.Logf("  → %s", d.Describe())
			for _, held := range d.Qualifying {
				t.Logf("     held: %s", held)
			}

			// The one assertion. A file carrying critical invariants must never
			// be routine, whatever else the deployment reports.
			if strings.Contains(file, "internal/workflow/engine.go") && d.Routine {
				t.Fatal("a file governed by critical invariants was classified routine")
			}
		})
	}
}

func liveRepoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for dir := wd; dir != "/"; dir = filepath.Dir(dir) {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
	}
	t.Fatal("no module root above the test")
	return ""
}

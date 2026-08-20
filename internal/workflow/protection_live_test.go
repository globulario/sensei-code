//go:build acceptance

package workflow

// Item 1 of the Stage 1 closure list: the delegated exclusion.
//
// The specification lists "any file listed in docs/awareness/high_risk_files.yaml"
// among the categorical exclusions. This repository does not re-read that file.
// The reasoning is in routine.go: the manual registry is one input to Sensei's
// own protection derivation, which also covers governed sources, files a
// governed invariant names, contract files and annotated source. Re-deriving a
// subset locally would duplicate Sensei governance semantics and disagree with
// Sensei the moment either side changed.
//
// Delegation is a defensible choice and an untested one is a hope. This asserts
// the property the delegation is supposed to deliver: everything Sensei
// protects, the routine tier refuses. It reads the effective set rather than a
// hand-written list, so it keeps testing the real delegation as that set grows.
//
//	go test -tags acceptance ./internal/workflow/ -run TestEverythingSenseiProtectsIsRefused -v
//
// Note that the registry here is empty -- manual_count is 0 -- so every path in
// the set arrives by derivation. That is exactly the case a local re-read of
// high_risk_files.yaml would have got wrong: it would have found nothing to
// exclude and reported a clean pass.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/report"
	"github.com/globulario/sensei-code/internal/sensei"
)

func TestEverythingSenseiProtectsIsRefused(t *testing.T) {
	root := liveRepoRoot(t)
	protected := protectedFiles(t, root)
	if len(protected) == 0 {
		t.Skip("no protection snapshot to read, so the delegation was never exercised")
	}
	t.Logf("effective protection snapshot lists %d path(s); testing the %d that are files",
		len(protected), len(protected))

	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sc, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Fatalf("start Sensei: %v", err)
	}
	defer sc.Close()

	for _, file := range protected {
		t.Run(file, func(t *testing.T) {
			result, err := sc.CallTool("awareness_preflight", map[string]any{
				"task":   "classify a Sensei-protected file for the routine tier",
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
			d := classifyRoutine(scoped, nil, sensei.EditCheckResult{Answered: true}, []string{file},
				CandidateShape{
					Files:         []report.FileChange{{Path: file, Status: report.Modified}},
					TestLineDelta: map[string]int{},
				})
			t.Logf("status=%s invariants=%d coverage_proven=%v → %s",
				scoped.Status, len(scoped.DirectInvariants), scoped.Coverage.Proven(), d.Describe())
			if d.Routine {
				t.Fatalf("Sensei protects %s and the routine tier would have skipped escalation for it", file)
			}
		})
	}
}

// protectedFiles reads the effective protection set Sensei published, keeping
// the entries that are files in this checkout. Directories are dropped rather
// than expanded: a preflight takes file paths, and expanding a directory here
// would be this test inventing scope Sensei did not name.
//
// The snapshot is scanned for the two lines this needs rather than parsed as
// YAML, because adding a YAML dependency to the module for one acceptance test
// is a worse trade than reading two prefixes.
func protectedFiles(t *testing.T, root string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, ".sensei", "project", "protection-coverage.yaml"))
	if err != nil {
		t.Skipf("no protection snapshot: %v", err)
	}
	var files []string
	var counts []string
	for _, line := range strings.Split(string(raw), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "manual_count:"),
			strings.HasPrefix(trimmed, "derived_count:"),
			strings.HasPrefix(trimmed, "provisional_count:"):
			counts = append(counts, trimmed)
			continue
		case !strings.HasPrefix(trimmed, "- path:"):
			continue
		}
		path := strings.TrimSpace(strings.TrimPrefix(trimmed, "- path:"))
		if path == "" {
			continue
		}
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil || info.IsDir() {
			continue
		}
		files = append(files, path)
	}
	t.Logf("snapshot: %s", strings.Join(counts, " "))
	return files
}

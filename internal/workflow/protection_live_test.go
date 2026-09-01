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
		// AN EMPTY PROTECTION SET IS THE WORST STATE, NOT AN ABSENCE OF WORK.
		//
		// This skipped here. "Nothing is protected" is precisely the condition
		// this test exists to make impossible, so a configuration that empties
		// the snapshot used to turn the test GREEN -- false-green evidence
		// debt hiding the exact class of defect the checker exists to detect.
		t.Fatal("the protection snapshot yielded no file to test: either nothing is " +
			"protected, or the snapshot could not be read as protecting anything. " +
			"Both are defects, and neither is a reason for this test to pass.")
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
		// The snapshot is a COMMITTED REPOSITORY ARTIFACT. Its absence is a
		// defect in this repository, not a limitation of the environment the
		// test happens to run in.
		t.Fatalf("no protection snapshot at .sensei/project/protection-coverage.yaml: %v; "+
			"it is committed to this repository, so its absence is a defect and not a "+
			"reason to stop checking", err)
	}
	var files []string
	var declared []string
	var dead []string
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
		declared = append(declared, path)
		info, err := os.Stat(filepath.Join(root, path))
		if err != nil {
			// A DEAD REFERENT, not a path to skip past. The snapshot claims to
			// protect something that is not there, so either the protection is
			// fictional or the file moved without the snapshot following.
			dead = append(dead, path)
			continue
		}
		if info.IsDir() {
			continue
		}
		files = append(files, path)
	}
	t.Logf("snapshot: %s", strings.Join(counts, " "))
	if len(declared) == 0 {
		t.Fatal("the protection snapshot declares no path at all; an empty protection " +
			"set is the worst state this test could find, not an absence of work")
	}
	if len(dead) > 0 {
		t.Fatalf("the protection snapshot names %d path(s) that do not exist: %s. "+
			"A dead referent cannot be protected, and silently skipping it lets the "+
			"snapshot claim coverage it does not have", len(dead), strings.Join(dead, ", "))
	}
	if len(files) == 0 {
		t.Fatalf("the snapshot declares %d path(s) but none is a file, so this test has "+
			"no subject; coverage is absent rather than satisfied", len(declared))
	}
	return files
}

package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/candidate"
)

// Finding 2 of the 2026-08-21 audit. Disposal consulted tc.EvidenceSnapshot,
// which is written only after validation and a decodable Sensei audit. Every
// earlier exit left it zeroed while the worktree held real work, and the zero
// was read as "the candidate holds no work" -- so the worktree and branch were
// deleted and that sentence was recorded as the reason.
//
// These run against a real git repository rather than asserting on source text,
// because the defect was that a correct-looking branch consulted the wrong
// source. Only observation distinguishes the two.

func newRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")
	return dir, run("rev-parse", "HEAD")
}

func candidateIdentityFor(base string) candidate.Identity {
	return candidate.Identity{BaseSHA: base}
}

func TestAnUntouchedCandidateIsSeenAsEmpty(t *testing.T) {
	dir, base := newRepo(t)
	seen := observeCandidate(context.Background(), dir, base)
	if seen.Err != nil {
		t.Fatalf("observation failed: %v", seen.Err)
	}
	if seen.HoldsWork() {
		t.Fatalf("an unmodified candidate is reported as holding work: %+v", seen)
	}
}

// The defect, exactly: work on disk, snapshot empty. Before the fix this was
// deleted and recorded as holding nothing.
func TestAModifiedCandidateHoldsWorkEvenWithAnEmptySnapshot(t *testing.T) {
	dir, base := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\nthe worker's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := observeCandidate(context.Background(), dir, base)
	if seen.Err != nil {
		t.Fatalf("observation failed: %v", seen.Err)
	}
	if !seen.HoldsWork() {
		t.Fatal("a candidate with an edited file is reported as holding no work")
	}
	if seen.DiffBytes == 0 || len(seen.ChangedPaths) == 0 {
		t.Fatalf("the observation carries no detail: %+v", seen)
	}

	// The stale snapshot the run would have had at an early exit.
	tc := &taskContext{}
	if tc.EvidenceSnapshot.DiffBytes != 0 {
		t.Fatal("test setup is wrong: the snapshot should start empty")
	}
	// The old code decided from this, and it says "no work".
	if !candidateEvidence(candidateIdentityFor(base), tc).ProducedNoWork {
		t.Fatal("test setup is wrong: the stale snapshot should read as no work")
	}
	// Observation must disagree, and observation is what disposal uses.
	if !seen.HoldsWork() {
		t.Fatal("disposal would still delete a candidate holding work")
	}
}

// A newly created, uncommitted file is work too: --intent-to-add is what makes
// it visible, and losing it is the same loss.
func TestANewUntrackedFileCountsAsWork(t *testing.T) {
	dir, base := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := observeCandidate(context.Background(), dir, base)
	if seen.Err != nil {
		t.Fatalf("observation failed: %v", seen.Err)
	}
	if !seen.HoldsWork() {
		t.Fatal("an untracked new file is reported as no work")
	}
	var found bool
	for _, p := range seen.ChangedPaths {
		if strings.Contains(p, "new.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the new file is not named in the observation: %v", seen.ChangedPaths)
	}
}

// Deletion is irreversible and observation failed, so the destroying branch must
// not be reachable from an answer nobody established.
func TestAnUnreadableCandidateHoldsWork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		workspace string
		base      string
	}{
		{"missing worktree", filepath.Join(t.TempDir(), "gone"), "abc123"},
		{"no workspace", "", "abc123"},
		{"no base", t.TempDir(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := observeCandidate(context.Background(), tc.workspace, tc.base)
			if seen.Err == nil {
				t.Fatal("an unreadable candidate reported no error")
			}
			if !seen.HoldsWork() {
				t.Fatal("an unreadable candidate is eligible for automatic deletion")
			}
		})
	}
}

// Disposal must read the candidate, not replay the snapshot.
func TestDisposalObservesRatherThanRecalls(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "disposeIfEmpty")
	if !strings.Contains(body, "observeCandidate") {
		t.Fatal("disposal no longer observes the candidate")
	}
	if strings.Contains(body, "candidateEvidence") && !strings.Contains(body, "observeCandidate") {
		t.Fatal("disposal decides from the recalled snapshot again")
	}
}

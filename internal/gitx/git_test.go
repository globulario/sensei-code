package gitx

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestWorktreeLivesOutsideRepository(t *testing.T) {
	repo := Repo{Root: filepath.Join(string(filepath.Separator), "work", "sensei")}
	got := repo.WorktreePath("task/1", "claude")
	if strings.HasPrefix(got, repo.Root+string(filepath.Separator)) {
		t.Fatalf("candidate worktree must not be nested in canonical repository: %s", got)
	}
	if !strings.Contains(got, "task-1") {
		t.Fatalf("task id was not sanitized: %s", got)
	}
}

func TestWorktreePathIsASiblingNamedOnce(t *testing.T) {
	r := Repo{Root: "/home/dave/src/sensei-code"}
	got := r.WorktreePath("task-1", "claude")
	want := "/home/dave/src/.sensei-code-worktrees/task-1/claude"
	if got != want {
		t.Fatalf("WorktreePath() = %q, want %q", got, want)
	}
}

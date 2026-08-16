package gitx

import (
	"context"
	"os"
	"os/exec"
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

func TestCandidateDiffIncludesFilesTheWorkerCreated(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@example.invalid")
	run("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "main.go")
	run("commit", "-q", "-m", "init")

	// A worker edits one file and creates another, which is the shape of almost
	// every real change.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc A() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main_test.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	repo := Repo{Root: dir}
	plain, err := repo.Diff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "main_test.go") {
		t.Fatal("plain diff unexpectedly contains the new file; this test no longer proves anything")
	}
	candidate, err := repo.CandidateDiff(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(candidate, "main_test.go") {
		t.Fatal("the candidate diff omits a file the worker created, so a reviewer cannot see it")
	}
	if !strings.Contains(candidate, "func A()") {
		t.Fatal("the candidate diff lost the edit to the tracked file")
	}
}

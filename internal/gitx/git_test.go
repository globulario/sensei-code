package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func TestWorktreeLivesOutsideRepository(t *testing.T) {
	repo := Repo{Root: filepath.Join(string(filepath.Separator), "work", "sensei")}
	got := repo.WorktreePath("task/1")
	if strings.HasPrefix(got, repo.Root+string(filepath.Separator)) {
		t.Fatalf("candidate worktree must not be nested in canonical repository: %s", got)
	}
	if !strings.Contains(got, "task-1") {
		t.Fatalf("task id was not sanitized: %s", got)
	}
}

func TestWorktreePathIsASiblingNamedOnce(t *testing.T) {
	r := Repo{Root: "/home/dave/src/sensei-code"}
	got := r.WorktreePath("task-1")
	want := "/home/dave/src/.sensei-code-worktrees/task-1"
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
	plain, err := repo.Diff(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(plain, "main_test.go") {
		t.Fatal("plain diff unexpectedly contains the new file; this test no longer proves anything")
	}
	candidate, err := repo.CandidateDiff(context.Background(), "")
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

func TestCreateWorktreeReusesTheTaskCandidate(t *testing.T) {
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

	repo := Repo{Root: dir}
	first, err := repo.CreateWorktree(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	// A worker leaves work behind when it runs out of review cycles.
	if err := os.WriteFile(filepath.Join(first, "progress.txt"), []byte("half done\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	second, err := repo.CreateWorktree(context.Background(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if second != first {
		t.Fatalf("second worker got a different worktree (%s vs %s), so the work was abandoned", second, first)
	}
	if _, err := os.Stat(filepath.Join(second, "progress.txt")); err != nil {
		t.Fatal("the previous worker's progress was not carried over")
	}
}

// TestCandidateDiffIsNotTrimmed pins the bug that made every Sensei audit in a
// real run return cannot_verify.
//
// A unified diff is a format, not a value. Trimming a patch whose final added
// lines are blank leaves the hunk header promising more lines than the body
// contains, and Sensei rejects it as malformed_diff -- "declares 67 added lines
// but contains 65". The symptom looked like graph trouble for several runs and
// was two missing newlines.
func TestCandidateDiffIsNotTrimmed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := Repo{Root: dir}
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	write(t, dir, "seed.txt", "seed\n")
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "seed")

	// A new file whose content ends in blank lines: exactly the shape whose
	// trailing newlines a trim would eat.
	write(t, dir, "added.txt", "one\ntwo\n\n\n")

	diff, err := repo.CandidateDiff(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(diff, "\n") {
		t.Fatal("the candidate diff does not end with a newline; a patch without its final newline is malformed")
	}

	// The hunk header must agree with the body, which is precisely what Sensei
	// checks and what the trim used to break.
	header := regexp.MustCompile(`@@ -0,0 \+1,(\d+) @@`).FindStringSubmatch(diff)
	if header == nil {
		t.Fatalf("no hunk header for the added file:\n%s", diff)
	}
	declared, err := strconv.Atoi(header[1])
	if err != nil {
		t.Fatal(err)
	}
	added := strings.Count(diff[strings.Index(diff, header[0]):], "\n+")
	if added != declared {
		t.Fatalf("hunk declares %d added lines but the body contains %d; this is the malformed_diff Sensei rejects:\n%s", declared, added, diff)
	}
}

// TestCandidateDiffSurvivesAWorkerThatCommits pins the second half.
//
// local_commit is a granted capability, and a worker that used it left a clean
// working tree -- so a working-tree diff came back empty and the run reported
// that the implementor had produced nothing. It had produced everything and
// then tidied up.
func TestCandidateDiffSurvivesAWorkerThatCommits(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := Repo{Root: dir}
	mustGit(t, dir, "init", "-b", "main")
	mustGit(t, dir, "config", "user.email", "t@t")
	mustGit(t, dir, "config", "user.name", "t")
	write(t, dir, "seed.txt", "seed\n")
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "seed")

	base, err := repo.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// The worker writes a file and commits it, which it is permitted to do.
	write(t, dir, "work.txt", "the work\n")
	mustGit(t, dir, "add", ".")
	mustGit(t, dir, "commit", "-m", "worker commit")

	if uncommitted, err := repo.CandidateDiff(ctx, ""); err != nil {
		t.Fatal(err)
	} else if strings.TrimSpace(uncommitted) != "" {
		t.Fatal("this test proves nothing: the working tree was expected to be clean after the commit")
	}

	diff, err := repo.CandidateDiff(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(diff, "work.txt") || !strings.Contains(diff, "the work") {
		t.Fatalf("committed candidate work is invisible to the reviewer:\n%s", diff)
	}
}

// mustGit runs a git command in dir and fails the test if it errors.
func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
}

// write puts a file in dir, creating parents as needed.
func write(t *testing.T, dir, name, body string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

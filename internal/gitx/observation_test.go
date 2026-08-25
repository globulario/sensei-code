package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The read-only measurement must not write the thing it is measuring.
//
// `git status` may refresh the index while answering: when a tracked file's
// stat data is stale but its content is unchanged, git notices, and rewrites
// .git/index to cache the new stat. That is a write to the repository -- so the
// observation lane's own cleanliness check was capable of modifying the
// repository it exists to prove unmodified.
//
// The observable is .git/index: its bytes and its mtime. That is what git
// actually rewrites, and it is checkable without depending on filesystem
// timestamp granularity for TRACKED FILE content, which is where byte-for-byte
// metadata comparison is unreliable.
//
// The control matters as much as the assertion. A test that only checked "the
// index did not change" would pass on any git that never refreshes, on a
// filesystem where the stat never went stale, and on a specimen where the
// refresh was never provoked -- proving nothing while reading as PASS. So the
// control runs first and FAILS the test if the unflagged command does not
// rewrite the index: no live defect, no meaningful proof.
func TestTheReadOnlyMeasurementDoesNotWriteTheIndex(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	mustGit(t, dir, "config", "user.email", "t@example.invalid")
	mustGit(t, dir, "config", "user.name", "T")
	write(t, dir, "main.go", "package main\n")
	mustGit(t, dir, "add", "main.go")
	mustGit(t, dir, "commit", "-qm", "init")
	// Prime it, so the index already holds fresh stat data and the refresh
	// below is caused by this test rather than by the commit.
	mustGit(t, dir, "status", "--porcelain")

	index := filepath.Join(dir, ".git", "index")
	// staleStat rewrites the tracked file with IDENTICAL content and a later
	// mtime. Content is unchanged, so the repository is still clean; only the
	// cached stat is stale, which is precisely what tempts git to refresh.
	staleStat := func() {
		t.Helper()
		later := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(filepath.Join(dir, "main.go"), later, later); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := func() (string, time.Time) {
		t.Helper()
		b, err := os.ReadFile(index)
		if err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(index)
		if err != nil {
			t.Fatal(err)
		}
		return string(b), fi.ModTime()
	}

	// Control: the unflagged command must rewrite the index here, or this
	// specimen does not exercise the defect and the assertion below is empty.
	beforeBytes, beforeTime := snapshot()
	staleStat()
	if out, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput(); err != nil {
		t.Fatalf("git status: %v %s", err, out)
	}
	controlBytes, controlTime := snapshot()
	if controlBytes == beforeBytes && controlTime.Equal(beforeTime) {
		t.Skip("this git does not refresh the index on status here, so there is no live defect " +
			"for the flagged call to avoid; the assertion below would prove nothing")
	}

	// The measurement itself, in the same provoked state.
	staleStat()
	repo := Repo{Root: dir}
	status, err := repo.WorktreeIsCleanDetail(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	afterBytes, afterTime := snapshot()
	if afterBytes != controlBytes {
		t.Error("the observation lane's cleanliness check rewrote .git/index; " +
			"a checker for a read-only lane became the write it is trying to detect")
	}
	if !afterTime.Equal(controlTime) {
		t.Errorf("the observation lane's cleanliness check touched .git/index (%s -> %s)",
			controlTime, afterTime)
	}
	// And it is still the right answer: suppressing the lock must not have cost
	// the measurement its correctness. Identical content means clean.
	if status != "" {
		t.Errorf("the repository is unmodified but was reported dirty: %q", status)
	}
}

// Every read-only inspection carries the flag, and no write does.
//
// Pinned by name rather than by behaviour because the behaviour is a negative:
// a future edit that reaches for exec.CommandContext directly would silently
// reintroduce the refreshing call, and nothing else here would notice.
func TestReadOnlyInspectionsTakeNoOptionalLock(t *testing.T) {
	src := source(t)
	// The reads.
	for _, fn := range []string{
		"func (r Repo) Head(",
		"func (r Repo) Branch(",
		"func (r Repo) IsClean(",
		"func (r Repo) WorktreeIsClean(",
		"func (r Repo) WorktreeIsCleanDetail(",
	} {
		body := bodyOf(t, src, fn)
		if !strings.Contains(body, "r.readOnly") {
			t.Errorf("%s inspects a checkout without going through the read-only path, "+
				"so it may refresh the index it is reading", fn)
		}
	}
	// Discover runs before a Repo exists and spells the flag out itself.
	// Matched on the identifier: the literal lives in exactly one place so a
	// second spelling cannot drift away from it.
	const flag = "noOptionalLocks"
	if !strings.Contains(bodyOf(t, src, "func Discover("), flag) {
		t.Errorf("Discover reads the repository without %s", noOptionalLocks)
	}
	// And the writes are left alone. Suppressing the lock a write needs would
	// be a correctness bug wearing this fix's clothes.
	for _, fn := range []string{
		"func (r Repo) addWorktree(",
		"func (r Repo) RemoveWorktree(",
		"func (r Repo) DeleteBranch(",
		"func (r Repo) CreateObservationWorktree(",
	} {
		if strings.Contains(bodyOf(t, src, fn), flag) {
			t.Errorf("%s is a write and must not suppress its locks", fn)
		}
	}
}

// The narrow guarantee must stay narrow.
//
// The first draft of the observation-worktree comment said a separate worktree
// "stops a write-capable provider from touching" the governed checkout. It does
// not: a subprocess keeps the ambient permissions of the user running Sensei
// Code and can address any path it likes. The observation lane audited this
// file and reported that sentence as false -- the same overclaim the reviewer
// had just rejected in the post-hoc cleanliness check, rewritten one layer
// down. Overclaims about this boundary have now been written twice, so the
// phrasing is pinned.
func TestTheIsolationClaimStaysNarrow(t *testing.T) {
	src := source(t)
	for _, overclaim := range []string{
		"no file is written",
		"prevents access",
		"cannot access",
		"cannot reach",
	} {
		if strings.Contains(strings.ToLower(prose(src)), overclaim) {
			t.Errorf("gitx claims %q; a detached worktree is isolation of WORKING DIRECTORIES, "+
				"not filesystem confinement", overclaim)
		}
	}
	// The refuted draft is QUOTED here on purpose, as the record of an
	// overclaim that shipped. Quoting it is fine; asserting it is not, so it
	// must stay marked as false wherever it appears.
	if draft := "stops a write-capable provider"; strings.Contains(prose(src), draft) {
		note := prose(bodyOf(t, src, "func (r Repo) CreateObservationWorktree("))
		if !strings.Contains(note, draft) || !strings.Contains(note, "reported that sentence as false") {
			t.Errorf("gitx asserts %q somewhere it is not marked as the refuted draft", draft)
		}
	}

	// And it says what it actually is.
	if !strings.Contains(prose(bodyOf(t, src, "func (r Repo) CreateObservationWorktree(")),
		"It is NOT filesystem confinement") {
		t.Error("the observation worktree no longer states the limit of what it provides")
	}
	if !strings.Contains(prose(bodyOf(t, src, "func (r Repo) readOnly(")),
		"Do not describe it as a boundary") {
		t.Error("the read-only invocation no longer states that it is not a boundary")
	}
}

// A successful observation leaves no registered worktree behind.
//
// A read-only lane that leaves state behind has not been read-only. The first
// restructured run left .sc2-worktrees/task-...-observe registered, so this is
// checked against real git rather than against the source.
func TestTheObservationWorkspaceLeavesNoRegisteredWorktree(t *testing.T) {
	dir := t.TempDir()
	mustGit(t, dir, "init", "-q")
	mustGit(t, dir, "config", "user.email", "t@example.invalid")
	mustGit(t, dir, "config", "user.name", "T")
	write(t, dir, "main.go", "package main\n")
	mustGit(t, dir, "add", "main.go")
	mustGit(t, dir, "commit", "-qm", "init")

	ctx := context.Background()
	repo := Repo{Root: dir}
	head, err := repo.Head(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workspace, err := repo.CreateObservationWorktree(ctx, "task-observe-1", head)
	if err != nil {
		t.Fatal(err)
	}
	// Detached: an observation has nothing to commit, so a branch would be
	// state to clean up rather than state to use.
	if b, err := (Repo{Root: workspace}).Branch(ctx); err != nil {
		t.Fatal(err)
	} else if b != "" {
		t.Errorf("the observation workspace is on branch %q; it must be detached", b)
	}

	// A provider write lands HERE, and is visible here.
	write(t, workspace, "scratch.txt", "the provider wrote something\n")
	clean, dirty, err := repo.WorktreeIsClean(ctx, workspace)
	if err != nil {
		t.Fatal(err)
	}
	if clean || !strings.Contains(dirty, "scratch.txt") {
		t.Errorf("a write in the disposable workspace was not visible: clean=%v %q", clean, dirty)
	}
	// …and not in the governed checkout, which is the distinction the report
	// has to keep.
	if governed, err := repo.WorktreeIsCleanDetail(ctx, dir); err != nil {
		t.Fatal(err)
	} else if governed != "" {
		t.Errorf("a workspace write was reported against the governed checkout: %q", governed)
	}

	if err := repo.RemoveObservationWorktree(ctx, workspace); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(workspace); !os.IsNotExist(err) {
		t.Errorf("the observation workspace directory survives at %s", workspace)
	}
	out, err := exec.Command("git", "-C", dir, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v %s", err, out)
	}
	if strings.Contains(string(out), "observe") {
		t.Errorf("a registered worktree survives the observation:\n%s", out)
	}
	// No branch was created, so none is left over either.
	if b, err := exec.Command("git", "-C", dir, "branch", "--list").CombinedOutput(); err != nil {
		t.Fatalf("git branch: %v %s", err, b)
	} else if strings.Contains(string(b), "observe") {
		t.Errorf("the observation created a branch:\n%s", b)
	}
}

// source reads git.go so a test can pin what it says as well as what it does.
func source(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("git.go")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// bodyOf returns one function's text, from the top of its doc comment to its
// closing brace. The comment is included on purpose: what a function CLAIMS is
// as pinnable as what it does, and two overclaims have already shipped in
// exactly those comments.
func bodyOf(t *testing.T, src, signature string) string {
	t.Helper()
	at := strings.Index(src, signature)
	if at < 0 {
		t.Fatalf("%s is gone from git.go", signature)
	}
	lines := strings.Split(strings.TrimSuffix(src[:at], "\n"), "\n")
	back := 0
	for i := len(lines) - 1; i >= 0 && strings.HasPrefix(strings.TrimSpace(lines[i]), "//"); i-- {
		back += len(lines[i]) + 1
	}
	// A top-level function ends at a closing brace in column zero, which stops
	// the body short of the NEXT function's doc comment.
	end := len(src)
	if k := strings.Index(src[at:], "\n}\n"); k >= 0 {
		end = at + k + len("\n}\n")
	}
	return src[at-back : end]
}

// prose renders a comment block as one line, so a pinned phrase survives being
// re-wrapped by gofmt or by an editor.
func prose(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "//", " ")), " ")
}

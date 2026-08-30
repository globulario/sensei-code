package gitx

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestTheExactPathSetSurvivesAPathnameGitQuotes is the seam this slice pulled
// forward: the canonical tree stages exactly these paths, so a lossy set would
// build an identity that is exact about the wrong tree.
//
// `--numstat` without -z is trimmed and split on tabs, and a pathname holding a
// tab or a newline is quoted by Git -- so the old derivation lost it or split
// it in half. Nothing here trims, splits or unquotes.
func TestTheExactPathSetSurvivesAPathnameGitQuotes(t *testing.T) {
	r, base := repoWithBase(t)
	awkward := "a file\twith a tab.txt"
	if err := os.WriteFile(filepath.Join(r.Root, awkward), []byte("x\n"), 0o644); err != nil {
		t.Skipf("this filesystem will not hold %q: %v", awkward, err)
	}
	os.WriteFile(filepath.Join(r.Root, "plain.txt"), []byte("y\n"), 0o644)

	cap, err := r.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	var sawAwkward, sawPlain bool
	for _, p := range cap.Paths {
		switch p {
		case awkward:
			sawAwkward = true
		case "plain.txt":
			sawPlain = true
		}
		if strings.Contains(p, `\t`) || strings.HasPrefix(p, `"`) {
			t.Errorf("path %q arrived quoted or escaped; the set must be Git's own bytes", p)
		}
	}
	if !sawPlain {
		t.Fatalf("the ordinary path is missing from %q", cap.Paths)
	}
	if !sawAwkward {
		t.Fatalf("the path containing a tab did not survive: %q", cap.Paths)
	}
}

func TestARenameArrivesAsADeletionAndAnAddition(t *testing.T) {
	r, base := repoWithBase(t)
	if err := os.Rename(filepath.Join(r.Root, "main.go"), filepath.Join(r.Root, "renamed.go")); err != nil {
		t.Fatal(err)
	}
	cap, err := r.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(cap.Paths, " ")
	if !strings.Contains(got, "main.go") || !strings.Contains(got, "renamed.go") {
		t.Fatalf("a rename must appear as both paths, got %q", cap.Paths)
	}
}

// The canonical tree is the base tree plus exactly the reviewed paths: a
// worker's scratch file cannot ride along.
func TestTheCanonicalTreeHoldsExactlyTheReviewedPaths(t *testing.T) {
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	os.WriteFile(filepath.Join(r.Root, "scratch.txt"), []byte("a worker's note\n"), 0o644)

	tree, err := r.CanonicalTree(context.Background(), base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", r.Root, "ls-tree", "-r", "--name-only", tree).Output()
	if err != nil {
		t.Fatal(err)
	}
	listed := strings.Fields(string(out))
	for _, p := range listed {
		if p == "scratch.txt" {
			t.Fatal("a path the reviewer never saw entered the canonical tree")
		}
	}
	if len(listed) != 1 || listed[0] != "main.go" {
		t.Fatalf("tree holds %q, want exactly main.go", listed)
	}
	// And the live index is untouched: the tree was built elsewhere.
	if st, _ := exec.Command("git", "-C", r.Root, "diff", "--cached", "--name-only").Output(); len(strings.TrimSpace(string(st))) != 0 {
		t.Errorf("the canonical tree staged into the live index: %q", st)
	}
}

func TestADeletedPathIsRecordedAsADeletionNotRetainedFromTheBase(t *testing.T) {
	r, base := repoWithBase(t)
	if err := os.Remove(filepath.Join(r.Root, "main.go")); err != nil {
		t.Fatal(err)
	}
	tree, err := r.CanonicalTree(context.Background(), base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	out, _ := exec.Command("git", "-C", r.Root, "ls-tree", "-r", "--name-only", tree).Output()
	if strings.Contains(string(out), "main.go") {
		t.Fatal("a deleted path was retained from the base tree")
	}
}

// TestTheCanonicalCommitIsAFunctionOfItsInputs is the proof the whole design
// rests on: an author string can be forged, a reconstruction cannot.
func TestTheCanonicalCommitIsAFunctionOfItsInputs(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.CanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}

	// Hostile local differences must not reach the object.
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", r.Root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("config", "user.name", "Somebody Else")
	run("config", "user.email", "somebody@example.com")
	run("config", "commit.gpgsign", "false")
	t.Setenv("TZ", "Pacific/Kiritimati")
	t.Setenv("GIT_AUTHOR_NAME", "Injected")
	t.Setenv("GIT_AUTHOR_EMAIL", "injected@example.com")
	t.Setenv("GIT_AUTHOR_DATE", "2001-02-03T04:05:06 +0900")
	t.Setenv("GIT_COMMITTER_DATE", "2001-02-03T04:05:06 +0900")

	second, err := r.CanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("the identity moved with the environment: %s then %s", short12(first), short12(second))
	}
	if err := r.VerifyCanonicalCommit(ctx, base, tree, first); err != nil {
		t.Fatalf("reconstruction rejected the object it produced: %v", err)
	}
}

func TestTheCanonicalCommitsFirstParentIsExactlyTheBase(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.CanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	parent, err := r.FirstParentOf(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if parent != base {
		t.Fatalf("first parent = %s, want exactly the base %s", short12(parent), short12(base))
	}
	got, err := r.CommitTreeOf(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	if got != tree {
		t.Fatalf("tree = %s, want %s", short12(got), short12(tree))
	}
}

// A worker's own commits must not determine the identity: the canonical object
// is parented on the base, whatever history the worker left behind.
func TestWorkerHistoryDoesNotDetermineTheIdentity(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", r.Root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "w1")
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	run("add", "-A")
	run("commit", "-q", "-m", "w2")

	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	c, err := r.CanonicalCommit(ctx, base, tree)
	if err != nil {
		t.Fatal(err)
	}
	parent, _ := r.FirstParentOf(ctx, c)
	if parent != base {
		t.Fatalf("first parent = %s: worker history determined the identity", short12(parent))
	}
	head, _ := r.Head(ctx)
	if c == head {
		t.Fatal("the canonical identity is the worker's tip; it must be its own object")
	}
}

// A forged author string survives inspection. It does not survive
// reconstruction.
func TestAForgedIdentityDoesNotSurviveReconstruction(t *testing.T) {
	ctx := context.Background()
	r, base := repoWithBase(t)
	os.WriteFile(filepath.Join(r.Root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	tree, err := r.CanonicalTree(ctx, base, []string{"main.go"})
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "-C", r.Root, "commit-tree", tree, "-p", base, "-m", "sensei-code candidate identity")
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME="+CanonicalName, "GIT_AUTHOR_EMAIL="+CanonicalEmail,
		"GIT_COMMITTER_NAME="+CanonicalName, "GIT_COMMITTER_EMAIL="+CanonicalEmail,
		"GIT_AUTHOR_DATE=1700000000 +0000", "GIT_COMMITTER_DATE=1700000000 +0000")
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	forged := strings.TrimSpace(string(out))
	if err := r.VerifyCanonicalCommit(ctx, base, tree, forged); err == nil {
		t.Fatal("an object wearing the canonical authorship passed verification; authorship is not the proof")
	}
}

func TestACanonicalTreeRefusesAnEmptyCandidate(t *testing.T) {
	r, base := repoWithBase(t)
	if _, err := r.CanonicalTree(context.Background(), base, nil); err == nil {
		t.Fatal("an empty path set produced a tree; a run that changed nothing is not a candidate")
	}
}

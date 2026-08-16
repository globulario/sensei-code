package publish

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicationRefusedWithoutTheCapability(t *testing.T) {
	if _, err := Open(context.Background(), Request{Branch: "b", Workspace: "/w"}, false, true); err != ErrPushNotGranted {
		t.Fatalf("err = %v, want ErrPushNotGranted", err)
	}
	if _, err := Open(context.Background(), Request{Branch: "b", Workspace: "/w"}, true, false); err != ErrCommitNotGranted {
		t.Fatalf("err = %v, want ErrCommitNotGranted", err)
	}
}

func TestPullRequestArgsNeverMerge(t *testing.T) {
	args := (Request{Branch: "b", Title: "t", Base: "main", Report: "r"}).PullRequestArgs()
	if len(args) < 2 || args[0] != "pr" || args[1] != "create" {
		t.Fatalf("argv should only create a pull request: %v", args)
	}
	// Exact tokens, not a substring scan: the body legitimately says that
	// Sensei Code does not merge, and that sentence must not fail this check.
	forbidden := map[string]bool{
		"merge": true, "--auto": true, "--admin": true,
		"--squash": true, "--rebase": true, "--merge": true,
	}
	for _, arg := range args {
		if forbidden[arg] {
			t.Fatalf("gh argv contains the merge token %q: %v", arg, args)
		}
	}
}

func TestBodyCarriesTheReportAndRefusesToClaimAdmission(t *testing.T) {
	body := (Request{Report: "files: 1 added"}).Body()
	for _, want := range []string{"files: 1 added", "not** a Sensei admission", "does not merge"} {
		if !strings.Contains(body, want) {
			t.Fatalf("pull request body is missing %q:\n%s", want, body)
		}
	}
}

func TestPRURLIsExtractedRatherThanEchoed(t *testing.T) {
	out := "Warning: 3 uncommitted changes\nhttps://github.com/o/r/pull/42\n"
	if got := prURL(out); got != "https://github.com/o/r/pull/42" {
		t.Fatalf("prURL = %q, want just the pull request URL", got)
	}
}

// TestCommitAndPushReachARealRemote exercises the git half against a local
// remote, so the argv is known to work rather than merely well formed.
func TestCommitAndPushReachARealRemote(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	work := filepath.Join(root, "work")
	git := func(dir string, args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return string(out)
	}
	git(root, "init", "--bare", "-q", remote)
	git(root, "clone", "-q", remote, work)
	git(work, "config", "user.email", "t@example.invalid")
	git(work, "config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(work, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(work, "add", "seed.txt")
	git(work, "commit", "-q", "-m", "seed")
	git(work, "push", "-q", "origin", "HEAD:refs/heads/main")

	git(work, "checkout", "-q", "-b", "sensei-code/task-1/claude")
	if err := os.WriteFile(filepath.Join(work, "new.txt"), []byte("candidate\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, args := range CommitArgs("add a thing") {
		git(work, args...)
	}
	git(work, PushArgs("sensei-code/task-1/claude")...)

	branches := git(root, "--git-dir", remote, "branch", "--list")
	if !strings.Contains(branches, "sensei-code/task-1/claude") {
		t.Fatalf("the candidate branch did not reach the remote: %s", branches)
	}
}

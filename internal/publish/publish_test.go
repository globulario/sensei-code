package publish

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPublicationRefusedWithoutTheCapability(t *testing.T) {
	if _, err := Open(context.Background(), Request{Branch: "b", Workspace: "/w"}, false); err != ErrPushNotGranted {
		t.Fatalf("err = %v, want ErrPushNotGranted", err)
	}
	// The local_commit capability is no longer checked here: this package does
	// not commit. It is enforced where the act now happens, at the mint.
	if _, err := Open(context.Background(), Request{Branch: "b", Workspace: "/w", Paths: []string{"a.go"}}, true); !errors.Is(err, ErrNoCandidateCommit) {
		t.Fatalf("err = %v, want ErrNoCandidateCommit", err)
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

	// The #89 shape: a build artifact the worker left beside the reviewed
	// file. CandidateCapture excluded it from the candidate, so it was never
	// audited or reviewed; it is still sitting untracked in the worktree.
	if err := os.WriteFile(filepath.Join(work, "gosumcheck.bin"), []byte("\x7fELF\x00\x00artifact"), 0o755); err != nil {
		t.Fatal(err)
	}

	// The candidate's identity is minted BEFORE publication now, so the fixture
	// commits exactly the reviewed path the way the mint does -- scoped, never
	// sweeping. The scoping guarantee itself is tested in internal/gitx, where
	// the canonical tree is built.
	git(work, "add", "--", "new.txt")
	git(work, "commit", "--message", "add a thing")
	head := strings.TrimSpace(git(work, "rev-parse", "HEAD"))
	git(work, PushArgs(head, "sensei-code/task-1/claude")...)

	branches := git(root, "--git-dir", remote, "branch", "--list")
	if !strings.Contains(branches, "sensei-code/task-1/claude") {
		t.Fatalf("the candidate branch did not reach the remote: %s", branches)
	}
	published := git(root, "--git-dir", remote, "ls-tree", "--name-only", "-r", "sensei-code/task-1/claude")
	if !strings.Contains(published, "new.txt") {
		t.Fatalf("the reviewed file did not reach the remote: %s", published)
	}
	if strings.Contains(published, "gosumcheck.bin") {
		t.Fatalf("an artifact excluded from the candidate was published anyway: %s", published)
	}
}

// Publication with nothing judged publishes nothing. Sweeping the worktree
// instead is exactly how an excluded artifact reached a remote.
func TestPublicationRefusesToSweepTheWorktree(t *testing.T) {
	_, err := Open(context.Background(), Request{Workspace: t.TempDir(), Branch: "b", Title: "t"}, true)
	if !errors.Is(err, ErrNoReviewedPaths) {
		t.Fatalf("a publication with no reviewed paths was not refused: %v", err)
	}
	for _, args := range [][]string{{"add", "--", "a.go", "b.go"}, {"commit", "--message", "m"}} {
		for _, a := range args {
			if a == "--all" || a == "-A" || a == "." {
				t.Fatalf("commit args sweep the worktree: %v", args)
			}
		}
	}
}

// Finding 3 of the 2026-08-21 audit. prURL fell back to whatever gh printed,
// directly contradicting its own doc comment, and the caller renders the result
// as "pull request opened: "+url — so arbitrary output announced a pull request
// that may not exist.
func TestNoURLIsNotAPullRequest(t *testing.T) {
	for _, out := range []string{
		"",
		"Warning: 3 uncommitted changes",
		"https://github.com/o/r/issues/5",
		"created something somewhere",
	} {
		if got := prURL(out); got != "" {
			t.Errorf("prURL(%q) = %q, want empty: that string is not a pull request", out, got)
		}
	}
	const real = "Creating pull request\nhttps://github.com/globulario/sensei-code/pull/48\n"
	if got := prURL(real); got != "https://github.com/globulario/sensei-code/pull/48" {
		t.Errorf("prURL lost a real URL: %q", got)
	}
}

// A push that succeeded before gh failed has already changed the remote.
// Reporting only the error records the candidate as unpublished while origin
// disagrees.
func TestAPartialPublicationReportsWhatReachedTheRemote(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    Result
		want string
	}{
		// There is no "committed" stage any more: publication does not commit,
		// and reporting an act this package no longer performs would be false
		// provenance. Before a push, nothing has left the worktree.
		{"nothing happened", Result{}, ""},
		{"pushed but no PR", Result{Pushed: true}, "pushed to origin"},
		{"opened", Result{Pushed: true, URL: "https://x/pull/1"}, "https://x/pull/1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.r.Effects()
			if tc.want == "" {
				if got != "" {
					t.Fatalf("Effects() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("Effects() = %q, want it to mention %q", got, tc.want)
			}
		})
	}
	if (Result{Pushed: true}).Opened() {
		t.Error("a push with no URL reports as an opened pull request")
	}
	if !(Result{URL: "https://x/pull/1"}).Opened() {
		t.Error("a URL does not report as opened")
	}
}

// TestPublicationNeverCommits is the ownership boundary as a gate.
//
// The accepted candidate is given exactly ONE commit -- its canonical identity,
// minted before anything reports or publishes it. Two commit paths would
// eventually disagree about what was committed.
func TestPublicationNeverCommits(t *testing.T) {
	src, err := os.ReadFile("publish.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{`"commit"`, "commit-tree", "hash-object"} {
		if strings.Contains(string(src), forbidden) {
			t.Errorf("publish.go invokes %s: publication pushes the accepted object and never creates one", forbidden)
		}
	}
}

// Publication names the object it publishes, so an identity that does not exist
// cannot be published under a plausible-looking ref.
//
// This replaces an earlier test that expected a refusal when the local branch
// pointed somewhere else. That refusal is gone because the INFERENCE is gone:
// publication no longer consults a ref to decide what to push, so there is no
// ref/object disagreement left to detect. The property it protected is now held
// by TestPublicationPushesTheObjectNotTheRefThatNamedIt.
func TestPublicationCannotPublishAnObjectThatDoesNotExist(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "w")
	remote := filepath.Join(root, "remote.git")
	git := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	os.MkdirAll(work, 0o755)
	git(root, "init", "--bare", "-q", remote)
	git(work, "init", "-q")
	git(work, "config", "user.email", "t@t")
	git(work, "config", "user.name", "t")
	git(work, "remote", "add", "origin", remote)
	os.WriteFile(filepath.Join(work, "a.go"), []byte("package a\n"), 0o644)
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "one")

	_, err := Open(context.Background(), Request{
		Workspace: work, Branch: "b", Title: "t",
		Paths:           []string{"a.go"},
		CandidateCommit: strings.Repeat("0", 40),
	}, true)
	if err == nil {
		t.Fatal("a nonexistent identity was published")
	}
	if out := git(root, "--git-dir", remote, "branch", "--list"); strings.Contains(out, "b") {
		t.Fatalf("a branch reached the remote for an object that does not exist: %s", out)
	}
}

// TestPublicationPushesTheObjectNotTheRefThatNamedIt is the adversarial case.
//
// Verifying that a branch points at C and then pushing "that branch" proves one
// identity and publishes another. They are different values, and anything may
// move the ref in between. The refspec removes the inference.
func TestPublicationPushesTheObjectNotTheRefThatNamedIt(t *testing.T) {
	root := t.TempDir()
	work := filepath.Join(root, "w")
	remote := filepath.Join(root, "remote.git")
	git := func(dir string, args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	os.MkdirAll(work, 0o755)
	git(root, "init", "--bare", "-q", remote)
	git(work, "init", "-q")
	git(work, "config", "user.email", "t@t")
	git(work, "config", "user.name", "t")
	git(work, "remote", "add", "origin", remote)

	os.WriteFile(filepath.Join(work, "reviewed.txt"), []byte("the accepted candidate\n"), 0o644)
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "accepted")
	accepted := strings.TrimSpace(git(work, "rev-parse", "HEAD"))

	// A local branch of the same name pointing at DIFFERENT work.
	git(work, "checkout", "-q", "-b", "sensei-code/task-1/claude")
	os.WriteFile(filepath.Join(work, "unreviewed.txt"), []byte("nobody judged this\n"), 0o644)
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "not the candidate")
	other := strings.TrimSpace(git(work, "rev-parse", "HEAD"))
	if accepted == other {
		t.Fatal("the fixture did not produce two distinct objects")
	}

	if _, err := Open(context.Background(), Request{
		Workspace: work, Branch: "sensei-code/task-1/claude", Title: "t",
		Paths: []string{"reviewed.txt"}, CandidateCommit: accepted,
	}, true); err != nil && !strings.Contains(err.Error(), "pull request") {
		t.Fatalf("publication failed before reaching the remote: %v", err)
	}

	landed := strings.TrimSpace(git(root, "--git-dir", remote, "rev-parse", "refs/heads/sensei-code/task-1/claude"))
	if landed != accepted {
		t.Fatalf("the remote holds %s, want the accepted candidate %s", landed[:12], accepted[:12])
	}
	listed := git(root, "--git-dir", remote, "ls-tree", "--name-only", "-r", "refs/heads/sensei-code/task-1/claude")
	if strings.Contains(listed, "unreviewed.txt") {
		t.Fatal("work nobody reviewed reached the remote because the push followed a ref instead of the object")
	}
}

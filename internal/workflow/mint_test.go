package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/runreceipt"
)

func mintRepo(t *testing.T) (gitx.Repo, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644)
	run("add", ".")
	run("commit", "-q", "-m", "base")
	out, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	return gitx.Repo{Root: dir}, strings.TrimSpace(string(out))
}

func mintEngine(repo gitx.Repo, grantCommit bool) *Engine {
	e := &Engine{Repo: repo}
	e.Config.Permissions.LocalCommit = grantCommit
	return e
}

// The payoff: an accepted candidate acquires an exact identity, and the receipt
// can finally state what the run produced.
func TestAnAcceptedCandidateAcquiresAnExactIdentity(t *testing.T) {
	ctx := context.Background()
	repo, base := mintRepo(t)
	os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)

	cap, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := mintEngine(repo, true)
	e.beginReceipt("task-1")
	e.noteCapturedTree("task-1", cap.Tree)
	// A DELIVERED verdict is what makes a tree the reviewed one.
	e.noteReviewDelivered("task-1", "codex", "accept", "d1", cap.Tree)
	tc := &taskContext{Identity: candidateIdentityWithBase(base)}

	if err := e.mintCandidateIdentity(ctx, "task-1", tc, repo.Root, ""); err != nil {
		t.Fatalf("minting the accepted candidate failed: %v", err)
	}
	r := e.emitReceipt("task-1", "workflow.completed", runreceipt.OutcomeAccepted, runreceipt.CandidatePresent)
	if r.CandidateCommit.State != runreceipt.Known {
		t.Fatalf("candidate commit = %+v, want a measured identity", r.CandidateCommit)
	}
	if r.CandidateTree.Text != cap.Tree {
		t.Fatalf("candidate tree = %s, want the reviewed tree %s", r.CandidateTree.Text, cap.Tree)
	}
	if r.CandidateFirstParent.Text != base {
		t.Fatalf("first parent = %s, want the governed base %s", r.CandidateFirstParent.Text, base)
	}
	// And the object is reconstructible by anyone holding (base, tree).
	if err := repo.VerifyCanonicalCommit(ctx, base, cap.Tree, r.CandidateCommit.Text); err != nil {
		t.Fatalf("the recorded identity does not reconstruct: %v", err)
	}
}

// The capability check travels with the act. It used to live in publish.Open
// because publication was where a commit happened.
func TestMintingRefusesWhereLocalCommitIsNotGranted(t *testing.T) {
	ctx := context.Background()
	repo, base := mintRepo(t)
	os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	cap, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := mintEngine(repo, false)
	e.beginReceipt("task-1")
	e.noteCapturedTree("task-1", cap.Tree)
	// A DELIVERED verdict is what makes a tree the reviewed one.
	e.noteReviewDelivered("task-1", "codex", "accept", "d1", cap.Tree)
	tc := &taskContext{Identity: candidateIdentityWithBase(base)}

	err = e.mintCandidateIdentity(ctx, "task-1", tc, repo.Root, "")
	if err == nil {
		t.Fatal("a candidate was committed in a repository whose configuration forbids it")
	}
	if !strings.Contains(err.Error(), "local_commit") {
		t.Errorf("the refusal must name the capability, got %v", err)
	}
}

// A candidate that moved after its verdict is not the candidate that was
// judged, and it does not get an identity.
func TestACandidateThatMovedAfterReviewIsRefused(t *testing.T) {
	ctx := context.Background()
	repo, base := mintRepo(t)
	os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	cap, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := mintEngine(repo, true)
	e.beginReceipt("task-1")
	e.noteCapturedTree("task-1", cap.Tree)
	// A DELIVERED verdict is what makes a tree the reviewed one.
	e.noteReviewDelivered("task-1", "codex", "accept", "d1", cap.Tree)
	tc := &taskContext{Identity: candidateIdentityWithBase(base)}

	// Somebody edits the worktree between the verdict and the mint.
	os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte("package main\n\nfunc main() { println(2) }\n"), 0o644)

	err = e.mintCandidateIdentity(ctx, "task-1", tc, repo.Root, "")
	if err == nil {
		t.Fatal("a candidate that changed after its review was given the reviewed candidate's identity")
	}
	if !strings.Contains(err.Error(), "moved after it was reviewed") {
		t.Errorf("the refusal must say what happened, got %v", err)
	}
}

// Nothing to mint is a refusal, not a silent skip: a run cannot claim an
// identity for content nobody measured.
func TestMintingRefusesWithoutAReviewedTree(t *testing.T) {
	repo, base := mintRepo(t)
	e := mintEngine(repo, true)
	e.beginReceipt("task-1")
	tc := &taskContext{Identity: candidateIdentityWithBase(base)}
	if err := e.mintCandidateIdentity(context.Background(), "task-1", tc, repo.Root, ""); err == nil {
		t.Fatal("an identity was minted for a candidate with no measured content")
	}
}

func candidateIdentityWithBase(base string) candidate.Identity {
	return candidate.Identity{BaseSHA: base}
}

var _ = config.Agent{}

// A captured tree is not a reviewed one. Minting from the capture would give an
// identity to content no reviewer is on record as having judged.
func TestACapturedButUnreviewedTreeCannotBeMinted(t *testing.T) {
	ctx := context.Background()
	repo, base := mintRepo(t)
	os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	cap, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := mintEngine(repo, true)
	e.beginReceipt("task-1")
	e.noteCapturedTree("task-1", cap.Tree) // captured, never reviewed
	tc := &taskContext{Identity: candidateIdentityWithBase(base)}

	if err := e.mintCandidateIdentity(ctx, "task-1", tc, repo.Root, ""); err == nil {
		t.Fatal("a captured tree was minted without any delivered review")
	}
}

// The tree the mint uses is the one the VERDICT's envelope named, not the one
// the capture happened to freeze.
func TestTheMintUsesTheVerdictsTreeNotTheCaptures(t *testing.T) {
	ctx := context.Background()
	repo, base := mintRepo(t)
	os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	cap, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	e := mintEngine(repo, true)
	e.beginReceipt("task-1")
	e.noteCapturedTree("task-1", cap.Tree)
	// A verdict bound to a DIFFERENT tree must not be satisfied by the capture.
	e.noteReviewDelivered("task-1", "codex", "accept", "d1", strings.Repeat("9", 40))
	tc := &taskContext{Identity: candidateIdentityWithBase(base)}

	err = e.mintCandidateIdentity(ctx, "task-1", tc, repo.Root, "")
	if err == nil {
		t.Fatal("the mint fell back to the captured tree when the verdict named another")
	}
	if !strings.Contains(err.Error(), "moved after it was reviewed") {
		t.Errorf("got %v", err)
	}
}

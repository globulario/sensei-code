package workflow

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

// TestAnAcceptedRunProducesACompleteReceipt is F1, closed and held closed.
//
// F1 was the finding that made this whole slice: an accepted run reported
// CandidateState PRESENT, which requires the candidate's commit, tree and first
// parent, and the loop never committed its candidate -- so the main success
// path could not produce a complete account of itself. It can now, and this
// test fails if that stops being true.
//
// It exercises the real sequence against a real repository: capture freezes a
// tree, a delivered verdict binds to it, the mint gives it an exact identity,
// and the receipt states all of it.
//
// The governor's own identity is stubbed for one honest reason: a binary built
// from a MODIFIED tree refuses to name its commit, and a test binary carries no
// VCS stamp at all. A real COMPLETE therefore requires a clean build -- which
// is the behaviour we want, not a gap.
func TestAnAcceptedRunProducesACompleteReceipt(t *testing.T) {
	ctx := context.Background()
	repo, base := mintRepo(t)
	os.WriteFile(filepath.Join(repo.Root, "main.go"), []byte("package main\n\nfunc main() { println(1) }\n"), 0o644)
	cap, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	restore := governorIdentityFn
	governorIdentityFn = func() (runreceipt.Value, runreceipt.Value) {
		return runreceipt.MeasuredValue("3e5ade1391b4b754f0472bdcde6877e6db699d19", "runtime/debug build info vcs.revision"),
			runreceipt.MeasuredValue(strings.Repeat("a", 64), "sha256 of the executable this process is running")
	}
	defer func() { governorIdentityFn = restore }()

	e := mintEngine(repo, true)
	e.beginReceipt("task-1")
	e.noteServingProducer("task-1", os.Getpid(), true)
	e.noteWorld("task-1", base, "42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64")
	e.notePlan("task-1", "", "a plan the architect wrote")
	e.noteCandidateWork("task-1", "some-tree", "a-different-base-tree")
	e.noteCapturedTree("task-1", cap.Tree)
	// The REAL rendering digest, so the canonical relation is measured rather
	// than staged: a fabricated digest would make this DIFFER, which is exactly
	// what the relation is for.
	reviewed := candidateRevision(cap.Diff)
	e.noteCandidateDigest("task-1", reviewed)
	e.noteReviewerAssigned("task-1", "codex")
	e.noteReviewDelivered("task-1", "codex", "accept", reviewed, cap.Tree)

	tc := &taskContext{Identity: candidateIdentityWithBase(base)}
	if err := e.mintCandidateIdentity(ctx, "task-1", tc, repo.Root, ""); err != nil {
		t.Fatalf("mint: %v", err)
	}
	r := e.emitReceipt("task-1", "workflow.completed", e.reviewedOutcome("task-1"), e.candidateStateFor("task-1"))
	state, missing := r.Completeness()
	if state != runreceipt.Complete {
		t.Fatalf("the accepted path cannot state what it produced: %v", missing)
	}
	if r.Outcome != runreceipt.OutcomeAccepted || r.CandidateState != runreceipt.CandidatePresent {
		t.Fatalf("outcome=%s candidate=%s", r.Outcome, r.CandidateState)
	}
	// The identity in the receipt reconstructs from (base, reviewed tree): the
	// record is checkable by anyone holding those two, with no signature and no
	// trust in this machine.
	if err := repo.VerifyCanonicalCommit(ctx, base, r.CandidateTree.Text, r.CandidateCommit.Text); err != nil {
		t.Fatalf("the receipt names an identity that does not reconstruct: %v", err)
	}
	if r.CandidateFirstParent.Text != base {
		t.Fatalf("first parent = %s, want the governed base", r.CandidateFirstParent.Text)
	}
	// The canonical rendering of the minted object equals the one the review
	// was given -- measured, through the same renderer.
	if r.CandidateDigestRelation != runreceipt.RelationMatch {
		t.Fatalf("digest relation = %s, want MATCH", r.CandidateDigestRelation)
	}
	if r.ReviewedTree.Text != r.CandidateTree.Text {
		t.Fatalf("reviewed tree %s != candidate tree %s", r.ReviewedTree.Text, r.CandidateTree.Text)
	}
}

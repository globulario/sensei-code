package workflow

// Giving the accepted candidate a name.
//
// The mint has no semantic freedom left by the time it runs. The reviewer
// already bound the content as a tree; this wraps that frozen tree in the one
// deterministic Git object it corresponds to, and measures that it did.
//
//	T = WHAT was reviewed
//	D = HOW T was presented to the reviewer
//	C = the deterministic Git identity wrapping T
//
// Creating C confers identity and nothing else. It is not a publication, and it
// is not an admission.

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/gitx"
)

// errNoLocalCommit refuses to mint where configuration forbids committing.
//
// The capability check travels with the ACT, not with the package that used to
// perform it: it lived in publish.Open because publication was where a commit
// happened. Minting earlier without moving the check would let the engine
// commit in a repository whose configuration says it may not.
var errNoLocalCommit = errors.New(
	"local_commit is not granted in .sensei-code/config.json, so the accepted candidate cannot be given a commit identity")

// mintCandidateIdentity turns the accepted candidate into one exact object.
//
// It re-measures first. Acceptance and minting are separated by the publication
// rendezvous and by whatever the human did in between, so the tree is rebuilt
// from the worktree and required to equal the tree the review was bound to. A
// candidate that moved after its verdict is not the candidate that was judged.
func (e *Engine) mintCandidateIdentity(ctx context.Context, taskID string, tc *taskContext, workspace, branch string) error {
	bound, ok := e.reviewedTreeFor(taskID)
	if !ok {
		return errors.New("no reviewed content identity was measured for this candidate, so there is nothing to mint")
	}
	base := strings.TrimSpace(tc.Identity.BaseSHA)
	if base == "" {
		return errors.New("the candidate has no recorded base, so no identity can be parented on it")
	}
	repo := gitx.Repo{Root: workspace}

	// Re-measure: T2 must equal T.
	recap, err := repo.CandidateCapture(ctx, base, tc.Files)
	if err != nil {
		return fmt.Errorf("re-measure the accepted candidate: %w", err)
	}
	if recap.Tree != bound {
		return fmt.Errorf(
			"the candidate moved after it was reviewed: the verdict was bound to tree %s and the worktree now holds %s",
			shortDigest(bound), shortDigest(recap.Tree))
	}

	if !e.Config.Permissions.LocalCommit {
		return errNoLocalCommit
	}

	commit, err := repo.MintCanonicalCommit(ctx, base, bound)
	if err != nil {
		return err
	}
	// Measure what was minted rather than assume it. Every one of these is
	// re-derivable by anyone later, which is the point.
	if err := repo.VerifyCanonicalCommit(ctx, base, bound, commit); err != nil {
		return err
	}
	tree, err := repo.CommitTreeOf(ctx, commit)
	if err != nil {
		return err
	}
	if tree != bound {
		return fmt.Errorf("the minted identity holds tree %s, not the reviewed tree %s", shortDigest(tree), shortDigest(bound))
	}
	parent, err := repo.FirstParentOf(ctx, commit)
	if err != nil {
		return err
	}
	if parent != base {
		return fmt.Errorf("the minted identity is parented on %s, not on the governed base %s", shortDigest(parent), shortDigest(base))
	}

	// The candidate branch names the object. This is LOCAL CANDIDATE CUSTODY:
	// it dies with the candidate, and it is not an admission.
	if strings.TrimSpace(branch) != "" {
		if err := repo.PointBranchAt(ctx, branch, commit); err != nil {
			return fmt.Errorf("point the candidate branch at its identity: %w", err)
		}
	}
	e.noteCandidateCommit(taskID, commit, tree, parent)
	return nil
}

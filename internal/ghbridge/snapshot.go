package ghbridge

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// Publishing a review snapshot makes an exact candidate readable through
// GitHub. It is a PROJECTION of the workflow binding, never a second
// measurement of the worktree.
//
// The commit is built from the already-captured tree with `git commit-tree`, so
// nothing re-stages files, re-reads the working directory, or derives another
// diff. A snapshot assembled by `git add` would be a NEW observation, and if
// the worktree had moved since the candidate was captured the reviewer would be
// shown something the workflow never bound to — the review would be of an
// artifact that exists nowhere in the task's history.
//
// It is publication for inspection only: not admission, not acceptance, not
// merge authority, not candidate minting.

func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// Snapshot is the published projection.
type Snapshot struct {
	Commit string
	Ref    string
}

// PublishSnapshot materializes tree/parent as a commit, proves it is what was
// asked for, and pushes it under a dedicated review ref.
//
// The verification is not ceremony: commit-tree will happily build a commit
// from any tree it is handed, so the only thing that makes this commit a
// projection of T rather than of something else is reading its tree and parent
// back out and comparing them.
func PublishSnapshot(ctx context.Context, dir, remote string, s Subject, requestID string) (Snapshot, error) {
	if !fullSHA.MatchString(s.CandidateTree) {
		return Snapshot{}, fmt.Errorf("cannot publish: candidate_tree is not a full tree id: %q", s.CandidateTree)
	}
	if !fullSHA.MatchString(s.BaseSHA) {
		return Snapshot{}, fmt.Errorf("cannot publish: base is not a full commit: %q", s.BaseSHA)
	}

	msg := fmt.Sprintf("sensei-code review snapshot\n\ntask=%s\nrequest=%s\ncandidate_digest=%s\n\n"+
		"Published for inspection only. Not admission, acceptance, merge authority or candidate minting.",
		s.TaskID, requestID, s.CandidateDigest)

	commit, err := git(ctx, dir, "commit-tree", s.CandidateTree, "-p", s.BaseSHA, "-m", msg)
	if err != nil {
		return Snapshot{}, fmt.Errorf("commit-tree: %w: %s", err, commit)
	}
	commit = strings.TrimSpace(commit)
	if !fullSHA.MatchString(commit) {
		return Snapshot{}, fmt.Errorf("commit-tree returned %q, which is not a commit id", commit)
	}

	if err := VerifySnapshot(ctx, dir, commit, s); err != nil {
		return Snapshot{}, err
	}

	ref := fmt.Sprintf("refs/sensei-code/review/%s/%s", s.TaskID, requestID)
	if out, perr := git(ctx, dir, "push", remote, commit+":"+ref); perr != nil {
		return Snapshot{}, fmt.Errorf("pushing the review snapshot: %w: %s", perr, out)
	}
	return Snapshot{Commit: commit, Ref: ref}, nil
}

// VerifySnapshot proves a commit is the projection it claims to be.
//
// Separate and exported so the property can be asserted independently of who
// built the commit — including against a snapshot somebody else pushed.
func VerifySnapshot(ctx context.Context, dir, commit string, s Subject) error {
	tree, err := git(ctx, dir, "rev-parse", commit+"^{tree}")
	if err != nil {
		return fmt.Errorf("reading the snapshot tree: %w: %s", err, tree)
	}
	if strings.TrimSpace(tree) != s.CandidateTree {
		return fmt.Errorf("review snapshot tree %s is not the candidate tree %s — the reviewer would be shown a different artifact",
			strings.TrimSpace(tree), s.CandidateTree)
	}
	parent, err := git(ctx, dir, "rev-parse", commit+"^")
	if err != nil {
		return fmt.Errorf("reading the snapshot parent: %w: %s", err, parent)
	}
	if strings.TrimSpace(parent) != s.BaseSHA {
		return fmt.Errorf("review snapshot parent %s is not the base %s — the diff shown would not be the candidate's",
			strings.TrimSpace(parent), s.BaseSHA)
	}
	return nil
}

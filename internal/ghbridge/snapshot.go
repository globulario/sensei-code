package ghbridge

import (
	"bytes"
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

// git runs a git command and returns ONLY what it wrote to stdout.
//
// The streams must stay separate because the values read here are object ids
// that get compared for identity. git writes advisories to stderr — a stale
// index notice, a config hint, an ambiguous-refname warning — and merging those
// into the value makes an id that is correct read as an id that is wrong. The
// mismatch would then be reported against the projection instead of against the
// reading that polluted it.
//
// A failure still says exactly what git said: stderr is wrapped into the error
// here, in the same "<exit error>: <what git wrote>" shape the callers used to
// build themselves, so their messages are unchanged. Callers therefore report
// the error alone — appending the stdout value as well would either duplicate a
// separator or, on a failed read, quote text that is not an id. Nothing is lost
// by dropping it: git echoes an unresolvable argument to stdout, and its own
// fatal on stderr quotes that same argument back.
func git(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		err = fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), err
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
		return Snapshot{}, fmt.Errorf("commit-tree: %w", err)
	}
	commit = strings.TrimSpace(commit)
	if !fullSHA.MatchString(commit) {
		return Snapshot{}, fmt.Errorf("commit-tree returned %q, which is not a commit id", commit)
	}

	if err := VerifySnapshot(ctx, dir, commit, s); err != nil {
		return Snapshot{}, err
	}

	ref := fmt.Sprintf("refs/sensei-code/review/%s/%s", s.TaskID, requestID)
	if _, perr := git(ctx, dir, "push", remote, commit+":"+ref); perr != nil {
		return Snapshot{}, fmt.Errorf("pushing the review snapshot: %w", perr)
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
		return fmt.Errorf("reading the snapshot tree: %w", err)
	}
	if strings.TrimSpace(tree) != s.CandidateTree {
		return fmt.Errorf("review snapshot tree %s is not the candidate tree %s — the reviewer would be shown a different artifact",
			strings.TrimSpace(tree), s.CandidateTree)
	}
	parent, err := git(ctx, dir, "rev-parse", commit+"^")
	if err != nil {
		return fmt.Errorf("reading the snapshot parent: %w", err)
	}
	if strings.TrimSpace(parent) != s.BaseSHA {
		return fmt.Errorf("review snapshot parent %s is not the base %s — the diff shown would not be the candidate's",
			strings.TrimSpace(parent), s.BaseSHA)
	}
	return nil
}

// RemoteRepository reads "owner/name" from a git remote URL.
//
// The workspace repository is a fact about the checkout that produced the
// candidate, so it is read from the remote the snapshot is pushed to rather
// than from configuration that could name a different repository than the one
// the objects actually reach.
//
// Returns "" when the remote is missing or its URL is not a recognisable
// GitHub path. Empty is honest: the marker then omits the field and a consumer
// fails closed on a missing binding, which is far better than a guess that
// sends it to the wrong repository.
func RemoteRepository(ctx context.Context, dir, remote string) string {
	if strings.TrimSpace(dir) == "" || strings.TrimSpace(remote) == "" {
		return ""
	}
	url, err := git(ctx, dir, "remote", "get-url", strings.TrimSpace(remote))
	if err != nil {
		return ""
	}
	url = strings.TrimSuffix(strings.TrimSpace(url), ".git")
	if i := strings.Index(url, "github.com"); i >= 0 {
		url = url[i+len("github.com"):]
	}
	url = strings.TrimLeft(url, ":/")
	parts := strings.Split(url, "/")
	if len(parts) < 2 {
		return ""
	}
	owner, name := parts[len(parts)-2], parts[len(parts)-1]
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
}

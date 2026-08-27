package gitx

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

type Repo struct{ Root string }

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// noOptionalLocks is git's flag for "answer this query without taking the
// locks a query would otherwise take". Named once so a test can pin that the
// read-only inspections still carry it.
const noOptionalLocks = "--no-optional-locks"

func Discover(ctx context.Context, start string) (Repo, error) {
	// Read-only, and it runs before any Repo exists, so the flag is spelled out
	// here rather than routed through readOnly.
	cmd := exec.CommandContext(ctx, "git", "-C", start, noOptionalLocks, "rev-parse", "--show-toplevel")
	b, err := cmd.Output()
	if err != nil {
		return Repo{}, fmt.Errorf("not a git repository: %w", err)
	}
	return Repo{Root: strings.TrimSpace(string(b))}, nil
}

func (r Repo) Head(ctx context.Context) (string, error) {
	return r.readOnlyOutput(ctx, "rev-parse", "HEAD")
}
func (r Repo) Branch(ctx context.Context) (string, error) {
	return r.readOnlyOutput(ctx, "branch", "--show-current")
}

// Diff returns the working-tree diff verbatim.
//
// It uses raw() rather than output() because a diff must not be trimmed. Every
// other git call here is a short single value where stripping whitespace is
// convenient; a unified diff is a format, and its trailing newline is part of
// it. Trimming the end of a patch whose final added lines are blank leaves a
// hunk header promising more lines than the body contains, and Sensei rejects
// the result as malformed_diff -- which is what made every audit in a real run
// come back cannot_verify, for weeks of apparent graph trouble that was
// actually two missing newlines.
func (r Repo) Diff(ctx context.Context, base string) (string, error) {
	args := []string{"diff", "--no-ext-diff", "--binary"}
	if b := strings.TrimSpace(base); b != "" {
		args = append(args, b)
	}
	return r.raw(ctx, args...)
}

// CandidateDiff is the whole of a candidate's work, including files it created.
//
// Plain `git diff` reports tracked modifications only, so a worker that added a
// file produced a diff that did not contain it. The reviewer then judged a
// candidate with its new files invisible -- rejecting real work for omitting a
// test that was sitting untracked on disk -- and no candidate could ever
// converge. Intent-to-add records the new paths so they appear in the diff,
// without staging their content; .gitignore is still honoured.
// CandidateDiff is everything the candidate changed relative to the base it was
// cut from, whether the worker committed it or not.
//
// Diffing the working tree alone was wrong in a way that only appeared once a
// worker used a capability it had been granted: local_commit is permitted, and
// a worker that commits its work leaves a clean tree, so the candidate diff
// came back empty and the run reported that the implementor had produced
// nothing. It had produced everything and then tidied up.
//
// Diffing against the recorded base makes committed and uncommitted work look
// the same to the reviewer, which is the only reading that matches what the
// candidate actually contains.
func (r Repo) CandidateDiff(ctx context.Context, base string) (string, error) {
	// Through the boundary, with no intended outputs: every new binary or
	// oversized file is an artifact here. Callers that know the plan use
	// CandidateCapture directly and receive what was excluded.
	cap, err := r.CandidateCapture(ctx, base, nil)
	if err != nil {
		return "", err
	}
	return cap.Diff, nil
}

// RevList returns commit ids in a range, newest first, bounded by limit.
//
// Used only by the counterfactual scanner, which replays history through the
// routine classifier. It reads history and never rewrites it.
func (r Repo) RevList(ctx context.Context, spec string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 20
	}
	out, err := r.output(ctx, "rev-list", fmt.Sprintf("--max-count=%d", limit), spec)
	if err != nil {
		return nil, err
	}
	var commits []string
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			commits = append(commits, line)
		}
	}
	return commits, nil
}

// CommitDiff is the patch one commit introduced, against its first parent.
func (r Repo) CommitDiff(ctx context.Context, commit string) (string, error) {
	return r.output(ctx, "diff", commit+"^!", "--")
}

func (r Repo) IsClean(ctx context.Context) (bool, error) {
	s, err := r.readOnlyOutput(ctx, "status", "--porcelain")
	return s == "", err
}

// Worktrees deliberately live next to the repository, not inside it. A Git
// worktree is an execution boundary and must never be confused with local
// Sensei Code session state under <repo>/.sensei-code.
//
// A candidate belongs to the task, not to the worker that happens to be
// carrying it. One worktree per worker meant a worker that ran out of review
// cycles took its work with it and the next one started from an empty
// checkout, discarding every fix the reviewer had already extracted.
func (r Repo) WorktreePath(taskID string) string {
	// "<repo>" not "<repo>.sensei-code": the suffix already names the tool, so
	// repeating the repository name produced ".sensei-code.sensei-code-worktrees".
	base := "." + filepath.Base(r.Root) + "-worktrees"
	return filepath.Join(filepath.Dir(r.Root), base, clean(taskID))
}

// WorktreeBranch names the branch a candidate is built on. Publication needs
// the same name the worktree was created with, so it is derived once here
// rather than reconstructed by each caller.
func (r Repo) WorktreeBranch(taskID string) string {
	return "sensei-code/" + clean(taskID)
}

// CreateWorktree returns the task's candidate worktree, creating it on first
// use and reusing it afterwards so a second worker continues the same work.
func (r Repo) CreateWorktree(ctx context.Context, taskID string) (string, error) {
	path := r.WorktreePath(taskID)
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	branch := r.WorktreeBranch(taskID)
	return path, r.addWorktree(ctx, path, branch, "HEAD")
}

// CreateWorktreeAt cuts a candidate from an exact commit rather than from
// whatever HEAD happens to mean at this instant.
//
// The distinction matters on re-entry: a task interrupted and resumed later
// must continue from the base its plan was approved against, and "HEAD" is not
// that base once anything else has been committed.
func (r Repo) CreateWorktreeAt(ctx context.Context, taskID, baseSHA string) (string, error) {
	base := strings.TrimSpace(baseSHA)
	if base == "" {
		return "", fmt.Errorf("refusing to create a candidate worktree without an explicit base commit")
	}
	path := r.WorktreePath(taskID)
	if _, err := os.Stat(filepath.Join(path, ".git")); err == nil {
		return path, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	return path, r.addWorktree(ctx, path, r.WorktreeBranch(taskID), base)
}

// CreateObservationWorktree checks out a DETACHED copy of one commit for a
// read-only run.
//
// Detached, and deliberately so: an observation has nothing to commit, so a
// branch would be state to clean up rather than state to use.
//
// What this buys, precisely: the observer's working directory is somewhere the
// governed checkout is not, so an ordinary edit lands here instead of there. It
// is NOT filesystem confinement. The subprocess keeps the ambient permissions
// of the user running Sensei Code and can address any path it likes, including
// the governed root.
//
// The first draft of this comment said a separate worktree "stops a
// write-capable provider from touching" the governed checkout. The observation
// lane audited this file and reported that sentence as false, which it was --
// the same overclaim the reviewer had just rejected in the post-hoc
// cleanliness check, rewritten one layer down.
//
// Removing it is the caller's job, and RemoveObservationWorktree does not need
// a branch deleted afterwards.
func (r Repo) CreateObservationWorktree(ctx context.Context, taskID, baseSHA string) (string, error) {
	base := strings.TrimSpace(baseSHA)
	if base == "" {
		return "", fmt.Errorf("refusing to create an observation worktree without an explicit base commit")
	}
	path := r.WorktreePath(taskID) + "-observe"
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", err
	}
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "worktree", "add", "--detach", path, base)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add --detach: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return path, nil
}

// WorktreeIsClean reports whether a worktree has no tracked or untracked
// changes, and names them when it does.
//
// Used on the observation workspace, where a write is harmless because the
// workspace is discarded, as a SIGNAL that a configured provider is not as
// read-only as the lane assumes.
//
// It carries exactly the weakness that disqualified this check as a safety
// mechanism on the governed repository: a commit, a write to an ignored path,
// or a write followed by a restore all leave status clean. That is tolerable
// here and was not there, because nothing rests on the answer -- a positive
// result is informative and a negative one proves nothing. Do not promote this
// to a boundary.
func (r Repo) WorktreeIsClean(ctx context.Context, path string) (bool, string, error) {
	b, err := r.readOnly(ctx, path, "status", "--porcelain")
	if err != nil {
		return false, "", err
	}
	out := strings.TrimSpace(string(b))
	return out == "", out, nil
}

func (r Repo) addWorktree(ctx context.Context, path, branch, base string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "worktree", "add", "-b", branch, path, base)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// DeleteBranch removes a candidate branch. It is deliberately the forceful
// form: a candidate branch is never merged anywhere by this program, so git's
// "not fully merged" refusal would block every legitimate disposal. What makes
// that safe is not the flag but the caller — a branch is only ever deleted
// after its evidence has been recorded.
func (r Repo) DeleteBranch(ctx context.Context, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("no branch named to delete")
	}
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "branch", "-D", name)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git branch -D %s: %w: %s", name, err, strings.TrimSpace(string(b)))
	}
	return nil
}

// WorktreeIsCleanDetail returns a worktree's porcelain status verbatim.
//
// Separate from WorktreeIsClean because the observation lane needs to COMPARE
// two moments rather than test one: "clean" cannot distinguish a change this
// run made from one that was already present, and treating an already-dirty
// repository as evidence of mutation is a false accusation that fires exactly
// when someone audits a work-in-progress.
func (r Repo) WorktreeIsCleanDetail(ctx context.Context, path string) (string, error) {
	b, err := r.readOnly(ctx, path, "status", "--porcelain")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// RemoveObservationWorktree discards a detached observation workspace. No
// branch is deleted, because none was created.
func (r Repo) RemoveObservationWorktree(ctx context.Context, path string) error {
	return r.RemoveWorktree(ctx, path)
}

func (r Repo) RemoveWorktree(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "worktree", "remove", "--force", path)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
}

// readOnly runs a git command that INSPECTS a checkout, taking no optional
// lock while it does.
//
// `--no-optional-locks` is git's own switch for exactly this. Without it a
// plain `git status` may refresh and rewrite the index while answering, which
// is a write to the repository being measured -- so the read-only lane's own
// cleanliness check was capable of modifying the thing it exists to prove
// unmodified. A checker for a read-only lane must not become the write it is
// trying to detect.
//
// What this narrows is precise and small: THIS PROGRAM'S measurement of a
// checkout does not mutate it. It is not filesystem confinement, not a
// sandbox, and says nothing about what a subprocess may reach. Do not describe
// it as a boundary.
//
// Reserved for genuine reads. `worktree add`, `worktree remove` and
// `branch -D` are writes and are left alone: suppressing the locks a write
// needs would be a correctness bug, not a narrowing.
func (r Repo) readOnly(ctx context.Context, dir string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", dir, noOptionalLocks}, args...)...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s in %s: %w: %s", strings.Join(args, " "), dir, err, strings.TrimSpace(string(b)))
	}
	return b, nil
}

// readOnlyOutput is readOnly for a short single value, trimmed like output().
func (r Repo) readOnlyOutput(ctx context.Context, args ...string) (string, error) {
	b, err := r.readOnly(ctx, r.Root, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// raw runs git and returns stdout exactly as produced. Use it for anything
// whose format matters; output() is for short values where trimming helps.
func (r Repo) raw(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

func (r Repo) output(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", r.Root}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return strings.TrimSpace(stdout.String()), nil
}

func clean(s string) string {
	s = safeName.ReplaceAllString(strings.TrimSpace(s), "-")
	s = strings.Trim(s, "-.")
	if s == "" {
		return "unnamed"
	}
	return s
}

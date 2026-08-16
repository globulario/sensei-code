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

func Discover(ctx context.Context, start string) (Repo, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", start, "rev-parse", "--show-toplevel")
	b, err := cmd.Output()
	if err != nil {
		return Repo{}, fmt.Errorf("not a git repository: %w", err)
	}
	return Repo{Root: strings.TrimSpace(string(b))}, nil
}

func (r Repo) Head(ctx context.Context) (string, error) { return r.output(ctx, "rev-parse", "HEAD") }
func (r Repo) Branch(ctx context.Context) (string, error) {
	return r.output(ctx, "branch", "--show-current")
}
func (r Repo) Diff(ctx context.Context) (string, error) {
	return r.output(ctx, "diff", "--no-ext-diff", "--binary")
}

// CandidateDiff is the whole of a candidate's work, including files it created.
//
// Plain `git diff` reports tracked modifications only, so a worker that added a
// file produced a diff that did not contain it. The reviewer then judged a
// candidate with its new files invisible -- rejecting real work for omitting a
// test that was sitting untracked on disk -- and no candidate could ever
// converge. Intent-to-add records the new paths so they appear in the diff,
// without staging their content; .gitignore is still honoured.
func (r Repo) CandidateDiff(ctx context.Context) (string, error) {
	if _, err := r.output(ctx, "add", "--intent-to-add", "--", "."); err != nil {
		return "", err
	}
	return r.Diff(ctx)
}
func (r Repo) IsClean(ctx context.Context) (bool, error) {
	s, err := r.output(ctx, "status", "--porcelain")
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
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "worktree", "add", "-b", branch, path, "HEAD")
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("git worktree add: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return path, nil
}

func (r Repo) RemoveWorktree(ctx context.Context, path string) error {
	cmd := exec.CommandContext(ctx, "git", "-C", r.Root, "worktree", "remove", "--force", path)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return nil
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

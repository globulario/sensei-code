// Package publish opens a pull request for an accepted candidate.
//
// Publication is human-owned. This package can push a candidate branch and open
// a pull request; it can never merge one. Sensei's own merge-check is documented
// as verifying that a pull request is merge-authorized and never merging it, and
// Sensei Code holds the same line: a reviewed candidate is a proposal, and the
// decision to land it belongs to a person.
//
// A pull request is also not admission. The body says so in the request itself,
// so the claim travels with the change instead of living only in a terminal
// somebody has already closed.
package publish

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// Request is one candidate offered for publication.
type Request struct {
	// Workspace is the candidate worktree holding the work.
	Workspace string
	// Branch is the candidate branch to publish.
	Branch string
	// Base is the branch the pull request targets.
	Base string
	// Title and Report become the pull request's title and body. Report is the
	// evidence-backed change report, including what it does not establish.
	Title  string
	Report string
}

// ErrPushNotGranted reports that publication was attempted without the
// capability. It is a refusal, not a failure: the configuration says this
// repository may not be pushed to.
var ErrPushNotGranted = errors.New("push is not granted in .sensei-code/config.json, so no pull request was opened")

// ErrCommitNotGranted reports that the candidate cannot be committed, which a
// pull request requires.
var ErrCommitNotGranted = errors.New("local_commit is not granted in .sensei-code/config.json, so the candidate cannot be committed")

// Body renders the pull request description. The change report is quoted
// verbatim and the governance position is stated plainly, because a reader of
// the pull request has none of the terminal context.
func (r Request) Body() string {
	var b strings.Builder
	b.WriteString(r.Report)
	b.WriteString("\n\n---\n\n")
	b.WriteString("Opened by Sensei Code from a reviewed candidate.\n\n")
	b.WriteString("- This pull request is **not** a Sensei admission. Admission was not requested.\n")
	b.WriteString("- Reviewer acceptance is not correctness certification.\n")
	b.WriteString("- Sensei Code does not merge. Landing this change is a human decision.\n")
	return b.String()
}

// CommitArgs is the argv that commits the candidate's work.
func CommitArgs(message string) [][]string {
	return [][]string{
		{"add", "--all"},
		{"commit", "--message", message},
	}
}

// PushArgs is the argv that publishes the candidate branch.
func PushArgs(branch string) []string {
	return []string{"push", "--set-upstream", "origin", branch}
}

// PullRequestArgs is the argv handed to gh. It never contains a merge verb.
func (r Request) PullRequestArgs() []string {
	args := []string{"pr", "create", "--head", r.Branch, "--title", r.Title, "--body", r.Body()}
	if strings.TrimSpace(r.Base) != "" {
		args = append(args, "--base", r.Base)
	}
	return args
}

// Open commits the candidate, publishes its branch, and opens a pull request,
// returning the URL. The caller is responsible for having obtained the human's
// decision; this only enforces the configured capabilities.
func Open(ctx context.Context, r Request, pushGranted, commitGranted bool) (string, error) {
	if !pushGranted {
		return "", ErrPushNotGranted
	}
	if !commitGranted {
		return "", ErrCommitNotGranted
	}
	if strings.TrimSpace(r.Branch) == "" || strings.TrimSpace(r.Workspace) == "" {
		return "", errors.New("publication needs a candidate branch and workspace")
	}
	for _, args := range CommitArgs(r.Title) {
		if out, err := run(ctx, r.Workspace, "git", args); err != nil {
			return "", fmt.Errorf("git %s: %s", args[0], out)
		}
	}
	if out, err := run(ctx, r.Workspace, "git", PushArgs(r.Branch)); err != nil {
		return "", fmt.Errorf("push the candidate branch: %s", out)
	}
	out, err := run(ctx, r.Workspace, "gh", r.PullRequestArgs())
	if err != nil {
		return "", fmt.Errorf("open the pull request: %s", out)
	}
	return prURL(out), nil
}

// prURL picks the pull request URL out of gh's output rather than reporting
// whatever it printed, so a caller never shows a success line that contains no
// pull request.
func prURL(out string) string {
	for _, field := range strings.Fields(out) {
		if strings.HasPrefix(field, "https://") && strings.Contains(field, "/pull/") {
			return field
		}
	}
	return strings.TrimSpace(out)
}

func run(ctx context.Context, dir, name string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

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
	// Paths are the repository-relative files the reviewed candidate changed,
	// and they are the only thing publication stages. Publication used to run
	// `git add --all`, which re-swept the worktree: a build artifact that
	// CandidateCapture had excluded from the candidate -- and that therefore
	// never appeared in the audit or the reviewer's packet -- was still
	// untracked in the worktree, and --all committed and pushed it (#89). What
	// is published is exactly what was judged, or nothing.
	Paths []string
	// CandidateCommit is the accepted candidate's canonical identity. It is
	// minted before publication is ever offered; this package pushes it and
	// never creates one.
	CandidateCommit string
}

// ErrNoReviewedPaths reports a publication with nothing judged to publish.
// It is a refusal: sweeping the worktree instead would publish what no
// reviewer saw.
var ErrNoReviewedPaths = errors.New("publication names no reviewed candidate paths, so nothing is staged; the worktree is not swept")

// ErrPushNotGranted reports that publication was attempted without the
// capability. It is a refusal, not a failure: the configuration says this
// repository may not be pushed to.
var ErrPushNotGranted = errors.New("push is not granted in .sensei-code/config.json, so no pull request was opened")

// ErrNoCandidateCommit reports a publication asked to push a branch that does
// not name the accepted candidate's identity.
//
// Publication no longer commits. The accepted candidate is given exactly one
// commit -- its canonical identity -- before anything reports or publishes it,
// and this package pushes THAT object or refuses. Two commit paths would
// eventually disagree about what was committed, which is the whole defect class
// this work exists to remove.
var ErrNoCandidateCommit = errors.New("the candidate branch does not name the accepted candidate's commit identity, so there is nothing to publish")

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

// Result is how far publication actually got.
//
// It is staged rather than boolean because the stages have different
// consequences and they fail independently. A push that succeeded before `gh`
// failed has already put a branch on the remote, and a caller told only "an
// error occurred" would record the candidate as unpublished while the remote
// disagrees. Effects that happened are reported whether or not the whole
// sequence did.
type Result struct {
	Committed bool
	Pushed    bool
	URL       string
}

// Opened reports a complete publication.
func (r Result) Opened() bool { return strings.TrimSpace(r.URL) != "" }

// Effects describes what reached the remote, for a caller recording a partial
// publication. It is empty when nothing left the candidate worktree.
func (r Result) Effects() string {
	switch {
	case r.Opened():
		return "pull request opened at " + r.URL
	case r.Pushed:
		return "the candidate branch was pushed to origin but no pull request was opened"
	case r.Committed:
		return "the candidate was committed locally but nothing was pushed"
	default:
		return ""
	}
}

// ErrNoPullRequestURL reports that gh returned success without a pull request
// URL. It is a failure rather than a detail: the caller's next act is to tell a
// person where their pull request is.
var ErrNoPullRequestURL = errors.New("gh reported success but printed no pull request URL")

// Open commits the candidate, publishes its branch, and opens a pull request.
// The caller is responsible for having obtained the human's decision; this only
// enforces the configured capabilities.
//
// The Result is returned on every path, including the error paths, so a partial
// publication is recoverable by the caller rather than lost with the error.
func Open(ctx context.Context, r Request, pushGranted bool) (Result, error) {
	var done Result
	if !pushGranted {
		return done, ErrPushNotGranted
	}
	if strings.TrimSpace(r.Branch) == "" || strings.TrimSpace(r.Workspace) == "" {
		return done, errors.New("publication needs a candidate branch and workspace")
	}
	paths := make([]string, 0, len(r.Paths))
	for _, p := range r.Paths {
		if strings.TrimSpace(p) != "" {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		// Kept from when this package committed: a publication that names
		// nothing judged is refused rather than allowed to mean "publish
		// whatever is there".
		return done, ErrNoReviewedPaths
	}
	if strings.TrimSpace(r.CandidateCommit) == "" {
		return done, ErrNoCandidateCommit
	}
	// Push the EXACT accepted object, or refuse. The branch was pointed at the
	// candidate's identity when it was minted; if it no longer names that
	// commit, something moved it and this is not the accepted candidate.
	head, err := run(ctx, r.Workspace, "git", []string{"rev-parse", "HEAD"})
	if err != nil {
		return done, fmt.Errorf("read the candidate branch head: %s", head)
	}
	if strings.TrimSpace(head) != strings.TrimSpace(r.CandidateCommit) {
		return done, fmt.Errorf("%w: the branch is at %s and the accepted candidate is %s",
			ErrNoCandidateCommit, short12(head), short12(r.CandidateCommit))
	}
	done.Committed = true
	if out, err := run(ctx, r.Workspace, "git", PushArgs(r.Branch)); err != nil {
		return done, fmt.Errorf("push the candidate branch: %s", out)
	}
	done.Pushed = true
	out, err := run(ctx, r.Workspace, "gh", r.PullRequestArgs())
	if err != nil {
		return done, fmt.Errorf("open the pull request: %s", out)
	}
	url := prURL(out)
	if url == "" {
		return done, fmt.Errorf("%w: %s", ErrNoPullRequestURL, oneLine(out))
	}
	done.URL = url
	return done, nil
}

// prURL picks the pull request URL out of gh's output, and returns nothing when
// there is none.
//
// It used to fall back to whatever gh printed, directly contradicting its own
// purpose: the caller renders this as "pull request opened: "+url, so echoing
// arbitrary output announced a pull request that may not exist and handed the
// reader a URL-shaped sentence containing no URL.
func prURL(out string) string {
	for _, field := range strings.Fields(out) {
		if strings.HasPrefix(field, "https://") && strings.Contains(field, "/pull/") {
			return field
		}
	}
	return ""
}

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > 200 {
		return s[:200] + "…"
	}
	return s
}

func run(ctx context.Context, dir, name string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func short12(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

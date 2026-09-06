package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// The transport half: posting a request and reading replies from one pull
// request. Deliberately thin, and deliberately shelling out to `gh` the way
// internal/publish already does, so credentials stay where gh keeps them.
//
// Worker isolation: this runs in the Sensei Code process. The Claude worker
// never receives these credentials, so a worker cannot post a comment that
// would parse as a review from the remote party. That matters even though the
// bridge is advisory — a forged marker would corrupt the loop's evidence even
// if it could not unlock a gate.

// Issue names the dedicated GitHub issue acting as the mailbox.
//
// An issue rather than a pull request on purpose: a PR carries topology — a
// head branch, a merge target, review state — and making any of that part of
// workflow semantics would tie the protocol to a shape it does not need. The
// artifact under review is fetchable by sha from the pushed review ref, so the
// mailbox only has to carry text.
type Issue struct {
	// Dir is the repository working directory gh is invoked from.
	Dir string
	// Number is the issue acting as the mailbox. Required: there is no
	// "current issue" for gh to infer, and inferring one would be a guess about
	// where a review request went.
	Number string
}

func (i Issue) args(rest ...string) []string {
	return append(append([]string(nil), rest...), i.Number)
}

// Valid reports whether this mailbox can be addressed at all.
func (i Issue) Valid() bool { return strings.TrimSpace(i.Number) != "" }

func run(ctx context.Context, dir string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// PostRequest publishes a review request for one exact candidate.
//
// The marker is emitted by Sensei Code and by nothing else: the remote party
// answers requests, it does not create them. Returns the request unchanged so a
// caller can record what it asked for.
func PostRequest(ctx context.Context, box Issue, r Request, note string) error {
	marker, err := r.Marker()
	if err != nil {
		return err
	}
	body := marker
	if strings.TrimSpace(note) != "" {
		body += "\n" + strings.TrimSpace(note) + "\n"
	}
	args := box.args("issue", "comment")
	args = append(args, "--body", body)
	if !box.Valid() {
		return errors.New("a review request needs the mailbox issue number")
	}
	if out, err := run(ctx, box.Dir, args); err != nil {
		return fmt.Errorf("gh issue comment: %w: %s", err, out)
	}
	return nil
}

type ghComment struct {
	Body   string `json:"body"`
	Author struct {
		Login string `json:"login"`
	} `json:"author"`
	CreatedAt time.Time `json:"createdAt"`
}

type ghIssueView struct {
	Comments []ghComment `json:"comments"`
}

// Reviews reads every parseable review currently on the pull request.
//
// Unparseable comments are skipped silently and on purpose: a pull request is a
// place people talk, and ordinary conversation is not a malformed review.
func Reviews(ctx context.Context, box Issue) ([]Review, error) {
	args := box.args("issue", "view")
	args = append(args, "--json", "comments")
	out, err := run(ctx, box.Dir, args)
	if err != nil {
		return nil, fmt.Errorf("gh issue view: %w: %s", err, out)
	}
	var view ghIssueView
	if jerr := json.Unmarshal([]byte(out), &view); jerr != nil {
		return nil, fmt.Errorf("gh returned a body this bridge could not read: %w", jerr)
	}
	var found []Review
	for _, c := range view.Comments {
		if rev, ok := ParseReview(c.Body, c.Author.Login); ok {
			found = append(found, rev)
		}
	}
	return found, nil
}

// ErrNoAnswer reports that no review answering this request has appeared yet.
var ErrNoAnswer = errors.New("no review answering that request has been posted yet")

// AwaitReview polls until a review answering THIS request appears.
//
// Answering is the strict three-way match, so a reply to an earlier request, or
// a review of a superseded candidate, does not end the wait. That is the point:
// when Claude repairs C1 into C2, the review of C1 is still sitting on the pull
// request, and a looser match would let it satisfy the request for C2.
//
// The context is the caller's deadline. A timeout leaves the task durable and
// pending in Sensei Code — nothing here completes or discards work.
func AwaitReview(ctx context.Context, box Issue, r Request, every time.Duration) (Review, error) {
	if err := r.Validate(); err != nil {
		return Review{}, err
	}
	if every <= 0 {
		every = 15 * time.Second
	}
	for {
		revs, err := Reviews(ctx, box)
		if err == nil {
			for _, rev := range revs {
				if rev.Answers(r) {
					return rev, nil
				}
			}
		}
		select {
		case <-ctx.Done():
			return Review{}, fmt.Errorf("%w: %v", ErrNoAnswer, ctx.Err())
		case <-time.After(every):
		}
	}
}

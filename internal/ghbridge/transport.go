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

// The transport half: posting a request and reading answers from one dedicated
// issue. Deliberately thin, and deliberately shelling out to `gh` the way
// internal/publish already does, so credentials stay where gh keeps them.
//
// Worker isolation: this runs in the Sensei Code process. The Claude worker
// never receives these credentials, so a worker cannot post a comment that
// would be read as an answer from the remote reviewer.

// Principal is the GitHub identity permitted to answer on this mailbox.
//
// It establishes WHO sent an advisory result. It does not establish
// independence and it never raises SessionMode: a turn answered over a
// transport stays roles.Unverified however well authenticated the sender is.
//
// UserID is the strongest identity available and is preferred: a numeric user
// id is immutable, while a login can be changed and reused. Login is carried
// for display, and is the match only when no id was configured.
type Principal struct {
	UserID int64
	Login  string
}

// Configured reports whether this principal can authenticate anybody.
//
// A mailbox with no expected responder authenticates NOBODY rather than
// everybody: an unconfigured bridge that accepted any parseable comment would
// let any account with write access post an advisory ACCEPT.
func (p Principal) Configured() bool { return p.UserID != 0 || strings.TrimSpace(p.Login) != "" }

// Matches reports whether a comment author is this principal.
func (p Principal) Matches(authorID int64, authorLogin string) bool {
	if !p.Configured() {
		return false
	}
	if p.UserID != 0 {
		return authorID == p.UserID
	}
	return strings.EqualFold(strings.TrimSpace(p.Login), strings.TrimSpace(authorLogin))
}

// String describes the principal for diagnostics.
func (p Principal) String() string {
	switch {
	case p.UserID != 0 && p.Login != "":
		return fmt.Sprintf("%s (id %d)", p.Login, p.UserID)
	case p.UserID != 0:
		return fmt.Sprintf("user id %d", p.UserID)
	case p.Login != "":
		return p.Login + " (login only)"
	default:
		return "no expected reviewer configured"
	}
}

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
	// ExpectedReviewer is the only GitHub identity whose comments are read as
	// answers. Never inferred from the issue creator, the repository owner, a
	// commit author, the current gh login, or author_association — each of those
	// is a different fact, and treating one as the reviewer would authenticate
	// whoever happened to satisfy it.
	ExpectedReviewer Principal
}

// Valid reports whether this mailbox can be addressed and can authenticate.
func (i Issue) Valid() bool {
	return strings.TrimSpace(i.Number) != "" && i.ExpectedReviewer.Configured()
}

func (i Issue) args(rest ...string) []string {
	return append(append([]string(nil), rest...), i.Number)
}

func run(ctx context.Context, dir string, args []string) (string, error) {
	cmd := exec.CommandContext(ctx, "gh", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// PostRequest publishes a review request for one exact candidate.
//
// The marker is emitted by Sensei Code and by nothing else: the remote party
// answers requests, it does not create them.
func PostRequest(ctx context.Context, box Issue, r Request, note string) error {
	if !box.Valid() {
		return errors.New("a review request needs a mailbox issue number and an expected reviewer")
	}
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
	if out, err := run(ctx, box.Dir, args); err != nil {
		return fmt.Errorf("gh issue comment: %w: %s", err, out)
	}
	return nil
}

// restComment is the REST shape, used instead of `gh issue view --json
// comments` because that view exposes only the author's login. An immutable
// numeric user id is the identity worth authenticating against.
type restComment struct {
	Body string `json:"body"`
	User struct {
		Login  string `json:"login"`
		ID     int64  `json:"id"`
		NodeID string `json:"node_id"`
	} `json:"user"`
}

// Reviews reads every answer on the mailbox that came from the configured
// reviewer.
//
// Authentication happens BEFORE identity matching and before anything reaches
// the workflow: a perfectly formed marker from another GitHub account is not an
// answer, it is a comment. Comments from anyone else are skipped silently — an
// issue is a place people talk.
func Reviews(ctx context.Context, box Issue) ([]Review, error) {
	if !box.Valid() {
		return nil, errors.New("reading the mailbox needs an issue number and an expected reviewer")
	}
	path := "repos/{owner}/{repo}/issues/" + box.Number + "/comments"
	out, err := run(ctx, box.Dir, []string{"api", "--paginate", path})
	if err != nil {
		return nil, fmt.Errorf("gh api %s: %w: %s", path, err, out)
	}
	var comments []restComment
	if jerr := json.Unmarshal([]byte(out), &comments); jerr != nil {
		return nil, fmt.Errorf("gh returned a body this bridge could not read: %w", jerr)
	}
	var found []Review
	for _, c := range comments {
		if !box.ExpectedReviewer.Matches(c.User.ID, c.User.Login) {
			continue
		}
		if rev, ok := ParseReview(c.Body, c.User.Login); ok {
			rev.AuthorID = c.User.ID
			found = append(found, rev)
		}
	}
	return found, nil
}

// ErrNoAnswer reports that no review answering this request appeared in time.
var ErrNoAnswer = errors.New("no review answering that request was posted")

// AwaitReview polls until an answer to THIS request appears.
//
// Answering is the strict match on request id and every identity field, so a
// reply to an earlier request, or a review of a superseded candidate, does not
// end the wait. That is the point: when Claude repairs C1 into C2, the review of
// C1 is still sitting on the mailbox.
//
// A mailbox read failure ends the turn immediately. It is NOT "no answer yet":
// polling through a gh auth failure or a network outage would turn a broken
// mailbox into a silent timeout, and the turn would fail much later for a
// reason unrelated to the real one.
//
// A timeout leaves the task durable and pending. Nothing here completes or
// discards work.
func AwaitReview(ctx context.Context, box Issue, r Request, every time.Duration) (Review, error) {
	if err := r.Validate(); err != nil {
		return Review{}, err
	}
	if every <= 0 {
		every = 15 * time.Second
	}
	for {
		revs, err := Reviews(ctx, box)
		if err != nil {
			// Distinguish "the mailbox is unreadable" from "nobody answered".
			if ctx.Err() != nil {
				return Review{}, ctx.Err()
			}
			return Review{}, fmt.Errorf("reading the review mailbox: %w", err)
		}
		for _, rev := range revs {
			if rev.Answers(r) {
				return rev, nil
			}
		}
		select {
		case <-ctx.Done():
			return Review{}, fmt.Errorf("%w: %v", ErrNoAnswer, ctx.Err())
		case <-time.After(every):
		}
	}
}

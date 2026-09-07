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
	// Number is the PULL REQUEST whose top-level conversation is the mailbox.
	// Required: there is no "current conversation" for gh to infer, and
	// inferring one would be a guess about where a request went.
	//
	// A pull request rather than an ordinary issue, because the remote actor is
	// woken by pull request activity. Both are addressed through GitHub's issues
	// resource — a PR conversation IS an issue conversation as far as
	// /issues/{n}/comments is concerned — which is why this type keeps its name
	// and why no second transport exists. What changed is which conversations
	// are acceptable, not how they are read. VerifyMailboxIsPullRequest is what
	// enforces that, and it is deliberately not something this struct can
	// decide for itself: it takes a network answer, and a type cannot be its own
	// evidence.
	Number string
	// API, when set, performs the mailbox's REST operations as the GitHub App
	// installation instead of through the operator's gh credentials. Selecting
	// it is deliberate and there is NO fallback: an App transport that could
	// silently revert to a person's credentials would post machine-originated
	// mailbox activity under that person's identity, which is the thing this
	// slice exists to stop being true.
	API *AppClient
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
		return errors.New("a review request needs a mailbox pull request number and an expected reviewer")
	}
	marker, err := r.Marker()
	if err != nil {
		return err
	}
	body := marker
	if strings.TrimSpace(note) != "" {
		body += "\n" + strings.TrimSpace(note) + "\n"
	}
	if box.API != nil {
		if !box.API.Configured() {
			return errors.New("the github app transport was selected but is not configured; " +
				"refusing rather than posting as the operator's gh account")
		}
		_, err := box.API.PostComment(ctx, box.Number, body)
		return err
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
		return nil, errors.New("reading the mailbox needs a pull request number and an expected reviewer")
	}
	var comments []restComment
	if box.API != nil {
		if !box.API.Configured() {
			return nil, errors.New("the github app transport was selected but is not configured; " +
				"refusing rather than reading as the operator's gh account")
		}
		var err error
		if comments, err = box.API.ListComments(ctx, box.Number); err != nil {
			return nil, err
		}
	} else {
		// DEFERRED (post-PR6): `gh api --paginate` emits one JSON value PER
		// PAGE, not one array, so this decode fails the moment the mailbox
		// crosses a pagination boundary. The App path above follows pages
		// explicitly and does not have this defect; this branch keeps the
		// existing behaviour unchanged for installations with no App
		// configured.
		path := "repos/{owner}/{repo}/issues/" + box.Number + "/comments"
		out, err := run(ctx, box.Dir, []string{"api", "--paginate", path})
		if err != nil {
			return nil, fmt.Errorf("gh api %s: %w: %s", path, err, out)
		}
		if jerr := json.Unmarshal([]byte(out), &comments); jerr != nil {
			return nil, fmt.Errorf("gh returned a body this bridge could not read: %w", jerr)
		}
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

// ErrNotAPullRequest reports a mailbox number that resolves to an ordinary
// issue rather than to a pull request.
var ErrNotAPullRequest = errors.New("the configured mailbox is not a pull request")

// VerifyMailboxIsPullRequest establishes that the configured mailbox number is
// a pull request in the configured repository.
//
// It matters because the remote actor is woken by PULL REQUEST activity. A
// mailbox aimed at an ordinary issue posts successfully, reads successfully,
// and is simply never answered — and an unanswered request is exactly what a
// remote party choosing not to reply looks like. That ambiguity cost four days
// once already: the request reached issue #156 correctly and nothing woke, and
// the silence was read as the architect being slow. This check exists so the
// wrong kind of target is a refusal at startup rather than a silence at
// runtime.
//
// Nothing is inferred. The number and the repository are configuration. The
// branch, the local git state, the checkout the process runs in and the
// conversation's own text are facts about this machine rather than about which
// conversation is the mailbox, and a bridge that read one of them would verify
// whatever it happened to be run beside.
//
// An unavailable GitHub is a REFUSAL, not a pass, per
// sensei_code.ghbridge.an_unavailable_bridge_refuses_rather_than_substituting:
// a bridge that cannot establish its own target does not get to assume the
// target is fine. Both transports authenticate — the App installation when one
// is selected, gh's own credentials otherwise — and the selected App transport
// never falls back to the operator's account to answer this question.
func VerifyMailboxIsPullRequest(ctx context.Context, box Issue) error {
	number := strings.TrimSpace(box.Number)
	if number == "" {
		return errors.New("establishing the mailbox needs a conversation number")
	}

	if box.API != nil {
		url, err := box.API.PullRequestURL(ctx, number)
		if err != nil {
			return fmt.Errorf("establishing that mailbox #%s is a pull request: %w", number, err)
		}
		if url == "" {
			return fmt.Errorf("%w: %s/%s #%s is an ordinary issue, and the remote actor is woken by "+
				"pull request activity", ErrNotAPullRequest, box.API.Owner, box.API.Repo, number)
		}
		// Being the right KIND of conversation is not the same as being one this
		// App may post into, and the two were conflated once already: reading
		// #157 needs only issues:read, so a mailbox that verified perfectly
		// returned HTTP 403 "Resource not accessible by integration" on the
		// first request — two seconds after an at-most-once approval receipt had
		// been spent, which made an unfixable-by-retry configuration error look
		// like a failed task.
		//
		// A PR conversation is reached through the issues endpoint but
		// authorized against PULL REQUESTS, which is why issues:write is not
		// enough and why the same endpoint worked for an issue mailbox and not
		// for this one.
		if err := box.API.Auth.RequireWrite(ctx, "pull_requests"); err != nil {
			return fmt.Errorf("the mailbox %s/%s #%s is a pull request this app cannot post to: %w",
				box.API.Owner, box.API.Repo, number, err)
		}
		return nil
	}

	// Same issues resource, read through gh. `.pull_request.url // ""` keeps the
	// two outcomes apart the same way the App path does: an ordinary issue is an
	// empty value, and only a failure to ask is an error.
	out, err := run(ctx, box.Dir, []string{
		"api", "repos/{owner}/{repo}/issues/" + number, "--jq", `.pull_request.url // ""`,
	})
	if err != nil {
		return fmt.Errorf("establishing that mailbox #%s is a pull request: %w: %s", number, err, out)
	}
	if strings.TrimSpace(out) == "" {
		return fmt.Errorf("%w: #%s is an ordinary issue, and the remote actor is woken by pull request activity",
			ErrNotAPullRequest, number)
	}
	return nil
}

package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PostArchitectureRequest publishes one exact objective/world question. Unlike
// review transport there is no Git snapshot to publish: the architect's subject
// is the approved objective at a pinned base and graph generation, not candidate
// content.
func PostArchitectureRequest(ctx context.Context, box Issue, r ArchitectureRequest) error {
	_, err := PublishArchitectureRequest(ctx, box, r)
	return err
}

// PublishArchitectureRequest posts the request and returns the comment id
// GitHub gave it, so a doorbell can point at this exact object.
//
// The id is 0 on the legacy gh path, which posts through `gh issue comment` and
// is not asked for the created identity. That is reported as an unknown
// locator rather than papered over: a doorbell cannot point at a comment whose
// name this process never learned, and inventing one would defeat the whole
// reason the wake carries a locator instead of a copy of the binding.
func PublishArchitectureRequest(ctx context.Context, box Issue, r ArchitectureRequest) (int64, error) {
	if !box.Valid() {
		return 0, errors.New("an architecture request needs a mailbox pull request number and an expected remote principal")
	}
	body, err := r.Marker()
	if err != nil {
		return 0, err
	}
	if box.API != nil {
		if !box.API.Configured() {
			return 0, errors.New("the github app transport was selected but is not configured; refusing rather than posting as the operator's gh account")
		}
		return box.API.PostComment(ctx, box.Number, body)
	}
	args := box.args("issue", "comment")
	args = append(args, "--body", body)
	if out, err := run(ctx, box.Dir, args); err != nil {
		return 0, fmt.Errorf("gh issue comment: %w: %s", err, out)
	}
	return 0, nil
}

// Architectures reads authenticated architecture answers from the same mailbox
// as reviews. ExpectedReviewer is an old field name here; its numeric principal
// is the configured remote ChatGPT transport identity, and authenticating that
// identity grants no objective authority or reviewer independence.
func Architectures(ctx context.Context, box Issue) ([]ArchitectureResponse, error) {
	if !box.Valid() {
		return nil, errors.New("reading the architecture mailbox needs a pull request number and an expected remote principal")
	}
	var comments []restComment
	if box.API != nil {
		if !box.API.Configured() {
			return nil, errors.New("the github app transport was selected but is not configured; refusing rather than reading as the operator's gh account")
		}
		var err error
		if comments, err = box.API.ListComments(ctx, box.Number); err != nil {
			return nil, err
		}
	} else {
		// Same known legacy-gh pagination limitation as Reviews. The configured
		// App path used by the live bridge follows Link pages explicitly.
		path := "repos/{owner}/{repo}/issues/" + box.Number + "/comments"
		out, err := run(ctx, box.Dir, []string{"api", "--paginate", path})
		if err != nil {
			return nil, fmt.Errorf("gh api %s: %w: %s", path, err, out)
		}
		if jerr := json.Unmarshal([]byte(out), &comments); jerr != nil {
			return nil, fmt.Errorf("gh returned a body this bridge could not read: %w", jerr)
		}
	}
	var found []ArchitectureResponse
	for _, c := range comments {
		if !box.ExpectedReviewer.Matches(c.User.ID, c.User.Login) {
			continue
		}
		if answer, ok := ParseArchitectureResponse(c.Body); ok {
			answer.Author = c.User.Login
			answer.AuthorID = c.User.ID
			found = append(found, answer)
		}
	}
	return found, nil
}

var ErrNoArchitectureAnswer = errors.New("no architecture answer answering that request was posted")

// AwaitArchitecture waits only for an authenticated answer matching request id,
// objective digest, base and graph generation. A stale architecture response is
// still visible on the issue and has no standing for this turn.
func AwaitArchitecture(ctx context.Context, box Issue, r ArchitectureRequest, every time.Duration) (ArchitectureResponse, error) {
	if err := r.Validate(); err != nil {
		return ArchitectureResponse{}, err
	}
	if every <= 0 {
		every = 15 * time.Second
	}
	for {
		answers, err := Architectures(ctx, box)
		if err != nil {
			if ctx.Err() != nil {
				return ArchitectureResponse{}, ctx.Err()
			}
			return ArchitectureResponse{}, fmt.Errorf("reading the architecture mailbox: %w", err)
		}
		for _, answer := range answers {
			if answer.Answers(r) {
				return answer, nil
			}
		}
		select {
		case <-ctx.Done():
			return ArchitectureResponse{}, fmt.Errorf("%w: %v", ErrNoArchitectureAnswer, ctx.Err())
		case <-time.After(every):
		}
	}
}

package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/roles"
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
	comments, err := architectureComments(ctx, box)
	if err != nil {
		return nil, err
	}
	return architectureAnswers(comments, box.ExpectedReviewer), nil
}

// architectureComments is the ONE read of the architecture mailbox.
//
// Extracted so answers and refusals are classified from the same bytes on the
// same poll. Two reads would let a consumer post its refusal between them and
// be seen by neither, which is the failure this envelope exists to end.
func architectureComments(ctx context.Context, box Issue) ([]restComment, error) {
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
		return comments, nil
	}
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
	return comments, nil
}

// architectureAnswers is the answer half of the classification, unchanged: an
// authenticated comment that parses as an architecture response, carrying the
// principal that posted it.
func architectureAnswers(comments []restComment, expected Principal) []ArchitectureResponse {
	var found []ArchitectureResponse
	for _, c := range comments {
		if !expected.Matches(c.User.ID, c.User.Login) {
			continue
		}
		if answer, ok := ParseArchitectureResponse(c.Body); ok {
			answer.Author = c.User.Login
			answer.AuthorID = c.User.ID
			found = append(found, answer)
		}
	}
	return found
}

// ArchitectureRefusalRejected is one refusal-SHAPED comment that cannot
// discharge a wait, together with the reason it cannot.
//
// Reported rather than skipped. A malformed or differently bound refusal that is
// silently dropped leaves the same observation as a consumer that never replied,
// which is the defect the refusal envelope removes -- so dropping one here would
// recreate it exactly one layer up.
type ArchitectureRefusalRejected struct {
	Comment    int64
	Author     string
	AuthorID   int64
	Diagnostic string
}

// identity names one rejection, so the same unusable comment seen on ten polls
// is reported once and an edited one is reported as what it now is.
func (r ArchitectureRefusalRejected) identity() string {
	return fmt.Sprintf("%d|%s", r.Comment, r.Diagnostic)
}

// ArchitectureObservation is what ONE read of the mailbox saw for one open
// request: answers, refusals bound to it exactly, and refusal-shaped comments
// that are neither.
//
// None of the three is authority. Only an exact-bound answer may proceed as an
// architecture result; a bound refusal ends the exchange and decides nothing;
// and a rejected refusal is evidence about what is in the mailbox.
type ArchitectureObservation struct {
	Answers  []ArchitectureResponse
	Refusals []ArchitectureRefusal
	Rejected []ArchitectureRefusalRejected
}

// Settlement is the ONE refusal that settles this request: the first valid bound
// one observed.
//
// Later copies are inert, which is what makes duplicate wakes and republished
// refusals harmless. A request cannot be refused twice -- the second copy says
// the same thing about the same request -- so there is nothing for a second
// settlement to mean.
func (o ArchitectureObservation) Settlement() (ArchitectureRefusal, bool) {
	if len(o.Refusals) == 0 {
		return ArchitectureRefusal{}, false
	}
	return o.Refusals[0], true
}

// ObserveArchitecture reads the mailbox once and classifies what it holds for
// one open request.
//
// Authentication first. A comment from any other account is ordinary mailbox
// content: an unauthenticated party must not be able to settle a governed
// exchange, nor to make Sensei-Code report that its consumer refused badly.
func ObserveArchitecture(ctx context.Context, box Issue, r ArchitectureRequest) (ArchitectureObservation, error) {
	comments, err := architectureComments(ctx, box)
	if err != nil {
		return ArchitectureObservation{}, err
	}
	obs := ArchitectureObservation{Answers: architectureAnswers(comments, box.ExpectedReviewer)}
	for _, c := range comments {
		if !box.ExpectedReviewer.Matches(c.User.ID, c.User.Login) {
			continue
		}
		if !ArchitectureRefusalShaped(c.Body) {
			continue
		}
		rejected := ArchitectureRefusalRejected{Comment: c.ID, Author: c.User.Login, AuthorID: c.User.ID}
		refusal, perr := ParseArchitectureRefusal(c.Body)
		if perr != nil {
			rejected.Diagnostic = "a refusal-shaped comment could not be read as a refusal: " + perr.Error()
			obs.Rejected = append(obs.Rejected, rejected)
			continue
		}
		if m := refusal.mismatch(r); m != "" {
			// A well formed refusal OF SOMETHING ELSE. Not a fault of the
			// consumer's, and not this request's terminal either: no subset of
			// the binding is accepted in place of the whole.
			rejected.Diagnostic = "a well formed refusal bound elsewhere cannot settle this request: " + m
			obs.Rejected = append(obs.Rejected, rejected)
			continue
		}
		refusal.Author, refusal.AuthorID, refusal.Comment = c.User.Login, c.User.ID, c.ID
		obs.Refusals = append(obs.Refusals, refusal)
	}
	return obs, nil
}

// ErrArchitectureRefused reports that the consumer this request was published to
// REFUSED it, by name, for a stated reason.
//
// Its own condition because none of the existing ones is true. Nobody is still
// owed an answer -- the consumer replied -- and nothing was decided either. It
// is the negative terminal the grammar previously could not express, and it ends
// one exact exchange and nothing else.
var ErrArchitectureRefused = errors.New("the remote consumer refused this exact architecture request")

// ArchitectureRefused is that refusal as a turn outcome.
//
// It travels as an ERROR, never in ArchitectureResponse, and that is structural
// rather than stylistic: there is no field here an architecture decision, plan,
// coverage or grant could be read out of, so no consumer of this outcome can
// mistake a refusal for a completed architect turn.
type ArchitectureRefused struct {
	RequestID string
	Binding   roles.ArchitectureBinding
	Stage     RefusalStage
	Reason    string
	Author    string
	AuthorID  int64
	Comment   int64
	// Rejected are the refusal-shaped comments seen while waiting that could not
	// settle this request. Carried with the settlement so a malformed copy stays
	// visible even when a later valid one ends the wait.
	Rejected []ArchitectureRefusalRejected
}

func (e *ArchitectureRefused) Error() string {
	return fmt.Sprintf("%v: request %s was refused at stage %s: %s%s",
		ErrArchitectureRefused, e.RequestID, e.Stage, e.Reason, renderRejected(e.Rejected))
}

func (e *ArchitectureRefused) Unwrap() error { return ErrArchitectureRefused }

// renderRejected states refusal-shaped comments that could not settle a request.
//
// Empty when there were none, so an exchange that never saw one reports exactly
// what it reported before this envelope existed.
func renderRejected(rejected []ArchitectureRefusalRejected) string {
	if len(rejected) == 0 {
		return ""
	}
	b := strings.Builder{}
	fmt.Fprintf(&b, "; %d refusal-shaped comment(s) could not settle this request", len(rejected))
	for _, rej := range rejected {
		fmt.Fprintf(&b, ": comment %d from %s: %s", rej.Comment, rej.Author, rej.Diagnostic)
	}
	return b.String()
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
	// Rejections accumulate across polls. A waiter that remembered only its last
	// poll would forget an unusable refusal the moment the next poll returned
	// nothing new, and would end as silence again.
	var rejected []ArchitectureRefusalRejected
	seen := map[string]bool{}
	for {
		obs, err := ObserveArchitecture(ctx, box, r)
		if err != nil {
			if ctx.Err() != nil {
				return ArchitectureResponse{}, ctx.Err()
			}
			return ArchitectureResponse{}, fmt.Errorf("reading the architecture mailbox: %w", err)
		}
		// The ANSWER path first and unchanged. A refusal is only ever reached
		// when no answer to this exact request is present, so the addition can
		// take nothing away from a consumer that answers.
		for _, answer := range obs.Answers {
			if answer.Answers(r) {
				return answer, nil
			}
		}
		for _, rej := range obs.Rejected {
			if seen[rej.identity()] {
				continue
			}
			seen[rej.identity()] = true
			rejected = append(rejected, rej)
		}
		// A valid bound refusal is TERMINAL for this exact request, immediately.
		// Waiting out the deadline after the consumer has already said why it
		// stopped is how an hour was spent on a diagnostic that existed in
		// seconds. A non-settling refusal changes nothing here: the wait
		// continues, and its rejection travels with whatever ends the wait.
		if refusal, ok := obs.Settlement(); ok {
			return ArchitectureResponse{}, &ArchitectureRefused{
				RequestID: refusal.RequestID, Binding: refusal.Binding,
				Stage: refusal.Stage, Reason: refusal.Reason,
				Author: refusal.Author, AuthorID: refusal.AuthorID, Comment: refusal.Comment,
				Rejected: rejected,
			}
		}
		select {
		case <-ctx.Done():
			return ArchitectureResponse{}, fmt.Errorf("%w: %v%s",
				ErrNoArchitectureAnswer, ctx.Err(), renderRejected(rejected))
		case <-time.After(every):
		}
	}
}

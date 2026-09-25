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

// ArchitectureTerminal is ONE artifact that can settle an exact request: either
// the answer to it or the refusal of it.
//
// One type for both because settlement has ONE ordering. Keeping answers and
// refusals in separate lists discarded the order they shared, and a reader then
// had to pick a rule -- answers first, refusals first, or by clock -- none of
// which is what the conversation says. Exactly one of the two pointers is set.
type ArchitectureTerminal struct {
	Answer  *ArchitectureResponse
	Refusal *ArchitectureRefusal
	// Comment is the mailbox comment this terminal was read from, so a reader
	// can say WHICH artifact settled and in what order they arrived.
	Comment int64
}

// Refused reports whether this terminal ends the request negatively.
func (t ArchitectureTerminal) Refused() bool { return t.Refusal != nil }

// ArchitectureObservation is what ONE read of the mailbox saw for one open
// request: the artifacts bound to it exactly that can settle it, in the order
// the conversation carries them, and the refusal-shaped comments that cannot.
//
// Neither is authority. Only an exact-bound answer may proceed as an
// architecture result; a bound refusal ends the exchange and decides nothing;
// and a rejected refusal is evidence about what is in the mailbox.
type ArchitectureObservation struct {
	// Terminals are every exact-bound settling artifact this read saw, IN
	// CONVERSATION ORDER. All of them, not just the first: a later conflicting
	// artifact must be observable as inert rather than invisible.
	Terminals []ArchitectureTerminal
	Rejected  []ArchitectureRefusalRejected
}

// Settlement is the ONE artifact that settles this request: THE EARLIEST valid
// exact-bound terminal in the conversation.
//
// EARLIEST, in either direction, and that is the repair. The observation used to
// hold answers and refusals in separate lists and the waiter scanned every
// answer before looking at any refusal, so a refusal that had already settled a
// request was overridden by an answer posted after it and the turn returned
// SUCCESS. The reverse ordering has the same shape. Whichever came first is what
// happened; later copies and later conflicting artifacts are inert, which is
// also what makes duplicate wakes and republished refusals harmless.
//
// Order is taken from the conversation itself, never from a clock. Comment order
// is what GitHub actually guarantees about this mailbox; two comments can share
// a timestamp, and a reader that broke the tie by clock would settle a request
// differently depending on which second they were posted in.
func (o ArchitectureObservation) Settlement() (ArchitectureTerminal, bool) {
	if len(o.Terminals) == 0 {
		return ArchitectureTerminal{}, false
	}
	return o.Terminals[0], true
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
	// ONE walk, in conversation order, classifying each comment once. Two walks
	// -- one for answers and one for refusals -- is exactly how the shared order
	// was lost.
	var obs ArchitectureObservation
	for _, c := range comments {
		if !box.ExpectedReviewer.Matches(c.User.ID, c.User.Login) {
			continue
		}
		// POSITION ZERO decides which grammar reads this comment. A refusal-
		// shaped body is never handed to the answer parser and an answer is
		// never handed to the refusal parser, so neither can be reported as a
		// malformed example of the other.
		if ArchitectureRefusalShaped(c.Body) {
			rejected := ArchitectureRefusalRejected{Comment: c.ID, Author: c.User.Login, AuthorID: c.User.ID}
			refusal, perr := ParseArchitectureRefusal(c.Body)
			if perr != nil {
				rejected.Diagnostic = "a refusal-shaped comment could not be read as a refusal: " + perr.Error()
				obs.Rejected = append(obs.Rejected, rejected)
				continue
			}
			if m := refusal.mismatch(r); m != "" {
				// A well formed refusal OF SOMETHING ELSE. Not a fault of the
				// consumer's, and not this request's terminal either: no subset
				// of the binding is accepted in place of the whole.
				rejected.Diagnostic = "a well formed refusal bound elsewhere cannot settle this request: " + m
				obs.Rejected = append(obs.Rejected, rejected)
				continue
			}
			refusal.Author, refusal.AuthorID, refusal.Comment = c.User.Login, c.User.ID, c.ID
			bound := refusal
			obs.Terminals = append(obs.Terminals, ArchitectureTerminal{Refusal: &bound, Comment: c.ID})
			continue
		}
		answer, ok := ParseArchitectureResponse(c.Body)
		if !ok || !answer.Answers(r) {
			continue
		}
		answer.Author, answer.AuthorID = c.User.Login, c.User.ID
		bound := answer
		obs.Terminals = append(obs.Terminals, ArchitectureTerminal{Answer: &bound, Comment: c.ID})
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

	// ExchangeCloseErr records that this request's durable exchange record could
	// NOT be closed after the refusal settled it.
	//
	// It lives on the refusal so one outcome carries both facts. The refusal is
	// still exactly what the consumer said; what is no longer claimed is that the
	// durable record agrees. An exchange left open is withdrawn by the next
	// startup, so a caller that reported "refused, settled" on a failed close
	// would have this process say the exchange ended and the next one say the
	// question was retracted unanswered.
	ExchangeCloseErr error
}

func (e *ArchitectureRefused) Error() string {
	out := fmt.Sprintf("%v: request %s was refused at stage %s: %s%s",
		ErrArchitectureRefused, e.RequestID, e.Stage, e.Reason, renderRejected(e.Rejected))
	if e.ExchangeCloseErr != nil {
		out += "; its durable exchange record could not be closed and is still open: " +
			e.ExchangeCloseErr.Error()
	}
	return out
}

// Unwrap carries the transport's own condition AND the role-level one.
//
// roles.ErrArchitectRefusal is what a fallback ladder reads: the consumer
// REPLIED, so this request is answered and no other provider may be asked the
// same question. Stating it here rather than leaving the engine to recognise
// this package's sentinel keeps the ladder from having to import a transport to
// tell a refusal from a provider it could not reach.
func (e *ArchitectureRefused) Unwrap() []error {
	return []error{ErrArchitectureRefused, roles.ErrArchitectRefusal}
}

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
		for _, rej := range obs.Rejected {
			if seen[rej.identity()] {
				continue
			}
			seen[rej.identity()] = true
			rejected = append(rejected, rej)
		}
		// THE EARLIEST exact-bound terminal governs, whichever kind it is.
		//
		// A valid bound refusal is terminal for this request immediately: waiting
		// out the deadline after the consumer has already said why it stopped is
		// how an hour was spent on a diagnostic that existed in seconds. An
		// answer is terminal in exactly the same way, and asking the observation
		// for ONE settlement is what keeps a late answer from overturning a
		// refusal that already ended this exchange -- and a late refusal from
		// overturning an answer. A non-settling refusal changes nothing here: the
		// wait continues, and its rejection travels with whatever ends the wait.
		if settled, ok := obs.Settlement(); ok {
			if !settled.Refused() {
				return *settled.Answer, nil
			}
			refusal := settled.Refusal
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

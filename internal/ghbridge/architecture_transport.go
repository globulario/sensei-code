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
	// THE REQUEST CARRIES THE RESPONSE SCAFFOLD, rendered by the code that
	// renders responses; see ArchitectureResponseScaffold.
	body, err := architectureRequestBody(r)
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

// ArchitectureResponseRejected is one response-SHAPED comment that cannot
// settle this request, together with the reason it cannot: a well formed answer
// bound elsewhere, or a malformed response whose identity is not exactly this
// request's.
//
// DF-26. An answer-shaped comment used to be skipped with no record at all, so
// a delivered, authenticated, correctly bound answer missing one empty line was
// reported as silence. It is recorded now, and NOT attributed to the request
// being waited on: the diagnostic travels with whatever ends the wait.
type ArchitectureResponseRejected struct {
	Comment    int64
	Author     string
	AuthorID   int64
	Diagnostic string
}

// identity names one rejection, so the same comment seen on ten polls is
// reported once.
func (r ArchitectureResponseRejected) identity() string {
	return fmt.Sprintf("%d|%s", r.Comment, r.Diagnostic)
}

// ArchitectureMalformedAnswer is an authenticated response-shaped comment whose
// grammar the strict parser refused, but whose identity header -- every
// canonical field, in its canonical position, before the defect -- names THIS
// exact request.
//
// It is not an answer and cannot become one: it carries no decision, and the
// bytes are kept only as evidence. It is not a refusal either. It is the third
// terminal: the consumer replied to this request, and the reply is unreadable.
type ArchitectureMalformedAnswer struct {
	Binding    roles.ArchitectureBinding
	RequestID  string
	Diagnostic string
	Author     string
	AuthorID   int64
	Comment    int64
}

// ArchitectureTerminal is ONE artifact that can settle an exact request: the
// answer to it, the refusal of it, or a malformed answer to it.
//
// One type for all three because settlement has ONE ordering. Keeping answers
// and refusals in separate lists discarded the order they shared, and a reader
// then had to pick a rule -- answers first, refusals first, or by clock -- none
// of which is what the conversation says. Exactly one of the pointers is set.
type ArchitectureTerminal struct {
	Answer    *ArchitectureResponse
	Refusal   *ArchitectureRefusal
	Malformed *ArchitectureMalformedAnswer
	// Comment is the mailbox comment this terminal was read from, so a reader
	// can say WHICH artifact settled and in what order they arrived.
	Comment int64
	// raw is the comment's exact bytes, which P7 chooses a representative by.
	raw string
	// principal is who the comment authenticated as; see
	// Principal.authenticatedAs.
	principal string
}

// Refused reports whether this terminal ends the request negatively.
func (t ArchitectureTerminal) Refused() bool { return t.Refusal != nil }

// IsMalformed reports whether this terminal is a malformed answer to the request.
func (t ArchitectureTerminal) IsMalformed() bool { return t.Malformed != nil }

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
	// Unusable are the response-shaped comments this read saw that cannot
	// settle this request: answers bound elsewhere and malformed responses
	// whose identity is not exactly this request's.
	Unusable []ArchitectureResponseRejected
}

// Settlement is the ONE artifact that settles this request, when there is one.
//
// ok is false both when nothing settles the request and when it is in
// conflict; settlement says which.
func (o ArchitectureObservation) Settlement() (ArchitectureTerminal, bool) {
	outcome, settled, _ := o.settlement()
	return settled, outcome == settledOne
}

// settlement is P7 applied to one read of the architecture mailbox: none, one
// material answer, or conflict -- the same rule the review wait settles by.
//
// It replaces "the earliest exact-bound terminal governs". Earliest was right
// about one thing: an answer and a refusal must never both be acted on. It was
// wrong about how to prevent that, because it picked a winner by comment order
// between terminals that say different things. Materially different terminals
// are a conflict with every one retained; materially identical copies -- a
// duplicate wake, a republished refusal -- are one terminal, and which copy is
// named is a function of the exact bytes, not of which was posted first.
//
// MATERIAL is the complete validated artifact: the kind (answer or refusal),
// the request, the whole binding, the authenticated principal as
// authentication identified it (Principal.authenticatedAs), and for an answer
// the complete body -- the architecture JSON is not read here, so no part of it
// can be declared immaterial -- or for a refusal its stage and its reason. The
// comment id and time are not, nor is a display login beside a configured id.
func (o ArchitectureObservation) settlement() (settlementOutcome, ArchitectureTerminal, []ArchitectureTerminal) {
	cands := make([]settleable, len(o.Terminals))
	for i, t := range o.Terminals {
		cands[i] = settleable{material: t.material(), raw: t.raw, locator: t.Comment}
	}
	switch outcome, rep := settle(cands); outcome {
	case settledOne:
		return outcome, o.Terminals[rep], nil
	case settledConflict:
		return outcome, ArchitectureTerminal{}, o.Terminals
	default:
		return outcome, ArchitectureTerminal{}, nil
	}
}

// material is this terminal's material identity; see settlement.
func (t ArchitectureTerminal) material() string {
	type identity struct {
		Kind      string
		RequestID string
		Binding   roles.ArchitectureBinding
		Principal string
		Body      string
		Stage     RefusalStage
		Reason    string
	}
	var id identity
	switch {
	case t.Answer != nil:
		id = identity{Kind: "answer", RequestID: t.Answer.RequestID, Binding: t.Answer.Binding,
			Principal: t.principal, Body: t.Answer.Body}
	case t.Refusal != nil:
		id = identity{Kind: "refusal", RequestID: t.Refusal.RequestID, Binding: t.Refusal.Binding,
			Principal: t.principal, Stage: t.Refusal.Stage, Reason: t.Refusal.Reason}
	case t.Malformed != nil:
		// The exact bytes are the complete artifact: nothing in them was read
		// as a payload, so no part of them can be declared immaterial.
		id = identity{Kind: "malformed", RequestID: t.Malformed.RequestID, Binding: t.Malformed.Binding,
			Principal: t.principal, Body: t.raw}
	default:
		// Not a terminal at all; its exact bytes are the only identity it has.
		return "bytes|" + t.raw
	}
	blob, err := json.Marshal(id)
	if err != nil {
		return "bytes|" + t.raw
	}
	return string(blob)
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
		principal := box.ExpectedReviewer.authenticatedAs(c.User.ID, c.User.Login)
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
			obs.Terminals = append(obs.Terminals, ArchitectureTerminal{Refusal: &bound, Comment: c.ID, raw: c.Body, principal: principal})
			continue
		}
		// A body that does not open with the response marker is ordinary
		// content, exactly as before: prose that merely mentions a marker is
		// not an artifact.
		if !architectureResponseShaped(c.Body) {
			continue
		}
		unusable := ArchitectureResponseRejected{Comment: c.ID, Author: c.User.Login, AuthorID: c.User.ID}
		answer, ok := ParseArchitectureResponse(c.Body)
		if ok {
			if !answer.Answers(r) {
				unusable.Diagnostic = "a well formed architecture answer bound elsewhere cannot settle this request: " +
					answerMismatch(answer, r)
				obs.Unusable = append(obs.Unusable, unusable)
				continue
			}
			answer.Author, answer.AuthorID = c.User.Login, c.User.ID
			bound := answer
			obs.Terminals = append(obs.Terminals, ArchitectureTerminal{Answer: &bound, Comment: c.ID, raw: c.Body, principal: principal})
			continue
		}
		// MALFORMED. Parsing stays strict: nothing here repairs these bytes or
		// reads an answer out of them. Identity is established, if at all, by
		// the diagnostic-only positional recognizer, and only an identity that
		// is exactly this request's makes the comment this request's terminal.
		diagnostic := "the architecture response grammar refused it"
		if _, _, _, _, perr := parseArchitectureEnvelope(c.Body, architectureResponseMarker, false); perr != nil {
			diagnostic += ": " + perr.Error()
		}
		binding, id, identified := recognizeArchitectureResponseIdentity(c.Body)
		if !identified {
			unusable.Diagnostic = "a malformed architecture response whose request identity cannot be " +
				"established is attributed to no request; " + diagnostic
			obs.Unusable = append(obs.Unusable, unusable)
			continue
		}
		if m := answerMismatch(ArchitectureResponse{Binding: binding, RequestID: id}, r); m != "" {
			unusable.Diagnostic = "a malformed architecture response bound elsewhere cannot settle this request: " +
				m + "; " + diagnostic
			obs.Unusable = append(obs.Unusable, unusable)
			continue
		}
		obs.Terminals = append(obs.Terminals, ArchitectureTerminal{
			Malformed: &ArchitectureMalformedAnswer{
				Binding: binding, RequestID: id, Diagnostic: diagnostic,
				Author: c.User.Login, AuthorID: c.User.ID, Comment: c.ID,
			},
			Comment: c.ID, raw: c.Body, principal: principal,
		})
	}
	return obs, nil
}

// answerMismatch names the first identity on which a response differs from an
// open request, or "" when it binds to it exactly. The refusal's predicate, so
// an answer and a refusal bound elsewhere are described by one rule.
func answerMismatch(a ArchitectureResponse, q ArchitectureRequest) string {
	return ArchitectureRefusal{Binding: a.Binding, RequestID: a.RequestID}.mismatch(q)
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
	// Unusable are the response-shaped comments seen while waiting that could
	// not settle this request, carried for the same reason.
	Unusable []ArchitectureResponseRejected

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
	out := fmt.Sprintf("%v: request %s was refused at stage %s: %s%s%s",
		ErrArchitectureRefused, e.RequestID, e.Stage, e.Reason, renderRejected(e.Rejected),
		renderUnusable(e.Unusable))
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

// renderUnusable states response-shaped comments that could not settle a
// request. Empty when there were none, so an exchange that never saw one
// reports exactly what it reported before.
func renderUnusable(unusable []ArchitectureResponseRejected) string {
	if len(unusable) == 0 {
		return ""
	}
	b := strings.Builder{}
	fmt.Fprintf(&b, "; %d architecture-response-shaped comment(s) could not settle this request", len(unusable))
	for _, u := range unusable {
		fmt.Fprintf(&b, ": comment %d from %s: %s", u.Comment, u.Author, u.Diagnostic)
	}
	return b.String()
}

var ErrNoArchitectureAnswer = errors.New("no architecture answer answering that request was posted")

// ErrArchitectureAnswerMalformed reports that the consumer answered THIS exact
// request and its answer is unreadable under the strict response grammar.
var ErrArchitectureAnswerMalformed = errors.New("the remote consumer's answer to this exact architecture request is malformed")

// ArchitectureAnswerMalformed is that malformed answer as a wait outcome.
//
// Its own condition because none of the existing ones is true. The consumer
// replied, so nothing is unanswered; it did not refuse, so nothing was refused;
// and the reply decides nothing, so nothing was answered. It travels as an
// ERROR and matches neither ErrNoArchitectureAnswer nor ErrArchitectureRefused
// nor roles.ErrArchitectRefusal: a malformed answer is never read as a refusal
// and never as an answer.
type ArchitectureAnswerMalformed struct {
	ArchitectureMalformedAnswer
	Rejected []ArchitectureRefusalRejected
	Unusable []ArchitectureResponseRejected
	// ExchangeCloseErr records that this request's durable exchange record
	// could NOT be closed after the malformed answer settled it; see
	// ArchitectureRefused.ExchangeCloseErr.
	ExchangeCloseErr error
}

func (e *ArchitectureAnswerMalformed) Error() string {
	out := fmt.Sprintf("%v: request %s was answered by comment %d from %s, and %s%s%s",
		ErrArchitectureAnswerMalformed, e.RequestID, e.Comment, e.Author, e.Diagnostic,
		renderRejected(e.Rejected), renderUnusable(e.Unusable))
	if e.ExchangeCloseErr != nil {
		out += "; its durable exchange record could not be closed and is still open: " +
			e.ExchangeCloseErr.Error()
	}
	return out
}

func (e *ArchitectureAnswerMalformed) Unwrap() error { return ErrArchitectureAnswerMalformed }

// ErrArchitectureConflict reports materially different exact-bound terminals
// for one architecture request.
var ErrArchitectureConflict = errors.New("materially different terminals answer that architecture request")

// ArchitectureConflict is that conflict as a wait outcome: every exact-bound
// terminal the read saw, none of them chosen.
//
// It is neither an answer nor a refusal, so it matches neither
// ErrArchitectureRefused nor ErrNoArchitectureAnswer: the consumer replied, and
// its replies disagree.
type ArchitectureConflict struct {
	RequestID string
	Terminals []ArchitectureTerminal
	Rejected  []ArchitectureRefusalRejected
	Unusable  []ArchitectureResponseRejected
}

func (e *ArchitectureConflict) Error() string {
	b := strings.Builder{}
	fmt.Fprintf(&b, "%v: request %s has %d exact-bound terminals and no winner", ErrArchitectureConflict,
		e.RequestID, len(e.Terminals))
	for _, t := range e.Terminals {
		kind := "answer"
		switch {
		case t.Refused():
			kind = "refusal at stage " + string(t.Refusal.Stage)
		case t.IsMalformed():
			kind = "malformed answer"
		}
		fmt.Fprintf(&b, "; comment %d: %s", t.Comment, kind)
	}
	b.WriteString(renderRejected(e.Rejected))
	b.WriteString(renderUnusable(e.Unusable))
	return b.String()
}

func (e *ArchitectureConflict) Unwrap() error { return ErrArchitectureConflict }

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
	//
	// EVERY exit carries them (ruling 47): an answer, a refusal, a malformed
	// answer, a conflict, the deadline and a mailbox read error alike. They are
	// provenance about what the mailbox held, not a settlement outcome, so a
	// later answer does not erase them and a final error does not discard them.
	var rejected []ArchitectureRefusalRejected
	var unusable []ArchitectureResponseRejected
	seen := map[string]bool{}
	seenUnusable := map[string]bool{}
	for {
		obs, err := ObserveArchitecture(ctx, box, r)
		if err != nil {
			// The deadline landing inside the read is the same fact as it
			// landing in the select below -- no answer arrived -- and it must
			// be reported the same way, rejections included.
			if ctx.Err() != nil {
				return ArchitectureResponse{}, fmt.Errorf("%w: %v%s%s",
					ErrNoArchitectureAnswer, ctx.Err(), renderRejected(rejected), renderUnusable(unusable))
			}
			return ArchitectureResponse{}, fmt.Errorf("reading the architecture mailbox: %w%s%s",
				err, renderRejected(rejected), renderUnusable(unusable))
		}
		for _, rej := range obs.Rejected {
			if seen[rej.identity()] {
				continue
			}
			seen[rej.identity()] = true
			rejected = append(rejected, rej)
		}
		for _, u := range obs.Unusable {
			if seenUnusable[u.identity()] {
				continue
			}
			seenUnusable[u.identity()] = true
			unusable = append(unusable, u)
		}
		// P7 settles the request, whichever kind of terminal settles it.
		//
		// A valid bound refusal is terminal for this request immediately: waiting
		// out the deadline after the consumer has already said why it stopped is
		// how an hour was spent on a diagnostic that existed in seconds. An
		// answer is terminal in exactly the same way. An answer and a refusal
		// together are materially different terminals and CONFLICT: neither is
		// acted on, and neither is chosen by which was posted first. A
		// non-settling refusal changes nothing here: the wait continues, and its
		// rejection travels with whatever ends the wait.
		//
		// A malformed answer bound exactly to this request is terminal in the
		// same way: the consumer replied and the reply is unreadable, so waiting
		// out the deadline would report a delivered answer as silence. It is
		// materially different from any valid answer or refusal, so beside one
		// it is a CONFLICT, whichever was posted first.
		switch outcome, settled, conflicting := obs.settlement(); outcome {
		case settledOne:
			switch {
			case settled.IsMalformed():
				return ArchitectureResponse{}, &ArchitectureAnswerMalformed{
					ArchitectureMalformedAnswer: *settled.Malformed,
					Rejected:                    rejected, Unusable: unusable,
				}
			case settled.Refused():
				refusal := settled.Refusal
				return ArchitectureResponse{}, &ArchitectureRefused{
					RequestID: refusal.RequestID, Binding: refusal.Binding,
					Stage: refusal.Stage, Reason: refusal.Reason,
					Author: refusal.Author, AuthorID: refusal.AuthorID, Comment: refusal.Comment,
					Rejected: rejected, Unusable: unusable,
				}
			}
			answer := *settled.Answer
			answer.Rejected, answer.Unusable = rejected, unusable
			return answer, nil
		case settledConflict:
			return ArchitectureResponse{}, &ArchitectureConflict{
				RequestID: r.RequestID, Terminals: conflicting, Rejected: rejected, Unusable: unusable,
			}
		}
		select {
		case <-ctx.Done():
			return ArchitectureResponse{}, fmt.Errorf("%w: %v%s%s",
				ErrNoArchitectureAnswer, ctx.Err(), renderRejected(rejected), renderUnusable(unusable))
		case <-time.After(every):
		}
	}
}

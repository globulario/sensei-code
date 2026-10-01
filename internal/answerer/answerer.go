package answerer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Validator is a contract check injected by the composition root: the
// workflow's own strict validator for the request's kind. It receives the
// model's original bytes and must not change them; this package owns no
// contract rule of its own.
type Validator func(body string) error

// Validators holds one validator per answered kind. Both are required.
type Validators struct {
	Architecture Validator
	Review       Validator
}

// The fixed role contracts. They are code, not configuration and not request
// text: the request is shown to the model as evidence, and nothing in it can
// replace these.
const (
	architectureSystem = `You are the architect answering one governed architecture request from Sensei Code.
The user message is the exact request. Answer it on its own; you have no earlier conversation.
Return ONLY the architecture JSON object the request describes, with nothing before or after it.
Do not write any [sensei-code:...] envelope: the transport attaches the envelope the request determines.
If you cannot answer from what the request contains, say so inside the JSON contract rather than guessing.`

	reviewSystem = `You are an ADVISORY reviewer answering one review request from Sensei Code.
The user message is the exact request. Judge it on its own; you have no earlier conversation.
Return ONLY the reviewer JSON payload the request describes, with nothing before or after it.
Do not write the [sensei-code:review] envelope or any other envelope: the transport prepends the
envelope the request embeds. Your review is advisory and establishes no independent review.`
)

// maxCommentBytes is GitHub's comment bound and the review artifact bound. An
// answer that would cross it is refused here rather than truncated anywhere.
const maxCommentBytes = 64 << 10

// Outcome is what happened to one request in one pass.
type Outcome struct {
	Kind    Kind
	Request string
	// Result is one of the Result constants.
	Result string
	Detail string
}

const (
	// ResultPosted: the answer was posted, unchanged, for exactly this request.
	ResultPosted = "posted"
	// ResultTransportFailure: nothing was posted; the model call failed or its
	// output did not conform. This is never a verdict about the request.
	ResultTransportFailure = "transport-failure"
	// ResultSkipped: the request stopped being live before the post.
	ResultSkipped = "skipped"
	// ResultMalformedRequest: a request-shaped comment from the requester did
	// not parse, so it was not answered.
	ResultMalformedRequest = "malformed-request"
)

// Answerer answers live requests on one mailbox.
type Answerer struct {
	Mailbox    Mailbox
	Model      Model
	Validators Validators
	Responder  identity
	Requester  identity
	// MailboxRepository is the configured mailbox's "owner/name"; a request
	// must name exactly it as its mailbox_repository to be answered.
	MailboxRepository string
	LockPath          string
	// Every is the pause between passes.
	Every time.Duration
	// Report receives every outcome. Nil writes to stderr.
	Report func(Outcome)

	// reported holds malformed request comments already reported, so each is
	// reported once. It never decides whether a request is answered: that is
	// mailbox state alone, and a request still live after a transport failure
	// gets a fresh call on the next pass.
	reported map[int64]bool
}

// New composes an answerer from configuration and injected validators.
func New(c Config, v Validators) (*Answerer, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &Answerer{
		Mailbox:           NewGitHubMailbox(c),
		Model:             NewChatModel(c),
		Validators:        v,
		Responder:         identity{login: c.ResponderLogin, id: c.ResponderID},
		Requester:         identity{login: c.RequesterLogin, id: c.RequesterID},
		MailboxRepository: c.MailboxRepository,
		LockPath:          c.LockPath,
		Every:             time.Minute,
	}, nil
}

// ErrAlreadyRunning refuses a second writer.
var ErrAlreadyRunning = errors.New("another answerer holds the lock")

// Lock is the single-writer lock: a file created exclusively and holding the
// owner's pid. A lock left by a crashed process is NOT reclaimed automatically,
// because a live holder and a dead one look the same from here; the operator
// confirms no answerer runs and removes the file.
type Lock struct{ path string }

// AcquireLock takes the lock or refuses.
func AcquireLock(path string) (*Lock, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			holder, _ := os.ReadFile(path)
			return nil, fmt.Errorf("%w: %s (holder: %s)", ErrAlreadyRunning, path, strings.TrimSpace(string(holder)))
		}
		return nil, err
	}
	_, werr := fmt.Fprintf(f, "pid=%d\n", os.Getpid())
	cerr := f.Close()
	if werr != nil || cerr != nil {
		os.Remove(path)
		return nil, fmt.Errorf("writing the lock %s: %v %v", path, werr, cerr)
	}
	return &Lock{path: path}, nil
}

// Release gives the lock up.
func (l *Lock) Release() error { return os.Remove(l.path) }

func (a *Answerer) ready() error {
	switch {
	case a.Mailbox == nil || a.Model == nil:
		return errors.New("answerer needs a mailbox and a model")
	case a.Validators.Architecture == nil || a.Validators.Review == nil:
		return errors.New("answerer needs an architecture and a review validator; it has no contract of its own to fall back on")
	case a.Responder.id == "" || a.Requester.id == "":
		return errors.New("answerer needs a configured responder and requester")
	case a.MailboxRepository == "":
		return errors.New("answerer needs the configured mailbox repository to check request routing against")
	}
	return nil
}

// Run holds the lock and answers until ctx ends. A second instance refuses to
// start. A mailbox read failure ends the run: an unreadable mailbox cannot say
// whether an answer already exists.
func (a *Answerer) Run(ctx context.Context) error {
	if err := a.ready(); err != nil {
		return err
	}
	lock, err := AcquireLock(a.LockPath)
	if err != nil {
		return err
	}
	defer lock.Release()
	every := a.Every
	if every <= 0 {
		every = time.Minute
	}
	for {
		if err := a.Once(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(every):
		}
	}
}

// Once makes one pass over the mailbox. The caller must hold the lock.
func (a *Answerer) Once(ctx context.Context) error {
	if err := a.ready(); err != nil {
		return err
	}
	if a.reported == nil {
		a.reported = map[int64]bool{}
	}
	comments, err := a.Mailbox.List(ctx)
	if err != nil {
		return fmt.Errorf("reading the mailbox: %w", err)
	}
	state := readState(comments, a.Responder, a.Requester, a.MailboxRepository)
	for id, perr := range state.refused {
		if !a.reported[id] {
			a.reported[id] = true
			a.report(Outcome{Request: fmt.Sprintf("comment:%d", id), Result: ResultMalformedRequest, Detail: perr.Error()})
		}
	}
	for _, r := range state.requests {
		if live, _ := state.live(r); !live {
			continue
		}
		if err := a.answer(ctx, r); err != nil {
			return err
		}
	}
	return nil
}

// answer spends one fresh model call on r and posts the result for r alone.
// The returned error is one the run cannot continue past; everything about r
// itself is an Outcome.
func (a *Answerer) answer(ctx context.Context, r Request) error {
	system, validate := architectureSystem, a.Validators.Architecture
	if r.Kind == KindReview {
		system, validate = reviewSystem, a.Validators.Review
	}
	fail := func(detail string) error {
		a.report(Outcome{Kind: r.Kind, Request: r.ID, Result: ResultTransportFailure, Detail: detail})
		return nil
	}
	out, err := a.Model.Answer(ctx, Turn{Kind: r.Kind, System: system, Request: r.Comment.Body})
	if err != nil {
		return fail(err.Error())
	}
	if why := protocolViolation(out); why != "" {
		return fail(why)
	}
	if err := validate(out); err != nil {
		return fail("the answer does not conform to the " + string(r.Kind) + " contract: " + err.Error())
	}
	body := r.Envelope + out
	if len(body) > maxCommentBytes {
		return fail(fmt.Sprintf("the answer is %d bytes with its envelope; the bound is %d", len(body), maxCommentBytes))
	}
	// AT MOST ONCE: the model call took time, so liveness is read again from the
	// mailbox immediately before posting.
	comments, err := a.Mailbox.List(ctx)
	if err != nil {
		return fmt.Errorf("re-reading the mailbox before posting %s: %w", r.ID, err)
	}
	if live, why := readState(comments, a.Responder, a.Requester, a.MailboxRepository).live(r); !live {
		a.report(Outcome{Kind: r.Kind, Request: r.ID, Result: ResultSkipped, Detail: why})
		return nil
	}
	posted, err := a.Mailbox.Post(ctx, body)
	if err != nil {
		// Whether the post landed is unknown, so the run stops: continuing could
		// post an answer the mailbox already holds under an unreadable reply.
		return fmt.Errorf("posting the answer to %s: %w", r.ID, err)
	}
	if !a.Responder.is(posted) {
		return fmt.Errorf("the answer to %s was posted as %s (id %d), not the configured responder %s; "+
			"stopping, because answers from this credential cannot be recognised as already given",
			r.ID, posted.AuthorLogin, posted.AuthorID, a.Responder.login)
	}
	a.report(Outcome{Kind: r.Kind, Request: r.ID, Result: ResultPosted, Detail: fmt.Sprintf("comment %d", posted.ID)})
	return nil
}

// protocolViolation names what makes output unpostable as transport, before
// any contract is consulted: an envelope the model tried to write, or citation
// artifacts a chat surface leaves in text. Contract conformance is the
// validator's; this reads nothing about meaning.
func protocolViolation(out string) string {
	if strings.HasPrefix(strings.TrimSpace(out), "[sensei-code:") {
		return "the model wrote a protocol envelope; the envelope is the request's, never the model's"
	}
	for _, r := range out {
		if r >= 0xE000 && r <= 0xF8FF {
			return fmt.Sprintf("the answer carries a private-use citation artifact (U+%04X)", r)
		}
	}
	for _, artifact := range []string{"oaicite:", "【", "citeturn", "filecite"} {
		if strings.Contains(out, artifact) {
			return fmt.Sprintf("the answer carries a citation artifact %q", artifact)
		}
	}
	return ""
}

func (a *Answerer) report(o Outcome) {
	if a.Report != nil {
		a.Report(o)
		return
	}
	fmt.Fprintf(os.Stderr, "answerer: %s %s %s: %s\n", o.Result, o.Kind, o.Request, o.Detail)
}

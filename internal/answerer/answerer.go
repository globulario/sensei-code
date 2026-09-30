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

// Protocol is the bridge's own reading and writing of the mailbox, injected by
// the composition root over the canonical ghbridge functions. This package
// imports no bridge and holds no request, reply or withdrawal grammar: which
// comment is a governed request, which request a withdrawal retracts, how a
// reply to a request is spelled, and whether a reply answers a request are all
// the bridge's answers. Every field is required; an answerer with any of them
// missing refuses to run rather than fall back on a reading of its own.
type Protocol struct {
	// ArchitectureRequest and ReviewRequest read a body with the bridge's
	// request parsers. ok=false means the bridge does not recognise it as a
	// governed request of that kind, and it is not answered.
	ArchitectureRequest func(body string) (CanonicalRequest, bool)
	ReviewRequest       func(body string) (CanonicalRequest, bool)
	// ArchitectureReply and ReviewReply render the complete reply to the
	// request in requestBody, carrying payload unchanged, with the bridge's
	// canonical renderer. The envelope is derived from the request alone.
	ArchitectureReply func(requestBody, payload string) (string, error)
	ReviewReply       func(requestBody, payload string) (string, error)
	// ArchitectureAnswers and ReviewAnswers report whether replyBody answers
	// exactly the request in requestBody, by the bridge's own parsers and
	// binding predicate.
	ArchitectureAnswers func(requestBody, replyBody string) bool
	ReviewAnswers       func(requestBody, replyBody string) bool
	// Withdrawal reads a body with the bridge's withdrawal parser and returns
	// the request it retracts. It is consulted only for a comment already
	// authenticated as the requester's.
	Withdrawal func(body string) (string, bool)
}

// CanonicalRequest is what the bridge read from a request.
type CanonicalRequest struct {
	ID                string
	TaskID            string
	MailboxRepository string
}

func (p Protocol) complete() bool {
	return p.ArchitectureRequest != nil && p.ReviewRequest != nil &&
		p.ArchitectureReply != nil && p.ReviewReply != nil &&
		p.ArchitectureAnswers != nil && p.ReviewAnswers != nil &&
		p.Withdrawal != nil
}

// recognise asks the bridge which kind of governed request c carries. A body
// both parsers claim is not one request, and is not answered.
func (p Protocol) recognise(c Comment) (Request, bool) {
	arch, isArch := p.ArchitectureRequest(c.Body)
	review, isReview := p.ReviewRequest(c.Body)
	var q CanonicalRequest
	var kind Kind
	switch {
	case isArch && !isReview:
		q, kind = arch, KindArchitecture
	case isReview && !isArch:
		q, kind = review, KindReview
	default:
		return Request{}, false
	}
	if strings.TrimSpace(q.ID) == "" || strings.TrimSpace(q.TaskID) == "" {
		return Request{}, false
	}
	return Request{Kind: kind, ID: q.ID, TaskID: q.TaskID, MailboxRepository: q.MailboxRepository, Comment: c}, true
}

func (p Protocol) answers(r Request, reply string) bool {
	if r.Kind == KindReview {
		return p.ReviewAnswers(r.Comment.Body, reply)
	}
	return p.ArchitectureAnswers(r.Comment.Body, reply)
}

func (p Protocol) reply(r Request, payload string) (string, error) {
	if r.Kind == KindReview {
		return p.ReviewReply(r.Comment.Body, payload)
	}
	return p.ArchitectureReply(r.Comment.Body, payload)
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
	// ResultMisrouted: a request addressed to another mailbox was not answered.
	ResultMisrouted = "misrouted"
)

// Answerer answers live requests on one mailbox.
type Answerer struct {
	Mailbox    Mailbox
	Model      Model
	Validators Validators
	Protocol   Protocol
	Responder  identity
	Requester  identity
	// MailboxRepository is the configured mailbox's "owner/name"; a request
	// must name exactly it as its mailbox repository to be answered.
	MailboxRepository string
	LockPath          string
	// Every is the pause between passes.
	Every time.Duration
	// Report receives every outcome. Nil writes to stderr.
	Report func(Outcome)

	// reported holds misrouted request comments already reported, so each is
	// reported once. It never decides whether a request is answered: that is
	// mailbox state alone, and a request still live after a transport failure
	// gets a fresh call on the next pass.
	reported map[int64]bool
}

// New composes an answerer from configuration, the injected validators and the
// bridge's injected protocol.
func New(c Config, v Validators, p Protocol) (*Answerer, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	a := &Answerer{
		Mailbox:           NewGitHubMailbox(c),
		Model:             NewChatModel(c),
		Validators:        v,
		Protocol:          p,
		Responder:         identity{login: c.ResponderLogin, id: c.ResponderID},
		Requester:         identity{login: c.RequesterLogin, id: c.RequesterID},
		MailboxRepository: c.MailboxRepository,
		LockPath:          c.LockPath,
		Every:             time.Minute,
	}
	if err := a.ready(); err != nil {
		return nil, err
	}
	return a, nil
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
	case !a.Protocol.complete():
		return errors.New("answerer needs the bridge's request, reply, answer and withdrawal functions for both kinds; " +
			"it has no protocol grammar of its own to fall back on")
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
	state := readState(comments, a.Responder, a.Requester, a.MailboxRepository, a.Protocol)
	for id, why := range state.misrouted {
		if !a.reported[id] {
			a.reported[id] = true
			a.report(Outcome{Request: fmt.Sprintf("comment:%d", id), Result: ResultMisrouted, Detail: why})
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
	body, err := a.Protocol.reply(r, out)
	if err != nil {
		return fail("the bridge could not render a reply to this request: " + err.Error())
	}
	if why := a.unboundReply(r, body, out); why != "" {
		return fail(why)
	}
	if len(body) > maxCommentBytes {
		return fail(fmt.Sprintf("the answer is %d bytes with its envelope; the bound is %d", len(body), maxCommentBytes))
	}
	// AT MOST ONCE: the model call took time, so liveness is read again from the
	// mailbox immediately before posting.
	comments, err := a.Mailbox.List(ctx)
	if err != nil {
		return fmt.Errorf("re-reading the mailbox before posting %s: %w", r.ID, err)
	}
	if live, why := readState(comments, a.Responder, a.Requester, a.MailboxRepository, a.Protocol).live(r); !live {
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
	if posted.Body != body {
		return fmt.Errorf("the answer to %s was stored as different bytes than were posted; "+
			"stopping, because an answer that changed in transit may not be recognised as already given", r.ID)
	}
	a.report(Outcome{Kind: r.Kind, Request: r.ID, Result: ResultPosted, Detail: fmt.Sprintf("comment %d", posted.ID)})
	return nil
}

// unboundReply names why the rendered reply may not be posted for r, or "".
//
// The renderer is the bridge's, and so is the judgement: the reply must carry
// the model's bytes unchanged after a non-empty envelope, a review envelope
// must be bytes the request itself embeds, and the bridge's own answer
// predicate must bind the whole reply to exactly r. A reply the consumer
// would not read as the answer to r is a protocol violation, never posted.
func (a *Answerer) unboundReply(r Request, body, out string) string {
	if !strings.HasSuffix(body, out) || len(body) == len(out) {
		return "the rendered reply does not carry the answer unchanged after an envelope"
	}
	envelope := body[:len(body)-len(out)]
	if r.Kind == KindReview && !strings.Contains(r.Comment.Body, envelope) {
		return "the rendered review envelope is not the envelope the request embeds"
	}
	if !a.Protocol.answers(r, body) {
		return "the rendered reply does not answer this request by the bridge's own reading"
	}
	return ""
}

// protocolViolation names what makes output unpostable as transport, before
// any contract is consulted: an envelope the model wrote, or a citation
// artifact a chat surface leaves in text. It reads nothing about meaning.
//
// An envelope is any line that opens, after leading whitespace or a byte-order
// mark, with the sensei-code envelope namespace. Every answered payload is a
// JSON object, in which such a line cannot occur inside a string, so this
// refuses no schema-valid answer while leaving the model no position from
// which it could write an envelope of its own.
func protocolViolation(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimLeft(line, " \t\r\ufeff"), envelopeNamespace) {
			return "the model wrote a protocol envelope; the envelope is the request's, never the model's"
		}
	}
	if artifact := citationArtifact(out); artifact != "" {
		return fmt.Sprintf("the answer carries a citation artifact %q", artifact)
	}
	return ""
}

// envelopeNamespace opens every sensei-code envelope. It names the namespace
// only; no envelope's grammar is read here.
const envelopeNamespace = "[sensei-code:"

// citationArtifact returns the first citation a chat surface left in out, or
// "". It recognises citeturn0search1 (also as fileciteturn0file2),
// [oaicite:0], 【4:0†source】, and ANY of the private-use runes U+E200, U+E201
// and U+E202 a chat surface frames an inline citation with -- framed or not,
// since a stray one is the residue of a citation and never JSON content. A bare
// word such as filecite or citeturn is payload content, never an artifact.
func citationArtifact(out string) string {
	for i := 0; i < len(out); i++ {
		rest := out[i:]
		switch {
		case strings.HasPrefix(rest, "citeturn"):
			// turn<digits><kind letters><digits>, e.g. citeturn0search1.
			n := 8
			if d := digitRun(rest[n:]); d > 0 {
				n += d
				if l := letterRun(rest[n:]); l > 0 {
					n += l
					if d := digitRun(rest[n:]); d > 0 {
						return rest[:n+d]
					}
				}
			}
		case strings.HasPrefix(rest, "[oaicite:"):
			if d := digitRun(rest[9:]); d > 0 && strings.HasPrefix(rest[9+d:], "]") {
				return rest[:9+d+1]
			}
		case strings.HasPrefix(rest, citeOpen), strings.HasPrefix(rest, citeClose), strings.HasPrefix(rest, citeSep):
			return rest[:len(citeOpen)]
		case strings.HasPrefix(rest, "【"):
			// 【<digits>:<digits>†...】
			n := len("【")
			if d := digitRun(rest[n:]); d > 0 && strings.HasPrefix(rest[n+d:], ":") {
				n += d + 1
				if d := digitRun(rest[n:]); d > 0 && strings.HasPrefix(rest[n+d:], "†") {
					if end := strings.Index(rest, "】"); end > 0 {
						return rest[:end+len("】")]
					}
				}
			}
		}
	}
	return ""
}

// The private-use runes that frame a chat surface's inline citation. Each is
// three bytes in UTF-8.
const (
	citeOpen  = "\ue200"
	citeClose = "\ue201"
	citeSep   = "\ue202"
)

func digitRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= '0' && s[n] <= '9' {
		n++
	}
	return n
}

func letterRun(s string) int {
	n := 0
	for n < len(s) && s[n] >= 'a' && s[n] <= 'z' {
		n++
	}
	return n
}

func (a *Answerer) report(o Outcome) {
	if a.Report != nil {
		a.Report(o)
		return
	}
	fmt.Fprintf(os.Stderr, "answerer: %s %s %s: %s\n", o.Result, o.Kind, o.Request, o.Detail)
}

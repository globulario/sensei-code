package answerer

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Kind is the closed set of request kinds this answerer serves, read by
// membership. Anything else on the mailbox -- a human approval question, an
// objective proposal, an attestation, a relayed review, a wake -- is not a
// request here and is never answered, deferred or recorded.
type Kind string

const (
	KindArchitecture Kind = "architecture"
	KindReview       Kind = "review"
)

// The mailbox envelopes this package reads. It cannot import the bridge, so
// these are the bridge's spellings, read here and never emitted except for the
// architecture answer envelope, whose fields come from the request itself.
const (
	architectureRequestMarker  = "[sensei-code:architecture-request]"
	architectureResponseMarker = "[sensei-code:architecture]"
	architectureRefusalMarker  = "[sensei-code:refused]"
	reviewRequestMarker        = "[sensei-code:review-request]"
	reviewResponseMarker       = "[sensei-code:review]"
	withdrawnMarker            = "[sensei-code:withdrawn]"
)

// Comment is one mailbox comment as the transport observed it.
type Comment struct {
	ID          int64
	AuthorID    string
	AuthorLogin string
	Body        string
}

// Mailbox is the one configured conversation. Post takes no destination: where
// an answer goes was fixed when the Mailbox was built from operator
// configuration, so no request, model output or caller can redirect it.
type Mailbox interface {
	List(ctx context.Context) ([]Comment, error)
	Post(ctx context.Context, body string) (Comment, error)
}

// Request is one publisher-authored request, bound exactly.
type Request struct {
	Kind      Kind
	Comment   int64
	TaskID    string
	RequestID string
	// Body is the request comment's exact bytes, and the only thing the model sees.
	Body string
	// Envelope is the reply envelope, taken only from the request: built from
	// its own header values for architecture, and the envelope it embeds
	// verbatim for review. The model never writes or chooses it.
	Envelope string
}

// Candidate is a request together with its liveness as the mailbox states it.
type Candidate struct {
	Request
	Live   bool
	Reason string
}

// Classify reads liveness from mailbox state and from nothing else. A request
// is live iff it exists exactly, is unanswered by the configured responder, is
// not withdrawn or superseded, and is the current open request for its task.
//
// Only comments authored by the configured publisher are requests. A
// withdrawal is honoured from ANY author: it can only suppress an answer,
// never cause one, so honouring it fails closed.
func Classify(comments []Comment, publisher, responder Principal) []Candidate {
	var requests []Request
	withdrawn := map[string]bool{}
	answered := map[string]bool{}
	for _, c := range comments {
		body := strings.ReplaceAll(c.Body, "\r\n", "\n")
		if id, ok := parseWithdrawal(body); ok {
			withdrawn[id] = true
			continue
		}
		if responder.Matches(c.AuthorID, c.AuthorLogin) {
			for _, marker := range []string{architectureResponseMarker, architectureRefusalMarker, reviewResponseMarker} {
				if id := headerRequest(body, marker); id != "" {
					answered[id] = true
				}
			}
		}
		if !publisher.Matches(c.AuthorID, c.AuthorLogin) {
			continue
		}
		if r, ok := parseArchitectureRequest(body); ok {
			r.Comment, r.Body = c.ID, c.Body
			requests = append(requests, r)
		} else if r, ok := parseReviewRequest(body); ok {
			r.Comment, r.Body = c.ID, c.Body
			requests = append(requests, r)
		}
	}
	ids := map[string]int{}
	latest := map[string]int64{}
	for _, r := range requests {
		ids[r.RequestID]++
		if r.Comment > latest[r.TaskID] {
			latest[r.TaskID] = r.Comment
		}
	}
	out := make([]Candidate, 0, len(requests))
	for _, r := range requests {
		c := Candidate{Request: r}
		switch {
		case ids[r.RequestID] > 1:
			c.Reason = "the request id is published more than once, so no answer can be bound to one of them"
		case answered[r.RequestID]:
			c.Reason = "the configured responder has already answered it"
		case withdrawn[r.RequestID]:
			c.Reason = "it is withdrawn"
		case latest[r.TaskID] != r.Comment:
			c.Reason = "a later request for its task supersedes it"
		default:
			c.Live = true
		}
		out = append(out, c)
	}
	return out
}

// AnsweredRequest names the request a composed answer is bound to, read back
// from the answer's own envelope.
func AnsweredRequest(body string) string {
	for _, marker := range []string{architectureResponseMarker, reviewResponseMarker} {
		if id := headerRequest(body, marker); id != "" {
			return id
		}
	}
	return ""
}

// headerRequest reads request=<id> from the key=value header that follows a
// position-zero marker, or "" when body does not open with that envelope.
func headerRequest(body, marker string) string {
	if !strings.HasPrefix(body, marker+"\n") {
		return ""
	}
	for _, line := range strings.Split(body[len(marker)+1:], "\n") {
		key, value, ok := fieldLine(line)
		if !ok {
			break
		}
		if key == "request" {
			return value
		}
	}
	return ""
}

func parseWithdrawal(body string) (string, bool) {
	if !strings.HasPrefix(body, withdrawnMarker+"\n") {
		return "", false
	}
	key, value, ok := fieldLine(strings.TrimRight(body[len(withdrawnMarker)+1:], " \t\n"))
	if !ok || key != "request" {
		return "", false
	}
	return value, true
}

// fieldLine reads one exact key=value header line: a lowercase key, and a
// value with no whitespace.
func fieldLine(line string) (string, string, bool) {
	key, value, ok := strings.Cut(line, "=")
	if !ok || key == "" || value == "" || strings.Trim(key, "abcdefghijklmnopqrstuvwxyz_") != "" ||
		strings.ContainsAny(value, " \t\r\n") {
		return "", "", false
	}
	return key, value, true
}

func requestIDShape(id string) bool {
	return len(id) <= 128 && id != "" &&
		strings.Trim(id, "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz_.:-") == ""
}

// architectureBinding is the response envelope's field order.
var architectureBinding = []string{"task", "request", "objective_digest", "base", "graph_repository", "graph_build_commit"}

// parseArchitectureRequest reads an architecture request strictly: a header of
// exact key=value lines ending at the first blank line, no duplicate or unknown
// field, kind=architecture, every binding field present, and a payload.
func parseArchitectureRequest(body string) (Request, bool) {
	if !strings.HasPrefix(body, architectureRequestMarker+"\n") {
		return Request{}, false
	}
	header, payload, ok := strings.Cut(body[len(architectureRequestMarker)+1:], "\n\n")
	if !ok || strings.TrimSpace(payload) == "" {
		return Request{}, false
	}
	allowed := map[string]bool{"kind": true, "mailbox_repository": true, "workspace_repository": true}
	for _, k := range architectureBinding {
		allowed[k] = true
	}
	f, ok := strictFields(strings.Split(header, "\n"), allowed)
	if !ok || f["kind"] != string(KindArchitecture) {
		return Request{}, false
	}
	var env strings.Builder
	env.WriteString(architectureResponseMarker + "\n")
	for _, k := range architectureBinding {
		if f[k] == "" {
			return Request{}, false
		}
		env.WriteString(k + "=" + f[k] + "\n")
	}
	env.WriteString("\n")
	if !requestIDShape(f["request"]) {
		return Request{}, false
	}
	return Request{Kind: KindArchitecture, TaskID: f["task"], RequestID: f["request"], Envelope: env.String()}, true
}

// reviewIdentity is the identity a review reply envelope carries.
var reviewIdentity = []string{"task", "request", "base", "candidate_digest", "candidate_tree", "review_commit", "reviewer"}

// parseReviewRequest reads a review request and the reply envelope it embeds.
//
// The embedded envelope is used VERBATIM, and only when exactly one embedded
// envelope carries precisely the request header's own identity. An envelope in
// the request's prose naming any other value is not this request's reply
// envelope, and two candidates are an ambiguity refused rather than resolved.
func parseReviewRequest(body string) (Request, bool) {
	if !strings.HasPrefix(body, reviewRequestMarker+"\n") {
		return Request{}, false
	}
	lines := strings.Split(body[len(reviewRequestMarker)+1:], "\n")
	var header []string
	for _, ln := range lines {
		if _, _, ok := fieldLine(ln); !ok {
			break
		}
		header = append(header, ln)
	}
	allowed := map[string]bool{"kind": true, "mailbox_repository": true, "workspace_repository": true}
	for _, k := range reviewIdentity {
		allowed[k] = true
	}
	f, ok := strictFields(header, allowed)
	if !ok || f["kind"] != string(KindReview) || !requestIDShape(f["request"]) {
		return Request{}, false
	}
	for _, k := range reviewIdentity {
		if f[k] == "" {
			return Request{}, false
		}
	}
	var envelopes []string
	all := strings.Split(body, "\n")
	for i, ln := range all {
		if i == 0 || ln != reviewResponseMarker {
			continue
		}
		var own []string
		for _, next := range all[i+1:] {
			if _, _, ok := fieldLine(next); !ok {
				break
			}
			own = append(own, next)
		}
		ef, ok := strictFields(own, identitySet())
		if !ok || len(ef) != len(reviewIdentity) {
			continue
		}
		same := true
		for _, k := range reviewIdentity {
			same = same && ef[k] == f[k]
		}
		if same {
			envelopes = append(envelopes, reviewResponseMarker+"\n"+strings.Join(own, "\n")+"\n")
		}
	}
	if len(envelopes) != 1 {
		return Request{}, false
	}
	return Request{Kind: KindReview, TaskID: f["task"], RequestID: f["request"], Envelope: envelopes[0]}, true
}

func identitySet() map[string]bool {
	s := map[string]bool{}
	for _, k := range reviewIdentity {
		s[k] = true
	}
	return s
}

// strictFields reads header lines, refusing a malformed line, a duplicate key
// or a key outside allowed. Ambiguity never decides which request is meant.
func strictFields(lines []string, allowed map[string]bool) (map[string]string, bool) {
	f := map[string]string{}
	for _, ln := range lines {
		key, value, ok := fieldLine(ln)
		if !ok || !allowed[key] {
			return nil, false
		}
		if _, dup := f[key]; dup {
			return nil, false
		}
		f[key] = value
	}
	return f, true
}

// GitHubMailbox is the configured pull request conversation, read and written
// through GitHub's issue-comment REST resource as the responder credential.
type GitHubMailbox struct {
	url   string
	token string
	do    jsonDoer
}

// NewGitHubMailbox addresses exactly the configured conversation. token is
// credential content and is only ever placed in the Authorization header.
func NewGitHubMailbox(cfg Config, token string) *GitHubMailbox {
	return &GitHubMailbox{
		url: fmt.Sprintf("%s/repos/%s/issues/%s/comments",
			strings.TrimRight(cfg.GitHubAPI, "/"), cfg.MailboxRepository, cfg.MailboxNumber),
		token: token,
		do:    newJSONDoer(mailboxTimeout),
	}
}

type ghComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	} `json:"user"`
}

func (c ghComment) comment() Comment {
	return Comment{ID: c.ID, AuthorID: fmt.Sprint(c.User.ID), AuthorLogin: c.User.Login, Body: c.Body}
}

func (g *GitHubMailbox) headers() map[string]string {
	return map[string]string{
		"Authorization":        "Bearer " + g.token,
		"Accept":               "application/vnd.github+json",
		"X-GitHub-Api-Version": "2022-11-28",
	}
}

const pageSize = 100

// List reads every comment, following pages until a short one.
func (g *GitHubMailbox) List(ctx context.Context) ([]Comment, error) {
	var out []Comment
	for page := 1; ; page++ {
		if page > 1000 {
			return nil, errors.New("the mailbox has more than 1000 pages of comments; refusing to read a partial mailbox")
		}
		var batch []ghComment
		url := fmt.Sprintf("%s?per_page=%d&page=%d", g.url, pageSize, page)
		if err := g.do(ctx, "GET", url, g.headers(), nil, &batch); err != nil {
			return nil, fmt.Errorf("listing the mailbox: %w", err)
		}
		for _, c := range batch {
			out = append(out, c.comment())
		}
		if len(batch) < pageSize {
			return out, nil
		}
	}
}

// Post creates one comment and returns what GitHub recorded, including who it
// says authored it.
func (g *GitHubMailbox) Post(ctx context.Context, body string) (Comment, error) {
	var created ghComment
	if err := g.do(ctx, "POST", g.url, g.headers(), map[string]string{"body": body}, &created); err != nil {
		return Comment{}, fmt.Errorf("posting to the mailbox: %w", err)
	}
	return created.comment(), nil
}

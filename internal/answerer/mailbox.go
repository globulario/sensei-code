package answerer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Kind is what a request asks for. Only these two kinds are ever answered;
// every other comment on the mailbox, including any human authority question,
// is not a request to this process.
type Kind string

const (
	KindArchitecture Kind = "architecture"
	KindReview       Kind = "review"
)

// The envelopes this process reads. The request envelopes and their replies are
// the bridge's (internal/ghbridge, internal/reviewartifact); they are spelled
// here because this package does not import the bridge, and every rule below
// reads them at position zero exactly as those owners do.
const (
	architectureRequestMarker  = "[sensei-code:architecture-request]"
	architectureResponseMarker = "[sensei-code:architecture]"
	architectureRefusalMarker  = "[sensei-code:refused]"
	reviewRequestMarker        = "[sensei-code:review-request]"
	reviewResponseMarker       = "[sensei-code:review]"
	withdrawnMarker            = "[sensei-code:withdrawn]"
)

// Comment is one mailbox comment exactly as GitHub returned it.
type Comment struct {
	ID          int64
	Body        string
	AuthorLogin string
	AuthorID    int64
}

// Mailbox is the one conversation this process reads and posts to. Its
// destination is fixed when it is built; nothing passed to Post can move it.
type Mailbox interface {
	List(ctx context.Context) ([]Comment, error)
	Post(ctx context.Context, body string) (Comment, error)
}

// Request is one live request, bound to the exact comment that carried it.
type Request struct {
	Kind   Kind
	ID     string
	TaskID string
	// Comment is the request as posted. Its Body is what the model is shown,
	// byte for byte.
	Comment Comment
	// MailboxRepository and WorkspaceRepository are the request's own routing,
	// each "owner/name". A request is answered only where its mailbox route
	// names the configured mailbox exactly.
	MailboxRepository   string
	WorkspaceRepository string
	// Envelope is the reply envelope, built only from this request's own
	// header fields in the request's own line terminator. A review request's
	// envelope is the bytes the request embeds, verbatim. The answer is
	// appended to it unchanged.
	Envelope string
}

var errNotARequest = errors.New("not a request")

// parseRequest reads a comment as a request of either kind, or refuses it.
func parseRequest(c Comment) (Request, error) {
	switch {
	case strings.HasPrefix(c.Body, architectureRequestMarker):
		return parseArchitectureRequest(c)
	case strings.HasPrefix(c.Body, reviewRequestMarker):
		return parseReviewRequest(c)
	}
	return Request{}, errNotARequest
}

// lineEnding is the terminator the marker line uses, which every header line
// must repeat. The request is read as written, never normalized: the reply
// envelope is spelled in the same bytes, and a header that mixes terminators
// cannot be.
func lineEnding(rest string) (string, bool) {
	switch {
	case strings.HasPrefix(rest, "\r\n"):
		return "\r\n", true
	case strings.HasPrefix(rest, "\n"):
		return "\n", true
	}
	return "", false
}

// parseArchitectureRequest applies the architecture request grammar: the
// marker, a newline, a header of unique known key=value lines, a blank line,
// and a non-empty prompt. Duplicate, unknown or missing fields refuse the
// request; that includes the routing, which the bridge may omit for a request
// that predates it but without which this process does not answer.
func parseArchitectureRequest(c Comment) (Request, error) {
	rest := c.Body[len(architectureRequestMarker):]
	eol, ok := lineEnding(rest)
	if !ok {
		return Request{}, errors.New("architecture request marker is not followed by a newline")
	}
	header, prompt, ok := strings.Cut(rest[len(eol):], eol+eol)
	if !ok || strings.TrimSpace(prompt) == "" {
		return Request{}, errors.New("architecture request has no prompt")
	}
	allowed := map[string]bool{
		"kind": true, "task": true, "request": true, "objective_digest": true, "base": true,
		"graph_repository": true, "graph_build_commit": true,
		"mailbox_repository": true, "workspace_repository": true,
	}
	f := map[string]string{}
	for _, line := range strings.Split(header, eol) {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" || strings.ContainsAny(line, "\r\n") {
			return Request{}, fmt.Errorf("malformed architecture header line %q", line)
		}
		if _, known := allowed[key]; !known {
			return Request{}, fmt.Errorf("unknown architecture header %q", key)
		}
		if _, dup := f[key]; dup {
			return Request{}, fmt.Errorf("duplicate architecture header %q", key)
		}
		f[key] = value
	}
	for key, required := range allowed {
		if required && f[key] == "" {
			return Request{}, fmt.Errorf("architecture header %q is missing", key)
		}
	}
	switch {
	case f["kind"] != "architecture":
		return Request{}, fmt.Errorf("architecture request kind is %q", f["kind"])
	case !requestID(f["request"]):
		return Request{}, fmt.Errorf("architecture request id %q is malformed", f["request"])
	case strings.TrimSpace(f["task"]) != f["task"]:
		return Request{}, errors.New("architecture task is padded")
	case !lowerHex(f["objective_digest"], 64), !lowerHex(f["base"], 40), !lowerHex(f["graph_build_commit"], 40):
		return Request{}, errors.New("architecture binding is not in canonical form")
	case !repositoryPath(f["graph_repository"]):
		return Request{}, fmt.Errorf("graph_repository %q is not owner/name", f["graph_repository"])
	}
	if err := routing(f); err != nil {
		return Request{}, err
	}
	envelope := architectureResponseMarker + eol
	for _, key := range []string{"task", "request", "objective_digest", "base", "graph_repository", "graph_build_commit"} {
		envelope += key + "=" + f[key] + eol
	}
	envelope += eol
	return Request{Kind: KindArchitecture, ID: f["request"], TaskID: f["task"], Comment: c, Envelope: envelope,
		MailboxRepository: f["mailbox_repository"], WorkspaceRepository: f["workspace_repository"]}, nil
}

// reviewRequestFields are the identity lines a review request must carry.
var reviewRequestFields = []string{"kind", "task", "request", "base", "candidate_digest", "candidate_tree", "review_commit", "reviewer"}

// routingFields are the routing lines both request kinds must carry, once each.
var routingFields = []string{"mailbox_repository", "workspace_repository"}

// routing refuses a request whose routing is absent or not exactly owner/name.
func routing(f map[string]string) error {
	for _, key := range routingFields {
		if f[key] == "" {
			return fmt.Errorf("request header %q is missing; an unrouted request is not answered", key)
		}
		if !repositoryPath(f[key]) {
			return fmt.Errorf("request header %s=%q is not owner/name", key, f[key])
		}
	}
	return nil
}

// parseReviewRequest applies the review request grammar and derives the reply
// envelope from the request's header. The request also EMBEDS that envelope,
// verbatim, in the response contract it teaches; the derived envelope must be
// found there, byte for byte and at the start of a line, or the request's two
// statements of its own identity disagree and it is not answered. The bytes
// found are the bytes posted.
func parseReviewRequest(c Comment) (Request, error) {
	rest := c.Body[len(reviewRequestMarker):]
	eol, ok := lineEnding(rest)
	if !ok {
		return Request{}, errors.New("review request marker is not followed by a newline")
	}
	f := map[string]string{}
	for _, line := range strings.Split(rest[len(eol):], eol) {
		key, value, ok := strings.Cut(line, "=")
		if !ok || !headerKey(key) || value == "" || strings.ContainsAny(value, " \t\r\n") {
			break
		}
		if _, dup := f[key]; dup {
			return Request{}, fmt.Errorf("duplicate review request header %q", key)
		}
		f[key] = value
	}
	for _, key := range reviewRequestFields {
		if f[key] == "" {
			return Request{}, fmt.Errorf("review request header %q is missing", key)
		}
	}
	switch {
	case f["kind"] != "review":
		return Request{}, fmt.Errorf("review request kind is %q", f["kind"])
	case !requestID(f["request"]):
		return Request{}, fmt.Errorf("review request id %q is malformed", f["request"])
	case !lowerHex(f["base"], 40), !lowerHex(f["candidate_tree"], 40), !lowerHex(f["review_commit"], 40):
		return Request{}, errors.New("review binding is not in canonical form")
	}
	if err := routing(f); err != nil {
		return Request{}, err
	}
	envelope := reviewResponseMarker + eol
	for _, key := range reviewRequestFields[1:] {
		envelope += key + "=" + f[key] + eol
	}
	if !strings.Contains(c.Body, "\n"+envelope) {
		return Request{}, errors.New("the review request does not embed the reply envelope its own header states")
	}
	return Request{Kind: KindReview, ID: f["request"], TaskID: f["task"], Comment: c, Envelope: envelope,
		MailboxRepository: f["mailbox_repository"], WorkspaceRepository: f["workspace_repository"]}, nil
}

// answeredRequest names the request a comment answers or retracts, or "" if it
// does neither. Only the header is read: a request id quoted in a payload
// answers nothing. A relayed review is neither: Objective 47a's answers are
// posted directly, and a relay does not stand in for the responder's answer.
func answeredRequest(body string) (marker, id string) {
	for _, m := range []string{architectureResponseMarker, architectureRefusalMarker, reviewResponseMarker, withdrawnMarker} {
		if !strings.HasPrefix(body, m) {
			continue
		}
		rest := strings.ReplaceAll(body[len(m):], "\r\n", "\n")
		if !strings.HasPrefix(rest, "\n") {
			return m, ""
		}
		for _, line := range strings.Split(rest[1:], "\n") {
			key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
			if !ok || !headerKey(key) {
				break
			}
			if key == "request" {
				return m, value
			}
		}
		return m, ""
	}
	return "", ""
}

// identity is one configured account. The numeric id is what is compared; a
// login can be renamed and reused.
type identity struct{ login, id string }

func (p identity) is(c Comment) bool {
	return p.id != "" && strconv.FormatInt(c.AuthorID, 10) == p.id
}

// mailboxState is what one listing says about every request on the mailbox.
type mailboxState struct {
	requests []Request
	// answered holds request ids the configured responder has answered or
	// refused.
	answered map[string]bool
	// withdrawn holds request ids the requester has withdrawn.
	withdrawn map[string]bool
	// latest is the last request id posted for each task.
	latest map[string]string
	// refused names request-shaped comments from the requester that did not
	// parse, so they are reported rather than silently ignored.
	refused map[int64]error
}

// readState classifies one listing. Requests are authenticated first: a
// request-shaped comment from anybody but the requester is not a request. A
// request routed to any mailbox other than the configured one is refused: it
// is not addressed to this mailbox, so this process does not answer it here.
func readState(comments []Comment, responder, requester identity, mailbox string) mailboxState {
	s := mailboxState{
		answered: map[string]bool{}, withdrawn: map[string]bool{},
		latest: map[string]string{}, refused: map[int64]error{},
	}
	for _, c := range comments {
		if marker, id := answeredRequest(c.Body); marker != "" {
			switch {
			case id == "":
			case marker == withdrawnMarker:
				// A withdrawal is the requester's retraction of its own
				// request. Withdrawal-shaped text from any other account is
				// ordinary mailbox content and cannot change liveness.
				if requester.is(c) {
					s.withdrawn[id] = true
				}
			case responder.is(c):
				s.answered[id] = true
			}
			continue
		}
		if !requester.is(c) {
			continue
		}
		r, err := parseRequest(c)
		if errors.Is(err, errNotARequest) {
			continue
		}
		if err == nil && r.MailboxRepository != mailbox {
			err = fmt.Errorf("request %s is routed to mailbox %q, not the configured %q", r.ID, r.MailboxRepository, mailbox)
		}
		if err != nil {
			s.refused[c.ID] = err
			continue
		}
		s.requests = append(s.requests, r)
		s.latest[r.TaskID] = r.ID
	}
	return s
}

// live says whether r is still owed an answer, from mailbox state alone: the
// exact request still stands, the responder has not answered it, it is not
// withdrawn, and it is the current request for its task.
func (s mailboxState) live(r Request) (bool, string) {
	present := false
	for _, q := range s.requests {
		if q.Comment.ID == r.Comment.ID && q.Comment.Body == r.Comment.Body && q.ID == r.ID {
			present = true
		}
	}
	switch {
	case !present:
		return false, "the request no longer stands exactly as it was read"
	case s.answered[r.ID]:
		return false, "the request is already answered"
	case s.withdrawn[r.ID]:
		return false, "the request is withdrawn"
	case s.latest[r.TaskID] != r.ID:
		return false, "the request is superseded by " + s.latest[r.TaskID]
	}
	return true, ""
}

func requestID(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || strings.ContainsRune("_.:-", r)) {
			return false
		}
	}
	return true
}

func headerKey(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r == '_') {
			return false
		}
	}
	return true
}

func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

func repositoryPath(s string) bool {
	owner, name, ok := strings.Cut(s, "/")
	return ok && owner != "" && name != "" && requestID(owner) && requestID(name) && !strings.ContainsAny(s, ":")
}

// GitHubMailbox is the pull request conversation named by configuration,
// addressed through GitHub's issue comments resource.
type GitHubMailbox struct {
	API        string
	Repository string
	Number     string
	Token      string
	Client     *http.Client
}

// NewGitHubMailbox builds the mailbox from configuration.
func NewGitHubMailbox(c Config) *GitHubMailbox {
	return &GitHubMailbox{
		API: strings.TrimRight(c.GitHubAPI, "/"), Repository: c.MailboxRepository,
		Number: c.MailboxNumber, Token: c.GitHubToken,
		Client: &http.Client{Timeout: time.Minute},
	}
}

// commentsURL is the only address this mailbox reads or writes.
func (m *GitHubMailbox) commentsURL() string {
	return m.API + "/repos/" + m.Repository + "/issues/" + m.Number + "/comments"
}

type restComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	} `json:"user"`
}

func (r restComment) comment() Comment {
	return Comment{ID: r.ID, Body: r.Body, AuthorLogin: r.User.Login, AuthorID: r.User.ID}
}

// maxPages bounds a listing. Crossing it is an error, never a short result: a
// listing that silently drops the newest comments would miss an answer and
// post a second one.
const maxPages = 200

// List reads every comment, in order.
func (m *GitHubMailbox) List(ctx context.Context) ([]Comment, error) {
	var all []Comment
	for page := 1; page <= maxPages; page++ {
		raw, err := m.do(ctx, http.MethodGet, m.commentsURL()+"?per_page=100&page="+strconv.Itoa(page), "")
		if err != nil {
			return nil, err
		}
		var batch []restComment
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, fmt.Errorf("mailbox listing is not a comment list: %w", err)
		}
		for _, c := range batch {
			all = append(all, c.comment())
		}
		if len(batch) < 100 {
			return all, nil
		}
	}
	return nil, fmt.Errorf("mailbox has more than %d pages of comments; refusing a partial listing", maxPages)
}

// Post publishes body and returns the comment GitHub says it created.
func (m *GitHubMailbox) Post(ctx context.Context, body string) (Comment, error) {
	encoded, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return Comment{}, err
	}
	raw, err := m.do(ctx, http.MethodPost, m.commentsURL(), string(encoded))
	if err != nil {
		return Comment{}, err
	}
	var created restComment
	if err := json.Unmarshal(raw, &created); err != nil {
		// The comment IS posted; only its description was unreadable.
		return Comment{}, fmt.Errorf("the answer was posted but GitHub's description of it could not be read: %w", err)
	}
	return created.comment(), nil
}

func (m *GitHubMailbox) do(ctx context.Context, method, url, body string) ([]byte, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, reader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	client := m.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("mailbox %s: %w", method, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("mailbox %s: %w", method, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("mailbox %s returned %s: %s", method, resp.Status, bounded(string(raw)))
	}
	return raw, nil
}

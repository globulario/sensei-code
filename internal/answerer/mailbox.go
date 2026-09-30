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

// Comment is one mailbox comment: its exact bytes and the transport facts
// GitHub states about who posted it.
type Comment struct {
	ID          int64
	Body        string
	AuthorLogin string
	AuthorID    int64
}

// Mailbox is the one conversation this process reads and answers on. The
// destination is fixed when the Mailbox is built, from operator configuration;
// Post takes a body and nothing that could name another place.
type Mailbox interface {
	// List returns every comment, oldest first.
	List(ctx context.Context) ([]Comment, error)
	// Post publishes body as one comment and returns it as GitHub recorded it.
	Post(ctx context.Context, body string) (Comment, error)
	// Identity is the account the posting credential authenticates as.
	Identity(ctx context.Context) (login string, id int64, err error)
}

// Kind is what a request asks for. The set is closed and read by membership:
// a comment that is not one of these two request envelopes is never answered,
// whatever it says, and never reaches the model.
type Kind string

const (
	KindArchitecture Kind = "architecture"
	KindReview       Kind = "review"
)

// The envelopes this package reads. They are the bridge's own spellings, stated
// again because this package may not import the bridge; a drift here makes a
// request unanswerable, never answerable as something else.
const (
	architectureRequestMarker  = "[sensei-code:architecture-request]"
	architectureResponseMarker = "[sensei-code:architecture]"
	reviewRequestMarker        = "[sensei-code:review-request]"
	reviewArtifactMarker       = "[sensei-code:review]"
	withdrawnMarker            = "[sensei-code:withdrawn]"
	protocolMarkerPrefix       = "[sensei-code:"
	// reviewPayloadPlaceholder ends the response contract a review request
	// embeds, immediately after the reply envelope it teaches.
	reviewPayloadPlaceholder = "<your review verdict, as the JSON object described above>"
)

// Request is one live question, exactly as it stands on the mailbox.
type Request struct {
	Kind      Kind
	Comment   int64
	TaskID    string
	RequestID string
	// Body is the request comment byte for byte. It is what the model is shown
	// and the whole of what it is shown.
	Body string
	// envelope is the reply envelope, derived from this request's own header
	// fields and from nothing else.
	envelope string
	// mailboxRepository is the request's own routing binding: the mailbox it
	// says it was posted to. ParseRequest admits it only when it is the
	// configured one.
	mailboxRepository string
}

// Envelope is the reply envelope this request binds.
func (r Request) Envelope() string { return r.envelope }

// Reply is the one comment that answers this request: its envelope, then the
// response bytes unchanged.
func (r Request) Reply(response string) string { return r.envelope + response }

// same reports whether two observations are the same request comment, byte for
// byte.
func (r Request) same(o Request) bool {
	return r.Kind == o.Kind && r.Comment == o.Comment && r.RequestID == o.RequestID &&
		r.TaskID == o.TaskID && r.Body == o.Body && r.envelope == o.envelope &&
		r.mailboxRepository == o.mailboxRepository
}

// ParseRequest reads c as an architecture or review request addressed to
// mailboxRepository, or refuses it. Refusal is the answer for anything
// malformed, ambiguous or of another kind, and for a request whose routing is
// missing, repeated, malformed, or names any other mailbox: a request that does
// not say it was posted here for this mailbox is not one this process answers.
func ParseRequest(c Comment, mailboxRepository string) (Request, bool) {
	var (
		r   Request
		err error
	)
	switch {
	case strings.HasPrefix(c.Body, architectureRequestMarker):
		r, err = parseArchitectureRequest(c.Body)
	case strings.HasPrefix(c.Body, reviewRequestMarker):
		r, err = parseReviewRequest(c.Body)
	default:
		return Request{}, false
	}
	if err != nil || r.mailboxRepository != mailboxRepository {
		return Request{}, false
	}
	r.Comment = c.ID
	r.Body = c.Body
	return r, true
}

// parseArchitectureRequest mirrors the bridge's strict request grammar: the
// marker at position zero, a newline, key=value header lines up to the first
// blank line, no duplicate or unknown key, every binding field present and well
// formed, and a non-empty prompt.
func parseArchitectureRequest(body string) (Request, error) {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	rest := strings.TrimPrefix(normalized, architectureRequestMarker)
	if !strings.HasPrefix(rest, "\n") {
		return Request{}, errors.New("the architecture request marker is not followed by a newline")
	}
	header, prompt, ok := strings.Cut(rest[1:], "\n\n")
	if !ok || strings.TrimSpace(prompt) == "" {
		return Request{}, errors.New("the architecture request has no prompt")
	}
	allowed := map[string]bool{
		"kind": true, "task": true, "request": true, "objective_digest": true, "base": true,
		"graph_repository": true, "graph_build_commit": true,
		"mailbox_repository": true, "workspace_repository": true,
	}
	f := map[string]string{}
	for _, line := range strings.Split(header, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key == "" || value == "" {
			return Request{}, fmt.Errorf("malformed header line %q", line)
		}
		if _, known := allowed[key]; !known {
			return Request{}, fmt.Errorf("unknown header %q", key)
		}
		if _, dup := f[key]; dup {
			return Request{}, fmt.Errorf("duplicate header %q", key)
		}
		f[key] = value
	}
	for key, required := range allowed {
		if required && f[key] == "" {
			return Request{}, fmt.Errorf("header %q is missing", key)
		}
	}
	if f["kind"] != "architecture" ||
		strings.TrimSpace(f["task"]) == "" ||
		!requestIDShape(f["request"]) ||
		!lowerHex(f["objective_digest"], 64) ||
		!lowerHex(f["base"], 40) ||
		!repositoryShape(f["graph_repository"]) ||
		!lowerHex(f["graph_build_commit"], 40) ||
		!repositoryShape(f["mailbox_repository"]) ||
		!repositoryShape(f["workspace_repository"]) {
		return Request{}, errors.New("the architecture request binding is malformed")
	}
	var b strings.Builder
	b.WriteString(architectureResponseMarker + "\n")
	for _, key := range []string{"task", "request", "objective_digest", "base", "graph_repository", "graph_build_commit"} {
		b.WriteString(key + "=" + f[key] + "\n")
	}
	b.WriteString("\n")
	return Request{Kind: KindArchitecture, TaskID: f["task"], RequestID: f["request"], envelope: b.String(),
		mailboxRepository: f["mailbox_repository"]}, nil
}

// parseReviewRequest reads the review request header and then the reply
// envelope the request embeds in its response contract. The embedded envelope
// is the reply envelope, taken verbatim -- and only after every one of its
// identity lines is shown to equal the request's own header, so an envelope
// that disagrees with the request it sits in binds nothing.
func parseReviewRequest(body string) (Request, error) {
	if strings.Contains(body, "\r") {
		return Request{}, errors.New("the review request is not LF-delimited")
	}
	rest := strings.TrimPrefix(body, reviewRequestMarker)
	if !strings.HasPrefix(rest, "\n") {
		return Request{}, errors.New("the review request marker is not followed by a newline")
	}
	allowed := map[string]bool{
		"kind": true, "task": true, "request": true, "base": true, "candidate_digest": true,
		"candidate_tree": true, "review_commit": true, "reviewer": true,
		"mailbox_repository": true, "workspace_repository": true,
	}
	f := map[string]string{}
	for _, line := range strings.Split(rest[1:], "\n") {
		key, value, ok := headerField(line)
		if !ok {
			break
		}
		if _, known := allowed[key]; !known {
			return Request{}, fmt.Errorf("unknown header %q", key)
		}
		if _, dup := f[key]; dup {
			return Request{}, fmt.Errorf("duplicate header %q", key)
		}
		f[key] = value
	}
	for key, required := range allowed {
		if required && f[key] == "" {
			return Request{}, fmt.Errorf("header %q is missing", key)
		}
	}
	if f["kind"] != "review" ||
		!requestIDShape(f["request"]) ||
		!lowerHex(f["base"], 40) ||
		!digestShape(f["candidate_digest"]) ||
		!lowerHex(f["candidate_tree"], 40) ||
		!lowerHex(f["review_commit"], 40) ||
		!providerShape(f["reviewer"]) ||
		!repositoryShape(f["mailbox_repository"]) ||
		!repositoryShape(f["workspace_repository"]) {
		return Request{}, errors.New("the review request binding is malformed")
	}
	var want strings.Builder
	want.WriteString(reviewArtifactMarker + "\n")
	for _, key := range []string{"task", "request", "base", "candidate_digest", "candidate_tree", "review_commit", "reviewer"} {
		want.WriteString(key + "=" + f[key] + "\n")
	}
	tail := want.String() + reviewPayloadPlaceholder
	trimmed := strings.TrimSuffix(body, "\n")
	if !strings.HasSuffix(trimmed, "\n"+tail) {
		return Request{}, errors.New("the review request does not end with a response contract bound to its own header")
	}
	start := len(trimmed) - len(tail)
	envelope := trimmed[start : start+len(want.String())]
	return Request{Kind: KindReview, TaskID: f["task"], RequestID: f["request"], envelope: envelope,
		mailboxRepository: f["mailbox_repository"]}, nil
}

// headerField reads one key=value header line in the review grammar: a key of
// [a-z_], optional spaces around '=', and a value with no whitespace.
func headerField(line string) (string, string, bool) {
	key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
	if !ok {
		return "", "", false
	}
	key, value = strings.TrimSpace(key), strings.TrimSpace(value)
	if key == "" || value == "" || strings.ContainsAny(value, " \t") {
		return "", "", false
	}
	for _, r := range key {
		if (r < 'a' || r > 'z') && r != '_' {
			return "", "", false
		}
	}
	return key, value, true
}

// answeredRequest names the request a responder comment answers, if it is a
// protocol envelope that names one. Any envelope the responder posted with a
// request= header counts -- an answer, a refusal or a review -- because the
// question here is only whether this responder has already spoken for it.
func answeredRequest(body string) (string, bool) {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(normalized, protocolMarkerPrefix) {
		return "", false
	}
	lines := strings.Split(normalized, "\n")
	for _, line := range lines[1:] {
		key, value, ok := headerField(line)
		if !ok {
			break
		}
		if key == "request" {
			return value, true
		}
	}
	return "", false
}

// withdrawnRequest reads the bridge's withdrawal grammar: the marker, then
// exactly one request= line.
func withdrawnRequest(body string) (string, bool) {
	normalized := strings.ReplaceAll(body, "\r\n", "\n")
	if !strings.HasPrefix(normalized, withdrawnMarker) {
		return "", false
	}
	rest := strings.TrimRight(normalized[len(withdrawnMarker):], " \t\n")
	id, ok := strings.CutPrefix(rest, "\nrequest=")
	if !ok || !requestIDShape(id) {
		return "", false
	}
	return id, true
}

// Live returns the requests this responder may answer now, from mailbox state
// alone and never from the clock. A request is live iff it parses exactly, its
// id names no other request comment, the configured responder has not answered
// it, nobody has withdrawn it, and it is the latest request for its task. Only
// requests routed to mailboxRepository are read at all.
func Live(comments []Comment, responderID int64, mailboxRepository string) []Request {
	var requests []Request
	idCount := map[string]int{}
	latest := map[string]int64{}
	answered := map[string]bool{}
	withdrawn := map[string]bool{}
	for _, c := range comments {
		if id, ok := withdrawnRequest(c.Body); ok {
			withdrawn[id] = true
			continue
		}
		if c.AuthorID == responderID {
			if id, ok := answeredRequest(c.Body); ok {
				answered[id] = true
			}
			continue
		}
		r, ok := ParseRequest(c, mailboxRepository)
		if !ok {
			continue
		}
		requests = append(requests, r)
		idCount[r.RequestID]++
		if c.ID > latest[r.TaskID] {
			latest[r.TaskID] = c.ID
		}
	}
	var live []Request
	for _, r := range requests {
		if idCount[r.RequestID] != 1 || answered[r.RequestID] || withdrawn[r.RequestID] || latest[r.TaskID] != r.Comment {
			continue
		}
		live = append(live, r)
	}
	return live
}

// stillLive re-reads the mailbox's verdict on one exact request.
func stillLive(comments []Comment, responderID int64, mailboxRepository string, r Request) bool {
	for _, l := range Live(comments, responderID, mailboxRepository) {
		if l.same(r) {
			return true
		}
	}
	return false
}

func requestIDShape(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !isAlnum(r) && !strings.ContainsRune("_.:-", r) {
			return false
		}
	}
	return true
}

func digestShape(s string) bool {
	if len(s) < 8 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !isAlnum(r) && !strings.ContainsRune(":_.-", r) {
			return false
		}
	}
	return true
}

func providerShape(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i, r := range s {
		lowerAlnum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if !lowerAlnum && (i == 0 || !strings.ContainsRune("._-", r)) {
			return false
		}
	}
	return true
}

func repositoryShape(s string) bool {
	owner, name, ok := strings.Cut(s, "/")
	if !ok {
		return false
	}
	for _, part := range []string{owner, name} {
		if part == "" {
			return false
		}
		for _, r := range part {
			if !isAlnum(r) && !strings.ContainsRune("._-", r) {
				return false
			}
		}
	}
	return true
}

func lowerHex(s string, n int) bool {
	if len(s) != n {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func isAlnum(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
}

// GitHubMailbox is the Mailbox over GitHub's REST API, fixed at construction to
// one repository and one pull request.
type GitHubMailbox struct {
	api        string
	repository string
	number     string
	// token is a secret; it leaves only as an Authorization header.
	token string
	http  *http.Client
}

// NewGitHubMailbox builds the mailbox the operator configured.
func NewGitHubMailbox(c Config) *GitHubMailbox {
	api := strings.TrimRight(c.GitHubAPI, "/")
	if api == "" {
		api = "https://api.github.com"
	}
	return &GitHubMailbox{api: api, repository: c.Repository, number: c.Mailbox, token: c.GitHubToken,
		http: &http.Client{Timeout: time.Minute}}
}

type restComment struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	User struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	} `json:"user"`
}

func (c restComment) comment() Comment {
	return Comment{ID: c.ID, Body: c.Body, AuthorLogin: c.User.Login, AuthorID: c.User.ID}
}

const (
	commentsPerPage = 100
	maxCommentPages = 100
)

func (m *GitHubMailbox) do(ctx context.Context, method, path, body string) ([]byte, error) {
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, m.api+path, reader)
	if err != nil {
		return nil, fmt.Errorf("building %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+m.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, fmt.Errorf("reading %s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%s %s: HTTP %d", method, path, resp.StatusCode)
	}
	return raw, nil
}

func (m *GitHubMailbox) commentsPath() string {
	return "/repos/" + m.repository + "/issues/" + m.number + "/comments"
}

// List reads every page of the conversation, oldest first.
func (m *GitHubMailbox) List(ctx context.Context) ([]Comment, error) {
	var out []Comment
	for page := 1; page <= maxCommentPages; page++ {
		raw, err := m.do(ctx, http.MethodGet,
			m.commentsPath()+"?per_page="+strconv.Itoa(commentsPerPage)+"&page="+strconv.Itoa(page), "")
		if err != nil {
			return nil, err
		}
		var batch []restComment
		if err := json.Unmarshal(raw, &batch); err != nil {
			return nil, fmt.Errorf("decoding mailbox comments: %w", err)
		}
		for _, c := range batch {
			out = append(out, c.comment())
		}
		if len(batch) < commentsPerPage {
			return out, nil
		}
	}
	// A conversation longer than the bound is refused rather than read in part:
	// an unread page could hold the answer or withdrawal that makes a post wrong.
	return nil, fmt.Errorf("the mailbox has more than %d comments; refusing to classify it from a partial read",
		commentsPerPage*maxCommentPages)
}

// Post publishes one comment on the configured conversation.
func (m *GitHubMailbox) Post(ctx context.Context, body string) (Comment, error) {
	payload, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return Comment{}, err
	}
	raw, err := m.do(ctx, http.MethodPost, m.commentsPath(), string(payload))
	if err != nil {
		return Comment{}, err
	}
	var created restComment
	if err := json.Unmarshal(raw, &created); err != nil {
		return Comment{}, fmt.Errorf("decoding the posted comment: %w", err)
	}
	return created.comment(), nil
}

// Identity asks GitHub who the posting credential is.
func (m *GitHubMailbox) Identity(ctx context.Context) (string, int64, error) {
	raw, err := m.do(ctx, http.MethodGet, "/user", "")
	if err != nil {
		return "", 0, err
	}
	var user struct {
		Login string `json:"login"`
		ID    int64  `json:"id"`
	}
	if err := json.Unmarshal(raw, &user); err != nil {
		return "", 0, fmt.Errorf("decoding the credential's identity: %w", err)
	}
	return user.Login, user.ID, nil
}

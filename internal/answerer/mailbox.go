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

// Request is one request the bridge's own parser recognised, bound to the
// exact comment that carried it.
type Request struct {
	Kind Kind
	ID   string
	// TaskID and MailboxRepository are what the bridge read from the request.
	TaskID            string
	MailboxRepository string
	// Comment is the request as posted. Its Body is what the model is shown,
	// byte for byte, and what every protocol callback is given.
	Comment Comment
}

// identity is one configured account. The numeric id is what is compared; a
// login can be renamed and reused.
type identity struct{ login, id string }

func (p identity) is(c Comment) bool {
	return p.id != "" && strconv.FormatInt(c.AuthorID, 10) == p.id
}

// mailboxState is what one listing says about every request on the mailbox.
// Nothing in it is read by this package's own grammar: every classification is
// the injected protocol's.
type mailboxState struct {
	requests []Request
	// replies holds every comment from the configured responder. Whether one
	// answers a request is the protocol's answer for that exact request; a
	// comment merely shaped like a reply ends nothing.
	replies []Comment
	// withdrawn holds request ids the requester has withdrawn, as the
	// bridge's withdrawal parser reads them.
	withdrawn map[string]bool
	// latest is the last request id posted for each task.
	latest map[string]string
	// misrouted names requests addressed to another mailbox.
	misrouted map[int64]string
	protocol  Protocol
}

// readState classifies one listing. Requests and withdrawals are
// authenticated first: only the requester's comments are read as either, and
// only the responder's comments can answer. A comment the bridge's parsers do
// not recognise as a request is not one, whatever it looks like.
func readState(comments []Comment, responder, requester identity, mailbox string, p Protocol) mailboxState {
	s := mailboxState{withdrawn: map[string]bool{}, latest: map[string]string{}, misrouted: map[int64]string{}, protocol: p}
	for _, c := range comments {
		if responder.is(c) {
			s.replies = append(s.replies, c)
		}
		if !requester.is(c) {
			continue
		}
		if id, ok := p.Withdrawal(c.Body); ok {
			s.withdrawn[id] = true
			continue
		}
		r, ok := p.recognise(c)
		if !ok {
			continue
		}
		if r.MailboxRepository != mailbox {
			// Not addressed to this mailbox, so it is not a request to this
			// process and supersedes nothing here.
			s.misrouted[c.ID] = fmt.Sprintf("request %s is routed to mailbox %q, not the configured %q", r.ID, r.MailboxRepository, mailbox)
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
		if q.Comment.ID == r.Comment.ID && q.Comment.Body == r.Comment.Body && q.ID == r.ID && q.Kind == r.Kind {
			present = true
		}
	}
	switch {
	case !present:
		return false, "the request no longer stands exactly as it was read"
	case s.answered(r):
		return false, "the request is already answered by the configured responder"
	case s.withdrawn[r.ID]:
		return false, "the request is withdrawn"
	case s.latest[r.TaskID] != r.ID:
		return false, "the request is superseded by " + s.latest[r.TaskID]
	}
	return true, ""
}

// answered says whether the configured responder has already answered exactly
// r, by the bridge's own binding predicate for r's kind.
func (s mailboxState) answered(r Request) bool {
	for _, c := range s.replies {
		if s.protocol.answers(r, c.Body) {
			return true
		}
	}
	return false
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
		return Comment{}, errors.New("the answer was posted but GitHub's description of it could not be read: " + err.Error())
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

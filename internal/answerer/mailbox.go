package answerer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Comment contains mailbox evidence, not governance state or a receipt.
type Comment struct {
	ID       int64
	AuthorID int64
	Body     string
}
type DecodeMailbox func([]Comment) (Snapshot, error)

// GitHubMailbox addresses only its operator-configured PR. The command supplies
// the existing bridge protocol adapter; this package knows no wire contracts.
type GitHubMailbox struct {
	Config Config
	Decode DecodeMailbox
	HTTP   *http.Client
}

func (m *GitHubMailbox) do(ctx context.Context, method, path string, body io.Reader) ([]byte, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, "https://api.github.com"+path, body)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+m.Config.GitHubToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	client := m.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, errors.New("GitHub HTTP transport failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("GitHub HTTP status %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, (8<<20)+1))
	if err != nil {
		return nil, nil, errors.New("GitHub response read failed")
	}
	if len(raw) > 8<<20 {
		return nil, nil, errors.New("GitHub response too large")
	}
	return raw, resp.Header, nil
}

func (m *GitHubMailbox) issuePath() string {
	return "/repos/" + m.Config.Repository + "/issues/" + m.Config.Number
}

// Verify establishes credential identity and PR routing before model calls.
func (m *GitHubMailbox) Verify(ctx context.Context) error {
	if err := m.Config.Validate(); err != nil {
		return err
	}
	raw, _, err := m.do(ctx, http.MethodGet, "/user", nil)
	if err != nil {
		return err
	}
	var user struct {
		ID int64 `json:"id"`
	}
	if json.Unmarshal(raw, &user) != nil || user.ID != m.Config.ResponderID {
		return errors.New("GitHub credential does not match configured responder")
	}
	raw, _, err = m.do(ctx, http.MethodGet, m.issuePath(), nil)
	if err != nil {
		return err
	}
	var issue struct {
		PullRequest *json.RawMessage `json:"pull_request"`
	}
	if json.Unmarshal(raw, &issue) != nil || issue.PullRequest == nil || string(*issue.PullRequest) == "null" {
		return errors.New("configured mailbox is not a pull request")
	}
	return nil
}

func (m *GitHubMailbox) List(ctx context.Context) (Snapshot, error) {
	if m.Decode == nil {
		return Snapshot{}, errors.New("mailbox protocol adapter is required")
	}
	var all []Comment
	seen := map[int64]bool{}
	for page := 1; page <= 200; page++ {
		raw, hdr, err := m.do(ctx, http.MethodGet, m.issuePath()+"/comments?per_page=100&page="+strconv.Itoa(page), nil)
		if err != nil {
			return Snapshot{}, err
		}
		var batch []struct {
			ID   int64  `json:"id"`
			Body string `json:"body"`
			User struct {
				ID int64 `json:"id"`
			} `json:"user"`
		}
		if err = json.Unmarshal(raw, &batch); err != nil {
			return Snapshot{}, errors.New("malformed GitHub comments")
		}
		for _, c := range batch {
			if c.ID <= 0 || c.User.ID <= 0 || seen[c.ID] {
				return Snapshot{}, errors.New("incomplete or inconsistent mailbox snapshot")
			}
			seen[c.ID] = true
			all = append(all, Comment{c.ID, c.User.ID, c.Body})
		}
		more := strings.Contains(hdr.Get("Link"), `rel="next"`)
		if !more {
			sort.Slice(all, func(i, j int) bool { return all[i].ID < all[j].ID })
			return m.Decode(all)
		}
		if len(batch) == 0 {
			return Snapshot{}, errors.New("mailbox pagination claims more after empty page")
		}
	}
	return Snapshot{}, errors.New("mailbox exceeds pagination bound; result incomplete")
}

// Post never retries an ambiguous write. Poll reserved this request durably.
func (m *GitHubMailbox) Post(ctx context.Context, body string) error {
	raw, err := json.Marshal(map[string]string{"body": body})
	if err != nil {
		return err
	}
	_, _, err = m.do(ctx, http.MethodPost, m.issuePath()+"/comments", strings.NewReader(string(raw)))
	return err
}

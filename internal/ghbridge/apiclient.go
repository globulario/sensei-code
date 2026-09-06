package ghbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// AppClient performs the mailbox's REST operations as the GitHub App
// installation rather than through a person's gh credentials.
//
// The repository is CONFIGURATION, stated here, never inferred from the working
// directory, a git remote, the current gh repository or an issue URL. Each of
// those is a fact about the machine rather than about which mailbox this bridge
// serves, and a bridge that read one of them would post review requests
// wherever it happened to be run.
//
// The installation token never leaves this type: it is fetched, placed in an
// Authorization header, and dropped. It appears in no error, no event and no
// return value.
type AppClient struct {
	Auth  *InstallationAuth
	Owner string
	Repo  string
	// HTTP is the client used. Empty means a bounded default.
	HTTP *http.Client
}

// Configured reports whether this client can address a repository as an App.
func (c *AppClient) Configured() bool {
	return c != nil && c.Auth.Configured() &&
		strings.TrimSpace(c.Owner) != "" && strings.TrimSpace(c.Repo) != ""
}

func (c *AppClient) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// do performs one authenticated REST call.
func (c *AppClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	if !c.Configured() {
		return nil, errors.New("the github app client is not configured (app auth, owner and repo are all required)")
	}
	token, _, err := c.Auth.Token(ctx)
	if err != nil {
		return nil, err
	}

	var payload io.Reader
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return nil, merr
		}
		payload = bytes.NewReader(b)
	}

	url := c.Auth.apiBase() + path
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		// The token travelled in a header and is not part of this error.
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, oneLineLimited(string(out), 200))
	}
	return out, nil
}

// PostComment adds one comment to an issue as the App installation.
func (c *AppClient) PostComment(ctx context.Context, issueNumber, body string) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%s/comments", c.Owner, c.Repo, strings.TrimSpace(issueNumber))
	_, err := c.do(ctx, http.MethodPost, path, map[string]string{"body": body})
	return err
}

// ListComments reads an issue's comments as the App installation.
//
// Pages are followed explicitly rather than through a client-side paginate
// flag, so the caller gets ONE decoded slice in comment order. This is the
// surface the deferred `gh api --paginate` defect lives on: that path emits one
// JSON value per page and decodes as a single array only while the mailbox fits
// in one page.
func (c *AppClient) ListComments(ctx context.Context, issueNumber string) ([]restComment, error) {
	var all []restComment
	for page := 1; page <= 50; page++ {
		path := fmt.Sprintf("/repos/%s/%s/issues/%s/comments?per_page=100&page=%d",
			c.Owner, c.Repo, strings.TrimSpace(issueNumber), page)
		raw, err := c.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var batch []restComment
		if jerr := json.Unmarshal(raw, &batch); jerr != nil {
			return nil, fmt.Errorf("issue comments could not be read: %w", jerr)
		}
		all = append(all, batch...)
		if len(batch) < 100 {
			break
		}
	}
	return all, nil
}

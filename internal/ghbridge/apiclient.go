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
func (c *AppClient) do(ctx context.Context, method, path string, body any) ([]byte, http.Header, error) {
	if !c.Configured() {
		return nil, nil, errors.New("the github app client is not configured (app auth, owner and repo are all required)")
	}
	token, _, err := c.Auth.token(ctx)
	if err != nil {
		return nil, nil, err
	}

	var payload io.Reader
	if body != nil {
		b, merr := json.Marshal(body)
		if merr != nil {
			return nil, nil, merr
		}
		payload = bytes.NewReader(b)
	}

	url := c.Auth.apiBase() + path
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return nil, nil, err
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
		return nil, nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	out, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("%s %s: HTTP %d: %s", method, path, resp.StatusCode, oneLineLimited(string(out), 200))
	}
	return out, resp.Header, nil
}

// PostComment adds one comment to a conversation as the App installation and
// returns the identity GitHub gave it.
//
// The id is returned rather than discarded because a published request needs a
// name. A doorbell points at THIS comment by number, so the wake signal carries
// a locator instead of a copy of the binding — and if the doorbell fails, the
// retry targets the same durable comment rather than minting a second request.
// Discarding the id would force a second identity to be invented for something
// GitHub already named.
func (c *AppClient) PostComment(ctx context.Context, issueNumber, body string) (int64, error) {
	path := fmt.Sprintf("/repos/%s/%s/issues/%s/comments", c.Owner, c.Repo, strings.TrimSpace(issueNumber))
	raw, _, err := c.do(ctx, http.MethodPost, path, map[string]string{"body": body})
	if err != nil {
		return 0, err
	}
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		// The comment IS published; only its name was unreadable. Say exactly
		// that, because a caller must not read this as "nothing was posted" and
		// publish a second request.
		return 0, fmt.Errorf("the comment was posted but its id could not be read: %w", err)
	}
	return created.ID, nil
}

// maxCommentPages bounds a mailbox read so a pathological pagination loop
// cannot run forever. Crossing it is an ERROR, never a truncated result.
const maxCommentPages = 200

// ErrMailboxTooLarge reports that the mailbox has more pages than this client
// will read. It exists so the failure is stated rather than silently returned as
// a short slice.
var ErrMailboxTooLarge = errors.New("mailbox exceeds supported pagination bound; result incomplete")

// ListComments reads an issue's comments as the App installation, in order.
//
// Termination is GitHub's own Link rel="next", not a guess from the batch size.
// An earlier version stopped after a fixed 50 pages and returned what it had:
// with a full page 50 and a page 51 existing, that result LOOKED complete while
// omitting every later comment — and a mailbox read that silently drops answers
// is worse than one that fails, because the missing answer is indistinguishable
// from an unanswered request.
//
// A short page also ends the read, which is the ordinary case and agrees with
// Link; the bound below only guards against a server that never stops offering
// a next page.
func (c *AppClient) ListComments(ctx context.Context, issueNumber string) ([]restComment, error) {
	var all []restComment
	path := fmt.Sprintf("/repos/%s/%s/issues/%s/comments?per_page=100&page=1",
		c.Owner, c.Repo, strings.TrimSpace(issueNumber))

	for pages := 0; ; pages++ {
		if pages >= maxCommentPages {
			return nil, fmt.Errorf("%w: stopped after %d pages with more still offered",
				ErrMailboxTooLarge, maxCommentPages)
		}
		raw, hdr, err := c.do(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		var batch []restComment
		if jerr := json.Unmarshal(raw, &batch); jerr != nil {
			return nil, fmt.Errorf("issue comments could not be read: %w", jerr)
		}
		all = append(all, batch...)

		next := nextPagePath(hdr.Get("Link"), c.Auth.apiBase())
		if next == "" || len(batch) == 0 {
			return all, nil
		}
		path = next
	}
}

// nextPagePath extracts the rel="next" target from a Link header and reduces it
// to a path this client can request.
//
// Returns "" when there is no next page, which is how a read terminates.
func nextPagePath(link, apiBase string) string {
	for _, part := range strings.Split(link, ",") {
		seg := strings.Split(strings.TrimSpace(part), ";")
		if len(seg) < 2 {
			continue
		}
		isNext := false
		for _, attr := range seg[1:] {
			if strings.Contains(strings.ReplaceAll(attr, " ", ""), `rel="next"`) {
				isNext = true
			}
		}
		if !isNext {
			continue
		}
		raw := strings.TrimSpace(seg[0])
		raw = strings.TrimPrefix(strings.TrimSuffix(raw, ">"), "<")
		if raw == "" {
			continue
		}
		return strings.TrimPrefix(raw, strings.TrimSuffix(apiBase, "/"))
	}
	return ""
}

// AppConfig is the operator's GitHub App configuration, exactly as supplied.
//
// Only the key PATH is configuration. The key CONTENT never is: it is not held
// here, not serialized, and not logged.
type AppConfig struct {
	AppID          int64
	InstallationID int64
	PrivateKeyPath string
	Owner          string
	Repo           string
}

// Selected reports whether the operator asked for App transport at all.
//
// ANY App-specific field means yes. Keying selection on the app id alone meant
// that installation id + key + owner + repo, with the app id merely forgotten,
// fell through to the operator's personal gh credentials — machine activity
// posted under a person's identity, by an operator who had plainly configured
// the opposite. Intent is expressed by configuring anything; completeness is
// then required rather than assumed.
func (c AppConfig) Selected() bool {
	return c.AppID != 0 ||
		c.InstallationID != 0 ||
		strings.TrimSpace(c.PrivateKeyPath) != "" ||
		strings.TrimSpace(c.Owner) != "" ||
		strings.TrimSpace(c.Repo) != ""
}

// Mailbox is the "owner/name" this App transport posts the request to, or ""
// when the App was not fully configured.
//
// It exists so the doorbell can be aimed at the same repository the request
// lands in without re-deriving it from a working directory. Two locators for
// one conversation is the defect; this is the single source.
func (c AppConfig) Mailbox() string {
	owner, repo := strings.TrimSpace(c.Owner), strings.TrimSpace(c.Repo)
	if owner == "" || repo == "" {
		return ""
	}
	return owner + "/" + repo
}

// Missing names the fields still required for the selected App transport.
func (c AppConfig) Missing() []string {
	var missing []string
	if c.AppID == 0 {
		missing = append(missing, "app id")
	}
	if c.InstallationID == 0 {
		missing = append(missing, "installation id")
	}
	if strings.TrimSpace(c.PrivateKeyPath) == "" {
		missing = append(missing, "private key path")
	}
	if strings.TrimSpace(c.Owner) == "" {
		missing = append(missing, "repository owner")
	}
	if strings.TrimSpace(c.Repo) == "" {
		missing = append(missing, "repository name")
	}
	return missing
}

// Client builds the App transport, or refuses.
//
// Three outcomes and no fourth:
//
//	nothing configured        -> (nil, nil)   legacy gh path, unchanged
//	partly configured         -> (nil, error) refusal; never the gh path
//	completely configured     -> (client, nil)
func (c AppConfig) Client() (*AppClient, error) {
	if !c.Selected() {
		return nil, nil
	}
	if missing := c.Missing(); len(missing) > 0 {
		return nil, fmt.Errorf(
			"github app transport was selected but is missing: %s; refusing rather than falling back to the operator's gh credentials",
			strings.Join(missing, ", "))
	}
	return &AppClient{
		Auth: &InstallationAuth{
			AppID:          c.AppID,
			InstallationID: c.InstallationID,
			PrivateKeyPath: strings.TrimSpace(c.PrivateKeyPath),
		},
		Owner: strings.TrimSpace(c.Owner),
		Repo:  strings.TrimSpace(c.Repo),
	}, nil
}

// PullRequestURL reports the pull-request URL of a conversation, or "" when the
// number names an ordinary issue.
//
// It reads the ISSUES resource on purpose. That is the resource this mailbox
// already posts to and reads from, so establishing PR-ness costs no second
// transport, and GitHub returns a `pull_request` object on an issue that is a
// pull request. PR-ness is therefore a VALUE here rather than an HTTP status
// reconstructed out of an error string, which keeps "this is an ordinary issue"
// distinguishable from "GitHub could not be asked". Collapsing those two is how
// a mailbox aimed at the wrong kind of target would come to look merely
// unreachable, and being unanswered is already what the wrong target looks like.
//
// The installation token travels in a header and is not part of any error
// returned here.
func (c *AppClient) PullRequestURL(ctx context.Context, number string) (string, error) {
	if !c.Configured() {
		return "", errors.New("the github app transport was selected but is not configured; refusing rather " +
			"than establishing the mailbox as the operator's gh account")
	}
	path := fmt.Sprintf("/repos/%s/%s/issues/%s", c.Owner, c.Repo, strings.TrimSpace(number))
	raw, _, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", err
	}
	var conversation struct {
		PullRequest *struct {
			URL string `json:"url"`
		} `json:"pull_request"`
	}
	if err := json.Unmarshal(raw, &conversation); err != nil {
		return "", fmt.Errorf("reading %s/%s #%s: %w", c.Owner, c.Repo, strings.TrimSpace(number), err)
	}
	if conversation.PullRequest == nil {
		return "", nil
	}
	return conversation.PullRequest.URL, nil
}

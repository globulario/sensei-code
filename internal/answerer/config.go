// Package answerer supplies answers to governed architecture and advisory
// review requests standing on the mailbox, unattended.
//
// It is TRANSPORT, not a bridge and not an authority. The sensei-code bridge
// still posts every request and consumes every answer; this package reads a
// live request, hands its exact bytes to one fresh model call, checks the
// result against the contract validator it was GIVEN, and posts those same
// bytes under the envelope the request itself determines. It makes no
// governance judgment: it never answers a human authority question, never
// rewrites what the model said, and a review it carries is advisory and
// satisfies no independent-review requirement.
package answerer

import (
	"errors"
	"strings"
)

// Config is fixed operator configuration. Nothing in a request can change any
// of it: request bytes are evidence to be answered, never instructions about
// who answers, where, or with which secret.
type Config struct {
	// GitHubAPI is the REST base URL, normally https://api.github.com.
	GitHubAPI string
	// MailboxRepository ("owner/name") and MailboxNumber name the one pull
	// request conversation this process reads and posts to.
	MailboxRepository string
	MailboxNumber     string
	// GitHubToken is the posting credential. Its account MUST be the responder
	// below: a post GitHub attributes to anybody else is reported as a failure.
	GitHubToken string
	// ResponderLogin and ResponderID are the account this process answers as.
	// "Already answered" is read against this identity and no other.
	ResponderLogin string
	ResponderID    string
	// RequesterLogin and RequesterID are the account the bridge publishes
	// requests as. A request-shaped comment from anybody else is not a request.
	RequesterLogin string
	RequesterID    string
	// ModelEndpoint is the full chat-completions URL; ModelName and ModelAPIKey
	// select and authorize the model.
	ModelEndpoint string
	ModelName     string
	ModelAPIKey   string
	// LockPath is the single-writer lock file.
	LockPath string
}

// The environment names the command reads. Operator-owned: set by whoever
// runs the process, never by anything posted on the mailbox.
const (
	EnvGitHubAPI         = "SENSEI_ANSWERER_GITHUB_API"
	EnvMailboxRepository = "SENSEI_ANSWERER_MAILBOX_REPOSITORY"
	EnvMailboxNumber     = "SENSEI_ANSWERER_MAILBOX_NUMBER"
	EnvGitHubToken       = "SENSEI_ANSWERER_GITHUB_TOKEN"
	EnvResponderLogin    = "SENSEI_ANSWERER_RESPONDER_LOGIN"
	EnvResponderID       = "SENSEI_ANSWERER_RESPONDER_ID"
	EnvRequesterLogin    = "SENSEI_ANSWERER_REQUESTER_LOGIN"
	EnvRequesterID       = "SENSEI_ANSWERER_REQUESTER_ID"
	EnvModelEndpoint     = "SENSEI_ANSWERER_MODEL_ENDPOINT"
	EnvModelName         = "SENSEI_ANSWERER_MODEL"
	EnvModelAPIKey       = "SENSEI_ANSWERER_MODEL_API_KEY"
	EnvLockPath          = "SENSEI_ANSWERER_LOCK"
)

// ConfigFromEnv reads the configuration through getenv. Only the GitHub API
// base has a default; everything else must be stated.
func ConfigFromEnv(getenv func(string) string) Config {
	c := Config{
		GitHubAPI:         strings.TrimSpace(getenv(EnvGitHubAPI)),
		MailboxRepository: strings.TrimSpace(getenv(EnvMailboxRepository)),
		MailboxNumber:     strings.TrimSpace(getenv(EnvMailboxNumber)),
		GitHubToken:       strings.TrimSpace(getenv(EnvGitHubToken)),
		ResponderLogin:    strings.TrimSpace(getenv(EnvResponderLogin)),
		ResponderID:       strings.TrimSpace(getenv(EnvResponderID)),
		RequesterLogin:    strings.TrimSpace(getenv(EnvRequesterLogin)),
		RequesterID:       strings.TrimSpace(getenv(EnvRequesterID)),
		ModelEndpoint:     strings.TrimSpace(getenv(EnvModelEndpoint)),
		ModelName:         strings.TrimSpace(getenv(EnvModelName)),
		ModelAPIKey:       strings.TrimSpace(getenv(EnvModelAPIKey)),
		LockPath:          strings.TrimSpace(getenv(EnvLockPath)),
	}
	if c.GitHubAPI == "" {
		c.GitHubAPI = "https://api.github.com"
	}
	return c
}

// Validate refuses an incomplete configuration. Every identity is required in
// full, login AND numeric id: a login can be renamed and reused, an id cannot.
func (c Config) Validate() error {
	var missing []string
	for _, f := range []struct{ name, value string }{
		{EnvGitHubAPI, c.GitHubAPI},
		{EnvMailboxRepository, c.MailboxRepository},
		{EnvMailboxNumber, c.MailboxNumber},
		{EnvGitHubToken, c.GitHubToken},
		{EnvResponderLogin, c.ResponderLogin},
		{EnvResponderID, c.ResponderID},
		{EnvRequesterLogin, c.RequesterLogin},
		{EnvRequesterID, c.RequesterID},
		{EnvModelEndpoint, c.ModelEndpoint},
		{EnvModelName, c.ModelName},
		{EnvModelAPIKey, c.ModelAPIKey},
		{EnvLockPath, c.LockPath},
	} {
		if strings.TrimSpace(f.value) == "" {
			missing = append(missing, f.name)
		}
	}
	if len(missing) != 0 {
		return errors.New("answerer configuration is incomplete: " + strings.Join(missing, ", "))
	}
	owner, name, ok := strings.Cut(c.MailboxRepository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		return errors.New("mailbox repository must be owner/name, got " + c.MailboxRepository)
	}
	if !digits(c.MailboxNumber) || !digits(c.ResponderID) || !digits(c.RequesterID) {
		return errors.New("mailbox number and account ids must be decimal integers")
	}
	return nil
}

func digits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

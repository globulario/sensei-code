// Package answerer supplies answers to a governed Sensei Code mailbox.
//
// It is NOT a second GitHub bridge. The sensei-code bridge stays authoritative
// for posting requests and consuming answers; this package only answers the two
// request kinds the bridge publishes for a remote party -- architecture requests
// and review requests -- and nothing else.
//
// What it does for one live request:
//
//	read the exact request bytes from the configured mailbox
//	make ONE fresh, stateless model call whose only input is those bytes
//	validate the output for format and protocol, never for content
//	build the reply envelope from the request's own header, never from the model
//	re-list the mailbox, and post only if the request is still live and unanswered
//
// What it never does: answer a human-owned authority question, run
// `sensei-code resume --answer`, post an approval, attestation, merge or waiver,
// or turn an advisory review into an independent one. A review it transports is
// ADVISORY: the responder identity below is operator configuration, and nothing
// about carrying the bytes establishes reviewer independence.
package answerer

import (
	"errors"
	"strings"
)

// Principal is a GitHub identity fixed by operator configuration.
//
// UserID is preferred because a numeric id is immutable while a login can be
// renamed and reused; Login is the match only when no id was configured. The
// id is kept as its decimal spelling so it is compared exactly as configured.
type Principal struct {
	UserID string
	Login  string
}

// Configured reports whether this principal can authenticate anybody. An
// unconfigured principal authenticates NOBODY rather than everybody.
func (p Principal) Configured() bool {
	return p.UserID != "" || strings.TrimSpace(p.Login) != ""
}

// Matches reports whether a comment author is this principal.
func (p Principal) Matches(authorID, authorLogin string) bool {
	if !p.Configured() {
		return false
	}
	if p.UserID != "" {
		return authorID == p.UserID
	}
	return strings.EqualFold(strings.TrimSpace(p.Login), strings.TrimSpace(authorLogin))
}

// Config is the whole of the answerer's authority, and all of it comes from the
// operator. No field is ever read from, defaulted from, or overridden by a
// request: request bytes are untrusted evidence and cannot move the
// destination, the identity, the secrets or the model.
//
// Secrets are named by PATH only. Their content is read once at startup and
// never reaches an error, a log line or an argv.
type Config struct {
	// Mailbox is the one conversation this answerer reads and posts to:
	// "owner/name" and the pull request number whose conversation it is.
	MailboxRepository string
	MailboxNumber     string
	// GitHubAPI is the REST base, https://api.github.com unless configured.
	GitHubAPI string

	// Responder is the identity this answerer posts as. Its existing answers are
	// what "already answered" means. Publisher is the only identity whose
	// requests are read as requests.
	Responder Principal
	Publisher Principal

	// GitHubTokenPath and ModelKeyPath are credential PATHS.
	GitHubTokenPath string
	ModelKeyPath    string

	// ModelEndpoint is a chat-completions URL; Model is the model it serves.
	ModelEndpoint string
	Model         string

	// LockPath is the single-writer lock file.
	LockPath string
}

// Environment variable names, one per Config field.
const (
	EnvMailboxRepository = "SENSEI_ANSWERER_MAILBOX_REPOSITORY"
	EnvMailboxNumber     = "SENSEI_ANSWERER_MAILBOX_PR"
	EnvGitHubAPI         = "SENSEI_ANSWERER_GITHUB_API"
	EnvResponderID       = "SENSEI_ANSWERER_RESPONDER_ID"
	EnvResponderLogin    = "SENSEI_ANSWERER_RESPONDER_LOGIN"
	EnvPublisherID       = "SENSEI_ANSWERER_PUBLISHER_ID"
	EnvPublisherLogin    = "SENSEI_ANSWERER_PUBLISHER_LOGIN"
	EnvGitHubTokenPath   = "SENSEI_ANSWERER_GITHUB_TOKEN_FILE"
	EnvModelKeyPath      = "SENSEI_ANSWERER_MODEL_KEY_FILE"
	EnvModelEndpoint     = "SENSEI_ANSWERER_MODEL_ENDPOINT"
	EnvModel             = "SENSEI_ANSWERER_MODEL"
	EnvLockPath          = "SENSEI_ANSWERER_LOCK_FILE"
)

const (
	defaultGitHubAPI     = "https://api.github.com"
	defaultModelEndpoint = "https://api.openai.com/v1/chat/completions"
)

// LoadConfig reads the operator's configuration through lookup (os.LookupEnv
// in production). The model is never defaulted: which model answers is an
// owner decision, not something this package may pick.
func LoadConfig(lookup func(string) (string, bool)) (Config, error) {
	get := func(key string) string {
		v, _ := lookup(key)
		return strings.TrimSpace(v)
	}
	c := Config{
		MailboxRepository: get(EnvMailboxRepository),
		MailboxNumber:     get(EnvMailboxNumber),
		GitHubAPI:         get(EnvGitHubAPI),
		Responder:         Principal{UserID: get(EnvResponderID), Login: get(EnvResponderLogin)},
		Publisher:         Principal{UserID: get(EnvPublisherID), Login: get(EnvPublisherLogin)},
		GitHubTokenPath:   get(EnvGitHubTokenPath),
		ModelKeyPath:      get(EnvModelKeyPath),
		ModelEndpoint:     get(EnvModelEndpoint),
		Model:             get(EnvModel),
		LockPath:          get(EnvLockPath),
	}
	if c.GitHubAPI == "" {
		c.GitHubAPI = defaultGitHubAPI
	}
	if c.ModelEndpoint == "" {
		c.ModelEndpoint = defaultModelEndpoint
	}
	return c, c.Validate()
}

// Validate refuses an incomplete or malformed configuration, naming every
// missing field at once.
func (c Config) Validate() error {
	var missing []string
	owner, name, ok := strings.Cut(c.MailboxRepository, "/")
	if !ok || owner == "" || name == "" || strings.Contains(name, "/") {
		missing = append(missing, EnvMailboxRepository+" (owner/name)")
	}
	if !digits(c.MailboxNumber) {
		missing = append(missing, EnvMailboxNumber+" (a pull request number)")
	}
	if !c.Responder.Configured() {
		missing = append(missing, EnvResponderID+" or "+EnvResponderLogin)
	}
	if !c.Publisher.Configured() {
		missing = append(missing, EnvPublisherID+" or "+EnvPublisherLogin)
	}
	if c.Responder.UserID != "" && !digits(c.Responder.UserID) {
		missing = append(missing, EnvResponderID+" (a numeric user id)")
	}
	if c.Publisher.UserID != "" && !digits(c.Publisher.UserID) {
		missing = append(missing, EnvPublisherID+" (a numeric user id)")
	}
	for _, f := range []struct{ name, value string }{
		{EnvGitHubTokenPath, c.GitHubTokenPath},
		{EnvModelKeyPath, c.ModelKeyPath},
		{EnvModel, c.Model},
		{EnvLockPath, c.LockPath},
	} {
		if f.value == "" {
			missing = append(missing, f.name)
		}
	}
	for _, f := range []struct{ name, value string }{
		{EnvGitHubAPI, c.GitHubAPI},
		{EnvModelEndpoint, c.ModelEndpoint},
	} {
		if !strings.HasPrefix(f.value, "https://") {
			missing = append(missing, f.name+" (an https URL)")
		}
	}
	if len(missing) > 0 {
		return errors.New("answerer configuration is incomplete: " + strings.Join(missing, ", "))
	}
	return nil
}

func digits(s string) bool {
	return s != "" && strings.Trim(s, "0123456789") == ""
}

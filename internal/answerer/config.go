package answerer

import (
	"errors"
	"strings"
)

// Config is the operator's fixed statement of who answers, where, with which
// credentials, through which model, and under which lock.
//
// Every field comes from the operator and none from a request. A request is
// untrusted evidence: nothing it says can change the responder, the mailbox a
// reply is posted to, the secrets used, or the model asked. That is why there is
// no method here that takes a request.
type Config struct {
	// Repository is the mailbox repository, "owner/name".
	Repository string
	// Mailbox is the pull request (issue) number the conversation lives on.
	Mailbox string
	// ResponderLogin and ResponderID are the GitHub account this process posts
	// as. The id is the identity: duplicate detection reads it, and a token
	// that authenticates as anybody else is refused before anything is posted.
	ResponderLogin string
	ResponderID    int64
	// GitHubAPI is the REST root. Empty means https://api.github.com.
	GitHubAPI string
	// GitHubToken is the posting credential. Secret: never logged, never put
	// in an error.
	GitHubToken string
	// ModelEndpoint is the chat-completions URL; Model is the model name.
	ModelEndpoint string
	Model         string
	// ModelKey is the model credential. Secret, handled as GitHubToken is.
	ModelKey string
	// LockPath is the single-writer lock file.
	LockPath string
}

// The environment names LoadConfig reads. The two secrets are read from FILES
// named here, never from the variables themselves, so a secret does not sit in
// the process environment.
const (
	EnvRepository      = "SENSEI_ANSWERER_REPOSITORY"
	EnvMailbox         = "SENSEI_ANSWERER_MAILBOX"
	EnvResponderLogin  = "SENSEI_ANSWERER_RESPONDER_LOGIN"
	EnvResponderID     = "SENSEI_ANSWERER_RESPONDER_ID"
	EnvGitHubAPI       = "SENSEI_ANSWERER_GITHUB_API"
	EnvGitHubTokenFile = "SENSEI_ANSWERER_GITHUB_TOKEN_FILE"
	EnvModelEndpoint   = "SENSEI_ANSWERER_MODEL_ENDPOINT"
	EnvModel           = "SENSEI_ANSWERER_MODEL"
	EnvModelKeyFile    = "SENSEI_ANSWERER_MODEL_KEY_FILE"
	EnvLockPath        = "SENSEI_ANSWERER_LOCK"
)

// DefaultModelEndpoint is used when the operator names no endpoint. The model
// itself has no default: which model answers is an operator decision.
const DefaultModelEndpoint = "https://api.openai.com/v1/chat/completions"

// LoadConfig reads the operator configuration through getenv and readSecret,
// and validates it whole. readSecret receives a path and returns the file's
// content; an error from it is reported by variable name only.
func LoadConfig(getenv func(string) string, readSecret func(path string) (string, error)) (Config, error) {
	if getenv == nil || readSecret == nil {
		return Config{}, errors.New("loading the answerer configuration needs an environment and a secret reader")
	}
	c := Config{
		Repository:     strings.TrimSpace(getenv(EnvRepository)),
		Mailbox:        strings.TrimSpace(getenv(EnvMailbox)),
		ResponderLogin: strings.TrimSpace(getenv(EnvResponderLogin)),
		GitHubAPI:      strings.TrimSpace(getenv(EnvGitHubAPI)),
		ModelEndpoint:  strings.TrimSpace(getenv(EnvModelEndpoint)),
		Model:          strings.TrimSpace(getenv(EnvModel)),
		LockPath:       strings.TrimSpace(getenv(EnvLockPath)),
	}
	if c.ModelEndpoint == "" {
		c.ModelEndpoint = DefaultModelEndpoint
	}
	id, ok := parseID(strings.TrimSpace(getenv(EnvResponderID)))
	if !ok {
		return Config{}, errors.New(EnvResponderID + " must be the responder's numeric GitHub user id")
	}
	c.ResponderID = id
	for _, secret := range []struct {
		env string
		dst *string
	}{
		{EnvGitHubTokenFile, &c.GitHubToken},
		{EnvModelKeyFile, &c.ModelKey},
	} {
		path := strings.TrimSpace(getenv(secret.env))
		if path == "" {
			return Config{}, errors.New(secret.env + " must name the file holding the credential")
		}
		value, err := readSecret(path)
		if err != nil {
			// The path is the operator's own configuration; the error text
			// could quote file content, so it is not carried.
			return Config{}, errors.New("reading the credential named by " + secret.env + " failed")
		}
		*secret.dst = strings.TrimSpace(value)
	}
	if err := c.Validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

// Validate refuses a configuration that could post as nobody in particular,
// to nowhere in particular, or without a lock.
func (c Config) Validate() error {
	owner, name, ok := strings.Cut(c.Repository, "/")
	if !ok || owner == "" || name == "" || strings.ContainsAny(c.Repository, " \t\n") || strings.Contains(name, "/") {
		return errors.New("the mailbox repository must be owner/name")
	}
	if _, ok := parseID(c.Mailbox); !ok {
		return errors.New("the mailbox must be a pull request number")
	}
	if strings.TrimSpace(c.ResponderLogin) == "" || c.ResponderID <= 0 {
		return errors.New("the responder must be configured by login and numeric id")
	}
	if c.GitHubToken == "" {
		return errors.New("no GitHub posting credential is configured")
	}
	if !strings.HasPrefix(c.ModelEndpoint, "https://") && !strings.HasPrefix(c.ModelEndpoint, "http://") {
		return errors.New("the model endpoint must be an http(s) URL")
	}
	if strings.TrimSpace(c.Model) == "" {
		return errors.New("no model is configured")
	}
	if c.ModelKey == "" {
		return errors.New("no model credential is configured")
	}
	if strings.TrimSpace(c.LockPath) == "" {
		return errors.New("no single-writer lock path is configured")
	}
	return nil
}

// parseID reads a positive decimal id. Anything else -- a sign, a space, a
// value too large for int64 -- is refused rather than read as some id.
func parseID(s string) (int64, bool) {
	if s == "" || len(s) > 18 {
		return 0, false
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, false
		}
		n = n*10 + int64(r-'0')
	}
	return n, n > 0
}

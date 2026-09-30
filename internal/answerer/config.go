// Package answerer supplies architecture and advisory-review bytes. It owns no
// architectural authority and cannot establish reviewer independence.
package answerer

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config is operator-owned. Request text is never interpreted as configuration.
type Config struct {
	Repository   string
	Number       string
	ResponderID  int64
	PublisherID  int64
	LockPath     string
	PollInterval time.Duration
	Model        string
	GitHubToken  string
	APIKey       string
}

func ConfigFromEnv() (Config, error) {
	c := Config{Repository: os.Getenv("ANSWERER_REPOSITORY"), Number: os.Getenv("ANSWERER_PR"), LockPath: os.Getenv("ANSWERER_LOCK"), Model: os.Getenv("ANSWERER_MODEL"), GitHubToken: os.Getenv("ANSWERER_GITHUB_TOKEN"), APIKey: os.Getenv("OPENAI_API_KEY"), PollInterval: 15 * time.Second}
	var err error
	c.ResponderID, err = strconv.ParseInt(os.Getenv("ANSWERER_RESPONDER_ID"), 10, 64)
	if err != nil {
		return c, errors.New("ANSWERER_RESPONDER_ID must be a numeric GitHub user id")
	}
	c.PublisherID, err = strconv.ParseInt(os.Getenv("ANSWERER_PUBLISHER_ID"), 10, 64)
	if err != nil {
		return c, errors.New("ANSWERER_PUBLISHER_ID must be a numeric GitHub user id")
	}
	return c, c.Validate()
}

func (c Config) Validate() error {
	parts := strings.Split(c.Repository, "/")
	if len(parts) != 2 || !safeName(parts[0]) || !safeName(parts[1]) {
		return errors.New("operator must name a GitHub owner/repository")
	}
	n, err := strconv.ParseUint(c.Number, 10, 64)
	if err != nil || n == 0 {
		return errors.New("operator must name a positive pull request number")
	}
	if c.ResponderID <= 0 || c.PublisherID <= 0 || c.ResponderID == c.PublisherID {
		return errors.New("distinct numeric responder and request publisher identities are required")
	}
	if !strings.HasPrefix(c.LockPath, "/") {
		return errors.New("operator must configure one absolute single-writer lock path")
	}
	if c.PollInterval <= 0 || strings.TrimSpace(c.Model) == "" || strings.TrimSpace(c.GitHubToken) == "" || strings.TrimSpace(c.APIKey) == "" {
		return errors.New("poll interval, model and both operator credentials are required")
	}
	return nil
}

func safeName(s string) bool {
	if s == "" || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

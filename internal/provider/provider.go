package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ID identifies a login surface, not a model. Provider credentials remain
// owned by the provider's native client and are never persisted by Sensei Code.
type ID string

const (
	ChatGPT     ID = "chatgpt"
	Codex       ID = "codex"
	Claude      ID = "claude"
	Antigravity ID = "antigravity"
)

var Ordered = []ID{ChatGPT, Codex, Claude, Antigravity}

type Status struct {
	ID            ID     `json:"id"`
	Label         string `json:"label"`
	Installed     bool   `json:"installed"`
	AuthKnown     bool   `json:"auth_known"`
	Authenticated bool   `json:"authenticated"`
	AuthMode      string `json:"auth_mode,omitempty"`
	Account       string `json:"account,omitempty"`
	Plan          string `json:"plan,omitempty"`
	Detail        string `json:"detail,omitempty"`
}

func Label(id ID) string {
	switch id {
	case ChatGPT:
		return "ChatGPT subscription (Codex app-server)"
	case Codex:
		return "Codex native login"
	case Claude:
		return "Claude Code"
	case Antigravity:
		return "Google Antigravity"
	default:
		return string(id)
	}
}

func Parse(value string) (ID, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "chatgpt", "openai":
		return ChatGPT, nil
	case "2", "codex":
		return Codex, nil
	case "3", "claude", "anthropic":
		return Claude, nil
	case "4", "antigravity", "agy", "google":
		return Antigravity, nil
	default:
		return "", fmt.Errorf("unknown provider %q", value)
	}
}

func StatusFor(ctx context.Context, id ID) Status {
	status := Status{ID: id, Label: Label(id)}
	switch id {
	case ChatGPT, Codex:
		if _, err := exec.LookPath("codex"); err != nil {
			status.Detail = "codex executable not found"
			return status
		}
		status.Installed = true
		account, err := ReadCodexAccount(ctx)
		if err != nil {
			status.Detail = err.Error()
			return status
		}
		status.AuthKnown = true
		status.Authenticated = account.Authenticated
		status.AuthMode = account.Type
		status.Account = account.Email
		status.Plan = account.Plan
		if !account.Authenticated {
			status.Detail = "not logged in"
		}
		return status
	case Claude:
		if _, err := exec.LookPath("claude"); err != nil {
			status.Detail = "claude executable not found"
			return status
		}
		status.Installed = true
		cmd := exec.CommandContext(ctx, "claude", "auth", "status")
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		err := cmd.Run()
		var payload struct {
			LoggedIn         bool   `json:"loggedIn"`
			AuthMethod       string `json:"authMethod"`
			APIProvider      string `json:"apiProvider"`
			SubscriptionType string `json:"subscriptionType"`
			Email            string `json:"email"`
		}
		if json.Unmarshal(stdout.Bytes(), &payload) == nil {
			status.AuthKnown = true
			status.Authenticated = payload.LoggedIn
			status.AuthMode = payload.AuthMethod
			status.Account = payload.Email
			status.Plan = payload.SubscriptionType
			if status.AuthMode == "" {
				status.AuthMode = payload.APIProvider
			}
			if !payload.LoggedIn {
				status.Detail = "not logged in"
			}
			return status
		}
		if err != nil {
			status.Detail = strings.TrimSpace(stderr.String())
			if status.Detail == "" {
				status.Detail = err.Error()
			}
			return status
		}
		status.Detail = "Claude auth state was not machine-readable"
		return status
	case Antigravity:
		if _, err := exec.LookPath("agy"); err != nil {
			status.Detail = "agy executable not found"
			return status
		}
		status.Installed = true
		// Antigravity deliberately owns Google Sign-In in the OS keyring. Its
		// public CLI does not expose a stable machine-readable auth-status API,
		// so installed and authenticated remain distinct facts here.
		status.Detail = "authentication is owned by Antigravity's system keyring"
		return status
	default:
		status.Detail = "unsupported provider"
		return status
	}
}

// Login delegates credential ownership to the native provider. ChatGPT is the
// exception only in UX: Sensei Code drives Codex's documented app-server login
// RPC, while Codex still owns persistence and token refresh.
func Login(ctx context.Context, id ID) (Status, error) {
	switch id {
	case ChatGPT:
		if err := LoginChatGPT(ctx); err != nil {
			return StatusFor(ctx, id), err
		}
		return StatusFor(ctx, id), nil
	case Codex:
		if err := runInteractive(ctx, "codex", "login"); err != nil {
			return StatusFor(ctx, id), err
		}
		return StatusFor(ctx, id), nil
	case Claude:
		if err := runInteractive(ctx, "claude", "auth", "login"); err != nil {
			return StatusFor(ctx, id), err
		}
		return StatusFor(ctx, id), nil
	case Antigravity:
		fmt.Fprintln(os.Stdout, "Antigravity owns Google Sign-In. Complete login in the native client, then exit back to Sensei Code.")
		if err := runInteractive(ctx, "agy"); err != nil {
			return StatusFor(ctx, id), err
		}
		return StatusFor(ctx, id), nil
	default:
		return Status{}, fmt.Errorf("unsupported provider %q", id)
	}
}

func Logout(ctx context.Context, id ID) error {
	switch id {
	case ChatGPT, Codex:
		return LogoutCodex(ctx)
	case Claude:
		return runInteractive(ctx, "claude", "auth", "logout")
	case Antigravity:
		return errors.New("Antigravity logout is provider-owned: run agy and use /logout")
	default:
		return fmt.Errorf("unsupported provider %q", id)
	}
}

func runInteractive(ctx context.Context, name string, args ...string) error {
	path, err := exec.LookPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

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

	"github.com/globulario/sensei-code/internal/roles"
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

// Roles is what this adapter declares it can satisfy.
//
// Declared rather than inferred, because the answer is a property of the
// transport and not of how capable the model is. ChatGPT reaches this project
// through the Codex app-server as a read-only architectural authority: it can
// architect and it can review, and it cannot implement — which says nothing
// about whether it could write the code.
//
// A provider that declares nothing cannot be assigned a role. That is the
// correct default for an adapter nobody has wired up: the alternative is
// discovering what it cannot do by giving it a job.
//
// counterexample_hunter and proof_runner are absent from every list on purpose.
// Neither has a driver yet, and declaring a capability nobody can exercise makes
// an unassignable role look like an available one.
func Roles(id ID) []roles.Role {
	switch id {
	case ChatGPT:
		return []roles.Role{roles.Architect, roles.Reviewer}
	case Codex, Claude:
		return []roles.Role{roles.Architect, roles.Implementer, roles.Reviewer}
	default:
		return nil
	}
}

// Capability is this adapter's declaration, in the form the role assigner reads.
func Capability(name string) roles.Capability {
	id, err := Parse(name)
	if err != nil {
		return roles.Capability{Provider: name}
	}
	return roles.Capability{Provider: name, Roles: Roles(id)}
}

// TurnCapability is whether a provider can actually serve a turn.
//
// It is owned here rather than derived by a diagnostic, because the provider is
// the only party that knows. doctor used to conclude readiness from
// authentication, which is a fact about credentials and not about capability,
// and the two are routinely different: an authenticated subscription with no
// quota left is authenticated and cannot serve anything.
//
// Unknown is the honest default and the only value StatusFor produces today.
// Establishing capability means spending a turn, and a readiness check that
// costs a weekly-budgeted turn to answer itself is one people learn to skip.
// The field exists so a caller that has legitimately demonstrated capability --
// a probe the operator asked for, or a turn that just succeeded -- can say so
// without doctor inferring it.
type TurnCapability string

const (
	// CapabilityUnknown means nobody has established whether a turn would work.
	// It is not a failure and it is not readiness.
	CapabilityUnknown TurnCapability = "unknown"
	// CapabilityDemonstrated means a turn was served.
	CapabilityDemonstrated TurnCapability = "demonstrated"
	// CapabilityRefused means a turn was attempted and refused -- quota,
	// timeout, malformed output, or an unusable configuration.
	CapabilityRefused TurnCapability = "refused"
)

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
	// Capability is what this provider says about its own ability to serve a
	// turn. StatusFor never claims more than Unknown: it inspects installation
	// and stored credentials and starts nothing.
	Capability TurnCapability `json:"capability"`
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
	// Unknown until somebody demonstrates otherwise. Reading a credential file
	// establishes that credentials exist, which is a different claim.
	status := Status{ID: id, Label: Label(id), Capability: CapabilityUnknown}
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
		status.AuthMode = account.Type
		status.Account = account.Email
		status.Plan = account.Plan
		if id == ChatGPT {
			status.Authenticated = account.Authenticated && account.Type == "chatgpt"
			if account.Authenticated && account.Type != "chatgpt" {
				status.Detail = "Codex is authenticated with " + nonempty(account.Type, "another auth mode") + ", not ChatGPT"
			} else if !status.Authenticated {
				status.Detail = "not logged in with ChatGPT"
			}
		} else {
			status.Authenticated = account.Authenticated
			if !account.Authenticated {
				status.Detail = "not logged in"
			}
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
			APIKeySource     string `json:"apiKeySource"`
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
				return status
			}
			// An ambient key would override this subscription login for a bare
			// `claude` invocation, and does not override it for Sensei Code:
			// SessionOnlyEnv strips exactly these variables from every agent
			// process, so the account reported here IS the one a worker
			// authenticates with.
			//
			// The earlier wording said the opposite -- that the key overrides
			// this login and the work would not use the subscription -- which
			// contradicted the stripping it sits ten lines above. Reporting a
			// credential the work does not use is the same defect as reporting a
			// readiness the provider never claimed, and this file now carries
			// both halves of that lesson.
			//
			// The key is still worth naming. It is what a shell running `claude`
			// outside this tool will use, and somebody comparing the two needs to
			// know why they differ.
			if source := envKeySource(payload.APIKeySource); source != "" {
				status.Detail = "using this subscription login; " + source +
					" is present in the environment and is stripped from agent processes," +
					" so it applies only to " + string(id) + " run outside Sensei Code"
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

func nonempty(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

// envKeySource reports the environment variable an agent's credential is coming
// from, when it is coming from one rather than from the stored login.
func envKeySource(apiKeySource string) string {
	source := strings.TrimSpace(apiKeySource)
	switch source {
	case "", "none", "login", "claude.ai":
		return ""
	}
	// Values that name an environment variable are upper snake case.
	if source == strings.ToUpper(source) && strings.Contains(source, "_") {
		return source
	}
	return ""
}

// SessionOnlyEnv lists credential variables that override an agent's own stored
// login. Sensei Code strips them from agent processes so every agent
// authenticates with the session that /login established and that doctor
// reports. Without this an ambient key silently takes precedence, doctor
// describes a subscription the work never uses, and the worker fails
// authentication against a credential nobody chose for it.
//
// The cost is deliberate: a user who wants API-key authentication must
// configure it on the agent rather than rely on an inherited variable, because
// an inherited variable is invisible to every check in this tool.
//
// The list covers every provider this tool reports on, because the guarantee is
// made for all of them. It used to name Anthropic's variables only, while the
// ChatGPT/Codex branch of StatusFor read stored Codex credentials and reported
// the account with the same implicit claim -- that this is the account a worker
// authenticates with -- and nothing removed an ambient OPENAI_API_KEY that
// might have taken precedence over the stored login.
//
// Whether the Codex CLI actually prefers the variable to its stored login is
// not verified here, and the stripping does not depend on knowing: it forces
// the login that /login established and that doctor reports, which is the
// property being claimed. Leaving the variable in place to find out would mean
// making a claim first and checking it later.
var SessionOnlyEnv = []string{
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"CLAUDE_CODE_OAUTH_TOKEN",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"CODEX_API_KEY",
}

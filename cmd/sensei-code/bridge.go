package main

import (
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// cliFallback is the resolver the engine uses when nothing else is installed:
// the provider command line.
//
// It exists because ghbridge.Resolver REFUSES rather than improvising when its
// Fallback is nil, which is correct -- a bridge that quietly built a CLI runner
// for a role it could not carry would answer a governed turn with the local
// agent and report it as the remote's. So a caller installing the bridge must
// hand it the behaviour it is wrapping, not nil.
type cliFallback struct{ sessionID string }

func (c cliFallback) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	return workflow.CLIResolved(spec, c.sessionID), nil
}

// installGitHubBridge points an engine's role turns at a GitHub mailbox.
//
// This is shared by every surface rather than living on `control`'s flags,
// because the two halves a governed run needs were in different processes:
// control could carry an architect turn to ChatGPT but could not resolve the
// human-owned boundary that turn produced, while the TUI could resolve one and
// had no bridge. A run that escalates needs both in one process.
//
// Returns nil having changed nothing when no bridge is configured, so an
// installation that has never heard of this behaves exactly as it did.
func installGitHubBridge(engine *workflow.Engine, repoRoot string, g config.GitHubBridge) (string, error) {
	if !g.Configured() {
		return "", nil
	}
	runners, err := composeEngineResolver(cliFallback{sessionID: engine.SessionID}, repoRoot, engine.SessionID, githubBridgeConfig{
		MailboxPR:     g.MailboxPR,
		ReviewerID:    g.ReviewerID,
		ReviewerLogin: g.ReviewerLogin,
		Provider:      g.Provider,
		Remote:        g.Remote,
		Roles:         mustRoles(g.Roles),
		Doorbell:      g.Doorbell,
		App: ghbridge.AppConfig{
			AppID:          g.AppID,
			InstallationID: g.InstallationID,
			PrivateKeyPath: g.AppKey,
			Owner:          g.Owner,
			Repo:           g.Repo,
		},
	})
	if err != nil {
		return "", fmt.Errorf("the configured github bridge could not be installed: %w", err)
	}
	engine.Runners = runners.Resolver
	return runners.Banner, nil
}

// mustRoles reads the closed role vocabulary, defaulting to the same set
// control's flag does.
//
// An unreadable spec returns the EMPTY set, never a silently full one: carrying
// a role nobody asked for sends a governed turn to a remote that may not be
// attended, and the run then waits out its deadline looking exactly like a
// remote that declined. Refusing to carry is recoverable; carrying by accident
// is not.
func mustRoles(spec string) map[roles.Role]bool {
	if strings.TrimSpace(spec) == "" {
		spec = "architect,reviewer"
	}
	parsed, err := parseBridgeRoles(spec)
	if err != nil {
		return map[roles.Role]bool{}
	}
	return parsed
}

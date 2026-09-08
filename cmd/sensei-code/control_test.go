package main

import (
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// The composition boundary between control.Server and the GitHub review bridge.
//
// These tests exist because both halves of it -- constructing the actual
// ghbridge.Runner, and refusing an unauthenticatable mailbox before installing
// anything -- were removed and restored during one change. Nothing failed while
// they were gone. A boundary whose absence is invisible is a boundary that needs
// a test, so each outcome below is pinned by name:
//
//	off                     -> the control server, unchanged
//	invalid mailbox         -> refusal, nothing installed
//	partial App config      -> refusal, and NEVER the personal gh path
//	configured              -> ghbridge.Resolver over the control server
//
// Hermetic by construction: control.New does not listen, and composeEngineResolver
// performs no I/O. No socket is bound and no GitHub call is made.

const testRepoRoot = "/srv/checkouts/sensei-code"

// newControlServer builds the real control.Server the composition falls back
// to. A stub would prove the shape of the composition without proving the thing
// the shape is for.
func newControlServer(t *testing.T) *control.Server {
	t.Helper()
	root := t.TempDir()
	store, err := session.New(root, "sess-compose")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	engine := workflow.New(gitx.Repo{Root: root}, config.Default(), event.NewBus(), store, "sess-compose")
	cred, err := control.Mint()
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	server, err := control.New(engine, cred, control.Options{
		Workspace: "github.com/globulario/sensei-code",
	})
	if err != nil {
		t.Fatalf("control.New: %v", err)
	}
	return server
}

// configuredBridge is a bridge nothing is wrong with, so each test can spoil
// exactly one field and attribute the outcome to that field.
func configuredBridge() githubBridgeConfig {
	return githubBridgeConfig{
		MailboxPR:     "412",
		ReviewerID:    1697116,
		ReviewerLogin: "the-reviewer",
		Provider:      "chatgpt",
		Remote:        "upstream",
		Wait:          17 * time.Minute,
		App: ghbridge.AppConfig{
			AppID:          8814,
			InstallationID: 55123,
			PrivateKeyPath: "/etc/sensei-code/app.pem",
			Owner:          "globulario",
			Repo:           "sensei-code",
		},
	}
}

func TestBridgeOffLeavesTheControlServerResolving(t *testing.T) {
	server := newControlServer(t)

	for _, issue := range []string{"", "   "} {
		gh := configuredBridge()
		gh.MailboxPR = issue

		got, err := composeEngineResolver(server, testRepoRoot, "sess-compose", gh)
		if err != nil {
			t.Fatalf("issue %q: unconfigured bridge refused: %v", issue, err)
		}
		if got.Resolver != workflow.RunnerResolver(server) {
			t.Fatalf("issue %q: the engine resolver is %T, not the control server", issue, got.Resolver)
		}
		if got.Banner != "" {
			t.Fatalf("issue %q: an unconfigured bridge announced itself: %q", issue, got.Banner)
		}
	}
}

func TestBridgeWithAnUnauthenticatableMailboxInstallsNothing(t *testing.T) {
	server := newControlServer(t)

	gh := configuredBridge()
	gh.ReviewerID = 0
	gh.ReviewerLogin = "  "

	got, err := composeEngineResolver(server, testRepoRoot, "sess-compose", gh)
	if err == nil {
		t.Fatal("a mailbox that cannot authenticate a sender was accepted")
	}
	if got.Resolver != nil {
		t.Fatalf("a refused bridge still installed a resolver: %T", got.Resolver)
	}
	if got.Banner != "" {
		t.Fatalf("a refused bridge still announced itself: %q", got.Banner)
	}
	if !strings.Contains(err.Error(), "expected reviewer") {
		t.Fatalf("the refusal does not name the missing reviewer: %v", err)
	}
}

func TestPartialAppConfigRefusesRatherThanFallingBackToPersonalGH(t *testing.T) {
	server := newControlServer(t)

	// Each field, alone, is an operator who plainly asked for App transport.
	// None of them may reach the operator's own gh credentials.
	partials := map[string]func(*ghbridge.AppConfig){
		"app id only":          func(a *ghbridge.AppConfig) { *a = ghbridge.AppConfig{AppID: 8814} },
		"installation id only": func(a *ghbridge.AppConfig) { *a = ghbridge.AppConfig{InstallationID: 55123} },
		"key path only":        func(a *ghbridge.AppConfig) { *a = ghbridge.AppConfig{PrivateKeyPath: "/etc/k.pem"} },
		"owner only":           func(a *ghbridge.AppConfig) { *a = ghbridge.AppConfig{Owner: "globulario"} },
		"repo only":            func(a *ghbridge.AppConfig) { *a = ghbridge.AppConfig{Repo: "sensei-code"} },
		"app id forgotten": func(a *ghbridge.AppConfig) {
			a.AppID = 0
		},
	}

	for name, spoil := range partials {
		t.Run(name, func(t *testing.T) {
			gh := configuredBridge()
			spoil(&gh.App)

			got, err := composeEngineResolver(server, testRepoRoot, "sess-compose", gh)
			if err == nil {
				t.Fatal("a partly configured App transport was accepted")
			}
			if got.Resolver != nil {
				t.Fatalf("a refused App transport still installed a resolver: %T", got.Resolver)
			}
			if got.Banner != "" {
				t.Fatalf("a refused App transport still announced itself: %q", got.Banner)
			}
			if !strings.Contains(err.Error(), "gh credentials") {
				t.Fatalf("the refusal does not say it is refusing the personal gh path: %v", err)
			}
		})
	}
}

// installedBridge composes a fully configured bridge and returns the resolver
// as the concrete type, failing the test if the composition did not install one.
func installedBridge(t *testing.T, server *control.Server, gh githubBridgeConfig) (ghbridge.Resolver, string) {
	t.Helper()
	got, err := composeEngineResolver(server, testRepoRoot, "sess-compose", gh)
	if err != nil {
		t.Fatalf("a fully configured bridge was refused: %v", err)
	}
	bridge, ok := got.Resolver.(ghbridge.Resolver)
	if !ok {
		t.Fatalf("the engine resolver is %T, not a ghbridge.Resolver", got.Resolver)
	}
	return bridge, got.Banner
}

func TestConfiguredBridgeInstallsTheResolverExactlyAsConfigured(t *testing.T) {
	server := newControlServer(t)
	gh := configuredBridge()
	bridge, _ := installedBridge(t, server, gh)

	if bridge.Provider != "chatgpt" {
		t.Errorf("Provider = %q, want the configured reviewer provider %q", bridge.Provider, "chatgpt")
	}
	if bridge.Fallback != workflow.RunnerResolver(server) {
		t.Errorf("Fallback = %T, want the existing control server", bridge.Fallback)
	}

	// The runner itself. It went missing once during a repair and nothing
	// noticed, because a Resolver with a nil Reviewer still satisfies the
	// interface and still routes everything to the fallback.
	if bridge.Reviewer == nil {
		t.Fatal("the bridge installed no reviewer runner: every review turn would fall through to the control server")
	}
	if got, want := bridge.Reviewer.Issue.Number, "412"; got != want {
		t.Errorf("Reviewer.Issue.Number = %q, want the exact configured mailbox %q", got, want)
	}
	if got, want := bridge.Reviewer.Issue.Dir, testRepoRoot; got != want {
		t.Errorf("Reviewer.Issue.Dir = %q, want the repository root %q", got, want)
	}
	if got, want := bridge.Reviewer.Issue.ExpectedReviewer, (ghbridge.Principal{UserID: 1697116, Login: "the-reviewer"}); got != want {
		t.Errorf("Reviewer.Issue.ExpectedReviewer = %+v, want %+v", got, want)
	}
	if got, want := bridge.Reviewer.RepoDir, testRepoRoot; got != want {
		t.Errorf("Reviewer.RepoDir = %q, want the exact repository root %q", got, want)
	}
	if got, want := bridge.Reviewer.Remote, "upstream"; got != want {
		t.Errorf("Reviewer.Remote = %q, want the configured remote %q", got, want)
	}
	if bridge.Reviewer.NewRequestID == nil {
		t.Error("Reviewer.NewRequestID is nil: every review turn would refuse for want of a request id source")
	} else if id := bridge.Reviewer.NewRequestID(); strings.TrimSpace(id) == "" {
		t.Error("Reviewer.NewRequestID minted an empty id")
	}
	if got, want := bridge.Reviewer.SessionID, "sess-compose"; got != want {
		t.Errorf("Reviewer.SessionID = %q, want %q", got, want)
	}
	if got, want := bridge.Reviewer.Wait, 17*time.Minute; got != want {
		t.Errorf("Reviewer.Wait = %v, want the configured wait %v", got, want)
	}
}

// The App client must survive INTO the runner, not merely into the banner.
//
// Announcing App transport over a runner still holding the operator's gh
// credentials would put machine-originated mailbox activity under a person's
// identity while reporting the opposite.
func TestTheConfiguredAppClientSurvivesIntoTheRunnersMailbox(t *testing.T) {
	server := newControlServer(t)
	gh := configuredBridge()
	bridge, banner := installedBridge(t, server, gh)

	api := bridge.Reviewer.Issue.API
	if api == nil {
		t.Fatal("the configured App client did not reach Runner.Issue.API: the mailbox would use the operator's gh credentials")
	}
	if !api.Configured() {
		t.Error("the App client reached the runner unconfigured")
	}
	if api.Owner != "globulario" || api.Repo != "sensei-code" {
		t.Errorf("the runner addresses %s/%s, not the configured repository globulario/sensei-code", api.Owner, api.Repo)
	}
	if api.Auth == nil {
		t.Fatal("the App client reached the runner with no installation auth")
	}
	if api.Auth.AppID != 8814 || api.Auth.InstallationID != 55123 {
		t.Errorf("the runner authenticates as app %d installation %d, not the configured 8814/55123",
			api.Auth.AppID, api.Auth.InstallationID)
	}
	if api.Auth.PrivateKeyPath != "/etc/sensei-code/app.pem" {
		t.Errorf("PrivateKeyPath = %q, want the configured path", api.Auth.PrivateKeyPath)
	}

	// And the line the operator reads describes the mailbox that was installed.
	if !strings.Contains(banner, "github app 8814 installation 55123 (globulario/sensei-code)") {
		t.Errorf("the banner does not report the App transport that was installed: %q", banner)
	}
}

// With no App configured at all the legacy gh path stays available, and the
// banner says so rather than claiming an App.
func TestNoAppConfiguredKeepsTheGHMailboxAndSaysSo(t *testing.T) {
	server := newControlServer(t)
	gh := configuredBridge()
	gh.App = ghbridge.AppConfig{}

	bridge, banner := installedBridge(t, server, gh)
	if bridge.Reviewer.Issue.API != nil {
		t.Error("an unselected App transport produced a client anyway")
	}
	if !strings.Contains(banner, "operator gh credentials") {
		t.Errorf("the banner does not report the gh mailbox: %q", banner)
	}
}

// Routing. The bridge is a transport for one assigned provider, and everything
// else must still reach the control server -- otherwise a delegated architect
// quietly becomes the local one.
func TestTheBridgeCarriesOnlyItsProvidersReviewTurns(t *testing.T) {
	server := newControlServer(t)
	bridge, _ := installedBridge(t, server, configuredBridge())

	cases := []struct {
		name       string
		spec       workflow.RunnerSpec
		wantBridge bool
	}{
		{
			name:       "architect reaches the control server",
			spec:       workflow.RunnerSpec{Role: roles.Architect, Agent: config.Agent{Name: "chatgpt"}},
			wantBridge: false,
		},
		{
			name:       "implementer reaches the control server",
			spec:       workflow.RunnerSpec{Role: roles.Implementer, Agent: config.Agent{Name: "chatgpt"}},
			wantBridge: false,
		},
		{
			name:       "proof runner reaches the control server",
			spec:       workflow.RunnerSpec{Role: roles.ProofRunner, Agent: config.Agent{Name: "chatgpt"}},
			wantBridge: false,
		},
		{
			name:       "a reviewer assigned to another provider reaches the control server",
			spec:       workflow.RunnerSpec{Role: roles.Reviewer, Agent: config.Agent{Name: "claude"}},
			wantBridge: false,
		},
		{
			name:       "a reviewer assigned to the carried provider reaches the github runner",
			spec:       workflow.RunnerSpec{Role: roles.Reviewer, Agent: config.Agent{Name: "chatgpt"}},
			wantBridge: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := bridge.Resolve(tc.spec)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			gotBridge := resolved.Runner == agent.Runner(bridge.Reviewer)
			if gotBridge != tc.wantBridge {
				t.Fatalf("resolved to %T (bridge=%v), want bridge=%v", resolved.Runner, gotBridge, tc.wantBridge)
			}
			if tc.wantBridge {
				// The transport does not rename the assignment.
				if resolved.Name != "chatgpt" {
					t.Errorf("the carried turn resolved as provider %q, not the assigned chatgpt", resolved.Name)
				}
				return
			}
			// Reaching the control server means its own answer, not the
			// bridge's: with nothing delegated that is the provider command line.
			if _, ok := resolved.Runner.(agent.CLI); !ok {
				t.Fatalf("the fallback answered with %T, not the control server's own resolution", resolved.Runner)
			}
			if resolved.Name != tc.spec.Agent.Name {
				t.Errorf("the fallback resolved as %q, not the assigned %q", resolved.Name, tc.spec.Agent.Name)
			}
		})
	}
}

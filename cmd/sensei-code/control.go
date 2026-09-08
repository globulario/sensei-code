package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/control"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/ghwebhook"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// tokenEnv supplies a stable credential across restarts.
//
// The lifecycle is deliberately blunt and stated rather than clever:
//
//	set      the token, and the principal derived from it, survive a restart.
//	         Nothing is written and nothing is printed -- the operator already
//	         has the secret, and handing it back is the only way this process
//	         could turn it into a log entry.
//	unset    a token is minted for this run, written to a 0600 file, and
//	         removed on a clean shutdown. Rotation IS restarting.
//
// There is no rotation endpoint, because a surface that can reissue its own
// credential is a surface that can be talked into reissuing its own credential.
const tokenEnv = "SENSEI_CODE_CONTROL_TOKEN"

// tokenFile is where a minted credential is left for the operator to read.
//
// A file rather than stdout, and the distinction is not fussiness. Under
// systemd, Docker, CI or a captured terminal, stdout is durable storage owned
// by somebody else, and a secret printed there outlives the process that
// created it by however long the log is kept. A file has an owner, a mode, and
// a lifetime this process controls.
func tokenFile(repoRoot string) string {
	return filepath.Join(repoRoot, ".sensei-code", "control-token")
}

// writeMintedToken leaves a freshly minted credential where the operator can
// read it, and nowhere else.
func writeMintedToken(repoRoot string, cred control.Credential) (string, error) {
	path := tokenFile(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	// Truncated and recreated with an explicit mode rather than written over,
	// so a file left world-readable by something else does not stay that way.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if _, err := f.WriteString(cred.Token() + "\n"); err != nil {
		return "", err
	}
	return path, nil
}

// runControlSurface serves the remote control surface for this repository.
//
// It is a separate verb from `mcp`, which configures each AGENT's access to
// Sensei. This is the opposite direction: a capable agent reaching in to hold a
// role here. Naming them the same would make the two directions of the same
// word mean opposite things.
//
// Not named runControl: that is already an interface in run.go meaning control
// OVER a run -- defer, stop, time out. Two unrelated things under one name in
// one package is how a reader ends up at the wrong one.
func runControlSurface(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("control", flag.ContinueOnError)
	addr := fs.String("addr", control.DefaultAddr, "loopback address to bind; a non-loopback address is refused")
	ttl := fs.Duration("lease", 0, "how long a role session holds without renewal (default 15m)")
	// The GitHub review bridge, off unless explicitly configured. All three
	// must be given together: an issue nobody answers on, or a mailbox that
	// cannot authenticate a sender, is not a usable bridge.
	ghRoles := fs.String("github-bridge-roles", "architect,reviewer",
		"comma-separated role turns the GitHub bridge carries: architect, reviewer, or none. "+
			"A role left out is served by the engine's own assignment ladder and recorded as such")
	ghDoorbell := fs.Bool("github-doorbell", false,
		"after publishing a request as the App, post a [sensei-code:wake] locator through the operator's gh "+
			"credentials; needed only where the remote wake path does not admit App-authored comments")
	ghMailboxPR := fs.String("github-mailbox-pr", "", "GitHub PULL REQUEST number whose top-level conversation is the architect/reviewer mailbox; enables the GitHub bridge")
	ghIssue := fs.String("github-review-issue", "", "deprecated alias for -github-mailbox-pr; the mailbox must still be a pull request")
	ghReviewerID := fs.Int64("github-reviewer-id", 0, "immutable GitHub user id permitted to answer review requests")
	ghReviewerLogin := fs.String("github-reviewer-login", "", "GitHub login of that reviewer, for display")
	ghProvider := fs.String("github-review-provider", "chatgpt", "which assigned reviewer provider the GitHub bridge carries")
	ghRemote := fs.String("github-remote", "origin", "git remote the review snapshot is pushed to")
	ghWait := fs.Duration("github-review-wait", 0, "how long a GitHub review turn waits for an answer (default 30m)")
	// GitHub App installation authentication for the mailbox. Selecting it is
	// deliberate: with an app id configured there is NO fallback to the
	// operator's gh credentials, because machine-originated mailbox activity
	// appearing under a person's identity is what this exists to stop.
	// Only the key PATH is configuration. The key content never is.
	ghAppID := fs.Int64("github-app-id", 0, "GitHub App id; enables App installation auth for the mailbox")
	ghInstallID := fs.Int64("github-installation-id", 0, "GitHub App installation id")
	ghKeyPath := fs.String("github-app-key", "", "path to the GitHub App private key (content is never read into config or logs)")
	ghOwner := fs.String("github-owner", "", "repository owner the mailbox lives under; never inferred from cwd or remote")
	ghRepo := fs.String("github-repo", "", "repository name the mailbox lives in; never inferred from cwd or remote")
	// GitHub webhook ingress, off unless explicitly configured. A SECOND
	// listener, deliberately not the MCP one: that surface authenticates a
	// Bearer credential and this one authenticates an HMAC over raw bytes, and
	// one surface holding both regimes is how a request authenticated under one
	// rule gets served by the other. Loopback only -- the public edge is a
	// reverse proxy in front of this process, not this socket.
	//
	// Only the secret's PATH is configuration. The content never is.
	whAddr := fs.String("github-webhook-addr", "", "loopback address for GitHub webhook ingress; enables the webhook listener")
	whSecret := fs.String("github-webhook-secret-file", "", "path to the webhook HMAC secret (content is never read into config, argv or logs)")
	whInstall := fs.Int64("github-webhook-installation-id", 0, "the one App installation whose deliveries are accepted")
	whRepoID := fs.Int64("github-webhook-repository-id", 0, "numeric repository id the deliveries must be for")
	whRepo := fs.String("github-webhook-repository", "", "expected repository full name, owner/name")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// The workspace this surface serves is resolved once, here, from Sensei —
	// never from a request. A client that could name its own workspace could
	// ask this instance about a repository it does not serve.
	domain, err := controlDomain(ctx, repo, cfg)
	if err != nil {
		return err
	}

	cred, supplied, err := credentialFromEnvOrMint()
	if err != nil {
		return err
	}
	tokenAt := ""
	if !supplied {
		tokenAt, err = writeMintedToken(repo.Root, cred)
		if err != nil {
			return fmt.Errorf("the minted control credential could not be stored: %w", err)
		}
		// The credential dies with the process that minted it. A clean shutdown
		// takes the file with it; a kill leaves it, which is why the next start
		// truncates and recreates it.
		defer os.Remove(tokenAt)
	}

	sessionID := session.ID(time.Now())
	store, err := session.New(repo.Root, sessionID)
	if err != nil {
		return err
	}
	bus := event.NewBus()
	engine := workflow.New(repo, cfg, bus, store, sessionID)

	// The operator placed an objective and then watched a silent terminal.
	//
	// A headless orchestrator that reports nothing is one where a run can fail
	// at the start gate and the only person who could act on it is looking at a
	// banner. Terminal events and status lines are printed; the full stream is
	// in the session record either way.
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()
	go func() {
		for ev := range events {
			if terminal(ev.Kind) || ev.Kind == event.TaskCreated || ev.Kind == event.ModeSelected {
				fmt.Println(renderEvent(ev))
			}
		}
	}()

	server, err := control.New(engine, cred, control.Options{
		Addr: *addr, Workspace: domain, LeaseTTL: *ttl,
	})
	if err != nil {
		return err
	}
	// The server becomes this engine's runner resolver. Without this the
	// rendezvous exists and nothing ever reaches it: every architect and
	// reviewer turn would take the configured command line, and a remote role
	// holder would register, inspect, and be asked nothing.
	//
	// One engine owner per orchestrated run: this process owns this engine, and
	// the resolver it consults is this process's own server -- or, with the
	// GitHub review bridge configured, a bridge that still falls back to it.
	//
	// Composed rather than decided here: composeEngineResolver is where the
	// bridge's off/refuse/install outcomes live, so they are provable without
	// binding a socket.
	mailboxPR, err := effectiveMailbox(*ghMailboxPR, *ghIssue)
	if err != nil {
		return err
	}
	bridgeRoles, err := parseBridgeRoles(*ghRoles)
	if err != nil {
		return err
	}
	runners, err := composeEngineResolver(server, repo.Root, engine.SessionID, githubBridgeConfig{
		MailboxPR:     mailboxPR,
		ReviewerID:    *ghReviewerID,
		ReviewerLogin: *ghReviewerLogin,
		Provider:      *ghProvider,
		Remote:        *ghRemote,
		Wait:          *ghWait,
		Roles:         bridgeRoles,
		Doorbell:      *ghDoorbell,
		App: ghbridge.AppConfig{
			AppID:          *ghAppID,
			InstallationID: *ghInstallID,
			PrivateKeyPath: *ghKeyPath,
			Owner:          *ghOwner,
			Repo:           *ghRepo,
		},
	})
	if err != nil {
		return err
	}
	engine.Runners = runners.Resolver
	if runners.Banner != "" {
		// Established before the banner prints, so the operator never reads a
		// healthy-looking mailbox line about a conversation nothing will wake on.
		// Refusal rather than a warning: a bridge whose requests are structurally
		// unanswerable is not a degraded bridge, it is a silent one, and silence
		// here is indistinguishable from a remote party that declined to reply.
		verifyCtx, cancelVerify := context.WithTimeout(context.Background(), mailboxVerifyTimeout)
		err := ghbridge.VerifyMailboxIsPullRequest(verifyCtx, runners.Mailbox)
		cancelVerify()
		if err != nil {
			return err
		}
		fmt.Println(runners.Banner)
	}

	// GitHub webhook ingress. Built before anything binds, so a wrong secret
	// path, a world-readable key or a non-loopback address is a startup refusal
	// rather than a surprise on GitHub's first delivery.
	//
	// The sink is an observation and nothing else. This process gains a way to
	// LEARN that a comment was posted; it gains no way to act on one. A signed
	// webhook is transport truth, not objective authority, and the ingress
	// cannot reach the engine even if a later author wants it to -- the import
	// boundary is pinned in internal/ghwebhook.
	webhook, err := ghwebhook.Config{
		Addr:           *whAddr,
		SecretFile:     *whSecret,
		InstallationID: *whInstall,
		RepositoryID:   *whRepoID,
		Repository:     *whRepo,
	}.Server(ghwebhook.NewObservationSink(os.Stdout))
	if err != nil {
		return err
	}
	if webhook != nil {
		if err := webhook.Listen(); err != nil {
			return err
		}
		defer webhook.Close()
		go func() {
			if err := webhook.Serve(); err != nil {
				fmt.Fprintln(os.Stderr, "sensei-code control: the github webhook ingress stopped:", err)
			}
		}()
	}

	if err := server.Listen(*addr); err != nil {
		return err
	}
	defer server.Close()

	// The operator's objective channel. Bound before the remote surface starts
	// serving, and refused if another process already owns it -- exactly one
	// process may own the engine for a repository, and two control processes
	// racing for the same socket is how that stops being true.
	if err := server.ListenLocal(repo.Root); err != nil {
		return err
	}
	defer server.CloseLocal()
	go func() {
		// SubmitGovernedLocal, and nothing else reachable from here. The
		// channel carries an objective; how it is carried out stays this
		// process's decision.
		if err := server.ServeLocal(func(task string) workflow.Submission {
			return engine.SubmitGovernedLocal(ctx, task)
		}); err != nil {
			fmt.Fprintln(os.Stderr, "sensei-code control: the objective channel stopped:", err)
		}
	}()

	printControlBanner(server, cred, tokenAt, supplied)
	printWebhookBanner(webhook, *whSecret, *whRepo)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	return server.Serve()
}

// githubBridgeConfig is the operator's GitHub review bridge configuration,
// exactly as the flags supplied it. Nothing here is inferred: neither the
// mailbox, the reviewer, the carried provider, nor the repository the App
// addresses.
type githubBridgeConfig struct {
	// MailboxPR is the pull request whose top-level conversation carries
	// architect and reviewer turns. Named for what it must be rather than for
	// the REST resource that addresses it: the conversation is reached through
	// GitHub's issues endpoint, but an ordinary issue is not an acceptable
	// value, because nothing wakes on one.
	MailboxPR     string
	ReviewerID    int64
	ReviewerLogin string
	Provider      string
	Remote        string
	Wait          time.Duration
	App           ghbridge.AppConfig
	// Roles is the closed set of role turns the bridge carries, already parsed.
	Roles map[roles.Role]bool
	// Doorbell enables the wake locator. Off by default: it exists only for a
	// remote wake path that cannot see the App, and it posts under the
	// operator's account, so it is an explicit choice rather than a silent
	// default.
	Doorbell bool
}

// mailboxVerifyTimeout bounds the one startup question asked of GitHub. Finite
// for the same reason every remote exchange here is: a bridge that hung while
// establishing its own target would fail to start in a way that looks like a
// hang rather than a refusal.
const mailboxVerifyTimeout = 30 * time.Second

// effectiveMailbox resolves the ONE mailbox target from the PR-specific flag and
// the deprecated alias.
//
// -github-review-issue predates the move to a pull-request conversation and is
// kept so an existing deployment does not silently lose its mailbox on upgrade.
// It is an ALIAS, never a second opinion: supplying both with different numbers
// is refused, because a deployment that disagreed with itself about where
// requests go would post into one conversation and wait for answers in another,
// and would look exactly like a remote party that never replied.
//
// The alias buys no leniency about what the number must BE. Either flag still
// has to name a pull request, and VerifyMailboxIsPullRequest establishes that
// for both.
func effectiveMailbox(mailboxPR, legacyIssue string) (string, error) {
	pr, legacy := strings.TrimSpace(mailboxPR), strings.TrimSpace(legacyIssue)
	switch {
	case pr == "" && legacy == "":
		return "", nil
	case pr != "" && legacy != "" && pr != legacy:
		return "", fmt.Errorf("-github-mailbox-pr is #%s and -github-review-issue is #%s; "+
			"they name one mailbox and must not disagree", pr, legacy)
	case pr != "":
		return pr, nil
	default:
		return legacy, nil
	}
}

// parseBridgeRoles reads the closed role vocabulary by MEMBERSHIP.
//
// Only "architect", "reviewer" and "none" are words here. Anything else is
// refused rather than ignored, because a misspelling silently dropped would
// leave the operator believing a role is carried when the bridge never accepts
// it -- and the symptom would be a turn served locally with no indication that
// the configuration meant otherwise.
//
// "none" is exclusive. Combining it with a role is a contradiction, not a
// precedence puzzle to resolve quietly.
func parseBridgeRoles(spec string) (map[roles.Role]bool, error) {
	out := map[roles.Role]bool{}
	var sawNone bool
	for _, field := range strings.Split(spec, ",") {
		switch name := strings.ToLower(strings.TrimSpace(field)); name {
		case "":
			continue
		case "none":
			sawNone = true
		case "architect":
			out[roles.Architect] = true
		case "reviewer":
			out[roles.Reviewer] = true
		default:
			return nil, fmt.Errorf("-github-bridge-roles: %q is not a role this bridge carries "+
				"(architect, reviewer, none)", name)
		}
	}
	if sawNone && len(out) != 0 {
		return nil, fmt.Errorf("-github-bridge-roles: %q says none and also names roles", spec)
	}
	return out, nil
}

// describeRoles renders the carried set for the operator's one line.
func describeRoles(set map[roles.Role]bool) string {
	var named []string
	if set[roles.Architect] {
		named = append(named, "architect")
	}
	if set[roles.Reviewer] {
		named = append(named, "reviewer")
	}
	if len(named) == 0 {
		return "no roles"
	}
	return strings.Join(named, "+")
}

// engineResolver is what an engine will consult for its role turns, plus the
// one line the operator reads about it.
//
// The banner travels WITH the resolver rather than being printed where the
// decision was made, so the two cannot disagree. A startup line announcing App
// transport over a runner that actually holds the operator's gh credentials is
// not a cosmetic defect: it is the machine's activity appearing under a
// person's identity, reported as though it were not.
type engineResolver struct {
	Resolver workflow.RunnerResolver
	// Banner is empty when the bridge is off.
	Banner string
	// Mailbox is the conversation actually installed, carried so startup
	// establishes THAT rather than re-deriving a box from the configuration
	// that was meant to build it. Zero value when the bridge is off.
	Mailbox ghbridge.Issue
}

// composeEngineResolver decides what serves this engine's role turns.
//
// Extracted from runControlSurface so this composition is provable without
// binding a socket. It is the whole of the decision and it has three outcomes:
//
//	bridge off        -> base, unchanged
//	bridge misconfigured -> refusal, and NOTHING installed
//	bridge configured -> ghbridge.Resolver over base
//
// The middle outcome is the one worth stating twice. A refusal returns no
// resolver at all rather than a partly-built one: half a bridge installed is a
// mailbox that cannot authenticate its sender, or a machine identity that
// quietly became a person's.
//
// COMPOSITION, not replacement. base stays reachable for every role and every
// other reviewer provider; routing anything straight to the command line here
// would bypass the control resolver and silently disable remote architect
// semantics.
func composeEngineResolver(base workflow.RunnerResolver, repoRoot, sessionID string, gh githubBridgeConfig) (engineResolver, error) {
	// Off by default. The package existing changes nothing; only configuration
	// does.
	if strings.TrimSpace(gh.MailboxPR) == "" {
		return engineResolver{Resolver: base}, nil
	}

	box := ghbridge.Issue{
		Dir:    repoRoot,
		Number: strings.TrimSpace(gh.MailboxPR),
		ExpectedReviewer: ghbridge.Principal{
			UserID: gh.ReviewerID,
			Login:  strings.TrimSpace(gh.ReviewerLogin),
		},
	}
	if !box.Valid() {
		return engineResolver{}, errors.New("the github review bridge needs a pull request number and an expected reviewer " +
			"(-github-reviewer-id, or -github-reviewer-login): a mailbox that cannot authenticate a " +
			"sender would read any parseable comment as an answer")
	}

	// Selection, completeness and refusal all live in ghbridge.AppConfig so
	// every permutation is testable rather than being a shape in main.
	api, err := gh.App.Client()
	if err != nil {
		return engineResolver{}, err
	}
	box.API = api

	// The doorbell publishes no protocol content -- one marker and one comment
	// id -- so posting it under the operator's account does not make asker and
	// answerer the same principal. The REQUEST stays App-authored, which is the
	// property reviewer independence actually rests on.
	var doorbell ghbridge.Doorbell
	if gh.Doorbell {
		doorbell = ghbridge.GHDoorbell{Dir: repoRoot, Conversation: box.Number}
	}

	// Every request this process publishes gets a durable lifetime record, and
	// every record left open by a PREVIOUS process is retracted below. A waiter
	// is a goroutine inside AwaitArchitecture, so an open record cannot have one
	// here -- which is what makes withdrawing them at startup honest (#162).
	exchanges := ghbridge.ExchangeLog{Dir: filepath.Join(repoRoot, ".sensei-code", "exchanges")}

	resolver := ghbridge.Resolver{
		Roles:     gh.Roles,
		Doorbell:  doorbell,
		Exchanges: exchanges,
		Provider:  strings.TrimSpace(gh.Provider),
		Reviewer: &ghbridge.Runner{
			Issue:        box,
			RepoDir:      repoRoot,
			Remote:       strings.TrimSpace(gh.Remote),
			NewRequestID: ghbridge.NewRequestID,
			SessionID:    sessionID,
			Wait:         gh.Wait,
		},
		Fallback: base,
	}

	// Read off the mailbox that was actually installed, never off the
	// configuration that was meant to build it.
	transport := "operator gh credentials"
	if box.API != nil {
		transport = fmt.Sprintf("github app %d installation %d (%s/%s)",
			gh.App.AppID, gh.App.InstallationID,
			strings.TrimSpace(gh.App.Owner), strings.TrimSpace(gh.App.Repo))
	}
	banner := fmt.Sprintf("github bridge: PR #%s conversation, reviewer %s, carrying provider %q for %s, mailbox via %s",
		box.Number, box.ExpectedReviewer, strings.TrimSpace(gh.Provider), describeRoles(gh.Roles), transport)

	// Retract whatever the last process left standing.
	//
	// Reported on the banner rather than swallowed: an operator reading startup
	// needs to know that requests they may have been watching are now dead, and
	// a retraction that FAILED is the case where a request is still out there
	// claiming to be live. Neither outcome fails startup -- refusing to serve
	// because an old request could not be retracted would trade a stale comment
	// for an unavailable engine.
	//
	// Its own bounded context, not the caller's: this is startup housekeeping
	// about a PREVIOUS process's requests, and it must not be able to hang the
	// engine coming up behind it.
	rctx, cancelReconcile := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelReconcile()
	if withdrawn, err := ghbridge.ReconcileAbandonedExchanges(rctx, exchanges, box, nil); err != nil {
		banner += fmt.Sprintf("\n  exchange reconciliation: %v; some earlier request may still stand", err)
	} else if withdrawn > 0 {
		banner += fmt.Sprintf("\n  withdrew %d architecture request(s) abandoned by an earlier process", withdrawn)
	}

	return engineResolver{Resolver: resolver, Banner: banner, Mailbox: box}, nil
}

// credentialFromEnvOrMint resolves the credential and reports whether the
// operator supplied it. A supplied secret is never written down and never
// printed back; only a minted one has a bootstrap step, because only a minted
// one is not already in somebody's hands.
func credentialFromEnvOrMint() (control.Credential, bool, error) {
	if supplied := strings.TrimSpace(os.Getenv(tokenEnv)); supplied != "" {
		cred, err := control.FromToken(supplied)
		return cred, true, err
	}
	cred, err := control.Mint()
	return cred, false, err
}

// controlDomain asks Sensei which repository this checkout is, and refuses to
// serve if it cannot say.
//
// Fail closed rather than falling back to a path or a git remote. The workspace
// is the identity every lease is held over, and an instance that guessed it
// would hand out roles over a repository nobody confirmed.
func controlDomain(ctx context.Context, repo gitx.Repo, cfg config.Config) (string, error) {
	client, err := sensei.Start(ctx, repo.Root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		return "", fmt.Errorf("the control surface could not reach Sensei to establish which repository it serves: %w", err)
	}
	defer client.Close()
	status, err := client.CallTool("sensei_workspace_status", map[string]any{"repo": repo.Root})
	if err != nil {
		return "", fmt.Errorf("the control surface could not establish which repository it serves: %w", err)
	}
	domain := strings.TrimSpace(sensei.RepositoryDomain(status))
	if domain == "" {
		return "", fmt.Errorf("Sensei did not name a repository domain for %s, so this instance cannot say which workspace a role would be held over", repo.Root)
	}
	return domain, nil
}

// printControlBanner shows the operator what to configure — and never the
// secret.
//
// The token reaches stdout in no case. A minted one is named by its path; a
// supplied one is not mentioned at all, because the operator already has it and
// printing it back is precisely how a secret that was handed to this process in
// an environment variable ends up in a journal.
func printControlBanner(server *control.Server, cred control.Credential, tokenAt string, supplied bool) {
	fmt.Println("Sensei Code control surface")
	fmt.Println("  workspace   ", server.Workspace())
	fmt.Println("  endpoint     http://" + server.Addr() + control.Endpoint)
	fmt.Println("  objectives   " + server.LocalAddr() + " (mode 0600, this machine only)")
	fmt.Println("  protocol     MCP " + control.SupportedProtocolVersion)
	fmt.Println("  principal   ", cred.Principal())
	if supplied {
		fmt.Println("  credential   supplied through " + tokenEnv + " and not repeated here")
	} else {
		fmt.Println("  credential   minted for this run · " + tokenAt + " (mode 0600, removed on shutdown)")
	}
	fmt.Println()
	fmt.Println("  Bound to loopback only. Expose it deliberately through a tunnel;")
	fmt.Println("  this surface will not bind a public interface.")
	fmt.Println("  The token authenticates a connection. It grants no role: a client")
	fmt.Println("  must still register for architect or reviewer and present the role")
	fmt.Println("  session it receives.")
	fmt.Println("  " + control.RoleContract)
	fmt.Println("  Place work with: sensei-code submit --task \"...\"")
	fmt.Println("  From a terminal on this machine. That channel is a local socket, not the")
	fmt.Println("  remote surface, and it refuses processes this orchestrator launched: an")
	fmt.Println("  objective is the operator's to authorize, never a worker's or the remote")
	fmt.Println("  architect's.")
	if !supplied {
		fmt.Println("  Restarting mints another credential; set " + tokenEnv)
		fmt.Println("  to a 64-character hex secret to keep one identity across restarts.")
	}
	fmt.Println()
}

// printWebhookBanner shows the operator what the webhook ingress is bound to —
// and never the secret.
//
// The secret's PATH is reported and its CONTENT is not, which is the whole of
// the distinction this slice keeps. The path is what the operator needs in
// order to read the value into GitHub's App settings form; printing the value
// itself would put it in whatever journal is capturing this process's stdout,
// where it would outlive the process by however long the log is kept.
func printWebhookBanner(webhook *ghwebhook.Server, secretPath, repository string) {
	if webhook == nil {
		return
	}
	fmt.Println("GitHub webhook ingress")
	fmt.Println("  endpoint     http://" + webhook.Addr() + ghwebhook.Endpoint)
	fmt.Println("  repository  ", repository)
	fmt.Println("  secret       " + strings.TrimSpace(secretPath) + " (path only; the value is never printed)")
	fmt.Println()
	fmt.Println("  Bound to loopback only. Publish it through the existing reverse proxy;")
	fmt.Println("  this listener will not bind a public interface.")
	fmt.Println("  A valid signature proves GitHub delivered those exact bytes. It does not")
	fmt.Println("  prove an objective was authorized, that the sender is an architect or a")
	fmt.Println("  reviewer, or that a comment should execute anything. Deliveries are")
	fmt.Println("  observed and nothing else in this slice.")
	fmt.Println()
}

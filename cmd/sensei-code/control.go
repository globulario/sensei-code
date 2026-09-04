package main

import (
	"context"
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
	"github.com/globulario/sensei-code/internal/gitx"
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

	server, err := control.New(engine, cred, control.Options{
		Addr: *addr, Workspace: domain, LeaseTTL: *ttl,
	})
	if err != nil {
		return err
	}
	// The server becomes this engine's runner resolver. Without this line the
	// rendezvous exists and nothing ever reaches it: every architect and
	// reviewer turn would take the configured command line, and a remote role
	// holder would register, inspect, and be asked nothing.
	//
	// One engine owner per orchestrated run: this process owns this engine, and
	// the resolver it consults is this process's own server.
	engine.Runners = server

	if err := server.Listen(*addr); err != nil {
		return err
	}
	defer server.Close()

	printControlBanner(server, cred, tokenAt, supplied)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	return server.Serve()
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
	if !supplied {
		fmt.Println("  Restarting mints another credential; set " + tokenEnv)
		fmt.Println("  to a 64-character hex secret to keep one identity across restarts.")
	}
	fmt.Println()
}

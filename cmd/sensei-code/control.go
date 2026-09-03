package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
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
// The lifecycle is deliberately blunt and stated rather than clever: with the
// variable set, the token — and therefore the remote principal derived from it
// — survives a restart; without it, every start mints a new one and rotation
// IS restarting. There is no token file, because a token on disk is a
// credential with a lifetime nobody decided, and no rotation endpoint, because
// a surface that can reissue its own credential is a surface that can be talked
// into reissuing its own credential.
const tokenEnv = "SENSEI_CODE_CONTROL_TOKEN"

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

	cred, err := credentialFromEnvOrMint()
	if err != nil {
		return err
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
	if err := server.Listen(*addr); err != nil {
		return err
	}
	defer server.Close()

	printControlBanner(server, cred)

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		_ = server.Close()
	}()
	return server.Serve()
}

// credentialFromEnvOrMint resolves the credential without ever writing it down.
func credentialFromEnvOrMint() (control.Credential, error) {
	if supplied := strings.TrimSpace(os.Getenv(tokenEnv)); supplied != "" {
		return control.FromToken(supplied)
	}
	return control.Mint()
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

// printControlBanner shows the operator what to configure, once, on stdout.
//
// The token is printed here and nowhere else. It does not go through the event
// bus and it is not appended to the session store: those are the durable record
// of a run, and a live credential copied into a file whose whole purpose is to
// be read later is a credential with a much longer life than anybody intended.
func printControlBanner(server *control.Server, cred control.Credential) {
	fmt.Println("Sensei Code control surface")
	fmt.Println("  workspace   ", server.Workspace())
	fmt.Println("  endpoint     http://" + server.Addr() + control.Endpoint)
	fmt.Println("  principal   ", cred.Principal())
	fmt.Println("  token        " + cred.Token())
	fmt.Println()
	fmt.Println("  Bound to loopback only. Expose it deliberately through a tunnel;")
	fmt.Println("  this surface will not bind a public interface.")
	fmt.Println("  The token authenticates a connection. It grants no role: a client")
	fmt.Println("  must still register for architect or reviewer and present the role")
	fmt.Println("  session it receives.")
	fmt.Println("  Read-only in this version: no architecture or review can be submitted,")
	fmt.Println("  and nothing here advances a task.")
	if strings.TrimSpace(os.Getenv(tokenEnv)) == "" {
		fmt.Println("  This token was minted for this run. Restarting mints another;")
		fmt.Println("  set " + tokenEnv + " to keep one identity across restarts.")
	}
	fmt.Println()
}

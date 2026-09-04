package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/doctor"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/tui"
	"github.com/globulario/sensei-code/internal/workflow"
)

func main() {
	if runVersion(os.Args[1:], os.Stdout) {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cwd, err := os.Getwd()
	fatalIf(err)
	repo, err := gitx.Discover(ctx, cwd)
	fatalIf(err)
	cfg, err := config.Load(repo.Root)
	fatalIf(err)
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "init":
			fatalIf(config.Save(repo.Root, cfg))
			fmt.Println("Sensei Code initialized at", filepath.Join(repo.Root, ".sensei-code"))
			return
		case "doctor":
			report := doctor.Run(ctx, repo.Root, cfg)
			for _, check := range report.Checks {
				detail := ""
				if strings.TrimSpace(check.Detail) != "" {
					detail = " · " + check.Detail
				}
				fmt.Printf("%-4s  %s%s\n", check.Status, check.Name, detail)
			}
			if !report.OK() {
				os.Exit(1)
			}
			return
		case "providers", "accounts":
			fatalIf(runProviders(ctx))
			return
		case "login":
			fatalIf(runLogin(ctx, os.Args[2:]))
			return
		case "logout":
			fatalIf(runLogout(ctx, os.Args[2:]))
			return
		case "setup":
			fatalIf(runSetup(ctx, repo, cfg, os.Args[2:]))
			return
		case "mcp":
			fatalIf(runMCP(repo, cfg, os.Args[2:]))
			return
		case "control":
			fatalIf(runControlSurface(ctx, repo, cfg, os.Args[2:]))
			return
		case "submit":
			fatalIf(runSubmit(repo, os.Args[2:]))
			return
		case "context":
			fatalIf(runContext(ctx, repo, cfg, os.Args[2:]))
			return
		case "handoff":
			fatalIf(runHandoff(os.Args[2:]))
			return
		case "run":
			os.Exit(runGoverned(ctx, repo, cfg, os.Args[2:]))
		case "audit-repair":
			os.Exit(runAuditRepair(ctx, repo, cfg, os.Args[2:]))
		case "observe":
			os.Exit(runObservation(ctx, repo, cfg, os.Args[2:]))
		case "routine-scan":
			fatalIf(runRoutineScan(ctx, repo, cfg, os.Args[2:]))
			return
		case "help", "--help", "-h":
			printUsage()
			return
		default:
			fmt.Fprintf(os.Stderr, "sensei-code: unknown command %q\n\n", os.Args[1])
			printUsage()
			os.Exit(2)
		}
	}
	// Continue the most recent conversation, the way a returning shell session
	// would. /clear starts a fresh one without deleting the old record.
	// A session that cannot work should say so now, with the fix, rather than
	// launching and failing the first task with a symptom that names none of
	// this. Broken means every task would fail; degraded is only a warning.
	if report := inspectQuick(ctx, repo, cfg); !report.Ready() {
		fmt.Fprintln(os.Stderr, report.Render())
		fmt.Fprint(os.Stderr, "\nThis repository is not ready. Run:\n\n    sensei-code setup --apply\n\n")
		os.Exit(1)
	}

	sessionID, resumed := session.Latest(repo.Root)
	if !resumed {
		sessionID = session.ID(time.Now())
	}
	store, err := session.New(repo.Root, sessionID)
	fatalIf(err)
	var history []event.Event
	if resumed {
		history, err = store.Load()
		if err != nil {
			fmt.Fprintln(os.Stderr, "sensei-code: could not replay the previous session:", err)
			history = nil
		}
	}
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()
	engine := workflow.New(repo, cfg, bus, store, sessionID)
	p := tea.NewProgram(tui.New(ctx, engine, events, history))
	_, err = p.Run()
	fatalIf(err)
}

func fatalIf(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code:", err)
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println(`Sensei Code

Usage:
` + renderCommands() + `
  sensei-code --version                 print the Sensei Code version

Run it with no command to launch the ChatGPT architect workspace.

Headless governed run:
  sensei-code run --task "..."      exit 0 complete · 1 failed · 3 awaiting human
                                    authority · 4 stopped · 5 timed out
  --json      emit the event stream as JSONL
  --timeout   give up after a duration, leaving the candidate in place
  --plan      JSON file holding the bounded plan (an architect decision with
              decision "proceed"); the architect is not asked for one, and the
              plan is routed, reviewed, and admitted exactly as an architect's
              would be, recorded as supplied rather than architect-produced

Read-only lanes:
  sensei-code observe --task "..."       exit 6 observed; the repository is unchanged
  sensei-code audit-repair --task "..."  observes first, then opens a SEPARATE
                                         governed task per evidence-backed finding
  --dry-run   report what WOULD become repair work, opening none

Inside the TUI:
  normal text             talk to the persistent ChatGPT architect
  /run <task>             cross into governed implementation/review
  /login                  connect ChatGPT, Codex, Claude, or Antigravity

The first-version architect is ChatGPT (GPT-5.6 Sol). Codex app-server is used as the authenticated ChatGPT transport; Codex and Claude remain bounded implementation/review providers.`)
}

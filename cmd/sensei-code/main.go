package main

import (
	"context"
	"errors"
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
		case "proposal":
			fatalIf(runProposal(repo, os.Args[2:]))
			return
		case "review":
			fatalIf(runReview(repo, os.Args[2:]))
			return
		case "context":
			fatalIf(runContext(ctx, repo, cfg, os.Args[2:]))
			return
		case "handoff":
			fatalIf(runHandoff(os.Args[2:]))
			return
		case "run":
			os.Exit(runGoverned(ctx, repo, cfg, os.Args[2:]))
		case "resume":
			os.Exit(resumeAuthorityAnswered(ctx, repo, cfg, os.Args[2:]))
		case "audit-repair":
			os.Exit(runAuditRepair(ctx, repo, cfg, os.Args[2:]))
		case "observe":
			os.Exit(runObservation(ctx, repo, cfg, os.Args[2:]))
		case "quarantine":
			fatalIf(session.QuarantineCommand(ctx, repo.Root, os.Args[2:], os.Stdout))
			return
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

	// An unreadable session store is fatal here rather than a fresh start.
	// Starting a new session inside storage that could not even be listed would
	// write this run's history where the previous one is unaccounted for, and
	// the first symptom would be a task nobody can find.
	recordID, sessionID, resumed, err := openConversation(repo.Root, time.Now())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code:", err)
		os.Exit(1)
	}
	store, err := session.New(repo.Root, recordID)
	fatalIf(err)
	// A reopened record that cannot be read -- or is gone -- is fatal, never an
	// empty conversation, and what /resume may continue is canonical discovery's.
	start, err := continueConversation(repo.Root, store, recordID, resumed)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code:", err)
		os.Exit(1)
	}
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()
	engine := workflow.New(repo, cfg, bus, store, sessionID)

	// The bridge belongs here as much as on `control`. A governed run that
	// reaches a human-owned boundary needs the surface that can ANSWER it in the
	// same process as the transport that carried the turn -- control had the
	// transport and no answer, this has the answer and had no transport, and a
	// task that escalated was therefore unfinishable from either.
	//
	// A refusal is fatal rather than a warning. Continuing would silently route
	// architect and reviewer turns to the local provider command line, which is
	// the fallback this repository configured a bridge specifically to avoid:
	// the run would look normal and the independence would be gone.
	banner, err := installGitHubBridge(engine, repo.Root, cfg.GitHubBridge)
	fatalIf(err)
	if banner != "" {
		fmt.Fprintln(os.Stderr, banner)
	}

	p := tea.NewProgram(tui.New(ctx, engine, events, start.history, start.inventory, start.discovery))
	_, err = p.Run()
	fatalIf(err)
}

// openConversation names the session record the interactive startup
// continues and the SessionID this process acts under. A new conversation is a
// new record written by this process, so the two are one. A reopened one keeps
// the holder's record -- one physical ledger -- but this process is not the one
// that wrote it, so it acts under a FRESH session minted by the same rule as
// `resume` (resumingSession, 70B2a1). A task it continues through /resume is
// bound to that task's session lineage by Engine.Resume before anything of it
// is recorded; the record's historical events keep the sessions that wrote
// them.
func openConversation(root string, now time.Time) (recordID, sessionID string, resumed bool, err error) {
	recordID, resumed, err = session.Latest(root)
	if err != nil {
		return "", "", false, err
	}
	if !resumed {
		recordID = session.ID(now)
		return recordID, recordID, false, nil
	}
	if sessionID, err = resumingSession(now); err != nil {
		return "", "", false, err
	}
	return recordID, sessionID, true, nil
}

// errConversationUnavailable is a session record the interactive startup
// selected to continue and could not read -- one that vanished after it was
// selected included. It is never an empty
// conversation: starting one would replay nothing and offer nothing to resume
// from a history nobody could open.
var errConversationUnavailable = errors.New("the session record this startup continues could not be read; " +
	"it is not replayed or resumed as an empty conversation")

// interactiveStart is what the interactive startup continues in the record
// openConversation named: the conversation that record replays, and the
// inventory /resume's tasks in it are discovered against.
type interactiveStart struct {
	history []event.Event
	// inventory is the repository's canonical discovery, in which the record
	// was established (interactiveDiscovery); the TUI takes /resume's tasks
	// from it and the replayed record (Discovery.ResumableRecord), refused
	// tasks included. discovery is that discovery's own typed refusal, when it
	// refused.
	inventory session.Discovery
	discovery error
}

// continueConversation is what the interactive startup continues in store,
// the record openConversation named. A new conversation reads nothing. A
// reopened one replays its record through loadConversation, which refuses a
// selected record it cannot read, a vanished one included, and takes what /resume may continue
// from the same canonical discovery the resume command reads
// (interactiveDiscovery), never from the raw record.
func continueConversation(root string, store *session.Store, recordID string, resumed bool) (interactiveStart, error) {
	if !resumed {
		return interactiveStart{}, nil
	}
	history, err := loadConversation(store, recordID)
	if err != nil {
		return interactiveStart{}, err
	}
	start := interactiveStart{history: history}
	start.inventory, start.discovery = interactiveDiscovery(root, recordID)
	return start, nil
}

// loadConversation is the replayed history of the record the startup
// continues, which openConversation selected as existing. A new conversation
// never reaches it (continueConversation's !resumed branch is the only empty
// startup), so EVERY failure here -- the selected record having disappeared
// before it was read included -- is errConversationUnavailable, typed, and
// never an empty history.
func loadConversation(store *session.Store, recordID string) ([]event.Event, error) {
	history, err := store.Load()
	if err != nil {
		return nil, fmt.Errorf("%w: session %s: %w", errConversationUnavailable, recordID, err)
	}
	return history, nil
}

// interactiveDiscovery is the inventory the TUI's /resume discovers the
// record it holds against: the SAME canonical discovery and refusal precedence
// the resume command reads (session.FindActive, Discovery.ResumableIn), in
// which the record is established as one this repository holds and no
// quarantine excludes. The TUI's tasks are that record's lineage members'
// (Discovery.ResumableRecord), a task that any record refuses or a quarantine
// reserves carried with that repository-wide typed refusal. A discovery that
// refuses is returned as that refusal, never as "nothing to resume".
func interactiveDiscovery(root, recordID string) (session.Discovery, error) {
	inventory, err := session.FindActive(root)
	if err != nil {
		return session.Discovery{}, err
	}
	if _, err := inventory.ResumableIn(recordID); err != nil {
		return session.Discovery{}, err
	}
	return inventory, nil
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
                                    authority · 4 stopped · 5 timed out ·
                                    8 blocked external · 9 not converged
                                    (resume --task <id> continues either)
  --json      emit the event stream as JSONL
  --timeout   give up after a duration, leaving the candidate in place
  --plan      JSON file holding the bounded plan (an architect decision with
              decision "proceed"); the architect is not asked for one, and the
              plan is routed, reviewed, and admitted exactly as an architect's
              would be, recorded as supplied rather than architect-produced

GitHub objective proposals:
  ChatGPT may post [sensei-code:objective-proposal] plus a strict JSON objective.
  The signed webhook records it as inert data only. proposal approve <comment-id>
  selects those exact stored bytes and crosses the existing local objective
  authority boundary; GitHub identity itself never authorizes work.

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

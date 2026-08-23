package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Headless governed execution.
//
// This is the SAME workflow as the TUI's /run. It calls Engine.SubmitGoverned
// and renders the event stream, and it contains no workflow logic of its own —
// deliberately, because two implementations of a governed pipeline would drift
// and the divergence would show up as a governance difference between what a
// human runs and what CI runs.
//
//	                Engine.SubmitGoverned
//	                        ▲
//	                ┌───────┴───────┐
//	            TUI /run        run --task
//
// Without this path the top-level loop can only be exercised by a person at a
// keyboard, which is why the most complex code in the product has the least
// automated coverage.

// Exit codes are distinct because the outcomes are distinct, and a caller that
// cannot tell them apart will retry the ones it should not.
const (
	exitCompleted = 0
	exitFailed    = 1
	exitUsage     = 2
	// exitAwaitingAuthority means a human-owned decision was reached with no
	// human present. Nothing failed and nothing was decided; the question is
	// preserved exactly as asked.
	exitAwaitingAuthority = 3
	exitStopped           = 4
	exitTimeout           = 5
)

func runGoverned(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	task := fs.String("task", "", "the work to carry out (required)")
	timeout := fs.Duration("timeout", 0, "give up after this long; 0 waits indefinitely")
	asJSON := fs.Bool("json", false, "emit the event stream as JSONL instead of prose")
	quiet := fs.Bool("quiet", false, "print only terminal outcomes")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*task) == "" {
		fmt.Fprintln(os.Stderr, "sensei-code run: --task is required")
		fs.Usage()
		return exitUsage
	}

	// The same readiness gate the TUI applies. A governed run that starts on a
	// broken repository fails later with a symptom that names none of this.
	if report := inspectQuick(ctx, repo, cfg); !report.Ready() {
		fmt.Fprintln(os.Stderr, report.Render())
		fmt.Fprint(os.Stderr, "\nThis repository is not ready. Run:\n\n    sensei-code setup --apply\n\n")
		return exitFailed
	}

	sessionID := session.ID(time.Now())
	store, err := session.New(repo.Root, sessionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code run:", err)
		return exitFailed
	}
	bus := event.NewBus()
	events, unsubscribe := bus.Subscribe(512)
	defer unsubscribe()

	if *timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, *timeout)
		defer cancel()
	}

	engine := workflow.New(repo, cfg, bus, store, sessionID)
	taskID := engine.SubmitGoverned(ctx, strings.TrimSpace(*task))
	if !*quiet {
		fmt.Printf("task %s  session %s\n", taskID, sessionID)
	}
	return streamUntilSettled(ctx, engine, events, taskID, *asJSON, *quiet)
}

// runControl is the part of the engine a headless run steers: it may withdraw
// attention and it may leave a question standing. It may not answer one.
//
// It is an interface so the settling logic can be tested without providers, a
// repository or a graph — the exit codes and the authority behaviour are the
// two things here that must not be wrong, and neither should need a live run
// to exercise.
type runControl interface {
	DeferAuthority(taskID string) bool
	Stop(taskID string) bool
}

// streamUntilSettled renders the run and returns when it settles.
func streamUntilSettled(ctx context.Context, engine runControl, events <-chan event.Event, taskID string, asJSON, quiet bool) int {
	enc := json.NewEncoder(os.Stdout)
	deferred := false
	for {
		select {
		case <-ctx.Done():
			// A timeout stops computation; it does not decide anything. The
			// candidate is left as it stands so the work can be resumed.
			engine.Stop(taskID)
			fmt.Fprintln(os.Stderr, "sensei-code run: timed out; the task was stopped and its candidate left in place")
			return exitTimeout
		case ev, ok := <-events:
			if !ok {
				return exitFailed
			}
			if ev.TaskID != "" && ev.TaskID != taskID {
				continue
			}
			if asJSON {
				_ = enc.Encode(ev)
			} else if !quiet || terminal(ev.Kind) {
				fmt.Println(renderEvent(ev))
			}

			if ev.Kind == event.AuthorityRequired {
				// A human-owned decision with no human present. Deferring
				// preserves the question exactly as asked; answering it here
				// would satisfy an authority boundary nobody was asked about.
				if engine.DeferAuthority(taskID) {
					deferred = true
					fmt.Fprintln(os.Stderr, "sensei-code run: a human-owned decision was reached and no human is present; the question is preserved, not answered")
				}
				continue
			}

			switch ev.Kind {
			case event.WorkflowCompleted:
				return exitCompleted
			case event.WorkflowFailed:
				return exitFailed
			case event.WorkflowStopped:
				if deferred {
					return exitAwaitingAuthority
				}
				return exitStopped
			case event.WorkflowAwaitingAuthority:
				return exitAwaitingAuthority
			}
		}
	}
}

func terminal(k event.Kind) bool {
	switch k {
	case event.WorkflowCompleted, event.WorkflowFailed, event.WorkflowStopped,
		event.WorkflowAwaitingAuthority, event.AuthorityRequired:
		return true
	}
	return false
}

// renderEvent is a plain-text projection of one event. It carries no styling:
// a log a machine reads should not need an ANSI parser, and a log a human
// greps should not contain escape sequences.
func renderEvent(ev event.Event) string {
	stamp := ev.Time.UTC().Format("15:04:05")
	line := fmt.Sprintf("%s  %-12s %-28s", stamp, ev.Source, ev.Kind)
	if s := strings.TrimSpace(ev.Summary); s != "" {
		line += "  " + strings.ReplaceAll(s, "\n", "\n"+strings.Repeat(" ", 12))
	}
	return strings.TrimRight(line, " ")
}

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
	// exitObserved means a read-only run finished by reporting findings.
	//
	// Distinct from exitCompleted, which means a change was admitted. An audit
	// that finds three real defects and admits nothing has succeeded, and a
	// caller that cannot tell the two apart will either treat every audit as a
	// no-op or go looking for a change that was never supposed to exist.
	exitObserved = 6
)

func runGoverned(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) int {
	return runHeadless(ctx, repo, cfg, args, "run", false)
}

// runObservation is `sensei-code observe`: read the repository, report what was
// found, admit nothing.
//
// A separate entrypoint rather than a flag on run, because the lane has to be
// structural. A flag is a claim the caller makes; which command was invoked is
// a fact about how the process was started, and the engine fixes the lane from
// it at submission time where nothing downstream can widen it.
func runObservation(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) int {
	return runHeadless(ctx, repo, cfg, args, "observe", true)
}

func runHeadless(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string, name string, observe bool) int {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	task := fs.String("task", "", "the work to carry out (required)")
	var planPath *string
	if !observe {
		planPath = fs.String("plan", "", "JSON file holding the bounded plan to carry out, instead of asking the architect for one")
	}
	timeout := fs.Duration("timeout", 0, "give up after this long; 0 waits indefinitely")
	asJSON := fs.Bool("json", false, "emit the event stream as JSONL instead of prose")
	quiet := fs.Bool("quiet", false, "print only terminal outcomes")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*task) == "" {
		fmt.Fprintln(os.Stderr, "sensei-code "+name+": --task is required")
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
		fmt.Fprintln(os.Stderr, "sensei-code "+name+":", err)
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
	// Nobody typed this. `RequestedByHuman` used to be stamped here because it
	// was set from which entrypoint ran, not from anything establishing a
	// person was present -- and in a dogfooding run an AI submitted a task the
	// engine then recorded as the human's.
	submit := engine.SubmitGovernedUnattended
	if observe {
		submit = engine.SubmitObservation
	}
	if planPath != nil && strings.TrimSpace(*planPath) != "" {
		// The plan is read and validated here, in full, before the task
		// exists. Its provenance is then the engine's to stamp: the file
		// supplies a bound and nothing about who is entitled to it.
		plan, err := loadSuppliedPlan(*planPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "sensei-code "+name+":", err)
			return exitUsage
		}
		submit = func(ctx context.Context, task string) string {
			return engine.SubmitGovernedWithPlan(ctx, task, plan)
		}
	}
	taskID := submit(ctx, strings.TrimSpace(*task))
	if !*quiet {
		fmt.Printf("task %s  session %s\n", taskID, sessionID)
	}
	return streamUntilSettled(ctx, engine, events, taskID, *asJSON, *quiet, *timeout)
}

// loadSuppliedPlan reads a plan file and validates it as a bound. Anything the
// validation refuses is a usage error: no task is created for a plan the run
// would not accept.
func loadSuppliedPlan(path string) (workflow.SuppliedPlan, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return workflow.SuppliedPlan{}, fmt.Errorf("--plan: %w", err)
	}
	plan, err := workflow.ParseSuppliedPlan(raw)
	if err != nil {
		return workflow.SuppliedPlan{}, fmt.Errorf("--plan %s: %w", path, err)
	}
	return plan, nil
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
	TimeOut(taskID, budget string) bool
}

// terminalGrace bounds how long an invocation waits for its own account. It is
// a variable so a test can shrink it; production never changes it.
var terminalGrace = 15 * time.Second

// drainUntilTerminal keeps rendering until the engine emits the terminal it
// owes, or a grace window closes.
//
// The grace is bounded because a hung engine must not hold the process open,
// and it is not zero because an invocation that ends without its own account
// forces every later reader back into the event stream.
func drainUntilTerminal(events <-chan event.Event, taskID string, enc *json.Encoder, asJSON, quiet bool, code int) int {
	grace := time.NewTimer(terminalGrace)
	defer grace.Stop()
	for {
		select {
		case <-grace.C:
			fmt.Fprintln(os.Stderr, "sensei-code run: the engine did not account for this invocation within the grace window")
			return code
		case ev, ok := <-events:
			if !ok {
				return code
			}
			if ev.TaskID != "" && ev.TaskID != taskID {
				continue
			}
			if asJSON {
				_ = enc.Encode(ev)
			} else if !quiet || terminal(ev.Kind) {
				fmt.Println(renderEvent(ev))
			}
			if invocationFinal(ev.Kind) {
				return code
			}
		}
	}
}

// streamUntilSettled renders the run and returns when it settles.
func streamUntilSettled(ctx context.Context, engine runControl, events <-chan event.Event, taskID string, asJSON, quiet bool, budget time.Duration) int {
	enc := json.NewEncoder(os.Stdout)
	deferred := false
	for {
		select {
		case <-ctx.Done():
			// A timeout stops computation; it does not decide anything. The
			// candidate is left as it stands so the work can be resumed.
			//
			// It does NOT exit here. Returning the moment the deadline fired
			// ended the process before the engine could terminalize, so a
			// timed-out invocation produced no terminal event and no receipt,
			// and the only account of it was the event stream -- the
			// reconstruction the receipt exists to abolish. The invocation
			// waits, briefly and boundedly, for its own account.
			spent := budget.String()
			if budget <= 0 {
				spent = "no deadline was configured; the context ended for another reason"
			}
			engine.TimeOut(taskID, spent)
			fmt.Fprintln(os.Stderr, "sensei-code run: timed out; the task was stopped and its candidate left in place")
			return drainUntilTerminal(events, taskID, enc, asJSON, quiet, exitTimeout)
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
			case event.WorkflowObserved:
				return exitObserved
			case event.WorkflowFailed:
				return exitFailed
			case event.WorkflowTimedOut:
				// The engine terminalized a deadline itself. Adding a terminal
				// kind without teaching every consumer that reads terminals is
				// how the drain ended up waiting a full grace window for an
				// event it had already been handed.
				return exitTimeout
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

// invocationFinal is the set that ENDS an invocation.
//
// It is deliberately narrower than terminal(): AuthorityRequired is an
// interruption boundary, not an ending. In the ordinary stream it triggers
// DeferAuthority and the engine then emits the real terminal afterwards. A
// drain that treated it as final could return on a buffered AuthorityRequired
// and exit before WorkflowTimedOut and its receipt ever arrived -- reopening
// the accounting hole this drain exists to close.
func invocationFinal(k event.Kind) bool {
	switch k {
	case event.WorkflowCompleted, event.WorkflowFailed, event.WorkflowStopped,
		event.WorkflowTimedOut, event.WorkflowObserved, event.WorkflowAwaitingAuthority:
		return true
	}
	return false
}

// terminal is the RENDER-worthy set: what a quiet run still prints, including
// the interruption boundary a human needs to see.
func terminal(k event.Kind) bool {
	switch k {
	case event.WorkflowCompleted, event.WorkflowFailed, event.WorkflowStopped,
		event.WorkflowTimedOut, event.WorkflowObserved,
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

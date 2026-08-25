package main

// `sensei-code audit-repair` — observe, then open governed repair work for what
// the observation actually established.
//
// A separate command rather than a flag on observe, for the same reason the
// observation lane is a separate command: which command was invoked is a fact
// about how the process was started, and a flag would be a claim the caller
// makes. Choosing this command IS the authorization to open repair work. It
// authorizes nothing else -- each repair is an ordinary governed task and every
// gate below still applies to it.
//
// What this does NOT do is let the observation mutate. The observation runs to
// its own terminal state first, discards its workspace, and only then is a
// SEPARATE task submitted, carrying the finding as evidence and no authority.

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/finding"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

func runAuditRepair(ctx context.Context, repo gitx.Repo, cfg config.Config, args []string) int {
	fs := flag.NewFlagSet("audit-repair", flag.ExitOnError)
	task := fs.String("task", "", "what to audit (required)")
	timeout := fs.Duration("timeout", 0, "give up after this long; 0 waits indefinitely")
	maxRepairs := fs.Int("max-repairs", 1, "how many eligible findings to open repair work for")
	dryRun := fs.Bool("dry-run", false, "observe and report what WOULD become repair work, opening none")
	quiet := fs.Bool("quiet", false, "print only terminal outcomes")
	if err := fs.Parse(args); err != nil {
		return exitUsage
	}
	if strings.TrimSpace(*task) == "" {
		fmt.Fprintln(os.Stderr, "sensei-code audit-repair: --task is required")
		fs.Usage()
		return exitUsage
	}
	// A non-positive limit is a caller error, not "audit and repair nothing".
	// Left to fall through, the loop broke on the first finding and returned a
	// completed status for a run that repaired nothing -- the caller would read
	// success where no work was even attempted. Use observe for an audit that
	// opens no repairs.
	if *maxRepairs < 1 {
		fmt.Fprintf(os.Stderr, "sensei-code audit-repair: --max-repairs must be at least 1 (got %d); "+
			"use `sensei-code observe` to audit without opening repair work\n", *maxRepairs)
		return exitUsage
	}
	if report := inspectQuick(ctx, repo, cfg); !report.Ready() {
		fmt.Fprintln(os.Stderr, report.Render())
		return exitFailed
	}

	sessionID := session.ID(time.Now())
	store, err := session.New(repo.Root, sessionID)
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code audit-repair:", err)
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

	// Phase 1: observation, to its own terminal state.
	observation := engine.SubmitObservation(ctx, strings.TrimSpace(*task))
	fmt.Printf("observation %s  session %s\n", observation, sessionID)
	if code := streamUntilSettled(ctx, engine, events, observation, false, *quiet); code != exitObserved {
		fmt.Fprintf(os.Stderr, "sensei-code audit-repair: the audit did not complete (exit %d); opening no repair work\n", code)
		return code
	}

	// Phase 2: decide what may become work. Eligibility is the finding's own
	// provenance, not this command's intent.
	all := engine.Findings(observation)
	// Deduplicate by the finding's stable id. An observation that states the
	// same thing twice -- easy for a model, and likelier the more thorough the
	// audit -- would otherwise open the same repair twice, and the second task
	// would run against a world the first had already changed.
	var eligible []finding.Finding
	seen := map[string]bool{}
	for _, f := range all {
		if !f.Eligible() || seen[f.ID] {
			continue
		}
		seen[f.ID] = true
		eligible = append(eligible, f)
	}
	fmt.Printf("\n%d finding(s); %d eligible to become repair work\n", len(all), len(eligible))
	for _, f := range all {
		mark := "  skip"
		if f.Eligible() {
			mark = "REPAIR"
		}
		fmt.Printf("  [%s] %-12s %s  (%s)\n", mark, f.Source, truncate(f.Statement, 96), strings.Join(f.Files, ", "))
	}
	if len(eligible) == 0 {
		fmt.Println("\nnothing evidence-backed to repair")
		return exitObserved
	}
	if *dryRun {
		fmt.Println("\ndry run: no repair work opened")
		return exitObserved
	}

	// Phase 3: a SEPARATE governed task per finding. Ordinary provenance,
	// ordinary routing, ordinary everything.
	worst := exitCompleted
	for i, f := range eligible {
		if i >= *maxRepairs {
			fmt.Printf("\n%d further eligible finding(s) not opened (--max-repairs=%d)\n", len(eligible)-i, *maxRepairs)
			break
		}
		fmt.Printf("\n=== repair %s from %s\n", f.ID, f.ObservationTask)
		repair := engine.SubmitGovernedUnattended(ctx, f.RepairObjective())
		fmt.Printf("repair task %s\n", repair)
		if code := streamUntilSettled(ctx, engine, events, repair, false, *quiet); code != exitCompleted {
			worst = code
		}
	}
	return worst
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

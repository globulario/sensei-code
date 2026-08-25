package main

// proofbench — measurement infrastructure for the proof campaign.
//
//	proofbench validate  --manifest benchmark/proof-v1/manifest.json
//	proofbench calibrate --manifest ... --session ... --repair <commit>
//	proofbench run       --manifest ... [--task ID] [--arm RAW|COLD|WARM]
//	proofbench report    --manifest ...
//
// A separate binary from sensei-code on purpose. This measures the product; it
// is not part of it, and nothing here is reachable from a governed run.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/proofbench"
)

const (
	exitOK      = 0
	exitFailed  = 1
	exitUsage   = 2
	exitNotGood = 3 // the campaign ran and the verdict is not GREEN
)

func main() { os.Exit(run(os.Args[1:])) }

func run(args []string) int {
	if len(args) == 0 {
		usage()
		return exitUsage
	}
	switch args[0] {
	case "validate":
		return validate(args[1:])
	case "calibrate":
		return calibrate(args[1:])
	case "run":
		return runCampaign(args[1:])
	case "report":
		return report(args[1:])
	case "help", "-h", "--help":
		usage()
		return exitOK
	}
	fmt.Fprintf(os.Stderr, "proofbench: unknown command %q\n\n", args[0])
	usage()
	return exitUsage
}

func usage() {
	fmt.Println(`proofbench — measurement infrastructure for the Sensei-code proof campaign

  proofbench validate  --manifest <path>
        refuse a corpus that cannot support the pre-registered gates,
        before it costs provider budget

  proofbench calibrate --manifest <path> --session <events.jsonl>
                       --repair <commit> [--out <dir>]
        prove the instrument can record the known #62 failure and the
        known #79/#80 win

  proofbench run       --manifest <path> [--task <id>] [--arm RAW|COLD|WARM]
                       [--attempt N] [--dry-run]
        execute arms in isolation and append to the ledger

  proofbench report    --manifest <path> [--out <dir>]
        derive GREEN/AMBER/RED mechanically from the committed records

Exit: 0 ok · 1 failed · 2 usage · 3 verdict is not GREEN`)
}

// dirOf is the benchmark directory a manifest lives in. Runs and reports sit
// beside the manifest so evidence and experiment travel together.
func dirOf(manifest string) string { return filepath.Dir(manifest) }

func validate(args []string) int {
	fs := flag.NewFlagSet("validate", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to the frozen manifest (required)")
	if err := fs.Parse(args); err != nil || strings.TrimSpace(*manifest) == "" {
		fs.Usage()
		return exitUsage
	}
	m, hash, err := proofbench.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench validate:", err)
		return exitFailed
	}
	fmt.Printf("manifest  %s\nversion   %s\nhash      %s\n", *manifest, m.Version, hash)
	fmt.Printf("corpus    %d primary task(s), %d linked, %d calibration specimen(s)\n",
		len(m.Tasks), m.LinkedTasks(), len(m.Calibration))
	fmt.Printf("rule      %s\n", m.SelectionRule)
	if n := len(m.Excluded); n > 0 {
		fmt.Printf("excluded  %d candidate(s), each with a reason\n", n)
	}
	fmt.Println("\nsound.")
	return exitOK
}

func calibrate(args []string) int {
	fs := flag.NewFlagSet("calibrate", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to the frozen manifest (required)")
	session := fs.String("session", "", "recorded events.jsonl holding the #62 specimen")
	task := fs.String("task", "", "task id of the non-convergence specimen (default: most eventful)")
	repair := fs.String("repair", "", "commit of the landed #79/#80 self-repair")
	out := fs.String("out", "", "directory to write calibration.json into (default: beside the manifest)")
	if err := fs.Parse(args); err != nil || strings.TrimSpace(*manifest) == "" {
		fs.Usage()
		return exitUsage
	}
	_, _, err := proofbench.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench calibrate:", err)
		return exitFailed
	}
	repoRoot, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench calibrate:", err)
		return exitFailed
	}

	var results []proofbench.CalibrationResult

	// The negative: #62, from its recorded stream.
	if s := strings.TrimSpace(*session); s != "" {
		events, err := proofbench.LoadSession(s)
		if err != nil {
			fmt.Fprintln(os.Stderr, "proofbench calibrate:", err)
			return exitFailed
		}
		id := strings.TrimSpace(*task)
		if id == "" {
			if ids := proofbench.SessionTasks(events); len(ids) > 0 {
				id = ids[0]
			}
		}
		ev := proofbench.MeasureConvergence(events, id)
		results = append(results, proofbench.CalibrateNonConvergence(ev))
		b, _ := json.MarshalIndent(ev, "", "  ")
		fmt.Printf("negative specimen (%s):\n%s\n\n", id, b)
	} else {
		results = append(results, proofbench.CalibrationResult{
			Task: "non-convergence-62", Expected: "negative / non_convergence",
			Observed: "not attempted", OK: false,
			Detail: "no --session supplied; the instrument was not shown able to record the known failure"})
	}

	// The positive: #79/#80, from what survives of it.
	if c := strings.TrimSpace(*repair); c != "" {
		ev, err := proofbench.MeasureSelfRepair(repoRoot, c)
		if err != nil {
			fmt.Fprintln(os.Stderr, "proofbench calibrate:", err)
			return exitFailed
		}
		ev.StreamRetained = false
		ev.Note = "the run's own event stream was not retained: governed runs execute in candidate " +
			"worktrees whose session stores are discarded with them, so this rests on the landed " +
			"artifact and the PR record rather than on a replayed stream"
		results = append(results, proofbench.CalibratePositive(ev))
		b, _ := json.MarshalIndent(ev, "", "  ")
		fmt.Printf("positive specimen:\n%s\n\n", b)
	} else {
		results = append(results, proofbench.CalibrationResult{
			Task: "self-repair-79-80", Expected: "positive", Observed: "not attempted", OK: false,
			Detail: "no --repair commit supplied"})
	}

	dir := *out
	if dir == "" {
		dir = dirOf(*manifest)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	b, _ := json.MarshalIndent(results, "", "  ")
	path := filepath.Join(dir, "calibration.json")
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	ok := true
	for _, r := range results {
		mark := "REPRODUCED"
		if !r.OK {
			mark, ok = "NOT REPRODUCED", false
		}
		fmt.Printf("%-16s %-28s %s\n     %s\n", mark, r.Task, r.Expected, r.Detail)
	}
	fmt.Println("\nwrote", path)
	if !ok {
		fmt.Fprintln(os.Stderr, "\nThe instrument did not reproduce both known shapes. Gate R6 is a "+
			"pre-registered RED condition, and primary numbers taken with an unvalidated instrument "+
			"do not mean what they appear to mean.")
		return exitNotGood
	}
	return exitOK
}

func report(args []string) int {
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to the frozen manifest (required)")
	out := fs.String("out", "", "directory for report.json (default: beside the manifest)")
	md := fs.String("markdown", "", "path for the human-readable report")
	if err := fs.Parse(args); err != nil || strings.TrimSpace(*manifest) == "" {
		fs.Usage()
		return exitUsage
	}
	m, hash, err := proofbench.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench report:", err)
		return exitFailed
	}
	dir := *out
	if dir == "" {
		dir = dirOf(*manifest)
	}
	ledger, err := proofbench.OpenLedger(filepath.Join(dir, "runs"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench report:", err)
		return exitFailed
	}
	var calib []proofbench.CalibrationResult
	if b, err := os.ReadFile(filepath.Join(dir, "calibration.json")); err == nil {
		_ = json.Unmarshal(b, &calib)
	}

	rep := proofbench.Build(m, hash, ledger, calib)
	j, err := rep.JSON()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	if err := os.WriteFile(filepath.Join(dir, "report.json"), append(j, '\n'), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return exitFailed
	}
	text := rep.Markdown()
	if p := strings.TrimSpace(*md); p != "" {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFailed
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return exitFailed
		}
		fmt.Println("wrote", p)
	}
	fmt.Println(text)
	if rep.Verdict != proofbench.Green {
		return exitNotGood
	}
	return exitOK
}

func runCampaign(args []string) int {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	manifest := fs.String("manifest", "", "path to the frozen manifest (required)")
	only := fs.String("task", "", "run one task")
	arm := fs.String("arm", "", "run one arm")
	attempt := fs.Int("attempt", 1, "attempt number; a retry must increment it")
	dry := fs.Bool("dry-run", false, "prepare and isolate every arm, run no provider, record nothing")
	timeout := fs.Duration("timeout", 45*time.Minute, "per-arm timeout")
	if err := fs.Parse(args); err != nil || strings.TrimSpace(*manifest) == "" {
		fs.Usage()
		return exitUsage
	}
	m, hash, err := proofbench.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench run:", err)
		return exitFailed
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench run:", err)
		return exitFailed
	}
	dir := dirOf(*manifest)
	ledger, err := proofbench.OpenLedger(filepath.Join(dir, "runs"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench run:", err)
		return exitFailed
	}
	runner := proofbench.Runner{
		RepoRoot:   root,
		WorkDir:    filepath.Join(os.TempDir(), "proofbench", m.Version),
		Binary:     binaryPath(root),
		RawCommand: rawCommand(),
		Now:        time.Now,
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	failures := 0
	for _, t := range m.Tasks {
		if *only != "" && t.ID != *only {
			continue
		}
		for _, a := range proofbench.ExecutionOrder(t.ID) {
			if *arm != "" && string(a) != *arm {
				continue
			}
			if err := executeArm(ctx, runner, ledger, m, hash, *manifest, t, a, *attempt, *dry, *timeout); err != nil {
				fmt.Fprintf(os.Stderr, "%s/%s: %v\n", t.ID, a, err)
				failures++
			}
		}
	}
	if failures != 0 {
		return exitFailed
	}
	return exitOK
}

// executeArm prepares isolation, runs the arm, judges it, and appends.
//
// Isolation is checked before the provider is invoked and the arm is refused
// rather than warned about: a contaminated run reported as a result is worse
// than no result at all.
func executeArm(ctx context.Context, r proofbench.Runner, l *proofbench.Ledger,
	m proofbench.Manifest, hash, manifestPath string, t proofbench.Task, arm proofbench.Arm,
	attempt int, dry bool, timeout time.Duration) error {

	plan := proofbench.Plan{Task: t, Arm: arm, Attempt: attempt, ManifestHash: hash, Benchmark: m.Version}
	if err := proofbench.CheckPromptIsolation(t.Statement, t); err != nil {
		return err
	}
	dir, err := r.Prepare(ctx, plan)
	if err != nil {
		return err
	}
	defer func() { _ = r.Cleanup(context.Background(), dir) }()

	fmt.Printf("%s / %s / attempt %d\n  worktree %s\n  base     %s\n", t.ID, arm, attempt, dir, t.BaseSHA)
	if dry {
		fmt.Println("  dry run: isolation verified, no provider invoked, nothing recorded")
		return nil
	}

	runBase := r.RunBase(ctx, dir)
	governedBefore := r.GovernedState(ctx)
	// The boundary reading is only meaningful if the governed checkout is
	// quiescent. A dirty tree at arm start means a later difference cannot be
	// attributed to the arm, so the reading is recorded as unmeasurable rather
	// than as a violation.
	boundaryMeasurable := strings.TrimSpace(governedBefore) == ""
	if !boundaryMeasurable {
		fmt.Println("  note     governed checkout is not quiescent; the boundary reading for this " +
			"arm will be recorded as unmeasurable rather than as clean or violated")
	}
	started := time.Now()
	var out proofbench.ArmOutcome
	if arm == proofbench.ArmRaw {
		out = r.ExecuteRaw(ctx, dir, t.Statement, timeout)
	} else {
		out = r.ExecuteGoverned(ctx, dir, t.Statement, timeout)
	}
	ended := time.Now()

	ev := r.Evidence(ctx, dir, governedBefore, boundaryMeasurable)
	verdict := proofbench.OracleResult{Verdict: proofbench.NoResult, Detail: out.Infrastructure}
	if out.Infrastructure == "" {
		verdict = r.Judge(ctx, dir, t)
	}

	a := proofbench.Attempt{
		Task: t.ID, Arm: arm, Number: attempt, ManifestHash: hash, Benchmark: m.Version,
		BaseSHA: t.BaseSHA, RunBase: runBase, Providers: providerIdentity(),
		Started: started.UTC().Format(time.RFC3339), Ended: ended.UTC().Format(time.RFC3339),
		WallSecs: int(ended.Sub(started).Seconds()),
		Terminal: out.Terminal, Verdict: verdict.Verdict, OracleDetail: verdict.Detail,
		Interventions: out.Interventions, Objections: out.Objections,
		ReviewCycles: out.ReviewCycles, Observations: out.Observations,
		DiffHash: ev.DiffHash, GovernedCheckoutClean: ev.GovernedClean,
		GovernedCheckoutState: ev.GovernedState, BoundaryMeasurable: ev.Measurable,
		Infrastructure: out.Infrastructure,
		Artifacts:      map[string]string{"transcript_tail_sha256": proofbench.HashBytes([]byte(out.Raw))},
		// filled below once the transcript is on disk
		Notes: fmt.Sprintf("%d event(s) observed", out.Events),
	}
	// Cost is left nil unless a provider reported it. Zero is a measurement;
	// nil is the absence of one.
	// The transcript is written beside the record, so the hash above binds to
	// something a reader can actually re-hash.
	tdir := filepath.Join(dirOf(manifestPath), "transcripts", t.ID, string(arm))
	if err := os.MkdirAll(tdir, 0o755); err == nil {
		tpath := filepath.Join(tdir, fmt.Sprintf("%d.log", attempt))
		if err := os.WriteFile(tpath, []byte(out.Raw), 0o644); err == nil {
			a.Artifacts["transcript"] = tpath
		}
	}
	if err := l.Append(a); err != nil {
		return err
	}
	fmt.Printf("  terminal %s\n  oracle   %s\n  wall     %ds\n", a.Terminal, a.Verdict, a.WallSecs)
	if a.Infrastructure != "" {
		fmt.Printf("  infra    %s\n", a.Infrastructure)
	}
	return nil
}

// providerIdentity records which provider/model played which role.
//
// Read from the environment the campaign was launched with, so a change between
// arms is visible in the record and invalidates the pair rather than being
// silently combined.
func providerIdentity() map[string]string {
	get := func(k, dflt string) string {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			return v
		}
		return dflt
	}
	return map[string]string{
		"architect":   get("PROOFBENCH_ARCHITECT", "chatgpt/unrecorded"),
		"implementor": get("PROOFBENCH_IMPLEMENTOR", "codex+claude/unrecorded"),
		"reviewer":    get("PROOFBENCH_REVIEWER", "independent/unrecorded"),
		"raw":         get("PROOFBENCH_RAW", "unrecorded"),
	}
}

func repoRoot() (string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("not a git repository: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// rawCommand is the ordinary coding agent the RAW baseline drives.
//
// Configured rather than hardcoded, and absent by default: an unconfigured RAW
// arm records NO_RESULT with the reason instead of quietly comparing the
// governed arms against nothing.
func rawCommand() []string {
	if v := strings.TrimSpace(os.Getenv("PROOFBENCH_RAW_CMD")); v != "" {
		return strings.Fields(v)
	}
	return nil
}

func binaryPath(root string) string {
	if b := strings.TrimSpace(os.Getenv("SENSEI_CODE_BIN")); b != "" {
		return b
	}
	return filepath.Join(root, "sensei-code")
}

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
	case "discriminate":
		return discriminate(args[1:])
	case "preflight-candidate":
		return preflightCandidate(args[1:])
	case "environment":
		return environment(args[1:])
	case "capacity":
		return capacity(args[1:])
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

  proofbench discriminate --manifest <path> [--task <id>]
        gate each task's contract oracle against REFERENCE / WRONG /
        ALTERNATE specimens. A task whose ALTERNATE fails is refused:
        that oracle has memorised one answer, not learned the contract

  proofbench preflight-candidate --manifest <path> --task <id>
                       --candidate <dir> [--want CORRECT]
        point the frozen oracle at an existing candidate worktree and
        prove the evaluator finds work where a governed run leaves it,
        without spending a provider token

  proofbench capacity --manifest <path> [--arms <n>] [--per-arm <fraction>]
        refuse a campaign that cannot FINISH. Reads the provider's own
        rate-limit windows and projects every remaining arm against the
        tightest one. "Can one arm start?" is not the question: the
        halted REPAIR_VERIFICATION wave passed that gate on a five-hour
        window that had just reset, while the seven-day window it would
        actually exhaust sat at 96%

  proofbench environment --manifest <path> [--pin] [--against <file>]
        establish, without spending a provider token, which graph the
        governed child process actually reaches, and refuse a wave whose
        authority is DEV, unusable or drifted

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

	proofbench.SetCorpusRoot(dir)
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
	// The instrument is frozen for the life of the campaign.
	//
	// Repairing the harness mid-wave and continuing would leave arm 1 measured
	// by one ruler and arms 2..N by another. The lock makes that impossible to
	// do quietly: a changed build stops matching, and the campaign must be
	// voided and restarted rather than continued.
	if !*dry {
		lock := proofbench.CurrentLock(hash, len(m.Tasks)*len(proofbench.ExecutionOrder("")), "")
		if err := proofbench.WriteLock(filepath.Join(dir, "harness.lock.json"), lock); err != nil {
			fmt.Fprintln(os.Stderr, "proofbench run:", err)
			return exitFailed
		}
	}
	ledger, err := proofbench.OpenLedger(filepath.Join(dir, "runs"))
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench run:", err)
		return exitFailed
	}
	runner := proofbench.Runner{
		RepoRoot:   root,
		WorkDir:    filepath.Join(os.TempDir(), "proofbench", m.Version),
		Binary:     binaryPath(root),
		CorpusRoot: dir,
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

	// Refuse before spending provider time, not after.
	//
	// The ledger's append-only rule already refuses a duplicate id, but it does
	// so once the arm has RUN. A governed arm is 22 minutes and a RAW arm is
	// five, and the campaign burned both learning an id was taken. A rule that
	// protects evidence should not also waste the budget collecting it.
	if l.Has(t.ID, arm, attempt) {
		fmt.Printf("%s / %s / attempt %d is already recorded; skipping. A retry is attempt %d, and "+
			"only an infrastructure failure licenses one.\n", t.ID, arm, attempt, l.NextAttempt(t.ID, arm))
		return nil
	}
	plan := proofbench.Plan{Task: t, Arm: arm, Attempt: attempt, ManifestHash: hash, Benchmark: m.Version}
	if err := proofbench.CheckPromptIsolation(t.Statement, t); err != nil {
		return err
	}
	dir, err := r.Prepare(ctx, plan)
	if err != nil {
		return err
	}
	// The arm checkout is removed after the candidate has been resolved and
	// judged, never before: the candidate's registration lives in the arm's git
	// metadata.
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
	// Read the provider's own remaining capacity either side of the arm. This
	// is how a campaign learns what an arm actually costs, which is the number
	// AdmitCampaign needs and which nothing before this measured.
	quotaBefore := readQuotaOrNil(ctx, r)
	started := time.Now()
	var out proofbench.ArmOutcome
	if arm == proofbench.ArmRaw {
		out = r.ExecuteRaw(ctx, dir, t.Statement, timeout)
	} else {
		out = r.ExecuteGoverned(ctx, dir, t.Statement, timeout)
	}
	ended := time.Now()
	quotaAfter := readQuotaOrNil(ctx, r)

	// Resolve the tree this arm's work is actually in, BEFORE cleanup: a
	// governed candidate is registered in the arm checkout's git metadata, and
	// that registration disappears with the checkout.
	//
	// A candidate that cannot be resolved is a measurement-integrity failure,
	// not a zero. proof-v5 scored eleven governed arms on a directory that
	// never received their work, and one of those recorded INCORRECT candidates
	// passes the frozen oracle when the oracle is pointed at the real tree.
	// Only a run that reached a successful ending is expected to have produced
	// a candidate; a refusal or an outage legitimately has none.
	delivered := out.Terminal == "workflow.completed" || out.Terminal == "raw.completed" ||
		out.Terminal == "retained" || out.Terminal == "accepted"
	cand, cerr := r.ResolveCandidate(ctx, dir, arm, out.Raw, delivered)
	if cerr != nil {
		return fmt.Errorf("%s / %s: %w", t.ID, arm, cerr)
	}
	if cand.Dir != "" {
		fmt.Printf("  candidate %s (%s)\n", cand.Dir, cand.Method)
	} else {
		fmt.Printf("  candidate none created (%s)\n", out.Terminal)
	}

	ev := r.Evidence(ctx, dir, governedBefore, boundaryMeasurable)
	patch := ""
	if cand.Dir != "" {
		patch = proofbench.CandidateDiff(ctx, cand, t.BaseSHA)
		ev.DiffHash = proofbench.HashBytes([]byte(patch))
	}
	verdict := proofbench.OracleResult{Verdict: proofbench.NoResult, Detail: out.Infrastructure}
	switch {
	case out.Infrastructure != "":
	case cand.Dir == "":
		verdict = proofbench.OracleResult{Verdict: proofbench.NoResult,
			Detail: "the run produced no candidate to judge: " + out.Terminal}
	default:
		verdict = r.Judge(ctx, cand.Dir, t)
	}

	a := proofbench.Attempt{
		Task: t.ID, Arm: arm, Number: attempt, ManifestHash: hash, Benchmark: m.Version,
		BaseSHA: t.BaseSHA, RunBase: runBase, Providers: providerIdentity(),
		Started: started.UTC().Format(time.RFC3339), Ended: ended.UTC().Format(time.RFC3339),
		WallSecs: int(ended.Sub(started).Seconds()),
		Terminal: out.Terminal, Verdict: verdict.Verdict, OracleDetail: verdict.Detail,
		Interventions: out.Interventions, Objections: out.Objections,
		ReviewCycles: out.ReviewCycles, Observations: out.Observations,
		DiffHash: ev.DiffHash, CandidateDir: cand.Dir, CandidateMethod: cand.Method,
		CandidateHead:         cand.HeadSHA,
		GovernedCheckoutClean: ev.GovernedClean,
		GovernedCheckoutState: ev.GovernedState, BoundaryMeasurable: ev.Measurable,
		Infrastructure:     out.Infrastructure,
		InfrastructureHint: out.InfrastructureHint,
		Classifier:         out.Classifier,
		TerminalSource:     string(out.TerminalSource),
		HarnessVersion:     proofbench.HarnessVersion,
		ClassifierVersion:  proofbench.ClassifierVersion,
		RawBytes:           out.RawBytes,
		RawSHA256:          out.RawSHA256,
		QuotaBefore:        quotaBefore,
		QuotaAfter:         quotaAfter,
		RefusalClaims:      out.RefusalClaims,
		Artifacts:          map[string]string{"transcript_sha256": out.RawSHA256},
		// filled below once the transcript is on disk
		Notes: fmt.Sprintf("%d event(s) observed", out.Events),
	}
	// Cost is left nil unless a provider reported it. Zero is a measurement;
	// nil is the absence of one.
	// The COMPLETE transcript is written beside the record, so the hash above
	// binds to something a reader can actually re-hash -- and so a
	// classification can be checked against the text that produced it. Under
	// harness v1 only the last 20KB was kept, which is why instrument defect
	// #13 could be neither confirmed nor refuted.
	tdir := filepath.Join(dirOf(manifestPath), "transcripts", t.ID, string(arm))
	if err := os.MkdirAll(tdir, 0o755); err == nil {
		tpath := filepath.Join(tdir, fmt.Sprintf("%d.log", attempt))
		if err := os.WriteFile(tpath, []byte(out.Raw), 0o644); err == nil {
			a.Artifacts["transcript"] = tpath
		}
	}
	// The patch itself, beside the record.
	//
	// A hash proves two runs produced the same patch and supports no other
	// claim. Asking later whether a solution the oracle scored CORRECT was an
	// ADMISSIBLE change to this architecture requires the code, and proof-v5
	// could not answer that question because only hashes survived.
	if patch != "" {
		pdir := filepath.Join(dirOf(manifestPath), "patches", t.ID, string(arm))
		if err := os.MkdirAll(pdir, 0o755); err == nil {
			ppath := filepath.Join(pdir, fmt.Sprintf("%d.patch", attempt))
			if err := os.WriteFile(ppath, []byte(patch), 0o644); err == nil {
				a.Artifacts["patch"] = ppath
			}
		}
	}
	if err := l.Append(a); err != nil {
		return err
	}
	fmt.Printf("  terminal %s\n  oracle   %s\n  wall     %ds\n", a.Terminal, a.Verdict, a.WallSecs)
	if a.Infrastructure != "" {
		fmt.Printf("  infra    %s\n", a.Infrastructure)
	}
	for _, c := range a.RefusalClaims {
		fmt.Printf("  claim    %s\n", firstLineOf(c))
	}
	if a.InfrastructureHint != "" {
		fmt.Printf("  hint     %q seen but overruled by %s (recorded, not scored)\n",
			a.InfrastructureHint, a.Terminal)
	}
	return nil
}

// readQuotaOrNil takes a capacity reading, treating failure as absence.
//
// nil is "the provider did not tell us", never zero. A missing reading must not
// read as a full tank.
func readQuotaOrNil(ctx context.Context, r proofbench.Runner) *proofbench.QuotaReading {
	q, err := proofbench.ReadQuota(ctx, r.Binary, r.RepoRoot)
	if err != nil {
		return nil
	}
	return &q
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

// capacity refuses a campaign that cannot finish.
//
// The gate the halted REPAIR_VERIFICATION wave did not have. It passed a probe
// asking "can an arm start?" -- which the five-hour window, freshly reset,
// happily answered yes to -- while the seven-day window that eleven arms would
// actually consume stood at 96%.
func capacity(args []string) int {
	fs := flag.NewFlagSet("capacity", flag.ContinueOnError)
	manifest := fs.String("manifest", "", "path to the frozen manifest (required)")
	arms := fs.Int("arms", 0, "arms remaining (default: every arm the manifest schedules)")
	perArm := fs.Float64("per-arm", 0,
		"fraction of the binding window one arm consumes (default: the worst observed in this ledger)")
	byRole := fs.Bool("by-role", false, "establish capacity per configured role rather than one global reading")
	acceptProven := fs.Bool("accept-proven", false,
		"admit a role that answers but reports no limits, on availability alone; the admission records it")
	if err := fs.Parse(args); err != nil || strings.TrimSpace(*manifest) == "" {
		fs.Usage()
		return exitUsage
	}
	m, _, err := proofbench.LoadManifest(*manifest)
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench capacity:", err)
		return exitFailed
	}
	root, err := repoRoot()
	if err != nil {
		fmt.Fprintln(os.Stderr, "proofbench capacity:", err)
		return exitFailed
	}
	dir := dirOf(*manifest)
	if *arms == 0 {
		*arms = len(m.Tasks) * len(proofbench.Arms)
	}
	// Prefer a measured per-arm cost over anything an operator guessed.
	source := "supplied"
	if *perArm <= 0 {
		if l, lerr := proofbench.OpenLedger(filepath.Join(dir, "runs")); lerr == nil {
			if observed := proofbench.RecordedPerArm(l.Attempts()); observed > 0 {
				*perArm, source = observed, "worst observed in this ledger"
			}
		}
	}
	// Per role, or not at all (instrument defect #14).
	if *byRole {
		ctx := context.Background()
		roles := []proofbench.RoleCapacity{
			proofbench.ProveArchitectCapacity(ctx, binaryPath(root), root),
			proofbench.ReadClaudeCapacity(ctx),
		}
		for _, r := range roles {
			state := "UNREADABLE"
			if r.Readable {
				state = fmt.Sprintf("%.1f%% of %s available", r.Available*100, r.Window)
			} else if r.Proven {
				state = "proven available, no limits reported"
			}
			fmt.Printf("  %-12s %-8s %s %s\n", r.Role, r.Provider, state, r.Detail)
		}
		fmt.Printf("  arms         %d scheduled, per arm %.2f%%\n", *arms, *perArm*100)
		if err := proofbench.AdmitCampaignByRole(roles, *arms, *perArm, *acceptProven); err != nil {
			fmt.Fprintln(os.Stderr, "\n"+err.Error())
			return exitFailed
		}
		if *acceptProven {
			fmt.Println("\n  ADMITTED — the architect on PROOF OF AVAILABILITY only (--accept-proven); " +
				"the implementor on a quantitative reading")
		} else {
			fmt.Println("\n  ADMITTED — every required role has provable capacity")
		}
		return exitOK
	}
	q, err := proofbench.ReadQuota(context.Background(), binaryPath(root), root)
	origin := "live probe"
	if err != nil {
		// A stale reading, clearly labelled, beats refusing to answer at all --
		// but it is never silently presented as current.
		if recovered, from, ok := proofbench.LatestQuotaFromTranscripts(dir); ok {
			q, origin = recovered, "STALE, from "+from
		} else {
			fmt.Fprintln(os.Stderr, "proofbench capacity:", err)
			return exitFailed
		}
	}
	window, available := q.Tightest()
	fmt.Printf("  source      %s\n", origin)
	fmt.Printf("  windows     %s\n", q)
	fmt.Printf("  binding     %s, %.1f%% remaining\n", window, available*100)
	if *perArm > 0 {
		fmt.Printf("  per arm     %.2f%% (%s)\n", *perArm*100, source)
	}
	fmt.Printf("  arms        %d scheduled\n", *arms)
	if err := proofbench.AdmitCampaign(q, *arms, *perArm); err != nil {
		fmt.Fprintln(os.Stderr, "\n"+err.Error())
		return exitFailed
	}
	fmt.Printf("\n  ADMITTED — %d arm(s) fit in the %s window with a %.0f%% margin\n",
		*arms, window, (proofbench.CapacityMargin-1)*100)
	return exitOK
}

// firstLineOf keeps console output to one line without losing the record.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 150 {
		s = s[:150] + "…"
	}
	return s
}

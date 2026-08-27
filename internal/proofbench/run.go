package proofbench

// Executing one arm of one task, in isolation.
//
// The isolation requirements are the experiment. If RAW can see Sensei
// knowledge, or COLD can see WARM state, or any arm can see the oracle, the
// numbers this harness produces are about something other than what they claim
// to measure -- and they will still look like numbers.
//
// So the leak checks below run BEFORE the provider is invoked, and refuse
// rather than warn. A campaign that reports a contaminated run as a result is
// worse than one that reports NO_RESULT.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Runner executes arms against a repository.
type Runner struct {
	// RepoRoot is the governed checkout. It is never the working directory of
	// any arm: every run gets its own worktree off a pinned base.
	RepoRoot string
	// WorkDir is where per-arm worktrees are created.
	WorkDir string
	// Binary is the sensei-code binary the governed arms drive.
	Binary string
	// RawCommand is the ordinary coding agent RAW uses, as argv with {{TASK}}
	// replaced by the task statement.
	RawCommand []string
	// CorpusRoot is where the manifest and its oracles live, so a contract
	// probe can be loaded at judgement time.
	CorpusRoot string
	// Now is injected so a run record's timestamps are reproducible in tests.
	Now func() time.Time
}

// Plan is one arm of one task, ready to execute.
type Plan struct {
	Task           Task
	Arm            Arm
	Attempt        int
	ManifestHash   string
	Benchmark      string
	ExecutionOrder int
	Providers      map[string]string
}

// ExecutionOrder returns the arm order for a task, deterministically shuffled.
//
// Counterbalanced rather than fixed, so provider service drift over an evening
// does not always land on the same arm. Deterministic from the task id, so the
// order is reproducible and recorded rather than genuinely random -- a run
// nobody can replay is not evidence.
func ExecutionOrder(taskID string) []Arm {
	sum := sha256.Sum256([]byte(taskID))
	out := append([]Arm(nil), Arms...)
	// Fisher-Yates driven by the digest.
	for i := len(out) - 1; i > 0; i-- {
		j := int(sum[i%len(sum)]) % (i + 1)
		out[i], out[j] = out[j], out[i]
	}
	return out
}

// ErrLeak is returned when an arm would receive something it must not see.
type ErrLeak struct{ What string }

func (e ErrLeak) Error() string { return "isolation violated: " + e.What }

// CheckPromptIsolation refuses a prompt that leaks the oracle.
//
// The worker must not score itself, and this is where that stops being a
// promise about the runner and becomes a property of the prompt. Checked on the
// exact text that will be sent.
func CheckPromptIsolation(prompt string, t Task) error {
	low := strings.ToLower(prompt)
	for _, hidden := range t.Oracle.Hidden() {
		h := strings.ToLower(strings.TrimSpace(hidden))
		if h == "" {
			continue
		}
		if strings.Contains(low, h) {
			return ErrLeak{fmt.Sprintf("task %s prompt contains hidden oracle material %q", t.ID, hidden)}
		}
	}
	for _, cmd := range t.Oracle.Command {
		if c := strings.ToLower(strings.TrimSpace(cmd)); len(c) > 8 && strings.Contains(low, c) {
			return ErrLeak{fmt.Sprintf("task %s prompt contains the oracle command %q", t.ID, cmd)}
		}
	}
	return nil
}

// CheckWorktreeIsolation refuses a worktree that carries what this arm must not
// have.
//
// RAW must not see Sensei project knowledge; COLD must not see WARM state.
// Checked against the filesystem the arm will actually run in, because that is
// the thing that can be wrong -- a runner's intention about what it copied is
// not evidence about what is there.
func CheckWorktreeIsolation(dir string, arm Arm, t Task) error {
	exists := func(rel string) bool {
		_, err := os.Stat(filepath.Join(dir, rel))
		return err == nil
	}
	// The oracle's withheld files must be absent from every arm's checkout.
	for _, p := range t.Oracle.Paths {
		if exists(p) {
			return ErrLeak{fmt.Sprintf("withheld oracle file %s is present in the %s worktree", p, arm)}
		}
	}
	switch arm {
	case ArmRaw:
		// RAW is the baseline for "what did the control plane buy". Sensei
		// project knowledge in its checkout would answer a different question.
		for _, rel := range []string{".sensei", "docs/awareness", ".sensei-code"} {
			if exists(rel) {
				return ErrLeak{fmt.Sprintf("RAW worktree contains Sensei project knowledge at %s", rel)}
			}
		}
	case ArmCold:
		// COLD may use knowledge valid at the pinned base and nothing learned
		// from later benchmark tasks.
		if exists(warmStateMarker) {
			return ErrLeak{"COLD worktree carries WARM benchmark state"}
		}
	}
	return nil
}

// warmStateMarker is the file a WARM worktree carries its accumulated
// benchmark knowledge in. Its presence in a COLD worktree is contamination.
const warmStateMarker = ".sensei-code/benchmark-warm-state.json"

// Prepare creates an isolated worktree for one arm and proves it clean.
func (r Runner) Prepare(ctx context.Context, p Plan) (string, error) {
	dir := filepath.Join(r.WorkDir, p.Task.ID, string(p.Arm), fmt.Sprintf("%d", p.Attempt))
	if _, err := os.Stat(dir); err == nil {
		return "", fmt.Errorf("refusing to reuse an existing worktree at %s; every attempt gets a "+
			"fresh checkout or it is not an independent observation", dir)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}
	// Detached at the pinned base. A run from any other commit is not this
	// experiment.
	cmd := exec.CommandContext(ctx, "git", "-C", r.RepoRoot, "worktree", "add", "--detach", dir, p.Task.BaseSHA)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("worktree add at %s: %w: %s", p.Task.BaseSHA, err, strings.TrimSpace(string(b)))
	}
	if err := r.verifyBase(ctx, dir, p.Task.BaseSHA); err != nil {
		return "", err
	}
	// Withhold the oracle before anything runs in here.
	for _, rel := range p.Task.Oracle.Paths {
		if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
			return "", fmt.Errorf("withholding oracle file %s: %w", rel, err)
		}
	}
	if p.Arm == ArmRaw {
		for _, rel := range []string{".sensei", "docs/awareness", ".sensei-code"} {
			if err := os.RemoveAll(filepath.Join(dir, rel)); err != nil {
				return "", err
			}
		}
	}
	if err := CheckWorktreeIsolation(dir, p.Arm, p.Task); err != nil {
		return "", err
	}
	// The withholding has to be COMMITTED, not merely applied.
	//
	// A governed run refuses to start in a checkout with uncommitted changes --
	// "a governed candidate cut from HEAD would omit them, so it would govern a
	// state you are not looking at" -- which is a correct product rule and not
	// one this harness may weaken to make itself runnable. Deleting the oracle
	// files leaves exactly that dirt, so every COLD and WARM arm failed in one
	// second before reaching a provider.
	//
	// The commit message states plainly what was done. Concealing it would mean
	// the instrument deceiving the thing it measures, and the removed paths are
	// visible in the diff regardless; what the worker cannot see is the CONTENT
	// of the withheld tests, which is what the oracle rests on.
	if _, err := r.commitWithholding(ctx, dir); err != nil {
		return "", err
	}
	return dir, nil
}

// commitWithholding commits the harness's own preparation and returns the
// derived commit the arm actually runs from.
//
// Recorded separately from the manifest's pinned base: the two are different
// commits and reporting the pinned one as what ran would be a small lie about
// which tree the provider saw.
func (r Runner) commitWithholding(ctx context.Context, dir string) (string, error) {
	status, err := exec.CommandContext(ctx, "git", "-C", dir,
		"--no-optional-locks", "status", "--porcelain").Output()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(string(status)) == "" {
		return r.headOf(ctx, dir)
	}
	if b, err := exec.CommandContext(ctx, "git", "-C", dir, "add", "-A").CombinedOutput(); err != nil {
		return "", fmt.Errorf("staging the withholding: %w: %s", err, strings.TrimSpace(string(b)))
	}
	msg := "benchmark: withhold the task oracle from this worker checkout\n\n" +
		"Applied by proofbench before any provider ran. The withheld test files are " +
		"restored only at evaluation time, by the harness, after the worker has finished."
	cmd := exec.CommandContext(ctx, "git", "-C", dir, "-c", "user.name=proofbench",
		"-c", "user.email=proofbench@invalid", "commit", "-q", "-m", msg)
	if b, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("committing the withholding: %w: %s", err, strings.TrimSpace(string(b)))
	}
	return r.headOf(ctx, dir)
}

func (r Runner) headOf(ctx context.Context, dir string) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"--no-optional-locks", "rev-parse", "HEAD").Output()
	return strings.TrimSpace(string(out)), err
}

// RunBase is the commit an arm actually ran from: the pinned base with the
// withheld oracle removed.
func (r Runner) RunBase(ctx context.Context, dir string) string {
	h, _ := r.headOf(ctx, dir)
	return h
}

// verifyBase refuses a checkout that is not at the pinned commit.
func (r Runner) verifyBase(ctx context.Context, dir, want string) error {
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "--no-optional-locks", "rev-parse", "HEAD").Output()
	if err != nil {
		return err
	}
	if got := strings.TrimSpace(string(out)); got != want {
		return fmt.Errorf("worktree is at %s, manifest pins %s; a run from the wrong base is not "+
			"a run of this task", got, want)
	}
	return nil
}

// OracleResult is what the hidden oracle decided.
type OracleResult struct {
	Verdict Verdict
	Detail  string
}

// Judge restores the withheld evidence and runs the oracle.
//
// Restored AFTER the worker is finished, from the accepted fix, into the
// candidate checkout. The worker never saw these files; the oracle is built
// from them.
// Judge scores the tree passed to it.
//
// The caller resolves that tree with ResolveCandidate and passes it in. Judge
// deliberately does NOT derive it: the proof-v5 failure was an evaluator that
// assumed the arm checkout, and an evaluator that can assume can assume wrong.
func (r Runner) Judge(ctx context.Context, dir string, t Task) OracleResult {
	// A gated task is judged by its behavioural contract, never by the accepted
	// fix's own tests. The withheld-tests path below survives only for tasks
	// that have not been given a contract oracle yet, and a task in that state
	// is not benchmark-eligible.
	if t.Contract != nil {
		return r.judgeContract(ctx, dir, t, *t.Contract)
	}
	switch t.Oracle.Kind {
	case "withheld_tests":
		for _, rel := range t.Oracle.Paths {
			src := filepath.Join(r.RepoRoot, rel)
			b, err := os.ReadFile(src)
			if err != nil {
				// Read from the fix commit rather than the working tree when
				// the file does not exist at HEAD.
				out, gerr := exec.CommandContext(ctx, "git", "-C", r.RepoRoot, "show",
					t.Origin+":"+rel).Output()
				if gerr != nil {
					return OracleResult{Inconclusive,
						fmt.Sprintf("could not restore withheld oracle %s: %v / %v", rel, err, gerr)}
				}
				b = out
			}
			dst := filepath.Join(dir, rel)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return OracleResult{Inconclusive, err.Error()}
			}
			if err := os.WriteFile(dst, b, 0o644); err != nil {
				return OracleResult{Inconclusive, err.Error()}
			}
		}
		fallthrough
	case "behavioral_probe":
		cmd := exec.CommandContext(ctx, t.Oracle.Command[0], t.Oracle.Command[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		detail := tail(string(out), 4000)
		if err == nil {
			return OracleResult{Correct, detail}
		}
		// A command that could not START is not a verdict about the candidate.
		if _, ok := err.(*exec.ExitError); !ok {
			return OracleResult{Inconclusive, fmt.Sprintf("oracle could not run: %v\n%s", err, detail)}
		}
		return OracleResult{Incorrect, detail}
	case "independent_review":
		// Deliberately not implemented as an automatic pass. An oracle this
		// harness cannot run must not silently become CORRECT.
		return OracleResult{Inconclusive,
			"independent_review oracle requires a reviewer provider that did not author the " +
				"candidate; not executed by this harness"}
	}
	return OracleResult{Inconclusive, "unknown oracle kind " + t.Oracle.Kind}
}

// judgeContract writes the probe into the finished candidate and runs it.
//
// The probe arrives AFTER the worker is done, exactly like the withheld tests
// it replaces. What changed is what it asserts: observable behaviour through a
// surface the task statement establishes, so a correct solution the benchmark
// did not write still passes.
func (r Runner) judgeContract(ctx context.Context, dir string, t Task, o ContractOracle) OracleResult {
	if strings.TrimSpace(o.Source) == "" {
		if err := o.LoadProbe(r.CorpusRoot); err != nil {
			return OracleResult{Inconclusive, "contract oracle: " + err.Error()}
		}
	}
	// The accepted fix's own tests must not be in the candidate: they would
	// judge an ALTERNATE-shaped answer by the reference's names, which is the
	// defect this whole oracle replaced.
	for _, rel := range t.Oracle.Paths {
		_ = os.RemoveAll(filepath.Join(dir, rel))
	}
	probe := filepath.Join(dir, o.Probe)
	if err := os.MkdirAll(filepath.Dir(probe), 0o755); err != nil {
		return OracleResult{Inconclusive, err.Error()}
	}
	if err := os.WriteFile(probe, []byte(o.Source), 0o644); err != nil {
		return OracleResult{Inconclusive, err.Error()}
	}
	cmd := exec.CommandContext(ctx, o.Command[0], o.Command[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	detail := tail(string(out), 4000)
	if err == nil {
		return OracleResult{Correct, detail}
	}
	if _, ok := err.(*exec.ExitError); !ok {
		return OracleResult{Inconclusive, fmt.Sprintf("contract probe could not run: %v\n%s", err, detail)}
	}
	return OracleResult{Incorrect, detail}
}

// CandidateEvidence is the git-side record of what an arm actually produced.
type CandidateEvidence struct {
	DiffHash string
	// Measurable says whether GovernedClean means anything.
	Measurable bool
	// GovernedClean reports the governed checkout was not mutated. Compared
	// before and after, because a single after-the-fact reading cannot tell a
	// change this run made from one already present.
	GovernedClean bool
	GovernedState string
}

// Evidence hashes the candidate diff and checks the governed checkout.
//
// measurable says whether the before reading can support a comparison at all: a
// governed checkout that was already dirty at arm start cannot distinguish a
// change this arm made from one the operator made while it ran.
func (r Runner) Evidence(ctx context.Context, dir, governedBefore string, measurable bool) CandidateEvidence {
	var e CandidateEvidence
	e.Measurable = measurable
	add := exec.CommandContext(ctx, "git", "-C", dir, "add", "--intent-to-add", "--", ".")
	_ = add.Run()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "diff").Output()
	if err == nil {
		sum := sha256.Sum256(out)
		e.DiffHash = "sha256:" + hex.EncodeToString(sum[:])
	}
	// Read the AFTER state through the same filter as the before state.
	//
	// It was read raw while the before was filtered, so the harness's own
	// output -- the record and transcript this very call is about to write --
	// appeared only on one side of the comparison and every arm reported a
	// governed-checkout mutation it had not made. Comparing two readings taken
	// different ways is not comparing two moments.
	e.GovernedState = r.GovernedState(ctx)
	e.GovernedClean = e.GovernedState == governedBefore
	return e
}

// GovernedState reads the governed checkout, for the before half of the
// comparison.
//
// The harness's OWN output is excluded. proofbench writes each record and
// transcript into benchmark/<version>/ inside the governed checkout, so after
// the first arm the checkout was never quiescent again and every subsequent
// boundary reading was discarded as unmeasurable -- the instrument blinding
// itself, one arm at a time.
//
// Excluding it is sound because those paths are attributable by construction:
// they are written by this process, after its arm has finished, and no arm can
// reach them from its own isolated worktree. Everything else still counts.
func (r Runner) GovernedState(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "-C", r.RepoRoot,
		"--no-optional-locks", "status", "--porcelain").Output()
	if err != nil {
		return ""
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" || strings.Contains(line, HarnessOutputDir) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// HarnessOutputDir is where proofbench writes its own evidence. A path
// containing it is this process's work, not an arm's.
const HarnessOutputDir = "benchmark/"

// Cleanup removes an arm's worktree.
func (r Runner) Cleanup(ctx context.Context, dir string) error {
	b, err := exec.CommandContext(ctx, "git", "-C", r.RepoRoot, "worktree", "remove", "--force", dir).CombinedOutput()
	if err != nil {
		return fmt.Errorf("worktree remove %s: %w: %s", dir, err, strings.TrimSpace(string(b)))
	}
	return nil
}

// ProviderIdentity records which provider/model played which role, so a change
// inside a paired comparison invalidates the pair instead of being combined.
func ProviderIdentity(roles map[string]string) map[string]string {
	out := map[string]string{}
	keys := make([]string, 0, len(roles))
	for k := range roles {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		out[k] = roles[k]
	}
	return out
}

// SameProviders reports whether two attempts can support a comparative claim.
func SameProviders(a, b Attempt) (bool, string) {
	if len(a.Providers) != len(b.Providers) {
		return false, fmt.Sprintf("provider role sets differ (%d vs %d)", len(a.Providers), len(b.Providers))
	}
	for role, id := range a.Providers {
		if other, ok := b.Providers[role]; !ok || other != id {
			return false, fmt.Sprintf("role %q: %s vs %s", role, id, other)
		}
	}
	return true, ""
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}

package proofbench

// Can this oracle recognise correctness it did not already know how to write?
//
// proof-v3's oracles could not. They were the accepted fix's own test files,
// and they failed to compile against any solution that chose different names --
// so `m.scrollUp undefined` was indistinguishable from doing nothing at all. An
// oracle like that measures whether a worker guessed the reference patch.
//
// The gate below is the repair, and it is a gate rather than a guideline: a task
// is benchmark-eligible only once its oracle has been shown to separate three
// specimens.
//
//	REFERENCE   the accepted fix                          must PASS
//	WRONG       a deliberate no-op or broken change        must FAIL
//	ALTERNATE   correct, with different internal structure must PASS
//
// The third is the sharp one. REFERENCE-passes and WRONG-fails is satisfied by
// an oracle that merely recompiles the reference patch; only ALTERNATE
// establishes that the oracle recognises a solution nobody wrote for it.
//
// A task whose ALTERNATE fails is not a task the campaign may use. It is an
// oracle that has memorised one answer.

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
)

// SpecimenKind is which of the three a specimen is.
type SpecimenKind string

const (
	// Reference is the accepted historical fix.
	Reference SpecimenKind = "REFERENCE"
	// Wrong is a deliberate non-solution: a no-op, or a change that does not
	// deliver the contract.
	Wrong SpecimenKind = "WRONG"
	// Alternate is a correct solution written to differ structurally from the
	// reference -- different names, decomposition, or approach.
	Alternate SpecimenKind = "ALTERNATE"
)

// Specimen is one candidate the oracle must judge correctly.
type Specimen struct {
	Kind SpecimenKind `json:"kind"`
	// Patch is a directory of files copied over the pinned base to produce this
	// specimen, relative to the corpus root.
	Patch string `json:"patch"`
	// FromCommit builds the specimen by checking out the accepted fix instead
	// of copying a patch directory. Only meaningful for REFERENCE.
	FromCommit bool `json:"from_commit,omitempty"`
	// Want is the verdict the oracle must return for this specimen.
	Want Verdict `json:"want"`
	// Why says what this specimen is testing, for a reader of the manifest.
	Why string `json:"why"`
}

// ContractOracle judges observable behaviour rather than implementation shape.
//
// Probe is a test file written FOR THE BENCHMARK, not lifted from the accepted
// fix. It may assert outputs, state transitions, error classes, invariants and
// externally visible effects. It may not require private helper names, a
// particular decomposition, filenames, or the reference patch's symbols --
// unless those symbols are part of the public contract the TASK STATEMENT
// names, which is a requirement rather than a leak.
type ContractOracle struct {
	// Probe is the path the probe file is written to inside the candidate.
	Probe string `json:"probe"`
	// SourceFile is the probe's content, held in the corpus beside the manifest.
	//
	// A .go.txt file rather than .go, so corpus sources never join this module's
	// build -- a probe that compiles as part of the repository under test is not
	// withheld from anything.
	SourceFile string `json:"source_file"`
	// Source is the loaded content. Populated by LoadProbe, never by the JSON.
	Source string `json:"-"`
	// Command decides the verdict once the probe is in place.
	Command []string `json:"command"`
	// Specimens are the three the oracle must separate.
	Specimens []Specimen `json:"specimens"`
}

// Hash is the oracle's identity, frozen before any arm runs.
//
// Over the probe SOURCE and the command, so that editing a probe after seeing
// an arm's answer changes the hash and orphans every result taken under the old
// one. The specimens are excluded on purpose: adding a fourth specimen
// strengthens the gate without changing what an arm is judged by.
func (o ContractOracle) Hash() string {
	sum := sha256.Sum256([]byte(o.Probe + "\x00" + o.Source + "\x00" + strings.Join(o.Command, "\x1f")))
	return "sha256:" + hex.EncodeToString(sum[:])[:32]
}

// LoadProbe reads the probe source from the corpus.
//
// Separate from decoding, so a manifest can be read without its corpus present
// and so the hash is over what is actually on disk at gate time.
func (o *ContractOracle) LoadProbe(corpusRoot string) error {
	b, err := os.ReadFile(filepath.Join(corpusRoot, o.SourceFile))
	if err != nil {
		return fmt.Errorf("probe source %s: %w", o.SourceFile, err)
	}
	o.Source = string(b)
	return nil
}

// Validate refuses an oracle that cannot support the gate.
func (o ContractOracle) Validate() error {
	if strings.TrimSpace(o.Probe) == "" {
		return fmt.Errorf("contract oracle has no probe path")
	}
	if strings.TrimSpace(o.SourceFile) == "" {
		return fmt.Errorf("contract oracle names no probe source file")
	}
	if strings.TrimSpace(o.Source) == "" {
		return fmt.Errorf("probe source not loaded from %s", o.SourceFile)
	}
	if len(o.Command) == 0 {
		return fmt.Errorf("contract oracle has no command to decide the verdict")
	}
	kinds := map[SpecimenKind]bool{}
	for _, s := range o.Specimens {
		kinds[s.Kind] = true
	}
	for _, need := range []SpecimenKind{Reference, Wrong, Alternate} {
		if !kinds[need] {
			return fmt.Errorf("no %s specimen; an oracle that has not been shown to separate all "+
				"three has not been shown to recognise correctness it did not write", need)
		}
	}
	return nil
}

// DiscriminationResult is one specimen's outcome.
type DiscriminationResult struct {
	Kind   SpecimenKind `json:"kind"`
	Want   Verdict      `json:"want"`
	Got    Verdict      `json:"got"`
	OK     bool         `json:"ok"`
	Detail string       `json:"detail"`
}

// TaskDiscrimination is the whole gate for one task.
type TaskDiscrimination struct {
	Task       string                 `json:"task"`
	OracleHash string                 `json:"oracle_hash"`
	Results    []DiscriminationResult `json:"results"`
	// Eligible is true only when every specimen landed where it must.
	Eligible bool   `json:"eligible"`
	Reason   string `json:"reason,omitempty"`
}

// Discriminate runs every specimen through the oracle.
//
// This is the only thing that makes a task benchmark-eligible, and it runs
// before any provider is paid.
func (r Runner) Discriminate(ctx context.Context, corpusRoot string, t Task, o ContractOracle) TaskDiscrimination {
	if err := o.LoadProbe(corpusRoot); err != nil {
		return TaskDiscrimination{Task: t.ID, Reason: err.Error()}
	}
	d := TaskDiscrimination{Task: t.ID, OracleHash: o.Hash()}
	if err := o.Validate(); err != nil {
		d.Reason = err.Error()
		return d
	}
	for _, s := range o.Specimens {
		got, detail := r.judgeSpecimen(ctx, corpusRoot, t, o, s)
		d.Results = append(d.Results, DiscriminationResult{
			Kind: s.Kind, Want: s.Want, Got: got, OK: got == s.Want, Detail: detail})
	}
	d.Eligible = true
	var failed []string
	for _, res := range d.Results {
		if !res.OK {
			d.Eligible = false
			failed = append(failed, fmt.Sprintf("%s wanted %s, got %s", res.Kind, res.Want, res.Got))
		}
	}
	if !d.Eligible {
		d.Reason = strings.Join(failed, "; ")
		for _, res := range d.Results {
			if res.Kind == Alternate && !res.OK {
				d.Reason += " — the ALTERNATE failure is the disqualifying one: this oracle has " +
					"memorised one answer rather than learned the contract"
			}
		}
	}
	return d
}

// judgeSpecimen builds one specimen at the pinned base and runs the probe.
func (r Runner) judgeSpecimen(ctx context.Context, corpusRoot string, t Task, o ContractOracle, s Specimen) (Verdict, string) {
	dir := filepath.Join(r.WorkDir, "discriminate", t.ID, string(s.Kind))
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return Inconclusive, err.Error()
	}
	base := t.BaseSHA
	if s.FromCommit {
		base = t.Origin
	}
	if b, err := exec.CommandContext(ctx, "git", "-C", r.RepoRoot,
		"worktree", "add", "--detach", dir, base).CombinedOutput(); err != nil {
		return Inconclusive, fmt.Sprintf("worktree add %s: %v: %s", base, err, strings.TrimSpace(string(b)))
	}
	defer func() {
		_ = exec.Command("git", "-C", r.RepoRoot, "worktree", "remove", "--force", dir).Run()
	}()

	// A patch directory is copied over the base. REFERENCE usually needs none,
	// because checking out the accepted fix IS the specimen.
	if p := strings.TrimSpace(s.Patch); p != "" {
		if err := copyTree(filepath.Join(corpusRoot, p), dir); err != nil {
			return Inconclusive, "applying specimen patch: " + err.Error()
		}
	}
	// The reference checkout carries the accepted fix's own tests. They must be
	// removed, or the probe's verdict would be contaminated by them -- and on an
	// ALTERNATE specimen they would fail for exactly the reason this whole gate
	// exists to eliminate.
	for _, rel := range t.Oracle.Paths {
		_ = os.RemoveAll(filepath.Join(dir, rel))
	}
	probe := filepath.Join(dir, o.Probe)
	if err := os.MkdirAll(filepath.Dir(probe), 0o755); err != nil {
		return Inconclusive, err.Error()
	}
	if err := os.WriteFile(probe, []byte(o.Source), 0o644); err != nil {
		return Inconclusive, err.Error()
	}

	cmd := exec.CommandContext(ctx, o.Command[0], o.Command[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	detail := tail(string(out), 2500)
	if err == nil {
		return Correct, detail
	}
	if _, ok := err.(*exec.ExitError); !ok {
		return Inconclusive, fmt.Sprintf("probe could not run: %v\n%s", err, detail)
	}
	return Incorrect, detail
}

// copyTree copies a specimen patch directory over a checkout.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(src, path)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		// Corpus sources are stored as .go.txt so they never join this module's
		// build, and land in the specimen checkout as the .go files they are.
		target = strings.TrimSuffix(target, ".txt")
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return os.WriteFile(target, b, 0o644)
	})
}

// EligibleTasks are the ids whose oracle passed the gate, sorted.
func EligibleTasks(ds []TaskDiscrimination) []string {
	var out []string
	for _, d := range ds {
		if d.Eligible {
			out = append(out, d.Task)
		}
	}
	sort.Strings(out)
	return out
}

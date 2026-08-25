// Package proofbench is measurement infrastructure. It is not a control plane.
//
// Nothing here routes, authorizes, reviews, admits, or decides anything about a
// candidate. It observes runs and scores them, and the separation is load
// bearing: the whole point of the campaign is to find out whether the existing
// mechanism works, and a benchmark that could alter the mechanism it measures
// would answer a different question than the one asked.
//
// So this package may READ event streams, receipts, candidate metadata, git
// state and provider usage. It may not write anything a governed run consults.
//
// # Why JSON and not YAML
//
// The brief sketches manifest.yaml. This repository has no YAML dependency in
// Go -- awareness YAML is read by the sensei CLI, not here -- and the brief says
// names may vary to fit the repository. JSON also hashes deterministically
// without a canonicalisation step, which the manifest freeze depends on.
package proofbench

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

// Arm is one experimental condition.
type Arm string

const (
	// ArmRaw is the same author provider working directly, with no Sensei
	// governance and no project knowledge. The baseline for "what did the
	// control plane buy over the coding model itself".
	ArmRaw Arm = "RAW"
	// ArmCold is Sensei-code with only knowledge valid at the task's pinned
	// base.
	ArmCold Arm = "COLD"
	// ArmWarm is Sensei-code carrying durable knowledge from earlier, DIFFERENT
	// benchmark tasks in the declared sequence.
	ArmWarm Arm = "WARM"
)

// Arms is every arm, in a fixed order for deterministic reporting. Execution
// order is randomised per task; this is only for rendering.
var Arms = []Arm{ArmRaw, ArmCold, ArmWarm}

// Manifest is the frozen experiment definition.
//
// Frozen means hashed. Changing task text, base SHA, oracle, arm configuration
// or the selection rule produces a different hash, and every run record carries
// the hash it ran under -- so a manifest edited after runs exist does not
// silently reinterpret them, it orphans them. See TestEditingAManifestOrphansItsRuns.
type Manifest struct {
	// Version names this benchmark. A changed manifest needs a changed version.
	Version string `json:"version"`
	// Stage says what this corpus is for, and it changes what soundness means.
	//
	// "calibration" is the small slice run first to find out whether the scores
	// are interpretable at all -- the mandate's two tasks by RAW and COLD. It is
	// held to the same oracle gate and to none of the size minimums, because
	// requiring ten tasks of a two-task probe would make the cheap check
	// impossible and push the campaign straight back to the expensive one.
	//
	// "full" is the campaign proper and carries every minimum.
	//
	// Declared in the manifest, before results, so the exemption is a stated
	// purpose rather than a threshold lowered once the corpus came up short.
	Stage string `json:"stage"`
	// SelectionRule is the eligibility rule, recorded because it was frozen
	// BEFORE the corpus was chosen. A rule written after seeing which tasks
	// Sensei happens to be good at is not a rule.
	SelectionRule string `json:"selection_rule"`
	// Excluded records candidates the rule admitted and a human or the
	// selector dropped, with the reason. Survivor bias is visible or it is
	// operating.
	Excluded []Exclusion `json:"excluded"`
	// Calibration specimens validate the instrument and never count toward the
	// primary n.
	Calibration []Task `json:"calibration"`
	// Tasks is the primary corpus.
	Tasks []Task `json:"tasks"`
}

// Exclusion is one candidate the corpus did not take.
type Exclusion struct {
	Ref    string `json:"ref"`
	Reason string `json:"reason"`
}

// Task is one benchmark task, pinned.
type Task struct {
	ID string `json:"id"`
	// Statement is what the worker is told. It must not contain the
	// implementation, and CheckStatementHidesTheAnswer enforces the shape.
	Statement string `json:"statement"`
	// BaseSHA is the pre-fix commit every arm starts from.
	BaseSHA string `json:"base_sha"`
	// Origin is the historical PR/commit this task was reconstructed from,
	// recorded so a reader can audit the reconstruction.
	Origin string `json:"origin"`
	// Oracle decides CORRECT / INCORRECT / INCONCLUSIVE, and the worker never
	// sees it.
	Oracle Oracle `json:"oracle"`
	// DependsOn names earlier benchmark tasks whose durable knowledge WARM is
	// allowed to carry into this one. A task with dependencies is a linked
	// later-task specimen: the COLD-vs-WARM compounding comparison.
	DependsOn []string `json:"depends_on,omitempty"`
	// Expected is what a calibration specimen's outcome must be.
	Expected string `json:"expected,omitempty"`
	// Contract is the behavioural oracle that replaced the withheld-tests one.
	//
	// Optional while a corpus is being rebuilt: a task with no contract oracle
	// is simply not yet benchmark-eligible, and discriminate says so rather
	// than failing to load the manifest.
	Contract *ContractOracle `json:"contract,omitempty"`
}

// Linked reports whether this task can carry earlier experience.
func (t Task) Linked() bool { return len(t.DependsOn) != 0 }

// Oracle is how a task is scored, by something that is not the worker.
type Oracle struct {
	// Kind is "withheld_tests" (deterministic regression tests from the
	// accepted fix, kept out of the worker checkout until evaluation),
	// "behavioral_probe", or "independent_review".
	Kind string `json:"kind"`
	// Paths are the test files withheld from the worker and restored at
	// evaluation time.
	Paths []string `json:"paths,omitempty"`
	// Command is what decides the verdict once the withheld evidence is in
	// place.
	Command []string `json:"command,omitempty"`
	// Rubric is used by an independent_review oracle.
	Rubric string `json:"rubric,omitempty"`
}

// Hidden is everything the worker must never see for this task.
//
// Used by the leak check rather than by the runner, so that "the worker did not
// receive the oracle" is something the harness can assert about a prompt rather
// than something the runner promises about itself.
func (o Oracle) Hidden() []string {
	out := append([]string(nil), o.Paths...)
	if r := strings.TrimSpace(o.Rubric); r != "" {
		out = append(out, r)
	}
	return out
}

// LoadManifest reads and validates a manifest, returning it with its hash.
func LoadManifest(path string) (Manifest, string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Manifest{}, "", err
	}
	var m Manifest
	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, "", fmt.Errorf("%s: %w", path, err)
	}
	return m, HashBytes(b), m.Validate()
}

// HashBytes is the manifest identity: the hash of the file exactly as
// committed.
//
// Of the BYTES, not of the decoded structure. A reformat is a change to the
// frozen artifact, and treating it as one costs a version bump and buys the
// guarantee that the thing on disk is the thing that was hashed.
func HashBytes(b []byte) string {
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}

var shaRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

// Validate refuses a manifest that cannot support the claims made from it.
//
// Every check here is about whether the EXPERIMENT is sound, not whether the
// file parses. A manifest that parses and cannot answer the question is worse
// than one that fails to load, because it produces numbers.
func (m Manifest) Validate() error {
	var problems []string
	add := func(f string, a ...any) { problems = append(problems, fmt.Sprintf(f, a...)) }

	if strings.TrimSpace(m.Version) == "" {
		add("manifest has no version; a frozen artifact needs a name to be frozen under")
	}
	if strings.TrimSpace(m.SelectionRule) == "" {
		add("no selection rule recorded. The rule must be frozen before the corpus is chosen, " +
			"or the corpus is a choice about which tasks flatter the tool")
	}

	seen := map[string]bool{}
	for _, t := range append(append([]Task(nil), m.Calibration...), m.Tasks...) {
		if strings.TrimSpace(t.ID) == "" {
			add("a task has no id")
			continue
		}
		if seen[t.ID] {
			add("%s: duplicate task id", t.ID)
		}
		seen[t.ID] = true
		if !shaRE.MatchString(t.BaseSHA) {
			add("%s: base_sha %q is not a full 40-character commit; an abbreviated or missing base "+
				"cannot pin what every arm starts from", t.ID, t.BaseSHA)
		}
		if strings.TrimSpace(t.Statement) == "" {
			add("%s: no task statement", t.ID)
		}
		if err := t.Oracle.Validate(); err != nil {
			add("%s: %v", t.ID, err)
		}
	}
	for _, t := range m.Tasks {
		for _, dep := range t.DependsOn {
			if !seen[dep] {
				add("%s depends on %q, which is not in this manifest", t.ID, dep)
			}
			if dep == t.ID {
				add("%s depends on itself", t.ID)
			}
		}
	}
	switch m.Stage {
	case "calibration":
		// Size minimums do not apply; the oracle gate still does.
		if len(problems) == 0 {
			return nil
		}
		sort.Strings(problems)
		return fmt.Errorf("manifest is not sound:\n  - %s", strings.Join(problems, "\n  - "))
	case "full", "":
	default:
		add("unrecognised stage %q; a corpus must declare what it is for", m.Stage)
	}

	// The campaign's own stated minimums. Checked here so a corpus that cannot
	// support the pre-registered gates is refused before it costs provider
	// budget, rather than discovered in the report.
	if n := len(m.Tasks); n < MinPrimaryTasks {
		add("primary corpus has %d tasks; the campaign requires at least %d", n, MinPrimaryTasks)
	}
	if n := m.LinkedTasks(); n < MinLinkedTasks {
		add("only %d linked later-task specimens; the COLD-vs-WARM compounding comparison "+
			"requires at least %d", n, MinLinkedTasks)
	}
	if len(m.Calibration) == 0 {
		add("no calibration specimens; the instrument must first prove it can record a known " +
			"win and a known failure")
	}
	if len(problems) == 0 {
		return nil
	}
	sort.Strings(problems)
	return fmt.Errorf("manifest is not sound:\n  - %s", strings.Join(problems, "\n  - "))
}

// MinPrimaryTasks and MinLinkedTasks are the campaign's declared minimums.
const (
	MinPrimaryTasks = 10
	MinLinkedTasks  = 4
)

// Validate refuses an oracle the worker could satisfy by inspection.
func (o Oracle) Validate() error {
	switch o.Kind {
	case "withheld_tests":
		if len(o.Paths) == 0 {
			return fmt.Errorf("withheld_tests oracle withholds nothing")
		}
		if len(o.Command) == 0 {
			return fmt.Errorf("withheld_tests oracle has no command to decide the verdict")
		}
	case "behavioral_probe":
		if len(o.Command) == 0 {
			return fmt.Errorf("behavioral_probe oracle has no command")
		}
	case "independent_review":
		if strings.TrimSpace(o.Rubric) == "" {
			return fmt.Errorf("independent_review oracle has no frozen rubric")
		}
	default:
		return fmt.Errorf("oracle kind %q is not one this harness can run", o.Kind)
	}
	return nil
}

// LinkedTasks counts the primary tasks that can carry earlier experience.
func (m Manifest) LinkedTasks() int {
	n := 0
	for _, t := range m.Tasks {
		if t.Linked() {
			n++
		}
	}
	return n
}

// Task finds a task by id in either corpus.
func (m Manifest) Task(id string) (Task, bool) {
	for _, t := range append(append([]Task(nil), m.Calibration...), m.Tasks...) {
		if t.ID == id {
			return t, true
		}
	}
	return Task{}, false
}

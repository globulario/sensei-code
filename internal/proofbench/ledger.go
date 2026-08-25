package proofbench

// The attempt ledger: no survivor bias.
//
// Every attempt is evidence, including the ones that failed, the ones that
// answered INCONCLUSIVE, and the ones that never produced a result at all. The
// failure mode this file exists to prevent is the ordinary one -- run it again,
// keep the good one, report the good one -- and it is prevented structurally
// rather than by discipline: an attempt id cannot be written twice, so a retry
// has to declare itself a retry and both stay in the report.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Verdict is what the oracle said. There are three, and the third is real.
type Verdict string

const (
	Correct Verdict = "CORRECT"
	// Incorrect: the oracle ran and the candidate is wrong.
	Incorrect Verdict = "INCORRECT"
	// Inconclusive: the oracle ran and could not decide. It must never be
	// converted to success because the candidate looks plausible.
	Inconclusive Verdict = "INCONCLUSIVE"
	// NoResult: the attempt never reached the oracle -- infrastructure failed
	// twice, or the arm could not be executed at all. Reported as an
	// operational-reliability fact, never dropped.
	NoResult Verdict = "NO_RESULT"
)

// Intervention is a human touching a run, classified by what they supplied.
//
// The distinction is the whole autonomy claim: a human choosing whether to land
// a retained candidate does not invalidate autonomy, and a human telling the
// system what the technical answer is does.
type Intervention struct {
	// Kind is "technical_answer", "landing_decision", "authority_decision",
	// or "operational".
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
	At     string `json:"at"`
}

// TechnicalAnswer reports whether this intervention supplied a technical
// premise, diagnosis, implementation choice, or fix.
func (i Intervention) TechnicalAnswer() bool { return i.Kind == "technical_answer" }

// Objection is one reviewer finding, tracked across cycles so "the same
// objection reworded" is distinguishable from progress.
type Objection struct {
	ID     string `json:"id"`
	Cycle  int    `json:"cycle"`
	Status string `json:"status"` // new | repeated | resolved | outside_candidate_authority
	Text   string `json:"text"`
}

// Attempt is one arm of one task, once.
type Attempt struct {
	Task         string `json:"task"`
	Arm          Arm    `json:"arm"`
	Number       int    `json:"attempt"`
	ManifestHash string `json:"manifest_hash"`
	Benchmark    string `json:"benchmark_version"`

	BaseSHA string `json:"base_sha"`
	// RunBase is the commit the arm actually ran from: BaseSHA with the
	// withheld oracle removed and that removal committed, because a governed
	// run refuses to start in a dirty checkout. Recorded separately, because
	// reporting the pinned base as what ran would misstate which tree the
	// provider saw.
	RunBase string `json:"run_base_sha"`
	// ExecutionOrder is where this arm fell in the per-task randomised order,
	// recorded so provider service drift cannot silently favour one arm.
	ExecutionOrder int `json:"execution_order"`

	// Providers maps role -> provider/model identity. A change inside a paired
	// comparison invalidates the pair rather than being quietly combined.
	Providers map[string]string `json:"providers"`

	Started  string `json:"started"`
	Ended    string `json:"ended"`
	WallSecs int    `json:"wall_seconds"`

	// Terminal is the workflow's own outcome; Verdict is the oracle's.
	// Deliberately separate: the loop completing and the code being right are
	// different claims, and collapsing them is how a benchmark flatters itself.
	Terminal string  `json:"terminal_status"`
	Verdict  Verdict `json:"oracle_verdict"`
	// OracleDetail is what the oracle actually reported.
	OracleDetail string `json:"oracle_detail"`

	Interventions []Intervention `json:"interventions"`
	Objections    []Objection    `json:"objections"`
	ReviewCycles  int            `json:"review_cycles"`
	Observations  int            `json:"observation_rounds"`

	// GapsEncountered and GapsClosed feed closure yield.
	GapsEncountered int `json:"gaps_encountered"`
	GapsClosed      int `json:"gaps_closed_autonomously"`
	// Rediscovered counts facts this run established that an earlier benchmark
	// task had already established.
	Rediscovered int `json:"rediscovered"`
	// KnowledgeReused is durable artifacts consumed AND shown to change
	// structured behavior. Retrieval into a prompt does not count.
	KnowledgeReused []string `json:"knowledge_reused"`
	// KnowledgeCreated is durable artifacts this run produced.
	KnowledgeCreated []string `json:"knowledge_created"`
	// BehaviorChanges are the machine-observable decisions prior knowledge
	// changed: route, required investigation, forbidden move, verification.
	BehaviorChanges []string `json:"behavior_changes"`

	DiffHash string `json:"candidate_diff_hash"`
	// CandidateDir is the tree the oracle actually judged, and CandidateMethod
	// is how it was resolved.
	//
	// Recorded because proof-v5's COLD wave scored eleven arms on a directory
	// that never received their work. A verdict that cannot name the tree it
	// describes is not attributable, and CheckAttributionAnomaly refuses a wave
	// whose governed attempts leave these blank.
	CandidateDir    string `json:"candidate_dir,omitempty"`
	CandidateMethod string `json:"candidate_method,omitempty"`
	CandidateHead   string `json:"candidate_head_sha,omitempty"`
	// GovernedCheckoutClean records whether the governed checkout was mutated
	// outside the allowed candidate boundary.
	GovernedCheckoutClean bool   `json:"governed_checkout_clean"`
	GovernedCheckoutState string `json:"governed_checkout_state"`
	// BoundaryMeasurable says whether that reading means anything.
	//
	// The governed checkout was dirty when this arm started, so a before/after
	// comparison cannot separate a change the ARM made from one the operator
	// made while it ran. The first real campaign run tripped exactly this: the
	// author committed harness fixes during a 25-minute arm and the run was
	// recorded as having mutated the governed repository.
	//
	// An unmeasurable boundary is not a clean one and is not a violation
	// either. It is absent evidence, and G3 counts only measurable readings
	// while the report says how many were not.
	BoundaryMeasurable bool `json:"boundary_measurable"`

	// CostUSD is a pointer so missing data stays missing. Zero is a
	// measurement; nil is the absence of one, and writing 0.0 for "the provider
	// did not tell us" would make the cost comparison a fiction.
	CostUSD *float64 `json:"cost_usd"`
	Tokens  *int     `json:"tokens"`

	// Infrastructure records an externally attributable failure -- provider
	// outage, quota, auth. It is the ONLY thing that licenses a retry.
	Infrastructure string `json:"infrastructure_failure,omitempty"`
	// Artifacts binds this summary to its supporting receipts by hash.
	Artifacts map[string]string `json:"artifacts"`
	// Notes is free text for a human reader. Nothing scores from it.
	Notes string `json:"notes,omitempty"`
	// MeasurementStatus marks an attempt the harness itself invalidated.
	//
	// Set on the proof-v5 COLD wave, which was scored against the wrong
	// directory. Such an attempt is preserved untouched -- it is evidence about
	// the instrument -- and must never enter a benchmark result. Attempts
	// carrying it are excluded from every rate and listed in the report.
	MeasurementStatus string `json:"measurement_status,omitempty"`
}

// ID is the attempt's identity, and it is what makes the ledger append-only.
func (a Attempt) ID() string {
	return fmt.Sprintf("%s/%s/%d", a.Task, a.Arm, a.Number)
}

// TechnicalAnswers counts human interventions that supplied the answer.
func (a Attempt) TechnicalAnswers() int {
	n := 0
	for _, i := range a.Interventions {
		if i.TechnicalAnswer() {
			n++
		}
	}
	return n
}

// AutonomousCorrect is C1: correct, and nobody was told the answer.
//
// All four conditions, and the last two are the ones a lazy scorer drops.
func (a Attempt) AutonomousCorrect() bool {
	if a.Verdict != Correct || a.TechnicalAnswers() != 0 {
		return false
	}
	// An unmeasurable boundary cannot establish the fourth condition, so it
	// cannot support the autonomy claim either. Fail closed: absent evidence is
	// not evidence of a clean boundary.
	return a.BoundaryMeasurable && a.GovernedCheckoutClean
}

// BoundaryViolation reports a MEASURED mutation of the governed checkout.
//
// False unless the reading means something. An unmeasurable boundary is
// reported separately rather than counted as a violation, because condemning
// the product on evidence the harness could not collect is the mirror image of
// flattering it.
func (a Attempt) BoundaryViolation() bool {
	return a.BoundaryMeasurable && !a.GovernedCheckoutClean
}

// Eligible reports whether this attempt counts toward correctness rates.
//
// NO_RESULT does not: it is an operational-reliability fact, and folding it
// into a correctness denominator would let infrastructure flakiness read as
// incorrectness, or -- worse, by exclusion from the report entirely -- let a
// campaign quietly shrink until it passed.
func (a Attempt) Eligible() bool {
	return a.Verdict != NoResult && strings.TrimSpace(a.MeasurementStatus) == ""
}

// Void reports an attempt the harness invalidated. It stays in the ledger and
// out of every number.
func (a Attempt) Void() bool { return strings.TrimSpace(a.MeasurementStatus) != "" }

// Ledger is every attempt recorded for a benchmark version.
type Ledger struct {
	Dir      string
	attempts map[string]Attempt
}

// OpenLedger reads every committed attempt under dir.
func OpenLedger(dir string) (*Ledger, error) {
	l := &Ledger{Dir: dir, attempts: map[string]Attempt{}}
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return l, nil
	}
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".json") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var a Attempt
		if err := json.Unmarshal(b, &a); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		if _, dup := l.attempts[a.ID()]; dup {
			return fmt.Errorf("%s: attempt %s is recorded twice; the ledger is append-only and "+
				"an id identifies exactly one attempt", path, a.ID())
		}
		l.attempts[a.ID()] = a
		return nil
	})
	return l, err
}

// Append writes one attempt, and refuses to overwrite one.
//
// This is the integrity rule that matters most. A semantic failure cannot be
// replaced by a later success under the same id: the retry must be attempt N+1,
// which means both appear in the report and the reader sees that it took two
// goes. Retry-until-green is not prevented by asking people not to do it.
func (l *Ledger) Append(a Attempt) error {
	if err := a.checkRecordable(); err != nil {
		return err
	}
	if prior, exists := l.attempts[a.ID()]; exists {
		return fmt.Errorf("refusing to overwrite attempt %s (recorded %s, verdict %s). "+
			"The ledger is append-only: a retry is attempt %d, and both stay visible",
			a.ID(), prior.Started, prior.Verdict, a.Number+1)
	}
	path := filepath.Join(l.Dir, a.Task, string(a.Arm), fmt.Sprintf("%d.json", a.Number))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	// O_EXCL: even if the in-memory index is stale, the filesystem refuses.
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return fmt.Errorf("refusing to overwrite %s: %w", path, err)
	}
	defer f.Close()
	if _, err := f.Write(append(b, '\n')); err != nil {
		return err
	}
	l.attempts[a.ID()] = a
	return nil
}

// checkRecordable refuses an attempt that cannot be interpreted later.
func (a Attempt) checkRecordable() error {
	switch {
	case strings.TrimSpace(a.Task) == "":
		return fmt.Errorf("attempt names no task")
	case a.Arm != ArmRaw && a.Arm != ArmCold && a.Arm != ArmWarm:
		return fmt.Errorf("attempt has no recognised arm: %q", a.Arm)
	case a.Number < 1:
		return fmt.Errorf("attempt number must start at 1")
	case strings.TrimSpace(a.ManifestHash) == "":
		return fmt.Errorf("%s: no manifest hash; a result that cannot name the experiment it "+
			"ran under is not evidence about that experiment", a.ID())
	case a.Verdict == "":
		return fmt.Errorf("%s: no oracle verdict; use NO_RESULT rather than leaving it blank", a.ID())
	}
	return nil
}

// Has reports whether an attempt id is already committed.
//
// Meant to be checked BEFORE an arm runs. Append's refusal is correct but
// arrives after the provider time is spent, and the campaign burned a
// 25-minute governed arm and a 5-minute RAW arm learning an id was taken. A
// rule that protects evidence should not also waste the budget collecting it.
func (l *Ledger) Has(task string, arm Arm, number int) bool {
	_, ok := l.attempts[fmt.Sprintf("%s/%s/%d", task, arm, number)]
	return ok
}

// NextAttempt is the first unused attempt number for a task/arm.
func (l *Ledger) NextAttempt(task string, arm Arm) int {
	for n := 1; ; n++ {
		if !l.Has(task, arm, n) {
			return n
		}
	}
}

// Attempts returns every recorded attempt, ordered for deterministic reporting.
func (l *Ledger) Attempts() []Attempt {
	out := make([]Attempt, 0, len(l.attempts))
	for _, a := range l.attempts {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Task != out[j].Task {
			return out[i].Task < out[j].Task
		}
		if out[i].Arm != out[j].Arm {
			return out[i].Arm < out[j].Arm
		}
		return out[i].Number < out[j].Number
	})
	return out
}

// ForManifest is every attempt that ran under this exact manifest hash, plus
// the ones that did not.
//
// Orphans are returned rather than filtered away. A manifest edited after runs
// exist does not reinterpret them and does not silently lose them: the report
// says how many results belong to a different experiment.
func (l *Ledger) ForManifest(hash string) (current, orphaned []Attempt) {
	for _, a := range l.Attempts() {
		if a.ManifestHash == hash {
			current = append(current, a)
		} else {
			orphaned = append(orphaned, a)
		}
	}
	return current, orphaned
}

// Scored is the attempt that counts for a task/arm.
//
// The LAST attempt, and only when every earlier one failed for an externally
// attributable infrastructure reason. A semantic failure followed by a success
// scores as the semantic failure -- otherwise "one retry for infrastructure"
// becomes the doorway retry-until-green walks through.
func Scored(attempts []Attempt) (Attempt, bool) {
	if len(attempts) == 0 {
		return Attempt{}, false
	}
	sort.Slice(attempts, func(i, j int) bool { return attempts[i].Number < attempts[j].Number })
	for i, a := range attempts {
		if i == len(attempts)-1 {
			return a, true
		}
		if strings.TrimSpace(a.Infrastructure) == "" {
			// A semantic result. It stands, whatever came after it.
			return a, true
		}
	}
	return attempts[len(attempts)-1], true
}

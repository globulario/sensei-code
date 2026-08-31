// Package taskstate keeps what a task means, so changing worker does not
// restart the thinking.
//
// Handing over a conversation transcript is not continuity. The next worker
// does not need to know what was said; it needs to know what is true: which
// task this is, which commit it is based on, what contract was agreed, which
// questions a human has already settled, what the candidate currently contains,
// which tests are required, and what the last reviewer objected to that nobody
// has fixed yet. Those are facts with a shape, and a paragraph is a poor
// container for them — a worker reading prose has to re-derive the state, and
// it will re-derive it slightly differently, which is exactly how the second
// worker undoes the first one's fix.
//
// One thing this deliberately does not carry: authority. The state records what
// Sensei said and which graph generation said it, never a grant that could
// stand in for asking. If this file is lost, the correct outcome is that the
// next run re-certifies from Sensei and possibly refuses — not that it proceeds
// on a remembered yes. Local session loss must never be able to manufacture
// authority that Sensei did not give, so nothing here is shaped like permission.
package taskstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Phase is where a task had got to.
type Phase string

const (
	Planning     Phase = "planning"
	Implementing Phase = "implementing"
	Reviewing    Phase = "reviewing"
	Revising     Phase = "revising"
	Accepted     Phase = "accepted"
	Blocked      Phase = "blocked"
)

// Contract is the architectural agreement the work is bounded by.
type Contract struct {
	Rationale    string   `json:"rationale,omitempty"`
	Plan         string   `json:"plan,omitempty"`
	Steps        []string `json:"steps,omitempty"`
	Consequences string   `json:"consequences,omitempty"`
	Files        []string `json:"files,omitempty"`
	Invariants   []string `json:"invariants,omitempty"`
}

// AuthorityDecision is a question a human has already answered, kept so the
// next worker does not reopen it.
//
// Durable records whether Sensei holds it. A decision that is not durable is
// still binding on this task — the human said it — but it is not project
// knowledge, and saying which is which prevents a worker from assuming the
// question is permanently settled.
type AuthorityDecision struct {
	Question  string    `json:"question"`
	Condition string    `json:"condition,omitempty"`
	Chosen    string    `json:"chosen"`
	Durable   bool      `json:"durable"`
	DecidedAt time.Time `json:"decided_at"`
}

// Evidence is what the candidate currently contains and what was said about it.
type Evidence struct {
	DiffBytes int `json:"diff_bytes"`
	// ReportBytes is the size of a read-only run's findings. It is separate
	// from DiffBytes because they are different claims: one says work was
	// produced, the other says the repository was left alone deliberately.
	ReportBytes   int      `json:"report_bytes,omitempty"`
	ChangedPaths  []string `json:"changed_paths,omitempty"`
	AuditVerdict  string   `json:"audit_verdict,omitempty"`
	AuditDetail   string   `json:"audit_detail,omitempty"`
	RequiredTests []string `json:"required_tests,omitempty"`
}

// Finding is something a reviewer or an audit raised that is still open.
type Finding struct {
	Source string `json:"source"`
	Detail string `json:"detail"`
}

// State is the whole semantic position of one task.
type State struct {
	Version   int    `json:"version"`
	TaskID    string `json:"task_id"`
	SessionID string `json:"session_id"`
	Task      string `json:"task"`
	Domain    string `json:"domain,omitempty"`

	// BaseSHA, Worktree and Branch are the candidate identity. They are copied
	// rather than referenced so a handover is readable on its own.
	BaseSHA  string `json:"base_sha,omitempty"`
	Worktree string `json:"worktree,omitempty"`
	Branch   string `json:"branch,omitempty"`

	Contract  Contract            `json:"contract"`
	Authority []AuthorityDecision `json:"authority,omitempty"`
	Evidence  Evidence            `json:"evidence"`
	Open      []Finding           `json:"open_findings,omitempty"`
	Phase     Phase               `json:"phase"`

	// Observations are the independent run dimensions, append-only. See the
	// run-dimension section below: each carries what produced it and which
	// candidate it describes, and none of them is derived from another.
	Observations []Observation `json:"observations,omitempty"`

	// Workers is who has already worked on this task, in order.
	Workers []string `json:"workers,omitempty"`

	// GraphBuildCommit is the graph generation the Sensei facts above were read
	// at, and ObservedAt is when. Together they are what makes staleness
	// detectable instead of assumed.
	GraphBuildCommit string    `json:"graph_build_commit,omitempty"`
	ObservedAt       time.Time `json:"observed_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Version is bumped when the shape changes incompatibly. Version 2 added the
// run-dimension observations; a version 1 state is projected into it on read.
const Version = 2

func path(repoRoot, taskID string) string {
	name := strings.TrimSpace(taskID)
	if name == "" {
		name = "default"
	}
	return filepath.Join(repoRoot, ".sensei-code", "tasks", filepath.Base(name)+".json")
}

// Save writes the state.
func (s State) Save(repoRoot string) error {
	s.Version = Version
	s.UpdatedAt = time.Now().UTC()
	target := path(repoRoot, s.TaskID)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(body, '\n'), 0o644)
}

// Load reads the state for a task.
func Load(repoRoot, taskID string) (State, bool, error) {
	body, err := os.ReadFile(path(repoRoot, taskID))
	if os.IsNotExist(err) {
		return State{}, false, nil
	}
	if err != nil {
		return State{}, false, err
	}
	var s State
	if err := json.Unmarshal(body, &s); err != nil {
		return State{}, false, fmt.Errorf("task state for %s is unreadable: %w", taskID, err)
	}
	// Exactly version 1 is upgraded, and only in memory. Any other version --
	// including one from a future build -- is refused rather than guessed at:
	// an unrecognized shape is not an old shape, and reading must never write.
	switch s.Version {
	case Version:
		return s, true, nil
	case 1:
		return upgradeFromV1(s), true, nil
	default:
		return State{}, false, fmt.Errorf(
			"task state for %s is version %d, which this build does not support (it understands %d, and upgrades 1)",
			taskID, s.Version, Version)
	}
}

// RecordWorker appends a worker, keeping the order and avoiding a duplicate
// entry when the same worker runs several cycles.
func (s *State) RecordWorker(name string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	if n := len(s.Workers); n > 0 && s.Workers[n-1] == name {
		return
	}
	s.Workers = append(s.Workers, name)
}

// Stale reports whether the Sensei facts in this state were read at a different
// graph generation than the one now in force.
//
// A state whose graph has moved is not wrong, it is unverified, and the
// difference matters: the correct response is to refresh it, not to discard the
// work or to carry on pretending. An unknown current generation counts as stale,
// because "I could not check" is not "it is fine".
func (s State) Stale(currentGraphBuildCommit string) bool {
	current := strings.TrimSpace(currentGraphBuildCommit)
	if current == "" || strings.TrimSpace(s.GraphBuildCommit) == "" {
		return true
	}
	return current != s.GraphBuildCommit
}

// Refresh rebinds the state to the graph generation now in force.
func (s State) Refresh(graphBuildCommit string, now time.Time) State {
	s.GraphBuildCommit = strings.TrimSpace(graphBuildCommit)
	s.ObservedAt = now.UTC()
	return s
}

// Handover renders the state for the next worker.
//
// It is written as facts rather than narrative, and it says explicitly what is
// still open, because the failure this replaces is a worker that reads a
// friendly summary, concludes the work is nearly done, and re-solves a problem
// the previous one had already solved differently.
func (s State) Handover(previousWorker string, currentGraphBuildCommit string) string {
	var b strings.Builder
	b.WriteString("CONTINUING AN EXISTING TASK. This is not a fresh start.\n\n")

	if previousWorker != "" {
		b.WriteString(fmt.Sprintf("A previous worker (%s) already worked in this candidate. Its changes are present.\n", previousWorker))
		b.WriteString("Continue from them. Do not start over, and do not re-solve what is already solved.\n\n")
	}

	b.WriteString("TASK IDENTITY\n")
	b.WriteString("  task: " + s.TaskID + "\n")
	b.WriteString("  request: " + s.Task + "\n")
	if s.Domain != "" {
		b.WriteString("  domain: " + s.Domain + "\n")
	}
	if s.BaseSHA != "" {
		b.WriteString("  base commit: " + s.BaseSHA + "\n")
	}
	if s.Branch != "" {
		b.WriteString("  candidate branch: " + s.Branch + "\n")
	}
	b.WriteString("  phase reached: " + string(s.Phase) + "\n")
	if len(s.Workers) != 0 {
		b.WriteString("  workers so far: " + strings.Join(s.Workers, " → ") + "\n")
	}

	b.WriteString("\nARCHITECTURAL CONTRACT (the bound you may not widen)\n")
	if r := strings.TrimSpace(s.Contract.Rationale); r != "" {
		b.WriteString("  why: " + r + "\n")
	}
	for i, step := range s.Contract.Steps {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, step))
	}
	if len(s.Contract.Files) != 0 {
		b.WriteString("  files in scope: " + strings.Join(s.Contract.Files, ", ") + "\n")
	}
	if len(s.Contract.Invariants) != 0 {
		b.WriteString("  governed by: " + strings.Join(s.Contract.Invariants, ", ") + "\n")
	}
	if c := strings.TrimSpace(s.Contract.Consequences); c != "" {
		b.WriteString("  consequences: " + c + "\n")
	}

	if len(s.Authority) != 0 {
		b.WriteString("\nDECISIONS A HUMAN HAS ALREADY MADE (do not reopen these)\n")
		for _, d := range s.Authority {
			line := "  - " + d.Question + " → " + d.Chosen
			if !d.Durable {
				line += "  [binding on this task; not yet project knowledge]"
			}
			b.WriteString(line + "\n")
		}
	}

	b.WriteString("\nCURRENT EVIDENCE\n")
	if s.Evidence.DiffBytes > 0 {
		b.WriteString(fmt.Sprintf("  candidate diff: %d bytes across %d files\n", s.Evidence.DiffBytes, len(s.Evidence.ChangedPaths)))
		for _, p := range s.Evidence.ChangedPaths {
			b.WriteString("    " + p + "\n")
		}
	} else {
		b.WriteString("  candidate diff: nothing yet\n")
	}
	if s.Evidence.AuditVerdict != "" {
		b.WriteString("  last Sensei audit: " + s.Evidence.AuditVerdict)
		if s.Evidence.AuditDetail != "" {
			b.WriteString(" — " + s.Evidence.AuditDetail)
		}
		b.WriteString("\n")
	}
	if len(s.Evidence.RequiredTests) != 0 {
		b.WriteString("  tests that must pass: " + strings.Join(s.Evidence.RequiredTests, ", ") + "\n")
	}

	b.WriteString("\nSTILL OPEN (this is your work)\n")
	if len(s.Open) == 0 {
		b.WriteString("  nothing recorded as open — re-read the contract and the last audit before assuming it is done\n")
	}
	for _, f := range s.Open {
		b.WriteString("  - [" + f.Source + "] " + f.Detail + "\n")
	}

	if s.Stale(currentGraphBuildCommit) {
		b.WriteString("\nCONTEXT FRESHNESS\n")
		b.WriteString("  The Sensei facts above were read at a different graph generation than the one now in force")
		if s.GraphBuildCommit != "" && currentGraphBuildCommit != "" {
			b.WriteString(fmt.Sprintf(" (%s, now %s)", s.GraphBuildCommit, currentGraphBuildCommit))
		}
		b.WriteString(".\n  Re-read Sensei for anything architectural before relying on it.\n")
	}

	b.WriteString("\nThis handover carries no authority. If a step needs a decision, ask for it;\n")
	b.WriteString("nothing above grants permission that Sensei has not given.\n")
	return b.String()
}

// OpenFindings replaces the open list, sorted for a stable handover so two
// identical states render identically.
func (s *State) OpenFindings(findings []Finding) {
	sort.SliceStable(findings, func(i, j int) bool {
		if findings[i].Source != findings[j].Source {
			return findings[i].Source < findings[j].Source
		}
		return findings[i].Detail < findings[j].Detail
	})
	s.Open = findings
}

// ---------------------------------------------------------------------------
// Run dimensions.
//
// A run observes six things, and they are independent facts about different
// questions. Collapsing them is how a terminal report comes to say something no
// stage established: a reviewer's verdict is not an audit's certification, an
// unreachable evaluator is not a defect in a candidate, and scope compliance is
// never correctness. Each is recorded separately, with what produced it and the
// candidate it describes, and disagreement between them is preserved rather
// than resolved here.
//
// Every vocabulary is closed and read BY MEMBERSHIP: a value this build does
// not know becomes the unobserved member and keeps the original in Detail, so
// a future value cannot silently read as a present one. And in each vocabulary
// the unobserved member is the ZERO VALUE, because the failure this exists to
// prevent is an absence that reads as a pass.
// ---------------------------------------------------------------------------

// Dimension names one of the six independent things a run observes.
type Dimension string

const (
	DimImplementation Dimension = "implementation"
	DimReview         Dimension = "review"
	DimAudit          Dimension = "audit"
	DimEvaluator      Dimension = "evaluator"
	DimScope          Dimension = "scope"
	DimAdmission      Dimension = "admission"
)

// Known reports whether this is one of the six dimensions this build defines.
// The set is closed and is read by membership: a name from a future version, or
// a mistake, is not a dimension here, and nothing may project it as one.
func (d Dimension) Known() bool {
	_, ok := vocabularies[d]
	return ok
}

// RunScoped reports whether a dimension describes the run rather than a
// candidate. Evaluator availability is a fact about the environment during the
// run; the rest are claims about a specific candidate and are selected by its
// identity.
func (d Dimension) RunScoped() bool { return d == DimEvaluator }

// ImplementationState is what a worker's cycle produced.
//
// UNCHANGED and REFUSED_TO_EDIT are deliberately separate. "The candidate did
// not change" and "the worker declined to edit, and said why" are different
// facts, and reading the first where the second was true is how a principled
// refusal gets reported as an implementor that ignored its feedback. Neither
// value alone decides convergence: unchanged after "no edit was required"
// differs from unchanged after a fixable finding was ignored, and that decision
// needs the requested action and the reviewer and audit states too.
type ImplementationState string

const (
	ImplementationUnobserved ImplementationState = "UNOBSERVED"
	ImplementationConverged  ImplementationState = "CONVERGED"
	ImplementationUnchanged  ImplementationState = "UNCHANGED"
	ImplementationRefused    ImplementationState = "REFUSED_TO_EDIT"
	ImplementationFailed     ImplementationState = "FAILED"
)

// ReviewVerdict is what an independent reviewer said. It is code-review
// approval and nothing else: it is NOT candidate acceptance, which additionally
// requires an audit that could be performed.
type ReviewVerdict string

const (
	ReviewUnobserved  ReviewVerdict = "UNOBSERVED"
	ReviewAccept      ReviewVerdict = "ACCEPT"
	ReviewRevise      ReviewVerdict = "REVISE"
	ReviewReject      ReviewVerdict = "REJECT"
	ReviewUnparseable ReviewVerdict = "UNPARSEABLE"
)

// AuditState is Sensei's certification of a diff. Every member is prefixed
// AUDIT_ so it cannot be mistaken at a glance for a reviewer verdict, and the
// two are distinct Go types so the confusion does not compile.
//
// AUDIT_REVIEW_REQUIRED names the audit's own "review" decision. It is not
// ReviewAccept. AUDIT_CANNOT_VERIFY explains certification only: it says
// nothing about the candidate, the implementor, or the evaluator.
type AuditState string

const (
	AuditUnobserved     AuditState = "UNOBSERVED"
	AuditPass           AuditState = "AUDIT_PASS"
	AuditReviewRequired AuditState = "AUDIT_REVIEW_REQUIRED"
	AuditBlock          AuditState = "AUDIT_BLOCK"
	AuditCannotVerify   AuditState = "AUDIT_CANNOT_VERIFY"
)

// EvaluatorState is what this process witnessed about reaching Sensei. It is
// never derived from an audit verdict: an audit that could not be performed
// does not say why, and an evaluator that answered does not certify anything.
type EvaluatorState string

const (
	EvaluatorUnobserved  EvaluatorState = "UNOBSERVED"
	EvaluatorAvailable   EvaluatorState = "AVAILABLE"
	EvaluatorUnreachable EvaluatorState = "UNREACHABLE"
	EvaluatorRefused     EvaluatorState = "REFUSED"
)

// ScopeState is whether the candidate stayed inside its admitted envelope.
// Compliance is not correctness certification and never implies it.
type ScopeState string

const (
	ScopeUnobserved ScopeState = "UNOBSERVED"
	ScopeCompliant  ScopeState = "COMPLIANT"
	ScopeViolated   ScopeState = "VIOLATED"
	ScopeStale      ScopeState = "STALE_BINDING"
)

// AdmissionState is whether Sensei established admission. DEFERRED is not
// REFUSED: an admission that could not be established because the evaluator was
// unreachable is a different fact from one Sensei declined.
type AdmissionState string

const (
	AdmissionUnobserved   AdmissionState = "UNOBSERVED"
	AdmissionAdmitted     AdmissionState = "ADMITTED"
	AdmissionDeferred     AdmissionState = "DEFERRED"
	AdmissionRefused      AdmissionState = "REFUSED"
	AdmissionNotRequested AdmissionState = "NOT_REQUESTED"
)

// vocabularies maps each dimension to its closed set. Membership is read from
// here, so adding a member in one place cannot leave a reader accepting it and
// another rejecting it.
var vocabularies = map[Dimension]map[string]bool{
	DimImplementation: {
		string(ImplementationUnobserved): true, string(ImplementationConverged): true,
		string(ImplementationUnchanged): true, string(ImplementationRefused): true,
		string(ImplementationFailed): true,
	},
	DimReview: {
		string(ReviewUnobserved): true, string(ReviewAccept): true, string(ReviewRevise): true,
		string(ReviewReject): true, string(ReviewUnparseable): true,
	},
	DimAudit: {
		string(AuditUnobserved): true, string(AuditPass): true, string(AuditReviewRequired): true,
		string(AuditBlock): true, string(AuditCannotVerify): true,
	},
	DimEvaluator: {
		string(EvaluatorUnobserved): true, string(EvaluatorAvailable): true,
		string(EvaluatorUnreachable): true, string(EvaluatorRefused): true,
	},
	DimScope: {
		string(ScopeUnobserved): true, string(ScopeCompliant): true,
		string(ScopeViolated): true, string(ScopeStale): true,
	},
	DimAdmission: {
		string(AdmissionUnobserved): true, string(AdmissionAdmitted): true,
		string(AdmissionDeferred): true, string(AdmissionRefused): true,
		string(AdmissionNotRequested): true,
	},
}

// unobserved is the zero member of each vocabulary. Every dimension has one,
// and it is what an unknown value, a missing measurement, or a legacy record
// without the field becomes.
const unobserved = "UNOBSERVED"

// CandidateIdentity is what a candidate IS, which is its content and the base
// it was derived from — never where it happens to live.
//
// A candidate can be valid and uncommitted, and a preservation commit changes
// custody without changing content. So identity is the bound base plus the
// normalized diff digest; a commit SHA is recorded as provenance and is never
// compared.
type CandidateIdentity struct {
	BaseSHA    string `json:"base_sha,omitempty"`
	DiffDigest string `json:"diff_digest,omitempty"`
}

// Known reports whether this identifies a candidate at all. An observation
// carrying an unknown identity may be recorded, but it can never be selected as
// a current claim ABOUT a candidate.
func (c CandidateIdentity) Known() bool {
	return strings.TrimSpace(c.BaseSHA) != "" && strings.TrimSpace(c.DiffDigest) != ""
}

// Equal compares content, not custody.
func (c CandidateIdentity) Equal(other CandidateIdentity) bool {
	return c.Known() && other.Known() &&
		strings.TrimSpace(c.BaseSHA) == strings.TrimSpace(other.BaseSHA) &&
		strings.TrimSpace(c.DiffDigest) == strings.TrimSpace(other.DiffDigest)
}

// Observation is one recorded fact about one dimension, together with what
// produced it, how it was measured, and which candidate it describes.
//
// Producer and Source are required. A value without them is an assertion
// wearing a measurement's clothes, so it is stored as unobserved with the
// omission named rather than accepted as knowledge.
type Observation struct {
	Dimension Dimension         `json:"dimension"`
	Value     string            `json:"value"`
	Candidate CandidateIdentity `json:"candidate,omitempty"`
	// CommitSHA is supplementary provenance. It is never a selector.
	CommitSHA string `json:"commit_sha,omitempty"`
	Detail    string `json:"detail,omitempty"`
	// Overrode records that a reviewer accepted while the audit refused. It is
	// the interesting event and it must survive serialization: a system that
	// resolves that disagreement silently teaches nobody anything.
	Overrode   bool      `json:"overrode,omitempty"`
	Producer   string    `json:"producer"`
	Source     string    `json:"source"`
	Lane       string    `json:"lane,omitempty"`
	Cycle      int       `json:"cycle,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
}

// normalize applies the two refusals this model exists for: an unsourced
// observation and an unrecognized value both become unobserved, and both say
// so rather than disappearing.
func (o Observation) normalize() Observation {
	if strings.TrimSpace(o.Producer) == "" || strings.TrimSpace(o.Source) == "" {
		o.Detail = strings.TrimSpace("unsourced observation of " + string(o.Dimension) +
			" recording " + strconv.Quote(o.Value) + ": " + o.Detail)
		o.Value = unobserved
		return o
	}
	members, known := vocabularies[o.Dimension]
	if !known {
		// The record keeps it -- something was observed and saying so is the
		// point -- but an unrecognized dimension is not one of the six, so no
		// governed projection may present it. See Current and Historical.
		o.Detail = strings.TrimSpace("unrecognized dimension " + strconv.Quote(string(o.Dimension)) + ": " + o.Detail)
		o.Value = unobserved
		o.Overrode = false
		return o
	}
	// Overrode means one thing: a reviewer accepted while the audit refused, so
	// admission could not be established. A free boolean on any dimension would
	// let a scope or evaluator observation assert a disagreement it does not
	// represent, so it is legal only where it has that meaning.
	if o.Overrode && o.Dimension != DimAdmission {
		o.Detail = strings.TrimSpace("overrode is meaningful only on " + string(DimAdmission) +
			"; dropped from a " + string(o.Dimension) + " observation: " + o.Detail)
		o.Overrode = false
	}
	if !members[o.Value] {
		o.Detail = strings.TrimSpace("unrecognized value " + strconv.Quote(o.Value) +
			" for dimension " + string(o.Dimension) + ": " + o.Detail)
		o.Value = unobserved
	}
	return o
}

// Record appends an observation. Observations are append-only: a later
// candidate makes an earlier one HISTORICAL, never false, and nothing here
// deletes or rewrites what was seen.
func (s *State) Record(o Observation) {
	if o.ObservedAt.IsZero() {
		o.ObservedAt = time.Now().UTC()
	}
	o.ObservedAt = o.ObservedAt.UTC()
	s.Observations = append(s.Observations, o.normalize())
}

// Current selects, for each dimension, the latest observation that describes
// the given candidate.
//
// A dimension the run never observed is absent from the result, which reads as
// unobserved; it is never filled in with a default, and a default is never a
// success. Run-scoped dimensions select their latest observation regardless of
// candidate. Everything else requires the candidate identity to MATCH: an
// observation about an earlier candidate stays in the record as history and
// cannot describe this one, and an observation carrying no identity can never
// become a current claim about a candidate.
func (s State) Current(candidate CandidateIdentity) map[Dimension]Observation {
	current := map[Dimension]Observation{}
	for _, o := range s.Observations {
		if !o.Dimension.Known() {
			continue
		}
		switch {
		case o.Dimension.RunScoped():
		case !candidate.Known() || !o.Candidate.Equal(candidate):
			continue
		}
		if held, ok := current[o.Dimension]; ok && held.ObservedAt.After(o.ObservedAt) {
			continue
		}
		current[o.Dimension] = o
	}
	return current
}

// Historical returns the observations that describe some candidate other than
// the one given. They are kept because an earlier candidate's verdict remains
// true of that candidate; it simply is not a claim about this one.
func (s State) Historical(candidate CandidateIdentity) []Observation {
	var out []Observation
	for _, o := range s.Observations {
		if !o.Dimension.Known() || o.Dimension.RunScoped() {
			continue
		}
		if o.Candidate.Known() && !o.Candidate.Equal(candidate) {
			out = append(out, o)
		}
	}
	return out
}

// legacyAuditVerdicts maps the exact strings Evidence.AuditVerdict has held to
// their dimension value. The strings are Sensei's audit decisions, copied
// rather than imported so this package keeps no dependency on that one; any
// string not listed here is not translated.
var legacyAuditVerdicts = map[string]AuditState{
	"pass":          AuditPass,
	"review":        AuditReviewRequired,
	"block":         AuditBlock,
	"cannot_verify": AuditCannotVerify,
}

// upgradeFromV1 projects a version 1 state into this version IN MEMORY.
//
// It is a read-time projection and it writes nothing: inspecting an old session
// must not rewrite it. The projection is narrow on purpose. Only an exactly
// recognized legacy audit verdict becomes an audit observation, and it is
// stamped as migrated so it is never mistaken for something this version
// observed. Anything else becomes unobserved and KEEPS THE ORIGINAL STRING, so
// the fact that something was there is not lost by failing to understand it.
//
// Nothing is inferred from prose and nothing defaults to success.
func upgradeFromV1(s State) State {
	s.Version = Version
	verdict := strings.TrimSpace(s.Evidence.AuditVerdict)
	if verdict == "" {
		return s
	}
	o := Observation{
		Dimension:  DimAudit,
		Producer:   "migration:v1",
		Source:     "taskstate.v1 Evidence.AuditVerdict",
		ObservedAt: s.UpdatedAt.UTC(),
	}
	if mapped, ok := legacyAuditVerdicts[verdict]; ok {
		o.Value = string(mapped)
	} else {
		o.Value = unobserved
		o.Detail = "v1 Evidence.AuditVerdict = " + strconv.Quote(verdict)
	}
	s.Observations = append(s.Observations, o)
	return s
}

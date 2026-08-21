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

	// Workers is who has already worked on this task, in order.
	Workers []string `json:"workers,omitempty"`

	// GraphBuildCommit is the graph generation the Sensei facts above were read
	// at, and ObservedAt is when. Together they are what makes staleness
	// detectable instead of assumed.
	GraphBuildCommit string    `json:"graph_build_commit,omitempty"`
	ObservedAt       time.Time `json:"observed_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// Version is bumped when the shape changes incompatibly.
const Version = 1

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
	if s.Version != Version {
		return State{}, false, fmt.Errorf("task state for %s is version %d, this build understands %d", taskID, s.Version, Version)
	}
	return s, true, nil
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

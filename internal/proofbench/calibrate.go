package proofbench

// Calibration: can this instrument record a known win and a known failure?
//
// Before any primary number means anything, the harness has to be shown capable
// of observing the two outcomes this project already knows it produced. A
// benchmark that cannot reproduce a result you already have is not measuring
// the thing you think it is -- and would report its blindness as a clean sheet.
//
// Neither specimen counts toward the primary n.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// SessionEvent is the subset of a recorded event this package reads.
//
// Read-only, and deliberately a separate struct from internal/event.Event: the
// harness consumes the event stream as evidence and must not acquire a
// compile-time say in what the workflow emits.
type SessionEvent struct {
	Time    string          `json:"time"`
	TaskID  string          `json:"task_id"`
	Source  string          `json:"source"`
	Kind    string          `json:"kind"`
	Summary string          `json:"summary"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// LoadSession reads one recorded event stream.
func LoadSession(path string) ([]SessionEvent, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []SessionEvent
	for _, line := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e SessionEvent
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue // a truncated trailing line is not a reason to lose the stream
		}
		out = append(out, e)
	}
	return out, nil
}

// ConvergenceEvidence is the #76 endpoint, measured from a real stream.
//
// Reviewer identity per cycle is the measurement that mattered in #62: two
// individually correct rules -- the reviewer is never the author, an
// unconverged candidate hands off -- compose into a reviewer that rotates with
// the worker, so the standard moves under a worker trying to satisfy it.
type ConvergenceEvidence struct {
	Task            string   `json:"task"`
	Cycles          int      `json:"review_cycles"`
	Reports         int      `json:"inspection_reports"`
	Findings        int      `json:"review_findings"`
	ReviewerByCycle []string `json:"reviewer_by_cycle"`
	ReviewerRotated bool     `json:"reviewer_rotated_mid_task"`
	Terminal        string   `json:"terminal"`
	TerminalDetail  string   `json:"terminal_detail"`
	WallMinutes     float64  `json:"wall_minutes"`
	// Converged is false when the loop ended without an accepted candidate.
	Converged bool `json:"converged"`
	// OutsideAuthority records whether any objection was identified as
	// unresolvable inside the candidate. #62's blocking finding was: an
	// objection no revision could satisfy meant the loop could only exhaust
	// cycles.
	OutsideAuthority bool `json:"blocker_outside_candidate_authority"`
}

// reviewerRE pulls the reviewer's identity out of the role announcement.
var knownProviders = []string{"Codex", "Claude", "ChatGPT", "Antigravity", "Gemini"}

// MeasureConvergence extracts the #62 shape from a recorded task.
func MeasureConvergence(events []SessionEvent, taskID string) ConvergenceEvidence {
	e := ConvergenceEvidence{Task: taskID}
	var first, last string
	for _, ev := range events {
		if ev.TaskID != taskID {
			continue
		}
		if first == "" {
			first = ev.Time
		}
		last = ev.Time
		switch ev.Kind {
		case "inspection.reported":
			e.Reports++
		case "review.started":
			e.Cycles++
		case "review.finding":
			e.Findings++
			low := strings.ToLower(ev.Summary)
			if strings.Contains(low, "blocking") || strings.Contains(low, "cannot be resolved") ||
				strings.Contains(low, "upstream") || strings.Contains(low, "premise is unresolved") {
				e.OutsideAuthority = true
			}
		case "agent.role.assigned":
			if !strings.Contains(strings.ToLower(ev.Summary), "reviewer") {
				continue
			}
			for _, p := range knownProviders {
				if strings.Contains(ev.Summary, p) {
					e.ReviewerByCycle = append(e.ReviewerByCycle, p)
					break
				}
			}
		case "workflow.completed":
			e.Terminal, e.TerminalDetail, e.Converged = ev.Kind, ev.Summary, true
		case "workflow.failed", "workflow.stopped":
			e.Terminal, e.TerminalDetail = ev.Kind, ev.Summary
		}
	}
	seen := map[string]bool{}
	for _, r := range e.ReviewerByCycle {
		seen[r] = true
	}
	e.ReviewerRotated = len(seen) > 1
	e.WallMinutes = minutesBetween(first, last)
	return e
}

func minutesBetween(a, b string) float64 {
	ta, err1 := time.Parse(time.RFC3339Nano, a)
	tb, err2 := time.Parse(time.RFC3339Nano, b)
	if err1 != nil || err2 != nil {
		return 0
	}
	return tb.Sub(ta).Minutes()
}

// CalibrateNonConvergence scores the known #62 failure.
//
// The instrument passes only if it observes the shape #62 documented: a
// multi-cycle review that did not converge, with the reviewer changing identity
// mid-task. Reproducing "it failed" is not enough -- a harness that recorded
// only the terminal status would report the same word for a quota error, and
// #62's whole finding was about WHY.
func CalibrateNonConvergence(ev ConvergenceEvidence) CalibrationResult {
	var missing []string
	if ev.Cycles < 3 {
		missing = append(missing, fmt.Sprintf("only %d review cycle(s) observed", ev.Cycles))
	}
	if ev.Converged {
		missing = append(missing, "the specimen converged; this is not the recorded failure")
	}
	if !ev.ReviewerRotated {
		missing = append(missing, fmt.Sprintf("reviewer identity did not rotate (%v); the moving "+
			"standard is the finding #62 recorded", ev.ReviewerByCycle))
	}
	if ev.Reports < 3 {
		missing = append(missing, fmt.Sprintf("only %d inspection report(s)", ev.Reports))
	}
	detail := fmt.Sprintf("%d cycles, %d reports, %d findings, reviewers %v, %.0f min, terminal %s",
		ev.Cycles, ev.Reports, ev.Findings, ev.ReviewerByCycle, ev.WallMinutes, ev.Terminal)
	if len(missing) != 0 {
		return CalibrationResult{Task: ev.Task, Expected: "negative / non_convergence",
			Observed: "shape not reproduced", OK: false,
			Detail: detail + " — missing: " + strings.Join(missing, "; ")}
	}
	return CalibrationResult{Task: ev.Task, Expected: "negative / non_convergence",
		Observed: "non-convergence with a rotating reviewer", OK: true, Detail: detail}
}

// SelfRepairEvidence is what survives of the #79/#80 positive specimen.
type SelfRepairEvidence struct {
	Commit       string `json:"commit"`
	Subject      string `json:"subject"`
	FilesChanged int    `json:"files_changed"`
	Insertions   int    `json:"insertions"`
	// TestsAdded records that the repair came with the tests that pin it.
	TestsAdded bool `json:"tests_added"`
	// StreamRetained says whether the run's own event stream still exists.
	StreamRetained bool `json:"event_stream_retained"`
	Note           string
}

// MeasureSelfRepair reads what the landed repair actually was.
func MeasureSelfRepair(repoRoot, commit string) (SelfRepairEvidence, error) {
	e := SelfRepairEvidence{Commit: commit}
	subj, err := exec.Command("git", "-C", repoRoot, "show", "-s", "--format=%s", commit).Output()
	if err != nil {
		return e, fmt.Errorf("commit %s is not in this repository: %w", commit, err)
	}
	e.Subject = strings.TrimSpace(string(subj))
	stat, err := exec.Command("git", "-C", repoRoot, "show", "--numstat", "--format=", commit).Output()
	if err != nil {
		return e, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(stat)), "\n") {
		f := strings.Fields(line)
		if len(f) != 3 {
			continue
		}
		e.FilesChanged++
		var add int
		fmt.Sscanf(f[0], "%d", &add)
		e.Insertions += add
		if strings.HasSuffix(f[2], "_test.go") {
			e.TestsAdded = true
		}
	}
	return e, nil
}

// CalibratePositive scores the known #79/#80 win.
//
// The honest limit is recorded rather than papered over. The run's own event
// stream was not retained -- governed runs execute in candidate worktrees whose
// session stores are discarded with them -- so this reconstruction rests on the
// landed artifact and the PR record, not on a replayed stream. That is weaker
// evidence than the #62 calibration, and the campaign says so instead of
// reporting two equally solid instrument checks.
func CalibratePositive(ev SelfRepairEvidence) CalibrationResult {
	var missing []string
	if ev.FilesChanged == 0 {
		missing = append(missing, "no landed diff found")
	}
	if !ev.TestsAdded {
		missing = append(missing, "the repair landed without the tests that pin it, which is not "+
			"the shape #79/#80 recorded")
	}
	detail := fmt.Sprintf("%s: %d file(s), %d insertion(s), tests=%v, event stream retained=%v",
		ev.Commit[:min(12, len(ev.Commit))], ev.FilesChanged, ev.Insertions, ev.TestsAdded, ev.StreamRetained)
	if ev.Note != "" {
		detail += " — " + ev.Note
	}
	if len(missing) != 0 {
		return CalibrationResult{Task: "self-repair-79-80", Expected: "positive",
			Observed: "shape not reproduced", OK: false, Detail: detail + " — missing: " + strings.Join(missing, "; ")}
	}
	return CalibrationResult{Task: "self-repair-79-80", Expected: "positive",
		Observed: "landed repair with its pinning tests", OK: true, Detail: detail}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// SessionTasks lists the task ids in a stream, most eventful first, so a
// calibration can be pointed at the right specimen without guessing.
func SessionTasks(events []SessionEvent) []string {
	n := map[string]int{}
	for _, e := range events {
		if e.TaskID != "" {
			n[e.TaskID]++
		}
	}
	out := make([]string, 0, len(n))
	for id := range n {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return n[out[i]] > n[out[j]] })
	return out
}

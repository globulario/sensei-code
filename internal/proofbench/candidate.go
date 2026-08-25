package proofbench

// Finding the tree a governed run actually produced.
//
// The eleventh instrument defect, and the most expensive: every COLD arm in
// proof-v5 recorded the empty-tree hash and was scored on code that was never
// where the harness looked. A governed run does its work in its OWN CANDIDATE
// WORKTREE, created by the engine beside the arm's checkout, and proofbench
// evaluated the arm checkout -- which never receives that work.
//
// One of those recorded INCORRECT candidates passes the frozen contract probe
// when the probe is run against the real candidate. The score was not wrong
// about the code; it was about a different directory.
//
// So attribution is now explicit and fails closed. The candidate is RESOLVED,
// the resolution METHOD is recorded, the path is bound to the attempt, and an
// arm whose candidate cannot be resolved is a measurement-integrity failure
// rather than a zero.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// EmptyTreeHash is what an empty diff hashes to.
//
// Named because it is an anomaly signal rather than a value: the proof-v5 COLD
// wave was caught by eleven supposedly independent runs all producing it, and
// nothing in the harness noticed. See CheckAttributionAnomaly.
const EmptyTreeHash = "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// Candidate is the tree an arm's work is in, and how that was established.
type Candidate struct {
	// Dir is the tree the oracle must judge.
	Dir string `json:"dir"`
	// Method says how it was resolved: "arm-checkout" for an arm that works in
	// place, "git-worktree-list" or "engine-layout" for a governed candidate.
	// Recorded so a reader can tell a bound attribution from an inferred one.
	Method string `json:"method"`
	// TaskID is the governed run's own task identity, cross-checked against the
	// resolved path so a stale or foreign worktree cannot be picked up.
	TaskID string `json:"task_id,omitempty"`
	// HeadSHA is the candidate's commit at evaluation time.
	HeadSHA string `json:"head_sha,omitempty"`
}

// ErrNoCandidate is a resolution that failed. It is an integrity failure, not a
// result: an arm whose work cannot be located has not been measured.
type ErrNoCandidate struct{ Why string }

func (e ErrNoCandidate) Error() string {
	return "candidate attribution failed: " + e.Why +
		" — the arm has not been measured, and scoring it would attribute a verdict to code " +
		"nobody located"
}

// taskIDFrom reads the governed run's task identity out of its own event
// stream.
//
// From the stream rather than from a name the harness invents, because the
// engine chooses the id and the whole point is to bind to what the run did.
func taskIDFrom(stream string) string {
	for _, line := range strings.Split(stream, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "{") {
			continue
		}
		var e SessionEvent
		if json.Unmarshal([]byte(line), &e) != nil {
			continue
		}
		if strings.TrimSpace(e.TaskID) != "" {
			return e.TaskID
		}
	}
	return ""
}

// ResolveCandidate finds the tree an arm's work is in.
//
// MUST be called before the arm checkout is removed: a governed candidate is
// registered in the arm's git metadata, and that registration is the strongest
// binding available. The engine-layout fallback exists for the case where git
// cannot answer, and is recorded as the weaker method it is.
//
// A RAW arm works in place, so its candidate is the arm checkout itself.
func (r Runner) ResolveCandidate(ctx context.Context, armDir string, arm Arm, stream string, delivered bool) (Candidate, error) {
	if arm == ArmRaw {
		return Candidate{Dir: armDir, Method: "arm-checkout"}, nil
	}
	// A run that never reached a candidate has nothing to attribute, and that
	// is a legitimate delivery failure rather than an integrity failure.
	//
	// The distinction, learned by getting it wrong: a governed run that refuses
	// at the start gate, or dies on an unreachable graph, never creates a
	// candidate worktree at all. Treating that as attribution failure halted a
	// wave over runs the product had correctly declined to start. Attribution
	// failure is for a run that DID deliver and whose work cannot be found.
	if !delivered {
		return Candidate{Method: "none-created", TaskID: taskIDFrom(stream)}, nil
	}
	taskID := taskIDFrom(stream)
	if taskID == "" {
		return Candidate{}, ErrNoCandidate{"the run's event stream carries no task id, so no " +
			"candidate can be bound to it"}
	}

	// Primary: ask git. The engine created the worktree from this checkout, so
	// git knows about it, and a registration is a fact rather than a guess.
	out, err := exec.CommandContext(ctx, "git", "-C", armDir,
		"--no-optional-locks", "worktree", "list", "--porcelain").Output()
	if err == nil {
		for _, block := range strings.Split(string(out), "\n") {
			if !strings.HasPrefix(block, "worktree ") {
				continue
			}
			path := strings.TrimSpace(strings.TrimPrefix(block, "worktree "))
			if path == armDir || !strings.Contains(path, clean(taskID)) {
				continue
			}
			if _, statErr := os.Stat(path); statErr != nil {
				continue
			}
			return Candidate{Dir: path, Method: "git-worktree-list", TaskID: taskID,
				HeadSHA: headOf(ctx, path)}, nil
		}
	}

	// Fallback: the layout the engine uses. Weaker, because it is a naming
	// convention rather than a registration, so it says so in Method.
	guess := filepath.Join(filepath.Dir(armDir), "."+filepath.Base(armDir)+"-worktrees", clean(taskID))
	if _, statErr := os.Stat(guess); statErr == nil {
		return Candidate{Dir: guess, Method: "engine-layout", TaskID: taskID,
			HeadSHA: headOf(ctx, guess)}, nil
	}

	return Candidate{}, ErrNoCandidate{fmt.Sprintf(
		"no candidate worktree for task %s is registered in %s or present at %s", taskID, armDir, guess)}
}

// clean mirrors the engine's task-id sanitisation, so a resolved path matches
// the one the engine created.
func clean(taskID string) string {
	var b strings.Builder
	for _, r := range taskID {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}

func headOf(ctx context.Context, dir string) string {
	out, err := exec.CommandContext(ctx, "git", "-C", dir,
		"--no-optional-locks", "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// CandidateDiffHash is the work in a candidate tree, hashed.
//
// Computed from the resolved candidate and nowhere else. A governed candidate
// may have committed its work, so the diff is taken against the arm's base
// rather than against the working tree alone -- the proof-v5 wave hashed an
// unchanged working tree and got the empty hash for a candidate that had
// committed real work.
func CandidateDiffHash(ctx context.Context, c Candidate, baseSHA string) string {
	dir := c.Dir
	_ = exec.CommandContext(ctx, "git", "-C", dir, "add", "--intent-to-add", "--", ".").Run()
	var out []byte
	if strings.TrimSpace(baseSHA) != "" {
		if b, err := exec.CommandContext(ctx, "git", "-C", dir, "diff", baseSHA).Output(); err == nil {
			out = b
		}
	}
	if len(out) == 0 {
		if b, err := exec.CommandContext(ctx, "git", "-C", dir, "diff").Output(); err == nil {
			out = b
		}
	}
	sum := sha256.Sum256(out)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// CheckAttributionAnomaly refuses a wave whose evidence looks like the
// proof-v5 failure.
//
// Two signals, both cheap and both retrospective on purpose. The COLD wave was
// caught by an implausible uniformity -- eleven supposedly independent governed
// runs all producing the empty-tree hash -- and nothing in the harness noticed.
// It notices now.
//
// This is not a quality gate on the product. It is a check that the instrument
// looked somewhere real, and it returns an integrity failure rather than a
// score.
func CheckAttributionAnomaly(attempts []Attempt) error {
	var governed []Attempt
	for _, a := range attempts {
		if a.Arm != ArmRaw {
			governed = append(governed, a)
		}
	}
	if len(governed) < 2 {
		return nil
	}
	// Only attempts that CLAIM to have delivered are held to attribution: a run
	// that refused or died before creating a candidate legitimately has none.
	var claimed []Attempt
	for _, a := range governed {
		if a.WorkflowTerminal("") == TerminalCompleted {
			claimed = append(claimed, a)
		}
	}
	if len(claimed) < 2 {
		return nil
	}
	empty, unbound := 0, []string{}
	for _, a := range claimed {
		if a.DiffHash == EmptyTreeHash {
			empty++
		}
		if strings.TrimSpace(a.CandidateDir) == "" {
			unbound = append(unbound, a.ID())
		}
	}
	if len(unbound) != 0 {
		sort.Strings(unbound)
		return fmt.Errorf("measurement-integrity failure: %d governed attempt(s) carry no resolved "+
			"candidate directory, so their verdicts describe a tree nobody identified: %s",
			len(unbound), strings.Join(unbound, ", "))
	}
	if empty == len(claimed) {
		return fmt.Errorf("measurement-integrity failure: all %d delivering governed attempts produced the "+
			"empty-tree diff hash. Independent runs do not agree that precisely, and the proof-v5 "+
			"COLD wave looked exactly like this while its candidates held real work in a directory "+
			"the harness never opened", len(claimed))
	}
	return nil
}

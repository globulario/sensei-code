package candidate

// Terminal disposition for a candidate.
//
// Every governed task creates a worktree and a branch, and for a while nothing
// ever resolved them: seven accumulated in a single day, six of them dead and
// one holding real unpublished work, with nothing in the system telling them
// apart. The cost is not disk. Recovery reads candidate and task state from
// disk, so undifferentiated leftovers turn "resume the interrupted task" into
// archaeology, and a candidate stops meaning anything specific — which is the
// opposite of what an exact, persisted base is for.
//
// Deleting on exit is the wrong repair, and it is worth saying why rather than
// leaving it as taste. It would have destroyed the one candidate that mattered:
// accepted, unpublished, its pull-request rendezvous deliberately declined
// because publication is human-owned. Cleaning up on exit performs the deletion
// half of a decision the system correctly refused to make.
//
// So this is disposition, not expiry, and it carries two properties that matter
// as much as the vocabulary:
//
//	Evidence is referenced before anything is removed. Base, diff digest, audit
//	verdict and changed paths outlive the worktree, so a cleaned-up candidate
//	leaves a record rather than a gap.
//
//	Retention is a decision with a reason. A retained worktree must be able to
//	say which branch of the matrix retained it; "still here because nobody
//	deleted it" is the state this exists to end.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Disposition is what became of a candidate. The vocabulary is closed: a
// candidate in no state at all is the condition being repaired, so an
// unrecognised value is refused rather than stored and puzzled over later.
type Disposition string

const (
	// Retained means the candidate holds work that is not published and is not
	// anyone's to discard — the accepted-but-unpublished case.
	Retained Disposition = "retained"
	// Adopted means the work reached shared history and the checkout is now a
	// copy of something durable.
	Adopted Disposition = "adopted"
	// Rejected means the work was judged not worth keeping.
	Rejected Disposition = "rejected"
	// Superseded means another candidate replaced this one.
	Superseded Disposition = "superseded"
	// Resumable means the run did not finish and durable state still references
	// this candidate, so removing it would destroy recoverable work.
	Resumable Disposition = "resumable"
	// Disposed means the worktree and branch have been removed and only the
	// evidence remains.
	Disposed Disposition = "disposed"
)

// Keeps reports whether this disposition retains the worktree and branch.
func (d Disposition) Keeps() bool {
	return d == Retained || d == Resumable
}

// Valid reports whether this is a disposition at all.
func (d Disposition) Valid() bool {
	switch d {
	case Retained, Adopted, Rejected, Superseded, Resumable, Disposed:
		return true
	}
	return false
}

// Evidence is what must outlive the candidate it describes.
//
// A removed worktree that leaves no trace converts a decision into a gap: a
// later reader cannot tell a candidate that was cleaned up from one that never
// existed, which is the same ambiguity in a different direction.
type Evidence struct {
	BaseSHA       string   `json:"base_sha"`
	DiffDigest    string   `json:"diff_digest,omitempty"`
	DiffBytes     int      `json:"diff_bytes"`
	ChangedPaths  []string `json:"changed_paths,omitempty"`
	AuditVerdict  string   `json:"audit_verdict,omitempty"`
	AuditDetail   string   `json:"audit_detail,omitempty"`
	ExecutedCheck []string `json:"executed_checks,omitempty"`
	// ProducedNoWork records that this candidate ended with an empty diff. It
	// is stated rather than inferred from a zero byte count, because "no work"
	// and "we did not look" must not be the same value.
	ProducedNoWork bool `json:"produced_no_work,omitempty"`
}

// Resolution is one terminal disposition, with the reason for it and the
// evidence that survives it.
type Resolution struct {
	Disposition Disposition `json:"disposition"`
	Reason      string      `json:"reason"`
	DecidedAt   time.Time   `json:"decided_at"`
	Evidence    Evidence    `json:"evidence"`
	// WorktreeRemoved and BranchRemoved record what actually happened, not what
	// the disposition implies. A disposal that failed halfway is a fact worth
	// having, and inferring removal from the word "disposed" would hide it.
	WorktreeRemoved bool `json:"worktree_removed"`
	BranchRemoved   bool `json:"branch_removed"`
}

// ErrNoEvidence reports an attempt to remove a candidate whose evidence would
// not survive it.
var ErrNoEvidence = errors.New("a candidate may not be removed before its evidence is recorded")

// Validate reports whether this resolution may be recorded.
func (r Resolution) Validate() error {
	if !r.Disposition.Valid() {
		return fmt.Errorf("%q is not a candidate disposition", r.Disposition)
	}
	if strings.TrimSpace(r.Reason) == "" {
		return fmt.Errorf("disposition %s carries no reason; retention and removal are both decisions", r.Disposition)
	}
	if r.Disposition.Keeps() {
		return nil
	}
	// Everything below removes a checkout. The base is the minimum that must
	// outlive it — without it the record cannot say what the work was against —
	// and either there was work, whose shape is recorded, or there was none,
	// which is stated.
	if strings.TrimSpace(r.Evidence.BaseSHA) == "" {
		return fmt.Errorf("%w: %s records no base commit", ErrNoEvidence, r.Disposition)
	}
	if !r.Evidence.ProducedNoWork && strings.TrimSpace(r.Evidence.DiffDigest) == "" && len(r.Evidence.ChangedPaths) == 0 {
		return fmt.Errorf("%w: %s records neither the work it removed nor that there was none",
			ErrNoEvidence, r.Disposition)
	}
	return nil
}

// Summary is the one-line form for a transcript or a listing.
func (r Resolution) Summary() string {
	if r.Disposition == "" {
		return "no disposition recorded"
	}
	line := string(r.Disposition) + ": " + strings.TrimSpace(r.Reason)
	if !r.Disposition.Keeps() && !r.WorktreeRemoved {
		line += " (worktree still present)"
	}
	return line
}

// Resolve records what became of this candidate.
//
// It writes the record before anything is removed, and refuses a removal whose
// evidence would not survive it. The order is the point: a disposal that
// deletes first and records second loses exactly the case it needs to explain.
func (i Identity) Resolve(repoRoot string, r Resolution) (Identity, error) {
	if err := r.Validate(); err != nil {
		return i, err
	}
	if r.DecidedAt.IsZero() {
		r.DecidedAt = time.Now().UTC()
	}
	if strings.TrimSpace(r.Evidence.BaseSHA) == "" {
		r.Evidence.BaseSHA = i.BaseSHA
	}
	i.Resolution = &r
	if err := i.Save(repoRoot); err != nil {
		return i, err
	}
	return i, nil
}

// Unresolved reports whether this candidate is still in the state this whole
// mechanism exists to end: present, and meaning nothing in particular.
func (i Identity) Unresolved() bool { return i.Resolution == nil }

// List reads every recorded candidate for this repository, newest first.
//
// It reads what is on disk rather than what a run remembers, because the
// question it answers — "what is lying around, and why" — is asked precisely
// when no run is in progress.
func List(repoRoot string) ([]Identity, error) {
	dir := filepath.Join(repoRoot, ".sensei-code", "candidates")
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Identity
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		taskID := strings.TrimSuffix(entry.Name(), ".json")
		id, ok, err := Load(repoRoot, taskID)
		if err != nil {
			// A record that cannot be read is reported as a record that cannot
			// be read. Skipping it silently would understate what is present.
			out = append(out, Identity{TaskID: taskID, Repository: repoRoot,
				Resolution: &Resolution{Disposition: "", Reason: "identity unreadable: " + err.Error()}})
			continue
		}
		if ok {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].CreatedAt.After(out[b].CreatedAt) })
	return out, nil
}

// Render lists candidates the way the question is actually asked: what is
// lying around, and why is it still here.
//
// Unresolved candidates are reported first and named as unresolved rather than
// described as retained. The difference is the whole issue: retained means
// somebody decided to keep this, and unresolved means nobody decided anything.
func Render(candidates []Identity) string {
	if len(candidates) == 0 {
		return "No candidates recorded for this repository."
	}
	var unresolved, resolved []Identity
	for _, c := range candidates {
		if c.Unresolved() {
			unresolved = append(unresolved, c)
			continue
		}
		resolved = append(resolved, c)
	}
	var b strings.Builder
	line := func(c Identity) {
		state := "unresolved — nobody has decided what became of this"
		if c.Resolution != nil {
			state = c.Resolution.Summary()
		}
		fmt.Fprintf(&b, "%s  %s\n", c.TaskID, state)
		fmt.Fprintf(&b, "    base %s · branch %s\n", short(c.BaseSHA), c.Branch)
		if c.Resolution != nil {
			ev := c.Resolution.Evidence
			switch {
			case ev.ProducedNoWork:
				fmt.Fprintf(&b, "    evidence: produced no work")
			case len(ev.ChangedPaths) != 0:
				fmt.Fprintf(&b, "    evidence: %d file(s), %d diff bytes", len(ev.ChangedPaths), ev.DiffBytes)
			default:
				fmt.Fprintf(&b, "    evidence: %d diff bytes", ev.DiffBytes)
			}
			if v := strings.TrimSpace(ev.AuditVerdict); v != "" {
				fmt.Fprintf(&b, " · audit %s", v)
			}
			b.WriteString("\n")
		}
		if c.Resolution == nil || c.Resolution.Disposition.Keeps() {
			fmt.Fprintf(&b, "    worktree %s\n", c.Worktree)
		}
	}
	if len(unresolved) != 0 {
		fmt.Fprintf(&b, "Unresolved (%d)\n\n", len(unresolved))
		for _, c := range unresolved {
			line(c)
		}
		b.WriteString("\n")
	}
	if len(resolved) != 0 {
		fmt.Fprintf(&b, "Resolved (%d)\n\n", len(resolved))
		for _, c := range resolved {
			line(c)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

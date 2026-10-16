// Package candidate binds a governed task to an exact repository state.
//
// A worktree created from "HEAD" is created from whatever HEAD meant at that
// instant, which is not the same as a state anybody agreed to govern. Two
// things go wrong quietly. A dirty canonical checkout means the human is
// looking at files that are not in the candidate at all, so the task is
// governing something the human cannot see and the human is editing something
// the task will never audit. And a second worker picking up a task after a
// restart re-derives HEAD, which may have moved, so the fallback silently
// continues from a different base than the one the plan was approved against.
//
// So the base is established once, written down, and thereafter read rather
// than recomputed. Identity is immutable for the life of a candidate: if HEAD
// has moved, that is a new generation and must be created deliberately, not
// inherited by a worker that happened to start later.
//
// Two laws, and the second was learned the hard way.
//
//	Base identity MUST be established immediately after the clean-workspace
//	start gate, and before any workflow action capable of mutating the governed
//	repository. Once established, no later observation of canonical HEAD or
//	working-tree cleanliness may redefine it.
//
//	Resume consumes durable base identity. It never reconstructs candidate
//	identity from the current canonical checkout.
//
// "Establish once" turned out to be an ordering rule as much as an identity
// rule. The workflow writes to its own repository during a run — a Level-3
// resolution is persisted into this repository's awareness corpus — so a base
// taken after that point observes a tree the run itself dirtied, and the
// dirty-checkout refusal fires on the system's own governance artifact. The
// last uncontaminated observation is the only honest one.
//
// The second law matters more than it looks. Without it, a restart after a
// resolution was written would quietly promote yesterday's governance side
// effect into today's candidate baseline: the recorded decision would become
// part of the base rather than part of the history, and nothing would report
// that the baseline had moved.
package candidate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Identity is the exact state a governed candidate is bound to.
type Identity struct {
	TaskID string `json:"task_id"`
	// Repository is the canonical checkout this candidate derives from.
	Repository string `json:"repository"`
	// Domain is the Sensei graph domain governing it.
	Domain string `json:"domain"`
	// BaseSHA is the commit the candidate was cut from. It never changes.
	BaseSHA string `json:"base_sha"`
	// WorktreeState records the policy that was satisfied when the base was
	// established, so a receipt says what "clean" meant rather than implying it.
	WorktreeState string `json:"worktree_state"`
	// GraphBuildCommit and SourceRepoCommit pin the graph generation that
	// certified this base, so a later audit can tell whether it is being judged
	// by the same rules it started under.
	GraphBuildCommit string    `json:"graph_build_commit,omitempty"`
	SourceRepoCommit string    `json:"source_repo_commit,omitempty"`
	Worktree         string    `json:"worktree"`
	Branch           string    `json:"branch"`
	CreatedAt        time.Time `json:"created_at"`
	// Resolution is what became of this candidate. It is absent while the task
	// is live, and absent afterwards is the defect it exists to remove: a
	// worktree nobody resolved means nothing in particular.
	Resolution *Resolution `json:"resolution,omitempty"`
	// InFlight is a confined execution started against this candidate whose
	// teardown has not been established (N5, RULING-358 §12). It is written
	// durably BEFORE the execution can acquire write authority over any
	// workspace of the task, and cleared only when the trusted execution
	// boundary has proven the teardown. A record that still carries it --
	// after a crash, an interruption or a restart -- describes an execution
	// that may still hold write authority, and is read exactly as a
	// quarantine: nothing ordinary may reuse, observe, recapture or dispose of
	// the candidate (Unsettled).
	InFlight *InFlight `json:"in_flight,omitempty"`
}

// InFlight identifies one confined execution of a task by nonsecret facts
// only: never the status nonce or any credential.
type InFlight struct {
	// Invocation is a random identifier of this execution, the binding a
	// settlement or a quarantine is checked against.
	Invocation string `json:"invocation"`
	// Phase names the validation phase that started it.
	Phase string `json:"phase"`
	// Workspace is the candidate workspace the execution runs against; a
	// baseline observation worktree the same execution may create is part of
	// the same boundary.
	Workspace string `json:"workspace"`
	// Baseline is the baseline observation worktree the execution created
	// for attribution, recorded before any baseline execution starts in it
	// (NoteBaseline). Empty while no baseline exists.
	Baseline string `json:"baseline,omitempty"`
	// Profile is the pinned confinement profile the execution runs under.
	Profile   string    `json:"profile,omitempty"`
	StartedAt time.Time `json:"started_at"`
}

// Unsettled reports whether this candidate may still be written by an
// execution nobody has proven ended: a quarantined disposition, or a confined
// execution recorded as started and never settled. Every ordinary use of the
// candidate -- worktree creation or reuse, observation, recapture, execution,
// review, publication, removal, resume -- refuses on it.
func (i Identity) Unsettled() bool {
	return i.InFlight != nil || (i.Resolution != nil && i.Resolution.Disposition == Quarantined)
}

// Quarantine returns the typed refusal for an unsettled candidate, or nil.
func (i Identity) Quarantine() *ErrQuarantined {
	if !i.Unsettled() {
		return nil
	}
	q := &ErrQuarantined{TaskID: i.TaskID, Workspace: i.Worktree}
	switch {
	case i.Resolution != nil && i.Resolution.Disposition == Quarantined:
		q.Reason = i.Resolution.Reason
	case i.InFlight != nil:
		q.Workspace = i.InFlight.Workspace
		q.Reason = fmt.Sprintf("confined execution %s (%s) was started at %s and its teardown was never established",
			i.InFlight.Invocation, i.InFlight.Phase, i.InFlight.StartedAt.UTC().Format(time.RFC3339))
	}
	return q
}

// ErrQuarantined refuses an ordinary operation on a candidate whose confined
// execution may still hold write authority. Only a trusted settlement of the
// exact invocation may release it; none is implemented here (RULING-358 §21).
type ErrQuarantined struct {
	TaskID    string
	Workspace string
	Reason    string
}

func (e *ErrQuarantined) Error() string {
	return fmt.Sprintf("candidate %s is quarantined: %s; its workspace %s may still be written by a confined execution, "+
		"so it is not reused, observed, recaptured, reviewed, published or removed until a trusted settlement establishes that the execution ended",
		e.TaskID, strings.TrimSpace(e.Reason), e.Workspace)
}

// ErrNoIdentity refuses to bind a confined execution to a task with no
// recorded candidate identity: the in-flight boundary has nowhere durable to
// live, so the execution must not start.
var ErrNoIdentity = errors.New("no candidate identity is recorded for this task, so a confined execution cannot be bound to it")

// Summary is the one-line form for a transcript or a pull request body.
func (i Identity) Summary() string {
	base := i.BaseSHA
	if len(base) > 12 {
		base = base[:12]
	}
	return fmt.Sprintf("candidate %s on %s from base %s (%s)", i.TaskID, i.Branch, base, i.WorktreeState)
}

// ErrDirtyCanonical reports that the human's checkout has uncommitted changes.
type ErrDirtyCanonical struct{ Repository string }

func (e *ErrDirtyCanonical) Error() string {
	return fmt.Sprintf("the canonical checkout %s has uncommitted changes; a governed candidate cut from HEAD would omit them, "+
		"so it would govern a state you are not looking at. Commit or stash them, then run this again", e.Repository)
}

// ErrBaseMoved reports an attempt to reuse a candidate identity against a
// different base.
type ErrBaseMoved struct {
	TaskID   string
	Recorded string
	Current  string
}

func (e *ErrBaseMoved) Error() string {
	return fmt.Sprintf("candidate %s was established at base %s but the repository is now at %s; "+
		"a candidate's base is immutable, so continuing here would govern a different state than the one that was planned and approved",
		e.TaskID, short(e.Recorded), short(e.Current))
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

// path is where a task's identity is recorded: beside the repository's own
// configuration, never inside the candidate, so the candidate's diff does not
// contain the description of itself.
func path(repoRoot, taskID string) string {
	name := strings.TrimSpace(taskID)
	if name == "" {
		name = "default"
	}
	return filepath.Join(repoRoot, ".sensei-code", "candidates", filepath.Base(name)+".json")
}

// Load reads a previously established identity.
func Load(repoRoot, taskID string) (Identity, bool, error) {
	return load(path(repoRoot, taskID), taskID)
}

func load(target, taskID string) (Identity, bool, error) {
	body, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return Identity{}, false, nil
	}
	if err != nil {
		return Identity{}, false, err
	}
	var id Identity
	if err := json.Unmarshal(body, &id); err != nil {
		return Identity{}, false, fmt.Errorf("candidate identity for %s is unreadable: %w", taskID, err)
	}
	if strings.TrimSpace(id.BaseSHA) == "" {
		return Identity{}, false, fmt.Errorf("candidate identity for %s records no base commit", taskID)
	}
	return id, true, nil
}

// recordMu serializes every write of a candidate record in this process, so a
// read-check-write of the record is one step: a stale in-memory Identity can
// never replace a newer durable in-flight or quarantine state between the
// check and the write.
var recordMu sync.Mutex

// Save writes the identity down.
//
// An ordinary save never removes an unsettled state the durable record holds:
// a record whose in-flight execution or quarantine is on disk is replaced only
// by one that keeps it (the in-flight boundary is settled by SettleExecution,
// and nothing here releases a quarantine). The write is atomic and durable:
// a crash leaves either the previous record or this one, never a torn file
// (durability of the rename itself is established on Linux and other unix
// systems; see syncDir). A record that exists but cannot be read is not
// overwritten: whether it holds an unsettled state is unknown, and an
// ordinary write must not be the thing that erases one.
func (i Identity) Save(repoRoot string) error {
	recordMu.Lock()
	defer recordMu.Unlock()
	target := path(repoRoot, i.TaskID)
	onDisk, ok, err := load(target, i.TaskID)
	if err != nil {
		return fmt.Errorf("the existing candidate record of %s cannot be read, so it is not overwritten: %w", i.TaskID, err)
	}
	if ok && onDisk.Unsettled() {
		if q := dropsUnsettled(onDisk, i); q != nil {
			return q
		}
	}
	return writeRecord(target, i)
}

// NoteBaseline durably names, on the in-flight record of the task, the
// baseline observation worktree an execution is about to run in (RULING-358
// §10): it is written after the worktree exists and before the baseline
// execution can acquire write authority over it, so a crash during
// attribution leaves the baseline's identity on the record. It refuses when
// no execution is in flight -- a baseline is never run outside the boundary.
func NoteBaseline(repoRoot, taskID, baseline string) error {
	recordMu.Lock()
	defer recordMu.Unlock()
	target := path(repoRoot, taskID)
	id, ok, err := load(target, taskID)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNoIdentity
	}
	if id.InFlight == nil {
		return fmt.Errorf("candidate %s records no in-flight execution to bind the baseline %s to", taskID, baseline)
	}
	if id.InFlight.Baseline == baseline {
		return nil
	}
	id.InFlight.Baseline = baseline
	return writeRecord(target, id)
}

// dropsUnsettled reports the refusal for a write of next over a durable record
// that is unsettled, or nil when next keeps that state.
func dropsUnsettled(onDisk, next Identity) *ErrQuarantined {
	if onDisk.InFlight != nil && (next.InFlight == nil || next.InFlight.Invocation != onDisk.InFlight.Invocation) {
		return onDisk.Quarantine()
	}
	if onDisk.Resolution != nil && onDisk.Resolution.Disposition == Quarantined &&
		(next.Resolution == nil || next.Resolution.Disposition != Quarantined) {
		return onDisk.Quarantine()
	}
	return nil
}

// writeRecord replaces target atomically and durably: a temporary file in the
// same directory is written and synced, renamed over the target, and the
// directory is synced so the rename itself survives a crash.
func writeRecord(target string, i Identity) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(append(body, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmpName, target); err != nil {
		return err
	}
	return syncDir(dir)
}

// syncDir makes a rename in dir durable. Windows cannot sync a directory
// handle; there the file's own sync before the rename is what is available.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}

// BeginExecution durably records that a confined execution is about to start
// against the task's candidate (RULING-358 §12). It is called BEFORE the
// execution can acquire write authority over any workspace of the task, and
// it refuses -- so the execution must not start -- when no identity is
// recorded, when the candidate is already unsettled, or when the record
// cannot be written.
func BeginExecution(repoRoot, taskID string, f InFlight) (Identity, error) {
	recordMu.Lock()
	defer recordMu.Unlock()
	target := path(repoRoot, taskID)
	id, ok, err := load(target, taskID)
	if err != nil {
		return Identity{}, err
	}
	if !ok {
		return Identity{}, ErrNoIdentity
	}
	if q := id.Quarantine(); q != nil {
		return id, q
	}
	if strings.TrimSpace(f.Invocation) == "" || strings.TrimSpace(f.Workspace) == "" {
		return id, errors.New("an in-flight execution must name its invocation and its workspace")
	}
	if f.StartedAt.IsZero() {
		f.StartedAt = time.Now().UTC()
	}
	id.InFlight = &f
	if err := writeRecord(target, id); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// SettleExecution clears the in-flight record of exactly this invocation once
// the trusted execution boundary has proven its teardown (or proven that it
// never acquired workspace authority). A quarantined candidate is never
// settled here.
func SettleExecution(repoRoot, taskID, invocation string) (Identity, error) {
	recordMu.Lock()
	defer recordMu.Unlock()
	target := path(repoRoot, taskID)
	id, ok, err := load(target, taskID)
	if err != nil {
		return Identity{}, err
	}
	if !ok {
		return Identity{}, ErrNoIdentity
	}
	if id.Resolution != nil && id.Resolution.Disposition == Quarantined {
		return id, id.Quarantine()
	}
	if id.InFlight == nil || id.InFlight.Invocation != invocation {
		return id, fmt.Errorf("candidate %s records no in-flight execution %s to settle", taskID, invocation)
	}
	id.InFlight = nil
	if err := writeRecord(target, id); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// Repo is the git surface Establish needs, kept narrow so the policy is
// testable without a real repository.
type Repo interface {
	Head() (string, error)
	IsClean() (bool, error)
}

// Establish binds a task to an exact base, or returns the binding it already
// has.
//
// On a first call it requires a clean canonical checkout. This is the policy
// choice the redesign asks for, and it is the strict one deliberately: the
// alternative — governing HEAD while the human holds uncommitted edits — is not
// a smaller version of correct, it is a task quietly scoped to a different
// repository state than the one on the human's screen.
//
// On any later call it returns the recorded identity, and refuses if the
// repository has moved underneath it. A second worker taking over after a
// restart therefore continues from the base the plan was approved against, not
// from wherever HEAD has drifted to.
func Establish(repoRoot, taskID, domain, worktree, branch string, repo Repo, now time.Time) (Identity, error) {
	if existing, ok, err := Load(repoRoot, taskID); err != nil {
		return Identity{}, err
	} else if ok {
		head, err := repo.Head()
		if err != nil {
			return Identity{}, err
		}
		if strings.TrimSpace(head) != "" && head != existing.BaseSHA {
			return existing, &ErrBaseMoved{TaskID: taskID, Recorded: existing.BaseSHA, Current: head}
		}
		return existing, nil
	}

	clean, err := repo.IsClean()
	if err != nil {
		return Identity{}, err
	}
	if !clean {
		return Identity{}, &ErrDirtyCanonical{Repository: repoRoot}
	}
	head, err := repo.Head()
	if err != nil {
		return Identity{}, err
	}
	if strings.TrimSpace(head) == "" {
		return Identity{}, fmt.Errorf("cannot establish a candidate base: the repository reported no HEAD")
	}

	id := Identity{
		TaskID:        taskID,
		Repository:    repoRoot,
		Domain:        domain,
		BaseSHA:       strings.TrimSpace(head),
		WorktreeState: "clean",
		Worktree:      worktree,
		Branch:        branch,
		CreatedAt:     now.UTC(),
	}
	if err := id.Save(repoRoot); err != nil {
		return Identity{}, err
	}
	return id, nil
}

// BindGraph records which graph generation certified this base. It is separate
// from Establish because the graph facts arrive from Sensei, and an identity
// that could not be written down at all is a worse failure than one missing its
// graph provenance.
func (i Identity) BindGraph(repoRoot, graphBuildCommit, sourceRepoCommit string) (Identity, error) {
	i.GraphBuildCommit = strings.TrimSpace(graphBuildCommit)
	i.SourceRepoCommit = strings.TrimSpace(sourceRepoCommit)
	return i, i.Save(repoRoot)
}

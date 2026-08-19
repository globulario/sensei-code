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
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
}

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
	body, err := os.ReadFile(path(repoRoot, taskID))
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

// Save writes the identity down.
func (i Identity) Save(repoRoot string) error {
	target := path(repoRoot, i.TaskID)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(i, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(target, append(body, '\n'), 0o644)
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

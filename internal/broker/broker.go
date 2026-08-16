// Package broker sits between what the workflow intends to do and what a
// worker process is actually able to do.
//
// The capability flags in .sensei-code/config.json read like guarantees. Most
// of them were not: force_push, production_deploy, run_builds and run_tests
// appeared in the config struct and were read by nothing, so the file described
// an intention while the execution layer enforced a different, wider one. A
// capability nobody consults is documentation with a boolean's syntax.
//
// This package fixes what it can and, just as importantly, says plainly what it
// cannot. Three things are mechanically enforced for a worker: pushing,
// force-pushing (as a non-fast-forward push, which is what force actually
// means to a remote), and writing outside the candidate worktree.
//
// THE ENVELOPE IS THEREFORE ONLY PARTLY ENFORCED. run_builds, run_tests,
// local_commit and production_deploy are still not mechanically preventable
// here: stopping a process from invoking a compiler, reaching the network, or
// running a deploy script it wrote itself needs a process sandbox this design
// does not have. Unenforceable() reports exactly that set, which is the honest
// interim behaviour and not the finished one — the capability envelope is not
// closed until those are enforced too, and calling it closed because the
// reporting is tidy would be the same category of claim this package exists to
// stop. Readiness fails the role rather than the product implying a boundary
// that is not there.
//
// The honest limit is worth stating twice: this stops accidents, not a
// determined process. Anything that can edit its own environment can route
// around a hooks path. The worktree branch remains the real blast radius.
package broker

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/config"
)

// Capability is one thing a worker may or may not be permitted to do.
type Capability string

const (
	ReadRepository   Capability = "read_repository"
	WriteCandidates  Capability = "write_candidates"
	CreateWorktrees  Capability = "create_worktrees"
	RunBuilds        Capability = "run_builds"
	RunTests         Capability = "run_tests"
	LocalCommit      Capability = "local_commit"
	Push             Capability = "push"
	ForcePush        Capability = "force_push"
	ProductionDeploy Capability = "production_deploy"
)

// Envelope is the set of capabilities granted for a run.
type Envelope struct {
	perms config.Permissions
}

func New(p config.Permissions) Envelope { return Envelope{perms: p} }

// Grants reports whether a capability is declared.
func (e Envelope) Grants(c Capability) bool {
	switch c {
	case ReadRepository:
		return e.perms.ReadRepository
	case WriteCandidates:
		return e.perms.WriteCandidates
	case CreateWorktrees:
		return e.perms.CreateWorktrees
	case RunBuilds:
		return e.perms.RunBuilds
	case RunTests:
		return e.perms.RunTests
	case LocalCommit:
		return e.perms.LocalCommit
	case Push:
		return e.perms.Push
	case ForcePush:
		return e.perms.ForcePush
	case ProductionDeploy:
		return e.perms.ProductionDeploy
	default:
		// An unknown capability is not granted. A typo must not read as
		// permission.
		return false
	}
}

// Require reports an error when a capability the caller depends on is absent,
// so a workflow step refuses rather than proceeding past its own envelope.
func (e Envelope) Require(c Capability) error {
	if e.Grants(c) {
		return nil
	}
	return fmt.Errorf("%s capability is not granted", c)
}

// mechanicallyEnforced is the set this package can actually make impossible.
var mechanicallyEnforced = map[Capability]bool{
	Push:            true,
	ForcePush:       true,
	WriteCandidates: true,
}

// Unenforceable lists capabilities that are denied in configuration but that
// this broker cannot mechanically prevent.
//
// Only denials appear. A granted capability needs no enforcement, so a config
// that permits everything has nothing unenforceable about it — the gap only
// exists where the file says "no" and the runtime cannot make that stick.
//
// Readiness reports this rather than the product asserting a boundary it does
// not have. A stated limit that a person can plan around is worth more than an
// unstated one they discover after it mattered.
func (e Envelope) Unenforceable() []Capability {
	var out []Capability
	for _, c := range []Capability{RunBuilds, RunTests, LocalCommit, ProductionDeploy} {
		if !e.Grants(c) && !mechanicallyEnforced[c] {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// pushHook refuses every push and names the reason, so a worker reports a
// governance boundary rather than an unexplained git failure.
const pushHook = `#!/bin/sh
echo "sensei-code: push is refused; publication is human-owned and the push capability is not granted" >&2
exit 1
`

// forcePushHook permits a fast-forward push and refuses a rewrite.
//
// Git gives a pre-push hook no flag saying "--force was used", so asking that
// question directly is not possible. What --force actually does to a remote is
// replace history that is not an ancestor of what is being pushed, and that is
// observable: for each ref, if the remote sha is not an ancestor of the local
// sha, the push would discard commits. Deletions are refused for the same
// reason. A zero remote sha is a new branch, which discards nothing.
const forcePushHook = `#!/bin/sh
zero=0000000000000000000000000000000000000000
while read -r local_ref local_sha remote_ref remote_sha; do
	if [ "$local_sha" = "$zero" ]; then
		echo "sensei-code: deleting a remote branch is refused; force_push capability is not granted" >&2
		exit 1
	fi
	if [ "$remote_sha" = "$zero" ]; then
		continue
	fi
	if ! git merge-base --is-ancestor "$remote_sha" "$local_sha" 2>/dev/null; then
		echo "sensei-code: this push would discard remote commits on $remote_ref; force_push capability is not granted" >&2
		exit 1
	fi
done
exit 0
`

// GuardDir is where a task's hooks live: inside the canonical repository's
// .sensei-code directory, deliberately outside the candidate worktree.
func GuardDir(canonicalRoot, taskID string) string {
	name := strings.TrimSpace(taskID)
	if name == "" {
		name = "default"
	}
	return filepath.Join(canonicalRoot, ".sensei-code", "guards", filepath.Base(name))
}

// Enforce realises the envelope, writing hooks into guardDir and returning the
// environment entries that bind a worker's git to them.
//
// guardDir must be outside the candidate worktree, and this refuses if it is
// not. Putting the hooks inside the worktree looks natural and is wrong three
// times over: the worker's own `git add .` commits the hook into the candidate,
// a `git reset --hard` past that commit deletes the hook and silently disarms
// the guard, and the hook file shows up in the candidate diff as a change
// nobody asked for. The first version of this package did exactly that, and the
// force-push test caught it only because resetting history is what a force push
// test has to do.
//
// The configuration is process-scoped so the refusal never leaks into the
// human's own checkout, which owns these capabilities and must keep them.
func (e Envelope) Enforce(guardDir, workspace string) ([]string, error) {
	guard, err := filepath.Abs(strings.TrimSpace(guardDir))
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(workspace) != "" {
		work, err := filepath.Abs(workspace)
		if err != nil {
			return nil, err
		}
		if rel, err := filepath.Rel(work, guard); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("refusing to install guards at %s: it is inside the candidate worktree %s, where the worker can commit or reset them away", guard, work)
		}
	}
	hooksDir := filepath.Join(guard, "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return nil, err
	}

	body := ""
	switch {
	case !e.Grants(Push):
		body = pushHook
	case !e.Grants(ForcePush):
		body = forcePushHook
	}
	if body != "" {
		if err := os.WriteFile(filepath.Join(hooksDir, "pre-push"), []byte(body), 0o755); err != nil {
			return nil, err
		}
	} else {
		// Both are granted. Remove a hook a previous, stricter run may have
		// left behind rather than silently keeping the old refusal.
		_ = os.Remove(filepath.Join(hooksDir, "pre-push"))
	}

	// GIT_CONFIG_* is process scoped. Setting core.hooksPath in the repository
	// would leak the refusal into the human's checkout.
	return []string{
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		fmt.Sprintf("GIT_CONFIG_VALUE_0=%s", hooksDir),
	}, nil
}

// GuardCanonicalCheckout refuses to run a governed worker against the
// repository the human is working in.
//
// Candidate isolation is the real blast-radius boundary in this design, so a
// workspace that is not a separate worktree is not a weaker version of the
// guarantee — it is the absence of it. This is checked rather than assumed
// because the workspace is a string threaded through several call sites, and a
// bug that passes the canonical root looks like nothing at all until a worker
// has already edited the human's files.
func GuardCanonicalCheckout(canonicalRoot, workspace string) error {
	canonical, err := filepath.Abs(strings.TrimSpace(canonicalRoot))
	if err != nil {
		return err
	}
	candidate, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return err
	}
	if canonical == candidate {
		return fmt.Errorf("refusing to run a governed worker in the canonical checkout %s: candidate isolation is the blast-radius boundary", canonical)
	}
	return nil
}

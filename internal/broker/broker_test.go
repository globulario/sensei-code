package broker

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
)

// git runs a git command in dir with the broker's environment applied, and
// returns combined output plus whether it succeeded.
func git(t *testing.T, dir string, env []string, args ...string) (string, bool) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t",
	)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// repoPair builds a real local "remote" and a clone of it, so push behaviour is
// exercised against git itself rather than against a description of git.
func repoPair(t *testing.T) (remote, clone string) {
	t.Helper()
	root := t.TempDir()
	remote = filepath.Join(root, "remote.git")
	work := filepath.Join(root, "seed")
	clone = filepath.Join(root, "clone")

	if _, ok := git(t, root, nil, "init", "--bare", "-b", "main", remote); !ok {
		t.Skip("git unavailable")
	}
	if _, ok := git(t, root, nil, "init", "-b", "main", work); !ok {
		t.Skip("git unavailable")
	}
	if err := os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, work, nil, "add", ".")
	git(t, work, nil, "commit", "-m", "one")
	git(t, work, nil, "remote", "add", "origin", remote)
	if out, ok := git(t, work, nil, "push", "origin", "main"); !ok {
		t.Skipf("cannot seed remote: %s", out)
	}
	if out, ok := git(t, root, nil, "clone", remote, clone); !ok {
		t.Skipf("cannot clone: %s", out)
	}
	return remote, clone
}

func commit(t *testing.T, dir, file, body, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, dir, nil, "add", ".")
	if out, ok := git(t, dir, nil, "commit", "-m", msg); !ok {
		t.Fatalf("commit failed: %s", out)
	}
}

// TestWorkerCannotPushWhenPushIsDenied covers "a worker cannot push when
// push=false even if its model tries". The model's intent is irrelevant; git
// itself refuses.
func TestWorkerCannotPushWhenPushIsDenied(t *testing.T) {
	_, clone := repoPair(t)
	env, err := New(config.Permissions{Push: false, ForcePush: false}).Enforce(t.TempDir(), clone)
	if err != nil {
		t.Fatal(err)
	}
	commit(t, clone, "a.txt", "two\n", "two")

	out, ok := git(t, clone, env, "push", "origin", "main")
	if ok {
		t.Fatal("push succeeded while the push capability was denied")
	}
	if !strings.Contains(out, "push is refused") {
		t.Fatalf("refusal did not explain itself as a governance boundary: %s", out)
	}
}

// TestWorkerCannotForcePushWhenOnlyForceIsDenied covers "a worker cannot
// force-push when force_push=false" while push itself is permitted — the case
// an unconditional refusal cannot express.
func TestWorkerCannotForcePushWhenOnlyForceIsDenied(t *testing.T) {
	_, clone := repoPair(t)
	env, err := New(config.Permissions{Push: true, ForcePush: false}).Enforce(t.TempDir(), clone)
	if err != nil {
		t.Fatal(err)
	}

	// An ordinary fast-forward push is allowed.
	commit(t, clone, "a.txt", "two\n", "two")
	if out, ok := git(t, clone, env, "push", "origin", "main"); !ok {
		t.Fatalf("a fast-forward push was refused while push was granted: %s", out)
	}

	// Rewriting history and force-pushing discards a remote commit, and must be
	// refused even though the flag says push is fine.
	git(t, clone, nil, "reset", "--hard", "HEAD~1")
	commit(t, clone, "a.txt", "rewritten\n", "rewritten")
	out, ok := git(t, clone, env, "push", "--force", "origin", "main")
	if ok {
		t.Fatal("a history-discarding force push succeeded while force_push was denied")
	}
	if !strings.Contains(out, "discard remote commits") {
		t.Fatalf("refusal did not name what it protected: %s", out)
	}
}

// TestBranchDeletionIsRefusedAsAForcePush pins the other way to discard remote
// history, which a naive ancestry check would let through.
func TestBranchDeletionIsRefusedAsAForcePush(t *testing.T) {
	_, clone := repoPair(t)
	env, err := New(config.Permissions{Push: true, ForcePush: false}).Enforce(t.TempDir(), clone)
	if err != nil {
		t.Fatal(err)
	}
	out, ok := git(t, clone, env, "push", "origin", "--delete", "main")
	if ok {
		t.Fatal("deleting a remote branch succeeded while force_push was denied")
	}
	if !strings.Contains(out, "refused") {
		t.Fatalf("deletion refusal did not explain itself: %s", out)
	}
}

// TestGrantedPushIsNotObstructed keeps the broker from becoming a blanket
// refusal. An envelope that grants both must leave git alone, including when a
// stricter earlier run left a hook behind.
func TestGrantedPushIsNotObstructed(t *testing.T) {
	_, clone := repoPair(t)
	guard := t.TempDir()
	if _, err := New(config.Permissions{}).Enforce(guard, clone); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(guard, "hooks", "pre-push")); err != nil {
		t.Fatalf("the strict run did not install a hook, so this test proves nothing: %v", err)
	}

	env, err := New(config.Permissions{Push: true, ForcePush: true}).Enforce(guard, clone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(guard, "hooks", "pre-push")); !os.IsNotExist(err) {
		t.Fatal("a stale refusal hook survived an envelope that grants pushing")
	}
	commit(t, clone, "a.txt", "two\n", "two")
	if out, ok := git(t, clone, env, "push", "origin", "main"); !ok {
		t.Fatalf("a fully granted push was refused: %s", out)
	}
}

// TestGovernedWorkerCannotMutateTheCanonicalCheckout covers the canonical
// checkout boundary.
func TestGovernedWorkerCannotMutateTheCanonicalCheckout(t *testing.T) {
	root := t.TempDir()
	if err := GuardCanonicalCheckout(root, root); err == nil {
		t.Fatal("a governed worker was allowed to run in the canonical checkout")
	}
	if err := GuardCanonicalCheckout(root, filepath.Join(root, ".sensei-code", "worktrees", "task-1")); err != nil {
		t.Fatalf("a genuine candidate worktree was refused: %v", err)
	}
	// Path spelling must not be a way around it.
	if err := GuardCanonicalCheckout(root, root+string(filepath.Separator)+"."); err == nil {
		t.Fatal("a differently spelled canonical root slipped past the guard")
	}
}

// TestDeniedCapabilitiesAreEitherEnforcedOrDeclared is the honesty test for the
// whole package: every capability the config denies must be either mechanically
// enforced or reported as unenforceable. Silence about a boundary is what the
// old configuration did, and it is the failure this slice exists to end.
func TestDeniedCapabilitiesAreEitherEnforcedOrDeclared(t *testing.T) {
	denyAll := New(config.Permissions{})
	declared := map[Capability]bool{}
	for _, c := range denyAll.Unenforceable() {
		declared[c] = true
	}
	for _, c := range []Capability{RunBuilds, RunTests, LocalCommit, Push, ForcePush, ProductionDeploy} {
		if mechanicallyEnforced[c] {
			continue
		}
		if !declared[c] {
			t.Errorf("%s is denied, not mechanically enforced, and not declared unenforceable — the config implies a boundary that does not exist", c)
		}
	}
	// production_deploy is the one a reader is most likely to assume is real.
	if !declared[ProductionDeploy] {
		t.Error("production_deploy denial is not declared unenforceable")
	}
}

// TestGrantedCapabilitiesAreNotReportedAsGaps keeps the readiness signal quiet
// when there is nothing to report.
func TestGrantedCapabilitiesAreNotReportedAsGaps(t *testing.T) {
	all := New(config.Permissions{
		ReadRepository: true, WriteCandidates: true, CreateWorktrees: true,
		RunBuilds: true, RunTests: true, LocalCommit: true,
		Push: true, ForcePush: true, ProductionDeploy: true,
	})
	if gaps := all.Unenforceable(); len(gaps) != 0 {
		t.Fatalf("an envelope that denies nothing reported gaps: %v", gaps)
	}
}

// TestUnknownCapabilityIsNotGranted guards the default direction.
func TestUnknownCapabilityIsNotGranted(t *testing.T) {
	e := New(config.Permissions{ReadRepository: true})
	if e.Grants(Capability("read_repositry")) {
		t.Fatal("a misspelled capability was granted")
	}
	if err := e.Require(Capability("deploy_to_mars")); err == nil {
		t.Fatal("an unknown capability satisfied Require")
	}
}

// TestRequireNamesTheMissingCapability keeps refusals actionable.
func TestRequireNamesTheMissingCapability(t *testing.T) {
	err := New(config.Permissions{}).Require(RunTests)
	if err == nil {
		t.Fatal("Require accepted a denied capability")
	}
	if !strings.Contains(err.Error(), "run_tests") {
		t.Fatalf("refusal does not name the capability: %v", err)
	}
}

// TestGuardsAreNeverInstalledInsideTheCandidate pins the bug the force-push
// test exposed. Hooks inside the worktree are committed by the worker's own
// `git add .`, deleted by any `git reset --hard` past that commit — silently
// disarming the guard — and show up in the candidate diff as a stray file.
func TestGuardsAreNeverInstalledInsideTheCandidate(t *testing.T) {
	workspace := t.TempDir()
	e := New(config.Permissions{})

	if _, err := e.Enforce(filepath.Join(workspace, "hooks"), workspace); err == nil {
		t.Fatal("guards were installed inside the candidate worktree")
	}
	if _, err := e.Enforce(filepath.Join(workspace, "nested", "deep"), workspace); err == nil {
		t.Fatal("a nested path inside the candidate was accepted")
	}
	if _, err := e.Enforce(t.TempDir(), workspace); err != nil {
		t.Fatalf("a guard dir outside the candidate was refused: %v", err)
	}
}

// TestGuardDirIsOutsideTheWorktreeByConstruction checks the helper callers use,
// so the safe location is the easy one to reach for.
func TestGuardDirIsOutsideTheWorktreeByConstruction(t *testing.T) {
	root := t.TempDir()
	worktree := filepath.Join(root, ".sensei-code", "worktrees", "task-1")
	guard := GuardDir(root, "task-1")

	rel, err := filepath.Rel(worktree, guard)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(rel, "..") {
		t.Fatalf("GuardDir %q is inside the worktree %q", guard, worktree)
	}
	if _, err := New(config.Permissions{}).Enforce(guard, worktree); err != nil {
		t.Fatalf("the helper's own location was refused by Enforce: %v", err)
	}
	// A task id that tries to escape must not place guards somewhere arbitrary.
	if got := GuardDir(root, "../../etc"); !strings.HasPrefix(got, root) {
		t.Fatalf("a traversing task id escaped the repository: %q", got)
	}
}

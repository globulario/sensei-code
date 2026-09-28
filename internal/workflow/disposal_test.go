package workflow

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/candidate"
)

// Finding 2 of the 2026-08-21 audit. Disposal consulted tc.EvidenceSnapshot,
// which is written only after validation and a decodable Sensei audit. Every
// earlier exit left it zeroed while the worktree held real work, and the zero
// was read as "the candidate holds no work" -- so the worktree and branch were
// deleted and that sentence was recorded as the reason.
//
// These run against a real git repository rather than asserting on source text,
// because the defect was that a correct-looking branch consulted the wrong
// source. Only observation distinguishes the two.

func newRepo(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-qm", "base")
	return dir, run("rev-parse", "HEAD")
}

func candidateIdentityFor(base string) candidate.Identity {
	return candidate.Identity{BaseSHA: base}
}

func TestAnUntouchedCandidateIsSeenAsEmpty(t *testing.T) {
	dir, base := newRepo(t)
	seen := observeCandidate(context.Background(), dir, base)
	if seen.Err != nil {
		t.Fatalf("observation failed: %v", seen.Err)
	}
	if seen.HoldsWork() {
		t.Fatalf("an unmodified candidate is reported as holding work: %+v", seen)
	}
}

// The defect, exactly: work on disk, snapshot empty. Before the fix this was
// deleted and recorded as holding nothing.
func TestAModifiedCandidateHoldsWorkEvenWithAnEmptySnapshot(t *testing.T) {
	dir, base := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\nthe worker's work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := observeCandidate(context.Background(), dir, base)
	if seen.Err != nil {
		t.Fatalf("observation failed: %v", seen.Err)
	}
	if !seen.HoldsWork() {
		t.Fatal("a candidate with an edited file is reported as holding no work")
	}
	if seen.DiffBytes == 0 || len(seen.ChangedPaths) == 0 {
		t.Fatalf("the observation carries no detail: %+v", seen)
	}

	// The stale snapshot the run would have had at an early exit.
	tc := &taskContext{}
	if tc.EvidenceSnapshot.DiffBytes != 0 {
		t.Fatal("test setup is wrong: the snapshot should start empty")
	}
	// The old code decided from this, and it says "no work".
	if !candidateEvidence(candidateIdentityFor(base), tc).ProducedNoWork {
		t.Fatal("test setup is wrong: the stale snapshot should read as no work")
	}
	// Observation must disagree, and observation is what disposal uses.
	if !seen.HoldsWork() {
		t.Fatal("disposal would still delete a candidate holding work")
	}
}

// A newly created, uncommitted file is work too: --intent-to-add is what makes
// it visible, and losing it is the same loss.
func TestANewUntrackedFileCountsAsWork(t *testing.T) {
	dir, base := newRepo(t)
	if err := os.WriteFile(filepath.Join(dir, "new.go"), []byte("package p\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seen := observeCandidate(context.Background(), dir, base)
	if seen.Err != nil {
		t.Fatalf("observation failed: %v", seen.Err)
	}
	if !seen.HoldsWork() {
		t.Fatal("an untracked new file is reported as no work")
	}
	var found bool
	for _, p := range seen.ChangedPaths {
		if strings.Contains(p, "new.go") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the new file is not named in the observation: %v", seen.ChangedPaths)
	}
}

// Deletion is irreversible and observation failed, so the destroying branch must
// not be reachable from an answer nobody established.
func TestAnUnreadableCandidateHoldsWork(t *testing.T) {
	for _, tc := range []struct {
		name      string
		workspace string
		base      string
	}{
		{"missing worktree", filepath.Join(t.TempDir(), "gone"), "abc123"},
		{"no workspace", "", "abc123"},
		{"no base", t.TempDir(), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seen := observeCandidate(context.Background(), tc.workspace, tc.base)
			if seen.Err == nil {
				t.Fatal("an unreadable candidate reported no error")
			}
			if !seen.HoldsWork() {
				t.Fatal("an unreadable candidate is eligible for automatic deletion")
			}
		})
	}
}

// Disposal must read the candidate, not replay the snapshot.
func TestDisposalObservesRatherThanRecalls(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "disposeIfEmpty")
	if !strings.Contains(body, "observeCandidate") {
		t.Fatal("disposal no longer observes the candidate")
	}
	if strings.Contains(body, "candidateEvidence") && !strings.Contains(body, "observeCandidate") {
		t.Fatal("disposal decides from the recalled snapshot again")
	}
}

// Disposal is reached by the very paths that cancel the context -- a stop, a
// timeout. If observation inherited that cancellation every interrupted run
// would be unreadable, every unreadable candidate is retained, and the
// leftovers automatic cleanup exists to prevent would return through the door
// marked safety.
func TestObservationSurvivesTheCancellationThatTriggeredIt(t *testing.T) {
	dir, base := newRepo(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // exactly the state a stopped run reaches disposal in

	seen := observeCandidate(ctx, dir, base)
	if seen.Err != nil {
		t.Fatalf("a cancelled context made the candidate unreadable: %v", seen.Err)
	}
	if seen.HoldsWork() {
		t.Fatal("an empty candidate would be retained after a stop, so cleanup never happens")
	}

	// And it still sees work when there is work.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\nwork\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !observeCandidate(ctx, dir, base).HoldsWork() {
		t.Fatal("work in a stopped run's candidate is not seen")
	}
}

// R15b W1 + W2. The accept path's decision recording, driven against a clean
// canonical git repository with a fake `sensei` on PATH (the exec pattern of
// derivedAnchorNaming, placed on PATH because decision.Write resolves the
// binary by name). The fake behaves like `sensei propose`: it appends under
// --target-repo's docs/awareness/ and stages the file unless --no-stage is
// given. The only ignore rule in play is the repository's own .sensei-code/
// entry, committed as the real .gitignore commits it; the cleanliness read is
// plain `git status --porcelain`.
//
// W1 is an absence: the canonical tracked status is exactly what it was, and
// the record exists at the task-owned location. W2 is its reader: the human
// promotion step is told where the pending record is by the DecisionRecorded
// summary, and the record is found at that path.
func TestAnAcceptedDecisionLeavesTheCanonicalCheckoutUnchangedAndIsFoundWherePromotionLooks(t *testing.T) {
	dir, _ := newRepo(t)
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return string(out)
	}
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(".sensei-code/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", ".gitignore")
	git("commit", "-qm", "ignore owned state")

	bin := t.TempDir()
	fake := `#!/bin/sh
target=""; title=""; stage=1
while [ $# -gt 0 ]; do
  case "$1" in
    --target-repo) target="$2"; shift ;;
    --title) title="$2"; shift ;;
    --no-stage) stage=0 ;;
  esac
  shift
done
f="$target/docs/awareness/architecture/decisions.yaml"
mkdir -p "$(dirname "$f")" || exit 1
printf 'decisions:\n  - title: %s\n' "$title" >> "$f" || exit 1
if [ "$stage" = 1 ]; then git -C "$target" add -f "$f" || exit 1; fi
`
	if err := os.WriteFile(filepath.Join(bin, "sensei"), []byte(fake), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	e := recordingEngine(t)
	e.Repo.Root = dir
	read, done := collect(t, e.Bus)
	defer done()

	ctx := context.Background()
	before := git("status", "--porcelain")
	if clean, err := e.Repo.IsClean(ctx); err != nil || !clean || before != "" {
		t.Fatalf("specimen is not a clean canonical checkout: clean=%v err=%v status=%q", clean, err, before)
	}

	const title = "record the owned decision"
	e.recordDecision(ctx, "task-r15b", &taskContext{
		Task: "the task", Rationale: title, Invariants: []string{"inv.one"}, Domain: "github.com/x/y",
	}, certifiedStart{}, []string{"a.txt"})

	// W1: the canonical tracked status is unchanged -- nothing staged,
	// modified or untracked.
	if after := git("status", "--porcelain"); after != before {
		t.Fatalf("recording the decision changed the canonical checkout:\n%s", after)
	}
	if clean, err := e.Repo.IsClean(ctx); err != nil || !clean {
		t.Fatalf("the canonical checkout is not clean after the decision: clean=%v err=%v", clean, err)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs", "awareness")); !os.IsNotExist(err) {
		t.Fatalf("the decision reached the canonical docs/awareness/: %v", err)
	}
	owned := filepath.Join(dir, ".sensei-code", "decisions", "task-r15b", "docs", "awareness", "architecture", "decisions.yaml")
	if body, err := os.ReadFile(owned); err != nil || !strings.Contains(string(body), title) {
		t.Fatalf("the decision is not at its owned location %s: %v %q", owned, err, body)
	}

	// W2: the promotion step reads the location the recorded event names.
	var summary string
	for _, ev := range read() {
		if strings.HasPrefix(ev.Summary, "architectural decision recorded for review:") {
			summary = ev.Summary
		}
	}
	const marker = "pending promotion at "
	i := strings.Index(summary, marker)
	if i < 0 || !strings.HasSuffix(summary, ")") {
		t.Fatalf("the recorded decision does not name where it is pending: %q", summary)
	}
	named := summary[i+len(marker) : len(summary)-1]
	if named != owned {
		t.Fatalf("promotion is pointed at %s, the record is at %s", named, owned)
	}
	if body, err := os.ReadFile(named); err != nil || !strings.Contains(string(body), title) {
		t.Fatalf("the promotion step does not find the record at %s: %v %q", named, err, body)
	}
}

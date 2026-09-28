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

// R15b W1, W2 and W4. An accepted run used to append its architectural decision
// to the canonical checkout's docs/awareness/architecture/decisions.yaml and
// stage it, so every accepted run left the human's checkout dirty and #216's
// truthful DIRTY_CANONICAL refusal then refused the task's own resume.
//
// decision.Write execs `sensei` from PATH, so the stand-in goes on PATH (the
// confinementRepo stand-in uses SENSEI_BIN, which this writer does not read).
// It behaves as the real propose does: it appends beneath --target-repo and
// stages what it wrote unless told --no-stage. The real command was
// characterized writing to a non-git target and to an ignored directory with
// --no-stage before this was written.
func TestAnAcceptedDecisionLeavesTheCanonicalCheckoutClean(t *testing.T) {
	ctx := context.Background()
	dir, base := newRepo(t)
	// The committed .gitignore states .sensei-code/ is not source; the fixture
	// states it where a fixture can without committing a file.
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte("/.sensei-code/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ntarget=\"\"; title=\"\"; stage=1\n" +
		"while [ $# -gt 0 ]; do case \"$1\" in --target-repo) target=\"$2\"; shift;; --title) title=\"$2\"; shift;; --no-stage) stage=0;; esac; shift; done\n" +
		"[ -n \"$target\" ] || exit 2\n" +
		"f=\"$target/docs/awareness/architecture/decisions.yaml\"\n" +
		"mkdir -p \"$(dirname \"$f\")\"\n" +
		"[ -f \"$f\" ] || echo decisions: > \"$f\"\n" +
		"printf '  - title: %s\\n' \"$title\" >> \"$f\"\n" +
		"if [ $stage = 1 ]; then git -C \"$target\" add \"$f\" || exit 1; fi\n"
	if err := os.WriteFile(filepath.Join(bin, "sensei"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	e := recordingEngine(t)
	e.Repo.Root = dir
	events, done := collect(t, e.Bus)
	defer done()
	// The task holds a candidate identity, so #216's resume precondition reads
	// this checkout exactly as it would for the task's own resume.
	id := candidateIdentityFor(base)
	id.TaskID = "task-7"
	if err := id.Save(dir); err != nil {
		t.Fatal(err)
	}
	status := func() string {
		t.Helper()
		out, err := exec.Command("git", "-C", dir, "status", "--porcelain").CombinedOutput()
		if err != nil {
			t.Fatalf("git status: %v\n%s", err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if s := status(); s != "" {
		t.Fatalf("the fixture is not a clean canonical checkout: %s", s)
	}

	tc := &taskContext{Task: "confine the decision", Rationale: "decision under owned state",
		Invariants: []string{"inv.one"}, Domain: "example.com/x"}
	e.recordDecision(ctx, "task-7", tc, certifiedStart{}, []string{"a.txt"})

	// W1: nothing tracked, staged or untracked appeared in the canonical checkout...
	if s := status(); s != "" {
		t.Fatalf("recording the decision modified the canonical checkout:\n%s", s)
	}
	if _, err := os.Stat(filepath.Join(dir, "docs")); !os.IsNotExist(err) {
		t.Fatalf("the decision was written into the canonical corpus: %v", err)
	}
	// ...so the task's own resume is not refused for a dirt its acceptance made.
	if err := e.resumePrecondition(ctx, "task-7"); err != nil {
		t.Fatalf("the task's own accepted decision trips its resume precondition: %v", err)
	}

	// W1 and W2: the record exists at the deterministic task-owned location,
	// and the event the promotion step reads names that location.
	pending := filepath.Join(dir, ".sensei-code", "decisions", "task-7", "docs", "awareness", "architecture", "decisions.yaml")
	var announced bool
	for _, ev := range events() {
		if ev.Kind != "decision.recorded" {
			continue
		}
		if !strings.Contains(ev.Summary, pending) || !strings.Contains(string(ev.Payload), pending) {
			t.Fatalf("the decision event does not name the pending location %s: %q %s", pending, ev.Summary, ev.Payload)
		}
		announced = true
	}
	if !announced {
		t.Fatal("no decision event was emitted, so the promotion step has nothing to follow")
	}
	body, err := os.ReadFile(pending)
	if err != nil || !strings.Contains(string(body), "decision under owned state") {
		t.Fatalf("the promotion step cannot find the record at %s (err=%v): %s", pending, err, body)
	}

	// W4 control: a genuinely dirty checkout is still refused as #216 refuses it.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("base\nhuman edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := e.resumePrecondition(ctx, "task-7").(*candidate.ErrDirtyCanonical); !ok {
		t.Fatal("a dirty canonical checkout was not refused with DIRTY_CANONICAL")
	}
}

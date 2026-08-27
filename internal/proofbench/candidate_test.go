package proofbench

// Attacks on candidate attribution.
//
// The proof-v5 COLD wave scored eleven governed arms on a directory that never
// received their work, and one recorded INCORRECT candidate passes the frozen
// oracle when the oracle is pointed at the real tree. These reproduce that
// defect and pin the repair.

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// governedArm builds the shape a governed run leaves behind: an arm checkout
// that is UNCHANGED, and a sibling candidate worktree holding the work.
func governedArm(t *testing.T) (repoRoot, armDir, taskID string) {
	t.Helper()
	repoRoot = t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git(repoRoot, "init", "-q")
	git(repoRoot, "config", "user.email", "probe@example.invalid")
	git(repoRoot, "config", "user.name", "probe")
	if err := os.WriteFile(filepath.Join(repoRoot, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repoRoot, "add", ".")
	git(repoRoot, "commit", "-qm", "base")

	// The arm checkout.
	armDir = filepath.Join(t.TempDir(), "1")
	git(repoRoot, "worktree", "add", "--detach", "-q", armDir)

	// The candidate, created FROM the arm the way the engine creates it.
	taskID = "task-1787679380239385302"
	candidate := filepath.Join(filepath.Dir(armDir), "."+filepath.Base(armDir)+"-worktrees", taskID)
	git(armDir, "worktree", "add", "--detach", "-q", candidate)

	// The work lands in the candidate. The arm stays untouched, which is the
	// whole shape of the defect.
	if err := os.WriteFile(filepath.Join(candidate, "main.go"),
		[]byte("package main\n\nfunc Delivered() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return repoRoot, armDir, taskID
}

func streamFor(taskID string) string {
	return `{"kind":"task.created","task_id":"` + taskID + `","summary":"a task"}` + "\n" +
		`{"kind":"workflow.completed","task_id":"` + taskID + `","summary":"candidate ready"}`
}

// The harness scores the candidate, not the arm checkout.
//
// This is the proof-v5 defect, reproduced: arm unchanged, sibling candidate
// changed. An evaluator that assumes the arm checkout reports an empty diff and
// judges code that is not there.
func TestTheCandidateIsScoredNotTheArmCheckout(t *testing.T) {
	repoRoot, armDir, taskID := governedArm(t)
	r := Runner{RepoRoot: repoRoot}

	cand, err := r.ResolveCandidate(context.Background(), armDir, ArmCold, streamFor(taskID), true)
	if err != nil {
		t.Fatalf("ResolveCandidate: %v", err)
	}
	if cand.Dir == armDir {
		t.Fatal("the arm checkout was resolved as the candidate; this is the proof-v5 defect")
	}
	if !strings.Contains(cand.Dir, taskID) {
		t.Errorf("the resolved candidate is not this run's: %s", cand.Dir)
	}
	if cand.Method != "git-worktree-list" {
		t.Errorf("method = %q; a registration is available and should have been used", cand.Method)
	}
	if cand.TaskID != taskID {
		t.Errorf("the candidate is not bound to the run's task id: %q", cand.TaskID)
	}

	// The work is visible from the resolved tree.
	body, err := os.ReadFile(filepath.Join(cand.Dir, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "func Delivered") {
		t.Error("the resolved candidate does not contain the arm's work")
	}
	// And its diff is not empty.
	if h := CandidateDiffHash(context.Background(), cand, ""); h == EmptyTreeHash {
		t.Error("the candidate's diff hashed empty although it holds work")
	}
}

// The negative control: evaluating the arm checkout gives the wrong answer.
//
// Without this, the test above could pass against an evaluator that happened to
// look in the right place for an unrelated reason. This states what the DEFECT
// produces, so the repair cannot quietly regress into it.
func TestEvaluatingTheArmCheckoutWouldGiveTheWrongAnswer(t *testing.T) {
	_, armDir, _ := governedArm(t)

	// The arm checkout is exactly as the run left it: untouched.
	out, err := exec.Command("git", "-C", armDir, "--no-optional-locks", "status", "--porcelain").Output()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Fatalf("the specimen is wrong: the arm checkout should be unchanged, got %q", out)
	}
	// So a hash taken from it is the empty-tree hash -- which is precisely what
	// all eleven proof-v5 COLD arms recorded.
	armAsCandidate := Candidate{Dir: armDir, Method: "arm-checkout"}
	if h := CandidateDiffHash(context.Background(), armAsCandidate, ""); h != EmptyTreeHash {
		t.Errorf("hashing the arm checkout gave %s; the defect this pins produces the empty hash", h)
	}
}

// An unresolvable candidate is an integrity failure, never a zero.
func TestAnUnresolvableCandidateIsNotAScore(t *testing.T) {
	repoRoot := t.TempDir()
	if out, err := exec.Command("git", "-C", repoRoot, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	r := Runner{RepoRoot: repoRoot}

	// No task id in the stream.
	if _, err := r.ResolveCandidate(context.Background(), repoRoot, ArmCold, "not a stream", true); err == nil {
		t.Error("a run whose stream carries no task id resolved a candidate anyway")
	}
	// A task id with no worktree anywhere.
	_, err := r.ResolveCandidate(context.Background(), repoRoot, ArmCold, streamFor("task-does-not-exist"), true)
	if err == nil {
		t.Fatal("a task with no candidate worktree resolved one anyway")
	}
	if !strings.Contains(err.Error(), "has not been measured") {
		t.Errorf("the failure does not say the arm is unmeasured: %v", err)
	}
	// A run that never reached a candidate has nothing to attribute, and that is
	// a delivery failure rather than an integrity failure. Getting this wrong
	// halted a v6 wave over runs the product had correctly declined to start.
	nd, err := r.ResolveCandidate(context.Background(), repoRoot, ArmCold, streamFor("task-refused"), false)
	if err != nil {
		t.Errorf("a run that produced no candidate was reported as an attribution failure: %v", err)
	}
	if nd.Dir != "" || nd.Method != "none-created" {
		t.Errorf("a candidate-less run resolved to %+v", nd)
	}

	// A RAW arm works in place and needs no resolution.
	c, err := r.ResolveCandidate(context.Background(), repoRoot, ArmRaw, "", true)
	if err != nil || c.Dir != repoRoot || c.Method != "arm-checkout" {
		t.Errorf("a RAW arm did not resolve to its own checkout: %+v %v", c, err)
	}
}

// The anomaly that caught proof-v5 is now caught by the harness.
//
// Eleven supposedly independent governed runs all producing the empty-tree hash
// is not a result. Nothing noticed at the time; something notices now.
func TestUniformEmptyCandidatesAreAnIntegrityFailure(t *testing.T) {
	var wave []Attempt
	for i := 0; i < 11; i++ {
		wave = append(wave, Attempt{Task: "t" + string(rune('a'+i)), Arm: ArmCold, Number: 1,
			Terminal: "workflow.completed",
			DiffHash: EmptyTreeHash, CandidateDir: "/tmp/somewhere"})
	}
	err := CheckAttributionAnomaly(wave)
	if err == nil {
		t.Fatal("a wave of eleven identical empty-tree hashes was accepted as a result")
	}
	if !strings.Contains(err.Error(), "measurement-integrity failure") {
		t.Errorf("the anomaly was not reported as an integrity failure: %v", err)
	}

	// One genuinely empty candidate among several is a RESULT, not an anomaly:
	// a run really can produce nothing, and refusing that would make the check
	// a quality gate on the product.
	// A REFUSED run carries no candidate and is not held to attribution.
	refused := []Attempt{
		{Task: "a", Arm: ArmCold, Number: 1, Terminal: "workflow.awaiting_authority"},
		{Task: "b", Arm: ArmCold, Number: 1, Terminal: "workflow.awaiting_authority"},
	}
	if err := CheckAttributionAnomaly(refused); err != nil {
		t.Errorf("runs that legitimately produced no candidate were refused: %v", err)
	}

	mixed := append([]Attempt(nil), wave[:3]...)
	mixed[0].DiffHash = "sha256:aaaa"
	mixed[1].DiffHash = "sha256:bbbb"
	if err := CheckAttributionAnomaly(mixed); err != nil {
		t.Errorf("a single empty candidate among several was refused: %v", err)
	}

	// An unbound candidate is refused whatever its hash: a verdict that cannot
	// name the tree it describes is not attributable.
	unbound := []Attempt{
		{Task: "a", Arm: ArmCold, Number: 1, Terminal: "workflow.completed", DiffHash: "sha256:aaaa"},
		{Task: "b", Arm: ArmCold, Number: 1, Terminal: "workflow.completed",
			DiffHash: "sha256:bbbb", CandidateDir: "/tmp/x"},
	}
	if err := CheckAttributionAnomaly(unbound); err == nil {
		t.Error("an attempt with no resolved candidate directory was accepted")
	}

	// RAW arms are exempt: they work in place, so an empty diff there is a
	// genuine "it changed nothing".
	raws := []Attempt{
		{Task: "a", Arm: ArmRaw, Number: 1, Terminal: "raw.completed", DiffHash: EmptyTreeHash},
		{Task: "b", Arm: ArmRaw, Number: 1, Terminal: "raw.completed", DiffHash: EmptyTreeHash},
	}
	if err := CheckAttributionAnomaly(raws); err != nil {
		t.Errorf("RAW arms were held to the governed-candidate rule: %v", err)
	}
}

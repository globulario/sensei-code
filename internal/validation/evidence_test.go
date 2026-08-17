package validation

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func at(seconds int) time.Time {
	return time.Date(2026, 8, 16, 12, 0, seconds, 0, time.UTC)
}

func runner(t *testing.T, permits func(CheckKind) (bool, string)) Runner {
	t.Helper()
	n := 0
	return Runner{
		Workspace: t.TempDir(),
		Permits:   permits,
		Now:       func() time.Time { n++; return at(n) },
	}
}

// TestEvidenceComesFromExecutionNotFromAReport is the property the whole slice
// rests on: a worker saying it ran the tests must not be expressible here.
func TestEvidenceComesFromExecutionNotFromAReport(t *testing.T) {
	r := runner(t, nil)
	b := r.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Test, Command: "sh", Args: []string{"-c", "exit 0"}},
		{Kind: Vet, Command: "sh", Args: []string{"-c", "echo problem >&2; exit 3"}},
	})

	if len(b.Checks) != 2 {
		t.Fatalf("expected two checks, got %d", len(b.Checks))
	}
	if b.Checks[0].Outcome != Passed {
		t.Fatalf("a zero exit was not recorded as passed: %+v", b.Checks[0])
	}
	if b.Checks[1].Outcome != Failed || b.Checks[1].ExitStatus != 3 {
		t.Fatalf("a non-zero exit was not recorded faithfully: %+v", b.Checks[1])
	}
	if !strings.Contains(b.Checks[1].Output, "problem") {
		t.Fatalf("the failure output was not captured: %q", b.Checks[1].Output)
	}
	// Requested and executed are different parties, which is what makes
	// worker-narrated evidence unrepresentable.
	if b.Checks[0].RequestedBy == b.Checks[0].ExecutedBy {
		t.Fatal("requester and executor are the same, so a worker could be recorded as its own evidence source")
	}
	if !strings.Contains(b.Checks[0].ExecutedBy, "broker") {
		t.Fatalf("evidence does not record the broker as executor: %q", b.Checks[0].ExecutedBy)
	}
}

// TestEvidenceIsBoundToExactCandidateContent covers the critical binding:
// evidence from candidate A must never certify candidate B.
func TestEvidenceIsBoundToExactCandidateContent(t *testing.T) {
	r := runner(t, nil)
	b := r.Run(context.Background(), "task-1", Digest("diff A"), []Check{
		{Kind: Test, Command: "sh", Args: []string{"-c", "exit 0"}},
	})

	if !b.Certifies("task-1", Digest("diff A")) {
		t.Fatal("evidence does not certify the candidate it was produced against")
	}
	if b.Certifies("task-1", Digest("diff B")) {
		t.Fatal("evidence certified different candidate content")
	}
	if b.Certifies("task-2", Digest("diff A")) {
		t.Fatal("evidence certified a different candidate id")
	}
	if b.Certifies("task-1", "") {
		t.Fatal("evidence certified an empty digest")
	}
}

// TestEvidenceGoesStaleWhenTheCandidateChanges is the formatter case. A check
// that rewrites the candidate means everything gathered before it describes
// different bytes.
func TestEvidenceGoesStaleWhenTheCandidateChanges(t *testing.T) {
	before := Digest("package x\nfunc  f(){}\n")
	after := Digest("package x\nfunc f() {}\n")

	r := runner(t, nil)
	b := r.Run(context.Background(), "task-1", before, []Check{
		{Kind: Test, Command: "sh", Args: []string{"-c", "exit 0"}},
	})
	if !b.Certifies("task-1", before) {
		t.Fatal("evidence does not certify the pre-format candidate")
	}
	if b.Certifies("task-1", after) {
		t.Fatal("evidence gathered before formatting still certified the reformatted candidate")
	}
}

// TestAPartiallyStaleBundleIsRefusedWholesale guards the shape that reads as
// complete while proving less than it appears to.
func TestAPartiallyStaleBundleIsRefusedWholesale(t *testing.T) {
	good := Evidence{CandidateID: "task-1", DiffDigest: "sha256:aaa", Outcome: Passed}
	stale := Evidence{CandidateID: "task-1", DiffDigest: "sha256:bbb", Outcome: Passed}
	b := Bundle{CandidateID: "task-1", DiffDigest: "sha256:aaa", Checks: []Evidence{good, stale}}

	if b.Certifies("task-1", "sha256:aaa") {
		t.Fatal("a bundle containing evidence from other bytes certified the candidate")
	}
}

// TestAnUnrunCheckIsNotAPass covers the two outcomes most likely to be read as
// success by accident.
func TestAnUnrunCheckIsNotAPass(t *testing.T) {
	denied := runner(t, func(k CheckKind) (bool, string) {
		return k != Test, "run_tests capability is not granted"
	})
	b := denied.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Test, Command: "sh", Args: []string{"-c", "exit 0"}},
	})
	if b.Checks[0].Outcome != NotPermitted {
		t.Fatalf("a denied check was recorded as %q", b.Checks[0].Outcome)
	}
	if b.Passed() {
		t.Fatal("a bundle whose only check was not permitted reported itself as passed")
	}
	if !strings.Contains(b.Checks[0].Detail, "run_tests") {
		t.Fatalf("the denial does not name the missing capability: %q", b.Checks[0].Detail)
	}

	missing := runner(t, nil)
	b = missing.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Build, Command: "this-binary-does-not-exist-anywhere"},
	})
	if b.Checks[0].Outcome != Errored {
		t.Fatalf("a check that could not run was recorded as %q", b.Checks[0].Outcome)
	}
	if b.Passed() {
		t.Fatal("a bundle whose check could not run reported itself as passed")
	}
}

// TestAnEmptyBundleIsNotAPass keeps silence from reading as success, which is
// the same mistake as an unrun check with fewer symptoms.
func TestAnEmptyBundleIsNotAPass(t *testing.T) {
	var b Bundle
	if b.Passed() {
		t.Fatal("a bundle with no checks reported itself as passed")
	}
	if b.Certifies("task-1", "sha256:abc") {
		t.Fatal("an empty bundle certified a candidate")
	}
	if !strings.Contains(b.Render(), "nothing here has been verified") {
		t.Fatalf("an empty bundle renders as though it said something: %q", b.Render())
	}
}

// TestRenderGivesTheReviewerWhatItAskedFor checks the output a reviewer reads.
// The canary's reviewer refused three times for want of exactly this.
func TestRenderGivesTheReviewerWhatItAskedFor(t *testing.T) {
	r := runner(t, nil)
	b := r.Run(context.Background(), "task-9", Digest("diff"), []Check{
		{Kind: Format, Command: "sh", Args: []string{"-c", "exit 0"}},
		{Kind: Vet, Command: "sh", Args: []string{"-c", "echo 'engine.go:12: unreachable code' >&2; exit 1"}},
	})
	out := b.Render()

	for _, want := range []string{
		"task-9",              // which candidate
		short(Digest("diff")), // which bytes
		"not reported by the worker",
		"passed",
		// The outcome names the attribution, so a reviewer reading the render
		// can tell a defect in the candidate from a broken environment.
		"candidate-failure",
		"engine.go:12: unreachable code", // the actionable detail
		"not proven",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered evidence is missing %q:\n%s", want, out)
		}
	}
}

// TestPassingChecksDoNotBuryTheReviewerInOutput keeps a green run terse, so the
// failures are the thing that stands out.
func TestPassingChecksDoNotBuryTheReviewerInOutput(t *testing.T) {
	r := runner(t, nil)
	// The marker is computed by the command rather than written in its
	// arguments, because Render prints the argument list and an echoed literal
	// would look like leaked output when it is only the command being shown.
	b := r.Run(context.Background(), "task-1", Digest("d"), []Check{
		{Kind: Test, Command: "sh", Args: []string{"-c", "echo $((6 * 7)); exit 0"}},
	})
	if strings.Contains(b.Render(), "42") {
		t.Fatal("a passing check dumped its output into the reviewer prompt")
	}
	// It is still captured on the evidence itself, and still digested.
	if !strings.Contains(b.Checks[0].Output, "42") {
		t.Fatal("passing output was discarded rather than merely not rendered")
	}
	if b.Checks[0].OutputDigest == "" {
		t.Fatal("output was captured without a digest, so truncation is indistinguishable from editing")
	}
}

// TestFailuresAreListedForTheCaller gives the workflow a typed way to act.
func TestFailuresAreListedForTheCaller(t *testing.T) {
	r := runner(t, nil)
	b := r.Run(context.Background(), "task-1", Digest("d"), []Check{
		{Kind: Vet, Command: "sh", Args: []string{"-c", "exit 0"}},
		{Kind: Test, Command: "sh", Args: []string{"-c", "exit 1"}},
	})
	f := b.Failures()
	if len(f) != 1 || f[0].Kind != Test {
		t.Fatalf("failures were not reported accurately: %+v", f)
	}
}

// TestAFailureTheCandidateCannotAffectIsNotACandidateFailure is the invariant a
// real run established: a mandatory check must test a property the candidate
// can influence, and one that cannot manufactures an impossible revision loop.
//
// Attribution is empirical rather than a guess about which error text looks
// environmental: the same command is run against a clean checkout of the base,
// and a check that fails identically there was not broken by this candidate.
func TestAFailureTheCandidateCannotAffectIsNotACandidateFailure(t *testing.T) {
	base := t.TempDir()
	r := runner(t, nil)
	r.Baseline = func() (string, error) { return base, nil }

	// Fails everywhere, base included: an environment problem.
	b := r.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Build, Command: "sh", Args: []string{"-c", "echo 'error obtaining VCS status' >&2; exit 1"}},
	})
	got := b.Checks[0]
	if got.Outcome != Infrastructure {
		t.Fatalf("a failure reproducible on the base was recorded as %q", got.Outcome)
	}
	if got.Attribution != "pre-existing" {
		t.Fatalf("attribution is %q", got.Attribution)
	}
	if len(b.CandidateFailures()) != 0 {
		t.Fatal("an infrastructure failure was offered as something the worker can fix")
	}
	if len(b.Unactionable()) != 1 {
		t.Fatal("the infrastructure failure was not reported as unactionable")
	}
	// It still blocks acceptance: nothing was proven about the candidate.
	if b.Passed() {
		t.Fatal("an infrastructure failure was treated as a pass")
	}
	if !strings.Contains(b.Render(), "Do not ask the worker to revise code for these") {
		t.Fatalf("the reviewer is not told this is unactionable:\n%s", b.Render())
	}
}

// TestARealCandidateFailureIsStillAttributedToTheCandidate keeps attribution
// from becoming a blanket excuse.
func TestARealCandidateFailureIsStillAttributedToTheCandidate(t *testing.T) {
	base := t.TempDir()
	r := runner(t, nil)
	r.Baseline = func() (string, error) { return base, nil }

	// Fails only in the candidate workspace, because the marker file is there.
	marker := "candidate-only"
	if err := writeFile(r.Workspace, marker); err != nil {
		t.Fatal(err)
	}
	b := r.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Test, Command: "sh", Args: []string{"-c", "test ! -f " + marker}},
	})
	got := b.Checks[0]
	if got.Outcome != Failed {
		t.Fatalf("a failure unique to the candidate was recorded as %q", got.Outcome)
	}
	if got.Attribution != "candidate" {
		t.Fatalf("attribution is %q", got.Attribution)
	}
	if len(b.CandidateFailures()) != 1 {
		t.Fatal("a genuine candidate failure was not offered as actionable")
	}
}

// TestUnattributedIsSaidRatherThanGuessed keeps the honest third answer. With no
// baseline the question was not asked, and reporting either verdict would be
// inventing one.
func TestUnattributedIsSaidRatherThanGuessed(t *testing.T) {
	r := runner(t, nil) // no Baseline
	b := r.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Build, Command: "sh", Args: []string{"-c", "exit 1"}},
	})
	if got := b.Checks[0].Attribution; got != "unattributed" {
		t.Fatalf("attribution without a baseline is %q, want unattributed", got)
	}
	if !strings.Contains(b.Checks[0].Detail, "not attributed") {
		t.Fatalf("the detail does not say the question was unanswered: %q", b.Checks[0].Detail)
	}
}

func writeFile(dir, name string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644)
}

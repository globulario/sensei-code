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

// TestACheckThatReportsByPrintingIsNotASilentPass covers verifiers that exit
// zero whatever they find.
//
// `gofmt -l` lists unformatted files and exits zero either way, so reading only
// the exit status records a pass for a candidate that is not formatted. This is
// the shape of a check that looks green and proves nothing.
func TestACheckThatReportsByPrintingIsNotASilentPass(t *testing.T) {
	base := t.TempDir()
	r := runner(t, nil)
	r.Baseline = func() (string, error) { return base, nil }

	b := r.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Format, Command: "sh", Args: []string{"-c", "echo internal/tui/model.go; exit 0"}, FailIfOutput: true},
	})
	if b.Checks[0].Outcome == Passed {
		t.Fatal("a verifier that printed a problem and exited zero was recorded as passed")
	}
	if !strings.Contains(b.Checks[0].Output, "internal/tui/model.go") {
		t.Fatalf("the reported file did not reach the evidence: %q", b.Checks[0].Output)
	}

	// Silence from the same check is a genuine pass.
	quiet := r.Run(context.Background(), "task-1", "sha256:abc", []Check{
		{Kind: Format, Command: "sh", Args: []string{"-c", "exit 0"}, FailIfOutput: true},
	})
	if quiet.Checks[0].Outcome != Passed {
		t.Fatalf("a verifier that found nothing was recorded as %q", quiet.Checks[0].Outcome)
	}
}

// requiredTestModule writes a tiny Go module into the runner's workspace whose
// package holds one passing test, one failing test and one skipped test, so a
// required test is executed for real rather than simulated.
func requiredTestModule(t *testing.T, r Runner) {
	t.Helper()
	dir := filepath.Join(r.Workspace, "pkg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(r.Workspace, "go.mod"): "module example.com/required\n\ngo 1.21\n",
		filepath.Join(dir, "x_test.go"): "package pkg\n\nimport \"testing\"\n\n" +
			"func TestPasses(t *testing.T) { t.Run(\"sub\", func(t *testing.T) {}) }\n\n" +
			"func TestFails(t *testing.T) { t.Fatal(\"required behaviour broken\") }\n\n" +
			"func TestSkips(t *testing.T) { t.Skip(\"not here\") }\n",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A named required test is executed on its own, recorded under its canonical
// id, and bound to the exact candidate content it ran against. Fails if the
// broker records the suite instead of the named test, keeps the class prefix,
// or lets the record certify other bytes or another candidate.
func TestARequiredTestIsExecutedByNameAndBoundToTheCandidate(t *testing.T) {
	r := runner(t, nil)
	requiredTestModule(t, r)
	got := r.RunRequiredTests(context.Background(), "task-1", Digest("diff A"),
		[]string{"test:pkg/x_test.go:TestPasses", "pkg/x_test.go:TestPasses"})
	if len(got) != 1 {
		t.Fatalf("one named test given twice must yield one record, got %d: %+v", len(got), got)
	}
	rec := got[0]
	if rec.ID != "pkg/x_test.go:TestPasses" {
		t.Fatalf("the record is not keyed by the canonical id: %q", rec.ID)
	}
	if !rec.Executed || !rec.Passed {
		t.Fatalf("a passing named test was not recorded executed and passing: %+v", rec)
	}
	if !strings.Contains(strings.Join(rec.Evidence.Args, " "), "^TestPasses$") {
		t.Fatalf("the broker did not run the named test on its own: %v", rec.Evidence.Args)
	}
	if !strings.Contains(rec.Evidence.ExecutedBy, "broker") {
		t.Fatalf("the record does not name the broker as executor: %q", rec.Evidence.ExecutedBy)
	}
	if !rec.Discharges("test:pkg/x_test.go:TestPasses", "task-1", Digest("diff A")) {
		t.Fatal("an executed, passing named test did not discharge itself for its own candidate")
	}
	if rec.Discharges("pkg/x_test.go:TestPasses", "task-1", Digest("diff B")) {
		t.Fatal("the record discharged the test for different candidate content")
	}
	if rec.Discharges("pkg/x_test.go:TestPasses", "task-2", Digest("diff A")) {
		t.Fatal("the record discharged the test for a different candidate")
	}
	if rec.Discharges("pkg/x_test.go:TestFails", "task-1", Digest("diff A")) {
		t.Fatal("a record for one test discharged a different test id")
	}
}

// A required test that did not run is not discharged, even when the command
// that was asked to run it exited zero. Fails if a zero exit, a skip, a denied
// capability or a malformed id is ever read as the named test having passed.
func TestAnUnexecutedRequiredTestIsNotDischarged(t *testing.T) {
	r := runner(t, nil)
	requiredTestModule(t, r)
	got := r.RunRequiredTests(context.Background(), "task-1", Digest("diff A"),
		[]string{"pkg/x_test.go:TestAbsent", "pkg/x_test.go:TestSkips", "not-a-test-id"})
	if len(got) != 3 {
		t.Fatalf("every named test must be recorded, even unrun ones; got %d: %+v", len(got), got)
	}
	if got[0].Evidence.Outcome != Passed {
		t.Fatalf("precondition: `go test -run` over a missing test exits zero; got %+v", got[0].Evidence)
	}
	for _, rec := range got {
		if rec.Executed || rec.Passed {
			t.Errorf("%s was recorded executed/passed without its own verdict line: %+v", rec.ID, rec)
		}
		if rec.Discharges(rec.ID, "task-1", Digest("diff A")) {
			t.Errorf("%s was discharged without being executed", rec.ID)
		}
	}

	denied := runner(t, func(CheckKind) (bool, string) { return false, "run_tests not granted" })
	requiredTestModule(t, denied)
	rec := denied.RunRequiredTests(context.Background(), "task-1", Digest("diff A"), []string{"pkg/x_test.go:TestPasses"})[0]
	if rec.Evidence.Outcome != NotPermitted || rec.Executed || rec.Discharges(rec.ID, "task-1", Digest("diff A")) {
		t.Fatalf("a required test the envelope did not permit was discharged or recorded as run: %+v", rec)
	}
}

// A required test that ran and failed is not discharged by having executed.
// Fails if Executed alone, or a non-zero exit, is ever treated as satisfying it.
func TestAFailingRequiredTestIsNotDischarged(t *testing.T) {
	r := runner(t, nil)
	requiredTestModule(t, r)
	rec := r.RunRequiredTests(context.Background(), "task-1", Digest("diff A"), []string{"pkg/x_test.go:TestFails"})[0]
	if !rec.Executed {
		t.Fatalf("a failing named test was not recorded as executed: %+v", rec)
	}
	if rec.Passed || rec.Evidence.Outcome == Passed {
		t.Fatalf("a failing named test was recorded as passing: %+v", rec)
	}
	if rec.Discharges(rec.ID, "task-1", Digest("diff A")) {
		t.Fatal("a required test that ran and failed was discharged")
	}
	if !strings.Contains(RenderRequiredTests([]RequiredTest{rec}), "FAILED") {
		t.Fatalf("the rendering does not say the required test failed:\n%s", RenderRequiredTests([]RequiredTest{rec}))
	}
}

// PRE-REPAIR WITNESSES: the broker-level witnesses and controls.
//
// Each fixture is a real Go module in the candidate workspace and another in a
// separate "recorded base" directory. The named test reads its package's
// state.txt and passes only when it says "repaired"; every run also leaves a
// ran.txt marker in the workspace it executed in, so a control can assert which
// legs were actually reached rather than inferring it from a refusal.

const witnessTestID = "w/w_test.go:TestWitness"

func witnessModule(t *testing.T, dir, state string) {
	t.Helper()
	pkg := filepath.Join(dir, "w")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(dir, "go.mod"):    "module example.com/witness\n\ngo 1.21\n",
		filepath.Join(pkg, "state.txt"): state,
		filepath.Join(pkg, "w_test.go"): "package w\n\nimport (\n\t\"os\"\n\t\"testing\"\n)\n\n" +
			"func TestWitness(t *testing.T) {\n\tos.WriteFile(\"ran.txt\", []byte(\"x\"), 0o644)\n" +
			"\tb, _ := os.ReadFile(\"state.txt\")\n\tif string(b) != \"repaired\" {\n\t\tt.Fatal(\"the behaviour is broken\")\n\t}\n}\n",
	}
	for path, body := range files {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func ranIn(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, "w", "ran.txt"))
	return err == nil
}

// brokerCalls records what the runner's downstream doubles were asked.
type brokerCalls struct {
	permits  []CheckKind
	baseline int
}

// witnessRunner is a runner over a candidate module in state cand and a base
// module in state base. The Test permit is granted when allow is set; when
// mustNotReach is set, reaching either double fails the test, so a control
// proves the refusal happened BEFORE production.
func witnessRunner(t *testing.T, cand, base string, allow, mustNotReach bool) (Runner, string, *brokerCalls) {
	t.Helper()
	calls := &brokerCalls{}
	r := runner(t, func(k CheckKind) (bool, string) {
		calls.permits = append(calls.permits, k)
		if mustNotReach {
			t.Errorf("the capability check was reached for a request that must be refused before production")
		}
		if !allow {
			return false, "run_tests not granted"
		}
		return true, ""
	})
	baseDir := t.TempDir()
	witnessModule(t, r.Workspace, cand)
	witnessModule(t, baseDir, base)
	r.Baseline = func() (string, error) {
		calls.baseline++
		if mustNotReach {
			t.Errorf("the recorded base was checked out for a request that must be refused before production")
		}
		return baseDir, nil
	}
	return r, baseDir, calls
}

func witnessRequest(testID string) PreRepairRequest {
	return PreRepairRequest{
		CandidateID: "task-1", BaseSHA: "base-sha-1", DiffDigest: Digest("diff A"),
		FindingID: "f2", FindingClass: "evidence",
		Subject: PreRepairSubject{Kind: NamedGoTest, TestID: testID},
	}
}

// W1 THE CONTRAST. The named test PASSES on the repaired candidate and FAILS on
// the recorded base, for its own assertion: the broker returns one witness
// holding both executions, the base leg naming the recorded base as what it
// executed against, bound to the task, the diff and the finding.
//
// Fails if either leg is not the named test's own verdict, if the base leg
// names the candidate, or if the witness contrasts any other candidate,
// base, diff or finding.
func TestW1APreRepairWitnessIsTheContrastOfTwoBrokerExecutions(t *testing.T) {
	r, baseDir, calls := witnessRunner(t, "repaired", "broken", true, false)
	w, ok, why := r.RunPreRepairWitness(context.Background(), witnessRequest("test:"+witnessTestID))
	if !ok {
		t.Fatalf("a test passing on the candidate and failing on the base produced no witness: %s", why)
	}
	if len(calls.permits) != 1 || calls.permits[0] != Test || calls.baseline != 1 || !ranIn(r.Workspace) || !ranIn(baseDir) {
		t.Fatalf("the witness was not produced by executing on both sides under the Test permit: %+v", calls)
	}
	if w.Purpose != PreRepair || w.SubjectKind != NamedGoTest || w.Subject != witnessTestID ||
		w.CandidateID != "task-1" || w.BaseSHA != "base-sha-1" || w.DiffDigest != Digest("diff A") ||
		w.FindingID != "f2" || w.FindingClass != "evidence" {
		t.Fatalf("the witness is not bound to its request: %+v", w)
	}
	want := "go test -count=1 -v -run ^TestWitness$ ./w"
	if w.Candidate.Command != want || w.Base.Command != want {
		t.Fatalf("the argv was not synthesized from the identity: %q / %q", w.Candidate.Command, w.Base.Command)
	}
	c, b := w.Candidate, w.Base
	if c.Target != OnCandidate || c.TargetIdentity != Digest("diff A") || !c.Executed || c.Outcome != LegPassed || c.ExitStatus != 0 ||
		!strings.Contains(c.Output, "--- PASS: TestWitness") || !strings.HasPrefix(c.OutputDigest, "sha256:") {
		t.Fatalf("the candidate leg is not the named test passing on the candidate: %+v", c)
	}
	if b.Target != OnRecordedBase || b.TargetIdentity != "base-sha-1" || !b.Executed || b.Outcome != LegFailed || b.ExitStatus == 0 ||
		!strings.Contains(b.Output, "--- FAIL: TestWitness") || !strings.HasPrefix(b.OutputDigest, "sha256:") {
		t.Fatalf("the base leg is not the named test failing on the recorded base: %+v", b)
	}
	if !w.Contrasts("task-1", "base-sha-1", Digest("diff A"), "f2") {
		t.Fatal("the witness does not contrast its own candidate")
	}
	for name, args := range map[string][4]string{
		"another diff":    {"task-1", "base-sha-1", Digest("diff B"), "f2"},
		"another base":    {"task-1", "base-sha-2", Digest("diff A"), "f2"},
		"another task":    {"task-2", "base-sha-1", Digest("diff A"), "f2"},
		"another finding": {"task-1", "base-sha-1", Digest("diff A"), "f3"},
	} {
		if w.Contrasts(args[0], args[1], args[2], args[3]) {
			t.Fatalf("%s: a witness transferred to other bytes or another finding", name)
		}
	}
}

// W2 ENVIRONMENTAL CONTROL -- THE TRAP. The named test fails on the candidate
// AND on the base. Both legs are reached and executed, and there is no witness:
// a failure the base shares is an environment problem, not a red phase. The
// same failure through Run is still Infrastructure / pre-existing (W8).
//
// Fails if candidate FAIL + base FAIL is laundered into a pre-repair witness,
// or if the control passes because a leg was never reached.
func TestW2AFailureSharedWithTheBaseIsNoPreRepairWitness(t *testing.T) {
	r, baseDir, calls := witnessRunner(t, "broken", "broken", true, false)
	w, ok, why := r.RunPreRepairWitness(context.Background(), witnessRequest(witnessTestID))
	if len(calls.permits) != 1 || calls.permits[0] != Test || calls.baseline != 1 {
		t.Fatalf("the permit and the baseline were not both asked: %+v", calls)
	}
	if !ranIn(r.Workspace) || !ranIn(baseDir) {
		t.Fatalf("both legs were not executed (candidate %v, base %v)", ranIn(r.Workspace), ranIn(baseDir))
	}
	if ok || w.Purpose != "" || !strings.Contains(why, "did not pass on the candidate") {
		t.Fatalf("a failure shared with the base produced a witness: ok=%v %+v (%s)", ok, w, why)
	}

	// W8: attribution keeps its meaning for the same execution.
	e := r.Run(context.Background(), "task-1", Digest("diff A"), []Check{
		{Kind: Test, Command: "go", Args: []string{"test", "-count=1", "-v", "-run", "^TestWitness$", "./w"}},
	}).Checks[0]
	if e.Outcome != Infrastructure || e.Attribution != "pre-existing" {
		t.Fatalf("attribute no longer classifies a failure shared with the base as pre-existing infrastructure: %+v", e)
	}
}

// W3 NO-RED CONTROL. The named test passes on the candidate and on the base.
// Both legs execute and there is no witness: asking for a failing-first run
// cannot manufacture one.
//
// Fails if a base PASS, or the request alone, yields a red artifact.
func TestW3APassOnBothSidesIsNoPreRepairWitness(t *testing.T) {
	r, baseDir, calls := witnessRunner(t, "repaired", "repaired", true, false)
	w, ok, why := r.RunPreRepairWitness(context.Background(), witnessRequest(witnessTestID))
	if len(calls.permits) != 1 || calls.baseline != 1 || !ranIn(r.Workspace) || !ranIn(baseDir) {
		t.Fatalf("both legs were not reached and executed: %+v", calls)
	}
	if ok || w.Purpose != "" || !strings.Contains(why, "did not fail on the recorded base") {
		t.Fatalf("a test green on both sides produced a witness: ok=%v %+v (%s)", ok, w, why)
	}
}

// W5 AUTHORITY CONTROL. Nothing but a canonical named-test identity or an
// admitted Test check can be executed. A command-shaped string, a malformed
// id, a non-test check, an unknown subject kind, a non-evidence finding and an
// unbound candidate are refused WITHOUT reaching the capability check or a
// checkout (the doubles fail the test if reached) and without executing. A
// valid test the envelope does not permit reaches the permit, which denies,
// and never reaches the base.
//
// Fails if worker text can become an executable check, if a refusal is
// actually some later guard's, or if a denied capability still executes.
func TestW5OnlyABrokerOwnedTestIdentityIsExecuted(t *testing.T) {
	for name, req := range map[string]PreRepairRequest{
		"command string":      witnessRequest("go test ./... ; touch pwned"),
		"no test name":        witnessRequest("w/w_test.go"),
		"escapes the repo":    witnessRequest("../w/w_test.go:TestWitness"),
		"absolute path":       witnessRequest("/w/w_test.go:TestWitness"),
		"not a test function": witnessRequest("w/w_test.go:Witness"),
		"shell in the name":   witnessRequest("w/w_test.go:TestWitness;touch pwned"),
		"not a test file":     witnessRequest("w/w.go:TestWitness"),
		"non-test check": func() PreRepairRequest {
			q := witnessRequest("")
			q.Subject = PreRepairSubject{Kind: AdmittedCheck, Check: Check{Kind: Build, Command: "sh", Args: []string{"-c", "touch pwned"}}}
			return q
		}(),
		"unknown subject kind": func() PreRepairRequest {
			q := witnessRequest(witnessTestID)
			q.Subject.Kind = "prose"
			return q
		}(),
		"code finding": func() PreRepairRequest {
			q := witnessRequest(witnessTestID)
			q.FindingClass = "code"
			return q
		}(),
		"no base": func() PreRepairRequest {
			q := witnessRequest(witnessTestID)
			q.BaseSHA = ""
			return q
		}(),
	} {
		r, baseDir, calls := witnessRunner(t, "repaired", "broken", true, true)
		w, ok, why := r.RunPreRepairWitness(context.Background(), req)
		if ok || w.Purpose != "" || why == "" {
			t.Fatalf("%s: produced a witness: %+v", name, w)
		}
		if len(calls.permits) != 0 || calls.baseline != 0 || ranIn(r.Workspace) || ranIn(baseDir) {
			t.Fatalf("%s: refusal came after production was reached: %+v", name, calls)
		}
		for _, dir := range []string{r.Workspace, baseDir} {
			if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
				t.Fatalf("%s: worker text was interpreted by a shell", name)
			}
		}
	}

	// Metacharacters inside an otherwise canonical id stay one argv element:
	// the directory does not exist, go says so, and no shell ever runs.
	r, baseDir, _ := witnessRunner(t, "repaired", "broken", true, false)
	if _, ok, _ := r.RunPreRepairWitness(context.Background(), witnessRequest("w$(touch pwned)/w_test.go:TestWitness")); ok {
		t.Fatal("a path that names no package produced a witness")
	}
	for _, dir := range []string{r.Workspace, baseDir} {
		if _, err := os.Stat(filepath.Join(dir, "pwned")); err == nil {
			t.Fatal("metacharacters in a test identity became shell syntax")
		}
	}

	// A valid typed test the envelope does not permit.
	denied, deniedBase, calls := witnessRunner(t, "repaired", "broken", false, false)
	w, ok, why := denied.RunPreRepairWitness(context.Background(), witnessRequest(witnessTestID))
	if len(calls.permits) != 1 || calls.permits[0] != Test {
		t.Fatalf("the Test capability was not the check that decided: %+v", calls)
	}
	if calls.baseline != 0 || ranIn(denied.Workspace) || ranIn(deniedBase) {
		t.Fatalf("a denied capability still executed or checked out the base: %+v", calls)
	}
	if ok || w.Purpose != "" || !strings.Contains(why, "not granted") {
		t.Fatalf("a denied capability produced a witness: ok=%v (%s)", ok, why)
	}
}

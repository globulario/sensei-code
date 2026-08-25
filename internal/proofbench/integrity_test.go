package proofbench

// The integrity attacks.
//
// A benchmark's failure mode is not producing a wrong number. It is producing a
// number that looks right, from evidence that has quietly been shaped to
// produce it. Each test below is one of the ways this campaign could lie to
// itself, named in the brief and pinned here.
//
// They are attacks on the HARNESS, not on Sensei-code. If any of them fails,
// nothing the campaign reports about the product can be believed.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func f64(v float64) *float64 { return &v }

func soundManifest() Manifest {
	m := Manifest{
		Version:       "proof-v1",
		SelectionRule: "merged PRs touching a _test.go and a non-test .go, chronological",
		Calibration: []Task{{
			ID: "cal-positive", Statement: "known win", BaseSHA: strings.Repeat("a", 40),
			Expected: "positive", Oracle: Oracle{Kind: "behavioral_probe", Command: []string{"true"}},
		}},
	}
	for i := 0; i < MinPrimaryTasks; i++ {
		t := Task{
			ID: string(rune('a'+i)) + "-task", Statement: "do the thing",
			BaseSHA: strings.Repeat("b", 40), Origin: "abc123",
			Oracle: Oracle{Kind: "withheld_tests", Paths: []string{"x_test.go"}, Command: []string{"go", "test", "./..."}},
		}
		if i < MinLinkedTasks {
			t.DependsOn = []string{"a-task"}
		}
		m.Tasks = append(m.Tasks, t)
	}
	m.Tasks[0].DependsOn = nil // the root of the chain depends on nothing
	m.Tasks = append(m.Tasks, Task{
		ID: "k-task", Statement: "extra", BaseSHA: strings.Repeat("c", 40), Origin: "abc123",
		DependsOn: []string{"b-task"},
		Oracle:    Oracle{Kind: "withheld_tests", Paths: []string{"y_test.go"}, Command: []string{"go", "test", "./..."}},
	})
	return m
}

func writeManifest(t *testing.T, m Manifest) (string, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "manifest.json")
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	return path, HashBytes(b)
}

// 1. Changing a manifest after a run versions the result rather than
// reinterpreting it.
func TestEditingAManifestOrphansItsRuns(t *testing.T) {
	m := soundManifest()
	path, hash := writeManifest(t, m)

	l, err := OpenLedger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Append(Attempt{Task: "a-task", Arm: ArmCold, Number: 1, ManifestHash: hash,
		Verdict: Correct, Terminal: "workflow.completed", GovernedCheckoutClean: true}); err != nil {
		t.Fatal(err)
	}

	// The manifest changes: one task statement is reworded.
	m.Tasks[1].Statement = "do the thing, but differently"
	b, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	_, newHash, err := LoadManifest(path)
	if err != nil {
		t.Fatal(err)
	}
	if newHash == hash {
		t.Fatal("a changed manifest kept its hash; the freeze is not a freeze")
	}
	current, orphaned := l.ForManifest(newHash)
	if len(current) != 0 {
		t.Error("a run from the previous manifest was counted under the new one")
	}
	if len(orphaned) != 1 {
		t.Errorf("the earlier run was dropped instead of reported as orphaned: %d", len(orphaned))
	}
	// And the report says so out loud rather than shrinking silently.
	rep := Build(m, newHash, l, nil)
	if rep.Orphaned != 1 || !strings.Contains(rep.Markdown(), "different manifest hash") {
		t.Error("the report does not disclose that committed evidence belongs to another experiment")
	}
}

// 2. A run from the wrong base SHA is refused.
func TestARunFromTheWrongBaseIsRefused(t *testing.T) {
	repo := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "t@example.invalid")
	git("config", "user.name", "T")
	if err := os.WriteFile(filepath.Join(repo, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git("add", "main.go")
	git("commit", "-qm", "one")
	head, err := exec.Command("git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	real := strings.TrimSpace(string(head))

	r := Runner{RepoRoot: repo, WorkDir: t.TempDir()}
	task := Task{ID: "t", BaseSHA: strings.Repeat("0", 40),
		Oracle: Oracle{Kind: "behavioral_probe", Command: []string{"true"}}}
	if _, err := r.Prepare(context.Background(), Plan{Task: task, Arm: ArmCold, Attempt: 1}); err == nil {
		t.Fatal("a worktree was created at a base commit that does not exist")
	}
	// And the real base is accepted, so the refusal above is about the base
	// rather than about the harness being broken.
	task.BaseSHA = real
	dir, err := r.Prepare(context.Background(), Plan{Task: task, Arm: ArmCold, Attempt: 1})
	if err != nil {
		t.Fatalf("the pinned base was refused: %v", err)
	}
	_ = r.Cleanup(context.Background(), dir)
}

// 3. A provider/model mismatch excludes the paired comparison rather than
// being silently combined.
func TestAProviderChangeInvalidatesAPair(t *testing.T) {
	cold := Attempt{Providers: map[string]string{"author": "codex@1", "reviewer": "claude@1"}}
	warm := Attempt{Providers: map[string]string{"author": "codex@2", "reviewer": "claude@1"}}
	same, why := SameProviders(cold, warm)
	if same {
		t.Fatal("a model change inside a paired comparison was treated as comparable")
	}
	c := CompareLinked("x", cold, warm, same, why)
	if c.Comparable || c.Improved {
		t.Error("an incomparable pair produced a compounding claim")
	}
	if !strings.Contains(c.Why, "author") {
		t.Errorf("the report does not say which role changed: %q", c.Why)
	}
}

// 4 and 6. An attempt id identifies exactly one attempt, and a failed semantic
// run cannot be replaced by a successful retry.
func TestAFailedRunCannotBeOverwrittenByASuccess(t *testing.T) {
	dir := t.TempDir()
	l, err := OpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	failed := Attempt{Task: "a-task", Arm: ArmCold, Number: 1, ManifestHash: "h",
		Verdict: Incorrect, Terminal: "workflow.failed"}
	if err := l.Append(failed); err != nil {
		t.Fatal(err)
	}
	success := failed
	success.Verdict = Correct
	success.Terminal = "workflow.completed"
	if err := l.Append(success); err == nil {
		t.Fatal("a failed attempt was overwritten by a successful one under the same id")
	}
	// Even a fresh ledger over the same directory refuses, because the refusal
	// is the filesystem's and not an in-memory index's.
	l2, err := OpenLedger(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := l2.Append(success); err == nil {
		t.Fatal("a new ledger process overwrote committed evidence")
	}

	// A retry is attempt 2, and the SEMANTIC failure still scores.
	retry := failed
	retry.Number = 2
	retry.Verdict = Correct
	if err := l2.Append(retry); err != nil {
		t.Fatal(err)
	}
	scored, _ := Scored([]Attempt{failed, retry})
	if scored.Verdict != Incorrect {
		t.Fatal("retry-until-green: a semantic failure was superseded by a later success")
	}

	// An INFRASTRUCTURE failure is the one thing that licenses a retry.
	infra := Attempt{Task: "b-task", Arm: ArmCold, Number: 1, ManifestHash: "h",
		Verdict: NoResult, Infrastructure: "provider quota"}
	good := infra
	good.Number = 2
	good.Verdict = Correct
	good.Infrastructure = ""
	if s, _ := Scored([]Attempt{infra, good}); s.Verdict != Correct {
		t.Fatal("a legitimate infrastructure retry did not score")
	}
}

// 5. Missing cost data stays unknown and never becomes zero.
func TestMissingCostIsUnknownNotZero(t *testing.T) {
	known := Attempt{Task: "a", Arm: ArmRaw, Number: 1, ManifestHash: "h", Verdict: Correct,
		CostUSD: f64(2.5), GovernedCheckoutClean: true}
	unknown := Attempt{Task: "b", Arm: ArmRaw, Number: 1, ManifestHash: "h", Verdict: Correct,
		GovernedCheckoutClean: true}
	m := Compute(ArmRaw, []Attempt{known, unknown})
	if m.CostUSD.N != 1 || m.CostUSD.Missing != 1 {
		t.Fatalf("cost distribution folded a missing value in: %+v", m.CostUSD)
	}
	if m.CostUSD.Median != 2.5 {
		t.Errorf("median %v; a missing cost was averaged as zero", m.CostUSD.Median)
	}
	if !strings.Contains(dist(m.CostUSD, " USD"), "unknown") {
		t.Error("the rendered cost does not disclose the missing observation")
	}
	// And with no data at all the gate does not pass by default.
	grade, gates := Evaluate(GateInput{PrimaryTasks: 10, GovernedCorrect: 10, GovernedAutonomous: 10,
		RawCorrect: 10, CalibrationPositiveOK: true, CalibrationNegativeOK: true, CostKnown: false})
	for _, g := range gates {
		if g.ID == "G8" && g.Passed {
			t.Error("the cost gate passed with no cost data; absent evidence is not evidence of " +
				"being within budget")
		}
	}
	if grade == Green {
		t.Error("GREEN was reached without cost evidence")
	}
}

// 7. Hidden-oracle content is never placed in the worker's prompt.
func TestTheOracleNeverReachesTheWorker(t *testing.T) {
	task := Task{ID: "t", Oracle: Oracle{Kind: "withheld_tests",
		Paths:   []string{"internal/router/authority_regression_test.go"},
		Command: []string{"go", "test", "./internal/router/ -run TestTheExactFix"}}}

	clean := "Fix the router so unknown provenance does not grant authority."
	if err := CheckPromptIsolation(clean, task); err != nil {
		t.Fatalf("an ordinary task statement was rejected: %v", err)
	}
	for _, leak := range []string{
		"Fix it. See internal/router/authority_regression_test.go for what is expected.",
		"Make `go test ./internal/router/ -run TestTheExactFix` pass.",
	} {
		if err := CheckPromptIsolation(leak, task); err == nil {
			t.Errorf("a prompt carrying the oracle was accepted: %q", leak)
		}
	}
}

// 8 and 9. RAW cannot receive Sensei project knowledge; COLD cannot receive
// WARM state.
func TestArmsCannotSeeWhatTheyMustNot(t *testing.T) {
	task := Task{ID: "t", Oracle: Oracle{Kind: "withheld_tests",
		Paths: []string{"pkg/thing_test.go"}, Command: []string{"go", "test"}}}

	dir := t.TempDir()
	mk := func(rel string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// A clean checkout passes every arm.
	if err := CheckWorktreeIsolation(dir, ArmRaw, task); err != nil {
		t.Fatalf("a clean RAW worktree was rejected: %v", err)
	}

	mk("docs/awareness/invariants.yaml")
	if err := CheckWorktreeIsolation(dir, ArmRaw, task); err == nil {
		t.Error("RAW ran with Sensei project knowledge in its checkout; the baseline would be " +
			"measuring the control plane it exists to exclude")
	}
	if err := CheckWorktreeIsolation(dir, ArmCold, task); err != nil {
		t.Errorf("COLD was denied knowledge it is allowed: %v", err)
	}

	mk(warmStateMarker)
	if err := CheckWorktreeIsolation(dir, ArmCold, task); err == nil {
		t.Error("COLD ran carrying WARM state; the compounding comparison would compare an arm " +
			"with itself")
	}

	// And the withheld oracle must be absent everywhere.
	mk("pkg/thing_test.go")
	for _, arm := range Arms {
		if err := CheckWorktreeIsolation(dir, arm, task); err == nil {
			t.Errorf("%s ran with the withheld oracle file present", arm)
		}
	}
}

// 10. Candidate diff hash and git-cleanliness evidence are recorded, and an
// unclean governed checkout disqualifies autonomy.
func TestBoundaryEvidenceIsRecordedAndCounts(t *testing.T) {
	dirty := Attempt{Verdict: Correct, GovernedCheckoutClean: false}
	if dirty.AutonomousCorrect() {
		t.Fatal("a run that mutated the governed checkout was scored autonomous-correct")
	}
	clean := Attempt{Verdict: Correct, GovernedCheckoutClean: true}
	if !clean.AutonomousCorrect() {
		t.Fatal("a clean correct run was not scored autonomous-correct")
	}
	told := Attempt{Verdict: Correct, GovernedCheckoutClean: true,
		Interventions: []Intervention{{Kind: "technical_answer", Detail: "told it the fix"}}}
	if told.AutonomousCorrect() {
		t.Fatal("a run where a human supplied the answer was scored autonomous")
	}
	// A landing decision is not a technical answer.
	landed := Attempt{Verdict: Correct, GovernedCheckoutClean: true,
		Interventions: []Intervention{{Kind: "landing_decision", Detail: "approved the merge"}}}
	if !landed.AutonomousCorrect() {
		t.Fatal("choosing whether to land a retained candidate was counted as supplying the answer")
	}
}

// 11. Report aggregation includes failed and NO_RESULT attempts.
func TestTheReportCannotShrinkToItsSuccesses(t *testing.T) {
	m := soundManifest()
	_, hash := writeManifest(t, m)
	l, err := OpenLedger(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// One success, one failure, one that never reached the oracle.
	add := func(task string, v Verdict, terminal, infra string) {
		t.Helper()
		if err := l.Append(Attempt{Task: task, Arm: ArmCold, Number: 1, ManifestHash: hash,
			Verdict: v, Terminal: terminal, Infrastructure: infra, GovernedCheckoutClean: true}); err != nil {
			t.Fatal(err)
		}
	}
	add("a-task", Correct, "workflow.completed", "")
	add("b-task", Incorrect, "workflow.completed", "")
	add("c-task", NoResult, "", "provider outage")

	rep := Build(m, hash, l, nil)
	cold := rep.Arms[ArmCold]
	if cold.Runs != 3 {
		t.Fatalf("the report kept %d of 3 recorded runs", cold.Runs)
	}
	if cold.NoResults != 1 {
		t.Errorf("the NO_RESULT run vanished: %+v", cold)
	}
	if cold.CorrectClosure.D != 2 {
		t.Errorf("correct closure denominator %d; NO_RESULT must be excluded from correctness "+
			"but visible as operational reliability", cold.CorrectClosure.D)
	}
	if cold.CorrectClosure.N != 1 {
		t.Errorf("correct closure numerator %d", cold.CorrectClosure.N)
	}
	// b-task is a false grant: incorrect, and the workflow completed on it.
	if len(rep.FalseGrants) != 1 {
		t.Errorf("an incorrect candidate that the workflow completed was not counted as a false "+
			"grant: %v", rep.FalseGrants)
	}
	if rep.Verdict != Red {
		t.Errorf("verdict %s; a false grant is a pre-registered RED condition", rep.Verdict)
	}
	md := rep.Markdown()
	for _, want := range []string{"NO_RESULT", "false grant", "Verdict: RED"} {
		if !strings.Contains(md, want) {
			t.Errorf("the report does not surface %q", want)
		}
	}
}

// 12. The verdict is deterministic from the committed records.
func TestTheVerdictIsAFunctionOfTheLedger(t *testing.T) {
	in := GateInput{PrimaryTasks: 10, GovernedCorrect: 9, GovernedAutonomous: 8, RawCorrect: 9,
		CalibrationPositiveOK: true, CalibrationNegativeOK: true,
		CostKnown: true, GovernedCostRatio: 2.0, ColdRediscovery: 10, WarmRediscovery: 4}
	for i := 0; i < MinLinkedTasks; i++ {
		in.Linked = append(in.Linked, LinkedComparison{Comparable: true, Improved: i < 3})
	}
	first, gatesA := Evaluate(in)
	second, gatesB := Evaluate(in)
	if first != second {
		t.Fatalf("the same input produced %s then %s", first, second)
	}
	if len(gatesA) != len(gatesB) {
		t.Fatal("gate list is not stable")
	}
	for i := range gatesA {
		if gatesA[i] != gatesB[i] {
			t.Fatalf("gate %d differs between evaluations", i)
		}
	}
	if first != Green {
		t.Fatalf("a specimen meeting every pre-registered GREEN condition graded %s: %+v", first, gatesA)
	}
}

// An untestable compounding claim is reported as untestable, never as passed.
func TestAnUntestableGateIsNotAPass(t *testing.T) {
	in := GateInput{PrimaryTasks: 10, GovernedCorrect: 10, GovernedAutonomous: 10, RawCorrect: 10,
		CalibrationPositiveOK: true, CalibrationNegativeOK: true,
		CostKnown: true, GovernedCostRatio: 1.0,
		ColdRediscovery: 0, WarmRediscovery: 0}
	for i := 0; i < MinLinkedTasks; i++ {
		in.Linked = append(in.Linked, LinkedComparison{Comparable: true, Improved: true})
	}
	grade, gates := Evaluate(in)
	for _, g := range gates {
		if g.ID == "G7" {
			if !g.Untestable {
				t.Error("a zero COLD rediscovery denominator was not reported as untestable")
			}
			if g.Passed {
				t.Error("an untestable gate was scored as passed")
			}
		}
	}
	if grade != Amber {
		t.Errorf("grade %s; an untestable GREEN condition must hold the verdict at AMBER", grade)
	}
}

// A manifest that cannot support the campaign's claims is refused before it
// costs provider budget.
func TestAnUnsoundManifestIsRefused(t *testing.T) {
	for name, mutate := range map[string]func(*Manifest){
		"no selection rule": func(m *Manifest) { m.SelectionRule = "" },
		"too few tasks":     func(m *Manifest) { m.Tasks = m.Tasks[:3] },
		"too few linked": func(m *Manifest) {
			for i := range m.Tasks {
				m.Tasks[i].DependsOn = nil
			}
		},
		"abbreviated base":         func(m *Manifest) { m.Tasks[0].BaseSHA = "abc1234" },
		"no calibration":           func(m *Manifest) { m.Calibration = nil },
		"oracle withholds nothing": func(m *Manifest) { m.Tasks[0].Oracle.Paths = nil },
		"unknown oracle kind":      func(m *Manifest) { m.Tasks[0].Oracle.Kind = "vibes" },
		"dangling dependency":      func(m *Manifest) { m.Tasks[1].DependsOn = []string{"nope"} },
	} {
		t.Run(name, func(t *testing.T) {
			m := soundManifest()
			mutate(&m)
			if err := m.Validate(); err == nil {
				t.Fatal("an unsound manifest validated")
			}
		})
	}
	if err := soundManifest().Validate(); err != nil {
		t.Fatalf("the sound specimen was refused, so the checks above prove nothing: %v", err)
	}
}

// Arm execution order is counterbalanced and reproducible.
func TestExecutionOrderIsCounterbalancedAndReplayable(t *testing.T) {
	first := map[Arm]int{}
	for _, id := range []string{"a-task", "b-task", "c-task", "d-task", "e-task", "f-task"} {
		order := ExecutionOrder(id)
		if len(order) != len(Arms) {
			t.Fatalf("%s: order has %d arms", id, len(order))
		}
		seen := map[Arm]bool{}
		for _, a := range order {
			if seen[a] {
				t.Fatalf("%s: %s appears twice", id, a)
			}
			seen[a] = true
		}
		first[order[0]]++
		// Reproducible: a run nobody can replay is not evidence.
		again := ExecutionOrder(id)
		for i := range order {
			if order[i] != again[i] {
				t.Fatalf("%s: order is not reproducible", id)
			}
		}
	}
	if len(first) < 2 {
		t.Errorf("every task put the same arm first (%v); provider drift would always favour "+
			"the same condition", first)
	}
}

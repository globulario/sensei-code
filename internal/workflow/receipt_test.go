package workflow

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

// TestEveryTerminalPathEmitsAReceipt is the wiring itself, not a description.
//
// A run that ends without a receipt is a run whose account has to be
// reconstructed afterwards from the event stream -- the architecture that cost
// C5. A new terminal path must not be able to skip it quietly, so the guard is
// structural: any function that emits a run terminal must also emit a receipt.
func TestEveryTerminalPathEmitsAReceipt(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing the package: %v", err)
	}
	fset := token.NewFileSet()
	terminals := []string{"WorkflowCompleted", "WorkflowFailed", "WorkflowStopped"}
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, perr := parser.ParseFile(fset, path, nil, 0)
		if perr != nil {
			t.Fatalf("%s: %v", path, perr)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			var emitsTerminal, emitsReceipt bool
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				for _, k := range terminals {
					if sel.Sel.Name == k {
						emitsTerminal = true
					}
				}
				if sel.Sel.Name == "emitReceipt" {
					emitsReceipt = true
				}
				return true
			})
			if emitsTerminal && !emitsReceipt {
				t.Errorf("%s: %s emits a run terminal without emitting a receipt. "+
					"A run whose account must be reconstructed afterwards is the architecture that cost C5.",
					path, fn.Name.Name)
			}
		}
	}
}

// TestARunThatDiesBeforeTheGateStillSaysWhatItNeverReached: the earliest
// failure is exactly where a silent record would be worst.
func TestARunThatDiesBeforeTheGateStillSaysWhatItNeverReached(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	r := e.emitReceipt("task-1", runreceipt.OutcomeFailed, e.candidateStateFor("task-1"))

	state, missing := r.Completeness()
	if state != runreceipt.Incomplete {
		t.Fatalf("state = %s: a run that never reached the gate cannot have a complete account", state)
	}
	if r.BaseCommit.State != runreceipt.Unknown || !strings.Contains(r.BaseCommit.Detail, "start gate") {
		t.Errorf("base commit = %+v, want an authored reason naming the gate", r.BaseCommit)
	}
	if r.CandidateState != runreceipt.CandidateNone {
		t.Errorf("candidate state = %s: no worktree was created, and that is a positive fact", r.CandidateState)
	}
	joined := strings.Join(missing, " ")
	for _, want := range []string{"base_commit", "graph_digest", "plan_digest"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing should name %s: %v", want, missing)
		}
	}
}

// TestTheEngineNeverInfersAReviewItDidNotMeasure.
func TestTheEngineNeverInfersAReviewItDidNotMeasure(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	if got := e.reviewedOutcome("task-1"); got != runreceipt.OutcomeUnreviewed {
		t.Fatalf("outcome = %s, want UNREVIEWED when nothing was measured", got)
	}
	e.noteReviewerAssigned("task-1", "codex")
	if got := e.reviewedOutcome("task-1"); got != runreceipt.OutcomeUnreviewed {
		t.Fatalf("outcome = %s: an assignment is not a verdict", got)
	}
	r := e.emitReceipt("task-1", runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if len(r.Attempts) != 1 || r.Attempts[0].DeliveredVerdict() {
		t.Fatalf("attempts = %+v: an assigned reviewer that never returned has not delivered", r.Attempts)
	}
	e.noteReviewDelivered("task-1", "codex", "accept", "b4f471f0")
	if got := e.reviewedOutcome("task-1"); got != runreceipt.OutcomeAccepted {
		t.Fatalf("outcome = %s after a measured acceptance", got)
	}
	r = e.emitReceipt("task-1", runreceipt.OutcomeAccepted, runreceipt.CandidateNone)
	if len(r.Attempts) != 1 || !r.Attempts[0].DeliveredVerdict() {
		t.Fatalf("the verdict must bind to the attempt that gave it: %+v", r.Attempts)
	}
}

// --- the three findings this slice surfaced, recorded as tests -------------

// FINDING 1. The main success path cannot produce a COMPLETE receipt, because
// the loop never commits its candidate. This is C5's obligation 2, visible at
// the production boundary without an experiment. The schema is NOT relaxed.
func TestAnAcceptedRunIsIncompleteWhileItsCandidateIsNeverCommitted(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteWorld("task-1", "f01592b0f0828605ed254047fc064f41dacc78f2", "fac399f8225f")
	e.notePlan("task-1", "", "a plan the architect wrote")
	e.noteCandidateCreated("task-1")
	e.noteCandidateDigest("task-1", "b4f471f096d13f2b")
	e.noteReviewerAssigned("task-1", "codex")
	e.noteReviewDelivered("task-1", "codex", "accept", "b4f471f096d13f2b")

	r := e.emitReceipt("task-1", e.reviewedOutcome("task-1"), e.candidateStateFor("task-1"))
	if r.CandidateState != runreceipt.CandidatePresent || r.Outcome != runreceipt.OutcomeAccepted {
		t.Fatalf("state=%s outcome=%s", r.CandidateState, r.Outcome)
	}
	state, missing := r.Completeness()
	if state != runreceipt.Incomplete {
		t.Fatal("a PRESENT candidate with no commit, tree or first parent must not read as complete")
	}
	joined := strings.Join(missing, " ")
	for _, want := range []string{"candidate_commit", "candidate_tree", "candidate_first_parent"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing should name %s: %v", want, missing)
		}
	}
	// And once the loop commits its candidate, the same run completes.
	//
	// The governor's own identity is stubbed here to isolate the candidate
	// axis. It is not a gap: `go version -m` on a built sensei-code shows
	// vcs.revision embedded by the toolchain, so a binary built from a clean
	// checkout states its commit. A `go test` binary carries no stamp, which is
	// a fact about test binaries rather than about the engine.
	restore := governorIdentityFn
	governorIdentityFn = func() (runreceipt.Value, runreceipt.Value) {
		return runreceipt.MeasuredValue("3e5ade1391b4b754f0472bdcde6877e6db699d19", "runtime/debug build info vcs.revision"),
			runreceipt.MeasuredValue(strings.Repeat("a", 64), "sha256 of the executable this process is running")
	}
	defer func() { governorIdentityFn = restore }()
	e.noteAwarenessProducer("task-1", "go")
	e.noteCandidateCommit("task-1", "cccc", "tttt", "f01592b0f0828605ed254047fc064f41dacc78f2")
	r = e.emitReceipt("task-1", e.reviewedOutcome("task-1"), e.candidateStateFor("task-1"))
	if state, missing := r.Completeness(); state != runreceipt.Complete {
		t.Fatalf("with a committed candidate the record should be complete: %v", missing)
	}
}

// FINDING 2. A human-stopped run has no outcome in this vocabulary. Recording
// it as FAILED would teach the record that the task shape breaks, which the
// engine explicitly refuses to do. So the receipt says UNKNOWN and is
// INCOMPLETE, and the gap is visible rather than papered over.
func TestAStoppedRunHasNoOutcomeInThisVocabularyYet(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteWorld("task-1", "f01592b0", "fac399f8225f")
	e.notePlan("task-1", "990090fd", "")
	r := e.emitReceipt("task-1", runreceipt.OutcomeUnknown, runreceipt.CandidateNone)
	if r.Outcome != runreceipt.OutcomeUnknown {
		t.Fatalf("outcome = %s", r.Outcome)
	}
	if state, missing := r.Completeness(); state != runreceipt.Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "outcome UNKNOWN") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
	for _, o := range []runreceipt.Outcome{runreceipt.OutcomeAccepted, runreceipt.OutcomeRefused,
		runreceipt.OutcomeFailed, runreceipt.OutcomeUnreviewed, runreceipt.OutcomeUnknown} {
		if string(o) == "STOPPED" {
			t.Fatal("if STOPPED is added, this test records the decision rather than the gap")
		}
	}
}

// FINDING 3. A conversational answer carries no plan, and plan_digest is
// unconditionally required, so that terminal cannot be complete. Unlike
// finding 1 this is not a missing measurement: the artifact does not exist.
func TestAConversationalAnswerCarriesNoPlanAndSaysSo(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteWorld("task-1", "f01592b0", "fac399f8225f")
	e.notePlan("task-1", "", "") // the architect replied instead of planning
	r := e.emitReceipt("task-1", runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if r.PlanDigest.State != runreceipt.Unknown {
		t.Fatalf("plan digest = %+v, want an explicit absence", r.PlanDigest)
	}
	if !strings.Contains(r.PlanDigest.Detail, "no plan text") {
		t.Errorf("the reason must say the artifact does not exist, got %q", r.PlanDigest.Detail)
	}
	if state, _ := r.Completeness(); state != runreceipt.Incomplete {
		t.Fatal("plan_digest is unconditionally required, so this terminal is incomplete")
	}
}

// An architect's plan had no identity at all before this slice asked for one.
func TestAnArchitectsPlanNowHasAnIdentityToo(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.notePlan("task-1", "", "two repairs, both in the governed loop")
	r := e.emitReceipt("task-1", runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if r.PlanDigest.State != runreceipt.Known || len(r.PlanDigest.Text) != 64 {
		t.Fatalf("plan digest = %+v, want a sha256 of the architect's plan text", r.PlanDigest)
	}
	if !strings.Contains(r.PlanDigest.Source, "architect") {
		t.Errorf("the source must distinguish it from a supplied plan, got %q", r.PlanDigest.Source)
	}
	// A supplied plan keeps its own identity, and the source says which it is.
	e.beginReceipt("task-2")
	e.notePlan("task-2", "990090fd", "ignored when a supplied digest exists")
	r2 := e.emitReceipt("task-2", runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if r2.PlanDigest.Text != "990090fd" || !strings.Contains(r2.PlanDigest.Source, "supplied") {
		t.Fatalf("supplied plan digest = %+v", r2.PlanDigest)
	}
}

// TestTheGovernorCanStateItsOwnIdentity: the schema required three facts about
// the running process that the engine could not state. Two were measurable from
// inside it, and the third is embedded by the toolchain.
func TestTheGovernorCanStateItsOwnIdentity(t *testing.T) {
	commit, binary := governorIdentity()
	if binary.State != runreceipt.Known || len(binary.Text) != 64 {
		t.Fatalf("binary digest = %+v, want a sha256 of the running executable", binary)
	}
	if !strings.Contains(binary.Source, "executable this process is running") {
		t.Errorf("source = %q", binary.Source)
	}
	// A test binary may or may not carry a VCS stamp, and either answer must be
	// stated rather than guessed.
	switch commit.State {
	case runreceipt.Known:
		if strings.TrimSpace(commit.Source) == "" {
			t.Error("a measured commit must say how it was measured")
		}
	case runreceipt.Unknown:
		if strings.TrimSpace(commit.Detail) == "" {
			t.Error("an absent commit must say why")
		}
	default:
		t.Fatalf("commit = %+v", commit)
	}
}

// A binary built from a modified tree must NOT name the commit it was built
// from: that is a governor claiming to be a revision it is not.
func TestAModifiedBuildRefusesToNameItsCommit(t *testing.T) {
	commit, _ := governorIdentity()
	if commit.State == runreceipt.Unknown && strings.Contains(commit.Detail, "MODIFIED") {
		if !strings.Contains(commit.Detail, "does not identify this binary") {
			t.Errorf("the reason must say why the commit is not the identity: %q", commit.Detail)
		}
	}
}

// The awareness executable is measured as an IMAGE, and says so. C5 found a
// frozen "producer" naming a file nobody had shown to be executing.
func TestTheAwarenessExecutableIsMeasuredAsAnImageNotAProcess(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteAwarenessProducer("task-1", "go")
	r := e.emitReceipt("task-1", runreceipt.OutcomeFailed, runreceipt.CandidateNone)
	if r.ServingProducer.State != runreceipt.Known {
		t.Fatalf("serving producer = %+v", r.ServingProducer)
	}
	if !strings.Contains(r.ServingProducer.Source, "launched for awareness") {
		t.Errorf("the source must say what was measured, got %q", r.ServingProducer.Source)
	}
	e.beginReceipt("task-2")
	e.noteAwarenessProducer("task-2", "definitely-not-on-path-xyz")
	r2 := e.emitReceipt("task-2", runreceipt.OutcomeFailed, runreceipt.CandidateNone)
	if r2.ServingProducer.State != runreceipt.Unknown || r2.ServingProducer.Detail == "" {
		t.Fatalf("an unresolvable command must be an explained absence: %+v", r2.ServingProducer)
	}
}

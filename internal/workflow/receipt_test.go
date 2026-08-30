package workflow

import (
	"go/ast"

	"github.com/globulario/sensei-code/internal/event"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

// TestOnlyEmitRunTerminalEndsARun makes the pairing an invariant instead of a
// convention.
//
// The previous version asked whether a function contained BOTH a terminal kind
// and some emitReceipt call, which a function with three terminal exits and one
// receipt would have passed. This one requires that a run-terminal event is
// only ever CONSTRUCTED inside emitRunTerminal, where the receipt is emitted
// first and cannot be omitted.
//
// Its limit, stated rather than hidden: it inspects direct arguments, so a
// terminal kind stashed in a variable and passed to e.emit elsewhere would
// evade it. That is a deliberate evasion, not the accidental drift this guards.
func TestOnlyEmitRunTerminalEndsARun(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("globbing the package: %v", err)
	}
	terminal := map[string]bool{"WorkflowCompleted": true, "WorkflowFailed": true, "WorkflowStopped": true}
	fset := token.NewFileSet()
	var funnel int
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
			inFunnel := fn.Name.Name == "emitRunTerminal"
			if inFunnel {
				funnel++
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "New" {
					return true
				}
				if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "event" {
					return true
				}
				for _, arg := range call.Args {
					kind, ok := arg.(*ast.SelectorExpr)
					if !ok || !terminal[kind.Sel.Name] {
						continue
					}
					if !inFunnel {
						t.Errorf("%s: %s constructs a run-terminal event directly. "+
							"Every run ends through emitRunTerminal, which emits the receipt first; "+
							"a run whose account must be reconstructed afterwards is the architecture that cost C5.",
							path, fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	if funnel != 1 {
		t.Fatalf("expected exactly one emitRunTerminal, found %d", funnel)
	}
}

// TestARunThatDiesBeforeTheGateStillSaysWhatItNeverReached: the earliest
// failure is exactly where a silent record would be worst.
func TestARunThatDiesBeforeTheGateStillSaysWhatItNeverReached(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, e.candidateStateFor("task-1"))

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
	for _, want := range []string{"base_commit", "graph_digest"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing should name %s: %v", want, missing)
		}
	}
	// The plan axis must NOT claim NONE here. A run that dies at step one does
	// not know whether a plan governs it -- a supplied plan may already exist --
	// so it says UNKNOWN and is incomplete for saying so.
	if r.PlanState != runreceipt.PlanUnknown {
		t.Errorf("plan state = %s, want UNKNOWN: an early death cannot deny a plan it may have been handed", r.PlanState)
	}
	if !strings.Contains(joined, "plan_state") {
		t.Errorf("missing should name plan_state: %v", missing)
	}
	if strings.Contains(joined, "plan_digest") {
		t.Errorf("a run that cannot say whether it has a plan does not owe a digest: %v", missing)
	}
}

// A supplied plan governs from the first instruction, so an early failure must
// report it rather than reporting no plan.
func TestAnEarlyFailureStillNamesASuppliedPlan(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.notePlan("task-1", "990090fd50446fedcdf60f11e3256ede", "")
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, e.candidateStateFor("task-1"))
	if r.PlanState != runreceipt.PlanPresent {
		t.Fatalf("plan state = %s, want PRESENT", r.PlanState)
	}
	if r.PlanDigest.State != runreceipt.Known || !strings.Contains(r.PlanDigest.Source, "supplied") {
		t.Fatalf("plan digest = %+v", r.PlanDigest)
	}
}

// The terminal field records the terminal EVENT. Recording the outcome there
// made Outcome quietly into two fields.
func TestTheTerminalFieldRecordsTheEventNotTheOutcome(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	r := e.emitReceipt("task-1", event.WorkflowCompleted, runreceipt.OutcomeAccepted, runreceipt.CandidateNone)
	if r.Terminal.Text != string(event.WorkflowCompleted) {
		t.Fatalf("terminal = %q, want %q", r.Terminal.Text, event.WorkflowCompleted)
	}
	if r.Terminal.Text == string(r.Outcome) {
		t.Fatal("the terminal event and the outcome are different facts and must not be the same field twice")
	}
}

// A lane that never plans CLAIMS so; it does not inherit the claim.
func TestALaneWithNoPlanClaimsItRatherThanInheritingIt(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	if got := func() runreceipt.PlanState {
		r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, runreceipt.CandidateNone)
		return r.PlanState
	}(); got != runreceipt.PlanUnknown {
		t.Fatalf("an unasserted plan axis = %s, want UNKNOWN", got)
	}
	e.notePlanAbsent("task-1")
	r := e.emitReceipt("task-1", event.WorkflowCompleted, runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if r.PlanState != runreceipt.PlanNone || r.PlanDigest.State != runreceipt.Unknown {
		t.Fatalf("after claiming no plan: state=%s digest=%+v", r.PlanState, r.PlanDigest)
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
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if len(r.Attempts) != 1 || r.Attempts[0].DeliveredVerdict() {
		t.Fatalf("attempts = %+v: an assigned reviewer that never returned has not delivered", r.Attempts)
	}
	e.noteReviewDelivered("task-1", "codex", "accept", "b4f471f0", "")
	if got := e.reviewedOutcome("task-1"); got != runreceipt.OutcomeAccepted {
		t.Fatalf("outcome = %s after a measured acceptance", got)
	}
	r = e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeAccepted, runreceipt.CandidateNone)
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
	e.noteCandidateWork("task-1", "some-tree", "a-different-base-tree")
	e.noteCandidateDigest("task-1", "b4f471f096d13f2b")
	e.noteReviewerAssigned("task-1", "codex")
	e.noteReviewDelivered("task-1", "codex", "accept", "b4f471f096d13f2b", "")

	r := e.emitReceipt("task-1", event.WorkflowFailed, e.reviewedOutcome("task-1"), e.candidateStateFor("task-1"))
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
	// The completion half of this claim is no longer staged here: a complete
	// accepted record now also needs the reviewed tree and the canonical
	// rendering relation, and faking those would prove nothing.
	// TestAnAcceptedRunProducesACompleteReceipt exercises the real sequence
	// against a real repository, including the mint.

}

// An architect's plan had no identity at all before this slice asked for one.
func TestAnArchitectsPlanNowHasAnIdentityToo(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.notePlan("task-1", "", "two repairs, both in the governed loop")
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if r.PlanDigest.State != runreceipt.Known || len(r.PlanDigest.Text) != 64 {
		t.Fatalf("plan digest = %+v, want a sha256 of the architect's plan text", r.PlanDigest)
	}
	if !strings.Contains(r.PlanDigest.Source, "architect") {
		t.Errorf("the source must distinguish it from a supplied plan, got %q", r.PlanDigest.Source)
	}
	// A supplied plan keeps its own identity, and the source says which it is.
	e.beginReceipt("task-2")
	e.notePlan("task-2", "990090fd", "ignored when a supplied digest exists")
	r2 := e.emitReceipt("task-2", event.WorkflowFailed, runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
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

// The serving producer is the PROCESS that answered, not the image that was
// intended to. C5 found a frozen "producer" field naming a file nobody had
// shown to be executing; measuring the resolvable image before launch would
// have been the same mistake one level down -- it would read KNOWN even when
// the process failed to start.
func TestTheServingProducerIsTheProcessThatAnswered(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteServingProducer("task-1", os.Getpid(), true)
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, runreceipt.CandidateNone)
	if r.ServingProducer.State != runreceipt.Known {
		t.Fatalf("serving producer = %+v", r.ServingProducer)
	}
	if !strings.Contains(r.ServingProducer.Source, "answered this run") {
		t.Errorf("the source must say the process answered, got %q", r.ServingProducer.Source)
	}
	// A process that never started served nothing, whatever image was resolvable.
	e.beginReceipt("task-2")
	e.noteServingProducer("task-2", 0, false)
	r2 := e.emitReceipt("task-2", event.WorkflowFailed, runreceipt.OutcomeFailed, runreceipt.CandidateNone)
	if r2.ServingProducer.State != runreceipt.Unknown ||
		!strings.Contains(r2.ServingProducer.Detail, "nothing served this run") {
		t.Fatalf("a failed launch must serve nothing: %+v", r2.ServingProducer)
	}
}

// The graph DIGEST and the graph BUILD COMMIT are different facts.
func TestTheGraphDigestIsNotTheBuildCommit(t *testing.T) {
	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteWorld("task-1", "f01592b0", "") // a start with no live digest
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, runreceipt.CandidateNone)
	if r.GraphDigest.State != runreceipt.Unknown {
		t.Fatalf("graph digest = %+v, want an explicit absence", r.GraphDigest)
	}
	if !strings.Contains(r.GraphDigest.Detail, "build commit is a different fact") {
		t.Errorf("the reason must name the distinction, got %q", r.GraphDigest.Detail)
	}
	e.noteWorld("task-1", "f01592b0", "42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64")
	r = e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeFailed, runreceipt.CandidateNone)
	if r.GraphDigest.State != runreceipt.Known || !strings.Contains(r.GraphDigest.Source, "live_store_graph_digest") {
		t.Fatalf("graph digest = %+v", r.GraphDigest)
	}
}

// Candidate and plan states are STATES, not Go zero values.
func TestCandidateAndPlanStatesAreRecordedNotInferred(t *testing.T) {
	e := &Engine{}
	if got := e.candidateStateFor("no-such-task"); got != runreceipt.CandidateUnknown {
		t.Fatalf("a task with no record = %s, want UNKNOWN rather than a convenient NONE", got)
	}
	e.beginReceipt("task-1")
	if got := e.candidateStateFor("task-1"); got != runreceipt.CandidateNone {
		t.Fatalf("a fresh run = %s, want NONE as a positive claim", got)
	}
	e.noteCandidateWork("task-1", "some-tree", "a-different-base-tree")
	if got := e.candidateStateFor("task-1"); got != runreceipt.CandidatePresent {
		t.Fatalf("after a worktree is created = %s", got)
	}
}

// A stopped run is a complete record of a real outcome.
func TestAStoppedRunIsCompleteAndSaysStopped(t *testing.T) {
	restore := governorIdentityFn
	governorIdentityFn = func() (runreceipt.Value, runreceipt.Value) {
		return runreceipt.MeasuredValue("3e5ade13", "runtime/debug build info vcs.revision"),
			runreceipt.MeasuredValue(strings.Repeat("a", 64), "sha256 of the executable this process is running")
	}
	defer func() { governorIdentityFn = restore }()

	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteServingProducer("task-1", os.Getpid(), true)
	e.noteWorld("task-1", "f01592b0", "42e6e12c")
	e.notePlan("task-1", "990090fd", "")
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeStopped, e.candidateStateFor("task-1"))
	state, missing := r.Completeness()
	if state != runreceipt.Complete {
		t.Fatalf("COMPLETE / STOPPED must be representable: %v", missing)
	}
	if r.Outcome != runreceipt.OutcomeStopped {
		t.Fatalf("outcome = %s", r.Outcome)
	}
}

// A conversational answer has NO plan, and that is now a complete record.
func TestAConversationalAnswerIsCompleteWithNoPlan(t *testing.T) {
	restore := governorIdentityFn
	governorIdentityFn = func() (runreceipt.Value, runreceipt.Value) {
		return runreceipt.MeasuredValue("3e5ade13", "runtime/debug build info vcs.revision"),
			runreceipt.MeasuredValue(strings.Repeat("a", 64), "sha256 of the executable this process is running")
	}
	defer func() { governorIdentityFn = restore }()

	e := &Engine{}
	e.beginReceipt("task-1")
	e.noteServingProducer("task-1", os.Getpid(), true)
	e.noteWorld("task-1", "f01592b0", "42e6e12c")
	e.notePlan("task-1", "", "") // the architect replied instead of planning
	r := e.emitReceipt("task-1", event.WorkflowFailed, runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if r.PlanState != runreceipt.PlanNone {
		t.Fatalf("plan state = %s, want NONE", r.PlanState)
	}
	if state, missing := r.Completeness(); state != runreceipt.Complete {
		t.Fatalf("a run with no plan is a complete record of a run with no plan: %v", missing)
	}
	// And a plan claimed absent while a digest is recorded is a contradiction.
	e.beginReceipt("task-2")
	e.noteServingProducer("task-2", os.Getpid(), true)
	e.noteWorld("task-2", "f01592b0", "42e6e12c")
	e.notePlan("task-2", "990090fd", "")
	e.withReceipt("task-2", func(f *receiptFacts) { f.planState = runreceipt.PlanNone })
	r2 := e.emitReceipt("task-2", event.WorkflowFailed, runreceipt.OutcomeUnreviewed, runreceipt.CandidateNone)
	if state, missing := r2.Completeness(); state != runreceipt.Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "plan_state is NONE") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
}

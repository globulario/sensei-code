//go:build linux

package workflow

// N5 P1 witnesses (RULING-358 §10-§15, §18 K, M, O, P and Q): an execution
// whose teardown is unresolved quarantines the whole task at the one owner
// (boundedRun), and the durable record -- not a latch of the process that
// wrote it -- refuses every later reuse, observation, disposal, resume and
// N3 binding. Fixture launchers; no real D2 executor runs here.

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/broker"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/validation"
)

// n4SilentLauncher runs every execution as n4Launcher does, except that for
// executions whose role, workspace mode or argv match `when` it runs the
// command and writes NO record: the execution may have started and nothing
// proves it ended.
func n4SilentLauncher(when string) string {
	return "#!/bin/sh\nread nonce <&5\nexec 5<&-\n" +
		"case \"$SENSEI_CONFINE_ROLE $SENSEI_CONFINE_WORKSPACE_MODE $*\" in " + when + ") while [ \"$1\" != \"--\" ]; do shift; done; shift; \"$@\" 3<&- 4>&-; exit 0;; esac\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&-\nrc=$?\necho \"" + validation.StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
}

func quarantinedRecord(t *testing.T, root string) candidate.Identity {
	t.Helper()
	id, ok, err := candidate.Load(root, "task-1")
	if err != nil || !ok || id.Resolution == nil || id.Resolution.Disposition != candidate.Quarantined || id.InFlight == nil {
		t.Fatalf("the candidate is not durably quarantined: %+v (%v)", id, err)
	}
	return id
}

// K. The candidate's own execution is proven, but the baseline attribution
// ran has no record: the whole task is quarantined, the baseline observation
// worktree is kept (its ordinary removal suppressed) and named, and the
// failure is not attributed.
func TestAnUnresolvedBaselineQuarantinesTheTaskAndKeepsItsWorktree(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4SilentLauncher("baseline*"))
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Permissions.RunTests = true
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	var u *WorkspaceUnsettled
	if !errors.As(err, &u) || u.Phase != "certify" || u.Baseline == "" || !u.Recorded || len(b.Checks) != 0 {
		t.Fatalf("an unresolved baseline did not quarantine the task: err=%v bundle=%+v", err, b)
	}
	last := u.Evidence[len(u.Evidence)-1]
	if last.Teardown != validation.TeardownProven || last.BaselineTeardown != validation.TeardownUnresolved ||
		last.Attribution == "candidate" || last.Attribution == "pre-existing" {
		t.Fatalf("the baseline uncertainty was collapsed into an attribution: %+v", last)
	}
	if st, statErr := os.Stat(u.Baseline); statErr != nil || !st.IsDir() {
		t.Fatalf("the baseline workspace an execution may still be writing was removed: %v", statErr)
	}
	id := quarantinedRecord(t, repo.Root)
	if !strings.Contains(id.Resolution.Reason, u.Baseline) || !strings.Contains(id.Resolution.Reason, u.Invocation) {
		t.Fatalf("the quarantine does not name the baseline workspace and the invocation: %q", id.Resolution.Reason)
	}
}

// The in-flight boundary is durable BEFORE the confined command can act: the
// command itself reads the candidate record and finds its own execution in
// flight (RULING-358 §12). After a proven teardown the record is settled.
func TestTheInFlightBoundaryIsDurableBeforeTheExecutionStarts(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4Launcher)
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", `grep -q '"in_flight"' .sensei-code/candidates/task-1.json`}}}
	e.Config.Permissions.RunTests = true
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	if err != nil || !b.Passed() {
		t.Fatalf("the execution did not find itself recorded in flight: err=%v bundle=%+v", err, b)
	}
	if id, _, _ := candidate.Load(repo.Root, "task-1"); id.Unsettled() {
		t.Fatalf("a proven execution left the record unsettled: %+v", id)
	}
}

// The implement entry consults the durable record before it creates or
// reuses the candidate worktree, and the ordinary resume path refuses an
// unsettled identity where it loads it (RULING-358 §14). Structural: the
// full entry paths need a live architect and Sensei, and the gates they call
// are witnessed above.
func TestTheEntryGatesPrecedeWorktreeUse(t *testing.T) {
	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	impl := s[strings.Index(s, "func (e *Engine) implement("):]
	impl = impl[:strings.Index(impl, "\n}\n")]
	gate, create := strings.Index(impl, "unsettledCandidate(e.Repo.Root, taskID)"), strings.Index(impl, "e.Repo.CreateWorktreeAt(")
	if gate < 0 || create < 0 || gate > create {
		t.Fatalf("implement does not consult the durable record before the worktree (gate %d, create %d)", gate, create)
	}
	if !strings.Contains(impl, "if isWorkspaceSafetyStop(err) {") {
		t.Fatal("implement does not stop at once on a workspace safety stop")
	}
	// The candidate loop returns on a required test's boundary refusal before
	// anything is handed to the reviewer (J and M on the named-test path;
	// the broker's own stop is witnessed in the validation package).
	req := strings.Index(s, "requiredTestEvidence(ctx, taskID, envelope, candidate, diff, audit)")
	if req < 0 || !strings.Contains(s[req:req+500], "if requiredErr != nil {\n") ||
		!strings.Contains(s[req:req+600], "return candidateNotConverged, plan, lastReview, lastAudit, requiredErr") {
		t.Fatal("the candidate loop does not return on a required test's unsettled execution")
	}
	resume := strings.Index(s, `fail(fmt.Errorf("cannot resume %s: no candidate identity was recorded for it", task.TaskID))`)
	if resume < 0 || !strings.Contains(s[resume:resume+400], "if q := identity.Quarantine(); q != nil {") {
		t.Fatal("the ordinary resume path does not refuse an unsettled identity where it loads it")
	}
}

// countingRunner answers like answeringRunner and counts the review turns.
type countingRunner struct {
	answeringRunner
	turns *int32
}

func (r countingRunner) Run(ctx context.Context, req agent.Request, on func(event.Event)) (agent.Result, error) {
	atomic.AddInt32(r.turns, 1)
	return r.answeringRunner.Run(ctx, req, on)
}

// M. On the real candidate loop: the formatter is proven and the final
// (certifying) validation is unresolved. The loop ends on the typed outcome:
// no reviewer turn, no recapture, no acceptance, and the candidate is
// durably quarantined with its worktree in place.
func TestAnUnresolvedFinalValidationNeverReachesTheReviewer(t *testing.T) {
	h := newGateHarness(t, roles.Policy{}, roles.Fresh, "accept")
	var turns int32
	h.engine.Runners = roleResolver{reviewer: countingRunner{answeringRunner{text: `{"decision":"accept","summary":"fine"}`, mode: roles.Fresh}, &turns},
		name: "remote:abc", session: "session-1"}
	h.engine.Config.Validation.Confined = true
	h.engine.Config.Validation.Confinement = pinnedWrapper(t, n4SilentLauncher("candidate\\ ro*"))
	outcome, _, _, _, err := h.engine.runCandidate(fixtureCtx(h.engine, "task-1"), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	var u *WorkspaceUnsettled
	if !errors.As(err, &u) || u.Phase != "certify" || !isWorkspaceSafetyStop(err) || outcome.Accepted() {
		t.Fatalf("an unresolved final validation did not end the loop on the typed outcome: %q %v", outcome, err)
	}
	if atomic.LoadInt32(&turns) != 0 {
		t.Fatalf("the reviewer was handed a candidate whose execution may still be writing it (%d turns)", turns)
	}
	for ev := range drainChan(h.events) {
		if ev.Kind == event.ReviewStarted || ev.Kind == event.ReviewCompleted {
			t.Fatalf("a review was started on an unsettled candidate: %+v", ev)
		}
	}
	quarantinedRecord(t, h.engine.Repo.Root)
	if _, statErr := os.Stat(h.work); statErr != nil {
		t.Fatalf("the quarantined candidate's worktree is gone: %v", statErr)
	}
}

func drainChan(events <-chan event.Event) <-chan event.Event {
	out := make(chan event.Event, 4096)
	for {
		select {
		case ev := <-events:
			out <- ev
		default:
			close(out)
			return out
		}
	}
}

// O. A fresh engine over the same repository -- no latch, only the durable
// record -- refuses to resume, observe, dispose of or reuse the quarantined
// candidate, and leaves its worktree in place.
func TestAFreshEngineRefusesAQuarantinedCandidate(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4GarbageStatusLauncher("rw*"))
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil); !isWorkspaceSafetyStop(err) {
		t.Fatalf("premise: the formatter's execution was not quarantined: %v", err)
	}
	refusesEverything(t, repo.Root, repo.Root)
}

// Q. A process that recorded an execution in flight and died before any
// outcome (no quarantine was ever written) leaves a record a fresh engine
// reads as unsettled: every ordinary path refuses, and no new confined
// execution starts on the task.
func TestAnInterruptedExecutionIsUnsettledAfterRestart(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4Launcher)
	if _, err := candidate.BeginExecution(repo.Root, "task-1", candidate.InFlight{Invocation: "exec-crashed", Phase: "certify", Workspace: repo.Root}); err != nil {
		t.Fatal(err)
	}
	refusesEverything(t, repo.Root, repo.Root)
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	fresh := &Engine{Repo: repo, Config: e.Config, Bus: event.NewBus()}
	b, _, err := fresh.validate(ctx, "task-1", base, broker.New(fresh.Config.Permissions), repo, before.Diff, nil)
	var q *candidate.ErrQuarantined
	if !errors.As(err, &q) || len(b.Checks) != 0 {
		t.Fatalf("a confined execution started over an unsettled interrupted one: %v %+v", err, b)
	}
	if _, statErr := os.Stat(repo.Root + "/" + "main.go"); statErr != nil {
		t.Fatal(statErr)
	}
}

// refusesEverything drives the ordinary entry points of a fresh engine over
// root against task-1's durable record.
func refusesEverything(t *testing.T, root, worktree string) {
	t.Helper()
	fresh := &Engine{Repo: gitx.Repo{Root: root}, Bus: event.NewBus(), SessionID: "session-fresh"}
	ctx := context.Background()
	var q *candidate.ErrQuarantined
	if err := fresh.resumePrecondition(ctx, "task-1"); !errors.As(err, &q) {
		t.Fatalf("resume was not refused: %v", err)
	}
	if err := unsettledCandidate(root, "task-1"); !errors.As(err, &q) {
		t.Fatalf("the implement gate (worktree creation or reuse) was not refused: %v", err)
	}
	id, _, _ := candidate.Load(root, "task-1")
	if seen := observeInherited(ctx, id); !errors.As(seen.Err, &q) {
		t.Fatalf("an unsettled candidate was observed: %+v", seen)
	}
	// Disposal speaks on behalf of an admitted invocation of the task, as the
	// run does: a durable record and the task's root, so its refusal is
	// published where this witness can read it.
	withFixtureStore(t, fresh)
	rootFixtureTaskOnce(t, fresh, "task-1", "task")
	events, cancel := fresh.Bus.Subscribe(64)
	defer cancel()
	fresh.disposeIfEmpty(fixtureCtx(fresh, "task-1"), "task-1", id, &taskContext{}, worktree, "nothing produced")
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("an unsettled candidate's worktree was removed: %v", err)
	}
	// The disposal gate itself refused, before any observation: the record's
	// overwrite guard is a second, independent layer and is not what this
	// asserts.
	gated := false
	for ev := range drainChan(events) {
		if strings.HasPrefix(ev.Summary, "candidate neither observed nor removed: ") {
			gated = true
		}
	}
	if !gated {
		t.Fatal("disposal reached the candidate instead of refusing at its gate")
	}
	after, _, _ := candidate.Load(root, "task-1")
	if !after.Unsettled() {
		t.Fatalf("disposal replaced the unsettled record: %+v", after)
	}
}

// P. The N3 execution service binds to no quarantined task, and a service
// bound before the quarantine refuses at its next authority revalidation.
func TestTheExecutionServiceRefusesAQuarantinedTask(t *testing.T) {
	r := newExecRig(t, "exit 0")
	if _, refusal := r.svc.revalidate(); refusal != nil {
		t.Fatalf("premise: the bound service was refused before any quarantine: %+v", refusal)
	}
	if _, err := candidate.BeginExecution(r.repo, n3Task, candidate.InFlight{Invocation: "exec-1", Phase: "certify", Workspace: r.work}); err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.QuarantineExecution(r.repo, n3Task, candidate.Resolution{Reason: "unresolved teardown"}); err != nil {
		t.Fatal(err)
	}
	if _, refusal := r.svc.revalidate(); refusal == nil || !strings.Contains(refusal.Error(), "quarantined") {
		t.Fatalf("a bound service kept its authority over a quarantined task: %+v", refusal)
	}
	var q *candidate.ErrQuarantined
	if _, err := r.e.startImplementerExec(fixtureCtx(r.e, n3Task), n3Task, r.work, n3Runner); !errors.As(err, &q) {
		t.Fatalf("a service was bound to a quarantined task: %v", err)
	}
}

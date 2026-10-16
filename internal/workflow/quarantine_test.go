//go:build linux

package workflow

// N5 P1 witnesses (RULING-358 §10-§15, §18 K, M, O, P and Q): an execution
// whose teardown is unresolved quarantines the whole task at the one owner
// (boundedRun), and the durable record -- not a latch of the process that
// wrote it -- refuses every later reuse, observation, disposal, resume and
// N3 binding. Fixture launchers; no real D2 executor runs here.

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/broker"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
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
	// The baseline was named on the durable in-flight record before its
	// execution started, so a crash during attribution keeps its identity.
	if id.InFlight.Baseline != u.Baseline {
		t.Fatalf("the in-flight record does not name the baseline workspace: %+v", id.InFlight)
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

// C1-F1, F4 (RULING-360). A failing formatter creates the baseline under the
// format invocation, which settles; certification reuses the cached baseline
// and that baseline execution is unresolved. The certify invocation's durable
// in-flight record must name the baseline before it ran, and the baseline
// survives for a fresh engine.
func TestACachedBaselineIsBoundToTheCurrentInvocation(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4SilentLauncher("baseline\\ ro*"))
	e.Config.Validation.Format = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Validation.FormatVerify = nil
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Permissions.RunTests = true
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	var u *WorkspaceUnsettled
	if !errors.As(err, &u) || u.Phase != "certify" || u.Baseline == "" {
		t.Fatalf("premise: the reused baseline's unresolved execution did not quarantine certify: %v", err)
	}
	id := quarantinedRecord(t, repo.Root)
	if id.InFlight.Invocation != u.Invocation || id.InFlight.Baseline != u.Baseline {
		t.Fatalf("the certify invocation's durable record does not name the reused baseline: in-flight %+v, unsettled %s at %s",
			id.InFlight, u.Invocation, u.Baseline)
	}
	if st, statErr := os.Stat(u.Baseline); statErr != nil || !st.IsDir() {
		t.Fatalf("the reused baseline workspace was removed: %v", statErr)
	}
	refusesEverything(t, repo.Root, repo.Root)
}

// n4MarkingLauncher is n4Launcher that also leaves a marker whenever it runs a
// baseline execution, so a witness can tell whether one ran.
func n4MarkingLauncher(marker string) string {
	return "#!/bin/sh\nread nonce <&5\nexec 5<&-\nif [ \"$SENSEI_CONFINE_ROLE\" = baseline ]; then touch '" + marker + "'; fi\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&-\nrc=$?\necho \"" + validation.StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
}

// C1-F2 (RULING-360 §5). The baseline cannot be bound to the in-flight
// invocation (the candidate record's directory refuses the write): no
// baseline execution runs -- not on the first request and not on a later
// one through a cached path -- and validation ends on a typed safety stop.
func TestABaselineThatCannotBeBoundNeverRuns(t *testing.T) {
	marker := t.TempDir() + "/baseline-ran"
	e, repo, base := confinedFormattingRig(t, n4MarkingLauncher(marker))
	candidates := repo.Root + "/.sensei-code/candidates"
	t.Cleanup(func() { _ = os.Chmod(candidates, 0o755) })
	e.Config.Validation.FormatVerify = nil
	e.Config.Validation.Vet = []config.Command{{Command: "sh", Args: []string{"-c", "chmod 555 .sensei-code/candidates; exit 1"}}}
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Permissions.RunBuilds, e.Config.Permissions.RunTests = true, true
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	if !isWorkspaceSafetyStop(err) {
		t.Fatalf("a failed baseline binding did not end on a typed safety stop: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a baseline execution ran without a durable binding to the in-flight invocation")
	}
	// B5 (RULING-361 §7). The batch's settlement could not be established
	// either, so nothing proves the workspace is free: the baseline is kept.
	if st, statErr := os.Stat(observationPath(repo)); statErr != nil || !st.IsDir() {
		t.Fatalf("a baseline whose batch never settled was removed: %v", statErr)
	}
}

// observationPath is the deterministic baseline observation worktree of
// task-1 (gitx.CreateObservationWorktree).
func observationPath(repo gitx.Repo) string {
	return repo.WorktreePath("task-1-baseline") + "-observe"
}

// C1-F2, the conversion (RULING-360 §5; independent review finding 1). The
// binding fails while the batch's own boundary does not: a later check of the
// same batch restores the record directory, so the in-flight record settles.
// The batch must still end on the typed boundary stop naming the failed
// binding, not as ordinary validation evidence, and no baseline runs.
func TestAFailedBaselineBindingAloneEndsOnTheBoundaryStop(t *testing.T) {
	marker := t.TempDir() + "/baseline-ran"
	e, repo, base := confinedFormattingRig(t, n4MarkingLauncher(marker))
	candidates := repo.Root + "/.sensei-code/candidates"
	t.Cleanup(func() { _ = os.Chmod(candidates, 0o755) })
	e.Config.Validation.FormatVerify = nil
	e.Config.Validation.Vet = []config.Command{{Command: "sh", Args: []string{"-c", "chmod 555 .sensei-code/candidates; exit 1"}}}
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", "chmod 755 .sensei-code/candidates; exit 0"}}}
	e.Config.Permissions.RunBuilds, e.Config.Permissions.RunTests = true, true
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	var stop *ExecutionBoundaryFailure
	if !errors.As(err, &stop) || !strings.Contains(err.Error(), "could not be bound to the in-flight invocation, so it did not run") {
		t.Fatalf("a failed baseline binding did not end on its own typed boundary stop: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr == nil {
		t.Fatal("a baseline execution ran without a durable binding to the in-flight invocation")
	}
	if fresh, _, _ := candidate.Load(repo.Root, "task-1"); fresh.InFlight != nil {
		t.Fatalf("the batch's own boundary did not settle, so the stop is not the binding's: %+v", fresh.InFlight)
	}
	// B1 (RULING-361 §7). No baseline ran and the batch settled: the detached
	// observation worktree no execution holds is removed, not retained.
	if _, statErr := os.Stat(observationPath(repo)); !os.IsNotExist(statErr) {
		t.Fatalf("an unbound baseline that never ran was retained after its batch settled: %v", statErr)
	}
	// B2. A second validation of the task recreates the same deterministic
	// observation worktree and runs its baseline, bound, normally.
	e.Config.Validation.Vet = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Validation.Test = nil
	if _, _, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil); err != nil {
		t.Fatalf("the next validation could not recreate the observation worktree: %v", err)
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatal("the next validation did not run its bound baseline")
	}
}

// B3 (RULING-361 §5, §7). The formatter's baseline execution ran, proven and
// settled; certification then fails to bind the cached baseline, and its
// batch settles. No later baseline ran, nothing is unresolved, so the
// baseline is removed -- the earlier settled execution holds no workspace.
func TestASettledEarlierBaselineDoesNotKeepAnUnboundOne(t *testing.T) {
	marker := t.TempDir() + "/baseline-runs"
	launcher := "#!/bin/sh\nread nonce <&5\nexec 5<&-\nif [ \"$SENSEI_CONFINE_ROLE\" = baseline ]; then echo run >> '" + marker + "'; fi\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&-\nrc=$?\necho \"" + validation.StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
	e, repo, base := confinedFormattingRig(t, launcher)
	candidates := repo.Root + "/.sensei-code/candidates"
	t.Cleanup(func() { _ = os.Chmod(candidates, 0o755) })
	e.Config.Validation.Format = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Validation.FormatVerify = nil
	e.Config.Validation.Test = []config.Command{
		{Command: "sh", Args: []string{"-c", "chmod 555 .sensei-code/candidates; exit 1"}},
		{Command: "sh", Args: []string{"-c", "chmod 755 .sensei-code/candidates; exit 0"}},
	}
	e.Config.Permissions.RunTests = true
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	var stop *ExecutionBoundaryFailure
	if !errors.As(err, &stop) || stop.Phase != "certify" {
		t.Fatalf("premise: certification's failed binding did not end on its boundary stop: %v", err)
	}
	if runs, _ := os.ReadFile(marker); strings.Count(string(runs), "run") != 1 {
		t.Fatalf("premise: exactly the formatter's baseline should have run, ran %q", runs)
	}
	if fresh, _, _ := candidate.Load(repo.Root, "task-1"); fresh.Unsettled() {
		t.Fatalf("premise: the batches did not settle: %+v", fresh)
	}
	if _, statErr := os.Stat(observationPath(repo)); !os.IsNotExist(statErr) {
		t.Fatalf("a baseline whose only execution was proven and settled was retained: %v", statErr)
	}
}

// C1-F3. A baseline created in one bounded invocation and correctly rebound
// in the next keeps ordinary attribution: the certify failure that the base
// reproduces is pre-existing, both executions are proven, and the record is
// settled.
func TestARebindedCachedBaselineKeepsOrdinaryAttribution(t *testing.T) {
	e, repo, base := confinedFormattingRig(t, n4Launcher)
	e.Config.Validation.Format = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Validation.FormatVerify = nil
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Permissions.RunTests = true
	ctx := context.Background()
	before, err := repo.CandidateCapture(ctx, base, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, _, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, before.Diff, nil)
	if err != nil {
		t.Fatal(err)
	}
	last := b.Checks[len(b.Checks)-1]
	if last.Kind != validation.Test || last.Attribution != "pre-existing" ||
		last.Teardown != validation.TeardownProven || last.BaselineTeardown != validation.TeardownProven {
		t.Fatalf("the rebound cached baseline did not attribute normally: %+v", last)
	}
	if id, _, _ := candidate.Load(repo.Root, "task-1"); id.Unsettled() {
		t.Fatalf("a proven validation left the record unsettled: %+v", id)
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
	// The quarantine's projection is a record the session ledger accepts: it
	// is durably recorded, and the invocation was not fenced as a ledger
	// fault for writing it.
	history, err := h.engine.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	recorded := false
	for _, ev := range history {
		if ev.TaskID == "task-1" && ev.Kind == event.Status && strings.Contains(ev.Summary, "the candidate is durably quarantined") {
			recorded = true
		}
	}
	if !recorded {
		t.Fatal("the quarantine was not recorded in the session ledger")
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

// RULING-360 §12. The candidate record's lock serializes record transactions,
// not task operations. What keeps a second process from acting on a task's
// worktree after its safety check went stale is the task's INVOCATION LEASE
// (session.Store.AcquireTaskInvocation): one kernel lock per task at the
// repository's level, taken by Run (openRun) and Resume (ResumeTask) before
// either reaches implement or validate, and held until the invocation ends.
// TestHelperLeaseHolder is the other process: it holds the lease as a live
// invocation of the task would.
func TestHelperLeaseHolder(t *testing.T) {
	root := os.Getenv("N5_LEASE_ROOT")
	if root == "" {
		t.Skip("helper")
	}
	store, err := session.New(root, "session-holder")
	if err != nil {
		os.Exit(3)
	}
	lease, err := store.AcquireTaskInvocation(context.Background(), "t-lease")
	if err != nil {
		os.Exit(4)
	}
	_ = lease
	os.WriteFile(filepath.Join(root, "leased"), nil, 0o644)
	time.Sleep(time.Minute)
	os.Exit(0)
}

func TestAnotherProcessCannotOperateATaskWhoseInvocationIsLive(t *testing.T) {
	root := t.TempDir()
	child := exec.Command(os.Args[0], "-test.run=^TestHelperLeaseHolder$")
	child.Env = append(os.Environ(), "N5_LEASE_ROOT="+root)
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = child.Process.Kill(); _ = child.Wait() })
	for i := 0; i < 4000; i++ {
		if _, err := os.Stat(filepath.Join(root, "leased")); err == nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	for _, sessionID := range []string{"session-holder", "session-fresh"} {
		store, err := session.New(root, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.AcquireTaskInvocation(t.Context(), "t-lease"); !errors.Is(err, session.ErrTaskInvocationLeased) {
			t.Fatalf("%s: a second invocation of a task another process is running was not refused: %v", sessionID, err)
		}
	}
	// The kernel ends the lease with its holder, so a crash leaves the task
	// resumable -- and the resumer then meets the durable record's gates.
	_ = child.Process.Kill()
	_ = child.Wait()
	store, _ := session.New(root, "session-holder")
	lease, err := store.AcquireTaskInvocation(t.Context(), "t-lease")
	if err != nil {
		t.Fatalf("the lease of a dead holder was not released: %v", err)
	}
	lease.Release()

	// Both entries take the lease before any worktree operation.
	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	run := s[strings.Index(s, "func (e *Engine) openRun("):]
	run = run[:strings.Index(run, "\n}\n")]
	if !strings.Contains(run, "e.leaseInvocation(leaseCtx, inv, e.Store, event.TaskCreated)") {
		t.Fatal("Run does not take the task's invocation lease when it opens")
	}
	if open, impl := strings.Index(s, "inv, ok := e.openRun(ctx, taskID, task)"), strings.Index(s, "e.implement(ctx, sc, start, taskID, &tc, plan, \"\", fail)"); open < 0 || impl < 0 || open > impl {
		t.Fatalf("Run reaches implement before it opens its leased invocation (%d, %d)", open, impl)
	}
	resume := s[strings.Index(s, "func (e *Engine) ResumeTask("):]
	lease2, impl2 := strings.Index(resume, "e.leaseInvocation(ctx, inv, e.Store, session.SessionLineageBound)"), strings.Index(resume, "e.implement(ctx, sc, start, task.TaskID, &tc, plan, carried, fail)")
	if lease2 < 0 || impl2 < 0 || lease2 > impl2 {
		t.Fatalf("Resume reaches implement before it takes the task's lease (%d, %d)", lease2, impl2)
	}
}

// injectPreStart substitutes the boundary recorder for one test (RULING-362
// §13: trusted test-side fault injection, no candidate code). fail decides,
// for the n-th boundary (from 1), whether that boundary fails before its
// execution starts and how; nil keeps the real recorder.
func injectPreStart(t *testing.T, fail func(n int, root, taskID string, f candidate.InFlight) (candidate.Identity, error, bool)) {
	t.Helper()
	saved := beginExecution
	t.Cleanup(func() { beginExecution = saved })
	n := 0
	beginExecution = func(root, taskID string, f candidate.InFlight) (candidate.Identity, error) {
		n++
		if id, err, inject := fail(n, root, taskID, f); inject {
			return id, err
		}
		return saved(root, taskID, f)
	}
}

// countingBaselineLauncher is n4Launcher that appends one line to marker
// for every baseline execution it runs.
func countingBaselineLauncher(marker string) string {
	return "#!/bin/sh\nread nonce <&5\nexec 5<&-\nif [ \"$SENSEI_CONFINE_ROLE\" = baseline ]; then echo run >> '" + marker + "'; fi\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&-\nrc=$?\necho \"" + validation.StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
}

// preStartRig is a formatter that fails, so its batch creates and runs the
// baseline (proven, settled), and a certification that fails too, so it
// would reuse that baseline -- if its own boundary ever started.
func preStartRig(t *testing.T) (*Engine, gitx.Repo, string, string, string) {
	t.Helper()
	marker := t.TempDir() + "/baseline-runs"
	e, repo, base := confinedFormattingRig(t, countingBaselineLauncher(marker))
	e.Config.Validation.Format = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Validation.FormatVerify = nil
	e.Config.Validation.Test = []config.Command{{Command: "sh", Args: []string{"-c", "exit 1"}}}
	e.Config.Permissions.RunTests = true
	before, err := repo.CandidateCapture(context.Background(), base, nil)
	if err != nil {
		t.Fatal(err)
	}
	return e, repo, base, before.Diff, marker
}

func baselineRuns(marker string) int {
	b, _ := os.ReadFile(marker)
	return strings.Count(string(b), "run")
}

var errInjectedPreStart = errors.New("injected: the boundary could not be recorded")

// W1 and W2 (RULING-362 §12). The formatter's baseline execution is proven
// and settled; certification's boundary fails before its execution starts.
// The typed stop stands, no further baseline runs, nothing protects the
// baseline -- so it is removed, and the next validation recreates the same
// deterministic observation worktree.
func TestAPreStartFailureLeavesNoOrphanBaseline(t *testing.T) {
	e, repo, base, diff, marker := preStartRig(t)
	injectPreStart(t, func(n int, _, _ string, _ candidate.InFlight) (candidate.Identity, error, bool) {
		return candidate.Identity{}, errInjectedPreStart, n == 2
	})
	ctx := context.Background()
	_, _, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	var stop *ExecutionBoundaryFailure
	if !errors.As(err, &stop) || stop.Phase != "certify" || !errors.Is(err, errInjectedPreStart) {
		t.Fatalf("premise: certification did not stop on its pre-start boundary failure: %v", err)
	}
	if runs := baselineRuns(marker); runs != 1 {
		t.Fatalf("premise: exactly the formatter's baseline should have run, ran %d", runs)
	}
	if id, _, _ := candidate.Load(repo.Root, "task-1"); id.Unsettled() {
		t.Fatalf("premise: the formatter's batch did not settle: %+v", id)
	}
	if _, statErr := os.Stat(observationPath(repo)); !os.IsNotExist(statErr) {
		t.Fatalf("a baseline no execution holds was retained after a pre-start failure: %v", statErr)
	}
	beginExecution = candidate.BeginExecution
	if _, _, err := e.validate(ctx, "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil); err != nil {
		t.Fatalf("the next validation could not recreate the observation worktree: %v", err)
	}
	if runs := baselineRuns(marker); runs < 2 {
		t.Fatalf("the next validation did not run its baseline: %d", runs)
	}
}

// W3. The boundary is written and its durability step then fails: no
// execution started, and the record keeps the in-flight boundary -- it is
// not cleared to tidy up. Every later operation on the candidate refuses.
func TestABoundaryThatFailedAfterItsWriteKeepsTheCandidateUnsettled(t *testing.T) {
	e, repo, base, diff, marker := preStartRig(t)
	var attempted string
	injectPreStart(t, func(n int, root, taskID string, f candidate.InFlight) (candidate.Identity, error, bool) {
		if n != 2 {
			return candidate.Identity{}, nil, false
		}
		attempted = f.Invocation
		id, err := candidate.BeginExecution(root, taskID, f)
		if err != nil {
			t.Fatalf("premise: the boundary was not written: %v", err)
		}
		return id, errInjectedPreStart, true
	})
	_, _, err := e.validate(context.Background(), "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	var stop *ExecutionBoundaryFailure
	if !errors.As(err, &stop) || stop.Phase != "certify" {
		t.Fatalf("premise: certification did not stop on its boundary failure: %v", err)
	}
	if runs := baselineRuns(marker); runs != 1 {
		t.Fatalf("an execution started after its boundary failed: %d baseline runs", runs)
	}
	id, _, _ := candidate.Load(repo.Root, "task-1")
	if id.InFlight == nil || id.InFlight.Invocation != attempted {
		t.Fatalf("the written boundary was cleared instead of protecting the candidate: %+v", id.InFlight)
	}
	// W14 (RULING-363 §7, §12). The locked record holds only this attempted
	// boundary, with no baseline bound to it, and every other condition is
	// established: the baseline no execution holds is removed, while the
	// boundary itself stays on the record.
	if _, statErr := os.Stat(observationPath(repo)); !os.IsNotExist(statErr) {
		t.Fatalf("the baseline was not removed although only the unstarted boundary is recorded: %v", statErr)
	}
	refusesEverything(t, repo.Root, repo.Root)
}

// W6. A boundary refused because the task is already quarantined is not a
// pre-start failure to tidy after: the quarantine protects the baseline.
func TestAnExistingQuarantineIsNotBypassedByThePreStartClassification(t *testing.T) {
	e, repo, base, diff, _ := preStartRig(t)
	injectPreStart(t, func(n int, root, taskID string, _ candidate.InFlight) (candidate.Identity, error, bool) {
		if n != 2 {
			return candidate.Identity{}, nil, false
		}
		if _, err := candidate.BeginExecution(root, taskID, candidate.InFlight{Invocation: "exec-earlier", Phase: "certify", Workspace: root, Profile: "d2-v3"}); err != nil {
			t.Fatal(err)
		}
		q, err := candidate.QuarantineExecution(root, taskID, candidate.Resolution{Reason: "an earlier unresolved execution"})
		if err != nil {
			t.Fatal(err)
		}
		return candidate.Identity{}, q.Quarantine(), true
	})
	_, _, err := e.validate(context.Background(), "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	var q *candidate.ErrQuarantined
	if !errors.As(err, &q) {
		t.Fatalf("premise: certification was not refused by the quarantine: %v", err)
	}
	if st, statErr := os.Stat(observationPath(repo)); statErr != nil || !st.IsDir() {
		t.Fatalf("a baseline under a quarantine was removed: %v", statErr)
	}
}

// W7. The boundary failed before start, but the durable record can no
// longer be read: its safety state is not established, so the baseline is
// kept.
func TestAnUnreadableRecordKeepsTheBaselineAfterAPreStartFailure(t *testing.T) {
	e, repo, base, diff, _ := preStartRig(t)
	injectPreStart(t, func(n int, root, taskID string, _ candidate.InFlight) (candidate.Identity, error, bool) {
		if n != 2 {
			return candidate.Identity{}, nil, false
		}
		if err := os.WriteFile(filepath.Join(root, ".sensei-code", "candidates", taskID+".json"), []byte("{torn"), 0o644); err != nil {
			t.Fatal(err)
		}
		return candidate.Identity{}, errInjectedPreStart, true
	})
	_, _, err := e.validate(context.Background(), "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	var stop *ExecutionBoundaryFailure
	if !errors.As(err, &stop) {
		t.Fatalf("premise: certification did not stop on its boundary failure: %v", err)
	}
	if st, statErr := os.Stat(observationPath(repo)); statErr != nil || !st.IsDir() {
		t.Fatalf("a baseline was removed although the record's safety state was not established: %v", statErr)
	}
}

func recordFile(root string) string {
	return filepath.Join(root, ".sensei-code", "candidates", "task-1.json")
}

func assertBaselineKept(t *testing.T, repo gitx.Repo, err error, why string) {
	t.Helper()
	var stop *ExecutionBoundaryFailure
	if !errors.As(err, &stop) || stop.Phase != "certify" || !errors.Is(err, errInjectedPreStart) {
		t.Fatalf("premise: certification did not stop on its pre-start boundary failure: %v", err)
	}
	if st, statErr := os.Stat(observationPath(repo)); statErr != nil || !st.IsDir() {
		t.Fatalf("%s, yet the baseline was removed: %v", why, statErr)
	}
}

// W10 (RULING-363 §8, §12). The pre-start failure is the record lock being
// held by an independent owner -- this test, through its own descriptor on
// the disposable fixture's lock file -- and the locked read cannot acquire it
// either (bounded by the record's lock wait): the durable state is not
// established, so the baseline is kept.
func TestALockHeldElsewhereKeepsTheBaseline(t *testing.T) {
	e, repo, base, diff, _ := preStartRig(t)
	var held *os.File
	t.Cleanup(func() {
		if held != nil {
			_ = syscall.Flock(int(held.Fd()), syscall.LOCK_UN)
			_ = held.Close()
		}
	})
	injectPreStart(t, func(n int, root, _ string, _ candidate.InFlight) (candidate.Identity, error, bool) {
		if n != 2 {
			return candidate.Identity{}, nil, false
		}
		f, err := os.OpenFile(recordFile(root)+".lock", os.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
			t.Fatal(err)
		}
		held = f
		return candidate.Identity{}, errInjectedPreStart, true
	})
	_, _, err := e.validate(context.Background(), "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	assertBaselineKept(t, repo, err, "the record lock was held elsewhere so no serialized read was possible")
}

// W11. The record's lock object has been replaced by a symlink: the locked
// read refuses, the baseline is kept, and the record is not written.
func TestAnInvalidLockObjectKeepsTheBaseline(t *testing.T) {
	e, repo, base, diff, _ := preStartRig(t)
	var before []byte
	injectPreStart(t, func(n int, root, _ string, _ candidate.InFlight) (candidate.Identity, error, bool) {
		if n != 2 {
			return candidate.Identity{}, nil, false
		}
		lock := recordFile(root) + ".lock"
		if err := os.Remove(lock); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
			t.Fatal(err)
		}
		before, _ = os.ReadFile(recordFile(root))
		return candidate.Identity{}, errInjectedPreStart, true
	})
	_, _, err := e.validate(context.Background(), "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	assertBaselineKept(t, repo, err, "the lock object was invalid")
	if after, _ := os.ReadFile(recordFile(repo.Root)); string(after) != string(before) {
		t.Fatal("the record was written through an invalid lock object")
	}
}

// W12. The locked record names another invocation's execution in flight:
// the pre-start failure proves nothing about that execution, so the baseline
// is kept and the record is left exactly as it is.
func TestAnotherInvocationInFlightKeepsTheBaseline(t *testing.T) {
	e, repo, base, diff, _ := preStartRig(t)
	injectPreStart(t, func(n int, root, taskID string, _ candidate.InFlight) (candidate.Identity, error, bool) {
		if n != 2 {
			return candidate.Identity{}, nil, false
		}
		if _, err := candidate.BeginExecution(root, taskID, candidate.InFlight{Invocation: "exec-other", Phase: "certify", Workspace: root, Profile: "d2-v3"}); err != nil {
			t.Fatal(err)
		}
		return candidate.Identity{}, errInjectedPreStart, true
	})
	_, _, err := e.validate(context.Background(), "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	assertBaselineKept(t, repo, err, "another invocation is recorded in flight")
	if id, _, _ := candidate.Load(repo.Root, "task-1"); id.InFlight == nil || id.InFlight.Invocation != "exec-other" {
		t.Fatalf("the other invocation's record was not preserved: %+v", id.InFlight)
	}
}

// W13 (RULING-363 §11). The pre-start error is NOT ErrQuarantined, so the
// type gate admits it to the durable check; the locked record is
// quarantined (a trusted fixture writes that recorded state, with no
// execution in flight, so only the quarantine check can refuse). The
// baseline is kept, the quarantine is not overwritten, the typed stop
// stands.
func TestADurableQuarantineKeepsTheBaselineAfterAPreStartFailure(t *testing.T) {
	e, repo, base, diff, _ := preStartRig(t)
	var before []byte
	injectPreStart(t, func(n int, root, taskID string, _ candidate.InFlight) (candidate.Identity, error, bool) {
		if n != 2 {
			return candidate.Identity{}, nil, false
		}
		id, _, err := candidate.Load(root, taskID)
		if err != nil {
			t.Fatal(err)
		}
		id.InFlight = nil
		id.Resolution = &candidate.Resolution{Disposition: candidate.Quarantined, Reason: "an earlier unresolved execution", DecidedAt: time.Now().UTC()}
		body, err := json.MarshalIndent(id, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(recordFile(root), body, 0o644); err != nil {
			t.Fatal(err)
		}
		before = body
		return candidate.Identity{}, errInjectedPreStart, true
	})
	_, _, err := e.validate(context.Background(), "task-1", base, broker.New(e.Config.Permissions), repo, diff, nil)
	assertBaselineKept(t, repo, err, "the durable record is quarantined")
	if after, _ := os.ReadFile(recordFile(repo.Root)); string(after) != string(before) {
		t.Fatal("the quarantine was overwritten")
	}
}

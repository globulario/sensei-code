package workflow

import (
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// A run must not be pausable by an observer.
//
// Engine.emit is on every path in the workflow — it is how status, agent
// output, authority questions and terminal verdicts leave the engine. While
// Publish blocked on a full subscriber channel, any watcher that stopped
// reading stopped the run itself, holding the bus read lock while it did.
// Nothing crashed and nothing was reported; the run simply stopped, with the
// cause in another package.
func TestAStalledWatcherCannotStopTheWorkflow(t *testing.T) {
	bus := event.NewBus()
	stalled, cancel := bus.Subscribe(1)
	defer cancel()
	_ = stalled // deliberately never read

	store, err := session.New(t.TempDir(), "sess-1")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	// FIXTURE MIGRATION (70B2a1): the task's records stand on its root.
	if err := seedAppend(t, store, event.New("sess-1", "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil)); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Bus: bus, Store: store, SessionID: "sess-1"}

	done := make(chan struct{})
	go func() {
		for i := 0; i < 500; i++ {
			e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-1", "task-1", event.SourceSystem, event.Status, "working", nil))
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a watcher that stopped reading stopped the workflow")
	}

	// The other half of the bargain: the watcher missed events and the RUN did
	// not. The store is appended to before anything is published, so a skipped
	// delivery is a gap in what somebody saw and never a gap in the record.
	if bus.Dropped() == 0 {
		t.Fatal("this test proved nothing: the stalled subscriber never actually filled up")
	}
	recorded, err := store.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(recorded) != 1+500 {
		t.Fatalf("the record holds %d of the root and 500 events; a dropped delivery lost part of the run", len(recorded))
	}
}

// drainFor collects what seen delivers until it is quiet.
func drainFor(seen <-chan event.Event) []event.Event {
	var out []event.Event
	for {
		select {
		case ev := <-seen:
			out = append(out, ev)
		case <-time.After(50 * time.Millisecond):
			return out
		}
	}
}

// wraps reports whether target is in err's chain.
func wraps(err, target error) bool {
	if err == nil {
		return false
	}
	if err == target {
		return true
	}
	switch u := err.(type) {
	case interface{ Unwrap() error }:
		return wraps(u.Unwrap(), target)
	case interface{ Unwrap() []error }:
		for _, inner := range u.Unwrap() {
			if wraps(inner, target) {
				return true
			}
		}
	}
	return false
}

// liveInvocation admits one invocation of taskID into e, as Run and Resume
// do, and ends it when the test does.
func liveInvocation(t *testing.T, e *Engine, taskID string) *invocation {
	t.Helper()
	inv, err := e.admitInvocation(t.Context(), taskID)
	if err != nil {
		t.Fatalf("the invocation was not admitted: %v", err)
	}
	leaseFixtureInvocation(e, inv)
	t.Cleanup(func() { e.endInvocation(inv) })
	return inv
}

// invocationUnder is the context of a fresh invocation of taskID in e bound
// to caller -- whose cancellation or deadline is then the invocation's own --
// replacing whatever invocation a fixture left live for the task, and ended
// when the test ends.
func invocationUnder(t *testing.T, e *Engine, caller waitContext, taskID string) waitContext {
	t.Helper()
	e.mu.Lock()
	live := e.admitted[taskID]
	e.mu.Unlock()
	if live != nil {
		e.endInvocation(live)
	}
	inv, err := e.admitInvocation(caller, taskID)
	if err != nil {
		t.Fatalf("the invocation was not admitted: %v", err)
	}
	leaseFixtureInvocation(e, inv)
	t.Cleanup(func() { e.endInvocation(inv) })
	return inv.ctx
}

// waitContext is the method set of a context.Context.
type waitContext = interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}

// errNoStoreSentinel is session.ErrNoStore, for witnesses in files that do
// not import package session.
var errNoStoreSentinel = session.ErrNoStore

// leaseAndBind binds inv's session to taskID's lineage exactly as ResumeTask
// does: under the task's invocation lease, taken for inv first.
func leaseAndBind(e *Engine, inv *invocation, taskID string) *RecordAppendFailure {
	// The invocation leases itself, exactly as ResumeTask does: whatever
	// lease its fixture gave it is given back first.
	e.mu.Lock()
	held := inv.lease
	inv.lease = nil
	e.mu.Unlock()
	held.Release()
	if f := e.leaseInvocation(inv.ctx, inv, e.Store, session.SessionLineageBound); f != nil {
		return f
	}
	_, f := e.bindLineage(inv, taskID)
	return f
}

// isHalted reports whether inv's halt has closed.
func isHalted(inv *invocation) bool {
	select {
	case <-inv.halted:
		return true
	default:
		return false
	}
}

// A1-W10 APPEND ERROR (objective 70B2a1): a durable engine event the session
// record refuses is never published as though it occurred, and nothing is
// published in its place. The refusal is forced through the task's session
// lineage: the task's root is session A's, and the engine acts as session C,
// which no transition made current.
func TestB2a1W10AppendError(t *testing.T) {
	store, err := session.New(t.TempDir(), "sess-A")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	if err := seedAppend(t, store, event.New("sess-A", "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil)); err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	seen, cancel := bus.Subscribe(64)
	defer cancel()
	e := &Engine{Bus: bus, Store: store, SessionID: "sess-C"}
	inv := liveInvocation(t, e, "task-1")

	// A caller that acts on the failure receives it, typed, and nothing is
	// published.
	durable := e.emitDurableIn(fixtureCtx(e, "task-1"), event.New("sess-C", "task-1", event.SourceSystem, event.PlanAttemptStarted, "durable", nil))
	unwrapped, ok := durable.(interface{ Unwrap() error })
	if !ok {
		t.Fatalf("the refused durable append was not reported: %v", durable)
	}
	if f, ok := unwrapped.Unwrap().(*RecordAppendFailure); !ok || f.Kind != event.PlanAttemptStarted || f.TaskID != "task-1" ||
		!wraps(f, session.ErrSessionAppendRefused) {
		t.Fatalf("the failure is not the typed append failure of that record: %v", durable)
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("a refused durable append was published: %+v", got)
	}
	// It halted the invocation that made it through the same owner as emit:
	// the returned failure is the fenced one, so nothing dependent can follow.
	if !isHalted(inv) {
		t.Fatal("a refused durable append did not halt its invocation")
	}
	if f := e.invocationFailure(inv); f == nil || f != unwrapped.Unwrap() {
		t.Fatalf("the halting failure is not the one the caller received: %v", f)
	}

	// A fire-and-forget emission halts its invocation: the event is not
	// published, no event is published in its place, the typed failure is
	// the invocation's account, and nothing dependent on it is recorded or
	// published afterwards.
	// One invocation of the task at a time holds its lease: the first ends.
	e.endInvocation(inv)
	e = &Engine{Bus: bus, Store: store, SessionID: "sess-C"}
	inv = liveInvocation(t, e, "task-1")
	e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-C", "task-1", event.SourceSystem, event.WorkflowNotConverged, "the run ended", nil))
	if !isHalted(inv) {
		t.Fatal("a failed governed emission did not halt its invocation")
	}
	f := e.invocationFailure(inv)
	if f == nil || f.Kind != event.WorkflowNotConverged || !wraps(f, session.ErrSessionAppendRefused) {
		t.Fatalf("the workflow owner does not report the typed append failure: %v", f)
	}
	e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-C", "task-1", event.SourceSystem, event.Status, "dependent", nil))
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("a halted task published %+v; neither the failed event nor any replacement may be", got)
	}
	if again := e.invocationFailure(inv); again != f {
		t.Fatalf("a dependent emission replaced the fenced failure: %v", again)
	}
	recorded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 {
		t.Fatalf("the record holds %d events; only the root may be there", len(recorded))
	}

	// Control: the task's own current session records and then publishes,
	// once the refused invocation has ended and given back the task's lease.
	e.endInvocation(inv)
	owner := &Engine{Bus: bus, Store: store, SessionID: "sess-A"}
	ownerInv := liveInvocation(t, owner, "task-1")
	owner.emitIn(fixtureCtx(owner, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.Status, "lawful", nil))
	if got := drainFor(seen); len(got) != 1 || got[0].Kind != event.Status {
		t.Fatalf("the current session's recorded event was not published: %+v", got)
	}
	if owner.invocationFailure(ownerInv) != nil {
		t.Fatal("a lawful append halted the invocation")
	}
}

// cancelledCaller is a caller context that has already ended.
type cancelledCaller struct{}

type callerStopped struct{}

func (callerStopped) Error() string { return "the caller stopped" }

func (cancelledCaller) Deadline() (time.Time, bool) { return time.Time{}, false }
func (cancelledCaller) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (cancelledCaller) Err() error    { return callerStopped{} }
func (cancelledCaller) Value(any) any { return nil }

// A1-W3 ROOTLESS, AT THE WORKFLOW OWNER (objective 70B2a1, RULING-189 required
// control): a resumed task whose lineage cannot be bound -- no Store, a Store
// holding no root of the task, a root proving no holder, or a binding the Store
// rejects -- is halted with the typed pre-binding refusal, which the workflow
// owner reports and whose halt a caller can wait on without hanging. Nothing is
// appended under the fresh session, nothing is published in its place, and the
// task's history is byte for byte what it was -- neither the caller's
// projection of the task nor the engine's memory stands in for a lineage.
// CONTROL: the same resume over the task's rooted record binds the fresh
// session and records under it.
func TestB2a1W3RootlessResumeHalts(t *testing.T) {
	task := session.Interrupted{TaskID: "task-rootless", Task: "the objective"}
	resumeUnder := func(t *testing.T, caller interface {
		Deadline() (time.Time, bool)
		Done() <-chan struct{}
		Err() error
		Value(any) any
	}, store *session.Store, fresh string, want error) {
		t.Helper()
		var before []event.Event
		if store != nil {
			var err error
			if before, err = store.Load(); err != nil {
				t.Fatal(err)
			}
		}
		bus := event.NewBus()
		seen, cancel := bus.Subscribe(64)
		defer cancel()
		e := &Engine{Bus: bus, Store: store, SessionID: fresh, pending: map[string]chan string{}}
		attempt := e.ResumeTask(caller, task)
		select {
		case <-attempt.Bound():
		case <-time.After(10 * time.Second):
			t.Fatal("the refused resume left its caller waiting")
		}
		select {
		case <-attempt.Halted():
		case <-time.After(10 * time.Second):
			t.Fatal("the refused resume did not halt its invocation")
		}
		f := attempt.Binding().Refusal
		if f == nil || !wraps(f, want) || f.Kind != session.SessionLineageBound || f.SessionID != fresh ||
			attempt.Binding().CurrentSessionID != "" {
			t.Fatalf("the resume was not refused before binding with %v: %+v", want, attempt.Binding())
		}
		if owner := attempt.Failure(); owner != f {
			t.Fatalf("the invocation reports %v, not the attempt's refusal %v", owner, f)
		}
		check := func() {
			t.Helper()
			if got := drainFor(seen); len(got) != 0 {
				t.Fatalf("a resume with no lineage published %+v", got)
			}
			if store == nil {
				return
			}
			after, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			// Nothing appended under the refused session, nothing rewritten.
			if len(after) != len(before) {
				t.Fatalf("a refused resume changed the task's history: %d records became %d", len(before), len(after))
			}
			for i := range before {
				if after[i].ID != before[i].ID || after[i].SessionID != before[i].SessionID {
					t.Fatalf("record %d changed under a refused resume", i)
				}
			}
		}
		check()
		// The string entry point returns only once the outcome is settled,
		// and a refused attempt resumed no task.
		<-attempt.Ended()
		if got := e.Resume(caller, task); got != "" {
			t.Fatalf("Resume reported %q resumed although its attempt was refused", got)
		}
		check()
	}
	resume := func(t *testing.T, store *session.Store, fresh string, want error) {
		t.Helper()
		resumeUnder(t, t.Context(), store, fresh, want)
	}
	rooted := func(t *testing.T, root event.Event, more ...event.Event) *session.Store {
		t.Helper()
		store, err := session.New(t.TempDir(), "sess-A")
		if err != nil {
			t.Fatal(err)
		}
		for _, ev := range append([]event.Event{root}, more...) {
			if err := seedAppend(t, store, ev); err != nil {
				t.Fatal(err)
			}
		}
		return store
	}
	t.Run("no store", func(t *testing.T) { resume(t, nil, "sess-B", session.ErrNoStore) })
	t.Run("rootless store", func(t *testing.T) {
		// Another task's root roots nothing of this one, and the Store takes
		// no record of a task it holds no root of.
		store := rooted(t, event.New("sess-A", "another-task", event.SourceSystem, event.TaskCreated, "another", nil))
		resume(t, store, "sess-B", session.ErrNoTaskRoot)
	})
	t.Run("root proving no holder", func(t *testing.T) {
		// Only a legacy writer can have left a root naming no session: the
		// Store refuses to write one.
		dir := t.TempDir()
		writeLegacyRecord(t, dir, "sess-A", event.New("", task.TaskID, event.SourceSystem, event.TaskCreated, task.Task, nil))
		store, err := session.New(dir, "sess-A")
		if err != nil {
			t.Fatal(err)
		}
		resume(t, store, "sess-B", session.ErrSessionlessRoot)
	})
	t.Run("rejected binding: a superseded session", func(t *testing.T) {
		store := rooted(t, event.New("sess-A", task.TaskID, event.SourceSystem, event.TaskCreated, task.Task, nil))
		lineage, err := store.TaskSessionLineage(task.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		b, err := lineage.BindingFor("sess-B")
		if err != nil {
			t.Fatal(err)
		}
		lease, err := store.AcquireTaskInvocation(t.Context(), task.TaskID)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.BindSessionLineage(t.Context(), lease, b)
		lease.Release()
		if err != nil {
			t.Fatal(err)
		}
		// A is in the lineage and is no longer its tip: it cannot become
		// current again.
		resume(t, store, "sess-A", session.ErrSessionLineage)
	})
	t.Run("rejected binding: over the size bound", func(t *testing.T) {
		store := rooted(t, event.New("sess-A", task.TaskID, event.SourceSystem, event.TaskCreated, task.Task, nil))
		huge := make([]byte, 17<<20)
		for i := range huge {
			huge[i] = 'b'
		}
		resume(t, store, string(huge), session.ErrSessionEventTooLarge)
	})
	t.Run("rejected binding: the caller already cancelled", func(t *testing.T) {
		// The record lock is free; a cancelled caller still takes none.
		store := rooted(t, event.New("sess-A", task.TaskID, event.SourceSystem, event.TaskCreated, task.Task, nil))
		resumeUnder(t, cancelledCaller{}, store, "sess-B", session.ErrRecordLockCanceled)
	})
	t.Run("control: rooted store binds", func(t *testing.T) {
		store := rooted(t, event.New("sess-A", task.TaskID, event.SourceSystem, event.TaskCreated, task.Task, nil))
		e := &Engine{Store: store, SessionID: "sess-B"}
		inv := liveInvocation(t, e, task.TaskID)
		if f := leaseAndBind(e, inv, task.TaskID); f != nil {
			t.Fatalf("a rooted resume was not bound: %v", f)
		}
		e.emitIn(fixtureCtx(e, task.TaskID), event.New("sess-B", task.TaskID, event.SourceSystem, event.Status, "continued", nil))
		if f := e.invocationFailure(inv); f != nil {
			t.Fatalf("the bound session could not record: %v", f)
		}
		if tip, err := store.TaskSessionLineage(task.TaskID); err != nil || tip.Tip() != "sess-B" || tip.HolderSessionID != "sess-A" {
			t.Fatalf("the fresh session is not the task's current session: %+v %v", tip, err)
		}
	})
}

// A1-W10 APPEND ERROR, NOT ON SESSION AUTHORITY (70B2a1 review f1): EVERY
// failure of the session record to take a governed durable event halts its
// task through the one owner, not only a lineage refusal. Here the writer is
// the task's own current session and the event is over the size bound the
// record reads back. The caller receives the original typed failure, the task
// is halted with it, and no substitute and no later terminal, receipt, status
// or refusal of the task is recorded or published.
func TestB2a1W10AnOversizedDurableEventHaltsItsTask(t *testing.T) {
	store, err := session.New(t.TempDir(), "sess-A")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	if err := seedAppend(t, store, event.New("sess-A", "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil)); err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	seen, cancel := bus.Subscribe(64)
	defer cancel()
	e := &Engine{Bus: bus, Store: store, SessionID: "sess-A"}
	inv := liveInvocation(t, e, "task-1")

	huge := make([]byte, 17<<20)
	for i := range huge {
		huge[i] = 'x'
	}
	err = e.emitDurableIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.PlanProposed, string(huge), nil))
	f := e.invocationFailure(inv)
	if err == nil || f == nil || f.Kind != event.PlanProposed || !wraps(err, f) || !wraps(f, session.ErrSessionEventTooLarge) {
		t.Fatalf("an oversized durable event is not the task's typed append failure: returned %v, latched %v", err, f)
	}
	if wraps(f, session.ErrSessionAppendRefused) {
		t.Fatalf("premise: the failure is a session-authority refusal, not a write failure: %v", f)
	}
	if !isHalted(inv) {
		t.Fatal("a durable event the record could not take did not halt its invocation")
	}

	// The caller's recovery: every dependent record is suppressed, durable or
	// not, and the original failure stays the task's outcome.
	e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.WorkflowFailed, "the plan could not be recorded", nil))
	e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.RunReceipt, "receipt", nil))
	e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.Status, "status", nil))
	if err := e.emitDurableIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.PlanAttemptRefused, "refusal", nil)); !wraps(err, f) {
		t.Fatalf("a halted task recorded a dependent durable refusal, or reported another failure: %v", err)
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("after the failed durable append the task published %+v", got)
	}
	if again := e.invocationFailure(inv); again != f {
		t.Fatalf("a dependent emission replaced the original failure: %v", again)
	}
	recorded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(recorded) != 1 {
		t.Fatalf("the record holds %d events; only the root may be there", len(recorded))
	}
}

// THE HALT ENDS THE TASK'S ONE LIVE INVOCATION, AND ONLY IT (70B2a1 review
// r5 c2 f3 and c3 f2; RULING-193 B, W6 and W7): a task admits one live
// invocation at a time. A second admission while it runs is refused, typed,
// and neither cancels nor touches the live one. An append failure cancels the
// live invocation and is its own: once it has ended, what it left running
// records nothing more, while a later invocation is admitted with no failure
// of its own and the earlier invocation still reports exactly its own.
func TestB2a1R193W7InvocationsAreIsolatedAndOwnTheirFailure(t *testing.T) {
	store, err := session.New(t.TempDir(), "sess-A")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	if err := seedAppend(t, store, event.New("sess-A", "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil)); err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	seen, cancel := bus.Subscribe(64)
	defer cancel()
	e := &Engine{Bus: bus, Store: store, SessionID: "sess-C"}

	live, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	if other, err := e.admitInvocation(t.Context(), "task-1"); !wraps(err, ErrTaskInvocationLive) || other != nil {
		t.Fatalf("a second invocation of a live task was admitted: %v", err)
	}
	if live.ctx.Err() != nil || e.invocationFailure(live) != nil {
		t.Fatal("refusing the second invocation touched the live one")
	}

	// sess-C is not the task's current session: the record refuses its event.
	e.emitIn(live.ctx, event.New("sess-C", "task-1", event.SourceSystem, event.Status, "refused", nil))
	first := e.invocationFailure(live)
	if first == nil || !isHalted(live) {
		t.Fatal("premise: the refused append did not halt the invocation")
	}
	if live.ctx.Err() == nil {
		t.Fatal("the halt left the invocation running")
	}
	// Still live until it returns: nothing else is admitted meanwhile.
	if _, err := e.admitInvocation(t.Context(), "task-1"); !wraps(err, ErrTaskInvocationLive) {
		t.Fatalf("an invocation was admitted while the halted one was still running: %v", err)
	}
	e.endInvocation(live)

	// What the halted invocation left running records nothing more -- even
	// an event the record itself would take from the task's current session.
	e.emitIn(live.ctx, event.New("sess-C", "task-1", event.SourceSystem, event.Status, "a straggler", nil))
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("the halted invocation, or what it left running, published %+v", got)
	}

	// W7: a later invocation is fresh, and the earlier one keeps its own.
	next, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil || next == live || e.invocationFailure(next) != nil || isHalted(next) {
		t.Fatalf("a later invocation inherited the earlier one's state: %v", err)
	}
	e.emitIn(next.ctx, event.New("sess-C", "task-1", event.SourceSystem, event.WorkflowFailed, "refused again", nil))
	second := e.invocationFailure(next)
	if second == nil || second == first || second.Kind != event.WorkflowFailed {
		t.Fatalf("the later invocation does not own its own failure: %v", second)
	}
	if again := e.invocationFailure(live); again != first {
		t.Fatalf("the earlier invocation now reports %v, not its own failure %v", again, first)
	}
	e.endInvocation(next)
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("a halted invocation published %+v", got)
	}
}

// rootedTaskEngine is an engine acting as the holder session of task-1 over
// a Store that roots it, and a subscription to its bus.
func rootedTaskEngine(t *testing.T, taskIDs ...string) (*Engine, *session.Store, <-chan event.Event) {
	t.Helper()
	store, err := session.New(t.TempDir(), "sess-A")
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	for _, taskID := range taskIDs {
		if err := seedAppend(t, store, event.New("sess-A", taskID, event.SourceSystem, event.TaskCreated, "the objective", nil)); err != nil {
			t.Fatal(err)
		}
	}
	bus := event.NewBus()
	seen, cancel := bus.Subscribe(64)
	t.Cleanup(cancel)
	return &Engine{Bus: bus, Store: store, SessionID: "sess-A"}, store, seen
}

func recordLength(t *testing.T, store *session.Store) int {
	t.Helper()
	recorded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return len(recorded)
}

// RULING-198 (70B2a1): A NIL PRODUCER CANNOT BORROW B'S AUTHORITY, AND
// STALE A CANNOT EMIT, APPEND, PUBLISH OR FENCE B -- not even through the
// terminal and receipt path. While invocation B of the task is live, work
// that names no invocation -- a nil-producer record, and emitIn,
// emitDurableIn, emitterFor and emitRunTerminal under a context of no
// invocation -- is refused as unbound, typed, and never resolved to B; and
// ended A's own context ends nothing through B. Nothing is appended, nothing
// is published, and B is neither halted nor cancelled. CONTROL: the same
// terminal under B's own context is recorded and published.
//
// B's refused material is armed throughout (f1, RULING-198): the nil and
// stale endings neither settle nor clear it, nor write the candidate's
// disposition, and B's own ending resolves it exactly once.
func TestB2a1R198NilProducerCannotBorrowBsAuthority(t *testing.T) {
	e, store, seen := rootedTaskEngine(t, "task-1")
	e.Repo.Root = t.TempDir()
	status := func(summary string) event.Event {
		return event.New("sess-A", "task-1", event.SourceSystem, event.Status, summary, nil)
	}
	a := liveInvocation(t, e, "task-1")
	e.endInvocation(a)
	b := liveInvocation(t, e, "task-1")
	identity := candidateIdentityFor("base-of-b")
	identity.TaskID = "task-1"
	e.armRefusedMaterial("task-1", refusedScopeMaterial{refusalID: "refusal-of-b", identity: identity})
	pending := func() *refusedScopeMaterial {
		e.mu.Lock()
		defer e.mu.Unlock()
		return e.planAttemptsOf("task-1").refusedMaterial
	}
	armed := pending()
	length := recordLength(t, store)

	if err := e.record(nil, e.Store, status("a nil producer"), false); !wraps(err, errInvocationUnbound) {
		t.Fatalf("a nil producer was not refused as unbound while B is live: %v", err)
	}
	unbound := t.Context()
	if invocationOf(unbound) != nil {
		t.Fatal("premise: the test's context names an invocation")
	}
	e.emitIn(unbound, status("emitIn under no invocation"))
	if err := e.emitDurableIn(unbound, status("emitDurableIn under no invocation")); !wraps(err, errInvocationUnbound) {
		t.Fatalf("a durable emission under no invocation was not refused as unbound: %v", err)
	}
	e.emitterFor(unbound)(status("a provider callback bound to no invocation"))
	e.emitRunTerminal(unbound, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED", "NONE", "an unbound ending", nil)
	e.emitRunTerminal(a.ctx, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED", "NONE", "A's stale ending", nil)
	if err := e.settleRefusedMaterial(unbound, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED"); !wraps(err, errInvocationUnbound) {
		t.Fatalf("an unbound settlement of B's refused material was not refused as unbound: %v", err)
	}
	if err := e.settleRefusedMaterial(a.ctx, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED"); !wraps(err, errInvocationEnded) {
		t.Fatalf("stale A's settlement of B's refused material was not refused as ended: %v", err)
	}
	if got := pending(); got != armed || got.identity.Resolution != nil {
		t.Fatalf("unbound or stale work consumed or changed B's refused material: %+v", got)
	}
	if got := e.governedBase("task-1"); got != "" {
		t.Fatalf("unbound or stale work wrote the candidate's disposition (base %q on disk)", got)
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("unbound or stale work published through B: %+v", got)
	}
	if got := recordLength(t, store); got != length {
		t.Fatalf("unbound or stale work was recorded: %d records became %d", length, got)
	}
	if e.invocationFailure(b) != nil || isHalted(b) || b.ctx.Err() != nil {
		t.Fatal("unbound or stale work fenced or cancelled B")
	}

	// CONTROL: B's own ending -- the disposition of its refused material,
	// receipt and terminal -- is recorded and published on B's behalf, and
	// the material is resolved exactly once.
	e.emitRunTerminal(b.ctx, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED", "NONE", "B's own ending", nil)
	got := drainFor(seen)
	if len(got) != 3 || got[0].Kind != event.CandidateResolved || got[1].Kind != event.RunReceipt ||
		got[2].Kind != event.WorkflowFailed || got[2].Summary != "B's own ending" {
		t.Fatalf("B's own ending was not recorded and published: %+v", got)
	}
	if got := recordLength(t, store); got != length+3 {
		t.Fatalf("B's own ending was not recorded: %d records became %d", length, got)
	}
	if got := pending(); got != nil {
		t.Fatalf("B's refused material is still pending after B settled it: %+v", got)
	}
	if got := e.governedBase("task-1"); got != "base-of-b" {
		t.Fatalf("B's disposition of its refused material was not written (base %q on disk)", got)
	}
	if err := e.settleRefusedMaterial(b.ctx, "task-1", event.WorkflowFailed, event.SourceSystem, "FAILED"); err != nil {
		t.Fatalf("a second settlement by B failed: %v", err)
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("B resolved its refused material more than once: %+v", got)
	}
	if got := recordLength(t, store); got != length+3 {
		t.Fatalf("B recorded its refused material's disposition more than once: %d records became %d", length+3, got)
	}
}

// RULING-195 (70B2a1 r6 review f2): STALE INVOCATION A CANNOT EMIT THROUGH B.
// Every emission is made on behalf of the exact invocation that produced it:
// the work invocation A hands a provider turn is bound to A (emitterFor). Once
// A has ended, what it left running records and publishes nothing -- before
// a later invocation B of the task is admitted and after -- and it can
// neither append, publish nor fence through B: B is not halted, not
// cancelled, and records its own events. An invocation of another task
// cannot record this task's events at all, and every invocation carries its
// own immutable identity. CONTROL: A's own emission while it is live is
// recorded and published.
func TestB2a1R195StaleInvocationACannotEmitThroughB(t *testing.T) {
	e, store, seen := rootedTaskEngine(t, "task-1", "task-2")
	status := func(summary string) event.Event {
		return event.New("sess-A", "task-1", event.SourceSystem, event.Status, summary, nil)
	}
	a, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	leaseFixtureInvocation(e, a)
	lateA := e.emitterFor(a.ctx)
	lateA(status("A while it is live"))
	if got := drainFor(seen); len(got) != 1 {
		t.Fatalf("premise: A's own emission while live was not published: %+v", got)
	}
	length := recordLength(t, store)
	e.endInvocation(a)

	// After A ended, before B: neither A's bound work nor unbound work
	// records anything of the task.
	if err := e.record(a, e.Store, status("A after it ended"), false); !wraps(err, errInvocationEnded) {
		t.Fatalf("A's emission after it ended was not refused as ended: %v", err)
	}
	lateA(status("A's provider after A ended"))
	if err := e.record(nil, e.Store, status("unbound work after A ended"), false); !wraps(err, errInvocationUnbound) {
		t.Fatalf("unbound work was not refused as unbound: %v", err)
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("an ended invocation's work published %+v", got)
	}
	if e.invocationFailure(a) != nil {
		t.Fatal("a refused stale emission halted the invocation that had ended cleanly")
	}

	b, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	leaseFixtureInvocation(e, b)
	defer e.endInvocation(b)
	const prefix = "invocation-"
	if a.id == b.id || len(a.id) <= len(prefix) || a.id[:len(prefix)] != prefix {
		t.Fatalf("the invocations do not carry distinct immutable identities: %q %q", a.id, b.id)
	}
	// After B is admitted: A's late work is refused as A's, never resolved
	// to B, whatever it emits -- an ending included.
	if err := e.record(a, e.Store, status("A through B"), false); !wraps(err, errInvocationEnded) {
		t.Fatalf("A's emission after B was admitted was not refused as A's: %v", err)
	}
	lateA(event.New("sess-A", "task-1", event.SourceSystem, event.WorkflowFailed, "A's stale ending", nil))
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("A's late work published through B: %+v", got)
	}
	if got := recordLength(t, store); got != length {
		t.Fatalf("A's late work was recorded: %d records became %d", length, got)
	}
	if e.invocationFailure(b) != nil || isHalted(b) || b.ctx.Err() != nil {
		t.Fatal("A's late work fenced or cancelled B")
	}

	// An invocation of another task records nothing of this one.
	c, err := e.admitInvocation(t.Context(), "task-2")
	if err != nil {
		t.Fatal(err)
	}
	leaseFixtureInvocation(e, c)
	defer e.endInvocation(c)
	if err := e.record(c, e.Store, status("task-2's invocation writing task-1"), false); !wraps(err, errInvocationForeign) {
		t.Fatalf("an invocation of another task recorded this task's event: %v", err)
	}
	if e.invocationFailure(b) != nil || e.invocationFailure(c) != nil {
		t.Fatal("a refused foreign emission halted an invocation")
	}

	// CONTROL: B records and publishes its own.
	e.emitterFor(b.ctx)(status("B's own"))
	if got := drainFor(seen); len(got) != 1 || got[0].Summary != "B's own" {
		t.Fatalf("B's own emission was not published: %+v", got)
	}
}

// RULING-195 (70B2a1 r6 review f2): A WITHDRAWN INVOCATION RECORDS ONLY THE
// ACCOUNT OF ITS ENDING. Once A's caller has cancelled it, nonterminal work
// it still emits is refused -- not recorded, not published -- and halts
// nothing, since the withdrawal itself is the invocation's outcome. The
// account of its ending is recorded: its run ending, and whatever the terminal
// classifier records once it explicitly opens that account for A
// (openEndingAccount) -- which authorizes A alone.
func TestB2a1R195AWithdrawnInvocationRecordsOnlyItsEndingAccount(t *testing.T) {
	e, store, seen := rootedTaskEngine(t, "task-1", "task-2")
	a, err := e.admitInvocation(cancelledCaller{}, "task-1")
	if err != nil {
		t.Fatal(err)
	}
	leaseFixtureInvocation(e, a)
	defer e.endInvocation(a)
	if a.ctx.Err() == nil {
		t.Fatal("premise: the invocation's caller has not withdrawn")
	}
	length := recordLength(t, store)
	for _, emitted := range []func(event.Event) error{
		func(ev event.Event) error { return e.record(a, e.Store, ev, false) },
		func(ev event.Event) error { return e.emitDurableIn(a.ctx, ev) },
	} {
		err := emitted(event.New("sess-A", "task-1", event.SourceSystem, event.PlanAttemptStarted, "stale nonterminal work", nil))
		if !wraps(err, errInvocationWithdrawn) {
			t.Fatalf("a withdrawn invocation's nonterminal work was not refused as withdrawn: %v", err)
		}
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("a withdrawn invocation's nonterminal work was published: %+v", got)
	}
	if got := recordLength(t, store); got != length {
		t.Fatal("a withdrawn invocation's nonterminal work was recorded")
	}
	if e.invocationFailure(a) != nil {
		t.Fatal("a refused withdrawn emission halted the invocation")
	}

	// Its ending is recorded and published.
	e.emitIn(fixtureCtx(e, "task-1"), event.New("sess-A", "task-1", event.SourceSystem, event.WorkflowStopped, "stopped by its caller", nil))
	if got := drainFor(seen); len(got) != 1 || got[0].Kind != event.WorkflowStopped {
		t.Fatalf("the withdrawn invocation's ending was not published: %+v", got)
	}
	// The ending capability is exactly the records an ending creates: A's
	// receipt is recorded, and a status it emits while ending is refused --
	// no flag widens the capability to another kind (f2, r7 review).
	receipt := event.New("sess-A", "task-1", event.SourceSystem, event.RunReceipt, "the ending's receipt",
		map[string]any{"receipt": map[string]string{"schema": "test"}, "completeness": "complete", "missing": []string{}})
	if err := e.record(a, e.Store, receipt, false); err != nil {
		t.Fatalf("the withdrawn invocation's receipt was refused: %v", err)
	}
	if err := e.record(a, e.Store, event.New("sess-A", "task-1", event.SourceSystem, event.Status, "not an ending record", nil), false); !wraps(err, errInvocationWithdrawn) {
		t.Fatalf("a withdrawn invocation recorded a status under its ending capability: %v", err)
	}
	if got := drainFor(seen); len(got) != 1 || got[0].Kind != event.RunReceipt {
		t.Fatalf("the ending account published %+v", got)
	}
}

// f2 (70B2a1 r7 review): A STALE INVOCATION'S CONTEXT-BOUND EMISSION IS
// REFUSED AS ITS OWN, NEVER RESOLVED TO THE TASK'S CURRENT INVOCATION. The
// engine's emissions name the invocation whose context they run under
// (emitIn, emitDurableIn). After A ended and B was admitted, A's emissions --
// ordinary and durable -- are refused as A's: nothing is recorded or
// published, and B is neither halted nor cancelled. B's own, through the same
// path, is the control.
func TestB2a1F2StaleContextBoundEmissionIsNotAttributedToTheCurrentInvocation(t *testing.T) {
	e, store, seen := rootedTaskEngine(t, "task-1")
	status := func(summary string) event.Event {
		return event.New("sess-A", "task-1", event.SourceSystem, event.Status, summary, nil)
	}
	a, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	leaseFixtureInvocation(e, a)
	e.emitIn(a.ctx, status("A while live"))
	if got := drainFor(seen); len(got) != 1 {
		t.Fatalf("premise: A's bound emission while live was not published: %+v", got)
	}
	e.endInvocation(a)
	b, err := e.admitInvocation(t.Context(), "task-1")
	if err != nil {
		t.Fatal(err)
	}
	leaseFixtureInvocation(e, b)
	defer e.endInvocation(b)
	length := recordLength(t, store)
	e.emitIn(a.ctx, status("A's stale work through B"))
	if err := e.emitDurableIn(a.ctx, status("A's stale durable work through B")); !wraps(err, errInvocationEnded) {
		t.Fatalf("A's stale durable emission was not refused as A's: %v", err)
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("A's stale emission was published through B: %+v", got)
	}
	if got := recordLength(t, store); got != length {
		t.Fatalf("A's stale emission was recorded: %d records became %d", length, got)
	}
	if e.invocationFailure(b) != nil || isHalted(b) || b.ctx.Err() != nil {
		t.Fatal("A's stale emission fenced or cancelled B")
	}
	// CONTROL: B's own bound emissions are recorded and published.
	e.emitIn(b.ctx, status("B's own"))
	if err := e.emitDurableIn(b.ctx, status("B's own durable")); err != nil {
		t.Fatalf("B's own durable emission was refused: %v", err)
	}
	if got := drainFor(seen); len(got) != 2 {
		t.Fatalf("B's own emissions were not published: %+v", got)
	}
}

// 70B2a1 cycle-2 review f2: EVERY TASK-BOUND EMISSION CARRIES ITS OWN
// INVOCATION, UNDER THAT INVOCATION'S IMMUTABLE SESSION. Work naming no
// invocation is refused as unbound even for a task this engine never admitted
// an invocation of -- there is no "nothing admitted yet" fallback -- and a
// live invocation cannot record an event labelled with another session, not
// even the engine's own current session after it changed. A record of no task
// handed to the task path is refused rather than published as a diagnostic;
// only the diagnostic path records one, and only a diagnostic of the closed
// registry. Nothing refused is appended or published. CONTROL: the
// invocation's own event under its own session is recorded and published, and
// so is a registered diagnostic.
func TestB2a1RF2EmissionNeedsItsInvocationsOwnSession(t *testing.T) {
	e, store, seen := rootedTaskEngine(t, "task-1")
	status := func(sessionID, summary string) event.Event {
		return event.New(sessionID, "task-1", event.SourceSystem, event.Status, summary, nil)
	}
	length := recordLength(t, store)
	// No invocation of task-1 was ever admitted here: unbound work is still
	// unbound.
	if err := e.record(nil, e.Store, status("sess-A", "before any admission"), false); !wraps(err, errInvocationUnbound) {
		t.Fatalf("unbound work recorded before any invocation was admitted: %v", err)
	}
	e.emitIn(t.Context(), status("sess-A", "emitIn under no invocation"))

	b := liveInvocation(t, e, "task-1")
	if b.sessionID != "sess-A" {
		t.Fatalf("premise: the invocation was not created under the engine's session: %q", b.sessionID)
	}
	if err := e.record(b, e.Store, status("sess-Z", "another session's label"), false); !wraps(err, errInvocationSession) {
		t.Fatalf("an invocation recorded an event labelled with another session: %v", err)
	}
	// The engine's current session changes; the invocation's does not.
	e.SessionID = "sess-Z"
	e.emitIn(b.ctx, event.New(e.SessionID, "task-1", event.SourceSystem, event.Status, "under the engine's new session", nil))
	e.SessionID = "sess-A"
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("refused emissions were published: %+v", got)
	}
	if got := recordLength(t, store); got != length {
		t.Fatalf("refused emissions were recorded: %d records became %d", length, got)
	}
	// The diagnostic path takes only a diagnostic of the closed registry.
	for name, bad := range map[string]event.Event{
		"a governed kind": event.New("sess-A", "", event.SourceSystem, event.WorkflowCompleted, "done", nil),
		"an agent source": event.New("sess-A", "", event.SourceClaude, event.Status, "x", nil),
		"a payload":       event.New("sess-A", "", event.SourceSystem, event.Status, "x", map[string]string{"k": "v"}),
		"a task":          status("sess-A", "a task record"),
	} {
		if err := e.emitDiagnostic(bad); err == nil {
			t.Errorf("the diagnostic path took %s", name)
		}
	}
	if got := drainFor(seen); len(got) != 0 {
		t.Fatalf("refused diagnostics were published: %+v", got)
	}
	if got := recordLength(t, store); got != length {
		t.Fatalf("refused diagnostics were recorded: %d records became %d", length, got)
	}

	// CONTROL: b's own event, and a registered diagnostic.
	if err := e.record(b, e.Store, status("sess-A", "b's own"), false); err != nil {
		t.Fatal(err)
	}
	if err := e.emitDiagnostic(event.New("sess-A", "", event.SourceSystem, event.Status, "a diagnostic", nil)); err != nil {
		t.Fatal(err)
	}
	if got := drainFor(seen); len(got) != 2 {
		t.Fatalf("the controls were not published: %+v", got)
	}
	if got := recordLength(t, store); got != length+2 {
		t.Fatalf("the controls were not recorded: %d records became %d", length, got)
	}
	if isHalted(b) {
		t.Fatalf("b was halted: %v", e.invocationFailure(b))
	}
	// A record of no task is never a task emission: handed to the task path
	// it is refused, published nowhere, and fails its producer closed.
	taskless := event.New("sess-A", "", event.SourceSystem, event.Status, "taskless through the task path", nil)
	if err := e.record(b, e.Store, taskless, false); !wraps(err, errTasklessEmission) {
		t.Fatalf("a record of no task went through the task path: %v", err)
	}
	if got := drainFor(seen); len(got) != 0 || recordLength(t, store) != length+2 || !isHalted(b) {
		t.Fatalf("a taskless record through the task path was published or recorded, or left its producer running: %+v", got)
	}
}

// seedAppend writes a fixture's event to store as the Store's lawful writer
// of it would (70B2a1 cycle-3 review f1): a root through the dedicated root
// creation and any other record of the task under the task's live invocation
// lease, made operative by the Store for the session its lineage names
// current. When a fixture invocation of the task already holds the lease
// through store (fixtureLease), the record is written under that lease, as
// that invocation's own; otherwise the lease is taken for this one record and
// released. No fixture borrows a capability the Store did not issue.
func seedAppend(t *testing.T, store *session.Store, ev event.Event) error {
	t.Helper()
	if store == nil {
		return store.Append(t.Context(), nil, ev)
	}
	if ev.TaskID == "" {
		return store.AppendDiagnostic(t.Context(), ev)
	}
	if held := fixtureLease(store, ev.TaskID); held != nil {
		if err := seedUnder(t, store, held, ev); !wraps(err, session.ErrTaskLeaseRequired) {
			return err
		}
	}
	lease, err := store.AcquireTaskInvocation(t.Context(), ev.TaskID)
	if err != nil {
		return err
	}
	defer lease.Release()
	return seedUnder(t, store, lease, ev)
}

// seedUnder writes ev under lease: a root through CreateTaskRoot, any other
// record once the Store has made the lease operative for the lineage's tip.
func seedUnder(t *testing.T, store *session.Store, lease *session.TaskLease, ev event.Event) error {
	t.Helper()
	if ev.Kind == event.TaskCreated {
		return store.CreateTaskRoot(t.Context(), lease, ev)
	}
	if lineage, err := store.TaskSessionLineage(ev.TaskID); err == nil {
		if _, err := store.ContinueTaskSession(t.Context(), lease, lineage.Tip()); err != nil {
			return err
		}
	}
	return store.Append(t.Context(), lease, ev)
}

// fixtureLeaseKey names the lease a fixture invocation took through one Store
// for one task.
type fixtureLeaseKey struct {
	store  *session.Store
	taskID string
}

// fixtureLeases are the leases fixture invocations took (leaseFixtureInvocation),
// guarded by fixtureLeaseGuard. A released one authorizes nothing, so a stale
// entry only sends seedAppend to take a lease of its own.
var (
	fixtureLeaseGuard = make(chan struct{}, 1)
	fixtureLeases     = map[fixtureLeaseKey]*session.TaskLease{}
)

func noteFixtureLease(store *session.Store, taskID string, lease *session.TaskLease) {
	fixtureLeaseGuard <- struct{}{}
	fixtureLeases[fixtureLeaseKey{store, taskID}] = lease
	<-fixtureLeaseGuard
}

func fixtureLease(store *session.Store, taskID string) *session.TaskLease {
	fixtureLeaseGuard <- struct{}{}
	defer func() { <-fixtureLeaseGuard }()
	return fixtureLeases[fixtureLeaseKey{store, taskID}]
}

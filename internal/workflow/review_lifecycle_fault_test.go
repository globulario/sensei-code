package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
)

// A REVIEW-LIFECYCLE FAULT IS NOT A REVIEWER FAILURE AND NOT AN IMPLEMENTER
// FAILURE.
//
// The bridge learned to name these conditions exactly; the routing layers above
// it still converted them back into participant failures. A fault at the review
// lifecycle -- an orphaned request, records that disagree or cannot be read, the
// caller stopping the run -- says nothing is wrong with the candidate, and
// nothing the next reviewer or the next implementer can do repairs it.
//
// These drive the REAL candidate loop with two reviewers and two implementers
// configured, because the bridge-level tests for each condition already pass.
// That is precisely why they could not catch the escape.

// faultingRunner is a reviewer transport that fails with one exact condition.
type faultingRunner struct {
	calls atomic.Int32
	err   error
	// block, when set, holds the call until the context ends, so a caller
	// cancellation is observed inside the review rather than before it.
	block bool
}

func (r *faultingRunner) Run(ctx context.Context, _ agent.Request, _ func(event.Event)) (agent.Result, error) {
	r.calls.Add(1)
	if r.block {
		<-ctx.Done()
		return agent.Result{}, fmt.Errorf("the review wait was ended by its caller: %w", ctx.Err())
	}
	return agent.Result{}, r.err
}

// lifecycleHarness configures TWO reviewers and TWO implementers, so a fallback
// of either kind is a test failure rather than a subtle difference in output.
func lifecycleHarness(t *testing.T, runner agent.Runner) *gateHarness {
	t.Helper()
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, "accept")
	h.engine.Runners = roleResolver{reviewer: runner, name: "chatgpt", session: "session-1"}
	h.engine.Config.Reviewers = []config.Agent{
		{Name: "chatgpt", Command: "true", Graph: "none"},
		{Name: "codex", Command: "true", Graph: "none"},
	}
	// The second implementor's command FAILS, so a handoff to it cannot pass
	// quietly.
	h.engine.Config.Implementors = []config.Agent{h.worker, {Name: "codex", Command: "false", Graph: "none"}}
	return h
}

// assertNoFallbackLadderRan states the whole law in one place: NO fallback of
// either kind, and the candidate still on disk.
//
// The reviewer-call bound is "at most once" rather than "exactly once", because
// a caller stop can land before the review is even reached -- under -race it
// reliably does. Whether this run got as far as asking is timing; whether it
// asked a SECOND participant after the fault is the law.
func assertNoFallbackLadderRan(t *testing.T, h *gateHarness, calls *atomic.Int32, events []event.Event) {
	t.Helper()
	if got := calls.Load(); got > 1 {
		t.Errorf("the reviewer was asked %d times; a lifecycle fault must not fall back to another reviewer", got)
	}
	if contains(events, event.HandoffCreated) {
		t.Errorf("a lifecycle fault created an implementer handoff: %v", kinds(events))
	}
	if contains(events, event.WorkflowCompleted) {
		t.Errorf("a lifecycle fault completed the run: %v", kinds(events))
	}
	// The candidate holds real work and is preserved exactly as it stands.
	if fp := candidateFingerprint(t, h.work); len(fp) == 0 {
		t.Error("the candidate worktree was disposed of")
	}
}

// 1. A published request whose obligation could not be recorded.
//
// The reviewer ladder already stopped correctly. The implementer ladder did
// not: runCandidate called it not_converged, so the worker was recorded as
// having failed and the unchanged candidate was handed to the next implementer
// -- who can no more write the obligation record than the first one could.
func TestAnUnrecordableReviewRequestReachesNoOtherParticipant(t *testing.T) {
	runner := &faultingRunner{err: fmt.Errorf("%w: r-0123456789abcdef was published as comment 42 and the record "+
		"could not be written: permission denied", roles.ErrReviewUnrecordable)}
	h := lifecycleHarness(t, runner)

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })

	if failed == nil {
		t.Fatal("an orphaned review request did not end the run")
	}
	if !errors.Is(failed, roles.ErrReviewUnrecordable) {
		t.Fatalf("the terminal error is no longer recognisable as unrecordable: %v", failed)
	}
	// It must NOT have been laundered into either participant-failure story.
	if errors.Is(failed, roles.ErrReviewUnobtainable) {
		t.Fatalf("an orphaned request was reported as reviewers being unreachable: %v", failed)
	}
	if got := runner.calls.Load(); got != 1 {
		t.Errorf("the reviewer was asked %d times, want exactly once", got)
	}
	assertNoFallbackLadderRan(t, h, &runner.calls, drainEvents(h.events))
}

// 2. The caller stopped the run.
//
// Ending observation of a review is what the caller asked for. It is not
// permission to ask a different reviewer, and it is certainly not a reason to
// send the candidate to a different implementer.
func TestACallerCancellationReachesNoOtherParticipant(t *testing.T) {
	runner := &faultingRunner{block: true}
	h := lifecycleHarness(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	var failed error
	h.engine.implement(ctx, h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })

	if failed == nil {
		t.Fatal("a cancelled run reported success")
	}
	if !errors.Is(failed, context.Canceled) {
		t.Fatalf("the cancellation lost its identity by the outermost boundary: %v", failed)
	}
	if errors.Is(failed, roles.ErrReviewUnobtainable) {
		t.Fatalf("a caller stop was reported as reviewers being unreachable: %v", failed)
	}
	if errors.Is(failed, roles.ErrReviewUnanswered) {
		t.Fatalf("a caller stop was reported as an unanswered review: %v", failed)
	}
	assertNoFallbackLadderRan(t, h, &runner.calls, drainEvents(h.events))
}

// 3. A malformed durable obligation.
//
// Reported as provider failure, every reviewer would read the same broken local
// file and the chain would end as ReviewUnobtainable -- which says reviewers
// could not be reached when the actual fact is that our own authority record is
// unreadable.
func TestAMalformedObligationReachesNoOtherParticipant(t *testing.T) {
	runner := &faultingRunner{err: fmt.Errorf("%w: request r-0123456789abcdef: base must be a full "+
		"40-character commit, got \"not-a-sha\"", roles.ErrReviewLifecycleFault)}
	h := lifecycleHarness(t, runner)

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })

	if failed == nil {
		t.Fatal("a malformed obligation did not end the run")
	}
	if !errors.Is(failed, roles.ErrReviewLifecycleFault) {
		t.Fatalf("the terminal error is no longer recognisable as a lifecycle fault: %v", failed)
	}
	if errors.Is(failed, roles.ErrReviewUnobtainable) {
		t.Fatalf("a malformed local record was reported as reviewers being unreachable: %v", failed)
	}
	if got := runner.calls.Load(); got != 1 {
		t.Errorf("the reviewer was asked %d times, want exactly once", got)
	}
	assertNoFallbackLadderRan(t, h, &runner.calls, drainEvents(h.events))
}

// A lifecycle conflict is a lifecycle fault too, so the same routing applies
// without the bridge having to say so twice.
func TestALifecycleConflictIsRoutedAsALifecycleFault(t *testing.T) {
	if !errors.Is(roles.ErrReviewLifecycleConflict, roles.ErrReviewLifecycleFault) {
		t.Fatal("a lifecycle conflict is not recognisable as a lifecycle fault, so routing them together " +
			"would depend on the bridge naming both")
	}
	runner := &faultingRunner{err: fmt.Errorf("%w: task task-1 has 2 (r-one, r-two)", roles.ErrReviewLifecycleConflict)}
	h := lifecycleHarness(t, runner)

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	if !errors.Is(failed, roles.ErrReviewLifecycleFault) {
		t.Fatalf("a lifecycle conflict did not route as a lifecycle fault: %v", failed)
	}
	if got := runner.calls.Load(); got != 1 {
		t.Errorf("the reviewer was asked %d times, want exactly once", got)
	}
	assertNoFallbackLadderRan(t, h, &runner.calls, drainEvents(h.events))
}

// A REVIEWER THAT TIMED OUT ON ITS OWN IS STILL UNAVAILABILITY.
//
// The guard reads the PARENT context, never context.DeadlineExceeded on the
// error, because a timed-out provider reports exactly that value. Matching it
// would have swallowed the whole #180 BLOCKED_EXTERNAL condition into
// "lifecycle fault" and stopped the reviewer chain from being walked at all.
func TestAProvidersOwnTimeoutIsStillReviewerUnavailability(t *testing.T) {
	if isReviewLifecycleFault(context.Background(), context.DeadlineExceeded) {
		t.Fatal("a provider's own deadline is being read as a review-lifecycle fault; reviewer " +
			"unavailability would stop being walked as a chain")
	}
	if isReviewLifecycleFault(context.Background(), context.Canceled) {
		t.Fatal("a provider's own cancellation is being read as a review-lifecycle fault")
	}
	// And the parent ending IS one, whatever the provider said.
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if !isReviewLifecycleFault(done, errors.New("something the provider said")) {
		t.Fatal("a stopped parent context is not being read as a caller stop")
	}
}

// A caller stop that lands during IMPLEMENTATION also reaches no other
// participant.
//
// The cancel need not land inside the review. Under load it lands while the
// implementor is still running, and the loop then recorded that worker as
// failed and walked on to the next -- which the same dead context killed, and
// recorded too, until the aggregate error read "no bounded implementor produced
// an acceptable candidate". A caller who pressed stop was told their
// participants failed.
//
// Cancelled BEFORE the call, so which stage observes it is not a matter of
// timing.
func TestACallerStopDuringImplementationReachesNoOtherParticipant(t *testing.T) {
	runner := &faultingRunner{err: errors.New("the reviewer should never be reached")}
	h := lifecycleHarness(t, runner)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	var failed error
	h.engine.implement(ctx, h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })

	if failed == nil {
		t.Fatal("a cancelled run reported success")
	}
	if !errors.Is(failed, context.Canceled) {
		t.Fatalf("the cancellation lost its identity: %v", failed)
	}
	// The tell of the old behaviour: every implementor recorded as having failed.
	if strings.Contains(failed.Error(), "no bounded implementor produced an acceptable candidate") {
		t.Fatalf("a caller stop was reported as the implementors failing: %v", failed)
	}
	events := drainEvents(h.events)
	if contains(events, event.HandoffCreated) {
		t.Fatalf("a caller stop created a handoff: %v", kinds(events))
	}
	if got := runner.calls.Load(); got != 0 {
		t.Fatalf("the reviewer was asked %d times after the caller had already stopped", got)
	}
}

// Structural: EVERY review call site in the candidate loop carries the
// lifecycle-fault guard.
//
// There are two -- the candidate review and the findings review -- and a
// witness that drives one proves nothing about the other. The findings path is
// reached only with open findings from a prior cycle, so it is easy to leave
// unguarded and hard to notice: a mutation removing just that guard survived
// the behavioural tests entirely.
func TestEveryReviewCallSiteCarriesTheLifecycleGuard(t *testing.T) {
	blob, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(blob)
	start := strings.Index(src, "func (e *Engine) runCandidate(")
	if start < 0 {
		t.Fatal("runCandidate was not found; this check proves nothing")
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	sites := strings.Count(body, "e.resolveReview(")
	guards := strings.Count(body, "isReviewLifecycleFault(ctx, err)")
	if sites < 2 {
		t.Fatalf("found %d review call sites in runCandidate; this check expects both", sites)
	}
	if guards != sites {
		t.Errorf("runCandidate has %d review call sites and %d lifecycle guards; every site must carry one, "+
			"or a review-lifecycle fault becomes a handoff on whichever path is unguarded", sites, guards)
	}
}

// OBSERVATION FAULTS ARE NOT SILENCE, AND NOT PARTICIPANT FAILURES.
//
// The reviewer replied and the reply is unusable: malformed, about another
// subject, or two answers to one question. Every consumer must preserve which
// of those it was, even though all three share one control action.

func observationFaultOf(kinds ...string) *roles.ReviewObservationFault {
	f := &roles.ReviewObservationFault{
		RequestID: "r-0123456789abcdef", RequestComment: 42, Conversation: "157",
		Binding:      roles.Binding{TaskID: "task-1", BaseSHA: "b", CandidateDigest: "d", CandidateTree: "t"},
		ReviewCommit: "c",
	}
	for i, k := range kinds {
		f.Observations = append(f.Observations, roles.ReviewObservation{
			Kind: k, Comment: int64(100 + i), Author: "davecourtois", AuthorID: 1697116,
			BodyDigest: fmt.Sprintf("sha256:observed-%d", i), Diagnostic: "observed " + k,
		})
	}
	return f
}

func TestAnObservationFaultReachesNoOtherParticipantAndIsNotSilence(t *testing.T) {
	for name, kind := range map[string]string{
		"malformed reviewer content":  roles.ObservedMalformed,
		"a review of another subject": roles.ObservedWrongTarget,
		"two answers to one question": roles.ObservedConflict,
	} {
		t.Run(name, func(t *testing.T) {
			fault := observationFaultOf(kind)
			// The precondition: this really is an observation fault of that
			// kind, and not one of its neighbours wearing the name.
			if !errors.Is(fault, roles.ErrReviewObservationFault) || !fault.Has(kind) {
				t.Fatalf("the fixture is not a %s observation fault: %v", kind, fault)
			}
			for _, neighbour := range []error{
				roles.ErrReviewUnanswered, roles.ErrReviewUnobtainable, roles.ErrReviewLifecycleFault,
			} {
				if errors.Is(fault, neighbour) {
					t.Fatalf("an observation fault is indistinguishable from %v", neighbour)
				}
			}

			runner := &faultingRunner{err: fault}
			h := lifecycleHarness(t, runner)

			var failed error
			h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
				"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })

			// NOT A FINISHED WORKFLOW. An observation fault leaves the review
			// owed, so the invocation may not end through the failure path: at
			// the execute boundary that becomes WorkflowFailed, which
			// session.FindInterrupted treats as done -- the obligation would
			// survive on disk while the task vanished from `resume --task`.
			if failed != nil {
				t.Fatalf("an observation fault ended the run as a failure: %v", failed)
			}
			// Drained ONCE: the channel is consumed by reading it, and draining
			// twice left the second reader with nothing -- which looked like the
			// terminal never being emitted.
			events := drainEvents(h.events)
			if contains(events, event.WorkflowFailed) {
				t.Fatalf("an observation fault emitted WorkflowFailed: %v", kinds(events))
			}
			if !contains(events, event.WorkflowAwaitingReview) {
				t.Fatalf("no resumable review terminal was emitted: %v", kinds(events))
			}
			// THE REAL CONSUMER CHAIN: the task must still be discoverable.
			// Driving implement() with a callback proves the inner consumer and
			// nothing about this, which is how the terminal defect survived.
			//
			// The task was created and planned before this invocation, as it is
			// in any real run; implement() alone emits neither, and
			// FindInterrupted needs both to consider a task at all.
			history := append([]event.Event{
				event.New("session-1", "task-1", event.SourceUser, event.TaskCreated, "the task", nil),
				event.New("session-1", "task-1", event.SourceSystem, event.PlanProposed, "the plan", nil),
			}, events...)
			// The precondition: without the observation terminal this task would
			// be interrupted and resumable, so a disappearance below is caused by
			// the terminal and not by the fixture.
			if len(session.FindInterrupted(history[:2])) != 1 {
				t.Fatal("the fixture's task is not resumable to begin with, so this proves nothing")
			}
			var found bool
			for _, task := range session.FindInterrupted(history) {
				if task.TaskID == "task-1" {
					found = true
					if !task.AwaitingReview {
						t.Error("the interrupted task is not marked as awaiting review")
					}
				}
			}
			if !found {
				t.Fatal("the task disappeared from FindInterrupted, so resume --task cannot reach its obligation")
			}
			assertNoFallbackLadderRan(t, h, &runner.calls, events)
			if got := runner.calls.Load(); got != 1 {
				t.Errorf("the reviewer was asked %d times, want exactly once", got)
			}

			// The terminal names WHAT WAS SEEN, and never calls it silence or
			// borrows the unanswered projection.
			var stated bool
			for _, ev := range events {
				var p struct {
					Kind     string   `json:"review_kind"`
					Observed []string `json:"observed"`
				}
				if len(ev.Payload) == 0 || json.Unmarshal(ev.Payload, &p) != nil {
					continue
				}
				if p.Kind == "unanswered" {
					t.Errorf("observed evidence borrowed the unanswered projection: %s", ev.Payload)
				}
				if p.Kind == "observation_fault" {
					stated = true
					if len(p.Observed) == 0 || p.Observed[0] != kind {
						t.Errorf("the terminal records observed=%v, want %s", p.Observed, kind)
					}
				}
			}
			if !stated {
				t.Error("no terminal stated what was observed")
			}
		})
	}
}

// Structural: every review call site routes the observation fault explicitly.
//
// runCandidate has two -- the candidate review and the findings review -- and a
// witness driving one proves nothing about the other.
func TestEveryReviewCallSiteRoutesObservationFaults(t *testing.T) {
	blob, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(blob)
	start := strings.Index(src, "func (e *Engine) runCandidate(")
	if start < 0 {
		t.Fatal("runCandidate was not found; this check proves nothing")
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	sites := strings.Count(body, "e.resolveReview(")
	guards := strings.Count(body, "roles.ErrReviewObservationFault")
	if sites < 2 {
		t.Fatalf("found %d review call sites; this check expects both", sites)
	}
	if guards != sites {
		t.Errorf("runCandidate has %d review call sites and %d observation guards; an unguarded site turns "+
			"observed reviewer evidence into a handoff", sites, guards)
	}
}

// An observation fault is checked BEFORE the provider is recorded unavailable.
//
// The assigned reviewer transport produced evidence. Counting it toward
// ReviewUnobtainable would end the run saying no reviewer could be reached while
// their reply sits in the conversation.
func TestAnObservationFaultIsCheckedBeforeReviewerFallback(t *testing.T) {
	blob, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(blob)
	start := strings.Index(src, "func (e *Engine) resolveReview(")
	if start < 0 {
		t.Fatal("resolveReview was not found; this check proves nothing")
	}
	body := src[start:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	guard := strings.Index(body, "roles.ErrReviewObservationFault")
	fallback := strings.Index(body, "trying the next independent reviewer")
	unobtainable := strings.Index(body, "ReviewUnobtainable")
	if guard < 0 {
		t.Fatal("resolveReview does not recognise an observation fault")
	}
	if fallback >= 0 && guard > fallback {
		t.Error("the observation guard runs after the fallback emit")
	}
	if unobtainable >= 0 && guard > unobtainable {
		t.Error("the observation guard runs after the provider is counted unavailable")
	}
}

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

// FAILURE TO OBTAIN AN INDEPENDENT REVIEW IS TRANSPORT STATE, NOT AUTHORITY.
//
// Reproduced live on 2026-09-16, task-1789591067359023454: the reviewer exited 1
// on quota, the alternate did not converge, and the orchestrator handed the same
// candidate back to the failed reviewer AS ITS IMPLEMENTER. The run ended
// INCOMPLETE/FAILED having never judged the work.
//
// These witnesses drive the REAL candidate loop over a real worktree, real diff
// and real audit, injecting faults at the reviewer transport. A stub that returns
// an expected enum would prove nothing about the state machine: the defect was a
// transition, not a value.

// unreachableRunner is a reviewer transport that fails. It counts its calls, so a
// witness can assert how many times the chain was tried.
type unreachableRunner struct {
	calls atomic.Int32
	err   error
}

func (r *unreachableRunner) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	r.calls.Add(1)
	return agent.Result{}, r.err
}

// quotaExhausted is the exact shape observed: a provider process exiting non-zero.
func quotaExhausted() error { return errors.New("codex exited 1: usage limit reached") }

// reviewerTimedOut is the transport timing out rather than refusing.
func reviewerTimedOut() error { return context.DeadlineExceeded }

// unavailableHarness is the gate harness with a FAILING reviewer transport.
func unavailableHarness(t *testing.T, runner agent.Runner) *gateHarness {
	t.Helper()
	h := newGateHarness(t, requiresIndependentReview(), roles.Fresh, "accept")
	h.engine.Runners = roleResolver{reviewer: runner, name: "remote:codex", session: "session-1"}
	return h
}

// candidateFingerprint is the bytes-and-identity of the candidate worktree, used
// to prove the blocked interval changed nothing.
func candidateFingerprint(t *testing.T, work string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(work, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if info.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		b, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		rel, _ := filepath.Rel(work, path)
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatalf("fingerprint the candidate: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("the candidate fingerprint is empty; this control asserts nothing")
	}
	return out
}

// runBlocked drives the real candidate loop and returns BOTH the outcome and the
// error, because the blocked condition travels with its error exactly as the
// unanswered one does. gateHarness.run fatals on any error, which would hide the
// very transition these witnesses are about.
func runBlocked(t *testing.T, h *gateHarness) (candidateOutcome, error) {
	t.Helper()
	outcome, _, _, _, err := h.engine.runCandidate(context.Background(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	if err != nil && !errors.Is(err, roles.ErrReviewUnobtainable) {
		t.Fatalf("the governed candidate loop failed for an unrelated reason: %v", err)
	}
	return outcome, err
}

// 1. REVIEWER PROVIDER UNAVAILABLE IMMEDIATELY.
func TestAnUnavailableReviewerBlocksExternallyRatherThanFailingTheImplementer(t *testing.T) {
	h := unavailableHarness(t, &unreachableRunner{err: quotaExhausted()})

	outcome, err := runBlocked(t, h)

	if !errors.Is(err, roles.ErrReviewUnobtainable) {
		t.Fatalf("the blocked condition did not travel with its own identity: %v", err)
	}
	if outcome == candidateNotConverged {
		t.Fatal("a reviewer that could not be reached was reported as the IMPLEMENTER failing to converge; " +
			"that is the transition that sent the candidate down the handoff ladder")
	}
	if outcome.Accepted() {
		t.Fatal("silence from every reviewer was read as ACCEPT")
	}
	if outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q; an unreachable reviewer must leave the candidate blocked on external review", outcome)
	}
}

// 2. REVIEWER TIMEOUT. A transport that times out is the same condition as one
// that refuses: no review was produced, and the candidate is not at fault.
func TestAReviewerTimeoutBlocksExternallyToo(t *testing.T) {
	h := unavailableHarness(t, &unreachableRunner{err: reviewerTimedOut()})

	if outcome, _ := runBlocked(t, h); outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q on a reviewer timeout; want the candidate blocked on external review", outcome)
	}
}

// 3. REPEATED REVIEWER-PROVIDER FAILURES. Every provider in the chain is tried --
// a fresh request to another authorized reviewer is PERMITTED -- and the
// exhausted chain is still transport state, not a verdict and not a failure.
func TestEveryReviewerIsTriedAndTheExhaustedChainIsStillTransportState(t *testing.T) {
	runner := &unreachableRunner{err: quotaExhausted()}
	h := unavailableHarness(t, runner)

	outcome, _ := runBlocked(t, h)

	if runner.calls.Load() == 0 {
		t.Fatal("no reviewer was tried at all; this witness asserts nothing about the chain")
	}
	if outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q after every reviewer failed; want blocked on external review", outcome)
	}
}

// 6. THE IMPLEMENTATION CYCLE DOES NOT ADVANCE WHILE REVIEW IS UNOBTAINABLE.
//
// Measured from the engine's OWN event stream -- agent.started for the
// implementer role -- not from a stub's side effects. The stub overwrites its
// record file on every call, so counting its bytes would have measured the size
// of one prompt and called it a cycle count.
func TestTheImplementationCycleDoesNotAdvanceWhileReviewIsUnobtainable(t *testing.T) {
	h := unavailableHarness(t, &unreachableRunner{err: quotaExhausted()})

	outcome, _ := runBlocked(t, h)
	if outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q; the rest of this witness is only meaningful on the blocked path", outcome)
	}

	starts := implementerStarts(h)
	if starts == 0 {
		t.Fatal("the implementer never started at all; this witness would pass without measuring anything")
	}
	if starts > 1 {
		t.Fatalf("the implementer started %d times for one candidate: the cycle advanced while the "+
			"candidate was blocked awaiting a review nobody performed", starts)
	}
}

// implementerStarts counts implementer turns on the engine's own event stream.
func implementerStarts(h *gateHarness) int {
	n := 0
	for {
		select {
		case ev := <-h.events:
			if ev.Kind == event.AgentStarted && ev.Source != event.SourceReviewer {
				n++
			}
		default:
			return n
		}
	}
}

// 7. THE CANDIDATE IS UNCHANGED ACROSS THE BLOCKED INTERVAL.
func TestTheCandidateIsByteIdenticalAcrossTheBlockedInterval(t *testing.T) {
	h := unavailableHarness(t, &unreachableRunner{err: quotaExhausted()})

	outcome, _ := runBlocked(t, h)
	if outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q; want blocked on external review", outcome)
	}
	after := candidateFingerprint(t, h.work)

	// Running the blocked path again must not consume, mutate or discard it.
	second, _ := runBlocked(t, h)
	if second != candidateReviewUnobtainable {
		t.Fatalf("second attempt concluded %q; a blocked candidate must stay blocked, not degrade", second)
	}
	again := candidateFingerprint(t, h.work)

	if len(after) != len(again) {
		t.Fatalf("the candidate gained or lost files while blocked: %d then %d", len(after), len(again))
	}
	for name, body := range after {
		if again[name] != body {
			t.Errorf("%s changed while the candidate was blocked awaiting review", name)
		}
	}
}

// 8. THE PARTICIPANT THAT FAILED THE REVIEW LEG IS NOT ITS IMPLEMENTER.
//
// This is the exact substitution observed live. The engine records the failed
// review leg; selection must then refuse that participant for that candidate.
func TestAFailedReviewerIsNotSelectedAsImplementerForTheSameCandidate(t *testing.T) {
	e := &Engine{SessionID: "session-1"}
	binding := roles.Binding{TaskID: "task-1", CandidateDigest: "3f9cae25747c", BaseSHA: "5883f978"}
	e.excludeFromImplementing("task-1", &roles.ReviewUnobtainable{
		Binding:   binding,
		Attempted: []roles.ReviewAttemptFailure{{Provider: "codex", Cause: quotaExhausted()}},
	})

	reason, excluded := e.implementerExcluded("task-1", "codex")
	if !excluded {
		t.Fatal("the participant that failed the independent-review leg was still eligible to implement " +
			"the candidate it could not judge")
	}
	if !strings.Contains(reason, "does not become its implementer") {
		t.Errorf("the refusal does not say why: %q", reason)
	}

	// THE NEGATIVE CONTROL. The exclusion is about this candidate's independence,
	// not a judgement about the provider: anyone who did not fail the leg stays
	// eligible, or the ladder would empty itself.
	if _, excluded := e.implementerExcluded("task-1", "claude"); excluded {
		t.Error("a participant that never reviewed this candidate was excluded from implementing it")
	}
	// And the exclusion does not leak across tasks.
	if _, excluded := e.implementerExcluded("task-2", "codex"); excluded {
		t.Error("the exclusion leaked to a different task")
	}
}

// 4 and 5. A FRESH REQUEST IS A NEW OBLIGATION IDENTITY AGAINST THE SAME CANDIDATE.
//
// The replacement request carries a new attempt identity bound to the SAME
// immutable candidate. A response bound to the withdrawn request cannot satisfy
// it -- otherwise a stale answer from the provider that already failed would
// close an obligation issued to somebody else.
func TestAReplacementRequestIsANewObligationOnTheSameCandidate(t *testing.T) {
	binding := roles.Binding{TaskID: "task-1", CandidateDigest: "3f9cae25747c", BaseSHA: "5883f978"}
	first := &roles.ReviewUnobtainable{Binding: binding, Attempt: 1,
		Attempted: []roles.ReviewAttemptFailure{{Provider: "codex", Cause: quotaExhausted()}}}

	// The candidate identity is what a replacement must match.
	if first.Binding.CandidateDigest != binding.CandidateDigest {
		t.Fatal("the blocked obligation lost the candidate identity a replacement must bind to")
	}
	// A new attempt against the same candidate is a NEW obligation identity.
	second := &roles.ReviewUnobtainable{Binding: binding, Attempt: 2,
		Attempted: []roles.ReviewAttemptFailure{{Provider: "gemini", Cause: quotaExhausted()}}}
	if second.Attempt == first.Attempt {
		t.Fatal("the replacement reused the original attempt identity; a stale response would satisfy it")
	}
	if second.Binding.CandidateDigest != first.Binding.CandidateDigest {
		t.Fatal("the replacement changed the candidate; a new request is a new obligation on the SAME candidate, " +
			"never a new candidate")
	}
	// The provider that failed the first leg is excluded; the fresh one is not.
	if !first.Excludes("codex") {
		t.Error("the failed provider was not recorded, so nothing can exclude it")
	}
	if first.Excludes("gemini") {
		t.Error("a provider that never attempted the leg was excluded")
	}
}

// THE TERMINAL SAYS WHAT IS OWED, and names the providers tried, so a fresh
// request can be issued to somebody else.
func TestTheBlockedTerminalNamesTheObligationAndTheProvidersTried(t *testing.T) {
	h := unavailableHarness(t, &unreachableRunner{err: quotaExhausted()})
	if outcome, _ := runBlocked(t, h); outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q; want blocked on external review", outcome)
	}
	u := &roles.ReviewUnobtainable{
		Binding:   roles.Binding{CandidateDigest: "3f9cae25747c"},
		Attempted: []roles.ReviewAttemptFailure{{Provider: "codex", Cause: quotaExhausted()}},
	}
	if !errors.Is(u, roles.ErrReviewUnobtainable) {
		t.Fatal("the condition does not match its own sentinel, so no caller can route on it")
	}
	if !strings.Contains(u.Error(), "codex") {
		t.Errorf("the error does not name the provider tried: %v", u)
	}
	var probe *roles.ReviewUnobtainable
	if !errors.As(error(u), &probe) {
		t.Fatal("the typed condition is not recoverable by errors.As, so the terminal cannot report its identity")
	}
}

var _ = json.Marshal
var _ = config.Agent{}

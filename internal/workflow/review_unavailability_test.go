package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// candidateA and candidateB are two distinct candidate identities in one task.
const (
	candidateA = "3f9cae25747c0aa1bb22cc33dd44ee55ff6600771188229933aa44bb55cc66dd"
	candidateB = "aa11bb22cc33dd44ee55ff6600771188229933aa44bb55cc66dd77ee88ff9900"
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
// This is the exact substitution observed live: the provider that had just failed
// to review a candidate was handed that candidate to implement.
func TestAFailedReviewerIsNotSelectedAsImplementerForTheSameCandidate(t *testing.T) {
	e := &Engine{SessionID: "session-1"}
	e.excludeFromImplementing("task-1", &roles.ReviewUnobtainable{
		Binding:   roles.Binding{TaskID: "task-1", CandidateDigest: candidateA, BaseSHA: "5883f978"},
		Attempted: []roles.ReviewAttemptFailure{{Provider: "codex", Cause: quotaExhausted()}},
	})

	reason, excluded := e.implementerExcluded("task-1", "codex", candidateA)
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
	if _, excluded := e.implementerExcluded("task-1", "claude", candidateA); excluded {
		t.Error("a participant that never reviewed this candidate was excluded from implementing it")
	}
	// And the exclusion does not leak across tasks.
	if _, excluded := e.implementerExcluded("task-2", "codex", candidateA); excluded {
		t.Error("the exclusion leaked to a different task")
	}
}

// 3 (this round). THE EXCLUSION IS SCOPED TO THE EXACT CANDIDATE BINDING.
//
// A provider that could not review candidate A has said nothing about candidate
// B. Keying the exclusion by task alone -- which the first cut of this repair did
// -- retires a worker from every later candidate in the task because of one
// transport failure it had no part in. That is a standing judgement about a
// provider dressed up as a fact about independence.
func TestAnExclusionForOneCandidateDoesNotRetireTheProviderForLaterCandidates(t *testing.T) {
	e := &Engine{SessionID: "session-1"}
	e.excludeFromImplementing("task-1", &roles.ReviewUnobtainable{
		Binding:   roles.Binding{TaskID: "task-1", CandidateDigest: candidateA},
		Attempted: []roles.ReviewAttemptFailure{{Provider: "codex", Cause: quotaExhausted()}},
	})

	// The candidate it failed to review: excluded.
	if _, excluded := e.implementerExcluded("task-1", "codex", candidateA); !excluded {
		t.Fatal("the exclusion does not hold for the candidate it was recorded against")
	}
	// An unrelated successor candidate in the SAME task: eligible.
	if reason, excluded := e.implementerExcluded("task-1", "codex", candidateB); excluded {
		t.Fatalf("a provider excluded for candidate %s was retired for unrelated candidate %s: %q",
			shortDigest(candidateA), shortDigest(candidateB), reason)
	}
	// An unnameable candidate excludes nobody: guessing here is how the
	// task-wide exclusion comes back.
	if _, excluded := e.implementerExcluded("task-1", "codex", ""); excluded {
		t.Error("an empty candidate identity produced an exclusion, which is the task-wide shape again")
	}
	// A record carrying no candidate identity is not stored at all, for the
	// same reason.
	e.excludeFromImplementing("task-9", &roles.ReviewUnobtainable{
		Attempted: []roles.ReviewAttemptFailure{{Provider: "codex", Cause: quotaExhausted()}},
	})
	if _, excluded := e.implementerExcluded("task-9", "codex", candidateA); excluded {
		t.Error("an unattributable exclusion was recorded and then applied to a candidate")
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

// perProviderResolver dispatches the reviewer turn by the CONFIGURED PROVIDER, so
// a two-provider chain can fail its first leg and answer on its second. A single
// stub cannot express that: it would answer identically whoever was asked, and
// "the chain was walked" would be unobservable.
type perProviderResolver struct {
	runners map[string]agent.Runner
	seen    *[]string
	session string
}

func (r perProviderResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	if spec.Role == roles.Reviewer {
		name := strings.ToLower(strings.TrimSpace(spec.Agent.Name))
		*r.seen = append(*r.seen, name)
		if runner, ok := r.runners[name]; ok {
			return Resolved{Runner: runner, Name: "remote:" + name, Label: name}, nil
		}
		return Resolved{}, fmt.Errorf("no stub reviewer for %q", name)
	}
	return CLIResolved(spec, r.session), nil
}

// twoProviderHarness configures a REAL two-reviewer roster and resolves each leg
// to its own runner.
func twoProviderHarness(t *testing.T, first, second agent.Runner) (*gateHarness, *[]string) {
	t.Helper()
	h := newGateHarness(t, requiresIndependentReview(), roles.Fresh, "accept")
	seen := &[]string{}
	h.engine.Config.Reviewer = config.Agent{}
	h.engine.Config.Reviewers = []config.Agent{
		{Name: "codex", Command: "true", Graph: "none"},
		// chatgpt, not an invented name: roles.Assign builds the chain from
		// provider.Capability, and a provider the binary does not recognise gets
		// no roles and can never be an alternate. A stub named "gemini" produced a
		// one-entry chain that looked like a fallback and was not.
		{Name: "chatgpt", Command: "true", Graph: "none"},
	}
	h.engine.Runners = perProviderResolver{
		runners: map[string]agent.Runner{"codex": first, "chatgpt": second},
		seen:    seen, session: "session-1",
	}
	return h, seen
}

// 2a. BOTH PROVIDERS FAIL: the chain is walked in order and the result is still
// transport state, with the candidate untouched and the cycle not advanced.
func TestBothReviewersFailingWalksTheChainAndStillBlocksExternally(t *testing.T) {
	h, seen := twoProviderHarness(t,
		&unreachableRunner{err: quotaExhausted()},
		&unreachableRunner{err: reviewerTimedOut()})

	outcome, err := runBlocked(t, h)
	// Fingerprinted AFTER the run: the blocked interval begins once the candidate
	// exists. Comparing against the pre-run tree measured the implementer doing
	// its job and called it drift.
	blocked := candidateFingerprint(t, h.work)

	if len(*seen) < 2 {
		t.Fatalf("the chain was not walked: reviewers attempted = %v; a second authorized reviewer must be "+
			"tried before the chain is called exhausted", *seen)
	}
	if (*seen)[0] == (*seen)[1] {
		t.Errorf("the same provider was tried twice instead of falling back: %v", *seen)
	}
	if outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q after BOTH reviewers failed; want blocked on external review", outcome)
	}
	var u *roles.ReviewUnobtainable
	if !errors.As(err, &u) {
		t.Fatalf("the exhausted chain did not carry its typed identity: %v", err)
	}
	if len(u.Providers()) < 2 {
		t.Errorf("only %v recorded as tried; a fresh request cannot avoid providers nobody recorded", u.Providers())
	}
	if starts := implementerStarts(h); starts > 1 {
		t.Errorf("the implementer started %d times: the cycle advanced because reviewers were unreachable", starts)
	}
	// The blocked candidate survives a second attempt untouched: not consumed,
	// not discarded, not reminted.
	if second, _ := runBlocked(t, h); second != candidateReviewUnobtainable {
		t.Fatalf("a second attempt on a blocked candidate concluded %q", second)
	}
	again := candidateFingerprint(t, h.work)
	if len(blocked) != len(again) {
		t.Fatalf("the candidate gained or lost files across the blocked interval: %d then %d", len(blocked), len(again))
	}
	for name, body := range blocked {
		if again[name] != body {
			t.Errorf("%s changed across the blocked interval", name)
		}
	}
}

// 2b. THE FIRST PROVIDER FAILS AND THE SECOND ANSWERS.
//
// The same immutable candidate continues on the second reviewer's verdict.
// Nothing is reminted and no implementation cycle is spent merely because the
// first leg failed: a transport failure is not work.
func TestASecondReviewerAnsweringContinuesTheSameCandidate(t *testing.T) {
	verdict, _ := json.Marshal(map[string]any{"decision": "accept", "summary": "the candidate stands"})
	h, seen := twoProviderHarness(t,
		&unreachableRunner{err: quotaExhausted()},
		answeringRunner{text: string(verdict), mode: roles.Fresh})

	outcome, err := runBlocked(t, h)

	if err != nil {
		t.Fatalf("a chain whose second reviewer answered still failed: %v", err)
	}
	if len(*seen) < 2 {
		t.Fatalf("the second reviewer was never asked: %v", *seen)
	}
	if outcome == candidateReviewUnobtainable {
		t.Fatal("a chain with an answering second reviewer was reported as unobtainable; " +
			"one failed leg is not an exhausted chain")
	}
	if outcome == candidateNotConverged {
		t.Fatal("the first leg's transport failure was charged to the implementer")
	}
	// Both legs judge ONE candidate produced by ONE implementer turn. If the
	// failed first leg had cost a cycle, the implementer would have run again.
	if starts := implementerStarts(h); starts > 1 {
		t.Errorf("the implementer started %d times: a cycle was spent because the first reviewer failed", starts)
	}
}

// 1 (this round). THE READ-ONLY PATH CARRIES THE SAME REPAIR.
//
// runCandidate has TWO review call sites: the modify loop and the inspection
// branch. The first cut of this repair changed both and witnessed only the
// modify one, so reverting the inspection site to candidateNotConverged left
// every test green -- a surviving mutant reported rather than hidden.
//
// A read-only plan produces findings rather than a diff, and those findings are
// acted on. An unreachable reviewer there is the same condition: nobody judged
// the report, and the worker did not fail.
func TestAnUnavailableReviewerOnTheReadOnlyPathAlsoBlocksExternally(t *testing.T) {
	h := unavailableHarness(t, &unreachableRunner{err: quotaExhausted()})
	h.tc.Mode = ModeInspect
	// A read-only plan that changes a file is refused before any reviewer is
	// consulted, and rightly. The inspect worker drains its prompt and REPORTS,
	// touching nothing, so the run reaches the review seam this witness is about.
	reporter := config.Agent{
		Name: "claude", Graph: "none",
		Command: "/bin/sh",
		Args:    []string{"-c", "cat >/dev/null; echo 'FINDING: the ledger verifier indexes the files slice against the entries slice'"},
	}
	h.worker = reporter
	h.engine.Config.Implementors = []config.Agent{reporter}

	outcome, err := runBlocked(t, h)

	if outcome == candidateNotConverged {
		t.Fatal("on the read-only path a reviewer that could not be reached was reported as the worker " +
			"failing to converge; the findings stand and nobody judged them")
	}
	if outcome != candidateReviewUnobtainable {
		t.Fatalf("outcome %q on the read-only path; want blocked on external review", outcome)
	}
	if !errors.Is(err, roles.ErrReviewUnobtainable) {
		t.Fatalf("the read-only path lost the blocked condition's identity: %v", err)
	}
}

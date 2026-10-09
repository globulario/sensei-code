package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
)

// DF-41A3 (RULING-145): THE INCOMPLETE IMPLEMENTER OBLIGATION REMAINS IN THE
// SAME LIVE REVIEW CYCLE. These witnesses drive the REAL candidate loop: a real
// repository and worktree, the stub Sensei, the landed 70A1 accounting and the
// landed 70A2 settlement. Only the implementer and reviewer transports are
// scripted, so what an invocation returned -- report and adapter observation --
// is exactly what the witness says it was.

// incompleteTurn is one implementer invocation.
type incompleteTurn struct {
	// provider is the adapter name the resolver binds; "" is claude.
	provider string
	// value is what main.go prints after the turn; 0 leaves main.go alone.
	value  int
	report string
	// inv is the adapter's observation. A turn with neither inv nor err is
	// observed as a real plain-text adapter observes a completed output:
	// returned, with report as its report. unobserved withholds that.
	inv        *agent.Invocation
	unobserved bool
	err        error
}

// plainReturn is a plain-text adapter's observation of a returned report.
func plainReturn(report string) *agent.Invocation {
	return &agent.Invocation{Transport: agent.Transport{Adapter: agent.AdapterPlainText}, Returned: true, Report: report, Exited: true}
}

// completionRig resolves every implementer turn from its script, in order, and
// every review from its verdicts; the last verdict repeats.
type completionRig struct {
	h        *gateHarness
	turns    []incompleteTurn
	next     int
	prompts  []string
	verdicts []string
	reviews  int
	events   <-chan event.Event
}

func (r *completionRig) Resolve(spec RunnerSpec) (Resolved, error) {
	switch spec.Role {
	case roles.Reviewer:
		return Resolved{Runner: rigReviewer{r}, Name: "remote:abc", Label: "remote:abc"}, nil
	case roles.Implementer:
		if r.next >= len(r.turns) {
			return Resolved{}, fmt.Errorf("the script has no implementer turn %d", r.next+1)
		}
		name := r.turns[r.next].provider
		if name == "" {
			name = "claude"
		}
		return Resolved{Runner: rigImplementer{r}, Name: name, Label: name}, nil
	}
	return CLIResolved(spec, "session-1"), nil
}

type rigImplementer struct{ r *completionRig }

func (x rigImplementer) Run(_ context.Context, req agent.Request, _ func(event.Event)) (agent.Result, error) {
	turn := x.r.turns[x.r.next]
	x.r.next++
	x.r.prompts = append(x.r.prompts, req.Prompt)
	if turn.value != 0 {
		body := fmt.Sprintf("package main\n\nfunc main() { println(%d) }\n", turn.value)
		if err := os.WriteFile(filepath.Join(req.Workspace, "main.go"), []byte(body), 0o644); err != nil {
			return agent.Result{}, err
		}
	}
	inv := turn.inv
	if inv == nil && turn.err == nil && !turn.unobserved {
		inv = plainReturn(turn.report)
	}
	return agent.Result{Text: turn.report, Invocation: inv}, turn.err
}

type rigReviewer struct{ r *completionRig }

func (x rigReviewer) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	i := x.r.reviews
	if i >= len(x.r.verdicts) {
		i = len(x.r.verdicts) - 1
	}
	x.r.reviews++
	return agent.Result{Text: x.r.verdicts[i], Session: roles.Fresh}, nil
}

const acceptVerdict = `{"decision":"accept","summary":"the candidate stands"}`

func reviseVerdict(findings ...string) string {
	return `{"decision":"revise","summary":"the plan is not refused","findings":[` + strings.Join(findings, ",") + `]}`
}

// codeOn is a blocking CODE finding pointing at main.go, the one file the plan names.
func codeOn(id string) string {
	return `{"id":"` + id + `","severity":"blocking","class":"code","claim":"claim ` + id + `","reference":"main.go","reason":"reason ` + id + `"}`
}

func answerCode(id string) string {
	return `{"id":"` + id + `","answered_by":"code","paths":["main.go"]}`
}

func accounting(responses ...string) string {
	return `{"finding_responses":[` + strings.Join(responses, ",") + `]}`
}

// newCompletionRig: cycle 1 writes main.go and is reviewed REVISE with
// findings; every later review accepts. turns are the invocations after cycle 1.
func newCompletionRig(t *testing.T, findings []string, turns ...incompleteTurn) *completionRig {
	t.Helper()
	h := newGateHarness(t, roles.Policy{Reason: "blast radius file with approval gate none"}, roles.Fresh, "accept")
	r := &completionRig{h: h, verdicts: []string{reviseVerdict(findings...), acceptVerdict}}
	r.turns = append([]incompleteTurn{{value: 1}}, turns...)
	events, cancel := h.engine.Bus.Subscribe(8192)
	t.Cleanup(cancel)
	r.events = events
	h.engine.Runners = r
	h.engine.Config.Workflow.ReviewCycles = 2
	return r
}

func (r *completionRig) run() (candidateOutcome, error) {
	outcome, _, _, _, err := r.h.engine.runCandidate(fixtureCtx(r.h.engine, "task-1"), r.h.sc, certifiedStart{},
		"task-1", r.h.tc, "Rewrite main.go so it prints a number.", r.h.worker, r.h.work, "")
	return outcome, err
}

// cycles are the review-cycle numbers of every settled implementer invocation.
func (r *completionRig) cycles() []int {
	var out []int
	for _, s := range r.h.engine.settledInvocations("task-1") {
		out = append(out, s.Cycle)
	}
	return out
}

func (r *completionRig) live(t *testing.T) *cycleCompletion {
	t.Helper()
	c, ok := r.h.engine.liveCycleCompletion("task-1")
	if !ok {
		t.Fatal("no live completion obligation binds the open review and the operative plan attempt")
	}
	return c
}

// stored is the task's obligation record whether or not it still binds: an
// accepted cycle closed its open review, and the record is what it completed.
func (r *completionRig) stored(t *testing.T) *cycleCompletion {
	t.Helper()
	r.h.engine.mu.Lock()
	defer r.h.engine.mu.Unlock()
	c := r.h.engine.completions["task-1"]
	if c == nil {
		t.Fatal("the task holds no completion obligation")
	}
	return c.clone()
}

// incompleteReports are the machine-readable incomplete-attempt records.
func (r *completionRig) incompleteReports(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, ev := range drainEvents(r.events) {
		var p struct {
			Incomplete map[string]any `json:"implementer_incomplete"`
		}
		if json.Unmarshal(ev.Payload, &p) == nil && p.Incomplete != nil {
			out = append(out, p.Incomplete)
		}
	}
	return out
}

// owedSection is the part of a prompt that lists what is still owed.
func owedLines(prompt string) string {
	i := strings.Index(prompt, "STILL OWED")
	if i < 0 {
		return ""
	}
	return prompt[i:]
}

func equalInts(a []int, b ...int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalStrings(a []string, b ...string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// W1 + W2. Cycle 2 owes f1 and f2. Invocation 1 validly answers f1 only: f1 is
// retained, f2 stays owed, the cycle stays 2, the attempt becomes 1 and the
// next invocation is asked only for f2. Invocation 2 answers f2: the cycle
// completes without f1 being re-answered, after two attempts, and goes to the
// ordinary review.
//
// Fails if an incomplete invocation ends the run or spends a cycle, if f1 is
// dropped, or if the retry is asked for f1 again.
func TestDF41A3W1W2AMissingResponseRetriesTheSameCycleAndTheSecondCompletesIt(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 3, report: accounting(answerCode("f2"))},
	)
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("the cycle was not completed by the second invocation: outcome %q err %v", outcome, err)
	}
	if !equalInts(r.cycles(), 1, 2, 2) {
		t.Fatalf("the incomplete retry did not keep its cycle: invocation cycles %v", r.cycles())
	}
	if r.h.engine.Config.Workflow.ReviewCycles != 2 {
		t.Fatalf("the review-cycle budget changed: %d", r.h.engine.Config.Workflow.ReviewCycles)
	}
	if r.reviews != 2 {
		t.Fatalf("the reviewer was asked %d times; cycle 2 goes to one ordinary review", r.reviews)
	}
	reports := r.incompleteReports(t)
	if len(reports) != 1 {
		t.Fatalf("expected exactly one incomplete attempt, got %v", reports)
	}
	got := reports[0]
	if got["review_cycle"] != float64(2) || got["attempts"] != float64(1) ||
		fmt.Sprint(got["owed_findings"]) != "[f2]" || fmt.Sprint(got["retained_findings"]) != "[f1]" {
		t.Fatalf("the incomplete attempt is not cycle 2, attempt 1, f1 retained, f2 owed: %v", got)
	}
	first, retry := r.prompts[1], r.prompts[2]
	if !strings.Contains(first, "[f1]") || !strings.Contains(first, "[f2]") {
		t.Fatalf("premise: the cycle's first invocation was not asked for both findings:\n%s", first)
	}
	owed := owedLines(retry)
	if !strings.Contains(owed, "- [f2]") || strings.Contains(owed, "[f1]") || strings.Contains(retry, "- [f1]") {
		t.Fatalf("the retry was not asked for exactly f2:\n%s", retry)
	}
	if !strings.Contains(retry, "ALREADY SATISFIED") || !strings.Contains(retry, "f1") {
		t.Fatalf("the retry was not given f1 as settled context:\n%s", retry)
	}
	c := r.stored(t)
	if len(c.Owed()) != 0 || !equalStrings(c.RetainedIDs(), "f1", "f2") || c.Attempts != 1 || c.Cycle != 2 {
		t.Fatalf("the completed obligation is not f1+f2 retained after one incomplete attempt in cycle 2: %+v", c)
	}
}

// W3 NO REVIEW-CYCLE CONSUMPTION. Two incomplete returns before one complete
// one, on a two-cycle budget. The review after them is the SAME cycle 2, and it
// accepts: had either retry spent a cycle the budget would be gone first.
func TestDF41A3W3IncompleteRetriesConsumeNoReviewCycle(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: "nothing yet"},
		incompleteTurn{value: 3, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 4, report: accounting(answerCode("f2"))},
	)
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("two incomplete retries spent the review budget: outcome %q err %v", outcome, err)
	}
	if !equalInts(r.cycles(), 1, 2, 2, 2) || r.reviews != 2 {
		t.Fatalf("the review after the retries is not cycle 2's: cycles %v reviews %d", r.cycles(), r.reviews)
	}
	changed := 0
	for _, ev := range drainEvents(r.events) {
		if ev.Kind == event.CandidateChanged && strings.Contains(string(ev.Payload), `"cycle":2`) {
			changed++
		}
	}
	if changed != 3 {
		t.Fatalf("expected three cycle-2 candidates (two incomplete, one reviewed), saw %d", changed)
	}
}

// W4 RETAINED SIBLING SURVIVES A MALFORMED RESTATEMENT. f1 is retained by
// invocation 1. Invocation 2 then answers f2 beside:
//
//	a harmless restatement of f1 -- the same response again: f1 stays retained
//	and the cycle completes;
//	a conflicting response for f1 -- here a mis-classed answer: it reaches the
//	canonical validator beside the retained one, the conflict rule reopens f1
//	ALONE, f2 is retained, and the retry is asked for exactly f1;
//	a wholly malformed accounting: nothing reaches the validator, f1 stays
//	retained and f2 stays owed.
//
// Fails if a later envelope for a retained finding is discarded unjudged (the
// conflict would leave f1 satisfied), or if it erases a sibling.
func TestDF41A3W4ARetainedSiblingSurvivesAMalformedRestatement(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 3, report: accounting(answerCode("f1"), answerCode("f2"))},
	)
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("a harmless restatement of a retained f1 reopened it: outcome %q err %v", outcome, err)
	}
	if reports := r.incompleteReports(t); len(reports) != 1 {
		t.Fatalf("the restatement was routed as anything but completion: %v", reports)
	}
	if c := r.stored(t); !equalStrings(c.RetainedIDs(), "f1", "f2") || c.Attempts != 1 {
		t.Fatalf("f1 and f2 are not both retained after one incomplete attempt: %+v", c)
	}

	conflicting := accounting(`{"id":"f1","answered_by":"evidence","evidence":"gofmt"}`, answerCode("f2"))
	r = newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 3, report: conflicting},
		incompleteTurn{value: 4, report: accounting(answerCode("f1"))},
	)
	outcome, err = r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("outcome %q err %v", outcome, err)
	}
	reports := r.incompleteReports(t)
	if len(reports) != 2 {
		t.Fatalf("the conflict on f1 was not judged: %d incomplete attempts, want 2: %v", len(reports), reports)
	}
	got := reports[1]
	if fmt.Sprint(got["owed_findings"]) != "[f1]" || fmt.Sprint(got["retained_findings"]) != "[f2]" ||
		fmt.Sprint(got["conflicted_findings"]) != "[f1]" || fmt.Sprint(got["lapsed_findings"]) != "[f1]" || got["review_cycle"] != float64(2) {
		t.Fatalf("the conflict did not reopen f1 alone while f2 was retained, in cycle 2: %v", got)
	}
	if owed := owedLines(r.prompts[3]); !strings.Contains(owed, "- [f1]") || strings.Contains(owed, "[f2]") ||
		!strings.Contains(owed, "duplicated or conflicting") {
		t.Fatalf("the retry after the conflict was not asked for exactly f1, under the canonical conflict rule:\n%s", r.prompts[3])
	}

	malformed := `{"finding_responses":[` + answerCode("f2") + `],"finding_responses":[]}`
	r = newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 3, report: malformed},
		incompleteTurn{value: 4, report: accounting(answerCode("f2"))},
	)
	outcome, err = r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("malformed accounting erased the retained f1 or satisfied f2: outcome %q err %v", outcome, err)
	}
	reports = r.incompleteReports(t)
	if len(reports) != 2 || fmt.Sprint(reports[1]["retained_findings"]) != "[f1]" || fmt.Sprint(reports[1]["owed_findings"]) != "[f2]" {
		t.Fatalf("after malformed accounting f1 is not retained with f2 owed: %v", reports)
	}
}

// architectDouble configures an architect that proceeds with a revised plan;
// the stub Sensei cannot certify it, so a dispute's route ends there.
func architectDouble(t *testing.T, h *gateHarness) string {
	t.Helper()
	asked := t.TempDir() + "/architect-prompt"
	script := t.TempDir() + "/architect.sh"
	if err := os.WriteFile(script, []byte("cat >> '"+asked+"'\n"+
		`echo '{"decision":"proceed","summary":"the reviewer class stands","mode":"modify","files":["main.go"],"plan":"Answer f4 as code."}'`+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	h.engine.Config.Architect = config.Agent{Name: "architect-double", Command: "sh", Args: []string{script}, Graph: "none"}
	return asked
}

// replacePlanAttempt makes a different plan operative through the production
// attempt transition.
func replacePlanAttempt(t *testing.T, h *gateHarness, plan string) planAttempt {
	t.Helper()
	return adoptFixturePlanAttempt(t, h.engine, "task-1", h.tc.Task, h.tc.Identity.BaseSHA, plan, []string{"main.go"}, func() {
		h.engine.setRouting("task-1", roles.Policy{Reason: "blast radius file with approval gate none"}, sensei.PreflightDecision{}, nil, nil)
	})
}

// W5 DISPUTE RETAINS SIBLINGS. f1-f3 answered, f4 disputed: f1-f3 are
// retained, only f4 is owed, and the dispute goes to the architect. While the
// architect's decision is unresolved the obligation keeps its facts and nothing
// else: no attempt is counted and the cycle is not live, so when the architect
// cannot be certified the failed-authority route leaves nothing for a later
// implementer to resume. Through the governed continuation onto the revised
// attempt, f1-f3 stay satisfied, the cycle is unchanged, and the next
// invocation is asked only for f4 in its original class.
//
// Fails if the dispute counts or activates the cycle before the architect has
// decided (Attempts 1, or Continuing, after the refused revision).
func TestDF41A3W5ADisputeRetainsItsSiblingsAcrossTheArchitectContinuation(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2"), codeOn("f3"), codeOn("f4")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"), answerCode("f2"), answerCode("f3"), disputeF4)},
	)
	asked := architectDouble(t, r.h)
	disputedUnder := r.h.engine.operativePlanAttempt("task-1").ID
	if _, err := r.run(); err == nil {
		t.Fatal("premise: the stub Sensei certified the architect's revision")
	}
	if _, err := os.Stat(asked); err != nil {
		t.Fatalf("the dispute never reached the architect: %v", err)
	}
	c := r.live(t)
	if !equalStrings(c.RetainedIDs(), "f1", "f2", "f3") || !equalStrings(c.Owed(), "f4") || c.Cycle != 2 {
		t.Fatalf("the disputed invocation did not retain f1-f3 with only f4 owed in cycle 2: %+v", c)
	}
	if c.Attempts != 0 || c.Continuing || c.Route != routeDisputeEscalation {
		t.Fatalf("the unresolved dispute counted or activated the cycle without the architect's decision: %+v", c)
	}
	if reports := r.incompleteReports(t); len(reports) != 0 {
		t.Fatalf("an incomplete attempt was recorded although the architect never decided: %v", reports)
	}

	// The architect's revision becomes operative through the production
	// transition, and the obligation crosses it only by the explicit check.
	revised := replacePlanAttempt(t, r.h, "Answer f4 as code.")
	if !r.h.engine.continueCycleCompletion("task-1", disputedUnder) {
		t.Fatal("the governed continuation did not carry the cycle's obligation")
	}
	c = r.live(t)
	if c.PlanAttemptID != revised.ID || c.Cycle != 2 || !equalStrings(c.RetainedIDs(), "f1", "f2", "f3") || !equalStrings(c.Owed(), "f4") {
		t.Fatalf("the continued obligation lost a sibling or changed its cycle: %+v", c)
	}
	feedback := c.retryFeedback("The architect resolved the classification dispute.", nil)
	if owed := owedLines(feedback); !strings.Contains(owed, "- [f4] blocking code") || strings.Contains(owed, "[f1]") ||
		strings.Contains(owed, "[f2]") || strings.Contains(owed, "[f3]") {
		t.Fatalf("the next invocation is not asked for exactly f4 in its original class:\n%s", feedback)
	}
}

const disputeF4 = `{"id":"f4","answered_by":"code","paths":["main.go"],"disputes_class":"evidence","reason":"proof only"}`

// W5B A DECIDED DISPUTE IS COUNTED ONCE, AFTER THE DECISION. Here Sensei
// certifies the architect's revision, so the production route adopts it and
// continues the obligation under it. Only then is the invocation counted --
// exactly once -- and the cycle retried: the next invocation serves cycle 2,
// is asked only for f4, and its answer completes the cycle.
func TestDF41A3W5BADecidedDisputeCountsOnceAfterTheContinuation(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2"), codeOn("f3"), codeOn("f4")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"), answerCode("f2"), answerCode("f3"), disputeF4)},
		incompleteTurn{value: 3, report: accounting(answerCode("f4"))},
	)
	asked := architectDouble(t, r.h)
	sc, err := sensei.Start(context.Background(), r.h.engine.Repo.Root, "sh", []string{"-c", objectiveRunScript})
	if err != nil {
		t.Fatalf("start the certifying Sensei stub: %v", err)
	}
	t.Cleanup(func() { sc.Close() })
	r.h.sc = sc
	disputedUnder := r.h.engine.operativePlanAttempt("task-1").ID
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("the decided dispute did not continue its cycle to completion: outcome %q err %v", outcome, err)
	}
	if _, err := os.Stat(asked); err != nil {
		t.Fatalf("the dispute never reached the architect: %v", err)
	}
	if !equalInts(r.cycles(), 1, 2, 2) {
		t.Fatalf("the continuation did not keep its cycle: invocation cycles %v", r.cycles())
	}
	reports := r.incompleteReports(t)
	if len(reports) != 1 || reports[0]["attempts"] != float64(1) || fmt.Sprint(reports[0]["owed_findings"]) != "[f4]" ||
		fmt.Sprint(reports[0]["retained_findings"]) != "[f1 f2 f3]" || reports[0]["plan_attempt_id"] == disputedUnder {
		t.Fatalf("the decided dispute was not counted exactly once under the adopted attempt: %v", reports)
	}
	if owed := owedLines(r.prompts[2]); !strings.Contains(owed, "- [f4] blocking code") || strings.Contains(owed, "[f1]") ||
		strings.Contains(owed, "[f2]") || strings.Contains(owed, "[f3]") {
		t.Fatalf("the continued invocation is not asked for exactly f4:\n%s", r.prompts[2])
	}
	c := r.stored(t)
	if c.Attempts != 1 || c.Cycle != 2 || !equalStrings(c.RetainedIDs(), "f1", "f2", "f3", "f4") || len(c.Continuations) != 1 {
		t.Fatalf("the completed obligation is not cycle 2, one attempt, f1-f4 retained across one continuation: %+v", c)
	}
}

// W6 PROVIDER REBOUND INSIDE THE LOOP. Invocation A (claude) returns incomplete
// and invocation B is served by another provider: the obligation is the same,
// f1 stays retained and f2 owed until B answers it.
func TestDF41A3W6AProviderReboundKeepsTheCycleObligation(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{provider: "gemini", value: 3, report: accounting(answerCode("f2"))},
	)
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("a rebound invocation lost the cycle's obligation: outcome %q err %v", outcome, err)
	}
	settled := r.h.engine.settledInvocations("task-1")
	if len(settled) != 3 || settled[1].Provider != "claude" || settled[2].Provider != "gemini" || settled[2].Cycle != 2 {
		t.Fatalf("premise: the second cycle-2 invocation was not another provider's: %+v", settled)
	}
	if owed := owedLines(r.prompts[2]); !strings.Contains(owed, "- [f2]") || strings.Contains(owed, "[f1]") {
		t.Fatalf("the rebound provider was not asked for exactly f2:\n%s", r.prompts[2])
	}
	c := r.stored(t)
	if c.Cycle != 2 || c.ReviewAttempt != 1 || c.Attempts != 1 || !equalStrings(c.RetainedIDs(), "f1", "f2") {
		t.Fatalf("the rebound rekeyed or reset the obligation: %+v", c)
	}
}

// W7 FINAL-INVOCATION REBOUND (the e3bab8e shape). The worker's incomplete
// return is followed by its provider proving it cannot serve, so the worker's
// run ends and the next worker takes over -- a new candidate run in the same
// process, with nothing keyed by its invocation. The obligation exists anyway:
// the new worker serves cycle 2 itself, is asked only for f2, and its answer
// completes cycle 2. The review then revises again, and on a two-cycle budget
// that is the end: the rebound minted no review slot.
//
// Fails if the rebound run restarts at cycle 1 -- its invocation would be
// settled as cycle 1 and the revision would buy it a further invocation.
func TestDF41A3W7TheFinalInvocationReboundKeepsTheObligation(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{err: quota()},
		incompleteTurn{provider: "gemini", value: 3, report: accounting(answerCode("f2"))},
		incompleteTurn{provider: "gemini", value: 4, report: accounting(answerCode("f3"))},
	)
	r.verdicts = []string{reviseVerdict(codeOn("f1"), codeOn("f2")), reviseVerdict(codeOn("f3"))}
	_, err := r.run()
	var blocked *RoleUnavailable
	if !errors.As(err, &blocked) || blocked.Role != roles.Implementer {
		t.Fatalf("premise: the provider's refusal did not end the worker's run as unavailable: %v", err)
	}
	if c := r.live(t); c.Attempts != 1 || !c.Continuing || c.Cycle != 2 || !equalStrings(c.RetainedIDs(), "f1") || !equalStrings(c.Owed(), "f2") {
		t.Fatalf("an unavailable provider counted as an attempt or moved the obligation: %+v", c)
	}

	// The next configured worker, exactly as implement() hands over on an
	// unavailable provider: no handoff, nothing carried.
	next := r.h.worker
	next.Name = "gemini"
	_, _, _, _, err = r.h.engine.runCandidate(fixtureCtx(r.h.engine, "task-1"), r.h.sc, certifiedStart{},
		"task-1", r.h.tc, "Rewrite main.go so it prints a number.", next, r.h.work, "")
	if !errors.Is(err, errReviewCyclesExhausted) {
		t.Fatalf("the rebound run did not end on the original two-cycle budget: %v", err)
	}
	settled := r.h.engine.settledInvocations("task-1")
	if !equalInts(r.cycles(), 1, 2, 2, 2) || settled[3].Provider != "gemini" {
		t.Fatalf("the rebound invocation did not serve cycle 2, or a review slot was minted for it: cycles %v", r.cycles())
	}
	if r.reviews != 2 {
		t.Fatalf("the reviewer was asked %d times; cycle 1 and cycle 2 own one review each", r.reviews)
	}
	prompt := r.prompts[3]
	if owed := owedLines(prompt); !strings.Contains(owed, "- [f2]") || strings.Contains(owed, "[f1]") {
		t.Fatalf("the rebound worker was not asked for exactly f2:\n%s", prompt)
	}
	if !strings.Contains(prompt, "Review cycle 2 is not complete") {
		t.Fatalf("the rebound worker was not told it continues cycle 2:\n%s", prompt)
	}
	if c := r.stored(t); c.Cycle != 2 || c.Continuing || !equalStrings(c.RetainedIDs(), "f1", "f2") {
		t.Fatalf("the rebound created a new obligation instead of completing cycle 2's: %+v", c)
	}
}

// W8 PLANATTEMPT BOUNDARY. A's retained f1 does not satisfy a replacement
// attempt B because the ids match; B's obligation is fresh and owes f1 again.
// Only the explicit continuation from A carries it, and a continuation from
// any other attempt drops the obligation rather than rebinding it.
func TestDF41A3W8RetainedResponsesDoNotCrossAReplacedPlanAttempt(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{err: quota()},
		incompleteTurn{value: 3, report: accounting(answerCode("f2"))},
		incompleteTurn{err: quota()},
	)
	a := r.h.engine.operativePlanAttempt("task-1").ID
	r.run()
	if c := r.live(t); c.PlanAttemptID != a || !equalStrings(c.RetainedIDs(), "f1") {
		t.Fatalf("premise: f1 is not retained under attempt A: %+v", c)
	}

	b := replacePlanAttempt(t, r.h, "A materially different plan.")
	if _, ok := r.h.engine.liveCycleCompletion("task-1"); ok {
		t.Fatal("A's obligation still binds after B replaced A")
	}
	r.run()
	c := r.live(t)
	if c.PlanAttemptID != b.ID || !equalStrings(c.RetainedIDs(), "f2") || !equalStrings(c.Owed(), "f1") || c.Attempts != 1 {
		t.Fatalf("A's f1 satisfied B by id equality: %+v", c)
	}

	// The explicit continuation, and only it, carries.
	r = newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{err: quota()},
	)
	a = r.h.engine.operativePlanAttempt("task-1").ID
	r.run()
	b = replacePlanAttempt(t, r.h, "Answer f2 as code.")
	if r.h.engine.continueCycleCompletion("task-1", "not-"+a) {
		t.Fatal("a continuation from an attempt the obligation was not bound to carried it")
	}
	if _, ok := r.h.engine.liveCycleCompletion("task-1"); ok {
		t.Fatal("a refused continuation left an obligation standing")
	}

	r = newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{err: quota()},
	)
	a = r.h.engine.operativePlanAttempt("task-1").ID
	r.run()
	b = replacePlanAttempt(t, r.h, "Answer f2 as code.")
	if !r.h.engine.continueCycleCompletion("task-1", a) {
		t.Fatal("the explicit continuation from A was refused")
	}
	c = r.live(t)
	if c.PlanAttemptID != b.ID || !equalStrings(c.RetainedIDs(), "f1") || !equalStrings(c.Continuations, a+"->"+b.ID) {
		t.Fatalf("the explicit continuation did not carry f1 onto B on the record: %+v", c)
	}
}

// W9 COMPLETE-RESPONSE NONCONVERGENCE CONTROL. Every response is complete and
// valid, and the reviewer keeps objecting: the budget is spent as before, and
// the outcome is ordinary non-convergence, never IMPLEMENTER_INCOMPLETE.
func TestDF41A3W9ACompleteResponseSetThatDoesNotConvergeIsOrdinaryNonConvergence(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
	)
	r.verdicts = []string{reviseVerdict(codeOn("f1"))}
	_, err := r.run()
	var incomplete *ImplementerIncomplete
	if !errors.Is(err, errReviewCyclesExhausted) || errors.As(err, &incomplete) {
		t.Fatalf("a complete response set that did not converge was not ordinary non-convergence: %v", err)
	}
	if len(r.incompleteReports(t)) != 0 || r.reviews != 2 {
		t.Fatalf("a complete invocation was counted incomplete, or the reviews changed: reviews %d", r.reviews)
	}
}

// W10 PRODUCED-NOTHING / EVIDENCE DIAGNOSIS CONTROL. The existing diagnoses
// keep deciding their shapes, on the first invocation of the cycle: none of
// them is swallowed into an incomplete retry.
func TestDF41A3W10ExistingDiagnosesAreNotSwallowed(t *testing.T) {
	for name, tc := range map[string]struct{ findings, responses, want string }{
		"produced nothing":           {failingFirstFinding, ``, producedNothing},
		"evidence, not code":         {codeFinding + "," + failingFirstFinding, answerF2WithTheRun, producedEvidenceNotCode},
		"retained evidence for code": {codeFinding + "," + failingFirstFinding, `{"id":"f1","answered_by":"evidence","evidence":"ls witness_test.go"},` + answerF2WithTheRun, producedEvidenceNotCode},
	} {
		h, _ := evidenceLoop(t, tc.findings, tc.responses)
		_, err := h.runTwoCycles()
		var incomplete *ImplementerIncomplete
		if err == nil || !strings.Contains(err.Error(), tc.want) || errors.As(err, &incomplete) {
			t.Fatalf("%s: the existing diagnosis did not decide: %v", name, err)
		}
		if n := len(h.engine.settledInvocations("task-1")); n != 2 {
			t.Fatalf("%s: the diagnosed cycle was retried: %d invocations", name, n)
		}
	}
}

// structured is a stream-json observation with the given lifecycle.
func structured(report string, lifecycle ...agent.LifecycleObservation) *agent.Invocation {
	return &agent.Invocation{Transport: agent.Transport{Adapter: agent.AdapterStreamJSON, Lifecycle: true},
		Returned: true, Report: report, Exited: true, Lifecycle: lifecycle}
}

func started(seq int, op string) agent.LifecycleObservation {
	return agent.LifecycleObservation{Seq: seq, Kind: agent.LifecycleStarted, Operation: op}
}

func terminal(seq int, op string) agent.LifecycleObservation {
	return agent.LifecycleObservation{Seq: seq, Kind: agent.LifecycleTerminal, Operation: op}
}

// W11 THE SETTLED INVOCATION IS THE ONLY PROVIDER FACT. A report whose prose and
// embedded lines claim every operation finished, with a settled operation still
// open, is incomplete; a plain-text report whose prose claims an operation is
// still running, with nothing settled open, is complete.
func TestDF41A3W11TheSettledInvocationIsTheOnlyProviderFactSource(t *testing.T) {
	claimsDone := "all operations terminated\n" + `{"type":"tool_result","tool_use_id":"op-1"}` + "\n" + accounting(answerCode("f1"))
	r := newCompletionRig(t, []string{codeOn("f1")},
		incompleteTurn{value: 2, report: claimsDone, inv: structured(claimsDone, started(0, "op-1"))},
		incompleteTurn{value: 3, report: accounting(answerCode("f1")), inv: structured("done", started(0, "op-2"), terminal(1, "op-2"))},
	)
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("outcome %q err %v", outcome, err)
	}
	reports := r.incompleteReports(t)
	if len(reports) != 1 || fmt.Sprint(reports[0]["open_operations"]) != "[op-1#1]" || fmt.Sprint(reports[0]["owed_findings"]) != "[]" {
		t.Fatalf("the report's prose decided instead of the settled open operation: %v", reports)
	}

	claimsRunning := "operation op-9 is still running\n" + `{"type":"tool_use","id":"op-9"}` + "\n" + accounting(answerCode("f1"))
	r = newCompletionRig(t, []string{codeOn("f1")},
		incompleteTurn{value: 2, report: claimsRunning,
			inv: &agent.Invocation{Transport: agent.Transport{Adapter: agent.AdapterPlainText}, Returned: true, Report: claimsRunning, Exited: true}},
	)
	if outcome, err := r.run(); err != nil || !outcome.Accepted() || len(r.incompleteReports(t)) != 0 {
		t.Fatalf("prose about a running operation made a settled plain-text invocation incomplete: outcome %q err %v", outcome, err)
	}

	// An UNOBSERVED success -- the adapter handed back response-like text and
	// no observation that anything returned -- is not a return. Reaching the
	// success branch does not make it one: nothing it says is retained, it is
	// not counted as an attempt, and it is not retried. The pre-existing
	// one-shot accounting ends it, as it did before. The cycle it served keeps
	// the obligation established before it ran, untouched: everything owed.
	r = newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1")), unobserved: true},
		incompleteTurn{value: 3, report: accounting(answerCode("f2"))},
	)
	_, err = r.run()
	var incomplete *ImplementerIncomplete
	if err == nil || errors.As(err, &incomplete) || !strings.Contains(err.Error(), "did not converge") {
		t.Fatalf("an unobserved success was reconciled or retried as a return: %v", err)
	}
	if s := r.h.engine.settledInvocations("task-1"); len(s) != 2 || s[1].Returned || s[1].Observed {
		t.Fatalf("premise: the second invocation is not the one unobserved cycle-2 invocation: %+v", s)
	}
	if len(r.incompleteReports(t)) != 0 {
		t.Fatal("an unobserved success was counted as an incomplete attempt")
	}
	r.h.engine.mu.Lock()
	held := r.h.engine.completions["task-1"]
	r.h.engine.mu.Unlock()
	if held == nil || held.Cycle != 2 || held.Attempts != 0 || len(held.Retained) != 0 || !equalStrings(held.Owed(), "f1", "f2") {
		t.Fatalf("an unobserved success fed the completion obligation: %+v", held)
	}
}

// W12 STRUCTURED OPEN OPERATION. A partial response set with a mandatory
// operation still open is incomplete by the settled predicate; a later settled
// invocation that terminates its operations and answers the rest completes it.
func TestDF41A3W12AStructuredOpenOperationIsCompletedByALaterSettledInvocation(t *testing.T) {
	partial := accounting(answerCode("f1"))
	rest := accounting(answerCode("f2"))
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: partial, inv: structured(partial, started(0, "edit"))},
		incompleteTurn{value: 3, report: rest, inv: structured(rest, started(0, "edit"), terminal(1, "edit"))},
	)
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("outcome %q err %v", outcome, err)
	}
	reports := r.incompleteReports(t)
	if len(reports) != 1 || fmt.Sprint(reports[0]["open_operations"]) != "[edit#1]" || fmt.Sprint(reports[0]["owed_findings"]) != "[f2]" {
		t.Fatalf("the open operation and the owed finding are not the incomplete state: %v", reports)
	}
	if !strings.Contains(r.prompts[2], "structured operations still open: edit#1") {
		t.Fatalf("the retry was not told the settled open operation:\n%s", r.prompts[2])
	}
}

// W13 PLAIN-TEXT CONTROL. No lifecycle telemetry and a complete valid response
// set: complete on the first invocation.
func TestDF41A3W13APlainTextInvocationWithoutLifecycleIsNotIncomplete(t *testing.T) {
	report := accounting(answerCode("f1"))
	r := newCompletionRig(t, []string{codeOn("f1")},
		incompleteTurn{value: 2, report: report,
			inv: &agent.Invocation{Transport: agent.Transport{Adapter: agent.AdapterPlainText}, Returned: true, Report: report, Exited: true}},
	)
	if outcome, err := r.run(); err != nil || !outcome.Accepted() || len(r.incompleteReports(t)) != 0 {
		t.Fatalf("absence of lifecycle telemetry made an invocation incomplete: outcome %q err %v", outcome, err)
	}
}

// exhaustingRig owes f2 forever; a fifth turn exists so a fourth cycle-2
// invocation would be observable rather than a script error.
func exhaustingRig(t *testing.T, cycles int) *completionRig {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 3, report: "still working"},
		incompleteTurn{value: 4, report: accounting(`{"id":"f2","answered_by":"evidence","evidence":"gofmt"}`)},
		incompleteTurn{value: 5, report: accounting(answerCode("f2"))},
	)
	r.h.engine.Config.Workflow.ReviewCycles = cycles
	return r
}

// W14 RETRY BOUND. Three incomplete attempts in cycle 2: two retries, then no
// fourth invocation, the cycle never advances, and the typed outcome names
// cycle 2, three attempts and exactly f2. Through implement() and the
// production terminal it is not handed to another worker, it is not
// NOT_CONVERGED, and it rides the failed terminal's payload.
func TestDF41A3W14TheThirdIncompleteAttemptEndsTypedWithNoFourth(t *testing.T) {
	r := exhaustingRig(t, 2)
	_, err := r.run()
	var incomplete *ImplementerIncomplete
	if !errors.As(err, &incomplete) {
		t.Fatalf("the exhausted cycle did not end IMPLEMENTER_INCOMPLETE: %v", err)
	}
	if errors.Is(err, errReviewCyclesExhausted) {
		t.Fatalf("IMPLEMENTER_INCOMPLETE was collapsed into review-cycle exhaustion: %v", err)
	}
	if incomplete.State != ImplementerIncompleteState || incomplete.Cycle != 2 || incomplete.Attempts != 3 ||
		!equalStrings(incomplete.Owed, "f2") || !equalStrings(incomplete.Retained, "f1") ||
		incomplete.TaskID != "task-1" || incomplete.PlanAttemptID != r.h.engine.operativePlanAttempt("task-1").ID || incomplete.PlanAttemptID == "" {
		t.Fatalf("the typed outcome is not cycle 2, 3 attempts, f2 owed, bound to the task and attempt: %+v", incomplete)
	}
	requireExhaustedCheckpointCommitted(t, r.h.engine, "task-1", incomplete.PlanAttemptID)
	if !equalInts(r.cycles(), 1, 2, 2, 2) || r.reviews != 1 {
		t.Fatalf("a fourth invocation ran or the cycle advanced: cycles %v reviews %d", r.cycles(), r.reviews)
	}

	r = exhaustingRig(t, 2)
	second := r.h.worker
	second.Name = "gemini"
	r.h.engine.Config.Implementors = []config.Agent{r.h.worker, second}
	failed, events := runImplement(r.h)
	if !errors.As(failed, &incomplete) {
		t.Fatalf("implement() did not end on IMPLEMENTER_INCOMPLETE: %v", failed)
	}
	requireExhaustedCheckpointCommitted(t, r.h.engine, "task-1", incomplete.PlanAttemptID)
	if n := len(r.h.engine.settledInvocations("task-1")); n != 4 {
		t.Fatalf("the exhausted cycle was handed to another worker: %d invocations", n)
	}
	if contains(events, event.HandoffCreated) || contains(events, event.WorkflowNotConverged) {
		t.Fatalf("the exhausted cycle was handed off or reported not converged: %v", kinds(events))
	}
	terminateWith(t, r.h, failed)
	var payload ImplementerIncomplete
	for _, ev := range drainEvents(r.events) {
		if ev.Kind == event.WorkflowFailed {
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				t.Fatalf("the failed terminal does not carry the typed state: %v", err)
			}
		}
	}
	if payload.State != ImplementerIncompleteState || payload.Cycle != 2 || payload.Attempts != 3 ||
		!equalStrings(payload.Owed, "f2") || payload.TaskID != "task-1" || payload.PlanAttemptID == "" {
		t.Fatalf("the failed terminal's payload is not the typed IMPLEMENTER_INCOMPLETE state: %+v", payload)
	}
	requireExhaustedCheckpointCommitted(t, r.h.engine, "task-1", payload.PlanAttemptID)
}

// W15 THE BOUND IS NOT USER CONFIGURATION. A ten-cycle review budget leaves it
// at three, and no configuration field carries it.
func TestDF41A3W15TheRetryBoundIsAnInternalConstant(t *testing.T) {
	if maxIncompleteImplementerAttempts != 3 {
		t.Fatalf("the internal bound is %d, not 3", maxIncompleteImplementerAttempts)
	}
	r := exhaustingRig(t, 10)
	_, err := r.run()
	var incomplete *ImplementerIncomplete
	if !errors.As(err, &incomplete) || incomplete.Attempts != 3 || incomplete.MaxAttempts != 3 || !equalInts(r.cycles(), 1, 2, 2, 2) {
		t.Fatalf("the review-cycle budget reached the incomplete bound: %v (cycles %v)", err, r.cycles())
	}
	raw, err := json.Marshal(config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if lower := strings.ToLower(string(raw)); strings.Contains(lower, "incomplete") {
		t.Fatalf("a configuration field carries the incomplete bound: %s", raw)
	}
}

// W16 NO PROVIDER AVAILABLE. After an incomplete return the only implementer
// proves it cannot serve: the existing unavailable route ends the run, no
// provider is invented, no attempt is counted and no cycle is consumed.
//
// Then the error-bearing RETURN: the cycle's first invocation returns a valid
// answer to f1 and a provider-unavailable error together. What it returned is
// kept -- f1 retained by the canonical validator, f2 owed -- before the same
// ExternalBlock route ends the run, and it is not an incomplete attempt. A
// runner taking over in this process continues cycle 2 and is asked only for f2.
func TestDF41A3W16NoProviderAfterAnIncompleteReturnKeepsTheExistingBlock(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{err: quota()},
	)
	failed, _ := runImplement(r.h)
	var blocked *RoleUnavailable
	if !errors.As(failed, &blocked) || blocked.Role != roles.Implementer {
		t.Fatalf("the existing unavailable route did not end the run: %v", failed)
	}
	if !equalInts(r.cycles(), 1, 2, 2) || r.reviews != 1 {
		t.Fatalf("an invocation was invented or a cycle consumed: cycles %v reviews %d", r.cycles(), r.reviews)
	}
	if s := r.h.engine.settledInvocations("task-1"); s[2].Returned {
		t.Fatalf("premise: the unavailable invocation was settled as returned: %+v", s[2])
	}
	if c := r.live(t); c.Attempts != 1 || !equalStrings(c.Owed(), "f2") || c.Cycle != 2 || !c.Continuing {
		t.Fatalf("the unavailable provider counted as an incomplete attempt: %+v", c)
	}

	partial := accounting(answerCode("f1"))
	r = newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: partial, inv: plainReturn(partial), err: quota()},
		incompleteTurn{provider: "gemini", value: 3, report: accounting(answerCode("f2"))},
	)
	_, err := r.run()
	if !errors.As(err, &blocked) || blocked.Role != roles.Implementer {
		t.Fatalf("a returned error-bearing invocation did not keep the existing unavailable route: %v", err)
	}
	if s := r.h.engine.settledInvocations("task-1"); len(s) != 2 || !s[1].Returned || s[1].Err == nil {
		t.Fatalf("premise: the cycle-2 invocation is not a returned, error-bearing settled fact: %+v", s)
	}
	if len(r.incompleteReports(t)) != 0 {
		t.Fatal("a provider-unavailable return was counted as an incomplete attempt")
	}
	c := r.live(t)
	if c.Attempts != 0 || c.Cycle != 2 || !c.Continuing || !equalStrings(c.RetainedIDs(), "f1") || !equalStrings(c.Owed(), "f2") {
		t.Fatalf("what the error-bearing invocation returned was discarded, or it was counted: %+v", c)
	}
	next := r.h.worker
	next.Name = "gemini"
	outcome, _, _, _, err := r.h.engine.runCandidate(fixtureCtx(r.h.engine, "task-1"), r.h.sc, certifiedStart{},
		"task-1", r.h.tc, "Rewrite main.go so it prints a number.", next, r.h.work, "")
	if err != nil || !outcome.Accepted() {
		t.Fatalf("the rebound runner did not complete cycle 2: outcome %q err %v", outcome, err)
	}
	if !equalInts(r.cycles(), 1, 2, 2) {
		t.Fatalf("the rebound invocation did not serve cycle 2: %v", r.cycles())
	}
	if owed := owedLines(r.prompts[2]); !strings.Contains(owed, "- [f2]") || strings.Contains(owed, "[f1]") {
		t.Fatalf("the rebound runner was not asked for exactly f2:\n%s", r.prompts[2])
	}
}

// handedOver drives implement(), the production orchestration, with a second
// configured implementer, gemini, after the harness worker.
func (r *completionRig) handedOver() error {
	next := r.h.worker
	next.Name = "gemini"
	r.h.engine.Config.Implementors = []config.Agent{r.h.worker, next}
	failed, _ := runImplement(r.h)
	return failed
}

// W6/W7 REBOUND WITH NOTHING RETAINED. The cycle-2 invocation returns malformed
// accounting together with a provider-unavailable error: the canonical
// validator retains nothing, and the attempt is not counted. implement() then
// selects the next configured implementer in this process, and that one still
// serves cycle 2, owing exactly f1 and f2.
//
// Fails if continuation depends on a retained response: the next implementer
// would restart at cycle 1, its invocation would be settled as cycle 1, and the
// cycle-2 obligation would be replaced by a new one.
func TestDF41A3W7AReboundWithNothingRetainedKeepsTheCycleObligation(t *testing.T) {
	malformed := "finding_responses: f1 done, f2 done"
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: malformed, inv: plainReturn(malformed), err: quota()},
		incompleteTurn{provider: "gemini", value: 3, report: accounting(answerCode("f1"), answerCode("f2"))},
	)
	r.handedOver()
	settled := r.h.engine.settledInvocations("task-1")
	if len(settled) != 3 || !settled[1].Returned || settled[1].Err == nil || settled[2].Provider != "gemini" {
		t.Fatalf("premise: cycle 2 was not a returned unavailable invocation followed by gemini: %+v", settled)
	}
	if !equalInts(r.cycles(), 1, 2, 2) || r.reviews != 2 {
		t.Fatalf("the rebound restarted the cycle count or minted a review slot: cycles %v reviews %d", r.cycles(), r.reviews)
	}
	if len(r.incompleteReports(t)) != 0 {
		t.Fatal("a provider-unavailable return was counted as an incomplete attempt")
	}
	if owed := owedLines(r.prompts[2]); !strings.Contains(owed, "- [f1]") || !strings.Contains(owed, "- [f2]") {
		t.Fatalf("the rebound implementer was not asked for exactly the owed f1 and f2:\n%s", r.prompts[2])
	}
	if !strings.Contains(r.prompts[2], "Review cycle 2 is not complete") {
		t.Fatalf("the rebound implementer was not told it continues cycle 2:\n%s", r.prompts[2])
	}
	if c := r.stored(t); c.Cycle != 2 || c.ReviewAttempt != 1 || c.Attempts != 0 || c.Continuing || !equalStrings(c.RetainedIDs(), "f1", "f2") {
		t.Fatalf("the rebound replaced or rekeyed cycle 2's obligation: %+v", c)
	}
}

// HANDOFF AFTER A RETURNED ERROR. The cycle-2 invocation returns a valid f1 and
// an ordinary error -- not a provider unavailability. implement() records the
// failure and hands the candidate to the next configured implementer, which
// continues cycle 2: f1 stays retained, only f2 is asked for, and no review
// slot is spent. Ordinary errors have no counting exemption: the handoff of an
// incomplete obligation is incomplete attempt 1.
//
// Fails if continuation follows only the RoleUnavailable route: the next
// implementer would restart at cycle 1 and be asked for f1 again. Fails too if
// the handoff is not counted.
func TestDF41A3W6BHandoffAfterAReturnedErrorKeepsTheCycleObligation(t *testing.T) {
	partial := accounting(answerCode("f1"))
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: partial, inv: plainReturn(partial), err: errors.New("the worker crashed: exit status 2")},
		incompleteTurn{provider: "gemini", value: 3, report: accounting(answerCode("f2"))},
	)
	r.handedOver()
	settled := r.h.engine.settledInvocations("task-1")
	if len(settled) != 3 || !settled[1].Returned || settled[1].Err == nil || settled[2].Provider != "gemini" {
		t.Fatalf("premise: cycle 2 was not a returned error-bearing invocation followed by gemini: %+v", settled)
	}
	if roleUnavailable(roles.Implementer, "claude", settled[1].Err) != nil {
		t.Fatal("premise: the returned error is a provider unavailability, not an ordinary error")
	}
	if !equalInts(r.cycles(), 1, 2, 2) || r.reviews != 2 {
		t.Fatalf("the handoff restarted the cycle count or minted a review slot: cycles %v reviews %d", r.cycles(), r.reviews)
	}
	if reports := r.incompleteReports(t); len(reports) != 1 || reports[0]["route"] != string(routeErrorHandoff) ||
		fmt.Sprint(reports[0]["owed_findings"]) != "[f2]" {
		t.Fatalf("the incomplete returned-error handoff was not counted once: %v", reports)
	}
	if owed := owedLines(r.prompts[2]); !strings.Contains(owed, "- [f2]") || strings.Contains(owed, "[f1]") {
		t.Fatalf("the handed-over implementer was not asked for exactly f2:\n%s", r.prompts[2])
	}
	if c := r.stored(t); c.Cycle != 2 || c.ReviewAttempt != 1 || c.Attempts != 1 || c.Continuing || !equalStrings(c.RetainedIDs(), "f1", "f2") {
		t.Fatalf("the handoff replaced or rekeyed cycle 2's obligation: %+v", c)
	}
}

// configure makes the harness worker the first of the named implementers.
func (r *completionRig) configure(names ...string) {
	workers := []config.Agent{r.h.worker}
	for _, name := range names {
		next := r.h.worker
		next.Name = name
		workers = append(workers, next)
	}
	r.h.engine.Config.Implementors = workers
}

// W14 THROUGH THE HANDOFF. Four implementers are configured. Cycle 2's
// invocation returns a valid f1 with an ordinary error, and so do the next two
// implementers the handoff selects for the same cycle, answering nothing more.
// Each handoff of the incomplete obligation is counted; the third ends the run
// with typed IMPLEMENTER_INCOMPLETE -- cycle 2, three attempts, f2 owed -- and
// the fourth implementer is never invoked.
//
// Fails if ordinary-error handoffs are exempt from the bound: the fourth
// implementer would be invoked for cycle 2 with no attempt ever counted.
func TestDF41A3W14BThreeIncompleteErrorHandoffsStopBeforeAFourthImplementer(t *testing.T) {
	partial := accounting(answerCode("f1"))
	crash := func(provider string, value int) incompleteTurn {
		return incompleteTurn{provider: provider, value: value, report: partial, inv: plainReturn(partial),
			err: errors.New("the worker crashed: exit status 2")}
	}
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		crash("", 2), crash("gemini", 3), crash("codex", 4),
		incompleteTurn{provider: "fourth", value: 5, report: accounting(answerCode("f2"))},
	)
	r.configure("gemini", "codex", "fourth")
	failed, _ := runImplement(r.h)
	var incomplete *ImplementerIncomplete
	if !errors.As(failed, &incomplete) {
		t.Fatalf("three incomplete error handoffs did not end typed: %v", failed)
	}
	if incomplete.State != ImplementerIncompleteState || incomplete.Cycle != 2 || incomplete.Attempts != 3 ||
		!equalStrings(incomplete.Owed, "f2") || !equalStrings(incomplete.Retained, "f1") {
		t.Fatalf("the typed outcome does not name cycle 2, three attempts and f2: %+v", incomplete)
	}
	if r.next != 4 || !equalInts(r.cycles(), 1, 2, 2, 2) || r.reviews != 1 {
		t.Fatalf("a fourth implementer was invoked or a cycle consumed: turns %d cycles %v reviews %d", r.next, r.cycles(), r.reviews)
	}
	reports := r.incompleteReports(t)
	if len(reports) != 3 {
		t.Fatalf("expected three counted handoffs, got %v", reports)
	}
	for i, rep := range reports {
		if rep["route"] != string(routeErrorHandoff) || fmt.Sprint(rep["attempts"]) != fmt.Sprint(i+1) {
			t.Fatalf("handoff %d was not counted as attempt %d: %v", i+1, i+1, rep)
		}
	}
	if c := r.stored(t); c.Continuing || c.Attempts != 3 {
		t.Fatalf("the exhausted cycle is still live for another implementer: %+v", c)
	}
}

// W10 THROUGH THE HANDOFF. Produced-nothing and produced-evidence-not-code end
// the worker's run with their existing diagnoses, and implement() hands the
// candidate to the next implementer exactly as before. The diagnosis route
// leaves no live incomplete continuation: the next implementer starts its own
// run at cycle 1, is not told it continues an incomplete cycle, and nothing is
// counted.
//
// Fails if absorbing the account selects the route: the cycle would be left
// continuing, and the next implementer would silently resume cycle 2 as an
// uncounted IMPLEMENTER_INCOMPLETE retry.
func TestDF41A3W10BDiagnosesLeaveNoIncompleteContinuationForTheHandoff(t *testing.T) {
	for name, tc := range map[string]struct{ findings, responses, want string }{
		"produced nothing":   {failingFirstFinding, ``, producedNothing},
		"evidence, not code": {codeFinding + "," + failingFirstFinding, answerF2WithTheRun, producedEvidenceNotCode},
	} {
		h, _ := evidenceLoop(t, tc.findings, tc.responses)
		events, cancel := h.engine.Bus.Subscribe(8192)
		next := h.worker
		next.Name = "gemini"
		h.engine.Config.Implementors = []config.Agent{h.worker, next}
		runImplement(h)
		seen := drainEvents(events)
		cancel()
		diagnosed := false
		for _, ev := range seen {
			if ev.Kind == event.HandoffCreated && strings.Contains(string(ev.Payload), tc.want) {
				diagnosed = true
			}
			var p struct {
				Incomplete *struct {
					Cycle int `json:"review_cycle"`
				} `json:"implementer_incomplete"`
			}
			if json.Unmarshal(ev.Payload, &p) == nil && p.Incomplete != nil && p.Incomplete.Cycle == 2 {
				t.Fatalf("%s: the diagnosed cycle was counted as an incomplete attempt: %s", name, ev.Payload)
			}
		}
		if !diagnosed {
			t.Fatalf("%s: the existing diagnosis did not decide the handoff", name)
		}
		settled := h.engine.settledInvocations("task-1")
		if len(settled) < 3 || settled[1].Cycle != 2 || settled[2].Provider != "gemini" || settled[2].Cycle != 1 {
			t.Fatalf("%s: the next implementer resumed the diagnosed cycle: %+v", name, settled)
		}
	}
}

// W6/W7 COMPLETE-RESPONSE REBOUND. Cycle 2's structured invocation answers
// every finding but leaves an operation open, and its provider proves
// unavailable. The cycle is not complete -- it never reached validation or
// review -- so it stays live for the next provider although no finding is
// owed: gemini serves cycle 2 itself, is told f1 and f2 are satisfied and owed
// nothing, and is told the settled open operation. Nothing is counted.
//
// Fails if liveness follows the owed set: with nothing owed the rebound would
// restart at cycle 1 and replace cycle 2's obligation.
func TestDF41A3W7BACompleteResponseReboundKeepsTheCycleAndItsContext(t *testing.T) {
	complete := accounting(answerCode("f1"), answerCode("f2"))
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: complete, inv: structured(complete, started(0, "a")), err: quota()},
		incompleteTurn{provider: "gemini", value: 3, report: "done"},
	)
	r.configure("gemini")
	if failed, _ := runImplement(r.h); failed != nil {
		t.Fatalf("the rebound did not complete cycle 2: %v", failed)
	}
	settled := r.h.engine.settledInvocations("task-1")
	if len(settled) != 3 || !settled[1].Returned || settled[1].Err == nil || settled[2].Provider != "gemini" {
		t.Fatalf("premise: cycle 2 was not a returned unavailable invocation followed by gemini: %+v", settled)
	}
	if !equalInts(r.cycles(), 1, 2, 2) || r.reviews != 2 {
		t.Fatalf("the rebound restarted the cycle count or minted a review slot: cycles %v reviews %d", r.cycles(), r.reviews)
	}
	prompt := r.prompts[2]
	if !strings.Contains(prompt, "Review cycle 2 is not complete") || owedLines(prompt) != "" ||
		!strings.Contains(prompt, "No review finding is still owed") ||
		!strings.Contains(prompt, "need not be restated: f1, f2") ||
		!strings.Contains(prompt, "structured operations still open: a#1") {
		t.Fatalf("the rebound was not given cycle 2, its satisfied responses, nothing owed and the open operation:\n%s", prompt)
	}
	if len(r.incompleteReports(t)) != 0 {
		t.Fatal("a provider rebound was counted as an incomplete attempt")
	}
	if c := r.stored(t); c.Cycle != 2 || c.ReviewAttempt != 1 || c.Attempts != 0 || c.Continuing || c.Route != routeProgress ||
		!equalStrings(c.RetainedIDs(), "f1", "f2") {
		t.Fatalf("the rebound replaced or rekeyed cycle 2's obligation: %+v", c)
	}
}

// RETURNED-ERROR RECONCILIATION KEEPS AN UNJUDGED SIBLING. Cycle 2's first
// invocation validly answers f1 alone and is retried. The retry returns a valid
// f2 together with an error -- an ordinary one, or a provider unavailability --
// so only f2 is judged; f1, retained and contradicted by nothing, is not. The
// next implementer then continues cycle 2 without restating anything, and the
// cycle completes: f1's retained response is the one invocation 1 gave, byte
// for byte.
//
// Fails if reconciling the returned error rewrites findings it did not judge:
// f1 would be replaced by an empty envelope, the continuing invocation would be
// asked for f1 again, and canonical accounting would reopen it.
func TestDF41A3W4BReturnedErrorReconciliationKeepsAnUnjudgedRetainedSibling(t *testing.T) {
	want, _ := parseFindingResponses(accounting(answerCode("f1")))
	for name, failure := range map[string]error{
		"ordinary error":       errors.New("the worker crashed: exit status 2"),
		"provider unavailable": quota(),
	} {
		t.Run(name, func(t *testing.T) {
			second := accounting(answerCode("f2"))
			r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
				incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
				incompleteTurn{value: 3, report: second, inv: plainReturn(second), err: failure},
				incompleteTurn{provider: "gemini", value: 4, report: "done"},
			)
			r.configure("gemini")
			if failed, _ := runImplement(r.h); failed != nil {
				t.Fatalf("the continuing implementer did not complete cycle 2: %v", failed)
			}
			if !equalInts(r.cycles(), 1, 2, 2, 2) || r.reviews != 2 {
				t.Fatalf("premise: cycle 2 was not served three times and reviewed once: cycles %v reviews %d", r.cycles(), r.reviews)
			}
			if prompt := r.prompts[3]; owedLines(prompt) != "" || !strings.Contains(prompt, "need not be restated: f1, f2") {
				t.Fatalf("the continuing implementer was not told f1 and f2 are satisfied and nothing is owed:\n%s", prompt)
			}
			c := r.stored(t)
			if len(want) != 1 || !sameResponse(c.Retained["f1"], want[0]) {
				t.Fatalf("f1's retained response was rewritten by a reconciliation that did not judge it: %+v", c.Retained["f1"])
			}
			if c.Cycle != 2 || c.Attempts != 1 || !equalStrings(c.RetainedIDs(), "f1", "f2") {
				t.Fatalf("the completed obligation is not cycle 2, one attempt, f1 and f2 retained: %+v", c)
			}
		})
	}
}

// W17 LANDED 70A1 COMPOSITION. Malformed accounting retains nothing, and a
// mis-classed answer is not retained: retention is exactly what the canonical
// validator discharged.
func TestDF41A3W17RetentionComesOnlyFromTheCanonicalValidator(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: `{"finding_responses":{"id":"f1","answered_by":"code","paths":["main.go"]}}`},
		incompleteTurn{value: 3, report: accounting(`{"id":"f1","answered_by":"evidence","evidence":"gofmt"}`, answerCode("f2"))},
		incompleteTurn{value: 4, report: accounting(answerCode("f1"))},
	)
	outcome, err := r.run()
	if err != nil || !outcome.Accepted() {
		t.Fatalf("outcome %q err %v", outcome, err)
	}
	reports := r.incompleteReports(t)
	if len(reports) != 2 ||
		fmt.Sprint(reports[0]["retained_findings"]) != "[]" || fmt.Sprint(reports[0]["owed_findings"]) != "[f1 f2]" ||
		fmt.Sprint(reports[1]["retained_findings"]) != "[f2]" || fmt.Sprint(reports[1]["owed_findings"]) != "[f1]" {
		t.Fatalf("retention is not exactly what the canonical validator discharged: %v", reports)
	}
	if _, perr := parseFindingResponses(`{"finding_responses":{"id":"f1"}}`); perr == nil {
		t.Fatal("premise: the landed validator accepts a non-array accounting")
	}
}

// W18 LANDED 70A2 COMPOSITION. The obligation reads the settled invocations the
// engine recorded, one per invocation, and its open operations are exactly the
// settled fact's.
func TestDF41A3W18TheObligationConsumesTheSettledInvocationsDirectly(t *testing.T) {
	partial := accounting(answerCode("f1"))
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: partial, inv: structured(partial, started(0, "a"), started(1, "a"), terminal(2, "a"))},
		incompleteTurn{provider: "gemini", value: 3, report: accounting(answerCode("f2"))},
	)
	if outcome, err := r.run(); err != nil || !outcome.Accepted() {
		t.Fatalf("outcome %q err %v", outcome, err)
	}
	settled := r.h.engine.settledInvocations("task-1")
	if len(settled) != 3 {
		t.Fatalf("expected one settled fact per invocation, got %d", len(settled))
	}
	reports := r.incompleteReports(t)
	if len(reports) != 1 || reports[0]["provider"] != settled[1].Provider ||
		fmt.Sprint(reports[0]["open_operations"]) != fmt.Sprint(openOperations(settled[1])) {
		t.Fatalf("the incomplete attempt is not the settled invocation's fact: %v vs %+v", reports, settled[1])
	}
	// The second start of "a" is a new generation, and the terminal closes that
	// one: the first is left open exactly as 70A2 settled it, not as a reading
	// of the stream's last word for "a" would have it.
	if !equalStrings(openOperations(settled[1]), "a#1") || fmt.Sprint(reports[0]["open_operations"]) != "[a#1]" {
		t.Fatalf("the open operation is not the settled first generation: %+v", settled[1].Operations)
	}
}

// C3 (RULING-246): SAME-CYCLE CANDIDATE IDENTITY AT THE RETRY DECISION. An
// incomplete attempt's candidate is compared with the immediately preceding
// attempt of the SAME review cycle before retry accounting advances, never with
// the previous review cycle's candidate. The identity compared is the
// engine-computed diff digest each account step records; what the implementer
// says about its candidate is never read.

// c3AnswerF3 cites a check no broker ever executes: f3 stays owed forever.
const c3AnswerF3 = `{"id":"f3","answered_by":"evidence","evidence":"ls other_test.go"}`

type c3Attempt struct {
	digest string
	route  cycleRoute
}

// c3Attempts pairs each account step's candidate digest with the route the
// engine selected after it, from the task's stored obligation.
func c3Attempts(t *testing.T, r *completionRig) (*cycleCompletion, []c3Attempt) {
	t.Helper()
	c := r.stored(t)
	var out []c3Attempt
	for i, s := range c.Steps {
		if s.Kind != stepAccount {
			continue
		}
		if s.Evidence == nil || s.Evidence.DiffDigest == "" {
			t.Fatalf("premise: account step %d carries no candidate identity: %+v", i, s)
		}
		a := c3Attempt{digest: s.Evidence.DiffDigest}
		if i+1 < len(c.Steps) && c.Steps[i+1].Kind == stepRoute {
			a.route = c.Steps[i+1].Route
		}
		out = append(out, a)
	}
	return c, out
}

// c3Identity is the engine's same-cycle classification of one accounted
// attempt at the retry decision: a counted attempt carries it on its
// incomplete-attempt record, and an attempt that took the no-progress
// diagnosis carries it in that diagnosis.
type c3Identity struct {
	classification string
	digest         string
	stalled        bool
	// preceding is the same-cycle predecessor compared with, when reported.
	preceding *string
}

const c3IdentityMarker = "Same-cycle candidate identity: "

func c3Identities(t *testing.T, r *completionRig, attempts []c3Attempt, terminal error) []c3Identity {
	t.Helper()
	var out []c3Identity
	for _, rep := range r.incompleteReports(t) {
		class, _ := rep["candidate_identity"].(string)
		digest, _ := rep["candidate_digest"].(string)
		stalled, _ := rep["unchanged_since_previous_cycle"].(bool)
		preceding, _ := rep["preceding_attempt_digest"].(string)
		out = append(out, c3Identity{classification: class, digest: digest, stalled: stalled, preceding: &preceding})
	}
	if n := len(attempts); n != 0 && attempts[n-1].route == routeDiagnosis && terminal != nil {
		msg := terminal.Error()
		if i := strings.Index(msg, c3IdentityMarker); i >= 0 {
			rest := msg[i+len(c3IdentityMarker):]
			class, tail, _ := strings.Cut(rest, ";")
			out = append(out, c3Identity{classification: class, digest: attempts[n-1].digest,
				stalled: strings.Contains(tail, "unchanged since the previous review cycle: true")})
		}
	}
	return out
}

// c3Classified asserts the engine classified each accounted attempt as want,
// in order, from the same certified digest the attempt's account step records.
func c3Classified(t *testing.T, r *completionRig, attempts []c3Attempt, terminal error, want ...string) []c3Identity {
	t.Helper()
	got := c3Identities(t, r, attempts, terminal)
	if len(got) != len(want) || len(attempts) != len(want) {
		t.Fatalf("classified %d attempts and accounted %d, want %d: %+v", len(got), len(attempts), len(want), got)
	}
	for i, id := range got {
		if id.classification != want[i] {
			t.Fatalf("attempt %d was classified %q against its same-cycle predecessor, want %q: %+v", i+1, id.classification, want[i], got)
		}
		if id.digest == "" || id.digest != attempts[i].digest {
			t.Fatalf("attempt %d was classified from %q, not its certified candidate %q", i+1, id.digest, attempts[i].digest)
		}
		if id.preceding != nil {
			want := ""
			if i > 0 {
				want = attempts[i-1].digest
			}
			if *id.preceding != want {
				t.Fatalf("attempt %d was compared with %q, not its same-cycle predecessor %q", i+1, *id.preceding, want)
			}
		}
	}
	return got
}

// producedNothingThisCycle is the no-progress diagnosis for consecutive
// attempts of one review cycle, distinct from producedNothing between cycles.
const producedNothingThisCycle = "consecutive attempts of the same review cycle produced an identical candidate"

// c3Rig: "f3 alone" is an undischargeable evidence finding by itself; "r3
// shape" adds a dischargeable evidence finding (f2, the broker's failing-first
// witness) beside it. values are the cycle-2 attempts' main.go values.
func c3Rig(t *testing.T, shape string, values ...int) *completionRig {
	t.Helper()
	findings := []string{secondEvidenceFinding}
	report := accounting(c3AnswerF3)
	if shape == "r3 shape" {
		findings = []string{failingFirstFinding, secondEvidenceFinding}
		report = accounting(answerF2WithTheRun, c3AnswerF3)
	}
	var turns []incompleteTurn
	for _, v := range values {
		turns = append(turns, incompleteTurn{value: v, report: report})
	}
	r := newCompletionRig(t, findings, turns...)
	if shape == "r3 shape" {
		r.h.engine.Config.Permissions.RunTests = true
		r.h.engine.Config.Validation.Test = []config.Command{{Command: "grep", Args: []string{"-c", "main() {}", "main.go"}}}
	}
	return r
}

// W1a THE f3-ONLY SHAPE. Cycle 2 attempt 1 writes a new candidate (value 2);
// attempt 2 leaves the worktree alone, so its candidate is byte-identical to
// attempt 1's, its owed set is still [f3] and it retains no evidence. That is
// recognized as identical before another retry is counted: attempt 2 takes the
// existing no-progress diagnosis, the obligation holds one counted attempt, and
// f3 is still owed -- identity is neither acceptance nor discharge.
//
// Fails at a8add85 because attempt 2 is compared with cycle 1's candidate,
// which it differs from, and is counted as a second incomplete retry.
func TestC3W1aAnIdenticalSameCycleAttemptIsRecognizedBeforeRetryAccountingAdvances(t *testing.T) {
	r := c3Rig(t, "f3 alone", 2, 0, 0, 0)
	outcome, err := r.run()
	c, attempts := c3Attempts(t, r)
	for i, a := range attempts {
		t.Logf("cycle-%d attempt %d: candidate %s -> route %s", c.Cycle, i+1, a.digest, a.route)
	}
	if len(attempts) < 2 {
		t.Fatalf("premise: fewer than two accounted cycle-2 attempts (%d): %v", len(attempts), err)
	}
	if attempts[0].route != routeIncompleteRetry {
		t.Fatalf("premise: attempt 1, a changed candidate with f3 owed, was not an incomplete retry: %s", attempts[0].route)
	}
	if attempts[1].digest != attempts[0].digest {
		t.Fatalf("premise: attempt 2's candidate %s is not attempt 1's %s", attempts[1].digest, attempts[0].digest)
	}
	if c.counts(attempts[1].route) || c.Attempts != 1 {
		t.Fatalf("attempt 2 reproduced attempt 1's candidate and was counted (route %s, %d attempts): "+
			"the identity was not recognized before accounting (not recognized as identical before accounting); terminal %v", attempts[1].route, c.Attempts, err)
	}
	if len(attempts) != 2 || attempts[1].route != routeDiagnosis {
		t.Fatalf("the identical attempt did not take the no-progress diagnosis: %+v", attempts)
	}
	c3Classified(t, r, attempts, err, "unknown", "identical")
	var incomplete *ImplementerIncomplete
	if err == nil || errors.As(err, &incomplete) || !strings.Contains(err.Error(), producedNothingThisCycle) ||
		strings.Contains(err.Error(), producedEvidenceNotCode) {
		t.Fatalf("the identical attempt did not end on the same-cycle produced-nothing diagnosis: %v", err)
	}
	// Attempt 1 changed the candidate relative to cycle 1, so the stall is
	// between consecutive attempts, not between review cycles: the diagnosis
	// must not claim what its own structured fact denies.
	if !strings.Contains(err.Error(), "unchanged since the previous review cycle: false") ||
		strings.Contains(err.Error(), producedNothing) {
		t.Fatalf("a same-cycle repetition was diagnosed as a stall between review cycles: %v", err)
	}
	if outcome.Accepted() || !equalStrings(c.Owed(), "f3") {
		t.Fatalf("an identical candidate was read as acceptance or discharge: outcome %q owed %v", outcome, c.Owed())
	}
	if !equalInts(r.cycles(), 1, 2, 2) {
		t.Fatalf("an invocation ran after the identity was recognized: %v", r.cycles())
	}
}

// W2 CONTROL. Every cycle-2 attempt writes a NEW candidate (values 2, 3, 4):
// each is recognized as changed, counted, and the third ends
// IMPLEMENTER_INCOMPLETE -- the landed DF-41A3 behaviour C3 must not change.
func TestC3W2AChangedCandidateIsRecognizedAsChanged(t *testing.T) {
	for _, shape := range []string{"f3 alone", "r3 shape"} {
		t.Run(shape, func(t *testing.T) {
			r := c3Rig(t, shape, 2, 3, 4, 5)
			_, err := r.run()
			_, attempts := c3Attempts(t, r)
			var incomplete *ImplementerIncomplete
			if !errors.As(err, &incomplete) || incomplete.Attempts != 3 || !equalStrings(incomplete.Owed, "f3") {
				t.Fatalf("three changed incomplete candidates did not end IMPLEMENTER_INCOMPLETE owing f3 after 3 attempts: %v", err)
			}
			if strings.Contains(err.Error(), producedNothing) {
				t.Fatalf("a changed candidate was diagnosed as unchanged: %v", err)
			}
			if len(attempts) != 3 {
				t.Fatalf("expected three accounted cycle-2 attempts, got %d", len(attempts))
			}
			for i, a := range attempts {
				if a.route != routeIncompleteRetry {
					t.Fatalf("changed attempt %d was not counted as an incomplete retry: %s", i+1, a.route)
				}
				if i > 0 && a.digest == attempts[i-1].digest {
					t.Fatalf("premise: attempt %d did not change the candidate", i+1)
				}
			}
			if !equalInts(r.cycles(), 1, 2, 2, 2) {
				t.Fatalf("a fourth invocation ran or the cycle advanced: %v", r.cycles())
			}
			c3Classified(t, r, attempts, err, "unknown", "changed", "changed")
		})
	}
}

// W4 THE BOUNDED RETRY STANDS FOR A CODE FINDING. Every cycle-2 attempt leaves
// the candidate byte-identical while a blocking code finding stays unresolved:
// the implementer still owes a change, so identity is not a diagnosis and not a
// discharge. Each attempt is counted, and the third ends IMPLEMENTER_INCOMPLETE
// owing f1.
func TestC3W4AnUnchangedCandidateWithAnOpenCodeFindingKeepsTheBoundedRetry(t *testing.T) {
	report := accounting(answerCode("f1"))
	r := newCompletionRig(t, []string{codeOn("f1")},
		incompleteTurn{report: report}, incompleteTurn{report: report},
		incompleteTurn{report: report}, incompleteTurn{report: report})
	outcome, err := r.run()
	c, attempts := c3Attempts(t, r)
	// Each attempt's route is read before the count: an attempt that is not
	// an incomplete retry is the failure W4 exists to name.
	for i, a := range attempts {
		if a.route != routeIncompleteRetry {
			t.Fatalf("attempt %d with f1 still owing a change left the bounded retry: route %s; terminal %v", i+1, a.route, err)
		}
		if i > 0 && a.digest != attempts[i-1].digest {
			t.Fatalf("premise: attempt %d changed the candidate", i+1)
		}
	}
	if len(attempts) != 3 {
		t.Fatalf("f1 left the bounded retry after %d accounted cycle-2 attempts, want 3: %+v: %v", len(attempts), attempts, err)
	}
	var incomplete *ImplementerIncomplete
	if !errors.As(err, &incomplete) || incomplete.Attempts != 3 || !equalStrings(incomplete.Owed, "f1") {
		t.Fatalf("the open code finding did not end IMPLEMENTER_INCOMPLETE owing f1 after 3 attempts: %v", err)
	}
	if strings.Contains(err.Error(), producedNothing) || outcome.Accepted() || !equalStrings(c.Owed(), "f1") {
		t.Fatalf("an identical candidate discharged or diagnosed an open code finding: outcome %q owed %v err %v", outcome, c.Owed(), err)
	}
	if !equalInts(r.cycles(), 1, 2, 2, 2) {
		t.Fatalf("a fourth invocation ran or the cycle advanced: %v", r.cycles())
	}
	c3Classified(t, r, attempts, err, "unknown", "identical", "identical")
}

// W6L THE COMPARISON IS THREE-VALUED. Two established identities compare equal
// or changed; when either is absent -- an error-return account carries no
// digest, a fresh process holds no previous attempt -- the answer is UNKNOWN,
// never equality.
func TestC3W6LAnAbsentIdentityIsUnknownNeverEqual(t *testing.T) {
	const a, b = "sha256:aaaa", "sha256:bbbb"
	for _, tc := range []struct {
		previous, current string
		want              candidateIdentity
	}{
		{a, a, candidateIdentical},
		{a, " " + a + "\n", candidateIdentical},
		{a, b, candidateChanged},
		{"", a, candidateIdentityUnknown},
		{a, "", candidateIdentityUnknown},
		{"", "", candidateIdentityUnknown},
		{"  ", "  ", candidateIdentityUnknown},
	} {
		if got := compareCandidateIdentity(tc.previous, tc.current); got != tc.want {
			t.Fatalf("compare(%q, %q) = %v, want %v", tc.previous, tc.current, got, tc.want)
		}
	}
}

// W6L AT THE RETRY DECISION. The first attempt of cycle 2 has no same-cycle
// predecessor, so its classification is UNKNOWN even though its candidate is
// byte-identical to cycle 1's: the previous cycle's candidate is never read as
// the preceding attempt. That cross-cycle identity is still observed, as its
// own fact, and keeps the existing no-progress diagnosis for a candidate that
// did not move between cycles.
func TestC3W6LTheFirstAttemptOfACycleHasNoSameCyclePredecessor(t *testing.T) {
	r := c3Rig(t, "f3 alone", 0, 0)
	_, err := r.run()
	_, attempts := c3Attempts(t, r)
	if len(attempts) != 1 || attempts[0].route != routeDiagnosis {
		t.Fatalf("premise: the unmoved first attempt of cycle 2 did not take the existing stall diagnosis: %+v: %v", attempts, err)
	}
	if err == nil || !strings.Contains(err.Error(), producedNothing) {
		t.Fatalf("premise: the unmoved first attempt did not end produced-nothing: %v", err)
	}
	got := c3Classified(t, r, attempts, err, "unknown")
	if !got[0].stalled {
		t.Fatalf("the cross-cycle identity was not kept as its own fact: %+v", got[0])
	}
}

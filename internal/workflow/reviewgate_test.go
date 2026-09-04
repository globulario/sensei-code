package workflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/sensei"
)

// The positive control for the gate.
//
// An earlier revision of this slice returned the review's standing and then
// dropped it -- `_ = advisory` at both call sites -- so an advisory ACCEPT
// walked straight into Accepted, minted, and offered a pull request while the
// record said "independent review not established". The transcript and the
// state machine disagreed, and the state machine won.
//
// Testing that the obligation is REMEMBERED does not catch that. These tests
// run the real loop, over a real worktree, with a real diff and a real audit,
// and assert what the loop CONCLUDED.

// roleResolver answers the reviewer turn with a stub and leaves every other
// role on the ordinary command-line path, which is the shape a delegating
// deployment actually has.
type roleResolver struct {
	reviewer agent.Runner
	name     string
	session  string
}

func (r roleResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	if spec.Role == roles.Reviewer {
		return Resolved{Runner: r.reviewer, Name: r.name, Label: r.name}, nil
	}
	return CLIResolved(spec, r.session), nil
}

// gateHarness builds the same fixture the formatter test uses: a real
// repository, a real candidate worktree, stub implementor and Sensei processes.
type gateHarness struct {
	engine *Engine
	sc     *sensei.Client
	tc     *taskContext
	work   string
	worker config.Agent
	// workerSaw is where the implementor stub writes the prompt it received.
	workerSaw string
	events    <-chan event.Event
}

func newGateHarness(t *testing.T, policy roles.Policy, mode roles.Session, decision string) *gateHarness {
	t.Helper()
	requireGofmt(t)
	ctx := context.Background()

	repo, base := mintRepo(t)
	workspace, err := repo.CreateWorktreeAt(ctx, "task-1", base)
	if err != nil {
		t.Fatalf("create the candidate worktree: %v", err)
	}
	record := t.TempDir()
	workerSaw := filepath.Join(record, "worker-prompt.txt")
	implCommand, implArgs := stubProcess(t, "implementor", workerSaw)
	senseiCommand, senseiArgs := stubProcess(t, "sensei", filepath.Join(record, "audited-diff.txt"))
	sc, err := sensei.Start(ctx, repo.Root, senseiCommand, senseiArgs)
	if err != nil {
		t.Fatalf("start the stub Sensei MCP: %v", err)
	}
	t.Cleanup(func() { sc.Close() })

	payload := map[string]any{"decision": decision, "summary": "the candidate stands"}
	if decision == string(roles.Revise) {
		// A revise with nothing to act on is refused by validation, and rightly:
		// a cycle spent on an unactionable objection produces a byte-identical
		// diff. The point of this case is the ROUTE, so give it a real finding.
		payload["summary"] = "the proof is missing"
		payload["findings"] = []map[string]any{{"id": "1", "severity": "blocking",
			"claim": "the test does not fail without the fix", "reference": "main.go", "reason": "no mutation"}}
	}
	verdict, _ := json.Marshal(payload)
	worker := config.Agent{Name: "claude", Command: implCommand, Args: implArgs, Graph: "none"}

	bus := event.NewBus()
	events, cancel := bus.Subscribe(512)
	t.Cleanup(cancel)

	e := &Engine{Repo: repo, SessionID: "session-1", Bus: bus}
	e.Config.Permissions = config.Permissions{
		ReadRepository: true, WriteCandidates: true, CreateWorktrees: true,
		RunFormatters: true, LocalCommit: true,
	}
	e.Config.Workflow.ReviewCycles = 1
	e.Config.Validation = formattingValidation()
	e.Config.Implementors = []config.Agent{worker}
	// The roster still names a configured reviewer; the resolver substitutes
	// the answering runner, exactly as a delegating deployment does.
	e.Config.Reviewer = config.Agent{Name: "codex", Command: "true", Graph: "none"}
	e.Runners = roleResolver{
		reviewer: answeringRunner{text: string(verdict), mode: mode},
		name:     "remote:abc", session: "session-1",
	}
	e.setRouting("task-1", policy, sensei.PreflightDecision{}, nil, nil)
	e.beginReceipt("task-1")

	return &gateHarness{
		engine: e, sc: sc, work: workspace, worker: worker, workerSaw: workerSaw,
		events: events,
		tc: &taskContext{
			Task: "print a number from main", Mode: ModeModify,
			Files: []string{"main.go"}, Identity: candidateIdentityWithBase(base),
		},
	}
}

func (h *gateHarness) run(t *testing.T) candidateOutcome {
	t.Helper()
	outcome, _, _, _, err := h.engine.runCandidate(context.Background(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	if err != nil {
		t.Fatalf("the governed candidate loop failed: %v", err)
	}
	return outcome
}

func requiresIndependentReview() roles.Policy {
	return roles.Policy{CrossProviderReview: true, Reason: "blast radius system with approval gate architect"}
}

// HIGH RISK. The reviewer accepts, its isolation was never established, and the
// task's measured risk requires an independent look. The candidate stands and
// nothing may proceed on its behalf.
func TestAnAdvisoryAcceptDoesNotUnlockAcceptanceOnAHighRiskTask(t *testing.T) {
	h := newGateHarness(t, requiresIndependentReview(), roles.Unverified, "accept")

	outcome := h.run(t)
	if outcome.Accepted() {
		t.Fatal("an advisory accept unlocked the acceptance path on a task requiring independent review")
	}
	if outcome != candidateAwaitingIndependentReview {
		t.Fatalf("the loop concluded %q; the candidate is intact and the review obligation is open", outcome)
	}
	// Neither a success nor an implementation failure: nothing about the
	// candidate needs revising, so the worker must not be sent back.
	if outcome == candidateNotConverged {
		t.Fatal("a candidate with nothing wrong with it was reported as a worker failure")
	}
	// The obligation is durable rather than only spoken.
	open := h.engine.AdvisoryObligations("task-1")
	if len(open) == 0 {
		t.Fatal("the unmet obligation was not recorded")
	}
	if !strings.Contains(open[0], "has not had one") {
		t.Fatalf("the obligation does not say what is missing: %q", open[0])
	}
}

// The other half of the control: an OBSERVED independent review of the same
// candidate, in the same fixture, does unlock acceptance. Without this the test
// above would pass for a loop that never accepts anything.
func TestAnObservedIndependentReviewOfTheSameCandidateUnlocksAcceptance(t *testing.T) {
	h := newGateHarness(t, requiresIndependentReview(), roles.Fresh, "accept")

	if outcome := h.run(t); !outcome.Accepted() {
		t.Fatalf("an observed independent accept concluded %q", outcome)
	}
	if open := h.engine.AdvisoryObligations("task-1"); len(open) != 0 {
		t.Fatalf("an independent review left an obligation behind: %v", open)
	}
}

// LOW RISK. Where the task requires no independent review, there is nothing for
// an advisory accept to stand in for, and it continues.
func TestAnAdvisoryAcceptContinuesWhenNoIndependentReviewIsRequired(t *testing.T) {
	h := newGateHarness(t, roles.Policy{Reason: "blast radius file with approval gate none"}, roles.Unverified, "accept")

	if outcome := h.run(t); !outcome.Accepted() {
		t.Fatalf("an advisory accept was blocked on a task that requires no independent review: %q", outcome)
	}
	if open := h.engine.AdvisoryObligations("task-1"); len(open) != 0 {
		t.Fatalf("an obligation was recorded where the task imposed none: %v", open)
	}
}

// What the blocked run says, and what it must not say.
func TestTheBlockedRunSaysWhatWasNotEstablishedAndClaimsNoSuccess(t *testing.T) {
	h := newGateHarness(t, requiresIndependentReview(), roles.Unverified, "accept")
	h.run(t)

	var said []string
	for {
		select {
		case ev := <-h.events:
			said = append(said, string(ev.Kind)+" "+ev.Summary)
			continue
		default:
		}
		break
	}
	transcript := strings.Join(said, "\n")

	if !strings.Contains(transcript, "requires an independent review and has not had one") {
		t.Fatalf("the run does not say what was not established:\n%s", transcript)
	}
	// The vocabulary a reader would take for success, none of which happened.
	for _, forbidden := range []string{
		"ready for governed admission", "pull request", "admitted", "workflow.completed",
	} {
		if strings.Contains(strings.ToLower(transcript), strings.ToLower(forbidden)) {
			t.Fatalf("a blocked run emitted %q:\n%s", forbidden, transcript)
		}
	}
}

// An advisory REVISE still drives the worker, and an advisory ESCALATE still
// reaches the architect. Only the ACCEPT transition is gated.
func TestAnAdvisoryReviseStillDrivesTheWorkerRatherThanBlocking(t *testing.T) {
	h := newGateHarness(t, requiresIndependentReview(), roles.Unverified, "revise")

	outcome, _, _, _, err := h.engine.runCandidate(context.Background(), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")

	// One review cycle is configured, so a revise exhausts the budget and the
	// loop reports a worker that did not converge. That IS the ordinary route:
	// the objection went back to the implementer rather than stopping the run
	// at the review gate.
	if err == nil {
		t.Fatalf("a revise with one cycle should exhaust the budget, got outcome %q", outcome)
	}
	if !strings.Contains(err.Error(), "did not converge") {
		t.Fatalf("an advisory revise took some other path: %v", err)
	}
	if outcome == candidateAwaitingIndependentReview {
		t.Fatal("an advisory revise was treated as a blocked acceptance")
	}
	if outcome.Accepted() {
		t.Fatal("a revise accepted the candidate")
	}
	// A revise raises no independent-review obligation: nothing was accepted,
	// so there is no acceptance standing in for anything.
	if open := h.engine.AdvisoryObligations("task-1"); len(open) != 0 {
		t.Fatalf("a revise recorded an unmet acceptance obligation: %v", open)
	}
}

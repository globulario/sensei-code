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
	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/runreceipt"
	"github.com/globulario/sensei-code/internal/session"
)

// PROVEN TEMPORARY PROVIDER UNAVAILABILITY IS EXTERNAL EXECUTION STATE, NOT TASK
// FAILURE AND NOT ROLE OUTPUT.
//
// Observed live on 2026-09-18, task-1789770525156538509: the ChatGPT architect
// was out of quota, the turn was retried with "No response was received", and
// the task ended FAILED -- final to FindInterrupted, so continuing the same
// objective meant a new task. These witnesses pin each property the repair
// claims, numbered as in docs/architecture/dogfood repair.md.

// quota is the adapter's proof, exactly as provider.ChatGPTSession returns it.
func quota() *provider.Unavailable {
	return &provider.Unavailable{Provider: "chatgpt", Reason: "usageLimitExceeded",
		Detail: "You've hit your usage limit. ... try again at Sep 19th, 2026 7:10 AM."}
}

// unavailableRunner is a role transport whose provider proves it cannot serve.
type unavailableRunner struct {
	calls atomic.Int32
	cause *provider.Unavailable
}

func (r *unavailableRunner) Run(context.Context, agent.Request, func(event.Event)) (agent.Result, error) {
	r.calls.Add(1)
	return agent.Result{}, r.cause
}

// 1 + 7. The architect turn ends at the first proven refusal: typed, attributed
// to the architect, and without spending the malformed/no-answer retry.
func TestAProvenUnavailableArchitectEndsTheTurnTypedAndUnretried(t *testing.T) {
	// A second scripted answer exists so that a retry would be observable, and
	// would succeed -- the witness fails if the loop takes it.
	architect, err := resolveWithArchitect(t, architectTurn{err: quota()}, architectTurn{text: replyDecision})
	var blocked *RoleUnavailable
	if !errors.As(err, &blocked) {
		t.Fatalf("a proven provider refusal did not surface as a role unavailability: %v", err)
	}
	if blocked.Role != roles.Architect || blocked.Provider != "claude" {
		t.Fatalf("the block names role=%q provider=%q, want the architect turn and the configured provider", blocked.Role, blocked.Provider)
	}
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Fatal("the adapter's proof was lost on the way up")
	}
	if len(architect.prompts) != 1 {
		t.Fatalf("the architect was asked %d times; provider unavailability must not consume the retry budget", len(architect.prompts))
	}
	if strings.Contains(err.Error(), "could not produce a bounded decision") {
		t.Fatalf("unavailability was reported as the architect failing to decide: %v", err)
	}
}

// 6. A runner failure the adapter did not classify stays a failure, even when
// its text is the usage-limit sentence word for word. Text proves nothing.
func TestAnOrdinaryRunnerFailureIsNotLaunderedIntoAnExternalBlock(t *testing.T) {
	sentence := errors.New("codex exited 1: You've hit your usage limit. ... try again at Sep 19th, 2026 7:10 AM.")
	architect, err := resolveWithArchitect(t, architectTurn{err: sentence}, architectTurn{err: sentence})
	var blocked *RoleUnavailable
	if errors.As(err, &blocked) || errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("an unclassified runner failure became an external block: %v", err)
	}
	if !strings.Contains(err.Error(), "could not produce a bounded decision") || len(architect.prompts) != 2 {
		t.Fatalf("the ordinary failure path changed: prompts=%d err=%v", len(architect.prompts), err)
	}

	// And a provider proof that NO role site claimed is not classified either:
	// attribution happens where the turn is asked, never downstream.
	e := New(gitx.Repo{Root: t.TempDir()}, config.Default(), event.NewBus(), nil, "sess-1")
	if e.blockExternally("task-1", fmt.Errorf("somewhere else: %w", quota())) {
		t.Fatal("an unattributed provider refusal was turned into a role block")
	}
	if e.blockExternally("task-1", errors.New("a programming error")) {
		t.Fatal("an ordinary error was turned into a role block")
	}
}

// 7. Malformed and empty output remain their own conditions.
func TestMalformedOrEmptyOutputIsNotProviderUnavailability(t *testing.T) {
	for name, turns := range map[string][]architectTurn{
		"malformed": {{text: "not json"}, {text: "still not json"}},
		"empty":     {{text: " "}, {text: ""}},
	} {
		t.Run(name, func(t *testing.T) {
			architect, err := resolveWithArchitect(t, turns...)
			var blocked *RoleUnavailable
			if errors.As(err, &blocked) {
				t.Fatalf("%s output was classified as provider unavailability: %v", name, err)
			}
			if len(architect.prompts) != 2 {
				t.Fatalf("%s output lost its retry: asked %d times", name, len(architect.prompts))
			}
		})
	}
}

// blockedEngine is an engine over a real session directory, the durable record
// a restarted process reads.
func blockedEngine(t *testing.T, root, sessionID string) (*Engine, <-chan event.Event, *session.Store) {
	t.Helper()
	store, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	events, cancel := bus.Subscribe(64)
	t.Cleanup(cancel)
	return New(gitx.Repo{Root: root}, config.Default(), bus, store, sessionID), events, store
}

// reopen is a process restart: a NEW store object over the same directory, and
// nothing carried in memory.
func reopen(t *testing.T, root, sessionID string) []session.Interrupted {
	t.Helper()
	store, err := session.New(root, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return session.FindInterrupted(history)
}

// 1, 2, 4, 5, 10. The terminal is BLOCKED_EXTERNAL rather than FAILED, the task
// survives a restart as itself, and the retry time is exactly as known as the
// provider made it -- known when supplied, UNKNOWN when not.
func TestABlockedTurnIsATerminalThatSurvivesRestartAsTheSameTask(t *testing.T) {
	supplied := time.Date(2026, 9, 19, 11, 10, 26, 0, time.UTC)
	for name, retryAt := range map[string]time.Time{"retry time supplied": supplied, "retry time absent": {}} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			e, events, _ := blockedEngine(t, root, "session-a")
			const task = "task-42"
			e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
			e.beginReceipt(task)

			cause := quota()
			cause.RetryAt = retryAt
			if !e.blockExternally(task, &RoleUnavailable{Role: roles.Architect, Provider: "chatgpt", Cause: cause}) {
				t.Fatal("a role unavailability was not treated as an external block")
			}
			seen := drainEvents(events)
			if contains(seen, event.WorkflowFailed) {
				t.Fatalf("an external block was also reported as a failure: %v", kinds(seen))
			}
			if !contains(seen, event.WorkflowBlockedExternal) {
				t.Fatalf("no BLOCKED_EXTERNAL terminal: %v", kinds(seen))
			}
			rec := receiptFrom(t, seen)
			if rec.Outcome != runreceipt.OutcomeBlockedExternal {
				t.Fatalf("receipt outcome %q, want BLOCKED_EXTERNAL", rec.Outcome)
			}
			if rec.ExternalBlock.State != runreceipt.Known || !strings.Contains(rec.ExternalBlock.Text, "architect turn: chatgpt reported usageLimitExceeded") {
				t.Fatalf("the receipt does not say which turn was blocked and why: %+v", rec.ExternalBlock)
			}
			// Blocked before the architect answered: no plan exists, and the
			// record says so rather than leaving it unknown.
			if rec.PlanState != runreceipt.PlanNone {
				t.Fatalf("an architect blocked before planning left plan_state %q", rec.PlanState)
			}

			// Restart.
			found := reopen(t, root, "session-a")
			if len(found) != 1 || found[0].TaskID != task {
				t.Fatalf("after restart the blocked task is not resumable as itself: %+v", found)
			}
			if found[0].Planned {
				t.Fatal("a task blocked before any plan read back as planned")
			}
			block, err := ParseExternalBlock(found[0].BlockedExternal)
			if err != nil {
				t.Fatalf("the durable block did not read back: %v", err)
			}
			if block.TaskID != task || block.Role != string(roles.Architect) || block.Reason != "usageLimitExceeded" {
				t.Fatalf("the durable block lost its identity: %+v", block)
			}
			if retryAt.IsZero() {
				if block.RetryAtState != RetryAtUnknown || block.RetryAt != "" {
					t.Fatalf("an absent retry time was not recorded as UNKNOWN: %+v", block)
				}
				if !strings.Contains(rec.ExternalBlock.Text, "retry_at UNKNOWN") {
					t.Fatalf("the receipt invented or omitted the retry state: %q", rec.ExternalBlock.Text)
				}
			} else {
				got, _ := time.Parse(time.RFC3339, block.RetryAt)
				if block.RetryAtState != RetryAtKnown || !got.Equal(retryAt) {
					t.Fatalf("the supplied retry time did not survive restart: %+v", block)
				}
			}
		})
	}
}

// DF-3's historical evidence stays what it is: a task that ended FAILED under
// the old semantics is not reopened, whatever its failure text says.
func TestAHistoricalFailedTaskIsNotReopened(t *testing.T) {
	root := t.TempDir()
	e, _, _ := blockedEngine(t, root, "session-old")
	e.emit(event.New(e.SessionID, "task-1789770525156538509", event.SourceSystem, event.TaskCreated, "deployment identity", nil))
	e.emit(event.New(e.SessionID, "task-1789770525156538509", event.SourceSystem, event.WorkflowFailed,
		"architect could not produce a bounded decision: You've hit your usage limit.", nil))
	if found := reopen(t, root, "session-old"); len(found) != 0 {
		t.Fatalf("a historical FAILED task was reopened: %+v", found)
	}
}

// A record that cannot say which turn is owed, or that claims a retry time it
// cannot justify, is refused rather than resumed.
func TestAnExternalBlockRecordThatCannotJustifyItselfIsRefused(t *testing.T) {
	good := ExternalBlock{TaskID: "t", Role: "architect", Provider: "chatgpt", Reason: "usageLimitExceeded", RetryAtState: RetryAtUnknown}
	for name, mutate := range map[string]func(*ExternalBlock){
		"no task":             func(b *ExternalBlock) { b.TaskID = "" },
		"unknown role":        func(b *ExternalBlock) { b.Role = "janitor" },
		"no provider":         func(b *ExternalBlock) { b.Provider = "" },
		"no reason":           func(b *ExternalBlock) { b.Reason = "" },
		"unknown with a time": func(b *ExternalBlock) { b.RetryAt = "2026-09-19T11:10:26Z" },
		"known without a time": func(b *ExternalBlock) {
			b.RetryAtState = RetryAtKnown
		},
		"no retry state": func(b *ExternalBlock) { b.RetryAtState = "" },
	} {
		b := good
		mutate(&b)
		raw, _ := json.Marshal(b)
		if _, err := ParseExternalBlock(raw); err == nil {
			t.Errorf("%s: the record was accepted", name)
		}
	}
	raw, _ := json.Marshal(good)
	if _, err := ParseExternalBlock(raw); err != nil {
		t.Fatalf("a well-formed record was refused: %v", err)
	}
}

// 3. What re-entry relies on to not re-ask the human: an answer this task
// already gave is read from the durable session record, so a NEW engine after a
// restart honours it instead of escalating the same condition again.
func TestAnAnsweredConditionSurvivesARestartedEngine(t *testing.T) {
	root := t.TempDir()
	first, _, _ := blockedEngine(t, root, "session-b")
	res := authority.Resolution{TaskID: "task-7", SessionID: "session-b", Question: "q",
		Condition: "graph coverage is absent for the planned files", OptionID: "1", OptionLabel: "authorize",
		Scope: []string{"internal/x.go"}, Outcome: authority.Authorize, DecidedAt: time.Now().UTC()}
	first.emit(event.New(first.SessionID, "task-7", event.SourceUser, event.AuthorityResolved, "answered", res))

	restarted, _, _ := blockedEngine(t, root, "session-b")
	authorized, asked := restarted.applyAnsweredCondition("task-7", res.Condition, "internal/x.go")
	if !asked || !authorized {
		t.Fatalf("a restarted engine re-asks a question this task already answered: asked=%v authorized=%v", asked, authorized)
	}
	if _, asked := restarted.applyAnsweredCondition("task-8", res.Condition, "internal/x.go"); asked {
		t.Fatal("one task's answer was applied to another task")
	}
}

// 2 + 10 at the engine surface. A blocked, unplanned task resumes by re-entering
// execute under the SAME task id: no TaskCreated is minted and the resumed turn
// is announced as the one the block recorded. Sensei is deliberately
// unstartable here, so the run stops at the first step -- under the same id.
func TestResumingAnUnplannedBlockedTaskReentersTheSameTask(t *testing.T) {
	root := t.TempDir()
	e, _, _ := blockedEngine(t, root, "session-c")
	const task = "task-9"
	e.emit(event.New(e.SessionID, task, event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.beginReceipt(task)
	e.blockExternally(task, &RoleUnavailable{Role: roles.Architect, Provider: "chatgpt", Cause: quota()})

	found := reopen(t, root, "session-c")
	if len(found) != 1 {
		t.Fatalf("blocked task not found after restart: %+v", found)
	}
	restarted, events, _ := blockedEngine(t, root, "session-c")
	restarted.Config.Sensei.Command = "/nonexistent/awareness-mcp"
	// Same-process resume: the submission's provenance is still held, and the
	// resume must not demote it.
	restarted.recordObjective(task, Objective{Text: "the objective", Provenance: RequestedByHuman})
	if got := restarted.Resume(context.Background(), found[0]); got != task {
		t.Fatalf("resume answered for task %q, want %q", got, task)
	}
	var seen []event.Event
	deadline := time.After(10 * time.Second)
	for done := false; !done; {
		select {
		case ev := <-events:
			seen = append(seen, ev)
			if ev.Kind == event.WorkflowFailed || ev.Kind == event.WorkflowBlockedExternal || ev.Kind == event.WorkflowCompleted {
				done = true
			}
		case <-deadline:
			t.Fatalf("the resumed task did not settle: %v", kinds(seen))
		}
	}
	for _, ev := range seen {
		if ev.TaskID != task {
			t.Fatalf("the resumed run emitted under a different task id %q: %v", ev.TaskID, kinds(seen))
		}
		if ev.Kind == event.TaskCreated {
			t.Fatal("resume minted a new task")
		}
	}
	resumedAt := false
	for _, ev := range seen {
		if ev.Kind == event.Status && strings.Contains(ev.Summary, "resuming the same task at the turn it is owed (architect turn") {
			resumedAt = true
		}
	}
	if !resumedAt {
		t.Fatalf("the resume did not announce the recorded turn: %v", kinds(seen))
	}
	if got := restarted.objective(task).Provenance; got != RequestedByHuman {
		t.Fatalf("the resume replaced the recorded provenance with %q", got)
	}
}

// implementerResolver serves implementer turns by provider name, and the
// reviewer turn from a fixed runner.
type implementerResolver struct {
	implementers map[string]agent.Runner
	reviewer     agent.Runner
	architect    agent.Runner
	session      string
}

func (r implementerResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	switch spec.Role {
	case roles.Architect:
		if r.architect != nil {
			return Resolved{Runner: r.architect, Name: "chatgpt", Label: "ChatGPT"}, nil
		}
	case roles.Reviewer:
		return Resolved{Runner: r.reviewer, Name: "remote:abc", Label: "remote:abc"}, nil
	case roles.Implementer:
		if run, ok := r.implementers[spec.Agent.Name]; ok {
			return Resolved{Runner: run, Name: spec.Agent.Name, Label: spec.Agent.Name}, nil
		}
	}
	return CLIResolved(spec, r.session), nil
}

// 9. The same primitive covers the implementer: the only configured implementor
// proves it cannot serve, and the task waits -- no handoff, no worker recorded
// as failed, and the candidate is not disposed of.
func TestAnUnavailableImplementerBlocksTheTaskAndKeepsTheCandidate(t *testing.T) {
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, "accept")
	down := &unavailableRunner{cause: quota()}
	h.engine.Runners = implementerResolver{implementers: map[string]agent.Runner{"claude": down},
		reviewer: answeringRunner{text: `{"decision":"accept","summary":"ok"}`, mode: roles.Unverified}, session: "session-1"}

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	seen := drainEvents(h.events)

	var blocked *RoleUnavailable
	if !errors.As(failed, &blocked) || blocked.Role != roles.Implementer || blocked.Provider != "claude" {
		t.Fatalf("an unavailable sole implementer did not block the task as an implementer turn: %v", failed)
	}
	if got := down.calls.Load(); got != 1 {
		t.Fatalf("the unavailable implementer was asked %d times, want once", got)
	}
	if contains(seen, event.HandoffCreated) {
		t.Fatalf("an unavailable provider was handed off as a failed worker: %v", kinds(seen))
	}
	if contains(seen, event.CandidateResolved) {
		t.Fatalf("the candidate of a blocked task was disposed of: %v", kinds(seen))
	}
	if _, err := os.Stat(h.work); err != nil {
		t.Fatalf("the candidate worktree is gone: %v", err)
	}
	if strings.Contains(failed.Error(), "no bounded implementor produced") {
		t.Fatalf("the block was reported as implementors failing: %v", failed)
	}
}

// 8. An authorized alternate is still tried exactly as configured: the first
// implementor is unavailable, the second one runs. The block does not end the
// run while an authorized alternate can serve.
func TestAnAuthorizedAlternateImplementorIsStillTried(t *testing.T) {
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, "accept")
	down := &unavailableRunner{cause: quota()}
	h.engine.Config.Implementors = []config.Agent{{Name: "codex", Command: "false", Graph: "none"}, h.worker}
	h.engine.Runners = implementerResolver{implementers: map[string]agent.Runner{"codex": down},
		reviewer: answeringRunner{text: `{"decision":"accept","summary":"ok"}`, mode: roles.Unverified}, session: "session-1"}

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	seen := drainEvents(h.events)

	if got := down.calls.Load(); got != 1 {
		t.Fatalf("the unavailable implementor was asked %d times, want once", got)
	}
	if _, err := os.Stat(h.workerSaw); err != nil {
		t.Fatalf("the authorized alternate implementor was never asked: %v", err)
	}
	var blocked *RoleUnavailable
	if errors.As(failed, &blocked) || contains(seen, event.WorkflowBlockedExternal) {
		t.Fatalf("the run was blocked although an authorized alternate could serve: %v", failed)
	}
	if contains(seen, event.HandoffCreated) {
		t.Fatalf("the unavailable implementor was recorded as a failed worker with a handoff: %v", kinds(seen))
	}
}

// receiptFrom reads the governed run receipt out of an event stream.
func receiptFrom(t *testing.T, events []event.Event) runreceipt.Receipt {
	t.Helper()
	for _, ev := range events {
		if ev.Kind != event.RunReceipt {
			continue
		}
		var body struct {
			Receipt runreceipt.Receipt `json:"receipt"`
		}
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			t.Fatalf("the receipt is unreadable: %v", err)
		}
		return body.Receipt
	}
	t.Fatalf("no run receipt was emitted: %v", kinds(events))
	return runreceipt.Receipt{}
}

// A plan already recorded stands when a later architect turn is blocked -- an
// architect re-planning inside a cycle does not un-plan the run.
func TestABlockAfterPlanningKeepsThePlan(t *testing.T) {
	e, events, _ := blockedEngine(t, t.TempDir(), "session-d")
	const task = "task-11"
	e.beginReceipt(task)
	e.notePlan(task, "", "the bounded plan")
	e.blockExternally(task, &RoleUnavailable{Role: roles.Architect, Provider: "chatgpt", Cause: quota()})
	if rec := receiptFrom(t, drainEvents(events)); rec.PlanState != runreceipt.PlanPresent {
		t.Fatalf("a recorded plan was erased by a later block: plan_state %q", rec.PlanState)
	}
}

// An architect turn blocked INSIDE the cycle -- the reviewer escalated and the
// re-plan cannot be served -- blocks the task as an ARCHITECT turn. It must not
// be mistaken for implementer unavailability and passed to the next implementor.
func TestAnArchitectBlockedMidCycleIsNotHandedToAnotherImplementor(t *testing.T) {
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, "escalate")
	down := &unavailableRunner{cause: quota()}
	next := &unavailableRunner{cause: quota()}
	h.engine.Config.Implementors = []config.Agent{h.worker, {Name: "codex", Command: "false", Graph: "none"}}
	// The roster names one configured architect; the resolver substitutes the
	// answering runner, exactly as the configured reviewer is treated above. A
	// roster of one is walked and exhausted, which is what makes this a block
	// rather than a fallback.
	h.engine.Config.Architect = config.Agent{Name: "chatgpt", Command: "true", Graph: "none"}
	h.engine.Runners = implementerResolver{implementers: map[string]agent.Runner{"codex": next}, architect: down,
		reviewer: answeringRunner{text: `{"decision":"escalate","summary":"the plan needs an architectural answer"}`, mode: roles.Unverified},
		session:  "session-1"}

	var failed error
	h.engine.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	seen := drainEvents(h.events)

	if down.calls.Load() == 0 {
		t.Fatalf("the escalation never reached the architect, so this witness exercised nothing: %v (%v)", failed, kinds(seen))
	}
	var blocked *RoleUnavailable
	if !errors.As(failed, &blocked) || blocked.Role != roles.Architect {
		t.Fatalf("an architect blocked mid-cycle was not reported as an architect block: %v", failed)
	}
	if got := next.calls.Load(); got != 0 {
		t.Fatalf("the architect's unavailability was handed to another implementor (%d calls)", got)
	}
	if contains(seen, event.HandoffCreated) {
		t.Fatalf("an architect block created an implementer handoff: %v", kinds(seen))
	}
}

// AN UNAVAILABLE ARCHITECT COSTS A FALLBACK, THEN AN HONEST EXTERNAL BLOCK.
//
// Measured 2026-09-24: requests r-e25830e22588e77d and r-36348ba82230e65a each
// waited 30 minutes and were withdrawn unanswered, and the run then ended
// INCOMPLETE/FAILED reporting that the architect "could not produce a bounded
// decision". The architect had never been reached -- the account's pool was
// exhausted with a published reset 49 minutes later. The witnesses below are
// W1-W7 of that repair. W3, W4 and W7 are its controls: they prove the ladder
// does NOT advance where advancing would be shopping for a different answer.

// architectEntry is one roster entry and what its transport does.
type architectEntry struct {
	provider string
	turns    []architectTurn
	// resolverRefusal makes resolving this entry's adapter fail, which is the
	// shape a bridge refusal and an unestablished graph binding both arrive in.
	resolverRefusal error
}

// rosterResolver hands each roster entry its OWN transport, so a witness reads
// WHICH entry answered out of the record instead of inferring it from a count.
type rosterResolver struct {
	byProvider map[string]*scriptedArchitect
	refuse     map[string]error
	// resolved is every provider an adapter was asked for, in order. An entry
	// the walk never reached does not appear here at all.
	resolved []string
}

func (r *rosterResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	if spec.Role != roles.Architect {
		return CLIResolved(spec, "sess-roster"), nil
	}
	r.resolved = append(r.resolved, spec.Agent.Name)
	if err, ok := r.refuse[spec.Agent.Name]; ok {
		return Resolved{}, err
	}
	runner, ok := r.byProvider[spec.Agent.Name]
	if !ok {
		return Resolved{}, fmt.Errorf("no scripted architect for %q", spec.Agent.Name)
	}
	return Resolved{Runner: runner, Name: spec.Agent.Name, Label: config.DisplayName(spec.Agent.Name)}, nil
}

// architectRoster is an engine over a real session directory whose architect
// roster is exactly these entries.
//
// One entry sets Architect and leaves Architects empty, which is the singleton
// compatibility path a deployment with no alternate runs on (W4). More than one
// states the roster.
func architectRoster(t *testing.T, entries ...architectEntry) (*Engine, *rosterResolver, <-chan event.Event, string) {
	t.Helper()
	root := t.TempDir()
	store, err := session.New(root, "sess-roster")
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Architect = config.Agent{Name: entries[0].provider, Command: "true", Graph: "none"}
	cfg.Architects = nil
	res := &rosterResolver{byProvider: map[string]*scriptedArchitect{}, refuse: map[string]error{}}
	for _, ent := range entries {
		if len(entries) > 1 {
			cfg.Architects = append(cfg.Architects, config.Agent{Name: ent.provider, Command: "true", Graph: "none"})
		}
		if ent.resolverRefusal != nil {
			res.refuse[ent.provider] = ent.resolverRefusal
			continue
		}
		res.byProvider[ent.provider] = &scriptedArchitect{turns: ent.turns}
	}
	bus := event.NewBus()
	events, cancel := bus.Subscribe(256)
	t.Cleanup(cancel)
	e := New(gitx.Repo{Root: root}, cfg, bus, store, "sess-roster")
	e.Runners = res
	e.emit(event.New(e.SessionID, "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil))
	e.beginReceipt("task-1")
	return e, res, events, root
}

// askRoster takes the architect turn over the whole roster.
func askRoster(t *testing.T, e *Engine) (architectureDecision, error) {
	t.Helper()
	return e.resolveArchitectureIn(context.Background(), nil, certifiedStart{}, "task-1", "task", "PROMPT", t.TempDir())
}

// asked is how many turns one entry's transport actually took.
func (r *rosterResolver) asked(provider string) int {
	if runner, ok := r.byProvider[provider]; ok {
		return len(runner.prompts)
	}
	return 0
}

// W1. A first architect that cannot be obtained costs a fallback: the next
// roster entry is tried and the run proceeds on ITS answer.
//
// Every shape internal/roles/unobtainable.go names is walked, because "no answer
// was obtained" arrives from as many places as there are transports and the
// ladder must not advance on only the one that was measured.
func TestAnUnobtainableArchitectCostsAFallbackAndTheRunProceeds(t *testing.T) {
	unreachable := errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
	for name, first := range map[string][]architectTurn{
		// Proven unavailability answers once: it must not spend the retry
		// budget on transport state before the roster advances.
		"out of quota":       {{err: quota()}},
		"could not connect":  {{err: unreachable}, {err: unreachable}},
		"timed out":          {{err: context.DeadlineExceeded}, {err: context.DeadlineExceeded}},
		"exited non-zero":    {{err: errors.New("codex exited 1")}, {err: errors.New("codex exited 1")}},
		"unparseable output": {{text: "I think we should proceed."}, {text: "still not json"}},
		"empty output":       {{text: " "}, {text: ""}},
	} {
		t.Run(name, func(t *testing.T) {
			e, res, _, _ := architectRoster(t,
				architectEntry{provider: "chatgpt", turns: first},
				architectEntry{provider: "claude", turns: []architectTurn{{text: replyDecision}}})
			d, err := askRoster(t, e)
			if err != nil {
				t.Fatalf("the roster did not advance past an architect that could not be obtained: %v", err)
			}
			if d.Decision != "reply" || d.Message != "hello" {
				t.Fatalf("the run did not proceed on the second entry's answer: %+v", d)
			}
			if got := res.asked("chatgpt"); got != len(first) {
				t.Fatalf("the first entry took %d turns, want its whole attempt budget of %d", got, len(first))
			}
			if got := res.asked("claude"); got != 1 {
				t.Fatalf("the second entry was asked %d times, want once", got)
			}
		})
	}
}

// W2. The answering agent is RECORDED, and the decision is attributed to it --
// not to the first roster entry, and not to "the architect" generically.
func TestTheDecisionIsAttributedToTheEntryThatAnsweredIt(t *testing.T) {
	e, _, events, _ := architectRoster(t,
		architectEntry{provider: "chatgpt", turns: []architectTurn{{err: quota()}}},
		architectEntry{provider: "claude", turns: []architectTurn{{text: replyDecision}}})
	if _, err := askRoster(t, e); err != nil {
		t.Fatal(err)
	}
	if got := e.architectAnswered("task-1"); got != "claude" {
		t.Fatalf("the answering provider was recorded as %q, want claude", got)
	}
	if got := e.architectLabel("task-1"); got != "Claude" {
		t.Fatalf("the decision is attributed to %q, want the party that answered", got)
	}
	authority := e.decisionAuthority("task-1", certifiedStart{})
	if authority.DecidedBy != "Claude" || strings.Contains(authority.DecidedBy, "ChatGPT") {
		t.Fatalf("the decision authority names the wrong architect: %+v", authority)
	}
	// And the plan record carries it across a restart, which is the only place a
	// resumed run can learn which entry decided.
	record, err := json.Marshal(proposedPlan{
		architectureDecision: architectureDecision{Decision: "proceed", Plan: "do the work"},
		PlanSource:           PlanByArchitect,
		Architect:            e.architectAnswered("task-1"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(record), `"architect":"claude"`) {
		t.Fatalf("the plan record does not name the architect that wrote it: %s", record)
	}
	restarted := &Engine{}
	if _, err := restarted.restorePlanBound(session.Interrupted{TaskID: "task-1",
		PlanSource: string(PlanByArchitect), PlanRecord: record, PlanEventSource: event.SourceArchitect}); err != nil {
		t.Fatalf("the recorded plan did not restore: %v", err)
	}
	if got := restarted.architectAnswered("task-1"); got != "claude" {
		t.Fatalf("a restart read the deciding architect back as %q, want claude", got)
	}
	// Each entry is named in the assignment record, in the order tried, so the
	// walk itself is legible and not only its outcome.
	var assigned []string
	for _, ev := range drainEvents(events) {
		if ev.Kind != event.RoleAssigned {
			continue
		}
		var body struct {
			Role     string `json:"role"`
			Provider string `json:"provider"`
		}
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			t.Fatal(err)
		}
		if body.Role == string(roles.Architect) {
			assigned = append(assigned, body.Provider)
		}
	}
	if len(assigned) != 2 || assigned[0] != "chatgpt" || assigned[1] != "claude" {
		t.Fatalf("the architect assignments recorded were %v, want chatgpt then claude", assigned)
	}
}

// W3, THE CONTROL THAT MATTERS MOST. An architect that IS reached and returns a
// bounded decision the run does not like ends the turn on that decision. The
// roster MUST NOT advance: a ladder that walks on a legitimate refusal is
// shopping for a provider that says yes.
//
// The second entry is scripted with an answer that would visibly succeed, so if
// the roster advanced the turn would return ITS answer and every assertion below
// fires. It answers in kind rather than with a plan, so an advance is caught by
// the assertions and not by a later component failing on a half-built turn.
func TestABoundedRefusalDoesNotAdvanceTheRoster(t *testing.T) {
	alternate := `{"decision":"reply","message":"the next architect would have answered"}`
	cases := map[string]struct {
		first architectTurn
		// cancelled stops the run before the turn, which is the caller's
		// decision and not a provider that could not be obtained.
		cancelled bool
		wants     string
	}{
		"a conversational answer instead of a plan": {first: architectTurn{text: replyDecision}, wants: ""},
		"a bound party refusing the request": {
			first: architectTurn{err: fmt.Errorf("request r-1 was refused at stage answer-contract: %w", roles.ErrArchitectRefusal)},
			wants: roles.ErrArchitectRefusal.Error(),
		},
		"the caller stopping the turn": {
			first:     architectTurn{err: context.Canceled},
			cancelled: true,
			wants:     "stopped by its caller",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			e, res, _, _ := architectRoster(t,
				architectEntry{provider: "chatgpt", turns: []architectTurn{tc.first}},
				architectEntry{provider: "claude", turns: []architectTurn{{text: alternate}}})
			ctx := context.Background()
			if tc.cancelled {
				stopped, cancel := context.WithCancel(ctx)
				cancel()
				ctx = stopped
			}
			d, err := e.resolveArchitectureIn(ctx, nil, certifiedStart{}, "task-1", "task", "PROMPT", t.TempDir())

			if tc.wants == "" {
				if err != nil {
					t.Fatalf("a bounded decision was not returned: %v", err)
				}
				// The FIRST entry's answer, distinguishable from the second's:
				// asserting only that a reply arrived would pass on either.
				if d.Decision != "reply" || d.Message != "hello" {
					t.Fatalf("the turn did not end on the first entry's decision: %+v", d)
				}
			} else {
				if err == nil || !strings.Contains(err.Error(), tc.wants) {
					t.Fatalf("the refusal was not returned as itself: %v", err)
				}
				if d.Plan != "" {
					t.Fatalf("a refused turn produced a plan: %+v", d)
				}
			}
			// THE PROOF OF NON-ADVANCEMENT: the second entry was never resolved
			// and never asked. Not "it produced no answer" -- it was not
			// consulted at all.
			if got := res.asked("claude"); got != 0 {
				t.Fatalf("the roster advanced past a bounded refusal: the next entry was asked %d times", got)
			}
			if len(res.resolved) != 1 || res.resolved[0] != "chatgpt" {
				t.Fatalf("adapters were resolved for %v; only the entry that answered may be consulted", res.resolved)
			}
			if got := e.architectAnswered("task-1"); tc.wants == "" && got != "chatgpt" {
				t.Fatalf("the answer was attributed to %q, want the entry that gave it", got)
			}
		})
	}
}

// W4, A CONTROL. A configuration naming only the single Architect still walks a
// bounded roster of ONE and behaves exactly as it does today. Exhaustion has to
// be of a bounded set rather than of nothing.
func TestAConfigurationWithNoAlternateStillWalksARosterOfOne(t *testing.T) {
	if got := config.Default().ArchitectRoster(); len(got) != 1 || got[0].Name != config.Default().Architect.Name {
		t.Fatalf("the shipped default is not a roster of one: %+v", got)
	}
	if got := (config.Config{}).ArchitectRoster(); len(got) != 0 {
		t.Fatalf("a configuration naming no architect produced a roster: %+v", got)
	}
	// Stating a roster replaces the singleton; stating only the architect does
	// not leave the singleton unreachable, which is the defect #355 cost a
	// governed review to find on the reviewer side.
	stated := config.Config{Architect: config.Agent{Name: "chatgpt"}, Architects: []config.Agent{{Name: "claude"}}}
	if got := stated.ArchitectRoster(); len(got) != 1 || got[0].Name != "claude" {
		t.Fatalf("an explicit roster did not take precedence: %+v", got)
	}

	// And the turn on a roster of one is the turn as it is today: one entry, its
	// own two-attempt budget, and a decision returned from it.
	e, res, _, _ := architectRoster(t, architectEntry{provider: "chatgpt",
		turns: []architectTurn{{text: "not json"}, {text: replyDecision}}})
	if got := e.Config.ArchitectRoster(); len(got) != 1 {
		t.Fatalf("the engine's roster is %+v, want one entry", got)
	}
	if len(e.Config.Architects) != 0 {
		t.Fatalf("the singleton path was not exercised: %+v", e.Config.Architects)
	}
	d, err := askRoster(t, e)
	if err != nil || d.Decision != "reply" {
		t.Fatalf("a roster of one no longer resolves as it does today: %+v %v", d, err)
	}
	if got := res.asked("chatgpt"); got != 2 {
		t.Fatalf("the single entry took %d turns, want its two-attempt budget", got)
	}
}

// W5. With EVERY roster entry unobtainable the run ends BLOCKED_EXTERNAL with its
// durable record, carrying a reset time when one is known -- and NOT
// INCOMPLETE/FAILED. The absence of the failed terminal is asserted, not only
// the presence of the blocked one.
func TestAnExhaustedArchitectRosterIsExternalAndNotFailed(t *testing.T) {
	reset := time.Date(2026, 9, 25, 0, 0, 26, 0, time.UTC)
	late := quota()
	late.RetryAt = reset
	e, _, events, root := architectRoster(t,
		architectEntry{provider: "chatgpt", turns: []architectTurn{{err: quota()}}},
		architectEntry{provider: "claude", turns: []architectTurn{{err: late}}})

	_, err := askRoster(t, e)
	var chain *roles.ArchitectUnobtainable
	if !errors.As(err, &chain) {
		t.Fatalf("an exhausted roster was not reported as the exhausted chain: %v", err)
	}
	if got := chain.Providers(); len(got) != 2 || got[0] != "chatgpt" || got[1] != "claude" {
		t.Fatalf("the chain does not name every entry in the order tried: %v", got)
	}
	// THE ABSENT FINDING. "No provider was available" must not be reported as
	// the architect failing to decide; that conflation is the whole defect.
	if strings.Contains(err.Error(), "could not produce a bounded decision") {
		t.Fatalf("an unavailable roster was reported as the architect failing to decide: %v", err)
	}
	if !errors.Is(err, provider.ErrUnavailable) {
		t.Fatal("the adapters' proof was lost on the way up")
	}

	if !e.blockExternally("task-1", err) {
		t.Fatalf("an exhausted architect roster did not reach the external terminal: %v", err)
	}
	seen := drainEvents(events)
	if contains(seen, event.WorkflowFailed) {
		t.Fatalf("the exhausted roster was also reported as a failure: %v", kinds(seen))
	}
	if !contains(seen, event.WorkflowBlockedExternal) {
		t.Fatalf("no BLOCKED_EXTERNAL terminal: %v", kinds(seen))
	}
	if rec := receiptFrom(t, seen); rec.Outcome != runreceipt.OutcomeBlockedExternal {
		t.Fatalf("receipt outcome %q, want BLOCKED_EXTERNAL", rec.Outcome)
	}
	found := reopen(t, root, "sess-roster")
	if len(found) != 1 || found[0].TaskID != "task-1" {
		t.Fatalf("the blocked task is not resumable as itself: %+v", found)
	}
	block, err := ParseExternalBlock(found[0].BlockedExternal)
	if err != nil {
		t.Fatalf("the durable block did not read back: %v", err)
	}
	// The reset time a provider PUBLISHED is carried, and it is carried whichever
	// entry published it: a known time must not be lost because the entry that
	// knew none happened to be configured first.
	if block.RetryAtState != RetryAtKnown {
		t.Fatalf("a published reset time was recorded as UNKNOWN: %+v", block)
	}
	if got, _ := time.Parse(time.RFC3339, block.RetryAt); !got.Equal(reset) {
		t.Fatalf("the durable block states reset %q, want %v", block.RetryAt, reset)
	}
	if block.Role != string(roles.Architect) || block.Provider != "claude" {
		t.Fatalf("the durable block does not name the turn and the provider it is waiting on: %+v", block)
	}
}

// W5's negative control. A roster every entry of which was REACHED and simply
// produced nothing usable proved no unavailability, and must NOT be parked as
// external state: that would map a genuine failure to decide onto a terminal
// meaning "come back later", which is the mirror image of the defect.
func TestARosterThatWasReachedAndDecidedNothingIsNotAnExternalBlock(t *testing.T) {
	e, _, events, _ := architectRoster(t,
		architectEntry{provider: "chatgpt", turns: []architectTurn{{text: "not json"}, {text: "still not json"}}},
		architectEntry{provider: "claude", turns: []architectTurn{{text: "nor this"}, {text: "nor that"}}})
	_, err := askRoster(t, e)
	if err == nil || !strings.Contains(err.Error(), "could not produce a bounded decision") {
		t.Fatalf("a roster that decided nothing did not report the architect failing to decide: %v", err)
	}
	if errors.Is(err, provider.ErrUnavailable) {
		t.Fatalf("unparseable output was laundered into provider unavailability: %v", err)
	}
	if e.blockExternally("task-1", err) {
		t.Fatalf("a genuine architect failure was parked as external state: %v", err)
	}
	if contains(drainEvents(events), event.WorkflowBlockedExternal) {
		t.Fatal("a genuine architect failure emitted the external terminal")
	}
}

// W6, the engine half. When the transport carrying the architect turn exhausts
// its bounded wait, the roster is walked afterwards: bridge exhaustion does not
// end the run.
//
// The error is the shape internal/ghbridge returns when its request went
// unanswered for its whole deadline (see AwaitArchitecture, and the ghbridge
// witness beside TestResolverCarriesOnlyBoundArchitectForItsProvider, which
// checks that exact error against ArchitectTurnUnobtainable). This package
// cannot import ghbridge -- ghbridge imports it -- so the rule is exported and
// each side proves its own half against the same predicate.
func TestAnExhaustedCarriedArchitectTurnWalksTheRosterAfterwards(t *testing.T) {
	unanswered := errors.New("no architecture answer answering that request was posted: context deadline exceeded")
	if !ArchitectTurnUnobtainable(unanswered) {
		t.Fatal("an exchange that ended unanswered is not classified as a failure to obtain an architect")
	}
	e, res, _, _ := architectRoster(t,
		architectEntry{provider: "chatgpt", turns: []architectTurn{{err: unanswered}, {err: unanswered}}},
		architectEntry{provider: "claude", turns: []architectTurn{{text: replyDecision}}})
	d, err := askRoster(t, e)
	if err != nil {
		t.Fatalf("an exhausted carried turn ended the run instead of costing a fallback: %v", err)
	}
	if d.Decision != "reply" || res.asked("claude") != 1 {
		t.Fatalf("the roster was not walked after the carried turn was exhausted: %+v, claude asked %d", d, res.asked("claude"))
	}
	if got := e.architectAnswered("task-1"); got != "claude" {
		t.Fatalf("the answer after a carried exhaustion was attributed to %q", got)
	}
}

// W7, A CONTROL. A roster entry that is reachable but whose graph binding cannot
// be established is REFUSED rather than run unbound, and the roster does not
// advance past it.
//
// Advancing would be wrong twice over. The binding is a property of the
// QUESTION, so every later entry would be refused for the same reason, and the
// run would end reporting that nobody could be reached while the real condition
// sat in the first entry's error. "Unbound must never look like bound" survives
// the roster, and it must not be allowed to look like unavailable either.
func TestAnUnboundArchitectIsRefusedAndTheRosterDoesNotAdvance(t *testing.T) {
	// The shape ghbridge.Resolver returns for a real task whose objective/base/
	// graph binding is missing; its own witness pins that text.
	unbound := errors.New("no exact objective/world binding for architect turn: task \"task-1\"")
	e, res, _, _ := architectRoster(t,
		architectEntry{provider: "chatgpt", resolverRefusal: unbound},
		architectEntry{provider: "claude", turns: []architectTurn{{text: replyDecision}}})
	d, err := askRoster(t, e)
	if err == nil || !strings.Contains(err.Error(), "no exact objective/world binding") {
		t.Fatalf("an unbound architect turn was not refused: %v %+v", err, d)
	}
	if d.Decision != "" {
		t.Fatalf("a refused unbound turn produced a decision: %+v", d)
	}
	if got := res.asked("claude"); got != 0 {
		t.Fatalf("an unbound turn was handed to the next entry, which ran %d times", got)
	}
	if len(res.resolved) != 1 {
		t.Fatalf("adapters were resolved for %v; the refusal must end the walk", res.resolved)
	}
	var chain *roles.ArchitectUnobtainable
	if errors.As(err, &chain) {
		t.Fatalf("a binding refusal was reported as no architect being obtainable: %v", err)
	}
	if e.blockExternally("task-1", err) {
		t.Fatal("a binding refusal was parked as external state")
	}
}

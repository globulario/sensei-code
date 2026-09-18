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
}

// implementerResolver serves implementer turns by provider name, and the
// reviewer turn from a fixed runner.
type implementerResolver struct {
	implementers map[string]agent.Runner
	reviewer     agent.Runner
	session      string
}

func (r implementerResolver) Resolve(spec RunnerSpec) (Resolved, error) {
	switch spec.Role {
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

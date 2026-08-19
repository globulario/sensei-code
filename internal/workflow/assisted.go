package workflow

// The assisted workflow.
//
// Same session, same event bus, same Sensei client, same providers, same TUI as
// the governed workflow — a different state machine over the same contracts,
// not a second product. What it deliberately does not do is the whole point:
// no candidate worktree, no bounded plan, no reviewer loop, no diff audit, no
// decision record, no pull request. A conversation produces no receipts,
// because a receipt for a conversation is a claim that something was governed
// when nothing was.
//
// It still reads Sensei. Assisted does not mean unaware: the architect answers
// with the graph in front of it, and freshness is reported rather than assumed.
// The difference from governed mode is authority, not information.

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/assist"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/continuity"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/retrieval"
	"github.com/globulario/sensei-code/internal/sensei"
)

// SubmitAssisted starts a conversational turn. This is what an ordinary
// message does.
func (e *Engine) SubmitAssisted(ctx context.Context, task string) string {
	taskID := fmt.Sprintf("task-%d", time.Now().UTC().UnixNano())
	go e.runAssisted(ctx, taskID, strings.TrimSpace(task))
	return taskID
}

// SubmitGoverned starts governed execution of a task. This is /run, and typing
// it is the authorization: the workflow does not ask again except when the
// authority router names a Level-3 condition, or before publication.
//
// Because there is no approval rendezvous to decline at, the run is started
// under a cancellable context whose cancel is held against the task. That is
// what makes Stop possible, and Stop is what keeps removing the rendezvous
// honest rather than merely quieter.
func (e *Engine) SubmitGoverned(ctx context.Context, task string) string {
	taskID := fmt.Sprintf("task-%d", time.Now().UTC().UnixNano())
	ctx, cancel := context.WithCancel(ctx)
	e.mu.Lock()
	if e.stops == nil {
		e.stops = make(map[string]context.CancelFunc)
	}
	e.stops[taskID] = cancel
	e.mu.Unlock()
	go func() {
		defer e.clearStop(taskID)
		e.run(ctx, taskID, strings.TrimSpace(task))
	}()
	return taskID
}

// Stop ends a governed task the human no longer wants running.
//
// It reports whether there was anything to stop, so a caller can say "nothing
// is running" rather than implying it killed something. Cancelling the context
// reaches the worker process itself — every agent runs under exec.CommandContext
// — so this is a real stop and not a flag the loop checks when convenient.
//
// What it deliberately does not do is clean up. The candidate worktree and
// whatever the worker had written stay exactly where they are, and the run
// reports itself stopped rather than failed, so the work remains resumable.
// A stop is the human withdrawing attention from a task, not a judgement that
// the work was worthless.
func (e *Engine) Stop(taskID string) bool {
	e.mu.Lock()
	cancel, ok := e.stops[taskID]
	delete(e.stops, taskID)
	e.mu.Unlock()
	if !ok {
		return false
	}
	cancel()
	return true
}

// Stoppable reports whether this task is a governed run that Stop can reach.
func (e *Engine) Stoppable(taskID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	_, ok := e.stops[taskID]
	return ok
}

func (e *Engine) clearStop(taskID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	delete(e.stops, taskID)
}

// Submit is the default entry point and is assisted.
//
// It is kept as a name because callers read better for it, but it no longer
// starts governed execution: an ordinary message is a question until somebody
// says otherwise.
func (e *Engine) Submit(ctx context.Context, task string) string {
	return e.SubmitAssisted(ctx, task)
}

// announceMode records the mode and its provenance as an event, so the UI shows
// what a task actually is rather than what the configuration might suggest.
func (e *Engine) announceMode(taskID string, m TaskMode) {
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.ModeSelected, m.Describe(), map[string]string{
		"mode":       string(m.Mode),
		"provenance": string(m.Provenance),
	}))
}

// renderRetrieved lays out what targeted retrieval produced, including the
// targets that produced nothing.
//
// The empty answers are the ones worth keeping. An architect shown only what
// was found will fill the gaps from memory and sound identical doing it; shown
// that the graph was asked about a target and had nothing, it can say so.
func renderRetrieved(outcomes []retrieval.Outcome) string {
	if len(outcomes) == 0 {
		return "(nothing in the question named a file or a governed id, so no targeted retrieval ran)"
	}
	var b strings.Builder
	for _, o := range outcomes {
		fmt.Fprintf(&b, "\n--- %s %s [%s] ---\n", o.Query.Kind, o.Query.Target, o.State)
		switch o.State {
		case retrieval.StatePresent:
			b.WriteString(o.Text + "\n")
		default:
			b.WriteString(o.Detail + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// retrievalBudget bounds how many targeted lookups one conversational turn may
// make.
//
// The bound exists for the reason §F of sensei-code#9 gives: a turn must not
// replay the whole graph, and a long conversation must not grow its own cost
// per question. Four is small on purpose — the alternative to a small bound is
// not a complete answer, it is a slow one built mostly of context nobody read.
// What the bound must never be is silent, which is why Plan carries what it
// dropped.
const retrievalBudget = 4

// senseiReader adapts the Sensei client to retrieval's read-only surface.
//
// The adapter exists so the retrieval package cannot reach a writing tool even
// by mistake: it is handed an interface whose only method takes the typed read
// surfaces, not a client that could propose, admit or advance a task.
type senseiReader struct{ sc *sensei.Client }

func (r senseiReader) CallTool(name string, args map[string]any) (retrieval.Result, error) {
	res, err := r.sc.CallTool(name, args)
	if err != nil {
		return retrieval.Result{}, err
	}
	return retrieval.Result{Text: firstText(res), Structured: res.Structured}, nil
}

func (e *Engine) runAssisted(ctx context.Context, taskID, task string) {
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.TaskCreated, task, nil))
	e.announceMode(taskID, assistedMode())

	fail := func(err error) {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowFailed, err.Error(), nil))
	}
	if task == "" {
		fail(errEmptyTask)
		return
	}
	if !e.Config.Permissions.ReadRepository {
		fail(errNoReadCapability)
		return
	}

	// Sensei is consulted, but an assisted turn is not gated on it. A question
	// asked while the graph is rebuilding should still get an answer; what it
	// must not get is a silent pretence that the answer was graph-backed. So a
	// degraded surface becomes a stated caveat rather than a refusal, which is
	// the opposite of the governed path and correct for the same reason:
	// authority differs, so what an unavailable answer costs differs too.
	var observations []string
	var consulted assist.Consulted
	var retrieved []retrieval.Outcome
	domain := ""
	workspaceEvidence, preflightEvidence := "(unavailable)", "(unavailable)"
	sc, err := sensei.Start(ctx, e.Repo.Root, e.Config.Sensei.Command, e.Config.Sensei.Args)
	if err != nil {
		observations = append(observations, "Sensei is unavailable for this turn ("+err.Error()+"), so nothing below is graph-backed.")
		consulted.Add(assist.Observation{Source: "sensei", State: assist.Unavailable, Reason: err.Error()})
	} else {
		defer sc.Close()
		workspaceStatus, wsErr := sc.CallTool("sensei_workspace_status", map[string]any{"repo": e.Repo.Root})
		if wsErr != nil {
			observations = append(observations, "Sensei workspace status is unavailable: "+wsErr.Error())
			consulted.Add(assist.Observation{Source: "workspace status", State: assist.Unavailable, Reason: wsErr.Error()})
		} else {
			workspaceEvidence = firstText(workspaceStatus)
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.SenseiResult, workspaceEvidence, workspaceStatus.Structured))
			if status, decodeErr := sensei.DecodeWorkspaceStatus(workspaceStatus); decodeErr == nil {
				domain = status.Binding.RepositoryDomain
				if !status.Permits() {
					observations = append(observations, "workspace identity is incomplete: "+status.Diagnostic())
					consulted.Add(assist.Observation{Source: "workspace status", State: assist.Stale, Reason: status.Diagnostic()})
				} else {
					consulted.Add(assist.Observation{Source: "workspace status", State: assist.Present, Reason: domain})
				}
			} else {
				observations = append(observations, "workspace status could not be read as a verdict: "+decodeErr.Error())
				consulted.Add(assist.Observation{Source: "workspace status", State: assist.Unavailable, Reason: decodeErr.Error()})
			}
		}

		args := map[string]any{"task": task, "files": []string{}, "mode": "compact"}
		if domain != "" {
			args["domain"] = domain
		}
		if preflight, pfErr := sc.CallTool("awareness_preflight", args); pfErr == nil {
			preflightEvidence = firstText(preflight)
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.SenseiResult, preflightEvidence, preflight.Structured))
			if decision, decodeErr := sensei.DecodePreflight(preflight); decodeErr == nil {
				consulted.Add(preflightSource(decision))
				if !decision.Authority.Certifiable() {
					// The one thing an assisted turn must never do quietly.
					observations = append(observations, "Sensei cannot vouch for its own graph right now ("+
						decision.Authority.Diagnostic()+"), so treat any architectural claim below as unverified.")
				}
			}
		} else {
			observations = append(observations, "Sensei preflight is unavailable: "+pfErr.Error())
			consulted.Add(assist.Observation{Source: "preflight", State: assist.Unavailable, Reason: pfErr.Error()})
		}

		// Retrieval is driven by the question rather than by habit. The two
		// broad reads above answer "can Sensei vouch for itself"; these answer
		// "what governs the thing being asked about", which is a different
		// question and the one the human actually asked.
		plan := retrieval.PlanFor(task, retrievalBudget)
		if len(plan.Queries) != 0 {
			retrieved = retrieval.Execute(senseiReader{sc}, domain, plan)
			for _, o := range retrieved {
				consulted.Add(assist.Observation{
					Source: string(o.Query.Kind) + " " + o.Query.Target,
					State:  assist.Presence(o.State),
					Reason: o.Detail,
				})
			}
		}
		if plan.Bounded() {
			// Never a silent cap: a turn that consulted four of nine sources
			// reads exactly like complete coverage unless it says otherwise.
			observations = append(observations, plan.Describe())
			consulted.Add(assist.Observation{Source: "retrieval budget", State: assist.Absent, Reason: plan.Describe()})
		}
	}

	// Continuity is established before the architect speaks, and its verdict is
	// carried into the prompt. An architect that has silently lost the thread
	// answers exactly like one that still has it, so the loss has to be said.
	thread := continuity.Load(e.Repo.Root)
	base := repositoryHead(ctx, e.Repo)
	resumption := thread.Continues(e.Config.Architect.Name, base)
	consulted.Add(assist.Observation{
		Source: "architect conversation",
		State:  continuityPresence(resumption),
		Reason: resumption.Describe(),
	})
	if resumption.State != continuity.Continued {
		observations = append(observations, resumption.Describe())
	}

	conversation := e.conversationSoFar(task, 40)
	architect := agent.CLI{
		Name: e.Config.Architect.Name, Label: config.DisplayName(e.Config.Architect.Name),
		Command: e.Config.Architect.Command, Args: e.Config.Architect.Args,
		Source: event.SourceArchitect, SessionID: e.SessionID, UnsetEnv: provider.SessionOnlyEnv,
	}
	result, err := architect.Run(ctx, agent.Request{
		Role: agent.Architect, TaskID: taskID, Workspace: e.Repo.Root,
		Prompt: assistedPrompt(e.Repo.Root, domain, config.DisplayName(e.Config.Architect.Name), task, conversation,
			observations, workspaceEvidence, preflightEvidence, renderRetrieved(retrieved)),
	}, e.emit)
	if err != nil {
		fail(err)
		return
	}

	answer := strings.TrimSpace(result.Text)
	if answer == "" {
		fail(errArchitectSilent)
		return
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceArchitect, event.ArchitectSpoke, answer, nil))
	// The evidence drawer is emitted for every turn, including the turns where
	// everything was fine. A provenance surface that only appears when
	// something is wrong is one nobody learns to read.
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.ContextConsulted, consulted.Render(), consulted))
	if err := thread.Record(e.Config.Architect.Name, result.SessionID, base, time.Now().UTC()).Save(e.Repo.Root); err != nil {
		// Losing the record costs continuity on the next turn and nothing else,
		// so it is reported rather than raised.
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.Status,
			"architect conversation identity not recorded: "+err.Error(), nil))
	}
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted, "", nil))
}

// continuityPresence maps a resumption onto the typed availability vocabulary
// the rest of the packet uses, so the drawer reads in one language.
func continuityPresence(r continuity.Resumption) assist.Presence {
	switch r.State {
	case continuity.Continued:
		if r.BaseMoved {
			return assist.Stale
		}
		return assist.Present
	case continuity.Reconstructed:
		// Not "unavailable": the conversation was rebuilt and the turn is
		// answerable. Absent is the honest word — what is missing is the prior
		// dialogue, not the ability to answer.
		return assist.Absent
	default:
		return assist.EmptyProven
	}
}

// preflightSource states what the graph could actually vouch for, rather than
// recording that a call returned.
func preflightSource(d sensei.PreflightDecision) assist.Observation {
	switch {
	case !d.Authority.Certifiable():
		return assist.Observation{Source: "preflight", State: assist.Stale, Reason: d.Authority.Diagnostic()}
	case d.Status == sensei.PreflightOK:
		return assist.Observation{Source: "preflight", State: assist.Present, Reason: string(d.Status)}
	case d.Status == sensei.PreflightEmpty:
		return assist.Observation{Source: "preflight", State: assist.EmptyProven, Reason: "the graph answered and found nothing governing this question"}
	default:
		return assist.Observation{Source: "preflight", State: assist.Absent, Reason: d.Diagnostic()}
	}
}

// assistedPrompt asks for an answer, not a plan.
//
// The governed prompt demands bounded JSON because a plan has to be machine
// readable. Here that would be actively harmful: a person asking a question
// wants prose, and forcing JSON would make the architect answer in the register
// of a change proposal even when nothing is being proposed.
//
// Two things it insists on that are easy to get backwards. The architect is
// told that routine architectural judgment is its own, because an assistant
// that asks permission for ordinary choices is a worse collaborator than one
// that decides and explains. And it is told to state uncertainty plainly,
// because an assisted answer has no reviewer, no audit and no candidate behind
// it — nothing downstream will catch an overstated claim, which is precisely
// why overstating is more costly here than in governed work, not less.
func assistedPrompt(repoRoot, domain, architectName, task, conversation string, observations []string, workspaceEvidence, preflightEvidence, retrievedEvidence string) string {
	notes := "(none)"
	if len(observations) != 0 {
		notes = "- " + strings.Join(observations, "\n- ")
	}
	scope := domain
	if strings.TrimSpace(scope) == "" {
		scope = "(this repository has no registered Sensei domain)"
	}
	return fmt.Sprintf(`You are %s, the architectural authority for the repository at %s (Sensei domain: %s), speaking directly with the human who owns it.

You are in ASSISTED mode. This is a human architectural conversation, not a
change request and not a machine handoff. Nothing you say here creates a
candidate, a plan of record, or any governed artifact, and you must not speak as
though it does. Do not produce JSON unless the human explicitly asks for JSON.

Answer naturally and with enough depth to be genuinely useful: precise,
concrete, technically rich. Explain the evidence, the architectural
consequences, the tradeoffs, and your recommendation where they matter. Do not
compress a real answer into a terse status line.

You may read the repository and query Sensei when that improves the answer. Do
not edit files, commit, push, deploy, or carry out implementation work in this
turn. Sensei remains the governance authority: do not weaken, reinterpret, or
invent its contracts, and treat the evidence below as live evidence rather than
decorative prompt text.

Routine architectural judgment is yours to make. Do not ask the human for
ordinary implementation choices or for permission. Only when the discussion
reaches a genuinely human-owned boundary -- product intent, a new invariant, an
externally meaningful contract, or a trust-policy choice existing authority
cannot settle -- name that boundary and offer at most three concrete options
with a recommendation.

Say plainly when you do not know, or when the graph does not cover something. An
assisted answer has no reviewer and no audit behind it, so nothing downstream
will catch a claim you overstated.

If the human is describing work they want carried out rather than asking a
question, say so and tell them to run it with /run, which crosses into governed
execution: candidate worktree, bounded plan, reviewer and audit. Do not start
doing the work here.

CAVEATS FOR THIS TURN:
%s

LIVE SENSEI WORKSPACE AUTHORITY:
%s

LIVE SENSEI PREFLIGHT:
%s

SENSEI EVIDENCE RETRIEVED FOR THIS QUESTION:
This was selected by what the question named, not injected on every turn. An
entry reporting that the graph found nothing is a real answer about that target:
it means the graph was asked and had nothing, which is not the same as the
target being safe, and not a reason to answer from memory instead.
%s

CONVERSATION SO FAR:
%s

THE HUMAN SAID:
%s`, architectName, repoRoot, scope, notes, workspaceEvidence, preflightEvidence, retrievedEvidence, conversationOrNone(conversation), task)
}

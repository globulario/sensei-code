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
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/sensei"
)

// SubmitAssisted starts a conversational turn. This is what an ordinary
// message does.
func (e *Engine) SubmitAssisted(ctx context.Context, task string) string {
	taskID := fmt.Sprintf("task-%d", time.Now().UTC().UnixNano())
	go e.runAssisted(ctx, taskID, strings.TrimSpace(task))
	return taskID
}

// SubmitGoverned starts governed execution of a task. This is /run.
func (e *Engine) SubmitGoverned(ctx context.Context, task string) string {
	taskID := fmt.Sprintf("task-%d", time.Now().UTC().UnixNano())
	go e.run(ctx, taskID, strings.TrimSpace(task))
	return taskID
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
	domain := ""
	sc, err := sensei.Start(ctx, e.Repo.Root, e.Config.Sensei.Command, e.Config.Sensei.Args)
	if err != nil {
		observations = append(observations, "Sensei is unavailable for this turn ("+err.Error()+"), so nothing below is graph-backed.")
	} else {
		defer sc.Close()
		workspaceStatus, wsErr := sc.CallTool("sensei_workspace_status", map[string]any{"repo": e.Repo.Root})
		if wsErr != nil {
			observations = append(observations, "Sensei workspace status is unavailable: "+wsErr.Error())
		} else {
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.SenseiResult, firstText(workspaceStatus), workspaceStatus.Structured))
			if status, decodeErr := sensei.DecodeWorkspaceStatus(workspaceStatus); decodeErr == nil {
				domain = status.Binding.RepositoryDomain
				if !status.Permits() {
					observations = append(observations, "workspace identity is incomplete: "+status.Diagnostic())
				}
			} else {
				observations = append(observations, "workspace status could not be read as a verdict: "+decodeErr.Error())
			}
		}

		args := map[string]any{"task": task, "files": []string{}, "mode": "compact"}
		if domain != "" {
			args["domain"] = domain
		}
		if preflight, pfErr := sc.CallTool("awareness_preflight", args); pfErr == nil {
			e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.SenseiResult, firstText(preflight), preflight.Structured))
			if decision, decodeErr := sensei.DecodePreflight(preflight); decodeErr == nil {
				if !decision.Authority.Certifiable() {
					// The one thing an assisted turn must never do quietly.
					observations = append(observations, "Sensei cannot vouch for its own graph right now ("+
						decision.Authority.Diagnostic()+"), so treat any architectural claim below as unverified.")
				}
			}
		} else {
			observations = append(observations, "Sensei preflight is unavailable: "+pfErr.Error())
		}
	}

	conversation := e.conversationSoFar(task, 40)
	architect := agent.CLI{
		Name: e.Config.Architect.Name, Label: config.DisplayName(e.Config.Architect.Name),
		Command: e.Config.Architect.Command, Args: e.Config.Architect.Args,
		Source: event.SourceArchitect, SessionID: e.SessionID, UnsetEnv: provider.SessionOnlyEnv,
	}
	result, err := architect.Run(ctx, agent.Request{
		Role: agent.Architect, TaskID: taskID, Workspace: e.Repo.Root,
		Prompt: assistedPrompt(e.Repo.Root, domain, config.DisplayName(e.Config.Architect.Name), task, conversation, observations),
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
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowCompleted, "", nil))
}

// assistedPrompt asks for an answer, not a plan.
//
// The governed prompt demands bounded JSON because a plan has to be machine
// readable. Here that would be actively harmful: a person asking a question
// wants prose, and forcing JSON would make the architect answer in the register
// of a change proposal even when nothing is being proposed.
func assistedPrompt(repoRoot, domain, architectName, task, conversation string, observations []string) string {
	notes := "(none)"
	if len(observations) != 0 {
		notes = "- " + strings.Join(observations, "\n- ")
	}
	scope := domain
	if strings.TrimSpace(scope) == "" {
		scope = "(this repository has no registered Sensei domain)"
	}
	return fmt.Sprintf(`You are %s, the architect for the repository at %s (Sensei domain: %s).

You are in ASSISTED mode. This is a conversation, not a change request. Nothing
you say here creates a candidate, a plan of record, or any governed artifact,
and you must not speak as though it does. Do not produce JSON. Do not propose a
bounded implementation contract unless the human asks for one.

Answer what they asked. Read the repository and query Sensei when it helps. If
the honest answer is that you do not know, or that the graph does not cover it,
say exactly that -- an assisted answer that overstates its certainty is worse
than governed work that refuses, because nothing downstream will catch it.

If the human is describing work they want carried out rather than asking a
question, say so plainly and tell them to run it with /run, which enters
governed execution: candidate worktree, bounded plan, reviewer and audit. Do not
start doing the work here.

CAVEATS FOR THIS TURN:
%s

CONVERSATION SO FAR:
%s

THE HUMAN SAID:
%s`, architectName, repoRoot, scope, notes, conversationOrNone(conversation), task)
}

package ghbridge

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
)

// ArchitectureRunner carries one architect turn over the GitHub mailbox. It is
// created by Resolver from a RunnerSpec whose Architecture binding was minted by
// the workflow at the resolver boundary. It has no way to invent or widen that
// binding from the prompt it carries.
type ArchitectureRunner struct {
	Issue        Issue
	Binding      roles.ArchitectureBinding
	NewRequestID func() string
	Poll         time.Duration
	SessionID    string
	Wait         time.Duration
}

var ErrNotArchitect = errors.New("the github architecture runner serves the architect role only")
var ErrUnboundArchitecture = errors.New("no exact objective/world binding for architect turn")

func (r *ArchitectureRunner) Run(ctx context.Context, req agent.Request, emit func(event.Event)) (agent.Result, error) {
	if req.Role != roles.Architect {
		return agent.Result{}, fmt.Errorf("%w: got %s", ErrNotArchitect, req.Role)
	}
	if req.TaskID != r.Binding.TaskID || !r.Binding.Valid() {
		return agent.Result{}, fmt.Errorf("%w: task %q binding %+v", ErrUnboundArchitecture, req.TaskID, r.Binding)
	}
	if r.NewRequestID == nil {
		return agent.Result{}, errors.New("the github architecture bridge needs a request id source")
	}
	request := ArchitectureRequest{
		Binding:   r.Binding,
		RequestID: r.NewRequestID(),
		Prompt:    req.Prompt,
	}
	if err := PostArchitectureRequest(ctx, r.Issue, request); err != nil {
		return agent.Result{}, fmt.Errorf("posting the architecture request: %w", err)
	}

	if emit != nil {
		emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentStarted,
			"waiting for the remote architect to answer request "+request.RequestID+" on github",
			map[string]any{
				"request_id":          request.RequestID,
				"objective_digest":    r.Binding.ObjectiveDigest,
				"base":                r.Binding.BaseSHA,
				"graph_build_commit":  r.Binding.GraphBuildCommit,
				"transport":           "github",
			}))
	}

	wait := r.Wait
	if wait <= 0 {
		wait = DefaultWait
	}
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	answer, err := AwaitArchitecture(wctx, r.Issue, request, r.Poll)
	if err != nil {
		return agent.Result{}, err
	}

	if emit != nil {
		emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentFinished,
			"the remote architect answered request "+request.RequestID,
			map[string]any{
				"request_id":          request.RequestID,
				"objective_digest":    r.Binding.ObjectiveDigest,
				"base":                r.Binding.BaseSHA,
				"graph_build_commit":  r.Binding.GraphBuildCommit,
				"github_author":       answer.Author,
				"github_author_id":    answer.AuthorID,
				"transport":           "github",
			}))
	}

	// The architecture JSON remains owned by workflow.resolveArchitectureIn.
	// Transport authenticates the sender and subject, then returns the body
	// verbatim. Session is Unverified because GitHub proves no model/session
	// property, even though architect standing does not use reviewer independence.
	return agent.Result{Text: answer.Body, Session: roles.Unverified}, nil
}

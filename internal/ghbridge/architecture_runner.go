package ghbridge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
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
	// Doorbell, when set, rings a wake signal pointing at the published
	// request. Nil means the remote actor is expected to see the request
	// itself, which is the arrangement whenever its wake path admits the App.
	Doorbell Doorbell
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
	requestComment, err := PublishArchitectureRequest(ctx, r.Issue, request)
	if err != nil {
		return agent.Result{}, fmt.Errorf("posting the architecture request: %w", err)
	}

	// The request is durable from here. A doorbell failure is therefore NOT a
	// turn failure: the authoritative object exists on GitHub with its complete
	// binding, and the only thing missing is a nudge to look at it. Failing the
	// turn would discard a published request and, on the next attempt, mint a
	// second one for the same objective -- which is how a transport problem
	// turns into a duplicate governed turn. The locator is reported so a retry
	// can ring the SAME comment instead.
	if r.Doorbell != nil && emit != nil && requestComment <= 0 {
		emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentStarted,
			"the request was published but its comment id is unknown, so no doorbell can point at it",
			map[string]any{"request_id": request.RequestID, "transport": "github"}))
	} else if r.Doorbell != nil {
		if ringErr := r.Doorbell.Ring(ctx, requestComment); ringErr != nil && emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentStarted,
				"the doorbell did not ring; the request stands and a retry must ring comment "+
					strconv.FormatInt(requestComment, 10)+" rather than publish another",
				map[string]any{
					"request_id":      request.RequestID,
					"request_comment": requestComment,
					"error":           ringErr.Error(),
					"transport":       "github",
				}))
		}
	}

	if emit != nil {
		emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentStarted,
			"waiting for the remote architect to answer request "+request.RequestID+" on github",
			map[string]any{
				"request_id":         request.RequestID,
				"objective_digest":   r.Binding.ObjectiveDigest,
				"base":               r.Binding.BaseSHA,
				"graph_build_commit": r.Binding.GraphBuildCommit,
				"transport":          "github",
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
		// An exchange that ENDED must say so where an operator can see it.
		//
		// The bound was always enforced; the reporting half of
		// every_remote_exchange_is_bounded lived only in this returned error.
		// From outside the process an exchange that had given up therefore
		// looked exactly like one still waiting, and the engine's re-ask looked
		// like nothing at all. Distinguishing "no answer yet" from "no answer,
		// I stopped" by reading GitHub by hand cost hours, and every field
		// below was already known here at the moment it was needed.
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentFinished,
				"the architect turn ended without an answer after "+wait.String()+
					"; request "+request.RequestID+" stands and was not withdrawn",
				map[string]any{
					"request_id":         request.RequestID,
					"request_comment":    requestComment,
					"objective_digest":   r.Binding.ObjectiveDigest,
					"base":               r.Binding.BaseSHA,
					"graph_build_commit": r.Binding.GraphBuildCommit,
					"waited":             wait.String(),
					"outcome":            "unanswered",
					"reason":             err.Error(),
					"transport":          "github",
				}))
		}
		return agent.Result{}, err
	}

	if emit != nil {
		emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentFinished,
			"the remote architect answered request "+request.RequestID,
			map[string]any{
				"request_id":         request.RequestID,
				"objective_digest":   r.Binding.ObjectiveDigest,
				"base":               r.Binding.BaseSHA,
				"graph_build_commit": r.Binding.GraphBuildCommit,
				"github_author":      answer.Author,
				"github_author_id":   answer.AuthorID,
				"transport":          "github",
			}))
	}

	// The architecture JSON remains owned by workflow.resolveArchitectureIn.
	// Transport authenticates the sender and subject, then returns the body
	// verbatim. Session is Unverified because GitHub proves no model/session
	// property, even though architect standing does not use reviewer independence.
	return agent.Result{Text: answer.Body, Session: roles.Unverified}, nil
}

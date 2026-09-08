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
	// Exchanges, when its Dir is set, records this request for as long as a
	// waiter exists for it.
	//
	// The record is what makes the request's lifetime legible to a process that
	// did not publish it. A turn that ends here retracts its own request; a
	// process that DIES here cannot, and the record is the only thing that lets
	// the next startup do it instead (#162).
	Exchanges ExchangeLog
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

	// The exchange is recorded BEFORE the doorbell and before the wait, because
	// everything after this point can fail in a way that leaves the request
	// standing. Recording it after a successful wait would record only the
	// exchanges that never needed the record.
	//
	// A record that cannot be written is reported and the turn continues: the
	// request is already published, and failing the turn here would discard it
	// and mint a duplicate on the next attempt.
	if r.Exchanges.Dir != "" {
		rec := ExchangeRecord{
			TaskID:         req.TaskID,
			RequestID:      request.RequestID,
			RequestComment: requestComment,
			Conversation:   r.Issue.Number,
			PublishedAt:    time.Now().UTC(),
			Deadline:       time.Now().Add(r.waitFor()).UTC(),
		}
		if openErr := r.Exchanges.Open(rec); openErr != nil && emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentStarted,
				"the exchange record could not be written, so a restart cannot retract request "+
					request.RequestID,
				map[string]any{
					"request_id":      request.RequestID,
					"request_comment": requestComment,
					"error":           openErr.Error(),
					"transport":       "github",
				}))
		}
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

	wait := r.waitFor()
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
		// The exchange ENDED, so the request must stop claiming otherwise.
		//
		// This is the half #162 was opened for. The bound was always enforced;
		// what was missing is that the enforcement was invisible from outside
		// the process. A request left standing is indistinguishable from a live
		// one -- same marker, same complete binding -- so a consumer that
		// behaves correctly answers a question nobody is listening to, and a
		// re-ask leaves an equally live-looking predecessor behind.
		//
		// Retraction is attempted before the failure is reported so that the
		// report can say whether it succeeded. A retraction that fails leaves
		// the record open on purpose: the next startup will try again.
		retracted, retractErr := "withdrawn", error(nil)
		if r.Exchanges.Dir != "" {
			if retractErr = withdraw(ctx, r.Issue, ExchangeRecord{
				TaskID: req.TaskID, RequestID: request.RequestID, RequestComment: requestComment,
			}); retractErr == nil {
				retractErr = r.Exchanges.Close(req.TaskID, request.RequestID)
			}
		} else {
			// Untracked is not a bug, it is the older arrangement: with no
			// exchange record there is nothing to retract from, and the request
			// really does still stand. Saying so keeps the guidance a retry
			// needs -- ring the same request, do not publish a second one.
			retracted = "stands and was not withdrawn, because this turn kept no exchange record"
		}
		if retractErr != nil {
			retracted = "stands and was not withdrawn: " + retractErr.Error()
		}

		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceArchitect, event.AgentFinished,
				"the architect turn ended without an answer after "+wait.String()+
					"; request "+request.RequestID+" is "+retracted,
				map[string]any{
					"request_id":         request.RequestID,
					"request_comment":    requestComment,
					"objective_digest":   r.Binding.ObjectiveDigest,
					"base":               r.Binding.BaseSHA,
					"graph_build_commit": r.Binding.GraphBuildCommit,
					"waited":             wait.String(),
					"outcome":            "unanswered",
					"request_state":      retracted,
					"reason":             err.Error(),
					"transport":          "github",
				}))
		}
		return agent.Result{}, err
	}

	// Answered: the exchange is over and needs no retraction. Closing the record
	// is what keeps the next startup from withdrawing a request that was already
	// satisfied, which would tell a reader the turn failed when it succeeded.
	if r.Exchanges.Dir != "" {
		_ = r.Exchanges.Close(req.TaskID, request.RequestID)
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

// waitFor is the bound this turn will wait, so the deadline written into the
// exchange record and the deadline actually enforced cannot drift apart.
func (r *ArchitectureRunner) waitFor() time.Duration {
	if r.Wait <= 0 {
		return DefaultWait
	}
	return r.Wait
}

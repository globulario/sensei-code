// Command sensei-code-answerer answers governed architecture and advisory review
// requests standing on the sensei-code mailbox, unattended.
//
// It is the composition root and nothing more: configuration comes from the
// operator's environment (see internal/answerer), the contract validators are
// the workflow's own strict ones, and every mailbox reading and every reply is
// the bridge's own -- its request parsers, its renderers, its withdrawal
// parser and its answer predicates, ghbridge.ReviewAnswers among them -- all
// injected so the answerer carries no second copy of any contract or grammar.
// Reviews it posts are ADVISORY and satisfy no independent-review requirement.
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/globulario/sensei-code/internal/answerer"
	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/workflow"
)

func main() {
	a, err := answerer.New(answerer.ConfigFromEnv(os.Getenv), answerer.Validators{
		Architecture: workflow.StrictValidateArchitectureBody,
		Review:       workflow.StrictValidateReviewPayload,
	}, bridgeProtocol())
	if err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code-answerer:", err)
		os.Exit(2)
	}
	if err := a.Run(context.Background()); err != nil {
		fmt.Fprintln(os.Stderr, "sensei-code-answerer:", err)
		os.Exit(1)
	}
}

// bridgeProtocol adapts the bridge's canonical functions to the answerer. Each
// adapter calls the bridge and translates its typed result; none decides
// anything the bridge did not.
func bridgeProtocol() answerer.Protocol {
	return answerer.Protocol{
		ArchitectureRequest: func(body string) (answerer.CanonicalRequest, bool) {
			q, ok := ghbridge.ParseArchitectureRequest(body)
			if !ok {
				return answerer.CanonicalRequest{}, false
			}
			return answerer.CanonicalRequest{ID: q.RequestID, TaskID: q.Binding.TaskID, MailboxRepository: q.MailboxRepository}, true
		},
		ReviewRequest: func(body string) (answerer.CanonicalRequest, bool) {
			q, ok := ghbridge.ParseRequest(body)
			if !ok {
				return answerer.CanonicalRequest{}, false
			}
			return answerer.CanonicalRequest{ID: q.RequestID, TaskID: q.TaskID, MailboxRepository: q.MailboxRepository}, true
		},
		// The architecture reply is the bridge's ArchitectureResponse envelope
		// over the binding the bridge read from the request itself.
		ArchitectureReply: func(request, payload string) (string, error) {
			q, ok := ghbridge.ParseArchitectureRequest(request)
			if !ok {
				return "", fmt.Errorf("not an architecture request")
			}
			return ghbridge.ArchitectureResponse{Binding: q.Binding, RequestID: q.RequestID, Body: payload}.Marker()
		},
		// The review reply is the canonical review artifact over the identity
		// the bridge read from the request -- the same envelope the request
		// teaches, rendered by the one function that spells it.
		ReviewReply: func(request, payload string) (string, error) {
			q, ok := ghbridge.ParseRequest(request)
			if !ok {
				return "", fmt.Errorf("not a review request")
			}
			var reply ghbridge.MailboxReview
			reply.Artifact.ReviewerProvider = q.ReviewerProvider
			reply.Artifact.TaskID = q.TaskID
			reply.Artifact.RequestID = q.RequestID
			reply.Artifact.BaseSHA = q.BaseSHA
			reply.Artifact.CandidateDigest = q.CandidateDigest
			reply.Artifact.CandidateTree = q.CandidateTree
			reply.Artifact.ReviewCommit = q.ReviewCommit
			reply.Artifact.Body = payload
			return reply.Artifact.Render()
		},
		ArchitectureAnswers: func(request, reply string) bool {
			q, ok := ghbridge.ParseArchitectureRequest(request)
			if !ok {
				return false
			}
			if resp, ok := ghbridge.ParseArchitectureResponse(reply); ok && resp.Answers(q) {
				return true
			}
			refusal, err := ghbridge.ParseArchitectureRefusal(reply)
			return err == nil && refusal.Refuses(q)
		},
		ReviewAnswers: ghbridge.ReviewAnswers,
		Withdrawal: func(body string) (string, bool) {
			id, err := ghbridge.ParseWithdrawal(body)
			return id, err == nil
		},
	}
}

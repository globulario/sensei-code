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

// Runner answers a REVIEWER turn by asking a party that can read and write
// GitHub but cannot reach this machine.
//
// Deliberately the same shape as control.remoteRunner: it executes nothing,
// owns no worktree and no capability envelope, and converts a question into a
// published request and the reply back into the same text a command-line
// provider would have printed. Everything downstream — parsing, claim checking,
// contradiction, certifiability, the authority router — is the path it always
// was. A second review system would have to re-earn all of it.
//
// The mode returned is roles.Unverified, stamped HERE by the orchestrator from
// the fact that the turn was answered over a transport. It is never read from
// the reply, and no GitHub property may raise it.
type Runner struct {
	// Issue is the mailbox. A dedicated issue rather than a pull request, so
	// PR topology never becomes part of workflow semantics.
	Issue Issue
	// RepoDir is the repository the snapshot is built and pushed from.
	RepoDir string
	// Remote is the git remote the snapshot is pushed to.
	Remote string
	// NewRequestID mints a unique id per request, so a reply can name which
	// question it answers even when the same artifact is asked about twice.
	NewRequestID func() string
	// Poll is how often the mailbox is re-read. Zero uses a sane default.
	Poll time.Duration
	// SessionID identifies the engine session for emitted events.
	SessionID string
	// Wait bounds the whole exchange. Zero uses DefaultWait rather than waiting
	// indefinitely: the control path bounds its remote turns by a TTL, and a
	// bridge that blocked forever would let a task sit on a party that is never
	// going to answer.
	Wait time.Duration
	// Doorbell, when set, rings a wake pointing at the published review request.
	//
	// Its absence was the last place normal progress required a person. The
	// architect path has rung since #158; the reviewer path published a fully
	// bound request and then waited for somebody to notice it. On 2026-09-12 the
	// review request for task-1789217963311720667 sat unanswered because the
	// only thing that ever woke the remote was Dave typing "answer the pending
	// review" -- a human scheduler in the middle of a machine workflow.
	//
	// It is a NOTIFICATION and never the authority. The durable exchange below
	// decides whether a review is outstanding; this only asks someone to look.
	Doorbell Doorbell
	// Exchanges records the open review exchange so a restart can find it.
	//
	// Without it a published review request exists only on GitHub: this process
	// held the sole knowledge that it was waiting, so process death left the
	// request standing with nothing listening and no local trace. The architect
	// path has had this record; the reviewer path had none.
	Exchanges ExchangeLog
}

// ErrNotReviewer reports a turn this bridge does not serve.
var ErrNotReviewer = errors.New("the github bridge serves the reviewer role only")

// ErrUnboundSubject reports a turn with no exact artifact to review.
var ErrUnboundSubject = errors.New("no exact candidate binding to review")

// DefaultWait bounds an exchange whose Wait is unset. Finite by construction:
// there is no indefinite default here.
const DefaultWait = 30 * time.Minute

// Run publishes the exact candidate, asks for a review of it, and waits.
func (r *Runner) Run(ctx context.Context, req agent.Request, emit func(event.Event)) (agent.Result, error) {
	if req.Role != roles.Reviewer {
		return agent.Result{}, fmt.Errorf("%w: got %s", ErrNotReviewer, req.Role)
	}
	if r.NewRequestID == nil {
		return agent.Result{}, errors.New("the github bridge needs a request id source")
	}

	// The subject comes from the binding the engine already owns. Nothing here
	// consults the worktree, HEAD, or a pull request head: those are transport
	// artifacts, and using one as the subject is how content identity gets
	// replaced by whatever happens to be checked out.
	subject := FromBinding(req.Binding)
	if err := subjectBindingComplete(subject); err != nil {
		return agent.Result{}, fmt.Errorf("%w: %v", ErrUnboundSubject, err)
	}

	requestID := r.NewRequestID()
	snap, err := PublishSnapshot(ctx, r.RepoDir, r.Remote, subject, requestID)
	if err != nil {
		return agent.Result{}, fmt.Errorf("publishing the review snapshot: %w", err)
	}
	subject.ReviewCommit = snap.Commit

	request := Request{Subject: subject, RequestID: requestID, Kind: KindReview}
	// Published rather than posted, because the comment id is what a doorbell
	// points at. PostRequest discards it, and a wake that cannot name the
	// request it is about is a nudge to read the whole conversation.
	requestComment, err := PublishRequest(ctx, r.Issue, request, req.Prompt)
	if err != nil {
		return agent.Result{}, fmt.Errorf("posting the review request: %w", err)
	}

	// Recorded BEFORE the doorbell and before the wait, for the same reason the
	// architect path records first: everything after this point can fail in a
	// way that leaves the request standing, and a record written after a
	// successful wait would record only the exchanges that never needed one.
	//
	// A record that cannot be written is reported and the turn continues. The
	// request is already published; failing here would discard it and mint a
	// duplicate on the next attempt.
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
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
				"the review exchange record could not be written, so a restart cannot find request "+
					request.RequestID,
				map[string]any{
					"request_id":      request.RequestID,
					"request_comment": requestComment,
					"error":           openErr.Error(),
					"transport":       "github",
				}))
		}
	}

	// The request is durable from here, so a doorbell failure is NOT a turn
	// failure: the authoritative object exists with its complete binding and
	// only the nudge is missing. Failing the turn would discard a published
	// request and mint a second one for the same candidate on the next attempt,
	// which is how a notification problem becomes a duplicate governed review.
	if r.Doorbell != nil && emit != nil && requestComment <= 0 {
		emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
			"the review request was published but its comment id is unknown, so no doorbell can point at it",
			map[string]any{"request_id": request.RequestID, "transport": "github"}))
	} else if r.Doorbell != nil {
		if ringErr := r.Doorbell.Ring(ctx, requestComment); ringErr != nil && emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
				"the review doorbell did not ring; the request stands and a retry must ring comment "+
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
		emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
			"waiting for the remote reviewer to answer request "+requestID+" on github",
			map[string]any{
				"request_id":       requestID,
				"base":             subject.BaseSHA,
				"candidate_digest": subject.CandidateDigest,
				"candidate_tree":   subject.CandidateTree,
				"review_commit":    subject.ReviewCommit,
				"review_ref":       snap.Ref,
				"transport":        "github",
			}))
	}

	wait := r.waitFor()
	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	// A timeout leaves the task durable and pending. Nothing in this path
	// completes or discards work: the engine may ask again, and the next ask
	// carries a fresh request id and a fresh snapshot of whatever the candidate
	// is by then.
	review, err := AwaitReview(wctx, r.Issue, request, r.Poll)
	if err != nil {
		// Same reason as the architect runner: an exchange that ended must say
		// so where an operator can see it, or a review that gave up is
		// indistinguishable from one still waiting.
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentFinished,
				"the review turn ended without an answer after "+wait.String()+
					"; request "+requestID+" stands and was not withdrawn",
				map[string]any{
					"request_id":       requestID,
					"candidate_digest": subject.CandidateDigest,
					"candidate_tree":   subject.CandidateTree,
					"base":             subject.BaseSHA,
					"review_commit":    snap.Commit,
					"waited":           wait.String(),
					"outcome":          "unanswered",
					"reason":           err.Error(),
					"transport":        "github",
				}))
		}
		return agent.Result{}, err
	}

	if emit != nil {
		emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentFinished,
			"the remote reviewer answered request "+requestID,
			map[string]any{
				"request_id":     requestID,
				"candidate_tree": subject.CandidateTree,
				"review_commit":  subject.ReviewCommit,
				// The authenticated sender, recorded for the transcript. It
				// says WHO answered; it does not raise the session mode and it
				// is not the provider identity — those are separate facts.
				"github_author":    review.Author,
				"github_author_id": review.AuthorID,
				"transport":        "github",
			}))
	}

	// Body is passed through verbatim. This package does not interpret it: the
	// workflow's reviewer parser is the sole interpreter, which is why prose
	// cannot become a decision.
	return agent.Result{Text: review.Body, Session: roles.Unverified}, nil
}

// subjectBindingComplete checks the four identity fields the engine supplies,
// before a snapshot is built. ReviewCommit is filled by publication and so is
// deliberately not required here.
func subjectBindingComplete(s Subject) error {
	probe := s
	probe.ReviewCommit = "0000000000000000000000000000000000000000"
	return probe.Validate()
}

// waitFor is the single owner of this runner's deadline.
//
// The exchange record and the context timeout must agree: a record claiming a
// deadline the waiter does not honour would have a restart retract a request
// that was still live, or leave one standing that had already expired.
func (r *Runner) waitFor() time.Duration {
	if r.Wait > 0 {
		return r.Wait
	}
	return DefaultWait
}

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
	// Relays holds reviews relayed by a local operator. The runner only READS it:
	// a published relay bound to the owed request answers the turn, and nothing
	// here can create or publish one.
	Relays RelayStore
}

// ErrNotReviewer reports a turn this bridge does not serve.
var ErrNotReviewer = errors.New("the github bridge serves the reviewer role only")

// ErrUnboundSubject reports a turn with no exact artifact to review.
var ErrUnboundSubject = errors.New("no exact candidate binding to review")

// DefaultWait bounds an exchange whose Wait is unset. Finite by construction:
// there is no indefinite default here.
const DefaultWait = 30 * time.Minute

// supersedeOwedReviews retires every review this task still owes under an older
// request, before a new request is published.
//
// A task owes at most one review at a time, and the newest request is the one
// that owes it. Keeping an older request open beside it would give a relayed or
// late verdict two requests to satisfy, and the older one may be about a
// candidate that no longer exists. So each is WITHDRAWN on the conversation and
// then CLOSED locally.
//
// The local close happens even when the withdrawal cannot be posted: from here
// on this process accepts nothing for that request either way, and a record kept
// open "until GitHub says so" would leave a superseded request still satisfiable
// here. Both facts are reported, so a request still visibly standing on GitHub is
// never silent.
func (r *Runner) supersedeOwedReviews(ctx context.Context, taskID, keep string, emit func(event.Event)) {
	owed, listErr := r.Exchanges.PendingReviews()
	if listErr != nil && emit != nil {
		emit(event.New(r.SessionID, taskID, event.SourceReviewer, event.Status,
			"some review records could not be read before superseding: "+listErr.Error(),
			map[string]any{"error": listErr.Error(), "transport": "github"}))
	}
	for _, rec := range owed {
		// keep is the replacement, already published and recorded. Retiring it
		// here would leave the task owing a review that nothing names.
		if rec.TaskID != taskID || rec.RequestID == keep {
			continue
		}
		withdrawErr := withdraw(ctx, r.Issue, rec)
		closeErr := r.Exchanges.Close(rec.TaskID, rec.RequestID)
		if emit == nil {
			continue
		}
		payload := map[string]any{
			"superseded_request": rec.RequestID,
			"candidate_digest":   rec.CandidateDigest,
			"candidate_tree":     rec.CandidateTree,
			"withdrawn":          withdrawErr == nil,
			"closed":             closeErr == nil,
			"transport":          "github",
		}
		summary := "review request " + rec.RequestID + " is superseded by a new request for this task"
		if withdrawErr != nil {
			payload["withdraw_error"] = withdrawErr.Error()
			summary += "; its withdrawal could not be posted, so it may still stand on the conversation, " +
				"but it is no longer accepted here"
		}
		if closeErr != nil {
			payload["close_error"] = closeErr.Error()
		}
		emit(event.New(r.SessionID, taskID, event.SourceReviewer, event.Status, summary, payload))
	}
}

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

	// A review a local operator relayed for the owed request on THIS candidate
	// answers the turn without a new request. Checked before supersession, which
	// would retire the very request the relay is bound to.
	if result, found, err := r.relayedReviewFor(ctx, req, subject, emit); found {
		return result, err
	}

	// What this candidate already owes, read BEFORE anything is published and
	// retired only after the replacement is durable (below).
	//
	// Retiring first left a window with no durable obligation at all: withdraw
	// the old record, fail to publish the snapshot or the request, and the task
	// owes a review that nothing names. Worse, that failure was an ordinary
	// reviewer error, so the ladder was free to ask a different reviewer -- a
	// transport failure silently became a change of reviewer. Every failure
	// point from here on leaves at least one durable obligation standing.
	standing, owedStands := r.owedReviewFor(req.TaskID, subject)

	requestID := r.NewRequestID()
	snap, err := PublishSnapshot(ctx, r.RepoDir, r.Remote, subject, requestID)
	if err != nil {
		err = fmt.Errorf("publishing the review snapshot: %w", err)
		if owedStands {
			return agent.Result{}, owedInstead(standing, err)
		}
		return agent.Result{}, err
	}
	subject.ReviewCommit = snap.Commit

	// Both repositories are STATED on the request. The consumer must never have
	// to recover one from prose, a repository name, or which repo happens to
	// contain a commit -- that inference is what left r-3212791306b4607c
	// unanswered on 2026-09-12.
	request := Request{
		Subject:             subject,
		RequestID:           requestID,
		Kind:                KindReview,
		MailboxRepository:   r.Issue.MailboxRepository(),
		WorkspaceRepository: RemoteRepository(ctx, r.RepoDir, r.Remote),
	}
	// Published rather than posted, because the comment id is what a doorbell
	// points at. PostRequest discards it, and a wake that cannot name the
	// request it is about is a nudge to read the whole conversation.
	requestComment, err := PublishRequest(ctx, r.Issue, request, req.Prompt)
	if err != nil {
		err = fmt.Errorf("posting the review request: %w", err)
		if owedStands {
			return agent.Result{}, owedInstead(standing, err)
		}
		return agent.Result{}, err
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
			// A review request is an owed review on this exact candidate, so it
			// records the candidate: a restart keeps it, and a relayed verdict or
			// a continuation is checked against what the request actually carried.
			Kind:            ExchangeReview,
			BaseSHA:         subject.BaseSHA,
			CandidateDigest: subject.CandidateDigest,
			CandidateTree:   subject.CandidateTree,
			ReviewCommit:    snap.Commit,
		}
		openErr := r.Exchanges.Open(rec)
		if openErr != nil && emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
				"the review exchange record could not be written, so a restart cannot find request "+
					request.RequestID+"; any earlier obligation for this task is kept rather than superseded",
				map[string]any{
					"request_id":      request.RequestID,
					"request_comment": requestComment,
					"error":           openErr.Error(),
					"transport":       "github",
				}))
		}
		// Only now, with the replacement published AND durably recorded, is the
		// obligation it replaces retired. If the record could not be written, the
		// earlier records are the only durable statement that this candidate owes
		// a review, and they stand.
		if openErr == nil {
			r.supersedeOwedReviews(ctx, req.TaskID, request.RequestID, emit)
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
		// The wait's OWN deadline passed while the turn was still wanted: the
		// request is published, bound to this exact candidate, and unanswered.
		// That is a review still owed, not a provider that failed, so it gets its
		// own typed error carrying the identity a later process continues from.
		// A cancelled or expired PARENT context is a different fact -- a human
		// stop, an invocation budget -- and keeps its own error.
		if ctx.Err() == nil && (errors.Is(err, ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded)) {
			return agent.Result{}, &roles.ReviewUnanswered{
				RequestID:      requestID,
				RequestComment: requestComment,
				Conversation:   r.Issue.Number,
				Binding: roles.Binding{
					TaskID:          req.TaskID,
					BaseSHA:         subject.BaseSHA,
					CandidateDigest: subject.CandidateDigest,
					CandidateTree:   subject.CandidateTree,
				},
				ReviewCommit: snap.Commit,
				Waited:       wait,
				Cause:        err,
			}
		}
		return agent.Result{}, err
	}

	// An answered request is no longer owed, so its record is closed: a restart
	// must not keep an obligation that was discharged. A record that cannot be
	// closed is reported, not fatal -- the answer is real and is still returned.
	if r.Exchanges.Dir != "" {
		if closeErr := r.Exchanges.Close(req.TaskID, request.RequestID); closeErr != nil && emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.Status,
				"the answered review's exchange record could not be closed: "+closeErr.Error(),
				map[string]any{"request_id": requestID, "error": closeErr.Error(), "transport": "github"}))
		}
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
// owedReviewFor is the durable obligation this task already carries for THIS
// exact candidate, if it carries one.
func (r *Runner) owedReviewFor(taskID string, subject Subject) (ExchangeRecord, bool) {
	if r.Exchanges.Dir == "" {
		return ExchangeRecord{}, false
	}
	owed, _ := r.Exchanges.PendingReviews()
	for _, rec := range owed {
		if rec.TaskID == taskID && rec.BaseSHA == subject.BaseSHA &&
			rec.CandidateDigest == subject.CandidateDigest && rec.CandidateTree == subject.CandidateTree {
			return rec, true
		}
	}
	return ExchangeRecord{}, false
}

// owedInstead reports a transport failure that happened while an obligation for
// this candidate still stands.
//
// A request that could not be REPLACED is still a review owed, not a reviewer
// that failed. Reported as the owed review it left behind, the engine preserves
// the candidate at the review boundary; reported as an ordinary error, the
// ladder would ask a different reviewer, which is a transport failure quietly
// changing who judges the work.
func owedInstead(rec ExchangeRecord, cause error) *roles.ReviewUnanswered {
	return &roles.ReviewUnanswered{
		RequestID: rec.RequestID, RequestComment: rec.RequestComment, Conversation: rec.Conversation,
		Binding: roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA,
			CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree},
		ReviewCommit: rec.ReviewCommit,
		Cause: fmt.Errorf("the review this candidate is owed under request %s could not be replaced: %w",
			rec.RequestID, cause),
	}
}

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

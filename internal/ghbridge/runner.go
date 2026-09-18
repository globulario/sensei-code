package ghbridge

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
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
	// it reports a relay that exists and cannot be consumed yet, and nothing
	// here can create or publish one.
	Relays RelayStore
	// Reviews is the common durable record of what a reviewer produced, whatever
	// carried it. It is the SEMANTIC source of "which review answered this
	// request": a mailbox answer and a published relay converge here, so the
	// same reviewer bytes mean the same thing either way.
	Reviews reviewstore.Store
	// ReviewerProvider is the provider the workflow assigned for this turn.
	//
	// Set by the resolver from the engine's assignment, never from the mailbox,
	// the GitHub login or this process's configuration. It is stated on every
	// request and every answer must echo it.
	ReviewerProvider string
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

	// The assignment must be known BEFORE anything is published. A request that
	// could not name who was asked would accept an answer from anyone, and the
	// assignment cannot be recovered later from a login or from configuration.
	provider := wireProvider(r.ReviewerProvider)
	if !ProviderShape.MatchString(provider) {
		return agent.Result{}, fmt.Errorf("%w: the workflow assigned no usable reviewer provider (%q)",
			ErrUnassignedReviewer, r.ReviewerProvider)
	}

	// A review already accepted against the owed request for THIS candidate
	// answers the turn without a new request, whatever transport carried it.
	// Checked before supersession, which would retire the very request the
	// stored review is bound to.
	if result, found, err := r.storedReviewFor(req, subject, emit); found {
		return result, err
	}
	// A relay that exists and has not become the answer keeps its request owed
	// and says why. It returns no verdict: the common store above is the only
	// place an answer comes from, so a published relay whose convergence failed
	// leaves the candidate waiting rather than being answered from a second
	// source.
	if found, err := r.pendingRelayFor(req, subject); found {
		return agent.Result{}, err
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
		ReviewerProvider:    provider,
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
			// Who was asked, recorded with the obligation. A restart, or a relay
			// arriving after the configuration changed, checks the answer against
			// THIS rather than against whatever is assigned by then.
			ReviewerProvider: provider,
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

	// The exact reviewer bytes become the one durable semantic record BEFORE the
	// obligation is closed and before the answer is returned. A review consumed
	// but never recorded would be invisible to a restart, to an attestation, and
	// to the relay -- the asymmetry R2 exists to remove.
	//
	// A store this process does not keep is not a failure: the answer is real
	// and is still returned. What must never happen is recording something the
	// reviewer did not write, which is why the exact Raw bytes are handed over.
	if r.Reviews.Dir != "" {
		owedRec, _ := r.owedReviewFor(req.TaskID, subject)
		if owedRec.RequestID == "" {
			owedRec = ExchangeRecord{
				TaskID: req.TaskID, RequestID: request.RequestID, RequestComment: requestComment,
				Conversation: r.Issue.Number, BaseSHA: subject.BaseSHA,
				CandidateDigest: subject.CandidateDigest, CandidateTree: subject.CandidateTree,
				ReviewCommit: subject.ReviewCommit, ReviewerProvider: provider,
			}
		}
		if _, acceptErr := r.acceptReview(owedRec, review); acceptErr != nil {
			if emit != nil {
				emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.Status,
					"the answered review could not be recorded in the review store: "+acceptErr.Error(),
					map[string]any{"request_id": requestID, "review_digest": review.Artifact.Digest,
						"error": acceptErr.Error(), "transport": "github"}))
			}
			// A payload the reviewer parser refuses, or a conflicting artifact,
			// is not an answer. The obligation stands under the same request.
			return agent.Result{}, unconsumableReview(ExchangeRecord{
				TaskID: req.TaskID, RequestID: request.RequestID, RequestComment: requestComment,
				Conversation: r.Issue.Number, BaseSHA: subject.BaseSHA,
				CandidateDigest: subject.CandidateDigest, CandidateTree: subject.CandidateTree,
				ReviewCommit: subject.ReviewCommit,
			}, acceptErr)
		}
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

	// Body is passed through as the reviewer wrote it. This package does not
	// interpret it: the workflow's reviewer parser is the sole interpreter,
	// which is why prose cannot become a decision.
	//
	// The digest names the EXACT comment bytes, so a later record about this
	// review -- a human attestation, say -- can be checked against the review it
	// claims to be about rather than against its own say-so.
	return agent.Result{Text: review.Artifact.Body, Session: roles.Unverified,
		ReviewDigest: review.Artifact.Digest}, nil
}

// ErrUnassignedReviewer reports a review turn with no known assigned provider.
//
// A refusal rather than a default: publishing a request that cannot say who was
// asked would accept an answer from anyone who writes a well-formed envelope,
// and no later step can recover the assignment from a login.
var ErrUnassignedReviewer = errors.New("this review turn has no assigned reviewer provider")

// wireProvider is the one encoding of a provider name on the wire.
//
// The workflow assigns a display-shaped name ("ChatGPT"); the protocol
// vocabulary is lowercase. That is one fact in two spellings, not two facts, so
// it is normalized HERE, at the single point where an assignment becomes a
// published request.
func wireProvider(assigned string) string {
	return strings.ToLower(strings.TrimSpace(assigned))
}

// acceptReview records the exact reviewer bytes against the obligation they
// answer, in the one store every transport converges on.
//
// The reviewer payload is checked through the workflow's own parser FIRST, so a
// comment that is a well-formed envelope around prose never becomes a durable
// review. This package still does not interpret the verdict: it asks the one
// component that does.
func (r *Runner) acceptReview(rec ExchangeRecord, rev MailboxReview) (reviewstore.Record, error) {
	if r.Reviews.Dir == "" {
		return reviewstore.Record{}, errors.New("this process keeps no common review store")
	}
	binding := roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA,
		CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree}
	return r.Reviews.Accept(reviewstore.Acceptance{
		RequestID: rec.RequestID,
		Artifact:  rev.Artifact.Raw,
		Evidence: reviewstore.Evidence{
			Transport:      reviewstore.GitHubMailbox,
			GitHubAuthor:   rev.Author,
			GitHubAuthorID: rev.AuthorID,
			GitHubComment:  rev.Comment,
		},
		Validate: func(a reviewartifact.Artifact) error {
			_, err := workflow.ValidateReviewBody(a.Body, binding, a.ReviewerProvider)
			return err
		},
	})
}

// storedReviewFor answers a review turn from the common store, when the owed
// request for this exact candidate already has an accepted review.
//
// Transport-blind on purpose: the record may carry mailbox evidence, relay
// evidence or both, and the verdict and digest are the same either way. That
// equivalence is the whole point of the common store -- before it, a relayed
// review could answer a turn and the same bytes on the mailbox could not.
//
// TRANSPORT ESTABLISHES ACCEPTANCE ONCE. After a governed adapter has durably
// accepted a review, transport availability does not own that review's
// lifetime. So nothing here reads the mailbox: a deleted comment, an edited
// one, a moved conversation or an unreachable GitHub cannot retract a review
// that was authenticated, bound, validated and recorded. Re-proving delivery at
// consumption would make this store a cache of an external system rather than
// the durable record it exists to be.
//
// What IS re-derived, locally and every time: the artifact reparses, its digest
// recomputes over the exact stored bytes, the request id matches, every
// candidate identity field matches the durable obligation, the reviewer
// provider matches the one that obligation recorded, and the body still
// satisfies the workflow reviewer contract. The residue that cannot be
// reconstructed -- that these bytes were observed from the authenticated GitHub
// principal, or reached a published relay through the governed terminal path --
// was recorded at ingestion and is read as the historical fact it is.
//
// This does NOT make a hand-written .sensei-code/reviews/*.json impossible. The
// repository already trusts workspace-local governed state: the same writer
// could edit an attestation that OVERRIDES a review, the obligation records
// this checks against, the authority configuration, or the binary. Hardening
// that boundary is one question about all of those stores, tracked as #184, and
// signing this one drawer would only hide where the boundary actually is.
//
// It never CREATES an obligation. A record is consulted only against a request
// this workspace already owes.
func (r *Runner) storedReviewFor(req agent.Request, subject Subject, emit func(event.Event)) (agent.Result, bool, error) {
	if r.Reviews.Dir == "" || r.Exchanges.Dir == "" {
		return agent.Result{}, false, nil
	}
	owed, _ := r.Exchanges.PendingReviews()
	for _, rec := range owed {
		if rec.TaskID != req.TaskID || rec.BaseSHA != subject.BaseSHA ||
			rec.CandidateDigest != subject.CandidateDigest || rec.CandidateTree != subject.CandidateTree {
			continue
		}
		stored, found, err := r.Reviews.Load(rec.RequestID)
		if err != nil {
			return agent.Result{}, true, unconsumableReview(rec,
				fmt.Errorf("the stored review could not be read: %w", err))
		}
		if !found {
			continue
		}
		art, err := stored.Artifact()
		if err != nil {
			return agent.Result{}, true, unconsumableReview(rec, err)
		}
		// Every identity field, again, at the moment of consumption. The store
		// proved the record is internally consistent; this proves it is about
		// the candidate THIS turn is asking about.
		if m := subjectMismatch(rec.Subject(), subjectOf(art)); m != "" {
			return agent.Result{}, true, unconsumableReview(rec, errors.New(m))
		}
		if !sameProvider(art.ReviewerProvider, rec.ReviewerProvider) {
			return agent.Result{}, true, unconsumableReview(rec, fmt.Errorf(
				"it names reviewer %q and request %s was assigned to %q",
				art.ReviewerProvider, rec.RequestID, rec.ReviewerProvider))
		}
		// The reviewer's own contract, re-read every time from the stored
		// bytes. The parser that decides what a review SAYS is the same one
		// that let it in, so a record cannot become consumable because the
		// contract moved after it was accepted.
		binding := roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA,
			CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree}
		if _, err := workflow.ValidateReviewBody(art.Body, binding, art.ReviewerProvider); err != nil {
			return agent.Result{}, true, unconsumableReview(rec, err)
		}
		if err := r.Exchanges.Close(rec.TaskID, rec.RequestID); err != nil && emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.Status,
				"the consumed review's exchange record could not be closed: "+err.Error(),
				map[string]any{"request_id": rec.RequestID, "error": err.Error(), "transport": "github"}))
		}
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentFinished,
				fmt.Sprintf("the verdict reviewer %s produced for request %s is consumed from the review store "+
					"with advisory standing", art.ReviewerProvider, rec.RequestID),
				map[string]any{
					"request_id":        rec.RequestID,
					"reviewer_provider": art.ReviewerProvider,
					"review_digest":     stored.ReviewDigest,
					"standing":          stored.Standing,
					"transport_evidence": func() []string {
						var out []string
						for _, ev := range stored.Evidence {
							out = append(out, string(ev.Transport))
						}
						return out
					}(),
					"base":             rec.BaseSHA,
					"candidate_digest": rec.CandidateDigest,
					"candidate_tree":   rec.CandidateTree,
					"review_commit":    rec.ReviewCommit,
					"transport":        "github",
				}))
		}
		return agent.Result{Text: art.Body, Session: roles.Unverified, ReviewDigest: stored.ReviewDigest}, true, nil
	}
	return agent.Result{}, false, nil
}

// subjectOf lifts the candidate identity a canonical artifact carries.
func subjectOf(a reviewartifact.Artifact) Subject {
	return Subject{TaskID: a.TaskID, BaseSHA: a.BaseSHA, CandidateDigest: a.CandidateDigest,
		CandidateTree: a.CandidateTree, ReviewCommit: a.ReviewCommit}
}

// unconsumableReview reports a review that exists for an owed request and could
// not be consumed. The obligation stands under the SAME request rather than
// being superseded, so the candidate waits instead of being asked about twice.
func unconsumableReview(rec ExchangeRecord, cause error) error {
	return &roles.ReviewUnanswered{
		RequestID: rec.RequestID, RequestComment: rec.RequestComment, Conversation: rec.Conversation,
		Binding: roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA,
			CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree},
		ReviewCommit: rec.ReviewCommit,
		Cause:        fmt.Errorf("a review for request %s exists and was not consumed: %w", rec.RequestID, cause),
	}
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

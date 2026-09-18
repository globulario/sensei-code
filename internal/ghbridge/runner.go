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
	// Reviews is the ONE durable record of what a reviewer produced, whatever
	// carried it. It is the semantic source of "which review answered this
	// request": a mailbox answer and a relayed one converge here, so the same
	// reviewer bytes mean the same thing either way.
	//
	// Read only, and read ONCE per turn. Before #182 R6 this runner also asked a
	// separate relay store whether a review existed, which made "what answers
	// this request" a question with two sources that had to be consulted in the
	// right order to agree.
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

// Run answers a reviewer turn against the review this candidate is owed.
//
// The obligation is resolved FIRST, before anything is minted or published,
// because what is owed is a durable fact and this turn is only the latest
// process to come looking for it. A waiter that timed out, a process that died
// and a doorbell that failed to ring all leave the same request standing; the
// only things that end one are an exact answer, or an explicit supersession
// because the candidate or the reviewer assignment actually changed.
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

	// What this task actively owes, from the one component that decides it.
	// A conflict or an unreadable record fails the turn closed: two outstanding
	// requests for one task is not something to pick between, and an obligation
	// that cannot be read is not the absence of one.
	obligations := r.obligations()
	current, have, err := obligations.Current(req.TaskID)
	if err != nil {
		return agent.Result{}, err
	}

	replacing := ""
	if have && current.IsAbout(subject) {
		// A1. ONE store read decides everything a stored review can decide: a
		// delivered review answers the turn, a staged one keeps the same request
		// standing, and absence falls through to reattach or replace.
		if result, found, cerr := r.storedReviewFor(current, req, emit); found {
			return result, cerr
		}
		switch {
		case !current.AssignedTo(provider):
			// A4. A different reviewer is a different obligation.
			replacing = fmt.Sprintf("the workflow now assigns this review to %s and request %s was published to %s",
				provider, current.RequestID, current.ReviewerProvider)
		default:
			// A3. Same candidate, same reviewer: REATTACH.
			if rerr := current.Reattachable(); rerr != nil {
				// A pre-R4 obligation that never recorded who could answer it.
				// The gap is not filled from today's config; the obligation is
				// replaced explicitly and loudly instead.
				replacing = "request " + current.RequestID + " predates the pinned mailbox principal (" +
					rerr.Error() + "), so it is replaced rather than reattached"
			} else {
				return r.reattachTo(ctx, current, req, emit)
			}
		}
	} else if have {
		// B. The candidate moved. A changed candidate is a different obligation.
		replacing = fmt.Sprintf("candidate %s -> %s since request %s was published",
			shortCandidate(current.CandidateDigest), shortCandidate(subject.CandidateDigest), current.RequestID)
	}

	// C (and the explicit replacement paths). Publish first, record second,
	// retire the predecessor only once its successor is durable. Every failure
	// point below leaves at least one durable obligation standing, because a
	// window with none would let the ladder quietly ask a different reviewer.
	requestID := r.NewRequestID()
	snap, err := PublishSnapshot(ctx, r.RepoDir, r.Remote, subject, requestID)
	if err != nil {
		err = fmt.Errorf("publishing the review snapshot: %w", err)
		if have {
			return agent.Result{}, obligationUnreplaced(current, err)
		}
		return agent.Result{}, err
	}
	subject.ReviewCommit = snap.Commit

	// Both repositories are STATED on the request. The consumer must never have
	// to recover one from prose, a repository name, or which repo happens to
	// contain a commit -- that inference is what left r-3212791306b4607c
	// unanswered on 2026-09-12.
	published := ReviewObligation{
		TaskID: req.TaskID, RequestID: requestID, Conversation: r.Issue.Number,
		PublishedAt:     time.Now().UTC(),
		BaseSHA:         subject.BaseSHA,
		CandidateDigest: subject.CandidateDigest,
		CandidateTree:   subject.CandidateTree,
		ReviewCommit:    snap.Commit,
		// Who was asked, and which account may answer. Two different facts about
		// two different parties, both pinned here so no later configuration can
		// rewrite either for a request that is already standing.
		ReviewerProvider:    provider,
		ExpectedReviewer:    r.Issue.ExpectedReviewer,
		MailboxRepository:   r.Issue.MailboxRepository(),
		WorkspaceRepository: RemoteRepository(ctx, r.RepoDir, r.Remote),
	}
	requestComment, publisher, err := PublishRequest(ctx, r.Issue, published.Request(), req.Prompt)
	if err != nil {
		err = fmt.Errorf("posting the review request: %w", err)
		if have {
			return agent.Result{}, obligationUnreplaced(current, err)
		}
		return agent.Result{}, err
	}
	published.RequestComment = requestComment
	// Pinned with the rest of the obligation's identity: who was asked, which
	// account may answer, and which account spoke for this machine. All three
	// are facts about THIS request, and none may be recovered later from
	// configuration.
	published.Publisher = publisher

	// A NEW APP-PUBLISHED REQUEST WITHOUT ITS PUBLISHER FAILS CLOSED.
	//
	// An obligation with no pinned publisher is a LEGACY shape: it predates the
	// field, it can authenticate no receipt, and its relay recovery path refuses
	// rather than guesses. That treatment is right for records written before
	// the field existed and wrong for one written now -- GitHub answered, this
	// process simply could not read an author out of the answer, and recording
	// the obligation anyway would manufacture a legacy-shaped record today and
	// quietly disable the authority check for its whole lifetime.
	//
	// The gh CLI path is exempt because it names no author at all and publishes
	// no relay: a relay there is refused before it reaches this question.
	if r.Issue.API != nil && r.Issue.API.Configured() && !published.Publisher.Configured() {
		unpinned := fmt.Errorf("request %s was published as comment %d and github named no author for it, "+
			"so this obligation would be unable to authenticate its own publications",
			published.RequestID, requestComment)
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
				"review request "+published.RequestID+" was published and its publisher could not be established, "+
					"so it is not recorded: "+unpinned.Error(),
				map[string]any{
					"request_id": published.RequestID, "request_comment": requestComment,
					"error": unpinned.Error(), "transport": "github",
				}))
		}
		if have {
			return agent.Result{}, obligationUnreplaced(current, unpinned)
		}
		return agent.Result{}, fmt.Errorf("%w: %v", roles.ErrReviewUnrecordable, unpinned)
	}

	// Recorded BEFORE the doorbell and before the wait: everything after this
	// point can fail in a way that leaves the request standing, and a record
	// written after a successful wait would record only the exchanges that never
	// needed one.
	// NO WAITER MAY ATTACH TO A REQUEST THAT IS NOT DURABLE.
	//
	// The published value above lives only in memory until this succeeds. A
	// waiter on it would listen to a request no later process can find: on
	// timeout it would report an owed review naming an id nothing recorded, and
	// after a restart the next run would mint again -- the pre-R4 defect,
	// reintroduced by a storage fault.
	if openErr := obligations.Open(published, time.Now().Add(r.waitFor()).UTC()); openErr != nil {
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
				"review request "+published.RequestID+" was published and its obligation could not be recorded, "+
					"so nothing waits on it: "+openErr.Error(),
				map[string]any{
					"request_id": published.RequestID, "request_comment": requestComment,
					"error": openErr.Error(), "transport": "github",
				}))
		}
		if have {
			// The predecessor is still the durable truth. Report IT, not the
			// successor nothing recorded.
			return agent.Result{}, obligationUnreplaced(current, openErr)
		}
		// Nothing durable names this candidate's review. Stopping here is the
		// point: the request exists remotely for an operator to see, and another
		// reviewer would only publish a second one beside it.
		return agent.Result{}, fmt.Errorf("%w: %s was published as comment %d and the record could not be written: %v",
			roles.ErrReviewUnrecordable, published.RequestID, requestComment, openErr)
	}
	// Only now, with the replacement published AND durable, is its predecessor
	// retired -- and only for the reasons that actually justify one.
	if have {
		if err := r.supersede(ctx, current, published, replacing, emit); err != nil {
			// FAIL CLOSED. Two obligations are now active and the owner's own
			// rule is that neither may be chosen, so this turn does not get to
			// consume or wait through the one it happens to be holding.
			return agent.Result{}, err
		}
	}
	return r.attachWaiter(ctx, published, req, emit, true)
}

// reattachTo attaches a new waiter to an obligation that is already standing.
//
// The whole reattachment path, in one function, so that "a second waiter
// creates nothing" is a property something can check rather than a claim in a
// comment. Nothing here mints a request id, pushes a snapshot, posts a request
// or retires an obligation: the request already exists, the projection already
// exists, and the only new thing in the world is a process listening.
func (r *Runner) reattachTo(ctx context.Context, o ReviewObligation, req agent.Request,
	emit func(event.Event)) (agent.Result, error) {

	// Configuration moved, the obligation did not. Publishing a new request
	// because this process is pointed elsewhere would ask the same question
	// twice in two conversations, and withdrawing there would post a retraction
	// to a conversation that never carried the request.
	if reach := o.ReachableFrom(r.Issue); reach != nil {
		return agent.Result{}, obligationStands(o, reach)
	}
	return r.attachWaiter(ctx, o, req, emit, false)
}

// obligations is the one owner of review-obligation lifetime for this runner.
func (r *Runner) obligations() ReviewObligationStore {
	return ReviewObligationStore{Exchanges: r.Exchanges}
}

// attachWaiter watches the mailbox for the answer to ONE standing obligation.
//
// fresh says whether this waiter published the request it is watching. Either
// way the obligation is the same durable thing, and this function may not
// create, replace or retire one -- it only observes, and hands what it sees to
// the discharge path.
func (r *Runner) attachWaiter(ctx context.Context, o ReviewObligation, req agent.Request,
	emit func(event.Event), fresh bool) (agent.Result, error) {

	// A doorbell is a NOTIFICATION. It points at the request comment that
	// already exists -- the same one on every reattachment -- and its failure is
	// never a reason to publish anything.
	if r.Doorbell != nil {
		switch {
		case o.RequestComment <= 0:
			if emit != nil {
				emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
					"review request "+o.RequestID+" has no known comment id, so no doorbell can point at it",
					map[string]any{"request_id": o.RequestID, "transport": "github"}))
			}
		default:
			if ringErr := r.Doorbell.Ring(ctx, o.RequestComment); ringErr != nil && emit != nil {
				emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted,
					"the review doorbell did not ring; request "+o.RequestID+" stands and a retry must ring comment "+
						strconv.FormatInt(o.RequestComment, 10)+" rather than publish another",
					map[string]any{
						"request_id": o.RequestID, "request_comment": o.RequestComment,
						"error": ringErr.Error(), "transport": "github",
					}))
			}
		}
	}

	wait := r.waitFor()
	if emit != nil {
		summary := "waiting for the remote reviewer to answer request " + o.RequestID + " on github"
		if !fresh {
			summary = "reattaching to standing review request " + o.RequestID +
				"; the waiter is new, the obligation is not"
		}
		emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentStarted, summary,
			map[string]any{
				"request_id": o.RequestID, "request_comment": o.RequestComment,
				"base": o.BaseSHA, "candidate_digest": o.CandidateDigest,
				"candidate_tree": o.CandidateTree, "review_commit": o.ReviewCommit,
				"reviewer_provider": o.ReviewerProvider,
				"reattached":        !fresh,
				"waiter_deadline":   time.Now().Add(wait).UTC(),
				"transport":         "github",
			}))
	}

	wctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()

	// Authenticated against the principal PINNED ON THE OBLIGATION, never
	// against this process's current configuration.
	review, err := AwaitReview(wctx, r.Issue, o, r.Poll)
	if err != nil {
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentFinished,
				"this waiter ended without an answer after "+wait.String()+
					"; request "+o.RequestID+" remains the review still owed",
				map[string]any{
					"request_id": o.RequestID, "candidate_digest": o.CandidateDigest,
					"candidate_tree": o.CandidateTree, "base": o.BaseSHA,
					"review_commit": o.ReviewCommit, "waited": wait.String(),
					"outcome": "unanswered", "reason": err.Error(), "transport": "github",
				}))
		}
		// A cancelled or expired PARENT context is checked FIRST, and keeps its
		// own identity. The caller stopped this run -- a human, an invocation
		// budget -- and that is not a reviewer who failed to answer. Reported as
		// ErrNoAnswer it would reach the reviewer ladder as this provider's
		// failure, and the next reviewer would be asked for a review the caller
		// had just cancelled. errors.Is must still match context.Canceled, so
		// the cause is WRAPPED rather than formatted.
		if cerr := ctx.Err(); cerr != nil {
			return agent.Result{}, fmt.Errorf(
				"the review wait was ended by its caller; request %s remains the review this candidate is owed: %w",
				o.RequestID, cerr)
		}
		// THIS waiter's deadline passed while the turn was still wanted, and
		// nothing relevant was observed. The request is published, bound to this
		// exact candidate, and unanswered: a review still owed, not a provider
		// that failed.
		//
		// An OBSERVATION FAULT deliberately does not match here. It wraps only
		// its own condition, so it falls through unchanged and reaches the
		// caller as itself -- reported as unanswered it would tell an operator to
		// wait for a reviewer who has already replied, with the unusable reply
		// sitting in the conversation unmentioned. There is no separate branch
		// for it because a branch that cannot change the outcome is a guard
		// nothing can test.
		if errors.Is(err, ErrNoAnswer) || errors.Is(err, context.DeadlineExceeded) {
			return agent.Result{}, obligationWaited(o, wait, err)
		}
		return agent.Result{}, err
	}

	return r.dischargeWith(o, req, review, emit)
}

// dischargeWith records the exact reviewer bytes and only then retires the
// obligation and releases the verdict.
//
// STRICT ORDERING, and it is the point. A verdict that escaped before the
// obligation was discharged would let the session say REVIEWED while the
// obligation store still said OWED -- and the next run would ask the reviewer
// again for a review that had already been given. So nothing leaves here until
// both the store holds the bytes and the obligation is retired; if the discharge
// fails, the review is reported as existing-but-not-yet-consumable and the next
// attempt reads it back out of the store rather than asking anybody.
func (r *Runner) dischargeWith(o ReviewObligation, req agent.Request, review MailboxReview,
	emit func(event.Event)) (agent.Result, error) {

	if r.Reviews.Dir != "" {
		if _, acceptErr := r.acceptReview(o, review); acceptErr != nil {
			if emit != nil {
				emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.Status,
					"the answered review could not be recorded in the review store: "+acceptErr.Error(),
					map[string]any{"request_id": o.RequestID, "review_digest": review.Artifact.Digest,
						"error": acceptErr.Error(), "transport": "github"}))
			}
			// A DIFFERENT canonical review already answers this request. That is
			// an observation about the mailbox, not a review that failed to
			// arrive: the stored artifact stays byte-identical, the offered one
			// is not stored, and nothing is discharged.
			if errors.Is(acceptErr, reviewstore.ErrConflict) {
				existing := ""
				if stored, found, lerr := r.Reviews.Load(o.RequestID); lerr == nil && found {
					existing = stored.ReviewDigest
				}
				return agent.Result{}, observationFault(o, []observation{{
					kind: roles.ObservedConflict, comment: review.Comment,
					author: review.Author, authorID: review.AuthorID,
					bodyDigest: bodyDigestOf(review.Artifact.Raw),
					bytes:      len(review.Artifact.Raw),
					artifact:   &review.Artifact,
					mismatch:   "review_digest is " + review.Artifact.Digest + " and this request is answered by " + existing,
					diagnostic: acceptErr.Error(),
				}})
			}
			// A payload the reviewer parser refuses is not an answer and
			// discharges nothing.
			return agent.Result{}, obligationStands(o, acceptErr)
		}
	}

	if err := r.obligations().Discharge(o); err != nil {
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.Status,
				"the answered review is recorded and its obligation could not be discharged, so the verdict is "+
					"withheld until it is: "+err.Error(),
				map[string]any{"request_id": o.RequestID, "review_digest": review.Artifact.Digest,
					"error": err.Error(), "transport": "github"}))
		}
		return agent.Result{}, obligationStands(o, fmt.Errorf(
			"the review is recorded and request %s could not be discharged: %w", o.RequestID, err))
	}

	if emit != nil {
		emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentFinished,
			"the remote reviewer answered request "+o.RequestID,
			map[string]any{
				"request_id":     o.RequestID,
				"candidate_tree": o.CandidateTree,
				"review_commit":  o.ReviewCommit,
				// The authenticated sender, recorded for the transcript. It
				// says WHO answered; it does not raise the session mode and it
				// is not the provider identity -- those are separate facts.
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

// supersede retires an obligation that an already-durable successor replaces.
//
// EXACT, old -> new, and only for a reason that justifies one. It does not
// sweep every record for the task, because a waiter starting is not a reason to
// retire anything.
//
// AUTHORITY LEADS TRANSPORT. The local obligation is retired FIRST, and the
// remote withdrawal is posted only after that succeeds. Withdrawing first put
// transport ahead of authority: when retirement then failed, the old request was
// already retracted on the conversation while the durable owner still said it
// was active -- two local obligations, one of them pointing at a request that no
// longer stands remotely, and no way to tell from the records which fact was
// stale. Nothing is retracted for an obligation this process still owes.
//
// If the WITHDRAWAL fails afterwards, the old request may remain visible on the
// conversation. That is reported and is not a failure of the transition: locally
// it is retired and accepted by nothing, which is the state that decides
// behaviour.
func (r *Runner) supersede(ctx context.Context, old, replacement ReviewObligation, reason string,
	emit func(event.Event)) error {

	if closeErr := r.obligations().Retire(old); closeErr != nil {
		if emit != nil {
			emit(event.New(r.SessionID, old.TaskID, event.SourceReviewer, event.Status,
				"review request "+replacement.RequestID+" was published and "+old.RequestID+
					" could not be retired, so nothing was withdrawn and this task now has two active "+
					"review obligations: "+closeErr.Error(),
				map[string]any{
					"superseded_request": old.RequestID, "request_id": replacement.RequestID,
					"reason": reason, "withdrawn": false, "closed": false,
					"close_error": closeErr.Error(), "transport": "github",
				}))
		}
		return supersessionIncomplete(old, replacement, closeErr)
	}

	withdrawErr := withdraw(ctx, r.Issue, old.record(time.Time{}))
	if emit == nil {
		return nil
	}
	payload := map[string]any{
		"superseded_request":        old.RequestID,
		"request_id":                replacement.RequestID,
		"reason":                    reason,
		"recorded_candidate_digest": old.CandidateDigest,
		"candidate_digest":          replacement.CandidateDigest,
		"recorded_candidate_tree":   old.CandidateTree,
		"candidate_tree":            replacement.CandidateTree,
		"superseded_provider":       old.ReviewerProvider,
		"reviewer_provider":         replacement.ReviewerProvider,
		"withdrawn":                 withdrawErr == nil,
		"closed":                    true,
		"transport":                 "github",
	}
	summary := "review request " + old.RequestID + " is superseded by " + replacement.RequestID + ": " + reason
	if withdrawErr != nil {
		payload["withdraw_error"] = withdrawErr.Error()
		summary += "; it is retired here and its withdrawal could not be posted, so it may still stand on the " +
			"conversation while being accepted by nothing"
	}
	emit(event.New(r.SessionID, old.TaskID, event.SourceReviewer, event.Status, summary, payload))
	return nil
}

// supersessionIncomplete reports a transition that created its successor and
// could not retire its predecessor.
//
// The task now has two active obligations, which is the state the owner refuses
// to choose within. Continuing to wait on the successor would be choosing, so
// the turn stops at the transition instead.
func supersessionIncomplete(old, replacement ReviewObligation, cause error) error {
	return fmt.Errorf("%w: %s was published and %s could not be retired (%v), so this task now has two active "+
		"review obligations and neither may be acted through until that is repaired",
		ErrObligationConflict, replacement.RequestID, old.RequestID, cause)
}

func shortCandidate(d string) string {
	if len(d) > 14 {
		return d[:14]
	}
	return d
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
func (r *Runner) acceptReview(o ReviewObligation, rev MailboxReview) (reviewstore.Record, error) {
	if r.Reviews.Dir == "" {
		return reviewstore.Record{}, errors.New("this process keeps no common review store")
	}
	binding := roles.Binding{TaskID: o.TaskID, BaseSHA: o.BaseSHA,
		CandidateDigest: o.CandidateDigest, CandidateTree: o.CandidateTree}
	return r.Reviews.Accept(reviewstore.Acceptance{
		RequestID: o.RequestID,
		Artifact:  rev.Artifact.Raw,
		Evidence: reviewstore.Evidence{
			// READY on arrival. A mailbox read is one-phase: these bytes were
			// taken FROM a published comment, so there is no delivery still to
			// complete and nothing here may be staged.
			Transport:      reviewstore.GitHubMailbox,
			State:          reviewstore.Ready,
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

// storedReviewFor answers a review turn from the common store, when the
// obligation this task owes already has an accepted review.
//
// It is handed the obligation rather than scanning for one: what is owed is the
// obligation owner's to decide, and a consumer that re-derived it would be a
// second lifecycle rule (#182 R4).
//
// Transport-blind on purpose: the record may carry mailbox evidence, relay
// evidence or both, and the verdict and digest are the same either way.
//
// TRANSPORT ESTABLISHES ACCEPTANCE ONCE. After a governed adapter has durably
// accepted a review, transport availability does not own that review's
// lifetime, so nothing here reads the mailbox: a deleted comment, an edited
// one, a moved conversation or an unreachable GitHub cannot retract a review
// that was authenticated, bound, validated and recorded.
//
// What IS re-derived, locally and every time: the artifact reparses, its digest
// recomputes over the exact stored bytes, the request id matches, every
// candidate identity field matches the obligation, the reviewer provider
// matches the one that obligation recorded, and the body still satisfies the
// workflow reviewer contract.
//
// The provider compared against is the OBLIGATION's, never today's assignment:
// a review already accepted for the question that was actually asked is not
// invalidated because the workflow would ask someone else now.
func (r *Runner) storedReviewFor(o ReviewObligation, req agent.Request, emit func(event.Event)) (agent.Result, bool, error) {
	if r.Reviews.Dir == "" {
		return agent.Result{}, false, nil
	}
	stored, found, err := r.Reviews.Load(o.RequestID)
	if err != nil {
		return agent.Result{}, true, obligationStands(o, fmt.Errorf("the stored review could not be read: %w", err))
	}
	if !found {
		return agent.Result{}, false, nil
	}
	art, err := stored.Artifact()
	if err != nil {
		return agent.Result{}, true, obligationStands(o, err)
	}
	// RECORD PRESENCE IS NOT CONSUMABILITY.
	//
	// A relay stages the reviewer's exact bytes before the App has published
	// them, so a record can exist while no governed ingestion of it has
	// completed. The question is the store's to answer, and it is asked BEFORE
	// anything is validated or discharged: a review nobody delivered must not
	// discharge the obligation that is still waiting for it.
	//
	// Not silence, either. The bytes are demonstrably here, and saying "no
	// answer arrived" would send an operator to wait for a review this process
	// is holding (#182 R5/R6).
	if !stored.Consumable() {
		return agent.Result{}, true, deliveryPending(o, stored, art)
	}
	if m := subjectMismatch(o.Subject(), subjectOf(art)); m != "" {
		return agent.Result{}, true, obligationStands(o, errors.New(m))
	}
	if !sameProvider(art.ReviewerProvider, o.ReviewerProvider) {
		return agent.Result{}, true, obligationStands(o, fmt.Errorf(
			"it names reviewer %q and request %s was published to %q",
			art.ReviewerProvider, o.RequestID, o.ReviewerProvider))
	}
	binding := roles.Binding{TaskID: o.TaskID, BaseSHA: o.BaseSHA,
		CandidateDigest: o.CandidateDigest, CandidateTree: o.CandidateTree}
	if _, err := workflow.ValidateReviewBody(art.Body, binding, art.ReviewerProvider); err != nil {
		return agent.Result{}, true, obligationStands(o, err)
	}

	// Discharged before the verdict is released, for the same reason the fresh
	// path discharges before returning: a consumed review whose obligation still
	// stands would be asked for again.
	if err := r.obligations().Discharge(o); err != nil {
		return agent.Result{}, true, obligationStands(o, fmt.Errorf(
			"the review is recorded and request %s could not be discharged: %w", o.RequestID, err))
	}
	if emit != nil {
		emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentFinished,
			fmt.Sprintf("the verdict reviewer %s produced for request %s is consumed from the review store "+
				"with advisory standing", art.ReviewerProvider, o.RequestID),
			map[string]any{
				"request_id":        o.RequestID,
				"reviewer_provider": art.ReviewerProvider,
				"review_digest":     stored.ReviewDigest,
				"standing":          stored.Standing,
				// ONLY DELIVERED EVIDENCE ESTABLISHED THIS ACCEPTANCE. A staged
				// row is reported beside it, never within it: an incomplete
				// delivery listed as acceptance evidence would read, later, as a
				// transport that established something it did not.
				"transport_evidence": deliveredTransports(stored),
				"staged_evidence":    stagedTransports(stored),
				"base":               o.BaseSHA,
				"candidate_digest":   o.CandidateDigest,
				"candidate_tree":     o.CandidateTree,
				"review_commit":      o.ReviewCommit,
				"transport":          "github",
			}))
	}
	return agent.Result{Text: art.Body, Session: roles.Unverified, ReviewDigest: stored.ReviewDigest}, true, nil
}

// deliveredTransports names the transports whose governed ingestion completed.
func deliveredTransports(rec reviewstore.Record) []string {
	var out []string
	for _, ev := range rec.Evidence {
		if ev.Delivered() {
			out = append(out, string(ev.Transport))
		}
	}
	return out
}

// stagedTransports names the transports still mid-delivery, for diagnostics.
func stagedTransports(rec reviewstore.Record) []string {
	var out []string
	for _, ev := range rec.Staged() {
		out = append(out, string(ev.Transport))
	}
	return out
}

// subjectOf lifts the candidate identity a canonical artifact carries.
func subjectOf(a reviewartifact.Artifact) Subject {
	return Subject{TaskID: a.TaskID, BaseSHA: a.BaseSHA, CandidateDigest: a.CandidateDigest,
		CandidateTree: a.CandidateTree, ReviewCommit: a.ReviewCommit}
}

// obligationStands reports that this turn could not be answered and the review
// is still owed under the SAME request.
//
// Not a provider failure and not an abandoned candidate: a review owed. The
// engine preserves the candidate at the review boundary on this error, and the
// next process reattaches to the identity carried here rather than asking
// anybody again.
func obligationStands(o ReviewObligation, cause error) error {
	return &roles.ReviewUnanswered{
		RequestID: o.RequestID, RequestComment: o.RequestComment, Conversation: o.Conversation,
		Binding: roles.Binding{TaskID: o.TaskID, BaseSHA: o.BaseSHA,
			CandidateDigest: o.CandidateDigest, CandidateTree: o.CandidateTree},
		ReviewCommit: o.ReviewCommit,
		Cause:        fmt.Errorf("request %s remains the review this candidate is owed: %w", o.RequestID, cause),
	}
}

// obligationUnreplaced reports that a REPLACEMENT could not be established
// while an obligation still stands.
//
// Distinct wording because it is a distinct fact: the candidate or the reviewer
// moved, a successor was attempted, and the attempt failed. What still stands is
// the predecessor, and a later reader needs to know the replacement is what
// broke rather than the wait.
func obligationUnreplaced(o ReviewObligation, cause error) error {
	err := obligationStands(o, cause).(*roles.ReviewUnanswered)
	err.Cause = fmt.Errorf("the review this candidate is owed under request %s could not be replaced: %w",
		o.RequestID, cause)
	return err
}

// obligationWaited reports that THIS waiter ended without an answer.
//
// Waited is the waiter's own telemetry -- how long this process listened -- and
// says nothing about the obligation's lifetime, which has none.
func obligationWaited(o ReviewObligation, waited time.Duration, cause error) error {
	err := obligationStands(o, cause).(*roles.ReviewUnanswered)
	err.Waited = waited
	err.Cause = fmt.Errorf("this waiter ended without an answer after %s; request %s remains the review "+
		"this candidate is owed: %w", waited, o.RequestID, cause)
	return err
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

package ghbridge

// A REVIEW OBLIGATION is the durable statement that one exact candidate is owed
// a review under one exact published request.
//
// A WAITER is a process that watches the mailbox for the answer. It is
// disposable. It times out, it is cancelled, its process dies, its doorbell
// fails to ring -- and none of that changes what is owed. Before #182 R4 the
// code said this in comments and did something else at runtime: the next run
// minted a new request id, published a new snapshot, and superseded the
// obligation it was supposed to be continuing. A candidate that nobody had
// touched was asked about twice, under two identities, with two wake targets.
//
// So this file is the one component that decides review-obligation lifetime.
// Everything else -- the runner, the relay, resume, startup, the session
// transcript -- reads it and does not invent rules of its own.
//
// It keeps NO second durable copy. The obligation IS the review-kind
// ExchangeRecord that was written when the request was published; this type is
// that record read as what it always meant. Two files would be two answers to
// "what is owed", and they would agree only until one of them didn't.

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/roles"
)

// ReviewObligation is one review still owed, exactly as it was published.
type ReviewObligation struct {
	TaskID         string
	RequestID      string
	RequestComment int64
	Conversation   string
	PublishedAt    time.Time

	// The exact candidate. ReviewCommit is the projection published for THIS
	// request, and it stays with the request across every waiter.
	BaseSHA         string
	CandidateDigest string
	CandidateTree   string
	ReviewCommit    string

	// ReviewerProvider is who the workflow asked to judge.
	ReviewerProvider string
	// ExpectedReviewer is which GitHub account may supply the bytes. A different
	// fact about a different party, pinned at publication.
	ExpectedReviewer Principal

	MailboxRepository   string
	WorkspaceRepository string
}

var (
	// ErrObligationConflict reports more than one active review obligation for
	// one task. Choosing between them is not this component's to do.
	//
	// It wraps the transport-neutral condition so the workflow can recognise a
	// lifecycle conflict without depending on this package.
	ErrObligationConflict = fmt.Errorf("%w: this task has more than one active review obligation",
		roles.ErrReviewLifecycleConflict)
	// ErrObligationUnreadable reports a review record that cannot be read as the
	// obligation it claims to be.
	//
	// It wraps the transport-neutral lifecycle-fault condition, because a
	// malformed authority record is neither a reviewer who failed nor an
	// implementer who failed: no fallback ladder repairs it, and reported as
	// provider unavailability it would claim reviewers could not be reached when
	// the real fact is that our own record is unreadable.
	ErrObligationUnreadable = fmt.Errorf("%w: a review obligation record could not be read",
		roles.ErrReviewLifecycleFault)
	// ErrObligationLegacy reports an obligation published before R4 that does not
	// carry the authority facts a waiter needs to reattach safely.
	ErrObligationLegacy = errors.New("this review obligation predates the pinned mailbox principal and cannot be reattached")
)

// Subject is the candidate identity this obligation is about.
func (o ReviewObligation) Subject() Subject {
	return Subject{TaskID: o.TaskID, BaseSHA: o.BaseSHA, CandidateDigest: o.CandidateDigest,
		CandidateTree: o.CandidateTree, ReviewCommit: o.ReviewCommit}
}

// IsAbout reports whether this obligation is owed on THIS candidate.
//
// Candidate equality only: task, base, digest and tree. The review commit is the
// projection this request published and is a property of the obligation, so it
// is deliberately not compared -- doing so would make every reattachment look
// like a different candidate.
func (o ReviewObligation) IsAbout(s Subject) bool {
	return o.TaskID == s.TaskID && o.BaseSHA == s.BaseSHA &&
		o.CandidateDigest == s.CandidateDigest && o.CandidateTree == s.CandidateTree
}

// AssignedTo reports whether this obligation was published to a provider.
func (o ReviewObligation) AssignedTo(provider string) bool {
	return sameProvider(o.ReviewerProvider, provider)
}

// Reattachable reports whether a waiter may be attached to this obligation
// again, or why it may not.
//
// The authority facts must be present and must have been RECORDED, never
// reconstructed. An obligation that never wrote down which account could answer
// it cannot be reattached: filling that in from today's configuration would
// invent the very fact the check exists to verify, and would let a config edit
// authorize an account to answer a question it was never asked.
func (o ReviewObligation) Reattachable() error {
	if err := o.identityReadable(); err != nil {
		return err
	}
	if !o.ExpectedReviewer.Configured() {
		return fmt.Errorf("%w: request %s", ErrObligationLegacy, o.RequestID)
	}
	if strings.TrimSpace(o.ReviewerProvider) == "" {
		return fmt.Errorf("%w: request %s names no assigned reviewer", ErrObligationLegacy, o.RequestID)
	}
	return nil
}

// Request reconstructs the request that was published, byte for byte in every
// identity field.
//
// Reconstructed from the OBLIGATION and from nothing else. A request rebuilt
// out of current configuration would be a different question wearing the old
// one's id.
func (o ReviewObligation) Request() Request {
	return Request{
		Subject:             o.Subject(),
		RequestID:           o.RequestID,
		Kind:                KindReview,
		MailboxRepository:   o.MailboxRepository,
		WorkspaceRepository: o.WorkspaceRepository,
		ReviewerProvider:    o.ReviewerProvider,
	}
}

// ReachableFrom reports whether a mailbox may be used to wait on this
// obligation, or why not.
//
// The request went to one conversation. A process pointed somewhere else has
// not lost the obligation -- it simply cannot observe it, and it must not
// publish a new request because configuration moved. Withdrawing there would be
// worse: a retraction posted to a conversation that never carried the request.
func (o ReviewObligation) ReachableFrom(box Issue) error {
	if c := strings.TrimSpace(o.Conversation); c != "" && c != strings.TrimSpace(box.Number) {
		return fmt.Errorf("request %s was published in conversation %s and this process is pointed at %s",
			o.RequestID, c, box.Number)
	}
	if want := strings.TrimSpace(o.MailboxRepository); want != "" {
		if got := strings.TrimSpace(box.MailboxRepository()); got != "" && got != want {
			return fmt.Errorf("request %s was published to %s and this process is pointed at %s",
				o.RequestID, want, got)
		}
	}
	return nil
}

// identityReadable states what an obligation must prove about itself on EVERY
// read, before anything treats it as authority.
//
// The immutable identity only. A malformed task, base, candidate digest, tree
// or review commit means the record cannot say what it is owed on -- and the
// runner would otherwise read it as "the candidate changed" and repair it by
// superseding, which silently replaces authority nobody could read.
//
// Deliberately NOT here: the pinned principal and the assigned provider. Their
// absence is a known pre-R4 migration state with an explicit path
// (Reattachable), not corruption.
func (o ReviewObligation) identityReadable() error {
	if strings.TrimSpace(o.RequestID) == "" {
		return fmt.Errorf("%w: a record for task %s names no request", ErrObligationUnreadable, o.TaskID)
	}
	if err := o.Subject().Validate(); err != nil {
		return fmt.Errorf("%w: request %s: %v", ErrObligationUnreadable, o.RequestID, err)
	}
	return nil
}

// obligationFrom reads a review ExchangeRecord as the obligation it is.
func obligationFrom(rec ExchangeRecord) ReviewObligation {
	return ReviewObligation{
		TaskID: rec.TaskID, RequestID: rec.RequestID, RequestComment: rec.RequestComment,
		Conversation: rec.Conversation, PublishedAt: rec.PublishedAt,
		BaseSHA: rec.BaseSHA, CandidateDigest: rec.CandidateDigest,
		CandidateTree: rec.CandidateTree, ReviewCommit: rec.ReviewCommit,
		ReviewerProvider:  rec.ReviewerProvider,
		ExpectedReviewer:  Principal{UserID: rec.ExpectedReviewerID, Login: rec.ExpectedReviewerLogin},
		MailboxRepository: rec.MailboxRepository, WorkspaceRepository: rec.WorkspaceRepository,
	}
}

// record renders an obligation back into the durable form.
func (o ReviewObligation) record(deadline time.Time) ExchangeRecord {
	return ExchangeRecord{
		TaskID: o.TaskID, RequestID: o.RequestID, RequestComment: o.RequestComment,
		Conversation: o.Conversation, PublishedAt: o.PublishedAt,
		// Waiter telemetry, written so an operator can see what the publishing
		// waiter intended. Nothing reads it as expiry.
		Deadline: deadline,
		Kind:     ExchangeReview,
		BaseSHA:  o.BaseSHA, CandidateDigest: o.CandidateDigest,
		CandidateTree: o.CandidateTree, ReviewCommit: o.ReviewCommit,
		ReviewerProvider:      o.ReviewerProvider,
		ExpectedReviewerID:    o.ExpectedReviewer.UserID,
		ExpectedReviewerLogin: o.ExpectedReviewer.Login,
		MailboxRepository:     o.MailboxRepository,
		WorkspaceRepository:   o.WorkspaceRepository,
	}
}

// ReviewObligationStore is the one owner of review-obligation lifetime.
//
// Backed by the existing exchange log: this is a lens, not a second store.
type ReviewObligationStore struct {
	Exchanges ExchangeLog
}

// Available reports whether this process keeps durable obligations at all.
func (s ReviewObligationStore) Available() bool { return s.Exchanges.Dir != "" }

// Current is the one review obligation this task actively owes.
//
// Exactly one, or an explicit conflict. "Newest wins" is a reasonable rule for a
// transcript and a disastrous one for authority: two active obligations mean two
// requests are outstanding for one task, and silently picking one would consume
// a review through it while the other stayed owed and invisible.
//
// A record that cannot be read is reported, never skipped: an unreadable
// obligation is not the absence of one.
func (s ReviewObligationStore) Current(taskID string) (ReviewObligation, bool, error) {
	if !s.Available() {
		return ReviewObligation{}, false, nil
	}
	pending, err := s.Exchanges.PendingReviews()
	if err != nil {
		return ReviewObligation{}, false, fmt.Errorf("%w: %v", ErrObligationUnreadable, err)
	}
	var found []ReviewObligation
	for _, rec := range pending {
		if rec.TaskID != taskID {
			continue
		}
		o := obligationFrom(rec)
		if err := o.identityReadable(); err != nil {
			return ReviewObligation{}, false, err
		}
		found = append(found, o)
	}
	switch len(found) {
	case 0:
		return ReviewObligation{}, false, nil
	case 1:
		return found[0], true, nil
	default:
		var ids []string
		for _, o := range found {
			ids = append(ids, o.RequestID)
		}
		return ReviewObligation{}, false, fmt.Errorf("%w: task %s has %d (%s); none is consumed and none is replaced",
			ErrObligationConflict, taskID, len(found), strings.Join(ids, ", "))
	}
}

// ByRequest is the active obligation a specific published request names.
//
// A relay, or anything else answering a request by id, asks HERE rather than
// scanning the log itself: "is this request still owed" is one question with
// one owner, and a second implementation of it would drift from this one.
func (s ReviewObligationStore) ByRequest(requestID string) (ReviewObligation, error) {
	if !s.Available() {
		return ReviewObligation{}, errors.New("this process keeps no review obligation store")
	}
	pending, err := s.Exchanges.PendingReviews()
	if err != nil {
		return ReviewObligation{}, fmt.Errorf("%w: %v", ErrObligationUnreadable, err)
	}
	for _, rec := range pending {
		if rec.RequestID != requestID {
			continue
		}
		o := obligationFrom(rec)
		if err := o.identityReadable(); err != nil {
			return ReviewObligation{}, err
		}
		// THE SAME UNIQUENESS LAW Current applies. Naming a request by id is not
		// a way around it: a task with two active obligations is a conflict
		// whichever one the caller happens to hold, and consuming a review
		// through this one while the other stayed owed and invisible is exactly
		// what refusing the conflict exists to prevent.
		if _, _, err := s.Current(o.TaskID); err != nil {
			return ReviewObligation{}, err
		}
		return o, nil
	}
	return ReviewObligation{}, fmt.Errorf("request %s is not a review owed in this workspace: it was answered, "+
		"superseded, withdrawn, or never published here", requestID)
}

// Open durably records a newly published request as the obligation it creates.
//
// Called AFTER the request exists on the mailbox: a record without a request
// would retire something that was never posted.
func (s ReviewObligationStore) Open(o ReviewObligation, waiterDeadline time.Time) error {
	if !s.Available() {
		return errors.New("this process keeps no review obligation store")
	}
	return s.Exchanges.Open(o.record(waiterDeadline))
}

// Discharge retires an obligation that has been answered.
//
// The ONLY non-supersession way one ends. A waiter ending, a process dying, a
// deadline passing and a doorbell failing are none of them this.
func (s ReviewObligationStore) Discharge(o ReviewObligation) error {
	if !s.Available() {
		return errors.New("this process keeps no review obligation store")
	}
	return s.Exchanges.Close(o.TaskID, o.RequestID)
}

// Retire ends an obligation that an explicitly published successor replaces.
//
// Separate from Discharge because the two are different facts: one review was
// answered, the other was replaced without ever being answered. Call it only
// after the replacement is durable.
func (s ReviewObligationStore) Retire(o ReviewObligation) error {
	if !s.Available() {
		return errors.New("this process keeps no review obligation store")
	}
	return s.Exchanges.Close(o.TaskID, o.RequestID)
}

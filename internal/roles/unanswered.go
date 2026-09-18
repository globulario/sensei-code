package roles

import (
	"errors"
	"fmt"
	"time"
)

// ErrReviewUnanswered reports that a review request for an exact candidate is
// published and nothing answering it has arrived.
//
// A REQUEST HAS NO DEADLINE (#182 R4). What ended was one waiter: a process
// that listened for a while and stopped. The obligation it was listening for is
// durable, and the next process attaches a new waiter to the SAME request.
//
// It is deliberately NOT a provider failure. A provider that crashed, ran out of
// quota or returned unparseable output has produced no review, and another
// provider may try. A published request that went unanswered is a review that is
// still OWED on a candidate that has been validated and audited: nothing about the
// candidate is known to be wrong, and nobody has judged it. Treating that as a
// failure sent the next implementer at code nobody objected to; trying the next
// reviewer would silently change who judges it. Both are refused here by giving
// the condition its own name.
var ErrReviewUnanswered = errors.New("the review request is published and no answer has arrived; it remains the review owed")

// ErrReviewUnrecordable reports a review request that was PUBLISHED remotely
// and could not be recorded as a durable obligation here.
//
// Its own condition because neither existing answer fits. It is not an
// unanswered review: nothing durable says this candidate owes one, so no later
// process can reattach to it. It is not a provider that failed either, and
// trying the next reviewer would publish a second request while the first is
// already standing on the conversation, with nothing local naming either.
//
// So the turn stops here. The remote request exists and is visible to an
// operator; what is missing is the local record, and that is a storage fault to
// repair rather than a reviewer to replace.
var ErrReviewUnrecordable = errors.New("the review request was published and could not be recorded as a durable obligation")

// ErrReviewLifecycleFault reports that a task's own durable review-lifecycle
// records cannot be acted on: they disagree, or one of them cannot be read.
//
// THE UMBRELLA, and it exists so no fallback ladder can reinterpret the
// condition. A lifecycle fault is not a reviewer that failed -- asking another
// one adds a third record to a task that already has two too many, or reads the
// same malformed file again -- and it is not an implementer that failed either,
// because nothing the next worker can edit repairs local authority state.
//
// Named in roles rather than in a transport so both routing layers can
// recognise it without depending on one.
var ErrReviewLifecycleFault = errors.New("this task's review lifecycle records cannot be acted on")

// ErrReviewLifecycleConflict reports that a task's own durable review records
// disagree about what it owes -- most often two active obligations at once.
var ErrReviewLifecycleConflict = fmt.Errorf("%w: they disagree about what is owed", ErrReviewLifecycleFault)

// ReviewUnanswered is the unanswered review, with the identity a later process
// needs to continue it: which request, on which conversation, for which exact
// candidate.
//
// Binding is the identity the REQUEST carried, not one reconstructed afterwards.
// A continuation that cannot show the candidate it resumes is the candidate this
// request was about must not reuse anything recorded for it.
type ReviewUnanswered struct {
	RequestID      string
	RequestComment int64
	Conversation   string
	Binding        Binding
	ReviewCommit   string
	// Waited is how long THIS waiter listened. Telemetry about a process, never
	// a property of the request: a request does not expire, so nothing may read
	// this to decide that one has.
	Waited time.Duration
	Cause  error
}

func (u *ReviewUnanswered) Error() string {
	return fmt.Sprintf("%v: request %s for candidate %s; this waiter listened %s",
		ErrReviewUnanswered, u.RequestID, u.Binding.CandidateDigest, u.Waited)
}

// Unwrap exposes both the condition and the transport cause, so errors.Is
// matches ErrReviewUnanswered while the cause stays readable.
func (u *ReviewUnanswered) Unwrap() []error {
	if u.Cause == nil {
		return []error{ErrReviewUnanswered}
	}
	return []error{ErrReviewUnanswered, u.Cause}
}

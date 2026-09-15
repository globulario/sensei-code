package roles

import (
	"errors"
	"fmt"
	"time"
)

// ErrReviewUnanswered reports that a review request for an exact candidate was
// published and nothing answering it arrived before the request's own deadline.
//
// It is deliberately NOT a provider failure. A provider that crashed, ran out of
// quota or returned unparseable output has produced no review, and another
// provider may try. A published request that went unanswered is a review that is
// still OWED on a candidate that has been validated and audited: nothing about the
// candidate is known to be wrong, and nobody has judged it. Treating that as a
// failure sent the next implementer at code nobody objected to; trying the next
// reviewer would silently change who judges it. Both are refused here by giving
// the condition its own name.
var ErrReviewUnanswered = errors.New("the review request was published and no answer arrived before its deadline")

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
	Waited         time.Duration
	Cause          error
}

func (u *ReviewUnanswered) Error() string {
	return fmt.Sprintf("%v: request %s for candidate %s waited %s",
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

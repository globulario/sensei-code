package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/ghbridge"
	"github.com/globulario/sensei-code/internal/session"
)

// Resuming without an answer can only ask for the review a candidate is already
// owed. Every other state is refused before any process, graph or bridge starts.
func TestAReviewResumeIsAdmittedOnlyForATaskThatOwesAReview(t *testing.T) {
	tasks := []session.Interrupted{
		{TaskID: "owes", Task: "t", AwaitingReview: true},
		{TaskID: "work", Task: "t"},
		{TaskID: "asking", Task: "t", AwaitingReview: true, AwaitingAuthority: json.RawMessage(`{"question":"q"}`)},
	}

	got, err := selectReviewResume(tasks, " owes ", nil)
	if err != nil || got.TaskID != "owes" {
		t.Fatalf("a task owing a review was not admitted: %+v, %v", got, err)
	}
	for _, c := range []struct {
		task string
		want error
	}{
		{"work", errNoReviewOwed},
		{"asking", errReviewBehindQuestion},
		{"missing", errTaskUnknown},
	} {
		if _, err := selectReviewResume(tasks, c.task, nil); !errorIs(err, c.want) {
			t.Fatalf("task %q: err = %v, want %v", c.task, err, c.want)
		}
	}
}

// The durable obligation is the authority on whether a review is owed; the
// session transcript is a projection of it.
//
// Two facts follow, and both matter. A process can die after publishing the
// request and recording the obligation but before the workflow ever emits its
// WAITING_REVIEW terminal -- that task is resumable, because the request exists
// and the review is owed; only the transcript is missing. And when both name a
// request and they DISAGREE, neither is chosen: one of the two is wrong about
// which review this candidate is owed, and guessing would either consume a
// review through the wrong request or ask for a second one.
func TestReviewResumeTreatsTheObligationAsAuthorityAndTheTranscriptAsProjection(t *testing.T) {
	owed := &ghbridge.ReviewObligation{TaskID: "T-1", RequestID: "r-standing"}
	named := func(request string) []session.Interrupted {
		return []session.Interrupted{{
			TaskID: "T-1", AwaitingReview: true,
			AwaitingReviewRecord: []byte(`{"review_kind":"unanswered","request_id":"` + request + `"}`),
		}}
	}

	t.Run("the transcript never recorded the wait", func(t *testing.T) {
		// AwaitingReview false: the process died before the terminal was emitted.
		tasks := []session.Interrupted{{TaskID: "T-1"}}
		if _, err := selectReviewResume(tasks, "T-1", nil); err == nil {
			t.Fatal("a task with no transcript and no obligation was resumable")
		}
		if _, err := selectReviewResume(tasks, "T-1", owed); err != nil {
			t.Fatalf("a durable obligation was not enough to resume: %v", err)
		}
	})

	t.Run("both name the same request", func(t *testing.T) {
		if _, err := selectReviewResume(named("r-standing"), "T-1", owed); err != nil {
			t.Fatalf("agreeing records were refused: %v", err)
		}
	})

	t.Run("they disagree", func(t *testing.T) {
		_, err := selectReviewResume(named("r-something-else"), "T-1", owed)
		if !errors.Is(err, errReviewIdentitySplit) {
			t.Fatalf("err = %v, want an explicit refusal rather than a choice", err)
		}
		if !strings.Contains(err.Error(), "r-something-else") || !strings.Contains(err.Error(), "r-standing") {
			t.Fatalf("the refusal does not name both records: %v", err)
		}
	})

	t.Run("a human-owned decision still blocks it", func(t *testing.T) {
		tasks := named("r-standing")
		tasks[0].AwaitingAuthority = []byte(`{"question":"which?"}`)
		if _, err := selectReviewResume(tasks, "T-1", owed); !errors.Is(err, errReviewBehindQuestion) {
			t.Fatalf("err = %v, want the human decision to block the review resume", err)
		}
	})
}

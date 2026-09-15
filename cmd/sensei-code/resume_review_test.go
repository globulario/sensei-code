package main

import (
	"encoding/json"
	"testing"

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

	got, err := selectReviewResume(tasks, " owes ")
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
		if _, err := selectReviewResume(tasks, c.task); !errorIs(err, c.want) {
			t.Fatalf("task %q: err = %v, want %v", c.task, err, c.want)
		}
	}
}

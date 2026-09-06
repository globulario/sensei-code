package ghwebhook

import (
	"context"
	"fmt"
	"io"
)

// ObservationSink is the production sink for this slice, and it has NO governed
// effect.
//
// It writes one line and returns. It does not submit an objective, does not
// wake a review, does not create a task, does not resolve a runner and does not
// touch the workflow engine — it could not, because this package cannot reach
// any of them (see the import boundary pinned in boundary_test.go).
//
// That is the whole point of the slice. The chain
//
//	ChatGPT -> GitHub -> public Globular -> Sensei Code
//
// is worth proving end to end BEFORE anything at this end can act, because a
// transport commissioned at the same time as its first consumer gives you two
// untested things and one symptom.
//
// The comment body is deliberately absent from the line. It is the one field
// that is attacker-influenced free text, and a log is read by people and
// scraped by tools that were not written with that in mind.
type ObservationSink struct {
	// Out receives the observation. Nil discards it.
	Out io.Writer
}

// NewObservationSink writes safe observations to w.
func NewObservationSink(w io.Writer) *ObservationSink { return &ObservationSink{Out: w} }

// IssueComment implements Sink.
func (s *ObservationSink) IssueComment(_ context.Context, d IssueCommentDelivery) error {
	if s == nil || s.Out == nil {
		return nil
	}
	// Sender is reported as METADATA. It is who GitHub says wrote the comment,
	// and this line is the record of that fact — not a grant of anything. The
	// same line is printed whether the sender is the operator's GitHub account,
	// the App's bot, or anyone else with write access.
	_, err := fmt.Fprintf(s.Out,
		"github webhook accepted delivery=%s event=issue_comment repo=%s issue=%d comment=%d sender=%s\n",
		d.DeliveryID, d.RepositoryFullName, d.IssueNumber, d.CommentID, d.SenderLogin)
	return err
}

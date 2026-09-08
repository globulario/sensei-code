package ghwebhook

import (
	"context"
	"errors"
	"fmt"
	"io"
)

// ObservationSink is the production sink for authenticated GitHub deliveries.
// It still has NO governed effect.
//
// Ordinary comments produce one safe observation line. A comment carrying the
// objective-proposal marker is additionally persisted as inert local data. It
// does not submit an objective, wake a review, create a task, resolve a runner,
// or touch the workflow engine. The package still cannot reach any of those
// capabilities (see boundary_test.go).
//
// Persisting the proposal closes only the transport-to-data gap. Authority is a
// separate later act: a local operator must select the exact immutable proposal
// and the existing local objective channel must accept that operator.
//
// The comment body is deliberately absent from every log line. It is
// attacker-influenced free text and is carried only inside the 0600 proposal
// record when it matches the explicit proposal protocol.
type ObservationSink struct {
	// Out receives safe observations. Nil discards them.
	Out io.Writer
	// Proposals is nil only when this process was not launched from inside a Git
	// worktree. A proposal marker in that state is a receive failure rather than
	// a silent drop, so GitHub may retry after the process is fixed.
	Proposals *ProposalStore
}

// NewObservationSink writes safe observations to w and, when the process is in
// a Git worktree, enables the durable inert proposal ledger for that worktree.
func NewObservationSink(w io.Writer) *ObservationSink {
	var proposals *ProposalStore
	if root := discoverProposalRepoRoot(); root != "" {
		proposals = NewProposalStore(root)
	}
	return &ObservationSink{Out: w, Proposals: proposals}
}

// IssueComment implements Sink.
func (s *ObservationSink) IssueComment(_ context.Context, d IssueCommentDelivery) error {
	if s == nil {
		return nil
	}

	// Proposal intake happens before the observation is printed. If durable
	// receipt fails, GitHub gets a 500 and may retry; printing "accepted" first
	// would present a delivery as received while its only durable consumer lost
	// it.
	if _, handled, parseErr := ParseObjectiveProposal(d.CommentBody); handled {
		if s.Proposals == nil {
			return errors.New("an objective proposal arrived but no local Git worktree is available for its durable record")
		}
		p, created, _, err := s.Proposals.Record(d)
		if err != nil {
			if errors.Is(err, ErrMalformedObjectiveProposal) {
				// This is a protocol refusal, not a transport failure. Retrying the
				// same signed malformed comment can never make it well formed.
				if s.Out != nil {
					_, _ = fmt.Fprintf(s.Out,
						"github objective proposal refused comment=%d sender=%s reason=malformed\n",
						d.CommentID, d.SenderLogin)
				}
			} else {
				return err
			}
		} else if created && s.Out != nil {
			_, _ = fmt.Fprintf(s.Out,
				"github objective proposal pending comment=%d digest=%s sender=%s\n",
				d.CommentID, p.ObjectiveDigest, d.SenderLogin)
		}
		_ = parseErr // Record performs the same strict parse and owns the verdict.
	}

	if s.Out == nil {
		return nil
	}
	// Sender is reported as METADATA. It is who GitHub says wrote the comment,
	// not a grant of anything. The same line is printed whether the sender is
	// the operator's GitHub account, the App bot, or another collaborator.
	_, err := fmt.Fprintf(s.Out,
		"github webhook accepted delivery=%s event=issue_comment repo=%s issue=%d comment=%d sender=%s\n",
		d.DeliveryID, d.RepositoryFullName, d.IssueNumber, d.CommentID, d.SenderLogin)
	return err
}

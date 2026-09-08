package ghwebhook

import "context"

// IssueCommentDelivery is one authenticated issue_comment delivery, normalized.
//
// Every field is a fact CARRIED BY a delivery whose signature verified. None of
// them is an authorization. In particular SenderID and SenderLogin say who
// GitHub says wrote the comment; they do not say that party may direct this
// process, and no consumer may read them as though they did.
type IssueCommentDelivery struct {
	// DeliveryID is GitHub's X-GitHub-Delivery. Preserved for the replay
	// boundary described below, and opaque to this package.
	DeliveryID string
	// Action is the payload's action, e.g. "created".
	Action string

	// The binding this delivery was accepted against. Carried so a consumer
	// reads what was checked rather than re-deriving it from configuration.
	InstallationID     int64
	RepositoryID       int64
	RepositoryFullName string

	// IssueNumber is the issue the comment was posted on.
	//
	// The transport does not decide what an issue MEANS. Which issue is the
	// review mailbox, which is an architecture mailbox, and whether either is
	// entitled to anything, are later protocol questions. Pinning one issue
	// number here would make the transport layer hold policy it cannot see the
	// consequences of.
	IssueNumber int64

	CommentID int64
	// CommentBody is the comment text.
	//
	// It arrives after authentication and it is DATA. It is not a command, not
	// an objective, and not authority. Nothing in this package reads it, and a
	// consumer that parses it for instructions has turned a signature over
	// bytes into permission to act on their content, which the signature never
	// granted. It is also never logged.
	CommentBody string

	SenderID    int64
	SenderLogin string
	SenderType  string
}

// REPLAY IS NOT SOLVED HERE, AND AUTHENTICITY IS NOT REPLAY PROTECTION.
//
// A valid signature proves GitHub sent these bytes. It proves nothing about
// whether GitHub sent them BEFORE. GitHub redelivers — on its own retry, and on
// an operator pressing redeliver — and every redelivery carries a signature
// that verifies exactly as the first one did.
//
// This slice is safe only because it has no side-effecting consumer: the
// production sink emits an observation and returns, so a duplicate delivery
// duplicates a log line and nothing else.
//
// That safety expires at the first consumer that turns a delivery into an
// action. Before that slice lands, DURABLE idempotency is mandatory — a record
// that survives restart, keyed on DeliveryID and cross-checked against
// CommentID, because the two answer different questions:
//
//	DeliveryID   was this DELIVERY already processed (redelivery, retry)
//	CommentID    was this COMMENT already acted on (a new delivery about an
//	             old comment, e.g. an edit replayed as a create)
//
// An in-memory set is not that. It forgets on restart, and a restart is
// precisely when a queued redelivery arrives. Both fields are preserved above
// so the later slice has what it needs; neither is checked here, and this
// comment exists so nobody reads the absence of a check as the absence of a
// problem.

// Sink receives authenticated deliveries.
//
// Narrow on purpose. It is the entire surface between webhook transport and
// everything else, so what a delivery can cause is bounded by what this
// interface can express. Widening it is how "authenticated" quietly becomes
// "authorized".
//
// An error returned here is a failure to RECEIVE, not a refusal of the
// delivery: the delivery was already authenticated by the time the sink saw it.
type Sink interface {
	IssueComment(context.Context, IssueCommentDelivery) error
}

// SinkFunc adapts a function to Sink.
type SinkFunc func(context.Context, IssueCommentDelivery) error

// IssueComment implements Sink.
func (f SinkFunc) IssueComment(ctx context.Context, d IssueCommentDelivery) error { return f(ctx, d) }

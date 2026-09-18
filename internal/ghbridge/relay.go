package ghbridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// A RELAYED review is a reviewer's verdict that reached this workspace through
// the local operator instead of the mailbox.
//
// It exists because a reviewer can produce a complete, bound review and still be
// unable to post it (a connector that refuses to write a comment). Three parties
// are kept distinct the whole way through, and conflating any two is the defect:
//
//	reviewer          produced the technical verdict      (named, never verified)
//	relay principal   carried it from a controlling terminal (authority to relay only)
//	GitHub App        published the accepted result on the mailbox
//
// The operator is not the author of the verdict. A relay carries the artifact
// byte for byte, is accepted only against the exact owed request it names, and
// never raises standing: a relayed review is advisory.
//
// THIS ADAPTER OWNS NO DURABLE RECORD. Until #182 R6 it kept its own: a
// RelayRecord with the artifact, the digest, the decision, the summary, the
// findings, the standing, the binding and its own two-state lifecycle. That was
// a second semantic copy of the review, and the runner had to consult it beside
// the common store to find out what a request was owed. What the relay
// legitimately owned was one fact -- that accepted bytes may exist before the
// App has published them -- and that fact is now a delivery state on the ONE
// review record. A relay stages exact canonical bytes and completes their
// delivery. It defines no review.

const relayedReviewMarker = "[sensei-code:relayed-review]"

var (
	// ErrRelayRefused reports a relay that was not accepted. Nothing durable was
	// written and nothing was published.
	ErrRelayRefused = errors.New("the relayed review was refused")
	// ErrRelayPublication reports a relay whose exact bytes ARE staged and whose
	// publication did not complete. The staged review is preserved and grants no
	// authority until it is published.
	ErrRelayPublication = errors.New("the relayed review is staged and preserved, and its publication did not complete")
	// ErrRelayConvergence reports a relay that was published and whose delivery
	// could not be recorded as complete.
	//
	// Its own error because the recovery is different: the GitHub receipt
	// already exists, so the repair is to retry the completion, never to publish
	// again. Resubmitting the identical artifact does exactly that -- it finds
	// the existing publication on the mailbox and completes from it.
	ErrRelayConvergence = errors.New("the relayed review is published and preserved, and completing its delivery did not finish")
)

// Relay delivery states, as this adapter reports them to its caller.
const (
	// RelayStaged is exact canonical bytes durably held with their delivery
	// incomplete. It is not an answer anybody may consume.
	RelayStaged = "staged"
	// RelayPublished is a relay whose delivery completed.
	RelayPublished = "published"
)

// RelayPrincipal is who relayed a review, as the control process observed the
// caller on its local socket. It is relay authority and nothing more.
type RelayPrincipal struct {
	UID      uint32 `json:"uid"`
	User     string `json:"user,omitempty"`
	PID      int    `json:"pid"`
	Terminal uint64 `json:"terminal"`
}

func (p RelayPrincipal) token() string {
	user := p.User
	if strings.TrimSpace(user) == "" {
		user = "-"
	}
	return fmt.Sprintf("uid:%d,user:%s,pid:%d,terminal:%d", p.UID, user, p.PID, p.Terminal)
}

// ReviewDigest names the exact bytes of a relayed artifact.
//
// It delegates so that one digest function names review bytes for every
// transport: a second implementation would let the same artifact carry two
// names, and an attestation covering one of them would miss the other.
func ReviewDigest(raw string) string { return reviewartifact.Digest(raw) }

// RelayResult is what one relay submission did. A RETURN VALUE, deliberately
// not a record: everything durable about a relayed review lives in the one
// review store, and this is only how the socket reports the outcome.
type RelayResult struct {
	State        string
	TaskID       string
	RequestID    string
	Reviewer     string
	ReviewDigest string

	Publication        string
	PublicationComment int64
}

// RelaySubmission is one relay, as the control process received it.
type RelaySubmission struct {
	Artifact  string
	Principal RelayPrincipal
	Exchanges ExchangeLog
	// Reviews is the one durable record every transport converges on. A relay
	// stages the ORIGINAL reviewer bytes here and completes their delivery, so
	// the same bytes mean the same thing whether they came down this pipe or the
	// mailbox.
	Reviews reviewstore.Store
	Mailbox Issue
	Now     func() time.Time
}

func refused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRelayRefused, fmt.Sprintf(format, args...))
}

// refusedBecause refuses a relay while PRESERVING the identity of the condition
// that caused it.
//
// The plain refused() formats its cause with %v, which reads fine and loses the
// error chain. A lifecycle conflict refused that way was indistinguishable from
// any other relay refusal, so nothing upstream -- or in a test -- could tell
// "this relay was malformed" from "this task's records disagree".
func refusedBecause(cause error) error {
	return fmt.Errorf("%w: %w", ErrRelayRefused, cause)
}

// AcceptRelayedReview validates a relayed artifact against the review it
// answers, stages it once, and has the App publish it.
//
// Called ONLY by the control process's relay socket handler, with the principal
// that socket observed; nothing in the review runner or the engine calls it.
// Either the artifact is staged exactly as submitted, or it is refused and
// nothing durable happens.
//
// The transaction, in the order the failures matter:
//
//	authenticate principal -> parse canonical artifact -> resolve the owed
//	obligation -> prove binding -> validate the reviewer body -> STAGE the
//	exact bytes with pending relay evidence -> publish the receipt ->
//	COMPLETE that evidence
//
// Nothing is published before the bytes are durable, and nothing is consumable
// before the publication is recorded. Resubmitting the identical artifact is how
// a failed publication or a failed completion is retried; a DIFFERENT artifact
// for a request that already has one is refused, and the stored bytes are left
// exactly as they were.
func AcceptRelayedReview(ctx context.Context, in RelaySubmission) (RelayResult, error) {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	if in.Exchanges.Dir == "" || in.Reviews.Dir == "" {
		return RelayResult{}, refused("this process keeps no review exchange log or review store")
	}
	if err := relayPublisherReady(in.Mailbox); err != nil {
		return RelayResult{}, refused("%v", err)
	}
	if in.Principal.PID <= 0 || in.Principal.Terminal == 0 {
		return RelayResult{}, refused("a relay must name the terminal principal that carried it")
	}
	// The CANONICAL grammar, directly. There is no relay-shaped review type to
	// lift it into any more: the artifact package defines what a review is, and
	// this adapter carries one.
	art, err := reviewartifact.Parse(in.Artifact)
	if err != nil {
		return RelayResult{}, refused("%v", err)
	}

	// Resolved through the one owner of review-obligation lifetime, so a relay
	// is accepted against what this workspace actually owes rather than against
	// a rule this handler invented for itself (#182 R4).
	owned := ReviewObligationStore{Exchanges: in.Exchanges}
	rec, err := owned.ByRequest(art.RequestID)
	if err != nil {
		return RelayResult{}, refusedBecause(err)
	}
	if mismatch := subjectMismatch(rec.Subject(), subjectOf(art)); mismatch != "" {
		return RelayResult{}, refused("the review is not about the candidate request %s carried: %s", art.RequestID, mismatch)
	}
	// The artifact must name the provider the WORKFLOW assigned when the request
	// was published. A record that never captured the assignment cannot
	// authenticate anybody: inferring it from today's configuration would invent
	// the very fact this check exists to verify, so an unknown assignment is a
	// refusal and the request may be superseded instead.
	if strings.TrimSpace(rec.ReviewerProvider) == "" {
		return RelayResult{}, refused("request %s predates the recorded reviewer assignment, so this artifact cannot be "+
			"authenticated against who was asked; supersede it with a new request rather than relaying against it", art.RequestID)
	}
	if !sameProvider(art.ReviewerProvider, rec.ReviewerProvider) {
		return RelayResult{}, refused("the artifact names reviewer %q and request %s was assigned to %q",
			art.ReviewerProvider, art.RequestID, rec.ReviewerProvider)
	}
	if rec.Conversation != "" && rec.Conversation != in.Mailbox.Number {
		return RelayResult{}, refused("request %s was published in conversation %s and this process publishes to %s",
			art.RequestID, rec.Conversation, in.Mailbox.Number)
	}
	binding := roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA,
		CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree}
	verdict, err := workflow.ValidateReviewBody(art.Body, binding, art.ReviewerProvider)
	if err != nil {
		return RelayResult{}, refused("the reviewer payload does not satisfy the reviewer contract: %v", err)
	}

	// Read BEFORE staging, for one reason only: to know whether this submission
	// is a retry. A record that already existed may carry a publication this
	// process failed to record, and that is the one case where the mailbox must
	// be consulted before posting a second receipt.
	prior, retry, err := in.Reviews.Load(art.RequestID)
	if err != nil {
		return RelayResult{}, refused("%v", err)
	}

	// STAGE. The exact submitted bytes, with evidence that names who carried
	// them and says plainly that their delivery has not completed.
	stored, err := in.Reviews.Accept(reviewstore.Acceptance{
		RequestID: art.RequestID,
		Artifact:  in.Artifact,
		Evidence: reviewstore.Evidence{
			Transport:      reviewstore.LocalRelay,
			State:          reviewstore.Pending,
			RelayPrincipal: in.Principal.token(),
		},
		Validate: func(a reviewartifact.Artifact) error {
			_, verr := workflow.ValidateReviewBody(a.Body, binding, a.ReviewerProvider)
			return verr
		},
		Now: in.Now,
	})
	if err != nil {
		// Including the conflict: a different review is already stored for this
		// request, and NOTHING is published on top of it.
		return RelayResult{}, refusedBecause(err)
	}
	out := RelayResult{State: RelayStaged, TaskID: rec.TaskID, RequestID: art.RequestID,
		Reviewer: art.ReviewerProvider, ReviewDigest: art.Digest}

	// Already delivered. The common semantic review exists and is consumable, so
	// the only thing a second identical submission could add is a second receipt
	// for one review. It publishes nothing.
	if stored.Consumable() {
		return deliveredResult(out, stored), nil
	}

	// A retry whose delivery is incomplete may be a crash between posting the
	// receipt and recording it. Reconcile from the mailbox BEFORE posting: the
	// existing publication is found by this exact request and review digest AND
	// by the account that authored it, and completing from it is what stops one
	// review acquiring two receipts.
	if retry && !prior.Consumable() {
		receipt, ferr := relayPublicationOf(ctx, in.Mailbox, rec, art.Digest)
		if ferr != nil {
			return out, fmt.Errorf("%w: the mailbox could not be read to establish whether this review "+
				"was already published: %v", ErrRelayConvergence, ferr)
		}
		switch {
		case receipt.ours:
			completed, cerr := in.Reviews.Complete(reviewstore.Completion{
				RequestID: art.RequestID, ReviewDigest: art.Digest, Transport: reviewstore.LocalRelay,
				Publication: receipt.publication, PublicationComment: receipt.comment,
				PublishedAt: now().UTC(), Now: in.Now,
			})
			if cerr != nil {
				return out, fmt.Errorf("%w: %v", ErrRelayConvergence, cerr)
			}
			return deliveredResult(out, completed), nil
		case receipt.unauthenticatable:
			// Something claiming to be this review's receipt is on the mailbox
			// and this obligation predates the pinned publisher, so nothing can
			// establish whether the App posted it. Neither promoting it nor
			// posting a second receipt beside it is honest.
			return out, fmt.Errorf("%w: a relayed-review receipt for %s is on the mailbox as comment %d and "+
				"request %s predates the recorded publisher, so it cannot be established as this machine's; "+
				"supersede the request rather than publishing a second receipt",
				ErrRelayConvergence, art.Digest, receipt.comment, art.RequestID)
		}
		// A look-alike from an account that is NOT the publisher establishes
		// nothing and blocks nothing. The genuine publication goes out below,
		// which is what makes the difference visible.
	}

	body, err := RenderRelayedReview(art, verdict, stagedPrincipal(stored), in.Mailbox)
	if err != nil {
		return out, fmt.Errorf("%w: %v", ErrRelayPublication, err)
	}
	comment, err := in.Mailbox.API.PostComment(ctx, in.Mailbox.Number, body)
	if err != nil {
		return out, fmt.Errorf("%w: %v; resubmit the same artifact to retry publication", ErrRelayPublication, err)
	}
	completed, err := in.Reviews.Complete(reviewstore.Completion{
		RequestID: art.RequestID, ReviewDigest: art.Digest, Transport: reviewstore.LocalRelay,
		Publication: publicationIdentity(in.Mailbox), PublicationComment: comment, PublishedAt: now().UTC(), Now: in.Now,
	})
	if err != nil {
		return out, fmt.Errorf("%w: posted as comment %d, and the delivery could not be recorded: %v",
			ErrRelayConvergence, comment, err)
	}
	return deliveredResult(out, completed), nil
}

// deliveredResult reports a completed relay from the record's OWN evidence,
// rather than from what this call believes it just did.
func deliveredResult(out RelayResult, rec reviewstore.Record) RelayResult {
	out.State = RelayPublished
	for _, ev := range rec.Evidence {
		if ev.Transport == reviewstore.LocalRelay && ev.Delivered() {
			out.Publication = ev.Publication
			out.PublicationComment = ev.PublicationComment
			break
		}
	}
	return out
}

// relayReceipt is what the mailbox shows about a publication of one exact review.
type relayReceipt struct {
	// ours is a receipt this machine demonstrably authored.
	ours bool
	// unauthenticatable is a matching receipt whose authorship cannot be
	// established, because the obligation predates the pinned publisher.
	unauthenticatable bool

	comment     int64
	publication string
	author      Principal
}

// relayPublicationOf finds a publication of THIS exact review already on the
// mailbox, and says whether this machine wrote it.
//
// THREE facts must agree, not two. The receipt must name this request and this
// review digest -- never "the most recent relayed-review comment", because a
// conversation carries many and last-comment-wins would attach one review's
// publication to another review's record -- AND its author must be the account
// this obligation recorded as its publisher.
//
// That third check is the authority one. Reading the mailbox AS the App
// authenticates the READER; the body of a comment is written by whoever posted
// it, and a marker, a request id and a digest are all public. Without the author
// check, anybody who can comment on the conversation could promote a review this
// process is holding but has never published -- turning "staged here" into
// "delivered" on a stranger's say-so, which is the one thing the delivery state
// exists to prevent.
//
// EVERY match is examined, and OURS WINS wherever it sits. Returning the first
// match made the answer depend on comment order: a look-alike posted before a
// genuine receipt that already existed would hide it, the retry would conclude
// nothing had been published, and this machine would post a second genuine
// receipt for one review -- the exact duplication the reconciliation exists to
// prevent, reachable by anybody who could comment first.
func relayPublicationOf(ctx context.Context, box Issue, o ReviewObligation, digest string) (relayReceipt, error) {
	comments, err := mailboxComments(ctx, box)
	if err != nil {
		return relayReceipt{}, err
	}
	var other relayReceipt
	var found bool
	for _, c := range comments {
		head := strings.TrimLeft(c.Body, " \t\r\n")
		if !strings.HasPrefix(head, relayedReviewMarker) {
			continue
		}
		f, _, ok := fields(c.Body, relayedReviewMarker)
		if !ok {
			continue
		}
		if f["request"] != o.RequestID || f["review_digest"] != digest {
			continue
		}
		out := relayReceipt{
			comment: c.ID,
			author:  Principal{UserID: c.User.ID, Login: c.User.Login},
		}
		out.publication = f["publication"]
		if strings.TrimSpace(out.publication) == "" {
			out.publication = publicationIdentity(box)
		}
		if o.Publisher.Configured() && o.Publisher.Matches(c.User.ID, c.User.Login) {
			// The genuine one. Nothing later can improve on it.
			out.ours = true
			return out, nil
		}
		if !o.Publisher.Configured() {
			// Nothing to authenticate against. The gap is NOT filled from
			// today's configuration or from the body's own claim.
			out.unauthenticatable = true
		}
		if !found {
			// Keep the FIRST non-ours match for the diagnostic, and keep
			// looking: a genuine receipt may sit behind it.
			other, found = out, true
		}
	}
	return other, nil
}

// stagedPrincipal is the terminal that carried the delivery this record holds.
func stagedPrincipal(rec reviewstore.Record) string {
	for _, ev := range rec.Evidence {
		if ev.Transport == reviewstore.LocalRelay {
			return ev.RelayPrincipal
		}
	}
	return ""
}

// subjectMismatch names the first identity field on which two subjects differ.
func subjectMismatch(want, got Subject) string {
	for _, f := range []struct{ name, want, got string }{
		{"task", want.TaskID, got.TaskID},
		{"base", want.BaseSHA, got.BaseSHA},
		{"candidate_digest", want.CandidateDigest, got.CandidateDigest},
		{"candidate_tree", want.CandidateTree, got.CandidateTree},
		{"review_commit", want.ReviewCommit, got.ReviewCommit},
	} {
		if f.want != f.got {
			return fmt.Sprintf("%s is %q, the request carried %q", f.name, f.got, f.want)
		}
	}
	return ""
}

// relayPublisherReady refuses a mailbox that cannot publish as the App. A relay
// published under the operator's gh account would put a person's identity on
// a verdict they did not author -- and that account may be the reviewer the
// mailbox reads answers from.
func relayPublisherReady(box Issue) error {
	if !box.Valid() {
		return errors.New("a relayed review needs a mailbox pull request number and an expected reviewer")
	}
	if box.API == nil || !box.API.Configured() {
		return errors.New("a relayed review is published by the GitHub App and never under the operator's gh account; " +
			"this process has no configured App transport")
	}
	return nil
}

func publicationIdentity(box Issue) string {
	if box.API == nil || box.API.Auth == nil {
		return "github-app"
	}
	return fmt.Sprintf("github-app:%d:installation:%d:%s/%s",
		box.API.Auth.AppID, box.API.Auth.InstallationID, box.API.Owner, box.API.Repo)
}

// RenderRelayedReview renders the App's publication of a staged relay.
//
// Rendered from the CANONICAL artifact and the verdict the workflow parser read
// out of its body, never from a stored second copy of the decision. Before R6
// the decision, summary and findings were persisted on the relay's own record
// so this function could print them; they are recomputed from the exact bytes
// instead, so what the receipt says cannot drift from what the review says.
//
// The relay principal is the STAGED delivery's, taken from the record, never the
// caller's. One delivery has one principal: when terminal A stages a review and
// terminal B retries it, the store correctly keeps A -- and a receipt rendered
// from the retry caller would publish B, so the mailbox and the durable record
// would name different people for the same delivery (#182 R6).
//
// Its envelope is NOT a review envelope, and it carries no review or request
// marker anywhere: nothing reading the mailbox can take the publication for an
// answer. The decision lives in the prose, beside who produced it and who
// relayed it, never in the envelope.
func RenderRelayedReview(art reviewartifact.Artifact, verdict roles.ReviewVerdict,
	relayPrincipal string, box Issue) (string, error) {

	var b strings.Builder
	b.WriteString(relayedReviewMarker + "\n")
	fmt.Fprintf(&b, "task=%s\n", art.TaskID)
	fmt.Fprintf(&b, "request=%s\n", art.RequestID)
	fmt.Fprintf(&b, "base=%s\n", art.BaseSHA)
	fmt.Fprintf(&b, "candidate_digest=%s\n", art.CandidateDigest)
	fmt.Fprintf(&b, "candidate_tree=%s\n", art.CandidateTree)
	fmt.Fprintf(&b, "review_commit=%s\n", art.ReviewCommit)
	fmt.Fprintf(&b, "reviewer_provider=%s\n", art.ReviewerProvider)
	fmt.Fprintf(&b, "review_digest=%s\n", art.Digest)
	fmt.Fprintf(&b, "relay_principal=%s\n", relayPrincipal)
	fmt.Fprintf(&b, "publication=%s\n", publicationIdentity(box))
	fmt.Fprintf(&b, "standing=%s\n", reviewstore.Advisory)
	fmt.Fprintf(&b, "\nRelayed review for request %s.\n\n", art.RequestID)
	fmt.Fprintf(&b, "The technical verdict below was produced by reviewer `%s`. A local operator relayed it from a "+
		"controlling terminal and did not author it; sensei-code's GitHub App publishes it. Its standing is advisory: "+
		"it satisfies no independent-review obligation and is not Sensei admission.\n\n", art.ReviewerProvider)
	fmt.Fprintf(&b, "Decision: %s\n\nSummary: %s\n", verdict.Decision, verdict.Summary)
	if len(verdict.Findings) > 0 {
		b.WriteString("\nFindings:\n")
		for _, f := range verdict.Findings {
			b.WriteString("- " + f.Line() + "\n")
		}
	}
	body := b.String()
	if strings.Contains(body, reviewartifact.Marker) || strings.Contains(body, requestMarker) ||
		strings.Count(body, "[sensei-code:") != 1 {
		return "", errors.New("the publication would carry a protocol marker beyond its own envelope, so it is not posted")
	}
	return body, nil
}

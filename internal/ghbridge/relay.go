package ghbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

const (
	relayedReviewMarker = "[sensei-code:relayed-review]"

	// RelayAccepted is a relay validated and durably recorded, not yet published.
	RelayAccepted = "accepted"
	// RelayPublished is a relay the App has published on the mailbox.
	RelayPublished = "published"
)

var (
	// ErrRelayRefused reports a relay that was not accepted. Nothing durable was
	// written and nothing was published.
	ErrRelayRefused = errors.New("the relayed review was refused")
	// ErrRelayPublication reports a relay that WAS accepted and whose publication
	// did not complete. The accepted review is preserved.
	ErrRelayPublication = errors.New("the relayed review is accepted and preserved, and its publication did not complete")
	// ErrRelayConvergence reports a relay that was accepted AND published, and
	// whose record in the common review store did not complete.
	//
	// Its own error because the recovery is different: the GitHub receipt
	// already exists, so the repair is to retry the store write, never to
	// publish again. Resubmitting the identical artifact does exactly that.
	ErrRelayConvergence = errors.New("the relayed review is published and preserved, and recording it in the review store did not complete")
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

// RelayArtifact is a review artifact exactly as the reviewer produced it.
//
// It is the canonical reviewartifact.Artifact seen through this transport's
// existing types: the relay carries a review, it does not define one.
type RelayArtifact struct {
	Review
	// Provider is the reviewer the artifact names. Claimed, not verified.
	Provider string
	// Raw is the artifact byte for byte, and Digest names those bytes.
	Raw    string
	Digest string
}

// ReviewDigest names the exact bytes of a relayed artifact.
//
// It delegates so that one digest function names review bytes for every
// transport: a second implementation would let the same artifact carry two
// names, and an attestation covering one of them would miss the other.
func ReviewDigest(raw string) string { return reviewartifact.Digest(raw) }

// ParseRelayArtifact reads a complete review artifact, or refuses it whole.
//
// The grammar, the validation, the size bound and the digest are the canonical
// artifact package's. This function only lifts the result into the transport's
// own types; it adds no rule of its own, so a relayed review and a review
// arriving by any other adapter mean the same thing.
func ParseRelayArtifact(raw string) (RelayArtifact, error) {
	a, err := reviewartifact.Parse(raw)
	if err != nil {
		return RelayArtifact{}, err
	}
	return RelayArtifact{
		Review: Review{
			Subject: Subject{
				TaskID:          a.TaskID,
				BaseSHA:         a.BaseSHA,
				CandidateDigest: a.CandidateDigest,
				CandidateTree:   a.CandidateTree,
				ReviewCommit:    a.ReviewCommit,
			},
			RequestID: a.RequestID,
			Body:      a.Body,
		},
		Provider: a.ReviewerProvider,
		Raw:      a.Raw,
		Digest:   a.Digest,
	}, nil
}

// RelayRecord is the durable receipt of one accepted relay.
type RelayRecord struct {
	Version int    `json:"version"`
	State   string `json:"state"`

	// The owed request the relay answered, exactly as the exchange log held it.
	TaskID          string `json:"task_id"`
	RequestID       string `json:"request_id"`
	RequestComment  int64  `json:"request_comment,omitempty"`
	Conversation    string `json:"conversation"`
	BaseSHA         string `json:"base"`
	CandidateDigest string `json:"candidate_digest"`
	CandidateTree   string `json:"candidate_tree"`
	ReviewCommit    string `json:"review_commit"`

	// The reviewer's verdict: who produced it, and what it said.
	Reviewer     string          `json:"reviewer_provider"`
	ReviewDigest string          `json:"review_digest"`
	Decision     string          `json:"decision"`
	Summary      string          `json:"summary"`
	Findings     []roles.Finding `json:"findings,omitempty"`
	Artifact     string          `json:"artifact"`
	Standing     string          `json:"standing"`

	// Who relayed it, kept apart from who produced it.
	RelayPrincipal RelayPrincipal `json:"relay_principal"`
	AcceptedAt     time.Time      `json:"accepted_at"`

	// Who published it, kept apart from both.
	Publication        string    `json:"publication,omitempty"`
	PublicationComment int64     `json:"publication_comment,omitempty"`
	PublishedAt        time.Time `json:"published_at,omitempty"`
}

// Subject is the candidate identity the relayed review is about.
func (r RelayRecord) Subject() Subject {
	return Subject{TaskID: r.TaskID, BaseSHA: r.BaseSHA, CandidateDigest: r.CandidateDigest,
		CandidateTree: r.CandidateTree, ReviewCommit: r.ReviewCommit}
}

// RelayStore holds one receipt per relayed request.
type RelayStore struct {
	Dir string
}

func (s RelayStore) path(requestID string) (string, error) {
	if s.Dir == "" {
		return "", errors.New("the relay store has no directory")
	}
	if !exchangeFileSafe.MatchString(requestID) {
		return "", fmt.Errorf("request id %q is not a safe file name", requestID)
	}
	return filepath.Join(s.Dir, requestID+".json"), nil
}

// Load reads the receipt for a request, if one exists.
func (s RelayStore) Load(requestID string) (RelayRecord, bool, error) {
	path, err := s.path(requestID)
	if err != nil {
		return RelayRecord{}, false, err
	}
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return RelayRecord{}, false, nil
	}
	if err != nil {
		return RelayRecord{}, false, err
	}
	var rec RelayRecord
	if err := json.Unmarshal(blob, &rec); err != nil {
		return RelayRecord{}, false, fmt.Errorf("the relay receipt for %s is unreadable: %w", requestID, err)
	}
	return rec, true, nil
}

// create writes a receipt that must not already exist: one accepted review per
// request, ever.
func (s RelayStore) create(rec RelayRecord) error {
	path, err := s.path(rec.RequestID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(blob, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// markPublished records the publication of the SAME accepted bytes.
func (s RelayStore) markPublished(requestID, digest string, comment int64, publication string, at time.Time) (RelayRecord, error) {
	rec, found, err := s.Load(requestID)
	if err != nil {
		return RelayRecord{}, err
	}
	if !found || rec.ReviewDigest != digest {
		return RelayRecord{}, fmt.Errorf("the accepted relay for %s is not the one being published", requestID)
	}
	rec.State = RelayPublished
	rec.Publication = publication
	rec.PublicationComment = comment
	rec.PublishedAt = at.UTC()
	path, err := s.path(requestID)
	if err != nil {
		return RelayRecord{}, err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return RelayRecord{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(blob, '\n'), 0o600); err != nil {
		return RelayRecord{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return RelayRecord{}, err
	}
	return rec, nil
}

// RelaySubmission is one relay, as the control process received it.
type RelaySubmission struct {
	Artifact  string
	Principal RelayPrincipal
	Exchanges ExchangeLog
	Store     RelayStore
	// Reviews is the common semantic record every transport converges on. A
	// published relay writes the ORIGINAL reviewer artifact here, so the same
	// bytes mean the same thing whether they came down this pipe or the mailbox.
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
// answers, records it once, and has the App publish it.
//
// Called ONLY by the control process's relay socket handler, with the principal
// that socket observed; nothing in the review runner or the engine calls it.
// Either the artifact is accepted exactly as submitted, or it is refused and
// nothing durable happens.
//
// Resubmitting the identical artifact is how a failed publication is retried.
// A DIFFERENT artifact for a request that already has one is refused: an
// accepted review is never replaced.
func AcceptRelayedReview(ctx context.Context, in RelaySubmission) (RelayRecord, error) {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	if in.Exchanges.Dir == "" || in.Store.Dir == "" {
		return RelayRecord{}, refused("this process keeps no review exchange log or relay store")
	}
	if err := relayPublisherReady(in.Mailbox); err != nil {
		return RelayRecord{}, refused("%v", err)
	}
	if in.Principal.PID <= 0 || in.Principal.Terminal == 0 {
		return RelayRecord{}, refused("a relay must name the terminal principal that carried it")
	}
	art, err := ParseRelayArtifact(in.Artifact)
	if err != nil {
		return RelayRecord{}, refused("%v", err)
	}

	// Resolved through the one owner of review-obligation lifetime, so a relay
	// is accepted against what this workspace actually owes rather than against
	// a rule this handler invented for itself (#182 R4).
	owned := ReviewObligationStore{Exchanges: in.Exchanges}
	rec, err := owned.ByRequest(art.RequestID)
	if err != nil {
		return RelayRecord{}, refusedBecause(err)
	}
	if mismatch := subjectMismatch(rec.Subject(), art.Subject); mismatch != "" {
		return RelayRecord{}, refused("the review is not about the candidate request %s carried: %s", art.RequestID, mismatch)
	}
	// The artifact must name the provider the WORKFLOW assigned when the request
	// was published. A record that never captured the assignment cannot
	// authenticate anybody: inferring it from today's configuration would invent
	// the very fact this check exists to verify, so an unknown assignment is a
	// refusal and the request may be superseded instead.
	if strings.TrimSpace(rec.ReviewerProvider) == "" {
		return RelayRecord{}, refused("request %s predates the recorded reviewer assignment, so this artifact cannot be "+
			"authenticated against who was asked; supersede it with a new request rather than relaying against it", art.RequestID)
	}
	if !sameProvider(art.Provider, rec.ReviewerProvider) {
		return RelayRecord{}, refused("the artifact names reviewer %q and request %s was assigned to %q",
			art.Provider, art.RequestID, rec.ReviewerProvider)
	}
	if rec.Conversation != "" && rec.Conversation != in.Mailbox.Number {
		return RelayRecord{}, refused("request %s was published in conversation %s and this process publishes to %s",
			art.RequestID, rec.Conversation, in.Mailbox.Number)
	}
	binding := roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA, CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree}
	verdict, err := workflow.ValidateReviewBody(art.Body, binding, art.Provider)
	if err != nil {
		return RelayRecord{}, refused("the reviewer payload does not satisfy the reviewer contract: %v", err)
	}

	record := RelayRecord{
		Version: 1, State: RelayAccepted,
		TaskID: rec.TaskID, RequestID: rec.RequestID, RequestComment: rec.RequestComment,
		Conversation: in.Mailbox.Number,
		BaseSHA:      rec.BaseSHA, CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree,
		ReviewCommit: rec.ReviewCommit,
		Reviewer:     art.Provider, ReviewDigest: art.Digest,
		Decision: string(verdict.Decision), Summary: verdict.Summary, Findings: verdict.Findings,
		Artifact: art.Raw, Standing: "advisory",
		RelayPrincipal: in.Principal, AcceptedAt: now().UTC(),
	}
	existing, found, err := in.Store.Load(art.RequestID)
	if err != nil {
		return RelayRecord{}, refused("%v", err)
	}
	if !found {
		if err := in.Store.create(record); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return RelayRecord{}, refused("the relay could not be recorded: %v", err)
			}
			if existing, found, err = in.Store.Load(art.RequestID); err != nil || !found {
				return RelayRecord{}, refused("the relay receipt for %s could not be read back", art.RequestID)
			}
		}
	}
	if found {
		if existing.ReviewDigest != art.Digest {
			return RelayRecord{}, refused("a different review (%s) is already accepted for request %s; an accepted review is not replaced",
				existing.ReviewDigest, art.RequestID)
		}
		record = existing
	}
	if record.State == RelayPublished {
		// Already on the mailbox. The only thing that can still be owed is the
		// common-store record, so resubmitting the identical artifact retries
		// exactly that and publishes nothing a second time.
		return record, convergeRelay(in, record)
	}
	published, err := publishRelayRecord(ctx, in.Mailbox, in.Store, record, now)
	if err != nil {
		return published, err
	}
	return published, convergeRelay(in, published)
}

// convergeRelay records a PUBLISHED relay's original reviewer artifact in the
// common review store, with relay and publication evidence.
//
// Only after publication, deliberately. An accepted-but-unpublished relay is
// not yet an answer anybody may consume, and writing it here early would make
// the common store say a request was answered while the mailbox still showed
// nothing -- the relay's own two-step lifecycle leaking into review meaning.
//
// The bytes written are the reviewer's own, exactly as submitted. The terminal
// principal and the App publication go in as EVIDENCE beside them: the operator
// carried this verdict and the App posted it, and neither of them produced it.
func convergeRelay(in RelaySubmission, rec RelayRecord) error {
	if in.Reviews.Dir == "" {
		return nil
	}
	binding := roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA,
		CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree}
	_, err := in.Reviews.Accept(reviewstore.Acceptance{
		RequestID: rec.RequestID,
		Artifact:  rec.Artifact,
		Evidence: reviewstore.Evidence{
			Transport:          reviewstore.LocalRelay,
			RelayPrincipal:     rec.RelayPrincipal.token(),
			Publication:        rec.Publication,
			PublicationComment: rec.PublicationComment,
			PublishedAt:        rec.PublishedAt,
		},
		Validate: func(a reviewartifact.Artifact) error {
			_, verr := workflow.ValidateReviewBody(a.Body, binding, a.ReviewerProvider)
			return verr
		},
		Now: in.Now,
	})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrRelayConvergence, err)
	}
	return nil
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

// RenderRelayedReview renders the App's publication of an accepted relay.
//
// Its envelope is NOT a review envelope, and it carries no review or request
// marker anywhere: nothing reading the mailbox can take the publication for an
// answer. The decision lives in the prose, beside who produced it and who
// relayed it, never in the envelope.
func RenderRelayedReview(rec RelayRecord, box Issue) (string, error) {
	var b strings.Builder
	b.WriteString(relayedReviewMarker + "\n")
	fmt.Fprintf(&b, "task=%s\n", rec.TaskID)
	fmt.Fprintf(&b, "request=%s\n", rec.RequestID)
	fmt.Fprintf(&b, "base=%s\n", rec.BaseSHA)
	fmt.Fprintf(&b, "candidate_digest=%s\n", rec.CandidateDigest)
	fmt.Fprintf(&b, "candidate_tree=%s\n", rec.CandidateTree)
	fmt.Fprintf(&b, "review_commit=%s\n", rec.ReviewCommit)
	fmt.Fprintf(&b, "reviewer_provider=%s\n", rec.Reviewer)
	fmt.Fprintf(&b, "review_digest=%s\n", rec.ReviewDigest)
	fmt.Fprintf(&b, "relay_principal=%s\n", rec.RelayPrincipal.token())
	fmt.Fprintf(&b, "publication=%s\n", publicationIdentity(box))
	fmt.Fprintf(&b, "standing=%s\n", rec.Standing)
	fmt.Fprintf(&b, "\nRelayed review for request %s.\n\n", rec.RequestID)
	fmt.Fprintf(&b, "The technical verdict below was produced by reviewer `%s`. A local operator relayed it from a "+
		"controlling terminal and did not author it; sensei-code's GitHub App publishes it. Its standing is advisory: "+
		"it satisfies no independent-review obligation and is not Sensei admission.\n\n", rec.Reviewer)
	fmt.Fprintf(&b, "Decision: %s\n\nSummary: %s\n", rec.Decision, rec.Summary)
	if len(rec.Findings) > 0 {
		b.WriteString("\nFindings:\n")
		for _, f := range rec.Findings {
			b.WriteString("- " + f.Line() + "\n")
		}
	}
	body := b.String()
	if strings.Contains(body, reviewMarker) || strings.Contains(body, requestMarker) ||
		strings.Count(body, "[sensei-code:") != 1 {
		return "", errors.New("the publication would carry a protocol marker beyond its own envelope, so it is not posted")
	}
	return body, nil
}

func publishRelayRecord(ctx context.Context, box Issue, store RelayStore, rec RelayRecord, now func() time.Time) (RelayRecord, error) {
	body, err := RenderRelayedReview(rec, box)
	if err != nil {
		return rec, fmt.Errorf("%w: %v", ErrRelayPublication, err)
	}
	comment, err := box.API.PostComment(ctx, box.Number, body)
	if err != nil {
		return rec, fmt.Errorf("%w: %v; resubmit the same artifact to retry publication", ErrRelayPublication, err)
	}
	published, err := store.markPublished(rec.RequestID, rec.ReviewDigest, comment, publicationIdentity(box), now())
	if err != nil {
		return rec, fmt.Errorf("%w: posted as comment %d, and the receipt could not record it: %v", ErrRelayPublication, comment, err)
	}
	return published, nil
}

// verifyRelayRecord proves a receipt is the relay of THIS owed request, from the
// artifact it holds rather than from its own summary fields.
func verifyRelayObligation(relay RelayRecord, owed ReviewObligation) (RelayArtifact, error) {
	if relay.State != RelayAccepted && relay.State != RelayPublished {
		return RelayArtifact{}, fmt.Errorf("the receipt is in unknown state %q", relay.State)
	}
	art, err := ParseRelayArtifact(relay.Artifact)
	if err != nil {
		return RelayArtifact{}, err
	}
	if art.Digest != relay.ReviewDigest {
		return RelayArtifact{}, fmt.Errorf("the stored artifact digests to %s and the receipt names %s", art.Digest, relay.ReviewDigest)
	}
	if relay.RequestID != owed.RequestID || art.RequestID != owed.RequestID {
		return RelayArtifact{}, fmt.Errorf("the receipt answers request %s and the owed request is %s", art.RequestID, owed.RequestID)
	}
	if m := subjectMismatch(owed.Subject(), art.Subject); m != "" {
		return RelayArtifact{}, errors.New(m)
	}
	if m := subjectMismatch(owed.Subject(), relay.Subject()); m != "" {
		return RelayArtifact{}, errors.New(m)
	}
	if relay.RelayPrincipal.PID <= 0 || relay.RelayPrincipal.Terminal == 0 {
		return RelayArtifact{}, errors.New("the receipt names no terminal principal")
	}
	return art, nil
}

// pendingRelayFor reports a relay that exists for THIS obligation and has NOT
// become the answer, so the candidate waits under the SAME request instead of
// being asked about again.
//
// It returns no verdict, and that is the point. Before R2 this path consumed a
// published relay directly, which left two semantic sources. ReviewStore is the
// only place an answer comes from; RelayStore owns retry and publication state,
// and says what is still owed.
//
// Handed the obligation rather than scanning for one (#182 R4): what is owed is
// the obligation owner's to decide. Nothing here reads the mailbox -- whether a
// relay was published is recorded in its receipt at the moment the App posted
// it, and re-asking GitHub would put a live availability check back into a path
// that decides whether a candidate waits.
func (r *Runner) pendingRelayFor(o ReviewObligation) (bool, error) {
	if r.Relays.Dir == "" {
		return false, nil
	}
	relay, found, err := r.Relays.Load(o.RequestID)
	if !found && err == nil {
		return false, nil
	}
	if err != nil {
		return true, obligationStands(o, err)
	}
	if _, err := verifyRelayObligation(relay, o); err != nil {
		return true, obligationStands(o, err)
	}
	if relay.State != RelayPublished {
		return true, obligationStands(o, errors.New("it is accepted and not yet published; resubmit the same "+
			"artifact with `sensei-code review submit` to publish it"))
	}
	// Published, and the common store has no record of it -- storedReviewFor
	// runs first and would already have answered. So convergence did not
	// complete. The repair is to retry the store write, never to publish again
	// and never to answer from here.
	return true, obligationStands(o, fmt.Errorf(
		"reviewer %s produced it and the App published it as comment %d, and it is not recorded in the review "+
			"store; resubmit the same artifact with `sensei-code review submit` to record it, which publishes nothing further",
		relay.Reviewer, relay.PublicationComment))
}

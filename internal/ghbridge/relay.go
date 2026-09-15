package ghbridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
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
	// maxRelayArtifactBytes bounds one artifact. It is a review, not a file.
	maxRelayArtifactBytes = 64 << 10

	// RelayAccepted is a relay validated and durably recorded, not yet published.
	RelayAccepted = "accepted"
	// RelayPublished is a relay the App has published on the mailbox.
	RelayPublished = "published"
)

var (
	providerShape = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

	// ErrRelayRefused reports a relay that was not accepted. Nothing durable was
	// written and nothing was published.
	ErrRelayRefused = errors.New("the relayed review was refused")
	// ErrRelayPublication reports a relay that WAS accepted and whose publication
	// did not complete. The accepted review is preserved.
	ErrRelayPublication = errors.New("the relayed review is accepted and preserved, and its publication did not complete")
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
type RelayArtifact struct {
	Review
	// Provider is the reviewer the artifact names. Claimed, not verified.
	Provider string
	// Raw is the artifact byte for byte, and Digest names those bytes.
	Raw    string
	Digest string
}

// ReviewDigest names the exact bytes of a relayed artifact.
func ReviewDigest(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// ParseRelayArtifact reads a complete review artifact, or refuses it whole.
//
// The artifact is the reviewer's own [sensei-code:review] envelope, with every
// identity field, a reviewer=<provider> line, and the reviewer payload after it.
// Nothing is filled in, trimmed or repaired: a relay that needed editing to
// parse is not the review the reviewer produced.
func ParseRelayArtifact(raw string) (RelayArtifact, error) {
	if len(raw) > maxRelayArtifactBytes {
		return RelayArtifact{}, fmt.Errorf("the artifact is %d bytes; a review artifact is bounded at %d", len(raw), maxRelayArtifactBytes)
	}
	if !strings.HasPrefix(strings.TrimLeft(raw, " \t\r\n"), reviewMarker) {
		return RelayArtifact{}, errors.New("the artifact must begin with the " + reviewMarker + " envelope the reviewer produced")
	}
	// Exactly one envelope and no other protocol marker anywhere: a second
	// envelope, a request or a relay record inside the payload would make one
	// artifact say two things about which question it answers.
	if strings.Count(raw, "[sensei-code:") != 1 {
		return RelayArtifact{}, errors.New("the artifact must carry exactly one sensei-code envelope and no other protocol marker")
	}
	f, _, _ := fields(raw, reviewMarker)
	provider := f["reviewer"]
	if !providerShape.MatchString(provider) {
		return RelayArtifact{}, fmt.Errorf("the artifact must name its reviewer on a reviewer=<provider> line, got %q", provider)
	}
	rev, ok := ParseReview(raw, "")
	if !ok {
		return RelayArtifact{}, errors.New("the review envelope is incomplete: it must state task, request, base, candidate_digest, candidate_tree and review_commit, and carry a reviewer payload")
	}
	return RelayArtifact{Review: rev, Provider: provider, Raw: raw, Digest: ReviewDigest(raw)}, nil
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
	Mailbox   Issue
	Now       func() time.Time
}

func refused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRelayRefused, fmt.Sprintf(format, args...))
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

	owed, err := in.Exchanges.PendingReviews()
	if err != nil {
		return RelayRecord{}, refused("the review exchange log could not be read in full: %v", err)
	}
	var rec *ExchangeRecord
	for i := range owed {
		if owed[i].RequestID == art.RequestID {
			rec = &owed[i]
			break
		}
	}
	if rec == nil {
		return RelayRecord{}, refused("request %s is not a review owed in this workspace: it was answered, superseded, "+
			"withdrawn, or never published here", art.RequestID)
	}
	if mismatch := subjectMismatch(rec.Subject(), art.Subject); mismatch != "" {
		return RelayRecord{}, refused("the review is not about the candidate request %s carried: %s", art.RequestID, mismatch)
	}
	if rec.Conversation != "" && rec.Conversation != in.Mailbox.Number {
		return RelayRecord{}, refused("request %s was published in conversation %s and this process publishes to %s",
			art.RequestID, rec.Conversation, in.Mailbox.Number)
	}
	binding := roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA, CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree}
	verdict, err := workflow.ValidateRelayedReview(art.Body, binding, art.Provider)
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
		return record, nil
	}
	return publishRelayRecord(ctx, in.Mailbox, in.Store, record, now)
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
func verifyRelayRecord(relay RelayRecord, owed ExchangeRecord) (RelayArtifact, error) {
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

// publicationStands finds the App's publication of this receipt on the mailbox.
func (r *Runner) publicationStands(ctx context.Context, relay RelayRecord) error {
	if err := relayPublisherReady(r.Issue); err != nil {
		return err
	}
	comments, err := r.Issue.API.ListComments(ctx, r.Issue.Number)
	if err != nil {
		return fmt.Errorf("reading the mailbox for publication comment %d: %w", relay.PublicationComment, err)
	}
	for _, c := range comments {
		if c.ID != relay.PublicationComment || relay.PublicationComment <= 0 {
			continue
		}
		f, _, ok := fields(c.Body, relayedReviewMarker)
		if ok && f["request"] == relay.RequestID && f["review_digest"] == relay.ReviewDigest {
			return nil
		}
		return fmt.Errorf("comment %d is not the publication of the relay of %s", c.ID, relay.RequestID)
	}
	return fmt.Errorf("publication comment %d is not on conversation %s", relay.PublicationComment, r.Issue.Number)
}

// relayedReviewFor answers a review turn from a relayed review, when the owed
// request for this exact candidate has one.
//
// It consumes only a relay that is published AND whose publication is on the
// mailbox, and it never publishes: publication is the terminal-authorized
// relay handler's, so a receipt that did not come through it is not made real
// here. A relay that exists and cannot be consumed yet keeps its request owed --
// the candidate waits under the SAME request rather than having it superseded.
func (r *Runner) relayedReviewFor(ctx context.Context, req agent.Request, subject Subject, emit func(event.Event)) (agent.Result, bool, error) {
	if r.Relays.Dir == "" || r.Exchanges.Dir == "" {
		return agent.Result{}, false, nil
	}
	owed, _ := r.Exchanges.PendingReviews()
	for _, rec := range owed {
		if rec.TaskID != req.TaskID || rec.BaseSHA != subject.BaseSHA ||
			rec.CandidateDigest != subject.CandidateDigest || rec.CandidateTree != subject.CandidateTree {
			continue
		}
		relay, found, err := r.Relays.Load(rec.RequestID)
		if !found && err == nil {
			continue
		}
		unconsumable := func(cause error) (agent.Result, bool, error) {
			return agent.Result{}, true, &roles.ReviewUnanswered{
				RequestID: rec.RequestID, RequestComment: rec.RequestComment, Conversation: rec.Conversation,
				Binding: roles.Binding{TaskID: rec.TaskID, BaseSHA: rec.BaseSHA,
					CandidateDigest: rec.CandidateDigest, CandidateTree: rec.CandidateTree},
				ReviewCommit: rec.ReviewCommit,
				Cause:        fmt.Errorf("a relayed review for request %s exists and was not consumed: %w", rec.RequestID, cause),
			}
		}
		if err != nil {
			return unconsumable(err)
		}
		art, err := verifyRelayRecord(relay, rec)
		if err != nil {
			return unconsumable(err)
		}
		if relay.State != RelayPublished {
			return unconsumable(errors.New("it is accepted and not yet published; resubmit the same artifact with " +
				"`sensei-code review submit` to publish it"))
		}
		if err := r.publicationStands(ctx, relay); err != nil {
			return unconsumable(err)
		}
		if err := r.Exchanges.Close(rec.TaskID, rec.RequestID); err != nil && emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.Status,
				"the relayed review's exchange record could not be closed: "+err.Error(),
				map[string]any{"request_id": rec.RequestID, "error": err.Error(), "transport": "github-relay"}))
		}
		if emit != nil {
			emit(event.New(r.SessionID, req.TaskID, event.SourceReviewer, event.AgentFinished,
				fmt.Sprintf("the verdict reviewer %s produced for request %s was relayed by a local operator and published "+
					"by the App as comment %d; it is consumed with advisory standing", relay.Reviewer, rec.RequestID,
					relay.PublicationComment),
				map[string]any{
					"request_id":          rec.RequestID,
					"reviewer_provider":   relay.Reviewer,
					"review_digest":       relay.ReviewDigest,
					"relay_principal":     relay.RelayPrincipal,
					"publication":         relay.Publication,
					"publication_comment": relay.PublicationComment,
					"base":                rec.BaseSHA,
					"candidate_digest":    rec.CandidateDigest,
					"candidate_tree":      rec.CandidateTree,
					"review_commit":       rec.ReviewCommit,
					"standing":            relay.Standing,
					"transport":           "github-relay",
				}))
		}
		return agent.Result{Text: art.Body, Session: roles.Unverified}, true, nil
	}
	return agent.Result{}, false, nil
}

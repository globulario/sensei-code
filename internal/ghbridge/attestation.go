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

// A human overriding a review obligation, recorded where the relay is recorded
// and published where the relay is published -- and named, everywhere, as an
// override rather than a review. See roles.Attestation for why the distinction
// is the whole point.

const attestationMarker = "[sensei-code:attestation]"

const (
	// AttestationAccepted is a validated attestation not yet published.
	AttestationAccepted = "accepted"
	// AttestationPublished is one the App has published on the mailbox.
	AttestationPublished = "published"

	// AttestationSchemaVersion is the record shape this code writes and can
	// read back. A record on another version is refused rather than
	// interpreted: reading an authority object under the wrong schema is how a
	// field means something its writer never said.
	AttestationSchemaVersion = 1
)

// ErrAttestationRefused reports an attestation that was not accepted. Nothing
// durable was written and nothing was published.
var ErrAttestationRefused = errors.New("the attestation was refused")

// AttestationRecord is the durable receipt of one accepted override.
type AttestationRecord struct {
	Version            int               `json:"version"`
	State              string            `json:"state"`
	Attestation        roles.Attestation `json:"attestation"`
	AcceptedAt         time.Time         `json:"accepted_at"`
	Publication        string            `json:"publication,omitempty"`
	PublicationComment int64             `json:"publication_comment,omitempty"`
	PublishedAt        time.Time         `json:"published_at,omitempty"`
}

// AttestationStore holds one record per attested request.
type AttestationStore struct {
	Dir string
}

func (s AttestationStore) path(requestID string) (string, error) {
	if s.Dir == "" {
		return "", errors.New("the attestation store has no directory")
	}
	if !exchangeFileSafe.MatchString(requestID) {
		return "", fmt.Errorf("request id %q is not a safe file name", requestID)
	}
	return filepath.Join(s.Dir, requestID+".json"), nil
}

// Load reads the record for a request, if one exists.
func (s AttestationStore) Load(requestID string) (AttestationRecord, bool, error) {
	path, err := s.path(requestID)
	if err != nil {
		return AttestationRecord{}, false, err
	}
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return AttestationRecord{}, false, nil
	}
	if err != nil {
		return AttestationRecord{}, false, err
	}
	var rec AttestationRecord
	if err := json.Unmarshal(blob, &rec); err != nil {
		return AttestationRecord{}, false, fmt.Errorf("the attestation for %s is unreadable: %w", requestID, err)
	}
	return rec, true, nil
}

func (s AttestationStore) create(rec AttestationRecord) error {
	path, err := s.path(rec.Attestation.RequestID)
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

func (s AttestationStore) markPublished(requestID, digest string, comment int64, publication string, at time.Time) (AttestationRecord, error) {
	rec, found, err := s.Load(requestID)
	if err != nil {
		return AttestationRecord{}, err
	}
	if !found || rec.Attestation.ReviewDigest != digest {
		return AttestationRecord{}, fmt.Errorf("the accepted attestation for %s is not the one being published", requestID)
	}
	rec.State = AttestationPublished
	rec.Publication = publication
	rec.PublicationComment = comment
	rec.PublishedAt = at.UTC()
	path, err := s.path(requestID)
	if err != nil {
		return AttestationRecord{}, err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return AttestationRecord{}, err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(blob, '\n'), 0o600); err != nil {
		return AttestationRecord{}, err
	}
	if err := os.Rename(tmp, path); err != nil {
		return AttestationRecord{}, err
	}
	return rec, nil
}

// AttestationFor is the published override covering this exact candidate AND
// this exact review, if this workspace holds one.
//
// The digest is a selection key, not a filter applied afterwards. One unchanged
// candidate legitimately accumulates overrides over time -- request R1 with
// review D1, then R2 with D2 after the first review went unanswered -- and every
// one of them covers that candidate. Selecting on the candidate alone returns
// whichever file the directory happened to yield first, so a valid override for
// the review actually being consumed became invisible behind an older one, and
// the candidate fell back to waiting. Which override applied depended on file
// ordering, which is the kind of defect that reads as "the review machinery
// randomly blocks".
//
// Published only: an attestation the App never posted is not yet part of the
// record anyone else can read, and consuming one would let a local file alone
// advance a candidate. It satisfies workflow.AttestationSource.
//
// Every record it reads must satisfy usableAsHistory first -- the same contract
// AcceptAttestation applies before reusing one. Structural and lifecycle only:
// no transport is re-contacted, no file is rewritten, and the frozen legacy
// statement stays valid.
func (s AttestationStore) AttestationFor(b roles.Binding, reviewDigest string) (roles.Attestation, bool, error) {
	if s.Dir == "" {
		return roles.Attestation{}, false, nil
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return roles.Attestation{}, false, nil
		}
		return roles.Attestation{}, false, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		var rec AttestationRecord
		blob, err := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if err != nil || json.Unmarshal(blob, &rec) != nil {
			return roles.Attestation{}, false, fmt.Errorf("unreadable attestation record: %s", e.Name())
		}
		// The SAME validity contract the retry path applies. One persisted
		// authority object cannot have two standards: refusing a malformed
		// record when republishing it and honouring the same record when
		// consuming it would mean the override that advances a candidate was
		// held to the weaker of the two.
		//
		// Reported rather than skipped, like the unreadable case above and for
		// the same reason: local authority state that cannot be read is not
		// "there is no override", and the engine turns that error into a
		// candidate that stays advisory. Nothing is rewritten, and no legacy
		// statement is weakened -- the pre-R3 writer already recorded a version,
		// an acceptance time and complete publication details.
		if err := rec.usableAsHistory(); err != nil {
			return roles.Attestation{}, false, fmt.Errorf("unusable attestation record %s: %w", e.Name(), err)
		}
		if rec.State != AttestationPublished {
			continue
		}
		// Asked with the digest of the review being consumed, so the answer is
		// "the override for THIS review", never "an override for this candidate".
		// The caller checks Covers again against the same digest: this selects,
		// that verifies, and neither stands in for the other.
		if rec.Attestation.Covers(b, reviewDigest) == nil {
			return rec.Attestation, true, nil
		}
	}
	return roles.Attestation{}, false, nil
}

// AttestationSubmission is one override, as the control process received it.
type AttestationSubmission struct {
	RequestID    string
	ReviewDigest string
	Principal    RelayPrincipal
	// Permitted is the owner's local grant of this authority (config). Refused
	// here rather than ignored: an override nobody authorized is not an override.
	Permitted bool
	// Reviews is the canonical review being overridden.
	//
	// The common store since #182 R3, deliberately not RelayStore. An owner
	// overrides a specific REVIEW, not a delivery receipt: while attestation
	// read the relay receipt, two semantically identical advisory ACCEPTs had
	// different owner authority depending on which pipe carried them, and a
	// review read directly off the authenticated mailbox could not be attested
	// at all.
	Reviews reviewstore.Store
	Store   AttestationStore
	Mailbox Issue
	Now     func() time.Time
}

func attestationRefused(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrAttestationRefused, fmt.Sprintf(format, args...))
}

// AcceptAttestation records a local operator's override of one exact canonical
// review, and has the App publish it.
//
// Called ONLY by the control process's attestation socket handler, with the
// principal that socket observed. The operator must name both the request and
// the review digest: an override typed from memory against "whatever review is
// there" would cover an artifact its author never read.
//
// What is overridden is the canonical artifact in the common review store.
// Reviewer, decision and candidate binding are RE-DERIVED from those exact
// bytes rather than read from any receipt beside them, so no transport fact can
// decide who reviewed, what they decided, or which candidate it was about. How
// the review arrived is recorded in the store as evidence and is not consulted
// here: a mailbox review and a relayed review with the same bytes are the same
// review, and the override says the same thing about either.
//
// No transport is re-contacted. The store proved its own acceptance residue
// when it was written (#182 R2), and re-proving delivery now would make a
// human override depend on GitHub still being reachable.
func AcceptAttestation(ctx context.Context, in AttestationSubmission) (AttestationRecord, error) {
	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	if !in.Permitted {
		return AttestationRecord{}, attestationRefused(
			"this workspace has not granted owner-attestation authority; set workflow.owner_attestation in .sensei-code config")
	}
	if in.Store.Dir == "" || in.Reviews.Dir == "" {
		return AttestationRecord{}, attestationRefused("this process keeps no review store or attestation store")
	}
	if err := relayPublisherReady(in.Mailbox); err != nil {
		return AttestationRecord{}, attestationRefused("%v", err)
	}
	if in.Principal.PID <= 0 || in.Principal.Terminal == 0 {
		return AttestationRecord{}, attestationRefused("an attestation must name the terminal principal who made it")
	}

	requestID := strings.TrimSpace(in.RequestID)
	stored, found, err := in.Reviews.Load(requestID)
	if err != nil {
		return AttestationRecord{}, attestationRefused("%v", err)
	}
	if !found {
		return AttestationRecord{}, attestationRefused("no canonical review is recorded for request %s", requestID)
	}
	// AN OVERRIDE COVERS A REVIEW THIS WORKSPACE ACCEPTED, not one it is holding.
	//
	// A relay stages exact reviewer bytes before the App has published them, so
	// a record can exist whose delivery never completed. Overriding that would
	// let the owner authorize on evidence nobody outside this machine can read
	// -- and the same question, "may this review be used", is asked here and in
	// the runner through the one predicate that answers it (#182 R6).
	if !stored.Consumable() {
		return AttestationRecord{}, attestationRefused(
			"the review recorded for %s has no completed delivery, so there is nothing yet to override", requestID)
	}
	// The operator names the digest, and it must be the one on record. This is
	// what ties the override to a review its author actually read, and it is
	// checked before anything durable exists.
	if stored.ReviewDigest != strings.TrimSpace(in.ReviewDigest) {
		return AttestationRecord{}, attestationRefused(
			"the review recorded for %s is %s and the attestation names %s", requestID, stored.ReviewDigest, in.ReviewDigest)
	}
	// Everything the override asserts comes from the reviewer's own bytes.
	art, err := stored.Artifact()
	if err != nil {
		return AttestationRecord{}, attestationRefused("%v", err)
	}
	binding := roles.Binding{TaskID: art.TaskID, BaseSHA: art.BaseSHA,
		CandidateDigest: art.CandidateDigest, CandidateTree: art.CandidateTree}
	// The decision is read by the one component that reads decisions, from the
	// payload inside those bytes. An override of an ACCEPT that was never an
	// ACCEPT would advance a candidate its reviewer objected to.
	verdict, err := workflow.ValidateReviewBody(art.Body, binding, art.ReviewerProvider)
	if err != nil {
		return AttestationRecord{}, attestationRefused("the reviewer payload does not satisfy the reviewer contract: %v", err)
	}

	att := roles.Attestation{
		RequestID:    art.RequestID,
		ReviewDigest: stored.ReviewDigest,
		Reviewer:     art.ReviewerProvider,
		Decision:     verdict.Decision,
		Binding:      binding,
		Principal:    in.Principal.token(),
		At:           now().UTC(),
		Statement:    roles.AttestationStatement,
	}
	if err := att.Validate(); err != nil {
		return AttestationRecord{}, attestationRefused("%v", err)
	}

	record := AttestationRecord{Version: 1, State: AttestationAccepted, Attestation: att, AcceptedAt: att.At}
	existing, have, err := in.Store.Load(att.RequestID)
	if err != nil {
		return AttestationRecord{}, attestationRefused("%v", err)
	}
	if !have {
		if err := in.Store.create(record); err != nil {
			if !errors.Is(err, os.ErrExist) {
				return AttestationRecord{}, attestationRefused("the attestation could not be recorded: %v", err)
			}
			if existing, have, err = in.Store.Load(att.RequestID); err != nil || !have {
				return AttestationRecord{}, attestationRefused("the attestation for %s could not be read back", att.RequestID)
			}
		}
	}
	if have {
		if existing.Attestation.ReviewDigest != att.ReviewDigest {
			return AttestationRecord{}, attestationRefused(
				"request %s is already attested for review %s; an attestation is not replaced",
				att.RequestID, existing.Attestation.ReviewDigest)
		}
		// Preserving an existing override's owner facts is only defensible when
		// those facts form a VALID historical override. Load unmarshals JSON and
		// proves nothing, so a record could agree with the canonical review on
		// every review-derived field while carrying no principal, a statement
		// outside the closed set, or a zero attested time -- and the retry would
		// publish it, announcing a human override whose stored act does not
		// satisfy roles.Attestation.Validate.
		//
		// Preservation is not permission to publish malformed authority. Checked
		// BEFORE the meaning comparison, so an unreadable record is reported as
		// unreadable rather than as a disagreement about a review.
		if err := existing.usableAsHistory(); err != nil {
			return AttestationRecord{}, attestationRefused(
				"request %s already has an attestation that is not a valid record: %v; "+
					"it is preserved unchanged and nothing was published", att.RequestID, err)
		}
		// A matching digest is not agreement. The stored override states what
		// review it is about, and if that disagrees with what the canonical
		// bytes actually say, reusing it would let the older record supply the
		// meaning this function just re-derived -- an attestation store acting
		// as a second semantic source. A reviewer mismatch is the sharp case:
		// Covers() checks candidate and digest, not who reviewed, so such a
		// record can advance a candidate while naming the wrong reviewer.
		if m := reviewMeaningMismatch(existing.Attestation, att); m != "" {
			return AttestationRecord{}, attestationRefused(
				"request %s already has an attestation that disagrees with the canonical review: %s; "+
					"it is preserved unchanged and nothing was published", att.RequestID, m)
		}
		// The review agrees, so the OWNER's facts stay the existing record's:
		// who attested, when, the statement they attested under (current or the
		// frozen legacy one) and how far publication got. Those are history, not
		// something this call re-derives.
		record = existing
	}
	if record.State == AttestationPublished {
		return record, nil
	}
	return publishAttestation(ctx, in.Mailbox, in.Store, record, now)
}

// usableAsHistory states what a stored override must prove about itself before
// this process will stand behind it -- reuse it, publish it, or return it as
// the authority covering a candidate.
//
// Structural and lifecycle only. It asks nothing about authenticity, which is
// #184's question about every governed local store; it asks whether this record
// is the kind of object its own schema describes.
func (r AttestationRecord) usableAsHistory() error {
	if r.Version != AttestationSchemaVersion {
		return fmt.Errorf("it declares schema version %d and this package writes %d", r.Version, AttestationSchemaVersion)
	}
	switch r.State {
	case AttestationAccepted, AttestationPublished:
	default:
		return fmt.Errorf("it is in unknown state %q", r.State)
	}
	if r.AcceptedAt.IsZero() {
		return errors.New("it does not say when it was accepted")
	}
	if err := r.Attestation.Validate(); err != nil {
		return err
	}
	if r.Attestation.At.IsZero() {
		return errors.New("it does not say when it was attested")
	}
	if r.State == AttestationPublished {
		if strings.TrimSpace(r.Publication) == "" || r.PublicationComment <= 0 || r.PublishedAt.IsZero() {
			return errors.New("it claims publication and does not say by whom, as which comment, or when")
		}
		return nil
	}
	// ACCEPTED. Stray publication fields are not proof that anything was
	// published: a record that is half-published is a record whose lifecycle
	// nobody can read, and treating those fields as evidence would let one be
	// consumed as though the App had posted it.
	if strings.TrimSpace(r.Publication) != "" || r.PublicationComment != 0 || !r.PublishedAt.IsZero() {
		return errors.New("it is accepted and carries publication details, so its lifecycle cannot be read")
	}
	return nil
}

// reviewMeaningMismatch names the first review-derived field on which a stored
// override disagrees with the canonical review, or "" when they agree.
//
// Only the fields that describe the REVIEW participate. Principal, At and
// Statement are the owner's own record of what they did and when, and are
// deliberately absent: a historical override carrying the frozen pre-R3
// statement still describes the same review, and retrying its publication must
// not require the operator to re-attest under today's wording.
func reviewMeaningMismatch(stored, canonical roles.Attestation) string {
	for _, f := range []struct{ name, stored, canonical string }{
		{"request", stored.RequestID, canonical.RequestID},
		{"review_digest", stored.ReviewDigest, canonical.ReviewDigest},
		{"reviewer", stored.Reviewer, canonical.Reviewer},
		{"decision", string(stored.Decision), string(canonical.Decision)},
		{"task", stored.Binding.TaskID, canonical.Binding.TaskID},
		{"base", stored.Binding.BaseSHA, canonical.Binding.BaseSHA},
		{"candidate_digest", stored.Binding.CandidateDigest, canonical.Binding.CandidateDigest},
		{"candidate_tree", stored.Binding.CandidateTree, canonical.Binding.CandidateTree},
	} {
		if f.stored != f.canonical {
			return fmt.Sprintf("it records %s %q and the canonical review says %q", f.name, f.stored, f.canonical)
		}
	}
	return ""
}

// RenderAttestation renders the App's publication of one override.
//
// Its envelope is its own, and it carries no review or request marker: nothing
// reading the mailbox can take an override for a review, which is the confusion
// the whole type exists to prevent.
func RenderAttestation(rec AttestationRecord, box Issue) (string, error) {
	a := rec.Attestation
	var b strings.Builder
	b.WriteString(attestationMarker + "\n")
	fmt.Fprintf(&b, "task=%s\n", a.Binding.TaskID)
	fmt.Fprintf(&b, "request=%s\n", a.RequestID)
	fmt.Fprintf(&b, "base=%s\n", a.Binding.BaseSHA)
	fmt.Fprintf(&b, "candidate_digest=%s\n", a.Binding.CandidateDigest)
	fmt.Fprintf(&b, "candidate_tree=%s\n", a.Binding.CandidateTree)
	fmt.Fprintf(&b, "review_digest=%s\n", a.ReviewDigest)
	fmt.Fprintf(&b, "reviewer_provider=%s\n", a.Reviewer)
	fmt.Fprintf(&b, "attested_decision=%s\n", a.Decision)
	fmt.Fprintf(&b, "attesting_principal=%s\n", a.Principal)
	fmt.Fprintf(&b, "publication=%s\n", publicationIdentity(box))
	fmt.Fprintf(&b, "standing=human_override\n")
	fmt.Fprintf(&b, "\nHuman override for request %s.\n\n%s\n\nThe candidate may proceed on that authority. The "+
		"adversarial-review obligation this task carries is NOT satisfied and remains on its record.\n",
		a.RequestID, a.Statement)
	body := b.String()
	if strings.Contains(body, reviewartifact.Marker) || strings.Contains(body, requestMarker) ||
		strings.Count(body, "[sensei-code:") != 1 {
		return "", errors.New("the publication would carry a protocol marker beyond its own envelope, so it is not posted")
	}
	return body, nil
}

func publishAttestation(ctx context.Context, box Issue, store AttestationStore, rec AttestationRecord, now func() time.Time) (AttestationRecord, error) {
	body, err := RenderAttestation(rec, box)
	if err != nil {
		return rec, fmt.Errorf("%w: %v", ErrRelayPublication, err)
	}
	comment, err := box.API.PostComment(ctx, box.Number, body)
	if err != nil {
		return rec, fmt.Errorf("%w: %v; resubmit the same attestation to retry publication", ErrRelayPublication, err)
	}
	published, err := store.markPublished(rec.Attestation.RequestID, rec.Attestation.ReviewDigest, comment,
		publicationIdentity(box), now())
	if err != nil {
		return rec, fmt.Errorf("%w: posted as comment %d, and the receipt could not record it: %v",
			ErrRelayPublication, comment, err)
	}
	return published, nil
}

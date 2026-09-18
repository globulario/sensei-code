package ghbridge

// HISTORICAL RELAY STATE, AND NOTHING ELSE.
//
// Until #182 R6 a relayed review was written to .sensei-code/relays as its own
// durable record: the artifact, its digest, a second copy of the decision,
// summary and findings, the binding, the standing, the relay principal and the
// App publication. That store is gone, and the reviews it holds are not.
//
// Deleting the runtime store must not strand them, so exactly one parser for
// the old shape survives -- HERE, behind a boundary whose name says what it is.
// It is reachable only from MigrateHistoricalRelays. Current relay ingestion
// calls reviewartifact.Parse and nothing in this file; a general fallback that
// current ingestion could reach would re-create the second grammar this slice
// removed, one call site at a time.
//
// The conversion is conservative in the one direction that matters:
//
//	published legacy relay   -> READY local-relay evidence
//	accepted legacy relay    -> PENDING local-relay evidence, published by nobody
//	anything unestablished   -> the file is RETAINED and reported
//
// A file is only ever moved aside after the equivalent state is durably
// represented in the review store. The migrator never publishes, never mints a
// request, never repairs a malformed record and never deletes the only
// surviving evidence of a state it could not establish.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Legacy relay states, as the pre-R6 store wrote them.
const (
	legacyRelayAccepted  = "accepted"
	legacyRelayPublished = "published"
)

// migratedDir is where a converted legacy file is kept, inside the old store so
// the archive travels with the evidence it came from.
const migratedDir = "migrated"

// historicalRelayRecord is the pre-R6 durable relay receipt, exactly as it was
// serialized. Read-only: nothing writes this shape again.
type historicalRelayRecord struct {
	Version int    `json:"version"`
	State   string `json:"state"`

	TaskID          string `json:"task_id"`
	RequestID       string `json:"request_id"`
	RequestComment  int64  `json:"request_comment,omitempty"`
	Conversation    string `json:"conversation"`
	BaseSHA         string `json:"base"`
	CandidateDigest string `json:"candidate_digest"`
	CandidateTree   string `json:"candidate_tree"`
	ReviewCommit    string `json:"review_commit"`

	Reviewer     string `json:"reviewer_provider"`
	ReviewDigest string `json:"review_digest"`
	Artifact     string `json:"artifact"`
	Standing     string `json:"standing"`

	RelayPrincipal RelayPrincipal `json:"relay_principal"`
	AcceptedAt     time.Time      `json:"accepted_at"`

	Publication        string    `json:"publication,omitempty"`
	PublicationComment int64     `json:"publication_comment,omitempty"`
	PublishedAt        time.Time `json:"published_at,omitempty"`
}

// Outcomes of migrating one historical file.
const (
	// MigratedReady is a published legacy relay now recorded as delivered.
	MigratedReady = "migrated_ready"
	// MigratedStaged is an accepted-but-unpublished legacy relay now recorded
	// with its delivery incomplete. Nothing was published to make it so.
	MigratedStaged = "migrated_staged"
	// RetainedConflict is a legacy relay whose bytes disagree with a review the
	// store already holds for that request. The file is left untouched.
	RetainedConflict = "retained_conflict"
	// RetainedUnreadable is a legacy relay this migrator could not establish.
	// The file is left untouched: it is the only evidence of whatever it holds.
	RetainedUnreadable = "retained_unreadable"
)

// HistoricalRelay is what happened to one legacy file.
type HistoricalRelay struct {
	File      string
	RequestID string
	Outcome   string
	Detail    string
}

// HistoricalRelayReport is what happened to the legacy store.
type HistoricalRelayReport struct {
	Dir     string
	Records []HistoricalRelay
}

// Migrated counts files whose state is now durably represented in the review
// store.
func (r HistoricalRelayReport) Migrated() int {
	n := 0
	for _, rec := range r.Records {
		if rec.Outcome == MigratedReady || rec.Outcome == MigratedStaged {
			n++
		}
	}
	return n
}

// Retained counts files this migrator could not convert and did not touch.
func (r HistoricalRelayReport) Retained() int { return len(r.Records) - r.Migrated() }

// MigrateHistoricalRelays converges a pre-R6 relay store into the one review
// record, and reports every file it could not.
//
// Idempotent: a converted file is moved into the archive, so a second run over
// the same directory finds nothing left to do. Running it against a store whose
// reviews are already present converges on the existing record rather than
// duplicating or replacing it -- evidence is additive, bytes are not.
func MigrateHistoricalRelays(dir string, reviews reviewstore.Store, now func() time.Time) (HistoricalRelayReport, error) {
	report := HistoricalRelayReport{Dir: dir}
	if strings.TrimSpace(dir) == "" || reviews.Dir == "" {
		return report, nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return report, nil
	}
	if err != nil {
		return report, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, name := range names {
		outcome := migrateOneHistoricalRelay(filepath.Join(dir, name), reviews, now)
		outcome.File = name
		if outcome.Outcome == MigratedReady || outcome.Outcome == MigratedStaged {
			if aerr := archiveHistoricalRelay(dir, name); aerr != nil {
				// The state IS represented; only the tidy-up failed. Say so
				// rather than claiming the file is gone.
				outcome.Detail += fmt.Sprintf("; the legacy file could not be archived: %v", aerr)
			}
		}
		report.Records = append(report.Records, outcome)
	}
	return report, nil
}

// migrateOneHistoricalRelay establishes what one legacy file holds, from its own
// stored artifact, and records the equivalent state.
func migrateOneHistoricalRelay(path string, reviews reviewstore.Store, now func() time.Time) HistoricalRelay {
	retained := func(format string, args ...any) HistoricalRelay {
		return HistoricalRelay{Outcome: RetainedUnreadable, Detail: fmt.Sprintf(format, args...)}
	}
	blob, err := os.ReadFile(path)
	if err != nil {
		return retained("%v", err)
	}
	var rec historicalRelayRecord
	if err := json.Unmarshal(blob, &rec); err != nil {
		return retained("it is not a readable relay receipt: %v", err)
	}
	out := HistoricalRelay{RequestID: rec.RequestID}

	// THE ARTIFACT IS THE AUTHORITY. Every summary field on the receipt is
	// cross-checked against the bytes it claims to describe, and a receipt that
	// disagrees with its own artifact is not repaired into one that agrees.
	art, err := reviewartifact.Parse(rec.Artifact)
	if err != nil {
		out.Outcome, out.Detail = RetainedUnreadable, fmt.Sprintf("its stored artifact is not a canonical review: %v", err)
		return out
	}
	if art.Digest != rec.ReviewDigest {
		out.Outcome = RetainedUnreadable
		out.Detail = fmt.Sprintf("its artifact digests to %s and the receipt names %s", art.Digest, rec.ReviewDigest)
		return out
	}
	if art.RequestID != rec.RequestID {
		out.Outcome = RetainedUnreadable
		out.Detail = fmt.Sprintf("its artifact answers request %s and the receipt names %s", art.RequestID, rec.RequestID)
		return out
	}
	if m := subjectMismatch(rec.subject(), subjectOf(art)); m != "" {
		out.Outcome, out.Detail = RetainedUnreadable, "the receipt and its artifact disagree: "+m
		return out
	}
	if !sameProvider(art.ReviewerProvider, rec.Reviewer) {
		out.Outcome = RetainedUnreadable
		out.Detail = fmt.Sprintf("its artifact names reviewer %q and the receipt names %q", art.ReviewerProvider, rec.Reviewer)
		return out
	}
	if rec.RelayPrincipal.PID <= 0 || rec.RelayPrincipal.Terminal == 0 {
		out.Outcome, out.Detail = RetainedUnreadable, "it names no terminal principal that carried the review"
		return out
	}
	binding := roles.Binding{TaskID: art.TaskID, BaseSHA: art.BaseSHA,
		CandidateDigest: art.CandidateDigest, CandidateTree: art.CandidateTree}
	if _, verr := workflow.ValidateReviewBody(art.Body, binding, art.ReviewerProvider); verr != nil {
		out.Outcome, out.Detail = RetainedUnreadable, fmt.Sprintf("its reviewer payload does not satisfy the reviewer contract: %v", verr)
		return out
	}

	ev := reviewstore.Evidence{
		Transport:      reviewstore.LocalRelay,
		RelayPrincipal: rec.RelayPrincipal.token(),
		ObservedAt:     rec.AcceptedAt,
	}
	switch rec.State {
	case legacyRelayPublished:
		// A completed delivery, and only when the receipt can prove it. A
		// published record missing its publication identity is not quietly
		// downgraded to pending either: it is retained, because neither state
		// is established.
		if strings.TrimSpace(rec.Publication) == "" || rec.PublicationComment <= 0 || rec.PublishedAt.IsZero() {
			out.Outcome, out.Detail = RetainedUnreadable, "it claims publication and does not say which publication"
			return out
		}
		ev.State = reviewstore.Ready
		ev.Publication = rec.Publication
		ev.PublicationComment = rec.PublicationComment
		ev.PublishedAt = rec.PublishedAt
		out.Outcome = MigratedReady
	case legacyRelayAccepted:
		// STAGED, and it stays staged. Seeing it at startup is not delivery,
		// and publishing it here would post a receipt nobody asked for, years
		// after the terminal that carried it went away.
		ev.State = reviewstore.Pending
		out.Outcome = MigratedStaged
	default:
		out.Outcome, out.Detail = RetainedUnreadable, fmt.Sprintf("it is in unknown state %q", rec.State)
		return out
	}
	if ev.ObservedAt.IsZero() {
		ev.ObservedAt = stamp(now)
	}

	if _, err := reviews.Accept(reviewstore.Acceptance{
		RequestID: rec.RequestID,
		Artifact:  rec.Artifact,
		Evidence:  ev,
		Validate: func(a reviewartifact.Artifact) error {
			_, verr := workflow.ValidateReviewBody(a.Body, binding, a.ReviewerProvider)
			return verr
		},
		Now: now,
	}); err != nil {
		if errors.Is(err, reviewstore.ErrConflict) {
			out.Outcome = RetainedConflict
			out.Detail = fmt.Sprintf("the review store already holds a different review for %s: %v", rec.RequestID, err)
			return out
		}
		out.Outcome, out.Detail = RetainedUnreadable, err.Error()
		return out
	}
	// A legacy PUBLISHED relay whose bytes are already staged here completes
	// that delivery rather than adding a second row beside it.
	if ev.State == reviewstore.Ready {
		if _, cerr := reviews.Complete(reviewstore.Completion{
			RequestID: rec.RequestID, ReviewDigest: art.Digest, Transport: reviewstore.LocalRelay,
			Publication: ev.Publication, PublicationComment: ev.PublicationComment,
			PublishedAt: ev.PublishedAt, Now: now,
		}); cerr != nil && !errors.Is(cerr, reviewstore.ErrUnreadable) {
			out.Outcome, out.Detail = RetainedUnreadable, cerr.Error()
			return out
		}
	}
	return out
}

// subject is the candidate identity the legacy receipt recorded beside its
// artifact. Cross-checked against the artifact, never trusted instead of it.
func (r historicalRelayRecord) subject() Subject {
	return Subject{TaskID: r.TaskID, BaseSHA: r.BaseSHA, CandidateDigest: r.CandidateDigest,
		CandidateTree: r.CandidateTree, ReviewCommit: r.ReviewCommit}
}

func stamp(now func() time.Time) time.Time {
	if now != nil {
		return now().UTC()
	}
	return time.Now().UTC()
}

// archiveHistoricalRelay moves a converted file aside, so the run is idempotent
// and the original bytes still exist.
func archiveHistoricalRelay(dir, name string) error {
	dest := filepath.Join(dir, migratedDir)
	if err := os.MkdirAll(dest, 0o700); err != nil {
		return err
	}
	return os.Rename(filepath.Join(dir, name), filepath.Join(dest, name))
}

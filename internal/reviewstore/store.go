// Package reviewstore is the one durable record of what a reviewer produced.
//
// A review used to mean different things depending on the pipe it came down.
// A relayed verdict got a durable receipt; the same bytes arriving directly on
// the authenticated mailbox got none, so "which review answered this request"
// had one answer for relays and another for everything else. Delivery was
// standing in for authority.
//
// So there is one record per review obligation, and every adapter converges on
// it. The canonical artifact's exact bytes and their digest ARE the semantic
// identity; everything a transport knows -- a GitHub login, a terminal
// principal, an App publication -- is kept beside them as evidence of HOW the
// bytes arrived, never as a statement of what they mean.
//
// Two rules make that stick:
//
//   - One request has at most one canonical artifact, ever. Redelivering the
//     same bytes converges; different bytes for an answered request are a
//     conflict and never replace the first.
//   - Identity is DERIVED by reparsing the stored bytes, not kept as a second
//     copy that can drift from them.
//
// A third rule arrived with #182 R6, when the relay's own durable receipt was
// deleted and the one thing it legitimately owned had to live somewhere:
//
//   - Transport evidence carries a DELIVERY STATE. A relay may stage the exact
//     canonical bytes before the App has published them, and those bytes are a
//     real review that grants no authority yet. Presence of a record is
//     therefore not permission to consume it; only READY evidence is.
//
// This package interprets no verdict. What a review SAYS is the workflow
// parser's to read, and Accept refuses to record anything that parser has not
// already passed -- which is why the validator is a required argument rather
// than an option.
package reviewstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/governedfile"
	"github.com/globulario/sensei-code/internal/reviewartifact"
)

// Transport names how canonical bytes reached this workspace.
//
// A closed vocabulary, read by membership: an unknown transport is refused
// rather than recorded, because evidence nobody can interpret later is
// indistinguishable from evidence nobody checked.
type Transport string

const (
	// GitHubMailbox is a review read directly off the authenticated mailbox.
	GitHubMailbox Transport = "github_mailbox"
	// LocalRelay is a review a terminal principal carried and the App published.
	LocalRelay Transport = "local_relay"
)

// Valid reads the closed set by membership.
func (t Transport) Valid() bool { return t == GitHubMailbox || t == LocalRelay }

// SchemaVersion is the shape of a record this package WRITES.
//
// A record on an unknown version is refused rather than read: a record parsed
// under the wrong schema is a fabricated specimen, and "version 0" is what an
// object nobody wrote through this package looks like.
//
// Version 2 added the evidence delivery state. Version 1 records remain
// readable because they are historical stored state and deleting the relay
// store must not make already-accepted reviews unreadable -- see legacyState.
const SchemaVersion = 2

// readableVersion reads the set of schemas this package can still interpret.
func readableVersion(v int) bool { return v == 1 || v == SchemaVersion }

// DeliveryState says whether the governed ingestion an evidence row describes
// has actually completed.
//
// The distinction exists because one transport is two-phase. A relayed review
// is validated and durably staged by this process, and only then published by
// the App; between those two moments the exact reviewer bytes are real and the
// delivery that would make them consumable has not happened. Before R6 that gap
// lived in a second durable store with its own record, its own verdict copy and
// its own lifecycle. It is one field here instead.
type DeliveryState string

const (
	// Pending is evidence whose governed ingestion has not completed. It grants
	// no authority, and it is not silence either: something reviewer-origin is
	// demonstrably here.
	Pending DeliveryState = "pending"
	// Ready is evidence whose governed ingestion completed. Only ready evidence
	// may permit consumption.
	Ready DeliveryState = "ready"
)

// Valid reads the closed set by membership. An unknown state is refused rather
// than treated as either end of it: a state nobody can interpret must not
// default to the permissive one.
func (d DeliveryState) Valid() bool { return d == Pending || d == Ready }

// Advisory is the only standing this store records.
//
// No transport establishes reviewer independence: authenticating a GitHub
// account proves who posted, and observing a terminal principal proves who
// relayed. Neither is anyone observing the reviewer's isolation.
const Advisory = "advisory"

// Evidence is one observation of how the canonical bytes arrived.
//
// Deliberately NOT one generic "author" field. A single field would mean the
// reviewer on one path and the relaying operator on another, and the moment two
// transports share a name for two different parties, the party becomes whatever
// the last writer meant.
type Evidence struct {
	Transport Transport `json:"transport"`
	// State says whether this transport's governed ingestion completed. Empty
	// only in a stored v1 record, where it is read as Ready -- see legacyState.
	State      DeliveryState `json:"state,omitempty"`
	ObservedAt time.Time     `json:"observed_at"`

	// Mailbox transport facts: who GitHub says posted, and where.
	GitHubAuthor   string `json:"github_author,omitempty"`
	GitHubAuthorID int64  `json:"github_author_id,omitempty"`
	GitHubComment  int64  `json:"github_comment,omitempty"`

	// Relay transport facts: who carried it, and the App's separate publication.
	RelayPrincipal     string    `json:"relay_principal,omitempty"`
	Publication        string    `json:"publication,omitempty"`
	PublicationComment int64     `json:"publication_comment,omitempty"`
	PublishedAt        time.Time `json:"published_at,omitempty"`
}

// observation names WHICH observation this is, independently of when this
// process happened to see it again.
//
// The identity of "the reviewer's comment 5150 from account 1697116" does not
// change because a later poll read it a second time. Folding the re-observation
// TIME into that identity made every poll a new observation, which is how an
// idempotent redelivery quietly became an append.
type observation struct {
	transport Transport
	// Mailbox: the durable comment locator and the principal GitHub authenticated.
	githubAuthor   string
	githubAuthorID int64
	githubComment  int64
	// Relay: who carried these bytes from a controlling terminal.
	relayPrincipal string
}

// observation deliberately excludes the delivery state and the App publication.
//
// Completing a staged relay does not make it a SECOND observation of the same
// bytes: the same terminal principal carried them once, and the publication is
// that one observation finishing. Folding either into identity would make a
// promoted row unrecognisable as the row it was promoted from, so a later retry
// would append a duplicate delivery beside the one it just completed.
func (e Evidence) observation() observation {
	return observation{
		transport:    e.Transport,
		githubAuthor: e.GitHubAuthor, githubAuthorID: e.GitHubAuthorID, githubComment: e.GitHubComment,
		relayPrincipal: e.RelayPrincipal,
	}
}

// Delivered reports whether this row establishes a completed governed
// ingestion.
func (e Evidence) Delivered() bool { return e.State == Ready }

// Validate states the OBSERVED residue each transport must carry.
//
// This is the whole trusted surface of the record: the part that cannot be
// re-derived later, saying which governed ingestion established acceptance. A
// row that names no locator claims an acceptance nobody can point at, and a
// record whose only evidence is that kind of row states that transport
// established acceptance while declining to say how.
//
// Structural only. Nothing here re-contacts GitHub, and none of it is proof
// against a writer who already holds the workspace (see #184).
func (e Evidence) Validate() error {
	if !e.Transport.Valid() {
		return fmt.Errorf("transport %q is not one this store records", e.Transport)
	}
	if !e.State.Valid() {
		return fmt.Errorf("%s evidence does not say whether its delivery completed (state %q)", e.Transport, e.State)
	}
	if e.ObservedAt.IsZero() {
		return fmt.Errorf("%s evidence does not say when it was observed", e.Transport)
	}
	switch e.Transport {
	case GitHubMailbox:
		// One-phase by construction: the bytes were READ from a comment that
		// already existed. There is no moment at which a mailbox observation is
		// staged and undelivered, so a pending one describes nothing real.
		if e.State != Ready {
			return errors.New("mailbox evidence reads bytes that are already published, so it is never pending")
		}
		if e.GitHubComment <= 0 {
			return errors.New("mailbox evidence must name the comment the bytes were read from")
		}
		if e.GitHubAuthorID == 0 && strings.TrimSpace(e.GitHubAuthor) == "" {
			return errors.New("mailbox evidence must name the principal GitHub authenticated")
		}
	case LocalRelay:
		// Two-phase. Who carried the bytes is known at staging time; the App
		// publication is not, and a pending row must not claim one.
		if strings.TrimSpace(e.RelayPrincipal) == "" {
			return errors.New("relay evidence must name the terminal principal that carried it")
		}
		if e.State == Pending {
			if strings.TrimSpace(e.Publication) != "" || e.PublicationComment > 0 || !e.PublishedAt.IsZero() {
				return errors.New("pending relay evidence names a publication it has not completed")
			}
			return nil
		}
		if e.PublicationComment <= 0 || strings.TrimSpace(e.Publication) == "" {
			return errors.New("relay evidence must name the App publication that carries it on the mailbox")
		}
		if e.PublishedAt.IsZero() {
			return errors.New("relay evidence must say when that publication completed")
		}
	}
	return nil
}

// Record is one review obligation's durable semantic record.
//
// It holds the reviewer's exact bytes and the digest naming them, and no second
// copy of what those bytes say. There is deliberately no decision, summary,
// findings or reviewer-provider field: every one of those is recoverable by
// reparsing ArtifactRaw, and a stored copy is a value that can disagree with
// the artifact it claims to describe.
type Record struct {
	Version   int    `json:"version"`
	RequestID string `json:"request_id"`

	// The reviewer's bytes, and the digest that names exactly those bytes.
	ArtifactRaw  string `json:"artifact_raw"`
	ReviewDigest string `json:"review_digest"`

	Standing   string    `json:"standing"`
	AcceptedAt time.Time `json:"accepted_at"`

	// How the bytes arrived, once per distinct observation. Additive: a second
	// transport seeing the same artifact adds a row and changes nothing else.
	Evidence []Evidence `json:"transport_evidence"`
}

// Artifact reparses the stored bytes.
//
// Every identity question -- which candidate, which request, which reviewer
// provider -- is answered from HERE, so the answer is always the artifact's own
// and never a field somebody set beside it.
func (r Record) Artifact() (reviewartifact.Artifact, error) {
	return reviewartifact.Parse(r.ArtifactRaw)
}

// Consumable reports whether any governed ingestion of these bytes actually
// COMPLETED.
//
// THE ONE PLACE THIS QUESTION IS ANSWERED. A record exists from the moment a
// relay stages exact reviewer bytes, which is before the App has published
// them; reading "the record is here" as "the review may be used" is the whole
// defect this predicate exists to prevent, and a caller reimplementing it would
// be free to read it the permissive way.
func (r Record) Consumable() bool {
	for _, ev := range r.Evidence {
		if ev.Delivered() {
			return true
		}
	}
	return false
}

// Staged lists evidence whose delivery has not completed.
//
// Diagnostic only. It is what makes a non-consumable record explainable --
// which transport is mid-flight and who carried it -- instead of merely absent.
func (r Record) Staged() []Evidence {
	var out []Evidence
	for _, ev := range r.Evidence {
		if !ev.Delivered() {
			out = append(out, ev)
		}
	}
	return out
}

// legacyState fills the delivery state of a stored v1 record.
//
// A v1 row is READ as ready, and that inference is safe because v1 had no
// representable pending row: its own validation required a completed App
// publication for relay evidence and a comment locator for mailbox evidence,
// so every row that was legal to store had already been delivered. The
// inference is confined to the schema field -- nothing here invents review
// identity, a provider or a binding from anything.
//
// In memory only. Reading a record never rewrites it.
func (r *Record) legacyState() {
	if r.Version != 1 {
		return
	}
	for i := range r.Evidence {
		if r.Evidence[i].State == "" {
			r.Evidence[i].State = Ready
		}
	}
}

var (
	// ErrConflict reports a DIFFERENT artifact offered for a request that
	// already has one. The stored record is untouched.
	ErrConflict = errors.New("this request already has a different canonical review")
	// ErrUnreadable reports a stored record that could not be trusted. It is
	// never repaired by overwriting: the bytes we cannot read are the only
	// evidence of what was accepted.
	ErrUnreadable = errors.New("the stored review record could not be read as the review it claims to be")
)

// requestFileSafe bounds a request id to something that names one file in this
// directory and nothing anywhere else.
var requestFileSafe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Store holds one canonical review per request id.
type Store struct {
	Dir string
}

func (s Store) path(requestID string) (string, error) {
	if strings.TrimSpace(s.Dir) == "" {
		return "", errors.New("the review store has no directory")
	}
	if !requestFileSafe.MatchString(requestID) {
		return "", fmt.Errorf("request id %q is not a safe file name", requestID)
	}
	return filepath.Join(s.Dir, requestID+".json"), nil
}

// writers serializes read-modify-write of one record's evidence within this
// process.
//
// Creation is already atomic across processes (O_EXCL), and creation is what
// decides SEMANTIC identity. This lock covers the weaker case: appending an
// evidence row. Two processes racing there can still lose a row -- which loses a
// transport observation and can never change what the review means, because the
// artifact bytes are only ever written once.
var writers struct {
	sync.Mutex
	held map[string]*sync.Mutex
}

func lockFor(path string) *sync.Mutex {
	writers.Lock()
	defer writers.Unlock()
	if writers.held == nil {
		writers.held = map[string]*sync.Mutex{}
	}
	m, ok := writers.held[path]
	if !ok {
		m = &sync.Mutex{}
		writers.held[path] = m
	}
	return m
}

// Load reads the record for a request and proves it is what it claims to be.
//
// The stored bytes are reparsed, the digest is recomputed over them, and the
// artifact must answer this exact request. A record failing any of those is
// reported as unreadable rather than returned partly trusted: a half-verified
// review is the kind of evidence that qualifies the wrong candidate.
func (s Store) Load(requestID string) (Record, bool, error) {
	path, err := s.path(requestID)
	if err != nil {
		return Record{}, false, err
	}
	blob, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	var rec Record
	if err := json.Unmarshal(blob, &rec); err != nil {
		return Record{}, false, fmt.Errorf("%w: %s is not valid json: %v", ErrUnreadable, requestID, err)
	}
	rec.legacyState()
	if err := rec.verify(requestID); err != nil {
		return Record{}, false, err
	}
	return rec, true, nil
}

// verify states what a stored record must prove about itself before anyone
// reads a review out of it.
func (r Record) verify(requestID string) error {
	art, err := r.Artifact()
	if err != nil {
		return fmt.Errorf("%w: %s no longer parses as a canonical review: %v", ErrUnreadable, requestID, err)
	}
	if want := reviewartifact.Digest(r.ArtifactRaw); r.ReviewDigest != want {
		return fmt.Errorf("%w: %s stores bytes digesting to %s under the name %s",
			ErrUnreadable, requestID, want, r.ReviewDigest)
	}
	if r.RequestID != requestID || art.RequestID != requestID {
		return fmt.Errorf("%w: %s holds a review of request %s/%s",
			ErrUnreadable, requestID, r.RequestID, art.RequestID)
	}
	if r.Standing != Advisory {
		return fmt.Errorf("%w: %s records standing %q and this store records %q only",
			ErrUnreadable, requestID, r.Standing, Advisory)
	}
	if !readableVersion(r.Version) {
		return fmt.Errorf("%w: %s declares schema version %d and this package reads 1 and %d",
			ErrUnreadable, requestID, r.Version, SchemaVersion)
	}
	if r.AcceptedAt.IsZero() {
		return fmt.Errorf("%w: %s does not say when it was accepted", ErrUnreadable, requestID)
	}
	// The OBSERVED residue. Accepting the workspace trust boundary means a
	// structurally complete local record is read as written; it does not mean a
	// record may claim that transport established acceptance and then decline to
	// say which ingestion did it.
	if len(r.Evidence) == 0 {
		return fmt.Errorf("%w: %s records no transport observation, so nothing says how it was accepted",
			ErrUnreadable, requestID)
	}
	for i, ev := range r.Evidence {
		if err := ev.Validate(); err != nil {
			return fmt.Errorf("%w: %s transport evidence %d is incomplete: %v", ErrUnreadable, requestID, i, err)
		}
	}
	return nil
}

// Acceptance is one adapter offering canonical bytes for one obligation.
type Acceptance struct {
	// RequestID is the obligation being answered. The artifact must name it too.
	RequestID string
	// Artifact is the reviewer's EXACT bytes. Never re-rendered before storing.
	Artifact string
	// Evidence is what this transport observed about the delivery.
	Evidence Evidence
	// Validate is the reviewer-body check that must already have passed. It is
	// required, not optional: this store never becomes a second place where a
	// payload that no parser accepted turns into a durable review.
	Validate func(reviewartifact.Artifact) error
	// Now supplies the acceptance time. Nil uses the wall clock.
	Now func() time.Time
}

// Accept records canonical bytes against one obligation, or refuses.
//
// Exact-byte semantics throughout:
//
//   - nothing stored yet -> created atomically;
//   - same digest -> the semantic record is untouched and this observation is
//     added if it is new; re-offering an observation already recorded is a no-op
//     and the acceptance time does not move;
//   - different digest -> ErrConflict, and the stored artifact is left byte for
//     byte as it was.
//
// A semantically similar ACCEPT that differs by one byte is a DIFFERENT
// artifact and therefore conflicts. Nothing here normalizes bytes to make two
// digests agree, merges bodies, or lets a later comment win.
func (s Store) Accept(in Acceptance) (Record, error) {
	if in.Validate == nil {
		return Record{}, errors.New("a review is recorded only after the reviewer-body parser has passed it")
	}
	path, err := s.path(in.RequestID)
	if err != nil {
		return Record{}, err
	}
	art, err := reviewartifact.Parse(in.Artifact)
	if err != nil {
		return Record{}, fmt.Errorf("the offered review is not a canonical artifact: %w", err)
	}
	if art.RequestID != in.RequestID {
		return Record{}, fmt.Errorf("the artifact answers request %s and it is offered for %s", art.RequestID, in.RequestID)
	}
	if err := in.Validate(art); err != nil {
		return Record{}, fmt.Errorf("the reviewer payload does not satisfy the reviewer contract: %w", err)
	}

	now := time.Now
	if in.Now != nil {
		now = in.Now
	}
	// The adapters do not carry a clock, so a zero time means "now". The stamp
	// is applied BEFORE validation and never participates in observation
	// identity: it records when this process saw the bytes, which is not part of
	// which observation this is.
	ev := in.Evidence
	if ev.ObservedAt.IsZero() {
		ev.ObservedAt = now().UTC()
	}
	ev.ObservedAt = ev.ObservedAt.UTC()
	if err := ev.Validate(); err != nil {
		return Record{}, err
	}

	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()

	rec := Record{
		Version:      SchemaVersion,
		RequestID:    in.RequestID,
		ArtifactRaw:  in.Artifact,
		ReviewDigest: art.Digest,
		Standing:     Advisory,
		AcceptedAt:   now().UTC(),
		Evidence:     []Evidence{ev},
	}
	created, err := s.create(path, rec)
	if err != nil {
		return Record{}, err
	}
	if created {
		return rec, nil
	}

	// Somebody got here first. Read what they wrote and compare EXACT bytes;
	// never truncate, replace or repair.
	existing, found, err := s.Load(in.RequestID)
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, fmt.Errorf("%w: %s exists and holds nothing readable", ErrUnreadable, in.RequestID)
	}
	if existing.ReviewDigest != art.Digest {
		return Record{}, fmt.Errorf("%w: %s is answered by %s and this artifact is %s",
			ErrConflict, in.RequestID, existing.ReviewDigest, art.Digest)
	}
	if existing.hasEvidence(ev) {
		return existing, nil
	}
	// A relay delivery that is already here is CONTINUED, not staged again.
	// The digest already matched, so these are the same bytes: a second
	// terminal submitting them is retrying one delivery, and appending would
	// both duplicate it and leave completion two rows to choose between. The
	// principal that first staged it stays the principal that carried it.
	if ev.Transport == LocalRelay && ev.State == Pending && existing.carries(LocalRelay) {
		return existing, nil
	}
	next := existing
	next.Evidence = append(append([]Evidence{}, existing.Evidence...), ev)
	return s.rewrite(path, existing, next)
}

// Completion is a two-phase transport reporting that its staged delivery
// finished.
type Completion struct {
	// RequestID is the record being completed.
	RequestID string
	// ReviewDigest names the EXACT bytes whose delivery completed. Required and
	// never inferred: a completion that took the store's word for which review
	// it was publishing could promote bytes its caller never published.
	ReviewDigest string
	// Transport is the staged transport being completed.
	Transport Transport
	// The App publication that now carries those bytes on the mailbox.
	Publication        string
	PublicationComment int64
	PublishedAt        time.Time
	// Now supplies the observation stamp when PublishedAt is zero.
	Now func() time.Time
}

// Complete promotes ONE staged evidence row to ready, in place.
//
// This is the only way evidence becomes consumable after the fact, and it
// changes nothing else: the artifact bytes, the digest, the acceptance time and
// every other observation are carried through unchanged (see preserves).
//
// The digest is checked against the stored record before anything is promoted.
// A completion naming other bytes is a conflict rather than a rename: the
// publication it is reporting is then a publication of something this record
// does not hold, and promoting on request id alone would let it make a review
// consumable that nobody delivered.
func (s Store) Complete(in Completion) (Record, error) {
	path, err := s.path(in.RequestID)
	if err != nil {
		return Record{}, err
	}
	if !in.Transport.Valid() {
		return Record{}, fmt.Errorf("transport %q is not one this store records", in.Transport)
	}
	if strings.TrimSpace(in.ReviewDigest) == "" {
		return Record{}, errors.New("a completion must name the exact review whose delivery completed")
	}

	mu := lockFor(path)
	mu.Lock()
	defer mu.Unlock()

	existing, found, err := s.Load(in.RequestID)
	if err != nil {
		return Record{}, err
	}
	if !found {
		return Record{}, fmt.Errorf("%w: %s has no staged review to complete", ErrUnreadable, in.RequestID)
	}
	if existing.ReviewDigest != in.ReviewDigest {
		return Record{}, fmt.Errorf("%w: %s holds %s and this completion names %s",
			ErrConflict, in.RequestID, existing.ReviewDigest, in.ReviewDigest)
	}

	at := in.PublishedAt
	if at.IsZero() {
		now := time.Now
		if in.Now != nil {
			now = in.Now
		}
		at = now()
	}
	next := existing
	next.Evidence = append([]Evidence{}, existing.Evidence...)
	// The FIRST row of that transport: the delivery that was staged. Scanning
	// for "a row that happens to fit" would let a later duplicate be completed
	// while the original stayed pending forever.
	for i, ev := range next.Evidence {
		if ev.Transport != in.Transport {
			continue
		}
		if ev.Delivered() {
			if ev.Publication == in.Publication && ev.PublicationComment == in.PublicationComment {
				return existing, nil
			}
			return Record{}, fmt.Errorf("%w: %s already carries %s publication %s comment %d",
				ErrConflict, in.RequestID, in.Transport, ev.Publication, ev.PublicationComment)
		}
		ev.State = Ready
		ev.Publication = in.Publication
		ev.PublicationComment = in.PublicationComment
		ev.PublishedAt = at.UTC()
		if verr := ev.Validate(); verr != nil {
			return Record{}, verr
		}
		next.Evidence[i] = ev
		return s.rewrite(path, existing, next)
	}
	return Record{}, fmt.Errorf("%w: %s records no staged %s delivery to complete",
		ErrUnreadable, in.RequestID, in.Transport)
}

// carries reports whether any evidence row came down this transport, whatever
// its delivery state.
func (r Record) carries(t Transport) bool {
	for _, ev := range r.Evidence {
		if ev.Transport == t {
			return true
		}
	}
	return false
}

// rewrite persists a record whose SEMANTIC identity is unchanged, writing it at
// the current schema.
//
// This is also the v1 -> v2 upgrade path, and the only one: a v1 record is
// rewritten because it received additive evidence, never because it was read.
func (s Store) rewrite(path string, from, to Record) (Record, error) {
	to.Version = SchemaVersion
	if err := to.preserves(from); err != nil {
		return Record{}, err
	}
	if err := s.replace(path, to); err != nil {
		return Record{}, err
	}
	return to, nil
}

// preserves states what a rewrite may NOT change.
//
// Everything that makes the record the review it is: the exact bytes, the
// digest naming them, which request they answer, when they were accepted, and
// every observation already recorded. Only the evidence list may grow, and a
// row already there may only be completed in place.
func (r Record) preserves(prev Record) error {
	if r.ArtifactRaw != prev.ArtifactRaw {
		return fmt.Errorf("%w: %s would rewrite the accepted artifact bytes", ErrUnreadable, prev.RequestID)
	}
	if r.ReviewDigest != prev.ReviewDigest {
		return fmt.Errorf("%w: %s holds %s and the rewrite names %s",
			ErrConflict, prev.RequestID, prev.ReviewDigest, r.ReviewDigest)
	}
	if r.RequestID != prev.RequestID {
		return fmt.Errorf("%w: %s would be rewritten as a record of %s", ErrUnreadable, prev.RequestID, r.RequestID)
	}
	if !r.AcceptedAt.Equal(prev.AcceptedAt) {
		return fmt.Errorf("%w: %s would move its acceptance time", ErrUnreadable, prev.RequestID)
	}
	for _, have := range prev.Evidence {
		if !r.hasEvidence(have) {
			return fmt.Errorf("%w: %s would drop a recorded %s observation",
				ErrUnreadable, prev.RequestID, have.Transport)
		}
	}
	return nil
}

// hasEvidence reports whether this observation is already recorded, so a
// repeated poll of the same comment, or a retried relay convergence, adds
// nothing.
//
// Compared by OBSERVATION IDENTITY, never by the whole struct: the stamp saying
// when this process last looked is not part of which observation it looked at,
// and comparing it made every redelivery a new row.
func (r Record) hasEvidence(ev Evidence) bool {
	want := ev.observation()
	for _, have := range r.Evidence {
		if have.observation() == want {
			return true
		}
	}
	return false
}

// create writes a record that must not already exist. It reports whether it
// won the creation, so the caller can converge rather than overwrite.
func (s Store) create(path string, rec Record) (bool, error) {
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return false, err
	}
	return governedfile.Create(path, append(blob, '\n'))
}

// replace rewrites a record whose ARTIFACT BYTES are unchanged.
//
// Only the evidence list ever grows this way. It verifies before writing, so a
// bug that mutated the artifact in memory cannot be persisted over the bytes
// that were accepted.
func (s Store) replace(path string, rec Record) error {
	if err := rec.verify(rec.RequestID); err != nil {
		return err
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return err
	}
	return governedfile.Replace(path, append(blob, '\n'))
}

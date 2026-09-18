package reviewstore

// STORED V1 RECORDS ARE HISTORY, AND HISTORY MUST STAY READABLE.
//
// #182 R6 added the evidence delivery state, which every v2 row must carry. The
// records this workspace already holds do not have one, and they were written by
// a package whose validation required a completed publication for relay evidence
// and a comment locator for mailbox evidence. There was no representable v1
// "pending" row, so reading a v1 row as READY states what was already true
// rather than assuming the permissive case.
//
// The fixtures below are SERIALIZED JSON, not structs converted in memory. A
// struct built with today's field set would carry the very field whose absence
// is the thing under test.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
)

const v1AcceptedAt = "2026-09-14T11:02:03Z"

// writeV1 writes a schema-1 record exactly as the pre-R6 package serialized one:
// no state field anywhere.
func writeV1(t *testing.T, s Store, raw string, evidence string) {
	t.Helper()
	blob := `{
  "version": 1,
  "request_id": "` + request + `",
  "artifact_raw": ` + mustJSON(t, raw) + `,
  "review_digest": "` + reviewartifact.Digest(raw) + `",
  "standing": "advisory",
  "accepted_at": "` + v1AcceptedAt + `",
  "transport_evidence": [` + evidence + `]
}`
	if strings.Contains(blob, `"state"`) {
		t.Fatal("the v1 fixture carries a delivery state, so it is not a v1 record")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, request+".json"), []byte(blob), 0o600); err != nil {
		t.Fatal(err)
	}
}

func mustJSON(t *testing.T, v string) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const v1MailboxEvidence = `{
    "transport": "github_mailbox",
    "observed_at": "2026-09-14T11:02:03Z",
    "github_author": "davecourtois",
    "github_author_id": 1697116,
    "github_comment": 5150
  }`

const v1RelayEvidence = `{
    "transport": "local_relay",
    "observed_at": "2026-09-14T11:02:03Z",
    "relay_principal": "uid:1000,user:dave,pid:4242,terminal:34816",
    "publication": "github-app:4850747:installation:159521273:globulario/sensei-code",
    "publication_comment": 5151,
    "published_at": "2026-09-14T11:02:01Z"
  }`

// A v1 record of either transport is readable, and its evidence is READY.
func TestAV1RecordLoadsWithItsDeliveryComplete(t *testing.T) {
	raw := artifact(t, request, "chatgpt", accept)
	for name, evidence := range map[string]string{
		"a mailbox comment": v1MailboxEvidence,
		"a published relay": v1RelayEvidence,
		"both, one of each": v1MailboxEvidence + ",\n  " + v1RelayEvidence,
	} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			writeV1(t, s, raw, evidence)

			rec, found, err := s.Load(request)
			if err != nil || !found {
				t.Fatalf("a v1 record became unreadable: found=%v err=%v", found, err)
			}
			if rec.Version != 1 {
				t.Fatalf("reading a v1 record changed its version to %d", rec.Version)
			}
			for i, ev := range rec.Evidence {
				if ev.State != Ready {
					t.Fatalf("v1 evidence %d (%s) reads as %q, want ready", i, ev.Transport, ev.State)
				}
			}
			if !rec.Consumable() {
				t.Fatal("a v1 record that was consumable before R6 is not consumable now")
			}
			if len(rec.Staged()) != 0 {
				t.Fatalf("a v1 record reports %d staged deliveries; v1 could not represent one", len(rec.Staged()))
			}
		})
	}
}

// Reading a v1 record does not rewrite it.
func TestReadingAV1RecordLeavesItOnDiskUnchanged(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	writeV1(t, s, raw, v1MailboxEvidence)
	path := filepath.Join(s.Dir, request+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, _, err := s.Load(request); err != nil {
			t.Fatalf("load %d: %v", i, err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatalf("reading a v1 record rewrote it:\n--- before\n%s\n--- after\n%s", before, after)
	}
}

// A v1 record that receives additive evidence is upgraded to v2, and the upgrade
// changes nothing about the review.
func TestAV1RecordUpgradesOnlyWhenItReceivesEvidence(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	writeV1(t, s, raw, v1MailboxEvidence)
	before, _, err := s.Load(request)
	if err != nil {
		t.Fatal(err)
	}

	// The same bytes arriving on the OTHER transport: one more observation, and
	// nothing else may move.
	after, err := s.Accept(Acceptance{
		RequestID: request, Artifact: raw, Evidence: relayEvidence(), Validate: passes,
	})
	if err != nil {
		t.Fatalf("adding evidence to a v1 record: %v", err)
	}
	if after.Version != SchemaVersion {
		t.Fatalf("the upgraded record is version %d, want %d", after.Version, SchemaVersion)
	}
	if after.ArtifactRaw != before.ArtifactRaw {
		t.Fatal("the upgrade rewrote the accepted artifact bytes")
	}
	if after.ReviewDigest != before.ReviewDigest {
		t.Fatalf("the upgrade renamed the review: %s -> %s", before.ReviewDigest, after.ReviewDigest)
	}
	if !after.AcceptedAt.Equal(before.AcceptedAt) {
		t.Fatalf("the upgrade moved the acceptance time: %s -> %s", before.AcceptedAt, after.AcceptedAt)
	}
	want, _ := time.Parse(time.RFC3339, v1AcceptedAt)
	if !after.AcceptedAt.Equal(want) {
		t.Fatalf("the acceptance time is %s, want the v1 record's own %s", after.AcceptedAt, want)
	}
	// The original observation survives, still ready.
	if len(after.Evidence) != 2 {
		t.Fatalf("the upgraded record holds %d evidence rows, want 2", len(after.Evidence))
	}
	if after.Evidence[0].Transport != GitHubMailbox || after.Evidence[0].State != Ready {
		t.Fatalf("the v1 observation did not survive the upgrade: %+v", after.Evidence[0])
	}
	// And what is on disk is now a v2 record that says so.
	blob, err := os.ReadFile(filepath.Join(s.Dir, request+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), `"state": "ready"`) {
		t.Fatalf("the upgraded record does not state its delivery:\n%s", blob)
	}
	reread, _, err := s.Load(request)
	if err != nil {
		t.Fatalf("the upgraded record is unreadable: %v", err)
	}
	if reread.Version != SchemaVersion || !reread.Consumable() {
		t.Fatalf("the upgraded record does not read back: version=%d consumable=%v", reread.Version, reread.Consumable())
	}
}

// A schema nobody wrote is refused. Version 0 is what an object written outside
// this package looks like, and a version from a later package is a record this
// one cannot claim to understand.
func TestAnUnknownSchemaVersionIsStillRefused(t *testing.T) {
	raw := artifact(t, request, "chatgpt", accept)
	for name, version := range map[string]int{
		"a record nobody wrote through this package": 0,
		"a record from a later package":              SchemaVersion + 1,
	} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			writeV1(t, s, raw, v1MailboxEvidence)
			path := filepath.Join(s.Dir, request+".json")
			blob, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var rec map[string]any
			if err := json.Unmarshal(blob, &rec); err != nil {
				t.Fatal(err)
			}
			rec["version"] = version
			out, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, found, err := s.Load(request); found || err == nil {
				t.Fatalf("schema version %d was read: found=%v err=%v", version, found, err)
			}
		})
	}
}

// A completion names the EXACT review whose delivery finished.
//
// Promoting on request id alone would let a transport that published one thing
// make a different thing consumable -- the record holds the bytes, the caller
// holds the publication, and only the digest ties the two together.
func TestACompletionMustNameTheReviewItPublished(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	other := artifact(t, request, "chatgpt",
		`{"decision":"accept","summary":"a different review entirely","instructions":"","findings":[]}`)
	staged := relayEvidence()
	staged.State, staged.Publication, staged.PublicationComment, staged.PublishedAt = Pending, "", 0, time.Time{}
	if _, err := s.Accept(Acceptance{RequestID: request, Artifact: raw, Evidence: staged, Validate: passes}); err != nil {
		t.Fatal(err)
	}

	_, err := s.Complete(Completion{
		RequestID: request, ReviewDigest: reviewartifact.Digest(other), Transport: LocalRelay,
		Publication: "github-app", PublicationComment: 5151, PublishedAt: time.Unix(1700000200, 0).UTC(),
	})
	if err == nil {
		t.Fatal("a completion naming other bytes promoted this record")
	}
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want a conflict", err)
	}
	rec, _, lerr := s.Load(request)
	if lerr != nil {
		t.Fatal(lerr)
	}
	if rec.Consumable() {
		t.Fatal("a completion naming other bytes made this review consumable")
	}
	if rec.ArtifactRaw != raw {
		t.Fatal("the refused completion changed the stored bytes")
	}
}

// A mailbox observation is never staged.
//
// It reads bytes that a comment already carries, so there is no delivery still
// to complete. A pending mailbox row would describe nothing real -- and, being
// pending, would make a review that IS published look undelivered.
func TestMailboxEvidenceIsNeverStaged(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	ev := mailboxEvidence()
	ev.State = Pending

	_, err := s.Accept(Acceptance{RequestID: request, Artifact: raw, Evidence: ev, Validate: passes})
	if err == nil {
		t.Fatal("a staged mailbox observation was recorded")
	}
	if !strings.Contains(err.Error(), "never pending") {
		t.Fatalf("err = %v, want the refusal to say a mailbox read is never pending", err)
	}
	if _, found, _ := s.Load(request); found {
		t.Fatal("a refused observation created a record")
	}
}

// Consumable reads DELIVERED evidence and nothing else.
//
// The isolating assertion for the one predicate every consumer asks. A record
// exists from the moment a relay stages exact bytes; if this returned true for
// any evidence row, every staged review would become an answer at once, and the
// runner, the attestation path and the store would all be wrong together.
func TestConsumableReadsOnlyDeliveredEvidence(t *testing.T) {
	raw := artifact(t, request, "chatgpt", accept)
	staged := relayEvidence()
	staged.State, staged.Publication, staged.PublicationComment, staged.PublishedAt = Pending, "", 0, time.Time{}

	s := store(t)
	rec, err := s.Accept(Acceptance{RequestID: request, Artifact: raw, Evidence: staged, Validate: passes})
	if err != nil {
		t.Fatal(err)
	}
	// The precondition: there IS evidence here, so a false answer below cannot
	// be explained by an empty record.
	if len(rec.Evidence) != 1 {
		t.Fatalf("the fixture holds %d evidence rows, want the staged one", len(rec.Evidence))
	}
	if rec.Consumable() {
		t.Fatal("a review whose only evidence is undelivered reads as consumable")
	}
	if len(rec.Staged()) != 1 {
		t.Fatalf("the record reports %d staged deliveries, want 1", len(rec.Staged()))
	}

	// Completing that one row is what makes it consumable, and nothing else.
	done, err := s.Complete(Completion{
		RequestID: request, ReviewDigest: rec.ReviewDigest, Transport: LocalRelay,
		Publication: "github-app", PublicationComment: 5151, PublishedAt: time.Unix(1700000200, 0).UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !done.Consumable() {
		t.Fatal("a completed delivery did not make the review consumable")
	}
	if len(done.Evidence) != 1 {
		t.Fatalf("completing a delivery added a row: %+v", done.Evidence)
	}
	if len(done.Staged()) != 0 {
		t.Fatalf("the completed record still reports %d staged deliveries", len(done.Staged()))
	}
}

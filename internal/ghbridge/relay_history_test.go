package ghbridge

// HISTORICAL RELAY RECEIPTS, CONVERGED.
//
// The fixtures here are SERIALIZED pre-R6 relay receipts, written as the deleted
// store wrote them. Building them from a struct would mean building them with
// today's field set, and the whole question is whether yesterday's bytes still
// convert -- a conversion test whose input is produced by the converter proves
// only that the converter is self-consistent.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
	"github.com/globulario/sensei-code/internal/reviewstore"
)

const legacyAcceptedAt = "2026-09-14T11:02:03Z"

// legacyReceipt serializes one pre-R6 relay receipt exactly as the deleted store
// did: a second copy of the verdict beside the artifact, and a two-state
// lifecycle of its own.
func legacyReceipt(t *testing.T, state, raw string, mutate func(map[string]any)) []byte {
	t.Helper()
	rec := map[string]any{
		"version": 1, "state": state,
		"task_id": relaySubject.TaskID, "request_id": relayRequest,
		"request_comment": 5686428018, "conversation": "157",
		"base": relaySubject.BaseSHA, "candidate_digest": relaySubject.CandidateDigest,
		"candidate_tree": relaySubject.CandidateTree, "review_commit": relaySubject.ReviewCommit,
		"reviewer_provider": "chatgpt", "review_digest": reviewartifact.Digest(raw),
		// The second semantic copy R6 deletes. The migrator must ignore all
		// three and re-read the verdict from the artifact.
		"decision": "accept", "summary": "a summary nobody may trust", "findings": []any{},
		"artifact": raw, "standing": "advisory",
		"relay_principal": map[string]any{"uid": 1000, "user": "dave", "pid": 4242, "terminal": 34816},
		"accepted_at":     legacyAcceptedAt,
	}
	if state == "published" {
		rec["publication"] = "github-app:4850747:installation:159521273:globulario/sensei-code"
		rec["publication_comment"] = 5151
		rec["published_at"] = "2026-09-14T11:02:01Z"
	}
	if mutate != nil {
		mutate(rec)
	}
	blob, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return blob
}

// legacyDir writes one historical relay store and returns its path.
func legacyDir(t *testing.T, files map[string][]byte) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "relays")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	for name, blob := range files {
		if err := os.WriteFile(filepath.Join(dir, name), blob, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func reviewStoreIn(t *testing.T) reviewstore.Store {
	t.Helper()
	return reviewstore.Store{Dir: filepath.Join(t.TempDir(), "reviews")}
}

// A published legacy relay becomes the same canonical review with READY relay
// evidence. The verdict is re-read from the artifact, never taken from the
// receipt's own copy of it.
func TestAPublishedHistoricalRelayMigratesAsDelivered(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	dir := legacyDir(t, map[string][]byte{
		relayRequest + ".json": legacyReceipt(t, "published", raw, nil),
	})
	reviews := reviewStoreIn(t)

	report, err := MigrateHistoricalRelays(dir, reviews, nil)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if report.Migrated() != 1 || report.Retained() != 0 {
		t.Fatalf("report: %+v", report.Records)
	}
	if report.Records[0].Outcome != MigratedReady {
		t.Fatalf("outcome %s, want %s", report.Records[0].Outcome, MigratedReady)
	}

	rec, found, err := reviews.Load(relayRequest)
	if err != nil || !found {
		t.Fatalf("the migrated review is not readable: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != raw || rec.ReviewDigest != ReviewDigest(raw) {
		t.Fatal("migration did not preserve the exact canonical bytes")
	}
	if !rec.Consumable() {
		t.Fatal("a published legacy relay did not migrate as delivered")
	}
	ev := relayEvidence(t, rec)
	if ev.State != reviewstore.Ready || ev.PublicationComment != 5151 {
		t.Fatalf("relay evidence: %+v", ev)
	}
	if ev.RelayPrincipal != operator.token() {
		t.Fatalf("relay principal %q, want %q", ev.RelayPrincipal, operator.token())
	}
	// The receipt's own summary copy reached nothing.
	blob, err := os.ReadFile(filepath.Join(reviews.Dir, relayRequest+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "a summary nobody may trust") {
		t.Fatalf("the legacy verdict copy was carried into the review record:\n%s", blob)
	}
	// The legacy file is archived, not deleted.
	if _, err := os.Stat(filepath.Join(dir, relayRequest+".json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the converted legacy file is still in place: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, migratedDir, relayRequest+".json")); err != nil {
		t.Fatalf("the converted legacy file was not archived: %v", err)
	}
}

// An accepted-but-unpublished legacy relay migrates STAGED, and nothing is
// published to make it otherwise.
func TestAnAcceptedHistoricalRelayMigratesStaged(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	dir := legacyDir(t, map[string][]byte{
		relayRequest + ".json": legacyReceipt(t, "accepted", raw, nil),
	})
	reviews := reviewStoreIn(t)

	report, err := MigrateHistoricalRelays(dir, reviews, nil)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if report.Records[0].Outcome != MigratedStaged {
		t.Fatalf("outcome %s, want %s", report.Records[0].Outcome, MigratedStaged)
	}
	rec, found, err := reviews.Load(relayRequest)
	if err != nil || !found {
		t.Fatalf("the migrated review is not readable: found=%v err=%v", found, err)
	}
	if rec.ArtifactRaw != raw {
		t.Fatal("migration did not preserve the exact canonical bytes")
	}
	if rec.Consumable() {
		t.Fatal("an unpublished legacy relay migrated as an answer")
	}
	ev := relayEvidence(t, rec)
	if ev.State != reviewstore.Pending {
		t.Fatalf("evidence state %q, want pending", ev.State)
	}
	if ev.Publication != "" || ev.PublicationComment != 0 || !ev.PublishedAt.IsZero() {
		t.Fatalf("a staged migration invented a publication: %+v", ev)
	}
}

// A legacy receipt whose bytes disagree with a review the store already holds is
// REFUSED, and its file is retained untouched.
func TestAConflictingHistoricalRelayIsRefusedAndRetained(t *testing.T) {
	mine := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	theirs := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"a different review entirely","instructions":"","findings":[]}`)
	dir := legacyDir(t, map[string][]byte{
		relayRequest + ".json": legacyReceipt(t, "published", theirs, nil),
	})
	reviews := reviewStoreIn(t)
	if _, err := reviews.Accept(reviewstore.Acceptance{
		RequestID: relayRequest, Artifact: mine,
		Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox, State: reviewstore.Ready,
			GitHubAuthor: "davecourtois", GitHubAuthorID: 1697116, GitHubComment: 5150},
		Validate: func(reviewartifact.Artifact) error { return nil },
	}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(dir, relayRequest+".json"))
	if err != nil {
		t.Fatal(err)
	}

	report, err := MigrateHistoricalRelays(dir, reviews, nil)
	if err != nil {
		t.Fatalf("migrating: %v", err)
	}
	if report.Records[0].Outcome != RetainedConflict {
		t.Fatalf("outcome %s, want %s (%s)", report.Records[0].Outcome, RetainedConflict, report.Records[0].Detail)
	}
	rec, _, _ := reviews.Load(relayRequest)
	if rec.ArtifactRaw != mine {
		t.Fatal("the conflicting legacy receipt replaced the stored review")
	}
	after, err := os.ReadFile(filepath.Join(dir, relayRequest+".json"))
	if err != nil {
		t.Fatalf("the refused legacy file was removed: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("the refused legacy file was rewritten")
	}
}

// Anything the migrator cannot establish is retained and reported. It never
// deletes the only surviving evidence of a state it could not read.
func TestAnUnestablishableHistoricalRelayIsRetainedAndReported(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	swapped := artifactFor(t, relaySubject, relayRequest, "chatgpt",
		`{"decision":"accept","summary":"swapped","instructions":"","findings":[]}`)

	for name, blob := range map[string][]byte{
		"not json at all": []byte("{ this is not a receipt"),
		"an artifact that is not canonical": legacyReceipt(t, "published", raw, func(m map[string]any) {
			m["artifact"] = "LGTM, ship it"
		}),
		"a digest naming other bytes": legacyReceipt(t, "published", raw, func(m map[string]any) {
			m["review_digest"] = reviewartifact.Digest(swapped)
		}),
		"a receipt whose candidate disagrees with its artifact": legacyReceipt(t, "published", raw, func(m map[string]any) {
			m["candidate_tree"] = strings.Repeat("a", 40)
		}),
		"a receipt whose reviewer disagrees with its artifact": legacyReceipt(t, "published", raw, func(m map[string]any) {
			m["reviewer_provider"] = "claude"
		}),
		"a receipt naming no terminal principal": legacyReceipt(t, "published", raw, func(m map[string]any) {
			m["relay_principal"] = map[string]any{"uid": 1000, "user": "dave"}
		}),
		"a payload the reviewer contract refuses": legacyReceipt(t, "published",
			artifactFor(t, relaySubject, relayRequest, "chatgpt", "LGTM, ship it"), nil),
		"published with no publication": legacyReceipt(t, "published", raw, func(m map[string]any) {
			delete(m, "publication")
			delete(m, "publication_comment")
		}),
		"an unknown lifecycle state": legacyReceipt(t, "withdrawn", raw, nil),
	} {
		t.Run(name, func(t *testing.T) {
			dir := legacyDir(t, map[string][]byte{relayRequest + ".json": blob})
			reviews := reviewStoreIn(t)

			report, err := MigrateHistoricalRelays(dir, reviews, nil)
			if err != nil {
				t.Fatalf("migrating: %v", err)
			}
			if len(report.Records) != 1 || report.Records[0].Outcome != RetainedUnreadable {
				t.Fatalf("report: %+v", report.Records)
			}
			if report.Records[0].Detail == "" {
				t.Fatal("a retained file was not explained")
			}
			if _, found, _ := reviews.Load(relayRequest); found {
				t.Fatal("an unestablishable legacy receipt became a review")
			}
			if _, err := os.Stat(filepath.Join(dir, relayRequest+".json")); err != nil {
				t.Fatalf("the retained legacy file is gone: %v", err)
			}
		})
	}
}

// Running the migration twice converges. The second run has nothing to do, and
// nothing about the review moved.
func TestMigrationIsIdempotent(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	dir := legacyDir(t, map[string][]byte{
		relayRequest + ".json": legacyReceipt(t, "published", raw, nil),
	})
	reviews := reviewStoreIn(t)

	if _, err := MigrateHistoricalRelays(dir, reviews, nil); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(filepath.Join(reviews.Dir, relayRequest+".json"))
	if err != nil {
		t.Fatal(err)
	}

	report, err := MigrateHistoricalRelays(dir, reviews, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Records) != 0 {
		t.Fatalf("the second run found %d file(s) to convert: %+v", len(report.Records), report.Records)
	}
	second, err := os.ReadFile(filepath.Join(reviews.Dir, relayRequest+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatalf("a second migration changed the review record:\n--- first\n%s\n--- second\n%s", first, second)
	}
}

// A legacy receipt for a review this workspace ALREADY holds converges on it:
// one record, and the relay's delivery recorded beside what is there.
func TestAHistoricalRelayForAKnownReviewAddsOnlyItsEvidence(t *testing.T) {
	raw := artifactFor(t, relaySubject, relayRequest, "chatgpt", acceptPayload)
	dir := legacyDir(t, map[string][]byte{
		relayRequest + ".json": legacyReceipt(t, "published", raw, nil),
	})
	reviews := reviewStoreIn(t)
	accepted, err := reviews.Accept(reviewstore.Acceptance{
		RequestID: relayRequest, Artifact: raw,
		Evidence: reviewstore.Evidence{Transport: reviewstore.GitHubMailbox, State: reviewstore.Ready,
			GitHubAuthor: "davecourtois", GitHubAuthorID: 1697116, GitHubComment: 5150,
			ObservedAt: time.Unix(1700000000, 0).UTC()},
		Validate: func(reviewartifact.Artifact) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := MigrateHistoricalRelays(dir, reviews, nil); err != nil {
		t.Fatal(err)
	}
	rec, _, err := reviews.Load(relayRequest)
	if err != nil {
		t.Fatal(err)
	}
	if rec.ArtifactRaw != accepted.ArtifactRaw || !rec.AcceptedAt.Equal(accepted.AcceptedAt) {
		t.Fatal("migration moved the bytes or the acceptance time of a review already held")
	}
	if len(rec.Evidence) != 2 {
		t.Fatalf("the record holds %d evidence rows, want the mailbox row plus the relay's", len(rec.Evidence))
	}
	if rec.Evidence[0].Transport != reviewstore.GitHubMailbox {
		t.Fatalf("the original observation did not survive: %+v", rec.Evidence[0])
	}
	if !relayEvidence(t, rec).Delivered() {
		t.Fatal("the migrated relay delivery is not recorded as complete")
	}
}

package reviewstore

import (
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/reviewartifact"
)

const (
	request = "r-0123456789abcdef"
	accept  = `{"decision":"accept","summary":"the ledger invariant holds at physical position 0","instructions":"","findings":[]}`
)

func artifact(t *testing.T, requestID, provider, body string) string {
	t.Helper()
	return mustArtifact(requestID, provider, body)
}

func mustArtifact(requestID, provider, body string) string {
	raw, err := reviewartifact.Artifact{
		ReviewerProvider: provider,
		TaskID:           "task-1789498413173471174",
		RequestID:        requestID,
		BaseSHA:          "91b475a172bba0257fd2ffd8a55d3edce582e883",
		CandidateDigest:  "sha256:caa4e6970000000000000000000000000000000000000000000000000000beef",
		CandidateTree:    "1b713c41d4d6ed058313ce940b0cc481e4b22b18",
		ReviewCommit:     "8e2579edbb35d109ffa1acfc4f5d8e7ef00be8a1",
		Body:             body,
	}.Render()
	if err != nil {
		panic(err)
	}
	return raw
}

func passes(reviewartifact.Artifact) error { return nil }

func store(t *testing.T) Store {
	t.Helper()
	return Store{Dir: filepath.Join(t.TempDir(), "reviews")}
}

func mailboxEvidence() Evidence {
	return Evidence{
		Transport: GitHubMailbox, ObservedAt: time.Unix(1700000000, 0).UTC(),
		GitHubAuthor: "davecourtois", GitHubAuthorID: 1697116, GitHubComment: 5001,
	}
}

func relayEvidence() Evidence {
	return Evidence{
		Transport: LocalRelay, ObservedAt: time.Unix(1700000100, 0).UTC(),
		RelayPrincipal: "uid:1000,user:dave,pid:4242,terminal:34816",
		Publication:    "globulario-sensei-code[bot]", PublicationComment: 5002,
		PublishedAt: time.Unix(1700000090, 0).UTC(),
	}
}

func accepted(t *testing.T, s Store, raw string, ev Evidence) Record {
	t.Helper()
	rec, err := s.Accept(Acceptance{RequestID: request, Artifact: raw, Evidence: ev, Validate: passes})
	if err != nil {
		t.Fatalf("accepting: %v", err)
	}
	return rec
}

// What was stored is what the reviewer wrote: exact bytes, and a digest naming
// exactly those bytes. The fixture carries surrounding whitespace a
// re-rendering store would silently drop.
func TestCreateThenLoadPreservesTheExactBytesAndDigest(t *testing.T) {
	s := store(t)
	raw := "\n " + artifact(t, request, "chatgpt", accept) + "\n"
	accepted(t, s, raw, mailboxEvidence())

	got, found, err := s.Load(request)
	if err != nil || !found {
		t.Fatalf("load: found=%v err=%v", found, err)
	}
	if got.ArtifactRaw != raw {
		t.Fatalf("stored bytes are %q, want the exact submitted %q", got.ArtifactRaw, raw)
	}
	if want := reviewartifact.Digest(raw); got.ReviewDigest != want {
		t.Fatalf("digest is %s, want the digest of the exact bytes %s", got.ReviewDigest, want)
	}
	if got.Standing != Advisory {
		t.Fatalf("standing is %q, want %q -- no transport establishes independence", got.Standing, Advisory)
	}
}

// Identity is derived by reparsing, never kept as a second copy beside the
// bytes. The record carries no field that could disagree with the artifact.
func TestIdentityIsDerivedFromTheStoredBytes(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	accepted(t, s, raw, mailboxEvidence())
	rec, _, err := s.Load(request)
	if err != nil {
		t.Fatal(err)
	}
	art, err := rec.Artifact()
	if err != nil {
		t.Fatalf("the stored record did not reparse: %v", err)
	}
	if art.ReviewerProvider != "chatgpt" {
		t.Fatalf("reviewer provider is %q, want chatgpt", art.ReviewerProvider)
	}

	// Structural: no stored copy of what the review says or who produced it.
	blob, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(blob, &fields); err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"decision", "summary", "findings", "instructions", "reviewer_provider", "reviewer"} {
		if _, ok := fields[forbidden]; ok {
			t.Errorf("the record stores %q beside the artifact; that copy can disagree with the bytes it describes", forbidden)
		}
	}
}

// A record that cannot prove it is the review it claims to be is refused, not
// returned half-trusted and not repaired by overwriting.
func TestAnUnprovableRecordIsRefusedAndNeverRepaired(t *testing.T) {
	raw := artifact(t, request, "chatgpt", accept)
	for name, corrupt := range map[string]func(*Record){
		"a digest naming other bytes": func(r *Record) { r.ReviewDigest = reviewartifact.Digest("something else") },
		"bytes that no longer parse":  func(r *Record) { r.ArtifactRaw = "not an artifact" },
		"an artifact for another request": func(r *Record) {
			r.ArtifactRaw = mustArtifact("r-ffffffffffffffff", "chatgpt", accept)
			r.ReviewDigest = reviewartifact.Digest(r.ArtifactRaw)
		},
		"a standing this store never records": func(r *Record) { r.Standing = "independent" },
	} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			accepted(t, s, raw, mailboxEvidence())
			path := filepath.Join(s.Dir, request+".json")

			var rec Record
			blob, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(blob, &rec); err != nil {
				t.Fatal(err)
			}
			corrupt(&rec)
			out, err := json.MarshalIndent(rec, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}
			before, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}

			got, found, err := s.Load(request)
			if err == nil {
				t.Fatalf("a corrupt record loaded as %+v", got)
			}
			if !errors.Is(err, ErrUnreadable) {
				t.Fatalf("err = %v, want ErrUnreadable", err)
			}
			if found {
				t.Fatal("a corrupt record was reported as found")
			}

			// Offering the good artifact again must not silently repair it.
			if _, err := s.Accept(Acceptance{RequestID: request, Artifact: raw,
				Evidence: mailboxEvidence(), Validate: passes}); err == nil {
				t.Fatal("re-accepting overwrote a record that could not be read")
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("an unreadable record was rewritten; those bytes are the only evidence of what was accepted")
			}
		})
	}
}

// The same bytes seen twice by the same transport change nothing at all.
func TestSameDigestAndSameEvidenceIsANoOp(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	first := accepted(t, s, raw, mailboxEvidence())
	second := accepted(t, s, raw, mailboxEvidence())

	if len(second.Evidence) != 1 {
		t.Fatalf("a repeated observation recorded %d evidence rows, want 1", len(second.Evidence))
	}
	if !second.AcceptedAt.Equal(first.AcceptedAt) {
		t.Fatalf("acceptance time moved from %s to %s on a redelivery", first.AcceptedAt, second.AcceptedAt)
	}
	if second.ArtifactRaw != first.ArtifactRaw || second.ReviewDigest != first.ReviewDigest {
		t.Fatal("a redelivery changed the semantic record")
	}
}

// The same bytes arriving by a second transport add evidence and nothing else.
// This is the convergence R2 exists for: one review, two ways of having seen it.
func TestASecondTransportAddsEvidenceWithoutChangingMeaning(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	first := accepted(t, s, raw, relayEvidence())
	second := accepted(t, s, raw, mailboxEvidence())

	if len(second.Evidence) != 2 {
		t.Fatalf("recorded %d evidence rows, want 2", len(second.Evidence))
	}
	if second.ArtifactRaw != first.ArtifactRaw || second.ReviewDigest != first.ReviewDigest {
		t.Fatal("a second transport changed the canonical bytes")
	}
	if !second.AcceptedAt.Equal(first.AcceptedAt) {
		t.Fatal("a second transport moved the acceptance time")
	}
	art, err := second.Artifact()
	if err != nil {
		t.Fatal(err)
	}
	if art.ReviewerProvider != "chatgpt" {
		t.Fatalf("reviewer provider became %q after transport evidence was added", art.ReviewerProvider)
	}
	seen := map[Transport]bool{}
	for _, ev := range second.Evidence {
		seen[ev.Transport] = true
	}
	if !seen[LocalRelay] || !seen[GitHubMailbox] {
		t.Fatalf("both transports should be recorded, got %v", seen)
	}
}

// Transport evidence names transport principals. None of them is the reviewer,
// and no evidence row can make one become the reviewer.
func TestTransportPrincipalsNeverBecomeTheReviewer(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	ev := relayEvidence()
	ev.RelayPrincipal = "uid:0,user:root,pid:1,terminal:1"
	ev.Publication = "chatgpt"
	ev.GitHubAuthor = "chatgpt"
	rec := accepted(t, s, raw, ev)

	art, err := rec.Artifact()
	if err != nil {
		t.Fatal(err)
	}
	if art.ReviewerProvider != "chatgpt" {
		t.Fatalf("provider is %q, want the artifact's own chatgpt", art.ReviewerProvider)
	}
	// The evidence said "chatgpt" in two transport fields. That must be a fact
	// about delivery, reachable only as evidence, never as review meaning.
	if rec.ReviewDigest != reviewartifact.Digest(raw) {
		t.Fatal("transport evidence changed the digest")
	}
	reloaded, _, err := s.Load(request)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.ArtifactRaw != raw {
		t.Fatal("transport evidence changed the stored bytes")
	}
}

// One request, one canonical artifact, ever. A different artifact conflicts and
// the first is left exactly as it was -- no last-write-wins, no merge.
func TestADifferentArtifactConflictsAndCannotReplaceTheFirst(t *testing.T) {
	s := store(t)
	original := artifact(t, request, "chatgpt", accept)
	accepted(t, s, original, mailboxEvidence())
	path := filepath.Join(s.Dir, request+".json")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	for name, rival := range map[string]string{
		"a different verdict": artifact(t, request, "chatgpt",
			`{"decision":"revise","summary":"the ledger invariant does not hold","instructions":"fix it","findings":[]}`),
		"a semantically equal body differing by one byte": artifact(t, request, "chatgpt",
			strings.Replace(accept, "position 0", "position 0.", 1)),
		"the same body from another provider": artifact(t, request, "claude", accept),
	} {
		t.Run(name, func(t *testing.T) {
			if rival == original {
				t.Fatal("the rival equals the original; this case proves nothing")
			}
			_, err := s.Accept(Acceptance{RequestID: request, Artifact: rival,
				Evidence: relayEvidence(), Validate: passes})
			if !errors.Is(err, ErrConflict) {
				t.Fatalf("err = %v, want ErrConflict", err)
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatal("a conflicting artifact changed the stored record")
			}
			rec, _, err := s.Load(request)
			if err != nil {
				t.Fatal(err)
			}
			if rec.ArtifactRaw != original {
				t.Fatal("the conflicting artifact replaced the original")
			}
		})
	}
}

// Racing writers resolve by create/read/compare. Identical bytes converge on
// one record; different bytes leave exactly one stored and refuse the rest.
func TestConcurrentWritersConvergeOrConflictAndNeverReplace(t *testing.T) {
	t.Run("same digest converges", func(t *testing.T) {
		s := store(t)
		raw := artifact(t, request, "chatgpt", accept)
		var wg sync.WaitGroup
		errs := make([]error, 8)
		for i := range errs {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				ev := mailboxEvidence()
				ev.GitHubComment = int64(6000 + i)
				_, errs[i] = s.Accept(Acceptance{RequestID: request, Artifact: raw, Evidence: ev, Validate: passes})
			}(i)
		}
		wg.Wait()
		for i, err := range errs {
			if err != nil {
				t.Fatalf("writer %d of identical bytes failed: %v", i, err)
			}
		}
		rec, found, err := s.Load(request)
		if err != nil || !found {
			t.Fatalf("load: found=%v err=%v", found, err)
		}
		if rec.ArtifactRaw != raw {
			t.Fatal("concurrent identical writers changed the bytes")
		}
		if len(rec.Evidence) != len(errs) {
			t.Fatalf("recorded %d evidence rows, want %d -- one per distinct observation", len(rec.Evidence), len(errs))
		}
	})

	t.Run("different digests conflict", func(t *testing.T) {
		s := store(t)
		raws := []string{
			artifact(t, request, "chatgpt", accept),
			artifact(t, request, "chatgpt", strings.Replace(accept, "position 0", "position 1", 1)),
			artifact(t, request, "chatgpt", strings.Replace(accept, "position 0", "position 2", 1)),
			artifact(t, request, "chatgpt", strings.Replace(accept, "position 0", "position 3", 1)),
		}
		var wg sync.WaitGroup
		errs := make([]error, len(raws))
		for i, raw := range raws {
			wg.Add(1)
			go func(i int, raw string) {
				defer wg.Done()
				_, errs[i] = s.Accept(Acceptance{RequestID: request, Artifact: raw,
					Evidence: mailboxEvidence(), Validate: passes})
			}(i, raw)
		}
		wg.Wait()

		won := 0
		for i, err := range errs {
			switch {
			case err == nil:
				won++
			case errors.Is(err, ErrConflict):
			default:
				t.Fatalf("writer %d failed with something other than a conflict: %v", i, err)
			}
		}
		if won != 1 {
			t.Fatalf("%d writers of different bytes succeeded, want exactly 1", won)
		}
		rec, found, err := s.Load(request)
		if err != nil || !found {
			t.Fatalf("load: found=%v err=%v", found, err)
		}
		var isOne bool
		for _, raw := range raws {
			if rec.ArtifactRaw == raw {
				isOne = true
			}
		}
		if !isOne {
			t.Fatal("the stored record is none of the offered artifacts")
		}
		if len(rec.Evidence) != 1 {
			t.Fatalf("the winner recorded %d evidence rows, want 1", len(rec.Evidence))
		}
	})
}

// A payload the reviewer-body parser rejects never becomes a durable review,
// and a caller cannot opt out of that check.
func TestNothingIsStoredWithoutTheReviewerBodyCheck(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", "LGTM, ship it")

	if _, err := s.Accept(Acceptance{RequestID: request, Artifact: raw,
		Evidence: mailboxEvidence(), Validate: nil}); err == nil {
		t.Fatal("a review was recorded with no reviewer-body parser at all")
	}
	if _, _, err := s.Load(request); err != nil {
		t.Fatal(err)
	}

	refuse := func(reviewartifact.Artifact) error { return errors.New("not a reviewer verdict") }
	if _, err := s.Accept(Acceptance{RequestID: request, Artifact: raw,
		Evidence: mailboxEvidence(), Validate: refuse}); err == nil {
		t.Fatal("a payload the parser rejected was recorded")
	}
	if _, found, err := s.Load(request); found || err != nil {
		t.Fatalf("a rejected payload left a record: found=%v err=%v", found, err)
	}
}

// The store answers one obligation and is never a way to name a file elsewhere,
// nor a way to invent an obligation the artifact does not claim.
func TestTheStoreRefusesUnsafeOrMismatchedIdentity(t *testing.T) {
	s := store(t)
	for _, id := range []string{"", "../escape", "a/b", "r-with space", strings.Repeat("r", 200), ".", ".."} {
		if _, _, err := s.Load(id); err == nil {
			t.Errorf("Load accepted unsafe request id %q", id)
		}
		if _, err := s.Accept(Acceptance{RequestID: id, Artifact: artifact(t, request, "chatgpt", accept),
			Evidence: mailboxEvidence(), Validate: passes}); err == nil {
			t.Errorf("Accept accepted unsafe request id %q", id)
		}
	}
	// A valid artifact offered against an obligation it does not name.
	if _, err := s.Accept(Acceptance{RequestID: "r-ffffffffffffffff",
		Artifact: artifact(t, request, "chatgpt", accept),
		Evidence: mailboxEvidence(), Validate: passes}); err == nil {
		t.Fatal("an artifact was recorded against a request it does not answer")
	}
	// An unknown transport is not recordable evidence.
	if _, err := s.Accept(Acceptance{RequestID: request, Artifact: artifact(t, request, "chatgpt", accept),
		Evidence: Evidence{Transport: "carrier-pigeon"}, Validate: passes}); err == nil {
		t.Fatal("an unknown transport was recorded")
	}
	if _, err := s.Accept(Acceptance{RequestID: request, Artifact: artifact(t, request, "chatgpt", accept),
		Evidence: Evidence{}, Validate: passes}); err == nil {
		t.Fatal("evidence naming no transport was recorded")
	}
}

// Review records are private to the operator, as the other stores here are.
func TestRecordsAreWrittenPrivate(t *testing.T) {
	s := store(t)
	raw := artifact(t, request, "chatgpt", accept)
	accepted(t, s, raw, mailboxEvidence())
	accepted(t, s, raw, relayEvidence()) // exercises the rewrite path too

	dir, err := os.Stat(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dir.Mode().Perm(); perm != 0o700 {
		t.Fatalf("store directory mode is %o, want 700", perm)
	}
	f, err := os.Stat(filepath.Join(s.Dir, request+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := f.Mode().Perm(); perm != 0o600 {
		t.Fatalf("record mode is %o, want 600", perm)
	}
	if _, err := os.Stat(filepath.Join(s.Dir, request+".json.tmp")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("a temporary file was left beside the record")
	}
}

// The durable semantic record sits beneath every transport, so it depends on
// none of them. A dependency here would make what a review MEANS a function of
// the pipe that carried it -- the exact collapse R2 removes.
func TestThisPackageDependsOnNoTransport(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatal(err)
	}
	forbidden := []string{"internal/ghbridge", "internal/control", "internal/agent", "internal/workflow"}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			for _, imp := range file.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if strings.HasSuffix(path, bad) {
						t.Errorf("%s imports %s; the semantic record must not depend on a transport", name, path)
					}
				}
			}
		}
	}
}

// The production shape: adapters carry no clock and submit a zero ObservedAt.
//
// TestSameDigestAndSameEvidenceIsANoOp supplies a fixed timestamp, which is not
// what the mailbox or the relay does. With whole-struct equality, Accept stamped
// each redelivery with a different time and every repeat poll appended a row --
// idempotent in the fixture and an append in production.
func TestRedeliveryWithNoSuppliedClockIsStillIdempotent(t *testing.T) {
	for name, ev := range map[string]Evidence{
		"a mailbox comment re-read on a later poll": {
			Transport: GitHubMailbox, GitHubAuthor: "davecourtois",
			GitHubAuthorID: 1697116, GitHubComment: 5150,
		},
		"a relay convergence retried after a store failure": {
			Transport: LocalRelay, RelayPrincipal: "uid:1000,user:dave,pid:4242,terminal:34816",
			Publication: "globulario-sensei-code[bot]", PublicationComment: 5002,
			PublishedAt: time.Unix(1700000090, 0).UTC(),
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !ev.ObservedAt.IsZero() {
				t.Fatal("this fixture must carry no clock; that is the production shape under test")
			}
			s := store(t)
			raw := artifact(t, request, "chatgpt", accept)

			first, err := s.Accept(Acceptance{RequestID: request, Artifact: raw, Evidence: ev, Validate: passes})
			if err != nil {
				t.Fatalf("first acceptance: %v", err)
			}
			for i := 0; i < 4; i++ {
				again, err := s.Accept(Acceptance{RequestID: request, Artifact: raw, Evidence: ev, Validate: passes})
				if err != nil {
					t.Fatalf("redelivery %d: %v", i, err)
				}
				if len(again.Evidence) != 1 {
					t.Fatalf("redelivery %d recorded %d evidence rows, want 1", i, len(again.Evidence))
				}
				if !again.AcceptedAt.Equal(first.AcceptedAt) {
					t.Fatalf("redelivery %d moved the acceptance time", i)
				}
				if !again.Evidence[0].ObservedAt.Equal(first.Evidence[0].ObservedAt) {
					t.Fatalf("redelivery %d overwrote the first observation's time: %s then %s",
						i, first.Evidence[0].ObservedAt, again.Evidence[0].ObservedAt)
				}
			}
			reloaded, _, err := s.Load(request)
			if err != nil {
				t.Fatal(err)
			}
			if len(reloaded.Evidence) != 1 {
				t.Fatalf("the stored record holds %d evidence rows, want 1", len(reloaded.Evidence))
			}
		})
	}
}

// A record must state the OBSERVED residue its own schema requires.
//
// Accepting the workspace trust boundary means a structurally complete local
// record is read as written. It does not license a record that claims transport
// established acceptance and then declines to say which ingestion did it, nor
// one on a schema this package never wrote.
func TestARecordMissingItsAcceptanceResidueIsRefused(t *testing.T) {
	raw := artifact(t, request, "chatgpt", accept)
	for name, corrupt := range map[string]func(*Record){
		"an unwritten schema version":           func(r *Record) { r.Version = 0 },
		"a schema version from a later package": func(r *Record) { r.Version = SchemaVersion + 1 },
		"no acceptance time":                    func(r *Record) { r.AcceptedAt = time.Time{} },
		"no transport observation at all":       func(r *Record) { r.Evidence = nil },
		"an empty observation":                  func(r *Record) { r.Evidence = []Evidence{{}} },
		"a transport nobody defines": func(r *Record) {
			r.Evidence = []Evidence{{Transport: "carrier-pigeon", ObservedAt: time.Unix(1700000000, 0).UTC()}}
		},
		"an observation with no time": func(r *Record) { r.Evidence[0].ObservedAt = time.Time{} },
		"mailbox evidence naming no comment": func(r *Record) {
			r.Evidence = []Evidence{{Transport: GitHubMailbox, ObservedAt: time.Unix(1700000000, 0).UTC(),
				GitHubAuthor: "davecourtois", GitHubAuthorID: 1697116}}
		},
		"mailbox evidence naming no principal": func(r *Record) {
			r.Evidence = []Evidence{{Transport: GitHubMailbox, ObservedAt: time.Unix(1700000000, 0).UTC(),
				GitHubComment: 5150}}
		},
		"relay evidence naming no principal": func(r *Record) {
			r.Evidence = []Evidence{{Transport: LocalRelay, ObservedAt: time.Unix(1700000000, 0).UTC(),
				Publication: "app", PublicationComment: 5002, PublishedAt: time.Unix(1700000090, 0).UTC()}}
		},
		"relay evidence naming no publication": func(r *Record) {
			r.Evidence = []Evidence{{Transport: LocalRelay, ObservedAt: time.Unix(1700000000, 0).UTC(),
				RelayPrincipal: "uid:1000", PublishedAt: time.Unix(1700000090, 0).UTC()}}
		},
		"relay evidence with no publication time": func(r *Record) {
			r.Evidence = []Evidence{{Transport: LocalRelay, ObservedAt: time.Unix(1700000000, 0).UTC(),
				RelayPrincipal: "uid:1000", Publication: "app", PublicationComment: 5002}}
		},
		"one good row and one incomplete one": func(r *Record) {
			r.Evidence = append(r.Evidence, Evidence{Transport: LocalRelay, ObservedAt: time.Unix(1700000000, 0).UTC()})
		},
	} {
		t.Run(name, func(t *testing.T) {
			s := store(t)
			accepted(t, s, raw, mailboxEvidence())
			path := filepath.Join(s.Dir, request+".json")

			var rec Record
			blob, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(blob, &rec); err != nil {
				t.Fatal(err)
			}
			before := rec
			corrupt(&rec)
			out, err := json.MarshalIndent(rec, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if string(out) == string(blob) {
				t.Fatal("the corruption changed nothing; this case proves nothing")
			}
			if err := os.WriteFile(path, out, 0o600); err != nil {
				t.Fatal(err)
			}

			got, found, err := s.Load(request)
			if err == nil {
				t.Fatalf("a record missing its acceptance residue loaded as %+v", got)
			}
			if !errors.Is(err, ErrUnreadable) {
				t.Fatalf("err = %v, want ErrUnreadable", err)
			}
			if found {
				t.Fatal("an unreadable record was reported as found")
			}
			_ = before
		})
	}
}

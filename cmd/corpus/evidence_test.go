package main

// Properties of the evidence representation model.
//
// Sixteen review rounds on PR #118 found the model's rules one hand-written
// filename at a time (`A[1].log`, `X.log.part-`, `A.log.part-x.log.run`,
// `X.log.part-001-of-001.receipts.jsonl`, …). Those specimens are kept as
// regressions in main_test.go, because each is a real defect with a real
// reproduction. What follows is the other half: the laws stated as
// properties and checked over generated namespaces, so the NEXT specimen is
// constructed mechanically rather than waiting for a reviewer to imagine it.

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// namespace generates a directory listing: some whole streams, some split,
// some artifacts, some irrelevant files, and some deliberate damage.
func namespace(rnd *rand.Rand) (names []string, intended map[string]bool) {
	intended = map[string]bool{}
	stems := []string{"A", "B", "run-1", "A.log.part-x", "", "deep.name"}
	exts := []string{".log", ".jsonl"}
	used := map[string]bool{}
	for i := 0; i < 1+rnd.Intn(4); i++ {
		stream := stems[rnd.Intn(len(stems))] + exts[rnd.Intn(len(exts))]
		// One representation per stream: a namespace that names the same
		// stream twice is a contradiction the index is right to refuse, and
		// the generator must not manufacture it accidentally.
		if used[stream] {
			continue
		}
		used[stream] = true
		switch rnd.Intn(3) {
		case 0: // whole
			names = append(names, stream)
			intended[stream] = true
		default: // split
			total := 1 + rnd.Intn(3)
			for n := 1; n <= total; n++ {
				names = append(names, fmt.Sprintf("%s%s%03d-of-%03d", stream, partMarker, n, total))
			}
			intended[stream] = true
		}
		if rnd.Intn(2) == 0 {
			for _, artifact := range []string{".run", ".receipts.jsonl", ".recipes-after.json", ".graph.metadata.pre.json", ".candidate.diff"} {
				names = append(names, streamBase(stream)+artifact)
			}
		}
	}
	for i := 0; i < rnd.Intn(3); i++ {
		names = append(names, []string{"notes.txt", "README.md", "x.json", "diff.patch"}[rnd.Intn(4)])
	}
	rnd.Shuffle(len(names), func(i, j int) { names[i], names[j] = names[j], names[i] })
	return names, intended
}

func TestEvidenceIndexProperties(t *testing.T) {
	rnd := rand.New(rand.NewSource(20260829))
	for round := 0; round < 2000; round++ {
		names, intended := namespace(rnd)
		ix := BuildEvidenceIndex("d", names)

		// PARTITION: exactly one role per physical name, no name unclassified.
		if len(ix.Roles) != len(dedupe(append([]string(nil), names...))) {
			t.Fatalf("round %d: %d name(s) classified for %d file(s): %v", round, len(ix.Roles), len(names), names)
		}
		for _, name := range names {
			switch ix.Roles[name] {
			case RoleWholeStream, RolePart, RoleArtifact, RoleIrrelevant:
			default:
				t.Fatalf("round %d: %q has role %q", round, name, ix.Roles[name])
			}
		}

		// ORDER INDEPENDENCE: the listing's order cannot change the result.
		shuffled := append([]string(nil), names...)
		rnd.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		other := BuildEvidenceIndex("d", shuffled)
		if strings.Join(ix.StreamNames(), "|") != strings.Join(other.StreamNames(), "|") {
			t.Fatalf("round %d: order changed the streams: %v vs %v", round, ix.StreamNames(), other.StreamNames())
		}
		for _, s := range ix.StreamNames() {
			a, aerr := ix.Files(s)
			b, berr := other.Files(s)
			if fmt.Sprint(a, aerr) != fmt.Sprint(b, berr) {
				t.Fatalf("round %d: order changed %q: %v/%v vs %v/%v", round, s, a, aerr, b, berr)
			}
		}

		// ROUND TRIP: every intended stream is present and readable.
		for stream := range intended {
			rep, ok := ix.Streams[stream]
			if !ok {
				t.Fatalf("round %d: intended stream %q vanished from %v", round, stream, names)
			}
			if rep.Problem != nil {
				t.Fatalf("round %d: intact stream %q was refused: %v", round, stream, rep.Problem)
			}
			if _, err := ix.Files(stream); err != nil {
				t.Fatalf("round %d: intact stream %q has no files: %v", round, stream, err)
			}
		}

		// UNIQUENESS: never whole and split at once.
		for name, rep := range ix.Streams {
			if rep.Problem == nil && rep.Whole != "" && len(rep.Parts) != 0 {
				t.Fatalf("round %d: %q accepted whole AND split", round, name)
			}
		}

		// COMPLETENESS: an accepted split is exactly 1..N of N, in order.
		for name, rep := range ix.Streams {
			if rep.Problem != nil || len(rep.Parts) == 0 {
				continue
			}
			for i, p := range rep.Parts {
				if p.Index != i+1 || p.Total != len(rep.Parts) {
					t.Fatalf("round %d: %q accepted pieces %v", round, name, rep.Parts)
				}
			}
		}

		// MUTATION: damaging one piece must refuse, never silently lose.
		for name, rep := range ix.Streams {
			if rep.Problem != nil || len(rep.Parts) == 0 {
				continue
			}
			damages := []string{"misnumber", "duplicate"}
			if len(rep.Parts) > 1 {
				// Dropping a piece of a one-piece stream removes its only
				// file, and nothing carried outside the filenames says the
				// stream ever existed -- see
				// TestDeletingEveryPieceIsUndetectableFromNamesAlone.
				damages = append(damages, "drop-first", "drop-last")
			}
			for _, damage := range damages {
				damaged := damagedNamespace(names, rep, damage)
				dix := BuildEvidenceIndex("d", damaged)
				drep, ok := dix.Streams[name]
				if !ok {
					t.Fatalf("round %d: %s made stream %q vanish entirely.\n  original: %v\n  damaged:  %v", round, damage, name, names, damaged)
				}
				if drep.Problem == nil {
					if _, err := dix.Files(name); err == nil {
						t.Fatalf("round %d: %s of %q was accepted as complete evidence: %v", round, damage, name, damaged)
					}
				}
			}
		}
	}
}

// damagedNamespace removes, misnumbers or duplicates one piece of a stream.
func damagedNamespace(names []string, rep *Representation, damage string) []string {
	out := []string{}
	drop := ""
	switch damage {
	case "drop-first":
		drop = rep.Parts[0].Name
	case "drop-last":
		drop = rep.Parts[len(rep.Parts)-1].Name
	}
	for _, n := range names {
		switch {
		case n == drop:
			continue
		case damage == "misnumber" && n == rep.Parts[0].Name:
			out = append(out, strings.Replace(n, "-of-", "-of-9", 1))
		default:
			out = append(out, n)
		}
	}
	if damage == "duplicate" {
		out = append(out, rep.Parts[0].Name)
	}
	return out
}

// NON-INTERFERENCE: adding a stream's artifact cannot change another
// stream's representation.
func TestAddingAnArtifactCannotDisturbAnotherStream(t *testing.T) {
	base := []string{"A.log", "A.log.part-x.log.part-001-of-001", "B.jsonl"}
	before := BuildEvidenceIndex("d", base)
	for _, artifact := range []string{"A.run", "A.log.part-x.run", "B.receipts.jsonl", "A.graph.metadata.pre.json"} {
		after := BuildEvidenceIndex("d", append(append([]string(nil), base...), artifact))
		if strings.Join(before.StreamNames(), "|") != strings.Join(after.StreamNames(), "|") {
			t.Fatalf("adding %q changed the streams: %v -> %v", artifact, before.StreamNames(), after.StreamNames())
		}
		for _, s := range before.StreamNames() {
			a, aerr := before.Files(s)
			b, berr := after.Files(s)
			if fmt.Sprint(a, aerr) != fmt.Sprint(b, berr) {
				t.Fatalf("adding %q changed %q", artifact, s)
			}
		}
	}
}

// TRANSPORT TRANSPARENCY: a stream's identity does not depend on whether it
// is stored whole or split, and pieces are ordered numerically -- lexical
// order put `part-1000-of-1000` between 100 and 101 and rejected a complete
// stream (#118, 13a, deferred to this refactor).
func TestSplitAndWholeAgreeAndOrderIsNumeric(t *testing.T) {
	whole := BuildEvidenceIndex("d", []string{"X.log"})
	var split []string
	const n = 1200
	for i := 1; i <= n; i++ {
		split = append(split, fmt.Sprintf("X.log%s%03d-of-%03d", partMarker, i, n))
	}
	ix := BuildEvidenceIndex("d", split)
	if strings.Join(whole.StreamNames(), "|") != strings.Join(ix.StreamNames(), "|") {
		t.Fatalf("identity changed with representation: %v vs %v", whole.StreamNames(), ix.StreamNames())
	}
	rep := ix.Streams["X.log"]
	if rep.Problem != nil {
		t.Fatalf("a complete %d-piece stream was refused: %v", n, rep.Problem)
	}
	files, err := ix.Files("X.log")
	if err != nil || len(files) != n {
		t.Fatalf("files = %d, %v", len(files), err)
	}
	if !sort.SliceIsSorted(rep.Parts, func(i, j int) bool { return rep.Parts[i].Index < rep.Parts[j].Index }) {
		t.Fatal("pieces are not in numeric order")
	}
	if !strings.HasSuffix(files[999], fmt.Sprintf("%s1000-of-%03d", partMarker, n)) {
		t.Fatalf("piece 1000 is not tenth-hundredth in order: %s", files[999])
	}
}

// The limit of a filename-carried declaration, stated rather than hidden.
//
// `-of-NNN` lets any surviving piece prove that others are missing. It
// cannot prove anything once NO piece survives: a stream deleted entirely
// leaves no name to carry the claim, and the corpus regenerates smaller
// with nothing to object to. The property suite found this in its second
// generated namespace, and it is the argument for a declaration that lives
// OUTSIDE the files it describes -- a producer-authored manifest naming the
// streams, their representation and their digests.
//
// Recorded as a known boundary of this model, with the falsifier that would
// close it: an index built against a declaration, not against a listing.
func TestDeletingEveryPieceIsUndetectableFromNamesAlone(t *testing.T) {
	full := BuildEvidenceIndex("d", []string{"X.log.part-001-of-001", "other.log"})
	if _, ok := full.Streams["X.log"]; !ok {
		t.Fatal("the one-piece stream is not present to begin with")
	}
	// Delete its only file. Nothing remains to claim the stream exists.
	gone := BuildEvidenceIndex("d", []string{"other.log"})
	if _, ok := gone.Streams["X.log"]; ok {
		t.Fatal("this test asserts the LIMIT: names alone cannot notice a fully deleted stream")
	}
	// A two-piece stream is different: any survivor carries the total.
	partial := BuildEvidenceIndex("d", []string{"Y.log.part-001-of-002"})
	rep, ok := partial.Streams["Y.log"]
	if !ok || rep.Problem == nil {
		t.Fatalf("a surviving piece must refuse the truncated stream: %+v", rep)
	}
	if !strings.Contains(rep.Problem.Error(), "truncated") {
		t.Fatalf("the refusal must say what is missing: %v", rep.Problem)
	}
}

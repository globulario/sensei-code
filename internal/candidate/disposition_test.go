package candidate

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Removing a candidate before its evidence is recorded converts a decision into
// a gap: afterwards nobody can tell a candidate that was cleaned up from one
// that never existed.
func TestRemovalRequiresEvidenceThatOutlivesTheCandidate(t *testing.T) {
	for _, d := range []Disposition{Adopted, Rejected, Superseded, Disposed} {
		r := Resolution{Disposition: d, Reason: "because"}
		if err := r.Validate(); !errors.Is(err, ErrNoEvidence) {
			t.Errorf("%s was accepted with no evidence at all: %v", d, err)
		}
		r.Evidence = Evidence{BaseSHA: "abc123"}
		if err := r.Validate(); !errors.Is(err, ErrNoEvidence) {
			t.Errorf("%s was accepted without recording the work it removed: %v", d, err)
		}
		// Either the work is described, or its absence is stated.
		r.Evidence.ProducedNoWork = true
		if err := r.Validate(); err != nil {
			t.Errorf("%s refused a candidate that recorded producing nothing: %v", d, err)
		}
		r.Evidence = Evidence{BaseSHA: "abc123", DiffDigest: "17b59fda5bc0", ChangedPaths: []string{"a.go"}}
		if err := r.Validate(); err != nil {
			t.Errorf("%s refused fully recorded evidence: %v", d, err)
		}
	}
}

// Retention is a decision with a reason, not the absence of a cleanup. A
// retained candidate must be able to say which branch of the matrix kept it.
func TestRetentionStatesItsReason(t *testing.T) {
	for _, d := range []Disposition{Retained, Resumable} {
		if err := (Resolution{Disposition: d}).Validate(); err == nil {
			t.Errorf("%s was recorded with no reason, which is the state this replaces", d)
		}
		if err := (Resolution{Disposition: d, Reason: "unpublished and human-owned"}).Validate(); err != nil {
			t.Errorf("%s refused a stated reason: %v", d, err)
		}
		if !d.Keeps() {
			t.Errorf("%s does not keep the worktree, so the matrix is wrong", d)
		}
	}
	for _, d := range []Disposition{Adopted, Rejected, Superseded, Disposed} {
		if d.Keeps() {
			t.Errorf("%s keeps the worktree, so nothing is ever cleaned up", d)
		}
	}
}

// The vocabulary is closed. An unrecognised disposition is refused rather than
// stored, because a candidate in a state nothing understands is the defect.
func TestDispositionVocabularyIsClosed(t *testing.T) {
	if err := (Resolution{Disposition: "probably fine", Reason: "r"}).Validate(); err == nil {
		t.Fatal("an invented disposition was accepted")
	}
	if (Disposition("")).Valid() {
		t.Error("the empty disposition is valid, so an unresolved candidate looks resolved")
	}
}

// A recorded disposition must survive the process, because the question it
// answers is asked when no run is in progress.
func TestResolutionOutlivesTheRun(t *testing.T) {
	root := t.TempDir()
	id := Identity{
		TaskID: "task-1", Repository: root, BaseSHA: "1bc39f29a7a2",
		Worktree: filepath.Join(root, "wt"), Branch: "sensei-code/task-1",
		WorktreeState: "clean", CreatedAt: time.Now().UTC(),
	}
	if err := id.Save(root); err != nil {
		t.Fatal(err)
	}
	if loaded, _, _ := Load(root, "task-1"); !loaded.Unresolved() {
		t.Fatal("a fresh candidate already claims a disposition")
	}

	resolved, err := id.Resolve(root, Resolution{
		Disposition: Retained,
		Reason:      "accepted by review and unpublished; landing it is the human's decision",
	})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Resolution.DecidedAt.IsZero() {
		t.Error("the disposition records no time, so its order cannot be reconstructed")
	}
	if resolved.Resolution.Evidence.BaseSHA != "1bc39f29a7a2" {
		t.Error("the base was not carried into the evidence")
	}

	back, ok, err := Load(root, "task-1")
	if err != nil || !ok {
		t.Fatalf("load: %v ok=%v", err, ok)
	}
	if back.Unresolved() {
		t.Fatal("the disposition did not survive being written down")
	}
	if back.Resolution.Disposition != Retained || !strings.Contains(back.Resolution.Reason, "unpublished") {
		t.Fatalf("the disposition changed across the record: %+v", back.Resolution)
	}
}

// The listing must distinguish "nobody decided" from "somebody decided to
// keep this", because that distinction is the entire point of the mechanism.
func TestListingNamesUnresolvedCandidatesAsUnresolved(t *testing.T) {
	root := t.TempDir()
	live := Identity{TaskID: "task-live", BaseSHA: "aaa111", Branch: "b1", Worktree: "/w1", CreatedAt: time.Now()}
	if err := live.Save(root); err != nil {
		t.Fatal(err)
	}
	kept := Identity{TaskID: "task-kept", BaseSHA: "bbb222", Branch: "b2", Worktree: "/w2", CreatedAt: time.Now().Add(-time.Hour)}
	if _, err := kept.Resolve(root, Resolution{Disposition: Retained, Reason: "accepted and unpublished"}); err != nil {
		t.Fatal(err)
	}

	list, err := List(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("listed %d candidates, want 2", len(list))
	}
	out := Render(list)
	if !strings.Contains(out, "Unresolved (1)") {
		t.Errorf("the listing does not separate undecided candidates:\n%s", out)
	}
	if !strings.Contains(out, "task-live") || !strings.Contains(out, "nobody has decided") {
		t.Errorf("an unresolved candidate is not named as one:\n%s", out)
	}
	if !strings.Contains(out, "retained: accepted and unpublished") {
		t.Errorf("a retained candidate does not say why it was kept:\n%s", out)
	}
}

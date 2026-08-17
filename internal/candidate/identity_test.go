package candidate

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

type fakeRepo struct {
	head  string
	clean bool
	err   error
}

func (f *fakeRepo) Head() (string, error)  { return f.head, f.err }
func (f *fakeRepo) IsClean() (bool, error) { return f.clean, f.err }

var when = time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)

// TestDirtyCanonicalCheckoutCannotSeedAGovernedCandidate covers "dirty
// canonical checkout cannot silently seed a governed candidate from a different
// state".
func TestDirtyCanonicalCheckoutCannotSeedAGovernedCandidate(t *testing.T) {
	root := t.TempDir()
	repo := &fakeRepo{head: "aaaaaaaaaaaa1111", clean: false}

	_, err := Establish(root, "task-1", "d", "/wt", "b", repo, when)
	if err == nil {
		t.Fatal("a dirty checkout seeded a governed candidate")
	}
	var dirty *ErrDirtyCanonical
	if !errors.As(err, &dirty) {
		t.Fatalf("want a dirty-checkout refusal, got %T: %v", err, err)
	}
	// The refusal must say what to do, not merely that something is wrong.
	if !strings.Contains(err.Error(), "stash") {
		t.Fatalf("refusal does not tell the human how to proceed: %v", err)
	}
	// And nothing may be recorded, or the next run would inherit a base that
	// was never legitimately established.
	if _, ok, _ := Load(root, "task-1"); ok {
		t.Fatal("a refused establishment still wrote an identity")
	}
}

// TestBaseSHAIsImmutableForACandidateLifecycle covers "base SHA is immutable
// for a candidate lifecycle" and "worker fallback starts from the same declared
// base".
func TestBaseSHAIsImmutableForACandidateLifecycle(t *testing.T) {
	root := t.TempDir()
	repo := &fakeRepo{head: "aaaaaaaaaaaa1111", clean: true}

	first, err := Establish(root, "task-1", "example.com/x", "/wt", "branch", repo, when)
	if err != nil {
		t.Fatal(err)
	}
	if first.BaseSHA != "aaaaaaaaaaaa1111" {
		t.Fatalf("base not recorded: %+v", first)
	}

	// A second worker taking over later re-enters with the same task. Even a
	// dirty tree must not change the answer: the base is already decided.
	repo.clean = false
	again, err := Establish(root, "task-1", "example.com/x", "/wt", "branch", repo, when.Add(time.Hour))
	if err != nil {
		t.Fatalf("a fallback worker could not reuse the established base: %v", err)
	}
	if again.BaseSHA != first.BaseSHA {
		t.Fatalf("base changed across workers: %s -> %s", first.BaseSHA, again.BaseSHA)
	}
	if !again.CreatedAt.Equal(first.CreatedAt) {
		t.Fatal("re-entry rewrote the creation time, so the identity is not immutable")
	}
}

// TestMovedHeadIsRefusedRatherThanInherited is the case that makes immutability
// mean something. If HEAD advanced while a task was interrupted, silently
// continuing would govern a state nobody planned against.
func TestMovedHeadIsRefusedRatherThanInherited(t *testing.T) {
	root := t.TempDir()
	repo := &fakeRepo{head: "aaaaaaaaaaaa1111", clean: true}
	if _, err := Establish(root, "task-1", "d", "/wt", "b", repo, when); err != nil {
		t.Fatal(err)
	}

	repo.head = "bbbbbbbbbbbb2222"
	got, err := Establish(root, "task-1", "d", "/wt", "b", repo, when)
	if err == nil {
		t.Fatal("a moved HEAD was silently inherited")
	}
	var moved *ErrBaseMoved
	if !errors.As(err, &moved) {
		t.Fatalf("want ErrBaseMoved, got %T: %v", err, err)
	}
	// The recorded identity is still returned so a caller can report both sides.
	if got.BaseSHA != "aaaaaaaaaaaa1111" {
		t.Fatalf("the recorded base was lost on refusal: %+v", got)
	}
	if !strings.Contains(err.Error(), "aaaaaaaaaaaa") || !strings.Contains(err.Error(), "bbbbbbbbbbbb") {
		t.Fatalf("refusal does not name both bases: %v", err)
	}
}

// TestIdentityNamesTheExactCandidateBasePair covers "audit evidence names the
// exact candidate/base pair".
func TestIdentityNamesTheExactCandidateBasePair(t *testing.T) {
	root := t.TempDir()
	repo := &fakeRepo{head: "abcdef0123456789", clean: true}
	id, err := Establish(root, "task-7", "example.com/x", "/wt/task-7", "sensei-code/task-7", repo, when)
	if err != nil {
		t.Fatal(err)
	}
	s := id.Summary()
	for _, want := range []string{"task-7", "sensei-code/task-7", "abcdef012345", "clean"} {
		if !strings.Contains(s, want) {
			t.Fatalf("summary %q does not name %q", s, want)
		}
	}
}

// TestGraphGenerationIsBoundToTheBase records which rules certified the base,
// so a later audit can tell whether it is judging by the same ones.
func TestGraphGenerationIsBoundToTheBase(t *testing.T) {
	root := t.TempDir()
	repo := &fakeRepo{head: "abcdef0123456789", clean: true}
	id, err := Establish(root, "task-1", "d", "/wt", "b", repo, when)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := id.BindGraph(root, "9723c9b177f1", "da512eb61c82")
	if err != nil {
		t.Fatal(err)
	}
	reloaded, ok, err := Load(root, "task-1")
	if err != nil || !ok {
		t.Fatalf("identity did not survive binding: %v", err)
	}
	if reloaded.GraphBuildCommit != "9723c9b177f1" || reloaded.SourceRepoCommit != "da512eb61c82" {
		t.Fatalf("graph generation not persisted: %+v", reloaded)
	}
	if reloaded.BaseSHA != bound.BaseSHA {
		t.Fatal("binding the graph changed the base")
	}
}

// TestUnreadableIdentityFailsClosed keeps a corrupt file from being treated as
// "no identity yet", which would re-establish a base and quietly discard the
// immutability guarantee.
func TestUnreadableIdentityFailsClosed(t *testing.T) {
	root := t.TempDir()
	repo := &fakeRepo{head: "aaaa", clean: true}
	if _, err := Establish(root, "task-1", "d", "/wt", "b", repo, when); err != nil {
		t.Fatal(err)
	}
	if err := writeCorrupt(path(root, "task-1")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(root, "task-1"); err == nil {
		t.Fatal("a corrupt identity file read as absent")
	}
	if _, err := Establish(root, "task-1", "d", "/wt", "b", repo, when); err == nil {
		t.Fatal("a corrupt identity silently re-established a new base")
	}
}

func writeCorrupt(p string) error {
	return os.WriteFile(p, []byte("{not json"), 0o644)
}

package governedfile

// ONE POSTURE, ONE DURABILITY, FOR EVERY GOVERNED RECORD.
//
// sensei-code#184 asks whether any store claims a stronger guarantee than the
// authority inputs it relies on. It did: the review record and the owner
// attestation replaced atomically at 0700/0600, while the review OBLIGATION --
// the single authority on what review is owed -- and the task state wrote
// straight onto the target, one of them at 0755/0644.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReplaceIsAtomicAndLeavesNoDebris(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "record.json")

	if err := Replace(path, []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "first\n" {
		t.Fatalf("read %q", got)
	}
	// A replacement never truncates in place: the target is only ever swapped.
	if err := Replace(path, []byte("second\n")); err != nil {
		t.Fatal(err)
	}
	if got := read(t, path); got != "second\n" {
		t.Fatalf("read %q", got)
	}
	// And nothing is left behind that a later reader could mistake for a record.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("a temp file survived the write: %s", e.Name())
		}
	}
}

func TestTheWholeGovernedChainSharesOnePosture(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "records", "one.json")
	if err := Replace(path, []byte("x")); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != FileMode {
		t.Fatalf("record mode %v, want %v", got, FileMode)
	}
	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != DirMode {
		t.Fatalf("directory mode %v, want %v", got, DirMode)
	}
	// Owner-only WRITE is the integrity boundary the repository relies on. A
	// record another local user could rewrite would let a governance decision
	// be made by somebody this process never authenticated.
	if FileMode&0o022 != 0 {
		t.Fatalf("a governed record is group/other writable (%v)", FileMode)
	}
	if DirMode&0o022 != 0 {
		t.Fatalf("a governed record directory is group/other writable (%v)", DirMode)
	}
}

// Create decides who got there first, and losing is not an error.
func TestCreateReportsTheRaceRatherThanOverwriting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "d", "rec.json")
	won, err := Create(path, []byte("original\n"))
	if err != nil || !won {
		t.Fatalf("first create: won=%v err=%v", won, err)
	}
	won, err = Create(path, []byte("replacement\n"))
	if err != nil {
		t.Fatalf("losing the race is not an error, got %v", err)
	}
	if won {
		t.Fatal("a second create claimed to have won")
	}
	if got := read(t, path); got != "original\n" {
		t.Fatalf("the loser overwrote the winner: %q", got)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

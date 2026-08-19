package investigate

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The boundary is an allowlist, not a promise. A conversational turn may look
// and may never change, and that has to be true of the code rather than of the
// prompt asking the architect nicely.
func TestOnlyReadOnlySubcommandsAreReachable(t *testing.T) {
	for _, allowed := range []string{"status", "log", "diff", "show", "rev-parse", "blame"} {
		if !Allowed(allowed) {
			t.Errorf("%s should be readable", allowed)
		}
	}
	for _, refused := range []string{
		"commit", "push", "merge", "rebase", "reset", "checkout", "clean",
		"worktree", "branch -D", "cherry-pick", "apply", "am", "gc", "config",
	} {
		if Allowed(refused) {
			t.Errorf("git %s is reachable from a read-only investigation surface", refused)
		}
	}

	r := Repository{Root: t.TempDir()}
	_, err := r.Run(context.Background(), "commit", "-m", "nope")
	var notReadOnly *ErrNotReadOnly
	if !errors.As(err, &notReadOnly) {
		t.Fatalf("a mutating subcommand was not refused by type: %v", err)
	}
	if !strings.Contains(err.Error(), "never change") {
		t.Errorf("the refusal does not say what the boundary is: %v", err)
	}
}

// What cannot be read is stated. A blank field reads as "nothing to report",
// which is the one thing it must never mean.
func TestUnreadableEvidenceIsStatedNotBlank(t *testing.T) {
	// A directory that is not a repository: every read fails, and the evidence
	// must say so rather than describing an empty clean repository.
	ev := Repository{Root: t.TempDir()}.Gather(context.Background(), nil, 3)
	if len(ev.Unavailable) == 0 {
		t.Fatal("a non-repository produced no unavailability, so it reads as clean and empty")
	}
	out := ev.Render()
	if !strings.Contains(out, "could not be read") {
		t.Errorf("the rendering hides what it failed to read:\n%s", out)
	}
	if !strings.Contains(out, "(unknown)") {
		t.Errorf("an unknown head was rendered as though it were known:\n%s", out)
	}
}

// The surfaces are enumerable, so a person can ask what this can see without
// reading the source.
func TestSurfacesAreEnumerable(t *testing.T) {
	got := Surfaces()
	if len(got) == 0 {
		t.Fatal("the investigation surface cannot describe itself")
	}
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("surfaces are not stably ordered: %v", got)
		}
	}
}

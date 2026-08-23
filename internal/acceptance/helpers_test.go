//go:build acceptance || stubsmoke

// Helpers shared by the live canary and the deterministic tripwire.
//
// They live here, under both build tags, because the two runs assert the same
// chain and diverge only in who plays the providers. Duplicating them per tag
// is how the two accounts of "which phases were reached" start disagreeing.
package acceptance

import (
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/acceptance -> repository root
	return strings.TrimSuffix(wd, "/internal/acceptance")
}

func oneLine(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " · "))
	// Deliberately generous. The first run truncated a diff audit at 160
	// characters and cut off the limitations, which were the only part that
	// said why the audit could not be performed -- so the log recorded that
	// something went wrong and hid what.
	if len(s) > 1200 {
		return s[:1200] + "…"
	}
	return s
}

func kinds(seen map[event.Kind]bool) []string {
	var out []string
	for k := range seen {
		out = append(out, string(k))
	}
	return out
}

// assertCutFromRecordedBase proves the worktree actually derives from the
// commit the identity records, and that the workflow's own governance writes
// did not leak into the change under review.
//
// Recording a base and cutting from it are separate facts. A run could pin one
// commit and create the worktree from another — from HEAD, say — and every
// receipt would still name the pinned one. The only way to know is to ask git
// what the worktree descends from.
func assertCutFromRecordedBase(t *testing.T, root, worktree, taskID string) {
	t.Helper()
	id, ok, err := candidate.Load(root, taskID)
	if err != nil || !ok {
		t.Fatalf("no candidate identity recorded for %s: ok=%v err=%v", taskID, ok, err)
	}
	if strings.TrimSpace(id.BaseSHA) == "" {
		t.Fatal("candidate identity records no base commit")
	}

	head, err := exec.Command("git", "-C", worktree, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("read candidate HEAD: %v", err)
	}
	tip := strings.TrimSpace(string(head))

	// Either the worker committed nothing, in which case the worktree still
	// sits exactly on the base, or it committed and the base must be an
	// ancestor. Anything else means the candidate was cut from somewhere else.
	if tip != id.BaseSHA {
		if err := exec.Command("git", "-C", worktree, "merge-base", "--is-ancestor", id.BaseSHA, tip).Run(); err != nil {
			t.Fatalf("candidate was not cut from the recorded base: recorded %s, worktree tip %s", id.BaseSHA, tip)
		}
	}
	t.Logf("candidate cut from recorded base %s (worktree tip %s)", short(id.BaseSHA), short(tip))

	// The resolution this run wrote into the awareness corpus is a governance
	// side effect, not part of the change being reviewed. If it appears in the
	// candidate diff, the run is proposing its own paperwork as work.
	diff, err := exec.Command("git", "-C", worktree, "diff", id.BaseSHA).Output()
	if err != nil {
		t.Fatalf("read candidate diff: %v", err)
	}
	if strings.Contains(string(diff), "candidates/proposals/") {
		t.Error("the candidate diff contains this run's own governance proposal; a decision record is a side effect, not part of the change")
	}
}

func short(sha string) string {
	if len(sha) > 12 {
		return sha[:12]
	}
	return sha
}

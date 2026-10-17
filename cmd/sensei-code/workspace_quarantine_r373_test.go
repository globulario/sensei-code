package main

// RULING-373 §16 TUI-B, the CLI half of the W-TUI-1 pre-fix control: the
// planned task whose candidate record is readable and settled but whose lock
// object is invalid is refused by the CLI as CANNOT ESTABLISH CONTAINMENT
// STATE, while Engine.ResumeTask -- the TUI's /resume -- continued it at
// a7b41bd (internal/workflow/workspace_quarantine_r373_test.go TUI-A). This
// file uses only API that exists at a7b41bd.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

// r373SettledInvalidLock records a settled candidate for taskID and then
// replaces its lock object with a symlink: an unlocked read sees a settled
// record, and the locked read fails.
func r373SettledInvalidLock(t *testing.T, root, taskID string) {
	t.Helper()
	wqSettledCandidate(t, root, taskID)
	lock := wqRecordPath(root, taskID) + ".lock"
	if err := os.Remove(lock); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
		t.Fatal(err)
	}
}

// r373PlannedHistory is a planned task's session history: its root and its
// recorded plan, no standing question.
func r373PlannedHistory(holder, taskID string) []event.Event {
	return []event.Event{
		event.New(holder, taskID, event.SourceSystem, event.TaskCreated, "the objective", nil),
		event.New(holder, taskID, event.SourceArchitect, event.PlanProposed, "the bounded plan",
			map[string]string{"decision": "proceed", "summary": "the bounded plan", "plan": "the bounded plan",
				"plan_source": "architect"}),
	}
}

// TUI-B. The CLI refuses the W-TUI-1 specimen: --list offers no continuation
// and --task refuses on containment.
func TestR373TUIBTheCLIRefusesThePlannedSettledInvalidLockSpecimen(t *testing.T) {
	root := gitRepoRoot(t)
	const holder, taskID = "session-20260129T000000.000000000Z", "t-planned"
	r373SettledInvalidLock(t, root, taskID)
	writeRawSession(t, root, holder, r373PlannedHistory(holder, taskID)...)
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if b := wqBlock(stdout, taskID); !strings.Contains(b, wqUnknownText) || strings.Contains(b, "resume     --task") ||
		strings.Contains(b, "--answer") || strings.Contains(b, "cannot be read as its own session lineage") {
		t.Errorf("TUI-B: --list does not show CANNOT ESTABLISH without an offer:\n%s", stdout)
	}
	code, _, stderr = runResume(t, root, "--task", taskID, "--quiet")
	if code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
		t.Errorf("TUI-B: --task was not refused as CANNOT ESTABLISH: exit %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, "continues as session") {
		t.Errorf("TUI-B: --task bound a session lineage:\n%s", stderr)
	}
}

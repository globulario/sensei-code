package main

// RULING-367 §7-§10 (N1, CLI half). On a platform with no cross-process lock
// the candidate record cannot be read under its lock. An ordinary unlocked
// read may keep legacy visibility, and it is never evidence that a protected
// invocation completed: an unsettled record is refused as owed settlement, an
// unreadable one as CANNOT ESTABLISH, and only a readable settled (or absent)
// record keeps the ordinary legacy behaviour. Linux has the lock, so the
// lockless platform is injected through the locked-read seam.

import (
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
)

func r367NoLock(t *testing.T) {
	t.Helper()
	saved := loadLockedRecord
	t.Cleanup(func() { loadLockedRecord = saved })
	loadLockedRecord = func(string, string) (candidate.Identity, bool, error) {
		return candidate.Identity{}, false, candidate.ErrNoCrossProcessLock
	}
}

// N1-W3. An existing durable Quarantined record -- and an execution recorded
// in flight -- stays non-continuable on a lockless platform.
func TestR367N1W3AQuarantineStaysNonContinuableWithoutTheLock(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260121T000000.000000000Z"
	wqCandidate(t, root, "t-q", true)
	wqCandidate(t, root, "t-flight", false)
	writeRawSession(t, root, holder,
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "quarantined work", nil),
		event.New(holder, "t-flight", event.SourceSystem, event.TaskCreated, "work in flight", nil))
	r367NoLock(t)
	for _, task := range []string{"t-q", "t-flight"} {
		if code, _, stderr := runResume(t, root, "--task", task, "--quiet"); code != exitFailed || !strings.Contains(stderr, wqLaneText) {
			t.Errorf("%s was not refused as owed settlement without the lock: exit %d\n%s", task, code, stderr)
		}
	}
	_, stdout, _ := runResume(t, root, "--list")
	for _, task := range []string{"t-q", "t-flight"} {
		if b := wqBlock(stdout, task); !strings.Contains(b, wqLaneText) || strings.Contains(b, "resume     --task") {
			t.Errorf("%s is not listed as owed settlement without the lock:\n%s", task, stdout)
		}
	}
}

// N1-W4. An unreadable record is never treated as settled without the lock.
func TestR367N1W4AnUnreadableRecordIsNotSettledWithoutTheLock(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260122T000000.000000000Z"
	wqSettledCandidate(t, root, "t-bad")
	if err := os.WriteFile(wqRecordPath(root, "t-bad"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeRawSession(t, root, holder,
		event.New(holder, "t-bad", event.SourceSystem, event.TaskCreated, "unknown containment", nil))
	r367NoLock(t)
	if code, _, stderr := runResume(t, root, "--task", "t-bad", "--quiet"); code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
		t.Fatalf("an unreadable record was not refused as unknown without the lock: exit %d\n%s", code, stderr)
	}
	_, stdout, _ := runResume(t, root, "--list")
	if b := wqBlock(stdout, "t-bad"); !strings.Contains(b, wqUnknownText) || strings.Contains(b, "resume     --task") {
		t.Fatalf("an unreadable record was listed as settled without the lock:\n%s", stdout)
	}
}

// N1-W5. Legacy/default-off: a readable settled record, and a task with no
// record at all, keep their ordinary behaviour without the lock.
func TestR367N1W5LegacyResumeKeepsItsBehaviourWithoutTheLock(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260123T000000.000000000Z"
	wqSettledCandidate(t, root, "t-ok")
	writeRawSession(t, root, holder,
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "settled work", nil),
		event.New(holder, "t-legacy", event.SourceSystem, event.TaskCreated, "legacy work", nil))
	r367NoLock(t)
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed without the lock: exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, task := range []string{"t-ok", "t-legacy"} {
		if b := wqBlock(stdout, task); !strings.Contains(b, "resume     --task "+task) || strings.Contains(b, wqLaneText) || strings.Contains(b, wqUnknownText) {
			t.Errorf("%s lost its ordinary behaviour without the lock:\n%s", task, stdout)
		}
	}
}

package main

// RULING-370 finding A, CLI half (§2-§3, §6-§8, §10-§11). The filename task id
// is the identity of the candidate record; an embedded task_id that is
// missing, empty or different is a contradiction the canonical reader
// rejects. The resume surface then reads it as it reads any unreadable record:
// CANNOT ESTABLISH CONTAINMENT STATE under the filename identity, refused
// before any session, lineage or answer -- never "no record", never ordinary
// work, and the embedded id never owns the filename's record. A1-A3 are the
// controls (they pass a contradiction through on efa1538); A8-A11 and the CLI
// halves of A12-A16 are the witnesses.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// r370Contradict writes task's record as one claiming claim (no task_id at
// all when omit), with a durable in-flight execution -- the state that must
// never read as absent -- and returns its bytes.
func r370Contradict(t *testing.T, root, task, claim string, omit bool) []byte {
	t.Helper()
	id := candidate.Identity{TaskID: claim, Repository: root, BaseSHA: strings.Repeat("a", 40),
		Worktree: filepath.Join(root, ".worktrees", task), CreatedAt: time.Now().UTC(),
		InFlight: &candidate.InFlight{Invocation: wqInvocation, Phase: "certify", Workspace: filepath.Join(root, ".worktrees", task)}}
	body, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if omit {
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "task_id")
		if body, err = json.Marshal(m); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(wqRecordPath(root, task)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wqRecordPath(root, task), body, 0o600); err != nil {
		t.Fatal(err)
	}
	return body
}

// r370After checks A15 and A16 after a command: the contradictory record is
// byte for byte what it was, and no record of the claimed task was invented.
func r370After(t *testing.T, root string, record []byte, what string) {
	t.Helper()
	if string(wqRead(t, wqRecordPath(root, "task-1"))) != string(record) {
		t.Errorf("A15: %s changed the contradictory record", what)
	}
	if _, err := os.Stat(wqRecordPath(root, "task-2")); !os.IsNotExist(err) {
		t.Errorf("A16: %s left a task-2 record because task-1.json claimed task-2: %v", what, err)
	}
}

// A1 A9. A record of task-1 claiming task-2, with a durable in-flight
// execution, is refused at the first gate: `resume --task task-1` is CANNOT
// ESTABLISH, before any session, lineage or readiness, and changes nothing.
func TestR370A1AContradictoryRecordIsRefusedAtTheFirstGate(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260131T000000.000000000Z"
	record := r370Contradict(t, root, "task-1", "task-2", false)
	writeRawSession(t, root, holder, event.New(holder, "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil))
	ledger, sessions := wqRead(t, wqLedger(root, holder)), wqSessions(t, root)

	code, _, stderr := runResume(t, root, "--task", "task-1", "--quiet")
	if code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
		t.Fatalf("A1: the contradictory record of task-1 was not refused as CANNOT ESTABLISH: exit %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, "not ready") || strings.Contains(stderr, "continues as session") {
		t.Errorf("A9: readiness or a session binding was reached before the refusal:\n%s", stderr)
	}
	after := wqRead(t, wqLedger(root, holder))
	if string(after) != string(ledger) || strings.Contains(string(after), string(event.SessionLineageBound)) {
		t.Error("A9: the refusal appended to the task's history")
	}
	if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
		t.Errorf("A9: a session was created for a refused resume: %v -> %v", sessions, got)
	}
	r370After(t, root, record, "resume --task")
}

// A2 A8. A record of task-1 with no (or an empty) task_id is not absent and
// not unattributed: the listing shows task-1 CANNOT ESTABLISH, never as
// ordinary work, and --task is refused the same way.
func TestR370A2AMissingIdentityIsNotTreatedAsAbsent(t *testing.T) {
	for name, omit := range map[string]bool{"no task_id": true, "an empty task_id": false} {
		t.Run(name, func(t *testing.T) {
			root := gitRepoRoot(t)
			const holder = "session-20260201T000000.000000000Z"
			record := r370Contradict(t, root, "task-1", "", omit)
			writeRawSession(t, root, holder, event.New(holder, "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil))

			code, stdout, stderr := runResume(t, root, "--list")
			if code != exitCompleted {
				t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
			}
			if b := wqBlock(stdout, "task-1"); !strings.Contains(b, wqUnknownText) || strings.Contains(b, "resume     --task") {
				t.Errorf("A2: task-1 with %s is not listed as CANNOT ESTABLISH:\n%s", name, stdout)
			}
			if code, _, stderr := runResume(t, root, "--task", "task-1", "--quiet"); code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
				t.Errorf("A2: --task over a record with %s was not refused as unknown: exit %d\n%s", name, code, stderr)
			}
			if string(wqRead(t, wqRecordPath(root, "task-1"))) != string(record) {
				t.Error("A15: resume changed the record")
			}
		})
	}
}

// A3 A11. With no session history at all, a contradictory record -- missing
// identity or another task's -- keeps its obligation: it is listed from the
// candidate records under task-1 and naming task-1 is refused as unknown.
func TestR370A3AnOrphanedContradictionKeepsItsObligation(t *testing.T) {
	for name, c := range map[string]struct {
		claim string
		omit  bool
	}{"no task_id": {"", true}, "another task's id": {"task-2", false}} {
		t.Run(name, func(t *testing.T) {
			root := gitRepoRoot(t)
			record := r370Contradict(t, root, "task-1", c.claim, c.omit)
			code, stdout, stderr := runResume(t, root, "--list")
			if code != exitCompleted {
				t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
			}
			if b := wqBlock(stdout, "task-1"); !strings.Contains(b, wqUnknownText) {
				t.Errorf("A3/A11: the orphaned contradictory record of task-1 is not discoverable:\n%s", stdout)
			}
			if strings.Contains(stdout, "task task-2  ") || strings.Contains(stdout, "task   ") {
				t.Errorf("A3/A11: the record is listed under its embedded identity:\n%s", stdout)
			}
			if code, _, stderr := runResume(t, root, "--task", "task-1", "--quiet"); code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
				t.Errorf("A11: --task over the orphaned contradiction was not refused as unknown: exit %d\n%s", code, stderr)
			}
			r370After(t, root, record, "resume over an orphan")
		})
	}
}

// A8 A16. The listing shows task-1 CANNOT ESTABLISH and does not let the
// embedded identity own the record: no task-2 block, no task-2 obligation.
func TestR370A8TheListingShowsTheFilenameIdentity(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260202T000000.000000000Z"
	record := r370Contradict(t, root, "task-1", "task-2", false)
	writeRawSession(t, root, holder, event.New(holder, "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil),
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if b := wqBlock(stdout, "task-1"); !strings.Contains(b, wqUnknownText) || strings.Contains(b, "resume     --task") {
		t.Errorf("A8: task-1 is not listed as CANNOT ESTABLISH:\n%s", stdout)
	}
	if strings.Contains(stdout, "task task-2  ") {
		t.Errorf("A8: the embedded identity owns a listing block:\n%s", stdout)
	}
	if !strings.Contains(stdout, "resume     --task t-ok") {
		t.Errorf("the contradiction hid another task:\n%s", stdout)
	}
	r370After(t, root, record, "resume --list")
}

// A10. --answer is gated exactly like --task: refused before any answer is
// consumed; the question still stands, byte for byte.
func TestR370A10AnAnswerIsRefusedBeforeItIsConsumed(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260203T000000.000000000Z"
	record := r370Contradict(t, root, "task-1", "task-2", false)
	writeRawSession(t, root, holder, event.New(holder, "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, "task-1"))
	before, err := session.FindActive(root)
	if err != nil {
		t.Fatal(err)
	}
	held, ok := activeTask(before.Active, "task-1")
	if !ok || len(held.Task.AwaitingAuthority) == 0 {
		t.Fatalf("premise: the question stands: %+v", before.Active)
	}
	ledger, sessions := wqRead(t, wqLedger(root, holder)), wqSessions(t, root)

	code, _, stderr := runResume(t, root, "--task", "task-1", "--answer", "1", "--quiet")
	if code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
		t.Fatalf("A10: an answer was not refused over the contradictory record: exit %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, "answered") || strings.Contains(stderr, "not ready") {
		t.Errorf("A10: the answer path went further than the refusal:\n%s", stderr)
	}
	if string(wqRead(t, wqLedger(root, holder))) != string(ledger) {
		t.Error("A10: the refused answer appended to the task's history")
	}
	if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
		t.Errorf("A10: a session was created for a refused answer: %v -> %v", sessions, got)
	}
	after, err := session.FindActive(root)
	if err != nil {
		t.Fatal(err)
	}
	if still, ok := activeTask(after.Active, "task-1"); !ok || string(still.Task.AwaitingAuthority) != string(held.Task.AwaitingAuthority) {
		t.Fatal("A10: the standing question did not survive the refused answer")
	}
	r370After(t, root, record, "resume --answer")
}

// A12 A13. A record whose embedded identity matches keeps its ordinary
// behaviour (settled: ordinary work; in flight: owed settlement), and a task
// with no record stays absent and ordinary; no read creates a record.
func TestR370A12MatchingAndAbsentRecordsKeepTheirBehaviour(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260204T000000.000000000Z"
	wqSettledCandidate(t, root, "t-ok")
	wqCandidate(t, root, "t-q", false)
	writeRawSession(t, root, holder,
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "settled work", nil),
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "work in flight", nil),
		event.New(holder, "t-none", event.SourceSystem, event.TaskCreated, "work with no record", nil))
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, task := range []string{"t-ok", "t-none"} {
		if b := wqBlock(stdout, task); !strings.Contains(b, "resume     --task "+task) || strings.Contains(b, wqUnknownText) {
			t.Errorf("A12/A13: %s lost its ordinary behaviour:\n%s", task, stdout)
		}
	}
	if b := wqBlock(stdout, "t-q"); !strings.Contains(b, wqLaneText) {
		t.Errorf("A12: a matching in-flight record is not owed settlement:\n%s", stdout)
	}
	if _, err := os.Stat(wqRecordPath(root, "t-none")); !os.IsNotExist(err) {
		t.Errorf("A13: listing created a record for a task with none: %v", err)
	}
}

// A14. On a lockless platform the unlocked fallback read applies the same
// identity check: the contradiction is CANNOT ESTABLISH, never settled.
func TestR370A14ALocklessPlatformRefusesTheContradiction(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260205T000000.000000000Z"
	record := r370Contradict(t, root, "task-1", "task-2", false)
	writeRawSession(t, root, holder, event.New(holder, "task-1", event.SourceSystem, event.TaskCreated, "the objective", nil))
	r367NoLock(t)
	if code, _, stderr := runResume(t, root, "--task", "task-1", "--quiet"); code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
		t.Fatalf("A14: the contradiction was not refused as unknown without the lock: exit %d\n%s", code, stderr)
	}
	_, stdout, _ := runResume(t, root, "--list")
	if b := wqBlock(stdout, "task-1"); !strings.Contains(b, wqUnknownText) || strings.Contains(b, "resume     --task") {
		t.Fatalf("A14: the contradiction was listed as settled without the lock:\n%s", stdout)
	}
	r370After(t, root, record, "the lockless resume")
}

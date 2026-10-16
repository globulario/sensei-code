package main

// RULING-371 §14 (mandatory cross-projection check) and C17. After an
// UNVERIFIED WORKSPACE_QUARANTINED terminal -- the candidate record could not
// be read under its lock -- restart discovery must not treat that uncertainty
// as permission to resume: the task is not offered for continuation by
// `resume --list`, and `resume --task` and `resume --task --answer` refuse it
// before anything continues it. A: the record exists but is unreadable under
// its lock; B: it exists with a contradictory embedded identity; C: its lock
// cannot be acquired AND the record is absent. Test-only: nothing here changes
// how resume decides.
//
// RULING-372 extends this file. §12 is the locked-absence witness, §13 the
// invalid-lock witness (the §14-C specimen), and §16-§17 cover the CLI half of
// the lockless platform (L1-L6, injected through the loadLockedRecord seam).
// §23 is the cross-projection matrix, whose engine half is in
// internal/workflow/workspace_quarantine_r372_test.go (L8: one status per
// specimen). §8 and M-R4 are covered by the containment refusal preceding the
// objective check and the resume identity.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

// r371Unverified is the UNVERIFIED terminal the engine records when the locked
// read failed: the task and the read failure, nothing else.
func r371Unverified(sessionID, taskID, readFailure string) event.Event {
	return event.New(sessionID, taskID, event.SourceSystem, event.WorkflowWorkspaceQuarantined,
		"the invocation ended on the worker died and the candidate record could not be read under its lock",
		map[string]any{"task_id": taskID, "recorded": false,
			"reason": "the candidate record of " + taskID + " could not be read under its lock, so its containment state is not established: " + readFailure})
}

func TestR371S14RestartDiscoveryDoesNotResumeAnUnverifiedQuarantine(t *testing.T) {
	for name, c := range map[string]struct {
		readFailure string
		damage      func(t *testing.T, root, taskID string)
	}{
		"A record unreadable under its lock": {"candidate identity is unreadable", func(t *testing.T, root, taskID string) {
			if err := os.MkdirAll(filepath.Dir(wqRecordPath(root, taskID)), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(wqRecordPath(root, taskID), []byte("{torn"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		"B record with a contradictory embedded identity": {"embedded task identity contradicts", func(t *testing.T, root, taskID string) {
			if err := os.MkdirAll(filepath.Dir(wqRecordPath(root, taskID)), 0o755); err != nil {
				t.Fatal(err)
			}
			body := `{"task_id":"t-other","repository":"` + root + `","base_sha":"` + strings.Repeat("a", 40) + `"}`
			if err := os.WriteFile(wqRecordPath(root, taskID), []byte(body), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		"C lock unacquirable and record absent": {"the candidate record lock is not a regular file", func(t *testing.T, root, taskID string) {
			lock := wqRecordPath(root, taskID) + ".lock"
			if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(wqRecordPath(root, taskID)); !os.IsNotExist(err) {
				t.Fatalf("premise: the record is not absent: %v", err)
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			root := gitRepoRoot(t)
			const holder, taskID = "session-20260120T000000.000000000Z", "t-u"
			c.damage(t, root, taskID)
			writeRawSession(t, root, holder,
				event.New(holder, taskID, event.SourceSystem, event.TaskCreated, "the objective", nil),
				b2a2Deferred(holder, taskID),
				r371Unverified(holder, taskID, c.readFailure))
			ledger, sessions := wqRead(t, wqLedger(root, holder)), wqSessions(t, root)

			code, stdout, stderr := runResume(t, root, "--list")
			if code != exitCompleted {
				t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
			}
			block := wqBlock(stdout, taskID)
			if block == "" {
				t.Errorf("--list hides the task:\n%s", stdout)
			}
			if strings.Contains(block, "resume     --task "+taskID) || strings.Contains(block, "--answer") {
				t.Errorf("--list offers the task for continuation after an unverified quarantine:\n%s", block)
			}
			for _, args := range [][]string{{"--task", taskID, "--quiet"}, {"--task", taskID, "--answer", "1", "--quiet"}} {
				code, _, stderr := runResume(t, root, args...)
				refused := strings.Contains(stderr, wqLaneText) || strings.Contains(stderr, wqUnknownText)
				if code != exitFailed || !refused {
					t.Errorf("resume %v was not refused on containment: exit %d\n%s", args, code, stderr)
				}
				if strings.Contains(stderr, "not ready") || strings.Contains(stderr, "answered") ||
					strings.Contains(stderr, "continues as session") {
					t.Errorf("resume %v went past the containment gate:\n%s", args, stderr)
				}
			}
			after := wqRead(t, wqLedger(root, holder))
			if string(after) != string(ledger) {
				t.Error("restart discovery appended to the task's history")
			}
			if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
				t.Errorf("a session was created: %v -> %v", sessions, got)
			}
		})
	}
}

// C17. The headless consumer ends on the unverified quarantine as it ends on
// the verified one: exit 11, final, waiting for nothing else.
func TestR371C17TheHeadlessExitIsWorkspaceQuarantined(t *testing.T) {
	ev := r371Unverified("s", "t-u", "x")
	for _, deferred := range []bool{false, true} {
		if code, final := exitFor(ev.Kind, deferred); code != 11 || !final {
			t.Fatalf("the unverified quarantine exits %d (final %v), want 11", code, final)
		}
	}
}

// r372InvalidLock leaves taskID with no candidate JSON and a lock object that
// is not a regular file: the locked read fails, and the inventory
// (candidate.List) has no entry for the task.
func r372InvalidLock(t *testing.T, root, taskID string) {
	t.Helper()
	lock := wqRecordPath(root, taskID) + ".lock"
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
		t.Fatal(err)
	}
}

// r372Refused asserts the containment refusal of --task and --task --answer:
// failed, naming want, and stopped before readiness, an answer, or a
// continuation.
func r372Refused(t *testing.T, root, taskID, want string) {
	t.Helper()
	for _, args := range [][]string{{"--task", taskID, "--quiet"}, {"--task", taskID, "--answer", "1", "--quiet"}} {
		code, _, stderr := runResume(t, root, args...)
		if code != exitFailed || !strings.Contains(stderr, want) {
			t.Errorf("resume %v was not refused with %q: exit %d\n%s", args, want, code, stderr)
		}
		if strings.Contains(stderr, "not ready") || strings.Contains(stderr, "answered") ||
			strings.Contains(stderr, "continues as session") || strings.Contains(stderr, "human-owned decision") {
			t.Errorf("resume %v went past the containment gate:\n%s", args, stderr)
		}
	}
}

// r372Allowed asserts that --task and --task --answer pass the containment
// gate. Each reaches the next gate (the standing decision, readiness), and
// neither says anything about containment.
func r372Allowed(t *testing.T, root, taskID string) {
	t.Helper()
	for _, args := range [][]string{{"--task", taskID, "--quiet"}, {"--task", taskID, "--answer", "1", "--quiet"}} {
		_, _, stderr := runResume(t, root, args...)
		if strings.Contains(stderr, wqLaneText) || strings.Contains(stderr, wqUnknownText) {
			t.Errorf("resume %v was refused on containment:\n%s", args, stderr)
		}
	}
}

// §12. The locked-absence witness: a session task, no candidate JSON, and a
// lock path that can be created. The locked read succeeds with ok=false, which
// is authoritative absence, so the ordinary lane is allowed. The read's
// documented metadata side effect is the candidates directory and this task's
// .lock. It creates nothing else: no candidate JSON, no in-flight state, no
// disposition, and no workspace.
func TestR372S12ALockedAbsenceAllowsTheOrdinaryLane(t *testing.T) {
	root := gitRepoRoot(t)
	const holder, taskID = "session-20260124T000000.000000000Z", "t-absent"
	writeRawSession(t, root, holder,
		event.New(holder, taskID, event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, taskID))
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if b := wqBlock(stdout, taskID); !strings.Contains(b, "--answer 1") || strings.Contains(b, wqLaneText) ||
		strings.Contains(b, wqUnknownText) {
		t.Errorf("an authoritatively absent record did not keep the ordinary lane:\n%s", stdout)
	}
	r372Allowed(t, root, taskID)
	entries, err := os.ReadDir(filepath.Dir(wqRecordPath(root, taskID)))
	if err != nil {
		t.Fatalf("the locked read left no candidates directory: %v", err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	if strings.Join(names, ",") != taskID+".json.lock" {
		t.Errorf("the locked read created more than the task's lock: %v", names)
	}
	if _, err := os.Stat(filepath.Join(root, ".worktrees")); !os.IsNotExist(err) {
		t.Errorf("a workspace was created: %v", err)
	}
}

// §13. The invalid-lock witness (the §14-C specimen): session history, an
// unanswered question, an unverified quarantine terminal, no candidate JSON,
// and an invalid lock object. The locked read fails, so containment CANNOT BE
// ESTABLISHED. --list does not offer --answer or --task. Both --task and
// --answer refuse on containment. No session lineage is minted, the standing
// question stays intact, and the lock object is not replaced.
func TestR372S13AnInvalidLockCannotEstablishContainment(t *testing.T) {
	root := gitRepoRoot(t)
	const holder, taskID = "session-20260125T000000.000000000Z", "t-lock"
	r372InvalidLock(t, root, taskID)
	writeRawSession(t, root, holder,
		event.New(holder, taskID, event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, taskID),
		r371Unverified(holder, taskID, "the candidate record lock is not a regular file"))
	ledger, sessions := wqRead(t, wqLedger(root, holder)), wqSessions(t, root)

	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	b := wqBlock(stdout, taskID)
	if !strings.Contains(b, wqUnknownText) || !strings.Contains(b, "preserved behind this obligation") {
		t.Errorf("--list does not show CANNOT ESTABLISH with the question preserved behind it:\n%s", b)
	}
	if strings.Contains(b, "--answer") || strings.Contains(b, "resume     --task") {
		t.Errorf("--list offers the task for continuation:\n%s", b)
	}
	r372Refused(t, root, taskID, wqUnknownText)
	if after := wqRead(t, wqLedger(root, holder)); string(after) != string(ledger) {
		t.Error("the standing question's history was appended to")
	}
	if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
		t.Errorf("a session lineage was minted: %v -> %v", sessions, got)
	}
	if fi, err := os.Lstat(wqRecordPath(root, taskID) + ".lock"); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the invalid lock object was replaced: %v %v", fi, err)
	}
	if _, err := os.Stat(wqRecordPath(root, taskID)); !os.IsNotExist(err) {
		t.Errorf("a candidate JSON was created: %v", err)
	}
}

// §8 and M-R4. The containment refusal precedes the objective check and the
// resume identity. A task that recorded no objective, whose containment cannot
// be established, is refused on containment, not as an unusable objective.
func TestR372S8TheContainmentRefusalPrecedesTheObjectiveCheck(t *testing.T) {
	root := gitRepoRoot(t)
	const holder, taskID = "session-20260126T000000.000000000Z", "t-noobj"
	r372InvalidLock(t, root, taskID)
	writeRawSession(t, root, holder,
		event.New(holder, taskID, event.SourceSystem, event.TaskCreated, "", nil),
		b2a2Deferred(holder, taskID))
	r372Refused(t, root, taskID, wqUnknownText)
	for _, args := range [][]string{{"--task", taskID, "--quiet"}, {"--task", taskID, "--answer", "1", "--quiet"}} {
		if _, _, stderr := runResume(t, root, args...); strings.Contains(stderr, "recorded no objective") {
			t.Errorf("resume %v reached the objective check before the containment refusal:\n%s", args, stderr)
		}
	}
}

// §9. One unreadable record hides nothing else. The listing completes, the
// task whose read failed is shown without an offer, and a task beside it whose
// read established absence keeps its question offered.
func TestR372S9OneFailedReadHidesNothingElse(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260127T000000.000000000Z"
	r372InvalidLock(t, root, "t-bad")
	writeRawSession(t, root, holder,
		event.New(holder, "t-bad", event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, "t-bad"),
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "other work", nil),
		b2a2Deferred(holder, "t-ok"))
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("one failed read failed the whole listing: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if b := wqBlock(stdout, "t-bad"); !strings.Contains(b, wqUnknownText) || strings.Contains(b, "--answer") {
		t.Errorf("t-bad is not shown as CANNOT ESTABLISH:\n%s", stdout)
	}
	if b := wqBlock(stdout, "t-ok"); !strings.Contains(b, "--answer 1") {
		t.Errorf("t-ok was hidden by t-bad's failed read:\n%s", stdout)
	}
}

// r372CLIStatus is the containment status the CLI projections report.
type r372CLIStatus string

const (
	r372CLIOrdinary    r372CLIStatus = "ordinary"
	r372CLIQuarantined r372CLIStatus = "refused: owed settlement"
	r372CLIUnknown     r372CLIStatus = "refused: cannot establish"
)

// §23 and L1-L6, L8. The CLI half of the cross-projection matrix. Each row is
// one specimen. Its engine terminal is the one the engine half
// (TestR372S23TheEngineMatrix) records over the same specimen, and its
// headless exit is that terminal's (exitFor). Every projection must describe
// the same status: --list offers the question only when --task and --answer
// pass the gate, and that happens only when the engine kept the ordinary
// terminal (headless exit not 11). A refusal never becomes an ordinary resume
// because the fold also holds a human question. Rows G-L simulate the
// lockless platform through the loadLockedRecord seam, where L1-L4 are G-I
// and L5-L6 are J-K.
func TestR372S23L8TheCLIMatrixAgreesWithTheEngine(t *testing.T) {
	const taskID = "t-u"
	torn := func(t *testing.T, root string) {
		if err := os.MkdirAll(filepath.Dir(wqRecordPath(root, taskID)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(wqRecordPath(root, taskID), []byte("{torn"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	contradictory := func(t *testing.T, root string) {
		if err := os.MkdirAll(filepath.Dir(wqRecordPath(root, taskID)), 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"task_id":"t-other","repository":"` + root + `","base_sha":"` + strings.Repeat("a", 40) + `"}`
		if err := os.WriteFile(wqRecordPath(root, taskID), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	none := func(*testing.T, string) {}
	settled := func(t *testing.T, root string) { wqSettledCandidate(t, root, taskID) }
	unsettled := func(t *testing.T, root string) { wqCandidate(t, root, taskID, false) }
	rows := []struct {
		row      string
		setup    func(t *testing.T, root string)
		lockless bool
		want     r372CLIStatus
		terminal event.Kind
	}{
		{"A torn record", torn, false, r372CLIUnknown, event.WorkflowWorkspaceQuarantined},
		{"B contradictory embedded identity", contradictory, false, r372CLIUnknown, event.WorkflowWorkspaceQuarantined},
		{"C invalid lock and no record", func(t *testing.T, root string) { r372InvalidLock(t, root, taskID) }, false,
			r372CLIUnknown, event.WorkflowWorkspaceQuarantined},
		{"D valid lock and no record", none, false, r372CLIOrdinary, event.WorkflowFailed},
		{"E readable settled", settled, false, r372CLIOrdinary, event.WorkflowFailed},
		{"F readable unsettled", unsettled, false, r372CLIQuarantined, event.WorkflowWorkspaceQuarantined},
		{"G lockless ordinary error (L1/L2)", settled, true, r372CLIOrdinary, event.WorkflowFailed},
		{"H lockless cancellation (L3)", none, true, r372CLIOrdinary, event.WorkflowStopped},
		{"I lockless timeout (L4)", none, true, r372CLIOrdinary, event.WorkflowTimedOut},
		{"J lockless unsettled (L5)", unsettled, true, r372CLIQuarantined, event.WorkflowWorkspaceQuarantined},
		{"K lockless unreadable (L6)", torn, true, r372CLIUnknown, event.WorkflowWorkspaceQuarantined},
		{"L required confined pre-start refusal", settled, true, r372CLIOrdinary, event.WorkflowFailed},
	}
	for _, r := range rows {
		t.Run(r.row, func(t *testing.T) {
			root := gitRepoRoot(t)
			const holder = "session-20260128T000000.000000000Z"
			r.setup(t, root)
			history := []event.Event{
				event.New(holder, taskID, event.SourceSystem, event.TaskCreated, "the objective", nil),
				b2a2Deferred(holder, taskID),
			}
			if r.want != r372CLIOrdinary {
				history = append(history, r371Unverified(holder, taskID, "the containment read failed"))
			}
			writeRawSession(t, root, holder, history...)
			if r.lockless {
				r367NoLock(t)
			}
			// Headless: the engine terminal's exit.
			code, _ := exitFor(r.terminal, true)
			if (code == exitWorkspaceQuarantined) != (r.want != r372CLIOrdinary) {
				t.Fatalf("L8: the engine terminal %s (exit %d) disagrees with the CLI status %s", r.terminal, code, r.want)
			}
			// --list
			lcode, stdout, stderr := runResume(t, root, "--list")
			if lcode != exitCompleted {
				t.Fatalf("the listing failed: exit %d\n%s\n%s", lcode, stdout, stderr)
			}
			b := wqBlock(stdout, taskID)
			switch r.want {
			case r372CLIOrdinary:
				if !strings.Contains(b, "--answer 1") || strings.Contains(b, wqLaneText) || strings.Contains(b, wqUnknownText) {
					t.Errorf("--list does not offer the ordinary lane:\n%s", b)
				}
				r372Allowed(t, root, taskID)
			default:
				want := wqUnknownText
				if r.want == r372CLIQuarantined {
					want = wqLaneText
				}
				if !strings.Contains(b, want) || strings.Contains(b, "--answer") || strings.Contains(b, "resume     --task") {
					t.Errorf("--list does not show %q without an offer:\n%s", want, b)
				}
				r372Refused(t, root, taskID, want)
			}
			t.Logf("%s: engine %s (headless exit %d), CLI %s", r.row, r.terminal, code, r.want)
		})
	}
}

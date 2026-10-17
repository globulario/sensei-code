package main

// N5 P2-B (RULING-366 §17-§20; §30 W8 W9 W12 W18 W19 W20 W21 W22 W25 W26).
//
// The durable candidate record is the one quarantine authority. Resume reads
// its CURRENT state -- never a session payload -- and a task whose workspace
// containment is not established is listed as WORKSPACE QUARANTINED - OWED
// SETTLEMENT and refused, ahead of every other lane, before any resume
// identity, session lineage, readiness check, engine or model exists. A
// standing question stays standing behind it and is not answerable through
// it. Nothing here reads or writes a workspace or the record.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

const (
	wqLaneText    = "WORKSPACE QUARANTINED - OWED SETTLEMENT"
	wqUnknownText = "CANNOT ESTABLISH CONTAINMENT STATE"
	wqNoSettle    = "no settlement capability exists"
	wqInvocation  = "exec-7"
)

// wqCandidate records a candidate for taskID with a confined execution in
// flight (and a baseline), quarantined when asked: the state P1 leaves.
func wqCandidate(t *testing.T, root, taskID string, quarantine bool) (workspace, baseline string) {
	t.Helper()
	workspace = filepath.Join(root, ".worktrees", taskID)
	baseline = workspace + "-baseline"
	id := candidate.Identity{TaskID: taskID, Repository: root, BaseSHA: strings.Repeat("a", 40),
		Worktree: workspace, CreatedAt: time.Now().UTC()}
	if err := id.Save(root); err != nil {
		t.Fatal(err)
	}
	if _, err := candidate.BeginExecution(root, taskID, candidate.InFlight{Invocation: wqInvocation, Phase: "certify",
		Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	if err := candidate.NoteBaseline(root, taskID, wqInvocation, baseline); err != nil {
		t.Fatal(err)
	}
	if quarantine {
		if _, err := candidate.QuarantineExecution(root, taskID, candidate.Resolution{
			Reason: "the certify execution has no established teardown"}); err != nil {
			t.Fatal(err)
		}
	}
	return workspace, baseline
}

// wqSettledCandidate records a candidate with nothing in flight.
func wqSettledCandidate(t *testing.T, root, taskID string) {
	t.Helper()
	id := candidate.Identity{TaskID: taskID, Repository: root, BaseSHA: strings.Repeat("a", 40),
		Worktree: filepath.Join(root, ".worktrees", taskID), CreatedAt: time.Now().UTC()}
	if err := id.Save(root); err != nil {
		t.Fatal(err)
	}
}

func wqRecordPath(root, taskID string) string {
	return filepath.Join(root, ".sensei-code", "candidates", taskID+".json")
}

// wqTerminal is the verified WORKSPACE_QUARANTINED terminal P2-A records.
func wqTerminal(sessionID, taskID, workspace string) event.Event {
	return event.New(sessionID, taskID, event.SourceSystem, event.WorkflowWorkspaceQuarantined,
		"the invocation ended while workspace containment is not established",
		map[string]any{"task_id": taskID, "recorded": true, "invocation": wqInvocation, "phase": "certify",
			"workspace": workspace, "reason": "no established teardown"})
}

func wqLedger(root, sessionID string) string {
	return filepath.Join(root, ".sensei-code", "sessions", sessionID, "events.jsonl")
}

func wqRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func wqSessions(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".sensei-code", "sessions"))
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

// wqBlock is the listing block of one task: from its "task <id>  " line to the
// next blank line.
func wqBlock(stdout, taskID string) string {
	at := strings.Index(stdout, "task "+taskID+"  ")
	if at < 0 {
		return ""
	}
	rest := stdout[at:]
	if end := strings.Index(rest, "\n\n"); end >= 0 {
		return rest[:end]
	}
	return rest
}

// W8 W19 W25. The listing names the workspace quarantine, decided from the
// current record, distinctly from a quarantined SESSION record; it shows the
// invocation, workspace and baseline and that no settlement capability exists;
// a standing question is preserved behind it and not offered for an answer;
// a task found both in a session and in the candidate records is listed once.
func TestP2BW8TheListingShowsAWorkspaceQuarantineDistinctly(t *testing.T) {
	root := gitRepoRoot(t)
	const holder, valid = "session-20260110T000000.000000000Z", "session-20260111T000000.000000000Z"
	const torn = "session-20260109T000000.000000000Z"
	workspace, baseline := wqCandidate(t, root, "t-q", false)
	writeRawSession(t, root, holder,
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, "t-q"),
		wqTerminal(holder, "t-q", workspace))
	b2a2Quarantined(t, root, torn, "t-torn")
	writeSession(t, root, valid, event.New(valid, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	record := wqRead(t, wqRecordPath(root, "t-q"))

	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	q := wqBlock(stdout, "t-q")
	for _, want := range []string{wqLaneText, wqInvocation, workspace, baseline, wqNoSettle} {
		if !strings.Contains(q, want) {
			t.Errorf("the quarantined task's block does not state %q:\n%s", want, q)
		}
	}
	// W25: the question is preserved and named, and not offered for an answer.
	if !strings.Contains(q, "question") || strings.Contains(q, "--answer") || strings.Contains(q, "resume     --task t-q") {
		t.Errorf("the standing question is not shown behind the obligation, or is offered as a route:\n%s", q)
	}
	// The SESSION-level quarantine keeps its own word, and only it.
	tornBlock := wqBlock(stdout, "t-torn")
	if !strings.Contains(tornBlock, "QUARANTINED") || strings.Contains(tornBlock, "WORKSPACE") {
		t.Errorf("the session-level quarantine is not shown as itself:\n%s", tornBlock)
	}
	if strings.Contains(q, errTaskQuarantined.Error()) {
		t.Errorf("the workspace quarantine reuses the session-level refusal:\n%s", q)
	}
	if !strings.Contains(stdout, "resume     --task t-ok") {
		t.Errorf("the ordinary task is hidden beside the quarantine:\n%s", stdout)
	}
	// W19: once, though the session holds it and the candidate records list it.
	if n := strings.Count(stdout, "task t-q  "); n != 1 {
		t.Errorf("the task found both ways is listed %d times:\n%s", n, stdout)
	}
	if string(wqRead(t, wqRecordPath(root, "t-q"))) != string(record) {
		t.Error("W12: listing changed the candidate record")
	}
}

// W3 at the surface, and the record decides: a crash with InFlight on disk and
// no terminal written is listed as quarantined; a session observation whose
// record is settled now is history and routes as ordinary work.
func TestP2BTheCurrentRecordDecidesNotTheSessionPayload(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260112T000000.000000000Z"
	wqCandidate(t, root, "t-crash", false)
	wqSettledCandidate(t, root, "t-history")
	writeRawSession(t, root, holder,
		event.New(holder, "t-crash", event.SourceSystem, event.TaskCreated, "crashed work", nil),
		event.New(holder, "t-history", event.SourceSystem, event.TaskCreated, "historical work", nil),
		wqTerminal(holder, "t-history", filepath.Join(root, ".worktrees", "t-history")))
	code, stdout, _ := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s", code, stdout)
	}
	if b := wqBlock(stdout, "t-crash"); !strings.Contains(b, wqLaneText) {
		t.Errorf("an in-flight record with no terminal is not listed as owed settlement:\n%s", b)
	}
	if b := wqBlock(stdout, "t-history"); strings.Contains(b, wqLaneText) || !strings.Contains(b, "resume     --task t-history") {
		t.Errorf("a session payload decided the lane against the settled record:\n%s", b)
	}
}

// W9 W21 W26 W12. `resume --task` on a quarantined task is refused, typed,
// before any resume identity, lineage binding, readiness check or engine:
// nothing is appended, no session is minted, the record is untouched -- and
// the refusal is the workspace quarantine, ahead of the standing question.
func TestP2BW21ResumeTaskIsRefusedBeforeAnyIdentityOrLineage(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260113T000000.000000000Z"
	wqCandidate(t, root, "t-q", true)
	writeRawSession(t, root, holder,
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, "t-q"))
	ledger, record, sessions := wqRead(t, wqLedger(root, holder)), wqRead(t, wqRecordPath(root, "t-q")), wqSessions(t, root)

	code, _, stderr := runResume(t, root, "--task", "t-q", "--quiet")
	if code != exitFailed || !strings.Contains(stderr, wqLaneText) || !strings.Contains(stderr, wqNoSettle) {
		t.Fatalf("the quarantined task was not refused as owed settlement: exit %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, errResumeBehindQuestion.Error()) {
		t.Errorf("the question lane was chosen ahead of the workspace quarantine:\n%s", stderr)
	}
	if strings.Contains(stderr, "not ready") || strings.Contains(stderr, "continues as session") {
		t.Errorf("readiness or a session binding was reached before the refusal:\n%s", stderr)
	}
	after := wqRead(t, wqLedger(root, holder))
	if string(after) != string(ledger) || strings.Contains(string(after), string(event.SessionLineageBound)) {
		t.Error("the refusal appended to the task's history")
	}
	if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
		t.Errorf("a session was created for a refused resume: %v -> %v", sessions, got)
	}
	if string(wqRead(t, wqRecordPath(root, "t-q"))) != string(record) {
		t.Error("W12: resume changed the candidate record")
	}
}

// W20 W25. `resume --answer` is gated exactly like --task: refused before any
// identity or lineage, and the human question is neither answered nor
// consumed -- it still stands, byte for byte.
func TestP2BW20AnAnswerIsRefusedAndTheQuestionStands(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260114T000000.000000000Z"
	workspace, _ := wqCandidate(t, root, "t-q", true)
	writeRawSession(t, root, holder,
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, "t-q"),
		wqTerminal(holder, "t-q", workspace))
	before, err := session.FindActive(root)
	if err != nil {
		t.Fatal(err)
	}
	held, ok := activeTask(before.Active, "t-q")
	if !ok || len(held.Task.AwaitingAuthority) == 0 {
		t.Fatalf("premise: the question stands: %+v", before.Active)
	}
	ledger, record, sessions := wqRead(t, wqLedger(root, holder)), wqRead(t, wqRecordPath(root, "t-q")), wqSessions(t, root)

	code, _, stderr := runResume(t, root, "--task", "t-q", "--answer", "1", "--quiet")
	if code != exitFailed || !strings.Contains(stderr, wqLaneText) {
		t.Fatalf("an answer was not refused behind the workspace quarantine: exit %d\n%s", code, stderr)
	}
	if strings.Contains(stderr, "answered") || strings.Contains(stderr, "not ready") {
		t.Errorf("the answer path went further than the refusal:\n%s", stderr)
	}
	if string(wqRead(t, wqLedger(root, holder))) != string(ledger) {
		t.Error("the refused answer appended to the task's history")
	}
	if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
		t.Errorf("a session was created for a refused answer: %v -> %v", sessions, got)
	}
	if string(wqRead(t, wqRecordPath(root, "t-q"))) != string(record) {
		t.Error("W12: an answer changed the candidate record")
	}
	after, err := session.FindActive(root)
	if err != nil {
		t.Fatal(err)
	}
	still, ok := activeTask(after.Active, "t-q")
	if !ok || string(still.Task.AwaitingAuthority) != string(held.Task.AwaitingAuthority) {
		t.Fatal("the standing question did not survive the refused answer")
	}
}

// W18. A P1-era task its history already ended FAILED, and a task whose
// session history is gone, are listed from the candidate records as owed
// settlement; the history is not rewritten, and naming either is the
// workspace refusal, not an absence.
func TestP2BW18AP1EraOrphanIsListedAsOwedSettlement(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260115T000000.000000000Z"
	workspace, baseline := wqCandidate(t, root, "t-p1", true)
	wqCandidate(t, root, "t-gone", false)
	writeRawSession(t, root, holder,
		event.New(holder, "t-p1", event.SourceSystem, event.TaskCreated, "p1-era work", nil),
		event.New(holder, "t-p1", event.SourceSystem, event.WorkflowFailed, "candidate t-p1 is quarantined", nil))
	ledger := wqRead(t, wqLedger(root, holder))

	code, stdout, _ := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("the listing failed: exit %d\n%s", code, stdout)
	}
	for _, task := range []string{"t-p1", "t-gone"} {
		if b := wqBlock(stdout, task); !strings.Contains(b, wqLaneText) || !strings.Contains(b, wqNoSettle) {
			t.Errorf("the orphan %s is not listed as owed settlement:\n%s", task, stdout)
		}
	}
	if b := wqBlock(stdout, "t-p1"); !strings.Contains(b, workspace) || !strings.Contains(b, baseline) {
		t.Errorf("the orphan does not name its workspace and baseline:\n%s", b)
	}
	if string(wqRead(t, wqLedger(root, holder))) != string(ledger) {
		t.Error("the listing rewrote the P1-era history")
	}
	for _, task := range []string{"t-p1", "t-gone"} {
		code, _, stderr := runResume(t, root, "--task", task, "--quiet")
		if code != exitFailed || !strings.Contains(stderr, wqLaneText) || strings.Contains(stderr, errTaskUnknown.Error()) {
			t.Errorf("--task %s did not refuse as owed settlement: exit %d\n%s", task, code, stderr)
		}
	}

	// With no session record at all, the unsafe workspace is still reported.
	bare := gitRepoRoot(t)
	wqCandidate(t, bare, "t-alone", true)
	code, stdout, stderr := runResume(t, bare, "--list")
	if code != exitCompleted || !strings.Contains(wqBlock(stdout, "t-alone"), wqLaneText) {
		t.Errorf("a repository with no session hides its quarantined workspace: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if code, _, stderr := runResume(t, bare, "--task", "t-alone"); code != exitFailed || !strings.Contains(stderr, wqLaneText) {
		t.Errorf("--task on a sessionless quarantine is not refused as one: exit %d\n%s", code, stderr)
	}
}

// W22. An unreadable record renders CANNOT ESTABLISH CONTAINMENT STATE for its
// task only: it is never settled, never hidden, and hides no other task.
func TestP2BW22AnUnreadableRecordHidesNoOtherTask(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260116T000000.000000000Z"
	wqCandidate(t, root, "t-q", true)
	if err := os.MkdirAll(filepath.Dir(wqRecordPath(root, "t-bad")), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, task := range []string{"t-bad", "t-bad-orphan"} {
		if err := os.WriteFile(wqRecordPath(root, task), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeRawSession(t, root, holder,
		event.New(holder, "t-bad", event.SourceSystem, event.TaskCreated, "unknown containment", nil),
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "quarantined work", nil),
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitCompleted {
		t.Fatalf("one unreadable record refused the whole listing: exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, task := range []string{"t-bad", "t-bad-orphan"} {
		if b := wqBlock(stdout, task); !strings.Contains(b, wqUnknownText) || strings.Contains(b, "resume     --task") {
			t.Errorf("the unreadable record of %s is not shown as unknown containment:\n%s", task, stdout)
		}
	}
	if !strings.Contains(wqBlock(stdout, "t-q"), wqLaneText) || !strings.Contains(stdout, "resume     --task t-ok") {
		t.Errorf("the unreadable record hid another task:\n%s", stdout)
	}
	if code, _, stderr := runResume(t, root, "--task", "t-bad", "--quiet"); code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
		t.Errorf("--task over an unreadable record was not refused as unknown: exit %d\n%s", code, stderr)
	}
}

// W26 / M13. The command consults the current record before it opens a
// resume identity, chooses a lane, checks readiness or builds an engine --
// so neither a display nor a refusal needs a role, a tool or a model.
func TestP2BW26TheRecordIsConsultedBeforeAnythingIsStarted(t *testing.T) {
	src, err := os.ReadFile("resume.go")
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	start, end := strings.Index(body, "func resumeAuthorityAnswered("), strings.Index(body, "func settleResumed(")
	if start < 0 || end < start {
		t.Fatal("resumeAuthorityAnswered was not found")
	}
	command := body[start:end]
	// THE MAIN GATE, not the FindActive-error or no-history branches (both
	// `} else if refusal := ...`): the one function-level refusal that every
	// named-task continuation passes.
	const gate = "\n\tif refusal := contained.refusal(); refusal != nil {\n"
	if n := strings.Count(command, gate); n != 1 {
		t.Fatalf("the command has %d function-level refusals from the current candidate record, want 1", n)
	}
	check := strings.Index(command, gate)
	for _, later := range []string{"activeTask(active, *taskID)", "errObjectiveUnusable",
		"openResumeIdentity(", "selectResumeLane(", "selectAuthorityResume(",
		"inspectQuick(", "workflow.New(", "owedReviewObligation("} {
		if at := strings.Index(command, later); at < 0 || at < check {
			t.Errorf("%s is reached before the candidate record is consulted", later)
		}
	}
}

// W21 W20 / M08a, on a task a session DOES hold. The workspace refusal is the
// first thing decided about a named task: it precedes the task lookup, the
// objective check and the resume identity. Minting that identity has no
// observable side effect for a held task (session.New only re-creates the
// holder's existing directory; FreshID writes nothing), so its order is
// pinned textually by W26 and behaviourally HERE by the two refusals that sit
// between the gate and openResumeIdentity: a held task with no usable
// objective, and a held task named under another --session. Each must be
// refused as owed settlement -- never as an unusable objective or a scoped
// miss -- for --task and --answer alike, with nothing appended, minted or
// rewritten.
func TestP2BW21AHeldQuarantinedTaskIsRefusedBeforeLookupObjectiveOrIdentity(t *testing.T) {
	for name, c := range map[string]struct {
		objective string
		scoped    bool
		other     error
	}{
		"held with no usable objective": {"", false, errObjectiveUnusable},
		"held in another named session": {"the objective", true, errTaskNotInSession},
	} {
		for _, answer := range []bool{false, true} {
			t.Run(name+map[bool]string{false: " --task", true: " --answer"}[answer], func(t *testing.T) {
				root := gitRepoRoot(t)
				const holder, other = "session-20260117T000000.000000000Z", "session-20260118T000000.000000000Z"
				wqCandidate(t, root, "t-q", true)
				writeRawSession(t, root, holder,
					event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, c.objective, nil),
					b2a2Deferred(holder, "t-q"))
				writeSession(t, root, other, event.New(other, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
				before, err := session.FindActive(root)
				if err != nil {
					t.Fatal(err)
				}
				held, ok := activeTask(before.Active, "t-q")
				if !ok || held.SessionID != holder || held.Task.ObjectiveUsable() == (c.objective == "") {
					t.Fatalf("premise: %s holds t-q with objective %q: %+v", holder, c.objective, before.Active)
				}
				ledger, record, sessions := wqRead(t, wqLedger(root, holder)), wqRead(t, wqRecordPath(root, "t-q")), wqSessions(t, root)

				args := []string{"--task", "t-q", "--quiet"}
				if answer {
					args = append(args, "--answer", "1")
				}
				if c.scoped {
					args = append(args, "--session", other)
				}
				code, _, stderr := runResume(t, root, args...)
				if code != exitFailed || !strings.Contains(stderr, wqLaneText) || !strings.Contains(stderr, wqNoSettle) {
					t.Fatalf("the held quarantined task was not refused as owed settlement first: exit %d\n%s", code, stderr)
				}
				if strings.Contains(stderr, c.other.Error()) {
					t.Errorf("a check after the gate decided the refusal:\n%s", stderr)
				}
				after := wqRead(t, wqLedger(root, holder))
				if string(after) != string(ledger) || strings.Contains(string(after), string(event.SessionLineageBound)) {
					t.Error("the refusal appended to the task's history")
				}
				if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
					t.Errorf("a session was created for a refused resume: %v -> %v", sessions, got)
				}
				if string(wqRead(t, wqRecordPath(root, "t-q"))) != string(record) {
					t.Error("W12: resume changed the candidate record")
				}
			})
		}
	}
}

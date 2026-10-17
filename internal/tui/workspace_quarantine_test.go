package tui

// RULING-368 §7-§8, §11, §13 (T1-T5). A WORKSPACE_QUARANTINED terminal reaches
// the TUI through its real Update and View paths: it ends the live wait (busy
// and pending cleared), it is part of the ordinary non-verbose conversation,
// and it is shown under the plain label WORKSPACE QUARANTINED stating that the
// invocation ended, containment is unresolved, the task is owed a settlement,
// and ordinary continuation is blocked. It is never READY, FAILED, STOPPED or
// TIMED OUT, and nothing here settles, recaptures or reuses the workspace.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/event"
)

const (
	r368Task    = "task-quarantined"
	r368Summary = "the invocation ended while the workspace containment of task task-quarantined is not established " +
		"(its candidate record: no established teardown); workspace /w may still be written, so the task is owed a " +
		"trusted settlement and nothing ordinary continues it"
)

func r368Event(workspace string) event.Event {
	return event.New("session-q", r368Task, event.SourceSystem, event.WorkflowWorkspaceQuarantined, r368Summary,
		map[string]any{"task_id": r368Task, "recorded": true, "invocation": "exec-7", "phase": "certify",
			"workspace": workspace, "reason": "no established teardown"})
}

// r368Busy is a real model mid-run of r368Task, non-verbose, with a pending
// question standing, as the TUI holds it while it waits.
func r368Busy() Model {
	m := New(context.Background(), nil, nil, nil, emptyInventory, nil)
	m.width, m.height = 400, 60
	m.lines = nil
	m.busy = true
	m.currentTask = r368Task
	m.pending = &authority.Decision{}
	m.pendingTask = r368Task
	return m
}

func r368Update(t *testing.T, m Model, e event.Event) Model {
	t.Helper()
	updated, _ := m.Update(eventMsg(e))
	return updated.(Model)
}

// quarantineBlock is the transcript block rendered for the event.
func r368Block(m Model) string {
	text := transcriptOf(m)
	at := strings.Index(text, "WORKSPACE QUARANTINED")
	if at < 0 {
		return ""
	}
	return text[at:]
}

// T1 (pre-fix control H3). The live wait ends.
func TestR368T1TheQuarantineClearsTheBusyWait(t *testing.T) {
	m := r368Update(t, r368Busy(), r368Event("/w"))
	if m.busy {
		t.Fatal("the TUI stays busy after the invocation ended on WORKSPACE_QUARANTINED")
	}
	if m.pending != nil || m.pendingTask != "" {
		t.Fatalf("the pending wait was not cleared: pending=%v task=%q", m.pending, m.pendingTask)
	}
	if strings.Contains(ansi.Strip(m.statusLine()), "type to steer") {
		t.Fatalf("the status line still reports a running task: %q", ansi.Strip(m.statusLine()))
	}
}

// T2 (pre-fix control H4). Ordinary non-verbose mode shows the label, the
// task, the obligation and the engine's summary -- live and on replay.
func TestR368T2NonVerboseShowsWorkspaceQuarantinedAndTheSummary(t *testing.T) {
	m := r368Update(t, r368Busy(), r368Event("/w"))
	if m.verbose {
		t.Fatal("the witness must run in non-verbose mode")
	}
	view := ansi.Strip(m.View().Content)
	if !strings.Contains(view, "WORKSPACE QUARANTINED") {
		t.Fatalf("the non-verbose view does not show WORKSPACE QUARANTINED:\n%s", view)
	}
	block := r368Block(m)
	for _, want := range []string{
		r368Task,
		"invocation ended",
		"containment is unresolved",
		"owed a trusted settlement",
		"ordinary continuation is blocked",
		"no established teardown",
	} {
		if !strings.Contains(strings.Join(strings.Fields(block), " "), want) {
			t.Fatalf("the quarantine block does not convey %q:\n%s", want, block)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(transcriptOf(m)), " "), strings.Join(strings.Fields(r368Summary), " ")) {
		t.Fatalf("the engine's summary is not in the transcript:\n%s", transcriptOf(m))
	}
	replayed := plainTranscript(replayConversation([]event.Event{r368Event("/w")}))
	if !strings.Contains(replayed, "WORKSPACE QUARANTINED") {
		t.Fatalf("a relaunch does not replay the quarantine:\n%s", replayed)
	}
}

// T3. It is not rendered as success, failure, stop or timeout, and it invites
// no resume, retry, implementation or settlement.
func TestR368T3TheQuarantineIsNotAnotherOutcome(t *testing.T) {
	block := r368Block(r368Update(t, r368Busy(), r368Event("/w")))
	if block == "" {
		t.Fatal("no quarantine block was rendered")
	}
	upper := strings.ToUpper(block)
	for _, other := range []string{"READY", "FAILED", "STOPPED", "TIMED OUT", "DEFERRED", "COMPLETED", "✓", "✗", "■",
		"/RESUME", "RETRY", "/RUN", "/IMPLEMENT", "/SETTLE"} {
		if strings.Contains(upper, other) {
			t.Fatalf("the quarantine is rendered with %q:\n%s", other, block)
		}
	}
}

// T4. No handler settles, recaptures or reuses the workspace: the engine is
// nil (any engine call would panic), nothing is offered for resume, the
// running task is not replaced, and the workspace is untouched.
func TestR368T4NothingSettlesRecapturesOrReusesTheWorkspace(t *testing.T) {
	workspace := t.TempDir()
	marker := filepath.Join(workspace, "candidate.txt")
	if err := os.WriteFile(marker, []byte("candidate"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := r368Busy()
	m := r368Update(t, before, r368Event(workspace))
	if len(m.resumable) != len(before.resumable) {
		t.Fatalf("the quarantine offered a task for resume: %+v", m.resumable)
	}
	if m.currentTask != before.currentTask {
		t.Fatalf("the running task changed from %q to %q", before.currentTask, m.currentTask)
	}
	if b, err := os.ReadFile(marker); err != nil || string(b) != "candidate" {
		t.Fatalf("the workspace was touched: %q %v", b, err)
	}
	entries, err := os.ReadDir(workspace)
	if err != nil || len(entries) != 1 {
		t.Fatalf("the workspace changed: %v %v", entries, err)
	}
}

// T5. Every existing terminal keeps its display and its busy clearing, and an
// ordinary status does not end the wait.
func TestR368T5ExistingTerminalDisplayIsUnchanged(t *testing.T) {
	for kind, label := range map[event.Kind]string{
		event.WorkflowCompleted:          "✓ READY",
		event.WorkflowFailed:             "✗ FAILED",
		event.WorkflowStopped:            "■ STOPPED",
		event.WorkflowAwaitingAuthority:  "◇ DEFERRED",
		event.WorkflowBlockedExternal:    "◇ BLOCKED EXTERNAL",
		event.WorkflowNotConverged:       "◇ NOT CONVERGED",
		event.WorkflowRestorationRefused: "◇ RESTORATION REFUSED",
	} {
		m := r368Update(t, r368Busy(), event.New("s", r368Task, event.SourceSystem, kind, "the ending", nil))
		if m.busy || m.pending != nil {
			t.Fatalf("%s no longer clears the wait", kind)
		}
		text := transcriptOf(m)
		if !strings.Contains(text, label) || strings.Contains(text, "WORKSPACE QUARANTINED") {
			t.Fatalf("%s is no longer rendered %q:\n%s", kind, label, text)
		}
	}
	m := r368Update(t, r368Busy(), event.New("s", r368Task, event.SourceSystem, event.Status, "working", nil))
	if !m.busy {
		t.Fatal("an ordinary status ended the wait")
	}
}

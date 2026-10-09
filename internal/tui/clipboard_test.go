package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"github.com/globulario/sensei-code/internal/event"

	"charm.land/lipgloss/v2"
)

// What the reader sees is styled; what they meant to copy is the text. Copying
// the stored line verbatim puts escape sequences in the clipboard, which paste
// into an editor as garbage.
func TestCopyingStripsStyling(t *testing.T) {
	styled := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#E95454")).Render("Architect")
	if !strings.Contains(styled, "\x1b") {
		t.Skip("lipgloss produced no styling in this environment")
	}
	got := plainTranscript([]string{styled, "  a plain line"})
	if strings.Contains(got, "\x1b") {
		t.Fatalf("copied text still carries escape sequences: %q", got)
	}
	for _, want := range []string{"Architect", "a plain line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("copied text lost %q: %q", want, got)
		}
	}
}

// The last response is kept as the text the event carried. Recovering it by
// reading rendered lines back would be parsing presentation for meaning.
func TestLastResponseComesFromTheEventNotTheRendering(t *testing.T) {
	body := funcBodyTUI(t, "Update")
	if !strings.Contains(body, "m.lastResponse = text") {
		t.Fatal("the last response is no longer captured from the event")
	}
	if strings.Contains(body, "lastResponseFromLines") {
		t.Fatal("the last response is being recovered from rendered lines")
	}
}

// A terminal that refuses an OSC 52 read answers with silence. Without the
// hint, ctrl+v is indistinguishable from an unbound key.
func TestAnUnansweredPasteExplainsItself(t *testing.T) {
	m := Model{pasteWaiting: true}
	updated, _ := m.Update(pasteUnansweredMsg{})
	got := updated.(Model)
	if got.pasteWaiting {
		t.Fatal("the model is still waiting after the read went unanswered")
	}
	joined := plainTranscript(got.lines)
	if !strings.Contains(joined, "ctrl+shift+v") {
		t.Fatalf("the hint does not name the paste that does work: %q", joined)
	}
}

// The hint must not fire for a read that was answered, or every successful
// paste would also be told it failed.
func TestAnAnsweredPasteIsNotWarnedAbout(t *testing.T) {
	m := Model{pasteWaiting: false}
	updated, _ := m.Update(pasteUnansweredMsg{})
	if len(updated.(Model).lines) != 0 {
		t.Fatal("a paste that was answered still produced a failure hint")
	}
}

// Releasing the mouse is what makes drag-to-select work; if the view keeps
// tracking regardless, /mouse would report a change it did not make.
func TestReleasingTheMouseReachesTheView(t *testing.T) {
	body := funcBodyTUI(t, "View")
	if !strings.Contains(body, "m.mouseOff") || !strings.Contains(body, "MouseModeNone") {
		t.Fatal("the view no longer releases the mouse")
	}
}

// Pressing the key must actually put the text on the clipboard. The structural
// checks above say the branch exists; this runs it and reads what came out.
func TestCopyKeysProduceClipboardWrites(t *testing.T) {
	for _, tc := range []struct {
		name    string
		key     tea.KeyPressMsg
		typed   string
		last    string
		want    string
		cleared bool
	}{
		{"copy composer", ctrlKey('y'), "hello clipboard world", "", "hello clipboard world", false},
		{"cut composer", ctrlKey('x'), "cut me", "", "cut me", true},
		{"copy last response", ctrlKey('r'), "", "the architect said this", "the architect said this", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(tc.typed, tc.last)
			updated, cmd := m.Update(tc.key)
			got := collectClipboardWrites(cmd)
			if len(got) != 1 {
				t.Fatalf("expected exactly one clipboard write, got %d: %q", len(got), got)
			}
			if got[0] != tc.want {
				t.Errorf("clipboard got %q, want %q", got[0], tc.want)
			}
			if left := updated.(Model).input.Value(); tc.cleared && left != "" {
				t.Errorf("cut left the composer holding %q", left)
			} else if !tc.cleared && tc.typed != "" && left != tc.typed {
				t.Errorf("copy changed the composer to %q", left)
			}
		})
	}
}

// Copying nothing must not write an empty clipboard: that would silently
// destroy whatever the reader had copied before.
func TestCopyingNothingWritesNothing(t *testing.T) {
	for _, key := range []tea.KeyPressMsg{ctrlKey('y'), ctrlKey('x'), ctrlKey('r')} {
		m := newTestModel("", "")
		_, cmd := m.Update(key)
		if got := collectClipboardWrites(cmd); len(got) != 0 {
			t.Errorf("%s wrote %q to the clipboard with nothing to copy", key.String(), got)
		}
	}
}

// ctrl+c stays quit. A clipboard key that stole it would strand the reader.
func TestCopyKeysDoNotStealQuit(t *testing.T) {
	m := newTestModel("some text", "")
	_, cmd := m.Update(ctrlKey('c'))
	if cmd == nil {
		t.Fatal("ctrl+c produced no command")
	}
	if fmt.Sprintf("%T", cmd()) != "tea.QuitMsg" {
		t.Fatalf("ctrl+c no longer quits: got %T", cmd())
	}
}

func TestClipboardDoesNotShellOutToAHelperBinary(t *testing.T) {
	// The helpers are named in clipboard.go's comment explaining why they are
	// not used, so this asks whether they are INVOKED, not whether the word
	// appears. atotto/clipboard is what the textarea's own ctrl+v uses; this
	// package must not import it.
	for _, src := range []string{"internal/tui/clipboard.go", "internal/tui/model.go"} {
		text := fileTextTUI(t, src)
		for _, bad := range []string{`atotto/clipboard`, `exec.Command("xclip"`, `exec.Command("xsel"`, `exec.Command("wl-copy"`} {
			if strings.Contains(text, bad) {
				t.Errorf("%s reaches for %s instead of the terminal", src, bad)
			}
		}
	}
	if !strings.Contains(fileTextTUI(t, "internal/tui/clipboard.go"), "tea.SetClipboard") {
		t.Fatal("copy no longer uses the terminal's clipboard escape")
	}
	if !strings.Contains(fileTextTUI(t, "internal/tui/model.go"), "tea.ReadClipboard") {
		t.Fatal("paste no longer asks the terminal")
	}
}

func ctrlKey(r rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl}
}

func newTestModel(typed, last string) Model {
	m := Model{lastResponse: last}
	m.input = textarea.New()
	if typed != "" {
		m.input.SetValue(typed)
	}
	return m
}

// collectClipboardWrites runs a command tree and returns the text of every
// OSC 52 write it produced. The message type is unexported, so it is matched by
// name and read by reflection -- the alternative is trusting that a branch which
// mentions SetClipboard reaches it.
func collectClipboardWrites(cmd tea.Cmd) []string {
	var out []string
	var walk func(tea.Cmd)
	walk = func(c tea.Cmd) {
		if c == nil {
			return
		}
		msg := c()
		switch v := msg.(type) {
		case tea.BatchMsg:
			for _, inner := range v {
				walk(inner)
			}
			return
		case nil:
			return
		}
		rv := reflect.ValueOf(msg)
		if rv.Type().Name() == "setClipboardMsg" && rv.Kind() == reflect.String {
			out = append(out, rv.String())
		}
	}
	walk(cmd)
	return out
}

// isConversation admits GuidanceDelivered, which carries the human's own words
// queued for a worker. Copying it back as "the last response" would quote the
// reader to themselves.
func TestTheHumansOwnWordsAreNotTheLastResponse(t *testing.T) {
	m := newTestModel("", "")
	architect := event.Event{Kind: event.ArchitectSpoke, Source: event.SourceArchitect, Summary: "the architect's answer"}
	guidance := event.Event{Kind: event.GuidanceDelivered, Source: event.SourceUser, Summary: "do it faster please"}

	updated, _ := m.Update(eventMsg(architect))
	updated, _ = updated.(Model).Update(eventMsg(guidance))
	got := updated.(Model)

	if got.lastResponse == "do it faster please" {
		t.Fatal("the human's own guidance became the last response")
	}
	if got.lastResponse != "the architect's answer" {
		t.Fatalf("last response is %q, want the architect's answer", got.lastResponse)
	}
}

// one returns s with one zero element appended, and zeroOf a fresh zero value
// behind a pointer of p's type: the halt message carries the engine's and the
// session record's types, which this file names only through the Model's own
// fields.
func one[T any](s []T) []T {
	var z T
	return append(s, z)
}

func zeroOf[T any](*T) *T { return new(T) }

func constant[T any](v T) func() T { return func() T { return v } }

// emptyInventory is the zero value of New's discovery inventory, named here
// only through New's own signature: an inventory holding no session record,
// so nothing in it refuses a task repository-wide and each task's standing is
// decided by the canonical projection of the record New replays.
var emptyInventory = inventoryParam(New)

func inventoryParam[A, B, C, D, E, F, R any](func(A, B, C, D, E, F) R) (zero E) { return zero }

// haltedRun is a model mid-/run of task taskID: busy, with the composer
// waiting on that invocation, and no event stream -- nothing on the bus can
// release it.
func haltedRun(taskID string) Model {
	m := newTestModel("", "")
	m.busy = true
	m.currentTask = taskID
	return m
}

func transcriptOf(m Model) string { return plainTranscript(m.lines) }

// fakeHalt is an invocation's control handle that has halted with failure and
// not yet ended. It is generic only so this file can stand in for a
// workflow.RunAttempt or ResumeAttempt without naming the workflow package.
type fakeHalt[F any] struct {
	halted, ended chan struct{}
	failure       F
}

func (h fakeHalt[F]) Halted() <-chan struct{} { return h.halted }
func (h fakeHalt[F]) Ended() <-chan struct{}  { return h.ended }
func (h fakeHalt[F]) Failure() F              { return h.failure }

func haltedHandle[F any](failure F) fakeHalt[F] {
	h := fakeHalt[F]{halted: make(chan struct{}), ended: make(chan struct{}), failure: failure}
	close(h.halted)
	return h
}

// TestB2a1RunAppendHaltEndsTheRunInModelUpdate is the behavioral witness that
// a /run whose governed event the session record refused ends, in the TUI,
// on the invocation's typed halt and nothing else (RULING-195/205/206). The
// model has no event stream: no WorkflowFailed or other bus terminal arrives,
// as none may when the record did not take it. The halt alone must release the
// composer and report the typed failure, exactly once, and a /run is never
// offered back as a resumable task.
func TestB2a1RunAppendHaltEndsTheRunInModelUpdate(t *testing.T) {
	m := haltedRun("task-run")
	failure := zeroOf(appendHaltedMsg{}.failure)
	failure.TaskID, failure.SessionID = "task-run", "session-a"
	failure.Kind, failure.Detail = event.WorkflowCompleted, "the record lock was not acquired"

	// The halt reaches Model.Update only through the TUI's wait on the
	// run's own control handle; nothing else may stand in for it.
	msg := runHalted(transcript(1).ctx, haltedHandle(failure), "task-run")
	if msg == nil {
		t.Fatal("the halted /run's control handle produced no message, so the TUI waits for a terminal that cannot arrive")
	}
	next, cmd := m.Update(msg)
	got := next.(Model)
	if got.busy || got.currentTask != "" || got.pending != nil || got.pendingTask != "" {
		t.Fatalf("the halted /run still holds the composer: busy=%v current=%q pending=%v pendingTask=%q",
			got.busy, got.currentTask, got.pending, got.pendingTask)
	}
	text := transcriptOf(got)
	if strings.Count(text, "✗ RUN") != 1 || !strings.Contains(text, failure.Error()) {
		t.Fatalf("the /run's typed failure was not reported exactly once:\n%s", text)
	}
	if strings.Contains(text, "✗ RESUME") || len(got.resumable) != 0 {
		t.Fatalf("a halted /run was reported or offered as a resume: resumable=%d\n%s", len(got.resumable), text)
	}
	if cmd == nil {
		t.Fatal("the halt produced no command, so the screen is never redrawn")
	}
}

// TestB2a1ResumeAppendHaltEndsTheResumeInModelUpdate is the same witness for a
// /resume: a pre-binding refusal or a later refused governed event ends the
// /resume on its typed halt with no bus terminal, and -- the record having
// been left as it was -- the task is offered to /resume again.
func TestB2a1ResumeAppendHaltEndsTheResumeInModelUpdate(t *testing.T) {
	m := haltedRun("task-resume")
	tasks := one(m.resumable)
	tasks[0].TaskID, tasks[0].Task = "task-resume", "the interrupted work"
	failure := zeroOf(appendHaltedMsg{}.failure)
	failure.TaskID, failure.SessionID = "task-resume", "session-b"
	failure.Kind, failure.Detail = event.SessionLineageBound, "the task's session record holds no TaskCreated root"

	msg := waitResumeHalted(transcript(1).ctx, haltedHandle(failure), tasks[0])()
	if msg == nil {
		t.Fatal("the halted /resume's control handle produced no message, so the TUI waits for a terminal that cannot arrive")
	}
	next, _ := m.Update(msg)
	got := next.(Model)
	if got.busy || got.currentTask != "" {
		t.Fatalf("the halted /resume still holds the composer: busy=%v current=%q", got.busy, got.currentTask)
	}
	text := transcriptOf(got)
	if strings.Count(text, "✗ RESUME") != 1 || !strings.Contains(text, failure.Error()) {
		t.Fatalf("the /resume's typed refusal was not reported exactly once:\n%s", text)
	}
	if len(got.resumable) != 1 || got.resumable[0].TaskID != "task-resume" {
		t.Fatalf("the refused task is not offered to /resume again: %+v", got.resumable)
	}
}

// TestB2a1AStaleHaltDoesNotEndTheCurrentInvocation: a halt is bound to the
// invocation that produced it. A late halt of an earlier task reports that
// task's failure but must not release the composer of the task now running.
func TestB2a1AStaleHaltDoesNotEndTheCurrentInvocation(t *testing.T) {
	m := haltedRun("task-b")
	failure := zeroOf(appendHaltedMsg{}.failure)
	failure.TaskID, failure.Kind, failure.Detail = "task-a", event.WorkflowFailed, "stale"

	next, _ := m.Update(appendHaltedMsg{taskID: "task-a", failure: failure})
	got := next.(Model)
	if !got.busy || got.currentTask != "task-b" {
		t.Fatalf("task-a's halt ended task-b's invocation: busy=%v current=%q", got.busy, got.currentTask)
	}
}

// TestB2a1HaltIsReportedTheMomentItHappens: the TUI's wait on an invocation
// returns its typed failure as soon as the record refuses an event, without
// waiting for the invocation to finish unwinding, and returns nothing for an
// invocation that ended unhalted. Channels are driven directly; nothing sleeps.
func TestB2a1HaltIsReportedTheMomentItHappens(t *testing.T) {
	failure := zeroOf(appendHaltedMsg{}.failure)
	failure.TaskID, failure.Kind, failure.Detail = "task-run", event.WorkflowFailed, "refused"
	ctx := transcript(1).ctx // a live, never-cancelled context from New

	halted, ended := make(chan struct{}), make(chan struct{})
	close(halted)
	if got := haltOf(ctx, halted, ended, constant(failure)); got != failure {
		t.Fatalf("a halted invocation that has not ended reported %v, want its typed failure", got)
	}

	halted, ended = make(chan struct{}), make(chan struct{})
	close(ended)
	if got := haltOf(ctx, halted, ended, constant(appendHaltedMsg{}.failure)); got != nil {
		t.Fatalf("an unhalted ending reported a halt: %v", got)
	}
}

// resumeTyped submits /resume through the composer, as a person does.
func resumeTyped(m Model) Model {
	m.input.SetValue("/resume")
	next, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	return next.(Model)
}

// proposed is a correctly framed plan.proposed record of taskID by sessionID.
func proposed(sessionID, taskID string) event.Event {
	return event.New(sessionID, taskID, event.SourceArchitect, event.PlanProposed, "the plan",
		map[string]string{"decision": "proceed", "summary": "s", "plan": "the plan", "plan_attempt_id": "attempt-1", "plan_source": "architect"})
}

// A2-W2 / R220-W6 / D8: the TUI's /resume honours the canonical discovery's
// typed refusals exactly as the CLI does. A task whose history cannot be read
// as its own session lineage is refused, typed, stays visible, and no older
// task is resumed in its place; a discovery that refused is reported as that
// refusal, never as "nothing to resume".
func TestB2a2R220W6TUIResumeHonoursTheCanonicalRefusal(t *testing.T) {
	const holder = "session-holder"
	// The replayed record, classified by New itself: an older planned task,
	// and a task whose second record is malformed (it has no event id).
	history := []event.Event{
		event.New(holder, "task-older", event.SourceSystem, event.TaskCreated, "older resumable work", nil),
		proposed(holder, "task-older"),
		event.New(holder, "task-refused", event.SourceSystem, event.TaskCreated, "refused work", nil),
		{SessionID: holder, TaskID: "task-refused", Source: event.SourceSystem, Kind: event.Status, Summary: "no event id"},
	}
	m := New(transcript(1).ctx, nil, nil, history, emptyInventory, nil)
	if len(m.resumable) != 2 || m.resumable[1].TaskID != "task-refused" || m.resumable[1].Unavailable == nil {
		t.Fatalf("premise: the canonical discovery of the record refuses task-refused: %+v", m.resumable)
	}
	// A real (zero) engine, so a /resume that skipped the refusal reaches an
	// actual resume invocation -- observed below as the model continuing it --
	// rather than a nil-engine panic that would say nothing about precedence.
	m.engine = zeroOf(m.engine)
	got, attempted := func() (got Model, attempted any) {
		defer func() { attempted = recover() }()
		return resumeTyped(m), nil
	}()
	if attempted != nil {
		t.Fatalf("REFUSAL PRECEDENCE: /resume attempted a resume invocation of the refused task before stopping at its typed refusal (%v)", attempted)
	}
	if got.busy || got.currentTask != "" {
		t.Fatalf("REFUSAL PRECEDENCE: /resume continued a resume invocation (busy=%v current=%q) instead of stopping at the refused task's typed refusal, or resumed an older task in its place", got.busy, got.currentTask)
	}
	text := transcriptOf(got)
	if !strings.Contains(text, "✗ RESUME") || !strings.Contains(text, "task-refused") || !strings.Contains(text, "malformed") {
		t.Fatalf("the refusal was not reported, typed:\n%s", text)
	}
	if len(got.resumable) != 2 {
		t.Fatalf("the refused task is no longer visible: %+v", got.resumable)
	}

	refused := New(transcript(1).ctx, nil, nil, history, emptyInventory, fmt.Errorf("two session records claim one task"))
	got = resumeTyped(refused)
	text = transcriptOf(got)
	if strings.Contains(text, "nothing to resume") || !strings.Contains(text, "two session records claim one task") {
		t.Fatalf("a refused discovery was reported as absence:\n%s", text)
	}
	if got.busy || got.currentTask != "" || len(got.resumable) != 0 {
		t.Fatalf("a refused discovery still offered the record's tasks: busy=%v current=%q %+v", got.busy, got.currentTask, got.resumable)
	}
}

// R238-X2b / A2-W2 / P1-W2 / A2-W11: the TUI's resume eligibility is decided
// by the canonical discovery of the record it replays -- the record handed to
// New, classified there (Discovery.ResumableRecord), never folded raw -- and
// Model.Update's /resume acts on nothing else. The record carries foreign and
// pre-root records that a raw fold would act on.
func TestB2a2R238X2bTUIResumeEligibilityIsDecidedByLineageMembersAlone(t *testing.T) {
	const holder, unrelated = "session-holder", "session-unrelated"
	resume := func(t *testing.T, history []event.Event) Model {
		t.Helper()
		m := New(transcript(1).ctx, nil, nil, history, emptyInventory, nil)
		// A real (zero) engine: a /resume that starts an invocation is
		// observed as the model's own transition, and the invocation itself
		// refuses downstream for want of a session record, without panicking.
		m.engine = zeroOf(m.engine)
		return resumeTyped(m)
	}

	t.Run("a pre-root completion does not hide the rooted task", func(t *testing.T) {
		got := resume(t, []event.Event{
			event.New(holder, "t-later", event.SourceSystem, event.WorkflowCompleted, "pre-root completion", nil),
			event.New(holder, "t-later", event.SourceSystem, event.TaskCreated, "the objective", nil),
			proposed(holder, "t-later"),
		})
		if !got.busy || got.currentTask != "t-later" {
			t.Fatalf("R238-X2b: a pre-root WorkflowCompleted decided the TUI's resume eligibility: /resume did not "+
				"continue the rooted task t-later (busy=%v current=%q):\n%s", got.busy, got.currentTask, transcriptOf(got))
		}
	})

	t.Run("a foreign session's records of a task are not resumable authority", func(t *testing.T) {
		got := resume(t, []event.Event{
			event.New(holder, "t-valid", event.SourceSystem, event.TaskCreated, "valid work", nil),
			proposed(holder, "t-valid"),
			// An unrelated session's correctly framed records of a task this
			// record never rooted: historical evidence, not a resumable task.
			proposed(unrelated, "t-foreign"),
		})
		if got.busy || got.currentTask != "" {
			t.Fatalf("R238-X2b: an unrelated session's same-record history became TUI resume authority: /resume "+
				"continued %q (busy=%v) instead of refusing the foreign-only task:\n%s", got.currentTask, got.busy, transcriptOf(got))
		}
		text := transcriptOf(got)
		if !strings.Contains(text, "✗ RESUME") || !strings.Contains(text, "t-foreign") || !strings.Contains(text, "foreign_only") {
			t.Fatalf("R238-X2b: the foreign-only task was not reported as its typed refusal:\n%s", text)
		}
		if len(got.resumable) != 2 || got.resumable[0].TaskID != "t-valid" || got.resumable[0].Unavailable != nil {
			t.Fatalf("R238-X2b: the foreign records hid or degraded the record's valid task: %+v", got.resumable)
		}
	})
}

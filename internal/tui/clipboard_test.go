package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

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

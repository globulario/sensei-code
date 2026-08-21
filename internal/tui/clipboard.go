package tui

import (
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Clipboard work goes through OSC 52, the terminal's own clipboard escape,
// rather than shelling out to xclip/xsel/wl-copy.
//
// Those helpers are absent on plenty of machines — including the one this was
// written on — and the bubbles textarea's built-in ctrl+v depends on them, so
// paste there fails silently and looks like a broken key rather than a missing
// package. OSC 52 also survives SSH, where a local helper would be copying into
// the wrong machine's clipboard.
//
// The cost is honest to state: OSC 52 WRITE is widely supported, but READ is
// disabled by default in many terminals because it lets a program exfiltrate
// whatever you last copied. So copy is reliable and paste may not answer, which
// is why pasteHint exists rather than a key that quietly does nothing.

// copyToClipboard hands text to the terminal, with the transcript's styling
// removed. The lines are stored already rendered, and pasting escape sequences
// into an editor is not copying what the reader saw.
func copyToClipboard(text string) tea.Cmd {
	return tea.SetClipboard(ansi.Strip(text))
}

// pasteRequestedMsg starts the wait described on pasteHint.
type pasteRequestedMsg struct{}

// pasteUnansweredMsg fires when the terminal did not answer a clipboard read.
type pasteUnansweredMsg struct{}

// pasteHint waits briefly and then reports that nothing came back.
//
// A terminal that refuses an OSC 52 read does not say no — it says nothing at
// all. Without this the key is indistinguishable from a key that is not bound,
// and the reader has no way to learn that their terminal's own paste works
// fine. The delay is generous enough for a local answer and short enough that
// the hint still reads as a response to the keystroke.
func pasteHint() tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg {
		return pasteUnansweredMsg{}
	})
}

// plainTranscript is the conversation as text, without styling.
func plainTranscript(lines []string) string {
	return strings.TrimSpace(ansi.Strip(strings.Join(lines, "\n")))
}

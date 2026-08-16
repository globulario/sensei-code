package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

// transcript builds a real model holding more lines than any window can show.
func transcript(height int) Model {
	m := New(context.Background(), nil, nil, nil)
	m.width, m.height = 80, height
	m.lines = nil
	for i := 0; i < 200; i++ {
		m.lines = append(m.lines, fmt.Sprintf("line-%03d", i))
	}
	return m
}

func TestTheTopOfTheTranscriptIsReachable(t *testing.T) {
	// Without scrollback the earliest output is simply lost: the window showed
	// the last N rows and nothing could reach past them.
	m := transcript(24)
	if strings.Contains(m.View().Content, "line-000") {
		t.Fatal("the first line is visible at the bottom, so this proves nothing")
	}
	m.scrollUp = 10000 // further than the transcript is long
	if !strings.Contains(m.View().Content, "line-000") {
		t.Fatal("scrolling to the top does not reach the first line")
	}
}

func TestScrollIndicatorDoesNotHideALine(t *testing.T) {
	// Consuming a content row to announce that rows are hidden loses exactly
	// what the reader was trying to see.
	m := transcript(24)
	content := m.View().Content
	if !strings.Contains(content, "earlier line") {
		t.Fatalf("no indicator was shown for a transcript that overflows:\n%s", content)
	}
	// The newest line must survive the indicator taking a row.
	if !strings.Contains(content, "line-199") {
		t.Fatalf("the newest line was pushed out by the indicator:\n%s", content)
	}
}

func TestPagingUpFromTheBottomContinuesFromTheScreen(t *testing.T) {
	// Paging up must continue from what is on screen, not jump to row zero.
	m := transcript(24)
	scrolled, handled := m.scroll("pgup")
	if !handled {
		t.Fatal("pgup was not handled")
	}
	if scrolled.scrollUp == 0 {
		t.Fatal("paging up left the view following new output")
	}
	if strings.Contains(scrolled.View().Content, "line-000") {
		t.Fatal("one page up jumped to the very top instead of one screen back")
	}
	if !strings.Contains(scrolled.View().Content, "line-1") {
		t.Fatal("paging up showed nothing earlier")
	}
}

func TestReturningToTheBottomFollowsNewOutputAgain(t *testing.T) {
	m := transcript(24)
	m.scrollUp = 10000
	returned, handled := m.scroll("ctrl+g")
	if !handled || returned.scrollUp != 0 {
		t.Fatal("ctrl+g did not return to following new output")
	}
	if !strings.Contains(returned.View().Content, "line-199") {
		t.Fatal("returning to the bottom does not show the newest line")
	}
}

func TestScrollLeavesOtherKeysAlone(t *testing.T) {
	m := transcript(24)
	for _, key := range []string{"enter", "a", "ctrl+c", "shift+tab"} {
		if _, handled := m.scroll(key); handled {
			t.Fatalf("scroll swallowed %q, which belongs to another binding", key)
		}
	}
}

func TestResizingChangesHowMuchIsVisible(t *testing.T) {
	// The window must follow the terminal, or a resize appears to do nothing.
	small := len(strings.Split(transcript(16).View().Content, "\n"))
	large := len(strings.Split(transcript(40).View().Content, "\n"))
	if large <= small {
		t.Fatalf("a taller terminal rendered %d rows, not more than %d", large, small)
	}
}

func TestASingleLineScrollActuallyMoves(t *testing.T) {
	// The arrow keys appeared dead because the key handler estimated the bottom
	// with different arithmetic than the renderer, so one line was clamped back.
	m := transcript(24)
	moved, handled := m.scroll("up")
	if !handled {
		t.Fatal("up was not handled")
	}
	if moved.View().Content == m.View().Content {
		t.Fatal("scrolling up one line changed nothing on screen")
	}
}

func TestScrollingPastTheStartRestsAtTheStart(t *testing.T) {
	m := transcript(24)
	m.scrollUp = 1 << 20
	content := m.View().Content
	if !strings.Contains(content, "line-000") {
		t.Fatal("scrolling far past the start does not show the start")
	}
	if !strings.Contains(content, "top of the conversation") {
		t.Fatalf("the reader is not told they are at the start:\n%s", content)
	}
}

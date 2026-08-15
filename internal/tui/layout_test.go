package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func TestWrapRowsPadsEveryRowToFullWidth(t *testing.T) {
	const width = 40
	lines := []string{
		senseiStyle.Render("◆ SENSEI"),
		"  a short line",
		"  " + strings.Repeat("long ", 30),
		"",
	}
	for i, row := range wrapRows(lines, width) {
		if got := lipgloss.Width(row); got != width {
			t.Fatalf("row %d has visible width %d, want %d: %q", i, got, width, row)
		}
	}
}

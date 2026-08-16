package command

import (
	"strings"
	"testing"
)

func TestEveryCommandIsDocumented(t *testing.T) {
	// Help is generated from this table, so an undocumented command is an
	// undiscoverable one.
	for _, c := range Registry {
		if !strings.HasPrefix(c.Name, "/") {
			t.Fatalf("%q is not a slash command", c.Name)
		}
		if strings.TrimSpace(c.Summary) == "" || strings.TrimSpace(c.Detail) == "" {
			t.Fatalf("%s has no summary or detail", c.Name)
		}
		if c.Kind == "" {
			t.Fatalf("%s is in no group, so help would omit it", c.Name)
		}
	}
}

func TestLookupSeparatesArguments(t *testing.T) {
	c, arg, ok := Lookup("/focus internal/tui/model.go")
	if !ok || c.Name != "/focus" || arg != "internal/tui/model.go" {
		t.Fatalf("Lookup = %+v %q %v", c, arg, ok)
	}
	if _, _, ok := Lookup("add a version flag"); ok {
		t.Fatal("ordinary task text was taken for a command")
	}
}

func TestUnknownCommandIsNotTreatedAsATask(t *testing.T) {
	// "/reprot" as a task would reach the architect as a feature request.
	if !IsUnknown("/reprot") {
		t.Fatal("a mistyped command was not recognised as unknown")
	}
	if IsUnknown("/report") {
		t.Fatal("a real command was reported as unknown")
	}
	if IsUnknown("refactor the parser") {
		t.Fatal("plain text was treated as a command")
	}
}

func TestSuggestOffersTheClosestName(t *testing.T) {
	if got := Suggest("/reprot"); got != "/report" {
		t.Fatalf("Suggest = %q, want /report", got)
	}
	if got := Suggest("/zzz"); got != "" {
		t.Fatalf("Suggest = %q, want no guess for an unrelated name", got)
	}
}

func TestGroupsCoverEveryCommand(t *testing.T) {
	seen := 0
	for _, g := range Groups() {
		if strings.TrimSpace(g.Title) == "" {
			t.Fatal("a group has no title")
		}
		seen += len(g.Commands)
	}
	if seen != len(Registry) {
		t.Fatalf("groups list %d commands, registry has %d", seen, len(Registry))
	}
}

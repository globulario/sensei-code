package decision

import (
	"errors"
	"strings"
	"testing"
)

func TestUnlinkedDecisionIsRefusedRatherThanPadded(t *testing.T) {
	r := Record{Title: "do a thing", Rationale: "because"}
	if err := r.Validate(); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("Validate() = %v, want ErrNotLinked: an unlinked decision must not be recorded", err)
	}
}

func TestLinkedDecisionIsAccepted(t *testing.T) {
	r := Record{Title: "do a thing", Rationale: "because", SourceFiles: []string{"main.go"}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestArgsNeverRebuildTheGraph(t *testing.T) {
	r := Record{Title: "t", Rationale: "r", Invariants: []string{"inv.one"}, RepoRoot: "/repo"}
	args := strings.Join(r.Args(), " ")
	if !strings.Contains(args, "--no-rebuild") {
		t.Fatal("decision recording must not republish the graph")
	}
	for _, want := range []string{"--kind decision", "--related-invariant inv.one", "--target-repo /repo"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args missing %q: %s", want, args)
		}
	}
}

func TestArgsSkipEmptyLinks(t *testing.T) {
	r := Record{Title: "t", Rationale: "r", SourceFiles: []string{"", "  "}, Invariants: []string{"inv.one"}}
	if strings.Contains(strings.Join(r.Args(), "\x00"), "--source-file\x00\x00") {
		t.Fatal("blank source file was passed to sensei")
	}
}

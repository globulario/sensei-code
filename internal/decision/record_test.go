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

func TestSourceFilesAloneDoNotSatisfyContractFirst(t *testing.T) {
	// Sensei refuses a decision linked only to files, so accepting it here
	// would mean sending a request that is certain to be rejected.
	r := Record{Title: "t", Rationale: "r", SourceFiles: []string{"main.go"}}
	if err := r.Validate(); !errors.Is(err, ErrNotLinked) {
		t.Fatalf("Validate() = %v, want ErrNotLinked", err)
	}
}

func TestLinkedDecisionIsAccepted(t *testing.T) {
	r := Record{Title: "do a thing", Rationale: "because", Invariants: []string{"inv.one"}}
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

func TestGraphClassPrefixesAreStrippedFromLinks(t *testing.T) {
	// awareness_query returns "invariant:x"; the corpus uses the bare id.
	// Writing the prefixed form produces a link that resolves to nothing, which
	// validation reports as dangling: provenance that points nowhere.
	r := Record{
		Title: "t", Rationale: "r",
		Invariants: []string{"invariant:sensei_code.provider.credentials_remain_provider_owned"},
		Failures:   []string{"failure:sensei_code.some_failure"},
	}
	args := strings.Join(r.Args(), " ")
	if strings.Contains(args, "invariant:sensei_code") || strings.Contains(args, "failure:sensei_code") {
		t.Fatalf("a graph-prefixed id was written verbatim: %s", args)
	}
	if !strings.Contains(args, "--related-invariant sensei_code.provider.credentials_remain_provider_owned") {
		t.Fatalf("the normalised invariant id is missing: %s", args)
	}
}

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
	r := Record{Title: "do a thing", Rationale: "because", Invariants: []string{"inv.one"},
		Authority: Authority{Owner: Architectural}}
	if err := r.Validate(); err != nil {
		t.Fatal(err)
	}
}

// A decision with no stated authority is refused rather than filed with an
// assumed one. The assumption that used to be made here was "the human accepted
// this", which is the single most misleading thing the record could say once
// architectural decisions flow without a human in the loop.
func TestUnattributedDecisionIsRefused(t *testing.T) {
	r := Record{Title: "do a thing", Rationale: "because", Invariants: []string{"inv.one"}}
	if err := r.Validate(); err == nil {
		t.Fatal("a decision with no authority owner was accepted")
	}
}

// The provenance a future reader gets must distinguish the two authorities, and
// must never describe an architectural decision as a human acceptance.
func TestAuthorityProvenanceNamesTheRealOwner(t *testing.T) {
	arch := Authority{
		Owner:       Architectural,
		CertifiedBy: "github.com/globulario/sensei-code @ abc123",
		DecidedBy:   "ChatGPT",
		HumanGrant:  "task execution via /run",
	}.Describe()
	for _, want := range []string{"decision_authority: architectural", "decided_by: ChatGPT", "human_authorization: task execution via /run"} {
		if !strings.Contains(arch, want) {
			t.Errorf("architectural provenance missing %q: %s", want, arch)
		}
	}
	if strings.Contains(strings.ToLower(arch), "accepted by the human") {
		t.Errorf("an architectural decision claims human acceptance: %s", arch)
	}

	human := Authority{
		Owner:       Human,
		CertifiedBy: "github.com/globulario/sensei-code @ abc123",
		Condition:   "graph coverage is absent for the planned files",
		Resolution:  "Authorize the architectural change described above",
	}.Describe()
	for _, want := range []string{"decision_authority: human", "condition: graph coverage is absent", "resolution: Authorize"} {
		if !strings.Contains(human, want) {
			t.Errorf("human provenance missing %q: %s", want, human)
		}
	}
}

func TestArgsNeverRebuildTheGraph(t *testing.T) {
	r := Record{Title: "t", Rationale: "r", Invariants: []string{"inv.one"}, WriteRoot: "/repo",
		Authority: Authority{Owner: Architectural}}
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

// R15b: the decision is appended into owned state, never staged. Staging is
// what left decisions.yaml modified in the canonical checkout after every
// accepted run, so the argv must say --no-stage, and the write root must be
// the task-owned root under the ignored .sensei-code/ area -- with the
// repository and domain provenance carried separately from where it is written.
func TestDecisionIsWrittenToTheOwnedRootWithoutStaging(t *testing.T) {
	root := OwnedRoot("/canonical", "task-7")
	if root != "/canonical/.sensei-code/decisions/task-7" {
		t.Fatalf("owned root = %q, want it beneath the canonical .sensei-code/ area", root)
	}
	if got := PendingPath(root); got != root+"/docs/awareness/architecture/decisions.yaml" {
		t.Fatalf("pending path = %q, not where sensei propose appends beneath the root", got)
	}
	r := Record{Title: "t", Rationale: "r", Invariants: []string{"inv.one"}, Authority: Authority{Owner: Architectural},
		Repo: "github.com/x/y", Domain: "github.com/x/y", WriteRoot: root}
	args := r.Args()
	joined := strings.Join(args, " ")
	for _, want := range []string{"--no-stage", "--no-rebuild", "--target-repo " + root,
		"--repo github.com/x/y", "--domain github.com/x/y"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("args missing %q: %s", want, joined)
		}
	}
	if strings.Contains(joined, "--target-repo /canonical ") || args[len(args)-1] == "/canonical" {
		t.Fatalf("the decision targets the canonical checkout: %s", joined)
	}
}

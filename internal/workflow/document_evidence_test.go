package workflow

// T3: a governed architecture document is proven by the invariant that protects it, not
// by a Go mechanical recipe.
//
// The trace found an EXISTING authority model rather than a missing one.
// docs/awareness/invariants.yaml already protects a markdown file through
// `protects.files`, and high_risk_files.yaml states that Sensei automatically protects
// "files a governed invariant or failure mode directly names via
// protects.files/enforces_files/...". So the repository already decides which documents
// carry architecture authority, and it decides it in governed knowledge — not from the
// document's own text.
//
// Two anti-loopholes drive the design, and both come from the Coverage type's own
// doctrine: "'no invariant applies' means one thing when the graph has indexed the region
// and found nothing governing it, and the opposite thing when the graph has never
// looked. Both render as an empty invariant list, and only the first is evidence."
//
//	looked, found protection  -> architecture artifact, evidence = those invariant IDs
//	looked, found none        -> ordinary documentation, no Go recipe required
//	never looked              -> knowledge limit, NOT "ordinary"
//
// The third is the one that matters: absence of a Go anchor must never be read as proof
// that a document is ordinary.

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

func docAction(files ...string) Action {
	return Action{Stage: StageCandidateEdit, Files: files}
}

// 1 & 2. A markdown file a governed invariant protects is an architecture artifact, and
// the protecting invariant's identity is in the evidence record.
func TestAProtectedDocumentIsAnArchitectureArtifactWithIdentity(t *testing.T) {
	a := docAction("docs/architecture/durable-exchange-migration.md")
	a.DocumentEvidence = map[string][]string{
		"docs/architecture/durable-exchange-migration.md": {"sensei_code.workflow.durable_exchange"},
	}
	if got := classifyArtifact("docs/architecture/durable-exchange-migration.md"); got != classDocument {
		t.Fatalf("class = %q, want document", got)
	}
	r, open := documentGovernanceGap(a)
	if open {
		t.Fatalf("a protected document raised a gap: %s", r.Condition)
	}
	ev, ok := a.documentEvidenceFor("docs/architecture/durable-exchange-migration.md")
	if !ok || len(ev) != 1 || ev[0] != "sensei_code.workflow.durable_exchange" {
		t.Errorf("the protecting invariant identity is not in the evidence record: %v", ev)
	}
}

// 3. A document the graph has never looked at fails closed. This is the anti-loophole:
// no Go anchor is not proof of ordinariness.
func TestAnUnexaminedDocumentFailsClosed(t *testing.T) {
	a := docAction("docs/architecture/durable-exchange-migration.md")
	a.Unexamined = []string{"docs/architecture/durable-exchange-migration.md"}
	// No DocumentEvidence entry at all: nothing established whether it is governed.
	r, open := documentGovernanceGap(a)
	if !open {
		t.Fatal("a document whose governance was never established passed silently")
	}
	if r.Gap.Kind != gapDocumentGovernanceUnestablished {
		t.Fatalf("gap kind = %q", r.Gap.Kind)
	}
	if r.Basis != BasisLacksKnowledge {
		t.Errorf("basis = %v, want BasisLacksKnowledge", r.Basis)
	}
	remedy := remedyForGap(r.Gap, "/src/sensei-code", "github.com/globulario/sensei-code")
	if !strings.Contains(strings.ToLower(remedy), "invariant") {
		t.Errorf("the remedy does not name the evidence class that is missing: %s", remedy)
	}
}

// 4 & 5. Ordinary documentation the graph HAS looked at is not sent through Go coverage,
// and is not promoted to architecture authority.
func TestOrdinaryDocumentationIsNeitherCoveredNorPromoted(t *testing.T) {
	a := docAction("README.md", "docs/notes/scratch.md")
	// The graph looked and found no protecting invariant for either.
	a.DocumentEvidence = map[string][]string{"README.md": {}, "docs/notes/scratch.md": {}}
	if _, open := documentGovernanceGap(a); open {
		t.Error("ordinary documentation raised a governance gap")
	}
	// And they never enter the production-coverage question.
	if arch := a.architecturalFiles(); len(arch) != 0 {
		t.Errorf("documents entered the architectural (Go source coverage) set: %v", arch)
	}
	for _, f := range a.Files {
		if ev, ok := a.documentEvidenceFor(f); ok && len(ev) != 0 {
			t.Errorf("%s was credited with architecture evidence: %v", f, ev)
		}
	}
}

// 6. A Go mechanical anchor cannot satisfy a document's requirement.
func TestAMechanicalAnchorCannotSatisfyADocument(t *testing.T) {
	doc := "docs/architecture/durable-exchange-migration.md"
	a := docAction(doc)
	a.Unexamined = []string{doc}
	// A derived anchor naming the document — the shape that would be a category error.
	a.DerivedCoverage = []CoverageAnchor{{File: doc, Requirement: RequirementLockDiscipline, Describe: "irrelevant"}}
	r, open := documentGovernanceGap(a)
	if !open {
		t.Fatal("a Go mechanical anchor satisfied a document's architecture requirement")
	}
	if r.Gap.Kind != gapDocumentGovernanceUnestablished {
		t.Errorf("gap kind = %q", r.Gap.Kind)
	}
}

// 7. An empty or blank invariant identity is not an identity.
func TestABlankInvariantIdentityDoesNotSatisfyTheRequirement(t *testing.T) {
	doc := "docs/architecture/x.md"
	for name, ids := range map[string][]string{
		"blank":      {"   "},
		"empty list": {},
	} {
		a := docAction(doc)
		a.Unexamined = []string{doc}
		a.DocumentEvidence = map[string][]string{doc: ids}
		_, open := documentGovernanceGap(a)
		if name == "blank" && !open {
			t.Errorf("a blank invariant id satisfied the requirement")
		}
		if name == "empty list" && open {
			t.Errorf("an examined-but-unprotected document raised a gap: it is ordinary documentation")
		}
	}
}

// 8. Changing the path so no invariant protects it changes the result visibly rather than
// preserving stale coverage.
func TestMovingADocumentOutOfProtectionChangesTheResult(t *testing.T) {
	before := "docs/architecture/durable-exchange-migration.md"
	after := "docs/notes/durable-exchange-migration.md"
	a := docAction(before)
	a.DocumentEvidence = map[string][]string{before: {"inv.one"}}
	if _, open := documentGovernanceGap(a); open {
		t.Fatal("the protected path raised a gap")
	}
	// Same document, new path, and the evidence record is keyed by path: nothing carries
	// over, and the new path is unexamined until the graph says otherwise.
	b := docAction(after)
	b.Unexamined = []string{after}
	if _, open := documentGovernanceGap(b); !open {
		t.Error("moving the document out of protection silently preserved its coverage")
	}
}

// 9. Multiple protecting invariants are deterministic and explicit.
func TestMultipleProtectingInvariantsAreDeterministic(t *testing.T) {
	doc := "docs/architecture/x.md"
	a := docAction(doc)
	a.DocumentEvidence = map[string][]string{doc: {"inv.b", "inv.a", "inv.c"}}
	first, _ := a.documentEvidenceFor(doc)
	second, _ := a.documentEvidenceFor(doc)
	if strings.Join(first, ",") != strings.Join(second, ",") {
		t.Fatalf("evidence identity is not deterministic: %v vs %v", first, second)
	}
	if strings.Join(first, ",") != "inv.a,inv.b,inv.c" {
		t.Errorf("identity is not sorted, so a record's identity depends on map order: %v", first)
	}
}

// 10. An unsupported artifact kind fails closed rather than passing.
func TestAnUnsupportedArtifactKindFailsClosed(t *testing.T) {
	for _, f := range []string{"assets/logo.png", "Makefile", "scripts/deploy.sh"} {
		if got := classifyArtifact(f); got != classUnsupported {
			t.Errorf("%s classified as %q, want unsupported", f, got)
		}
		a := docAction(f)
		a.Unexamined = []string{f}
		r, open := unsupportedArtifactGap(a)
		if !open {
			t.Errorf("%s passed silently", f)
			continue
		}
		if r.Basis != BasisLacksKnowledge {
			t.Errorf("%s basis = %v", f, r.Basis)
		}
	}
}

// A document must never be asked for a Go source property, which is the T3 half of the
// same rule T2 established for tests.
func TestDocumentsNeverJoinTheProductionCoverageQuestion(t *testing.T) {
	a := docAction("internal/x/thing.go", "docs/architecture/x.md", "README.md")
	arch := a.architecturalFiles()
	for _, f := range arch {
		if strings.HasSuffix(f, ".md") {
			t.Errorf("a document entered the architectural set: %v", arch)
		}
	}
	if len(arch) != 1 || arch[0] != "internal/x/thing.go" {
		t.Errorf("architectural set = %v, want only the Go production file", arch)
	}
}

// The probe must actually FEED the classifier. Without this the classifier is correct and
// starved: every document reads as "the graph never looked", permanently — fail-closed,
// but a different defect from the one T3 fixes.
func TestThePerFileProbeRecordsDocumentEvidence(t *testing.T) {
	// The package's own healthy, graph-identified authority fixture: a probe must be
	// bound to the region's generation, and a hand-built Authority is not certifiable.
	region := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_OK",`+
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":2},`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},`+
		identifiedAuthority+`}`)
	answer := func(invariants []sensei.Invariant, proven bool) sensei.PreflightDecision {
		c := sensei.Coverage{FileCount: 1, IndexedFileCount: 1, Sufficient: proven, DirectAnchorCount: 0}
		if proven {
			c.DirectAnchorCount = 1
		}
		return sensei.PreflightDecision{Status: sensei.PreflightOK, Authority: region.Authority,
			Coverage: c, DirectInvariants: invariants}
	}
	ask := func(f string) (sensei.PreflightDecision, error) {
		switch f {
		case "docs/architecture/governed.md":
			return answer([]sensei.Invariant{{ID: "inv.protects.governed"}}, true), nil
		case "docs/notes/ordinary.md":
			return answer(nil, true), nil
		}
		return answer(nil, true), nil
	}
	files := []string{"docs/architecture/governed.md", "docs/notes/ordinary.md"}
	_, docs, err := unexaminedFiles(StageCandidateEdit, 2, ask, files, region)
	if err != nil {
		t.Fatalf("unexaminedFiles: %v", err)
	}
	ids, asked := docs["docs/architecture/governed.md"]
	if !asked || len(ids) != 1 || ids[0] != "inv.protects.governed" {
		t.Errorf("the protecting invariant was not recorded: %v (asked=%v)", ids, asked)
	}
	// Asked and unprotected must be an EMPTY entry, not a missing one: that is the
	// difference between ordinary documentation and never having looked.
	ids, asked = docs["docs/notes/ordinary.md"]
	if !asked {
		t.Error("an examined unprotected document has no entry, so it is indistinguishable from never looked")
	}
	if len(ids) != 0 {
		t.Errorf("an unprotected document was credited with %v", ids)
	}
}

// The probe set includes documents and excludes tests, for stated reasons.
func TestTheProbeSetCoversProductionAndDocumentsOnly(t *testing.T) {
	a := docAction("internal/x/thing.go", "internal/x/thing_test.go", "docs/architecture/x.md", "Makefile")
	got := strings.Join(a.probeSet(), " ")
	for _, want := range []string{"internal/x/thing.go", "docs/architecture/x.md"} {
		if !strings.Contains(got, want) {
			t.Errorf("probe set omits %s: %s", want, got)
		}
	}
	for _, unwanted := range []string{"_test.go", "Makefile"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("probe set includes %s: %s", unwanted, got)
		}
	}
}

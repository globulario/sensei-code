package workflow

// T2: a test artifact is governed by test evidence, not by a production-source
// mechanical property.
//
// The trace, at the exact boundary. Action.architecturalFiles() (consequence.go) returns
// "every planned file not under an operational grant", and unexaminedCoverageGap then
// asks the graph for source coverage over that set. A planned *_test.go leaves the set
// only by earning a testEditGrant, whose predicate is
// EXISTING_TEST_EDIT_ADMISSIBLE(F, S, W):
//
//	F is a planned *_test.go positively present at W
//	S is a planned file IN F's DIRECTORY that a derived anchor covers at W
//	F and S declare the SAME PACKAGE at W (an external p_test is foreign, no grant)
//
// The predicate's own contract says an unestablished case "leaves F ungranted, silently
// to routing". Silently is the defect: ungranted means the file falls back into the
// ARCHITECTURAL set, and a test file is then reported as production source the graph has
// not examined — `coverage-unexamined`, whose remedy talks about graph coverage that no
// graph state could ever supply for a test file.
//
// Measured on W3: cmd/sensei-code/control_test.go was reported exactly that way.
//
// The repair is classification, not exclusion. A test file keeps a governance
// requirement; what changes is WHICH requirement, and what the gap says when it is
// unmet.

import (
	"strings"
	"testing"
)

func planAction(files ...string) Action {
	return Action{Files: files, Unexamined: files}
}

// 1. A covered production file plus a valid grant on its neighbouring test: the test is
// governed and raises no coverage gap.
func TestAGrantedTestIsGovernedAndRaisesNoCoverageGap(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	a.OperationalAuthority = []string{"internal/x/thing_test.go"}
	a.Unexamined = []string{"internal/x/thing_test.go"} // production file is examined
	if got := a.architecturalFiles(); len(got) != 1 || got[0] != "internal/x/thing.go" {
		t.Fatalf("architectural set = %v, want only the production file", got)
	}
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if open {
		t.Errorf("a granted test opened a gap: %s / %v", r.Condition, r.Gap)
	}
}

// 2. Production source still answers to source coverage.
func TestProductionSourceStillRequiresSourceCoverage(t *testing.T) {
	a := planAction("internal/x/thing.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("an unexamined production file raised no gap")
	}
	if r.Gap.Kind != "coverage-unexamined" {
		t.Errorf("gap kind = %q, want coverage-unexamined for production source", r.Gap.Kind)
	}
}

// 3. THE DEFECT. A test file with no valid grant must fail closed as a TEST-GOVERNANCE
// condition, never as a production coverage gap.
func TestAnUngrantedTestIsATestGovernanceGapNotACoverageGap(t *testing.T) {
	a := planAction("cmd/sensei-code/control_test.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("an ungranted test file raised no gap at all; a test must not become governance-free")
	}
	if r.Gap.Kind == "coverage-unexamined" {
		t.Fatalf("a test file was reported as unexamined production source: %s", r.Condition)
	}
	if r.Gap.Kind != "test-governance-unestablished" {
		t.Fatalf("gap kind = %q, want test-governance-unestablished", r.Gap.Kind)
	}
	if r.Basis != BasisLacksKnowledge {
		t.Errorf("basis = %v, want BasisLacksKnowledge", r.Basis)
	}
	// The remedy must not send anyone to the graph: no graph state can create a test
	// grant, so recommending a rebuild would be a true-sounding instruction that cannot
	// work.
	remedy := remedyForGap(r.Gap, "/src/sensei-code", "github.com/globulario/sensei-code")
	for _, forbidden := range []string{"sensei build", "sensei import", "rebuild", "refresh"} {
		if strings.Contains(strings.ToLower(remedy), forbidden) {
			t.Errorf("the remedy recommends %q for a test-governance gap: %s", forbidden, remedy)
		}
	}
	if !strings.Contains(remedy, "control_test.go") {
		t.Errorf("the remedy does not name the artifact: %s", remedy)
	}
}

// 4. A mixed plan is covered by two different evidence classes at once.
func TestAMixedPlanUsesTwoEvidenceClasses(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	a.OperationalAuthority = []string{"internal/x/thing_test.go"}
	a.Unexamined = nil // production file examined
	if _, open := unexaminedCoverageGap(a, blindSpotReading{}); open {
		t.Error("a plan whose production file is examined and whose test is granted still opened a gap")
	}
}

// 5. Naming a production file *_test.go cannot silently make it exempt. Under existing
// repository semantics a *_test.go IS a test artifact — so the protection is that it
// still needs test-governance evidence, and is never simply skipped.
func TestATestSuffixDoesNotCreateAnExemptArtifact(t *testing.T) {
	a := planAction("internal/x/sneaky_test.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("a *_test.go file with no grant was exempted entirely")
	}
	if r.Gap.Kind != "test-governance-unestablished" {
		t.Errorf("gap kind = %q", r.Gap.Kind)
	}
}

// 6. Non-test Go files that merely LOOK testish are production source.
func TestFixtureAndGeneratedFilesAreNotTests(t *testing.T) {
	for _, f := range []string{
		"internal/x/testdata_helper.go", // "test" in the name, not a _test.go
		"internal/x/test_helpers.go",    // prefix, not suffix
		"internal/x/thing.pb.go",        // generated
	} {
		a := planAction(f)
		r, open := unexaminedCoverageGap(a, blindSpotReading{})
		if !open {
			t.Errorf("%s raised no gap", f)
			continue
		}
		if r.Gap.Kind != "coverage-unexamined" {
			t.Errorf("%s was classified as a test artifact (kind=%s)", f, r.Gap.Kind)
		}
	}
}

// 7 & 8 are properties of the GRANT predicate, which is where the production-evidence
// relation lives. They are asserted against it directly in testedit_test.go's existing
// suite; here we assert the classifier consequence: a plan whose grant list does not
// include the test file gets the test-governance gap, which is exactly what "the
// supporting production evidence was lost" produces.
func TestLosingTheGrantProducesTheTestGovernanceGap(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	a.OperationalAuthority = nil // the grant is gone: its covered subject no longer derives
	a.Unexamined = []string{"internal/x/thing_test.go"}
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("losing the grant left the test ungoverned")
	}
	if r.Gap.Kind != "test-governance-unestablished" {
		t.Errorf("gap kind = %q, want the test-governance gap", r.Gap.Kind)
	}
	if len(r.Gap.Scope) != 1 || r.Gap.Scope[0] != "internal/x/thing_test.go" {
		t.Errorf("gap scope = %v, want only the test file", r.Gap.Scope)
	}
}

// A plan with BOTH an unexamined production file and an ungranted test must not hide
// either: the production gap is the one that blocks, and the test gap must still be
// visible in its own terms rather than absorbed.
func TestBothGapsAreReportedInTheirOwnTerms(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("no gap at all")
	}
	if r.Gap.Kind != "coverage-unexamined" {
		t.Errorf("with production source unexamined the blocking gap should be the source one, got %q", r.Gap.Kind)
	}
	for _, f := range r.Gap.Scope {
		if strings.HasSuffix(f, "_test.go") {
			t.Errorf("a test file was pulled into the production coverage scope: %v", r.Gap.Scope)
		}
	}
}

// The disposal path must carry the TEST gap's own scope and remedy. Reading the
// unexamined ARCHITECTURAL files here was right while one out-of-band gap existed; a
// test-governance gap is about artifacts deliberately absent from that set, so it would
// have found nothing missing and escalated to a human for evidence no human can supply.
func TestTheTestGovernanceGapIsDisposedWithItsOwnScopeAndRemedy(t *testing.T) {
	if closureOwnerFor(gapTestGovernanceUnestablished) != closureOwnerOutOfBand {
		t.Fatal("a test-governance gap is not closable by reasoning and must be typed out-of-band")
	}
	gap := GapIdentity{Kind: gapTestGovernanceUnestablished, Scope: []string{"cmd/sensei-code/control_test.go"}}
	remedy := remedyForGap(gap, "/src/sensei-code", "github.com/globulario/sensei-code")
	if !strings.Contains(remedy, "no graph operation can establish this") {
		t.Errorf("the remedy does not say the graph cannot supply this: %s", remedy)
	}
	low := strings.ToLower(remedy)
	if !strings.Contains(low, "same") || !strings.Contains(low, "package") || !strings.Contains(low, "director") {
		t.Errorf("the remedy does not name the relation that is missing: %s", remedy)
	}
	// And the source remedy is unchanged for the source gap.
	src := remedyForGap(GapIdentity{Kind: "coverage-unexamined", Scope: []string{"a.go"}}, "/src/x", "example.com/d")
	if !strings.Contains(src, "sensei import --refresh") {
		t.Errorf("the source remedy changed: %s", src)
	}
}

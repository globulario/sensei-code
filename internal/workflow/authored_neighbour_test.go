package workflow

// THE REPAIR: a test grant depends on the production neighbour being GOVERNED, not on
// which legitimate instrument established that fact.
//
// EXISTING_TEST_EDIT_ADMISSIBLE(F,S,W) is unchanged in meaning — a planned same-package
// test F beside a planned, governed production neighbour S at world W. What was wrong is
// that "governed" silently meant "covered by a derived anchor", so the authored
// instrument did not count.
//
// Measured at the pinned W3 world:
//
//	cmd/sensei-code/control.go       PREFLIGHT_STATUS_OK, sufficient=true, 2 anchors,
//	                                 invariant sensei_code.ghwebhook.ingress_and_control_
//	                                 surfaces_stay_separate
//	cmd/sensei-code/control_test.go  EMPTY, no anchors
//
// control.go is governed. It is governed by an authored invariant rather than a
// derivation, and the grant refused it for that reason alone. That is the recorded
// "two coverage instruments, one word" defect, reaching the grant predicate.
//
// The authored path is deliberately the SAME per-file relation preflight already uses to
// call control.go governed: one owner, one interpretation. Nothing directory-level, nothing
// package-wide, nothing that merely mentions a filename.

import (
	"context"
	"strings"
	"testing"
)

const teInvariant = "sensei_code.ghwebhook.ingress_and_control_surfaces_stay_separate"

func teFiles() map[string]string { return map[string]string{teS: teSSrc, teF: teFSrc} }

// authoredFor builds per-file authored governance at teWorld.
func authoredFor(file string, ids ...string) authoredEvidence {
	return authoredEvidence{World: teWorld, ByFile: map[string][]string{file: ids}}
}

// 1. The derived path still works, unchanged.
func TestADerivedNeighbourStillGrantsItsTest(t *testing.T) {
	grants, reasons := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, teRead(teFiles()))
	if len(grants) != 1 {
		t.Fatalf("the derived path stopped granting: %v", reasons)
	}
	g := grants[0]
	if g.Path != teF || g.Covering != teS {
		t.Errorf("grant = %+v", g)
	}
	if g.CoveringEvidence != evidenceDerived {
		t.Errorf("evidence class = %q, want %q", g.CoveringEvidence, evidenceDerived)
	}
	if len(g.CoveringIdentity) == 0 {
		t.Error("the derived grant carries no evidence identity")
	}
}

// 2. THE REPAIR. An authored-invariant-governed neighbour now grants its planned test.
func TestAnAuthoredNeighbourGrantsItsTest(t *testing.T) {
	grants, reasons := testEditGrants(context.Background(), teWorld, []string{teS, teF},
		nil, authoredFor(teS, teInvariant), teRead(teFiles()))
	if len(grants) != 1 {
		t.Fatalf("an authored-governed neighbour did not grant its test: %v", reasons)
	}
	g := grants[0]
	if g.CoveringEvidence != evidenceAuthored {
		t.Errorf("evidence class = %q, want %q", g.CoveringEvidence, evidenceAuthored)
	}
	// 9. The identity must survive into the grant: a green result with the provenance
	// erased is exactly the shape that cannot be audited later.
	if len(g.CoveringIdentity) != 1 || g.CoveringIdentity[0] != teInvariant {
		t.Errorf("evidence identity = %v, want the invariant id", g.CoveringIdentity)
	}
	if g.World != teWorld {
		t.Errorf("grant world = %q", g.World)
	}
}

// 3. An authored invariant protecting some OTHER file grants nothing.
func TestAnAuthoredInvariantOnAnotherFileGrantsNothing(t *testing.T) {
	grants, reasons := testEditGrants(context.Background(), teWorld, []string{teS, teF},
		nil, authoredFor("modfile/unrelated.go", teInvariant), teRead(teFiles()))
	if len(grants) != 0 {
		t.Fatalf("an invariant on another file granted the test: %+v", grants)
	}
	if len(reasons) == 0 {
		t.Error("the refusal is silent")
	}
}

// 4. Authored evidence from a DIFFERENT world does not grant.
func TestAuthoredEvidenceFromAnotherWorldDoesNotGrant(t *testing.T) {
	stale := authoredEvidence{World: "0000000000000000000000000000000000000000", ByFile: map[string][]string{teS: {teInvariant}}}
	grants, reasons := testEditGrants(context.Background(), teWorld, []string{teS, teF}, nil, stale, teRead(teFiles()))
	if len(grants) != 0 {
		t.Fatalf("stale authored evidence granted a test: %+v", grants)
	}
	if !strings.Contains(strings.Join(reasons, " "), "world") {
		t.Errorf("the refusal does not name the world mismatch: %v", reasons)
	}
}

// 5. The relationship itself is unchanged: a neighbour in ANOTHER directory grants nothing,
// authored or derived.
func TestTheDirectoryRelationshipIsUnchanged(t *testing.T) {
	far := "other/rule.go"
	files := map[string]string{far: teSSrc, teF: teFSrc}
	grants, _ := testEditGrants(context.Background(), teWorld, []string{far, teF},
		nil, authoredFor(far, teInvariant), teRead(files))
	if len(grants) != 0 {
		t.Fatalf("a neighbour outside the test's directory granted it: %+v", grants)
	}
}

// 6. A foreign/external test package is still refused, on the authored path too.
func TestAForeignTestPackageIsStillRefusedOnTheAuthoredPath(t *testing.T) {
	foreign := "//go:build go1.20\n\npackage modfile_test\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"
	files := map[string]string{teS: teSSrc, teF: foreign}
	grants, reasons := testEditGrants(context.Background(), teWorld, []string{teS, teF},
		nil, authoredFor(teS, teInvariant), teRead(files))
	if len(grants) != 0 {
		t.Fatalf("a foreign-package test was granted: %+v", grants)
	}
	if !strings.Contains(strings.Join(reasons, " "), "foreign-package") {
		t.Errorf("the refusal does not name the package mismatch: %v", reasons)
	}
}

// 7. Neither instrument: no grant, and the caller's typed gap follows.
func TestNeitherDerivedNorAuthoredLeavesTheTestUngranted(t *testing.T) {
	grants, reasons := testEditGrants(context.Background(), teWorld, []string{teS, teF}, nil, authoredEvidence{}, teRead(teFiles()))
	if len(grants) != 0 {
		t.Fatalf("a test was granted with no production governance at all: %+v", grants)
	}
	if len(reasons) == 0 {
		t.Error("the refusal is silent")
	}
}

// 8. Removing the authored evidence makes the test fail again — the grant is a function of
// the evidence, not a sticky fact.
func TestRemovingAuthoredEvidenceRevokesTheGrant(t *testing.T) {
	with, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, nil, authoredFor(teS, teInvariant), teRead(teFiles()))
	if len(with) != 1 {
		t.Fatal("precondition: the authored path grants")
	}
	without, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, nil, authoredEvidence{}, teRead(teFiles()))
	if len(without) != 0 {
		t.Fatalf("the grant survived removal of the evidence that justified it: %+v", without)
	}
}

// A blank authored identity is not an identity: the file cannot be called governed by a
// list of empty strings.
func TestABlankAuthoredIdentityDoesNotGrant(t *testing.T) {
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, nil, authoredFor(teS, "  "), teRead(teFiles()))
	if len(grants) != 0 {
		t.Fatalf("a blank invariant id granted the test: %+v", grants)
	}
}

// Both instruments present: the grant records ONE of them explicitly rather than a merged
// boolean, and derived is preferred as the stronger, recomputed fact.
func TestWhenBothInstrumentsGovernTheGrantNamesWhichOneItUsed(t *testing.T) {
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredFor(teS, teInvariant), teRead(teFiles()))
	if len(grants) != 1 {
		t.Fatal("no grant")
	}
	if grants[0].CoveringEvidence != evidenceDerived {
		t.Errorf("evidence class = %q; a recomputed derivation outranks an authored anchor", grants[0].CoveringEvidence)
	}
	if len(grants[0].CoveringIdentity) == 0 {
		t.Error("identity erased")
	}
}

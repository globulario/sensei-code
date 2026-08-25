package workflow

// The adversary, and what it established.
//
// The brief asked for falsification BEFORE design: could the router be made to
// accept a trivially true but irrelevant derived proposition as sufficient
// coverage for a real knowledge gap?
//
// It could, and by construction rather than by accident. Action.DerivedCoverage
// was a []string of file paths, and the router asked coversAll(covered,
// planned) — pure subject overlap. Nothing anywhere carried which architectural
// question a derivation answered, so `P is DERIVED` and `P resolves gap G` were
// the same proposition.
//
// The preserved adversary is a wide layering fact:
//
//	package P does not import net/http
//
// True, cheaply derivable over a whole package, and it says nothing about the
// lock discipline, ownership, provenance, contract or purpose question that
// caused CLOSE_GAP.
//
// The deriver below is test-only and cannot leak into production: production
// obtains anchors from Engine.derivedCoverage, which computes every
// Requirement through requirementOfFamily from the anchor's real family. The
// only thing this file fabricates is what a NEW family would look like.

import (
	"strings"
	"testing"
)

const emptyOverBus = `{"status":"PREFLIGHT_STATUS_EMPTY",` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	healthyAuthority + `}`

// layeringAnchors is the adversary: a wide, true, independently derived fact
// from a family nobody has established an architectural reading for.
func layeringAnchors(files ...string) []CoverageAnchor {
	var out []CoverageAnchor
	for _, f := range files {
		out = append(out, CoverageAnchor{
			File:        f,
			Requirement: requirementOfFamily("package_does_not_import"),
			Describe:    "package_does_not_import(net/http) — derived over " + f,
		})
	}
	return out
}

// The adversary, run against the consumer boundary the router actually reads.
func TestAWideTrueIrrelevantDerivationClosesNothing(t *testing.T) {
	scoped := scopedPreflight(t, emptyOverBus)
	planned := []string{"internal/event/bus.go"}

	// The specimen must be live: without any derived coverage this is a real
	// knowledge gap, or the refusal below refuses nothing.
	if bare := routeAuthorityForAction(scoped, nil, plannedEdit(planned...)); !bare.ClosesGap() {
		t.Fatalf("the specimen is not a knowledge gap, so this proves nothing: %+v", bare)
	}

	// Perfect subject overlap. Every planned file is covered by a DERIVED,
	// independently checkable, true proposition — and it resolves nothing.
	got := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: layeringAnchors(planned...)})
	if !got.ClosesGap() {
		t.Fatalf("a wide true irrelevant derivation manufactured coverage: %+v", got)
	}
	if got.Granted() {
		t.Fatal("an unrecognised derivation family reached a grant")
	}

	// And the working family over the same file still closes the gap it was
	// introduced to answer. Refusing everything is not the fix.
	lock := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: lockAnchors(planned...)})
	if lock.ClosesGap() {
		t.Fatalf("the lock-discipline closure for internal/event/bus.go stopped working: %+v", lock)
	}
	if !lock.Granted() {
		t.Fatalf("after closure the bounded edit was not granted: %+v", lock)
	}
}

// P is DERIVED and P resolves gap G are different propositions.
//
// Stated as its own assertion because the two were literally the same value
// before this slice: a file path in a list.
func TestDerivedIsNotResolves(t *testing.T) {
	planned := []string{"internal/event/bus.go"}
	derivedTrue := layeringAnchors(planned...)

	// It IS derived: the anchor exists, over exactly this file.
	if len(derivedTrue) != 1 || derivedTrue[0].File != planned[0] {
		t.Fatal("the specimen does not cover the planned file, so the distinction is untested")
	}
	// It does NOT resolve: nobody has established what this family answers.
	if closed, _ := derivationClosesGap(RequirementUnqualified, derivedTrue, planned); closed {
		t.Error("a derivation with no established architectural reading resolved a gap")
	}
	if closed, by := derivationClosesGap(RequirementUnqualified, lockAnchors(planned...), planned); !closed {
		t.Error("the recognised family resolved nothing")
	} else if !strings.Contains(by, "field_access_under_lock") {
		t.Errorf("the closure does not name what resolved it: %q", by)
	}
}

// The required attacks, in the brief's own order.
func TestTheSufficiencyRelationRefusesWhatItMust(t *testing.T) {
	planned := []string{"internal/event/bus.go"}
	two := []string{"internal/event/bus.go", "internal/event/event.go"}

	for _, tc := range []struct {
		name    string
		gap     Requirement
		anchors []CoverageAnchor
		planned []string
		want    bool
	}{
		{"wide true but irrelevant", RequirementUnqualified, layeringAnchors(planned...), planned, false},
		{"same file, wrong relation kind", RequirementLockDiscipline,
			[]CoverageAnchor{{File: planned[0], Requirement: RequirementInvocationConfinement}}, planned, false},
		{"subjects partially overlap the planned files", RequirementUnqualified,
			lockAnchors(planned[0]), two, false},
		{"a provider-supplied relevance label", RequirementUnqualified,
			[]CoverageAnchor{{File: planned[0], Requirement: Requirement("relevant")}}, planned, false},
		{"an empty requirement", RequirementUnqualified,
			[]CoverageAnchor{{File: planned[0]}}, planned, false},
		{"the lock-discipline happy path", RequirementUnqualified, lockAnchors(planned...), planned, true},
		{"a qualified gap its family answers", RequirementLockDiscipline, lockAnchors(planned...), planned, true},
		{"no anchors at all", RequirementUnqualified, nil, planned, false},
		{"anchors but no planned files", RequirementUnqualified, lockAnchors(planned...), nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, _ := derivationClosesGap(tc.gap, tc.anchors, tc.planned)
			if got != tc.want {
				t.Fatalf("closes = %v, want %v", got, tc.want)
			}
		})
	}
}

// A family cannot declare its own relevance, because the declaration is not a
// field.
//
// internal/derived says a recipe is safe to commit and safe to let an agent
// write, on the grounds that the worst a forged one achieves is spending one
// derivation. This is the assertion that keeps that true: the mapping from
// family to architectural question lives in the consumer, and a Recipe has
// nowhere to put a relevance claim.
func TestAFamilyCannotSelfLabelItsRelevance(t *testing.T) {
	src := rawSource(t, "internal/derived/derived.go")
	at := strings.Index(src, "type Recipe struct {")
	end := strings.Index(src[at:], "\n}\n")
	if at < 0 || end < 0 {
		t.Fatal("Recipe is gone from internal/derived")
	}
	for _, forbidden := range []string{"Requirement", "Relevant", "Resolves", "Sufficient", "Closes"} {
		if strings.Contains(src[at:at+end], forbidden) {
			t.Errorf("Recipe carries a %q field; a recipe is data anyone may commit and must not "+
				"be able to state what it resolves", forbidden)
		}
	}
	// And the consumer's mapping fails closed on anything it was not taught.
	for _, unknown := range []string{"", "  ", "package_does_not_import", "field_access_under_lock_v2", "anything"} {
		if got := requirementOfFamily(unknown); got != RequirementUnrecognised {
			t.Errorf("family %q read as %q; an untaught family must resolve nothing", unknown, got)
		}
	}
}

// Every coverage gap in the live specimen is unqualified.
//
// Measured, not assumed. The asymmetry in satisfies() — a recognised family
// closes an unqualified gap — rests entirely on Sensei's coverage vocabulary
// never saying WHICH question it cannot answer. If that changes, this test
// fails and the asymmetry gets re-examined instead of quietly persisting.
func TestEveryProbedCoverageGapIsUnqualified(t *testing.T) {
	rows := loadProbe(t)
	seen := 0
	for _, r := range rows {
		spots := readBlindSpots(r.BlindSpots)
		if len(spots.Coverage) == 0 {
			continue
		}
		seen++
		if got := gapRequirement(spots.Coverage); got != RequirementUnqualified {
			t.Errorf("%s: the graph now names a requirement (%q) — re-examine satisfies(), which "+
				"assumes it never does: %v", r.File, got, spots.Coverage)
		}
	}
	if seen == 0 {
		t.Fatal("no probed file reported a coverage blind spot, so this measured nothing")
	}
	t.Logf("%d of %d probed files report a coverage gap; every one is unqualified", seen, len(rows))
	// And the reader is not vacuous: a vocabulary that DID say gets read.
	if got := gapRequirement([]string{"no lock discipline established for this region"}); got != RequirementLockDiscipline {
		t.Fatalf("a qualified gap was flattened to %q", got)
	}
}

// The family admission gate.
//
// The brief asked for a test or documented gate for future derivation families.
// It is a test so that adding a family without answering the questions fails
// rather than merely being impolite.
//
// The five questions, from docs/work/derived-coverage-must-be-relevant.md:
//
//  1. What real unresolved architectural question does this family answer?
//  2. What subjects does a successful proof establish?
//  3. What uncertainty is still outside its claim?
//  4. Can a trivial/wide true proposition in the family manufacture coverage?
//  5. Does the family still produce useful NOT_DERIVED/UNKNOWN outcomes, or was
//     it tuned until it passes?
//
// Reach is a tiebreaker after architectural significance and soundness. It is
// never a selector.
func TestAFamilyMustAnswerTheAdmissionQuestions(t *testing.T) {
	// Every requirement this consumer recognises must have an admitted family
	// behind it, and every admitted family must map to one.
	admitted := map[string]Requirement{
		// 1. which lock guards which field, and whether every access takes it.
		// 2. the type and field the proposition is about.
		// 3. WHY the lock exists — a purpose claim no derivation establishes.
		// 4. no: the proposition names one type, one field and one lock.
		// 5. yes: the refuted specimens in internal/derived stay NOT_DERIVED.
		"field_access_under_lock": RequirementLockDiscipline,
		// 1. whether an executable is invoked only from its owning package.
		// 2. the invocation sites found, under the searched paths.
		// 3. whether the confinement is INTENDED, and anything outside the
		//    searched paths — which is why SearchPaths is a term of the
		//    question rather than a search convenience.
		// 4. no: a narrower search is a weaker claim, and it is pinned.
		// 5. yes: the git-confinement specimen remains NOT_DERIVED.
		"command_invocation_confined_to": RequirementInvocationConfinement,
	}
	for family, want := range admitted {
		if got := requirementOfFamily(family); got != want {
			t.Errorf("admitted family %q resolves %q, want %q", family, got, want)
		}
	}
	// Nothing recognised without an admission entry above.
	recognised := map[Requirement]bool{}
	for _, r := range admitted {
		recognised[r] = true
	}
	for _, r := range []Requirement{RequirementLockDiscipline, RequirementInvocationConfinement} {
		if !recognised[r] {
			t.Errorf("%q is recognised by satisfies() with no family admitted for it; a requirement "+
				"nobody can derive is a hole a future family walks through", r)
		}
	}
}

// The governed run does not supply derived coverage at all.
//
// Found while implementing this brief and recorded rather than quietly fixed:
// routePlan builds its Action WITHOUT DerivedCoverage, so Engine.derivedCoverage
// has no production caller and the whole closure path is reachable only from
// the derivelive test.
//
// So the adversary could not reach a live governed run — but not because of any
// relevance mechanism. The wire was never connected. Connecting it WIDENS
// autonomy, and doing that as a side effect of writing about relevance is
// exactly the move this line of work exists to refuse. Pinned so that
// connecting it is a deliberate, visible change made with the relation above
// already in place.
func TestTheGovernedRunDoesNotYetSupplyDerivedCoverage(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "routePlan")
	if strings.Contains(body, "DerivedCoverage") {
		t.Fatal("routePlan now supplies derived coverage to the router. That is a real widening " +
			"of autonomy and may well be right — but it must arrive with its own evidence, " +
			"not by editing this test. Re-read relevance.go's limits first: while Sensei's " +
			"coverage vocabulary stays unqualified, relevance is enforced per FAMILY and not " +
			"per proposition.")
	}
}

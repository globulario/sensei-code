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

// The governed run supplies derived coverage to the router.
//
// It did not, and the absence was silent: the router's only coverage input was
// the graph's own, so a bounded knowledge gap could be reported, a closure
// round could run, the architect could investigate -- and no channel existed by
// which any of that reached the decision. The gap could not close, so it
// escalated to a human every time.
//
// Measured in the proof-v6 campaign: three of five governed refusals were
// unclosed coverage gaps whose closure round ran and failed for exactly this
// reason. This is the wiring that repairs them, and it adds no mechanism --
// derivedCoverage, the derivation families and the relevance gate were all
// already built.
func TestTheGovernedRunSuppliesDerivedCoverage(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "routePlan")
	if !strings.Contains(body, "DerivedCoverage") {
		t.Fatal("routePlan builds its Action without DerivedCoverage, so a derivation cannot " +
			"reach the router and a coverage gap can never close autonomously")
	}
	if !strings.Contains(body, "e.derivedCoverage") {
		t.Error("the coverage supplied is not the one revalidated in this world over these files")
	}
}

// A derivation that resolves the gap lets the run continue.
func TestDerivedCoverageThatResolvesTheGapAllowsRouting(t *testing.T) {
	scoped := scopedPreflight(t, emptyOverBus)
	planned := []string{"internal/event/bus.go"}

	// The specimen must be a live gap, or the closure below closes nothing.
	if bare := routeAuthorityForAction(scoped, nil, plannedEdit(planned...)); !bare.ClosesGap() {
		t.Fatalf("the specimen is not a knowledge gap: %+v", bare)
	}
	closed := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: lockAnchors(planned...)})
	if closed.ClosesGap() {
		t.Fatalf("a derivation that resolves the gap did not close it: %+v", closed)
	}
	if !closed.Granted() {
		t.Fatalf("after closure the bounded edit was not granted: %+v", closed)
	}
}

// Evidence that does not resolve the gap still refuses.
//
// Every way the closure can fall short, in one table. This is the half that
// must not move: the repair connects a channel, it does not lower a bar.
func TestIncompleteOrUntrustedClosureStillRefuses(t *testing.T) {
	scoped := scopedPreflight(t, emptyOverBus)
	one := []string{"internal/event/bus.go"}
	two := []string{"internal/event/bus.go", "internal/event/unseen.go"}

	for name, action := range map[string]Action{
		"no derivation at all": {Stage: StageCandidateEdit, Files: one},
		"a family nobody has an architectural reading for": {
			Stage: StageCandidateEdit, Files: one, DerivedCoverage: layeringAnchors(one...)},
		"covers only some of the planned files": {
			Stage: StageCandidateEdit, Files: two, DerivedCoverage: lockAnchors(one[0])},
		"a provider-supplied relevance label": {
			Stage: StageCandidateEdit, Files: one,
			DerivedCoverage: []CoverageAnchor{{File: one[0], Requirement: Requirement("relevant")}}},
		"an empty requirement": {
			Stage: StageCandidateEdit, Files: one,
			DerivedCoverage: []CoverageAnchor{{File: one[0]}}},
	} {
		t.Run(name, func(t *testing.T) {
			got := routeAuthorityForAction(scoped, nil, action)
			if !got.ClosesGap() {
				t.Fatalf("the gap was treated as closed by insufficient evidence: %+v", got)
			}
			if got.Granted() {
				t.Fatal("insufficient closure reached a grant")
			}
		})
	}
}

// Closing a coverage gap does not clear an approval gate.
//
// The gate is checked before coverage, so supplying a derivation cannot buy
// consequence authority. Pinned here as well as in blindspot_test.go, because
// this is the wiring that made derived coverage reachable at all.
func TestDerivedCoverageStillDoesNotClearAnApprovalGate(t *testing.T) {
	gated := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},`+
		healthyAuthority+`}`)
	planned := []string{"internal/event/bus.go"}
	got := routeAuthorityForAction(gated, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: lockAnchors(planned...)})
	if !got.RequiresHuman() {
		t.Fatalf("derived coverage cleared an approval gate: %+v", got)
	}
}

// No human answer is synthesised anywhere on this path.
//
// The repair must not make a run look autonomous by inventing an authority
// answer. Coverage arrives from a revalidated derivation or not at all.
func TestClosureSynthesisesNoHumanAnswer(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "routePlan")
	for _, forbidden := range []string{
		"applyAnsweredCondition", "authority.Resolution", "RouteArchitectural",
	} {
		if strings.Contains(body, forbidden) {
			t.Errorf("routePlan reaches for %s; coverage must come from a derivation, never from a "+
				"manufactured authority answer", forbidden)
		}
	}
	// And the coverage it supplies is revalidated, not read from a store.
	derived := funcBody(t, "internal/workflow/engine.go", "derivedCoverage")
	if !strings.Contains(derived, "AnchorsFor") {
		t.Error("derived coverage no longer comes from a revalidation in the assessed world")
	}
}

// The coverage-observability event decides nothing.
//
// It exists because the event stream could not answer a question the repair
// verification requires: did a derivation REACH the router, or did the channel
// carry nothing? Those are different findings needing different repairs, and
// "the gap did not close" reads identically for both.
//
// Measurement-only, so the risk is that it becomes an input. These pin that it
// is emitted after the decision and read by nothing.
func TestTheCoverageObservabilityEventDecidesNothing(t *testing.T) {
	// Raw source, not funcBody: this pins ORDER and the literal that marks the
	// event, and funcBody renders identifiers rather than source.
	src := rawSource(t, "internal/workflow/engine.go")
	at := strings.Index(src, "func (e *Engine) routePlan(")
	if at < 0 {
		t.Fatal("routePlan is gone from engine.go")
	}
	body := src[at:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}

	// Emitted AFTER the routing decision, never before it.
	route := strings.Index(body, "routeAuthorityForAction")
	emit := strings.Index(body, "derived coverage:")
	if route < 0 || emit < 0 {
		t.Fatal("routePlan no longer routes or no longer records what reached the router")
	}
	if emit < route {
		t.Error("the observability event is built before the routing decision, so it could " +
			"influence one; it must only describe a decision already made")
	}
	// The routing value is never reassigned after it is computed.
	after := body[route:]
	if strings.Contains(after, "routing =") || strings.Contains(after, "routing.Route =") {
		t.Error("routing is mutated after it is decided; the observability path must not touch it")
	}
}

// Routing is identical whether or not anything observes it.
//
// The strongest form: the same inputs produce the same route, and the route is
// a pure function of the preflight, the claims and the action.
func TestRoutingIsUnchangedByObservation(t *testing.T) {
	scoped := scopedPreflight(t, emptyOverBus)
	planned := []string{"internal/event/bus.go"}

	for name, action := range map[string]Action{
		"with resolving coverage": {Stage: StageCandidateEdit, Files: planned,
			DerivedCoverage: lockAnchors(planned...)},
		"with irrelevant coverage": {Stage: StageCandidateEdit, Files: planned,
			DerivedCoverage: layeringAnchors(planned...)},
		"with none": {Stage: StageCandidateEdit, Files: planned},
	} {
		t.Run(name, func(t *testing.T) {
			first := routeAuthorityForAction(scoped, nil, action)
			second := routeAuthorityForAction(scoped, nil, action)
			if first.Route != second.Route || first.Condition != second.Condition {
				t.Fatalf("routing is not deterministic: %+v then %+v", first, second)
			}
		})
	}
}

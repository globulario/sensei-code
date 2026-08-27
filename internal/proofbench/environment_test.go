package proofbench

// Attacks on environment identity.
//
// A proof-v6 COLD wave halted after one arm: the MCP the product spawned was
// reaching a throwaway DEV graph while the CLI reached the authoritative one.
// The product refused correctly; the harness had no way to know before spending
// twenty minutes finding out. These pin both directions.

import (
	"strings"
	"testing"
)

func authoritative() GraphIdentity {
	return GraphIdentity{
		Authoritative: true,
		Provenance:    "BUILD_PROVENANCE_STATE_STAMPED",
		Freshness:     "GRAPH_FRESHNESS_STATE_CURRENT",
		SeedState:     "SEED_STATE_CURRENT",
		Composition:   "complete",
		GraphCommit:   "a4034c78de600ad14f388343224492a5d722459c",
		SeedDigest:    "fee1748e8a8b25cc11ad92157e5fee26d101a25b9a0b3ed7de4600fa2f3626de",
		TripleCount:   158349,
	}
}

// An authoritative graph lets the wave start.
func TestAnAuthoritativeGraphAllowsTheWaveToStart(t *testing.T) {
	g := authoritative()
	if !g.Usable() {
		t.Fatalf("an authoritative stamped graph was refused: %s", g)
	}
	if err := RequireEnvironment(g); err != nil {
		t.Fatalf("RequireEnvironment: %v", err)
	}
}

// A DEV, drifted or unusable authority halts BEFORE any provider runs.
func TestAWrongOrDriftedAuthorityHaltsTheBenchmark(t *testing.T) {
	for name, mutate := range map[string]func(*GraphIdentity){
		// The exact observed failure: a scratchpad dev graph.
		"dev provenance":    func(g *GraphIdentity) { g.Provenance = "BUILD_PROVENANCE_STATE_DEV" },
		"not authoritative": func(g *GraphIdentity) { g.Authoritative = false },
		"partial workspace": func(g *GraphIdentity) { g.Composition = "partial" },
	} {
		t.Run(name, func(t *testing.T) {
			g := authoritative()
			mutate(&g)
			if g.Usable() {
				t.Fatal("an unusable graph was accepted")
			}
			err := RequireEnvironment(g)
			if err == nil {
				t.Fatal("the wave was allowed to start against an unusable graph")
			}
			if !strings.Contains(err.Error(), "MEASUREMENT_INTEGRITY_FAILURE") {
				t.Errorf("the halt was not reported as a measurement-integrity failure: %v", err)
			}
			// And it must never read as a product score.
			for _, forbidden := range []string{"REFUSED", "INCORRECT"} {
				if strings.Contains(err.Error(), forbidden) {
					t.Errorf("an environment failure was dressed as %s: %v", forbidden, err)
				}
			}
			if !strings.Contains(err.Error(), "not a Sensei-code result") {
				t.Errorf("the failure does not disclaim being a product result: %v", err)
			}
		})
	}
}

// Identity is compared by authority, never by size.
//
// Triple count is diagnostic: two graphs can share a count and differ in
// everything that matters, and the count that changed underneath the v6 wave
// was a symptom rather than the thing to check.
func TestIdentityIsAuthorityNotSize(t *testing.T) {
	a := authoritative()

	// Same authority, different size: still the same graph.
	grown := a
	grown.TripleCount = a.TripleCount + 5000
	if !a.Same(grown) {
		t.Error("a graph was treated as different because its triple count moved")
	}
	if err := RequireStableEnvironment(a, grown); err != nil {
		t.Errorf("a stable authority was halted for a size change: %v", err)
	}

	// Same size, different authority: a different graph.
	swapped := a
	swapped.GraphCommit = "0000000000000000000000000000000000000000"
	if a.Same(swapped) {
		t.Error("two different authorities compared equal")
	}
	err := RequireStableEnvironment(a, swapped)
	if err == nil {
		t.Fatal("the wave continued after its authoritative graph was replaced")
	}
	if !strings.Contains(err.Error(), "changed during the wave") {
		t.Errorf("the halt does not say the authority moved: %v", err)
	}

	// A seed change is also a different graph.
	reseeded := a
	reseeded.SeedDigest = "deadbeef"
	if a.Same(reseeded) {
		t.Error("a reseeded graph compared equal to the original")
	}

	// And drift into an unusable state halts even if the identity matches.
	degraded := a
	degraded.Provenance = "BUILD_PROVENANCE_STATE_DEV"
	if err := RequireStableEnvironment(a, degraded); err == nil {
		t.Error("a wave continued after its graph stopped vouching for itself")
	}
}

//go:build derivelive

// The vertical run through the engine's own code path.
//
//	SENSEI_BIN=/path/to/sensei go test -tags derivelive ./internal/workflow/ -v
//
// Engine.derivedCoverage is what routePlan calls. Nothing here substitutes a
// fixture for it: the recipes come off disk, the derivation is a real `sensei
// derive` over the real repository at its real HEAD, and the result is fed to
// the real router.
package workflow

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/gitx"
)

// requireDeriveCapable fails loudly when the binary cannot derive.
//
// Permanent regression coverage for a false green that actually happened: an
// older installed sensei answered UNKNOWN to everything, so both refusal
// assertions passed while measuring nothing. A green check can overclaim, and
// evidence is only meaningful once the instrument is shown capable of observing
// the property the conclusion depends on.
func requireDeriveCapable(t *testing.T) {
	t.Helper()
	out, err := exec.Command(senseiBinary(), "derive", "-h").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "typed architectural proposition") {
		t.Fatalf("%s cannot derive, so every outcome would be UNKNOWN and the refusals below would "+
			"pass without testing anything. Set SENSEI_BIN to a build carrying `derive`.", senseiBinary())
	}
}

func liveEngine(t *testing.T) *Engine {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return &Engine{Repo: gitx.Repo{Root: strings.TrimSuffix(wd, "/internal/workflow")}}
}

// The chain: recipe on disk -> revalidated at the current world -> coverage ->
// the router no longer reports a knowledge gap.
func TestLiveTheCommittedRecipeClosesTheGapThroughTheEnginePath(t *testing.T) {
	requireDeriveCapable(t)
	e := liveEngine(t)
	planned := []string{"internal/event/bus.go"}

	covered := e.derivedCoverage(context.Background(), "", planned, nil)
	if len(covered) != 1 || covered[0].File != planned[0] {
		t.Fatalf("the engine path produced no coverage for a file a committed recipe covers: %v", covered)
	}
	// And it carries what that derivation is able to answer, computed from the
	// family rather than read off the wire.
	if covered[0].Requirement != RequirementLockDiscipline {
		t.Fatalf("the live anchor resolves %q; the committed recipe is field_access_under_lock",
			covered[0].Requirement)
	}

	// A real EMPTY preflight over that file, routed with what the engine
	// actually computed.
	scoped := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},`+
		healthyAuthority+`}`)

	without := routeAuthorityForAction(scoped, nil, Action{Stage: StageCandidateEdit, Files: planned})
	if !without.ClosesGap() {
		t.Fatalf("baseline: an uncovered EMPTY region should be a bounded gap, got %+v", without)
	}

	with := routeAuthorityForAction(scoped, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: covered})
	if with.ClosesGap() {
		t.Fatalf("the derived fact did not close the gap: %+v", with)
	}
	if !with.Granted() {
		t.Fatalf("after closure the bounded edit was not granted: %+v", with)
	}
	t.Logf("gap closed by derivation: %v", covered)
}

// Attack: a file no recipe covers stays uncovered through the same path.
func TestLiveAnUncoveredFileStaysUncovered(t *testing.T) {
	requireDeriveCapable(t)
	e := liveEngine(t)
	covered := e.derivedCoverage(context.Background(), "", []string{"internal/workflow/authority.go"}, nil)
	if len(covered) != 0 {
		t.Fatalf("a file no committed recipe covers was reported covered: %v", covered)
	}
}

// Attack: derived coverage cannot clear an explicit approval gate, through the
// real coverage computation.
func TestLiveDerivedCoverageDoesNotClearAnApprovalGate(t *testing.T) {
	requireDeriveCapable(t)
	e := liveEngine(t)
	planned := []string{"internal/event/bus.go"}
	covered := e.derivedCoverage(context.Background(), "", planned, nil)
	if len(covered) == 0 {
		t.Fatal("setup: expected real coverage")
	}
	gated := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",`+
		`"change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},`+
		healthyAuthority+`}`)
	got := routeAuthorityForAction(gated, nil, Action{
		Stage: StageCandidateEdit, Files: planned, DerivedCoverage: covered})
	if !got.RequiresHuman() {
		t.Fatalf("derived coverage cleared an approval gate: %+v", got)
	}
}

// Attack: no recipe file at all means no coverage, not an error and not a pass.
func TestLiveNoRecipesMeansNoCoverage(t *testing.T) {
	requireDeriveCapable(t)
	e := &Engine{Repo: gitx.Repo{Root: t.TempDir()}}
	if covered := e.derivedCoverage(context.Background(), "", []string{"internal/event/bus.go"}, nil); len(covered) != 0 {
		t.Fatalf("a repository with no recipes reported coverage: %v", covered)
	}
}

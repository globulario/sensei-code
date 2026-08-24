package derived

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// fake stands in for `sensei derive` so the CONTROL PLANE can be tested without
// a Sensei binary. The real thing is exercised separately in the live test
// below; what this checks is that sensei-code connects the pieces without
// inventing a shortcut, which is where integrations historically go wrong.
type fake struct {
	outcome Outcome
	world   string
	inputs  []string
	calls   int
}

func (f *fake) Revalidate(_ context.Context, _, revision string, r Recipe) Result {
	f.calls++
	res := Result{Recipe: r, Outcome: f.outcome, Detail: "fake"}
	if f.outcome != Derived {
		return res
	}
	world := f.world
	if world == "" {
		world = revision
	}
	res.Anchor = &Anchor{recipe: r, world: world, files: f.inputs}
	return res
}

func busRecipe() Recipe {
	return Recipe{Kind: "field_access_under_lock", Dir: "internal/event",
		Type: "Bus", Field: "subs", Lock: "mu"}
}

// THE COLLAPSE. A recipe on disk must contribute nothing on its own.
//
// This is the whole safety property of the integration: if merely holding a
// recipe produced coverage, a file anyone can write would close a real gap.
func TestARecipeAloneProvidesNoCoverage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "recipes.json")
	body, _ := json.Marshal(map[string]any{"recipes": []Recipe{busRecipe()}})
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	recipes, err := LoadRecipes(path)
	if err != nil || len(recipes) != 1 {
		t.Fatalf("recipes=%v err=%v", recipes, err)
	}

	// Loaded, and worth nothing until something recomputes it.
	covered, uncovered := CoveredFiles(nil, "world", []string{"internal/event/bus.go"})
	if len(covered) != 0 || len(uncovered) != 1 {
		t.Fatalf("a recipe with no anchor produced coverage: covered=%v", covered)
	}

	// And the type offers no way to turn one into an anchor.
	rt := reflect.TypeOf(Anchor{})
	for i := 0; i < rt.NumField(); i++ {
		if rt.Field(i).IsExported() {
			t.Fatalf("Anchor.%s is exported; an anchor must come only from a successful revalidation",
				rt.Field(i).Name)
		}
	}
}

// The legitimate path: revalidate, then anchor, then coverage.
func TestCoverageComesFromRevalidationInTheAssessedWorld(t *testing.T) {
	f := &fake{outcome: Derived, inputs: []string{"internal/event/bus.go", "internal/event/event.go"}}
	anchors, results := AnchorsFor(context.Background(), f, ".", "base-sha", []Recipe{busRecipe()})
	if f.calls != 1 {
		t.Fatalf("revalidation ran %d times; coverage must never be decided without it", f.calls)
	}
	if len(anchors) != 1 || results[0].Outcome != Derived {
		t.Fatalf("anchors=%d results=%+v", len(anchors), results)
	}
	covered, uncovered := CoveredFiles(anchors, "base-sha", []string{"internal/event/bus.go"})
	if len(covered) != 1 || len(uncovered) != 0 {
		t.Fatalf("covered=%v uncovered=%v", covered, uncovered)
	}
}

// Attack 1: a forged recipe. Anyone may write the file; nobody may make the
// derivation succeed by writing it.
func TestAForgedRecipeClosesNoGap(t *testing.T) {
	f := &fake{outcome: NotDerived}
	anchors, _ := AnchorsFor(context.Background(), f, ".", "world", []Recipe{busRecipe()})
	if len(anchors) != 0 {
		t.Fatal("a recipe that did not derive produced an anchor")
	}
	covered, _ := CoveredFiles(anchors, "world", []string{"internal/event/bus.go"})
	if len(covered) != 0 {
		t.Fatal("a forged recipe closed a coverage gap")
	}
}

// Attack 2: the B specimen. A purpose claim has no derivation, so it is UNKNOWN
// and anchors nothing — and UNKNOWN must not be treated as a weaker yes.
func TestThePurposeClaimClosesNoGap(t *testing.T) {
	f := &fake{outcome: Unknown}
	b := busRecipe()
	b.Kind = "lock_purpose_serialize_map"
	anchors, results := AnchorsFor(context.Background(), f, ".", "world", []Recipe{b})
	if len(anchors) != 0 {
		t.Fatal("a purpose claim anchored coverage")
	}
	if results[0].Outcome != Unknown {
		t.Fatalf("outcome %s", results[0].Outcome)
	}
}

// Attack 3, the temporal hole. An anchor established at the base must not cover
// a candidate assessment, even in the same process, even in memory.
//
// This is the failure that survives every serialization guarantee: nothing was
// stored, nothing was forged, and an in-memory value simply got reused across
// two worlds.
func TestAnAnchorFromTheBaseCannotCoverACandidate(t *testing.T) {
	f := &fake{outcome: Derived, world: "base-sha", inputs: []string{"internal/event/bus.go"}}
	anchors, _ := AnchorsFor(context.Background(), f, ".", "base-sha", []Recipe{busRecipe()})
	if len(anchors) != 1 {
		t.Fatal("setup")
	}
	// Same anchor, different world.
	covered, uncovered := CoveredFiles(anchors, "candidate-sha", []string{"internal/event/bus.go"})
	if len(covered) != 0 {
		t.Fatal("an anchor derived at the base covered a candidate world")
	}
	if len(uncovered) != 1 {
		t.Fatalf("uncovered=%v", uncovered)
	}
}

// Coverage extends exactly as far as the derivation looked. A file the
// derivation never opened is not covered by it, whatever the recipe names.
func TestCoverageReachesOnlyTheFilesTheDerivationRead(t *testing.T) {
	f := &fake{outcome: Derived, inputs: []string{"internal/event/bus.go"}}
	anchors, _ := AnchorsFor(context.Background(), f, ".", "w", []Recipe{busRecipe()})
	covered, uncovered := CoveredFiles(anchors, "w",
		[]string{"internal/event/bus.go", "internal/event/never_read.go"})
	if len(covered) != 1 || covered[0] != "internal/event/bus.go" {
		t.Fatalf("covered=%v", covered)
	}
	if len(uncovered) != 1 || uncovered[0] != "internal/event/never_read.go" {
		t.Fatalf("uncovered=%v", uncovered)
	}
}

// The envelope travels with the coverage claim, so it never reads stronger than
// the derivation behind it.
func TestTheDescriptionCarriesTheDerivationEnvelope(t *testing.T) {
	a := Anchor{recipe: busRecipe(), world: "abcdef0123456789", files: []string{"internal/event/bus.go"},
		scope: []string{"access through an alias", "cross-goroutine lock state"}}
	d := a.Describe()
	for _, want := range []string{"unobserved", "alias", "internal/event/bus.go"} {
		if !strings.Contains(d, want) {
			t.Errorf("description drops %q: %q", want, d)
		}
	}
}

func TestMissingRecipeFileIsNoRecipesNotAnError(t *testing.T) {
	got, err := LoadRecipes(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil || got != nil {
		t.Fatalf("got=%v err=%v", got, err)
	}
}

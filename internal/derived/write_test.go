package derived

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpRecipes(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "derived_recipes.json")
	if err := os.WriteFile(p, []byte(`{"_comment":["keep me"],"recipes":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func goodRecipe() Recipe {
	return Recipe{Kind: "field_access_under_lock", Dir: "internal/event",
		Type: "Bus", Field: "subs", Lock: "mu", Why: "read Publish and Subscribe"}
}

func prov() Provenance { return Provenance{OriginTask: "task-1", OriginGap: "no anchored rules apply"} }

// THE PROPERTY THIS WHOLE DESIGN RESTS ON.
//
// A closure round may record a question it believes and is WRONG about. Nothing
// bad happens: the derivation answers it, the answer is no, and no anchor
// exists. The graph never holds the belief.
func TestAFalseQuestionIsSafeBecauseNothingTrustsIt(t *testing.T) {
	false_ := Recipe{Kind: "field_access_under_lock", Dir: "internal/event",
		Type: "Bus", Field: "subs", Lock: "notALockThatExists",
		Why: "plausible and wrong: this is the case that must cost nothing"}
	p := tmpRecipes(t)
	added, err := Append(p, false_, prov(), []string{"internal/event/bus.go"})
	if err != nil || !added {
		t.Fatalf("a wrong-but-honest question must be recordable: added=%v err=%v", added, err)
	}
	// It is written. Now: written is not believed. Only a DERIVED outcome
	// becomes an anchor, so a question that does not hold anchors nothing.
	anchors, results := AnchorsFor(t.Context(), stubRevalidator{outcome: NotDerived},
		t.TempDir(), "world", []Recipe{false_})
	if len(anchors) != 0 {
		t.Fatal("a question that does not hold produced coverage; the graph now mirrors a belief")
	}
	if len(results) != 1 || results[0].Outcome != NotDerived {
		t.Fatal("the failed derivation must still be reported, or the loop is only ever seen succeeding")
	}
}

// Encounter 1 writes. Encounter 2 benefits. Never the same run.
func TestARecipeCannotCoverTheRunThatWroteIt(t *testing.T) {
	r := goodRecipe()
	r.Provenance = &Provenance{OriginTask: "task-1"}
	if got := ExcludingTask([]Recipe{r}, "task-1"); len(got) != 0 {
		t.Fatal("the run that wrote a question was covered by it; that is self-approval")
	}
	if got := ExcludingTask([]Recipe{r}, "task-2"); len(got) != 1 {
		t.Fatal("a later task must benefit, or nothing compounds")
	}
	// A human-committed recipe has no provenance and covers everything.
	if got := ExcludingTask([]Recipe{goodRecipe()}, "task-1"); len(got) != 1 {
		t.Fatal("a human-committed recipe must not be excluded")
	}
}

// It is a question writer, not a knowledge writer.
func TestOnlyAnswerableQuestionsAreAccepted(t *testing.T) {
	for _, bad := range []struct {
		name string
		r    Recipe
	}{
		{"an invented kind", Recipe{Kind: "component_owns_mutation_of", Dir: "internal/event"}},
		{"an asserted rule", Recipe{Kind: "invariant", Dir: "internal/event"}},
		{"a missing term", Recipe{Kind: "field_access_under_lock", Dir: "internal/event", Type: "Bus"}},
		{"an escaping path", Recipe{Kind: "field_access_under_lock", Dir: "../../etc",
			Type: "Bus", Field: "subs", Lock: "mu"}},
	} {
		if err := Validate(bad.r, []string{"internal/event/bus.go"}); err == nil {
			t.Fatalf("%s was accepted", bad.name)
		}
	}
	if err := Validate(goodRecipe(), []string{"internal/event/bus.go"}); err != nil {
		t.Fatalf("a well-formed answerable question was refused: %v", err)
	}
}

// A round may not widen its own future authority over somewhere it never looked.
func TestAQuestionMustBeAboutTheRegionInvestigated(t *testing.T) {
	elsewhere := goodRecipe()
	elsewhere.Dir = "internal/workflow"
	if err := Validate(elsewhere, []string{"internal/event/bus.go"}); err == nil {
		t.Fatal("a closure round on internal/event wrote a question about internal/workflow")
	}
}

// Forty encounters with one unknown region must produce one question.
func TestTheSameQuestionIsNotAccumulated(t *testing.T) {
	p := tmpRecipes(t)
	region := []string{"internal/event/bus.go"}
	if added, err := Append(p, goodRecipe(), prov(), region); err != nil || !added {
		t.Fatalf("first write: added=%v err=%v", added, err)
	}
	reworded := goodRecipe()
	reworded.Why = "a different investigation phrased it differently"
	reworded.Dir = "internal/event/" // trailing slash, same question
	added, err := Append(p, reworded, Provenance{OriginTask: "task-2"}, region)
	if err != nil {
		t.Fatal(err)
	}
	if added {
		t.Fatal("the same question was recorded twice because its prose differed")
	}
	got, err := LoadRecipes(p)
	if err != nil || len(got) != 1 {
		t.Fatalf("expected exactly one question, got %d (%v)", len(got), err)
	}
	if got[0].Provenance == nil || got[0].Provenance.OriginTask != "task-1" {
		t.Fatal("provenance must record the round that first asked, and survive a redundant write")
	}
}

// Provenance is stamped, not authored, and the file's other contents survive.
func TestProvenanceIsStampedAndTheFileIsPreserved(t *testing.T) {
	p := tmpRecipes(t)
	forged := goodRecipe()
	forged.Provenance = &Provenance{OriginTask: "a-task-that-never-ran",
		OriginGap: "an investigation that never happened"}
	if _, err := Append(p, forged, prov(), []string{"internal/event/bus.go"}); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadRecipes(p)
	if got[0].Provenance.OriginTask != "task-1" {
		t.Fatalf("the agent authored its own provenance: %q", got[0].Provenance.OriginTask)
	}
	if got[0].Provenance.WrittenAt == "" {
		t.Fatal("no write time was stamped")
	}
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), "keep me") {
		t.Fatal("the file's explanatory comment was destroyed by the writer")
	}
	var doc map[string]json.RawMessage
	if json.Unmarshal(b, &doc) != nil {
		t.Fatal("the writer produced unparseable JSON")
	}
}

// A recipe with no origin task cannot be written, or future-only is unenforceable.
func TestAQuestionWithoutAnOriginTaskIsRefused(t *testing.T) {
	if _, err := Append(tmpRecipes(t), goodRecipe(), Provenance{}, []string{"internal/event/bus.go"}); err == nil {
		t.Fatal("a question with no origin task was written; the future-only rule cannot be enforced")
	}
}

type stubRevalidator struct{ outcome Outcome }

func (s stubRevalidator) Revalidate(_ context.Context, _, _ string, r Recipe) Result {
	return Result{Recipe: r, Outcome: s.outcome}
}

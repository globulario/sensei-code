//go:build derivelive

// The live vertical run: a real `sensei derive`, the real repository, real
// commits.
//
//	go test -tags derivelive ./internal/derived/ -v
//
// It needs a sensei binary built from a revision carrying `sensei derive`, so
// it is tagged rather than skipped: a silent skip is how an integration test
// stops being run at all.
package derived

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func head(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse: %v", err)
	}
	return strings.TrimSpace(string(out))
}

func bin(t *testing.T) string {
	t.Helper()
	b := strings.TrimSpace(os.Getenv("SENSEI_BIN"))
	if b == "" {
		b = "sensei"
	}
	// A binary without `derive` answers UNKNOWN to everything, which makes the
	// two refusal tests below pass for entirely the wrong reason. That happened
	// on the first live run against an older installed sensei: "the forged
	// recipe anchored nothing" was true, and meaningless.
	//
	// So capability is established before any expectation rests on it.
	out, err := exec.Command(b, "derive", "-h").CombinedOutput()
	if err != nil && !strings.Contains(string(out), "typed architectural proposition") {
		t.Fatalf("%s has no `derive` command, so every outcome would be UNKNOWN and the refusal "+
			"assertions here would pass without testing anything. Build one from a revision that "+
			"carries it and set SENSEI_BIN.", b)
	}
	return b
}

// The happy path, end to end, against the real derivation.
func TestLiveRecipeAnchorsRealCoverage(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := strings.TrimSuffix(root, "/internal/derived")
	world := head(t, repo)

	cli := CLI{Bin: bin(t)}
	anchors, results := AnchorsFor(context.Background(), cli, repo, world, []Recipe{busRecipe()})
	if len(anchors) != 1 {
		t.Fatalf("no anchor from a real derivation: %+v", results)
	}
	if anchors[0].World() != world {
		t.Fatalf("anchor world %s, assessed %s", anchors[0].World(), world)
	}
	covered, uncovered := CoveredFiles(anchors, world, []string{"internal/event/bus.go"})
	if len(covered) != 1 || len(uncovered) != 0 {
		t.Fatalf("covered=%v uncovered=%v", covered, uncovered)
	}
	t.Logf("DERIVED: %s", anchors[0].Describe())
}

// Attack 2 against the real binary: the B specimen reaches UNKNOWN and anchors
// nothing.
func TestLiveThePurposeClaimIsUnknown(t *testing.T) {
	root, _ := os.Getwd()
	repo := strings.TrimSuffix(root, "/internal/derived")
	b := busRecipe()
	b.Kind = "lock_purpose_serialize_map"
	anchors, results := AnchorsFor(context.Background(), CLI{Bin: bin(t)}, repo, head(t, repo), []Recipe{b})
	if len(anchors) != 0 {
		t.Fatal("the purpose claim anchored real coverage")
	}
	if results[0].Outcome != Unknown {
		t.Fatalf("outcome %s: %s", results[0].Outcome, results[0].Detail)
	}
}

// Attack 1 against the real binary: a recipe naming something that is not there
// derives nothing.
func TestLiveAForgedRecipeDerivesNothing(t *testing.T) {
	root, _ := os.Getwd()
	repo := strings.TrimSuffix(root, "/internal/derived")
	forged := busRecipe()
	forged.Field = "nosuchfield"
	anchors, results := AnchorsFor(context.Background(), CLI{Bin: bin(t)}, repo, head(t, repo), []Recipe{forged})
	if len(anchors) != 0 {
		t.Fatalf("a forged recipe anchored coverage: %+v", results)
	}
}

// Restart durability: a recipe recovered from disk in a fresh process
// revalidates to the same anchor. Without this the ratchet exists only inside
// one run.
func TestLiveARecoveredRecipeRevalidatesTheSame(t *testing.T) {
	root, _ := os.Getwd()
	repo := strings.TrimSuffix(root, "/internal/derived")
	world := head(t, repo)
	recipes, err := LoadRecipes(repo + "/docs/awareness/derived_recipes.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(recipes) == 0 {
		t.Fatal("no committed recipes; the ratchet has nothing durable in it")
	}
	anchors, results := AnchorsFor(context.Background(), CLI{Bin: bin(t)}, repo, world, recipes)
	if len(anchors) == 0 {
		t.Fatalf("committed recipes anchored nothing: %+v", results)
	}
	for _, a := range anchors {
		t.Logf("recovered and revalidated: %s", a.Describe())
	}
}

// The event.go specimen, through the live control plane.
//
// The derivation reads internal/event/event.go to resolve types and says
// nothing about it. Coverage must reach bus.go and stop.
//
// This test is willing to LOSE apparent coverage in exchange for truthfulness,
// which is the whole point: before the fix, both files were covered and the
// numbers looked better.
func TestLiveCoverageStopsAtTheSubjectFile(t *testing.T) {
	root, _ := os.Getwd()
	repo := strings.TrimSuffix(root, "/internal/derived")
	world := head(t, repo)

	anchors, results := AnchorsFor(context.Background(), CLI{Bin: bin(t)}, repo, world, []Recipe{busRecipe()})
	if len(anchors) != 1 {
		t.Fatalf("no anchor: %+v", results)
	}
	files := anchors[0].Files()
	if len(files) != 1 || files[0] != "internal/event/bus.go" {
		t.Fatalf("coverage extent = %v, want only internal/event/bus.go", files)
	}

	covered, uncovered := CoveredFiles(anchors, world,
		[]string{"internal/event/bus.go", "internal/event/event.go"})
	if len(covered) != 1 || covered[0] != "internal/event/bus.go" {
		t.Fatalf("covered=%v", covered)
	}
	if len(uncovered) != 1 || uncovered[0] != "internal/event/event.go" {
		t.Fatalf("event.go was covered by a proposition that says nothing about it: uncovered=%v", uncovered)
	}
	t.Logf("read more than it covers, correctly: covered=%v uncovered=%v", covered, uncovered)
}

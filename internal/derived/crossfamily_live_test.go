//go:build derivelive

// A second derivation family, through the same consumer path.
//
//	go test -tags derivelive ./internal/derived/ -run CrossFamily -v
//
// One family proves a lock analyzer works. Two families of different SPECIES,
// sharing every piece of machinery with no family-specific branch in this
// package, is what makes the admission path general.
package derived

import (
	"context"
	"os"
	"strings"
	"testing"
)

func confinementRecipe(command, owner string) Recipe {
	return Recipe{
		Kind: "command_invocation_confined_to", Command: command, Owner: owner,
		SearchPaths: []string{"internal", "cmd"},
	}
}

// The consumer path is family-blind.
//
// This package supplies the question and reads the receipt. If it had to know
// which family it was carrying, the "architecture" would be one analyzer with a
// wrapper around it.
func TestCrossFamilyTheConsumerPathIsFamilyBlind(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = strings.TrimSuffix(root, "/internal/derived")
	rev := head(t, root)
	cli := CLI{Bin: bin(t)}

	for _, r := range []Recipe{
		{Kind: "field_access_under_lock", Dir: "internal/event", Type: "Bus", Field: "subs", Lock: "mu"},
		confinementRecipe("claude", "internal/provider"),
	} {
		anchors, results := AnchorsFor(context.Background(), cli, root, rev, []Recipe{r})
		if results[0].Outcome != Derived {
			t.Fatalf("%s: %s — %s", r, results[0].Outcome, results[0].Detail)
		}
		if len(anchors) != 1 {
			t.Fatalf("%s anchored %d", r, len(anchors))
		}
		if len(anchors[0].Files()) == 0 {
			t.Fatalf("%s covered nothing", r)
		}
		t.Logf("%s -> covers %v", r, anchors[0].Files())
	}
}

// The extent split, at a scale where getting it wrong would have been severe.
//
// A confinement derivation must read every file under the searched subtree to
// find invocation sites, and says something about only the files that contain
// one. Coverage follows the subjects. Had it followed inputs — the defect this
// architecture already shipped once and fixed — a single derived fact would
// have claimed coverage over the whole of internal/ and cmd/.
func TestCrossFamilyCoverageFollowsSubjectsNotTheFilesRead(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = strings.TrimSuffix(root, "/internal/derived")
	cli := CLI{Bin: bin(t)}
	rev := head(t, root)
	anchors, results := AnchorsFor(context.Background(), cli, root, rev,
		[]Recipe{confinementRecipe("claude", "internal/provider")})
	if results[0].Outcome != Derived {
		t.Fatalf("%s: %s", results[0].Outcome, results[0].Detail)
	}
	subjects := anchors[0].Files()
	if len(subjects) != 1 || subjects[0] != "internal/provider/provider.go" {
		t.Fatalf("covered %v; only the file holding the invocation is a subject", subjects)
	}

	// Every other file under the searched subtree was READ and is NOT covered.
	// internal/event/bus.go is the specimen: this derivation opened and parsed
	// it, and says nothing whatsoever about it.
	covered, uncovered := CoveredFiles(anchors, rev,
		[]string{"internal/provider/provider.go", "internal/event/bus.go", "internal/gitx/git.go"})
	if len(covered) != 1 || covered[0] != "internal/provider/provider.go" {
		t.Fatalf("covered = %v", covered)
	}
	if len(uncovered) != 2 {
		t.Fatalf("uncovered = %v; reading a file must not cover it", uncovered)
	}
	t.Logf("covers %v, leaves %v uncovered despite reading them", covered, uncovered)
}

// A real refutation, kept as a live specimen.
//
// `git` is NOT confined to internal/gitx in this repository. Two packages
// invoke it directly, and both of those files answer PREFLIGHT_STATUS_EMPTY —
// the graph has no facts about exactly the files that break the boundary.
//
// So this family does not close a coverage gap here, and the reason is the
// interesting part: the derivation that WOULD close the gap is the one that
// comes back false, because of those very files. Gap closure by derivation is
// not available when the proposition is untrue where the gap is, and that is
// the architecture behaving correctly rather than a shortfall to be routed
// around. The gap-closure evidence rests on the first family; this one earns
// its place by generalising the machinery and by finding this.
func TestCrossFamilyARefutedConfinementClosesNothing(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	root = strings.TrimSuffix(root, "/internal/derived")
	cli := CLI{Bin: bin(t)}
	anchors, results := AnchorsFor(context.Background(), cli, root, head(t, root),
		[]Recipe{confinementRecipe("git", "internal/gitx")})
	res := results[0]
	if res.Outcome != NotDerived {
		t.Fatalf("expected the git confinement to be refuted, got %s: %s", res.Outcome, res.Detail)
	}
	if len(anchors) != 0 {
		t.Fatal("a refuted proposition anchored coverage")
	}
	for _, want := range []string{"internal/doctor", "internal/investigate"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("the refutation does not name %s: %q", want, res.Detail)
		}
	}
	t.Logf("refuted, and it names where: %s", res.Detail)
}

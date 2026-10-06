package workflow

// package_import_confined_to, consumed through the ordinary machinery.
//
// The derivation belongs to Sensei. What this repository owns is carrying the
// question to `sensei derive` and reading the answer back through the same
// Recipe -> CLI -> Anchor -> CoverageAnchor -> router path every other family
// uses. Nothing here decides whether a package's imports are confined; these
// tests only prove the question is asked faithfully and the answer is read
// with no special path.

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/derived"
)

const importKind = "package_import_confined_to"

// stubSensei is a fake `sensei` that records its argv and prints a fixed
// receipt, so the consumer's handling of each receipt shape can be observed.
func stubSensei(t *testing.T, receipt string) (bin, argv string) {
	t.Helper()
	dir := t.TempDir()
	argv = dir + "/argv"
	bin = dir + "/sensei"
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argv + "\ncat <<'EOF'\n" + receipt + "\nEOF\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin, argv
}

func TestPackageImportRecipeValidation(t *testing.T) {
	ok := derived.Recipe{Kind: importKind, Dir: "internal/conv", SearchPaths: []string{"."}}
	if err := derived.Validate(ok, nil); err != nil {
		t.Fatalf("an ownerless recipe was refused; owner is optional for this kind: %v", err)
	}
	withOwner := ok
	withOwner.Owner = "internal/owner"
	if err := derived.Validate(withOwner, nil); err != nil {
		t.Fatalf("a recipe with an owner was refused: %v", err)
	}
	for name, r := range map[string]derived.Recipe{
		"no dir":         {Kind: importKind, SearchPaths: []string{"."}},
		"no search":      {Kind: importKind, Dir: "internal/conv"},
		"absolute dir":   {Kind: importKind, Dir: "/etc", SearchPaths: []string{"."}},
		"escaping owner": {Kind: importKind, Dir: "internal/conv", Owner: "../x", SearchPaths: []string{"."}},
		"escaping scope": {Kind: importKind, Dir: "internal/conv", SearchPaths: []string{"../"}},
	} {
		if err := derived.Validate(r, nil); err == nil {
			t.Errorf("%s: accepted %+v", name, r)
		}
	}
	// The question must be about the region the round investigated.
	if err := derived.Validate(ok, []string{"internal/other/x.go"}); err == nil {
		t.Error("a question about a package outside the investigated region was accepted")
	}
	if err := derived.Validate(ok, []string{"internal/conv/conv.go"}); err != nil {
		t.Errorf("a question inside the investigated region was refused: %v", err)
	}
}

func TestPackageImportRecipeIdentity(t *testing.T) {
	base := derived.Recipe{Kind: importKind, Dir: "internal/conv", Owner: "internal/owner",
		SearchPaths: []string{"internal", "cmd"}}
	same := derived.Recipe{Kind: importKind, Dir: "/internal/conv/", Owner: " internal/owner/",
		SearchPaths: []string{" cmd/ ", "internal/"}, Why: "different prose"}
	if base.Identity() != same.Identity() {
		t.Fatalf("one question under two spellings got two identities:\n%s\n%s", base.Identity(), same.Identity())
	}
	for name, r := range map[string]derived.Recipe{
		"no owner":         {Kind: importKind, Dir: "internal/conv", SearchPaths: []string{"internal", "cmd"}},
		"other owner":      {Kind: importKind, Dir: "internal/conv", Owner: "internal/x", SearchPaths: []string{"internal", "cmd"}},
		"narrower search":  {Kind: importKind, Dir: "internal/conv", Owner: "internal/owner", SearchPaths: []string{"internal"}},
		"other dir":        {Kind: importKind, Dir: "internal/x", Owner: "internal/owner", SearchPaths: []string{"internal", "cmd"}},
		"other kind, same": {Kind: "command_invocation_confined_to", Dir: "internal/conv", Owner: "internal/owner", SearchPaths: []string{"internal", "cmd"}},
	} {
		if r.Identity() == base.Identity() {
			t.Errorf("%s: a different question collided with the base identity %s", name, base.Identity())
		}
	}
}

func TestPackageImportArgvOmitsAnAbsentOwner(t *testing.T) {
	receipt := `{"result":"REFUTED","detail":"stub"}`
	for _, tc := range []struct {
		owner     string
		wantOwner bool
	}{{"", false}, {"internal/owner", true}} {
		bin, argv := stubSensei(t, receipt)
		r := derived.Recipe{Kind: importKind, Dir: "internal/conv", Owner: tc.owner,
			SearchPaths: []string{"internal", "cmd"}}
		derived.CLI{Bin: bin}.Revalidate(context.Background(), "/repo", "rev1", r)
		raw, err := os.ReadFile(argv)
		if err != nil {
			t.Fatalf("the stub captured no argv: %v", err)
		}
		got := strings.Join(strings.Fields(string(raw)), " ")
		want := "derive -json -repo-root /repo -revision rev1 -kind " + importKind + " -dir internal/conv"
		if tc.wantOwner {
			want += " -owner internal/owner"
		}
		want += " -search internal -search cmd"
		if got != want {
			t.Errorf("owner %q: argv\n got %s\nwant %s", tc.owner, got, want)
		}
	}
}

// The existing fail-closed handling holds for the new kind: only a DERIVED
// receipt that names subject files yields an anchor, and its extent is the
// subjects, never the files read.
func TestPackageImportReceiptsFailClosed(t *testing.T) {
	r := derived.Recipe{Kind: importKind, Dir: "conv", SearchPaths: []string{"."}}
	for name, tc := range map[string]struct {
		receipt string
		want    derived.Outcome
	}{
		"unreadable": {`not json`, derived.Unknown},
		"refuted": {`{"result":"REFUTED","pinned_commit":"w1","subjects":[{"file":"conv/conv.go"}]}`,
			derived.Refuted},
		"unresolved": {`{"result":"UNRESOLVED","pinned_commit":"w1","subjects":[{"file":"conv/conv.go"}]}`,
			derived.Unresolved},
		"subjectless": {`{"result":"DERIVED","pinned_commit":"w1","independently_observed_inputs":["conv/conv.go"]}`,
			derived.Unknown},
	} {
		bin, _ := stubSensei(t, tc.receipt)
		anchors, results := derived.AnchorsFor(context.Background(), derived.CLI{Bin: bin}, "/repo", "w1", []derived.Recipe{r})
		if results[0].Outcome != tc.want || len(anchors) != 0 || results[0].Anchor != nil {
			t.Errorf("%s: outcome %s with %d anchor(s), want %s and none", name, results[0].Outcome, len(anchors), tc.want)
		}
	}
	bin, _ := stubSensei(t, `{"result":"DERIVED","pinned_commit":"w1",`+
		`"independently_observed_inputs":["conv/conv.go","other/other.go"],`+
		`"subjects":[{"file":"conv/conv.go","entity":"package conv","role":"package-file"}]}`)
	anchors, _ := derived.AnchorsFor(context.Background(), derived.CLI{Bin: bin}, "/repo", "w1", []derived.Recipe{r})
	if len(anchors) != 1 || anchors[0].Kind() != importKind {
		t.Fatalf("a DERIVED receipt with subjects did not anchor: %+v", anchors)
	}
	if !anchors[0].Covers("w1", "conv/conv.go") || anchors[0].Covers("w1", "other/other.go") || anchors[0].Covers("w2", "conv/conv.go") {
		t.Fatalf("the anchor's extent is not the subject files at its own world: %v", anchors[0].Files())
	}
}

// The family is recognised by the closed mapping, resolves exactly its own
// requirement, and a near-miss name resolves nothing.
func TestPackageImportConfinementIsAClosedRecognisedFamily(t *testing.T) {
	if got := requirementOfFamily(importKind); got != RequirementPackageImportConfinement {
		t.Fatalf("got %q", got)
	}
	if !satisfies(RequirementPackageImportConfinement, RequirementUnqualified) ||
		!satisfies(RequirementPackageImportConfinement, RequirementPackageImportConfinement) {
		t.Fatal("a recognised import-confinement derivation does not resolve its own gap")
	}
	for _, other := range []Requirement{RequirementLockDiscipline, RequirementInvocationConfinement, RequirementMutationConfinement} {
		if satisfies(RequirementPackageImportConfinement, other) || satisfies(other, RequirementPackageImportConfinement) {
			t.Fatalf("import confinement and %q resolve each other's gaps", other)
		}
	}
	for _, unknown := range []string{"package_import_confined", "package_import_confined_to_owner", "function_confined_to", ""} {
		if got := requirementOfFamily(unknown); got != RequirementUnrecognised || satisfies(got, RequirementUnqualified) {
			t.Errorf("unknown family %q resolved %q", unknown, got)
		}
		if err := derived.Validate(derived.Recipe{Kind: unknown, Dir: "conv", SearchPaths: []string{"."}}, nil); err == nil {
			t.Errorf("unknown family %q was accepted as a recipe", unknown)
		}
	}
}

// W5: a committed recipe of the new kind, re-derived by the pinned Sensei in a
// later world, contributes ordinary coverage through coverageAtWorld and the
// router -- and stops contributing in a still later world where it is false.
func TestW5ACommittedImportRecipeCoversALaterPinnedWorld(t *testing.T) {
	if strings.TrimSpace(os.Getenv("SENSEI_BIN")) == "" {
		t.Skip("SENSEI_BIN is unset: W5 needs the explicitly pinned Sensei executable, " +
			"and must not resolve an unqualified sensei from PATH")
	}
	ctx := context.Background()
	repo, _ := mintRepo(t)
	root := repo.Root
	commitFixtureFile(t, root, "go.mod", "module example.com/m\n\ngo 1.21\n")
	commitFixtureFile(t, root, "conv/conv.go", "package conv\n\nfunc From(s string) int { return len(s) }\n")
	commitFixtureFile(t, root, "owner/owner.go",
		"package owner\n\nimport \"example.com/m/conv\"\n\nfunc Use() int { return conv.From(\"x\") }\n")
	recipeWorld := commitFixtureFile(t, root, committedRecipesPath,
		`{"recipes":[{"kind":"`+importKind+`","dir":"conv","owner":"owner","search_paths":["."]}]}`+"\n")
	later := commitFixtureFile(t, root, "other/other.go", "package other\n\nfunc Nothing() {}\n")
	if later == recipeWorld {
		t.Fatal("the fixture has no later world")
	}

	e := &Engine{Repo: repo}
	planned := []string{"conv/conv.go"}
	c, ok := e.coverageAtWorld(ctx, "task-w5", planned, nil)
	if !ok || c.world != later {
		t.Fatalf("coverage was not computed at the later world %s: ok=%v world=%s", later, ok, c.world)
	}
	var hit *CoverageAnchor
	for i := range c.coverage {
		if c.coverage[i].File == "conv/conv.go" {
			hit = &c.coverage[i]
		}
	}
	if hit == nil {
		t.Fatalf("the committed recipe contributed no coverage at the later world: %+v", c.coverage)
	}
	if hit.Requirement != RequirementPackageImportConfinement ||
		!strings.Contains(hit.Describe, importKind) || !strings.Contains(hit.Describe, later[:12]) {
		t.Fatalf("the coverage is not an ordinary anchor of this family at this world: %+v", *hit)
	}
	for _, a := range c.coverage {
		if a.File == "owner/owner.go" || a.File == "other/other.go" {
			t.Fatalf("coverage extended past the derivation's subjects: %+v", a)
		}
	}

	// The ordinary router consumes it: the gap stands without it and closes with it.
	scoped := scopedPreflight(t, emptyOverBus)
	if bare := routeAuthorityForAction(scoped, nil, plannedEdit(planned...)); !bare.ClosesGap() {
		t.Fatalf("the specimen is not a knowledge gap: %+v", bare)
	}
	// conv/conv.go exists at the later pinned world (it was committed above), and a
	// derivation settles only a file confirmed present.
	routed := routeAuthorityForAction(scoped, nil, Action{Stage: StageCandidateEdit, Files: planned, Present: planned, DerivedCoverage: c.coverage})
	if routed.ClosesGap() || !routed.Granted() {
		t.Fatalf("ordinary routing did not accept the derived coverage: %+v", routed)
	}
	if closes, _ := derivationClosesGap(RequirementLockDiscipline, c.coverage, planned); closes {
		t.Fatal("import-confinement coverage closed a gap that named another requirement")
	}

	// Re-derived, never reused: a world where a third package imports conv
	// earns nothing from the same committed recipe.
	violated := commitFixtureFile(t, root, "other/other.go",
		"package other\n\nimport \"example.com/m/conv\"\n\nfunc Nothing() int { return conv.From(\"\") }\n")
	v, ok := e.coverageAtWorld(ctx, "task-w5", planned, nil)
	if !ok || v.world != violated {
		t.Fatalf("coverage was not recomputed at the violating world: ok=%v world=%s", ok, v.world)
	}
	if len(v.coverage) != 0 {
		t.Fatalf("a recipe whose proposition is false at this world still covered: %+v", v.coverage)
	}
}

// Accepting package_import_confined_to changed recipe validation, canonical
// identity and deduplication, so a receipt stamped by this handling must not
// carry the post-processing version that refused the kind.
func TestPackageImportPostProcessingIsVersionTwo(t *testing.T) {
	if derived.PostProcessingVersion != "closure-recipe/v2" {
		t.Fatalf("post-processing version = %q, want closure-recipe/v2", derived.PostProcessingVersion)
	}
	path := t.TempDir() + "/receipts.jsonl"
	r := derived.Recipe{Kind: importKind, Dir: "internal/conv", SearchPaths: []string{"internal"}}
	if err := derived.AppendReceipt(path, derived.InferenceReceipt{
		ModelName: "stub", Outcome: derived.InferenceOutcome("RECORDED"),
		CandidateDigest: derived.DigestOf(r), CandidateID: r.Identity(),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := derived.LoadReceipts(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].PostProcessing != "closure-recipe/v2" {
		t.Fatalf("a package-import receipt was not stamped closure-recipe/v2: %+v", got)
	}
}

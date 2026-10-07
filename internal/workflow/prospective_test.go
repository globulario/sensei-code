package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/derived"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// The gosumcheck-shaped world: one covered surface S at the pinned world, and a
// plan that creates a regression test beside it.
const (
	prospectiveWorld = "0123456789abcdef0123456789abcdef01234567"
	gosumcheckS      = "gosumcheck/gosumcheck.go"
	gosumcheckF      = "gosumcheck/gosumcheck_test.go"
	gosumcheckSrc    = `package gosumcheck

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/mod/sumdb"
)

func Check() { fmt.Println(os.Args, strings.ToUpper("x"), sumdb.ErrSecurity) }
`
)

// worldOf is a pinned world with exactly the files given; reading anything
// else fails, as `git show <world>:<missing>` does.
func worldOf(files map[string]string) worldReader {
	return func(_ context.Context, world, file string) ([]byte, error) {
		if world != prospectiveWorld {
			return nil, errors.New("wrong world")
		}
		src, ok := files[file]
		if !ok {
			return nil, fmt.Errorf("not at world %s: %w", file, errNotAtWorld)
		}
		return []byte(src), nil
	}
}

func gosumcheckAnchors() []CoverageAnchor {
	return []CoverageAnchor{{File: gosumcheckS, Requirement: RequirementInvocationConfinement,
		Describe: "command_invocation_confined_to — derived in 0123456789ab over gosumcheck/gosumcheck.go; unobserved: nothing"}}
}

func gosumcheckDeclaration() ProspectiveSurface {
	return ProspectiveSurface{Path: gosumcheckF, Package: "gosumcheck", Role: roleGoRegressionTest,
		Dependencies: []string{"testing", "strings", "golang.org/x/mod/sumdb"}}
}

func prospectiveFor(t *testing.T, planned []string, decl []ProspectiveSurface, anchors []CoverageAnchor, files map[string]string) []prospectiveGrant {
	t.Helper()
	return prospectiveAnchors(context.Background(), prospectiveWorld, planned, decl, anchors, worldOf(files))
}

// The positive case: a correct declaration for gosumcheck-shaped input yields
// one PROSPECTIVE anchor carrying S's requirement and naming S.
func TestACorrectDeclarationYieldsAProspectiveAnchorCarryingSRequirement(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 {
		t.Fatalf("expected one grant, got %+v", grants)
	}
	a := grants[0].Anchor
	if a.File != gosumcheckF || a.Requirement != RequirementInvocationConfinement {
		t.Fatalf("anchor does not carry S's requirement for F: %+v", a)
	}
	if !strings.HasPrefix(a.Describe, "PROSPECTIVE ") || !strings.Contains(a.Describe, gosumcheckS) {
		t.Fatalf("anchor description must say PROSPECTIVE and name S: %q", a.Describe)
	}
	if grants[0].Covering != gosumcheckS || !grants[0].Facts.Imports["golang.org/x/mod/sumdb"] {
		t.Fatalf("grant does not record S's facts at the pinned world: %+v", grants[0])
	}
}

// Each falsifier leaves F UNCOVERED.
func TestProspectiveFalsifiersLeaveTheFileUncovered(t *testing.T) {
	world := map[string]string{gosumcheckS: gosumcheckSrc}
	planned := []string{gosumcheckS, gosumcheckF}
	cases := []struct {
		name    string
		planned []string
		decl    []ProspectiveSurface
		anchors []CoverageAnchor
		files   map[string]string
	}{
		{"wrong package", planned, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Package = "gosumcheck_test"
			return d
		}()}, gosumcheckAnchors(), world},
		{"wrong directory", []string{gosumcheckS, "other/gosumcheck_test.go"}, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Path = "other/gosumcheck_test.go"
			return d
		}()}, gosumcheckAnchors(), world},
		{"wrong role", planned, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Role = "go-helper"
			return d
		}()}, gosumcheckAnchors(), world},
		{"path not matching the role", []string{gosumcheckS, "gosumcheck/helpers.go"}, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Path = "gosumcheck/helpers.go"
			return d
		}()}, gosumcheckAnchors(), world},
		{"novel import outside the allowance", planned, []ProspectiveSurface{func() ProspectiveSurface {
			d := gosumcheckDeclaration()
			d.Dependencies = append(d.Dependencies, "net/http")
			return d
		}()}, gosumcheckAnchors(), world},
		{"S not covered by any anchor", planned, []ProspectiveSurface{gosumcheckDeclaration()}, nil, world},
		{"declaration with no matching planned file", []string{gosumcheckS}, []ProspectiveSurface{gosumcheckDeclaration()}, gosumcheckAnchors(), world},
		{"planned file with no declaration at all", planned, nil, gosumcheckAnchors(), world},
		{"F already exists at world", planned, []ProspectiveSurface{gosumcheckDeclaration()}, gosumcheckAnchors(),
			map[string]string{gosumcheckS: gosumcheckSrc, gosumcheckF: "package gosumcheck\n"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if grants := prospectiveFor(t, c.planned, c.decl, c.anchors, c.files); len(grants) != 0 {
				t.Fatalf("%s: the file was covered: %+v", c.name, grants)
			}
		})
	}
}

// The engine reads only S's bytes at the pinned world, never the working tree:
// a covering surface the world cannot show establishes nothing.
func TestAcoveringSurfaceAbsentAtTheWorldGrantsNothing(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{})
	if len(grants) != 0 {
		t.Fatalf("a surface unreadable at the world covered F: %+v", grants)
	}
}

func createdDiff(path, src string) string {
	var b strings.Builder
	b.WriteString("diff --git a/" + path + " b/" + path + "\nnew file mode 100644\nindex 0000000..1111111\n--- /dev/null\n+++ b/" + path + "\n@@ -0,0 +1 @@\n")
	for _, line := range strings.Split(strings.TrimRight(src, "\n"), "\n") {
		b.WriteString("+" + line + "\n")
	}
	return b.String()
}

func gosumcheckFacts(t *testing.T) map[string]prospectiveFacts {
	t.Helper()
	facts, err := parseGoFacts([]byte(gosumcheckSrc))
	if err != nil {
		t.Fatal(err)
	}
	return map[string]prospectiveFacts{gosumcheckF: facts}
}

// After creation, a file whose imports exceed S's imports plus the allowance
// is refuted, and the refusal says so in its first words.
func TestACreatedFileWhoseImportsExceedTheAllowanceIsRefuted(t *testing.T) {
	diff := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"net/http\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = http.Get }\n")
	err := inspectProspectiveSurfaces(diff, []ProspectiveSurface{gosumcheckDeclaration()}, gosumcheckFacts(t))
	if err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") || !strings.Contains(err.Error(), "net/http") {
		t.Fatalf("expected a refutation naming net/http, got %v", err)
	}
}

// The authorized shape, produced: nothing to refute. And the other mismatches
// -- not created, wrong package -- are refuted the same way.
func TestPostCreationInspectionAcceptsTheAuthorizedShapeOnly(t *testing.T) {
	decl := []ProspectiveSurface{gosumcheckDeclaration()}
	good := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = strings.ToUpper }\n")
	if err := inspectProspectiveSurfaces(good, decl, gosumcheckFacts(t)); err != nil {
		t.Fatalf("the authorized shape was refuted: %v", err)
	}
	if err := inspectProspectiveSurfaces("", decl, gosumcheckFacts(t)); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
		t.Fatalf("a declared file the candidate did not create was not refuted: %v", err)
	}
	wrongPkg := createdDiff(gosumcheckF, "package gosumcheck_test\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	if err := inspectProspectiveSurfaces(wrongPkg, decl, gosumcheckFacts(t)); err == nil || !strings.Contains(err.Error(), "package") {
		t.Fatalf("a wrong package clause was not refuted: %v", err)
	}
	// No grant recorded for the declaration: refused outright. The role
	// allowance is never a substitute for a grant.
	if err := inspectProspectiveSurfaces(good, decl, nil); err == nil || !strings.Contains(err.Error(), "no recorded grant") {
		t.Fatalf("an unauthorized declaration was inspected against the role allowance alone: %v", err)
	}
}

// derivedAnchorNaming produces a real derived.Anchor, through the only
// construction site there is, whose subject list names the given files at the
// pinned world. The fake `sensei derive` answers DERIVED for anything.
func derivedAnchorNaming(t *testing.T, subjects ...string) []derived.Anchor {
	t.Helper()
	var subj []string
	for _, s := range subjects {
		subj = append(subj, fmt.Sprintf(`{"file":%q,"entity":"x","role":"subject"}`, s))
	}
	receipt := fmt.Sprintf(`{"result":"DERIVED","pinned_commit":%q,"subjects":[%s],"completeness_scope":["nothing"]}`,
		prospectiveWorld, strings.Join(subj, ","))
	bin := filepath.Join(t.TempDir(), "sensei")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\ncat <<'RECEIPT'\n"+receipt+"\nRECEIPT\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	anchors, _ := derived.AnchorsFor(context.Background(), derived.CLI{Bin: bin}, t.TempDir(), prospectiveWorld,
		[]derived.Recipe{{Kind: "command_invocation_confined_to", Command: "go", Owner: "gosumcheck", SearchPaths: []string{"."}}})
	if len(anchors) != 1 {
		t.Fatalf("the fake derivation produced %d anchors, want 1", len(anchors))
	}
	return anchors
}

// Engine-level falsifier (cycle 2): a derived Anchor whose subject list names
// a planned path ABSENT at the pinned world cannot cover that path. Covers
// compares paths; it does not establish existence, so without partitioning by
// verified existence first an anchor naming a not-yet-created file would give
// it ordinary coverage and skip declaration, role, package, dependency and
// post-creation checks.
func TestAnAnchorNamingAnAbsentPlannedPathCannotCoverItWithoutADeclaration(t *testing.T) {
	anchors := derivedAnchorNaming(t, gosumcheckS, gosumcheckF)
	world := worldOf(map[string]string{gosumcheckS: gosumcheckSrc})
	planned := []string{gosumcheckS, gosumcheckF}

	// The premise: the anchor DOES name F, and CoveredFiles alone would take it.
	if c, _ := derived.CoveredFiles(anchors, prospectiveWorld, planned); len(c) != 2 {
		t.Fatalf("specimen is not a bypass: CoveredFiles over the anchor covers %v", c)
	}

	grants, out := coverPlannedAtWorld(context.Background(), prospectiveWorld, planned, nil, anchors, world)
	if len(grants) != 0 {
		t.Fatalf("no declaration, yet prospective grants: %+v", grants)
	}
	var files []string
	for _, a := range out {
		files = append(files, a.File)
		if a.File == gosumcheckF {
			t.Fatalf("the absent planned file was covered by ordinary derived coverage: %+v", a)
		}
	}
	if len(out) != 1 || out[0].File != gosumcheckS {
		t.Fatalf("the existing surface should be the only covered file, got %v", files)
	}

	// With an admissible declaration the same absent path is authorized ONLY by
	// its prospective grant, whose anchor names S and says so.
	//
	// MIGRATED under DF-30 (objective 59, ruling 178): this asserted that the
	// grant's anchor was projected into ordinary coverage. That projection is
	// the superseded law -- it read as a derivation over a file that does not
	// exist -- so the grant now stands beside the coverage, never inside it,
	// and the absent file still takes no ordinary coverage at all.
	grants, out = coverPlannedAtWorld(context.Background(), prospectiveWorld, planned,
		[]ProspectiveSurface{gosumcheckDeclaration()}, anchors, world)
	if len(grants) != 1 || grants[0].Covering != gosumcheckS {
		t.Fatalf("an admissible declaration did not grant against S: %+v", grants)
	}
	if len(out) != 1 || out[0].File != gosumcheckS {
		t.Fatalf("a declared absent file entered ordinary coverage: %+v", out)
	}
	n := 0
	for _, a := range grantAnchors(grants) {
		if a.File != gosumcheckF {
			continue
		}
		n++
		if !strings.HasPrefix(a.Describe, "PROSPECTIVE") || a.Requirement != RequirementInvocationConfinement {
			t.Fatalf("the absent file's authority is not the prospective anchor: %+v", a)
		}
	}
	if n != 1 {
		t.Fatalf("the absent file's grant carries %d anchors, want exactly the prospective one", n)
	}

	// An existence check that cannot be answered is neither presence nor
	// absence: nothing is covered.
	dark := func(context.Context, string, string) ([]byte, error) { return nil, errors.New("unreadable world") }
	if grants, out := coverPlannedAtWorld(context.Background(), prospectiveWorld, planned,
		[]ProspectiveSurface{gosumcheckDeclaration()}, anchors, dark); len(out) != 0 || len(grants) != 0 {
		t.Fatalf("a world that cannot be read still produced coverage: %v %v", out, grants)
	}
}

// unreadableAt wraps a world so that one path fails with an unclassified read
// error while everything else, S included, reads as before.
func unreadableAt(inner worldReader, file string) worldReader {
	return func(ctx context.Context, world, f string) ([]byte, error) {
		if f == file {
			return nil, errors.New("git show: transient failure reading " + f)
		}
		return inner(ctx, world, f)
	}
}

// Cycle 3, f1: a read of F that fails for any reason other than the world's
// tree lacking F is not absence. S stays readable and every other clause
// holds, and F still receives nothing — from the predicate directly and from
// the partition that feeds it.
func TestAnUnreadableFWithAReadableSReceivesNoProspectiveAuthority(t *testing.T) {
	read := unreadableAt(worldOf(map[string]string{gosumcheckS: gosumcheckSrc}), gosumcheckF)
	planned := []string{gosumcheckS, gosumcheckF}
	decl := []ProspectiveSurface{gosumcheckDeclaration()}

	if grants := prospectiveAnchors(context.Background(), prospectiveWorld, planned, decl, gosumcheckAnchors(), read); len(grants) != 0 {
		t.Fatalf("an unclassified read failure for F was taken as absence: %+v", grants)
	}

	anchors := derivedAnchorNaming(t, gosumcheckS, gosumcheckF)
	grants, out := coverPlannedAtWorld(context.Background(), prospectiveWorld, planned, decl, anchors, read)
	if len(grants) != 0 {
		t.Fatalf("the partition sent an unreadable F to prospective authority: %+v", grants)
	}
	for _, a := range out {
		if a.File == gosumcheckF {
			t.Fatalf("an unreadable planned file was covered: %+v", a)
		}
	}
	if len(out) != 1 || out[0].File != gosumcheckS {
		t.Fatalf("S alone should be covered, got %+v", out)
	}
}

// commitFixtureFile commits one file at rel into the repository at root and
// returns the new HEAD, so a fixture's pinned world can hold a file the minted
// base does not.
func commitFixtureFile(t *testing.T, root, rel, content string) string {
	t.Helper()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, rel)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, rel), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "--", rel)
	run("commit", "-q", "-m", "fixture: "+rel)
	return run("rev-parse", "HEAD")
}

// The Git reader itself tells the three states apart: present, provably
// missing from the tree, and unreadable (here: a world that is not an object).
func TestGitShowAtDistinguishesMissingFromUnreadable(t *testing.T) {
	root := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	run("init", "-q")
	if err := os.MkdirAll(filepath.Join(root, "gosumcheck"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, gosumcheckS), []byte(gosumcheckSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-q", "-m", "s")
	world := run("rev-parse", "HEAD")
	// F is in the working tree but not at the world: the reader must not see it.
	if err := os.WriteFile(filepath.Join(root, gosumcheckF), []byte("package gosumcheck\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	read := gitShowAt(root)
	if b, err := read(context.Background(), world, gosumcheckS); err != nil || string(b) != gosumcheckSrc {
		t.Fatalf("S should read at world: %v", err)
	}
	if _, err := read(context.Background(), world, gosumcheckF); !confirmedMissing(err) {
		t.Fatalf("F is provably missing from the tree, got %v", err)
	}
	if _, err := read(context.Background(), strings.Repeat("0", 40), gosumcheckF); err == nil || confirmedMissing(err) {
		t.Fatalf("a world that is not an object must be unclassified, got %v", err)
	}
}

// Cycle 3, f2: the grants are written to the session as they were read and
// restored, bound to the world, before an interrupted candidate is inspected.
func TestInterruptedProspectiveInspectionUsesTheRecordedGrants(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 {
		t.Fatalf("premise: one grant, got %d", len(grants))
	}
	ev := event.New("s", "t1", event.SourceSystem, event.ProspectiveGranted, "recorded", prospectiveRecord{World: prospectiveWorld, Grants: grants})
	found := session.FindInterrupted([]event.Event{
		event.New("s", "t1", event.SourceSystem, event.TaskCreated, "task", nil),
		event.New("s", "t1", event.SourceArchitect, event.PlanProposed, "plan", proposedPlan{architectureDecision: architectureDecision{Plan: "p"}}),
		ev,
	})
	if len(found) != 1 || len(found[0].ProspectiveRecord) == 0 {
		t.Fatalf("the record did not survive the session: %+v", found)
	}
	decl := []ProspectiveSurface{gosumcheckDeclaration()}

	e := &Engine{}
	if err := e.restoreProspectiveGrants(found[0], decl, prospectiveWorld); err != nil {
		t.Fatal(err)
	}
	restored := e.prospectiveGrants("t1")
	if len(restored) != 1 || restored[0].Covering != gosumcheckS || !restored[0].Facts.Imports["strings"] {
		t.Fatalf("restored grants differ from the recorded ones: %+v", restored)
	}
	facts := map[string]prospectiveFacts{}
	for _, g := range restored {
		facts[g.Anchor.File] = g.Facts
	}
	// A created test importing one of S's imports is accepted against the
	// restored facts, and one exceeding them is refuted, exactly as before
	// the interruption.
	good := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = strings.ToUpper }\n")
	if err := inspectProspectiveSurfaces(good, decl, facts); err != nil {
		t.Fatalf("the authorized shape was refuted after resume: %v", err)
	}
	bad := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"net/http\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = http.Get }\n")
	if err := inspectProspectiveSurfaces(bad, decl, facts); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
		t.Fatalf("an import outside the allowance survived resume: %v", err)
	}

	// Fail closed: no record, or a record from another world, does not resume
	// a task that declared surfaces; a task that declared none needs no record.
	if err := (&Engine{}).restoreProspectiveGrants(session.Interrupted{TaskID: "t1"}, decl, prospectiveWorld); err == nil {
		t.Fatal("a declared surface with no recorded authorization was resumed")
	}
	if err := (&Engine{}).restoreProspectiveGrants(found[0], decl, strings.Repeat("f", 40)); err == nil {
		t.Fatal("a record from another world was accepted")
	}
	if err := (&Engine{}).restoreProspectiveGrants(session.Interrupted{TaskID: "t2"}, nil, prospectiveWorld); err != nil {
		t.Fatalf("a task with no declarations needs no record: %v", err)
	}
}

// The sharp case from review: a record at the correct world whose grant for F
// was lost must not resume. Role alone is not a receipt.
func TestAResumeRecordMissingTheGrantForADeclaredSurfaceIsRefused(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 {
		t.Fatalf("premise: one grant, got %d", len(grants))
	}
	decl := []ProspectiveSurface{gosumcheckDeclaration()}
	record := func(gs []prospectiveGrant) session.Interrupted {
		raw, _ := json.Marshal(prospectiveRecord{World: prospectiveWorld, Grants: gs})
		return session.Interrupted{TaskID: "t", ProspectiveRecord: raw}
	}
	other := grants[0]
	other.Anchor.File = "gosumcheck/other_test.go"
	stale := grants[0]
	stale.Surface.Package = "somethingelse"
	noFacts := grants[0]
	noFacts.Covering, noFacts.Facts = "", prospectiveFacts{}
	for name, gs := range map[string][]prospectiveGrant{
		"grant removed, world correct":  {},
		"grant for another path":        {other},
		"grant for another declaration": {stale},
		"grant without covering facts":  {noFacts},
		"duplicate grants":              {grants[0], grants[0]},
		"an extra grant":                {grants[0], other},
	} {
		e := &Engine{}
		if err := e.restoreProspectiveGrants(record(gs), decl, prospectiveWorld); err == nil {
			t.Errorf("%s: resumed", name)
		}
		if len(e.prospectiveGrants("t")) != 0 {
			t.Errorf("%s: a refused resume registered grants", name)
		}
	}
	// And the intact record still resumes.
	if err := (&Engine{}).restoreProspectiveGrants(record(grants), decl, prospectiveWorld); err != nil {
		t.Fatalf("the intact record was refused: %v", err)
	}

	// Inspection itself refuses a declaration with no recorded facts, so even
	// a path that bypassed restore cannot be judged by the role allowance alone.
	good := createdDiff(gosumcheckF, "package gosumcheck\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	if err := inspectProspectiveSurfaces(good, decl, map[string]prospectiveFacts{}); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
		t.Fatalf("a declaration with no recorded grant passed on the role allowance alone: %v", err)
	}
}

// A covering surface with several derivations carries every one of them, so
// the consumer selects by requirement rather than by filename order.
func TestEveryAdmissibleAnchorOverTheCoveringSurfaceIsCarried(t *testing.T) {
	anchors := append(gosumcheckAnchors(), CoverageAnchor{File: gosumcheckS, Requirement: RequirementLockDiscipline, Describe: "lock discipline over gosumcheck"})
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		anchors, map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 || len(grants[0].Anchors) != 2 {
		t.Fatalf("expected one grant carrying two anchors, got %+v", grants)
	}
	reqs := map[Requirement]bool{}
	for _, a := range grants[0].Anchors {
		if a.File != gosumcheckF || !strings.HasPrefix(a.Describe, "PROSPECTIVE") {
			t.Fatalf("a carried anchor is not a prospective anchor over F: %+v", a)
		}
		reqs[a.Requirement] = true
	}
	if !reqs[RequirementInvocationConfinement] || !reqs[RequirementLockDiscipline] {
		t.Fatalf("a requirement was dropped: %+v", reqs)
	}
}

// Grant facts are read at the candidate's pinned base, never at a HEAD that
// may have moved since the identity was established.
func TestProspectiveFactsAreReadAtThePinnedBase(t *testing.T) {
	// The world is chosen in coverageAtWorld, the pure computation both the
	// routing path (derivedCoverage) and resume share.
	body := funcBody(t, "internal/workflow/engine.go", "coverageAtWorld")
	// funcBody renders selector paths as "e.governedBase( " tokens.
	base, head := strings.Index(body, "e.governedBase("), strings.Index(body, "e.Repo.Head(")
	if base < 0 {
		t.Fatal("coverageAtWorld does not read the pinned base")
	}
	if head >= 0 && head < base {
		t.Fatal("coverageAtWorld consults HEAD before the pinned base")
	}
	if !strings.Contains(body, "declarations nil") {
		t.Fatal("without a pinned base, prospective declarations are still evaluated")
	}
}

// The worker is shown the exact CREATE grant it already operates under: the
// path, covering surface, package, role, declaration, the EFFECTIVE allowed
// import set (S's imports at the pinned world plus the role allowance), and the
// requirements carried -- and is told to report an unsatisfiable envelope
// rather than widen it. Without a grant the prompt is unchanged.
func TestTheImplementorIsShownTheProspectiveGrantItOperatesUnder(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 {
		t.Fatalf("premise: one grant, got %d", len(grants))
	}
	rendered := renderProspectiveGrants(grants)
	for _, want := range []string{
		"CREATE " + gosumcheckF,
		"covering surface: " + gosumcheckS,
		"package: gosumcheck",
		"role: " + roleGoRegressionTest,
		"EFFECTIVE ALLOWED IMPORTS",
		"testing",
		"strings",
		string(RequirementInvocationConfinement),
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered grant lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "bytes") {
		t.Fatal("the effective import set widened beyond S's imports and the role allowance")
	}

	with := implementationPrompt(taskContext{Task: "t"}, "plan", "", 1, nil, rendered)
	if !strings.Contains(with, "PROSPECTIVE CREATE GRANTS") || !strings.Contains(with, rendered) {
		t.Fatal("the grant did not reach the worker's prompt")
	}
	for _, want := range []string{"does not widen your scope", "do NOT add one", "architect's decision"} {
		if !strings.Contains(with, want) {
			t.Fatalf("the prompt does not say %q", want)
		}
	}
	without := implementationPrompt(taskContext{Task: "t"}, "plan", "", 1, nil, renderProspectiveGrants(nil))
	if strings.Contains(without, "PROSPECTIVE CREATE GRANTS") {
		t.Fatal("a plan with no grants carries a grant section")
	}
}

// The prompt the engine builds is fed the task's recorded grants, so a resumed
// or handed-off worker sees the same grant routing issued.
func TestRunCandidateHandsTheRecordedGrantsToThePrompt(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if !strings.Contains(body, "renderProspectiveGrants ") || !strings.Contains(body, "e.prospectiveGrants(") {
		t.Fatal("runCandidate does not pass the recorded grants into the implementation prompt")
	}
}

// A revision cycle carries review feedback AND the grant. The first draft
// assigned the feedback into the prompt's extra section, which overwrote the
// grant: cycle 1 saw the envelope and cycle 2 -- the cycle that edits the
// authorized file under a reviewer's instruction -- did not.
func TestTheGrantSurvivesAReviewFeedbackCycle(t *testing.T) {
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()},
		gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	rendered := renderProspectiveGrants(grants)
	const feedback = "the test must also cover the non-verbose path"
	got := implementationPrompt(taskContext{Task: "t"}, "plan", feedback, 2,
		[]string{"keep the assertion on one line"}, rendered)
	for _, want := range []string{"PROSPECTIVE CREATE GRANTS", rendered, "REVIEW FEEDBACK TO RECONCILE", feedback, "GUIDANCE FROM THE HUMAN ARCHITECT", "keep the assertion on one line"} {
		if !strings.Contains(got, want) {
			t.Fatalf("cycle 2 lost %q", want[:min(40, len(want))])
		}
	}
	if strings.Count(got, "PROSPECTIVE CREATE GRANTS") != 1 {
		t.Fatal("the grant section is repeated")
	}
}

// The new-package world: one module, a covered library surface under
// internal/, a covered command surface under cmd/, and nothing at the two new
// directories a plan creates.
const (
	newLibDir  = "internal/answerer"
	newCmdDir  = "cmd/sensei-code-answerer"
	libS       = "internal/relay/relay.go"
	cmdS       = "cmd/sensei-code/main.go"
	libSSource = "package relay\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\t\"strings\"\n)\n\nfunc R() { fmt.Println(os.Args, strings.ToUpper(\"x\")) }\n"
	cmdSSource = "package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n)\n\nfunc main() { fmt.Println(os.Args) }\n"
)

func newPackageWorld() map[string]string {
	return map[string]string{"go.mod": "module example.com/m\n", libS: libSSource, cmdS: cmdSSource}
}

func newPackageAnchors() []CoverageAnchor {
	return []CoverageAnchor{
		{File: libS, Requirement: RequirementLockDiscipline, Describe: "lock discipline over internal/relay"},
		{File: cmdS, Requirement: RequirementInvocationConfinement, Describe: "command_invocation_confined_to over cmd/sensei-code"},
	}
}

func newLibDeclarations() []ProspectiveSurface {
	return []ProspectiveSurface{
		{Path: newLibDir + "/answerer.go", Package: "answerer", Role: roleGoLibraryPackage, Covering: libS, Dependencies: []string{"fmt", "strings"}},
		{Path: newLibDir + "/config.go", Package: "answerer", Role: roleGoLibraryPackage, Covering: libS, Dependencies: []string{"os"}},
		{Path: newLibDir + "/answerer_test.go", Package: "answerer", Role: roleGoRegressionTest, Dependencies: []string{"testing", "strings"}},
	}
}

func newLibPlanned() []string {
	return []string{newLibDir + "/answerer.go", newLibDir + "/config.go", newLibDir + "/answerer_test.go"}
}

func newCmdDeclarations() []ProspectiveSurface {
	return []ProspectiveSurface{{Path: newCmdDir + "/main.go", Package: "main", Role: roleGoCommandPackage, Covering: cmdS, Dependencies: []string{"fmt", "os"}}}
}

func grantedPaths(grants []prospectiveGrant) map[string]prospectiveGrant {
	out := map[string]prospectiveGrant{}
	for _, g := range grants {
		out[g.Anchor.File] = g
	}
	return out
}

// W1: a new library package -- production files plus an adjacent test -- with
// a named, covered covering surface is admitted with grants for exactly those
// files. The test traces through a production grant to the same surface.
func TestW1ADeclaredNewLibraryPackageIsAdmittedFileByFile(t *testing.T) {
	grants := prospectiveFor(t, newLibPlanned(), newLibDeclarations(), newPackageAnchors(), newPackageWorld())
	got := grantedPaths(grants)
	if len(grants) != 3 || len(got) != 3 {
		t.Fatalf("expected exactly three grants, got %+v", grants)
	}
	for _, f := range newLibPlanned() {
		g, ok := got[f]
		if !ok {
			t.Fatalf("no grant for %s", f)
		}
		if g.Covering != libS || g.Facts.Package != "relay" || !g.Facts.Imports["strings"] {
			t.Fatalf("%s is not granted against the pinned facts of %s: %+v", f, libS, g)
		}
		if !strings.HasPrefix(g.Anchor.Describe, "PROSPECTIVE ") || !strings.Contains(g.Anchor.Describe, libS) || g.Anchor.Requirement != RequirementLockDiscipline {
			t.Fatalf("%s's anchor is not a prospective anchor carrying S's requirement: %+v", f, g.Anchor)
		}
	}
	if test := got[newLibDir+"/answerer_test.go"]; test.Via != newLibDir+"/answerer.go" {
		t.Fatalf("the adjacent test does not trace through a production grant: %+v", test)
	}
	if prod := got[newLibDir+"/answerer.go"]; prod.Via != "" {
		t.Fatalf("a production grant claims to trace through another: %+v", prod)
	}
	if err := matchGrantsToDeclarations(newLibDeclarations(), grants); err != nil {
		t.Fatalf("the issued grants do not match their declarations: %v", err)
	}
}

// W2: a new command package is admitted the same way.
func TestW2ADeclaredNewCommandPackageIsAdmitted(t *testing.T) {
	grants := prospectiveFor(t, []string{newCmdDir + "/main.go"}, newCmdDeclarations(), newPackageAnchors(), newPackageWorld())
	if len(grants) != 1 || grants[0].Anchor.File != newCmdDir+"/main.go" || grants[0].Covering != cmdS ||
		grants[0].Facts.Package != "main" || grants[0].Anchor.Requirement != RequirementInvocationConfinement {
		t.Fatalf("the command package was not granted against %s: %+v", cmdS, grants)
	}
	// Both new packages in one plan are decided independently.
	both := prospectiveFor(t, append(newLibPlanned(), newCmdDir+"/main.go"), append(newLibDeclarations(), newCmdDeclarations()...),
		newPackageAnchors(), newPackageWorld())
	if len(both) != 4 {
		t.Fatalf("expected four grants over the two packages, got %+v", both)
	}
}

// W3: ABSENCE. Each falsifier leaves every file of the new package ungranted.
func TestW3NewPackageFalsifiersGrantNothing(t *testing.T) {
	with := func(mut func(ds []ProspectiveSurface) []ProspectiveSurface) []ProspectiveSurface {
		return mut(newLibDeclarations())
	}
	world := newPackageWorld()
	extend := func(extra map[string]string) map[string]string {
		w := newPackageWorld()
		for k, v := range extra {
			w[k] = v
		}
		return w
	}
	cases := []struct {
		name    string
		planned []string
		decl    []ProspectiveSurface
		anchors []CoverageAnchor
		read    worldReader
	}{
		{"an undeclared file in the new directory", append(newLibPlanned(), newLibDir+"/mailbox.go"), newLibDeclarations(), newPackageAnchors(), worldOf(world)},
		{"a declaration with no matching planned file", newLibPlanned()[:2], newLibDeclarations(), newPackageAnchors(), worldOf(world)},
		{"an unknown role name", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[1].Role = "go-helper-package"
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"no covering surface named", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[0].Covering, ds[1].Covering = "", ""
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"two covering surfaces named", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[1].Covering = cmdS
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"covering surface missing at the world", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			worldOf(map[string]string{"go.mod": "module example.com/m\n", cmdS: cmdSSource})},
		{"covering surface uncovered at the world", newLibPlanned(), newLibDeclarations(), newPackageAnchors()[1:], worldOf(world)},
		{"covering surface is a command, not a library", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[0].Covering, ds[1].Covering = cmdS, cmdS
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"covering surface under another top-level root", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[0].Covering, ds[1].Covering = "pkg/relay/relay.go", "pkg/relay/relay.go"
			return ds
		}), append(newPackageAnchors(), CoverageAnchor{File: "pkg/relay/relay.go", Requirement: RequirementLockDiscipline, Describe: "x"}),
			worldOf(extend(map[string]string{"pkg/relay/relay.go": libSSource}))},
		{"covering surface in another Go module", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			worldOf(extend(map[string]string{"internal/relay/go.mod": "module example.com/relay\n"}))},
		{"no Go module at the world", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			worldOf(map[string]string{libS: libSSource, cmdS: cmdSSource})},
		{"the new directory already exists", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			worldOf(extend(map[string]string{newLibDir: "tree", newLibDir + "/old.go": "package answerer\n"}))},
		{"a production role on a test path", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[2].Role, ds[2].Covering = roleGoLibraryPackage, libS
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"a library declaring package main", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			for i := range ds {
				ds[i].Package = "main"
			}
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"a test outside the package's clause", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[2].Package = "answerer_test"
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"a dependency outside the covering surface's imports", newLibPlanned(), with(func(ds []ProspectiveSurface) []ProspectiveSurface {
			ds[0].Dependencies = append(ds[0].Dependencies, "net/http")
			return ds
		}), newPackageAnchors(), worldOf(world)},
		{"a test with no production declaration", []string{newLibDir + "/answerer_test.go"}, newLibDeclarations()[2:], newPackageAnchors(), worldOf(world)},
		{"unclassified read failure of a planned file", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			unreadableAt(worldOf(world), newLibDir+"/config.go")},
		{"unclassified read failure of the new directory", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			unreadableAt(worldOf(world), newLibDir)},
		{"unclassified read failure of go.mod", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			unreadableAt(worldOf(world), "go.mod")},
		{"unclassified read failure of the covering surface", newLibPlanned(), newLibDeclarations(), newPackageAnchors(),
			unreadableAt(worldOf(world), libS)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			grants := prospectiveAnchors(context.Background(), prospectiveWorld, c.planned, c.decl, c.anchors, c.read)
			for _, g := range grants {
				if strings.HasPrefix(g.Anchor.File, newLibDir+"/") {
					t.Fatalf("%s: the new package was granted: %+v", c.name, grants)
				}
			}
		})
	}

	cmdCases := []struct {
		name string
		decl []ProspectiveSurface
		dir  string
	}{
		{"command covering surface is a library", []ProspectiveSurface{{Path: newCmdDir + "/main.go", Package: "main", Role: roleGoCommandPackage, Covering: libS}}, newCmdDir},
		{"command not under the covering surface's cmd root", []ProspectiveSurface{{Path: "tools/answerer/main.go", Package: "main", Role: roleGoCommandPackage, Covering: cmdS}}, "tools/answerer"},
		{"command declaring a non-main package", []ProspectiveSurface{{Path: newCmdDir + "/main.go", Package: "answerer", Role: roleGoCommandPackage, Covering: cmdS}}, newCmdDir},
	}
	for _, c := range cmdCases {
		t.Run(c.name, func(t *testing.T) {
			if grants := prospectiveFor(t, []string{c.decl[0].Path}, c.decl, newPackageAnchors(), world); len(grants) != 0 {
				t.Fatalf("%s: the command package was granted: %+v", c.name, grants)
			}
		})
	}

	// Through the engine's partition as well: an unreadable planned file in
	// the new directory is not handed on as absent, and still counts against
	// the package.
	grants, out := coverPlannedAtWorld(context.Background(), prospectiveWorld, newLibPlanned(), newLibDeclarations(),
		derivedAnchorNaming(t, libS), unreadableAt(worldOf(world), newLibDir+"/config.go"))
	if len(grants) != 0 {
		t.Fatalf("an unreadable planned file left its package grantable: %+v", grants)
	}
	for _, a := range out {
		if strings.HasPrefix(a.File, newLibDir+"/") {
			t.Fatalf("a file of the refused package was covered: %+v", a)
		}
	}
	// The same for an unreadable planned file nobody declared: it is neither
	// present nor absent, and it is still a create the package did not declare.
	grants, _ = coverPlannedAtWorld(context.Background(), prospectiveWorld, append(newLibPlanned(), newLibDir+"/mailbox.go"),
		newLibDeclarations(), derivedAnchorNaming(t, libS), unreadableAt(worldOf(world), newLibDir+"/mailbox.go"))
	if len(grants) != 0 {
		t.Fatalf("an unreadable undeclared file left its package grantable: %+v", grants)
	}
}

// W4: a created file whose package clause or imports diverge from its
// declaration is refused, and so is an undeclared file created in the new
// package. The admitted shape passes.
func TestW4NewPackageInspectionRefusesDivergence(t *testing.T) {
	decl := newLibDeclarations()
	grants := prospectiveFor(t, newLibPlanned(), decl, newPackageAnchors(), newPackageWorld())
	if len(grants) != 3 {
		t.Fatalf("premise: three grants, got %+v", grants)
	}
	facts := map[string]prospectiveFacts{}
	for _, g := range grants {
		facts[g.Anchor.File] = g.Facts
	}
	good := createdDiff(newLibDir+"/answerer.go", "package answerer\n\nimport \"strings\"\n\nvar X = strings.ToUpper\n") +
		createdDiff(newLibDir+"/config.go", "package answerer\n\nimport \"os\"\n\nvar Y = os.Getenv\n") +
		createdDiff(newLibDir+"/answerer_test.go", "package answerer\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	if err := inspectProspectiveSurfaces(good, decl, facts); err != nil {
		t.Fatalf("the admitted shape was refused: %v", err)
	}
	for name, diff := range map[string]string{
		"package clause": strings.Replace(good, "+package answerer\n+\n+import \"os\"", "+package config\n+\n+import \"os\"", 1),
		"import":         strings.Replace(good, "import \"os\"", "import \"net/http\"", 1),
		"test import":    strings.Replace(good, "+import \"testing\"", "+import (\n+\t\"net/http\"\n+\t\"testing\"\n+)", 1),
		"undeclared":     good + createdDiff(newLibDir+"/mailbox.go", "package answerer\n"),
	} {
		if diff == good {
			t.Fatalf("%s: the mutation did not apply", name)
		}
		if err := inspectProspectiveSurfaces(diff, decl, facts); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
			t.Fatalf("%s divergence was not refused: %v", name, err)
		}
	}
	// A command whose created file is not package main is refused.
	cmd := newCmdDeclarations()
	cmdGrants := prospectiveFor(t, []string{newCmdDir + "/main.go"}, cmd, newPackageAnchors(), newPackageWorld())
	cmdFacts := map[string]prospectiveFacts{newCmdDir + "/main.go": cmdGrants[0].Facts}
	if err := inspectProspectiveSurfaces(createdDiff(newCmdDir+"/main.go", "package answerer\n"), cmd, cmdFacts); err == nil || !strings.Contains(err.Error(), "package") {
		t.Fatalf("a command with the wrong package clause was not refused: %v", err)
	}
}

// Resume inspects against the same pinned facts: the recorded new-package
// grants restore intact, and a record whose test grant no longer traces to a
// same-directory production grant over the same surface is refused.
func TestNewPackageGrantsResumeOnlyWhenTheyStillTrace(t *testing.T) {
	decl := newLibDeclarations()
	grants := prospectiveFor(t, newLibPlanned(), decl, newPackageAnchors(), newPackageWorld())
	record := func(gs []prospectiveGrant) session.Interrupted {
		raw, _ := json.Marshal(prospectiveRecord{World: prospectiveWorld, Grants: gs})
		return session.Interrupted{TaskID: "t", ProspectiveRecord: raw}
	}
	if err := (&Engine{}).restoreProspectiveGrants(record(grants), decl, prospectiveWorld); err != nil {
		t.Fatalf("the intact record was refused: %v", err)
	}
	tamper := func(f func(g *prospectiveGrant)) []prospectiveGrant {
		out := append([]prospectiveGrant(nil), grants...)
		for i := range out {
			f(&out[i])
		}
		return out
	}
	for name, gs := range map[string][]prospectiveGrant{
		"test traces through nothing": tamper(func(g *prospectiveGrant) {
			if g.Via != "" {
				g.Via = newLibDir + "/gone.go"
			}
		}),
		"test covers by another surface than its production grant": tamper(func(g *prospectiveGrant) {
			if g.Via != "" {
				g.Covering = cmdS
			}
		}),
		"production covers by another surface": tamper(func(g *prospectiveGrant) {
			if g.Via == "" {
				g.Covering = cmdS
			}
		}),
		"test drops its trace": tamper(func(g *prospectiveGrant) {
			if g.Via != "" {
				g.Via = ""
			}
		}),
		"production claims a trace": tamper(func(g *prospectiveGrant) {
			if g.Via == "" {
				g.Via = newLibDir + "/config.go"
			}
		}),
	} {
		e := &Engine{}
		if err := e.restoreProspectiveGrants(record(gs), decl, prospectiveWorld); err == nil {
			t.Errorf("%s: resumed", name)
		}
		if len(e.prospectiveGrants("t")) != 0 {
			t.Errorf("%s: a refused resume registered grants", name)
		}
	}
}

// The worker sees a new-package grant as its own shape: the named covering
// surface, the declared package, and the production grant a test traces to.
func TestTheImplementorIsShownANewPackageGrant(t *testing.T) {
	rendered := renderProspectiveGrants(prospectiveFor(t, newLibPlanned(), newLibDeclarations(), newPackageAnchors(), newPackageWorld()))
	for _, want := range []string{
		"CREATE " + newLibDir + "/answerer.go",
		"covering surface: " + libS,
		"role: " + roleGoLibraryPackage,
		"package: answerer (the new package's declared clause)",
		"traced through production grant: " + newLibDir + "/answerer.go",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("the rendered grant lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "must equal the covering surface's package") {
		t.Fatalf("a new package was told to take the covering surface's package:\n%s", rendered)
	}
}

// Cause B: a new command imports its own new library package. The fixtures
// below are the Objective 48 new-package world with the command declaring that
// one same-plan library as a dependency.
const answererImport = "example.com/m/" + newLibDir

func edgeCmdDeclarations() []ProspectiveSurface {
	return []ProspectiveSurface{{Path: newCmdDir + "/main.go", Package: "main", Role: roleGoCommandPackage, Covering: cmdS,
		Dependencies: []string{"fmt", "os", answererImport}}}
}

func edgePlanned() []string { return append(newLibPlanned(), newCmdDir+"/main.go") }

func edgeDeclarations() []ProspectiveSurface {
	return append(newLibDeclarations(), edgeCmdDeclarations()...)
}

func edgeRecordOf(grants []prospectiveGrant) session.Interrupted {
	raw, _ := json.Marshal(prospectiveRecord{World: prospectiveWorld, Grants: grants})
	return session.Interrupted{TaskID: "t", ProspectiveRecord: raw}
}

func edgeLibraryDiff() string {
	return createdDiff(newLibDir+"/answerer.go", "package answerer\n\nimport \"strings\"\n\nvar X = strings.ToUpper\n") +
		createdDiff(newLibDir+"/config.go", "package answerer\n\nimport \"os\"\n\nvar Y = os.Getenv\n") +
		createdDiff(newLibDir+"/answerer_test.go", "package answerer\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
}

func edgeCommandDiff(imports ...string) string {
	src := "package main\n\nimport (\n"
	for _, imp := range imports {
		src += "\t_ " + fmt.Sprintf("%q", imp) + "\n"
	}
	return createdDiff(newCmdDir+"/main.go", src+")\n\nfunc main() {}\n")
}

func commandGrant(grants []prospectiveGrant) (prospectiveGrant, bool) {
	g, ok := grantedPaths(grants)[newCmdDir+"/main.go"]
	return g, ok
}

// W1 CONTROL for cause B: covering-surface eligibility is unchanged. Through
// the engine's partition, with recipe-derived anchors fixed by the caller, the
// command binds its library edge only when its own named surface carries a
// derived anchor; declaring the edge derives nothing for an uncovered surface.
func TestCauseBW1CoveringSurfaceEligibilityIsUnchanged(t *testing.T) {
	world := worldOf(newPackageWorld())
	grants, _ := coverPlannedAtWorld(context.Background(), prospectiveWorld, edgePlanned(), edgeDeclarations(), derivedAnchorNaming(t, libS, cmdS), world)
	if g, ok := commandGrant(grants); !ok || g.Edge == nil {
		t.Fatalf("with both surfaces derived the command was not granted its edge: %+v", grants)
	}
	grants, out := coverPlannedAtWorld(context.Background(), prospectiveWorld, edgePlanned(), edgeDeclarations(), derivedAnchorNaming(t, libS), world)
	if _, ok := commandGrant(grants); ok {
		t.Fatalf("a command whose named surface has no derived anchor was granted: %+v", grants)
	}
	if len(grants) != 3 {
		t.Fatalf("the library's own eligibility changed: %+v", grants)
	}
	for _, a := range out {
		if a.File == cmdS || strings.HasPrefix(a.File, newCmdDir+"/") {
			t.Fatalf("declaring the edge produced coverage for %s: %+v", a.File, a)
		}
	}
}

// W2 cause B: the command declared with its covering surface's imports plus the
// same-plan granted library is GRANTED with that one edge, and inspection
// accepts a candidate containing exactly that import.
func TestCauseBW2ACommandImportingItsGrantedSamePlanLibraryIsGranted(t *testing.T) {
	decl := edgeDeclarations()
	grants := prospectiveFor(t, edgePlanned(), decl, newPackageAnchors(), newPackageWorld())
	if len(grants) != 4 {
		t.Fatalf("expected four grants, got %+v", grants)
	}
	g, ok := commandGrant(grants)
	want := prospectiveEdge{Import: answererImport, Library: newLibDir, Module: "example.com/m", ModuleDir: "."}
	if !ok || g.Edge == nil || *g.Edge != want || g.Covering != cmdS {
		t.Fatalf("the command was not granted the one library edge: %+v", g)
	}
	for _, lg := range grants {
		if lg.Anchor.File != g.Anchor.File && lg.Edge != nil {
			t.Fatalf("a non-command grant carries an edge: %+v", lg)
		}
	}
	if err := matchGrantsToDeclarations(decl, grants); err != nil {
		t.Fatalf("the issued grants do not match their declarations: %v", err)
	}
	// Declaration order does not decide it: a command declared before its
	// library still binds the library once that library is granted whole.
	if g, ok := commandGrant(prospectiveFor(t, edgePlanned(), append(edgeCmdDeclarations(), newLibDeclarations()...), newPackageAnchors(), newPackageWorld())); !ok || g.Edge == nil {
		t.Fatalf("a command declared before its library was not granted its edge: %+v", g)
	}
	good := edgeLibraryDiff() + edgeCommandDiff("fmt", "os", answererImport)
	if err := inspectProspectiveGrants(good, decl, grants); err != nil {
		t.Fatalf("the admitted command was refused: %v", err)
	}
	for name, diff := range map[string]string{
		"a second novel import": edgeLibraryDiff() + edgeCommandDiff("fmt", answererImport, "example.com/m/internal/relay"),
		"another package only":  edgeLibraryDiff() + edgeCommandDiff("fmt", "example.com/m/internal/relay"),
	} {
		if err := inspectProspectiveGrants(diff, decl, grants); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
			t.Fatalf("%s was accepted: %v", name, err)
		}
	}
	// Covering facts alone never carried the edge.
	facts := map[string]prospectiveFacts{}
	for _, g := range grants {
		facts[g.Anchor.File] = g.Facts
	}
	if err := inspectProspectiveSurfaces(good, decl, facts); err == nil {
		t.Fatal("covering facts alone authorized the library import")
	}
	if rendered := renderProspectiveGrants(grants); !strings.Contains(rendered, "same-plan library edge: "+answererImport) {
		t.Fatalf("the worker is not shown the edge:\n%s", rendered)
	}
}

// W3 ABSENCE: each falsifier yields no command grant.
func TestCauseBW3FalsifiersGrantNoDependentCommand(t *testing.T) {
	cmdWith := func(deps ...string) []ProspectiveSurface {
		d := edgeCmdDeclarations()
		d[0].Dependencies = append([]string{"fmt", "os"}, deps...)
		return d
	}
	second := []ProspectiveSurface{
		{Path: "internal/answerer2/a.go", Package: "answerer2", Role: roleGoLibraryPackage, Covering: libS, Dependencies: []string{"fmt"}},
	}
	refusedLib := newLibDeclarations()
	refusedLib[1].Dependencies = append(refusedLib[1].Dependencies, "net/http")

	// A library in another Go module that the command's module path would
	// otherwise name: internal/ is its own module at the world.
	crossWorld := newPackageWorld()
	crossWorld["internal/go.mod"] = "module example.com/m/internal\n"
	crossWorld["internal/sub/relay/relay.go"] = libSSource
	crossLib := []ProspectiveSurface{{Path: "internal/sub/answerer/a.go", Package: "answerer", Role: roleGoLibraryPackage, Covering: "internal/sub/relay/relay.go", Dependencies: []string{"fmt"}}}
	crossAnchors := append(newPackageAnchors(), CoverageAnchor{File: "internal/sub/relay/relay.go", Requirement: RequirementLockDiscipline, Describe: "x"})
	if lib := prospectiveFor(t, []string{"internal/sub/answerer/a.go"}, crossLib, crossAnchors, crossWorld); len(lib) != 1 {
		t.Fatalf("premise: the cross-module library is granted on its own, got %+v", lib)
	}

	cases := []struct {
		name    string
		planned []string
		decl    []ProspectiveSurface
		world   map[string]string
		anchors []CoverageAnchor
	}{
		{"a package that is not in the plan", edgePlanned(), append(newLibDeclarations(), cmdWith("example.com/m/internal/relay")...), newPackageWorld(), newPackageAnchors()},
		{"a same-plan path in another module path", edgePlanned(), append(newLibDeclarations(), cmdWith("example.com/other/"+newLibDir)...), newPackageWorld(), newPackageAnchors()},
		{"the library is absent from the plan", []string{newCmdDir + "/main.go"}, edgeCmdDeclarations(), newPackageWorld(), newPackageAnchors()},
		{"a planned production create of the library was refused", edgePlanned(), append(refusedLib, edgeCmdDeclarations()...), newPackageWorld(), newPackageAnchors()},
		{"an undeclared file in the library", append(edgePlanned(), newLibDir+"/mailbox.go"), edgeDeclarations(), newPackageWorld(), newPackageAnchors()},
		{"an undeclared file in the command", append(edgePlanned(), newCmdDir+"/extra.go"), edgeDeclarations(), newPackageWorld(), newPackageAnchors()},
		{"a second novel import", append(edgePlanned(), "internal/answerer2/a.go"),
			append(append(newLibDeclarations(), second...), cmdWith(answererImport, "example.com/m/internal/answerer2")...), newPackageWorld(), newPackageAnchors()},
		{"two command files binding two libraries", append(append(edgePlanned(), "internal/answerer2/a.go"), newCmdDir+"/two.go"),
			append(append(edgeDeclarations(), second...), ProspectiveSurface{Path: newCmdDir + "/two.go", Package: "main", Role: roleGoCommandPackage, Covering: cmdS,
				Dependencies: []string{"example.com/m/internal/answerer2"}}), newPackageWorld(), newPackageAnchors()},
		{"a library in another Go module", []string{"internal/sub/answerer/a.go", newCmdDir + "/main.go"},
			append(crossLib, cmdWith("example.com/m/internal/sub/answerer")...), crossWorld, crossAnchors},
		{"a novel import on a test in the command", append(edgePlanned(), newCmdDir+"/main_test.go"),
			append(edgeDeclarations(), ProspectiveSurface{Path: newCmdDir + "/main_test.go", Package: "main", Role: roleGoRegressionTest, Dependencies: []string{"testing", answererImport}}),
			newPackageWorld(), newPackageAnchors()},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			grants := prospectiveFor(t, c.planned, c.decl, c.anchors, c.world)
			for _, g := range grants {
				if strings.HasPrefix(g.Anchor.File, newCmdDir+"/") {
					t.Fatalf("%s: the dependent command was granted: %+v", c.name, grants)
				}
			}
		})
	}
	// The control: without the library edge the second case's command is
	// granted, so the refusal above is the edge's.
	if g, ok := commandGrant(prospectiveFor(t, edgePlanned(), append(newLibDeclarations(), cmdWith()...), newPackageAnchors(), newPackageWorld())); !ok || g.Edge != nil {
		t.Fatalf("a command with no novel dependency was not granted plainly: %+v", g)
	}
}

// W4 resume: the recorded edge is restored through the session record, and a
// restored command grant whose edge is dropped or disagrees with the record is
// refused; inspection honors no absent or tampered edge.
func TestCauseBW4TheEdgeResumesOnlyIntact(t *testing.T) {
	decl := edgeDeclarations()
	grants := prospectiveFor(t, edgePlanned(), decl, newPackageAnchors(), newPackageWorld())
	if _, ok := commandGrant(grants); !ok || len(grants) != 4 {
		t.Fatalf("premise: four grants with the command, got %+v", grants)
	}
	found := session.FindInterrupted([]event.Event{
		event.New("s", "t", event.SourceSystem, event.TaskCreated, "task", nil),
		event.New("s", "t", event.SourceArchitect, event.PlanProposed, "plan", proposedPlan{architectureDecision: architectureDecision{Plan: "p"}}),
		event.New("s", "t", event.SourceSystem, event.ProspectiveGranted, "recorded", prospectiveRecord{World: prospectiveWorld, Grants: grants}),
	})
	if len(found) != 1 {
		t.Fatalf("the record did not survive the session: %+v", found)
	}
	e := &Engine{}
	if err := e.restoreProspectiveGrants(found[0], decl, prospectiveWorld); err != nil {
		t.Fatalf("the intact record was refused: %v", err)
	}
	restored, ok := commandGrant(e.prospectiveGrants("t"))
	if !ok || restored.Edge == nil || restored.Edge.Import != answererImport || restored.Edge.Library != newLibDir {
		t.Fatalf("the edge was not restored: %+v", restored)
	}
	good := edgeLibraryDiff() + edgeCommandDiff("fmt", answererImport)
	if err := inspectProspectiveGrants(good, decl, e.prospectiveGrants("t")); err != nil {
		t.Fatalf("the restored edge did not reach inspection: %v", err)
	}

	tamper := func(f func(g *prospectiveGrant)) []prospectiveGrant {
		out := append([]prospectiveGrant(nil), grants...)
		for i := range out {
			if out[i].Edge != nil {
				edge := *out[i].Edge
				out[i].Edge = &edge
			}
			f(&out[i])
		}
		return out
	}
	isCmd := func(g *prospectiveGrant) bool { return g.Anchor.File == newCmdDir+"/main.go" }
	for name, gs := range map[string][]prospectiveGrant{
		"edge dropped": tamper(func(g *prospectiveGrant) {
			if isCmd(g) {
				g.Edge = nil
			}
		}),
		"edge import altered": tamper(func(g *prospectiveGrant) {
			if isCmd(g) {
				g.Edge.Import = "example.com/m/internal/relay"
			}
		}),
		"edge retargeted to a package outside the plan": tamper(func(g *prospectiveGrant) {
			if isCmd(g) {
				g.Edge.Library, g.Edge.Import = "internal/relay", "example.com/m/internal/relay"
			}
		}),
		"edge module altered": tamper(func(g *prospectiveGrant) {
			if isCmd(g) {
				g.Edge.Module = "example.com/other"
			}
		}),
		"edge on a library grant": tamper(func(g *prospectiveGrant) {
			if g.Anchor.File == newLibDir+"/answerer.go" {
				g.Edge = &prospectiveEdge{Import: answererImport, Library: newLibDir, Module: "example.com/m", ModuleDir: "."}
			}
		}),
		"library grant for another declaration": tamper(func(g *prospectiveGrant) {
			if g.Anchor.File == newLibDir+"/config.go" {
				g.Surface.Role = roleGoCommandPackage
			}
		}),
	} {
		e := &Engine{}
		if err := e.restoreProspectiveGrants(edgeRecordOf(gs), decl, prospectiveWorld); err == nil {
			t.Errorf("%s: resumed", name)
		}
		if len(e.prospectiveGrants("t")) != 0 {
			t.Errorf("%s: a refused resume registered grants", name)
		}
	}
	// A path that bypassed restore still cannot import through a dropped or
	// tampered edge.
	for name, gs := range map[string][]prospectiveGrant{
		"dropped": tamper(func(g *prospectiveGrant) {
			if isCmd(g) {
				g.Edge = nil
			}
		}),
		"retargeted": tamper(func(g *prospectiveGrant) {
			if isCmd(g) {
				g.Edge.Library = "internal/relay"
			}
		}),
	} {
		if err := inspectProspectiveGrants(good, decl, gs); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
			t.Errorf("inspection honored a %s edge: %v", name, err)
		}
	}
}

// Objective 57 (DF-19 residual): go-existing-package. The Objective-49-shaped
// world: an existing package internal/session whose covered store.go and
// journal.go are read at the pinned world, a governing go.mod that requires
// golang.org/x/sys, and a plan creating two platform files in that package.
const (
	existingS        = "internal/session/store.go"
	existingSibling  = "internal/session/journal.go"
	existingUnix     = "internal/session/recordlock_unix.go"
	existingWindows  = "internal/session/recordlock_windows.go"
	existingThird    = "internal/session/recordlock_plan9.go"
	existingGoMod    = "module github.com/globulario/sensei-code\n\ngo 1.25.0\n\nrequire (\n\tcharm.land/bubbletea/v2 v2.0.8\n\tgolang.org/x/sys v0.46.0\n)\n\nrequire golang.org/x/sync v0.21.0 // indirect\n"
	existingStoreSrc = "package session\n\nimport (\n\t\"encoding/json\"\n\t\"os\"\n\t\"sync\"\n\n\t\"github.com/globulario/sensei-code/internal/event\"\n)\n\ntype Store struct{ mu sync.Mutex }\n\nvar _ = json.Marshal\nvar _ = os.Open\nvar _ event.Event\n"
	existingJournal  = "package session\n\nimport (\n\t\"bufio\"\n\t\"fmt\"\n)\n\nvar _ = bufio.NewReader\nvar _ = fmt.Sprint\n"
)

func existingWorld() map[string]string {
	return map[string]string{"go.mod": existingGoMod, existingS: existingStoreSrc, existingSibling: existingJournal}
}

func existingDeclarations() []ProspectiveSurface {
	return []ProspectiveSurface{
		{Path: existingUnix, Package: "session", Role: roleGoExistingPackage, Covering: existingS, Dependencies: []string{"os", "golang.org/x/sys/unix"}},
		{Path: existingWindows, Package: "session", Role: roleGoExistingPackage, Covering: existingS, Dependencies: []string{"os", "fmt", "golang.org/x/sys/windows"}},
	}
}

func existingPlanned() []string { return []string{existingS, existingUnix, existingWindows} }

// existingGrants drives a plan through coverPlannedAtWorld, the function the
// router calls for a live plan, over a real derived anchor naming the covered
// files of the package.
func existingGrants(t *testing.T, planned []string, decl []ProspectiveSurface, files map[string]string) ([]prospectiveGrant, []CoverageAnchor) {
	t.Helper()
	anchors := derivedAnchorNaming(t, existingS, existingSibling)
	return coverPlannedAtWorld(context.Background(), prospectiveWorld, planned, decl, anchors, worldOf(files))
}

const (
	unixLockSrc    = "//go:build unix\n\npackage session\n\nimport (\n\t\"os\"\n\n\t\"golang.org/x/sys/unix\"\n)\n\nfunc lock(f *os.File) error { return unix.Flock(int(f.Fd()), unix.LOCK_EX) }\n"
	windowsLockSrc = "//go:build windows\n\npackage session\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\n\t\"golang.org/x/sys/windows\"\n)\n\nfunc lock(f *os.File) error { return fmt.Errorf(\"%v\", windows.Handle(f.Fd())) }\n"
)

func existingDiff() string {
	return createdDiff(existingUnix, unixLockSrc) + createdDiff(existingWindows, windowsLockSrc)
}

// W1 + W9: the Objective-49-shaped pair, covered by store.go, both receive
// prospective grants through the production router path, pass the strict
// architecture contract, and their candidate files -- each importing a declared
// golang.org/x/sys subpackage the package never imported -- pass the production
// inspection against the recorded grants.
func TestObj57W1TheRecordLockPairIsGrantedThroughTheExistingPackageRole(t *testing.T) {
	decl := existingDeclarations()
	for _, d := range decl {
		if err := strictProspectiveDeclaration(d); err != nil {
			t.Fatalf("the strict contract refused %s: %v", d.Path, err)
		}
	}
	grants, out := existingGrants(t, existingPlanned(), decl, existingWorld())
	got := grantedPaths(grants)
	if len(grants) != 2 || len(got) != 2 {
		t.Fatalf("expected exactly the two record-lock grants, got %+v", grants)
	}
	for _, f := range []string{existingUnix, existingWindows} {
		g, ok := got[f]
		if !ok {
			t.Fatalf("no grant for %s", f)
		}
		if g.Covering != existingS || g.Facts.Package != "session" || g.Via != "" || g.Edge != nil || g.Existing == nil {
			t.Fatalf("%s is not an existing-package grant over %s: %+v", f, existingS, g)
		}
		// The package facts are the union over the covered files of the package.
		for _, imp := range []string{"encoding/json", "os", "sync", "bufio", "fmt", "github.com/globulario/sensei-code/internal/event"} {
			if !g.Facts.Imports[imp] {
				t.Fatalf("%s's package facts lack %q: %+v", f, imp, g.Facts)
			}
		}
		if g.Existing.Module != "github.com/globulario/sensei-code" || g.Existing.ModuleDir != "." {
			t.Fatalf("%s's grant names the wrong module: %+v", f, g.Existing)
		}
		if !strings.HasPrefix(g.Anchor.Describe, "PROSPECTIVE "+roleGoExistingPackage+" "+f) || !strings.Contains(g.Anchor.Describe, existingS) {
			t.Fatalf("%s's anchor is not a prospective anchor naming its covering file: %+v", f, g.Anchor)
		}
	}
	if env := strings.Join(got[existingUnix].Existing.Envelope, ","); env != "golang.org/x/sys/unix,os" {
		t.Fatalf("the unix envelope is not exactly its declared dependencies: %s", env)
	}
	if env := strings.Join(got[existingWindows].Existing.Envelope, ","); env != "fmt,golang.org/x/sys/windows,os" {
		t.Fatalf("the windows envelope is not exactly its declared dependencies: %s", env)
	}
	// MIGRATED under DF-30 (objective 59, ruling 178): the pair's prospective
	// anchors were asserted to be in the router's ordinary coverage. They are
	// the grants' own, kept beside it: the existing surface alone is covered,
	// and each created file carries exactly one prospective anchor on its grant.
	covered := map[string]int{}
	for _, a := range out {
		covered[a.File]++
	}
	if covered[existingUnix] != 0 || covered[existingWindows] != 0 || covered[existingS] == 0 {
		t.Fatalf("ordinary coverage is not exactly the existing surface's: %v", covered)
	}
	prospective := map[string]int{}
	for _, a := range grantAnchors(grants) {
		prospective[a.File]++
	}
	if prospective[existingUnix] != 1 || prospective[existingWindows] != 1 || len(prospective) != 2 {
		t.Fatalf("the grants do not carry the created pair's prospective anchors: %v", prospective)
	}
	if err := matchGrantsToDeclarations(decl, grants); err != nil {
		t.Fatalf("the issued grants are not a receipt for their declarations: %v", err)
	}
	if err := inspectProspectiveGrants(existingDiff(), decl, grants); err != nil {
		t.Fatalf("the authorized record-lock files were refuted: %v", err)
	}
	// The worker is shown the envelope, not the package's whole import set.
	rendered := renderProspectiveGrants(grants)
	if !strings.Contains(rendered, "CREATE "+existingUnix) || !strings.Contains(rendered, "(exactly the declared dependencies the pinned package and go.mod admit; nothing else): golang.org/x/sys/unix, os\n") {
		t.Fatalf("the rendered grant does not state the exact envelope:\n%s", rendered)
	}
}

// W2 + requirement 8: an undeclared third sibling create receives no grant and
// a candidate that creates it is refuted; a declaration with no planned file
// grants nothing.
func TestObj57W2AnUndeclaredSiblingCreateIsRefused(t *testing.T) {
	grants, out := existingGrants(t, append(existingPlanned(), existingThird), existingDeclarations(), existingWorld())
	if _, ok := grantedPaths(grants)[existingThird]; ok || len(grants) != 2 {
		t.Fatalf("the undeclared sibling was granted: %+v", grants)
	}
	for _, a := range out {
		if a.File == existingThird {
			t.Fatalf("the undeclared sibling is covered: %+v", a)
		}
	}
	diff := existingDiff() + createdDiff(existingThird, "package session\n\nimport \"os\"\n\nvar _ = os.Open\n")
	if err := inspectProspectiveGrants(diff, existingDeclarations(), grants); err == nil || !strings.Contains(err.Error(), existingThird) {
		t.Fatalf("a candidate creating an undeclared sibling was not refuted: %v", err)
	}
	// A declaration alone authorizes nothing: declared but not planned.
	decl := append(existingDeclarations(), ProspectiveSurface{Path: existingThird, Package: "session", Role: roleGoExistingPackage, Covering: existingS, Dependencies: []string{"os"}})
	grants, _ = existingGrants(t, existingPlanned(), decl, existingWorld())
	if _, ok := grantedPaths(grants)[existingThird]; ok {
		t.Fatalf("a declaration with no planned create was granted: %+v", grants)
	}
}

// existingWith returns the declarations with the unix declaration mutated.
func existingWith(mut func(d *ProspectiveSurface)) []ProspectiveSurface {
	decl := existingDeclarations()
	mut(&decl[0])
	return decl
}

func unixGranted(t *testing.T, decl []ProspectiveSurface, files map[string]string, planned []string) bool {
	t.Helper()
	grants, _ := existingGrants(t, planned, decl, files)
	for _, g := range grants {
		if g.Surface.Path == decl[0].Path {
			return true
		}
	}
	return false
}

// W3: a declared package clause that differs from the covering file's is
// refused at grant time, and a created file whose clause drifts is refuted.
func TestObj57W3APackageClauseMismatchIsRefused(t *testing.T) {
	if unixGranted(t, existingWith(func(d *ProspectiveSurface) { d.Package = "lock" }), existingWorld(), existingPlanned()) {
		t.Fatal("a declaration whose package differs from the covering file's was granted")
	}
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	drift := createdDiff(existingUnix, strings.Replace(unixLockSrc, "package session", "package lock", 1)) + createdDiff(existingWindows, windowsLockSrc)
	if err := inspectProspectiveGrants(drift, existingDeclarations(), grants); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
		t.Fatalf("a created file with a drifted package clause was not refuted: %v", err)
	}
	// The covering file's package governs: a sibling with another clause
	// contributes nothing to the package facts.
	world := existingWorld()
	world[existingSibling] = "package session_other\n\nimport \"net/http\"\n\nvar _ = http.Get\n"
	grants, _ = existingGrants(t, existingPlanned(), existingDeclarations()[:1], world)
	if len(grants) != 1 || grants[0].Facts.Imports["net/http"] {
		t.Fatalf("a file with another package clause widened the package facts: %+v", grants)
	}
}

// W4: a *_test.go is never this role, and a path in a directory absent at the
// pinned world is refused by it; the new-package roles are unchanged.
func TestObj57W4TestPathsAndNewDirectoriesAreNotThisRole(t *testing.T) {
	testDecl := existingWith(func(d *ProspectiveSurface) { d.Path = "internal/session/recordlock_test.go" })
	if err := strictProspectiveDeclaration(testDecl[0]); err == nil {
		t.Fatal("the strict contract admitted a *_test.go under go-existing-package")
	}
	if unixGranted(t, testDecl, existingWorld(), []string{existingS, testDecl[0].Path, existingWindows}) {
		t.Fatal("a *_test.go was granted under go-existing-package")
	}
	if err := inspectProspectiveGrants(createdDiff(testDecl[0].Path, "package session\n"), testDecl[:1], nil); err == nil || !strings.Contains(err.Error(), "path shape") {
		t.Fatalf("inspection did not refute a *_test.go under go-existing-package: %v", err)
	}

	// A new directory, covering file named inside it: nothing exists there.
	newDir := existingWith(func(d *ProspectiveSurface) {
		d.Path, d.Covering = "internal/recordlock/lock_unix.go", "internal/recordlock/lock.go"
	})
	if unixGranted(t, newDir, existingWorld(), []string{existingS, newDir[0].Path, existingWindows}) {
		t.Fatal("a create in a directory absent at the pinned world was granted under go-existing-package")
	}
	// A new directory covered by a file of an existing package is refused
	// statically and by the predicate.
	outside := existingWith(func(d *ProspectiveSurface) { d.Path = "internal/recordlock/lock_unix.go" })
	if err := strictProspectiveDeclaration(outside[0]); err == nil {
		t.Fatal("the strict contract admitted a covering file outside the created file's directory")
	}
	if unixGranted(t, outside, existingWorld(), []string{existingS, outside[0].Path, existingWindows}) {
		t.Fatal("a create in a new directory was granted through a covering file elsewhere")
	}
	// Mixed into a new-package directory, the role poisons the package rather
	// than riding on its production grant.
	mixed := append(newLibDeclarations(), ProspectiveSurface{Path: newLibDir + "/extra.go", Package: "answerer", Role: roleGoExistingPackage, Covering: libS, Dependencies: []string{"fmt"}})
	if g := prospectiveFor(t, append(newLibPlanned(), newLibDir+"/extra.go"), mixed, newPackageAnchors(), newPackageWorld()); len(g) != 0 {
		t.Fatalf("a go-existing-package declaration in a new package was granted: %+v", g)
	}
	// Control: the new-package roles still admit the new directory.
	if g := prospectiveFor(t, newLibPlanned(), newLibDeclarations(), newPackageAnchors(), newPackageWorld()); len(g) != 3 {
		t.Fatalf("the new-package roles changed: %+v", g)
	}
}

// W5: a declared path that exists at the pinned world is refused, and absence
// alone grants nothing: without a named, covered, readable covering file in
// the package, an absent path stays ungranted.
func TestObj57W5ExistenceAndAbsenceAloneGrantNothing(t *testing.T) {
	world := existingWorld()
	world[existingUnix] = "package session\n"
	if unixGranted(t, existingDeclarations(), world, existingPlanned()) {
		t.Fatal("a declared path present at the pinned world was granted as a create")
	}
	if err := inspectProspectiveGrants(createdDiff(existingWindows, windowsLockSrc), existingDeclarations(), nil); err == nil {
		t.Fatal("inspection accepted a declared file the candidate did not create")
	}
	for name, decl := range map[string][]ProspectiveSurface{
		"no covering file":          existingWith(func(d *ProspectiveSurface) { d.Covering = "" }),
		"covering file absent":      existingWith(func(d *ProspectiveSurface) { d.Covering = "internal/session/missing.go" }),
		"covering file is a test":   existingWith(func(d *ProspectiveSurface) { d.Covering = "internal/session/store_test.go" }),
		"covering file is itself":   existingWith(func(d *ProspectiveSurface) { d.Covering = existingUnix }),
		"covering file not covered": existingWith(func(d *ProspectiveSurface) { d.Covering = "internal/session/uncovered.go" }),
		"unknown role":              existingWith(func(d *ProspectiveSurface) { d.Role = "go-existing-file" }),
		"declared twice":            append(existingDeclarations(), existingDeclarations()[0]),
		"no governing go.mod":       nil,
	} {
		files := existingWorld()
		files["internal/session/uncovered.go"] = "package session\n"
		files["internal/session/store_test.go"] = "package session\n"
		if name == "no governing go.mod" {
			delete(files, "go.mod")
			decl = existingDeclarations()
		}
		if unixGranted(t, decl, files, existingPlanned()) {
			t.Errorf("%s: an absent path was granted", name)
		}
	}
	if err := strictProspectiveDeclaration(existingWith(func(d *ProspectiveSurface) { d.Covering = "" })[0]); err == nil {
		t.Fatal("the strict contract admitted a go-existing-package with no covering file")
	}
}

// W6: both sides of the closed dependency rule, through the production
// predicate and the production inspection.
func TestObj57W6TheDependencyRuleAdmitsDeclaredRequiredModulesOnly(t *testing.T) {
	// Admitted: a declared subpackage of a required module (W1 also carries
	// golang.org/x/sys/unix and /windows), and a package-set import.
	if !unixGranted(t, existingWith(func(d *ProspectiveSurface) {
		d.Dependencies = []string{"bufio", "golang.org/x/sys/unix", "golang.org/x/sync/errgroup", "charm.land/bubbletea/v2"}
	}), existingWorld(), existingPlanned()) {
		t.Fatal("declared imports the closed rule admits were refused")
	}
	// Refused at grant time: declared but inadmissible.
	for _, dep := range []string{
		"syscall",                // standard library the package does not import
		"github.com/pkg/errors",  // a module go.mod does not require
		"golang.org/x/sysx/unix", // prefix of a required module, not a path-segment descendant
		"github.com/globulario/sensei-code/internal/workflow", // same-module package not already imported
		"",
	} {
		if unixGranted(t, existingWith(func(d *ProspectiveSurface) { d.Dependencies = []string{"os", dep} }), existingWorld(), existingPlanned()) {
			t.Errorf("declared dependency %q outside the closed rule was granted", dep)
		}
	}
	// Longest path-segment ownership: an import inside the main module is not
	// admitted by a shorter required prefix.
	if dependencyAdmitted("example.com/m/sub/x", nil, "example.com/m/sub", []string{"example.com/m"}) {
		t.Error("a shorter required module admitted an import the main module owns")
	}
	if !dependencyAdmitted("example.com/m/other", nil, "example.com/m/sub", []string{"example.com/m"}) {
		t.Error("an import owned by a required module was refused")
	}

	// Refused at inspection: undeclared imports, including ones the package
	// already imports and ones a build tag would never compile on this host.
	grants, _ := existingGrants(t, existingPlanned(), existingDeclarations(), existingWorld())
	for name, src := range map[string]string{
		"undeclared package-set import": strings.Replace(unixLockSrc, "\"os\"\n", "\"os\"\n\t\"sync\"\n", 1),
		"undeclared required module":    strings.Replace(unixLockSrc, "\"os\"\n", "\"os\"\n\t\"golang.org/x/sync/errgroup\"\n", 1),
		"another platform's subpackage": strings.Replace(unixLockSrc, "golang.org/x/sys/unix\"", "golang.org/x/sys/unix\"\n\t\"golang.org/x/sys/windows\"", 1),
		"stdlib behind a build tag":     strings.Replace(unixLockSrc, "\"os\"\n", "\"os\"\n\t\"syscall\"\n", 1),
	} {
		diff := createdDiff(existingUnix, src) + createdDiff(existingWindows, windowsLockSrc)
		if err := inspectProspectiveGrants(diff, existingDeclarations(), grants); err == nil || !strings.Contains(err.Error(), "envelope") {
			t.Errorf("%s: the candidate was not refuted for its import: %v", name, err)
		}
	}
	// An envelope tampered in memory, bypassing restore, authorizes nothing.
	tampered := append([]prospectiveGrant(nil), grants...)
	for i := range tampered {
		x := *tampered[i].Existing
		x.Envelope = append(append([]string(nil), x.Envelope...), "syscall")
		tampered[i].Existing = &x
	}
	diff := createdDiff(existingUnix, strings.Replace(unixLockSrc, "\"os\"\n", "\"os\"\n\t\"syscall\"\n", 1)) + createdDiff(existingWindows, windowsLockSrc)
	if err := inspectProspectiveGrants(diff, existingDeclarations(), tampered); err == nil {
		t.Fatal("a widened envelope authorized an undeclared import")
	}
}

// W7: the binding read back through the session after a restart is the exact
// recorded binding; resuming again restores the same bytes, and a resume that
// would mint (an extra declaration) or widen (a tampered record) is refused.
func TestObj57W7TheBindingSurvivesRestartWithoutMintingOrWidening(t *testing.T) {
	decl := existingDeclarations()
	grants, _ := existingGrants(t, existingPlanned(), decl, existingWorld())
	if len(grants) != 2 {
		t.Fatalf("premise: two grants, got %+v", grants)
	}
	found := session.FindInterrupted([]event.Event{
		event.New("s", "t", event.SourceSystem, event.TaskCreated, "task", nil),
		event.New("s", "t", event.SourceArchitect, event.PlanProposed, "plan", proposedPlan{architectureDecision: architectureDecision{Plan: "p"}}),
		event.New("s", "t", event.SourceSystem, event.ProspectiveGranted, "recorded", prospectiveRecord{World: prospectiveWorld, Grants: grants}),
	})
	if len(found) != 1 || len(found[0].ProspectiveRecord) == 0 {
		t.Fatalf("the record did not survive the session: %+v", found)
	}
	want, _ := json.Marshal(grants)
	e := &Engine{}
	for i := 0; i < 2; i++ {
		if err := e.restoreProspectiveGrants(found[0], decl, prospectiveWorld); err != nil {
			t.Fatalf("resume %d refused the intact record: %v", i+1, err)
		}
		got, _ := json.Marshal(e.prospectiveGrants("t"))
		if string(got) != string(want) {
			t.Fatalf("resume %d restored a different binding:\n got %s\nwant %s", i+1, got, want)
		}
	}
	if err := inspectProspectiveGrants(existingDiff(), decl, e.prospectiveGrants("t")); err != nil {
		t.Fatalf("the restored binding did not reach inspection: %v", err)
	}

	// Minting: a resume cannot add a declaration the record never granted.
	minted := append(existingDeclarations(), ProspectiveSurface{Path: existingThird, Package: "session", Role: roleGoExistingPackage, Covering: existingS, Dependencies: []string{"os"}})
	if err := (&Engine{}).restoreProspectiveGrants(found[0], minted, prospectiveWorld); err == nil {
		t.Fatal("a resume minted a grant for a declaration the record never held")
	}

	// Substitution: a resumed declaration that repeats a dependency in place
	// of one the record names is not the recorded declaration. Equal lengths
	// and one-way membership must not let the record's
	// golang.org/x/sys/unix survive a declaration that no longer names it.
	substituted := existingDeclarations()
	substituted[0].Dependencies = []string{"os", "os"}
	sub := &Engine{}
	if err := sub.restoreProspectiveGrants(found[0], substituted, prospectiveWorld); err == nil {
		t.Fatal("a resume restored a recorded dependency the resumed declaration replaced with a duplicate")
	}
	if len(sub.prospectiveGrants("t")) != 0 {
		t.Fatal("a refused duplicate-substitution resume registered grants")
	}

	tamper := func(f func(g *prospectiveGrant)) []prospectiveGrant {
		out := append([]prospectiveGrant(nil), grants...)
		for i := range out {
			if out[i].Existing != nil {
				x := *out[i].Existing
				x.Envelope = append([]string(nil), x.Envelope...)
				x.Requires = append([]string(nil), x.Requires...)
				out[i].Existing = &x
			}
			if out[i].Anchor.File == existingUnix {
				f(&out[i])
			}
		}
		return out
	}
	for name, gs := range map[string][]prospectiveGrant{
		"envelope dropped":            tamper(func(g *prospectiveGrant) { g.Existing = nil }),
		"envelope widened":            tamper(func(g *prospectiveGrant) { g.Existing.Envelope = append(g.Existing.Envelope, "syscall") }),
		"envelope substituted":        tamper(func(g *prospectiveGrant) { g.Existing.Envelope = []string{"fmt", "golang.org/x/sys/unix"} }),
		"envelope narrowed":           tamper(func(g *prospectiveGrant) { g.Existing.Envelope = g.Existing.Envelope[:1] }),
		"requirement removed":         tamper(func(g *prospectiveGrant) { g.Existing.Requires = []string{"charm.land/bubbletea/v2"} }),
		"module removed":              tamper(func(g *prospectiveGrant) { g.Existing.Module = "" }),
		"covering moved out of dir":   tamper(func(g *prospectiveGrant) { g.Covering = "internal/event/event.go" }),
		"package facts altered":       tamper(func(g *prospectiveGrant) { g.Facts.Package = "lock" }),
		"traced through another file": tamper(func(g *prospectiveGrant) { g.Via = existingWindows }),
		"declaration altered": tamper(func(g *prospectiveGrant) {
			g.Surface.Dependencies = append([]string{"syscall"}, g.Surface.Dependencies...)
		}),
		"envelope on another role": tamper(func(g *prospectiveGrant) {
			g.Surface.Role = roleGoRegressionTest
		}),
	} {
		raw, _ := json.Marshal(prospectiveRecord{World: prospectiveWorld, Grants: gs})
		e := &Engine{}
		if err := e.restoreProspectiveGrants(session.Interrupted{TaskID: "t", ProspectiveRecord: raw}, decl, prospectiveWorld); err == nil {
			t.Errorf("%s: resumed", name)
		}
		if len(e.prospectiveGrants("t")) != 0 {
			t.Errorf("%s: a refused resume registered grants", name)
		}
	}
	// An envelope on a grant of another role is refused by the record check
	// even when the declaration agrees.
	reg := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, []ProspectiveSurface{gosumcheckDeclaration()}, gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	reg[0].Existing = &prospectiveExisting{Module: "m", ModuleDir: ".", Envelope: []string{"testing"}}
	if err := matchGrantsToDeclarations([]ProspectiveSurface{gosumcheckDeclaration()}, reg); err == nil {
		t.Fatal("a regression-test grant carrying an existing-package envelope was accepted")
	}
}

// Requirement 10: build constraints and GOOS suffixes do not alter the
// authority identity. The same declaration under a platform-neutral name is
// granted the same binding, and the record carries no platform fact.
func TestObj57BuildConstraintsDoNotAlterTheBinding(t *testing.T) {
	neutral := existingWith(func(d *ProspectiveSurface) { d.Path = "internal/session/recordlock.go" })
	gn, _ := existingGrants(t, []string{existingS, neutral[0].Path}, neutral[:1], existingWorld())
	gu, _ := existingGrants(t, []string{existingS, existingUnix}, existingDeclarations()[:1], existingWorld())
	if len(gn) != 1 || len(gu) != 1 {
		t.Fatalf("premise: one grant each, got %+v %+v", gn, gu)
	}
	if gn[0].Covering != gu[0].Covering || strings.Join(gn[0].Existing.Envelope, ",") != strings.Join(gu[0].Existing.Envelope, ",") ||
		strings.Join(gn[0].Existing.Requires, ",") != strings.Join(gu[0].Existing.Requires, ",") || len(gn[0].Facts.Imports) != len(gu[0].Facts.Imports) {
		t.Fatalf("the GOOS suffix changed the binding: %+v vs %+v", gn[0], gu[0])
	}
}

// Objective 60 (DF-37): a prospective declaration is an admission obligation.
// The two specimens are the run-2 shapes of Objectives 59 and 49: a declared
// go-regression-test whose covering surface carries no derivation, so the
// production predicate produced no grant, and the plan was admitted anyway.
const (
	obj59Test     = "internal/workflow/coverage_reconciliation_test.go"
	obj59Covering = "internal/workflow/authority.go"
	obj49Test     = "internal/session/repair_test.go"
)

func obj59Declaration() ProspectiveSurface {
	return ProspectiveSurface{Path: obj59Test, Package: "workflow", Role: roleGoRegressionTest, Covering: obj59Covering, Dependencies: []string{"testing"}}
}

func obj49Declaration() ProspectiveSurface {
	return ProspectiveSurface{Path: obj49Test, Package: "session", Role: roleGoRegressionTest, Dependencies: []string{"testing"}}
}

// admittedAfterRouting records the grants the routing predicate produced, as
// derivedCoverage does, and asks plan admission about the declarations.
func admittedAfterRouting(grants []prospectiveGrant, decl []ProspectiveSurface) error {
	e := &Engine{}
	e.setProspectiveGrants("t", grants)
	return e.reconcileProspectiveGrants("t", decl)
}

// W1: the Objective-59 run-2 shape is refused at admission, naming the
// declaration and why no canonical grant exists.
func TestDF37W1Objective59UngrantedDeclarationRefusesAdmission(t *testing.T) {
	decl := []ProspectiveSurface{obj59Declaration()}
	world := map[string]string{obj59Covering: "package workflow\n\nimport \"fmt\"\n\nvar _ = fmt.Sprint\n"}
	grants := prospectiveFor(t, []string{obj59Covering, obj59Test}, decl, nil, world)
	if len(grants) != 0 {
		t.Fatalf("premise: the specimen's covering surface carries no derivation, so no grant, got %+v", grants)
	}
	err := admittedAfterRouting(grants, decl)
	if err == nil {
		t.Fatal("a declared test with no canonical grant was admitted")
	}
	for _, want := range []string{"prospective admission refused before implementation", obj59Test, "holds no recorded grant", obj59Covering} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not say %q: %v", want, err)
		}
	}
}

// W2: the Objective-49 run-2 shape, the same refusal.
func TestDF37W2Objective49UngrantedDeclarationRefusesAdmission(t *testing.T) {
	decl := []ProspectiveSurface{obj49Declaration()}
	grants := prospectiveFor(t, []string{existingS, obj49Test}, decl, nil, existingWorld())
	if len(grants) != 0 {
		t.Fatalf("premise: no grant, got %+v", grants)
	}
	err := admittedAfterRouting(grants, decl)
	if err == nil || !strings.Contains(err.Error(), obj49Test) || !strings.Contains(err.Error(), "holds no recorded grant") {
		t.Fatalf("the Objective-49 shape was not refused naming its declaration: %v", err)
	}
}

// W3: a declaration with its matching grant proceeds, and a plan with no
// declarations is untouched whatever is recorded.
func TestDF37W3AGrantedDeclarationIsAdmitted(t *testing.T) {
	decl := []ProspectiveSurface{gosumcheckDeclaration()}
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, decl, gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 {
		t.Fatalf("premise: one grant, got %+v", grants)
	}
	if err := admittedAfterRouting(grants, decl); err != nil {
		t.Fatalf("a granted declaration was refused: %v", err)
	}
	if err := admittedAfterRouting(nil, nil); err != nil {
		t.Fatalf("a plan with no declarations was refused: %v", err)
	}
}

// W4: one granted and one ungranted declaration -- the whole plan is refused,
// and the refusal names only the unsatisfied declaration.
func TestDF37W4OneUngrantedDeclarationRefusesTheWholePlan(t *testing.T) {
	decl := []ProspectiveSurface{gosumcheckDeclaration(), obj59Declaration()}
	world := map[string]string{gosumcheckS: gosumcheckSrc, obj59Covering: "package workflow\n"}
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF, obj59Covering, obj59Test}, decl, gosumcheckAnchors(), world)
	if len(grants) != 1 || grants[0].Anchor.File != gosumcheckF {
		t.Fatalf("premise: only the gosumcheck declaration is granted, got %+v", grants)
	}
	err := admittedAfterRouting(grants, decl)
	if err == nil || !strings.Contains(err.Error(), obj59Test) {
		t.Fatalf("the plan with an ungranted declaration was admitted or not named: %v", err)
	}
	if strings.Contains(err.Error(), gosumcheckF) {
		t.Fatalf("the refusal names the satisfied declaration: %v", err)
	}
}

// W5: duplicate, mismatched, malformed, extra and stale grants each refuse
// admission, naming the declaration.
func TestDF37W5DuplicateOrMismatchedGrantsRefuseAdmission(t *testing.T) {
	decl := []ProspectiveSurface{gosumcheckDeclaration()}
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF}, decl, gosumcheckAnchors(), map[string]string{gosumcheckS: gosumcheckSrc})
	if len(grants) != 1 {
		t.Fatalf("premise: one grant, got %+v", grants)
	}
	mismatched := grants[0]
	mismatched.Surface.Package = "other"
	malformed := grants[0]
	malformed.Covering, malformed.Facts = "", prospectiveFacts{}
	elsewhere := grants[0]
	elsewhere.Anchor.File = "gosumcheck/other_test.go"
	for name, gs := range map[string][]prospectiveGrant{
		"duplicate grants":  {grants[0], grants[0]},
		"mismatched grant":  {mismatched},
		"malformed grant":   {malformed},
		"an extra grant":    {grants[0], elsewhere},
		"grant elsewhere":   {elsewhere},
		"no grant recorded": nil,
	} {
		err := admittedAfterRouting(gs, decl)
		if err == nil {
			t.Errorf("%s: admitted", name)
			continue
		}
		if !strings.Contains(err.Error(), gosumcheckF) && name != "an extra grant" {
			t.Errorf("%s: the refusal does not name the declaration: %v", name, err)
		}
	}

	// Two individually valid command grants in one command package bound to
	// different libraries: the record is refused naming both declarations.
	second := ProspectiveSurface{Path: "internal/answerer2/a.go", Package: "answerer2", Role: roleGoLibraryPackage, Covering: libS, Dependencies: []string{"fmt"}}
	two := ProspectiveSurface{Path: newCmdDir + "/two.go", Package: "main", Role: roleGoCommandPackage, Covering: cmdS,
		Dependencies: []string{"fmt", "os", "example.com/m/internal/answerer2"}}
	cmds := append(edgeDeclarations(), second)
	cmdGrants := prospectiveFor(t, append(edgePlanned(), second.Path), cmds, newPackageAnchors(), newPackageWorld())
	main, ok := commandGrant(cmdGrants)
	if len(cmdGrants) != 5 || !ok || main.Edge == nil {
		t.Fatalf("premise: both libraries and the edged command are granted, got %+v", cmdGrants)
	}
	twoGrant := main
	twoGrant.Anchor.File, twoGrant.Surface = two.Path, two
	twoGrant.Edge = &prospectiveEdge{Import: "example.com/m/internal/answerer2", Library: "internal/answerer2", Module: "example.com/m", ModuleDir: "."}
	if err := matchGrantsToDeclarations([]ProspectiveSurface{second, two}, []prospectiveGrant{grantedPaths(cmdGrants)[second.Path], twoGrant}); err != nil {
		t.Fatalf("premise: the second command grant is valid on its own: %v", err)
	}
	err := admittedAfterRouting(append(cmdGrants, twoGrant), append(cmds, two))
	if err == nil {
		t.Fatal("two command files bound to two libraries were admitted")
	}
	for _, want := range []string{newCmdDir + "/main.go", two.Path, "bind two library packages", "exactly one same-plan library package"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the multi-edge refusal does not say %q: %v", want, err)
		}
	}

	// Stale: grants recorded for an earlier plan do not survive a routing
	// that derived nothing for this one.
	e := &Engine{}
	e.setProspectiveGrants("t", grants)
	e.derivedCoverage(context.Background(), "t", nil, decl)
	if err := e.reconcileProspectiveGrants("t", decl); err == nil {
		t.Fatal("a stale grant from an earlier routing admitted the plan")
	}
}

// W7: restoration and candidate inspection read the same declaration/grant
// rule admission does: the same grant set fails all three with the same
// canonical fault, and an intact one passes all three.
func TestDF37W7RestorationUsesTheSameCanonicalMatcher(t *testing.T) {
	decl := []ProspectiveSurface{gosumcheckDeclaration(), obj59Declaration()}
	world := map[string]string{gosumcheckS: gosumcheckSrc, obj59Covering: "package workflow\n"}
	grants := prospectiveFor(t, []string{gosumcheckS, gosumcheckF, obj59Covering, obj59Test}, decl, gosumcheckAnchors(), world)
	canonical := matchGrantsToDeclarations(decl, grants)
	if canonical == nil {
		t.Fatal("premise: the canonical rule refuses the ungranted declaration")
	}
	raw, _ := json.Marshal(prospectiveRecord{World: prospectiveWorld, Grants: grants})
	restore := (&Engine{}).restoreProspectiveGrants(session.Interrupted{TaskID: "t", ProspectiveRecord: raw}, decl, prospectiveWorld)
	admit := admittedAfterRouting(grants, decl)
	for name, err := range map[string]error{"restoration": restore, "admission": admit} {
		if err == nil || !strings.HasSuffix(err.Error(), canonical.Error()) {
			t.Errorf("%s does not refuse with the canonical fault %q: %v", name, canonical, err)
		}
	}

	// A grant set whose content checks pass but whose record does not match
	// the declarations is refuted at inspection by the same rule.
	one := decl[:1]
	extra := append([]prospectiveGrant{}, grants...)
	other := grants[0]
	other.Anchor.File = "gosumcheck/other_test.go"
	extra = append(extra, other)
	good := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = strings.ToUpper }\n")
	want := matchGrantsToDeclarations(one, extra)
	if want == nil {
		t.Fatal("premise: an extra grant breaks the canonical rule")
	}
	if err := inspectProspectiveGrants(good, one, extra); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") || !strings.HasSuffix(err.Error(), want.Error()) {
		t.Fatalf("inspection did not refute with the canonical fault %q: %v", want, err)
	}
	// Intact: all three pass.
	raw, _ = json.Marshal(prospectiveRecord{World: prospectiveWorld, Grants: grants})
	if err := (&Engine{}).restoreProspectiveGrants(session.Interrupted{TaskID: "t", ProspectiveRecord: raw}, one, prospectiveWorld); err != nil {
		t.Fatalf("restoration refused an intact grant: %v", err)
	}
	if err := admittedAfterRouting(grants, one); err != nil {
		t.Fatalf("admission refused an intact grant: %v", err)
	}
	if err := inspectProspectiveGrants(good, one, grants); err != nil {
		t.Fatalf("inspection refused an intact grant: %v", err)
	}
}

// DF-30 (objective 59): a planned CREATE absent at the pinned world whose
// prospective surface was granted is governed for its own coverage question.
//
// The measured specimen (objective 49 runs 6 and 7): recordlock_unix.go,
// recordlock_windows.go and repair_test.go held prospective grants covered by
// internal/session/store.go, store.go and cmd/sensei-code/resume.go carried
// derived anchors, and cmd/sensei-code/main.go and commands.go -- examined by
// the graph, already governed -- carried none. The router required a
// derivation over EVERY architectural file, so the gap never closed, and the
// terminal prescribed `import --refresh` of files that do not exist.
const (
	df30Resume   = "cmd/sensei-code/resume.go"
	df30Main     = "cmd/sensei-code/main.go"
	df30Commands = "cmd/sensei-code/commands.go"
	df30Repair   = "internal/session/repair_test.go"
)

// df30Attempt is the plan attempt the DF-30 witnesses route and record for.
func df30Attempt() planAttempt {
	return planAttempt{ID: "pa-df30-measured", TaskID: "task-df30", World: prospectiveWorld}
}

func df30World() map[string]string {
	w := existingWorld()
	for _, f := range []string{df30Resume, df30Main, df30Commands} {
		w[f] = "package main\n"
	}
	return w
}

func df30Declarations() []ProspectiveSurface {
	return append(existingDeclarations(),
		ProspectiveSurface{Path: df30Repair, Package: "session", Role: roleGoRegressionTest, Covering: existingS, Dependencies: []string{"testing"}})
}

func df30Planned() []string {
	return []string{existingS, df30Resume, df30Main, df30Commands, existingUnix, existingWindows, df30Repair}
}

// df30Measured derives the measured plan's grants and coverage through the
// production predicate (coverPlannedAtWorld), over real derived anchors naming
// store.go, journal.go and resume.go.
func df30Measured(t *testing.T) ([]ProspectiveSurface, []prospectiveGrant, []CoverageAnchor) {
	t.Helper()
	decl := df30Declarations()
	anchors := derivedAnchorNaming(t, existingS, existingSibling, df30Resume)
	grants, out := coverPlannedAtWorld(context.Background(), prospectiveWorld, df30Planned(), decl, anchors, worldOf(df30World()))
	if len(grants) != 3 {
		t.Fatalf("premise: the measured plan's three creates are granted, got %+v", grants)
	}
	return decl, grants, out
}

// df30Recorded is the grant record routing writes for df30Attempt.
func df30Recorded(grants []prospectiveGrant) prospectiveRecord {
	return prospectiveRecord{PlanAttemptID: df30Attempt().ID, World: prospectiveWorld, Grants: grants}
}

// df30Action is the action the engine assembles for the measured plan once the
// per-file probes ran: the present files examined, the creates unexamined and
// confirmed absent at the pinned world.
func df30Action(out []CoverageAnchor, units []prospectiveUnit) Action {
	present := []string{existingS, df30Resume, df30Main, df30Commands}
	return Action{
		Stage: StageCandidateEdit, Files: df30Planned(), DerivedCoverage: out,
		Unexamined: []string{existingUnix, existingWindows}, Examined: present,
		Present: present, Absent: []string{existingUnix, existingWindows, df30Repair},
		Prospective: units,
	}
}

func withoutGrantFor(grants []prospectiveGrant, f string) []prospectiveGrant {
	var out []prospectiveGrant
	for _, g := range grants {
		if g.Anchor.File != f {
			out = append(out, g)
		}
	}
	return out
}

// mutateGrant returns grants with f's grant replaced by mut applied to a copy.
func mutateGrant(grants []prospectiveGrant, f string, mut func(g *prospectiveGrant)) []prospectiveGrant {
	out := append([]prospectiveGrant(nil), grants...)
	for i := range out {
		if out[i].Anchor.File == f {
			g := out[i]
			mut(&g)
			out[i] = g
		}
	}
	return out
}

func sameFiles(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// W1 -- THE MEASURED SHAPE. The objective-49 run-7 plan routes past
// coverage-unexamined through the production router: each granted create is
// settled by its own recorded prospective unit, and the examined files without
// derived anchors are not asked to acquire one. Fails at base, where the
// derivation had to cover every architectural file.
func TestDF30W1TheMeasuredRecordLockPlanRoutesPastCoverageUnexamined(t *testing.T) {
	decl, grants, out := df30Measured(t)
	units := prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), df30Recorded(grants))
	for _, u := range units {
		if !u.Valid {
			t.Fatalf("premise: every recorded unit of the measured plan is valid, got %+v", u)
		}
	}
	action := df30Action(out, units)
	// The specimen is live: the examined command files carry no derived anchor,
	// so a plan-wide derivation requirement cannot be met.
	if closed, _ := derivationClosesGap(RequirementUnqualified, out, action.architecturalFiles()); closed {
		t.Fatal("the specimen is not the measured shape: a derivation covers every architectural file")
	}
	if gap, open := unexaminedCoverageGap(action, blindSpotReading{}); open {
		t.Fatalf("granted creates beside examined files kept coverage-unexamined open: %+v", gap)
	}
	if got := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, action); !got.Granted() {
		t.Fatalf("the measured plan did not route past coverage-unexamined: %+v", got)
	}
}

// W2 -- A DECLARATION IS NOT A GRANT. One create merely declared, or a recorded
// grant that is stale or mismatched, keeps the gap open for exactly the creates
// it fails to settle; no settled or examined file is reintroduced.
func TestDF30W2AnUngrantedStaleOrMismatchedCreateKeepsItsGapOpen(t *testing.T) {
	decl, grants, out := df30Measured(t)
	stale, other := df30Recorded(grants), df30Recorded(grants)
	stale.World = "fedcba9876543210fedcba9876543210fedcba98"
	other.PlanAttemptID = "pa-another-attempt"
	for name, c := range map[string]struct {
		rec  prospectiveRecord
		want []string
	}{
		"declared with no recorded grant":      {df30Recorded(withoutGrantFor(grants, existingWindows)), []string{existingWindows}},
		"a grant recorded at another world":    {stale, []string{existingUnix, existingWindows}},
		"a grant recorded for another attempt": {other, []string{existingUnix, existingWindows}},
		"a grant issued for another declaration": {df30Recorded(mutateGrant(grants, existingWindows, func(g *prospectiveGrant) {
			g.Surface.Dependencies = []string{"os"}
		})), []string{existingWindows}},
	} {
		t.Run(name, func(t *testing.T) {
			action := df30Action(out, prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), c.rec))
			got := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, action)
			if !got.ClosesGap() || got.Gap.Kind != gapCoverageUnexamined {
				t.Fatalf("an unsettled create did not keep coverage-unexamined open: %+v", got)
			}
			if !sameFiles(got.Gap.Scope, c.want) {
				t.Fatalf("gap scope = %v, want exactly %v", got.Gap.Scope, c.want)
			}
			for _, settled := range []string{existingS, df30Resume, df30Main, df30Commands} {
				if strings.Contains(got.Condition, settled) {
					t.Fatalf("a governed file was reintroduced into the gap: %q", got.Condition)
				}
			}
		})
	}
}

// W8 -- ATOMIC UNIT. A new-package directory is admitted whole, so one
// malformed or missing member leaves EVERY member of it unsettled; an unrelated
// valid existing-package create in the same plan stays settled.
func TestDF30W8ANewPackageUnitSettlesWholeOrNotAtAll(t *testing.T) {
	lib := prospectiveFor(t, newLibPlanned(), newLibDeclarations(), newPackageAnchors(), newPackageWorld())
	if len(lib) != 3 {
		t.Fatalf("premise: the new library package is granted whole, got %+v", lib)
	}
	unix, _ := existingGrants(t, []string{existingS, existingUnix}, existingDeclarations()[:1], existingWorld())
	if len(unix) != 1 {
		t.Fatalf("premise: the existing-package create is granted, got %+v", unix)
	}
	decl := append(newLibDeclarations(), existingDeclarations()[0])
	planned := append(newLibPlanned(), existingS, existingUnix)
	answerer, config := newLibDir+"/answerer.go", newLibDir+"/config.go"
	creates := []string{answerer, config, existingUnix}
	route := func(grants []prospectiveGrant) (Routing, []prospectiveUnit) {
		units := prospectiveAuthorityUnits(df30Attempt(), decl, planned, df30Recorded(grants))
		return routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, Action{
			Stage: StageCandidateEdit, Files: planned, Unexamined: creates,
			Examined: []string{existingS}, Present: []string{existingS},
			Absent: append(creates, newLibDir+"/answerer_test.go"), Prospective: units,
		}), units
	}
	all := append(append([]prospectiveGrant(nil), lib...), unix...)
	if got, _ := route(all); !got.Granted() {
		t.Fatalf("control: the complete grant state did not settle every create: %+v", got)
	}
	for name, grants := range map[string][]prospectiveGrant{
		"a missing member grant": withoutGrantFor(all, config),
		"a malformed member grant": mutateGrant(all, config, func(g *prospectiveGrant) {
			g.Facts.Imports = nil
		}),
		"a duplicate member grant": append(append([]prospectiveGrant(nil), all...), grantedPaths(lib)[config]),
	} {
		t.Run(name, func(t *testing.T) {
			got, units := route(grants)
			for _, u := range units {
				if sameFiles(u.Files, []string{existingUnix}) && !u.Valid {
					t.Fatalf("the independent existing-package unit was revoked: %+v", u)
				}
			}
			if !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{answerer, config}) {
				t.Fatalf("the atomic unit did not stay unsettled as a whole: %+v", got)
			}
		})
	}
}

// W9 -- INDEPENDENT UNITS. Two existing-package creates are admitted one
// declaration at a time, so each settles on its own: one invalid grant does not
// reopen the other.
func TestDF30W9IndependentCreatesSettleIndependently(t *testing.T) {
	decl, grants, out := df30Measured(t)
	for _, broken := range []string{existingUnix, existingWindows} {
		t.Run(broken, func(t *testing.T) {
			malformed := mutateGrant(grants, broken, func(g *prospectiveGrant) { g.Existing = nil })
			units := prospectiveAuthorityUnits(df30Attempt(), decl, df30Planned(), df30Recorded(malformed))
			got := routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, df30Action(out, units))
			if !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{broken}) {
				t.Fatalf("an invalid grant for %s settled or reopened more than itself: %+v", broken, got)
			}
		})
	}
}

// W8 -- ATOMIC UNIT OVER PRESENCE. A valid recorded unit settles only when the
// pinned world confirms EVERY member absent: one member of a new-package unit
// present, or whose presence was never read, leaves every member unsettled,
// and the independent existing-package create beside it still settles. The
// same holds through a dependency: a command unit whose validity rests on a
// same-plan library settles only while that library's creates are confirmed
// absent too.
func TestDF30W8AUnitSettlesOnlyWhenTheWorldConfirmsItWhollyAbsent(t *testing.T) {
	lib := prospectiveFor(t, newLibPlanned(), newLibDeclarations(), newPackageAnchors(), newPackageWorld())
	unix, _ := existingGrants(t, []string{existingS, existingUnix}, existingDeclarations()[:1], existingWorld())
	decl := append(newLibDeclarations(), existingDeclarations()[0])
	planned := append(newLibPlanned(), existingS, existingUnix)
	answerer, config, libTest := newLibDir+"/answerer.go", newLibDir+"/config.go", newLibDir+"/answerer_test.go"
	units := prospectiveAuthorityUnits(df30Attempt(), decl, planned, df30Recorded(append(append([]prospectiveGrant(nil), lib...), unix...)))
	for _, u := range units {
		if !u.Valid {
			t.Fatalf("premise: every recorded unit is valid, got %+v", u)
		}
	}
	route := func(present, absent []string) Routing {
		return routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, Action{
			Stage: StageCandidateEdit, Files: planned, Unexamined: []string{answerer, config, existingUnix},
			Examined: []string{existingS}, Present: append([]string{existingS}, present...),
			Absent: absent, Prospective: units,
		})
	}
	if got := route(nil, []string{answerer, config, libTest, existingUnix}); !got.Granted() {
		t.Fatalf("control: a valid unit confirmed wholly absent did not settle: %+v", got)
	}
	for name, c := range map[string]struct{ present, absent []string }{
		"one production member present":              {[]string{answerer}, []string{config, libTest, existingUnix}},
		"one production member of unknown presence":  {nil, []string{config, libTest, existingUnix}},
		"the traced test member of unknown presence": {nil, []string{answerer, config, existingUnix}},
	} {
		t.Run(name, func(t *testing.T) {
			got := route(c.present, c.absent)
			if !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{answerer, config}) {
				t.Fatalf("a unit only partly confirmed absent settled a member, or the independent create was reopened: %+v", got)
			}
		})
	}

	grants := prospectiveFor(t, edgePlanned(), edgeDeclarations(), newPackageAnchors(), newPackageWorld())
	command := newCmdDir + "/main.go"
	edgeUnits := prospectiveAuthorityUnits(df30Attempt(), edgeDeclarations(), edgePlanned(), df30Recorded(grants))
	for _, u := range edgeUnits {
		if !u.Valid {
			t.Fatalf("premise: every recorded edge unit is valid, got %+v", u)
		}
	}
	routeCmd := func(absent []string) Routing {
		return routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, Action{
			Stage: StageCandidateEdit, Files: edgePlanned(), Unexamined: []string{command},
			Absent: absent, Prospective: edgeUnits,
		})
	}
	if got := routeCmd([]string{command, answerer, config, libTest}); !got.Granted() {
		t.Fatalf("control: the command beside its wholly absent library did not settle: %+v", got)
	}
	if got := routeCmd([]string{command, config, libTest}); !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{command}) {
		t.Fatalf("the command settled while a library create it depends on was of unknown presence: %+v", got)
	}
}

// DF-30 review f2: several command members bound to ONE same-plan library
// form one valid command unit. The admission predicate forbids only different
// library bindings; projecting the shared edge once per member handed the
// library's declarations and grants to the predicate again as duplicates, so a
// valid command unit read invalid and its absent creates stayed unresolved.
// The unit is still atomic: a missing or malformed command member leaves the
// whole command unit unresolved, and the library unit it depends on stands.
func TestDF30ACommandUnitSharingOneLibraryEdgeSettlesAtomically(t *testing.T) {
	command, two := newCmdDir+"/main.go", newCmdDir+"/two.go"
	twoDecl := ProspectiveSurface{Path: two, Package: "main", Role: roleGoCommandPackage, Covering: cmdS,
		Dependencies: []string{"fmt", answererImport}}
	decl := append(edgeDeclarations(), twoDecl)
	planned := append(edgePlanned(), two)
	grants := prospectiveFor(t, planned, decl, newPackageAnchors(), newPackageWorld())
	edged := 0
	for _, g := range grants {
		if g.Edge != nil && g.Edge.Library == newLibDir {
			edged++
		}
	}
	if edged != 2 {
		t.Fatalf("premise: both command members are granted over the one library edge, got %+v", grants)
	}
	if err := matchGrantsToDeclarations(decl, grants); err != nil {
		t.Fatalf("premise: the canonical predicate admits the plan: %v", err)
	}
	route := func(units []prospectiveUnit) Routing {
		return routeAuthorityForAction(scopedPreflight(t, neighbourCovered), nil, Action{
			Stage: StageCandidateEdit, Files: planned, Unexamined: []string{command, two},
			Absent: planned, Prospective: units,
		})
	}
	commandUnit := func(units []prospectiveUnit) prospectiveUnit {
		for _, u := range units {
			if containsPath(u.Files, command) {
				return u
			}
		}
		t.Fatalf("no unit holds %s: %+v", command, units)
		return prospectiveUnit{}
	}

	units := prospectiveAuthorityUnits(df30Attempt(), decl, planned, df30Recorded(grants))
	for _, u := range units {
		if !u.Valid {
			t.Fatalf("a unit of a canonically admitted plan projected invalid: %+v", u)
		}
	}
	if u := commandUnit(units); !sameFiles(u.Files, []string{command, two}) || !sameFiles(u.Requires, []string{newLibDir + "/answerer.go", newLibDir + "/answerer_test.go", newLibDir + "/config.go"}) {
		t.Fatalf("the command unit is not the two members over the one library: %+v", u)
	}
	if got := route(units); !got.Granted() {
		t.Fatalf("two command members sharing one valid library edge did not settle: %+v", got)
	}

	var missing, malformed []prospectiveGrant
	for _, g := range grants {
		if g.Anchor.File == two {
			bad := g
			bad.Surface.Package = "other"
			malformed = append(malformed, bad)
			continue
		}
		missing = append(missing, g)
		malformed = append(malformed, g)
	}
	for name, rec := range map[string][]prospectiveGrant{"missing": missing, "malformed": malformed} {
		t.Run(name, func(t *testing.T) {
			units := prospectiveAuthorityUnits(df30Attempt(), decl, planned, df30Recorded(rec))
			if u := commandUnit(units); u.Valid {
				t.Fatalf("a command unit with a %s member projected valid: %+v", name, u)
			}
			for _, u := range units {
				if containsPath(u.Files, newLibDir+"/answerer.go") && !u.Valid {
					t.Fatalf("the library unit fell with the command member: %+v", u)
				}
			}
			if got := route(units); !got.ClosesGap() || !sameFiles(got.Gap.Scope, []string{command, two}) {
				t.Fatalf("a command unit with a %s member settled any member: %+v", name, got)
			}
		})
	}
}

// pinWorld binds taskID's candidate to world in a fresh repository root, as
// candidate.Establish records it, so the engine's governedBase -- the world
// every production routing site completes a gap identity with -- is world.
func pinWorld(t *testing.T, e *Engine, taskID, world string) {
	t.Helper()
	e.Repo.Root = t.TempDir()
	dir := filepath.Join(e.Repo.Root, ".sensei-code", "candidates")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"task_id": taskID, "base_sha": world})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, taskID+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := e.governedBase(taskID); got != world {
		t.Fatalf("premise: the candidate is pinned to %s, got %q", world, got)
	}
}

// df30Production is a governed run's production surroundings for the DF-30
// lifecycle witness (W14): a pinned Git world holding the planned files that
// exist, the task's candidate bound to it, a `sensei derive` that answers the
// recipe with the subjects the case supplies, and a Sensei MCP answering the
// scoped region preflight and every per-file probe from state the case can
// change between routings. Nothing here calls the router, a reconciler or the
// ledger directly: an engine built on it is driven only through its entry
// points, and the facts a gap is decided on arrive the way production reads
// them -- presence from Git, grants from the derivation routing records,
// examination from the probe.
//
// The Sensei MCP itself is started by df30Sensei over this fixture's state.
type df30Production struct {
	e     *Engine
	root  string
	state string
	world string
}

// newDF30Production pins taskID to a fresh world holding files, installs the
// derivation stub and returns an engine over them with a durable record.
func newDF30Production(t *testing.T, taskID string, files map[string]string) *df30Production {
	t.Helper()
	root, state := t.TempDir(), t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("init", "-q")
	for rel, content := range files {
		write(filepath.Join(root, rel), content)
	}
	git("add", "-A")
	git("commit", "-q", "-m", "the pinned world")
	world := git("rev-parse", "HEAD")
	// Recipes, the derivation binary and the candidate pin are this run's
	// workflow state, outside the world's tree.
	write(filepath.Join(root, ownedRecipesPath),
		`{"recipes":[{"kind":"command_invocation_confined_to","command":"go","owner":"gosumcheck","search_paths":["."]}]}`)
	bin := filepath.Join(state, "sensei")
	write(bin, "#!/bin/sh\ncat '"+filepath.Join(state, "receipt.json")+"'\n")
	if err := os.Chmod(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSEI_BIN", bin)
	pin, err := json.Marshal(map[string]string{"task_id": taskID, "base_sha": world})
	if err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(root, ".sensei-code", "candidates", taskID+".json"), string(pin))

	w := &df30Production{root: root, state: state, world: world}
	write(filepath.Join(state, "examined.json"), df30ExaminedProbe)
	write(filepath.Join(state, "unexamined.json"), df30UnexaminedProbe)
	w.probes(nil, nil)
	store, err := session.New(t.TempDir(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	w.e = &Engine{Bus: event.NewBus(), Store: store, SessionID: "s1", pending: map[string]chan string{}}
	w.e.Repo.Root = root
	if got := w.e.governedBase(taskID); got != world {
		t.Fatalf("premise: the candidate is pinned to %s, got %q", world, got)
	}
	return w
}

func (w *df30Production) put(t *testing.T, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(w.state, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// region sets the scoped region preflight's structured answer.
func (w *df30Production) region(t *testing.T, structured string) {
	t.Helper()
	w.put(t, "region.json", `{"content":[{"type":"text","text":"region"}],"structuredContent":`+structured+`}`)
}

// probes sets which files a per-file probe reports examined and unexamined.
func (w *df30Production) probes(examined, unexamined []string) {
	_ = os.WriteFile(filepath.Join(w.state, "examined"), []byte(strings.Join(examined, "\n")+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(w.state, "unexamined"), []byte(strings.Join(unexamined, "\n")+"\n"), 0o644)
}

// derives sets the subjects the recipe derives over at the pinned world.
func (w *df30Production) derives(t *testing.T, subjects ...string) {
	t.Helper()
	var subj []string
	for _, s := range subjects {
		subj = append(subj, fmt.Sprintf(`{"file":%q,"entity":"x","role":"subject"}`, s))
	}
	w.put(t, "receipt.json", fmt.Sprintf(`{"result":"DERIVED","pinned_commit":%q,"subjects":[%s],"completeness_scope":["nothing"]}`,
		w.world, strings.Join(subj, ",")))
}

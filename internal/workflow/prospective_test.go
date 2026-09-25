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

	// With an admissible declaration the same absent path is still covered by
	// NOTHING. The declaration establishes the SHAPE the created file must
	// have -- S's package, S's import envelope, checked after creation -- and
	// a shape is not an observation: a file that does not exist has no bytes
	// for S's derivation to have answered about, so letting S's anchor stand
	// in for F made a neighbour an imaginary anchor for an unobserved file.
	// The grant carries what it legitimately establishes; coverage does not.
	grants, out = coverPlannedAtWorld(context.Background(), prospectiveWorld, planned,
		[]ProspectiveSurface{gosumcheckDeclaration()}, anchors, world)
	if len(grants) != 1 || grants[0].Covering != gosumcheckS {
		t.Fatalf("an admissible declaration did not grant against S: %+v", grants)
	}
	if !strings.HasPrefix(grants[0].Anchor.Describe, "PROSPECTIVE") || grants[0].Anchor.Requirement != RequirementInvocationConfinement {
		t.Fatalf("the grant does not record what S establishes, or does not say it is PROSPECTIVE: %+v", grants[0].Anchor)
	}
	for _, a := range out {
		if a.File == gosumcheckF {
			t.Fatalf("a declaration bought the absent file coverage from its neighbour: %+v", a)
		}
	}
	if len(out) != 1 || out[0].File != gosumcheckS {
		t.Fatalf("the existing surface should still be the only covered file, got %+v", out)
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

// --- PLANNED_CREATE: the 02a shape ------------------------------------------
//
// The specimen is the run that could not finish. The objective's whole job was
// to write docs/evidence/operator-actions-inventory.md; the path does not exist
// at the pinned base, so no invariant protects it and no actor reachable from a
// governed run can examine it. Two architect turns tried, and the run ended
// COMPLETE / FAILED, terminal KNOWLEDGE_LIMITED. Nothing about the plan was
// wrong: governance had no way to say "this artifact does not exist yet".
const (
	operatorInventory = "docs/evidence/operator-actions-inventory.md"
	operatorAppendix  = "docs/evidence/operator-actions-appendix.md"
	// The existing code the inventory is BUILT FROM. Ordinary existing files,
	// governed ordinarily -- the exemption must not touch them.
	operatorSubjectA = "internal/workflow/authority.go"
	operatorSubjectB = "cmd/sensei-code/control.go"
)

func inventoryBinding() createBinding {
	return createBinding{TaskID: "task-02a", ObjectiveDigest: strings.Repeat("a", 64), Base: prospectiveWorld}
}

func inventoryFiles() []string {
	return []string{operatorSubjectA, operatorSubjectB, operatorInventory}
}

// inventoryWorld is the pinned base: it holds the evidence inputs and does NOT
// hold the declared output. That absence is the premise of the task.
func inventoryWorld(extra map[string]string) worldReader {
	files := map[string]string{operatorSubjectA: "package workflow\n", operatorSubjectB: "package main\n"}
	for k, v := range extra {
		files[k] = v
	}
	return worldOf(files)
}

func inventoryCreates(t *testing.T, extra map[string]string) ([]plannedCreate, []string) {
	t.Helper()
	return plannedCreates(context.Background(), inventoryBinding(), inventoryFiles(),
		[]string{operatorInventory}, inventoryWorld(extra))
}

// inventoryAction is the 02a plan as the router sees it: two examined evidence
// inputs the graph has facts about, and the declared output.
func inventoryAction(creates []plannedCreate) Action {
	return Action{
		Stage: StageCandidateEdit,
		Files: inventoryFiles(),
		DerivedCoverage: []CoverageAnchor{
			{File: operatorSubjectA, Requirement: RequirementInvocationConfinement, Describe: "derived over " + operatorSubjectA},
			{File: operatorSubjectB, Requirement: RequirementInvocationConfinement, Describe: "derived over " + operatorSubjectB},
		},
		PlannedCreates: plannedCreatePaths(creates),
	}
}

// W1. THE REAL 02a SHAPE. The absent declared output may proceed past the
// pre-existence identity check, while the identical plan without the
// declaration is still refused exactly as 02a was.
func TestTheDeclaredAbsentInventoryProceedsPastThePreExistenceIdentityCheck(t *testing.T) {
	creates, reasons := inventoryCreates(t, nil)
	if len(creates) != 1 {
		t.Fatalf("the declared absent path was not bound as a create: %+v (refused: %v)", creates, reasons)
	}
	got := creates[0]
	if got.Path != operatorInventory || got.Disposition != dispositionCreate {
		t.Fatalf("the disposition does not name the exact path and CREATE: %+v", got)
	}
	if got.Bound != inventoryBinding() {
		t.Fatalf("the create is not bound to the task, its objective digest and its pinned base: %+v", got.Bound)
	}

	// The specimen is real: without the disposition, this exact plan is the
	// refusal 02a died on.
	bare, open := unexaminedCoverageGap(inventoryAction(nil), blindSpotReading{})
	if !open || bare.Gap.Kind != gapDocumentGovernanceUnestablished || !strings.Contains(bare.Condition, operatorInventory) {
		t.Fatalf("premise: the undeclared absent document must still be refused, got open=%v %+v", open, bare)
	}

	// With it, the same plan proceeds.
	if r, open := unexaminedCoverageGap(inventoryAction(creates), blindSpotReading{}); open {
		t.Fatalf("the declared future artifact was still refused for having no identity: %s", r.Condition)
	}

	// And it bought no coverage doing it: no graph anchor, no derived anchor,
	// no invariant identity for a file nobody has observed.
	a := inventoryAction(creates)
	for _, c := range a.DerivedCoverage {
		if c.File == operatorInventory {
			t.Fatalf("the declared create was credited with coverage: %+v", c)
		}
	}
	if ev, asked := a.documentEvidenceFor(operatorInventory); asked || len(ev) != 0 {
		t.Fatalf("the declared create was credited with document evidence: %v (asked=%v)", ev, asked)
	}

	// The candidate that creates exactly what it bound, and nothing else, is
	// admitted -- that is what ends the temporary state.
	if err := inspectPlannedCreates(createdDiff(operatorInventory, "# Operator actions inventory\n"), creates, nil); err != nil {
		t.Fatalf("the declared create, created exactly as declared, was refuted: %v", err)
	}
}

// The exemption is one exact path wide. Everything else in the same plan, the
// same directory and the same run keeps every requirement it had.
func TestACreateDeclarationExemptsNothingButItsOwnExactPath(t *testing.T) {
	creates, _ := inventoryCreates(t, nil)

	// A neighbour in the SAME directory, undeclared: still refused.
	neighbour := inventoryAction(creates)
	neighbour.Files = append(neighbour.Files, operatorAppendix)
	r, open := documentGovernanceGap(neighbour)
	if !open || strings.Contains(r.Condition, operatorInventory) || !strings.Contains(r.Condition, "appendix") {
		t.Fatalf("the exemption reached past the declared path: open=%v %s", open, r.Condition)
	}

	// An existing evidence input the graph has not examined: still refused,
	// and named as itself.
	input := inventoryAction(creates)
	input.DerivedCoverage = []CoverageAnchor{{File: operatorSubjectB, Requirement: RequirementInvocationConfinement, Describe: "derived over " + operatorSubjectB}}
	input.Unexamined = []string{operatorSubjectA}
	r, open = unexaminedCoverageGap(input, blindSpotReading{})
	if !open || r.Gap.Kind != "coverage-unexamined" || !strings.Contains(r.Condition, operatorSubjectA) {
		t.Fatalf("an unexamined existing evidence input was carried by the create's exemption: open=%v %+v", open, r)
	}
}

// W2. DANGEROUS DIRECTION A: a path that ALREADY EXISTS at the pinned base is
// an ordinary existing file and cannot be claimed as a future one. The other
// two ways a declaration fails to bind are refused beside it.
func TestAnExistingPathCannotBeClaimedAsAPlannedCreate(t *testing.T) {
	creates, reasons := inventoryCreates(t, map[string]string{operatorInventory: "# already written\n"})
	if len(creates) != 0 {
		t.Fatalf("a path present at the pinned base was bound as a create: %+v", creates)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "already exists at the pinned base") {
		t.Fatalf("the refusal does not say the path already exists: %v", reasons)
	}
	// And the guard it would have exempted still fires for it.
	if r, open := documentGovernanceGap(inventoryAction(creates)); !open || !strings.Contains(r.Condition, operatorInventory) {
		t.Fatalf("an existing document escaped governance through a refused create: open=%v %+v", open, r)
	}

	// An unanswered read is not absence.
	dark := unreadableAt(inventoryWorld(nil), operatorInventory)
	if c, reasons := plannedCreates(context.Background(), inventoryBinding(), inventoryFiles(), []string{operatorInventory}, dark); len(c) != 0 ||
		len(reasons) != 1 || !strings.Contains(reasons[0], "could not be established") {
		t.Fatalf("an unclassified read failure was taken as absence: %+v %v", c, reasons)
	}

	// A create the plan does not touch is not this plan's future artifact.
	if c, reasons := plannedCreates(context.Background(), inventoryBinding(), []string{operatorSubjectA},
		[]string{operatorInventory}, inventoryWorld(nil)); len(c) != 0 ||
		len(reasons) != 1 || !strings.Contains(reasons[0], "not part of the plan") {
		t.Fatalf("a path outside the plan was bound as its create: %+v %v", c, reasons)
	}

	// A binding that cannot name its task, objective or base grants nothing.
	for _, incomplete := range []createBinding{
		{ObjectiveDigest: strings.Repeat("a", 64), Base: prospectiveWorld},
		{TaskID: "task-02a", Base: prospectiveWorld},
		{TaskID: "task-02a", ObjectiveDigest: strings.Repeat("a", 64)},
	} {
		if c, reasons := plannedCreates(context.Background(), incomplete, inventoryFiles(),
			[]string{operatorInventory}, inventoryWorld(nil)); len(c) != 0 || len(reasons) != 1 {
			t.Fatalf("an unbound declaration granted %+v (%v)", c, reasons)
		}
	}
}

// W3. DANGEROUS DIRECTION B: a second path the task never declared is refused,
// and the declared one must actually be created -- which is what ends the
// temporary state rather than letting it persist.
func TestACandidateThatCreatesAnUndeclaredPathIsRefused(t *testing.T) {
	creates, _ := inventoryCreates(t, nil)
	declared := createdDiff(operatorInventory, "# Operator actions inventory\n\n- sensei briefing\n")

	second := declared + createdDiff(operatorAppendix, "# appendix\n")
	err := inspectPlannedCreates(second, creates, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "planned create refuted:") ||
		!strings.Contains(err.Error(), "operator-actions-appendix.md") ||
		!strings.Contains(err.Error(), "no CREATE disposition") {
		t.Fatalf("an undeclared created path was accepted, or refused for another reason: %v", err)
	}
	if !isProspectiveSurfaceRefutation(err) {
		t.Fatalf("the refutation is not terminal: %v", err)
	}
	if strings.Contains(err.Error(), operatorInventory) {
		t.Fatalf("the refusal blames the declared path: %v", err)
	}

	// A declaration the candidate never honoured is refuted too: the state is
	// temporary, and it ends in the file existing.
	err = inspectPlannedCreates("", creates, nil)
	if err == nil || !strings.Contains(err.Error(), "did not create it") {
		t.Fatalf("a declared create the candidate never made was accepted: %v", err)
	}
}

// W4. A PROSPECTIVE SURFACE IS NOT CREATION AUTHORITY.
//
// The first reading of this repair built the authorized set from BOTH the
// bound creates and the declared surfaces, so a path declared only as a
// surface was authorized to be created with no exact-path CREATE disposition
// at all. Every witness it carried called the inspector with a non-empty
// creates set and a nil surface list, which exercises neither half of that.
//
// The task here holds a real, valid CREATE binding for one path, and creates a
// SECOND path that only the surface declaration names. The surface declaration
// must not carry it.
func TestASurfaceDeclarationAloneDoesNotAuthorizeACreation(t *testing.T) {
	creates, _ := inventoryCreates(t, nil)
	if len(creates) != 1 {
		t.Fatalf("premise: one valid bound create, got %+v", creates)
	}
	surfaces := []ProspectiveSurface{gosumcheckDeclaration()}
	diff := createdDiff(operatorInventory, "# Operator actions inventory\n") +
		createdDiff(gosumcheckF, "package gosumcheck\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")

	err := inspectPlannedCreates(diff, creates, surfaces)
	if err == nil || !strings.HasPrefix(err.Error(), "planned create refuted:") ||
		!strings.Contains(err.Error(), gosumcheckF) ||
		!strings.Contains(err.Error(), "no CREATE disposition") {
		t.Fatalf("a path declared only as a prospective surface was created on the strength of that declaration: %v", err)
	}
	if strings.Contains(err.Error(), operatorInventory) {
		t.Fatalf("the refusal blames the properly bound create: %v", err)
	}
}

// W5. THE SAME REFUSAL WITH NO VALID CREATES AT ALL.
//
// Bypass 1 composed with bypass 2: the surface declaration was the only thing
// naming the path, AND the bound CREATE set was empty, so the inspection
// returned early and never reached even the widened set. Neither the empty set
// nor the declaration may admit the creation.
func TestASurfaceDeclarationAloneDoesNotAuthorizeACreationWithNoBoundCreates(t *testing.T) {
	surfaces := []ProspectiveSurface{gosumcheckDeclaration()}
	diff := createdDiff(gosumcheckF, "package gosumcheck\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")

	err := inspectPlannedCreates(diff, nil, surfaces)
	if err == nil || !strings.HasPrefix(err.Error(), "planned create refuted:") ||
		!strings.Contains(err.Error(), gosumcheckF) ||
		!strings.Contains(err.Error(), "no CREATE disposition") {
		t.Fatalf("a surface declaration authorized a creation for a task with no bound creates: %v", err)
	}
	if !isProspectiveSurfaceRefutation(err) {
		t.Fatalf("the refutation is not terminal: %v", err)
	}
}

// W6. A TASK WITH AN EMPTY BOUND CREATE SET IS STILL INSPECTED.
//
// The inspection was guarded twice on the bound set being non-empty -- once
// inside the inspector and once at the call site -- so the case that most
// needs refusing, a task that bound no creates and created a file anyway, was
// the one case never examined. Both halves are witnessed: the predicate, and
// the call site that must reach it unconditionally.
func TestATaskWithNoBoundCreatesStillHasItsAddedPathsInspected(t *testing.T) {
	err := inspectPlannedCreates(createdDiff(operatorAppendix, "# appendix\n"), nil, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "planned create refuted:") ||
		!strings.Contains(err.Error(), operatorAppendix) ||
		!strings.Contains(err.Error(), "no CREATE disposition") {
		t.Fatalf("a task with zero bound creates created a file and was not refused: %v", err)
	}
	// A candidate that adds nothing has nothing to refuse -- the guard is
	// about added paths, not about the run having work.
	if err := inspectPlannedCreates("", nil, nil); err != nil {
		t.Fatalf("a candidate that created nothing was refuted: %v", err)
	}

	// The call site reaches the predicate unconditionally. Read from the
	// source, because the defect was the SHAPE of the call and not the
	// predicate behind it: a length guard here skips a correct inspector.
	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	const unconditional = "if err := inspectPlannedCreates(diff, e.plannedCreatesFor(taskID), tc.Prospective); err != nil {"
	if !strings.Contains(string(src), unconditional) {
		t.Fatalf("runCandidate does not call the added-path inspection unconditionally; the call must read exactly:\n\t%s", unconditional)
	}
	// And no length guard stands between the call site and the predicate.
	// The routing record legitimately reports only a non-empty bound set, so
	// the shapes named here are the ones that would SKIP the inspection: the
	// bound set read into a conditional, in either spelling.
	for _, guarded := range []string{"plannedCreatesFor(taskID); len(", "len(e.plannedCreatesFor("} {
		if strings.Contains(string(src), guarded) {
			t.Fatalf("the added-path inspection is guarded on the bound CREATE set being non-empty: %q", guarded)
		}
	}
}

// W7. CONJUNCTION, NOT DISJUNCTION.
//
// A new Go test file is admitted only when the SAME exact path holds BOTH the
// bound CREATE disposition and the prospective shape grant. Proving only that
// the conjunction admits would pass on an implementation that accepts either
// condition alone, so both single-condition cases are proven to refuse first,
// and each refusal is checked for the reason it is supposed to give.
func TestANewGoTestNeedsBothTheCreateBindingAndItsShapeGrant(t *testing.T) {
	const src = "package gosumcheck\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = strings.ToUpper }\n"
	diff := createdDiff(gosumcheckF, src)
	surfaces := []ProspectiveSurface{gosumcheckDeclaration()}

	bound, reasons := plannedCreates(context.Background(), inventoryBinding(),
		[]string{gosumcheckS, gosumcheckF}, []string{gosumcheckF},
		worldOf(map[string]string{gosumcheckS: gosumcheckSrc}))
	if len(bound) != 1 || bound[0].Path != gosumcheckF {
		t.Fatalf("premise: the absent test path was not bound as a create: %+v (%v)", bound, reasons)
	}

	// SHAPE ALONE: declared as a surface, no CREATE binding. Refused for
	// having no disposition.
	err := inspectPlannedCreates(diff, nil, surfaces)
	if err == nil || !strings.Contains(err.Error(), "no CREATE disposition") {
		t.Fatalf("the shape grant alone admitted the creation: %v", err)
	}

	// BINDING ALONE: a valid CREATE disposition for exactly this path, and no
	// surface declaration. Refused for having no shape.
	err = inspectPlannedCreates(diff, bound, nil)
	if err == nil || !strings.HasPrefix(err.Error(), "planned create refuted:") ||
		!strings.Contains(err.Error(), gosumcheckF) ||
		!strings.Contains(err.Error(), roleGoRegressionTest) ||
		!strings.Contains(err.Error(), "no prospective surface declaration") {
		t.Fatalf("the CREATE binding alone admitted a new Go test with no declared shape: %v", err)
	}
	if !isProspectiveSurfaceRefutation(err) {
		t.Fatalf("the refutation is not terminal: %v", err)
	}

	// BOTH: admitted, and the shape the declaration states is then enforced
	// against the covering surface's bytes at the pinned world.
	if err := inspectPlannedCreates(diff, bound, surfaces); err != nil {
		t.Fatalf("the conjunction of a CREATE binding and its shape grant was refused: %v", err)
	}
	if err := inspectProspectiveSurfaces(diff, surfaces, gosumcheckFacts(t)); err != nil {
		t.Fatalf("the authorized shape was refuted by the second gate: %v", err)
	}
	// And the second gate stays a constraint: the same conjunction with a
	// shape the declaration does not authorize is still refused.
	wide := createdDiff(gosumcheckF, "package gosumcheck\n\nimport (\n\t\"net/http\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = http.Get }\n")
	if err := inspectPlannedCreates(wide, bound, surfaces); err != nil {
		t.Fatalf("the first gate refused a properly bound path for its imports: %v", err)
	}
	if err := inspectProspectiveSurfaces(wide, surfaces, gosumcheckFacts(t)); err == nil || !strings.Contains(err.Error(), "net/http") {
		t.Fatalf("a bound create escaped its declared import envelope: %v", err)
	}
}

// The same law in the other check the pre-existence identity gates: a declared
// absent PRODUCTION file is not an unexamined existing one. The graph has no
// facts about a path the base does not hold and cannot acquire any -- `sensei
// import --refresh` examines a repository, and the repository does not contain
// this file yet. An absent path the plan did NOT declare stays unexamined.
func TestADeclaredAbsentProductionPathIsNotAnUnexaminedExistingFile(t *testing.T) {
	const generator = "internal/workflow/operatorinventory.go"
	files := append(inventoryFiles(), generator)
	creates, reasons := plannedCreates(context.Background(), inventoryBinding(), files,
		[]string{operatorInventory, generator}, inventoryWorld(nil))
	if len(creates) != 2 {
		t.Fatalf("the declared absent paths were not bound: %+v (refused: %v)", creates, reasons)
	}

	a := inventoryAction(creates)
	a.Files = files
	a.Unexamined = []string{generator}
	if r, open := unexaminedCoverageGap(a, blindSpotReading{}); open {
		t.Fatalf("a declared future production file was refused for having no graph facts: %s", r.Condition)
	}

	// And it exempts nothing but itself. operatorSubjectA sits in the SAME
	// DIRECTORY as the declared create and the graph has not examined it: it
	// is an ordinary existing file the create's exemption must not reach, and
	// the gap must name it and not the create.
	sameDir := inventoryAction(creates)
	sameDir.Files = files
	sameDir.DerivedCoverage = []CoverageAnchor{{File: operatorSubjectB, Requirement: RequirementInvocationConfinement, Describe: "derived over " + operatorSubjectB}}
	sameDir.Unexamined = []string{generator, operatorSubjectA}
	r, open := unexaminedCoverageGap(sameDir, blindSpotReading{})
	if !open || r.Gap.Kind != "coverage-unexamined" || !strings.Contains(r.Condition, operatorSubjectA) {
		t.Fatalf("an unexamined existing file beside a declared create was carried by the create's exemption: open=%v %+v", open, r)
	}
	if strings.Contains(r.Condition, generator) {
		t.Fatalf("the declared create was itself reported as unexamined: %s", r.Condition)
	}

	// Undeclared: the same absent path is an unexamined planned file again.
	b := inventoryAction(nil)
	b.Files = files
	b.Unexamined = []string{generator}
	r, open = unexaminedCoverageGap(b, blindSpotReading{})
	if !open || r.Gap.Kind != "coverage-unexamined" || !strings.Contains(r.Condition, generator) {
		t.Fatalf("an undeclared absent path escaped the coverage question: open=%v %+v", open, r)
	}
}

// The disposition travels in the DURABLE plan bound, not in a record of its
// own. A resume that lost it would ask a path the base provably lacks for an
// identity all over again, and the re-planned scope carries it for the same
// reason the files it belongs to are carried.
func TestTheCreateDispositionSurvivesTheDurablePlanBound(t *testing.T) {
	d := architectureDecision{Plan: "p", Files: inventoryFiles(), Creates: []string{operatorInventory}}
	found := session.FindInterrupted([]event.Event{
		event.New("s", "t1", event.SourceSystem, event.TaskCreated, "task", nil),
		event.New("s", "t1", event.SourceArchitect, event.PlanProposed, "plan",
			proposedPlan{architectureDecision: d, PlanSource: PlanByArchitect}),
	})
	if len(found) != 1 {
		t.Fatalf("premise: one interrupted task, got %+v", found)
	}
	bound, err := (&Engine{}).restorePlanBound(found[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(bound.Creates, ",") != operatorInventory {
		t.Fatalf("the CREATE disposition did not survive the plan record: %+v", bound.Creates)
	}

	// A re-plan moves the whole scope, this included, and says so for the record.
	var tc taskContext
	applyPlanScope(&tc, d)
	if strings.Join(tc.Creates, ",") != operatorInventory {
		t.Fatalf("a re-planned scope dropped the CREATE disposition: %+v", tc.Creates)
	}
	if !strings.Contains(scopeSummary(tc), "creates "+operatorInventory) {
		t.Fatalf("the recorded scope does not say what the candidate is bound to create: %s", scopeSummary(tc))
	}
}

// --- f1: A DECLARATION THAT CANNOT BIND MUST REFUSE THE PLAN ----------------
//
// The first cycle of this repair DERIVED the refusals correctly and then only
// REPORTED them. routePlan emitted each refused declaration as a Status event
// and carried on; Resume threw the returned reasons away entirely. So a path
// the base already holds, a path outside the plan, or a path whose absence
// could not be read was silently downgraded to an ordinary path -- and where
// that path's ORDINARY coverage happened to be sufficient and the candidate
// added nothing, the whole run proceeded on an invalid disposition that
// nothing ever refused. W2 could not see this: it asserted the refusal REASON,
// which the first cycle produced, and the reason is not the boundary.
//
// pinnedBaseWith commits files into a scratch repository and returns a reader
// bound to that real commit. Real git, not worldOf: what is under test is
// whether a declaration binds against what a base ACTUALLY holds.
func pinnedBaseWith(t *testing.T, files map[string]string) (worldReader, string) {
	t.Helper()
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
	for name, src := range files {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, name), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("commit", "-q", "-m", "base")
	return gitShowAt(root), run("rev-parse", "HEAD")
}

// f1 control: the falsely declared path is an ORDINARILY COVERED existing Go
// file, so no other guard in the run has anything to say about it. W2 used the
// 02a document, which document governance refuses anyway -- that witness could
// pass on an implementation where the invalid disposition itself refused
// nothing. Here the only thing that can refuse the plan is the disposition.
func TestAFalselyDeclaredCreateOverAnOrdinarilyCoveredFileRefusesThePlan(t *testing.T) {
	read, base := pinnedBaseWith(t, map[string]string{gosumcheckS: gosumcheckSrc})
	bound := createBinding{TaskID: "task-f1", ObjectiveDigest: strings.Repeat("b", 64), Base: base}
	planned := []string{gosumcheckS, gosumcheckF}

	// The premise that makes this a control and not a coincidence: nothing
	// else in the run refuses this file.
	covered := Action{Stage: StageCandidateEdit, Files: []string{gosumcheckS}, DerivedCoverage: gosumcheckAnchors()}
	if r, open := unexaminedCoverageGap(covered, blindSpotReading{}); open {
		t.Fatalf("premise: the falsely declared file must be ordinarily covered, got %+v", r)
	}
	if r, open := documentGovernanceGap(covered); open {
		t.Fatalf("premise: a production Go file is not a document artifact, got %+v", r)
	}

	creates, reasons := plannedCreates(context.Background(), bound, planned, []string{gosumcheckS}, read)
	if len(creates) != 0 {
		t.Fatalf("a file present at the real pinned base was bound as a create: %+v", creates)
	}
	if len(reasons) != 1 || !strings.Contains(reasons[0], "already exists at the pinned base") {
		t.Fatalf("the refusal does not say the path already exists: %v", reasons)
	}

	// AND IT IS A REFUSAL, NOT A REPORT. A typed error the caller cannot
	// mistake for a status line, naming the declaration it refused.
	err := createDispositionRefusal(reasons)
	if err == nil {
		t.Fatal("an unbindable CREATE declaration produced no refusal; a reason nobody must act on is a report, and the plan proceeds on an invalid disposition")
	}
	if !errors.Is(err, errCreateDisposition) {
		t.Fatalf("the refusal is not typed as a CREATE-disposition refusal: %v", err)
	}
	if !strings.Contains(err.Error(), gosumcheckS) || !strings.Contains(err.Error(), "already exists at the pinned base") {
		t.Fatalf("the refusal does not carry which declaration failed and why: %v", err)
	}

	// The valid direction, at the same real base, still binds and refuses
	// nothing: the guard is about invalid declarations, not about declaring.
	ok, okReasons := plannedCreates(context.Background(), bound, planned, []string{gosumcheckF}, read)
	if len(ok) != 1 || ok[0].Path != gosumcheckF || ok[0].Bound != bound {
		t.Fatalf("the absent declared path did not bind at the real base: %+v (%v)", ok, okReasons)
	}
	if err := createDispositionRefusal(okReasons); err != nil {
		t.Fatalf("a plan whose every declaration bound was refused anyway: %v", err)
	}
}

// Empty, duplicate and conflicting declarations were NORMALIZED AWAY: the loop
// cleaned each path, skipped "." and skipped anything already seen, so a
// malformed declaration simply vanished and the plan ran as though it had
// never been made. An exact binding that quietly discards part of what it was
// asked to bind is not exact.
func TestAnEmptyDuplicateOrConflictingCreateDeclarationIsRefusedNotNormalized(t *testing.T) {
	read, base := pinnedBaseWith(t, map[string]string{gosumcheckS: gosumcheckSrc})
	bound := createBinding{TaskID: "task-f1", ObjectiveDigest: strings.Repeat("b", 64), Base: base}
	planned := []string{gosumcheckS, gosumcheckF}

	for name, tc := range map[string]struct {
		declared []string
		want     string
	}{
		"empty string":         {[]string{""}, "names no path"},
		"whitespace only":      {[]string{"  \t "}, "names no path"},
		"the current dir":      {[]string{"."}, "names no path"},
		"a valid one and none": {[]string{gosumcheckF, ""}, "names no path"},
		"declared twice":       {[]string{gosumcheckF, gosumcheckF}, "declared as a create more than once"},
		"two spellings":        {[]string{gosumcheckF, "./" + gosumcheckF}, "two spellings of one path"},
	} {
		creates, reasons := plannedCreates(context.Background(), bound, planned, tc.declared, read)
		err := createDispositionRefusal(reasons)
		if err == nil {
			t.Errorf("%s: the declaration was normalized away instead of refused", name)
			continue
		}
		if !errors.Is(err, errCreateDisposition) {
			t.Errorf("%s: the refusal is not typed: %v", name, err)
		}
		if !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: the refusal does not name what was wrong (want %q): %v", name, tc.want, err)
		}
		// All or nothing. A plan with one bad declaration binds none of them:
		// a partial binding is an exemption the plan was never granted.
		if len(creates) != 0 {
			t.Errorf("%s: a refused plan still bound %+v", name, creates)
		}
	}

	// A declaration with no way to read the pinned base establishes nothing,
	// and silently binding nothing is how it used to pass.
	if creates, reasons := plannedCreates(context.Background(), bound, planned, []string{gosumcheckF}, nil); len(creates) != 0 ||
		createDispositionRefusal(reasons) == nil {
		t.Fatalf("a declaration with no reader for the pinned base was dropped rather than refused: %+v %v", creates, reasons)
	}
	// Declaring nothing is not a defect.
	if _, reasons := plannedCreates(context.Background(), bound, planned, nil, read); createDispositionRefusal(reasons) != nil {
		t.Fatalf("a plan that declared no creates was refused: %v", reasons)
	}
}

// The refusal must be APPLIED, at both places the binding is derived. The
// first cycle derived it at both and applied it at neither: routePlan emitted
// and continued, Resume discarded. A guard's position is part of its
// correctness, so this reads the two call sites rather than the predicate --
// the predicate was already right.
//
// Source-level, because these two functions cannot be driven from this test
// surface: routePlan takes a *sensei.Client and Resume loads a candidate
// identity, and neither package may be imported here.
func TestRoutingAndResumeRefuseAnUnbindableCreateDeclarationRatherThanReportingIt(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	region := func(sig string) string {
		t.Helper()
		at := strings.Index(src, sig)
		if at < 0 {
			t.Fatalf("%s is gone from engine.go", sig)
		}
		body := src[at:]
		if end := strings.Index(body, "\n}\n"); end > 0 {
			body = body[:end]
		}
		return body
	}

	// ROUTING. The refusal is returned, and returned before anything is routed
	// or assessed from the same action.
	route := region("func (e *Engine) routePlan(")
	const routed = "creates, createRefused := e.bindPlannedCreates(ctx, taskID, d.Files, d.Creates)"
	if !strings.Contains(route, routed) {
		t.Fatalf("routePlan does not capture the CREATE binding's refusal; the call must read exactly:\n\t%s", routed)
	}
	if !strings.Contains(route, "if createRefused != nil {") ||
		!strings.Contains(route, "return Routing{}, sensei.PreflightDecision{}, Action{}, createRefused") {
		t.Error("routePlan does not RETURN the CREATE-disposition refusal; reporting it as an event and continuing is what let an invalid disposition reach implementation")
	}
	bindAt := strings.Index(route, routed)
	for _, later := range []string{"routeAuthorityForAction", "AssessConsequences"} {
		if at := strings.Index(route, later); at >= 0 && at < bindAt {
			t.Errorf("the CREATE declaration is bound after %s, so an unbindable one reaches consequence routing", later)
		}
	}

	// RESUME. Re-derived and re-refused before any candidate work, because a
	// resume does not re-route: this is the only place the restored plan's
	// declarations are judged again.
	resume := region("func (e *Engine) Resume(")
	const resumed = "if _, err := e.bindPlannedCreates(ctx, task.TaskID, bound.Files, bound.Creates); err != nil {"
	if !strings.Contains(resume, resumed) {
		t.Fatalf("Resume does not refuse an unbindable CREATE declaration; the call must read exactly:\n\t%s", resumed)
	}
	if strings.Contains(resume, "\n\t\te.bindPlannedCreates(") {
		t.Error("Resume calls the CREATE binding as a bare statement and discards its refusal")
	}
	after := resume[strings.Index(resume, resumed):]
	if at := strings.Index(after, "fail(err)"); at < 0 || at > 120 {
		t.Error("Resume does not fail the run on a refused CREATE declaration")
	}
	if at, work := strings.Index(resume, resumed), strings.Index(resume, "e.implement("); at < 0 || work < 0 || at > work {
		t.Error("the resumed run reaches candidate work before the restored CREATE declarations are re-bound and refused")
	}
}

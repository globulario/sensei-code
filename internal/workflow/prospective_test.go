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

	// With an admissible declaration the same absent path is covered ONLY by
	// the prospective anchor, which names S and says so.
	grants, out = coverPlannedAtWorld(context.Background(), prospectiveWorld, planned,
		[]ProspectiveSurface{gosumcheckDeclaration()}, anchors, world)
	if len(grants) != 1 || grants[0].Covering != gosumcheckS {
		t.Fatalf("an admissible declaration did not grant against S: %+v", grants)
	}
	n := 0
	for _, a := range out {
		if a.File != gosumcheckF {
			continue
		}
		n++
		if !strings.HasPrefix(a.Describe, "PROSPECTIVE") || a.Requirement != RequirementInvocationConfinement {
			t.Fatalf("the absent file's coverage is not the prospective anchor: %+v", a)
		}
	}
	if n != 1 {
		t.Fatalf("the absent file carries %d anchors, want exactly the prospective one", n)
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

// The dependency-edge world: the new-package world above, with a plan that
// creates a library package and a command that imports it. The fixture module
// is example.com/m, so the library's import path is example.com/m/internal/answerer.
const edgeImport = "example.com/m/" + newLibDir

func edgeCmdDeclarations(deps ...string) []ProspectiveSurface {
	return []ProspectiveSurface{{Path: newCmdDir + "/main.go", Package: "main", Role: roleGoCommandPackage, Covering: cmdS,
		Dependencies: append([]string{"fmt", "os"}, deps...)}}
}

func edgePlanned() []string { return append(newLibPlanned(), newCmdDir+"/main.go") }

func edgeDeclarations(deps ...string) []ProspectiveSurface {
	return append(newLibDeclarations(), edgeCmdDeclarations(deps...)...)
}

// edgeRecord is the session record a resumed task restores its grants from.
func edgeRecord(gs []prospectiveGrant) session.Interrupted {
	raw, _ := json.Marshal(prospectiveRecord{World: prospectiveWorld, Grants: gs})
	return session.Interrupted{TaskID: "t", ProspectiveRecord: raw}
}

// edgeW1Engine is a governed engine over a real repository at a pinned base
// holding the library's named covering surface and one unrelated file, with
// one composed recipe and a `sensei derive` whose subjects are read from
// W1_SUBJECTS. The candidate identity pins the base, so coverageAtWorld runs
// exactly as routing does.
func edgeW1Engine(t *testing.T, taskID string, recipe string) *Engine {
	t.Helper()
	repo, _ := mintRepo(t)
	files := map[string]string{"go.mod": "module example.com/m\n", libS: libSSource, cmdS: cmdSSource,
		"internal/other/other.go": "package other\n", committedRecipesPath: `{"recipes":[` + recipe + `]}`}
	for f, src := range files {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(repo.Root, f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(repo.Root, f), []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"commit", "-q", "-m", "world"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo.Root}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	head, err := exec.Command("git", "-C", repo.Root, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	id := candidateIdentityWithBase(strings.TrimSpace(string(head)))
	id.TaskID = taskID
	if err := id.Save(repo.Root); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "sensei")
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -revision ] && rev=\"$2\"; shift; done\n" +
		"subj=''; for f in $W1_SUBJECTS; do subj=\"$subj${subj:+,}{\\\"file\\\":\\\"$f\\\"}\"; done\n" +
		"printf '{\"result\":\"DERIVED\",\"pinned_commit\":\"%s\",\"subjects\":[%s]}' \"$rev\" \"$subj\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSEI_BIN", bin)
	return &Engine{Repo: repo}
}

// Edge W1: what a new-package declaration must establish is that the ONE
// covering surface it names is covered at the pinned world, and the surface
// looked at to establish it is that named file -- unplanned, in another
// directory -- derived by the composed recipes at the governed base. Through
// coverageAtWorld, the path routing takes, a derivation naming the surface
// grants the package. A derivation naming only an unrelated file introduces
// no surface, a declaration does not cover what it names, and a recipe the
// running task wrote is excluded before it is spent.
//
// Honesty note: at base 6122a03 the engine already handed every derived
// subject to the predicate, so the granted case passes there too. What this
// change adds is that no subject other than a same-directory or NAMED surface
// is a surface; the 47a zero-anchor outcome came from no composed recipe
// having its named surfaces as subjects, which no engine change may repair.
func TestEdgeW1AnUnplannedNamedCoveringSurfaceCoversByItsDerivation(t *testing.T) {
	ctx := context.Background()
	recipe := `{"kind":"field_access_under_lock","dir":"internal/relay","type":"R","field":"f","lock":"mu"}`

	e := edgeW1Engine(t, "task-w1", recipe)
	t.Setenv("W1_SUBJECTS", libS+" internal/other/other.go")
	c, ok := e.coverageAtWorld(ctx, "task-w1", newLibPlanned(), newLibDeclarations())
	got := grantedPaths(c.prospective)
	if !ok || len(c.prospective) != 3 || len(got) != 3 || got[newLibDir+"/answerer.go"].Covering != libS {
		t.Fatalf("the library package was not granted against its derived, unplanned covering surface: ok=%v %+v", ok, c.prospective)
	}
	for _, a := range c.coverage {
		if strings.Contains(a.File, "internal/other") || strings.Contains(a.Describe, "PROSPECTIVE") && !strings.Contains(a.Describe, libS) {
			t.Fatalf("coverage came from a surface no declaration named: %+v", a)
		}
	}
	// Only the named surface (and planned files' own directories) are surfaces.
	sameDir, named := prospectiveSurfaceScope(newLibPlanned(), newLibDeclarations())
	if len(named) != 1 || !named[libS] || sameDir["internal/other"] || sameDir["internal/relay"] {
		t.Fatalf("the surface scope is not exactly the named covering surface: sameDir=%v named=%v", sameDir, named)
	}
	if _, named := prospectiveSurfaceScope(newLibPlanned(), nil); len(named) != 0 {
		t.Fatalf("a surface was introduced without a new-package declaration: %v", named)
	}

	// The derivation names only an unrelated file: the named surface is not
	// derived, and naming it is not coverage.
	t.Setenv("W1_SUBJECTS", "internal/other/other.go")
	if c, _ := e.coverageAtWorld(ctx, "task-w1", newLibPlanned(), newLibDeclarations()); len(c.prospective) != 0 {
		t.Fatalf("a covering surface no derivation named was treated as covered: %+v", c.prospective)
	}

	// Future-only: the running task's own recipe establishes nothing for it.
	t.Setenv("W1_SUBJECTS", libS)
	own := edgeW1Engine(t, "task-w1",
		`{"kind":"field_access_under_lock","dir":"internal/relay","type":"R","field":"f","lock":"mu","provenance":{"origin_task":"task-w1"}}`)
	if c, _ := own.coverageAtWorld(ctx, "task-w1", newLibPlanned(), newLibDeclarations()); len(c.prospective) != 0 {
		t.Fatalf("a recipe written by the running task covered its named surface: %+v", c.prospective)
	}
}

// Edge W2: a new command that imports exactly the same-plan library package,
// whose whole planned production set is granted, is granted with the edge
// recorded; inspection accepts a candidate carrying exactly that import.
func TestEdgeW2ACommandMayImportItsGrantedSamePlanLibrary(t *testing.T) {
	decl := edgeDeclarations(edgeImport)
	grants := prospectiveFor(t, edgePlanned(), decl, newPackageAnchors(), newPackageWorld())
	got := grantedPaths(grants)
	if len(grants) != 4 || len(got) != 4 {
		t.Fatalf("expected four grants over the library and its command, got %+v", grants)
	}
	cmd := got[newCmdDir+"/main.go"]
	if cmd.Edge == nil || cmd.Edge.ImportPath != edgeImport || cmd.Edge.Dir != newLibDir || cmd.Edge.Module != "." {
		t.Fatalf("the command grant does not record its edge to %s: %+v", edgeImport, cmd)
	}
	for f, g := range got {
		if f != newCmdDir+"/main.go" && g.Edge != nil {
			t.Fatalf("%s carries an edge only a command may carry: %+v", f, g)
		}
	}
	if err := matchGrantsToDeclarations(decl, grants); err != nil {
		t.Fatalf("the issued grants do not match their declarations: %v", err)
	}
	facts, edges := map[string]prospectiveFacts{}, map[string]string{}
	for _, g := range grants {
		facts[g.Anchor.File] = g.Facts
		if g.Edge != nil {
			edges[g.Anchor.File] = g.Edge.ImportPath
		}
	}
	lib := createdDiff(newLibDir+"/answerer.go", "package answerer\n\nimport \"strings\"\n\nvar X = strings.ToUpper\n") +
		createdDiff(newLibDir+"/config.go", "package answerer\n\nimport \"os\"\n\nvar Y = os.Getenv\n") +
		createdDiff(newLibDir+"/answerer_test.go", "package answerer\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n")
	good := lib + createdDiff(newCmdDir+"/main.go", "package main\n\nimport (\n\t\"fmt\"\n\t\""+edgeImport+"\"\n)\n\nfunc main() { fmt.Println(answerer.X) }\n")
	if err := inspectProspectiveSurfacesWithEdges(good, decl, facts, edges); err != nil {
		t.Fatalf("the admitted command shape was refused: %v", err)
	}
	if err := inspectProspectiveSurfaces(good, decl, facts); err == nil || !strings.Contains(err.Error(), edgeImport) {
		t.Fatalf("inspection without the recorded edge accepted the edge import: %v", err)
	}
	for name, diff := range map[string]string{
		"a second novel import":        strings.Replace(good, "+\t\"fmt\"\n", "+\t\"fmt\"\n+\t\"net/http\"\n", 1),
		"a library importing the edge": strings.Replace(good, "+import \"os\"", "+import (\n+\t\"os\"\n+\t\""+edgeImport+"\"\n+)", 1),
		"an undeclared file":           good + createdDiff(newCmdDir+"/extra.go", "package main\n"),
	} {
		if diff == good {
			t.Fatalf("%s: the mutation did not apply", name)
		}
		if err := inspectProspectiveSurfacesWithEdges(diff, decl, facts, edges); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
			t.Fatalf("%s was not refused: %v", name, err)
		}
	}
	// Only a command uses an edge, even when one is keyed to a library file.
	libEdged := strings.Replace(good, "+import \"os\"", "+import (\n+\t\"os\"\n+\t\""+edgeImport+"\"\n+)", 1)
	edges[newLibDir+"/config.go"] = edgeImport
	if err := inspectProspectiveSurfacesWithEdges(libEdged, decl, facts, edges); err == nil || !strings.Contains(err.Error(), newLibDir+"/config.go") {
		t.Fatalf("a library file was allowed an edge: %v", err)
	}
	delete(edges, newLibDir+"/config.go")
	// The edge is shown to the worker as part of the grant it operates under.
	if rendered := renderProspectiveGrants(grants); !strings.Contains(rendered, "dependency edge: "+edgeImport+" (same-plan library "+newLibDir+")") {
		t.Fatalf("the rendered grant does not state the edge:\n%s", rendered)
	}
}

// Edge W3: ABSENCE. The command is granted nothing when its novel import is
// not a same-plan library, when that library is refused in any part, when a
// second novel import is requested, or when its own directory holds an
// undeclared file; and a named covering surface that is uncovered or missing
// grants the library nothing.
func TestEdgeW3NoEdgeOutsideTheOneSamePlanGrantedLibrary(t *testing.T) {
	world := newPackageWorld()
	commandGranted := func(grants []prospectiveGrant) bool {
		_, ok := grantedPaths(grants)[newCmdDir+"/main.go"]
		return ok
	}
	libRefused := func() []ProspectiveSurface {
		ds := edgeDeclarations(edgeImport)
		ds[1].Dependencies = append(ds[1].Dependencies, "net/http")
		return ds
	}
	cases := []struct {
		name    string
		planned []string
		decl    []ProspectiveSurface
		anchors []CoverageAnchor
		files   map[string]string
	}{
		{"an import of a package that is not same-plan", edgePlanned(), edgeDeclarations("example.com/m/internal/other"), newPackageAnchors(), world},
		{"the library is not in the plan", []string{newCmdDir + "/main.go"}, edgeCmdDeclarations(edgeImport), newPackageAnchors(), world},
		{"a planned production create of the library is refused", edgePlanned(), libRefused(), newPackageAnchors(), world},
		{"the library holds an undeclared planned file", append(edgePlanned(), newLibDir+"/mailbox.go"), edgeDeclarations(edgeImport), newPackageAnchors(), world},
		{"the library's covering surface is uncovered", edgePlanned(), edgeDeclarations(edgeImport), newPackageAnchors()[1:], world},
		{"the library's covering surface is missing", edgePlanned(), edgeDeclarations(edgeImport), newPackageAnchors(),
			map[string]string{"go.mod": "module example.com/m\n", cmdS: cmdSSource}},
		{"a second novel import", edgePlanned(), edgeDeclarations(edgeImport, "net/http"), newPackageAnchors(), world},
		{"an undeclared file beside the command", append(edgePlanned(), newCmdDir+"/extra.go"), edgeDeclarations(edgeImport), newPackageAnchors(), world},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if grants := prospectiveFor(t, c.planned, c.decl, c.anchors, c.files); commandGranted(grants) {
				t.Fatalf("%s: the command was granted: %+v", c.name, grants)
			}
		})
	}
	// A command in another Go module than the library: the nested module's
	// command is otherwise admissible, so only the module separates them.
	nestedS, nestedCmd := "svc/cmd/sensei-code/main.go", "svc/cmd/sensei-code-answerer/main.go"
	nested := newPackageWorld()
	nested["svc/go.mod"], nested[nestedS] = "module example.com/svc\n", cmdSSource
	nestedAnchors := append(newPackageAnchors(), CoverageAnchor{File: nestedS, Requirement: RequirementInvocationConfinement, Describe: "x"})
	nestedDecl := func(deps ...string) []ProspectiveSurface {
		return append(newLibDeclarations(), ProspectiveSurface{Path: nestedCmd, Package: "main", Role: roleGoCommandPackage, Covering: nestedS,
			Dependencies: append([]string{"fmt"}, deps...)})
	}
	nestedPlanned := append(newLibPlanned(), nestedCmd)
	if _, ok := grantedPaths(prospectiveFor(t, nestedPlanned, nestedDecl(), nestedAnchors, nested))[nestedCmd]; !ok {
		t.Fatalf("premise: the nested-module command without the edge is admissible")
	}
	if _, ok := grantedPaths(prospectiveFor(t, nestedPlanned, nestedDecl(edgeImport), nestedAnchors, nested))[nestedCmd]; ok {
		t.Fatalf("a command was granted an edge to a library in another Go module")
	}
	// Two same-plan libraries, both granted whole: the command may depend on
	// either, and on no more than one.
	twoLibs := func(deps ...string) []ProspectiveSurface {
		return append(edgeDeclarations(deps...),
			ProspectiveSurface{Path: "internal/mailbox/mailbox.go", Package: "mailbox", Role: roleGoLibraryPackage, Covering: libS, Dependencies: []string{"fmt"}})
	}
	twoPlanned := append(edgePlanned(), "internal/mailbox/mailbox.go")
	if !commandGranted(prospectiveFor(t, twoPlanned, twoLibs("example.com/m/internal/mailbox"), newPackageAnchors(), world)) {
		t.Fatalf("premise: the command may depend on the second same-plan library alone")
	}
	if grants := prospectiveFor(t, twoPlanned, twoLibs(edgeImport, "example.com/m/internal/mailbox"), newPackageAnchors(), world); commandGranted(grants) {
		t.Fatalf("a command was granted edges to two same-plan libraries: %+v", grants)
	}
	// One edge per command PACKAGE: two production files of one command,
	// each naming a different granted same-plan library, grant no command
	// file; the same library on both is one edge and is granted.
	twoFileCmd := func(second string) []ProspectiveSurface {
		return append(twoLibs(edgeImport), ProspectiveSurface{Path: newCmdDir + "/serve.go", Package: "main", Role: roleGoCommandPackage,
			Covering: cmdS, Dependencies: []string{"fmt", second}})
	}
	twoFilePlanned := append(twoPlanned, newCmdDir+"/serve.go")
	if !commandGranted(prospectiveFor(t, twoFilePlanned, twoFileCmd(edgeImport), newPackageAnchors(), world)) {
		t.Fatalf("premise: two command files sharing the one edge are admissible")
	}
	if grants := prospectiveFor(t, twoFilePlanned, twoFileCmd("example.com/m/internal/mailbox"), newPackageAnchors(), world); commandGranted(grants) {
		t.Fatalf("a two-file command was granted edges to two same-plan libraries: %+v", grants)
	} else if _, ok := grantedPaths(grants)[newCmdDir+"/serve.go"]; ok {
		t.Fatalf("one file of a two-edge command was granted: %+v", grants)
	} else if _, ok := grantedPaths(grants)["internal/mailbox/mailbox.go"]; !ok {
		t.Fatalf("premise: the second library is itself granted: %+v", grants)
	}
	// A library in a different directory is not the edge even if its path is
	// a prefix of the requested import.
	if grants := prospectiveFor(t, edgePlanned(), edgeDeclarations(edgeImport+"/sub"), newPackageAnchors(), world); commandGranted(grants) {
		t.Fatalf("an import below the same-plan library was admitted as the edge: %+v", grants)
	}
	// A regression test beside the command may not use the edge.
	withTest := append(edgeDeclarations(edgeImport), ProspectiveSurface{Path: newCmdDir + "/main_test.go", Package: "main", Role: roleGoRegressionTest,
		Dependencies: []string{"testing", edgeImport}})
	if grants := prospectiveFor(t, append(edgePlanned(), newCmdDir+"/main_test.go"), withTest, newPackageAnchors(), world); commandGranted(grants) {
		t.Fatalf("a test beside the command was admitted the edge: %+v", grants)
	}
}

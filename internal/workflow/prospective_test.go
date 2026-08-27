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
	body := funcBody(t, "internal/workflow/engine.go", "derivedCoverage")
	// funcBody renders selector paths as "e.governedBase( " tokens.
	base, head := strings.Index(body, "e.governedBase("), strings.Index(body, "e.Repo.Head(")
	if base < 0 {
		t.Fatal("derivedCoverage does not read the pinned base")
	}
	if head >= 0 && head < base {
		t.Fatal("derivedCoverage consults HEAD before the pinned base")
	}
	if !strings.Contains(body, "declarations nil") {
		t.Fatal("without a pinned base, prospective declarations are still evaluated")
	}
}

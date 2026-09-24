package workflow

// M2.2 -- the frozen falsifiers of docs/work/m2.2-existing-test-edit-authority.md.
// Each leaves F ungranted, refutes the candidate, or is the one positive.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
)

const (
	teS     = "modfile/rule.go"
	teF     = "modfile/rule_test.go"
	teWorld = "989c6210000000000000000000000000000000000"
	teSSrc  = "package modfile\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\ntype File struct{ Module *Module }\ntype Module struct{}\n\nfunc (f *File) AddComment(s string) { _ = fmt.Sprint(strings.TrimSpace(s)) }\n"
	teFSrc  = "//go:build go1.20\n\npackage modfile\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = strings.ToUpper }\n"
)

func teCovered() []CoverageAnchor {
	return []CoverageAnchor{{File: teS, Requirement: RequirementMutationConfinement, Describe: "PROSPECTIVE? no -- a derived anchor over rule.go"}}
}

func teRead(files map[string]string) worldReader {
	return func(_ context.Context, world, f string) ([]byte, error) {
		if world != teWorld {
			return nil, errors.New("wrong world")
		}
		src, ok := files[f]
		if !ok {
			return nil, errNotAtWorld
		}
		if src == "\x00unreadable" {
			return nil, errors.New("unclassified read failure")
		}
		return []byte(src), nil
	}
}

// The positive: an existing test beside a covered subject, same package,
// same directory, present at the world -> one grant bound to F's bytes.
func TestAnExistingTestBesideACoveredSubjectIsGrantedAnEdit(t *testing.T) {
	grants, reasons := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, teRead(map[string]string{teS: teSSrc, teF: teFSrc}))
	if len(grants) != 1 || len(reasons) != 0 {
		t.Fatalf("grants=%+v reasons=%v", grants, reasons)
	}
	g := grants[0]
	if g.Path != teF || g.Covering != teS || g.World != teWorld || g.BaseHash == "" || g.Facts.Package != "modfile" || !g.Facts.Imports["testing"] || len(g.Facts.Constraints) != 1 {
		t.Fatalf("grant is not bound to F at the world: %+v", g)
	}
	if got := operationalFiles(grants); len(got) != 1 || got[0] != teF {
		t.Fatalf("operational files: %v", got)
	}
}

// The frozen falsifiers that leave F ungranted.
func TestAnExistingTestIsNotGrantedWhenThePredicateFails(t *testing.T) {
	cases := map[string]struct {
		planned []string
		covered []CoverageAnchor
		files   map[string]string
		reason  string
	}{
		"foreign-package test": {[]string{teS, teF}, teCovered(), map[string]string{teS: teSSrc, teF: strings.Replace(teFSrc, "package modfile", "package modfile_test", 1)}, "foreign-package"},
		"missing test":         {[]string{teS, teF}, teCovered(), map[string]string{teS: teSSrc}, "absent at the pinned world"},
		// The wording changed with the rule: "architectural coverage" named only the
		// derived instrument, and the refusal now has to say that NEITHER instrument
		// governs a neighbour, or it would misreport which evidence is missing.
		"ungoverned sibling":  {[]string{teS, teF}, nil, map[string]string{teS: teSSrc, teF: teFSrc}, "is governed at the pinned world, by a derived anchor or by an authored invariant"},
		"different directory": {[]string{teS, "module/module_test.go"}, teCovered(), map[string]string{teS: teSSrc, "module/module_test.go": teFSrc}, "no planned file in its directory"},
		"unreadable F":        {[]string{teS, teF}, teCovered(), map[string]string{teS: teSSrc, teF: "\x00unreadable"}, "presence not established"},
		"sibling not planned": {[]string{teF}, teCovered(), map[string]string{teS: teSSrc, teF: teFSrc}, "no planned file in its directory"},
	}
	for name, c := range cases {
		grants, reasons := testEditGrants(context.Background(), teWorld, c.planned, c.covered, authoredEvidence{}, teRead(c.files))
		if len(grants) != 0 {
			t.Errorf("%s: granted anyway: %+v", name, grants)
		}
		if len(reasons) == 0 || !strings.Contains(strings.Join(reasons, " "), c.reason) {
			t.Errorf("%s: reason not named (%q): %v", name, c.reason, reasons)
		}
	}
}

// The frozen falsifiers that refute a candidate, and the one that passes.
func TestAGrantedTestEditIsInspectedAgainstItsExactGrant(t *testing.T) {
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, teRead(map[string]string{teS: teSSrc, teF: teFSrc}))
	edited := "diff --git a/" + teF + " b/" + teF + "\nindex 1..2 100644\n--- a/" + teF + "\n+++ b/" + teF + "\n@@ -9 +9 @@\n-func TestX\n+func TestY\n"
	after := func(src string) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return []byte(src), nil }
	}
	good := strings.Replace(teFSrc, "TestX", "TestY", 1)
	if err := inspectTestEdits(edited, grants, after(good)); err != nil {
		t.Fatalf("an in-place edit inside the grant was refuted: %v", err)
	}
	// An untouched granted file is not a mismatch.
	if err := inspectTestEdits("diff --git a/"+teS+" b/"+teS+"\n", grants, after("")); err != nil {
		t.Fatalf("an untouched grant was inspected: %v", err)
	}
	refutations := map[string]struct {
		diff  string
		after string
		want  string
	}{
		"package change":   {edited, strings.Replace(good, "package modfile", "package modfile_test", 1), "package clause"},
		"build-tag change": {edited, strings.Replace(good, "//go:build go1.20", "//go:build go1.21", 1), "build constraints"},
		"novel import":     {edited, strings.Replace(good, "\"strings\"\n", "\"strings\"\n\t\"bytes\"\n", 1), "novel import"},
		"delete":           {"diff --git a/" + teF + " b/" + teF + "\ndeleted file mode 100644\n", good, "deletes it"},
		"create":           {"diff --git a/" + teF + " b/" + teF + "\nnew file mode 100644\n", good, "creates it"},
		"rename":           {"diff --git a/" + teF + " b/modfile/rule2_test.go\nrename from " + teF + "\nrename to modfile/rule2_test.go\n", good, "renames it"},
	}
	for name, r := range refutations {
		err := inspectTestEdits(r.diff, grants, after(r.after))
		if err == nil || !strings.HasPrefix(err.Error(), "test edit refuted:") || !strings.Contains(err.Error(), r.want) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if !isProspectiveSurfaceRefutation(errors.New("test edit refuted: x")) {
		t.Fatal("a test-edit refutation is not terminal")
	}
}

// Routing: with S covered and F granted, the plan over {S, F} is no longer a
// coverage gap; with F ungranted it still is. F never becomes an anchor.
func TestOperationalAuthorityIsSubtractedFromTheCoverageQuestionNotAddedToIt(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"coverage": {"anchors":0,"files":0,"indexed":0,"sufficient":false},
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	claims := []Claim{{Statement: "s", About: teS, Source: "repository"}}
	action := plannedEdit(teS, teF)
	action.DerivedCoverage = teCovered()
	// The per-file fact a real run establishes: the graph has no facts about the test
	// file. This fixture predates per-file typing and asserted the conflated outcome.
	action.Unexamined = []string{teF}
	cold := routeAuthorityForAction(scoped, claims, action)
	if !cold.ClosesGap() {
		t.Fatalf("a plan over a covered source and an ungranted test did not close a gap: %+v", cold)
	}
	// And it is the TEST-GOVERNANCE gap, not a coverage gap: the production file is
	// covered, so nothing about the graph is missing.
	if cold.Gap.Kind != gapTestGovernanceUnestablished {
		t.Fatalf("the ungranted test read as %q rather than a test-governance gap", cold.Gap.Kind)
	}
	action.OperationalAuthority = []string{teF}
	warm := routeAuthorityForAction(scoped, claims, action)
	if warm.ClosesGap() || warm.RequiresHuman() {
		t.Fatalf("with the test granted, the plan still routed away from architectural authority: %+v", warm)
	}
	if len(action.DerivedCoverage) != 1 || action.DerivedCoverage[0].File != teS {
		t.Fatal("the granted test entered DerivedCoverage; operational authority must never become an anchor")
	}
	if arch := action.architecturalFiles(); len(arch) != 1 || arch[0] != teS {
		t.Fatalf("architectural files: %v", arch)
	}
}

// The grant reaches the worker as its own kind, and survives resume exactly
// or refuses.
func TestATestEditGrantReachesTheWorkerAndSurvivesResume(t *testing.T) {
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, teRead(map[string]string{teS: teSSrc, teF: teFSrc}))
	rendered := renderTestEditGrants(grants)
	for _, want := range []string{"EDIT " + teF, "beside covered subject: " + teS, "package: modfile (may not change)", "//go:build go1.20", "ALLOWED IMPORTS", "testing", "do not create, delete, or rename"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("rendered grant lacks %q:\n%s", want, rendered)
		}
	}
	joined := joinGrants("", rendered)
	if !strings.Contains(joined, "EXISTING-TEST EDIT GRANTS") || !strings.Contains(joined, "not coverage") {
		t.Fatalf("the worker is not told what kind of authority this is:\n%s", joined)
	}
	prompt := implementationPrompt(taskContext{Task: "t"}, "plan", "", 1, nil, joined)
	if !strings.Contains(prompt, rendered) {
		t.Fatal("the grant did not reach the worker's prompt")
	}

	record, _ := json.Marshal(testEditRecord{World: teWorld, Grants: grants})
	found := session.FindInterrupted([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "plan", Payload: json.RawMessage(`{"plan":"p","files":["` + teS + `","` + teF + `"]}`)},
		{TaskID: "t", Kind: event.TestEditGranted, Payload: record},
	})
	if len(found) != 1 || len(found[0].TestEditRecord) == 0 {
		t.Fatalf("the record did not survive the session: %+v", found)
	}
	planned := []string{teS, teF}
	// Resume RE-ESTABLISHES the grant from the pinned world and requires the
	// record to match it exactly; the intact record does.
	e := &Engine{}
	if err := e.restoreTestEditGrants(found[0], grants, planned, teWorld); err != nil || len(e.testEditGrants("t")) != 1 {
		t.Fatalf("an intact record was not re-established: %v", err)
	}
	_ = roles.Reviewer
}

// #101 review, P2: a record is not authority. Each of these records parses,
// names the right world and the right planned file, and would have restored
// under a matcher that only checks fields are present. The pinned world
// disagrees with every one, and the resume refuses.
func TestARecordedTestEditGrantIsReEstablishedFromTheWorldOrRefused(t *testing.T) {
	world := teRead(map[string]string{teS: teSSrc, teF: teFSrc})
	fresh, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, world)
	if len(fresh) != 1 {
		t.Fatal("premise: one fresh grant")
	}
	record := func(g testEditGrant) session.Interrupted {
		raw, _ := json.Marshal(testEditRecord{World: teWorld, Grants: []testEditGrant{g}})
		return session.Interrupted{TaskID: "t", TestEditRecord: raw}
	}
	forgedCovering := fresh[0]
	forgedCovering.Covering = "modfile/other.go"
	forgedHash := fresh[0]
	forgedHash.BaseHash = "0000"
	forgedFacts := fresh[0]
	forgedFacts.Facts = testEditFacts{Package: "modfile", Imports: map[string]bool{"testing": true, "bytes": true}, Constraints: forgedFacts.Facts.Constraints}
	for name, g := range map[string]testEditGrant{"forged covering": forgedCovering, "forged base hash": forgedHash, "forged facts": forgedFacts} {
		e := &Engine{}
		if err := e.restoreTestEditGrants(record(g), fresh, []string{teS, teF}, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
	// S no longer planned, or no longer covered: the world recomputes NO
	// grant, and a record holding one is refused.
	for name, c := range map[string]struct {
		planned []string
		covered []CoverageAnchor
	}{
		"S no longer planned": {[]string{teF}, teCovered()},
		"S no longer covered": {[]string{teS, teF}, nil},
	} {
		recomputed, _ := testEditGrants(context.Background(), teWorld, c.planned, c.covered, authoredEvidence{}, world)
		e := &Engine{}
		if err := e.restoreTestEditGrants(record(fresh[0]), recomputed, c.planned, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
	// And a world that authorises what the run never recorded does not hand
	// the resumed run that authority.
	e := &Engine{}
	if err := e.restoreTestEditGrants(session.Interrupted{TaskID: "t"}, fresh, []string{teS, teF}, teWorld); err != nil || len(e.testEditGrants("t")) != 0 {
		t.Fatalf("an unrecorded grant became authority on resume: %v %d", err, len(e.testEditGrants("t")))
	}
}

// #101 review, P2: a granted path containing whitespace is seen as Git
// wrote it, so an illegal change to that file is caught rather than skipped.
func TestAGrantedTestPathWithWhitespaceIsStillInspected(t *testing.T) {
	const f = "modfile/a b_test.go"
	src := strings.Replace(teFSrc, "TestX", "TestSpace", 1)
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, f}, teCovered(), authoredEvidence{}, teRead(map[string]string{teS: teSSrc, f: src}))
	if len(grants) != 1 || grants[0].Path != f {
		t.Fatalf("premise: a grant for the whitespace path: %+v", grants)
	}
	diff := "diff --git a/" + f + " b/" + f + "\nindex 1..2 100644\n--- a/" + f + "\n+++ b/" + f + "\n@@ -9 +9 @@\n-x\n+y\n"
	touched, _, _, _ := diffFileStates(diff)
	if !touched[f] {
		t.Fatalf("the whitespace path was not seen as touched: %v", touched)
	}
	novel := strings.Replace(src, "\"strings\"\n", "\"strings\"\n\t\"bytes\"\n", 1)
	err := inspectTestEdits(diff, grants, func(string) ([]byte, error) { return []byte(novel), nil })
	if err == nil || !strings.Contains(err.Error(), "novel import") {
		t.Fatalf("an illegal import change on a whitespace path slipped past inspection: %v", err)
	}
	pkg := strings.Replace(src, "package modfile", "package modfile_test", 1)
	if err := inspectTestEdits(diff, grants, func(string) ([]byte, error) { return []byte(pkg), nil }); err == nil || !strings.Contains(err.Error(), "package clause") {
		t.Fatalf("an illegal package change on a whitespace path slipped past inspection: %v", err)
	}
	// A rename of the whitespace path is seen too.
	ren := "diff --git a/" + f + " b/modfile/c d_test.go\nrename from " + f + "\nrename to modfile/c d_test.go\n"
	if err := inspectTestEdits(ren, grants, func(string) ([]byte, error) { return []byte(src), nil }); err == nil || !strings.Contains(err.Error(), "renames it") {
		t.Fatalf("a rename of a whitespace path was not refuted: %v", err)
	}
}

// #101 review 5047003424: re-establishment must not write. The routing path
// records; the resume path only computes and compares. Two resumes of a task
// whose original run recorded no grant must leave it ungranted both times --
// including when the pinned world would grant it today.
func TestARepeatedResumeCannotMintTestEditAuthority(t *testing.T) {
	// The pure computation records nothing: no engine state, no event.
	body := funcBody(t, "internal/workflow/engine.go", "coverageAtWorld")
	for _, forbidden := range []string{"e.emit(", "e.setTestEditGrants(", "e.setProspectiveGrants("} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("coverageAtWorld has a side effect: %s", forbidden)
		}
	}
	resume := funcBody(t, "internal/workflow/engine.go", "Resume")
	if strings.Contains(resume, "e.derivedCoverage(") || !strings.Contains(resume, "e.coverageAtWorld(") {
		t.Fatal("Resume re-establishes through the recording path")
	}
	routing := funcBody(t, "internal/workflow/engine.go", "derivedCoverage")
	if !strings.Contains(routing, "e.setTestEditGrants(") || !strings.Contains(routing, "TestEditGranted") {
		t.Fatal("the routing path no longer records what it acts on")
	}

	// The two-resume scenario at the level of records. The original run
	// recorded nothing. The world would grant today.
	world := teRead(map[string]string{teS: teSSrc, teF: teFSrc})
	fresh, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, world)
	if len(fresh) != 1 {
		t.Fatal("premise: the world grants today")
	}
	original := []event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "plan", Payload: json.RawMessage(`{"plan":"p","files":["` + teS + `","` + teF + `"]}`)},
	}
	first := session.FindInterrupted(original)[0]
	e := &Engine{}
	if err := e.restoreTestEditGrants(first, fresh, []string{teS, teF}, teWorld); err != nil || len(e.testEditGrants("t")) != 0 {
		t.Fatalf("first resume: %v, grants=%d", err, len(e.testEditGrants("t")))
	}
	// The first resume wrote nothing a session could read back: the events
	// the task holds are exactly the original run's. A second interruption
	// and resume therefore sees the same absent record and installs nothing.
	afterFirstResume := append([]event.Event(nil), original...) // nothing appended by the resume path
	second := session.FindInterrupted(afterFirstResume)[0]
	if len(second.TestEditRecord) != 0 {
		t.Fatal("the first resume left a test-edit record behind")
	}
	e2 := &Engine{}
	if err := e2.restoreTestEditGrants(second, fresh, []string{teS, teF}, teWorld); err != nil || len(e2.testEditGrants("t")) != 0 {
		t.Fatalf("second resume minted authority: %v, grants=%d", err, len(e2.testEditGrants("t")))
	}
	// Had the first resume recorded (the defect), the second would have been
	// handed the grant: pin that this is the difference.
	minted, _ := json.Marshal(testEditRecord{World: teWorld, Grants: fresh})
	tainted := session.FindInterrupted(append(afterFirstResume, event.Event{TaskID: "t", Kind: event.TestEditGranted, Payload: minted}))[0]
	e3 := &Engine{}
	if err := e3.restoreTestEditGrants(tainted, fresh, []string{teS, teF}, teWorld); err != nil || len(e3.testEditGrants("t")) != 1 {
		t.Fatal("precondition: a written record would have been honoured, which is exactly why resume must not write one")
	}
}

// ---------------------------------------------------------------------------
// PROJECTION: the same refusal, at the entrance.
//
// A constraint decidable before work begins must be decided before work
// begins. The measured run was refused at candidate time for importing
// "errors" into a test file that did not import it at the pinned base --
// after the implementer had written 3147 insertions across 23 files. Nothing
// about the implementation was needed to reach that verdict.
//
// W3, W4, W5 and W6 below are controls. W4 is the one that matters: a
// projection that admits something the late check would refuse has inverted
// the repair into a hole.
// ---------------------------------------------------------------------------

// teEditGrants is the pinned-world authority the projector resolves against:
// one grant over teF (package modfile, //go:build go1.20, imports strings and
// testing).
func teEditGrants(t *testing.T) []testEditGrant {
	t.Helper()
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, teRead(teFiles()))
	if len(grants) != 1 || grants[0].Path != teF {
		t.Fatalf("premise: one grant over %s, got %+v", teF, grants)
	}
	return grants
}

// teSourceFor is the candidate a declaration DESCRIBES: the file exactly as
// the plan said it would be after the edit. It is what makes W4 a subset
// proof rather than an assertion -- the projected fixture is handed to the
// late checker as the bytes it stands for.
func teSourceFor(d TestEditDeclaration) string {
	var b strings.Builder
	for _, c := range d.BuildConstraints {
		b.WriteString(c + "\n")
	}
	b.WriteString("\npackage " + d.Package + "\n\nimport (\n")
	for _, imp := range d.Imports {
		b.WriteString("\t\"" + imp + "\"\n")
	}
	b.WriteString(")\n\nfunc TestY(t *testing.T) {}\n")
	return b.String()
}

const (
	teDiffEdited  = "diff --git a/" + teF + " b/" + teF + "\nindex 1..2 100644\n--- a/" + teF + "\n+++ b/" + teF + "\n@@ -9 +9 @@\n-func TestX\n+func TestY\n"
	teDiffCreated = "diff --git a/" + teF + " b/" + teF + "\nnew file mode 100644\n"
	teDiffDeleted = "diff --git a/" + teF + " b/" + teF + "\ndeleted file mode 100644\n"
	teDiffRenamed = "diff --git a/" + teF + " b/modfile/rule2_test.go\nrename from " + teF + "\nrename to modfile/rule2_test.go\n"
)

// teInsideTheGrant is the declaration of an edit that stays entirely within
// the pinned world's facts.
func teInsideTheGrant() TestEditDeclaration {
	return TestEditDeclaration{Path: teF, Operation: testEditOperationEdit, Package: "modfile",
		BuildConstraints: []string{"//go:build go1.20"}, Imports: []string{"strings", "testing"}}
}

// teTheMeasuredCase is the declaration the measured run would have carried:
// the same edit, plus the "errors" import the file lacks at the pinned base.
func teTheMeasuredCase() TestEditDeclaration {
	d := teInsideTheGrant()
	d.Imports = append(d.Imports, "errors")
	return d
}

// W1 -- THE MEASURED CASE, PROJECTED. A plan declaring an edit to an existing
// test file, where the witness would require an import that file lacks at the
// pinned base, is refused AT PLAN ADMISSION, naming the file and the novel
// import -- with no implementer asked for anything.
func TestTheMeasuredNovelImportIsRefusedAtPlanAdmission(t *testing.T) {
	err := projectTestEditRefusals([]TestEditDeclaration{teTheMeasuredCase()}, teEditGrants(t))
	if err == nil {
		t.Fatal("the novel import the candidate-time check refuses was admitted at plan admission")
	}
	for _, want := range []string{"test edit refuted:", teF, `"errors"`, "novel import", roleGoRegressionTestEdit} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the projected refusal does not name %q: %v", want, err)
		}
	}
	// NO IMPLEMENTER WORK WAS REQUESTED. The door is routePlan, which runs in
	// the architect stage: it asks for no runner, builds no implementation
	// prompt, and returns the refusal through the plan-admission error path
	// that execute already fails the task on. The one function that requests
	// an implementer is runCandidate, and execute never reaches it once
	// resolveArchitecture has returned an error.
	body := funcBody(t, "internal/workflow/engine.go", "routePlan")
	if !strings.Contains(body, "projectTestEditRefusals") {
		t.Fatal("routePlan no longer projects the decidable test-edit refusals, so the door is gone")
	}
	for _, implementerWork := range []string{"resolveRunner", "implementationPrompt", "runCandidate", "Implementer"} {
		if strings.Contains(body, implementerWork) {
			t.Errorf("routePlan reaches %s: the projected refusal no longer precedes every request for implementer work", implementerWork)
		}
	}
}

// W2 -- SAME REASON, EARLIER DOOR. Two different sentences for one condition
// is the defect this project keeps removing: the projected refusal and the
// candidate-time refusal for the identical situation are the same string,
// because both come from the same constructor.
func TestTheProjectedRefusalIsTheSentenceTheLateCheckWouldHaveProduced(t *testing.T) {
	grants := teEditGrants(t)
	decl := teTheMeasuredCase()
	early := projectTestEditRefusals([]TestEditDeclaration{decl}, grants)
	late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(decl)), nil })
	if early == nil || late == nil {
		t.Fatalf("premise: both doors refuse this (early=%v late=%v)", early, late)
	}
	if early.Error() != late.Error() {
		t.Fatalf("one condition, two sentences:\n  early: %s\n  late:  %s", early, late)
	}
}

// W3 -- LATE CHECK SURVIVES (CONTROL). With no declaration to project from,
// projection is inapplicable and the candidate-time check refuses exactly as
// it does today. Projection is not load-bearing for correctness.
func TestTheCandidateTimeCheckStillRefusesWithoutAnyProjection(t *testing.T) {
	grants := teEditGrants(t)
	if err := projectTestEditRefusals(nil, grants); err != nil {
		t.Fatalf("an undeclared plan was refused early: %v", err)
	}
	novel := strings.Replace(teFSrc, "\"strings\"\n", "\"strings\"\n\t\"errors\"\n", 1)
	late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(novel), nil })
	if late == nil || !strings.Contains(late.Error(), "novel import") {
		t.Fatalf("the candidate-time check stopped refusing a novel import: %v", late)
	}
	// And it is still invoked on the candidate, unchanged, by the one place
	// that inspects a produced candidate.
	if !strings.Contains(funcBody(t, "internal/workflow/engine.go", "runCandidate"), "inspectTestEdits") {
		t.Fatal("the candidate-time inspection is gone from runCandidate: projection became the only enforcement point")
	}
}

// W4 -- NOT MORE PERMISSIVE (CONTROL). Every fixture the projector refuses is
// also refused by the candidate-time checker, on the candidate that fixture's
// declaration describes, with the identical reason. The projected set is a
// SUBSET of the late-refused set.
func TestEveryProjectedRefusalIsAlsoALateRefusal(t *testing.T) {
	grants := teEditGrants(t)
	wrongPackage := teInsideTheGrant()
	wrongPackage.Package = "modfile_test"
	wrongConstraints := teInsideTheGrant()
	wrongConstraints.BuildConstraints = []string{"//go:build go1.21"}
	asCreate, asDelete, asRename := teInsideTheGrant(), teInsideTheGrant(), teInsideTheGrant()
	asCreate.Operation, asDelete.Operation, asRename.Operation = testEditOperationCreate, testEditOperationDelete, testEditOperationRename

	projected := []struct {
		name string
		decl TestEditDeclaration
		diff string
	}{
		{"novel import", teTheMeasuredCase(), teDiffEdited},
		{"package change", wrongPackage, teDiffEdited},
		{"build-constraint change", wrongConstraints, teDiffEdited},
		{"create", asCreate, teDiffCreated},
		{"delete", asDelete, teDiffDeleted},
		{"rename", asRename, teDiffRenamed},
	}
	for _, c := range projected {
		early := projectTestEditRefusals([]TestEditDeclaration{c.decl}, grants)
		if early == nil {
			t.Errorf("%s: not projected", c.name)
			continue
		}
		late := inspectTestEdits(c.diff, grants, func(string) ([]byte, error) { return []byte(teSourceFor(c.decl)), nil })
		if late == nil {
			t.Errorf("%s: PROJECTED BUT NOT LATE-REFUSED -- the projection is no longer a subset: %s", c.name, early)
			continue
		}
		if early.Error() != late.Error() {
			t.Errorf("%s: two sentences for one condition:\n  early: %s\n  late:  %s", c.name, early, late)
		}
	}
}

// W5 -- UNDECIDABLE IS NOT PROJECTED (CONTROL). What an implementer will
// write is not decidable from the plan, the pinned world and the role. A
// declaration that says nothing about imports, and an operation word outside
// the closed vocabulary, are admitted early and governed by the late check.
// Absence of a projection is not permission: the late check still refuses.
func TestAnUndecidableEffectIsAdmittedEarlyAndRefusedLate(t *testing.T) {
	grants := teEditGrants(t)
	contentDependent := TestEditDeclaration{Path: teF, Operation: testEditOperationEdit}
	if err := projectTestEditRefusals([]TestEditDeclaration{contentDependent}, grants); err != nil {
		t.Fatalf("a content-dependent outcome was guessed at plan admission: %v", err)
	}
	unknownOperation := teInsideTheGrant()
	unknownOperation.Operation = "rewrite"
	if err := projectTestEditRefusals([]TestEditDeclaration{unknownOperation}, grants); err != nil {
		t.Fatalf("an operation outside the closed vocabulary was read as a violation: %v", err)
	}
	// The plan proceeded, the implementer wrote the novel import anyway, and
	// the candidate-time check -- which is the authority of record -- refused
	// it.
	novel := strings.Replace(teFSrc, "\"strings\"\n", "\"strings\"\n\t\"errors\"\n", 1)
	late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(novel), nil })
	if late == nil || !strings.Contains(late.Error(), "novel import") {
		t.Fatalf("the undeclared novel import was never refused at all: %v", late)
	}
}

// W6 -- A PLAN THAT IS FINE IS STILL FINE (CONTROL). A declared edit entirely
// within the pinned world's facts is admitted, and so is a declaration for a
// path the world granted no edit: the projection refuses nothing it has no
// grounds to refuse.
func TestAnAdmissibleDeclarationIsNotRefusedAtPlanAdmission(t *testing.T) {
	grants := teEditGrants(t)
	fine := teInsideTheGrant()
	if err := projectTestEditRefusals([]TestEditDeclaration{fine}, grants); err != nil {
		t.Fatalf("an edit inside the grant was refused at plan admission: %v", err)
	}
	// It reaches implementation and the late check agrees.
	if err := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(fine)), nil }); err != nil {
		t.Fatalf("the candidate the admitted declaration describes was refuted late: %v", err)
	}
	ungranted := teInsideTheGrant()
	ungranted.Path = "modfile/other_test.go"
	ungranted.Package = "somethingelse"
	ungranted.Imports = []string{"bytes"}
	if err := projectTestEditRefusals([]TestEditDeclaration{ungranted}, grants); err != nil {
		t.Fatalf("a path under no test-edit grant was refused by this authority: %v", err)
	}
	if err := projectTestEditRefusals([]TestEditDeclaration{fine}, nil); err != nil {
		t.Fatalf("a declaration with no grants at all was refused: %v", err)
	}
}

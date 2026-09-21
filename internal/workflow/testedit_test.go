package workflow

// DF-20 -- the frozen falsifiers of regression-test witness authority, and the
// M2.2 falsifiers they replace.
//
// Every test here answers one question: does authority over a test file come
// from a declared proof obligation over a governed subject, or from where the
// file happens to sit? Each case either leaves the witness ungranted, refutes
// the candidate, or is one of the two positives.

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
	teS      = "modfile/rule.go"
	teF      = "modfile/rule_test.go"
	teOther  = "modfile/other_test.go"
	teAbsent = "modfile/unavailable_test.go"
	teFar    = "acceptance/governed_run_test.go"
	teWorld  = "989c6210000000000000000000000000000000000"
	teSSrc   = "package modfile\n\nimport (\n\t\"fmt\"\n\t\"strings\"\n)\n\ntype File struct{ Module *Module }\ntype Module struct{}\n\nfunc (f *File) AddComment(s string) { _ = fmt.Sprint(strings.TrimSpace(s)) }\n"
	teFSrc   = "//go:build go1.20\n\npackage modfile\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestX(t *testing.T) { _ = strings.ToUpper }\n"
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

func teWorldFiles() map[string]string { return map[string]string{teS: teSSrc, teF: teFSrc} }

// teBind is the one proof obligation every grant below belongs to.
func teBind() planBinding {
	return planBinding{Task: "t", Objective: identityOf("prove the rule"), Plan: identityOf("the plan text")}
}

// teEdit declares the existing test as the witness for the governed subject.
func teEdit() TestWitness {
	return TestWitness{Path: teF, Operation: witnessEdit, Role: roleGoRegressionTestEdit, Subject: teS}
}

// teCreate declares a regression test that does not exist at the pinned base.
func teCreate() TestWitness {
	return TestWitness{Path: teAbsent, Operation: witnessCreate, Role: roleGoRegressionTestCreate,
		Subject: teS, Package: "modfile", Dependencies: []string{"strings"}}
}

func teGrant(t *testing.T, planned []string, declared []TestWitness, covered []CoverageAnchor, files map[string]string) ([]testEditGrant, []string) {
	t.Helper()
	return testWitnessGrants(context.Background(), teBind(), teWorld, planned, declared, nil, covered, authoredEvidence{}, teRead(files))
}

// WITNESS 1 -- EXISTING TEST EDIT. A plan governs a production behaviour and
// names an existing test as its witness. The test file has no anchor of its
// own; the subject supplies the authority and the declaration bounds it.
func TestADeclaredExistingTestIsGrantedAnEditBoundToItsObligation(t *testing.T) {
	grants, reasons := teGrant(t, []string{teS, teF, teOther}, []TestWitness{teEdit()}, teCovered(), teWorldFiles())
	if len(grants) != 1 || len(reasons) != 0 {
		t.Fatalf("grants=%+v reasons=%v", grants, reasons)
	}
	g := grants[0]
	switch {
	case g.Path != teF, g.Covering != teS, g.World != teWorld, g.BaseHash == "":
		t.Fatalf("grant is not bound to the declared path at the pinned world: %+v", g)
	case g.Facts.Package != "modfile", !g.Facts.Imports["testing"], len(g.Facts.Constraints) != 1:
		t.Fatalf("grant carries no base facts to inspect against: %+v", g)
	case g.CoveringEvidence != evidenceDerived, len(g.CoveringIdentity) == 0:
		t.Fatalf("grant cannot name the instrument that governed its subject: %+v", g)
	case !g.Binding.equal(teBind()), !sameWitness(g.Declared, teEdit()):
		t.Fatalf("grant is not bound to this task, objective, plan and declaration: %+v", g)
	}
	// NOTHING ELSE BECAME EDITABLE. modfile/other_test.go is planned, is a test,
	// and sits in the same directory as both the subject and the granted
	// witness. It is undeclared, so it holds nothing.
	if got := operationalFiles(grants); len(got) != 1 || got[0] != teF {
		t.Fatalf("authority reached past the declared path: %v", got)
	}
}

// The declared witness need not sit anywhere near its subject. This is the
// DF-20 blockage itself: an acceptance test in a package holding no production
// file at all could never be granted under the proximity rule, and the plan it
// was required by was admissible.
func TestADeclaredWitnessInAnotherPackageIsGranted(t *testing.T) {
	far := teEdit()
	far.Path = teFar
	files := teWorldFiles()
	files[teFar] = "package acceptance\n\nimport \"testing\"\n\nfunc TestGoverned(t *testing.T) {}\n"
	grants, reasons := teGrant(t, []string{teS, teFar}, []TestWitness{far}, teCovered(), files)
	if len(grants) != 1 || grants[0].Path != teFar || grants[0].Covering != teS {
		t.Fatalf("a witness outside the subject's directory was refused: %+v %v", grants, reasons)
	}
	if grants[0].Facts.Package != "acceptance" {
		t.Fatalf("the grant did not read the witness's own package: %+v", grants[0])
	}
}

// WITNESS 2 -- PLANNED TEST CREATE. Absence at the pinned base is confirmed,
// the path is authorized for that task alone, and no graph identity is minted:
// the grant carries no anchor and no base bytes, because there are none.
func TestADeclaredAbsentTestIsAPlannedCreateAndNotAnAnchor(t *testing.T) {
	grants, reasons := teGrant(t, []string{teS, teAbsent}, []TestWitness{teCreate()}, teCovered(), teWorldFiles())
	if len(grants) != 1 || len(reasons) != 0 {
		t.Fatalf("grants=%+v reasons=%v", grants, reasons)
	}
	g := grants[0]
	switch {
	case g.Path != teAbsent, g.Declared.Operation != witnessCreate:
		t.Fatalf("the absent witness was not classified as a planned create: %+v", g)
	case g.BaseHash != "":
		t.Fatal("a file absent at the pinned base was given base bytes")
	case g.Facts.Package != "modfile", !g.Facts.Imports["strings"], g.Facts.Imports["fmt"]:
		t.Fatalf("the create envelope is not the DECLARED one: %+v", g.Facts)
	case g.Covering != teS, g.CoveringEvidence != evidenceDerived:
		t.Fatalf("the create is not derived from a governed subject: %+v", g)
	}
	// It is operational authority, never coverage. The router is handed the
	// path as an operational grant and the coverage question is unchanged.
	action := plannedEdit(teS, teAbsent)
	action.DerivedCoverage = teCovered()
	action.OperationalAuthority = operationalFiles(grants)
	for _, c := range action.DerivedCoverage {
		if c.File == teAbsent {
			t.Fatal("the planned create entered DerivedCoverage; an absent file has no graph identity")
		}
	}
	if arch := action.architecturalFiles(); len(arch) != 1 || arch[0] != teS {
		t.Fatalf("the planned create is being asked the coverage question: %v", arch)
	}
}

// The frozen falsifiers that leave a declared witness ungranted. Each names the
// clause it failed, so the record says which evidence was missing.
func TestADeclaredWitnessIsNotGrantedWhenThePredicateFails(t *testing.T) {
	badRole := teEdit()
	badRole.Role = roleGoRegressionTestCreate
	badOp := teEdit()
	badOp.Operation = "amend"
	production := teEdit()
	production.Path = "modfile/helper.go"
	noSubject := teEdit()
	noSubject.Subject = ""
	testSubject := teEdit()
	testSubject.Subject = teOther
	unplannedSubject := teEdit()
	unplannedSubject.Subject = "modfile/elsewhere.go"
	createExisting := teCreate()
	createExisting.Path = teF
	editAbsent := teEdit()
	editAbsent.Path = teAbsent
	noPackage := teCreate()
	noPackage.Package = ""
	unreadable := teEdit()
	unreadable.Path = "modfile/broken_test.go"

	cases := map[string]struct {
		planned  []string
		declared []TestWitness
		covered  []CoverageAnchor
		files    map[string]string
		reason   string
	}{
		// NEGATIVE 5: the subject carries no governed evidence, so the test
		// path cannot bootstrap governance for what it claims to prove.
		"ungoverned subject":  {[]string{teS, teF}, []TestWitness{teEdit()}, nil, teWorldFiles(), "no derived anchor and no authored invariant governs"},
		"subject not planned": {[]string{teS, teF}, []TestWitness{unplannedSubject}, teCovered(), teWorldFiles(), "which this plan does not carry"},
		"subject is a test":   {[]string{teS, teF, teOther}, []TestWitness{testSubject}, teCovered(), teWorldFiles(), "not a Go production surface"},
		"no subject at all":   {[]string{teS, teF}, []TestWitness{noSubject}, teCovered(), teWorldFiles(), "cannot bootstrap governance"},
		"subject absent at the world": {[]string{teS, teF}, []TestWitness{teEdit()}, teCovered(),
			map[string]string{teF: teFSrc}, "could not be read at the pinned world"},
		// NEGATIVE 4: test authority never reaches a production path.
		"production path": {[]string{teS, "modfile/helper.go"}, []TestWitness{production}, teCovered(), teWorldFiles(), "never reaches a production path"},
		// The closed vocabularies, read by membership.
		"unknown operation": {[]string{teS, teF}, []TestWitness{badOp}, teCovered(), teWorldFiles(), "not an operation this build knows"},
		"mismatched role":   {[]string{teS, teF}, []TestWitness{badRole}, teCovered(), teWorldFiles(), "admits only " + roleGoRegressionTestEdit},
		// The two operations are not interchangeable.
		"create of a file that exists": {[]string{teS, teF}, []TestWitness{createExisting}, teCovered(), teWorldFiles(), "already exists at the pinned world"},
		"edit of a file that does not": {[]string{teS, teAbsent}, []TestWitness{editAbsent}, teCovered(), teWorldFiles(), "the pinned world's tree lacks it"},
		"create with no package":       {[]string{teS, teAbsent}, []TestWitness{noPackage}, teCovered(), teWorldFiles(), "with no package"},
		// STRICT ABSENCE: an unanswered read is not absence and not presence.
		"unreadable witness": {[]string{teS, "modfile/broken_test.go"}, []TestWitness{unreadable}, teCovered(),
			map[string]string{teS: teSSrc, "modfile/broken_test.go": "\x00unreadable"}, "presence at the pinned world could not be established"},
		"witness not planned": {[]string{teS}, []TestWitness{teEdit()}, teCovered(), teWorldFiles(), "is not one of this plan's files"},
		"declared twice":      {[]string{teS, teF}, []TestWitness{teEdit(), teEdit()}, teCovered(), teWorldFiles(), "declared twice"},
	}
	for name, c := range cases {
		grants, reasons := teGrant(t, c.planned, c.declared, c.covered, c.files)
		if name != "declared twice" && len(grants) != 0 {
			t.Errorf("%s: granted anyway: %+v", name, grants)
		}
		if len(reasons) == 0 || !strings.Contains(strings.Join(reasons, " "), c.reason) {
			t.Errorf("%s: reason not named (%q): %v", name, c.reason, reasons)
		}
	}
}

// A run that cannot say which task, objective and plan a grant would serve
// issues none. Authority nothing bounds is not authority.
func TestAnIncompleteBindingIssuesNoWitnessGrant(t *testing.T) {
	for name, bind := range map[string]planBinding{
		"no task":      {Objective: identityOf("o"), Plan: identityOf("p")},
		"no objective": {Task: "t", Plan: identityOf("p")},
		"no plan":      {Task: "t", Objective: identityOf("o")},
	} {
		grants, reasons := testWitnessGrants(context.Background(), bind, teWorld, []string{teS, teF},
			[]TestWitness{teEdit()}, nil, teCovered(), authoredEvidence{}, teRead(teWorldFiles()))
		if len(grants) != 0 {
			t.Errorf("%s: granted anyway: %+v", name, grants)
		}
		if len(reasons) != 1 || !strings.Contains(reasons[0], "cannot name the task, objective and plan") {
			t.Errorf("%s: reason not named: %v", name, reasons)
		}
	}
}

// A path may hold ONE authority. A file declared both as a prospective CREATE
// surface and as a regression-test witness would be inspected twice against two
// different envelopes, and whichever ran first would decide.
func TestAPathCannotHoldBothProspectiveAndWitnessAuthority(t *testing.T) {
	surfaces := []ProspectiveSurface{{Path: teAbsent, Package: "modfile", Role: roleGoRegressionTest}}
	grants, reasons := testWitnessGrants(context.Background(), teBind(), teWorld, []string{teS, teAbsent},
		[]TestWitness{teCreate()}, surfaces, teCovered(), authoredEvidence{}, teRead(teWorldFiles()))
	if len(grants) != 0 || len(reasons) == 0 || !strings.Contains(reasons[0], "one path takes one authority") {
		t.Fatalf("grants=%+v reasons=%v", grants, reasons)
	}
}

// NEGATIVE 3 -- UNDECLARED TEST. The candidate edits a second test file the
// plan never declared. Being a test file, and sitting beside a granted one,
// authorizes nothing.
func TestAnUndeclaredTestFileIsRefusedAtInspection(t *testing.T) {
	grants, _ := teGrant(t, []string{teS, teF, teOther}, []TestWitness{teEdit()}, teCovered(), teWorldFiles())
	if len(grants) != 1 {
		t.Fatal("premise: the declared witness is granted")
	}
	sibling := "diff --git a/" + teOther + " b/" + teOther + "\nindex 1..2 100644\n--- a/" + teOther + "\n+++ b/" + teOther + "\n@@ -1 +1 @@\n-a\n+b\n"
	err := inspectTestWitnesses(sibling, grants, func(string) ([]byte, error) { return []byte(teFSrc), nil })
	if err == nil || !strings.HasPrefix(err.Error(), "test witness refuted:") || !strings.Contains(err.Error(), teOther) {
		t.Fatalf("an undeclared sibling test was accepted: %v", err)
	}
	// And with no grants at all, a touched test file is still refused: the
	// inspection is not gated on the run holding authority.
	if err := inspectTestWitnesses(sibling, nil, func(string) ([]byte, error) { return []byte(teFSrc), nil }); err == nil {
		t.Fatal("a run holding no witness grant edited a test file unchallenged")
	}
	// A candidate that touches only production files is not this check's
	// business: confinement and the audit own those.
	production := "diff --git a/" + teS + " b/" + teS + "\nindex 1..2 100644\n--- a/" + teS + "\n+++ b/" + teS + "\n@@ -1 +1 @@\n-a\n+b\n"
	if err := inspectTestWitnesses(production, nil, func(string) ([]byte, error) { return nil, errors.New("unused") }); err != nil {
		t.Fatalf("a production-only candidate was refuted by the witness inspection: %v", err)
	}
}

// The frozen falsifiers that refute a granted EDIT, and the one that passes.
func TestAGrantedTestEditIsInspectedAgainstItsExactGrant(t *testing.T) {
	grants, _ := teGrant(t, []string{teS, teF}, []TestWitness{teEdit()}, teCovered(), teWorldFiles())
	edited := "diff --git a/" + teF + " b/" + teF + "\nindex 1..2 100644\n--- a/" + teF + "\n+++ b/" + teF + "\n@@ -9 +9 @@\n-func TestX\n+func TestY\n"
	after := func(src string) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return []byte(src), nil }
	}
	good := strings.Replace(teFSrc, "TestX", "TestY", 1)
	if err := inspectTestWitnesses(edited, grants, after(good)); err != nil {
		t.Fatalf("an in-place edit inside the grant was refuted: %v", err)
	}
	// An untouched granted EDIT is not a mismatch: the grant authorized an
	// edit rather than requiring one.
	if err := inspectTestWitnesses("diff --git a/"+teS+" b/"+teS+"\n", grants, after("")); err != nil {
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
		err := inspectTestWitnesses(r.diff, grants, after(r.after))
		if err == nil || !strings.Contains(err.Error(), r.want) {
			t.Errorf("%s: %v", name, err)
		}
		if !isProspectiveSurfaceRefutation(err) {
			t.Errorf("%s: refutation is not terminal", name)
		}
	}
}

// WITNESS 7 -- CANDIDATE VALIDATION. The prospective authorization is spent on
// the bytes that now exist, and cannot survive as a substitute for them.
func TestAPlannedCreateIsDischargedByTheBytesTheCandidateProduced(t *testing.T) {
	grants, _ := teGrant(t, []string{teS, teAbsent}, []TestWitness{teCreate()}, teCovered(), teWorldFiles())
	if len(grants) != 1 {
		t.Fatal("premise: the planned create is granted")
	}
	created := "diff --git a/" + teAbsent + " b/" + teAbsent + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + teAbsent + "\n@@ -0,0 +1 @@\n+x\n"
	good := "package modfile\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestUnavailable(t *testing.T) { _ = strings.ToUpper }\n"
	after := func(src string) func(string) ([]byte, error) {
		return func(string) ([]byte, error) { return []byte(src), nil }
	}
	if err := inspectTestWitnesses(created, grants, after(good)); err != nil {
		t.Fatalf("a created witness inside its declared envelope was refuted: %v", err)
	}
	refutations := map[string]struct {
		diff  string
		after string
		want  string
	}{
		"not created":      {"diff --git a/" + teS + " b/" + teS + "\n", good, "did not create it"},
		"wrong package":    {created, strings.Replace(good, "package modfile", "package modfile_test", 1), "has package"},
		"import outside":   {created, strings.Replace(good, "\"strings\"\n", "\"strings\"\n\t\"bytes\"\n", 1), "outside the declared dependencies"},
		"modified instead": {"diff --git a/" + teAbsent + " b/" + teAbsent + "\nindex 1..2 100644\n--- a/" + teAbsent + "\n+++ b/" + teAbsent + "\n@@ -1 +1 @@\n-a\n+b\n", good, "modifies an existing file"},
		"deleted":          {"diff --git a/" + teAbsent + " b/" + teAbsent + "\ndeleted file mode 100644\n", good, "deletes or renames it"},
		"unreadable bytes": {created, "package modfile\n\nimport (\n", "could not be read as Go"},
	}
	for name, r := range refutations {
		err := inspectTestWitnesses(r.diff, grants, after(r.after))
		if err == nil || !strings.HasPrefix(err.Error(), "test witness refuted:") || !strings.Contains(err.Error(), r.want) {
			t.Errorf("%s: %v", name, err)
		}
		if !isProspectiveSurfaceRefutation(err) {
			t.Errorf("%s: refutation is not terminal", name)
		}
	}
	// The role's one novel allowance, and nothing more.
	novel := "package modfile\n\nimport \"testing\"\n\nfunc TestUnavailable(t *testing.T) {}\n"
	if err := inspectTestWitnesses(created, grants, after(novel)); err != nil {
		t.Fatalf("the role allowance was not honoured: %v", err)
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
	grants, _ := teGrant(t, []string{teS, teF, teAbsent}, []TestWitness{teEdit(), teCreate()}, teCovered(), teWorldFiles())
	if len(grants) != 2 {
		t.Fatalf("premise: both witness forms are granted: %+v", grants)
	}
	edits := renderTestEditGrants(editGrants(grants))
	for _, want := range []string{"EDIT " + teF, "witness for governed subject: " + teS, "derived", "package: modfile (may not change)", "//go:build go1.20", "ALLOWED IMPORTS", "testing", "do not create, delete, or rename"} {
		if !strings.Contains(edits, want) {
			t.Fatalf("rendered edit grant lacks %q:\n%s", want, edits)
		}
	}
	creates := renderTestWitnessCreates(createGrants(grants))
	for _, want := range []string{"CREATE " + teAbsent, roleGoRegressionTestCreate, "witness for governed subject: " + teS, "has no graph identity", "package: modfile (must be exactly this)", "strings, testing", "not a sibling, not a directory, not a production file"} {
		if !strings.Contains(creates, want) {
			t.Fatalf("rendered create grant lacks %q:\n%s", want, creates)
		}
	}
	joined := joinGrants("", edits, creates)
	if !strings.Contains(joined, "EXISTING-TEST EDIT GRANTS") || !strings.Contains(joined, "not coverage") {
		t.Fatalf("the worker is not told what kind of authority an edit is:\n%s", joined)
	}
	if !strings.Contains(joined, "PLANNED TEST CREATE GRANTS") || !strings.Contains(joined, "gains none by being created") {
		t.Fatalf("the worker is not told what kind of authority a planned create is:\n%s", joined)
	}
	prompt := implementationPrompt(taskContext{Task: "t"}, "plan", "", 1, nil, joined)
	if !strings.Contains(prompt, edits) || !strings.Contains(prompt, creates) {
		t.Fatal("the grants did not reach the worker's prompt")
	}

	// WITNESS 6 -- RESUME DURABILITY. Both forms are recorded, the process
	// restarts, and the exact grants come back.
	record, _ := json.Marshal(testEditRecord{World: teWorld, Grants: grants})
	found := session.FindInterrupted([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "plan", Payload: json.RawMessage(`{"plan":"p","files":["` + teS + `","` + teF + `","` + teAbsent + `"]}`)},
		{TaskID: "t", Kind: event.TestEditGranted, Payload: record},
	})
	if len(found) != 1 || len(found[0].TestEditRecord) == 0 {
		t.Fatalf("the record did not survive the session: %+v", found)
	}
	e := &Engine{}
	err := e.restoreTestWitnessGrants(context.Background(), found[0], teBind(), []string{teS, teF, teAbsent},
		[]TestWitness{teEdit(), teCreate()}, teWorld, teRead(teWorldFiles()))
	if err != nil || len(e.testEditGrants("t")) != 2 {
		t.Fatalf("an intact record was not re-established: %v", err)
	}
	restored := e.testEditGrants("t")
	if !sameWitness(restored[0].Declared, teEdit()) || !sameWitness(restored[1].Declared, teCreate()) {
		t.Fatalf("the resumed grants are not the recorded ones: %+v", restored)
	}
	_ = roles.Reviewer
}

// A record is not authority. Each of these parses, names the right world and
// the right planned file, and would restore under a matcher that only checked
// fields were present. The plan, the binding or the pinned base disagrees with
// every one, and the resume refuses.
func TestARecordedTestEditGrantIsReEstablishedFromTheWorldOrRefused(t *testing.T) {
	planned := []string{teS, teF, teAbsent}
	declared := []TestWitness{teEdit(), teCreate()}
	fresh, _ := teGrant(t, planned, declared, teCovered(), teWorldFiles())
	if len(fresh) != 2 {
		t.Fatal("premise: both grants")
	}
	record := func(gs ...testEditGrant) session.Interrupted {
		raw, _ := json.Marshal(testEditRecord{World: teWorld, Grants: gs})
		return session.Interrupted{TaskID: "t", TestEditRecord: raw}
	}
	mutate := func(f func(*testEditGrant)) testEditGrant {
		g := fresh[0]
		g.CoveringIdentity = append([]string(nil), fresh[0].CoveringIdentity...)
		f(&g)
		return g
	}
	// The pinned base still holds these bytes; only the record lies.
	forged := map[string]testEditGrant{
		"forged subject":   mutate(func(g *testEditGrant) { g.Covering = "modfile/other.go" }),
		"forged base hash": mutate(func(g *testEditGrant) { g.BaseHash = "0000" }),
		"forged facts": mutate(func(g *testEditGrant) {
			g.Facts = testEditFacts{Package: "modfile", Imports: map[string]bool{"testing": true, "bytes": true}, Constraints: g.Facts.Constraints}
		}),
		"forged task":       mutate(func(g *testEditGrant) { g.Binding.Task = "other" }),
		"forged objective":  mutate(func(g *testEditGrant) { g.Binding.Objective = identityOf("another request") }),
		"forged plan":       mutate(func(g *testEditGrant) { g.Binding.Plan = identityOf("another plan") }),
		"forged world":      mutate(func(g *testEditGrant) { g.World = strings.Repeat("b", 40) }),
		"forged operation":  mutate(func(g *testEditGrant) { g.Declared.Operation = witnessCreate }),
		"unnamed evidence":  mutate(func(g *testEditGrant) { g.CoveringIdentity = nil }),
		"create with bytes": mutate(func(g *testEditGrant) { g.Declared = teCreate(); g.Path = teAbsent }),
	}
	// A planned create has no base bytes to re-read, so a widened envelope is
	// caught only by recomputing it from the declaration.
	widened := fresh[1]
	widened.Facts = testEditFacts{Package: "modfile", Imports: map[string]bool{"strings": true, "net/http": true}}
	forged["widened create envelope"] = widened
	for name, g := range forged {
		e := &Engine{}
		err := e.restoreTestWitnessGrants(context.Background(), record(g), teBind(), planned, declared, teWorld, teRead(teWorldFiles()))
		if err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
	// A grant for a path the resumed plan no longer declares, and one the plan
	// no longer carries at all.
	for name, c := range map[string]struct {
		planned  []string
		declared []TestWitness
	}{
		"witness no longer declared": {planned, []TestWitness{teCreate()}},
		"witness no longer planned":  {[]string{teS, teAbsent}, declared},
	} {
		e := &Engine{}
		err := e.restoreTestWitnessGrants(context.Background(), record(fresh[0]), teBind(), c.planned, c.declared, teWorld, teRead(teWorldFiles()))
		if err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
	// The pinned base itself disagrees: the witness's bytes moved, and the
	// planned-create path now exists there.
	for name, files := range map[string]map[string]string{
		"base bytes moved":        {teS: teSSrc, teF: strings.Replace(teFSrc, "TestX", "TestMoved", 1)},
		"planned create is there": {teS: teSSrc, teF: teFSrc, teAbsent: teFSrc},
	} {
		e := &Engine{}
		err := e.restoreTestWitnessGrants(context.Background(), record(fresh...), teBind(), planned, declared, teWorld, teRead(files))
		if err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
	// And a record read at another world authorizes nothing here.
	raw, _ := json.Marshal(testEditRecord{World: strings.Repeat("c", 40), Grants: fresh})
	e := &Engine{}
	if err := e.restoreTestWitnessGrants(context.Background(), session.Interrupted{TaskID: "t", TestEditRecord: raw},
		teBind(), planned, declared, teWorld, teRead(teWorldFiles())); err == nil {
		t.Error("a record from another world was restored")
	}
	// A pinned base that cannot be read re-establishes nothing.
	e2 := &Engine{}
	if err := e2.restoreTestWitnessGrants(context.Background(), record(fresh...), teBind(), planned, declared, teWorld, nil); err == nil {
		t.Error("grants were restored without reading the pinned base")
	}
}

// #101 review 5047003424, carried forward: resume revalidates recorded
// authority and never mints new authority. A task whose original run recorded
// no grant resumes with none, twice, however broadly the world would authorize
// it today -- and DF-20 strengthens this: the resume asks the CURRENT GRAPH
// nothing at all, so a graph rebuilt between runs can neither add a grant nor
// take one away.
func TestARepeatedResumeCannotMintTestEditAuthority(t *testing.T) {
	// The pure computation records nothing: no engine state, no event.
	body := funcBody(t, "internal/workflow/engine.go", "coverageAtWorld")
	for _, forbidden := range []string{"e.emit(", "e.setTestEditGrants(", "e.setProspectiveGrants("} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("coverageAtWorld has a side effect: %s", forbidden)
		}
	}
	// The resume path runs NO current graph authority: neither the recording
	// path nor the pure recomputation. Its only inputs are the record and the
	// pinned base. Both halves of the path are pinned -- Resume, and the one
	// restoration function it delegates to -- so moving the derivation behind
	// the delegation would not satisfy either.
	resume := funcBody(t, "internal/workflow/engine.go", "Resume")
	restore := funcBody(t, "internal/workflow/testedit.go", "resumeWitnessAuthority")
	// funcBody flattens to a token stream: a selector call keeps its "(", a
	// plain identifier does not. "testWitnessGrants(" could never have matched
	// anything, so the derivation it was meant to forbid is named bare.
	for _, forbidden := range []string{"e.derivedCoverage(", "e.coverageAtWorld(", "testWitnessGrants ", "e.setTestEditGrants("} {
		if strings.Contains(resume, forbidden) {
			t.Fatalf("Resume re-derives authority from the current graph: %s", forbidden)
		}
		if strings.Contains(restore, forbidden) {
			t.Fatalf("resumeWitnessAuthority re-derives authority from the current graph: %s", forbidden)
		}
	}
	if !strings.Contains(resume, "e.resumeWitnessAuthority(") || !strings.Contains(resume, "gitShowAt ") {
		t.Fatal("Resume does not restore the recorded witness authority against the pinned base")
	}
	if !strings.Contains(restore, "e.restoreTestWitnessGrants(") {
		t.Fatal("the restoration path does not restore the recorded witness authority")
	}
	// f2: the recorded objective identity is re-established BEFORE the binding
	// is computed. A restarted process holds no objective, so a binding
	// computed first hashes nothing and refuses every recorded grant.
	objective, binding := strings.Index(restore, "e.recordObjectiveIfAbsent("), strings.Index(restore, "e.planBindingFor(")
	if objective < 0 || binding < 0 || objective > binding {
		t.Fatalf("the resumed binding is computed before the recorded objective is restored (%d, %d)", objective, binding)
	}
	routing := funcBody(t, "internal/workflow/engine.go", "derivedCoverage")
	if !strings.Contains(routing, "e.setTestEditGrants(") || !strings.Contains(routing, "TestEditGranted") {
		t.Fatal("the routing path no longer records what it acts on")
	}

	// The two-resume scenario at the level of records. The original run
	// recorded nothing. The world would grant today.
	planned := []string{teS, teF}
	declared := []TestWitness{teEdit()}
	fresh, _ := teGrant(t, planned, declared, teCovered(), teWorldFiles())
	if len(fresh) != 1 {
		t.Fatal("premise: the world grants today")
	}
	original := []event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "plan", Payload: json.RawMessage(`{"plan":"p","files":["` + teS + `","` + teF + `"]}`)},
	}
	first := session.FindInterrupted(original)[0]
	e := &Engine{}
	if err := e.restoreTestWitnessGrants(context.Background(), first, teBind(), planned, declared, teWorld, teRead(teWorldFiles())); err != nil || len(e.testEditGrants("t")) != 0 {
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
	if err := e2.restoreTestWitnessGrants(context.Background(), second, teBind(), planned, declared, teWorld, teRead(teWorldFiles())); err != nil || len(e2.testEditGrants("t")) != 0 {
		t.Fatalf("second resume minted authority: %v, grants=%d", err, len(e2.testEditGrants("t")))
	}
	// Had the first resume recorded (the defect), the second would have been
	// handed the grant: pin that this is the difference.
	minted, _ := json.Marshal(testEditRecord{World: teWorld, Grants: fresh})
	tainted := session.FindInterrupted(append(afterFirstResume, event.Event{TaskID: "t", Kind: event.TestEditGranted, Payload: minted}))[0]
	e3 := &Engine{}
	if err := e3.restoreTestWitnessGrants(context.Background(), tainted, teBind(), planned, declared, teWorld, teRead(teWorldFiles())); err != nil || len(e3.testEditGrants("t")) != 1 {
		t.Fatalf("precondition: a written record would have been honoured, which is exactly why resume must not write one: %v", err)
	}
}

// #101 review, P2: a granted path containing whitespace is seen as Git
// wrote it, so an illegal change to that file is caught rather than skipped.
func TestAGrantedTestPathWithWhitespaceIsStillInspected(t *testing.T) {
	const f = "modfile/a b_test.go"
	src := strings.Replace(teFSrc, "TestX", "TestSpace", 1)
	d := teEdit()
	d.Path = f
	grants, reasons := teGrant(t, []string{teS, f}, []TestWitness{d}, teCovered(), map[string]string{teS: teSSrc, f: src})
	if len(grants) != 1 || grants[0].Path != f {
		t.Fatalf("premise: a grant for the whitespace path: %+v %v", grants, reasons)
	}
	diff := "diff --git a/" + f + " b/" + f + "\nindex 1..2 100644\n--- a/" + f + "\n+++ b/" + f + "\n@@ -9 +9 @@\n-x\n+y\n"
	touched, _, _, _ := diffFileStates(diff)
	if !touched[f] {
		t.Fatalf("the whitespace path was not seen as touched: %v", touched)
	}
	novel := strings.Replace(src, "\"strings\"\n", "\"strings\"\n\t\"bytes\"\n", 1)
	err := inspectTestWitnesses(diff, grants, func(string) ([]byte, error) { return []byte(novel), nil })
	if err == nil || !strings.Contains(err.Error(), "novel import") {
		t.Fatalf("an illegal import change on a whitespace path slipped past inspection: %v", err)
	}
	pkg := strings.Replace(src, "package modfile", "package modfile_test", 1)
	if err := inspectTestWitnesses(diff, grants, func(string) ([]byte, error) { return []byte(pkg), nil }); err == nil || !strings.Contains(err.Error(), "package clause") {
		t.Fatalf("an illegal package change on a whitespace path slipped past inspection: %v", err)
	}
	// A rename of the whitespace path is seen too.
	ren := "diff --git a/" + f + " b/modfile/c d_test.go\nrename from " + f + "\nrename to modfile/c d_test.go\n"
	if err := inspectTestWitnesses(ren, grants, func(string) ([]byte, error) { return []byte(src), nil }); err == nil || !strings.Contains(err.Error(), "renames it") {
		t.Fatalf("a rename of a whitespace path was not refuted: %v", err)
	}
}

// f1 -- THE NORMAL ARCHITECT PATH REACHES BOTH GRANT FORMS.
//
// The channel existed and nothing could enter it. The architect's response
// contract named "prospective_surfaces" and not "test_witnesses", and told the
// architect to declare every created file under the former -- which the witness
// predicate refuses, because a path may hold only one authority. So an ordinary
// architect following its own contract could not express an admissible planned
// test create at all, and the DF-20 blockage stood for every plan an architect
// actually produced; only a supplied or hand-written plan could reach the new
// authority. A channel nothing can enter is not a repair.
//
// This drives the real chain end to end: the contract the architect is given,
// the decoder its response passes through, the scope mapping the decision takes
// into the candidate's bound, and the predicate routing applies to it.
func TestAnArchitectProducedPlanReachesBothWitnessGrantForms(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "", "")
	for _, want := range []string{
		`"test_witnesses"`,
		`"operation":"edit|create"`,
		roleGoRegressionTestEdit,
		roleGoRegressionTestCreate,
		// Where the authority comes from, and that a test cannot supply its own.
		"cannot bootstrap governance",
		// The exclusion, so a created regression test is not forced into both.
		"A path declared in both channels is refused in both",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the architect's response contract does not state %q", want)
		}
	}

	// A response in exactly the shape that contract asks for, read by the
	// engine's own decoder rather than by a hand-built decision value.
	response := `{"decision":"proceed","summary":"s","plan":"the plan text","mode":"modify",` +
		`"files":["` + teS + `","` + teF + `","` + teAbsent + `"],` +
		`"test_witnesses":[` +
		`{"path":"` + teF + `","operation":"edit","role":"` + roleGoRegressionTestEdit + `","subject":"` + teS + `"},` +
		`{"path":"` + teAbsent + `","operation":"create","role":"` + roleGoRegressionTestCreate + `",` +
		`"subject":"` + teS + `","package":"modfile","dependencies":["strings"]}]}`
	var d architectureDecision
	if err := decodeModelJSON(response, &d); err != nil {
		t.Fatalf("a response in the shape the contract asks for does not decode: %v", err)
	}
	if len(d.TestWitnesses) != 2 {
		t.Fatalf("the decoded decision carries no witness declarations: %+v", d.TestWitnesses)
	}

	// Routing reads the declarations off that decision and nothing else.
	grants, reasons := testWitnessGrants(context.Background(), teBind(), teWorld, d.Files,
		d.TestWitnesses, d.ProspectiveSurfaces, teCovered(), authoredEvidence{}, teRead(teWorldFiles()))
	if len(grants) != 2 || len(reasons) != 0 {
		t.Fatalf("an architect-produced plan reached no witness authority: %+v %v", grants, reasons)
	}
	if len(editGrants(grants)) != 1 || len(createGrants(grants)) != 1 {
		t.Fatalf("both forms are not reachable from the normal path: %+v", grants)
	}
	if editGrants(grants)[0].Path != teF || createGrants(grants)[0].Path != teAbsent {
		t.Fatalf("the grants do not answer the declared paths: %+v", grants)
	}

	// And the plan's scope carries the declarations into the bound a candidate
	// is confined by, which is what a resume later validates against.
	var tc taskContext
	applyPlanScope(&tc, d)
	if len(tc.Witnesses) != 2 || !sameWitness(tc.Witnesses[0], d.TestWitnesses[0]) {
		t.Fatalf("the plan's scope dropped or altered the declarations: %+v", tc.Witnesses)
	}

	// The last link, which the calls above stand in for: routing reads the
	// declarations off the decoded decision and carries them to the predicate.
	// routePlan itself needs a served graph to run, so the wiring between the
	// decision and the predicate is pinned rather than executed here.
	//
	// EVERY grant-issuing pass, not merely one. Routing has two -- the derived
	// pass before the per-file probe and the authored pass after it -- and a
	// pin that only asked whether the declarations appear SOMEWHERE in routePlan
	// still matched when one of the two was handed nil instead.
	route := funcBody(t, "internal/workflow/engine.go", "routePlan")
	passes := strings.Count(route, "e.derivedCoverage(") + strings.Count(route, "e.authoredTestEditGrants(")
	if passes < 2 || strings.Count(route, "d.TestWitnesses(") != passes {
		t.Fatalf("%d of routing's %d grant-issuing passes are handed the plan's witness declarations",
			strings.Count(route, "d.TestWitnesses("), passes)
	}
	compute := funcBody(t, "internal/workflow/engine.go", "coverageAtWorld")
	if !strings.Contains(compute, "testWitnessGrants ") || !strings.Contains(compute, "witnesses ") {
		t.Fatal("the coverage computation does not apply the witness predicate to the declarations it was given")
	}
}

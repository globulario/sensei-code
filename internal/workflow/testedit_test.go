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
	// A missing snapshot is malformed even when the world happens to authorize
	// nothing: every accepted plan records its complete (possibly empty) set.
	e := &Engine{}
	if err := e.restoreTestEditGrants(session.Interrupted{TaskID: "t"}, fresh, []string{teS, teF}, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
		t.Fatalf("a missing authority snapshot resumed: %v %d", err, len(e.testEditGrants("t")))
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
// records; the resume path only computes and compares. A missing snapshot is
// refused on every resume, including when the pinned world would grant today.
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
	if !strings.Contains(routing, "e.setTestEditGrants(") {
		t.Fatal("the routing path no longer holds what it acts on")
	}
	if !strings.Contains(funcBody(t, "internal/workflow/engine.go", "recordTestEditGrants"), "TestEditGranted") {
		t.Fatal("the routing path no longer records what it acts on")
	}

	// The two-resume scenario at the level of records. The original run omitted
	// its required snapshot; the world would grant today.
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
	if err := e.restoreTestEditGrants(first, fresh, []string{teS, teF}, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
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
	if err := e2.restoreTestEditGrants(second, fresh, []string{teS, teF}, teWorld); err == nil || len(e2.testEditGrants("t")) != 0 {
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

// An empty set is still the routed authority snapshot for a plan. Omitting it
// would make a later resume unable to distinguish "routing granted none" from
// a partial or forged session history.
func TestAnAcceptedPlanRecordsAnEmptyTestEditSnapshot(t *testing.T) {
	store, err := session.New(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: store, SessionID: "s"}
	e.setCoverageWorld("t", teWorld)
	e.recordTestEditGrants("t")
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != event.TestEditGranted {
		t.Fatalf("empty test-edit authority was not recorded: %+v", events)
	}
	var record testEditRecord
	if err := json.Unmarshal(events[0].Payload, &record); err != nil || record.World != teWorld || len(record.Grants) != 0 {
		t.Fatalf("empty test-edit authority snapshot is malformed: %+v (%v)", record, err)
	}
}

// An inspect plan with no files still has a pinned candidate base. Its empty
// snapshot is re-established repeatedly without creating a later authority
// record, so it cannot be confused with an omitted snapshot.
func TestAnEmptyPlanSnapshotIsBoundAndRepeatedResumesDoNotMint(t *testing.T) {
	coverage := funcBody(t, "internal/workflow/engine.go", "coverageAtWorld")
	if base, empty := strings.Index(coverage, "e.governedBase("), strings.Index(coverage, "len planned"); base < 0 || empty < 0 || base > empty {
		t.Fatal("an empty plan is returned before its candidate base is resolved")
	}
	if !strings.Contains(coverage, "coverageComputation world world true") {
		t.Fatal("an empty plan does not preserve its candidate base in the snapshot")
	}

	store, err := session.New(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: store, SessionID: "s"}
	e.setCoverageWorld("t", teWorld) // teWorld stands for the candidate BaseSHA.
	e.recordTestEditGrants("t")
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Kind != event.TestEditGranted {
		t.Fatalf("empty plan snapshot was not recorded: %+v", events)
	}
	task := session.FindInterrupted([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "inspect"},
		{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "inspect plan"},
		events[0],
	})[0]
	for i := 0; i < 2; i++ {
		if err := e.restoreTestEditGrants(task, nil, nil, teWorld); err != nil {
			t.Fatalf("resume %d did not re-establish the empty snapshot: %v", i+1, err)
		}
		after, err := store.Load()
		if err != nil || len(after) != len(events) {
			t.Fatalf("resume %d minted a test-edit record (%d -> %d events, %v)", i+1, len(events), len(after), err)
		}
	}
}

// A second directory, so one plan can hold a derived-governed and an
// authored-governed neighbour at once.
const (
	teAS     = "other/owner.go"
	teAF     = "other/owner_test.go"
	teASSrc  = "package other\n\nfunc Owner() {}\n"
	teAFSrc  = "package other\n\nimport \"testing\"\n\nfunc TestOwner(t *testing.T) {}\n"
	teAInvID = "sensei_code.other.owner_is_governed"
)

func teMixedWorld() worldReader {
	return teRead(map[string]string{teS: teSSrc, teF: teFSrc, teAS: teASSrc, teAF: teAFSrc})
}

// routedTestEdits is what routing holds after both passes: the derived grants,
// then the authored ones composed onto them.
func routedTestEdits(planned []string, covered []CoverageAnchor, authored authoredEvidence) []testEditGrant {
	derived, _ := testEditGrants(context.Background(), teWorld, planned, covered, authoredEvidence{}, teMixedWorld())
	return withAuthoredTestEditGrants(context.Background(), teWorld, planned, derived, authored, teMixedWorld())
}

func teRecord(grants []testEditGrant) session.Interrupted {
	raw, _ := json.Marshal(testEditRecord{World: teWorld, Grants: grants})
	return session.Interrupted{TaskID: "t", TestEditRecord: raw}
}

// THE DEFECT: routing granted an edit from AUTHORED governance and resume, which
// re-established only the derived half, refused it. Resume composes both, by the
// same function, and the grant routing recorded is re-established exactly.
func TestAnAuthoredOnlyTestEditGrantSurvivesResume(t *testing.T) {
	planned := []string{teS, teF}
	authored := authoredFor(teS, teInvariant)
	routed := routedTestEdits(planned, nil, authored)
	if len(routed) != 1 || routed[0].CoveringEvidence != evidenceAuthored {
		t.Fatalf("premise: routing grants one authored edit: %+v", routed)
	}
	derivedOnly, _ := testEditGrants(context.Background(), teWorld, planned, nil, authoredEvidence{}, teMixedWorld())
	if err := (&Engine{}).restoreTestEditGrants(teRecord(routed), derivedOnly, planned, teWorld); err == nil {
		t.Fatal("premise: re-establishing only the derived half refuses the authored grant")
	}
	e := &Engine{}
	recomputed := withAuthoredTestEditGrants(context.Background(), teWorld, planned, derivedOnly, authored, teMixedWorld())
	if err := e.restoreTestEditGrants(teRecord(routed), recomputed, planned, teWorld); err != nil || len(e.testEditGrants("t")) != 1 {
		t.Fatalf("an authored grant routing recorded was not re-established: %v", err)
	}
	// The resume half is wired: Resume reads authored governance and composes it.
	resume := funcBody(t, "internal/workflow/engine.go", "Resume")
	if !strings.Contains(resume, "e.authoredGovernanceForResume(") || !strings.Contains(resume, "withAuthoredTestEditGrants ") {
		t.Fatal("Resume does not recompute authored governance")
	}
	if !strings.Contains(funcBody(t, "internal/workflow/engine.go", "afterHumanAuthorization"), "withAuthoredTestEditGrants") {
		t.Fatal("a human-authorized route drops the authored governance its per-file probes found")
	}
}

// Mixed: one derived and one authored grant are recorded as ONE snapshot, and the
// old per-pass record -- which held only the authored half -- is refused as
// missing a grant rather than honoured.
func TestAMixedDerivedAndAuthoredGrantSetIsOneSnapshot(t *testing.T) {
	planned := []string{teS, teF, teAS, teAF}
	authored := authoredEvidence{World: teWorld, ByFile: map[string][]string{teAS: {teAInvID}}}
	routed := routedTestEdits(planned, teCovered(), authored)
	if len(routed) != 2 || routed[0].CoveringEvidence != evidenceDerived || routed[1].CoveringEvidence != evidenceAuthored {
		t.Fatalf("premise: one derived and one authored grant: %+v", routed)
	}
	recomputed := routedTestEdits(planned, teCovered(), authored)
	e := &Engine{}
	if err := e.restoreTestEditGrants(teRecord(routed), recomputed, planned, teWorld); err != nil || len(e.testEditGrants("t")) != 2 {
		t.Fatalf("the complete snapshot was not re-established: %v", err)
	}
	for name, partial := range map[string][]testEditGrant{"authored half only": routed[1:], "derived half only": routed[:1]} {
		e := &Engine{}
		if err := e.restoreTestEditGrants(teRecord(partial), recomputed, planned, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: a partial record was re-established (%v)", name, err)
		}
	}
	// Routing records nothing per pass: the one record is written after the plan.
	if strings.Contains(funcBody(t, "internal/workflow/engine.go", "derivedCoverage"), "TestEditGranted") ||
		strings.Contains(funcBody(t, "internal/workflow/engine.go", "routePlan"), "TestEditGranted") {
		t.Fatal("routing still records a partial grant set per pass")
	}
}

// A grant routed for an EARLIER plan is superseded by the next PlanProposed; the
// record a resume sees is the one written after the plan it resumes.
func TestAnEarlierPlansTestEditGrantIsSuperseded(t *testing.T) {
	grants := routedTestEdits([]string{teS, teF}, teCovered(), authoredEvidence{})
	raw, _ := json.Marshal(testEditRecord{World: teWorld, Grants: grants})
	plan := func(files string) event.Event {
		return event.Event{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "plan", Payload: json.RawMessage(`{"plan":"p","files":[` + files + `]}`)}
	}
	found := session.FindInterrupted([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		plan(`"` + teS + `","` + teF + `"`),
		{TaskID: "t", Kind: event.TestEditGranted, Payload: raw},
		plan(`"` + teS + `"`),
	})[0]
	if len(found.TestEditRecord) != 0 {
		t.Fatal("an earlier plan's test-edit grant survived a later plan")
	}
	// Every place a grant set is recorded is immediately after a PlanProposed.
	body := funcBody(t, "internal/workflow/engine.go", "recordTestEditGrants")
	if strings.Contains(body, "PlanProposed") {
		t.Fatal("recordTestEditGrants must record grants, not a plan")
	}
	for _, name := range []string{"execute", "recordRevisedPlan"} {
		b := funcBody(t, "internal/workflow/engine.go", name)
		i := strings.Index(b, "e.recordTestEditGrants(")
		if i < 0 {
			t.Fatalf("%s records no plan-scoped grant set", name)
		}
		if p := strings.LastIndex(b[:i], "event.PlanProposed"); p < 0 {
			t.Fatalf("%s records grants before any plan", name)
		}
	}
	// A resume that re-plans adopts its revision through the one recording transition.
	if !strings.Contains(funcBody(t, "internal/workflow/engine.go", "Resume"), "e.recordRevisedPlan(") {
		t.Fatal("Resume adopts a re-plan without recording it")
	}
}

// Provenance is part of the grant. A record that agrees on path, bytes and facts
// but misstates WHICH instrument governed the neighbour, or what it named, is refused.
func TestATamperedTestEditProvenanceIsRefused(t *testing.T) {
	planned := []string{teS, teF, teAS, teAF}
	authored := authoredEvidence{World: teWorld, ByFile: map[string][]string{teAS: {teAInvID}}}
	fresh := routedTestEdits(planned, teCovered(), authored)
	tamper := func(i int, f func(*testEditGrant)) []testEditGrant {
		out := make([]testEditGrant, len(fresh))
		copy(out, fresh)
		f(&out[i])
		return out
	}
	for name, grants := range map[string][]testEditGrant{
		"derived relabelled authored":  tamper(0, func(g *testEditGrant) { g.CoveringEvidence = evidenceAuthored }),
		"authored relabelled derived":  tamper(1, func(g *testEditGrant) { g.CoveringEvidence = evidenceDerived }),
		"unknown evidence class":       tamper(1, func(g *testEditGrant) { g.CoveringEvidence = "trusted" }),
		"no evidence class":            tamper(1, func(g *testEditGrant) { g.CoveringEvidence = "" }),
		"forged authored identity":     tamper(1, func(g *testEditGrant) { g.CoveringIdentity = []string{"sensei_code.forged"} }),
		"extra authored identity":      tamper(1, func(g *testEditGrant) { g.CoveringIdentity = []string{teAInvID, "sensei_code.forged"} }),
		"forged derived identity":      tamper(0, func(g *testEditGrant) { g.CoveringIdentity = []string{"another-requirement"} }),
		"no identity":                  tamper(1, func(g *testEditGrant) { g.CoveringIdentity = nil }),
		"blank identity":               tamper(1, func(g *testEditGrant) { g.CoveringIdentity = []string{" "} }),
		"grant bound to another world": tamper(1, func(g *testEditGrant) { g.World = "another-world" }),
		"extra grant":                  append(append([]testEditGrant(nil), fresh...), testEditGrant{Path: teAS}),
	} {
		e := &Engine{}
		if err := e.restoreTestEditGrants(teRecord(grants), fresh, planned, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
	// A record the pinned world no longer supports -- the authored invariant is gone
	// -- is stale, and refused.
	stale := routedTestEdits(planned, teCovered(), authoredEvidence{})
	if err := (&Engine{}).restoreTestEditGrants(teRecord(fresh), stale, planned, teWorld); err == nil {
		t.Error("a grant whose authored governance is gone was re-established")
	}
}

// f1 review: the comparison is structural, not textual. A record that spells the path
// differently, binds the snapshot to a differently spelled world, or folds two
// identities or two constraints into one delimiter-joined element is not the grant
// routing wrote, and is refused -- even though each would read equal once normalised
// or joined.
func TestATestEditRecordMustMatchStructurallyNotTextually(t *testing.T) {
	const twoConstraints = "//go:build go1.20\n// +build go1.20\n\npackage modfile\n\nimport \"testing\"\n\nfunc TestX(t *testing.T) {}\n"
	planned := []string{teS, teF, teAS, teAF}
	authored := authoredEvidence{World: teWorld, ByFile: map[string][]string{teAS: {teAInvID, "sensei_code.other.second"}}}
	world := teRead(map[string]string{teS: teSSrc, teF: twoConstraints, teAS: teASSrc, teAF: teAFSrc})
	derived, _ := testEditGrants(context.Background(), teWorld, planned, teCovered(), authoredEvidence{}, world)
	fresh := withAuthoredTestEditGrants(context.Background(), teWorld, planned, derived, authored, world)
	if len(fresh) != 2 || len(fresh[0].Facts.Constraints) != 2 || len(fresh[1].CoveringIdentity) != 2 {
		t.Fatalf("premise: two grants, two constraints, two identities: %+v", fresh)
	}
	e := &Engine{}
	if err := e.restoreTestEditGrants(teRecord(fresh), fresh, planned, teWorld); err != nil || len(e.testEditGrants("t")) != 2 {
		t.Fatalf("premise: the exact record is re-established: %v", err)
	}
	tamper := func(i int, f func(*testEditGrant)) []testEditGrant {
		out := make([]testEditGrant, len(fresh))
		copy(out, fresh)
		f(&out[i])
		return out
	}
	for name, grants := range map[string][]testEditGrant{
		"joined identities":        tamper(1, func(g *testEditGrant) { g.CoveringIdentity = []string{strings.Join(g.CoveringIdentity, "\n")} }),
		"joined constraints":       tamper(0, func(g *testEditGrant) { g.Facts.Constraints = []string{strings.Join(g.Facts.Constraints, "\n")} }),
		"dot-segment path":         tamper(0, func(g *testEditGrant) { g.Path = "modfile/./rule_test.go" }),
		"padded path":              tamper(0, func(g *testEditGrant) { g.Path = " " + teF }),
		"trailing-slash path":      tamper(0, func(g *testEditGrant) { g.Path = teF + "/" }),
		"padded grant world":       tamper(0, func(g *testEditGrant) { g.World = teWorld + " " }),
		"missing identity element": tamper(1, func(g *testEditGrant) { g.CoveringIdentity = g.CoveringIdentity[:1] }),
	} {
		e := &Engine{}
		if err := e.restoreTestEditGrants(teRecord(grants), fresh, planned, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
	for name, w := range map[string]string{"padded record world": " " + teWorld, "blank record world": ""} {
		raw, _ := json.Marshal(testEditRecord{World: w, Grants: fresh})
		e := &Engine{}
		if err := e.restoreTestEditGrants(session.Interrupted{TaskID: "t", TestEditRecord: raw}, fresh, planned, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: resumed (%v)", name, err)
		}
	}
}

// Repeated resumes re-establish exactly the routed authored grant and nothing more:
// resume reads authored governance and records nothing, so a world that would
// grant MORE today never reaches the record.
func TestRepeatedResumesCannotMintAuthoredTestEditAuthority(t *testing.T) {
	for name, file := range map[string]string{
		"authoredGovernanceForResume": "internal/workflow/engine.go",
		"coverageAtWorld":             "internal/workflow/engine.go",
		"restoreTestEditGrants":       "internal/workflow/testedit.go",
		"withAuthoredTestEditGrants":  "internal/workflow/testedit.go",
	} {
		b := funcBody(t, file, name)
		for _, forbidden := range []string{"event.TestEditGranted", "e.recordTestEditGrants("} {
			if strings.Contains(b, forbidden) {
				t.Fatalf("%s records authority: %s", name, forbidden)
			}
		}
	}
	planned := []string{teS, teF, teAS, teAF}
	// Routing recorded only the derived grant: authored governance of other/ did
	// not exist when the plan was routed.
	routed := routedTestEdits(planned, teCovered(), authoredEvidence{})
	events := []event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "task"},
		{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "plan", Payload: json.RawMessage(`{"plan":"p","files":["` + teS + `","` + teF + `","` + teAS + `","` + teAF + `"]}`)},
	}
	raw, _ := json.Marshal(testEditRecord{World: teWorld, Grants: routed})
	events = append(events, event.Event{TaskID: "t", Kind: event.TestEditGranted, Payload: raw})
	// Today the world also authored-governs other/owner.go.
	today := routedTestEdits(planned, teCovered(), authoredEvidence{World: teWorld, ByFile: map[string][]string{teAS: {teAInvID}}})
	if len(today) != 2 {
		t.Fatal("premise: the world grants more today than routing did")
	}
	for i := 0; i < 2; i++ {
		task := session.FindInterrupted(events)[0]
		e := &Engine{}
		if err := e.restoreTestEditGrants(task, today, planned, teWorld); err == nil || len(e.testEditGrants("t")) != 0 {
			t.Fatalf("resume %d accepted a grant routing never recorded (%v)", i+1, err)
		}
		if got := session.FindInterrupted(events)[0].TestEditRecord; string(got) != string(raw) {
			t.Fatalf("resume %d changed the record", i+1)
		}
	}
}

// f1 review (cycle 3): a reviewer-triggered revision that becomes the worker's plan is
// recorded through the same transition as the initial plan. Routing P1/G1, adopting a
// revised P2/G2, interrupting, and reconstructing with FindInterrupted yields P2 and
// only G2 -- and resuming it, twice, re-establishes G2 while writing nothing.
func TestAReviewerTriggeredRevisionIsTheRecordAResumeSees(t *testing.T) {
	store, err := session.New(t.TempDir(), "s")
	if err != nil {
		t.Fatal(err)
	}
	e := &Engine{Store: store, SessionID: "s"}
	tc := &taskContext{Task: "task"}

	// P1 is routed with a derived grant and adopted.
	p1Files := []string{teS, teF}
	g1 := routedTestEdits(p1Files, teCovered(), authoredEvidence{})
	e.setTestEditGrants("t", g1)
	e.setCoverageWorld("t", teWorld)
	p1 := architectureDecision{Decision: "proceed", Summary: "P1", Plan: "p1", Files: p1Files}
	e.recordRevisedPlan("t", p1)
	applyPlanScope(tc, p1)

	// The reviewer escalates; the architect's revision P2 is routed with an authored
	// grant on another file and adopted through the same transition.
	p2Files := []string{teAS, teAF}
	authored := authoredEvidence{World: teWorld, ByFile: map[string][]string{teAS: {teAInvID}}}
	g2 := routedTestEdits(p2Files, nil, authored)
	if len(g1) != 1 || len(g2) != 1 || g2[0].CoveringEvidence != evidenceAuthored || g1[0].Path == g2[0].Path {
		t.Fatalf("premise: one derived grant for P1, a different authored grant for P2: %+v / %+v", g1, g2)
	}
	e.setTestEditGrants("t", g2)
	p2 := architectureDecision{Decision: "proceed", Summary: "P2", Plan: "p2", Files: p2Files}
	e.recordRevisedPlan("t", p2)
	applyPlanScope(tc, p2)
	if strings.Join(tc.Files, ",") != strings.Join(p2Files, ",") {
		t.Fatalf("the revised plan's scope was not applied: %v", tc.Files)
	}

	// Interrupted here. The restart reads the session.
	events, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	found := session.FindInterrupted(append([]event.Event{{TaskID: "t", Kind: event.TaskCreated, Summary: "task"}}, events...))
	if len(found) != 1 || !strings.Contains(found[0].Plan, "p2") || strings.Contains(found[0].Plan, "p1") {
		t.Fatalf("the resume does not see the adopted revision: %+v", found)
	}
	var rec testEditRecord
	if err := json.Unmarshal(found[0].TestEditRecord, &rec); err != nil || len(rec.Grants) != 1 || rec.Grants[0].Path != g2[0].Path {
		t.Fatalf("the resume sees a grant record other than P2's: %s (%v)", found[0].TestEditRecord, err)
	}

	// P1's grant, recomputed, is refused against P2's record; P2's is re-established.
	if err := (&Engine{}).restoreTestEditGrants(found[0], g1, p1Files, teWorld); err == nil {
		t.Fatal("the earlier plan's grant was re-established for the revision")
	}
	for i := 0; i < 2; i++ {
		r := &Engine{Store: store, SessionID: "s"}
		recomputed := routedTestEdits(p2Files, nil, authored)
		if err := r.restoreTestEditGrants(found[0], recomputed, p2Files, teWorld); err != nil || len(r.testEditGrants("t")) != 1 {
			t.Fatalf("resume %d did not re-establish P2's grant: %v", i+1, err)
		}
		after, err := store.Load()
		if err != nil || len(after) != len(events) {
			t.Fatalf("resume %d wrote to the session (%d -> %d events, %v)", i+1, len(events), len(after), err)
		}
	}

	// Every revision runCandidate continues under is recorded, not held in memory:
	// each "plan = revised.Plan" is immediately preceded by the recording transition
	// and the revision's whole scope.
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	const adopted, recorded = "plan revised.Plan( ", "e.recordRevisedPlan( e recordRevisedPlan taskID revised applyPlanScope tc revised "
	parts := strings.Split(body, adopted)
	if len(parts)-1 != 3 {
		t.Fatalf("premise: runCandidate continues under a revision at three places, found %d", len(parts)-1)
	}
	for _, before := range parts[:len(parts)-1] {
		if !strings.HasSuffix(before, recorded) {
			t.Fatal("a revision is adopted in memory before, or without, being recorded")
		}
	}
	adopt := funcBody(t, "internal/workflow/engine.go", "recordRevisedPlan")
	if p, g := strings.Index(adopt, "event.PlanProposed"), strings.Index(adopt, "e.recordTestEditGrants("); p < 0 || g < p {
		t.Fatal("recordRevisedPlan does not record the plan before its grant snapshot")
	}
}

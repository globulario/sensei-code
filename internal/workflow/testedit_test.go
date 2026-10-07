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
	if err := teInspect(t, edited, grants, after(good)); err != nil {
		t.Fatalf("an in-place edit inside the grant was refuted: %v", err)
	}
	// An untouched granted file is not a mismatch.
	if err := teInspect(t, "diff --git a/"+teS+" b/"+teS+"\n", grants, after("")); err != nil {
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
		err := teInspect(t, r.diff, grants, after(r.after))
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
	// The derivation owns a present source only: the pinned world confirms the
	// production file exists. Only S is marked; the granted test is never an anchor.
	action.Present = []string{teS}
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
	err := teInspect(t, diff, grants, func(string) ([]byte, error) { return []byte(novel), nil })
	if err == nil || !strings.Contains(err.Error(), "novel import") {
		t.Fatalf("an illegal import change on a whitespace path slipped past inspection: %v", err)
	}
	pkg := strings.Replace(src, "package modfile", "package modfile_test", 1)
	if err := teInspect(t, diff, grants, func(string) ([]byte, error) { return []byte(pkg), nil }); err == nil || !strings.Contains(err.Error(), "package clause") {
		t.Fatalf("an illegal package change on a whitespace path slipped past inspection: %v", err)
	}
	// A rename of the whitespace path is seen too.
	ren := "diff --git a/" + f + " b/modfile/c d_test.go\nrename from " + f + "\nrename to modfile/c d_test.go\n"
	if err := teInspect(t, ren, grants, func(string) ([]byte, error) { return []byte(src), nil }); err == nil || !strings.Contains(err.Error(), "renames it") {
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
	if !strings.Contains(routing, "e.setTestEditGrants(") || !strings.Contains(routing, "e.recordTestEditGrants(") {
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

// DF-24a -- the PREDICATE boundary of the restoration containment repair.
//
// The measured defect (2026-09-23): test-edit authority is composed at routing
// from TWO instruments, DERIVED (recomputed from a derivation at the pinned
// base) and AUTHORED (read per planned file from the live graph and merged
// additively into the same record). Resume recomputed through the DERIVED
// instrument only and demanded exact equality with the WHOLE record, so a task
// holding AUTHORED grants refused with "the pinned world authorises 0
// existing-test edit(s) and the record holds N" -- a statement about ONE
// INSTRUMENT phrased as a statement about the world -- into a terminal that
// removed the task from resume tooling for ever.
//
// W1, W4, W6, W7 and W8 live here because restoreTestEditGrants is where the
// cross-instrument comparison was made. Their lifecycle halves -- that the
// refusal preserves the task, writes nothing and reads back from a restart --
// are in resume_composition_test.go, at the boundary where the destruction
// happened. W1, W4 and W8 are CONTROLS; W7 asserts the ABSENCE of the false
// DERIVED-mismatch diagnosis, not merely the presence of a better one.

// A second directory, governed by an AUTHORED invariant rather than a derived
// anchor -- the shape internal/workflow/authority.go had in the measured task.
const (
	rrS    = "guard/gate.go"
	rrF    = "guard/gate_test.go"
	rrSSrc = "package guard\n\nimport \"strings\"\n\nfunc Open(s string) bool { return strings.TrimSpace(s) != \"\" }\n"
	rrFSrc = "package guard\n\nimport (\n\t\"strings\"\n\t\"testing\"\n)\n\nfunc TestOpen(t *testing.T) { _ = strings.ToUpper }\n"
	// The invariant identity the AUTHORED instrument named at routing time.
	rrInvariant = "invariant:sensei_code.guard.gate_is_closed_by_default"
)

// rrFiles is the world both directories live in.
func rrFiles() map[string]string {
	return map[string]string{teS: teSSrc, teF: teFSrc, rrS: rrSSrc, rrF: rrFSrc}
}

func rrAuthored() authoredEvidence {
	return authoredEvidence{World: teWorld, ByFile: map[string][]string{rrS: {rrInvariant}}}
}

// derivedGrant is what routing recorded through the DERIVED instrument.
func derivedGrant(t *testing.T) testEditGrant {
	t.Helper()
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, teRead(rrFiles()))
	if len(grants) != 1 || grants[0].CoveringEvidence != evidenceDerived || len(grants[0].CoveringIdentity) == 0 {
		t.Fatalf("premise: one DERIVED grant naming its identity: %+v", grants)
	}
	return grants[0]
}

// authoredGrant is what routing recorded through the AUTHORED instrument: the
// merge at engine.go that reads "existing-test edit authority recorded from
// AUTHORED production governance".
func authoredGrant(t *testing.T) testEditGrant {
	t.Helper()
	grants, _ := testEditGrants(context.Background(), teWorld, []string{rrS, rrF}, nil, rrAuthored(), teRead(rrFiles()))
	if len(grants) != 1 || grants[0].CoveringEvidence != evidenceAuthored || len(grants[0].CoveringIdentity) == 0 {
		t.Fatalf("premise: one AUTHORED grant naming its identity: %+v", grants)
	}
	return grants[0]
}

// rrPayload is the durable TestEditGranted payload for these grants, byte for
// byte as routing would have written it.
func rrPayload(t *testing.T, grants ...testEditGrant) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(testEditRecord{World: teWorld, Grants: grants})
	if err != nil {
		t.Fatal(err)
	}
	return json.RawMessage(raw)
}

// rrRecord is a task as a restart finds it, carrying exactly these grants.
func rrRecord(t *testing.T, taskID string, grants ...testEditGrant) session.Interrupted {
	t.Helper()
	return session.Interrupted{TaskID: taskID, TestEditRecord: rrPayload(t, grants...)}
}

// rrDerivedRecomputation is what THIS resume's one instrument produces at the
// pinned base: coverageAtWorld is handed no authored evidence, so every grant
// it can produce is derived.
func rrDerivedRecomputation(t *testing.T, planned []string, covered []CoverageAnchor) []testEditGrant {
	t.Helper()
	grants, _ := testEditGrants(context.Background(), teWorld, planned, covered, authoredEvidence{}, teRead(rrFiles()))
	return grants
}

// refusalOf reads the typed refusal out of an error, failing if the error is
// not one: an untyped error is classified as task failure, which is the
// destructive terminal this repair exists to remove.
func refusalOf(t *testing.T, err error) *RestorationRefusal {
	t.Helper()
	if err == nil {
		t.Fatal("the restoration did not refuse")
	}
	r, ok := err.(*RestorationRefusal)
	if !ok || r == nil {
		t.Fatalf("the refusal is not typed (%T: %v); an untyped error is classified as task failure", err, err)
	}
	return r
}

// theFalseDiagnosis is the sentence the destroyed tasks were refused with. Its
// ABSENCE is the assertion; a stable, inspectable false diagnosis is not an
// acceptable intermediate state.
const theFalseDiagnosis = "the pinned world authorises"

func assertNotTheFalseDiagnosis(t *testing.T, r *RestorationRefusal, err error) {
	t.Helper()
	if r.Binding == RestorationDerivedMismatch {
		t.Fatalf("a record whose unresolved condition is AUTHORED authority was reported as a DERIVED mismatch: %+v", r)
	}
	if r.Instrument == RestorationInstrumentDerived {
		t.Fatalf("the refusal blames the DERIVED instrument for authority it did not produce: %+v", r)
	}
	for _, text := range []string{err.Error(), r.Describe()} {
		if strings.Contains(text, theFalseDiagnosis) {
			t.Fatalf("the false diagnosis survives: %q", text)
		}
	}
}

// W1. DERIVED-ONLY CONTROL. A record that is entirely DERIVED still recomputes
// from the pinned base and still requires EXACT equality. This repair must not
// weaken existing deterministic restoration.
func TestW1ADerivedOnlyRecordStillRequiresExactRecomputation(t *testing.T) {
	g := derivedGrant(t)
	planned := []string{teS, teF}
	fresh := rrDerivedRecomputation(t, planned, teCovered())

	e := &Engine{}
	if err := e.restoreTestEditGrants(rrRecord(t, "t", g), fresh, planned, teWorld); err != nil {
		t.Fatalf("an intact DERIVED record was not re-established: %v", err)
	}
	if got := e.testEditGrants("t"); len(got) != 1 || got[0].Path != teF {
		t.Fatalf("the re-established authority is not the recorded one: %+v", got)
	}
	// And every exactness check still bites. Each of these differs from the
	// recomputation in ONE fact, and each must refuse.
	for name, forge := range map[string]func(testEditGrant) testEditGrant{
		"covering":  func(g testEditGrant) testEditGrant { g.Covering = "guard/other.go"; return g },
		"base hash": func(g testEditGrant) testEditGrant { g.BaseHash = "0000"; return g },
		// THE IDENTITY IS PART OF THE EQUALITY. Everything else about this
		// record agrees with the pinned base -- same path, same covering
		// subject, same bytes, same facts -- and it cites another derivation
		// requirement. A stale or edited record is what sensei-code#101 names,
		// and without this check it would be installed as re-established
		// authority on the strength of the files being unchanged.
		"covering identity": func(g testEditGrant) testEditGrant {
			g.CoveringIdentity = []string{"requirement:some.other.question"}
			return g
		},
		"facts": func(g testEditGrant) testEditGrant {
			g.Facts = testEditFacts{Package: g.Facts.Package, Imports: map[string]bool{"testing": true, "bytes": true}, Constraints: g.Facts.Constraints}
			return g
		},
	} {
		e := &Engine{}
		err := e.restoreTestEditGrants(rrRecord(t, "t", forge(g)), fresh, planned, teWorld)
		r := refusalOf(t, err)
		if r.Binding != RestorationDerivedMismatch || r.Instrument != RestorationInstrumentDerived {
			t.Errorf("%s: a DERIVED disagreement was not reported as one: %+v", name, r)
		}
		if len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: authority was installed over a disagreement", name)
		}
	}
}

// W1, the IDENTITY RULE. Comparing identity vectors needs a stated rule for
// what "the same identity" is, because a rule nobody wrote down is one the next
// reader changes by accident.
//
// The rule (canonicalIdentity): trim, drop blanks, drop duplicates, sort. Order
// is an artifact of how the instrument walked its evidence, not something either
// instrument asserts, and a blank names nothing -- which is already what
// namesAnIdentity reads at the grant predicate, so reading it as a difference
// here would make the same vector evidence at one predicate and a mismatch at
// the other. The negatives are the half that matters: a canonicalisation that
// also swallowed a DIFFERENT id would make the whole identity check decorative.
func TestW1TheDerivedIdentityRuleCanonicalisesShapeButNeverDifference(t *testing.T) {
	for name, tc := range map[string]struct {
		a, b []string
		same bool
	}{
		"identical":                {[]string{"req:a"}, []string{"req:a"}, true},
		"reordered":                {[]string{"req:b", "req:a"}, []string{"req:a", "req:b"}, true},
		"blank padded":             {[]string{"req:a", "", "   "}, []string{"req:a"}, true},
		"whitespace around the id": {[]string{"  req:a  "}, []string{"req:a"}, true},
		"duplicated":               {[]string{"req:a", "req:a"}, []string{"req:a"}, true},
		"reordered with blanks":    {[]string{"", "req:b", " ", "req:a"}, []string{"req:a", "req:b"}, true},

		"a different id":            {[]string{"req:a"}, []string{"req:b"}, false},
		"an extra id":               {[]string{"req:a", "req:b"}, []string{"req:a"}, false},
		"a missing id":              {[]string{"req:a"}, []string{"req:a", "req:b"}, false},
		"only blanks against an id": {[]string{"", "  "}, []string{"req:a"}, false},
		"a prefix, not the id":      {[]string{"req:a"}, []string{"req:ab"}, false},
	} {
		if got := sameIdentity(tc.a, tc.b); got != tc.same {
			t.Errorf("%s: sameIdentity(%v, %v) = %v, want %v", name, tc.a, tc.b, got, tc.same)
		}
		// Symmetric, or the verdict would depend on which side the caller
		// happened to pass first.
		if got := sameIdentity(tc.b, tc.a); got != tc.same {
			t.Errorf("%s: the rule is not symmetric: sameIdentity(%v, %v) = %v, want %v", name, tc.b, tc.a, got, tc.same)
		}
	}
}

// W1, the identity rule AT THE RESTORE BOUNDARY. The rule above is only worth
// stating if the exact comparison applies it, so the same cases are driven
// through restoreTestEditGrants: a record naming the same evidence in another
// shape is still re-established, and one naming OTHER evidence refuses as the
// DERIVED mismatch it is, with nothing installed.
func TestW1IdentityIsJudgedByTheRuleAtTheRestoreBoundary(t *testing.T) {
	planned := []string{teS, teF}
	fresh := rrDerivedRecomputation(t, planned, teCovered())
	if len(fresh) != 1 || len(fresh[0].CoveringIdentity) != 1 {
		t.Fatalf("premise: the DERIVED instrument names exactly one requirement per grant: %+v", fresh)
	}
	req := fresh[0].CoveringIdentity[0]

	// SAME EVIDENCE, OTHER SHAPE -- what a legacy or hand-edited record
	// plausibly holds. These must restore: refusing them would be the
	// destructive direction this whole repair closes.
	for name, ids := range map[string][]string{
		"as recomputed": {req},
		"blank padded":  {req, ""},
		"whitespace":    {"  " + req + "  "},
		"duplicated":    {req, req},
	} {
		g := derivedGrant(t)
		g.CoveringIdentity = ids
		e := &Engine{}
		if err := e.restoreTestEditGrants(rrRecord(t, "t", g), fresh, planned, teWorld); err != nil {
			t.Errorf("%s: a record naming the same derivation identity was refused: %v", name, err)
			continue
		}
		if len(e.testEditGrants("t")) != 1 {
			t.Errorf("%s: the authority was not re-established", name)
		}
	}

	// OTHER EVIDENCE. Every other fact still agrees with the pinned base; only
	// the question that established the authority differs.
	for name, ids := range map[string][]string{
		"another requirement": {"requirement:some.other.question"},
		"the id plus another": {req, "requirement:some.other.question"},
		"a longer name":       {req + ".extended"},
	} {
		g := derivedGrant(t)
		g.CoveringIdentity = ids
		e := &Engine{}
		r := refusalOf(t, e.restoreTestEditGrants(rrRecord(t, "t", g), fresh, planned, teWorld))
		if r.Binding != RestorationDerivedMismatch || r.Instrument != RestorationInstrumentDerived {
			t.Errorf("%s: an identity disagreement INSIDE the DERIVED instrument was not named as one: %+v", name, r)
		}
		if !strings.Contains(r.Detail, teF) || !strings.Contains(r.Detail, req) {
			t.Errorf("%s: the refusal does not show which identities disagreed: %q", name, r.Detail)
		}
		if strings.Contains(r.Describe(), theFalseDiagnosis) {
			t.Errorf("%s: the false diagnosis survives: %q", name, r.Describe())
		}
		if len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: authority was installed over an identity disagreement", name)
		}
	}

	// REORDERING, where it can actually be observed. Said plainly: the DERIVED
	// instrument emits ONE requirement per grant today, so a reordering cannot
	// arise between a real recomputation and a real record, and BOTH sides here
	// are forged to reach the rule at this boundary. It is a rule control, not a
	// replay of a shape routing currently produces.
	two := []string{req, "requirement:a.second.question"}
	freshTwo := append([]testEditGrant{}, fresh...)
	freshTwo[0].CoveringIdentity = two
	reordered := derivedGrant(t)
	reordered.CoveringIdentity = []string{two[1], two[0]}
	e := &Engine{}
	if err := e.restoreTestEditGrants(rrRecord(t, "t", reordered), freshTwo, planned, teWorld); err != nil {
		t.Errorf("a reordered identity vector was read as a disagreement: %v", err)
	} else if len(e.testEditGrants("t")) != 1 {
		t.Error("the authority was not re-established under a reordered identity vector")
	}

	// THE SUBSET DIRECTION, which only a multi-id recomputation can reach: with
	// one id per recomputed grant, a record that is a strict subset of it is the
	// empty set, and an identity that names nothing is refused as an ambiguous
	// binding long before this comparison. So it is pinned here. A record naming
	// FEWER requirements than the pinned base does is not the same authority --
	// "every id the record names is present" is containment, not equality, and a
	// rule that confused the two would re-establish authority over a question
	// the record never answered.
	subset := derivedGrant(t)
	subset.CoveringIdentity = []string{two[0]}
	eSub := &Engine{}
	rSub := refusalOf(t, eSub.restoreTestEditGrants(rrRecord(t, "t", subset), freshTwo, planned, teWorld))
	if rSub.Binding != RestorationDerivedMismatch || rSub.Instrument != RestorationInstrumentDerived {
		t.Errorf("a record naming a subset of the recomputed identity was not a DERIVED mismatch: %+v", rSub)
	}
	if len(eSub.testEditGrants("t")) != 0 {
		t.Error("authority was installed over a record naming fewer requirements than the pinned base")
	}
}

// W4, the PREDICATE half. A DERIVED recomputation that genuinely differs from
// the recorded DERIVED portion is named as the DERIVED binding it is, cites the
// two counts the DERIVED instrument measured on both sides, and installs
// nothing. The lifecycle half -- that this refusal still preserves the task --
// is TestW4ADerivedMismatchRefusesExecutionAndPreservesTheTask.
func TestW4ADerivedMismatchNamesTheDerivedBindingAndInstallsNothing(t *testing.T) {
	planned := []string{teS, teF}
	fresh := rrDerivedRecomputation(t, planned, teCovered())
	if len(fresh) != 1 {
		t.Fatalf("premise: the DERIVED instrument authorises exactly one edit: %+v", fresh)
	}
	// A DISAGREEMENT INSIDE THE INSTRUMENT, not across two of them: the record
	// is DERIVED, names its derivation requirement, and disagrees about the
	// bytes it was bound to.
	forged := derivedGrant(t)
	forged.BaseHash = "not the bytes at the pinned base"

	e := &Engine{}
	err := e.restoreTestEditGrants(rrRecord(t, "t", forged), fresh, planned, teWorld)
	r := refusalOf(t, err)
	if r.Binding != RestorationDerivedMismatch || r.Instrument != RestorationInstrumentDerived {
		t.Fatalf("a DERIVED disagreement was not named as one: %+v", r)
	}
	if !strings.Contains(r.Detail, teF) || !strings.Contains(r.Detail, "base hash") {
		t.Errorf("the refusal does not name what disagreed: %q", r.Detail)
	}
	// 1 against 1. The number on each side was produced by the same means, so
	// the comparison is about one question.
	if r.Measured == nil || r.Measured.RecordedDerived != 1 || r.Measured.RecomputedDerived != 1 || r.Measured.RecordedAuthored != 0 {
		t.Fatalf("the DERIVED comparison did not state both sides as the same instrument's: %+v", r.Measured)
	}
	if len(e.testEditGrants("t")) != 0 {
		t.Fatal("authority was installed over a DERIVED disagreement")
	}
	// The DERIVED absence is reported as the DERIVED instrument's absence, and
	// still never as a statement about "the world".
	e2 := &Engine{}
	r2 := refusalOf(t, e2.restoreTestEditGrants(rrRecord(t, "t", derivedGrant(t)), nil, planned, teWorld))
	if r2.Binding != RestorationDerivedMismatch || strings.Contains(r2.Describe(), theFalseDiagnosis) {
		t.Fatalf("an empty DERIVED recomputation was not described as the DERIVED instrument's: %+v", r2)
	}
}

// W6, the PREDICATE half. For a record holding BOTH instruments the DERIVED
// portion is checked against the recorded DERIVED authority ALONE, AUTHORED is
// not judged by DERIVED recomputation, and the refusal is the true AUTHORED
// blocker. This is NOT a successful mixed restoration. The lifecycle half is
// TestW6AMixedRecordRefusesOnTheAuthoredBlockerAndPreservesTheTask.
func TestW6AMixedRecordChecksDerivedOnlyAgainstRecordedDerived(t *testing.T) {
	planned := []string{teS, teF, rrS, rrF}
	mixed := rrRecord(t, "t", derivedGrant(t), authoredGrant(t))
	// The DERIVED instrument authorises exactly the DERIVED half.
	fresh := rrDerivedRecomputation(t, planned, teCovered())
	if len(fresh) != 1 || fresh[0].Path != teF {
		t.Fatalf("premise: the DERIVED instrument authorises the derived half only: %+v", fresh)
	}

	e := &Engine{}
	err := e.restoreTestEditGrants(mixed, fresh, planned, teWorld)
	r := refusalOf(t, err)
	assertNotTheFalseDiagnosis(t, r, err)
	if r.Binding != RestorationAuthoredUnverifiable {
		t.Fatalf("the mixed record refused on something other than its AUTHORED blocker: %+v", r)
	}
	// The DERIVED portion was compared with the recorded DERIVED portion and
	// agreed -- 1 against 1. Had it been compared with the WHOLE record it
	// would have read 1 against 2 and refused as a DERIVED mismatch, which is
	// the defect.
	if r.Measured == nil || r.Measured.RecordedDerived != 1 || r.Measured.RecomputedDerived != 1 || r.Measured.RecordedAuthored != 1 {
		t.Fatalf("the two instruments were not counted separately: %+v", r.Measured)
	}
	if !strings.Contains(r.Detail, rrF) || strings.Contains(r.Detail, teF) {
		t.Errorf("the refusal does not name the AUTHORED grant alone: %q", r.Detail)
	}
	if len(e.testEditGrants("t")) != 0 {
		t.Fatal("a mixed record installed authority")
	}

	// AND THE TWO DIAGNOSES DO NOT COLLAPSE -- but which one a MIXED record
	// reports is settled by precedence, not by which check happens to run first.
	// A mixed record whose DERIVED half ALSO genuinely disagrees still refuses on
	// its AUTHORED blocker: repairing the DERIVED half would leave the record
	// exactly as unresumable, so naming the DERIVED mismatch would name a
	// condition whose resolution changes nothing.
	broken := derivedGrant(t)
	broken.BaseHash = "0000"
	e2 := &Engine{}
	r2 := refusalOf(t, e2.restoreTestEditGrants(rrRecord(t, "t", broken, authoredGrant(t)), fresh, planned, teWorld))
	if r2.Binding != RestorationAuthoredUnverifiable || r2.Instrument != RestorationInstrumentAuthored {
		t.Fatalf("a mixed record refused on its conditional DERIVED half rather than its unconditional AUTHORED blocker: %+v", r2)
	}
	if len(e2.testEditGrants("t")) != 0 {
		t.Fatal("a mixed record with a broken DERIVED half installed authority")
	}
	// The DERIVED diagnosis is still reachable and still exact: the SAME broken
	// grant, with no AUTHORED grant beside it, is named as the DERIVED mismatch
	// it is. That is where the two diagnoses are held apart -- by the presence of
	// the unconditional blocker, not by the order of the checks.
	e3 := &Engine{}
	r3 := refusalOf(t, e3.restoreTestEditGrants(rrRecord(t, "t", broken), fresh, planned, teWorld))
	if r3.Binding != RestorationDerivedMismatch || r3.Instrument != RestorationInstrumentDerived {
		t.Fatalf("a DERIVED disagreement with no AUTHORED grant beside it was not reported as one: %+v", r3)
	}
	if len(e3.testEditGrants("t")) != 0 {
		t.Fatal("authority was installed over a DERIVED disagreement")
	}
}

// W1. PRECEDENCE. When two restoration conditions hold at once, the refusal
// names the one that NO amount of agreement in the other can resolve.
//
// The measured shape: the record holds an AUTHORED grant for a path the DERIVED
// instrument ALSO authorises at the pinned base. derivedDisagreement then sees
// one recomputed DERIVED grant against zero recorded DERIVED grants and reports
// a mismatch -- so under the prior ordering the refusal was typed
// instrument=derived, binding=derived_mismatch while an unverifiable AUTHORED
// grant sat unmentioned underneath it.
//
// Those two conditions are not the same kind of thing. A DERIVED disagreement is
// CONDITIONAL: a record that agreed about paths, identity, bytes and facts would
// resolve it. An unverifiable AUTHORED grant is UNCONDITIONAL: no amount of
// DERIVED agreement verifies it, because the DERIVED instrument cannot adjudicate
// AUTHORED authority at all. Reporting the conditional one mislabels the blocker
// and points the operator at a record edit that could never have worked.
//
// The assertion is an ABSENCE -- not merely that a better answer is present.
func TestW1AnUnverifiableAuthoredGrantOutranksADerivedDisagreement(t *testing.T) {
	planned := []string{rrS, rrF}
	// ONE PATH, TWO INSTRUMENTS. A derived anchor over the same production
	// neighbour the AUTHORED invariant governs, so the DERIVED recomputation at
	// the pinned base authorises the very path the record holds under AUTHORED.
	fresh := rrDerivedRecomputation(t, planned, []CoverageAnchor{{File: rrS, Requirement: RequirementMutationConfinement,
		Describe: "a derived anchor over the same neighbour the AUTHORED invariant governs"}})
	if len(fresh) != 1 || fresh[0].Path != rrF || fresh[0].CoveringEvidence != evidenceDerived {
		t.Fatalf("premise: the DERIVED instrument authorises rrF at the pinned base: %+v", fresh)
	}
	authored := authoredGrant(t)
	if authored.Path != fresh[0].Path {
		t.Fatalf("premise: both instruments must be speaking about ONE path: %q and %q", authored.Path, fresh[0].Path)
	}
	// AND THE DERIVED COMPARISON REALLY DOES DISAGREE. Without this the witness
	// could pass for the trivial reason that there was never anything to
	// outrank: zero recorded DERIVED grants against one recomputed.
	if err := derivedDisagreement(partitionByInstrument([]testEditGrant{authored}), fresh); err == nil {
		t.Fatal("premise: the DERIVED comparison must disagree here, or there is no precedence to decide")
	}

	e := &Engine{}
	err := e.restoreTestEditGrants(rrRecord(t, "t", authored), fresh, planned, teWorld)
	r := refusalOf(t, err)

	// THE ABSENCE. The conditional diagnosis is not what the operator reads.
	if r.Binding == RestorationDerivedMismatch {
		t.Fatalf("a conditional DERIVED disagreement was reported while an unconditional AUTHORED blocker was present: %+v", r)
	}
	if r.Instrument == RestorationInstrumentDerived {
		t.Fatalf("the refusal blames the DERIVED instrument for authority it did not produce: %+v", r)
	}
	assertNotTheFalseDiagnosis(t, r, err)
	// THE PRESENCE. The unconditional blocker, by name.
	if r.Binding != RestorationAuthoredUnverifiable || r.Instrument != RestorationInstrumentAuthored {
		t.Fatalf("the unconditional blocker was not named: %+v", r)
	}
	// Still measured, and still attributed: the DERIVED recomputation is stated
	// as the DERIVED instrument's own number, not as a fact about "the world".
	if r.Measured == nil || r.Measured.RecordedAuthored != 1 || r.Measured.RecordedDerived != 0 || r.Measured.RecomputedDerived != 1 {
		t.Fatalf("the quantities are not attributed to the instruments that measured them: %+v", r.Measured)
	}
	// W6. NO REDUCED SET. The DERIVED instrument agrees about this exact path,
	// and that agreement installs nothing: execution never continues under a
	// subset the original run did not operate under.
	if got := e.testEditGrants("t"); len(got) != 0 {
		t.Fatalf("execution continued under authority the AUTHORED instrument never re-established: %+v", got)
	}

	// W5. DIAGNOSTIC NOT LOST. Changing which refusal WINS must not delete what
	// the message SAYS. An operator who can see the DERIVED instrument
	// authorising this path would otherwise read a refusal that never mentions
	// it -- an unexplained absence, which is the class of sentence this repair
	// exists to remove. Asserted on the message, not only on the typed fields.
	for _, text := range []string{r.Detail, r.Describe(), err.Error()} {
		if !strings.Contains(text, crossInstrumentAgreementNote) {
			t.Errorf("the cross-instrument fact is missing from the refusal: %q", text)
		}
		if !strings.Contains(text, rrF) {
			t.Errorf("the refusal does not name the path both instruments speak about: %q", text)
		}
	}
	for _, want := range []string{"AUTHORED", "provenance"} {
		if !strings.Contains(r.Detail, want) {
			t.Errorf("the refusal no longer names %q: %q", want, r.Detail)
		}
	}
}

// W7. KNOWN AUTHORED LEGACY TRUTHFULNESS CONTROL. The exact measured shape --
// recorded authority AUTHORED, DERIVED recomputation ZERO -- refuses for missing
// AUTHORED verification, and the false DERIVED-mismatch diagnosis is ABSENT.
func TestW7TheMeasuredShapeIsNotReportedAsADerivedMismatch(t *testing.T) {
	planned := []string{rrS, rrF}
	fresh := rrDerivedRecomputation(t, planned, nil)
	if len(fresh) != 0 {
		t.Fatalf("premise: the DERIVED instrument recomputes zero: %+v", fresh)
	}
	e := &Engine{}
	err := e.restoreTestEditGrants(rrRecord(t, "task-1790140827968282923", authoredGrant(t)), fresh, planned, teWorld)
	r := refusalOf(t, err)

	assertNotTheFalseDiagnosis(t, r, err)
	if r.Binding != RestorationAuthoredUnverifiable {
		t.Fatalf("binding %q: the unresolved condition is unverifiable AUTHORED authority", r.Binding)
	}
	// The refusal says what an operator has to know: which instrument, and that
	// nothing exists yet to verify it against.
	for _, want := range []string{"AUTHORED", "provenance"} {
		if !strings.Contains(r.Detail, want) {
			t.Errorf("the refusal does not name %q: %q", want, r.Detail)
		}
	}
	// It cites no quantity produced by one instrument against another: the
	// recomputed number is stated as the DERIVED instrument's, beside the
	// record's own two numbers.
	if r.Measured == nil || r.Measured.RecomputedDerived != 0 || r.Measured.RecordedDerived != 0 || r.Measured.RecordedAuthored != 1 {
		t.Fatalf("the quantities are not attributed to the instruments that measured them: %+v", r.Measured)
	}
	if len(e.testEditGrants("task-1790140827968282923")) != 0 {
		t.Fatal("authority was installed for a record nothing verified")
	}
}

// W8. AMBIGUOUS-INSTRUMENT CONTROL. A record that cannot prove which instrument
// produced a grant refuses as an instrument-binding ambiguity -- and does NOT
// infer the answer from current derivation output, even though that output
// authorises the exact path in question.
func TestW8AnUnreadableInstrumentBindingIsRefusedAndNeverInferred(t *testing.T) {
	planned := []string{teS, teF}
	// The DERIVED instrument authorises exactly this path today. A resume that
	// inferred provenance from its own output would restore all three records.
	fresh := rrDerivedRecomputation(t, planned, teCovered())
	if len(fresh) != 1 || fresh[0].Path != teF {
		t.Fatalf("premise: the recomputation authorises the path in question: %+v", fresh)
	}
	legacy := map[string]func(testEditGrant) testEditGrant{
		"no instrument recorded":       func(g testEditGrant) testEditGrant { g.CoveringEvidence = ""; return g },
		"an instrument nobody defines": func(g testEditGrant) testEditGrant { g.CoveringEvidence = "governed"; return g },
		"an instrument with no identity": func(g testEditGrant) testEditGrant {
			g.CoveringIdentity = []string{"  "}
			return g
		},
	}
	for name, forge := range legacy {
		e := &Engine{}
		err := e.restoreTestEditGrants(rrRecord(t, "t", forge(derivedGrant(t))), fresh, planned, teWorld)
		r := refusalOf(t, err)
		if r.Binding != RestorationInstrumentAmbiguous || r.Instrument != RestorationInstrumentUnreadable {
			t.Errorf("%s: was not refused as an ambiguous instrument binding: %+v", name, r)
		}
		if !strings.Contains(r.Detail, "does not infer") {
			t.Errorf("%s: the refusal does not say the answer was not inferred: %q", name, r.Detail)
		}
		if len(e.testEditGrants("t")) != 0 {
			t.Errorf("%s: a record that cannot name its instrument became authority", name)
		}
	}
	// The same guard in the other direction: authority the RECOMPUTATION
	// cannot attribute to the DERIVED instrument is not silently compared
	// either.
	e := &Engine{}
	r := refusalOf(t, e.restoreTestEditGrants(rrRecord(t, "t", derivedGrant(t)), []testEditGrant{authoredGrant(t)}, planned, teWorld))
	if r.Binding != RestorationInstrumentAmbiguous {
		t.Fatalf("a recomputation carrying non-DERIVED authority was compared anyway: %+v", r)
	}

	// AMBIGUITY WINS EARLIEST, and it keeps winning now that the AUTHORED
	// refusal precedes the DERIVED comparison. All three conditions hold at
	// once here: a grant whose instrument the record cannot name, an
	// unverifiable AUTHORED grant, and a DERIVED comparison that would
	// disagree. A record that cannot say which instrument produced a grant is
	// not a record either later check may speak about -- both of them are
	// statements about a partition this record has not earned -- so it refuses
	// BEFORE any comparison, as it always has.
	all := []string{teS, teF, rrS, rrF}
	allFresh := rrDerivedRecomputation(t, all, teCovered())
	unbound := derivedGrant(t)
	unbound.CoveringEvidence = ""
	eAll := &Engine{}
	rAll := refusalOf(t, eAll.restoreTestEditGrants(rrRecord(t, "t", unbound, authoredGrant(t)), allFresh, all, teWorld))
	if rAll.Binding != RestorationInstrumentAmbiguous || rAll.Instrument != RestorationInstrumentUnreadable {
		t.Fatalf("an unnameable instrument binding did not refuse first: %+v", rAll)
	}
	// The premises: both of the conditions it outranks really were present.
	if rAll.Measured == nil || rAll.Measured.RecordedAuthored != 1 || rAll.Measured.RecordedUnbound != 1 {
		t.Fatalf("premise: an AUTHORED grant and an unbound grant were both recorded: %+v", rAll.Measured)
	}
	if err := derivedDisagreement(partitionByInstrument([]testEditGrant{unbound, authoredGrant(t)}), allFresh); err == nil {
		t.Fatal("premise: the DERIVED comparison must also disagree, or ambiguity had nothing to outrank")
	}
	if len(eAll.testEditGrants("t")) != 0 {
		t.Fatal("a record that cannot name its instrument became authority")
	}
}

// The refusal reaches the terminal through the path a real resume takes, and
// through no other: Resume hands every restoration error to the one classifier.
func TestRestorationRefusalsAreClassifiedByTheOneTerminalClassifier(t *testing.T) {
	resume := funcBody(t, "internal/workflow/engine.go", "Resume")
	if !strings.Contains(resume, "e.restoreTestEditGrants(") || !strings.Contains(resume, "e.terminateRun(") {
		t.Fatal("Resume no longer hands its restoration error to the one terminal classifier")
	}
	body := funcBody(t, "internal/workflow/engine.go", "terminateRun")
	if !strings.Contains(body, "e.refuseRestoration(") {
		t.Fatal("the one classifier does not recognise a restoration refusal, so it would record one as task failure")
	}
	// A restoration refusal is not a record the reconstruction treats as an
	// ending: an unreadable one is refused rather than believed.
	if _, err := ParseRestorationRefusal(json.RawMessage(`{"task_id":"t","subject":"s","instrument":"derived","binding":"derived_mismatch","detail":"d"}`)); err == nil {
		t.Fatal("a refusal citing a comparison it never measured was accepted")
	}
	if _, err := ParseRestorationRefusal(json.RawMessage(`{"task_id":"t","subject":"s","instrument":"invented","binding":"derived_mismatch","detail":"d"}`)); err == nil {
		t.Fatal("a refusal naming an instrument nobody defines was accepted")
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

// teConstraints is a PRESENT build-constraint declaration. teConstraints() --
// with no lines -- is the plan saying "this file will carry none", which is a
// different statement from not declaring the field at all, and the pointer is
// what keeps the two apart.
func teConstraints(lines ...string) *[]string {
	set := append([]string{}, lines...)
	return &set
}

// teSourceFor is the candidate a declaration DESCRIBES: the file exactly as
// the plan said it would be after the edit. It is what makes W4 a subset
// proof rather than an assertion -- the projected fixture is handed to the
// late checker as the bytes it stands for.
//
// It renders only what the declaration states. A declaration that omits a
// field describes no single candidate, which is why omitted fields are not
// projected and are not carried through this helper as though they were.
func teSourceFor(d TestEditDeclaration) string {
	var b strings.Builder
	if d.BuildConstraints != nil {
		for _, c := range *d.BuildConstraints {
			b.WriteString(c + "\n")
		}
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
		BuildConstraints: teConstraints("//go:build go1.20"), Imports: []string{"strings", "testing"}}
}

// teTheMeasuredCase is the declaration the measured run would have carried:
// the same edit, plus the "errors" import the file lacks at the pinned base.
func teTheMeasuredCase() TestEditDeclaration {
	d := teInsideTheGrant()
	d.Imports = append(d.Imports, "errors")
	return d
}

// teWithNovelImport adds the measured novel import to any declaration, so a
// case can carry a decidable violation beside whatever else it is testing.
func teWithNovelImport(d TestEditDeclaration) TestEditDeclaration {
	d.Imports = append(append([]string{}, d.Imports...), "errors")
	return d
}

// teSpelledUnnormally is the same file written so that only normalization
// sees one path. A duplicate that could be hidden by writing the path
// differently would be no constraint at all.
func teSpelledUnnormally(d TestEditDeclaration) TestEditDeclaration {
	d.Path = "./modfile/../modfile/rule_test.go"
	return d
}

// teCandidateWithNovelImport is the file the implementer actually produced in
// the measured run: the pinned bytes plus the import the role admits no novel
// form of.
func teCandidateWithNovelImport() string {
	return strings.Replace(teFSrc, "\"strings\"\n", "\"strings\"\n\t\"errors\"\n", 1)
}

// W1 -- THE MEASURED CASE, PROJECTED. A plan declaring an edit to an existing
// test file, where the witness would require an import that file lacks at the
// pinned base, is refused AT PLAN ADMISSION, naming the file and the novel
// import.
//
// The declaration arrives the way a real one does: decoded out of the
// architect's JSON by the engine's own decoder, so the field the prompt asks
// for is the field the projector reads. The engine-level half of W1 -- that
// this refusal is what routePlan returns, and that no implementer is reached
// past it -- is in engine_test.go.
func TestTheMeasuredNovelImportIsRefusedAtPlanAdmission(t *testing.T) {
	plan, err := json.Marshal(architectureDecision{
		Decision: "proceed", Summary: "edit the regression beside its subject", Plan: "add the witness",
		Files: []string{teS, teF}, TestEdits: []TestEditDeclaration{teTheMeasuredCase()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var d architectureDecision
	if err := decodeModelJSON(string(plan), &d); err != nil {
		t.Fatalf("the declaration did not survive the decoder the architect's answer goes through: %v", err)
	}
	if len(d.TestEdits) != 1 || d.TestEdits[0].Path != teF {
		t.Fatalf("premise: the decoded plan carries no declaration over %s: %+v", teF, d.TestEdits)
	}
	err = projectTestEditRefusals(d.TestEdits, teEditGrants(t))
	if err == nil {
		t.Fatal("the novel import the candidate-time check refuses was admitted at plan admission")
	}
	if err.Error() != refuteTestEditNovelImport(teF, "errors").Error() {
		t.Fatalf("the projected refusal is not the authority's own sentence: %v", err)
	}
	for _, want := range []string{"test edit refuted:", teF, `"errors"`, "novel import", roleGoRegressionTestEdit} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the projected refusal does not name %q: %v", want, err)
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
	late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(decl)), nil })
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
	late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teCandidateWithNovelImport()), nil })
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
//
// The table carries the adversarial combinations as well as the plain ones:
// a declaration whose fields disagree with each other, declarations that
// violate two rules at once, where the two doors must still agree on WHICH
// rule they name first, and THE SAME PATH DECLARED MORE THAN ONCE. A subset
// proof that only covers one violation at a time, on distinct paths, cannot
// see an ordering that has drifted or a repetition that was mishandled.
//
// Every row's entries describe ONE candidate -- that is why the row is
// projected at all -- so decls[0] is the candidate the late door is given.
func TestEveryProjectedRefusalIsAlsoALateRefusal(t *testing.T) {
	grants := teEditGrants(t)

	wrongPackage := teInsideTheGrant()
	wrongPackage.Package = "modfile_test"

	wrongConstraints := teInsideTheGrant()
	wrongConstraints.BuildConstraints = teConstraints("//go:build go1.21")

	// The field is PRESENT and empty. The plan is saying the edited file will
	// carry no build constraint, and the pinned world says it carries one.
	// Declared, decidable, and refusable now.
	strippedConstraints := teInsideTheGrant()
	strippedConstraints.BuildConstraints = teConstraints()

	asCreate, asDelete, asRename := teInsideTheGrant(), teInsideTheGrant(), teInsideTheGrant()
	asCreate.Operation, asDelete.Operation, asRename.Operation = testEditOperationCreate, testEditOperationDelete, testEditOperationRename

	// The operation and the structural fields disagree: the declaration says
	// the file will not survive as an edit AND describes its contents. The
	// operation decides, at both doors.
	deleteCarryingAnImport := teWithNovelImport(asDelete)
	createCarryingAWrongPackage := asCreate
	createCarryingAWrongPackage.Package = "modfile_test"

	// Two violations in one edit. Package is named before imports at both
	// doors, or the operator reads a different reason depending on which door
	// caught it.
	packageAndImport := teWithNovelImport(wrongPackage)

	// Two novel imports. Both doors sort, so both name "bytes".
	twoNovelImports := teTheMeasuredCase()
	twoNovelImports.Imports = append(twoNovelImports.Imports, "bytes")

	// SAME PATH, DECLARED TWICE, SAYING THE SAME THING. Repetition is not
	// ambiguity: these state exactly what one of them states, so the outcome
	// stays decidable and the refusal must still arrive early. The variants
	// differ only in how they are WRITTEN -- the path spelled unnormally, the
	// imports reordered and repeated -- so a projector that compared
	// declarations by their text rather than by what they state would stop
	// projecting here and quietly hand a decidable refusal back to the late
	// door.
	//
	// The OPERATION is not restated differently here, and must not be: the
	// vocabulary is closed and its membership is exact, so a differently cased
	// operation is a different statement rather than the same one written
	// another way. That direction is
	// TestAnOperationOutsideTheClosedVocabularyIsNotReadAsAMemberOfIt.
	restated := teTheMeasuredCase()
	restated.Imports = []string{"errors", "testing", "strings", "errors"}

	projected := []struct {
		name  string
		decls []TestEditDeclaration
		diff  string
		want  string
	}{
		{"novel import", []TestEditDeclaration{teTheMeasuredCase()}, teDiffEdited, "novel import"},
		{"package change", []TestEditDeclaration{wrongPackage}, teDiffEdited, "package clause"},
		{"build-constraint change", []TestEditDeclaration{wrongConstraints}, teDiffEdited, "build constraints"},
		{"build constraints declared empty", []TestEditDeclaration{strippedConstraints}, teDiffEdited, "build constraints"},
		{"create", []TestEditDeclaration{asCreate}, teDiffCreated, "creates it"},
		{"delete", []TestEditDeclaration{asDelete}, teDiffDeleted, "deletes it"},
		{"rename", []TestEditDeclaration{asRename}, teDiffRenamed, "renames it"},
		{"delete declared beside a novel import", []TestEditDeclaration{deleteCarryingAnImport}, teDiffDeleted, "deletes it"},
		{"create declared beside a wrong package", []TestEditDeclaration{createCarryingAWrongPackage}, teDiffCreated, "creates it"},
		{"a wrong package beside a novel import", []TestEditDeclaration{packageAndImport}, teDiffEdited, "package clause"},
		{"two novel imports", []TestEditDeclaration{twoNovelImports}, teDiffEdited, `"bytes"`},
		{"one path declared twice, identically", []TestEditDeclaration{teTheMeasuredCase(), teTheMeasuredCase()}, teDiffEdited, "novel import"},
		{"one path declared twice, restated", []TestEditDeclaration{teTheMeasuredCase(), restated}, teDiffEdited, "novel import"},
		{"one path declared twice, restated first", []TestEditDeclaration{restated, teTheMeasuredCase()}, teDiffEdited, "novel import"},
		{"one path spelled two ways", []TestEditDeclaration{teTheMeasuredCase(), teSpelledUnnormally(teTheMeasuredCase())}, teDiffEdited, "novel import"},
		{"one path declared three times", []TestEditDeclaration{teTheMeasuredCase(), restated, teSpelledUnnormally(restated)}, teDiffEdited, "novel import"},
		{"a delete declared twice", []TestEditDeclaration{asDelete, asDelete}, teDiffDeleted, "deletes it"},
	}
	for _, c := range projected {
		early := projectTestEditRefusals(c.decls, grants)
		if early == nil {
			t.Errorf("%s: not projected", c.name)
			continue
		}
		if !strings.Contains(early.Error(), c.want) {
			t.Errorf("%s: the early door names something else (%q): %s", c.name, c.want, early)
		}
		late := teInspect(t, c.diff, grants, func(string) ([]byte, error) { return []byte(teSourceFor(c.decls[0])), nil })
		if late == nil {
			t.Errorf("%s: PROJECTED BUT NOT LATE-REFUSED -- the projection is no longer a subset: %s", c.name, early)
			continue
		}
		if early.Error() != late.Error() {
			t.Errorf("%s: two sentences for one condition:\n  early: %s\n  late:  %s", c.name, early, late)
		}
	}
}

// W4, AT THE WIRE. The distinction the projector rests on is a JSON one: the
// architect either sends "build_constraints" or does not, and the two must not
// arrive here as the same value. A decoder that flattens an explicit [] into an
// absent field silently admits a plan the pinned world already refuses -- the
// decidable refusal becomes invisible again, at the exact door this seam added.
//
// Both halves are asserted from the bytes rather than from a constructed
// struct, because the flattening happens in the decoding, not in the logic.
func TestAnExplicitlyEmptyBuildConstraintListIsADeclarationAndAnOmittedOneIsNot(t *testing.T) {
	grants := teEditGrants(t)
	decode := func(raw string) TestEditDeclaration {
		t.Helper()
		var d architectureDecision
		if err := decodeModelJSON(raw, &d); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		if len(d.TestEdits) != 1 {
			t.Fatalf("premise: %s carries no declaration", raw)
		}
		return d.TestEdits[0]
	}
	// The declaration is assembled as bytes, with the one field this witness is
	// about either present or absent, because that is the difference under test.
	const plan = `{"decision":"proceed","summary":"s","plan":"p","test_edits":[{"path":"` + teF +
		`","operation":"edit","package":"modfile",$CONSTRAINTS"imports":["strings","testing"]}]}`
	withConstraints := func(field string) string { return strings.Replace(plan, "$CONSTRAINTS", field, 1) }

	// PRESENT AND EMPTY: the plan states the edited file will carry no build
	// constraint. The pinned world says it carries one. Decidable, and refused
	// with the authority's own sentence.
	declared := decode(withConstraints(`"build_constraints":[],`))
	if declared.BuildConstraints == nil {
		t.Fatal("an explicit empty list decoded as an absent field: the two statements have collapsed into one")
	}
	early := projectTestEditRefusals([]TestEditDeclaration{declared}, grants)
	if early == nil {
		t.Fatal("a plan declaring that it strips the build constraint was admitted at plan admission")
	}
	late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(declared)), nil })
	if late == nil || early.Error() != late.Error() {
		t.Fatalf("the early door refuses something the late door does not, identically:\n  early: %s\n  late:  %v", early, late)
	}

	// ABSENT: the plan says nothing about build constraints, so nothing about
	// them is decided here. The late check governs, and still refuses.
	omitted := decode(withConstraints(""))
	if omitted.BuildConstraints != nil {
		t.Fatal("an omitted field decoded as a declaration: absence would now be read as a statement")
	}
	if err := projectTestEditRefusals([]TestEditDeclaration{omitted}, grants); err != nil {
		t.Fatalf("an undeclared build constraint was decided at plan admission: %v", err)
	}
	stripped := strings.TrimPrefix(teFSrc, "//go:build go1.20\n")
	if late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(stripped), nil }); late == nil {
		t.Fatal("nothing refused the undeclared strip at either door")
	}

	// AND THE DIFFERENCE SURVIVES REPETITION. Two declarations of one path that
	// differ only in whether the field is there at all are not the same
	// statement, so they do not collapse: the path states no single outcome and
	// nothing about it is decided here. If absence and emptiness were compared
	// as equal, the pair would collapse onto whichever entry came first and the
	// projector would refuse -- or admit -- by declaration order.
	for name, pair := range map[string][]TestEditDeclaration{
		"empty then absent": {declared, omitted},
		"absent then empty": {omitted, declared},
	} {
		if err := projectTestEditRefusals(pair, grants); err != nil {
			t.Errorf("%s: an absent and an empty declaration were collapsed into one statement: %v", name, err)
		}
	}
}

// W5 -- UNDECIDABLE IS NOT PROJECTED (CONTROL). What an implementer will
// write is not decidable from the plan, the pinned world and the role, and
// neither is the shape of a declaration this vocabulary cannot read. Each of
// these is admitted early and governed by the late check. Absence of a
// projection is not permission: the late check still refuses.
func TestAnUndecidableEffectIsAdmittedEarlyAndRefusedLate(t *testing.T) {
	grants := teEditGrants(t)

	// The imports are simply not declared: what the implementer will need is
	// a property of code nobody has written.
	contentDependent := TestEditDeclaration{Path: teF, Operation: testEditOperationEdit}

	// The build-constraint field is ABSENT rather than empty. It says nothing,
	// so nothing about the constraints is decided here -- the other half of the
	// absent-versus-empty distinction, and the direction that must stay late.
	constraintsUndeclared := teInsideTheGrant()
	constraintsUndeclared.BuildConstraints = nil

	// An operation outside the closed vocabulary, and an absent one. Both
	// carry a novel import and a wrong package beside them, so a projector
	// that read the fields anyway would have something to refuse.
	unknownOperation := teWithNovelImport(teInsideTheGrant())
	unknownOperation.Operation = "rewrite"
	unknownOperation.Package = "modfile_test"
	absentOperation := unknownOperation
	absentOperation.Operation = ""
	// And the operations that RESEMBLE a member of the closed vocabulary
	// without being one. Membership is exact, so these are as unreadable as
	// "rewrite": a projector that folded case or trimmed space would decide
	// membership by resemblance and refuse on a declaration the wire contract
	// reserves entirely for the late check. The full property, with its
	// anti-vacuity premise and both declaration orders, is
	// TestAnOperationOutsideTheClosedVocabularyIsNotReadAsAMemberOfIt.
	upperCasedOperation := unknownOperation
	upperCasedOperation.Operation = "EDIT"
	paddedOperation := unknownOperation
	paddedOperation.Operation = " edit "

	for name, decl := range map[string]TestEditDeclaration{
		"undeclared imports":            contentDependent,
		"undeclared build constraints":  constraintsUndeclared,
		"an unreadable operation":       unknownOperation,
		"no operation at all":           absentOperation,
		"an upper-cased near miss":      upperCasedOperation,
		"a whitespace-padded near miss": paddedOperation,
	} {
		if err := projectTestEditRefusals([]TestEditDeclaration{decl}, grants); err != nil {
			t.Errorf("%s: guessed at plan admission: %v", name, err)
		}
	}

	// The plan proceeded, the implementer wrote the novel import anyway, and
	// the candidate-time check -- which is the authority of record -- refused
	// it.
	late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teCandidateWithNovelImport()), nil })
	if late == nil || !strings.Contains(late.Error(), "novel import") {
		t.Fatalf("the undeclared novel import was never refused at all: %v", late)
	}
	// So did the stripped build constraint the plan never declared.
	stripped := strings.TrimPrefix(teFSrc, "//go:build go1.20\n")
	if late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(stripped), nil }); late == nil ||
		!strings.Contains(late.Error(), "build constraints") {
		t.Fatalf("an undeclared build-constraint change was never refused at all: %v", late)
	}
	// AND THE REASON IT MUST STAY LATE. For a declaration this vocabulary
	// cannot read, the candidate is not determined: the same declaration is
	// compatible with a candidate the late check refuses for DELETING the
	// file, which is not the reason an eager projector would have given. Two
	// different conditions for one declaration is exactly what a projection
	// may not choose between.
	deleted := teInspect(t, teDiffDeleted, grants, func(string) ([]byte, error) { return []byte(teSourceFor(unknownOperation)), nil })
	if deleted == nil || !strings.Contains(deleted.Error(), "deletes it") {
		t.Fatalf("premise: the same unreadable declaration admits a candidate refused for deletion: %v", deleted)
	}
	if strings.Contains(deleted.Error(), "novel import") {
		t.Fatal("the late check named the import on a deleted file; the two conditions are no longer distinguishable")
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
	if err := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(fine)), nil }); err != nil {
		t.Fatalf("the candidate the admitted declaration describes was refuted late: %v", err)
	}
	// Repeating an admissible declaration states exactly what one of them
	// states, so it stays admitted: collapsing repetition must not invent a
	// refusal any more than it may hide one.
	if err := projectTestEditRefusals([]TestEditDeclaration{fine, teSpelledUnnormally(fine)}, grants); err != nil {
		t.Fatalf("an admissible declaration became a refusal by being stated twice: %v", err)
	}
	// Declaring FEWER imports than the file already has is not a violation:
	// dropping one is admissible, so nothing here may refuse it.
	fewer := fine
	fewer.Imports = []string{"testing"}
	if err := projectTestEditRefusals([]TestEditDeclaration{fewer}, grants); err != nil {
		t.Fatalf("a declaration that drops an import was refused: %v", err)
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

// W4/W5 -- DECLARATIONS OF ONE PATH THAT DISAGREE STATE NO OUTCOME (CONTROL).
// Two declarations for one file that do not say the same thing do not describe
// one candidate: the accepted plan states no single structural outcome for it,
// so none is decidable at plan time.
//
// This is a SUBSET control, not a tidiness one. A projector that walked the
// entries and refused on whichever forbidden one it met first would pick a
// winner by declaration order, and a candidate that followed the admissible
// entry would pass the candidate-time check on a plan that was refused -- the
// projected set would stop being a subset of the late-refused set, which is the
// one inversion this repair may not contain. So every disagreeing path is left
// wholly to the late check, in either order, and absence of a projection is not
// permission: the late check still refuses whatever is actually produced.
//
// Its twin is in TestEveryProjectedRefusalIsAlsoALateRefusal, where the same
// path declared twice SAYING THE SAME THING is still projected. The rule is
// about what the entries state, never about how many there are.
func TestDeclarationsOfOnePathThatDisagreeAreNotProjectedInEitherOrder(t *testing.T) {
	grants := teEditGrants(t)

	admissible := teInsideTheGrant()
	novelImport := teTheMeasuredCase()
	deletion := teInsideTheGrant()
	deletion.Operation = testEditOperationDelete
	// Same operation, same package, same imports -- and one of them says
	// nothing about build constraints. An incomplete twin is a disagreement:
	// the two do not state the same candidate.
	constraintsUndeclared := novelImport
	constraintsUndeclared.BuildConstraints = nil
	// Two entries that agree about everything the pinned world settles EXCEPT
	// the imports, with the field that would otherwise separate them absent
	// from both. Nothing but the import sets distinguishes these, so an
	// identity test that stopped comparing once the constraints matched would
	// collapse them and project whichever came first.
	forbiddenUndeclaredConstraints := novelImport
	forbiddenUndeclaredConstraints.BuildConstraints = nil
	admissibleUndeclaredConstraints := admissible
	admissibleUndeclaredConstraints.BuildConstraints = nil
	// A novel import that sorts AFTER every import the admissible twin
	// declares, so once both are canonically ordered the shorter list is a
	// PREFIX of the longer one. Every pairwise element agrees as far as the
	// shorter one goes, and only its LENGTH says the two are different
	// statements.
	trailingNovelImport := teInsideTheGrant()
	trailingNovelImport.Imports = append(append([]string{}, trailingNovelImport.Imports...), "unicode")
	// And the other way round: a forbidden declaration stating exactly as MANY
	// imports as the admissible twin, so only WHICH imports each names tells
	// the two statements apart.
	sameCountNovelImport := teInsideTheGrant()
	sameCountNovelImport.Imports = []string{"errors", "testing"}

	// PREMISE, AND THE ANTI-VACUITY HALF. Each forbidden declaration is
	// projected when it stands alone, including under the alternative spelling.
	// Without this, the absences below could be an inert fixture -- a path that
	// never bound to the grant at all -- rather than the disagreement.
	for name, decl := range map[string]TestEditDeclaration{
		"the novel import alone":               novelImport,
		"the deletion alone":                   deletion,
		"the novel import, spelled unnormally": teSpelledUnnormally(novelImport),
		"the trailing novel import alone":      trailingNovelImport,
		"the same-count novel import alone":    sameCountNovelImport,
	} {
		if err := projectTestEditRefusals([]TestEditDeclaration{decl}, grants); err == nil {
			t.Fatalf("premise: %s is not projected on its own, so the controls below would pass for the wrong reason", name)
		}
	}

	disagreeing := map[string][]TestEditDeclaration{
		"admissible then novel import":         {admissible, novelImport},
		"novel import then admissible":         {novelImport, admissible},
		"admissible then delete":               {admissible, deletion},
		"delete then admissible":               {deletion, admissible},
		"two different forbidden declarations": {novelImport, deletion},
		"the novel import spelled unnormally":  {admissible, teSpelledUnnormally(novelImport)},
		"unnormally spelled, and first":        {teSpelledUnnormally(novelImport), admissible},
		"an incomplete twin":                   {novelImport, constraintsUndeclared},
		"the incomplete twin first":            {constraintsUndeclared, novelImport},
		// Only the imports differ, and only the imports can be seen.
		"twins separated by their imports alone": {forbiddenUndeclaredConstraints, admissibleUndeclaredConstraints},
		// The forbidden entry is the one spelled normally, so a projector that
		// bound the admissible twin under its unnormalized spelling would see
		// one unopposed forbidden declaration and refuse a plan the late check
		// admits.
		"the admissible twin spelled unnormally": {teSpelledUnnormally(admissible), novelImport},
		// Separated only by how MANY imports each states.
		"twins whose imports are a prefix of one another": {trailingNovelImport, admissible},
		"the shorter twin first":                          {admissible, trailingNovelImport},
		// Separated only by WHICH imports each states.
		"twins stating the same number of imports": {sameCountNovelImport, admissible},
		"the admissible twin first":                {admissible, sameCountNovelImport},
		// A third entry that agrees with the first must not resolve a
		// disagreement the second one already established.
		"a disagreement restated": {novelImport, admissible, novelImport},
	}
	for name, decls := range disagreeing {
		if err := projectTestEditRefusals(decls, grants); err != nil {
			t.Errorf("%s: a path stating no single outcome was refused early, by declaration order: %v", name, err)
		}
	}

	// AND THE LATE CHECK REMAINS AUTHORITATIVE over the file the plan could not
	// uniquely describe. Whichever candidate is actually produced is judged, by
	// the same sentences, exactly as before projection existed.
	late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teCandidateWithNovelImport()), nil })
	if late == nil || !strings.Contains(late.Error(), "novel import") {
		t.Fatalf("a novel import declared twice, inconsistently, was never refused at all: %v", late)
	}
	if late := teInspect(t, teDiffDeleted, grants, func(string) ([]byte, error) { return []byte(teFSrc), nil }); late == nil ||
		!strings.Contains(late.Error(), "deletes it") {
		t.Fatalf("a deletion declared twice, inconsistently, was never refused at all: %v", late)
	}
	// A candidate that followed the ADMISSIBLE entry passes late -- which is
	// precisely why no early refusal may be produced from this plan: the early
	// door would have refused a run the authority of record admits.
	if err := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(admissible)), nil }); err != nil {
		t.Fatalf("premise: the admissible entry of the disagreeing pair describes a candidate the late check accepts: %v", err)
	}
}

// W4/W5 -- A NEAR MISS IS NOT A MEMBER OF THE CLOSED VOCABULARY (CONTROL).
// "operation" is read by EXACT membership in edit, create, delete and rename.
// "EDIT", "Edit" and " edit " are outside that set, so they declare NOTHING --
// not the operation, and not the package, constraints or imports beside them --
// and the file they name is governed entirely by the unchanged late check.
//
// The defect this control exists for is a normalizer. Folding case or trimming
// space before testing membership decides membership by RESEMBLANCE, which
// closes nothing: a vocabulary's whole meaning is which values it excludes, and
// normalizing moves a value from outside the set to inside it. The early door
// would then read the fields beside a declaration the wire contract reserves
// for the late check, and refuse a plan on a statement the plan never made.
//
// Every near miss below carries fields that WOULD be refusable for a valid
// edit -- a changed package clause and an import the pinned world lacks -- so a
// projector that read them has something to refuse and cannot pass this test by
// having nothing to say.
//
// BOTH DECLARATION ORDERS. The discriminating case is a near miss beside its
// EXACTLY spelled twin stating the same fields: a normalizer collapses the two
// into one refusable statement and projects, where exact membership sees one
// readable statement and one that says nothing, which is not one stated outcome
// for that path. So the path is undecidable here, in either order, and stays
// with the late check -- and absence of a projection is not permission, which
// the late half below asserts.
func TestAnOperationOutsideTheClosedVocabularyIsNotReadAsAMemberOfIt(t *testing.T) {
	grants := teEditGrants(t)

	spelled := func(d TestEditDeclaration, op string) TestEditDeclaration {
		d.Operation = op
		return d
	}
	// Refusable twice over for a VALID edit: a changed package clause and the
	// measured novel import.
	refusable := teWithNovelImport(teInsideTheGrant())
	refusable.Package = "modfile_test"
	admissible := teInsideTheGrant()
	deletion := spelled(admissible, testEditOperationDelete)

	nearMisses := map[string]TestEditDeclaration{
		"upper-cased edit":      spelled(refusable, "EDIT"),
		"title-cased edit":      spelled(refusable, "Edit"),
		"space-padded edit":     spelled(refusable, " edit "),
		"tab-suffixed edit":     spelled(refusable, "edit\t"),
		"upper-cased delete":    spelled(deletion, "DELETE"),
		"space-padded delete":   spelled(deletion, " delete "),
		"upper-cased create":    spelled(refusable, "CREATE"),
		"newline-padded rename": spelled(admissible, " rename\n"),
	}
	for spelling, decl := range nearMisses {
		// The member this near miss RESEMBLES, built with the very
		// normalization the projector may not perform and carrying the
		// IDENTICAL fields.
		twin := spelled(decl, strings.ToLower(strings.TrimSpace(decl.Operation)))

		// PREMISE, THE ANTI-VACUITY HALF, PER ROW. Spelled exactly, this
		// fixture IS projected. Without it, every absence below could come from
		// a path that never bound to the grant, a field the projector ignores,
		// or fields that are simply admissible -- and the row would pass while
		// the vocabulary stood wide open.
		if err := projectTestEditRefusals([]TestEditDeclaration{twin}, grants); err == nil {
			t.Fatalf("premise: %q spelled exactly (%q) is not projected, so this row would pass for the wrong reason", spelling, twin.Operation)
		}
		// THE NEAR MISS ALONE states nothing, so nothing is projected from it.
		if err := projectTestEditRefusals([]TestEditDeclaration{decl}, grants); err != nil {
			t.Errorf("%q was read as a member of the closed vocabulary and its fields were refused early: %v", spelling, err)
		}
		// AND BESIDE ITS EXACTLY SPELLED TWIN, IN EITHER ORDER. This is the
		// pair a normalizer collapses into one refusable statement; under exact
		// membership the path holds one readable statement and one that says
		// nothing, which is not one stated outcome, so it stays with the late
		// check.
		if err := projectTestEditRefusals([]TestEditDeclaration{decl, twin}, grants); err != nil {
			t.Errorf("%q collapsed into its exactly spelled twin and the pair was refused early: %v", spelling, err)
		}
		if err := projectTestEditRefusals([]TestEditDeclaration{twin, decl}, grants); err != nil {
			t.Errorf("%q collapsed into its exactly spelled twin, twin first, and the pair was refused early: %v", spelling, err)
		}
	}

	// ABSENCE OF A PROJECTION IS NOT PERMISSION. inspectTestEdits -- the
	// authority of record, unchanged -- still refuses the candidate each near
	// miss describes, with its own sentence.
	late := teInspect(t, teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(refusable)), nil })
	if late == nil || !strings.Contains(late.Error(), "package clause") {
		t.Fatalf("the candidate a near-missed edit describes was never refused at all: %v", late)
	}
	if late := teInspect(t, teDiffDeleted, grants, func(string) ([]byte, error) { return []byte(teFSrc), nil }); late == nil ||
		!strings.Contains(late.Error(), "deletes it") {
		t.Fatalf("the deletion a near-missed operation describes was never refused at all: %v", late)
	}
}

// MECHANICAL FIXTURE MIGRATION (ruling 91), and nothing more. Candidate
// inspection now reads the test-edit record of the operative plan attempt at
// the pinned world, so each pre-existing control above hands its grants over
// as exactly that record: bound to one canonical PlanAttemptID, at teWorld.
// Its diff, its grants, its candidate bytes and its expected outcome are
// unchanged. The DF-23 behaviour is proven by the separate witnesses below.
func teInspect(t *testing.T, diff string, grants []testEditGrant, candidate func(string) ([]byte, error)) error {
	t.Helper()
	a := teAttemptAt(t, "the M2.2 control plan", teWorld)
	return inspectDiff(diff, a, testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants}, candidate)
}

// teAttemptAt is a canonical plan attempt derived AT world: its ID is
// planAttemptID over that world, so the ID and the world it carries are one
// identity, as the operative attempt's are.
func teAttemptAt(t *testing.T, plan, world string) planAttempt {
	t.Helper()
	d := architectureDecision{Decision: "proceed", Plan: plan}
	id, err := planAttemptID("t", attemptObjective, world, PlanByArchitect, "", d)
	if err != nil {
		t.Fatal(err)
	}
	return planAttempt{ID: id, TaskID: "t", World: world, PlanSource: PlanByArchitect, Plan: d}
}

// MECHANICAL FIXTURE MIGRATION (DF-23, finding f1). Inspection now reads the
// frozen capture's exact path set and measures existence at the pinned world
// and in the candidate tree, instead of re-parsing the rendered diff. Every
// control above still states its candidate as a fixture diff; inspectDiff
// translates that fixture into exactly the state the capture would hold --
// the changed paths, rename detection off; absent at the world if the fixture
// creates it; absent from the candidate if it deletes it -- and nothing else.
// The fixtures are unquoted, so this reading of them is exact; the
// quoted-path witnesses below construct their state directly.
func inspectDiff(diff string, operative planAttempt, rec testEditRecord, candidate func(string) ([]byte, error)) error {
	return inspectTestEdits(teDiffState(diff, candidate), operative, rec)
}

func teDiffState(diff string, candidate func(string) ([]byte, error)) candidateTestState {
	notAtWorld, notInCandidate := map[string]bool{}, map[string]bool{}
	var paths []string
	for _, block := range strings.Split(diff, "diff --git a/")[1:] {
		header, body, _ := strings.Cut(block, "\n")
		from, to, _ := strings.Cut(header, " b/")
		switch {
		case strings.Contains(body, "new file mode"):
			paths, notAtWorld[to] = append(paths, to), true
		case strings.Contains(body, "deleted file mode"):
			paths, notInCandidate[from] = append(paths, from), true
		case strings.Contains(body, "rename from "):
			paths = append(paths, from, to)
			notInCandidate[from], notAtWorld[to] = true, true
		default:
			paths = append(paths, to)
		}
	}
	return candidateTestState{
		Paths:       paths,
		World:       teWorld,
		AtWorld:     teReader(notAtWorld, func(string) ([]byte, error) { return []byte("package x\n"), nil }),
		InCandidate: teReader(notInCandidate, candidate),
		Diff:        diff,
	}
}

func teReader(absent map[string]bool, read func(string) ([]byte, error)) func(string) ([]byte, error) {
	return func(p string) ([]byte, error) {
		if absent[p] {
			return nil, errNotAtWorld
		}
		return read(p)
	}
}

// DF-23 -- EXISTING-TEST EDIT AUTHORITY IS ENUMERATED FROM THE CANDIDATE.
//
// Objective 61, run 1: the plan named internal/event/bus_test.go and
// internal/runreceipt/receipt_test.go, routing granted neither, the candidate
// edited both, and nothing refused it -- inspection iterated the grants, so an
// ungranted test was never examined. These witnesses are separate from the
// migrated controls above.

const (
	df23Receipt = "internal/runreceipt/receipt_test.go"
	df23Bus     = "internal/event/bus_test.go"
)

func df23Edit(p string) string {
	return "diff --git a/" + p + " b/" + p + "\nindex 1..2 100644\n--- a/" + p + "\n+++ b/" + p + "\n@@ -1 +1 @@\n-x\n+y\n"
}

// df23Candidate serves teF as an in-grant edit and anything else as some Go.
func df23Candidate(p string) ([]byte, error) {
	if p == teF {
		return []byte(strings.Replace(teFSrc, "TestX", "TestY", 1)), nil
	}
	return []byte("package other\n"), nil
}

func df23Record(t *testing.T, plan string, grants []testEditGrant) (planAttempt, testEditRecord) {
	t.Helper()
	a := teAttemptAt(t, plan, teWorld)
	return a, testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants}
}

// W1 -- the objective-61 run-1 shape: both ungranted tests edited, under an
// explicit empty grant set and under a record granting only another test.
func TestDF23W1AnUngrantedExistingTestEditIsRefused(t *testing.T) {
	for name, grants := range map[string][]testEditGrant{"no grants at all": nil, "grants for another test": teEditGrants(t)} {
		a, rec := df23Record(t, "objective 61", grants)
		for _, f := range []string{df23Receipt, df23Bus} {
			err := inspectDiff(df23Edit(f), a, rec, df23Candidate)
			if err == nil || !strings.HasPrefix(err.Error(), "test edit refuted:") || !strings.Contains(err.Error(), f) ||
				!strings.Contains(err.Error(), "holds no test-edit grant") {
				t.Errorf("%s: an ungranted edit of %s was not refused: %v", name, f, err)
			}
			if !isProspectiveSurfaceRefutation(err) {
				t.Errorf("%s: the refusal of %s is not terminal: %v", name, f, err)
			}
		}
		both := df23Edit(df23Receipt) + df23Edit(df23Bus)
		if err := inspectDiff(both, a, rec, df23Candidate); err == nil {
			t.Errorf("%s: the measured candidate was admitted", name)
		}
	}
	// No operative plan attempt, or a record at another world: no authority.
	a, rec := df23Record(t, "objective 61", teEditGrants(t))
	if err := inspectDiff(df23Edit(teF), planAttempt{World: teWorld}, rec, df23Candidate); err == nil || !strings.Contains(err.Error(), "no operative plan attempt") {
		t.Errorf("an edit with no operative plan attempt was admitted: %v", err)
	}
	other := rec
	other.World = "other-world"
	if err := inspectDiff(df23Edit(teF), a, other, df23Candidate); err == nil || !strings.Contains(err.Error(), "not the pinned world") {
		t.Errorf("a record read at another world authorized an edit: %v", err)
	}
	unpinned := a
	unpinned.World = ""
	if err := inspectDiff(df23Edit(teF), unpinned, rec, df23Candidate); err == nil || !strings.Contains(err.Error(), "not the candidate's base") {
		t.Errorf("an edit under an attempt with no pinned world was admitted: %v", err)
	}
	// A grant inside the record that was read at another world.
	g := teEditGrants(t)
	g[0].World = "other-world"
	if err := inspectDiff(df23Edit(teF), a, testEditRecord{PlanAttemptID: a.ID, World: teWorld, Grants: g}, df23Candidate); err == nil ||
		!strings.Contains(err.Error(), "its grant was read at world") {
		t.Errorf("a grant read at another world authorized an edit: %v", err)
	}
	// Two grants for one path are not exactly one.
	dup := append(teEditGrants(t), teEditGrants(t)...)
	if err := inspectDiff(df23Edit(teF), a, testEditRecord{PlanAttemptID: a.ID, World: teWorld, Grants: dup}, df23Candidate); err == nil ||
		!strings.Contains(err.Error(), "not exactly one") {
		t.Errorf("duplicate grants authorized an edit: %v", err)
	}
}

// W1, review f2 -- THE ATTEMPT'S ID AND WORLD ARE ONE IDENTITY. An attempt
// canonically derived at another world, with a record and grant bound to it
// at that same world -- internally consistent -- authorizes nothing in a
// candidate measured against teWorld: the operative world must be the base.
// And an ID derived at teWorld does not borrow another world's record.
func TestDF23AnOperativeAttemptAtAnotherWorldAuthorizesNothing(t *testing.T) {
	const elsewhere = "1111111111111111111111111111111111111111"
	there := teAttemptAt(t, "objective 61", elsewhere)
	g := teEditGrants(t)
	g[0].World = elsewhere
	rec := testEditRecord{PlanAttemptID: there.ID, World: elsewhere, Grants: g}
	if err := inspectDiff(df23Edit(teF), there, rec, df23Candidate); err == nil ||
		!strings.Contains(err.Error(), "not the candidate's base") || !strings.Contains(err.Error(), teF) {
		t.Errorf("an attempt pinned at another world authorized the candidate's edit: %v", err)
	}
	// The same plan derived at teWorld is a different canonical attempt: its
	// ID is not the other world's, and the other world's record is refused.
	here := teAttemptAt(t, "objective 61", teWorld)
	if here.ID == there.ID {
		t.Fatal("premise: the canonical ID does not depend on the world it was derived at")
	}
	if err := inspectDiff(df23Edit(teF), here, rec, df23Candidate); err == nil || !strings.Contains(err.Error(), "superseded or other attempt") {
		t.Errorf("another world's attempt record authorized the operative attempt's edit: %v", err)
	}
	// Mixing: this attempt's ID paired with the other world is still refused.
	mixed := here
	mixed.World = elsewhere
	if err := inspectDiff(df23Edit(teF), mixed, testEditRecord{PlanAttemptID: here.ID, World: elsewhere, Grants: g}, df23Candidate); err == nil ||
		!strings.Contains(err.Error(), "not the candidate's base") {
		t.Errorf("an attempt ID carried with another world authorized an edit: %v", err)
	}
	// Control: the attempt derived at teWorld with its own record proceeds.
	if err := inspectDiff(df23Edit(teF), here, testEditRecord{PlanAttemptID: here.ID, World: teWorld, Grants: teEditGrants(t)}, df23Candidate); err != nil {
		t.Errorf("the bound attempt's own grant was refused: %v", err)
	}
}

// W2 -- control: the edit the current attempt granted proceeds.
func TestDF23W2AGrantedExistingTestEditProceeds(t *testing.T) {
	a, rec := df23Record(t, "objective 61", teEditGrants(t))
	if err := inspectDiff(df23Edit(teF), a, rec, df23Candidate); err != nil {
		t.Fatalf("an edit inside the current attempt's grant was refused: %v", err)
	}
	// A candidate that mutates no existing test needs no grant at all.
	if err := inspectDiff(df23Edit(teS), a, testEditRecord{PlanAttemptID: a.ID, World: teWorld}, df23Candidate); err != nil {
		t.Fatalf("a production-only candidate was refused: %v", err)
	}
}

// df23Engine is a durable engine whose task is pinned at teWorld, so every
// plan attempt routing begins is canonically derived AT teWorld -- the world
// the fixture candidates are measured against.
func df23Engine(t *testing.T, root, task string) *Engine {
	t.Helper()
	e, _ := attemptEngine(t)
	if root != "" {
		e.Repo.Root = root
	}
	df23Pin(t, e, task, teWorld)
	return e
}

// df23Pin records world as task's candidate base, which governedBase -- and
// so beginPlanAttempt -- reads as the world an attempt is derived at.
func df23Pin(t *testing.T, e *Engine, task, world string) {
	t.Helper()
	id := candidateIdentityWithBase(world)
	id.TaskID = task
	if err := id.Save(e.Repo.Root); err != nil {
		t.Fatal(err)
	}
}

// df23Route routes d through the production attempt transition: the attempt
// begins at the pinned world, its whole grant state is recorded bound to it at
// that world, and it is made operative.
func df23Route(t *testing.T, e *Engine, task string, d architectureDecision, grants []testEditGrant) planAttempt {
	t.Helper()
	a, err := e.beginPlanAttempt(task, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.recordTestEditGrants(task, "recorded", testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants}); err != nil {
		t.Fatal(err)
	}
	e.setTestEditGrants(task, grants)
	if err := e.recordProspectiveGrants(task, "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: a.World}); err != nil {
		t.Fatal(err)
	}
	e.setProspectiveGrants(task, nil)
	if _, err := e.adoptPlanAttempt(task, attemptObjective, d); err != nil {
		t.Fatal(err)
	}
	return a
}

// df23Inspect is the candidate-inspection handoff as runCandidate makes it:
// the operative attempt whole, and the test-edit record recorded for exactly
// it, against a candidate measured at teWorld.
func df23Inspect(e *Engine, taskID, diff string) error {
	operative := e.operativePlanAttempt(taskID)
	_, edits := e.recordedGrants(taskID, operative.ID)
	return inspectDiff(diff, operative, edits, df23Candidate)
}

// W3 -- the same test path across a PRODUCTION re-plan: plan A's grant does
// not authorize plan B, and B's own re-derived grant (plan C) does.
func TestDF23W3AGrantFromASupersededPlanAttemptConfersNoAuthority(t *testing.T) {
	const task = "task-df23-replan"
	e := df23Engine(t, "", task)
	planA := attemptPlan("plan A", teS, teF)
	a := df23Route(t, e, task, planA, teEditGrants(t))
	if a.World != teWorld {
		t.Fatalf("premise: the attempt was derived at the pinned world, got %q", a.World)
	}
	if err := df23Inspect(e, task, df23Edit(teF)); err != nil {
		t.Fatalf("premise: plan A's grant authorizes its edit: %v", err)
	}
	planB := attemptPlan("plan B", teS, teF)
	b := df23Route(t, e, task, planB, nil)
	if op := e.operativePlanAttempt(task); op.ID != b.ID || b.ID == a.ID {
		t.Fatalf("premise: the re-plan made B operative (a=%s b=%s operative=%s)", short12(a.ID), short12(b.ID), short12(op.ID))
	}
	if err := df23Inspect(e, task, df23Edit(teF)); err == nil || !strings.Contains(err.Error(), "holds no test-edit grant") {
		t.Fatalf("plan A's grant authorized plan B's edit of %s: %v", teF, err)
	}
	// Plan A's record, handed over whole, is bound to the superseded attempt.
	_, stale := e.recordedGrants(task, a.ID)
	if len(stale.Grants) != 1 {
		t.Fatalf("premise: plan A's record still holds its grant: %+v", stale)
	}
	if err := inspectDiff(df23Edit(teF), e.operativePlanAttempt(task), stale, df23Candidate); err == nil || !strings.Contains(err.Error(), "superseded") {
		t.Fatalf("a superseded attempt's record authorized the operative attempt's edit: %v", err)
	}
	// A further re-plan that re-derives the grant for the same path is authorized by its own record.
	planC := attemptPlan("plan C", teS, teF)
	df23Route(t, e, task, planC, teEditGrants(t))
	if err := df23Inspect(e, task, df23Edit(teF)); err != nil {
		t.Fatalf("the operative attempt's own re-derived grant was refused: %v", err)
	}
}

// W3, resumed: a resume restores the operative attempt first and then accepts
// only the grants recorded for it.
func TestDF23W3AResumedTaskInspectsUnderItsOperativeAttemptsRecord(t *testing.T) {
	const task = "task-df23-resume"
	e := df23Engine(t, "", task)
	planA := attemptPlan("plan A", teS, teF)
	df23Route(t, e, task, planA, teEditGrants(t))
	interrupted := interruptedFrom(t, e.Store, task)
	fresh := &Engine{}
	if err := fresh.restorePlanAttempt(interrupted, teWorld); err != nil {
		t.Fatal(err)
	}
	if err := fresh.restoreTestEditGrants(interrupted, teEditGrants(t), []string{teS, teF}, teWorld); err != nil {
		t.Fatal(err)
	}
	if err := df23Inspect(fresh, task, df23Edit(teF)); err != nil {
		t.Fatalf("the resumed operative attempt's recorded grant was refused: %v", err)
	}
	if err := df23Inspect(fresh, task, df23Edit(df23Bus)); err == nil {
		t.Fatal("a resumed task edited an ungranted existing test")
	}
}

// W4 -- a newly created test is prospective CREATE's to govern, never an
// existing-test edit, with or without any existing-test grant.
func TestDF23W4ACreatedTestIsNotAnExistingTestEdit(t *testing.T) {
	const created = "modfile/new_test.go"
	diff := "diff --git a/" + created + " b/" + created + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + created + "\n@@ -0,0 +1 @@\n+package modfile\n"
	for name, grants := range map[string][]testEditGrant{"no grants": nil, "a grant for another test": teEditGrants(t)} {
		a, rec := df23Record(t, "plan", grants)
		if err := inspectDiff(diff, a, rec, df23Candidate); err != nil {
			t.Errorf("%s: a created test was inspected as an existing-test edit: %v", name, err)
		}
	}
}

// W5 -- deleting or renaming an existing test is a mutation of the ORIGINAL
// path, and needs authority over it; a rename does not escape as a CREATE.
func TestDF23W5DeletingOrRenamingAnUngrantedExistingTestIsRefused(t *testing.T) {
	a, rec := df23Record(t, "plan", teEditGrants(t))
	deleted := "diff --git a/" + df23Receipt + " b/" + df23Receipt + "\ndeleted file mode 100644\n"
	if err := inspectDiff(deleted, a, rec, df23Candidate); err == nil ||
		!strings.Contains(err.Error(), "deletes the existing test "+df23Receipt) || !strings.Contains(err.Error(), "holds no test-edit grant") {
		t.Errorf("deleting an ungranted existing test was not refused: %v", err)
	}
	for name, dest := range map[string]string{"to another test": "internal/runreceipt/moved_test.go", "to a non-test": "internal/runreceipt/moved.go"} {
		renamed := "diff --git a/" + df23Receipt + " b/" + dest + "\nsimilarity index 100%\nrename from " + df23Receipt + "\nrename to " + dest + "\n"
		if err := inspectDiff(renamed, a, rec, df23Candidate); err == nil ||
			!strings.Contains(err.Error(), "renames the existing test "+df23Receipt) || !strings.Contains(err.Error(), "holds no test-edit grant") {
			t.Errorf("renaming an ungranted existing test %s was not refused on its source: %v", name, err)
		}
	}
	// With authority over the source the edit-in-place rule still refuses both.
	if err := inspectDiff(teDiffDeleted, a, rec, df23Candidate); err == nil || !strings.Contains(err.Error(), "deletes it") {
		t.Errorf("deleting a granted test was admitted: %v", err)
	}
	if err := inspectDiff(teDiffRenamed, a, rec, df23Candidate); err == nil || !strings.Contains(err.Error(), "renames it") {
		t.Errorf("renaming a granted test was admitted: %v", err)
	}
}

// preDF23InspectTestEdits is the NEGATIVE CONTROL for W6a: inspectTestEdits as
// it stood before DF-23 (main 5760b20), reproduced faithfully in its path
// selection -- it iterates the GRANTS, skips a granted file the diff did not
// touch, and never looks at any other path -- and in its in-place checks. Only
// the order in which novel imports are reported is unsorted here; it decides
// which import a refusal names, never whether one is refused.
func preDF23InspectTestEdits(diff string, grants []testEditGrant, candidate func(path string) ([]byte, error)) error {
	if len(grants) == 0 {
		return nil
	}
	touched, created, deleted, renamed := diffFileStates(diff)
	for _, g := range grants {
		f := g.Path
		if !touched[f] {
			continue
		}
		switch {
		case created[f]:
			return refuteTestEditCreated(f)
		case deleted[f]:
			return refuteTestEditDeleted(f)
		case renamed[f]:
			return refuteTestEditRenamed(f)
		}
		after, err := candidate(f)
		if err != nil {
			return errors.New("test edit refuted: " + f + " could not be read from the candidate: " + err.Error())
		}
		facts, err := testFacts(after)
		if err != nil {
			return errors.New("test edit refuted: " + f + " could not be read as Go after the edit: " + err.Error())
		}
		if facts.Package != g.Facts.Package {
			return refuteTestEditPackage(f, g.Facts.Package, facts.Package)
		}
		if strings.Join(facts.Constraints, "\n") != strings.Join(g.Facts.Constraints, "\n") {
			return refuteTestEditConstraints(f, g.Facts.Constraints, facts.Constraints)
		}
		for imp := range facts.Imports {
			if !g.Facts.Imports[imp] {
				return refuteTestEditNovelImport(f, imp)
			}
		}
	}
	return nil
}

// W6a -- NEGATIVE CONTROL, executed. The pre-existing control's fixture
// (TestAGrantedTestEditIsInspectedAgainstItsExactGrant), migrated exactly as
// teInspect migrates it -- one record bound to a canonical attempt at teWorld
// -- is run through BOTH inspectors. Its granted scenario passes both,
// unchanged. The same migrated record with an ungranted existing test edited
// beside it is ADMITTED by the pre-DF-23 grant-driven inspector and REFUSED by
// the repaired one: the migration alone does not make the repair pass; the
// candidate-first enumeration does.
func TestDF23W6aTheMigratedControlFixtureDoesNotItselfRefuse(t *testing.T) {
	grants, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, teRead(map[string]string{teS: teSSrc, teF: teFSrc}))
	good := func(string) ([]byte, error) { return []byte(strings.Replace(teFSrc, "TestX", "TestY", 1)), nil }
	a := teAttemptAt(t, "the M2.2 control plan", teWorld)
	rec := testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants}

	// The control scenario, unchanged, passes both.
	if err := preDF23InspectTestEdits(df23Edit(teF), rec.Grants, good); err != nil {
		t.Fatalf("premise: the pre-DF-23 inspector admits the control's in-grant edit: %v", err)
	}
	if err := inspectDiff(df23Edit(teF), a, rec, good); err != nil {
		t.Fatalf("the migrated control no longer passes: %v", err)
	}
	// The unauthorized edit: the migrated record does not refuse it on its own.
	for _, diff := range []string{df23Edit(df23Receipt), df23Edit(teF) + df23Edit(df23Receipt)} {
		if err := preDF23InspectTestEdits(diff, rec.Grants, good); err != nil {
			t.Fatalf("the negative control is not faithful: the pre-DF-23 inspector refused through the migrated record: %v", err)
		}
		if err := inspectDiff(diff, a, rec, good); err == nil || !strings.Contains(err.Error(), df23Receipt) || !strings.Contains(err.Error(), "holds no test-edit grant") {
			t.Fatalf("the repaired inspector admitted the ungranted existing test edit: %v", err)
		}
	}
	// And the refusing controls still refuse under the pre-DF-23 inspector,
	// so the control is the real old predicate, not a stub that admits all.
	if err := preDF23InspectTestEdits(teDiffDeleted, rec.Grants, good); err == nil || !strings.Contains(err.Error(), "deletes it") {
		t.Fatalf("the negative control does not reproduce the old in-place checks: %v", err)
	}
}

// W7 -- complete enumeration: one granted and one ungranted mutation, in
// either order, is refused on the ungranted path and only on it.
func TestDF23W7SomeValidGrantsDoNotNarrowTheEnumeration(t *testing.T) {
	a, rec := df23Record(t, "plan", teEditGrants(t))
	for _, diff := range []string{df23Edit(teF) + df23Edit(df23Bus), df23Edit(df23Bus) + df23Edit(teF)} {
		err := inspectDiff(diff, a, rec, df23Candidate)
		if err == nil || !strings.Contains(err.Error(), df23Bus) || strings.Contains(err.Error(), teF) {
			t.Errorf("the mixed candidate was not refused on the ungranted path alone: %v", err)
		}
	}
	if err := inspectDiff(df23Edit(teF), a, rec, df23Candidate); err != nil {
		t.Errorf("the granted mutation alone was refused: %v", err)
	}
}

// W1/W7 AT CANDIDATE ADMISSION (review f3). The real governed loop
// (runCandidate) over a real repository: the pinned world holds the two
// objective-61 tests, the operative plan attempt is routed through the
// production transition at that world, and the candidate edits the tests
// beside the worker's own change. Admission must end with the path-specific
// test-edit refusal BEFORE any audit or review -- under an explicit empty
// grant record, and under a record granting one of the two (complete
// enumeration). The control: with only the granted test edited, admission
// passes inspection and reaches the review.
func TestDF23CandidateAdmissionRefusesAnUngrantedExistingTestEdit(t *testing.T) {
	const busSrc = "package event\n\nimport \"testing\"\n\nfunc TestBus(t *testing.T) {}\n"
	const receiptSrc = "package runreceipt\n\nimport \"testing\"\n\nfunc TestReceipt(t *testing.T) {}\n"
	edit := func(src string) string {
		return strings.Replace(src, "(t *testing.T) {}", "(t *testing.T) { t.Log(1) }", 1)
	}

	type outcome struct {
		err             error
		changed, beyond bool
	}
	admit := func(t *testing.T, granted []string, edited ...string) outcome {
		t.Helper()
		h := newGateHarness(t, roles.Policy{}, roles.Fresh, string(roles.Accept))
		e := h.engine
		e.Store = sessionStore(t)
		commitFixtureFile(t, h.work, df23Bus, busSrc)
		world := commitFixtureFile(t, h.work, df23Receipt, receiptSrc)
		h.tc.Identity = candidateIdentityWithBase(world)
		df23Pin(t, e, "task-1", world)

		// A grant as routing records one: the file's facts at the pinned world.
		var grants []testEditGrant
		for _, p := range granted {
			facts, err := testFacts([]byte(map[string]string{df23Bus: busSrc, df23Receipt: receiptSrc}[p]))
			if err != nil {
				t.Fatal(err)
			}
			grants = append(grants, testEditGrant{Path: p, World: world, Facts: facts})
		}
		d := attemptPlan("objective 61", "main.go", df23Bus, df23Receipt)
		a, err := e.beginPlanAttempt("task-1", "task", d)
		if err != nil {
			t.Fatal(err)
		}
		if a.World != world {
			t.Fatalf("premise: the attempt was derived at the candidate's base, got %q", a.World)
		}
		if err := e.recordTestEditGrants("task-1", "recorded", testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants}); err != nil {
			t.Fatal(err)
		}
		e.setTestEditGrants("task-1", grants)
		if err := e.recordProspectiveGrants("task-1", "recorded", prospectiveRecord{PlanAttemptID: a.ID, World: a.World}); err != nil {
			t.Fatal(err)
		}
		e.setProspectiveGrants("task-1", nil)
		if _, err := e.adoptPlanAttempt("task-1", "task", d); err != nil {
			t.Fatal(err)
		}
		for _, p := range edited {
			src := map[string]string{df23Bus: busSrc, df23Receipt: receiptSrc}[p]
			commitFixtureFile(t, h.work, p, edit(src))
		}
		h.tc.Files = []string{"main.go", df23Bus, df23Receipt}

		_, _, _, _, err = e.runCandidate(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
			"Rewrite main.go so it prints a number.", h.worker, h.work, "")
		var o outcome
		o.err = err
		for _, ev := range drainEvents(h.events) {
			switch ev.Kind {
			case event.CandidateChanged:
				o.changed = true
			case event.CandidateAudited, event.ReviewStarted, event.ReviewCompleted:
				o.beyond = true
			}
		}
		return o
	}
	refused := func(name string, o outcome, path string) {
		t.Helper()
		if !o.changed {
			t.Fatalf("%s: premise: the loop never produced a candidate: %v", name, o.err)
		}
		if o.err == nil || !strings.HasPrefix(o.err.Error(), "test edit refuted:") || !strings.Contains(o.err.Error(), path) ||
			!strings.Contains(o.err.Error(), "holds no test-edit grant") {
			t.Errorf("%s: candidate admission did not refuse the ungranted edit of %s: %v", name, path, o.err)
		}
		if o.beyond {
			t.Errorf("%s: the candidate reached audit or review before the test-edit refusal", name)
		}
	}

	refused("empty grant record, receipt", admit(t, nil, df23Receipt), df23Receipt)
	refused("empty grant record, bus", admit(t, nil, df23Bus), df23Bus)
	mixed := admit(t, []string{df23Bus}, df23Bus, df23Receipt)
	refused("mixed", mixed, df23Receipt)
	if mixed.err != nil && strings.Contains(mixed.err.Error(), df23Bus) {
		t.Errorf("mixed: the granted edit was named in the refusal: %v", mixed.err)
	}
	control := admit(t, []string{df23Bus}, df23Bus)
	if control.err != nil && strings.Contains(control.err.Error(), "test edit refuted") {
		t.Errorf("control: the granted edit was refused at admission: %v", control.err)
	}
	if !control.beyond {
		t.Errorf("control: the granted candidate never reached audit or review: %v", control.err)
	}
}

// f1 -- A TEST WHOSE NAME GIT QUOTES IS STILL ENUMERATED. Against a real
// repository, through the frozen capture runCandidate inspects and the state
// it hands over (candidateTestStateAt): an existing test whose name holds a
// tab renders under a quoted `diff --git "a/..." "b/..."` header, which the
// rendered-diff reading never saw. Edited, deleted, renamed away, and edited
// beside a granted test, it is refused without a grant -- and the same
// mutations under a grant for it are judged by the unchanged in-place rules.
func TestDF23AQuotedExistingTestPathIsEnumeratedFromTheCapture(t *testing.T) {
	ctx := context.Background()
	const quoted = "modfile/rule\tquoted_test.go"
	const moved = "modfile/moved_test.go"
	good := strings.Replace(teFSrc, "TestX", "TestY", 1)
	repo, empty := mintRepo(t)
	commitFixtureFile(t, repo.Root, teS, teSSrc)
	commitFixtureFile(t, repo.Root, teF, teFSrc)
	world := commitFixtureFile(t, repo.Root, quoted, teFSrc)

	grantFor := func(paths ...string) testEditRecord {
		var gs []testEditGrant
		for _, p := range paths {
			g := teEditGrants(t)[0]
			g.Path, g.World = p, world
			gs = append(gs, g)
		}
		a := teAttemptAt(t, "the quoted-path plan", world)
		return testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: gs}
	}
	// candidateAt cuts a worktree at commit, applies files on top, and
	// captures it against the pinned world exactly as runCandidate does.
	candidateAt := func(name, commit string, files map[string]string) candidateTestState {
		t.Helper()
		path, err := repo.CreateWorktreeAt(ctx, name, commit)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = repo.RemoveWorktree(ctx, path) })
		for rel, src := range files {
			commitFixtureFile(t, path, rel, src)
		}
		wt := repo
		wt.Root = path
		capture, err := wt.CandidateCapture(ctx, world, nil)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(capture.Diff, `"a/modfile/rule\tquoted_test.go"`) {
			t.Fatalf("premise: Git does not quote the test's name in the rendered diff:\n%s", capture.Diff)
		}
		return candidateTestStateAt(ctx, path, world, capture.Tree, capture.Paths, capture.Diff)
	}
	inspect := func(state candidateTestState, rec testEditRecord) error {
		return inspectTestEdits(state, teAttemptAt(t, "the quoted-path plan", world), rec)
	}
	refusedOn := func(name string, err error, verb string) {
		t.Helper()
		if err == nil || !strings.HasPrefix(err.Error(), "test edit refuted:") || !strings.Contains(err.Error(), verb+" the existing test "+quoted) ||
			!strings.Contains(err.Error(), "holds no test-edit grant") || strings.Contains(err.Error(), teF+",") {
			t.Errorf("%s: the quoted existing test was not refused on its own path: %v", name, err)
		}
	}

	edited := candidateAt("quoted-edit", world, map[string]string{quoted: good})
	refusedOn("edit", inspect(edited, grantFor(teF)), "edits")
	if err := inspect(edited, grantFor(quoted)); err != nil {
		t.Errorf("edit: the granted quoted test's in-grant edit was refused: %v", err)
	}

	// The candidate at the pre-world commit has deleted all three files.
	deleted := candidateAt("quoted-delete", empty, nil)
	refusedOn("delete", inspect(deleted, grantFor(teF)), "deletes")
	if err := inspect(deleted, grantFor(quoted, teF)); err == nil || !strings.Contains(err.Error(), "deletes it") {
		t.Errorf("delete: deleting the granted quoted test was admitted: %v", err)
	}

	// Renamed away: absent from the candidate, its bytes at another path.
	renamed := candidateAt("quoted-rename", empty, map[string]string{teS: teSSrc, teF: teFSrc, moved: teFSrc})
	refusedOn("rename source", inspect(renamed, grantFor(teF)), "deletes")
	if err := inspect(renamed, grantFor(quoted)); err == nil || !strings.Contains(err.Error(), quoted) {
		t.Errorf("rename source: renaming the granted quoted test away was admitted: %v", err)
	}

	// Mixed: the granted test's edit does not narrow the enumeration.
	mixed := candidateAt("quoted-mixed", world, map[string]string{teF: good, quoted: good})
	refusedOn("mixed", inspect(mixed, grantFor(teF)), "edits")
	if err := inspect(mixed, grantFor(teF, quoted)); err != nil {
		t.Errorf("mixed: both granted edits were refused: %v", err)
	}
}

// DF-37b -- A DECLARED EXISTING-TEST EDIT IS AN ADMISSION OBLIGATION.
//
// Objective 61, run 3: the plan's test_edits declared internal/event/bus_test.go
// and internal/runreceipt/receipt_test.go, routing recorded "no test-edit
// authority" for both, and the plan was admitted anyway -- the projector checks
// only the declarations a grant answers. These witnesses are separate from the
// objective 60, 63 and 64 witnesses above, which are unchanged.

const (
	df37bBusS     = "internal/event/bus.go"
	df37bReceiptS = "internal/runreceipt/receipt.go"
)

// df37bFiles is the pinned world of the objective-61 plan: each test beside
// its production file, in the same package.
func df37bFiles() map[string]string {
	return map[string]string{
		df37bBusS:     "package event\n\nfunc Publish() {}\n",
		df23Bus:       "package event\n\nimport \"testing\"\n\nfunc TestBus(t *testing.T) {}\n",
		df37bReceiptS: "package runreceipt\n\nfunc Mint() {}\n",
		df23Receipt:   "package runreceipt\n\nimport \"testing\"\n\nfunc TestReceipt(t *testing.T) {}\n",
	}
}

// df37bPlan is the objective-61 run-3 plan: both tests declared as edits.
func df37bPlan(text string) architectureDecision {
	d := attemptPlan(text, df37bBusS, df23Bus, df37bReceiptS, df23Receipt)
	d.TestEdits = []TestEditDeclaration{
		{Path: df23Bus, Operation: testEditOperationEdit, Package: "event"},
		{Path: df23Receipt, Operation: testEditOperationEdit, Package: "runreceipt"},
	}
	return d
}

// df37bGrants derives the grants the predicate gives the plan at teWorld when
// the given production files are governed -- none when nothing is.
func df37bGrants(t *testing.T, governed ...string) ([]testEditGrant, []string) {
	t.Helper()
	var covered []CoverageAnchor
	for _, f := range governed {
		covered = append(covered, CoverageAnchor{File: f, Requirement: RequirementMutationConfinement})
	}
	return testEditGrants(context.Background(), teWorld, df37bPlan("").Files, covered, authoredEvidence{}, teRead(df37bFiles()))
}

// df37bAdmit begins d's attempt at the pinned world, records its complete
// test-edit state exactly as routing does, and asks plan admission.
func df37bAdmit(t *testing.T, e *Engine, task string, d architectureDecision, grants []testEditGrant) (planAttempt, error) {
	t.Helper()
	a, err := e.beginPlanAttempt(task, attemptObjective, d)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.recordTestEditGrants(task, "recorded", testEditRecord{PlanAttemptID: a.ID, World: a.World, Grants: grants}); err != nil {
		t.Fatal(err)
	}
	e.setTestEditGrants(task, grants)
	return a, e.reconcileTestEditGrants(task, d)
}

func df37bRefusesNaming(t *testing.T, err error, ungranted string, granted ...string) {
	t.Helper()
	var refusal *planAdmissionRefusal
	if err == nil || !errors.As(err, &refusal) {
		t.Fatalf("the plan was not refused at plan admission: %v", err)
	}
	if !strings.Contains(err.Error(), "refused before implementation") || !strings.Contains(err.Error(), ungranted) {
		t.Fatalf("the refusal does not name the ungranted %s: %v", ungranted, err)
	}
	for _, g := range granted {
		if strings.Contains(err.Error(), g) {
			t.Fatalf("the refusal names the granted %s: %v", g, err)
		}
	}
}

// W1 -- the objective-61 run-3 specimen: both declared tests receive explicit
// no-authority results, and the plan is refused before an implementer starts.
func TestDF37bW1DeclaredTestEditsWithNoAuthorityAreRefusedAtAdmission(t *testing.T) {
	grants, reasons := df37bGrants(t)
	if len(grants) != 0 || len(reasons) != 2 {
		t.Fatalf("premise: both declared tests are refused authority explicitly: grants %+v, reasons %q", grants, reasons)
	}
	for i, f := range []string{df23Bus, df23Receipt} {
		if !strings.Contains(reasons[i], f) || !strings.Contains(reasons[i], "no planned file in its directory is governed") {
			t.Fatalf("premise: no explicit no-authority result for %s: %q", f, reasons)
		}
	}
	const task = "task-df37b-w1"
	e := df23Engine(t, "", task)
	_, err := df37bAdmit(t, e, task, df37bPlan("objective 61 run 3"), grants)
	df37bRefusesNaming(t, err, df23Bus)
	if !strings.Contains(err.Error(), "holds no test-edit grant") {
		t.Fatalf("the refusal does not state the reason: %v", err)
	}
	// The projector alone admits it: that was the hole.
	if err := projectTestEditRefusals(df37bPlan("").TestEdits, grants); err != nil {
		t.Fatalf("premise: the projector refuses nothing ungranted: %v", err)
	}
	// A plan with no declared existing-test edit is untouched, and so is one
	// whose only entries declare nothing (no operation in the closed vocabulary).
	plain := df37bPlan("no test edits")
	plain.TestEdits = nil
	if _, err := df37bAdmit(t, e, task, plain, nil); err != nil {
		t.Fatalf("a plan declaring no existing-test edit was refused: %v", err)
	}
	unread := df37bPlan("unread operations")
	unread.TestEdits = []TestEditDeclaration{{Path: df23Bus, Operation: "Edit"}, {Path: df23Receipt}}
	if _, err := df37bAdmit(t, e, task, unread, nil); err != nil {
		t.Fatalf("declarations outside the closed vocabulary were read as edits: %v", err)
	}
	// Declarations that disagree still declare the path.
	disagree := df37bPlan("disagreeing")
	disagree.TestEdits = append(disagree.TestEdits, TestEditDeclaration{Path: "./" + df23Bus, Operation: testEditOperationDelete})
	_, err = df37bAdmit(t, e, task, disagree, nil)
	df37bRefusesNaming(t, err, df23Bus)
}

// W2 -- control: the same plan with valid grants for both proceeds; a
// malformed or duplicated grant does not count as one.
func TestDF37bW2DeclaredTestEditsWithValidGrantsProceed(t *testing.T) {
	grants, reasons := df37bGrants(t, df37bBusS, df37bReceiptS)
	if len(grants) != 2 || len(reasons) != 0 {
		t.Fatalf("premise: both tests are granted: grants %+v, reasons %q", grants, reasons)
	}
	const task = "task-df37b-w2"
	e := df23Engine(t, "", task)
	if _, err := df37bAdmit(t, e, task, df37bPlan("objective 61 run 3, granted"), grants); err != nil {
		t.Fatalf("a plan whose declared test edits are all granted was refused: %v", err)
	}
	malformed := append([]testEditGrant{}, grants...)
	malformed[1].BaseHash = ""
	_, err := df37bAdmit(t, e, task, df37bPlan("malformed"), malformed)
	df37bRefusesNaming(t, err, df23Receipt)
	if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("a malformed grant was not refused as malformed: %v", err)
	}
	_, err = df37bAdmit(t, e, task, df37bPlan("duplicated"), append(grants, grants[0]))
	df37bRefusesNaming(t, err, df23Bus)
	if !strings.Contains(err.Error(), "not exactly one") {
		t.Fatalf("a duplicated grant was not refused: %v", err)
	}
	elsewhere := append([]testEditGrant{}, grants...)
	elsewhere[0].World = "other-world"
	_, err = df37bAdmit(t, e, task, df37bPlan("other world"), elsewhere)
	df37bRefusesNaming(t, err, df23Bus)
}

// W3 -- one granted and one ungranted declared edit: refused, naming only the
// ungranted path.
func TestDF37bW3OnlyTheUngrantedDeclaredPathIsNamed(t *testing.T) {
	grants, _ := df37bGrants(t, df37bBusS)
	if len(grants) != 1 || grants[0].Path != df23Bus {
		t.Fatalf("premise: only %s is granted: %+v", df23Bus, grants)
	}
	const task = "task-df37b-w3"
	e := df23Engine(t, "", task)
	_, err := df37bAdmit(t, e, task, df37bPlan("one granted"), grants)
	df37bRefusesNaming(t, err, df23Receipt, df23Bus)
}

// W4 -- a grant recorded under a superseded PlanAttemptID does not satisfy the
// replacement plan's declaration; the replacement's own fresh grant does.
func TestDF37bW4ASupersededAttemptsGrantDoesNotAdmitTheReplacement(t *testing.T) {
	grants, _ := df37bGrants(t, df37bBusS, df37bReceiptS)
	const task = "task-df37b-w4"
	e := df23Engine(t, "", task)
	a, err := df37bAdmit(t, e, task, df37bPlan("plan A"), grants)
	if err != nil {
		t.Fatalf("premise: plan A is admitted under its own grants: %v", err)
	}
	b, err := df37bAdmit(t, e, task, df37bPlan("plan B"), nil)
	if a.ID == b.ID {
		t.Fatal("premise: the replacement plan is a new attempt")
	}
	df37bRefusesNaming(t, err, df23Bus)
	// Plan A's record, handed to plan B whole, is bound to the superseded attempt.
	_, stale := e.recordedGrants(task, a.ID)
	if len(stale.Grants) != 2 {
		t.Fatalf("premise: plan A's record still holds its grants: %+v", stale)
	}
	err = reconcileTestEditDeclarations(df37bPlan("plan B").TestEdits, df37bPlan("plan B").Files, b, stale)
	if err == nil || !strings.Contains(err.Error(), "superseded") || !strings.Contains(err.Error(), df23Bus) {
		t.Fatalf("a superseded attempt's grants admitted the replacement plan: %v", err)
	}
	// A replacement that derives its grants fresh under its own attempt is admitted.
	if _, err := df37bAdmit(t, e, task, df37bPlan("plan C"), grants); err != nil {
		t.Fatalf("the replacement's own fresh grants were refused: %v", err)
	}
}

// The door is in routePlan, after every grant the plan operates under is
// recorded -- the authored ones included -- and with the projector beside it.
func TestDF37bRoutePlanReconcilesAfterTheCompleteGrantState(t *testing.T) {
	calls := callsIn(funcDeclIn(t, "internal/workflow/engine.go", "routePlan").Body)
	reconcile, ok := calls["reconcileTestEditGrants"]
	if !ok {
		t.Fatal("routePlan does not reconcile declared test edits against recorded grants")
	}
	if authored, ok := calls["authoredTestEditGrants"]; !ok || reconcile < authored {
		t.Fatal("routePlan reconciles before the authored grants are recorded")
	}
	if project, ok := calls["projectTestEditRefusals"]; !ok || reconcile > project {
		t.Fatal("routePlan projects structural refusals before it establishes each declared edit is granted")
	}
}

// DF-39 -- AN UNPLANNED EXISTING-PRODUCTION EDIT IS REFUSED.
//
// Objective 61, run 6: the operative plan omitted
// internal/runreceipt/legacy/fromevents.go, the candidate modified it, and the
// candidate went through validation and review instead of being refused.
// Candidate inspection now enumerates every existing production Go file the
// candidate mutates from the frozen capture, and each must be named by the
// Files of the operative plan attempt. These witnesses are separate from the
// DF-23, DF-37 and DF-37b witnesses above, which are unchanged.

const (
	df39A          = "internal/workflow/engine.go"
	df39B          = "internal/workflow/gate.go"
	df39FromEvents = "internal/runreceipt/legacy/fromevents.go"
)

// df39Attempt is a canonical plan attempt at teWorld whose plan names files.
func df39Attempt(t *testing.T, files ...string) planAttempt {
	t.Helper()
	d := attemptPlan("objective 67", files...)
	id, err := planAttemptID("t", attemptObjective, teWorld, PlanByArchitect, "", d)
	if err != nil {
		t.Fatal(err)
	}
	return planAttempt{ID: id, TaskID: "t", World: teWorld, PlanSource: PlanByArchitect, Plan: d}
}

func df39Scope(diff string, operative planAttempt) error {
	return inspectProductionScope(teDiffState(diff, df23Candidate), operative)
}

func df39Created(p string) string {
	return "diff --git a/" + p + " b/" + p + "\nnew file mode 100644\n--- /dev/null\n+++ b/" + p + "\n@@ -0,0 +1 @@\n+package x\n"
}

func df39Deleted(p string) string {
	return "diff --git a/" + p + " b/" + p + "\ndeleted file mode 100644\n--- a/" + p + "\n+++ /dev/null\n@@ -1 +0,0 @@\n-package x\n"
}

func df39Renamed(from, to string) string {
	return "diff --git a/" + from + " b/" + to + "\nsimilarity index 100%\nrename from " + from + "\nrename to " + to + "\n"
}

// df39Refused asserts err is the production-scope refusal naming exactly the
// mutation verb+" "+path, and naming none of clear.
func df39Refused(t *testing.T, name string, err error, verb, p string, clear ...string) {
	t.Helper()
	if err == nil || !strings.HasPrefix(err.Error(), "production scope refuted:") || !strings.Contains(err.Error(), verb+" "+p) {
		t.Errorf("%s: the unplanned production %s of %s was not refused by path: %v", name, verb, p, err)
		return
	}
	for _, c := range clear {
		if strings.Contains(err.Error(), c) {
			t.Errorf("%s: the refusal named %s, which is not unauthorized: %v", name, c, err)
		}
	}
}

// W1 -- the plan names A; the candidate edits A and the existing B. B is
// refused by its exact path, and A is not named.
func TestDF39W1AnExtraExistingProductionFileIsRefusedByPath(t *testing.T) {
	err := df39Scope(df23Edit(df39A)+df23Edit(df39B), df39Attempt(t, df39A))
	df39Refused(t, "W1", err, "edits", df39B, "edits "+df39A)
	if err != nil && !strings.Contains(err.Error(), "does not name it") {
		t.Errorf("W1: the refusal does not say the operative plan omits the path: %v", err)
	}
}

// W2 -- the planned control: the plan names A and B, in any spelling the
// architect may write, and the same candidate is not refused. A planned file
// the candidate leaves unchanged refuses nothing either.
func TestDF39W2PlannedExistingProductionEditsAreNotRefused(t *testing.T) {
	diff := df23Edit(df39A) + df23Edit(df39B)
	for name, files := range map[string][]string{
		"exact":                 {df39A, df39B},
		"unnormal spelling":     {" ./" + df39A, "internal//workflow/gate.go "},
		"planned but untouched": {df39A, df39B, df39FromEvents},
	} {
		if err := df39Scope(diff, df39Attempt(t, files...)); err != nil {
			t.Errorf("%s: planned production edits were refused: %v", name, err)
		}
	}
}

// W3 -- an existing *_test.go stays the test-edit inspection's: the
// production rule neither refuses it nor admits it, and the DF-23 refusal of
// the ungranted edit is unchanged.
func TestDF39W3AnExistingTestEditIsNotReclassifiedAsProduction(t *testing.T) {
	diff := df23Edit(df39A) + df23Edit(df23Receipt)
	a := df39Attempt(t, df39A)
	if err := df39Scope(diff, a); err != nil {
		t.Fatalf("the production-scope rule judged an existing test edit: %v", err)
	}
	err := inspectDiff(diff, a, testEditRecord{PlanAttemptID: a.ID, World: a.World}, df23Candidate)
	if err == nil || !strings.HasPrefix(err.Error(), "test edit refuted:") || !strings.Contains(err.Error(), "edits the existing test "+df23Receipt) ||
		!strings.Contains(err.Error(), "holds no test-edit grant") {
		t.Errorf("the ungranted existing test edit is no longer governed by the test-edit inspection: %v", err)
	}
	// Planning the test does not make it production scope's to admit either.
	if err := inspectDiff(diff, df39Attempt(t, df39A, df23Receipt), testEditRecord{}, df23Candidate); err == nil {
		t.Error("naming an existing test in the plan stood in for its test-edit grant")
	}
}

// W4 -- a created production file is not an existing-production edit,
// planned or not, and continues to the prospective-create inspection, which
// still refutes it against its (absent) grant.
func TestDF39W4ACreatedProductionFileContinuesToProspectiveInspection(t *testing.T) {
	const created = "internal/workflow/df39new.go"
	diff := df23Edit(df39A) + df39Created(created)
	if err := df39Scope(diff, df39Attempt(t, df39A)); err != nil {
		t.Fatalf("a created production file was judged as an existing-production edit: %v", err)
	}
	decl := []ProspectiveSurface{{Path: created, Package: "x", Role: "go-existing-package", Covering: df39A}}
	if err := inspectProspectiveGrants(diff, decl, nil); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
		t.Errorf("the created file did not reach prospective-create authority: %v", err)
	}
}

// W5 -- deleting an existing unplanned production file is refused; deleting
// a planned one is not refused for scope.
func TestDF39W5DeletingAnUnplannedExistingProductionFileIsRefused(t *testing.T) {
	diff := df23Edit(df39A) + df39Deleted(df39B)
	df39Refused(t, "W5", df39Scope(diff, df39Attempt(t, df39A)), "deletes", df39B, df39A)
	if err := df39Scope(diff, df39Attempt(t, df39A, df39B)); err != nil {
		t.Errorf("W5: deleting a planned production file was refused for scope: %v", err)
	}
}

// W6 -- renaming an existing production file whose SOURCE the plan does not
// name is refused on the source, whether or not the destination is planned.
// With the source planned, scope refuses nothing, and the destination -- a
// creation -- is left to prospective semantics, not judged here.
func TestDF39W6RenamingAnUnplannedExistingProductionFileIsRefusedOnItsSource(t *testing.T) {
	const dest = "internal/workflow/gate_moved.go"
	diff := df23Edit(df39A) + df39Renamed(df39B, dest)
	df39Refused(t, "W6", df39Scope(diff, df39Attempt(t, df39A)), "renames", df39B, df39A, dest)
	df39Refused(t, "W6 destination planned", df39Scope(diff, df39Attempt(t, df39A, dest)), "renames", df39B, df39A, dest)
	if err := df39Scope(diff, df39Attempt(t, df39A, df39B)); err != nil {
		t.Errorf("W6: renaming a planned production file was refused for scope: %v", err)
	}
	decl := []ProspectiveSurface{{Path: dest, Package: "x", Role: "go-existing-package", Covering: df39A}}
	if err := inspectProspectiveGrants(diff, decl, nil); err == nil || !strings.HasPrefix(err.Error(), "prospective surface refuted:") {
		t.Errorf("W6: the rename destination did not reach prospective-create authority: %v", err)
	}
}

// W7 -- a re-plan through the production attempt transition: attempt A plans
// B, attempt C omits it, and the candidate under C that modifies B is refused
// although A once planned it. Re-planning B again (attempt D) authorizes it.
func TestDF39W7APathPlannedOnlyByASupersededAttemptIsRefused(t *testing.T) {
	const task = "task-df39-replan"
	e := df23Engine(t, "", task)
	diff := df23Edit(df39A) + df23Edit(df39B)
	inspect := func() error {
		return inspectProductionScope(teDiffState(diff, df23Candidate), e.operativePlanAttempt(task))
	}
	a := df23Route(t, e, task, attemptPlan("plan A", df39A, df39B), nil)
	if err := inspect(); err != nil {
		t.Fatalf("premise: attempt A authorizes its own planned edit of %s: %v", df39B, err)
	}
	c := df23Route(t, e, task, attemptPlan("plan C", df39A), nil)
	if op := e.operativePlanAttempt(task); op.ID != c.ID || c.ID == a.ID {
		t.Fatalf("premise: the re-plan made C operative (a=%s c=%s operative=%s)", short12(a.ID), short12(c.ID), short12(op.ID))
	}
	df39Refused(t, "W7", inspect(), "edits", df39B, "edits "+df39A)
	df23Route(t, e, task, attemptPlan("plan D", df39A, df39B), nil)
	if err := inspect(); err != nil {
		t.Errorf("W7: the operative attempt's own plan of %s was refused: %v", df39B, err)
	}
}

// No operative attempt, or one pinned at another world, authorizes no
// existing production edit -- planned or not. A candidate that mutates no
// existing production file needs no plan to say so.
func TestDF39WithoutAnOperativeAttemptAtTheBaseNoProductionEditIsAuthorized(t *testing.T) {
	diff := df23Edit(df39A)
	if err := df39Scope(diff, planAttempt{}); err == nil || !strings.HasPrefix(err.Error(), "production scope refuted:") ||
		!strings.Contains(err.Error(), "no operative plan attempt") || !strings.Contains(err.Error(), df39A) {
		t.Errorf("an edit with no operative plan attempt was not refused: %v", err)
	}
	elsewhere := df39Attempt(t, df39A)
	elsewhere.World = "another-world"
	if err := df39Scope(diff, elsewhere); err == nil || !strings.Contains(err.Error(), "not the candidate's base") {
		t.Errorf("an attempt pinned at another world authorized the edit: %v", err)
	}
	if err := df39Scope(df39Created("internal/workflow/df39new.go")+df23Edit(df23Receipt), planAttempt{}); err != nil {
		t.Errorf("a candidate mutating no existing production file was refused for scope: %v", err)
	}
}

// W8 -- THE OBJECTIVE-61 REGRESSION, in the real governed loop (runCandidate)
// over a real repository: fromevents.go exists at the pinned world, the
// operative plan routed through the production transition omits it, and the
// candidate modifies it beside the worker's planned main.go. The loop must end
// with the production-scope refusal naming fromevents.go BEFORE any audit or
// review, so ACCEPT is never reachable. The control plans it and reaches the
// review.
func TestDF39W8TheObjective61UnplannedFromEventsEditIsRefusedBeforeReview(t *testing.T) {
	const fromEventsSrc = "package legacy\n\nfunc FromEvents() int { return 0 }\n"
	type outcome struct {
		result          candidateOutcome
		err             error
		changed, beyond bool
	}
	run := func(t *testing.T, planned ...string) outcome {
		t.Helper()
		h := newGateHarness(t, roles.Policy{}, roles.Fresh, string(roles.Accept))
		e := h.engine
		world := commitFixtureFile(t, h.work, df39FromEvents, fromEventsSrc)
		h.tc.Identity = candidateIdentityWithBase(world)
		h.tc.Files = append([]string{"main.go"}, planned...)
		adoptFixturePlanAttempt(t, e, "task-1", h.tc.Task, world, "Rewrite main.go so it prints a number.", h.tc.Files, nil)
		commitFixtureFile(t, h.work, df39FromEvents, strings.Replace(fromEventsSrc, "return 0", "return 1", 1))

		var o outcome
		o.result, _, _, _, o.err = e.runCandidate(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
			"Rewrite main.go so it prints a number.", h.worker, h.work, "")
		for _, ev := range drainEvents(h.events) {
			switch ev.Kind {
			case event.CandidateChanged:
				o.changed = true
			case event.CandidateAudited, event.ReviewStarted, event.ReviewCompleted:
				o.beyond = true
			}
		}
		return o
	}

	refused := run(t)
	if !refused.changed {
		t.Fatalf("premise: the loop never produced a candidate: %v", refused.err)
	}
	df39Refused(t, "W8", refused.err, "edits", df39FromEvents, "main.go")
	if refused.beyond {
		t.Error("W8: the unplanned fromevents.go edit reached audit or review before it was refused")
	}
	if refused.result.Accepted() {
		t.Error("W8: the candidate with the unplanned fromevents.go edit was accepted")
	}

	control := run(t, df39FromEvents)
	if control.err != nil && strings.Contains(control.err.Error(), "production scope refuted") {
		t.Errorf("control: the planned fromevents.go edit was refused for scope: %v", control.err)
	}
	if !control.beyond {
		t.Errorf("control: the planned candidate never reached audit or review: %v", control.err)
	}
}

// The DF-39 refusal holds in the OUTER loop, not only in runCandidate: with a
// second implementor configured, Engine.implement must not ask that
// implementor or create a handoff for the refused candidate. W8 drives
// runCandidate directly, so it cannot see this.
//
// AMENDED FOR ROUTING ONLY (Objective 74, DF-48): the refusal no longer ends
// the run itself; it is recorded, by path, and returned to the architect,
// who here stops. The refusal and the no-handoff, no-second-implementor
// assertions are unchanged.
func TestDF39AProductionScopeRefusalReachesNoSecondImplementorAndNoHandoff(t *testing.T) {
	const fromEventsSrc = "package legacy\n\nfunc FromEvents() int { return 0 }\n"
	h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, string(roles.Accept))
	e := h.engine
	world := commitFixtureFile(t, h.work, df39FromEvents, fromEventsSrc)
	h.tc.Identity = candidateIdentityWithBase(world)
	adoptFixturePlanAttempt(t, e, "task-1", h.tc.Task, world, "Rewrite main.go so it prints a number.", h.tc.Files, nil)
	commitFixtureFile(t, h.work, df39FromEvents, strings.Replace(fromEventsSrc, "return 0", "return 1", 1))

	// The second implementor is the same stub under another name, so if it
	// were asked it would produce a candidate; its turn is visible as an
	// agent start from its own source.
	second := h.worker
	second.Name = "codex"
	e.Config.Implementors = append(e.Config.Implementors, second)
	architect := &scriptedArchitect{turns: []architectTurn{{text: df48Stop}}}
	e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "chatgpt", "true", "none"
	e.Runners = implementerResolver{architect: architect, session: "session-1",
		reviewer: answeringRunner{text: `{"decision":"accept","summary":"the candidate stands"}`, mode: roles.Unverified}}

	var failed error
	e.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
		"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
	seen := drainEvents(h.events)

	starts := map[event.Source]int{}
	for _, ev := range seen {
		if ev.Kind == event.AgentStarted {
			starts[ev.Source]++
		}
	}
	if starts[event.SourceClaude] != 1 {
		t.Fatalf("premise: the first implementor was asked %d times, want once: %v", starts[event.SourceClaude], failed)
	}
	refusals := refusalsIn(t, seen)
	if len(refusals) != 1 || refusals[0].Class != refusalProductionScope || len(architect.prompts) != 1 {
		t.Fatalf("the production-scope refusal was not recorded and returned to the architect: %+v (%d architect turns, %v)",
			refusals, len(architect.prompts), failed)
	}
	df39Refused(t, "outer loop", errors.New(refusals[0].Reason), "edits", df39FromEvents, "main.go")
	if starts[event.SourceCodex] != 0 {
		t.Error("a second implementor was asked after the production-scope refusal")
	}
	if contains(seen, event.HandoffCreated) {
		t.Errorf("the production-scope refusal created an implementer handoff: %v", kinds(seen))
	}
	if failed != nil && strings.Contains(failed.Error(), "no bounded implementor produced") {
		t.Errorf("the refusal was reported as implementors failing rather than as itself: %v", failed)
	}
}

// OBJECTIVE 61 -- AN EXISTING-TEST EDIT ADMISSION REFUSAL TAKES THE SAME
// TYPED PLAN-ADMISSION CONTINUATION. The DF-37b and DF-23 witnesses above are
// unchanged; these consume them.

// obj61W4Refused edits the existing test beside main.go, which routing grants,
// but declares an import the pinned world's test does not carry: refused by
// projectTestEditRefusals at plan admission.
const obj61W4Refused = `{"decision":"proceed","summary":"edit main","plan":"plan A: edit main.go and its test with a new import","files":["main.go","main_test.go"],"mode":"modify",` +
	`"test_edits":[{"path":"main_test.go","operation":"edit","package":"main","build_constraints":[],"imports":["testing","os"]}]}`

// W4 (Objective 61): COMMON ADMISSION PATH. A projected existing-test edit
// refusal establishes only that the proposed plan is inadmissible, so it takes
// the same continuation a prospective refusal takes: no implementer starts,
// the refusal is returned to the architect, the replacement's authority is
// derived fresh under its own attempt, and its identical repetition parks.
func TestObj61W4AnExistingTestEditRefusalTakesTheSameContinuation(t *testing.T) {
	t.Run("returned, then replaced", func(t *testing.T) {
		const task = "task-obj61-w4"
		run, architect := obj61Run(t, sessionStore(t), task, obj61W4Refused, replanA)
		refusals := refusalsIn(t, run.events)
		attempts := startedAttempts(t, run.events)
		if len(refusals) != 1 || len(attempts) != 2 || refusals[0].PlanAttemptID != attempts[0] {
			t.Fatalf("premise: the edit plan refused once at admission, then replaced: attempts %v refusals %+v\n%s", attempts, refusals, gapLoopTrace(run.events))
		}
		r := refusals[0]
		if r.Class != refusalTestEditAdmission || !strings.Contains(r.Reason, "os") || !strings.Contains(string(r.Declaration), "main_test.go") {
			t.Fatalf("the projected refusal is not typed as an existing-test edit admission refusal: %+v", r)
		}
		if implementerStartedBefore(run.events, indexOfKind(run.events, event.PlanAttemptRefused)) {
			t.Fatal("an agent started before the existing-test edit refusal")
		}
		if len(architect.prompts) != 2 || !strings.Contains(architect.prompts[1], planAdmissionRefusedMarker) {
			t.Fatal("the existing-test edit refusal was not returned to the architect")
		}
		// The replacement holds its OWN grant, recorded under its own attempt.
		_, ed := run.engine.recordedGrants(task, attempts[1])
		if ed.PlanAttemptID != attempts[1] || len(ed.Grants) != 1 || ed.Grants[0].Path != "main_test.go" {
			t.Fatalf("the replacement's test-edit authority was not derived under its own attempt: %+v", ed)
		}
		if op := run.engine.operativePlanAttempt(task); op.ID != attempts[1] {
			t.Fatalf("the replacement did not become operative: %s", op.ID)
		}
	})
	t.Run("repeated, then parked", func(t *testing.T) {
		const task = "task-obj61-w4-repeat"
		run, architect := obj61Run(t, sessionStore(t), task, obj61W4Refused)
		if len(architect.prompts) != 2 || !hasKind(kindsOf(run.events), event.WorkflowPlanAdmissionRefused) ||
			hasKind(kindsOf(run.events), event.WorkflowFailed) || hasKind(kindsOf(run.events), event.PlanProposed) {
			t.Fatalf("the repeated existing-test edit refusal was not bounded (%d prompts):\n%s", len(architect.prompts), gapLoopTrace(run.events))
		}
	})
}

// obj61W6Plan is an objective-61-shaped plan requiring two existing-test
// edits: main_test.go beside main.go and other/other_test.go beside
// other/other.go.
const obj61W6Plan = `{"decision":"proceed","summary":"vocabulary","plan":"plan W6: edit two production files and the existing test beside each","files":["main.go","main_test.go","other/other.go","other/other_test.go"],"mode":"modify",` +
	`"test_edits":[{"path":"main_test.go","operation":"edit","package":"main","build_constraints":[],"imports":["testing"]},` +
	`{"path":"other/other_test.go","operation":"edit","package":"other","build_constraints":[],"imports":["testing"]}]}`

// W6 (Objective 61): TEST-AUTHORITY CONSUMPTION, through the production
// routing and architect path. Where the pinned world's governance derives a
// grant for main_test.go and none for other/other_test.go, the plan is refused
// at admission, its refusal reaches the architect, no implementer starts, and
// its identical repeat parks. Where the governance derives both, the SAME plan
// is admitted, operative, with both grants recorded under its PlanAttemptID.
// Every grant here is one the production coverage path derived and recorded;
// DF-23 policy is consumed, not altered.
func TestObj61W6UngrantedRequiredTestEditCannotReachImplementation(t *testing.T) {
	region := []string{"main.go", "main_test.go", "other/other.go", "other/other_test.go"}
	t.Run("one ungranted", func(t *testing.T) {
		const task = "task-obj61-w6-partial"
		run, architect := obj61CoveredRun(t, task, []string{"main.go"}, region, obj61W6Plan)
		attempts := startedAttempts(t, run.events)
		refusals := refusalsIn(t, run.events)
		if len(attempts) == 0 || len(refusals) != 1 || refusals[0].PlanAttemptID != attempts[0] {
			t.Fatalf("premise: the plan was routed and refused once at admission: attempts %v refusals %+v\n%s", attempts, refusals, gapLoopTrace(run.events))
		}
		_, ed := run.engine.recordedGrants(task, attempts[0])
		if len(ed.Grants) != 1 || ed.Grants[0].Path != "main_test.go" || ed.PlanAttemptID != attempts[0] {
			t.Fatalf("premise: routing derived exactly the main_test.go grant for the attempt: %+v", ed)
		}
		r := refusals[0]
		if r.Class != refusalTestEditAdmission || !strings.Contains(r.Reason, "other/other_test.go") || strings.Contains(r.Reason, "main_test.go") {
			t.Fatalf("the refusal does not name only the ungranted required edit: %+v", r)
		}
		if implementerStartedBefore(run.events, len(run.events)) {
			t.Fatalf("an implementer started under a plan with an ungranted required test edit:\n%s", gapLoopTrace(run.events))
		}
		if len(architect.prompts) != 2 || !strings.Contains(architect.prompts[1], planAdmissionRefusedMarker) || !strings.Contains(architect.prompts[1], "other/other_test.go") {
			t.Fatalf("the refusal did not reach the architect (%d prompts)", len(architect.prompts))
		}
		if !hasKind(kindsOf(run.events), event.WorkflowPlanAdmissionRefused) || hasKind(kindsOf(run.events), event.PlanProposed) {
			t.Fatalf("the identical ungranted plan was not parked, or became operative:\n%s", gapLoopTrace(run.events))
		}
	})
	t.Run("both granted", func(t *testing.T) {
		const task = "task-obj61-w6-full"
		run, architect := obj61CoveredRun(t, task, []string{"main.go", "other/other.go"}, region, obj61W6Plan)
		if len(refusalsIn(t, run.events)) != 0 || len(architect.prompts) != 1 {
			t.Fatalf("the fully granted plan was refused (%d prompts):\n%s", len(architect.prompts), gapLoopTrace(run.events))
		}
		proposed := indexOfKind(run.events, event.PlanProposed)
		if proposed < 0 {
			t.Fatalf("the fully granted plan did not become operative:\n%s", gapLoopTrace(run.events))
		}
		var p struct {
			PlanAttemptID string `json:"plan_attempt_id"`
		}
		if err := json.Unmarshal(run.events[proposed].Payload, &p); err != nil || p.PlanAttemptID == "" {
			t.Fatalf("the operative plan names no attempt: %v", err)
		}
		if op := run.engine.operativePlanAttempt(task); op.ID != p.PlanAttemptID {
			t.Fatalf("the operative attempt is not the admitted plan: %s", op.ID)
		}
		_, ed := run.engine.recordedGrants(task, p.PlanAttemptID)
		paths := map[string]bool{}
		for _, g := range ed.Grants {
			paths[g.Path] = true
		}
		if ed.PlanAttemptID != p.PlanAttemptID || len(ed.Grants) != 2 || !paths["main_test.go"] || !paths["other/other_test.go"] {
			t.Fatalf("both required grants are not recorded under the operative PlanAttemptID: %+v", ed)
		}
		if implementerStartedBefore(run.events, proposed) {
			t.Fatal("work started before the plan was admitted")
		}
		// Past admission the run goes on to implementation, whose first gate
		// in this rig is the candidate-worktree capability it does not grant:
		// the plan proceeded, and nothing parked or refused it.
		failed := indexOfKind(run.events, event.WorkflowFailed)
		if hasKind(kindsOf(run.events), event.WorkflowPlanAdmissionRefused) || failed < proposed ||
			!strings.Contains(run.events[failed].Summary, "candidate worktree capability") {
			t.Fatalf("the admitted plan did not proceed to implementation:\n%s", gapLoopTrace(run.events))
		}
	})
}

// ---------------------------------------------------------------------------
// OBJECTIVE 74 (DF-48) -- CANDIDATE SCOPE REFUSAL RETURNS TO ARCHITECTURE.
// The DF-39 refusal above is unchanged; these witnesses drive what happens
// AFTER it, through a real governed run (Engine.run) over a real repository
// with a certifying Sensei stub. The architect's first plan names main.go;
// the candidate edits main.go and the existing other.go -- the 70B2 r3 shape,
// one existing production file outside the operative plan.
// ---------------------------------------------------------------------------

const (
	df48Task     = "task-df48"
	df48Other    = "other.go"
	df48OtherSrc = "package main\n\nfunc other() int { return 0 }\n"
	// df48Revise objects with a real finding, so a candidate that reaches
	// review spends its one cycle and the run ends NOT_CONVERGED, resumable.
	df48Revise = `{"decision":"revise","summary":"the proof is missing","findings":[{"id":"1","severity":"blocking","class":"code",` +
		`"claim":"the test does not fail without the fix","reference":"main.go","reason":"no mutation"}]}`
	// df48Include names the refused path; df48Exclude does not; df48Narrow
	// names the refused path and omits the planned one.
	df48Include = `{"decision":"proceed","summary":"include other.go","plan":"rewrite main.go so it prints a number, keeping the other.go change","files":["main.go","other.go"],"mode":"modify"}`
	df48Exclude = `{"decision":"proceed","summary":"exclude other.go","plan":"rewrite main.go so it prints a number; other.go must not change","files":["main.go"],"mode":"modify"}`
	df48Narrow  = `{"decision":"proceed","summary":"other.go only","plan":"change other.go only","files":["other.go"],"mode":"modify"}`
	df48Stop    = `{"decision":"reply","message":"the objective stops here: other.go is outside what this task may change"}`
	// df48RefusedMarker is productionScopeRefusalPrompt's own heading.
	df48RefusedMarker = "CANDIDATE PRODUCTION SCOPE REFUSED"
)

// df48Rig is one governed run of the specimen and what it recorded.
type df48Rig struct {
	e         *Engine
	architect *scriptedArchitect
	world     string
	events    []event.Event
}

// df48Drive runs the specimen: the architect answers objectivePlan, then
// turns in order, then stops. With alternate, a second implementor is
// configured, so a handoff or a second implementor would be visible.
func df48Drive(t *testing.T, alternate bool, turns ...string) df48Rig {
	t.Helper()
	script := []architectTurn{{text: objectivePlan}}
	for _, turn := range turns {
		script = append(script, architectTurn{text: turn})
	}
	script = append(script, architectTurn{text: df48Stop})
	architect := &scriptedArchitect{turns: script}
	e, _ := objectiveEngine(t, t.TempDir(), objectiveRoles{architect: architect, reviewer: df48Revise, bindings: make(chan RunnerSpec, 64)})
	e.Config.Sensei.Args = []string{"-c", regionScript(objectiveRunScript, "main.go", df48Other)}
	world := commitFixtureFile(t, e.Repo.Root, df48Other, df48OtherSrc)
	// The task's candidate worktree, at the world the run will pin, already
	// holds the unplanned edit of the existing other.go; the implementer
	// then writes main.go beside it.
	work, err := e.Repo.CreateWorktreeAt(context.Background(), df48Task, world)
	if err != nil {
		t.Fatal(err)
	}
	commitFixtureFile(t, work, df48Other, strings.Replace(df48OtherSrc, "return 0", "return 1", 1))
	if alternate {
		second := e.Config.Implementors[0]
		second.Name = "gemini"
		e.Config.Implementors = append(e.Config.Implementors, second)
	}
	run := drivePlanAdmission(t, e, architect, world, df48Task, func(ctx context.Context) {
		e.run(ctx, df48Task, attemptObjective, RequestedByHuman)
	})
	return df48Rig{e: e, architect: architect, world: world, events: run.events}
}

// scopeRefusals are the production_scope refusals the run recorded, in order.
func (r df48Rig) scopeRefusals(t *testing.T) []planAttemptRefusal {
	t.Helper()
	var out []planAttemptRefusal
	for _, rec := range refusalsIn(t, r.events) {
		if rec.Class == refusalProductionScope {
			out = append(out, rec)
		}
	}
	return out
}

// starts are the implementer processes started, by source.
func (r df48Rig) starts() map[event.Source]int {
	n := map[event.Source]int{}
	for _, ev := range r.events {
		if ev.Kind == event.AgentStarted {
			n[ev.Source]++
		}
	}
	return n
}

// proposedAfter is the first plan made operative after index at, and where.
func (r df48Rig) proposedAfter(t *testing.T, at int) (string, int) {
	t.Helper()
	for i := at + 1; i < len(r.events); i++ {
		if r.events[i].Kind != event.PlanProposed {
			continue
		}
		var p struct {
			PlanAttemptID string `json:"plan_attempt_id"`
		}
		if err := json.Unmarshal(r.events[i].Payload, &p); err != nil {
			t.Fatal(err)
		}
		return p.PlanAttemptID, i
	}
	return "", -1
}

// reviewedBetween reports whether any audit or review happened in [from, to).
func (r df48Rig) reviewedBetween(from, to int) bool {
	for _, ev := range r.events[from:to] {
		switch ev.Kind {
		case event.CandidateAudited, event.ReviewStarted, event.ReviewCompleted:
			return true
		}
	}
	return false
}

// df48RequireRefusal asserts rec is the typed DF-48 refusal of attempt, naming
// exactly path as omitted, returned to the architect, carrying the refused
// candidate's frozen identity at world as non-authoritative material.
func df48RequireRefusal(t *testing.T, name string, rec planAttemptRefusal, attempt, path, world string) {
	t.Helper()
	var d productionScopeDeclaration
	if err := json.Unmarshal(rec.Declaration, &d); err != nil {
		t.Fatalf("%s: the production_scope declaration does not decode: %v", name, err)
	}
	if rec.Class != refusalProductionScope || rec.PlanAttemptID != attempt || len(d.OmittedPaths) != 1 || d.OmittedPaths[0] != path {
		t.Errorf("%s: not a production_scope refusal of attempt %s naming exactly %s: %+v %+v", name, short12(attempt), path, rec, d)
	}
	if rec.Continuation != session.PlanAdmissionContinuationArchitectTurn {
		t.Errorf("%s: the refusal was not recorded as returned to the architect: %q", name, rec.Continuation)
	}
	c := d.Candidate
	if c.BaseSHA != world || strings.TrimSpace(c.Tree) == "" || strings.TrimSpace(c.Revision) == "" || c.Standing != refusedCandidateStanding {
		t.Errorf("%s: the refused candidate's frozen identity is not recorded as non-authoritative material: %+v", name, c)
	}
	df39Refused(t, name, errors.New(rec.Reason), "edits", path)
}

// runreceiptKnown is runreceipt.Known, read without importing the package.
const runreceiptKnown = "KNOWN"

// W1 -- SPECIMEN SHAPE. The task does not end workflow.failed: a typed scope
// refusal naming exactly other.go reaches the architect, before any audit or
// review, and the candidate's identity is recorded as non-authoritative
// material. Fails if the unplanned edit is not refused (DF-39) or if the
// refusal ends the task instead of returning to architecture.
func TestDF48W1AnUnplannedExistingProductionEditReturnsATypedScopeRefusalToTheArchitect(t *testing.T) {
	r := df48Drive(t, false, df48Include)
	attempts := startedAttempts(t, r.events)
	refusals := r.scopeRefusals(t)
	if len(attempts) < 2 || len(refusals) == 0 {
		t.Fatalf("premise: the specimen candidate was refused for production scope (attempts %v):\n%s", attempts, gapLoopTrace(r.events))
	}
	df48RequireRefusal(t, "W1", refusals[0], attempts[0], df48Other, r.world)
	refusedAt := indexOfKind(r.events, event.PlanAttemptRefused)
	if r.reviewedBetween(0, refusedAt) {
		t.Error("W1: the refused candidate reached audit or review before its scope refusal")
	}
	if len(r.architect.prompts) < 2 || !strings.Contains(r.architect.prompts[1], df48RefusedMarker) ||
		!strings.Contains(r.architect.prompts[1], refusals[0].RefusalID) || !strings.Contains(r.architect.prompts[1], df48Other) {
		t.Fatalf("W1: the typed scope refusal did not reach the architect (%d prompts)", len(r.architect.prompts))
	}
	if hasKind(kindsOf(r.events), event.WorkflowFailed) {
		t.Fatalf("W1: the scope refusal ended the task workflow.failed:\n%s", gapLoopTrace(r.events))
	}
	// The refused candidate's certification is retracted the moment it is
	// refused: whatever the run's receipt later holds was certified again,
	// after the replacement was made operative (W2).
	if _, at := r.proposedAfter(t, refusedAt); at < 0 || indexOfKindAfter(r.events, event.ValidationRun, at) < 0 {
		t.Errorf("W1: no fresh validation followed the replacement:\n%s", gapLoopTrace(r.events))
	}
}

// indexOfKindAfter is the index of the first event of kind after index at.
func indexOfKindAfter(evs []event.Event, kind event.Kind, at int) int {
	for i := at + 1; i < len(evs); i++ {
		if evs[i].Kind == kind {
			return i
		}
	}
	return -1
}

// W2 -- NO STANDING CARRIES. The replacement PlanAttempt is a fresh identity,
// operative, with its own freshly recorded grant state; its candidate is
// validated and reviewed anew under it, starting again at cycle 1; and the
// run's receipt binds the replacement, not the refused attempt.
func TestDF48W2TheReplacementAttemptInheritsNoStandingFromTheRefusedCandidate(t *testing.T) {
	r := df48Drive(t, false, df48Include)
	refusals := r.scopeRefusals(t)
	if len(refusals) != 1 {
		t.Fatalf("premise: one scope refusal:\n%s", gapLoopTrace(r.events))
	}
	refused := refusals[0].PlanAttemptID
	refusedAt := indexOfKind(r.events, event.PlanAttemptRefused)
	replacement, at := r.proposedAfter(t, refusedAt)
	if replacement == "" || replacement == refused {
		t.Fatalf("W2: no fresh replacement attempt was made operative after the refusal:\n%s", gapLoopTrace(r.events))
	}
	if op := r.e.operativePlanAttempt(df48Task); op.ID != replacement {
		t.Errorf("W2: the operative attempt is %s, not the replacement %s", short12(op.ID), short12(replacement))
	}
	if p, ed := r.e.recordedGrants(df48Task, replacement); p.PlanAttemptID != replacement || ed.PlanAttemptID != replacement {
		t.Errorf("W2: the replacement holds no grant state recorded for itself: %+v %+v", p, ed)
	}
	validated := 0
	for _, ev := range r.events[at:] {
		if ev.Kind == event.ValidationRun {
			validated++
		}
	}
	if validated == 0 || !r.reviewedBetween(at, len(r.events)) || r.reviewedBetween(0, refusedAt) {
		t.Errorf("W2: the replacement's candidate was not validated and reviewed anew under it (validations after: %d)", validated)
	}
	settled := r.e.settledInvocations(df48Task)
	if len(settled) != 2 || settled[0].Cycle != 1 || settled[1].Cycle != 1 {
		t.Errorf("W2: the replacement's implementation did not start its own cycle 1: %+v", settled)
	}
	if !hasKind(kindsOf(r.events), event.WorkflowNotConverged) {
		t.Fatalf("W2: the replacement candidate's own review did not decide the run:\n%s", gapLoopTrace(r.events))
	}
	// The receipt binds the replacement, and the review it names is the
	// replacement candidate's own, taken after the replacement was operative.
	rec := receiptFrom(t, r.events)
	if rec.PlanDigest.Text != replacement || rec.PlanDigest.Text == refused {
		t.Errorf("W2: the receipt does not bind the replacement attempt: %+v", rec.PlanDigest)
	}
	if rec.ReviewedDigest.State == runreceiptKnown && indexOfKindAfter(r.events, event.ReviewCompleted, at) < 0 {
		t.Errorf("W2: the receipt names a review not taken under the replacement: %+v", rec.ReviewedDigest)
	}
	// The worktree went on as the replacement's material: the refusal it was
	// refused under resolved nothing.
	if at, d := df48DispositionOf(t, r.events, refusals[0].RefusalID); at >= 0 {
		t.Errorf("W2: the replanned material was resolved under its refusal: %+v", d)
	}

	// INTERIM (RULING-170): nothing carries across a process boundary either.
	// The process ends right after the refusal is durably recorded; a fresh
	// engine's Resume meets the owed production_scope refusal and returns the
	// typed restoration refusal -- no architect turn, no implementer, no
	// refused attempt made operative, nothing owed reconstructed.
	t.Run("resume", func(t *testing.T) {
		found, next := df48ResumeAfter(t, r, func(ev event.Event, _ []event.Event) bool { return ev.Kind == event.PlanAttemptRefused })
		var named planAttemptRefusal
		if json.Unmarshal(found.PlanAdmissionRefused, &named) != nil || named.RefusalID != refusals[0].RefusalID {
			t.Fatalf("premise: the session projection does not name the owed scope refusal: %s", found.PlanAdmissionRefused)
		}
		next(t, refusals[0])
	})
	// The same after the replacement's PlanProposed discharged the owed
	// marker: the refusal is history, not owed, and is still not restored into
	// the recurrence state that decides whether a later refusal may reach the
	// architect.
	t.Run("resume after the replacement", func(t *testing.T) {
		found, next := df48ResumeAfter(t, r, func(ev event.Event, seen []event.Event) bool {
			return ev.Kind == event.PlanProposed && indexOfKind(seen, event.PlanAttemptRefused) >= 0
		})
		if len(found.PlanAdmissionRefused) != 0 || len(found.PlanAttemptRefusals) == 0 {
			t.Fatalf("premise: the scope refusal is recorded and no longer owed: owed %s, records %d",
				found.PlanAdmissionRefused, len(found.PlanAttemptRefusals))
		}
		next(t, refusals[0])
	})
}

// df48ResumeAfter interrupts r's run at the first event of the task cut
// selects -- given it and the task's events before it -- and returns the
// session projection of the interrupted history, with a check that resuming
// it ends in the typed restoration refusal and continues no authority from
// the historical scope refusal.
func df48ResumeAfter(t *testing.T, r df48Rig, cut func(event.Event, []event.Event) bool) (session.Interrupted, func(*testing.T, planAttemptRefusal)) {
	t.Helper()
	history, err := r.e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	at := -1
	var seen []event.Event
	for i, ev := range history {
		if ev.TaskID != df48Task {
			continue
		}
		if cut(ev, seen) {
			at = i
			break
		}
		seen = append(seen, ev)
	}
	if at < 0 {
		t.Fatal("premise: the run never reached the interruption point")
	}
	interrupted := sessionStore(t)
	for _, ev := range history[:at+1] {
		if err := interrupted.Append(ev); err != nil {
			t.Fatal(err)
		}
	}
	found := reconstructed(t, interrupted, df48Task)
	return found, func(t *testing.T, refusal planAttemptRefusal) {
		t.Helper()
		next, _, _ := newGapLoopEngine(t, &gapLoopRun{engine: r.e, world: r.world}, interrupted, df48Include)
		next.Config = r.e.Config
		answer := &scriptedArchitect{turns: []architectTurn{{text: df48Include}, {text: df48Stop}}}
		next.Runners = objectiveRoles{architect: answer, reviewer: df48Revise, bindings: make(chan RunnerSpec, 64)}
		run := drivePlanAdmission(t, next, answer, r.world, df48Task, func(ctx context.Context) { next.Resume(ctx, found) })
		refusedAt := indexOfKind(run.events, event.WorkflowRestorationRefused)
		var typed RestorationRefusal
		if refusedAt < 0 || json.Unmarshal(run.events[refusedAt].Payload, &typed) != nil || typed.Binding != RestorationPlanAttemptUnbound {
			t.Fatalf("resume: the scope refusal did not end in the typed restoration refusal:\n%s", gapLoopTrace(run.events))
		}
		if len(answer.prompts) != 0 || implementerStartedBefore(run.events, len(run.events)) ||
			hasKind(kindsOf(run.events), event.PlanProposed) || hasKind(kindsOf(run.events), event.WorkflowFailed) {
			t.Errorf("resume: authority was continued from the historical refusal (%d architect turns):\n%s",
				len(answer.prompts), gapLoopTrace(run.events))
		}
		if _, owed := next.owedPlanAdmissionRefusal(df48Task); owed || next.operativePlanAttempt(df48Task).ID == refusal.PlanAttemptID {
			t.Error("resume: the refused attempt or its owed refusal was reconstructed as workflow authority")
		}
		// The historical refusal is not recurrence state: it could not decide
		// whether a later production_scope refusal reaches the architect.
		later := refusal
		later.RefusalID = "later"
		if next.refusalRecorded(df48Task, refusal.RefusalID) || next.refusalRecurs(df48Task, later) {
			t.Error("resume: the historical scope refusal was restored into the live recurrence bound")
		}
	}
}

// df48RequireStoppedState asserts the persisted task state agrees with the
// architect's stop: nothing implementing, and no finding left open -- the
// answered scope refusal least of all.
func df48RequireStoppedState(t *testing.T, name, root string) {
	t.Helper()
	var state struct {
		Phase string `json:"phase"`
		Open  []struct {
			Source string `json:"source"`
			Detail string `json:"detail"`
		} `json:"open_findings"`
	}
	if err := json.Unmarshal([]byte(readRecord(t, root+"/.sensei-code/tasks/"+df48Task+".json")), &state); err != nil {
		t.Fatal(err)
	}
	if state.Phase == "implementing" || state.Phase == "reviewing" || state.Phase == "revising" {
		t.Errorf("%s: task state still records an active %s phase after the architect's stop", name, state.Phase)
	}
	if len(state.Open) != 0 {
		t.Errorf("%s: task state keeps findings open after the architect's stop: %+v", name, state.Open)
	}
}

// df48Disposition is what a CandidateResolved record holds.
type df48Disposition struct {
	Disposition string `json:"disposition"`
	Reason      string `json:"reason"`
	Evidence    struct {
		BaseSHA      string   `json:"base_sha"`
		DiffBytes    int      `json:"diff_bytes"`
		ChangedPaths []string `json:"changed_paths"`
		AuditVerdict string   `json:"audit_verdict"`
		AuditDetail  string   `json:"audit_detail"`
	} `json:"evidence"`
}

// df48DispositionOf is the first CandidateResolved record naming refusalID:
// its index, or -1, and what it recorded.
func df48DispositionOf(t *testing.T, evs []event.Event, refusalID string) (int, df48Disposition) {
	t.Helper()
	for i, ev := range evs {
		if ev.Kind != event.CandidateResolved {
			continue
		}
		var d df48Disposition
		if err := json.Unmarshal(ev.Payload, &d); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(d.Reason, refusalID) {
			return i, d
		}
	}
	return -1, df48Disposition{}
}

// df48RequireDisposition asserts the refused material was disposed of as want,
// before the terminal at index terminal, naming the full refusal ID, with the
// frozen refused capture -- which holds path, at r.world -- as its evidence and
// no audit or review of it, and that the candidate record on disk binds the
// same disposition to the refused candidate's identity.
func df48RequireDisposition(t *testing.T, name string, r df48Rig, refusalID, want string, terminal int, path string) {
	t.Helper()
	at, d := df48DispositionOf(t, r.events, refusalID)
	if at < 0 || terminal < 0 || at > terminal || d.Disposition != want {
		t.Fatalf("%s: the refused material was not disposed of as %s, naming refusal %s, before the terminal (at %d, terminal %d, %+v):\n%s",
			name, want, refusalID, at, terminal, d, gapLoopTrace(r.events))
	}
	ev := d.Evidence
	if ev.BaseSHA != r.world || ev.DiffBytes == 0 || !strings.Contains(strings.Join(ev.ChangedPaths, "\n"), path) ||
		ev.AuditVerdict != "" || ev.AuditDetail != "" {
		t.Errorf("%s: the disposition does not carry the frozen refused capture as its evidence: %+v", name, ev)
	}
	var record struct {
		TaskID     string           `json:"task_id"`
		BaseSHA    string           `json:"base_sha"`
		Worktree   string           `json:"worktree"`
		Resolution *df48Disposition `json:"resolution"`
	}
	if err := json.Unmarshal([]byte(readRecord(t, r.e.Repo.Root+"/.sensei-code/candidates/"+df48Task+".json")), &record); err != nil {
		t.Fatal(err)
	}
	if record.TaskID != df48Task || record.BaseSHA != r.world || strings.TrimSpace(record.Worktree) == "" ||
		record.Resolution == nil || record.Resolution.Disposition != want || !strings.Contains(record.Resolution.Reason, refusalID) {
		t.Errorf("%s: the candidate record does not bind the disposition to the refused candidate's identity: %+v", name, record)
	}
}

// df48RequireNoFormatterStanding asserts a terminal receipt claims nothing the
// formatter measured of refused bytes.
func df48RequireNoFormatterStanding(t *testing.T, name string, events []event.Event) {
	t.Helper()
	if f := receiptFrom(t, events).FormatterMutationState; f.Text == "MUTATED" || f.Text == "UNCHANGED" {
		t.Errorf("%s: the receipt keeps the refused bytes' formatter fact as current standing: %+v", name, f)
	}
}

// W3 -- ARCHITECT OPTIONS. (a) include: fresh authority admits a candidate
// editing other.go, and it reaches review. (b) exclude: a new candidate still
// editing other.go is refused again by name, under the replacement. (c) stop:
// the task ends on the architect's stop, and the scope refusal stays a scope
// refusal, never "objective impossible".
func TestDF48W3TheArchitectMayIncludeExcludeOrStop(t *testing.T) {
	t.Run("include", func(t *testing.T) {
		r := df48Drive(t, false, df48Include)
		refusedAt := indexOfKind(r.events, event.PlanAttemptRefused)
		_, at := r.proposedAfter(t, refusedAt)
		if len(r.scopeRefusals(t)) != 1 || at < 0 || !r.reviewedBetween(at, len(r.events)) ||
			hasKind(kindsOf(r.events), event.WorkflowPlanAdmissionRefused) {
			t.Fatalf("(a): the replacement naming other.go did not admit a candidate editing it to review:\n%s", gapLoopTrace(r.events))
		}
	})
	t.Run("exclude", func(t *testing.T) {
		r := df48Drive(t, false, df48Exclude)
		refusals := r.scopeRefusals(t)
		if len(refusals) != 2 {
			t.Fatalf("(b): the candidate still editing the excluded path was not refused again:\n%s", gapLoopTrace(r.events))
		}
		replacement, _ := r.proposedAfter(t, indexOfKind(r.events, event.PlanAttemptRefused))
		df48RequireRefusal(t, "(b)", refusals[1], replacement, df48Other, r.world)
		if r.reviewedBetween(0, len(r.events)) || !hasKind(kindsOf(r.events), event.WorkflowPlanAdmissionRefused) {
			t.Fatalf("(b): the refused candidate reached review, or the run did not park:\n%s", gapLoopTrace(r.events))
		}
		// The repeated refusal's material is resumable, decided before the
		// park; the first refusal's material went on under the replacement
		// and was never resolved.
		df48RequireDisposition(t, "(b)", r, refusals[1].RefusalID, "resumable", indexOfKind(r.events, event.WorkflowPlanAdmissionRefused), df48Other)
		if at, _ := df48DispositionOf(t, r.events, refusals[0].RefusalID); at >= 0 {
			t.Errorf("(b): the replanned material was resolved under the refusal it went on from:\n%s", gapLoopTrace(r.events))
		}
		df48RequireNoFormatterStanding(t, "(b)", r.events)
	})
	t.Run("stop", func(t *testing.T) {
		r := df48Drive(t, false)
		refusals := r.scopeRefusals(t)
		completed := indexOfKind(r.events, event.WorkflowCompleted)
		spoke := indexOfKind(r.events, event.ArchitectSpoke)
		if len(refusals) != 1 || completed < 0 || spoke < 0 || spoke > completed {
			t.Fatalf("(c): one scope refusal, then the architect's stop through the architect-reply terminal:\n%s", gapLoopTrace(r.events))
		}
		if hasKind(kindsOf(r.events), event.WorkflowFailed) || hasKind(kindsOf(r.events), event.WorkflowPlanAdmissionRefused) {
			t.Fatalf("(c): the architect's stop ended the task as a failure or a park:\n%s", gapLoopTrace(r.events))
		}
		df48RequireRefusal(t, "(c)", refusals[0], startedAttempts(t, r.events)[0], df48Other, r.world)
		var named planAttemptRefusal
		if err := json.Unmarshal(r.events[completed].Payload, &named); err != nil || named.RefusalID != refusals[0].RefusalID ||
			named.Class != refusalProductionScope {
			t.Errorf("(c): the stop terminal does not carry the recorded production_scope refusal: %v %+v", err, named)
		}
		summary := r.events[completed].Summary
		if strings.HasPrefix(summary, "production scope refuted:") || strings.Contains(strings.ToLower(summary), "impossible") {
			t.Errorf("(c): the terminal restates the scope refusal as the objective: %q", summary)
		}
		// The architect-reply terminal's receipt: UNREVIEWED over the
		// preserved candidate, which is UNATTEMPTED material -- its certified
		// observation was replaced when it was refused -- naming no parked
		// refusal. Only the terminal's own facts are held complete; mint
		// evidence is absent for a candidate never minted.
		rec := receiptFrom(t, r.events)
		if rec.CandidateDigest.State == runreceiptKnown {
			t.Errorf("(c): the stop receipt certifies a digest of the refused candidate: %+v", rec.CandidateDigest)
		}
		if rec.Outcome != "UNREVIEWED" || rec.CandidateState != "UNATTEMPTED" ||
			rec.PlanAdmissionRefusal != nil && rec.PlanAdmissionRefusal.State == "KNOWN" {
			t.Errorf("(c): the receipt is not the architect-reply terminal over the preserved candidate: %s %s %+v",
				rec.Outcome, rec.CandidateState, rec.PlanAdmissionRefusal)
		}
		_, missing := rec.Completeness()
		for _, m := range missing {
			if strings.HasPrefix(m, "outcome") || strings.HasPrefix(m, "candidate_state") || strings.Contains(m, "plan_admission_refusal") ||
				strings.HasPrefix(m, "terminal") || strings.HasPrefix(m, "plan_digest") {
				t.Errorf("(c): the architect-stop receipt is not complete in its own facts: %s", m)
			}
		}
		if id, _ := r.proposedAfter(t, indexOfKind(r.events, event.PlanAttemptRefused)); id != "" || len(r.starts()) != 1 {
			t.Errorf("(c): work continued after the architect stopped: replacement %q, starts %v", short12(id), r.starts())
		}
		// The refused material is kept by decision, not left meaning nothing:
		// its disposition is recorded before the terminal.
		df48RequireDisposition(t, "(c)", r, refusals[0].RefusalID, "retained", completed, df48Other)
		df48RequireNoFormatterStanding(t, "(c)", r.events)
		df48RequireStoppedState(t, "(c)", r.e.Repo.Root)
	})
}

// df48LaterEdit serves the specimen's roles and, when the second implementer
// turn is resolved -- after cycle 1 has been audited and reviewed -- plants the
// unplanned edit of the existing other.go in the candidate worktree, so only
// cycle 2's candidate exceeds the plan.
type df48LaterEdit struct {
	t        *testing.T
	roles    objectiveRoles
	work     string
	resolved *int
}

func (r df48LaterEdit) Resolve(spec RunnerSpec) (Resolved, error) {
	if spec.Role == roles.Implementer {
		*r.resolved++
		if *r.resolved == 2 {
			commitFixtureFile(r.t, r.work, df48Other, strings.Replace(df48OtherSrc, "return 0", "return 1", 1))
		}
	}
	return r.roles.Resolve(spec)
}

// W3 (c), LATER CYCLE -- the stop records the refused material, not an earlier
// revision. Cycle 1 edits only main.go, is audited and reviewed, and receives
// REVISE; cycle 2 adds the unplanned other.go edit and is refused; the
// architect stops. Neither the retained disposition nor task state may carry
// cycle 1's audit, review, paths or required-test results as evidence of the
// refused cycle-2 material.
func TestDF48W3TheArchitectStopAfterAReviewedCycleCarriesNoEarlierStanding(t *testing.T) {
	architect := &scriptedArchitect{turns: []architectTurn{{text: objectivePlan}, {text: df48Stop}}}
	base := objectiveRoles{architect: architect, reviewer: df48Revise, bindings: make(chan RunnerSpec, 64)}
	e, _ := objectiveEngine(t, t.TempDir(), base)
	e.Config.Sensei.Args = []string{"-c", regionScript(objectiveRunScript, "main.go", df48Other)}
	e.Config.Workflow.ReviewCycles = 2
	world := commitFixtureFile(t, e.Repo.Root, df48Other, df48OtherSrc)
	work, err := e.Repo.CreateWorktreeAt(context.Background(), df48Task, world)
	if err != nil {
		t.Fatal(err)
	}
	resolved := 0
	e.Runners = df48LaterEdit{t: t, roles: base, work: work, resolved: &resolved}
	run := drivePlanAdmission(t, e, architect, world, df48Task, func(ctx context.Context) {
		e.run(ctx, df48Task, attemptObjective, RequestedByHuman)
	})
	r := df48Rig{e: e, architect: architect, world: world, events: run.events}

	refusals := r.scopeRefusals(t)
	refusedAt := indexOfKind(r.events, event.PlanAttemptRefused)
	completed := indexOfKind(r.events, event.WorkflowCompleted)
	if len(refusals) != 1 || refusedAt < 0 || completed < refusedAt || indexOfKind(r.events, event.CandidateAudited) > refusedAt ||
		indexOfKind(r.events, event.ReviewCompleted) < 0 || indexOfKind(r.events, event.ReviewCompleted) > refusedAt {
		t.Fatalf("premise: cycle 1 audited and reviewed, cycle 2 refused for scope, then the architect's stop:\n%s", gapLoopTrace(r.events))
	}
	df48RequireRefusal(t, "later", refusals[0], startedAttempts(t, r.events)[0], df48Other, r.world)

	// The retained disposition describes the refused cycle-2 capture -- it
	// holds other.go -- and claims no audit or review of it.
	resolvedAt := indexOfKind(r.events, event.CandidateResolved)
	var disposition struct {
		Disposition string `json:"disposition"`
		Evidence    struct {
			DiffBytes    int      `json:"diff_bytes"`
			ChangedPaths []string `json:"changed_paths"`
			AuditVerdict string   `json:"audit_verdict"`
			AuditDetail  string   `json:"audit_detail"`
		} `json:"evidence"`
	}
	if resolvedAt < 0 || resolvedAt > completed || json.Unmarshal(r.events[resolvedAt].Payload, &disposition) != nil ||
		disposition.Disposition != "retained" {
		t.Fatalf("later: no retained disposition before the stop terminal:\n%s", gapLoopTrace(r.events))
	}
	ev := disposition.Evidence
	if ev.AuditVerdict != "" || ev.AuditDetail != "" {
		t.Errorf("later: the refused material's disposition carries an earlier revision's audit or review: %+v", ev)
	}
	if ev.DiffBytes == 0 || !strings.Contains(strings.Join(ev.ChangedPaths, "\n"), df48Other) {
		t.Errorf("later: the disposition does not describe the refused cycle-2 capture: %+v", ev)
	}

	// Task state likewise: the refused capture, the task's required tests,
	// and no audit, review or required-test result of cycle 1.
	var state struct {
		Evidence struct {
			ChangedPaths        []string          `json:"changed_paths"`
			AuditVerdict        string            `json:"audit_verdict"`
			AuditDetail         string            `json:"audit_detail"`
			RequiredTestResults []json.RawMessage `json:"required_test_results"`
		} `json:"evidence"`
		Open []struct {
			Source string `json:"source"`
		} `json:"open_findings"`
	}
	if err := json.Unmarshal([]byte(readRecord(t, e.Repo.Root+"/.sensei-code/tasks/"+df48Task+".json")), &state); err != nil {
		t.Fatal(err)
	}
	if st := state.Evidence; st.AuditVerdict != "" || st.AuditDetail != "" || len(st.RequiredTestResults) != 0 ||
		!strings.Contains(strings.Join(st.ChangedPaths, "\n"), df48Other) {
		t.Errorf("later: task state carries cycle 1's standing as evidence of the refused material: %+v", st)
	}
	for _, f := range state.Open {
		if f.Source == "reviewer" || f.Source == "sensei audit" {
			t.Errorf("later: task state keeps cycle 1's %s finding open against the refused material", f.Source)
		}
	}
	df48RequireStoppedState(t, "later", e.Repo.Root)
}

// W4 -- DF-39 STRICTNESS. The unplanned path is refused by name, typed or not;
// no implementation is retried under the refused plan; no second implementor
// and no handoff serves the refused candidate.
func TestDF48W4TheProductionScopeRefusalStaysStrict(t *testing.T) {
	err := df39Scope(df23Edit(df39A)+df23Edit(df39B), df39Attempt(t, df39A))
	typed := refuseProductionScope(err, teWorld, "tree", "revision")
	df39Refused(t, "W4 typed", typed, "edits", df39B, "edits "+df39A)
	if scope, ok := productionScopeRefusalOf(typed); !ok || len(scope.paths) != 1 || scope.paths[0] != df39B {
		t.Fatalf("W4: the omitted path is not carried by the typed refusal: %v", typed)
	}

	r := df48Drive(t, true, df48Exclude)
	refusals := r.scopeRefusals(t)
	if len(refusals) == 0 {
		t.Fatalf("premise: the specimen candidate was refused:\n%s", gapLoopTrace(r.events))
	}
	for _, rec := range refusals {
		df39Refused(t, "W4 loop", errors.New(rec.Reason), "edits", df48Other, "main.go")
	}
	refusedAt := indexOfKind(r.events, event.PlanAttemptRefused)
	_, at := r.proposedAfter(t, refusedAt)
	if at < 0 {
		at = len(r.events)
	}
	if implementerStartedBefore(r.events[refusedAt:], at-refusedAt) {
		t.Error("W4: an implementer was started under the refused plan after its scope refusal")
	}
	if starts := r.starts(); len(starts) != 1 {
		t.Errorf("W4: a second implementor served the refused candidate: %v", starts)
	}
	if hasKind(kindsOf(r.events), event.HandoffCreated) {
		t.Error("W4: the scope refusal created an implementer handoff")
	}
}

// W5 -- BOUNDED ARCHITECTURAL RECONSIDERATION. A later production_scope
// refusal naming a DIFFERENT path (main.go, after other.go) does not mint
// another return: it is recorded and the invocation ends with the existing
// PLAN_ADMISSION_REFUSED outcome, the architect is not asked a third time, and
// the receipt names the canonical production_scope class, complete.
func TestDF48W5ALaterScopeRefusalOfAnyPathsConsumesTheOneReplan(t *testing.T) {
	r := df48Drive(t, false, df48Narrow, df48Include)
	refusals := r.scopeRefusals(t)
	if len(refusals) != 2 {
		t.Fatalf("premise: two scope refusals, naming different paths:\n%s", gapLoopTrace(r.events))
	}
	replacement, _ := r.proposedAfter(t, indexOfKind(r.events, event.PlanAttemptRefused))
	df48RequireRefusal(t, "W5 first", refusals[0], startedAttempts(t, r.events)[0], df48Other, r.world)
	df48RequireRefusal(t, "W5 second", refusals[1], replacement, "main.go", r.world)
	parked := indexOfKind(r.events, event.WorkflowPlanAdmissionRefused)
	var named planAttemptRefusal
	if parked < 0 || json.Unmarshal(r.events[parked].Payload, &named) != nil || named.RefusalID != refusals[1].RefusalID {
		t.Fatalf("W5: the materially different scope refusal did not end the invocation PLAN_ADMISSION_REFUSED:\n%s", gapLoopTrace(r.events))
	}
	if len(r.architect.prompts) != 2 || r.reviewedBetween(0, len(r.events)) || hasKind(kindsOf(r.events), event.WorkflowFailed) {
		t.Errorf("W5: the bound was not held (%d architect turns):\n%s", len(r.architect.prompts), gapLoopTrace(r.events))
	}
	// The bound is the canonical recorded refusals' to decide: both refusals
	// are recorded under their own identities, and nothing else holds it.
	if !r.e.refusalRecorded(df48Task, refusals[0].RefusalID) || !r.e.refusalRecorded(df48Task, refusals[1].RefusalID) ||
		!r.e.refusalRecurs(df48Task, planAttemptRefusal{Class: refusalProductionScope}) {
		t.Error("W5: the live bound is not derived from the task's recorded refusals")
	}
	rec := receiptFrom(t, r.events)
	if rec.CandidateState != "UNATTEMPTED" || rec.CandidateDigest.State == runreceiptKnown {
		t.Errorf("W5: the bounded terminal's receipt certifies the refused candidate: %s %+v", rec.CandidateState, rec.CandidateDigest)
	}
	if rec.Outcome != "PLAN_ADMISSION_REFUSED" || rec.PlanAdmissionRefusal == nil || rec.PlanAdmissionRefusal.Class != "production_scope" ||
		rec.PlanAdmissionRefusal.RefusalID != refusals[1].RefusalID || rec.PlanAdmissionRefusal.Declaration != string(refusals[1].Declaration) {
		t.Fatalf("W5: the receipt does not name the parked production_scope refusal: %+v", rec.PlanAdmissionRefusal)
	}
	_, missing := rec.Completeness()
	for _, m := range missing {
		if strings.Contains(m, "plan_admission_refusal") {
			t.Errorf("W5: the receipt's production_scope refusal is not receipt-complete: %s", m)
		}
	}
	df48RequireDisposition(t, "W5", r, refusals[1].RefusalID, "resumable", parked, "main.go")
	df48RequireNoFormatterStanding(t, "W5", r.events)

	// Materially the same reconsideration: the architect answers with the
	// refused plan itself. It is refused before it is routed -- no attempt
	// is begun again under it, nothing is implemented -- and the invocation
	// ends with the existing PLAN_ADMISSION_REFUSED outcome naming the
	// recorded refusal.
	t.Run("reproposal", func(t *testing.T) {
		r := df48Drive(t, false, objectivePlan)
		refusals := r.scopeRefusals(t)
		if len(refusals) != 1 {
			t.Fatalf("premise: one scope refusal:\n%s", gapLoopTrace(r.events))
		}
		refusedAt := indexOfKind(r.events, event.PlanAttemptRefused)
		parked := indexOfKind(r.events, event.WorkflowPlanAdmissionRefused)
		var named planAttemptRefusal
		if parked < 0 || json.Unmarshal(r.events[parked].Payload, &named) != nil || named.RefusalID != refusals[0].RefusalID {
			t.Fatalf("W5: the reproposed plan did not end the invocation PLAN_ADMISSION_REFUSED:\n%s", gapLoopTrace(r.events))
		}
		if hasKind(kindsOf(r.events[refusedAt:]), event.PlanAttemptStarted) || hasKind(kindsOf(r.events[refusedAt:]), event.PlanProposed) ||
			implementerStartedBefore(r.events[refusedAt:], len(r.events)-refusedAt) || len(r.architect.prompts) != 2 {
			t.Errorf("W5: the refused attempt was routed or implemented again (%d architect turns):\n%s",
				len(r.architect.prompts), gapLoopTrace(r.events))
		}
		df48RequireDisposition(t, "W5 reproposal", r, refusals[0].RefusalID, "resumable", parked, df48Other)
		df48RequireNoFormatterStanding(t, "W5 reproposal", r.events)
	})

	// Every other exit of the reconsideration disposes of the material at the
	// terminal boundary itself, chosen by the terminal's typed semantics: an
	// invocation the task resumes from keeps it resumable, a final ending
	// retains it -- and a disposition that cannot be written suppresses the
	// terminal and fails the run, naming it.
	const refusalID = "df48-every-terminal-refusal-0123456789abcdef"
	arm := func(e *Engine) {
		id := candidateIdentityWithBase("world")
		id.TaskID, id.Worktree = df48Task, "/candidate/worktree"
		ev := refusedMaterialEvidence(nil, nil)
		ev.DiffBytes, ev.ChangedPaths = 9, []string{df48Other}
		e.armRefusedMaterial(df48Task, refusedScopeMaterial{refusalID: refusalID, identity: id, evidence: ev})
	}
	for _, c := range []struct {
		name string
		kind event.Kind
		want string
		end  func(e *Engine)
	}{
		{"deferred", event.WorkflowAwaitingAuthority, "resumable", func(e *Engine) {
			e.emitRunTerminal(df48Task, event.WorkflowAwaitingAuthority, event.SourceUser, "DEFERRED", "UNATTEMPTED", "deferred", nil)
		}},
		{"external block", event.WorkflowBlockedExternal, "resumable", func(e *Engine) {
			e.emitRunTerminal(df48Task, event.WorkflowBlockedExternal, event.SourceSystem, "BLOCKED_EXTERNAL", "UNATTEMPTED", "blocked", nil)
		}},
		{"timeout", event.WorkflowTimedOut, "resumable", func(e *Engine) {
			e.emitRunTerminal(df48Task, event.WorkflowTimedOut, event.SourceSystem, "TIMED_OUT", "UNATTEMPTED", "timed out", nil)
		}},
		{"caller stop", event.WorkflowStopped, "resumable", func(e *Engine) {
			e.emitRunTerminal(df48Task, event.WorkflowStopped, event.SourceSystem, "STOPPED", "UNATTEMPTED", "stopped", nil)
		}},
		{"human-authority stop", event.WorkflowStopped, "retained", func(e *Engine) {
			e.emitRunTerminal(df48Task, event.WorkflowStopped, event.SourceUser, "STOPPED", "UNATTEMPTED", "stopped", nil)
		}},
		{"failure", event.WorkflowFailed, "retained", func(e *Engine) {
			e.emitRunTerminal(df48Task, event.WorkflowFailed, event.SourceSystem, "FAILED", "UNATTEMPTED", "failed", nil)
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			e, store := attemptEngine(t)
			arm(e)
			c.end(e)
			// Consumed exactly once: a later terminal resolves nothing.
			e.emitRunTerminal(df48Task, event.WorkflowFailed, event.SourceSystem, "FAILED", "UNATTEMPTED", "again", nil)
			evs, err := store.Load()
			if err != nil {
				t.Fatal(err)
			}
			df48RequireDisposition(t, c.name, df48Rig{e: e, world: "world", events: evs}, refusalID, c.want, indexOfKind(evs, c.kind), df48Other)
			resolved := 0
			for _, ev := range evs {
				if ev.Kind == event.CandidateResolved {
					resolved++
				}
			}
			if resolved != 1 {
				t.Errorf("%s: the refused material was disposed of %d times", c.name, resolved)
			}
		})
	}
	t.Run("unrecordable", func(t *testing.T) {
		e, store := attemptEngine(t)
		arm(e)
		e.Repo.Root += "/\x00unwritable"
		e.emitRunTerminal(df48Task, event.WorkflowCompleted, event.SourceSystem, "UNREVIEWED", "UNATTEMPTED", "completed", nil)
		evs, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		failed := indexOfKind(evs, event.WorkflowFailed)
		if hasKind(kindsOf(evs), event.WorkflowCompleted) || hasKind(kindsOf(evs), event.CandidateResolved) || failed < 0 ||
			!strings.Contains(evs[failed].Summary, refusalID) || !strings.Contains(evs[failed].Summary, "no recorded disposition") {
			t.Fatalf("an unrecordable disposition did not suppress the terminal and fail the run naming it:\n%s", gapLoopTrace(evs))
		}
	})
}

// W6 -- TERMINALITY UNCHANGED OUTSIDE THE DF-48 SHAPE. A prospective-surface
// refutation, an edit with no operative attempt, one under an attempt pinned
// at another world, and one judged under an attempt no longer operative are
// never typed or recorded as production_scope refusals; in the real loop the
// no-plan and wrong-world shapes end terminal with no architect turn. The
// authorized control is the omitted shape, which is returned.
func TestDF48W6OnlyAnOmissionByAValidOperativeAttemptReturnsToTheArchitect(t *testing.T) {
	diff := df23Edit(df39A) + df23Edit(df39B)
	elsewhere := df39Attempt(t, df39A)
	elsewhere.World = "another-world"
	const created = "internal/workflow/df48new.go"
	for name, err := range map[string]error{
		"prospective surface": inspectProspectiveGrants(df39Created(created),
			[]ProspectiveSurface{{Path: created, Package: "x", Role: "go-existing-package", Covering: df39A}}, nil),
		"no operative plan attempt":     df39Scope(diff, planAttempt{}),
		"plan attempt at another world": df39Scope(diff, elsewhere),
	} {
		if err == nil {
			t.Fatalf("premise %s: the shape was not refused", name)
		}
		if got := refuseProductionScope(err, teWorld, "tree", "revision"); got != err {
			t.Errorf("%s: the refusal was rewritten: %v", name, got)
		}
		if _, ok := productionScopeRefusalOf(err); ok {
			t.Errorf("%s: typed as the DF-48 production_scope shape", name)
		}
	}
	if got := refuseProductionScope(df39Scope(diff, df39Attempt(t, df39A)), "another-base", "tree", "revision"); func() bool {
		_, ok := productionScopeRefusalOf(got)
		return ok
	}() {
		t.Error("an omission measured at another base than the candidate's was typed as the DF-48 shape")
	}

	// The canonical owner refuses a production_scope refusal judged under an
	// attempt that is no longer operative, and returns the one judged under
	// the operative attempt.
	const task = "task-df48-owner"
	e := df23Engine(t, "", task)
	a := df23Route(t, e, task, attemptPlan("plan A", df39A), nil)
	stale := refuseProductionScope(inspectProductionScope(teDiffState(diff, df23Candidate), a), teWorld, "tree", "revision")
	c := df23Route(t, e, task, attemptPlan("plan C", df39A), nil)
	if got, err := e.continueAfterAdmissionRefusal(task, stale); err != stale || got.RefusalID != "" ||
		e.refusalRecurs(task, planAttemptRefusal{Class: refusalProductionScope}) {
		t.Errorf("a refusal judged under the superseded attempt %s was returned or recorded: %+v %v", short12(a.ID), got, err)
	}
	current := refuseProductionScope(inspectProductionScope(teDiffState(diff, df23Candidate), c), teWorld, "tree", "revision")
	if got, err := e.continueAfterAdmissionRefusal(task, current); err != nil || got.PlanAttemptID != c.ID || got.Class != refusalProductionScope {
		t.Fatalf("authorized control: the omission by the operative attempt was not returned to the architect: %+v %v", got, err)
	}

	for name, mutate := range map[string]func(*planAttempt){
		"no operative plan attempt":     func(a *planAttempt) { *a = planAttempt{} },
		"plan attempt at another world": func(a *planAttempt) { a.World = "another-world" },
	} {
		t.Run(name, func(t *testing.T) {
			h := newGateHarness(t, roles.Policy{Reason: "blast radius local with approval gate none"}, roles.Unverified, string(roles.Accept))
			e := h.engine
			world := commitFixtureFile(t, h.work, df39FromEvents, df48OtherSrc)
			h.tc.Identity = candidateIdentityWithBase(world)
			adoptFixturePlanAttempt(t, e, "task-1", h.tc.Task, world, "Rewrite main.go so it prints a number.", h.tc.Files, nil)
			commitFixtureFile(t, h.work, df39FromEvents, strings.Replace(df48OtherSrc, "return 0", "return 1", 1))
			e.mu.Lock()
			mutate(&e.attempts["task-1"].operative)
			e.mu.Unlock()
			architect := &scriptedArchitect{turns: []architectTurn{{text: df48Exclude}}}
			e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "chatgpt", "true", "none"
			e.Runners = implementerResolver{architect: architect, session: "session-1",
				reviewer: answeringRunner{text: `{"decision":"accept","summary":"ok"}`, mode: roles.Unverified}}
			var failed error
			e.implement(context.Background(), h.sc, certifiedStart{}, "task-1", h.tc,
				"Rewrite main.go so it prints a number.", "", func(err error) { failed = err })
			seen := drainEvents(h.events)
			var parked *PlanAdmissionRefused
			if failed == nil || !strings.HasPrefix(failed.Error(), "production scope refuted:") || errors.As(failed, &parked) {
				t.Fatalf("the shape did not stay terminal on its own refusal: %v", failed)
			}
			if len(architect.prompts) != 0 || contains(seen, event.PlanAttemptRefused) {
				t.Errorf("the shape entered the DF-48 architect-return route (%d architect turns)", len(architect.prompts))
			}
		})
	}
}

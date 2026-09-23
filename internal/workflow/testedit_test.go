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

	// And a mixed record whose DERIVED half genuinely disagrees is reported as
	// the DERIVED failure it is: the two diagnoses do not collapse.
	broken := derivedGrant(t)
	broken.BaseHash = "0000"
	e2 := &Engine{}
	r2 := refusalOf(t, e2.restoreTestEditGrants(rrRecord(t, "t", broken, authoredGrant(t)), fresh, planned, teWorld))
	if r2.Binding != RestorationDerivedMismatch || r2.Instrument != RestorationInstrumentDerived {
		t.Fatalf("a broken DERIVED half inside a mixed record was not reported as a DERIVED mismatch: %+v", r2)
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

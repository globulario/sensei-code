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
	late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teCandidateWithNovelImport()), nil })
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
	// operation cased differently, the imports reordered and repeated -- so a
	// projector that compared declarations by their text rather than by what
	// they state would stop projecting here and quietly hand a decidable
	// refusal back to the late door.
	restated := teTheMeasuredCase()
	restated.Operation = "EDIT"
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
		late := inspectTestEdits(c.diff, grants, func(string) ([]byte, error) { return []byte(teSourceFor(c.decls[0])), nil })
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
	late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(declared)), nil })
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
	if late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(stripped), nil }); late == nil {
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

	for name, decl := range map[string]TestEditDeclaration{
		"undeclared imports":           contentDependent,
		"undeclared build constraints": constraintsUndeclared,
		"an unreadable operation":      unknownOperation,
		"no operation at all":          absentOperation,
	} {
		if err := projectTestEditRefusals([]TestEditDeclaration{decl}, grants); err != nil {
			t.Errorf("%s: guessed at plan admission: %v", name, err)
		}
	}

	// The plan proceeded, the implementer wrote the novel import anyway, and
	// the candidate-time check -- which is the authority of record -- refused
	// it.
	late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teCandidateWithNovelImport()), nil })
	if late == nil || !strings.Contains(late.Error(), "novel import") {
		t.Fatalf("the undeclared novel import was never refused at all: %v", late)
	}
	// So did the stripped build constraint the plan never declared.
	stripped := strings.TrimPrefix(teFSrc, "//go:build go1.20\n")
	if late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(stripped), nil }); late == nil ||
		!strings.Contains(late.Error(), "build constraints") {
		t.Fatalf("an undeclared build-constraint change was never refused at all: %v", late)
	}
	// AND THE REASON IT MUST STAY LATE. For a declaration this vocabulary
	// cannot read, the candidate is not determined: the same declaration is
	// compatible with a candidate the late check refuses for DELETING the
	// file, which is not the reason an eager projector would have given. Two
	// different conditions for one declaration is exactly what a projection
	// may not choose between.
	deleted := inspectTestEdits(teDiffDeleted, grants, func(string) ([]byte, error) { return []byte(teSourceFor(unknownOperation)), nil })
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
	if err := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(fine)), nil }); err != nil {
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
	late := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teCandidateWithNovelImport()), nil })
	if late == nil || !strings.Contains(late.Error(), "novel import") {
		t.Fatalf("a novel import declared twice, inconsistently, was never refused at all: %v", late)
	}
	if late := inspectTestEdits(teDiffDeleted, grants, func(string) ([]byte, error) { return []byte(teFSrc), nil }); late == nil ||
		!strings.Contains(late.Error(), "deletes it") {
		t.Fatalf("a deletion declared twice, inconsistently, was never refused at all: %v", late)
	}
	// A candidate that followed the ADMISSIBLE entry passes late -- which is
	// precisely why no early refusal may be produced from this plan: the early
	// door would have refused a run the authority of record admits.
	if err := inspectTestEdits(teDiffEdited, grants, func(string) ([]byte, error) { return []byte(teSourceFor(admissible)), nil }); err != nil {
		t.Fatalf("premise: the admissible entry of the disagreeing pair describes a candidate the late check accepts: %v", err)
	}
}

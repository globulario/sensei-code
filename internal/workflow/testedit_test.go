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

// THE DF-23 SPECIMEN, as a lifecycle witness.
//
// task-1789960053774525922 held a validated, audited candidate of 1796
// insertions. restoreTestEditGrants recomputed 0 grants from a task-only
// preflight, compared them against the 7 the record held, and refused. The
// refusal is CORRECT and is unchanged here: a stale, damaged or edited record
// must not become operational authority by being present.
//
// What was wrong is that the refusal emitted a TASK terminal, so the task left
// `resume --list` entirely and 120 KB of reviewed work was reachable only as a
// new task. The invocation stops; the task keeps the refusal as its obligation.
func TestARefusedRestorationEndsTheInvocationAndNotTheTask(t *testing.T) {
	world := teRead(map[string]string{teS: teSSrc, teF: teFSrc})
	fresh, _ := testEditGrants(context.Background(), teWorld, []string{teS, teF}, teCovered(), authoredEvidence{}, world)
	if len(fresh) != 1 {
		t.Fatal("premise: the pinned world grants one edit")
	}
	forged := fresh[0]
	forged.BaseHash = "0000"
	raw, _ := json.Marshal(testEditRecord{World: teWorld, Grants: []testEditGrant{forged}})
	recorded := session.Interrupted{TaskID: "t", TestEditRecord: raw}

	refusal := (&Engine{}).restoreTestEditGrants(recorded, fresh, []string{teS, teF}, teWorld)
	if refusal == nil {
		t.Fatal("premise: the guard refuses a record the pinned world disagrees with")
	}

	bus := event.NewBus()
	stream, done := bus.Subscribe(16)
	defer done()
	e := &Engine{Bus: bus, SessionID: "s1"}
	// The wrap the resume call site applies: the refusal, plus the task and
	// candidate identity that call site already holds. Nothing is derived.
	e.terminateRun(context.Background(), "t", "close the lifecycle boundary",
		preserveContinuation(ContinuationObligation{
			TaskID: "t", Kind: ContinuationRestorationRefused,
			CandidateBaseSHA: teWorld, CandidateBranch: "sensei-code/t",
		}, refusal))

	emitted := drainEvents(stream)
	var terminal *event.Event
	for i, ev := range emitted {
		if ev.Kind == event.WorkflowFailed {
			if terminal != nil {
				t.Fatal("one invocation, two terminals")
			}
			terminal = &emitted[i]
		}
	}
	if terminal == nil {
		t.Fatalf("the invocation recorded no terminal at all: %v", kinds(emitted))
	}
	o, err := ParseContinuation(terminal.Payload)
	if err != nil {
		t.Fatalf("the terminal carried no readable obligation: %v", err)
	}
	// VERBATIM. The preserved account is the guard's own sentence; a summary of
	// it is a different statement about what happened.
	if o.Reason != refusal.Error() {
		t.Fatalf("the refusal was not preserved verbatim:\n got %q\nwant %q", o.Reason, refusal.Error())
	}
	if o.CandidateBaseSHA != teWorld || o.Kind != ContinuationRestorationRefused {
		t.Fatalf("the obligation lost the identity its call site held: %+v", o)
	}

	// THE RESTART. A fresh reconstruction over the durable events alone -- no
	// engine, no memory of this process -- still finds the task, with the same
	// candidate identity and the same obligation.
	record := append([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "close the lifecycle boundary"},
		{TaskID: "t", Kind: event.PlanProposed, Source: event.SourceArchitect, Summary: "plan",
			Payload: json.RawMessage(`{"plan":"p","files":["` + teS + `","` + teF + `"]}`)},
	}, emitted...)
	found := session.FindInterrupted(record)
	if len(found) != 1 || found[0].TaskID != "t" {
		t.Fatalf("a refused restoration destroyed the task: %+v", found)
	}
	restored, err := ParseContinuation(found[0].Continuation)
	if err != nil || restored != o {
		t.Fatalf("the obligation did not survive the restart: %+v %v", restored, err)
	}
	if !found[0].Planned {
		t.Fatal("the restored task lost its plan, so it would be sent back to the architect")
	}

	// THE CONTROL. The same boundary, an ordinary failure: the task ends.
	bus2 := event.NewBus()
	stream2, done2 := bus2.Subscribe(16)
	defer done2()
	e2 := &Engine{Bus: bus2, SessionID: "s1"}
	e2.terminateRun(context.Background(), "t", "close the lifecycle boundary", errors.New("the worker died"))
	ended := session.FindInterrupted(append(record[:2:2], drainEvents(stream2)...))
	if len(ended) != 0 {
		t.Fatalf("a genuine failure left the task active, so failure has been made impossible: %+v", ended)
	}
}

// A FRESH PROCESS THAT CANNOT RE-ENTER A DEFERRED QUESTION MUST NOT CONSUME IT.
//
// The known bypass: resumeAuthority reached sensei.Start and emitted
// WorkflowFailed directly, around the shared boundary. A task already holding a
// durable human-owned question therefore died of an unavailable dependency, and
// the question disappeared with it (DF-21 destroyed a recorded authorization in
// four seconds).
//
// The question is the authority object. It is preserved as recorded and nothing
// about it is re-derived: no candidate, no answer, no actor, no scope.
func TestADeferredQuestionSurvivesAReentryItsDependencyRefused(t *testing.T) {
	question := json.RawMessage(`{"condition":"graph coverage is absent for the planned files",` +
		`"domain":"github.com/globulario/sensei-code","base_sha":"abc123",` +
		`"decision":{"subject":"Authorize the architectural change?","options":[{"id":"1","label":"authorize"},{"id":"2","label":"stop"}]},` +
		`"task_id":"t","scope":["` + teS + `"],"scope_recorded":true}`)
	bus := event.NewBus()
	stream, done := bus.Subscribe(16)
	defer done()
	// No Sensei command is configured, so starting the dependency fails: the
	// shape of a fresh process that cannot re-enter the turn.
	e := &Engine{Bus: bus, SessionID: "s1"}
	e.resumeAuthority(context.Background(), session.Interrupted{
		TaskID: "t", Task: "widen the boundary", AwaitingAuthority: question,
	})

	emitted := drainEvents(stream)
	if contains(emitted, event.AuthorityResolved) {
		t.Fatalf("a failed re-entry recorded an answer nobody gave: %v", kinds(emitted))
	}
	var terminal *event.Event
	for i, ev := range emitted {
		if ev.Kind == event.WorkflowFailed {
			terminal = &emitted[i]
		}
	}
	if terminal == nil {
		t.Fatalf("the re-entry ended with no terminal: %v", kinds(emitted))
	}
	o, err := ParseContinuation(terminal.Payload)
	if err != nil {
		t.Fatalf("the terminal did not preserve the task: %v", err)
	}
	if o.Kind != ContinuationAuthorityReentry {
		t.Fatalf("the obligation is not the authority one: %+v", o)
	}
	// NOTHING MINTED. The authority path holds no candidate, and a record that
	// carried one would be the recovery procedure inventing the identity it
	// exists to restore.
	if o.CandidateBaseSHA != "" || o.CandidateBranch != "" {
		t.Fatalf("the authority obligation invented a candidate identity: %+v", o)
	}
	if !strings.Contains(o.Reason, "start Sensei") {
		t.Fatalf("the obligation does not say what was unavailable: %q", o.Reason)
	}

	record := append([]event.Event{
		{TaskID: "t", Kind: event.TaskCreated, Summary: "widen the boundary"},
		{TaskID: "t", Kind: event.WorkflowAwaitingAuthority, Source: event.SourceUser,
			Summary: "authority decision deferred", Payload: question},
	}, emitted...)
	found := session.FindInterrupted(record)
	if len(found) != 1 {
		t.Fatalf("the task holding the question was destroyed: %+v", found)
	}
	// BYTE FOR BYTE. The question a later process asks is the question that was
	// asked, not a re-rendering of it.
	if string(found[0].AwaitingAuthority) != string(question) {
		t.Fatalf("the question changed on the way through:\n got %s\nwant %s", found[0].AwaitingAuthority, question)
	}
}

// THE CONTROL FOR THE ABOVE. "Preserve the existing question" must never widen
// into "preserve anything shaped vaguely like a question".
//
// Each record here is unreadable, unanswerable, or about another task. Each is
// refused BEFORE anything is started, and each still ends the task: continuity
// existing is not a reason to convert a record nobody can trust into a standing
// authority obligation.
func TestACorruptOrMismatchedDeferredQuestionStillFailsClosed(t *testing.T) {
	for name, question := range map[string]json.RawMessage{
		"unreadable":           json.RawMessage(`{"condition":`),
		"no options to answer": json.RawMessage(`{"condition":"c","decision":{"subject":"?"}}`),
		"bound to another task": json.RawMessage(`{"condition":"c","task_id":"other",` +
			`"decision":{"subject":"?","options":[{"id":"1"}]},"scope_recorded":true}`),
	} {
		bus := event.NewBus()
		stream, done := bus.Subscribe(16)
		e := &Engine{Bus: bus, SessionID: "s1"}
		e.resumeAuthority(context.Background(), session.Interrupted{
			TaskID: "t", Task: "widen the boundary", AwaitingAuthority: question,
		})
		emitted := drainEvents(stream)
		done()
		if !contains(emitted, event.WorkflowFailed) {
			t.Errorf("%s: the invocation did not fail closed: %v", name, kinds(emitted))
			continue
		}
		for _, ev := range emitted {
			if ev.Kind != event.WorkflowFailed {
				continue
			}
			if _, err := ParseContinuation(ev.Payload); err == nil {
				t.Errorf("%s: a record nobody can trust became a resumable obligation", name)
			}
		}
		found := session.FindInterrupted(append([]event.Event{
			{TaskID: "t", Kind: event.TaskCreated, Summary: "widen the boundary"},
			{TaskID: "t", Kind: event.WorkflowAwaitingAuthority, Source: event.SourceUser, Payload: question},
		}, emitted...))
		if len(found) != 0 {
			t.Errorf("%s: the task stayed active on a record that cannot be answered: %+v", name, found)
		}
	}
}

// THE TWO PACKAGES AGREE ABOUT THE CLOSED VOCABULARY.
//
// The workflow package owns the kinds and internal/session recognises them
// without importing it, so the agreement is a behaviour rather than a shared
// constant. Adding a kind here and forgetting it there would produce a terminal
// this engine calls preserving and every reader calls final -- the exact
// disagreement between the record and the lifecycle this repair removes.
func TestEveryContinuationKindIsHonouredByTheDurableReader(t *testing.T) {
	for _, kind := range []string{ContinuationRestorationRefused, ContinuationImplementationDeclined, ContinuationAuthorityReentry} {
		o := ContinuationObligation{TaskID: "t", Kind: kind, Reason: "because"}
		if kind != ContinuationAuthorityReentry {
			o.CandidateBaseSHA = "abc123"
		}
		payload, err := preservingPayload("t", o)
		if err != nil {
			t.Fatalf("%s: the engine refused its own record: %v", kind, err)
		}
		raw, _ := json.Marshal(payload)
		found := session.FindInterrupted([]event.Event{
			{TaskID: "t", Kind: event.TaskCreated, Summary: "a task"},
			{TaskID: "t", Kind: event.WorkflowFailed, Summary: "preserved", Payload: raw},
		})
		if len(found) != 1 {
			t.Fatalf("%s: the durable reader does not honour a kind this engine emits", kind)
		}
	}
	// And the validation is KIND-SPECIFIC, not universal. An authority
	// obligation must not be required to carry a candidate -- that boundary is
	// reached before one exists -- and must not be allowed to carry one either.
	if _, err := preservingPayload("t", ContinuationObligation{
		TaskID: "t", Kind: ContinuationAuthorityReentry, Reason: "r", CandidateBaseSHA: "abc123"}); err == nil {
		t.Fatal("an authority obligation carrying a candidate identity was admitted")
	}
	if _, err := preservingPayload("t", ContinuationObligation{
		TaskID: "t", Kind: ContinuationRestorationRefused, Reason: "r"}); err == nil {
		t.Fatal("a restoration obligation with no candidate identity was admitted")
	}
	// Binding is checked against the task being terminated, not against the
	// record's own claim about itself.
	if _, err := preservingPayload("t", ContinuationObligation{
		TaskID: "other", Kind: ContinuationRestorationRefused, Reason: "r", CandidateBaseSHA: "abc"}); err == nil {
		t.Fatal("a record bound to another task preserved this one")
	}
	_ = roles.Implementer
}

// AN IMPLEMENTER THAT LAWFULLY PRODUCED NO DIFF DID NOT FAIL AT THE WORK.
//
// task-1789998421052523830, the first attempt at this objective: the implementer
// correctly refused to mutate without the architectural context it needed,
// produced no diff, and the engine recorded "implementor produced no candidate
// diff" -> INCOMPLETE/FAILED. It did the right thing and was recorded as having
// failed.
//
// The aggregate is POSITIVE on both sides -- there is at least one decline and
// the declines account for every failure -- so a worker error, an exclusion, an
// unavailable provider or any mixture stays on the path it already had.
func TestOnlyAnAllDeclinedImplementationPreservesTheTask(t *testing.T) {
	body := sourceOfFunc(t, "internal/workflow/engine.go", "func (e *Engine) implement(")
	branch := strings.Index(body, "ContinuationImplementationDeclined")
	if branch < 0 {
		t.Fatal("the implementer aggregate no longer distinguishes a lawful decline")
	}
	guard := body[:branch]
	for _, want := range []string{
		"len(declined) > 0",              // at least one positive decline
		"len(declined) == len(failures)", // and nothing else failed
		"len(ineligible) == 0",           // nobody was excluded or unavailable
	} {
		if !strings.Contains(guard, want) {
			t.Fatalf("the preserving branch is not positively guarded by %s", want)
		}
	}
	// The decline is recognised by its sentinel, never by reading a message.
	if !strings.Contains(body, "errors.Is(err, errImplementerDeclined)") {
		t.Fatal("a decline is classified by something other than its typed identity")
	}
	// ORDER. Non-convergence is answered by a re-plan and must keep its own
	// terminal; the generic failure must stay reachable after this branch.
	replan := strings.Index(body, "e.endNotConverged(")
	generic := strings.Index(body, "no bounded implementor produced an acceptable candidate")
	if replan < 0 || generic < 0 || !(replan < branch && branch < generic) {
		t.Fatalf("the preserving branch is out of order: replan=%d declined=%d generic=%d", replan, branch, generic)
	}
	// The refusal itself is untouched: an empty diff still ends this worker's
	// turn for a modify plan.
	if !strings.Contains(rawSource(t, "internal/workflow/engine.go"), `errors.New("implementor produced no candidate diff")`) {
		t.Fatal("the empty-diff refusal was weakened rather than reclassified")
	}
}

// sourceOfFunc is one function's TEXT, from its signature to the next top-level
// declaration. funcBody renders a token stream, which cannot see a string
// literal or the shape of a boolean guard -- the two things the assertions above
// are about.
func sourceOfFunc(t *testing.T, rel, signature string) string {
	t.Helper()
	src := rawSource(t, rel)
	start := strings.Index(src, signature)
	if start < 0 {
		t.Fatalf("%s no longer declares %s", rel, signature)
	}
	rest := src[start+len(signature):]
	if end := strings.Index(rest, "\nfunc "); end >= 0 {
		return rest[:end]
	}
	return rest
}

// THE RESTORATION CALL SITE CARRIES THE OBLIGATION, and the guard it wraps is
// unchanged.
func TestTheRestorationRefusalIsWrappedWithoutChangingTheGuard(t *testing.T) {
	resume := sourceOfFunc(t, "internal/workflow/engine.go", "func (e *Engine) Resume(")
	call := strings.Index(resume, "e.restoreTestEditGrants(")
	if call < 0 {
		t.Fatal("Resume no longer re-establishes the recorded grants")
	}
	after := resume[call:]
	if !strings.Contains(after[:strings.Index(after, "restoreProspectiveGrants")], "ContinuationRestorationRefused") {
		t.Fatal("the restoration refusal is not carried as a continuation obligation")
	}
	// The guard's own inputs and comparisons are not this change's business.
	restore := sourceOfFunc(t, "internal/workflow/testedit.go", "func (e *Engine) restoreTestEditGrants(")
	for _, required := range []string{
		"the pinned world authorises %d existing-test edit(s) and the record holds %d",
		"the pinned world does not authorise the recorded edit of %s",
		"the recorded base hash of %s does not match its bytes at the pinned world",
		"sameTestFacts(f.Facts, g.Facts)",
	} {
		if !strings.Contains(restore, required) {
			t.Fatalf("the exact-match guard lost %q", required)
		}
	}
	if strings.Contains(restore, "Continuation") {
		t.Fatal("the guard itself now knows about lifecycle preservation; its blast radius changed, not its rules")
	}
}

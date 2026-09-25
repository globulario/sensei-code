package workflow

// T2: a test artifact is governed by test evidence, not by a production-source
// mechanical property.
//
// The trace, at the exact boundary. Action.architecturalFiles() (consequence.go) returns
// "every planned file not under an operational grant", and unexaminedCoverageGap then
// asks the graph for source coverage over that set. A planned *_test.go leaves the set
// only by earning a testEditGrant, whose predicate is
// EXISTING_TEST_EDIT_ADMISSIBLE(F, S, W):
//
//	F is a planned *_test.go positively present at W
//	S is a planned file IN F's DIRECTORY that a derived anchor covers at W
//	F and S declare the SAME PACKAGE at W (an external p_test is foreign, no grant)
//
// The predicate's own contract says an unestablished case "leaves F ungranted, silently
// to routing". Silently is the defect: ungranted means the file falls back into the
// ARCHITECTURAL set, and a test file is then reported as production source the graph has
// not examined — `coverage-unexamined`, whose remedy talks about graph coverage that no
// graph state could ever supply for a test file.
//
// Measured on W3: cmd/sensei-code/control_test.go was reported exactly that way.
//
// The repair is classification, not exclusion. A test file keeps a governance
// requirement; what changes is WHICH requirement, and what the gap says when it is
// unmet.

import (
	"strings"
	"testing"
)

func planAction(files ...string) Action {
	return Action{Files: files, Unexamined: files}
}

// 1. A covered production file plus a valid grant on its neighbouring test: the test is
// governed and raises no coverage gap.
func TestAGrantedTestIsGovernedAndRaisesNoCoverageGap(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	a.OperationalAuthority = []string{"internal/x/thing_test.go"}
	a.Unexamined = []string{"internal/x/thing_test.go"} // production file is examined
	if got := a.architecturalFiles(); len(got) != 1 || got[0] != "internal/x/thing.go" {
		t.Fatalf("architectural set = %v, want only the production file", got)
	}
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if open {
		t.Errorf("a granted test opened a gap: %s / %v", r.Condition, r.Gap)
	}
}

// 2. Production source still answers to source coverage.
func TestProductionSourceStillRequiresSourceCoverage(t *testing.T) {
	a := planAction("internal/x/thing.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("an unexamined production file raised no gap")
	}
	if r.Gap.Kind != "coverage-unexamined" {
		t.Errorf("gap kind = %q, want coverage-unexamined for production source", r.Gap.Kind)
	}
}

// 3. THE DEFECT. A test file with no valid grant must fail closed as a TEST-GOVERNANCE
// condition, never as a production coverage gap.
func TestAnUngrantedTestIsATestGovernanceGapNotACoverageGap(t *testing.T) {
	a := planAction("cmd/sensei-code/control_test.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("an ungranted test file raised no gap at all; a test must not become governance-free")
	}
	if r.Gap.Kind == "coverage-unexamined" {
		t.Fatalf("a test file was reported as unexamined production source: %s", r.Condition)
	}
	if r.Gap.Kind != "test-governance-unestablished" {
		t.Fatalf("gap kind = %q, want test-governance-unestablished", r.Gap.Kind)
	}
	if r.Basis != BasisLacksKnowledge {
		t.Errorf("basis = %v, want BasisLacksKnowledge", r.Basis)
	}
	// The remedy must not send anyone to the graph: no graph state can create a test
	// grant, so recommending a rebuild would be a true-sounding instruction that cannot
	// work.
	remedy := remedyForGap(r.Gap, "/src/sensei-code", "github.com/globulario/sensei-code")
	for _, forbidden := range []string{"sensei build", "sensei import", "rebuild", "refresh"} {
		if strings.Contains(strings.ToLower(remedy), forbidden) {
			t.Errorf("the remedy recommends %q for a test-governance gap: %s", forbidden, remedy)
		}
	}
	if !strings.Contains(remedy, "control_test.go") {
		t.Errorf("the remedy does not name the artifact: %s", remedy)
	}
}

// 4. A mixed plan is covered by two different evidence classes at once.
func TestAMixedPlanUsesTwoEvidenceClasses(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	a.OperationalAuthority = []string{"internal/x/thing_test.go"}
	a.Unexamined = nil // production file examined
	if _, open := unexaminedCoverageGap(a, blindSpotReading{}); open {
		t.Error("a plan whose production file is examined and whose test is granted still opened a gap")
	}
}

// 5. Naming a production file *_test.go cannot silently make it exempt. Under existing
// repository semantics a *_test.go IS a test artifact — so the protection is that it
// still needs test-governance evidence, and is never simply skipped.
func TestATestSuffixDoesNotCreateAnExemptArtifact(t *testing.T) {
	a := planAction("internal/x/sneaky_test.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("a *_test.go file with no grant was exempted entirely")
	}
	if r.Gap.Kind != "test-governance-unestablished" {
		t.Errorf("gap kind = %q", r.Gap.Kind)
	}
}

// 6. Non-test Go files that merely LOOK testish are production source.
func TestFixtureAndGeneratedFilesAreNotTests(t *testing.T) {
	for _, f := range []string{
		"internal/x/testdata_helper.go", // "test" in the name, not a _test.go
		"internal/x/test_helpers.go",    // prefix, not suffix
		"internal/x/thing.pb.go",        // generated
	} {
		a := planAction(f)
		r, open := unexaminedCoverageGap(a, blindSpotReading{})
		if !open {
			t.Errorf("%s raised no gap", f)
			continue
		}
		if r.Gap.Kind != "coverage-unexamined" {
			t.Errorf("%s was classified as a test artifact (kind=%s)", f, r.Gap.Kind)
		}
	}
}

// 7 & 8 are properties of the GRANT predicate, which is where the production-evidence
// relation lives. They are asserted against it directly in testedit_test.go's existing
// suite; here we assert the classifier consequence: a plan whose grant list does not
// include the test file gets the test-governance gap, which is exactly what "the
// supporting production evidence was lost" produces.
func TestLosingTheGrantProducesTheTestGovernanceGap(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	a.OperationalAuthority = nil // the grant is gone: its covered subject no longer derives
	a.Unexamined = []string{"internal/x/thing_test.go"}
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("losing the grant left the test ungoverned")
	}
	if r.Gap.Kind != "test-governance-unestablished" {
		t.Errorf("gap kind = %q, want the test-governance gap", r.Gap.Kind)
	}
	if len(r.Gap.Scope) != 1 || r.Gap.Scope[0] != "internal/x/thing_test.go" {
		t.Errorf("gap scope = %v, want only the test file", r.Gap.Scope)
	}
}

// A plan with BOTH an unexamined production file and an ungranted test must not hide
// either: the production gap is the one that blocks, and the test gap must still be
// visible in its own terms rather than absorbed.
func TestBothGapsAreReportedInTheirOwnTerms(t *testing.T) {
	a := planAction("internal/x/thing.go", "internal/x/thing_test.go")
	r, open := unexaminedCoverageGap(a, blindSpotReading{})
	if !open {
		t.Fatal("no gap at all")
	}
	if r.Gap.Kind != "coverage-unexamined" {
		t.Errorf("with production source unexamined the blocking gap should be the source one, got %q", r.Gap.Kind)
	}
	for _, f := range r.Gap.Scope {
		if strings.HasSuffix(f, "_test.go") {
			t.Errorf("a test file was pulled into the production coverage scope: %v", r.Gap.Scope)
		}
	}
}

// The disposal path must carry the TEST gap's own scope and remedy. Reading the
// unexamined ARCHITECTURAL files here was right while one out-of-band gap existed; a
// test-governance gap is about artifacts deliberately absent from that set, so it would
// have found nothing missing and escalated to a human for evidence no human can supply.
func TestTheTestGovernanceGapIsDisposedWithItsOwnScopeAndRemedy(t *testing.T) {
	if closureOwnerFor(gapTestGovernanceUnestablished) != closureOwnerOutOfBand {
		t.Fatal("a test-governance gap is not closable by reasoning and must be typed out-of-band")
	}
	gap := GapIdentity{Kind: gapTestGovernanceUnestablished, Scope: []string{"cmd/sensei-code/control_test.go"}}
	remedy := remedyForGap(gap, "/src/sensei-code", "github.com/globulario/sensei-code")
	if !strings.Contains(remedy, "no graph operation can establish this") {
		t.Errorf("the remedy does not say the graph cannot supply this: %s", remedy)
	}
	low := strings.ToLower(remedy)
	if !strings.Contains(low, "same") || !strings.Contains(low, "package") || !strings.Contains(low, "director") {
		t.Errorf("the remedy does not name the relation that is missing: %s", remedy)
	}
	// And the source remedy is unchanged for the source gap.
	src := remedyForGap(GapIdentity{Kind: "coverage-unexamined", Scope: []string{"a.go"}}, "/src/x", "example.com/d")
	if !strings.Contains(src, "sensei import --refresh") {
		t.Errorf("the source remedy changed: %s", src)
	}
}

// ── Required-test observations correlated with broker execution ──────────────
//
// LAW: a required test is discharged by being EXECUTED and PASSING against the
// exact candidate, not by appearing in its diff. The diff audit's "omitted from
// the supplied diff" finding is an OBSERVATION, preserved as reported; it is
// satisfied only by the broker's record under the SAME required-test id, for
// the SAME candidate content.

const (
	rtCandidate = "task-rt"
	rtDigest    = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	rtFile      = "internal/workflow/governed_record_coverage_test.go"
	rtAudit     = rtFile + ":TestTheAuditRecordCarriesItsRequest"
	rtCallSites = rtFile + ":TestEveryGovernedCallSiteIsClassified"
)

// omittedObservation is the finding the diff audit emits for a required test
// whose file the candidate did not touch, in the audit's own field names.
func omittedObservation(id string) auditObservation {
	return auditObservation{
		RecordID: id, RecordClass: "required_test", Disposition: "review", FilePath: rtFile,
		Explanation: "required test " + id + " (defined in " + rtFile + ") is omitted from the supplied diff",
	}
}

// brokerRecord is a broker record of one named test. passed also sets the
// command's outcome, so a record is consistent unless a test says otherwise.
func brokerRecord(id string, executed, passed bool, candidateID, digest string) requiredTestRun {
	r := requiredTestRun{ID: id, CandidateID: candidateID, DiffDigest: digest, Executed: executed, Passed: passed}
	r.Evidence.Kind = "test"
	r.Evidence.Command = "go"
	r.Evidence.ExecutedBy = "sensei-code execution broker"
	r.Evidence.CandidateID = candidateID
	r.Evidence.DiffDigest = digest
	r.Evidence.Outcome = "candidate-failure"
	if passed {
		r.Evidence.Outcome = "passed"
	}
	return r
}

// W1 THE MEASURED CASE. The candidate touched engine.go, both bound required
// tests were omitted from its diff, and the broker executed both by name and
// they passed at this exact candidate. Both observations are preserved AND
// recorded satisfied, and the reviewer's evidence says so.
//
// Fails if: correlation does not happen at all (the pre-repair behaviour), the
// observation is replaced rather than preserved, or the review evidence does
// not carry the satisfied state.
func TestW1AnOmittedRequiredTestProvenPassingIsSatisfiedInReviewEvidence(t *testing.T) {
	findings := []auditObservation{omittedObservation(rtAudit), omittedObservation(rtCallSites)}
	runs := []requiredTestRun{
		brokerRecord(rtAudit, true, true, rtCandidate, rtDigest),
		brokerRecord(rtCallSites, true, true, rtCandidate, rtDigest),
	}
	got := correlateRequiredTests(findings, runs, rtCandidate, rtDigest)
	if len(got) != 2 {
		t.Fatalf("every required-test observation must be correlated, got %d: %+v", len(got), got)
	}
	for i, c := range got {
		if c.ID != findings[i].RecordID {
			t.Errorf("observation %d correlated under %q, want %q", i, c.ID, findings[i].RecordID)
		}
		if !c.Satisfied {
			t.Errorf("%s executed and passed at this candidate and was not satisfied: %+v", c.ID, c)
		}
		if c.Observation != findings[i] {
			t.Errorf("the audit observation was not preserved as reported: %+v", c.Observation)
		}
		if c.Run == nil || c.Run.ID != c.ID {
			t.Errorf("%s is satisfied without carrying the broker record that satisfied it", c.ID)
		}
	}
	text := reviewValidationEvidence("VALIDATION EVIDENCE for candidate "+rtCandidate, runs, got, rtCandidate, rtDigest)
	for _, want := range []string{"SATISFIED", rtAudit, rtCallSites, findings[0].Explanation, "VALIDATION EVIDENCE"} {
		if !strings.Contains(text, want) {
			t.Errorf("the review evidence does not carry %q:\n%s", want, text)
		}
	}
	for _, p := range requiredTestResults(got) {
		if !p.Satisfied || !p.Executed || !p.Passed {
			t.Errorf("the task-evidence projection lost the result: %+v", p)
		}
	}

	// Drive it to the terminal: the packet the engine hands the independent
	// reviewer, and the prompt that reviewer actually reads. Both the broker's
	// per-test records and the correlation must arrive there, beside the audit
	// observations still reported as the audit made them.
	audit := "SENSEI DIFF AUDIT decision: review\n- [review] " + findings[0].Explanation + "\n- [review] " + findings[1].Explanation
	packet := reviewPacket(taskContext{}, reviewBinding(), certifiedStart{}, "the plan", "a diff", audit, text)
	if packet.Validation != text {
		t.Fatalf("the review packet does not carry the correlated validation evidence:\n%s", packet.Validation)
	}
	prompt := reviewPrompt(packet)
	for _, want := range []string{
		"REQUIRED TESTS executed by name by the execution broker",
		rtAudit + " — executed, PASSED", rtCallSites + " — executed, PASSED",
		"SATISFIED    " + rtAudit, "SATISFIED    " + rtCallSites,
		findings[0].Explanation, findings[1].Explanation,
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the reviewer's prompt does not carry %q", want)
		}
	}
}

// W2 CONTROL. A required test that did not run leaves the observation
// outstanding -- whether the broker has no record at all, or recorded a zero
// exit in which the named test itself never ran. Asserts the ABSENCE of any
// satisfied marking.
//
// Fails if: a missing record, or Executed=false under a zero exit, is read as
// satisfied anywhere -- in the typed result, the projection or the text.
func TestW2AnUnexecutedRequiredTestIsNotDischarged(t *testing.T) {
	findings := []auditObservation{omittedObservation(rtAudit), omittedObservation(rtCallSites)}
	notRun := brokerRecord(rtAudit, false, false, rtCandidate, rtDigest)
	notRun.Evidence.Outcome = "passed" // the command exited zero; the named test did not run
	got := correlateRequiredTests(findings, []requiredTestRun{notRun}, rtCandidate, rtDigest)
	if len(got) != 2 {
		t.Fatalf("an observation with no execution record must still be reported, got %d", len(got))
	}
	for _, c := range got {
		if c.Satisfied {
			t.Errorf("%s did not execute and was satisfied: %+v", c.ID, c)
		}
		if c.Observation.Disposition != "review" {
			t.Errorf("the audit observation was altered: %+v", c.Observation)
		}
	}
	if got[1].Run != nil {
		t.Errorf("an observation with no broker record was given one: %+v", got[1].Run)
	}
	text := reviewValidationEvidence("", []requiredTestRun{notRun}, got, rtCandidate, rtDigest)
	if strings.Contains(text, "SATISFIED") {
		t.Fatalf("an unexecuted required test is marked satisfied in the review evidence:\n%s", text)
	}
	if !strings.Contains(text, "OUTSTANDING") {
		t.Fatalf("the unexecuted required test is not reported outstanding:\n%s", text)
	}
	for _, p := range requiredTestResults(got) {
		if p.Satisfied {
			t.Errorf("the task-evidence projection marks an unexecuted test satisfied: %+v", p)
		}
	}
}

// W3 CONTROL. A required test that ran and FAILED is not discharged by having
// executed, and the observation stays exactly as the audit reported it -- this
// repair gives the candidate nothing for merely running something.
//
// Fails if: Executed alone discharges, or a record claiming Passed over a
// failed command outcome is believed.
func TestW3AFailingRequiredTestIsNotDischarged(t *testing.T) {
	findings := []auditObservation{omittedObservation(rtAudit)}
	failed := brokerRecord(rtAudit, true, false, rtCandidate, rtDigest)
	got := correlateRequiredTests(findings, []requiredTestRun{failed}, rtCandidate, rtDigest)
	if len(got) != 1 || got[0].Satisfied {
		t.Fatalf("a required test that ran and failed was discharged: %+v", got)
	}
	if got[0].Observation != findings[0] {
		t.Fatalf("the audit observation did not survive a failing run: %+v", got[0].Observation)
	}
	if !strings.Contains(strings.ToLower(got[0].Reason), "fail") {
		t.Errorf("the outstanding reason does not say the test failed: %q", got[0].Reason)
	}

	// A record that says Passed while the command that ran it failed is not a pass.
	inconsistent := brokerRecord(rtAudit, true, true, rtCandidate, rtDigest)
	inconsistent.Evidence.Outcome = "candidate-failure"
	if c := correlateRequiredTests(findings, []requiredTestRun{inconsistent}, rtCandidate, rtDigest); c[0].Satisfied {
		t.Fatal("a Passed flag over a failed execution discharged the required test")
	}
}

// W4 THE CORRELATION IS BY IDENTITY, NOT BY COUNT. The audit named one test and
// the broker passed a different one: equal counts, same file, wrong id. Nor
// does the right id at other candidate content discharge anything.
//
// Fails if: correlation is by count, position, file, or ignores the binding.
func TestW4CorrelationIsByRequiredTestIdNotByCount(t *testing.T) {
	findings := []auditObservation{omittedObservation(rtAudit)}
	other := brokerRecord(rtCallSites, true, true, rtCandidate, rtDigest) // same file, different test
	got := correlateRequiredTests(findings, []requiredTestRun{other}, rtCandidate, rtDigest)
	if len(got) != 1 || got[0].Satisfied {
		t.Fatalf("a different required test's pass discharged the named one: %+v", got)
	}
	if got[0].Run != nil {
		t.Fatalf("the named test was correlated with another test's record: %+v", got[0].Run)
	}

	// Same count, swapped positions: each must still find its own record.
	findings = []auditObservation{omittedObservation(rtAudit), omittedObservation(rtCallSites)}
	runs := []requiredTestRun{
		brokerRecord(rtCallSites, true, true, rtCandidate, rtDigest),
		brokerRecord(rtAudit, true, false, rtCandidate, rtDigest),
	}
	got = correlateRequiredTests(findings, runs, rtCandidate, rtDigest)
	if got[0].ID != rtAudit || got[0].Satisfied {
		t.Errorf("the failing %s was satisfied by position: %+v", rtAudit, got[0])
	}
	if got[1].ID != rtCallSites || !got[1].Satisfied {
		t.Errorf("the passing %s was not matched by its own id: %+v", rtCallSites, got[1])
	}

	// The graph's class-qualified form is the same test; the canonical id matches it.
	qualified := omittedObservation("test:" + rtAudit)
	if c := correlateRequiredTests([]auditObservation{qualified}, []requiredTestRun{brokerRecord(rtAudit, true, true, rtCandidate, rtDigest)}, rtCandidate, rtDigest); !c[0].Satisfied {
		t.Errorf("the class-qualified id did not match the canonical record: %+v", c[0])
	}

	// The right id at other bytes, or for another candidate, proves nothing here.
	for _, stale := range []requiredTestRun{
		brokerRecord(rtAudit, true, true, rtCandidate, "sha256:other"),
		brokerRecord(rtAudit, true, true, "task-other", rtDigest),
	} {
		if c := correlateRequiredTests([]auditObservation{omittedObservation(rtAudit)}, []requiredTestRun{stale}, rtCandidate, rtDigest); c[0].Satisfied {
			t.Errorf("a record bound to %s at %s discharged the test for this candidate", stale.CandidateID, stale.DiffDigest)
		}
	}
}

// W5 THE TWO STATES RENDER DIFFERENTLY. "not edited but proven passing" and
// "not edited and not proven" are different typed states and different text.
//
// Fails if: both states share a State value, or render to the same line.
func TestW5ProvenAndUnprovenOmissionsRenderDifferently(t *testing.T) {
	proven := correlateRequiredTests([]auditObservation{omittedObservation(rtAudit)},
		[]requiredTestRun{brokerRecord(rtAudit, true, true, rtCandidate, rtDigest)}, rtCandidate, rtDigest)[0]
	unproven := correlateRequiredTests([]auditObservation{omittedObservation(rtAudit)}, nil, rtCandidate, rtDigest)[0]
	if proven.State == "" || unproven.State == "" || proven.State == unproven.State {
		t.Fatalf("the two states are not distinct typed values: %q vs %q", proven.State, unproven.State)
	}
	a := renderRequiredTestEvidence([]correlatedRequiredTest{proven}, rtCandidate, rtDigest)
	b := renderRequiredTestEvidence([]correlatedRequiredTest{unproven}, rtCandidate, rtDigest)
	if a == b {
		t.Fatalf("proven and unproven omissions render identically:\n%s", a)
	}
	if !strings.Contains(a, "not edited, proven passing") || strings.Contains(a, "not proven") {
		t.Errorf("the proven omission does not read as proven passing:\n%s", a)
	}
	if !strings.Contains(b, "not edited, not proven") || strings.Contains(b, "SATISFIED") {
		t.Errorf("the unproven omission does not read as not proven:\n%s", b)
	}
	// Both keep the audit's observation in view.
	for _, s := range []string{a, b} {
		if !strings.Contains(s, "is omitted from the supplied diff") {
			t.Errorf("the rendering dropped the audit observation:\n%s", s)
		}
	}
}

// W6 CONTROL. Editing the required test is still not required. Correlation
// takes no diff and no changed paths, so an untouched test file cannot be held
// against the candidate; and nothing tells anyone to edit the test to clear it.
//
// Fails if: the unproven remedy steers toward editing the test file, or the
// review evidence stops saying that editing it is not required.
func TestW6EditingTheRequiredTestIsNotRequired(t *testing.T) {
	proven := correlateRequiredTests([]auditObservation{omittedObservation(rtAudit)},
		[]requiredTestRun{brokerRecord(rtAudit, true, true, rtCandidate, rtDigest)}, rtCandidate, rtDigest)
	unproven := correlateRequiredTests([]auditObservation{omittedObservation(rtCallSites)}, nil, rtCandidate, rtDigest)
	if !proven[0].Satisfied {
		t.Fatal("a proven-passing test whose file the candidate never touched was penalised")
	}
	text := renderRequiredTestEvidence(append(proven, unproven...), rtCandidate, rtDigest)
	if !strings.Contains(text, "editing the required test's file is not required") {
		t.Errorf("the review evidence does not state that editing the test is not required:\n%s", text)
	}
	low := strings.ToLower(unproven[0].Reason + "\n" + text)
	for _, incentive := range []string{"edit the test", "modify the test", "add the test file", "touch the test"} {
		if strings.Contains(low, incentive) {
			t.Errorf("the evidence creates an incentive to edit a required test (%q):\n%s", incentive, text)
		}
	}
	// Observations of other classes are not required-test observations and are
	// neither correlated nor altered.
	other := auditObservation{RecordID: "invariant:x", RecordClass: "invariant", Disposition: "block", FilePath: "internal/workflow/engine.go"}
	if c := correlateRequiredTests([]auditObservation{other}, nil, rtCandidate, rtDigest); len(c) != 0 {
		t.Errorf("a non-required-test observation was correlated: %+v", c)
	}
}

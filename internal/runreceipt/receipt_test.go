package runreceipt

import (
	"reflect"
	"strings"
	"testing"
)

// TestTheZeroValueIsNotAFact: an unset Value carries State "", which is not one
// of the four. A receipt must STATE every fact, including an absence, rather
// than let a struct's zero value stand in for one.
func TestTheZeroValueIsNotAFact(t *testing.T) {
	if (Value{}).State.Valid() {
		t.Fatal("the zero Value must not read as a defined state")
	}
	rec := completeReceipt()
	rec.ReviewerExecutable = Value{}
	if state, _ := rec.Completeness(); state != Incomplete {
		t.Fatalf("state = %s: an unset value is not an absence anyone recorded", state)
	}
}

func TestKnowledgeRequiresAStatedSource(t *testing.T) {
	if v := MeasuredValue("f01592b0", ""); v.State != Malformed {
		t.Fatalf("an unsourced claim of knowledge = %v, want MALFORMED", v)
	}
	if v := MeasuredValue("   ", "git rev-parse HEAD"); v.State != Unknown {
		t.Fatalf("a blank measurement = %v, want UNKNOWN", v)
	}
	// A caller can bypass the constructor by writing the struct directly, so
	// validation must catch a sourceless claim wherever it appears.
	rec := completeReceipt()
	rec.BaseCommit = Value{Text: "f01592b0", State: Known}
	state, missing := rec.Completeness()
	if state != Incomplete {
		t.Fatalf("state = %s, want INCOMPLETE for a sourceless claim", state)
	}
	if len(missing) == 0 || !strings.Contains(strings.Join(missing, " "), "no stated source") {
		t.Fatalf("missing = %v, want the sourceless claim named", missing)
	}
}

func TestAMissingRequiredFieldIsIncompleteAndNamedNeverDefaulted(t *testing.T) {
	rec := completeReceipt()
	rec.GraphDigest = UnknownValue("the run did not report it")
	state, missing := rec.Completeness()
	if state != Incomplete {
		t.Fatalf("state = %s, want INCOMPLETE", state)
	}
	if len(missing) != 1 || !strings.Contains(missing[0], "graph_digest") {
		t.Fatalf("missing = %v, want the field named", missing)
	}
}

// TestVOIDCannotBeTheOutcomeOfACompleteReceipt is the adversarial case: the
// constants omit VOID, but omission is not prevention. Outcome membership is
// read by ENUMERATION -- an unrecognised string is invalid, not a new value.
func TestVOIDCannotBeTheOutcomeOfACompleteReceipt(t *testing.T) {
	for _, bad := range []Outcome{"VOID", "void", "ADMITTED", "anything at all", "COMPLETE"} {
		rec := completeReceipt()
		rec.Outcome = bad
		state, missing := rec.Completeness()
		if state != Incomplete {
			t.Fatalf("outcome %q yielded %s; VOID and friends must never survive validation", bad, state)
		}
		if !strings.Contains(strings.Join(missing, " "), "not a value this schema defines") {
			t.Fatalf("outcome %q: missing = %v", bad, missing)
		}
		if bad.Valid() {
			t.Fatalf("%q must not be a valid outcome", bad)
		}
	}
}

// TestAnUnknownOutcomeIsRepresentableButNeverComplete keeps both halves: a
// reader must be able to SAY the record does not state what happened, and such
// a record must not pass as a complete account of a governed run.
func TestAnUnknownOutcomeIsRepresentableButNeverComplete(t *testing.T) {
	if !OutcomeUnknown.Valid() {
		t.Fatal("UNKNOWN must be representable")
	}
	if OutcomeUnknown.SufficientForComplete() {
		t.Fatal("UNKNOWN must not be sufficient for COMPLETE")
	}
	rec := completeReceipt()
	rec.Outcome = OutcomeUnknown
	if state, _ := rec.Completeness(); state != Incomplete {
		t.Fatalf("state = %s, want INCOMPLETE", state)
	}
}

// TestACompleteRecordOfAnUnreviewedRunIsNotVoid is the fourth C5 amendment as a
// test: completeness and outcome are orthogonal, and an unwelcome outcome must
// never be converted into an instrument verdict.
func TestACompleteRecordOfAnUnreviewedRunIsNotVoid(t *testing.T) {
	rec := completeReceipt()
	rec.ReviewVerdict = UnknownValue("no provider produced a bounded verdict")
	rec.ReviewedDigest = UnknownValue("no provider produced a bounded verdict")
	rec.ReviewedTree = UnknownValue("no provider produced a bounded verdict")
	rec.Attempts = nil
	rec.Outcome = OutcomeUnreviewed
	state, missing := rec.Completeness()
	if state != Complete {
		t.Fatalf("state = %s (%v), want COMPLETE", state, missing)
	}
}

func TestACompleteRecordOfAFailedRunIsAlsoComplete(t *testing.T) {
	rec := completeReceipt()
	rec.ReviewVerdict = UnknownValue("the run ended before a verdict")
	rec.ReviewedDigest = UnknownValue("the run ended before a verdict")
	rec.ReviewedTree = UnknownValue("the run ended before a verdict")
	rec.Attempts = nil
	rec.Outcome = OutcomeFailed
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("state = %s (%v)", state, missing)
	}
}

func TestVerificationDistinguishesDisagreementFromUnmeasurability(t *testing.T) {
	rec := completeReceipt()
	rec.CandidateCommit = Value{State: Malformed, Detail: "reported as a number"}
	got := rec.Verify(func(field string) (string, bool) {
		switch field {
		case "base_commit":
			return "a-different-commit", true
		case "plan_digest":
			return "", false
		}
		return recordedValue(rec, field), true
	})
	kinds := map[string]MismatchKind{}
	for _, m := range got {
		kinds[m.Field] = m.Kind
	}
	if kinds["base_commit"] != MismatchDisagreement {
		t.Errorf("base_commit kind = %q, want DISAGREEMENT", kinds["base_commit"])
	}
	if kinds["plan_digest"] != MismatchUnrecomputable {
		t.Errorf("plan_digest kind = %q, want UNRECOMPUTABLE: silence is not agreement", kinds["plan_digest"])
	}
	// candidate_commit is not required, so a malformed optional field is not a
	// verification failure -- but a required malformed one would be.
	rec.GraphDigest = Value{State: Malformed, Detail: "reported as an array"}
	for _, m := range rec.Verify(func(string) (string, bool) { return "", true }) {
		if m.Field == "graph_digest" && m.Kind != MismatchRecordedMalformed {
			t.Errorf("graph_digest kind = %q, want RECORDED_MALFORMED", m.Kind)
		}
	}
}

// TestAReceiptCannotAdmitAnything holds the bootstrap boundary. The receipt
// reports evidence; it may not grant authority to itself.
func TestAReceiptCannotAdmitAnything(t *testing.T) {
	typ := reflect.TypeOf(Receipt{})
	for i := 0; i < typ.NumMethod(); i++ {
		name := strings.ToLower(typ.Method(i).Name)
		for _, forbidden := range []string{"admit", "authorize", "authorise", "grant", "approve"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("Receipt.%s: a receipt reports evidence, it does not confer authority", typ.Method(i).Name)
			}
		}
	}
	for _, o := range []Outcome{OutcomeAccepted, OutcomeRefused, OutcomeFailed, OutcomeUnreviewed, OutcomeStopped, OutcomeUnknown} {
		if strings.Contains(strings.ToLower(string(o)), "admit") {
			t.Fatalf("outcome %q would let a run admit itself", o)
		}
	}
}

func completeReceipt() Receipt {
	return Receipt{
		Schema:                    SchemaVersion,
		FormatterMutationState:    MeasuredValue(string(FormatterUnchanged), "no formatter is configured"),
		ExecutionBudget:           UnknownValue("no execution budget expired"),
		DeferredQuestion:          UnknownValue("no authority question was deferred"),
		PlanState:                 PlanPresent,
		CandidateState:            CandidatePresent,
		CandidateCommit:           MeasuredValue("cccccccccccccccccccccccccccccccccccccccc", "git rev-parse refs/heads/sensei-code/task-1"),
		CandidateTree:             MeasuredValue("tttttttttttttttttttttttttttttttttttttttt", "git rev-parse refs/heads/sensei-code/task-1^{tree}"),
		CandidateFirstParent:      MeasuredValue("f01592b0f0828605ed254047fc064f41dacc78f2", "git rev-parse refs/heads/sensei-code/task-1^1"),
		CandidateDigest:           MeasuredValue("b4f471f096d13f2b", "sha256(candidate diff)"),
		ReviewerProvider:          MeasuredValue("codex", "event:review.completed.payload.provenance.provider"),
		ReviewVerdict:             MeasuredValue("accept", "event:review.completed.payload.decision"),
		ReviewedDigest:            MeasuredValue("b4f471f096d13f2b", "event:review.completed.payload.provenance.candidate_digest"),
		ReviewedTree:              MeasuredValue("tttttttttttttttttttttttttttttttttttttttt", "the candidate tree the verdict's envelope names"),
		CandidateCommitDiffDigest: MeasuredValue("b4f471f096d13f2b", "sha256 of the canonical rendering of the minted object"),
		CandidateDigestRelation:   RelationMatch,
		Attempts: []Attempt{{
			Provider: MeasuredValue("codex", "event:agent.role.assigned.payload.provider"),
			Delivery: DeliveryValue(Delivered, "event:review.completed"),
			Verdict:  MeasuredValue("accept", "event:review.completed.payload.decision"),
			Digest:   MeasuredValue("b4f471f096d13f2b", "event:review.completed.payload.provenance.candidate_digest"),
			Tree:     MeasuredValue("tttttttttttttttttttttttttttttttttttttttt", "the candidate tree the verdict's envelope names"),
		}},
		GovernorCommit:       MeasuredValue("f01592b0f0828605ed254047fc064f41dacc78f2", "governor self-report"),
		GovernorBinarySHA256: MeasuredValue("7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea", "sha256:/path/to/governor"),
		BaseCommit:           MeasuredValue("f01592b0f0828605ed254047fc064f41dacc78f2", "git rev-parse HEAD"),
		PlanDigest:           MeasuredValue("990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554", "sha256:plan.json"),
		GraphDigest:          MeasuredValue("42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64", "awareness metadata"),
		ServingProducer:      MeasuredValue("pid 2375957 | sha256 1070ee8d", "readlink /proc/2375957/exe"),
		ReviewerExecutable:   UnknownValue("the governor did not measure the reviewer executable"),
		Terminal:             MeasuredValue("workflow.completed", "event:workflow.completed"),
		Outcome:              OutcomeAccepted,
	}
}

func recordedValue(r Receipt, field string) string {
	for _, f := range r.Fields() {
		if f.Name == field {
			return f.Value.Text
		}
	}
	return ""
}

// --- conditional evidence: C5's third amendment, carried into the product ---

func TestAPresentCandidateRequiresTheEvidenceThatDescribesIt(t *testing.T) {
	for _, drop := range []string{"candidate_tree", "candidate_first_parent", "candidate_digest", "candidate_commit"} {
		rec := completeReceipt()
		switch drop {
		case "candidate_tree":
			rec.CandidateTree = UnknownValue("not measured")
		case "candidate_first_parent":
			rec.CandidateFirstParent = UnknownValue("not measured")
		case "candidate_digest":
			rec.CandidateDigest = UnknownValue("not measured")
		case "candidate_commit":
			rec.CandidateCommit = UnknownValue("not measured")
		}
		state, missing := rec.Completeness()
		if state != Incomplete {
			t.Fatalf("PRESENT candidate missing %s yielded %s: a receipt may not name a candidate whose describing evidence disappeared", drop, state)
		}
		if !strings.Contains(strings.Join(missing, " "), drop) {
			t.Errorf("missing = %v, want %s named", missing, drop)
		}
	}
}

func TestACandidateNamedWhileClaimingNoneIsInconsistent(t *testing.T) {
	rec := completeReceipt()
	rec.CandidateState = CandidateNone
	state, missing := rec.Completeness()
	if state != Incomplete {
		t.Fatalf("state = %s, want INCOMPLETE", state)
	}
	if !strings.Contains(strings.Join(missing, " "), "contradicts the evidence") {
		t.Fatalf("missing = %v, want the contradiction named", missing)
	}
}

func TestACandidateStateThatCannotSayIsNeverComplete(t *testing.T) {
	if CandidateState("MAYBE").Valid() {
		t.Fatal("membership must be read by enumeration")
	}
	for _, bad := range []CandidateState{CandidateUnknown, "MAYBE", ""} {
		rec := completeReceipt()
		rec.CandidateState = bad
		if state, _ := rec.Completeness(); state != Incomplete {
			t.Fatalf("candidate_state %q yielded %s", bad, state)
		}
	}
}

// A read-only run legitimately has no candidate, and that is a fact rather than
// an absence of one.
func TestARunWithNoCandidateIsCompleteWhenItSaysSo(t *testing.T) {
	rec := completeReceipt()
	rec.CandidateState = CandidateNone
	rec.CandidateCommit = UnknownValue("the run produced no candidate")
	rec.CandidateTree = UnknownValue("the run produced no candidate")
	rec.CandidateFirstParent = UnknownValue("the run produced no candidate")
	rec.CandidateDigest = UnknownValue("the run produced no candidate")
	rec.CandidateCommitDiffDigest = UnknownValue("the run produced no candidate")
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("state = %s (%v)", state, missing)
	}
}

func TestABoundedVerdictRequiresTheEvidenceOfWhoGaveIt(t *testing.T) {
	for _, outcome := range []Outcome{OutcomeAccepted, OutcomeRefused} {
		for _, drop := range []string{"reviewer_provider", "review_verdict", "reviewed_digest", "no delivered attempt"} {
			rec := completeReceipt()
			rec.Outcome = outcome
			switch drop {
			case "reviewer_provider":
				rec.ReviewerProvider = UnknownValue("not measured")
			case "review_verdict":
				rec.ReviewVerdict = UnknownValue("not measured")
			case "reviewed_digest":
				rec.ReviewedDigest = UnknownValue("not measured")
			case "no delivered attempt":
				rec.Attempts = nil
			}
			state, missing := rec.Completeness()
			if state != Incomplete {
				t.Fatalf("%s without %s yielded %s: the outcome asserts a bounded verdict that nothing evidences", outcome, drop, state)
			}
			_ = missing
		}
	}
}

func TestUnreviewedWhileABoundedVerdictIsRecordedIsInconsistent(t *testing.T) {
	rec := completeReceipt()
	rec.Outcome = OutcomeUnreviewed // but the verdict fields are still measured
	state, missing := rec.Completeness()
	if state != Incomplete {
		t.Fatalf("state = %s, want INCOMPLETE", state)
	}
	if !strings.Contains(strings.Join(missing, " "), "contradicts the evidence") {
		t.Fatalf("missing = %v, want the contradiction named", missing)
	}
}

// --- every value, wherever it lives ---

func TestASourcelessClaimInsideAReviewerAttemptIsAlsoInvalid(t *testing.T) {
	rec := completeReceipt()
	rec.Attempts[0].Provider = Value{Text: "codex", State: Known}
	state, missing := rec.Completeness()
	if state != Incomplete {
		t.Fatalf("state = %s: attempts carry evidence too, and validation must reach them", state)
	}
	if !strings.Contains(strings.Join(missing, " "), "attempts[0].provider") {
		t.Fatalf("missing = %v, want the attempt value named", missing)
	}
}

func TestAKnownnessOutsideTheDefinedFourIsInvalid(t *testing.T) {
	if Knownness("CERTAIN").Valid() {
		t.Fatal("membership must be read by enumeration")
	}
	rec := completeReceipt()
	rec.ReviewerExecutable = Value{Text: "/usr/bin/codex", State: Knownness("CERTAIN")}
	state, missing := rec.Completeness()
	if state != Incomplete {
		t.Fatalf("state = %s: an invalid state on an OPTIONAL field must still be caught", state)
	}
	if !strings.Contains(strings.Join(missing, " "), "not a value this schema defines") {
		t.Fatalf("missing = %v", missing)
	}
}

func TestARequiredUnsupportedFieldIsReportedAsUnsupported(t *testing.T) {
	rec := completeReceipt()
	rec.GraphDigest = Value{State: Unsupported, Detail: "emitted under a newer schema"}
	var kind MismatchKind
	for _, m := range rec.Verify(func(string) (string, bool) { return "", true }) {
		if m.Field == "graph_digest" {
			kind = m.Kind
		}
	}
	if kind != MismatchRecordedUnsupported {
		t.Fatalf("graph_digest kind = %q, want RECORDED_UNSUPPORTED: collapsing it into RECORDED_UNKNOWN brings back prose-parsing", kind)
	}
}

// --- review binding: the last cluster ---

// TestDeliveryIsMeasuredNotABoolean closes the one place a raw Go zero value
// was still doing evidentiary work. `Delivered: false` could not distinguish a
// measured non-delivery from a field nobody set.
func TestDeliveryIsMeasuredNotABoolean(t *testing.T) {
	if (Attempt{}).DeliveredVerdict() {
		t.Fatal("an unset attempt must not read as having delivered")
	}
	rec := completeReceipt()
	rec.Attempts[0].Delivery = Value{Text: string(Delivered), State: Known} // no source
	if state, missing := rec.Completeness(); state != Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "attempts[0].delivery") {
		t.Fatalf("a sourceless delivery claim = %s %v", state, missing)
	}
	rec = completeReceipt()
	rec.Attempts[0].Delivery = DeliveryValue(DeliveryUnsaid, "the stream does not say")
	if state, _ := rec.Completeness(); state != Incomplete {
		t.Fatal("UNKNOWN delivery must never satisfy a bounded-review requirement")
	}
	rec = completeReceipt()
	rec.Attempts[0].Delivery = MeasuredValue("SORT_OF", "somewhere")
	if state, missing := rec.Completeness(); state != Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "not a value this schema defines") {
		t.Fatalf("delivery membership = %s %v", state, missing)
	}
}

// TestTheDeliveredAttemptMustBeTheOneTheReceiptDescribes: it is not enough that
// SOME attempt delivered. One measured fact must not support a different claim.
func TestTheDeliveredAttemptMustBeTheOneTheReceiptDescribes(t *testing.T) {
	for _, diverge := range []string{"provider", "verdict", "digest"} {
		rec := completeReceipt()
		switch diverge {
		case "provider":
			rec.Attempts[0].Provider = MeasuredValue("gemini", "event:agent.role.assigned.payload.provider")
		case "verdict":
			rec.Attempts[0].Verdict = MeasuredValue("revise", "event:review.completed.payload.decision")
		case "digest":
			rec.Attempts[0].Digest = MeasuredValue("BBBB", "event:review.completed.payload.provenance.candidate_digest")
		}
		state, missing := rec.Completeness()
		if state != Incomplete {
			t.Fatalf("attempt diverging in %s yielded %s: the delivering attempt must be the one described", diverge, state)
		}
		if !strings.Contains(strings.Join(missing, " "), "no DELIVERED attempt carries the same") {
			t.Errorf("%s: missing = %v", diverge, missing)
		}
	}
}

func TestTheReviewVocabularyIsClosedAndBoundToTheOutcome(t *testing.T) {
	rec := completeReceipt()
	rec.ReviewVerdict = MeasuredValue("banana", "event:review.completed.payload.decision")
	rec.Attempts[0].Verdict = MeasuredValue("banana", "event:review.completed.payload.decision")
	if state, missing := rec.Completeness(); state != Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "closed review vocabulary") {
		t.Fatalf("an invented decision = %s %v", state, missing)
	}
	// ACCEPTED must mean the reviewer said accept.
	rec = completeReceipt()
	rec.ReviewVerdict = MeasuredValue("revise", "event:review.completed.payload.decision")
	rec.Attempts[0].Verdict = MeasuredValue("revise", "event:review.completed.payload.decision")
	if state, missing := rec.Completeness(); state != Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "ACCEPTED while the review decision") {
		t.Fatalf("ACCEPTED with a revise decision = %s %v", state, missing)
	}
	// REFUSED covers revise and escalate, and only those.
	for _, d := range []ReviewDecision{DecisionRevise, DecisionEscalate} {
		rec := completeReceipt()
		rec.Outcome = OutcomeRefused
		rec.ReviewVerdict = MeasuredValue(string(d), "event:review.completed.payload.decision")
		rec.Attempts[0].Verdict = MeasuredValue(string(d), "event:review.completed.payload.decision")
		if state, missing := rec.Completeness(); state != Complete {
			t.Fatalf("REFUSED/%s = %s %v", d, state, missing)
		}
	}
	rec = completeReceipt()
	rec.Outcome = OutcomeRefused // verdict is still "accept"
	if state, _ := rec.Completeness(); state != Incomplete {
		t.Fatal("REFUSED with an accept decision must not be complete")
	}
}

func TestClaimingNoCandidateRequiresTheAbsenceToBeReadable(t *testing.T) {
	for _, st := range []Knownness{Malformed, Unsupported} {
		rec := completeReceipt()
		rec.CandidateState = CandidateNone
		rec.CandidateCommit = Value{State: st, Detail: "unreadable"}
		rec.CandidateTree = UnknownValue("no candidate")
		rec.CandidateFirstParent = UnknownValue("no candidate")
		rec.CandidateDigest = UnknownValue("no candidate")
		state, missing := rec.Completeness()
		if state != Incomplete {
			t.Fatalf("NONE with a %s candidate field yielded %s: an instrument that cannot read the thing may not claim it does not exist", st, state)
		}
		if !strings.Contains(strings.Join(missing, " "), "not unreadable") {
			t.Errorf("%s: missing = %v", st, missing)
		}
	}
}

// --- the plan axis: the third instance of conditional evidence -------------

func TestAPresentPlanRequiresItsIdentity(t *testing.T) {
	rec := completeReceipt()
	rec.PlanDigest = UnknownValue("not measured")
	state, missing := rec.Completeness()
	if state != Incomplete || !strings.Contains(strings.Join(missing, " "), "plan_digest") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
}

// A conversational answer genuinely has no plan. Requiring a digest for an
// artifact that does not exist is the wrong predicate, not rigour.
func TestARunWithNoPlanIsCompleteWhenItSaysSo(t *testing.T) {
	rec := completeReceipt()
	rec.PlanState = PlanNone
	rec.PlanDigest = UnknownValue("the architect replied instead of planning")
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("state = %s (%v)", state, missing)
	}
}

func TestAPlanNamedWhileClaimingNoneIsInconsistent(t *testing.T) {
	rec := completeReceipt()
	rec.PlanState = PlanNone // the digest is still measured
	state, missing := rec.Completeness()
	if state != Incomplete || !strings.Contains(strings.Join(missing, " "), "plan_state is NONE") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
}

func TestAPlanStateThatCannotSayIsNeverComplete(t *testing.T) {
	if PlanState("MAYBE").Valid() {
		t.Fatal("membership must be read by enumeration")
	}
	for _, bad := range []PlanState{PlanUnknown, "MAYBE", ""} {
		rec := completeReceipt()
		rec.PlanState = bad
		if state, _ := rec.Completeness(); state != Incomplete {
			t.Fatalf("plan_state %q yielded %s", bad, state)
		}
	}
}

// A human stop is a real outcome, not instrument incompleteness and not
// failure. COMPLETE / STOPPED is a fully meaningful record.
func TestAStoppedRunIsACompleteRecordOfARealOutcome(t *testing.T) {
	if !OutcomeStopped.Valid() || !OutcomeStopped.SufficientForComplete() {
		t.Fatal("STOPPED must be a valid outcome sufficient for a complete record")
	}
	rec := completeReceipt()
	rec.Outcome = OutcomeStopped
	rec.ReviewVerdict = UnknownValue("the human ended the run before a verdict")
	rec.ReviewedDigest = UnknownValue("the human ended the run before a verdict")
	rec.ReviewedTree = UnknownValue("the human ended the run before a verdict")
	rec.Attempts = nil
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("COMPLETE / STOPPED must be representable: %v", missing)
	}
	if OutcomeStopped == OutcomeFailed {
		t.Fatal("a human stop must never be recorded as a failure")
	}
}

// TestADeferredRunIsACompleteRecordOfARealOutcome closes R1.
//
// The first real governed run reached a human-owned authority boundary, the
// human declined to answer, the process exited -- and it emitted NOTHING,
// because that terminal was classified non-terminal on the grounds that the run
// is resumable. True inside the process model; false outside it.
func TestADeferredRunIsACompleteRecordOfARealOutcome(t *testing.T) {
	if !OutcomeDeferred.Valid() || !OutcomeDeferred.SufficientForComplete() {
		t.Fatal("DEFERRED must be a valid outcome sufficient for a complete record")
	}
	rec := completeReceipt()
	rec.Outcome = OutcomeDeferred
	rec.ReviewVerdict = UnknownValue("the run stopped at an authority boundary")
	rec.ReviewedDigest = UnknownValue("the run stopped at an authority boundary")
	rec.ReviewedTree = UnknownValue("the run stopped at an authority boundary")
	rec.Attempts = nil
	rec.DeferredQuestion = MeasuredValue(
		"Architectural authority reached a human-owned boundary. — graph coverage is absent for the planned file",
		"the authority decision the human declined to answer")
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("COMPLETE / DEFERRED must be representable: %v", missing)
	}
	if OutcomeDeferred == OutcomeFailed || OutcomeDeferred == OutcomeStopped {
		t.Fatal("a deferral is neither a failure nor a withdrawal")
	}
}

// A record that says a question was deferred without saying WHICH is the same
// shape as a candidate that cannot name its own commit.
func TestADeferredRunMustNameTheQuestion(t *testing.T) {
	rec := completeReceipt()
	rec.Outcome = OutcomeDeferred
	rec.ReviewVerdict = UnknownValue("stopped at an authority boundary")
	rec.ReviewedDigest = UnknownValue("stopped at an authority boundary")
	rec.ReviewedTree = UnknownValue("stopped at an authority boundary")
	rec.Attempts = nil
	rec.DeferredQuestion = UnknownValue("not recorded")
	state, missing := rec.Completeness()
	if state != Incomplete || !strings.Contains(strings.Join(missing, " "), "deferred_question") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
}

// A deferral did not also get judged.
func TestADeferredRunWithAVerdictIsInconsistent(t *testing.T) {
	rec := completeReceipt() // still carries a bounded verdict
	rec.Outcome = OutcomeDeferred
	rec.DeferredQuestion = MeasuredValue("a question", "the authority decision")
	state, missing := rec.Completeness()
	if state != Incomplete || !strings.Contains(strings.Join(missing, " "), "did not also get judged") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
}

// TestTheSchemaVersionPinsItsVocabulary makes a silent version drift impossible.
//
// DEFERRED once shipped under the v3 label: the bump was written, the
// replacement silently did not match, the commit message claimed v4, and a real
// run emitted a v4 feature under a v3 version. That is exactly the fabricated
// specimen the version comment warns about, produced by the author of the
// comment.
//
// Pinning the version ALONGSIDE the vocabulary means adding an outcome without
// moving the version fails here, rather than being caught by someone reading a
// receipt from a live run.
func TestTheSchemaVersionPinsItsVocabulary(t *testing.T) {
	const version = "sensei-code.governed-run-receipt/v6"
	if SchemaVersion != version {
		t.Fatalf("SchemaVersion = %q, pinned %q. If the vocabulary below changed, move BOTH.", SchemaVersion, version)
	}
	outcomes := []Outcome{OutcomeAccepted, OutcomeRefused, OutcomeFailed,
		OutcomeUnreviewed, OutcomeStopped, OutcomeDeferred, OutcomeTimedOut, OutcomeUnknown}
	for _, o := range outcomes {
		if !o.Valid() {
			t.Errorf("%q is enumerated here but not Valid()", o)
		}
	}
	// Every value Valid() accepts must be one this test names, or the
	// vocabulary grew without the version moving.
	for _, candidate := range []Outcome{"ADMITTED", "DEFERED", "PENDING", "VOID", "BLOCKED", "TIMEDOUT"} {
		if candidate.Valid() {
			t.Errorf("%q is valid but not pinned by this test", candidate)
		}
	}
	if len(outcomes) != 8 {
		t.Fatalf("%d outcomes pinned; if the set changed, the version must move with it", len(outcomes))
	}
}

// TestAReceiptMustSpeakTheLanguageItsVersionDefines gives the version string
// evidentiary meaning rather than making it decorative metadata.
func TestAReceiptMustSpeakTheLanguageItsVersionDefines(t *testing.T) {
	const v3 = "sensei-code.governed-run-receipt/v3"
	if err := SpeaksItsVersion(v3, OutcomeDeferred); err == nil {
		t.Fatal("v3 + DEFERRED must be invalid: DEFERRED was added in v4")
	}
	if err := SpeaksItsVersion(SchemaVersion, OutcomeDeferred); err != nil {
		t.Fatalf("v4 + DEFERRED must be valid: %v", err)
	}
	if err := SpeaksItsVersion(v3, OutcomeStopped); err != nil {
		t.Fatalf("v3 + STOPPED must be valid: %v", err)
	}
	// An unrecognised version is reported, never assumed permissive.
	if err := SpeaksItsVersion("sensei-code.governed-run-receipt/v99", OutcomeAccepted); err == nil {
		t.Fatal("an unknown schema version must not be treated as permissive")
	}
	// And a whole receipt wearing the wrong label is INCOMPLETE for that reason.
	rec := completeReceipt()
	rec.Schema = v3
	rec.Outcome = OutcomeDeferred
	rec.ReviewVerdict = UnknownValue("stopped at an authority boundary")
	rec.ReviewedDigest = UnknownValue("stopped at an authority boundary")
	rec.ReviewedTree = UnknownValue("stopped at an authority boundary")
	rec.Attempts = nil
	rec.DeferredQuestion = MeasuredValue("a question", "the authority decision")
	state, missing := rec.Completeness()
	joined := strings.Join(missing, " ")
	if state != Incomplete || !strings.Contains(joined, "not in the vocabulary") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
}

// A timeout is not a withdrawal: an expired budget and a human stepping back are
// different evidence, and a reader that cannot tell them apart learns the wrong
// causal fact about why work ended.
func TestATimedOutRunIsACompleteRecordOfARealOutcome(t *testing.T) {
	if !OutcomeTimedOut.Valid() || !OutcomeTimedOut.SufficientForComplete() {
		t.Fatal("TIMED_OUT must be a valid outcome sufficient for a complete record")
	}
	if OutcomeTimedOut == OutcomeStopped || OutcomeTimedOut == OutcomeFailed {
		t.Fatal("a timeout is neither a withdrawal nor a failure")
	}
	rec := completeReceipt()
	rec.Outcome = OutcomeTimedOut
	rec.ExecutionBudget = MeasuredValue("25m0s", "the -timeout this invocation was given")
	// A timeout AFTER a bounded verdict is the ordinary shape of a revision
	// cycle that ran out of budget, and must not read as a contradiction.
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("COMPLETE / TIMED_OUT after a REVISE must be representable: %v", missing)
	}
	// And it must name the budget it exhausted.
	rec.ExecutionBudget = UnknownValue("not recorded")
	if state, missing := rec.Completeness(); state != Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "execution_budget") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
	// v4 does not define TIMED_OUT.
	if err := SpeaksItsVersion("sensei-code.governed-run-receipt/v4", OutcomeTimedOut); err == nil {
		t.Fatal("v4 + TIMED_OUT must be invalid: TIMED_OUT was added in v5")
	}
}

// The formatter fact is instrumentation, and it obeys the same laws as every
// other fact: a closed vocabulary, a stated source, and no silent gap.
func TestTheFormatterMutationFactIsMeasuredAndClosed(t *testing.T) {
	for _, v := range []FormatterMutation{FormatterMutated, FormatterUnchanged, FormatterUnsaid} {
		if !v.Valid() {
			t.Errorf("%q is enumerated but not valid", v)
		}
	}
	for _, bad := range []FormatterMutation{"YES", "NO", "CHANGED", ""} {
		if bad.Valid() {
			t.Errorf("%q must not be a valid formatter fact", bad)
		}
	}
	// UNKNOWN is an acceptable VALUE; an unstated field is not.
	rec := completeReceipt()
	rec.FormatterMutationState = MeasuredValue(string(FormatterUnsaid), "validation had not run")
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("UNKNOWN must be an acceptable value: %v", missing)
	}
	rec.FormatterMutationState = UnknownValue("not recorded")
	if state, _ := rec.Completeness(); state != Incomplete {
		t.Fatal("a candidate that exists must STATE the formatter fact, even as UNKNOWN")
	}
	rec.FormatterMutationState = MeasuredValue("PROBABLY", "a guess")
	if state, missing := rec.Completeness(); state != Incomplete ||
		!strings.Contains(strings.Join(missing, " "), "formatter_mutation") {
		t.Fatalf("state=%s missing=%v", state, missing)
	}
}

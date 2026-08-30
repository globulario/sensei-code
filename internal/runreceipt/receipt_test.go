package runreceipt

import (
	"reflect"
	"strings"
	"testing"
)

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
	rec.Outcome = OutcomeUnreviewed
	state, missing := rec.Completeness()
	if state != Complete {
		t.Fatalf("state = %s (%v), want COMPLETE", state, missing)
	}
}

func TestACompleteRecordOfAFailedRunIsAlsoComplete(t *testing.T) {
	rec := completeReceipt()
	rec.ReviewVerdict = UnknownValue("the run ended before a verdict")
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
	for _, o := range []Outcome{OutcomeAccepted, OutcomeRefused, OutcomeFailed, OutcomeUnreviewed, OutcomeUnknown} {
		if strings.Contains(strings.ToLower(string(o)), "admit") {
			t.Fatalf("outcome %q would let a run admit itself", o)
		}
	}
}

func completeReceipt() Receipt {
	return Receipt{
		Schema:               SchemaVersion,
		GovernorCommit:       MeasuredValue("f01592b0f0828605ed254047fc064f41dacc78f2", "governor self-report"),
		GovernorBinarySHA256: MeasuredValue("7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea", "sha256:/path/to/governor"),
		BaseCommit:           MeasuredValue("f01592b0f0828605ed254047fc064f41dacc78f2", "git rev-parse HEAD"),
		PlanDigest:           MeasuredValue("990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554", "sha256:plan.json"),
		GraphDigest:          MeasuredValue("42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64", "awareness metadata"),
		ServingProducer:      MeasuredValue("pid 2375957 | sha256 1070ee8d", "readlink /proc/2375957/exe"),
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

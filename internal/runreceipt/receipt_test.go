package runreceipt

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

// corpus is the set of REAL governed-run logs this repository has preserved.
//
// C5 was voided by a parser validated only against specimens its author
// invented, while a log containing the exact crashing shape sat in this
// repository unexamined. So the corpus test is not optional and does not skip:
// a missing corpus is a failure, because a regression suite that quietly stops
// asking reality is how the fabricated specimen got in.
var corpus = []string{
	"../../experiments/c4-path-authority/runs/C4.log",
	"../../experiments/c5-witness-obligations/runs/C5.log",
}

func TestTheRealCorpusNeverCrashesTheReader(t *testing.T) {
	for _, path := range corpus {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("the preserved corpus must be readable, and %s was not: %v. "+
				"A parser tested only against invented specimens is what voided C5.", path, err)
		}
		rec := FromEvents(f)
		f.Close()
		if rec.Schema != SchemaVersion {
			t.Errorf("%s: schema %q", path, rec.Schema)
		}
		// Reading reality must produce measurements, not a wall of unknowns.
		if rec.BaseCommit.State != Known {
			t.Errorf("%s: base commit %s (%s)", path, rec.BaseCommit.State, rec.BaseCommit.Detail)
		}
		if rec.GraphDigest.State != Known {
			t.Errorf("%s: graph digest %s (%s)", path, rec.GraphDigest.State, rec.GraphDigest.Detail)
		}
		// Deliberately NOT logging rec.Outcome. C5 is a VOID witness, and its
		// semantic content is inadmissible as evidence about that run; printing
		// a run outcome derived from void bytes into a passing test's output is
		// how an inadmissible fact acquires a citable home. The corpus is here
		// to prove the reader is total, not to say what these runs did.
		t.Logf("%s -> base=%s attempts=%d diagnostics=%d",
			path, rec.BaseCommit.Text, len(rec.Attempts), len(rec.Diagnostics))
	}
}

// TestTheShapeThatVoidedC5IsAClassificationNotACrash pins the specific defect.
// payload.provenance is a string in one real event out of 9123, and an
// assumption that it was an object ended an expensive run.
func TestTheShapeThatVoidedC5IsAClassificationNotACrash(t *testing.T) {
	log := `{"kind":"mode.selected","payload":{"provenance":"submitted unattended with an externally supplied plan"}}
{"kind":"review.completed","payload":{"decision":"accept","provenance":"a string where an object was assumed"}}`
	rec := FromEvents(strings.NewReader(log))
	if rec.ReviewedDigest.State != Malformed {
		t.Fatalf("reviewed digest state = %s, want MALFORMED", rec.ReviewedDigest.State)
	}
	if !strings.Contains(rec.ReviewedDigest.Detail, "not an object") {
		t.Errorf("detail must say what arrived, got %q", rec.ReviewedDigest.Detail)
	}
	// The verdict itself was well formed, so it must still be measured: one
	// malformed neighbour does not erase a fact that was reported correctly.
	if rec.ReviewVerdict.State != Known || rec.Outcome != OutcomeAccepted {
		t.Errorf("verdict=%v outcome=%s", rec.ReviewVerdict, rec.Outcome)
	}
}

// TestNoSyntacticallyValidEventCanCrashExtraction feeds every JSON type into
// every nested position the reader touches.
func TestNoSyntacticallyValidEventCanCrashExtraction(t *testing.T) {
	shapes := []string{`null`, `"s"`, `12`, `true`, `[1,2]`, `{}`, `{"provider":[]}`, `{"decision":{}}`}
	kinds := []string{"review.completed", "agent.role.assigned", "sensei.result", "candidate.resolved", "plan.proposed", "wholly.unknown.kind", ""}
	for _, k := range kinds {
		for _, s := range shapes {
			line := `{"kind":"` + k + `","payload":` + s + `}`
			rec := FromEvents(strings.NewReader(line)) // must not panic
			if rec.Schema != SchemaVersion {
				t.Fatalf("kind=%q payload=%s produced no receipt", k, s)
			}
			for _, f := range rec.Fields() {
				switch f.Value.State {
				case Known, Unknown, Malformed, Unsupported:
				default:
					t.Fatalf("kind=%q payload=%s left %s in state %q", k, s, f.Name, f.Value.State)
				}
			}
		}
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
	// An empty string must not pass as a measurement.
	rec2 := completeReceipt()
	rec2.BaseCommit = KnownValue("   ")
	if state, _ := rec2.Completeness(); state != Incomplete {
		t.Fatalf("a blank value passed as a measurement: %s", state)
	}
}

// TestACompleteRecordOfAnUnreviewedRunIsNotVoid is the fourth C5 amendment as a
// test: completeness and outcome are orthogonal, and an unwelcome outcome must
// never be converted into an instrument verdict.
func TestACompleteRecordOfAnUnreviewedRunIsNotVoid(t *testing.T) {
	rec := completeReceipt()
	rec.ReviewVerdict = UnknownValue("no provider produced a bounded verdict")
	rec.Terminal = KnownValue("workflow.completed")
	rec.Outcome = outcomeFrom(rec)
	state, missing := rec.Completeness()
	if state != Complete {
		t.Fatalf("state = %s (%v), want COMPLETE", state, missing)
	}
	if rec.Outcome != OutcomeUnreviewed {
		t.Fatalf("outcome = %s, want UNREVIEWED", rec.Outcome)
	}
	for _, o := range []Outcome{OutcomeAccepted, OutcomeRefused, OutcomeFailed, OutcomeUnreviewed, OutcomeUnknown} {
		if string(o) == "VOID" {
			t.Fatal("VOID is not an outcome: it belongs to the completeness axis")
		}
	}
}

func TestACompleteRecordOfAFailedRunIsAlsoComplete(t *testing.T) {
	rec := completeReceipt()
	rec.ReviewVerdict = UnknownValue("the run ended before a verdict")
	rec.Terminal = KnownValue("workflow.failed")
	rec.Outcome = outcomeFrom(rec)
	if state, missing := rec.Completeness(); state != Complete {
		t.Fatalf("state = %s (%v)", state, missing)
	}
	if rec.Outcome != OutcomeFailed {
		t.Fatalf("outcome = %s, want FAILED", rec.Outcome)
	}
}

func TestARederivableFieldIsRecomputedAndAMismatchIsVisible(t *testing.T) {
	rec := completeReceipt()
	got := rec.Verify(func(field string) (string, bool) {
		switch field {
		case "base_commit":
			return "a-different-commit", true
		case "plan_digest":
			return "", false
		}
		return recordedValue(rec, field), true
	})
	var sawMismatch, sawUnmeasurable bool
	for _, m := range got {
		if m.Field == "base_commit" && m.Recomputed == "a-different-commit" {
			sawMismatch = true
		}
		if m.Field == "plan_digest" && strings.Contains(m.Reason, "could not be recomputed") {
			sawUnmeasurable = true
		}
	}
	if !sawMismatch {
		t.Error("a disagreeing recomputation must be visible")
	}
	if !sawUnmeasurable {
		t.Error("a field the verifier could not recompute must be reported, not passed: silence is not agreement")
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

func TestAnUnmodelledKindIsReportedRatherThanSilentlyDropped(t *testing.T) {
	rec := FromEvents(strings.NewReader(`{"kind":"something.nobody.modelled","payload":{}}`))
	var found bool
	for _, d := range rec.Diagnostics {
		if strings.Contains(d, "something.nobody.modelled") {
			found = true
		}
	}
	if !found {
		t.Fatalf("an unmodelled kind must appear in diagnostics, got %v", rec.Diagnostics)
	}
}

func TestTheReviewerTrailKeepsAFailedAttemptBesideTheDeliveringOne(t *testing.T) {
	log := `{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"codex"}}
{"kind":"agent.finished","payload":{"provider":"codex","error":"no response"}}
{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"gemini"}}
{"kind":"review.completed","payload":{"decision":"accept","provenance":{"provider":"gemini","candidate_digest":"dd11"}}}`
	rec := FromEvents(strings.NewReader(log))
	if len(rec.Attempts) != 2 {
		t.Fatalf("attempts = %d, want both the failed and the delivering provider", len(rec.Attempts))
	}
	if rec.Attempts[0].Provider.Text != "codex" || rec.Attempts[0].Delivered {
		t.Errorf("first attempt = %+v, want codex undelivered", rec.Attempts[0])
	}
	if rec.Attempts[1].Provider.Text != "gemini" || !rec.Attempts[1].Delivered {
		t.Errorf("second attempt = %+v, want gemini delivered", rec.Attempts[1])
	}
}

func completeReceipt() Receipt {
	return Receipt{
		Schema:               SchemaVersion,
		GovernorCommit:       KnownValue("f01592b0f0828605ed254047fc064f41dacc78f2"),
		GovernorBinarySHA256: KnownValue("7c0bd86ba2030666f577c9d0ef4dae550eff77a9f6eec01828edf25509c5baea"),
		BaseCommit:           KnownValue("f01592b0f0828605ed254047fc064f41dacc78f2"),
		PlanDigest:           KnownValue("990090fd50446fedcdf60f11e3256ed91a22fac1670cc4d9333e86f9e638d554"),
		GraphDigest:          KnownValue("42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64"),
		ServingProducer:      KnownValue("pid 1 | exe /x | sha256 aa"),
		Terminal:             KnownValue("workflow.completed"),
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

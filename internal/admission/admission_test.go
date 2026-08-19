package admission

import (
	"strings"
	"testing"
)

func request() Request {
	return Request{
		LineagePath:  "/w/.sensei/candidates/abc.lineage.json",
		BundleDir:    "/w/.sensei/bundle",
		RequestPath:  "/w/.sensei/admission/request.yaml",
		DecisionPath: "/w/.sensei/admission/decision.yaml",
		GraphNT:      "/w/.sensei/graph.nt",
		Repo:         "/w",
		Target:       "/w-candidate",
	}
}

// The chain is Sensei's, in Sensei's order, and each step reads what the
// previous one wrote. Getting that wiring wrong is the failure that produces a
// decision about one artifact being applied to another.
func TestTheChainWiresEachStepToWhatThePreviousOneWrote(t *testing.T) {
	chain, err := request().Chain()
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 4 {
		t.Fatalf("chain has %d steps, want 4", len(chain))
	}
	want := []Step{Compose, Decide, Apply, Verify}
	for i, step := range want {
		if chain[i].Step != step {
			t.Fatalf("step %d is %s, want %s", i, chain[i].Step, step)
		}
	}
	compose, decide, apply, verify := chain[0].Command(), chain[1].Command(), chain[2].Command(), chain[3].Command()

	if !strings.Contains(compose, "--output /w/.sensei/admission/request.yaml") ||
		!strings.Contains(decide, "--request /w/.sensei/admission/request.yaml") {
		t.Error("admit-change does not read the request synthesis-admit wrote")
	}
	if !strings.Contains(decide, "--output /w/.sensei/admission/decision.yaml") ||
		!strings.Contains(apply, "--decision /w/.sensei/admission/decision.yaml") ||
		!strings.Contains(verify, "--decision /w/.sensei/admission/decision.yaml") {
		t.Error("apply and verify do not use the decision admit-change wrote")
	}
	// The applied artifact must be the admitted one: apply and compose must
	// name the same lineage, or a different candidate is being materialised
	// under an admitted decision's receipt.
	if !strings.Contains(apply, "--lineage /w/.sensei/candidates/abc.lineage.json") {
		t.Error("apply materialises a candidate other than the one admitted")
	}
	// Verification is about the tree the candidate was applied into.
	if !strings.Contains(verify, "--repo /w-candidate") {
		t.Error("verification checks a tree other than the one the candidate was applied to")
	}
}

// Admission decides against a strict policy. This package may pass one through
// and must never soften the default.
func TestPolicyIsPassedThroughAndNeverSoftened(t *testing.T) {
	chain, _ := request().Chain()
	if strings.Contains(chain[1].Command(), "--policy") {
		t.Error("an unset policy was given a value here rather than left to Sensei's strict default")
	}
	r := request()
	r.PolicyID = "admission.strict.v1"
	chain, _ = r.Chain()
	if !strings.Contains(chain[1].Command(), "--policy admission.strict.v1") {
		t.Error("an explicit policy was dropped")
	}
}

// The chain refuses to run on inputs it would have to invent.
func TestMissingInputsAreRefusedRatherThanGuessed(t *testing.T) {
	r := request()
	r.LineagePath = ""
	r.GraphNT = "  "
	if _, err := r.Chain(); err == nil {
		t.Fatal("a chain with no lineage and no graph was composed anyway")
	} else if !strings.Contains(err.Error(), "graph-nt") || !strings.Contains(err.Error(), "lineage") {
		t.Errorf("the refusal does not name what is missing: %v", err)
	}

	// Applying into the checkout under discussion is refused: apply requires a
	// clean target already at the admitted base, which a working checkout is not.
	same := request()
	same.Target = same.Repo
	if _, err := same.Chain(); err == nil {
		t.Fatal("the candidate would have been applied into the repository checkout itself")
	}
}

// Sensei's exit vocabulary is richer than success/failure, and a refusal must
// not read as an outage.
func TestRefusalAndBreakageAreDistinguished(t *testing.T) {
	unsupported := Interpret(Compose, 3, "")
	if !unsupported.Refused || !strings.Contains(unsupported.Detail, "does not support") {
		t.Errorf("an unsupported operation was not reported as a refusal: %+v", unsupported)
	}
	empty := Interpret(Compose, 4, "")
	if !empty.Refused || !strings.Contains(empty.Detail, "nothing to admit") {
		t.Errorf("an empty candidate was not reported as a refusal: %+v", empty)
	}
	broken := Interpret(Compose, 1, "")
	if broken.Refused {
		t.Errorf("inputs failing to resolve was reported as an architectural refusal: %+v", broken)
	}
	// A code from a future Sensei must not be mapped onto an existing meaning.
	future := Interpret(Compose, 42, "")
	if future.Refused || !strings.Contains(future.Detail, "does not recognise") {
		t.Errorf("an unrecognised outcome was given a meaning: %+v", future)
	}
}

// Only the deciding step can say a change was admitted, and only the whole
// chain can say the result stayed in the envelope.
func TestOnlyTheDecidingStepEstablishesAdmission(t *testing.T) {
	composedOnly := []Outcome{{Step: Compose, Code: 0}}
	if Admitted(composedOnly) {
		t.Error("composing a request was read as admission")
	}
	refused := []Outcome{{Step: Compose, Code: 0}, {Step: Decide, Code: 1}}
	if Admitted(refused) {
		t.Error("a failed decision was read as admission")
	}
	decided := []Outcome{{Step: Compose, Code: 0}, {Step: Decide, Code: 0}}
	if !Admitted(decided) {
		t.Error("a completed decision was not read as admission")
	}
	if Verified(decided) {
		t.Error("an admitted-but-unapplied change was reported as verified")
	}
	full := append(decided, Outcome{Step: Apply, Code: 0}, Outcome{Step: Verify, Code: 0})
	if !Verified(full) {
		t.Error("a fully applied and verified chain was not reported as such")
	}

	// The words the summary uses are the claims the chain actually supports.
	s := Summary(full)
	if !strings.Contains(s, "scope compliance is not correctness certification") {
		t.Errorf("the summary overstates what verification established:\n%s", s)
	}
	if !strings.Contains(Summary(decided), "permission to attempt, not proof of correctness") {
		t.Errorf("the summary overstates what admission established:\n%s", Summary(decided))
	}
	if !strings.Contains(Summary(nil), "not admitted") {
		t.Error("an unattempted chain does not report the candidate as unadmitted")
	}
}

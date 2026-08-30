package runreceipt

import (
	"testing"

	"github.com/globulario/sensei-code/internal/roles"
)

// TestTheReceiptVocabularyMatchesTheReviewerContract keeps two representations
// of one closed set equal.
//
// The receipt defines its own ReviewDecision rather than importing
// roles.Decision, because a serialization schema that borrows a live internal
// type changes meaning whenever that type does. Independent representation is
// only safe if the drift is caught, so it is caught here: every reviewer
// decision must be a receipt decision, and every receipt decision must be a
// reviewer decision. Every representation preserves its predicate.
func TestTheReceiptVocabularyMatchesTheReviewerContract(t *testing.T) {
	contract := []roles.Decision{roles.Accept, roles.Revise, roles.Escalate}
	schema := []ReviewDecision{DecisionAccept, DecisionRevise, DecisionEscalate}

	if len(contract) != len(schema) {
		t.Fatalf("the vocabularies differ in size: %v vs %v", contract, schema)
	}
	for _, d := range contract {
		if !ReviewDecision(d).Valid() {
			t.Errorf("reviewer decision %q is not a receipt decision", d)
		}
	}
	for _, d := range schema {
		if !roles.Decision(d).Valid() {
			t.Errorf("receipt decision %q is not a reviewer decision", d)
		}
	}
	// And membership is read by enumeration on both sides.
	for _, invented := range []string{"banana", "ACCEPT", "", "approve"} {
		if ReviewDecision(invented).Valid() || roles.Decision(invented).Valid() {
			t.Errorf("%q must not be a valid decision in either vocabulary", invented)
		}
	}
}

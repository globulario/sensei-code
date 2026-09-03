package principal

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/roles"
)

func TestAPrincipalReviewingItsOwnArchitectureIsAdvisory(t *testing.T) {
	q := Qualify("agent-a", "agent-a")

	if q.Kind() != AdvisoryReview {
		t.Fatalf("kind is %s", q.Kind())
	}
	if q.Independent() {
		t.Fatal("a principal reviewing work produced under its own architecture reported independence")
	}
	if q.Reason() == "" {
		t.Fatal("advisory with no reason a person could act on")
	}
}

func TestADifferentPrincipalIsIndependentOfTheArchitecture(t *testing.T) {
	q := Qualify("agent-a", "agent-b")

	if q.Kind() != IndependentReview || !q.Independent() {
		t.Fatalf("kind is %s, independent %v", q.Kind(), q.Independent())
	}
}

// The missing fact must not produce the stronger answer. An unrecorded author
// is not evidence of a different author.
func TestAnUnrecordedArchitectQualifiesAsAdvisory(t *testing.T) {
	for _, q := range []Qualification{
		Qualify("", "agent-b"),
		Qualify("agent-a", ""),
		Qualify("", ""),
	} {
		if q.Independent() {
			t.Fatalf("independence was claimed with architect=%q reviewer=%q", q.Architect(), q.Reviewer())
		}
	}
}

func TestTheZeroQualificationClaimsNothing(t *testing.T) {
	var q Qualification
	if q.Independent() {
		t.Fatal("a qualification nobody filled in claimed independence")
	}
	if q.Kind() != AdvisoryReview {
		t.Fatalf("the zero qualification renders as %s", q.Kind())
	}
}

// The amendment in one test: an advisory ACCEPT drives the loop and satisfies
// no adversarial-review obligation.
func TestAnAdvisoryReviewLeavesACrossProviderObligationUnmet(t *testing.T) {
	policy := roles.PolicyFor("system", "architect")
	if !policy.CrossProviderReview {
		t.Fatal("this test needs a policy that requires independent review")
	}

	advisory := Qualify("agent-a", "agent-a")
	err := advisory.ObligationUnmet(policy)
	if !errors.Is(err, ErrAdversarialObligationUnmet) {
		t.Fatalf("an advisory review satisfied a cross-provider obligation: %v", err)
	}

	independent := Qualify("agent-a", "agent-b")
	if err := independent.ObligationUnmet(policy); err != nil {
		t.Fatalf("an architecturally independent review was refused: %v", err)
	}
}

func TestAnAdvisoryReviewLeavesNothingUnmetWhenNothingWasRequired(t *testing.T) {
	var none roles.Policy
	if none.CrossProviderReview {
		t.Fatal("the zero policy requires independent review")
	}
	if err := Qualify("agent-a", "agent-a").ObligationUnmet(none); err != nil {
		t.Fatalf("an obligation was reported where the task imposed none: %v", err)
	}
}

// Sensei's unclassified reading is high risk, so a same-principal review of an
// unclassified task must not be counted.
func TestUnclassifiedRiskStillRefusesAnAdvisoryReview(t *testing.T) {
	if err := Qualify("agent-a", "agent-a").ObligationUnmet(roles.PolicyFor("", "")); !errors.Is(err, ErrAdversarialObligationUnmet) {
		t.Fatalf("an advisory review was counted on an unclassified task: %v", err)
	}
}

// Two fields holding one fact eventually disagree. Here there is one fact and
// two renderings, and the rendering is computed from the fact.
func TestTheRecordCannotSayIndependentReviewOfAnAdvisoryOne(t *testing.T) {
	for _, q := range []Qualification{
		Qualify("agent-a", "agent-a"),
		Qualify("", "agent-b"),
		{},
	} {
		raw, err := json.Marshal(q)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var got struct {
			Kind              string `json:"review_kind"`
			IndependentReview bool   `json:"independent_review"`
		}
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.IndependentReview {
			t.Fatalf("advisory review serialized as independent_review=true: %s", raw)
		}
		if got.Kind != string(AdvisoryReview) {
			t.Fatalf("advisory review serialized as %q", got.Kind)
		}
	}

	raw, err := json.Marshal(Qualify("agent-a", "agent-b"))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		IndependentReview bool `json:"independent_review"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.IndependentReview {
		t.Fatalf("an independent review serialized as independent_review=false: %s", raw)
	}
}

// Qualify is the only way to obtain one. A struct another package could
// assemble would eventually be assembled with the convenient answer.
func TestNoOtherPackageCanBuildAQualificationThatClaimsIndependence(t *testing.T) {
	typ := reflect.TypeOf(Qualification{})
	for i := range typ.NumField() {
		if typ.Field(i).IsExported() {
			t.Fatalf("Qualification.%s is exported, so a caller can assemble the answer it wants",
				typ.Field(i).Name)
		}
	}
}

func TestTheReviewKindVocabularyIsClosed(t *testing.T) {
	for _, k := range AllReviewKinds {
		if !k.Valid() {
			t.Fatalf("%s is in the closed set and reports invalid", k)
		}
	}
	for _, k := range []ReviewKind{"", "partial", "peer"} {
		if k.Valid() {
			t.Fatalf("%q is not a review kind and it validated", k)
		}
	}
}

// The statement is what a person reads. An advisory one must not offer the
// vocabulary of admission or completion.
func TestTheAdvisoryStatementDoesNotClaimAdmission(t *testing.T) {
	statement := Qualify("agent-a", "agent-a").Statement()
	for _, forbidden := range []string{"admitted", "verified", "complete", "approved"} {
		if strings.Contains(statement, forbidden) {
			t.Fatalf("the advisory statement contains %q: %s", forbidden, statement)
		}
	}
	if !strings.Contains(statement, "advisory") {
		t.Fatalf("the advisory statement does not say so: %s", statement)
	}
}

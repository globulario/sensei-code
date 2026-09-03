package principal

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/roles"
)

// isolated is a provenance of the only shape that proves anything today: a
// review turn Sensei Code launched itself and observed the session mode of.
func isolated() roles.Provenance {
	return roles.Provenance{
		Role: roles.Reviewer, Provider: "claude",
		SessionMode: roles.Fresh, At: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC),
	}
}

func proof(t *testing.T, reviewer ID) IndependenceProof {
	t.Helper()
	p, err := Prove(reviewer, LaunchedSession, isolated())
	if err != nil {
		t.Fatalf("Prove: %v", err)
	}
	return p
}

func TestAPrincipalReviewingItsOwnArchitectureIsAdvisory(t *testing.T) {
	q := Qualify("agent-a", "agent-a")

	if q.Kind() != AdvisoryReview {
		t.Fatalf("kind is %s", q.Kind())
	}
	if q.Independent() {
		t.Fatal("a principal reviewing work produced under its own architecture reported independence")
	}
	if q.Attribution() != SameParty {
		t.Fatalf("attribution is %s", q.Attribution())
	}
	if q.Reason() == "" {
		t.Fatal("advisory with no reason a person could act on")
	}
}

// The regression this file exists for. Both identities are stated by the party
// being judged, so two different names are one client's labelling choice. If a
// difference in stated IDs could produce IndependentReview, a remote client
// would manufacture adversarial standing by registering twice — the forbidden
// fix one layer down, with the client self-asserting the fact that is supposed
// to make its review worth something.
func TestTwoStatedPrincipalIdentitiesCannotManufactureIndependentReview(t *testing.T) {
	for _, ids := range [][2]ID{
		{"agent-a", "agent-b"},
		{"architect", "reviewer"},
		{"chatgpt-1", "chatgpt-2"},
	} {
		q := Qualify(ids[0], ids[1])
		if q.Independent() {
			t.Fatalf("stating architect=%q and reviewer=%q manufactured an independent review", ids[0], ids[1])
		}
		if q.Kind() != AdvisoryReview {
			t.Fatalf("architect=%q reviewer=%q qualified as %s", ids[0], ids[1], q.Kind())
		}
		if err := q.ObligationUnmet(roles.PolicyFor("system", "architect")); !errors.Is(err, ErrAdversarialObligationUnmet) {
			t.Fatalf("two stated names satisfied a cross-provider obligation: %v", err)
		}
	}
}

// Different names are worth recording. They are just not worth counting.
func TestDistinctPrincipalsAreAttributionRatherThanIndependence(t *testing.T) {
	q := Qualify("agent-a", "agent-b")

	if q.Attribution() != DistinctParties {
		t.Fatalf("attribution is %s, want the distinct identities to be recorded", q.Attribution())
	}
	if q.Independent() {
		t.Fatal("attribution was read as independence")
	}
}

func TestOnlyAProvenIsolatedReviewerQualifiesAsIndependent(t *testing.T) {
	q := QualifyProven("agent-a", "agent-b", proof(t, "agent-b"))

	if !q.Independent() || q.Kind() != IndependentReview {
		t.Fatalf("a proven isolated reviewer qualified as %s", q.Kind())
	}
	if q.Mechanism() != LaunchedSession {
		t.Fatalf("the record does not say what established independence: %q", q.Mechanism())
	}
	if err := q.ObligationUnmet(roles.PolicyFor("system", "architect")); err != nil {
		t.Fatalf("a proven independent review was refused: %v", err)
	}
}

func TestAProofAboutAnotherReviewerProvesNothingAboutThisOne(t *testing.T) {
	q := QualifyProven("agent-a", "agent-b", proof(t, "agent-c"))

	if q.Independent() {
		t.Fatal("a proof naming agent-c made agent-b's review independent")
	}
}

// A party cannot be isolated from itself, so a proof offered for one is
// evidence that the proof is wrong.
func TestAProofCannotRescueASamePrincipalReview(t *testing.T) {
	q := QualifyProven("agent-a", "agent-a", proof(t, "agent-a"))

	if q.Independent() {
		t.Fatal("a proof turned a self-review into an independent one")
	}
	if q.Attribution() != SameParty {
		t.Fatalf("attribution is %s", q.Attribution())
	}
}

func TestTheZeroProofProvesNothing(t *testing.T) {
	var empty IndependenceProof
	if empty.Proves("agent-b") {
		t.Fatal("a zero proof proved something")
	}
	if QualifyProven("agent-a", "agent-b", empty).Independent() {
		t.Fatal("a zero proof produced an independent review")
	}
}

func TestProveRefusesEverythingItDidNotObserve(t *testing.T) {
	inherited := isolated()
	inherited.SessionMode = roles.Continue
	notAReview := isolated()
	notAReview.Role = roles.Architect
	noProvider := isolated()
	noProvider.Provider = ""
	unstated := isolated()
	unstated.SessionMode = ""

	for name, attempt := range map[string]func() (IndependenceProof, error){
		"a session that inherited the work": func() (IndependenceProof, error) {
			return Prove("agent-b", LaunchedSession, inherited)
		},
		"an artifact that is not a review": func() (IndependenceProof, error) {
			return Prove("agent-b", LaunchedSession, notAReview)
		},
		"a session with no provider": func() (IndependenceProof, error) {
			return Prove("agent-b", LaunchedSession, noProvider)
		},
		"a session whose mode nobody stated": func() (IndependenceProof, error) {
			return Prove("agent-b", LaunchedSession, unstated)
		},
		"a mechanism this repository does not have": func() (IndependenceProof, error) {
			return Prove("agent-b", Mechanism("remote_client_says_so"), isolated())
		},
		"an unidentified reviewer": func() (IndependenceProof, error) {
			return Prove("", LaunchedSession, isolated())
		},
	} {
		if _, err := attempt(); err == nil {
			t.Fatalf("independence was proven from %s", name)
		}
	}
}

// The honest state of the world, asserted so that adding a remote mechanism is
// a deliberate change to this test rather than a quiet one to the vocabulary.
func TestNoMechanismCanBeSatisfiedByARemotePrincipal(t *testing.T) {
	if len(AllMechanisms) != 1 || AllMechanisms[0] != LaunchedSession {
		t.Fatalf("the mechanism vocabulary changed to %v; a remote transport may only be added here "+
			"once it can produce isolation rather than report it", AllMechanisms)
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
		if q.Attribution() != Unattributed {
			t.Fatalf("attribution is %s", q.Attribution())
		}
	}
	if QualifyProven("", "agent-b", proof(t, "agent-b")).Independent() {
		t.Fatal("a proof made a review of an unattributed architecture independent")
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
	if q.Attribution() != Unattributed {
		t.Fatalf("the zero qualification attributes itself to %s", q.Attribution())
	}
}

// The amendment in one test: an advisory ACCEPT drives the loop and satisfies
// no adversarial-review obligation.
func TestAnAdvisoryReviewLeavesACrossProviderObligationUnmet(t *testing.T) {
	policy := roles.PolicyFor("system", "architect")
	if !policy.CrossProviderReview {
		t.Fatal("this test needs a policy that requires independent review")
	}

	err := Qualify("agent-a", "agent-a").ObligationUnmet(policy)
	if !errors.Is(err, ErrAdversarialObligationUnmet) {
		t.Fatalf("an advisory review satisfied a cross-provider obligation: %v", err)
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

// Sensei's unclassified reading is high risk, so an unproven review of an
// unclassified task must not be counted.
func TestUnclassifiedRiskStillRefusesAnUnprovenReview(t *testing.T) {
	for _, q := range []Qualification{Qualify("agent-a", "agent-a"), Qualify("agent-a", "agent-b")} {
		if err := q.ObligationUnmet(roles.PolicyFor("", "")); !errors.Is(err, ErrAdversarialObligationUnmet) {
			t.Fatalf("an unproven review was counted on an unclassified task: %v", err)
		}
	}
}

// Two fields holding one fact eventually disagree. Here there is one fact and
// two renderings, and the rendering is computed from the fact.
func TestTheRecordCannotSayIndependentReviewOfAnUnprovenOne(t *testing.T) {
	for _, q := range []Qualification{
		Qualify("agent-a", "agent-a"),
		Qualify("agent-a", "agent-b"),
		Qualify("", "agent-b"),
		{},
	} {
		var got struct {
			Kind              string `json:"review_kind"`
			IndependentReview bool   `json:"independent_review"`
			Attribution       string `json:"attribution"`
		}
		raw := marshal(t, q)
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if got.IndependentReview {
			t.Fatalf("an unproven review serialized as independent_review=true: %s", raw)
		}
		if got.Kind != string(AdvisoryReview) {
			t.Fatalf("an unproven review serialized as %q", got.Kind)
		}
		if got.Attribution == "" {
			t.Fatalf("the record does not say who reviewed: %s", raw)
		}
	}

	var got struct {
		IndependentReview bool   `json:"independent_review"`
		Mechanism         string `json:"independence_mechanism"`
	}
	raw := marshal(t, QualifyProven("agent-a", "agent-b", proof(t, "agent-b")))
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.IndependentReview {
		t.Fatalf("a proven independent review serialized as independent_review=false: %s", raw)
	}
	if got.Mechanism != string(LaunchedSession) {
		t.Fatalf("the record does not say what established independence: %s", raw)
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

// Qualify and QualifyProven are the only ways to obtain one. A struct another
// package could assemble would eventually be assembled with the convenient
// answer, and so would a proof.
func TestNoOtherPackageCanBuildAQualificationOrAProof(t *testing.T) {
	for _, typ := range []reflect.Type{reflect.TypeOf(Qualification{}), reflect.TypeOf(IndependenceProof{})} {
		for i := range typ.NumField() {
			if typ.Field(i).IsExported() {
				t.Fatalf("%s.%s is exported, so a caller can assemble the answer it wants",
					typ.Name(), typ.Field(i).Name)
			}
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

func TestTheAttributionVocabularyIsClosed(t *testing.T) {
	for _, a := range AllAttributions {
		if !a.Valid() {
			t.Fatalf("%s is in the closed set and reports invalid", a)
		}
	}
	for _, a := range []Attribution{"", "probably_distinct", "trusted"} {
		if a.Valid() {
			t.Fatalf("%q is not an attribution and it validated", a)
		}
	}
}

// The statement is what a person reads. An unproven one must not offer the
// vocabulary of admission or completion, and must not call itself independent.
func TestTheAdvisoryStatementDoesNotClaimAdmissionOrIndependence(t *testing.T) {
	for _, q := range []Qualification{Qualify("agent-a", "agent-a"), Qualify("agent-a", "agent-b")} {
		statement := q.Statement()
		for _, forbidden := range []string{"admitted", "verified", "complete", "approved"} {
			if strings.Contains(statement, forbidden) {
				t.Fatalf("the advisory statement contains %q: %s", forbidden, statement)
			}
		}
		if !strings.Contains(statement, "advisory") {
			t.Fatalf("the advisory statement does not say so: %s", statement)
		}
		if strings.Contains(statement, "review kind: independent") {
			t.Fatalf("an unproven review described itself as independent: %s", statement)
		}
	}
}

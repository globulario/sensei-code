package workflow

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

// scopedPreflight builds a file-scoped preflight result on a healthy graph,
// with the fields under test overridden by the caller.
func scopedPreflight(t *testing.T, body string) sensei.PreflightDecision {
	t.Helper()
	d, err := sensei.DecodePreflight(result(t, "preflight", body))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

const healthyAuthority = `"authority": {
	"authoritative": true,
	"verdict": "authoritative",
	"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
	"seed_state": "SEED_STATE_CURRENT"
}`

// TestCoverageAbsentRequiresHumanEvenWhenTheArchitectIsConfident covers
// "architect says proceed, graph coverage is absent -> human authority
// required". The architect's confidence is not an input at all, which is the
// structural form of that guarantee.
func TestCoverageAbsentRequiresHumanEvenWhenTheArchitectIsConfident(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"risk_class": "UNKNOWN_IMPACT",
		`+healthyAuthority+`
	}`)
	got := routeAuthority(scoped, []Claim{{Statement: "this is routine", About: "internal/tui", Source: "graph"}})
	if got.Route != RouteHuman {
		t.Fatalf("absent coverage did not require human authority: %+v", got)
	}
	if !strings.Contains(got.Condition, "coverage") {
		t.Fatalf("condition does not name the coverage gap: %q", got.Condition)
	}
}

// TestBlindSpotRequiresHuman covers the contradicted/uncovered governing region
// case: Sensei says there is something here it cannot see.
func TestBlindSpotRequiresHuman(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"blind_spots": ["anchor with severity=critical"],
		"required_actions": ["Change risk: blast=local, approval=none"],
		`+healthyAuthority+`
	}`)
	got := routeAuthority(scoped, nil)
	if got.Route != RouteHuman {
		t.Fatalf("a blind spot did not require human authority: %+v", got)
	}
	if !strings.Contains(got.Condition, "anchor with severity=critical") {
		t.Fatalf("condition does not name the blind spot: %q", got.Condition)
	}
}

// TestUnverifiedPremiseCannotAcquireArchitecturalAuthority covers "a plan with
// an unverified governing premise cannot acquire Level-2 authority".
func TestUnverifiedPremiseCannotAcquireArchitecturalAuthority(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"required_actions": ["Change risk: blast=local, approval=none"],
		`+healthyAuthority+`
	}`)
	claims := []Claim{
		{Statement: "the store is append-only", About: "internal/session", Source: "graph"},
		{Statement: "no other caller depends on this signature", About: "internal/agent", Source: "inference"},
	}
	got := routeAuthority(scoped, claims)
	if got.Route != RouteHuman {
		t.Fatalf("an inferred premise still acquired architectural authority: %+v", got)
	}
	if !strings.Contains(got.Condition, "no other caller depends on this signature") {
		t.Fatalf("condition does not quote the unverified premise: %q", got.Condition)
	}
	if !strings.Contains(got.Condition, "internal/agent") {
		t.Fatalf("condition does not say what the premise was about: %q", got.Condition)
	}
}

// TestCertifiableQuestionIsResolvedWithoutAHumanPrompt covers "architect says
// escalate, Sensei can certify the question -> no human prompt". The router
// cannot even see that the architect wanted to escalate.
func TestCertifiableQuestionIsResolvedWithoutAHumanPrompt(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"risk_class": "ARCHITECTURE_SENSITIVE",
		"required_actions": ["Change risk: blast=local, approval=none"],
		`+healthyAuthority+`
	}`)
	got := routeAuthority(scoped, []Claim{{Statement: "engine.go owns the loop", About: "internal/workflow", Source: "graph"}})
	if got.Route != RouteArchitectural {
		t.Fatalf("a fully certifiable question was not granted architectural authority: %+v", got)
	}
	if got.RequiresHuman() {
		t.Fatal("a certifiable question interrupted a human")
	}
}

// TestModelConfidenceHasNoEffectOnRouting covers "model confidence/uncertainty
// text has no effect on routing".
//
// The strongest available form of this test is that there is no parameter to
// vary: the same evidence yields the same route regardless of what the model
// said, because what the model said never reaches the router. What can still be
// varied is the claim prose, so that is what this varies.
func TestModelConfidenceHasNoEffectOnRouting(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"required_actions": ["Change risk: blast=local, approval=none"],
		`+healthyAuthority+`
	}`)
	base := routeAuthority(scoped, []Claim{{Statement: "certain", About: "x", Source: "graph"}})
	for _, prose := range []string{
		"I am not at all sure about this and would prefer a human decide",
		"ESCALATE: this feels dangerous",
		"absolutely certain, no risk whatsoever",
	} {
		got := routeAuthority(scoped, []Claim{{Statement: prose, About: "x", Source: "graph"}})
		if got.Route != base.Route {
			t.Fatalf("routing changed with model prose %q: %v != %v", prose, got.Route, base.Route)
		}
	}
}

// TestUncertifiableGraphIsNotAHumanDesignQuestion keeps the third route
// distinct. A stale graph is a repair, not a decision to hand a person: asking
// them to adjudicate it would be asking them to rule on something no one has
// evidence about.
func TestUncertifiableGraphIsNotAHumanDesignQuestion(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"authority": {
			"authoritative": true,
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_STALE",
			"seed_state": "SEED_STATE_CURRENT"
		}
	}`)
	got := routeAuthority(scoped, nil)
	if got.Route != RouteCannotEstablish {
		t.Fatalf("a stale graph produced %v rather than cannot-establish: %+v", got.Route, got)
	}
	if !strings.Contains(got.Condition, "stale") {
		t.Fatalf("condition does not name the staleness: %q", got.Condition)
	}
}

// TestEveryHumanRouteNamesItsCondition covers "every Level-3 event names the
// exact certifiability condition that caused it".
func TestEveryHumanRouteNamesItsCondition(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		claims []Claim
	}{
		{"coverage absent", `{"status":"PREFLIGHT_STATUS_EMPTY",` + healthyAuthority + `}`, nil},
		{"blind spot", `{"status":"PREFLIGHT_STATUS_OK","blind_spots":["x"],` + healthyAuthority + `}`, nil},
		{"approval required", `{"status":"PREFLIGHT_STATUS_OK","required_actions":["Change risk: blast=cluster, approval=human_approval_required"],` + healthyAuthority + `}`, nil},
		{"unverified premise", `{"status":"PREFLIGHT_STATUS_OK","required_actions":["Change risk: blast=local, approval=none"],` + healthyAuthority + `}`,
			[]Claim{{Statement: "nothing else reads this", Source: "inference"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := routeAuthority(scopedPreflight(t, tc.body), tc.claims)
			if got.Route != RouteHuman {
				t.Fatalf("expected a human route, got %+v", got)
			}
			if strings.TrimSpace(got.Condition) == "" {
				t.Fatal("a human interruption carried no condition, so the human cannot tell why they were asked")
			}
			if rendered := escalationCondition(got); !strings.Contains(rendered, got.Condition) {
				t.Fatalf("rendered escalation drops the condition: %q", rendered)
			}
		})
	}
}

// TestApprovalGateFailsClosedWhenUnreadable pins the one string-reading corner
// of the governance path. If Sensei changes the shape of its change-risk line,
// this must escalate rather than silently read as approval=none.
func TestApprovalGateFailsClosedWhenUnreadable(t *testing.T) {
	if got := approvalGate([]string{"Change risk: blast=local, approval=none"}); got != "none" {
		t.Fatalf("failed to read the documented format: %q", got)
	}
	if got := approvalGate([]string{"Change risk: blast=cluster, approval=human_approval_required"}); got != "human_approval_required" {
		t.Fatalf("failed to read an approval class: %q", got)
	}
	if got := approvalGate([]string{"Change risk: something else entirely"}); got != "unreadable" {
		t.Fatalf("an unparseable change-risk line did not fail closed: %q", got)
	}

	// A change-risk line this build cannot read must not grant authority.
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"required_actions": ["Change risk: reformatted by a future Sensei"],
		`+healthyAuthority+`
	}`)
	if got := routeAuthority(scoped, nil); got.Route != RouteHuman {
		t.Fatalf("an unreadable approval class granted authority: %+v", got)
	}
}

// TestApprovalClassesRequiringAHumanAllEscalate walks Sensei's real approval
// vocabulary rather than a single representative value.
func TestApprovalClassesRequiringAHumanAllEscalate(t *testing.T) {
	for _, gate := range []string{"human_approval_required", "manual_only", "multi_step_approval_required", "never"} {
		scoped := scopedPreflight(t, `{
			"status": "PREFLIGHT_STATUS_OK",
			"required_actions": ["Change risk: blast=service, approval=`+gate+`"],
			`+healthyAuthority+`
		}`)
		got := routeAuthority(scoped, nil)
		if got.Route != RouteHuman {
			t.Fatalf("approval=%s did not require a human: %+v", gate, got)
		}
		if !strings.Contains(got.Condition, gate) {
			t.Fatalf("condition does not name the approval class %q: %q", gate, got.Condition)
		}
	}
}

// TestArchitectPromptDemandsClaims pins the other half of the router: the
// routing rule for unverified premises is dead code unless the architect is
// actually asked for claims, and a prompt is the kind of thing that gets
// reworded without anyone noticing what depended on it.
func TestArchitectPromptDemandsClaims(t *testing.T) {
	prompt := architecturePrompt("/repo", "github.com/globulario/sensei-code", "ChatGPT", "add a thing", "", "workspace", "preflight")
	for _, required := range []string{`"claims"`, "inference", "graph", "repository"} {
		if !strings.Contains(prompt, required) {
			t.Fatalf("architect prompt does not mention %q, so claims-based routing cannot fire", required)
		}
	}
}

// TestArchitectDecisionCarriesClaims proves the claims survive the model JSON
// decode into the struct the router reads.
func TestArchitectDecisionCarriesClaims(t *testing.T) {
	var d architectureDecision
	body := `{"decision":"proceed","summary":"s","plan":"p",
		"claims":[{"statement":"nothing else reads this","about":"internal/agent","source":"inference"}]}`
	if err := decodeModelJSON(body, &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Claims) != 1 {
		t.Fatalf("claims did not decode: %+v", d)
	}
	if d.Claims[0].Source != "inference" || d.Claims[0].About != "internal/agent" {
		t.Fatalf("claim fields did not decode: %+v", d.Claims[0])
	}
}

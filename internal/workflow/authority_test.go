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
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
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
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
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
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
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
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
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
		{"approval required", `{"status":"PREFLIGHT_STATUS_OK","change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},` + healthyAuthority + `}`, nil},
		{"unverified premise", `{"status":"PREFLIGHT_STATUS_OK","change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` + healthyAuthority + `}`,
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

// TestUnclassifiedChangeRiskFailsClosed pins the direction the approval gate
// must fail in. Sensei's own contract says an unspecified gate means
// unclassified and never "none", so a preflight that reaches no verdict — an
// older server, a region the classifier could not judge, a member this build
// does not know — must escalate rather than read as permission.
func TestUnclassifiedChangeRiskFailsClosed(t *testing.T) {
	cases := []struct {
		name string
		risk string
	}{
		{"field absent entirely", ""},
		{"gate explicitly unspecified", `"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_UNSPECIFIED"},`},
		{"blast classified, gate not", `"change_risk": {"blast_radius":"BLAST_RADIUS_CLUSTER"},`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scoped := scopedPreflight(t, `{
				"status": "PREFLIGHT_STATUS_OK",
				`+tc.risk+healthyAuthority+`
			}`)
			got := routeAuthority(scoped, nil)
			if got.Route != RouteHuman {
				t.Fatalf("an unclassified approval gate granted authority: %+v", got)
			}
			if !strings.Contains(got.Condition, "approval gate") {
				t.Fatalf("condition does not name what was missing: %q", got.Condition)
			}
		})
	}
}

// TestRoutingReadsChangeRiskStructurallyNotAsProse states the property that
// replaced the old string parser: the change-risk sentence Sensei still renders
// into required_actions for older consumers has no effect on routing whatsoever.
// Only the structured verdict decides.
func TestRoutingReadsChangeRiskStructurallyNotAsProse(t *testing.T) {
	// The prose says no approval is needed. The structured verdict says a human
	// must approve. The structured verdict wins.
	contradicted := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"required_actions": ["Change risk: blast=local, approval=none"],
		"change_risk": {"blast_radius":"BLAST_RADIUS_SECURITY","approval_gate":"APPROVAL_GATE_MANUAL_ONLY"},
		`+healthyAuthority+`
	}`)
	if got := routeAuthority(contradicted, nil); got.Route != RouteHuman {
		t.Fatalf("prose overrode the structured verdict: %+v", got)
	}

	// And a change-risk line this build could never have parsed is simply not
	// consulted, because the structured verdict is present and says none.
	reworded := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"required_actions": ["Change risk: reformatted by a future Sensei"],
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	if got := routeAuthority(reworded, nil); got.Route != RouteArchitectural {
		t.Fatalf("a reworded prose line still reached the router: %+v", got)
	}

	// The router must not read the sentence at all.
	if strings.Contains(fileText(t, "internal/workflow/authority.go"), "Change risk:") {
		t.Error("the router still recognises the change-risk sentence; the structured fields are the contract")
	}
}

// TestApprovalClassesRequiringAHumanAllEscalate walks Sensei's real approval
// vocabulary rather than a single representative value, including a member this
// build has never seen.
func TestApprovalClassesRequiringAHumanAllEscalate(t *testing.T) {
	for _, gate := range []string{
		"APPROVAL_GATE_REVIEW_REQUIRED",
		"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED",
		"APPROVAL_GATE_MULTI_STEP_APPROVAL_REQUIRED",
		"APPROVAL_GATE_MANUAL_ONLY",
		"APPROVAL_GATE_INVENTED_BY_A_LATER_SENSEI",
	} {
		scoped := scopedPreflight(t, `{
			"status": "PREFLIGHT_STATUS_OK",
			"change_risk": {"blast_radius":"BLAST_RADIUS_SERVICE","approval_gate":"`+gate+`"},
			`+healthyAuthority+`
		}`)
		got := routeAuthority(scoped, nil)
		if got.Route != RouteHuman {
			t.Fatalf("approval=%s did not require a human: %+v", gate, got)
		}
		human := strings.ToLower(strings.TrimPrefix(gate, "APPROVAL_GATE_"))
		if !strings.Contains(got.Condition, human) {
			t.Fatalf("condition does not name the approval class %q: %q", human, got.Condition)
		}
		if !strings.Contains(got.Condition, "service") {
			t.Fatalf("condition does not name the blast radius: %q", got.Condition)
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

package workflow

import (
	"strconv"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

// scopedPreflight builds a file-scoped preflight result on a healthy graph,
// with the fields under test overridden by the caller.
// scopedPreflight decodes a fixture, defaulting the coverage block the way a
// real server always fills it.
//
// Every fixture in this package used to omit "coverage" entirely, which no live
// preflight does -- probing the running graph, an OK answer carries
// sufficient=true with real counts and an EMPTY answer carries nulls. That gap
// stopped mattering while the router inferred coverage from the STATUS, and
// started mattering the moment it read the coverage evidence instead.
//
// So the default is applied by status, matching observed responses, and any
// fixture stating its own coverage keeps it. A test about thin or absent
// coverage now says so explicitly rather than relying on a silence that real
// responses never contain.
func scopedPreflight(t *testing.T, body string) sensei.PreflightDecision {
	t.Helper()
	if !strings.Contains(body, "\"coverage\"") && strings.Contains(body, "PREFLIGHT_STATUS_OK") {
		body = strings.Replace(body, "{", "{\n\t\t\t\t"+provenCoverage, 1)
	}
	d, err := sensei.DecodePreflight(result(t, "preflight", body))
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// provenCoverage is what a live OK preflight publishes alongside its status.
const provenCoverage = `"coverage": {"sufficient": true, "direct_anchor_count": 1, "indexed_file_count": 1, "file_count": 1},`

const healthyAuthority = `"authority": {
	"authoritative": true,
	"verdict": "authoritative",
	"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
	"seed_state": "SEED_STATE_CURRENT"
}`

// Absent coverage never grants, whatever the architect thinks. Its confidence
// is not an input at all, which is the structural form of that guarantee.
//
// It no longer interrupts a human either. Missing coverage is not a decision
// anybody owns: asking a person to supply it is asking for a technical answer,
// and answering leaves the graph as empty as before, so the next task over the
// same region asks again. The route is bounded epistemic work -- and it is
// still not permission to proceed, which is what the Granted assertion pins.
func TestCoverageAbsentNeverGrantsAndNoLongerInterrupts(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"risk_class": "UNKNOWN_IMPACT",
		`+healthyAuthority+`
	}`)
	got := routeAuthorityForAction(scoped, []Claim{{Statement: "this is routine", About: "internal/tui", Source: "graph"}}, plannedEdit())
	if got.Granted() {
		t.Fatalf("absent coverage was granted architectural authority: %+v", got)
	}
	if !got.ClosesGap() {
		t.Fatalf("absent coverage did not route to bounded work: %+v", got)
	}
	if got.RequiresHuman() {
		t.Fatalf("absent coverage asked a human for a technical answer: %+v", got)
	}
	if !strings.Contains(got.Condition, "coverage") {
		t.Fatalf("condition does not name the coverage gap: %q", got.Condition)
	}
}

// Every route that is not a grant must carry a condition, including the new
// one. A gap-closing instruction nobody can trace to a cause is as useless as
// an interruption nobody can trace to a cause.
func TestCloseGapRoutesNameTheirCondition(t *testing.T) {
	cases := map[string]string{
		"coverage absent": `{"status":"PREFLIGHT_STATUS_EMPTY",` + healthyAuthority + `}`,
		"coverage blind spot": `{"status":"PREFLIGHT_STATUS_OK",` +
			`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
			`"blind_spots":["coverage_insufficient: no direct anchors and no indexed files"],` +
			healthyAuthority + `}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			got := routeAuthorityForAction(scopedPreflight(t, body), nil, plannedEdit())
			if !got.ClosesGap() {
				t.Fatalf("got %+v", got)
			}
			if strings.TrimSpace(got.Condition) == "" {
				t.Fatal("a gap-closing route carried no condition")
			}
		})
	}
}

// A consequence signal never reaches a grant without an assessment behind it.
//
// This test used to be called TestBlindSpotRequiresHuman and described
// `anchor with severity=critical` as "something here Sensei cannot see". That
// was the misreading the blind-spot classifier corrects: the anchor fires
// BECAUSE the graph can see something, and severity is a property of knowledge
// it holds.
//
// What survives is the part that was always right — this must never be a free
// pass. With no action stated there is nothing to assess, and the answer is
// CANNOT_ESTABLISH: nobody knows, which is not the same as nobody needs to.
func TestAConsequenceSignalIsNeverAGrantOnItsOwn(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"blind_spots": ["anchor with severity=critical"],
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	got := routeAuthorityForAction(scoped, nil, unclassifiedAction())
	if got.Granted() {
		t.Fatalf("a consequence signal was granted with no action assessed: %+v", got)
	}
	if got.Route != RouteCannotEstablish {
		t.Fatalf("an unassessable action: %+v", got)
	}
	if !strings.Contains(got.Condition, "anchor with severity=critical") {
		t.Fatalf("condition does not name the signal: %q", got.Condition)
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
	got := routeAuthorityForAction(scoped, claims, plannedEdit())
	if got.Granted() {
		t.Fatalf("an inferred premise still acquired architectural authority: %+v", got)
	}
	// It is a verification task, not a decision anybody owns: what the plan
	// needs is evidence, and the architect is the one who can go and get it.
	if !got.ClosesGap() {
		t.Fatalf("an inferred premise did not route to verification: %+v", got)
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
	got := routeAuthorityForAction(scoped, []Claim{{Statement: "engine.go owns the loop", About: "internal/workflow", Source: "graph"}}, plannedEdit())
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
	base := routeAuthorityForAction(scoped, []Claim{{Statement: "certain", About: "x", Source: "graph"}}, plannedEdit())
	for _, prose := range []string{
		"I am not at all sure about this and would prefer a human decide",
		"ESCALATE: this feels dangerous",
		"absolutely certain, no risk whatsoever",
	} {
		got := routeAuthorityForAction(scoped, []Claim{{Statement: prose, About: "x", Source: "graph"}}, plannedEdit())
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
	got := routeAuthorityForAction(scoped, nil, plannedEdit())
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
		{"unrecognised blind spot", `{"status":"PREFLIGHT_STATUS_OK","blind_spots":["x"],` + healthyAuthority + `}`, nil},
		{"consequence blind spot", `{"status":"PREFLIGHT_STATUS_OK","blind_spots":["file path under high-risk directory"],` +
			healthyAuthority + `}`, nil},
		{"approval required", `{"status":"PREFLIGHT_STATUS_OK","change_risk":{"blast_radius":"BLAST_RADIUS_CLUSTER","approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"},` + healthyAuthority + `}`, nil},
		{"unclassified gate with coverage", `{"status":"PREFLIGHT_STATUS_OK","direct_invariants":[{"id":"i","label":"l","severity":"warning","status":"active"}],` + healthyAuthority + `}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := routeAuthorityForAction(scopedPreflight(t, tc.body), tc.claims, plannedEdit())
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
			got := routeAuthorityForAction(scoped, nil, plannedEdit())
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
	if got := routeAuthorityForAction(contradicted, nil, plannedEdit()); got.Route != RouteHuman {
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
	if got := routeAuthorityForAction(reworded, nil, plannedEdit()); got.Route != RouteArchitectural {
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
		got := routeAuthorityForAction(scoped, nil, plannedEdit())
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
	prompt := architecturePrompt("/repo", "github.com/globulario/sensei-code", "ChatGPT", "add a thing", "", "workspace", "preflight", "", "", "", "")
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

// TestPlanRevisionInsideTheCertifiedEnvelopeDoesNotInterrupt states the rule
// that keeps discovery from becoming a question.
//
// A worker that learns something mid-task should let the architect revise the
// plan, and a revision that stays inside the region Sensei already certified is
// the architect's to make. Only a revision that reaches outside it becomes an
// authority question — and then the interruption names the region, not the
// revision.
func TestPlanRevisionInsideTheCertifiedEnvelopeDoesNotInterrupt(t *testing.T) {
	// The revised plan drops a step and touches the same certified region.
	inside := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"direct_invariants": [{"id":"invariant:covered.region","label":"covered","severity":"critical","status":"active"}],
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	revised := []Claim{{Statement: "step C is unnecessary", About: "internal/workflow", Source: "graph"}}
	if got := routeAuthorityForAction(inside, revised, plannedEdit()); got.Route != RouteArchitectural {
		t.Fatalf("a revision inside the certified envelope interrupted a human: %+v", got)
	}

	// The revision now reaches a region the graph does not cover. Sensei was
	// asked about the new file set and answered; that answer is what escalates.
	outside := scopedPreflight(t, `{"status":"PREFLIGHT_STATUS_EMPTY",`+healthyAuthority+`}`)
	got := routeAuthorityForAction(outside, revised, plannedEdit())
	if got.Granted() {
		t.Fatalf("a revision reaching outside the certified region was granted: %+v", got)
	}
	if !got.ClosesGap() {
		t.Fatalf("a revision reaching outside the certified region did not stop: %+v", got)
	}
	if !strings.Contains(got.Condition, "coverage") {
		t.Errorf("the interruption does not name the region that caused it: %q", got.Condition)
	}
}

// TestUnreadableClaimProvenanceCannotAcquireArchitecturalAuthority pins the
// half of the vocabulary the router used to leave open.
//
// Claim.Source documents a closed vocabulary -- graph, repository, inference --
// but nothing enforces it. decodeModelJSON validates no field, so the source is
// whatever the model typed, and the router only ever recognised "inference".
// Every other value fell past the check to RouteArchitectural: a claim with a
// blank source, a misspelling, or an invented label was granted the authority
// of a checked premise for the sole reason that it was not spelled "inference".
//
// The evidence here is otherwise perfect -- OK status, proven coverage, no
// approval gate, no blind spots -- so the claim's provenance is the only thing
// left that can decide the route, which is exactly the case that failed.
func TestUnreadableClaimProvenanceCannotAcquireArchitecturalAuthority(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	// Same evidence, a claim whose source IS readable: this is what the
	// unreadable cases below are being distinguished from.
	if got := routeAuthorityForAction(scoped, []Claim{{Statement: "the store is append-only", About: "internal/session", Source: "graph"}}, plannedEdit()); got.Route != RouteArchitectural {
		t.Fatalf("a verified premise on certifiable evidence was not granted: %+v", got)
	}

	for _, source := range []string{
		"",            // the field was never filled in
		"   ",         // filled in with nothing
		"Graph ",      // spacing and case are normalised, not rejected
		"REPOSITORY",  //
		"observation", // a plausible word that is not in the vocabulary
		"repositoy",   // a misspelling of one that is
		"graph, mostly",
		"trust me",
	} {
		got := routeAuthorityForAction(scoped, []Claim{{Statement: "no other caller depends on this signature", About: "internal/agent", Source: source}}, plannedEdit())
		normalised := strings.ToLower(strings.TrimSpace(source))
		if normalised == "graph" || normalised == "repository" {
			if got.Route != RouteArchitectural {
				t.Fatalf("source %q normalises to declared provenance and was not granted: %+v", source, got)
			}
			continue
		}
		if got.Granted() {
			t.Fatalf("source %q acquired architectural authority: %+v", source, got)
		}
		if !got.ClosesGap() {
			t.Fatalf("source %q did not route to bounded verification: %+v", source, got)
		}
		// A route a human cannot trace back to its cause is one they learn to
		// dismiss, so the condition carries both the premise and the reason.
		if !strings.Contains(got.Condition, "no other caller depends on this signature") {
			t.Fatalf("source %q: condition does not quote the premise: %q", source, got.Condition)
		}
		if !strings.Contains(got.Condition, "internal/agent") {
			t.Fatalf("source %q: condition does not say what the premise was about: %q", source, got.Condition)
		}
		if strings.TrimSpace(source) == "" {
			if !strings.Contains(got.Condition, "no source was declared") {
				t.Fatalf("a claim with no source did not say so: %q", got.Condition)
			}
			continue
		}
		if !strings.Contains(got.Condition, strconv.Quote(strings.TrimSpace(source))) {
			t.Fatalf("source %q: condition does not name the provenance it could not read: %q", source, got.Condition)
		}
	}
}

// TestOneUnreadableClaimIsEnough proves the check is not a majority vote.
//
// A plan is not granted authority because most of its premises were checked.
// The one that was not is the one the plan can be wrong about, so a single
// unreadable source decides the route no matter how much verified evidence
// travels beside it, and no matter where in the list it sits.
func TestOneUnreadableClaimIsEnough(t *testing.T) {
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	verified := Claim{Statement: "the store is append-only", About: "internal/session", Source: "graph"}
	unreadable := Claim{Statement: "nothing else reads this field", About: "internal/agent", Source: "hunch"}
	for _, claims := range [][]Claim{
		{unreadable, verified},
		{verified, unreadable},
		{verified, verified, unreadable, verified},
	} {
		got := routeAuthorityForAction(scoped, claims, plannedEdit())
		if got.Granted() {
			t.Fatalf("an unreadable premise beside verified ones was granted: %+v", got)
		}
		if !strings.Contains(got.Condition, "nothing else reads this field") {
			t.Fatalf("condition does not quote the unreadable premise: %q", got.Condition)
		}
	}
}

// TestTheDecoderStillDoesNotValidateClaims states why the router is the place
// this is enforced.
//
// decodeModelJSON is the generic model-JSON path used by every structured
// response in the workflow, and it is deliberately left alone: teaching it
// about claim vocabularies would put a routing rule inside a decoder, where
// the next caller inherits it silently. What it must keep doing is carrying an
// unrecognised source through UNCHANGED, so the router sees what the model
// actually said rather than something already normalised away.
func TestTheDecoderStillDoesNotValidateClaims(t *testing.T) {
	var d architectureDecision
	body := `{"decision":"proceed","summary":"s","plan":"p",
		"claims":[{"statement":"nothing else reads this","about":"internal/agent","source":"vibes"},
		          {"statement":"the loop is owned here","about":"internal/workflow"}]}`
	if err := decodeModelJSON(body, &d); err != nil {
		t.Fatalf("a claim with an undeclared source failed to decode: %v", err)
	}
	if len(d.Claims) != 2 {
		t.Fatalf("claims did not decode: %+v", d)
	}
	if d.Claims[0].Source != "vibes" {
		t.Fatalf("the decoder altered an unrecognised source: %+v", d.Claims[0])
	}
	if d.Claims[1].Source != "" {
		t.Fatalf("the decoder invented a source for a claim that omitted one: %+v", d.Claims[1])
	}
}

// plannedEdit is the ordinary action a router test is about: a candidate edit
// in an isolated worktree, declaring nothing outward.
//
// Every routing test must now state its action. There used to be an action-less
// routeAuthority for tests that were not "about" the action, and Action{} meant
// unspecified — which reached a grant, because nothing on the path read the
// stage unless a blind spot happened to route it there. An unspecified action
// is not a neutral one, so there is no longer a way to leave it out.
//
// Files are deliberately left empty by default: that is the only field whose
// absence still changes a route (derived coverage is refused over an empty file
// list), so a test that cares about files names them.
func plannedEdit(files ...string) Action {
	return Action{Stage: StageCandidateEdit, Files: files}
}

// unclassifiedAction is an action whose stage nothing has classified.
//
// Named rather than written as a bare Action{}, because the two used to be
// spelled the same and meant different things: "I am not testing the action"
// and "nobody established what this action is". Only the second is a real
// state, and it must fail closed as ignorance -- CANNOT_ESTABLISH, never a
// silent grant and never a human authority question about something no one has
// evidence for.
func unclassifiedAction() Action { return Action{} }

// sensei-code#97. The closure budget is spent against an engine-issued premise
// receipt carried through the closure loop, not against the condition's
// wording and not against a file bucket. Fixtures below route real claims
// through the router and drive receipts the way resolveArchitectureIn does.

func premiseFixture(t *testing.T) (sensei.PreflightDecision, Action, func(Claim) Routing) {
	t.Helper()
	scoped := scopedPreflight(t, `{
		"status": "PREFLIGHT_STATUS_OK",
		"change_risk": {"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},
		`+healthyAuthority+`
	}`)
	action := plannedEdit("gosumcheck/main.go", "gosumcheck/main_test.go")
	route := func(c Claim) Routing {
		r := routeAuthorityForAction(scoped, []Claim{c}, action)
		if !r.ClosesGap() {
			t.Fatalf("an inferred premise did not route to closure: %+v", r)
		}
		r.Gap.World = "9c7e562"
		return r
	}
	return scoped, action, route
}

// Series C1's three wordings of one unsettled premise: three conditions, one
// receipt, one round. The second and third arrive after the round reported
// the receipt unresolved, and are its residue whether they name the file or
// nothing at all.
func TestAParaphrasedPremiseDoesNotBuyAFreshClosureRound(t *testing.T) {
	_, _, route := premiseFixture(t)
	e := &Engine{}
	first := route(Claim{Statement: "Replacing the default HTTP transport during the test can exercise ReadRemote deterministically", About: "gosumcheck/main_test.go regression strategy", Source: "inference"})
	r1 := e.premiseReceiptFor("task-1", first, first.ClaimGap)
	if !e.spendClosure("task-1", r1.ID) {
		t.Fatal("the first round was refused")
	}
	// The round did not settle it, and said so.
	e.applyPremiseResolutions("task-1", []PremiseResolution{{Gap: r1.ID, Outcome: "unresolved"}})
	for _, c := range []Claim{
		{Statement: "Replacing http.DefaultClient.Transport in a test will intercept ReadRemote's request", About: "prospective gosumcheck/main_test.go regression strategy", Source: "inference"},
		{Statement: "A stub transport can isolate ReadRemote using only main.go's imports plus testing", About: "clientOps.ReadRemote", Source: "inference"}, // a symbol, not a path
	} {
		r := route(c)
		if r.Condition == first.Condition {
			t.Fatal("precondition: the wording must differ")
		}
		got := e.premiseReceiptFor("task-1", r, r.ClaimGap)
		if got.ID != r1.ID {
			t.Fatalf("a re-worded unsettled premise was issued a new receipt %s (about %q)", got.ID, c.About)
		}
		if e.spendClosure("task-1", got.ID) {
			t.Fatalf("a re-worded unsettled premise bought a fresh round (about %q)", c.About)
		}
	}
	if len(r1.Wordings) != 3 {
		t.Fatalf("the receipt does not keep the wordings as evidence: %v", r1.Wordings)
	}
}

// Review falsifier 1: two unrelated inference gaps about the SAME planned
// file, same scope, same world each receive one round.
func TestTwoUnrelatedPremisesAboutOneFileEachGetARound(t *testing.T) {
	_, _, route := premiseFixture(t)
	e := &Engine{}
	a := route(Claim{Statement: "ReadRemote writes the verbose line only after a successful body read", About: "gosumcheck/main.go", Source: "inference"})
	ra := e.premiseReceiptFor("task-1", a, a.ClaimGap)
	if !e.spendClosure("task-1", ra.ID) {
		t.Fatal("gap A was refused its round")
	}
	// The round answered the question it was asked: A is established.
	e.applyPremiseResolutions("task-1", []PremiseResolution{{Gap: ra.ID, Outcome: "established", Evidence: "main.go ReadRemote"}})
	b := route(Claim{Statement: "the -v flag is parsed before any network call", About: "gosumcheck/main.go", Source: "inference"})
	rb := e.premiseReceiptFor("task-1", b, b.ClaimGap)
	if rb.ID == ra.ID {
		t.Fatal("a different premise about the same file was folded into an answered receipt")
	}
	if !e.spendClosure("task-1", rb.ID) {
		t.Fatal("gap B, unrelated to gap A, was denied its own round")
	}
	// A receipt the round refuted is closed the same way.
	e.applyPremiseResolutions("task-1", []PremiseResolution{{Gap: rb.ID, Outcome: "refuted"}})
	c := route(Claim{Statement: "yet another premise", About: "gosumcheck/main.go", Source: "inference"})
	if rc := e.premiseReceiptFor("task-1", c, c.ClaimGap); rc.ID == rb.ID || !e.spendClosure("task-1", rc.ID) {
		t.Fatal("a premise after a refuted receipt did not get its own round")
	}
}

// Review falsifier 2: one premise whose About changes from a file path to a
// symbol does not acquire another round -- neither by residue after an
// unresolved round, nor by explicit reference.
func TestAPremiseRestatedAsASymbolIsStillTheSamePremise(t *testing.T) {
	_, _, route := premiseFixture(t)
	e := &Engine{}
	byPath := route(Claim{Statement: "the verbose line is written by ReadRemote", About: "gosumcheck/main.go", Source: "inference"})
	r := e.premiseReceiptFor("task-1", byPath, byPath.ClaimGap)
	if !e.spendClosure("task-1", r.ID) {
		t.Fatal("first round refused")
	}
	// (a) The round did not answer at all: read as unresolved. The re-worded
	// premise, now a symbol, is the residue.
	bySymbol := route(Claim{Statement: "the verbose line is written by ReadRemote", About: "clientOps.ReadRemote", Source: "inference"})
	e.applyPremiseResolutions("task-1", []PremiseResolution{{Gap: r.ID, Outcome: "something not in the vocabulary"}})
	if got := e.premiseReceiptFor("task-1", bySymbol, bySymbol.ClaimGap); got.ID != r.ID || e.spendClosure("task-1", got.ID) {
		t.Fatal("a premise re-stated as a symbol acquired another round by residue")
	}
	// (b) The architect references the receipt explicitly; wording and
	// subject are irrelevant.
	referenced := route(Claim{Statement: "completely new words", About: "somewhere/else.go", Source: "inference", Gap: r.ID})
	if got := e.premiseReceiptFor("task-1", referenced, referenced.ClaimGap); got.ID != r.ID || e.spendClosure("task-1", got.ID) {
		t.Fatal("a claim referencing the receipt acquired another round")
	}
	// A resolution naming a receipt this task never issued closes nothing.
	e.applyPremiseResolutions("task-1", []PremiseResolution{{Gap: "gap-forged-99", Outcome: "established"}})
	if !r.open() {
		t.Fatal("a forged resolution closed a receipt")
	}
}

// The identity is consulted where the budget is spent, and the closure round
// is asked to answer it.
func TestTheClosureBudgetIsSpentAgainstThePremiseReceipt(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	if strings.Contains(src, "spendClosure(taskID, routing.Condition)") || strings.Contains(src, "spendClosure(taskID, routing.GapKey())") {
		t.Fatal("a call site still spends the budget against the condition or a location bucket")
	}
	if n := strings.Count(src, "spendClosure(taskID, receipt.ID)"); n != 2 {
		t.Fatalf("%d of 2 call sites spend against the receipt", n)
	}
	if n := strings.Count(src, "e.applyPremiseResolutions(taskID, d.PremiseResolutions)"); n != 2 {
		t.Fatalf("%d of 2 branches apply the round's answers", n)
	}
	if n := strings.Count(src, "premiseReceiptNote(receipt.ID)"); n != 2 {
		t.Fatalf("%d of 2 closure prompts ask the round to answer the receipt", n)
	}
	body := funcBody(t, "internal/workflow/engine.go", "routePlan")
	if !strings.Contains(body, "e.governedBase(") || !strings.Contains(body, "routing.Gap.World") {
		t.Fatal("routePlan does not complete the gap metadata with the pinned base")
	}
}

// Review 5046471526: silence is unresolved. A round that omits
// premise_resolutions entirely, or answers only receipts nobody issued, leaves
// the receipt it was asked about unresolved -- so the same paraphrased
// premise, without a claim.gap reference, reuses that receipt and is refused a
// second round. Left blank, the receipt missed the residue rule and the
// paraphrase bought a fresh one.
func TestAnUnansweredReceiptIsUnresolvedNotUnasked(t *testing.T) {
	_, _, route := premiseFixture(t)
	for name, answers := range map[string][]PremiseResolution{
		"resolutions omitted":      nil,
		"only a forged receipt":    {{Gap: "gap-forged-99", Outcome: "established"}},
		"only an empty resolution": {{}},
	} {
		e := &Engine{}
		asked := route(Claim{Statement: "the verbose line is written by ReadRemote", About: "gosumcheck/main.go", Source: "inference"})
		r := e.premiseReceiptFor("task-1", asked, asked.ClaimGap)
		if !e.spendClosure("task-1", r.ID) {
			t.Fatalf("%s: first round refused", name)
		}
		// The next response arrives; the engine applies whatever it answered
		// before routing again, exactly as resolveArchitectureIn does.
		e.applyPremiseResolutions("task-1", answers)
		if r.Outcome != premiseUnresolved {
			t.Fatalf("%s: the asked receipt was left %q, not unresolved", name, r.Outcome)
		}
		paraphrased := route(Claim{Statement: "ReadRemote is what emits the -v diagnostic", About: "clientOps.ReadRemote", Source: "inference"})
		if paraphrased.ClaimGap != "" {
			t.Fatal("precondition: no claim.gap reference")
		}
		got := e.premiseReceiptFor("task-1", paraphrased, paraphrased.ClaimGap)
		if got.ID != r.ID {
			t.Fatalf("%s: the paraphrase was issued a new receipt %s", name, got.ID)
		}
		if e.spendClosure("task-1", got.ID) {
			t.Fatalf("%s: the paraphrase bought a second round", name)
		}
	}
}

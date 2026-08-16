package authority

import (
	"errors"
	"strings"
	"testing"
	"time"
)

type fakeCaller struct {
	result ToolResult
	err    error
	args   map[string]any
	name   string
}

func (f *fakeCaller) CallTool(name string, args map[string]any) (ToolResult, error) {
	f.name, f.args = name, args
	return f.result, f.err
}

func sample() Resolution {
	return Resolution{
		TaskID:      "task-1",
		SessionID:   "sess-1",
		Domain:      "github.com/globulario/sensei-code",
		BaseSHA:     "abcdef0123456789",
		DecidedAt:   time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC),
		Question:    "Should the reload path keep its cache?",
		Condition:   "graph coverage is absent for the planned files",
		OptionID:    "2",
		OptionLabel: "Authorize removing the cache",
	}
}

// TestResolutionGoesThroughSenseiRatherThanALocalStore covers the governing
// constraint: the human's answer is submitted to Sensei's own review queue as a
// typed proposal, not written anywhere this program then treats as canon.
func TestResolutionGoesThroughSenseiRatherThanALocalStore(t *testing.T) {
	caller := &fakeCaller{result: ToolResult{Structured: map[string]any{
		"status": "ACCEPTED", "accepted": true,
		"candidate_path": "docs/awareness/candidates/contract_unknown-1.yaml",
	}}}
	got := Persist(caller, sample())

	if caller.name != "awareness_propose" {
		t.Fatalf("resolution did not go through Sensei's write path, called %q", caller.name)
	}
	if kind := caller.args["kind"]; kind != "contract_unknown" {
		t.Fatalf("resolution was proposed as %v; a Level-3 escalation means the graph had no contract, so it must be contract_unknown", kind)
	}
	proposed, _ := caller.args["proposed_contract"].(string)
	if !strings.Contains(proposed, "Authorize removing the cache") {
		t.Fatalf("proposed contract does not carry the human's decision: %q", proposed)
	}
	if !strings.Contains(proposed, "graph coverage is absent") {
		t.Fatalf("proposed contract does not carry the condition it answers: %q", proposed)
	}
	if !got.Durable() {
		t.Fatalf("a verified accepted proposal was not durable: %+v", got)
	}
	if !strings.Contains(got.Summary(), "not canonical until promoted") {
		t.Fatalf("summary claims more than a review-queue entry earns: %q", got.Summary())
	}
}

// TestProvenanceTravelsWithTheResolution keeps the evidence bound to the state
// it was decided against.
func TestProvenanceTravelsWithTheResolution(t *testing.T) {
	caller := &fakeCaller{result: ToolResult{Structured: map[string]any{
		"accepted": true, "candidate_path": "p.yaml",
	}}}
	Persist(caller, sample())

	evidence, ok := caller.args["evidence"].([]string)
	if !ok {
		t.Fatalf("evidence was not submitted: %#v", caller.args["evidence"])
	}
	joined := strings.Join(evidence, " | ")
	for _, want := range []string{"task-1", "sess-1", "graph coverage is absent", "abcdef0123456789"} {
		if !strings.Contains(joined, want) {
			t.Errorf("provenance %q missing from evidence: %s", want, joined)
		}
	}
	if domain := caller.args["domain"]; domain != "github.com/globulario/sensei-code" {
		t.Errorf("resolution was not scoped to its domain: %v", domain)
	}
}

// TestUnsupportedPersistenceIsVisibleAndNotASuccess covers "failure to persist
// the resolution is visible and cannot be converted into a successful
// durable-resolution claim".
func TestUnsupportedPersistenceIsVisibleAndNotASuccess(t *testing.T) {
	caller := &fakeCaller{err: errors.New("propose is Unavailable: server started without propose enabled")}
	got := Persist(caller, sample())

	if got.Durable() {
		t.Fatal("an unavailable Sensei produced a durable resolution")
	}
	if got.State != Unsupported {
		t.Fatalf("an unavailable propose was classified %q rather than unsupported", got.State)
	}
	s := got.Summary()
	if !strings.Contains(s, "resolution applied to this run") {
		t.Fatalf("summary does not say the run continued: %q", s)
	}
	if !strings.Contains(s, "unsupported") || !strings.Contains(s, "asked again") {
		t.Fatalf("summary hides that nothing was learned: %q", s)
	}
}

// TestNoFailurePathCanClaimDurability walks every way persistence can go wrong
// and asserts none of them yields Durable(). This is the property that matters:
// a single branch that reported success on a failed write would turn "the
// project learned this" into a lie that nothing downstream could detect.
func TestNoFailurePathCanClaimDurability(t *testing.T) {
	cases := []struct {
		name   string
		caller *fakeCaller
	}{
		{"transport error", &fakeCaller{err: errors.New("connection reset")}},
		{"tool error", &fakeCaller{result: ToolResult{IsError: true, Text: "refused"}}},
		{"no structured result", &fakeCaller{result: ToolResult{Text: "status: ACCEPTED"}}},
		{"accepted without artifact", &fakeCaller{result: ToolResult{Structured: map[string]any{"accepted": true}}}},
		{"validation errors", &fakeCaller{result: ToolResult{Structured: map[string]any{
			"accepted": false, "validation_errors": []any{"title is required"},
		}}}},
		{"not accepted", &fakeCaller{result: ToolResult{Structured: map[string]any{
			"accepted": false, "status": "REJECTED",
		}}}},
		{"malformed payload", &fakeCaller{result: ToolResult{Structured: map[string]any{"accepted": "yes please"}}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Persist(tc.caller, sample())
			if got.Durable() {
				t.Fatalf("%s produced a durable resolution: %+v", tc.name, got)
			}
			if strings.Contains(got.Summary(), "proposed to Sensei for review") {
				t.Fatalf("%s summary claims a review-queue entry: %q", tc.name, got.Summary())
			}
			if !strings.Contains(got.Summary(), "asked again") {
				t.Fatalf("%s summary does not warn the question returns: %q", tc.name, got.Summary())
			}
		})
	}
}

// TestAcceptedWithoutAnArtifactIsNotDurable is called out separately because it
// is the plausible-looking one: Sensei said yes, and there is nothing to point
// at. A status is a claim; a candidate path is a thing a later reader can open.
func TestAcceptedWithoutAnArtifactIsNotDurable(t *testing.T) {
	caller := &fakeCaller{result: ToolResult{Structured: map[string]any{
		"accepted": true, "status": "ACCEPTED",
	}}}
	got := Persist(caller, sample())
	if got.Durable() {
		t.Fatal("acceptance without a candidate path was treated as a verified write")
	}
	if !strings.Contains(got.Detail, "cannot be verified") {
		t.Fatalf("detail does not explain what is missing: %q", got.Detail)
	}
}

// TestRejectionIsDistinctFromUnavailability keeps the two apart, because the
// remedies differ: one is a Sensei that cannot accept proposals at all, the
// other is a proposal this program built badly.
func TestRejectionIsDistinctFromUnavailability(t *testing.T) {
	rejected := Persist(&fakeCaller{result: ToolResult{Structured: map[string]any{
		"accepted": false, "validation_errors": []any{"proposed_contract is required"},
	}}}, sample())
	if rejected.State != Rejected {
		t.Fatalf("a validation failure was classified %q", rejected.State)
	}
	if !strings.Contains(rejected.Summary(), "proposed_contract is required") {
		t.Fatalf("summary drops the reason Sensei gave: %q", rejected.Summary())
	}

	unsupported := Persist(&fakeCaller{err: errors.New("unknown tool \"awareness_propose\"")}, sample())
	if unsupported.State != Unsupported {
		t.Fatalf("a missing tool was classified %q", unsupported.State)
	}
}

// TestResolutionWithoutAQuestionIsRefused stops an empty escalation from being
// filed as project knowledge.
func TestResolutionWithoutAQuestionIsRefused(t *testing.T) {
	caller := &fakeCaller{result: ToolResult{Structured: map[string]any{"accepted": true, "candidate_path": "p"}}}
	got := Persist(caller, Resolution{TaskID: "t", OptionLabel: "something"})
	if got.Durable() {
		t.Fatal("a resolution with no question was filed as durable knowledge")
	}
	if caller.name != "" {
		t.Fatal("an empty resolution was still sent to Sensei")
	}
}

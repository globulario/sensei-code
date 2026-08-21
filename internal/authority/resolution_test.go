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

// Sensei derives a proposal's filename from its title. The title was a pure
// function of the condition, and conditions recur -- so two different answers
// to the same standing question produced the same slug and the second replaced
// the first on disk.
//
// Observed twice in this repository: a recorded decision of "preserve current
// human-owned intent and require another design" was overwritten by a later
// "authorize the architectural change" -- the opposite answer, about different
// work, with nothing left saying the first had ever been given.
func TestTwoDecisionsOnOneConditionDoNotShareATitle(t *testing.T) {
	const condition = "Sensei reported blind spots in the planned region: anchor with severity=critical, file path under high-risk directory"

	first := Resolution{TaskID: "task-1787235334698173142", Condition: condition, OptionLabel: "Preserve current human-owned intent and require another design"}
	second := Resolution{TaskID: "task-1787270680163801586", Condition: condition, OptionLabel: "Authorize the architectural change described above"}

	if title(first) == title(second) {
		t.Fatalf("both decisions carry the same title, so one overwrites the other:\n  %s", title(first))
	}
	for _, r := range []Resolution{first, second} {
		if !strings.Contains(title(r), r.TaskID) {
			t.Errorf("title does not identify the decision: %q", title(r))
		}
		if !strings.Contains(title(r), "blind spots") {
			t.Errorf("title no longer says what was decided: %q", title(r))
		}
	}
}

// The same task answering the same condition is the same decision, and must not
// mint a second record.
func TestOneDecisionKeepsOneTitle(t *testing.T) {
	r := Resolution{TaskID: "task-1", Condition: "a condition", OptionLabel: "an option"}
	if title(r) != title(r) {
		t.Fatal("title is not stable for one decision")
	}
	again := Resolution{TaskID: "task-1", Condition: "a condition", OptionLabel: "a different label"}
	if title(r) != title(again) {
		t.Fatal("the same task and condition produce different titles, so one decision files twice")
	}
}

// Without a task there is nothing to distinguish two decisions, and inventing a
// discriminator would make records look distinct without either being
// traceable. The collision is left visible rather than papered over.
func TestATaskLessDecisionStillNamesTheCondition(t *testing.T) {
	r := Resolution{Condition: "a condition"}
	got := title(r)
	if !strings.Contains(got, "a condition") {
		t.Fatalf("title lost the condition: %q", got)
	}
	if strings.Contains(got, "()") {
		t.Fatalf("title carries an empty identity: %q", got)
	}
}

// Finding 6 of the 2026-08-21 audit. answeredConditions keyed the memo on the
// routing condition alone, and conditions carry no file list: "Sensei requires
// approval for this change class: human_approval_required (blast radius
// security)" is a property of the region, not of the work. A yes given for one
// plan silently authorized every later plan in the same task that reached the
// same gate -- including one touching entirely different files.
func TestOneYesDoesNotAuthorizeADifferentPlan(t *testing.T) {
	const condition = "Sensei requires approval for this change class: human_approval_required (blast radius security)"

	answered := Resolution{Condition: condition, Scope: []string{"internal/provider/provider.go"}}
	different := Resolution{Condition: condition, Scope: []string{"internal/workflow/engine.go", "internal/publish/publish.go"}}

	if answered.Key() == different.Key() {
		t.Fatal("a plan touching different files reuses the answer given for another")
	}
}

// The same plan asked again is the same question, however the file list is
// ordered or repeated -- otherwise the human is re-interrogated in a loop and
// the run never starts, which is what the memo exists to prevent.
func TestTheSamePlanKeepsItsAnswer(t *testing.T) {
	const condition = "a condition"
	a := Resolution{Condition: condition, Scope: []string{"b.go", "a.go"}}
	b := Resolution{Condition: condition, Scope: []string{"a.go", "b.go", "a.go", "  "}}
	if a.Key() != b.Key() {
		t.Fatalf("the same plan produced two keys:\n  %q\n  %q", a.Key(), b.Key())
	}
}

// An answer recorded without a scope cannot be shown to be about the current
// plan. Treating "unknown" as "matches anything" is how the reuse this field
// exists to stop would come back.
func TestAnUnscopedAnswerIsNotAWildcard(t *testing.T) {
	const condition = "a condition"
	unscoped := Resolution{Condition: condition}
	scoped := Resolution{Condition: condition, Scope: []string{"a.go"}}
	if unscoped.Key() == scoped.Key() {
		t.Fatal("an answer with no recorded scope matches a scoped plan")
	}
	if ScopeKey(nil) != ScopeKey([]string{"", "   "}) {
		t.Error("an empty scope and a blank scope are different keys")
	}
}

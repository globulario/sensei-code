// SPDX-License-Identifier: AGPL-3.0-only

package evidence

import (
	"encoding/json"
	"testing"
)

// The #134 shape: a verdict with no question.
func TestAVerdictWithoutItsRequestIsNotReplayable(t *testing.T) {
	verdict := Envelope{Operation: "awareness_preflight"}
	if verdict.Replayable() {
		t.Fatal("an envelope with no request reported itself replayable; that is exactly the record " +
			"that preserved 'human_approval_required (blast radius cluster)' and could never be replayed")
	}
	if !contains(verdict.Missing(), "request") {
		t.Errorf("the missing request is not named: %v", verdict.Missing())
	}

	// The question alone is NOT enough. Replaying against an unknown revision
	// and an unknown graph generation runs a NEW experiment that happens to
	// share a question.
	question := Envelope{
		Operation: "awareness_preflight",
		Request:   map[string]any{"task": "widen the boundary", "files": []string{"a.go"}, "mode": "compact"},
	}
	if !question.HasQuestion() {
		t.Fatal("an envelope carrying operation and request has its question")
	}
	if question.Replayable() {
		t.Fatal("an envelope with no revision and no graph generation called itself replayable, " +
			"while Missing() named both as absent -- two answers from one type")
	}
	full := question
	full.Revision = "abc123"
	full.GraphDigest = "digest"
	if !full.Replayable() {
		t.Fatal("a complete envelope is not replayable")
	}
}

// A partial envelope must report itself as partial rather than be discovered
// when the replay is attempted -- which is when #134 was discovered.
func TestAPartialEnvelopeNamesWhatIsMissing(t *testing.T) {
	e := Envelope{Operation: "awareness_preflight", Request: map[string]any{"task": "x"}}
	m := e.Missing()
	for _, want := range []string{"revision", "graph_digest"} {
		if !contains(m, want) {
			t.Errorf("%q is absent and unnamed: %v", want, m)
		}
	}
	// The weaker property survives, so a caller can report "the question is
	// here, the world is not" rather than choosing between true and false.
	if !e.HasQuestion() {
		t.Error("the question was lost as well as the world")
	}
	if e.Replayable() {
		t.Error("a partial envelope called itself replayable")
	}
}

// The envelope travels ALONGSIDE the result. Every existing reader of these
// payloads looks for result fields such as risk_class, and displacing them
// would break consumers to record provenance -- trading one loss for another.
func TestTheEnvelopeDoesNotDisplaceTheResult(t *testing.T) {
	result := map[string]any{"risk_class": "SECURITY_RISK", "status": "PREFLIGHT_STATUS_OK"}
	e := Envelope{Operation: "awareness_preflight", Request: map[string]any{"task": "t"}, Revision: "abc123"}

	rec := e.Record(result)
	if rec["risk_class"] != "SECURITY_RISK" || rec["status"] != "PREFLIGHT_STATUS_OK" {
		t.Fatalf("recording provenance displaced the result: %+v", rec)
	}
	if _, polluted := result["evidence"]; polluted {
		t.Error("Record mutated the caller's result map, which the caller decodes afterwards")
	}

	// It has to survive the durable payload, which is JSON.
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("the record does not marshal: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("round trip: %v", err)
	}
	ev, ok := back["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("the envelope did not survive the round trip: %s", raw)
	}
	req, ok := ev["request"].(map[string]any)
	if !ok || req["task"] != "t" {
		t.Fatalf("the REQUEST did not survive, and it is the half that makes a replay possible: %s", raw)
	}
	if ev["revision"] != "abc123" {
		t.Errorf("the revision did not survive: %s", raw)
	}
}

// An empty optional field must be absent, not present-and-empty: a record
// carrying revision:"" claims a revision was captured.
func TestAnUncapturedFieldIsAbsentNotEmpty(t *testing.T) {
	e := Envelope{Operation: "op", Request: map[string]any{"a": 1}}
	p := e.payload()
	for _, k := range []string{"revision", "graph_digest", "tool"} {
		if _, present := p[k]; present {
			t.Errorf("%q was not captured but appears in the record, claiming it was", k)
		}
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

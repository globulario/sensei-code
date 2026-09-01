package workflow

// A preserved verdict is not a preserved experiment.
//
// sensei-code#134 preserved a run that stopped at the authority boundary
// carrying "human_approval_required (blast radius cluster)" and NOT the request
// that produced it. routePlan issues the preflight whose verdict decides
// routing, and it emitted nothing at all -- so when the upstream repair was
// ready the specimen could not be replayed, and the escalation no longer
// reproduced even on the binary that had produced it.
//
// Structural tests, in this file's existing idiom: routePlan cannot be driven
// without a live Sensei, and what matters is a property of the source -- that
// the request reaches the record, and reaches it before anything can return
// early.

import (
	"encoding/json"
	"strings"
	"testing"
)

func routePlanBody(t *testing.T) string {
	t.Helper()
	src := rawSource(t, "internal/workflow/engine.go")
	at := strings.Index(src, "func (e *Engine) routePlan(")
	if at < 0 {
		t.Fatal("routePlan is gone from engine.go")
	}
	body := src[at:]
	if end := strings.Index(body, "\n}\n"); end > 0 {
		body = body[:end]
	}
	return body
}

func TestRoutePlanRecordsTheRequestItRoutesOn(t *testing.T) {
	body := routePlanBody(t)
	if !strings.Contains(body, "preflightRecord(args, result.Structured)") {
		t.Fatal("routePlan no longer records the preflight REQUEST beside its result — " +
			"a run that stops at the authority boundary would preserve the answer and lose the question")
	}
}

// The record must be written before anything can return early, or a decode
// failure loses the question exactly when it is most wanted.
func TestTheRequestIsRecordedBeforeDecodingCanFail(t *testing.T) {
	body := routePlanBody(t)
	record := strings.Index(body, "preflightRecord(")
	decode := strings.Index(body, "sensei.DecodePreflight(result)")
	if record < 0 || decode < 0 {
		t.Fatal("routePlan no longer records the request, or no longer decodes the preflight")
	}
	if record > decode {
		t.Error("the request is recorded after DecodePreflight, whose error path returns early — " +
			"a malformed preflight would lose the question it was asked")
	}
}

// The request must travel ALONGSIDE the result, not instead of it: existing
// readers of a sensei.result payload look for result fields such as risk_class.
func TestRecordingTheRequestDoesNotDisplaceTheResult(t *testing.T) {
	structured := map[string]any{"risk_class": "SECURITY_RISK", "status": "PREFLIGHT_STATUS_OK"}
	args := map[string]any{"task": "widen the boundary", "files": []string{"a.go"}, "mode": "compact"}

	rec := preflightRecord(args, structured)
	if rec["risk_class"] != "SECURITY_RISK" || rec["status"] != "PREFLIGHT_STATUS_OK" {
		t.Fatalf("recording the request displaced the result a reader depends on: %+v", rec)
	}
	req, ok := rec["request"].(map[string]any)
	if !ok {
		t.Fatalf("the request is not on the record: %+v", rec)
	}
	if req["task"] != "widen the boundary" || req["mode"] != "compact" {
		t.Errorf("the request is on the record but incomplete: %+v", req)
	}

	// The caller decodes the same result afterwards, so the source map must not
	// have been mutated.
	if _, polluted := structured["request"]; polluted {
		t.Error("preflightRecord mutated the result map the caller still decodes")
	}

	// It has to survive the event payload round-trip, which is JSON.
	raw, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("the record does not marshal into an event payload: %v", err)
	}
	var back map[string]any
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("the payload does not survive the round trip: %v", err)
	}
	rq, ok := back["request"].(map[string]any)
	if !ok {
		t.Fatalf("the request did not survive the round trip: %s", raw)
	}
	files, ok := rq["files"].([]any)
	if !ok || len(files) != 1 || files[0] != "a.go" {
		t.Errorf("the FILES did not survive, and they are half of what makes a replay possible: %s", raw)
	}
}

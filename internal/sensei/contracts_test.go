package sensei

import (
	"encoding/json"
	"strings"
	"testing"
)

// structured builds a ToolResult the way the MCP client would hand one over,
// from the literal JSON Sensei publishes, so these tests exercise the real
// wire shape rather than a Go struct that happens to agree with itself.
func structured(t *testing.T, text, body string) ToolResult {
	t.Helper()
	var m map[string]any
	if body != "" {
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("test fixture is not valid JSON: %v", err)
		}
	}
	r := ToolResult{Structured: m}
	if text != "" {
		r.Content = append(r.Content, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: text})
	}
	return r
}

// TestRequiredResultWithoutStructuredContentFailsClosed covers the acceptance
// criterion "empty required structuredContent fails closed".
//
// This is the case that reads as healthy in a transcript: Sensei answered,
// there is prose to show the human, nothing errored. Decoding it into a
// zero-valued struct would yield a verdict of "" for every enum, and the whole
// point of the typed layer is that "" can never reach a gate.
func TestRequiredResultWithoutStructuredContentFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		decode func(ToolResult) error
	}{
		{"preflight", func(r ToolResult) error { _, err := DecodePreflight(r); return err }},
		{"diff audit", func(r ToolResult) error { _, err := DecodeDiffAudit(r); return err }},
		{"workspace status", func(r ToolResult) error { _, err := DecodeWorkspaceStatus(r); return err }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.decode(structured(t, "everything looks fine to me", ""))
			if err == nil {
				t.Fatal("a result with prose but no structured content decoded as a verdict")
			}
			var ce *ContractError
			if !asContractError(err, &ce) {
				t.Fatalf("want a ContractError so callers can tell this from a transport failure, got %T: %v", err, err)
			}
		})
	}
}

// TestMalformedRequiredResultFailsClosed covers "malformed required result
// fails closed" — here the payload is present but does not match the contract.
func TestMalformedRequiredResultFailsClosed(t *testing.T) {
	// status is an object where the contract says string.
	r := structured(t, "preflight ok", `{"status": {"unexpected": "shape"}}`)
	if _, err := DecodePreflight(r); err == nil {
		t.Fatal("a structurally malformed preflight decoded as a verdict")
	}

	// Present, well-formed JSON that simply omits the governing field.
	r = structured(t, "audit complete", `{"availability": "available"}`)
	if _, err := DecodeDiffAudit(r); err == nil {
		t.Fatal("an audit result with no decision field decoded as a verdict")
	}
}

// TestUnavailableSenseiIsNotACleanResult covers "unavailable Sensei is not
// rendered as a clean result".
func TestUnavailableSenseiIsNotACleanResult(t *testing.T) {
	r := structured(t, "graph unavailable", `{"decision": "cannot_verify", "availability": "cannot_verify"}`)
	audit, err := DecodeDiffAudit(r)
	if err != nil {
		t.Fatalf("a well-formed cannot_verify result should decode, then refuse: %v", err)
	}
	if audit.ReviewerMayAccept() {
		t.Fatal("an audit that could not be performed permitted acceptance")
	}
	if audit.Diagnostic() == "" {
		t.Fatal("an unverifiable audit produced no diagnostic for the human")
	}

	// A tool error is likewise never a verdict.
	if _, err := DecodePreflight(ToolResult{IsError: true, Structured: map[string]any{"status": "PREFLIGHT_STATUS_OK"}}); err == nil {
		t.Fatal("a tool error carrying an OK-looking payload decoded as a pass")
	}
}

// TestBlockedAuditNeverPermitsReviewerAcceptance is the authority inversion the
// slice exists to close: Sensei refuses, and no reviewer verdict may override
// that refusal.
func TestBlockedAuditNeverPermitsReviewerAcceptance(t *testing.T) {
	r := structured(t, "audit found a violation", `{
		"decision": "block",
		"availability": "available",
		"findings": [{"id": "inv.x", "file": "internal/workflow/engine.go", "disposition": "block", "detail": "violates a critical invariant"}]
	}`)
	audit, err := DecodeDiffAudit(r)
	if err != nil {
		t.Fatal(err)
	}
	if audit.ReviewerMayAccept() {
		t.Fatal("a blocking audit permitted reviewer acceptance")
	}
	if !audit.Blocks() {
		t.Fatal("a blocking audit did not report itself as blocking")
	}
	d := audit.Diagnostic()
	if !strings.Contains(d, "engine.go") || !strings.Contains(d, "violates a critical invariant") {
		t.Fatalf("diagnostic does not name the blocking finding, so the worker cannot act on it: %q", d)
	}
}

// TestReviewVerdictLeavesRoomForTheReviewer guards the other direction. Failing
// closed must not mean refusing everything: Sensei's "review" verdict means
// precisely that a reviewer should judge, and a gate that rejected it too would
// make the audit unusable rather than authoritative.
func TestReviewVerdictLeavesRoomForTheReviewer(t *testing.T) {
	audit, err := DecodeDiffAudit(structured(t, "needs review", `{"decision": "review", "availability": "available"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !audit.ReviewerMayAccept() {
		t.Fatal("Sensei asked for a reviewer's judgement and the gate refused to let the reviewer give one")
	}
	if audit.Blocks() {
		t.Fatal("a review verdict reported itself as a block")
	}
}

// TestUnrecognisedVerdictFailsClosed is the forward-compatibility guarantee: a
// Sensei newer than this binary can return an enum member this code has never
// seen, and it must fail safe on the day it ships rather than being read as a
// pass until somebody notices.
func TestUnrecognisedVerdictFailsClosed(t *testing.T) {
	audit, err := DecodeDiffAudit(structured(t, "", `{"decision": "quarantined", "availability": "available"}`))
	if err != nil {
		t.Fatal(err)
	}
	if audit.ReviewerMayAccept() {
		t.Fatal("an unrecognised audit decision was treated as permission")
	}
	if !strings.Contains(audit.Diagnostic(), "quarantined") {
		t.Fatalf("diagnostic hides the unrecognised value: %q", audit.Diagnostic())
	}

	p, err := DecodePreflight(structured(t, "", `{"status": "PREFLIGHT_STATUS_QUARANTINED"}`))
	if err != nil {
		t.Fatal(err)
	}
	if p.Permits() {
		t.Fatal("an unrecognised preflight status was treated as permission")
	}
}

// TestStaleGraphIsNotCertifiable is the state that had no representation at all
// before this slice. A stale graph still answers, and answers fluently; that is
// what makes it dangerous rather than merely unhelpful.
func TestStaleGraphIsNotCertifiable(t *testing.T) {
	for _, body := range []string{
		`{"status":"PREFLIGHT_STATUS_OK","authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_STALE","seed_state":"SEED_STATE_CURRENT"}}`,
		`{"status":"PREFLIGHT_STATUS_OK","authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_STALE"}}`,
		`{"status":"PREFLIGHT_STATUS_OK","authority":{"authoritative":false,"verdict":"unverified","graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT"}}`,
		`{"status":"PREFLIGHT_STATUS_OK","authority":{"authoritative":true,"seed_state":"SEED_STATE_CURRENT"}}`,
	} {
		p, err := DecodePreflight(structured(t, "preflight ok", body))
		if err != nil {
			t.Fatal(err)
		}
		if p.Permits() {
			t.Fatalf("preflight permitted work on a non-certifiable graph: %s", body)
		}
		if p.Diagnostic() == "" {
			t.Fatalf("no diagnostic explaining the refusal: %s", body)
		}
	}
}

// TestCertifiablePreflightPermits keeps the gate honest in the affirmative
// direction, using the exact payload shape this repository's own Sensei returns.
func TestCertifiablePreflightPermits(t *testing.T) {
	p, err := DecodePreflight(structured(t, "ok", `{
		"status": "PREFLIGHT_STATUS_OK",
		"risk_class": "ARCHITECTURE_SENSITIVE",
		"confidence": "CONFIDENCE_MEDIUM",
		"authority": {
			"authoritative": true,
			"verdict": "authoritative",
			"state": "current",
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
			"seed_state": "SEED_STATE_CURRENT",
			"build_provenance_state": "BUILD_PROVENANCE_STATE_STAMPED"
		},
		"direct_invariants": [{"id":"invariant:x","label":"a rule","severity":"critical","status":"active"}],
		"required_actions": ["run the tests"]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if !p.Permits() {
		t.Fatalf("a fully certifiable preflight was refused: %s", p.Diagnostic())
	}
	if p.RiskClass != "ARCHITECTURE_SENSITIVE" || len(p.DirectInvariants) != 1 {
		t.Fatalf("typed fields did not survive decoding: %+v", p)
	}
	if p.DirectInvariants[0].Label != "a rule" {
		t.Fatalf("invariant label lost: %+v", p.DirectInvariants[0])
	}
}

// TestIncompleteWorkspaceCompositionFailsClosed covers the workspace surface:
// governing a candidate whose identity is only partly known is governing
// something other than what ships.
func TestIncompleteWorkspaceCompositionFailsClosed(t *testing.T) {
	w, err := DecodeWorkspaceStatus(structured(t, "partial", `{
		"composition_state": "partial",
		"limitations": [{"code": "domain_unregistered", "detail": "no domain entry for this repository"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	if w.Permits() {
		t.Fatal("a partially composed workspace permitted governed work")
	}
	if !strings.Contains(w.Diagnostic(), "domain_unregistered") {
		t.Fatalf("diagnostic does not name the limitation: %q", w.Diagnostic())
	}
}

// TestTextStructuredDisagreementSurfacesAsDiagnostic covers the acceptance
// criterion that a text/structured disagreement follows the structured
// contract and surfaces the discrepancy.
func TestTextStructuredDisagreementSurfacesAsDiagnostic(t *testing.T) {
	msg := Discrepancy("diff audit", "audit result: pass, no violations found", string(AuditBlock), AuditDecisionTokens())
	if msg == "" {
		t.Fatal("prose claiming pass beside a structured block produced no discrepancy diagnostic")
	}
	if !strings.Contains(msg, "pass") || !strings.Contains(msg, "block") {
		t.Fatalf("diagnostic does not name both readings: %q", msg)
	}
	if !strings.Contains(msg, "structured verdict governs") {
		t.Fatalf("diagnostic does not say which one wins: %q", msg)
	}

	// Agreement is silent.
	if msg := Discrepancy("diff audit", "audit result: block", string(AuditBlock), AuditDecisionTokens()); msg != "" {
		t.Fatalf("agreement produced a spurious discrepancy: %q", msg)
	}
}

func asContractError(err error, target **ContractError) bool {
	ce, ok := err.(*ContractError)
	if ok {
		*target = ce
	}
	return ok
}

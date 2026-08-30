package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

func result(t *testing.T, text, body string) sensei.ToolResult {
	t.Helper()
	var m map[string]any
	if body != "" {
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("fixture is not valid JSON: %v", err)
		}
	}
	r := sensei.ToolResult{Structured: m}
	if text != "" {
		r.Content = append(r.Content, struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}{Type: "text", Text: text})
	}
	return r
}

const okWorkspace = `{
	"composition_state": "complete",
	"binding": {"repository_domain": "github.com/globulario/sensei-code"}
}`

const okPreflight = `{
	"status": "PREFLIGHT_STATUS_OK",
	"risk_class": "ARCHITECTURE_SENSITIVE",
	"required_actions": ["run the tests"],
	"authority": {
		"authoritative": true,
		"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
		"seed_state": "SEED_STATE_CURRENT"
	}
}`

// TestReviewerCannotAcceptOverASenseiBlock is the authority inversion this
// slice exists to close. Before the typed gate the audit reached the reviewer
// only as prose in a prompt, so an "accept" concluded the candidate no matter
// what Sensei had decided.
func TestReviewerCannotAcceptOverASenseiBlock(t *testing.T) {
	audit, err := sensei.DecodeDiffAudit(result(t, "looks fine to me", `{
		"decision": "block",
		"availability": "available",
		"findings": [{"id": "inv.scope", "file": "internal/gitx/git.go", "disposition": "block", "detail": "widens worker scope beyond the plan"}]
	}`))
	if err != nil {
		t.Fatal(err)
	}

	got := judgeCandidate("accept", audit)
	if got.Accepted {
		t.Fatal("reviewer acceptance concluded a candidate that Sensei blocked")
	}
	if !got.Overrode {
		t.Fatal("the reviewer/Sensei disagreement was not recorded as an override")
	}
	if !strings.Contains(got.Refusal, "widens worker scope") {
		t.Fatalf("refusal does not carry the finding the implementor must fix: %q", got.Refusal)
	}
	if instruction := reviseInstruction(got); !strings.Contains(instruction, "widens worker scope") {
		t.Fatalf("revise instruction does not tell the implementor what to fix: %q", instruction)
	}
}

// TestUnverifiableAuditIsNotAcceptance covers the quieter half: an audit that
// could not run is not a pass, however healthy the transcript looks.
func TestUnverifiableAuditIsNotAcceptance(t *testing.T) {
	audit, err := sensei.DecodeDiffAudit(result(t, "audit complete", `{"decision": "cannot_verify", "availability": "cannot_verify"}`))
	if err != nil {
		t.Fatal(err)
	}
	if judgeCandidate("accept", audit).Accepted {
		t.Fatal("a candidate was accepted on an audit that could not be performed")
	}
}

// TestReviewerMayStillAcceptWhatSenseiPermits guards against the gate becoming
// a blanket refusal. If "review" stopped acceptance too, the audit would be
// unusable rather than authoritative, and the product would stall on every
// change Sensei merely wanted a human to look at.
func TestReviewerMayStillAcceptWhatSenseiPermits(t *testing.T) {
	for _, decision := range []string{"pass", "review"} {
		audit, err := sensei.DecodeDiffAudit(result(t, "", `{"decision": "`+decision+`", "availability": "available"}`))
		if err != nil {
			t.Fatal(err)
		}
		got := judgeCandidate("accept", audit)
		if !got.Accepted {
			t.Fatalf("Sensei returned %q and the gate still refused acceptance: %s", decision, got.Refusal)
		}
		if got.Overrode {
			t.Fatalf("a permitted acceptance was recorded as an override for %q", decision)
		}
	}
}

// TestNonAcceptReviewIsUntouchedByTheGate keeps the gate's scope narrow: it
// governs acceptance, and must not quietly convert a revise into an accept.
func TestNonAcceptReviewIsUntouchedByTheGate(t *testing.T) {
	audit, err := sensei.DecodeDiffAudit(result(t, "", `{"decision": "pass", "availability": "available"}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := judgeCandidate("revise", audit); got.Accepted {
		t.Fatal("a revise verdict was treated as acceptance because the audit passed")
	}
}

// TestStartRefusedOnStaleGraphBeforeAnyWorkerRuns covers "preflight refusal
// prevents worker execution even if the architect says proceed". The gate is
// structural: certifiedStart cannot be constructed except by certifyStart, and
// every path that starts a worker requires one.
func TestStartRefusedOnStaleGraphBeforeAnyWorkerRuns(t *testing.T) {
	stale := `{
		"status": "PREFLIGHT_STATUS_OK",
		"authority": {
			"authoritative": true,
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_STALE",
			"graph_freshness_detail": "live store does not match the validated artifact",
			"seed_state": "SEED_STATE_CURRENT"
		}
	}`
	_, err := certifyStart(result(t, "ok", okWorkspace), result(t, "preflight ok", stale), "")
	if err == nil {
		t.Fatal("a stale graph certified a start")
	}
	if !strings.Contains(err.Error(), "stale") {
		t.Fatalf("refusal does not say the graph was stale: %v", err)
	}
}

// TestStartRefusedWhenSenseiSaysNothing is the empty-answer case: prose in the
// transcript, no structured payload, and previously a zero-valued verdict
// carried forward as though Sensei had spoken.
func TestStartRefusedWhenSenseiSaysNothing(t *testing.T) {
	if _, err := certifyStart(result(t, "workspace looks good", ""), result(t, "preflight ok", okPreflight), ""); err == nil {
		t.Fatal("an empty workspace status certified a start")
	}
	if _, err := certifyStart(result(t, "ok", okWorkspace), result(t, "preflight ok", ""), ""); err == nil {
		t.Fatal("an empty preflight certified a start")
	}
}

// TestStartRefusedOnPartialWorkspaceComposition covers the identity half:
// governing a candidate whose repository identity is only partly known governs
// something other than what ships.
func TestStartRefusedOnPartialWorkspaceComposition(t *testing.T) {
	partial := `{
		"composition_state": "partial",
		"limitations": [{"code": "domain_unregistered", "detail": "no domain entry for this repository"}]
	}`
	_, err := certifyStart(result(t, "", partial), result(t, "", okPreflight), "")
	if err == nil {
		t.Fatal("a partially composed workspace certified a start")
	}
	if !strings.Contains(err.Error(), "domain_unregistered") {
		t.Fatalf("refusal does not name the limitation: %v", err)
	}
}

// TestCertifiedStartCarriesSenseiFactsForward proves the gate is not merely a
// veto: it is where the typed facts enter the workflow, so downstream code
// stops re-deriving them from prose.
func TestCertifiedStartCarriesSenseiFactsForward(t *testing.T) {
	start, err := certifyStart(result(t, "", okWorkspace), result(t, "", okPreflight), "")
	if err != nil {
		t.Fatalf("a fully certifiable pair was refused: %v", err)
	}
	if start.Domain() != "github.com/globulario/sensei-code" {
		t.Fatalf("domain did not survive the gate: %q", start.Domain())
	}
	if start.RiskClass() != "ARCHITECTURE_SENSITIVE" {
		t.Fatalf("risk class did not survive the gate: %q", start.RiskClass())
	}
	if len(start.RequiredActions()) != 1 {
		t.Fatalf("required actions did not survive the gate: %v", start.RequiredActions())
	}
}

// TestUnscopedStartPreflightDoesNotBlockEveryTask pins the payload this
// engine's own start-of-task call actually produces.
//
// The engine asks for a preflight before a plan exists, so it names no files,
// and Sensei correctly answers PREFLIGHT_STATUS_EMPTY with UNKNOWN_IMPACT: no
// files were named, so no impact was found. An earlier version of this gate
// required PREFLIGHT_STATUS_OK and therefore refused every task in the product
// on the strength of the caller's own unscoped query — a gate that fails closed
// on everything is indistinguishable from a broken one. This was caught by
// running the decoders against the live server rather than against fixtures,
// which is why the live payload is reproduced here verbatim.
func TestUnscopedStartPreflightDoesNotBlockEveryTask(t *testing.T) {
	live := `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"risk_class": "UNKNOWN_IMPACT",
		"confidence": "CONFIDENCE_LOW",
		"authority": {
			"authoritative": true,
			"verdict": "authoritative",
			"state": "current",
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
			"seed_state": "SEED_STATE_CURRENT",
			"build_provenance_state": "BUILD_PROVENANCE_STATE_STAMPED"
		}
	}`
	start, err := certifyStart(result(t, "", okWorkspace), result(t, "preflight empty", live), "")
	if err != nil {
		t.Fatalf("the engine's own start-of-task preflight was refused, which would block every task: %v", err)
	}
	if start.Domain() == "" {
		t.Fatal("certified start lost the domain")
	}
}

// TestDegradedPreflightStillRefusesStart keeps the correction from going too
// far. EMPTY is the absence of a scoped question; DEGRADED is Sensei stating
// that its own answer is impaired, which is a real negative and must refuse.
func TestDegradedPreflightStillRefusesStart(t *testing.T) {
	degraded := `{
		"status": "PREFLIGHT_STATUS_DEGRADED",
		"authority": {
			"authoritative": true,
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
			"seed_state": "SEED_STATE_CURRENT"
		}
	}`
	if _, err := certifyStart(result(t, "", okWorkspace), result(t, "", degraded), ""); err == nil {
		t.Fatal("a degraded preflight certified a start")
	}
}

// TestEmptyPreflightStillRefusesOnAnUncertifiableGraph proves the relaxation is
// scoped to the status field only. An unscoped query is forgivable; a graph
// that cannot vouch for itself is not, whatever the status says.
func TestEmptyPreflightStillRefusesOnAnUncertifiableGraph(t *testing.T) {
	staleAndEmpty := `{
		"status": "PREFLIGHT_STATUS_EMPTY",
		"authority": {
			"authoritative": true,
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_STALE",
			"seed_state": "SEED_STATE_CURRENT"
		}
	}`
	if _, err := certifyStart(result(t, "", okWorkspace), result(t, "", staleAndEmpty), ""); err == nil {
		t.Fatal("an empty preflight on a stale graph certified a start")
	}
}

// TestGraphSourceCommitIsNotComparedToTheGovernedRepository pins a correction.
//
// An earlier version refused a start when the graph's SourceRepoCommit differed
// from the governed repository's HEAD. That comparison is meaningless here:
// SourceRepoCommit is the identity of the rule snapshot, and on this
// installation the graph server runs with -home-domain
// github.com/globulario/services, so the value is a services commit. Comparing
// it to a sensei-code commit can never match, and the refusal told the human to
// rebuild a graph that was already current.
func TestGraphSourceCommitIsNotComparedToTheGovernedRepository(t *testing.T) {
	// A graph whose corpus commit belongs to another repository, which is the
	// normal case, must still certify a start.
	otherRepo := `{
		"status": "PREFLIGHT_STATUS_OK",
		"authority": {
			"authoritative": true,
			"verdict": "authoritative",
			"graph_freshness_state": "GRAPH_FRESHNESS_STATE_CURRENT",
			"seed_state": "SEED_STATE_CURRENT",
			"source_repo_commit": "da512eb61c82"
		}
	}`
	if _, err := certifyStart(result(t, "", okWorkspace), result(t, "", otherRepo), "f3e5ef38b09b22450351771d69371ebcc57d0176"); err != nil {
		t.Fatalf("a graph whose corpus commit belongs to another repository was refused: %v", err)
	}
}

// TestAnUnreadableHeadDoesNotBlockTheOtherChecks keeps the new comparison from
// becoming a hard dependency on git being readable.
func TestAnUnreadableHeadDoesNotBlockTheOtherChecks(t *testing.T) {
	if _, err := certifyStart(result(t, "", okWorkspace), result(t, "", okPreflight), ""); err != nil {
		t.Fatalf("an unknown repository head refused an otherwise certifiable start: %v", err)
	}
}

// TestTheGraphDigestComesFromWhereSenseiReportsIt pins the source.
//
// The first real governed run emitted a receipt saying the certified start
// "did not carry a live graph digest" while the digest was sitting in the
// workspace contract the gate had already decoded. The receipt was right that
// it had not been given one; the accessor was looking in the wrong result.
func TestTheGraphDigestComesFromWhereSenseiReportsIt(t *testing.T) {
	const digest = "42e6e12cd5737530c4c8d054f8178cde849b72cae7c4845b6613f07a714d2b64"
	var start certifiedStart
	start.workspace.GraphAuthority = &sensei.Authority{LiveStoreGraphDigest: digest}
	if got := start.GraphDigest(); got != digest {
		t.Fatalf("GraphDigest() = %q, want the workspace contract's digest", got)
	}
	// The build commit is a DIFFERENT fact and never stands in for it.
	var only certifiedStart
	only.workspace.GraphAuthority = &sensei.Authority{GraphBuildCommit: "fac399f8225f"}
	if got := only.GraphDigest(); got != "" {
		t.Fatalf("GraphDigest() = %q; the build commit must not stand in for the digest", got)
	}
	// A workspace that reports none, with preflight carrying it, still resolves.
	var viaPreflight certifiedStart
	viaPreflight.preflight.Authority.LiveStoreGraphDigest = digest
	if got := viaPreflight.GraphDigest(); got != digest {
		t.Fatalf("GraphDigest() = %q, want the preflight digest when it is the only one", got)
	}
}

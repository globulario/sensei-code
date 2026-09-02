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
	if !strings.Contains(body, "e.preflightRecord(args, result.Structured") {
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

	rec := (&Engine{}).preflightRecord(args, structured, "rev-abc", "digest-xyz")
	if rec["risk_class"] != "SECURITY_RISK" || rec["status"] != "PREFLIGHT_STATUS_OK" {
		t.Fatalf("recording the request displaced the result a reader depends on: %+v", rec)
	}
	env, ok := rec["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("the evidence envelope is not on the record: %+v", rec)
	}
	req, ok := env["request"].(map[string]any)
	if !ok {
		t.Fatalf("the request is not on the record: %+v", rec)
	}
	// The envelope now also carries the world the verdict was true in.
	if env["revision"] != "rev-abc" || env["graph_digest"] != "digest-xyz" {
		t.Errorf("the causal envelope lost its revision or graph generation: %+v", env)
	}
	if req["task"] != "widen the boundary" || req["mode"] != "compact" {
		t.Errorf("the request is on the record but incomplete: %+v", req)
	}

	// The caller decodes the same result afterwards, so the source map must not
	// have been mutated.
	if _, polluted := structured["evidence"]; polluted {
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
	be, ok := back["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("the envelope did not survive the round trip: %s", raw)
	}
	rq, ok := be["request"].(map[string]any)
	if !ok {
		t.Fatalf("the request did not survive the round trip: %s", raw)
	}
	files, ok := rq["files"].([]any)
	if !ok || len(files) != 1 || files[0] != "a.go" {
		t.Errorf("the FILES did not survive, and they are half of what makes a replay possible: %s", raw)
	}
}

// The class is closed: every preflight whose result is EMITTED as a durable
// record must carry its request.
//
// #137 repaired one call site. An audit then found one of six recording its
// request, so repairing specimens one at a time would have left the next site
// to be forgotten -- and the sixth would look exactly like the first five until
// someone needed to replay it.
//
// Structural, in this package's existing rawSource idiom: what matters is a
// property of the source, and these paths cannot be driven without a live
// Sensei.
func TestEveryEmittedPreflightCarriesItsRequest(t *testing.T) {
	// Sites that call preflight and produce NO durable record. They are listed
	// with the reason, so an exemption is a statement rather than an omission.
	// A site that starts emitting must leave this list.
	exempt := map[string]string{
		"unexaminedPlannedFiles": "returns to a caller that emits; the per-file probe is not itself a record",
		"emitChangeReport":       "the result feeds ChangeReported, which carries its own subject and diff",
	}

	for _, file := range []string{"internal/workflow/engine.go", "internal/workflow/assisted.go"} {
		src := rawSource(t, file)
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if !strings.Contains(line, `CallTool("awareness_preflight"`) {
				continue
			}
			// The enclosing function.
			fn := ""
			for j := i; j >= 0; j-- {
				if strings.HasPrefix(lines[j], "func ") {
					fn = lines[j]
					break
				}
			}
			name := enclosingFuncName(fn)
			if _, ok := exempt[name]; ok {
				continue
			}
			// Does this site emit, and if so does it record the request?
			window := strings.Join(lines[i:min(i+30, len(lines))], "\n")
			if !strings.Contains(window, "event.SenseiResult") {
				continue // emits nothing here
			}
			if !strings.Contains(window, "preflightRecord(") {
				t.Errorf("%s: the preflight in %s emits a durable record WITHOUT its request; "+
					"a preserved verdict is not a preserved experiment", file, name)
			}
		}
	}
}

func enclosingFuncName(decl string) string {
	if i := strings.Index(decl, ") "); i >= 0 {
		decl = decl[i+2:]
	} else {
		decl = strings.TrimPrefix(decl, "func ")
	}
	if i := strings.Index(decl, "("); i > 0 {
		return decl[:i]
	}
	return strings.TrimSpace(decl)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// The envelope's revision is the GOVERNED CHECKOUT, not the graph's source
// commit — and the codebase already said so.
//
// gate.go's own comment on GraphSourceCommit reads: "It is emphatically not the
// candidate's base: the two differ whenever the graph has not been rebuilt
// since the last commit, and conflating them records a human decision against a
// commit nobody was working on."
//
// I used SourceRepoCommit() as the envelope's revision anyway, in a file whose
// neighbour documents the exact mistake. A replay would then check the wrong
// source and could not reproduce the run.
func TestTheEnvelopeRevisionIsTheGovernedCheckoutNotTheGraphSource(t *testing.T) {
	for _, file := range []string{"internal/workflow/engine.go", "internal/workflow/assisted.go"} {
		src := rawSource(t, file)
		for _, line := range strings.Split(src, "\n") {
			if !strings.Contains(line, "preflightRecord(") {
				continue
			}
			for _, wrong := range []string{"SourceRepoCommit()", "GraphSourceCommit()", "GraphBuildCommit()"} {
				if strings.Contains(line, wrong) {
					t.Errorf("%s: an envelope records %s as its subject revision; that is the commit the "+
						"GRAPH was built from, not the checkout the change is against", file, wrong)
				}
			}
		}
	}
}

// The digest belongs to the response that ANSWERED, not to an earlier call.
//
// Every site took it from the preceding sensei_workspace_status response. Two
// RPCs are two moments: if the live graph is replaced between them the event
// pairs a new verdict with an old generation, and in the assisted lane a failed
// workspace call followed by a successful preflight paired a verdict with no
// generation at all.
func TestTheEnvelopeDigestComesFromTheAnsweringPreflight(t *testing.T) {
	for _, file := range []string{"internal/workflow/engine.go", "internal/workflow/assisted.go"} {
		src := rawSource(t, file)
		if strings.Contains(src, "LiveGraphDigest(workspaceStatus)") {
			t.Errorf("%s: an envelope takes its graph generation from the workspace-status call "+
				"rather than from the preflight that produced the verdict", file)
		}
	}
}

// The subject revision is captured BEFORE the question is asked.
//
// Reading HEAD after the response attaches the checkout as it is NOW to a
// verdict whose question was issued against the checkout as it was THEN. If
// HEAD moves while the call is in flight, the envelope pairs an answer with a
// subject it was never asked about — a smaller version of the #134 defect,
// produced by the repair for it.
func TestTheSubjectRevisionIsCapturedBeforeTheCall(t *testing.T) {
	for _, file := range []string{"internal/workflow/engine.go", "internal/workflow/assisted.go"} {
		src := rawSource(t, file)
		lines := strings.Split(src, "\n")
		for i, line := range lines {
			if !strings.Contains(line, `CallTool("awareness_preflight"`) {
				continue
			}
			// Anything recording a revision must have captured it above this
			// call, not below it.
			window := strings.Join(lines[i:min(i+30, len(lines))], "\n")
			if !strings.Contains(window, "preflightRecord(") {
				continue
			}
			// Only a revision read INSIDE the record call is the defect. A
			// repositoryHead elsewhere in the window -- certifyStart takes one
			// legitimately -- is a different question asked at a different
			// moment, and flagging it made this test fail on correct code.
			for _, l := range strings.Split(window, "\n") {
				if strings.Contains(l, "preflightRecord(") && strings.Contains(l, "repositoryHead(") {
					t.Errorf("%s: the envelope reads its revision inside the record call near line %d; "+
						"it must be captured before the question is asked", file, i+1)
				}
			}
			// The multi-line form: the record spans lines, so check the
			// argument that follows it too.
			if j := strings.Index(window, "preflightRecord("); j >= 0 {
				call := window[j:]
				if end := strings.Index(call, "))"); end > 0 {
					if strings.Contains(call[:end], "repositoryHead(") {
						t.Errorf("%s: the envelope reads its revision inside the record call near line %d; "+
							"it must be captured before the question is asked", file, i+1)
					}
				}
			}
		}
	}
}

// SPDX-License-Identifier: AGPL-3.0-only

package workflow

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/evidence"
)

// THE AUDIT RECORD CARRIES THE QUESTION THAT PRODUCED IT.
//
// awareness_audit_diff is the record that decides admission: a reviewer may not
// accept over a failing audit. It emitted its verdict six times carrying no
// request at all, which is sensei-code#134 sitting on the acceptance record
// instead of on an escalation.
func TestTheAuditRecordCarriesItsRequest(t *testing.T) {
	e := &Engine{}
	args := map[string]any{
		"diff":          "--- a/x.go\n+++ b/x.go\n",
		"task":          "t",
		"domain":        "github.com/globulario/sensei-code",
		"expected_head": "abc123",
	}
	rec := e.auditRecord(args, map[string]any{"decision": "pass"}, "abc123", "graph999")

	if rec["decision"] != "pass" {
		t.Fatalf("the result no longer reaches existing readers at the top level: %+v", rec)
	}
	env, ok := rec["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("no evidence envelope on the acceptance record (got %T)", rec["evidence"])
	}
	if env["operation"] != "awareness_audit_diff" {
		t.Errorf("operation=%v", env["operation"])
	}
	req, ok := env["request"].(map[string]any)
	if !ok {
		t.Fatalf("the request did not survive: a preserved verdict is not a preserved experiment")
	}
	// expected_head is a CORRECTNESS input, not a detail: omitting it and
	// pinning the wrong one both yield cannot_verify through different doors,
	// so without it a preserved refusal cannot say which happened.
	if req["expected_head"] != "abc123" {
		t.Errorf("expected_head absent from the preserved request: a cannot_verify "+
			"record cannot distinguish an unverifiable candidate from a wrongly asked question: %+v", req)
	}
	if req["diff"] == nil || req["diff"] == "" {
		t.Errorf("the diff -- the question itself -- did not survive: %+v", req)
	}
	if env["revision"] != "abc123" || env["graph_digest"] != "graph999" {
		t.Errorf("the world the verdict was true in did not survive: %+v", env)
	}
}

// A CALL THAT COULD NOT BE AUDITED STILL PRESERVES WHAT WAS ASKED.
//
// This is the path where the request matters most: "Sensei could not audit
// this" is the one verdict that says nothing about the candidate, so without the
// request there is nothing left to tell a refused candidate from a malformed
// question. It emitted nil.
func TestAnUnobtainedVerdictStillPreservesTheQuestion(t *testing.T) {
	e := &Engine{}
	args := map[string]any{"diff": "d", "task": "t", "expected_head": "abc123"}
	rec := e.auditRecord(args, nil, "abc123", "graph999")
	env, ok := rec["evidence"].(map[string]any)
	if !ok {
		t.Fatalf("no envelope on an unobtained verdict: %+v", rec)
	}
	req, _ := env["request"].(map[string]any)
	if len(req) == 0 {
		t.Fatal("a call that produced no verdict preserved no question either: " +
			"the record establishes nothing at all")
	}
}

// governedCallSites classifies every governed tool call in this repository.
//
// `record` means the result reaches a durable event and MUST carry an
// evidence.Envelope. `context` means the result feeds a prompt and is not a
// preserved experiment; the reason is stated so the exclusion is a decision
// rather than an omission.
var governedCallSites = map[string]string{
	"internal/workflow/engine.go:awareness_audit_diff":  "record",
	"internal/workflow/engine.go:awareness_preflight":   "record",
	"internal/workflow/assisted.go:awareness_preflight": "record",

	"internal/workflow/routine.go:awareness_edit_check":  "context: advisory warning to the agent, not emitted",
	"internal/architect/run.go:awareness_briefing":       "context: feeds the architect prompt",
	"internal/architect/run.go:awareness_metadata":       "context: feeds the architect prompt",
	"internal/architect/run.go:awareness_preflight":      "context: feeds the architect prompt",
	"internal/architect/run.go:awareness_propose":        "context: a write, whose receipt is the proposal itself",
	"internal/architect/run.go:awareness_resolve":        "context: feeds the architect prompt",
	"internal/assist/packet.go:awareness_preflight":      "context: composed into the assisted packet handed to a worker",
	"internal/authority/resolution.go:awareness_propose": "context: a write, whose receipt is the proposal itself",
	"internal/doctor/taskbinding.go:awareness_metadata":  "context: a diagnostic read",
	"cmd/sensei-code/routine.go:awareness_edit_check":    "context: advisory warning to the agent, not emitted",
	"cmd/sensei-code/routine.go:awareness_preflight":     "context: advisory warning to the agent, not emitted",
}

// THE PACKAGE COMMENT'S GUARANTEE, MADE MECHANICAL.
//
// internal/evidence says it is "the one way to record such a call, so a new site
// cannot quietly omit the half that makes a record replayable". Nothing enforced
// that: awareness_audit_diff omitted it on six emits while the sentence stood.
// A guarantee that lives in a doc comment is a convention.
//
// This is the same repair as sensei#335: enumerate the consumption surface
// structurally, require both directions, and refuse to pass on a scan that
// matched nothing.
func TestEveryGovernedCallSiteIsClassified(t *testing.T) {
	root := repoRootForCoverage(t)
	call := regexp.MustCompile(`CallTool\("(awareness_[a-z_]+)"`)

	found := map[string]bool{}
	for _, dir := range []string{"internal", "cmd"} {
		err := filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return err
			}
			b, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			rel, _ := filepath.Rel(root, p)
			for _, m := range call.FindAllStringSubmatch(string(b), -1) {
				found[filepath.ToSlash(rel)+":"+m[1]] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}

	// REFUSE TO PASS VACUOUSLY. A scan matching nothing -- a renamed helper, a
	// broken pattern -- reports success exactly like full classification.
	if len(found) == 0 {
		t.Fatal("no governed call site found: the scan matched nothing, which is a " +
			"broken check and not a classified repository")
	}

	for site := range found {
		if _, ok := governedCallSites[site]; !ok {
			t.Errorf("%s is an unclassified governed call. If its result reaches a "+
				"durable event it must carry an evidence.Envelope and be marked "+
				"\"record\"; if it only feeds a prompt, mark it \"context: <why>\".", site)
		}
	}
	for site := range governedCallSites {
		if !found[site] {
			t.Errorf("governedCallSites names %s, which no longer exists: the "+
				"classification describes a call that is gone", site)
		}
	}

	// Every site marked `record` must actually reach a recorder.
	for site, class := range governedCallSites {
		if class != "record" {
			continue
		}
		file := strings.SplitN(site, ":", 2)[0]
		b, err := os.ReadFile(filepath.Join(root, file))
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if !strings.Contains(string(b), "Record(") && !strings.Contains(string(b), "auditEvidence(") {
			t.Errorf("%s is marked \"record\" but routes nothing through internal/evidence", site)
		}
	}
}

// repoRootForCoverage walks up to the module root so the scan covers the whole
// repository rather than this package.
func repoRootForCoverage(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("cwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Fatal("module root not found: the scan would cover an unknown population")
	return ""
}

var _ = evidence.Envelope{}

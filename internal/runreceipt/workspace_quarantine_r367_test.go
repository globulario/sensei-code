package runreceipt

// RULING-367 §13-§14 (N2). The v14 workspace_quarantine observation carries
// an optional closed disclosure of refused material pending at the quarantine
// terminal: bound to the observation's task, explicitly PENDING, its
// governing refusal stated with its source or UNKNOWN. A disclosure that
// contradicts itself, or whose governing refusal is not established, is not
// a complete statement; its absence asserts nothing.

import (
	"encoding/json"
	"strings"
	"testing"
)

func pendingDisclosure() map[string]any {
	return map[string]any{
		"task_id":     "task-1",
		"refusal_id":  map[string]any{"text": strings.Repeat("f", 64), "state": "KNOWN", "source": "the engine's pending refused material"},
		"workspace":   map[string]any{"text": "/repo/.worktrees/task-1", "state": "KNOWN", "source": "the refused material's candidate identity"},
		"disposition": "PENDING",
		"reason":      "its ordinary disposition was suspended by the containment boundary and remains owed",
	}
}

func withDisclosure(p map[string]any) map[string]any {
	wq := verifiedQuarantine()
	wq["pending_refused_material"] = p
	return wq
}

func TestR367N2APendingDisclosureIsReadAndCompletes(t *testing.T) {
	r := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, withDisclosure(pendingDisclosure()))
	if raw, err := json.Marshal(r); err != nil || !strings.Contains(string(raw), `"pending_refused_material"`) {
		t.Fatalf("the receipt does not read the pending disclosure: %s %v", raw, err)
	}
	if state, missing := r.Completeness(); state != Complete {
		t.Fatalf("a consistent disclosure made the receipt incomplete: %v", missing)
	}
	if text := r.WorkspaceQuarantine.value().Text; !strings.Contains(text, strings.Repeat("f", 64)) {
		t.Fatalf("the rendered observation does not name the owed disposition: %q", text)
	}
}

func TestR367N2AContradictoryOrUnknownDisclosureIsIncomplete(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"another task": func(m map[string]any) { m["task_id"] = "task-2" },
		"resolved":     func(m map[string]any) { m["disposition"] = "RETAINED" },
		"no reason":    func(m map[string]any) { m["reason"] = " " },
		"unknown refusal": func(m map[string]any) {
			m["refusal_id"] = map[string]any{"state": "UNKNOWN", "detail": "not established"}
		},
		"sourceless refusal": func(m map[string]any) { m["refusal_id"] = map[string]any{"text": "f", "state": "KNOWN"} },
		"invalid workspace": func(m map[string]any) {
			m["workspace"] = map[string]any{"text": "/x", "state": "CERTAIN", "source": "s"}
		},
	} {
		p := pendingDisclosure()
		edit(p)
		r := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, withDisclosure(p))
		if state, missing := r.Completeness(); state != Incomplete || !mentions(missing, "pending_refused_material") {
			t.Errorf("%s: the receipt is %s and does not name the disclosure: %v", name, state, missing)
		}
	}
}

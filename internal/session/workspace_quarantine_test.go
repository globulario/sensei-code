package session

// N5 P2-A (RULING-366 §5, §12; §30 W7, mutant M11). The workspace-quarantine
// terminal is system-owned and carries a closed, discriminated payload: a
// verified observation of the durable candidate record, or an unverified one
// that names the read failure and nothing measured.

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

const workspaceQuarantinedKind = event.Kind("workflow.workspace_quarantined")

func quarantineEvent(t *testing.T, source event.Source, payload map[string]any) event.Event {
	t.Helper()
	ev := event.New("A", lineageTask, source, workspaceQuarantinedKind,
		"the invocation ended while workspace containment of the task is not established", nil)
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		ev.Payload = raw
	}
	return ev
}

func verifiedQuarantinePayload() map[string]any {
	return map[string]any{"task_id": lineageTask, "recorded": true, "invocation": "exec-1", "phase": "certify",
		"workspace": "/repo/.worktrees/t", "baseline": "/repo/.worktrees/t-baseline", "reason": "no established teardown"}
}

func unverifiedQuarantinePayload() map[string]any {
	return map[string]any{"task_id": lineageTask, "recorded": false, "reason": "the candidate record could not be read"}
}

// W7. Both variants are valid as the system records them; every extra,
// missing, mistyped or contradictory member is refused, and so is any source
// but the system.
func TestW7TheWorkspaceQuarantinePayloadIsClosedAndSystemOwned(t *testing.T) {
	for name, p := range map[string]map[string]any{
		"verified":              verifiedQuarantinePayload(),
		"verified, no baseline": func() map[string]any { m := verifiedQuarantinePayload(); delete(m, "baseline"); return m }(),
		"unverified":            unverifiedQuarantinePayload(),
	} {
		if err := ValidGovernedEvent(quarantineEvent(t, event.SourceSystem, p)); err != nil {
			t.Fatalf("the %s quarantine terminal is not a valid governed record: %v", name, err)
		}
	}
	type mutant struct {
		name    string
		source  event.Source
		payload map[string]any
	}
	edit := func(base map[string]any, f func(map[string]any)) map[string]any {
		out := map[string]any{}
		for k, v := range base {
			out[k] = v
		}
		f(out)
		return out
	}
	verified, unverified := verifiedQuarantinePayload(), unverifiedQuarantinePayload()
	mutants := []mutant{
		{"no payload", event.SourceSystem, nil},
		{"recorded by the user", event.SourceUser, verified},
		{"recorded by the architect", event.SourceArchitect, verified},
		{"recorded by Git", event.SourceGit, verified},
		{"recorded by Claude", event.SourceClaude, unverified},
		{"an undeclared member", event.SourceSystem, edit(verified, func(m map[string]any) { m["settled"] = true })},
		{"another task", event.SourceSystem, edit(verified, func(m map[string]any) { m["task_id"] = "task-other" })},
		{"no task", event.SourceSystem, edit(verified, func(m map[string]any) { delete(m, "task_id") })},
		{"no recorded state", event.SourceSystem, edit(verified, func(m map[string]any) { delete(m, "recorded") })},
		{"a recorded state that is not one", event.SourceSystem, edit(verified, func(m map[string]any) { m["recorded"] = "yes" })},
		{"no reason", event.SourceSystem, edit(verified, func(m map[string]any) { m["reason"] = "" })},
		{"a verified record with no workspace", event.SourceSystem, edit(verified, func(m map[string]any) { delete(m, "workspace") })},
		{"an empty invocation", event.SourceSystem, edit(verified, func(m map[string]any) { m["invocation"] = "" })},
		{"an unverified record naming a workspace", event.SourceSystem, edit(unverified, func(m map[string]any) { m["workspace"] = "/repo" })},
		{"an unverified record naming an invocation", event.SourceSystem, edit(unverified, func(m map[string]any) { m["invocation"] = "exec-1" })},
		{"an unverified record with an empty baseline", event.SourceSystem, edit(unverified, func(m map[string]any) { m["baseline"] = "" })},
		{"no summary", event.SourceSystem, nil},
	}
	for i, m := range mutants {
		var ev event.Event
		if m.name == "no summary" {
			ev = quarantineEvent(t, m.source, verified)
			ev.Summary = ""
		} else {
			ev = quarantineEvent(t, m.source, m.payload)
		}
		if err := ValidGovernedEvent(ev); !errors.Is(err, ErrMalformedEvent) {
			t.Errorf("mutant %d (%s) was accepted: %v", i, m.name, err)
		}
	}
}

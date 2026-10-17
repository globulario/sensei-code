package session

// RULING-367 §11-§15 (N2). Refused material pending at a quarantine terminal
// is disclosed in the terminal's closed payload as one closed, optional,
// system-produced member: bound to the task and its governing refusal,
// explicitly PENDING, absent when nothing is pending, and in both the verified
// and the unverified variant.

import (
	"errors"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

func pendingMaterial() map[string]any {
	return map[string]any{"task_id": lineageTask, "refusal_id": "ffff", "workspace": "/repo/.worktrees/t",
		"disposition": "PENDING", "reason": "its ordinary disposition was suspended by the containment boundary and remains owed"}
}

func withPending(base map[string]any, pending map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	out["pending_refused_material"] = pending
	return out
}

func TestR367N2ThePendingMaterialMemberIsClosedAndBound(t *testing.T) {
	unknownRefusal := pendingMaterial()
	delete(unknownRefusal, "refusal_id")
	delete(unknownRefusal, "workspace")
	for name, p := range map[string]map[string]any{
		"verified":                   withPending(verifiedQuarantinePayload(), pendingMaterial()),
		"unverified":                 withPending(unverifiedQuarantinePayload(), pendingMaterial()),
		"refusal identity not known": withPending(verifiedQuarantinePayload(), unknownRefusal),
	} {
		if err := ValidGovernedEvent(quarantineEvent(t, event.SourceSystem, p)); err != nil {
			t.Fatalf("the %s quarantine terminal disclosing pending material is not valid: %v", name, err)
		}
	}
	edit := func(f func(map[string]any)) map[string]any {
		m := pendingMaterial()
		f(m)
		return withPending(verifiedQuarantinePayload(), m)
	}
	for name, p := range map[string]map[string]any{
		"another task":          edit(func(m map[string]any) { m["task_id"] = "task-other" }),
		"no task":               edit(func(m map[string]any) { delete(m, "task_id") }),
		"resolved, not pending": edit(func(m map[string]any) { m["disposition"] = "RETAINED" }),
		"no disposition":        edit(func(m map[string]any) { delete(m, "disposition") }),
		"no reason":             edit(func(m map[string]any) { m["reason"] = "" }),
		"an undeclared member":  edit(func(m map[string]any) { m["settled"] = true }),
		"an empty refusal":      edit(func(m map[string]any) { m["refusal_id"] = "" }),
		"not an object":         withPending(verifiedQuarantinePayload(), nil),
	} {
		if name == "not an object" {
			p["pending_refused_material"] = "pending"
		}
		if err := ValidGovernedEvent(quarantineEvent(t, event.SourceSystem, p)); !errors.Is(err, ErrMalformedEvent) {
			t.Errorf("%s was accepted: %v", name, err)
		}
	}
	if err := ValidGovernedEvent(quarantineEvent(t, event.SourceUser, withPending(verifiedQuarantinePayload(), pendingMaterial()))); !errors.Is(err, ErrMalformedEvent) {
		t.Errorf("a user-sourced disclosure was accepted: %v", err)
	}
}

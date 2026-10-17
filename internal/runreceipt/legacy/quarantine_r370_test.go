package legacy

// RULING-370 finding B (§13-§21). The adapter may be incomplete, but it never
// presents out-of-schema input as a verified observation. The quarantine
// terminal's payload is closed: the verified variant (recorded=true) admits
// exactly the members internal/session/lineage.go's workspaceQuarantinedShape
// declares, the unverified variant (recorded=false) exactly those of
// workspaceUnverifiedShape, and a pending_refused_material disclosure exactly
// those of pendingRefusedMaterialShape. An undeclared member -- null or empty
// included -- keeps the terminal and the WORKSPACE_QUARANTINED outcome and
// makes the observation MALFORMED, naming the member, with a diagnostic. A
// pending disclosure that states no disposition is not given PENDING. B1-B4
// are the controls (efa1538 accepts or supplies each); B5-B14 the witnesses.

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

const r370Refusal = "3333333333333333333333333333333333333333333333333333333333333333"

// r370Pending is a full pending disclosure with extra raw members appended,
// and the disposition left out when noDisposition.
func r370Pending(noDisposition bool, extra string) string {
	body := `"task_id":"task-q","refusal_id":"` + r370Refusal + `","workspace":"/ws/task-q","reason":"refused for production scope; owed"`
	if !noDisposition {
		body += `,"disposition":"PENDING"`
	}
	if extra != "" {
		body += "," + extra
	}
	return "{" + body + "}"
}

func r370Diagnosed(rec runreceipt.Receipt, member string) bool {
	for _, d := range rec.Diagnostics {
		if strings.Contains(d, quarantinedKind) && strings.Contains(d, member) {
			return true
		}
	}
	return false
}

// r370MalformedNaming asserts the closed-shape contract of an undeclared
// member: the terminal and the outcome stand, the candidate is UNKNOWN, the
// observation is MALFORMED naming the member with a diagnostic, nothing is
// verified, and the record is never COMPLETE.
func r370MalformedNaming(t *testing.T, name string, rec runreceipt.Receipt, member string) *runreceipt.WorkspaceQuarantine {
	t.Helper()
	if rec.Terminal.State != runreceipt.Known || rec.Terminal.Text != quarantinedKind {
		t.Errorf("%s: terminal = %+v, want KNOWN %s", name, rec.Terminal, quarantinedKind)
	}
	if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined || OutcomeFrom(rec) != runreceipt.OutcomeWorkspaceQuarantined {
		t.Errorf("%s: outcome = %s / %s, want WORKSPACE_QUARANTINED", name, rec.Outcome, OutcomeFrom(rec))
	}
	if rec.CandidateState != runreceipt.CandidateUnknown {
		t.Errorf("%s: candidate_state = %s, want UNKNOWN", name, rec.CandidateState)
	}
	q := rec.WorkspaceQuarantine
	if q == nil {
		t.Fatalf("%s: workspace_quarantine absent", name)
	}
	if q.State != runreceipt.Malformed {
		t.Errorf("%s: observation state = %s (%q), want MALFORMED", name, q.State, q.Detail)
	}
	if !strings.Contains(q.Detail, member) {
		t.Errorf("%s: the observation does not name the undeclared member %q: %q", name, member, q.Detail)
	}
	if !r370Diagnosed(rec, member) {
		t.Errorf("%s: no diagnostic names the undeclared member %q: %v", name, member, rec.Diagnostics)
	}
	if q.Source != "" {
		t.Errorf("%s: a malformed observation keeps a verified source %q", name, q.Source)
	}
	if state, _ := rec.Completeness(); state != runreceipt.Incomplete {
		t.Errorf("%s: an out-of-schema observation must leave the record INCOMPLETE", name)
	}
	return q
}

// B1 B5. A verified payload carrying a member the verified variant does not
// declare is MALFORMED -- whatever its value, null and empty included.
func TestR370B1B5AnUndeclaredVerifiedMemberIsMalformed(t *testing.T) {
	for name, raw := range map[string]string{"string": `"x"`, "null": `null`, "empty": `""`, "object": `{}`} {
		rec := stream(quarantineEvent(verifiedPayload(map[string]string{"settled": raw})))
		r370MalformedNaming(t, "verified + settled "+name, rec, "settled")
	}
}

// B2 B6. An unverified payload admits only its own members: a verified-only
// member, or one neither variant declares, is MALFORMED, and nothing it
// carries is promoted to a measurement.
func TestR370B2B6AnUndeclaredUnverifiedMemberIsMalformed(t *testing.T) {
	for _, member := range []string{"invocation", "phase", "workspace", "baseline", "settled"} {
		p := unverifiedPayload()
		p[member] = `"/ws/x"`
		rec := stream(quarantineEvent(p))
		q := r370MalformedNaming(t, "unverified + "+member, rec, member)
		if q.Recorded {
			t.Errorf("unverified + %s: recorded promoted", member)
		}
		for m, v := range map[string]runreceipt.Value{"invocation": q.Invocation, "phase": q.Phase, "workspace": q.Workspace, "baseline": q.Baseline} {
			if v.State == runreceipt.Known {
				t.Errorf("unverified + %s: %s = %+v measured", member, m, v)
			}
		}
	}
}

// B3 B7. A pending disclosure carrying a member its closed shape does not
// declare makes the observation MALFORMED naming it; the disclosure keeps its
// task, refusal, workspace, PENDING and reason, and nothing is settled.
func TestR370B3B7AnUndeclaredPendingMemberIsMalformed(t *testing.T) {
	for name, payload := range map[string]map[string]string{
		"verified": verifiedPayload(map[string]string{"pending_refused_material": r370Pending(false, `"released":true`)}),
		"unverified": func() map[string]string {
			p := unverifiedPayload()
			p["pending_refused_material"] = r370Pending(false, `"released":null`)
			return p
		}(),
	} {
		rec := stream(quarantineEvent(payload))
		q := r370MalformedNaming(t, name+" pending + released", rec, "released")
		p := q.PendingRefusedMaterial
		if p == nil {
			t.Fatalf("%s: the pending disclosure was dropped", name)
		}
		if p.TaskID != "task-q" || p.RefusalID.Text != r370Refusal || p.Workspace.Text != "/ws/task-q" ||
			p.Disposition != runreceipt.PendingDisposition || !strings.Contains(p.Reason, "owed") {
			t.Errorf("%s: the disclosure lost a stated member: %+v", name, p)
		}
	}
}

// B4 B8. A pending disclosure that states no disposition is not given one:
// PENDING is not synthesized, the absence is diagnosed, and v14 rejects it.
func TestR370B4B8AMissingPendingDispositionIsNotSynthesized(t *testing.T) {
	for name, raw := range map[string]string{"absent": r370Pending(true, ""), "null": r370Pending(true, `"disposition":null`)} {
		rec := stream(quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": raw})))
		p := quarantineOf(t, rec).PendingRefusedMaterial
		if p == nil {
			t.Fatalf("%s: the pending disclosure was dropped", name)
		}
		if p.Disposition == runreceipt.PendingDisposition {
			t.Errorf("%s: PENDING was synthesized for a disclosure that states no disposition", name)
		}
		if !r370Diagnosed(rec, "disposition") {
			t.Errorf("%s: the missing disposition is not diagnosed: %v", name, rec.Diagnostics)
		}
		if len(issuesWith(rec, "workspace_quarantine.pending_refused_material.disposition")) == 0 {
			t.Errorf("%s: v14 does not reject a disclosure with no disposition", name)
		}
		if p.TaskID != "task-q" || p.RefusalID.Text != r370Refusal {
			t.Errorf("%s: the disclosure lost a stated member: %+v", name, p)
		}
	}
}

// B9. The exact verified shape stays a valid verified observation.
func TestR370B9ACorrectVerifiedPayloadStaysValid(t *testing.T) {
	for name, p := range map[string]map[string]string{
		"without pending": verifiedPayload(nil),
		"with pending":    verifiedPayload(map[string]string{"pending_refused_material": r370Pending(false, "")}),
	} {
		rec := stream(quarantineEvent(p))
		q := quarantineOf(t, rec)
		if q.State != runreceipt.Known || !q.Recorded || q.Workspace.Text != "/ws/task-q" || q.Invocation.Text != "inv-7" {
			t.Errorf("%s: observation = %+v, want the verified observation", name, q)
		}
		if issues := issuesWith(rec, "workspace_quarantine"); len(issues) > 0 {
			t.Errorf("%s: a correct verified payload is diagnosed: %v", name, issues)
		}
		if len(rec.Diagnostics) > 0 {
			t.Errorf("%s: a correct verified payload produced diagnostics: %v", name, rec.Diagnostics)
		}
	}
}

// B10. The exact unverified shape stays UNKNOWN (unmeasured), not MALFORMED.
func TestR370B10ACorrectUnverifiedPayloadStaysUnknown(t *testing.T) {
	p := unverifiedPayload()
	p["pending_refused_material"] = r370Pending(false, "")
	for name, payload := range map[string]map[string]string{"plain": unverifiedPayload(), "with pending": p} {
		rec := stream(quarantineEvent(payload))
		q := quarantineOf(t, rec)
		if q.State != runreceipt.Unknown || q.Recorded || !strings.Contains(q.Detail, "permission denied") {
			t.Errorf("%s: observation = %+v, want UNKNOWN carrying the read failure", name, q)
		}
		if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
			t.Errorf("%s: outcome = %s", name, rec.Outcome)
		}
	}
}

// B11. A stated PENDING stays PENDING; a stated alternative is kept as
// stated, so v14 rejects it rather than the adapter rewriting it.
func TestR370B11AStatedDispositionIsKeptAsStated(t *testing.T) {
	rec := stream(quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": r370Pending(false, "")})))
	if p := quarantineOf(t, rec).PendingRefusedMaterial; p == nil || p.Disposition != runreceipt.PendingDisposition {
		t.Fatalf("a stated PENDING = %+v, want PENDING", p)
	}
	other := strings.Replace(r370Pending(false, ""), `"PENDING"`, `"RELEASED"`, 1)
	rec = stream(quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": other})))
	if p := quarantineOf(t, rec).PendingRefusedMaterial; p == nil || p.Disposition != "RELEASED" {
		t.Fatalf("a stated RELEASED = %+v, want it kept as stated", p)
	}
	if len(issuesWith(rec, "workspace_quarantine.pending_refused_material.disposition")) == 0 {
		t.Error("a stated disposition other than PENDING is not rejected")
	}
}

// B12 B14. No undeclared member, at any level, in any variant, panics the
// extraction or yields anything but a total, INCOMPLETE record.
func TestR370B12B14UndeclaredMembersNeverPanic(t *testing.T) {
	values := []string{`null`, `""`, `0`, `true`, `[]`, `{}`, `{"x":[null]}`}
	var lines []string
	for _, v := range values {
		lines = append(lines,
			quarantineEvent(verifiedPayload(map[string]string{"zz": v})),
			quarantineEvent(func() map[string]string { p := unverifiedPayload(); p["zz"] = v; return p }()),
			quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": r370Pending(false, `"zz":`+v)})),
			quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": r370Pending(true, `"zz":`+v)})),
			quarantineEvent(verifiedPayload(map[string]string{"recorded": v, "zz": v})))
	}
	for _, line := range lines {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("extraction panicked on %s: %v", line, r)
				}
			}()
			rec := stream(line)
			if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
				t.Errorf("%s: outcome = %s", line, rec.Outcome)
			}
			if state, _ := rec.Completeness(); state != runreceipt.Incomplete {
				t.Errorf("%s: record is not INCOMPLETE", line)
			}
		}()
	}
}

// B13. An earlier verdict never overrides a quarantine terminal whose payload
// is out of schema.
func TestR370B13AnEarlierVerdictNeverOverridesAMalformedQuarantine(t *testing.T) {
	for name, verdict := range map[string]string{"accept": acceptVerdict, "revise": reviseVerdict} {
		rec := stream(verdict, quarantineEvent(verifiedPayload(map[string]string{"settled": `true`})))
		r370MalformedNaming(t, name+" then malformed quarantine", rec, "settled")
	}
}

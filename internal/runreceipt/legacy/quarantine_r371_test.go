package legacy

// RULING-371 Q2 (§16-§18). A pending_refused_material member that is present
// but not a JSON object -- a string, an array, a number, a boolean or an
// explicit null -- is not a valid disclosure. The terminal and its
// WORKSPACE_QUARANTINED outcome are kept, the candidate stays UNKNOWN, the
// observation is MALFORMED with a diagnostic naming the member, and no
// disposition is inferred for it. A valid object is unchanged; an absent one
// stays absent. D1-D3 are the controls (a7b41bd accepts each); D4-D8 the
// witnesses. D8's corpus and totality suite is fromevents_test.go.

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

var r371Malformed = map[string]string{
	"D1 string": `"x"`,
	"D2 array":  `[{"task_id":"task-q","disposition":"PENDING"}]`,
	"D3 null":   `null`,
	"number":    `5`,
	"boolean":   `true`,
}

// D1, D2, D3 (controls) and D6, D7, over both observation variants.
func TestR371D1D3AMalformedPendingDisclosureIsRejected(t *testing.T) {
	for name, raw := range r371Malformed {
		for variant, payload := range map[string]map[string]string{
			"verified":   verifiedPayload(map[string]string{"pending_refused_material": raw}),
			"unverified": func() map[string]string { p := unverifiedPayload(); p["pending_refused_material"] = raw; return p }(),
		} {
			t.Run(name+"/"+variant, func(t *testing.T) {
				rec := stream(quarantineEvent(payload))
				q := quarantineOf(t, rec)
				if q.State != runreceipt.Malformed || !strings.Contains(q.Detail, "pending_refused_material") {
					t.Errorf("observation = %s (%q), want MALFORMED naming pending_refused_material", q.State, q.Detail)
				}
				if !r370Diagnosed(rec, "pending_refused_material") {
					t.Errorf("no diagnostic names pending_refused_material: %v", rec.Diagnostics)
				}
				if p := q.PendingRefusedMaterial; p != nil && p.Disposition != "" {
					t.Errorf("a disposition %q was inferred for a malformed disclosure", p.Disposition)
				}
				// D6
				if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined || rec.CandidateState != runreceipt.CandidateUnknown {
					t.Errorf("outcome %s / %s, want WORKSPACE_QUARANTINED / UNKNOWN", rec.Outcome, rec.CandidateState)
				}
				// D7
				if state, _ := rec.Completeness(); state != runreceipt.Incomplete {
					t.Errorf("a malformed disclosure left the receipt %s", state)
				}
			})
		}
	}
}

// D4. A valid disclosure is unchanged: KNOWN, PENDING, its refusal carried.
func TestR371D4AValidDisclosureKeepsPending(t *testing.T) {
	rec := stream(quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": r370Pending(false, "")})))
	q := quarantineOf(t, rec)
	p := q.PendingRefusedMaterial
	if q.State != runreceipt.Known || p == nil || p.Disposition != runreceipt.PendingDisposition ||
		p.RefusalID.State != runreceipt.Known || p.RefusalID.Text != r370Refusal {
		t.Fatalf("a valid disclosure changed: %s %+v", q.State, p)
	}
	if issues := issuesWith(rec, "workspace_quarantine"); len(issues) > 0 {
		t.Errorf("a valid disclosure is diagnosed: %v", issues)
	}
}

// D5. An absent disclosure invents nothing and does not make the observation
// MALFORMED.
func TestR371D5AnAbsentDisclosureInventsNothing(t *testing.T) {
	rec := stream(quarantineEvent(verifiedPayload(nil)))
	q := quarantineOf(t, rec)
	if q.State != runreceipt.Known || q.PendingRefusedMaterial != nil {
		t.Fatalf("an absent disclosure: %s %+v", q.State, q.PendingRefusedMaterial)
	}
	for _, d := range rec.Diagnostics {
		if strings.Contains(d, "pending_refused_material") {
			t.Errorf("an absent disclosure is diagnosed: %q", d)
		}
	}
}

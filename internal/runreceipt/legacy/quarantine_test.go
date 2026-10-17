package legacy

import (
	"sort"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

// RULING-369: a historical stream that ends an invocation on
// workflow.workspace_quarantined is reconstructed as that ending. L1-L5 are the
// pre-fix controls; L6-L16 the corrected witnesses. L17 is the preserved
// real-log corpus and totality suite in fromevents_test.go, and L18
// TestTheGovernedLoopNeverReachesThisAdapter in boundary_test.go.

const quarantinedKind = "workflow.workspace_quarantined"

// quarantineEvent is a workflow.workspace_quarantined terminal whose payload
// members are the given raw JSON values; a nil map gives a null payload.
func quarantineEvent(members map[string]string) string {
	if members == nil {
		return `{"kind":"` + quarantinedKind + `","payload":null}`
	}
	keys := make([]string, 0, len(members))
	for k := range members {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, `"`+k+`":`+members[k])
	}
	return `{"kind":"` + quarantinedKind + `","payload":{` + strings.Join(parts, ",") + `}}`
}

// verifiedPayload is the payload shape the engine writes for a VERIFIED
// quarantine (workflow.WorkspaceQuarantine, Recorded true), with set replacing
// members and the named members left out.
func verifiedPayload(set map[string]string, omit ...string) map[string]string {
	m := map[string]string{
		"task_id":    `"task-q"`,
		"recorded":   `true`,
		"invocation": `"inv-7"`,
		"phase":      `"execute"`,
		"workspace":  `"/ws/task-q"`,
		"baseline":   `"/ws/task-q.baseline"`,
		"reason":     `"the candidate record names an execution whose teardown was never established"`,
	}
	for k, v := range set {
		m[k] = v
	}
	for _, k := range omit {
		delete(m, k)
	}
	return m
}

// unverifiedPayload is the UNVERIFIED shape: the task and the read failure.
func unverifiedPayload() map[string]string {
	return map[string]string{
		"task_id":  `"task-q"`,
		"recorded": `false`,
		"reason":   `"the candidate record of task-q could not be read under its lock: permission denied"`,
	}
}

const (
	acceptVerdict = `{"kind":"review.completed","payload":{"decision":"accept","provenance":{"candidate_digest":"` +
		"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" + `","provider":"p"}}}`
	reviseVerdict = `{"kind":"review.completed","payload":{"decision":"revise","provenance":{"candidate_digest":"` +
		"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd" + `","provider":"p"}}}`
)

func stream(lines ...string) runreceipt.Receipt {
	return FromEvents(strings.NewReader(strings.Join(lines, "\n")))
}

func issuesWith(rec runreceipt.Receipt, prefix string) []string {
	_, missing := rec.Completeness()
	var out []string
	for _, m := range missing {
		if strings.HasPrefix(m, prefix) {
			out = append(out, m)
		}
	}
	return out
}

// quarantineOf is rec's workspace_quarantine, failing the test when absent.
func quarantineOf(t *testing.T, rec runreceipt.Receipt) *runreceipt.WorkspaceQuarantine {
	t.Helper()
	if rec.WorkspaceQuarantine == nil {
		t.Fatalf("workspace_quarantine is absent (outcome %s, terminal %+v)", rec.Outcome, rec.Terminal)
	}
	return rec.WorkspaceQuarantine
}

// ---- L1-L5: pre-fix controls -------------------------------------------

// TestL1AQuarantineTerminalIsModelled: a stream ending on the quarantine
// terminal states that terminal.
func TestL1AQuarantineTerminalIsModelled(t *testing.T) {
	rec := stream(quarantineEvent(verifiedPayload(nil)))
	if rec.Terminal.State != runreceipt.Known || rec.Terminal.Text != quarantinedKind {
		t.Errorf("terminal = %+v, want KNOWN %s", rec.Terminal, quarantinedKind)
	}
	for _, d := range rec.Diagnostics {
		if strings.Contains(d, quarantinedKind) && strings.Contains(d, "not modelled") {
			t.Errorf("the quarantine terminal is reported unmodelled: %q", d)
		}
	}
}

// TestL2AnEarlierAcceptDoesNotNameTheQuarantinedInvocation.
func TestL2AnEarlierAcceptDoesNotNameTheQuarantinedInvocation(t *testing.T) {
	rec := stream(acceptVerdict, quarantineEvent(verifiedPayload(nil)))
	if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
		t.Errorf("outcome = %s, want WORKSPACE_QUARANTINED over the earlier ACCEPT", rec.Outcome)
	}
}

// TestL3AnEarlierRefusalDoesNotNameTheQuarantinedInvocation.
func TestL3AnEarlierRefusalDoesNotNameTheQuarantinedInvocation(t *testing.T) {
	rec := stream(reviseVerdict, quarantineEvent(verifiedPayload(nil)))
	if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
		t.Errorf("outcome = %s, want WORKSPACE_QUARANTINED over the earlier REVISE", rec.Outcome)
	}
}

// TestL4APreviousCandidateIsNotTheQuarantinedMeasurement: neither an earlier
// invocation's settled PRESENT nor the quarantined invocation's own reviewer
// assignment becomes the measured candidate of the quarantined invocation.
func TestL4APreviousCandidateIsNotTheQuarantinedMeasurement(t *testing.T) {
	digest := strings.Repeat("f", 64)
	for name, rec := range map[string]runreceipt.Receipt{
		"earlier invocation settled PRESENT": stream(completePresent(), `{"kind":"workflow.failed","payload":{}}`,
			quarantineEvent(verifiedPayload(nil))),
		"same invocation assigned a candidate": stream(
			`{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"p","candidate":"`+digest+`"}}`,
			quarantineEvent(verifiedPayload(nil))),
	} {
		if rec.CandidateState != runreceipt.CandidateUnknown {
			t.Errorf("%s: candidate_state = %s, want UNKNOWN", name, rec.CandidateState)
		}
		for field, v := range candidateIdentities(rec) {
			if v.State == runreceipt.Known {
				t.Errorf("%s: %s = %+v is stated for a quarantined invocation", name, field, v)
			}
		}
	}
}

// TestL5AVerifiedPayloadIsReconstructedAsTheV14Fact.
func TestL5AVerifiedPayloadIsReconstructedAsTheV14Fact(t *testing.T) {
	rec := stream(quarantineEvent(verifiedPayload(nil)))
	q := rec.WorkspaceQuarantine
	if q == nil {
		t.Fatal("workspace_quarantine is absent; the verified terminal payload is not reconstructed")
	}
	if q.State != runreceipt.Known || !q.Recorded || q.TaskID != "task-q" || q.Workspace.Text != "/ws/task-q" {
		t.Errorf("workspace_quarantine = %+v, want the verified observation the payload carries", q)
	}
}

// ---- L6-L16: corrected witnesses ----------------------------------------

func TestL6TheOutcomeIsWorkspaceQuarantined(t *testing.T) {
	for name, p := range map[string]map[string]string{"verified": verifiedPayload(nil), "unverified": unverifiedPayload()} {
		rec := stream(`{"kind":"candidate.changed","payload":{"cycle":1}}`, quarantineEvent(p))
		if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
			t.Errorf("%s: outcome = %s, want WORKSPACE_QUARANTINED", name, rec.Outcome)
		}
		if got := OutcomeFrom(rec); got != runreceipt.OutcomeWorkspaceQuarantined {
			t.Errorf("%s: OutcomeFrom = %s, want WORKSPACE_QUARANTINED", name, got)
		}
	}
}

func TestL7TheTerminalIsKnownWithItsSource(t *testing.T) {
	rec := stream(quarantineEvent(verifiedPayload(nil)))
	if rec.Terminal.State != runreceipt.Known || rec.Terminal.Text != quarantinedKind || rec.Terminal.Source != "event:"+quarantinedKind {
		t.Errorf("terminal = %+v, want KNOWN %s from its event", rec.Terminal, quarantinedKind)
	}
	if issues := issuesWith(rec, "terminal"); len(issues) > 0 {
		t.Errorf("terminal issues %v", issues)
	}
}

func TestL8AnEarlierAcceptDoesNotOverrideTheQuarantine(t *testing.T) {
	rec := stream(acceptVerdict, `{"kind":"candidate.changed","payload":{"cycle":2}}`, acceptVerdict,
		quarantineEvent(verifiedPayload(nil)))
	if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
		t.Fatalf("outcome = %s, want WORKSPACE_QUARANTINED", rec.Outcome)
	}
	// The verdict stays the historical review fact; it is not the ending.
	if rec.ReviewVerdict.Text != "accept" {
		t.Errorf("review verdict = %+v, want the historical accept preserved", rec.ReviewVerdict)
	}
}

func TestL9AnEarlierRefusalDoesNotOverrideTheQuarantine(t *testing.T) {
	for _, verdict := range []string{"revise", "reject"} {
		rec := stream(strings.Replace(reviseVerdict, `"revise"`, `"`+verdict+`"`, 1), quarantineEvent(unverifiedPayload()))
		if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
			t.Errorf("%s: outcome = %s, want WORKSPACE_QUARANTINED", verdict, rec.Outcome)
		}
	}
}

func TestL10TheQuarantinedCandidateIsUnknown(t *testing.T) {
	digest := strings.Repeat("d", 64)
	rec := stream(
		`{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"p","candidate":"`+digest+`"}}`,
		acceptVerdict, completePresent(), `{"kind":"candidate.changed","payload":{}}`,
		quarantineEvent(verifiedPayload(nil)))
	if rec.CandidateState != runreceipt.CandidateUnknown {
		t.Errorf("candidate_state = %s, want UNKNOWN", rec.CandidateState)
	}
	for field, v := range candidateIdentities(rec) {
		if v.State != runreceipt.Unknown || v.Text != "" {
			t.Errorf("%s = %+v, want UNKNOWN with no identity", field, v)
		}
		if !strings.Contains(v.Detail, "WORKSPACE_QUARANTINED") {
			t.Errorf("%s detail %q must say why nothing is measured", field, v.Detail)
		}
	}
	if issues := issuesWith(rec, "candidate_state"); len(issues) > 0 {
		t.Errorf("a verified quarantine explains candidate_state UNKNOWN, got %v", issues)
	}
	_, missing := rec.Completeness()
	for _, m := range missing {
		if strings.Contains(m, "while the outcome is WORKSPACE_QUARANTINED") {
			t.Errorf("the quarantined candidate contradicts its outcome: %q", m)
		}
	}
}

func TestL11AVerifiedPayloadIsReconstructedWithoutStrengtheningAbsentFacts(t *testing.T) {
	rec := stream(quarantineEvent(verifiedPayload(nil, "baseline", "phase")))
	q := rec.WorkspaceQuarantine
	if q == nil {
		t.Fatal("workspace_quarantine absent")
	}
	if q.State != runreceipt.Known || !strings.Contains(q.Source, "event:"+quarantinedKind+".payload") || !q.Recorded {
		t.Errorf("observation = %+v, want KNOWN, recorded, attributed to the terminal payload", q)
	}
	if q.TaskID != "task-q" || !strings.Contains(q.Reason, "teardown was never established") {
		t.Errorf("task/reason = %q/%q, want the payload's", q.TaskID, q.Reason)
	}
	for name, want := range map[string]struct {
		v    runreceipt.Value
		text string
	}{"invocation": {q.Invocation, "inv-7"}, "workspace": {q.Workspace, "/ws/task-q"}} {
		if want.v.State != runreceipt.Known || want.v.Text != want.text || want.v.Source != "event:"+quarantinedKind+".payload."+name {
			t.Errorf("%s = %+v, want KNOWN %q from its payload member", name, want.v, want.text)
		}
	}
	for name, v := range map[string]runreceipt.Value{"phase": q.Phase, "baseline": q.Baseline} {
		if v.State != runreceipt.Unknown || v.Text != "" {
			t.Errorf("%s = %+v: an absent member must stay UNKNOWN", name, v)
		}
	}
	if issues := issuesWith(rec, "workspace_quarantine"); len(issues) > 0 {
		t.Errorf("the reconstructed fact must satisfy the v14 checks, got %v", issues)
	}
	if state, _ := rec.Completeness(); state != runreceipt.Incomplete {
		t.Error("recognizing the terminal must not make a reconstruction COMPLETE")
	}
}

func TestL12AnUnverifiedPayloadStaysUnverified(t *testing.T) {
	p := unverifiedPayload()
	// Members an unverified payload should not carry are not promoted.
	p["invocation"], p["workspace"] = `"inv-x"`, `"/ws/x"`
	for name, members := range map[string]map[string]string{"recorded=false": p, "recorded absent": verifiedPayload(nil, "recorded")} {
		rec := stream(quarantineEvent(members))
		q := rec.WorkspaceQuarantine
		if q == nil {
			t.Fatalf("%s: workspace_quarantine absent", name)
		}
		if q.State == runreceipt.Known || q.Recorded {
			t.Errorf("%s: observation = %+v, want unverified", name, q)
		}
		for m, v := range map[string]runreceipt.Value{"invocation": q.Invocation, "phase": q.Phase, "workspace": q.Workspace, "baseline": q.Baseline} {
			if v.State == runreceipt.Known {
				t.Errorf("%s: %s = %+v measured by an unverified observation", name, m, v)
			}
		}
		if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined || rec.CandidateState != runreceipt.CandidateUnknown {
			t.Errorf("%s: outcome %s candidate %s", name, rec.Outcome, rec.CandidateState)
		}
		if issues := issuesWith(rec, "candidate_state"); len(issues) == 0 {
			t.Errorf("%s: an unverified quarantine must not explain candidate_state UNKNOWN as complete", name)
		}
	}
	// The exact unverified shape is UNKNOWN carrying the read failure; one
	// carrying members the unverified variant does not declare is MALFORMED
	// (RULING-370 B6), and neither promotes them.
	rec := stream(quarantineEvent(unverifiedPayload()))
	if q := quarantineOf(t, rec); q.State != runreceipt.Unknown || !strings.Contains(q.Detail, "permission denied") {
		t.Errorf("recorded=false: observation = %+v, want UNKNOWN carrying the read failure", q)
	}
	rec = stream(quarantineEvent(p))
	if q := quarantineOf(t, rec); q.State != runreceipt.Malformed || !strings.Contains(q.Detail, "invocation") {
		t.Errorf("recorded=false with verified members: observation = %+v, want MALFORMED naming them", q)
	}
}

func TestL13MissingOrMalformedMembersAreClassified(t *testing.T) {
	cases := map[string]struct {
		line  string
		state runreceipt.Knownness
	}{
		"null payload":         {quarantineEvent(nil), runreceipt.Unknown},
		"absent payload":       {`{"kind":"` + quarantinedKind + `"}`, runreceipt.Unknown},
		"string payload":       {`{"kind":"` + quarantinedKind + `","payload":"x"}`, runreceipt.Malformed},
		"empty payload":        {quarantineEvent(map[string]string{}), runreceipt.Unknown},
		"recorded not a bool":  {quarantineEvent(verifiedPayload(map[string]string{"recorded": `"yes"`})), runreceipt.Malformed},
		"invocation a number":  {quarantineEvent(verifiedPayload(map[string]string{"invocation": `5`})), runreceipt.Malformed},
		"task_id an object":    {quarantineEvent(verifiedPayload(map[string]string{"task_id": `{}`})), runreceipt.Malformed},
		"reason absent":        {quarantineEvent(verifiedPayload(nil, "reason")), runreceipt.Known},
		"pending not object":   {quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": `"x"`})), runreceipt.Malformed},
		"pending refusal list": {quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": `{"task_id":"task-q","refusal_id":[1],"disposition":"PENDING","reason":"r"}`})), runreceipt.Known},
	}
	for name, c := range cases {
		var rec runreceipt.Receipt
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("%s: extraction panicked: %v", name, r)
				}
			}()
			rec = stream(c.line)
		}()
		if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
			t.Errorf("%s: outcome = %s, want WORKSPACE_QUARANTINED", name, rec.Outcome)
		}
		q := rec.WorkspaceQuarantine
		if q == nil {
			t.Errorf("%s: workspace_quarantine absent; the insufficiency must be stated", name)
			continue
		}
		if q.State != c.state {
			t.Errorf("%s: observation state = %s (%q), want %s", name, q.State, q.Detail, c.state)
		}
		if state, _ := rec.Completeness(); state != runreceipt.Incomplete {
			t.Errorf("%s: an insufficient quarantine must leave the record INCOMPLETE", name)
		}
		if c.state != runreceipt.Known && len(issuesWith(rec, "workspace_quarantine")) == 0 && len(issuesWith(rec, "candidate_state")) == 0 {
			t.Errorf("%s: the insufficiency is not diagnosed", name)
		}
		if c.state == runreceipt.Malformed && len(rec.Diagnostics) == 0 {
			t.Errorf("%s: a malformed member must be reported in diagnostics", name)
		}
	}
	rec := stream(cases["reason absent"].line)
	if issues := issuesWith(rec, "workspace_quarantine.reason"); len(issues) == 0 {
		t.Errorf("a verified fact without a reason must be diagnosed by the v14 checks")
	}
	rec = stream(cases["invocation a number"].line)
	if v := quarantineOf(t, rec).Invocation; v.State != runreceipt.Malformed {
		t.Errorf("invocation = %+v, want MALFORMED", v)
	}
	rec = stream(cases["pending refusal list"].line)
	if p := quarantineOf(t, rec).PendingRefusedMaterial; p == nil || p.RefusalID.State != runreceipt.Malformed {
		t.Errorf("pending refusal_id = %+v, want MALFORMED", p)
	}
	rec = stream(cases["pending not object"].line)
	// RULING-371 §16-§18 (amended from PENDING): a disclosure that is not an
	// object is not a valid disclosure; no disposition is inferred for it.
	if p := quarantineOf(t, rec).PendingRefusedMaterial; p == nil || p.Disposition != "" ||
		p.RefusalID.State != runreceipt.Malformed {
		t.Errorf("unreadable pending disclosure = %+v, want no inferred disposition and MALFORMED members", p)
	}
}

func TestL14PendingRefusedMaterialStaysPending(t *testing.T) {
	refusal := strings.Repeat("3", 64)
	full := `{"task_id":"task-q","refusal_id":"` + refusal + `","workspace":"/ws/task-q","disposition":"PENDING","reason":"refused for production scope; owed"}`
	rec := stream(quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": full})))
	p := quarantineOf(t, rec).PendingRefusedMaterial
	if p == nil {
		t.Fatal("pending_refused_material dropped")
	}
	if p.Disposition != runreceipt.PendingDisposition || p.TaskID != "task-q" || !strings.Contains(p.Reason, "owed") {
		t.Errorf("pending = %+v, want PENDING bound to the task with its reason", p)
	}
	if p.RefusalID.State != runreceipt.Known || p.RefusalID.Text != refusal || !strings.Contains(p.RefusalID.Source, "pending_refused_material.refusal_id") {
		t.Errorf("refusal_id = %+v, want the supplied refusal with its source", p.RefusalID)
	}
	if p.Workspace.State != runreceipt.Known || p.Workspace.Text != "/ws/task-q" {
		t.Errorf("workspace = %+v", p.Workspace)
	}
	if issues := issuesWith(rec, "workspace_quarantine"); len(issues) > 0 {
		t.Errorf("a full pending disclosure satisfies v14, got %v", issues)
	}
	for _, d := range rec.Diagnostics {
		if strings.Contains(d, "candidate.resolved") {
			t.Errorf("no resolution may be synthesized: %q", d)
		}
	}

	// Members not supplied are not invented.
	rec = stream(quarantineEvent(unverifiedPayload()), "")
	if quarantineOf(t, rec).PendingRefusedMaterial != nil {
		t.Error("no pending disclosure was supplied, and none may be stated")
	}
	p2 := unverifiedPayload()
	p2["pending_refused_material"] = `{"task_id":"task-q","disposition":"PENDING","reason":"owed; its governing refusal identity is not established"}`
	rec = stream(quarantineEvent(p2))
	p = quarantineOf(t, rec).PendingRefusedMaterial
	if p == nil || p.Disposition != runreceipt.PendingDisposition {
		t.Fatalf("unverified pending = %+v, want PENDING", p)
	}
	if p.RefusalID.State != runreceipt.Unknown || p.Workspace.State != runreceipt.Unknown {
		t.Errorf("unsupplied members = %+v / %+v, want UNKNOWN", p.RefusalID, p.Workspace)
	}
	if issues := issuesWith(rec, "workspace_quarantine.pending_refused_material.refusal_id"); len(issues) == 0 {
		t.Error("an unestablished governing refusal must be diagnosed")
	}
}

func TestL15ALaterInvocationTerminalIsNotOverriddenByAStaleQuarantine(t *testing.T) {
	q := quarantineEvent(verifiedPayload(map[string]string{"pending_refused_material": `{"task_id":"task-q","disposition":"PENDING","reason":"r"}`}))
	cases := map[string]struct {
		later   []string
		outcome runreceipt.Outcome
	}{
		"completed":                    {[]string{`{"kind":"candidate.changed","payload":{}}`, `{"kind":"workflow.completed","payload":{}}`}, runreceipt.OutcomeUnreviewed},
		"failed":                       {[]string{`{"kind":"workflow.failed","payload":{}}`}, runreceipt.OutcomeFailed},
		"plan admission refused":       {[]string{refusalPayload()}, runreceipt.OutcomePlanAdmissionRefused},
		"unmodelled invocation ending": {[]string{`{"kind":"workflow.stopped","payload":{}}`}, runreceipt.OutcomeUnknown},
	}
	for name, c := range cases {
		rec := stream(append([]string{q}, c.later...)...)
		if rec.Outcome != c.outcome {
			t.Errorf("%s: outcome = %s, want %s", name, rec.Outcome, c.outcome)
		}
		if rec.WorkspaceQuarantine != nil {
			t.Errorf("%s: the earlier invocation's quarantine (%+v) is stated as this record's ending", name, rec.WorkspaceQuarantine)
		}
		if rec.Terminal.Text == quarantinedKind {
			t.Errorf("%s: terminal = %+v, the stale quarantine", name, rec.Terminal)
		}
	}
	// The later invocation's own candidate evidence is its account.
	digest := strings.Repeat("f", 64)
	rec := stream(q, `{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"p","candidate":"`+digest+`"}}`,
		`{"kind":"workflow.failed","payload":{}}`)
	if rec.CandidateState != runreceipt.CandidatePresent || rec.CandidateDigest.Text != digest {
		t.Errorf("later invocation: candidate_state %s digest %+v, want its own PRESENT", rec.CandidateState, rec.CandidateDigest)
	}
	// And a quarantine in a later invocation is that invocation's ending.
	rec = stream(acceptVerdict, `{"kind":"workflow.completed","payload":{}}`, q)
	if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined {
		t.Errorf("later quarantine: outcome = %s", rec.Outcome)
	}
}

func TestL16PlanAdmissionRefusalPrecedenceIsUnchanged(t *testing.T) {
	rec := stream(acceptVerdict, refusalPayload())
	if rec.Outcome != runreceipt.OutcomePlanAdmissionRefused || rec.PlanAdmissionRefusal == nil {
		t.Errorf("outcome = %s refusal %+v, want PLAN_ADMISSION_REFUSED over the earlier ACCEPT", rec.Outcome, rec.PlanAdmissionRefusal)
	}
	rec = stream(refusalPayload(), quarantineEvent(verifiedPayload(nil)))
	if rec.Outcome != runreceipt.OutcomeWorkspaceQuarantined || rec.PlanAdmissionRefusal != nil {
		t.Errorf("a later quarantine: outcome = %s refusal %+v, want WORKSPACE_QUARANTINED and no stale refusal", rec.Outcome, rec.PlanAdmissionRefusal)
	}
}

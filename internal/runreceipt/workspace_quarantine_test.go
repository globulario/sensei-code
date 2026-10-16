package runreceipt

// N5 P2-A receipt witnesses (RULING-366 §7-§11, §30 W6, W15, W16, W17).
//
// The quarantine observation is attached through the receipt's JSON form, the
// form a reader decodes from the record, so these witnesses read the same bytes
// on a schema that does not define the field as on one that does.

import (
	"encoding/json"
	"strings"
	"testing"
)

const (
	v13 = "sensei-code.governed-run-receipt/v13"
	v14 = "sensei-code.governed-run-receipt/v14"
	// The outcome and terminal as the record spells them.
	workspaceQuarantinedOutcome = Outcome("WORKSPACE_QUARANTINED")
)

// verifiedQuarantine is the observation a locked read of an unsettled
// candidate record establishes: every fact the record names, each with its
// source, and the baseline UNKNOWN because the record names none.
func verifiedQuarantine() map[string]any {
	return map[string]any{
		"state":      "KNOWN",
		"source":     "candidate.LoadLocked: the durable candidate record names an unsettled execution",
		"task_id":    "task-1",
		"recorded":   true,
		"invocation": map[string]any{"text": "exec-1", "state": "KNOWN", "source": "candidate record in_flight.invocation"},
		"phase":      map[string]any{"text": "certify", "state": "KNOWN", "source": "candidate record in_flight.phase"},
		"workspace":  map[string]any{"text": "/repo/.worktrees/task-1", "state": "KNOWN", "source": "candidate record in_flight.workspace"},
		"baseline":   map[string]any{"state": "UNKNOWN", "detail": "the candidate record names no baseline workspace"},
		"reason":     "confined execution exec-1 (certify) has no established teardown",
	}
}

// unverifiedQuarantine is the observation of a record that could not be read
// while a workspace safety stop ended the run: nothing measured, and the read
// failure stated.
func unverifiedQuarantine() map[string]any {
	return map[string]any{
		"state":      "UNKNOWN",
		"detail":     "the candidate record of task-1 could not be read: candidate identity for task-1 is unreadable",
		"task_id":    "task-1",
		"recorded":   false,
		"invocation": map[string]any{"state": "UNKNOWN", "detail": "the record could not be read"},
		"phase":      map[string]any{"state": "UNKNOWN", "detail": "the record could not be read"},
		"workspace":  map[string]any{"state": "UNKNOWN", "detail": "the record could not be read"},
		"baseline":   map[string]any{"state": "UNKNOWN", "detail": "the record could not be read"},
		"reason":     "candidate identity for task-1 is unreadable",
	}
}

// quarantinedReceipt is a receipt of a v14 WORKSPACE_QUARANTINED terminal:
// candidate_state UNKNOWN, no candidate evidence, no verdict, carrying wq as
// its workspace_quarantine member (none when wq is nil).
func quarantinedReceipt(t *testing.T, schema string, outcome Outcome, wq map[string]any) Receipt {
	t.Helper()
	r := completeReceipt()
	r.Schema = schema
	r.Outcome = outcome
	r.CandidateState = CandidateUnknown
	none := UnknownValue("not measured: the workspace is quarantined and may still be written")
	r.CandidateCommit, r.CandidateTree, r.CandidateFirstParent, r.CandidateDigest = none, none, none, none
	r.CandidateCommitDiffDigest = none
	r.CandidateDigestRelation = RelationUnknown
	r.ReviewerProvider = UnknownValue("no reviewer was assigned")
	r.ReviewVerdict = UnknownValue("no bounded verdict was returned")
	r.ReviewedDigest = UnknownValue("no bounded verdict was returned")
	r.ReviewedTree = UnknownValue("no bounded review was delivered")
	r.Attempts = nil
	r.Terminal = MeasuredValue("workflow.workspace_quarantined", "the terminal event this run emitted")
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if wq != nil {
		doc["workspace_quarantine"] = wq
	}
	raw, err = json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var out Receipt
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func mentions(missing []string, sub string) bool {
	for _, m := range missing {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

// W6. The current schema is v14 and speaks WORKSPACE_QUARANTINED; v13 does
// not, and its vocabularies are what they were.
func TestW6TheV14SchemaSpeaksWorkspaceQuarantinedAndV13DoesNot(t *testing.T) {
	if SchemaVersion != v14 {
		t.Fatalf("SchemaVersion = %q, want %q", SchemaVersion, v14)
	}
	if !workspaceQuarantinedOutcome.Valid() || !workspaceQuarantinedOutcome.SufficientForComplete() {
		t.Fatal("WORKSPACE_QUARANTINED is not an outcome this schema defines")
	}
	if err := SpeaksItsVersion(v14, workspaceQuarantinedOutcome); err != nil {
		t.Fatalf("v14 does not speak WORKSPACE_QUARANTINED: %v", err)
	}
	if err := SpeaksItsVersion(v13, workspaceQuarantinedOutcome); err == nil {
		t.Fatal("v13 speaks WORKSPACE_QUARANTINED, which it never defined")
	}
	if got := len(vocabularies[v13]); got != 15 {
		t.Fatalf("the v13 outcome vocabulary changed (%d outcomes)", got)
	}
	if got := len(candidateVocabularies[v13]); got != 4 {
		t.Fatalf("the v13 candidate vocabulary changed (%d states)", got)
	}
	// A v13 receipt that states the outcome is refused by its own version.
	r := quarantinedReceipt(t, v13, workspaceQuarantinedOutcome, verifiedQuarantine())
	if state, missing := r.Completeness(); state != Incomplete || !mentions(missing, "not in the vocabulary") {
		t.Fatalf("a v13 receipt speaking WORKSPACE_QUARANTINED was not refused by its version: %s %v", state, missing)
	}
}

// W6 and W15. A v14 WORKSPACE_QUARANTINED receipt requires its
// workspace_quarantine observation: with a consistent, source-backed one it
// is COMPLETE although candidate_state is UNKNOWN; without one it is not.
func TestW15AVerifiedQuarantineExplainsAnUnknownCandidateAndAMissingOneDoesNot(t *testing.T) {
	r := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, verifiedQuarantine())
	if state, missing := r.Completeness(); state != Complete {
		t.Fatalf("a verified, consistent quarantine receipt is not COMPLETE: %v", missing)
	}
	absent := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, nil)
	state, missing := absent.Completeness()
	if state != Incomplete || !mentions(missing, "workspace_quarantine") || !mentions(missing, "candidate_state UNKNOWN") {
		t.Fatalf("a quarantine receipt with no quarantine observation was not INCOMPLETE on both counts: %s %v", state, missing)
	}
}

// W15, the contradictions. Each makes the observation inconsistent, and an
// inconsistent observation explains nothing.
func TestW15AContradictoryQuarantineObservationIsIncomplete(t *testing.T) {
	for name, edit := range map[string]func(map[string]any){
		"recorded false while known": func(m map[string]any) { m["recorded"] = false },
		"no task":                    func(m map[string]any) { delete(m, "task_id") },
		"no reason":                  func(m map[string]any) { m["reason"] = " " },
		"no source":                  func(m map[string]any) { delete(m, "source") },
		"an unknown workspace":       func(m map[string]any) { m["workspace"] = map[string]any{"state": "UNKNOWN", "detail": "?"} },
		"a sourceless invocation":    func(m map[string]any) { m["invocation"] = map[string]any{"text": "exec-1", "state": "KNOWN"} },
		"an invalid phase state": func(m map[string]any) {
			m["phase"] = map[string]any{"text": "certify", "state": "CERTAIN", "source": "s"}
		},
	} {
		wq := verifiedQuarantine()
		edit(wq)
		r := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, wq)
		if state, missing := r.Completeness(); state != Incomplete || !mentions(missing, "workspace_quarantine") {
			t.Errorf("%s: the receipt is %s and does not name the observation: %v", name, state, missing)
		}
	}
}

// W16. CandidateUnknown is never sufficient elsewhere: not for another
// outcome carrying the same observation, not under an older version, and the
// quarantine outcome does not admit another candidate state.
func TestW16AnUnknownCandidateIsInsufficientForEveryOtherOutcomeAndVersion(t *testing.T) {
	for _, o := range []Outcome{OutcomeFailed, OutcomeStopped, OutcomeTimedOut, OutcomeUnreviewed, OutcomeDeferred} {
		r := quarantinedReceipt(t, v14, o, verifiedQuarantine())
		state, missing := r.Completeness()
		if state != Incomplete || !mentions(missing, "candidate_state UNKNOWN") {
			t.Errorf("%s with an UNKNOWN candidate was not refused for it: %s %v", o, state, missing)
		}
		if !mentions(missing, "workspace_quarantine") {
			t.Errorf("%s carrying a quarantine observation was not refused as contradictory: %v", o, missing)
		}
	}
	old := quarantinedReceipt(t, v13, workspaceQuarantinedOutcome, verifiedQuarantine())
	if state, missing := old.Completeness(); state != Incomplete || !mentions(missing, "candidate_state UNKNOWN") {
		t.Fatalf("a v13 label made an UNKNOWN candidate sufficient: %s %v", state, missing)
	}
	present := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, verifiedQuarantine())
	present.CandidateState = CandidatePresent
	if state, missing := present.Completeness(); state != Incomplete || !mentions(missing, "WORKSPACE_QUARANTINED") {
		t.Fatalf("WORKSPACE_QUARANTINED beside a PRESENT candidate was not refused: %s %v", state, missing)
	}
	minted := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, verifiedQuarantine())
	minted.CandidateTree = MeasuredValue("tttttttttttttttttttttttttttttttttttttttt", "git rev-parse")
	if state, missing := minted.Completeness(); state != Incomplete || !mentions(missing, "candidate_tree") {
		t.Fatalf("a quarantine receipt stating a candidate tree was not refused: %s %v", state, missing)
	}
}

// W17, the receipt half. An unverified observation -- the record could not
// be read -- is stated, carries the read failure, and never completes the
// receipt; it may not state a measurement it did not make.
func TestW17AnUnverifiedQuarantineNeverCompletesTheReceipt(t *testing.T) {
	r := quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, unverifiedQuarantine())
	state, missing := r.Completeness()
	if state != Incomplete || !mentions(missing, "workspace_quarantine: the candidate record of task-1 could not be read") {
		t.Fatalf("an unverified quarantine completed the receipt or hid its read failure: %s %v", state, missing)
	}
	if r.CandidateState != CandidateUnknown {
		t.Fatalf("premise: %s", r.CandidateState)
	}
	fabricated := unverifiedQuarantine()
	fabricated["workspace"] = map[string]any{"text": "/repo", "state": "KNOWN", "source": "a guess"}
	r = quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, fabricated)
	if _, missing := r.Completeness(); !mentions(missing, "workspace_quarantine.workspace") {
		t.Fatalf("an unverified observation stated a measured workspace unrefused: %v", missing)
	}
	claimed := unverifiedQuarantine()
	claimed["recorded"] = true
	r = quarantinedReceipt(t, v14, workspaceQuarantinedOutcome, claimed)
	if _, missing := r.Completeness(); !mentions(missing, "recorded") {
		t.Fatalf("an unverified observation claimed a recorded state unrefused: %v", missing)
	}
}

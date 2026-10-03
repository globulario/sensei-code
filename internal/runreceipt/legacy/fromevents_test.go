package legacy

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/runreceipt"
)

// baseline is the set of preserved governed-run logs that must ALWAYS be part
// of reality testing, pinned by digest.
//
// C5 was voided by a parser validated only against specimens its author
// invented, while a log carrying the exact crashing shape sat unexamined in
// this repository. So the baseline never skips and never rotates: pure
// discovery could silently lose C4 or C5 while a newer log kept the count up,
// and the digests make it the corpus that actually produced the historical
// observations rather than a file that later took its name.
var baseline = map[string]string{
	"../../../experiments/c4-path-authority/runs/C4.log":      "9fdf2fc2cf65ceb618bc866384cc13a02d89a7f634f1dfbec6b74ba6fc132095",
	"../../../experiments/c5-witness-obligations/runs/C5.log": "e80c2e87f73676ff36061775aec1c37ce24735a96909f20e6f86efbe0a3e7579",
}

// discovered adds every other preserved run log, so C6 and its successors join
// reality testing without anyone remembering to edit this file.
func discovered(t *testing.T) []string {
	t.Helper()
	all, err := filepath.Glob("../../../experiments/*/runs/*.log")
	if err != nil {
		t.Fatalf("globbing preserved logs: %v", err)
	}
	var extra []string
	for _, p := range all {
		if _, pinned := baseline[p]; !pinned {
			extra = append(extra, p)
		}
	}
	sort.Strings(extra)
	return extra
}

func TestTheBaselineCorpusIsPresentAndUnchanged(t *testing.T) {
	for path, want := range baseline {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("the baseline corpus must be readable, and %s was not: %v. "+
				"A regression suite that quietly stops asking reality is how the fabricated specimen got in.", path, err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Fatalf("%s digest %s, pinned %s: the historical corpus changed, so it is no longer the corpus that produced the historical observations", path, got, want)
		}
	}
}

// TestEveryPreservedLogIsTotallyReadable is the totality contract over ALL
// preserved logs, pinned and discovered alike.
//
// It asserts only what totality means: no crash, every field in a state this
// schema defines, and a valid outcome. It deliberately asserts NOTHING about
// content, because discovery reaches runs that are legitimately incomplete --
// W1.void1-operator.log is a void run that never reached a binding revision,
// and demanding a base commit from it would be asserting that reality should
// have been tidier.
func TestEveryPreservedLogIsTotallyReadable(t *testing.T) {
	paths := discovered(t)
	for path := range baseline {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) < len(baseline) {
		t.Fatalf("only %d preserved log(s) found; the baseline alone is %d", len(paths), len(baseline))
	}
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		rec := FromEvents(f)
		f.Close()
		for _, fl := range rec.Fields() {
			if !fl.Value.State.Valid() {
				t.Fatalf("%s left %s in state %q", path, fl.Name, fl.Value.State)
			}
		}
		for i, a := range rec.Attempts {
			for name, v := range map[string]runreceipt.Value{"provider": a.Provider, "delivery": a.Delivery, "verdict": a.Verdict, "digest": a.Digest} {
				if !v.State.Valid() {
					t.Fatalf("%s attempts[%d].%s state %q", path, i, name, v.State)
				}
			}
		}
		if !rec.CandidateDigestRelation.Valid() {
			t.Fatalf("%s: digest relation %q", path, rec.CandidateDigestRelation)
		}
		if !rec.Outcome.Valid() || !rec.CandidateState.Valid() || !rec.PlanState.Valid() {
			t.Fatalf("%s: outcome %q candidate_state %q plan_state %q",
				path, rec.Outcome, rec.CandidateState, rec.PlanState)
		}
		// Deliberately NOT logging rec.Outcome. C5 is a VOID witness and its
		// semantic content is inadmissible as evidence about that run; a
		// passing test's output is exactly where an inadmissible fact would
		// acquire a citable home.
		t.Logf("%s -> attempts=%d diagnostics=%d", path, len(rec.Attempts), len(rec.Diagnostics))
	}
}

// TestThePinnedBaselineStillYieldsMeasurements is the content assertion, and it
// applies only to the two full runs the baseline pins. If the reader ever stops
// finding what these logs demonstrably contain, that is a regression in the
// reader rather than a fact about a run that ended early.
func TestThePinnedBaselineStillYieldsMeasurements(t *testing.T) {
	for path := range baseline {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		rec := FromEvents(f)
		f.Close()
		if rec.BaseCommit.State != runreceipt.Known {
			t.Errorf("%s: base commit %s (%s)", path, rec.BaseCommit.State, rec.BaseCommit.Detail)
		}
		if rec.GraphDigest.State != runreceipt.Known {
			t.Errorf("%s: graph digest %s (%s)", path, rec.GraphDigest.State, rec.GraphDigest.Detail)
		}
		if rec.BaseCommit.State == runreceipt.Known && strings.TrimSpace(rec.BaseCommit.Source) == "" {
			t.Errorf("%s: a measured base commit with no source", path)
		}
	}
}

// TestTheAdapterNeverInfersTheGovernorFromTheBase is the law this chain keeps
// rediscovering: one measured fact must not become two claims. G == B held in
// C5 by construction; it is not an architectural invariant, and an adapter that
// copies one into the other manufactures a governance relationship the event
// stream never recorded.
func TestTheAdapterNeverInfersTheGovernorFromTheBase(t *testing.T) {
	log := `{"kind":"sensei.result","payload":{"binding":{"revision":"f01592b0f0828605ed254047fc064f41dacc78f2"}}}`
	rec := FromEvents(strings.NewReader(log))
	if rec.BaseCommit.State != runreceipt.Known {
		t.Fatalf("base commit should be measured, got %v", rec.BaseCommit)
	}
	if rec.GovernorCommit.State != runreceipt.Unknown {
		t.Fatalf("governor commit = %v; the stream does not identify it and the adapter must say so", rec.GovernorCommit)
	}
	if !strings.Contains(rec.GovernorCommit.Detail, "NOT the base commit") {
		t.Errorf("the reason must name the distinction, got %q", rec.GovernorCommit.Detail)
	}
}

// TestTheShapeThatVoidedC5IsAClassificationNotACrash pins the specific defect:
// payload.provenance is a string in one real event out of 9123.
func TestTheShapeThatVoidedC5IsAClassificationNotACrash(t *testing.T) {
	log := `{"kind":"mode.selected","payload":{"provenance":"submitted unattended with an externally supplied plan"}}
{"kind":"review.completed","payload":{"decision":"accept","provenance":"a string where an object was assumed"}}`
	rec := FromEvents(strings.NewReader(log))
	if rec.ReviewedDigest.State != runreceipt.Malformed {
		t.Fatalf("reviewed digest state = %s, want MALFORMED", rec.ReviewedDigest.State)
	}
	if !strings.Contains(rec.ReviewedDigest.Detail, "not an object") {
		t.Errorf("detail must say what arrived, got %q", rec.ReviewedDigest.Detail)
	}
	// One malformed neighbour must not erase a fact reported correctly.
	if rec.ReviewVerdict.State != runreceipt.Known || rec.Outcome != runreceipt.OutcomeAccepted {
		t.Errorf("verdict=%v outcome=%s", rec.ReviewVerdict, rec.Outcome)
	}
}

func TestNoSyntacticallyValidEventCanCrashExtraction(t *testing.T) {
	shapes := []string{`null`, `"s"`, `12`, `true`, `[1,2]`, `{}`, `{"provider":[]}`, `{"decision":{}}`,
		`{"provenance":12}`, `{"binding":"s"}`, `{"evidence":[]}`, `{"graph_authority":null}`}
	kinds := []string{"review.completed", "agent.role.assigned", "sensei.result", "candidate.resolved",
		"plan.proposed", "workflow.completed", "run.receipt", "workflow.plan_admission_refused", "wholly.unknown.kind", ""}
	for _, k := range kinds {
		for _, s := range shapes {
			rec := FromEvents(strings.NewReader(`{"kind":"` + k + `","payload":` + s + `}`))
			if rec.Schema != runreceipt.SchemaVersion {
				t.Fatalf("kind=%q payload=%s produced no receipt", k, s)
			}
			for _, f := range rec.Fields() {
				switch f.Value.State {
				case runreceipt.Known, runreceipt.Unknown, runreceipt.Malformed, runreceipt.Unsupported:
				default:
					t.Fatalf("kind=%q payload=%s left %s in state %q", k, s, f.Name, f.Value.State)
				}
			}
			if !rec.Outcome.Valid() {
				t.Fatalf("kind=%q payload=%s produced invalid outcome %q", k, s, rec.Outcome)
			}
		}
	}
}

func TestAnUnmodelledKindIsReportedRatherThanSilentlyDropped(t *testing.T) {
	rec := FromEvents(strings.NewReader(`{"kind":"something.nobody.modelled","payload":{}}`))
	if !strings.Contains(strings.Join(rec.Diagnostics, " "), "something.nobody.modelled") {
		t.Fatalf("an unmodelled kind must appear in diagnostics, got %v", rec.Diagnostics)
	}
}

func TestTheReviewerTrailKeepsAFailedAttemptBesideTheDeliveringOne(t *testing.T) {
	log := `{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"codex"}}
{"kind":"agent.finished","payload":{"provider":"codex","error":"no response"}}
{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"gemini"}}
{"kind":"review.completed","payload":{"decision":"accept","provenance":{"provider":"gemini","candidate_digest":"dd11"}}}`
	rec := FromEvents(strings.NewReader(log))
	if len(rec.Attempts) != 2 {
		t.Fatalf("attempts = %d, want both the failed and the delivering provider", len(rec.Attempts))
	}
	if rec.Attempts[0].Provider.Text != "codex" || rec.Attempts[0].DeliveredVerdict() {
		t.Errorf("first attempt = %+v, want codex not measured as delivering", rec.Attempts[0])
	}
	// The adapter must not INFER that the superseded attempt failed: the stream
	// records a replacement, not a failure.
	if rec.Attempts[0].Delivery.State != runreceipt.Unknown {
		t.Errorf("first attempt delivery = %v, want UNKNOWN rather than an inferred failure", rec.Attempts[0].Delivery)
	}
	if rec.Attempts[1].Provider.Text != "gemini" || !rec.Attempts[1].DeliveredVerdict() {
		t.Errorf("second attempt = %+v, want gemini measured as delivering", rec.Attempts[1])
	}
}

// TestAnAdapterBuiltReceiptIsNeverCompleteOnItsOwn states the boundary plainly:
// the historical stream cannot supply governor identity, the serving producer
// or a binary digest, so a reconstruction can never masquerade as a full
// account. Only a governor emitting its own receipt can produce one.
func TestAnAdapterBuiltReceiptIsNeverCompleteOnItsOwn(t *testing.T) {
	f, err := os.Open("../../../experiments/c4-path-authority/runs/C4.log")
	if err != nil {
		t.Fatalf("baseline corpus: %v", err)
	}
	defer f.Close()
	rec := FromEvents(f)
	state, missing := rec.Completeness()
	if state != runreceipt.Incomplete {
		t.Fatal("a reconstruction must never pass as a complete governed-run record")
	}
	joined := strings.Join(missing, " ")
	for _, want := range []string{"governor_commit", "governor_binary_sha256", "serving_producer"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing reasons should name %s, got %v", want, missing)
		}
	}
}

// specimenStream has the objective-62 (DF-35) shape, taken from objective 61
// run 1: the run's own receipt says PRESENT and names the reviewed diff digest,
// while every minted-identity field carries the live emitter's "no candidate
// was created" wording although a candidate commit existed.
const specimenStream = `{"kind":"candidate.changed","payload":{"cycle":1,"review_attempt":1}}
{"kind":"candidate.resolved","payload":{"disposition":"resumable","evidence":{"base_sha":"385377c953a06800a1b9347249b6a43ad0143504"}}}
{"kind":"run.receipt","payload":{"completeness":"INCOMPLETE","receipt":{"schema":"sensei-code.governed-run-receipt/v13","candidate_state":"PRESENT",` +
	`"candidate_commit":{"state":"UNKNOWN","detail":"not measured: no candidate was created"},` +
	`"candidate_tree":{"state":"UNKNOWN","detail":"not measured: no candidate was created"},` +
	`"candidate_first_parent":{"state":"UNKNOWN","detail":"not measured: no candidate was created"},` +
	`"candidate_digest":{"text":"2da0c2816f8aa91c203e3d1128dcfd2a30327b038ed202fffdae7a53c15c62c9","state":"KNOWN","source":"sha256 of the candidate diff, as the review binding names it"},` +
	`"candidate_commit_diff_digest":{"state":"UNKNOWN","detail":"not measured: no candidate identity was minted"},"outcome":"FAILED"}}}
{"kind":"workflow.failed","payload":null}`

// TestW1TheDF35SpecimenDoesNotRenderAnUnprovenAbsence: where the stream cannot
// establish whether a candidate identity was minted, the reconstruction says
// so, and does not repeat an absence claim the events do not prove.
func TestW1TheDF35SpecimenDoesNotRenderAnUnprovenAbsence(t *testing.T) {
	for name, log := range map[string]string{"specimen": specimenStream, "empty stream": ``} {
		rec := FromEvents(strings.NewReader(log))
		for _, f := range rec.Fields() {
			for _, absence := range []string{"no candidate was created", "no candidate identity was minted"} {
				if strings.Contains(f.Value.Detail, absence) {
					t.Errorf("%s: %s = %q claims absence the stream does not establish", name, f.Name, f.Value.Detail)
				}
			}
		}
		d := rec.CandidateCommitDiffDigest
		if d.State != runreceipt.Unknown || !strings.Contains(d.Detail, "cannot establish whether a candidate identity was minted") {
			t.Errorf("%s: candidate_commit_diff_digest = %+v, want UNKNOWN naming the stream's insufficiency", name, d)
		}
		if rec.CandidateCommit.State != runreceipt.Unknown {
			t.Errorf("%s: candidate_commit = %+v, want UNKNOWN", name, rec.CandidateCommit)
		}
	}
	rec := FromEvents(strings.NewReader(specimenStream))
	if rec.CandidateState != runreceipt.CandidatePresent {
		t.Errorf("specimen candidate_state = %s, want the PRESENT its receipt and events establish", rec.CandidateState)
	}
	if rec.CandidateDigest.State != runreceipt.Known || rec.CandidateDigest.Text != "2da0c2816f8aa91c203e3d1128dcfd2a30327b038ed202fffdae7a53c15c62c9" {
		t.Errorf("specimen candidate_digest = %+v, want the digest its receipt measured", rec.CandidateDigest)
	}
}

// TestW2APositiveAbsenceStillRendersAsAbsence: a receipt that canonically
// states NONE is evidence of absence, and is not demoted to UNKNOWN.
func TestW2APositiveAbsenceStillRendersAsAbsence(t *testing.T) {
	log := `{"kind":"run.receipt","payload":{"receipt":{"schema":"sensei-code.governed-run-receipt/v13","candidate_state":"NONE",` +
		`"candidate_commit":{"state":"UNKNOWN","detail":"not measured: no candidate was created"}}}}
{"kind":"workflow.completed","payload":{}}`
	rec := FromEvents(strings.NewReader(log))
	if rec.CandidateState != runreceipt.CandidateNone {
		t.Fatalf("candidate_state = %s, want NONE", rec.CandidateState)
	}
	for name, v := range map[string]runreceipt.Value{"candidate_commit": rec.CandidateCommit, "candidate_tree": rec.CandidateTree,
		"candidate_first_parent": rec.CandidateFirstParent, "candidate_digest": rec.CandidateDigest,
		"candidate_commit_diff_digest": rec.CandidateCommitDiffDigest} {
		if v.State != runreceipt.Unknown || !strings.Contains(v.Detail, "states candidate_state NONE") {
			t.Errorf("%s = %+v, want the absence the receipt established", name, v)
		}
	}
	_, missing := rec.Completeness()
	for _, m := range missing {
		if strings.HasPrefix(m, "candidate_") {
			t.Errorf("a NONE reconstruction must be consistent with its candidate evidence, got %q", m)
		}
	}

	// Transient work earlier in the same invocation does not veto the
	// receipt's canonical final NONE: the receipt is the invocation's account,
	// and candidate.changed is only a fallback observation.
	rec = FromEvents(strings.NewReader(`{"kind":"candidate.changed","payload":{"cycle":1}}` + "\n" + log))
	if rec.CandidateState != runreceipt.CandidateNone {
		t.Errorf("NONE after transient work: candidate_state = %s, want the receipt's NONE", rec.CandidateState)
	}
	if v := rec.CandidateCommit; v.State != runreceipt.Unknown || !strings.Contains(v.Detail, "states candidate_state NONE") {
		t.Errorf("NONE after transient work: candidate_commit = %+v, want the absence the receipt established", v)
	}
}

// knownField renders a KNOWN receipt identity field.
func knownField(v string) string { return `{"text":"` + v + `","state":"KNOWN","source":"git"}` }

// receiptEvent is a v13 run.receipt event with the given candidate state and
// identity fields; an empty state omits candidate_state.
func receiptEvent(state string, fields map[string]string) string {
	parts := []string{`"schema":"sensei-code.governed-run-receipt/v13"`}
	if state != "" {
		parts = append(parts, `"candidate_state":"`+state+`"`)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		parts = append(parts, `"`+k+`":`+fields[k])
	}
	return `{"kind":"run.receipt","payload":{"receipt":{` + strings.Join(parts, ",") + `}}}`
}

// completePresent is a receipt naming every candidate identity.
func completePresent() string {
	return receiptEvent("PRESENT", map[string]string{
		"candidate_commit": knownField(strings.Repeat("a", 40)), "candidate_tree": knownField(strings.Repeat("b", 40)),
		"candidate_first_parent": knownField(strings.Repeat("c", 40)), "candidate_digest": knownField(strings.Repeat("d", 64)),
		"candidate_commit_diff_digest": knownField(strings.Repeat("e", 64)),
	})
}

// candidateIdentities names the five candidate identity fields of rec.
func candidateIdentities(rec runreceipt.Receipt) map[string]runreceipt.Value {
	return map[string]runreceipt.Value{"candidate_commit": rec.CandidateCommit, "candidate_tree": rec.CandidateTree,
		"candidate_first_parent": rec.CandidateFirstParent, "candidate_digest": rec.CandidateDigest,
		"candidate_commit_diff_digest": rec.CandidateCommitDiffDigest}
}

// TestFallbackCandidateEvidenceDoesNotCrossATerminal: fallback work evidence
// belongs to the invocation that observed it. A terminal settles and resets
// it, so a later invocation does not inherit an earlier one's candidate, and a
// stream that ends after its terminal keeps the invocation that terminal
// settled.
func TestFallbackCandidateEvidenceDoesNotCrossATerminal(t *testing.T) {
	digest := strings.Repeat("f", 64)
	first := `{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"p","candidate":"` + digest + `"}}
{"kind":"workflow.failed","payload":{}}`

	rec := FromEvents(strings.NewReader(first + "\n" + `{"kind":"candidate.changed","payload":{"cycle":2}}
{"kind":"workflow.failed","payload":{}}`))
	if rec.CandidateState != runreceipt.CandidatePresent {
		t.Errorf("later invocation's own work: candidate_state = %s, want PRESENT", rec.CandidateState)
	}
	if rec.CandidateDigest.State != runreceipt.Unknown {
		t.Errorf("later invocation: candidate_digest = %+v, want UNKNOWN rather than the earlier invocation's candidate", rec.CandidateDigest)
	}

	rec = FromEvents(strings.NewReader(first + "\n" + receiptEvent("UNKNOWN", nil) + "\n" + `{"kind":"workflow.completed","payload":{}}`))
	if rec.CandidateState != runreceipt.CandidateUnknown || rec.CandidateDigest.State != runreceipt.Unknown {
		t.Errorf("later UNKNOWN receipt: candidate_state = %s, candidate_digest = %+v; the earlier invocation's fallback must not reach it",
			rec.CandidateState, rec.CandidateDigest)
	}

	// Trailing events without candidate evidence leave the settled invocation.
	rec = FromEvents(strings.NewReader(first + "\n" + `{"kind":"plan.proposed","payload":{"plan_digest":"x"}}`))
	if rec.CandidateState != runreceipt.CandidatePresent || rec.CandidateDigest.Text != digest {
		t.Errorf("settled invocation: candidate_state = %s, candidate_digest = %+v, want PRESENT %s",
			rec.CandidateState, rec.CandidateDigest, digest)
	}
}

// TestAnEvidenceFreeInvocationKeepsTheSettledCandidate: an invocation that
// reaches a terminal without candidate evidence resets its own state but does
// not erase the candidate an earlier invocation settled.
func TestAnEvidenceFreeInvocationKeepsTheSettledCandidate(t *testing.T) {
	digest := strings.Repeat("f", 64)
	log := `{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"p","candidate":"` + digest + `"}}
{"kind":"workflow.failed","payload":{}}
{"kind":"plan.proposed","payload":{"plan_digest":"x"}}
{"kind":"workflow.failed","payload":{}}`
	rec := FromEvents(strings.NewReader(log))
	if rec.CandidateState != runreceipt.CandidatePresent || rec.CandidateDigest.State != runreceipt.Known || rec.CandidateDigest.Text != digest {
		t.Errorf("evidence-free later invocation: candidate_state = %s, candidate_digest = %+v, want the settled PRESENT %s",
			rec.CandidateState, rec.CandidateDigest, digest)
	}
}

// TestAnUnsupportedIdentityIsNotAnAbsence: a receipt stating NONE beside an
// identity it classified UNSUPPORTED could not read what it claims is absent.
// The classification is kept, so the reconstruction stays incomplete rather
// than rendering a clean absence.
func TestAnUnsupportedIdentityIsNotAnAbsence(t *testing.T) {
	log := receiptEvent("NONE", map[string]string{"candidate_commit": `{"state":"UNSUPPORTED","detail":"newer shape"}`}) + "\n" +
		`{"kind":"workflow.completed","payload":{}}`
	rec := FromEvents(strings.NewReader(log))
	if rec.CandidateState != runreceipt.CandidateNone {
		t.Fatalf("candidate_state = %s, want the receipt's NONE", rec.CandidateState)
	}
	if v := rec.CandidateCommit; v.State != runreceipt.Unsupported || strings.Contains(v.Detail, "absent") {
		t.Errorf("candidate_commit = %+v, want UNSUPPORTED, not an absence", v)
	}
	if v := rec.CandidateTree; v.State != runreceipt.Unknown || !strings.Contains(v.Detail, "states candidate_state NONE") {
		t.Errorf("candidate_tree = %+v, want the absence the receipt established", v)
	}
	completeness, missing := rec.Completeness()
	if completeness != runreceipt.Incomplete {
		t.Errorf("completeness = %s, want INCOMPLETE", completeness)
	}
	found := false
	for _, m := range missing {
		if strings.HasPrefix(m, "candidate_commit is UNSUPPORTED while candidate_state is NONE") {
			found = true
		}
	}
	if !found {
		t.Errorf("missing = %q, want the NONE/UNSUPPORTED contradiction named", missing)
	}
}

// TestALaterUnknownReceiptClearsAnEarlierPresentOne: a supported receipt is a
// complete snapshot. A later receipt stating UNKNOWN replaces an earlier
// PRESENT one whole -- state and every identity -- and transient work in its
// invocation does not strengthen that UNKNOWN.
func TestALaterUnknownReceiptClearsAnEarlierPresentOne(t *testing.T) {
	later := receiptEvent("UNKNOWN", map[string]string{"candidate_commit": `{"state":"UNKNOWN","detail":"not measured: no candidate was created"}`})
	for name, between := range map[string]string{
		"across a terminal":     `{"kind":"workflow.failed","payload":{}}` + "\n",
		"within one invocation": "",
	} {
		log := completePresent() + "\n" + between + `{"kind":"candidate.changed","payload":{"cycle":2}}` + "\n" + later + "\n" +
			`{"kind":"workflow.failed","payload":{}}`
		rec := FromEvents(strings.NewReader(log))
		if rec.CandidateState != runreceipt.CandidateUnknown {
			t.Errorf("%s: candidate_state = %s, want the later receipt's UNKNOWN", name, rec.CandidateState)
		}
		for field, v := range candidateIdentities(rec) {
			if v.State != runreceipt.Unknown || strings.Contains(v.Detail, "no candidate was created") {
				t.Errorf("%s: %s = %+v, want UNKNOWN stating the stream's insufficiency", name, field, v)
			}
		}
	}
}

// TestALaterPartialReceiptInheritsNoIdentity: a later PRESENT receipt naming
// only some identities does not inherit the rest from an earlier receipt.
func TestALaterPartialReceiptInheritsNoIdentity(t *testing.T) {
	digest := strings.Repeat("9", 64)
	later := receiptEvent("PRESENT", map[string]string{"candidate_digest": knownField(digest),
		"candidate_tree": `{"state":"UNKNOWN","detail":"not measured"}`})
	for name, between := range map[string]string{
		"across a terminal":     `{"kind":"workflow.failed","payload":{}}` + "\n",
		"within one invocation": "",
	} {
		rec := FromEvents(strings.NewReader(completePresent() + "\n" + between + later + "\n" + `{"kind":"workflow.failed","payload":{}}`))
		if rec.CandidateState != runreceipt.CandidatePresent {
			t.Errorf("%s: candidate_state = %s, want PRESENT", name, rec.CandidateState)
		}
		for field, v := range candidateIdentities(rec) {
			if field == "candidate_digest" {
				if v.State != runreceipt.Known || v.Text != digest {
					t.Errorf("%s: candidate_digest = %+v, want the later receipt's %s", name, v, digest)
				}
				continue
			}
			if v.State != runreceipt.Unknown {
				t.Errorf("%s: %s = %+v, want UNKNOWN: the later receipt does not name it", name, field, v)
			}
		}
	}
}

// TestEveryRunEndingSeparatesAReceiptFromLaterFallback: every run ending the
// canonical terminal vocabulary names is an invocation boundary. An earlier
// invocation's authoritative receipt must not take precedence over a later
// invocation that has only fallback candidate evidence, whichever terminal
// separated them.
func TestEveryRunEndingSeparatesAReceiptFromLaterFallback(t *testing.T) {
	digest := strings.Repeat("f", 64)
	later := `{"kind":"agent.role.assigned","payload":{"role":"reviewer","provider":"p","candidate":"` + digest + `"}}`
	for _, terminal := range []string{
		"workflow.completed", "workflow.failed", "workflow.observed",
		"workflow.stopped", "workflow.timed_out", "workflow.awaiting_authority", "workflow.awaiting_review",
		"workflow.blocked_external", "workflow.not_converged", "workflow.restoration_refused",
		"workflow.base_moved_refused", "workflow.dirty_canonical_refused", "workflow.plan_admission_refused",
		"candidate.not_auditable",
	} {
		log := completePresent() + "\n" + `{"kind":"` + terminal + `","payload":{}}` + "\n" + later
		rec := FromEvents(strings.NewReader(log))
		if rec.CandidateState != runreceipt.CandidatePresent {
			t.Errorf("%s: candidate_state = %s, want the later invocation's PRESENT", terminal, rec.CandidateState)
		}
		for field, v := range candidateIdentities(rec) {
			if field == "candidate_digest" {
				if v.State != runreceipt.Known || v.Text != digest {
					t.Errorf("%s: candidate_digest = %+v, want the later invocation's %s", terminal, v, digest)
				}
				continue
			}
			if v.State == runreceipt.Known {
				t.Errorf("%s: %s = %+v survived from the earlier invocation's receipt", terminal, field, v)
			}
		}
	}
}

// TestW3AKnownCandidateIdentityIsReconstructed: identity values a run's
// receipt measured are carried, with the event path they were read from.
func TestW3AKnownCandidateIdentityIsReconstructed(t *testing.T) {
	commit, tree, parent := strings.Repeat("a", 40), strings.Repeat("b", 40), strings.Repeat("c", 40)
	digest, rendering := strings.Repeat("d", 64), strings.Repeat("e", 64)
	known := func(v string) string { return `{"text":"` + v + `","state":"KNOWN","source":"git"}` }
	log := `{"kind":"run.receipt","payload":{"receipt":{"schema":"sensei-code.governed-run-receipt/v13","candidate_state":"PRESENT",` +
		`"candidate_commit":` + known(commit) + `,"candidate_tree":` + known(tree) + `,"candidate_first_parent":` + known(parent) +
		`,"candidate_digest":` + known(digest) + `,"candidate_commit_diff_digest":` + known(rendering) + `}}}`
	rec := FromEvents(strings.NewReader(log))
	if rec.CandidateState != runreceipt.CandidatePresent {
		t.Fatalf("candidate_state = %s, want PRESENT", rec.CandidateState)
	}
	for name, c := range map[string]struct {
		got  runreceipt.Value
		want string
	}{
		"candidate_commit": {rec.CandidateCommit, commit}, "candidate_tree": {rec.CandidateTree, tree},
		"candidate_first_parent": {rec.CandidateFirstParent, parent}, "candidate_digest": {rec.CandidateDigest, digest},
		"candidate_commit_diff_digest": {rec.CandidateCommitDiffDigest, rendering},
	} {
		if c.got.State != runreceipt.Known || c.got.Text != c.want {
			t.Errorf("%s = %+v, want KNOWN %s", name, c.got, c.want)
		}
		if c.got.Source != "event:run.receipt.payload.receipt."+name {
			t.Errorf("%s source = %q, want the event path it was read from", name, c.got.Source)
		}
	}

	// A receipt whose schema the receipt package does not define populates no
	// candidate identity, and the rejection is reported.
	const v13 = `"schema":"sensei-code.governed-run-receipt/v13",`
	for name, schema := range map[string]string{"no schema": ``, "unknown schema": `"schema":"sensei-code.governed-run-receipt/v99",`} {
		rec := FromEvents(strings.NewReader(strings.Replace(log, v13, schema, 1)))
		for field, v := range map[string]runreceipt.Value{"candidate_commit": rec.CandidateCommit, "candidate_tree": rec.CandidateTree,
			"candidate_first_parent": rec.CandidateFirstParent, "candidate_digest": rec.CandidateDigest,
			"candidate_commit_diff_digest": rec.CandidateCommitDiffDigest} {
			if v.State == runreceipt.Known {
				t.Errorf("%s: %s = %+v, want nothing read from an unsupported receipt", name, field, v)
			}
		}
		if rec.CandidateState != runreceipt.CandidateUnknown {
			t.Errorf("%s: candidate_state = %s, want UNKNOWN", name, rec.CandidateState)
		}
		if !strings.Contains(strings.Join(rec.Diagnostics, " "), "candidate facts are not read") {
			t.Errorf("%s: diagnostics %v, want the rejected receipt reported", name, rec.Diagnostics)
		}
	}
}

// refusalPayload is a PLAN_ADMISSION_REFUSED terminal payload, with the named
// keys left out.
func refusalPayload(omit ...string) string {
	return refusalEvent("workflow.plan_admission_refused", nil, omit...)
}

// refusalEvent is a refusal event of the given kind: the canonical fields,
// with set's JSON values replacing them and the named keys left out.
func refusalEvent(kind string, set map[string]string, omit ...string) string {
	fields := map[string]string{
		"plan_attempt_id":       `"` + strings.Repeat("1", 64) + `"`,
		"task_id":               `"task-1"`,
		"reason":                `"declared surface holds no grant"`,
		"refusal_class":         `"prospective_admission"`,
		"declaration":           `[{"path":"a.go","role":"x"}]`,
		"governing_evidence_id": `"` + strings.Repeat("2", 64) + `"`,
		"refusal_id":            `"` + strings.Repeat("3", 64) + `"`,
	}
	for k, v := range set {
		fields[k] = v
	}
	for _, k := range omit {
		delete(fields, k)
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, `"`+k+`":`+fields[k])
	}
	return `{"kind":"` + kind + `","payload":{` + strings.Join(parts, ",") + `}}`
}

// TestW4APlanAdmissionRefusalIsReconstructedAsV13: a sufficiently expressive
// refusal stream yields the v13 outcome and the refusal identities it carries.
func TestW4APlanAdmissionRefusalIsReconstructedAsV13(t *testing.T) {
	rec := FromEvents(strings.NewReader(`{"kind":"review.completed","payload":{"decision":"revise"}}` + "\n" + refusalPayload()))
	if rec.Outcome != runreceipt.OutcomePlanAdmissionRefused {
		t.Fatalf("outcome = %s, want PLAN_ADMISSION_REFUSED", rec.Outcome)
	}
	p := rec.PlanAdmissionRefusal
	if p == nil || p.State != runreceipt.Known {
		t.Fatalf("plan_admission_refusal = %+v, want KNOWN", p)
	}
	if p.PlanAttemptID != strings.Repeat("1", 64) || p.GoverningEvidenceID != strings.Repeat("2", 64) ||
		p.RefusalID != strings.Repeat("3", 64) || p.Class != runreceipt.PlanAdmissionProspective ||
		p.Reason != "declared surface holds no grant" || p.Declaration != `[{"path":"a.go","role":"x"}]` {
		t.Errorf("plan_admission_refusal = %+v, want exactly the identities the event carried", p)
	}
	_, missing := rec.Completeness()
	for _, m := range missing {
		if strings.HasPrefix(m, "plan_admission_refusal") || strings.HasPrefix(m, "outcome") {
			t.Errorf("the reconstructed refusal must satisfy the v13 refusal checks, got %q", m)
		}
	}
	if strings.Contains(strings.Join(rec.Diagnostics, " "), "plan_admission_refused") {
		t.Errorf("the v13 terminal is modelled now, got diagnostics %v", rec.Diagnostics)
	}
}

// TestW5AHistoricalRefusalLacksNewerIdentitiesAndSaysSo: a refusal stream
// missing identities schema v13 requires keeps its outcome and its usable
// fields, and names the missing ones UNKNOWN instead of inventing them.
func TestW5AHistoricalRefusalLacksNewerIdentitiesAndSaysSo(t *testing.T) {
	rec := FromEvents(strings.NewReader(refusalPayload("refusal_id", "governing_evidence_id")))
	if rec.Outcome != runreceipt.OutcomePlanAdmissionRefused {
		t.Fatalf("outcome = %s, want PLAN_ADMISSION_REFUSED retained", rec.Outcome)
	}
	p := rec.PlanAdmissionRefusal
	if p == nil || p.State != runreceipt.Unknown {
		t.Fatalf("plan_admission_refusal = %+v, want UNKNOWN", p)
	}
	for _, want := range []string{"refusal_id", "governing_evidence_id"} {
		if !strings.Contains(p.Detail, want) {
			t.Errorf("detail %q must name the missing %s", p.Detail, want)
		}
	}
	if p.RefusalID != "" || p.GoverningEvidenceID != "" {
		t.Errorf("missing identities were invented: %+v", p)
	}
	if p.PlanAttemptID != strings.Repeat("1", 64) || p.Class != runreceipt.PlanAdmissionProspective {
		t.Errorf("usable fields must be kept, got %+v", p)
	}
	state, missing := rec.Completeness()
	if state != runreceipt.Incomplete || !strings.Contains(strings.Join(missing, " "), "plan_admission_refusal: the historical") {
		t.Errorf("an UNKNOWN refusal must leave the record incomplete with its reason, got %s %v", state, missing)
	}
}

// TestW4BNonCanonicalRefusalValuesAreNotKnown: a refusal is KNOWN only when the
// receipt's own v13 checks accept every value. A blank required value is
// UNKNOWN naming the field; a present value the receipt rejects is MALFORMED
// with the receipt's reason.
func TestW4BNonCanonicalRefusalValuesAreNotKnown(t *testing.T) {
	for name, c := range map[string]struct {
		set   map[string]string
		state runreceipt.Knownness
		want  string
	}{
		"short plan attempt": {map[string]string{"plan_attempt_id": `"abc123"`}, runreceipt.Malformed,
			"plan_admission_refusal.plan_attempt_id \"abc123\" is not a canonical identity"},
		"uppercase refusal identity": {map[string]string{"refusal_id": `"` + strings.Repeat("A", 64) + `"`}, runreceipt.Malformed,
			"plan_admission_refusal.refusal_id"},
		"uppercase governing evidence": {map[string]string{"governing_evidence_id": `"` + strings.Repeat("F", 64) + `"`}, runreceipt.Malformed,
			"plan_admission_refusal.governing_evidence_id"},
		"non-returning class": {map[string]string{"refusal_class": `"scope_admission"`}, runreceipt.Malformed,
			"plan_admission_refusal.refusal_class \"scope_admission\" is not a class that returns to the architect"},
		"blank reason":     {map[string]string{"reason": `"  "`}, runreceipt.Unknown, "do not carry reason,"},
		"empty refusal id": {map[string]string{"refusal_id": `""`}, runreceipt.Unknown, "do not carry refusal_id,"},
		"numeric attempt":  {map[string]string{"plan_attempt_id": `7`}, runreceipt.Malformed, "plan_attempt_id is float64"},
	} {
		rec := FromEvents(strings.NewReader(refusalEvent("workflow.plan_admission_refused", c.set)))
		if rec.Outcome != runreceipt.OutcomePlanAdmissionRefused {
			t.Errorf("%s: outcome = %s, want PLAN_ADMISSION_REFUSED retained", name, rec.Outcome)
		}
		p := rec.PlanAdmissionRefusal
		if p == nil || p.State != c.state || !strings.Contains(p.Detail, c.want) {
			t.Errorf("%s: plan_admission_refusal = %+v, want %s naming %q", name, p, c.state, c.want)
		}
	}
}

// TestW5BABoundPriorRefusalCompletesTheTerminal: a terminal missing a newer
// field is completed from an earlier canonical refusal record only when both
// carry the same refusal identity, never from a record because it is the only
// one.
func TestW5BABoundPriorRefusalCompletesTheTerminal(t *testing.T) {
	terminal := refusalPayload("governing_evidence_id")
	prior := refusalEvent("plan.attempt.refused", nil)
	// factOf is a run.receipt whose header (schema and outcome members) is
	// header, stating a KNOWN refusal fact.
	factOf := func(header string, set map[string]string) string {
		ev := refusalEvent("x", set)
		body := strings.TrimSuffix(strings.TrimPrefix(ev, `{"kind":"x","payload":{`), `}}`)
		return `{"kind":"run.receipt","payload":{"receipt":{` + header + `"plan_admission_refusal":{"state":"KNOWN","source":"record",` + body + `}}}}`
	}
	const v13Refused = `"schema":"sensei-code.governed-run-receipt/v13","outcome":"PLAN_ADMISSION_REFUSED",`
	declaration := map[string]string{"declaration": `"[{\"path\":\"a.go\",\"role\":\"x\"}]"`}
	receiptFact := factOf(v13Refused, declaration)

	for name, log := range map[string]string{
		"plan.attempt.refused": prior + "\n" + terminal,
		"run.receipt fact":     receiptFact + "\n" + terminal,
	} {
		rec := FromEvents(strings.NewReader(log))
		p := rec.PlanAdmissionRefusal
		if rec.Outcome != runreceipt.OutcomePlanAdmissionRefused || p == nil || p.State != runreceipt.Known {
			t.Fatalf("%s: outcome %s, plan_admission_refusal = %+v, want KNOWN completed from the bound record", name, rec.Outcome, p)
		}
		if p.GoverningEvidenceID != strings.Repeat("2", 64) || p.Declaration != `[{"path":"a.go","role":"x"}]` {
			t.Errorf("%s: plan_admission_refusal = %+v, want the bound record's values", name, p)
		}
		if !strings.Contains(p.Source, "bound by refusal_id") {
			t.Errorf("%s: source %q must say the fact was completed from a bound record", name, p.Source)
		}
		if strings.Contains(strings.Join(rec.Diagnostics, " "), "plan.attempt.refused") {
			t.Errorf("%s: plan.attempt.refused is read now, got diagnostics %v", name, rec.Diagnostics)
		}
	}

	// Controls: a record of a different refusal, and a terminal that names no
	// refusal, bind nothing although each stream holds exactly one record.
	other := refusalEvent("plan.attempt.refused", map[string]string{"refusal_id": `"` + strings.Repeat("4", 64) + `"`})
	for name, c := range map[string]struct{ log, want string }{
		"mismatched record":         {other + "\n" + terminal, "do not carry governing_evidence_id,"},
		"terminal names no refusal": {prior + "\n" + refusalPayload("governing_evidence_id", "refusal_id"), "no earlier refusal record is bound"},
		"record after terminal":     {terminal + "\n" + prior, "do not carry governing_evidence_id,"},
	} {
		p := FromEvents(strings.NewReader(c.log)).PlanAdmissionRefusal
		if p == nil || p.State != runreceipt.Unknown || !strings.Contains(p.Detail, c.want) || p.GoverningEvidenceID != "" {
			t.Errorf("%s: plan_admission_refusal = %+v, want UNKNOWN naming %q with nothing invented", name, p, c.want)
		}
	}

	// A bound record that disagrees with the terminal is a contradiction, not
	// a value to pick.
	clash := refusalEvent("plan.attempt.refused", map[string]string{"plan_attempt_id": `"` + strings.Repeat("5", 64) + `"`})
	p := FromEvents(strings.NewReader(clash + "\n" + terminal)).PlanAdmissionRefusal
	if p == nil || p.State != runreceipt.Malformed || !strings.Contains(p.Detail, "plan_attempt_id differs") {
		t.Errorf("disagreeing bound record: plan_admission_refusal = %+v, want MALFORMED naming the contradiction", p)
	}
	// An UNKNOWN receipt fact is the receipt saying it had none; it binds nothing.
	unknownFact := strings.Replace(receiptFact, `"state":"KNOWN"`, `"state":"UNKNOWN"`, 1)
	p = FromEvents(strings.NewReader(unknownFact + "\n" + terminal)).PlanAdmissionRefusal
	if p == nil || p.State != runreceipt.Unknown {
		t.Errorf("UNKNOWN receipt fact: plan_admission_refusal = %+v, want UNKNOWN", p)
	}

	// A receipt with no authority to speak a v13 refusal completes nothing,
	// however well shaped its fact is, and the rejection is reported.
	for name, c := range map[string]struct{ header, diag string }{
		"no schema":             {`"outcome":"PLAN_ADMISSION_REFUSED",`, "is not a version this reader defines"},
		"unknown schema":        {`"schema":"sensei-code.governed-run-receipt/v99","outcome":"PLAN_ADMISSION_REFUSED",`, "is not a version this reader defines"},
		"v12 schema":            {`"schema":"sensei-code.governed-run-receipt/v12","outcome":"PLAN_ADMISSION_REFUSED",`, "not in the vocabulary"},
		"no outcome":            {`"schema":"sensei-code.governed-run-receipt/v13",`, `outcome "" is not PLAN_ADMISSION_REFUSED`},
		"contradicting outcome": {`"schema":"sensei-code.governed-run-receipt/v13","outcome":"FAILED",`, `outcome "FAILED" is not PLAN_ADMISSION_REFUSED`},
	} {
		rec := FromEvents(strings.NewReader(factOf(c.header, declaration) + "\n" + terminal))
		p := rec.PlanAdmissionRefusal
		if p == nil || p.State != runreceipt.Unknown || p.GoverningEvidenceID != "" || !strings.Contains(p.Detail, "do not carry governing_evidence_id,") {
			t.Errorf("%s: plan_admission_refusal = %+v, want UNKNOWN with nothing taken from the receipt", name, p)
		}
		if diags := strings.Join(rec.Diagnostics, " "); !strings.Contains(diags, c.diag) || !strings.Contains(diags, "plan_admission_refusal is not read") {
			t.Errorf("%s: diagnostics %v, want the rejected receipt reported (%q)", name, rec.Diagnostics, c.diag)
		}
	}

	// A bound record carrying a required field in a shape v13 does not model
	// is evidence the stream did carry: the fact is MALFORMED, not UNKNOWN.
	bad := refusalEvent("plan.attempt.refused", map[string]string{"governing_evidence_id": `7`})
	p = FromEvents(strings.NewReader(bad + "\n" + terminal)).PlanAdmissionRefusal
	if p == nil || p.State != runreceipt.Malformed || !strings.Contains(p.Detail, "governing_evidence_id in event:plan.attempt.refused.payload") ||
		p.GoverningEvidenceID != "" {
		t.Errorf("malformed bound record: plan_admission_refusal = %+v, want MALFORMED naming the field and its source", p)
	}
}

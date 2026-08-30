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

func TestTheRealCorpusNeverCrashesTheReader(t *testing.T) {
	paths := discovered(t)
	for path := range baseline {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		rec := FromEvents(f)
		f.Close()
		for _, fl := range rec.Fields() {
			switch fl.Value.State {
			case runreceipt.Known, runreceipt.Unknown, runreceipt.Malformed, runreceipt.Unsupported:
			default:
				t.Fatalf("%s left %s in state %q", path, fl.Name, fl.Value.State)
			}
		}
		if rec.BaseCommit.State != runreceipt.Known {
			t.Errorf("%s: base commit %s (%s)", path, rec.BaseCommit.State, rec.BaseCommit.Detail)
		}
		// Deliberately NOT logging rec.Outcome. C5 is a VOID witness and its
		// semantic content is inadmissible as evidence about that run; a
		// passing test's output is exactly where an inadmissible fact would
		// acquire a citable home. The corpus proves the reader is total, not
		// what those runs did.
		t.Logf("%s -> base=%s attempts=%d diagnostics=%d", path, rec.BaseCommit.Text, len(rec.Attempts), len(rec.Diagnostics))
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
		"plan.proposed", "workflow.completed", "wholly.unknown.kind", ""}
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
	if rec.Attempts[0].Provider.Text != "codex" || rec.Attempts[0].Delivered {
		t.Errorf("first attempt = %+v, want codex undelivered", rec.Attempts[0])
	}
	if rec.Attempts[1].Provider.Text != "gemini" || !rec.Attempts[1].Delivered {
		t.Errorf("second attempt = %+v, want gemini delivered", rec.Attempts[1])
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

package derived

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// §6.3 requires a receipt per inference RUN. A run that proposed a duplicate,
// or proposed nothing, still ran -- and those are the recurrence and decline
// signals a later ranking model would need.
func TestEveryRunGetsAReceiptIncludingTheOnesThatProducedNothing(t *testing.T) {
	p := filepath.Join(t.TempDir(), "receipts.jsonl")
	for _, o := range []InferenceOutcome{
		OutcomeRecorded, OutcomeDuplicate, OutcomeRefused, OutcomeNoProposal,
	} {
		if err := AppendReceipt(p, InferenceReceipt{
			ModelName: "chatgpt", OriginTask: "t", OriginGap: "no anchored rules apply", Outcome: o,
		}); err != nil {
			t.Fatalf("%s: %v", o, err)
		}
	}
	got, err := LoadReceipts(p)
	if err != nil || len(got) != 4 {
		t.Fatalf("expected 4 receipts, got %d (%v)", len(got), err)
	}
	seen := map[InferenceOutcome]bool{}
	for _, r := range got {
		seen[r.Outcome] = true
	}
	for _, o := range []InferenceOutcome{OutcomeRecorded, OutcomeDuplicate, OutcomeRefused, OutcomeNoProposal} {
		if !seen[o] {
			t.Fatalf("%s produced no receipt; that outcome becomes invisible to any later analysis", o)
		}
	}
}

// A receipt that implies reproducibility this run does not have is worse than
// none. §6.3 asks for "any nondeterminism declaration"; a hosted LLM has one.
func TestNondeterminismIsDeclaredRatherThanLeftBlank(t *testing.T) {
	p := filepath.Join(t.TempDir(), "receipts.jsonl")
	if err := AppendReceipt(p, InferenceReceipt{ModelName: "chatgpt", Outcome: OutcomeRecorded}); err != nil {
		t.Fatal(err)
	}
	got, _ := LoadReceipts(p)
	if got[0].Nondeterminism == "" {
		t.Fatal("a blank nondeterminism field implies a reproducibility this run does not have")
	}
	for _, want := range []string{"nondeterministic", "not addressable", "no model artifact digest"} {
		if !strings.Contains(got[0].Nondeterminism, want) {
			t.Fatalf("the declaration does not state %q: %s", want, got[0].Nondeterminism)
		}
	}
	if got[0].ModelArtifact != "" {
		t.Fatal("an artifact digest was invented for a hosted model")
	}
	if got[0].FeatureVersion == "" || got[0].PostProcessing == "" {
		t.Fatal("feature-extractor and post-processing versions must be stamped, or two " +
			"incomparable outcomes look like one")
	}
}

// Append-only. A model update must never silently rewrite candidate history.
func TestReceiptsAreAppendOnlyAndNeverDeduplicated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "receipts.jsonl")
	same := InferenceReceipt{ModelName: "chatgpt", OriginTask: "t",
		CandidateDigest: "sha256:aa", Outcome: OutcomeDuplicate}
	for i := 0; i < 3; i++ {
		if err := AppendReceipt(p, same); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := LoadReceipts(p)
	if len(got) != 3 {
		t.Fatalf("three runs of one configuration collapsed to %d receipts; recurrence is the "+
			"signal here and deduplicating destroys it", len(got))
	}
	b, _ := os.ReadFile(p)
	if strings.Count(strings.TrimSpace(string(b)), "\n") != 2 {
		t.Fatal("the log is not one receipt per line")
	}
}

// The digest identifies WHAT WAS ASKED, so a reworded question is the same
// candidate and a different question is not.
func TestTheCandidateDigestIdentifiesTheQuestionNotItsProse(t *testing.T) {
	a := Recipe{Kind: "field_access_under_lock", Dir: "internal/event",
		Type: "Bus", Field: "subs", Lock: "mu", Why: "one investigation"}
	b := a
	b.Why = "a different investigation, phrased differently"
	b.Dir = "internal/event/"
	if DigestOf(a) != DigestOf(b) {
		t.Fatal("rewording a question changed its digest; recurrence would read as novelty")
	}
	c := a
	c.Field = "somethingElse"
	if DigestOf(a) == DigestOf(c) {
		t.Fatal("two different questions share a digest")
	}
}

// Two runs against different graph states must be distinguishable.
func TestTheGraphDigestSeparatesRunsTakenAgainstDifferentStates(t *testing.T) {
	one := GraphDigest(map[string]string{"graph_build_commit": "aaa", "seed": "CURRENT"})
	same := GraphDigest(map[string]string{"seed": "CURRENT", "graph_build_commit": "aaa"})
	other := GraphDigest(map[string]string{"graph_build_commit": "bbb", "seed": "CURRENT"})
	if one != same {
		t.Fatal("the graph digest depends on map iteration order and is not reproducible")
	}
	if one == other {
		t.Fatal("two different graph states produced the same digest; runs taken against " +
			"different worlds would look comparable")
	}
}

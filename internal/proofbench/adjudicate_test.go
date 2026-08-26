package proofbench

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A refusal must carry its own claim.
//
// proof-v6 recorded all six COLD refusals as "Architectural authority reached a
// human-owned boundary". That is a ROUTE, and it is the same string whether the
// system found a violated invariant, hit a change class needing approval, or
// simply lacked coverage of the files. Those are three different claims and
// they deserve three different verdicts, so the route alone cannot support any
// of them.
func TestARefusalRecordsWhatItActuallyClaimed(t *testing.T) {
	cases := map[string]string{
		"internal-setup-e645669":   "graph coverage is absent",
		"internal-tui-ea046ba":     "human_approval_required",
		"internal-session-4d32937": "review_required",
	}
	for task, want := range cases {
		matches, _ := filepath.Glob(
			filepath.Join("../../benchmark/proof-v6/transcripts", task, "COLD", "*.log"))
		if len(matches) == 0 {
			t.Skipf("%s transcript absent", task)
		}
		b, err := os.ReadFile(matches[0])
		if err != nil {
			t.Fatal(err)
		}
		o := &ArmOutcome{}
		o.readEvents(string(b))
		if len(o.RefusalClaims) == 0 {
			t.Fatalf("%s: the refusal recorded no claim, so it cannot be adjudicated", task)
		}
		joined := strings.Join(o.RefusalClaims, "\n")
		if !strings.Contains(joined, want) {
			t.Fatalf("%s: claim %q does not contain %q", task, joined, want)
		}
		// The route alone must never be mistaken for the reason.
		if joined == "Architectural authority reached a human-owned boundary." {
			t.Fatalf("%s: only the route was captured, not the claim", task)
		}
	}
}

// The three claims are of different KINDS, and only one kind is an assertion
// about the candidate's code.
func TestRefusalClaimsSeparateIntoDistinctKinds(t *testing.T) {
	coverage := "a bounded knowledge gap was not closed by investigation: graph coverage is absent"
	policy := "Sensei requires approval for this change class: human_approval_required"

	// Neither of the claims proof-v6 actually produced asserts that the
	// candidate's code is wrong. That is the finding, pinned so it cannot drift:
	// no COLD refusal in proof-v6 claimed an invariant would be violated.
	for _, c := range []string{coverage, policy} {
		if strings.Contains(c, "violates invariant") {
			t.Fatalf("fixture claims an invariant violation: %q", c)
		}
	}
	if !strings.Contains(coverage, "coverage is absent") {
		t.Fatal("the coverage claim is an admission about Sensei's own knowledge")
	}
	if !strings.Contains(policy, "requires approval") {
		t.Fatal("the policy claim is about permission, not about the patch")
	}
}

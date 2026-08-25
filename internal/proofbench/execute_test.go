package proofbench

// Attacks on the execution layer.
//
// The scoring rules are only as good as what the runner records, so these pin
// the two places a run can be misread: an ordinary failure mistaken for
// infrastructure (which would license a retry it should not), and a human
// authority decision mistaken for a human supplying the answer (which would
// make the autonomy number meaningless in either direction).

import (
	"strings"
	"testing"
)

// An ordinary failure is not an infrastructure failure.
//
// Infrastructure is the ONE thing that licenses a retry. Treating every
// non-zero exit as infrastructure would turn the retry rule into a licence to
// re-roll until a run happened to pass.
func TestOnlyRecognisedProviderFailuresLicenseARetry(t *testing.T) {
	for _, ordinary := range []string{
		"no bounded implementor produced an acceptable candidate: claude exited 1",
		"cannot establish authority for this plan: preflight degraded",
		"the human declined this architectural change",
		"candidate did not converge after 3 review cycles",
		"FAIL github.com/globulario/sensei-code/internal/gitx",
		// From the FIRST real campaign run. The hash contains "403", and a bare
		// three-digit match classified a governed failure as infrastructure --
		// licensing a retry the rule exists to forbid. A stream full of hashes,
		// counts and timestamps matches any three digits constantly.
		`{"certified_awareness_graph_commit":"a4034c78de600ad14f388343224492a5d722459c"}`,
		`{"live_store_graph_triple_count":292667,"graph_build_time_unix":1787243065}`,
		"no bounded implementor: candidate 401k lines is not a status code",
	} {
		if why := infrastructureReason(ordinary); why != "" {
			t.Errorf("a semantic failure was classified as infrastructure (%q): %q", why, ordinary)
		}
	}
	for _, infra := range []string{
		"You've hit your usage limit. Upgrade to Pro",
		"thread/fork: rpc -32600: no rollout found for thread id 01a0",
		"error: connection refused",
		"HTTP 503 service unavailable",
		"authentication failed",
		"the provider returned status 429",
		"403 Forbidden",
	} {
		if infrastructureReason(infra) == "" {
			t.Errorf("an externally attributable failure was not recognised: %q", infra)
		}
	}
}

// A human authority decision is not a human supplying the technical answer.
//
// The autonomy claim rests entirely on this distinction: a human choosing
// whether to land a retained candidate does not invalidate autonomy, and a
// human telling the system what the fix is does.
func TestAnAuthorityQuestionIsNotATechnicalAnswer(t *testing.T) {
	var o ArmOutcome
	o.readEvents(strings.Join([]string{
		`{"kind":"authority.required","summary":"Sensei requires approval for this change class","time":"t1"}`,
		`{"kind":"review.started","summary":"cycle 1"}`,
		`{"kind":"review.finding","summary":"major: the report overstates two conclusions"}`,
	}, "\n"))

	if o.AuthorityAsks != 1 {
		t.Fatalf("authority question not counted: %d", o.AuthorityAsks)
	}
	if len(o.Interventions) != 1 {
		t.Fatalf("interventions: %d", len(o.Interventions))
	}
	if o.Interventions[0].TechnicalAnswer() {
		t.Error("an approval prompt was recorded as a human supplying the technical answer; the " +
			"autonomy rate would then measure how chatty the gate is")
	}
	// And the attempt is still autonomous-correct if the oracle passes.
	a := Attempt{Verdict: Correct, GovernedCheckoutClean: true, Interventions: o.Interventions}
	if !a.AutonomousCorrect() {
		t.Error("a run whose only human contact was an authority decision was scored non-autonomous")
	}
}

// The same objection reworded is recognised as a repeat.
//
// This is the #76 endpoint: repeated prose changes to the same unresolved
// objection are not progress, and a loop that produces them is the #62 shape.
func TestARewordedObjectionIsARepeat(t *testing.T) {
	var o ArmOutcome
	o.readEvents(strings.Join([]string{
		`{"kind":"review.started"}`,
		`{"kind":"review.finding","summary":"major: the report overstates two conclusions about consumers"}`,
		`{"kind":"review.started"}`,
		`{"kind":"review.finding","summary":"major: the report overstates two conclusions -- see section 3"}`,
		`{"kind":"review.started"}`,
		`{"kind":"review.finding","summary":"major: the report overstates two conclusions, still"}`,
		`{"kind":"review.finding","summary":"minor: a different objection entirely about flags"}`,
	}, "\n"))

	if len(o.Objections) != 4 {
		t.Fatalf("objections: %d", len(o.Objections))
	}
	if o.Objections[1].Status != "repeated" {
		t.Errorf("a reworded restatement of the same objection was counted as progress: %+v", o.Objections[1])
	}
	if o.Objections[3].Status != "new" {
		t.Errorf("a genuinely different objection was merged into an earlier one: %+v", o.Objections[3])
	}
	// Three cycles with TWO repeats and no outside-authority blocker is the
	// non-convergent shape. Two rather than one on purpose: this metric feeds a
	// pre-registered RED condition, and a gate that can condemn the product
	// must not fire on a single restatement.
	if !nonConvergent(Attempt{ReviewCycles: o.ReviewCycles, Objections: o.Objections}) {
		t.Error("a loop repeating the same objection across cycles was not scored non-convergent")
	}
}

// An unconfigured RAW arm records why, rather than comparing against nothing.
func TestAnUnconfiguredRawArmIsNotABaseline(t *testing.T) {
	r := Runner{}
	out := r.ExecuteRaw(nil, "", "do the thing", 0)
	if out.Terminal != "raw.not_configured" {
		t.Fatalf("terminal %q", out.Terminal)
	}
	if out.Infrastructure == "" {
		t.Error("an unmeasurable baseline was recorded without saying so")
	}
	// It must not read as a governed win by default: NO_RESULT excludes it from
	// correctness while keeping it visible.
	a := Attempt{Verdict: NoResult, Infrastructure: out.Infrastructure}
	if a.Eligible() {
		t.Error("an arm that never ran counted toward a correctness rate")
	}
}

// Governed exit codes are read from the documented contract.
func TestGovernedExitCodesMatchTheDocumentedContract(t *testing.T) {
	for code, want := range map[int]string{
		0: "workflow.completed", 1: "workflow.failed", 3: "workflow.awaiting_authority",
		4: "workflow.stopped", 5: "workflow.timed_out", 6: "workflow.observed",
	} {
		if got := governedExit(code); got != want {
			t.Errorf("exit %d = %q, want %q", code, got, want)
		}
	}
	// An unrecognised code is named, not silently mapped onto success.
	if got := governedExit(99); !strings.Contains(got, "99") || got == "workflow.completed" {
		t.Errorf("an unknown exit code became %q", got)
	}
}

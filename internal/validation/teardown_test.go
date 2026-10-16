//go:build linux

package validation

// N5 P1 witnesses (RULING-358 §6-§11, §18 A-J and R): the broker's typed
// teardown statement, decoded only from the execution mode and the verified
// status record, and the producer-level stop at the first execution whose
// teardown is unresolved. Fixture launchers; no real D2 executor runs here.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// refusingLauncherWith writes one refusal record with exactly this reason.
func refusingLauncherWith(reason string) string {
	return "#!/bin/sh\nread n <&5\nexec 5<&-\necho \"" + StatusProtocol + " $n refused " + reason + "\" >&4\nexit 0\n"
}

// alwaysErring reports an ended context without ever being Done: the broker
// reads the record exactly as it would after a cancellation that arrived
// once the record was already written.
type alwaysErring struct{ context.Context }

func (alwaysErring) Err() error { return context.DeadlineExceeded }

func exitCheck(code string) Check {
	return Check{Kind: Test, Command: "sh", Args: []string{"-c", "exit " + code}}
}

// A. A valid, nonce-bound record consistent with the wrapper's exit proves
// the teardown, whatever the command's exit.
func TestAValidRanRecordProvesTeardown(t *testing.T) {
	for code, want := range map[string]Outcome{"0": Passed, "3": Failed} {
		e := confinedRun(t, wrapper(t, passThrough), t.TempDir(), exitCheck(code))
		if e.Outcome != want || e.Teardown != TeardownProven || e.Unsettled() {
			t.Fatalf("exit %s: a valid record did not prove the teardown: %+v", code, e)
		}
	}
}

// B. A refusal the pinned launcher makes before anything starts, and every
// refusal the broker itself makes before starting the launcher, acquired no
// workspace authority: not applicable, never proven, never unsettled.
func TestAPreStartRefusalIsNotApplicable(t *testing.T) {
	for _, reason := range []string{"usage", "manifest-digest", "bwrap-digest", "bad-workspace", "run-dir-exists", "profile-mismatch", "subreaper"} {
		e := confinedRun(t, wrapper(t, refusingLauncherWith(reason)), t.TempDir(), exitCheck("0"))
		if !refused(e) || e.Teardown != TeardownNotApplicable || e.Unsettled() {
			t.Fatalf("%s: a listed pre-start refusal was not read as not applicable: %+v", reason, e)
		}
	}
	bad := wrapper(t, passThrough)
	bad.SHA256 = strings.Repeat("0", 64)
	for name, r := range map[string]Runner{
		"wrong digest":  {Workspace: t.TempDir(), Confine: bad},
		"refusing":      {Workspace: t.TempDir(), Refusal: "no profile"},
		"not permitted": {Workspace: t.TempDir(), Confine: wrapper(t, passThrough), Permits: func(CheckKind) (bool, string) { return false, "no" }},
	} {
		e := r.Run(context.Background(), "task-1", "sha256:abc", []Check{exitCheck("0")}).Checks[0]
		if e.Teardown != TeardownNotApplicable || e.Unsettled() {
			t.Fatalf("%s: a refusal before any start was not read as not applicable: %+v", name, e)
		}
	}
}

// C. No refusal reason is read as a proven teardown: the launcher's
// post-start reasons -- including one it emits only after its own teardown
// proof -- are unresolved until a reviewed post-cleanup classification exists
// (RULING-358 §9).
func TestNoRefusalReasonIsProvenWithoutItsProof(t *testing.T) {
	for _, reason := range []string{"sandbox-status-disagreement", "sandbox-did-not-start", "sandbox-identity"} {
		e := confinedRun(t, wrapper(t, refusingLauncherWith(reason)), t.TempDir(), exitCheck("0"))
		if e.Teardown != TeardownUnresolved || !e.Unsettled() {
			t.Fatalf("%s: a post-start refusal was not unresolved: %+v", reason, e)
		}
	}
}

// D and E. The launcher's own teardown-unresolved, and any reason outside the
// closed vocabulary -- free text, a known token with words after it, another
// case, no reason -- are unresolved.
func TestTeardownUnresolvedAndUnknownReasonsAreUnresolved(t *testing.T) {
	for _, reason := range []string{"teardown-unresolved", "no D2 executor for this check", "bad-workspace and more", "BAD-WORKSPACE", ""} {
		e := confinedRun(t, wrapper(t, refusingLauncherWith(reason)), t.TempDir(), exitCheck("0"))
		if !refused(e) || e.Teardown != TeardownUnresolved || !e.Unsettled() {
			t.Fatalf("%q: was not unresolved: %+v", reason, e)
		}
	}
}

// F. A launcher that may have started the command and wrote no record leaves
// the teardown unresolved.
func TestNoStatusAfterAPossibleStartIsUnresolved(t *testing.T) {
	work := t.TempDir()
	e := confinedRun(t, wrapper(t, silentPassThrough), work, touch(filepath.Join(work, "ran")))
	if !refused(e) || e.Teardown != TeardownUnresolved || !e.Unsettled() || !ran(filepath.Join(work, "ran")) {
		t.Fatalf("a record-less execution was not unresolved: %+v", e)
	}
}

// G. An invocation cancelled before any record exists is unresolved.
func TestCancellationWithoutACompletionProofIsUnresolved(t *testing.T) {
	slow := "#!/bin/sh\nread n <&5\nexec 5<&-\nsleep 5\necho \"" + StatusProtocol + " $n ran 0\" >&4\n"
	x, _, _ := boundedExecute(t, wrapper(t, slow), t.TempDir(), exitCheck("0"), 300*time.Millisecond, nil)
	if x.err == nil || !strings.Contains(x.err.Error(), "did not finish") || x.teardown != TeardownUnresolved {
		t.Fatalf("a cancelled invocation without a record was not unresolved: %+v", x)
	}
	e := Runner{Workspace: t.TempDir(), Confine: wrapper(t, silentPassThrough)}.Run(alwaysErring{context.Background()}, "task-1", "sha256:abc", []Check{exitCheck("0")}).Checks[0]
	if e.Teardown != TeardownUnresolved || !strings.Contains(e.Detail, "did not finish") {
		t.Fatalf("an ended context with no record was not unresolved: %+v", e)
	}
}

// H. A completion proof the launcher already wrote survives the context
// ending afterwards: the record is the proof, not the context.
func TestACompletionProofSurvivesALaterCancellation(t *testing.T) {
	e := Runner{Workspace: t.TempDir(), Confine: wrapper(t, passThrough)}.Run(alwaysErring{context.Background()}, "task-1", "sha256:abc", []Check{exitCheck("0")}).Checks[0]
	if e.Outcome != Passed || e.Teardown != TeardownProven || e.Unsettled() {
		t.Fatalf("a written completion proof was discarded by the ended context: %+v", e)
	}
}

// I. No later check starts on a workspace an earlier execution may still be
// writing, and none is recorded as if it had run.
func TestNoCheckRunsAfterAnUnresolvedOne(t *testing.T) {
	work := t.TempDir()
	first, second := filepath.Join(work, "first"), filepath.Join(work, "second")
	b := Runner{Workspace: work, Confine: wrapper(t, silentPassThrough)}.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(first), touch(second)})
	if len(b.Checks) != 1 || !b.Checks[0].Unsettled() || len(b.Unsettled()) != 1 || ran(second) || !ran(first) {
		t.Fatalf("a check ran or was recorded after an unresolved execution: %+v", b)
	}
	// Control: with a proving launcher both run.
	work2 := t.TempDir()
	b = Runner{Workspace: work2, Confine: wrapper(t, passThrough)}.Run(context.Background(), "task-1", "sha256:abc",
		[]Check{touch(filepath.Join(work2, "first")), touch(filepath.Join(work2, "second"))})
	if len(b.Checks) != 2 || len(b.Unsettled()) != 0 || !ran(filepath.Join(work2, "second")) {
		t.Fatalf("control: proven executions did not both run: %+v", b)
	}
}

// J. No later named required test starts after an unresolved one.
func TestNoRequiredTestRunsAfterAnUnresolvedOne(t *testing.T) {
	r := Runner{Workspace: t.TempDir(), Confine: wrapper(t, silentPassThrough)}
	tests := r.RunRequiredTests(context.Background(), "task-1", "sha256:abc", []string{"pkg/a_test.go:TestA", "pkg/b_test.go:TestB"})
	if len(tests) != 1 || !tests[0].Evidence.Unsettled() || tests[0].Executed {
		t.Fatalf("a named test ran or was recorded after an unresolved one: %+v", tests)
	}
	r = Runner{Workspace: t.TempDir(), Confine: wrapper(t, refusingLauncherWith("bad-workspace"))}
	tests = r.RunRequiredTests(context.Background(), "task-1", "sha256:abc", []string{"pkg/a_test.go:TestA", "pkg/b_test.go:TestB"})
	if len(tests) != 2 {
		t.Fatalf("control: pre-start refusals stopped the sequence: %+v", tests)
	}
}

// K (broker side). A baseline execution whose teardown is unresolved marks
// the record unsettled even when the candidate's own execution was proven,
// and is not collapsed into an attribution.
func TestAnUnresolvedBaselineMarksTheRecordUnsettled(t *testing.T) {
	launcher := "#!/bin/sh\nread n <&5\nexec 5<&-\nif [ \"$SENSEI_CONFINE_ROLE\" = baseline ]; then exit 0; fi\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&-\nrc=$?\necho \"" + StatusProtocol + " $n ran $rc\" >&4\nexit $rc\n"
	base := t.TempDir()
	e := Runner{Workspace: t.TempDir(), Confine: wrapper(t, launcher), Baseline: func() (string, error) { return base, nil }}.
		Run(context.Background(), "task-1", "sha256:abc", []Check{exitCheck("1")}).Checks[0]
	if e.Teardown != TeardownProven || e.BaselineTeardown != TeardownUnresolved || !e.Unsettled() || e.Attribution == "candidate" || e.Attribution == "pre-existing" {
		t.Fatalf("an unresolved baseline was not carried as unsettled: %+v", e)
	}
}

// R. The unconfined broker and the legacy (N3) convention the configuration
// selects make no teardown claim and are never unsettled.
func TestUnconfinedAndLegacyExecutionsAreNotApplicable(t *testing.T) {
	e := Runner{Workspace: t.TempDir()}.Run(context.Background(), "task-1", "sha256:abc", []Check{exitCheck("0")}).Checks[0]
	if e.Teardown != TeardownNotApplicable || e.Unsettled() || e.Outcome != Passed {
		t.Fatalf("the unconfined broker changed: %+v", e)
	}
	legacy := wrapper(t, silentPassThrough)
	legacy.RequireStatus = false
	e = Runner{Workspace: t.TempDir(), Confine: legacy}.Run(context.Background(), "task-1", "sha256:abc", []Check{exitCheck("0")}).Checks[0]
	if e.Teardown != TeardownNotApplicable || e.Unsettled() || e.Outcome != Passed {
		t.Fatalf("the legacy convention changed: %+v", e)
	}
}

// A record from a required-status executor that carries no teardown
// statement (an older record, or one written by anything but this broker) is
// unsettled: the missing statement fails closed.
func TestAMissingStatementFromARequiredStatusExecutorIsUnsettled(t *testing.T) {
	e := Evidence{ExecutedBy: "sensei-code execution broker under confinement profile p via /x (sha256:y) with launcher status " + StatusProtocol}
	if !e.Unsettled() {
		t.Fatal("a missing teardown statement from a required-status executor was not unsettled")
	}
	if (Evidence{ExecutedBy: "sensei-code execution broker"}).Unsettled() {
		t.Fatal("an unconfined record without a statement was unsettled")
	}
}

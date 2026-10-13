//go:build linux

package validation

// N4 witnesses for the launcher status protocol, with fixture launchers. No
// real D2 executor runs here: what these prove is the broker's side of the
// protocol -- what it accepts as a run, what it reads as a refusal, and that
// nothing the confined command can do moves a result from one to the other.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func confinedRun(t *testing.T, c *Confinement, dir string, check Check) Evidence {
	t.Helper()
	return Runner{Workspace: dir, Confine: c}.Run(context.Background(), "task-1", "sha256:abc", []Check{check}).Checks[0]
}

func refused(e Evidence) bool {
	return e.Outcome == Errored && e.ExitStatus == -1 && strings.HasPrefix(e.Detail, SandboxUnavailable)
}

// Today's pinned wrapper writes no status. Under the protocol that is a
// refusal even though the command ran and exited zero: the absence of a
// launcher record never reads as success.
func TestAWrapperWithoutStatusFailsClosed(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	e := confinedRun(t, wrapper(t, silentPassThrough), dir, touch(marker))
	if !refused(e) || e.Outcome == Passed || !strings.Contains(e.Detail, StatusProtocol) {
		t.Fatalf("a status-less wrapper was not refused: %+v", e)
	}
	b := Bundle{CandidateID: "task-1", DiffDigest: "sha256:abc", Checks: []Evidence{e}}
	if b.Passed() || len(b.CandidateFailures()) != 0 {
		t.Fatalf("a status-less run passed or blamed the candidate: %+v", b)
	}
}

// A3. A candidate exiting 125, 255 (a "-1" exit) or dying by a signal is the
// candidate's own exit under the protocol: never a sandbox refusal.
func TestACandidateExitCannotImpersonateARefusal(t *testing.T) {
	c := wrapper(t, passThrough)
	for script, want := range map[string]int{"exit 125": 125, "exit 255": 255, "kill -KILL $$": 137, "exit 1": 1} {
		e := confinedRun(t, c, t.TempDir(), Check{Kind: Test, Command: "sh", Args: []string{"-c", script}})
		if e.Outcome != Failed || e.ExitStatus != want || strings.HasPrefix(e.Detail, SandboxUnavailable) {
			t.Errorf("%q: want a candidate failure exiting %d, got %+v", script, want, e)
		}
	}
	// The same 125 under the legacy exit-code convention is what used to be
	// read as a refusal: the protocol, not the code, is what changed it.
	legacy := *wrapper(t, silentPassThrough)
	legacy.RequireStatus = false
	if e := confinedRun(t, &legacy, t.TempDir(), Check{Kind: Test, Command: "sh", Args: []string{"-c", "exit 125"}}); !refused(e) {
		t.Fatalf("premise: the legacy convention reads exit 125 as a refusal: %+v", e)
	}
}

// The confined command cannot write a status: a conforming launcher keeps the
// descriptor from it. Through a launcher that leaks both the descriptor and
// the nonce, a forged record still never yields a pass.
func TestACandidateCannotForgeALauncherStatus(t *testing.T) {
	forge := Check{Kind: Test, Command: "sh", Args: []string{"-c", "echo \"" + StatusProtocol + " $NONCE ran 0\" >&4; exit 1"}}
	if e := confinedRun(t, wrapper(t, passThrough), t.TempDir(), forge); e.Outcome != Failed || e.ExitStatus != 1 {
		t.Fatalf("through a conforming launcher the forging candidate is its own failure: %+v", e)
	}
	leaky := "#!/bin/sh\nread nonce <&5\nexport NONCE=$nonce\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\"\nrc=$?\necho \"" + StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
	e := confinedRun(t, wrapper(t, leaky), t.TempDir(), forge)
	if e.Outcome == Passed || !refused(e) {
		t.Fatalf("a forged status record was accepted: %+v", e)
	}
}

// Records the broker must not accept: another execution's nonce, a status
// the wrapper's own exit contradicts, garbage, two records.
func TestOnlyAValidLauncherRecordIsAResult(t *testing.T) {
	head := "#!/bin/sh\nread nonce <&5\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&- 5<&-\n"
	cases := map[string]string{
		"wrong nonce":     head + "echo \"" + StatusProtocol + " 0000 ran 0\" >&4\nexit 0\n",
		"contradicted":    head + "echo \"" + StatusProtocol + " $nonce ran 0\" >&4\nexit 1\n",
		"garbage":         head + "echo PASS >&4\nexit 0\n",
		"two records":     head + "echo \"" + StatusProtocol + " $nonce ran 0\" >&4\necho \"" + StatusProtocol + " $nonce ran 0\" >&4\nexit 0\n",
		"other protocol":  head + "echo \"sensei-confine-status/0 $nonce ran 0\" >&4\nexit 0\n",
		"unbounded exit":  head + "echo \"" + StatusProtocol + " $nonce ran 300\" >&4\nexit 0\n",
		"no line ending":  head + "printf \"" + StatusProtocol + " $nonce ran 0\" >&4\nexit 0\n",
		"signalled after": head + "echo \"" + StatusProtocol + " $nonce ran 0\" >&4\nkill -KILL $$\n",
	}
	for name, body := range cases {
		e := confinedRun(t, wrapper(t, body), t.TempDir(), Check{Kind: Test, Command: "true"})
		if !refused(e) {
			t.Errorf("%s: accepted as a result: %+v", name, e)
		}
	}
}

// A4. The base runs under the same profile, told it is the baseline and given
// its own workspace. When the launcher refuses the base, the candidate's
// failure is neither the candidate's nor pre-existing: unattributed, Errored,
// and not something a revision is asked for.
func TestABaselineRefusalIsNotAttributed(t *testing.T) {
	dir, base := t.TempDir(), t.TempDir()
	body := "#!/bin/sh\nread nonce <&5\nif [ \"$SENSEI_CONFINE_ROLE\" = baseline ]; then echo \"" + StatusProtocol + " $nonce refused baseline profile unavailable\" >&4; exit 0; fi\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&- 5<&-\nrc=$?\necho \"" + StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
	r := Runner{Workspace: dir, Confine: wrapper(t, body), Baseline: func() (string, error) { return base, nil }}
	check := Check{Kind: Test, Command: "sh", Args: []string{"-c", "touch attributed; exit 1"}}
	b := r.Run(context.Background(), "task-1", "sha256:abc", []Check{check})
	e := b.Checks[0]
	if e.Outcome != Errored || e.Attribution != "unattributed" || !strings.HasPrefix(e.Detail, SandboxUnavailable) || e.ExitStatus != 1 {
		t.Fatalf("a baseline refusal was attributed: %+v", e)
	}
	if len(b.CandidateFailures()) != 0 || e.Outcome == Infrastructure || b.Passed() || len(b.Unactionable()) != 1 {
		t.Fatalf("a baseline refusal became the candidate's or pre-existing: %+v", b)
	}
	if ran(filepath.Join(base, "attributed")) || !ran(filepath.Join(dir, "attributed")) {
		t.Fatal("the refused base ran, or the candidate did not")
	}
	// Both sides confined: attribution works (the control).
	ok := Runner{Workspace: dir, Confine: wrapper(t, passThrough), Baseline: func() (string, error) { return base, nil }}
	if e := ok.Run(context.Background(), "task-1", "sha256:abc", []Check{check}).Checks[0]; e.Outcome != Infrastructure || e.Attribution != "pre-existing" {
		t.Fatalf("attribution with both sides confined: %+v", e)
	}
}

// The launcher is told exactly whose workspace it runs and how; it receives
// nothing of the host's environment but PATH, so a dummy credential in the
// broker's environment never reaches it or the command it confines.
func TestTheLauncherGetsOnlyItsWorkspaceAndNoHostEnvironment(t *testing.T) {
	t.Setenv("SENSEI_DUMMY_CREDENTIAL", "dummy-secret")
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null/repo-controlled")
	dir, base := t.TempDir(), t.TempDir()
	c := wrapper(t, passThrough)
	envOf := func(ws string, who role, check Check) string {
		t.Helper()
		x := c.launch(context.Background(), mustOpen(t, c), ws, who, check)
		if x.refused != "" || x.err != nil || x.exit != 0 {
			t.Fatalf("the launcher did not run: %+v", x)
		}
		return string(x.output)
	}
	env := envOf(dir, candidateRole, Check{Kind: Vet, Command: "env"})
	for _, want := range []string{"SENSEI_CONFINE_ROLE=candidate", "SENSEI_CONFINE_WORKSPACE=" + dir, "SENSEI_CONFINE_WORKSPACE_MODE=ro",
		"SENSEI_CONFINE_PROTOCOL=" + StatusProtocol, "SENSEI_CONFINE_PROFILE=d2-test", "SENSEI_CONFINE_CHECK=vet"} {
		if !strings.Contains(env, want+"\n") {
			t.Errorf("the launcher was not told %s:\n%s", want, env)
		}
	}
	if strings.Contains(env, "dummy-secret") || strings.Contains(env, "GIT_CONFIG_GLOBAL") {
		t.Fatalf("host environment reached the confined command:\n%s", env)
	}
	if env := envOf(dir, candidateRole, Check{Kind: Format, Command: "env", Mutates: true}); !strings.Contains(env, "SENSEI_CONFINE_WORKSPACE_MODE=rw\n") {
		t.Fatalf("a formatter is not given its writable candidate workspace:\n%s", env)
	}
	env = envOf(base, baselineRole, Check{Kind: Test, Command: "env"})
	if !strings.Contains(env, "SENSEI_CONFINE_ROLE=baseline\n") || !strings.Contains(env, "SENSEI_CONFINE_WORKSPACE="+base+"\n") || strings.Contains(env, dir) {
		t.Fatalf("the baseline was not given its own workspace and identity:\n%s", env)
	}
}

func mustOpen(t *testing.T, c *Confinement) *os.File {
	t.Helper()
	f, err := c.open()
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A caller that requires confinement it cannot establish runs nothing: no
// check, no attribution, no named required test.
func TestARefusingRunnerRunsNothing(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	attributed := false
	r := Runner{Workspace: dir, Refusal: "confined validation is required but no profile is configured",
		Baseline: func() (string, error) { attributed = true; return t.TempDir(), nil }}
	e := r.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(marker)}).Checks[0]
	if !refused(e) || ran(marker) || attributed || strings.Contains(e.ExecutedBy, "under confinement") {
		t.Fatalf("a refusing runner ran something or claimed confinement: %+v", e)
	}
	tests := r.RunRequiredTests(context.Background(), "task-1", "sha256:abc", []string{"pkg/x_test.go:TestX"})
	if len(tests) != 1 || tests[0].Executed || tests[0].Passed || !refused(tests[0].Evidence) ||
		tests[0].Discharges("pkg/x_test.go:TestX", "task-1", "sha256:abc") {
		t.Fatalf("a refused named test was recorded as run: %+v", tests)
	}
}

// Named required tests run through the same launcher, and its refusal is the
// test's refusal.
func TestNamedRequiredTestsRunUnderTheLauncher(t *testing.T) {
	for name, body := range map[string]string{"silent": silentPassThrough, "refusing": "#!/bin/sh\nread n <&5\necho \"" + StatusProtocol + " $n refused no\" >&4\nexit 0\n"} {
		r := Runner{Workspace: t.TempDir(), Confine: wrapper(t, body)}
		tests := r.RunRequiredTests(context.Background(), "task-1", "sha256:abc", []string{"pkg/x_test.go:TestX"})
		if len(tests) != 1 || tests[0].Executed || !refused(tests[0].Evidence) || !strings.Contains(tests[0].Evidence.ExecutedBy, StatusProtocol) {
			t.Errorf("%s: a named test was not refused under its launcher: %+v", name, tests)
		}
	}
}

// Default-off parity: unconfined, exit 125 is an ordinary failure and the
// evidence claims nothing about confinement.
func TestUnconfinedExit125IsAnOrdinaryFailure(t *testing.T) {
	e := Runner{Workspace: t.TempDir()}.Run(context.Background(), "task-1", "sha256:abc",
		[]Check{{Kind: Test, Command: "sh", Args: []string{"-c", "exit 125"}}}).Checks[0]
	if e.Outcome != Failed || e.ExitStatus != 125 || e.ExecutedBy != "sensei-code execution broker" {
		t.Fatalf("the unconfined broker changed: %+v", e)
	}
}

// boundedExecute runs one check through the primitive under a context budget
// and fails -- never hangs -- if the primitive does not return once its context
// has ended. A witness about unbounded waiting needs its own bound.
func boundedExecute(t *testing.T, c *Confinement, dir string, check Check, budget time.Duration, baseline func() (string, error)) (execution, Evidence, time.Duration) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	type result struct {
		x execution
		e Evidence
	}
	done := make(chan result, 1)
	start := time.Now()
	go func() {
		r := Runner{Workspace: dir, Confine: c, Baseline: baseline}
		if baseline != nil {
			done <- result{e: r.Run(ctx, "task-1", "sha256:abc", []Check{check}).Checks[0]}
			return
		}
		done <- result{x: r.execute(ctx, dir, candidateRole, check)}
	}()
	select {
	case res := <-done:
		return res.x, res.e, time.Since(start)
	case <-time.After(budget + 2*statusGrace + 10*time.Second):
		t.Fatalf("the primitive did not return within %s of its context ending: the execution path is unbounded", 2*statusGrace+10*time.Second)
	}
	return execution{}, Evidence{}, 0
}

// B1-A (RULING-338). A wrapper that writes far more status than the pipe
// holds must be refused as soon as the bound is crossed, not after the writer
// has been waited for: a reader that stops at the bound and then waits for the
// wrapper's exit waits on a writer blocked on that very reader.
func TestAnOversizedStatusCannotWaitForItsWriter(t *testing.T) {
	// 300 KiB past the bound; `timeout 8` is the fixture's own ceiling so a
	// regression costs eight seconds, never the suite.
	body := "#!/bin/sh\nread nonce <&5\nexec 5<&-\ntimeout 8 sh -c 'head -c 300000 /dev/zero | tr \"\\\\0\" x' >&4\nexit 0\n"
	x, _, took := boundedExecute(t, wrapper(t, body), t.TempDir(), Check{Kind: Test, Command: "true"}, 20*time.Second, nil)
	if x.refused == "" || !strings.Contains(x.refused, "too large") || x.err != nil {
		t.Fatalf("an oversized status was not refused as too large: %+v", x)
	}
	if took > 3*time.Second {
		t.Fatalf("the oversized status took %s to refuse: the broker waited for the blocked writer", took)
	}
}

// B1-B. A wrapper that records a valid status and exits while a descendant
// keeps the output pipe: the wait is bounded by the grace, and the capture it
// leaves is incomplete, so the run is not a result.
func TestAnOutputDescriptorHeldAfterTheWrapperExitsIsBounded(t *testing.T) {
	body := "#!/bin/sh\nread nonce <&5\nexec 5<&-\n( exec 3<&- 4>&- 5<&-; sleep 8 ) &\necho \"" + StatusProtocol + " $nonce ran 0\" >&4\nexit 0\n"
	x, _, took := boundedExecute(t, wrapper(t, body), t.TempDir(), Check{Kind: Test, Command: "true"}, 20*time.Second, nil)
	if took > 2*statusGrace+time.Second {
		t.Fatalf("the broker waited %s for a descendant holding the output pipe", took)
	}
	if x.refused == "" || x.err != nil || !strings.Contains(x.refused, "held open") {
		t.Fatalf("an incomplete capture was accepted as a result: %+v", x)
	}
}

// B1-C. Cancellation during a required-status run: the wrapper is killed, the
// confined command it started outlives it holding the output pipe, and the
// primitive still returns within the grace, as a did-not-finish error rather
// than as a refusal or a result.
func TestCancellationDuringARequiredStatusRunIsBounded(t *testing.T) {
	x, _, took := boundedExecute(t, wrapper(t, passThrough), t.TempDir(), Check{Kind: Test, Command: "sh", Args: []string{"-c", "sleep 8"}}, time.Second, nil)
	if took > time.Second+2*statusGrace+time.Second {
		t.Fatalf("cancellation took %s to return: the broker waited for the orphaned command", took)
	}
	if x.err == nil || x.refused != "" || !strings.Contains(x.err.Error(), "did not finish") {
		t.Fatalf("a cancelled run was not reported as unfinished: %+v", x)
	}
}

// B2 (RULING-338, A4). Under the required-status profile a baseline execution
// that is interrupted is an environment outcome: the candidate's real exit is
// kept as an observation, the failure is unattributed, and it is never one the
// worker is asked to fix. The legacy convention is left as it was.
func TestAnInterruptedBaselineIsNotTheCandidatesFailure(t *testing.T) {
	dir, base := t.TempDir(), t.TempDir()
	body := "#!/bin/sh\nread nonce <&5\nexec 5<&-\nif [ \"$SENSEI_CONFINE_ROLE\" = baseline ]; then exec 3<&- 4>&-; sh -c 'sleep 12'; exit 0; fi\n" +
		"while [ \"$1\" != \"--\" ]; do shift; done\nshift\n\"$@\" 3<&- 4>&- 5<&-\nrc=$?\necho \"" + StatusProtocol + " $nonce ran $rc\" >&4\nexit $rc\n"
	check := Check{Kind: Test, Command: "sh", Args: []string{"-c", "exit 1"}}
	// Four seconds for the candidate's own run and the start of the baseline:
	// generous on a loaded machine, far under the fixture's twelve-second sleep.
	_, e, took := boundedExecute(t, wrapper(t, body), dir, check, 4*time.Second, func() (string, error) { return base, nil })
	if took > 4*time.Second+2*statusGrace+time.Second {
		t.Fatalf("the interrupted baseline took %s", took)
	}
	if e.Outcome != Errored || e.Attribution != "unattributed" || e.ExitStatus != 1 || !strings.Contains(e.Detail, "baseline") {
		t.Fatalf("an interrupted baseline did not become an unattributed environment outcome: %+v", e)
	}
	b := Bundle{CandidateID: "task-1", DiffDigest: "sha256:abc", Checks: []Evidence{e}}
	if len(b.CandidateFailures()) != 0 || len(b.Unactionable()) != 1 || b.Passed() {
		t.Fatalf("an interrupted baseline is actionable or certifying: %+v", b)
	}
	// Controls: an executed baseline still attributes both ways.
	ok := Runner{Workspace: dir, Confine: wrapper(t, passThrough), Baseline: func() (string, error) { return base, nil }}
	if e := ok.Run(context.Background(), "task-1", "sha256:abc", []Check{check}).Checks[0]; e.Outcome != Infrastructure || e.Attribution != "pre-existing" {
		t.Fatalf("a base failing the same way is pre-existing: %+v", e)
	}
	// Relative to the workspace each side runs in: present in the candidate's, absent in the base's.
	passesOnBase := Check{Kind: Test, Command: "sh", Args: []string{"-c", "test ! -e only-here"}}
	if err := os.WriteFile(filepath.Join(dir, "only-here"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if e := ok.Run(context.Background(), "task-1", "sha256:abc", []Check{passesOnBase}).Checks[0]; e.Outcome != Failed || e.Attribution != "candidate" {
		t.Fatalf("a base passing makes the failure the candidate's: %+v", e)
	}
}

// B1-B, non-zero exit (review SF1). A wrapper that records a genuine failing
// exit and exits with it while a descendant keeps the output pipe: the exit
// does not rescue the capture. It is cut, so it is not a result, and the
// candidate is not recorded as having failed with output it did not have.
func TestAnIncompleteCaptureIsNotAResultWhateverTheExit(t *testing.T) {
	body := "#!/bin/sh\nread nonce <&5\nexec 5<&-\necho partial\n( exec 3<&- 4>&- 5<&-; sleep 8 ) &\necho \"" + StatusProtocol + " $nonce ran 1\" >&4\nexit 1\n"
	x, _, took := boundedExecute(t, wrapper(t, body), t.TempDir(), Check{Kind: Test, Command: "true"}, 20*time.Second, nil)
	if took > 2*statusGrace+time.Second {
		t.Fatalf("the broker waited %s for a descendant holding the output pipe", took)
	}
	if x.refused == "" || x.err != nil || !strings.Contains(x.refused, "incomplete") {
		t.Fatalf("a cut capture behind a failing exit was accepted as the candidate's failure: %+v", x)
	}
	// Through the runner the same run is a refusal, never a candidate failure.
	e := confinedRun(t, wrapper(t, body), t.TempDir(), Check{Kind: Test, Command: "true"})
	if !refused(e) || e.Outcome == Failed {
		t.Fatalf("an incomplete capture became evidence: %+v", e)
	}
}

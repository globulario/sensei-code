//go:build linux

package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const passThrough = "#!/bin/sh\nwhile [ \"$1\" != \"--\" ]; do shift; done\nshift\nexec \"$@\"\n"

func wrapper(t *testing.T, body string) *Confinement {
	t.Helper()
	p := filepath.Join(t.TempDir(), "d2-confine")
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(body))
	return &Confinement{Profile: "d2-test", Command: p, Args: []string{"--profile", "d2-test"}, SHA256: hex.EncodeToString(sum[:])}
}

// touch is a check that leaves a marker, so a test can tell whether it ran.
func touch(marker string) Check {
	return Check{Kind: Test, Command: "sh", Args: []string{"-c", "touch " + marker}}
}

func ran(marker string) bool {
	_, err := os.Stat(marker)
	return err == nil
}

// A confined runner runs the check through the pinned wrapper and says so in
// the evidence: the profile, the wrapper and its digest, and who asked.
func TestAConfinedCheckRunsThroughItsPinnedWrapper(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	c := wrapper(t, passThrough)
	r := Runner{Workspace: dir, Confine: c, RequestedBy: ToolTimeRequester}
	b := r.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(marker)})
	e := b.Checks[0]
	if e.Outcome != Passed || !ran(marker) {
		t.Fatalf("the confined check did not run and pass: %+v", e)
	}
	if !strings.Contains(e.ExecutedBy, "confinement profile d2-test") || !strings.Contains(e.ExecutedBy, c.Command) ||
		!strings.Contains(e.ExecutedBy, c.SHA256) || e.RequestedBy != ToolTimeRequester || e.Command != "sh" {
		t.Fatalf("the evidence does not name the executor and profile: %+v", e)
	}
	// A worker's own check is advisory: bound to the bytes, certifying none.
	if e.Certifies("task-1", "sha256:abc") || b.Certifies("task-1", "sha256:abc") || !b.ToolTime() {
		t.Fatalf("tool-time evidence certified: %+v", b)
	}
	if !strings.Contains(b.Render(), "ADVISORY") {
		t.Fatalf("the rendering does not say it is advisory:\n%s", b.Render())
	}
	// The same evidence asked for by the workflow certifies (the control).
	e.RequestedBy = "sensei-code workflow"
	if !e.Certifies("task-1", "sha256:abc") {
		t.Fatal("premise: workflow evidence bound to these bytes does not certify them")
	}
}

// Missing, mismatched or failing confinement is Errored with the typed
// refusal, and the check never ran on the host.
func TestNoConfinementMeansNoExecution(t *testing.T) {
	good := wrapper(t, passThrough)
	cases := map[string]*Confinement{
		"missing wrapper":  {Profile: "d2-test", Command: filepath.Join(t.TempDir(), "gone"), SHA256: good.SHA256},
		"digest mismatch":  {Profile: "d2-test", Command: good.Command, SHA256: strings.Repeat("0", 64)},
		"no pin":           {Profile: "d2-test", Command: good.Command},
		"relative wrapper": {Profile: "d2-test", Command: "d2-confine", SHA256: good.SHA256},
		"no profile":       {Command: good.Command, SHA256: good.SHA256},
		"wrapper fails":    wrapper(t, "#!/bin/sh\nexit 125\n"),
		"custom failure":   func() *Confinement { c := wrapper(t, "#!/bin/sh\nexit 3\n"); c.FailureExit = 3; return c }(),
	}
	for name, c := range cases {
		dir := t.TempDir()
		marker := filepath.Join(dir, "ran")
		e := Runner{Workspace: dir, Confine: c}.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(marker)}).Checks[0]
		if e.Outcome != Errored || !strings.HasPrefix(e.Detail, SandboxUnavailable) {
			t.Errorf("%s: not refused: %+v", name, e)
		}
		if ran(marker) {
			t.Errorf("%s: the check ran unconfined", name)
		}
	}
}

// A confined check that exits non-zero is still the candidate's failure, and
// printing PASS does not change that.
func TestAConfinedFailureIsACandidateFailure(t *testing.T) {
	dir := t.TempDir()
	r := Runner{Workspace: dir, Confine: wrapper(t, passThrough)}
	e := r.Run(context.Background(), "task-1", "sha256:abc", []Check{{Kind: Test, Command: "sh", Args: []string{"-c", "echo PASS; exit 1"}}}).Checks[0]
	if e.Outcome != Failed || e.ExitStatus != 1 || strings.HasPrefix(e.Detail, SandboxUnavailable) {
		t.Fatalf("a printed PASS with exit 1 is not a candidate failure: %+v", e)
	}
}

// F2. What runs is the wrapper that was hashed: a path swapped after
// verification does not change it. Fails if the wrapper is executed by path.
func TestASwappedWrapperPathDoesNotRun(t *testing.T) {
	dir := t.TempDir()
	marker, swapped := filepath.Join(dir, "ran"), filepath.Join(dir, "swapped")
	c := wrapper(t, passThrough)
	f, err := c.open()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	evil := c.Command + ".evil"
	if err := os.WriteFile(evil, []byte("#!/bin/sh\ntouch "+swapped+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(evil, c.Command); err != nil {
		t.Fatal(err)
	}
	cmd := c.commandFor(context.Background(), f, dir, touch(marker))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("the verified wrapper did not run: %v: %s", err, out)
	}
	if ran(swapped) || !ran(marker) {
		t.Fatalf("the swapped wrapper ran (swapped=%v, verified=%v)", ran(swapped), ran(marker))
	}
	// Through the runner, the swapped bytes no longer match the pin.
	e := Runner{Workspace: dir, Confine: c}.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(marker)}).Checks[0]
	if e.Outcome != Errored || !strings.HasPrefix(e.Detail, SandboxUnavailable) || ran(swapped) {
		t.Fatalf("a swapped wrapper was run: %+v", e)
	}
	// A symlinked wrapper is refused, even to the pinned bytes.
	good := wrapper(t, passThrough)
	link := filepath.Join(t.TempDir(), "d2-link")
	if err := os.Symlink(good.Command, link); err != nil {
		t.Fatal(err)
	}
	linked := *good
	linked.Command = link
	if e := (Runner{Workspace: dir, Confine: &linked}).Run(context.Background(), "task-1", "sha256:abc", []Check{touch(marker)}).Checks[0]; e.Outcome != Errored ||
		!strings.HasPrefix(e.Detail, SandboxUnavailable) {
		t.Fatalf("a symlinked wrapper was followed: %+v", e)
	}
}

// F3. A wrapper killed by a signal has no exit status to attribute: it is
// refused, never the candidate's failure. Fails if only the failure exit is
// recognised.
func TestASignalledWrapperIsRefusedNotAFailure(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	r := Runner{Workspace: dir, Confine: wrapper(t, "#!/bin/sh\nkill -KILL $$\n")}
	e := r.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(marker)}).Checks[0]
	if e.Outcome != Errored || !strings.HasPrefix(e.Detail, SandboxUnavailable) || ran(marker) {
		t.Fatalf("a signalled wrapper was not refused: %+v", e)
	}
}

// F7. A check the envelope refuses still records who asked for it.
func TestANotPermittedCheckRecordsItsRequester(t *testing.T) {
	dir := t.TempDir()
	r := Runner{Workspace: dir, RequestedBy: ToolTimeRequester, Permits: func(CheckKind) (bool, string) { return false, "denied" }}
	e := r.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(filepath.Join(dir, "ran"))}).Checks[0]
	if e.Outcome != NotPermitted || e.RequestedBy != ToolTimeRequester {
		t.Fatalf("the refused check does not name its requester: %+v", e)
	}
}

// Attribution runs the base under the same confinement; a base it cannot
// confine is unattributed, never run on the host.
func TestAttributionUsesTheSameConfinement(t *testing.T) {
	dir, base := t.TempDir(), t.TempDir()
	c := wrapper(t, passThrough)
	r := Runner{Workspace: dir, Confine: c, Baseline: func() (string, error) { return base, nil }}
	check := Check{Kind: Test, Command: "sh", Args: []string{"-c", "touch attributed; exit 1"}}
	e := r.Run(context.Background(), "task-1", "sha256:abc", []Check{check}).Checks[0]
	if e.Outcome != Infrastructure || !ran(filepath.Join(base, "attributed")) {
		t.Fatalf("the base was not run under the confinement: %+v", e)
	}
}

// Unconfined is unchanged: no confinement recorded, the workflow as requester.
func TestAnUnconfinedRunnerIsUnchanged(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	e := Runner{Workspace: dir}.Run(context.Background(), "task-1", "sha256:abc", []Check{touch(marker)}).Checks[0]
	if e.Outcome != Passed || e.RequestedBy != "sensei-code workflow" ||
		e.ExecutedBy != "sensei-code execution broker" || !ran(marker) {
		t.Fatalf("the default runner changed: %+v", e)
	}
}

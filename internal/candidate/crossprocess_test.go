//go:build unix

package candidate

// RULING-360 C2 witnesses with genuinely separate OS processes (goroutines do
// not prove flock): the candidate record's read-check-write is one
// transaction across processes, and no stale writer in another process can
// remove or alter a durable safety state.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const helperEnv = "N5_CANDIDATE_HELPER"

// TestHelperCandidateProcess is the child side; skipped unless the parent
// started it with an action.
func TestHelperCandidateProcess(t *testing.T) {
	action := os.Getenv(helperEnv)
	if action == "" {
		t.Skip("helper")
	}
	root, task, tag := os.Getenv("N5_ROOT"), "task-1", os.Getenv("N5_TAG")
	result := func(s string) {
		os.WriteFile(filepath.Join(root, "result-"+tag), []byte(s), 0o644)
		os.Exit(0)
	}
	switch action {
	case "stale-save", "stale-resolve":
		stale, ok, err := Load(root, task)
		if err != nil || !ok {
			os.Exit(3)
		}
		os.WriteFile(filepath.Join(root, "loaded-"+tag), nil, 0o644)
		waitFor(filepath.Join(root, "go"))
		if action == "stale-save" {
			err = stale.Save(root)
		} else {
			_, err = stale.Resolve(root, Resolution{Disposition: Resumable, Reason: "ordinary path in another process"})
		}
		if err != nil {
			result("refused: " + err.Error())
		}
		result("written")
	case "begin":
		os.WriteFile(filepath.Join(root, "ready-"+tag), nil, 0o644)
		waitFor(filepath.Join(root, "go"))
		if _, err := BeginExecution(root, task, inflight("exec-"+tag)); err != nil {
			result("refused: " + err.Error())
		}
		result("won")
	case "hold-lock":
		unlock, err := lockRecord(path(root, task) + ".lock")
		if err != nil {
			os.Exit(4)
		}
		_ = unlock
		os.WriteFile(filepath.Join(root, "locked-"+tag), nil, 0o644)
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(2)
}

func waitFor(p string) {
	for i := 0; i < 4000; i++ {
		if _, err := os.Stat(p); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func startHelper(t *testing.T, action, root, tag string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperCandidateProcess$")
	cmd.Env = append(os.Environ(), helperEnv+"="+action, "N5_ROOT="+root, "N5_TAG="+tag)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func resultOf(t *testing.T, cmd *exec.Cmd, root, tag string) string {
	t.Helper()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("helper %s: %v", tag, err)
	}
	b, _ := os.ReadFile(filepath.Join(root, "result-"+tag))
	return string(b)
}

// W2. Process B loads the record before process A begins an execution; B's
// stale copy (no in-flight record) is refused, and A's execution stays.
func TestAStaleProcessCannotDropTheInFlightRecord(t *testing.T) {
	root, _ := recorded(t)
	b := startHelper(t, "stale-save", root, "b")
	waitFor(filepath.Join(root, "loaded-b"))
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "go"), nil, 0o644)
	res := resultOf(t, b, root, "b")
	if fresh, _, _ := Load(root, "task-1"); fresh.InFlight == nil || fresh.InFlight.Invocation != "exec-1" || !strings.HasPrefix(res, "refused") {
		t.Fatalf("another process's stale copy dropped the execution: %q %+v", res, fresh.InFlight)
	}
}

// W3. Process B holds a copy with the same invocation but without the
// baseline A bound after B loaded: B cannot remove the binding.
func TestAStaleProcessCannotDropTheBaselineBinding(t *testing.T) {
	root, _ := recorded(t)
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	b := startHelper(t, "stale-save", root, "b")
	waitFor(filepath.Join(root, "loaded-b"))
	if err := NoteBaseline(root, "task-1", "exec-1", "/base"); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "go"), nil, 0o644)
	res := resultOf(t, b, root, "b")
	fresh, _, _ := Load(root, "task-1")
	if fresh.InFlight == nil || fresh.InFlight.Baseline != "/base" || !strings.HasPrefix(res, "refused") {
		t.Fatalf("a stale process removed the baseline binding: child %q, record %+v", res, fresh.InFlight)
	}
}

// W4. A records the quarantine after B loaded; B's ordinary resolution is
// refused and the quarantine stays authoritative.
func TestAnotherProcessCannotResolveOverAQuarantine(t *testing.T) {
	root, _ := recorded(t)
	b := startHelper(t, "stale-resolve", root, "b")
	waitFor(filepath.Join(root, "loaded-b"))
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := QuarantineExecution(root, "task-1", Resolution{Reason: "unresolved"}); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(root, "go"), nil, 0o644)
	res := resultOf(t, b, root, "b")
	if fresh, _, _ := Load(root, "task-1"); fresh.Resolution == nil || fresh.Resolution.Disposition != Quarantined || !strings.HasPrefix(res, "refused") {
		t.Fatalf("another process resolved over the quarantine: %q %+v", res, fresh.Resolution)
	}
}

// W1 and W6. Several processes race to begin an execution for one task:
// exactly one establishes authority, the record names the winner, and no
// winner's state is lost to another.
func TestConcurrentProcessesCannotBothBeginAnExecution(t *testing.T) {
	root, _ := recorded(t)
	tags := []string{"p1", "p2", "p3", "p4", "p5", "p6"}
	var cmds []*exec.Cmd
	for _, tag := range tags {
		cmds = append(cmds, startHelper(t, "begin", root, tag))
	}
	for _, tag := range tags {
		waitFor(filepath.Join(root, "ready-"+tag))
	}
	os.WriteFile(filepath.Join(root, "go"), nil, 0o644)
	won := ""
	for i, tag := range tags {
		if res := resultOf(t, cmds[i], root, tag); res == "won" {
			if won != "" {
				t.Fatalf("two processes both established authority: %s and %s", won, tag)
			}
			won = tag
		}
	}
	fresh, _, _ := Load(root, "task-1")
	if won == "" || fresh.InFlight == nil || fresh.InFlight.Invocation != "exec-"+won {
		t.Fatalf("the record does not name the one winner %q: %+v", won, fresh.InFlight)
	}
}

// W5 and lock participation. While another process holds the record's lock,
// no transaction here proceeds (it fails closed at its bound); once that
// process dies holding it, the kernel releases it and the next transaction
// reads the last durably committed state.
func TestTheRecordLockSerializesAcrossProcessesAndSurvivesADeadHolder(t *testing.T) {
	root, id := recorded(t)
	saved := lockWait
	lockWait = 300 * time.Millisecond
	t.Cleanup(func() { lockWait = saved })
	holder := startHelper(t, "hold-lock", root, "h")
	waitFor(filepath.Join(root, "locked-h"))
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); !errors.Is(err, errLockTimeout) {
		t.Fatalf("a transaction proceeded while another process held the record lock: %v", err)
	}
	if fresh, _, _ := Load(root, "task-1"); fresh.InFlight != nil {
		t.Fatalf("the record changed while locked: %+v", fresh.InFlight)
	}
	_ = holder.Process.Kill()
	_ = holder.Wait()
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatalf("the lock of a dead holder was not released: %v", err)
	}
	if fresh, _, _ := Load(root, "task-1"); fresh.InFlight == nil || fresh.BaseSHA != id.BaseSHA {
		t.Fatalf("the last committed state was not read: %+v", fresh)
	}
}

// W7. A lock object that is not the expected regular file (a symlink planted
// in its place) refuses the transaction, and the record is not overwritten.
func TestAnInvalidLockObjectRefusesWithoutWriting(t *testing.T) {
	root, id := recorded(t)
	target := path(root, "task-1")
	if err := os.Remove(target + ".lock"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), target+".lock"); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(target)
	id.Branch = "changed"
	if err := id.Save(root); err == nil {
		t.Fatal("a transaction proceeded through a symlinked lock object")
	}
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err == nil {
		t.Fatal("an execution began through a symlinked lock object")
	}
	if after, _ := os.ReadFile(target); string(after) != string(before) {
		t.Fatal("the record was overwritten")
	}
}

// S7 (RULING-361 §12). Another process loaded the record while A's execution
// was in flight; A settles; B's ordinary save and B's ordinary resolution
// arrive afterwards. Neither resurrects the settled execution.
func TestAnotherProcessCannotResurrectASettledExecution(t *testing.T) {
	for _, action := range []string{"stale-save", "stale-resolve"} {
		t.Run(action, func(t *testing.T) {
			root, _ := recorded(t)
			if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
				t.Fatal(err)
			}
			b := startHelper(t, action, root, "b")
			waitFor(filepath.Join(root, "loaded-b"))
			if _, err := SettleExecution(root, "task-1", "exec-1"); err != nil {
				t.Fatal(err)
			}
			os.WriteFile(filepath.Join(root, "go"), nil, 0o644)
			res := resultOf(t, b, root, "b")
			if fresh, _, _ := Load(root, "task-1"); fresh.InFlight != nil {
				t.Fatalf("another process resurrected a settled execution (%s: %q): %+v", action, res, fresh.InFlight)
			}
		})
	}
}

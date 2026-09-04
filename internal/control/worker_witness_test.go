package control

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/workflow"
)

// The witness for the authority the objective channel actually grants.
//
// A 0600 socket establishes the OS user and nothing else. The governed workers
// this orchestrator launches run as that same user, the broker states plainly
// that there is no process sandbox, and the guard environment hands each worker
// an absolute path under the canonical repository -- so locating
// <canonical>/.sensei-code/control.sock is not an information problem.
//
// Without a check beyond the file mode, a worker implementing one objective can
// originate another. That is the operator's authority, acquired by a process
// that was only ever given implementation authority, and it is invisible: the
// task it creates looks exactly like one a person placed.
//
// This test is the demonstration. It launches a process shaped the way a
// governed implementer is shaped -- no controlling terminal, stdin and stdout
// pipes, the guard environment, running in a candidate worktree -- and has it
// derive the canonical root and submit. The repaired system must refuse it and
// create no task.

const witnessMarker = "sensei-code-worker-witness"

// workerShapedSubmit runs a subprocess with a governed worker's shape and
// returns what it reported.
func workerShapedSubmit(t *testing.T, canonicalRoot, workspace, objective string) (string, error) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate this test binary to re-enter it as the worker: %v", err)
	}
	// The guard environment a governed implementer receives, verbatim in shape:
	// an absolute hooks path under the canonical repository.
	guard := filepath.Join(canonicalRoot, ".sensei-code", "guards", "task-1", "hooks")
	cmd := exec.Command(exe, "-test.run=^TestControlWorkerWitnessProcess$", witnessMarker, objective)
	cmd.Dir = workspace
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_COUNT=1",
		"GIT_CONFIG_KEY_0=core.hooksPath",
		"GIT_CONFIG_VALUE_0="+guard,
	)
	// No controlling terminal, and stdin is a pipe: exactly how a worker is
	// launched. This is the property the repair reads.
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	out, runErr := func() ([]byte, error) {
		defer stdin.Close()
		return cmd.CombinedOutput()
	}()
	return strings.TrimSpace(string(out)), runErr
}

// TestControlWorkerWitnessProcess is that subprocess. Without the marker it
// skips; with one it does what a worker with implementation authority could try
// and prints the outcome.
func TestControlWorkerWitnessProcess(t *testing.T) {
	args := flag.Args()
	if len(args) < 2 || args[0] != witnessMarker {
		t.Skip("not a witness re-entry: this test is the worker-shaped subprocess entry point")
	}
	objective := args[1]

	// Derive the canonical repository from the guard path, the way a worker
	// with this environment can.
	guard := os.Getenv("GIT_CONFIG_VALUE_0")
	root := guard
	for _, segment := range []string{"hooks", "task-1", "guards", ".sensei-code"} {
		if filepath.Base(root) != segment {
			fmt.Printf("WITNESS-ERROR: guard path %q does not have the expected shape\n", guard)
			os.Exit(0)
		}
		root = filepath.Dir(root)
	}

	accepted, err := SubmitLocalObjective(root, objective)
	if err != nil {
		fmt.Printf("REFUSED: %v\n", err)
		os.Exit(0)
	}
	fmt.Printf("ORIGINATED: %s\n", accepted.TaskID)
	os.Exit(0)
}

// A governed worker must not be able to originate an objective.
//
// It runs as this user, it can find the socket, and it has every reason to be
// able to open it. What it must not have is the authority the channel carries.
func TestAGovernedWorkerCannotOriginateAnObjective(t *testing.T) {
	// No stubbed observation: the kernel reports what it reports about the real
	// subprocess. This test is worth nothing otherwise.
	h := newLocalHarnessWithPeer(t, nil)
	// A candidate worktree beside the canonical repository, which is where a
	// worker actually runs.
	workspace := t.TempDir()

	out, err := workerShapedSubmit(t, h.root, workspace, "originate work I was never asked to do")
	if err != nil {
		t.Fatalf("the witness subprocess did not run: %v (%s)", err, out)
	}
	if strings.HasPrefix(out, "WITNESS-ERROR") {
		t.Fatalf("the witness could not reach the channel it is supposed to test: %s", out)
	}
	if strings.HasPrefix(out, "ORIGINATED") {
		t.Fatalf("a governed worker originated a governed objective: %s\n"+
			"locality is not objective authority: this process runs as the same user, "+
			"was given the canonical root in its guard environment, and is sandboxed by nothing", out)
	}
	if !strings.HasPrefix(out, "REFUSED") {
		t.Fatalf("the witness reported something unexpected: %s", out)
	}
	// And nothing reached the engine.
	if len(h.submitted) != 0 {
		t.Fatalf("a refused submission still created work: %v", h.submitted)
	}
}

// The law itself, over the facts the kernel reports.
//
// The observation is substitutable in tests and the JUDGEMENT is not: this
// exercises mayOriginateObjective directly, so nothing here is a stub returning
// what it was told to.
func TestOnlyAnOperatorMayOriginateAnObjective(t *testing.T) {
	self := uint32(os.Getuid())
	operator := peer{PID: 4242, UID: self, Terminal: 34816}

	if err := mayOriginateObjective(operator, self); err != nil {
		t.Fatalf("an interactive process this orchestrator did not launch was refused: %v", err)
	}

	for name, p := range map[string]peer{
		// The demonstrated defect: a worker is this process's descendant.
		"a governed worker": {PID: 4243, UID: self, Terminal: 34816, Descendant: true},
		// A worker launched with pipes has no terminal either. Both facts are
		// checked, so escaping one does not pass.
		"a worker with no terminal": {PID: 4244, UID: self, Descendant: true},
		// A background process at this user's privilege, which is what "0600
		// establishes the user" actually admits.
		"a background process": {PID: 4245, UID: self},
		// Another account entirely.
		"another user": {PID: 4246, UID: self + 1, Terminal: 34816},
	} {
		if err := mayOriginateObjective(p, self); err == nil {
			t.Fatalf("%s was granted authority to originate governed work", name)
		}
	}
}

// Each refusal says which fact was missing, because "refused" alone leaves an
// operator with nothing to fix.
func TestEachRefusalNamesTheFactThatWasMissing(t *testing.T) {
	self := uint32(os.Getuid())
	for want, p := range map[string]peer{
		"launched":             {PID: 1, UID: self, Terminal: 1, Descendant: true},
		"controlling terminal": {PID: 2, UID: self},
		"uid":                  {PID: 3, UID: self + 1, Terminal: 1},
	} {
		err := mayOriginateObjective(p, self)
		if err == nil {
			t.Fatalf("%s: not refused", want)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
}

// A platform whose peer this project cannot inspect refuses rather than falling
// back to the file mode. The mode establishes the user, and the user is not the
// question.
func TestAnUnestablishablePeerIsRefused(t *testing.T) {
	h := newLocalHarnessWithPeer(t, func(net.Conn) (peer, error) {
		return peer{}, errors.New("no peer credentials on this platform")
	})
	if _, err := SubmitLocalObjective(h.root, "originate something"); err == nil {
		t.Fatal("an objective was accepted from a caller nobody could establish")
	}
	if len(h.submitted) != 0 {
		t.Fatalf("a refused submission still created work: %v", h.submitted)
	}
}

// The reported provenance is the one the entry point recorded, not one the
// transport predicted.
func TestTheReportedProvenanceComesFromTheEntryPoint(t *testing.T) {
	h := newHarness(t)
	h.server.peerFor = operatorPeer
	if err := h.server.ListenLocal(h.root); err != nil {
		t.Fatalf("bind: %v", err)
	}
	t.Cleanup(func() { h.server.CloseLocal() })
	// An entry point that recorded something else. The channel must report what
	// it was told, not what it expects -- if it predicted, this would still say
	// "local operator" and the disagreement would be invisible.
	go h.server.ServeLocal(func(task string) workflow.Submission {
		return workflow.Submission{TaskID: "task-9", Provenance: workflow.SubmittedUnattended}
	})

	accepted, err := SubmitLocalObjective(h.root, "do the thing")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if accepted.Provenance != string(workflow.SubmittedUnattended) {
		t.Fatalf("the channel reported %q rather than what the entry point recorded", accepted.Provenance)
	}
}

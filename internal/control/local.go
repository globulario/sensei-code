package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/workflow"
)

// The local channel exists because a headless orchestrator needs an objective
// and the remote role holder must not be the one to supply it.
//
//	human / local operator  -> authorizes the objective
//	control Engine          -> orchestrates it
//	remote architect        -> answers the architecture turn it is issued
//	local Claude / Codex    -> implements
//	remote reviewer         -> answers the review turn it is issued
//
// A Unix domain socket rather than another HTTP route on the same listener, and
// the reason is mechanical rather than stylistic. The remote agent reaches this
// process through a tunnel, and a tunnel forwards a TCP port; it does not
// forward a filesystem. So a socket inside the repository, at mode 0600, is
// reachable by a process running as this user on this machine and by nothing
// that arrives over the wire. Adding a submit_task route to the MCP surface
// would have been one line and would have handed the remote architect the one
// authority it must never hold.
//
// What this establishes is local authority over this process. It is NOT
// evidence that a person typed: a local script submits identically, and the
// provenance says so -- see workflow.SubmittedByLocalOperator.
//
// The surface is one message. There is no command here, no argument vector, no
// file path and no way to name a provider: an operator submits an OBJECTIVE,
// and everything about how it is carried out remains this process's own
// decision.

// LocalSocketName is the socket's name inside the repository's local state
// directory.
const LocalSocketName = "control.sock"

// LocalSocketPath is where a control process listens for local submissions.
func LocalSocketPath(repoRoot string) string {
	return filepath.Join(repoRoot, ".sensei-code", LocalSocketName)
}

// LocalSubmission is the only thing an operator may say on this channel.
//
// One field. A second one would be the beginning of a local command protocol,
// and a local command protocol is a shell with extra steps.
type LocalSubmission struct {
	Task string `json:"task"`
}

// LocalAccepted is what the process answers: which task it created, and under
// whose authority it recorded the objective.
//
// The provenance is returned so the operator sees what was actually stamped
// rather than assuming their access made the objective a human's. It is the
// process's answer about its own record, not a value the submitter chose.
type LocalAccepted struct {
	TaskID     string `json:"task_id"`
	Provenance string `json:"provenance"`
	Workspace  string `json:"workspace"`
}

// ListenLocal binds the local submission socket.
//
// A stale socket from a process that was killed is removed first; a socket a
// LIVE process is listening on is not, and the refusal names the collision.
// Removing that one would take the objective channel away from a running owner
// and give it to a second — which is exactly the two-Engine-owners mistake this
// channel exists inside.
func (s *Server) ListenLocal(repoRoot string) error {
	path := LocalSocketPath(repoRoot)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if live, err := net.Dial("unix", path); err == nil {
		_ = live.Close()
		return fmt.Errorf("another control process is already listening on %s; "+
			"exactly one process may own the engine for this repository", path)
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	ln, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	// Narrowed after binding rather than trusted to the umask, which is a
	// process setting anybody's environment can have changed.
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		return fmt.Errorf("could not restrict %s to this user: %w", path, err)
	}
	s.local = ln
	s.localPath = path
	return nil
}

// LocalAddr is the socket this process is listening on, empty before
// ListenLocal.
func (s *Server) LocalAddr() string { return s.localPath }

// ServeLocal accepts objectives until the listener is closed.
func (s *Server) ServeLocal(submit func(task string) string) error {
	if s.local == nil {
		return errors.New("the control surface was asked to accept objectives before it bound a local socket")
	}
	for {
		conn, err := s.local.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.serveLocalConn(conn, submit)
	}
}

// CloseLocal stops accepting objectives and removes the socket.
func (s *Server) CloseLocal() error {
	if s.local == nil {
		return nil
	}
	err := s.local.Close()
	if s.localPath != "" {
		_ = os.Remove(s.localPath)
	}
	return err
}

// maxLocalSubmissionBytes bounds one objective. It is an objective, not a file.
const maxLocalSubmissionBytes = 64 << 10

// localDeadline bounds one exchange.
//
// A client that writes half a message and waits must not hold a goroutine and a
// socket open indefinitely: the decoder would wait for the rest of the object
// and the client for a reply that is not coming. That is a hang rather than a
// refusal, and a hang is the failure this project has the least vocabulary for.
const localDeadline = 10 * time.Second

func (s *Server) serveLocalConn(conn net.Conn, submit func(task string) string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(localDeadline))
	var in LocalSubmission
	dec := json.NewDecoder(io.LimitReader(conn, maxLocalSubmissionBytes))
	// Strict, for the same reason register_role is strict: the fields that must
	// not be settable here are provenance, principal and authority, and a
	// lenient decoder ignores exactly those silently.
	dec.DisallowUnknownFields()
	if err := dec.Decode(&in); err != nil {
		writeLocalError(conn, "the submission is not a bounded objective: "+err.Error())
		return
	}
	// Checked BEFORE anything is submitted. A message carrying a second object
	// is one this channel does not understand, and understanding half of it is
	// how a submitter comes to believe it said something the owner never read.
	if dec.More() {
		writeLocalError(conn, "the submission carries trailing content; this channel takes one objective")
		return
	}
	task := strings.TrimSpace(in.Task)
	if task == "" {
		writeLocalError(conn, "a submission must carry an objective")
		return
	}

	// The provenance is stamped by the ENTRYPOINT, in the workflow package, and
	// there is no argument here that could influence it. An operator with local
	// access places an objective; what that establishes is recorded by the
	// engine, not chosen by the submitter.
	taskID := submit(task)
	_ = json.NewEncoder(conn).Encode(LocalAccepted{
		TaskID:     taskID,
		Provenance: string(workflow.SubmittedByLocalOperator),
		Workspace:  s.workspace,
	})
}

func writeLocalError(conn net.Conn, message string) {
	_ = json.NewEncoder(conn).Encode(map[string]string{"error": message})
}

// SubmitLocalObjective is the client half: connect, say one thing, read the
// answer.
//
// It lives here so the process that submits and the process that accepts agree
// about the message by construction rather than by two people keeping two
// encoders in step. It creates no engine and runs nothing.
func SubmitLocalObjective(repoRoot, task string) (LocalAccepted, error) {
	objective := strings.TrimSpace(task)
	if objective == "" {
		return LocalAccepted{}, errors.New("an objective cannot be empty")
	}
	path := LocalSocketPath(repoRoot)
	conn, err := net.Dial("unix", path)
	if err != nil {
		return LocalAccepted{}, fmt.Errorf(
			"no control process is accepting objectives at %s; start one with `sensei-code control`: %w", path, err)
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(localDeadline))
	if err := json.NewEncoder(conn).Encode(LocalSubmission{Task: objective}); err != nil {
		return LocalAccepted{}, err
	}
	// The write half is closed so the far side sees the end of the message
	// rather than waiting for bytes that are not coming. Without it a
	// well-formed exchange still works and a truncated one deadlocks -- the
	// worse half to leave to a timeout.
	if unix, ok := conn.(*net.UnixConn); ok {
		_ = unix.CloseWrite()
	}
	raw, err := io.ReadAll(io.LimitReader(conn, maxLocalSubmissionBytes))
	if err != nil {
		return LocalAccepted{}, err
	}
	var refusal struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(raw, &refusal) == nil && strings.TrimSpace(refusal.Error) != "" {
		return LocalAccepted{}, errors.New(refusal.Error)
	}
	var accepted LocalAccepted
	if err := json.Unmarshal(bytes.TrimSpace(raw), &accepted); err != nil {
		return LocalAccepted{}, fmt.Errorf("the control process answered something this client cannot read: %w", err)
	}
	if strings.TrimSpace(accepted.TaskID) == "" {
		return LocalAccepted{}, errors.New("the control process accepted the objective and named no task")
	}
	return accepted, nil
}

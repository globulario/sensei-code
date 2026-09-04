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
func (s *Server) ServeLocal(submit func(task string) workflow.Submission) error {
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

func (s *Server) serveLocalConn(conn net.Conn, submit func(task string) workflow.Submission) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(localDeadline))

	// Who is on the other end, established from the socket before a single
	// byte of the request is read. A payload cannot influence this, and a
	// refusal here never reaches the engine.
	if err := s.authorizeObjective(conn); err != nil {
		writeLocalError(conn, err.Error())
		return
	}

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
	// The provenance reported is the one the ENTRY POINT recorded, returned by
	// it. The channel used to predict it with the same constant, which agreed
	// until one of them changed -- and a transport that predicts the record is
	// a second answer to what happened.
	recorded := submit(task)
	_ = json.NewEncoder(conn).Encode(LocalAccepted{
		TaskID:     recorded.TaskID,
		Provenance: string(recorded.Provenance),
		Workspace:  s.workspace,
	})
}

// writeLocalError sends the reason and then drains whatever the caller was
// still sending.
//
// Without the drain the operator sees "connection reset by peer" instead of the
// refusal. Closing a socket that still has unread data in its receive buffer
// resets it, and the reset arrives before the reply the caller was waiting for
// -- so the one refusal a person most needs to read (this caller has no
// controlling terminal) is replaced by a transport error that explains nothing.
// It happens exactly on the paths that refuse before reading the request, which
// is every authority refusal.
func writeLocalError(conn net.Conn, message string) {
	_ = json.NewEncoder(conn).Encode(map[string]string{"error": message})
	_, _ = io.Copy(io.Discard, io.LimitReader(conn, maxLocalSubmissionBytes))
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

// authorizeObjective decides whether the party on the other end may originate
// governed work.
//
// The file mode establishes the OS user and stops there. That is not the
// question. The governed workers this orchestrator launches run as that same
// user, the broker states plainly that there is no process sandbox, and each
// worker is handed an absolute path under the canonical repository in its guard
// environment -- so a worker implementing one objective could open this socket
// and originate another. It was demonstrated before this function existed; see
// TestAGovernedWorkerCannotOriginateAnObjective.
//
// So two facts are established from the socket, both by the kernel and neither
// from the request:
//
//	the peer is not a process this orchestrator launched
//	the peer has a controlling terminal
//
// The first is what separates an operator from a worker: workers are this
// process's descendants. The second is what separates a person at a shell from
// a background process, and it is the same evidence /run rests on -- human
// provenance comes from the interactive entry point rather than from words in
// the request.
//
// WHAT THIS DOES NOT ESTABLISH, stated because the alternative is a reader
// assuming otherwise. It is not proof that a human typed. A determined
// same-UID process could daemonize out of the descendant check and allocate a
// pseudo-terminal to pass the other, and nothing here would notice, because
// there is no sandbox between this orchestrator and the processes it runs. What
// this establishes is "an interactive process this orchestrator did not
// launch". That is a real narrowing of "anything running as this user" and it
// is not a proof of personhood. An automation principal that legitimately needs
// this authority should be introduced deliberately, as its own authority type,
// rather than by widening this check until it lets one through.
func (s *Server) authorizeObjective(conn net.Conn) error {
	observe := s.peerFor
	if observe == nil {
		observe = inspectPeer
	}
	if !peerInspectionSupported() && s.peerFor == nil {
		return errors.New("this platform cannot establish who is on the other end of the objective channel, " +
			"so it will not accept one; locality alone is not authority to originate governed work")
	}
	p, err := observe(conn)
	if err != nil {
		return fmt.Errorf("the objective was refused because the caller could not be established: %w", err)
	}
	return mayOriginateObjective(p, uint32(os.Getuid()))
}

// mayOriginateObjective is the judgement, separated from the observation.
//
// Split so that a test can substitute what the kernel SAID without substituting
// what this project DECIDES about it. A test that could stub the decision would
// be checking that a stub returns what it was told to.
func mayOriginateObjective(p peer, selfUID uint32) error {
	if p.UID != selfUID {
		return fmt.Errorf("the objective channel belongs to uid %d and the caller is uid %d", selfUID, p.UID)
	}
	if p.Descendant {
		return fmt.Errorf("pid %d is a process this orchestrator launched, and a worker may not originate "+
			"governed work: implementing one objective does not confer the authority to create another", p.PID)
	}
	if p.Terminal == 0 {
		return fmt.Errorf("pid %d has no controlling terminal; an objective is placed by an operator at one, "+
			"and running as this user is not by itself authority to originate governed work", p.PID)
	}
	return nil
}

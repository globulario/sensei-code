package workflow

// N3 -- EXECUTION TOOLS ARE BOUND TO THE ENGINE'S TASK AUTHORITY, NOT TO
// THEIR ARGUMENTS.
//
// An implementer launched under the execution profile has no built-in tool.
// Its only reach into the candidate is read_file, apply_patch and run_check,
// served by one execService that belongs to exactly one live implementer
// invocation. Everything that decides what a call may touch -- the task, the
// operative PlanAttempt and its world, the candidate's base and worktree, the
// grants, the check commands and the confinement they run under -- is pinned
// by the engine when the invocation starts, and revalidated through the
// engine's own accessors immediately before every effect. No argument can
// select any of it: an argument the tool does not define is refused, never
// ignored.
//
// The service derives, mints and stores no authority. It reads the operative
// attempt (operativePlanAttempt), the recorded grants (recordedGrants), the
// candidate identity (candidate.Load), the configured checks (checksOf) and
// the capability envelope (validationPermits). Its evidence goes out as an
// ordinary ValidationRun through the engine's emission path, marked advisory:
// the post-capture validate() remains the certifier.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/broker"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/validation"
)

// The typed refusals. Every one is returned to the client as isError, never
// as a result.
const (
	refuseTaskBinding = "TASK_BINDING_REFUSED"
	refusePlanStale   = "PLAN_ATTEMPT_STALE"
	refuseGrant       = "GRANT_REFUSED"
	refusePatchStale  = "PATCH_STALE"
	refusePath        = "PATH_REFUSED"
	refuseArg         = "ARG_REFUSED"
	refuseCheck       = "CHECK_REFUSED"
	refuseSandbox     = validation.SandboxUnavailable
	refuseInFlight    = "EFFECT_IN_FLIGHT"
)

// execRequestedBy is the requesting party tool-time evidence records, and
// the marker by which it never certifies.
const execRequestedBy = validation.ToolTimeRequester

// maxToolFile bounds what read_file and apply_patch will hold in memory.
const maxToolFile = 8 << 20

type toolRefusal struct {
	Code   string
	Detail string
}

func (r *toolRefusal) Error() string { return r.Code + ": " + r.Detail }

func refuseTool(code, format string, a ...any) *toolRefusal {
	return &toolRefusal{Code: code, Detail: fmt.Sprintf(format, a...)}
}

// authorityGate is the engine-owned boundary that makes a tool effect and
// the authority it was revalidated against one step.
//
// Effects hold it shared from their revalidation to the end of their
// mutation; installing an operative attempt, recording grants and ending an
// invocation hold it exclusively. One per task, so a long check in one task
// never waits on another, and it is never e.mu: the engine lock is taken only
// briefly, inside, by the accessors revalidation reads.
func (e *Engine) authorityGate(taskID string) *sync.RWMutex {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.authorityGates == nil {
		e.authorityGates = map[string]*sync.RWMutex{}
	}
	g := e.authorityGates[taskID]
	if g == nil {
		g = &sync.RWMutex{}
		e.authorityGates[taskID] = g
	}
	return g
}

// execBinding is what the engine pins one service to when the invocation
// starts. Nothing in it comes from the client.
type execBinding struct {
	TaskID     string
	Invocation string
	Attempt    planAttempt
	BaseSHA    string
	Worktree   string
}

// execService serves one implementer invocation.
type execService struct {
	e      *Engine
	b      execBinding
	dir    string
	socket string
	ln     *net.UnixListener

	ctx    context.Context
	cancel context.CancelFunc

	// effects serializes this invocation's mutations and checks, so two
	// concurrent calls never race each other's preimage.
	effects sync.Mutex
	// checking is set while a check runs. The check is candidate code that
	// shares this user and may reach the socket; a call it makes back into
	// the service would wait on the very effect it belongs to, so any call
	// arriving while it is set is refused at once, never queued.
	checking atomic.Bool
	// closed is guarded by the task's authority gate.
	closed bool

	connMu   sync.Mutex
	conns    map[net.Conn]bool
	stopping bool
	wg       sync.WaitGroup
	stopOnce sync.Once

	// client is the binding the provider is launched with.
	client *agent.ExecBinding
}

// execPromptNote tells the implementer what its hands are. It grants
// nothing: the service decides every call.
const execPromptNote = "\n\nEXECUTION TOOLS: you have no built-in tools. Read, edit and check the candidate only through " +
	"mcp__sensei_exec__read_file, mcp__sensei_exec__apply_patch (exact sha256 preimage from read_file, old_string occurring " +
	"exactly once) and mcp__sensei_exec__run_check (format_verify, vet, build, test). They are bound to this task's operative " +
	"plan; a refusal is typed and final for that call. run_check evidence is advisory: the engine validates the captured " +
	"candidate itself.\n"

// startImplementerExec starts the execution service for one implementer
// invocation when the execution profile is selected, and returns nil when it
// is not: the default launch is unchanged. It refuses, before anything
// starts, a runner that would not enforce the binding.
func (e *Engine) startImplementerExec(ctx context.Context, taskID, workspace string, runner agent.Runner) (*execService, error) {
	if !e.Config.ExecBinding.Enabled {
		return nil, nil
	}
	if err := execEnforcingRunner(runner); err != nil {
		return nil, err
	}
	s, err := e.startExecService(ctx, taskID, workspace)
	if err != nil {
		return nil, err
	}
	c, err := s.clientBinding()
	if err != nil {
		s.stop()
		return nil, err
	}
	s.client = c
	return s, nil
}

// execEnforcingRunner establishes that runner is the agent CLI adapter: the
// one implementation whose Run launches the provider through bindExecArgs,
// which refuses a provider that cannot honour the binding. Any other runner
// -- a remote one, a double -- would be handed req.Exec and could ignore it
// while the engine believed the profile in force. The test is the concrete
// type, never something a runner declares about itself.
func execEnforcingRunner(runner agent.Runner) error {
	var cli agent.CLI
	switch r := runner.(type) {
	case agent.CLI:
		cli = r
	case *agent.CLI:
		if r == nil {
			return errors.New("no implementer runner was resolved, so nothing would enforce the execution binding")
		}
		cli = *r
	default:
		return fmt.Errorf("the implementer runner %T does not enforce the execution binding; refusing to start the implementer", runner)
	}
	return cli.ExecBindable()
}

// bind is req launched through this service. A nil service binds nothing.
func (s *execService) bind(req agent.Request) agent.Request {
	if s == nil {
		return req
	}
	req.Exec = s.client
	req.Prompt += execPromptNote
	return req
}

// startExecService pins a service to the task's current authority and starts
// serving it on a private socket outside the worktree. It fails rather than
// start a service it cannot bind: no operative attempt, no recorded
// candidate, or a worktree that is not the candidate's.
func (e *Engine) startExecService(ctx context.Context, taskID, workspace string) (*execService, error) {
	op := e.operativePlanAttempt(taskID)
	if op.ID == "" {
		return nil, errors.New("no plan attempt is operative for this task, so the execution service has no authority to bind")
	}
	id, ok, err := candidate.Load(e.Repo.Root, taskID)
	if err != nil {
		return nil, fmt.Errorf("the candidate identity cannot be read: %w", err)
	}
	if !ok {
		return nil, errors.New("no candidate identity is recorded for this task, so the execution service has no candidate to bind")
	}
	worktree, err := filepath.Abs(workspace)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(id.Worktree) != worktree {
		return nil, fmt.Errorf("the workspace %s is not the recorded candidate worktree %s", worktree, id.Worktree)
	}
	invocation, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	base := strings.TrimSpace(e.Config.ExecBinding.RuntimeDir)
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "sensei-exec-")
	if err != nil {
		return nil, fmt.Errorf("the execution service's private directory cannot be created: %w", err)
	}
	fail := func(err error) (*execService, error) {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return fail(err)
	}
	if inside(dir, worktree) {
		return fail(fmt.Errorf("the execution service directory %s is inside the candidate worktree %s", dir, worktree))
	}
	socket := filepath.Join(dir, "s")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return fail(fmt.Errorf("the execution service socket cannot be opened: %w", err))
	}
	ln.SetUnlinkOnClose(true)
	if err := os.Chmod(socket, 0o600); err != nil {
		ln.Close()
		return fail(err)
	}
	sctx, cancel := context.WithCancel(ctx)
	s := &execService{
		e:   e,
		b:   execBinding{TaskID: taskID, Invocation: invocation, Attempt: op, BaseSHA: id.BaseSHA, Worktree: worktree},
		dir: dir, socket: socket, ln: ln, ctx: sctx, cancel: cancel,
		conns: map[net.Conn]bool{},
	}
	s.wg.Add(1)
	go s.serve()
	return s, nil
}

// inside reports whether dir is root or beneath it, symlinks resolved.
func inside(dir, root string) bool {
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		dir = r
	}
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	rel, err := filepath.Rel(root, dir)
	return err != nil || rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// clientBinding is the binding the provider is launched with: the engine's
// own relay command aimed at this service's socket and identity, the
// private directory its configuration is written into, and the trusted
// client settings.
func (s *execService) clientBinding() (*agent.ExecBinding, error) {
	shim := strings.TrimSpace(s.e.Config.ExecBinding.ShimCommand)
	if shim == "" {
		self, err := os.Executable()
		if err != nil {
			return nil, err
		}
		shim = self
	}
	// The mandatory builtin exclusions always apply; configuration can only
	// add to them. An empty or partial configured list must not re-enable a
	// builtin plugin the profile requires disabled.
	enabled := map[string]bool{}
	for _, p := range config.DefaultDisabledPlugins {
		enabled[p] = false
	}
	for _, p := range s.e.Config.ExecBinding.DisabledPlugins {
		if p = strings.TrimSpace(p); p != "" {
			enabled[p] = false
		}
	}
	settings, err := json.Marshal(map[string]any{"enabledPlugins": enabled})
	if err != nil {
		return nil, err
	}
	return &agent.ExecBinding{
		Command:  shim,
		Args:     []string{"exec-shim", "--socket", s.socket, "--service", s.b.Invocation},
		Dir:      s.dir,
		Settings: settings,
	}, nil
}

// stop ends the invocation's capability: no new connection, every open one
// closed, every later or in-flight effect refused, and the socket and its
// directory removed. Safe to call more than once and on nil.
func (s *execService) stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		// Cancel first, so a running check is killed rather than waited for.
		s.cancel()
		s.ln.Close()
		gate := s.e.authorityGate(s.b.TaskID)
		gate.Lock()
		s.closed = true
		gate.Unlock()
		s.connMu.Lock()
		s.stopping = true
		for c := range s.conns {
			c.Close()
		}
		s.connMu.Unlock()
		s.wg.Wait()
		_ = os.RemoveAll(s.dir)
	})
}

func (s *execService) serve() {
	defer s.wg.Done()
	for {
		conn, err := s.ln.AcceptUnix()
		if err != nil {
			return
		}
		s.connMu.Lock()
		if s.stopping {
			s.connMu.Unlock()
			conn.Close()
			return
		}
		s.conns[conn] = true
		s.wg.Add(1)
		s.connMu.Unlock()
		go func() {
			defer s.wg.Done()
			defer func() {
				s.connMu.Lock()
				delete(s.conns, conn)
				s.connMu.Unlock()
				conn.Close()
			}()
			s.handle(conn)
		}()
	}
}

// peerUID is the uid of the process at the other end of conn.
func peerUID(conn *net.UnixConn) (uint32, error) {
	raw, err := conn.SyscallConn()
	if err != nil {
		return 0, err
	}
	var cred *unix.Ucred
	var cerr error
	if err := raw.Control(func(fd uintptr) {
		cred, cerr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, err
	}
	if cerr != nil {
		return 0, cerr
	}
	return cred.Uid, nil
}

type execHello struct {
	Hello   string `json:"sensei_exec_hello,omitempty"`
	Service string `json:"sensei_exec_service,omitempty"`
	Refused string `json:"sensei_exec_refused,omitempty"`
}

// handle serves one client: a peer that is this user, naming this service's
// identity, and then newline-delimited JSON-RPC 2.0 -- the MCP stdio framing,
// relayed unchanged by the shim.
func (s *execService) handle(conn *net.UnixConn) {
	if uid, err := peerUID(conn); err != nil || uid != uint32(os.Getuid()) {
		return
	}
	r := bufio.NewReaderSize(conn, 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := readLine(r)
	if err != nil {
		return
	}
	var hello execHello
	if json.Unmarshal(line, &hello) != nil || hello.Hello != s.b.Invocation {
		_ = writeJSONLine(conn, execHello{Refused: refuseTaskBinding})
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	if writeJSONLine(conn, execHello{Service: s.b.Invocation}) != nil {
		return
	}
	for {
		line, err := readLine(r)
		if err != nil {
			return
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if resp := s.rpc(line); resp != nil {
			if writeJSONLine(conn, resp) != nil {
				return
			}
		}
	}
}

func readLine(r *bufio.Reader) ([]byte, error) {
	var out []byte
	for {
		chunk, err := r.ReadSlice('\n')
		out = append(out, chunk...)
		if len(out) > maxToolFile*2 {
			return nil, errors.New("message too large")
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return out, nil
	}
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
}

type rpcMessage struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// rpc answers one JSON-RPC message, or returns nil for a notification.
func (s *execService) rpc(line []byte) *rpcMessage {
	var req rpcMessage
	if err := json.Unmarshal(line, &req); err != nil {
		return &rpcMessage{JSONRPC: "2.0", ID: json.RawMessage("null"), Error: &rpcError{Code: -32700, Message: "parse error"}}
	}
	if len(req.ID) == 0 {
		return nil
	}
	reply := &rpcMessage{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		version := "2025-06-18"
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		if json.Unmarshal(req.Params, &p) == nil && p.ProtocolVersion != "" {
			version = p.ProtocolVersion
		}
		reply.Result = map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": agent.ExecServerName, "version": "n3"},
			"instructions": "Task-bound execution tools for one governed implementer invocation. The engine, not any argument, " +
				"selects the task, plan attempt, worktree, grants, checks and confinement.",
		}
	case "ping":
		reply.Result = map[string]any{}
	case "tools/list":
		reply.Result = map[string]any{"tools": execToolList()}
	case "tools/call":
		var p struct {
			Name      string                     `json:"name"`
			Arguments map[string]json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil {
			reply.Error = &rpcError{Code: -32602, Message: "invalid params"}
			return reply
		}
		reply.Result = toolResult(s.call(p.Name, p.Arguments))
	default:
		reply.Error = &rpcError{Code: -32601, Message: "method not found"}
	}
	return reply
}

func toolResult(v any, refusal *toolRefusal) map[string]any {
	if refusal != nil {
		return map[string]any{"isError": true, "content": []map[string]any{{"type": "text", "text": refusal.Error()}}}
	}
	b, _ := json.Marshal(v)
	return map[string]any{"isError": false, "content": []map[string]any{{"type": "text", "text": string(b)}}}
}

func execToolList() []map[string]any {
	str := map[string]any{"type": "string"}
	integer := map[string]any{"type": "integer"}
	schema := func(props map[string]any, required ...string) map[string]any {
		return map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false}
	}
	return []map[string]any{
		{"name": "read_file", "description": "Read one file of the bound candidate (relative path). Returns the sha256 of the WHOLE file and its line count; start_line (1-based) and max_lines bound the excerpt.",
			"inputSchema": schema(map[string]any{"path": str, "start_line": integer, "max_lines": integer}, "path")},
		{"name": "apply_patch", "description": "Replace exactly one occurrence of old_string with new_string in a file the operative plan admits, whose current sha256 equals expected_sha256.",
			"inputSchema": schema(map[string]any{"path": str, "expected_sha256": str, "old_string": str, "new_string": str}, "path", "expected_sha256", "old_string", "new_string")},
		{"name": "run_check", "description": "Run one configured check (format_verify, vet, build, test; go_vet, go_build, go_test name the same) against the bound candidate under the pinned confinement. Advisory: it never certifies the candidate.",
			"inputSchema": schema(map[string]any{"name": str}, "name")},
	}
}

// args decodes a call's arguments strictly: every key must be one the tool
// defines. An argument that tries to name a task, a worktree, a plan or a
// command is refused, not ignored.
type toolArgs map[string]json.RawMessage

func (a toolArgs) only(allowed ...string) *toolRefusal {
	ok := map[string]bool{}
	for _, k := range allowed {
		ok[k] = true
	}
	var extra []string
	for k := range a {
		if !ok[k] {
			extra = append(extra, k)
		}
	}
	if len(extra) != 0 {
		sort.Strings(extra)
		return refuseTool(refuseArg, "argument(s) %s are not defined by this tool; the engine, not the caller, selects the task, worktree, plan and check", strings.Join(extra, ", "))
	}
	return nil
}

func (a toolArgs) str(key string, required bool) (string, *toolRefusal) {
	raw, ok := a[key]
	if !ok {
		if required {
			return "", refuseTool(refuseArg, "%s is required", key)
		}
		return "", nil
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return "", refuseTool(refuseArg, "%s must be a string", key)
	}
	return v, nil
}

func (a toolArgs) integer(key string) (int, *toolRefusal) {
	raw, ok := a[key]
	if !ok {
		return 0, nil
	}
	var v float64
	if json.Unmarshal(raw, &v) != nil || v != float64(int(v)) || v < 0 {
		return 0, refuseTool(refuseArg, "%s must be a non-negative integer", key)
	}
	return int(v), nil
}

// call dispatches one tool call.
func (s *execService) call(name string, raw map[string]json.RawMessage) (any, *toolRefusal) {
	if s.checking.Load() {
		return nil, refuseTool(refuseInFlight, "a check is running for this invocation; no call is served until it ends")
	}
	args := toolArgs(raw)
	switch name {
	case "read_file":
		return s.readFile(args)
	case "apply_patch":
		return s.applyPatch(args)
	case "run_check":
		return s.runCheck(args)
	}
	return nil, refuseTool(refuseArg, "unknown tool %q", name)
}

// revalidate re-establishes, through the engine's own accessors, that the
// authority this service was pinned to is still the task's authority. The
// caller holds the task's authority gate.
func (s *execService) revalidate() (planAttempt, *toolRefusal) {
	if s.closed || s.ctx.Err() != nil {
		return planAttempt{}, refuseTool(refuseTaskBinding, "the implementer invocation this service belongs to has ended")
	}
	op := s.e.operativePlanAttempt(s.b.TaskID)
	if op.ID != s.b.Attempt.ID || op.World != s.b.Attempt.World {
		return planAttempt{}, refuseTool(refusePlanStale, "plan attempt %s is no longer operative for task %s (operative: %s)",
			short12(s.b.Attempt.ID), s.b.TaskID, short12(op.ID))
	}
	id, ok, err := candidate.Load(s.e.Repo.Root, s.b.TaskID)
	if err != nil || !ok || id.BaseSHA != s.b.BaseSHA || filepath.Clean(id.Worktree) != s.b.Worktree {
		return planAttempt{}, refuseTool(refuseTaskBinding, "the recorded candidate of task %s is no longer the one this invocation was bound to", s.b.TaskID)
	}
	return op, nil
}

// toolPath establishes p as a clean path relative to the worktree, naming
// nothing inside .git.
func toolPath(p string) ([]string, *toolRefusal) {
	if p == "" || strings.ContainsAny(p, "\x00\\") || strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return nil, refuseTool(refusePath, "%q is not a clean path relative to the candidate", p)
	}
	parts := strings.Split(p, "/")
	for _, c := range parts {
		if c == "" || c == "." || c == ".." || strings.EqualFold(c, ".git") {
			return nil, refuseTool(refusePath, "%q leaves the candidate or names its repository metadata", p)
		}
	}
	return parts, nil
}

// admitted reports whether the operative attempt admits an edit of p: the
// plan names it, and a test file additionally holds a test-edit grant the
// pinned attempt itself recorded at its world.
func (s *execService) admitted(op planAttempt, p string) *toolRefusal {
	planned := false
	for _, f := range op.Plan.Files {
		if path.Clean(strings.TrimSpace(f)) == p {
			planned = true
			break
		}
	}
	if !planned {
		return refuseTool(refuseGrant, "%s is not a file the operative plan attempt %s names", p, short12(op.ID))
	}
	if !strings.HasSuffix(p, "_test.go") {
		return nil
	}
	_, edits := s.e.recordedGrants(s.b.TaskID, op.ID)
	if edits.PlanAttemptID == op.ID && edits.World == op.World {
		for _, g := range edits.Grants {
			if g.Path == p && g.World == op.World {
				return nil
			}
		}
	}
	return refuseTool(refuseGrant, "%s is an existing test and plan attempt %s recorded no test-edit grant for it", p, short12(op.ID))
}

// openDirBeneath opens the directory holding parts' last element, walking
// from the worktree one component at a time and refusing a symlink at any of
// them. The descriptor it returns is the identity every later step uses, so
// a path swapped after the check is never the path written.
func openDirBeneath(root string, parts []string) (int, error) {
	dir, err := unix.Open(root, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	for _, c := range parts[:len(parts)-1] {
		next, err := unix.Openat(dir, c, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(dir)
		if err != nil {
			return -1, err
		}
		dir = next
	}
	return dir, nil
}

// readAt reads the regular file name in dir without following a symlink.
func readAt(dir int, name string) (unix.Stat_t, []byte, error) {
	var st unix.Stat_t
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return st, nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	if err := unix.Fstat(fd, &st); err != nil {
		return st, nil, err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return st, nil, errors.New("not a regular file")
	}
	if st.Size > maxToolFile {
		return st, nil, fmt.Errorf("larger than %d bytes", maxToolFile)
	}
	body, err := io.ReadAll(io.LimitReader(f, maxToolFile+1))
	return st, body, err
}

func pathRefusal(p string, err error) *toolRefusal {
	switch {
	case errors.Is(err, unix.ELOOP), errors.Is(err, unix.ENOTDIR):
		return refuseTool(refusePath, "%s passes through a symlink or a non-directory; links are never followed", p)
	case errors.Is(err, unix.ENOENT):
		return refuseTool(refusePath, "%s does not exist in the candidate; these tools never create a file", p)
	}
	return refuseTool(refusePath, "%s cannot be read: %v", p, err)
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func (s *execService) readFile(args toolArgs) (any, *toolRefusal) {
	if r := args.only("path", "start_line", "max_lines"); r != nil {
		return nil, r
	}
	p, r := args.str("path", true)
	if r != nil {
		return nil, r
	}
	start, r := args.integer("start_line")
	if r != nil {
		return nil, r
	}
	max, r := args.integer("max_lines")
	if r != nil {
		return nil, r
	}
	parts, r := toolPath(p)
	if r != nil {
		return nil, r
	}
	gate := s.e.authorityGate(s.b.TaskID)
	gate.RLock()
	defer gate.RUnlock()
	if _, r := s.revalidate(); r != nil {
		return nil, r
	}
	dir, err := openDirBeneath(s.b.Worktree, parts)
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	defer unix.Close(dir)
	_, body, err := readAt(dir, parts[len(parts)-1])
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	lines := strings.SplitAfter(string(body), "\n")
	if n := len(lines); n != 0 && lines[n-1] == "" {
		lines = lines[:n-1]
	}
	if start < 1 {
		start = 1
	}
	end := len(lines)
	if max > 0 && start-1+max < end {
		end = start - 1 + max
	}
	content := ""
	if start <= len(lines) {
		content = strings.Join(lines[start-1:end], "")
	} else {
		end = start - 1
	}
	return map[string]any{"path": p, "sha256": sha256Hex(body), "total_lines": len(lines), "total_bytes": len(body),
		"start_line": start, "end_line": end, "content": content}, nil
}

func (s *execService) applyPatch(args toolArgs) (any, *toolRefusal) {
	if r := args.only("path", "expected_sha256", "old_string", "new_string"); r != nil {
		return nil, r
	}
	p, r := args.str("path", true)
	if r != nil {
		return nil, r
	}
	expected, r := args.str("expected_sha256", true)
	if r != nil {
		return nil, r
	}
	old, r := args.str("old_string", true)
	if r != nil {
		return nil, r
	}
	replacement, r := args.str("new_string", true)
	if r != nil {
		return nil, r
	}
	if old == "" {
		return nil, refuseTool(refuseArg, "old_string must not be empty")
	}
	expected = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(expected)), "sha256:")
	parts, r := toolPath(p)
	if r != nil {
		return nil, r
	}

	s.effects.Lock()
	defer s.effects.Unlock()
	gate := s.e.authorityGate(s.b.TaskID)
	gate.RLock()
	defer gate.RUnlock()
	op, r := s.revalidate()
	if r != nil {
		return nil, r
	}
	if r := s.admitted(op, p); r != nil {
		return nil, r
	}
	dir, err := openDirBeneath(s.b.Worktree, parts)
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	defer unix.Close(dir)
	name := parts[len(parts)-1]
	st, body, err := readAt(dir, name)
	if err != nil {
		return nil, pathRefusal(p, err)
	}
	before := sha256Hex(body)
	if before != expected {
		return nil, refuseTool(refusePatchStale, "%s is at sha256 %s, not the expected %s; read it again", p, before, expected)
	}
	if n := occurrences(string(body), old); n != 1 {
		return nil, refuseTool(refuseArg, "old_string occurs %d times in %s; exactly one occurrence is required", n, p)
	}
	updated := []byte(strings.Replace(string(body), old, replacement, 1))

	suffix, err := randomHex(6)
	if err != nil {
		return nil, refuseTool(refusePath, "%v", err)
	}
	tmp := "." + name + ".sensei-exec-" + suffix
	tfd, err := unix.Openat(dir, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, refuseTool(refusePath, "the replacement for %s cannot be staged: %v", p, err)
	}
	tf := os.NewFile(uintptr(tfd), tmp)
	_, werr := tf.Write(updated)
	if werr == nil {
		werr = unix.Fchmod(tfd, st.Mode&0o7777)
	}
	if werr == nil {
		werr = tf.Sync()
	}
	tf.Close()
	discard := func() { _ = unix.Unlinkat(dir, tmp, 0) }
	if werr != nil {
		discard()
		return nil, refuseTool(refusePath, "the replacement for %s cannot be written: %v", p, werr)
	}
	// Concurrent modification: the name must still be the inode that was
	// read, holding exactly the bytes the preimage named.
	now, again, err := readAt(dir, name)
	if err != nil || now.Dev != st.Dev || now.Ino != st.Ino || !bytes.Equal(again, body) {
		discard()
		return nil, refuseTool(refusePatchStale, "%s changed while the patch was being applied; nothing was written", p)
	}
	if err := unix.Renameat(dir, tmp, dir, name); err != nil {
		discard()
		return nil, refuseTool(refusePath, "the replacement for %s cannot be installed: %v", p, err)
	}
	_ = unix.Fsync(dir)
	return map[string]any{"path": p, "previous_sha256": before, "sha256": sha256Hex(updated), "replacements": 1}, nil
}

// occurrences counts every position sub starts at in s, overlapping ones
// included: "aa" occurs twice in "aaa", which is exactly the ambiguity an
// exactly-one replacement must refuse.
func occurrences(s, sub string) int {
	n := 0
	for i := strings.Index(s, sub); i >= 0; {
		n++
		next := strings.Index(s[i+1:], sub)
		if next < 0 {
			break
		}
		i += 1 + next
	}
	return n
}

// checkNamed resolves a check name to its configured kind and commands.
func (s *execService) checkNamed(name string) (validation.CheckKind, []config.Command, bool) {
	v := s.e.Config.Validation
	switch name {
	case "format_verify":
		return validation.Format, v.FormatVerify, true
	case "vet", "go_vet":
		return validation.Vet, v.Vet, true
	case "build", "go_build":
		return validation.Build, v.Build, true
	case "test", "go_test":
		return validation.Test, v.Test, true
	}
	return "", nil, false
}

// candidateDigest is the identity of the whole worktree a check runs in:
// every entry except the repository metadata, by path, type and mode, a
// regular file by its content and a symlink by its target, never followed. A
// check that changes anything there -- a planned file or not, tracked or
// not, a new file, a mode -- changes the digest and so invalidates its own
// evidence.
func (s *execService) candidateDigest(ctx context.Context) (string, error) {
	root := s.b.Worktree
	h := sha256.New()
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if rel == ".git" {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		content := ""
		switch mode := info.Mode(); {
		case mode.IsRegular():
			if content, err = regularFileSum(p); err != nil {
				return err
			}
		case mode&fs.ModeSymlink != 0:
			if content, err = os.Readlink(p); err != nil {
				return err
			}
		}
		fmt.Fprintf(h, "%d:%s\x00%s\x00%d:%s\n", len(rel), rel, info.Mode().String(), len(content), content)
		return nil
	})
	if err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}

// regularFileSum hashes the regular file at p without following a symlink.
func regularFileSum(p string) (string, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if info, err := f.Stat(); err != nil || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s is no longer a regular file", p)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (s *execService) runCheck(args toolArgs) (any, *toolRefusal) {
	if r := args.only("name"); r != nil {
		return nil, r
	}
	name, r := args.str("name", true)
	if r != nil {
		return nil, r
	}
	kind, commands, ok := s.checkNamed(name)
	if !ok {
		return nil, refuseTool(refuseCheck, "%q is not a configured check (format_verify, vet, build, test)", name)
	}
	checks := checksOf(kind, commands)
	if len(checks) == 0 {
		return nil, refuseTool(refuseCheck, "%q configures no command", name)
	}

	s.effects.Lock()
	defer s.effects.Unlock()
	gate := s.e.authorityGate(s.b.TaskID)
	gate.RLock()
	defer gate.RUnlock()
	op, r := s.revalidate()
	if r != nil {
		return nil, r
	}
	permits := validationPermits(broker.New(s.e.Config.Permissions))
	if ok, reason := permits(kind); !ok {
		return nil, refuseTool(refuseCheck, "%s is not permitted by the capability envelope: %s", name, reason)
	}
	c := s.e.Config.ExecBinding.Confinement
	if strings.TrimSpace(c.Command) == "" {
		return nil, refuseTool(refuseSandbox, "no D2 confinement profile is configured; the check was not run")
	}
	before, err := s.candidateDigest(s.ctx)
	if err != nil {
		return nil, refuseTool(refuseCheck, "the candidate could not be measured before the check, so nothing would bind its evidence: %v", err)
	}
	runner := validation.Runner{
		Workspace:   s.b.Worktree,
		Permits:     permits,
		RequestedBy: execRequestedBy,
		Confine: &validation.Confinement{Profile: c.Profile, Command: c.Command, Args: c.Args,
			SHA256: c.SHA256, FailureExit: c.FailureExit},
	}
	s.checking.Store(true)
	bundle := runner.Run(s.ctx, s.b.TaskID, before, checks)
	s.checking.Store(false)
	// The invocation may have ended while the check ran; its evidence then
	// belongs to nobody and is not recorded.
	if _, r := s.revalidate(); r != nil {
		return nil, r
	}
	record := bundle
	// What the check ran under travels in fields the record declares: the
	// session record refuses any other shape, and a refused record halts the
	// invocation.
	ranUnder := fmt.Sprintf("advisory tool-time check %s, invocation %s, plan attempt %s, base %s", name, s.b.Invocation, op.ID, s.b.BaseSHA)
	sandbox := ""
	for i := range record.Checks {
		c := &record.Checks[i]
		if sandbox == "" && strings.HasPrefix(c.Detail, refuseSandbox) {
			sandbox = strings.TrimPrefix(strings.TrimPrefix(c.Detail, refuseSandbox), ": ")
		}
		if c.Detail == "" {
			c.Detail = ranUnder
		} else {
			c.Detail = ranUnder + "; " + c.Detail
		}
	}
	invalidated := ""
	after, err := s.candidateDigest(s.ctx)
	if err != nil || after != before {
		invalidated = "the candidate changed while the check ran"
		if err != nil {
			invalidated = "the candidate could not be re-measured after the check: " + err.Error()
		}
		invalidate(&record, invalidated)
	}
	// The service's context is the invocation's own, so the record is bound
	// to the invocation that ran the check. The payload is exactly the
	// ValidationRun shape: the session record refuses anything else, and a
	// refused record halts the invocation.
	ctx := s.ctx
	s.e.emitIn(ctx, event.New(s.e.SessionID, s.b.TaskID, event.SourceSystem, event.ValidationRun, record.Render(), record))

	if sandbox != "" {
		return nil, refuseTool(refuseSandbox, "%s", sandbox)
	}
	var results []map[string]any
	for _, ev := range record.Checks {
		results = append(results, map[string]any{"command": ev.Command, "args": ev.Args, "outcome": ev.Outcome,
			"exit_status": ev.ExitStatus, "detail": ev.Detail, "output": ev.Output, "executed_by": ev.ExecutedBy})
	}
	return map[string]any{"check": name, "candidate_digest": record.DiffDigest, "invalidated": invalidated,
		"advisory": "tool-time evidence never certifies; the engine validates the captured candidate itself", "results": results}, nil
}

// invalidate strips evidence of the binding it no longer has. Every check
// becomes Errored and binds to no digest, so neither Certifies nor Passed can
// read it as proof about any bytes.
func invalidate(b *validation.Bundle, reason string) {
	b.DiffDigest = ""
	for i := range b.Checks {
		c := &b.Checks[i]
		c.Detail = strings.TrimSpace("EVIDENCE_INVALIDATED (" + reason + "; recorded outcome " + string(c.Outcome) + ") " + c.Detail)
		c.DiffDigest = ""
		c.Outcome = validation.Errored
	}
}

// ExecShim relays one MCP client's stdio to the execution service at socket,
// after establishing that the socket is the private one the engine made and
// that the service answering is the one named.
//
// It reads nothing from the candidate and takes no configuration but its two
// arguments, which the engine wrote into the client configuration outside the
// worktree.
func ExecShim(ctx context.Context, socket, service string, in io.Reader, out io.Writer) error {
	if strings.TrimSpace(service) == "" {
		return errors.New("no service identity was given")
	}
	if err := checkPrivateSocket(socket); err != nil {
		return err
	}
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", socket)
	if err != nil {
		return fmt.Errorf("the execution service is not reachable: %w", err)
	}
	conn := c.(*net.UnixConn)
	defer conn.Close()
	if err := writeJSONLine(conn, execHello{Hello: service}); err != nil {
		return err
	}
	r := bufio.NewReaderSize(conn, 64<<10)
	_ = conn.SetReadDeadline(time.Now().Add(10 * time.Second))
	line, err := readLine(r)
	if err != nil {
		return fmt.Errorf("the execution service did not answer: %w", err)
	}
	_ = conn.SetReadDeadline(time.Time{})
	var reply execHello
	if json.Unmarshal(line, &reply) != nil || reply.Service != service {
		return fmt.Errorf("the execution service at %s is not service %s", socket, service)
	}
	go func() {
		_, _ = io.Copy(conn, in)
		_ = conn.CloseWrite()
	}()
	_, err = io.Copy(out, r)
	return err
}

// checkPrivateSocket establishes that socket is a socket owned by this user
// in a directory only this user can enter, neither of them a symlink.
func checkPrivateSocket(socket string) error {
	if !filepath.IsAbs(socket) {
		return fmt.Errorf("the execution socket %q is not an absolute path", socket)
	}
	uid := uint32(os.Getuid())
	owner := func(info os.FileInfo) (uint32, bool) {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			return 0, false
		}
		return st.Uid, true
	}
	dir, err := os.Lstat(filepath.Dir(socket))
	if err != nil {
		return err
	}
	if u, ok := owner(dir); !dir.IsDir() || dir.Mode().Perm() != 0o700 || !ok || u != uid {
		return fmt.Errorf("the execution socket's directory %s is not a private directory of this user", filepath.Dir(socket))
	}
	info, err := os.Lstat(socket)
	if err != nil {
		return err
	}
	if u, ok := owner(info); info.Mode()&os.ModeSocket == 0 || !ok || u != uid {
		return fmt.Errorf("%s is not a socket owned by this user", socket)
	}
	return nil
}

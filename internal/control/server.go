package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/principal"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Server is the remote control surface for ONE running Sensei Code.
//
// It holds the engine the local surface holds — the same pointer, the same
// repository root, the same session store — and it keeps no task or workflow
// state of its own. That is the whole architectural point of this slice: a
// remote reader and a local one are looking at one thing, not at two copies
// somebody has to keep in step. Every read below goes to the canonical record;
// there is no cache here to go stale, because there is nothing here to cache.
//
// What it does own is the lease registry, and that is not workflow state. A
// lease is a live relationship with a connected party: who is currently holding
// which role. It is deliberately in-memory and deliberately lost on restart —
// see internal/principal.
type Server struct {
	engine *workflow.Engine
	cred   Credential
	leases *principal.Registry
	// workspace is the repository this surface is about, named by the domain
	// Sensei owns. It is resolved once at startup by the process that starts
	// the server and is never taken from a request: a client that could name
	// its own workspace could ask about a repository this instance does not
	// serve.
	workspace string

	ln  net.Listener
	srv *http.Server
}

// Options configure a control server. Everything here is decided by the
// operator or by the process, and nothing by a remote client.
type Options struct {
	// Addr is the listen address. It must be loopback; see Listen.
	Addr string
	// Workspace is the repository domain this instance serves.
	Workspace string
	// LeaseTTL is how long a role session holds without renewal.
	LeaseTTL time.Duration
	// Now is the clock, injectable so lease expiry is testable without waiting.
	Now func() time.Time
}

// DefaultAddr binds the loopback interface on a port the OS chooses.
//
// A fixed default port would be a second thing to collide on, and this surface
// is reached through a tunnel the operator sets up with the address this
// returns rather than by anybody guessing it.
const DefaultAddr = "127.0.0.1:0"

// New builds a control server. It does not listen; see Listen.
//
// It refuses rather than starting degraded. A control surface that came up
// without a credential, without a workspace, or without an engine would be a
// surface that answers questions it cannot attribute, about a repository it
// cannot name, from state it does not have.
func New(engine *workflow.Engine, cred Credential, opts Options) (*Server, error) {
	if engine == nil {
		return nil, errors.New("a control surface without an engine has nothing canonical to read")
	}
	if !cred.Configured() {
		return nil, errors.New("a control surface without a credential would authenticate everybody")
	}
	workspace := strings.TrimSpace(opts.Workspace)
	if workspace == "" {
		return nil, errors.New("a control surface must know which repository it serves")
	}
	return &Server{
		engine:    engine,
		cred:      cred,
		leases:    principal.NewRegistry(opts.LeaseTTL, opts.Now),
		workspace: workspace,
	}, nil
}

// Workspace is the repository this surface serves.
func (s *Server) Workspace() string { return s.workspace }

// Listen binds the address, refusing anything that is not loopback.
//
// There is no opt-in to a public interface in this slice, and the omission is
// the design. Exposure to a remote agent is a tunnel's job — a deliberate act
// by a person, outside this process — and an orchestration surface that could
// be published by setting a config key would eventually be published by setting
// a config key. Failing to establish the intended boundary is a refusal to
// start, never a quieter boundary than the one asked for.
func (s *Server) Listen(addr string) error {
	if strings.TrimSpace(addr) == "" {
		addr = DefaultAddr
	}
	if err := requireLoopback(addr); err != nil {
		return err
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	// Bound, and then checked again against what was actually bound. The
	// address asked for and the address obtained are different facts.
	if tcp, ok := ln.Addr().(*net.TCPAddr); ok && !tcp.IP.IsLoopback() {
		_ = ln.Close()
		return fmt.Errorf("refusing to serve the control surface on %s, which is not loopback", ln.Addr())
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	return nil
}

// Addr is what was actually bound, empty before Listen.
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Serve runs until Close. It refuses to serve a socket it never bound.
func (s *Server) Serve() error {
	if s.ln == nil {
		return errors.New("the control surface was asked to serve before it bound an address")
	}
	err := s.srv.Serve(s.ln)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Close stops serving.
func (s *Server) Close() error {
	if s.srv == nil {
		if s.ln != nil {
			return s.ln.Close()
		}
		return nil
	}
	return s.srv.Close()
}

// requireLoopback refuses an address that is not, or cannot be shown to be, the
// local machine.
//
// A name is resolved and EVERY answer must be loopback. "localhost" is
// ordinarily 127.0.0.1 and is a line in a file that anything on the machine can
// write, so accepting the name on trust would make the boundary a property of
// /etc/hosts. An empty host means every interface and is refused by name,
// because it is the specific mistake this function exists to catch.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("the control address %q is not host:port: %w", addr, err)
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("refusing to serve the control surface on %q, which binds every interface; use 127.0.0.1", addr)
	}
	if ip := net.ParseIP(host); ip != nil {
		if !ip.IsLoopback() {
			return fmt.Errorf("refusing to serve the control surface on %s, which is not loopback", host)
		}
		return nil
	}
	resolved, err := net.LookupIP(host)
	if err != nil {
		return fmt.Errorf("refusing to serve the control surface on %q, which does not resolve: %w", host, err)
	}
	if len(resolved) == 0 {
		return fmt.Errorf("refusing to serve the control surface on %q, which resolves to nothing", host)
	}
	for _, ip := range resolved {
		if !ip.IsLoopback() {
			return fmt.Errorf("refusing to serve the control surface on %q, which resolves to %s and is not loopback", host, ip)
		}
	}
	return nil
}

// Endpoint is the single path the surface answers on.
const Endpoint = "/mcp"

// Handler is the HTTP surface, exported so a test can drive the protocol
// without binding a socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(Endpoint, s.handleRPC)
	return mux
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	// Authentication is checked before the body is read, let alone parsed. An
	// unauthenticated caller must not be able to reach the parser, and must not
	// learn from the shape of the answer whether its request was well formed.
	if !s.cred.Authenticates(BearerToken(r.Header.Get("Authorization"))) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "the control surface answers POST", http.StatusMethodNotAllowed)
		return
	}

	var req rpcRequest
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err := dec.Decode(&req); err != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", Error: &rpcError{
			Code: codeParse, Message: "the request is not valid JSON-RPC: " + err.Error()}})
		return
	}
	if req.JSONRPC != "2.0" || strings.TrimSpace(req.Method) == "" {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{
			Code: codeInvalidRequest, Message: `every request must carry jsonrpc "2.0" and a method`}})
		return
	}

	// A notification carries no id and gets no response, per JSON-RPC.
	result, rpcErr := s.dispatch(req)
	if len(req.ID) == 0 {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	if rpcErr != nil {
		writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: rpcErr})
		return
	}
	writeRPC(w, rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: result})
}

// maxRequestBytes bounds a request so a malformed or hostile body cannot drive
// an unbounded read. The surface's inputs are a role list and two identifiers.
const maxRequestBytes = 1 << 20

func (s *Server) dispatch(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return initializeResult(), nil
	case "notifications/initialized":
		return nil, nil
	case "tools/list":
		return map[string]any{"tools": toolDescriptors()}, nil
	case "tools/call":
		return s.callTool(req.Params)
	}
	// Read by membership. An unknown method is refused rather than guessed at,
	// and the refusal names it so a client version mismatch is legible.
	return nil, &rpcError{Code: codeMethodNotFound,
		Message: fmt.Sprintf("this control surface has no method %q", req.Method)}
}

func initializeResult() map[string]any {
	return map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "sensei-code-control", "version": "1"},
		// Said in the handshake so a client cannot mistake reaching the surface
		// for holding a role on it.
		"instructions": "Authentication reaches this surface and grants no role. " +
			"Call register_role to request architect or reviewer, and present the returned " +
			"role session on every later call. This slice is read-only: no architecture or " +
			"review may be submitted, and nothing here advances a task.",
	}
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
)

func writeRPC(w http.ResponseWriter, resp rpcResponse) {
	w.Header().Set("Content-Type", "application/json")
	// The HTTP status stays 200 for a JSON-RPC error: the transport succeeded
	// and the call did not, and conflating those makes a client retry a
	// refusal.
	_ = json.NewEncoder(w).Encode(resp)
}

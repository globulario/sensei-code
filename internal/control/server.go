package control

import (
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/principal"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Server is the remote control surface for ONE running Sensei Code.
//
// The topology is two product surfaces over one workflow implementation and one
// canonical task model — not two frontends holding one Go pointer:
//
//	TUI mode      -> workflow kernel -> local interactive surface
//	CONTROL mode  -> workflow kernel -> remote MCP surface
//
// A `sensei-code control` process builds its own Engine, bus and session, the
// same way the TUI process does. It is NOT attached to a separately running
// TUI's memory, and no in-process rendezvous state crosses between them. What
// is shared is the thing that is durable: the repository, and the canonical
// task records under .sensei-code/tasks. That is what "one state model" means
// here, and it is the only claim this package is entitled to make.
//
// The invariant is therefore:
//
//	one workflow implementation
//	one canonical task model
//	one engine owner per active orchestrated run
//	no mirrored remote workflow
//
// The last line is what this server is arranged to keep. It holds no task,
// workflow or session state of its own: every read below goes to the canonical
// record, and there is no cache here to go stale because there is nothing here
// to cache. A remote reader and a local one see one thing because they read one
// thing, not because anything is kept in step.
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

// SupportedProtocolVersion is the one MCP revision this server implements.
//
// One, and stated rather than negotiated over a range. Advertising a version is
// a claim to implement its semantics, and a server that echoed whatever a
// client asked for would be claiming to implement every revision anybody names.
const SupportedProtocolVersion = "2025-06-18"

// protocolVersionHeader carries the negotiated revision on every request after
// initialization.
const protocolVersionHeader = "MCP-Protocol-Version"

// Handler is the HTTP surface, exported so a test can drive the protocol
// without binding a socket.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(Endpoint, s.handleRPC)
	return mux
}

func (s *Server) handleRPC(w http.ResponseWriter, r *http.Request) {
	// GET is the optional server-to-client SSE stream. This server does not
	// offer one, and says so with the status that means exactly that rather
	// than by opening a stream it will never write to.
	if r.Method == http.MethodGet {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "this control surface offers no server-to-client stream", http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "the control surface answers POST", http.StatusMethodNotAllowed)
		return
	}

	// Origin is validated on every incoming connection, before anything else
	// looks at the request. It is the DNS-rebinding guard: a page in a browser
	// on this machine can reach a loopback port, and without this check the
	// only thing standing between a visited web page and this surface would be
	// a token it cannot read -- which is true today and is not a reason to
	// leave the door open. A request with no Origin is an ordinary
	// server-to-server client and is fine; a request that HAS one must be from
	// this machine.
	if !originAllowed(r.Header.Get("Origin")) {
		http.Error(w, "this control surface does not accept that origin", http.StatusForbidden)
		return
	}

	// Authentication before the body is read, let alone parsed. An
	// unauthenticated caller must not reach the parser, and must not learn from
	// the shape of the answer whether its request was well formed.
	if !s.cred.Authenticates(BearerToken(r.Header.Get("Authorization"))) {
		w.Header().Set("WWW-Authenticate", "Bearer")
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if ct := r.Header.Get("Content-Type"); !isJSONContentType(ct) {
		http.Error(w, "the control surface reads application/json", http.StatusUnsupportedMediaType)
		return
	}
	if accept := r.Header.Get("Accept"); !acceptsJSON(accept) {
		// This implementation answers in JSON and never opens a stream, so a
		// client that will only take text/event-stream cannot be served. Saying
		// so is better than sending it JSON it declared it cannot read.
		http.Error(w, "this control surface answers application/json", http.StatusNotAcceptable)
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
	if status, err := s.checkProtocolVersion(r, req.Method); err != nil {
		http.Error(w, err.Error(), status)
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

// checkProtocolVersion applies this server's stateless header rule.
//
// The rule, and why it is this one. MCP lets a server issue an Mcp-Session-Id
// and recover the negotiated revision from it on later requests. This server
// issues none -- it holds no protocol session, only leases -- so there is
// nothing here from which a negotiated version could be recovered. That leaves
// two honest options for a request that names no version: assume an older
// revision, as the specification suggests for backwards compatibility, or
// refuse. Assuming would mean answering under semantics this server does not
// implement, decided by the absence of a header. So it refuses.
//
//	initialize          the header is optional; the version is being negotiated.
//	                    If present it must still be one this server implements.
//	everything else     the header is required and must name a supported
//	                    revision. Absent, empty or unsupported is 400.
func (s *Server) checkProtocolVersion(r *http.Request, method string) (int, error) {
	stated := strings.TrimSpace(r.Header.Get(protocolVersionHeader))
	if method == "initialize" {
		if stated == "" || stated == SupportedProtocolVersion {
			return 0, nil
		}
		return http.StatusBadRequest, fmt.Errorf("%s %q is not implemented by this server, which speaks %s",
			protocolVersionHeader, stated, SupportedProtocolVersion)
	}
	if stated == "" {
		return http.StatusBadRequest, fmt.Errorf(
			"%s is required on every request after initialize; this server keeps no protocol session to recover it from, and will not assume a revision it does not implement",
			protocolVersionHeader)
	}
	if stated != SupportedProtocolVersion {
		return http.StatusBadRequest, fmt.Errorf("%s %q is not implemented by this server, which speaks %s",
			protocolVersionHeader, stated, SupportedProtocolVersion)
	}
	return 0, nil
}

// originAllowed answers the DNS-rebinding question.
//
// No Origin is an ordinary non-browser client and is allowed: server-to-server
// callers do not send one, and requiring it would refuse every legitimate use
// of this surface. An Origin that IS present has to be this machine, because
// the attack this check exists for is a page on some other site persuading a
// browser to talk to a loopback port on the machine that is viewing it.
func originAllowed(origin string) bool {
	o := strings.TrimSpace(origin)
	if o == "" {
		return true
	}
	u, err := url.Parse(o)
	if err != nil {
		return false
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return false
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return strings.EqualFold(host, "localhost")
}

// isJSONContentType accepts application/json with or without parameters, and
// nothing else. A POST body that is not declared as JSON is not an MCP message
// this server can read, and treating any POST body as one is how a form
// submission becomes a tool call.
func isJSONContentType(value string) bool {
	media, _, err := mime.ParseMediaType(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	return strings.EqualFold(media, "application/json")
}

// acceptsJSON reports whether a client will take the answer this server gives.
//
// An absent Accept means no preference was stated and is allowed. The
// specification asks clients to send "application/json, text/event-stream";
// what matters here is only that JSON is acceptable, since JSON is all this
// implementation produces.
func acceptsJSON(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return true
	}
	for _, part := range strings.Split(v, ",") {
		media, _, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		switch strings.ToLower(media) {
		case "application/json", "application/*", "*/*":
			return true
		}
	}
	return false
}

// maxRequestBytes bounds a request so a malformed or hostile body cannot drive
// an unbounded read. The surface's inputs are a role list and two identifiers.
const maxRequestBytes = 1 << 20

func (s *Server) dispatch(req rpcRequest) (any, *rpcError) {
	switch req.Method {
	case "initialize":
		return s.initialize(req.Params)
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

// initializeParams is the client half of the MCP handshake.
//
// Decoded strictly. The three fields below are what the revision this server
// implements defines, and a payload carrying something else is a client
// speaking a protocol this server does not -- which is exactly the case the
// handshake exists to discover, and exactly the case a lenient decoder would
// hide by succeeding.
type initializeParams struct {
	ProtocolVersion string          `json:"protocolVersion"`
	Capabilities    json.RawMessage `json:"capabilities"`
	ClientInfo      struct {
		Name    string `json:"name"`
		Title   string `json:"title,omitempty"`
		Version string `json:"version"`
	} `json:"clientInfo"`
}

// initialize answers the handshake.
//
// An empty or malformed initialize is refused rather than treated as a
// successful one. "Initialized" is a state the rest of the protocol depends on,
// and a server that reached it by ignoring the request would be agreeing to
// terms nobody stated.
//
// When the client names a revision this server does not implement, the answer
// is the revision this server DOES implement -- which is what the specification
// requires, and which leaves the client to decide whether it can proceed. The
// one thing that must not happen is echoing the client's version back, because
// that is a claim to implement semantics this code has never seen.
func (s *Server) initialize(raw json.RawMessage) (any, *rpcError) {
	var p initializeParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: "initialize: " + err.Error()}
	}
	if strings.TrimSpace(p.ProtocolVersion) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "initialize must name the protocol version the client speaks"}
	}
	if strings.TrimSpace(p.ClientInfo.Name) == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "initialize must name the client"}
	}
	return map[string]any{
		"protocolVersion": SupportedProtocolVersion,
		"capabilities":    map[string]any{"tools": map[string]any{}},
		"serverInfo":      map[string]any{"name": "sensei-code-control", "version": "1"},
		// Said in the handshake so a client cannot mistake reaching the surface
		// for holding a role on it.
		"instructions": "Authentication reaches this surface and grants no role. " +
			"Call register_role to request architect or reviewer, and present the returned " +
			"role session on every later call. This slice is read-only: no architecture or " +
			"review may be submitted, and nothing here advances a task.",
	}, nil
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

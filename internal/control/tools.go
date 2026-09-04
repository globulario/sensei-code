package control

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/principal"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/taskstate"
)

// The tool surface is semantic and deliberately small.
//
// There is no tool here that runs a command, writes a file, constructs a worker
// invocation, mutates a task record, or admits a change — and in this slice
// there is none that submits an architectural decision or a review either. A
// surface that cannot express an operation cannot be argued into performing it,
// which is a stronger guarantee than a surface that can express it and checks.
//
// Every tool that touches a task passes through heldLease. Reaching this
// surface is authentication; doing anything on it requires a lease that is
// held, that grants the operation, and that belongs to the party presenting it.

// toolDescriptors is what tools/list returns. Written out rather than derived
// from the handlers, because the description is the contract a remote model
// reads, and a generated one would describe the signature rather than the
// meaning.
func toolDescriptors() []map[string]any {
	return []map[string]any{
		{
			"name": "register_role",
			"description": "Request one or more roles on this Sensei Code instance. Requesting is not receiving: " +
				"each role is granted or refused with a reason. The principal a decision is attributed to is " +
				"derived from your credential and cannot be supplied. Returns a role session per granted role; " +
				"present it on every later call.",
			"inputSchema": map[string]any{
				"type": "object", "required": []string{"roles"},
				"additionalProperties": false,
				"properties": map[string]any{
					"roles": map[string]any{"type": "array", "items": map[string]any{"type": "string"},
						"description": "architect and/or reviewer"},
					"client":   map[string]any{"type": "string", "description": "client name, recorded as evidence only"},
					"protocol": map[string]any{"type": "string", "description": "client protocol/version, recorded as evidence only"},
					"label":    map[string]any{"type": "string", "description": "display label, recorded as evidence only; it is not an identity"},
				},
			},
		},
		{
			"name":        "release_role",
			"description": "Give a role back. Releasing is a decision and is recorded as one, distinct from a lease running out.",
			"inputSchema": leaseSchema(),
		},
		{
			"name": "renew_role",
			"description": "Extend a role session that is still held. It cannot revive one that was released or that expired, " +
				"cannot change its role, cannot move its task binding, and cannot widen what it grants.",
			"inputSchema": leaseSchema(),
		},
		{
			"name": "get_work",
			"description": "List the tasks this instance holds canonical state for, with the workflow phase each is in. " +
				"Read-only: observing a task does not advance it.",
			"inputSchema": leaseSchema(),
		},
		{
			"name": "inspect_task",
			"description": "Read one task's canonical state: identity, workflow phase, base and candidate identity, " +
				"architectural contract, authority decisions, open findings, evidence and workers. Every field carries " +
				"whether it is present, proven empty, absent, stale or unavailable; an empty field is not an answer.",
			"inputSchema": map[string]any{
				"type": "object", "required": []string{"role_session", "task"},
				"additionalProperties": false,
				"properties": map[string]any{
					"role_session": map[string]any{"type": "string"},
					"task":         map[string]any{"type": "string"},
				},
			},
		},
	}
}

func leaseSchema() map[string]any {
	return map[string]any{
		"type": "object", "required": []string{"role_session"},
		"additionalProperties": false,
		"properties":           map[string]any{"role_session": map[string]any{"type": "string"}},
	}
}

type callParams struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
}

func (s *Server) callTool(raw json.RawMessage) (any, *rpcError) {
	var call callParams
	if err := decodeStrict(raw, &call); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	switch call.Name {
	case "register_role":
		return s.registerRole(call.Arguments)
	case "release_role":
		return s.releaseRole(call.Arguments)
	case "renew_role":
		return s.renewRole(call.Arguments)
	case "get_work":
		return s.getWork(call.Arguments)
	case "inspect_task":
		return s.inspectTask(call.Arguments)
	}
	// Read by membership. An unknown tool is refused rather than approximated.
	return nil, &rpcError{Code: codeInvalidParams,
		Message: fmt.Sprintf("this control surface has no tool %q", call.Name)}
}

// decodeStrict refuses a payload with fields this surface does not define.
//
// Strict rather than lenient, and the reason is specific rather than tidiness:
// the fields a client must NOT be able to send are principal, workspace and
// authority, and a lenient decoder ignores exactly those silently. A client
// that sends {"principal": "someone-else"} must be told no, not quietly
// answered as itself — because the version of this bug that matters is the one
// where the client believes it worked.
func decodeStrict(raw json.RawMessage, dst any) error {
	if len(bytes.TrimSpace(raw)) == 0 {
		raw = json.RawMessage("{}")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return fmt.Errorf("the arguments are not what this tool accepts: %w", err)
	}
	if dec.More() {
		return errors.New("the arguments carry trailing content")
	}
	return nil
}

type registerParams struct {
	Roles    []string `json:"roles"`
	Client   string   `json:"client,omitempty"`
	Protocol string   `json:"protocol,omitempty"`
	// Label is a display name and nothing else. It is recorded as evidence and
	// consulted by nothing: the identity this registration is attributed to
	// comes from the credential, so two labels are one party and always were.
	Label string `json:"label,omitempty"`
}

func (s *Server) registerRole(raw json.RawMessage) (any, *rpcError) {
	var p registerParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if len(p.Roles) == 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "register_role was asked for no roles"}
	}

	want := make([]roles.Role, 0, len(p.Roles))
	for _, r := range p.Roles {
		want = append(want, roles.Role(strings.ToLower(strings.TrimSpace(r))))
	}
	// The role vocabulary, what may be leased, and what each role grants are
	// all internal/principal's. Filtering here would be a second opinion about
	// the same question, and the two would drift.
	reg, err := s.leases.Register(principal.Request{
		Principal: s.cred.Principal(),
		Workspace: s.workspace,
		Roles:     want,
		Client:    p.Client,
		Protocol:  p.Protocol,
	})
	if err != nil {
		return toolError(err.Error()), nil
	}

	granted := make([]map[string]any, 0, len(reg.Granted))
	for _, l := range reg.Granted {
		granted = append(granted, leaseView(l))
	}
	refused := make([]map[string]any, 0, len(reg.Refused))
	for _, r := range reg.Refused {
		refused = append(refused, map[string]any{"role": string(r.Role), "reason": r.Reason})
	}
	return toolResult(map[string]any{
		"principal": string(reg.Principal),
		"workspace": reg.Workspace,
		"label":     strings.TrimSpace(p.Label),
		"granted":   granted,
		"refused":   refused,
		"notice": "This role session grants what its capabilities list says and nothing else. " +
			"This slice is read-only: no architecture or review can be submitted, nothing here advances a task, " +
			"and no review conducted through this surface can be independent.",
	}), nil
}

// leaseView is what a client is told about its own lease. The registry's
// internals are not handed out: this is the identity, what it grants, and when
// it runs out.
func leaseView(l principal.Lease) map[string]any {
	caps := make([]string, 0, len(l.Capabilities))
	for _, c := range l.Capabilities {
		caps = append(caps, string(c))
	}
	view := map[string]any{
		"role_session": l.ID,
		"role":         string(l.Role),
		"state":        string(l.State),
		"capabilities": caps,
		"authority":    l.Authority.String(),
		"granted_at":   l.GrantedAt,
		"expires_at":   l.ExpiresAt,
	}
	if l.Task != "" {
		view["task"] = l.Task
	}
	if !l.EndedAt.IsZero() {
		view["ended_at"] = l.EndedAt
	}
	if !l.RenewedAt.IsZero() {
		view["renewed_at"] = l.RenewedAt
	}
	return view
}

type leaseParams struct {
	RoleSession string `json:"role_session"`
}

// heldLease is the one gate. It answers whether THIS party may do THIS thing on
// THIS task right now, and it refuses rather than infers.
//
// The principal check is not redundant with the registry's. The registry knows
// whether a lease is held; it does not know who is asking. A lease identifier
// presented by a different credential is a different party using somebody
// else's role, and only this layer can see that.
func (s *Server) heldLease(id string, c principal.Capability, taskID string) (principal.Lease, *rpcError) {
	lease, err := s.leases.Authorize(id, c, s.workspace, taskID)
	if err != nil {
		return principal.Lease{}, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if lease.Principal != s.cred.Principal() {
		return principal.Lease{}, &rpcError{Code: codeInvalidParams,
			Message: "that role session is held by another principal"}
	}
	return lease, nil
}

// ownLease is heldLease for the two operations that are about the lease itself
// rather than about a task. Releasing and renewing are not capabilities in the
// vocabulary — they are things a holder does to its own role session — so they
// check holding and ownership without asking for a grant that does not exist.
func (s *Server) ownLease(id string) (principal.Lease, *rpcError) {
	lease, ok := s.leases.Lookup(id)
	if !ok {
		return principal.Lease{}, &rpcError{Code: codeInvalidParams, Message: principal.ErrNoSuchLease.Error()}
	}
	if lease.Principal != s.cred.Principal() {
		return principal.Lease{}, &rpcError{Code: codeInvalidParams,
			Message: "that role session is held by another principal"}
	}
	return lease, nil
}

func (s *Server) releaseRole(raw json.RawMessage) (any, *rpcError) {
	var p leaseParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if _, rpcErr := s.ownLease(p.RoleSession); rpcErr != nil {
		return nil, rpcErr
	}
	released, err := s.leases.Release(p.RoleSession)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return toolResult(map[string]any{"released": leaseView(released)}), nil
}

func (s *Server) renewRole(raw json.RawMessage) (any, *rpcError) {
	var p leaseParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	before, rpcErr := s.ownLease(p.RoleSession)
	if rpcErr != nil {
		return nil, rpcErr
	}
	renewed, err := s.leases.Renew(p.RoleSession)
	if err != nil {
		// A released or expired lease is not revived. The honest answer is the
		// refusal and the reason, and the client's move is to register again --
		// which produces a NEW authority relationship rather than rewriting the
		// one that ended.
		return toolError(err.Error()), nil
	}
	// Renewal moves one thing. Asserted here rather than assumed, because the
	// registry and this surface are separately maintained and a renewal that
	// quietly changed a role or a task binding would be invisible to a client
	// that only reads the new expiry.
	if renewed.Role != before.Role || renewed.Task != before.Task ||
		len(renewed.Capabilities) != len(before.Capabilities) || renewed.Authority != before.Authority {
		return toolError("renewal would have changed what this role session is, so it was refused"), nil
	}
	return toolResult(map[string]any{"renewed": leaseView(renewed)}), nil
}

func (s *Server) getWork(raw json.RawMessage) (any, *rpcError) {
	var p leaseParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	lease, rpcErr := s.heldLease(p.RoleSession, principal.InspectTask, "")
	if rpcErr != nil {
		return nil, rpcErr
	}

	root := s.engine.Repo.Root
	ids, err := taskstate.List(root)
	if err != nil {
		return toolError("the canonical task record could not be read: " + err.Error()), nil
	}
	work := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		// Read, and only read. Nothing on this path calls the engine, resolves
		// a runner, or writes a record: observing what a task is waiting for
		// must not be what moves it.
		state, found, err := taskstate.Load(root, id)
		if err != nil {
			work = append(work, map[string]any{"task": id, "record": noRecord(id)})
			continue
		}
		work = append(work, workView(id, state, found))
	}
	return toolResult(map[string]any{
		"workspace": s.workspace,
		"role":      string(lease.Role),
		"work":      work,
		"notice": "This surface is read-only. No task here is waiting on a decision from this role, " +
			"because remote architecture and review submission do not exist in this slice.",
	}), nil
}

type inspectParams struct {
	RoleSession string `json:"role_session"`
	Task        string `json:"task"`
}

func (s *Server) inspectTask(raw json.RawMessage) (any, *rpcError) {
	var p inspectParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	taskID := strings.TrimSpace(p.Task)
	if taskID == "" {
		return nil, &rpcError{Code: codeInvalidParams, Message: "inspect_task was asked about no task"}
	}
	if _, rpcErr := s.heldLease(p.RoleSession, principal.InspectTask, taskID); rpcErr != nil {
		return nil, rpcErr
	}

	root := s.engine.Repo.Root
	state, found, err := taskstate.Load(root, taskID)
	if err != nil {
		// A record that exists and cannot be read is not an absent one. Saying
		// "absent" here would tell a remote architect that nothing is known
		// about this task, when what is true is that we could not find out.
		return toolResult(map[string]any{
			"task": taskID, "workspace": s.workspace,
			"record": unreadable(taskID, err),
		}), nil
	}
	view := taskView(taskID, state, found)
	view["workspace"] = s.workspace
	return toolResult(view), nil
}

// toolResult renders structured content in the shape this project's own MCP
// client reads, with a text rendering beside it for a reader that only takes
// text. The structured half is the contract; the text is presentation.
func toolResult(structured map[string]any) map[string]any {
	text, err := json.MarshalIndent(structured, "", "  ")
	if err != nil {
		text = []byte("{}")
	}
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": string(text)}},
		"structuredContent": structured,
		"isError":           false,
	}
}

// toolError is a refusal the client asked for honestly and did not get. It is
// a tool error rather than a JSON-RPC error because the protocol succeeded: the
// question was well formed and the answer is no.
func toolError(message string) map[string]any {
	return map[string]any{
		"content":           []map[string]any{{"type": "text", "text": message}},
		"structuredContent": map[string]any{"refused": message},
		"isError":           true,
	}
}

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

// The tool surface is semantic and deliberately small: five reads and two typed
// submissions.
//
// There is no tool here that runs a command, writes a file, constructs a worker
// invocation, mutates a task record, admits a change, publishes, merges, or
// creates a task. A surface that cannot express an operation cannot be argued
// into performing it, which is a stronger guarantee than a surface that can
// express it and checks.
//
// The absence of a task-creating verb is the load-bearing one. An architect
// lease is architectural authority, never human objective authority: holding
// one must not mean this principal may invent development work and cause
// workers to execute it.
//
// The two submissions answer a question the engine already asked. There is no
// queue, no "hold this until it is wanted", and no way to answer a question
// nobody asked. Every tool that touches a task passes through heldLease:
// reaching this surface is authentication; doing anything on it requires a
// lease that is held, that grants the operation, and that belongs to the party
// presenting it.

// RoleContract is what a remote role holder may and may not do, in one
// sentence, said in the handshake and in every grant.
//
// It lives in one place because it used to live in three, and after the
// submission verbs shipped all three still said the surface was read-only.
// Product wording that contradicts the product is worse than none: a client is
// entitled to believe what the server tells it about itself.
const RoleContract = "A remote architect or reviewer may answer the exact turns this engine issues, and nothing else: it may not create tasks, execute workers, mutate candidates, claim independence, admit, publish or merge."

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
			"name": "submit_architecture",
			"description": "Answer an open architect turn with a bounded architectural decision. It is parsed and " +
				"checked by exactly the path a local architect's answer takes: claims are resolved against the graph, " +
				"scope and certifiability are routed, and a decision crossing human authority escalates. " +
				"It requires the exact turn_id the engine issued; you do not choose which task it is about.",
			"inputSchema": map[string]any{
				"type": "object", "required": []string{"role_session", "turn_id", "decision"},
				"additionalProperties": false,
				"properties": map[string]any{
					"role_session": map[string]any{"type": "string"},
					"turn_id":      map[string]any{"type": "string"},
					"decision": map[string]any{"type": "object",
						"description": "the bounded architect decision, in the same shape a local architect returns"},
				},
			},
		},
		{
			"name": "submit_review",
			"description": "Answer an open reviewer turn with accept, revise or escalate. It is about the exact " +
				"candidate the turn names; a verdict that arrives after the worker has revised is refused as stale. " +
				"A review given through this surface is ADVISORY: this project cannot observe whether your context " +
				"was isolated from the work, so it satisfies no adversarial-review obligation and is never admission.",
			"inputSchema": map[string]any{
				"type": "object", "required": []string{"role_session", "turn_id", "review"},
				"additionalProperties": false,
				"properties": map[string]any{
					"role_session": map[string]any{"type": "string"},
					"turn_id":      map[string]any{"type": "string"},
					"review": map[string]any{"type": "object",
						"description": "decision (accept|revise|escalate), summary, instructions, findings"},
				},
			},
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
	case "submit_architecture":
		return s.submitArchitecture(call.Arguments)
	case "submit_review":
		return s.submitReview(call.Arguments)
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
			RoleContract + " No review conducted through this surface can be independent: " +
			"this project cannot observe whether your context was isolated from the work it judges.",
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
	// Giving the role back must not leave the engine waiting on a party that
	// has left. Every turn this session was asked wakes with a typed refusal,
	// which enters the ordinary recovery ladder rather than hanging the run.
	woken := s.turns.AbandonSession(p.RoleSession, fmt.Errorf("%w: the role session was released", ErrTurnAbandoned))
	return toolResult(map[string]any{"released": leaseView(released), "turns_abandoned": woken}), nil
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
	recorded := make(map[string]bool, len(ids))
	for _, id := range ids {
		recorded[id] = true
		// Read, and only read. Nothing on this path calls the engine, resolves
		// a runner, or writes a record: observing what a task is waiting for
		// must not be what moves it.
		state, found, err := taskstate.Load(root, id)
		if err != nil {
			work = append(work, map[string]any{"task": id, "record": noRecord(id)})
			continue
		}
		view := workView(id, state, found)
		if turn, ok := s.waitingTurnFor(id, lease); ok {
			view["waiting_on"] = string(turn.Role)
			view["turn"] = turnView(turn)
		}
		work = append(work, view)
	}
	// A turn may be open for a task that has no canonical record yet -- the
	// architect is asked before anything is written. Reporting only recorded
	// tasks would hide exactly the turn this role exists to answer.
	for _, turn := range s.turns.Waiting("") {
		if turn.RoleSession != lease.ID || recorded[turn.TaskID] {
			continue
		}
		work = append(work, map[string]any{
			"task": turn.TaskID, "record": noRecord(turn.TaskID),
			"waiting_on": string(turn.Role), "turn": turnView(turn),
		})
	}
	return toolResult(map[string]any{
		"workspace": s.workspace,
		"role":      string(lease.Role),
		"work":      work,
		"notice": "A turn listed here is a question the engine is waiting on; answer it with " +
			"submit_architecture or submit_review, presenting its exact turn_id. Nothing here advances a task, " +
			"and a review given through this surface is advisory.",
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

// The two submission verbs, and nothing else.
//
// Each requires an active lease that grants the operation, held by the party
// presenting it, and the EXACT turn the engine issued. Calling one when the
// workflow is not waiting for it refuses: there is no queue here, no "hold this
// until it is wanted", and no way to answer a question nobody asked.
//
// Neither reads the payload for identity. What a submission is ABOUT was
// decided when the turn was opened -- which task, which base, which exact
// candidate -- and the payload supplies only what the party thinks.

type submitParams struct {
	RoleSession string          `json:"role_session"`
	TurnID      string          `json:"turn_id"`
	Decision    json.RawMessage `json:"decision,omitempty"`
	Review      json.RawMessage `json:"review,omitempty"`
}

func (s *Server) submitArchitecture(raw json.RawMessage) (any, *rpcError) {
	var p submitParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if len(bytes.TrimSpace(p.Decision)) == 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "submit_architecture carried no decision"}
	}
	if len(bytes.TrimSpace(p.Review)) != 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "submit_architecture does not take a review"}
	}
	// Handed on verbatim. The engine's existing architect parser decides
	// whether this is a bounded decision, and a weaker check here would be a
	// second opinion that the strict one never sees.
	turn, err := s.submitTurn(roles.Architect, p.RoleSession, p.TurnID, p.Decision)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return toolResult(map[string]any{
		"accepted_for": turnView(turn),
		"notice": "The decision was delivered to the turn that asked for it. It now enters the same " +
			"contradiction, scope and certifiability checks a local architect's answer enters, and it may " +
			"still be refused or escalated by them.",
	}), nil
}

func (s *Server) submitReview(raw json.RawMessage) (any, *rpcError) {
	var p submitParams
	if err := decodeStrict(raw, &p); err != nil {
		return nil, &rpcError{Code: codeInvalidParams, Message: err.Error()}
	}
	if len(bytes.TrimSpace(p.Review)) == 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "submit_review carried no review"}
	}
	if len(bytes.TrimSpace(p.Decision)) != 0 {
		return nil, &rpcError{Code: codeInvalidParams, Message: "submit_review does not take an architecture decision"}
	}
	turn, err := s.submitTurn(roles.Reviewer, p.RoleSession, p.TurnID, p.Review)
	if err != nil {
		return toolError(err.Error()), nil
	}
	return toolResult(map[string]any{
		"accepted_for": turnView(turn),
		"review_kind":  "advisory",
		"notice": "Delivered to the turn that asked for it, about the exact candidate that turn names. " +
			"This review is advisory: this project cannot observe whether your context was isolated from the " +
			"work it judges, so it satisfies no adversarial-review obligation and is not Sensei admission, " +
			"verification or completion.",
	}), nil
}

// waitingTurnFor reports the open turn this lease was asked, if any.
func (s *Server) waitingTurnFor(taskID string, lease principal.Lease) (Turn, bool) {
	for _, t := range s.turns.Waiting(taskID) {
		if t.RoleSession == lease.ID {
			return t, true
		}
	}
	return Turn{}, false
}

// turnView is what a role holder is told about the question it was asked.
//
// It carries the request the engine issued and the exact subject binding, which
// is what makes the turn answerable. It carries no credential, no argv, no
// worktree path and no worker capability: the remote role decides, and the
// mechanics of carrying that decision out are not its business.
func turnView(t Turn) map[string]any {
	view := map[string]any{
		"turn_id":    t.ID,
		"task":       t.TaskID,
		"role":       string(t.Role),
		"request":    t.Request,
		"created_at": t.CreatedAt,
		"expires_at": t.ExpiresAt,
	}
	subject := map[string]any{"task": t.Binding.TaskID}
	if t.Binding.BaseSHA != "" {
		subject["base_sha"] = t.Binding.BaseSHA
	}
	if t.Binding.CandidateDigest != "" {
		subject["candidate_digest"] = t.Binding.CandidateDigest
	}
	if t.Binding.CandidateTree != "" {
		subject["candidate_tree"] = t.Binding.CandidateTree
	}
	view["subject"] = subject
	return view
}

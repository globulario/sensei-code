// Package principal is who a remote agent is, and what a remote agent has been
// granted — kept apart from what a remote agent can physically call.
//
// A capable agent reaching Sensei Code over a network can call whatever the
// transport exposes. That is capability, and this package exists because
// capability is not authority. A remote client asking for the architect role is
// making a request; whether it holds that role, over which workspace, for how
// long, and with which operations actually granted, is decided here and
// recorded here.
//
// Three rules are enforced by type rather than by prompt or by convention:
//
//	A remote principal never holds a role that mutates a candidate.
//	A remote principal never holds human authority.
//	A role that is let go and a role that timed out are different facts.
//
// The third looks like bookkeeping and is not. A vanished client must not leave
// the architect role occupied forever, so leases expire — and once they do, the
// record has to distinguish an agent that finished and released from an agent
// that stopped answering. Collapsing those into "not active" would make a
// disconnected architect indistinguishable from a completed one in the evidence.
//
// Nothing here decides anything governed. A lease grants the right to be ASKED
// for an architectural decision or a review; Sensei owns admission, and the
// adversarial-independence question this package types is answered in
// qualify.go by refusing to claim independence, never by granting it.
package principal

import (
	"strings"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/roles"
)

// ID is a remote principal: one identity on the far side of the control
// surface, independent of which model or vendor is behind it.
//
// Deliberately not a provider name. A provider is an adapter this repository
// configures and launches; a principal is a party that connected. The
// distinction is what lets two remote agents be told apart when the later,
// stronger scenario — one principal architects, a different one reviews —
// needs them to be.
type ID string

func (i ID) String() string { return string(i) }

// Known reports whether this identity was actually stated. An unstated
// principal is not an anonymous one that may proceed; it is a party nothing can
// be attributed to, and attribution is the whole point.
func (i ID) Known() bool { return strings.TrimSpace(string(i)) != "" }

// Capability is one operation a lease actually grants.
//
// The vocabulary is closed and it is deliberately semantic. There is no
// capability here for running a command, writing a file, constructing a worker
// invocation, mutating a task record, or admitting a change — not because those
// are guarded elsewhere, but because a remote role holder has no business
// naming them at all. A surface that cannot express "run this" cannot be
// tricked into running it.
type Capability string

const (
	// InspectTask reads the canonical task: identity, state, evidence,
	// decisions, findings. It advances nothing.
	InspectTask Capability = "inspect_task"
	// SubmitArchitecture returns a bounded architectural decision when the
	// workflow asks the architect for one.
	SubmitArchitecture Capability = "submit_architecture"
	// SubmitReview returns a bounded verdict about an exact candidate when the
	// workflow asks the reviewer for one.
	SubmitReview Capability = "submit_review"
)

// AllCapabilities is the closed set. A capability outside it is refused rather
// than treated as an extension: an unknown capability has no grant rule, and
// accepting one would mean authorizing an operation nobody wrote a rule for.
var AllCapabilities = []Capability{InspectTask, SubmitArchitecture, SubmitReview}

// Valid reads the closed set by membership.
//
// By membership, and never by excluding the ones we happen to remember. A
// predicate written as "not implement and not admit" answers yes for every
// operation invented after it was written, which is the direction that fails
// open.
func (c Capability) Valid() bool {
	for _, known := range AllCapabilities {
		if c == known {
			return true
		}
	}
	return false
}

func (c Capability) String() string { return string(c) }

// Leasable reports whether a role may be held by a remote principal at all.
//
// Architect and reviewer, and nothing else. Implementer is excluded because a
// remote role holder must never mutate a candidate — the worktree, the worker
// invocation and the capability envelope stay owned by Sensei Code, and a
// remote party that could take the implementer role would be choosing what runs
// in that worktree. counterexample_hunter and proof_runner are excluded because
// neither has a driver anywhere in this repository; granting an unfillable role
// would make an unassignable job look like an available one.
func Leasable(r roles.Role) bool {
	return r == roles.Architect || r == roles.Reviewer
}

// CapabilitiesFor is what a role actually grants, which is narrower than what
// the role is about. Both leasable roles may read the task; each may submit
// exactly the one artifact its role produces.
//
// A reviewer that also wants to submit architecture must hold the architect
// lease too. Holding the reviewer lease and calling submit_architecture is the
// canonical case this function exists to refuse: the call is physically
// available and the authority is not.
func CapabilitiesFor(r roles.Role) []Capability {
	switch r {
	case roles.Architect:
		return []Capability{InspectTask, SubmitArchitecture}
	case roles.Reviewer:
		return []Capability{InspectTask, SubmitReview}
	}
	return nil
}

// authorityFor is the level a role carries when a remote principal holds it.
//
// The architect holds architectural authority, bounded exactly as a local
// architect's is: inside the region Sensei can certify, and nowhere else. The
// reviewer holds execution authority, because a reviewer decides how this run
// proceeds and decides nothing about the architecture — a reviewer that wants
// to change the design escalates to the architect.
//
// authority.Human appears in neither, and no code path in this package can
// produce it. Human intent, invariants, contracts and trust boundaries are not
// reachable from a lease, which is the mechanical form of "a remote role holder
// cannot change human-owned intent".
func authorityFor(r roles.Role) (authority.Level, bool) {
	switch r {
	case roles.Architect:
		return authority.Architectural, true
	case roles.Reviewer:
		return authority.Execution, true
	}
	return 0, false
}

// Request is what a remote client asks for. Every field is a claim; the only
// thing a request decides is what was asked.
type Request struct {
	// Principal identifies the party connecting.
	//
	// Today it is a label the client states, and it is therefore worth exactly
	// what the thing that established it is worth — which, over an
	// unauthenticated transport, is nothing. Nothing in this package lets a
	// stated identity buy authority it was not granted, and qualify.go
	// deliberately refuses to read two different stated names as two different
	// review contexts.
	//
	// The transport that eventually carries this must not treat a
	// client-supplied identity as the authority-bearing one. A client label may
	// be recorded as evidence; the identity a decision is attributed to has to
	// be minted or bound by the authenticated transport.
	Principal ID `json:"principal"`
	// Workspace is the repository this registration is about, named by the
	// domain Sensei owns rather than by a local path: two checkouts of one
	// repository are the same workspace, and a path is a fact about a machine.
	Workspace string `json:"workspace"`
	// Roles are the roles requested. Requesting is not holding.
	Roles []roles.Role `json:"roles"`
	// Client and Protocol are what the far side says it is. They are evidence
	// only — recorded so a decision can be traced to the software that made
	// it, and consulted by nothing that grants anything.
	Client   string `json:"client,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// Refusal is a role that was asked for and not granted, with the reason a
// person can act on.
//
// Refusals are returned rather than dropped. A registration that silently
// returns fewer roles than were requested leaves the client to infer which ones
// it holds, and a client that infers wrongly discovers the answer by having a
// submission rejected halfway through a task.
type Refusal struct {
	Role   roles.Role `json:"role"`
	Reason string     `json:"reason"`
}

// Registration is the answer to one Request: what was granted, what was
// refused, and to whom.
type Registration struct {
	Principal ID     `json:"principal"`
	Workspace string `json:"workspace"`
	// Granted holds one lease per granted role. One lease per role rather than
	// one lease carrying several: the roles are released, renewed and expired
	// independently, and the later scenario where one principal architects and
	// a different one reviews needs them to be separately held.
	Granted []Lease `json:"granted,omitempty"`
	// Refused holds every role asked for and not granted.
	Refused []Refusal `json:"refused,omitempty"`
}

// Holds reports whether this registration granted a usable lease for a role.
func (r Registration) Holds(role roles.Role) bool {
	_, ok := r.Lease(role)
	return ok
}

// Lease returns the granted lease for a role.
func (r Registration) Lease(role roles.Role) (Lease, bool) {
	for _, l := range r.Granted {
		if l.Role == role {
			return l, true
		}
	}
	return Lease{}, false
}

// Roles is what this registration actually granted, in the order granted.
func (r Registration) Roles() []roles.Role {
	out := make([]roles.Role, 0, len(r.Granted))
	for _, l := range r.Granted {
		out = append(out, l.Role)
	}
	return out
}

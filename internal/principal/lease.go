package principal

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/roles"
)

// LeaseState is where a role session stands. The vocabulary is closed and the
// three values are genuinely different facts, not three shades of one.
type LeaseState string

const (
	// Active means the principal holds the role now.
	Active LeaseState = "active"
	// Released means the principal gave the role back. Somebody decided.
	Released LeaseState = "released"
	// Expired means the lease ran out. Nobody decided anything; the client
	// stopped renewing, and the role was reclaimed so it would not stay
	// occupied by a party that is no longer there.
	Expired LeaseState = "expired"
)

// AllLeaseStates is the closed set.
var AllLeaseStates = []LeaseState{Active, Released, Expired}

func (s LeaseState) Valid() bool {
	for _, known := range AllLeaseStates {
		if s == known {
			return true
		}
	}
	return false
}

// Holds reports whether the role is held right now.
//
// Written as equality against Active rather than as "not released and not
// expired". The excluding form answers yes for every state added after it was
// written, and a lease state nobody has taught this predicate about would be
// read as a lease that holds. Membership fails closed; exclusion fails open.
func (s LeaseState) Holds() bool { return s == Active }

func (s LeaseState) String() string { return string(s) }

// Lease is one role, held by one principal, over one workspace, until it is
// released or runs out.
//
// It is the unit the control surface authenticates against and the unit the
// record attributes decisions to. A submission carries a lease; the lease
// carries who, which role, over what, and under what authority — so a decision
// can never be attributed to "the MCP client" after the fact.
type Lease struct {
	// ID is the role session identity. It is the credential a submission
	// presents, so it is unguessable rather than sequential.
	ID        string     `json:"id"`
	Principal ID         `json:"principal"`
	Workspace string     `json:"workspace"`
	Role      roles.Role `json:"role"`
	// Task is the task this lease is bound to, once it is bound. Empty means
	// the lease is registered and not yet attached to any task; it does not
	// mean the lease is good for every task.
	Task string `json:"task,omitempty"`
	// Authority is the level this role carries. It is stamped from the role by
	// authorityFor and is never taken from a request: a principal that could
	// state its own authority level would be granting itself the thing this
	// package exists to withhold.
	Authority authority.Level `json:"authority"`
	// Capabilities is what was actually granted, which is what a caller should
	// consult rather than re-deriving it from the role.
	Capabilities []Capability `json:"capabilities"`

	State LeaseState `json:"state"`
	// GrantedAt, RenewedAt and ExpiresAt are the lease clock. EndedAt is when
	// it stopped holding, and State says which of the two ways it stopped.
	GrantedAt time.Time `json:"granted_at"`
	RenewedAt time.Time `json:"renewed_at,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
	EndedAt   time.Time `json:"ended_at,omitempty"`

	// Client and Protocol are what the far side said it was at registration,
	// carried for the record and consulted by nothing.
	Client   string `json:"client,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

// Grants reports whether this lease actually grants a capability, by
// membership over what was granted.
func (l Lease) Grants(c Capability) bool {
	for _, have := range l.Capabilities {
		if have == c {
			return true
		}
	}
	return false
}

// Describe is one line for the transcript and the run record.
func (l Lease) Describe() string {
	parts := []string{string(l.Role), "held by " + string(l.Principal)}
	if l.Task != "" {
		parts = append(parts, "on "+l.Task)
	}
	parts = append(parts, string(l.State))
	return strings.Join(parts, " · ")
}

func (l Lease) clone() Lease {
	out := l
	out.Capabilities = append([]Capability(nil), l.Capabilities...)
	return out
}

// The refusals a caller may want to tell apart. They are sentinels because the
// control surface has to map them onto different answers: a released lease is
// something the client did, an expired one is something that happened to it,
// and a capability that was never granted is a client asking for authority it
// did not receive.
var (
	ErrNoSuchLease          = errors.New("no such role session")
	ErrLeaseReleased        = errors.New("this role session was released")
	ErrLeaseExpired         = errors.New("this role session expired")
	ErrCapabilityNotGranted = errors.New("this role session does not grant that operation")
	ErrBoundToAnotherTask   = errors.New("this role session is bound to a different task")
	ErrWorkspaceMismatch    = errors.New("this role session is held over a different workspace")
)

// Registry is the leases a running Sensei Code has issued.
//
// It is in-memory and deliberately so. A lease is a live relationship with a
// connected party, not project knowledge: if this process restarts, no remote
// principal is connected any more, and reviving a lease from disk would revive
// authority for a client that is no longer there. Continuity across a
// disconnect is the TASK's job — the task is durable, the lease is not.
type Registry struct {
	mu     sync.Mutex
	now    func() time.Time
	ttl    time.Duration
	leases map[string]*Lease
}

// DefaultTTL is how long a lease holds without being renewed.
//
// Long enough that an agent thinking about a hard architectural question does
// not lose the role mid-thought, short enough that a client which vanished does
// not hold the architect role for the rest of the day.
const DefaultTTL = 15 * time.Minute

// NewRegistry builds a registry. A zero or negative ttl uses DefaultTTL, and a
// nil clock uses time.Now — the clock is injectable because expiry is the
// behaviour most worth testing and the least worth waiting for.
func NewRegistry(ttl time.Duration, now func() time.Time) *Registry {
	if ttl <= 0 {
		ttl = DefaultTTL
	}
	if now == nil {
		now = time.Now
	}
	return &Registry{now: now, ttl: ttl, leases: make(map[string]*Lease)}
}

// Register decides which of the requested roles this principal may hold.
//
// A request is not authority. Every role is granted or refused with a reason,
// and the refusals are returned rather than dropped.
func (r *Registry) Register(req Request) (Registration, error) {
	if !req.Principal.Known() {
		return Registration{}, errors.New("a role cannot be granted to an unidentified principal")
	}
	workspace := strings.TrimSpace(req.Workspace)
	if workspace == "" {
		return Registration{}, errors.New("a role is held over a workspace, and none was stated")
	}
	if len(req.Roles) == 0 {
		return Registration{}, errors.New("registration requested no roles")
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	r.expireLocked(now)

	reg := Registration{Principal: req.Principal, Workspace: workspace}
	seen := make(map[roles.Role]bool, len(req.Roles))
	for _, role := range req.Roles {
		if seen[role] {
			continue
		}
		seen[role] = true

		if !role.Valid() {
			reg.Refused = append(reg.Refused, Refusal{Role: role,
				Reason: fmt.Sprintf("%q is not a role this project has", role)})
			continue
		}
		if !Leasable(role) {
			reg.Refused = append(reg.Refused, Refusal{Role: role,
				Reason: "the " + role.Label() + " role is never granted to a remote principal: " + whyNotLeasable(role)})
			continue
		}
		level, ok := authorityFor(role)
		if !ok {
			reg.Refused = append(reg.Refused, Refusal{Role: role,
				Reason: "no authority level is defined for the " + role.Label() + " role"})
			continue
		}

		// A principal that is already holding this role over this workspace is
		// reconnecting, not asking for a second lease. Renewing the one it has
		// keeps the task binding and the identity a submission was made under,
		// and it is why a dropped connection does not lose the role.
		if held, found := r.activeLocked(req.Principal, workspace, role); found {
			held.RenewedAt = now
			held.ExpiresAt = now.Add(r.ttl)
			reg.Granted = append(reg.Granted, held.clone())
			continue
		}
		// Somebody else is holding it. Refused rather than shared: two
		// architects on one workspace would each be answering the same
		// question, and the run would take whichever answered first.
		if other, found := r.activeHolderLocked(workspace, role); found {
			reg.Refused = append(reg.Refused, Refusal{Role: role, Reason: fmt.Sprintf(
				"%s holds the %s role over %s until %s",
				other.Principal, role.Label(), workspace, other.ExpiresAt.Format(time.RFC3339))})
			continue
		}

		id, err := newLeaseID()
		if err != nil {
			return Registration{}, fmt.Errorf("could not mint a role session: %w", err)
		}
		lease := &Lease{
			ID: id, Principal: req.Principal, Workspace: workspace, Role: role,
			Authority: level, Capabilities: CapabilitiesFor(role),
			State: Active, GrantedAt: now, ExpiresAt: now.Add(r.ttl),
			Client: strings.TrimSpace(req.Client), Protocol: strings.TrimSpace(req.Protocol),
		}
		r.leases[id] = lease
		reg.Granted = append(reg.Granted, lease.clone())
	}
	return reg, nil
}

func whyNotLeasable(r roles.Role) string {
	switch r {
	case roles.Implementer:
		return "it mutates a candidate, and candidate mutation stays owned by Sensei Code"
	case roles.CounterexampleHunter, roles.ProofRunner:
		return "nothing in this repository drives it, so granting it would make an unfillable role look available"
	}
	return "it is not one of the roles a remote principal may hold"
}

// Lookup returns a lease as of now, with expiry already applied.
func (r *Registry) Lookup(id string) (Lease, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(r.now().UTC())
	lease, ok := r.leases[strings.TrimSpace(id)]
	if !ok {
		return Lease{}, false
	}
	return lease.clone(), true
}

// Authorize is the one gate every consequential remote call passes through.
//
// It answers a single question — may THIS role session do THIS thing on THIS
// task right now — and it answers it by refusing, never by inferring. A caller
// that holds a lease has not thereby been authorized for anything; it has been
// authorized for what the returned lease says it has, on the task the returned
// lease is bound to.
func (r *Registry) Authorize(id string, c Capability, workspace, taskID string) (Lease, error) {
	if !c.Valid() {
		return Lease{}, fmt.Errorf("%w: %q is not an operation this surface has", ErrCapabilityNotGranted, c)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(r.now().UTC())

	lease, ok := r.leases[strings.TrimSpace(id)]
	if !ok {
		return Lease{}, ErrNoSuchLease
	}
	if !lease.State.Holds() {
		switch lease.State {
		case Released:
			return Lease{}, fmt.Errorf("%w at %s", ErrLeaseReleased, lease.EndedAt.Format(time.RFC3339))
		case Expired:
			return Lease{}, fmt.Errorf("%w at %s", ErrLeaseExpired, lease.EndedAt.Format(time.RFC3339))
		}
		// A state this predicate has not been taught about does not hold the
		// role. Reaching here is a bug, and the bug fails closed.
		return Lease{}, fmt.Errorf("%w: state %q", ErrNoSuchLease, lease.State)
	}
	if ws := strings.TrimSpace(workspace); ws != "" && !strings.EqualFold(ws, lease.Workspace) {
		return Lease{}, fmt.Errorf("%w: it is held over %s, and this is %s", ErrWorkspaceMismatch, lease.Workspace, ws)
	}
	if !lease.Grants(c) {
		return Lease{}, fmt.Errorf("%w: the %s role does not grant %s", ErrCapabilityNotGranted, lease.Role.Label(), c)
	}
	if task := strings.TrimSpace(taskID); task != "" && lease.Task != "" && lease.Task != task {
		return Lease{}, fmt.Errorf("%w: it is bound to %s, and this is %s", ErrBoundToAnotherTask, lease.Task, task)
	}
	return lease.clone(), nil
}

// Bind attaches a lease to a task. A lease already bound elsewhere is refused
// rather than moved: a role session that could follow the client from task to
// task would let a decision made under one task's evidence govern another's.
func (r *Registry) Bind(id, taskID string) (Lease, error) {
	task := strings.TrimSpace(taskID)
	if task == "" {
		return Lease{}, errors.New("cannot bind a role session to an unnamed task")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(r.now().UTC())

	lease, ok := r.leases[strings.TrimSpace(id)]
	if !ok {
		return Lease{}, ErrNoSuchLease
	}
	if !lease.State.Holds() {
		return Lease{}, fmt.Errorf("%w: %s", ErrNoSuchLease, lease.State)
	}
	if lease.Task != "" && lease.Task != task {
		return Lease{}, fmt.Errorf("%w: it is bound to %s", ErrBoundToAnotherTask, lease.Task)
	}
	lease.Task = task
	return lease.clone(), nil
}

// Renew extends a lease that is still held. It cannot revive one that is not:
// an expired role was reclaimed, and reclaiming it meant something might have
// been given to somebody else in the meantime.
func (r *Registry) Renew(id string) (Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	r.expireLocked(now)

	lease, ok := r.leases[strings.TrimSpace(id)]
	if !ok {
		return Lease{}, ErrNoSuchLease
	}
	if !lease.State.Holds() {
		if lease.State == Expired {
			return Lease{}, ErrLeaseExpired
		}
		return Lease{}, ErrLeaseReleased
	}
	lease.RenewedAt = now
	lease.ExpiresAt = now.Add(r.ttl)
	return lease.clone(), nil
}

// Release gives a role back. It is recorded as a decision, distinct from
// running out.
func (r *Registry) Release(id string) (Lease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	r.expireLocked(now)

	lease, ok := r.leases[strings.TrimSpace(id)]
	if !ok {
		return Lease{}, ErrNoSuchLease
	}
	if !lease.State.Holds() {
		return lease.clone(), nil
	}
	lease.State = Released
	lease.EndedAt = now
	return lease.clone(), nil
}

// Held lists the leases holding right now, for the record and for the surface
// that has to say who currently holds what.
func (r *Registry) Held() []Lease {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(r.now().UTC())
	var out []Lease
	for _, l := range r.leases {
		if l.State.Holds() {
			out = append(out, l.clone())
		}
	}
	return out
}

// HolderOf reports which principal holds a role on a task, which is what
// qualify.go needs to decide whether a review is independent of the
// architecture it is judging.
//
// It matches on the task binding rather than on the workspace alone: two tasks
// in one repository may legitimately have different architects, and answering
// from the workspace would attribute one task's architecture to another's.
func (r *Registry) HolderOf(role roles.Role, taskID string) (ID, bool) {
	task := strings.TrimSpace(taskID)
	if task == "" {
		return "", false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.expireLocked(r.now().UTC())
	for _, l := range r.leases {
		if l.Role == role && l.Task == task && l.State.Holds() {
			return l.Principal, true
		}
	}
	return "", false
}

// expireLocked reclaims leases whose time is up, stamping WHY they stopped
// holding so the record can tell a client that finished from one that went
// away. Callers hold r.mu.
func (r *Registry) expireLocked(now time.Time) {
	for _, l := range r.leases {
		if l.State.Holds() && !now.Before(l.ExpiresAt) {
			l.State = Expired
			l.EndedAt = l.ExpiresAt
		}
	}
}

func (r *Registry) activeLocked(p ID, workspace string, role roles.Role) (*Lease, bool) {
	for _, l := range r.leases {
		if l.Principal == p && l.Role == role && l.State.Holds() &&
			strings.EqualFold(l.Workspace, workspace) {
			return l, true
		}
	}
	return nil, false
}

func (r *Registry) activeHolderLocked(workspace string, role roles.Role) (*Lease, bool) {
	for _, l := range r.leases {
		if l.Role == role && l.State.Holds() && strings.EqualFold(l.Workspace, workspace) {
			return l, true
		}
	}
	return nil, false
}

// newLeaseID mints an unguessable role session identity. The lease id is the
// credential a submission presents, so a sequential one would let any client
// address another's role session.
func newLeaseID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "rs-" + hex.EncodeToString(b[:]), nil
}

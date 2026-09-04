package control

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/principal"
	"github.com/globulario/sensei-code/internal/roles"
)

// A turn is one question the engine asked, waiting for one answer.
//
// The identity is minted here and the client is told only the id. Everything a
// submission is about — which task, which base, which exact candidate — is what
// the SERVER recorded when it opened the turn, never what the client says when
// it answers. That asymmetry is the whole design: the producer says what it
// thinks, and the orchestrator says what object it was asked to think about.
//
// Identifying a turn by task and role would not do. A worker revises a
// candidate between cycles, so "the reviewer turn for task T" names different
// objects at different moments, and a late answer to the first would be
// delivered to the second — a review of C governing C2 because both were "the
// review of T". The turn id names one question asked once.

// Turn is one pending question.
type Turn struct {
	ID        string     `json:"turn_id"`
	TaskID    string     `json:"task"`
	Role      roles.Role `json:"role"`
	Workspace string     `json:"workspace"`
	// RoleSession and Principal are who was asked. A submission from anyone
	// else is not a late answer, it is a different party's answer.
	RoleSession string       `json:"role_session"`
	Principal   principal.ID `json:"principal"`
	// Binding is the exact artifact this turn is about.
	Binding roles.Binding `json:"binding"`
	// Request is what the engine actually asked, so the remote role can answer
	// the question rather than guess at it.
	Request   string    `json:"request"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
}

// The refusals a submission can meet, separated because they mean different
// things to a client: an id nobody knows, an answer that arrived after the
// question stopped mattering, a second answer to a question already answered,
// and an answer from the wrong party.
var (
	ErrNoSuchTurn   = errors.New("no such pending turn")
	ErrTurnConsumed = errors.New("that turn has already been answered")
	ErrTurnExpired  = errors.New("that turn is no longer open")
	ErrTurnNotYours = errors.New("that turn was issued to another role session")
	// ErrTurnAbandoned wakes a waiting runner when the party that was asked
	// stopped holding the role.
	ErrTurnAbandoned = errors.New("the role session that was asked no longer holds the role")
)

// pending is one open turn and the channel its answer arrives on.
type pending struct {
	turn Turn
	// answer is buffered so a submission never blocks on a runner that has
	// already given up. Whoever consumes the turn closes nothing; the
	// rendezvous removes it.
	answer chan json.RawMessage
	// failed carries a typed refusal that must wake the runner: the lease went
	// away, the task stopped, the turn expired.
	failed chan error
	done   bool
}

// rendezvous is the pending-turn registry.
//
// It is authoritative for what the engine is waiting on, and it is NOT workflow
// state: a turn exists only while a call is blocked on it, and losing the whole
// registry loses no task. The canonical record is still the canonical record.
type rendezvous struct {
	mu   sync.Mutex
	now  func() time.Time
	open map[string]*pending
	// answered remembers, briefly, the turns that were answered.
	//
	// Without it a second submission to a consumed turn is refused with "no
	// such turn", which conflates two different facts: a question that was
	// already answered, and one that never existed. A client hitting the first
	// has a race or a retry to fix; a client hitting the second has the wrong
	// id. It is the same distinction release and expiry keep, for the same
	// reason.
	answered map[string]time.Time
}

// answeredMemory is how long a consumed turn is remembered so its second answer
// can be refused with the honest reason. Long enough to cover a retry, short
// enough that the map is not a leak.
const answeredMemory = 10 * time.Minute

func newRendezvous(now func() time.Time) *rendezvous {
	if now == nil {
		now = time.Now
	}
	return &rendezvous{now: now, open: make(map[string]*pending), answered: make(map[string]time.Time)}
}

// Open registers a turn and returns it with its minted identity.
func (r *rendezvous) Open(t Turn, ttl time.Duration) (*pending, error) {
	id, err := newTurnID()
	if err != nil {
		return nil, err
	}
	now := r.now().UTC()
	t.ID = id
	t.CreatedAt = now
	t.ExpiresAt = now.Add(ttl)

	r.mu.Lock()
	defer r.mu.Unlock()
	p := &pending{turn: t, answer: make(chan json.RawMessage, 1), failed: make(chan error, 1)}
	r.open[id] = p
	return p, nil
}

// Close removes a turn, whether it was answered, abandoned or given up on. A
// turn that was ANSWERED is remembered briefly, so a second answer can be told
// what actually happened.
func (r *rendezvous) Close(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	if p, ok := r.open[id]; ok && p.done {
		r.answered[id] = now
	}
	delete(r.open, id)
	for old, at := range r.answered {
		if now.Sub(at) > answeredMemory {
			delete(r.answered, old)
		}
	}
}

// Submit delivers an answer to exactly one pending turn.
//
// It refuses rather than approximates at every step: an unknown id, an id whose
// turn has expired, a second answer to a consumed turn, and an answer from a
// party the turn was not issued to. Nothing here reads the payload — what the
// answer SAYS is the caller's business, and what it is ABOUT was decided when
// the turn was opened.
func (r *rendezvous) Submit(id string, by principal.ID, session string, role roles.Role, payload json.RawMessage) (Turn, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	key := strings.TrimSpace(id)
	p, ok := r.open[key]
	if !ok {
		if _, answered := r.answered[key]; answered {
			return Turn{}, ErrTurnConsumed
		}
		return Turn{}, ErrNoSuchTurn
	}
	if p.done {
		return Turn{}, ErrTurnConsumed
	}
	if !r.now().UTC().Before(p.turn.ExpiresAt) {
		return Turn{}, fmt.Errorf("%w: it expired at %s", ErrTurnExpired, p.turn.ExpiresAt.Format(time.RFC3339))
	}
	if p.turn.Principal != by || !strings.EqualFold(p.turn.RoleSession, strings.TrimSpace(session)) {
		return Turn{}, ErrTurnNotYours
	}
	if p.turn.Role != role {
		return Turn{}, fmt.Errorf("%w: it is a %s turn", ErrTurnNotYours, p.turn.Role.Label())
	}
	// Consumed before the answer is handed over, under the same lock that
	// found it. A turn answered twice is a second opinion arriving as though it
	// were the first, and the window for that is exactly here.
	p.done = true
	p.answer <- payload
	return p.turn, nil
}

// Abandon wakes a waiting runner with a typed refusal, used when the role
// session that was asked stops holding the role.
func (r *rendezvous) Abandon(id string, cause error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	p, ok := r.open[id]
	if !ok || p.done {
		return
	}
	p.done = true
	select {
	case p.failed <- cause:
	default:
	}
}

// AbandonSession wakes every turn issued to a role session, so a released or
// expired lease never leaves the engine blocked on a party that is not there.
func (r *rendezvous) AbandonSession(session string, cause error) int {
	r.mu.Lock()
	ids := make([]string, 0, len(r.open))
	for id, p := range r.open {
		if !p.done && strings.EqualFold(p.turn.RoleSession, session) {
			ids = append(ids, id)
		}
	}
	r.mu.Unlock()
	for _, id := range ids {
		r.Abandon(id, cause)
	}
	return len(ids)
}

// Waiting lists the open turns, expiring any whose time has passed. It is what
// get_work reports, and it is a read: nothing here advances anything.
func (r *rendezvous) Waiting(taskID string) []Turn {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	var out []Turn
	for _, p := range r.open {
		if p.done || !now.Before(p.turn.ExpiresAt) {
			continue
		}
		if taskID != "" && p.turn.TaskID != taskID {
			continue
		}
		out = append(out, p.turn)
	}
	return out
}

// newTurnID mints an unguessable pending-turn identity. It is presented by a
// submission, so a sequential one would let a client answer a question that was
// asked of somebody else.
func newTurnID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "turn-" + hex.EncodeToString(b[:]), nil
}

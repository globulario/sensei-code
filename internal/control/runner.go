package control

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/principal"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// The remote agent occupies an existing provider slot. It does not become a
// second workflow.
//
// Resolve is the whole of the routing decision, and it has exactly three
// outcomes. Not delegated: the configured command line answers, as it always
// did. Delegated and holding: a runner that blocks until the remote party
// answers the exact question. Delegated and NOT holding: a refusal.
//
// That third case is the one worth stating twice. A delegated architect that
// vanished must not become the local architect quietly — the plan would be
// attributed to a party that did not write it, and nothing in the record would
// show the substitution. So delegation is remembered per task, and once a task's
// role has been delegated, the only alternatives are the same party or a stop.

// DefaultTurnTTL bounds how long the engine will wait for a remote answer.
//
// A blocked workflow is worse than a failed one: a failure enters the recovery
// ladder, and a hang enters nothing. The bound is generous enough for a model
// to think about a real architectural question and short enough that a
// disappeared client does not hold a run for the rest of the day.
const DefaultTurnTTL = 10 * time.Minute

// delegation remembers which principal a task's role was handed to.
type delegation struct {
	principal   principal.ID
	roleSession string
}

func delegationKey(taskID string, role roles.Role) string { return taskID + "\x00" + string(role) }

// Delegated reports what this server has handed out, for the record.
func (s *Server) Delegated(taskID string, role roles.Role) (principal.ID, bool) {
	s.delegatedMu.Lock()
	defer s.delegatedMu.Unlock()
	d, ok := s.delegations[delegationKey(taskID, role)]
	return d.principal, ok
}

func (s *Server) rememberDelegation(taskID string, role roles.Role, lease principal.Lease) {
	s.delegatedMu.Lock()
	defer s.delegatedMu.Unlock()
	if s.delegations == nil {
		s.delegations = make(map[string]delegation)
	}
	s.delegations[delegationKey(taskID, role)] = delegation{principal: lease.Principal, roleSession: lease.ID}
}

// Resolve routes one role turn. It implements workflow.RunnerResolver.
func (s *Server) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	// The implementer is never remote, and this is checked by role rather than
	// left to fall through. A remote party must not mutate a candidate, and
	// "Engine.Runners is non-nil" must never be the reason a worker turn takes
	// a different path than it did before.
	if spec.Role != roles.Architect && spec.Role != roles.Reviewer {
		return workflow.CLIResolved(spec, s.engine.SessionID), nil
	}

	held, holding := s.holdingLease(spec.Role)
	previous, delegated := s.Delegated(spec.TaskID, spec.Role)

	switch {
	case delegated && (!holding || held.Principal != previous):
		// The party this task's role was handed to is not there any more.
		// Refused, loudly, rather than answered by somebody else.
		return workflow.Resolved{}, fmt.Errorf(
			"the %s role for %s was delegated to %s, which no longer holds it; this run will not substitute another decision-maker",
			spec.Role.Label(), spec.TaskID, previous)
	case !holding:
		// Nothing was delegated and nothing is holding: the ordinary local path.
		return workflow.CLIResolved(spec, s.engine.SessionID), nil
	}

	bound, err := s.leases.Bind(held.ID, spec.TaskID)
	if err != nil {
		return workflow.Resolved{}, fmt.Errorf(
			"the %s role could not be bound to %s: %w", spec.Role.Label(), spec.TaskID, err)
	}
	s.rememberDelegation(spec.TaskID, spec.Role, bound)
	return workflow.Resolved{
		Runner: &remoteRunner{server: s, lease: bound},
		Name:   string(bound.Principal),
		Label:  "remote " + spec.Role.Label() + " " + string(bound.Principal),
	}, nil
}

// holdingLease finds an active remote lease for a role over this workspace.
func (s *Server) holdingLease(role roles.Role) (principal.Lease, bool) {
	for _, l := range s.leases.Held() {
		if l.Role == role && strings.EqualFold(l.Workspace, s.workspace) {
			return l, true
		}
	}
	return principal.Lease{}, false
}

// remoteRunner answers a role turn by asking a connected party and waiting.
//
// It executes nothing. It has no process, no worktree and no capability
// envelope; it converts a question into a pending turn and a pending turn back
// into the same text a command-line provider would have printed. Everything
// downstream — parsing, claim checking, contradiction, certifiability, the
// authority router — is the path it always was.
type remoteRunner struct {
	server *Server
	lease  principal.Lease
}

func (r *remoteRunner) Run(ctx context.Context, req agent.Request, emit func(event.Event)) (agent.Result, error) {
	s := r.server
	p, err := s.turns.Open(Turn{
		TaskID: req.TaskID, Role: req.Role, Workspace: s.workspace,
		RoleSession: r.lease.ID, Principal: r.lease.Principal,
		Binding: req.Binding, Request: req.Prompt,
	}, s.turnTTL)
	if err != nil {
		return agent.Result{}, err
	}
	defer s.turns.Close(p.turn.ID)

	emit(event.New(s.engine.SessionID, req.TaskID, sourceFor(req.Role), event.AgentStarted,
		"waiting for the remote "+req.Role.Label()+" to answer turn "+p.turn.ID,
		map[string]any{
			"turn_id": p.turn.ID, "role": string(req.Role), "principal": string(r.lease.Principal),
			"role_session": r.lease.ID, "expires_at": p.turn.ExpiresAt,
		}))

	// Every branch below ends the wait. There is no path here that waits
	// forever, and that is the point of the type: the workflow can survive a
	// remote party that stops answering, and cannot survive a block with no
	// deadline.
	//
	// The wait is measured from the configured TTL rather than from the turn's
	// ExpiresAt. Those agree by construction -- both are this moment plus the
	// same duration -- but they are read from different clocks: the registry
	// stamps ExpiresAt from the injectable one so expiry is testable, and this
	// timer is monotonic. Deriving the wait from a wall-clock field would make
	// how long the engine blocks depend on how far the two clocks had drifted.
	deadline := time.NewTimer(s.turnTTL)
	defer deadline.Stop()

	// A lease that ends while its turn is pending must wake the runner rather
	// than let the engine sit on a party that is gone. Polled rather than
	// pushed: expiry is a property of a clock, and nothing fires an event when
	// a moment passes.
	watch := time.NewTicker(leaseWatchInterval)
	defer watch.Stop()

	for {
		select {
		case payload := <-p.answer:
			emit(event.New(s.engine.SessionID, req.TaskID, sourceFor(req.Role), event.AgentFinished,
				"the remote "+req.Role.Label()+" answered turn "+p.turn.ID, map[string]any{"turn_id": p.turn.ID}))
			// The mode is Unverified and is stamped HERE, by the orchestrator,
			// from the fact that this turn was answered over a transport. It is
			// not read from the payload, and there is no field a remote party
			// could set to say otherwise.
			return agent.Result{Text: string(payload), SessionID: r.lease.ID, Session: roles.Unverified}, nil

		case err := <-p.failed:
			return agent.Result{}, err

		case <-deadline.C:
			return agent.Result{}, fmt.Errorf("the remote %s did not answer turn %s within %s",
				req.Role.Label(), p.turn.ID, s.turnTTL)

		case <-ctx.Done():
			// The task was stopped or timed out. The turn dies with it.
			return agent.Result{}, ctx.Err()

		case <-watch.C:
			if _, err := s.leases.Authorize(r.lease.ID, submitCapability(req.Role), s.workspace, req.TaskID); err != nil {
				return agent.Result{}, fmt.Errorf("%w: %v", ErrTurnAbandoned, err)
			}
		}
	}
}

// leaseWatchInterval is how often a pending turn re-checks that the party it is
// waiting on still holds the role.
const leaseWatchInterval = 2 * time.Second

// submitCapability is the operation a role must hold to answer its own turn.
func submitCapability(role roles.Role) principal.Capability {
	if role == roles.Architect {
		return principal.SubmitArchitecture
	}
	return principal.SubmitReview
}

func sourceFor(role roles.Role) event.Source {
	if role == roles.Architect {
		return event.SourceArchitect
	}
	return event.SourceReviewer
}

// submitTurn is the server side of a submission: authorize the party, deliver
// the payload to the exact turn, and refuse everything else.
func (s *Server) submitTurn(role roles.Role, session, turnID string, payload json.RawMessage) (Turn, error) {
	if strings.TrimSpace(turnID) == "" {
		return Turn{}, errors.New("a submission must name the turn it answers")
	}
	// The lease is checked first and against the capability this role needs, so
	// a reviewer presenting an architect's turn id is refused for holding the
	// wrong authority rather than for naming the wrong turn.
	lease, err := s.leases.Authorize(session, submitCapability(role), s.workspace, "")
	if err != nil {
		return Turn{}, err
	}
	if lease.Principal != s.cred.Principal() {
		return Turn{}, errors.New("that role session is held by another principal")
	}
	return s.turns.Submit(turnID, lease.Principal, lease.ID, role, payload)
}

// turnsMu guards nothing here; the rendezvous has its own lock. Declared so the
// Server's delegation map has one.
var _ = sync.Mutex{}

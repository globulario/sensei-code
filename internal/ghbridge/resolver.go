package ghbridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Resolver carries the configured ChatGPT provider over GitHub for the two
// roles whose remote mailbox protocols are explicit: architect and reviewer.
// Implementers, proof runners, and every other provider still go to the
// resolver that would otherwise have served them.
//
// The bridge is a TRANSPORT for the provider the workflow selected, not a
// provider identity of its own. Three facts stay separate, and only the middle
// one belongs to this package:
//
//	provider     = the identity the workflow assigned (e.g. chatgpt)
//	transport    = github
//	session_mode = unverified
//
// The workflow picks a concrete provider before the resolver is consulted. A
// resolver that renamed the turn to its own transport would overwrite that
// choice, and the provider recorded on the resulting artifact would disagree
// with the provider the engine assigned — RoleAssigned.Provider against
// ReviewVerdict.Provider for a review, and the recorded architect for a plan.
// So both branches below return spec.Agent.Name unchanged, and GitHub appears
// only in ResolverLabel and in event metadata. Transport is never part of the
// semantic identity.
//
// Nor may transport raise session mode. A turn answered over GitHub is
// roles.Unverified however well authenticated the sender is; the runners stamp
// that themselves, and no GitHub property may lift it.
//
// It COMPOSES rather than replaces. Delegating other roles straight to the CLI
// would bypass control.Server and silently disable remote architect semantics —
// a delegated architect would quietly become the local one, which is the
// failure workflow.resolveRunner refuses to allow.
//
// A governed architect turn is captured only with a complete binding to
// objective + base + graph. If it has a task id but no binding it is refused,
// never silently routed to the local CLI. A bare resolver probe with no task id
// and no binding is not a task turn at all and continues to the composed
// fallback, preserving the resolver's existing non-task behavior.
//
//	engine
//	  └── Resolver
//	        ├── architect + Provider + bound subject → ArchitectureRunner
//	        ├── reviewer  + Provider                → review Runner
//	        └── everything else                     → Fallback
type Resolver struct {
	// Provider is the assignment this bridge carries, e.g. "chatgpt". A turn
	// assigned to any other provider is not this bridge's, whatever its role.
	Provider string
	// Reviewer holds the shared mailbox/App configuration and serves reviews.
	// Architect runners are built per turn from this transport plus the exact
	// workflow-supplied architecture binding, so no mutable binding is shared
	// across concurrent tasks.
	Reviewer *Runner
	// Doorbell, when set, is carried into each architect turn so a published
	// request can be pointed at. Nil wherever the remote wake path admits the
	// App and no locator is needed.
	Doorbell Doorbell
	// Fallback serves every other role and every other provider. Required, and
	// never bypassed.
	Fallback workflow.RunnerResolver
}

// ResolverLabel is what a person reads for a turn carried over this bridge. The
// NAME stays the assigned provider's, so the label is the only place the
// transport shows up in the resolved identity.
const ResolverLabel = "ChatGPT via GitHub"

// ErrBridgeUnavailable reports that the assigned provider's GitHub transport
// cannot serve this turn.
//
// Returned rather than recovered from, and that is the load-bearing half.
// workflow.resolveRunner PROPAGATES a resolver's refusal and has no path that
// builds the CLI anyway, so a refusal here cannot decay into a quiet local
// architect or a quiet local review. The engine may still choose an explicitly
// recorded alternative through its own assignment ladder — but that is a
// decision the workflow makes and records, which is a different thing from
// silent substitution by this bridge.
//
// The same reasoning governs ErrUnboundArchitecture below: a real task whose
// binding is missing is refused for the same reason, because answering it
// locally would attribute a plan to a party that did not write it and leave
// nothing in the record to show the substitution.
var ErrBridgeUnavailable = errors.New("the assigned provider's github transport is not available for this turn")

// Resolve implements workflow.RunnerResolver.
func (r Resolver) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	if r.carries(spec.Agent.Name) {
		switch spec.Role {
		case roles.Reviewer:
			if r.Reviewer == nil {
				return workflow.Resolved{}, fmt.Errorf("%w: provider %s", ErrBridgeUnavailable, spec.Agent.Name)
			}
			return workflow.Resolved{Runner: r.Reviewer, Name: spec.Agent.Name, Label: ResolverLabel}, nil
		case roles.Architect:
			// No task means there is no governed architecture subject to carry.
			// This is distinct from a real task whose binding was lost: the latter
			// is a refusal below, not a fallback.
			if strings.TrimSpace(spec.TaskID) == "" && !spec.Architecture.Valid() {
				break
			}
			if r.Reviewer == nil {
				return workflow.Resolved{}, fmt.Errorf("%w: provider %s", ErrBridgeUnavailable, spec.Agent.Name)
			}
			if !spec.Architecture.Valid() {
				return workflow.Resolved{}, fmt.Errorf("%w: %+v", ErrUnboundArchitecture, spec.Architecture)
			}
			architect := &ArchitectureRunner{
				Issue:        r.Reviewer.Issue,
				Binding:      spec.Architecture,
				NewRequestID: r.Reviewer.NewRequestID,
				Poll:         r.Reviewer.Poll,
				SessionID:    r.Reviewer.SessionID,
				Wait:         r.Reviewer.Wait,
				Doorbell:     r.Doorbell,
			}
			return workflow.Resolved{Runner: architect, Name: spec.Agent.Name, Label: ResolverLabel}, nil
		}
	}
	if r.Fallback == nil {
		return workflow.Resolved{}, fmt.Errorf("no resolver for the %s role", spec.Role.Label())
	}
	return r.Fallback.Resolve(spec)
}

// carries reports whether an assigned provider is the one this bridge serves.
//
// An unconfigured Provider carries NOTHING. The empty string is not a wildcard:
// a Resolver nobody configured forwards every turn to the fallback rather than
// capturing all of them, so a half-built bridge cannot quietly become the
// answer for every provider in the ladder. That is why the p != "" test is
// separate from the comparison rather than folded into it.
func (r Resolver) carries(assigned string) bool {
	p := strings.TrimSpace(r.Provider)
	return p != "" && strings.EqualFold(p, strings.TrimSpace(assigned))
}

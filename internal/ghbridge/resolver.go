package ghbridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Resolver serves the reviewer the workflow already ASSIGNED, when that
// reviewer is the provider this bridge carries. Everything else goes to the
// resolver that would otherwise have served it.
//
// The bridge is a TRANSPORT for an assigned provider, not a provider of its
// own. workflow.resolveReview picks a concrete reviewer before the resolver is
// consulted; a resolver that turned every reviewer assignment into its own name
// would overwrite that choice, and the provider recorded on the verdict would
// disagree with the provider the engine assigned. So:
//
//	provider     = the assigned reviewer (e.g. chatgpt)
//	transport    = github
//	session_mode = unverified
//
// are three separate facts, and only the middle one belongs to this package.
// Transport is reported in event metadata, never in the semantic identity.
//
// It COMPOSES rather than replaces. Delegating non-review roles straight to the
// CLI would bypass control.Server and silently disable remote architect
// semantics — a delegated architect would quietly become the local one, which
// is the failure workflow.resolveRunner refuses to allow.
//
//	engine
//	  └── Resolver
//	        ├── reviewer AND assigned provider == Provider → ghbridge.Runner
//	        └── everything else                            → Fallback
type Resolver struct {
	// Provider is the reviewer assignment this bridge carries, e.g. "chatgpt".
	// A reviewer turn assigned to any other provider is not this bridge's.
	Provider string
	// Reviewer serves matching reviewer turns.
	Reviewer *Runner
	// Fallback serves every other role and every other reviewer provider.
	// Required, and never bypassed.
	Fallback workflow.RunnerResolver
}

// ResolverLabel is what a person reads for a turn carried over this bridge. The
// NAME stays the assigned provider's, so the label is the only place the
// transport shows up in the resolved identity.
const ResolverLabel = "ChatGPT via GitHub"

// ErrBridgeUnavailable reports that the assigned reviewer's GitHub transport
// cannot serve this turn.
//
// Returned rather than recovered from. workflow.resolveRunner propagates a
// resolver's refusal and has no path that builds the CLI anyway, so a refusal
// here cannot become a quiet local review. The engine may then choose an
// explicitly recorded fallback reviewer through its own assignment ladder,
// which is a different thing from silent substitution.
var ErrBridgeUnavailable = errors.New("the assigned reviewer's github transport is not available for this turn")

// Resolve implements workflow.RunnerResolver.
func (r Resolver) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	if spec.Role == roles.Reviewer && r.carries(spec.Agent.Name) {
		if r.Reviewer == nil {
			return workflow.Resolved{}, fmt.Errorf("%w: provider %s", ErrBridgeUnavailable, spec.Agent.Name)
		}
		return workflow.Resolved{
			Runner: r.Reviewer,
			// The assignment's identity, preserved. Anything else would make
			// RoleAssigned.Provider and ReviewVerdict.Provider disagree.
			Name:  spec.Agent.Name,
			Label: ResolverLabel,
		}, nil
	}
	if r.Fallback == nil {
		return workflow.Resolved{}, fmt.Errorf("no resolver for the %s role", spec.Role.Label())
	}
	return r.Fallback.Resolve(spec)
}

// carries reports whether an assigned provider is the one this bridge serves.
// An unconfigured Provider carries nothing, so a Resolver nobody configured
// forwards every turn rather than capturing all of them.
func (r Resolver) carries(assigned string) bool {
	p := strings.TrimSpace(r.Provider)
	return p != "" && strings.EqualFold(p, strings.TrimSpace(assigned))
}

package ghbridge

import (
	"errors"
	"fmt"

	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// Resolver routes reviewer turns to the GitHub bridge and everything else to
// the resolver that would otherwise have served them.
//
// It COMPOSES rather than replaces. Delegating non-review roles straight to the
// CLI would bypass control.Server and silently disable remote architect
// semantics — a delegated architect would quietly become the local one, which
// is the failure workflow.resolveRunner refuses to allow for exactly this
// reason.
//
//	engine
//	  └── Resolver
//	        ├── reviewer → ghbridge.Runner
//	        └── other    → Fallback (control.Server)
//	                         ├── remote delegated role when held
//	                         └── ordinary CLI when not delegated
type Resolver struct {
	// Reviewer serves reviewer turns. Required: a Resolver with none refuses
	// rather than quietly handing the role back to the local provider.
	Reviewer *Runner
	// Fallback serves every other role. Required, and never bypassed.
	Fallback workflow.RunnerResolver
}

// ResolverName is the stable identity recorded for turns this bridge served.
const (
	ResolverName  = "chatgpt-github"
	ResolverLabel = "ChatGPT via GitHub"
)

// ErrBridgeUnavailable reports that a reviewer turn was selected for the GitHub
// bridge and the bridge cannot serve it.
//
// It is returned rather than recovered from. workflow.resolveRunner propagates
// a resolver's refusal and deliberately has no path that builds the CLI anyway:
// a run where the remote reviewer quietly became the local one would produce a
// review attributed to a party that did not write it.
var ErrBridgeUnavailable = errors.New("the github review bridge is not available for this turn")

// Resolve implements workflow.RunnerResolver.
func (r Resolver) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	if spec.Role == roles.Reviewer {
		if r.Reviewer == nil {
			return workflow.Resolved{}, ErrBridgeUnavailable
		}
		return workflow.Resolved{
			Runner: r.Reviewer,
			Name:   ResolverName,
			Label:  ResolverLabel,
		}, nil
	}
	if r.Fallback == nil {
		return workflow.Resolved{}, fmt.Errorf("no resolver for the %s role", spec.Role.Label())
	}
	return r.Fallback.Resolve(spec)
}

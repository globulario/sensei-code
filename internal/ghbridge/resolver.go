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
// provider identity of its own. Reviewer turns keep the engine's assigned
// reviewer name. Architect turns keep the configured architect name. GitHub is
// represented in the label and event metadata only.
//
// Architecture is captured only when the workflow supplies a complete binding
// to objective + base + graph. An unbound architect turn is refused, never
// silently routed to the local CLI, because that would change who decided while
// making the transcript look continuous.
//
//	engine
//	  └── Resolver
//	        ├── architect + Provider + bound subject → ArchitectureRunner
//	        ├── reviewer  + Provider                → review Runner
//	        └── everything else                     → Fallback
type Resolver struct {
	// Provider is the semantic provider carried over GitHub, e.g. "chatgpt".
	Provider string
	// Reviewer holds the shared mailbox/App configuration and serves reviews.
	// Architect runners are built per turn from this transport plus the exact
	// workflow-supplied architecture binding, so no mutable binding is shared
	// across concurrent tasks.
	Reviewer *Runner
	// Fallback serves every other role/provider. Required and never bypassed.
	Fallback workflow.RunnerResolver
}

const ResolverLabel = "ChatGPT via GitHub"

var ErrBridgeUnavailable = errors.New("the assigned provider's github transport is not available for this turn")

// Resolve implements workflow.RunnerResolver.
func (r Resolver) Resolve(spec workflow.RunnerSpec) (workflow.Resolved, error) {
	if r.carries(spec.Agent.Name) {
		switch spec.Role {
		case roles.Reviewer:
			if r.Reviewer == nil {
				return workflow.Resolved{}, fmt.Errorf("%w: provider %s", ErrBridgeUnavailable, spec.Agent.Name)
			}
			return workflow.Resolved{
				Runner: r.Reviewer,
				Name:   spec.Agent.Name,
				Label:  ResolverLabel,
			}, nil
		case roles.Architect:
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
			}
			return workflow.Resolved{
				Runner: architect,
				Name:   spec.Agent.Name,
				Label:  ResolverLabel,
			}, nil
		}
	}
	if r.Fallback == nil {
		return workflow.Resolved{}, fmt.Errorf("no resolver for the %s role", spec.Role.Label())
	}
	return r.Fallback.Resolve(spec)
}

func (r Resolver) carries(assigned string) bool {
	p := strings.TrimSpace(r.Provider)
	return p != "" && strings.EqualFold(p, strings.TrimSpace(assigned))
}

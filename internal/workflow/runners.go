package workflow

import (
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/roles"
)

// The engine used to build its adapters where it used them: four agent.CLI
// literals, one per call site, each repeating the same eight fields. That made
// agent.Runner an interface nothing implemented and nothing accepted -- a seam
// in name only. Anything that is not the provider's own command line had
// nowhere to be plugged in, so a second way of answering as the architect would
// have arrived as a second code path rather than as another implementation of
// the one that exists.
//
// So the engine now ASKS for the thing that answers as a role, and does not
// construct one. Every answer today is still agent.CLI and the argv is
// unchanged; what moved is who decides which adapter serves a turn.
//
// The rule that matters is at the bottom of resolveRunner: a resolver that
// cannot produce an adapter is a refusal, never a reason to fall back to the
// configured command line. Silently running the local provider because a
// delegated one was unavailable would change WHO DECIDED without changing
// anything a reader could see.

// RunnerSpec is everything needed to obtain the adapter that serves one turn.
type RunnerSpec struct {
	// Role is the job being filled. It is the load-bearing field: a resolver
	// decides by role, not by which provider happens to be configured.
	Role roles.Role
	// Agent is the configured provider for this role, which the default
	// resolver turns into argv and a delegating one may ignore.
	Agent config.Agent
	// Source is the actor the turn's events are attributed to.
	Source event.Source
	// TaskID is which task this turn belongs to. It is carried because a
	// delegated role is held per task, not per process.
	TaskID string
	// Env are extra environment entries enforcing capability boundaries the
	// agent must not be able to talk its way past.
	Env []string
}

// Resolved is the adapter that will serve a turn, and who it is.
//
// Name and Label are returned rather than re-derived from the spec because the
// party that answers is not always the configured provider. Name is the
// load-bearing half -- output normalization matches on it, and the self-review
// exclusion compares it -- and Label is what a person reads.
type Resolved struct {
	Runner agent.Runner
	Name   string
	Label  string
}

// RunnerResolver hands back the adapter that will serve a turn.
//
// Implemented by nothing today: the engine's default is the provider command
// line, and this interface exists so that a later adapter is an implementation
// rather than a branch. A resolver may refuse, and refusing is a real answer --
// see resolveRunner.
type RunnerResolver interface {
	Resolve(RunnerSpec) (Resolved, error)
}

// CLIResolved is the default adapter: the provider's own command line, built
// exactly as the engine built it inline before.
func CLIResolved(spec RunnerSpec, sessionID string) Resolved {
	name := spec.Agent.Name
	return Resolved{
		Name:  name,
		Label: config.DisplayName(name),
		Runner: agent.CLI{
			Name:      name,
			Label:     config.DisplayName(name),
			Command:   spec.Agent.Command,
			Args:      spec.Agent.Args,
			NoGraph:   !spec.Agent.ConsumesGraph(),
			Source:    spec.Source,
			SessionID: sessionID,
			Env:       spec.Env,
			UnsetEnv:  provider.SessionOnlyEnv,
		},
	}
}

// resolveRunner returns the adapter that serves this turn.
//
// With no resolver configured it is the provider command line, which is every
// path in this repository today. With one configured, its answer is used or its
// refusal is propagated -- and an empty answer is a refusal too. There is
// deliberately no path here that recovers from a resolver by building the CLI
// anyway: a run where a delegated architect quietly became the local one would
// produce a plan attributed to a party that did not write it.
func (e *Engine) resolveRunner(spec RunnerSpec) (Resolved, error) {
	if !spec.Role.Valid() {
		return Resolved{}, fmt.Errorf("cannot resolve an adapter for unknown role %q", spec.Role)
	}
	if e.Runners == nil {
		return CLIResolved(spec, e.SessionID), nil
	}
	resolved, err := e.Runners.Resolve(spec)
	if err != nil {
		return Resolved{}, fmt.Errorf("no adapter took the %s role: %w", spec.Role.Label(), err)
	}
	if resolved.Runner == nil {
		return Resolved{}, fmt.Errorf("the resolver returned no adapter for the %s role", spec.Role.Label())
	}
	if strings.TrimSpace(resolved.Name) == "" {
		// An unnamed adapter cannot be excluded from reviewing its own work,
		// and cannot be attributed in a receipt. Both of those failures are
		// silent, which is why this one is not.
		return Resolved{}, fmt.Errorf("the resolver returned an unnamed adapter for the %s role", spec.Role.Label())
	}
	if strings.TrimSpace(resolved.Label) == "" {
		resolved.Label = resolved.Name
	}
	return resolved, nil
}

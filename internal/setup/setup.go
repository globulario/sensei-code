// Package setup turns every obstacle between a fresh checkout and a working
// session into a check that knows its own repair.
//
// Each of these cost real time to diagnose the first time. A released Sensei
// that lags the tools this needs, an agent whose MCP server is registered but
// whose tools are all blocked, a graph domain that must be registered in a file
// outside the repository, two copies of a binary where PATH order decides which
// one answers, and a graph marker that goes stale and fails every task closed.
// None of them announce themselves; each one presents as something unrelated.
//
// So a check here states the symptom the user would actually see, not the
// internal condition, and carries the exact command that fixes it. A check that
// can repair itself does; a check that cannot says precisely what a person must
// do. Nothing is repaired silently: the plan is shown before it runs, because
// this touches a shared graph and a machine's PATH.
package setup

import (
	"context"
	"fmt"
	"strings"
)

// State is the outcome of one check.
type State string

const (
	// OK means the check passed.
	OK State = "ok"
	// Broken means the check failed and the session will not work until it is
	// repaired.
	Broken State = "broken"
	// Degraded means the session works but something is missing that will bite
	// later.
	Degraded State = "degraded"
	// Unknown means the check could not determine the answer, which is never
	// reported as a pass.
	Unknown State = "unknown"
)

// Check is one thing that must be true before Sensei Code can work.
type Check struct {
	// Name is short and stable, used as the row label.
	Name string
	// State is the result.
	State State
	// Detail says what was observed.
	Detail string
	// Symptom describes what the user sees when this is broken, in the words
	// they would use, so a failure they already hit is recognisable here.
	Symptom string
	// Fix is the command that repairs it, empty when nothing can.
	Fix string
	// Repair performs the fix. Nil when only a person can do it.
	Repair func(ctx context.Context) error
}

// Report is the full readiness picture.
type Report struct {
	Checks []Check
}

// Ready reports whether a session can work at all.
func (r Report) Ready() bool {
	for _, c := range r.Checks {
		if c.State == Broken {
			return false
		}
	}
	return true
}

// Repairable lists the checks that can fix themselves.
func (r Report) Repairable() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.Repair != nil && (c.State == Broken || c.State == Degraded) {
			out = append(out, c)
		}
	}
	return out
}

// Blocking lists failures that no repair here can resolve.
func (r Report) Blocking() []Check {
	var out []Check
	for _, c := range r.Checks {
		if c.State == Broken && c.Repair == nil {
			out = append(out, c)
		}
	}
	return out
}

// Render writes the report for a terminal.
func (r Report) Render() string {
	var b strings.Builder
	b.WriteString("Setup\n")
	for _, c := range r.Checks {
		b.WriteString(fmt.Sprintf("  %-6s %-26s %s\n", marker(c.State), c.Name, c.Detail))
		if c.State == OK {
			continue
		}
		if c.Symptom != "" {
			b.WriteString("         what you would see: " + c.Symptom + "\n")
		}
		if c.Fix != "" {
			// "can run", not "will run": the same report is rendered by a
			// read-only reader and by `setup --apply`, and only the second one
			// runs anything. Wording that promised the repair would happen would
			// be a lie in the reader that never calls Apply.
			verb := "fix"
			if c.Repair != nil {
				verb = "can run"
			}
			b.WriteString("         " + verb + ": " + c.Fix + "\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func marker(s State) string {
	switch s {
	case OK:
		return "ok"
	case Broken:
		return "BROKEN"
	case Degraded:
		return "warn"
	default:
		return "?"
	}
}

// Apply runs every repair the report offers, in order, and returns what it did.
// Repairs run in the order the checks are declared because they depend on each
// other: a domain cannot be registered before Sensei exists, and a graph cannot
// be published before a domain is registered.
func Apply(ctx context.Context, r Report) []string {
	var done []string
	for _, c := range r.Repairable() {
		if err := c.Repair(ctx); err != nil {
			done = append(done, c.Name+": "+err.Error())
			continue
		}
		done = append(done, c.Name+": "+c.Fix)
	}
	return done
}

package agent

import (
	"testing"

	"github.com/globulario/sensei-code/internal/roles"
)

// An adversarial role must not inherit the conversation it is judging, and the
// caller does not get a say. A reviewer that read the architect's case for the
// change before forming a view agrees more often, and the transcript cannot
// tell that agreement apart from a real one.
func TestAnAdversarialRoleAlwaysStartsFresh(t *testing.T) {
	for _, r := range []roles.Role{roles.Reviewer, roles.CounterexampleHunter} {
		if got := (Request{Role: r}).session(); got != roles.Fresh {
			t.Fatalf("%s defaulted to %q", r, got)
		}
		// Explicitly asking for continuity does not grant it.
		if got := (Request{Role: r, Session: roles.Continue}).session(); got != roles.Fresh {
			t.Fatalf("%s was given the conversation it is judging when the caller asked for it", r)
		}
	}
}

// The architect is the conversation. Reconstructing it every turn would discard
// the dialogue the human is having.
func TestTheArchitectContinuesItsConversation(t *testing.T) {
	if got := (Request{Role: roles.Architect}).session(); got != roles.Continue {
		t.Fatalf("architect session = %q, want continue", got)
	}
	if got := (Request{Role: roles.Architect, Session: roles.Fresh}).session(); got != roles.Fresh {
		t.Fatal("an architect turn that deliberately asked to start clean was overridden")
	}
}

// An implementer inherits nothing by default: it works from the plan and the
// candidate, not from a remembered discussion about them.
func TestAnImplementerDefaultsToAFreshSession(t *testing.T) {
	if got := (Request{Role: roles.Implementer}).session(); got != roles.Fresh {
		t.Fatalf("implementer session = %q, want fresh", got)
	}
}

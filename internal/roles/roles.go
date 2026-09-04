// Package roles makes agent jobs semantic rather than a property of which
// provider happened to be configured.
//
// The failure this exists to prevent is subtle and it does not look like a
// failure at the time. Ask three models "is this correct?" and three of them
// say yes, and the run reads as three independent confirmations. It is not:
// they were given the same question, usually derived from the same plan, and
// often — when one provider serves several roles — the same conversation. What
// looks like corroboration is one opinion counted three times, and the moment
// it is wrong every copy of it is wrong in the same direction.
//
// So a role here is a job with its own instruction shape, its own context, and
// its own session. Two rules follow, and both are enforced by type rather than
// by prompt text:
//
//	An adversarial role never inherits the session state of what it attacks.
//	A cross-agent artifact is about exactly one task, base and candidate
//	revision, and is refused when it is about any other.
//
// Nothing in this package decides anything governed. A reviewer verdict is a
// reviewer's opinion with provenance attached; Sensei owns admission, and a
// package that could turn agreement into permission would be the thing this
// project exists to refuse.
package roles

import "strings"

// Role is a job. Provider names are configuration and may change under it.
type Role string

const (
	// Architect interprets the task inside certifiable architecture and owns
	// reconciliation when other roles disagree.
	Architect Role = "architect"
	// Implementer mutates an isolated candidate and nothing else.
	Implementer Role = "implementer"
	// Reviewer attacks the candidate and its proof.
	Reviewer Role = "reviewer"
	// CounterexampleHunter attacks the behavioural claim rather than the diff.
	//
	// Named and unfilled, deliberately. A hunter's verdict is a new class of
	// claim about a candidate, and until an admitted candidate can carry its
	// verdicts into Sensei (sensei-code#22) that claim would terminate in this
	// repository — agents agreeing amongst themselves, which is the failure this
	// vocabulary exists to prevent rather than to dress up. The session rule is
	// stated here now so that the role arrives independent when it arrives.
	CounterexampleHunter Role = "counterexample_hunter"
	// ProofRunner executes obligations and reports what actually ran.
	//
	// Named because the vocabulary is closed and this is one of the jobs, and
	// unassigned today: the execution broker runs the validation commands
	// itself, and nothing routes a proof obligation to an agent. A reader should
	// take this constant as vocabulary rather than as a role somebody is
	// currently filling.
	ProofRunner Role = "proof_runner"
)

// All is the closed vocabulary. A role outside it is a configuration error, not
// a new capability: an unknown role has no instruction shape, no context rule
// and no session rule, so accepting one would mean running an agent with none
// of the three.
var All = []Role{Architect, Implementer, Reviewer, CounterexampleHunter, ProofRunner}

func (r Role) Valid() bool {
	for _, known := range All {
		if r == known {
			return true
		}
	}
	return false
}

func (r Role) String() string { return string(r) }

// Label is what a human reads in the transcript.
func (r Role) Label() string {
	return strings.ReplaceAll(string(r), "_", " ")
}

// Adversarial reports whether this role exists to attack another role's output.
//
// It is the whole reason session independence is a rule rather than a
// preference. An attacker that inherits its target's conversation has already
// read the argument for why the work is right, in the target's own words, before
// forming a view — and it will agree more often than an attacker that had to
// look at the artifact cold. That agreement is worth nothing, and it is
// indistinguishable from the real thing in the transcript.
func (r Role) Adversarial() bool {
	return r == Reviewer || r == CounterexampleHunter
}

// Mutates reports whether the role is allowed to change a candidate. It is a
// statement about what the role is for; the mechanical boundary is the broker's
// capability envelope and the candidate worktree, not this predicate.
func (r Role) Mutates() bool { return r == Implementer }

// Session is how a role's provider conversation is opened.
type Session string

const (
	// Continue inherits the architectural conversation. Only the architect
	// does: it IS that conversation, and reconstructing it every turn would
	// discard the dialogue the human is having.
	Continue Session = "continue"
	// Fresh opens a provider session that inherits nothing.
	//
	// It is a claim this project is entitled to make only about a process it
	// started: agent.CLI launches the provider, chooses the mode, and reports
	// what it actually ran. Fresh means observed, not intended.
	Fresh Session = "fresh"
	// Unverified is a context this project did not open and cannot observe.
	//
	// It exists because a remote agent's conversation lives on the far side of
	// a transport, and neither of the other two values can honestly describe
	// it. Fresh would be a claim of isolation nobody checked -- the exact claim
	// the adversarial-independence invariant exists to require evidence for.
	// Continue would assert an inheritance that may not have happened. The zero
	// value is not available either: it already means "the caller stated no
	// preference, apply the role's default", which is a request, and this is a
	// finding.
	//
	// So the missing fact gets a name. Independent() reads Fresh and only
	// Fresh, so a turn recorded as Unverified satisfies no adversarial
	// obligation anywhere, forever, without any caller having to remember.
	Unverified Session = "unverified"
)

// AllSessions is the closed vocabulary.
var AllSessions = []Session{Continue, Fresh, Unverified}

// Valid reads the closed set by membership. An unknown session mode is a
// configuration error, not a new kind of isolation.
func (s Session) Valid() bool {
	for _, known := range AllSessions {
		if s == known {
			return true
		}
	}
	return false
}

func (s Session) String() string { return string(s) }

// SessionMode is the default session for a role, and adversarial roles get
// Fresh by construction rather than by remembering to pass it.
func (r Role) SessionMode() Session {
	if r == Architect {
		return Continue
	}
	return Fresh
}

// Capability is a provider adapter declaring which roles it can satisfy.
//
// This is declared rather than inferred because the answer is not a property of
// the model. ChatGPT reaches this project through the Codex app-server as a
// read-only architectural authority: it can architect and it can review, and it
// cannot implement, which is a fact about the transport and not about how good
// the model is at writing code.
type Capability struct {
	Provider string `json:"provider"`
	Roles    []Role `json:"roles"`
}

func (c Capability) Satisfies(r Role) bool {
	for _, have := range c.Roles {
		if have == r {
			return true
		}
	}
	return false
}

// Capabilities is the declared set for a deployment.
type Capabilities []Capability

// For returns the providers that declare they can serve a role, in the order
// they were configured. Order is the fallback order.
func (cs Capabilities) For(r Role) []string {
	var out []string
	for _, c := range cs {
		if c.Satisfies(r) {
			out = append(out, c.Provider)
		}
	}
	return out
}

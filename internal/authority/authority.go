package authority

import "fmt"

type Level uint8

const (
	Execution Level = iota + 1
	Architectural
	Human
)

func (l Level) String() string {
	switch l {
	case Execution:
		return "execution"
	case Architectural:
		return "architectural"
	case Human:
		return "human"
	default:
		return fmt.Sprintf("authority(%d)", l)
	}
}

// Outcome is what choosing an option does to the run.
//
// It is a property of the option, assigned by the orchestrator, and never
// derived from the option's text. An earlier design decided this by matching
// substrings in the label -- and the labels can be supplied by the architect,
// which meant the party the boundary exists to constrain was writing the words
// that decided whether a human's answer authorized anything.
type Outcome string

const (
	// Authorize settles the condition: the run may proceed past it.
	Authorize Outcome = "authorize"
	// Revise refuses this plan and leaves the condition open. The next plan is
	// routed on its own merits rather than refused from the record.
	//
	// It is distinct from Decline because "not this design" and "not this
	// change, ever" are different answers, and only one of them should end a
	// task. Conflating them made "require another design" unsatisfiable
	// whenever the condition was a property of the region rather than of the
	// plan: every redesign re-reached the same condition and was refused by the
	// human's own earlier answer.
	Revise Outcome = "revise"
	// Decline settles the condition negatively: this change is not authorized.
	Decline Outcome = "decline"
	// Stop ends the task.
	Stop Outcome = "stop"
)

// Settles reports whether this outcome answers the condition for the task, so
// the router need not ask again. Revise deliberately does not.
func (o Outcome) Settles() bool { return o == Authorize || o == Decline || o == Stop }

// Permits reports whether the run may proceed past the condition.
func (o Outcome) Permits() bool { return o == Authorize }

func (o Outcome) Valid() bool {
	return o == Authorize || o == Revise || o == Decline || o == Stop
}

type Option struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	// Outcome is what this option does. It is set by the orchestrator when the
	// decision surface is composed, never by a model: an option arriving from
	// an architect carries text and no authority.
	Outcome Outcome `json:"outcome,omitempty"`
}

type Decision struct {
	Level          Level    `json:"level"`
	Subject        string   `json:"subject"`
	Reason         string   `json:"reason"`
	Recommendation string   `json:"recommendation,omitempty"`
	Options        []Option `json:"options,omitempty"`
}

func (d Decision) RequiresHuman() bool { return d.Level == Human }

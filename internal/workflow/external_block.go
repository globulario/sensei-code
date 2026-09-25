package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/provider"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/runreceipt"
)

// PROVEN TEMPORARY PROVIDER UNAVAILABILITY IS EXTERNAL EXECUTION STATE.
//
// It is not task failure and it is not role output. The first dogfood run
// (2026-09-18, task-1789770525156538509) reached its architect, the ChatGPT
// account was out of quota, the turn was retried with "No response was received"
// guidance, and the task ended FAILED -- which FindInterrupted reads as done, so
// continuing the same objective once quota returned meant minting a new task.
// Nothing about the objective had failed.
//
// Three rules shape this file:
//
//   - The PROVIDER ADAPTER establishes unavailability (provider.Unavailable). The
//     workflow never reads a message to decide it.
//   - The ROLE is attached where the turn was asked, as RoleUnavailable. A
//     provider.Unavailable that no role site claimed is not classified here; it
//     stays an ordinary failure. Attribution is never inferred downstream.
//   - One primitive for every role. The architect was merely the first to expose
//     the missing semantic; the implementer uses the same type and terminal. The
//     reviewer keeps its own #180 path (WAITING_REVIEW), which already preserves
//     the candidate and treats availability as transport state.
//
// AN EXHAUSTED ROLE ROSTER IS THE SECOND WAY IN, and it is established
// STRUCTURALLY rather than from any provider's words. The roster walk puts one
// condition in roles.ArchitectUnobtainable: every entry was asked and none
// produced an architect answer. A bounded decision, a refusal from a party that
// was reached, a resolver refusal and a caller stop all return from the entry
// that produced them and never reach that type, so its arrival here is proof of
// the condition without anything reading a message to decide it.
//
// The two ways in are kept apart where it matters. Only an adapter mints a
// provider.Unavailable, so only a PROVEN refusal carries a reset time; a roster
// that merely produced nothing usable records its retry time as UNKNOWN, which
// is what stops a time nobody stated from becoming a moment to wake up on.

// RoleUnavailable is a role turn the task is owed and that no authorized
// provider could serve, because each one tried proved it was unavailable.
type RoleUnavailable struct {
	Role roles.Role
	// Provider is the configured provider that refused last.
	Provider string
	// Cause is the adapter's proof. Never nil for a value this package builds.
	Cause *provider.Unavailable
}

func (r *RoleUnavailable) Error() string {
	return fmt.Sprintf("the %s turn could not be served: %v", r.Role.Label(), r.Cause)
}

func (r *RoleUnavailable) Unwrap() error { return r.Cause }

// roleUnavailable attaches a role to a proven provider unavailability, or
// returns nil when err does not carry one. Call it only where the turn for that
// role was asked.
func roleUnavailable(role roles.Role, providerName string, err error) *RoleUnavailable {
	u, ok := provider.AsUnavailable(err)
	if !ok {
		return nil
	}
	return &RoleUnavailable{Role: role, Provider: providerName, Cause: u}
}

// Retry-time states. KNOWN only when the provider supplied a structured time.
const (
	RetryAtKnown   = "KNOWN"
	RetryAtUnknown = "UNKNOWN"
)

// ReasonNoArchitectObtained is the reason code for an exhausted architect
// roster in which no entry published anything about itself.
//
// It is THIS ENGINE's code, not a provider's, and it is deliberately not
// spelled like one. No adapter emits it, no provider.Unavailable carries it, and
// nothing derives a retry time from it: a roster recorded under this reason
// always states its retry time as UNKNOWN, because nobody said when they would
// serve again and a moment nobody stated must not be invented.
const ReasonNoArchitectObtained = "ENGINE_NO_ARCHITECT_OBTAINED"

// ExternalBlock is the durable record of a WorkflowBlockedExternal terminal: the
// payload FindInterrupted carries back byte for byte, so a later process retries
// the same turn of the same task.
type ExternalBlock struct {
	TaskID   string `json:"task_id"`
	Role     string `json:"role"`
	Provider string `json:"provider"`
	// Reason is a structured code, never a paraphrase and never prose.
	//
	// It is the PROVIDER's own code whenever a provider proved it cannot serve
	// now. It is ReasonNoArchitectObtained when nothing was proven and the
	// condition is the one this engine established itself: the roster ran out.
	// The two are distinguishable on the record, which is the point -- a reader
	// can tell a published refusal from an engine finding without reading Detail.
	Reason string `json:"reason"`
	// RetryAtState is KNOWN or UNKNOWN; RetryAt is present only when KNOWN.
	RetryAtState string `json:"retry_at_state"`
	RetryAt      string `json:"retry_at,omitempty"`
	// Detail is for people: the provider's own message, or for an exhausted
	// roster the ordered account of every entry and its reason. Nothing reads it,
	// and no decision is ever taken from it.
	Detail string `json:"detail,omitempty"`
}

// Block renders the durable record for one task.
func (r *RoleUnavailable) Block(taskID string) ExternalBlock {
	b := ExternalBlock{
		TaskID:       taskID,
		Role:         string(r.Role),
		Provider:     r.Provider,
		RetryAtState: RetryAtUnknown,
	}
	if r.Cause != nil {
		b.Reason = r.Cause.Reason
		b.Detail = r.Cause.Detail
		if !r.Cause.RetryAt.IsZero() {
			b.RetryAtState = RetryAtKnown
			b.RetryAt = r.Cause.RetryAt.UTC().Format(time.RFC3339)
		}
	}
	return b
}

// Describe is the one-line account the receipt and the terminal carry.
func (b ExternalBlock) Describe() string {
	retry := "retry_at UNKNOWN"
	if b.RetryAtState == RetryAtKnown {
		retry = "retry_at " + b.RetryAt
	}
	return fmt.Sprintf("%s turn: %s reported %s; %s", b.Role, b.Provider, b.Reason, retry)
}

// ParseExternalBlock reads a recorded block back, refusing one that cannot say
// which turn is owed or that states a retry time it cannot justify.
func ParseExternalBlock(raw json.RawMessage) (ExternalBlock, error) {
	var b ExternalBlock
	if err := json.Unmarshal(raw, &b); err != nil {
		return ExternalBlock{}, fmt.Errorf("the external block record is unreadable: %w", err)
	}
	if strings.TrimSpace(b.TaskID) == "" {
		return ExternalBlock{}, errors.New("the external block record names no task")
	}
	if !roles.Role(b.Role).Valid() {
		return ExternalBlock{}, fmt.Errorf("the external block record names no known role (%q)", b.Role)
	}
	if strings.TrimSpace(b.Provider) == "" || strings.TrimSpace(b.Reason) == "" {
		return ExternalBlock{}, errors.New("the external block record does not name the provider and its reason")
	}
	switch b.RetryAtState {
	case RetryAtUnknown:
		if b.RetryAt != "" {
			return ExternalBlock{}, errors.New("the external block record states a retry time it calls UNKNOWN")
		}
	case RetryAtKnown:
		if _, err := time.Parse(time.RFC3339, b.RetryAt); err != nil {
			return ExternalBlock{}, fmt.Errorf("the external block record's retry time is not a time: %w", err)
		}
	default:
		return ExternalBlock{}, fmt.Errorf("the external block record's retry state %q is not KNOWN or UNKNOWN", b.RetryAtState)
	}
	return b, nil
}

// blockExternally ends the invocation as BLOCKED_EXTERNAL when err carries an
// external execution condition, and reports whether it did.
//
// The task is left exactly as it stands: no candidate disposal, no handoff, no
// new identity. It is the one terminal both run and resume reach for this
// condition, so a resumed task that is blocked again says so the same way.
func (e *Engine) blockExternally(taskID string, err error) bool {
	role, b, ok := externalBlockFor(taskID, err)
	if !ok {
		return false
	}
	e.noteExternalBlock(taskID, b)
	e.emitRunTerminal(taskID, event.WorkflowBlockedExternal, event.SourceSystem,
		runreceipt.OutcomeBlockedExternal, e.candidateStateFor(taskID),
		"the "+role.Label()+" turn this task is owed could not be served: "+b.Describe()+
			". The task is preserved; resume it to retry that turn", b)
	return true
}

// externalBlockFor is the durable record a terminal reports for err, and the
// role it is owed to, or ok=false when err carries no external condition.
//
// An exhausted role roster is checked FIRST, because it carries one cause per
// provider and errors.As would answer with whichever happens to come first in
// the chain. Which entry the record names is a decision, not an accident: see
// rosterBlock.
func externalBlockFor(taskID string, err error) (roles.Role, ExternalBlock, bool) {
	var chain *roles.ArchitectUnobtainable
	if errors.As(err, &chain) && chain != nil && len(chain.Attempted) > 0 {
		return roles.Architect, rosterBlock(taskID, chain), true
	}
	var ru *RoleUnavailable
	if errors.As(err, &ru) && ru != nil && ru.Cause != nil {
		return ru.Role, ru.Block(taskID), true
	}
	return "", ExternalBlock{}, false
}

// rosterBlock is the durable record for an exhausted architect roster.
//
// EVERY entry in the chain failed to obtain an architect answer -- an entry that
// could not be reached, one that exhausted its quota, one that timed out or
// exited non-zero, one that never produced parseable output inside its attempt
// budget. That is the only condition the roster walk puts in this chain: a
// bounded decision, a refusal from a party that WAS reached, a resolver refusal
// and a caller stop each return from the entry that produced them and never
// arrive here. So the whole chain is one finding -- no architect was available
// -- and it is reported as external execution state rather than as the architect
// failing to decide. They are different findings and must not share a terminal.
//
// Which entry the record NAMES is chosen rather than taken, in this order:
//
//   - the provider that PROVED a temporary unavailability and stated the
//     EARLIEST reset time, because that is the first moment the turn this task
//     is owed can be served again. Preferring a stated time over an unknown one
//     is what keeps "the reset time is known" from depending on which provider
//     happened to be configured first;
//   - with none stated, the last refusal that proved one -- the one that
//     exhausted the roster;
//   - with nothing proven at all, the last entry tried, and the retry time is
//     UNKNOWN. Nobody published a reset, so there is none to carry, and this is
//     the case that must never be auto-resumed on an invented time.
func rosterBlock(taskID string, chain *roles.ArchitectUnobtainable) ExternalBlock {
	if proven := provenRosterRefusal(chain.Attempted); proven != nil {
		return proven.Block(taskID)
	}
	// The last entry tried, which is the one that exhausted the roster. A roster
	// entry the configuration left unnamed cannot be named here either; the
	// record refuses itself on read rather than inventing a party, and the
	// terminal is still the right one.
	last := chain.Attempted[len(chain.Attempted)-1]
	for i := len(chain.Attempted) - 1; i >= 0; i-- {
		if strings.TrimSpace(chain.Attempted[i].Provider) != "" {
			last = chain.Attempted[i]
			break
		}
	}
	return ExternalBlock{
		TaskID:       taskID,
		Role:         string(roles.Architect),
		Provider:     last.Provider,
		Reason:       ReasonNoArchitectObtained,
		RetryAtState: RetryAtUnknown,
		// Every entry and its own reason, for the person who has to decide what
		// to do about a roster that answered nothing. Nothing reads it.
		Detail: chain.Error(),
	}
}

// provenRosterRefusal is the adapter-proven unavailability an exhausted roster
// reports, or nil when no entry proved one.
//
// Only an ADAPTER mints a provider.Unavailable, so only a path through here can
// carry a published reset time. Nothing above synthesizes one: an engine finding
// that dressed itself as a provider's proof would make the record say a party
// published something it never said.
func provenRosterRefusal(attempted []roles.ArchitectAttemptFailure) *RoleUnavailable {
	var best *RoleUnavailable
	for _, a := range attempted {
		var ru *RoleUnavailable
		if !errors.As(a.Cause, &ru) || ru == nil || ru.Cause == nil {
			continue
		}
		switch {
		case best == nil:
			best = ru
		case best.Cause.RetryAt.IsZero():
			// Nothing stated so far, so a later refusal is the more current
			// account of it -- and any stated time replaces an unknown one.
			best = ru
		case !ru.Cause.RetryAt.IsZero() && ru.Cause.RetryAt.Before(best.Cause.RetryAt):
			best = ru
		}
	}
	return best
}

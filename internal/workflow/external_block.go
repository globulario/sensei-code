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

// ExternalBlock is the durable record of a WorkflowBlockedExternal terminal: the
// payload FindInterrupted carries back byte for byte, so a later process retries
// the same turn of the same task.
type ExternalBlock struct {
	TaskID   string `json:"task_id"`
	Role     string `json:"role"`
	Provider string `json:"provider"`
	// Reason is the provider's own structured code, verbatim.
	Reason string `json:"reason"`
	// RetryAtState is KNOWN or UNKNOWN; RetryAt is present only when KNOWN.
	RetryAtState string `json:"retry_at_state"`
	RetryAt      string `json:"retry_at,omitempty"`
	// Detail is the provider's message, for people. Nothing reads it.
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

// blockExternally ends the invocation as BLOCKED_EXTERNAL when err carries a
// role-attributed provider unavailability, and reports whether it did.
//
// The task is left exactly as it stands: no candidate disposal, no handoff, no
// new identity. It is the one terminal both run and resume reach for this
// condition, so a resumed task that is blocked again says so the same way.
func (e *Engine) blockExternally(taskID string, err error) bool {
	ru := externalBlockCause(err)
	if ru == nil {
		return false
	}
	b := ru.Block(taskID)
	e.noteExternalBlock(taskID, b)
	e.emitRunTerminal(taskID, event.WorkflowBlockedExternal, event.SourceSystem,
		runreceipt.OutcomeBlockedExternal, e.candidateStateFor(taskID),
		"the "+ru.Role.Label()+" turn this task is owed could not be served: "+b.Describe()+
			". The task is preserved; resume it to retry that turn", b)
	return true
}

// externalBlockCause is the proven, role-attributed unavailability a terminal
// reports, or nil when err carries none.
//
// An exhausted role roster is checked FIRST, because it carries one cause per
// provider and errors.As would answer with whichever happens to come first in
// the chain. Which entry the record names is a decision, not an accident: see
// rosterBlock.
func externalBlockCause(err error) *RoleUnavailable {
	var chain *roles.ArchitectUnobtainable
	if errors.As(err, &chain) && chain != nil {
		return rosterBlock(chain.Attempted)
	}
	var ru *RoleUnavailable
	if errors.As(err, &ru) && ru != nil && ru.Cause != nil {
		return ru
	}
	return nil
}

// rosterBlock is the proven unavailability an exhausted roster reports, or nil
// when no entry proved one.
//
// Its absence is as load-bearing as its presence. A roster every entry of which
// was REACHED and simply produced nothing usable proved no unavailability, and
// must not be recorded as the outside world being unavailable: that would map a
// genuine failure to decide onto a terminal that means "come back later", which
// is the mirror image of the bug this repairs.
//
// When there is one, which entry the record names is chosen rather than taken:
// the EARLIEST known reset time, because that is the first moment the turn this
// task is owed can be served again. With none known it is the last refusal, the
// one that exhausted the roster. Preferring a stated time over an unknown one is
// what keeps "the reset time is known" from depending on which provider happened
// to be configured first.
func rosterBlock(attempted []roles.ArchitectAttemptFailure) *RoleUnavailable {
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

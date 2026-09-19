package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/runreceipt"
)

// NON-CONVERGENCE IS OWED AN ARCHITECT RE-PLAN, NOT A FINAL FAILURE.
//
// DF-6, observed 2026-09-19 on task-1789775984590842441: Claude and then Codex
// each spent every review cycle, every cycle validated green, every review was a
// bounded REVISE with real findings -- and the task ended WorkflowFailed, which
// FindInterrupted reads as final. In the same instant the candidate record said
// disposition "resumable": 45 KB of reviewed work that "resumable state
// references". `resume --task` answered "no interrupted task with that id". Two
// records disagreed about whether the task was alive, and the resumable one was
// unreachable.
//
// The owner's ruling: exhausting every implementer's review cycles is its own
// terminal. The task stays itself, the candidate stays as it stands, and what it
// owes is an ARCHITECT re-plan over the candidate and its open findings -- the
// party that can change the plan, rather than another implementer re-running the
// same plan against the same objection. Resuming delivers exactly that, and
// records the re-plan as the task's plan (PlanProposed), which discharges the
// obligation and binds every later resume to the revised plan.
//
// Only exhaustion qualifies. A worker that errored, a structural refusal, a
// refuted prospective grant, or a failure beside exhaustion stay FAILED: they are
// not "the reviewer still objects", and calling them re-plannable would let an
// arbitrary failure buy another round.

// errReviewCyclesExhausted marks the one worker failure that means "every cycle
// was spent and the reviewer still asks for revision".
var errReviewCyclesExhausted = errors.New("candidate did not converge")

// OwedArchitectReplan is the only thing a non-converged task owes.
const OwedArchitectReplan = "architect_replan"

// NotConverged is the durable record of a WorkflowNotConverged terminal,
// carried back byte for byte by FindInterrupted.
type NotConverged struct {
	TaskID string `json:"task_id"`
	// Implementers are the workers that each exhausted their cycles, in order.
	Implementers []string `json:"implementers"`
	// ReviewCycles is the per-implementer budget each one spent.
	ReviewCycles int `json:"review_cycles"`
	// Owed is what a resume delivers: always OwedArchitectReplan.
	Owed string `json:"owed"`
}

// Describe is the one-line account the receipt and the terminal carry.
func (n NotConverged) Describe() string {
	return fmt.Sprintf("%s each spent %d review cycles and the reviewer still requires revision; owed: %s",
		strings.Join(n.Implementers, ", "), n.ReviewCycles, n.Owed)
}

// ParseNotConverged reads a recorded non-convergence back, refusing one that
// cannot say which task it belongs to or what it owes.
func ParseNotConverged(raw json.RawMessage) (NotConverged, error) {
	var n NotConverged
	if err := json.Unmarshal(raw, &n); err != nil {
		return NotConverged{}, fmt.Errorf("the non-convergence record is unreadable: %w", err)
	}
	if strings.TrimSpace(n.TaskID) == "" {
		return NotConverged{}, errors.New("the non-convergence record names no task")
	}
	if len(n.Implementers) == 0 || n.ReviewCycles <= 0 {
		return NotConverged{}, errors.New("the non-convergence record does not say who spent which review budget")
	}
	if n.Owed != OwedArchitectReplan {
		return NotConverged{}, fmt.Errorf("the non-convergence record owes %q, which no resume delivers", n.Owed)
	}
	return n, nil
}

// endNotConverged ends the invocation as NOT_CONVERGED. The task is not
// finished: the candidate stands and a resume re-plans it.
func (e *Engine) endNotConverged(taskID string, n NotConverged) {
	e.noteNotConverged(taskID, n)
	e.emitRunTerminal(taskID, event.WorkflowNotConverged, event.SourceSystem,
		runreceipt.OutcomeNotConverged, e.candidateStateFor(taskID),
		"the candidate did not converge: "+n.Describe()+
			". The task and its candidate are preserved; resume it to have the architect re-plan", n)
}

// replanPrompt asks the architect for a revised bounded plan for a candidate
// that did not converge, or whose re-plan was blocked. It is the escalation
// prompt's sibling: the same contract, a different reason.
func replanPrompt(task, plan, why, openFindings string) string {
	findings := strings.TrimSpace(openFindings)
	if findings == "" {
		findings = "(the record carries no review text)"
	}
	return fmt.Sprintf(`The bounded implementation of this task is owed an architectural re-plan: %s

The candidate is preserved and already contains the implementers' work. Revise the plan with your architectural authority so that the open findings below are resolved by design, not patched one instance at a time: if the same class of finding kept recurring, the plan should route that concern through one owner. Escalate to the human only if the decision changes human-owned intent/policy/contract/trust authority. Otherwise issue a revised bounded plan.

TASK:
%s

CURRENT PLAN:
%s

OPEN FINDINGS (latest independent review):
%s

Return ONLY the same architecture JSON contract as before.`, why, task, plan, findings)
}

// owedReplan reports whether a planned task's resume must begin with an
// architect re-plan, and why. A non-convergence record owes one; so does an
// architect turn that was blocked on a planned task (a re-plan inside a cycle).
func owedReplan(notConverged, blocked json.RawMessage, taskID string) (string, bool, error) {
	if len(notConverged) != 0 {
		n, err := ParseNotConverged(notConverged)
		if err == nil && n.TaskID != taskID {
			err = fmt.Errorf("the non-convergence record is bound to task %s, not to this one", n.TaskID)
		}
		if err != nil {
			return "", false, err
		}
		return "the candidate did not converge: " + n.Describe(), true, nil
	}
	if len(blocked) != 0 {
		if b, err := ParseExternalBlock(blocked); err == nil && b.Role == string(roles.Architect) {
			return "the architect re-plan this task was owed was blocked (" + b.Describe() + ")", true, nil
		}
	}
	return "", false, nil
}

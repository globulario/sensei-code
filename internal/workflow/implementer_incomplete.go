package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/taskstate"
	"github.com/globulario/sensei-code/internal/validation"
)

// THE INCOMPLETE IMPLEMENTER OBLIGATION REMAINS IN THE SAME LIVE REVIEW CYCLE
// (DF-41A3, RULING-145).
//
// A review cycle owns one exact outstanding finding obligation: the findings the
// open review left, by their full identity. An implementer invocation that
// returns before every one of them has a valid canonical response does not
// complete that cycle and does not spend it. The engine keeps every valid
// response the canonical validator (accountForFindings) accepted, and the next
// invocation is asked only for what is still owed -- in the same cycle, under
// the same number, whichever runner or provider serves it.
//
// The obligation is keyed by the logical cycle, never by one invocation: task,
// operative PlanAttempt, and the open review it answers. A different review, a
// superseded finding, or a PlanAttempt that replaced the operative one without
// an explicit continuation (continueCycleCompletion) is a different obligation,
// and nothing retained under the old one satisfies it.
//
// It lives in memory. Its durable form is a replay capsule (70B1, RULING-153):
// the canonical inputs it was built from, which only replaying them through the
// same owners turns back into an obligation. Restoring it in a fresh process is
// not this record's job.

// maxIncompleteImplementerAttempts is the internal safety bound on settled
// incomplete implementer invocations serving one logical review cycle. It is a
// workflow constant on purpose: no user, project or provider setting reaches it,
// and it is separate from the review-cycle budget, which it never touches.
const maxIncompleteImplementerAttempts = 3

// ImplementerIncompleteState is the machine-readable state an exhausted
// incomplete obligation reports, inside the existing failed terminal.
const ImplementerIncompleteState = "IMPLEMENTER_INCOMPLETE"

// cycleCompletion is one logical review cycle's live completion obligation.
type cycleCompletion struct {
	TaskID        string
	PlanAttemptID string
	// Cycle is the logical review-cycle number the obligation was established
	// in. Incomplete retries never change it.
	Cycle int
	// ReviewAttempt, CandidateDigest and CandidateTree name the open review
	// the findings were raised by.
	ReviewAttempt   int
	CandidateDigest string
	CandidateTree   string
	// Findings are the outstanding findings, as the reviewer recorded them.
	Findings []roles.Finding
	// Retained are the responses the canonical validator accepted, by finding
	// id. Only absorb writes it.
	Retained map[string]findingResponse
	// Why says, for each finding still owed, why the last account left it open.
	Why map[string]string
	// OpenOperations are the last reconciled settled invocation's unterminated
	// structured operations, as id#generation: settled lifecycle context the
	// next invocation of this cycle is told about.
	OpenOperations []string
	// Attempts counts settled implementer invocations that left this cycle
	// incomplete and that the workflow chose to serve it again.
	Attempts int
	// Route is the workflow route last selected for the cycle. Only transition
	// writes it, and only transition writes Continuing and Attempts: absorbing
	// an account retains facts and never selects a route.
	Route cycleRoute
	// Continuing says the cycle stays live for the next implementer this
	// process selects: that invocation resumes it at Cycle, with the retained
	// responses as settled context and exactly Owed as its obligation -- which
	// may be empty. It is independent of whether any finding is still owed.
	Continuing bool
	// Continuations are the PlanAttempt transitions this obligation was
	// explicitly carried across, as "from->to".
	Continuations []string
	// Origin is the operative PlanAttempt the obligation was established
	// under, with the objective its identity was derived from.
	Origin planAttemptBinding
	// Steps are the canonical inputs of every transition the obligation took,
	// in order: what a replay drives the same owners with.
	Steps []replayStep
	// CheckpointID is the last checkpoint this process committed for the
	// obligation, if any.
	CheckpointID string
}

func newCycleCompletion(taskID, planAttemptID string, cycle int, open openReview) *cycleCompletion {
	return &cycleCompletion{
		TaskID: taskID, PlanAttemptID: planAttemptID, Cycle: cycle,
		ReviewAttempt: open.Attempt, CandidateDigest: open.CandidateDigest, CandidateTree: open.CandidateTree,
		Findings: append([]roles.Finding(nil), outstandingFindings(open)...),
		Retained: map[string]findingResponse{}, Why: map[string]string{},
	}
}

// answers reports whether c is the obligation of this open review: the same
// review, raised on the same candidate, owing the same findings by their full
// identity. Id equality alone is not identity.
func (c *cycleCompletion) answers(open openReview) bool {
	outstanding := outstandingFindings(open)
	if c.ReviewAttempt != open.Attempt || c.CandidateDigest != open.CandidateDigest ||
		c.CandidateTree != open.CandidateTree || len(c.Findings) != len(outstanding) {
		return false
	}
	for i := range outstanding {
		if c.Findings[i] != outstanding[i] {
			return false
		}
	}
	return true
}

// binds reports whether c is the live obligation for this task, operative
// PlanAttempt and open review.
func (c *cycleCompletion) binds(taskID, planAttemptID string, open openReview) bool {
	return c != nil && c.TaskID == taskID && c.PlanAttemptID == planAttemptID && c.answers(open)
}

// bindsCycle is binds, in the logical review cycle cycle: the same findings
// owed in another cycle are another cycle's obligation.
func (c *cycleCompletion) bindsCycle(taskID, planAttemptID string, cycle int, open openReview) bool {
	return c.binds(taskID, planAttemptID, open) && c.Cycle == cycle
}

func (c *cycleCompletion) clone() *cycleCompletion {
	if c == nil {
		return nil
	}
	out := *c
	out.Findings = append([]roles.Finding(nil), c.Findings...)
	out.Continuations = append([]string(nil), c.Continuations...)
	out.Steps = append([]replayStep(nil), c.Steps...)
	out.Retained = make(map[string]findingResponse, len(c.Retained))
	for k, v := range c.Retained {
		out.Retained[k] = v
	}
	out.Why = make(map[string]string, len(c.Why))
	for k, v := range c.Why {
		out.Why[k] = v
	}
	return &out
}

// responsesFor is the response set the canonical validator judges for one
// invocation: every retained response, and the invocation's own responses.
//
// A later response naming a retained finding is not discarded: the canonical
// validator decides it. Only a restatement -- exactly one later envelope, the
// same response as the one retained -- is the retained response again, and it
// is judged once, in restated. Anything else for that finding -- a different
// answer, a dispute, a duplicate -- is judged beside the retained response,
// where the canonical conflict rule applies to that finding alone; its id is
// returned in conflicted. Unrelated retained siblings are not touched.
func (c *cycleCompletion) responsesFor(fresh []findingResponse) (judged []findingResponse, restated, conflicted []string) {
	later := map[string][]findingResponse{}
	for _, r := range fresh {
		id := strings.TrimSpace(r.ID)
		later[id] = append(later[id], r)
	}
	retained := map[string]bool{}
	for _, f := range c.Findings {
		id := strings.TrimSpace(f.ID)
		r, ok := c.Retained[id]
		if !ok || retained[id] {
			continue
		}
		retained[id] = true
		judged = append(judged, r)
		switch again := later[id]; {
		case len(again) == 0:
		case len(again) == 1 && sameResponse(again[0], r):
			restated = append(restated, id)
		default:
			judged = append(judged, again...)
			conflicted = append(conflicted, id)
		}
	}
	for _, r := range fresh {
		if !retained[strings.TrimSpace(r.ID)] {
			judged = append(judged, r)
		}
	}
	return judged, restated, conflicted
}

// sameResponse reports whether two envelopes are the same response, field for
// field.
func sameResponse(a, b findingResponse) bool {
	if strings.TrimSpace(a.ID) != strings.TrimSpace(b.ID) || a.AnsweredBy != b.AnsweredBy || a.Evidence != b.Evidence ||
		a.DisputesClass != b.DisputesClass || a.Reason != b.Reason || len(a.Paths) != len(b.Paths) {
		return false
	}
	for i := range a.Paths {
		if a.Paths[i] != b.Paths[i] {
			return false
		}
	}
	return true
}

// judging are the findings an account must judge when it cannot re-judge the
// retained responses -- an invocation that returned with an error, before this
// cycle's validation ran: every finding still owed, and every retained finding
// a later envelope conflicts with. A retained response nothing contradicts
// stands; nothing here can show it no longer applies.
func (c *cycleCompletion) judging(conflicted []string) []roles.Finding {
	reopen := map[string]bool{}
	for _, id := range conflicted {
		reopen[id] = true
	}
	var out []roles.Finding
	for _, f := range c.Findings {
		id := strings.TrimSpace(f.ID)
		if _, held := c.Retained[id]; !held || reopen[id] {
			out = append(out, f)
		}
	}
	return out
}

// absorb records what the canonical account concluded about the findings it
// judged -- judging, exactly the outstanding set the account was asked about:
// a judged finding it discharged is retained with the response that discharged
// it, and a judged finding it left open is owed, whatever was retained for it
// before. A finding the account did not judge is not touched: its retained
// response, if any, stands as it was. It also keeps the settled invocation's
// open structured operations. It returns the retained findings the account
// showed no longer apply.
//
// It is fact-only: it selects no route, changes no liveness and counts nothing.
func (c *cycleCompletion) absorb(judging []roles.Finding, account findingAccount, judged []findingResponse, settled settledInvocation) (lapsed []string) {
	open := map[string]string{}
	for _, o := range account.Open {
		open[strings.TrimSpace(o.ID)] = o.Why
	}
	byID := map[string]findingResponse{}
	for _, r := range judged {
		byID[strings.TrimSpace(r.ID)] = r
	}
	for _, f := range judging {
		id := strings.TrimSpace(f.ID)
		delete(c.Why, id)
		if why, owed := open[id]; owed {
			if _, was := c.Retained[id]; was {
				lapsed = append(lapsed, id)
				delete(c.Retained, id)
			}
			c.Why[id] = why
			continue
		}
		if r, ok := byID[id]; ok {
			c.Retained[id] = r
		}
	}
	c.OpenOperations = openOperations(settled)
	return lapsed
}

// Owed are the findings still owed a response, in the reviewer's order:
// the current findings minus the currently retained responses.
func (c *cycleCompletion) Owed() []string {
	owed := []string{}
	for _, f := range c.Findings {
		if _, ok := c.Retained[strings.TrimSpace(f.ID)]; !ok {
			owed = append(owed, f.ID)
		}
	}
	return owed
}

// RetainedIDs are the satisfied findings, in the reviewer's order.
func (c *cycleCompletion) RetainedIDs() []string {
	held := []string{}
	for _, f := range c.Findings {
		if _, ok := c.Retained[strings.TrimSpace(f.ID)]; ok {
			held = append(held, f.ID)
		}
	}
	return held
}

// Exhausted reports that the cycle has had every incomplete attempt it may.
func (c *cycleCompletion) Exhausted() bool { return c.Attempts >= maxIncompleteImplementerAttempts }

// incompleteResponse reports that the cycle's response obligation is not met:
// a finding is still owed, or the reconciled settled invocation left a
// structured operation open.
func (c *cycleCompletion) incompleteResponse() bool {
	return len(c.Owed()) != 0 || len(c.OpenOperations) != 0
}

// cycleRoute is the route the workflow selected for a live cycle.
type cycleRoute string

const (
	// routeIncompleteRetry: a settled invocation left the cycle incomplete
	// and the candidate loop serves the same cycle again. Counted.
	routeIncompleteRetry cycleRoute = "incomplete_retry"
	// routeProviderRebound: the runner could not be resolved, or its provider
	// proved unavailable. Never counted; the cycle stays live for the next
	// provider whether or not anything is still owed.
	routeProviderRebound cycleRoute = "provider_rebound"
	// routeOrdinaryError: a settled invocation ended with an ordinary error.
	// The cycle stays live, and the outer handoff decides whether it counts.
	routeOrdinaryError cycleRoute = "ordinary_error"
	// routeErrorHandoff: the outer handoff passes an ordinary-error cycle to
	// another implementer. Counted only when the response obligation is still
	// incomplete.
	routeErrorHandoff cycleRoute = "error_handoff"
	// routeProgress: the response obligation is complete and the cycle
	// proceeds normally.
	routeProgress cycleRoute = "progress"
	// routeDiagnosis: a completed-response diagnosis (produced nothing,
	// evidence not code) ends the cycle's continuation.
	routeDiagnosis cycleRoute = "diagnosis"
	// routeDisputeEscalation: a disputed finding awaits the architect's
	// decision. Never counted and not live: no implementer may resume the
	// cycle until that decision is adopted and the obligation explicitly
	// continued under it.
	routeDisputeEscalation cycleRoute = "dispute_escalation"
)

// transition is the one owner of a cycle's liveness and incomplete-attempt
// accounting: it records the selected route, decides whether the cycle stays
// live for another implementer, and counts the attempt when the route retries
// an incomplete obligation. It reports whether it counted. A counted attempt
// that spends the allowance leaves the cycle not live: no further invocation
// may be selected for it. A route the cycle's sequencing does not admit
// (admit) is refused, and nothing changes.
func (c *cycleCompletion) transition(route cycleRoute) (counted bool, err error) {
	if err := c.admit(replayStep{Kind: stepRoute, Route: route}); err != nil {
		return false, err
	}
	c.Route = route
	c.Steps = append(c.Steps, replayStep{Kind: stepRoute, Route: route})
	switch route {
	case routeIncompleteRetry, routeErrorHandoff:
		if !c.counts(route) {
			c.Continuing = true
			return false, nil
		}
		c.Attempts++
		c.Continuing = !c.Exhausted()
		return true, nil
	case routeProviderRebound, routeOrdinaryError:
		c.Continuing = true
	default:
		c.Continuing = false
	}
	return false, nil
}

// counts reports whether route would count an incomplete attempt now.
func (c *cycleCompletion) counts(route cycleRoute) bool {
	return route == routeIncompleteRetry || (route == routeErrorHandoff && c.incompleteResponse())
}

// open reports whether the cycle may still be served: no route was selected
// yet, or the last one kept it live.
func (c *cycleCompletion) open() bool { return c.Route == "" || c.Continuing }

// admit is THE sequencing rule of a cycle's transitions, the 70A3 owner's
// own, applied to the live obligation and to its replay alike, so a replay
// can reconstruct only a sequence the live workflow could have taken:
//
//   - an invocation is accounted only to an open cycle, and only after a
//     selected route (each invocation's account is followed by its route);
//   - incomplete_retry, progress, diagnosis and dispute_escalation follow only
//     the account of a validated invocation (judgeAll); incomplete_retry also
//     follows the one continuation path below;
//   - provider_rebound and ordinary_error never follow a validated
//     invocation's account;
//   - error_handoff follows only the ordinary_error route it hands over;
//   - a PlanAttempt continuation carries an open cycle between invocations,
//     or a cycle escalated for a disputed finding, once;
//   - a closed cycle admits nothing, except the exact owner-defined
//     continuation: a dispute_escalation, then one PlanAttempt continuation,
//     then incomplete_retry;
//   - no counted transition is admitted once the allowance is spent, so an
//     exhausted cycle has exactly maxIncompleteImplementerAttempts attempts.
func (c *cycleCompletion) admit(s replayStep) error {
	var last *replayStep
	if n := len(c.Steps); n != 0 {
		last = &c.Steps[n-1]
	}
	lastIs := func(k replayStepKind, j judgingSet) bool {
		return last != nil && last.Kind == k && (j == "" || last.Judging == j)
	}
	switch s.Kind {
	case stepAccount:
		if s.Judging != judgeAll && s.Judging != judgeUnsettled {
			return fmt.Errorf("an account judging %q is not an account of this cycle", s.Judging)
		}
		if !c.open() {
			return fmt.Errorf("the cycle is closed on route %s: no invocation may be accounted to it", c.Route)
		}
		if lastIs(stepAccount, "") {
			return errors.New("an invocation is accounted only after the previous one's route was selected")
		}
	case stepContinue:
		if s.To == nil {
			return errors.New("a continuation names no plan attempt")
		}
		escalated := c.Route == routeDisputeEscalation && lastIs(stepRoute, "")
		if !escalated && (!c.open() || lastIs(stepAccount, "")) {
			return errors.New("only an open cycle between invocations, or one escalated for a disputed finding and not yet continued, may be continued to another plan attempt")
		}
	case stepRoute:
		if !s.Route.valid() {
			return fmt.Errorf("route %q is not a route", s.Route)
		}
		// continued: the dispute continuation, whose one admitted route is
		// the retry it was continued for.
		continued := lastIs(stepContinue, "") && !c.open()
		switch {
		case continued && s.Route != routeIncompleteRetry:
			return fmt.Errorf("a continued cycle is retried, not routed %s", s.Route)
		case !continued && !c.open():
			return fmt.Errorf("the cycle is closed on route %s: route %s cannot reopen it", c.Route, s.Route)
		}
		switch s.Route {
		case routeIncompleteRetry, routeProgress, routeDiagnosis, routeDisputeEscalation:
			if !lastIs(stepAccount, judgeAll) && !(s.Route == routeIncompleteRetry && continued) {
				return fmt.Errorf("route %s follows only the account of a validated invocation", s.Route)
			}
		case routeErrorHandoff:
			if c.Route != routeOrdinaryError || !c.Continuing || !lastIs(stepRoute, "") {
				return errors.New("route error_handoff follows only the ordinary_error route it hands over")
			}
		case routeProviderRebound, routeOrdinaryError:
			if lastIs(stepAccount, judgeAll) {
				return fmt.Errorf("route %s does not follow the account of a validated invocation", s.Route)
			}
		}
		if c.counts(s.Route) && c.Attempts >= maxIncompleteImplementerAttempts {
			return fmt.Errorf("route %s would count attempt %d of %d: the allowance is spent", s.Route, c.Attempts+1, maxIncompleteImplementerAttempts)
		}
	default:
		return fmt.Errorf("a %q step is not a transition", s.Kind)
	}
	return nil
}

// retires is the owner's rule for which obligation a retirement reason
// tombstones: completed retires a cycle that progressed; superseded_plan_attempt
// one escalated for a dispute whose continuation was refused (the superseding
// transition itself is proven by the Objective-64 owner); replaced_checkpoint
// one that still had a durable status. Nothing else is retired.
func (c *cycleCompletion) retires(reason session.RetirementReason) error {
	switch reason {
	case session.RetiredCompleted:
		if c.Route == routeProgress {
			return nil
		}
	case session.RetiredSupersededPlanAttempt:
		if n := len(c.Steps); c.Route == routeDisputeEscalation && n != 0 && c.Steps[n-1].Kind == stepRoute {
			return nil
		}
	case session.RetiredReplacedCheckpoint:
		if _, ok := c.durableStatus(); ok {
			return nil
		}
	default:
		return fmt.Errorf("%q is not a retirement reason", reason)
	}
	return fmt.Errorf("an obligation on route %s is not one a %s retirement tombstones", orNone(string(c.Route), "none"), reason)
}

// retryFeedback is what the next invocation of this cycle is told: the same
// cycle, the retained responses as settled context, and exactly the findings
// still owed as its response obligation. lead says why it is being asked.
func (c *cycleCompletion) retryFeedback(lead string, openOperations []string) string {
	var b strings.Builder
	if lead = strings.TrimSpace(lead); lead != "" {
		b.WriteString(lead + "\n\n")
	}
	if c.Attempts == 0 {
		fmt.Fprintf(&b, "Review cycle %d is not complete: an earlier implementer invocation of this cycle ended before the cycle could proceed. "+
			"This is the same review cycle, not a new one.\n", c.Cycle)
	} else {
		fmt.Fprintf(&b, "Review cycle %d is not complete: implementer attempt %d of %d for this cycle returned before every review finding had a valid response. "+
			"This is the same review cycle, not a new one.\n", c.Cycle, c.Attempts, maxIncompleteImplementerAttempts)
	}
	if held := c.RetainedIDs(); len(held) != 0 {
		b.WriteString("\nALREADY SATISFIED -- the engine retains these responses; they are not owed and need not be restated: " +
			strings.Join(held, ", ") + "\n")
	}
	if len(openOperations) != 0 {
		b.WriteString("\nThe previous invocation returned with structured operations still open: " +
			strings.Join(openOperations, ", ") + ". Finish the work before returning.\n")
	}
	if len(c.Owed()) == 0 {
		b.WriteString("\nNo review finding is still owed a response; finish the cycle's work and return.\n")
	} else {
		b.WriteString("\nSTILL OWED -- respond to exactly these findings:\n")
		for _, f := range c.Findings {
			id := strings.TrimSpace(f.ID)
			if _, ok := c.Retained[id]; ok {
				continue
			}
			b.WriteString("- " + f.Line())
			if why := strings.TrimSpace(c.Why[id]); why != "" {
				b.WriteString(" (still owed: " + why + ")")
			}
			b.WriteString("\n")
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// incomplete is the typed outcome of an exhausted obligation.
func (c *cycleCompletion) incomplete(openOperations []string, diagnosis string) *ImplementerIncomplete {
	return &ImplementerIncomplete{
		State: ImplementerIncompleteState, TaskID: c.TaskID, PlanAttemptID: c.PlanAttemptID,
		Cycle: c.Cycle, ReviewAttempt: c.ReviewAttempt,
		Attempts: c.Attempts, MaxAttempts: maxIncompleteImplementerAttempts,
		Owed: c.Owed(), Retained: c.RetainedIDs(),
		OpenOperations: append([]string(nil), openOperations...), Diagnosis: diagnosis,
	}
}

// ImplementerIncomplete is the operational state of a review cycle whose
// incomplete-invocation allowance is spent. It is not non-convergence: no review
// judged the candidate on these attempts, and no review cycle was consumed by
// them. It travels inside the existing failed terminal; it is not a terminal
// kind of its own.
type ImplementerIncomplete struct {
	State         string `json:"state"`
	TaskID        string `json:"task_id"`
	PlanAttemptID string `json:"plan_attempt_id"`
	// Cycle is the logical review cycle the attempts served.
	Cycle         int `json:"review_cycle"`
	ReviewAttempt int `json:"review_attempt"`
	Attempts      int `json:"attempts"`
	MaxAttempts   int `json:"max_attempts"`
	// Owed are the exact finding ids still owed a valid response.
	Owed     []string `json:"owed_findings"`
	Retained []string `json:"retained_findings,omitempty"`
	// OpenOperations are the settled invocation's unterminated structured
	// operations, as id#generation.
	OpenOperations []string `json:"open_operations,omitempty"`
	Diagnosis      string   `json:"diagnosis,omitempty"`
}

func (i *ImplementerIncomplete) Error() string {
	msg := fmt.Sprintf("%s: review cycle %d of plan attempt %s is incomplete after %d implementer attempt(s); still owed: %s",
		i.State, i.Cycle, orNone(short12(i.PlanAttemptID), "none"), i.Attempts, orNone(strings.Join(i.Owed, ", "), "none"))
	if len(i.OpenOperations) != 0 {
		msg += "; open structured operations: " + strings.Join(i.OpenOperations, ", ")
	}
	if d := strings.TrimSpace(i.Diagnosis); d != "" {
		msg += " (" + d + ")"
	}
	return msg
}

// nextReviewCycle advances the review-cycle counter, except after an
// incomplete attempt that retries its own cycle: that one keeps the number and
// spends nothing.
func nextReviewCycle(cycle int, sameCycle *bool) int {
	if *sameCycle {
		*sameCycle = false
		return cycle
	}
	return cycle + 1
}

// openOperations names a settled invocation's unterminated operations. It reads
// the settled fact only: generations were attributed when it was settled.
func openOperations(s settledInvocation) []string {
	var out []string
	for _, op := range s.Open() {
		out = append(out, fmt.Sprintf("%s#%d", op.ID, op.Generation))
	}
	return out
}

// establishCycleCompletion returns the live obligation for this task's open
// review under its operative PlanAttempt, creating it when none binds. It never
// updates a record keyed by an invocation: whichever invocation asks, the same
// cycle gets the same obligation, and a stale one is replaced, never carried.
func (e *Engine) establishCycleCompletion(taskID string, cycle int, open openReview) *cycleCompletion {
	attempt := e.operativePlanAttempt(taskID)
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.completions == nil {
		e.completions = map[string]*cycleCompletion{}
	}
	// A bound obligation the owner has closed -- progressed, diagnosed,
	// exhausted -- is not served again: a later invocation of the same review
	// is a new obligation, never a reopened one.
	if c := e.completions[taskID]; c.bindsCycle(taskID, attempt.ID, cycle, open) && c.open() {
		return c.clone()
	}
	c := newCycleCompletion(taskID, attempt.ID, cycle, open)
	c.Origin = attempt.binding()
	e.completions[taskID] = c
	return c.clone()
}

// serveCycleCompletion establishes the obligation of the open review an
// implementer invocation is about to serve in cycle. It runs before any runner
// is resolved, so no way that invocation can end leaves the cycle without its
// obligation. It selects no route: whether the cycle stays live is decided by
// the route the workflow takes after the invocation (routeCycle).
func (e *Engine) serveCycleCompletion(taskID string, cycle int) {
	open, ok := e.openReview(taskID)
	if !ok || len(outstandingFindings(open)) == 0 {
		return
	}
	e.establishCycleCompletion(taskID, cycle, open)
}

// routeCycle applies one route to the task's live obligation for cycle, if
// one binds, and reports whether the route counted an incomplete attempt.
func (e *Engine) routeCycle(taskID string, cycle int, route cycleRoute) bool {
	c, ok := e.liveCycleCompletion(taskID)
	if !ok || c.Cycle != cycle {
		return false
	}
	counted, err := c.transition(route)
	if err != nil {
		return false
	}
	e.saveCycleCompletion(c)
	return counted
}

// handOffCycle is the outer implementer handoff's route. A cycle left live by
// an ordinary-error invocation (routeOrdinaryError) is passed to the next
// implementer only when served says another implementer will serve it; that
// handoff counts the invocation when its response obligation is still
// incomplete, and the attempt that spends the allowance is returned typed, so
// no further implementer is selected for the cycle. A cycle on any other route
// -- a provider rebound, a diagnosis, normal progression -- is not touched.
//
// A counted handoff is checkpointed before anything is returned: the exhausted
// one through the exhaustion choke point (exhaustCycle), any other as the live
// obligation the next implementer serves. A checkpoint that cannot be committed
// is returned as the typed persistence failure.
func (e *Engine) handOffCycle(ctx context.Context, tc *taskContext, workspace, taskID string, served bool) error {
	c, ok := e.liveCycleCompletion(taskID)
	if !ok || !served || !c.Continuing || c.Route != routeOrdinaryError {
		return nil
	}
	counted, err := c.transition(routeErrorHandoff)
	if err != nil {
		return err
	}
	e.saveCycleCompletion(c)
	if !counted {
		return nil
	}
	e.reportIncompleteAttempt(taskID, c, map[string]any{"route": string(c.Route)})
	if c.Exhausted() {
		return e.exhaustCycle(ctx, tc, workspace, c, c.OpenOperations, "")
	}
	return e.checkpointCycle(ctx, tc, workspace, c, session.CheckpointLive, "")
}

// saveCycleCompletion stores c as its task's live obligation, unless another
// obligation replaced it meanwhile.
func (e *Engine) saveCycleCompletion(c *cycleCompletion) {
	e.mu.Lock()
	defer e.mu.Unlock()
	held := e.completions[c.TaskID]
	if held == nil || held.PlanAttemptID != c.PlanAttemptID || held.ReviewAttempt != c.ReviewAttempt || held.Cycle != c.Cycle {
		return
	}
	e.completions[c.TaskID] = c.clone()
}

// liveCycleCompletion is the task's obligation if it still binds the operative
// PlanAttempt and the open review. A rebound invocation reads it here.
func (e *Engine) liveCycleCompletion(taskID string) (*cycleCompletion, bool) {
	open, ok := e.openReview(taskID)
	if !ok {
		return nil, false
	}
	attempt := e.operativePlanAttempt(taskID).ID
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.completions[taskID]
	if !c.binds(taskID, attempt, open) {
		return nil, false
	}
	return c.clone(), true
}

// continueCycleCompletion carries the obligation across one governed
// PlanAttempt transition made INSIDE its cycle, from the attempt it was bound
// to. It is the only path that rebinds an obligation, and it is explicit: the
// obligation must be bound to from, and the open review must still owe exactly
// the obligation's findings by their full identity. Otherwise the obligation is
// dropped, and nothing retained under it satisfies the new attempt.
func (e *Engine) continueCycleCompletion(taskID, from string) bool {
	open, ok := e.openReview(taskID)
	to := e.operativePlanAttempt(taskID)
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.completions[taskID]
	if c == nil {
		return false
	}
	if !ok || !c.binds(taskID, from, open) || c.continueTo(to.binding()) != nil {
		delete(e.completions, taskID)
		return false
	}
	return true
}

// continueTo carries the obligation from its PlanAttempt to to, and records
// the transition: the one rebinding rule, for the live obligation and for its
// replay alike. A continuation the cycle's sequencing does not admit is
// refused, and nothing changes.
func (c *cycleCompletion) continueTo(to planAttemptBinding) error {
	if err := c.admit(replayStep{Kind: stepContinue, To: &to}); err != nil {
		return err
	}
	if to.Attempt.ID != c.PlanAttemptID {
		c.Continuations = append(c.Continuations, c.PlanAttemptID+"->"+to.Attempt.ID)
		c.PlanAttemptID = to.Attempt.ID
	}
	c.Steps = append(c.Steps, replayStep{Kind: stepContinue, To: &to})
	return nil
}

// THE DURABLE CHECKPOINT IS A REPLAY CAPSULE (70B1, RULING-153).
//
// A checkpoint stores the canonical INPUTS the landed owners consumed -- the
// open review's findings, the operative PlanAttempt and the objective its
// identity was derived from, each settled invocation's observation, the
// measured moved files, the executed validation and the identities of the
// evidence read back for it, every selected route and PlanAttempt
// continuation, and the 70A4 candidate measurement -- and nothing those owners
// derive. It holds no "valid", no owed set, no lifecycle verdict and no
// continuation verdict. It is valid only when replaying those inputs through
// the same owners (newCycleCompletion, responsesFor, accountForFindings,
// absorb, transition, continueTo, settleInvocation, the Objective-64 identity
// and observedCandidate) reconstructs exactly one obligation whose canonical
// digest is the ReplayDigest it was committed with. There is no field-by-field
// substitute for that test, and none is written here.

// replayStepKind is the closed kind of one recorded transition.
type replayStepKind string

const (
	// stepAccount: one settled, returned invocation reconciled into the cycle.
	stepAccount replayStepKind = "account"
	// stepRoute: one route the workflow selected (transition).
	stepRoute replayStepKind = "route"
	// stepContinue: one explicit PlanAttempt continuation (continueTo).
	stepContinue replayStepKind = "continue"
)

// judgingSet names which findings an account step judged.
type judgingSet string

const (
	// judgeAll: every finding of the cycle, beside the retained responses --
	// the account of a returned invocation whose candidate was validated.
	judgeAll judgingSet = "all"
	// judgeUnsettled: only what an error-bearing return could re-judge
	// (cycleCompletion.judging).
	judgeUnsettled judgingSet = "unsettled"
)

// replayStep is the canonical input of one transition, in the order the live
// obligation took it.
type replayStep struct {
	Kind replayStepKind `json:"kind"`
	// Account inputs.
	Judging judgingSet    `json:"judging,omitempty"`
	Settled *settledInput `json:"settled,omitempty"`
	Moved   *movedFacts   `json:"moved,omitempty"`
	// Evidence is the executed validation the account read, and ReadBack
	// the durable identity (taskstate.RetainedEvidence.Key) of each record
	// read back for an evidence finding, retained under EvidenceCandidate.
	// Replay loads those records again; a key is never itself the answer.
	Evidence          *validation.Bundle           `json:"evidence,omitempty"`
	EvidenceCandidate *taskstate.CandidateIdentity `json:"evidence_candidate,omitempty"`
	ReadBack          map[string]string            `json:"read_back,omitempty"`
	// Route input.
	Route cycleRoute `json:"route,omitempty"`
	// Continuation input.
	To *planAttemptBinding `json:"to,omitempty"`
}

// settledInput is what 70A2 settled one invocation from: the provider, the
// cycle and the adapter's observation. Replay settles it again
// (settleInvocation); the settled operations are never stored.
type settledInput struct {
	Provider   string            `json:"provider"`
	Cycle      int               `json:"cycle"`
	Invocation *agent.Invocation `json:"invocation,omitempty"`
}

// movedFacts is the canonical file-by-file change measurement an account read
// (movedPathsSince), never a response's own paths. Measured=false is the
// measurement that could not be made, which binds no change at all.
type movedFacts struct {
	Measured bool     `json:"measured"`
	Paths    []string `json:"paths,omitempty"`
}

func movedFactsOf(moved map[string]bool) *movedFacts {
	if moved == nil {
		return &movedFacts{}
	}
	f := &movedFacts{Measured: true}
	for p, ok := range moved {
		if ok {
			f.Paths = append(f.Paths, p)
		}
	}
	sort.Strings(f.Paths)
	return f
}

func (f movedFacts) moved() map[string]bool {
	if !f.Measured {
		return nil
	}
	out := make(map[string]bool, len(f.Paths))
	for _, p := range f.Paths {
		out[p] = true
	}
	return out
}

// recordAccount records the inputs of one account the live obligation is about
// to absorb. readBack is what retainFindingEvidence read back. An account the
// cycle's sequencing does not admit is refused: it must not be absorbed.
func (c *cycleCompletion) recordAccount(judging judgingSet, settled settledInvocation, moved map[string]bool,
	evidence validation.Bundle, evidenceCandidate taskstate.CandidateIdentity, readBack map[string]string) error {
	step := replayStep{Kind: stepAccount, Judging: judging, Moved: movedFactsOf(moved),
		Settled: &settledInput{Provider: settled.Provider, Cycle: settled.Cycle, Invocation: settled.observation()}}
	if len(evidence.Checks) != 0 || evidence.DiffDigest != "" || evidence.CandidateID != "" {
		b := evidence
		step.Evidence = &b
	}
	if len(readBack) != 0 {
		cand := evidenceCandidate
		step.EvidenceCandidate = &cand
		step.ReadBack = make(map[string]string, len(readBack))
		for k, v := range readBack {
			step.ReadBack[k] = v
		}
	}
	if err := c.admit(step); err != nil {
		return err
	}
	c.Steps = append(c.Steps, step)
	return nil
}

// valid reports membership in the closed route vocabulary.
func (r cycleRoute) valid() bool {
	switch r {
	case routeIncompleteRetry, routeProviderRebound, routeOrdinaryError, routeErrorHandoff,
		routeProgress, routeDiagnosis, routeDisputeEscalation:
		return true
	}
	return false
}

// durableStatus is the durable status this owner gives its own state: an
// obligation whose allowance is spent is exhausted; one kept live for the next
// provider after a provider proved it could not serve is blocked; any other
// live one is live. Anything else has no durable status but retired.
func (c *cycleCompletion) durableStatus() (session.CheckpointStatus, bool) {
	switch {
	case c.Attempts > maxIncompleteImplementerAttempts:
		return "", false
	case c.Exhausted():
		return session.CheckpointExhausted, true
	case c.Continuing && c.Route == routeProviderRebound:
		return session.CheckpointBlocked, true
	case c.Continuing:
		return session.CheckpointLive, true
	}
	return "", false
}

// replayInputs is a checkpoint's opaque Inputs, as this owner reads them.
type replayInputs struct {
	Cycle     int                `json:"review_cycle"`
	Review    replayReview       `json:"review"`
	Origin    planAttemptBinding `json:"plan_attempt"`
	Steps     []replayStep       `json:"steps"`
	Candidate candidateInput     `json:"candidate"`
}

// replayReview is the open review the obligation answers: its attempt, the
// candidate it was raised on, and its findings by full identity.
type replayReview struct {
	Attempt         int             `json:"review_attempt"`
	CandidateDigest string          `json:"candidate_digest"`
	CandidateTree   string          `json:"candidate_tree,omitempty"`
	Findings        []roles.Finding `json:"findings"`
}

// capsule is c's canonical replay inputs, with cand as its candidate input.
func (c *cycleCompletion) capsule(cand candidateInput) replayInputs {
	return replayInputs{
		Cycle: c.Cycle,
		Review: replayReview{Attempt: c.ReviewAttempt, CandidateDigest: c.CandidateDigest, CandidateTree: c.CandidateTree,
			Findings: append([]roles.Finding(nil), c.Findings...)},
		Origin: c.Origin, Steps: append([]replayStep(nil), c.Steps...), Candidate: cand,
	}
}

// replayedObligation is the canonical reconstructed obligation a ReplayDigest
// is taken of. A retired checkpoint replays to a tombstone: no completion.
type replayedObligation struct {
	TaskID          string                   `json:"task_id"`
	PlanAttemptID   string                   `json:"plan_attempt_id"`
	Status          session.CheckpointStatus `json:"status"`
	Retirement      session.RetirementReason `json:"retirement,omitempty"`
	Cycle           int                      `json:"review_cycle"`
	ReviewAttempt   int                      `json:"review_attempt"`
	CandidateDigest string                   `json:"candidate_digest"`
	CandidateTree   string                   `json:"candidate_tree,omitempty"`
	Completion      *completionState         `json:"completion,omitempty"`
	Candidate       candidateRecord          `json:"candidate"`
}

// completionState is the live state of a non-retired obligation.
type completionState struct {
	Findings       []roles.Finding            `json:"findings"`
	Retained       map[string]findingResponse `json:"retained"`
	Why            map[string]string          `json:"why"`
	OpenOperations []string                   `json:"open_operations"`
	Attempts       int                        `json:"attempts"`
	Route          cycleRoute                 `json:"route"`
	Continuing     bool                       `json:"continuing"`
	Continuations  []string                   `json:"continuations"`
}

// obligation is c's canonical form under status, with cand its candidate.
func (c *cycleCompletion) obligation(status session.CheckpointStatus, reason session.RetirementReason, cand candidateRecord) replayedObligation {
	o := replayedObligation{
		TaskID: c.TaskID, PlanAttemptID: c.PlanAttemptID, Status: status, Retirement: reason,
		Cycle: c.Cycle, ReviewAttempt: c.ReviewAttempt, CandidateDigest: c.CandidateDigest, CandidateTree: c.CandidateTree,
		Candidate: cand,
	}
	if status != session.CheckpointRetired {
		o.Completion = &completionState{
			Findings: append([]roles.Finding{}, c.Findings...), Retained: map[string]findingResponse{}, Why: map[string]string{},
			OpenOperations: append([]string{}, c.OpenOperations...), Attempts: c.Attempts, Route: c.Route,
			Continuing: c.Continuing, Continuations: append([]string{}, c.Continuations...),
		}
		for k, v := range c.Retained {
			o.Completion.Retained[k] = v
		}
		for k, v := range c.Why {
			o.Completion.Why[k] = v
		}
	}
	return o
}

// digest is the ReplayDigest of o: sha256 of its canonical encoding.
func (o replayedObligation) digest() (string, error) {
	raw, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// retainedEvidenceReader loads the evidence durable task state holds for one
// candidate of a task.
type retainedEvidenceReader func(taskID string, cand taskstate.CandidateIdentity) ([]taskstate.RetainedEvidence, error)

// replaySources are the durable records a replay reads its owners' authority
// from, never from the checkpoint itself: the session record, read
// authoritatively, which holds every PlanAttempt start and operative
// transition; and the task's retained evidence.
type replaySources struct {
	Record   []event.Event
	Evidence retainedEvidenceReader
}

// replayed is one checkpoint as replay reconstructed it.
type replayed struct {
	Obligation replayedObligation
	Digest     string
	// Completion is the reconstructed live obligation; nil for a tombstone,
	// which can never produce one.
	Completion *cycleCompletion
}

// Replay reconstructs a checkpoint through the landed owners and returns the
// one obligation its inputs establish, with its ReplayDigest. It refuses
// inputs any owner refuses, an obligation not owned by the checkpoint's task
// and named PlanAttempt, and a status the reconstructed obligation does not
// have. Whether the result is the checkpoint's committed state is decided by
// comparing Digest with the committed ReplayDigest, and by nothing else.
func Replay(cp session.Checkpoint, durable replaySources) (replayed, error) {
	refuse := func(format string, args ...any) (replayed, error) {
		return replayed{}, fmt.Errorf("checkpoint %s does not replay: "+format, append([]any{short12(cp.CheckpointID)}, args...)...)
	}
	var in replayInputs
	if err := session.DecodeExactlyOne(cp.Inputs, &in); err != nil {
		return refuse("its inputs are malformed: %v", err)
	}
	// The obligation's PlanAttempts -- its origin and every continuation --
	// are established by the Objective-64 owner from the durable record of
	// their starts and operative transitions, before anything is rebound.
	chain := []planAttemptBinding{in.Origin}
	for i, step := range in.Steps {
		if step.Kind != stepContinue {
			continue
		}
		if step.To == nil {
			return refuse("continuation step %d names no plan attempt", i+1)
		}
		chain = append(chain, *step.To)
	}
	superseded := cp.Status == session.CheckpointRetired && cp.Retirement == session.RetiredSupersededPlanAttempt
	if err := verifyPlanAttemptTransitions(durable.Record, cp.TaskID, chain, superseded); err != nil {
		return refuse("its plan attempts are not established by their durable transitions: %v", err)
	}
	c := newCycleCompletion(cp.TaskID, in.Origin.Attempt.ID, in.Cycle, openReview{Attempt: in.Review.Attempt,
		CandidateDigest: in.Review.CandidateDigest, CandidateTree: in.Review.CandidateTree, Findings: in.Review.Findings})
	c.Origin = in.Origin
	if len(c.Findings) == 0 || len(c.Findings) != len(in.Review.Findings) {
		return refuse("its review owes no finding, or names a finding no obligation is owed for")
	}
	for i, step := range in.Steps {
		switch step.Kind {
		case stepAccount:
			if err := c.admit(step); err != nil {
				return refuse("account step %d is not one the cycle's owner admits: %v", i+1, err)
			}
			if step.Settled == nil || step.Moved == nil {
				return refuse("account step %d carries no settled invocation or no change measurement", i+1)
			}
			settled := settleInvocation(step.Settled.Provider, step.Settled.Cycle, agent.Result{Invocation: step.Settled.Invocation}, nil)
			if !settled.Returned || settled.Cycle != c.Cycle {
				return refuse("account step %d reconciles an invocation 70A2 did not settle as returned in cycle %d", i+1, c.Cycle)
			}
			fresh, _ := parseFindingResponses(strings.TrimSpace(settled.Report))
			responses, _, conflicted := c.responsesFor(fresh)
			var judging []roles.Finding
			if step.Judging == judgeAll {
				judging = c.Findings
			} else {
				judging = c.judging(conflicted)
			}
			var bundle validation.Bundle
			if step.Evidence != nil {
				bundle = *step.Evidence
			}
			// Each read-back identity is resolved to its durable record,
			// which must be exactly the record the executed check builds
			// for that finding: a key alone answers nothing.
			readBack := map[string]string{}
			if len(step.ReadBack) != 0 {
				if step.EvidenceCandidate == nil {
					return refuse("account step %d names retained evidence on no candidate", i+1)
				}
				if durable.Evidence == nil {
					return refuse("account step %d names retained evidence and no durable task state can be read", i+1)
				}
				held, err := durable.Evidence(cp.TaskID, *step.EvidenceCandidate)
				if err != nil {
					return refuse("the retained evidence of account step %d could not be loaded: %v", i+1, err)
				}
				wanted := map[string]taskstate.RetainedEvidence{}
				for _, rec := range retainedRecords(*step.EvidenceCandidate, judging, responses, bundle, step.Settled.Cycle) {
					if step.ReadBack[rec.FindingID] == rec.Key() {
						wanted[rec.FindingID] = rec
					}
				}
				readBack = readBackRetained(held, wanted)
			}
			account := accountForFindings(judging, responses, step.Moved.moved(), bundle, readBack)
			c.absorb(judging, account, responses, settled)
			c.Steps = append(c.Steps, step)
		case stepRoute:
			if _, err := c.transition(step.Route); err != nil {
				return refuse("route step %d is not one the cycle's owner admits: %v", i+1, err)
			}
		case stepContinue:
			if err := c.continueTo(*step.To); err != nil {
				return refuse("continuation step %d is not one the cycle's owner admits: %v", i+1, err)
			}
		default:
			return refuse("step %d is a %q step, which is not a transition", i+1, step.Kind)
		}
	}
	if c.PlanAttemptID != cp.PlanAttemptID {
		return refuse("its inputs establish an obligation owned by plan attempt %s, not %s",
			orNone(short12(c.PlanAttemptID), "none"), orNone(short12(cp.PlanAttemptID), "none"))
	}
	candidate, err := observedCandidate(in.Candidate)
	if err != nil {
		return refuse("%v", err)
	}
	out := replayed{Completion: c}
	if cp.Status == session.CheckpointRetired {
		if err := c.retires(cp.Retirement); err != nil {
			return refuse("%v", err)
		}
		out.Completion = nil
	} else if status, ok := c.durableStatus(); !ok || status != cp.Status {
		return refuse("its inputs reconstruct a %s obligation, not a %s one", orNone(string(status), "non-durable"), cp.Status)
	}
	out.Obligation = c.obligation(cp.Status, cp.Retirement, candidate.record())
	if out.Digest, err = out.Obligation.digest(); err != nil {
		return refuse("its reconstructed obligation cannot be encoded: %v", err)
	}
	return out, nil
}

// IncompleteObligationPersistenceFailedState is the machine-readable state of
// an obligation no checkpoint could be committed for.
const IncompleteObligationPersistenceFailedState = "incomplete_obligation_persistence_failed"

// PriorCheckpoint is what is known about the committed checkpoint before a
// failed one: KNOWN names it, ABSENT is a successful authoritative read that
// found none, and UNKNOWN is a store that could not be read -- never absence.
type PriorCheckpoint string

const (
	PriorCheckpointKnown   PriorCheckpoint = "known"
	PriorCheckpointAbsent  PriorCheckpoint = "absent"
	PriorCheckpointUnknown PriorCheckpoint = "unknown"
)

// ObligationPersistenceFailed is the typed outcome of an obligation whose
// checkpoint could not be committed. The obligation it describes is NOT
// durable and NOT resumable, and nothing was retired or overwritten for it.
type ObligationPersistenceFailed struct {
	State         string                   `json:"state"`
	TaskID        string                   `json:"task_id"`
	PlanAttemptID string                   `json:"plan_attempt_id"`
	Cycle         int                      `json:"review_cycle"`
	Status        session.CheckpointStatus `json:"checkpoint_status"`
	// Attempts are the complete durable write attempts made; 0 when the
	// checkpoint could not even be constructed.
	Attempts int `json:"attempts"`
	// Prior is what is known of the last committed checkpoint, and
	// PriorCheckpointID names it when it is known.
	Prior             PriorCheckpoint `json:"prior_checkpoint"`
	PriorCheckpointID string          `json:"prior_checkpoint_id,omitempty"`
	Cause             string          `json:"cause"`
}

func (f *ObligationPersistenceFailed) Error() string {
	prior := "whether an earlier checkpoint was committed is UNKNOWN: the store could not be read"
	switch f.Prior {
	case PriorCheckpointKnown:
		prior = "the last committed checkpoint " + short12(f.PriorCheckpointID) + " stands unchanged"
	case PriorCheckpointAbsent:
		prior = "no earlier checkpoint was committed"
	}
	return fmt.Sprintf("%s: the %s obligation of review cycle %d (plan attempt %s) could not be checkpointed after %d attempt(s) "+
		"and is not durable or resumable; %s: %s", f.State, f.Status, f.Cycle, orNone(short12(f.PlanAttemptID), "none"),
		f.Attempts, prior, f.Cause)
}

// exhaustCycle is the ONE choke point of IMPLEMENTER_INCOMPLETE: the exhausted
// obligation is checkpointed and committed first, and only then is the typed
// state returned. A checkpoint that cannot be committed -- no store, or every
// attempt failed -- returns the typed persistence failure instead, never the
// exhaustion as though durable state existed.
func (e *Engine) exhaustCycle(ctx context.Context, tc *taskContext, workspace string, c *cycleCompletion, openOperations []string, diagnosis string) error {
	if err := e.checkpointCycle(ctx, tc, workspace, c, session.CheckpointExhausted, ""); err != nil {
		return err
	}
	return c.incomplete(openOperations, diagnosis)
}

// checkpointCycle commits c's checkpoint under status and records it on the
// live obligation.
func (e *Engine) checkpointCycle(ctx context.Context, tc *taskContext, workspace string, c *cycleCompletion,
	status session.CheckpointStatus, reason session.RetirementReason) error {
	id, err := e.commitCheckpoint(c, status, reason, e.measureCandidate(ctx, tc, workspace))
	if err != nil {
		return err
	}
	c.CheckpointID = id
	e.saveCycleCompletion(c)
	return nil
}

// checkpointBlocked commits the blocked checkpoint of cycle's live obligation,
// if one binds, before the provider-unavailable block is returned.
func (e *Engine) checkpointBlocked(ctx context.Context, tc *taskContext, workspace, taskID string, cycle int) error {
	c, ok := e.liveCycleCompletion(taskID)
	if !ok || c.Cycle != cycle {
		return nil
	}
	return e.checkpointCycle(ctx, tc, workspace, c, session.CheckpointBlocked, "")
}

// retireCycle commits the typed tombstone of an obligation this process
// checkpointed, so its committed checkpoint no longer stands as live. An
// obligation never checkpointed has nothing to retire.
func (e *Engine) retireCycle(ctx context.Context, tc *taskContext, workspace string, c *cycleCompletion, reason session.RetirementReason) error {
	if c == nil || c.CheckpointID == "" {
		return nil
	}
	return e.checkpointCycle(ctx, tc, workspace, c, session.CheckpointRetired, reason)
}

// errCheckpointUnconstructible marks a checkpoint the owners refused to
// construct: it is not a persistence failure a retry could repair.
var errCheckpointUnconstructible = errors.New("the checkpoint could not be constructed")

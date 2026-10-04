package workflow

import (
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/roles"
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
// It is in memory only. Restoring it in a fresh process is not this record's job.

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
// may be selected for it.
func (c *cycleCompletion) transition(route cycleRoute) (counted bool) {
	c.Route = route
	switch route {
	case routeIncompleteRetry, routeErrorHandoff:
		if route == routeErrorHandoff && !c.incompleteResponse() {
			c.Continuing = true
			return false
		}
		c.Attempts++
		c.Continuing = !c.Exhausted()
		return true
	case routeProviderRebound, routeOrdinaryError:
		c.Continuing = true
	default:
		c.Continuing = false
	}
	return false
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
	attempt := e.operativePlanAttempt(taskID).ID
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.completions == nil {
		e.completions = map[string]*cycleCompletion{}
	}
	if c := e.completions[taskID]; c.bindsCycle(taskID, attempt, cycle, open) {
		return c.clone()
	}
	c := newCycleCompletion(taskID, attempt, cycle, open)
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
	counted := c.transition(route)
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
func (e *Engine) handOffCycle(taskID string, served bool) *ImplementerIncomplete {
	c, ok := e.liveCycleCompletion(taskID)
	if !ok || !served || !c.Continuing || c.Route != routeOrdinaryError {
		return nil
	}
	counted := c.transition(routeErrorHandoff)
	e.saveCycleCompletion(c)
	if !counted {
		return nil
	}
	e.reportIncompleteAttempt(taskID, c, map[string]any{"route": string(c.Route)})
	if c.Exhausted() {
		return c.incomplete(c.OpenOperations, "")
	}
	return nil
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
	to := e.operativePlanAttempt(taskID).ID
	e.mu.Lock()
	defer e.mu.Unlock()
	c := e.completions[taskID]
	if c == nil {
		return false
	}
	if !ok || !c.binds(taskID, from, open) {
		delete(e.completions, taskID)
		return false
	}
	if to != from {
		c.Continuations = append(c.Continuations, from+"->"+to)
		c.PlanAttemptID = to
	}
	return true
}

package workflow

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/taskstate"
	"github.com/globulario/sensei-code/internal/validation"
)

// cycleStep is one call into a landed cycle owner. A replay journal records
// owner inputs, not hand-computed conclusions.
type cycleStep struct {
	Account     *accountInputs `json:"account,omitempty"`
	Route       cycleRoute     `json:"route,omitempty"`
	ContinuedTo string         `json:"continued_to,omitempty"`
}

// obligationInputs are the canonical inputs needed to reconstruct one
// cycleCompletion through the same owners that built it live.
type obligationInputs struct {
	EstablishedUnder string      `json:"established_under"`
	Cycle            int         `json:"review_cycle"`
	Review           openReview  `json:"review"`
	Steps            []cycleStep `json:"steps"`
}

// accountInputs are the canonical inputs consumed when 70A1 accounted one
// returned invocation. Evidence is referenced by durable task-state identity;
// its outcome is never copied here as authority.
type accountInputs struct {
	Responses     []findingResponse `json:"responses"`
	Base          string            `json:"base"`
	CandidateTree string            `json:"candidate_tree,omitempty"`
	Evidence      *evidenceInputs   `json:"evidence,omitempty"`
	Settled       settledInputs     `json:"settled"`
}

// evidenceInputs name the durable retained-evidence records that the live
// account read back for one candidate.
type evidenceInputs struct {
	Candidate taskstate.CandidateIdentity `json:"candidate"`
	Records   map[string]string           `json:"records,omitempty"`
}

// settledInputs are the exact adapter observation plus direct invocation error
// from which 70A2 produced settledInvocation. The error is an input, never a
// derived "errored" flag.
type settledInputs struct {
	Provider        string                       `json:"provider"`
	Cycle           int                          `json:"cycle"`
	Transport       agent.Transport              `json:"transport"`
	TransportFailed bool                         `json:"transport_failed,omitempty"`
	Returned        bool                         `json:"returned"`
	Lifecycle       []agent.LifecycleObservation `json:"lifecycle,omitempty"`
	Error           *string                      `json:"error,omitempty"`
}

func settledInputsOf(s settledInvocation) settledInputs {
	in := settledInputs{
		Provider: s.Provider, Cycle: s.Cycle, Transport: s.Transport,
		TransportFailed: s.TransportFailed, Returned: s.Returned,
		Lifecycle: append([]agent.LifecycleObservation(nil), s.Lifecycle...),
	}
	if s.Err != nil {
		text := s.Err.Error()
		in.Error = &text
	}
	return in
}

// settle reconstructs the 70A2 fact by calling the same owner.
func (in settledInputs) settle() settledInvocation {
	var err error
	if in.Error != nil {
		err = errors.New(*in.Error)
	}
	return settleInvocation(in.Provider, in.Cycle, agent.Result{Invocation: &agent.Invocation{
		Transport: in.Transport, TransportFailed: in.TransportFailed,
		Returned: in.Returned, Lifecycle: append([]agent.LifecycleObservation(nil), in.Lifecycle...),
	}}, err)
}

func cloneAccountInputs(in accountInputs) accountInputs {
	out := in
	out.Responses = append([]findingResponse(nil), in.Responses...)
	for i := range out.Responses {
		out.Responses[i].Paths = append([]string(nil), in.Responses[i].Paths...)
	}
	out.Settled.Lifecycle = append([]agent.LifecycleObservation(nil), in.Settled.Lifecycle...)
	if in.Settled.Error != nil {
		text := *in.Settled.Error
		out.Settled.Error = &text
	}
	if in.Evidence != nil {
		e := *in.Evidence
		e.Records = make(map[string]string, len(in.Evidence.Records))
		for k, v := range in.Evidence.Records {
			e.Records[k] = v
		}
		out.Evidence = &e
	}
	return out
}

func cloneCycleSteps(in []cycleStep) []cycleStep {
	out := make([]cycleStep, len(in))
	for i, step := range in {
		out[i] = step
		if step.Account != nil {
			a := cloneAccountInputs(*step.Account)
			out[i].Account = &a
		}
	}
	return out
}

// judgedBy centralizes which finding set the live and replay paths give 70A1.
// A direct invocation error is the 70A2 fact that narrows re-judgment to the
// still-relevant set. Nothing derives that branch from prose or a copied bool.
func (c *cycleCompletion) judgedBy(settled settledInvocation, conflicted []string) []roles.Finding {
	if settled.Err != nil {
		return c.judging(conflicted)
	}
	return c.Findings
}

// rebind is the sole cycle owner for explicit PlanAttempt continuation.
func (c *cycleCompletion) rebind(to string) {
	c.Journal = append(c.Journal, cycleStep{ContinuedTo: to})
	c.Continuations = append(c.Continuations, c.PlanAttemptID+"->"+to)
	c.PlanAttemptID = to
}

func (r cycleRoute) valid() bool {
	switch r {
	case routeIncompleteRetry, routeProviderRebound, routeOrdinaryError, routeErrorHandoff,
		routeProgress, routeDiagnosis, routeDisputeEscalation:
		return true
	}
	return false
}

// planAttemptTransitions is the durable Objective-64 transition sequence after
// every PlanProposed has been verified through verifyPlanAttempt.
type planAttemptTransitions []verifiedTransition

type verifiedTransition struct {
	ID  string
	Err error
}

func (e *Engine) planAttemptTransitions(taskID, objective string) (planAttemptTransitions, error) {
	if e.Store == nil {
		return nil, session.ErrNoStore
	}
	events, err := e.Store.Load()
	if err != nil {
		return nil, err
	}
	world := e.governedBase(taskID)
	var out planAttemptTransitions
	for _, tr := range session.PlanTransitions(events, taskID) {
		a, err := verifyPlanAttempt(taskID, objective, world, tr)
		out = append(out, verifiedTransition{ID: a.ID, Err: err})
	}
	return out, nil
}

func (t planAttemptTransitions) madeOperative(attempt string) bool {
	for _, tr := range t {
		if tr.Err == nil && tr.ID == attempt {
			return true
		}
	}
	return false
}

func (t planAttemptTransitions) continues(from, to string) bool {
	if strings.TrimSpace(from) == "" || strings.TrimSpace(to) == "" || from == to {
		return false
	}
	for i := 0; i+1 < len(t); i++ {
		if t[i].Err == nil && t[i+1].Err == nil && t[i].ID == from && t[i+1].ID == to {
			return true
		}
	}
	return false
}

func (t planAttemptTransitions) operative() (string, error) {
	if len(t) == 0 {
		return "", errors.New("no durable transition made any plan attempt operative")
	}
	last := t[len(t)-1]
	if last.Err != nil {
		return "", last.Err
	}
	return last.ID, nil
}

// retainedExecution reconstructs exactly the broker execution facts that
// accountForFindings consumes from one durable retained-evidence record.
func retainedExecution(r taskstate.RetainedEvidence) validation.Evidence {
	c := validation.Evidence{
		Command: r.Command, ExitStatus: r.ExitStatus, Attribution: r.Attribution,
		Output: r.Output, OutputDigest: r.OutputDigest, ExecutedBy: r.Producer,
		DiffDigest: r.Candidate.DiffDigest,
	}
	switch r.Outcome {
	case taskstate.EvidencePassed:
		c.Outcome = validation.Passed
	case taskstate.EvidenceFailed:
		c.Outcome = validation.Failed
	}
	return c
}

// readBackEvidence resolves only the canonical durable evidence keys named by
// the replay input. Missing or mismatched records answer nothing.
func (e *Engine) readBackEvidence(taskID string, in *evidenceInputs) (validation.Bundle, map[string]string, error) {
	readBack := map[string]string{}
	if in == nil || len(in.Records) == 0 {
		return validation.Bundle{}, readBack, nil
	}
	held, err := e.retainedEvidence(taskID, in.Candidate)
	if err != nil {
		return validation.Bundle{}, nil, fmt.Errorf("the retained evidence cannot be read: %w", err)
	}
	bundle := validation.Bundle{DiffDigest: in.Candidate.DiffDigest}
	for _, rec := range held {
		if key, ok := in.Records[rec.FindingID]; ok && rec.Key() == key && rec.FindingClass == string(roles.EvidenceFinding) {
			readBack[rec.FindingID] = key
			bundle.Checks = append(bundle.Checks, retainedExecution(rec))
		}
	}
	return bundle, readBack, nil
}

// replayAccount routes one recorded account through 70A2, 70A1 and the 70A3
// absorption owner. It never trusts the journal to state what was discharged.
func (e *Engine) replayAccount(ctx context.Context, c *cycleCompletion, in accountInputs) error {
	settled := in.Settled.settle()
	responses, _, conflicted := c.responsesFor(in.Responses)
	judging := c.judgedBy(settled, conflicted)

	var moved map[string]bool
	if strings.TrimSpace(in.CandidateTree) != "" {
		diff, err := e.Repo.RenderCandidateDiff(ctx, in.Base, in.CandidateTree)
		if err != nil {
			return fmt.Errorf("render candidate tree %s against %s: %w", in.CandidateTree, in.Base, err)
		}
		moved, err = movedPathsSince(ctx, e.Repo, in.Base, c.CandidateTree, diff)
		if err != nil {
			return err
		}
	}
	bundle, readBack, err := e.readBackEvidence(c.TaskID, in.Evidence)
	if err != nil {
		return err
	}
	account := accountForFindings(judging, responses, moved, bundle, readBack)
	c.absorb(judging, account, responses, settled, in)
	return nil
}

// replayCycle reconstructs one cycle through the landed owners. Objective-64
// verifies every PlanAttempt transition; 70A2 settles invocation inputs; 70A1
// re-accounts responses; and 70A3 performs every state transition.
func (e *Engine) replayCycle(ctx context.Context, taskID, objective string, in obligationInputs) (*cycleCompletion, error) {
	if strings.TrimSpace(taskID) == "" || strings.TrimSpace(objective) == "" || strings.TrimSpace(in.EstablishedUnder) == "" {
		return nil, errors.New("replay cycle is missing task, objective, or established PlanAttempt")
	}
	plans, err := e.planAttemptTransitions(taskID, objective)
	if err != nil {
		return nil, err
	}
	if !plans.madeOperative(in.EstablishedUnder) {
		return nil, fmt.Errorf("PlanAttempt %s was not made operative by a verified durable transition", short12(in.EstablishedUnder))
	}

	c := newCycleCompletion(taskID, in.EstablishedUnder, in.Cycle, in.Review)
	if len(c.Findings) == 0 {
		return nil, errors.New("the review owes no finding, so it establishes no incomplete obligation")
	}
	for i, step := range in.Steps {
		if c.Exhausted() {
			return nil, fmt.Errorf("step %d follows cycle exhaustion", i)
		}
		switch {
		case step.Account != nil && step.Route == "" && step.ContinuedTo == "":
			if err := e.replayAccount(ctx, c, cloneAccountInputs(*step.Account)); err != nil {
				return nil, fmt.Errorf("step %d account: %w", i, err)
			}
		case step.Account == nil && step.Route != "" && step.ContinuedTo == "":
			if !step.Route.valid() {
				return nil, fmt.Errorf("step %d names unknown cycle route %q", i, step.Route)
			}
			c.transition(step.Route)
		case step.Account == nil && step.Route == "" && step.ContinuedTo != "":
			if !plans.continues(c.PlanAttemptID, step.ContinuedTo) {
				return nil, fmt.Errorf("step %d does not name a verified PlanAttempt continuation from %s to %s", i, short12(c.PlanAttemptID), short12(step.ContinuedTo))
			}
			c.rebind(step.ContinuedTo)
		default:
			return nil, fmt.Errorf("step %d is not exactly one owner call", i)
		}
	}
	operative, err := plans.operative()
	if err != nil {
		return nil, fmt.Errorf("newest PlanAttempt transition is not operative: %w", err)
	}
	if c.PlanAttemptID != operative {
		return nil, fmt.Errorf("replayed cycle is bound to %s, but durable Objective-64 state is %s", short12(c.PlanAttemptID), short12(operative))
	}
	return c, nil
}

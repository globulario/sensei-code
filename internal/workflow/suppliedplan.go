package workflow

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// PlanSource says who authored the bound a candidate is judged against.
//
// It is stamped by the engine from the entrypoint that submitted the plan, and
// there is no field a plan can set to choose its own. A plan that could label
// itself architect-produced would be the same forgeable claim that
// Objective.Provenance refuses to take from a flag.
type PlanSource string

const (
	// PlanByArchitect is a plan the configured architect produced in this run.
	PlanByArchitect PlanSource = "architect"
	// PlanSupplied is a plan handed in from outside the run, byte for byte,
	// through `run --plan`. Nobody in the run authored it; the run only judged
	// it. Every gate downstream reads it exactly as it reads an architect's.
	PlanSupplied PlanSource = "supplied"
)

// SuppliedPlan is an externally authored architectureDecision, validated and
// digested at the entrypoint so what was submitted is fixed before anything
// routes it.
//
// The decision type stays unexported: the only way to obtain one of these is
// ParseSuppliedPlan, so a caller cannot assemble a plan the validation never
// saw.
type SuppliedPlan struct {
	decision architectureDecision
	// Digest is sha256 over the exact bytes submitted. It is the plan's
	// identity in every receipt, so a later reader can check that the bound
	// the reviewer saw is the bound the file held.
	Digest string
}

// ParseSuppliedPlan validates raw JSON as an externally supplied plan.
//
// It fails closed on everything the architect loop would tolerate or repair by
// re-prompting, because there is no author here to re-prompt:
//
//   - unknown fields: a plan asserting plan_source, provenance, authority or
//     any other key this type does not define is refused rather than ignored.
//     Ignoring is how a self-provenance claim would travel silently and how a
//     typo'd field name would drop a bound on the floor.
//   - decision other than "proceed": reply and escalate are conversation with
//     an architect, and a supplied plan has none.
//   - an empty plan, or an invalid mode.
func ParseSuppliedPlan(raw []byte) (SuppliedPlan, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return SuppliedPlan{}, errors.New("supplied plan is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var d architectureDecision
	if err := dec.Decode(&d); err != nil {
		return SuppliedPlan{}, fmt.Errorf("supplied plan is not a valid bounded plan: %w", err)
	}
	if dec.More() {
		return SuppliedPlan{}, errors.New("supplied plan must be exactly one JSON object")
	}
	d.Decision = strings.ToLower(strings.TrimSpace(d.Decision))
	if d.Decision != "proceed" {
		return SuppliedPlan{}, fmt.Errorf("supplied plan decision must be \"proceed\", got %q; a supplied plan cannot reply or escalate because no architect authored it", d.Decision)
	}
	if strings.TrimSpace(d.Plan) == "" {
		return SuppliedPlan{}, errors.New("supplied plan has no plan text")
	}
	if m := strings.TrimSpace(d.Mode); m != "" && !strings.EqualFold(m, ModeInspect) && !strings.EqualFold(m, ModeModify) {
		return SuppliedPlan{}, fmt.Errorf("supplied plan mode must be %q or %q, got %q", ModeInspect, ModeModify, d.Mode)
	}
	if strings.TrimSpace(d.HumanQuestion) != "" || len(d.Options) != 0 {
		return SuppliedPlan{}, errors.New("supplied plan carries a human question; questions to the human are raised by the authority router, not by the plan")
	}
	sum := sha256.Sum256(raw)
	return SuppliedPlan{decision: d, Digest: hex.EncodeToString(sum[:])}, nil
}

// errSuppliedPlanCannotBeRevised is returned wherever the workflow would
// otherwise hand the plan back to the architect for a revision.
//
// A supplied plan is the whole bound. An architect revising it would make the
// bound the reviewer judges against a mixture of two authors under one label,
// and the label is what the receipt carries. So the run ends here, with the
// condition that needed a revision recorded, and whoever supplied the plan
// supplies the next one.
func errSuppliedPlanCannotBeRevised(why string) error {
	return fmt.Errorf("the supplied plan needs a revision the run cannot make: %s. A supplied plan is not revised by the architect; supply a revised plan", why)
}

// supplyPlan records a supplied plan for a task before its run starts.
func (e *Engine) supplyPlan(taskID string, p SuppliedPlan) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.supplied == nil {
		e.supplied = make(map[string]SuppliedPlan)
	}
	e.supplied[taskID] = p
}

// suppliedPlan returns the plan handed in for a task, if one was.
func (e *Engine) suppliedPlan(taskID string) (SuppliedPlan, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	p, ok := e.supplied[taskID]
	return p, ok
}

// planSource is who authored the bound for this task.
func (e *Engine) planSource(taskID string) PlanSource {
	if _, ok := e.suppliedPlan(taskID); ok {
		return PlanSupplied
	}
	return PlanByArchitect
}

// planDigest is the supplied plan's digest, or "" for an architect's plan.
func (e *Engine) planDigest(taskID string) string {
	p, _ := e.suppliedPlan(taskID)
	return p.Digest
}

// errSuppliedPlanContextUnavailable is the resume of a supplied-plan task whose
// exact bound cannot be re-established from the session record.
//
// The alternative -- resuming it as an architect's plan -- would let a restart
// change who may author the governing plan, which is the invariant
// resolveArchitectureForRevision exists to hold. The task stays resumable; it
// waits for a record it can trust.
var errSuppliedPlanContextUnavailable = errors.New("SUPPLIED_PLAN_CONTEXT_UNAVAILABLE: the session record does not establish who authored this task's plan, or does not hold a supplied plan intact; it is not resumed under the architect instead")

// resumedBound is the executable contract a resumed task continues under.
type resumedBound struct {
	Source       PlanSource
	Plan         string
	Rationale    string
	Steps        []string
	Mode         string
	Consequences string
	Invariants   []string
	Prospective  []ProspectiveSurface
}

// restorePlanBound re-establishes, from the session record, both who authored a
// resumed task's bound and what that bound exactly is, re-registering a
// supplied plan so every path that refuses to revise one still refuses after a
// restart.
//
// The bound is taken from the PlanProposed payload -- the decision as the
// engine recorded it -- and never from the event's summary. The summary is
// planSummary's rendering for a person, and when a plan has steps it renders
// the steps and omits the plan text entirely; a resume that implemented the
// summary continued under a different contract than the one that was routed.
//
// A record with no plan_source is NOT read as the architect's on that absence
// alone: a supplied record that lost only that field would then resume as a
// plan the architect may revise. It is read as the architect's only when the
// event itself was emitted by the architect -- a fact recorded independently
// of the payload, and the only emitter of PlanProposed before plan_source
// existed. Any other absence is refused.
func (e *Engine) restorePlanBound(task session.Interrupted) (resumedBound, error) {
	var rec proposedPlan
	recorded := len(task.PlanRecord) != 0 && json.Unmarshal(task.PlanRecord, &rec) == nil && strings.TrimSpace(rec.Plan) != ""
	switch PlanSource(task.PlanSource) {
	case PlanSupplied:
		if !recorded || rec.PlanDigest == "" || rec.PlanDigest != task.PlanDigest {
			return resumedBound{}, errSuppliedPlanContextUnavailable
		}
		e.supplyPlan(task.TaskID, SuppliedPlan{decision: rec.architectureDecision, Digest: rec.PlanDigest})
	case PlanByArchitect:
	case "":
		if task.PlanEventSource != event.SourceArchitect {
			return resumedBound{}, errSuppliedPlanContextUnavailable
		}
		if !recorded {
			// Predates the payload as well as the field: the summary is all
			// there is, and it was the architect's.
			return resumedBound{Source: PlanByArchitect, Plan: task.Plan, Rationale: task.Plan}, nil
		}
	default:
		return resumedBound{}, fmt.Errorf("the session record names a plan source this build does not know: %q", task.PlanSource)
	}
	if !recorded {
		return resumedBound{}, fmt.Errorf("the session record for %s holds no plan to resume under", task.TaskID)
	}
	return resumedBound{
		Source:       e.planSource(task.TaskID),
		Plan:         rec.Plan,
		Rationale:    rec.Summary,
		Steps:        rec.Steps,
		Mode:         planMode(rec.Mode),
		Consequences: rec.Consequences,
		Invariants:   rec.Invariants,
		Prospective:  rec.ProspectiveSurfaces,
	}, nil
}

package workflow

import (
	"context"
	"strings"

	"github.com/globulario/sensei-code/internal/assist"
	"github.com/globulario/sensei-code/internal/investigate"
	"github.com/globulario/sensei-code/internal/project"
	"github.com/globulario/sensei-code/internal/retrieval"
	"github.com/globulario/sensei-code/internal/sensei"
)

// architecturalContext assembles what the architect ought to know before it
// plans, from sources that can be checked afterwards.
//
// The governed turn used to receive the task, the conversation, the workspace
// status and a preflight -- and that preflight is necessarily the unscoped one,
// because there are no files to scope to until a plan exists. Its answer is
// therefore "PREFLIGHT_STATUS_EMPTY, coverage is thin, re-run with --file", so
// the architect planned with no governing evidence and the router then judged
// that plan against a scoped preflight the architect never saw. The evidence
// existed the whole time; it was assembled for the assisted turn and not for
// this one.
//
// Every part is DERIVED. Retrieval is driven by the task's own words against
// the graph, repository evidence is read from the checkout, and the standing
// summary is folded from this session's durable record. None of it is recalled
// dialogue: a brief that narrated remembered conclusions under a governed
// heading would undo the continuity rule that deliberately stores the identity
// of a conversation rather than its architectural claims.
//
// It shares the assisted path's own functions rather than reimplementing them,
// so the two cannot drift into disagreeing about what governs a region.
func (e *Engine) architecturalContext(ctx context.Context, sc *sensei.Client, domain, task string) (retrievedEvidence, repositoryEvidence, standingContext string, consulted assist.Consulted) {
	// The standing summary needs no Sensei and is available even when the graph
	// is not, so it is folded first and unconditionally.
	standing := project.Summarize(e.sessionEvents(), nil, project.DefaultBounds())
	standingContext = standing.Render()

	if sc == nil {
		consulted.Add(assist.Observation{
			Source: "graph retrieval",
			State:  assist.Absent,
			Reason: "no Sensei client for this turn, so nothing was looked up",
		})
		return "", "", standingContext, consulted
	}

	plan := retrieval.PlanFor(task, retrievalBudget)
	if len(plan.Queries) == 0 {
		// The task named nothing the graph can be looked up by. Ask what it
		// holds and match against real labels, rather than reporting silence
		// about a region it may cover under words the task did not use.
		var surveyed retrieval.SurveyOutcome
		plan, surveyed = surveyPlan(senseiReader{sc}, domain, task)
		if len(plan.Queries) == 0 {
			consulted.Add(assist.Observation{
				Source: "graph survey",
				State:  assist.EmptyProven,
				Reason: surveyed.Describe(),
			})
		}
	}

	var retrieved []retrieval.Outcome
	if len(plan.Queries) != 0 {
		retrieved = retrieval.Execute(senseiReader{sc}, domain, plan)
		for _, o := range retrieved {
			consulted.Add(assist.Observation{
				Source: string(o.Query.Kind) + " " + o.Query.Target,
				State:  assist.Presence(o.State),
				Reason: o.Detail,
			})
		}
		retrievedEvidence = renderRetrieved(retrieved)
	}

	repo := investigate.Repository{Root: e.Repo.Root}.Gather(ctx, retrievedTargets(retrieved), 5)
	consulted.Add(assist.Observation{
		Source: "repository evidence",
		State:  repositoryPresence(repo),
		Reason: strings.Join(repo.Unavailable, "; "),
	})
	repositoryEvidence = repo.Render()

	return retrievedEvidence, repositoryEvidence, standingContext, consulted
}

package workflow

// The authority router.
//
// Whether a plan may proceed on architectural authority alone is a property of
// the graph, not a feeling the architect reports. Before this file, the model
// returned "proceed" or "escalate" and the workflow simply obeyed, which gets
// both directions wrong: a confident model proceeds through a region the graph
// cannot cover, and a cautious model interrupts a human over a question Sensei
// could have answered outright. Neither error is visible at the time, because
// in both cases the model sounds exactly as sure as it always does.
//
// So routing takes no model text as input. It reads Sensei's structured
// evidence and the architect's explicit factual claims, and every human
// interruption it produces names the exact certifiability condition that caused
// it — an interruption a human cannot trace back to a condition is one they
// learn to dismiss.

import (
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/sensei"
)

// Route is the outcome of the authority router.
type Route string

const (
	// RouteArchitectural means Sensei can certify the region and the plan may
	// proceed without interrupting anyone.
	RouteArchitectural Route = "architectural-authority-granted"
	// RouteHuman means a human owns this decision. The condition says why.
	RouteHuman Route = "human-authority-required"
	// RouteCannotEstablish means Sensei could not vouch for its own answers, so
	// the question is not "who decides" but "the governance surface is broken".
	// Escalating this to a human as a design question would be asking them to
	// adjudicate something nobody has evidence about.
	RouteCannotEstablish Route = "cannot-establish-authority"
)

// Claim is a factual premise the architect asserts its plan rests on.
//
// Source is the architect's own account of where the premise came from. It is
// not trusted as evidence — an "inference" is routed to a human precisely
// because the model is reporting that nothing verified it.
type Claim struct {
	Statement string `json:"statement"`
	About     string `json:"about"`
	Source    string `json:"source"` // graph | repository | inference
}

// Routing is the router's decision plus the reason a human can act on.
type Routing struct {
	Route Route
	// Condition is the exact certifiability condition that produced the route.
	Condition string
}

// RequiresHuman reports whether this routing interrupts a person.
func (r Routing) RequiresHuman() bool { return r.Route == RouteHuman }

// Granted reports whether the plan may proceed on architectural authority.
func (r Routing) Granted() bool { return r.Route == RouteArchitectural }

// routeAuthority decides who owns this plan.
//
// scoped is a preflight scoped to the files the plan intends to touch, which is
// the first point in the workflow where a file list exists at all. That is why
// this is a second preflight rather than a reuse of the start gate's: the start
// gate could only ask whether Sensei was healthy, while this one can ask
// whether Sensei covers the specific region about to be edited.
//
// The architect's own decision string is deliberately not a parameter. A model
// may ask for more investigation, and that request is worth honouring as
// investigation, but it cannot by itself manufacture a human interruption when
// Sensei can certify the question.
func routeAuthority(scoped sensei.PreflightDecision, claims []Claim) Routing {
	// Sensei vouching for itself comes first. Every judgement below reads a
	// field of this same result, so if the graph is stale or unauthoritative
	// then the coverage and risk answers are not evidence either — they are a
	// stale graph's opinions, delivered with exactly the same confidence.
	if !scoped.Authority.Certifiable() {
		return Routing{Route: RouteCannotEstablish, Condition: scoped.Authority.Diagnostic()}
	}

	switch scoped.Status {
	case sensei.PreflightOK:
		// The only status that can lead to a grant.
	case sensei.PreflightEmpty:
		// Files were named and Sensei still found nothing. Unlike the unscoped
		// start query, this is a real answer: the planned region is outside
		// what the graph covers, so there is no architectural authority to
		// grant and a human owns it.
		return Routing{Route: RouteHuman, Condition: "graph coverage is absent for the planned files"}
	default:
		return Routing{
			Route:     RouteCannotEstablish,
			Condition: "preflight " + strings.ToLower(strings.TrimPrefix(string(scoped.Status), "PREFLIGHT_STATUS_")),
		}
	}

	if len(scoped.BlindSpots) != 0 {
		return Routing{
			Route:     RouteHuman,
			Condition: "Sensei reported blind spots in the planned region: " + strings.Join(scoped.BlindSpots, ", "),
		}
	}

	// An inferred premise is the architect telling us, in its own words, that
	// this part of the plan rests on something nothing checked.
	for _, c := range claims {
		if strings.EqualFold(strings.TrimSpace(c.Source), "inference") {
			statement := strings.TrimSpace(c.Statement)
			if statement == "" {
				statement = "(unstated)"
			}
			about := strings.TrimSpace(c.About)
			if about != "" {
				about = " about " + about
			}
			return Routing{
				Route:     RouteHuman,
				Condition: "the plan rests on an unverified premise" + about + ": " + statement,
			}
		}
	}

	if gate := approvalGate(scoped.RequiredActions); gate != "" && gate != "none" {
		return Routing{
			Route:     RouteHuman,
			Condition: "Sensei requires approval for this change class: " + gate,
		}
	}

	return Routing{Route: RouteArchitectural}
}

// approvalGate reads the approval class out of Sensei's change-risk action.
//
// Sensei composes this as a single formatted line — "Change risk: blast=local,
// approval=none" — and does not currently publish blast radius or approval as
// structured fields, so there is no non-textual way to obtain it. That makes
// this the one place in the governance path that reads a string, and it is kept
// narrow deliberately: it matches a fixed machine-emitted token, never prose,
// and returns "" when it cannot find one so the caller fails closed rather than
// inventing an approval class. If Sensei later publishes these structurally,
// this function should be deleted rather than extended.
func approvalGate(actions []string) string {
	const prefix = "approval="
	for _, action := range actions {
		if !strings.HasPrefix(strings.TrimSpace(action), "Change risk:") {
			continue
		}
		idx := strings.Index(action, prefix)
		if idx < 0 {
			// The line Sensei guarantees is present but shaped differently than
			// this build understands. Unknown is not none.
			return "unreadable"
		}
		gate := action[idx+len(prefix):]
		if cut := strings.IndexAny(gate, ", "); cut >= 0 {
			gate = gate[:cut]
		}
		return strings.TrimSpace(gate)
	}
	return ""
}

// escalationCondition renders a routing for a human, so a Level-3 interruption
// always arrives with the condition that caused it attached.
func escalationCondition(r Routing) string {
	if r.Condition == "" {
		return string(r.Route)
	}
	return fmt.Sprintf("%s: %s", r.Route, r.Condition)
}

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
	// RouteCloseGap means the plan cannot be granted YET because relevant
	// knowledge is incomplete, the incompleteness is bounded, and closing it
	// does not cross a human-owned consequence boundary.
	//
	// It is not permission to proceed, and it is not permission to experiment.
	// It is an instruction to go and establish what is already knowable, after
	// which governance runs again from the top. Retrieval silence still confers
	// nothing: the route grants no authority over the planned change, it names
	// work that must happen before the question can even be asked properly.
	//
	// Only after established knowledge has been retrieved, and only if it
	// leaves more than one viable technical alternative, does the
	// DesignQuestion lane become relevant. This route does not enter it.
	RouteCloseGap Route = "bounded-knowledge-gap"
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
	// Blast and Gate are Sensei's structured change-risk verdict, carried
	// forward rather than consumed here.
	//
	// The router only asks one question of them — may this proceed without a
	// person — and answering it discards how expensive the change is. That
	// second fact decides something else: how adversarially the candidate must
	// be judged. A local change reviewed by whoever is free and a system-wide
	// one reviewed by the provider that wrote it are not the same risk, and
	// without these fields the second is indistinguishable from the first by the
	// time the reviewer is assigned.
	Blast string
	Gate  string
}

// RequiresHuman reports whether this routing interrupts a person.
func (r Routing) RequiresHuman() bool { return r.Route == RouteHuman }

// Granted reports whether the plan may proceed on architectural authority.
func (r Routing) Granted() bool { return r.Route == RouteArchitectural }

// ClosesGap reports whether the route names bounded epistemic work rather than
// an owner for the decision.
func (r Routing) ClosesGap() bool { return r.Route == RouteCloseGap }

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
	// The risk reading is attached to whatever route the decision reaches. It is
	// not the route's justification; it is a fact about the change that outlives
	// this decision, and a caller that has to re-derive it will re-derive it
	// from a preflight taken at a different moment.
	r := decideRoute(scoped, claims)
	r.Blast = scoped.ChangeRisk.Blast()
	r.Gate = scoped.ChangeRisk.Gate()
	return r
}

func decideRoute(scoped sensei.PreflightDecision, claims []Claim) Routing {
	// The order below is the whole design, and it is not the order the
	// conditions were written in.
	//
	//   1. can Sensei vouch for itself at all
	//   2. is the surface answering
	//   3. does a CONSEQUENCE verdict already own this
	//   4. only then, is the blocker epistemic
	//
	// Consequence must outrank every epistemic question, or closing a knowledge
	// gap becomes a way to walk past an approval gate: the router would notice
	// the missing coverage first, send the agent off to establish evidence, and
	// never reach the verdict that said a human owns this change class. That
	// bug was live in the first draft of this file and is pinned by
	// TestAnApprovalGateIsNotClosableByEvidence.

	// Sensei vouching for itself comes first. Every judgement below reads a
	// field of this same result, so if the graph is stale or unauthoritative
	// then the coverage and risk answers are not evidence either — they are a
	// stale graph's opinions, delivered with exactly the same confidence.
	if !scoped.Authority.Certifiable() {
		return Routing{Route: RouteCannotEstablish, Condition: scoped.Authority.Diagnostic()}
	}

	coverageAbsent := false
	switch scoped.Status {
	case sensei.PreflightOK:
		// The only status that can lead to a grant.
	case sensei.PreflightEmpty:
		// Files were named and Sensei still found nothing: the planned region
		// is outside what the graph covers. Recorded rather than returned, so
		// the consequence checks below still run over it.
		coverageAbsent = true
	default:
		return Routing{
			Route:     RouteCannotEstablish,
			Condition: "preflight " + strings.ToLower(strings.TrimPrefix(string(scoped.Status), "PREFLIGHT_STATUS_")),
		}
	}

	// Consequence authority. Both of these escalate rather than permit, so
	// reading them on an EMPTY preflight is safe in the one direction that
	// matters: an absent classification can stop a change here, and can never
	// clear one.
	//
	// An EXPLICIT approval verdict outranks everything, including a coverage
	// gap. Guarded by Classified(), because Gate() renders an unclassified
	// verdict as "unclassified" — which is not "none" and would otherwise
	// escalate here as though a verdict had been reached.
	if scoped.ChangeRisk.Classified() {
		if gate := scoped.ChangeRisk.Gate(); gate != "none" {
			return Routing{
				Route: RouteHuman,
				Condition: "Sensei requires approval for this change class: " + gate +
					" (blast radius " + scoped.ChangeRisk.Blast() + ")",
			}
		}
	}

	// Epistemic incompleteness. Nothing below grants; each names work that must
	// happen before the question can be asked properly.
	//
	// This precedes the unclassified-gate check on purpose. When the graph
	// covers nothing, it has nothing to classify either: the missing verdict
	// and the missing coverage are one absence, and reporting it as "nobody
	// judged what this change costs" dresses an epistemic hole as a consequence
	// verdict. That is the same conflation this file exists to undo.
	if coverageAbsent {
		// Sending this to a human asks them to supply coverage Sensei lacks —
		// a technical answer — and answering leaves the graph exactly as empty
		// as before, so the next task over the same region asks again.
		return Routing{Route: RouteCloseGap, Condition: "graph coverage is absent for the planned files"}
	}

	// An unclassified gate on a preflight that DOES hold coverage is a
	// different animal: the graph has anchors here and still reached no
	// verdict, so nobody has judged what the change costs. Not a default to
	// proceed on, and not obviously bounded work either — across 135 probed
	// files it never fired once, and reclassifying a branch with no
	// observations behind it would be guessing.
	if !scoped.ChangeRisk.Classified() {
		return Routing{
			Route:     RouteHuman,
			Condition: "Sensei classified no approval gate for the planned region",
		}
	}

	// Blind spots are read, not counted. See blindspot.go for the measured
	// vocabulary and why the two kinds are opposites.
	if len(scoped.BlindSpots) != 0 {
		spots := readBlindSpots(scoped.BlindSpots)
		switch {
		case len(spots.Unrecognised) != 0:
			// Fail closed. A blind spot nobody has classified must not become
			// bounded work by default, or every future addition to Sensei's
			// vocabulary becomes silent autonomy.
			return Routing{
				Route: RouteHuman,
				Condition: "Sensei reported a blind spot this router has no reading for: " +
					strings.Join(spots.Unrecognised, ", "),
			}
		case len(spots.Coverage) != 0:
			return Routing{
				Route:     RouteCloseGap,
				Condition: "Sensei reported missing coverage in the planned region: " + strings.Join(spots.Coverage, ", "),
			}
		default:
			// Only consequence signals remain: severity, path class, namespace.
			// These describe knowledge the graph HAS, not knowledge it lacks,
			// and on 22 of the 26 measured OK files they escalate a region the
			// risk channel had already classified APPROVAL_GATE_NONE.
			//
			// Deferring them to that gate would make those 22 grantable. That
			// is a policy choice about who may edit high-risk paths unattended
			// — a consequence and value question — and it is NOT taken here.
			// The route is unchanged; only the condition is corrected, so the
			// escalation stops describing strong knowledge as a blind spot.
			//
			// Declared as dq.consequence_blind_spot_authority rather than
			// decided in passing. Widening a router to improve a coverage
			// number is the failure this line of work exists to avoid.
			return Routing{
				Route: RouteHuman,
				Condition: "Sensei reported consequence signals in the planned region (knowledge the graph holds, not a gap): " +
					strings.Join(spots.Consequence, ", "),
			}
		}
	}

	// An inferred premise is the architect telling us, in its own words, that
	// this part of the plan rests on something nothing checked. That is a
	// verification task, not a decision a human owns: what is being asked for
	// is evidence, and the architect is the one who can go and get it.
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
				Route:     RouteCloseGap,
				Condition: "the plan rests on an unverified premise" + about + ": " + statement,
			}
		}
	}

	return Routing{Route: RouteArchitectural}
}

// escalationCondition renders a routing for a human, so a Level-3 interruption
// always arrives with the condition that caused it attached.
func escalationCondition(r Routing) string {
	if r.Condition == "" {
		return string(r.Route)
	}
	return fmt.Sprintf("%s: %s", r.Route, r.Condition)
}

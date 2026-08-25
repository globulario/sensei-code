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
	"strconv"
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
	// RouteObserve means the action changes nothing, so no architectural
	// authority is needed and none is granted.
	//
	// This exists because a governed audit of an uncovered region used to be
	// impossible. "Check these doc comments against the code" needs no coverage
	// -- reading a file establishes nothing and breaks nothing -- but the router
	// saw a region the graph could not cover and sent the question to a human,
	// which meant the system could not investigate a subsystem until it already
	// knew enough to modify it. Observation and authorization were the same
	// question, and they are not.
	//
	// What makes this safe is not trust. It is that the stage is structural and
	// the absence of a change is VERIFIED before the run may report anything.
	RouteObserve Route = "observation-no-authority-needed"
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

// Observes reports whether this action may proceed as a read-only observation.
func (r Routing) Observes() bool { return r.Route == RouteObserve }

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
	return routeAuthorityForAction(scoped, claims, Action{})
}

// routeAuthorityForAction is routeAuthority with the proposed action supplied.
// Production always uses this: an action-less routing can only ever reach
// CANNOT_ESTABLISH once a consequence signal is the last thing standing, which
// is the correct answer to a question nobody asked properly.
func routeAuthorityForAction(scoped sensei.PreflightDecision, claims []Claim, action Action) Routing {
	// The risk reading is attached to whatever route the decision reaches. It is
	// not the route's justification; it is a fact about the change that outlives
	// this decision, and a caller that has to re-derive it will re-derive it
	// from a preflight taken at a different moment.
	r := decideRouteForAction(scoped, claims, action)
	r.Blast = scoped.ChangeRisk.Blast()
	r.Gate = scoped.ChangeRisk.Gate()
	return r
}

func decideRoute(scoped sensei.PreflightDecision, claims []Claim) Routing {
	return decideRouteForAction(scoped, claims, Action{})
}

// decideRouteForAction is decideRoute with the proposed action supplied.
//
// The action matters only where a consequence signal is the last thing
// standing: everywhere else the route is decided before it is read.
func decideRouteForAction(scoped sensei.PreflightDecision, claims []Claim, action Action) Routing {
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
	// An action that changes nothing is routed before Sensei is asked to vouch
	// for anything, because none of what follows is about it.
	//
	// Deliberately ahead of the certifiability check: whether the graph is
	// stale, unauthoritative or silent has no bearing on whether a process may
	// READ a file. Placing it after would make an unusable graph prevent the
	// very investigation that might explain why the graph is unusable -- which
	// is precisely the trap where a system cannot learn about itself until it
	// already knows enough to change itself.
	//
	// Nothing here grants authority. It records that none was required.
	if action.Stage == StageObserve {
		return Routing{Route: RouteObserve,
			Condition: "the action reads and reports; no file is written and nothing is admitted, " +
				"so there is no architectural authority to grant"}
	}

	if !scoped.Authority.Certifiable() {
		return Routing{Route: RouteCannotEstablish, Condition: scoped.Authority.Diagnostic()}
	}

	// The status decides whether there is an ANSWER to read. It does not decide
	// what the answer says about coverage.
	switch scoped.Status {
	case sensei.PreflightOK, sensei.PreflightEmpty:
		// Both are answers. What they establish is read below, from the
		// coverage evidence, not from which of the two words came back.
	default:
		return Routing{
			Route:     RouteCannotEstablish,
			Condition: "preflight " + strings.ToLower(strings.TrimPrefix(string(scoped.Status), "PREFLIGHT_STATUS_")),
		}
	}

	// Coverage is computed from coverage evidence.
	//
	// This read `PreflightEmpty -> coverageAbsent = true`, converting a summary
	// status into a different and stronger proposition: "graph coverage is
	// absent for the planned files". Those are equivalent only if the preflight
	// contract guarantees the equivalence, and it does not. Live counterexample,
	// pinned in TestAnEmptyStatusIsNotACoverageVerdict:
	//
	//	internal/workflow/authority.go
	//	  status   PREFLIGHT_STATUS_EMPTY
	//	  coverage sufficient=true indexed_file_count=1
	//
	// Coverage.Proven() is TRUE there while the router declared the region
	// uncovered and sent the run off to close a gap that was not open. The
	// status was accurate; the stronger reading of it was false.
	//
	// Reading Coverage directly also stops a future status value from silently
	// changing routing: a new enum member alters what is ANSWERABLE, never what
	// is COVERED.
	// DerivedCoverage is applied further down, where it already was.
	coverageAbsent := !scoped.Coverage.Proven()

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
	// A machine-derived fact may close a coverage gap, but only where a
	// derivation succeeded in THIS world over THESE files. The caller
	// revalidated to obtain the list; nothing here reads a stored record.
	//
	// It closes the gap rather than granting anything: the consequence checks
	// above have already run, and the premise checks below still apply.
	if coverageAbsent && len(action.Files) != 0 && coversAll(action.DerivedCoverage, action.Files) {
		coverageAbsent = false
	}
	if coverageAbsent {
		// Sending this to a human asks them to supply coverage Sensei lacks —
		// a technical answer — and answering leaves the graph exactly as empty
		// as before, so the next task over the same region asks again.
		//
		// The condition states the evidence rather than the status, because the
		// two came apart: a preflight can answer EMPTY while publishing
		// sufficient coverage, and it can answer OK while proving none.
		return Routing{Route: RouteCloseGap,
			Condition: "graph coverage is absent for the planned files: " + scoped.Coverage.Diagnostic()}
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
			//
			// Assessed rather than routed. A consequence signal says LOOK
			// HARDER HERE; it does not say who decides. What decides is
			// whether THIS action's consequences are bounded.
			signals := strings.Join(spots.Consequence, ", ")
			assessment := AssessConsequences(action)
			switch assessment.Result {
			case ConsequenceBounded:
				// The signal was real and the action is still bounded. Fall
				// through to the gate checks, which have already run above --
				// so reaching here means the risk channel published
				// APPROVAL_GATE_NONE for this exact region and this assessment
				// agrees the action cannot reach past its boundary.
			case ConsequenceUnacceptable:
				return Routing{
					Route: RouteHuman,
					Condition: "consequence signals in the planned region (" + signals + ") and the proposed action is not bounded: " +
						assessment.Boundary,
				}
			default:
				// "Nobody knows" is not "this is dangerous", and reporting it
				// as an authority question would ask a human to adjudicate
				// something no one has evidence about.
				return Routing{
					Route: RouteCannotEstablish,
					Condition: "consequence signals in the planned region (" + signals + ") and this action's consequences could not be established: " +
						assessment.Boundary,
				}
			}
		}
	}

	// Provenance is read from a closed vocabulary, and only "graph" and
	// "repository" are evidence.
	//
	// An inferred premise is the architect telling us, in its own words, that
	// this part of the plan rests on something nothing checked. That is a
	// verification task, not a decision a human owns: what is being asked for
	// is evidence, and the architect is the one who can go and get it.
	//
	// Anything else is read the same way, and that is the point. Claim.Source
	// is whatever the model typed: decodeModelJSON validates no field, so a
	// blank source, a misspelling, or a word this router has no reading for
	// arrives looking exactly like a checked premise. Recognising only
	// "inference" made every one of those grantable -- an unchecked premise
	// acquired architectural authority by being labelled with something nobody
	// defined. The same fail-closed rule as an unrecognised blind spot above,
	// for the same reason: a later addition to the vocabulary must not become
	// silent autonomy by arriving before the router has a reading for it.
	for _, c := range claims {
		source := strings.ToLower(strings.TrimSpace(c.Source))
		if source == "graph" || source == "repository" {
			continue
		}
		statement := strings.TrimSpace(c.Statement)
		if statement == "" {
			statement = "(unstated)"
		}
		about := strings.TrimSpace(c.About)
		if about != "" {
			about = " about " + about
		}
		if source == "inference" {
			return Routing{
				Route:     RouteCloseGap,
				Condition: "the plan rests on an unverified premise" + about + ": " + statement,
			}
		}
		// The provenance is named, because the work this route asks for is not
		// the same work: an inference needs evidence gathered, while this needs
		// the premise re-stated with a source that can be checked at all.
		return Routing{
			Route: RouteCloseGap,
			Condition: "the plan rests on an unverified premise" + about +
				" (" + declaredSource(c.Source) + "): " + statement,
		}
	}

	return Routing{Route: RouteArchitectural}
}

// declaredSource renders what the architect actually wrote in a claim's source
// field, so a routing caused by unreadable provenance says which provenance.
//
// A missing source and a misspelled one are different mistakes and are reported
// as different mistakes: the first is a claim that never stated where it came
// from, the second is a claim that stated something this router cannot read.
func declaredSource(source string) string {
	declared := strings.TrimSpace(source)
	if declared == "" {
		return "no source was declared"
	}
	return "unrecognised source " + strconv.Quote(declared)
}

// coversAll reports whether every planned file is covered.
//
// Every one. Partial derived coverage is not coverage: a plan touching a file
// no derivation looked at is a plan Sensei cannot speak for, and accepting the
// majority would grant authority over the remainder for free.
func coversAll(covered, planned []string) bool {
	if len(covered) == 0 {
		return false
	}
	have := make(map[string]bool, len(covered))
	for _, c := range covered {
		have[c] = true
	}
	for _, p := range planned {
		if !have[p] {
			return false
		}
	}
	return true
}

// escalationCondition renders a routing for a human, so a Level-3 interruption
// always arrives with the condition that caused it attached.
func escalationCondition(r Routing) string {
	if r.Condition == "" {
		return string(r.Route)
	}
	return fmt.Sprintf("%s: %s", r.Route, r.Condition)
}

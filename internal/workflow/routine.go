package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/sensei"
)

// Level-1 routine: proportional ceremony without lost authority.
//
// Specified in docs/p1-level-1-routine.md. The problem it addresses is that a
// one-file test change travels the whole governed path, and disproportionate
// ceremony is not a cosmetic complaint — it trains a person to approve without
// reading, which converts every prompt in the product into noise and quietly
// removes the protection the prompts existed to provide.
//
// One rule bounds the whole slice:
//
//	Relax interruptions, never evidence.
//
// A Level-1 change skips the Level-3 escalation and skips nothing else. It is
// still isolated in a candidate, still audited by Sensei, still reviewed, and
// still cannot be accepted over a refusal. What changes is that nobody is woken
// up about it.
//
// Smallness is computed, never claimed. The moment "this is just a typo"
// becomes a judgement an agent asserts, model-controlled authority is back in
// friendlier clothing. So this is a pure function over Sensei's own structured
// evidence with no model input, and the one place a model's statement is
// consulted — an inferred claim — restricts rather than grants.

// RouteRoutine is a fourth route beside the existing three. It is a narrowing
// of architectural authority, never a way around a human boundary.
const RouteRoutine Route = "level-1-routine"

// RoutineDecision is the classification and its reasons.
//
// Qualifying is kept even when the change does not qualify, because the point
// of the dark run is to learn which condition does the blocking. A decision
// that reported only true/false would answer the question "did it qualify" and
// leave "what would it take" unanswerable from the record.
type RoutineDecision struct {
	Routine bool `json:"routine"`
	// Qualifying lists every condition that held, in order.
	Qualifying []string `json:"qualifying,omitempty"`
	// Blocking is the first condition that did not, when Routine is false.
	Blocking string `json:"blocking,omitempty"`
}

// Describe renders the decision for the transcript.
func (d RoutineDecision) Describe() string {
	if d.Routine {
		return fmt.Sprintf("routine: all %d conditions held", len(d.Qualifying))
	}
	return fmt.Sprintf("not routine: %s (%d condition(s) held first)", d.Blocking, len(d.Qualifying))
}

// governancePath is the set this tier may never fast-path.
//
// The exclusion matters most and is the easiest to forget: a routine tier that
// could fast-path an edit to its own qualifying conditions is a tier that can
// widen itself. It is a prefix list rather than a call into Sensei because it
// is a statement about this program's own structure, which Sensei has no reason
// to hold.
var governancePath = []string{
	"internal/sensei/contracts.go",
	"internal/workflow/gate.go",
	"internal/workflow/authority.go",
	"internal/workflow/routine.go",
	"internal/broker",
	"internal/candidate",
	"internal/authority",
}

// High-risk files are deliberately NOT re-read from
// docs/awareness/high_risk_files.yaml here. That list is one input to Sensei's
// own protection derivation, which also covers governed sources, files a
// governed invariant names, contract files and annotated source. Re-deriving a
// subset of it locally would duplicate Sensei governance semantics and would
// disagree with Sensei the moment either side changed. A protected file arrives
// through the preflight instead — as a direct invariant, or as a blind spot
// naming the high-risk directory.

// classifyRoutine decides whether a change is routine, from evidence only.
//
// scoped is a file-scoped preflight for the planned files. claims are the
// architect's stated premises. edit is the edit-check result for the candidate.
// planned is what the approved plan named; changed is what the candidate
// actually touched.
func classifyRoutine(scoped sensei.PreflightDecision, claims []Claim, edit sensei.EditCheckResult, planned, changed []string) RoutineDecision {
	var held []string
	block := func(reason string) RoutineDecision {
		return RoutineDecision{Routine: false, Qualifying: held, Blocking: reason}
	}

	// Categorical exclusions first. They are not measurements and no amount of
	// clean evidence overrides them.
	for _, path := range changed {
		if excluded, why := categoricallyExcluded(path); excluded {
			return block(why)
		}
	}

	// 1. A stale graph will call anything routine, fluently.
	if !scoped.Authority.Certifiable() {
		return block("Sensei cannot vouch for its own graph: " + scoped.Authority.Diagnostic())
	}
	held = append(held, "graph authority is certifiable")

	// 2. The graph actually answered about these files. This is also what keeps
	// ignorance out: a file the graph has no facts about comes back DEGRADED,
	// and one it has never heard of comes back EMPTY. Neither is OK.
	if scoped.Status != sensei.PreflightOK {
		return block("preflight is " + strings.ToLower(strings.TrimPrefix(string(scoped.Status), "PREFLIGHT_STATUS_")) + ", not ok")
	}
	held = append(held, "preflight answered OK for these files")

	// 3. Coverage is proven-empty rather than absent.
	//
	// The specification treats this as a separate condition, and in this
	// deployment it is carried by condition 2: Sensei answers a file it holds no
	// facts about with DEGRADED and says so in as many words — "this is NOT
	// proof of safety — the graph has no facts about this file". It is asserted
	// again here rather than assumed, because the day that stops being true is
	// the day a fast path starts firing on precisely the code nobody has ever
	// analysed.
	if absent, why := coverageIsAbsent(scoped); absent {
		return block(why)
	}
	held = append(held, "coverage is proven rather than absent")

	// 4. Nothing governing is in scope.
	for _, inv := range scoped.DirectInvariants {
		switch strings.ToLower(strings.TrimSpace(inv.Severity)) {
		case "critical", "high":
			return block("a " + strings.ToLower(inv.Severity) + " invariant governs this region: " + inv.ID)
		}
	}
	held = append(held, "no critical or high invariant is in scope")

	// 5. Sensei is not reporting that it cannot see.
	if len(scoped.BlindSpots) != 0 {
		return block("Sensei reported blind spots: " + strings.Join(scoped.BlindSpots, "; "))
	}
	held = append(held, "no blind spots reported")

	// 6. Sensei's own risk classification, not ours.
	if !scoped.ChangeRisk.Classified() {
		return block("Sensei classified no change risk for this region")
	}
	if blast, gate := scoped.ChangeRisk.Blast(), scoped.ChangeRisk.Gate(); blast != "local" || gate != "none" {
		return block("change risk is blast=" + blast + " gate=" + gate + ", not local/none")
	}
	held = append(held, "change risk is local with no approval gate")

	// 7. The shape is not a known-broken repair.
	//
	// A check that did not run cannot clear anything. This is the condition the
	// surface's own error text is emphatic about: an unreachable edit check is
	// not an empty result.
	if !edit.Clean() {
		return block(edit.Diagnostic())
	}
	held = append(held, "edit check ran and matched no forbidden fix")

	// 8. Scope did not widen after the decision.
	if widened := notPlanned(planned, changed); len(widened) != 0 {
		return block("the candidate touched files the plan did not name: " + strings.Join(widened, ", "))
	}
	held = append(held, "every changed path was named by the plan")

	// 9. The architect itself reported an unverified premise.
	//
	// The one case where a model's own statement restricts rather than grants,
	// which is safe in the direction it operates.
	for _, c := range claims {
		if strings.EqualFold(strings.TrimSpace(c.Source), "inference") {
			statement := strings.TrimSpace(c.Statement)
			if statement == "" {
				statement = "(unstated)"
			}
			return block("the plan rests on an unverified premise: " + statement)
		}
	}
	held = append(held, "no claim is marked inference")

	return RoutineDecision{Routine: true, Qualifying: held}
}

// categoricallyExcluded reports a path this tier may never fast-path.
func categoricallyExcluded(path string) (bool, string) {
	clean := strings.TrimSpace(path)
	for _, prefix := range governancePath {
		if clean == prefix || strings.HasPrefix(clean, strings.TrimSuffix(prefix, "/")+"/") {
			return true, "the change touches the governance path itself: " + clean
		}
	}
	return false, ""
}

// coverageIsAbsent distinguishes a graph that covers this region and reports
// nothing governing it from a graph that has never heard of it.
func coverageIsAbsent(scoped sensei.PreflightDecision) (bool, string) {
	for _, spot := range scoped.BlindSpots {
		if strings.Contains(strings.ToLower(spot), "coverage_insufficient") {
			return true, "the graph has no facts about these files, which is ignorance rather than evidence"
		}
	}
	return false, ""
}

// notPlanned returns changed paths the plan never named.
func notPlanned(planned, changed []string) []string {
	named := make(map[string]bool, len(planned))
	for _, p := range planned {
		named[strings.TrimSpace(p)] = true
	}
	var widened []string
	for _, c := range changed {
		if c = strings.TrimSpace(c); c != "" && !named[c] {
			widened = append(widened, c)
		}
	}
	return widened
}

// classifyForDarkRun computes the Level-1 classification and records it.
//
// Stage 1 of the rollout grants nothing: this runs after the candidate exists
// and before the review, changes no branch, and skips no step. It exists so the
// question that decides whether the tier is worth having is answered from real
// runs rather than from intuition — and so that if nothing qualifies for a
// month, the answer is to invest in graph coverage rather than to loosen the
// conditions.
//
// A classification that cannot be computed is reported as such rather than as
// "not routine". The two are different: one says the change was measured and
// found architectural, the other says nobody measured it, and a dark run that
// conflated them would report a tidy zero for the wrong reason.
func (e *Engine) classifyForDarkRun(sc *sensei.Client, start certifiedStart, taskID string, tc *taskContext, diff string) {
	record, ok := e.routingFor(taskID)
	if !ok {
		e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.RoutineClassified,
			"routine classification unavailable: no routing evidence was recorded for this task", nil))
		return
	}

	changed := changedPaths(diff)
	edit, err := e.editCheck(sc, start, changed, diff)
	if err != nil {
		// Reported, not swallowed, and not treated as a clean check. The
		// surface's own refusal text is emphatic that an unreachable check is
		// not an empty result, and the classifier agrees: an unanswered check
		// blocks rather than clears.
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
			"edit check could not run for the routine classification: "+err.Error(), nil))
	}

	decision := classifyRoutine(record.Scoped, record.Claims, edit, record.Planned, changed)
	e.emit(event.New(e.SessionID, taskID, event.SourceSystem, event.RoutineClassified,
		"level-1 dark run — "+decision.Describe()+" (nothing was skipped)", decision))
}

// editCheck asks Sensei whether the candidate's content matches a known-broken
// repair shape.
//
// It is asked once for the whole candidate rather than per file: the surface
// takes one file and its proposed content, and the classifier needs a single
// answer for the change. The first changed path carries the diff as its
// content, which is what this repository's advisory rules are written against —
// none of them carries a matchable pattern today, so every check observed so
// far returns clean. That is worth stating plainly: condition 7 currently
// proves that nothing matched, in a corpus where nothing can match.
func (e *Engine) editCheck(sc *sensei.Client, start certifiedStart, changed []string, diff string) (sensei.EditCheckResult, error) {
	if len(changed) == 0 {
		return sensei.EditCheckResult{}, fmt.Errorf("the candidate changed no files")
	}
	args := map[string]any{"file": changed[0], "proposed_content": diff}
	if domain := start.Domain(); domain != "" {
		args["domain"] = domain
	}
	result, err := sc.CallTool("awareness_edit_check", args)
	if err != nil {
		return sensei.EditCheckResult{}, err
	}
	return sensei.DecodeEditCheck(result)
}

// RoutineTally summarises the dark run for a human.
//
// It answers the question that decides whether the tier is worth building:
// across real runs, how many changes would have qualified, and when they did
// not, which condition stopped them. A count on its own would say the tier is
// unused without saying whether that is because the conditions are right or
// because one of them can never hold.
type RoutineTally struct {
	Classified int            `json:"classified"`
	Qualified  int            `json:"qualified"`
	Unmeasured int            `json:"unmeasured"`
	Blocking   map[string]int `json:"blocking,omitempty"`
}

// RoutineSummary reads the session record and tallies what the dark run saw.
func RoutineSummary(events []event.Event) RoutineTally {
	tally := RoutineTally{Blocking: map[string]int{}}
	for _, ev := range events {
		if ev.Kind != event.RoutineClassified {
			continue
		}
		tally.Classified++
		var d RoutineDecision
		if len(ev.Payload) == 0 || json.Unmarshal(ev.Payload, &d) != nil {
			// A classification that carries no decision is one that could not be
			// computed. Counting it as "not routine" would report a tidy zero
			// for the wrong reason.
			tally.Unmeasured++
			continue
		}
		if d.Routine {
			tally.Qualified++
			continue
		}
		tally.Blocking[generalise(d.Blocking)]++
	}
	return tally
}

// Render is the /report line. It says nothing at all when nothing has been
// classified, rather than printing a zero that reads like a measurement.
func (t RoutineTally) Render() string {
	if t.Classified == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Level-1 routine (dark run, grants nothing): %d of %d governed change(s) would have qualified",
		t.Qualified, t.Classified))
	if t.Unmeasured != 0 {
		b.WriteString(fmt.Sprintf("; %d could not be classified", t.Unmeasured))
	}
	b.WriteString("\n")
	for _, reason := range sortedByCount(t.Blocking) {
		b.WriteString(fmt.Sprintf("  %3d × %s\n", t.Blocking[reason], reason))
	}
	if t.Qualified == 0 && t.Classified > 0 {
		b.WriteString("  Nothing qualified. If one condition accounts for all of it, the answer is\n")
		b.WriteString("  to invest in graph coverage rather than to loosen that condition.\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// generalise collapses a blocking reason to its class, so the tally groups runs
// that were stopped by the same condition rather than by the same file name.
func generalise(reason string) string {
	reason = strings.TrimSpace(reason)
	for _, prefix := range []string{
		"a critical invariant governs this region",
		"a high invariant governs this region",
		"Sensei reported blind spots",
		"the change touches the governance path itself",
		"the candidate touched files the plan did not name",
		"the plan rests on an unverified premise",
		"Sensei cannot vouch for its own graph",
		"change risk is blast=",
		"preflight is ",
		"the edit check did not run",
	} {
		if strings.HasPrefix(reason, prefix) {
			return strings.TrimSuffix(strings.TrimSpace(prefix), "=")
		}
	}
	return reason
}

func sortedByCount(counts map[string]int) []string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if counts[keys[i]] != counts[keys[j]] {
			return counts[keys[i]] > counts[keys[j]]
		}
		return keys[i] < keys[j]
	})
	return keys
}

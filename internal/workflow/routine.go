package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/report"
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

// CandidateShape is what the diff did, as distinct from which paths it named.
//
// The specification sketches classifyRoutine as taking changed []string, and a
// list of paths cannot express one of the rules the specification itself
// requires: deletion or weakening of an existing test is never routine, and
// neither is visible in a path. So the classifier takes the shape instead,
// which subsumes the path list.
type CandidateShape struct {
	Files []report.FileChange
	// TestLineDelta is net added-minus-removed lines per test file. A test that
	// shrank is the mechanical trace of weakening; a test that grew is not.
	TestLineDelta map[string]int
}

// shapeOf reads the diff rather than asking the worker what it did.
func shapeOf(diff string) CandidateShape {
	shape := CandidateShape{Files: report.FromDiff(diff).Files, TestLineDelta: map[string]int{}}
	var current string
	for _, line := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(line, "+++ b/"):
			current = strings.TrimPrefix(line, "+++ b/")
		case strings.HasPrefix(line, "--- a/") && current == "":
			current = strings.TrimPrefix(line, "--- a/")
		case !isTestPath(current):
			// nothing to count
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			// headers, not content
		case strings.HasPrefix(line, "+"):
			shape.TestLineDelta[current]++
		case strings.HasPrefix(line, "-"):
			shape.TestLineDelta[current]--
		}
	}
	return shape
}

// Paths is every path the candidate touched.
func (c CandidateShape) Paths() []string {
	out := make([]string, 0, len(c.Files))
	for _, f := range c.Files {
		out = append(out, f.Path)
	}
	return out
}

// isTestPath reports whether a path holds tests.
//
// Exact for Go, which is this repository's implementation language, and
// heuristic elsewhere. The heuristic errs toward calling something a test,
// which is the safe direction: a false positive costs a change its fast path
// and nothing else, while a false negative would let a deleted test through.
func isTestPath(path string) bool {
	clean := strings.ToLower(strings.TrimSpace(path))
	if clean == "" {
		return false
	}
	if strings.HasSuffix(clean, "_test.go") {
		return true
	}
	for _, marker := range []string{"_test.", ".test.", "test_", "/tests/", "/test/", "/spec/", "_spec."} {
		if strings.Contains(clean, marker) {
			return true
		}
	}
	return false
}

// classifyRoutine decides whether a change is routine, from evidence only.
//
// scoped is a file-scoped preflight for the planned files. claims are the
// architect's stated premises. edit is the edit-check result for the candidate.
// planned is what the approved plan named; changed is what the candidate
// actually touched.
func classifyRoutine(scoped sensei.PreflightDecision, claims []Claim, edit sensei.EditCheckResult, planned []string, shape CandidateShape) RoutineDecision {
	var held []string
	changed := shape.Paths()
	block := func(reason string) RoutineDecision {
		return RoutineDecision{Routine: false, Qualifying: held, Blocking: reason}
	}

	// Categorical exclusions first. They are not measurements and no amount of
	// clean evidence overrides them.
	for _, f := range shape.Files {
		if excluded, why := categoricallyExcluded(f.Path); excluded {
			return block(why)
		}
		// A deleted test is the one exclusion a path list cannot express, and
		// the one most worth having: a change that removes the proof of a
		// behaviour must never be the change nobody is told about.
		if f.Status == report.Deleted && isTestPath(f.Path) {
			return block("the candidate deletes a test: " + f.Path)
		}
	}
	for path, delta := range shape.TestLineDelta {
		if delta < 0 {
			return block(fmt.Sprintf("the candidate weakens a test: %s lost %d line(s)", path, -delta))
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
	// Read from the published coverage verdict, not inferred from the shape of
	// the answer and not matched out of a human-readable note. An earlier draft
	// of this condition searched the blind spots for the substring
	// "coverage_insufficient", which is the same mistake as the change-risk
	// parser this repository deleted: a governance decision resting on
	// recognising a sentence, failing open the day the sentence is reworded.
	//
	// The distinction is the load-bearing one in the whole tier. A fast path
	// that fired on absent coverage would fast-path precisely the code nobody
	// has ever analysed.
	if !scoped.Coverage.Proven() {
		return block("coverage is not proven for these files, which is ignorance rather than evidence: " + scoped.Coverage.Diagnostic())
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

	shape := shapeOf(diff)
	edit, err := e.editCheck(sc, start, shape.Paths(), diff)
	if err != nil {
		// Reported, not swallowed, and not treated as a clean check. The
		// surface's own refusal text is emphatic that an unreachable check is
		// not an empty result, and the classifier agrees: an unanswered check
		// blocks rather than clears.
		e.emit(event.New(e.SessionID, taskID, event.SourceSensei, event.Status,
			"edit check could not run for the routine classification: "+err.Error(), nil))
	}

	decision := classifyRoutine(record.Scoped, record.Claims, edit, record.Planned, shape)
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

// Revocation turns a routine change that later went wrong into a proposal
// against the conditions that let it through.
//
// The tier tightens from experience instead of being trusted permanently. That
// only works if the conditions that qualified a change survive the change: a
// privilege granted on reasoning nobody kept cannot be revised, only revoked
// wholesale or defended on faith.
//
// This produces the proposal and does not make it. Recording knowledge is
// Sensei's, and a tier that could quietly rewrite its own qualifying conditions
// in response to its own failures would be deciding its own scope.
type Revocation struct {
	// Qualified is what let the change through, verbatim from the decision.
	Qualified []string `json:"qualified"`
	// Candidate identifies the change now implicated.
	Candidate string `json:"candidate,omitempty"`
	// Failure is what was observed.
	Failure string `json:"failure"`
}

// RevocationFor builds the proposal, or reports that there is nothing to revise.
//
// A change that never took the routine path has no qualifying conditions to
// blame, and proposing against the tier for a failure it did not enable would
// teach the graph a false lesson. That is the one case this refuses.
func RevocationFor(d RoutineDecision, candidate, failure string) (Revocation, error) {
	if !d.Routine {
		return Revocation{}, fmt.Errorf("this change did not take the routine path, so the tier did not enable the failure")
	}
	if strings.TrimSpace(failure) == "" {
		return Revocation{}, errors.New("a revocation must say what was observed")
	}
	if len(d.Qualifying) == 0 {
		return Revocation{}, errors.New("the decision recorded no qualifying conditions, so there is nothing to revise")
	}
	return Revocation{Qualified: d.Qualifying, Candidate: candidate, Failure: strings.TrimSpace(failure)}, nil
}

// Proposal renders the failure_mode a human reviews before it is recorded.
func (r Revocation) Proposal() string {
	var b strings.Builder
	b.WriteString("A change classified routine was implicated in a failure.\n\n")
	b.WriteString("OBSERVED: " + r.Failure + "\n")
	if r.Candidate != "" {
		b.WriteString("CANDIDATE: " + r.Candidate + "\n")
	}
	b.WriteString("\nTHE CONDITIONS THAT LET IT THROUGH, and which this proposal is against:\n")
	for i, c := range r.Qualified {
		b.WriteString(fmt.Sprintf("  %d. %s\n", i+1, c))
	}
	b.WriteString("\nOne of these is weaker than it reads. Revising the tier means naming which,\n")
	b.WriteString("and what evidence it should have required instead.")
	return b.String()
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
	// Skipped enumerates the changes that qualified, each with the conditions
	// that qualified it. A privilege that cannot be enumerated afterwards is not
	// governed, and a count cannot answer "show me everything that skipped
	// escalation, and why".
	//
	// During stage 1 nothing skips, so these are the changes that would have.
	// The distinction is stated wherever this is rendered, because a list of
	// changes under the heading "skipped" would claim a privilege was exercised
	// that never was.
	Skipped []SkippedChange `json:"skipped,omitempty"`
}

// SkippedChange is one change the tier let through, and why.
type SkippedChange struct {
	TaskID     string   `json:"task_id,omitempty"`
	Qualifying []string `json:"qualifying"`
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
			tally.Skipped = append(tally.Skipped, SkippedChange{TaskID: ev.TaskID, Qualifying: d.Qualifying})
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
	for _, s := range t.Skipped {
		b.WriteString("  would have skipped escalation: " + taskOrUnnamed(s.TaskID) + "\n")
		for _, c := range s.Qualifying {
			b.WriteString("      because " + c + "\n")
		}
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

func taskOrUnnamed(id string) string {
	if strings.TrimSpace(id) == "" {
		return "(a change the record did not name)"
	}
	return id
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

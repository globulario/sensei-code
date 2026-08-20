package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/sensei"
)

// certifiableAuthority is Sensei vouching for its own answers: authoritative,
// graph current, seed current. Anything less and a stale graph would be calling
// changes routine, fluently.
func certifiableAuthority() sensei.Authority {
	return sensei.Authority{
		Authoritative:       true,
		GraphFreshnessState: sensei.GraphCurrent,
		SeedState:           sensei.SeedCurrent,
	}
}

// qualifyingPreflight is the only shape that can be routine: the graph vouches
// for itself, it answered about these files, nothing governing is in scope, it
// reports no blind spot, and it classified the change as local with no gate.
func qualifyingPreflight() sensei.PreflightDecision {
	return sensei.PreflightDecision{
		Status:    sensei.PreflightOK,
		Authority: certifiableAuthority(),
		ChangeRisk: sensei.ChangeRisk{
			BlastRadius:  "BLAST_RADIUS_LOCAL",
			ApprovalGate: "APPROVAL_GATE_NONE",
		},
	}
}

func cleanEditCheck() sensei.EditCheckResult {
	return sensei.EditCheckResult{Answered: true}
}

func TestARoutineChangeQualifiesOnEvidenceAlone(t *testing.T) {
	d := classifyRoutine(qualifyingPreflight(), nil, cleanEditCheck(),
		[]string{"internal/tui/model_test.go"}, []string{"internal/tui/model_test.go"})
	if !d.Routine {
		t.Fatalf("this change should qualify; blocked by %q", d.Blocking)
	}
	if len(d.Qualifying) != 9 {
		t.Fatalf("expected all nine conditions recorded, got %d: %v", len(d.Qualifying), d.Qualifying)
	}
}

// Each condition, removed one at a time from an otherwise qualifying change.
// The table is the specification: a condition that stops appearing here has
// stopped being enforced.
func TestEveryConditionCanBlockOnItsOwn(t *testing.T) {
	planned := []string{"internal/tui/model_test.go"}
	for _, tc := range []struct {
		name    string
		scoped  func(sensei.PreflightDecision) sensei.PreflightDecision
		claims  []Claim
		edit    sensei.EditCheckResult
		changed []string
		want    string
	}{
		{
			name:   "1 uncertifiable graph",
			scoped: func(p sensei.PreflightDecision) sensei.PreflightDecision { p.Authority = sensei.Authority{}; return p },
			want:   "cannot vouch for its own graph",
		},
		{
			name: "2 preflight did not answer OK",
			scoped: func(p sensei.PreflightDecision) sensei.PreflightDecision {
				p.Status = "PREFLIGHT_STATUS_DEGRADED"
				return p
			},
			want: "preflight is degraded",
		},
		{
			name: "3 coverage is absent rather than proven",
			scoped: func(p sensei.PreflightDecision) sensei.PreflightDecision {
				p.BlindSpots = []string{"coverage_insufficient: no direct anchors and no strong-tier pattern match"}
				return p
			},
			want: "ignorance rather than evidence",
		},
		{
			name: "4 a critical invariant governs the region",
			scoped: func(p sensei.PreflightDecision) sensei.PreflightDecision {
				p.DirectInvariants = []sensei.Invariant{{ID: "sensei_code.publication.never_merges", Severity: "critical"}}
				return p
			},
			want: "critical invariant governs this region",
		},
		{
			name: "5 Sensei reports a blind spot",
			scoped: func(p sensei.PreflightDecision) sensei.PreflightDecision {
				p.BlindSpots = []string{"file path under high-risk directory"}
				return p
			},
			want: "blind spots",
		},
		{
			name: "6 change risk is not local/none",
			scoped: func(p sensei.PreflightDecision) sensei.PreflightDecision {
				p.ChangeRisk = sensei.ChangeRisk{BlastRadius: "BLAST_RADIUS_SYSTEM", ApprovalGate: "APPROVAL_GATE_HUMAN"}
				return p
			},
			want: "blast=system",
		},
		{
			name: "6b change risk is unclassified",
			scoped: func(p sensei.PreflightDecision) sensei.PreflightDecision {
				p.ChangeRisk = sensei.ChangeRisk{}
				return p
			},
			want: "classified no change risk",
		},
		{
			name: "7 the edit check did not run",
			edit: sensei.EditCheckResult{},
			want: "did not run",
		},
		{
			name: "7b the edit check matched a forbidden shape",
			edit: sensei.EditCheckResult{Answered: true, Reported: []string{"matches: 1 entry"}},
			want: "edit check matched",
		},
		{
			name:    "8 the candidate widened its own scope",
			changed: []string{"internal/tui/model_test.go", "internal/tui/model.go"},
			want:    "the plan did not name",
		},
		{
			name:   "9 the architect reported an unverified premise",
			claims: []Claim{{Statement: "nothing else reads this field", Source: "inference"}},
			want:   "unverified premise",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			scoped := qualifyingPreflight()
			if tc.scoped != nil {
				scoped = tc.scoped(scoped)
			}
			edit := tc.edit
			if !strings.HasPrefix(tc.name, "7") {
				edit = cleanEditCheck()
			}
			changed := tc.changed
			if changed == nil {
				changed = planned
			}
			d := classifyRoutine(scoped, tc.claims, edit, planned, changed)
			if d.Routine {
				t.Fatalf("this change must not qualify")
			}
			if !strings.Contains(d.Blocking, tc.want) {
				t.Fatalf("blocked by %q, want something containing %q", d.Blocking, tc.want)
			}
		})
	}
}

// The exclusion that matters most: a routine tier able to fast-path an edit to
// its own qualifying conditions is a tier that can widen itself.
func TestTheRoutineTierCannotFastPathItsOwnGovernance(t *testing.T) {
	for _, path := range []string{
		"internal/workflow/routine.go",
		"internal/workflow/authority.go",
		"internal/workflow/gate.go",
		"internal/sensei/contracts.go",
		"internal/broker/broker.go",
		"internal/candidate/identity.go",
		"internal/authority/authority.go",
	} {
		d := classifyRoutine(qualifyingPreflight(), nil, cleanEditCheck(), []string{path}, []string{path})
		if d.Routine {
			t.Fatalf("%s was classified routine; the tier can widen itself", path)
		}
		if !strings.Contains(d.Blocking, "governance path") {
			t.Fatalf("%s was blocked for the wrong reason: %q", path, d.Blocking)
		}
	}
}

// A categorical exclusion is not a measurement, so no amount of clean evidence
// overrides it — including a plan that named the file.
func TestCleanEvidenceDoesNotOverrideACategoricalExclusion(t *testing.T) {
	path := "internal/broker/broker.go"
	d := classifyRoutine(qualifyingPreflight(), nil, cleanEditCheck(), []string{path}, []string{path})
	if d.Routine {
		t.Fatal("a categorical exclusion was overridden by clean evidence")
	}
	if len(d.Qualifying) != 0 {
		t.Fatalf("an exclusion should short-circuit before any condition is credited, got %v", d.Qualifying)
	}
}

// An unanswered edit check must never read as a clean one. This is the
// distinction the surface's own refusal text insists on.
func TestAnUnrunEditCheckIsNotACleanOne(t *testing.T) {
	var never sensei.EditCheckResult
	if never.Clean() {
		t.Fatal("the zero value reports itself clean, so a check nobody ran would clear a change")
	}
	if !cleanEditCheck().Clean() {
		t.Fatal("a check that ran and found nothing should be clean")
	}
	reported := sensei.EditCheckResult{Answered: true, Reported: []string{"x"}}
	if reported.Clean() {
		t.Fatal("a check that reported something is not clean")
	}
}

// Stage 1 grants nothing. The classification is computed and recorded, and no
// branch of the workflow consults it.
func TestTheDarkRunGrantsNothing(t *testing.T) {
	body := funcBody(t, "internal/workflow/routine.go", "classifyForDarkRun")
	for _, forbidden := range []string{"awaitHuman", "awaitChoice", "RouteRoutine"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("the dark run reaches %s; stage 1 must grant nothing", forbidden)
		}
	}
	// And nothing else in the workflow branches on the decision yet.
	for _, file := range []string{"internal/workflow/engine.go", "internal/workflow/authority.go"} {
		if strings.Contains(fileText(t, file), "RouteRoutine") {
			t.Fatalf("%s consults RouteRoutine; stage 1 skips no step", file)
		}
	}
}

func TestTheTallyDistinguishesUnmeasuredFromNotRoutine(t *testing.T) {
	mk := func(d *RoutineDecision) event.Event {
		if d == nil {
			return event.New("s", "t", event.SourceSystem, event.RoutineClassified, "unavailable", nil)
		}
		return event.New("s", "t", event.SourceSystem, event.RoutineClassified, "x", d)
	}
	events := []event.Event{
		mk(&RoutineDecision{Routine: true}),
		mk(&RoutineDecision{Blocking: "a critical invariant governs this region: x"}),
		mk(&RoutineDecision{Blocking: "a critical invariant governs this region: y"}),
		mk(nil),
		event.New("s", "t", event.SourceSystem, event.Status, "unrelated", nil),
	}
	got := RoutineSummary(events)
	if got.Classified != 4 || got.Qualified != 1 || got.Unmeasured != 1 {
		t.Fatalf("tally = %+v", got)
	}
	// Two runs blocked by the same condition on different files group together,
	// so the report names the condition rather than the file.
	if got.Blocking["a critical invariant governs this region"] != 2 {
		t.Fatalf("blocking reasons did not group: %+v", got.Blocking)
	}
	rendered := got.Render()
	if !strings.Contains(rendered, "1 of 4") || !strings.Contains(rendered, "1 could not be classified") {
		t.Fatalf("render = %q", rendered)
	}
}

func TestAnEmptyTallyPrintsNothing(t *testing.T) {
	if got := RoutineSummary(nil).Render(); got != "" {
		t.Fatalf("a session with no governed run rendered %q; a zero tally reads like a finding", got)
	}
}

func TestTheDecisionSurvivesTheEventRecord(t *testing.T) {
	d := RoutineDecision{Blocking: "Sensei reported blind spots: x", Qualifying: []string{"a", "b"}}
	ev := event.New("s", "t", event.SourceSystem, event.RoutineClassified, d.Describe(), d)
	var back RoutineDecision
	if err := json.Unmarshal(ev.Payload, &back); err != nil {
		t.Fatalf("decision did not survive the record: %v", err)
	}
	if back.Blocking != d.Blocking || len(back.Qualifying) != 2 {
		t.Fatalf("round trip lost detail: %+v", back)
	}
}

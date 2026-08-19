package retrieval

import (
	"errors"
	"strings"
	"testing"
)

// Selection routes a lookup; it never decides anything. What the question names
// becomes a target to ask about, and the graph's answer is what governs.
func TestSelectionRoutesLookupsFromWhatTheQuestionNames(t *testing.T) {
	plan := PlanFor("does internal/workflow/authority.go still satisfy sensei_code.workflow.plan_is_approved_before_work_starts?", 4)
	if len(plan.Queries) != 2 {
		t.Fatalf("planned %d queries, want 2: %+v", len(plan.Queries), plan.Queries)
	}
	if plan.Queries[0].Kind != ByFile || plan.Queries[0].Target != "internal/workflow/authority.go" {
		t.Errorf("the named path was not routed as a file lookup: %+v", plan.Queries[0])
	}
	if plan.Queries[1].Kind != ByID || !strings.HasPrefix(plan.Queries[1].Target, "sensei_code.workflow") {
		t.Errorf("the named governed id was not routed as an id lookup: %+v", plan.Queries[1])
	}
	for _, q := range plan.Queries {
		if strings.TrimSpace(q.Why) == "" {
			t.Errorf("%s was selected with no traceable reason", q.Target)
		}
	}

	// Prose alone must not manufacture targets. A pattern loose enough to match
	// sentences would send the graph looking for sentences.
	if got := PlanFor("what do you think about the way we handle authority in general?", 4); len(got.Queries) != 0 {
		t.Errorf("prose produced targets: %+v", got.Queries)
	}
}

// A bound that is not stated reads exactly like complete coverage.
func TestTheBudgetIsBoundedAndSaysWhatItDropped(t *testing.T) {
	plan := PlanFor("compare a/one.go b/two.go c/three.go d/four.go e/five.go", 3)
	if len(plan.Queries) != 3 {
		t.Fatalf("budget was not enforced: %d queries", len(plan.Queries))
	}
	if !plan.Bounded() || len(plan.Dropped) != 2 {
		t.Fatalf("the cap was silent: %+v", plan)
	}
	described := plan.Describe()
	for _, want := range []string{"not retrieved", "e/five.go"} {
		if !strings.Contains(described, want) {
			t.Errorf("the plan does not disclose what it omitted (%q):\n%s", want, described)
		}
	}
}

type fakeCaller struct {
	results map[string]Result
	errs    map[string]error
}

func (f fakeCaller) CallTool(name string, args map[string]any) (Result, error) {
	key, _ := args["file"].(string)
	if key == "" {
		key, _ = args["id"].(string)
	}
	if err := f.errs[key]; err != nil {
		return Result{}, err
	}
	return f.results[key], nil
}

// The empty answer is the one that matters. A target the graph knows nothing
// about must be reported as asked-and-empty, never widened until something
// comes back and never quietly dropped — both of which leave the architect
// answering from memory while sounding graph-backed.
func TestAnEmptyAnswerIsReportedRatherThanWidened(t *testing.T) {
	plan := PlanFor("check internal/known.go and internal/unknown.go and internal/broken.go", 4)
	caller := fakeCaller{
		results: map[string]Result{
			"internal/known.go":   {Text: "invariant: something governs this"},
			"internal/unknown.go": {Text: "  "},
		},
		errs: map[string]error{"internal/broken.go": errors.New("connection refused")},
	}

	outcomes := Execute(caller, "github.com/globulario/sensei-code", plan)
	if len(outcomes) != 3 {
		t.Fatalf("got %d outcomes, want 3", len(outcomes))
	}
	byTarget := map[string]Outcome{}
	for _, o := range outcomes {
		byTarget[o.Query.Target] = o
	}
	if got := byTarget["internal/known.go"]; got.State != StatePresent || got.Text == "" {
		t.Errorf("a found answer was not carried: %+v", got)
	}
	if got := byTarget["internal/unknown.go"]; got.State != StateEmptyProven {
		t.Errorf("an empty answer was not reported as one: %+v", got)
	} else if !strings.Contains(got.Detail, "internal/unknown.go") {
		t.Errorf("the empty answer does not name what was asked about: %+v", got)
	}
	if got := byTarget["internal/broken.go"]; got.State != StateUnavailable {
		t.Errorf("a failed lookup was not reported as unavailable: %+v", got)
	}
}

// Retrieval may only reach Sensei's typed read surfaces.
func TestOnlyTypedReadSurfacesAreReachable(t *testing.T) {
	for _, q := range []Query{{Kind: ByFile, Target: "a.go"}, {Kind: ByID, Target: "x.y.z"}} {
		name, args := q.request("dom")
		switch name {
		case "awareness_briefing", "awareness_query":
		default:
			t.Errorf("%v routed to %q, which is not a typed read surface", q, name)
		}
		if args["domain"] != "dom" {
			t.Errorf("the query was not domain-scoped: %v", args)
		}
		if mode, ok := args["mode"]; ok && mode != "by_id" {
			t.Errorf("a free-form query mode reached the graph: %v", mode)
		}
	}
}

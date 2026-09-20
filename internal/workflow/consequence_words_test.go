package workflow

import "testing"

// The consequence lane classifies declared ACTIONS. It does not search prose
// for frightening words.
//
// The first governed run against a foreign repository was refused as "declares
// an outward action: release" because its plan said "acquisition and release
// logic remains unchanged" -- semaphore.Weighted.Release, a method. Ambiguous
// prose must never silently become an outward action; an undeclared publish is
// stopped by the stage boundary regardless.
func TestOutwardActionsAreReadFromDeclaredActionsNotScaryWords(t *testing.T) {
	notOutward := []string{
		"acquisition and release logic remains unchanged",
		"Weighted.Release restores capacity to the semaphore",
		"release a waiter once its weight is available",
		"notifyWaiters wakes queued callers under mu",
		"migrate the field into the struct that owns the lock",
		"production of the snapshot happens under the same mutex",
		"the deployment.go helper is unchanged",
		"upload_test.go covers the parser",
	}
	for _, s := range notOutward {
		if got := declaredOutwardActions([]string{s}, ""); len(got) != 0 {
			t.Fatalf("%q was read as an outward action (%v); ambiguous prose must not "+
				"silently acquire consequence authority", s, got)
		}
	}
	outward := map[string]string{
		"cut a release for v1.2":                        "cut a release",
		"publish release v1.2 to the registry":          "publish",
		"deploy to the staging cluster":                 "deploy",
		"git push the branch to origin":                 "git push",
		"run the migration against the production db":   "run the migration",
		"notify the team once the artifact is uploaded": "notify the team",
	}
	for s, want := range outward {
		got := declaredOutwardActions([]string{s}, "")
		if len(got) == 0 {
			t.Fatalf("%q declares an outward action and was not read as one", s)
		}
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("%q: expected %q among %v", s, want, got)
		}
	}
}

// And the assessment itself, end to end, on the sentence that caused the halt.
func TestASemaphorePlanIsNotADeployment(t *testing.T) {
	a := Action{Stage: StageCandidateEdit, Files: []string{"semaphore/semaphore.go"},
		DeclaredSteps: []string{"add Stats returning held weight and queued waiters under mu"},
		DeclaredConsequences: "Calling Stats briefly contends on Weighted's existing mutex, " +
			"but acquisition and release logic remains unchanged."}
	if got := AssessConsequences(a); got.Result != ConsequenceBounded {
		t.Fatalf("a semaphore edit was assessed %s (%s); the word 'release' named a method",
			got.Result, got.Boundary)
	}
	a.DeclaredSteps = append(a.DeclaredSteps, "then cut a release and publish it")
	if got := AssessConsequences(a); got.Result != ConsequenceUnacceptable {
		t.Fatal("a plan that declares cutting a release must still escalate")
	}
}

// POLARITY, NOT STRINGS. The same hazardous operation, read in four contexts.
//
// Observed 2026-09-20 on task-1789870806342069862: the plan step "publication
// may open a branch and pull request only, never merge or push to main" was
// assessed UNACCEPTABLE with "the plan declares an outward action: push to",
// and the task stopped at a Level-3 human boundary the plan had promised never
// to reach. The inversion is the harm: the more carefully an objective writes
// down what it will NOT do, the more certainly it escalates.
//
// So the operation is held constant -- "push to main" in all four rows -- and
// only the context around it moves. A row that passed because the wording
// changed would prove nothing; these rows share the wording.
//
// Rows 1 and 4 are the control. They are the Level-3 path this repair must
// leave exactly as it is, and row 4 in particular is the negative control for
// the forbidden fix of suppressing any sentence containing "never" or "not":
// it carries "no reviewer is bypassed" in the same clause as a genuinely
// asserted push, so a classifier that read negation at the sentence, or
// anywhere in the clause rather than ahead of the operation, would grant it.
func TestTheSameOutwardOperationIsClassifiedByWhatThePlanAsserts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		step       string
		wantResult ConsequenceResult
		wantRoute  Route
		says       string
	}{{
		// 1. Asserted. The plan says it will do the thing.
		name:       "an affirmative outward action escalates",
		step:       "once the tests pass the run will push to main so CI picks the change up",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}, {
		// 2. Prohibited. The plan says it will NOT do the thing. Verbatim from
		// the live halt, so this row is bound to the observation rather than to
		// a paraphrase of it -- and note that one negator governs a coordination
		// ("never merge or push to main"), which is why " or " is not a clause
		// break.
		name:       "an explicit prohibition is not a declaration",
		step:       "publication may open a branch and pull request only, never merge or push to main",
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 3. Mentioned. The operation is quoted, as the name of a boundary the
		// run is writing a test for. Deliberately carries no negator of its own,
		// so it can only pass if mentions are distinguished from uses.
		name:       "a quoted forbidden action is a mention, not a use",
		step:       `the objective the worker was given says "push to main" is out of bounds, and this run adds the test that proves it`,
		wantResult: ConsequenceBounded,
		wantRoute:  RouteArchitectural,
	}, {
		// 4. Asserted, wrapped in reassurance. The negator sits in the SAME
		// clause as the push and negates something else entirely, so this row
		// fails both ways a repair can overshoot: suppressing a sentence that
		// contains "no", and counting negators anywhere in the clause instead
		// of only ahead of the operation they govern.
		name:       "reassuring prose does not clear an asserted outward action",
		step:       "every edit stays in the disposable candidate worktree; the last step will push to main and no reviewer is bypassed",
		wantResult: ConsequenceUnacceptable,
		wantRoute:  RouteHuman,
		says:       "outside the worktree",
	}} {
		t.Run(tc.name, func(t *testing.T) {
			action := Action{Stage: StageCandidateEdit,
				Files:         []string{"internal/workflow/consequence.go"},
				DeclaredSteps: []string{tc.step}}

			got := AssessConsequences(action)
			if got.Result != tc.wantResult {
				t.Fatalf("AssessConsequences = %s (%s), want %s\n  effects: %v\n  step: %q",
					got.Result, got.Boundary, tc.wantResult, got.Effects, tc.step)
			}

			routing := routeAuthorityForAction(
				scopedPreflight(t, okBody(t, nil, "APPROVAL_GATE_NONE")), nil, action)
			if routing.Route != tc.wantRoute {
				t.Fatalf("route = %s (%s), want %s\n  step: %q",
					routing.Route, routing.Condition, tc.wantRoute, tc.step)
			}
			// And it must reach that route for the stated reason. An escalation
			// that arrived by some other path would satisfy the route check
			// while proving nothing about this classifier.
			if tc.says != "" && !containsWord(routing.Condition, tc.says) {
				t.Fatalf("escalated without naming why: %q", routing.Condition)
			}
			if tc.wantResult == ConsequenceBounded && !containsWord(got.Boundary, "candidate worktree") {
				t.Fatalf("bounded without naming the boundary: %q", got.Boundary)
			}
		})
	}
}

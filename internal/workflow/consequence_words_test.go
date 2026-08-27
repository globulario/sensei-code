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

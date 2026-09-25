package workflow

import (
	"os"
	"strings"
	"testing"
)

// This is deliberately a composition/source pin, matching the repository's
// other last-wire tests. The value-producing pieces are behaviorally tested in
// roles and ghbridge; this test protects the one easy-to-delete edge between
// them: the workflow must mint architecture identity from its own records before
// handing the turn to a resolver. A resolver extracting the same values from the
// rendered prompt would consume weaker presentation truth and could drift.
func TestArchitectBindingComesFromWorkflowTruthBeforeResolver(t *testing.T) {
	raw, err := os.ReadFile("runners.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, want := range []string{
		"roles.BindArchitecture(",
		"e.objective(taskID).Text",
		"e.governedBase(taskID)",
		"graph.Digest",
		// The graph repository is CONFIGURED. Deriving it from the workspace
		// remote would send a Sensei graph commit to a repository that never
		// held it, which is the 422 this binding exists to prevent.
		"e.Config.Sensei.Repository",
		"spec.Architecture = e.architectureBinding(spec.TaskID)",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("architect binding no longer carries workflow truth %q", want)
		}
	}
	bindAt := strings.Index(src, "spec.Architecture = e.architectureBinding(spec.TaskID)")
	resolveAt := strings.Index(src, "e.Runners.Resolve(spec)")
	if bindAt < 0 || resolveAt < 0 || bindAt > resolveAt {
		t.Fatal("the resolver can see the architect turn before the workflow has attached its objective/world binding")
	}
}

// Binding truth and presentation truth are separate surfaces and must agree.
// The architecture digest is minted from the durable objective record, while
// the remote architect consumes the rendered prompt. Both currently originate
// from the exact same task variable. Pin that composition so a later refactor
// cannot normalize/rewrite one path while leaving the other unchanged and still
// produce a perfectly valid, perfectly misleading envelope.
func TestArchitectReadsTheSameObjectiveBytesTheWorkflowRecorded(t *testing.T) {
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "e.recordObjective(taskID, Objective{Text: task, Provenance: how})") {
		t.Fatal("the durable objective is no longer recorded from the governed task bytes this proof names")
	}
	if !strings.Contains(src, "config.DisplayName(e.Config.Architect.Name), task, conversation,") {
		t.Fatal("architecturePrompt no longer receives the same governed task bytes recorded as the objective")
	}
}

// A planned resume restores the recorded objective ONCE, through the
// record-if-absent path, before anything in it can reach the owed architect
// re-plan. Behaviour is witnessed by TestW1APlannedTaskResumedAtItsOwedReplan...
// in nonconvergence_test.go; this pins the position, which that witness cannot
// see because Sensei certification cannot be driven there.
func TestAPlannedResumeRestoresTheObjectiveOnceBeforeTheOwedReplan(t *testing.T) {
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	start := strings.Index(src, "func (e *Engine) Resume(")
	if start < 0 {
		t.Fatal("Resume is gone, so this pin would witness nothing")
	}
	body := src[start:]
	if end := strings.Index(body[1:], "\nfunc "); end >= 0 {
		body = body[:end+1]
	}
	const restore = "e.recordObjectiveIfAbsent(task.TaskID, Objective{Text: task.Task, Provenance: ResumedGoverned})"
	if n := strings.Count(body, restore); n != 1 {
		t.Fatalf("Resume restores the recorded objective %d times, want exactly once", n)
	}
	if strings.Contains(body, "e.recordObjective(") {
		t.Fatal("Resume can replace a held objective; only record-if-absent preserves provenance")
	}
	unplanned := strings.Index(body, "e.resumeUnplannedArchitecture(ctx, task)")
	restoreAt := strings.Index(body, restore)
	sensei := strings.Index(body, "sensei.Start(")
	replan := strings.Index(body, "e.resolveArchitectureForRevision(")
	if unplanned < 0 || sensei < 0 || replan < 0 || !(unplanned < restoreAt && restoreAt < sensei && sensei < replan) {
		t.Fatal("the planned resume does not restore the recorded objective before anything can fail or reach the owed re-plan")
	}
}

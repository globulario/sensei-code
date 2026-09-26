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

// sourceOf is one top-level function's text, located by its declaration line.
func sourceOf(t *testing.T, src, decl string) string {
	t.Helper()
	at := strings.Index(src, decl)
	if at < 0 {
		t.Fatalf("%s is gone", decl)
	}
	end := strings.Index(src[at:], "\n}\n")
	if end < 0 {
		t.Fatalf("%s has no end", decl)
	}
	return src[at : at+end]
}

// ONE AUTHORITY, REACHED ONCE. A resume restores the objective identity at one
// place -- its entry, before the mode is announced and before any of its three
// branches is dispatched -- and no branch restores its own. A branch-local
// restoration is exactly how one branch obeyed the law and another did not
// (task-1790353318851268310, 2026-09-25).
//
// And the objective-referent guard sits between minting and choosing: after
// the binding is attached, before BOTH the provider command line and a
// configured resolver, and reading the durable record rather than the process
// cache it exists to distrust.
func TestTheResumeRestoresTheObjectiveOnceAndTheGuardPrecedesEveryAdapter(t *testing.T) {
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	engine := string(raw)
	resume := sourceOf(t, engine, "func (e *Engine) Resume(")
	restore := "e.restoreDurableTask(task)"
	if strings.Count(resume, restore) != 1 {
		t.Fatalf("Resume restores the durable task %d times, want exactly once", strings.Count(resume, restore))
	}
	restoreAt := strings.Index(resume, restore)
	for _, after := range []string{"e.announceMode(", "e.resumeAuthority(ctx, task)", "e.resumeUnplannedArchitecture(ctx, task)", "candidate.Load(e.Repo.Root, task.TaskID)"} {
		at := strings.Index(resume, after)
		if at < 0 || at < restoreAt {
			t.Errorf("%s is reached before the one restoration", after)
		}
	}
	// The planned branch lives in Resume itself; it and the other two branches
	// perform no reconciliation of their own.
	branches := map[string]string{
		"Resume (planned branch)":     resume[restoreAt+len(restore):],
		"resumeAuthority":             sourceOf(t, engine, "func (e *Engine) resumeAuthority("),
		"resumeUnplannedArchitecture": sourceOf(t, engine, "func (e *Engine) resumeUnplannedArchitecture("),
	}
	for name, body := range branches {
		for _, forbidden := range []string{"recordObjective", "restoreDurableTask", "e.objectives"} {
			if strings.Contains(body, forbidden) {
				t.Errorf("%s restores the objective itself (%s)", name, forbidden)
			}
		}
	}

	raw, err = os.ReadFile("runners.go")
	if err != nil {
		t.Fatal(err)
	}
	runners := string(raw)
	resolve := sourceOf(t, runners, "func (e *Engine) resolveRunner(")
	order := []string{
		"spec.Architecture = e.architectureBinding(spec.TaskID)",
		"e.refuseUnboundObjective(spec)",
		"CLIResolved(spec, e.SessionID)",
		"e.Runners.Resolve(spec)",
	}
	last := -1
	for _, step := range order {
		at := strings.Index(resolve, step)
		if at < 0 || at < last {
			t.Fatalf("resolveRunner no longer orders binding -> objective guard -> adapter choice at %q", step)
		}
		last = at
	}
	guard := sourceOf(t, runners, "func (e *Engine) refuseUnboundObjective(")
	if !strings.Contains(guard, "e.durableTask(spec.TaskID)") {
		t.Error("the objective guard does not decide from the durable task record")
	}
	for _, forbidden := range []string{"e.objectives", "e.objective("} {
		if strings.Contains(guard, forbidden) {
			t.Errorf("the objective guard decides from the process cache (%s)", forbidden)
		}
	}
}

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
		// The objective comes from the one objective record, never from the
		// provenance reader that answers an absent objective with empty text.
		"objective, err := e.objectiveRecord(taskID)",
		"objective.Text,",
		"e.governedBase(taskID)",
		"graph.Digest",
		// The graph repository is CONFIGURED. Deriving it from the workspace
		// remote would send a Sensei graph commit to a repository that never
		// held it, which is the 422 this binding exists to prevent.
		"e.Config.Sensei.Repository",
		"binding, err := e.architectureBinding(spec.TaskID)",
		"spec.Architecture = binding",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("architect binding no longer carries workflow truth %q", want)
		}
	}
	if strings.Contains(src, "e.objective(taskID)") {
		t.Error("architecture binding reads the provenance projection, where an absent objective is empty text")
	}
	bindAt := strings.Index(src, "binding, err := e.architectureBinding(spec.TaskID)")
	resolveAt := strings.Index(src, "e.Runners.Resolve(spec)")
	if bindAt < 0 || resolveAt < 0 || bindAt > resolveAt {
		t.Fatal("the resolver can see the architect turn before the workflow has attached its objective/world binding")
	}
	// Absence is returned before the resolver, not bound as an empty text.
	refuseAt := strings.Index(src[bindAt:], "return Resolved{}, fmt.Errorf(")
	if refuseAt < 0 || bindAt+refuseAt > resolveAt {
		t.Fatal("an absent objective is not refused before the resolver is asked")
	}
}

// W3 -- ABSENCE REFUSES BEFORE THE ARCHITECT RUNNER.
//
// A continuation that can request architecture -- an unplanned task resumed in
// a fresh engine -- with no objective in its durable history and none in
// memory. It must stop before the resolver is ever asked, and say why; the
// objective must not have become an empty string, the task id, or the bytes the
// continuation happened to be handed.
//
// The control is the same continuation over a history that DOES record the
// objective: it reaches the resolver. Without it, a continuation that never
// reached any architect would pass the absence half for the wrong reason.
func TestW3AnAbsentObjectiveRefusesBeforeTheArchitectRunner(t *testing.T) {
	const handed = "the bytes the continuation was handed"
	absent := resumeUnplannedToArchitect(t, "task-w3-absent", "", handed, nil)
	if absent.reached {
		t.Fatalf("an architect request with no recorded objective reached the resolver: %+v\n%s",
			absent.spec.Architecture, absent.trace)
	}
	if !strings.Contains(absent.ended, "has no recorded objective") {
		t.Fatalf("the continuation did not refuse on the absent objective; it ended %q\n%s", absent.ended, absent.trace)
	}
	if o := absent.engine.objective("task-w3-absent"); o.Text != "" || o.HumanAuthorized() {
		t.Fatalf("an absent objective was projected as %+v", o)
	}

	present := resumeUnplannedToArchitect(t, "task-w3-present", handed, handed, nil)
	if !present.reached {
		t.Fatalf("CONTROL: the same continuation over a recorded objective never reached the resolver: %q\n%s",
			present.ended, present.trace)
	}
	if got, want := present.spec.Architecture.ObjectiveDigest, objectiveDigestRule(handed); got == "" || got != want {
		t.Fatalf("CONTROL: the recorded objective was bound as %q, want %q", got, want)
	}
}

// W3, the rule half: absent or empty input has no digest under the one rule,
// and both of its consumers -- the architecture binding and the webhook
// proposal store -- are routed to that rule rather than keeping their own.
func TestW3OneDigestRuleYieldsNoDigestForAnAbsentObjective(t *testing.T) {
	if got := objectiveDigestRule(""); got != "" {
		t.Fatalf("an absent objective has digest %q under the shared rule", got)
	}
	for _, c := range []struct{ file, call string }{
		{"../roles/architecture.go", "ObjectiveDigest:  event.ObjectiveDigest(objective),"},
		{"../ghwebhook/proposal.go", "return event.ObjectiveDigest(objective)"},
	} {
		raw, err := os.ReadFile(c.file)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if !strings.Contains(src, c.call) {
			t.Errorf("%s no longer takes its objective digest from event.ObjectiveDigest", c.file)
		}
		// A second rule would be a second hash of the objective's bytes.
		if strings.Contains(src, "sha256.Sum256([]byte(objective))") {
			t.Errorf("%s hashes the objective by a rule of its own", c.file)
		}
	}
	engine, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	// No continuation keeps a restoration rule of its own.
	if strings.Contains(string(engine), "recordObjectiveIfAbsent") {
		t.Error("a path-local objective restoration is back beside the objective record")
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

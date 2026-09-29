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
		// The one objective accessor, whose ABSENT is a refusal -- never the
		// provenance view, which reads an absent objective as empty text.
		"objective, err := e.objectiveRecord(taskID)",
		"objective.Text",
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
	bindAt := strings.Index(src, "spec.Architecture = binding")
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

// W3 -- ABSENCE IS A REFUSAL, BEFORE ANY ARCHITECT IS ASKED.
//
// A task whose durable history records no creation, in a process that holds no
// objective for it, has no objective anyone can name. The continuation that
// would ask its architect stops at the binding: neither the resolver nor the
// architect runner is reached, and no digest is minted for it.
func TestAnAbsentObjectiveRefusesTheArchitectTurnBeforeTheArchitectIsAsked(t *testing.T) {
	const task = "task-absent"
	e, _, _ := blockedEngine(t, t.TempDir(), "session-absent")
	// Durable history exists and speaks of the task -- just not its creation.
	e.announceMode(task, governedMode(ResumedGoverned))
	// Another task's objective, held in this process, is not this one's.
	e.recordObjective("task-other", Objective{Text: "another objective", Provenance: RequestedByHuman})
	architect := &scriptedArchitect{turns: []architectTurn{{text: replyDecision}}}
	resolver := &fixedResolver{runner: architect, name: "claude"}
	e.Runners = resolver

	_, err := e.resolveArchitectureIn(t.Context(), nil, certifiedStart{}, task, "task", "PROMPT", t.TempDir())
	if err == nil || !strings.Contains(err.Error(), ErrObjectiveAbsent.Error()) {
		t.Fatalf("an architect turn with no recorded objective was not refused as ABSENT: %v", err)
	}
	if len(resolver.specs) != 0 || len(architect.prompts) != 0 {
		t.Fatalf("the architect was reached with no objective: %d resolver call(s), %d prompt(s)", len(resolver.specs), len(architect.prompts))
	}
	if o := e.objective(task); o.Text != "" || o.HumanAuthorized() {
		t.Fatalf("absence was filled in: %+v", o)
	}

	// The binding path produces no digest for an absent or an empty objective:
	// ABSENT is not the SHA-256 of "", and neither is empty.
	e.recordObjective("task-empty", Objective{Text: "", Provenance: SubmittedUnattended})
	for _, id := range []string{task, "task-empty"} {
		b, err := e.architectureBinding(id)
		if err == nil || b.ObjectiveDigest != "" || b.Valid() {
			t.Fatalf("%s: an objective nobody can name was bound: %+v (err %v)", id, b, err)
		}
	}
	// And present bytes are bound exactly, untrimmed: the same identity the
	// objective-proposal digest pins in internal/ghwebhook (W4).
	e.recordObjective("task-exact", Objective{Text: "\t  resume reads the recorded objective  \n", Provenance: SubmittedUnattended})
	b, err := e.architectureBinding("task-exact")
	if err != nil || b.ObjectiveDigest != "d60db1d22ca040881a6fec3d731ae42a0ecdad7c0c941a6885ac188ff47ea6f2" {
		t.Fatalf("the exact objective bytes were not bound: %q (err %v)", b.ObjectiveDigest, err)
	}
}

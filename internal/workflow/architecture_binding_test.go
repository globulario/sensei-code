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
		// The objective is read through the one accessor every continuation
		// shares, and its absence is returned rather than bound.
		"objective, err := e.recordedObjective(taskID)",
		"e.bindArchitecture(taskID, objective.Text)",
		"e.governedBase(taskID)",
		"graph.Digest",
		// The graph repository is CONFIGURED. Deriving it from the workspace
		// remote would send a Sensei graph commit to a repository that never
		// held it, which is the 422 this binding exists to prevent.
		"e.Config.Sensei.Repository",
		"binding, err := e.architectureBinding(spec.TaskID)",
		"spec.Architecture = binding",
		// Every governed continuation reaches the architect through the one
		// roster walk, and that walk asks for the governed binding.
		"spec.Role == roles.Architect && spec.governed",
	} {
		if !strings.Contains(src, want) {
			t.Errorf("architect binding no longer carries workflow truth %q", want)
		}
	}
	engine, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(engine), "Role: roles.Architect, Agent: cfg, Source: event.SourceArchitect, TaskID: taskID, governed: true,") {
		t.Error("the governed architect roster walk no longer asks for the governed binding")
	}
	bindAt := strings.Index(src, "binding, err := e.architectureBinding(spec.TaskID)")
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

// W3 -- ABSENCE IS A REFUSAL, NOT AN EMPTY OBJECTIVE.
//
// A task whose objective nothing recorded -- no submission in this process and
// no TaskCreated in its durable session record -- must not reach an architect
// turn at all. Before the accessor, objective() answered such a task with empty
// text, architectureBinding bound an empty digest, and the turn went out: the
// local architect answered it, and only the GitHub resolver refused
// (task-1790481145146367848). The refusal is asserted on the roster walk every
// architect turn takes, and the resolver and the architect are both proven
// never to have been asked.
func TestAnAbsentObjectiveRefusesTheArchitectTurnBeforeAnyRunner(t *testing.T) {
	const absent = "task-with-no-recorded-objective"
	for name, setup := range map[string]func(e *Engine){
		// A durable record that holds other tasks but not this one.
		"durable record without it": func(e *Engine) {
			e.Store = storeWithTaskCreated(t, "task-other", "another task")
			e.recordObjective("task-other", Objective{Text: "another task", Provenance: SubmittedUnattended})
		},
		// A durable record that holds this task's TaskCreated with no bytes.
		"empty durable record": func(e *Engine) {
			e.Store = storeWithTaskCreated(t, absent, "")
		},
		// No durable record at all.
		"no durable record": func(e *Engine) {},
		// A submission that recorded no bytes recorded no objective.
		"empty submission": func(e *Engine) {
			e.recordObjective(absent, Objective{Text: "", Provenance: RequestedByHuman})
		},
	} {
		t.Run(name, func(t *testing.T) {
			architect := &scriptedArchitect{turns: []architectTurn{{text: replyDecision}}}
			resolver := &fixedResolver{runner: architect, name: "claude"}
			e := &Engine{SessionID: "s1", Runners: resolver}
			e.Config.Architect.Name, e.Config.Architect.Command = "claude", "true"
			setup(e)

			_, err := e.resolveArchitectureIn(t.Context(), nil, certifiedStart{}, absent, "task", "PROMPT", t.TempDir())
			if err == nil {
				t.Fatal("an architect turn was answered for a task with no recorded objective")
			}
			var typed objectiveAbsent
			for cause := err; cause != nil; {
				if a, ok := cause.(objectiveAbsent); ok {
					typed = a
					break
				}
				u, ok := cause.(interface{ Unwrap() error })
				if !ok {
					break
				}
				cause = u.Unwrap()
			}
			if typed.TaskID != absent {
				t.Fatalf("the refusal is not the typed objective absence for this task: %v", err)
			}
			if len(resolver.specs) != 0 {
				t.Fatalf("the resolver was asked for an architect turn with no objective: %+v", resolver.specs)
			}
			if len(architect.prompts) != 0 {
				t.Fatalf("the architect was run with no objective: %d prompts", len(architect.prompts))
			}
			// Absence is never read as an empty objective by the one reader.
			if o, err := e.recordedObjective(absent); err == nil {
				t.Fatalf("the accessor returned an objective for a task that recorded none: %+v", o)
			}
			if o := e.objective(absent); o != (Objective{}) {
				t.Fatalf("the projection holds an objective the accessor refused: %+v", o)
			}
		})
	}

	// UNREADABLE IS NOT ABSENT. A durable record that cannot be read has shown
	// nothing about whether an objective was recorded; it refuses the turn all
	// the same, under its own type.
	t.Run("unreadable durable record", func(t *testing.T) {
		architect := &scriptedArchitect{turns: []architectTurn{{text: replyDecision}}}
		resolver := &fixedResolver{runner: architect, name: "claude"}
		// A session store nothing was ever appended to has no file to read.
		e := &Engine{SessionID: "s1", Runners: resolver, Store: sessionStore(t)}
		e.Config.Architect.Name, e.Config.Architect.Command = "claude", "true"

		_, err := e.resolveArchitectureIn(t.Context(), nil, certifiedStart{}, absent, "task", "PROMPT", t.TempDir())
		if err == nil {
			t.Fatal("an architect turn was answered over an unreadable durable record")
		}
		if len(resolver.specs) != 0 || len(architect.prompts) != 0 {
			t.Fatalf("the architect turn was asked over an unreadable record: %d specs, %d prompts", len(resolver.specs), len(architect.prompts))
		}
		_, err = e.recordedObjective(absent)
		if _, ok := err.(objectiveUnreadable); !ok {
			t.Fatalf("an unreadable record was not reported as unreadable: %T %v", err, err)
		}
		if _, ok := err.(objectiveAbsent); ok {
			t.Fatal("an unreadable record was reported as a recorded absence")
		}
	})

	// The digest rule, asserted on roles.BindArchitecture directly: the empty
	// objective binds no digest at all.
	if got := (&Engine{SessionID: "s1"}).bindArchitecture("task-empty", "").ObjectiveDigest; got != "" {
		t.Fatalf("BindArchitecture gave the empty objective a digest: %s", got)
	}

	// The digest rule itself: no digest for the empty objective. CheckObjective
	// recomputes roles.BindArchitecture over the text it is given, and refuses
	// exactly when that yields no digest or a different one.
	e := &Engine{SessionID: "s1"}
	const exact = "  bind these exact bytes\n"
	e.recordObjective("task-bound", Objective{Text: exact, Provenance: SubmittedUnattended})
	binding, err := e.architectureBinding("task-bound")
	if err != nil {
		t.Fatalf("a recorded objective was not bound: %v", err)
	}
	// sha256 of exact, computed independently of the code under test; the
	// same literal pins ghwebhook.DigestObjective in proposal_test.go.
	const exactDigest = "697cfa7b1fd6ef72dbfebe973586ed019ad384310554df7e9d97660195b239bb"
	if binding.ObjectiveDigest != exactDigest {
		t.Fatalf("the binding does not digest the exact recorded bytes: %s", binding.ObjectiveDigest)
	}
	// And BindArchitecture itself, on the exact whitespace-bearing bytes and on
	// their normalizations: only the exact bytes carry that identity.
	if got := e.bindArchitecture("task-bound", exact).ObjectiveDigest; got != exactDigest {
		t.Fatalf("BindArchitecture does not digest the exact bytes: %s", got)
	}
	for _, normalized := range []string{strings.TrimSpace(exact), strings.TrimLeft(exact, " "), strings.TrimRight(exact, "\n")} {
		if got := e.bindArchitecture("task-bound", normalized).ObjectiveDigest; got == exactDigest || got == "" {
			t.Fatalf("normalized bytes %q were bound as %q", normalized, got)
		}
	}
	if err := binding.CheckObjective(""); err == nil {
		t.Fatal("the empty objective was given a digest")
	}
	if err := binding.CheckObjective(strings.TrimSpace(exact)); err == nil {
		t.Fatal("trimmed bytes were read as the same objective")
	}
	if err := binding.CheckObjective(exact); err != nil {
		t.Fatalf("the exact bytes no longer name their own binding: %v", err)
	}
}

package workflow

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/roles"
)

// The objective a caller submitted is the objective that is RECORDED, byte for
// byte, and the architecture binding is the SHA-256 of exactly those bytes.
//
// This chain had three separate normalizations in it — the local client, the
// local server, and the governed entry point — each of which looked harmless
// on its own. Together they meant a GitHub objective proposal could store and
// hash one string while the workflow recorded another. The digest would verify
// against nothing anybody had submitted, and the architecture envelope built
// on it would be valid and misleading, which is worse than broken.
//
// Validation is unaffected: an objective that is only whitespace still says
// nothing and is still refused.

// recordingEngine builds an engine whose run() will record the objective and
// then fail out of execute() for want of a read capability. That failure is
// the point: the objective is recorded before execution is attempted, so this
// observes the record without running a workflow.
func recordingEngine(t *testing.T) *Engine {
	t.Helper()
	root := t.TempDir()
	cfg := config.Default()
	cfg.Permissions.ReadRepository = false
	e := New(gitx.Repo{Root: root}, cfg, event.NewBus(), nil, "sess-exact")
	return e
}

func awaitObjective(t *testing.T, e *Engine, taskID string) Objective {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if o := e.objective(taskID); o.Text != "" {
			return o
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("no objective was recorded for %s", taskID)
	return Objective{}
}

func TestTheRecordedObjectiveIsTheExactSubmittedBytes(t *testing.T) {
	exact := []string{
		"  exact objective bytes  \n",
		"\n\nleading and trailing newlines\n\n",
		"\ttab indented\t",
		"trailing space ",
		"unicode  café ✅  padded  ",
	}
	for _, want := range exact {
		e := recordingEngine(t)
		taskID := e.submit(context.Background(), want, SubmittedByLocalOperator, false)

		got := awaitObjective(t, e, taskID)
		if got.Text != want {
			t.Errorf("the governed entry point altered the objective:\n got  %q\n want %q", got.Text, want)
		}
		if got.Provenance != SubmittedByLocalOperator {
			t.Errorf("provenance = %q", got.Provenance)
		}

		// And the architecture binding names those exact bytes -- the property
		// the whole proposal/architecture protocol rests on.
		want64 := roles.BindArchitecture("t", want, "", "").ObjectiveDigest
		got64 := roles.BindArchitecture("t", got.Text, "", "").ObjectiveDigest
		if got64 != want64 {
			t.Errorf("the architecture digest names different bytes than were submitted:\n got  %s\n want %s", got64, want64)
		}
	}
}

// The governed entry point hands run() the bytes it was given. Source-pinned
// alongside the behavioural test above, because the existing proof that run()
// records Objective{Text: task} is itself a source pin -- the two together are
// what make the chain airtight.
func TestTheGovernedEntryPointDoesNotNormalizeTheObjective(t *testing.T) {
	raw, err := os.ReadFile("assisted.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	if !strings.Contains(src, "e.run(ctx, taskID, task, how)") {
		t.Fatal("the governed entry point no longer hands run() the exact submitted bytes")
	}
	if strings.Contains(src, "e.run(ctx, taskID, strings.TrimSpace(task), how)") {
		t.Fatal("the governed entry point trims the objective; the recorded bytes and the digest that names them would differ")
	}
}

// Emptiness is still refused -- validated on the trimmed form, never rewritten.
func TestAnAllWhitespaceObjectiveIsStillRejected(t *testing.T) {
	raw, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `if strings.TrimSpace(task) == "" {`) {
		t.Fatal("execute no longer validates emptiness on the trimmed objective")
	}

	e := recordingEngine(t)
	var terminal []event.Event
	events, unsubscribe := e.Bus.Subscribe(64)
	defer unsubscribe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range events {
			if ev.Kind == event.WorkflowFailed {
				terminal = append(terminal, ev)
				return
			}
		}
	}()

	taskID := e.submit(context.Background(), "   \n\t ", SubmittedByLocalOperator, false)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("an all-whitespace objective neither failed nor was refused")
	}
	if len(terminal) != 1 || !strings.Contains(terminal[0].Summary, "task is empty") {
		t.Fatalf("an all-whitespace objective did not fail as empty: %+v", terminal)
	}
	_ = taskID
}

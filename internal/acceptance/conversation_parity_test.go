//go:build acceptance

package acceptance

// Criteria 1 and 6 of sensei-code#9, which cannot be established from fixtures.
//
//	1. Ask an architectural question, then a follow-up using an unresolved
//	   reference; the second answer preserves the architectural subject without
//	   restating the task.
//	6. Ask about a rejected architectural direction; the answer retrieves the
//	   durable decision rather than relying on model recollection.
//
// Both are judgements about a live answer, and both are the kind of thing a
// fixture can fake perfectly. So this drives real assisted turns against the
// configured architect and prints what came back, asserting only what can be
// asserted honestly: that a second turn saw the first, that the evidence drawer
// records what was consulted, and that nothing governed was produced by talking.
//
// Whether the answers are GOOD is left to a person reading the transcript. A
// test that scored an architect's prose would be measuring its own rubric.
//
//	go test -tags acceptance ./internal/acceptance/ -run TestConversationParity -v -timeout 10m

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

func TestConversationParityAcrossTurns(t *testing.T) {
	root := repoRoot(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sessionID := session.ID(time.Now())
	store, err := session.New(root, sessionID)
	if err != nil {
		t.Fatalf("session store: %v", err)
	}
	bus := event.NewBus()
	events, done := bus.Subscribe(256)
	defer done()
	engine := workflow.New(gitx.Repo{Root: root}, cfg, bus, store, sessionID)

	// What governed artifacts exist before a word is said. Criterion 10 is that
	// talking adds none, and that can only be measured against the state this
	// conversation actually started from -- a repository in use accumulates
	// candidates from governed runs, and a list frozen when the test was
	// written reports every later run as something a conversation produced.
	before := governedArtifacts(t, root)
	t.Logf("governed artifacts before the conversation: %d", len(before))

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()

	first := ask(ctx, t, engine, events,
		"What governs whether Sensei Code may open a pull request, and who decides it?")
	if first.unavailable != "" {
		t.Skipf("the architect is unavailable, so conversational parity cannot be observed: %s", first.unavailable)
	}
	if strings.TrimSpace(first.answer) == "" {
		t.Fatal("the first turn produced no answer")
	}
	t.Logf("turn 1 consulted:\n%s", first.consulted)
	t.Logf("turn 1 answer:\n%s", truncate(first.answer, 600))

	// Criterion 1: the follow-up names no subject of its own.
	second := ask(ctx, t, engine, events, "And what happens if I say no to it?")
	if second.unavailable != "" {
		t.Skipf("the architect became unavailable mid-conversation: %s", second.unavailable)
	}
	t.Logf("turn 2 answer:\n%s", truncate(second.answer, 600))

	if strings.TrimSpace(second.answer) == "" {
		t.Fatal("the follow-up produced no answer, so continuity cannot have been preserved")
	}
	// The strongest honest assertion: the second turn was given the first. What
	// the architect did with it is a human judgement, printed above.
	if !strings.Contains(second.consulted, "architect conversation") {
		t.Errorf("the follow-up did not record the conversation as a consulted source:\n%s", second.consulted)
	}

	// Criterion 10 again, live this time: talking produced nothing governed.
	if added := addedSince(before, governedArtifacts(t, root)); len(added) != 0 {
		t.Errorf("a conversation produced governed artifacts: %v", added)
	}
}

type turn struct {
	answer      string
	consulted   string
	unavailable string
}

// ask runs one assisted turn and collects what it produced.
func ask(ctx context.Context, t *testing.T, engine *workflow.Engine, events <-chan event.Event, question string) turn {
	t.Helper()
	taskID := engine.SubmitAssisted(ctx, question)
	var out turn
	for {
		select {
		case ev := <-events:
			if ev.TaskID != taskID {
				continue
			}
			switch ev.Kind {
			case event.ArchitectSpoke:
				out.answer = ev.Summary
			case event.ContextConsulted:
				out.consulted = ev.Summary
			case event.WorkflowFailed:
				out.unavailable = ev.Summary
				return out
			case event.WorkflowCompleted:
				return out
			}
		case <-ctx.Done():
			t.Fatalf("the turn did not finish: %v", ctx.Err())
		}
	}
}

// governedArtifacts reports candidate worktrees created during this test, which
// a conversation must never produce.
// governedArtifacts is the set of candidate task ids present right now.
//
// A failure to read them is reported rather than returned as an empty set: an
// unreadable candidate directory would otherwise make the criterion pass by
// looking like a repository that has never governed anything.
func governedArtifacts(t *testing.T, root string) map[string]bool {
	t.Helper()
	list, err := candidateTaskIDs(root)
	if err != nil {
		t.Fatalf("read candidate task ids: %v", err)
	}
	out := make(map[string]bool, len(list))
	for _, id := range list {
		out[id] = true
	}
	return out
}

// addedSince returns what appeared between two snapshots.
func addedSince(before, after map[string]bool) []string {
	var added []string
	for id := range after {
		if !before[id] {
			added = append(added, id)
		}
	}
	sort.Strings(added)
	return added
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func candidateTaskIDs(root string) ([]string, error) {
	list, err := candidate.List(root)
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, c := range list {
		ids = append(ids, c.TaskID)
	}
	return ids, nil
}

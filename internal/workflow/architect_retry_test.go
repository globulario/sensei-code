package workflow

// A retry tells the architect what actually happened to its previous attempt.
// An attempt that produced no answer is not an attempt that produced bad JSON.

import (
	"context"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
)

type architectTurn struct {
	text string
	err  error
}

// scriptedArchitect answers each turn from a script and keeps every prompt it
// was sent, so a test reads the retry guidance the architect actually received.
type scriptedArchitect struct {
	turns   []architectTurn
	prompts []string
}

func (s *scriptedArchitect) Run(_ context.Context, req agent.Request, _ func(event.Event)) (agent.Result, error) {
	s.prompts = append(s.prompts, req.Prompt)
	turn := s.turns[len(s.prompts)-1]
	return agent.Result{Text: turn.text}, turn.err
}

const replyDecision = `{"decision":"reply","message":"hello"}`

func resolveWithArchitect(t *testing.T, turns ...architectTurn) (*scriptedArchitect, error) {
	t.Helper()
	architect := &scriptedArchitect{turns: turns}
	e := New(gitx.Repo{Root: t.TempDir()}, config.Default(), event.NewBus(), nil, "sess-1")
	e.Runners = &fixedResolver{runner: architect, name: "claude"}
	_, err := e.resolveArchitectureIn(context.Background(), nil, certifiedStart{}, "task-1", "task", "PROMPT", t.TempDir())
	return architect, err
}

func TestAnArchitectRetryNamesTheFailureItFollows(t *testing.T) {
	cases := []struct {
		name      string
		first     architectTurn
		malformed bool
	}{
		{"a timeout", architectTurn{err: context.DeadlineExceeded}, false},
		{"a blank result", architectTurn{text: " \n\t"}, false},
		{"undecodable output", architectTurn{text: "I think we should proceed."}, true},
		{"a decision of the wrong shape", architectTurn{text: `{"decision":"reply"}`}, true},
		{"an unknown decision", architectTurn{text: `{"decision":"maybe"}`}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			architect, err := resolveWithArchitect(t, tc.first, architectTurn{text: replyDecision})
			if err != nil {
				t.Fatalf("the retry should have resolved: %v", err)
			}
			if len(architect.prompts) != 2 {
				t.Fatalf("architect ran %d times, want 2", len(architect.prompts))
			}
			if architect.prompts[0] != "PROMPT" {
				t.Fatalf("the first attempt carried retry guidance: %q", architect.prompts[0])
			}
			retry := architect.prompts[1]
			accused := strings.Contains(retry, "not valid bounded JSON")
			absent := strings.Contains(retry, "No response was received")
			if tc.malformed && (!accused || absent) {
				t.Fatalf("a malformed response must be named as malformed, got %q", retry)
			}
			if !tc.malformed && (accused || !absent) {
				t.Fatalf("an attempt that produced no answer was described as malformed output: %q", retry)
			}
		})
	}
}

// The guidance follows the latest failure, and the budget stays two attempts.
func TestArchitectRetryBudgetIsUnchanged(t *testing.T) {
	architect, err := resolveWithArchitect(t,
		architectTurn{err: context.DeadlineExceeded},
		architectTurn{text: "not json"},
	)
	if err == nil || !strings.Contains(err.Error(), "could not produce a bounded decision") {
		t.Fatalf("two failed attempts must fail closed, got %v", err)
	}
	if len(architect.prompts) != 2 {
		t.Fatalf("architect ran %d times, want 2", len(architect.prompts))
	}
}

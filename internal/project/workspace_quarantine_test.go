package project

// RULING-368 §9, §11, §14 (P1-P4). The project summary keeps a
// WORKSPACE_QUARANTINED ending among its recent outcomes under its own kind:
// never collapsed to failed, never claimed settled or completed. The existing
// bounds and every prior classification are unchanged.

import (
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
)

const r368Summary = "the invocation ended while the workspace containment of task tq is not established; " +
	"the task is owed a trusted settlement and nothing ordinary continues it"

func r368Events() []event.Event {
	return []event.Event{
		ev("tq", event.TaskCreated, "harden the certify gate"),
		ev("tq", event.WorkflowWorkspaceQuarantined, r368Summary),
	}
}

// P1 (pre-fix control H5). Summarize includes it.
func TestR368P1SummarizeIncludesTheQuarantine(t *testing.T) {
	s := Summarize(r368Events(), nil, DefaultBounds())
	if len(s.Recent) != 1 || s.Recent[0].TaskID != "tq" || s.Recent[0].Task != "harden the certify gate" {
		t.Fatalf("the quarantined invocation is missing from Recent: %+v", s.Recent)
	}
}

// P2. It keeps its own workspace_quarantined meaning.
func TestR368P2TheQuarantineKeepsItsDistinctMeaning(t *testing.T) {
	s := Summarize(r368Events(), nil, DefaultBounds())
	if len(s.Recent) != 1 || s.Recent[0].Result != "workspace_quarantined" {
		t.Fatalf("Recent does not carry workspace_quarantined: %+v", s.Recent)
	}
	if !strings.Contains(s.Render(), "workspace_quarantined  tq") {
		t.Fatalf("the rendering does not name the quarantine:\n%s", s.Render())
	}
}

// P3. No settlement, completion or failure is claimed; the retained candidate
// stays in its own existing projection.
func TestR368P3NoSettlementIsClaimed(t *testing.T) {
	retained := candidate.Identity{TaskID: "tq", Repository: "/repo", BaseSHA: strings.Repeat("a", 40),
		Worktree: "/repo/.worktrees/tq", CreatedAt: time.Now()}
	s := Summarize(r368Events(), []candidate.Identity{retained}, DefaultBounds())
	for _, o := range s.Recent {
		switch o.Result {
		case "completed", "failed", "stopped", "settled", "timed_out":
			t.Fatalf("the quarantine was summarized as %s", o.Result)
		}
	}
	for _, line := range strings.Split(s.Render(), "\n") {
		if strings.Contains(line, "tq") && strings.Contains(strings.ToLower(line), "settled") {
			t.Fatalf("the summary claims a settlement: %q", line)
		}
	}
	if len(s.Retained) != 1 || s.Retained[0].TaskID != "tq" {
		t.Fatalf("the retained-candidate projection changed: %+v", s.Retained)
	}
}

// P4. The bounded summary and every prior classification are unchanged.
func TestR368P4BoundsAndPriorClassificationsAreUnchanged(t *testing.T) {
	prior := map[event.Kind]string{
		event.WorkflowCompleted: "completed", event.WorkflowFailed: "failed", event.WorkflowStopped: "stopped",
		event.WorkflowAwaitingAuthority: "awaiting_authority", event.WorkflowBlockedExternal: "blocked_external",
		event.WorkflowNotConverged: "not_converged", event.WorkflowRestorationRefused: "restoration_refused",
	}
	for kind, want := range prior {
		s := Summarize([]event.Event{ev("t", event.TaskCreated, "x"), ev("t", kind, "y")}, nil, DefaultBounds())
		if len(s.Recent) != 1 || s.Recent[0].Result != want {
			t.Fatalf("%s is now summarized as %+v, want %s", kind, s.Recent, want)
		}
	}
	for _, kind := range []event.Kind{event.Status, event.TaskCreated, event.AuthorityRequired} {
		if s := Summarize([]event.Event{ev("t", kind, "z")}, nil, DefaultBounds()); len(s.Recent) != 0 {
			t.Fatalf("%s became a recent outcome: %+v", kind, s.Recent)
		}
	}
	var events []event.Event
	for i := 0; i < 6; i++ {
		events = append(events, ev("tq", event.WorkflowWorkspaceQuarantined, r368Summary))
	}
	events = append(events, ev("tc", event.WorkflowCompleted, "done"))
	s := Summarize(events, nil, DefaultBounds())
	if len(s.Recent) != 5 || s.Recent[4].Result != "completed" {
		t.Fatalf("the outcome bound changed: %+v", s.Recent)
	}
	if len(s.Dropped) != 1 || s.Dropped[0] != "2 older task outcomes" {
		t.Fatalf("the bound is not disclosed as before: %v", s.Dropped)
	}
}

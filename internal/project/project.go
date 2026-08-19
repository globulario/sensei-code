// Package project assembles the standing context of a repository's work:
// what has been decided, what is unresolved, and what is lying around.
//
// The obvious way to build this is to keep a summary file and update it as the
// project moves. That is the way that fails. A maintained summary is a second
// store of architectural claims, and the moment it disagrees with Sensei or
// with the session record, nothing reports the disagreement — the summary is
// the thing everyone reads, so it wins by being convenient.
//
// So nothing here is stored. The summary is derived, every time, from sources
// that are already durable and already owned by someone: the session record,
// the task state P0.8 writes, and the candidate dispositions. It holds
// references, never copied authority: an invariant id rather than what the
// invariant says, a decision title rather than the decision's force. Anything
// that needs to be true must be retrieved from Sensei at the moment it is
// relied on.
//
//	This is an index. It can be deleted at any time without losing anything, and
//	if that ever stops being true, something has been stored here that should
//	not have been.
package project

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
)

// Outcome is one completed piece of work, by reference.
type Outcome struct {
	TaskID string
	Task   string
	// Result is the terminal transition the record shows, not an interpretation
	// of it: completed, failed, stopped or awaiting-authority.
	Result string
	At     time.Time
}

// Standing is one thing still open, by reference.
type Standing struct {
	TaskID string
	// What is the question or finding as it was recorded. It carries no
	// authority; it is a pointer to where the real answer lives.
	What string
	// Kind distinguishes an unanswered authority question from a reviewer
	// finding, because they are resolved by different people.
	Kind string
}

// Summary is the derived standing context. Every field is a reference into
// something durable; none of it is a claim this package is making.
type Summary struct {
	Recent    []Outcome
	Open      []Standing
	Retained  []candidate.Identity
	Decisions []string
	// Dropped names what the bounds excluded, in the same discipline retrieval
	// uses: a summary that silently truncates reads as a complete picture.
	Dropped []string
}

// Bounds keeps the summary small enough to inject every turn.
type Bounds struct {
	Outcomes  int
	Open      int
	Decisions int
}

// DefaultBounds is deliberately small. The purpose is standing context, not
// project history: a summary that grows with the project eventually costs more
// than the answer it was helping with.
func DefaultBounds() Bounds { return Bounds{Outcomes: 5, Open: 5, Decisions: 5} }

// Summarize derives the standing context from durable sources.
//
// It takes the events rather than reading them so the caller controls scope,
// and candidates by value so this package never touches the filesystem — the
// less this can reach, the less it can quietly become.
func Summarize(events []event.Event, candidates []candidate.Identity, b Bounds) Summary {
	if b.Outcomes <= 0 {
		b = DefaultBounds()
	}
	var s Summary

	tasks := map[string]string{}
	unanswered := map[string]string{}
	for _, e := range events {
		switch e.Kind {
		case event.TaskCreated:
			tasks[e.TaskID] = e.Summary
		case event.AuthorityRequired:
			unanswered[e.TaskID] = e.Summary
		case event.AuthorityResolved:
			delete(unanswered, e.TaskID)
		case event.DecisionRecorded:
			s.Decisions = append(s.Decisions, e.Summary)
		case event.WorkflowCompleted, event.WorkflowFailed, event.WorkflowStopped, event.WorkflowAwaitingAuthority:
			s.Recent = append(s.Recent, Outcome{
				TaskID: e.TaskID, Task: tasks[e.TaskID],
				Result: strings.TrimPrefix(string(e.Kind), "workflow."), At: e.Time,
			})
		}
	}
	for taskID, question := range unanswered {
		s.Open = append(s.Open, Standing{TaskID: taskID, What: question, Kind: "authority question"})
	}
	sort.Slice(s.Open, func(a, b int) bool { return s.Open[a].TaskID < s.Open[b].TaskID })

	// A candidate retained on purpose is standing context: it is work that
	// exists, is unpublished, and is somebody's decision to make. A disposed one
	// is not — it has been resolved, and resolved things are history.
	for _, c := range candidates {
		if c.Unresolved() || (c.Resolution != nil && c.Resolution.Disposition.Keeps()) {
			s.Retained = append(s.Retained, c)
		}
	}

	s.Recent, s.Dropped = tail(s.Recent, b.Outcomes, s.Dropped, "task outcomes")
	s.Open, s.Dropped = tailStanding(s.Open, b.Open, s.Dropped)
	s.Decisions, s.Dropped = tailStrings(s.Decisions, b.Decisions, s.Dropped, "recorded decisions")
	return s
}

// Render lays the summary out for a prompt, stating what it is and is not.
func (s Summary) Render() string {
	if len(s.Recent) == 0 && len(s.Open) == 0 && len(s.Retained) == 0 && len(s.Decisions) == 0 {
		return "(no standing project context: nothing has been run, decided or left open in this repository's record)"
	}
	var b strings.Builder
	b.WriteString("These are references into durable records, not claims. Anything that has to\n")
	b.WriteString("be true must be retrieved from Sensei at the moment you rely on it.\n")
	if len(s.Recent) != 0 {
		b.WriteString("\nRecent task outcomes:\n")
		for _, o := range s.Recent {
			fmt.Fprintf(&b, "  %s  %s — %s\n", o.Result, o.TaskID, firstLine(o.Task))
		}
	}
	if len(s.Open) != 0 {
		b.WriteString("\nStill open:\n")
		for _, o := range s.Open {
			fmt.Fprintf(&b, "  %s (%s) — %s\n", o.Kind, o.TaskID, firstLine(o.What))
		}
	}
	if len(s.Retained) != 0 {
		b.WriteString("\nCandidates that still exist:\n")
		for _, c := range s.Retained {
			state := "unresolved — nobody has decided what became of it"
			if c.Resolution != nil {
				state = c.Resolution.Summary()
			}
			fmt.Fprintf(&b, "  %s — %s\n", c.TaskID, state)
		}
	}
	if len(s.Decisions) != 0 {
		b.WriteString("\nDecisions recorded for review:\n")
		for _, d := range s.Decisions {
			fmt.Fprintf(&b, "  %s\n", firstLine(d))
		}
	}
	if len(s.Dropped) != 0 {
		fmt.Fprintf(&b, "\nNot shown, bounded for size: %s\n", strings.Join(s.Dropped, "; "))
	}
	return strings.TrimRight(b.String(), "\n")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	if len(s) > 120 {
		return s[:117] + "..."
	}
	if s == "" {
		return "(unrecorded)"
	}
	return s
}

func tail(in []Outcome, n int, dropped []string, what string) ([]Outcome, []string) {
	if len(in) <= n {
		return in, dropped
	}
	return in[len(in)-n:], append(dropped, fmt.Sprintf("%d older %s", len(in)-n, what))
}

func tailStanding(in []Standing, n int, dropped []string) ([]Standing, []string) {
	if len(in) <= n {
		return in, dropped
	}
	return in[:n], append(dropped, fmt.Sprintf("%d further open items", len(in)-n))
}

func tailStrings(in []string, n int, dropped []string, what string) ([]string, []string) {
	if len(in) <= n {
		return in, dropped
	}
	return in[len(in)-n:], append(dropped, fmt.Sprintf("%d older %s", len(in)-n, what))
}

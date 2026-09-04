package control

import (
	"path/filepath"

	"github.com/globulario/sensei-code/internal/assist"
	"github.com/globulario/sensei-code/internal/taskstate"
)

// Everything a remote role holder reads comes from the canonical record and is
// typed on the way out.
//
// The rule this file exists to keep is section 4.2's, applied to a new reader:
// an empty panel is not an answer. "This task recorded no architectural
// contract" and "there is no canonical record of this task at all" are
// different facts, and a remote architect that cannot tell them apart will
// reason confidently about a task it has no evidence for. So every field
// carries a state, and the absence of a record propagates to every field it
// would have populated rather than rendering as a set of empty strings.
//
// assist.Presence is reused rather than restated. A second absence vocabulary
// would drift from the first, and the two would disagree about what "empty"
// means in exactly the situation both exist to describe.

// recordSource names where a fact was read from, so a reader can go and look.
func recordSource(taskID string) string {
	return filepath.Join(".sensei-code", "tasks", taskID+".json")
}

// noRecord is what every field becomes when the task has no canonical state.
func noRecord(taskID string) assist.Observation {
	return assist.Observation{
		State:  assist.Absent,
		Source: recordSource(taskID),
		Reason: "this repository holds no canonical record for that task, so nothing is known about it either way",
	}
}

// present is a fact the record actually holds.
func present(taskID string, structured map[string]any, text string) assist.Observation {
	return assist.Observation{State: assist.Present, Source: recordSource(taskID), Structured: structured, Text: text}
}

// emptyProven is the record answering, and the answer being nothing.
//
// Distinct from Absent by exactly the distinction that matters: the record
// exists, it was read, and it holds no such fact. A reader may act on that. It
// may not act on Absent.
func emptyProven(taskID, reason string) assist.Observation {
	return assist.Observation{State: assist.EmptyProven, Source: recordSource(taskID), Reason: reason}
}

// taskView renders one task's canonical state for a remote reader.
//
// found is the Load result rather than something inferred from the contents: a
// zero State and an absent file are indistinguishable by value, and telling
// them apart is the whole job here.
func taskView(taskID string, state taskstate.State, found bool) map[string]any {
	view := map[string]any{"task": taskID}
	if !found {
		gone := noRecord(taskID)
		view["record"] = gone
		for _, field := range []string{
			"workflow_state", "base", "candidate", "contract",
			"authority", "open_findings", "evidence", "workers", "graph_generation",
		} {
			view[field] = gone
		}
		return view
	}

	view["record"] = present(taskID, map[string]any{
		"version": state.Version, "session": state.SessionID, "task_text": state.Task,
		"domain": state.Domain, "updated_at": state.UpdatedAt,
	}, "")

	view["workflow_state"] = present(taskID, map[string]any{"phase": string(state.Phase)}, string(state.Phase))

	if state.BaseSHA == "" {
		view["base"] = emptyProven(taskID, "the record holds no base commit for this task yet")
	} else {
		view["base"] = present(taskID, map[string]any{"base_sha": state.BaseSHA}, state.BaseSHA)
	}

	if state.Worktree == "" && state.Branch == "" && len(state.Evidence.ChangedPaths) == 0 {
		view["candidate"] = emptyProven(taskID, "no candidate has been recorded for this task")
	} else {
		view["candidate"] = present(taskID, map[string]any{
			"worktree": state.Worktree, "branch": state.Branch,
			"changed_paths": state.Evidence.ChangedPaths,
		}, "")
	}

	if state.Contract.Plan == "" && len(state.Contract.Steps) == 0 && len(state.Contract.Files) == 0 {
		view["contract"] = emptyProven(taskID, "no architectural contract has been recorded for this task")
	} else {
		view["contract"] = present(taskID, map[string]any{
			"plan": state.Contract.Plan, "rationale": state.Contract.Rationale,
			"steps": state.Contract.Steps, "files": state.Contract.Files,
			"invariants": state.Contract.Invariants, "consequences": state.Contract.Consequences,
		}, state.Contract.Plan)
	}

	if len(state.Authority) == 0 {
		view["authority"] = emptyProven(taskID, "no authority decision has been recorded for this task")
	} else {
		decisions := make([]map[string]any, 0, len(state.Authority))
		for _, d := range state.Authority {
			decisions = append(decisions, map[string]any{
				"question": d.Question, "condition": d.Condition, "chosen": d.Chosen,
				// Durable says whether Sensei holds this answer. A decision that
				// binds this run and is not project knowledge is a different
				// fact from one that is, and flattening them would let a reader
				// treat a local answer as settled architecture.
				"durable": d.Durable, "decided_at": d.DecidedAt,
			})
		}
		view["authority"] = present(taskID, map[string]any{"decisions": decisions}, "")
	}

	if len(state.Open) == 0 {
		view["open_findings"] = emptyProven(taskID, "the record holds no unanswered findings for this task")
	} else {
		findings := make([]map[string]any, 0, len(state.Open))
		for _, f := range state.Open {
			findings = append(findings, map[string]any{"source": f.Source, "detail": f.Detail})
		}
		view["open_findings"] = present(taskID, map[string]any{"findings": findings}, "")
	}

	if state.Evidence.DiffBytes == 0 && state.Evidence.ReportBytes == 0 &&
		state.Evidence.AuditVerdict == "" && len(state.Evidence.RequiredTests) == 0 {
		view["evidence"] = emptyProven(taskID, "no candidate evidence has been recorded for this task")
	} else {
		view["evidence"] = present(taskID, map[string]any{
			"diff_bytes": state.Evidence.DiffBytes, "report_bytes": state.Evidence.ReportBytes,
			"audit_verdict": state.Evidence.AuditVerdict, "audit_detail": state.Evidence.AuditDetail,
			"required_tests": state.Evidence.RequiredTests,
		}, state.Evidence.AuditVerdict)
	}

	if len(state.Workers) == 0 {
		view["workers"] = emptyProven(taskID, "no worker has been recorded against this task")
	} else {
		view["workers"] = present(taskID, map[string]any{"workers": state.Workers}, "")
	}

	// The graph generation the record's Sensei facts were read at — and NOT a
	// statement that they are still current. Answering that means asking the
	// live graph, which this surface never queries, so the freshness question
	// is reported unavailable rather than silently answered "fine".
	// A stale generation presented as a current one is the failure that makes
	// injected context worse than no context.
	if state.GraphBuildCommit == "" {
		view["graph_generation"] = emptyProven(taskID, "the record does not name the graph generation its Sensei facts came from")
	} else {
		view["graph_generation"] = assist.Observation{
			State: assist.Unavailable, Source: recordSource(taskID),
			Structured: map[string]any{"recorded_graph_build_commit": state.GraphBuildCommit, "observed_at": state.ObservedAt},
			Reason:     "this is the generation the record was written at; whether it is still current was not checked, because this surface does not query the live graph",
		}
	}

	return view
}

// workView is one line per task for get_work: what it is and where it stands,
// read from the same canonical record inspect_task reads.
func workView(taskID string, state taskstate.State, found bool) map[string]any {
	if !found {
		return map[string]any{"task": taskID, "record": noRecord(taskID)}
	}
	return map[string]any{
		"task":      taskID,
		"phase":     string(state.Phase),
		"task_text": state.Task,
		"record":    assist.Observation{State: assist.Present, Source: recordSource(taskID)},
	}
}

// unreadable is a record that exists and could not be read.
//
// Deliberately not Absent. Absent says nothing is known either way and is safe
// to act on as "this task has no history"; unreadable says we could not find
// out, which is not safe to act on at all. Collapsing the two is how a remote
// architect reasons confidently about a task whose evidence it never saw.
func unreadable(taskID string, cause error) assist.Observation {
	return assist.Observation{
		State:  assist.Unavailable,
		Source: recordSource(taskID),
		Reason: "the canonical record for this task could not be read: " + cause.Error(),
	}
}

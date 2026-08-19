package event

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"time"
)

type Source string

const (
	SourceSystem    Source = "system"
	SourceUser      Source = "user"
	SourceSensei    Source = "sensei"
	SourceArchitect Source = "architect"
	SourceReviewer  Source = "reviewer"
	SourceClaude    Source = "claude"
	SourceCodex     Source = "codex"
	SourceGit       Source = "git"
	SourceTests     Source = "tests"
)

type Kind string

const (
	TaskCreated Kind = "task.created"
	// ModeSelected records the mode a task runs in and why, so the UI reports
	// what a task actually is rather than what configuration might imply.
	ModeSelected Kind = "mode.selected"
	Status       Kind = "status"
	// ArchitectSpoke carries the architect's own words to the human. It is
	// conversation, not activity, and is always shown.
	ArchitectSpoke Kind = "architect.spoke"
	// PlanProposed carries a bounded plan awaiting the human's go-ahead.
	PlanProposed Kind = "plan.proposed"
	// DecisionRecorded reports whether the accepted plan reached Sensei's
	// decisions surface, including when it did not.
	DecisionRecorded Kind = "decision.recorded"
	// ChangeReported carries the architectural change report for a candidate.
	ChangeReported Kind = "change.reported"
	// PullRequestOpened reports a published candidate. It is never a merge.
	PullRequestOpened Kind = "pull_request.opened"
	// GuidanceDelivered reports that a worker cycle read the human's guidance.
	GuidanceDelivered Kind = "guidance.delivered"
	Output            Kind = "output"
	SenseiResult      Kind = "sensei.result"
	// ContextConsulted is the evidence drawer for one turn: which sources were
	// consulted and what state each was in. It is emitted even when everything
	// was fine, because a provenance surface that only appears on failure is
	// one nobody learns to read.
	ContextConsulted Kind = "context.consulted"
	AgentStarted     Kind = "agent.started"
	AgentFinished    Kind = "agent.finished"
	CandidateChanged Kind = "candidate.changed"
	// CandidateResolved records a candidate's terminal disposition: what became
	// of the worktree and branch, and why. A candidate with no such event is
	// one nobody resolved.
	CandidateResolved Kind = "candidate.resolved"
	CandidateAudited  Kind = "candidate.audited"
	// ValidationRun records checks the broker executed against a candidate.
	ValidationRun     Kind = "validation.run"
	AuthorityRequired Kind = "authority.required"
	AuthorityResolved Kind = "authority.resolved"
	WorkflowFailed    Kind = "workflow.failed"
	WorkflowCompleted Kind = "workflow.completed"
	// WorkflowStopped is the human withdrawing from a running task. It is its
	// own transition rather than a flavour of failure: a stop proves nothing
	// about the work, and the candidate it leaves behind is resumable, where a
	// failed one is not.
	WorkflowStopped Kind = "workflow.stopped"
	// WorkflowAwaitingAuthority is a Level-3 question the human declined to
	// answer now. It is not a stop and not an answer.
	//
	// Once the router establishes a genuine authority condition there is no
	// architect-authorized continuation left, so deferring cannot mean "no" —
	// that would let a keystroke manufacture a third answer the human never
	// gave. What it means is that the question stands, unchanged, until they
	// choose one of its options. The payload carries the question verbatim so
	// resuming asks the same one rather than re-deriving it from a graph that
	// may have moved.
	WorkflowAwaitingAuthority Kind = "workflow.awaiting_authority"
)

type Event struct {
	ID        string          `json:"id"`
	Time      time.Time       `json:"time"`
	SessionID string          `json:"session_id"`
	TaskID    string          `json:"task_id,omitempty"`
	Source    Source          `json:"source"`
	Kind      Kind            `json:"kind"`
	Summary   string          `json:"summary,omitempty"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

func New(sessionID, taskID string, source Source, kind Kind, summary string, payload any) Event {
	var raw json.RawMessage
	if payload != nil {
		raw, _ = json.Marshal(payload)
	}
	now := time.Now().UTC()
	var entropy [4]byte
	_, _ = rand.Read(entropy[:])
	id := now.Format("20060102T150405.000000000Z") + "-" + hex.EncodeToString(entropy[:])
	return Event{ID: id, Time: now, SessionID: sessionID, TaskID: taskID, Source: source, Kind: kind, Summary: summary, Payload: raw}
}

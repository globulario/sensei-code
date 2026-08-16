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
	Status      Kind = "status"
	// ArchitectSpoke carries the architect's own words to the human. It is
	// conversation, not activity, and is always shown.
	ArchitectSpoke Kind = "architect.spoke"
	// PlanProposed carries a bounded plan awaiting the human's go-ahead.
	PlanProposed Kind = "plan.proposed"
	// DecisionRecorded reports whether the accepted plan reached Sensei's
	// decisions surface, including when it did not.
	DecisionRecorded Kind = "decision.recorded"
	// ChangeReported carries the architectural change report for a candidate.
	ChangeReported    Kind = "change.reported"
	Output            Kind = "output"
	SenseiResult      Kind = "sensei.result"
	AgentStarted      Kind = "agent.started"
	AgentFinished     Kind = "agent.finished"
	CandidateChanged  Kind = "candidate.changed"
	CandidateAudited  Kind = "candidate.audited"
	AuthorityRequired Kind = "authority.required"
	AuthorityResolved Kind = "authority.resolved"
	WorkflowFailed    Kind = "workflow.failed"
	WorkflowCompleted Kind = "workflow.completed"
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

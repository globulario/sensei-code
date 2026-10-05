package event

import (
	"crypto/rand"
	"crypto/sha256"
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
	// RoleAssigned records which provider took which semantic role, and whether
	// its provider session inherited anything.
	//
	// It is a separate event from agent.started because it answers a different
	// question. agent.started says a process ran; this says a job was given to
	// somebody, under a stated independence rule, chosen from a bounded set. A
	// run whose reviewer silently became the same provider that implemented the
	// candidate is invisible in the first and unmissable in the second.
	RoleAssigned Kind = "agent.role.assigned"
	// HandoffCreated records a typed WorkerHandoffPacket passing between
	// implementers. The candidate does not restart, and this is where that is
	// written down.
	HandoffCreated Kind = "handoff.created"
	// ReviewStarted, ReviewFinding and ReviewCompleted reconstruct one
	// independent review. Findings are emitted individually because a review
	// summarised into a single line loses the one thing a worker can act on.
	ReviewStarted   Kind = "review.started"
	ReviewFinding   Kind = "review.finding"
	ReviewCompleted Kind = "review.completed"
	// ReviewContradiction records two independent verdicts on the same
	// candidate digest and the same executed evidence that disagree: one did
	// not accept, the next accepted, and nothing changed but the reviewer. It
	// is emitted by the engine, never by a reviewer, and it is always followed
	// by an adjudication or a failure -- never by a silent conclusion.
	ReviewContradiction Kind = "review.contradiction"
	// ArchitectReconciliation is the orchestration receipt for a disagreement:
	// what was disputed, what evidence decided it, and what is still open. It
	// explains why the loop branched. It is never a governance receipt.
	ArchitectReconciliation Kind = "architect.reconciliation"
	CandidateChanged        Kind = "candidate.changed"
	// CandidateResolved records a candidate's terminal disposition: what became
	// of the worktree and branch, and why. A candidate with no such event is
	// one nobody resolved.
	CandidateResolved Kind = "candidate.resolved"
	CandidateAudited  Kind = "candidate.audited"
	// CandidateArtifactExcluded records a path the candidate boundary refused:
	// a build output or oversized file the plan did not name (#89).
	CandidateArtifactExcluded Kind = "candidate.artifact_excluded"
	// CandidateNotAuditable is a STRUCTURAL terminal: the candidate cannot be
	// audited for a reason no reviewer or further implementor can change, and
	// the run ends with that reason rather than consuming another executor.
	CandidateNotAuditable Kind = "candidate.not_auditable"
	// InspectionReported carries the findings of a read-only plan. Such a run
	// produces no diff, so without this its only result would be whatever a
	// reader happened to scroll past in the transcript.
	InspectionReported Kind = "inspection.reported"
	// RoutineClassified is the Level-1 dark run: whether this change would have
	// qualified as routine, and which condition stopped it if not. During
	// stage 1 it grants nothing and skips nothing — it is a measurement,
	// emitted so the tier can be judged from real runs rather than intuition.
	RoutineClassified Kind = "routine.classified"
	// WorkflowTimedOut ends an invocation whose execution budget expired.
	//
	// Distinct from WorkflowStopped: a human withdrawing and a deadline
	// expiring are different evidence about why work ended. The TASK may still
	// be resumable; the INVOCATION is over either way, and it owes an account.
	WorkflowTimedOut Kind = "workflow.timed_out"
	// RunReceipt is the governed run's own account of itself: the identities
	// it measured, the candidate it produced, who reviewed it, and how it
	// ended, in one typed record whose completeness is checkable without
	// reconstructing the run from the rest of this stream.
	//
	// It reports evidence and grants nothing. Admission is decided outside.
	RunReceipt Kind = "run.receipt"
	// ValidationRun records checks the broker executed against a candidate.
	ValidationRun     Kind = "validation.run"
	AuthorityRequired Kind = "authority.required"
	AuthorityResolved Kind = "authority.resolved"
	WorkflowFailed    Kind = "workflow.failed"
	WorkflowCompleted Kind = "workflow.completed"
	// WorkflowObserved is a read-only run that finished by reporting findings.
	//
	// Distinct from WorkflowCompleted, which means a change was admitted. An
	// audit that finds three real defects and admits nothing has succeeded, and
	// a control plane whose only successful ending is an admitted change cannot
	// say so -- it has to report the audit as awaiting a human instead.
	WorkflowObserved Kind = "workflow.observed"
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
	// WorkflowAwaitingReview is a candidate that stands, was accepted by a
	// reviewer, and still owes the independent review its task requires.
	//
	// Its own kind rather than a flavour of failure, for the same reason
	// WorkflowStopped is: nothing failed. The worker converged, the reviewer
	// judged, and one obligation was left undischarged because the party that
	// judged could not be shown to be independent of the work. Emitting this as
	// WorkflowFailed made the task terminal to FindInterrupted, so a run whose
	// receipt said "the obligation remains" and whose task record said
	// "reviewing" was, to the continuity layer, done -- three layers telling the
	// truth and the fourth quietly disagreeing.
	//
	// Terminal for the INVOCATION and not for the TASK. The candidate is
	// preserved and the next invocation resumes at the review boundary rather
	// than sending a worker back to fix code nobody objected to.
	WorkflowAwaitingReview Kind = "workflow.awaiting_review"
	// WorkflowBlockedExternal is a role turn the task is owed and a provider
	// PROVED it could not serve -- out of quota, for example -- with no
	// authorized alternate able to serve it instead.
	//
	// Terminal for the INVOCATION and not for the TASK, like
	// WorkflowAwaitingReview. The first dogfood run (2026-09-18) ended FAILED
	// when its architect ran out of quota, which FindInterrupted reads as done:
	// the only way on was a new task. Nothing about the objective had failed.
	// The payload is the block itself (workflow.ExternalBlock), so a resume
	// retries the same turn of the same task.
	WorkflowBlockedExternal Kind = "workflow.blocked_external"
	// WorkflowNotConverged is a task whose every configured implementer spent
	// its review cycles while the independent reviewer still required revision.
	//
	// Terminal for the INVOCATION and not for the TASK. Emitted as
	// WorkflowFailed it was final to FindInterrupted while the candidate record
	// called the same work resumable (DF-6, 2026-09-19). The payload is
	// workflow.NotConverged; a resume has the architect re-plan the candidate.
	WorkflowNotConverged Kind = "workflow.not_converged"
	// WorkflowRestorationRefused is a resumed invocation that could not
	// RE-ESTABLISH the authority its record holds, and refused rather than
	// execute.
	//
	// Terminal for the INVOCATION and not for the TASK, like
	// WorkflowBlockedExternal. Emitted as WorkflowFailed it destroyed the very
	// obligation the refusal was protecting: task-1789960053774525922
	// (2026-09-21) left the resumable set permanently because a restoration
	// check refused it. The payload is workflow.RestorationRefusal, which names
	// the authority instrument whose binding could not be read or verified, so
	// the refusal can be told apart from a DERIVED mismatch it is not.
	WorkflowRestorationRefused Kind = "workflow.restoration_refused"
	// WorkflowBaseMovedRefused is an invocation that refused to continue a
	// task whose recorded candidate base is no longer the repository's HEAD
	// (candidate.ErrBaseMoved), and executed nothing.
	//
	// Terminal for the INVOCATION and not for the TASK. The refusal is right --
	// a candidate's base is immutable -- and its ending was wrong: emitted as
	// WorkflowFailed it removed task-1790489127599728062 (2026-09-27) from the
	// resumable set after nothing had run. The summary is the refusal's reason.
	WorkflowBaseMovedRefused Kind = "workflow.base_moved_refused"
	// WorkflowDirtyCanonicalRefused is an invocation that refused to continue
	// because the canonical checkout is not clean (candidate.ErrDirtyCanonical),
	// and executed nothing. Terminal for the INVOCATION and not for the TASK,
	// for the same reason as WorkflowBaseMovedRefused.
	WorkflowDirtyCanonicalRefused Kind = "workflow.dirty_canonical_refused"
	// WorkflowPlanAdmissionRefused is an invocation whose architect, returned
	// a typed plan-admission refusal of its plan, answered with the materially
	// same refused plan -- the same canonical PlanAttemptID, refused under the
	// same canonical refusal identity, with no new governed evidence. No
	// implementer started, and no third identical plan is requested.
	//
	// Terminal for the INVOCATION and not for the TASK: a refusal of a plan
	// establishes that the plan cannot proceed, not that the objective is
	// impossible (objective 61). The payload is workflow.PlanAdmissionRefused.
	WorkflowPlanAdmissionRefused Kind = "workflow.plan_admission_refused"
	// ProspectiveGranted records the prospective authorization the router read
	// for a task's declared new surfaces (sensei#312): the covering surface,
	// the pinned world and the facts read from it. The payload is the record
	// the post-creation inspection checks against, carried verbatim so a
	// resumed task inspects against the facts that authorized it rather than
	// against whatever is readable after a restart.
	ProspectiveGranted Kind = "prospective.granted"
	// TestEditGranted records the existing-test edit authority the router
	// read (M2.2): operational, never coverage. Restored on resume and
	// inspected against after the candidate is produced.
	TestEditGranted Kind = "testedit.granted"
	// PlanAttemptStarted records one routed architecture plan as a plan
	// attempt BEFORE any plan-local authority is derived under it: its
	// canonical PlanAttemptID and the complete plan payload that identity was
	// derived from. It is not the operative plan; PlanProposed carrying the
	// same id is the transition that makes it operative.
	PlanAttemptStarted Kind = "plan.attempt.started"
	// PlanAttemptRefused records that plan admission refused one plan attempt,
	// bound to its PlanAttemptID. A refused attempt never becomes operative,
	// and its refusal never attaches to any other attempt.
	PlanAttemptRefused Kind = "plan.attempt.refused"
	// CheckpointPrepared and CheckpointCommitted are the two records of one
	// durable incomplete-obligation checkpoint (70B1). PREPARED binds a
	// checkpoint id to its payload digest and ReplayDigest before the payload
	// is written; COMMITTED, appended only after the written payload was read
	// back and replayed to the same ReplayDigest, makes it authoritative. A
	// prepared checkpoint with no matching commit is not a checkpoint. Neither
	// is a run ending.
	CheckpointPrepared  Kind = "checkpoint.prepared"
	CheckpointCommitted Kind = "checkpoint.committed"
)

// Terminality is what a governed run's ending ends: the INVOCATION only, or
// the TASK as well.
type Terminality string

const (
	// InvocationTerminal ends one process's attempt and leaves the task owing
	// something: it stays resumable.
	InvocationTerminal Terminality = "invocation"
	// TaskTerminal ends the task. Nothing is left to resume.
	TaskTerminal Terminality = "task"
)

// RunTerminality is the ONE classification of every governed run ending.
//
// It is closed and read by membership: a kind that is not a run ending --
// unknown, or a known nonterminal such as Status -- reports false rather than
// a default, so a new ending cannot quietly become either kind of terminal.
// The task-terminal set is exactly WorkflowCompleted, WorkflowFailed and
// WorkflowObserved: a change was admitted, the work failed, or a read-only run
// reported what it found. Every other ending leaves the task resumable.
func RunTerminality(k Kind) (Terminality, bool) {
	switch k {
	case WorkflowCompleted, WorkflowFailed, WorkflowObserved:
		return TaskTerminal, true
	case WorkflowStopped, WorkflowTimedOut, WorkflowAwaitingAuthority, WorkflowAwaitingReview,
		WorkflowBlockedExternal, WorkflowNotConverged, WorkflowRestorationRefused,
		WorkflowBaseMovedRefused, WorkflowDirtyCanonicalRefused, WorkflowPlanAdmissionRefused:
		return InvocationTerminal, true
	}
	return "", false
}

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

// ObjectiveDigest is the one identity rule for an objective: the SHA-256 of its
// exact bytes, lowercase hex.
//
// The bytes are neither trimmed nor normalized: two objectives that render
// alike but differ in bytes are two inputs. An empty objective has NO digest,
// so an absent record cannot masquerade as the valid SHA-256 of "". It lives
// here, beside TaskCreated, because every holder of an objective -- the
// workflow's architecture binding and the webhook's proposal store alike --
// already depends on this package and on nothing else in common.
func ObjectiveDigest(objective string) string {
	if objective == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(objective))
	return hex.EncodeToString(sum[:])
}

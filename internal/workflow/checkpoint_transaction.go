package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/session"
)

const (
	checkpointVersion            = "sensei-code/incomplete-obligation-checkpoint/v1"
	maxCheckpointPersistAttempts = 3
)

type checkpointStatus string

const (
	checkpointLive      checkpointStatus = "live"
	checkpointBlocked   checkpointStatus = "blocked"
	checkpointExhausted checkpointStatus = "exhausted"
	checkpointRetired   checkpointStatus = "retired"
)

func (s checkpointStatus) valid() bool {
	switch s {
	case checkpointLive, checkpointBlocked, checkpointExhausted, checkpointRetired:
		return true
	}
	return false
}

type retirementReason string

const (
	retiredCompleted             retirementReason = "completed"
	retiredSupersededPlanAttempt retirementReason = "superseded_plan_attempt"
	retiredReplacedCheckpoint    retirementReason = "replaced_checkpoint"
)

func (r retirementReason) valid() bool {
	switch r {
	case retiredCompleted, retiredSupersededPlanAttempt, retiredReplacedCheckpoint:
		return true
	}
	return false
}

// replayCapsule persists canonical owner inputs. No Owed, Retained, Attempts,
// candidate-state conclusion, or validity bit is authority in this payload.
type replayCapsule struct {
	Version              string            `json:"version"`
	TaskID               string            `json:"task_id"`
	PlanAttemptID        string            `json:"plan_attempt_id"`
	Objective            string            `json:"objective"`
	Status               checkpointStatus  `json:"status"`
	Retirement           retirementReason  `json:"retirement_reason,omitempty"`
	PreviousCheckpointID string            `json:"previous_checkpoint_id,omitempty"`
	Obligation           *obligationInputs `json:"obligation,omitempty"`
	Candidate            *candidateReplay  `json:"candidate,omitempty"`
}

type obligationState struct {
	TaskID          string                     `json:"task_id"`
	PlanAttemptID   string                     `json:"plan_attempt_id"`
	Cycle           int                        `json:"review_cycle"`
	ReviewAttempt   int                        `json:"review_attempt"`
	CandidateDigest string                     `json:"candidate_digest"`
	CandidateTree   string                     `json:"candidate_tree"`
	Findings        []roles.Finding            `json:"findings"`
	Retained        map[string]findingResponse `json:"retained"`
	Owed            []string                   `json:"owed"`
	OpenOperations  []string                   `json:"open_operations"`
	Attempts        int                        `json:"attempts"`
	Route           cycleRoute                 `json:"route"`
	Continuing      bool                       `json:"continuing"`
	Continuations   []string                   `json:"continuations"`
}

type replayedObligation struct {
	Version              string                 `json:"version"`
	TaskID               string                 `json:"task_id"`
	PlanAttemptID        string                 `json:"plan_attempt_id"`
	Objective            string                 `json:"objective"`
	Status               checkpointStatus       `json:"status"`
	Retirement           retirementReason       `json:"retirement_reason,omitempty"`
	PreviousCheckpointID string                 `json:"previous_checkpoint_id,omitempty"`
	Cycle                *obligationState       `json:"cycle,omitempty"`
	Incomplete           *ImplementerIncomplete `json:"incomplete,omitempty"`
	Candidate            *candidateSemantic     `json:"candidate,omitempty"`
	live                 *cycleCompletion
}

func (r replayedObligation) Live() (*cycleCompletion, bool) {
	if r.live == nil {
		return nil, false
	}
	return r.live.clone(), true
}

func (r replayedObligation) digest() string {
	raw, err := json.Marshal(r)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

func semanticObligation(taskID, planAttemptID, objective string, status checkpointStatus, reason retirementReason, previous string,
	c *cycleCompletion, candidate *candidateObservation) replayedObligation {
	r := replayedObligation{
		Version: checkpointVersion, TaskID: taskID, PlanAttemptID: planAttemptID, Objective: objective,
		Status: status, Retirement: reason, PreviousCheckpointID: previous,
	}
	if c != nil {
		retained := make(map[string]findingResponse, len(c.Retained))
		for k, v := range c.Retained {
			v.Paths = append([]string(nil), v.Paths...)
			retained[k] = v
		}
		r.Cycle = &obligationState{
			TaskID: c.TaskID, PlanAttemptID: c.PlanAttemptID, Cycle: c.Cycle, ReviewAttempt: c.ReviewAttempt,
			CandidateDigest: c.CandidateDigest, CandidateTree: c.CandidateTree,
			Findings: append([]roles.Finding(nil), c.Findings...), Retained: retained,
			Owed: nonNilStrings(c.Owed()), OpenOperations: nonNilStrings(append([]string(nil), c.OpenOperations...)),
			Attempts: c.Attempts, Route: c.Route, Continuing: c.Continuing,
			Continuations: nonNilStrings(append([]string(nil), c.Continuations...)),
		}
		if status == checkpointExhausted {
			r.Incomplete = c.incomplete(c.OpenOperations, "")
			r.Incomplete.Owed = nonNilStrings(r.Incomplete.Owed)
			r.Incomplete.Retained = nonNilStrings(r.Incomplete.Retained)
			r.Incomplete.OpenOperations = nonNilStrings(r.Incomplete.OpenOperations)
		}
		r.live = c.clone()
	}
	if candidate != nil {
		sem := candidate.semantic()
		r.Candidate = &sem
	}
	return r
}

type checkpointSubject struct {
	TaskID        string
	PlanAttemptID string
	Objective     string
	Status        checkpointStatus
	Retirement    retirementReason
	Obligation    *cycleCompletion
	Candidate     *candidateObservation
	CandidateIn   candidateReplay
}

func (s checkpointSubject) capsule(previous string) replayCapsule {
	k := replayCapsule{
		Version: checkpointVersion, TaskID: s.TaskID, PlanAttemptID: s.PlanAttemptID, Objective: s.Objective,
		Status: s.Status, Retirement: s.Retirement, PreviousCheckpointID: previous,
	}
	if c := s.Obligation; c != nil {
		k.Obligation = &obligationInputs{
			EstablishedUnder: c.EstablishedUnder, Cycle: c.Cycle, Review: c.Review,
			Steps: cloneCycleSteps(c.Journal),
		}
	}
	if s.Candidate != nil {
		in := s.CandidateIn
		k.Candidate = &in
	}
	return k
}

func (s checkpointSubject) liveDigest(previous string) string {
	return semanticObligation(s.TaskID, s.PlanAttemptID, s.Objective, s.Status, s.Retirement, previous, s.Obligation, s.Candidate).digest()
}

func (e *Engine) obligationSubject(c *cycleCompletion, status checkpointStatus) checkpointSubject {
	obs, in := e.currentCandidate(c.TaskID)
	return checkpointSubject{
		TaskID: c.TaskID, PlanAttemptID: c.PlanAttemptID, Status: status,
		Obligation: c.clone(), Candidate: &obs, CandidateIn: in,
	}
}

func recordedObjective(events []event.Event, taskID string) (string, error) {
	var objective string
	count := 0
	for _, ev := range events {
		if ev.TaskID != taskID || ev.Kind != event.TaskCreated {
			continue
		}
		count++
		objective = ev.Summary
	}
	if count != 1 || strings.TrimSpace(objective) == "" {
		return "", fmt.Errorf("task %s has %d durable creation roots or a blank objective; exactly one nonblank task.created root is required", taskID, count)
	}
	return objective, nil
}

// replayCheckpoint reconstructs checkpoint meaning only through landed owners:
// Objective-64, 70A3, 70A2, 70A1, durable evidence readback, and 70A4.
func (e *Engine) replayCheckpoint(ctx context.Context, k replayCapsule) (replayedObligation, error) {
	refuse := func(format string, args ...any) (replayedObligation, error) {
		return replayedObligation{}, fmt.Errorf("checkpoint replay refused: "+format, args...)
	}
	if k.Version != checkpointVersion {
		return refuse("version %q is not %q", k.Version, checkpointVersion)
	}
	if !k.Status.valid() {
		return refuse("status %q is outside live, blocked, exhausted, retired", k.Status)
	}
	if strings.TrimSpace(k.TaskID) == "" || strings.TrimSpace(k.PlanAttemptID) == "" || strings.TrimSpace(k.Objective) == "" {
		return refuse("task, PlanAttempt and objective are all required")
	}

	if k.Status == checkpointRetired {
		if !k.Retirement.valid() || k.Obligation != nil || k.Candidate != nil || strings.TrimSpace(k.PreviousCheckpointID) == "" {
			return refuse("retired checkpoint is not a closed tombstone with a valid reason and predecessor")
		}
		plans, err := e.planAttemptTransitions(k.TaskID, k.Objective)
		if err != nil {
			return refuse("PlanAttempt history cannot be verified: %v", err)
		}
		if !plans.madeOperative(k.PlanAttemptID) {
			return refuse("retired checkpoint names PlanAttempt %s that Objective-64 did not make operative", short12(k.PlanAttemptID))
		}
		return semanticObligation(k.TaskID, k.PlanAttemptID, k.Objective, k.Status, k.Retirement, k.PreviousCheckpointID, nil, nil), nil
	}
	if k.Retirement != "" {
		return refuse("%s checkpoint carries retirement reason %q", k.Status, k.Retirement)
	}
	if k.Obligation == nil || k.Candidate == nil {
		return refuse("%s checkpoint is missing obligation or candidate owner input", k.Status)
	}
	c, err := e.replayCycle(ctx, k.TaskID, k.Objective, *k.Obligation)
	if err != nil {
		return refuse("cycle owner cannot reconstruct the obligation: %v", err)
	}
	if c.PlanAttemptID != k.PlanAttemptID {
		return refuse("replayed cycle belongs to PlanAttempt %s, checkpoint names %s", short12(c.PlanAttemptID), short12(k.PlanAttemptID))
	}
	if c.Exhausted() != (k.Status == checkpointExhausted) {
		return refuse("cycle has %d of %d incomplete attempts, inconsistent with status %q", c.Attempts, maxIncompleteImplementerAttempts, k.Status)
	}
	obs, err := replayCandidateObservation(ctx, e.Repo, *k.Candidate)
	if err != nil {
		return refuse("70A4 cannot reconstruct the candidate: %v", err)
	}
	return semanticObligation(k.TaskID, k.PlanAttemptID, k.Objective, k.Status, "", k.PreviousCheckpointID, c, &obs), nil
}

const IncompleteObligationPersistenceFailedState = "incomplete_obligation_persistence_failed"

type PriorCheckpoint string

const (
	priorCheckpointCommitted PriorCheckpoint = "committed"
	priorCheckpointNone      PriorCheckpoint = "none"
	priorCheckpointUnknown   PriorCheckpoint = "unknown"
)

type IncompleteObligationPersistenceFailed struct {
	State             string           `json:"state"`
	TaskID            string           `json:"task_id"`
	PlanAttemptID     string           `json:"plan_attempt_id"`
	Status            checkpointStatus `json:"checkpoint_status"`
	Attempts          int              `json:"persistence_attempts"`
	PriorCheckpoint   PriorCheckpoint  `json:"prior_checkpoint"`
	PriorCheckpointID string           `json:"prior_checkpoint_id,omitempty"`
	Cause             string           `json:"cause"`
}

func (f *IncompleteObligationPersistenceFailed) Error() string {
	prior := "no earlier committed checkpoint exists"
	switch f.PriorCheckpoint {
	case priorCheckpointCommitted:
		prior = "earlier committed checkpoint " + short12(f.PriorCheckpointID) + " stands unchanged"
	case priorCheckpointUnknown:
		prior = "earlier checkpoint state is unknown because durable history could not be read"
	}
	return fmt.Sprintf("%s: %s checkpoint for task %s, PlanAttempt %s, did not commit after %d persistence attempt(s); %s: %s",
		f.State, f.Status, f.TaskID, orNone(short12(f.PlanAttemptID), "none"), f.Attempts, prior, f.Cause)
}

func (e *Engine) persistenceFailed(subject checkpointSubject, attempts int, prior PriorCheckpoint, priorID, cause string) error {
	if strings.TrimSpace(cause) == "" {
		cause = "checkpoint transaction did not establish a committed record"
	}
	return &IncompleteObligationPersistenceFailed{
		State: IncompleteObligationPersistenceFailedState, TaskID: subject.TaskID,
		PlanAttemptID: subject.PlanAttemptID, Status: subject.Status, Attempts: attempts,
		PriorCheckpoint: prior, PriorCheckpointID: priorID, Cause: cause,
	}
}

func checkpointIdentity(taskID, planAttemptID, previous, payloadDigest string) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		checkpointVersion, taskID, planAttemptID, previous, payloadDigest,
	}, "\n")))
	return hex.EncodeToString(sum[:])
}

var writeCheckpointPayload = func(s *session.Store, id string, payload []byte) error {
	return s.WriteCheckpoint(id, payload)
}

func (e *Engine) commitObligation(ctx context.Context, c *cycleCompletion, status checkpointStatus) error {
	_, err := e.commitCheckpoint(ctx, e.obligationSubject(c, status))
	return err
}

// commitCheckpoint is the only publication transaction. Nothing is resumable
// merely because PREPARED or payload bytes exist.
func (e *Engine) commitCheckpoint(ctx context.Context, subject checkpointSubject) (session.Checkpoint, error) {
	if e.Store == nil {
		return session.Checkpoint{}, e.persistenceFailed(subject, 0, priorCheckpointUnknown, "", session.ErrNoStore.Error())
	}
	prior, priorID := priorCheckpointUnknown, ""
	cause := ""
	for attempt := 1; attempt <= maxCheckpointPersistAttempts; attempt++ {
		events, err := e.Store.Load()
		if err != nil {
			cause = "session history cannot be read: " + err.Error()
			continue
		}
		objective, err := recordedObjective(events, subject.TaskID)
		if err != nil {
			cause = err.Error()
			continue
		}
		if subject.Objective != "" && subject.Objective != objective {
			return session.Checkpoint{}, e.persistenceFailed(subject, attempt, prior, priorID,
				"subject objective differs from the durable task creation root")
		}
		subject.Objective = objective

		latest, found, err := session.LatestCommittedCheckpoint(events, e.SessionID, subject.TaskID)
		if err != nil {
			cause = "committed-checkpoint projection failed: " + err.Error()
			continue
		}
		prior, priorID = priorCheckpointNone, ""
		if found {
			prior, priorID = priorCheckpointCommitted, latest.CheckpointID
			// A previous attempt may have durably committed but failed to
			// acknowledge it. Recognize the exact desired semantic state instead
			// of manufacturing a replacement checkpoint on retry.
			if latest.PlanAttemptID == subject.PlanAttemptID && e.verifyCheckpoint(ctx, latest.CheckpointRecord) == "" &&
				latest.ReplayDigest == subject.liveDigest(latest.PreviousCheckpointID) {
				return latest, nil
			}
		}

		capsule := subject.capsule(priorID)
		replayed, err := e.replayCheckpoint(ctx, capsule)
		if err != nil {
			return session.Checkpoint{}, e.persistenceFailed(subject, attempt, prior, priorID,
				"checkpoint inputs do not replay: "+err.Error())
		}
		if replayed.digest() != subject.liveDigest(priorID) {
			return session.Checkpoint{}, e.persistenceFailed(subject, attempt, prior, priorID,
				"checkpoint inputs replay to semantics different from the live owner state")
		}
		payload, err := json.Marshal(capsule)
		if err != nil {
			return session.Checkpoint{}, e.persistenceFailed(subject, attempt, prior, priorID, "checkpoint serialization failed: "+err.Error())
		}
		sum := sha256.Sum256(payload)
		rec := session.CheckpointRecord{
			TaskID: subject.TaskID, PlanAttemptID: subject.PlanAttemptID, PreviousCheckpointID: priorID,
			PayloadDigest: hex.EncodeToString(sum[:]), ReplayDigest: replayed.digest(),
		}
		rec.CheckpointID = checkpointIdentity(rec.TaskID, rec.PlanAttemptID, rec.PreviousCheckpointID, rec.PayloadDigest)

		if err := e.emitDurable(event.New(e.SessionID, subject.TaskID, event.SourceSystem, event.CheckpointPrepared,
			fmt.Sprintf("%s checkpoint %s prepared", subject.Status, short12(rec.CheckpointID)), rec)); err != nil {
			cause = err.Error()
			continue
		}
		if err := writeCheckpointPayload(e.Store, rec.CheckpointID, payload); err != nil {
			cause = "checkpoint payload write failed: " + err.Error()
			continue
		}
		if cause = e.verifyCheckpoint(ctx, rec); cause != "" {
			continue
		}
		if err := e.emitDurable(event.New(e.SessionID, subject.TaskID, event.SourceSystem, event.CheckpointCommitted,
			fmt.Sprintf("%s checkpoint %s committed", subject.Status, short12(rec.CheckpointID)), rec)); err != nil {
			cause = err.Error()
			continue
		}
		return session.Checkpoint{CheckpointRecord: rec, Source: event.SourceSystem, SessionID: e.SessionID}, nil
	}
	return session.Checkpoint{}, e.persistenceFailed(subject, maxCheckpointPersistAttempts, prior, priorID, cause)
}

// verifyCheckpoint binds persisted bytes back to every prepared framing field,
// replays owner inputs, and compares the reconstructed semantic digest.
func (e *Engine) verifyCheckpoint(ctx context.Context, rec session.CheckpointRecord) string {
	if e.Store == nil {
		return session.ErrNoStore.Error()
	}
	raw, err := e.Store.ReadCheckpoint(rec.CheckpointID)
	if err != nil {
		return "checkpoint payload readback failed: " + err.Error()
	}
	sum := sha256.Sum256(raw)
	payloadDigest := hex.EncodeToString(sum[:])
	if payloadDigest != rec.PayloadDigest {
		return "checkpoint payload read back is not the payload named by PREPARED"
	}
	var back replayCapsule
	if err := session.DecodeStrict(raw, &back); err != nil {
		return "checkpoint payload is not one strict capsule: " + err.Error()
	}
	if back.TaskID != rec.TaskID || back.PlanAttemptID != rec.PlanAttemptID || back.PreviousCheckpointID != rec.PreviousCheckpointID {
		return "checkpoint payload metadata does not match its prepared task, PlanAttempt and predecessor"
	}
	if checkpointIdentity(back.TaskID, back.PlanAttemptID, back.PreviousCheckpointID, payloadDigest) != rec.CheckpointID {
		return "checkpoint id does not reproduce from its prepared identity fields and payload digest"
	}
	replayed, err := e.replayCheckpoint(ctx, back)
	if err != nil {
		return err.Error()
	}
	if replayed.digest() != rec.ReplayDigest {
		return fmt.Sprintf("checkpoint replays to %s, not prepared ReplayDigest %s", short12(replayed.digest()), short12(rec.ReplayDigest))
	}
	return ""
}

package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

func liveCheckpointRig(t *testing.T) (*completionRig, *cycleCompletion, error) {
	t.Helper()
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{err: quota()},
	)
	_, err := r.run()
	if err == nil {
		t.Fatal("premise: the unavailable provider did not leave a live cycle")
	}
	c := r.live(t)
	if c.Attempts != 1 || c.Cycle != 2 || !c.Continuing ||
		!equalStrings(c.RetainedIDs(), "f1") || !equalStrings(c.Owed(), "f2") {
		t.Fatalf("premise: cycle 2 is not live with f1 retained, f2 owed and one attempt: %+v", c)
	}
	return r, c, err
}

func committedCheckpoint(t *testing.T, e *Engine, taskID string) (session.Checkpoint, replayCapsule, bool) {
	t.Helper()
	events, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	cp, ok, err := session.LatestCommittedCheckpoint(events, e.SessionID, taskID)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		return session.Checkpoint{}, replayCapsule{}, false
	}
	raw, err := e.Store.ReadCheckpoint(cp.CheckpointID)
	if err != nil {
		t.Fatal(err)
	}
	var k replayCapsule
	if err := session.DecodeStrict(raw, &k); err != nil {
		t.Fatal(err)
	}
	return cp, k, true
}

func checkpointEventCount(t *testing.T, e *Engine, kind event.Kind) int {
	t.Helper()
	events, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, ev := range events {
		if ev.Kind == kind {
			n++
		}
	}
	return n
}

func checkpointBytes(t *testing.T, k replayCapsule) []byte {
	t.Helper()
	raw, err := json.Marshal(k)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func verifyRawCheckpoint(t *testing.T, e *Engine, k replayCapsule, raw []byte, replayDigest string) string {
	t.Helper()
	sum := sha256.Sum256(raw)
	payloadDigest := hex.EncodeToString(sum[:])
	rec := session.CheckpointRecord{
		TaskID: k.TaskID, PlanAttemptID: k.PlanAttemptID,
		PreviousCheckpointID: k.PreviousCheckpointID,
		PayloadDigest: payloadDigest, ReplayDigest: replayDigest,
	}
	rec.CheckpointID = checkpointIdentity(rec.TaskID, rec.PlanAttemptID, rec.PreviousCheckpointID, rec.PayloadDigest)
	if err := e.Store.WriteCheckpoint(rec.CheckpointID, raw); err != nil {
		t.Fatal(err)
	}
	return e.verifyCheckpoint(context.Background(), rec)
}

func failCheckpointWrites(t *testing.T, failures int) *int {
	t.Helper()
	previous := writeCheckpointPayload
	calls := 0
	writeCheckpointPayload = func(s *session.Store, id string, payload []byte) error {
		calls++
		if failures < 0 || calls <= failures {
			return errors.New("injected checkpoint payload failure")
		}
		return previous(s, id, payload)
	}
	t.Cleanup(func() { writeCheckpointPayload = previous })
	return &calls
}

func TestDF41B1TransactionCommitsOnlyOwnerReplayedState(t *testing.T) {
	r, c, _ := liveCheckpointRig(t)
	e := r.h.engine
	subject := e.obligationSubject(c, checkpointLive)
	subject.Objective = r.h.tc.Task

	replayed, err := e.replayCheckpoint(context.Background(), subject.capsule(""))
	if err != nil {
		t.Fatalf("live owner state does not replay: %v", err)
	}
	if replayed.digest() != subject.liveDigest("") {
		t.Fatalf("replay digest %s != live digest %s", replayed.digest(), subject.liveDigest(""))
	}

	cp, err := e.commitCheckpoint(context.Background(), subject)
	if err != nil {
		t.Fatalf("commit live checkpoint: %v", err)
	}
	held, capsule, ok := committedCheckpoint(t, e, "task-1")
	if !ok || held.CheckpointRecord != cp.CheckpointRecord {
		t.Fatalf("session projection does not prove the committed checkpoint: %+v", held)
	}
	if capsule.Status != checkpointLive || capsule.Objective != r.h.tc.Task {
		t.Fatalf("committed capsule lost status or durable objective: %+v", capsule)
	}
	if why := e.verifyCheckpoint(context.Background(), held.CheckpointRecord); why != "" {
		t.Fatalf("committed checkpoint fails readback verification: %s", why)
	}
	if got, err := e.replayCheckpoint(context.Background(), capsule); err != nil || got.digest() != cp.ReplayDigest {
		t.Fatalf("committed payload does not reproduce ReplayDigest: digest=%q err=%v", got.digest(), err)
	}

	// Retrying after an acknowledgment ambiguity recognizes the same committed
	// semantic state rather than manufacturing a successor.
	again, err := e.commitCheckpoint(context.Background(), subject)
	if err != nil || again.CheckpointID != cp.CheckpointID {
		t.Fatalf("idempotent retry minted another checkpoint: first=%s again=%s err=%v", cp.CheckpointID, again.CheckpointID, err)
	}
	if checkpointEventCount(t, e, event.CheckpointCommitted) != 1 {
		t.Fatal("idempotent retry appended a second committed record")
	}

	// Derived answers must not sneak back into the capsule.
	raw := string(checkpointBytes(t, capsule))
	for _, forbidden := range []string{`"owed"`, `"retained"`, `"attempts"`, `"continuing"`, `"valid"`, `"constructible"`} {
		if strings.Contains(raw, forbidden) {
			t.Errorf("capsule persists derived answer %s: %s", forbidden, raw)
		}
	}
}

func TestDF41B1PreparedPayloadIsNotEligibleUntilCommitted(t *testing.T) {
	r, c, _ := liveCheckpointRig(t)
	e := r.h.engine
	previous := writeCheckpointPayload
	t.Cleanup(func() { writeCheckpointPayload = previous })

	var eligibleDuringWrite bool
	var observed bool
	writeCheckpointPayload = func(s *session.Store, id string, payload []byte) error {
		events, err := s.Load()
		if err != nil {
			return err
		}
		_, eligibleDuringWrite, err = session.LatestCommittedCheckpoint(events, e.SessionID, "task-1")
		if err != nil {
			return err
		}
		observed = true
		return previous(s, id, payload)
	}
	cp, err := e.commitCheckpoint(context.Background(), e.obligationSubject(c, checkpointLive))
	if err != nil {
		t.Fatal(err)
	}
	if !observed || eligibleDuringWrite {
		t.Fatalf("checkpoint was eligible before COMMITTED: observed=%v eligible=%v", observed, eligibleDuringWrite)
	}
	if checkpointEventCount(t, e, event.CheckpointPrepared) != 1 ||
		checkpointEventCount(t, e, event.CheckpointCommitted) != 1 {
		t.Fatal("transaction did not emit exactly one PREPARED and one COMMITTED")
	}
	held, _, ok := committedCheckpoint(t, e, "task-1")
	if !ok || held.CheckpointID != cp.CheckpointID {
		t.Fatal("checkpoint did not become eligible after COMMITTED")
	}
}

func TestDF41B1ReadbackBindsEveryPreparedIdentityField(t *testing.T) {
	r, c, _ := liveCheckpointRig(t)
	e := r.h.engine
	cp, err := e.commitCheckpoint(context.Background(), e.obligationSubject(c, checkpointLive))
	if err != nil {
		t.Fatal(err)
	}

	for name, mutate := range map[string]func(*session.CheckpointRecord){
		"task": func(rec *session.CheckpointRecord) { rec.TaskID = "task-other" },
		"plan attempt": func(rec *session.CheckpointRecord) { rec.PlanAttemptID = strings.Repeat("f", 64) },
		"predecessor": func(rec *session.CheckpointRecord) { rec.PreviousCheckpointID = strings.Repeat("e", 64) },
		"payload digest": func(rec *session.CheckpointRecord) { rec.PayloadDigest = strings.Repeat("d", 64) },
		"replay digest": func(rec *session.CheckpointRecord) { rec.ReplayDigest = strings.Repeat("c", 64) },
		"checkpoint id": func(rec *session.CheckpointRecord) { rec.CheckpointID = strings.Repeat("b", 64) },
	} {
		rec := cp.CheckpointRecord
		mutate(&rec)
		if why := e.verifyCheckpoint(context.Background(), rec); why == "" {
			t.Errorf("%s can be changed without invalidating the prepared binding", name)
		}
	}
}

func TestDF41B1StrictReadbackRejectsTrailingData(t *testing.T) {
	r, c, _ := liveCheckpointRig(t)
	e := r.h.engine
	subject := e.obligationSubject(c, checkpointLive)
	subject.Objective = r.h.tc.Task
	k := subject.capsule("")
	replayed, err := e.replayCheckpoint(context.Background(), k)
	if err != nil {
		t.Fatal(err)
	}
	raw := checkpointBytes(t, k)
	if why := verifyRawCheckpoint(t, e, k, raw, replayed.digest()); why != "" {
		t.Fatalf("valid capsule does not verify: %s", why)
	}
	for _, tail := range []string{"]", "}", `{"version":"x"}`, " null", "trailing"} {
		bad := append(append([]byte(nil), raw...), []byte(tail)...)
		if why := verifyRawCheckpoint(t, e, k, bad, replayed.digest()); why == "" {
			t.Errorf("capsule with trailing %q verified", tail)
		}
	}
}

func TestDF41B1PersistenceFailureIsTypedAndBounded(t *testing.T) {
	r, c, _ := liveCheckpointRig(t)
	e := r.h.engine
	calls := failCheckpointWrites(t, -1)

	_, err := e.commitCheckpoint(context.Background(), e.obligationSubject(c, checkpointBlocked))
	var failed *IncompleteObligationPersistenceFailed
	if !errors.As(err, &failed) {
		t.Fatalf("three payload failures did not return typed persistence failure: %v", err)
	}
	if *calls != maxCheckpointPersistAttempts || failed.Attempts != maxCheckpointPersistAttempts ||
		failed.PriorCheckpoint != priorCheckpointNone || failed.Status != checkpointBlocked {
		t.Fatalf("bounded failure is not truthful: calls=%d failure=%+v", *calls, failed)
	}
	if checkpointEventCount(t, e, event.CheckpointCommitted) != 0 {
		t.Fatal("failed persistence advertised a committed checkpoint")
	}

	e.Store = nil
	_, err = e.commitCheckpoint(context.Background(), e.obligationSubject(c, checkpointBlocked))
	if !errors.As(err, &failed) || failed.Attempts != 0 || failed.PriorCheckpoint != priorCheckpointUnknown {
		t.Fatalf("nil store was not typed as unknown durable state: %v", err)
	}
}

func TestDF41B1FailedSuccessorLeavesPriorCheckpointStanding(t *testing.T) {
	r, c, _ := liveCheckpointRig(t)
	e := r.h.engine
	first, err := e.commitCheckpoint(context.Background(), e.obligationSubject(c, checkpointBlocked))
	if err != nil {
		t.Fatal(err)
	}

	failCheckpointWrites(t, -1)
	retired := checkpointSubject{
		TaskID: "task-1", PlanAttemptID: c.PlanAttemptID,
		Status: checkpointRetired, Retirement: retiredReplacedCheckpoint,
	}
	_, err = e.commitCheckpoint(context.Background(), retired)
	var failed *IncompleteObligationPersistenceFailed
	if !errors.As(err, &failed) ||
		failed.PriorCheckpoint != priorCheckpointCommitted ||
		failed.PriorCheckpointID != first.CheckpointID {
		t.Fatalf("failed successor did not name the prior committed checkpoint: %v", err)
	}
	held, k, ok := committedCheckpoint(t, e, "task-1")
	if !ok || held.CheckpointRecord != first.CheckpointRecord || k.Status != checkpointBlocked {
		t.Fatalf("failed successor displaced prior committed checkpoint: %+v %+v", held, k)
	}
	if why := e.verifyCheckpoint(context.Background(), held.CheckpointRecord); why != "" {
		t.Fatalf("prior checkpoint stopped verifying: %s", why)
	}
}

func TestDF41B1BlockedTerminalFollowsCommittedCheckpoint(t *testing.T) {
	r, _, blockedErr := liveCheckpointRig(t)
	e := r.h.engine

	if !e.blockExternally("task-1", blockedErr) {
		t.Fatalf("live provider block was not classified as external: %v", blockedErr)
	}
	cp, capsule, ok := committedCheckpoint(t, e, "task-1")
	if !ok || capsule.Status != checkpointBlocked {
		t.Fatalf("external block did not commit a blocked checkpoint first: checkpoint=%+v capsule=%+v", cp, capsule)
	}
	if why := e.verifyCheckpoint(context.Background(), cp.CheckpointRecord); why != "" {
		t.Fatalf("blocked checkpoint is not replay-valid: %s", why)
	}

	events, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	committedAt, blockedAt := -1, -1
	for i, ev := range events {
		switch ev.Kind {
		case event.CheckpointCommitted:
			committedAt = i
		case event.WorkflowBlockedExternal:
			blockedAt = i
		}
	}
	if committedAt < 0 || blockedAt < 0 || committedAt >= blockedAt {
		t.Fatalf("resumable external block was advertised before checkpoint commit: committed=%d blocked=%d", committedAt, blockedAt)
	}
}

func TestDF41B1BlockedTerminalIsWithheldWhenCheckpointPersistenceFails(t *testing.T) {
	r, _, blockedErr := liveCheckpointRig(t)
	e := r.h.engine
	calls := failCheckpointWrites(t, -1)

	if !e.blockExternally("task-1", blockedErr) {
		t.Fatalf("live provider block was not classified as external: %v", blockedErr)
	}
	if *calls != maxCheckpointPersistAttempts {
		t.Fatalf("checkpoint persistence was not bounded: calls=%d", *calls)
	}
	events, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range events {
		if ev.Kind == event.WorkflowBlockedExternal {
			t.Fatalf("uncommitted obligation was advertised as resumable: %+v", ev)
		}
		if ev.Kind == event.CheckpointCommitted {
			t.Fatalf("failed checkpoint write produced a committed checkpoint: %+v", ev)
		}
	}
	foundTyped := false
	for _, ev := range events {
		if ev.Kind == event.Status && strings.Contains(ev.Summary, IncompleteObligationPersistenceFailedState) {
			foundTyped = true
		}
	}
	if !foundTyped {
		t.Fatal("checkpoint persistence failure was not emitted as typed observable state")
	}
}


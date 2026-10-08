package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/event"
)

// Objective 70B2a1 witnesses: session lineage is established and enforced at
// the durable Store boundary. A1-W1 lives in cmd/sensei-code/resume_test.go
// and A1-W10 in internal/workflow/emit_test.go.

const lineageTask = "task-lineage"

// waitContext is the method set of a context.Context, named here so a helper
// can take any of the contexts these witnesses wait under.
type waitContext = interface {
	Deadline() (time.Time, bool)
	Done() <-chan struct{}
	Err() error
	Value(any) any
}

// cancelledContext is a context.Context whose wait has already ended.
type cancelledContext struct{}

var errLineageTestCancelled = errors.New("the caller cancelled")

func (cancelledContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (cancelledContext) Done() <-chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
func (cancelledContext) Err() error       { return errLineageTestCancelled }
func (cancelledContext) Value(any) any    { return nil }
func (c cancelledContext) String() string { return "cancelledContext" }

// closableContext is a context.Context whose wait ends when close is called.
type closableContext struct{ done chan struct{} }

func newClosableContext() closableContext { return closableContext{done: make(chan struct{})} }

func (closableContext) Deadline() (time.Time, bool) { return time.Time{}, false }
func (c closableContext) Done() <-chan struct{}     { return c.done }
func (c closableContext) Err() error {
	select {
	case <-c.done:
		return errLineageTestCancelled
	default:
		return nil
	}
}
func (closableContext) Value(any) any  { return nil }
func (closableContext) String() string { return "closableContext" }
func (c closableContext) close()       { close(c.done) }

func lineageStore(t *testing.T, holder string) *Store {
	t.Helper()
	s, err := New(t.TempDir(), holder)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func created(sessionID string) event.Event {
	return event.New(sessionID, lineageTask, event.SourceSystem, event.TaskCreated, "the objective", nil)
}

func status(sessionID, summary string) event.Event {
	return event.New(sessionID, lineageTask, event.SourceSystem, event.Status, summary, nil)
}

// governed is a record of kind by sessionID that is semantically valid for
// its kind (validSemantics): attributed to a source that may record it and
// carrying the payload its kind carries, so a refusal of it is a refusal of
// its writer, not of its shape.
func governed(sessionID string, kind event.Kind, summary string) event.Event {
	source := event.SourceSystem
	var payload any
	switch kind {
	case event.AuthorityResolved:
		source, payload = event.SourceUser, map[string]string{"option": "1"}
	case event.RunReceipt:
		payload = map[string]any{"receipt": map[string]string{"schema": "test"}, "completeness": "complete", "missing": []string{}}
	case event.CheckpointPrepared, event.CheckpointCommitted:
		payload = CheckpointBinding{CheckpointID: strings.Repeat("cd", 32), TaskID: lineageTask, PlanAttemptID: "attempt-1",
			Status: CheckpointLive, PayloadDigest: "p", ReplayDigest: "r"}
	case event.PlanAttemptRefused:
		payload = map[string]string{"plan_attempt_id": "attempt-1", "task_id": lineageTask, "reason": "refused"}
	case event.PlanAttemptStarted:
		payload = map[string]any{"plan_attempt_id": "attempt-1", "task_id": lineageTask, "world": "w", "plan_source": "architect",
			"plan": map[string]string{}}
	case event.PlanProposed:
		source, payload = event.SourceArchitect, map[string]string{"decision": "proceed", "summary": "s", "plan": "the plan", "plan_attempt_id": "attempt-1", "plan_source": "architect"}
	case event.ProspectiveGranted, event.TestEditGranted:
		payload = map[string]any{"plan_attempt_id": "attempt-1", "world": "w", "grants": []string{}}
	case event.ReviewCompleted:
		source, payload = event.SourceReviewer, map[string]any{"decision": "accept", "summary": "s",
			"provenance": map[string]string{"task_id": lineageTask}}
	case event.AuthorityRequired:
		source, payload = event.SourceArchitect, standingDecision()
	case event.WorkflowAwaitingAuthority:
		source, payload = event.SourceUser, standingQuestion(sessionID)
	case event.WorkflowAwaitingReview:
		source, payload = event.SourceReviewer, map[string]any{"review_kind": "unanswered", "independent_review": false,
			"obligations": []string{"the candidate is owed an independent review"}, "request_id": "request-1"}
	case event.WorkflowBlockedExternal:
		payload = map[string]any{"task_id": lineageTask, "role": "architect", "provider": "codex", "reason": "QUOTA",
			"retry_at_state": "KNOWN", "retry_at": "2026-10-08T07:10:00Z"}
	case event.WorkflowNotConverged:
		payload = map[string]any{"task_id": lineageTask, "implementers": []string{"claude"}, "review_cycles": 3, "owed": "architect_replan"}
	case event.WorkflowRestorationRefused:
		payload = map[string]any{"task_id": lineageTask, "subject": "operative-plan-attempt", "instrument": "record",
			"binding": "record_unreadable", "detail": "the record could not be read"}
	case event.WorkflowBaseMovedRefused:
		payload = map[string]string{"TaskID": lineageTask, "Recorded": "1e3f4a8", "Current": "7aaeab1"}
	case event.WorkflowDirtyCanonicalRefused:
		payload = map[string]string{"Repository": "/repo"}
	case event.WorkflowPlanAdmissionRefused:
		payload = map[string]string{"plan_attempt_id": "attempt-1", "task_id": lineageTask, "reason": "refused again",
			"refusal_id": "refusal-1", "continuation": PlanAdmissionContinuationArchitectTurn}
	case event.DecisionRecorded:
		source = event.SourceSensei
	case event.GuidanceDelivered:
		source = event.SourceUser
	case event.InspectionReported:
		source = event.SourceClaude
	case event.ModeSelected:
		payload = map[string]string{"mode": "governed", "provenance": "configured"}
	case event.ArchitectSpoke:
		source, payload = event.SourceArchitect, map[string]any{"decision": "proceed", "summary": "s", "plan": "p", "files": []string{"a.go"}}
	case event.PullRequestOpened:
		source, payload = event.SourceGit, map[string]string{"url": "https://example.invalid/pr/1"}
	case event.RoleAssigned:
		payload = map[string]any{"role": "architect", "provider": "codex", "roster_position": 1, "roster_size": 2}
	case event.CandidateChanged:
		source, payload = event.SourceGit, map[string]int{"cycle": 1, "review_attempt": 1}
	case event.CandidateArtifactExcluded:
		source, payload = event.SourceGit, map[string]string{"path": "bin/x", "class": "binary", "size": "12", "reason": "built"}
	case event.Output:
		source, payload = event.SourceClaude, map[string]string{"stream": "assistant"}
	case event.AgentStarted:
		source, payload = event.SourceReviewer, map[string]any{"request_id": "request-1", "transport": "github"}
	case event.AgentFinished:
		source, payload = event.SourceReviewer, map[string]any{"request_id": "request-1", "candidate_tree": "t", "review_commit": "c",
			"github_author": "octo", "github_author_id": 7, "transport": "github"}
	case event.Status:
		payload = map[string]int{"cycle": 2}
	case event.SenseiResult:
		source, payload = event.SourceSensei, map[string]any{"risk_class": "LOW", "evidence": map[string]any{
			"operation": "awareness_preflight", "request": map[string]any{"file": "a.go"}}}
	case event.CandidateAudited, event.CandidateNotAuditable:
		source, payload = event.SourceSensei, map[string]any{"decision": "pass", "evidence": map[string]any{
			"operation": "awareness_audit_diff", "request": map[string]any{"diff": "d"}, "revision": "r"}}
	case event.ChangeReported:
		source, payload = event.SourceSensei, map[string]any{"files": []string{"a.go"}, "risk": "low"}
	case event.ContextConsulted:
		payload = map[string]any{"sources": []string{"graph"}}
	case event.HandoffCreated:
		payload = map[string]any{"provenance": map[string]string{"task_id": lineageTask}, "state": "open", "cycles_used": 3}
	case event.ReviewContradiction:
		payload = map[string]any{"reviewer": "codex", "review_attempt": 2, "decision": "revise", "summary": "s",
			"candidate_digest": "d", "evidence_identity": "e"}
	case event.ArchitectReconciliation:
		source, payload = event.SourceArchitect, map[string]any{"provenance": map[string]string{"task_id": lineageTask},
			"disputed": "f1", "inputs": []string{}, "canonical": []string{}, "decision": "d", "authority": "architect"}
	case event.RoutineClassified:
		payload = map[string]any{"routine": false, "blocking": "a contract"}
	case event.ValidationRun:
		payload = map[string]any{"candidate_id": "c", "diff_digest": "d", "checks": []string{}}
	case event.WorkflowCompleted:
		payload = map[string]string{"workspace": "w", "implementor": "claude", "plan": "p", "review": "accept", "audit": "pass", "publication": "declined"}
	case event.WorkflowFailed:
		payload = map[string]any{"state": "IMPLEMENTER_INCOMPLETE", "task_id": lineageTask, "plan_attempt_id": "attempt-1",
			"review_cycle": 1, "review_attempt": 1, "attempts": 2, "max_attempts": 2, "owed_findings": []string{"f1"}}
	case event.ReviewStarted:
		source, payload = event.SourceReviewer, map[string]any{"candidate": "d", "review_attempt": 1}
	case event.ReviewFinding:
		source, payload = event.SourceReviewer, map[string]any{"id": "f1", "severity": "blocking", "claim": "c", "reason": "r"}
	case event.CandidateResolved:
		source = event.SourceGit
		payload = map[string]any{"disposition": "resumable", "reason": "kept", "decided_at": "2026-10-08T00:00:00Z",
			"evidence": map[string]any{"base_sha": "1e3f4a8", "diff_bytes": 0}, "worktree_removed": false, "branch_removed": false}
		summary = "resumable: kept"
	}
	return event.New(sessionID, lineageTask, source, kind, summary, payload)
}

// standingDecision is a decision a person is asked for, in its complete shape.
func standingDecision() map[string]any {
	return map[string]any{"level": 3, "subject": "may this land?", "reason": "it changes a contract",
		"options": []map[string]string{{"id": "1", "label": "Authorize", "outcome": "authorize"}}}
}

// standingQuestion is the complete durable shape of a question sessionID
// deferred in task lineageTask.
func standingQuestion(sessionID string) map[string]any {
	return map[string]any{"condition": "c", "domain": "d", "base_sha": "b", "decision": standingDecision(),
		"task_id": lineageTask, "session_id": sessionID, "scope": []string{"a.go"}, "scope_recorded": true}
}

func mustAppend(t *testing.T, s *Store, evs ...event.Event) {
	t.Helper()
	for _, e := range evs {
		if err := appendTip(t, s, t.Context(), false, e); err != nil {
			t.Fatalf("append %s by %s: %v", e.Kind, e.SessionID, err)
		}
	}
}

// tipLease takes taskID's invocation lease through s and has the Store make
// it operative for the session the record's lineage names current now, as
// the invocation of that session holds it (ContinueTaskSession). With no
// lineage to continue the lease is live and operative for no session. The
// caller releases it.
func tipLease(t *testing.T, s *Store, taskID string) (*TaskLease, error) {
	t.Helper()
	lease, err := s.AcquireTaskInvocation(t.Context(), taskID)
	if err != nil {
		return nil, err
	}
	if l, err := s.TaskSessionLineage(taskID); err == nil {
		if _, err := s.ContinueTaskSession(t.Context(), lease, l.Tip()); err != nil {
			lease.Release()
			t.Fatalf("the tip %s of task %s could not be continued: %v", l.Tip(), taskID, err)
		}
	}
	return lease, nil
}

// appendTip appends e to s as the writer an invocation of the task's current
// session is: under the task's live lease, operative for the lineage tip
// (tipLease), waiting for the record lock under ctx. A root goes through
// CreateTaskRoot and a record of no task through AppendDiagnostic, each the
// one path of its kind, and no Store at all presents no lease. So a refusal
// is the Store's refusal of e's writer, never a missing capability.
func appendTip(t *testing.T, s *Store, ctx waitContext, durable bool, e event.Event) error {
	t.Helper()
	switch {
	case s == nil:
		return s.Append(ctx, nil, e)
	case e.TaskID == "":
		return s.AppendDiagnostic(ctx, e)
	case e.Kind == event.TaskCreated:
		lease, err := s.AcquireTaskInvocation(t.Context(), e.TaskID)
		if err != nil {
			return err
		}
		defer lease.Release()
		return s.CreateTaskRoot(ctx, lease, e)
	}
	lease, err := tipLease(t, s, e.TaskID)
	if err != nil {
		return err
	}
	defer lease.Release()
	if durable {
		return s.AppendDurable(ctx, lease, e)
	}
	return s.Append(ctx, lease, e)
}

// appendTipUnit is appendTip for one append unit of the task evs[0] names.
func appendTipUnit(t *testing.T, s *Store, ctx waitContext, evs ...event.Event) error {
	t.Helper()
	task := lineageTask
	if len(evs) > 0 {
		task = evs[0].TaskID
	}
	lease, err := tipLease(t, s, task)
	if err != nil {
		return err
	}
	defer lease.Release()
	return s.AppendUnit(ctx, lease, evs...)
}

// bind binds current to the lineage the record establishes now.
func bind(t *testing.T, s *Store, current string) SessionLineage {
	t.Helper()
	l, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.BindingFor(current)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := bindLeased(t, s, t.Context(), b)
	if err != nil {
		t.Fatalf("binding %s: %v", current, err)
	}
	return bound
}

func recordOf(t *testing.T, s *Store) []event.Event {
	t.Helper()
	record, err := s.ReadRecord()
	if err != nil {
		t.Fatal(err)
	}
	return record
}

// writeRaw appends events to the record without the Store's authorization:
// what a legacy or hostile writer could have left in the file.
func writeRaw(t *testing.T, s *Store, evs ...event.Event) {
	t.Helper()
	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, e := range evs {
		line, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func rawBinding(b SessionLineageBinding) event.Event {
	return event.New(b.CurrentSessionID, b.TaskID, event.SourceSystem, SessionLineageBound, "raw", b)
}

// A1-W2 ROOT: a valid TaskCreated holder root establishes the physical holder
// identity, read from the durable root rather than from any caller.
func TestB2a1W2Root(t *testing.T) {
	// The physical record is named "record-name"; the root is A's.
	s := lineageStore(t, "record-name")
	// What a legacy writer left before the root; the Store itself refuses it.
	writeRaw(t, s, status("A", "before the root"))
	mustAppend(t, s, created("A"), status("A", "after the root"))
	holder, err := s.HolderSessionID(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	if holder != "A" {
		t.Fatalf("the holder is %q; the root was written by A", holder)
	}
	l, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	if l.HolderSessionID != "A" || l.Tip() != "A" || l.Root != 1 || len(l.Segments) != 1 {
		t.Fatalf("the root does not establish A as holder and tip at position 1: %+v", l)
	}
	// The holder a transition carries is the proven one, whatever the caller
	// believes the record is named.
	b, err := l.BindingFor("B")
	if err != nil {
		t.Fatal(err)
	}
	if b.HolderSessionID != "A" || b.ParentSessionID != "A" || b.CurrentSessionID != "B" {
		t.Fatalf("the owed transition does not name the proven holder and tip: %+v", b)
	}
}

// A1-W3 ROOTLESS: a nil Store and a Store with no TaskCreated root both fail
// closed, and neither can be bound.
func TestB2a1W3Rootless(t *testing.T) {
	var none *Store
	if _, err := none.HolderSessionID(lineageTask); !errors.Is(err, ErrNoStore) {
		t.Fatalf("a nil Store proved a holder: %v", err)
	}
	if _, err := none.TaskSessionLineage(lineageTask); !errors.Is(err, ErrNoStore) {
		t.Fatalf("a nil Store established a lineage: %v", err)
	}
	b := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A", CurrentSessionID: "B",
		Relation: SessionLineageRelationResume}
	if _, err := bindLeased(t, none, t.Context(), b); !errors.Is(err, ErrNoStore) {
		t.Fatalf("a nil Store bound a session: %v", err)
	}

	// An absent record: nothing is bound, and nothing is created.
	absent := lineageStore(t, "A")
	if _, err := bindLeased(t, absent, t.Context(), b); !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("an absent record bound a session: %v", err)
	}
	if _, err := os.Stat(absent.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused binding created the record: %v", err)
	}

	// A record holding the task's events but no root of it.
	rootless := lineageStore(t, "A")
	mustAppend(t, rootless, event.New("A", "another-task", event.SourceSystem, event.TaskCreated, "another task", nil))
	// The Store takes no record of a task it holds no root of, whatever its
	// kind and whoever writes it: rootless history fails closed.
	for _, k := range []event.Kind{event.Status, event.WorkflowFailed, event.RunReceipt, event.CheckpointCommitted} {
		e := governed("A", k, "work without a root")
		if err := appendTip(t, rootless, t.Context(), false, e); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrNoTaskRoot) {
			t.Fatalf("a rootless %s was appended: %v", k, err)
		}
		if err := appendTip(t, rootless, t.Context(), true, e); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrNoTaskRoot) {
			t.Fatalf("a rootless %s was durably appended: %v", k, err)
		}
	}
	if got := recordOf(t, rootless); len(got) != 1 {
		t.Fatalf("refused rootless appends wrote %d records", len(got)-1)
	}
	// What a legacy writer left in the file.
	writeRaw(t, rootless, status("A", "work without a root"))
	before := recordOf(t, rootless)
	if _, err := rootless.HolderSessionID(lineageTask); !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("a rootless record proved a holder: %v", err)
	}
	if _, err := bindLeased(t, rootless, t.Context(), b); !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("a rootless record bound a session: %v", err)
	}
	if got := recordOf(t, rootless); len(got) != len(before) {
		t.Fatalf("a refused binding wrote %d records", len(got)-len(before))
	}
	classes, _, err := ClassifyTaskHistory(recordOf(t, rootless), lineageTask)
	if !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("a rootless record was classified without refusal: %v", err)
	}
	for _, c := range classes {
		if c.Class != HistoryPreRoot {
			t.Fatalf("a rootless record's event was classified %s; it has no task-session owner", c.Class)
		}
	}
	// And a root written under no session -- which only a legacy writer can
	// have left (TestB2a1W3ASessionlessRootIsNeverWritten) -- establishes no
	// lineage either.
	sessionless := lineageStore(t, "A")
	writeRaw(t, sessionless, created(""))
	if _, err := sessionless.HolderSessionID(lineageTask); !errors.Is(err, ErrSessionlessRoot) {
		t.Fatalf("a sessionless root proved a holder: %v", err)
	}
	if err := appendTip(t, sessionless, t.Context(), false, status("B", "introduces a session")); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("a session was introduced into a sessionless task: %v", err)
	}
}

// A1-W3 ROOTLESS, the root itself (70B2a1 review f2): the Store writes a
// task's TaskCreated root only when it is a root the canonical lineage
// establishes -- one written under the session it proves the holder. A root
// naming no session would leave the task permanently without a lineage, so
// both generic appends refuse it and the record is unchanged byte for byte.
func TestB2a1W3ASessionlessRootIsNeverWritten(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, event.New("A", "another-task", event.SourceSystem, event.TaskCreated, "another task", nil))
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	for _, writer := range []string{"", "   "} {
		root := created(writer)
		if err := appendTip(t, s, t.Context(), false, root); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrSessionlessRoot) {
			t.Fatalf("a root written by %q was appended: %v", writer, err)
		}
		if err := appendTip(t, s, t.Context(), true, root); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrSessionlessRoot) {
			t.Fatalf("a root written by %q was durably appended: %v", writer, err)
		}
	}
	after, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("refused sessionless roots changed the record:\n%s", after[len(before):])
	}
	if _, err := s.HolderSessionID(lineageTask); !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("the task acquired a root from a refused append: %v", err)
	}
	// The same Store still takes a valid root.
	mustAppend(t, s, created("A"))
	if holder, err := s.HolderSessionID(lineageTask); err != nil || holder != "A" {
		t.Fatalf("a valid root did not prove its holder: %q %v", holder, err)
	}
}

// A1-W4 BIND: A -> B is durably established before B can perform a normal
// governed append, and only through the dedicated binding operation.
func TestB2a1W4Bind(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"), status("A", "A works"))
	if err := appendTip(t, s, t.Context(), false, status("B", "B before its binding")); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("an unbound B appended to A's task: %v", err)
	}
	// A generic append cannot carry the transition, even a well-formed one.
	owed := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A", CurrentSessionID: "B",
		Relation: SessionLineageRelationResume}
	if err := appendTip(t, s, t.Context(), false, rawBinding(owed)); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("a generic append wrote a session-lineage transition: %v", err)
	}
	if err := appendTip(t, s, t.Context(), true, rawBinding(owed)); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("a durable generic append wrote a session-lineage transition: %v", err)
	}
	if got := recordOf(t, s); len(got) != 2 {
		t.Fatalf("refused appends wrote %d records", len(got)-2)
	}

	bound := bind(t, s, "B")
	if bound.Tip() != "B" {
		t.Fatalf("the binding did not make B current: %+v", bound)
	}
	// Durable: a fresh Store over the same file reads the transition back.
	reopened, err := New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(s.path)))), "A")
	if err != nil {
		t.Fatal(err)
	}
	record := recordOf(t, reopened)
	last := record[len(record)-1]
	var b SessionLineageBinding
	if err := DecodeExactlyOne(last.Payload, &b); err != nil || last.Kind != SessionLineageBound ||
		last.SessionID != "B" || last.Source != event.SourceSystem || b != owed {
		t.Fatalf("the durable transition is not exactly A -> B written by B: %+v %+v %v", last, b, err)
	}
	mustAppend(t, s, status("B", "B works"))
	if err := appendTip(t, s, t.Context(), false, status("A", "A after it was continued")); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("superseded A appended after B became current: %v", err)
	}
	// B continuing itself binds nothing.
	l, _ := s.TaskSessionLineage(lineageTask)
	if _, err := l.BindingFor("B"); !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("the tip was offered a transition to itself: %v", err)
	}
}

// A1-W5 FOREIGN APPEND: an unrelated C cannot append a same-task governed
// event through any generic append, whatever its kind.
func TestB2a1W5ForeignAppend(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	bind(t, s, "B")
	before := recordOf(t, s)
	kinds := []event.Kind{event.Status, event.WorkflowFailed, event.WorkflowCompleted, event.WorkflowRestorationRefused,
		event.RunReceipt, event.AuthorityResolved, event.CheckpointCommitted, event.WorkflowBlockedExternal, event.TaskCreated}
	for _, k := range kinds {
		e := governed("C", k, "foreign")
		if err := appendTip(t, s, t.Context(), false, e); !errors.Is(err, ErrSessionAppendRefused) {
			t.Errorf("unrelated C appended %s: %v", k, err)
		}
		if err := appendTip(t, s, t.Context(), true, e); !errors.Is(err, ErrSessionAppendRefused) {
			t.Errorf("unrelated C durably appended %s: %v", k, err)
		}
	}
	if got := recordOf(t, s); len(got) != len(before) {
		t.Fatalf("refused appends wrote %d records", len(got)-len(before))
	}
	// A record of no task is not this task's lineage, and is no exemption
	// from the registry (RULING-198, review f6): only a diagnostic of the
	// closed taskless registry is taken; every other taskless record --
	// an unknown kind, a governed kind, a diagnostic from a source that may
	// not record it or carrying a payload -- is refused, and writes nothing.
	length := len(recordOf(t, s))
	for name, taskless := range map[string]event.Event{
		"a conversation turn":    event.New("C", "", event.SourceUser, event.ArchitectSpoke, "conversation", nil),
		"a receipt":              event.New("C", "", event.SourceSystem, event.RunReceipt, "receipt", map[string]any{"receipt": map[string]any{"a": 1}, "completeness": "complete"}),
		"a terminal":             event.New("C", "", event.SourceSystem, event.WorkflowCompleted, "done", nil),
		"an unknown kind":        event.New("C", "", event.SourceSystem, event.Kind("made.up"), "x", nil),
		"an agent's status":      event.New("C", "", event.SourceClaude, event.Status, "x", nil),
		"a status with payload":  event.New("C", "", event.SourceSystem, event.Status, "x", map[string]string{"k": "v"}),
		"an unframed diagnostic": {SessionID: "C", Kind: event.Status, Source: event.SourceSystem, Summary: "x"},
	} {
		if err := appendTip(t, s, t.Context(), false, taskless); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedEvent) {
			t.Errorf("%s of no task was not refused: %v", name, err)
		}
	}
	if got := len(recordOf(t, s)); got != length {
		t.Fatalf("refused taskless records wrote %d records", got-length)
	}
	// Control: the one taskless diagnostic is taken.
	if err := appendTip(t, s, t.Context(), false, event.New("C", "", event.SourceSystem, event.Status, "behavioral outcome not recorded", nil)); err != nil {
		t.Fatalf("a valid diagnostic was refused: %v", err)
	}
	// Even the current tip B cannot record an authority answer the system
	// claims to have given: only the user answers (RULING-193 A, f1).
	length = len(recordOf(t, s))
	systemAnswer := event.New("B", lineageTask, event.SourceSystem, event.AuthorityResolved, "answered by nobody", map[string]string{"option": "1"})
	if err := appendTip(t, s, t.Context(), false, systemAnswer); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("a SourceSystem AuthorityResolved by the current session was appended: %v", err)
	}
	if got := len(recordOf(t, s)); got != length {
		t.Fatal("a refused system answer was written")
	}
	// Control: the bound tip B appends the same kinds C was refused.
	for _, k := range kinds[:len(kinds)-1] {
		if err := appendTip(t, s, t.Context(), false, governed("B", k, "lawful")); err != nil {
			t.Errorf("the current session B was refused %s: %v", k, err)
		}
	}
}

// f1 (70B2a1 r7 review): ONE CANONICAL PER-KIND SEMANTIC VALIDATOR. The
// current session's own record of a governed kind is refused when its source
// may not record that kind or its payload is not the kind's payload, and the
// same validator classifies such a record malformed. Each case breaks exactly one rule of a record
// that is otherwise the valid one (governed), whose acceptance is the
// control.
func TestB2a1F1PerKindSemanticsAreEnforcedAtTheStore(t *testing.T) {
	type mutant struct {
		name string
		kind event.Kind
		edit func(*event.Event)
	}
	payload := func(raw string) func(*event.Event) { return func(e *event.Event) { e.Payload = json.RawMessage(raw) } }
	source := func(src event.Source) func(*event.Event) { return func(e *event.Event) { e.Source = src } }
	mutants := []mutant{
		{"system answer", event.AuthorityResolved, source(event.SourceSystem)},
		{"reviewer answer", event.AuthorityResolved, source(event.SourceReviewer)},
		{"answer naming no option", event.AuthorityResolved, payload(`{"option":""}`)},
		{"answer with no payload", event.AuthorityResolved, payload(``)},
		{"answer with a foreign member", event.AuthorityResolved, payload(`{"option":"1","cleared":true}`)},
		{"answer for another task", event.AuthorityResolved,
			payload(`{"task_id":"another","session_id":"B","decided_at":"2026-01-01T00:00:00Z","question":"q","condition":"c","option_id":"1","option_label":"yes","outcome":"authorize","state":"failed"}`)},
		{"answer naming no outcome", event.AuthorityResolved,
			payload(`{"task_id":"` + lineageTask + `","session_id":"B","decided_at":"2026-01-01T00:00:00Z","question":"q","condition":"c","option_id":"1","option_label":"yes","outcome":"","state":"proposed"}`)},
		{"receipt by an agent", event.RunReceipt, source(event.SourceClaude)},
		{"receipt with no receipt", event.RunReceipt, payload(`{"completeness":"complete","missing":[]}`)},
		{"checkpoint by the reviewer", event.CheckpointCommitted, source(event.SourceReviewer)},
		{"checkpoint of another task", event.CheckpointPrepared,
			payload(`{"checkpoint_id":"` + strings.Repeat("cd", 32) + `","task_id":"another","plan_attempt_id":"a","status":"live","payload_digest":"p","replay_digest":"r"}`)},
		{"checkpoint in no durable state", event.CheckpointCommitted,
			payload(`{"checkpoint_id":"` + strings.Repeat("cd", 32) + `","task_id":"` + lineageTask + `","plan_attempt_id":"a","status":"sleeping","payload_digest":"p","replay_digest":"r"}`)},
		{"terminal by an agent", event.WorkflowFailed, source(event.SourceCodex)},
		{"completion by the user", event.WorkflowCompleted, source(event.SourceUser)},
		{"refusal by the architect", event.WorkflowRestorationRefused, source(event.SourceArchitect)},
		{"terminal carrying a bare string", event.WorkflowFailed, payload(`"failed"`)},
		{"plan attempt refusal by the reviewer", event.PlanAttemptRefused, source(event.SourceReviewer)},
	}
	for _, m := range mutants {
		t.Run(m.name, func(t *testing.T) {
			s := lineageStore(t, "A")
			mustAppend(t, s, created("A"))
			valid := governed("A", m.kind, "valid")
			if err := ValidGovernedEvent(valid); err != nil {
				t.Fatalf("premise: the unmutated %s is not valid: %v", m.kind, err)
			}
			bad := governed("A", m.kind, "mutated")
			m.edit(&bad)
			if err := ValidGovernedEvent(bad); !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("the canonical validator accepted it: %v", err)
			}
			for name, add := range map[string]func(event.Event) error{
				"Append":        func(e event.Event) error { return appendTip(t, s, t.Context(), false, e) },
				"AppendDurable": func(e event.Event) error { return appendTip(t, s, t.Context(), true, e) },
			} {
				if err := add(bad); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedEvent) {
					t.Fatalf("%s took it from the current session: %v", name, err)
				}
			}
			if got := recordOf(t, s); len(got) != 1 {
				t.Fatalf("a refused record was written: %d records", len(got))
			}
			// Control: the valid record of the same kind is taken.
			mustAppend(t, s, valid)
			// What a legacy or hostile writer left is classified malformed,
			// and leaves the task with no lineage to continue over it; the
			// valid record before it keeps its owner.
			writeRaw(t, s, bad)
			classes, _, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
			if !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a malformed record after the root left the lineage establishable: %v", err)
			}
			if c := classes[len(classes)-1]; c.Class != HistoryMalformed {
				t.Fatalf("the written mutant was classified %s", c.Class)
			}
			if c := classes[len(classes)-2]; c.Class != HistoryLineageMember {
				t.Fatalf("the valid record was classified %s", c.Class)
			}
		})
	}
}

// f4 (70B2a1 r7 review): AN ABSENT RECORD IS EXAMINED UNDER THE RECORD LOCK.
// While another writer holds the lock, a lineage read and a binding of an
// absent record wait for it -- and are refused by its bound -- rather than
// concluding "no root" from an unlocked look; and neither creates the record.
func TestB2a1F4AbsentRecordIsExaminedUnderTheRecordLock(t *testing.T) {
	s := lineageStore(t, "A")
	lock := recordLockPath(s.path)
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	release, held, err := tryLockRecordFile(lock)
	if err != nil || !held {
		t.Fatalf("the test could not take the record lock: %v", err)
	}
	wait := recordLockWait
	t.Cleanup(func() { recordLockWait = wait })
	recordLockWait = 100 * time.Millisecond
	if _, err := s.TaskSessionLineage(lineageTask); !errors.Is(err, ErrRecordLockTimeout) {
		t.Fatalf("an absent record's lineage was read without the record lock: %v", err)
	}
	b := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A", CurrentSessionID: "B",
		Relation: SessionLineageRelationResume}
	if _, err := bindLeased(t, s, t.Context(), b); !errors.Is(err, ErrRecordLockTimeout) {
		t.Fatalf("an absent record was refused a binding without the record lock: %v", err)
	}
	release()
	// Under the free lock, the absent record holds no root, and is not created.
	if _, err := s.TaskSessionLineage(lineageTask); !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("an absent record established a lineage: %v", err)
	}
	if _, err := bindLeased(t, s, t.Context(), b); !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("an absent record bound a session: %v", err)
	}
	if _, err := os.Stat(s.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("examining an absent record created it: %v", err)
	}
}

// f5 (70B2a1 r7 review): A GOVERNED APPEND IS ALL OR NOTHING. A write, a sync
// or a verification that fails after the event's bytes reached the record
// leaves the record byte for byte what it was -- every earlier valid event
// intact and readable -- and the failure says it was not recorded; an append
// that created the record and failed leaves no record. When the undo itself
// fails the failure is ErrAppendIndeterminate, never "not recorded".
func TestB2a1F5GovernedAppendIsAllOrNothing(t *testing.T) {
	fault := appendCommitFault
	t.Cleanup(func() { appendCommitFault = fault })
	failAt := func(steps ...string) {
		appendCommitFault = func(step string) error {
			for _, s := range steps {
				if s == step {
					return errors.New("injected " + step + " failure")
				}
			}
			return nil
		}
	}
	for _, c := range []struct {
		step    string
		durable bool
	}{{"write", false}, {"write", true}, {"sync", true}} {
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"), status("A", "earlier valid evidence"))
		before, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		failAt(c.step)
		err = appendTip(t, s, t.Context(), c.durable, status("A", "the failed append"))
		appendCommitFault = fault
		if err == nil || errors.Is(err, ErrAppendIndeterminate) || !strings.Contains(err.Error(), "injected "+c.step) {
			t.Fatalf("a %s failure (durable %v) was not returned as an undone append: %v", c.step, c.durable, err)
		}
		after, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("a %s failure (durable %v) left the record changed: %d bytes became %d", c.step, c.durable, len(before), len(after))
		}
		if got := recordOf(t, s); len(got) != 2 {
			t.Fatalf("earlier evidence did not survive: %d records", len(got))
		}
		// Control: the next lawful append is taken and reads back.
		mustAppend(t, s, status("A", "after the failure"))
		if got := recordOf(t, s); len(got) != 3 || got[2].Summary != "after the failure" {
			t.Fatalf("the record after an undone append is %+v", got)
		}
	}

	// A record the failed append created is removed.
	fresh := lineageStore(t, "A")
	failAt("write")
	err := appendTip(t, fresh, t.Context(), false, created("A"))
	appendCommitFault = fault
	if err == nil || errors.Is(err, ErrAppendIndeterminate) {
		t.Fatalf("a failed first append was not undone: %v", err)
	}
	if _, err := os.Stat(fresh.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a failed first append left a record: %v", err)
	}

	// A write that reports success but leaves the record any other length
	// than the record plus the event -- here a concurrent stray byte -- is
	// caught by the commit's verification and undone.
	torn := lineageStore(t, "A")
	mustAppend(t, torn, created("A"))
	tornBefore, err := os.ReadFile(torn.path)
	if err != nil {
		t.Fatal(err)
	}
	appendCommitFault = func(step string) error {
		if step == "write" {
			f, err := os.OpenFile(torn.path, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write([]byte("x"))
			return err
		}
		return nil
	}
	err = appendTip(t, torn, t.Context(), false, status("A", "torn"))
	appendCommitFault = fault
	if err == nil || errors.Is(err, ErrAppendIndeterminate) {
		t.Fatalf("a record of the wrong length after a write was not refused and undone: %v", err)
	}
	if after, _ := os.ReadFile(torn.path); string(after) != string(tornBefore) {
		t.Fatalf("a torn append left the record changed: %d bytes became %d", len(tornBefore), len(after))
	}

	// An append that cannot be undone is indeterminate.
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	failAt("sync", "rollback")
	err = appendTip(t, s, t.Context(), true, status("A", "neither committed nor undone"))
	appendCommitFault = fault
	if !errors.Is(err, ErrAppendIndeterminate) {
		t.Fatalf("an append that could not be undone was not indeterminate: %v", err)
	}
}

// A1-W6 STALE TIP: after A -> B, a competing A -> C binding is refused, and of
// concurrent binders from one observed tip exactly one wins.
func TestB2a1W6StaleTip(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	atA, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	competing, err := atA.BindingFor("C")
	if err != nil {
		t.Fatal(err)
	}
	bind(t, s, "B")
	before := recordOf(t, s)
	if _, err := bindLeased(t, s, t.Context(), competing); !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("a competing A -> C transition was bound after A -> B: %v", err)
	}
	if got := recordOf(t, s); len(got) != len(before) {
		t.Fatal("a refused transition was written")
	}
	if l, _ := s.TaskSessionLineage(lineageTask); l.Tip() != "B" {
		t.Fatalf("the tip moved to %q", l.Tip())
	}
	// A historical session never becomes current again.
	if _, err := bindLeased(t, s, t.Context(), SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A",
		ParentSessionID: "B", CurrentSessionID: "A", Relation: SessionLineageRelationResume}); !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("B -> A was bound: %v", err)
	}

	// Concurrent binders, each through its own Store over the same physical
	// record, all parented on the tip they observed.
	race := lineageStore(t, "A")
	mustAppend(t, race, created("A"))
	observed, _ := race.TaskSessionLineage(lineageTask)
	root := filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(race.path))))
	var wg sync.WaitGroup
	var mu sync.Mutex
	won := 0
	for i := 0; i < 8; i++ {
		b, err := observed.BindingFor("D" + string(rune('0'+i)))
		if err != nil {
			t.Fatal(err)
		}
		other, err := New(root, "A")
		if err != nil {
			t.Fatal(err)
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := bindLeased(t, other, t.Context(), b); err == nil {
				mu.Lock()
				won++
				mu.Unlock()
			} else if !errors.Is(err, ErrSessionLineage) {
				t.Errorf("a competing binder failed for another reason: %v", err)
			}
		}()
	}
	wg.Wait()
	if won != 1 {
		t.Fatalf("%d of 8 binders parented on A were bound; exactly one may continue A", won)
	}
	if l, err := race.TaskSessionLineage(lineageTask); err != nil || len(l.Segments) != 2 {
		t.Fatalf("the record does not hold exactly one transition: %+v %v", l, err)
	}
}

// A1-W7 PRE-ROOT: events before TaskCreated receive no owner from the later
// root.
func TestB2a1W7PreRoot(t *testing.T) {
	s := lineageStore(t, "A")
	// What a legacy writer left before the root; the Store itself refuses it.
	writeRaw(t, s, status("A", "before the root"))
	mustAppend(t, s, created("A"), status("A", "after the root"))
	classes, l, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	if l.OwnerAt(0) != "" {
		t.Fatalf("position 0 precedes the root and is owned by %q", l.OwnerAt(0))
	}
	want := []HistoryClass{HistoryPreRoot, HistoryLineageMember, HistoryLineageMember}
	if len(classes) != len(want) {
		t.Fatalf("classified %d records, want %d", len(classes), len(want))
	}
	for i, c := range classes {
		if c.Class != want[i] {
			t.Errorf("record %d is %s, want %s", i, c.Class, want[i])
		}
	}
	if classes[0].Writer != "A" {
		t.Fatalf("the pre-root record's writer was rewritten to %q", classes[0].Writer)
	}
	// And a transition before the root establishes nothing.
	early := lineageStore(t, "A")
	writeRaw(t, early, rawBinding(SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A",
		CurrentSessionID: "B", Relation: SessionLineageRelationResume}), created("A"))
	if _, err := early.TaskSessionLineage(lineageTask); !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("a transition preceding the root was accepted: %v", err)
	}
}

// A1-W8 HOLDER MISMATCH: a lineage record naming the wrong HolderSessionID is
// refused even though its Parent/Current pair continues the tip.
func TestB2a1W8HolderMismatch(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	wrong := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "X", ParentSessionID: "A", CurrentSessionID: "B",
		Relation: SessionLineageRelationResume}
	if _, err := bindLeased(t, s, t.Context(), wrong); !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("a transition naming holder X was bound: %v", err)
	}
	if got := recordOf(t, s); len(got) != 1 {
		t.Fatal("a refused transition was written")
	}
	// The same record, already in the file, establishes no lineage: B cannot
	// act on it, and it is malformed to a consumer. The root before it keeps
	// its class (RULING-195).
	writeRaw(t, s, rawBinding(wrong))
	if _, err := s.TaskSessionLineage(lineageTask); !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("a record naming the wrong holder established a lineage: %v", err)
	}
	if err := appendTip(t, s, t.Context(), false, status("B", "acting on a misbound lineage")); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("B appended under a transition naming the wrong holder: %v", err)
	}
	classes, _, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
	if !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("a misbound lineage was classified without refusal: %v", err)
	}
	if len(classes) != 2 || classes[0].Class != HistoryLineageMember || classes[1].Class != HistoryMalformed {
		t.Fatalf("a misbound lineage was classified %+v", classes)
	}
}

// A1-W9 LOCK: lock acquisition is bounded and cancellable, nothing is written
// by a refused wait, and the interruption does not disable later appends.
func TestB2a1W9Lock(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	wait := recordLockWait
	t.Cleanup(func() { recordLockWait = wait })
	// Read before the lock is held: a lineage read waits for the record
	// lock as an append does.
	l, _ := s.TaskSessionLineage(lineageTask)
	b, _ := l.BindingFor("B")
	// A's capability is taken before the lock is held, so every refusal
	// below is the record lock's wait and nothing else.
	leaseA, err := tipLease(t, s, lineageTask)
	if err != nil {
		t.Fatal(err)
	}

	release, held, err := tryLockRecordFile(recordLockPath(s.path))
	if err != nil || !held {
		t.Fatalf("the test could not take the record lock: %v", err)
	}
	recordLockWait = 150 * time.Millisecond
	start := time.Now()
	if err := s.Append(t.Context(), leaseA, status("A", "while the lock is held")); !errors.Is(err, ErrRecordLockTimeout) {
		t.Fatalf("an append past the bounded wait was not refused as a timeout: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("the bounded wait took %s", took)
	}
	recordLockWait = time.Hour
	start = time.Now()
	if _, err := s.BindSessionLineage(cancelledContext{}, leaseA, b); !errors.Is(err, ErrRecordLockCanceled) || !errors.Is(err, errLineageTestCancelled) {
		t.Fatalf("a cancelled binding was not refused as cancelled: %v", err)
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Fatalf("a cancelled wait waited %s", took)
	}
	// An ordinary governed append blocked on the lock ends when its caller
	// cancels it, not only at the bound, and so does a durable one.
	blocked := newClosableContext()
	result := make(chan error, 1)
	go func() { result <- s.Append(blocked, leaseA, status("A", "blocked on the lock")) }()
	select {
	case err := <-result:
		t.Fatalf("an append returned while the lock was still held and its caller had not cancelled: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	blocked.close()
	select {
	case err := <-result:
		if !errors.Is(err, ErrRecordLockCanceled) || !errors.Is(err, errLineageTestCancelled) {
			t.Fatalf("a cancelled governed append was not refused as cancelled: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a cancelled governed append kept waiting for the lock")
	}
	if err := s.AppendDurable(cancelledContext{}, leaseA, status("A", "durable")); !errors.Is(err, ErrRecordLockCanceled) {
		t.Fatalf("a cancelled durable append was not refused as cancelled: %v", err)
	}
	if got := recordOf(t, s); len(got) != 1 {
		t.Fatalf("a refused wait wrote %d records", len(got)-1)
	}
	release()
	leaseA.Release()

	// Nothing durable says "locked": the next lawful appends go through.
	recordLockWait = 150 * time.Millisecond
	mustAppend(t, s, status("A", "after the holder let go"))
	bind(t, s, "B")
	mustAppend(t, s, status("B", "B after binding"))
	entries, err := os.ReadDir(filepath.Dir(s.path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != filepath.Base(s.path) && entry.Name() != filepath.Base(filepath.Dir(recordLockPath(s.path))) {
			t.Fatalf("the record lock left durable state beside the record: %s", entry.Name())
		}
	}
	// Its lock file is a stable, empty target: no durable state says
	// "locked".
	if info, err := os.Stat(recordLockPath(s.path)); err != nil || info.Size() != 0 {
		t.Fatalf("the record's lock file is not an empty lock target: %v %+v", err, info)
	}
}

// A1-W9 LOCK, FREE AND ALREADY CANCELLED: a caller whose context has ended
// takes no record lock even when nobody holds it. A binding, a governed
// append and a durable append are each refused as cancelled, the ledger stays
// byte-for-byte as it was, and the lock is left free for a lawful writer.
func TestB2a1W9LockRefusesACancelledCallerOnAFreeLock(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"), status("A", "before"))
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.BindingFor("B")
	if err != nil {
		t.Fatal(err)
	}
	attempts := map[string]func() error{
		"BindSessionLineage": func() error {
			_, err := bindLeased(t, s, cancelledContext{}, b)
			return err
		},
		"AppendContext":        func() error { return appendTip(t, s, cancelledContext{}, false, status("A", "cancelled")) },
		"AppendDurableContext": func() error { return appendTip(t, s, cancelledContext{}, true, status("A", "cancelled durable")) },
	}
	for name, attempt := range attempts {
		if err := attempt(); !errors.Is(err, ErrRecordLockCanceled) || !errors.Is(err, errLineageTestCancelled) {
			t.Fatalf("%s on a free lock with a cancelled context was not refused as cancelled: %v", name, err)
		}
		after, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("%s with a cancelled context changed the ledger: %d bytes became %d", name, len(before), len(after))
		}
		release, held, err := tryLockRecordFile(recordLockPath(s.path))
		if err != nil || !held {
			t.Fatalf("%s with a cancelled context left the record lock held: %v", name, err)
		}
		release()
	}
	if bound := bind(t, s, "B"); bound.Tip() != "B" {
		t.Fatalf("a lawful binding after the cancelled attempts did not take: %+v", bound)
	}
}

// parkedContext is a caller context that reports when the waiter it is
// handed has parked on it: every Done call -- made only where a record-lock
// wait selects on the caller -- signals parked once parkedWhen holds. The test
// cancels only after that signal, so a cancellation can reach the waiter only
// through the wait itself, never through a check made before it.
type parkedContext struct {
	closableContext
	parked     chan struct{}
	parkedWhen func() bool
}

func newParkedContext(parkedWhen func() bool) parkedContext {
	return parkedContext{closableContext: newClosableContext(), parked: make(chan struct{}, 1), parkedWhen: parkedWhen}
}

func (c parkedContext) Done() <-chan struct{} {
	if c.parkedWhen() {
		select {
		case c.parked <- struct{}{}:
		default:
		}
	}
	return c.done
}

// A1-W9 LOCK, CANCELLED WHILE PARKED (RULING-205/206 M5): an append parked
// behind a holder the test controls -- this Store's own writer gate, then the
// record's OS lock -- ends, typed and with nothing written, when its caller
// cancels, and only through the wait it is parked in. Neither the bound nor
// the poll can release it: both are an hour, so a wait that ignored its
// caller would never return. Synchronization is the waiter's own Done call;
// nothing sleeps.
func TestB2a1W9LockCancellationReachesAParkedWait(t *testing.T) {
	wait, poll := recordLockWait, recordLockPoll
	t.Cleanup(func() { recordLockWait, recordLockPoll = wait, poll })
	recordLockWait, recordLockPoll = time.Hour, time.Hour

	// parkedOn parks one append on the lock the test holds, cancels it once
	// it has parked, and lets the holder go (letGo) exactly once on every
	// path. A wait that ignores its caller is reported and abandoned -- it is
	// not waited for, and its lease is not released under it -- so it fails
	// the test instead of outliving it. It reports whether the wait ended as
	// the law requires.
	parkedOn := func(t *testing.T, s *Store, lease *TaskLease, ctx parkedContext, letGo func()) bool {
		t.Helper()
		before, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		result := make(chan error, 1)
		go func() { result <- s.Append(ctx, lease, status("A", "parked on the lock")) }()
		select {
		case <-ctx.parked:
		case err := <-result:
			letGo()
			t.Errorf("the append returned before it parked on the held lock: %v", err)
			return false
		case <-time.After(5 * time.Second):
			letGo()
			t.Error("the append waited on the held lock without ever selecting on its caller's cancellation")
			return false
		}
		ctx.close()
		select {
		case err := <-result:
			letGo()
			if !errors.Is(err, ErrRecordLockCanceled) || !errors.Is(err, errLineageTestCancelled) {
				t.Errorf("the parked append was not refused as cancelled: %v", err)
				return false
			}
		case <-time.After(5 * time.Second):
			letGo()
			t.Error("the cancelled append stayed parked on the lock: its caller's cancellation did not end the wait")
			return false
		}
		after, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(after) != string(before) {
			t.Fatalf("the cancelled wait changed the record: %d bytes became %d", len(before), len(after))
		}
		return true
	}

	t.Run("the Store's own writer gate", func(t *testing.T) {
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"))
		lease, err := tipLease(t, s, lineageTask)
		if err != nil {
			t.Fatal(err)
		}
		s.gateOnce.Do(func() { s.gate = make(chan struct{}, 1) })
		s.gate <- struct{}{}
		if !parkedOn(t, s, lease, newParkedContext(func() bool { return true }), func() { <-s.gate }) {
			return
		}
		defer lease.Release()
		if err := s.Append(t.Context(), lease, status("A", "after the gate was let go")); err != nil {
			t.Fatalf("the writer gate stayed unusable after the cancelled wait: %v", err)
		}
	})

	t.Run("the record's OS lock", func(t *testing.T) {
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"))
		lease, err := tipLease(t, s, lineageTask)
		if err != nil {
			t.Fatal(err)
		}
		release, held, err := tryLockRecordFile(recordLockPath(s.path))
		if err != nil || !held {
			t.Fatalf("the test could not take the record lock: %v", err)
		}
		// The gate is free, so the waiter holds it once it is past it: a Done
		// call then is the OS-lock poll's, after an attempt the held lock
		// refused.
		s.gateOnce.Do(func() { s.gate = make(chan struct{}, 1) })
		if !parkedOn(t, s, lease, newParkedContext(func() bool { return len(s.gate) == 1 }), release) {
			return
		}
		defer lease.Release()
		if len(s.gate) != 0 {
			t.Fatal("the cancelled wait kept this Store's writer gate")
		}
		if err := s.Append(t.Context(), lease, status("A", "after the holder let go")); err != nil {
			t.Fatalf("the record lock stayed unusable after the cancelled wait: %v", err)
		}
	})
}

// A1-W4 BIND, AT THE SIZE BOUNDARY: the binding operation bounds its
// transition exactly as a generic append bounds an event. A transition Load
// could not read back is refused, typed, before the lock is taken and before
// any byte is written, so the ledger stays byte-for-byte as it was and
// readable, and a lawful binding still goes through.
func TestB2a1W4BindRefusesAnOversizedTransition(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"), status("A", "before"))
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.BindingFor(strings.Repeat("B", maxSessionEvent/2+1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bindLeased(t, s, t.Context(), b); !errors.Is(err, ErrSessionEventTooLarge) {
		t.Fatalf("an oversized transition was not refused as too large: %v", err)
	}
	after, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("the refused transition changed the ledger: %d bytes became %d", len(before), len(after))
	}
	if _, err := s.Load(); err != nil {
		t.Fatalf("the ledger is unreadable after the refusal: %v", err)
	}
	if bound := bind(t, s, "B"); bound.Tip() != "B" {
		t.Fatalf("a lawful binding after the refusal did not take: %+v", bound)
	}
}

// A1-W11 HISTORICAL CLASSIFICATION: valid ancestor events are consumable;
// unrelated same-task events are foreign.
func TestB2a1W11HistoricalClassification(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"), status("A", "ancestor"))
	bind(t, s, "B")
	mustAppend(t, s, status("B", "current"))
	// What the Store would refuse, left in the file by another writer.
	writeRaw(t, s, status("C", "unrelated"), status("A", "superseded ancestor"))
	mustAppend(t, s, event.New("Z", "other-task", event.SourceSystem, event.TaskCreated, "another task", nil))

	classes, l, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	if l.Tip() != "B" || l.HolderSessionID != "A" {
		t.Fatalf("the classifier read the wrong lineage: %+v", l)
	}
	want := []struct {
		writer string
		kind   event.Kind
		class  HistoryClass
	}{
		{"A", event.TaskCreated, HistoryLineageMember},
		{"A", event.Status, HistoryLineageMember},
		{"B", SessionLineageBound, HistoryLineageMember},
		{"B", event.Status, HistoryLineageMember},
		{"C", event.Status, HistoryForeign},
		{"A", event.Status, HistoryForeign},
	}
	if len(classes) != len(want) {
		t.Fatalf("classified %d records of the task, want %d: %+v", len(classes), len(want), classes)
	}
	for i, w := range want {
		c := classes[i]
		if c.Writer != w.writer || c.Event.Kind != w.kind || c.Class != w.class {
			t.Errorf("record %d: %s by %s is %s; want %s by %s as %s", i, c.Event.Kind, c.Writer, c.Class, w.kind, w.writer, w.class)
		}
	}
}

// A1-W12 OWNERSHIP: historical record ownership stays its exact SessionID after
// descendants are added; a checkpoint of the holder session stays the holder's
// and is not owned by the descendant after A -> B.
func TestB2a1W12Ownership(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"), status("A", "A's work"))
	cp := CheckpointBinding{CheckpointID: strings.Repeat("ab", 32), TaskID: lineageTask, PlanAttemptID: "attempt-1",
		Status: CheckpointLive, PayloadDigest: "payload", ReplayDigest: "replay"}
	for _, k := range []event.Kind{event.CheckpointPrepared, event.CheckpointCommitted} {
		if err := appendTip(t, s, t.Context(), true, event.New("A", lineageTask, event.SourceSystem, k, "checkpoint", cp)); err != nil {
			t.Fatal(err)
		}
	}
	before := recordOf(t, s)
	bind(t, s, "B")
	mustAppend(t, s, status("B", "B's work"))
	bind(t, s, "C")
	mustAppend(t, s, status("C", "C's work"))
	after := recordOf(t, s)

	for i, e := range before {
		if after[i].SessionID != e.SessionID || after[i].ID != e.ID {
			t.Fatalf("record %d (%s) was written by %q and now reads %q", i, e.Kind, e.SessionID, after[i].SessionID)
		}
	}
	if got, ok := CommittedCheckpoint(after, lineageTask, "A"); !ok || got != cp {
		t.Fatal("the holder's checkpoint is no longer the holder's")
	}
	for _, descendant := range []string{"B", "C"} {
		if _, ok := CommittedCheckpoint(after, lineageTask, descendant); ok {
			t.Fatalf("descendant %s owns the holder's checkpoint", descendant)
		}
		for _, r := range CheckpointRecords(after, lineageTask) {
			if r.owns(lineageTask, descendant) {
				t.Fatalf("descendant %s owns a %s record written by %s", descendant, r.Kind, r.SessionID)
			}
		}
	}
	classes, _, err := ClassifyTaskHistory(after, lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range classes {
		if c.Writer != c.Event.SessionID {
			t.Fatalf("record %d's writer reads %q; it was written by %q", c.Index, c.Writer, c.Event.SessionID)
		}
		if c.Event.Kind == event.CheckpointCommitted && (c.Writer != "A" || c.Class != HistoryLineageMember) {
			t.Fatalf("the holder's checkpoint is %s by %s", c.Class, c.Writer)
		}
	}
}

// RULING-193 W10, A LINEAGE COMMIT IS ALL OR NOTHING (70B2a1 r5 review c1
// f3): BindSessionLineage
// never returns a refusal after changing the record, and never refuses a
// transition the record holds. A failure at any step of the commit -- once
// the transition's bytes are written, once they are synced, or when the
// committed record is read back -- leaves the record byte for byte as it was
// and refuses the binding, so the holder stays the tip and B cannot append.
// When the undo itself fails, the record's own bytes decide: a transition
// fully written is returned BOUND, and B's appends are authorized.
func TestB2a1R193W10BindCommitIsAllOrNothing(t *testing.T) {
	failing := func(t *testing.T, steps ...string) {
		t.Helper()
		saved := lineageCommitFault
		t.Cleanup(func() { lineageCommitFault = saved })
		lineageCommitFault = func(step string) error {
			for _, s := range steps {
				if s == step {
					return errors.New("injected failure at " + step)
				}
			}
			return nil
		}
	}
	setup := func(t *testing.T) (*Store, SessionLineageBinding, string) {
		t.Helper()
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"), status("A", "A works"))
		l, err := s.TaskSessionLineage(lineageTask)
		if err != nil {
			t.Fatal(err)
		}
		b, err := l.BindingFor("B")
		if err != nil {
			t.Fatal(err)
		}
		before, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		return s, b, string(before)
	}
	for _, step := range []string{"write", "sync", "readback"} {
		t.Run("refused and unchanged after a failed "+step, func(t *testing.T) {
			s, b, before := setup(t)
			failing(t, step)
			if l, err := bindLeased(t, s, t.Context(), b); err == nil || !strings.Contains(err.Error(), "injected failure at "+step) {
				t.Fatalf("a binding whose %s failed was not refused with that failure: %+v %v", step, l, err)
			} else if errors.Is(err, ErrSessionLineageIndeterminate) {
				t.Fatalf("an undone binding was reported indeterminate: %v", err)
			}
			after, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != before {
				t.Fatalf("a refused binding changed the record: %d bytes became %d", len(before), len(after))
			}
			lineageCommitFault = func(string) error { return nil }
			if l, err := s.TaskSessionLineage(lineageTask); err != nil || l.Tip() != "A" {
				t.Fatalf("after a refused binding the tip is %+v %v, not the holder", l, err)
			}
			if err := appendTip(t, s, t.Context(), false, status("B", "B after a refused binding")); !errors.Is(err, ErrSessionAppendRefused) {
				t.Fatalf("a refused binding authorized B: %v", err)
			}
			// The record is unharmed: the same transition then commits.
			if bound := bind(t, s, "B"); bound.Tip() != "B" {
				t.Fatalf("the binding did not commit once the failure cleared: %+v", bound)
			}
		})
	}
	// A synced transition is durable: when its undo fails, the record's own
	// bytes decide, and they bind B.
	t.Run("bound when the undo of a failed readback fails", func(t *testing.T) {
		s, b, before := setup(t)
		failing(t, "readback", "rollback")
		l, err := bindLeased(t, s, t.Context(), b)
		if err != nil || l.Tip() != "B" || l.HolderSessionID != "A" {
			t.Fatalf("a durable transition the record holds was not reported bound: %+v %v", l, err)
		}
		lineageCommitFault = func(string) error { return nil }
		after, err := os.ReadFile(s.path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(string(after), before) || len(after) <= len(before) {
			t.Fatal("the record does not hold the committed transition after its prior bytes")
		}
		if read, err := s.TaskSessionLineage(lineageTask); err != nil || read.Tip() != "B" {
			t.Fatalf("the record does not establish B as reported: %+v %v", read, err)
		}
		mustAppend(t, s, status("B", "B works"))
	})
	// 70B2a1 r7 review f2: a transition written but never known to be synced
	// is visible and not durable. When its undo fails, a readback that shows
	// it proves nothing a crash cannot take back, so the binding is
	// indeterminate and makes no session operative.
	for _, step := range []string{"write", "sync"} {
		t.Run("indeterminate when the undo of a failed "+step+" fails", func(t *testing.T) {
			s, b, before := setup(t)
			failing(t, step, "rollback")
			l, err := bindLeased(t, s, t.Context(), b)
			if !errors.Is(err, ErrSessionLineageIndeterminate) || l.Tip() != "" {
				t.Fatalf("an unsynced transition whose undo failed was reported %+v %v, not indeterminate", l, err)
			}
			after, rerr := os.ReadFile(s.path)
			if rerr != nil {
				t.Fatal(rerr)
			}
			if !strings.HasPrefix(string(after), before) || len(after) <= len(before) {
				t.Fatal("the fault did not leave the unsynced transition readable: the witness tests nothing")
			}
		})
	}
}

// 70B2a1 r7 review f2: a lineage read is serialized with binding and append
// by the same record lock. While a binding of B is between its write and its
// verification -- its bytes readable, its commit unproven -- a read through
// another Store of the same record cannot observe B as the tip: it waits for
// the lock, and its caller's bound ends that wait typed. Once the binding has
// settled the read answers.
func TestB2a1R7LineageReadIsSerializedWithBinding(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	l, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.BindingFor("B")
	if err != nil {
		t.Fatal(err)
	}
	reader := &Store{path: s.path}
	saved := lineageCommitFault
	t.Cleanup(func() { lineageCommitFault = saved })
	var observed SessionLineage
	var readErr error
	reads := 0
	lineageCommitFault = func(step string) error {
		if step != "readback" {
			return nil
		}
		reads++
		ctx := newClosableContext()
		timer := time.AfterFunc(50*time.Millisecond, ctx.close)
		defer timer.Stop()
		observed, readErr = reader.TaskSessionLineageContext(ctx, lineageTask)
		return nil
	}
	if _, err := bindLeased(t, s, t.Context(), b); err != nil {
		t.Fatal(err)
	}
	if reads != 1 {
		t.Fatalf("the read ran %d times during the binding: the witness tests nothing", reads)
	}
	if !errors.Is(readErr, ErrRecordLockCanceled) || observed.Tip() != "" {
		t.Fatalf("a read during an unverified binding answered %+v %v instead of waiting on the record lock", observed, readErr)
	}
	lineageCommitFault = saved
	if read, err := reader.TaskSessionLineageContext(t.Context(), lineageTask); err != nil || read.Tip() != "B" {
		t.Fatalf("after the binding settled the read answered %+v %v", read, err)
	}
	// An absent record holds no root, and the read does not create it.
	absent := lineageStore(t, "Z")
	if _, err := absent.TaskSessionLineageContext(t.Context(), lineageTask); !errors.Is(err, ErrNoTaskRoot) {
		t.Fatalf("an absent record answered %v, not ErrNoTaskRoot", err)
	}
	if _, err := os.Stat(absent.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reading an absent record's lineage created it: %v", err)
	}
}

// 70B2a1 r7 review f1: the task INVOCATION LEASE is the Store's, OS-backed,
// and held for an invocation's lifetime. A second invocation of the task
// through another Store of the same record -- what another Engine or process
// opens -- is refused, typed and bounded, while the first holds it; another
// task is not; a cancelled caller takes no lease; and release -- or the
// holder's death, which closes the descriptor the same way -- frees it for a
// later lawful invocation.
func TestB2a1R7TaskInvocationLeaseExcludesAcrossStores(t *testing.T) {
	savedWait := taskLeaseWait
	t.Cleanup(func() { taskLeaseWait = savedWait })
	taskLeaseWait = 30 * time.Millisecond
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	other := &Store{path: s.path}

	release, err := s.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	if _, err := other.AcquireTaskInvocation(t.Context(), lineageTask); !errors.Is(err, ErrTaskInvocationLeased) {
		t.Fatalf("a competing invocation through another Store was not refused as leased: %v", err)
	}
	if waited := time.Since(start); waited > 5*time.Second {
		t.Fatalf("the refused acquisition waited %s, past its bound", waited)
	}
	if _, err := other.AcquireTaskInvocation(cancelledContext{}, lineageTask); !errors.Is(err, ErrRecordLockCanceled) {
		t.Fatalf("a cancelled caller was not refused typed: %v", err)
	}
	if after, err := os.ReadFile(s.path); err != nil || string(after) != string(before) {
		t.Fatalf("a refused lease changed the record: %v", err)
	}
	if r, err := other.AcquireTaskInvocation(t.Context(), "another-task"); err != nil {
		t.Fatalf("one task's lease excluded another task: %v", err)
	} else {
		r.Release()
	}
	release.Release()
	release.Release() // idempotent
	again, err := other.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatalf("a released lease was not available to a later invocation: %v", err)
	}
	again.Release()
	var none *Store
	if _, err := none.AcquireTaskInvocation(t.Context(), lineageTask); !errors.Is(err, ErrNoStore) {
		t.Fatalf("no Store leased an invocation: %v", err)
	}
}

// malformedRoots are TaskCreated records of lineageTask written under a
// session, each missing one part of the ordinary framing validTaskRoot
// requires of a root.
func malformedRoots() map[string]event.Event {
	noID := created("A")
	noID.ID = ""
	noTime := created("A")
	noTime.Time = time.Time{}
	agentSource := created("A")
	agentSource.Source = event.SourceClaude
	noSource := created("A")
	noSource.Source = ""
	unknownSource := created("A")
	unknownSource.Source = "someone"
	// A root carries no payload (RULING-200 A): one that does is no root.
	withPayload := created("A")
	withPayload.Payload = json.RawMessage(`{"holder":"A"}`)
	return map[string]event.Event{"no event ID": noID, "no timestamp": noTime,
		"an agent source": agentSource, "no source": noSource, "a source outside the vocabulary": unknownSource,
		"a payload": withPayload}
}

// RULING-193 W1, A1-W3/W8 ROOT FRAMING (70B2a1 r5 review c2 f2, RULING-191
// B): only a well-framed
// TaskCreated root establishes a holder. A first TaskCreated that names a
// session but lacks its event ID, timestamp or creation source -- whether a
// legacy writer left it in the file or a caller offers it to a generic
// append -- proves no holder, authorizes no append or binding, and leaves the
// record byte for byte as it was. A holder minted under an earlier identity
// scheme still roots a well-framed record.
func TestB2a1R193W1MalformedRootEstablishesNoHolder(t *testing.T) {
	for name, root := range malformedRoots() {
		t.Run(name, func(t *testing.T) {
			// What a legacy or hostile writer left in the file.
			raw := lineageStore(t, "A")
			writeRaw(t, raw, root)
			before, err := os.ReadFile(raw.path)
			if err != nil {
				t.Fatal(err)
			}
			if holder, err := raw.HolderSessionID(lineageTask); !errors.Is(err, ErrSessionLineage) || !errors.Is(err, ErrMalformedTaskRoot) {
				t.Fatalf("a root with %s proved holder %q: %v", name, holder, err)
			}
			if err := appendTip(t, raw, t.Context(), false, status("A", "work under the malformed root's writer")); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedTaskRoot) {
				t.Fatalf("a root with %s authorized an append: %v", name, err)
			}
			if err := appendTip(t, raw, t.Context(), true, status("A", "durable work")); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedTaskRoot) {
				t.Fatalf("a root with %s authorized a durable append: %v", name, err)
			}
			b := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A",
				CurrentSessionID: "B", Relation: SessionLineageRelationResume}
			if _, err := bindLeased(t, raw, t.Context(), b); !errors.Is(err, ErrMalformedTaskRoot) {
				t.Fatalf("a root with %s bound a session: %v", name, err)
			}
			after, err := os.ReadFile(raw.path)
			if err != nil {
				t.Fatal(err)
			}
			if string(after) != string(before) {
				t.Fatalf("refusals under a root with %s changed the record:\n%s", name, after[len(before):])
			}
			classes, _, err := ClassifyTaskHistory(recordOf(t, raw), lineageTask)
			if !errors.Is(err, ErrMalformedTaskRoot) || len(classes) != 1 || classes[0].Class != HistoryMalformed {
				t.Fatalf("a root with %s was classified %+v: %v", name, classes, err)
			}

			// Offered to the generic appends, it is never written.
			s := lineageStore(t, "A")
			mustAppend(t, s, event.New("A", "another-task", event.SourceSystem, event.TaskCreated, "another task", nil))
			before, err = os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			if err := appendTip(t, s, t.Context(), false, root); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedTaskRoot) {
				t.Fatalf("a root with %s was appended: %v", name, err)
			}
			if err := appendTip(t, s, t.Context(), true, root); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedTaskRoot) {
				t.Fatalf("a root with %s was durably appended: %v", name, err)
			}
			if after, err := os.ReadFile(s.path); err != nil || string(after) != string(before) {
				t.Fatalf("refused roots with %s changed the record (%v)", name, err)
			}
			if _, err := s.HolderSessionID(lineageTask); !errors.Is(err, ErrNoTaskRoot) {
				t.Fatalf("the task acquired a root with %s: %v", name, err)
			}
		})
	}
	// The control: the same framing with a legacy holder ID, and a root the
	// user's objective is attributed to, each root the task.
	for _, root := range []event.Event{created("s1"),
		event.New("session-20260101T000000.000000000Z", lineageTask, event.SourceUser, event.TaskCreated, "the objective", nil)} {
		s := lineageStore(t, "A")
		mustAppend(t, s, root)
		if holder, err := s.HolderSessionID(lineageTask); err != nil || holder != root.SessionID {
			t.Fatalf("a well-framed root by %s proved %q: %v", root.SessionID, holder, err)
		}
	}
}

// malformedTransitions is, for the binding A -> B of a task rooted by A, the
// SessionLineageBound records that look like it but are not well framed or
// are not the system's -- each one field away from the lawful transition.
// bindLeased binds b through s as an invocation does: under the task's
// invocation lease, taken through s and released once the binding returns.
// A lease that cannot be taken is the binding's refusal.
func bindLeased(t *testing.T, s *Store, ctx waitContext, b SessionLineageBinding) (SessionLineage, error) {
	t.Helper()
	lease, err := s.AcquireTaskInvocation(t.Context(), b.TaskID)
	if err != nil {
		return SessionLineage{}, err
	}
	defer lease.Release()
	return s.BindSessionLineage(ctx, lease, b)
}

func malformedTransitions() map[string]event.Event {
	b := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A",
		CurrentSessionID: "B", Relation: SessionLineageRelationResume}
	noID := rawBinding(b)
	noID.ID = ""
	noTime := rawBinding(b)
	noTime.Time = time.Time{}
	noSource := rawBinding(b)
	noSource.Source = ""
	agentSource := rawBinding(b)
	agentSource.Source = event.SourceClaude
	userSource := rawBinding(b)
	userSource.Source = event.SourceUser
	noPayload := rawBinding(b)
	noPayload.Payload = nil
	extraField := rawBinding(b)
	extraField.Payload = json.RawMessage(`{"task_id":"` + lineageTask + `","holder_session_id":"A","parent_session_id":"A",` +
		`"current_session_id":"B","relation":"` + SessionLineageRelationResume + `","authority":"all"}`)
	otherWriter := rawBinding(b)
	otherWriter.SessionID = "C"
	return map[string]event.Event{"no event ID": noID, "no timestamp": noTime, "no source": noSource,
		"an agent source": agentSource, "the user's source": userSource, "no payload": noPayload,
		"an undeclared payload field": extraField, "a writer that is not the bound session": otherWriter}
}

// RULING-193 W2: A MALFORMED LINEAGE BINDING IS REJECTED (70B2a1 r5 review c3
// f4). A SessionLineageBound record left in the file that is one field away
// from the lawful A -> B transition -- no event ID, no timestamp, no or a
// non-system source, a missing or widened payload, a writer that is not the
// session it binds -- establishes no lineage at all: the holder cannot be
// proven through it, B is authorized to append nothing, nothing more can be
// bound, its records classify as malformed, and the refusals leave the
// record byte for byte as it was. CONTROL: the lawful transition, written the
// same raw way, makes B the tip.
func TestB2a1R193W2MalformedLineageBindingIsRejected(t *testing.T) {
	for name, transition := range malformedTransitions() {
		t.Run(name, func(t *testing.T) {
			s := lineageStore(t, "A")
			mustAppend(t, s, created("A"))
			writeRaw(t, s, transition)
			before, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			if l, err := s.TaskSessionLineage(lineageTask); !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a transition with %s established lineage %+v: %v", name, l, err)
			}
			for _, writer := range []string{"A", "B", "C"} {
				if err := appendTip(t, s, t.Context(), false, status(writer, "work")); !errors.Is(err, ErrSessionAppendRefused) {
					t.Fatalf("under a transition with %s, %s appended: %v", name, writer, err)
				}
			}
			b := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "B",
				CurrentSessionID: "D", Relation: SessionLineageRelationResume}
			if _, err := bindLeased(t, s, t.Context(), b); !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a transition with %s let D bind: %v", name, err)
			}
			classes, _, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
			if !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a transition with %s was classified without error: %v", name, err)
			}
			// The root keeps its class; the transition and all after it are
			// malformed (RULING-195).
			for i, c := range classes {
				if want := map[bool]HistoryClass{true: HistoryLineageMember, false: HistoryMalformed}[i == 0]; c.Class != want {
					t.Fatalf("under a transition with %s, record %d classified %s, want %s", name, c.Index, c.Class, want)
				}
			}
			if after, err := os.ReadFile(s.path); err != nil || string(after) != string(before) {
				t.Fatalf("refusals under a transition with %s changed the record (%v)", name, err)
			}
		})
	}
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	writeRaw(t, s, rawBinding(SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A",
		CurrentSessionID: "B", Relation: SessionLineageRelationResume}))
	if l, err := s.TaskSessionLineage(lineageTask); err != nil || l.Tip() != "B" {
		t.Fatalf("control: the lawful transition did not make B current: %+v %v", l, err)
	}
	mustAppend(t, s, status("B", "lawful"))
}

// RULING-193 W3: A FOREIGN SESSION OR A WRONG SOURCE IS REJECTED. After
// A -> B, a generic append of the task is refused when it is written by a
// session that is not the tip -- unrelated, superseded, unnamed -- and when
// it is not well framed: no event ID, no timestamp, or a source outside the
// closed vocabulary, even from the tip itself. A root offered under a
// source that does not create tasks is refused too. Nothing is written.
// CONTROL: the tip's well-framed record, under any source of the vocabulary,
// is appended.
func TestB2a1R193W3ForeignSessionAndWrongSourceAreRejected(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	bind(t, s, "B")
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	refused := map[string]event.Event{
		"an unrelated session":  status("C", "foreign"),
		"the superseded holder": status("A", "stale"),
		"no session":            status("", "unowned"),
	}
	noID := status("B", "no ID")
	noID.ID = ""
	refused["the tip, with no event ID"] = noID
	noTime := status("B", "no time")
	noTime.Time = time.Time{}
	refused["the tip, with no timestamp"] = noTime
	noSource := status("B", "no source")
	noSource.Source = ""
	refused["the tip, with no source"] = noSource
	unknown := status("B", "unknown source")
	unknown.Source = "someone"
	refused["the tip, with a source outside the vocabulary"] = unknown
	for name, e := range refused {
		if err := appendTip(t, s, t.Context(), false, e); !errors.Is(err, ErrSessionAppendRefused) {
			t.Errorf("%s appended: %v", name, err)
		}
		if err := appendTip(t, s, t.Context(), true, e); !errors.Is(err, ErrSessionAppendRefused) {
			t.Errorf("%s appended durably: %v", name, err)
		}
	}
	other := lineageStore(t, "A")
	agentRoot := created("A")
	agentRoot.Source = event.SourceReviewer
	if err := appendTip(t, other, t.Context(), false, agentRoot); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedTaskRoot) {
		t.Errorf("a root attributed to a reviewer was appended: %v", err)
	}
	if after, err := os.ReadFile(s.path); err != nil || string(after) != string(before) {
		t.Fatalf("refused appends changed the record (%v)", err)
	}
	for _, source := range []event.Source{event.SourceSystem, event.SourceArchitect, event.SourceReviewer, event.SourceClaude} {
		if err := appendTip(t, s, t.Context(), false, event.New("B", lineageTask, source, event.Status, "lawful", nil)); err != nil {
			t.Errorf("control: the tip's well-framed %s record was refused: %v", source, err)
		}
	}
}

// RULING-195 (70B2a1 r6 review f3): A MALFORMED LATER TRANSITION PRESERVES
// PRE-ROOT EVIDENCE. A record holding a pre-root event, a valid TaskCreated
// root and then a malformed SessionLineageBound record establishes no lineage
// -- the transition is refused and nothing is authorized -- but the event
// that physically precedes the root is still classified pre-root, exactly as
// it is when no malformed transition follows. The malformed state reaches the
// root and everything after it, and nothing before. Every malformed
// transition shape is tried.
func TestB2a1R195MalformedLaterLineagePreservesPreRootEvidence(t *testing.T) {
	for name, transition := range malformedTransitions() {
		t.Run(name, func(t *testing.T) {
			s := lineageStore(t, "A")
			writeRaw(t, s, status("A", "before the root"))
			mustAppend(t, s, created("A"), status("A", "after the root"))
			writeRaw(t, s, transition, status("B", "after the malformed transition"))
			if _, err := s.TaskSessionLineage(lineageTask); !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a malformed later transition (%s) established a lineage: %v", name, err)
			}
			if err := appendTip(t, s, t.Context(), false, status("B", "acting on it")); !errors.Is(err, ErrSessionAppendRefused) {
				t.Fatalf("B appended under a malformed transition (%s): %v", name, err)
			}
			classes, l, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
			if !errors.Is(err, ErrSessionLineage) || len(l.Segments) != 0 {
				t.Fatalf("a malformed later transition (%s) was classified as a lineage %+v: %v", name, l, err)
			}
			// The validated prefix keeps its classes: the root and the
			// holder's record after it are lineage members; the refused
			// transition and what follows it are malformed (RULING-195).
			want := []HistoryClass{HistoryPreRoot, HistoryLineageMember, HistoryLineageMember, HistoryMalformed, HistoryMalformed}
			if len(classes) != len(want) {
				t.Fatalf("classified %d records, want %d", len(classes), len(want))
			}
			for i, c := range classes {
				if c.Class != want[i] {
					t.Errorf("record %d (%s) is %s, want %s", i, c.Event.Kind, c.Class, want[i])
				}
				if c.Writer != c.Event.SessionID {
					t.Errorf("record %d's writer was rewritten to %q", i, c.Writer)
				}
			}
		})
	}
	// The same shape with a transition that precedes the root: what precedes
	// the refused transition has no owner; it and everything after it are
	// malformed.
	early := lineageStore(t, "A")
	writeRaw(t, early, status("A", "before everything"), rawBinding(SessionLineageBinding{TaskID: lineageTask,
		HolderSessionID: "A", ParentSessionID: "A", CurrentSessionID: "B", Relation: SessionLineageRelationResume}), created("A"))
	classes, _, err := ClassifyTaskHistory(recordOf(t, early), lineageTask)
	if !errors.Is(err, ErrSessionLineage) || len(classes) != 3 || classes[0].Class != HistoryPreRoot ||
		classes[1].Class != HistoryMalformed || classes[2].Class != HistoryMalformed {
		t.Fatalf("a transition preceding the root was classified %+v: %v", classes, err)
	}
}

// 70B2a1 cycle-3 review f1: THE SAME-STORE WAIT IS BOUNDED AND CANCELLABLE.
// While one operation holds this Store's own serialization, a second append,
// lineage read or binding through the SAME Store is refused typed when its
// caller cancels, and by the bound otherwise -- it never waits on an
// unconditional mutex -- and nothing is written. Once the holder releases, a
// lawful append is taken.
func TestB2a1C3F1SameStoreWaitIsBoundedAndCancellable(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	before := recordOf(t, s)
	// A's capability, taken before the record lock is held: each operation
	// below waits on that lock and nothing else.
	leaseA, err := tipLease(t, s, lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	release, err := s.lockRecord(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A", CurrentSessionID: "B",
		Relation: SessionLineageRelationResume}
	ops := map[string]func(c closableContext) error{
		"Append":        func(c closableContext) error { return s.Append(c, leaseA, status("A", "queued")) },
		"AppendDurable": func(c closableContext) error { return s.AppendDurable(c, leaseA, status("A", "queued")) },
		"Lineage": func(c closableContext) error {
			_, err := s.TaskSessionLineageContext(c, lineageTask)
			return err
		},
		"Bind": func(c closableContext) error {
			_, err := s.BindSessionLineage(c, leaseA, b)
			return err
		},
	}
	for name, op := range ops {
		ctx := newClosableContext()
		done := make(chan error, 1)
		go func() { done <- op(ctx) }()
		time.Sleep(20 * time.Millisecond)
		ctx.close()
		select {
		case err := <-done:
			if !errors.Is(err, ErrRecordLockCanceled) {
				t.Fatalf("%s queued on the same Store was not refused as cancelled: %v", name, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s queued on the same Store did not observe its caller's cancellation", name)
		}
	}
	wait := recordLockWait
	t.Cleanup(func() { recordLockWait = wait })
	recordLockWait = 50 * time.Millisecond
	if err := s.Append(t.Context(), leaseA, status("A", "bounded")); !errors.Is(err, ErrRecordLockTimeout) {
		t.Fatalf("an append queued on the same Store was not refused by the bound: %v", err)
	}
	if got := recordOf(t, s); len(got) != len(before) {
		t.Fatalf("a refused same-Store operation wrote %d records", len(got)-len(before))
	}
	release()
	leaseA.Release()
	recordLockWait = wait
	mustAppend(t, s, status("A", "after release"))
}

// 70B2a1 cycle-3 review f2: THE GOVERNED KIND REGISTRY IS CLOSED. A task
// event of a kind the registry does not name is refused from the current
// session, whatever its source; and each authority-bearing kind is refused
// from a wrong source, with a payload missing a required member, or naming
// another task or session than its envelope.
func TestB2a1C3F2GovernedKindRegistryIsClosed(t *testing.T) {
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	for _, src := range []event.Source{event.SourceSystem, event.SourceUser} {
		unknown := event.New("A", lineageTask, src, event.Kind("plan.secretly_granted"), "x", map[string]string{"grant": "all"})
		if err := ValidGovernedEvent(unknown); !errors.Is(err, ErrMalformedEvent) {
			t.Fatalf("an unknown kind from %s was valid: %v", src, err)
		}
		if err := appendTip(t, s, t.Context(), false, unknown); !errors.Is(err, ErrSessionAppendRefused) {
			t.Fatalf("an unknown kind from %s was appended: %v", src, err)
		}
	}
	raw := func(e event.Event, payload string) event.Event {
		e.Payload = json.RawMessage(payload)
		return e
	}
	for name, bad := range map[string]event.Event{
		"plan by the reviewer":                 raw(event.New("A", lineageTask, event.SourceReviewer, event.PlanProposed, "p", nil), `{"summary":"p","decision":"proceed","plan":"p","plan_source":"architect"}`),
		"supplied plan by the architect":       raw(event.New("A", lineageTask, event.SourceArchitect, event.PlanProposed, "p", nil), `{"summary":"p","decision":"proceed","plan":"p","plan_source":"supplied"}`),
		"architect plan by the system":         raw(event.New("A", lineageTask, event.SourceSystem, event.PlanProposed, "p", nil), `{"summary":"p","decision":"proceed","plan":"p","plan_source":"architect"}`),
		"plan with an empty attempt":           raw(event.New("A", lineageTask, event.SourceArchitect, event.PlanProposed, "p", nil), `{"summary":"p","decision":"proceed","plan":"p","plan_attempt_id":""}`),
		"plan with no payload":                 event.New("A", lineageTask, event.SourceArchitect, event.PlanProposed, "p", nil),
		"architect plan with an empty payload": raw(event.New("A", lineageTask, event.SourceArchitect, event.PlanProposed, "p", nil), `{}`),
		"plan with no decision":                raw(event.New("A", lineageTask, event.SourceArchitect, event.PlanProposed, "p", nil), `{"summary":"p","plan":"p","plan_source":"architect"}`),
		"plan with no plan":                    raw(event.New("A", lineageTask, event.SourceArchitect, event.PlanProposed, "p", nil), `{"summary":"p","decision":"proceed","plan_source":"architect"}`),
		"plan from no plan source":             raw(event.New("A", lineageTask, event.SourceArchitect, event.PlanProposed, "p", nil), `{"summary":"p","decision":"proceed","plan":"p","plan_source":"oracle"}`),
		"answer choosing no outcome":           raw(event.New("A", lineageTask, event.SourceUser, event.AuthorityResolved, "a", nil), `{"task_id":"`+lineageTask+`","session_id":"A","decided_at":"2026-01-01T00:00:00Z","question":"q","condition":"c","option_id":"1","option_label":"yes","outcome":"anything nonempty","state":"failed"}`),
		"answer in no persistence state":       raw(event.New("A", lineageTask, event.SourceUser, event.AuthorityResolved, "a", nil), `{"task_id":"`+lineageTask+`","session_id":"A","decided_at":"2026-01-01T00:00:00Z","question":"q","condition":"c","option_id":"1","option_label":"yes","outcome":"authorize","state":"settled"}`),
		"answer to no question":                raw(event.New("A", lineageTask, event.SourceUser, event.AuthorityResolved, "a", nil), `{"task_id":"`+lineageTask+`","session_id":"A","decided_at":"2026-01-01T00:00:00Z","question":"","condition":"c","option_id":"1","option_label":"yes","outcome":"authorize","state":"failed"}`),
		"review by the system":                 raw(event.New("A", lineageTask, event.SourceSystem, event.ReviewCompleted, "r", nil), `{"decision":"accept","provenance":{"task_id":"`+lineageTask+`"}}`),
		"review deciding nothing known":        raw(event.New("A", lineageTask, event.SourceReviewer, event.ReviewCompleted, "r", nil), `{"decision":"admit","provenance":{"task_id":"`+lineageTask+`"}}`),
		"review of another task":               raw(event.New("A", lineageTask, event.SourceReviewer, event.ReviewCompleted, "r", nil), `{"decision":"accept","provenance":{"task_id":"other"}}`),
		"plan attempt naming no attempt":       raw(event.New("A", lineageTask, event.SourceSystem, event.PlanAttemptStarted, "a", nil), `{"task_id":"`+lineageTask+`","world":"w","plan_source":"architect","plan":{}}`),
		"plan attempt of another task":         raw(event.New("A", lineageTask, event.SourceSystem, event.PlanAttemptStarted, "a", nil), `{"plan_attempt_id":"x","task_id":"other","world":"w","plan_source":"architect","plan":{}}`),
		"plan attempt with no plan":            raw(event.New("A", lineageTask, event.SourceSystem, event.PlanAttemptStarted, "a", nil), `{"plan_attempt_id":"x","task_id":"`+lineageTask+`","world":"w","plan_source":"architect"}`),
		"grant with no grant list":             raw(event.New("A", lineageTask, event.SourceSystem, event.TestEditGranted, "g", nil), `{"world":"w","grants":"all"}`),
		"grant by the architect":               raw(event.New("A", lineageTask, event.SourceArchitect, event.ProspectiveGranted, "g", nil), `{"world":"w","grants":[]}`),
		"status naming another task":           raw(event.New("A", lineageTask, event.SourceSystem, event.Status, "s", nil), `{"task_id":"other"}`),
		"question with no payload":             event.New("A", lineageTask, event.SourceArchitect, event.AuthorityRequired, "q", nil),
		"answer decided in a foreign one":      raw(event.New("A", lineageTask, event.SourceUser, event.AuthorityResolved, "a", nil), `{"task_id":"`+lineageTask+`","session_id":"C","decided_at":"2026-01-01T00:00:00Z","question":"q","condition":"c","option_id":"1","option_label":"yes","outcome":"authorize","state":"failed"}`),
	} {
		if err := ValidGovernedEvent(bad); !errors.Is(err, ErrMalformedEvent) {
			t.Errorf("%s was valid: %v", name, err)
		}
		if err := appendTip(t, s, t.Context(), false, bad); !errors.Is(err, ErrSessionAppendRefused) {
			t.Errorf("%s was appended by the current session: %v", name, err)
		}
	}
	if got := recordOf(t, s); len(got) != 1 {
		t.Fatalf("refused records were written: %d records", len(got))
	}
	// Controls: the same kinds, lawful, are taken.
	answer := raw(event.New("A", lineageTask, event.SourceUser, event.AuthorityResolved, "a", nil), `{"task_id":"`+lineageTask+`","session_id":"A","decided_at":"2026-01-01T00:00:00Z","question":"q","condition":"c","option_id":"1","option_label":"yes","outcome":"authorize","state":"failed"}`)
	mustAppend(t, s, answer,
		raw(event.New("A", lineageTask, event.SourceSystem, event.PlanProposed, "p", nil), `{"decision":"proceed","summary":"p","plan":"p","plan_source":"supplied"}`),
		governed("A", event.PlanProposed, "p"), governed("A", event.ReviewCompleted, "r"),
		governed("A", event.PlanAttemptStarted, "a"), governed("A", event.TestEditGranted, "g"))
}

// 70B2a1 cycle-3 review f4: ONE PHYSICAL LEDGER AND ONE LEASE PER TASK. The
// invocation lease is keyed by the task at the repository's level, so Stores
// of two different records contend for it; and a task whose root two records
// hold is refused a lease and a binding through either -- no winner is chosen.
func TestB2a1C3F4OneLedgerAndOneLeasePerTaskAcrossRecords(t *testing.T) {
	savedWait := taskLeaseWait
	t.Cleanup(func() { taskLeaseWait = savedWait })
	taskLeaseWait = 30 * time.Millisecond
	repo := t.TempDir()
	holder, err := New(repo, "A")
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, holder, created("A"))
	elsewhere, err := New(repo, "X")
	if err != nil {
		t.Fatal(err)
	}
	// One lease across records: a task no record has rooted yet -- a Run
	// before its TaskCreated -- leased through one record's Store is refused
	// through another's by the lease alone.
	const unrooted = "task-unrooted"
	release, err := holder.AcquireTaskInvocation(t.Context(), unrooted)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := elsewhere.AcquireTaskInvocation(t.Context(), unrooted); !errors.Is(err, ErrTaskInvocationLeased) || errors.Is(err, ErrTaskLedgerAmbiguous) {
		t.Fatalf("an invocation through another record's Store was not refused as leased: %v", err)
	}
	release.Release()
	// Control: one ledger, one lineage, binds.
	bind(t, holder, "B")
	// A lease taken while the holder was the task's only ledger: the binding
	// operation proves the sole ledger again, under its own record lock.
	held, err := holder.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()
	// No second root is CREATED while the lease is held (70B2a1 cycle-3
	// review f1): through another record, neither the holder's lease nor a
	// lease of its own creates one, so root uniqueness is never left to a
	// race with the cross-record scan.
	if err := elsewhere.CreateTaskRoot(t.Context(), held, created("X")); !errors.Is(err, ErrTaskLeaseRequired) {
		t.Fatalf("another record's root was created under the holder's lease: %v", err)
	}
	if err := appendTip(t, elsewhere, t.Context(), false, created("X")); !errors.Is(err, ErrTaskInvocationLeased) {
		t.Fatalf("another record's root was created while the task's lease was held: %v", err)
	}
	// A second record claiming the task -- what a legacy or hostile writer
	// could leave -- makes it ambiguous everywhere.
	writeRaw(t, elsewhere, created("X"))
	before := recordOf(t, holder)
	l, err := holder.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	next, err := l.BindingFor("C")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := holder.BindSessionLineage(t.Context(), held, next); !errors.Is(err, ErrTaskLedgerAmbiguous) {
		t.Fatalf("a task two records claim was bound: %v", err)
	}
	held.Release()
	for name, s := range map[string]*Store{"holder": holder, "elsewhere": elsewhere} {
		if _, err := s.AcquireTaskInvocation(t.Context(), lineageTask); !errors.Is(err, ErrTaskLedgerAmbiguous) || !errors.Is(err, ErrSessionLineage) {
			t.Fatalf("the %s record leased a task two records claim: %v", name, err)
		}
	}
	if got := recordOf(t, holder); len(got) != len(before) {
		t.Fatalf("a refused binding wrote %d records", len(got)-len(before))
	}
	// The lease of the task is released by the refusal: another task leases.
	if r, err := elsewhere.AcquireTaskInvocation(t.Context(), "another-task"); err != nil {
		t.Fatalf("an unrelated task was refused: %v", err)
	} else {
		r.Release()
	}
}

// RULING-198 (70B2a1): THE GOVERNED KIND REGISTRY IS EXHAUSTIVE OVER THE
// DECLARED VOCABULARY. Every event kind the event package declares is named by
// the registry, with the sources that may record it and its payload rule, and
// the registry names no kind the vocabulary does not declare -- so a kind
// added to the vocabulary without a rule is refused by every Store until it
// has one, and no rule stands for a kind nothing can emit.
func TestB2a1R198TheRegistryNamesExactlyTheDeclaredKinds(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "event", "event.go"))
	if err != nil {
		t.Fatal(err)
	}
	declared := map[event.Kind]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		_, value, ok := strings.Cut(line, ` Kind = "`)
		if !ok {
			continue
		}
		kind, _, ok := strings.Cut(value, `"`)
		if !ok || kind == "" {
			t.Fatalf("an unreadable kind declaration: %q", line)
		}
		declared[event.Kind(kind)] = true
	}
	if len(declared) < 50 {
		t.Fatalf("premise: only %d declared kinds were read from event.go", len(declared))
	}
	for kind := range declared {
		rule, ok := governedKinds[kind]
		if !ok {
			t.Errorf("the declared kind %s has no registry rule", kind)
			continue
		}
		if len(rule.sources) == 0 || rule.payload == nil {
			t.Errorf("the registry rule of %s names no source or no payload rule", kind)
		}
	}
	for kind := range governedKinds {
		if !declared[kind] {
			t.Errorf("the registry names %s, which the vocabulary does not declare", kind)
		}
	}
}

// 70B2a1 cycle-2 review f3: THE LINEAGE BINDING REQUIRES THE TASK'S LIVE
// LEASE. While invocation B holds the task's invocation lease, another Store
// of the same record cannot move the lineage tip: a binder with no lease, a
// forged (zero) lease, a lease of another task, or a lease already released is
// refused with ErrTaskLeaseRequired -- even for the transition B -> C, which is
// otherwise exactly the one the lineage owes -- and the record stays byte for
// byte as it was; nor can it take the lease while B holds it. CONTROL: once B
// releases, a binder holding a fresh lease of the task binds C.
func TestB2a1RF3BindingRequiresTheTaskLease(t *testing.T) {
	savedWait := taskLeaseWait
	t.Cleanup(func() { taskLeaseWait = savedWait })
	taskLeaseWait = 30 * time.Millisecond
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	leaseB, err := s.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	toB, err := l.BindingFor("B")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.BindSessionLineage(t.Context(), leaseB, toB); err != nil {
		t.Fatalf("B, holding the task's lease, was not bound: %v", err)
	}
	afterB, err := l.BindingFor("C")
	if err != nil {
		t.Fatal(err)
	}
	afterB.ParentSessionID = "B" // the transition the lineage owes now: B -> C
	other := &Store{path: s.path}
	otherTask, err := other.AcquireTaskInvocation(t.Context(), "another-task")
	if err != nil {
		t.Fatal(err)
	}
	defer otherTask.Release()
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	released, err := (&Store{path: s.path}).AcquireTaskInvocation(t.Context(), "a-third-task")
	if err != nil {
		t.Fatal(err)
	}
	released.Release()
	released.taskID = lineageTask // a released lease, renamed: still not live
	for name, lease := range map[string]*TaskLease{
		"no lease":                 nil,
		"a forged lease":           {taskID: lineageTask, leasePath: other.taskLeasePath(lineageTask), recordPath: s.path},
		"another task's lease":     otherTask,
		"a released lease":         released,
		"a live lease elsewhere":   {taskID: lineageTask, leasePath: other.taskLeasePath(lineageTask), recordPath: s.path + ".other", live: true},
		"a live lease of no lease": {taskID: lineageTask, leasePath: s.path, recordPath: s.path, live: true},
	} {
		if _, err := other.BindSessionLineage(t.Context(), lease, afterB); !errors.Is(err, ErrTaskLeaseRequired) {
			t.Errorf("a binder with %s moved B's tip: %v", name, err)
		}
	}
	if _, err := other.AcquireTaskInvocation(t.Context(), lineageTask); !errors.Is(err, ErrTaskInvocationLeased) {
		t.Fatalf("another Store took the lease B holds: %v", err)
	}
	if after, err := os.ReadFile(s.path); err != nil || string(after) != string(before) {
		t.Fatalf("refused binders changed the record (%v)", err)
	}
	if l, err := s.TaskSessionLineage(lineageTask); err != nil || l.Tip() != "B" {
		t.Fatalf("B is not the tip after refused binders: %q, %v", l.Tip(), err)
	}
	// Control: B ends; a binder holding a fresh lease of the task binds C.
	leaseB.Release()
	if _, err := s.BindSessionLineage(t.Context(), leaseB, afterB); !errors.Is(err, ErrTaskLeaseRequired) {
		t.Fatalf("B's released lease still bound: %v", err)
	}
	leaseC, err := other.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	defer leaseC.Release()
	if bound, err := other.BindSessionLineage(t.Context(), leaseC, afterB); err != nil || bound.Tip() != "C" {
		t.Fatalf("a binder holding the task's live lease did not bind C: %q, %v", bound.Tip(), err)
	}
}

// 70B2a1 cycle-2 review f7: A UNIT IS APPENDED ALL OR NOTHING. A run's receipt
// and its terminal are appended as one unit (AppendUnit): when the Store
// refuses the terminal -- malformed, or written by a session that is not the
// tip -- or the unit's write fails, the record is byte for byte what it was
// and holds no receipt. CONTROL: a lawful unit is recorded whole, in order.
func TestB2a1RF7AppendUnitIsAllOrNothing(t *testing.T) {
	fault := appendCommitFault
	t.Cleanup(func() { appendCommitFault = fault })
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"))
	receipt := governed("A", event.RunReceipt, "receipt")
	before, err := os.ReadFile(s.path)
	if err != nil {
		t.Fatal(err)
	}
	malformed := governed("A", event.WorkflowFailed, "failed")
	malformed.Payload = json.RawMessage(`"failed"`)
	for name, terminal := range map[string]event.Event{
		"a malformed terminal":          malformed,
		"a terminal by another session": governed("C", event.WorkflowFailed, "failed"),
	} {
		if err := appendTipUnit(t, s, t.Context(), receipt, terminal); !errors.Is(err, ErrSessionAppendRefused) {
			t.Errorf("the unit with %s was not refused: %v", name, err)
		}
	}
	appendCommitFault = func(step string) error {
		if step == "sync" {
			return errors.New("injected sync failure")
		}
		return nil
	}
	if err := appendTipUnit(t, s, t.Context(), receipt, governed("A", event.WorkflowFailed, "failed")); err == nil {
		t.Error("a unit whose write failed was reported recorded")
	}
	appendCommitFault = fault
	if after, err := os.ReadFile(s.path); err != nil || string(after) != string(before) {
		t.Fatalf("a refused or failed unit changed the record (%v)", err)
	}
	if err := appendTipUnit(t, s, t.Context()); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("an empty unit was not refused: %v", err)
	}
	// CONTROL.
	if err := appendTipUnit(t, s, t.Context(), receipt, governed("A", event.WorkflowFailed, "failed")); err != nil {
		t.Fatal(err)
	}
	got := recordOf(t, s)
	if len(got) != 3 || got[1].Kind != event.RunReceipt || got[2].Kind != event.WorkflowFailed {
		t.Fatalf("a lawful unit was not recorded whole, in order: %d records", len(got))
	}
}

// 70B2a1 cycle-3 review f1: EVERY TASK-BOUND WRITE PRESENTS THE STORE-ISSUED
// CAPABILITY. A root, an ordinary append, a durable append and an append unit
// are each taken only under the task's live invocation lease, taken through a
// Store of this physical record and made operative, by the Store, for exactly
// the writing session. Being the lineage's tip is not enough: B's visible
// SessionID appends nothing without B's live lease -- none at all, a released
// one, one never made operative, one operative for another session, one of
// another task, one taken through another record -- and nothing refused is
// written. A record of no task has its own path, and no task record takes it.
func TestB2a1RF1EveryTaskAppendRequiresTheWriteCapability(t *testing.T) {
	s := lineageStore(t, "A")
	leaseA, err := s.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTaskRoot(t.Context(), nil, created("A")); !errors.Is(err, ErrTaskLeaseRequired) {
		t.Fatalf("a root was created with no lease: %v", err)
	}
	// Refused as a root, whatever the capability: generic append is never
	// the root's path.
	for _, lease := range []*TaskLease{nil, leaseA} {
		if err := s.Append(t.Context(), lease, created("A")); !errors.Is(err, ErrSessionAppendRefused) || errors.Is(err, ErrTaskLeaseRequired) {
			t.Fatalf("a root was not refused as a root through generic append: %v", err)
		}
	}
	if err := s.CreateTaskRoot(t.Context(), leaseA, created("A")); err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTaskRoot(t.Context(), leaseA, created("A")); !errors.Is(err, ErrTaskLeaseRequired) {
		t.Fatalf("a lease already operative created a second root: %v", err)
	}
	if err := s.Append(t.Context(), leaseA, status("A", "the holder, under its lease")); err != nil {
		t.Fatalf("the holder's lawful append under its operative lease was refused: %v", err)
	}
	leaseA.Release()
	if err := s.Append(t.Context(), leaseA, status("A", "the holder, its lease released")); !errors.Is(err, ErrTaskLeaseRequired) {
		t.Fatalf("the holder appended under a released lease: %v", err)
	}

	// B becomes the tip through the binding operation, under its own lease.
	leaseB, err := s.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	l, err := s.TaskSessionLineage(lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	b, err := l.BindingFor("B")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Append(t.Context(), leaseB, status("B", "before its binding")); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("B appended before it was bound: %v", err)
	}
	if _, err := s.BindSessionLineage(t.Context(), leaseB, b); err != nil {
		t.Fatal(err)
	}
	mustOK := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s under B's operative lease was refused: %v", what, err)
		}
	}
	mustOK("Append", s.Append(t.Context(), leaseB, status("B", "ordinary")))
	mustOK("AppendDurable", s.AppendDurable(t.Context(), leaseB, status("B", "durable")))
	mustOK("AppendUnit", s.AppendUnit(t.Context(), leaseB, governed("B", event.RunReceipt, "receipt"), governed("B", event.WorkflowCompleted, "done")))
	// A stale session presents B's capability: refused.
	if err := s.Append(t.Context(), leaseB, status("A", "stale A under B's lease")); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("a stale session appended under the tip's lease: %v", err)
	}

	// Another task's lease, operative for a session named B: verified
	// against the task, it authorizes nothing here.
	otherTask, err := s.AcquireTaskInvocation(t.Context(), "another-task")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.CreateTaskRoot(t.Context(), otherTask, event.New("B", "another-task", event.SourceSystem, event.TaskCreated, "o", nil)); err != nil {
		t.Fatal(err)
	}
	// Another record's lease of this task, operative for a session named B.
	foreign := lineageStore(t, "B")
	otherRecord, err := foreign.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	if err := foreign.CreateTaskRoot(t.Context(), otherRecord, created("B")); err != nil {
		t.Fatal(err)
	}
	leaseB.Release()
	unbound, err := s.AcquireTaskInvocation(t.Context(), lineageTask)
	if err != nil {
		t.Fatal(err)
	}
	before := recordOf(t, s)
	for name, lease := range map[string]*TaskLease{
		"no lease": nil, "B's released lease": leaseB, "a live lease never made operative": unbound,
		"another task's lease": otherTask, "another record's lease": otherRecord,
	} {
		for op, write := range map[string]func() error{
			"Append":        func() error { return s.Append(t.Context(), lease, status("B", name)) },
			"AppendDurable": func() error { return s.AppendDurable(t.Context(), lease, status("B", name)) },
			"AppendUnit": func() error {
				return s.AppendUnit(t.Context(), lease, governed("B", event.RunReceipt, "r"), governed("B", event.WorkflowFailed, name))
			},
		} {
			if err := write(); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrTaskLeaseRequired) {
				t.Fatalf("%s by the tip B under %s was not refused for its capability: %v", op, name, err)
			}
		}
	}
	if got := recordOf(t, s); len(got) != len(before) {
		t.Fatalf("refused writes changed the record: %d records became %d", len(before), len(got))
	}
	// Continuing names only the tip: a stale session is not made operative.
	if _, err := s.ContinueTaskSession(t.Context(), unbound, "A"); !errors.Is(err, ErrSessionLineage) {
		t.Fatalf("a stale session was continued: %v", err)
	}
	// Control: the tip's own fresh lease, made operative by the Store,
	// appends.
	if _, err := s.ContinueTaskSession(t.Context(), unbound, "B"); err != nil {
		t.Fatal(err)
	}
	mustOK("Append after continuing", s.Append(t.Context(), unbound, status("B", "continued")))
	unbound.Release()
	otherTask.Release()
	otherRecord.Release()

	// A record of no task has its own path; no task record takes it, and no
	// task path takes a record of no task.
	diag := event.New("B", "", event.SourceSystem, event.Status, "behavioral outcome not recorded", nil)
	if err := s.AppendDiagnostic(t.Context(), status("B", "a task record")); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("a task record was appended as a diagnostic: %v", err)
	}
	if err := s.Append(t.Context(), nil, diag); !errors.Is(err, ErrSessionAppendRefused) {
		t.Fatalf("a diagnostic was appended through the task path: %v", err)
	}
	if err := s.AppendDiagnostic(t.Context(), diag); err != nil {
		t.Fatalf("a valid diagnostic was refused: %v", err)
	}

	// Root creation proves the sole ledger under the lease, at the write: a
	// root another record gained after the lease was taken -- what only a
	// legacy writer can leave -- makes the task ambiguous, and no root is
	// created beside it.
	const late = "task-late-claim"
	leaseLate, err := s.AcquireTaskInvocation(t.Context(), late)
	if err != nil {
		t.Fatal(err)
	}
	defer leaseLate.Release()
	sibling, err := New(filepath.Dir(filepath.Dir(filepath.Dir(filepath.Dir(s.path)))), "Z")
	if err != nil {
		t.Fatal(err)
	}
	writeRaw(t, sibling, event.New("Z", late, event.SourceSystem, event.TaskCreated, "z", nil))
	lateBefore := recordOf(t, s)
	if err := s.CreateTaskRoot(t.Context(), leaseLate, event.New("B", late, event.SourceSystem, event.TaskCreated, "b", nil)); !errors.Is(err, ErrTaskLedgerAmbiguous) {
		t.Fatalf("a root was created beside another record's claim: %v", err)
	}
	if got := recordOf(t, s); len(got) != len(lateBefore) {
		t.Fatalf("a refused root was written")
	}
}

// 70B2a1 cycle-3 review f2: EVERY STANDING-STATE KIND IS VALIDATED AS ITS
// COMPLETE DURABLE SHAPE. A standing question, a provider block, an owed
// re-plan, an owed review, a refused restoration, a refused precondition and
// an owed plan-admission turn are each taken from the current session only
// whole: an absent payload, a missing or empty required member, a value
// outside its closed vocabulary, another task or session than the envelope's,
// a member the shape does not declare, or a source the kind's own source set
// does not name is refused at the Store, and nothing is written -- so no
// record can replace a standing obligation with nothing. The kinds that only
// report a role turn are attributed to the sources that run one.
func TestB2a1RF2StandingStateKindsAreExactAtTheStore(t *testing.T) {
	type mutant struct {
		name string
		edit func(e *event.Event, m map[string]any)
	}
	set := func(path string, v any) func(*event.Event, map[string]any) {
		return func(_ *event.Event, m map[string]any) {
			keys := strings.Split(path, ".")
			for _, k := range keys[:len(keys)-1] {
				m = m[k].(map[string]any)
			}
			m[keys[len(keys)-1]] = v
		}
	}
	drop := func(path string) func(*event.Event, map[string]any) {
		return func(_ *event.Event, m map[string]any) {
			keys := strings.Split(path, ".")
			for _, k := range keys[:len(keys)-1] {
				m = m[k].(map[string]any)
			}
			delete(m, keys[len(keys)-1])
		}
	}
	by := func(s event.Source) func(*event.Event, map[string]any) {
		return func(e *event.Event, _ map[string]any) { e.Source = s }
	}
	common := func(task string) []mutant {
		return []mutant{
			{"an undeclared member", set("smuggled", "x")},
			{"another task", set(task, "another-task")},
		}
	}
	cases := map[event.Kind][]mutant{
		event.WorkflowAwaitingAuthority: append(common("task_id"),
			mutant{"no task", drop("task_id")},
			mutant{"another session", set("session_id", "C")},
			mutant{"no session", drop("session_id")},
			mutant{"no decision", drop("decision")},
			mutant{"a decision of no level", set("decision.level", 0)},
			mutant{"a decision of an unknown level", set("decision.level", 259)},
			mutant{"a decision naming no subject", set("decision.subject", " ")},
			mutant{"an option naming no id", set("decision.options", []map[string]string{{"label": "yes"}})},
			mutant{"an option of no outcome", set("decision.options", []map[string]string{{"id": "1", "outcome": "maybe"}})},
			mutant{"an undeclared decision member", set("decision.smuggled", "x")},
			mutant{"scope recording unstated", drop("scope_recorded")},
			mutant{"an empty scope entry", set("scope", []string{""})},
			mutant{"no condition", drop("condition")},
			mutant{"recorded by the system", by(event.SourceSystem)},
		),
		event.WorkflowBlockedExternal: append(common("task_id"),
			mutant{"no role", drop("role")},
			mutant{"an unknown role", set("role", "janitor")},
			mutant{"no provider", set("provider", "")},
			mutant{"no reason", set("reason", "")},
			mutant{"no retry state", drop("retry_at_state")},
			mutant{"an unknown retry state", set("retry_at_state", "SOON")},
			mutant{"KNOWN with no time", drop("retry_at")},
			mutant{"KNOWN with no instant", set("retry_at", "tomorrow")},
			mutant{"UNKNOWN stating a time", set("retry_at_state", "UNKNOWN")},
			mutant{"recorded by an agent", by(event.SourceCodex)},
		),
		event.WorkflowNotConverged: append(common("task_id"),
			mutant{"no implementers", set("implementers", []string{})},
			mutant{"an unnamed implementer", set("implementers", []string{""})},
			mutant{"no review cycles", set("review_cycles", 0)},
			mutant{"owing something else", set("owed", "implementation")},
			mutant{"recorded by the architect", by(event.SourceArchitect)},
		),
		event.WorkflowAwaitingReview: {
			{"an undeclared member", set("smuggled", "x")},
			{"no review kind", drop("review_kind")},
			{"an unknown review kind", set("review_kind", "vibes")},
			{"an independent review claimed", set("independent_review", true)},
			{"independence unstated", drop("independent_review")},
			{"no obligations", set("obligations", []string{})},
			{"an empty obligation", set("obligations", []string{" "})},
			{"recorded by the system", by(event.SourceSystem)},
		},
		event.WorkflowRestorationRefused: append(common("task_id"),
			mutant{"no subject", set("subject", "")},
			mutant{"no instrument", drop("instrument")},
			mutant{"no binding", set("binding", "")},
			mutant{"no detail", drop("detail")},
			mutant{"a measurement that is not one", set("measured", "3")},
			mutant{"recorded by the architect", by(event.SourceArchitect)},
		),
		event.WorkflowBaseMovedRefused: append(common("TaskID"),
			mutant{"no recorded base", set("Recorded", "")},
			mutant{"one base as both", set("Current", "1e3f4a8")},
			mutant{"no reason", func(e *event.Event, _ map[string]any) { e.Summary = "" }},
		),
		event.WorkflowDirtyCanonicalRefused: {
			{"an undeclared member", set("smuggled", "x")},
			{"no repository", set("Repository", "")},
			{"no reason", func(e *event.Event, _ map[string]any) { e.Summary = "" }},
		},
		event.WorkflowPlanAdmissionRefused: append(common("task_id"),
			mutant{"no attempt", drop("plan_attempt_id")},
			mutant{"no reason", set("reason", "")},
			mutant{"no task", drop("task_id")},
			mutant{"an unknown continuation", set("continuation", "later")},
			mutant{"an empty refusal id", set("refusal_id", "")},
		),
		event.AuthorityRequired: {
			{"an undeclared member", set("smuggled", "x")},
			{"no level", drop("level")},
			{"no subject", set("subject", "")},
			{"recorded by the system", by(event.SourceSystem)},
			{"recorded by Sensei", by(event.SourceSensei)},
		},
	}
	for kind, mutants := range cases {
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"))
		valid := governed("A", kind, "valid")
		if err := ValidGovernedEvent(valid); err != nil {
			t.Fatalf("premise: the complete %s is not valid: %v", kind, err)
		}
		absent := valid
		absent.Payload = nil
		all := append([]mutant{{"an absent payload", nil}}, mutants...)
		for _, m := range all {
			bad := valid
			if m.edit == nil {
				bad = absent
			} else {
				var members map[string]any
				if err := json.Unmarshal(valid.Payload, &members); err != nil {
					t.Fatal(err)
				}
				m.edit(&bad, members)
				raw, err := json.Marshal(members)
				if err != nil {
					t.Fatal(err)
				}
				bad.Payload = raw
			}
			if err := ValidGovernedEvent(bad); !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("%s with %s: the canonical validator accepted it: %v", kind, m.name, err)
			}
			if err := appendTip(t, s, t.Context(), true, bad); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("%s with %s was taken from the current session: %v", kind, m.name, err)
			}
		}
		if got := recordOf(t, s); len(got) != 1 {
			t.Fatalf("a refused %s was written: %d records", kind, len(got))
		}
		// Control: the complete shape is taken.
		if err := appendTip(t, s, t.Context(), true, valid); err != nil {
			t.Fatalf("the complete %s was refused: %v", kind, err)
		}
	}
	// The one block that names no provider: an exhausted architect roster
	// whose last entry was left unnamed. Any other role, or reason, names one.
	unnamed := governed("A", event.WorkflowBlockedExternal, "roster exhausted")
	unnamed.Payload = json.RawMessage(`{"task_id":"` + lineageTask + `","role":"architect","provider":"","reason":"ENGINE_NO_ARCHITECT_OBTAINED","retry_at_state":"UNKNOWN"}`)
	if err := ValidGovernedEvent(unnamed); err != nil {
		t.Fatalf("an exhausted architect roster with no named entry was refused: %v", err)
	}
	unnamed.Payload = json.RawMessage(`{"task_id":"` + lineageTask + `","role":"implementer","provider":"","reason":"ENGINE_NO_ARCHITECT_OBTAINED","retry_at_state":"UNKNOWN"}`)
	if err := ValidGovernedEvent(unnamed); !errors.Is(err, ErrMalformedEvent) {
		t.Fatalf("an implementer block naming no provider was accepted: %v", err)
	}
	// Per-kind sources: a role turn is reported by the sources that run one.
	for _, c := range []struct {
		kind   event.Kind
		source event.Source
	}{
		{event.Output, event.SourceGit}, {event.Output, event.SourceUser}, {event.AgentStarted, event.SourceSensei},
		{event.AgentFinished, event.SourceTests}, {event.InspectionReported, event.SourceArchitect},
		{event.InspectionReported, event.SourceReviewer}, {event.Status, event.SourceTests},
	} {
		if err := ValidGovernedEvent(event.New("A", lineageTask, c.source, c.kind, "x", nil)); !errors.Is(err, ErrMalformedEvent) {
			t.Fatalf("a %s attributed to %s was accepted: %v", c.kind, c.source, err)
		}
	}
	for _, c := range []struct {
		kind   event.Kind
		source event.Source
	}{
		{event.Output, event.SourceClaude}, {event.Output, event.SourceArchitect}, {event.AgentStarted, event.SourceReviewer},
		{event.AgentFinished, event.SourceSystem}, {event.InspectionReported, event.SourceCodex}, {event.Status, event.SourceGit},
	} {
		var payload any
		if c.kind == event.Output {
			payload = map[string]string{"stream": "assistant"}
		}
		if err := ValidGovernedEvent(event.New("A", lineageTask, c.source, c.kind, "x", payload)); err != nil {
			t.Fatalf("a %s attributed to %s was refused: %v", c.kind, c.source, err)
		}
	}
}

// RULING-200 B, LINEARIZABLE INVOCATION CAPABILITY (r9 c3 f2): a Store write
// the lease admitted holds the lease from its verification through its commit,
// and Release cannot overtake it. The operation is paused deterministically
// after its capability was verified and inside its commit; Release is then
// started, and while the operation is paused Release has not returned, a
// competing invocation of the task is refused as leased, and the closing lease
// admits no new write. Released, the operation commits, Release returns, the
// old lease writes nothing, and the competing invocation is admitted.
func TestB2a1W9LeaseReleaseCannotOvertakeAGuardedStoreOperation(t *testing.T) {
	savedWait := taskLeaseWait
	t.Cleanup(func() { taskLeaseWait = savedWait })
	taskLeaseWait = 20 * time.Millisecond
	savedAppend, savedLineage := appendCommitFault, lineageCommitFault
	t.Cleanup(func() { appendCommitFault, lineageCommitFault = savedAppend, savedLineage })

	// pauseAt returns a commit fault that parks the first commit at the write
	// step until resume is closed, and closes paused when it parks there.
	pauseAt := func(paused, resume chan struct{}) func(string) error {
		var once sync.Once
		return func(step string) error {
			if step == "write" {
				once.Do(func() {
					close(paused)
					<-resume
				})
			}
			return nil
		}
	}
	// closing waits until Release has closed lease to new operations,
	// observed on the lease itself, and fails the test if it never does.
	closing := func(t *testing.T, lease *TaskLease) {
		deadline := time.Now().Add(10 * time.Second)
		for {
			lease.mu.Lock()
			c := lease.closing
			lease.mu.Unlock()
			if c {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("Release never closed the lease to new operations")
			}
			time.Sleep(time.Millisecond)
		}
	}
	// overtaken runs the race against an operation already paused inside its
	// commit under lease, and returns once Release has returned.
	overtaken := func(t *testing.T, s *Store, lease *TaskLease, op <-chan error, resume func()) {
		released := make(chan struct{})
		go func() {
			lease.Release()
			close(released)
		}()
		closing(t, lease)
		select {
		case <-released:
			t.Fatal("Release returned while an operation the lease admitted was still committing")
		default:
		}
		other := &Store{path: s.path}
		if competing, err := other.AcquireTaskInvocation(t.Context(), lineageTask); !errors.Is(err, ErrTaskInvocationLeased) {
			if competing != nil {
				competing.Release()
			}
			t.Fatalf("a competing invocation acquired the task while an admitted operation was committing: %v", err)
		}
		if err := s.Append(t.Context(), lease, status("A", "after Release began")); !errors.Is(err, ErrTaskLeaseRequired) {
			t.Fatalf("a closing lease admitted a new write: %v", err)
		}
		resume()
		if err := <-op; err != nil {
			t.Fatalf("the operation the lease admitted before Release did not commit: %v", err)
		}
		<-released
		if err := s.Append(t.Context(), lease, status("A", "after Release")); !errors.Is(err, ErrTaskLeaseRequired) {
			t.Fatalf("a released lease wrote: %v", err)
		}
		competing, err := other.AcquireTaskInvocation(t.Context(), lineageTask)
		if err != nil {
			t.Fatalf("the released task was not available to the competing invocation: %v", err)
		}
		competing.Release()
	}

	t.Run("append", func(t *testing.T) {
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"))
		lease, err := tipLease(t, s, lineageTask)
		if err != nil {
			t.Fatal(err)
		}
		paused, resume := make(chan struct{}), make(chan struct{})
		appendCommitFault = pauseAt(paused, resume)
		op := make(chan error, 1)
		go func() { op <- s.Append(t.Context(), lease, status("A", "admitted before Release")) }()
		select {
		case <-paused:
		case err := <-op:
			t.Fatalf("the operation ended before it reached its commit: %v", err)
		}
		var unpause sync.Once
		resumeOnce := func() { unpause.Do(func() { close(resume) }) }
		t.Cleanup(resumeOnce)
		overtaken(t, s, lease, op, resumeOnce)
		appendCommitFault = savedAppend
		var summaries []string
		for _, e := range recordOf(t, s) {
			summaries = append(summaries, e.Summary)
		}
		if got := strings.Join(summaries, "|"); got != "the objective|admitted before Release" {
			t.Fatalf("the record is %q", got)
		}
	})

	t.Run("bind", func(t *testing.T) {
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"))
		l, err := s.TaskSessionLineage(lineageTask)
		if err != nil {
			t.Fatal(err)
		}
		b, err := l.BindingFor("B")
		if err != nil {
			t.Fatal(err)
		}
		lease, err := s.AcquireTaskInvocation(t.Context(), lineageTask)
		if err != nil {
			t.Fatal(err)
		}
		paused, resume := make(chan struct{}), make(chan struct{})
		lineageCommitFault = pauseAt(paused, resume)
		op := make(chan error, 1)
		go func() {
			_, err := s.BindSessionLineage(t.Context(), lease, b)
			op <- err
		}()
		select {
		case <-paused:
		case err := <-op:
			t.Fatalf("the operation ended before it reached its commit: %v", err)
		}
		var unpause sync.Once
		resumeOnce := func() { unpause.Do(func() { close(resume) }) }
		t.Cleanup(resumeOnce)
		overtaken(t, s, lease, op, resumeOnce)
		lineageCommitFault = savedLineage
		if bound, err := s.TaskSessionLineage(lineageTask); err != nil || bound.Tip() != "B" {
			t.Fatalf("the binding the lease admitted before Release is not the tip: %v %+v", err, bound)
		}
	})
}

// RULING-200 A, CLOSED EVENT VALIDATION (r9 c3 f1): EVERY GOVERNED KIND HAS
// EXACT STORE SEMANTICS. For every kind the registry names, a valid record
// (governed) is taken by the Store, and the Store refuses -- writing nothing --
// the same record attributed to a source the kind does not admit; carrying a
// member no shape of the kind declares (for a payloadless kind, any payload);
// missing each member its shape requires; carrying each member as a value of
// another JSON type; and, for a kind whose payload is required, carrying none.
// No kind accepts an arbitrary object: the witness fails if any rule is
// neither payloadless nor closed.
func TestB2a1W5EveryGovernedKindHasExactStoreSemantics(t *testing.T) {
	kinds := make([]string, 0, len(governedKinds))
	for kind := range governedKinds {
		kinds = append(kinds, string(kind))
	}
	sources := make([]string, 0, len(eventSources))
	for src := range eventSources {
		sources = append(sources, string(src))
	}
	// otherType is a value of a JSON type other than raw's.
	otherType := func(raw json.RawMessage) json.RawMessage {
		if t := strings.TrimSpace(string(raw)); strings.HasPrefix(t, `"`) {
			return json.RawMessage(`7`)
		}
		return json.RawMessage(`"not its type"`)
	}
	// admits is this witness's own reading of whether a shape of rule admits
	// m -- every member declared, every required member present, and, for
	// member typed, a declared type its value (a number, or a string) can
	// be -- independent of the production matcher it witnesses, so removing
	// an enforcement there cannot silence an assertion here.
	admits := func(rule kindRule, m payloadMembers, typed string) bool {
		for _, s := range rule.shapes {
			ok := true
			for k := range m {
				if _, declared := s.members[k]; !declared {
					ok = false
				}
			}
			for k, d := range s.members {
				if d.required && !m.has(k) {
					ok = false
				}
			}
			if d, declared := s.members[typed]; ok && declared {
				number := strings.TrimSpace(string(m[typed])) == "7"
				ok = (number && d.t == tNumber) || (!number && (d.t == tString || d.t == tText))
			}
			if ok {
				return true
			}
		}
		return false
	}
	for _, name := range kinds {
		kind := event.Kind(name)
		rule := governedKinds[kind]
		t.Run(name, func(t *testing.T) {
			if kind == event.TaskCreated || kind == SessionLineageBound {
				// Written only through CreateTaskRoot and BindSessionLineage,
				// whose own witnesses (W2, R193W1, R193W2) refuse malformed ones.
				if err := ValidGovernedEvent(governed("A", kind, "valid")); err != nil && kind == event.TaskCreated {
					t.Fatalf("premise: the valid root is refused: %v", err)
				}
				return
			}
			s := lineageStore(t, "A")
			mustAppend(t, s, created("A"))
			refused := func(what string, bad event.Event) {
				t.Helper()
				if err := ValidGovernedEvent(bad); !errors.Is(err, ErrMalformedEvent) {
					t.Fatalf("%s: the canonical validator accepted it: %v", what, err)
				}
				if err := appendTip(t, s, t.Context(), true, bad); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedEvent) {
					t.Fatalf("%s: the Store took it: %v", what, err)
				}
			}
			valid := governed("A", kind, "valid")
			if err := ValidGovernedEvent(valid); err != nil {
				t.Fatalf("premise: the valid %s is refused: %v", kind, err)
			}
			for _, src := range sources {
				if !rule.sources[event.Source(src)] {
					bad := valid
					bad.Source = event.Source(src)
					refused("attributed to "+src, bad)
				}
			}
			m, present, err := decodePayload(valid.Payload)
			if err != nil {
				t.Fatal(err)
			}
			switch {
			case rule.shapes == nil && rule.observation != nil:
				// A Sensei observation: its evidence envelope is closed and its
				// operation one of the closed vocabulary, and it is Sensei's alone.
				for name, raw := range map[string]string{
					"an envelope member it does not declare": `{"evidence":{"operation":"awareness_audit_diff","request":{},"smuggled":"x"}}`,
					"an operation it does not record":        `{"evidence":{"operation":"awareness_rewrite","request":{}}}`,
					"an envelope that is not one":            `{"evidence":"awareness_audit_diff"}`,
					"no object at all":                       `["awareness_audit_diff"]`,
				} {
					bad := valid
					bad.Payload = json.RawMessage(raw)
					refused(name, bad)
				}
			case rule.shapes == nil:
				if present {
					t.Fatalf("the payloadless %s's valid record carries a payload", kind)
				}
				// A payloadless kind: any payload is refused, and the rule is
				// exactly noPayload's.
				bad := valid
				bad.Payload = json.RawMessage(`{"smuggled":"x"}`)
				refused("a payloadless kind carrying a payload", bad)
				if err := rule.payload(valid, nil, true); err == nil {
					t.Fatalf("the payloadless %s accepts a payload", kind)
				}
			default:
				if !present {
					// The valid record of a kind that may record nothing; the
					// closed shapes are proven on a fixture that carries one.
					if !rule.absent {
						t.Fatalf("premise: the valid %s carries no payload, which its kind requires", kind)
					}
					return
				}
				if !admits(rule, m, "") {
					t.Fatalf("premise: the valid %s matches no shape of its kind", kind)
				}
				if !rule.absent {
					bad := valid
					bad.Payload = nil
					refused("no payload", bad)
				}
				with := func(edit func(payloadMembers)) event.Event {
					c := payloadMembers{}
					for k, v := range m {
						c[k] = v
					}
					edit(c)
					raw, err := json.Marshal(c)
					if err != nil {
						t.Fatal(err)
					}
					bad := valid
					bad.Payload = raw
					return bad
				}
				refused("an undeclared member", with(func(c payloadMembers) { c["smuggled"] = json.RawMessage(`"x"`) }))
				for k := range m {
					dropped := payloadMembers{}
					for kk, v := range m {
						if kk != k {
							dropped[kk] = v
						}
					}
					if !admits(rule, dropped, "") {
						refused("missing "+k, with(func(c payloadMembers) { delete(c, k) }))
					}
					retyped := with(func(c payloadMembers) { c[k] = otherType(c[k]) })
					rm, _, _ := decodePayload(retyped.Payload)
					if !admits(rule, rm, k) {
						refused(k+" of another type", retyped)
					}
				}
			}
			if got := recordOf(t, s); len(got) != 1 {
				t.Fatalf("a refused record was written: %d records", len(got))
			}
			mustAppend(t, s, valid)
		})
	}
}

// RULING-200 A: CandidateResolved IS THE DISPOSITION OWNER'S EXACT
// RESOLUTION. Beyond the closed shape every kind has (W5 above), the Store
// refuses a resolution its owner could not record: a disposition outside the
// closed vocabulary, no reason, no or an unreadable decision time, a removal
// whose evidence would not survive it, a retained candidate reported removed,
// a branch removed without its worktree, a summary that is not the
// resolution's own, and one recorded by the system rather than Git. CONTROL:
// a removal decided before cleanup, truthfully reporting nothing removed yet,
// and a completed removal are both taken.
func TestB2a1W5CandidateResolvedIsTheDispositionOwnersExactResolution(t *testing.T) {
	resolution := func(edit func(map[string]any)) map[string]any {
		r := map[string]any{"disposition": "disposed", "reason": "produced nothing", "decided_at": "2026-10-08T00:00:00Z",
			"evidence":         map[string]any{"base_sha": "1e3f4a8", "diff_bytes": 0, "produced_no_work": true},
			"worktree_removed": false, "branch_removed": false}
		if edit != nil {
			edit(r)
		}
		return r
	}
	record := func(r map[string]any, summary string) event.Event {
		return event.New("A", lineageTask, event.SourceGit, event.CandidateResolved, summary, r)
	}
	const disposedSummary = "disposed: produced nothing (worktree still present)"
	for name, bad := range map[string]event.Event{
		"no disposition vocabulary": record(resolution(func(r map[string]any) { r["disposition"] = "forgotten" }), "forgotten: produced nothing"),
		"no reason":                 record(resolution(func(r map[string]any) { r["reason"] = " " }), "disposed: "),
		"no decision time":          record(resolution(func(r map[string]any) { r["decided_at"] = "0001-01-01T00:00:00Z" }), disposedSummary),
		"an unreadable time":        record(resolution(func(r map[string]any) { r["decided_at"] = "yesterday" }), disposedSummary),
		"a removal with no evidence": record(resolution(func(r map[string]any) {
			r["evidence"] = map[string]any{"base_sha": "1e3f4a8", "diff_bytes": 0}
		}), disposedSummary),
		"a removal with no base": record(resolution(func(r map[string]any) {
			r["evidence"] = map[string]any{"base_sha": "", "diff_bytes": 0, "produced_no_work": true}
		}), disposedSummary),
		"a retained candidate reported removed": record(resolution(func(r map[string]any) {
			r["disposition"], r["worktree_removed"] = "retained", true
		}), "retained: produced nothing"),
		"a branch removed without its worktree":  record(resolution(func(r map[string]any) { r["branch_removed"] = true }), disposedSummary),
		"a summary that is not the resolution's": record(resolution(nil), "disposed: something else"),
		"recorded by the system": func() event.Event {
			e := record(resolution(nil), disposedSummary)
			e.Source = event.SourceSystem
			return e
		}(),
		"an evidence member it does not declare": record(resolution(func(r map[string]any) {
			r["evidence"] = map[string]any{"base_sha": "1e3f4a8", "diff_bytes": 0, "produced_no_work": true, "smuggled": "x"}
		}), disposedSummary),
	} {
		t.Run(name, func(t *testing.T) {
			s := lineageStore(t, "A")
			mustAppend(t, s, created("A"))
			if err := appendTip(t, s, t.Context(), true, bad); !errors.Is(err, ErrSessionAppendRefused) || !errors.Is(err, ErrMalformedEvent) {
				t.Fatalf("the Store took it: %v", err)
			}
			if got := recordOf(t, s); len(got) != 1 {
				t.Fatalf("a refused resolution was written: %d records", len(got))
			}
		})
	}
	s := lineageStore(t, "A")
	mustAppend(t, s, created("A"),
		record(resolution(nil), disposedSummary),
		record(resolution(func(r map[string]any) { r["worktree_removed"], r["branch_removed"] = true, true }), "disposed: produced nothing"))
}

// f1 (70B2a1 r10 review): A MALFORMED RECORD AFTER THE ROOT LEAVES NO LINEAGE.
// The canonical reconstruction validates every record of the task at or after
// its root (ValidGovernedEvent), not only its root and transitions. A raw
// record of the task with an unknown kind, a source that may not record its
// kind, a payload that is not its kind's, or missing framing leaves the task
// with no establishable lineage: it cannot be continued, bound or appended
// to, the records before it keep their class, and every refusal leaves the
// record byte for byte as it was. CONTROLS: the same malformed record before
// the root is pre-root and blocks nothing, and a well-formed record of an
// unrelated session after the root is foreign and blocks nothing.
func TestB2a1F1MalformedPostRootHistoryLeavesNoLineage(t *testing.T) {
	unknownKind := status("A", "unknown kind")
	unknownKind.Kind = "workflow.invented"
	wrongSource := governed("A", event.RunReceipt, "wrong source")
	wrongSource.Source = event.SourceClaude
	badPayload := governed("A", event.RunReceipt, "bad payload")
	badPayload.Payload = json.RawMessage(`{"completeness":"complete","missing":[]}`)
	noID := status("A", "no ID")
	noID.ID = ""
	noSession := status("A", "no session")
	noSession.SessionID = ""
	malformed := map[string]event.Event{"an unknown kind": unknownKind, "a wrong source": wrongSource,
		"an invalid payload": badPayload, "no event ID": noID, "no session": noSession}
	for name, bad := range malformed {
		t.Run(name, func(t *testing.T) {
			if err := ValidGovernedEvent(bad); err == nil {
				t.Fatalf("premise: the record with %s is valid", name)
			}
			s := lineageStore(t, "A")
			mustAppend(t, s, created("A"), status("A", "valid"))
			writeRaw(t, s, bad)
			before, err := os.ReadFile(s.path)
			if err != nil {
				t.Fatal(err)
			}
			if l, err := s.TaskSessionLineage(lineageTask); !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a record with %s left lineage %+v: %v", name, l, err)
			}
			lease, err := s.AcquireTaskInvocation(t.Context(), lineageTask)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ContinueTaskSession(t.Context(), lease, "A"); !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a record with %s let A continue: %v", name, err)
			}
			lease.Release()
			b := SessionLineageBinding{TaskID: lineageTask, HolderSessionID: "A", ParentSessionID: "A",
				CurrentSessionID: "B", Relation: SessionLineageRelationResume}
			if _, err := bindLeased(t, s, t.Context(), b); !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a record with %s let B bind: %v", name, err)
			}
			for _, durable := range []bool{false, true} {
				if err := appendTip(t, s, t.Context(), durable, status("A", "more")); err == nil {
					t.Fatalf("a record with %s let the holder append (durable %v)", name, durable)
				}
			}
			classes, _, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
			if !errors.Is(err, ErrSessionLineage) {
				t.Fatalf("a record with %s was classified without error: %v", name, err)
			}
			want := []HistoryClass{HistoryLineageMember, HistoryLineageMember, HistoryMalformed}
			if len(classes) != len(want) {
				t.Fatalf("classified %d records, want %d", len(classes), len(want))
			}
			for i, c := range classes {
				if c.Class != want[i] {
					t.Fatalf("record %d classified %s, want %s", c.Index, c.Class, want[i])
				}
			}
			if after, err := os.ReadFile(s.path); err != nil || string(after) != string(before) {
				t.Fatalf("refusals under a record with %s changed the record (%v)", name, err)
			}
		})
	}
	t.Run("control: before the root", func(t *testing.T) {
		s := lineageStore(t, "A")
		writeRaw(t, s, unknownKind)
		mustAppend(t, s, created("A"))
		bind(t, s, "B")
		mustAppend(t, s, status("B", "lawful"))
	})
	t.Run("control: a well-formed foreign record", func(t *testing.T) {
		s := lineageStore(t, "A")
		mustAppend(t, s, created("A"))
		writeRaw(t, s, status("C", "foreign"))
		bind(t, s, "B")
		mustAppend(t, s, status("B", "lawful"))
		classes, _, err := ClassifyTaskHistory(recordOf(t, s), lineageTask)
		if err != nil {
			t.Fatal(err)
		}
		if classes[1].Class != HistoryForeign {
			t.Fatalf("the unrelated record was classified %s", classes[1].Class)
		}
	})
}

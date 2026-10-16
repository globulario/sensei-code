//go:build linux

package workflow

// RULING-373 §16 pre-fix controls (TUI-A, TUI-C, TUI-D, TUI-E; TUI-B is the
// CLI half in cmd/sensei-code/workspace_quarantine_r373_test.go). The TUI's
// /resume calls Engine.ResumeTask directly (internal/tui/model.go), so these
// controls drive Engine.ResumeTask exactly as the TUI does. Each asserts the
// SAFE property, so each fails on the engine before the RULING-373 gate:
//
//	TUI-A  settled record + invalid lock object + planned task: ResumeTask
//	       must refuse before binding the lineage, and no implementer turn
//	       may start.
//	TUI-C  deferred question + absent record + invalid lock + a recorded
//	       UNVERIFIED quarantine terminal: ResumeTask must refuse before
//	       binding the lineage.
//	TUI-D  the same specimen: the standing question must not be answerable
//	       (no answer consumed, no AuthorityResolved) before containment is
//	       established.
//	TUI-E  settled record whose lock is held past its bound: no implementer
//	       turn may start, and the lineage is not bound.
//
// This file uses only API that exists at a7b41bd, so it is the same file
// before and after the fix.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

const r373Task = "task-w-tui"

func r373LockPath(repoRoot, taskID string) string {
	return filepath.Join(repoRoot, ".sensei-code", "candidates", taskID+".json.lock")
}

// r373InvalidLock replaces taskID's record lock object with a symlink, so the
// locked read fails ("the candidate record lock is not a regular file" or an
// O_NOFOLLOW refusal) while an unlocked read still sees the record.
func r373InvalidLock(t *testing.T, repoRoot, taskID string) {
	t.Helper()
	lock := r373LockPath(repoRoot, taskID)
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(lock)
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
		t.Fatal(err)
	}
}

// r373HoldLock holds taskID's record lock on a separate open file description
// (as another process would) until the test ends, so every locked read and
// write waits out its bound and fails.
func r373HoldLock(t *testing.T, repoRoot, taskID string) {
	t.Helper()
	lock := r373LockPath(repoRoot, taskID)
	if err := os.MkdirAll(filepath.Dir(lock), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(lock, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	})
}

// r373Tree is a digest of every name, mode and byte under dir, so a worktree
// that was written, created or reused in place reads differently.
func r373Tree(t *testing.T, dir string) string {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		h.Write([]byte(rel + "\x00" + info.Mode().String() + "\x00" + info.ModTime().UTC().Format(time.RFC3339Nano) + "\x00"))
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read the worktree %s: %v", dir, err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// r373Ledger is every event the store holds, as kinds.
func r373Ledger(t *testing.T, store *session.Store) []event.Kind {
	t.Helper()
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	return kinds(history)
}

// r373Planned is the W-TUI-1 specimen: a planned task whose first process
// stopped with its implementer blocked, leaving a readable SETTLED candidate
// record and its worktree, and a restarted engine over the same record whose
// implementer counts its turns.
type r373Planned struct {
	e        *Engine
	root     string
	repoRoot string
	impl     *unavailableRunner
	task     session.Interrupted
	worktree string
}

func r373PlannedSpecimen(t *testing.T) r373Planned {
	t.Helper()
	root := t.TempDir()
	first, _ := objectiveRun(t, root, r373Task, "make main print a number\n", RequestedByHuman)
	impl := &unavailableRunner{cause: quota()}
	restarted, _ := objectiveEngine(t, root, objectiveRoles{implementer: impl,
		reviewer: `{"decision":"accept","summary":"ok"}`, bindings: make(chan RunnerSpec, 8)})
	restarted.Repo = first.Repo
	found := reopen(t, root, "s1")
	if len(found) != 1 || found[0].TaskID != r373Task || !found[0].Planned || len(found[0].AwaitingAuthority) != 0 {
		t.Fatalf("premise: one planned task with no standing question: %+v", found)
	}
	id, ok, err := candidate.Load(first.Repo.Root, r373Task)
	if err != nil || !ok || id.Unsettled() {
		t.Fatalf("premise: a readable settled candidate record: ok=%v err=%v %+v", ok, err, id)
	}
	if st, err := os.Stat(id.Worktree); err != nil || !st.IsDir() {
		t.Fatalf("premise: the candidate worktree exists: %v", err)
	}
	return r373Planned{e: restarted, root: root, repoRoot: first.Repo.Root, impl: impl, task: found[0], worktree: id.Worktree}
}

// r373Resume resumes task through Engine.ResumeTask, as the TUI's /resume
// does, and waits for the invocation to end.
func r373Resume(t *testing.T, e *Engine, task session.Interrupted, wait time.Duration) (ResumeBinding, *ResumeAttempt, []event.Event) {
	t.Helper()
	events, cancel := e.Bus.Subscribe(8192)
	defer cancel()
	attempt := e.ResumeTask(context.Background(), task)
	b := attempt.Binding()
	select {
	case <-attempt.Ended():
	case <-time.After(wait):
		t.Fatalf("the resumed invocation did not end within %s: %v", wait, summaries(drainEvents(events)))
	}
	return b, attempt, drainEvents(events)
}

// TUI-A. A planned task, readable settled record, invalid lock object.
func TestR373TUIAPlannedSettledInvalidLockDoesNotReachTheImplementer(t *testing.T) {
	s := r373PlannedSpecimen(t)
	r373InvalidLock(t, s.repoRoot, r373Task)
	b, _, seen := r373Resume(t, s.e, s.task, 90*time.Second)
	if b.Refusal == nil {
		t.Errorf("TUI-A: ResumeTask bound the lineage (session %s) over a containment that cannot be established", b.CurrentSessionID)
	}
	if n := s.impl.calls.Load(); n != 0 {
		t.Errorf("TUI-A: the implementer was reached %d time(s) over a containment that cannot be established:\n%s",
			n, strings.Join(summaries(seen), "\n"))
	}
}

// TUI-E. A planned task, readable settled record, its lock held past its
// bound by another holder.
func TestR373TUIELockHeldPastItsBoundDoesNotReachTheImplementer(t *testing.T) {
	s := r373PlannedSpecimen(t)
	r373HoldLock(t, s.repoRoot, r373Task)
	b, _, seen := r373Resume(t, s.e, s.task, 150*time.Second)
	if b.Refusal == nil {
		t.Errorf("TUI-E: ResumeTask bound the lineage (session %s) while the record lock was held", b.CurrentSessionID)
	}
	if n := s.impl.calls.Load(); n != 0 {
		t.Errorf("TUI-E: the implementer was reached %d time(s) while the record lock was held:\n%s",
			n, strings.Join(summaries(seen), "\n"))
	}
}

// r373Question is the question-lane specimen (RULING-373 §4, the §14-C
// specimen): a session task with a standing deferred question, no candidate
// JSON, and an invalid lock object; the engine is a fresh process over the
// record, so a successful binding writes a durable lineage transition.
type r373Question struct {
	e        *Engine
	task     session.Interrupted
	holder   string
	repoRoot string
}

func r373QuestionSpecimen(t *testing.T, unverifiedTerminal bool) r373Question {
	t.Helper()
	e, _, _ := resumeHarness(t, false)
	e.Config.Sensei.Command = "sh"
	e.Config.Sensei.Args = []string{"-c", proposalSenseiScript(t)}
	const taskID = "task-r"
	holder := e.SessionID
	q := deferScoped(t, taskID, planScope())
	q.SessionID = holder
	history := []event.Event{
		event.New(holder, taskID, event.SourceUser, event.TaskCreated, "the objective", nil),
		event.New(holder, taskID, event.SourceUser, event.WorkflowAwaitingAuthority, q.Condition, q),
	}
	if unverifiedTerminal {
		history = append(history, event.New(holder, taskID, event.SourceSystem, event.WorkflowWorkspaceQuarantined,
			"the invocation ended and the candidate record could not be read under its lock",
			map[string]any{"task_id": taskID, "recorded": false,
				"reason": "the candidate record of " + taskID + " could not be read under its lock, so its containment " +
					"state is not established: the candidate record lock is not a regular file"}))
	}
	for _, ev := range history {
		if err := seedAppend(t, e.Store, ev); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Remove(filepath.Join(e.Repo.Root, ".sensei-code", "candidates", taskID+".json")); err != nil {
		t.Fatal(err)
	}
	r373InvalidLock(t, e.Repo.Root, taskID)
	before, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	standing := session.FindInterrupted(before)
	if len(standing) != 1 || standing[0].TaskID != taskID || len(standing[0].AwaitingAuthority) == 0 {
		t.Fatalf("premise: one task with a standing question: %+v", standing)
	}
	fresh, err := session.FreshID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	e.SessionID = fresh
	return r373Question{e: e, task: standing[0], holder: holder, repoRoot: e.Repo.Root}
}

// r373AnswerWhileRunning resumes the question specimen and offers the answer
// a person would give for as long as the invocation runs, reporting whether
// the engine took it.
func r373AnswerWhileRunning(t *testing.T, s r373Question) (ResumeBinding, bool, []event.Event) {
	t.Helper()
	events, cancel := s.e.Bus.Subscribe(8192)
	defer cancel()
	attempt := s.e.ResumeTask(context.Background(), s.task)
	consumed := false
	deadline := time.After(90 * time.Second)
	for done := false; !done; {
		if s.e.ResolveHuman(s.task.TaskID, "1") {
			consumed = true
		}
		select {
		case <-attempt.Ended():
			done = true
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the resumed invocation did not end: %v", summaries(drainEvents(events)))
		}
	}
	return attempt.Binding(), consumed, drainEvents(events)
}

// TUI-C. Deferred question, absent record, invalid lock, recorded UNVERIFIED
// terminal: no lineage may be bound before containment is established.
func TestR373TUICQuestionLaneDoesNotBindTheLineage(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(map[bool]string{true: "unverified terminal recorded", false: "no terminal"}[terminal], func(t *testing.T) {
			s := r373QuestionSpecimen(t, terminal)
			b, _, seen := r373AnswerWhileRunning(t, s)
			if b.Refusal == nil {
				t.Errorf("TUI-C: ResumeTask bound the lineage (session %s) before containment was established:\n%s",
					b.CurrentSessionID, strings.Join(summaries(seen), "\n"))
			}
			for _, k := range r373Ledger(t, s.e.Store) {
				if k == session.SessionLineageBound {
					t.Errorf("TUI-C: a session lineage transition was recorded")
					break
				}
			}
		})
	}
}

// TUI-D. The same specimen: the standing question cannot be answered before
// containment is established.
func TestR373TUIDQuestionCannotBeAnsweredBeforeContainment(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(map[bool]string{true: "unverified terminal recorded", false: "no terminal"}[terminal], func(t *testing.T) {
			s := r373QuestionSpecimen(t, terminal)
			_, consumed, seen := r373AnswerWhileRunning(t, s)
			if consumed || contains(seen, event.AuthorityResolved) {
				t.Errorf("TUI-D: the standing question was answered before containment was established:\n%s",
					strings.Join(summaries(seen), "\n"))
			}
		})
	}
}

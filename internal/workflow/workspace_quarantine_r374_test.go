//go:build linux

package workflow

// RULING-374 witnesses. An invocation whose in-run containment admission (the
// planned lane's identity load, implement's entry, the question lane's
// resumePrecondition -- each deciding through containmentCheck) refused it
// ends on that refusal: the existing WorkflowWorkspaceQuarantined
// InvocationTerminal, never WorkflowFailed, never a behavioural FAILURE,
// whatever a later read of the record shows. The pre-fix controls are in
// workspace_quarantine_r374_control_test.go (N1-C1, N1-C1h, N1-C1q); they pass
// after the fix and are the end-to-end half of W2.
//
//	W1  the original gate refusal is the reason
//	W2  a later successful read cannot turn it into FAILED
//	W3  no behavioural FAILURE
//	W4  no further workspace write
//	W5  InvocationTerminal, not TaskTerminal; the task stays discoverable
//	W6  one terminal and one receipt, through the existing owner
//	W7  explicitly UNVERIFIED when the gate's read failed
//	W8  no fabricated PID, invocation or workspace measurement
//	W9  a future settled locked read permits the ordinary resume
//	W10 a future failed read refuses (before the lineage)
//	W11 ordinary errors keep their classifiers
//	W12 pre-binding refusals stay typed binding refusals (and N3 wording)
//	W13 headless exit 11: cmd/sensei-code/workspace_quarantine_r374_test.go
//	W14 TUI not left busy: established by the existing, unmodified
//	    internal/tui TestR368T1TheQuarantineClearsTheBusyWait -- the TUI
//	    releases busy on the terminal's kind (model.go), and W5 shows the
//	    in-run refusal ends on exactly that kind
//	W15 a currently unsettled record keeps the VERIFIED projection
//	N6  ResumeTask consumes the helper's decision (source); an in-run
//	    VERIFIED terminal stays reachable after a lawful admission

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// r374Refused takes site's refusal over an invalid lock object, then repairs
// the lock, so the record reads settled under its lock again. It returns the
// refusal and the original locked-read failure.
func r374Refused(t *testing.T, e *Engine, root string, site func(t *testing.T, e *Engine, root string) error) (error, string) {
	t.Helper()
	r371Damage["invalid lock object"](t, root)
	_, _, readErr := candidate.LoadLocked(root, wqTask)
	if readErr == nil {
		t.Fatal("premise: the lock object is invalid")
	}
	refusal := site(t, e, root)
	if !errors.Is(refusal, ErrContainmentUnestablished) {
		t.Fatalf("premise: the gate refused on the lock: %v", refusal)
	}
	if err := os.Remove(r371RecordPath(root) + ".lock"); err != nil {
		t.Fatal(err)
	}
	if id, ok, err := candidate.LoadLocked(root, wqTask); err != nil || !ok || id.Unsettled() {
		t.Fatalf("premise: the repaired record reads settled under its lock: ok=%v err=%v", ok, err)
	}
	return refusal, readErr.Error()
}

// W1-W8 at every in-run gate, under every ending.
func TestR374W1toW8TheGateRefusalIsTheEnding(t *testing.T) {
	for sname, site := range r374Sites {
		for ename, end := range r371Endings {
			t.Run(sname+"/"+ename, func(t *testing.T) {
				e, events, store, root := wqRig(t)
				cfg, statuses := r374Behaviour(t)
				e.Config.Behavioral = cfg
				refusal, readFailure := r374Refused(t, e, root, site)
				record := wqRecordBytes(t, root, wqTask)
				drainEvents(events)
				ctx := fixtureCtx(e, wqTask)
				if end.timedOut {
					e.mu.Lock()
					e.timedOut = map[string]bool{wqTask: true}
					e.mu.Unlock()
				}
				if end.cancel {
					c, cancel := context.WithCancel(ctx)
					cancel()
					ctx = c
				}
				e.terminateRun(ctx, wqTask, "the objective", refusal)
				seen := drainEvents(events)
				// W2, W6, W7, W8 (and the receipt INCOMPLETE over UNKNOWN).
				p, terminal := r371AssertUnverified(t, seen, readFailure)
				// W1: the failed admission and its read failure are the reason;
				// the account names the task and what is owed.
				reason, _ := p["reason"].(string)
				if !strings.Contains(reason, "containment admission of task "+wqTask+" failed") {
					t.Fatalf("W1: the reason is not the failed admission: %q", reason)
				}
				for _, want := range []string{wqTask, "in-run containment admission", readFailure,
					"no further workspace execution was authorized", "must be established by a locked read"} {
					if !strings.Contains(terminal.Summary, want) {
						t.Fatalf("W1: the account does not state %q: %q", want, terminal.Summary)
					}
				}
				if end.note != "" && !strings.Contains(terminal.Summary, end.note) {
					t.Fatalf("the %s is not kept in the account: %q", ename, terminal.Summary)
				}
				// W8: no writer, PID, live execution or settlement claim.
				if _, ok := p["pid"]; ok {
					t.Fatalf("W8: the terminal states a pid: %v", p)
				}
				for _, claim := range []string{"may still be written", "is settled", "teardown was"} {
					if strings.Contains(terminal.Summary, claim) {
						t.Fatalf("W8: the unverified account claims %q: %q", claim, terminal.Summary)
					}
				}
				// W5: an invocation ending; the task stays discoverable.
				if tm, ok := event.RunTerminality(terminal.Kind); !ok || tm != event.InvocationTerminal {
					t.Fatalf("W5: the ending is %q, not an invocation terminal", tm)
				}
				history, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				found := false
				for _, it := range session.FindInterrupted(history) {
					found = found || it.TaskID == wqTask
				}
				if !found {
					t.Fatal("W5: the task left the discoverable set")
				}
				// W4: no workspace, no disposition, no record write.
				if _, err := os.Stat(filepath.Join(root, "wt")); !os.IsNotExist(err) {
					t.Fatalf("W4: the workspace was touched: %v", err)
				}
				for _, ev := range seen {
					if ev.Source == event.SourceGit || ev.Kind == event.CandidateResolved {
						t.Fatalf("W4: %s %s", ev.Kind, ev.Summary)
					}
				}
				if wqRecordBytes(t, root, wqTask) != record {
					t.Fatal("W4: the candidate record was written")
				}
				// W3.
				r374NoBehaviouralFailure(t, statuses, 500*time.Millisecond)
			})
		}
	}
}

// W11. Ordinary errors keep their classifiers over a readable settled record:
// a compiler, test, review or provider error is FAILED; a candidate
// precondition keeps its typed refusal; a *candidate.ErrQuarantined that the
// containment check did not produce keeps the ordinary locked read; and an
// error that merely READS like the gate's refusal is not one.
func TestR374W11OrdinaryErrorsKeepTheirClassifiers(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want event.Kind
	}{
		"compiler":                {errors.New("go build ./...: exit status 1"), event.WorkflowFailed},
		"test":                    {errors.New("go test ./...: FAIL"), event.WorkflowFailed},
		"review":                  {errors.New("the reviewer returned no usable verdict"), event.WorkflowFailed},
		"unrelated filesystem":    {&os.PathError{Op: "open", Path: "/x", Err: os.ErrPermission}, event.WorkflowFailed},
		"message only":            {errors.New(ErrContainmentUnestablished.Error() + ": task " + wqTask), event.WorkflowFailed},
		"quarantine not the gate": {&candidate.ErrQuarantined{TaskID: wqTask, Workspace: "wt", Reason: "x"}, event.WorkflowFailed},
		"base moved":              {&candidate.ErrBaseMoved{TaskID: wqTask, Recorded: "a", Current: "b"}, event.WorkflowBaseMovedRefused},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, _ := wqRig(t)
			drainEvents(events)
			e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", c.err)
			seen := drainEvents(events)
			if contains(seen, wqKind) || !contains(seen, c.want) {
				t.Fatalf("W11: %s did not keep %s: %v", name, c.want, summaries(seen))
			}
		})
	}
}

// W15. The gate refused on an unsettled record that is still unsettled when
// the invocation ends: the VERIFIED projection of the current record.
func TestR374W15AStillUnsettledRecordIsTheVerifiedProjection(t *testing.T) {
	e, events, _, root := wqRig(t)
	wt := filepath.Join(root, "wt")
	wqInFlight(t, root, wqTask, "exec-r374", wt, "")
	_, _, refusal := containmentCheck(root, wqTask)
	var q *candidate.ErrQuarantined
	if !errors.As(refusal, &q) {
		t.Fatalf("premise: the gate refused on the record's quarantine: %v", refusal)
	}
	drainEvents(events)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", refusal)
	seen := drainEvents(events)
	wqAssertQuarantined(t, seen, wqTask, "exec-r374", wt)
	if _, terminal := wqPayload(t, seen); !strings.Contains(terminal.Summary, "in-run containment admission refused") {
		t.Fatalf("W15: the account does not say the admission refused: %q", terminal.Summary)
	}
}

// W15 (its converse, §8). The record no longer reads unsettled when the
// invocation ends: no settlement is inferred from the refusal, the original
// refusal is the reason, and the projection is UNVERIFIED.
func TestR374W15ARecordThatChangedKeepsTheOriginalRefusal(t *testing.T) {
	e, events, _, root := wqRig(t)
	wqInFlight(t, root, wqTask, "exec-r374", filepath.Join(root, "wt"), "")
	_, _, refusal := containmentCheck(root, wqTask)
	if _, err := candidate.SettleExecution(root, wqTask, "exec-r374"); err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", refusal)
	p, _ := r371AssertUnverified(t, drainEvents(events), "")
	reason, _ := p["reason"].(string)
	if !strings.Contains(reason, "was refused on its candidate record's quarantine") || !strings.Contains(reason, "exec-r374") ||
		!strings.Contains(reason, "no longer reads unsettled") {
		t.Fatalf("§8: the original refusal is not the reason: %q", reason)
	}
}

// W9, W10 (and W5 end to end). After the planned lane's gate refused -- the
// Sensei start invalidated the lock after a lawful admission -- the task is
// still offered, a resume over the still-invalid lock is refused before the
// lineage, and once the lock is repaired the ordinary resume proceeds.
func TestR374W9W10AFutureResumeIsDecidedByTheLockedGate(t *testing.T) {
	s := r373PlannedSpecimen(t)
	lock := r373LockPath(s.repoRoot, r373Task)
	script := s.e.Config.Sensei.Args[1]
	s.e.Config.Sensei.Args = []string{"-c", "ln -sfn /nonexistent-r374 '" + lock + "'\n" + script}
	b, _, seen := r373Resume(t, s.e, s.task, 60*time.Second)
	if b.Refusal != nil {
		t.Fatalf("premise: the admission bound: %v", b.Refusal)
	}
	if contains(seen, event.WorkflowFailed) || s.impl.calls.Load() != 0 {
		t.Fatalf("premise: the in-run refusal ended on the containment terminal: %v", summaries(seen))
	}
	if p, _ := wqPayload(t, seen); p["recorded"] != false {
		t.Fatalf("premise: unverified: %v", p)
	}
	s.e.Config.Sensei.Args = []string{"-c", script}
	found := reopen(t, s.root, "s1")
	if len(found) != 1 || found[0].TaskID != r373Task {
		t.Fatalf("W5: the task is not offered again: %+v", found)
	}
	// W10: the lock is still invalid.
	b, attempt, seen := r373Resume(t, s.e, found[0], 60*time.Second)
	r373AssertGateRefusal(t, b, attempt, r373Task, ErrContainmentUnestablished)
	if len(seen) != 0 || s.impl.calls.Load() != 0 {
		t.Fatalf("W10: the refused resume published %v", summaries(seen))
	}
	// W9: repaired, settled -> ordinary.
	if err := os.Remove(lock); err != nil {
		t.Fatal(err)
	}
	found = reopen(t, s.root, "s1")
	if len(found) != 1 {
		t.Fatalf("W9: the task is not offered: %+v", found)
	}
	b, _, seen = r373Resume(t, s.e, found[0], 60*time.Second)
	if b.Refusal != nil {
		t.Fatalf("W9: a settled record under a valid lock was refused: %v", b.Refusal)
	}
	if s.impl.calls.Load() == 0 || contains(seen, wqKind) {
		t.Fatalf("W9: the ordinary resume did not reach the implementer: %v", summaries(seen))
	}
}

// W12 and N3. The pre-binding refusal stays the typed binding refusal (no
// lineage, no events, no containment terminal), and says what happened: it
// was refused before binding because containment could not be established,
// naming the cause -- not that a session append was attempted. A real
// session-append failure keeps its own wording and is not the refusal.
func TestR374W12N3ThePreBindingRefusalSaysWhatHappened(t *testing.T) {
	const want = "Resume refused before session lineage binding because workspace containment could not be established."
	s := r373PlannedSpecimen(t)
	r373InvalidLock(t, s.repoRoot, r373Task)
	_, _, readErr := candidate.LoadLocked(s.repoRoot, r373Task)
	b, attempt, seen := r373Resume(t, s.e, s.task, 30*time.Second)
	r373AssertGateRefusal(t, b, attempt, r373Task, ErrContainmentUnestablished)
	if !errors.Is(b.Refusal, readErr) && !strings.Contains(b.Refusal.Error(), readErr.Error()) {
		t.Fatalf("W12: the read failure is not kept: %v", b.Refusal)
	}
	if len(seen) != 0 {
		t.Fatalf("W12: the pre-binding refusal published %v", summaries(seen))
	}
	msg := b.Refusal.Error()
	if !strings.HasPrefix(msg, want) || !strings.Contains(msg, "CANNOT ESTABLISH CONTAINMENT STATE") ||
		!strings.Contains(msg, readErr.Error()) || !strings.Contains(msg, r373Task) {
		t.Fatalf("N3: the refusal does not say what happened: %q", msg)
	}
	for _, claim := range []string{"the session record did not take", "neither recorded nor published"} {
		if strings.Contains(msg, claim) {
			t.Fatalf("N3: the refusal claims a session append (%q): %q", claim, msg)
		}
	}
	// The quarantine variant of the same refusal.
	quarantined := newRecordAppendFailure(event.New("s2", r373Task, event.SourceSystem, session.SessionLineageBound, "", nil),
		fmt.Errorf("%w: %w", ErrResumeContainmentRefused, &containmentAdmissionRefusal{err: &candidate.ErrQuarantined{TaskID: r373Task, Workspace: "wt", Reason: "in flight"}}))
	if m := quarantined.Error(); !strings.HasPrefix(m, want) || !strings.Contains(m, "is quarantined") {
		t.Fatalf("N3: the quarantine refusal does not say what happened: %q", m)
	}
	// A real session-append failure is not a containment refusal.
	appendFailure := newRecordAppendFailure(event.New("s2", r373Task, event.SourceSystem, session.SessionLineageBound, "", nil),
		errors.New("the session record lock was not released in time"))
	if errors.Is(appendFailure, ErrResumeContainmentRefused) || strings.HasPrefix(appendFailure.Error(), want) ||
		!strings.HasPrefix(appendFailure.Error(), "the session record did not take the "+string(session.SessionLineageBound)) {
		t.Fatalf("N3: a session-append failure reads as a containment refusal: %q", appendFailure.Error())
	}
}

// N6 (a). ResumeTask consumes the containment helper's decision: its gate
// halts on resumeContainmentRefusal's result and settles the binding with it;
// resumeContainmentRefusal refuses exactly when containmentCheck errs; and
// containmentCheck returns containmentFromRead's decision over its one
// LoadLocked read -- so the decision tests over containmentFromRead (E23-E26)
// are the decision ResumeTask acts on. Source level, by syntax tree.
func TestR374N6ResumeTaskConsumesTheHelpersDecision(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "engine.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	render := func(n ast.Node) string {
		var b bytes.Buffer
		_ = printer.Fprint(&b, fset, n)
		return b.String()
	}
	funcs := map[string]*ast.FuncDecl{}
	for _, d := range f.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			funcs[fd.Name.Name] = fd
		}
	}
	consumed := false
	ast.Inspect(funcs["ResumeTask"], func(n ast.Node) bool {
		is, ok := n.(*ast.IfStmt)
		if !ok || is.Init == nil || render(is.Init) != "refused := e.resumeContainmentRefusal(inv, task.TaskID)" ||
			render(is.Cond) != "refused != nil" || len(is.Body.List) != 2 {
			return true
		}
		_, returns := is.Body.List[1].(*ast.ReturnStmt)
		consumed = returns && render(is.Body.List[0]) == "attempt.settle(ResumeBinding{TaskID: task.TaskID, Refusal: refused})"
		return true
	})
	if !consumed {
		t.Fatal("N6: ResumeTask does not halt on and settle with the containment gate's decision")
	}
	gate := render(funcs["resumeContainmentRefusal"].Body)
	if !strings.Contains(gate, "if _, _, err := containmentCheck(e.Repo.Root, taskID); err != nil {") ||
		!strings.Contains(gate, "return nil") {
		t.Fatalf("N6: resumeContainmentRefusal does not refuse exactly on containmentCheck's error:\n%s", gate)
	}
	check := render(funcs["containmentCheck"].Body)
	for _, want := range []string{"id, ok, err := candidate.LoadLocked(repoRoot, taskID)",
		"id, ok, err = containmentFromRead(repoRoot, taskID, id, ok, err)",
		"return id, ok, &containmentAdmissionRefusal{err: err}", "return id, ok, nil"} {
		if !strings.Contains(check, want) {
			t.Fatalf("N6: containmentCheck does not return containmentFromRead's decision (%q):\n%s", want, check)
		}
	}
}

// N6 (b). An in-run VERIFIED quarantine terminal is still reachable: the
// admission is lawful (settled record, valid lock, lineage bound), and the
// record then becomes unsettled before the planned lane's gate. The change is
// made by the test at the Sensei start through the canonical locked writer
// (candidate.BeginExecution) -- a controlled stand-in for an execution
// recorded in flight; no lease is violated and no concurrent process runs.
func TestR374N6AnInRunVerifiedQuarantineIsStillReachable(t *testing.T) {
	s := r373PlannedSpecimen(t)
	dir := t.TempDir()
	started, ack := filepath.Join(dir, "sensei-started"), filepath.Join(dir, "in-flight")
	s.e.Config.Sensei.Args = []string{"-c", "touch '" + started + "'; while [ ! -e '" + ack + "' ]; do sleep 0.05; done\n" +
		s.e.Config.Sensei.Args[1]}
	wrote := make(chan error, 1)
	go func() {
		for deadline := time.Now().Add(60 * time.Second); ; time.Sleep(10 * time.Millisecond) {
			if _, err := os.Stat(started); err == nil {
				break
			}
			if time.Now().After(deadline) {
				wrote <- errors.New("the Sensei start never signalled")
				return
			}
		}
		_, err := candidate.BeginExecution(s.repoRoot, r373Task, candidate.InFlight{Invocation: "exec-n6", Phase: "certify", Workspace: s.worktree})
		_ = os.WriteFile(ack, nil, 0o600)
		wrote <- err
	}()
	tree := r373Tree(t, s.worktree)
	b, _, seen := r373Resume(t, s.e, s.task, 60*time.Second)
	if err := <-wrote; err != nil {
		t.Fatalf("premise: the record was made unsettled after the admission: %v", err)
	}
	if b.Refusal != nil {
		t.Fatalf("premise: the admission was lawful and bound: %v", b.Refusal)
	}
	if n := s.impl.calls.Load(); n != 0 {
		t.Fatalf("the implementer was reached %d time(s) over an unsettled record", n)
	}
	if r373Tree(t, s.worktree) != tree {
		t.Fatal("the worktree was touched")
	}
	wqAssertQuarantined(t, seen, r373Task, "exec-n6", s.worktree)
}

// E1 by a FRESH session (coordinator addition to the RULING-374 cycle; M-E1
// survived E1 because the planned specimen resumes under s1, already its
// lineage tip, so bindLineage records nothing). Here the resuming process has a
// fresh session, so a binding WOULD record a SessionLineageBound transition:
// the control shows it does when containment is established, and the refusal
// over an invalid lock records none and leaves the lineage tip unchanged.
func TestR374E1AFreshSessionRefusalRecordsNoLineageTransition(t *testing.T) {
	lineageBound := func(t *testing.T, store *session.Store, before int) bool {
		t.Helper()
		for _, k := range r373Ledger(t, store)[before:] {
			if k == session.SessionLineageBound {
				return true
			}
		}
		return false
	}
	fresh := func(t *testing.T, s r373Planned) {
		t.Helper()
		id, err := session.FreshID(time.Now())
		if err != nil {
			t.Fatal(err)
		}
		s.e.SessionID = id
	}
	t.Run("control: established containment binds a fresh session", func(t *testing.T) {
		s := r373PlannedSpecimen(t)
		fresh(t, s)
		before := len(r373Ledger(t, s.e.Store))
		b, _, _ := r373Resume(t, s.e, s.task, 60*time.Second)
		if b.Refusal != nil || b.CurrentSessionID != s.e.SessionID {
			t.Fatalf("premise: the fresh session was not bound: %+v", b)
		}
		if !lineageBound(t, s.e.Store, before) {
			t.Fatal("premise: binding a fresh session recorded no lineage transition, so the refusal case would prove nothing")
		}
	})
	t.Run("refusal: an invalid lock records no lineage transition", func(t *testing.T) {
		s := r373PlannedSpecimen(t)
		fresh(t, s)
		r373InvalidLock(t, s.repoRoot, r373Task)
		lineage, err := s.e.Store.TaskSessionLineage(r373Task)
		if err != nil {
			t.Fatal(err)
		}
		tip := lineage.Tip()
		if tip == s.e.SessionID {
			t.Fatal("premise: the resuming session is already the lineage tip")
		}
		before := len(r373Ledger(t, s.e.Store))
		b, attempt, seen := r373Resume(t, s.e, s.task, 30*time.Second)
		r373AssertGateRefusal(t, b, attempt, r373Task, ErrContainmentUnestablished)
		if lineageBound(t, s.e.Store, before) || len(seen) != 0 {
			t.Fatalf("E1: the refused resume recorded a lineage transition or published %v", summaries(seen))
		}
		if after, err := s.e.Store.TaskSessionLineage(r373Task); err != nil || after.Tip() != tip {
			t.Fatalf("E1: the lineage tip moved on a refused resume: %v", err)
		}
		if n := s.impl.calls.Load(); n != 0 {
			t.Fatalf("E1: the implementer was reached %d time(s)", n)
		}
	})
}

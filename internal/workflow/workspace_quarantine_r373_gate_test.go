//go:build linux

package workflow

// RULING-373 witnesses E1-E28 for the engine containment gate: ResumeTask
// decides containment through the one containment check (containmentCheck,
// candidate.LoadLocked) under the task's invocation lease and before the
// lineage is bound; resumePrecondition, the planned lane's identity load and
// the implementation and empty-candidate disposal gates (unsettledCandidate)
// decide through the same check. The pre-fix controls are in
// workspace_quarantine_r373_test.go (TUI-A, C, D, E); this file depends on the
// gate's symbols and has no a7b41bd run.
//
// The TUI's /resume calls Engine.ResumeTask directly and reads its outcome
// from ResumeAttempt.Binding (internal/tui/model.go waitResumeBinding): a
// non-nil Refusal becomes appendHaltedMsg, which reports "✗ RESUME", releases
// the busy composer for the task (currentTask == attempt.TaskID) and offers
// the task to /resume again. E5 is established here at the engine level --
// the gate's refusal is exactly that typed ResumeBinding.Refusal, settled
// promptly, carried as the invocation's Failure too -- and by the existing
// TUI witness of that message (internal/tui
// TestB2a1ResumeAppendHaltEndsTheResumeInModelUpdate), run unmodified.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/validation"
)

// r373Worktrees lists the repository's git worktrees, so a created or
// removed worktree is seen.
func r373Worktrees(t *testing.T, repoRoot string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", repoRoot, "worktree", "list", "--porcelain").CombinedOutput()
	if err != nil {
		t.Fatalf("git worktree list: %v\n%s", err, out)
	}
	return string(out)
}

// r373AssertGateRefusal holds what a containment pre-binding refusal owes
// (E4): the typed refusal in ResumeAttempt.Binding, no lineage claimed, the
// invocation's own Failure, the lineage-binding kind it refused, and the
// containment error it wraps.
func r373AssertGateRefusal(t *testing.T, b ResumeBinding, attempt *ResumeAttempt, taskID string, cause error) {
	t.Helper()
	if b.Refusal == nil {
		t.Fatalf("the resume was bound (session %s), not refused before binding", b.CurrentSessionID)
	}
	if b.CurrentSessionID != "" || b.TaskID != taskID || attempt.TaskID != taskID {
		t.Fatalf("the refusal claims a lineage or another task: %+v (attempt %s)", b, attempt.TaskID)
	}
	if !errors.Is(b.Refusal, ErrResumeContainmentRefused) || !errors.Is(b.Refusal, cause) {
		t.Fatalf("the refusal is not the typed containment refusal over %v: %v", cause, b.Refusal)
	}
	if b.Refusal.Kind != session.SessionLineageBound || b.Refusal.Indeterminate {
		t.Fatalf("the refusal is not a determinate pre-binding refusal: %+v", b.Refusal)
	}
	if f := attempt.Failure(); f != b.Refusal {
		t.Fatalf("the invocation's failure is not its pre-binding refusal: %v", f)
	}
	select {
	case <-attempt.Halted():
	default:
		t.Fatal("the refused invocation is not halted")
	}
}

// E1-E6. A planned task, readable settled record, invalid lock object: the
// gate refuses before the lineage; no implementer; the worktree and the
// repository's worktrees are unchanged; the typed refusal; the TUI's binding
// path is handed exactly that refusal; nothing recorded, no terminal.
func TestR373E1toE6SettledInvalidLockRefusesBeforeTheLineage(t *testing.T) {
	s := r373PlannedSpecimen(t)
	r373InvalidLock(t, s.repoRoot, r373Task)
	tree, worktrees := r373Tree(t, s.worktree), r373Worktrees(t, s.repoRoot)
	ledger := r373Ledger(t, s.e.Store)
	record, err := os.ReadFile(filepath.Join(s.repoRoot, ".sensei-code", "candidates", r373Task+".json"))
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	b, attempt, seen := r373Resume(t, s.e, s.task, 30*time.Second)
	// E1, E4: refused, typed, before the lineage, with the read failure kept.
	r373AssertGateRefusal(t, b, attempt, r373Task, ErrContainmentUnestablished)
	if !strings.Contains(b.Refusal.Error(), "CANNOT ESTABLISH CONTAINMENT STATE") || !strings.Contains(b.Refusal.Error(), "lock") {
		t.Fatalf("E4: the refusal does not explain the containment failure: %v", b.Refusal)
	}
	// E2: no implementer turn.
	if n := s.impl.calls.Load(); n != 0 {
		t.Fatalf("E2: the implementer was reached %d time(s)", n)
	}
	// E3: the worktree is neither written nor reused, and no worktree was
	// created or removed; the candidate record is unchanged.
	if r373Tree(t, s.worktree) != tree || r373Worktrees(t, s.repoRoot) != worktrees {
		t.Fatal("E3: the candidate worktree was touched")
	}
	if after, _ := os.ReadFile(filepath.Join(s.repoRoot, ".sensei-code", "candidates", r373Task+".json")); string(after) != string(record) {
		t.Fatal("E3: the candidate record was written")
	}
	// E5: the binding outcome the TUI waits on settles at once, typed, for
	// this task -- waitResumeBinding turns it into appendHaltedMsg, which
	// releases the composer of currentTask == attempt.TaskID.
	select {
	case <-attempt.Bound():
	default:
		t.Fatal("E5: the binding outcome is not settled")
	}
	if time.Since(start) > 20*time.Second {
		t.Fatalf("E5: the refusal took %s", time.Since(start))
	}
	// E6: nothing published, nothing recorded, no terminal of any kind.
	if len(seen) != 0 {
		t.Fatalf("E6: the refused resume published %v", summaries(seen))
	}
	if got := r373Ledger(t, s.e.Store); len(got) != len(ledger) {
		t.Fatalf("E6: the refused resume recorded %v", got[len(ledger):])
	}
	// The task stays discoverable as it was.
	if found := reopen(t, s.root, "s1"); len(found) != 1 || found[0].TaskID != r373Task || !found[0].Planned {
		t.Fatalf("the refused task left the discoverable set: %+v", found)
	}
}

// E7 and E21. A planned task, readable settled record, valid lock: the
// ordinary resume -- bound, and the implementer reached.
func TestR373E7E21SettledValidLockResumesNormally(t *testing.T) {
	s := r373PlannedSpecimen(t)
	b, _, seen := r373Resume(t, s.e, s.task, 60*time.Second)
	if b.Refusal != nil {
		t.Fatalf("E7: a settled record under a valid lock was refused: %v", b.Refusal)
	}
	if n := s.impl.calls.Load(); n == 0 {
		t.Fatalf("E7: the implementer was not reached:\n%s", strings.Join(summaries(seen), "\n"))
	}
	if contains(seen, wqKind) {
		t.Fatalf("E7: the ordinary resume ended quarantined: %v", summaries(seen))
	}
	// E21: the check itself keeps the settled record eligible.
	id, ok, err := containmentCheck(s.repoRoot, r373Task)
	if err != nil || !ok || id.TaskID != r373Task || unsettledCandidate(s.repoRoot, r373Task) != nil {
		t.Fatalf("E21: a verified settled record is not eligible: ok=%v err=%v", ok, err)
	}
}

// E8 and E20. An unsettled record (an execution in flight, or a quarantine):
// refused before the lineage, with the record's own quarantine refusal, and
// the implementer never reaches the workspace.
func TestR373E8E20UnsettledRefusesBeforeTheLineage(t *testing.T) {
	for _, quarantine := range []bool{false, true} {
		t.Run(map[bool]string{false: "in flight", true: "quarantined"}[quarantine], func(t *testing.T) {
			s := r373PlannedSpecimen(t)
			wqInFlight(t, s.repoRoot, r373Task, "exec-r373", s.worktree, "")
			if quarantine {
				wqQuarantine(t, s.repoRoot, r373Task)
			}
			tree := r373Tree(t, s.worktree)
			b, attempt, seen := r373Resume(t, s.e, s.task, 30*time.Second)
			var q *candidate.ErrQuarantined
			r373AssertGateRefusal(t, b, attempt, r373Task, ErrResumeContainmentRefused)
			if !errors.As(b.Refusal, &q) || q.TaskID != r373Task || errors.Is(b.Refusal, ErrContainmentUnestablished) {
				t.Fatalf("E8: the refusal is not the record's quarantine: %v", b.Refusal)
			}
			if s.impl.calls.Load() != 0 || r373Tree(t, s.worktree) != tree || len(seen) != 0 {
				t.Fatalf("E20: the quarantined workspace was reached: implementer %d, events %v", s.impl.calls.Load(), summaries(seen))
			}
		})
	}
}

// E9-E14. The question lane over an absent record and an invalid lock: the
// gate refuses before the lineage; no AuthorityResolved; the answer is not
// consumed; no Sensei or role turn starts; no Establish is attempted (no
// candidate JSON, the invalid lock object kept); the standing question is
// still discoverable, unchanged.
func TestR373E9toE14QuestionLaneRefusesBeforeTheLineage(t *testing.T) {
	for _, terminal := range []bool{true, false} {
		t.Run(map[bool]string{true: "unverified terminal recorded", false: "no terminal"}[terminal], func(t *testing.T) {
			s := r373QuestionSpecimen(t, terminal)
			marker := filepath.Join(t.TempDir(), "sensei-started")
			s.e.Config.Sensei.Args = []string{"-c", "touch '" + marker + "'\n" + s.e.Config.Sensei.Args[1]}
			ledger := r373Ledger(t, s.e.Store)
			b, consumed, seen := r373AnswerWhileRunning(t, s)
			if b.Refusal == nil || !errors.Is(b.Refusal, ErrResumeContainmentRefused) || !errors.Is(b.Refusal, ErrContainmentUnestablished) {
				t.Fatalf("E9: the question lane was not refused before the lineage: %+v", b)
			}
			after := r373Ledger(t, s.e.Store)
			if len(after) != len(ledger) {
				t.Fatalf("E9/E10: the refused resume recorded %v", after[len(ledger):])
			}
			if contains(seen, event.AuthorityResolved) || contains(seen, event.AuthorityRequired) {
				t.Fatalf("E10: the question was asked or resolved: %v", summaries(seen))
			}
			if consumed {
				t.Fatal("E11: the answer was consumed")
			}
			if _, err := os.Stat(marker); !os.IsNotExist(err) || len(seen) != 0 {
				t.Fatalf("E12: Sensei or a role turn started (marker %v), events %v", err, summaries(seen))
			}
			if _, err := os.Stat(filepath.Join(s.repoRoot, ".sensei-code", "candidates", s.task.TaskID+".json")); !os.IsNotExist(err) {
				t.Fatalf("E13: a candidate identity was established: %v", err)
			}
			if fi, err := os.Lstat(r373LockPath(s.repoRoot, s.task.TaskID)); err != nil || fi.Mode()&os.ModeSymlink == 0 {
				t.Fatalf("E13: the invalid lock object was replaced: %v %v", fi, err)
			}
			history, err := s.e.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			now := session.FindInterrupted(history)
			if len(now) != 1 || string(now[0].AwaitingAuthority) != string(s.task.AwaitingAuthority) {
				t.Fatalf("E14: the standing question is not discoverable unchanged: %+v", now)
			}
		})
	}
}

// E15 and E16. The question lane under a valid lock keeps its ordinary
// continuation: locked absence (no record) and a settled record are both
// bound, and the question is asked and answered.
func TestR373E15E16QuestionLaneKeepsTheOrdinaryContinuation(t *testing.T) {
	for _, settled := range []bool{false, true} {
		t.Run(map[bool]string{false: "E15 locked absence", true: "E16 settled record"}[settled], func(t *testing.T) {
			s := r373QuestionSpecimen(t, false)
			if err := os.Remove(r373LockPath(s.repoRoot, s.task.TaskID)); err != nil {
				t.Fatal(err)
			}
			// This fixture's session record lives inside the repository, as
			// in production, where .sensei-code/ is ignored; it is excluded
			// here so the precondition's cleanliness check reads the
			// checkout and not the record.
			if err := os.WriteFile(filepath.Join(s.repoRoot, ".git", "info", "exclude"), []byte(".sensei-code/\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if settled {
				head, err := s.e.Repo.Head(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				id := candidate.Identity{TaskID: s.task.TaskID, Repository: s.repoRoot, BaseSHA: head,
					Worktree: s.e.Repo.WorktreePath(s.task.TaskID), Branch: s.e.Repo.WorktreeBranch(s.task.TaskID),
					CreatedAt: time.Now().UTC()}
				if err := id.Save(s.repoRoot); err != nil {
					t.Fatal(err)
				}
			}
			b, consumed, seen := r373AnswerWhileRunning(t, s)
			if b.Refusal != nil {
				t.Fatalf("the ordinary question lane was refused: %v", b.Refusal)
			}
			if !consumed || !contains(seen, event.AuthorityResolved) {
				t.Fatalf("the question was not asked and answered: %v", summaries(seen))
			}
		})
	}
}

// E17. A lock that becomes unavailable after the admission and before the
// implementer reuses the workspace stops the reuse: the planned lane's check
// refuses (the Sensei start invalidates the lock object), no implementer turn
// starts, the worktree is unchanged, and the invocation ends UNVERIFIED.
func TestR373E17LockUnavailableBeforeReuseStopsTheWrite(t *testing.T) {
	s := r373PlannedSpecimen(t)
	lock := r373LockPath(s.repoRoot, r373Task)
	s.e.Config.Sensei.Args = []string{"-c", "ln -sfn /nonexistent-r373 '" + lock + "'\n" + s.e.Config.Sensei.Args[1]}
	tree := r373Tree(t, s.worktree)
	b, _, seen := r373Resume(t, s.e, s.task, 60*time.Second)
	if b.Refusal != nil {
		t.Fatalf("premise: the admission saw a valid lock and bound: %v", b.Refusal)
	}
	if fi, err := os.Lstat(lock); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("premise: the Sensei start invalidated the lock: %v %v", fi, err)
	}
	if n := s.impl.calls.Load(); n != 0 {
		t.Fatalf("E17: the implementer was reached %d time(s) after the lock became unavailable", n)
	}
	if r373Tree(t, s.worktree) != tree {
		t.Fatal("E17: the worktree was touched")
	}
	p, _ := wqPayload(t, seen)
	if p["recorded"] != false {
		t.Fatalf("E17: the invocation did not end on the UNVERIFIED quarantine: %v", summaries(seen))
	}
}

// r373Implement drives implement's entry over wqRig's record and reports the
// error it failed with; with no implementor configured, nothing past the
// entry gate can run a role.
func r373Implement(t *testing.T, e *Engine, root string) error {
	t.Helper()
	id, ok, err := candidate.Load(root, wqTask)
	if err != nil || !ok {
		id = candidate.Identity{TaskID: wqTask, Repository: root, BaseSHA: strings.Repeat("a", 40), Worktree: filepath.Join(root, "wt")}
	}
	var failed error
	tc := taskContext{Task: "the objective", Identity: id}
	e.implement(fixtureCtx(e, wqTask), nil, certifiedStart{}, wqTask, &tc, "the plan", "", func(err error) { failed = err })
	return failed
}

// E17 (implement's own gate), E18, E20, E22. implement's entry check is the
// containment check: an unavailable lock, an unreadable record, a
// contradictory identity and an unsettled record each stop it before any
// worktree is created or reused, and a Linux lock failure over a readable
// settled record is not repaired by an unlocked read.
func TestR373E17E18E20E22ImplementRefusesBeforeTheWorktree(t *testing.T) {
	for name, c := range map[string]struct {
		damage func(t *testing.T, root string)
		want   error
	}{
		"E17/E22 invalid lock over a settled record": {r371Damage["invalid lock object"], ErrContainmentUnestablished},
		"E18 torn record":            {r371Damage["torn record"], ErrContainmentUnestablished},
		"E18 contradictory identity": {r371Damage["contradictory embedded identity"], candidate.ErrIdentityContradiction},
		"E20 in flight": {func(t *testing.T, root string) {
			wqInFlight(t, root, wqTask, "exec-r373", filepath.Join(root, "wt"), "")
		}, nil},
		"E20 quarantined": {func(t *testing.T, root string) {
			wqInFlight(t, root, wqTask, "exec-r373", filepath.Join(root, "wt"), "")
			wqQuarantine(t, root, wqTask)
		}, nil},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			id, _, _ := candidate.Load(root, wqTask)
			c.damage(t, root)
			if loaded, ok, err := candidate.Load(root, wqTask); name == "E17/E22 invalid lock over a settled record" &&
				(err != nil || !ok || loaded.Unsettled()) {
				t.Fatalf("premise: an unlocked read sees a settled record: %v", err)
			}
			drainEvents(events)
			failed := r373Implement(t, e, root)
			if c.want != nil && !errors.Is(failed, c.want) {
				t.Fatalf("implement did not stop on the containment check: %v", failed)
			}
			var q *candidate.ErrQuarantined
			if c.want == nil && !errors.As(failed, &q) {
				t.Fatalf("implement did not stop on the record's quarantine: %v", failed)
			}
			if _, err := os.Stat(id.Worktree); !os.IsNotExist(err) {
				t.Fatalf("a worktree was created or reused: %v", err)
			}
			for _, ev := range drainEvents(events) {
				if ev.Source == event.SourceGit {
					t.Fatalf("implement reached the worktree: %s", ev.Summary)
				}
			}
		})
	}
}

// E19. An unsettled record -- and one whose containment cannot be
// established -- is neither observed nor disposed by the empty-candidate
// disposal: the workspace and the record stay exactly as they were.
func TestR373E19DisposalNeitherObservesNorRemoves(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, root string){
		"in flight": func(t *testing.T, root string) {
			wqInFlight(t, root, wqTask, "exec-r373", filepath.Join(root, "wt"), "")
		},
		"invalid lock": r371Damage["invalid lock object"],
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			wt := filepath.Join(root, "wt")
			if err := os.MkdirAll(wt, 0o755); err != nil {
				t.Fatal(err)
			}
			id, _, _ := candidate.Load(root, wqTask)
			damage(t, root)
			tree := r373Tree(t, wt)
			record, _ := os.ReadFile(r371RecordPath(root))
			drainEvents(events)
			tc := taskContext{Task: "the objective", Identity: id}
			e.disposeIfEmpty(fixtureCtx(e, wqTask), wqTask, id, &tc, wt, "the run did not converge")
			seen := drainEvents(events)
			if len(seen) != 1 || !strings.Contains(seen[0].Summary, "candidate neither observed nor removed") {
				t.Fatalf("the disposal did not refuse: %v", summaries(seen))
			}
			if r373Tree(t, wt) != tree {
				t.Fatal("the workspace was touched")
			}
			if after, _ := os.ReadFile(r371RecordPath(root)); string(after) != string(record) {
				t.Fatal("the record was written")
			}
		})
	}
}

// E22. A Linux lock failure is never repaired by an unlocked read: a read
// failure other than candidate.ErrNoCrossProcessLock over a readable settled
// record -- a lock not released in time, an invalid lock object -- is
// CANNOT ESTABLISH.
func TestR373E22ALinuxLockFailureIsNotRepairedByAnUnlockedRead(t *testing.T) {
	_, _, _, root := wqRig(t)
	for _, readErr := range []error{errors.New("the candidate record lock was not released in time"),
		errors.New("the candidate record lock is not a regular file")} {
		_, ok, err := containmentFromRead(root, wqTask, candidate.Identity{}, false, readErr)
		if ok || !errors.Is(err, ErrContainmentUnestablished) || !errors.Is(err, readErr) {
			t.Fatalf("a %q read was not CANNOT ESTABLISH: ok=%v %v", readErr, ok, err)
		}
	}
}

// E23-E26. The lockless platform (candidate.ErrNoCrossProcessLock): an
// ordinary candidate.Load decides, as legacy visibility only. Absent and
// readable-settled continue; unsettled and unreadable refuse.
func TestR373E23toE26TheLocklessException(t *testing.T) {
	for _, c := range []struct {
		witness  string
		specimen string
		ok       bool
		want     error
	}{
		{"E23 no candidate continues", "no record", false, nil},
		{"E24 settled continues", "settled record", true, nil},
		{"E25 unsettled refuses", "unsettled record", true, nil},
		{"E26 unreadable refuses", "unreadable record", false, ErrContainmentUnestablished},
	} {
		t.Run(c.witness, func(t *testing.T) {
			_, _, _, root := wqRig(t)
			r372Specimens[c.specimen](t, root)
			before, _ := os.ReadFile(r371RecordPath(root))
			id, ok, err := containmentFromRead(root, wqTask, candidate.Identity{}, false, candidate.ErrNoCrossProcessLock)
			switch c.witness[:3] {
			case "E23", "E24":
				if err != nil || ok != c.ok || (ok && id.TaskID != wqTask) {
					t.Fatalf("the lockless %s did not continue: ok=%v %v", c.specimen, ok, err)
				}
			case "E25":
				var q *candidate.ErrQuarantined
				if !errors.As(err, &q) {
					t.Fatalf("the lockless unsettled record did not refuse as quarantined: %v", err)
				}
			case "E26":
				if !errors.Is(err, ErrContainmentUnestablished) || !errors.Is(err, candidate.ErrNoCrossProcessLock) ||
					!strings.Contains(err.Error(), "unlocked read failed") {
					t.Fatalf("the lockless unreadable record did not refuse naming both failures: %v", err)
				}
			}
			if after, _ := os.ReadFile(r371RecordPath(root)); string(after) != string(before) {
				t.Fatal("the check wrote the record")
			}
		})
	}
}

// E27 and E28. Required confined validation cannot start without P1's
// boundary on a lockless platform, and the typed pre-start refusal is never
// fabricated into evidence that a confined execution ran: no execution, no
// in-flight state, no baseline, no quarantine terminal, no validation
// evidence, and the ordinary unsupported-confinement failure.
func TestR373E27E28LocklessPreStartRefusalFabricatesNothing(t *testing.T) {
	e, events, _, root := wqRig(t)
	injectPreStart(t, func(int, string, string, candidate.InFlight) (candidate.Identity, error, bool) {
		return candidate.Identity{}, candidate.ErrNoCrossProcessLock, true
	})
	before := wqRecordBytes(t, root, wqTask)
	ran := false
	runner := validation.Runner{Confine: &validation.Confinement{Profile: "p", Command: "c", RequireStatus: true}}
	err := e.boundedRun(fixtureCtx(e, wqTask), wqTask, "certify", filepath.Join(root, "wt"), runner,
		func(string) []validation.Evidence { ran = true; return nil }, func() string { return "" })
	if ran || !confinementUnsupportedBeforeStart(err) {
		t.Fatalf("E27: a required confined execution started without the P1 boundary (ran=%v): %v", ran, err)
	}
	drainEvents(events)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", err)
	seen := drainEvents(events)
	r367Ordinary(t, seen)
	if contains(seen, event.ValidationRun) {
		t.Fatalf("E28: validation evidence was fabricated: %v", summaries(seen))
	}
	if after := wqRecordBytes(t, root, wqTask); after != before {
		t.Fatal("E28: the refusal wrote the candidate record")
	}
	id, _, _ := candidate.Load(root, wqTask)
	if id.InFlight != nil || id.Unsettled() {
		t.Fatalf("E28: the record claims an execution: %+v", id)
	}
}

// Source order (RULING-373 §6, §26 B-C; M-E1, M-E3): in ResumeTask the
// lease precedes the containment gate, which precedes the lineage binding;
// every engine containment decision goes through containmentCheck, whose one
// read is candidate.LoadLocked.
func TestR373TheGateIsOrderedAndCanonical(t *testing.T) {
	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	body := func(sig string) string {
		i := strings.Index(s, sig)
		if i < 0 {
			t.Fatalf("%s is missing", sig)
		}
		b := s[i:]
		return b[:strings.Index(b, "\n}\n")]
	}
	resume := body("func (e *Engine) ResumeTask(")
	lease := strings.Index(resume, "e.leaseInvocation(ctx, inv, e.Store, session.SessionLineageBound)")
	gate := strings.Index(resume, "e.resumeContainmentRefusal(inv, task.TaskID)")
	bind := strings.Index(resume, "e.bindLineage(inv, task.TaskID)")
	if lease < 0 || gate < 0 || bind < 0 || !(lease < gate && gate < bind) {
		t.Fatalf("ResumeTask is not lease -> containment gate -> bindLineage (%d, %d, %d)", lease, gate, bind)
	}
	if strings.Count(resume, "containmentCheck(e.Repo.Root, task.TaskID)") != 1 ||
		strings.Contains(resume, "identity, ok, idErr := candidate.Load(") {
		t.Fatal("the planned lane's identity load does not decide through containmentCheck")
	}
	for _, sig := range []string{"func (e *Engine) resumeContainmentRefusal(", "func unsettledCandidate(",
		"func (e *Engine) resumePrecondition("} {
		b := body(sig)
		if !strings.Contains(b, "containmentCheck(") || strings.Contains(b, "candidate.Load(") {
			t.Fatalf("%s does not decide through containmentCheck alone", sig)
		}
	}
	check := body("func containmentCheck(")
	if !strings.Contains(check, "candidate.LoadLocked(repoRoot, taskID)") {
		t.Fatal("containmentCheck does not read through candidate.LoadLocked")
	}
}

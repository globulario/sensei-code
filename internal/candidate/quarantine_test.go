package candidate

// N5 P1 witnesses (RULING-358 §12-§13, §18 N and Q): the durable in-flight
// boundary and the quarantine live on the candidate record, survive a fresh
// read (a restart), and no ordinary write can remove them.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func recorded(t *testing.T) (string, Identity) {
	t.Helper()
	root := t.TempDir()
	id := Identity{TaskID: "task-1", Repository: root, BaseSHA: strings.Repeat("a", 40), Worktree: filepath.Join(root, "wt"), Branch: "b", CreatedAt: time.Now().UTC()}
	if err := id.Save(root); err != nil {
		t.Fatal(err)
	}
	return root, id
}

func inflight(inv string) InFlight {
	return InFlight{Invocation: inv, Phase: "format", Workspace: "/w", Profile: "d2-v3"}
}

// The boundary refuses to bind an execution to a task with no record: the
// execution must not start.
func TestNoExecutionIsBoundWithoutAnIdentity(t *testing.T) {
	if _, err := BeginExecution(t.TempDir(), "task-1", inflight("exec-1")); !errors.Is(err, ErrNoIdentity) {
		t.Fatalf("an execution was bound with no identity recorded: %v", err)
	}
}

// Q. An execution recorded in flight and never settled is read as unsettled
// by a fresh reader -- a later process after a crash -- and a settled one is
// not.
func TestAnInFlightExecutionIsUnsettledOnAFreshRead(t *testing.T) {
	root, _ := recorded(t)
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	fresh, ok, err := Load(root, "task-1")
	if err != nil || !ok || !fresh.Unsettled() || fresh.Quarantine() == nil || fresh.InFlight.Invocation != "exec-1" {
		t.Fatalf("an interrupted execution's record does not read as unsettled: %+v (%v)", fresh, err)
	}
	if _, err := BeginExecution(root, "task-1", inflight("exec-2")); err == nil {
		t.Fatal("a second execution started over an unsettled one")
	}
	if _, err := SettleExecution(root, "task-1", "exec-other"); err == nil {
		t.Fatal("another invocation settled this one")
	}
	if _, err := SettleExecution(root, "task-1", "exec-1"); err != nil {
		t.Fatal(err)
	}
	if settled, _, _ := Load(root, "task-1"); settled.Unsettled() {
		t.Fatalf("a settled execution still reads as unsettled: %+v", settled)
	}
}

// N. A quarantine is never replaced by an ordinary resolution or by an
// ordinary save of a stale in-memory copy, and it keeps the in-flight
// record it is bound to.
func TestAQuarantineCannotBeOverwritten(t *testing.T) {
	root, stale := recorded(t)
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	q, err := QuarantineExecution(root, "task-1", Resolution{Reason: "first unresolved execution"})
	if err != nil || q.Resolution.Disposition != Quarantined || q.InFlight == nil || q.InFlight.Invocation != "exec-1" ||
		q.Resolution.WorktreeRemoved || !q.Resolution.Disposition.Keeps() {
		t.Fatalf("the quarantine is not bound to its invocation: %+v (%v)", q, err)
	}
	for name, r := range map[string]Resolution{
		"resumable": {Disposition: Resumable, Reason: "ordinary path"},
		"retained":  {Disposition: Retained, Reason: "ordinary path"},
		"disposed":  {Disposition: Disposed, Reason: "ordinary path", Evidence: Evidence{BaseSHA: stale.BaseSHA, ProducedNoWork: true}},
	} {
		var target *ErrQuarantined
		if _, err := stale.Resolve(root, r); !errors.As(err, &target) {
			t.Fatalf("%s: an ordinary resolution replaced the quarantine: %v", name, err)
		}
	}
	if err := stale.Save(root); err == nil {
		t.Fatal("a stale in-memory identity replaced the durable quarantine")
	}
	if _, err := stale.Resolve(root, Resolution{Disposition: Quarantined, Reason: "x"}); err == nil {
		t.Fatal("an ordinary resolution recorded a quarantine")
	}
	again, err := QuarantineExecution(root, "task-1", Resolution{Reason: "a later one"})
	if err != nil || again.Resolution.Reason != "first unresolved execution" {
		t.Fatalf("a later quarantine replaced the first: %+v (%v)", again, err)
	}
	if _, err := SettleExecution(root, "task-1", "exec-1"); err == nil {
		t.Fatal("a quarantined execution was settled by the ordinary settlement")
	}
	fresh, _, _ := Load(root, "task-1")
	if fresh.Resolution.Disposition != Quarantined || fresh.InFlight == nil {
		t.Fatalf("the durable record lost the quarantine: %+v", fresh)
	}
}

// An ordinary save over an in-flight record is refused too: only the
// settlement of that exact invocation clears it.
func TestAStaleSaveCannotClearAnInFlightRecord(t *testing.T) {
	root, stale := recorded(t)
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.BindGraph(root, "g", "s"); err == nil {
		t.Fatal("an ordinary write cleared the in-flight record")
	}
	if fresh, _, _ := Load(root, "task-1"); fresh.InFlight == nil {
		t.Fatal("the in-flight record was lost")
	}
}

// A record that exists but cannot be read is not overwritten by an ordinary
// save: whether it held a quarantine is unknown.
func TestAnUnreadableRecordIsNotOverwritten(t *testing.T) {
	root, id := recorded(t)
	target := filepath.Join(root, ".sensei-code", "candidates", "task-1.json")
	if err := os.WriteFile(target, []byte("{torn"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := id.Save(root); err == nil {
		t.Fatal("an unreadable record was overwritten")
	}
	if b, _ := os.ReadFile(target); string(b) != "{torn" {
		t.Fatalf("the unreadable record was changed: %q", b)
	}
}

// The baseline workspace is named on the in-flight record of exactly the
// invocation that runs it, never without an execution in flight and never on
// the strength of another invocation's binding (RULING-360 §4).
func TestTheBaselineIsBoundToTheInFlightRecord(t *testing.T) {
	root, _ := recorded(t)
	if err := NoteBaseline(root, "task-1", "exec-1", "/base"); err == nil {
		t.Fatal("a baseline was bound with no execution in flight")
	}
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	if err := NoteBaseline(root, "task-1", "exec-other", "/base"); err == nil {
		t.Fatal("a baseline was bound to an invocation that is not in flight")
	}
	if err := NoteBaseline(root, "task-1", "exec-1", "/base"); err != nil {
		t.Fatal(err)
	}
	if fresh, _, _ := Load(root, "task-1"); fresh.InFlight == nil || fresh.InFlight.Baseline != "/base" {
		t.Fatalf("the baseline is not on the durable record: %+v", fresh)
	}
	// A later invocation's record does not inherit the earlier binding.
	if _, err := SettleExecution(root, "task-1", "exec-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := BeginExecution(root, "task-1", inflight("exec-2")); err != nil {
		t.Fatal(err)
	}
	if fresh, _, _ := Load(root, "task-1"); fresh.InFlight.Baseline != "" {
		t.Fatalf("a new invocation inherited an earlier baseline binding: %+v", fresh.InFlight)
	}
}

// W3 in one process: a stale copy with the same invocation but without the
// baseline the record now names, and a quarantine in an older representation,
// are both refused (RULING-360 §11).
func TestTheWholeSafetyStateIsMonotonic(t *testing.T) {
	root, _ := recorded(t)
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	stale, _, _ := Load(root, "task-1")
	if err := NoteBaseline(root, "task-1", "exec-1", "/base"); err != nil {
		t.Fatal(err)
	}
	if err := stale.Save(root); err == nil {
		t.Fatal("a stale copy with the same invocation dropped the baseline binding")
	}
	if _, err := QuarantineExecution(root, "task-1", Resolution{Reason: "first"}); err != nil {
		t.Fatal(err)
	}
	older, _, _ := Load(root, "task-1")
	older.Resolution.Reason = "an older representation"
	if err := older.Save(root); err == nil {
		t.Fatal("a quarantine was rewritten with another representation")
	}
	if fresh, _, _ := Load(root, "task-1"); fresh.Resolution.Reason != "first" || fresh.InFlight.Baseline != "/base" {
		t.Fatalf("the durable safety state changed: %+v", fresh)
	}
}

// An ordinary resolution is refused while an execution is in flight, not only
// over a quarantine (defence in depth below the workflow's disposal gate).
func TestNoOrdinaryResolutionWhileAnExecutionIsInFlight(t *testing.T) {
	root, stale := recorded(t)
	inFlight, err := BeginExecution(root, "task-1", inflight("exec-1"))
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []Identity{stale, inFlight} {
		var q *ErrQuarantined
		if _, err := id.Resolve(root, Resolution{Disposition: Disposed, Reason: "x", Evidence: Evidence{BaseSHA: id.BaseSHA, ProducedNoWork: true}}); !errors.As(err, &q) {
			t.Fatalf("an ordinary resolution was recorded over an execution in flight: %v", err)
		}
	}
}

// The record is replaced atomically: no temporary file is left beside it,
// and the listing sees exactly the one record (the record's lock file is
// beside it and is not a record).
func TestTheRecordIsReplacedAtomically(t *testing.T) {
	root, _ := recorded(t)
	if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
		t.Fatal(err)
	}
	if _, err := SettleExecution(root, "task-1", "exec-1"); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".sensei-code", "candidates"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if e.Name() != "task-1.json.lock" {
			names = append(names, e.Name())
		}
	}
	if len(names) != 1 || names[0] != "task-1.json" {
		t.Fatalf("the replacement left more than the record: %v", entries)
	}
	if list, err := List(root); err != nil || len(list) != 1 {
		t.Fatalf("the listing does not see exactly one record: %v %v", list, err)
	}
}

package candidate

// RULING-370 finding A (§2-§8, §10-§11): the filename/requested task id is
// the identity of the record being read; the embedded task_id is a claim,
// accepted only when the two agree exactly. A missing, empty or different
// embedded task_id is a contradiction: the canonical reader returns a typed
// identity-read error and no Identity, for Load, LoadLocked and every writer
// that reads through it, and it never alters the record. A4-A7 and A12-A16
// are the reader witnesses; A1-A3, A8-A11 and the CLI halves of A14-A16 are
// in cmd/sensei-code.

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// r370Record writes, under the record name of fileTask, a record whose
// embedded task_id is claim -- left out entirely when omit -- carrying a
// durable in-flight execution, and returns the record path and its bytes.
func r370Record(t *testing.T, root, fileTask, claim string, omit bool) (string, []byte) {
	t.Helper()
	id := Identity{TaskID: claim, Repository: root, BaseSHA: strings.Repeat("a", 40), Worktree: filepath.Join(root, "wt"),
		Branch: "b", CreatedAt: time.Now().UTC(), InFlight: &InFlight{Invocation: "exec-1", Phase: "certify", Workspace: "/w"}}
	body, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	if omit {
		var m map[string]any
		if err := json.Unmarshal(body, &m); err != nil {
			t.Fatal(err)
		}
		delete(m, "task_id")
		if body, err = json.Marshal(m); err != nil {
			t.Fatal(err)
		}
	}
	target := path(root, fileTask)
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return target, body
}

// r370Contradictions are the three contradictory claims a record named
// task-1 can carry.
var r370Contradictions = map[string]struct {
	claim string
	omit  bool
}{
	"another task's id": {"task-2", false},
	"an empty task_id":  {"", false},
	"no task_id":        {"", true},
	"a padded task_id":  {" task-1", false},
}

func r370Unchanged(t *testing.T, target string, want []byte, after string) {
	t.Helper()
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("A15: the record is gone after %s: %v", after, err)
	}
	if string(got) != string(want) {
		t.Errorf("A15: %s changed the contradictory record", after)
	}
}

// A4 A6. Load rejects a record whose embedded identity is not the requested
// one -- different, empty or missing -- with the typed contradiction and no
// Identity; the embedded id is never selected.
func TestR370A4LoadRejectsAContradictoryIdentity(t *testing.T) {
	for name, c := range r370Contradictions {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target, body := r370Record(t, root, "task-1", c.claim, c.omit)
			id, ok, err := Load(root, "task-1")
			if !errors.Is(err, ErrIdentityContradiction) {
				t.Fatalf("Load accepted a record of task-1 claiming %q: ok=%v err=%v", c.claim, ok, err)
			}
			if ok || id.TaskID != "" || id.InFlight != nil || id.BaseSHA != "" {
				t.Errorf("a contradictory read returned an identity: ok=%v %+v", ok, id)
			}
			if !strings.Contains(err.Error(), "task-1") {
				t.Errorf("the contradiction does not name the requested identity: %v", err)
			}
			r370Unchanged(t, target, body, "Load")
		})
	}
}

// A5 A6. LoadLocked rejects it under the record lock, the same way.
func TestR370A5LoadLockedRejectsAContradictoryIdentity(t *testing.T) {
	if !crossProcessLock {
		t.Skip("no cross-process lock on this platform; A14 covers the lockless read")
	}
	for name, c := range r370Contradictions {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target, body := r370Record(t, root, "task-1", c.claim, c.omit)
			id, ok, err := LoadLocked(root, "task-1")
			if !errors.Is(err, ErrIdentityContradiction) {
				t.Fatalf("LoadLocked accepted a record of task-1 claiming %q: ok=%v err=%v", c.claim, ok, err)
			}
			if ok || id.TaskID != "" || id.InFlight != nil {
				t.Errorf("a contradictory locked read returned an identity: ok=%v %+v", ok, id)
			}
			r370Unchanged(t, target, body, "LoadLocked")
		})
	}
}

// A7 A16. List keeps its read-error behaviour: the contradictory record is
// reported under its filename identity as unreadable -- never omitted, never
// appended under the embedded id, never settled, and its untrusted InFlight
// is not copied into the entry.
func TestR370A7ListReportsAContradictionUnderTheFilenameIdentity(t *testing.T) {
	for name, c := range r370Contradictions {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			target, body := r370Record(t, root, "task-1", c.claim, c.omit)
			list, err := List(root)
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != 1 {
				t.Fatalf("List = %+v, want exactly the one record under task-1", list)
			}
			got := list[0]
			if got.TaskID != "task-1" {
				t.Errorf("the record is listed under %q, not its filename identity task-1", got.TaskID)
			}
			if got.Resolution == nil || !strings.Contains(got.Resolution.Reason, "unreadable") ||
				!strings.Contains(got.Resolution.Reason, ErrIdentityContradiction.Error()) {
				t.Errorf("the record is not reported as an unreadable contradiction: %+v", got.Resolution)
			}
			if got.InFlight != nil || got.BaseSHA != "" {
				t.Errorf("untrusted record content was copied into the listing: %+v", got)
			}
			r370Unchanged(t, target, body, "List")
			if _, err := os.Stat(path(root, "task-2")); !os.IsNotExist(err) {
				t.Errorf("A16: a task-2 record exists because task-1.json claimed task-2: %v", err)
			}
		})
	}
}

// A12. A record whose embedded identity is the requested one keeps its
// ordinary behaviour through every reader.
func TestR370A12AMatchingIdentityKeepsOrdinaryBehaviour(t *testing.T) {
	root, saved := recorded(t)
	id, ok, err := Load(root, "task-1")
	if err != nil || !ok || id.TaskID != "task-1" || id.BaseSHA != saved.BaseSHA {
		t.Fatalf("Load of a matching record = %+v %v %v", id, ok, err)
	}
	if crossProcessLock {
		if id, ok, err := LoadLocked(root, "task-1"); err != nil || !ok || id.TaskID != "task-1" {
			t.Fatalf("LoadLocked of a matching record = %+v %v %v", id, ok, err)
		}
	}
	list, err := List(root)
	if err != nil || len(list) != 1 || list[0].TaskID != "task-1" || list[0].Resolution != nil {
		t.Fatalf("List of a matching record = %+v %v", list, err)
	}
	if crossProcessLock {
		if _, err := BeginExecution(root, "task-1", inflight("exec-1")); err != nil {
			t.Fatalf("a matching record refused an execution: %v", err)
		}
		if _, err := SettleExecution(root, "task-1", "exec-1"); err != nil {
			t.Fatalf("a matching record refused its settlement: %v", err)
		}
	}
}

// A13. A genuinely absent record stays absent, and the read creates none.
func TestR370A13AnAbsentRecordStaysAbsent(t *testing.T) {
	root := t.TempDir()
	if id, ok, err := Load(root, "task-1"); err != nil || ok || id.TaskID != "" {
		t.Fatalf("Load of an absent record = %+v %v %v", id, ok, err)
	}
	if crossProcessLock {
		if id, ok, err := LoadLocked(root, "task-1"); err != nil || ok || id.TaskID != "" {
			t.Fatalf("LoadLocked of an absent record = %+v %v %v", id, ok, err)
		}
	}
	if _, err := os.Stat(path(root, "task-1")); !os.IsNotExist(err) {
		t.Errorf("reading an absent record created it: %v", err)
	}
}

// A14. The lockless legacy path (RULING-367: candidate.Load is the fallback
// where LoadLocked has no lock) applies the same identity check; nothing about
// the lock decides it.
func TestR370A14TheLocklessReadRejectsAContradiction(t *testing.T) {
	root := t.TempDir()
	target, body := r370Record(t, root, "task-1", "task-2", false)
	// Load is exactly the read a lockless platform falls back to: no lock
	// object is consulted or created by it.
	if _, _, err := Load(root, "task-1"); !errors.Is(err, ErrIdentityContradiction) {
		t.Fatalf("the unlocked read accepted a contradictory identity: %v", err)
	}
	if _, err := os.Stat(target + ".lock"); !os.IsNotExist(err) {
		t.Errorf("the unlocked read touched the record lock: %v", err)
	}
	r370Unchanged(t, target, body, "the unlocked read")
}

// A15 A16, and the writers that read through the common loader: every one
// refuses a contradictory record (fail closed), writes nothing to it and
// invents no record under the embedded identity.
func TestR370A15WritersRefuseAContradictoryRecordAndChangeNothing(t *testing.T) {
	if !crossProcessLock {
		t.Skip("the in-flight writers refuse without a cross-process lock")
	}
	root := t.TempDir()
	target, body := r370Record(t, root, "task-1", "task-2", false)
	writers := map[string]func() error{
		"Save": func() error {
			return Identity{TaskID: "task-1", Repository: root, BaseSHA: strings.Repeat("b", 40), CreatedAt: time.Now().UTC()}.Save(root)
		},
		"BeginExecution":  func() error { _, err := BeginExecution(root, "task-1", inflight("exec-2")); return err },
		"NoteBaseline":    func() error { return NoteBaseline(root, "task-1", "exec-1", "/w-baseline") },
		"SettleExecution": func() error { _, err := SettleExecution(root, "task-1", "exec-1"); return err },
		"QuarantineExecution": func() error {
			_, err := QuarantineExecution(root, "task-1", Resolution{Reason: "no established teardown"})
			return err
		},
		"Establish": func() error {
			_, err := Establish(root, "task-1", "d", "/wt", "b", &fakeRepo{head: strings.Repeat("a", 40), clean: true}, when)
			return err
		},
	}
	for name, write := range writers {
		if err := write(); !errors.Is(err, ErrIdentityContradiction) {
			t.Errorf("%s did not refuse the contradictory record: %v", name, err)
		}
		r370Unchanged(t, target, body, name)
	}
	if _, err := os.Stat(path(root, "task-2")); !os.IsNotExist(err) {
		t.Errorf("A16: a task-2 record was invented: %v", err)
	}
}

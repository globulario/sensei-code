package main

// RULING-375 (D375). A complete `resume --list` positively asserts that the
// candidate inventory was enumerated. When .sensei-code/candidates cannot be
// listed the inventory is UNKNOWN -- not empty, not complete: the listing
// still shows every known containment and session fact and the diagnostic,
// and then fails. Successful discovery keeps its existing exit semantics.
// Nothing here writes a candidate record, a workspace or a session.

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

const r375ListDiag = "the candidate records of this repository could not be listed"

// r375Unlistable makes the candidate inventory deterministically unlistable:
// .sensei-code/candidates is a regular file, so enumerating it fails with
// ENOTDIR whatever the caller's privileges.
func r375Unlistable(t *testing.T, root string) {
	t.Helper()
	dir := filepath.Join(root, ".sensei-code")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "candidates"), []byte("not a directory\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// r375Snapshot is every file under .sensei-code and .worktrees, path -> bytes.
// Lock objects are left out: the locked read may create one (RULING-372
// §10-§11), and creating it is not a record, workspace or session mutation.
func r375Snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, top := range []string{".sensei-code", ".worktrees"} {
		base := filepath.Join(root, top)
		err := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if os.IsNotExist(err) && path == base {
					return nil
				}
				return err
			}
			rel, _ := filepath.Rel(root, path)
			if d.IsDir() {
				out[rel+"/"] = ""
				return nil
			}
			if strings.HasSuffix(path, ".lock") {
				return nil
			}
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			out[rel] = string(b)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func r375SameSnapshot(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for path, b := range before {
		if a, ok := after[path]; !ok {
			t.Errorf("%s: %s disappeared", what, path)
		} else if a != b {
			t.Errorf("%s: %s changed", what, path)
		}
	}
	for path := range after {
		if _, ok := before[path]; !ok && !strings.HasSuffix(path, "/") {
			t.Errorf("%s: %s was created", what, path)
		}
	}
}

// D375-W1. No session history and an unlistable inventory: the diagnostic is
// shown, nothing is called absent, and the listing fails.
func TestD375W1NoHistoryUnlistableInventoryFails(t *testing.T) {
	root := gitRepoRoot(t)
	r375Unlistable(t, root)
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitFailed {
		t.Errorf("an unlistable inventory with no history did not fail: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, r375ListDiag) || !strings.Contains(stdout, wqUnknownText) {
		t.Errorf("the discovery diagnostic is not shown:\n%s\n%s", stdout, stderr)
	}
	if strings.Contains(stderr, errNoSession.Error()) {
		t.Errorf("an unknown inventory was reported as no session:\n%s", stderr)
	}
	if strings.HasPrefix(stdout, "task ") || strings.Contains(stdout, "\ntask ") {
		t.Errorf("an orphan was invented from an inventory nobody could list:\n%s", stdout)
	}
}

// D375-W2. Session history and an unlistable inventory: every known task is
// still listed, the diagnostic is shown, and the listing fails.
func TestD375W2HistoryUnlistableInventoryShowsKnownTasksAndFails(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260301T000000.000000000Z"
	writeRawSession(t, root, holder,
		event.New(holder, "t-a", event.SourceSystem, event.TaskCreated, "first work", nil),
		event.New(holder, "t-b", event.SourceSystem, event.TaskCreated, "second work", nil))
	r375Unlistable(t, root)
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitFailed {
		t.Errorf("an unlistable inventory with history did not fail: exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, task := range []string{"t-a", "t-b"} {
		if wqBlock(stdout, task) == "" {
			t.Errorf("the known task %s is hidden:\n%s", task, stdout)
		}
	}
	if !strings.Contains(stdout, r375ListDiag) {
		t.Errorf("the discovery diagnostic is not shown:\n%s\n%s", stdout, stderr)
	}
}

// D375-W2, permission variant: the inventory directory can be entered and
// written but not read, so a held task's locked read succeeds and its
// established quarantine is shown, while enumeration fails.
func TestD375W2UnreadableInventoryKeepsAnEstablishedQuarantineVisible(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("permission bits do not bind root; the ENOTDIR witness covers this path")
	}
	root := gitRepoRoot(t)
	const holder = "session-20260302T000000.000000000Z"
	wqCandidate(t, root, "t-q", true)
	writeRawSession(t, root, holder,
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "quarantined work", nil),
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	// Every lock object a held task's read needs exists before the directory
	// loses its read bit.
	if code, out, errs := runResume(t, root, "--list"); code != exitCompleted {
		t.Fatalf("premise: the readable listing failed: exit %d\n%s\n%s", code, out, errs)
	}
	dir := filepath.Dir(wqRecordPath(root, "t-q"))
	if err := os.Chmod(dir, 0o300); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("this filesystem lists a directory without its read bit")
	}
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitFailed {
		t.Errorf("an unreadable inventory did not fail: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if b := wqBlock(stdout, "t-q"); !strings.Contains(b, wqLaneText) {
		t.Errorf("the established quarantine is hidden:\n%s\n%s", stdout, stderr)
	}
	if wqBlock(stdout, "t-ok") == "" || !strings.Contains(stdout, r375ListDiag) {
		t.Errorf("a known task or the diagnostic is hidden:\n%s\n%s", stdout, stderr)
	}
}

// D375-W3. A scoped listing filters presentation, not discovery: a
// repository-wide enumeration failure is not hidden and the listing fails.
func TestD375W3ScopedListingDoesNotHideAnUnlistableInventory(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260303T000000.000000000Z"
	writeRawSession(t, root, holder,
		event.New(holder, "t-a", event.SourceSystem, event.TaskCreated, "scoped work", nil))
	r375Unlistable(t, root)
	code, stdout, stderr := runResume(t, root, "--list", "--session", holder)
	if code != exitFailed {
		t.Errorf("a scoped listing over an unlistable inventory did not fail: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout+stderr, r375ListDiag) {
		t.Errorf("the scoped listing hid the discovery failure:\n%s\n%s", stdout, stderr)
	}
	if wqBlock(stdout, "t-a") == "" {
		t.Errorf("the scoped listing hid its known task:\n%s", stdout)
	}
}

// D375-W4. A successful inventory with no orphans keeps the successful exit.
func TestD375W4ListableInventoryWithoutOrphansSucceeds(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260304T000000.000000000Z"
	wqSettledCandidate(t, root, "t-ok")
	writeRawSession(t, root, holder,
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	for _, args := range [][]string{{"--list"}, {"--list", "--session", holder}} {
		code, stdout, stderr := runResume(t, root, args...)
		if code != exitCompleted || strings.Contains(stdout+stderr, r375ListDiag) {
			t.Errorf("%v: a listable inventory was treated as a failure: exit %d\n%s\n%s", args, code, stdout, stderr)
		}
		if !strings.Contains(stdout, "resume     --task t-ok") {
			t.Errorf("%v: the ordinary task is not offered:\n%s", args, stdout)
		}
	}
}

// D375-W5. A successful inventory with quarantined orphans keeps its display
// and exit behaviour, with and without session history.
func TestD375W5ListableInventoryWithOrphansKeepsItsBehaviour(t *testing.T) {
	bare := gitRepoRoot(t)
	wqCandidate(t, bare, "t-alone", true)
	code, stdout, stderr := runResume(t, bare, "--list")
	if code != exitCompleted || !strings.Contains(wqBlock(stdout, "t-alone"), wqLaneText) {
		t.Errorf("no history: the orphan display changed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	root := gitRepoRoot(t)
	const holder = "session-20260305T000000.000000000Z"
	wqCandidate(t, root, "t-gone", true)
	writeRawSession(t, root, holder,
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	code, stdout, stderr = runResume(t, root, "--list")
	if code != exitCompleted || !strings.Contains(wqBlock(stdout, "t-gone"), wqLaneText) ||
		!strings.Contains(stdout, "resume     --task t-ok") {
		t.Errorf("history: the orphan display changed: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout+stderr, r375ListDiag) {
		t.Errorf("a listable inventory reported a discovery failure:\n%s\n%s", stdout, stderr)
	}
}

// D375-W6. A valid EMPTY inventory -- no candidates directory, or an empty one
// -- is not an I/O failure: with no history it keeps the no-session answer,
// with history the listing succeeds.
func TestD375W6AValidEmptyInventoryIsNotAFailure(t *testing.T) {
	for _, withDir := range []bool{false, true} {
		root := gitRepoRoot(t)
		if withDir {
			if err := os.MkdirAll(filepath.Dir(wqRecordPath(root, "x")), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		code, stdout, stderr := runResume(t, root, "--list")
		if code != exitUsage || !strings.Contains(stderr, errNoSession.Error()) || strings.Contains(stdout+stderr, r375ListDiag) {
			t.Errorf("dir=%v: an empty inventory with no history is misclassified: exit %d\n%s\n%s",
				withDir, code, stdout, stderr)
		}
		const holder = "session-20260306T000000.000000000Z"
		writeRawSession(t, root, holder,
			event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
		code, stdout, stderr = runResume(t, root, "--list")
		if code != exitCompleted || strings.Contains(stdout+stderr, r375ListDiag) {
			t.Errorf("dir=%v: an empty inventory with history is misclassified: exit %d\n%s\n%s",
				withDir, code, stdout, stderr)
		}
	}
}

// r375ReviewUnreadable makes the durable review obligation of taskID
// unreadable, as RULING-367's N3 witness does.
func r375ReviewUnreadable(t *testing.T, root, taskID string) {
	t.Helper()
	dir := filepath.Join(root, ".sensei-code", "exchanges")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(map[string]any{
		"task_id": taskID, "request_id": "r-bad", "kind": "review", "base": "not-a-sha",
		"candidate_digest": "sha256:" + strings.Repeat("b", 40), "candidate_tree": strings.Repeat("c", 40),
		"review_commit": strings.Repeat("d", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "r-bad.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
}

// D375-W7. A review-obligation read failure still fails with the known
// containment obligations visible -- alone, and together with an unlistable
// inventory, where the review failure keeps its precedence.
func TestD375W7AReviewReadFailureStillFailsWithObligationsVisible(t *testing.T) {
	const holder = "session-20260307T000000.000000000Z"
	root := gitRepoRoot(t)
	wqCandidate(t, root, "t-q", true)
	wqCandidate(t, root, "t-orphan", true)
	writeRawSession(t, root, holder,
		event.New(holder, "t-rev", event.SourceSystem, event.TaskCreated, "unreadable review", nil),
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "quarantined work", nil))
	r375ReviewUnreadable(t, root, "t-rev")
	code, stdout, stderr := runResume(t, root, "--list")
	if code != exitFailed || !strings.Contains(stderr, "review obligation of task t-rev could not be established") {
		t.Errorf("a review read failure did not fail with its error: exit %d\n%s", code, stderr)
	}
	for _, task := range []string{"t-q", "t-orphan"} {
		if !strings.Contains(wqBlock(stdout, task), wqLaneText) {
			t.Errorf("the obligation of %s is hidden:\n%s", task, stdout)
		}
	}

	both := gitRepoRoot(t)
	writeRawSession(t, both, holder,
		event.New(holder, "t-rev", event.SourceSystem, event.TaskCreated, "unreadable review", nil),
		event.New(holder, "t-a", event.SourceSystem, event.TaskCreated, "known work", nil))
	r375ReviewUnreadable(t, both, "t-rev")
	r375Unlistable(t, both)
	code, stdout, stderr = runResume(t, both, "--list")
	if code != exitFailed || !strings.Contains(stderr, "review obligation of task t-rev could not be established") {
		t.Errorf("review failure + unlistable inventory: not failed with the review error: exit %d\n%s\n%s", code, stdout, stderr)
	}
	if !strings.Contains(stdout, r375ListDiag) || wqBlock(stdout, "t-a") == "" {
		t.Errorf("review failure + unlistable inventory: the diagnostic or a known task is hidden:\n%s", stdout)
	}
	if strings.Contains(stdout, "resume     --task") {
		t.Errorf("a continuation was offered:\n%s", stdout)
	}
}

// D375-W8. --task and --answer keep their canonical locked per-task gate (they
// never consult the inventory listing): with the inventory unlistable, the
// named task's own locked read fails and both are refused as CANNOT ESTABLISH
// before any identity, lineage or session exists. The readable-inventory
// gates are TestP2BW21ResumeTaskIsRefusedBeforeAnyIdentityOrLineage and
// TestP2BW20AnAnswerIsRefusedAndTheQuestionStands.
func TestD375W8TaskAndAnswerKeepTheirCanonicalGates(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260308T000000.000000000Z"
	writeRawSession(t, root, holder,
		event.New(holder, "t-a", event.SourceSystem, event.TaskCreated, "the objective", nil),
		b2a2Deferred(holder, "t-a"))
	r375Unlistable(t, root)
	ledger, sessions := wqRead(t, wqLedger(root, holder)), wqSessions(t, root)
	for _, args := range [][]string{{"--task", "t-a", "--quiet"}, {"--task", "t-a", "--answer", "1", "--quiet"}} {
		code, _, stderr := runResume(t, root, args...)
		if code != exitFailed || !strings.Contains(stderr, wqUnknownText) {
			t.Errorf("%v: not refused as unknown containment: exit %d\n%s", args, code, stderr)
		}
		if strings.Contains(stderr, "not ready") || strings.Contains(stderr, "continues as session") ||
			strings.Contains(stderr, "answered") {
			t.Errorf("%v: went past the gate:\n%s", args, stderr)
		}
	}
	if string(wqRead(t, wqLedger(root, holder))) != string(ledger) {
		t.Error("a refused continuation appended to the history")
	}
	if got := wqSessions(t, root); strings.Join(got, ",") != strings.Join(sessions, ",") {
		t.Errorf("a session was created: %v -> %v", sessions, got)
	}
}

// D375-W9. The listing mutates no candidate identity, workspace, disposition
// or session -- over an unlistable inventory and over a listable one.
func TestD375W9TheListingMutatesNothing(t *testing.T) {
	const holder = "session-20260309T000000.000000000Z"
	unlistable := gitRepoRoot(t)
	writeRawSession(t, unlistable, holder,
		event.New(holder, "t-a", event.SourceSystem, event.TaskCreated, "known work", nil))
	r375Unlistable(t, unlistable)
	before := r375Snapshot(t, unlistable)
	for _, args := range [][]string{{"--list"}, {"--list", "--session", holder}} {
		runResume(t, unlistable, args...)
	}
	r375SameSnapshot(t, "unlistable", before, r375Snapshot(t, unlistable))

	listable := gitRepoRoot(t)
	wqCandidate(t, listable, "t-q", true)
	wqCandidate(t, listable, "t-orphan", false)
	writeRawSession(t, listable, holder,
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "quarantined work", nil),
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	runResume(t, listable, "--list") // the lock objects a read may create exist from here on
	before = r375Snapshot(t, listable)
	for _, args := range [][]string{{"--list"}, {"--list", "--session", holder}} {
		runResume(t, listable, args...)
	}
	r375SameSnapshot(t, "listable", before, r375Snapshot(t, listable))
}

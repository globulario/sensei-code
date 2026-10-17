package main

// RULING-367 §16-§17 (N3). A review-obligation read failure in `resume --list`
// does not hide a separately verified workspace quarantine: the known
// containment obligations and their task ids are still shown, the review
// error is preserved, review is never declared settled and nothing is offered
// for continuation. The command may still end with an error.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

func TestR367N3AReviewReadFailureDoesNotHideContainment(t *testing.T) {
	root := gitRepoRoot(t)
	const holder = "session-20260120T000000.000000000Z"
	wqCandidate(t, root, "t-q", true)
	wqCandidate(t, root, "t-orphan", true)
	writeRawSession(t, root, holder,
		event.New(holder, "t-rev", event.SourceSystem, event.TaskCreated, "work whose review state is unreadable", nil),
		event.New(holder, "t-q", event.SourceSystem, event.TaskCreated, "quarantined work", nil),
		event.New(holder, "t-ok", event.SourceSystem, event.TaskCreated, "good work", nil))
	// The durable review obligation of t-rev cannot be read.
	dir := filepath.Join(root, ".sensei-code", "exchanges")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(map[string]any{
		"task_id": "t-rev", "request_id": "r-bad", "kind": "review", "base": "not-a-sha",
		"candidate_digest": "sha256:" + strings.Repeat("b", 40), "candidate_tree": strings.Repeat("c", 40),
		"review_commit": strings.Repeat("d", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "r-bad.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	record := wqRead(t, wqRecordPath(root, "t-q"))

	_, stdout, stderr := runResume(t, root, "--list")
	if !strings.Contains(stderr, "review obligation of task t-rev could not be established") {
		t.Fatalf("the review error was not preserved:\n%s", stderr)
	}
	for _, task := range []string{"t-q", "t-orphan"} {
		if b := wqBlock(stdout, task); !strings.Contains(b, wqLaneText) || !strings.Contains(b, wqNoSettle) {
			t.Errorf("the review failure hid the workspace quarantine of %s:\nstdout:\n%s\nstderr:\n%s", task, stdout, stderr)
		}
	}
	if strings.Contains(stdout, "resume     --task") || strings.Contains(stdout, "no human-owned question is standing") {
		t.Errorf("a listing that could not read review obligations offered a continuation or declared review settled:\n%s", stdout)
	}
	if string(wqRead(t, wqRecordPath(root, "t-q"))) != string(record) {
		t.Error("the listing changed the candidate record")
	}
}

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A record is regenerable from the log and the overlay, and nothing in it is
// inferred: an absent overlay field is "unrecorded", the human review is
// never derived, and each mechanical field comes from the event that carries it.
func TestARecordIsExtractedFromTheLogAndNeverInferred(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "x-v1", "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	log := strings.Join([]string{
		`task task-1  session s`,
		`{"time":"2026-08-28T01:00:00Z","task_id":"task-1","source":"system","kind":"task.created","summary":"do the thing"}`,
		`{"time":"2026-08-28T01:00:01Z","task_id":"task-1","source":"system","kind":"mode.selected","summary":"governed · submitted unattended"}`,
		`{"time":"2026-08-28T01:00:02Z","task_id":"task-1","source":"system","kind":"status","summary":"derived coverage: 2 anchor(s) over 1 planned file(s); route architectural-authority-granted\n  a.go [mutation confinement]","payload":{"anchors":["a.go [mutation confinement]"],"operational_authority":["a_test.go"]}}`,
		`{"time":"2026-08-28T01:00:03Z","task_id":"task-1","source":"git","kind":"status","summary":"candidate task-1 on b from base 3b6be68abc (clean)"}`,
		`{"time":"2026-08-28T01:00:04Z","task_id":"task-1","source":"reviewer","kind":"review.completed","summary":"ACCEPT","payload":{"decision":"accept","summary":"fine","provenance":{"provider":"codex","candidate_digest":"d1"}}}`,
		`{"time":"2026-08-28T01:00:05Z","task_id":"task-1","source":"system","kind":"workflow.completed","summary":"candidate ready for governed admission"}`,
	}, "\n")
	os.WriteFile(filepath.Join(runs, "E9.log"), []byte(log), 0o644)
	os.WriteFile(filepath.Join(runs, "E9.run"), []byte("START 01:00:00Z base 3b6be68\nEXIT 0 01:00:05Z\n"), 0o644)
	rec, err := extract(filepath.Join(runs, "E9.log"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Encounter != "x-v1/E9" || rec.Task["text"] != "do the thing" || rec.Route[0] != "architectural-authority-granted" {
		t.Fatalf("%+v", rec)
	}
	if rec.Coverage[0]["anchors"] != "2" || rec.Coverage[0]["planned_files"] != "1" {
		t.Fatalf("coverage: %+v", rec.Coverage)
	}
	if rec.Instrument["world"] != "3b6be68" || rec.Instrument["sensei_sha"] != "unrecorded" || rec.HumanReview != "unrecorded" {
		t.Fatalf("instrument/human: %+v %v", rec.Instrument, rec.HumanReview)
	}
	if rec.Review[0]["provider"] != "codex" || rec.Terminal["kind"] != "workflow.completed" || rec.Terminal["exit"] != "0" {
		t.Fatalf("review/terminal: %+v %+v", rec.Review, rec.Terminal)
	}
	// The overlay supplies what no event carries, and only that.
	os.WriteFile(filepath.Join(dir, "x-v1", "corpus-overlay.json"), []byte(`{"E9":{"sensei_sha":"f79f96f9","human_review":["one hole"]}}`), 0o644)
	rec, _ = extract(filepath.Join(runs, "E9.log"))
	if rec.Instrument["sensei_sha"] != "f79f96f9" || rec.Instrument["sensei_code_sha"] != "unrecorded" {
		t.Fatalf("overlay: %+v", rec.Instrument)
	}
	if hr, ok := rec.HumanReview.([]any); !ok || hr[0] != "one hole" {
		t.Fatalf("human review: %v", rec.HumanReview)
	}
}

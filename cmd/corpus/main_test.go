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
		`{"time":"2026-08-28T01:00:02Z","task_id":"task-1","source":"sensei","kind":"status","summary":"graph binding for every agent in this task: domain example.com/m, build (none), via awareness-mcp --awareness-addr localhost:1"}`,
		`{"time":"2026-08-28T01:00:03Z","task_id":"task-1","source":"git","kind":"status","summary":"candidate task-1 on b from base 3b6be68abc (clean)"}`,
		`{"time":"2026-08-28T01:00:03Z","task_id":"task-1","source":"git","kind":"candidate.changed","summary":"candidate diff 1016 bytes · cycle 1"}`,
		`{"time":"2026-08-28T01:00:03Z","task_id":"task-1","source":"system","kind":"validation.run","summary":"v","payload":{"diff_digest":"bcfe2cac","checks":[]}}`,
		`{"time":"2026-08-28T01:00:04Z","task_id":"task-1","source":"sensei","kind":"candidate.audited","summary":"a","payload":{"decision":"pass","digest":"x","graph_commit":"f56f5a305798"}}`,
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
	if rec.Instrument["world"] != "3b6be68" || rec.Instrument["sensei_sha"] != "unrecorded" || rec.ReviewFindings != "unrecorded" || rec.ReviewProvenance != "unrecorded" {
		t.Fatalf("instrument/human: %+v %v", rec.Instrument, rec.ReviewFindings)
	}
	if rec.Review[0]["provider"] != "codex" || rec.Terminal["kind"] != "workflow.completed" || rec.Terminal["exit"] != "0" {
		t.Fatalf("review/terminal: %+v %+v", rec.Review, rec.Terminal)
	}
	if rec.Graph["domain"] != "example.com/m" || rec.Graph["audit_graph_commit"] != "f56f5a305798" || rec.Graph["input_graph_digest"] != "unrecorded" {
		t.Fatalf("graph identity: %+v", rec.Graph)
	}
	if rec.Candidate[0]["bytes"] != "1016" || rec.Candidate[0]["cycle"] != "1" || rec.Candidate[0]["digest"] != "bcfe2cac" {
		t.Fatalf("candidate: %+v", rec.Candidate)
	}
	if len(rec.Derivation) != 1 || rec.Derivation[0]["file"] != "a.go" || rec.Derivation[0]["requirement"] != "mutation confinement" {
		t.Fatalf("derivation: %+v", rec.Derivation)
	}
	// The overlay supplies what no event carries, and only that.
	os.WriteFile(filepath.Join(dir, "x-v1", "corpus-overlay.json"), []byte(`{"E9":{"sensei_sha":"f79f96f9","review_findings":["one hole"],"review_provenance":{"provider":"model","mediated_by":"human"}}}`), 0o644)
	rec, _ = extract(filepath.Join(runs, "E9.log"))
	if rec.Instrument["sensei_sha"] != "f79f96f9" || rec.Instrument["sensei_code_sha"] != "unrecorded" {
		t.Fatalf("overlay: %+v", rec.Instrument)
	}
	if hr, ok := rec.ReviewFindings.([]any); !ok || hr[0] != "one hole" {
		t.Fatalf("review findings: %v", rec.ReviewFindings)
	}
	if rp, ok := rec.ReviewProvenance.(map[string]any); !ok || rp["provider"] != "model" {
		t.Fatalf("review provenance: %v", rec.ReviewProvenance)
	}
}

// The committed corpus is exactly what regeneration from the logs and the
// overlays produces. This is the freshness gate: CI runs go test ./..., so a
// log or overlay changed without regenerating fails here.
func TestCommittedCorpusIsFresh(t *testing.T) {
	if _, err := os.Stat("../../experiments"); err != nil {
		t.Skip("no experiments directory beside this package")
	}
	generated, n, err := generate("../../experiments")
	if err != nil {
		t.Fatalf("regeneration failed: %v", err)
	}
	committed, err := os.ReadFile("../../docs/evidence/corpus/encounters.jsonl")
	if err != nil {
		t.Fatalf("no committed corpus: %v", err)
	}
	if string(committed) != string(generated) {
		t.Fatalf("docs/evidence/corpus/encounters.jsonl is stale (%d encounters regenerated); run `go run ./cmd/corpus`", n)
	}
}

// A log that cannot be read is an error, never a silently skipped record.
func TestAnUnreadableLogFailsTheCorpusClosed(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "y-v1", "runs")
	os.MkdirAll(runs, 0o755)
	os.WriteFile(filepath.Join(runs, "E1.log"), []byte(`{"kind":"task.created","summary":"ok"}`+"\n"), 0o644)
	if _, n, err := generate(dir); err != nil || n != 1 {
		t.Fatalf("a readable log failed: %v", err)
	}
	os.Chmod(filepath.Join(runs, "E1.log"), 0o000)
	defer os.Chmod(filepath.Join(runs, "E1.log"), 0o644)
	if _, _, err := generate(dir); err == nil {
		t.Fatal("an unreadable log was skipped rather than failing the corpus")
	}
}

// #103 review, P1 x3: receipts of another task are not this encounter's;
// a malformed event line fails the corpus rather than vanishing; a run
// nested beneath `runs` is discovered and named by its depth.
func TestReceiptsAreAttributedByTaskMalformedLinesFailAndNestedRunsAreFound(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "z-v1", "runs", "seriesQ")
	os.MkdirAll(runs, 0o755)
	log := strings.Join([]string{
		`task task-9  session s`,
		`{"time":"t","task_id":"task-9","source":"system","kind":"task.created","summary":"do"}`,
		`{"time":"t","task_id":"task-9","source":"system","kind":"workflow.completed","summary":"done"}`,
	}, "\n")
	os.WriteFile(filepath.Join(runs, "Q1.log"), []byte(log), 0o644)
	os.WriteFile(filepath.Join(runs, "Q1.receipts.jsonl"), []byte(
		`{"origin_task":"task-9","closure_round":1,"outcome":"RECORDED","output_candidate_identity":"mine"}`+"\n"+
			`{"origin_task":"task-OTHER","closure_round":1,"outcome":"RECORDED","output_candidate_identity":"theirs"}`+"\n"), 0o644)
	logs, err := discover(dir)
	if err != nil || len(logs) != 1 || !strings.HasSuffix(logs[0], "seriesQ/Q1.log") {
		t.Fatalf("nested run not discovered: %v %v", logs, err)
	}
	rec, err := extract(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.Encounter != "z-v1/seriesQ/Q1" {
		t.Fatalf("encounter name lost its depth: %s", rec.Encounter)
	}
	if len(rec.QuestionOrigin) != 1 || rec.QuestionOrigin[0]["identity"] != "mine" || rec.ReceiptsOtherTasks != 1 {
		t.Fatalf("receipt attribution: %+v other=%d", rec.QuestionOrigin, rec.ReceiptsOtherTasks)
	}
	// The CLI's own terminal message is kept as evidence; a truncated event
	// line is fatal.
	os.WriteFile(filepath.Join(runs, "Q1.log"), []byte(log+"\n"+`sensei-code run: a human-owned decision was reached and no human is present; the question is preserved, not answered`), 0o644)
	rec, err = extract(logs[0])
	if err != nil || len(rec.CLILines) != 1 {
		t.Fatalf("the CLI's terminal line was not kept: %v %v", err, rec.CLILines)
	}
	os.WriteFile(filepath.Join(runs, "Q1.log"), []byte(log+"\n"+`{"time":"t","kind":"candidate.audited","payload":{"decision":"pa`), 0o644)
	if _, err := extract(logs[0]); err == nil || !strings.Contains(err.Error(), "line 4 is not an event") {
		t.Fatalf("a malformed event line did not fail the corpus: %v", err)
	}
	// And a receipts file whose task cannot be established attributes nothing.
	os.WriteFile(filepath.Join(runs, "Q1.log"), []byte(`{"time":"t","kind":"workflow.completed","summary":"done"}`), 0o644)
	rec, _ = extract(logs[0])
	if len(rec.QuestionOrigin) != 0 || rec.ReceiptsOtherTasks != 2 {
		t.Fatalf("receipts attributed without a task id: %+v", rec.QuestionOrigin)
	}
}

// A correction is appended to history and never rewrites the observation.
func TestHistoryIsOverlayOnlyAndAppended(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(runs, "X.log")
	if err := os.WriteFile(log, []byte("task task-1  session s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := extract(log)
	if err != nil {
		t.Fatal(err)
	}
	if rec.History != "unrecorded" {
		t.Fatalf("history without an overlay: %v", rec.History)
	}
	overlay := `{"X": {"merge_provenance": {"merge_executed_by": "not merged"}, "history": [{"recorded": "later", "event": "merged", "supersedes": "merge_provenance.merge_executed_by = 'not merged'"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "corpus-overlay.json"), []byte(overlay), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err = extract(log)
	if err != nil {
		t.Fatal(err)
	}
	if mp, ok := rec.MergeProvenance.(map[string]any); !ok || mp["merge_executed_by"] != "not merged" {
		t.Fatalf("the observation was rewritten: %v", rec.MergeProvenance)
	}
	if h, ok := rec.History.([]any); !ok || len(h) != 1 || h[0].(map[string]any)["event"] != "merged" {
		t.Fatalf("history: %v", rec.History)
	}
}

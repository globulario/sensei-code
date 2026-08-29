package main

import (
	"fmt"
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

// A stream committed as ordered parts is one encounter, reconstructed.
//
// A run log can exceed a tool call's message ceiling, so it is committed as
// C2.log.part-001, .part-002 … The parts are the same bytes in the same
// order: the corpus must read them as the one stream they reconstruct, or
// splitting a file would silently delete an encounter from the record.
func TestASplitStreamIsOneEncounterReconstructedFromItsParts(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	whole := "task task-1  session s\n" + `{"kind":"task.created","task_id":"task-1","summary":"do the thing"}` + "\n"
	// Split MID-LINE, so only a faithful reconstruction parses: two parts
	// read independently would each hold half an event.
	split := len(whole) - 20
	for i, chunk := range []string{whole[:split], whole[split:]} {
		if err := os.WriteFile(filepath.Join(runs, fmt.Sprintf("X.log.part-%03d-of-002", i+1)), []byte(chunk), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || filepath.Base(logs[0]) != "X.log" {
		t.Fatalf("discover = %v; want the one logical stream X.log", logs)
	}
	rec, err := extract(logs[0])
	if err != nil {
		t.Fatal(err)
	}
	if rec.Encounter != filepath.Base(dir)+"/X" {
		t.Fatalf("encounter = %q", rec.Encounter)
	}
	if rec.Task["id"] != "task-1" || rec.Task["text"] != "do the thing" {
		t.Fatalf("the reconstructed stream was not read as one: %+v", rec.Task)
	}
}

// Missing evidence fails closed: a gap in the parts is not a stream.
//
// Glob returns whatever is present, so part-001 + part-003 would concatenate
// into something that parses while silently omitting every event in between
// -- candidate, validation, review -- and a regenerated corpus would treat
// that incomplete evidence as authoritative (#118 review, P1).
func TestAGapInTheSplitStreamFailsClosed(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []int{1, 3} {
		line := fmt.Sprintf(`{"kind":"status","summary":"part %d"}`+"\n", n)
		if err := os.WriteFile(filepath.Join(runs, fmt.Sprintf("X.log.part-%03d", n)), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := discover(dir)
	if err == nil && len(logs) == 1 {
		if _, err = extract(logs[0]); err == nil {
			t.Fatal("a stream missing its middle part was read as complete evidence")
		}
	}
	if err == nil {
		t.Fatal("the gap was not reported")
	}
	if !strings.Contains(err.Error(), "part") {
		t.Fatalf("the refusal must name the parts: %v", err)
	}
}

// A whole stream and parts of it cannot both stand: one encounter, one
// record. Two representations of the same stream is an ambiguity about what
// the evidence IS, not something to silently prefer one side of (#118, P2).
func TestAWholeStreamBesideItsPartsIsRefused(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"kind":"status","summary":"whole"}` + "\n"
	if err := os.WriteFile(filepath.Join(runs, "X.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "X.log.part-001"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	logs, err := discover(dir)
	if err != nil {
		if !strings.Contains(err.Error(), "X.log") {
			t.Fatalf("the refusal must name the stream: %v", err)
		}
		return
	}
	if len(logs) != 1 {
		t.Fatalf("one stream produced %d entries: %v", len(logs), logs)
	}
	if _, err := extract(logs[0]); err == nil {
		t.Fatal("a stream present both whole and split was read without objection")
	}
}

// A stream is found by its literal name, never by pattern.
//
// filepath.Glob reads its argument as a pattern, so a logical stream whose
// name contains metacharacters can match a SIBLING's parts: `A[1].log`
// matches `A1.log.part-001`. The corpus would then emit the sibling's
// evidence twice and omit the stream that actually exists (#118 review).
func TestSplitStreamPathsAreMatchedLiterally(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, summary string) {
		line := fmt.Sprintf(`{"kind":"task.created","task_id":%q,"summary":%q}`+"\n", summary, summary)
		if err := os.WriteFile(filepath.Join(runs, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("A1.log.part-001-of-001", "sibling")
	write("A[1].log.part-001-of-001", "itself")

	logs, err := discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("two streams produced %d entries: %v", len(logs), logs)
	}
	got := map[string]string{}
	for _, l := range logs {
		rec, err := extract(l)
		if err != nil {
			t.Fatalf("%s: %v", l, err)
		}
		got[filepath.Base(l)] = rec.Task["id"].(string)
	}
	if got["A[1].log"] != "itself" || got["A1.log"] != "sibling" {
		t.Fatalf("a stream was reconstructed from another stream's parts: %v", got)
	}
}

// A malformed part name is refused, never ignored.
//
// `X.log.part-01` (a packaging typo) matched neither the strict part
// pattern nor the plain-stream suffixes, so discovery walked past it and
// regeneration emitted NO record for that encounter -- evidence disappearing
// quietly, which is the one direction this must never fail (#118 review).
func TestAMisnumberedPartIsRefusedNotIgnored(t *testing.T) {
	// "X.log.part-" is the zero-length edge of the same property: the
	// sequence missing entirely, found independently by the owner's review
	// and by Codex on f4e86e1.
	for _, name := range []string{"X.log.part-01", "X.log.part-abc", "X.jsonl.part-1", "X.log.part-", "X.jsonl.part-", ".jsonl.part-002"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			runs := filepath.Join(dir, "runs")
			if err := os.MkdirAll(runs, 0o755); err != nil {
				t.Fatal(err)
			}
			line := `{"kind":"task.created","task_id":"task-1","summary":"evidence"}` + "\n"
			if err := os.WriteFile(filepath.Join(runs, name), []byte(line), 0o644); err != nil {
				t.Fatal(err)
			}
			logs, err := discover(dir)
			if err != nil {
				return // refused at discovery is also fail-closed
			}
			if len(logs) == 0 {
				t.Fatal("a stream-like part was ignored, so the encounter vanished from the corpus")
			}
			if _, err := extract(logs[0]); err == nil {
				t.Fatalf("a malformed part name was read as evidence: %v", logs)
			}
		})
	}
}

// A stream with an empty stem is a stream, split or whole.
//
// A file named exactly `.log` is accepted by whole-stream discovery, so
// `.log.part-001` must reconstruct it -- otherwise splitting an otherwise
// discoverable stream erases its evidence. Recognition and discovery now
// share one predicate (isStreamName), so the two cannot disagree again
// (#118 review, round 6).
func TestAStreamWithAnEmptyStemIsStillAStream(t *testing.T) {
	if !isStreamName(".log") || !isStreamName(".jsonl") {
		t.Fatal("whole-stream discovery accepts these names, so part recognition must too")
	}
	if partOf(".log.part-001-of-001") != ".log" || partOf(".jsonl.part-001") != ".jsonl" {
		t.Fatalf("an empty-stem part claimed no stream: %q %q", partOf(".log.part-001-of-001"), partOf(".jsonl.part-001"))
	}
	// Not a stream at all, split or whole.
	if partOf("notes.txt.part-001-of-001") != "" || isStreamName("notes.txt") {
		t.Fatal("a non-stream name claimed to be evidence")
	}
	// And a well-formed empty-stem split reconstructs rather than vanishing.
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"kind":"task.created","task_id":"task-1","summary":"evidence"}` + "\n"
	if err := os.WriteFile(filepath.Join(runs, ".log.part-001-of-001"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	logs, err := discover(dir)
	if err != nil || len(logs) != 1 || filepath.Base(logs[0]) != ".log" {
		t.Fatalf("discover = %v, %v; want the one logical stream .log", logs, err)
	}
	rec, err := extract(logs[0])
	if err != nil || rec.Task["id"] != "task-1" {
		t.Fatalf("the empty-stem stream did not reconstruct: %+v %v", rec.Task, err)
	}
}

// A part belongs to the LONGEST prefix that is itself a valid stream name.
//
// `A.log.part-x.log` is a legitimate stream: it ends in .log, so whole-stream
// discovery accepts it. Split, it is `A.log.part-x.log.part-001` — and a
// left-to-right marker scan stops at the first qualifying prefix, `A.log`,
// which is ALSO a legitimate stream. The parts then fail their sequence
// check (expected `A.log.part-001`) and the real stream gets no encounter:
// fail-closed, but under the wrong identity. Distinct from rounds 4-6, where
// evidence vanished silently; here it enters the machinery and is refused
// for being something it is not. Constructed independently by the owner's
// review and by Codex on 284577a, before the repair.
func TestPartRecognitionChoosesTheLongestStreamIdentity(t *testing.T) {
	for name, want := range map[string]string{
		"A.log.part-x.log.part-001-of-001":     "A.log.part-x.log",
		"X.log.part-run.log.part-001-of-001":   "X.log.part-run.log",
		"A.log.part-001-of-001":                "A.log",
		"a/b/deep.jsonl.part-002-of-002":       "a/b/deep.jsonl",
		"notes.txt.part-001-of-001":            "",
		"X.log.part-run.jsonl.part-003-of-003": "X.log.part-run.jsonl",
	} {
		if got := partOf(name); got != want {
			t.Errorf("partOf(%q) = %q; want %q", name, got, want)
		}
	}

	// End to end: the nested stream reconstructs as itself.
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"kind":"task.created","task_id":"task-1","summary":"nested"}` + "\n"
	if err := os.WriteFile(filepath.Join(runs, "A.log.part-x.log.part-001-of-001"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	logs, err := discover(dir)
	if err != nil || len(logs) != 1 || filepath.Base(logs[0]) != "A.log.part-x.log" {
		t.Fatalf("discover = %v, %v; want the one logical stream A.log.part-x.log", logs, err)
	}
	rec, err := extract(logs[0])
	if err != nil || rec.Task["id"] != "task-1" {
		t.Fatalf("the nested stream did not reconstruct: %+v %v", rec.Task, err)
	}
}

// One identity calculation, used everywhere a part is attributed.
//
// `A.log` and `A.log.part-x.log` are two legitimate streams, and
// `A.log.part-x.log.part-001` is a part of the SECOND. Collecting parts by
// bare prefix attributed it to the first as well, so reading `A.log` --
// present whole, and correctly so -- reported an ambiguous whole-plus-parts
// representation and aborted the whole corpus. partOf is the identity
// calculation; streamParts must use it rather than a prefix test (#118
// review, round 8).
func TestPartsAreAttributedByIdentityNotByPrefix(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, id string) {
		line := fmt.Sprintf(`{"kind":"task.created","task_id":%q,"summary":%q}`+"\n", id, id)
		if err := os.WriteFile(filepath.Join(runs, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("A.log", "whole")                             // a stream, present whole
	write("A.log.part-x.log.part-001-of-001", "nested") // a part of A.log.part-x.log

	logs, err := discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("two streams produced %d entries: %v", len(logs), logs)
	}
	got := map[string]string{}
	for _, l := range logs {
		rec, err := extract(l)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(l), err)
		}
		got[filepath.Base(l)] = rec.Task["id"].(string)
	}
	if got["A.log"] != "whole" || got["A.log.part-x.log"] != "nested" {
		t.Fatalf("parts were attributed by prefix rather than identity: %v", got)
	}
}

// A name is either a stream or a piece of one, never both.
//
// `A.log.part-x.log` ends in .log, so it IS a stream -- and it also contains
// the marker after `A.log`, which is also a stream, so identity resolution
// alone called it a part of `A.log`. Reading `A.log` then reported a false
// whole-plus-parts ambiguity and aborted the corpus, though both files are
// independent evidence. Being a stream settles it: a whole stream is not a
// piece of anything (#118 review, round 9).
func TestAWholeStreamIsNotAPartOfAnother(t *testing.T) {
	if partOf("A.log.part-x.log") != "" || partOf("A.jsonl.part-x.jsonl") != "" {
		t.Fatalf("a whole stream claimed to be a part: %q %q",
			partOf("A.log.part-x.log"), partOf("A.jsonl.part-x.jsonl"))
	}
	// Its own pieces still resolve to it.
	if got := partOf("A.log.part-x.log.part-001-of-001"); got != "A.log.part-x.log" {
		t.Fatalf("partOf(nested part) = %q; want A.log.part-x.log", got)
	}

	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, id string) {
		line := fmt.Sprintf(`{"kind":"task.created","task_id":%q,"summary":%q}`+"\n", id, id)
		if err := os.WriteFile(filepath.Join(runs, name), []byte(line), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("A.log", "first")
	write("A.log.part-x.log", "second")

	logs, err := discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(logs) != 2 {
		t.Fatalf("two whole streams produced %d entries: %v", len(logs), logs)
	}
	got := map[string]string{}
	for _, l := range logs {
		rec, err := extract(l)
		if err != nil {
			t.Fatalf("%s: %v", filepath.Base(l), err)
		}
		got[filepath.Base(l)] = rec.Task["id"].(string)
	}
	if got["A.log"] != "first" || got["A.log.part-x.log"] != "second" {
		t.Fatalf("two independent streams were not read as two: %v", got)
	}
}

// A stream's sidecar is neither a stream nor a piece of one.
//
// `A.log.part-x.log` is an accepted stream, so it may carry the metadata
// files extract already reads: `.run`, `.receipts.jsonl`,
// `.recipes-after.json`. Each of those names contains the marker after
// `A.log`, so part attribution claimed them for that shorter stream --
// inventing an `A.log` encounter whose "part" is a `.run` file, which then
// fails its sequence check and aborts the corpus. A nested stream could
// therefore never carry the sidecars every other stream has (#118, round 10).
func TestASidecarIsNotAPartOfAnotherStream(t *testing.T) {
	// Ownership decides, not a suffix list (round 11 replaced round 10's
	// list): a metadata file belongs to a stream that exists beside it,
	// and `extract` builds those names from the stream WITHOUT its
	// extension -- `A.log.part-x.run`, not `A.log.part-x.log.run`.
	streams := []string{"A.log.part-x.log", "A.log"}
	for _, suffix := range []string{".run", ".receipts.jsonl", ".recipes-after.json", ".graph.metadata.pre.json"} {
		if name := "A.log.part-x" + suffix; !artifactOf(name, streams) {
			t.Errorf("%q is not recognised as an artifact of A.log.part-x.log", name)
		}
	}
	// The stream itself and its real pieces are unaffected.
	if partOf("A.log.part-x.log.part-001-of-001") != "A.log.part-x.log" {
		t.Fatal("the nested stream's own piece stopped resolving")
	}
	if artifactOf("A.log.part-x.log.part-001-of-001", streams) {
		t.Fatal("a real piece was exempted as an artifact")
	}

	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"kind":"task.created","task_id":"nested","summary":"nested"}` + "\n"
	if err := os.WriteFile(filepath.Join(runs, "A.log.part-x.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "A.log.part-x.run"), []byte("START\nEXIT 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs, err := discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(logs) != 1 || filepath.Base(logs[0]) != "A.log.part-x.log" {
		t.Fatalf("discover = %v; want only the nested stream", logs)
	}
	if _, err := extract(logs[0]); err != nil {
		t.Fatalf("a nested stream could not carry its sidecar: %v", err)
	}
}

// Uniqueness must be certified, not assumed: if the directory cannot be
// enumerated, nothing proves the whole stream has no split or malformed
// twin, so the whole file is not accepted on its own (#118, round 10).
func TestAWholeStreamIsRefusedWhenUniquenessCannotBeCertified(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(runs, "X.log")
	if err := os.WriteFile(path, []byte("task task-1  session s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(runs, 0o100); err != nil { // executable, not readable
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(runs, 0o755) })
	if _, err := os.ReadDir(runs); err == nil {
		t.Skip("this environment can list an unreadable directory; the specimen needs enumeration to fail")
	}
	if fh, err := os.Open(path); err != nil {
		t.Skipf("the whole file is not openable either, so the specimen is not the one under test: %v", err)
	} else {
		fh.Close()
	}
	if _, err := openLog(path); err == nil {
		t.Fatal("a whole stream was accepted while its uniqueness could not be certified")
	}
}

// A packaging error must not hide behind a metadata suffix.
//
// `X.log.part-001.run` claims to be a piece of `X.log` AND ends in a
// metadata suffix. Exempting it as a sidecar on the suffix alone made it
// invisible again -- zero encounters, or a coexisting `X.log` accepted as
// uniquely represented -- which is the silent omission rounds 4-6 closed.
// Ownership decides: a metadata file belongs to a stream that exists, and
// nothing between a stream and its sequence may carry a marker (#118, 11a).
func TestAPartShapedNameEndingInAMetadataSuffixIsNotExempt(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"X.log", "X.log.part-001.run"} {
		if err := os.WriteFile(filepath.Join(runs, name), []byte("task task-1  session s\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := discover(dir)
	if err != nil {
		return // refused at discovery is fail-closed
	}
	var failed bool
	for _, l := range logs {
		if _, err := extract(l); err != nil {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("a part-shaped packaging error was ignored and X.log was accepted as unique: %v", logs)
	}
}

// A stream's run artifacts are its own, whatever they are called.
//
// The reader excluded three suffixes it happens to read. The runs this
// repository actually commits also hold `.graph.metadata.pre.json` and
// `.candidate.diff`, and any future artifact would be one more name nobody
// listed -- each of them claimed by a shorter stream when the owning stream's
// name contains a marker. Ownership is the rule, not a list (#118, 11b).
func TestARunArtifactBelongsToItsOwnStream(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"kind":"task.created","task_id":"nested","summary":"nested"}` + "\n"
	if err := os.WriteFile(filepath.Join(runs, "A.log.part-x.log"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, artifact := range []string{"A.log.part-x.graph.metadata.pre.json", "A.log.part-x.candidate.diff", "A.log.part-x.run"} {
		if err := os.WriteFile(filepath.Join(runs, artifact), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	logs, err := discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(logs) != 1 || filepath.Base(logs[0]) != "A.log.part-x.log" {
		t.Fatalf("discover = %v; want only the nested stream", logs)
	}
	if _, err := extract(logs[0]); err != nil {
		t.Fatalf("a nested stream could not carry its run artifacts: %v", err)
	}
}

// A truncated stream fails closed: the parts must declare their total.
//
// Contiguity from 001 proves no HOLE, never that the tail is present.
// Dropping the last part leaves `part-001` alone, which is contiguous and
// parses -- the split is on a line boundary -- so the corpus emitted a
// partial encounter: missing reviews, no terminal kind, and no complaint.
// Every part therefore declares the total (`.part-001-of-002`), so a
// missing tail is a counting failure rather than a silence (#118, 12a).
func TestATruncatedStreamFailsClosed(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	head := "task task-1  session s\n" + `{"kind":"task.created","task_id":"task-1","summary":"begun"}` + "\n"
	tail := `{"kind":"workflow.completed","task_id":"task-1","summary":"ended"}` + "\n"
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(runs, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("X.log.part-001-of-002", head)
	write("X.log.part-002-of-002", tail)

	// Whole: it reconstructs and carries the terminal event.
	logs, err := discover(dir)
	if err != nil || len(logs) != 1 {
		t.Fatalf("discover = %v, %v", logs, err)
	}
	rec, err := extract(logs[0])
	if err != nil || rec.Terminal["kind"] != "workflow.completed" {
		t.Fatalf("the intact stream did not reconstruct: %+v %v", rec.Terminal, err)
	}

	// Truncated: the tail is gone and the reader must say so.
	if err := os.Remove(filepath.Join(runs, "X.log.part-002-of-002")); err != nil {
		t.Fatal(err)
	}
	logs, err = discover(dir)
	if err == nil && len(logs) == 1 {
		_, err = extract(logs[0])
	}
	if err == nil {
		t.Fatal("a truncated stream was read as complete evidence")
	}
	if !strings.Contains(err.Error(), "truncated") {
		t.Fatalf("the refusal must name the counting failure: %v", err)
	}
}

// A stream that exists ONLY as parts still owns its artifacts.
//
// The ownership set was built from whole-stream filenames alone, so a
// split-only stream owned nothing: its `.run` sidecar was claimed by the
// shorter `A.log`, which then aborted expecting a part of itself. Ownership
// is over logical identities, however they are represented (#118, 12b).
func TestASplitOnlyStreamOwnsItsArtifacts(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"kind":"task.created","task_id":"nested","summary":"nested"}` + "\n"
	if err := os.WriteFile(filepath.Join(runs, "A.log.part-x.log.part-001-of-001"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "A.log.part-x.run"), []byte("START\nEXIT 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs, err := discover(dir)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(logs) != 1 || filepath.Base(logs[0]) != "A.log.part-x.log" {
		t.Fatalf("discover = %v; want only the split-only nested stream", logs)
	}
	rec, err := extract(logs[0])
	if err != nil || rec.Task["id"] != "nested" {
		t.Fatalf("the split-only stream did not reconstruct with its artifacts: %+v %v", rec.Task, err)
	}
}

// The receipts suffix is not a bypass either.
//
// `.receipts.jsonl` kept the one unconditional suffix skip that ownership
// replaced everywhere else, so `X.log.part-001-of-001.receipts.jsonl` --
// a name claiming to be a piece -- was dropped before it could claim, and
// generation succeeded with zero encounters. A receipts file is exempt
// because it is OWNED by a stream that exists, never because of how it is
// spelled (#118, round 14).
func TestAReceiptsSuffixDoesNotBypassPartClaims(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "X.log.part-001-of-001.receipts.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs, err := discover(dir)
	if err == nil && len(logs) == 1 {
		_, err = extract(logs[0])
	}
	if err == nil {
		t.Fatalf("a part-shaped receipts name was dropped and the corpus reported %d encounter(s)", len(logs))
	}

	// A real receipts file, owned by a stream that exists, stays exempt --
	// and is still read as the stream's receipts rather than as evidence.
	dir2 := t.TempDir()
	runs2 := filepath.Join(dir2, "runs")
	if err := os.MkdirAll(runs2, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs2, "Y.log"), []byte("task task-1  session s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs2, "Y.receipts.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logs2, err := discover(dir2)
	if err != nil || len(logs2) != 1 || filepath.Base(logs2[0]) != "Y.log" {
		t.Fatalf("discover = %v, %v; want only Y.log", logs2, err)
	}
}

// The world a run pinned is read from the stamp the run actually wrote.
//
// A run refused before any candidate exists emits no `from base <sha>`
// line, so its `.run` stamp is the only record of the world it was pinned
// to. The parser recognised only a SHA appended to START, so C1 -- refused
// at routing, which is the whole point of that encounter -- was published
// with `world: unrecorded` while the SHA sat in the file (#118, round 15).
func TestTheSubjectWorldIsReadFromTheRunStamp(t *testing.T) {
	dir := t.TempDir()
	runs := filepath.Join(dir, "runs")
	if err := os.MkdirAll(runs, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runs, "R.log"), []byte("task task-1  session s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := "START 2026-08-29T00:32:39Z\n" +
		"governor commit (source) f01592b0f0828605ed254047fc064f41dacc78f2\n" +
		"subject HEAD abcdef1234567890abcdef1234567890abcdef12\n" +
		"EXIT 1 2026-08-29T00:32:46Z\n"
	if err := os.WriteFile(filepath.Join(runs, "R.run"), []byte(stamp), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err := extract(filepath.Join(runs, "R.log"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Instrument["world"] != "abcdef1234567890abcdef1234567890abcdef12" {
		t.Fatalf("world = %v; the stamp recorded it and the record must carry it", rec.Instrument["world"])
	}
	if rec.Terminal["start"] != "2026-08-29T00:32:39Z" || rec.Terminal["exit"] != "1" {
		t.Fatalf("the stamp's other fields stopped being read: %+v", rec.Terminal)
	}
	// The log's own `from base` line still wins where it exists: it is the
	// world the run itself reported, not the harness's stamp of it.
	if err := os.WriteFile(filepath.Join(runs, "R.log"),
		[]byte("task task-1  session s\n"+`{"kind":"status","summary":"candidate task-1 on b from base 1111111111111111111111111111111111111111 (clean)"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec, err = extract(filepath.Join(runs, "R.log"))
	if err != nil {
		t.Fatal(err)
	}
	if rec.Instrument["world"] != "1111111111111111111111111111111111111111" {
		t.Fatalf("world = %v; the run's own report must win over the harness stamp", rec.Instrument["world"])
	}
}

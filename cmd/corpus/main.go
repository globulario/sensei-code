// corpus turns Phase B's run logs into the evidence corpus, one record per
// encounter. It reads what the run wrote and an optional overlay; it infers
// nothing from prose. See docs/evidence/corpus/SCHEMA.md.
package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type event struct {
	Time    string          `json:"time"`
	TaskID  string          `json:"task_id"`
	Source  string          `json:"source"`
	Kind    string          `json:"kind"`
	Summary string          `json:"summary"`
	Payload json.RawMessage `json:"payload"`
}

type record struct {
	Encounter string `json:"encounter"`
	SourceLog string `json:"source_log"`
	// ReceiptsOtherTasks counts receipt lines beside this log that belong to
	// another task and were therefore NOT attributed to this encounter.
	ReceiptsOtherTasks int `json:"receipts_other_tasks"`
	// CLILines are the CLI\'s own terminal messages found in the stream.
	CLILines          []string         `json:"cli_lines,omitempty"`
	Instrument        map[string]any   `json:"instrument"`
	Graph             map[string]any   `json:"graph"`
	Task              map[string]any   `json:"task"`
	QuestionOrigin    []map[string]any `json:"question_origin"`
	RecipesAtStart    []map[string]any `json:"recipes_at_start"`
	RecipesAfter      []map[string]any `json:"recipes_after"`
	Derivation        []map[string]any `json:"derivation"`
	Coverage          []map[string]any `json:"coverage"`
	GapIdentity       []map[string]any `json:"gap_identity"`
	Route             []string         `json:"route"`
	AuthorityToWorker map[string]any   `json:"authority_to_worker"`
	Candidate         []map[string]any `json:"candidate"`
	Validation        []map[string]any `json:"validation"`
	Audit             []map[string]any `json:"audit"`
	Review            []map[string]any `json:"review"`
	// ReviewFindings are the findings of the independent review of the
	// evidence or repair, and ReviewProvenance says who produced them and who
	// mediated and merged. Both come only from the overlay. The field used to
	// be called human_review. Neither that name nor the first correction
	// ("a model reviewed, a human mediated") was right: the reasoning was
	// GPT-5.6 Sol's, Codex reviewed separately, the GitHub account that
	// executed it belongs to the project owner, and the owner is the ultimate
	// authority. Actors are named, never collapsed into an account.
	ReviewFindings   any `json:"review_findings"`
	ReviewProvenance any `json:"review_provenance"`
	// MergeProvenance says who executed the merge of the evidence or repair
	// and under whose account and authority. Overlay only.
	MergeProvenance any `json:"merge_provenance"`
	// History is the append-only list of corrections and later events about
	// this record. A correction never rewrites review_findings,
	// review_provenance or merge_provenance -- those stay the observation as
	// first committed -- it is appended here naming what it supersedes.
	// Overlay only. Declared because the SCHEMA promised "appended, never
	// rewritten" with no field to hold an appended correction (#114 review).
	History  any            `json:"history"`
	Terminal map[string]any `json:"terminal"`
	Note     any            `json:"note,omitempty"`
}

var (
	coverageLine  = regexp.MustCompile(`^derived coverage: (\d+) anchor\(s\) over (\d+) planned file\(s\); route ([a-z-]+)`)
	baseLine      = regexp.MustCompile(`from base ([0-9a-f]+)`)
	runStamp      = regexp.MustCompile(`(?m)^START (\S+)(?: base (\S+))?|^EXIT (\d+) (\S+)`)
	bindingLine   = regexp.MustCompile(`^graph binding for every agent in this task: domain (\S+), build (\S+), via (.+)$`)
	candidateLine = regexp.MustCompile(`^candidate diff (\d+) bytes · cycle (\d+)`)
	anchorLine    = regexp.MustCompile(`^(\S+) \[(.+)\]$`)
	// headerLine is the one non-JSON line a run writes: `sensei-code run`
	// prints "task <id>  session <id>" before the event stream. Nothing else
	// is allowed to be non-JSON.
	headerLine = regexp.MustCompile(`^task \S+\s+session \S+\s*$`)
	// cliLine is the other thing a run writes outside the event stream: the
	// CLI's own terminal message on stderr, merged into the log by the
	// runner's 2>&1. It is evidence (the exit's stated reason) and is kept.
	cliLine = regexp.MustCompile(`^sensei-code (run|observe|audit-repair): `)
)

func main() {
	root := flag.String("experiments", "experiments", "experiments directory")
	out := flag.String("out", "docs/evidence/corpus/encounters.jsonl", "output JSONL")
	check := flag.Bool("check", false, "regenerate in memory and fail unless the committed output is byte-identical")
	flag.Parse()
	generated, n, err := generate(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
	if *check {
		committed, err := os.ReadFile(*out)
		if err != nil {
			fmt.Fprintln(os.Stderr, "corpus: cannot read committed corpus:", err)
			os.Exit(1)
		}
		if !bytes.Equal(committed, generated) {
			fmt.Fprintf(os.Stderr, "corpus: %s is stale: regeneration from logs and overlays differs; run `go run ./cmd/corpus`\n", *out)
			os.Exit(1)
		}
		fmt.Printf("corpus: %s is fresh (%d encounter(s))\n", *out, n)
		return
	}
	if err := os.WriteFile(*out, generated, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "corpus:", err)
		os.Exit(1)
	}
	fmt.Printf("corpus: %d encounter(s) -> %s\n", n, *out)
}

// generate extracts every encounter under root. Any log that cannot be
// read, parsed, or encoded is an error, never a skipped record: a corpus that
// silently omits an encounter misreports the campaign.
func generate(root string) ([]byte, int, error) {
	logs, err := discover(root)
	if err != nil {
		return nil, 0, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	for _, log := range logs {
		rec, err := extract(log)
		if err != nil {
			return nil, 0, fmt.Errorf("%s: %w", log, err)
		}
		// The record names its source relative to the experiments root, so
		// the corpus is identical however the root was reached.
		if rel, err := filepath.Rel(root, log); err == nil {
			rec.SourceLog = filepath.ToSlash(filepath.Join("experiments", rel))
		}
		if err := enc.Encode(rec); err != nil {
			return nil, 0, fmt.Errorf("%s: encode: %w", log, err)
		}
	}
	return buf.Bytes(), len(logs), nil
}

func extract(log string) (record, error) {
	// The experiment is the first path segment beneath the experiments root;
	// the run is the stream's path beneath its `runs` directory, so a nested
	// run keeps its depth in its name.
	exp, run := encounterName(log)
	rec := record{Encounter: exp + "/" + run, SourceLog: log,
		Instrument: map[string]any{"sensei_sha": "unrecorded", "sensei_code_sha": "unrecorded", "fixture": "unrecorded", "world": "unrecorded"},
		Graph:      map[string]any{"domain": "unrecorded", "build": "unrecorded", "address": "unrecorded", "audit_graph_commit": "unrecorded", "input_graph_digest": "unrecorded", "authority": "unrecorded"},
		Task:       map[string]any{}, AuthorityToWorker: map[string]any{}, Terminal: map[string]any{}, ReviewFindings: "unrecorded", ReviewProvenance: "unrecorded", MergeProvenance: "unrecorded", History: "unrecorded"}
	fh, err := openLog(log)
	if err != nil {
		return rec, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var first, last string
	lineNo := 0
	for sc.Scan() {
		lineNo++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || headerLine.MatchString(raw) {
			continue
		}
		if cliLine.MatchString(raw) {
			rec.CLILines = append(rec.CLILines, raw)
			continue
		}
		var e event
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			// A line that is neither the header nor an event is evidence
			// that cannot be read. Skipping it would let a truncated
			// validation, audit or terminal event vanish silently.
			return rec, fmt.Errorf("line %d is not an event: %v", lineNo, err)
		}
		if e.Kind == "" {
			return rec, fmt.Errorf("line %d is an event with no kind", lineNo)
		}
		if first == "" {
			first = e.Time
		}
		last = e.Time
		var p map[string]any
		_ = json.Unmarshal(e.Payload, &p)
		switch e.Kind {
		case "task.created":
			rec.Task["id"], rec.Task["text"] = e.TaskID, e.Summary
		case "mode.selected":
			rec.Task["provenance"] = e.Summary
		case "plan.proposed":
			plan := map[string]any{"files": p["files"], "mode": p["mode"], "plan_source": p["plan_source"], "plan_digest": p["plan_digest"], "prospective_surfaces": p["prospective_surfaces"]}
			if cs, ok := p["claims"].([]any); ok {
				var srcs []any
				for _, c := range cs {
					if m, ok := c.(map[string]any); ok {
						srcs = append(srcs, m["source"])
					}
				}
				plan["claim_sources"] = srcs
			}
			rec.Task["plan"] = plan
		case "sensei.result":
			if ga, ok := p["graph_authority"]; ok {
				rec.Graph["authority"] = ga
			} else if a, ok := p["authority"]; ok && rec.Graph["authority"] == "unrecorded" {
				rec.Graph["authority"] = a
			}
		case "status":
			if m := bindingLine.FindStringSubmatch(e.Summary); m != nil {
				rec.Graph["domain"], rec.Graph["build"], rec.Graph["address"] = m[1], m[2], m[3]
			}
			if m := coverageLine.FindStringSubmatch(e.Summary); m != nil {
				rec.Coverage = append(rec.Coverage, map[string]any{"anchors": m[1], "planned_files": m[2], "route": m[3], "anchor_lines": p["anchors"], "operational_authority": p["operational_authority"]})
				rec.Route = append(rec.Route, m[3])
				if lines, ok := p["anchors"].([]any); ok {
					for _, l := range lines {
						if am := anchorLine.FindStringSubmatch(fmt.Sprint(l)); am != nil {
							rec.Derivation = append(rec.Derivation, map[string]any{"round": len(rec.Coverage), "file": am[1], "requirement": am[2], "outcome": "DERIVED"})
						}
					}
				}
			}
			if g, ok := p["gap"]; ok {
				rec.GapIdentity = append(rec.GapIdentity, map[string]any{"receipt": g, "identity": p["gap_identity"], "condition": e.Summary})
			}
			if m := baseLine.FindStringSubmatch(e.Summary); m != nil && strings.Contains(e.Summary, "candidate ") {
				rec.Instrument["world"] = m[1]
			}
		case "prospective.granted":
			rec.AuthorityToWorker["prospective"] = p
		case "testedit.granted":
			rec.AuthorityToWorker["test_edit"] = p
		case "candidate.changed":
			c := map[string]any{"time": e.Time, "bytes": "unrecorded", "cycle": "unrecorded", "digest": "unrecorded"}
			if m := candidateLine.FindStringSubmatch(e.Summary); m != nil {
				c["bytes"], c["cycle"] = m[1], m[2]
			}
			rec.Candidate = append(rec.Candidate, c)
		case "validation.run":
			rec.Validation = append(rec.Validation, map[string]any{"diff_digest": p["diff_digest"], "checks": p["checks"]})
			// The validation binds the digest to the candidate that was
			// just produced; the candidate.changed event carries none.
			if n := len(rec.Candidate); n != 0 && rec.Candidate[n-1]["digest"] == "unrecorded" {
				rec.Candidate[n-1]["digest"] = p["diff_digest"]
			}
		case "candidate.audited":
			rec.Audit = append(rec.Audit, map[string]any{"decision": p["decision"], "digest": p["digest"], "findings": p["findings"], "reason_codes": p["reason_codes"]})
			if gc, ok := p["graph_commit"]; ok {
				rec.Graph["audit_graph_commit"] = gc
			}
		case "review.completed":
			r := map[string]any{"decision": p["decision"], "summary": p["summary"], "findings": p["findings"]}
			if prov, ok := p["provenance"].(map[string]any); ok {
				r["provider"], r["candidate_digest"] = prov["provider"], prov["candidate_digest"]
			}
			rec.Review = append(rec.Review, r)
		case "authority.required":
			rec.Terminal["question"] = e.Summary
		case "workflow.completed", "workflow.failed", "workflow.stopped", "workflow.awaiting_authority":
			rec.Terminal["kind"], rec.Terminal["summary"] = e.Kind, e.Summary
		}
	}
	if err := sc.Err(); err != nil {
		return rec, fmt.Errorf("read: %w", err)
	}
	rec.Terminal["first_event"], rec.Terminal["last_event"] = first, last
	base := strings.TrimSuffix(strings.TrimSuffix(log, ".log"), ".jsonl")
	if b, err := os.ReadFile(base + ".run"); err == nil {
		for _, m := range runStamp.FindAllStringSubmatch(string(b), -1) {
			if m[1] != "" {
				rec.Terminal["start"] = m[1]
				if m[2] != "" {
					rec.Instrument["world"] = m[2]
				}
			}
			if m[3] != "" {
				rec.Terminal["exit"], rec.Terminal["end"] = m[3], m[4]
			}
		}
	}
	if b, err := os.ReadFile(base + ".receipts.jsonl"); err == nil {
		taskID, _ := rec.Task["id"].(string)
		for _, line := range strings.Split(string(b), "\n") {
			var r map[string]any
			if json.Unmarshal([]byte(line), &r) == nil && r["outcome"] != nil {
				// A receipt is this encounter's only if it names this task.
				// The receipts file beside a log can hold rounds of another
				// run that shared the fixture; those are counted, not merged.
				if origin, _ := r["origin_task"].(string); taskID == "" || origin != taskID {
					rec.ReceiptsOtherTasks++
					continue
				}
				rec.QuestionOrigin = append(rec.QuestionOrigin, map[string]any{"round": r["closure_round"], "outcome": r["outcome"], "identity": r["output_candidate_identity"], "gap": r["origin_gap"], "region": r["region"]})
				if d, ok := r["input_graph_digest"]; ok && rec.Graph["input_graph_digest"] == "unrecorded" {
					rec.Graph["input_graph_digest"] = d
				}
			}
		}
	}
	if b, err := os.ReadFile(base + ".recipes-after.json"); err == nil {
		var doc struct {
			Recipes []map[string]any `json:"recipes"`
		}
		if json.Unmarshal(b, &doc) == nil {
			// Recipes at start are those after the run minus the ones this
			// run's own receipts recorded -- by identity, which the receipt
			// carries and the recipe's provenance names.
			recorded := map[string]bool{}
			for _, q := range rec.QuestionOrigin {
				if q["outcome"] == "RECORDED" {
					recorded[fmt.Sprint(q["identity"])] = true
				}
			}
			for _, r := range doc.Recipes {
				entry := map[string]any{"kind": r["kind"], "dir": r["dir"], "type": r["type"], "field": r["field"], "command": r["command"], "owner": r["owner"], "search_paths": r["search_paths"]}
				rec.RecipesAfter = append(rec.RecipesAfter, entry)
				if !recorded[recipeIdentity(r)] {
					rec.RecipesAtStart = append(rec.RecipesAtStart, entry)
				}
			}
		}
	}
	// The overlay belongs to the experiment, however deep the stream sits
	// beneath its runs/ directory.
	if b, err := os.ReadFile(filepath.Join(experimentDir(log), "corpus-overlay.json")); err == nil {
		var overlay map[string]map[string]any
		if json.Unmarshal(b, &overlay) == nil {
			if o, ok := overlay[run]; ok {
				for _, k := range []string{"sensei_sha", "sensei_code_sha", "fixture"} {
					if v, ok := o[k]; ok {
						rec.Instrument[k] = v
					}
				}
				if v, ok := o["review_findings"]; ok {
					rec.ReviewFindings = v
				}
				if v, ok := o["review_provenance"]; ok {
					rec.ReviewProvenance = v
				}
				if v, ok := o["merge_provenance"]; ok {
					rec.MergeProvenance = v
				}
				if v, ok := o["history"]; ok {
					rec.History = v
				}
				if v, ok := o["note"]; ok {
					rec.Note = v
				}
			}
		}
	}
	return rec, nil
}

// discover walks every experiment for event streams at any depth beneath a
// `runs` directory. The rule is explicit so an omission is a decision and
// not an accident of directory shape:
//
//   - included: any file under a directory named `runs`, at any depth, with
//     suffix .log (trimmed stream) or .jsonl (untrimmed stream);
//   - excluded: *.receipts.jsonl (receipts, read beside their stream), and
//     anything outside a `runs` directory.
//
// isStreamName is the ONE answer to "is this the name of an evidence
// stream", used by discovery and by part recognition alike.
//
// The two used to answer it differently -- discovery by suffix, parts by a
// regexp requiring a non-empty stem and a non-empty sequence -- and every
// disagreement between them was a way for evidence to disappear: a
// misnumbered part, an empty sequence, an empty stem. Each was found and
// fixed separately (#118 rounds 4, 5, 6) because the asymmetry, not any one
// spelling, was the defect. There is now one predicate, so a name is a
// stream in both places or in neither.
func isStreamName(name string) bool {
	if strings.HasSuffix(name, ".receipts.jsonl") {
		return false
	}
	return strings.HasSuffix(name, ".log") || strings.HasSuffix(name, ".jsonl")
}

// partMarker separates a stream from the sequence of one of its pieces.
const partMarker = ".part-"

// partOf returns the logical stream a name claims to be a piece of, or "".
//
// Anything shaped like a piece of a stream CLAIMS one, whatever follows the
// marker: the claim is what brings the file into the evidence machinery,
// where streamParts proves the strict sequence or refuses it. Recognition is
// permissive on purpose; acceptance is strict.
//
// The rule for WHICH stream, stated semantically rather than as this scan:
//
//	a part belongs to the LONGEST prefix that is itself a valid
//	evidence-stream name.
//
// More than one prefix can qualify. `A.log.part-x.log` is a legitimate
// stream -- it ends in .log -- and split it is `A.log.part-x.log.part-001`,
// whose first qualifying prefix is the *other* legitimate stream `A.log`.
// Taking the first match claimed the wrong identity: the parts then failed
// their sequence check and the real stream got no encounter at all. That is
// not the vanishing of rounds 4-6; the evidence arrives and is refused for
// being something it is not. Constructed independently, before the repair,
// by the owner reading this scan and by Codex reading the diff.
func partOf(name string) string {
	// A name is either a stream or a piece of one, never both. Both
	// `A.log` and `A.log.part-x.log` are streams -- the second ends in .log
	// -- and the second also contains the marker after the first, so
	// identity resolution alone called it a piece of `A.log`: reading that
	// stream then reported a false whole-plus-parts ambiguity and aborted
	// the corpus, though the two files are independent evidence. Being a
	// stream settles it (#118 review, round 9).
	if isStreamName(name) {
		return ""
	}
	logical := ""
	for i := 0; i+len(partMarker) <= len(name); i++ {
		if name[i:i+len(partMarker)] == partMarker && isStreamName(name[:i]) {
			logical = name[:i]
		}
	}
	return logical
}

// streamParts returns the ordered parts of a stream, refusing anything that
// is not the whole of it.
//
// The refusals are the point. The directory is enumerated and names compared
// literally -- globbing would read a stream's own name as a PATTERN, so
// `A[1].log` matched the sibling `A1.log.part-001`. The sequence must start
// at 001 and be contiguous, because part-001 beside part-003 would
// concatenate into something that PARSES while silently omitting every event
// between them, and a regenerated corpus would carry that as authoritative.
func streamParts(path string) ([]string, error) {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	// Attributed by IDENTITY, not by prefix. `A.log.part-x.log.part-001` is
	// a part of `A.log.part-x.log`, and starts with `A.log` + the marker: a
	// prefix test handed it to `A.log` as well, so reading that stream --
	// present whole, and correctly so -- reported an ambiguous
	// whole-plus-parts representation and aborted the corpus. partOf is the
	// one identity calculation; everything that attributes a part uses it.
	logical := filepath.Base(path)
	var parts []string
	for _, e := range entries {
		if !e.IsDir() && partOf(e.Name()) == logical {
			parts = append(parts, filepath.Join(dir, e.Name()))
		}
	}
	if len(parts) == 0 {
		return nil, nil
	}
	sort.Strings(parts)
	for i, p := range parts {
		want := fmt.Sprintf("%s%s%03d", filepath.Base(path), partMarker, i+1)
		if filepath.Base(p) != want {
			return parts, fmt.Errorf("%s: the parts are not a whole stream: expected %s, found %s (%d part(s) present)",
				path, want, filepath.Base(p), len(parts))
		}
	}
	return parts, nil
}

// openLog opens a stream, reconstructing it from its ordered parts when the
// stream itself is not present.
func openLog(path string) (io.ReadCloser, error) {
	parts, perr := streamParts(path)
	whole, err := os.Open(path)
	switch {
	case err == nil && len(parts) != 0:
		whole.Close()
		return nil, fmt.Errorf("%s: the stream is present both whole and as %d part(s); one encounter has one representation", path, len(parts))
	case err == nil:
		return whole, nil
	case !os.IsNotExist(err):
		return nil, err
	case perr != nil:
		return nil, perr
	case len(parts) == 0:
		return nil, fmt.Errorf("%s: neither the stream nor any part of it is present", path)
	}
	readers := make([]io.Reader, 0, len(parts))
	closers := make([]io.Closer, 0, len(parts))
	for _, p := range parts {
		fh, err := os.Open(p)
		if err != nil {
			for _, c := range closers {
				c.Close()
			}
			return nil, err
		}
		readers = append(readers, fh)
		closers = append(closers, fh)
	}
	return partReader{Reader: io.MultiReader(readers...), closers: closers}, nil
}

type partReader struct {
	io.Reader
	closers []io.Closer
}

func (p partReader) Close() error {
	var err error
	for _, c := range p.closers {
		if e := c.Close(); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func discover(root string) ([]string, error) {
	var logs []string
	seen := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		parts := strings.Split(filepath.ToSlash(rel), "/")
		underRuns := false
		for _, seg := range parts[:len(parts)-1] {
			if seg == "runs" {
				underRuns = true
			}
		}
		if !underRuns {
			return nil
		}
		name := d.Name()
		switch {
		case strings.HasSuffix(name, ".receipts.jsonl"):
			return nil
		case isStreamName(name):
			// Logical, so a stream present both whole and split is one entry
			// and openLog refuses it once rather than producing two records.
			if !seen[path] {
				seen[path] = true
				logs = append(logs, path)
			}
		case partOf(name) != "":
			// A stream too large to transit a tool call is committed as
			// ordered parts. It is ONE encounter: the parts are the same
			// bytes in the same order, and the logical name is the stream
			// they reconstruct, so a split changes the packaging and never
			// the record.
			logical := filepath.Join(filepath.Dir(path), partOf(name))
			if !seen[logical] {
				seen[logical] = true
				logs = append(logs, logical)
			}
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("discover: %w", err)
	}
	sort.Strings(logs)
	return logs, nil
}

// recipeIdentity mirrors derived.Recipe.Identity for the three families, so a
// receipt's output_candidate_identity can be matched against a recipe.
func recipeIdentity(r map[string]any) string {
	str := func(k string) string { return strings.TrimSpace(fmt.Sprint(r[k])) }
	paths := []string{}
	if sp, ok := r["search_paths"].([]any); ok {
		for _, p := range sp {
			paths = append(paths, strings.Trim(fmt.Sprint(p), "/"))
		}
	}
	sort.Strings(paths)
	switch r["kind"] {
	case "command_invocation_confined_to":
		return strings.ToLower(fmt.Sprintf("%s|%s|%s|%s", str("kind"), str("command"), strings.Trim(str("owner"), "/"), strings.Join(paths, ",")))
	case "state_mutation_confined_to_owner":
		return strings.ToLower(fmt.Sprintf("%s|%s|%s|%s|%s", str("kind"), strings.Trim(str("dir"), "/"), str("type"), str("field"), strings.Join(paths, ",")))
	default:
		return strings.ToLower(fmt.Sprintf("%s|%s|%s|%s|%s", str("kind"), strings.Trim(str("dir"), "/"), str("type"), str("field"), str("lock")))
	}
}

// encounterName splits a stream path into experiment and run: the run keeps
// any depth beneath `runs` so nested streams do not collide.
func encounterName(log string) (exp, run string) {
	parts := strings.Split(filepath.ToSlash(log), "/")
	for i, seg := range parts {
		if seg == "runs" && i > 0 && i < len(parts)-1 {
			exp = parts[i-1]
			run = strings.Join(parts[i+1:], "/")
			break
		}
	}
	if exp == "" {
		exp = filepath.Base(filepath.Dir(filepath.Dir(log)))
		run = filepath.Base(log)
	}
	run = strings.TrimSuffix(strings.TrimSuffix(run, ".log"), ".jsonl")
	return exp, run
}

// experimentDir is the experiment directory that owns a stream: the parent
// of the first `runs` segment on its path.
func experimentDir(log string) string {
	dir := log
	for {
		parent := filepath.Dir(dir)
		if parent == dir {
			return filepath.Dir(filepath.Dir(log))
		}
		if filepath.Base(dir) == "runs" {
			return parent
		}
		dir = parent
	}
}

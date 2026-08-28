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
	Encounter         string           `json:"encounter"`
	SourceLog         string           `json:"source_log"`
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
	HumanReview       any              `json:"human_review"`
	Terminal          map[string]any   `json:"terminal"`
	Note              any              `json:"note,omitempty"`
}

var (
	coverageLine  = regexp.MustCompile(`^derived coverage: (\d+) anchor\(s\) over (\d+) planned file\(s\); route ([a-z-]+)`)
	baseLine      = regexp.MustCompile(`from base ([0-9a-f]+)`)
	runStamp      = regexp.MustCompile(`(?m)^START (\S+)(?: base (\S+))?|^EXIT (\d+) (\S+)`)
	bindingLine   = regexp.MustCompile(`^graph binding for every agent in this task: domain (\S+), build (\S+), via (.+)$`)
	candidateLine = regexp.MustCompile(`^candidate diff (\d+) bytes · cycle (\d+)`)
	anchorLine    = regexp.MustCompile(`^(\S+) \[(.+)\]$`)
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
	logs, _ := filepath.Glob(filepath.Join(root, "*", "runs", "*.log"))
	// Some early encounters were preserved as untrimmed .jsonl event streams
	// rather than trimmed .log files; they are encounters all the same.
	// Receipt files share the suffix and are not event streams.
	streams, _ := filepath.Glob(filepath.Join(root, "*", "runs", "*.jsonl"))
	for _, s := range streams {
		if !strings.HasSuffix(s, ".receipts.jsonl") {
			logs = append(logs, s)
		}
	}
	sort.Strings(logs)
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
	exp := filepath.Base(filepath.Dir(filepath.Dir(log)))
	run := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(log), ".log"), ".jsonl")
	rec := record{Encounter: exp + "/" + run, SourceLog: log,
		Instrument: map[string]any{"sensei_sha": "unrecorded", "sensei_code_sha": "unrecorded", "fixture": "unrecorded", "world": "unrecorded"},
		Graph:      map[string]any{"domain": "unrecorded", "build": "unrecorded", "address": "unrecorded", "audit_graph_commit": "unrecorded", "input_graph_digest": "unrecorded", "authority": "unrecorded"},
		Task:       map[string]any{}, AuthorityToWorker: map[string]any{}, Terminal: map[string]any{}, HumanReview: "unrecorded"}
	fh, err := os.Open(log)
	if err != nil {
		return rec, err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	var first, last string
	for sc.Scan() {
		var e event
		if json.Unmarshal(sc.Bytes(), &e) != nil || e.Kind == "" {
			continue
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
		for _, line := range strings.Split(string(b), "\n") {
			var r map[string]any
			if json.Unmarshal([]byte(line), &r) == nil && r["outcome"] != nil {
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
	if b, err := os.ReadFile(filepath.Join(filepath.Dir(filepath.Dir(log)), "corpus-overlay.json")); err == nil {
		var overlay map[string]map[string]any
		if json.Unmarshal(b, &overlay) == nil {
			if o, ok := overlay[run]; ok {
				for _, k := range []string{"sensei_sha", "sensei_code_sha", "fixture"} {
					if v, ok := o[k]; ok {
						rec.Instrument[k] = v
					}
				}
				if v, ok := o["human_review"]; ok {
					rec.HumanReview = v
				}
				if v, ok := o["note"]; ok {
					rec.Note = v
				}
			}
		}
	}
	return rec, nil
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

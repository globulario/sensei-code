// corpus turns Phase B's run logs into the evidence corpus, one record per
// encounter. It reads what the run wrote and an optional overlay; it infers
// nothing from prose. See docs/evidence/corpus/SCHEMA.md.
package main

import (
	"bufio"
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
	Task              map[string]any   `json:"task"`
	QuestionOrigin    []map[string]any `json:"question_origin"`
	RecipesAfter      []map[string]any `json:"recipes_after"`
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
	coverageLine = regexp.MustCompile(`^derived coverage: (\d+) anchor\(s\) over (\d+) planned file\(s\); route ([a-z-]+)`)
	baseLine     = regexp.MustCompile(`from base ([0-9a-f]+)`)
	runStamp     = regexp.MustCompile(`(?m)^START (\S+)(?: base (\S+))?|^EXIT (\d+) (\S+)`)
)

func main() {
	root := flag.String("experiments", "experiments", "experiments directory")
	out := flag.String("out", "docs/evidence/corpus/encounters.jsonl", "output JSONL")
	flag.Parse()
	logs, _ := filepath.Glob(filepath.Join(*root, "*", "runs", "*.log"))
	// Some early encounters were preserved as untrimmed .jsonl event streams
	// rather than trimmed .log files; they are encounters all the same.
	// Receipt files share the suffix and are not event streams.
	streams, _ := filepath.Glob(filepath.Join(*root, "*", "runs", "*.jsonl"))
	for _, s := range streams {
		if !strings.HasSuffix(s, ".receipts.jsonl") {
			logs = append(logs, s)
		}
	}
	sort.Strings(logs)
	var records []record
	for _, log := range logs {
		rec, err := extract(log)
		if err != nil {
			fmt.Fprintln(os.Stderr, log+":", err)
			continue
		}
		records = append(records, rec)
	}
	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, r := range records {
		_ = enc.Encode(r)
	}
	fmt.Printf("corpus: %d encounter(s) from %d log(s) -> %s\n", len(records), len(logs), *out)
}

func extract(log string) (record, error) {
	exp := filepath.Base(filepath.Dir(filepath.Dir(log)))
	run := strings.TrimSuffix(strings.TrimSuffix(filepath.Base(log), ".log"), ".jsonl")
	rec := record{Encounter: exp + "/" + run, SourceLog: log,
		Instrument: map[string]any{"sensei_sha": "unrecorded", "sensei_code_sha": "unrecorded", "fixture": "unrecorded", "world": "unrecorded"},
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
		case "status":
			if m := coverageLine.FindStringSubmatch(e.Summary); m != nil {
				rec.Coverage = append(rec.Coverage, map[string]any{"anchors": m[1], "planned_files": m[2], "route": m[3], "anchor_lines": p["anchors"], "operational_authority": p["operational_authority"]})
				rec.Route = append(rec.Route, m[3])
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
			rec.Candidate = append(rec.Candidate, map[string]any{"summary": e.Summary, "time": e.Time})
		case "validation.run":
			rec.Validation = append(rec.Validation, map[string]any{"diff_digest": p["diff_digest"], "checks": p["checks"]})
		case "candidate.audited":
			rec.Audit = append(rec.Audit, map[string]any{"decision": p["decision"], "digest": p["digest"], "findings": p["findings"], "reason_codes": p["reason_codes"]})
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
			}
		}
	}
	if b, err := os.ReadFile(base + ".recipes-after.json"); err == nil {
		var doc struct {
			Recipes []map[string]any `json:"recipes"`
		}
		if json.Unmarshal(b, &doc) == nil {
			for _, r := range doc.Recipes {
				rec.RecipesAfter = append(rec.RecipesAfter, map[string]any{"kind": r["kind"], "dir": r["dir"], "type": r["type"], "field": r["field"], "command": r["command"], "owner": r["owner"], "search_paths": r["search_paths"]})
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

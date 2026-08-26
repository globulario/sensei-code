package derived

// Letting a closure round remember what is worth checking.
//
// The dangerous loop, already demonstrated against this repository's own
// promote path, is: an agent meets a blind spot, writes a claim, the claim
// removes the blind spot, and the agent gains authority from its own writing.
// `sensei promote` accepted a TRUE claim, a plausible FALSE one, and a claim
// whose evidence cited only files its own change introduced -- identically. It
// validates form, not evidence, so it cannot be the boundary that admits
// knowledge.
//
// A recipe is not knowledge. It is a QUESTION, and it establishes nothing by
// being written: coverage exists only where `sensei derive` answers it against
// the world being assessed, and a question that does not derive yields no
// anchor at all. The agent chooses WHERE TO LOOK; the mechanism decides WHAT IS
// SO. Self-approval cannot arise, because writing the question grants nothing
// and the author does not control the answer.
//
// That is why this write path is open while the one for invariants, contracts
// and decisions stays shut behind dq.closure_knowledge_admission.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Provenance is why a recipe exists, recorded by the engine rather than
// supplied by the agent.
//
// Stamped by the caller so it cannot be authored: an agent that could write its
// own provenance could make an invented question look like the product of an
// investigation that never happened.
type Provenance struct {
	// OriginTask is the task whose closure round produced this question. It is
	// also the mechanism behind the future-only rule: a recipe never covers the
	// task that wrote it.
	OriginTask string `json:"origin_task,omitempty"`
	// OriginGap is the governance condition that went unclosed.
	OriginGap string `json:"origin_gap,omitempty"`
	// Region is the files the closure round was investigating.
	Region []string `json:"region,omitempty"`
	// WrittenAt and WrittenBy identify the write.
	WrittenAt string `json:"written_at,omitempty"`
	WrittenBy string `json:"written_by,omitempty"`
}

// answerableKinds are the questions `sensei derive` knows how to answer.
//
// A closure round may not invent a kind. An unknown kind does not fail loudly --
// it derives UNKNOWN forever, which is a quiet way of writing nothing while
// looking like accumulation.
var answerableKinds = map[string][]string{
	"field_access_under_lock":        {"dir", "type", "field", "lock"},
	"command_invocation_confined_to": {"command", "owner", "search_paths"},
}

// Identity is a recipe's canonical key, computed from the QUESTION only.
//
// Provenance and prose are excluded on purpose: forty encounters with the same
// unknown region must produce one question, not forty, and two rounds that
// phrase the same check differently must collide rather than accumulate.
func (r Recipe) Identity() string {
	switch r.Kind {
	case "command_invocation_confined_to":
		paths := append([]string(nil), r.SearchPaths...)
		sort.Strings(paths)
		return strings.ToLower(fmt.Sprintf("%s|%s|%s|%s",
			r.Kind, r.Command, clean(r.Owner), strings.Join(paths, ",")))
	default:
		return strings.ToLower(fmt.Sprintf("%s|%s|%s|%s|%s",
			r.Kind, clean(r.Dir), r.Type, r.Field, r.Lock))
	}
}

func clean(p string) string { return strings.Trim(filepath.ToSlash(strings.TrimSpace(p)), "/") }

// ErrRecipeRefused is a proposed question the writer will not record.
type ErrRecipeRefused struct{ Why string }

func (e ErrRecipeRefused) Error() string { return "recipe refused: " + e.Why }

// Validate checks a proposed question is answerable and confined.
//
// Deliberately mechanical. Nothing here judges whether the question is a GOOD
// one -- that is what the derivation and the experiment decide. It only ensures
// the question CAN be answered and names a region the round was actually
// looking at.
func Validate(r Recipe, region []string) error {
	need, ok := answerableKinds[r.Kind]
	if !ok {
		var kinds []string
		for k := range answerableKinds {
			kinds = append(kinds, k)
		}
		sort.Strings(kinds)
		return ErrRecipeRefused{fmt.Sprintf("kind %q is not one `sensei derive` can answer (%s); "+
			"an unanswerable question derives UNKNOWN forever, which is writing nothing while "+
			"looking like accumulation", r.Kind, strings.Join(kinds, ", "))}
	}
	got := map[string]string{
		"dir": r.Dir, "type": r.Type, "field": r.Field, "lock": r.Lock,
		"command": r.Command, "owner": r.Owner,
		"search_paths": strings.Join(r.SearchPaths, ","),
	}
	for _, term := range need {
		if strings.TrimSpace(got[term]) == "" {
			return ErrRecipeRefused{fmt.Sprintf("kind %q needs a %q term", r.Kind, term)}
		}
	}
	for _, p := range append([]string{r.Dir, r.Owner}, r.SearchPaths...) {
		if p == "" {
			continue
		}
		if filepath.IsAbs(p) || strings.Contains(filepath.ToSlash(p), "../") {
			return ErrRecipeRefused{fmt.Sprintf("path %q must be repository-relative and inside the tree", p)}
		}
	}
	// The question must be about somewhere the round was actually looking.
	//
	// Without this, a closure round on package A could write a question about
	// package B and quietly widen its own future authority over a region it
	// never investigated.
	if len(region) != 0 {
		if where := clean(firstNonEmpty(r.Dir, r.Owner)); where != "" && !within(where, region) {
			return ErrRecipeRefused{fmt.Sprintf("%q is outside the region this round investigated (%s)",
				where, strings.Join(region, ", "))}
		}
	}
	return nil
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// within reports whether a directory contains, or is contained by, any file in
// the investigated region.
func within(dir string, region []string) bool {
	for _, f := range region {
		d := clean(filepath.Dir(clean(f)))
		if d == dir || strings.HasPrefix(d+"/", dir+"/") || strings.HasPrefix(dir+"/", d+"/") {
			return true
		}
	}
	return false
}

// Append records a question, refusing a duplicate and never a rule.
//
// Returns added=false when an identical question is already present, which is
// the ordinary outcome once a region has been investigated once and is not an
// error. The file's other contents, including its explanatory comment, are
// preserved.
func Append(path string, r Recipe, p Provenance, region []string) (added bool, err error) {
	if err := Validate(r, region); err != nil {
		return false, err
	}
	if strings.TrimSpace(p.OriginTask) == "" {
		return false, ErrRecipeRefused{"a recipe must record the task that produced it, or the " +
			"future-only rule cannot be enforced and the writer could cover its own run"}
	}
	var doc map[string]json.RawMessage
	if b, rerr := os.ReadFile(path); rerr == nil {
		if json.Unmarshal(b, &doc) != nil {
			return false, fmt.Errorf("parse %s", path)
		}
	} else if !os.IsNotExist(rerr) {
		return false, rerr
	}
	if doc == nil {
		doc = map[string]json.RawMessage{}
	}
	var existing []Recipe
	if raw, ok := doc["recipes"]; ok {
		if json.Unmarshal(raw, &existing) != nil {
			return false, fmt.Errorf("parse recipes in %s", path)
		}
	}
	for _, have := range existing {
		if have.Identity() == r.Identity() {
			return false, nil
		}
	}
	if p.WrittenAt == "" {
		p.WrittenAt = time.Now().UTC().Format(time.RFC3339)
	}
	r.Provenance = &p
	existing = append(existing, r)
	raw, err := json.MarshalIndent(existing, "  ", "  ")
	if err != nil {
		return false, err
	}
	doc["recipes"] = raw
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(path, append(out, '\n'), 0o644)
}

// ExcludingTask drops recipes a given task wrote.
//
// The future-only rule, enforced mechanically rather than trusted. A closure
// round that writes a question must not be covered by it: if it were, the run
// that could not establish its own authority would have established it by
// writing something down, which is the self-approval this whole design refuses.
//
// One capability at a time. Whether a run may safely benefit from its own
// closure is a separate question, and it is not answered by assuming it can.
func ExcludingTask(recipes []Recipe, taskID string) []Recipe {
	if strings.TrimSpace(taskID) == "" {
		return recipes
	}
	out := make([]Recipe, 0, len(recipes))
	for _, r := range recipes {
		if r.Provenance != nil && r.Provenance.OriginTask == taskID {
			continue
		}
		out = append(out, r)
	}
	return out
}

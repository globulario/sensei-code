// Package retrieval decides what evidence a conversational turn should consult.
//
// The assisted turn used to inject the same two things every time: workspace
// status and a broad, unscoped preflight. That is a graph-dump discipline
// wearing a retrieval name — it costs the same on every question, and it
// answers none of them specifically. A question about one invariant got the
// same context as a question about the whole repository.
//
// So selection is driven by the question. Two rules make that safe rather than
// clever.
//
//	Selection routes a query; it never creates a claim. Nothing here decides
//	what is true. If the question names something the graph has never heard of,
//	the retrieval reports that the graph answered and found nothing — it does
//	not fall back to inference, and it does not quietly widen the query until
//	something comes back.
//
//	Every bound is stated. A turn that consults four sources out of nine says
//	so, because a silent cap reads exactly like complete coverage to the next
//	person who trusts the answer.
package retrieval

import (
	"regexp"
	"sort"
	"strings"
)

// Kind is how a target should be looked up. The vocabulary is closed and maps
// onto Sensei's own typed query modes; there is no free-form query here,
// because a free-form query is where a retrieval layer starts inventing
// questions the graph never agreed to answer.
type Kind string

const (
	// ByFile asks what governs a path.
	ByFile Kind = "by_file"
	// ByID asks the graph to resolve one named node.
	ByID Kind = "by_id"
)

// Query is one thing to ask, and why it was selected.
//
// Why is carried so the evidence drawer can show what in the question produced
// this lookup. A retrieval a person cannot trace back to their own words is one
// they cannot correct.
type Query struct {
	Kind   Kind   `json:"kind"`
	Target string `json:"target"`
	Why    string `json:"why"`
}

// Plan is what a turn intends to consult, and what it will not.
type Plan struct {
	Queries []Query `json:"queries"`
	// Dropped names the targets the budget excluded. It is a list rather than a
	// count so the omission is legible: "3 dropped" tells a reader nothing they
	// can act on.
	Dropped []string `json:"dropped,omitempty"`
	Budget  int      `json:"budget"`
}

// Bounded reports whether the budget excluded anything.
func (p Plan) Bounded() bool { return len(p.Dropped) != 0 }

// Describe states the plan, including what it left out.
func (p Plan) Describe() string {
	if len(p.Queries) == 0 {
		return "nothing in the question named a file or a governed id, so no targeted retrieval was planned"
	}
	var targets []string
	for _, q := range p.Queries {
		targets = append(targets, q.Target)
	}
	line := "retrieving " + strings.Join(targets, ", ")
	if p.Bounded() {
		line += "; not retrieved (budget " + itoa(p.Budget) + "): " + strings.Join(p.Dropped, ", ")
	}
	return line
}

var (
	// A path is recognised structurally: a slash and a file extension, or a
	// known source directory prefix. This is deliberately narrow. A pattern
	// that matched prose would send the graph looking for sentences.
	pathPattern = regexp.MustCompile(`(?:^|[\s"` + "`" + `(])((?:[\w.-]+/)+[\w.-]+\.\w{1,6})`)
	// A governed id is dotted and unslashed — sensei_code.workflow.mode — or
	// class-qualified, as awareness_query returns them.
	idPattern = regexp.MustCompile(`(?:^|[\s"` + "`" + `(])((?:invariant|failure|contract|decision):[\w.\-]+|[a-z][\w-]*(?:\.[a-z][\w-]*){2,})`)
)

// PlanFor selects what to consult for one question.
//
// It reads the question to route lookups and for nothing else. The targets it
// finds are candidates for retrieval, not findings: whether any of them means
// anything is the graph's answer to give.
func PlanFor(question string, budget int) Plan {
	if budget <= 0 {
		budget = 4
	}
	plan := Plan{Budget: budget}
	seen := map[string]bool{}
	add := func(kind Kind, target, why string) {
		target = strings.Trim(strings.TrimSpace(target), ".,;:)\"`")
		if target == "" || seen[target] {
			return
		}
		seen[target] = true
		if len(plan.Queries) >= budget {
			plan.Dropped = append(plan.Dropped, target)
			return
		}
		plan.Queries = append(plan.Queries, Query{Kind: kind, Target: target, Why: why})
	}

	// Paths first. A question that names a file is asking about that file, and
	// what governs it is the most specific evidence available.
	for _, m := range pathPattern.FindAllStringSubmatch(question, -1) {
		add(ByFile, m[1], "the question names this path")
	}
	for _, m := range idPattern.FindAllStringSubmatch(question, -1) {
		add(ByID, m[1], "the question names this governed id")
	}
	sort.SliceStable(plan.Dropped, func(a, b int) bool { return plan.Dropped[a] < plan.Dropped[b] })
	return plan
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Caller is the Sensei surface retrieval needs. It is read-only by
// construction: the tools it can name are the typed read surfaces, and there is
// no path here that reaches propose, admission or task state.
type Caller interface {
	CallTool(name string, args map[string]any) (Result, error)
}

// Result is the minimum of a tool result this package reads.
type Result struct {
	Text       string
	Structured map[string]any
}

// Outcome is what one query produced, in the same typed vocabulary the rest of
// the context packet uses.
type Outcome struct {
	Query Query
	// State is one of the assist presence values, kept as a string so this
	// package does not import the packet it feeds.
	State  string
	Detail string
	Text   string
}

const (
	StatePresent     = "present"
	StateEmptyProven = "empty-proven"
	StateUnavailable = "unavailable"
)

// Execute runs the plan and reports each outcome.
//
// A query that returns nothing is reported as the graph having answered and
// found nothing — not as an error, and never by silently widening the search
// until something comes back. Absence of evidence is a real answer here, and it
// is the one most easily converted into a confident wrong one.
func Execute(caller Caller, domain string, plan Plan) []Outcome {
	var out []Outcome
	for _, q := range plan.Queries {
		name, args := q.request(domain)
		res, err := caller.CallTool(name, args)
		switch {
		case err != nil:
			out = append(out, Outcome{Query: q, State: StateUnavailable, Detail: err.Error()})
		case strings.TrimSpace(res.Text) == "":
			out = append(out, Outcome{Query: q, State: StateEmptyProven,
				Detail: "the graph answered and returned nothing for " + q.Target})
		default:
			out = append(out, Outcome{Query: q, State: StatePresent, Text: res.Text, Detail: q.Why})
		}
	}
	return out
}

// request maps a query onto the exact typed Sensei surface for it.
func (q Query) request(domain string) (string, map[string]any) {
	args := map[string]any{}
	if d := strings.TrimSpace(domain); d != "" {
		args["domain"] = d
	}
	switch q.Kind {
	case ByFile:
		args["file"] = q.Target
		return "awareness_briefing", args
	default:
		args["mode"] = "by_id"
		args["id"] = q.Target
		return "awareness_query", args
	}
}

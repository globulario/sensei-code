// Package architect turns Sensei's evidence into the answers an architect
// actually asks for.
//
// Its one rule is that a number must carry its own provenance. Sensei publishes
// exact totals for some classes and none for others, and a query that returns
// rows returns at most as many as it was asked for. Presenting those as the same
// kind of fact is how a report starts lying: "144 invariants" reads as a well
// governed repository when 138 of them are an installed portable pack and six
// were written for this code.
package architect

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Count is one figure with the standing of the evidence behind it.
type Count struct {
	Label string
	Value int
	// Exact marks a total Sensei published. When false the figure is a lower
	// bound from a bounded query and is rendered as such.
	Exact bool
	// Note explains anything the number alone would misrepresent.
	Note string
}

// Report is what Sensei holds about one repository.
type Report struct {
	Domain string
	// Authority is Sensei's own verdict on whether its answers can be relied on.
	Authority   string
	Freshness   string
	Coverage    string
	Triples     int
	Authored    []Count
	Installed   []Count
	Unpublished []string
	// Gaps are the classes Sensei holds nothing for, named rather than shown as
	// a reassuring zero.
	Gaps []string
}

// metadataCounts maps Sensei's published totals to the labels an architect
// reads. Only fields Sensei actually publishes appear here; a class absent from
// metadata has no total and must never be rendered as zero.
var metadataCounts = []struct {
	Field string
	Label string
}{
	{"invariant_count", "invariants"},
	{"failure_mode_count", "failure modes"},
	{"forbidden_fix_count", "forbidden fixes"},
	{"incident_pattern_count", "incident patterns"},
	{"required_test_count", "required tests"},
	{"source_file_count", "source files"},
	{"intent_count", "intents"},
}

// unpublishedClasses are classes Sensei's metadata reports no total for. They
// are named explicitly so their absence from the counts is a stated fact rather
// than an omission the reader has to notice.
var unpublishedClasses = []string{"contracts", "decisions", "design patterns", "implementation patterns"}

// FromMetadata builds the report from one awareness_metadata document.
func FromMetadata(domain string, m map[string]any) Report {
	r := Report{
		Domain:      domain,
		Authority:   authorityOf(m),
		Freshness:   humanState(str(m, "graph_freshness_state"), "GRAPH_FRESHNESS_STATE_"),
		Coverage:    humanState(str(m, "coverage_state"), "COVERAGE_STATE_"),
		Triples:     num(m, "triple_count"),
		Unpublished: append([]string(nil), unpublishedClasses...),
	}

	// The portable meta-principle pack is installed by `sensei init` and is
	// counted inside invariant_count. Reporting the total alone would credit
	// this repository with governance it never wrote.
	meta := num(m, "meta_principle_count")
	for _, c := range metadataCounts {
		value, ok := m[c.Field]
		if !ok {
			continue
		}
		count := Count{Label: c.Label, Value: toInt(value), Exact: true}
		if c.Field == "invariant_count" {
			switch {
			case meta > 0:
				count.Value -= meta
				count.Note = fmt.Sprintf("%d further invariants come from the installed meta-principle pack", meta)
			default:
				// Not knowing the pack size is not the same as there being no
				// pack. Saying so keeps the figure from reading as authored
				// governance when most of it may be installed.
				count.Exact = false
				count.Note = "this surface does not publish how many of these are the installed meta-principle pack"
			}
		}
		r.Authored = append(r.Authored, count)
		if count.Value == 0 {
			r.Gaps = append(r.Gaps, c.Label)
		}
	}
	if meta > 0 {
		r.Installed = append(r.Installed, Count{
			Label: "meta principles", Value: meta, Exact: true,
			Note: "portable pack installed by sensei init, not authored for this repository",
		})
	}
	sort.SliceStable(r.Authored, func(i, j int) bool { return r.Authored[i].Value > r.Authored[j].Value })
	return r
}

func authorityOf(m map[string]any) string {
	authority, ok := m["authority"].(map[string]any)
	if !ok {
		// Sensei said nothing about its own standing, which is not the same as
		// saying it is authoritative.
		return "unstated"
	}
	verdict := str(authority, "verdict")
	if verdict == "" {
		verdict = "unstated"
	}
	if state := str(authority, "state"); state != "" {
		verdict += " · " + state
	}
	return verdict
}

// Render writes the report. It ends with what the report does not establish,
// because a page of counts reads as an assessment unless it says otherwise.
func (r Report) Render(width int) string {
	var b strings.Builder
	b.WriteString("Architecture report — " + r.Domain + "\n")
	b.WriteString(fmt.Sprintf("  graph: %s · freshness %s · coverage %s · %d triples\n",
		r.Authority, r.Freshness, r.Coverage, r.Triples))

	if len(r.Authored) != 0 {
		b.WriteString("\n  authored for this repository (exact totals from Sensei):\n")
		b.WriteString(bars(r.Authored, width))
	}
	if len(r.Installed) != 0 {
		b.WriteString("\n  installed, not authored here:\n")
		b.WriteString(bars(r.Installed, width))
	}
	if len(r.Unpublished) != 0 {
		b.WriteString("\n  Sensei publishes no total for: " + strings.Join(r.Unpublished, ", ") + "\n")
		b.WriteString("    these cannot be counted from metadata, so they are neither reported as zero nor as present\n")
	}
	if len(r.Gaps) != 0 {
		b.WriteString("\n  classes Sensei holds nothing for: " + strings.Join(r.Gaps, ", ") + "\n")
	}

	b.WriteString("\n  what this does not establish:\n")
	b.WriteString("    - a class Sensei holds nothing for is knowledge it was never given, not evidence the repository lacks it\n")
	b.WriteString("    - counts measure what is recorded, never whether the code obeys it\n")
	return strings.TrimRight(b.String(), "\n")
}

// bars renders counts proportionally. The scale is the largest value shown, so
// a bar means "relative to the rest of this report" and never implies a target.
func bars(counts []Count, width int) string {
	label, longest := 0, 0
	for _, c := range counts {
		if len(c.Label) > label {
			label = len(c.Label)
		}
		if c.Value > longest {
			longest = c.Value
		}
	}
	span := width - label - 16
	if span < 8 {
		span = 8
	}
	var b strings.Builder
	for _, c := range counts {
		filled := 0
		if longest > 0 {
			filled = c.Value * span / longest
		}
		value := fmt.Sprintf("%d", c.Value)
		if !c.Exact {
			value = "≥" + value
		}
		b.WriteString(fmt.Sprintf("    %-*s %6s  %s\n", label, c.Label, value, strings.Repeat("█", filled)))
		if c.Note != "" {
			b.WriteString(fmt.Sprintf("    %-*s         %s\n", label, "", c.Note))
		}
	}
	return b.String()
}

func str(m map[string]any, key string) string {
	v, _ := m[key].(string)
	return v
}

func num(m map[string]any, key string) int { return toInt(m[key]) }

// toInt accepts the shapes Sensei's two surfaces use. The MCP payload sends
// numbers as JSON numbers and the CLI sends several of them as strings, so a
// reader that understands only one silently sees zero on the other.
func toInt(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0
		}
		return parsed
	}
	return 0
}

// humanState strips Sensei's enum prefix so a report reads as prose. An unknown
// or absent state stays "unknown" rather than being smoothed into something
// reassuring.
func humanState(value, prefix string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	return strings.ToLower(strings.TrimPrefix(value, prefix))
}

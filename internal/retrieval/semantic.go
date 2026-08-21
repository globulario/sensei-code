package retrieval

// Selecting a target the question does not name.
//
// Structural routing answers a question that names a path or a governed id, and
// answers nothing else. Ask "what stops a worker widening its own scope" and
// nothing is retrieved, even though the graph holds an invariant about exactly
// that. The turn reports the absence honestly, which is the correct failure and
// still a failure.
//
// So when the question names nothing, the graph is asked what it holds and the
// question is matched against those labels. Two properties keep that from
// becoming a search engine that invents relevance:
//
//	Matching selects a lookup and states its evidence. A selected node is a
//	node worth asking about, never a node that answers anything. What the node
//	says is retrieved from the graph afterwards, and that retrieval is the
//	evidence — the match is only how it was found.
//
//	A survey that matches nothing says how much it looked at. "Nothing was
//	retrieved" and "the graph holds forty invariants and none shares a term with
//	your question" are different answers, and only the second lets a reader tell
//	a coverage gap from a vocabulary mismatch.

import (
	"sort"
	"strings"
)

// ByClass surveys what the graph holds of one class, so the question can be
// matched against real labels rather than guessed ids.
const ByClass Kind = "by_class"

// SurveyClasses are the classes worth surveying for an architectural question.
// It is short on purpose: surveying everything would spend the budget on
// classes that answer no question anyone asks in conversation.
var SurveyClasses = []string{"invariant", "contract"}

// Node is one row of a survey.
type Node struct {
	ID    string `json:"id"`
	Class string `json:"class"`
	Label string `json:"label"`
}

// Match is a node the question plausibly refers to, with the terms that made it
// plausible and how much they were worth.
type Match struct {
	Node  Node
	Terms []string
	// Weight sums the distinctiveness of the matched terms. A word that appears
	// in most nodes barely distinguishes anything, and ranking without that
	// makes the most generic node win — which is how "who decides what happens
	// to a candidate" selected a node about admission rather than about
	// candidates, on the single word "decides".
	Weight float64
}

// stopWords are the words that match everything and therefore select nothing.
var stopWords = map[string]bool{
	"the": true, "and": true, "for": true, "that": true, "this": true, "with": true,
	"what": true, "when": true, "does": true, "not": true, "how": true, "why": true,
	"are": true, "was": true, "can": true, "our": true, "its": true, "from": true,
	"into": true, "have": true, "has": true, "should": true, "would": true, "must": true,
	"you": true, "your": true, "about": true, "which": true, "there": true, "then": true,
}

// terms reduces text to the words worth matching on.
func terms(s string) []string {
	var out []string
	for _, raw := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !('a' <= r && r <= 'z') && !('0' <= r && r <= '9')
	}) {
		if len(raw) < 4 || stopWords[raw] {
			continue
		}
		out = append(out, raw)
	}
	return out
}

// Select ranks surveyed nodes against the question.
//
// Ties are broken by id so the same question selects the same nodes, because a
// retrieval that varies between identical turns is one nobody can reason about.
func Select(question string, nodes []Node, limit int) []Match {
	if limit <= 0 {
		limit = 2
	}
	want := map[string]bool{}
	for _, t := range terms(question) {
		want[t] = true
	}
	if len(want) == 0 {
		return nil
	}
	// How many nodes each term appears in, so a common word counts for less.
	frequency := map[string]int{}
	for _, n := range nodes {
		seen := map[string]bool{}
		for _, t := range terms(n.Label + " " + n.ID) {
			if !seen[t] {
				seen[t] = true
				frequency[t]++
			}
		}
	}

	var matches []Match
	for _, n := range nodes {
		var hit []string
		weight := 0.0
		seen := map[string]bool{}
		for _, t := range terms(n.Label + " " + n.ID) {
			if want[t] && !seen[t] {
				seen[t] = true
				hit = append(hit, t)
				weight += 1 / float64(frequency[t])
			}
		}
		if len(hit) == 0 {
			continue
		}
		sort.Strings(hit)
		matches = append(matches, Match{Node: n, Terms: hit, Weight: weight})
	}
	sort.SliceStable(matches, func(a, b int) bool {
		if matches[a].Weight != matches[b].Weight {
			return matches[a].Weight > matches[b].Weight
		}
		if len(matches[a].Terms) != len(matches[b].Terms) {
			return len(matches[a].Terms) > len(matches[b].Terms)
		}
		// Last resort, and stable: identical evidence must not reorder between
		// identical turns.
		return matches[a].Node.ID < matches[b].Node.ID
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return matches
}

// Queries turns matches into the lookups that will actually produce evidence.
func Queries(matches []Match) []Query {
	var out []Query
	for _, m := range matches {
		out = append(out, Query{
			Kind:   ByID,
			Target: m.Node.ID,
			Why:    "the question and this node share: " + strings.Join(m.Terms, ", "),
		})
	}
	return out
}

// SurveyOutcome describes what a survey found, including when it found nothing.
type SurveyOutcome struct {
	Surveyed int
	Matched  int
	// Failed counts class queries that did not answer, and Reason is the first
	// thing that went wrong.
	//
	// They exist because Surveyed==0 has two causes that read identically once
	// the count is all that survives: a graph that genuinely holds nothing, and
	// a graph that was never reached. The first is a finding about the project;
	// the second is a finding about the connection, and reporting it as the
	// first is the recorded failure mode
	// empty_sensei_tool_response_accepted_as_present_evidence.
	Failed int
	Reason string
}

// Complete reports whether every surveyed class actually answered. Only a
// complete survey may make a claim about what the graph holds.
func (s SurveyOutcome) Complete() bool { return s.Failed == 0 }

// Describe states the survey result in a form that distinguishes a coverage gap
// from a vocabulary mismatch.
func (s SurveyOutcome) Describe() string {
	switch {
	case s.Failed > 0 && s.Surveyed == 0:
		// Nothing was learned, so nothing may be asserted. Saying "the graph
		// holds nothing" here would turn a transport failure into a claim
		// about the project.
		return "the graph could not be surveyed: all " + itoa(s.Failed) + " class quer(ies) failed (" + s.Reason +
			"), so nothing is known about what it holds for this domain"
	case s.Failed > 0:
		return "the graph was surveyed incompletely: " + itoa(s.Failed) + " class quer(ies) failed (" + s.Reason +
			"), so the " + itoa(s.Surveyed) + " node(s) found are a floor and not the whole picture"
	case s.Surveyed == 0:
		return "the graph holds nothing of the surveyed classes for this domain, so there was nothing to match the question against"
	case s.Matched == 0:
		return "the graph holds " + itoa(s.Surveyed) + " governed node(s) and none shares a term with the question; " +
			"that is a vocabulary mismatch or a coverage gap, not a statement that nothing governs this"
	default:
		return "matched " + itoa(s.Matched) + " of " + itoa(s.Surveyed) + " surveyed node(s)"
	}
}

// NodesFrom reads survey rows out of a structured query result.
//
// It reads the structured payload rather than the rendered listing, for the
// same reason the router stopped reading the change-risk sentence: a consumer
// that parses a formatted table is one wording change away from silently
// finding nothing.
func NodesFrom(structured map[string]any) []Node {
	rows, _ := structured["rows"].([]any)
	var out []Node
	for _, raw := range rows {
		row, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		id, _ := row["id"].(string)
		if strings.TrimSpace(id) == "" {
			continue
		}
		class, _ := row["class"].(string)
		label, _ := row["label"].(string)
		out = append(out, Node{ID: id, Class: class, Label: label})
	}
	return out
}

// SurveyQuery asks the graph what it holds of one class.
func SurveyQuery(class string, limit int) (string, map[string]any) {
	return "awareness_query", map[string]any{"mode": string(ByClass), "class": class, "limit": limit}
}

package retrieval

import (
	"strings"
	"testing"
)

func nodes() []Node {
	return []Node{
		{ID: "invariant:sensei_code.workflow.context_never_widens_worker_scope", Class: "invariant",
			Label: "Shared context explains why a change was asked for and never enlarges what may be changed"},
		{ID: "invariant:sensei_code.publication.never_merges", Class: "invariant",
			Label: "Sensei Code may open a pull request and may never merge one"},
		{ID: "invariant:sensei_code.candidate.disposition_is_decided", Class: "invariant",
			Label: "A candidate is kept or removed by decision, and its evidence is recorded before anything is removed"},
	}
}

// The case criterion 4 of sensei-code#9 asks for: a question that names no file
// and no id still reaches the governed knowledge about it.
func TestAQuestionThatNamesNothingStillFindsWhatGovernsIt(t *testing.T) {
	matches := Select("what stops a worker from widening the scope it was given?", nodes(), 2)
	if len(matches) == 0 {
		t.Fatal("a question about scope widening matched nothing")
	}
	if !strings.Contains(matches[0].Node.ID, "context_never_widens_worker_scope") {
		t.Fatalf("the closest node was not selected first: %+v", matches[0])
	}
	// The selection has to be able to say why it chose, or a human cannot
	// correct a wrong choice.
	if len(matches[0].Terms) == 0 {
		t.Error("a node was selected with no stated evidence for the match")
	}
	for _, term := range matches[0].Terms {
		if !strings.Contains(strings.ToLower("what stops a worker from widening the scope it was given?"), term) {
			t.Errorf("the match cites a term %q that is not in the question", term)
		}
	}
}

// Matching selects a lookup, never an answer. The queries it produces are
// by_id lookups whose result is the evidence.
func TestMatchingProducesLookupsNotAnswers(t *testing.T) {
	qs := Queries(Select("candidate disposition and evidence", nodes(), 2))
	if len(qs) == 0 {
		t.Fatal("no lookups were produced")
	}
	for _, q := range qs {
		if q.Kind != ByID {
			t.Errorf("a match produced a %s query rather than a lookup: %+v", q.Kind, q)
		}
		if !strings.Contains(q.Why, "share") {
			t.Errorf("the lookup does not carry why it was selected: %+v", q)
		}
		name, _ := q.request("dom")
		if name != "awareness_query" {
			t.Errorf("a selected lookup does not go to a typed read surface: %s", name)
		}
	}
}

// Common words select everything and therefore select nothing useful. A match
// on "the" would make every question retrieve every invariant.
func TestCommonWordsDoNotSelect(t *testing.T) {
	if got := Select("what about the thing that you have for this?", nodes(), 2); len(got) != 0 {
		t.Fatalf("a question of stop-words matched nodes: %+v", got)
	}
	if got := Select("", nodes(), 2); len(got) != 0 {
		t.Fatalf("an empty question matched nodes: %+v", got)
	}
}

// An empty survey and an empty match are different answers, and only one of
// them is about coverage.
func TestASurveyThatMatchesNothingSaysHowMuchItLookedAt(t *testing.T) {
	empty := SurveyOutcome{Surveyed: 0}.Describe()
	if !strings.Contains(empty, "nothing of the surveyed classes") {
		t.Errorf("an empty graph is not distinguished: %s", empty)
	}
	mismatch := SurveyOutcome{Surveyed: 40, Matched: 0}.Describe()
	if !strings.Contains(mismatch, "40") || !strings.Contains(mismatch, "not a statement that nothing governs this") {
		t.Errorf("a vocabulary mismatch reads as an absence of governance: %s", mismatch)
	}
}

// The survey reads the structured payload, not a rendered table.
func TestSurveyRowsAreReadStructurally(t *testing.T) {
	got := NodesFrom(map[string]any{"rows": []any{
		map[string]any{"id": "invariant:a", "class": "invariant", "label": "A"},
		map[string]any{"id": "  ", "class": "invariant", "label": "blank id"},
		"not a row",
	}})
	if len(got) != 1 || got[0].ID != "invariant:a" {
		t.Fatalf("rows were not read structurally: %+v", got)
	}
	name, args := SurveyQuery("invariant", 40)
	if name != "awareness_query" || args["mode"] != "by_class" {
		t.Errorf("the survey does not use the typed by_class surface: %s %v", name, args)
	}
}

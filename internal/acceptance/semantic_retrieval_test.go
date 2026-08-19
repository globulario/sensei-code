//go:build acceptance

package acceptance

// Criterion 4 of sensei-code#9, against a live graph.
//
//	"Ask about a known invariant/contract without naming its file; the turn
//	 retrieves the relevant Sensei knowledge."
//
// The unit tests establish that selection ranks the right node out of a fixture.
// This asks the real graph, because the property that matters is not that the
// ranking function works — it is that a question phrased the way a person
// phrases it reaches knowledge the graph actually holds.
//
//	go test -tags acceptance ./internal/acceptance/ -run TestSemanticRetrieval -v

import (
	"context"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/retrieval"
	"github.com/globulario/sensei-code/internal/sensei"
)

type liveReader struct{ sc *sensei.Client }

func (r liveReader) CallTool(name string, args map[string]any) (retrieval.Result, error) {
	res, err := r.sc.CallTool(name, args)
	if err != nil {
		return retrieval.Result{}, err
	}
	text := ""
	if len(res.Content) > 0 {
		text = res.Content[0].Text
	}
	return retrieval.Result{Text: text, Structured: res.Structured}, nil
}

func TestSemanticRetrievalFindsWhatAQuestionDoesNotName(t *testing.T) {
	root := repoRoot(t)
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	sc, err := sensei.Start(context.Background(), root, cfg.Sensei.Command, cfg.Sensei.Args)
	if err != nil {
		t.Fatalf("start Sensei: %v", err)
	}
	defer sc.Close()

	const domain = "github.com/globulario/sensei-code"
	var nodes []retrieval.Node
	for _, class := range retrieval.SurveyClasses {
		name, args := retrieval.SurveyQuery(class, 40)
		args["domain"] = domain
		res, err := liveReader{sc}.CallTool(name, args)
		if err != nil {
			t.Logf("%s survey unavailable: %v", class, err)
			continue
		}
		nodes = append(nodes, retrieval.NodesFrom(res.Structured)...)
	}
	if len(nodes) == 0 {
		t.Skip("the endpoint holds no invariants or contracts for this domain, so there is nothing a question could find")
	}
	t.Logf("surveyed %d governed node(s)", len(nodes))

	// Questions phrased the way a person phrases them, naming no file and no id.
	for _, q := range []struct{ question, want string }{
		{"what stops a worker from widening the scope it was given?", "widen"},
		{"can this thing merge my pull request by itself?", "merge"},
		{"who decides what happens to a candidate worktree afterwards?", "disposition"},
	} {
		matches := retrieval.Select(q.question, nodes, 2)
		if len(matches) == 0 {
			t.Errorf("%q retrieved nothing from a graph that holds %d nodes", q.question, len(nodes))
			continue
		}
		var found bool
		for i, m := range matches {
			t.Logf("%q -> [%d] %s (terms: %s, weight %.2f)", q.question, i, m.Node.ID, strings.Join(m.Terms, ", "), m.Weight)
			if strings.Contains(strings.ToLower(m.Node.ID+" "+m.Node.Label), q.want) {
				found = true
			}
		}
		// Among the retrieved set, not necessarily first. Lexical selection
		// finds nodes sharing distinctive words, which is not the same as
		// ranking by which one answers the question — "who decides" is a strong
		// signal for a node about deciding even when the question is about
		// candidates. Retrieval hands the architect real governed knowledge to
		// read; it does not pretend to have understood the question.
		if !found {
			t.Errorf("%q retrieved nothing mentioning %q from a graph that holds %d nodes",
				q.question, q.want, len(nodes))
		}

		// And the selected node must actually resolve: a match that names an id
		// the graph cannot return is a suggestion, not evidence.
		out := retrieval.Execute(liveReader{sc}, domain, retrieval.Plan{Queries: retrieval.Queries(matches[:1])})
		if len(out) != 1 || out[0].State != retrieval.StatePresent {
			t.Errorf("the selected node did not resolve to evidence: %+v", out)
		}
	}
}

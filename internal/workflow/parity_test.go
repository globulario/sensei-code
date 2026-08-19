package workflow

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/event"
)

// The acceptance criteria from sensei-code#9 that can be established
// mechanically, written as the issue numbers them.
//
// Three of the issue's fourteen cannot be: 1 (a follow-up preserving an
// architectural subject), 4 (retrieving knowledge about a contract the question
// does not name) and 6 (recalling a rejected direction) are judgements about
// the quality of a live answer over a populated graph. Asserting them here
// would mean asserting that a fixture came back, which is not the same claim
// and would be the more comfortable one to make. They are named in
// docs/open-items.md instead.

// #7 — a prior architect statement that conflicts with Sensei loses to Sensei,
// and the conflict is surfaced rather than smoothed over.
// #9 — a conversational preference does not become a contract by being said.
func TestSenseiOutranksTheConversationAndPreferencesAreNotContracts(t *testing.T) {
	prompt := assistedPrompt("/repo", "example.com/x", "ChatGPT", "what governs this?", "", nil,
		"ws", "pf", "(none)", "(none)", "(none)")

	// Matched as fragments: the prompt is wrapped, so a phrase spanning a line
	// break would fail for the wrong reason.
	for _, want := range []string{
		"Sensei remains the governance authority",
		"reinterpret",
		"invent its contracts",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the turn does not place Sensei above the conversation (%q missing)", want)
		}
	}
	// An assisted turn must not be able to file anything. Nothing said in
	// conversation becomes governance by being said.
	body := fileText(t, "internal/workflow/assisted.go")
	for _, forbidden := range []string{"awareness_propose", "authority.Persist", "decision.Write"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("an assisted turn reaches %s, so a preference could become recorded knowledge", forbidden)
		}
	}
}

// #8 — local project state cannot make an unpromoted proposal canonical.
//
// The strongest available form is structural: the summary is derived from
// durable records rather than stored, and it says what it is in the prompt. A
// store that could hold a claim is the thing that would make this possible, and
// there isn't one.
func TestLocalContextCannotManufactureCanonicalKnowledge(t *testing.T) {
	body := fileText(t, "internal/project/project.go")
	if strings.Contains(body, "func Save") || strings.Contains(body, "os.WriteFile") {
		t.Error("the project summary writes itself down; a second store of claims can disagree with Sensei invisibly")
	}
	prompt := assistedPrompt("/repo", "example.com/x", "ChatGPT", "q", "", nil, "ws", "pf", "(none)", "(none)",
		"These are references into durable records, not claims.")
	if !strings.Contains(prompt, "not claims") {
		t.Error("standing context reaches the architect without saying it carries no authority")
	}
}

// #10 — normal conversation cannot start a candidate, worker, commit, push or
// deployment. Covered structurally by the mode guard; asserted here too because
// this is the criterion whose failure would be least visible.
func TestConversationCannotReachExecution(t *testing.T) {
	// The turn's own body, not the file: the file contains the prompt text,
	// which legitimately talks about implementation without being able to do any.
	body := funcBody(t, "internal/workflow/assisted.go", "runAssisted")
	for _, forbidden := range []string{"CreateWorktree", "runCandidate", "implement", "offerPullRequest"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("an assisted turn can reach %s", forbidden)
		}
	}
	// And it must still tell the human where execution lives, or the boundary
	// is a dead end rather than a door.
	prompt := assistedPrompt("/repo", "example.com/x", "ChatGPT", "q", "", nil, "ws", "pf", "(none)", "(none)", "(none)")
	if !strings.Contains(prompt, "/run") {
		t.Error("the turn refuses to act without saying how the human can ask for action")
	}
}

// #11 — a long conversation does not replay itself into every request, and the
// bound is disclosed rather than silent.
func TestALongConversationIsBoundedAndSaysSo(t *testing.T) {
	var events []event.Event
	for i := 0; i < 60; i++ {
		events = append(events, event.Event{Kind: event.TaskCreated, Summary: "question " + itoa(i)})
	}
	e := &Engine{}
	turns := e.windowFrom(events, "question 59", 40)

	if strings.Count(turns, "HUMAN:") > 40 {
		t.Errorf("more than the limit was injected: %d turns", strings.Count(turns, "HUMAN:"))
	}
	if !strings.Contains(turns, "earlier turns are not shown") {
		t.Errorf("the window truncated silently, so the architect reads a conversation that began at turn 21:\n%s", turns)
	}
	if !strings.Contains(turns, "did not start here") {
		t.Error("the disclosure does not tell the architect what it is missing")
	}
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

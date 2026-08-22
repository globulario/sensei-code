package workflow

import (
	"strings"
	"testing"
)

// The governed architect used to receive the task, the conversation, the
// workspace status and an UNSCOPED preflight -- which is thin by construction,
// because there are no files to scope to until a plan exists. In both governed
// runs on 2026-08-21 that input read "PREFLIGHT_STATUS_EMPTY … Coverage is thin".
// So the architect planned with no governing evidence, and decideRoute then
// judged that plan against a scoped preflight the architect never saw.
func TestTheArchitectIsGivenWhatGovernsTheSubject(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "",
		"ws", "pf",
		"invariant:sensei_code.authority.only_an_explicit_answer_satisfies_a_boundary — critical",
		"internal/workflow/engine.go: runCandidate observes the candidate",
		"1 candidate resumable; 2 decisions taken",
		"2026-08-20 (task-1787235334698173142): decided: Preserve current human-owned intent")

	for _, want := range []string{
		"WHAT SENSEI HOLDS ABOUT THIS SUBJECT",
		"REPOSITORY EVIDENCE",
		"WHERE THIS PROJECT STANDS",
		"only_an_explicit_answer_satisfies_a_boundary",
		"runCandidate observes the candidate",
		"1 candidate resumable",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the architect prompt is missing %q", want)
		}
	}
}

// The thin preflight must be labelled, or the architect reads "coverage is
// thin" as a finding that the work is ungoverned rather than as an artefact of
// being asked before a plan exists.
func TestTheUnscopedPreflightSaysWhyItIsThin(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "", "")
	for _, want := range []string{"UNSCOPED", "thin by construction", "before knowing what you will touch"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the preflight section does not explain itself: missing %q", want)
		}
	}
}

// An empty section must read as a stated absence. A blank heading looks like a
// prompt that lost its content, and a model will treat it as one.
func TestAnEmptySectionStatesItsAbsence(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "", "")
	for _, want := range []string{
		"(the graph returned nothing for this subject)",
		"(no repository evidence was gathered for this turn)",
		"(this session has no standing work)",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("an empty section renders blank rather than absent: missing %q", want)
		}
	}
	if orNone("  ", "absent") != "absent" {
		t.Error("whitespace is not treated as empty")
	}
	if orNone("real", "absent") != "real" {
		t.Error("a present value was replaced by the absence text")
	}
}

// The standing summary is context, not permission. It is folded from the
// session record and must not read as authority to widen the plan.
func TestStandingContextAuthorisesNothing(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "work in flight", "")
	if !strings.Contains(prompt, "authorises nothing on its own") {
		t.Fatal("the standing section does not disclaim authority")
	}
}

// Every part must be derived from a source that can be re-read. A brief that
// narrated remembered conclusions under a governed heading would undo the
// continuity rule, which deliberately stores the identity of a conversation
// rather than its architectural claims.
func TestTheBriefIsDerivedNotRecalled(t *testing.T) {
	body := funcBody(t, "internal/workflow/architect_context.go", "architecturalContext")
	for _, want := range []string{"retrieval.PlanFor", "retrieval.Execute", "investigate.Repository", "project.Summarize"} {
		if !strings.Contains(body, want) {
			t.Errorf("the brief no longer derives from %s", want)
		}
	}
	// The assisted path's own functions are reused so the two cannot drift into
	// disagreeing about what governs a region.
	for _, shared := range []string{"surveyPlan", "renderRetrieved", "retrievedTargets"} {
		if !strings.Contains(body, shared) {
			t.Errorf("the governed brief no longer shares %s with the assisted turn", shared)
		}
	}
}

// What the architect was given must be recorded, or the brief is an assumption
// rather than evidence.
func TestTheGovernedBriefIsEmittedAsEvidence(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "execute")
	if !strings.Contains(body, "architecturalContext") {
		t.Fatal("the governed turn no longer assembles a brief")
	}
	if !strings.Contains(body, "ContextConsulted") {
		t.Fatal("the governed brief is not recorded, so nobody can check what the architect was told")
	}
	// Assembled before the architect is asked, not after.
	assemble := strings.Index(body, "architecturalContext")
	ask := strings.Index(body, "resolveArchitecture")
	if ask >= 0 && ask < assemble {
		t.Fatal("the architect is asked before its context is assembled")
	}
}

// A turn with no Sensei client must say so rather than rendering silence, and
// must still carry the standing summary, which needs no graph.
func TestABriefWithoutSenseiSaysSo(t *testing.T) {
	e := &Engine{}
	retrieved, repo, standing, _, consulted := e.architecturalContext(t.Context(), nil, "d", "task")
	if retrieved != "" || repo != "" {
		t.Error("graph-derived sections were populated without a Sensei client")
	}
	if !strings.Contains(consulted.Render(), "no Sensei client") {
		t.Errorf("the absence is not recorded: %q", consulted.Render())
	}
	_ = standing // folded unconditionally; its content depends on the session record
}

// The standing summary is bounded by the session: Store.Load reads one
// session's events.jsonl, so it resets on /clear and on every new run. The
// recorded decisions and audits are committed files, and are the only part of
// the brief that survives that boundary.
func TestRecordedHistoryCrossesTheSessionBoundary(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "",
		"2026-08-20 (task-123): decided: Preserve current human-owned intent")
	for _, want := range []string{
		"WHAT THIS REPOSITORY HAS ALREADY DECIDED AND FOUND",
		"Read from committed records, not recalled",
		"Preserve current human-owned intent",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the architect prompt is missing %q", want)
		}
	}
	// A recorded decision binds; the architect must be told that, or it will
	// treat a settled condition as an open one.
	if !strings.Contains(prompt, "has been answered") {
		t.Error("the prompt does not say a recorded decision binds")
	}
}

func TestRecordedHistoryIsGatheredAndDrawn(t *testing.T) {
	body := funcBody(t, "internal/workflow/architect_context.go", "architecturalContext")
	if !strings.Contains(body, "history.Gather") {
		t.Fatal("the brief no longer reads the recorded decisions and audits")
	}
	// Gathered before the Sensei-dependent sections, so a turn with no graph
	// still carries what the repository has decided.
	gather := strings.Index(body, "history.Gather")
	guard := strings.Index(body, "sc == nil")
	if guard >= 0 && guard < gather {
		t.Fatal("recorded history is skipped when Sensei is unavailable, though it needs no graph")
	}
	if !strings.Contains(body, "historyPresence") {
		t.Fatal("the drawer does not distinguish an empty corpus from an unreadable one")
	}
}

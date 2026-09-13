package workflow

// A bounded knowledge gap may only be assigned to a closure actor capable of
// changing the evidence whose absence caused the gap.
//
// Measured on task-1789272620293170079 (2026-09-13): a coverage-unexamined gap
// was routed to the architect, which cannot change graph coverage, and on failure
// escalated to Level-3 human authority — where a person cannot supply the missing
// evidence either. The engine's entire awareness surface is read-only with
// respect to coverage (awareness_audit_diff / edit_check / preflight, plus
// investigate and candidates which are documented "read-only and candidate-only;
// never promotes knowledge"). Only `sensei import` / bootstrap / build / rebuild
// change coverage, and those are CLI stages, not engine-reachable operations.
//
// So coverage-unexamined has no engine-reachable closure actor at all, and must
// be reported as a knowledge limit with its remedy rather than asked of a human.

import (
	"errors"
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/gitx"
)

const (
	unexaminedA = "internal/ghbridge/exchange.go"
	unexaminedB = "docs/architecture/durable-exchange-migration.md"
)

// coverageGap is the routing the W3 run actually produced at 14:41:20.
func coverageGap() Routing {
	return Routing{
		Route:     RouteCloseGap,
		Condition: "graph coverage is absent for planned file(s) the graph has not examined: " + unexaminedA + ", " + unexaminedB,
		Gap: GapIdentity{
			Kind:  "coverage-unexamined",
			Scope: []string{unexaminedA, unexaminedB},
			World: "9d0ec1671c1ac7ed655629f1f273a02dcb7f4365",
		},
	}
}

func coverageAction() Action {
	return Action{
		Files:      []string{unexaminedA, "internal/ghbridge/transport.go", unexaminedB},
		Unexamined: []string{unexaminedA, unexaminedB},
	}
}

// A — after the one allowed narrowing round, an unchanged coverage gap must
// terminate as a knowledge limit, NOT as human authority.
func TestAnUnclosedCoverageGapIsAKnowledgeLimitNotAHumanQuestion(t *testing.T) {
	e := &Engine{SessionID: "s1"}
	routed, limited := e.disposeUnclosedGap("task-1", coverageGap(), coverageAction())

	var limit *knowledgeLimitError
	if !errors.As(limited, &limit) {
		t.Fatalf("an unclosed coverage gap returned %v, want a typed knowledgeLimitError", limited)
	}
	if routed.Route == RouteHuman {
		t.Error("an unclosed coverage gap was routed to human authority; a person cannot make files examined")
	}
	if routed.Basis != BasisLacksKnowledge {
		t.Errorf("basis = %v, want BasisLacksKnowledge", routed.Basis)
	}
}

// B — the stop names the concrete missing files.
func TestTheKnowledgeLimitNamesTheMissingFiles(t *testing.T) {
	e := &Engine{SessionID: "s1"}
	routed, limited := e.disposeUnclosedGap("task-1", coverageGap(), coverageAction())
	if limited == nil {
		t.Fatal("an unclosed coverage gap produced no knowledge limit, so it names no missing files")
	}
	for _, want := range []string{unexaminedA, unexaminedB} {
		if !strings.Contains(limited.Error(), want) {
			t.Errorf("the stop does not name the missing file %s: %v", want, limited)
		}
		if !strings.Contains(routed.Closes, want) {
			t.Errorf("Closes does not name the missing file %s: %q", want, routed.Closes)
		}
	}
	// And it must not name a file that IS examined: a remedy that asks for work
	// already done is not actionable.
	if strings.Contains(routed.Closes, "internal/ghbridge/transport.go") {
		t.Errorf("the remedy names an already-examined file: %q", routed.Closes)
	}
}

// C — the stop names an actionable remedy, in the CLI's real shape.
func TestTheKnowledgeLimitNamesAnActionableRemedy(t *testing.T) {
	e := &Engine{SessionID: "s1", Repo: gitx.Repo{Root: "/src/sensei-code"}}
	routed, limited := e.disposeUnclosedGap("task-1", coverageGap(), coverageAction())
	if limited == nil {
		t.Fatal("an unclosed coverage gap produced no knowledge limit, so it names no remedy")
	}
	for _, want := range []string{"sensei import --refresh", "/src/sensei-code", "--domain"} {
		if !strings.Contains(routed.Closes, want) {
			t.Errorf("the remedy omits %q: %q", want, routed.Closes)
		}
	}
	// `import --refresh` takes a CHECKOUT PATH, not a file list. A remedy that
	// passed files would not run.
	if strings.Contains(routed.Closes, "--refresh "+unexaminedA) {
		t.Errorf("the remedy passes files to --refresh, which takes a checkout path: %q", routed.Closes)
	}
	// It must not claim the remedy is safe or authorised; the engine only reports
	// what would close the limit.
	for _, forbidden := range []string{"safe to run", "automatically", "will be run"} {
		if strings.Contains(strings.ToLower(routed.Closes), forbidden) {
			t.Errorf("the remedy claims authorisation it does not have (%q): %q", forbidden, routed.Closes)
		}
	}
}

// D — no human is asked to authorise implementation over absent coverage.
func TestNoHumanIsAskedToSupplyMissingCoverage(t *testing.T) {
	e := &Engine{SessionID: "s1"}
	routed, limited := e.disposeUnclosedGap("task-1", coverageGap(), coverageAction())
	if limited == nil {
		t.Fatal("the run continued to a human question over absent coverage")
	}
	if routed.Route == RouteHuman {
		t.Fatal("route = human-authority-required over absent coverage")
	}
	// The condition must not be reworded into an authorisation question.
	if strings.Contains(strings.ToLower(limited.Error()), "authorize") ||
		strings.Contains(strings.ToLower(limited.Error()), "proceed with the gap still open") {
		t.Errorf("the knowledge limit is phrased as an authorisation request: %v", limited)
	}
}

// The gap TYPE decides the owner. An unverified premise is reasoning work the
// architect genuinely owns, and must still become a human question when its
// round is spent — not a knowledge limit.
func TestAnUnverifiedPremiseStillReachesTheHuman(t *testing.T) {
	e := &Engine{SessionID: "s1"}
	premise := Routing{
		Route:     RouteCloseGap,
		Condition: "the plan rests on an unverified premise about reviewer W3 migration design",
		Gap:       GapIdentity{Kind: "unverified-premise", Subject: "a premise", Scope: []string{unexaminedA}},
	}
	routed, limited := e.disposeUnclosedGap("task-2", premise, coverageAction())
	if limited != nil {
		t.Fatalf("an unverified premise was classified as a knowledge limit: %v", limited)
	}
	if routed.Route != RouteHuman {
		t.Errorf("route = %v, want human-authority-required for an unsettled premise", routed.Route)
	}
	if !strings.Contains(routed.Condition, "was not closed by investigation") {
		t.Errorf("the escalated condition lost its unclosed marker: %q", routed.Condition)
	}
}

// A gap with no unexamined files left is not a coverage limit, whatever its kind
// says: the architect narrowed onto examined material and the run may continue.
func TestANarrowedFullyExaminedPlanIsNotAKnowledgeLimit(t *testing.T) {
	e := &Engine{SessionID: "s1"}
	narrowed := Action{Files: []string{"internal/ghbridge/transport.go"}}
	routed, limited := e.disposeUnclosedGap("task-3", coverageGap(), narrowed)
	if limited != nil {
		t.Fatalf("a plan with no unexamined files was stopped as lacking knowledge: %v", limited)
	}
	if routed.Route != RouteHuman {
		t.Errorf("route = %v; with coverage present this is an ordinary escalation", routed.Route)
	}
}

// The engine must not invoke anything that mutates the authoritative graph. The
// run's certified graph digest has to stay stable, so acquisition is reported and
// never performed.
func TestTheEngineNeverMutatesTheGraphToCloseAGap(t *testing.T) {
	for _, file := range []string{"authority.go", "engine.go", "premise.go"} {
		src := rawSource(t, "internal/workflow/"+file)
		for _, forbidden := range []string{
			`"import"`, `"bootstrap"`, `"rebuild"`, `"derive"`,
			`exec.Command("sensei"`, `awareness_promote`, `promoteCandidate`,
		} {
			if strings.Contains(src, forbidden) {
				t.Errorf("%s reaches %s; a governed run must not change the graph it is governed by", file, forbidden)
			}
		}
	}
}

// Both escalation sites must dispose through the typed owner, so neither can
// keep its own idea of what an unclosed gap becomes.
func TestBothEscalationSitesDisposeThroughTheTypedOwner(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	if n := strings.Count(src, "e.disposeUnclosedGap("); n != 2 {
		t.Errorf("disposeUnclosedGap is called %d times; both unclosed-gap sites must use it", n)
	}
	if strings.Count(src, `routing.Route = RouteHuman`) != 0 {
		t.Error("an escalation site still sets RouteHuman directly, bypassing the typed owner")
	}
}

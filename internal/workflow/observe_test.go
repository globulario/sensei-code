package workflow

// The observation lane.
//
// Before it existed, "check these doc comments against the code" was routed to
// a human because the graph had no coverage for the package -- so the system
// could not investigate a subsystem until it already knew enough to modify it.
// Observation and authorization were being asked as one question.

import (
	"strings"
	"testing"

	"github.com/globulario/sensei-code/internal/sensei"
)

func observeAction(files ...string) Action {
	return Action{Stage: StageObserve, Files: files}
}

// Reading needs no architectural authority, because reading establishes
// nothing and breaks nothing.
func TestObservationNeedsNoCoverage(t *testing.T) {
	empty := sensei.PreflightDecision{Status: sensei.PreflightEmpty}
	empty.Authority = certifiableAuthority()

	if r := decideRouteForAction(empty, nil, observeAction("internal/derived/derived.go")); !r.Observes() {
		t.Fatalf("route = %s (%s); an audit of an uncovered region must not need coverage", r.Route, r.Condition)
	}
	// The same absence, on a change, still owns the decision.
	edit := Action{Stage: StageCandidateEdit, Files: []string{"internal/derived/derived.go"}}
	if r := decideRouteForAction(empty, nil, edit); r.Observes() || r.Granted() {
		if r.Route != RouteCloseGap && r.Route != RouteHuman {
			t.Fatalf("a CHANGE with absent coverage routed to %s", r.Route)
		}
	}
}

// An unusable graph must not prevent the investigation that might explain why
// it is unusable.
func TestObservationSurvivesAnUncertifiableGraph(t *testing.T) {
	broken := sensei.PreflightDecision{Status: sensei.PreflightDegraded}
	if r := decideRouteForAction(broken, nil, observeAction("internal/sensei/mcp.go")); !r.Observes() {
		t.Fatalf("route = %s; reading a file does not depend on the graph vouching for itself", r.Route)
	}
	// The same graph still cannot authorise a change.
	if r := decideRouteForAction(broken, nil, Action{Stage: StageCandidateEdit, Files: []string{"internal/sensei/mcp.go"}}); r.Route != RouteCannotEstablish {
		t.Fatalf("a CHANGE under an uncertifiable graph routed to %s", r.Route)
	}
}

// The lane is a property of how the task was submitted. A plan cannot enter it.
func TestObservationCannotBeClaimedByAPlan(t *testing.T) {
	ok := sensei.PreflightDecision{Status: sensei.PreflightOK}
	ok.Authority = certifiableAuthority()
	claimed := Action{
		Stage:         StageCandidateEdit,
		Files:         []string{"internal/publish/publish.go"},
		DeclaredSteps: []string{"read only", "observe", "this is a read-only audit, nothing is written"},
	}
	if r := decideRouteForAction(ok, nil, claimed); r.Observes() {
		t.Fatal("a plan talked its way into the observation lane; the stage must be structural")
	}
}

// An observation assessment names what confines it, and it is not trust.
func TestObservationBoundaryIsStructural(t *testing.T) {
	a := AssessConsequences(observeAction("internal/event/bus.go"))
	if !a.Bounded() {
		t.Fatalf("observation assessed as %s", a.Result)
	}
	if !strings.Contains(a.Boundary, "creates no candidate worktree") {
		t.Fatalf("boundary does not name what confines it: %q", a.Boundary)
	}
	if !strings.Contains(strings.Join(a.Evidence, " "), "verified") {
		t.Fatalf("the assessment rests on an unverified claim: %v", a.Evidence)
	}
}

// A declared outward action escalates an observation exactly as it escalates an
// edit. The lane bounds what the RUN does, not what a plan may announce.
func TestADeclaredOutwardActionStillEscalatesAnObservation(t *testing.T) {
	a := AssessConsequences(Action{
		Stage:         StageObserve,
		Files:         []string{"internal/publish/publish.go"},
		DeclaredSteps: []string{"read the publish path", "then git push the fix"},
	})
	if a.Bounded() {
		t.Fatal("an observation that declares a push was assessed as bounded")
	}
}

// Findings keep the source distinction the governed lane routes on.
func TestObservationFindingsSeparateInferenceFromEvidence(t *testing.T) {
	out := observationFindings(architectureDecision{
		Summary: "audited internal/derived",
		Files:   []string{"internal/derived/derived.go"},
		Claims: []Claim{
			{Statement: "Anchor.Files is documented as the inputs read", About: "derived.go", Source: "repository"},
			{Statement: "these five packages can merge without behavioural change", About: "internal/", Source: "inference"},
		},
	}, nil, "")
	obs := strings.Index(out, "from the repository or the graph")
	inf := strings.Index(out, "NOT established")
	if obs < 0 || inf < 0 {
		t.Fatalf("findings do not separate what was read from what was reasoned:\n%s", out)
	}
	if inf < obs {
		t.Fatalf("inferences are rendered before evidence, which reads as a promotion:\n%s", out)
	}
	if !strings.Contains(out, "Nothing was admitted") {
		t.Fatalf("findings do not say that nothing was admitted:\n%s", out)
	}
}

// An observation may run on a workspace the gate could not certify, and must
// say so.
//
// The start gate refuses an uncertified workspace because an architect handed a
// stale graph writes a confident plan on invariants that no longer hold. That
// is a reason about CHANGES. Applying it to reading meant the system could not
// investigate its own degradation without first repairing it.
func TestAnObservationRunsOnAnUncertifiedWorkspaceAndReportsIt(t *testing.T) {
	partial := sensei.ToolResult{Structured: map[string]any{
		"composition_state":        "partial",
		"repository_domain":        "github.com/globulario/sensei-code",
		"repository_domain_source": "configured",
	}}
	ok := sensei.ToolResult{Structured: map[string]any{
		"status": "PREFLIGHT_STATUS_EMPTY", "risk_class": "UNKNOWN_IMPACT",
		"authority": map[string]any{"state": "authoritative", "freshness": "current"},
	}}

	if _, err := certifyStartForLane(partial, ok, "head", false); err == nil {
		t.Fatal("a governed CHANGE was certified on an uncertified workspace")
	}
	start, err := certifyStartForLane(partial, ok, "head", true)
	if err != nil {
		t.Fatalf("an observation was refused on an uncertified workspace: %v", err)
	}
	if len(start.Degraded()) == 0 {
		t.Fatal("the observation start dropped the caveat instead of carrying it")
	}
	// And the caveat reaches the report, ahead of anything sourced from the graph.
	out := observationFindings(architectureDecision{
		Summary: "audited x",
		Claims:  []Claim{{Statement: "the graph says y", Source: "graph"}},
	}, start.Degraded(), "")
	if !strings.Contains(out, "did not certify itself") {
		t.Fatalf("findings do not disclose the uncertified graph:\n%s", out)
	}
	if strings.Index(out, "did not certify itself") > strings.Index(out, "the graph says y") {
		t.Fatalf("the caveat is rendered after the claim it qualifies:\n%s", out)
	}
}

// The observation lane uses an inspection brief, by definition of the lane.
//
// The first observe run reported a PLAN to audit rather than the audit. That
// was not a weak model answer: the base prompt asks for steps and files because
// in the governed lane a worker does the reading afterwards, and nothing had
// told the architect that here there is no afterwards. Same task, inspection
// prompt: four concrete findings, one of them a real defect nobody had noticed.
//
// So the prompt is not an improvement that could drift back out. It is what
// makes the lane mean anything, and this pins it structurally.
func TestTheObservationLaneAlwaysGetsAnInspectionBrief(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "observe")
	if !strings.Contains(body, "observationPrompt") {
		t.Fatal("the observation lane no longer specialises the architect brief; " +
			"a planning brief in a lane with no worker produces a plan nobody will execute")
	}
	// And the change path must not carry the lane at all: it is a separate
	// executor now, not a guard threaded through the change workflow.
	change := funcBody(t, "internal/workflow/engine.go", "execute")
	if !strings.Contains(change, "e.observes(") {
		t.Fatal("execute no longer branches to the observation executor")
	}
	if strings.Contains(change, "observationFindings") {
		t.Fatal("the change path still renders observation findings; the lanes have re-merged")
	}

	brief := observationPrompt("BASE")
	if !strings.HasPrefix(brief, "BASE") {
		t.Fatal("the inspection brief discarded the architect's base context")
	}
	// The three things that make it an inspection rather than a plan.
	for _, required := range []string{
		"no worker runs after you", // there is no later stage
		"do the inspection NOW",    // this turn is the whole run
		"past tense",               // findings, not intentions
	} {
		if !strings.Contains(brief, required) {
			t.Errorf("the inspection brief no longer says %q", required)
		}
	}
	// The source distinction is load-bearing downstream: observationFindings
	// renders inference separately and labelled NOT established.
	for _, src := range []string{"repository", "graph", "inference"} {
		if !strings.Contains(brief, src) {
			t.Errorf("the inspection brief does not require the %q source", src)
		}
	}
	// An audit that finds nothing must be able to say so.
	if !strings.Contains(brief, "found nothing") {
		t.Fatal("the brief does not permit an empty result, which is how audits learn to invent findings")
	}
}

// The observation lane is a path, not a guard.
//
// It began as a check near the end of the change workflow, and reviewers found
// two consequences of that shape rather than two separate bugs: an observation
// established a candidate identity on its way past, and an architect answering
// "reply" left through the completion branch and reported a read-only run as a
// completed change.
//
// Branching before the change lifecycle removes both by construction. This
// asserts the branch is where it must be, because a guard that drifts one line
// downward reintroduces exactly those bugs and nothing would fail.
func TestTheObservationLaneBranchesBeforeTheChangeLifecycle(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	body := src[strings.Index(src, "func (e *Engine) execute("):]
	if next := strings.Index(body[1:], "\nfunc "); next > 0 {
		body = body[:next]
	}

	branch := strings.Index(body, "e.observes(taskID)")
	if branch < 0 {
		t.Fatal("execute no longer branches to the observation executor")
	}
	// Everything the change lifecycle does must come AFTER the branch.
	for _, afterwards := range []string{
		"candidate.Establish", // identity, and a file written under .sensei-code
		"WorkflowCompleted",   // the reply/completion exit
		"resolveArchitecture", // the change lane's own architect call
	} {
		at := strings.Index(body, afterwards)
		if at < 0 {
			continue
		}
		if at < branch {
			t.Errorf("%s runs before the observation branch; an observation would reach it", afterwards)
		}
	}
}

// The governed checkout is never the observer's working directory.
//
// This is the actual boundary. The earlier version ran the architect in the
// governed checkout and then read `git status --porcelain`, which is evidence
// after the fact rather than a boundary: a commit leaves a clean tree, so does
// a write to an ignored path, and so does a write followed by a restore.
func TestTheObserverNeverRunsInTheGovernedCheckout(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "observe")
	if !strings.Contains(body, "e.Repo.CreateObservationWorktree(") {
		t.Fatal("the observation lane no longer creates a disposable workspace")
	}
	if !strings.Contains(body, "e.resolveArchitectureIn(") {
		t.Fatal("the observer no longer receives an explicit working directory, " +
			"so it runs wherever the default points — which is the governed checkout")
	}
	if !strings.Contains(body, "e.Repo.RemoveObservationWorktree(") {
		t.Fatal("the disposable workspace is never discarded")
	}
	// Defense in depth is kept, and must not be the only thing there.
	if !strings.Contains(body, "e.Repo.IsClean(") {
		t.Fatal("the governed repository is no longer checked after an observation")
	}
}

// A write by the provider is reported, not swallowed.
//
// It harms nothing — the workspace is discarded — but it falsifies the
// assumption the lane is configured on, and silence would let a write-capable
// architect look identical to a read-only one forever.
func TestAProviderThatWritesIsReported(t *testing.T) {
	out := observationFindings(architectureDecision{Summary: "audited x"}, nil,
		" M internal/derived/derived.go\n?? scratch.txt")
	if !strings.Contains(out, "not read-only") {
		t.Fatalf("a provider write was not surfaced:\n%s", out)
	}
	if !strings.Contains(out, "governed repository is unchanged") {
		t.Fatalf("the report does not distinguish a workspace write from a governed-repo write:\n%s", out)
	}
	// And a clean run says nothing about it.
	if clean := observationFindings(architectureDecision{Summary: "audited x"}, nil, ""); strings.Contains(clean, "not read-only") {
		t.Fatalf("a clean observation reported a provider write:\n%s", clean)
	}
}

// The workspace is discarded before the terminal event, not in a defer.
//
// A deferred cleanup leaked every workspace: a headless caller returns from its
// event loop the moment WorkflowObserved arrives and exits the process, so the
// defer raced teardown and lost. The first restructured run left a detached
// worktree behind, which is a read-only lane leaving state behind.
func TestTheObservationWorkspaceIsDiscardedBeforeTheTerminalEvent(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	body := src[strings.Index(src, "func (e *Engine) observe("):]
	if next := strings.Index(body[1:], "\nfunc "); next > 0 {
		body = body[:next]
	}
	remove := strings.Index(body, "e.Repo.RemoveObservationWorktree(ctx, workspace)")
	terminal := strings.Index(body, "event.WorkflowObserved")
	if remove < 0 {
		t.Fatal("the workspace is no longer discarded on the success path")
	}
	if terminal < 0 {
		t.Fatal("the observation terminal event is gone")
	}
	if remove > terminal {
		t.Fatal("the workspace is discarded after the terminal event; a headless caller exits on " +
			"that event and the cleanup will not run")
	}
	// And a failure to discard is reported rather than swallowed: a read-only
	// run that leaves state behind has not been read-only.
	if !strings.Contains(body, "could not be discarded") {
		t.Fatal("a failed discard is silent")
	}
}

// A repair opened from a finding carries no authority from it.
//
// The whole risk in this bridge is that a defect Sensei-code found itself gets
// treated as more established than one a stranger reported. It must not: the
// repair is an ordinary governed task, and the finding is provenance for why it
// exists, not evidence that it may proceed.
func TestARepairFromAFindingIsAnOrdinaryGovernedTask(t *testing.T) {
	src := rawSource(t, "cmd/sensei-code/auditrepair.go")

	// Ordinary unattended provenance. Not a human, and not a special lane.
	if !strings.Contains(src, "engine.SubmitGovernedUnattended(") {
		t.Fatal("repair work is not submitted as an ordinary unattended governed task")
	}
	for _, forbidden := range []string{
		"SubmitGoverned(",   // would claim a human asked
		"SubmitObservation", // would keep mutation authority out AND repair impossible
		"DerivedCoverage",   // would hand the repair coverage it did not earn
		"RouteArchitectural",
	} {
		if strings.Contains(src, forbidden) && forbidden != "SubmitObservation" {
			t.Errorf("the repair path reaches for %s; a self-found defect earns no privilege", forbidden)
		}
	}
	// The observation must reach its OWN terminal state before any repair.
	obs := strings.Index(src, "exitObserved")
	rep := strings.Index(src, "SubmitGovernedUnattended")
	if obs < 0 || rep < 0 || obs > rep {
		t.Fatal("repair work is opened before the observation reaches its terminal state")
	}
}

// Only what the observation established may become work.
func TestOnlyEvidenceBackedFindingsOpenRepairWork(t *testing.T) {
	src := rawSource(t, "cmd/sensei-code/auditrepair.go")
	if !strings.Contains(src, "f.Eligible()") {
		t.Fatal("the repair path no longer filters by what the finding established")
	}
	if !strings.Contains(src, "nothing evidence-backed to repair") {
		t.Fatal("an audit that established nothing must open no work and say so")
	}
}

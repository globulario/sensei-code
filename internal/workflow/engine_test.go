package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/roles"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/decision"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/taskstate"
)

func TestDecodeModelJSON(t *testing.T) {
	var got architectureDecision
	err := decodeModelJSON("```json\n{\"decision\":\"proceed\",\"summary\":\"ok\",\"plan\":\"edit x\"}\n```", &got)
	if err != nil {
		t.Fatal(err)
	}
	if got.Decision != "proceed" || got.Plan != "edit x" {
		t.Fatalf("unexpected decision: %#v", got)
	}
}

func TestDecodeModelJSONRejectsProse(t *testing.T) {
	var got architectureDecision
	if err := decodeModelJSON("no bounded response", &got); err == nil {
		t.Fatal("expected malformed model response to fail closed")
	}
}

func TestArchitectureOptionsRoundTrip(t *testing.T) {
	in := `{"decision":"escalate","summary":"policy","human_question":"choose","recommendation":"1","options":[{"id":"1","label":"preserve"},{"id":"2","label":"change"}]}`
	var got architectureDecision
	if err := decodeModelJSON(in, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Options) != 2 || got.Options[0] != (authority.Option{ID: "1", Label: "preserve"}) {
		t.Fatalf("unexpected options: %#v", got.Options)
	}
}

// What an option does is carried on the option, not read out of its wording.
// The label may say anything at all; only the outcome decides.
func TestAnOptionsEffectComesFromItsOutcomeNotItsWording(t *testing.T) {
	stopping := authority.Option{Label: "Continue happily forever", Outcome: authority.Stop}
	if stopping.Outcome != authority.Stop {
		t.Fatal("an option's outcome is its own")
	}
	if stopping.Outcome.Permits() || stopping.Outcome.Settles() != true {
		t.Fatalf("stop settles the condition and permits nothing: %+v", stopping.Outcome)
	}
	authorizing := authority.Option{Label: "stop cancel abort", Outcome: authority.Authorize}
	if !authorizing.Outcome.Permits() {
		t.Fatal("wording that reads like a refusal must not override an authorize outcome")
	}
	// Revise is the one outcome that leaves the condition open, so a redesign
	// is routed on its own merits rather than refused from the record.
	if authority.Revise.Settles() {
		t.Fatal("revise must not settle the condition")
	}
	if authority.Revise.Permits() {
		t.Fatal("revise must not permit the change either")
	}
}

func testContext() taskContext {
	return taskContext{
		Task:            "add a --version flag",
		Conversation:    "HUMAN: can we version this?\nYOU: yes, a conventional flag",
		WorkspaceStatus: "composition_state: complete",
		Preflight:       "risk_class: UNKNOWN_IMPACT",
		Rationale:       "conventional flag, no governance change",
		Steps:           []string{"locate the CLI construction", "print and exit"},
	}
}

func TestImplementationPromptCarriesContextWithoutWideningScope(t *testing.T) {
	got := implementationPrompt(testContext(), "edit main.go", "", 1, nil, "")
	for _, want := range []string{
		"can we version this?",             // the conversation
		"conventional flag, no governance", // the architect's reasoning
		"1. locate the CLI construction",   // the plan steps
		"composition_state: complete",      // Sensei evidence
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("implementation prompt is missing %q", want)
		}
	}
	// Context explains why; it must never read as permission to do more.
	if !strings.Contains(got, "They do NOT widen your scope") {
		t.Fatal("implementation prompt dropped the scope boundary")
	}
}

func TestReviewPromptCarriesContextWithoutLoweringTheBar(t *testing.T) {
	got := reviewPrompt(testReviewPacket(testContext(), "edit main.go", "diff --git a b", "audit says fine", "passed go test ./..."))
	for _, want := range []string{"can we version this?", "conventional flag, no governance", "audit says fine"} {
		if !strings.Contains(got, want) {
			t.Fatalf("review prompt is missing %q", want)
		}
	}
	if !strings.Contains(got, "Context does not\nlower the bar") {
		t.Fatal("review prompt dropped the standard-of-proof guard")
	}
}

func TestTaskContextIntentIsExplicitWhenEmpty(t *testing.T) {
	if got := (taskContext{}).intent(); !strings.Contains(got, "no additional rationale") {
		t.Fatalf("empty intent = %q, want an explicit statement of absence", got)
	}
}

func TestGuidanceReachesTheWorkerWithoutEnlargingThePlan(t *testing.T) {
	got := implementationPrompt(testContext(), "edit main.go", "", 2,
		[]string{"use the existing version constant, do not add a package"}, "")
	if !strings.Contains(got, "use the existing version constant") {
		t.Fatal("the human's guidance did not reach the worker")
	}
	if !strings.Contains(got, "takes precedence over your own") {
		t.Fatal("guidance must outrank the worker's own judgement about how to implement")
	}
	if !strings.Contains(got, "does not silently enlarge the") {
		t.Fatal("guidance must not become a way to grow the approved plan unnoticed")
	}
}

func TestNoteQueuesOnlyForARealTask(t *testing.T) {
	e := &Engine{}
	if e.Note("", "hello") {
		t.Fatal("guidance was accepted for no task, so nothing would ever read it")
	}
	if e.Note("task-1", "   ") {
		t.Fatal("empty guidance was accepted")
	}
	if !e.Note("task-1", "prefer the simpler shape") {
		t.Fatal("guidance for a running task was refused")
	}
	if got := e.takeNotes("task-1"); len(got) != 1 || got[0] != "prefer the simpler shape" {
		t.Fatalf("takeNotes = %v, want the queued guidance", got)
	}
	if got := e.takeNotes("task-1"); len(got) != 0 {
		t.Fatalf("guidance was delivered twice: %v", got)
	}
}

// TestHandoverTellsTheNextWorkerWhatWasLeftBehind carries forward the
// guarantees the old prose handover note made, now that the handover is
// assembled from semantic state instead of written as a paragraph. The
// properties are the same; what changed is that they are now facts with a
// shape rather than sentences the next worker has to parse.
func TestHandoverTellsTheNextWorkerWhatWasLeftBehind(t *testing.T) {
	state := taskstate.State{
		TaskID: "task-1", Task: "make the counts honest", Phase: taskstate.Revising,
		GraphBuildCommit: "gen-1",
	}
	state.OpenFindings(openFindings(
		"REVISE: counts are presented as exact",
		"",
		errors.New("did not converge after 3 review cycles"),
	))
	note := state.Handover("claude", "gen-1")

	for _, want := range []string{
		"did not converge",              // why it stopped
		"changes are present",           // the work is not gone
		"Do not start over",             // continue rather than restart
		"counts are presented as exact", // the unresolved finding
	} {
		if !strings.Contains(note, want) {
			t.Fatalf("handover is missing %q:\n%s", want, note)
		}
	}
}

func TestHandoverEntersTheNextWorkerAsUnansweredFeedback(t *testing.T) {
	got := implementationPrompt(testContext(), "plan", "the previous worker left this unresolved", 1, nil, "")
	if !strings.Contains(got, "the previous worker left this unresolved") {
		t.Fatal("a handover did not reach the next worker's first cycle")
	}
}

// A post-creation prospective refutation is terminal. It is not review
// feedback another implementor may reinterpret or retry.
func TestAProspectiveSurfaceRefutationStopsBeforeHandoff(t *testing.T) {
	if !isProspectiveSurfaceRefutation(errors.New("prospective surface refuted: package mismatch")) {
		t.Fatal("a prospective refutation was not recognized")
	}
	if isProspectiveSurfaceRefutation(errors.New("candidate validation failed")) {
		t.Fatal("an ordinary candidate failure was treated as a prospective refutation")
	}

	body := rawSource(t, "internal/workflow/engine.go")
	refutation := strings.Index(body, "isProspectiveSurfaceRefutation")
	handoff := strings.Index(body, "handoffPacket")
	if refutation < 0 || handoff < 0 {
		t.Fatal("the prospective terminal branch or ordinary handoff path is missing")
	}
	if refutation > handoff {
		t.Fatal("a prospective refutation reaches handoff before the terminal branch")
	}
	terminal := body[refutation:handoff]
	if !strings.Contains(terminal, "fail(err)") || !strings.Contains(terminal, "return") {
		t.Fatal("a prospective refutation does not fail the run before another implementor can be assigned")
	}
}

func TestDecisionReferencesFilesTheCandidateActuallyChanged(t *testing.T) {
	// At approval the architect can only name files it intends to create, and a
	// decision referencing a file the task never produced references nothing.
	diff := `diff --git a/cmd/main.go b/cmd/main.go
--- a/cmd/main.go
+++ b/cmd/main.go
@@ -1 +1,2 @@
+added
diff --git a/internal/gone.go b/internal/gone.go
deleted file mode 100644
--- a/internal/gone.go
+++ /dev/null
`
	got := changedPaths(diff)
	if len(got) != 1 || got[0] != "cmd/main.go" {
		t.Fatalf("changedPaths = %v, want only the file that exists afterwards", got)
	}
}

func TestEveryRoleIsToldItCanReadTheSameGraph(t *testing.T) {
	// Convergence depends on all three roles consulting one source. Only the
	// architect used to be told the tools existed; a worker that does not know
	// it can ask forms its own view of the code instead.
	prompts := map[string]string{
		"architect": architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "", ""),
		"worker":    implementationPrompt(testContext(), "plan", "", 1, nil, ""),
		"reviewer":  reviewPrompt(testReviewPacket(testContext(), "plan", "diff", "audit", "evidence")),
	}
	for role, prompt := range prompts {
		if !strings.Contains(prompt, "awareness_briefing") {
			t.Fatalf("the %s is never told it can read the graph", role)
		}
		if !strings.Contains(prompt, "sensei_workspace_status") {
			t.Fatalf("the %s is not told which workspace it is in", role)
		}
	}
	for _, role := range []string{"worker", "reviewer"} {
		if !strings.Contains(prompts[role], "same graph") {
			t.Fatalf("the %s is not told the other roles read the same graph", role)
		}
	}
}

// TestArchitectConversationPromptIsHumanFacing keeps #8's guarantees for the
// assisted turn after the two conversation implementations were reconciled into
// one. The prompt builder changed; what it must promise the human did not.
func TestArchitectConversationPromptIsHumanFacing(t *testing.T) {
	got := assistedPrompt("/repo", "example.com/x", "ChatGPT", "Should this boundary move?", "",
		nil, "workspace evidence", "preflight evidence", "(none)", "(none)", "(none)")
	for _, want := range []string{
		"speaking directly with the human",
		"precise,\nconcrete, technically rich",
		"LIVE SENSEI WORKSPACE AUTHORITY",
		"workspace evidence",
		"preflight evidence",
		"/run",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation prompt missing %q", want)
		}
	}
	if strings.Contains(got, "Return ONLY JSON") {
		t.Fatal("human-facing architect conversation must not be compressed into the machine JSON contract")
	}
	// Routine judgement stays with the architect: an assistant that asks
	// permission for ordinary choices is a worse collaborator than one that
	// decides and explains.
	if !strings.Contains(strings.Join(strings.Fields(got), " "), "Routine architectural judgment is yours to make") {
		t.Fatal("the architect is not told that routine judgement is its own")
	}
}

// TestReviewerSeesExecutedEvidenceNotAWorkerReport closes the gap the canary
// found. Three review cycles refused the same candidate for want of gofmt, vet
// and test results, and the workflow had no way to carry them, so the worker
// re-emitted a byte-identical diff until the run timed out.
func TestReviewerSeesExecutedEvidenceNotAWorkerReport(t *testing.T) {
	got := reviewPrompt(testReviewPacket(testContext(), "plan", "diff", "audit", "passed  go test ./...\n  exit 0"))
	if !strings.Contains(got, "VALIDATION EVIDENCE") {
		t.Fatal("the reviewer is never shown the validation evidence")
	}
	if !strings.Contains(got, "go test ./...") {
		t.Fatal("the evidence itself did not reach the reviewer")
	}
	// The reviewer must be told why it can rely on this, or it will treat it as
	// one more thing an agent asserted.
	if !strings.Contains(got, "not by the worker reporting") {
		t.Error("the prompt does not distinguish executed evidence from a worker's claim")
	}
	// And must not read an unrun check as satisfied. Matched on normalised
	// whitespace: the prompt is hard-wrapped and a reflow must not silently
	// drop the guarantee.
	if !strings.Contains(strings.Join(strings.Fields(got), " "), "did not run and proves nothing") {
		t.Error("the prompt does not tell the reviewer that a not-permitted check proves nothing")
	}
}

// TestRunDoesNotAskForRoutinePlanApproval is the executable half of
// sensei_code.workflow.execution_is_authorized_once_at_run.
//
// Typing /run authorizes the task. The plan that follows is published as
// information, and nothing between it and the worker may ask the human to
// authorize what they already authorized. The check is structural because the
// property is about what the code cannot do: the governed run has exactly two
// paths that put a decision to a person, and neither of them sits here.
func TestRunDoesNotAskForRoutinePlanApproval(t *testing.T) {
	// The governed run is entered through run and carried out by execute; the
	// property is about the path, so both are read.
	entry := funcBody(t, "internal/workflow/engine.go", "run")
	if !strings.Contains(entry, "execute") {
		t.Fatal("run no longer delegates to execute; this test would be reading the wrong function")
	}
	body := entry + " " + funcBody(t, "internal/workflow/engine.go", "execute")

	// The plan is still shown. Removing the ceremony must not remove the
	// information: a human who cannot see the plan cannot decide to stop.
	if !strings.Contains(body, "event.PlanProposed") {
		t.Error("the run no longer publishes the plan, so the human cannot see what was authorized")
	}
	if strings.Contains(body, "approvePlan") {
		t.Error("the plan-approval rendezvous is back in run()")
	}
	file := fileText(t, "internal/workflow/engine.go")
	for _, gone := range []string{`"Implement this plan"`, `"the human declined the proposed plan"`} {
		if strings.Contains(file, gone) {
			t.Errorf("the approval prompt survives somewhere in the engine: %s", gone)
		}
	}
	// awaitChoice is how a decision is put to a human. In the governed run it
	// must be reachable only through the router's escalation and through
	// publication, both of which live in their own functions.
	if strings.Contains(body, "awaitChoice") || strings.Contains(body, "awaitHuman") {
		t.Error("run() blocks on a human decision directly; only the authority router and publication may")
	}
}

// TestOnlyTheAuthorityRouterCreatesALevel3Stop states the boundary that keeps
// autonomy structural rather than cultural: a Level-3 interruption is produced
// by evidence, not by anyone's discretion.
//
// Publication is the one deliberate exception, and it is a different decision —
// it authorizes reaching the repository's shared history, which no
// certification grants.
func TestOnlyTheAuthorityRouterCreatesALevel3Stop(t *testing.T) {
	allowed := map[string]bool{
		"awaitHuman":       true, // the router's escalation
		"offerPullRequest": true, // publication, which is human-owned by contract
	}
	for _, fn := range functionsIn(t, "internal/workflow/engine.go") {
		body := funcBody(t, "internal/workflow/engine.go", fn)
		if strings.Contains(body, "authority.Human") && !allowed[fn] {
			t.Errorf("%s constructs a Level-3 decision; only the authority router and publication may", fn)
		}
		// Naming the allowed constructors is not enough on its own: what makes
		// the escalation evidence-driven is that it is only ever reached from a
		// routing verdict. A function that asks the human without having asked
		// Sensei first is discretion wearing the router's clothes.
		if fn != "awaitHuman" && strings.Contains(body, "awaitHuman") && !strings.Contains(body, "routePlan") {
			t.Errorf("%s escalates to a human without routing the question first", fn)
		}
	}
	// And the router itself must reach it only from a routing verdict.
	router := fileText(t, "internal/workflow/authority.go")
	if !strings.Contains(router, "RouteHuman") {
		t.Fatal("the router no longer produces a human route at all")
	}
}

// TestAGovernedRunCanBeStopped is the other half of removing the approval
// prompt. The prompt was also the only place a human could say no; taking it
// away without an interrupt would have removed their control while claiming to
// remove their burden.
func TestAGovernedRunCanBeStopped(t *testing.T) {
	e := &Engine{}
	stopped := false
	e.mu.Lock()
	e.stops = map[string]context.CancelFunc{"task-1": func() { stopped = true }}
	e.mu.Unlock()

	if !e.Stoppable("task-1") {
		t.Fatal("a running governed task reports as unstoppable")
	}
	if !e.Stop("task-1") {
		t.Fatal("Stop refused a running task")
	}
	if !stopped {
		t.Fatal("Stop reported success without cancelling the run")
	}
	if e.Stop("task-1") {
		t.Error("stopping an already-stopped task reported success, so the UI would claim it killed something twice")
	}
	if e.Stoppable("task-1") {
		t.Error("a stopped task still reports as stoppable")
	}
	if e.Stop("never-existed") {
		t.Error("Stop claimed to stop a task that was never running")
	}
}

// TestAStoppedRunIsReportedAsStoppedNotFailed keeps the distinction the
// behavioural record depends on. A stop proves nothing about the work; filing
// it as a failure would teach the project that this task shape breaks, and
// would make the candidate unresumable.
func TestAStoppedRunIsReportedAsStoppedNotFailed(t *testing.T) {
	bus := event.NewBus()
	events, done := bus.Subscribe(16)
	defer done()

	e := &Engine{Bus: bus, SessionID: "s1"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// ReadRepository is not granted, so the run refuses immediately and takes
	// the failure path with an already-cancelled context — which is exactly the
	// shape a stop produces, without needing a live Sensei or a worker.
	e.run(ctx, "task-1", "do something", RequestedByHuman)

	var kinds []event.Kind
	for {
		select {
		case ev := <-events:
			kinds = append(kinds, ev.Kind)
			continue
		default:
		}
		break
	}
	var sawStopped, sawFailed bool
	for _, k := range kinds {
		switch k {
		case event.WorkflowStopped:
			sawStopped = true
		case event.WorkflowFailed:
			sawFailed = true
		}
	}
	if !sawStopped {
		t.Errorf("a stopped run emitted no stop transition: %v", kinds)
	}
	if sawFailed {
		t.Errorf("a stopped run was also reported as failed: %v", kinds)
	}
}

// TestDecisionRecordNamesTheRealAuthorityOwner pins the provenance the durable
// record carries. With Level-2 work flowing without a human rendezvous, a
// record claiming human acceptance would be false about the one thing a future
// agent reads it for.
func TestDecisionRecordNamesTheRealAuthorityOwner(t *testing.T) {
	e := &Engine{}
	e.Config.Architect.Name = "chatgpt"
	e.recordObjective("task-1", Objective{Text: "t", Provenance: RequestedByHuman})

	// No Level-3 condition was answered during this task.
	got := e.decisionAuthority("task-1", certifiedStart{})
	if got.Owner != decision.Architectural {
		t.Fatalf("an uninterrupted run recorded owner %q, want architectural", got.Owner)
	}
	if !strings.Contains(got.HumanGrant, "/run") {
		t.Errorf("the record does not say what the human actually authorized: %+v", got)
	}
	if got.Condition != "" || got.Resolution != "" {
		t.Errorf("an architectural decision claims a human answered something: %+v", got)
	}
	if strings.Contains(strings.ToLower(got.Describe()), "accepted by the human") {
		t.Errorf("the old unconditional claim is back: %s", got.Describe())
	}
}

// functionsIn lists the top-level function and method names declared in a file,
// so a test can assert a property over every one of them rather than over the
// handful someone remembered to name.
func functionsIn(t *testing.T, rel string) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../"+rel, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	var out []string
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Body != nil {
			out = append(out, fn.Name.Name)
		}
	}
	return out
}

// The four properties of a deferred Level-3 decision.
//
// Once the router establishes an authority condition there is no
// architect-authorized continuation left. The human may decline to answer now —
// that is theirs — but declining must not become a third answer, and the
// question they eventually answer must be the one they were asked.

// 1. Esc at an authority boundary produces the deferral transition, not a
// failure and not a stop.
func TestDeferredAuthorityIsItsOwnTransition(t *testing.T) {
	bus := event.NewBus()
	events, done := bus.Subscribe(16)
	defer done()
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}

	decided := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(context.Background(), nil, "task-1",
			"graph coverage is absent for the planned files", "dom", "base",
			authority.Decision{Level: authority.Human, Subject: "Authorize?", Options: level3Options()},
			level3Options())
		decided <- err
	}()

	waitForPending(t, e, "task-1")
	if !e.DeferAuthority("task-1") {
		t.Fatal("a task waiting at an authority boundary refused the deferral")
	}
	if err := <-decided; !errors.Is(err, errAuthorityDeferred) {
		t.Fatalf("deferral produced %v, want errAuthorityDeferred", err)
	}

	kinds := drain(events)
	if !hasKind(kinds, event.WorkflowAwaitingAuthority) {
		t.Errorf("no awaiting-authority transition was recorded: %v", kinds)
	}
	for _, wrong := range []event.Kind{event.WorkflowFailed, event.WorkflowStopped, event.AuthorityResolved} {
		if hasKind(kinds, wrong) {
			t.Errorf("deferring emitted %s, which claims something the human did not do: %v", wrong, kinds)
		}
	}
}

// 2. Nothing is consulted after the deferral. The run ends where it stood.
func TestDeferralCallsNoOneAfterwards(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "awaitChoice")
	defer_ := strings.Index(body, "errAuthorityDeferred")
	if defer_ < 0 {
		t.Fatal("awaitChoice no longer returns the deferral")
	}
	// The deferral branch must return before the resolution machinery: no
	// proposal to Sensei, no persisted resolution, no stop-option handling.
	prefix := body[:defer_]
	for _, forbidden := range []string{"authority.Persist", "senseiProposer", "authority.Stop"} {
		if strings.Contains(prefix, forbidden) {
			t.Errorf("the deferral path reaches %s; deferring must resolve nothing", forbidden)
		}
	}
	// And the run must not report it as an outcome: reportOutcome is how a task
	// tells the behavioural record what happened, and nothing happened.
	fail := funcBody(t, "internal/workflow/engine.go", "execute")
	if i := strings.Index(fail, "errAuthorityDeferred"); i < 0 {
		t.Error("the governed run does not recognise a deferred decision")
	}
}

// 3. Resuming asks the same question, and does not re-derive it.
func TestResumeRestoresTheDeferredQuestionWithoutRerouting(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "resumeAuthority")
	for _, forbidden := range []string{"routeAuthority", "routePlan", "certifyStart", "resolveArchitecture", "awareness_preflight"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("resuming a deferred decision reaches %s; the question must be restored, not re-derived", forbidden)
		}
	}
	if !strings.Contains(body, "awaitChoice") {
		t.Error("resuming a deferred decision does not ask it again")
	}

	// The recorded question survives a round trip through the session record
	// with its condition and options intact.
	original := DeferredAuthority{
		Condition: "graph coverage is absent for the planned files",
		Domain:    "github.com/globulario/sensei-code",
		BaseSHA:   "1bc39f29a7a2",
		Decision: authority.Decision{
			Level: authority.Human, Subject: "Authorize this architectural change?",
			Options: level3Options(),
		},
	}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var restored DeferredAuthority
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Condition != original.Condition || restored.BaseSHA != original.BaseSHA {
		t.Fatalf("the restored question is not the one asked: %+v", restored)
	}
	if len(restored.Decision.Options) != len(original.Decision.Options) {
		t.Fatalf("options were lost in the record: %+v", restored.Decision.Options)
	}
	for i, opt := range restored.Decision.Options {
		if opt.ID != original.Decision.Options[i].ID || opt.Label != original.Decision.Options[i].Label {
			t.Errorf("option %d changed across the record: %+v", i, opt)
		}
	}
}

// 4. Only an explicit chosen option moves the workflow past the boundary.
func TestOnlyAChosenOptionSatisfiesAnAuthorityBoundary(t *testing.T) {
	e := &Engine{Bus: event.NewBus(), SessionID: "s1", pending: map[string]chan string{}}

	type outcome struct {
		choice string
		err    error
	}
	results := make(chan outcome, 1)
	go func() {
		choice, err := e.awaitChoice(context.Background(), nil, "task-1", "a condition", "dom", "base",
			authority.Decision{Level: authority.Human, Subject: "Authorize?", Options: level3Options()},
			level3Options())
		results <- outcome{choice, err}
	}()
	waitForPending(t, e, "task-1")

	// Deferring does not satisfy it.
	if !e.DeferAuthority("task-1") {
		t.Fatal("the deferral was refused")
	}
	got := <-results
	if got.choice != "" {
		t.Fatalf("deferring produced a choice %q, so the boundary was satisfied without an answer", got.choice)
	}

	// Neither does an option nobody offered.
	go func() {
		choice, err := e.awaitChoice(context.Background(), nil, "task-2", "a condition", "dom", "base",
			authority.Decision{Level: authority.Human, Subject: "Authorize?", Options: level3Options()},
			level3Options())
		results <- outcome{choice, err}
	}()
	waitForPending(t, e, "task-2")
	if !e.ResolveHuman("task-2", "7") {
		t.Fatal("the answer was not delivered")
	}
	if got := <-results; got.err == nil || got.choice != "" {
		t.Fatalf("an option that was never offered satisfied the boundary: %+v", got)
	}

	// An offered option does.
	go func() {
		choice, err := e.awaitChoice(context.Background(), nil, "task-3", "", "dom", "base",
			authority.Decision{Level: authority.Human, Subject: "Authorize?", Options: level3Options()},
			level3Options())
		results <- outcome{choice, err}
	}()
	waitForPending(t, e, "task-3")
	if !e.ResolveHuman("task-3", "2") {
		t.Fatal("the answer was not delivered")
	}
	if got := <-results; got.err != nil || !strings.HasPrefix(got.choice, "2:") {
		t.Fatalf("an explicit answer did not move the workflow past the boundary: %+v", got)
	}
}

func level3Options() []authority.Option {
	return []authority.Option{
		{ID: "1", Label: "Preserve current human-owned intent and require another design"},
		{ID: "2", Label: "Authorize the architectural change described above"},
	}
}

func waitForPending(t *testing.T, e *Engine, taskID string) {
	t.Helper()
	for i := 0; i < 200; i++ {
		e.mu.Lock()
		_, ok := e.pending[taskID]
		e.mu.Unlock()
		if ok {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("%s never reached the authority rendezvous", taskID)
}

func drain(events <-chan event.Event) []event.Kind {
	var kinds []event.Kind
	for {
		select {
		case ev := <-events:
			kinds = append(kinds, ev.Kind)
		default:
			return kinds
		}
	}
}

func hasKind(kinds []event.Kind, want event.Kind) bool {
	for _, k := range kinds {
		if k == want {
			return true
		}
	}
	return false
}

// TestOnlyAnEmptyCandidateIsRemovedAutomatically is the safety property of the
// terminal lifecycle. Automatic cleanup exists so a directory of undifferentiated
// leftovers stops being the normal state, and it must never be the mechanism
// that destroys unpublished work — which is exactly what "delete on exit" would
// have done to the one accepted candidate that mattered.
func TestOnlyAnEmptyCandidateIsRemovedAutomatically(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "disposeIfEmpty")

	// This assertion is about ORDER, which is all source structure can settle.
	// Whether the question is answered correctly is settled behaviourally in
	// disposal_test.go, against a real repository -- the 2026-08-21 audit found
	// this test passing while disposal consulted a stale snapshot and deleted
	// work, because an identifier in the right place says nothing about where
	// its value came from.
	produced := strings.Index(body, "HoldsWork")
	removal := strings.Index(body, "RemoveWorktree")
	if produced < 0 {
		t.Fatal("disposal no longer consults whether the candidate holds work")
	}
	if !strings.Contains(body, "observeCandidate") {
		t.Fatal("disposal decides from something other than the candidate itself")
	}
	if removal < 0 {
		t.Fatal("disposal no longer removes anything")
	}
	if produced > removal {
		t.Fatal("the worktree is removed before its contents are considered")
	}
	// Evidence is recorded before git is touched: a disposal that deletes first
	// and records second loses the case it needs to explain.
	record := strings.Index(body, "resolveCandidate")
	if record < 0 || record > removal {
		t.Fatal("the candidate is removed before its disposition is recorded")
	}
	if !strings.Contains(body, "Resumable") {
		t.Error("a candidate holding work has no retention branch, so it would fall through to removal")
	}

	// The accepted path retains deliberately rather than by omission.
	impl := funcBody(t, "internal/workflow/engine.go", "implement")
	if !strings.Contains(impl, "Retained") {
		t.Error("an accepted candidate records no disposition, so it means nothing in particular afterwards")
	}
	if strings.Contains(impl, "RemoveWorktree") {
		t.Error("the run removes a worktree directly, bypassing the evidence-before-removal rule")
	}
}

// #37: the architect may widen what a human can say yes to, and can never
// remove or reword the way to say no.
func TestArchitectOptionsCannotDecideWhatChoosingThemMeans(t *testing.T) {
	// An architect trying to dress a refusal as an authorization, and to crowd
	// the real refusals off a three-slot surface.
	hostile := []authority.Option{
		{ID: "99", Label: "Stop this task", Outcome: authority.Authorize},
		{ID: "98", Label: "Abort everything"},
		{ID: "97", Label: "Cancel"},
		{ID: "96", Label: "And another"},
	}
	got := composeAuthorityOptions(hostile)

	var revise, stop int
	for _, o := range got {
		switch o.Outcome {
		case authority.Revise:
			revise++
		case authority.Stop:
			stop++
		case authority.Authorize:
			// fine: choosing an architect alternative authorizes it
		default:
			t.Fatalf("option %q carries no outcome", o.Label)
		}
	}
	if revise != 1 || stop != 1 {
		t.Fatalf("the two refusals must always be present exactly once, got revise=%d stop=%d", revise, stop)
	}
	// The architect proposed four; it cannot fill the surface.
	if len(got) != 4 {
		t.Fatalf("expected two proposals plus two refusals, got %d", len(got))
	}
	// Its option labelled "Stop this task" authorizes an alternative, because
	// what an option does is ours to decide and its wording is not.
	if got[0].Outcome != authority.Authorize {
		t.Fatalf("an architect option was allowed to declare its own outcome: %+v", got[0])
	}
	// The refusals are our words, at the end, with our IDs.
	if got[2].Outcome != authority.Revise || got[3].Outcome != authority.Stop {
		t.Fatalf("the refusals are not the last two options: %+v", got)
	}
	for i, o := range got {
		if o.ID != fmt.Sprint(i+1) {
			t.Fatalf("option %d has ID %q; the surface must be an unambiguous 1..n", i, o.ID)
		}
	}
}

// With no architect options at all, the surface is still complete.
func TestTheDecisionSurfaceIsCompleteWithoutAnyArchitectOptions(t *testing.T) {
	got := composeAuthorityOptions(nil)
	if len(got) != 3 {
		t.Fatalf("expected authorize, revise, stop; got %d", len(got))
	}
	if got[0].Outcome != authority.Authorize || got[1].Outcome != authority.Revise || got[2].Outcome != authority.Stop {
		t.Fatalf("default surface = %+v", got)
	}
	// An empty label from a model is dropped rather than shown as a blank row.
	if got := composeAuthorityOptions([]authority.Option{{Label: "   "}}); len(got) != 3 {
		t.Fatalf("a blank proposal was rendered as a choice: %+v", got)
	}
}

// A governance receipt must cite a commit this repository contains.
//
// The escalation path passed the graph's SourceRepoCommit as the resolution's
// base. That field is the rule snapshot's identity and on this installation it
// belongs to the services repository — gate.go says so, having already been
// bitten by comparing the two — so a real Level-3 resolution was filed citing
// "base commit e0f49fca0357", a commit sensei-code does not contain.
func TestAResolutionCitesThisRepositorysBaseNotTheGraphsSourceCommit(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "awaitHuman")
	if strings.Contains(body, "start.GraphSourceCommit(") || strings.Contains(body, "start.SourceRepoCommit(") {
		t.Fatal("the escalation still cites the rule snapshot's commit as the resolution's base")
	}
	if !strings.Contains(body, "e.governedBase(") {
		t.Fatal("the escalation no longer sources the base from this repository's candidate identity")
	}
	// And an unestablished identity yields nothing rather than a substitute: a
	// resolution with no stated base is honest, one with somebody else's is not.
	fn := funcBody(t, "internal/workflow/engine.go", "governedBase")
	if !strings.Contains(fn, "candidate.Load") {
		t.Fatal("the base is not read from the candidate identity")
	}
	for _, forbidden := range []string{"SourceRepoCommit", "GraphBuildCommit"} {
		if strings.Contains(fn, forbidden) {
			t.Fatalf("governedBase falls back to %s; another repository's commit is not a substitute", forbidden)
		}
	}
}

// rawSource reads a file verbatim. funcBody renders the syntax tree, so string
// literals never appear in it -- an assertion about a message the code emits
// has to read the bytes.
func rawSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile("../../" + rel)
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// "Audit sensei-code" failed with "no bounded implementor produced an
// acceptable candidate". Both implementors had done exactly what was asked: the
// architect's own plan said "Conduct a read-only architectural audit", they read
// and reported, and the loop read their empty diff as a worker that failed to
// produce a change.
//
// A read-only plan's result is findings and no diff. The distinction is declared
// by the architect rather than inferred, because `files` lists what a plan
// touches and an audit touches many files while changing none.
func TestAReadOnlyPlanIsNotAFailedImplementation(t *testing.T) {
	body := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(body, "ModeInspect") {
		t.Fatal("the candidate loop no longer distinguishes a read-only plan")
	}
	// The empty-diff refusal must still exist for work that was meant to change
	// something: turning every empty diff into success would hide a worker that
	// failed to implement a change it was asked for.
	if !strings.Contains(body, "implementor produced no candidate diff") {
		t.Fatal("an empty diff is now accepted for modify plans too")
	}
}

// An unknown or absent mode is modify. Treating it as inspect would let a
// malformed field turn a change request into a run that accepts producing
// nothing.
func TestAnUnknownPlanModeIsModify(t *testing.T) {
	for _, declared := range []string{"", "  ", "MODIFY", "something-else", "inspect-ish"} {
		if got := planMode(declared); got != ModeModify {
			t.Errorf("planMode(%q) = %q, want %q", declared, got, ModeModify)
		}
	}
	for _, declared := range []string{"inspect", "INSPECT", " Inspect "} {
		if got := planMode(declared); got != ModeInspect {
			t.Errorf("planMode(%q) = %q, want %q", declared, got, ModeInspect)
		}
	}
}

// A read-only plan that edits the repository is out of scope, and saying which
// files it touched is more useful than reviewing them.
func TestAReadOnlyPlanThatChangesFilesIsRefused(t *testing.T) {
	body := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(body, "the plan was read-only and the candidate changed") {
		t.Fatal("a read-only plan that produced a diff is reviewed rather than refused")
	}
}

// Nothing is published or retained for a read-only run: an empty branch offered
// for publication asks the human to land nothing.
func TestAReadOnlyRunPublishesAndRetainsNothing(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "implement")
	inspect := strings.Index(body, "ModeInspect")
	if inspect < 0 {
		t.Fatal("implement no longer handles a read-only plan")
	}
	offer := strings.Index(body, "offerPullRequest")
	if offer >= 0 && offer < inspect {
		t.Fatal("publication is offered before the read-only branch returns")
	}
	if !strings.Contains(body, "disposeIfEmpty") {
		t.Fatal("a read-only run leaves its empty candidate behind")
	}
}

// The architect is told the vocabulary, and told that listing files does not
// make a plan a modifying one.
func TestTheArchitectIsAskedToDeclareTheMode(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "", "")
	for _, want := range []string{`"mode": "modify" | "inspect"`, "MODE IS REQUIRED", "changes nothing", "does not make a plan modify"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the architect prompt is missing %q", want)
		}
	}
}

// Finding 4 of the 2026-08-21 audit: a read-only run's success condition was
// "the diff was empty", which is a fact about what the worker did NOT do. A
// worker that reported nothing, and one that did the work, passed on identical
// evidence.
func TestAReadOnlyRunThatReportsNothingIsNotSuccess(t *testing.T) {
	body := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(body, "the read-only plan produced no findings") {
		t.Fatal("an empty inspection report is accepted again")
	}
}

// The worker's own text is the deliverable of an inspection. Discarding it left
// the run with nothing to show but a transcript nobody had judged.
func TestTheWorkerResultIsKept(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if strings.Contains(body, "if _, err := impl.Run") {
		t.Fatal("the implementor's result is discarded again")
	}
	if !strings.Contains(body, "report") {
		t.Fatal("runCandidate no longer keeps the worker's report")
	}
}

// Acceptance must rest on an independent verdict about the findings, not on the
// absence of a diff. Without this an inspection is self-certifying.
func TestInspectionAcceptanceRestsOnAnIndependentReview(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	inspect := strings.Index(body, "ModeInspect")
	if inspect < 0 {
		t.Fatal("the inspect branch is gone")
	}
	for _, want := range []string{"inspectionPacket", "resolveReview", "roles.Assign"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the inspect branch does not reach %s", want)
		}
	}
	// The verdict, not the empty diff, decides. Read the bytes: funcBody
	// renders the tree, where string literals do not appear.
	if !strings.Contains(rawSource(t, "internal/workflow/engine.go"), "the findings were reviewed independently and accepted") {
		t.Fatal("acceptance no longer names the review as its grounds")
	}
}

// The reviewer that judged the findings must not be the worker that produced
// them, exactly as for a change.
func TestTheInspectionReviewerIsNotTheWorker(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if !strings.Contains(body, "roles.ErrNoIndependentReviewer") {
		t.Fatal("an inspection can be reviewed by its own author")
	}
}

// A read-only worker was told "you may inspect, edit, build, and test", so an
// audit that edited was caught only afterwards -- by refusing a candidate the
// worker had been invited to produce.
func TestAReadOnlyWorkerIsToldNotToEdit(t *testing.T) {
	inspect := implementationPrompt(taskContext{Mode: ModeInspect, Task: "audit"}, "plan", "", 1, nil, "")
	for _, want := range []string{"THIS PLAN IS READ-ONLY", "Do not edit", "unverified", "did NOT cover", "independent reviewer"} {
		if !strings.Contains(inspect, want) {
			t.Errorf("the read-only worker prompt is missing %q", want)
		}
	}
	if strings.Contains(inspect, "You may inspect, edit, build, and test") {
		t.Error("a read-only worker is still invited to edit")
	}
	modify := implementationPrompt(taskContext{Mode: ModeModify, Task: "build"}, "plan", "", 1, nil, "")
	if !strings.Contains(modify, "You may inspect, edit, build, and test") {
		t.Error("a modify worker lost its editing instruction")
	}
	if strings.Contains(modify, "THIS PLAN IS READ-ONLY") {
		t.Error("a modify worker is told the plan is read-only")
	}
}

// The reviewer is asked the question that fits the artifact. Judging findings
// with the diff prompt asks whether an empty change is safe, which it trivially
// is.
func TestTheInspectionReviewerJudgesFindingsNotADiff(t *testing.T) {
	p := roles.IndependentReviewPacket{Report: "finding one", Task: "audit", Plan: "read only"}
	if !p.Inspection() {
		t.Fatal("a packet carrying a report and no diff is not recognised as an inspection")
	}
	prompt := reviewPrompt(p)
	for _, want := range []string{"SUPPORTED", "EVIDENCE", "SCOPE", "LIMITS", "OVERSTATEMENT", "finding one"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the inspection review prompt is missing %q", want)
		}
	}
	if strings.Contains(prompt, "CANDIDATE DIFF") {
		t.Error("the inspection reviewer is being shown a diff section")
	}
	// A real change must still get the diff prompt.
	change := roles.IndependentReviewPacket{Diff: "--- a/x\n+++ b/x", Task: "build"}
	if change.Inspection() {
		t.Fatal("a packet carrying a diff is treated as an inspection")
	}
	if !strings.Contains(reviewPrompt(change), "CANDIDATE DIFF") {
		t.Error("a change review lost its diff section")
	}
}

// The modify path stops when a worker returns a byte-identical diff after being
// asked to revise; a read-only run had no such guard and would spend the whole
// cycle budget on a worker re-asserting the same findings.
func TestAnUnchangedReportStopsTheLoop(t *testing.T) {
	body := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(body, "the findings did not change between review cycles") {
		t.Fatal("a read-only run has no stagnation guard")
	}
	if !strings.Contains(body, "previousReportRevision") {
		t.Fatal("nothing remembers the previous report")
	}
}

// A reviewer escalation reaches the architect, never the human, and never ends
// the task on its own. The read-only path failed the run instead, which lets a
// reviewer close a task by raising a question nobody was asked to answer.
func TestAnInspectionEscalationReachesTheArchitect(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	inspect := strings.Index(body, "ModeInspect")
	escalate := strings.Index(body, "roles.Escalate")
	if inspect < 0 || escalate < 0 {
		t.Fatal("the inspect branch or its escalation handling is gone")
	}
	// Both paths resolve through the architect rather than returning an error.
	if n := strings.Count(body, "resolveArchitecture"); n < 2 {
		t.Fatalf("only %d escalation path(s) reach the architect; a change and an inspection both must", n)
	}
	if n := strings.Count(body, "recordReconciliation"); n < 2 {
		t.Fatalf("only %d escalation path(s) record a reconciliation", n)
	}
}

// The scope has to actually reach the recorded answer and the memo lookup, or
// keying on it changes nothing.
func TestAnAnswerIsRememberedAgainstThePlanItWasGivenFor(t *testing.T) {
	apply := funcBody(t, "internal/workflow/engine.go", "applyAnsweredCondition")
	// Coverage is subset-tolerant now, so the lookup matches answers rather
	// than looking up one key. The property is unchanged: the plan's scope
	// must reach the decision, or keying on it changes nothing.
	if !strings.Contains(apply, "Covers") {
		t.Fatal("the lookup does not consider the plan's scope")
	}
	// Every caller must supply the scope; one that does not would silently key
	// on "(no scope recorded)" and never match a real answer.
	src := rawSource(t, "internal/workflow/engine.go")
	// Two in the architect's proceed/escalate branches, two in the supplied
	// plan's routing (the earlier answer, and the answer just given).
	if n := strings.Count(src, "applyAnsweredCondition(taskID, routing.Condition, d.Files...)"); n != 4 {
		t.Fatalf("%d of 4 call sites pass the plan's files", n)
	}
	if strings.Contains(src, "applyAnsweredCondition(taskID, routing.Condition)") {
		t.Fatal("a call site still asks without naming the plan")
	}
	// And the answer must be recorded with it in the first place.
	choice := funcBody(t, "internal/workflow/engine.go", "awaitChoice")
	if !strings.Contains(choice, "Scope") {
		t.Fatal("the recorded resolution does not carry the scope it was given for")
	}
}

// Finding 8 of the 2026-08-21 audit. resolveArchitecture budgets malformed JSON
// with `attempt`, and four paths legitimately reset it to start a fresh
// question. The certified-escalation path needs no person, nests the previous
// prompt inside the next one, and comes straight back — so an architect that
// keeps escalating loops with no ceiling, spending a provider turn and growing
// the prompt every round, until the context is cancelled.
func TestTheArchitectResolutionLoopIsBounded(t *testing.T) {
	// Read the bytes, scoped to this function: funcBody collects identifiers
	// only, so an assignment like `attempt = 0` never appears in it.
	// resolveArchitectureIn holds the loop; resolveArchitecture is a wrapper
	// that supplies the governed checkout as the working directory. The
	// observation lane calls the same loop with a disposable workspace, so the
	// ceiling being asserted here covers both lanes.
	src := rawSource(t, "internal/workflow/engine.go")
	start := strings.Index(src, "func (e *Engine) resolveArchitectureIn")
	if start < 0 {
		t.Fatal("resolveArchitectureIn is gone")
	}
	rest := src[start:]
	if next := strings.Index(rest[1:], "\nfunc "); next > 0 {
		rest = rest[:next]
	}

	resets := strings.Count(rest, "attempt = 0")
	guards := strings.Count(rest, "newRound(")
	if resets == 0 {
		t.Fatal("the resolution loop no longer restarts for a new question")
	}
	// One guard per reset. A reset that skips the counter is a hole in the
	// ceiling, which is the whole defect.
	if guards != resets {
		t.Fatalf("%d reset(s) but %d guarded: every reset must be counted", resets, guards)
	}
	if !strings.Contains(rest, "newRound := func") {
		t.Fatal("the round counter is gone")
	}
	if !strings.Contains(rest, "maxRounds") {
		t.Fatal("there is no overall ceiling on resolution rounds")
	}
	if !strings.Contains(rest, "did not settle after") {
		t.Fatal("exhausting the rounds does not say what happened")
	}
}

// Between "you may" and "you may not" about the same work, the refusal governs.
// Both were given about a region containing this plan, and only one reading is
// safe.
func TestARefusalGovernsOverAnAuthorisationThatAlsoCovers(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "applyAnsweredCondition")
	permits := strings.Index(body, "Permits")
	authorized := strings.Index(body, "authorized")
	if permits < 0 {
		t.Fatal("the lookup no longer classifies the answer")
	}
	if authorized >= 0 && authorized < permits {
		t.Fatal("an authorisation is recorded before a refusal is checked")
	}
	src := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(src, "the only safe reading is the refusal") {
		t.Fatal("the precedence between a refusal and an authorisation is not stated")
	}
}

// The re-plan after a human answer used to receive the choice and the previous
// escalation's summary, and nothing about the plan itself. So the architect
// re-planned from the task rather than revising the approved plan, and produced
// a different file list every round. Since an answer covers only a plan inside
// what was authorised, every outward drift became a fresh question and the same
// boundary went back to the person.
//
// Observed twice on 2026-08-22: seven files authorised, nine proposed next,
// three never seen — including internal/candidate/identity.go.
func TestTheReplanIsGivenTheScopeTheHumanAuthorised(t *testing.T) {
	d := architectureDecision{
		Summary: "the previous escalation",
		Files:   []string{"internal/admission/admission.go", "internal/workflow/engine.go"},
	}
	prompt := humanResolutionPrompt("ORIGINAL", d, "1: Authorize")

	for _, want := range []string{
		"THE SCOPE THE HUMAN AUTHORISED",
		"internal/admission/admission.go",
		"internal/workflow/engine.go",
		"Revise the plan they approved rather than",
		"Dropping a file is free",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the re-plan prompt is missing %q", want)
		}
	}
	// The original prompt and the choice must survive.
	if !strings.Contains(prompt, "ORIGINAL") || !strings.Contains(prompt, "1: Authorize") {
		t.Error("the re-plan lost the original prompt or the human's choice")
	}
}

// An architect that genuinely needs more scope must be able to say so.
// Forbidding additions outright would trade an honest question for a quiet
// omission, which is the worse failure.
func TestWideningIsAllowedButMustBeNamed(t *testing.T) {
	prompt := humanResolutionPrompt("o", architectureDecision{Files: []string{"a.go"}}, "1")
	if strings.Contains(prompt, "you may not add") {
		t.Error("the prompt forbids widening outright, which invites a silent omission instead")
	}
	for _, want := range []string{"add what it needs", "files you added and why each is necessary", "wandering is not"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not require a widening to be named: missing %q", want)
		}
	}
	// And it must say what widening costs, or there is no reason not to wander.
	if !strings.Contains(prompt, "asked again") {
		t.Error("the prompt does not say that adding a file re-asks the human")
	}
}

// A plan that named no files means there is no authorised set, which is not the
// same as no constraint. A blank list would read as the latter.
func TestAnUnscopedApprovalSaysSoRatherThanRenderingBlank(t *testing.T) {
	prompt := humanResolutionPrompt("o", architectureDecision{}, "1")
	if !strings.Contains(prompt, "named no files") {
		t.Fatal("an approval with no scope renders as an empty list, which reads as no constraint")
	}
}

// Sensei reads base content only from a caller-pinned commit, so without
// expected_head a modify hunk cannot be reconstructed and every audit of a
// changed file returned cannot_verify / repository_context_unavailable. The
// field used to be omitted deliberately, because the audit also compared it
// against the graph's own authority commit — which identifies the rule
// snapshot, not the repository, so a sensei-code commit could never equal it.
// Both roads ended at cannot_verify.
//
// Sensei 7bf987d4 removed that false coupling. Measured against sensei b9ebca0c
// on one diff: without the field cannot_verify/repository_context_unavailable,
// with it pass/available.
func TestTheAuditIsPinnedToTheCandidatesBase(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "runCandidate")
	if !strings.Contains(body, "auditArgs") {
		t.Fatal("the diff audit no longer builds its arguments here")
	}
	src := rawSource(t, "internal/workflow/engine.go")
	if !strings.Contains(src, `auditArgs["expected_head"]`) {
		t.Fatal("expected_head is not sent, so a modified file cannot be audited")
	}
	// The candidate's base, never the repository head. Once a worktree exists
	// those differ, and auditing against a commit the candidate was not cut
	// from reconstructs the wrong pre-change bytes — which is worse than not
	// auditing, because it looks like it worked.
	i := strings.Index(src, `auditArgs["expected_head"]`)
	window := src[max0(i-260) : i+120]
	if !strings.Contains(window, "tc.Identity.BaseSHA") {
		t.Fatalf("expected_head is not taken from the candidate's identity:\n%s", window)
	}
	for _, wrong := range []string{"repositoryHead(", "start.SourceRepoCommit()", "GraphBuildCommit()"} {
		if strings.Contains(window, wrong) {
			t.Errorf("expected_head is taken from %s, which is not the candidate's base", wrong)
		}
	}
}

// An empty base must not be sent. A blank pin is not a pin, and Sensei would
// read it as no base at all — the same cannot_verify, reached less honestly.
func TestAnEmptyBaseIsNotSentAsAPin(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	i := strings.Index(src, `auditArgs["expected_head"]`)
	if i < 0 {
		t.Fatal("expected_head is not sent")
	}
	if !strings.Contains(src[max0(i-160):i], `!= ""`) {
		t.Fatal("expected_head is set without checking the base is present")
	}
}

func max0(i int) int {
	if i < 0 {
		return 0
	}
	return i
}

// Four consecutive inspection reports on 2026-08-22 were sent back by the
// independent reviewer for the same error, and the prompt's existing "mark
// anything you could not establish as unverified" did not prevent any of them:
//
//	"zero consumers, non-test and test. The package is orphaned"
//	"No non-test consumer exists — proven"
//
// A worker does not experience an empty search as unestablished. It searched,
// found nothing, and concluded nothing exists — so the move has to be named.
// Sensei already draws this line: EmptyProven is "I looked here and there was
// nothing"; Absent is "nothing exists".
func TestAReadOnlyWorkerIsToldNotToProveAbsenceFromASearch(t *testing.T) {
	prompt := implementationPrompt(taskContext{Mode: ModeInspect, Task: "audit"}, "plan", "", 1, nil, "")
	for _, want := range []string{
		"A SEARCH THAT FOUND NOTHING HAS NOT PROVEN ANYTHING ABSENT",
		"EmptyProven",
		"Absent",
		"Name the searches and their bounds",
	} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the read-only prompt does not name the prove-a-negative mistake: missing %q", want)
		}
	}
	// The modify worker must not inherit it: it is not writing findings.
	if strings.Contains(implementationPrompt(taskContext{Mode: ModeModify}, "p", "", 1, nil, ""),
		"A SEARCH THAT FOUND NOTHING") {
		t.Error("a modify worker is given inspection-report guidance it has no use for")
	}
}

// Two findings that contradict each other mean at least one is wrong and the
// reader cannot tell which. A reviewer caught exactly this on 2026-08-22:
// "every field of admission.Request except Repo is produced by steps 1-2"
// conflicting with the report's own Finding 5.
func TestAReadOnlyWorkerIsToldToReconcileItsOwnFindings(t *testing.T) {
	prompt := implementationPrompt(taskContext{Mode: ModeInspect, Task: "audit"}, "plan", "", 1, nil, "")
	for _, want := range []string{"CHECK YOUR FINDINGS AGAINST EACH OTHER", "at least one is wrong", "less sure of"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("the prompt does not require internal consistency: missing %q", want)
		}
	}
}

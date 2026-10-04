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
// authorize what they already authorized. The governed run has exactly two
// paths that put a decision to a person, and neither of them sits here.
//
// The plan's publication is observed by driving the ordinary production run --
// run, through execute, to the canonical plan transition -- against a Sensei
// that certifies the region, rather than by searching run's text: the one
// durable PlanProposed is emitted by that transition, which the run calls.
func TestRunDoesNotAskForRoutinePlanApproval(t *testing.T) {
	const taskID = "task-routine-plan"
	store := sessionStore(t)
	routine := `{"decision":"proceed","summary":"edit main","plan":"edit main.go","files":["main.go"],"mode":"modify"}`
	e, architect, world := newGapLoopEngine(t, nil, store, routine)
	run := driveGapLoop(t, e, architect, world, taskID, "", func(ctx context.Context) {
		e.run(ctx, taskID, "change main.go", RequestedByHuman)
	})

	// The plan is still shown. Removing the ceremony must not remove the
	// information: a human who cannot see the plan cannot decide to stop.
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var proposed []event.Event
	for _, ev := range history {
		if ev.TaskID == taskID && ev.Kind == event.PlanProposed {
			proposed = append(proposed, ev)
		}
	}
	if len(proposed) != 1 {
		t.Fatalf("the run no longer publishes the plan exactly once, so the human cannot see what was authorized: %d PlanProposed\n%s",
			len(proposed), gapLoopTrace(run.events))
	}
	var rec proposedPlan
	if err := json.Unmarshal(proposed[0].Payload, &rec); err != nil || rec.Plan != "edit main.go" || rec.PlanAttemptID == "" {
		t.Fatalf("the published plan is not the routed plan made operative by the canonical transition: %v %+v", err, rec)
	}
	// No routine plan approval: nothing in the run asked the human anything.
	for _, ev := range run.events {
		if ev.Kind == event.AuthorityRequired || ev.Kind == event.WorkflowAwaitingAuthority {
			t.Fatalf("a routine plan asked for human approval: %s: %s", ev.Kind, ev.Summary)
		}
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
	body := funcBody(t, "internal/workflow/engine.go", "run") + " " + funcBody(t, "internal/workflow/engine.go", "execute") +
		" " + funcBody(t, "internal/workflow/engine.go", "adoptPlanAttempt")
	if strings.Contains(body, "approvePlan") {
		t.Error("the plan-approval rendezvous is back in the run")
	}
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
	// Every governed ending -- a fresh run or a resumed one -- is classified by
	// terminateRun, which must recognise a deferral before anything else.
	if !strings.Contains(funcBody(t, "internal/workflow/engine.go", "terminateRun"), "errAuthorityDeferred") {
		t.Error("the governed run does not recognise a deferred decision")
	}
	for _, fn := range []string{"execute", "Resume"} {
		if !strings.Contains(funcBody(t, "internal/workflow/engine.go", fn), "terminateRun") {
			t.Errorf("%s does not end through the classifier that recognises a deferral", fn)
		}
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
	// askArchitect holds the loop, and resolveArchitectureIn walks the architect
	// roster over it; resolveArchitecture is a wrapper that supplies the governed
	// checkout as the working directory. The observation lane calls the same loop
	// with a disposable workspace, so the ceiling asserted here covers both lanes.
	src := rawSource(t, "internal/workflow/engine.go")
	rest := sourceOfFunc(t, src, "func (e *Engine) askArchitect")

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
	// The counter is the one the roster walk owns, not one this turn made for
	// itself. A per-entry counter would give every fallback architect a fresh
	// budget of rounds, and the ceiling would bound nothing.
	if !strings.Contains(rest, "newRound := rounds.begin") {
		t.Fatal("the round counter is gone, or is no longer the shared one the roster walk owns")
	}
	walk := sourceOfFunc(t, src, "func (e *Engine) resolveArchitectureIn")
	if strings.Count(walk, "rounds := &resolutionRounds{}") != 1 {
		t.Fatal("the resolution rounds are not created once for the whole roster walk")
	}
	if at := strings.Index(walk, "rounds := &resolutionRounds{}"); at > strings.Index(walk, "for position, cfg := range roster") {
		t.Fatal("the round counter is created inside the roster loop, so each architect gets a fresh budget")
	}
	ceiling := sourceOfFunc(t, src, "func (r *resolutionRounds) begin")
	if !strings.Contains(ceiling, "maxResolutionRounds") {
		t.Fatal("there is no overall ceiling on resolution rounds")
	}
	if !strings.Contains(ceiling, "did not settle after") {
		t.Fatal("exhausting the rounds does not say what happened")
	}
}

// sourceOfFunc is one function's bytes, from its declaration to the next one.
func sourceOfFunc(t *testing.T, src, decl string) string {
	t.Helper()
	start := strings.Index(src, decl)
	if start < 0 {
		t.Fatalf("%s is gone", decl)
	}
	rest := src[start:]
	if next := strings.Index(rest[1:], "\nfunc "); next > 0 {
		rest = rest[:next]
	}
	return rest
}

// W3's REMAINING HALF -- a control, and not optional. A bounded decision the run
// does not like ends the turn on the entry that produced it: a human question, a
// human stop, a condition the human already declined, a gap disposal that limits
// the plan, an escalation whose authority the router cannot establish. The roster
// MUST NOT advance past any of them. A ladder that walks on a legitimate refusal
// is shopping for a provider that says yes.
//
// Asserted on the source rather than by driving the turn, and the reason is a
// limit worth stating rather than hiding: every human-owned route is decided
// INSIDE routePlan's answer, and reaching one behaviourally needs a preflight
// that vouches for its own graph. No test surface in this package can supply one
// -- the stub Sensei here refuses certifiability well before the router reaches a
// consequence or an approval gate -- so a "behavioural" version of this witness
// would prove the routing refusal and not the human boundary.
//
// So the property is asserted where it is decided. architectNotObtained is the
// ONLY thing the roster may advance past, it is constructed in exactly the two
// places that ended WITHOUT an answer, and every human-owned and governance
// return between them hands back its own error unwrapped.
func TestOnlyAnUnansweredArchitectTurnIsFallbackEligible(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	ask := sourceOfFunc(t, src, "func (e *Engine) askArchitect")

	// Two, and only two: the provider that PROVED it cannot serve now, and the
	// turn that spent its whole attempt budget without an answer.
	if got := strings.Count(ask, "&architectNotObtained{"); got != 2 {
		t.Fatalf("askArchitect makes %d of its returns fallback-eligible, want exactly the two that produced no answer", got)
	}
	proven := strings.Index(ask, "if blocked := roleUnavailable(")
	first := strings.Index(ask, "&architectNotObtained{")
	if proven < 0 || first < proven {
		t.Fatal("the first fallback-eligible return is no longer the one a provider proved")
	}
	if last := strings.LastIndex(ask, "&architectNotObtained{"); !strings.Contains(ask[last:], "lastErr") {
		t.Fatal("the last fallback-eligible return is no longer the exhausted attempt budget")
	}

	// THE CONTROL. Each human-owned or governance outcome is followed by a
	// refusal that is returned as ITSELF. Wrapping any one of them would hand a
	// legitimate refusal to the next architect on the roster, and would fire here.
	for _, site := range []string{
		"e.awaitHuman(",               // a human question, and a human stop
		"e.applyAnsweredCondition(",   // a condition the human already declined
		"e.disposeUnclosedGap(",       // a disposal that limits what may be done
		"cannot establish authority ", // an escalation the router refuses
	} {
		rest, found := ask, 0
		for {
			at := strings.Index(rest, site)
			if at < 0 {
				break
			}
			found++
			rest = rest[at+len(site):]
			next := strings.Index(rest, "return architectureDecision{}")
			if next < 0 {
				t.Fatalf("%s is no longer followed by a refusal this turn returns", site)
			}
			stmt := rest[next:min(next+140, len(rest))]
			if strings.Contains(stmt, "architectNotObtained") {
				t.Fatalf("the refusal following %s is handed to the next roster entry: %s", site, stmt)
			}
		}
		if found == 0 {
			t.Fatalf("%s is gone from the architect turn, so this control no longer covers it", site)
		}
	}

	// And the walk consults exactly one predicate to decide whether to advance,
	// so no second rule can grow beside it.
	walk := sourceOfFunc(t, src, "func (e *Engine) resolveArchitectureIn")
	if got := strings.Count(walk, "errors.As(err, &notObtained)"); got != 1 {
		t.Fatalf("the roster walk decides advancement in %d places, want one", got)
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

// ---------------------------------------------------------------------------
// W1, ENGINE LEVEL -- the projected refusal is what PLAN ADMISSION returns,
// and nothing downstream of it is reached.
//
// The measured run was refused at candidate time for importing "errors" into
// a test file that did not import it at the pinned base, after the implementer
// had written 3147 insertions across 23 files. The refusal was correct; the
// door was in the wrong place. The declaration half of this witness is in
// testedit_test.go. This half binds it to the admission path: the sentence the
// projector produces is the sentence routePlan returns, and a plan refused
// there cannot reach the point where an implementer is asked for anything.
// ---------------------------------------------------------------------------

// funcDeclIn parses one top-level function or method out of a source file, so
// a witness can assert the SHAPE of a control-flow guard rather than the
// presence of a token. A token scan cannot tell a returned error from an
// ignored one, and it is the return that makes a check a door.
func funcDeclIn(t *testing.T, rel, name string) *ast.FuncDecl {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "../../"+rel, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", rel, err)
	}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok && fn.Name.Name == name && fn.Body != nil {
			return fn
		}
	}
	t.Fatalf("%s declares no %s", rel, name)
	return nil
}

// callsIn maps each function name called anywhere under n to where it is
// first called.
func callsIn(n ast.Node) map[string]token.Pos {
	out := map[string]token.Pos{}
	note := func(name string, pos token.Pos) {
		if _, seen := out[name]; !seen {
			out[name] = pos
		}
	}
	ast.Inspect(n, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fn := call.Fun.(type) {
		case *ast.Ident:
			note(fn.Name, call.Pos())
		case *ast.SelectorExpr:
			note(fn.Sel.Name, call.Pos())
		}
		return true
	})
	return out
}

// errIsNotNil reports whether cond is exactly `err != nil`.
func errIsNotNil(cond ast.Expr) bool {
	b, ok := cond.(*ast.BinaryExpr)
	if !ok || b.Op != token.NEQ {
		return false
	}
	lhs, lok := b.X.(*ast.Ident)
	rhs, rok := b.Y.(*ast.Ident)
	return lok && rok && lhs.Name == "err" && rhs.Name == "nil"
}

// returnsErr reports whether the statement list returns, carrying err.
func returnsErr(body *ast.BlockStmt) bool {
	for _, stmt := range body.List {
		ret, ok := stmt.(*ast.ReturnStmt)
		if !ok {
			continue
		}
		for _, r := range ret.Results {
			if id, ok := r.(*ast.Ident); ok && id.Name == "err" {
				return true
			}
		}
	}
	return false
}

// selectorNamed reports whether e is a selector expression ending in name.
func selectorNamed(e ast.Expr, name string) bool {
	sel, ok := e.(*ast.SelectorExpr)
	return ok && sel.Sel.Name == name
}

func TestPlanAdmissionReturnsTheProjectedRefusalBeforeAnyImplementerIsReached(t *testing.T) {
	// 1. THE REFUSAL, from the plan as the architect actually emits it. The
	// engine's own decoder reads the declaration out of the architect's JSON,
	// and the projector resolves it against the pinned grant.
	plan, err := json.Marshal(architectureDecision{
		Decision: "proceed", Summary: "edit the regression beside its subject", Plan: "add the witness",
		Files: []string{teS, teF}, TestEdits: []TestEditDeclaration{teTheMeasuredCase()},
	})
	if err != nil {
		t.Fatal(err)
	}
	var d architectureDecision
	if err := decodeModelJSON(string(plan), &d); err != nil {
		t.Fatalf("the architect's plan did not decode: %v", err)
	}
	refusal := projectTestEditRefusals(d.TestEdits, teEditGrants(t))
	if refusal == nil {
		t.Fatal("the measured plan was admitted")
	}
	if refusal.Error() != refuteTestEditNovelImport(teF, "errors").Error() {
		t.Fatalf("plan admission would refuse with a sentence of its own: %v", refusal)
	}

	// 2. THAT ERROR IS WHAT routePlan RETURNS. Asserted on the guard's shape:
	// the projector is called on the ACCEPTED PLAN's declarations and the
	// PINNED grants, and its error is returned rather than logged, warned
	// about, or dropped.
	routePlan := funcDeclIn(t, "internal/workflow/engine.go", "routePlan")
	var guarded bool
	var projectedAt token.Pos
	ast.Inspect(routePlan.Body, func(n ast.Node) bool {
		branch, ok := n.(*ast.IfStmt)
		if !ok {
			return true
		}
		assign, ok := branch.Init.(*ast.AssignStmt)
		if !ok || len(assign.Rhs) != 1 {
			return true
		}
		call, ok := assign.Rhs[0].(*ast.CallExpr)
		if !ok {
			return true
		}
		if fn, ok := call.Fun.(*ast.Ident); !ok || fn.Name != "projectTestEditRefusals" {
			return true
		}
		if !errIsNotNil(branch.Cond) || !returnsErr(branch.Body) {
			t.Fatal("routePlan calls the projector without returning what it says; a refusal that is not returned is not a door")
		}
		if len(call.Args) != 2 || !selectorNamed(call.Args[0], "TestEdits") {
			t.Fatalf("the projection does not read the accepted plan's declarations: %d arg(s)", len(call.Args))
		}
		grants, ok := call.Args[1].(*ast.CallExpr)
		if !ok || !selectorNamed(grants.Fun, "testEditGrants") {
			t.Fatal("the projection does not resolve against the pinned test-edit grants")
		}
		guarded, projectedAt = true, call.Pos()
		return false
	})
	if !guarded {
		t.Fatal("plan admission no longer projects the decidable test-edit refusals: the door at the entrance is gone")
	}

	// 3. AND IT PROJECTS AGAINST THE COMPLETE GRANT SET. The authored grants
	// are merged into the task's authority after the derived ones, so a
	// projection placed before that block would resolve against a smaller
	// authority than the candidate-time check will use -- and would silently
	// stop projecting for every authored-governed file.
	inRoutePlan := callsIn(routePlan.Body)
	authored, ok := inRoutePlan["authoredTestEditGrants"]
	if !ok {
		t.Fatal("routePlan no longer assembles the authored test-edit grants, so this ordering proves nothing")
	}
	if projectedAt < authored {
		t.Fatal("the projection reads the grants before the authored ones are merged: it would resolve against a partial authority")
	}

	// 4. NO IMPLEMENTER WORK IS REQUESTED AT THAT DOOR. routePlan resolves no
	// runner, builds no implementation prompt and runs no candidate loop, so a
	// plan refused here is refused before anyone is asked to spend a budget.
	for _, implementerWork := range []string{"resolveRunner", "implementationPrompt", "runCandidate", "implement"} {
		if _, reached := inRoutePlan[implementerWork]; reached {
			t.Errorf("routePlan reaches %s: the projected refusal no longer precedes every request for implementer work", implementerWork)
		}
	}

	// 5. AND THE CALLER STOPS ON IT. execute resolves the architecture, returns
	// on its error, and only then reaches the implementation. A refusal from
	// plan admission therefore ends the run with no worker selected -- which is
	// the whole point of moving this refusal earlier.
	execute := callsIn(funcDeclIn(t, "internal/workflow/engine.go", "execute").Body)
	admission, ok := execute["resolveArchitecture"]
	if !ok {
		t.Fatal("execute no longer resolves an architecture")
	}
	if supplied, ok := execute["resolveSuppliedPlan"]; ok && supplied < admission {
		admission = supplied
	}
	implementation, ok := execute["implement"]
	if !ok {
		t.Fatal("execute no longer implements anything, so this ordering proves nothing")
	}
	if admission > implementation {
		t.Fatal("execute selects an implementer before the plan is admitted")
	}
	var stops bool
	ast.Inspect(funcDeclIn(t, "internal/workflow/engine.go", "execute").Body, func(n ast.Node) bool {
		branch, ok := n.(*ast.IfStmt)
		if !ok || branch.Pos() < admission || branch.Pos() > implementation || !errIsNotNil(branch.Cond) {
			return true
		}
		guard := callsIn(branch.Body)
		if _, fails := guard["fail"]; !fails {
			return true
		}
		for _, stmt := range branch.Body.List {
			if _, returns := stmt.(*ast.ReturnStmt); returns {
				stops = true
			}
		}
		return true
	})
	if !stops {
		t.Fatal("a refused plan does not end the run between admission and implementation: the refusal would be survivable")
	}
}

// confinementRepo is a clean canonical checkout carrying the repository's own
// rule that .sensei-code/ is not source (the committed .gitignore says so;
// the fixture states it where a fixture can without committing a file), with
// a sensei stand-in whose every derivation DERIVES over main.go at the world
// it is asked about. The stand-in is what lets a coverage read be observed at
// all: a recipe the reader never saw and one it excluded both look like no
// coverage, so the derivation must succeed whenever it is asked.
func confinementRepo(t *testing.T) *Engine {
	t.Helper()
	repo, _ := mintRepo(t)
	if err := os.WriteFile(repo.Root+"/.git/info/exclude", []byte("/.sensei-code/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir() + "/sensei"
	script := "#!/bin/sh\nwhile [ $# -gt 0 ]; do [ \"$1\" = -revision ] && rev=\"$2\"; shift; done\n" +
		"printf '{\"result\":\"DERIVED\",\"pinned_commit\":\"%s\",\"subjects\":[{\"file\":\"main.go\"}]}' \"$rev\"\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SENSEI_BIN", bin)
	return &Engine{Repo: repo}
}

func closureDecision(t *testing.T) architectureDecision {
	t.Helper()
	var d architectureDecision
	if err := json.Unmarshal([]byte(`{"decision":"escalate","files":["main.go"],"proposed_recipe":`+
		`{"kind":"field_access_under_lock","dir":".","type":"T","field":"f","lock":"mu"}}`), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func coversMain(c coverageComputation) bool {
	for _, a := range c.coverage {
		if a.File == "main.go" {
			return true
		}
	}
	return false
}

// W1-W3: a closure round starting from a clean canonical checkout records its
// question and receipt, leaves the checkout clean, and what it wrote is read
// by a LATER task and never by the task that wrote it.
func TestAClosureRoundLeavesTheCanonicalCheckoutClean(t *testing.T) {
	ctx := context.Background()
	e := confinementRepo(t)
	if clean, err := e.Repo.IsClean(ctx); err != nil || !clean {
		t.Fatalf("the fixture is not a clean canonical checkout (clean=%v, err=%v)", clean, err)
	}

	e.recordClosureQuestion("task-writer", "coverage gap", closureDecision(t), certifiedStart{}, "model", 1)

	// The operation produced the derived state, in the owned location.
	recipes, err := os.ReadFile(e.Repo.Root + "/" + ownedRecipesPath)
	if err != nil || !strings.Contains(string(recipes), `"task-writer"`) {
		t.Fatalf("the closure round recorded no owned question (err=%v): %s", err, recipes)
	}
	if receipts, err := os.ReadFile(e.Repo.Root + "/" + ownedReceiptsPath); err != nil ||
		!strings.Contains(string(receipts), "task-writer") {
		t.Fatalf("the closure round recorded no owned receipt (err=%v): %s", err, receipts)
	}
	// W1: and nothing in the canonical checkout changed.
	if _, err := os.Stat(e.Repo.Root + "/docs/awareness"); !os.IsNotExist(err) {
		t.Fatalf("the closure round wrote into the committed corpus: %v", err)
	}
	if clean, err := e.Repo.IsClean(ctx); err != nil || !clean {
		t.Fatalf("a closure round left workflow-owned modification in the canonical checkout (clean=%v, err=%v)", clean, err)
	}

	// W2: a later task's coverage read consumes the owned question.
	later, ok := e.coverageAtWorld(ctx, "task-later", []string{"main.go"}, nil)
	if !ok || !coversMain(later) {
		t.Fatalf("a later task could not read the question an earlier run recorded: ok=%v %+v", ok, later.coverage)
	}
	// W3: the writing task still cannot.
	own, _ := e.coverageAtWorld(ctx, "task-writer", []string{"main.go"}, nil)
	if coversMain(own) {
		t.Fatalf("the task that wrote the question was covered by it: %+v", own.coverage)
	}
}

// W4: a recipe published in the committed corpus is still read, composes with
// the owned overlay, and is not copied into it when a round proposes it again.
func TestACommittedRecipeIsStillReadBesideTheOwnedOverlay(t *testing.T) {
	ctx := context.Background()
	e := confinementRepo(t)
	if err := os.MkdirAll(e.Repo.Root+"/docs/awareness", 0o755); err != nil {
		t.Fatal(err)
	}
	published := `{"recipes":[{"kind":"field_access_under_lock","dir":".","type":"T","field":"f","lock":"mu"}]}`
	if err := os.WriteFile(e.Repo.Root+"/"+committedRecipesPath, []byte(published), 0o644); err != nil {
		t.Fatal(err)
	}

	e.recordClosureQuestion("task-writer", "coverage gap", closureDecision(t), certifiedStart{}, "model", 1)
	if _, err := os.Stat(e.Repo.Root + "/" + ownedRecipesPath); !os.IsNotExist(err) {
		t.Fatalf("a question already published was copied into the owned overlay: %v", err)
	}
	if receipts, _ := os.ReadFile(e.Repo.Root + "/" + ownedReceiptsPath); !strings.Contains(string(receipts), `"outcome":"DUPLICATE"`) {
		t.Fatalf("the round was not recorded as a duplicate of the published question: %s", receipts)
	}
	if got, _ := os.ReadFile(e.Repo.Root + "/" + committedRecipesPath); string(got) != published {
		t.Fatalf("the committed corpus was rewritten: %s", got)
	}
	// Published, so no task wrote it and the future-only rule excludes no one.
	c, ok := e.coverageAtWorld(ctx, "task-writer", []string{"main.go"}, nil)
	if !ok || !coversMain(c) {
		t.Fatalf("the committed recipe is no longer read: ok=%v %+v", ok, c.coverage)
	}

	// And this repository's own published corpus survives composition whole.
	root := repoRootForCoverage(t)
	committed, err := os.ReadFile(root + "/" + committedRecipesPath)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Recipes []struct{ Kind, Type string } `json:"recipes"`
	}
	if err := json.Unmarshal(committed, &doc); err != nil || len(doc.Recipes) == 0 {
		t.Fatalf("the published corpus is unreadable or empty, so this proves nothing: %v", err)
	}
	composed, err := composedRecipes(root)
	if err != nil {
		t.Fatalf("the published corpus no longer composes: %v", err)
	}
	for _, want := range doc.Recipes {
		found := false
		for _, r := range composed {
			found = found || (r.Kind == want.Kind && r.Type == want.Type)
		}
		if !found {
			t.Fatalf("published recipe %s/%s is not read after composition", want.Kind, want.Type)
		}
	}
}

// The architect is told the closed role set as it now stands: the two
// new-package production roles, the covering surface each must name and the
// structural rule it must satisfy, and the unchanged regression-test rule. The
// schema's own example declares a covering surface the decision type reads.
func TestTheArchitectPromptStatesTheNewPackageRoles(t *testing.T) {
	prompt := architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf", "", "", "", "")
	for _, want := range []string{
		"closed set of exactly three",
		"go-regression-test: a *_test.go beside a covered file",
		"It is the only test role",
		"go-library-package / go-command-package",
		`MUST name "covering"`,
		"same Go module",
		`existing "cmd" directory`,
		"same top-level directory of the module",
		"declare EVERY file created in that",
		"only through the package's production declarations",
		"never establish a covering file",
		`"covering":"internal/m/m.go"`,
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("the architect prompt does not say %q", want)
		}
	}
	if strings.Contains(prompt, "the only role is") {
		t.Fatal("the architect prompt still says go-regression-test is the only role")
	}
	var d architectureDecision
	if err := json.Unmarshal([]byte(`{"prospective_surfaces":[{"path":"internal/n/n.go","package":"n","role":"go-library-package","covering":"internal/m/m.go"}]}`), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.ProspectiveSurfaces) != 1 || d.ProspectiveSurfaces[0].Covering != "internal/m/m.go" {
		t.Fatalf("the declared covering surface is not read: %+v", d.ProspectiveSurfaces)
	}
}

// Cause B, at the engine: the one command-to-library edge is recorded with the
// grants, restored with them, and the recorded grants -- not covering facts
// alone -- reach candidate inspection. A restored command grant that lost its
// edge refuses the dependent command, terminally. A plan with no prospective
// declarations is untouched: no grant, no record needed, no inspection.
func TestTheRecordedLibraryEdgeReachesCandidateInspection(t *testing.T) {
	body := rawSource(t, "internal/workflow/engine.go")
	gate := strings.Index(body, "if len(tc.Prospective) != 0 {")
	call := strings.Index(body, "inspectProspectiveGrants(diff, tc.Prospective, e.prospectiveGrants(taskID))")
	if gate < 0 || call < gate || strings.Contains(body[gate:call], "}") {
		t.Fatal("candidate inspection is not handed the recorded grants inside the declaration gate")
	}
	if strings.Contains(body, "inspectProspectiveSurfaces(") {
		t.Fatal("the engine still inspects candidates against covering facts alone")
	}

	decl := edgeDeclarations()
	grants := prospectiveFor(t, edgePlanned(), decl, newPackageAnchors(), newPackageWorld())
	e := &Engine{}
	if err := e.restoreProspectiveGrants(edgeRecordOf(grants), decl, prospectiveWorld); err != nil {
		t.Fatalf("the recorded edge did not restore: %v", err)
	}
	good := edgeLibraryDiff() + edgeCommandDiff("fmt", "os", answererImport)
	if err := inspectProspectiveGrants(good, decl, e.prospectiveGrants("t")); err != nil {
		t.Fatalf("the restored edge did not reach inspection: %v", err)
	}

	dropped := append([]prospectiveGrant(nil), e.prospectiveGrants("t")...)
	for i := range dropped {
		dropped[i].Edge = nil
	}
	if err := (&Engine{}).restoreProspectiveGrants(edgeRecordOf(dropped), decl, prospectiveWorld); err == nil {
		t.Fatal("a restored command grant without its edge was resumed")
	}
	e.setProspectiveGrants("t", dropped)
	err := inspectProspectiveGrants(good, decl, e.prospectiveGrants("t"))
	if err == nil || !isProspectiveSurfaceRefutation(err) || !strings.Contains(err.Error(), answererImport) {
		t.Fatalf("a command whose edge was dropped was not refuted terminally: %v", err)
	}

	// No prospective declarations: routing grants nothing over the same files,
	// resume needs no record, and inspection has nothing to check.
	none, _ := coverPlannedAtWorld(context.Background(), prospectiveWorld, edgePlanned(), nil, derivedAnchorNaming(t, libS, cmdS), worldOf(newPackageWorld()))
	if len(none) != 0 {
		t.Fatalf("a plan with no declarations was granted: %+v", none)
	}
	if err := (&Engine{}).restoreProspectiveGrants(edgeRecordOf(nil), nil, prospectiveWorld); err != nil {
		t.Fatalf("a plan with no declarations needed a record: %v", err)
	}
	if err := inspectProspectiveGrants(good, nil, nil); err != nil {
		t.Fatalf("a plan with no declarations was inspected: %v", err)
	}
}

// DF-37 (Objective 60), structural: plan admission reconciles the declared
// prospective surfaces against the grants derivedCoverage just recorded,
// AFTER that derivation and BEFORE the router can return an admitted plan,
// and returns the refusal on the plan-admission error path. Admission,
// restoration and candidate inspection all read the one canonical rule.
func TestDF37ReconciliationRunsAfterDerivationAndBeforeRouting(t *testing.T) {
	src := rawSource(t, "internal/workflow/engine.go")
	at := strings.Index(src, "func (e *Engine) routePlan(")
	if at < 0 {
		t.Fatal("routePlan not found")
	}
	end := strings.Index(src[at+1:], "\nfunc ")
	if end < 0 {
		t.Fatal("routePlan has no end")
	}
	body := src[at : at+1+end]
	derive := strings.Index(body, "e.derivedCoverage(ctx, taskID, d.Files, d.ProspectiveSurfaces)")
	reconcile := strings.Index(body, "if err := e.reconcileProspectiveGrants(taskID, d.ProspectiveSurfaces); err != nil {\n\t\treturn Routing{}, sensei.PreflightDecision{}, Action{}, err\n\t}")
	route := strings.Index(body, "routeAuthorityForAction(")
	if derive < 0 || reconcile < 0 || route < 0 {
		t.Fatalf("routePlan lacks derivation (%d), reconciliation refusing admission (%d) or routing (%d)", derive, reconcile, route)
	}
	if !(derive < reconcile && reconcile < route) {
		t.Fatalf("reconciliation is not between derivation (%d) and the first routing (%d): %d", derive, route, reconcile)
	}
	if strings.Count(body, "e.reconcileProspectiveGrants(") != 1 {
		t.Fatal("routePlan reconciles prospective grants more or less than once")
	}

	for name, want := range map[string]string{
		"func (e *Engine) reconcileProspectiveGrants(": "matchGrantsToDeclarations(declared, e.prospectiveGrants(taskID))",
		"func (e *Engine) restoreProspectiveGrants(":   "matchGrantsToDeclarations(declared, rec.Grants)",
	} {
		i := strings.Index(src, name)
		if i < 0 {
			t.Fatalf("%s not found", name)
		}
		j := strings.Index(src[i+1:], "\nfunc ")
		if j < 0 || !strings.Contains(src[i:i+1+j], want) {
			t.Fatalf("%s does not read the canonical declaration/grant rule", name)
		}
	}
	prospective := rawSource(t, "internal/workflow/prospective.go")
	i := strings.Index(prospective, "func inspectProspectiveGrants(")
	j := strings.Index(prospective[i+1:], "\nfunc ")
	if i < 0 || j < 0 || !strings.Contains(prospective[i:i+1+j], "matchGrantsToDeclarations(declarations, grants)") ||
		!strings.Contains(prospective[i:i+1+j], "inspectProspective(diff, declarations, facts, edges, envelopes)") {
		t.Fatal("candidate inspection does not read both the canonical rule and the candidate-content checks")
	}
}

// 70B1 (RULING-153): THE DURABLE INCOMPLETE-OBLIGATION CHECKPOINT IS A REPLAY
// CAPSULE. These witnesses drive the production transaction (commitCheckpoint)
// over a real session store, and the production candidate loop through the
// completion rig, whose harness holds a real store (RULING-154).

// committedFixture is a task's committed checkpoint, verified, with a way to
// replay its exact payload under one changed input.
type committedFixture struct {
	id, replayDigest, status, retirement string
	rep                                  replayed
	// replay replays the committed checkpoint with status (when not "") and
	// the inputs mutate leaves.
	replay func(status string, mutate func(*replayInputs)) (replayed, error)
}

func committedOf(t *testing.T, e *Engine, taskID string) committedFixture {
	t.Helper()
	b, cp, rep, found, err := e.committedCheckpoint(taskID)
	if err != nil || !found {
		t.Fatalf("no verified committed checkpoint for %s: found %v err %v", taskID, found, err)
	}
	return committedFixture{id: b.CheckpointID, replayDigest: b.ReplayDigest, status: string(b.Status), retirement: string(b.Retirement), rep: rep,
		replay: func(status string, mutate func(*replayInputs)) (replayed, error) {
			c := cp
			if status != "" {
				if err := json.Unmarshal([]byte(`"`+status+`"`), &c.Status); err != nil {
					t.Fatal(err)
				}
			}
			var in replayInputs
			if err := json.Unmarshal(c.Inputs, &in); err != nil {
				t.Fatal(err)
			}
			if mutate != nil {
				mutate(&in)
			}
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			c.Inputs = raw
			durable, err := e.replaySourcesOf(e.Store)
			if err != nil {
				t.Fatal(err)
			}
			if durable, err = durable.atPrepared(b, e.SessionID); err != nil {
				t.Fatal(err)
			}
			return Replay(c, durable)
		}}
}

// committedRecords counts the task's prepared and committed records.
func committedRecords(t *testing.T, e *Engine) (prepared, committed int) {
	t.Helper()
	history, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range history {
		switch ev.Kind {
		case event.CheckpointPrepared:
			prepared++
		case event.CheckpointCommitted:
			committed++
		}
	}
	return prepared, committed
}

// failCheckpointIO makes the named durable checkpoint operation fail when fail
// says so, for the rest of the test.
func failCheckpointIO(t *testing.T, fail func(op string, call int) bool) *[]string {
	t.Helper()
	original := checkpointIO
	calls := map[string]int{}
	var seen []string
	checkpointIO = func(op string, do func() error) error {
		calls[op]++
		seen = append(seen, op)
		if fail(op, calls[op]) {
			return fmt.Errorf("injected %s failure %d", op, calls[op])
		}
		return do()
	}
	t.Cleanup(func() { checkpointIO = original })
	return &seen
}

// fixtureEngine is an engine over a real session store and task-state root.
func fixtureEngine(t *testing.T) *Engine {
	t.Helper()
	e, _, _ := blockedEngine(t, t.TempDir(), "session-1")
	return e
}

// canonicalAttempt is a plan attempt made operative for taskID through the
// production transition: durably started, then adopted, in e's session record.
func canonicalAttempt(t *testing.T, e *Engine, taskID, plan string) planAttempt {
	t.Helper()
	adoptFixturePlanAttempt(t, e, taskID, "the objective", "world-1", plan, nil, nil)
	return e.operativePlanAttempt(taskID)
}

// mintedAttempt is a plan attempt whose identity its own record derives, and
// which no record shows was ever started or made operative.
func mintedAttempt(t *testing.T, taskID, plan string) planAttempt {
	t.Helper()
	a := planAttempt{TaskID: taskID, World: "world-1", PlanSource: PlanByArchitect,
		Plan: architectureDecision{Decision: "proceed", Summary: plan, Plan: plan}, objective: "the objective"}
	id, err := planAttemptID(taskID, a.objective, a.World, a.PlanSource, "", a.Plan)
	if err != nil {
		t.Fatal(err)
	}
	a.ID = id
	return a
}

func evidenceOn(id string) roles.Finding {
	return roles.Finding{ID: id, Severity: roles.Major, Class: roles.EvidenceFinding, Claim: "claim " + id,
		Reference: "main.go", Reason: "reason " + id, ProofGap: "go test ./..."}
}

func codeObligationFinding(id string) roles.Finding {
	return roles.Finding{ID: id, Severity: roles.Major, Class: roles.CodeFinding, Claim: "claim " + id, Reference: "main.go", Reason: "reason " + id}
}

// fixtureObligation is cycle 2's obligation for findings under a canonical attempt.
func fixtureObligation(t *testing.T, e *Engine, findings ...roles.Finding) *cycleCompletion {
	t.Helper()
	return obligationUnder(canonicalAttempt(t, e, "task-c", "the plan"), findings...)
}

// obligationUnder is cycle 2's obligation for findings under attempt a.
func obligationUnder(a planAttempt, findings ...roles.Finding) *cycleCompletion {
	c := newCycleCompletion("task-c", a.ID, 2, openReview{Attempt: 1, CandidateDigest: "sha256:raised", CandidateTree: "tree-raised", Findings: findings})
	c.Origin = a.binding()
	return c
}

// reconcile absorbs one returned report into c as the candidate loop does: the
// canonical validator judges every finding beside what is retained, against
// the measured moved files and, with evidence, the executed validation and the
// evidence retained and read back from durable task state.
func reconcile(t *testing.T, e *Engine, c *cycleCompletion, report string, moved map[string]bool, evidence bool) {
	t.Helper()
	bundle := n2bBundle("ok")
	for i := range bundle.Checks {
		bundle.Checks[i].ExecutedBy = "broker"
	}
	if !evidence {
		bundle.DiffDigest, bundle.Checks = "", nil
	}
	settled := settledInvocation{Provider: "claude", Cycle: c.Cycle, Observed: true, Returned: true, Report: report,
		Transport: plainReturn("").Transport}
	fresh, err := parseFindingResponses(report)
	if err != nil {
		t.Fatal(err)
	}
	responses, _, _ := c.responsesFor(fresh)
	cand := retainedCandidate("base-1", bundle)
	readBack := map[string]string{}
	if evidence {
		if readBack, err = e.retainFindingEvidence(c.TaskID, c.Cycle, cand, c.Findings, responses, bundle); err != nil {
			t.Fatal(err)
		}
	}
	account := accountForFindings(c.Findings, responses, moved, bundle, readBack)
	if err := c.recordAccount(judgeAll, settled, moved, bundle, cand, readBack); err != nil {
		t.Fatalf("premise: the account was refused: %v", err)
	}
	c.absorb(c.Findings, account, responses, settled)
}

var fixtureCandidate = candidateInput{What: "the fixture's capture", Base: "base-1", Tree: "tree-now"}

// escalateAndContinue takes c through the one owner-defined continuation: a
// validated invocation's account, its dispute escalation, the continuation
// onto to, and the retry it was continued for.
func escalateAndContinue(t *testing.T, e *Engine, c *cycleCompletion, to planAttempt) {
	t.Helper()
	reconcile(t, e, c, accounting(), nil, false)
	if _, err := c.transition(routeDisputeEscalation); err != nil {
		t.Fatalf("premise: the escalation was refused: %v", err)
	}
	if err := c.continueTo(to.binding()); err != nil {
		t.Fatalf("premise: the continuation was refused: %v", err)
	}
	if _, err := c.transition(routeIncompleteRetry); err != nil {
		t.Fatalf("premise: the continued retry was refused: %v", err)
	}
}

// retry counts one incomplete attempt as the candidate loop does: an
// invocation that answered nothing is accounted, and the cycle is retried.
func retry(t *testing.T, e *Engine, c *cycleCompletion) {
	t.Helper()
	reconcile(t, e, c, accounting(), nil, false)
	if _, err := c.transition(routeIncompleteRetry); err != nil {
		t.Fatalf("premise: the retry was refused: %v", err)
	}
}

// commitLive counts c's incomplete attempt -- unless it was just counted, or
// continued for one -- and commits its live checkpoint.
func commitLive(t *testing.T, e *Engine, c *cycleCompletion) string {
	t.Helper()
	var last replayStepKind
	if n := len(c.Steps); n != 0 {
		last = c.Steps[n-1].Kind
	}
	if last == stepContinue || last == stepAccount {
		if _, err := c.transition(routeIncompleteRetry); err != nil {
			t.Fatalf("premise: the retry was refused: %v", err)
		}
	} else {
		retry(t, e, c)
	}
	status, ok := c.durableStatus()
	if !ok || status != "live" {
		t.Fatalf("premise: the fixture obligation is %q, not live", status)
	}
	id, err := e.commitCheckpoint(c, status, "", fixtureCandidate)
	if err != nil {
		t.Fatalf("the live checkpoint was not committed: %v", err)
	}
	return id
}

func retainedIn(r replayed, id string) bool {
	if r.Completion == nil {
		return false
	}
	_, ok := r.Completion.Retained[id]
	return ok
}

// W1 LIVE REPLAY. A cycle-2 invocation answers f1 and leaves f2 owed: the live
// checkpoint committed before the cycle is served again replays through the
// landed owners to exactly the live obligation -- cycle 2, one attempt, f1
// retained, f2 owed -- and to its committed ReplayDigest.
func TestB1W1ALiveCheckpointReplaysToTheLiveObligation(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 3, unobserved: true, err: errors.New("the worker crashed")},
	)
	if _, err := r.run(); err == nil {
		t.Fatal("premise: the crashed invocation did not end the run")
	}
	got := committedOf(t, r.h.engine, "task-1")
	c := r.live(t)
	if got.status != "live" || got.rep.Digest != got.replayDigest || got.rep.Completion == nil {
		t.Fatalf("the committed checkpoint is not a live one that replays to its digest: %+v", got)
	}
	rc := got.rep.Completion
	if rc.Cycle != 2 || rc.Attempts != 1 || !equalStrings(rc.RetainedIDs(), "f1") || !equalStrings(rc.Owed(), "f2") ||
		rc.PlanAttemptID != c.PlanAttemptID || rc.PlanAttemptID != r.h.engine.operativePlanAttempt("task-1").ID {
		t.Fatalf("the replayed obligation is not the live one: %+v", rc)
	}
	// The checkpoint is the obligation as the retry left it; the crash that
	// followed routed the live one on, and was not checkpointed.
	if rc.Route != routeIncompleteRetry || !rc.Continuing || c.Route != routeOrdinaryError {
		t.Fatalf("the replayed obligation is not the one the retry committed: replayed %s, live %s", rc.Route, c.Route)
	}
}

// W2 BLOCKED REPLAY. After f1 is retained the next provider proves it cannot
// serve: the obligation is committed blocked before the block is returned, and
// replays to the same obligation with the provider-rebound route.
func TestB1W2ABlockedCheckpointReplaysToTheObligationAndItsBlock(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{err: quota()},
	)
	_, err := r.run()
	var blocked *RoleUnavailable
	if !errors.As(err, &blocked) {
		t.Fatalf("premise: the run did not end on the provider block: %v", err)
	}
	got := committedOf(t, r.h.engine, "task-1")
	rc := got.rep.Completion
	if got.status != "blocked" || rc == nil || rc.Route != routeProviderRebound || !rc.Continuing || rc.Attempts != 1 ||
		!equalStrings(rc.RetainedIDs(), "f1") || !equalStrings(rc.Owed(), "f2") {
		t.Fatalf("the committed checkpoint is not the blocked obligation: %s %+v", got.status, rc)
	}
}

// W3 EXHAUSTED REPLAY and W20 THE CHOKE POINT. The third incomplete attempt is
// committed exhausted before IMPLEMENTER_INCOMPLETE is returned; it replays to
// attempts == max with the typed state's owed findings.
func TestB1W3W20TheExhaustedCheckpointIsCommittedBeforeTheTypedState(t *testing.T) {
	r := exhaustingRig(t, 2)
	_, err := r.run()
	var incomplete *ImplementerIncomplete
	if !errors.As(err, &incomplete) {
		t.Fatalf("premise: the run did not end IMPLEMENTER_INCOMPLETE: %v", err)
	}
	got := committedOf(t, r.h.engine, "task-1")
	rc := got.rep.Completion
	if got.status != "exhausted" || rc == nil || !rc.Exhausted() || rc.Attempts != maxIncompleteImplementerAttempts ||
		!equalStrings(rc.Owed(), incomplete.Owed...) || rc.Cycle != incomplete.Cycle || rc.PlanAttemptID != incomplete.PlanAttemptID {
		t.Fatalf("the committed checkpoint is not the exhausted obligation the typed state reports: %s %+v / %+v", got.status, rc, incomplete)
	}
	if _, committed := committedRecords(t, r.h.engine); committed != 3 {
		t.Fatalf("expected the two live retries and the exhaustion committed, got %d commits", committed)
	}

	// The exhausted commit fails every attempt: no typed exhaustion is
	// returned, and the terminal carries the persistence failure instead.
	r = exhaustingRig(t, 2)
	failCheckpointIO(t, func(op string, call int) bool { return op == checkpointCommit && call > 2 })
	_, err = r.run()
	if errors.As(err, &incomplete) {
		t.Fatalf("IMPLEMENTER_INCOMPLETE was returned without a committed exhausted checkpoint: %v", err)
	}
	var unrecorded *ObligationPersistenceFailed
	if !errors.As(err, &unrecorded) || unrecorded.Status != "exhausted" || unrecorded.Attempts != maxCheckpointAttempts ||
		unrecorded.Prior != PriorCheckpointKnown || unrecorded.State != IncompleteObligationPersistenceFailedState {
		t.Fatalf("the failed exhaustion is not the typed persistence failure over a known prior checkpoint: %v", err)
	}
	if got := committedOf(t, r.h.engine, "task-1"); got.status != "live" || got.id != unrecorded.PriorCheckpointID {
		t.Fatalf("the prior live checkpoint did not stand: %+v / %+v", got, unrecorded)
	}
	terminateWith(t, r.h, err)
	for _, ev := range drainEvents(r.events) {
		if ev.Kind == event.WorkflowFailed && strings.Contains(string(ev.Payload), ImplementerIncompleteState) {
			t.Fatalf("the terminal reported IMPLEMENTER_INCOMPLETE without its checkpoint: %s", ev.Payload)
		}
		if ev.Kind == event.WorkflowFailed && !strings.Contains(string(ev.Payload), IncompleteObligationPersistenceFailedState) {
			t.Fatalf("the terminal does not carry the typed persistence failure: %s", ev.Payload)
		}
	}
}

// W4 RETIRED REPLAY. The cycle that completes retires the checkpoint it had
// committed with a typed reason; the tombstone replays non-live and produces
// no obligation.
func TestB1W4ARetiredCheckpointReplaysAsATombstone(t *testing.T) {
	r := newCompletionRig(t, []string{codeOn("f1"), codeOn("f2")},
		incompleteTurn{value: 2, report: accounting(answerCode("f1"))},
		incompleteTurn{value: 3, report: accounting(answerCode("f2"))},
	)
	if outcome, err := r.run(); err != nil || !outcome.Accepted() {
		t.Fatalf("premise: the cycle did not complete: %q %v", outcome, err)
	}
	got := committedOf(t, r.h.engine, "task-1")
	if got.status != "retired" || got.retirement != "completed" || got.rep.Completion != nil || got.rep.Obligation.Completion != nil {
		t.Fatalf("the completed cycle's checkpoint is not a typed tombstone: %+v", got)
	}
	if rep, err := got.replay("live", nil); err == nil {
		t.Fatalf("a completed obligation replayed as live: %+v", rep.Completion)
	}
}

// W5 UNKNOWN STATUS. A fifth status is refused before anything is prepared,
// and a committed payload replayed under one is refused.
func TestB1W5AFifthStatusIsRefusedBeforeCommitment(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	commitLive(t, e, c)
	for _, status := range []string{"serving", "closed"} {
		before, _ := committedRecords(t, e)
		_, err := e.commitCheckpoint(c, "serving", "", fixtureCandidate)
		var unrecorded *ObligationPersistenceFailed
		if !errors.As(err, &unrecorded) || unrecorded.Attempts != 0 {
			t.Fatalf("status %s was not refused before commitment: %v", status, err)
		}
		if after, _ := committedRecords(t, e); after != before {
			t.Fatalf("status %s was prepared", status)
		}
		if _, err := committedOf(t, e, "task-c").replay(status, nil); err == nil {
			t.Fatalf("a checkpoint replayed under status %s", status)
		}
	}
}

// W7 OWNER-CONSTRUCTIBILITY. Each input below is syntactically valid on its
// own; each describes a state a landed owner refuses to construct, and replay
// refuses it.
func TestB1W7AnInputTheOwnerCannotConstructDoesNotReplay(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"), codeObligationFinding("c2"))
	reconcile(t, e, c, accounting(answerCode("c1")), map[string]bool{"main.go": true}, false)
	commitLive(t, e, c)
	got := committedOf(t, e, "task-c")
	for name, mutate := range map[string]func(*replayInputs){
		"an invocation 70A2 settled as a transport failure": func(in *replayInputs) {
			in.Steps[0].Settled.Invocation.TransportFailed = true
		},
		"an invocation settled in another cycle":    func(in *replayInputs) { in.Steps[0].Settled.Cycle = 3 },
		"a route outside the closed vocabulary":     func(in *replayInputs) { in.Steps[1].Route = "retry_later" },
		"a step kind outside the closed vocabulary": func(in *replayInputs) { in.Steps[1].Kind = "note" },
		"a review owing only minor findings": func(in *replayInputs) {
			for i := range in.Review.Findings {
				in.Review.Findings[i].Severity = roles.Minor
			}
		},
		"a judging set outside the closed vocabulary": func(in *replayInputs) { in.Steps[0].Judging = "some" },
	} {
		if rep, err := got.replay("", mutate); err == nil {
			t.Fatalf("%s replayed: %+v", name, rep.Completion)
		}
	}
	// A live obligation its own recorded inputs do not reproduce records
	// nothing: it is refused before anything is prepared.
	before, _ := committedRecords(t, e)
	retry(t, e, c)
	c.Why["c2"] = "a reason no owner gave"
	_, err := e.commitCheckpoint(c, "live", "", fixtureCandidate)
	var unrecorded *ObligationPersistenceFailed
	if !errors.As(err, &unrecorded) || unrecorded.Attempts != 0 {
		t.Fatalf("an obligation its inputs do not reproduce was not refused before commitment: %v", err)
	}
	if after, _ := committedRecords(t, e); after != before {
		t.Fatal("an obligation its inputs do not reproduce was prepared")
	}
}

// W8 CANDIDATE OWNER. A candidate measurement whose every field is well formed
// but which no capture could have produced -- a failure that also froze a tree
// -- is refused by the 70A4 owner, so the checkpoint does not replay.
func TestB1W8ACandidateThe70A4OwnerCannotConstructDoesNotReplay(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	commitLive(t, e, c)
	got := committedOf(t, e, "task-c")
	for name, mutate := range map[string]func(*replayInputs){
		"a failed capture that froze a tree": func(in *replayInputs) { in.Candidate.Failure = "worktree unreadable" },
		"a capture that froze no tree":       func(in *replayInputs) { in.Candidate.Tree = "" },
		"a measurement naming no capture":    func(in *replayInputs) { in.Candidate.What = "" },
	} {
		if _, err := got.replay("", mutate); err == nil {
			t.Fatalf("%s replayed", name)
		}
	}
	if _, err := e.commitCheckpoint(c, "live", "", candidateInput{What: "x", Tree: "t", Failure: "unreadable"}); err == nil {
		t.Fatal("a checkpoint whose candidate the owner refuses was committed")
	}
}

// W9 EVIDENCE OWNER. An evidence response is satisfied only by the record
// replay loads again from durable task state under its canonical identity. An
// identity that is absent, stale or bound to another candidate leaves the
// finding owed, and the replay no longer matches the committed ReplayDigest.
func TestB1W9EvidenceReplaysOnlyFromItsDurableRecord(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, evidenceOn("e1"), codeObligationFinding("c1"))
	reconcile(t, e, c, accounting(`{"id":"e1","answered_by":"evidence","evidence":"go test ./..."}`), nil, true)
	if !equalStrings(c.RetainedIDs(), "e1") {
		t.Fatalf("premise: the evidence finding was not discharged by its read-back record: %+v", c)
	}
	commitLive(t, e, c)
	got := committedOf(t, e, "task-c")
	if !retainedIn(got.rep, "e1") {
		t.Fatal("premise: the committed checkpoint does not replay the evidence response as satisfied")
	}
	for name, mutate := range map[string]func(*replayInputs){
		"a stale record identity": func(in *replayInputs) { in.Steps[0].ReadBack["e1"] = strings.Repeat("0", 64) },
		"another candidate":       func(in *replayInputs) { in.Steps[0].EvidenceCandidate.DiffDigest = "sha256:other" },
		"no record identity":      func(in *replayInputs) { in.Steps[0].ReadBack = nil },
		// The record's key is unchanged by each of these, and the durable
		// record disagrees with the copied execution: the record decides.
		"a copied execution another party produced": func(in *replayInputs) {
			for i := range in.Steps[0].Evidence.Checks {
				in.Steps[0].Evidence.Checks[i].ExecutedBy = "the worker"
			}
		},
		"a copied execution with another exit status": func(in *replayInputs) {
			for i := range in.Steps[0].Evidence.Checks {
				in.Steps[0].Evidence.Checks[i].ExitStatus = 7
			}
		},
		"a copied execution with another attribution": func(in *replayInputs) {
			for i := range in.Steps[0].Evidence.Checks {
				in.Steps[0].Evidence.Checks[i].Attribution = "pre-existing"
			}
		},
	} {
		rep, err := got.replay("", mutate)
		if err == nil && (retainedIn(rep, "e1") || rep.Digest == got.replayDigest) {
			t.Fatalf("evidence with %s replayed as satisfied", name)
		}
	}
	// Absent: the same payload against a task state that holds no record.
	other := fixtureEngine(t)
	b, cp, _, _, _ := e.committedCheckpoint("task-c")
	durable, err := e.replaySourcesOf(e.Store)
	if err != nil {
		t.Fatal(err)
	}
	durable.Evidence = other.retainedEvidence
	rep, err := Replay(cp, durable)
	if err == nil && (retainedIn(rep, "e1") || rep.Digest == b.ReplayDigest) {
		t.Fatal("evidence absent from durable task state replayed as satisfied")
	}
	// The committed checkpoint itself, its payload byte for byte, stops being
	// valid once the durable record its evidence stands on is gone.
	e.Repo.Root = t.TempDir()
	if _, _, _, found, err := e.committedCheckpoint("task-c"); !found || err == nil {
		t.Fatalf("a checkpoint whose evidence record is gone still verified: found %v", found)
	}
}

// W10 MOVED-FILE TRUTH. A code response naming main.go is satisfied only by
// the measured change facts. Without them the response's own paths prove
// nothing, and replacing the measurement with "not measured" changes the
// reconstructed obligation.
func TestB1W10AResponseCannotProveItsOwnChange(t *testing.T) {
	e := fixtureEngine(t)
	unmeasured := fixtureObligation(t, e, codeObligationFinding("c1"), codeObligationFinding("c2"))
	reconcile(t, e, unmeasured, accounting(answerCode("c1")), nil, false)
	if !equalStrings(unmeasured.Owed(), "c1", "c2") {
		t.Fatalf("a response's own paths satisfied a code finding without measured change facts: %+v", unmeasured)
	}
	c := obligationUnder(e.operativePlanAttempt("task-c"), codeObligationFinding("c1"), codeObligationFinding("c2"))
	reconcile(t, e, c, accounting(answerCode("c1")), map[string]bool{"main.go": true}, false)
	commitLive(t, e, c)
	got := committedOf(t, e, "task-c")
	if !retainedIn(got.rep, "c1") {
		t.Fatal("premise: the measured change did not satisfy c1 on replay")
	}
	for name, mutate := range map[string]func(*replayInputs){
		"no measurement":         func(in *replayInputs) { in.Steps[0].Moved = &movedFacts{} },
		"another file moved":     func(in *replayInputs) { in.Steps[0].Moved.Paths = []string{"other.go"} },
		"no change facts at all": func(in *replayInputs) { in.Steps[0].Moved = nil },
	} {
		rep, err := got.replay("", mutate)
		if err == nil && (retainedIn(rep, "c1") || rep.Digest == got.replayDigest) {
			t.Fatalf("with %s, c1 still replayed as satisfied", name)
		}
	}
}

// W11 PLANATTEMPT OWNER. A continuation is replayed only from a plan attempt
// record that derives the identity it names. One whose id string matches the
// checkpoint's PlanAttemptID but whose record derives another identity does
// not replay, and neither does an origin that is not the task's.
func TestB1W11AStringMatchedPlanAttemptContinuationDoesNotReplay(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	next := canonicalAttempt(t, e, "task-c", "the revised plan")
	escalateAndContinue(t, e, c, next)
	if c.PlanAttemptID != next.ID || !equalStrings(c.Continuations, c.Origin.Attempt.ID+"->"+next.ID) {
		t.Fatalf("premise: the obligation was not continued: %+v", c)
	}
	commitLive(t, e, c)
	got := committedOf(t, e, "task-c")
	if got.rep.Completion.PlanAttemptID != next.ID {
		t.Fatalf("premise: the continuation did not replay: %+v", got.rep.Completion)
	}
	for name, mutate := range map[string]func(*replayInputs){
		"a continuation whose record derives another id": func(in *replayInputs) {
			in.Steps[2].To.Attempt.Plan.Plan = "a plan nobody routed"
		},
		"a continuation with no record":        func(in *replayInputs) { in.Steps[2].To = nil },
		"an origin of another task":            func(in *replayInputs) { in.Origin.Attempt.TaskID = "task-x" },
		"an origin under another objective":    func(in *replayInputs) { in.Origin.Objective = "another objective" },
		"no continuation to the named attempt": func(in *replayInputs) { in.Steps = in.Steps[:2] },
	} {
		if _, err := got.replay("", mutate); err == nil {
			t.Fatalf("%s replayed", name)
		}
	}

	// THE OBJECTIVE-64 OWNER, not a recomputed hash. Each obligation below
	// names attempts whose identities their own records derive; none was
	// made operative by the recorded transitions in the order it claims, and
	// none is committed.
	minted := mintedAttempt(t, "task-c", "a plan nobody routed")
	if err := minted.binding().verify("task-c"); err != nil {
		t.Fatalf("premise: the minted attempt does not derive its own identity: %v", err)
	}
	reversed := fixtureEngine(t)
	later := canonicalAttempt(t, reversed, "task-c", "the revised plan")
	earlier := canonicalAttempt(t, reversed, "task-c", "the plan")
	for name, tc := range map[string]struct {
		e *Engine
		c func() *cycleCompletion
	}{
		"an origin never started or adopted": {e, func() *cycleCompletion {
			c := obligationUnder(minted, codeObligationFinding("c1"))
			retry(t, e, c)
			return c
		}},
		"a continuation to an attempt never started or adopted": {e, func() *cycleCompletion {
			c := obligationUnder(e.operativePlanAttempt("task-c"), codeObligationFinding("c1"))
			escalateAndContinue(t, e, c, minted)
			return c
		}},
		"a continuation to an attempt made operative before its origin": {reversed, func() *cycleCompletion {
			c := obligationUnder(earlier, codeObligationFinding("c1"))
			escalateAndContinue(t, reversed, c, later)
			return c
		}},
	} {
		before, _ := committedRecords(t, tc.e)
		_, err := tc.e.commitCheckpoint(tc.c(), "live", "", fixtureCandidate)
		var unrecorded *ObligationPersistenceFailed
		if !errors.As(err, &unrecorded) || unrecorded.Attempts != 0 {
			t.Fatalf("%s was not refused before commitment: %v", name, err)
		}
		if after, _ := committedRecords(t, tc.e); after != before {
			t.Fatalf("%s was prepared", name)
		}
	}
}

// W12 LIFECYCLE OWNER. The settled invocation's open operations are 70A2's
// generations of its observations, never stored. An observation stream under
// which the open generation is not constructible -- a terminal no start
// opened -- reconstructs another obligation and fails the ReplayDigest.
func TestB1W12LifecycleGenerationsAreReplayedThrough70A2(t *testing.T) {
	report := accounting(answerCode("f1"))
	r := newCompletionRig(t, []string{codeOn("f1")},
		incompleteTurn{value: 2, report: report, inv: structured(report, started(0, "op-1"))},
		incompleteTurn{value: 3, unobserved: true, err: errors.New("the worker crashed")},
	)
	r.run()
	got := committedOf(t, r.h.engine, "task-1")
	if !equalStrings(got.rep.Completion.OpenOperations, "op-1#1") {
		t.Fatalf("premise: the live checkpoint does not replay the open generation: %+v", got.rep.Completion)
	}
	for name, mutate := range map[string]func(*replayInputs){
		"a terminal no start opened": func(in *replayInputs) {
			in.Steps[0].Settled.Invocation.Lifecycle = append(in.Steps[0].Settled.Invocation.Lifecycle[:0], terminal(0, "op-1"))
		},
		"a transport without lifecycle": func(in *replayInputs) { in.Steps[0].Settled.Invocation.Transport.Lifecycle = false },
	} {
		rep, err := got.replay("", mutate)
		if err == nil && rep.Digest == got.replayDigest {
			t.Fatalf("%s replayed to the committed ReplayDigest", name)
		}
	}
}

// W15 PREPARE/COMMIT. The durable operations run in exactly the canonical
// order, and the checkpoint is not eligible until its COMMITTED record exists.
func TestB1W15ACheckpointIsEligibleOnlyAfterItsCommit(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	var beforeCommit bool
	ops := failCheckpointIO(t, func(op string, _ int) bool {
		if op == checkpointCommit {
			_, _, _, found, _ := e.committedCheckpoint("task-c")
			beforeCommit = found
		}
		return false
	})
	id := commitLive(t, e, c)
	if strings.Join(*ops, ",") != "load,prepare,write,read,commit" {
		t.Fatalf("the transaction ran %v", *ops)
	}
	if beforeCommit {
		t.Fatal("the checkpoint was eligible before its COMMITTED record")
	}
	if got := committedOf(t, e, "task-c"); got.id != id || got.rep.Digest != got.replayDigest {
		t.Fatalf("the committed checkpoint is not eligible: %+v", got)
	}
}

// W15 READBACK. A payload that does not read back as the payload prepared --
// here corrupted on disk between its write and its read -- is never
// committed, however many attempts are made.
func TestB1W15APayloadThatDoesNotReadBackIsNeverCommitted(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	dir := e.Repo.Root + "/.sensei-code/sessions/session-1/checkpoints/"
	failCheckpointIO(t, func(op string, _ int) bool {
		if op != checkpointRead {
			return false
		}
		entries, err := os.ReadDir(dir)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			raw, err := os.ReadFile(dir + entry.Name())
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(dir+entry.Name(), []byte(strings.Replace(string(raw), `"live"`, `"blocked"`, 1)), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		return false
	})
	retry(t, e, c)
	_, err := e.commitCheckpoint(c, "live", "", fixtureCandidate)
	var unrecorded *ObligationPersistenceFailed
	if !errors.As(err, &unrecorded) || unrecorded.Attempts != maxCheckpointAttempts {
		t.Fatalf("a corrupted read-back was not refused on every attempt: %v", err)
	}
	if _, committed := committedRecords(t, e); committed != 0 {
		t.Fatal("a payload that did not read back as prepared was committed")
	}
}

// W16 PERSIST RETRY. Two failed writes and then a success commit exactly one
// checkpoint, from the third complete attempt, with nothing else committed.
func TestB1W16TwoFailedWritesThenOneCommittedCheckpoint(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	failCheckpointIO(t, func(op string, call int) bool { return op == checkpointWrite && call <= 2 })
	id := commitLive(t, e, c)
	prepared, committed := committedRecords(t, e)
	if prepared != 3 || committed != 1 {
		t.Fatalf("expected three prepared attempts and one commit, got %d and %d", prepared, committed)
	}
	if got := committedOf(t, e, "task-c"); got.id != id {
		t.Fatalf("the committed checkpoint is not the third attempt's: %s / %s", got.id, id)
	}
}

// W17 THREE FAILURES, W18 PRIOR PRESERVED. Three failed writes return the typed
// failure, commit nothing new, and the previously committed checkpoint stands
// unchanged and verifiable.
func TestB1W17W18ThreeFailedWritesPreserveThePriorCheckpoint(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	_, err := e.commitCheckpoint(c, "live", "", fixtureCandidate)
	if err == nil {
		t.Fatal("premise: an obligation that is not live was committed live")
	}
	prior := commitLive(t, e, c)
	_, committedBefore := committedRecords(t, e)
	failCheckpointIO(t, func(op string, _ int) bool { return op == checkpointWrite })
	retry(t, e, c)
	_, err = e.commitCheckpoint(c, "live", "", fixtureCandidate)
	var unrecorded *ObligationPersistenceFailed
	if !errors.As(err, &unrecorded) || unrecorded.Attempts != maxCheckpointAttempts ||
		unrecorded.Prior != PriorCheckpointKnown || unrecorded.PriorCheckpointID != prior {
		t.Fatalf("three failed writes are not the typed failure over the known prior checkpoint: %v", err)
	}
	if _, committed := committedRecords(t, e); committed != committedBefore {
		t.Fatal("a failed checkpoint was committed")
	}
	got := committedOf(t, e, "task-c")
	if got.id != prior || got.status != "live" || got.rep.Completion.Attempts != 1 {
		t.Fatalf("the prior committed checkpoint did not stand: %+v", got)
	}

	fresh := fixtureEngine(t)
	if canonicalAttempt(t, fresh, "task-c", "the plan").ID != c.PlanAttemptID {
		t.Fatal("premise: the fresh session did not make the obligation's plan attempt operative")
	}
	_, err = fresh.commitCheckpoint(c, "live", "", fixtureCandidate)
	if !errors.As(err, &unrecorded) || unrecorded.Prior != PriorCheckpointAbsent || unrecorded.PriorCheckpointID != "" {
		t.Fatalf("a store read that found no checkpoint is not established absence: %v", err)
	}
}

// W19 UNREADABLE STORE. A store that cannot be read says nothing about earlier
// checkpoints: the failure states that availability is UNKNOWN, never absence.
func TestB1W19AnUnreadableStoreIsUnknownNotAbsent(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	commitLive(t, e, c)
	failCheckpointIO(t, func(op string, _ int) bool { return op == checkpointLoad })
	retry(t, e, c)
	_, err := e.commitCheckpoint(c, "live", "", fixtureCandidate)
	var unrecorded *ObligationPersistenceFailed
	if !errors.As(err, &unrecorded) || unrecorded.Prior != PriorCheckpointUnknown || unrecorded.PriorCheckpointID != "" {
		t.Fatalf("an unreadable store was not reported as UNKNOWN: %v", err)
	}
	if msg := err.Error(); !strings.Contains(msg, "UNKNOWN") || strings.Contains(msg, "no earlier checkpoint") {
		t.Fatalf("the failure reads as absence: %s", msg)
	}
}

// W19 EVERY ATTEMPT READS AGAIN. The first attempt's read succeeds and finds
// the prior checkpoint, its write fails, and the next two attempts cannot
// read the store: what the first read established is not reported, and the
// prior checkpoint's availability is UNKNOWN.
func TestB1W19AReadThatFailsAfterASuccessfulOneIsUnknown(t *testing.T) {
	for name, firstRead := range map[string]bool{"a prior checkpoint": true, "no prior checkpoint": false} {
		// A subtest each, so one case's injected failures end with it.
		t.Run(name, func(t *testing.T) {
			e := fixtureEngine(t)
			c := fixtureObligation(t, e, codeObligationFinding("c1"))
			if firstRead {
				commitLive(t, e, c)
			}
			failCheckpointIO(t, func(op string, call int) bool {
				return op == checkpointWrite || (op == checkpointLoad && call > 1)
			})
			retry(t, e, c)
			_, err := e.commitCheckpoint(c, "live", "", fixtureCandidate)
			var unrecorded *ObligationPersistenceFailed
			if !errors.As(err, &unrecorded) || unrecorded.Attempts != maxCheckpointAttempts ||
				unrecorded.Prior != PriorCheckpointUnknown || unrecorded.PriorCheckpointID != "" {
				t.Fatalf("%s: a store unreadable on the last attempt was not reported UNKNOWN: %v", name, err)
			}
		})
	}
}

// W19 KNOWN MEANS VERIFIED. A prior committed checkpoint whose payload no
// longer verifies -- unreadable, or not the bytes its record binds -- is not a
// KNOWN prior and is never chained to: its availability is UNKNOWN, and
// nothing new is committed over it.
func TestB1W19APriorCheckpointThatDoesNotVerifyIsUnknown(t *testing.T) {
	for name, damage := range map[string]func(path string) error{
		"an unreadable payload": os.Remove,
		"a corrupted payload": func(path string) error {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, []byte(strings.Replace(string(raw), `"live"`, `"blocked"`, 1)), 0o600)
		},
		"a payload with a trailing value": func(path string) error {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			return os.WriteFile(path, append(raw, []byte(" {}")...), 0o600)
		},
	} {
		e := fixtureEngine(t)
		c := fixtureObligation(t, e, codeObligationFinding("c1"))
		prior := commitLive(t, e, c)
		if err := damage(e.Repo.Root + "/.sensei-code/sessions/session-1/checkpoints/" + prior + ".json"); err != nil {
			t.Fatal(err)
		}
		_, committedBefore := committedRecords(t, e)
		retry(t, e, c)
		_, err := e.commitCheckpoint(c, "live", "", fixtureCandidate)
		var unrecorded *ObligationPersistenceFailed
		if !errors.As(err, &unrecorded) || unrecorded.Prior != PriorCheckpointUnknown || unrecorded.PriorCheckpointID != "" {
			t.Fatalf("%s: an unverifiable prior checkpoint was reported %v", name, err)
		}
		if _, committed := committedRecords(t, e); committed != committedBefore {
			t.Fatalf("%s: a checkpoint was committed over an unverifiable prior", name)
		}
	}
}

// THE PERSISTENCE FAILURE IS TERMINAL. Every checkpoint commit fails, so the
// first counted retry's live obligation cannot be committed. Through
// implement() with a second implementor configured, the run ends on the typed
// persistence failure: no handoff is created, and no other implementer serves
// the unrecorded obligation.
func TestB1W17APersistenceFailureIsNeverHandedToAnotherImplementer(t *testing.T) {
	r := exhaustingRig(t, 2)
	second := r.h.worker
	second.Name = "gemini"
	r.h.engine.Config.Implementors = append(r.h.engine.Config.Implementors, second)
	failCheckpointIO(t, func(op string, _ int) bool { return op == checkpointCommit })
	failed, events := runImplement(r.h)
	var unrecorded *ObligationPersistenceFailed
	if !errors.As(failed, &unrecorded) || unrecorded.Attempts != maxCheckpointAttempts || unrecorded.Status != "live" {
		t.Fatalf("implement() did not end on the typed persistence failure: %v", failed)
	}
	if contains(events, event.HandoffCreated) {
		t.Fatalf("the unrecorded obligation was handed off: %v", kinds(events))
	}
	settled := r.h.engine.settledInvocations("task-1")
	if !equalInts(r.cycles(), 1, 2) {
		t.Fatalf("another implementer served the unrecorded obligation: cycles %v", r.cycles())
	}
	for _, s := range settled {
		if s.Provider == "gemini" {
			t.Fatal("the second implementor was invoked after the persistence failure")
		}
	}
}

// W21 NIL STORE. With no store nothing is committed: the first counted retry
// already fails typed, no exhaustion, retirement or success is produced, and
// what is known of a prior checkpoint is UNKNOWN.
func TestB1W21ANilStoreCannotProduceDurableState(t *testing.T) {
	r := exhaustingRig(t, 2)
	r.h.engine.Store = nil
	_, err := r.run()
	var incomplete *ImplementerIncomplete
	var unrecorded *ObligationPersistenceFailed
	if errors.As(err, &incomplete) || !errors.As(err, &unrecorded) {
		t.Fatalf("a nil store produced something other than the typed persistence failure: %v", err)
	}
	if unrecorded.Status != "live" || unrecorded.Prior != PriorCheckpointUnknown || !equalInts(r.cycles(), 1, 2) {
		t.Fatalf("the nil store's failure is not the first retry's, over an UNKNOWN prior: %+v (cycles %v)", unrecorded, r.cycles())
	}
	fixture := fixtureEngine(t)
	c := fixtureObligation(t, fixture, codeObligationFinding("c1"))
	retry(t, fixture, c)
	retry(t, fixture, c)
	retry(t, fixture, c)
	e := &Engine{}
	if err := e.exhaustCycle(context.Background(), nil, "", c, nil, ""); !errors.As(err, &unrecorded) || errors.As(err, &incomplete) {
		t.Fatalf("a nil store reached the typed exhaustion: %v", err)
	}
	c.CheckpointID = strings.Repeat("a", 64)
	if err := e.retireCycle(context.Background(), nil, "", c, "completed"); !errors.As(err, &unrecorded) {
		t.Fatalf("a nil store retired a checkpoint: %v", err)
	}
}

// W3/W7 THE CYCLE OWNER'S SEQUENCING. The 70A3 owner admits only sequences
// the live workflow can take, live and on replay: no counted transition once
// the allowance is spent, so an exhausted cycle has exactly the maximum; no
// route or account reopens a closed cycle; and a completed retirement
// tombstones only a cycle that progressed.
func TestB1W3W7TheCycleOwnerAdmitsOnlyLiveSequences(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, codeObligationFinding("c1"))
	retry(t, e, c)
	retry(t, e, c)
	retry(t, e, c)
	if !c.Exhausted() || c.Attempts != maxIncompleteImplementerAttempts || c.Continuing {
		t.Fatalf("premise: the third retry did not exhaust the cycle: %+v", c)
	}
	held := len(c.Steps)
	if _, err := c.transition(routeIncompleteRetry); err == nil {
		t.Fatal("a fourth retry was admitted after the allowance was spent")
	}
	if err := c.admit(replayStep{Kind: stepAccount, Judging: judgeAll}); err == nil {
		t.Fatal("an invocation was accounted to an exhausted cycle")
	}
	if len(c.Steps) != held || c.Attempts != maxIncompleteImplementerAttempts {
		t.Fatalf("a refused transition changed the cycle: %+v", c)
	}
	if _, err := e.commitCheckpoint(c, "exhausted", "", fixtureCandidate); err != nil {
		t.Fatalf("premise: the exhausted checkpoint was not committed: %v", err)
	}
	got := committedOf(t, e, "task-c")
	for name, mutate := range map[string]func(*replayInputs){
		"a fourth retry": func(in *replayInputs) {
			n := len(in.Steps)
			in.Steps = append(in.Steps, in.Steps[n-2], in.Steps[n-1])
		},
		"an account after exhaustion": func(in *replayInputs) { in.Steps = append(in.Steps, in.Steps[len(in.Steps)-2]) },
		"a provider rebound reopening it": func(in *replayInputs) {
			in.Steps = append(in.Steps, replayStep{Kind: stepRoute, Route: routeProviderRebound})
		},
		"a retry with no account before it": func(in *replayInputs) {
			in.Steps = append(in.Steps[:0:0], in.Steps[1], in.Steps[1], in.Steps[1])
		},
	} {
		if rep, err := got.replay("", mutate); err == nil {
			t.Fatalf("%s replayed: %+v", name, rep.Completion)
		}
	}

	// A completed cycle is closed: nothing reopens it, live or replayed.
	done := obligationUnder(e.operativePlanAttempt("task-c"), codeObligationFinding("c1"))
	reconcile(t, e, done, accounting(answerCode("c1")), map[string]bool{"main.go": true}, false)
	if _, err := done.transition(routeProgress); err != nil {
		t.Fatalf("premise: the completed cycle did not progress: %v", err)
	}
	for _, route := range []cycleRoute{routeProviderRebound, routeOrdinaryError, routeIncompleteRetry, routeErrorHandoff} {
		if _, err := done.transition(route); err == nil || done.Continuing || done.Route != routeProgress {
			t.Fatalf("route %s reopened a completed cycle: %+v", route, done)
		}
	}
	if err := done.admit(replayStep{Kind: stepAccount, Judging: judgeUnsettled}); err == nil {
		t.Fatal("an invocation was accounted to a completed cycle")
	}
	if _, err := e.commitCheckpoint(done, "retired", "completed", fixtureCandidate); err != nil {
		t.Fatalf("premise: the completed cycle was not retired: %v", err)
	}
	tomb := committedOf(t, e, "task-c")
	if tomb.status != "retired" {
		t.Fatalf("premise: the tombstone is not the committed checkpoint: %+v", tomb)
	}
	if _, err := tomb.replay("", func(in *replayInputs) {
		in.Steps = append(in.Steps, replayStep{Kind: stepRoute, Route: routeProviderRebound})
	}); err == nil {
		t.Fatal("a provider rebound after progress replayed")
	}
	// A completed retirement of a cycle that never progressed is refused.
	live := obligationUnder(e.operativePlanAttempt("task-c"), codeObligationFinding("c1"))
	retry(t, e, live)
	if _, err := e.commitCheckpoint(live, "retired", "completed", fixtureCandidate); err == nil {
		t.Fatal("a cycle that never progressed was retired as completed")
	}
}

// W11 OPERATIVE AT THE BOUNDARY. A checkpoint's obligation must be owned by
// the attempt operative at its own PREPARED record: one naming an attempt that
// was operative once and superseded since is refused; a checkpoint committed
// while it was operative still verifies, because its replay reads the record
// as it stood then; a superseded retirement is proven by the transition that
// superseded it; and a transition recorded only after PREPARED establishes
// nothing.
func TestB1W11ThePlanAttemptMustBeOperativeAtTheCheckpointBoundary(t *testing.T) {
	e := fixtureEngine(t)
	a := canonicalAttempt(t, e, "task-c", "the plan")
	c := obligationUnder(a, codeObligationFinding("c1"))
	prior := commitLive(t, e, c)
	canonicalAttempt(t, e, "task-c", "the revised plan")
	if got := committedOf(t, e, "task-c"); got.id != prior || got.rep.Digest != got.replayDigest {
		t.Fatalf("a checkpoint committed while its attempt was operative no longer verifies: %+v", got)
	}
	retry(t, e, c)
	before, _ := committedRecords(t, e)
	_, err := e.commitCheckpoint(c, "live", "", fixtureCandidate)
	var unrecorded *ObligationPersistenceFailed
	if !errors.As(err, &unrecorded) || unrecorded.Attempts != 0 {
		t.Fatalf("a live checkpoint naming a superseded plan attempt was not refused: %v", err)
	}
	if after, _ := committedRecords(t, e); after != before {
		t.Fatal("a live checkpoint naming a superseded plan attempt was prepared")
	}

	// The superseded retirement: proven by the transition that superseded A.
	disputed := obligationUnder(a, codeObligationFinding("c1"))
	reconcile(t, e, disputed, accounting(), nil, false)
	if _, err := disputed.transition(routeDisputeEscalation); err != nil {
		t.Fatal(err)
	}
	if _, err := e.commitCheckpoint(disputed, "retired", "superseded_plan_attempt", fixtureCandidate); err != nil {
		t.Fatalf("the retirement of an obligation whose attempt was superseded was refused: %v", err)
	}
	current := obligationUnder(e.operativePlanAttempt("task-c"), codeObligationFinding("c1"))
	reconcile(t, e, current, accounting(), nil, false)
	if _, err := current.transition(routeDisputeEscalation); err != nil {
		t.Fatal(err)
	}
	if _, err := e.commitCheckpoint(current, "retired", "superseded_plan_attempt", fixtureCandidate); err == nil {
		t.Fatal("an obligation of the operative attempt was retired as superseded")
	}

	// A transition that appears only after PREPARED.
	e2 := fixtureEngine(t)
	a2 := canonicalAttempt(t, e2, "task-c", "the plan")
	b2 := canonicalAttempt(t, e2, "task-c", "the revised plan")
	c2 := obligationUnder(a2, codeObligationFinding("c1"))
	escalateAndContinue(t, e2, c2, b2)
	id := commitLive(t, e2, c2)
	binding, _, _, _, err := e2.committedCheckpoint("task-c")
	if err != nil {
		t.Fatal(err)
	}
	history, err := e2.Store.ReadRecord()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := e2.Store.ReadCheckpoint(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := verifyCheckpoint(payload, binding, replaySources{Record: history, Evidence: e2.retainedEvidence}, e2.SessionID); err != nil {
		t.Fatalf("premise: the continued checkpoint does not verify: %v", err)
	}
	prepared, transition := -1, -1
	for i, ev := range history {
		if ev.Kind == event.CheckpointPrepared && strings.Contains(string(ev.Payload), id) {
			prepared = i
		}
		if ev.Kind == event.PlanProposed && strings.Contains(string(ev.Payload), b2.ID) && transition < 0 {
			transition = i
		}
	}
	if prepared < 0 || transition < 0 || transition > prepared {
		t.Fatalf("premise: the record does not hold B's transition before PREPARED: %d %d", transition, prepared)
	}
	var reordered []event.Event
	for i, ev := range history {
		if i == transition {
			reordered = append(reordered, history[prepared])
		}
		if i != prepared {
			reordered = append(reordered, ev)
		}
	}
	if _, _, err := verifyCheckpoint(payload, binding, replaySources{Record: reordered, Evidence: e2.retainedEvidence}, e2.SessionID); err == nil {
		t.Fatal("a transition recorded only after PREPARED established the checkpoint's continuation")
	}
}

// W22 REPLAY DIGEST. Every load-bearing input is covered: changing any one of
// them reconstructs an obligation that does not match the committed digest.
func TestB1W22EveryLoadBearingInputMovesTheReplayDigest(t *testing.T) {
	e := fixtureEngine(t)
	c := fixtureObligation(t, e, evidenceOn("e1"), codeObligationFinding("c1"), codeObligationFinding("c2"))
	reconcile(t, e, c, accounting(`{"id":"e1","answered_by":"evidence","evidence":"go test ./..."}`, answerCode("c1")),
		map[string]bool{"main.go": true}, true)
	commitLive(t, e, c)
	got := committedOf(t, e, "task-c")
	for name, mutate := range map[string]func(*replayInputs){
		"the cycle":            func(in *replayInputs) { in.Cycle = 3; in.Steps[0].Settled.Cycle = 3 },
		"the review attempt":   func(in *replayInputs) { in.Review.Attempt = 2 },
		"a finding's claim":    func(in *replayInputs) { in.Review.Findings[2].Claim = "another claim" },
		"the raised candidate": func(in *replayInputs) { in.Review.CandidateTree = "another-tree" },
		"the report":           func(in *replayInputs) { in.Steps[0].Settled.Invocation.Report = accounting(answerCode("c1")) },
		"the moved files":      func(in *replayInputs) { in.Steps[0].Moved.Paths = nil },
		"the evidence":         func(in *replayInputs) { in.Steps[0].Evidence.Checks = nil },
		"the route":            func(in *replayInputs) { in.Steps[1].Route = routeProviderRebound },
		"the candidate tree":   func(in *replayInputs) { in.Candidate.Tree = "another-tree" },
		"the candidate base":   func(in *replayInputs) { in.Candidate.Base = "another-base" },
		"an extra attempt":     func(in *replayInputs) { in.Steps = append(in.Steps, in.Steps[1]) },
	} {
		rep, err := got.replay("", mutate)
		if err == nil && rep.Digest == got.replayDigest {
			t.Fatalf("changing %s left the ReplayDigest unchanged", name)
		}
	}
	if rep, err := got.replay("", nil); err != nil || rep.Digest != got.replayDigest {
		t.Fatalf("premise: the unchanged inputs do not replay to the committed digest: %v", err)
	}
}

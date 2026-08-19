package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
	"time"

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

func TestIsStopOption(t *testing.T) {
	for _, label := range []string{"Stop this task", "Cancel", "Abort operation"} {
		if !isStopOption(label) {
			t.Fatalf("expected %q to stop", label)
		}
	}
	if isStopOption("Preserve the contract") {
		t.Fatal("normal authority option must not stop the task")
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
	got := implementationPrompt(testContext(), "edit main.go", "", 1, nil)
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
	got := reviewPrompt(testContext(), "edit main.go", "diff --git a b", "audit says fine", "passed go test ./...")
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
		[]string{"use the existing version constant, do not add a package"})
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
	got := implementationPrompt(testContext(), "plan", "the previous worker left this unresolved", 1, nil)
	if !strings.Contains(got, "the previous worker left this unresolved") {
		t.Fatal("a handover did not reach the next worker's first cycle")
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
		"architect": architecturePrompt("/repo", "d", "ChatGPT", "task", "", "ws", "pf"),
		"worker":    implementationPrompt(testContext(), "plan", "", 1, nil),
		"reviewer":  reviewPrompt(testContext(), "plan", "diff", "audit", "evidence"),
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
		nil, "workspace evidence", "preflight evidence", "(none)")
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
	got := reviewPrompt(testContext(), "plan", "diff", "audit", "passed  go test ./...\n  exit 0")
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
	e.run(ctx, "task-1", "do something")

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
	for _, forbidden := range []string{"authority.Persist", "senseiProposer", "isStopOption"} {
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

	produced := strings.Index(body, "ProducedNoWork")
	removal := strings.Index(body, "RemoveWorktree")
	if produced < 0 {
		t.Fatal("disposal no longer consults whether the candidate holds work")
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

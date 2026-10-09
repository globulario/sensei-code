package workflow

// Two defects the real standing task exposed, witnessed before repair.
//
// 1. The authority identity a live answer carries does not survive deferral.
//    The question is asked ABOUT a plan's file scope; the deferral record drops
//    it; `resumeAuthority` supplies none; and `Covers` — correctly — refuses to
//    apply an unscoped answer to a scoped re-derivation. The human answers and
//    the router asks again.
//
// 2. A human choosing Stop is recorded as an execution FAILURE. The stop is an
//    anonymous `errors.New`, indistinguishable from a broken run, so the
//    behavioural record learns that this task shape breaks when in fact a
//    person declined it.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/behavioral"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
)

// scopedQuestion is the real shape: a Level-3 condition reached about a plan
// that names files. task-1788804009410633746 is exactly this.
const scopedCondition = "Sensei reported a blind spot this router has no reading for: anchors present, no high-risk category fired"

// realOptions are byte-identical to what task-1788804009410633746 recorded:
// composeAuthorityOptions assigns every outcome itself, and the witnesses must
// exercise those outcomes rather than bare labels.
func realOptions() []authority.Option {
	return []authority.Option{
		{ID: "1", Label: "Authorize the architectural change described above", Outcome: authority.Authorize},
		{ID: "2", Label: "Require another design (this change may still be right; this plan is not)", Outcome: authority.Revise},
		{ID: "3", Label: "Stop this task", Outcome: authority.Stop},
	}
}

func planScope() []string {
	return []string{"internal/ghbridge/snapshot.go", "internal/ghbridge/runner_test.go"}
}

// collect subscribes and returns the events emitted, with payloads intact.
// drain() in engine_test.go keeps only kinds, and the payload is the evidence
// here.
func collect(t *testing.T, bus *event.Bus) (func() []event.Event, func()) {
	t.Helper()
	ch, done := bus.Subscribe(64)
	return func() []event.Event {
		var out []event.Event
		for {
			select {
			case ev := <-ch:
				out = append(out, ev)
			default:
				return out
			}
		}
	}, done
}

func payloadOf(t *testing.T, evs []event.Event, kind event.Kind, into any) {
	t.Helper()
	for _, ev := range evs {
		if ev.Kind == kind {
			if err := json.Unmarshal(ev.Payload, into); err != nil {
				t.Fatalf("%s payload did not decode: %v", kind, err)
			}
			return
		}
	}
	t.Fatalf("no %s event was emitted", kind)
}

// deferScoped asks a scoped question and defers it, returning the durable record
// exactly as the session log holds it.
func deferScoped(t *testing.T, taskID string, scope []string) DeferredAuthority {
	t.Helper()
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := withFixtureStore(t, &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}, taskID)

	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(fixtureCtx(e, taskID), nil, taskID, scopedCondition, "github.com/globulario/sensei-code",
			"e7d3fede98ff88b89904b096a40b363adc9c5667",
			authority.Decision{Level: authority.Human, Subject: "Architectural authority reached a human-owned boundary.", Options: realOptions()},
			realOptions(), scope...)
		errc <- err
	}()
	waitForPending(t, e, taskID)
	if !e.DeferAuthority(taskID) {
		t.Fatal("the deferral was refused")
	}
	if err := <-errc; !errors.Is(err, errAuthorityDeferred) {
		t.Fatalf("deferral produced %v", err)
	}
	var q DeferredAuthority
	payloadOf(t, take(), event.WorkflowAwaitingAuthority, &q)
	return q
}

// answerRestored answers a restored question the way resumeAuthority must, and
// returns the Resolution that was durably recorded.
func answerRestored(t *testing.T, q DeferredAuthority, taskID, optionID string) authority.Resolution {
	t.Helper()
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := withFixtureStore(t, &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}, taskID)

	type res struct {
		choice string
		err    error
	}
	out := make(chan res, 1)
	go func() {
		choice, err := e.awaitChoice(fixtureCtx(e, taskID), nil, taskID, q.Condition, q.Domain, q.BaseSHA,
			q.Decision, q.Decision.Options, q.Scope...)
		out <- res{choice, err}
	}()
	waitForPending(t, e, taskID)
	if !e.ResolveHuman(taskID, optionID) {
		t.Fatal("the answer was not delivered")
	}
	<-out
	var r authority.Resolution
	payloadOf(t, take(), event.AuthorityResolved, &r)
	return r
}

// WITNESS 1 — the durable question must carry the scope it was asked about.
// Without it the answer cannot be as applicable as a live one, and nothing
// downstream can reconstruct it without inventing facts.
func TestTheDeferredQuestionPreservesTheScopeItWasAskedAbout(t *testing.T) {
	q := deferScoped(t, "task-1", planScope())
	if !q.ScopeRecorded {
		t.Fatal("the deferral does not record that a scope was preserved, so a reader cannot tell 'no files' from 'not preserved'")
	}
	if len(q.Scope) != len(planScope()) {
		t.Fatalf("the deferred question dropped its scope: %v, want %v", q.Scope, planScope())
	}
	for i, f := range planScope() {
		if q.Scope[i] != f {
			t.Fatalf("scope[%d] = %q, want %q", i, q.Scope[i], f)
		}
	}
}

// WITNESS 2 — the repaired loop's whole point: a resumed answer settles the
// re-derived question under the SAME rules the live run used.
func TestAResumedAnswerSettlesTheSameScopedQuestion(t *testing.T) {
	q := deferScoped(t, "task-1", planScope())
	r := authorizeResolution(t, q)
	if !r.Covers(scopedCondition, planScope()) {
		t.Fatalf("the resumed answer does not cover the re-derived question it answered: recorded scope %v", r.Scope)
	}
	if !r.Outcome.Settles() || !r.Outcome.Permits() {
		t.Fatalf("an authorize answer neither settles nor permits: %+v", r.Outcome)
	}
}

// WITNESS 3 — and it must not settle a question about different files. Reusing
// one yes across plans is the defect Covers exists to prevent.
func TestAResumedAnswerDoesNotSettleADifferentFileScope(t *testing.T) {
	q := deferScoped(t, "task-1", planScope())
	r := authorizeResolution(t, q)
	if r.Covers(scopedCondition, []string{"internal/workflow/engine.go"}) {
		t.Fatal("an answer about ghbridge authorised a change to workflow/engine.go")
	}
	// A DIFFERENT condition is a different question, however well the files match.
	if r.Covers("some other certifiability condition", planScope()) {
		t.Fatal("an answer to one condition settled another")
	}
}

// WITNESS 4 — broader/narrower follows the existing Covers contract, unchanged:
// subset-tolerant inward, refusing outward.
func TestResumedScopeFollowsTheExistingCoversContract(t *testing.T) {
	broad := deferScoped(t, "task-broad", planScope())
	rb := authorizeResolution(t, broad)
	if !rb.Covers(scopedCondition, []string{"internal/ghbridge/snapshot.go"}) {
		t.Fatal("a narrower re-derivation inside what was authorised was refused")
	}
	if rb.Covers(scopedCondition, append(planScope(), "internal/ghbridge/transport.go")) {
		t.Fatal("a re-derivation reaching a file nobody authorised was accepted")
	}

	narrow := deferScoped(t, "task-narrow", []string{"internal/ghbridge/snapshot.go"})
	rn := authorizeResolution(t, narrow)
	if rn.Covers(scopedCondition, planScope()) {
		t.Fatal("a narrow authorisation covered a broader re-derivation")
	}

	// Unscoped on BOTH sides still matches exactly, which is the pre-existing
	// rule and must not change.
	none := deferScoped(t, "task-none", nil)
	rnone := authorizeResolution(t, none)
	if !rnone.Covers(scopedCondition, nil) {
		t.Fatal("an unscoped answer no longer covers an unscoped question")
	}
	if rnone.Covers(scopedCondition, planScope()) {
		t.Fatal("an unscoped answer covered a scoped question; Covers was weakened")
	}
}

// WITNESS 5 — the resolution stays bound to the question, not to the process
// that happened to answer it.
func TestTheResumedResolutionIsBoundToTheQuestionsOwnIdentity(t *testing.T) {
	q := deferScoped(t, "task-1788804009410633746", planScope())
	r := authorizeResolution(t, q)
	if r.TaskID != "task-1788804009410633746" {
		t.Fatalf("TaskID = %q", r.TaskID)
	}
	if r.Condition != q.Condition {
		t.Fatalf("Condition = %q, want the question's own", r.Condition)
	}
	if r.Domain != q.Domain {
		t.Fatalf("Domain = %q, want %q", r.Domain, q.Domain)
	}
	if r.BaseSHA != q.BaseSHA {
		t.Fatalf("BaseSHA = %q, want the base the question was asked at (%q)", r.BaseSHA, q.BaseSHA)
	}
	if r.Question != q.Decision.Subject {
		t.Fatalf("Question = %q", r.Question)
	}
}

// WITNESS 6 — resuming continues the same task. It must not submit a new one.
func TestResumeContinuesTheSameTaskAndSubstitutesNoFreshOne(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "resumeAuthority")
	for _, forbidden := range []string{"SubmitGoverned", "SubmitObservation", "SubmitGovernedWithPlan", "SubmitGovernedUnattended"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("resuming reaches %s; a fresh task is not a continuation", forbidden)
		}
	}
	// funcBody yields a flattened token stream, so these are token paths rather
	// than source text.
	for _, want := range []string{"e.execute(", "task.TaskID(", "task.Task("} {
		if !strings.Contains(body, want) {
			t.Errorf("resuming does not continue the same task id and objective: %s absent", want)
		}
	}
	// And the restored scope must actually reach the rendezvous.
	if !strings.Contains(body, "deferred.Scope(") {
		t.Error("resumeAuthority does not restore the question's scope, so its answer cannot cover a scoped re-derivation")
	}
}

// WITNESS 7 — a legacy record, deferred before scope was preserved, must be
// recognised as such rather than read as "no files were in scope".
func TestALegacyDeferralIsDistinguishableFromAnUnscopedOne(t *testing.T) {
	// Exactly the bytes a pre-repair session holds: no scope key at all.
	legacy := []byte(`{"condition":"` + scopedCondition + `","domain":"github.com/globulario/sensei-code","base_sha":"e7d3fede98ff88b89904b096a40b363adc9c5667","decision":{"level":3,"subject":"Architectural authority reached a human-owned boundary.","options":[{"id":"1","label":"Authorize","outcome":"authorize"}]}}`)
	var q DeferredAuthority
	if err := json.Unmarshal(legacy, &q); err != nil {
		t.Fatal(err)
	}
	if q.ScopeRecorded {
		t.Fatal("a legacy record claims its scope was preserved")
	}
	if len(q.Scope) != 0 {
		t.Fatalf("a legacy record invented a scope: %v", q.Scope)
	}
	// A record written now says so, even when the scope is genuinely empty.
	fresh := deferScoped(t, "task-fresh", nil)
	if !fresh.ScopeRecorded {
		t.Fatal("a fresh unscoped deferral is indistinguishable from a legacy one")
	}
}

// WITNESS 8 — a human Stop is a stop. Today it is an anonymous error that the
// run reports as a FAILURE, which teaches the behavioural record that this task
// shape breaks.
func TestAHumanStopIsDistinguishableFromAFailure(t *testing.T) {
	q := deferScoped(t, "task-1", planScope())
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := withFixtureStore(t, &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}, "task-1")

	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(fixtureCtx(e, "task-1"), nil, "task-1", q.Condition, q.Domain, q.BaseSHA,
			q.Decision, q.Decision.Options, q.Scope...)
		errc <- err
	}()
	waitForPending(t, e, "task-1")
	if !e.ResolveHuman("task-1", stopOptionID(t, q)) {
		t.Fatal("the stop answer was not delivered")
	}
	err := <-errc
	if !errors.Is(err, errStoppedByHumanAuthority) {
		t.Fatalf("a human stop produced %v, which no caller can tell apart from a broken run", err)
	}

	evs := take()
	// It still resolves the question: the human answered, they did not decline
	// to answer.
	var r authority.Resolution
	payloadOf(t, evs, event.AuthorityResolved, &r)
	if r.Outcome != authority.Stop {
		t.Fatalf("outcome = %q, want stop", r.Outcome)
	}
	if r.State != authority.Unsupported {
		t.Fatalf("a stop proposed a contract to Sensei: state %q", r.State)
	}
	for _, wrong := range []event.Kind{event.WorkflowFailed, event.WorkflowAwaitingAuthority} {
		if hasKind(kindsOf(evs), wrong) {
			t.Errorf("a human stop emitted %s", wrong)
		}
	}
}

// WITNESS 9 — the three endings an authority boundary can have, at the one place
// that decides them. Behavioural: the terminal EMITTED is the evidence, not the
// presence of a token in the source.
func TestTheAuthorityClassifierDistinguishesTheThreeEndings(t *testing.T) {
	for name, tc := range map[string]struct {
		err        error
		wantKind   event.Kind
		forbidden  []event.Kind
		wantSilent bool
	}{
		"a human stop is a stop": {
			err: errStoppedByHumanAuthority, wantKind: event.WorkflowStopped,
			forbidden: []event.Kind{event.WorkflowFailed},
		},
		"a real error is a failure": {
			err: errors.New("the worker died"), wantKind: event.WorkflowFailed,
			forbidden: []event.Kind{event.WorkflowStopped},
		},
		"a deferral was already accounted for": {
			err: errAuthorityDeferred, wantSilent: true,
			forbidden: []event.Kind{event.WorkflowFailed, event.WorkflowStopped, event.WorkflowAwaitingAuthority},
		},
	} {
		t.Run(name, func(t *testing.T) {
			bus := event.NewBus()
			take, done := collect(t, bus)
			defer done()
			e := withFixtureStore(t, &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}, "task-1")
			e.terminateAuthorityOutcome(fixtureCtx(e, "task-1"), "task-1", "the objective", tc.err)
			kinds := kindsOf(take())
			if tc.wantSilent {
				for _, k := range kinds {
					if k != event.RunReceipt && k != event.Status {
						t.Fatalf("a deferral emitted %s; it was already terminalized", k)
					}
				}
			} else if !hasKind(kinds, tc.wantKind) {
				t.Fatalf("terminal = %v, want %s", kinds, tc.wantKind)
			}
			for _, wrong := range tc.forbidden {
				if hasKind(kinds, wrong) {
					t.Fatalf("emitted %s: %v", wrong, kinds)
				}
			}
		})
	}
}

// WITNESS 10 — the behavioural record must not learn that this task shape breaks
// because a person declined it. The STATUS that reaches the service is the fact
// under test, so a fake service captures it.
func TestTheBehaviouralRecordLearnsStoppedNotFailureFromAHumanStop(t *testing.T) {
	statuses := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params struct {
				Name      string `json:"name"`
				Arguments struct {
					Status string `json:"status"`
				} `json:"arguments"`
			} `json:"params"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if req.Params.Name == "behavioral_record_outcome" {
			statuses <- req.Params.Arguments.Status
		}
		w.Header().Set("Mcp-Session-Id", "sess-1")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{}}`))
	}))
	defer srv.Close()

	newEngine := func() *Engine {
		e := withFixtureStore(t, &Engine{Bus: event.NewBus(), SessionID: "s1", pending: map[string]chan string{}}, "task-1")
		e.Config.Behavioral = behavioral.Config{Enabled: true, URL: srv.URL, Project: "sensei-code", Domain: "d"}
		return e
	}

	stopped := newEngine()
	stopped.terminateAuthorityOutcome(fixtureCtx(stopped, "task-1"), "task-1", "the objective", errStoppedByHumanAuthority)
	select {
	case got := <-statuses:
		if got != "stopped" {
			t.Fatalf("a human stop was recorded as %q; the record now believes this task shape breaks", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no behavioural outcome was recorded for a human stop")
	}

	// And a genuine error still reports a failure, so the distinction is real in
	// both directions rather than everything becoming "stopped".
	failed := newEngine()
	failed.terminateAuthorityOutcome(fixtureCtx(failed, "task-1"), "task-1", "the objective", errors.New("the worker died"))
	select {
	case got := <-statuses:
		if got != "failure" {
			t.Fatalf("a real error was recorded as %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("no behavioural outcome was recorded for a failure")
	}
}

// WITNESS 11 — neither path keeps its own idea of the terminal, so they cannot
// drift apart again. The classifier takes no boolean and returns none, so a call
// site cannot guard it off and fall through.
func TestNeitherAuthorityPathKeepsItsOwnTerminalDecision(t *testing.T) {
	// execute and Resume end through terminateRun, and terminateRun through
	// the authority classifier. Resume used to keep a closure of its own that
	// reported every ending as FAILED, which turned a deferred question raised
	// during a resumed re-plan into a final failure (#194 review at b23a8ae).
	for _, fn := range []string{"execute", "ResumeTask"} {
		if !strings.Contains(funcBody(t, "internal/workflow/engine.go", fn), "terminateRun") {
			t.Errorf("%s does not end through the shared run classifier", fn)
		}
	}
	if strings.Contains(funcBody(t, "internal/workflow/engine.go", "ResumeTask"), "event.WorkflowFailed") {
		t.Error("Resume decides a failure terminal itself instead of through the shared classifier")
	}
	for _, fn := range []string{"terminateRun", "resumeAuthority"} {
		body := funcBody(t, "internal/workflow/engine.go", fn)
		if !strings.Contains(body, "terminateAuthorityOutcome") {
			t.Errorf("%s does not route its authority ending through the shared classifier", fn)
		}
	}
	// resumeAuthority must not re-decide what a deferral or a stop means.
	resume := funcBody(t, "internal/workflow/engine.go", "resumeAuthority")
	for _, forbidden := range []string{"errAuthorityDeferred", "errStoppedByHumanAuthority", "OutcomeStopped"} {
		if strings.Contains(resume, forbidden) {
			t.Errorf("resumeAuthority reaches %s; the ending is not its decision to make", forbidden)
		}
	}
}

// WITNESS 12 — a record bound to another task refuses BEFORE anything starts.
// Reachable with no Sensei at all, which is the proof that it costs nothing.
func TestAQuestionBoundToAnotherTaskIsRefusedBeforeAnythingStarts(t *testing.T) {
	q := deferScoped(t, "task-original", planScope())
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	// FIXTURE MIGRATION (70B2a1): the resumed task's record, rooted by its
	// holder session; this fresh session is bound before anything is recorded.
	e := &Engine{Bus: bus, SessionID: "s1", Store: heldTaskStore(t, "task-someone-else"), pending: map[string]chan string{}}
	bindFixtureSession(t, e.Store, "task-someone-else", e.SessionID)
	// No Repo, no Config.Sensei: if the guard ran late this would fail on
	// starting Sensei instead, with a message naming the wrong thing.
	e.resumeAuthority(fixtureCtx(e, "task-someone-else"), session.Interrupted{
		TaskID: "task-someone-else", Task: "an objective", AwaitingAuthority: raw,
	})
	var named bool
	for _, ev := range take() {
		if ev.Kind == event.WorkflowFailed {
			if !strings.Contains(ev.Summary, "bound to task task-original") {
				t.Fatalf("the refusal does not name the mismatch: %q", ev.Summary)
			}
			if strings.Contains(ev.Summary, "start Sensei") {
				t.Fatal("the guard ran after Sensei was started")
			}
			named = true
		}
	}
	if !named {
		t.Fatal("a question bound to another task was not refused")
	}
}

func authorizeResolution(t *testing.T, q DeferredAuthority) authority.Resolution {
	t.Helper()
	for _, o := range q.Decision.Options {
		if o.Outcome == authority.Authorize {
			return answerRestored(t, q, resolvedTaskID(q), o.ID)
		}
	}
	t.Fatal("the question offers no authorize option")
	return authority.Resolution{}
}

// resolvedTaskID keeps the answer bound to the task the question names, so a
// test cannot accidentally prove binding it did not exercise.
func resolvedTaskID(q DeferredAuthority) string {
	if q.TaskID != "" {
		return q.TaskID
	}
	return "task-1"
}

func stopOptionID(t *testing.T, q DeferredAuthority) string {
	t.Helper()
	for _, o := range q.Decision.Options {
		if o.Outcome == authority.Stop {
			return o.ID
		}
	}
	t.Fatal("the question offers no stop option")
	return ""
}

func kindsOf(evs []event.Event) []event.Kind {
	out := make([]event.Kind, 0, len(evs))
	for _, ev := range evs {
		out = append(out, ev.Kind)
	}
	return out
}

// WITNESS 13 — a record that names NO task predates the binding field. Absence is
// not disagreement, and reading it as one would make every question deferred
// before that field permanently unanswerable.
//
// The proof is that the legacy record gets PAST the binding guard: it reaches the
// next step (starting Sensei, which this engine cannot do) instead of being
// refused for a mismatch it never claimed.
func TestARecordNamingNoTaskIsNotTreatedAsAMismatch(t *testing.T) {
	// Exactly the bytes a pre-repair session holds: no task_id, no scope.
	legacy := []byte(`{"condition":"` + scopedCondition + `","domain":"github.com/globulario/sensei-code",` +
		`"base_sha":"e7d3fede98ff88b89904b096a40b363adc9c5667","decision":{"level":3,` +
		`"subject":"Architectural authority reached a human-owned boundary.",` +
		`"options":[{"id":"3","label":"Stop this task","outcome":"stop"}]}}`)

	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := &Engine{Bus: bus, SessionID: "s1", Store: heldTaskStore(t, "task-1788804009410633746"), pending: map[string]chan string{}}
	bindFixtureSession(t, e.Store, "task-1788804009410633746", e.SessionID)
	e.resumeAuthority(fixtureCtx(e, "task-1788804009410633746"), session.Interrupted{
		TaskID: "task-1788804009410633746", Task: "an objective", AwaitingAuthority: legacy,
	})

	var sawLegacyGate bool
	for _, ev := range take() {
		if strings.Contains(ev.Summary, "bound to task") {
			t.Fatalf("a record naming no task was refused as a mismatch: %q", ev.Summary)
		}
		if ev.Kind == event.Status && strings.Contains(ev.Summary, "only the stop option is admissible") {
			sawLegacyGate = true
		}
	}
	// And the legacy boundary is ENFORCED, not merely mentioned: the gate is
	// armed for this task before the question is asked.
	if !sawLegacyGate {
		t.Error("the legacy scope boundary was not stated, so a person cannot know their answer will be refused")
	}
	if err := e.refuseUnprovenAuthority("task-1788804009410633746",
		authority.Option{ID: "1", Outcome: authority.Authorize}); err == nil {
		t.Error("the gate was not armed for a record that cannot prove its scope")
	}
	if err := e.refuseUnprovenAuthority("task-1788804009410633746",
		authority.Option{ID: "3", Outcome: authority.Stop}); err != nil {
		t.Errorf("the gate refused a stop, which authorises nothing: %v", err)
	}
}

// ---------------------------------------------------------------------------
// LEGACY SAFETY. A durable record that cannot prove what it was asked about must
// not be able to authorize anything.
//
// Forced by the 2026-09-13 incident: the first repair WARNED and continued, and
// a warning is not a boundary — it depends on the reader, and the reader was an
// agent that had already decided the command was inert. See
// docs/audit/2026-09-13-incident-unauthorized-authority-consumption.md.
// ---------------------------------------------------------------------------

// legacyAsk asks a question whose record cannot prove its scope, answers it with
// one option, and returns everything emitted.
func legacyAsk(t *testing.T, taskID, optionID string) ([]event.Event, error) {
	t.Helper()
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := withFixtureStore(t, &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}, taskID)
	e.noteUnprovenAuthorityScope(taskID)

	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(fixtureCtx(e, taskID), nil, taskID, scopedCondition,
			"github.com/globulario/sensei-code", "e7d3fede98ff88b89904b096a40b363adc9c5667",
			authority.Decision{Level: authority.Human, Subject: "Architectural authority reached a human-owned boundary.", Options: realOptions()},
			realOptions())
		errc <- err
	}()
	waitForPending(t, e, taskID)
	if !e.ResolveHuman(taskID, optionID) {
		t.Fatal("the answer was not delivered")
	}
	err := <-errc
	return take(), err
}

// L1/L2/L3 — an answer that would AUTHORIZE work is refused, and refused before
// anything durable claims it happened.
func TestAnUnprovenScopeRefusesEveryAnswerThatAuthorisesWork(t *testing.T) {
	for _, tc := range []struct{ option, outcome string }{
		{"1", "authorize"},
		{"2", "revise"},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			evs, err := legacyAsk(t, "task-legacy", tc.option)

			var refusal *UnprovenAuthorityScopeError
			if !errors.As(err, &refusal) {
				t.Fatalf("err = %v, want a typed UnprovenAuthorityScopeError", err)
			}
			if refusal.OptionID != tc.option || string(refusal.Outcome) != tc.outcome {
				t.Fatalf("the refusal does not name what was refused: %+v", refusal)
			}
			// The question must be preserved, not consumed.
			if !errors.Is(err, errAuthorityDeferred) {
				t.Fatal("the refusal does not leave the question standing")
			}
			kinds := kindsOf(evs)
			if hasKind(kinds, event.AuthorityResolved) {
				t.Fatal("a refused answer was recorded as a resolution; the question is consumed")
			}
			if !hasKind(kinds, event.WorkflowAwaitingAuthority) {
				t.Fatalf("the question was not preserved: %v", kinds)
			}
			if hasKind(kinds, event.WorkflowFailed) {
				t.Fatal("the refusal emitted WorkflowFailed, which makes the task terminal and the question unreachable")
			}
			// And the person is told why, in the record.
			var said bool
			for _, ev := range evs {
				if strings.Contains(ev.Summary, "cannot prove which files") {
					said = true
				}
			}
			if !said {
				t.Error("the refusal reason was not recorded")
			}
		})
	}
}

// L4 — a Stop is admissible for such a record, because it authorizes no
// subsequent work. It resolves, and it terminates as STOPPED.
func TestAnUnprovenScopeStillAdmitsAHumanStop(t *testing.T) {
	evs, err := legacyAsk(t, "task-legacy", "3")
	if !errors.Is(err, errStoppedByHumanAuthority) {
		t.Fatalf("a stop on a legacy record produced %v", err)
	}
	var r authority.Resolution
	payloadOf(t, evs, event.AuthorityResolved, &r)
	if r.Outcome != authority.Stop {
		t.Fatalf("outcome = %q", r.Outcome)
	}
	if r.State != authority.Unsupported {
		t.Fatalf("a stop proposed a contract to Sensei: %q", r.State)
	}
	if hasKind(kindsOf(evs), event.WorkflowAwaitingAuthority) {
		t.Fatal("a stop left the question standing; it answered it")
	}

	// And the terminal that follows is STOPPED, never FAILED.
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := withFixtureStore(t, &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}, "task-legacy")
	e.terminateAuthorityOutcome(fixtureCtx(e, "task-legacy"), "task-legacy", "the objective", err)
	kinds := kindsOf(take())
	if !hasKind(kinds, event.WorkflowStopped) || hasKind(kinds, event.WorkflowFailed) {
		t.Fatalf("a legacy stop terminated as %v, want workflow.stopped", kinds)
	}
}

// L5 — the guard is not universal. A record that CAN prove its scope authorizes
// exactly as it always did.
func TestAProvenScopeStillAuthorises(t *testing.T) {
	q := deferScoped(t, "task-proven", planScope())
	if !q.ScopeRecorded {
		t.Fatal("the fixture did not record its scope")
	}
	r := authorizeResolution(t, q)
	if r.Outcome != authority.Authorize {
		t.Fatalf("a provable record was refused: %+v", r)
	}
	if !r.Covers(scopedCondition, planScope()) {
		t.Fatal("the authorised answer does not cover its own question")
	}
}

// L6 — after a refusal the question is still there, byte for byte, and still
// resumable. A refusal that quietly spent it would be the incident again.
func TestARefusedLegacyAnswerLeavesTheQuestionResumable(t *testing.T) {
	evs, _ := legacyAsk(t, "task-legacy", "1")
	// Replay the emitted events through the session reader the resume path uses.
	history := []event.Event{
		event.New("s1", "task-legacy", event.SourceSystem, event.TaskCreated, "an objective", nil),
	}
	history = append(history, evs...)
	standing := session.FindInterrupted(history)
	if len(standing) != 1 {
		t.Fatalf("the task is no longer resumable after a refusal: %d interrupted", len(standing))
	}
	if len(standing[0].AwaitingAuthority) == 0 {
		t.Fatal("the standing question was cleared by a refusal")
	}
	var q DeferredAuthority
	if err := json.Unmarshal(standing[0].AwaitingAuthority, &q); err != nil {
		t.Fatal(err)
	}
	if q.Condition != scopedCondition || len(q.Decision.Options) != 3 {
		t.Fatalf("the preserved question changed: %+v", q)
	}
}

// L7 — no override. The gate must not be re-openable by a flag, an env var, or a
// boolean that restores the unsafe path.
func TestNoOverrideRestoresUnprovenAuthorisation(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "refuseUnprovenAuthority")
	for _, forbidden := range []string{"Getenv", "LookupEnv", "Force", "force", "Override", "override", "AllowUnproven"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("the gate consults %s; an unprovable record must not become authoritative by configuration", forbidden)
		}
	}
	// Stop is the ONLY admitted outcome, named positively rather than by
	// excluding a list that a new outcome could slip past.
	if !strings.Contains(body, "authority.Stop") {
		t.Error("the gate does not name the one outcome it admits, so a new outcome would default to permitted")
	}
}

// WITNESS 14 — a task that answered its question and died before it had a plan
// re-enters governed execution as ITSELF.
//
// The record task-1789848074761930104 left on 2026-09-19 carried no standing
// question (it was answered), no plan (none was proposed) and no block. The
// earlier continuation for an unplanned task REQUIRED a block record, so this
// shape had no path at all while its candidate identity sat on disk.
//
// Execution is entered here and stops at the first capability gate, which is
// exactly the evidence wanted: what this proves is WHICH task re-entered, not
// how far a run with no repository can get.
func TestAnUnplannedTaskReEntersExecutionUnderItsOwnIdentity(t *testing.T) {
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	task := session.Interrupted{TaskID: "task-1789848074761930104", Task: "repair the resume path"}
	// The durable record the task was reconstructed from: the only place a
	// restarted process can read its objective.
	store := sessionStore(t)
	if err := seedAppend(t, store, event.New("s1", task.TaskID, event.SourceUser, event.TaskCreated, task.Task, nil)); err != nil {
		t.Fatal(err)
	}
	// A restarted process acts under a fresh session, which the production
	// lineage binding makes the task's current session before it records
	// anything (70B2a1, RULING-189): the same TASK re-enters, not the same
	// session.
	e := &Engine{Bus: bus, SessionID: "s2", Store: store, pending: map[string]chan string{}}
	bindFixtureSession(t, store, task.TaskID, e.SessionID)

	e.resumeUnplannedArchitecture(fixtureCtx(e, task.TaskID), task)

	evs := take()
	if len(evs) == 0 {
		t.Fatal("an unplanned task produced no account of being resumed at all")
	}
	owed := ""
	for _, ev := range evs {
		if ev.TaskID != task.TaskID {
			t.Fatalf("a resumed task emitted an event under another identity %q: %s", ev.TaskID, ev.Kind)
		}
		if ev.Kind == event.Status && strings.Contains(ev.Summary, "the turn it is owed") {
			owed = ev.Summary
		}
	}
	if owed == "" {
		t.Fatalf("the record does not say what the task was resumed at: %v", kindsOf(evs))
	}
	if !strings.Contains(owed, "architect turn") && !strings.Contains(owed, "no recorded plan") {
		t.Fatalf("the owed turn is not named as the architect's: %q", owed)
	}
	// The objective is the recorded one, recovered from TaskCreated under the
	// resumption's own provenance: a restarted process establishes no human.
	if got, err := e.recordedObjective(task.TaskID); err != nil || got.Text != task.Task || got.Provenance != ResumedGoverned {
		t.Fatalf("the recorded objective was not carried: %+v %v", got, err)
	}
	// And the run it entered is a continuation, never a fresh submission.
	body := funcBody(t, "internal/workflow/engine.go", "resumeUnplannedArchitecture")
	for _, forbidden := range []string{"SubmitGoverned", "SubmitObservation", "SubmitGovernedWithPlan", "SubmitGovernedUnattended"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("resuming reaches %s; a fresh task is not a continuation", forbidden)
		}
	}
	// funcBody yields a flattened token stream, so these are token paths.
	for _, want := range []string{"e.execute(", "task.TaskID(", "task.Task("} {
		if !strings.Contains(body, want) {
			t.Errorf("the continuation does not carry the recorded task id and objective: %s absent", want)
		}
	}
	// And RESUME itself routes on the absence of a plan, not on the presence of
	// a block. Asserted by driving Resume rather than by reading it: the earlier
	// version called this very function, and only its GUARD -- a block record it
	// also required -- left this shape unreachable.
	//
	// It is driven as a restart drives it (70B2a1): a FRESH process over the
	// holder's record, under a fresh SessionID. The same TASK resumes; the same
	// session does not. The fresh session becomes the task's current session
	// only through the production lineage binding Resume performs, and the
	// root stays owned by the holder s1.
	fresh, err := session.FreshID(time.Now())
	if err != nil {
		t.Fatal(err)
	}
	resumed := event.NewBus()
	ch, stop := resumed.Subscribe(64)
	defer stop()
	// The earlier invocation of the task ends, and gives back its lease,
	// before the fresh process resumes it.
	endFixtureInvocation(e, task.TaskID)
	// Its run stopped at the capability gate and recorded the task's ending,
	// so that record no longer holds an interrupted task: Resume reads what
	// the task owes from the record's canonical member history (70B2a2), not
	// from the caller's projection. The restart is driven over a holder record
	// in the shape the crashed task left -- created, and nothing after.
	store = sessionStore(t)
	if err := seedAppend(t, store, event.New("s1", task.TaskID, event.SourceUser, event.TaskCreated, task.Task, nil)); err != nil {
		t.Fatal(err)
	}
	r := &Engine{Bus: resumed, SessionID: fresh, Store: store, pending: map[string]chan string{}}
	if got := r.Resume(context.Background(), task); got != task.TaskID {
		t.Fatalf("Resume continued %q instead of the task it was given", got)
	}
	deadline := time.After(20 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.TaskID != task.TaskID || ev.SessionID != fresh {
				t.Fatalf("the resumed process published %s as task %q session %q; want task %q session %q",
					ev.Kind, ev.TaskID, ev.SessionID, task.TaskID, fresh)
			}
			if ev.Kind == event.Status && strings.Contains(ev.Summary, "the turn it is owed") {
				lineage, err := store.TaskSessionLineage(task.TaskID)
				if err != nil || lineage.HolderSessionID != "s1" || lineage.Tip() != fresh {
					t.Fatalf("the resumed session was not bound through the task's lineage: %+v %v", lineage, err)
				}
				return
			}
			if ev.Kind == event.WorkflowFailed || ev.Kind == event.WorkflowCompleted {
				t.Fatalf("Resume settled without routing an unplanned task to its architect turn: %s", ev.Summary)
			}
		case <-deadline:
			t.Fatal("Resume never reached the architect turn an unplanned task is owed")
		}
	}
}

// WITNESS 15 — the answer a resumed task carries authorizes exactly the question
// it answered, about exactly the files it was asked about.
//
// This is what makes re-entering execution safe: the run re-derives its routing
// from current evidence, and the recorded answer settles the one condition it
// was given for. A resumed task must not be asked its settled question again --
// and must not treat that answer as a yes to anything else.
func TestAResumedAnswerAuthorisesOnlyTheQuestionItAnswered(t *testing.T) {
	store, err := session.New(t.TempDir(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	rootFixtureTask(t, store, "s1", "task-1", "task")
	recorded := authority.Resolution{
		TaskID: "task-1", SessionID: "s1", Question: "Architectural authority reached a human-owned boundary.",
		Condition: scopedCondition, OptionID: "1", OptionLabel: "Authorize the architectural change described above",
		Scope: planScope(), Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC(),
	}
	if err := seedAppend(t, store, event.New("s1", "task-1", event.SourceUser, event.AuthorityResolved,
		recorded.OptionLabel, recorded)); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Bus: event.NewBus(), SessionID: "s1", Store: store}

	// The same question about the same files is not asked again.
	if authorized, asked := e.applyAnsweredCondition("task-1", scopedCondition, planScope()...); !authorized || !asked {
		t.Fatalf("the settled question would be asked again: authorized=%v asked=%v", authorized, asked)
	}
	// One file more than the human saw is a WIDER question, and unanswered.
	wider := append(append([]string{}, planScope()...), "internal/workflow/engine.go")
	if authorized, _ := e.applyAnsweredCondition("task-1", scopedCondition, wider...); authorized {
		t.Fatal("the recorded answer authorized a file scope nobody was shown")
	}
	// A different condition over the same files is a different question.
	if _, asked := e.applyAnsweredCondition("task-1", "human_approval_required (blast radius security)", planScope()...); asked {
		t.Fatal("the recorded answer was read as an answer to another question")
	}
	// And it is bound to the task it was given for.
	if _, asked := e.applyAnsweredCondition("task-2", scopedCondition, planScope()...); asked {
		t.Fatal("another task inherited this task's answer")
	}
}

// architectCapture is the resolver the witness below configures. It records the
// FIRST architect RunnerSpec the engine hands it -- the binding exactly as it
// crosses into adapter selection -- and refuses, so the run stops there.
type architectCapture struct{ specs chan RunnerSpec }

func (c architectCapture) Resolve(spec RunnerSpec) (Resolved, error) {
	select {
	case c.specs <- spec:
	default:
	}
	return Resolved{}, errors.New("the witness captured the " + spec.Role.Label() + " request and serves no adapter")
}

// resumeSenseiScript is a Sensei MCP that certifies the start, run with sh so
// the witness needs no import beyond what this file already has.
//
// It answers in the Content-Length framing internal/sensei speaks: a certified
// workspace for this repository's domain, an OK preflight, and an empty answer
// for every other tool. It also names the fixture repository's origin, because
// the start gate compares the graph's domain against the remote on disk and a
// repository with none is -- correctly -- refused.
const resumeSenseiScript = `
LC_ALL=C; export LC_ALL
git remote add origin https://github.com/globulario/sensei-code.git >/dev/null 2>&1
reply() { printf 'Content-Length: %d\r\n\r\n%s' "${#1}" "$1"; }
while :; do
	len=
	while IFS= read -r line; do
		line=$(printf %s "$line" | tr -d '\r')
		[ -z "$line" ] && break
		case "$line" in Content-Length:*) len=$(printf %s "${line#Content-Length:}" | tr -dc 0-9) ;; esac
	done
	[ -n "$len" ] || exit 0
	body=$(dd bs=1 count="$len" 2>/dev/null)
	case "$body" in '{"jsonrpc":"2.0","id":'*) ;; *) continue ;; esac
	rest=${body#'{"jsonrpc":"2.0","id":'}
	id=${rest%%,*}
	rest=${rest#*,}
	case "$rest" in
	'"method":"initialize"'*)
		result='{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"resume-stub","version":"0"}}' ;;
	*'"name":"sensei_workspace_status"}}')
		result='{"content":[{"type":"text","text":"composition_state: complete"}],"structuredContent":{"composition_state":"complete","binding":{"repository_domain":"github.com/globulario/sensei-code"}}}' ;;
	*'"name":"awareness_preflight"}}')
		result='{"content":[{"type":"text","text":"preflight ok"}],"structuredContent":{"status":"PREFLIGHT_STATUS_OK","risk_class":"ARCHITECTURE_SENSITIVE","required_actions":["run the tests"],"authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT"}}}' ;;
	*)
		result='{"content":[{"type":"text","text":"the stub graph holds nothing about this"}],"structuredContent":{}}' ;;
	esac
	reply "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":$result}"
done
`

// WITNESS 16 — an authority-deferred task reaches its FIRST resumed architect
// request with its objective identity intact.
//
// Measured on task-1790481145146367848 (2026-09-27): the preserved question was
// answered, the answer applied, and the continuation died one second later with
// "no exact objective/world binding for architect turn: {... ObjectiveDigest: ...}"
// -- every referent but the objective survived.
//
// The assertion is made on the RunnerSpec the engine hands the configured
// resolver, which is where that run emitted the empty digest; nothing here
// asks architectureBinding for its opinion. The expected digest is pinned as the
// SHA-256 of the recorded bytes, derived outside the code under test.
//
// The engine is FRESH over the durable session record, exactly as `sensei-code
// resume` builds it: no objective is held in memory before Resume, so the
// identity the architect request carries can only have been recovered from the
// recorded TaskCreated through recordedObjective.
func TestAnAnsweredAuthorityQuestionResumesTheArchitectWithTheRecordedObjective(t *testing.T) {
	const (
		taskID    = "task-1790481145146367848"
		objective = "restore the recorded objective across an answered authority question"
		// sha256 of objective, computed independently of roles.BindArchitecture.
		wantDigest = "5549bf4fa65d8ddc670960dc4b831d39101a1955e437e072cb6986b962b15b9d"
	)

	// The smallest history: objective recorded -> authority deferred.
	q := deferScoped(t, taskID, planScope())
	history := []event.Event{
		event.New("s1", taskID, event.SourceUser, event.TaskCreated, objective, nil),
		event.New("s1", taskID, event.SourceUser, event.WorkflowAwaitingAuthority, q.Condition, q),
	}
	standing := session.FindInterrupted(history)
	if len(standing) != 1 || standing[0].TaskID != taskID {
		t.Fatalf("the deferred task was not reconstructed as itself: %+v", standing)
	}
	task := standing[0]
	if task.Task != objective {
		t.Fatalf("reconstruction changed the recorded objective bytes: %q", task.Task)
	}
	if len(task.AwaitingAuthority) == 0 {
		t.Fatal("the reconstructed task no longer carries its standing question")
	}

	store := sessionStore(t)
	for _, ev := range history {
		if err := seedAppend(t, store, ev); err != nil {
			t.Fatal(err)
		}
	}

	repo, _ := mintRepo(t)
	bus := event.NewBus()
	events, cancel := bus.Subscribe(1024)
	defer cancel()
	capture := architectCapture{specs: make(chan RunnerSpec, 1)}
	e := &Engine{Repo: repo, Bus: bus, SessionID: "s1", Store: store, pending: map[string]chan string{}, Runners: capture}
	if len(e.objectives) != 0 {
		t.Fatalf("the fresh engine already holds an objective, so recovery would not be proven: %+v", e.objectives)
	}
	e.Config.Permissions.ReadRepository = true
	e.Config.Sensei.Command = "sh"
	e.Config.Sensei.Args = []string{"-c", resumeSenseiScript}
	e.Config.Sensei.Repository = "globulario/sensei"
	// Named so the architect turn is attempted; the capture serves it.
	e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "chatgpt", "true", "none"

	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	if got := e.Resume(ctx, task); got != taskID {
		t.Fatalf("Resume continued %q instead of the task it was given", got)
	}

	// Answer the restored question the way a person does.
	deadline := time.Now().Add(20 * time.Second)
	for !e.ResolveHuman(taskID, "1") {
		if time.Now().After(deadline) {
			t.Fatal("the resumed task never re-asked its preserved question")
		}
		time.Sleep(10 * time.Millisecond)
	}

	var seen []string
	timeout := time.After(60 * time.Second)
	for {
		select {
		case spec := <-capture.specs:
			got := spec.Architecture
			if spec.TaskID != taskID || got.TaskID != taskID {
				t.Fatalf("the resumed architect request names another task: spec=%q binding=%q", spec.TaskID, got.TaskID)
			}
			if got.ObjectiveDigest == "" {
				t.Fatalf("the first resumed architect request carries no objective identity: %+v", got)
			}
			if got.ObjectiveDigest != wantDigest || event.ObjectiveDigest(objective) != wantDigest {
				t.Fatalf("the resumed architect request carries an objective identity not derived from the recorded bytes: %s", got.ObjectiveDigest)
			}
			// Restored under the resumption's own provenance: nothing here
			// establishes that a person asked.
			if o := e.objective(taskID); o.Text != objective || o.Provenance != ResumedGoverned || o.HumanAuthorized() {
				t.Fatalf("the restored objective claims provenance it does not have: %+v", o)
			}
			return
		case ev := <-events:
			seen = append(seen, string(ev.Kind)+": "+ev.Summary)
			if ev.Kind == event.WorkflowFailed || ev.Kind == event.WorkflowCompleted {
				t.Fatalf("the resumed run ended before any architect request reached the resolver:\n%s", strings.Join(seen, "\n"))
			}
		case <-timeout:
			t.Fatalf("no architect request reached the resolver:\n%s", strings.Join(seen, "\n"))
		}
	}
}

// ---------------------------------------------------------------------------
// P9 -- ONE AUTHORITY RESOLUTION PER GAP IDENTITY (architect rulings 08/09).
//
// Measured on task-1790513596091085059 (2026-09-27): the proceed route declared
// a gap open and, two seconds later, the escalate route certified the same
// region because it routed a differently shaped plan. Two deciders answered
// "is this gap settled for this task?" and disagreed; the round ceiling was
// the only thing that could end the loop.
//
// The loop witnesses below drive the real resolution loop through execute,
// with a scripted architect and a Sensei stub that certifies the region. Only
// events and prompts that exist at the base are read, so the behavioural core
// runs there too.
// ---------------------------------------------------------------------------

// gapLoopSenseiScript certifies the region fully: authoritative and current,
// coverage proven over the one planned file, an explicit no-approval gate and
// no blind spots. Any gap the router reports is therefore the plan's own -- an
// inference claim -- and a plan without one routes to certification.
//
// It also states, before the first run establishes its candidate, the rule
// every real repository carries that .sensei-code/ is not source (this
// repository's .gitignore; confinementRepo states it the same way). Without it
// the first run's own identity record dirties the fixture, and a resume refuses
// -- correctly -- on the repository-wide cleanliness check.
const gapLoopSenseiScript = `
LC_ALL=C; export LC_ALL
git remote add origin https://github.com/globulario/sensei-code.git >/dev/null 2>&1
exclude=$(git rev-parse --git-path info/exclude)
grep -qx '/.sensei-code/' "$exclude" 2>/dev/null || printf '/.sensei-code/\n' >> "$exclude"
reply() { printf 'Content-Length: %d\r\n\r\n%s' "${#1}" "$1"; }
while :; do
	len=
	while IFS= read -r line; do
		line=$(printf %s "$line" | tr -d '\r')
		[ -z "$line" ] && break
		case "$line" in Content-Length:*) len=$(printf %s "${line#Content-Length:}" | tr -dc 0-9) ;; esac
	done
	[ -n "$len" ] || exit 0
	body=$(dd bs=1 count="$len" 2>/dev/null)
	case "$body" in '{"jsonrpc":"2.0","id":'*) ;; *) continue ;; esac
	rest=${body#'{"jsonrpc":"2.0","id":'}
	id=${rest%%,*}
	rest=${rest#*,}
	case "$rest" in
	'"method":"initialize"'*)
		result='{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"gap-loop-stub","version":"0"}}' ;;
	*'"name":"sensei_workspace_status"}}')
		result='{"content":[{"type":"text","text":"composition_state: complete"}],"structuredContent":{"composition_state":"complete","binding":{"repository_domain":"github.com/globulario/sensei-code"}}}' ;;
	*'"name":"awareness_preflight"}}')
		result='{"content":[{"type":"text","text":"preflight ok"}],"structuredContent":{"status":"PREFLIGHT_STATUS_OK","risk_class":"LOW_RISK","authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT","graph_build_commit":"05feaf64d2694e97ac42b6bb93fbb49b9851a1f1","source_repo_commit":"39a8d2809ef239f203d5365d7f6e170349186cc4"},"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},"coverage":{"direct_anchor_count":2,"file_count":1,"indexed_file_count":1,"sufficient":true}}}' ;;
	*)
		result='{"content":[{"type":"text","text":"the stub graph holds nothing about this"}],"structuredContent":{}}' ;;
	esac
	reply "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":$result}"
done
`

// The architect's two answers in the measured run: a modifying plan resting on
// an unverified premise (a bounded gap), and an inspect escalation over the
// same file carrying no premise at all (a plan Sensei certifies).
const (
	gapProceed = `{"decision":"proceed","summary":"edit main","plan":"edit main.go","files":["main.go"],"mode":"modify",` +
		`"claims":[{"statement":"main has no callers","about":"main.go","source":"inference"}]}`
	gapEscalate = `{"decision":"escalate","summary":"unsure about main","plan":"inspect main.go","files":["main.go"],"mode":"inspect",` +
		`"human_question":"May main.go change?"}`
	gapReply = `{"decision":"reply","message":"settled on the certification"}`
	// certifiedMarker is certifiedResolutionPrompt's own heading.
	certifiedMarker = "AUTHORITY ROUTING RESULT"
)

type gapLoopRun struct {
	engine  *Engine
	events  []event.Event
	prompts []string
	world   string
}

// newGapLoopEngine builds an engine whose architect answers from turns (the
// last one repeated) and whose Sensei certifies the region. A prior engine's
// repository is reused, so a resumed run continues the same candidate.
func newGapLoopEngine(t *testing.T, prior *gapLoopRun, store *session.Store, turns ...string) (*Engine, *scriptedArchitect, string) {
	t.Helper()
	e := &Engine{Bus: event.NewBus(), Store: store, SessionID: "s1", pending: map[string]chan string{}}
	world := ""
	if prior != nil {
		e.Repo, world = prior.engine.Repo, prior.world
	} else {
		repo, base := mintRepo(t)
		e.Repo, world = repo, base
	}
	script := make([]architectTurn, 0, 32)
	for _, turn := range turns {
		script = append(script, architectTurn{text: turn})
	}
	for len(script) < 32 {
		script = append(script, architectTurn{text: turns[len(turns)-1]})
	}
	architect := &scriptedArchitect{turns: script}
	e.Runners = &fixedResolver{runner: architect, name: "claude"}
	e.Config.Permissions.ReadRepository = true
	e.Config.Sensei.Command = "sh"
	e.Config.Sensei.Args = []string{"-c", gapLoopSenseiScript}
	e.Config.Sensei.Repository = "globulario/sensei"
	e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "claude", "true", "none"
	return e, architect, world
}

// driveGapLoop starts a run and follows it to its terminal. The first human
// question is answered with answer ("" defers it); any later one is deferred,
// so the run always ends.
func driveGapLoop(t *testing.T, e *Engine, architect *scriptedArchitect, world, taskID, answer string, start func(context.Context)) gapLoopRun {
	t.Helper()
	ch, cancel := e.Bus.Subscribe(8192)
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	// A run the session record halted (70B2a1) has no terminal that can be
	// recorded; its outcome is its invocation's failure, so the halt ends
	// the drive as a terminal would.
	halted := taskHalted(ctx, e, taskID)
	go start(ctx)
	run := gapLoopRun{engine: e, world: world}
	asked := 0
	timeout := time.After(90 * time.Second)
	for {
		select {
		case <-halted:
			halted = nil
			settle := time.After(300 * time.Millisecond)
			for {
				select {
				case ev := <-ch:
					run.events = append(run.events, ev)
				case <-settle:
					run.prompts = append(run.prompts, architect.prompts...)
					return run
				}
			}
		case ev := <-ch:
			run.events = append(run.events, ev)
			switch ev.Kind {
			case event.AuthorityRequired:
				asked++
				waitForPending(t, e, taskID)
				if asked == 1 && answer != "" {
					e.ResolveHuman(taskID, answer)
				} else {
					e.DeferAuthority(taskID)
				}
			case event.WorkflowAwaitingAuthority, event.WorkflowFailed, event.WorkflowCompleted, event.WorkflowStopped:
				// Whatever the run still emits on its way out is kept.
				settle := time.After(300 * time.Millisecond)
				for {
					select {
					case ev := <-ch:
						run.events = append(run.events, ev)
					case <-settle:
						run.prompts = append(run.prompts, architect.prompts...)
						return run
					}
				}
			}
		case <-timeout:
			stop()
			t.Fatalf("the governed run did not end:\n%s", gapLoopTrace(run.events))
		}
	}
}

// runGapLoop drives one fresh governed run through the real resolution loop.
// A record the fixture already rooted the task in, with history after the
// root, is continued under that root (runRootedTask).
func runGapLoop(t *testing.T, store *session.Store, taskID string, turns ...string) gapLoopRun {
	t.Helper()
	e, architect, world := newGapLoopEngine(t, nil, store, turns...)
	_, rooted := store.TaskSessionLineage(taskID)
	return driveGapLoop(t, e, architect, world, taskID, "", func(ctx context.Context) {
		if rooted == nil {
			runRootedTask(ctx, e, taskID, "change main.go", RequestedByHuman)
			return
		}
		e.run(ctx, taskID, "change main.go", RequestedByHuman)
	})
}

// runRootedTask is e.run for a task whose TaskCreated root the fixture
// already wrote, so that history the run must see -- an earlier answer --
// stands after the root, as it does in production (70B2a1). e.run writes the
// root itself and a second root is refused, so this is e.run after its root,
// under an invocation admitted as e.run's is: a record names its invocation
// or is refused (RULING-198) -- and under the task's lease, which the Store
// makes operative for the root's holder (70B2a1 cycle-3 review f1).
func runRootedTask(ctx context.Context, e *Engine, taskID, task string, how Provenance) {
	inv, err := e.admitInvocation(ctx, taskID)
	if err != nil {
		return
	}
	// Leased as e.run's invocation is, and operative for the root's holder.
	leaseFixtureInvocation(e, inv)
	defer e.endInvocation(inv)
	ctx = inv.ctx
	e.recordObjective(taskID, Objective{Text: task, Provenance: how})
	e.announceMode(ctx, taskID, governedMode(how))
	e.execute(ctx, taskID, task)
}

func gapLoopTrace(evs []event.Event) string {
	var b strings.Builder
	for _, ev := range evs {
		b.WriteString(string(ev.Kind) + ": " + ev.Summary + "\n")
	}
	return b.String()
}

func certifiedPrompts(prompts []string) int {
	n := 0
	for _, p := range prompts {
		if strings.Contains(p, certifiedMarker) {
			n++
		}
	}
	return n
}

// storeWithTaskCreated is a durable session record holding one task's
// TaskCreated, with exactly the given bytes as its objective.
func storeWithTaskCreated(t *testing.T, taskID, objective string) *session.Store {
	t.Helper()
	store := sessionStore(t)
	if err := seedAppend(t, store, event.New("s1", taskID, event.SourceUser, event.TaskCreated, objective, nil)); err != nil {
		t.Fatal(err)
	}
	return store
}

// rootFixtureTask writes the canonical durable root production Submit writes
// first -- the task's TaskCreated, under the session that holds it -- so the
// task has a session lineage its later records are authorized against
// (70B2a1, RULING-190: rootless fixtures are migrated, never exempted).
func rootFixtureTask(t *testing.T, store *session.Store, sessionID, taskID, objective string) {
	t.Helper()
	if err := seedAppend(t, store, event.New(sessionID, taskID, event.SourceSystem, event.TaskCreated, objective, nil)); err != nil {
		t.Fatalf("the fixture could not root task %s: %v", taskID, err)
	}
}

// withFixtureStore gives e a test-owned durable Store under e's session,
// with each of taskIDs rooted in it, and returns e. FIXTURE MIGRATION
// (70B2a1, RULING-193 A and W4): a task-bound event is published only once
// it is recorded, and an engine with no Store records none and publishes
// none, so a fixture that observes a task's events stands on a durable record
// that roots the task, exactly as a submitted task's does.
func withFixtureStore(t *testing.T, e *Engine, taskIDs ...string) *Engine {
	t.Helper()
	store, err := session.New(t.TempDir(), e.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	e.Store = store
	for _, taskID := range taskIDs {
		rootFixtureTask(t, store, e.SessionID, taskID, "the fixture's objective")
	}
	return e
}

// rootFixtureTaskOnce roots taskID under the engine's session when e's
// record holds no root of it yet: a plan attempt, grant or answer exists in
// production only after the task's TaskCreated root, so a fixture that
// records one stands on that root first. A record that already roots the
// task is left exactly as it is.
func rootFixtureTaskOnce(t *testing.T, e *Engine, taskID, objective string) {
	t.Helper()
	if e.Store == nil {
		return
	}
	if _, err := e.Store.TaskSessionLineage(taskID); !errors.Is(err, session.ErrNoTaskRoot) {
		return
	}
	// A live invocation of the task in e holds the task's lease: it creates
	// the root under that lease, exactly as a Run does, and so becomes its
	// operative writer.
	e.mu.Lock()
	var lease *session.TaskLease
	if live := e.admitted[taskID]; live != nil && !live.ended {
		lease = live.lease
	}
	e.mu.Unlock()
	if lease != nil {
		if err := e.Store.CreateTaskRoot(t.Context(), lease, event.New(e.SessionID, taskID, event.SourceSystem, event.TaskCreated, objective, nil)); err != nil {
			t.Fatalf("the fixture could not root task %s: %v", taskID, err)
		}
		return
	}
	rootFixtureTask(t, e.Store, e.SessionID, taskID, objective)
}

// bindFixtureSession makes fresh the current session of taskID through the
// production session-lineage binding, exactly as a resumed process is bound
// before it records anything of the task.
func bindFixtureSession(t *testing.T, store *session.Store, taskID, fresh string) {
	t.Helper()
	lineage, err := store.TaskSessionLineage(taskID)
	if err != nil {
		t.Fatalf("task %s has no session lineage to bind %s to: %v", taskID, fresh, err)
	}
	b, err := lineage.BindingFor(fresh)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.AcquireTaskInvocation(context.Background(), taskID)
	if err != nil {
		t.Fatalf("leasing task %s to bind %s: %v", taskID, fresh, err)
	}
	defer lease.Release()
	if _, err := store.BindSessionLineage(context.Background(), lease, b); err != nil {
		t.Fatalf("binding %s to task %s: %v", fresh, taskID, err)
	}
}

// heldTaskStore is the durable record of taskID as the process that began it
// left it: rooted under its holder session, which is never the fresh session
// a resuming engine runs under.
func heldTaskStore(t *testing.T, taskID string) *session.Store {
	t.Helper()
	store := sessionStore(t)
	rootFixtureTask(t, store, "s0-holder", taskID, "an objective")
	return store
}

func sessionStore(t *testing.T) *session.Store {
	t.Helper()
	store, err := session.New(t.TempDir(), "s1")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

// W1 -- THE MEASURED LOOP. Proceed opens a bounded gap; the architect then
// escalates an inspect plan over the same file that Sensei would certify. The
// escalation must not be answered with the certification while the gap is
// open, and the run must end deferred on the gap -- not at the round ceiling.
func TestAnEscalationIsNotCertifiedWhileTheProceedGapIsOpen(t *testing.T) {
	run := runGapLoop(t, sessionStore(t), "task-w1", gapProceed, gapEscalate)
	kinds := kindsOf(run.events)

	// ABSENCE: no certified round while the gap is open.
	if n := certifiedPrompts(run.prompts); n != 0 {
		t.Fatalf("the escalation was answered with certifiedResolutionPrompt %d time(s) while the proceed gap was open:\n%s",
			n, gapLoopTrace(run.events))
	}
	for _, ev := range run.events {
		if strings.Contains(ev.Summary, "Sensei certifies this region") {
			t.Fatalf("the escalation was certified while the gap was open: %q", ev.Summary)
		}
		if strings.Contains(ev.Summary, "resolution rounds") {
			t.Fatalf("the run ended at the round ceiling, not at the gap: %q", ev.Summary)
		}
	}
	// The honest outcome: the human is asked about the gap and the run is
	// preserved awaiting that answer.
	if !hasKind(kinds, event.AuthorityRequired) || !hasKind(kinds, event.WorkflowAwaitingAuthority) {
		t.Fatalf("the run did not end deferred on the gap:\n%s", gapLoopTrace(run.events))
	}
	if hasKind(kinds, event.WorkflowFailed) {
		t.Fatalf("the run failed instead of deferring on the gap:\n%s", gapLoopTrace(run.events))
	}
	var q DeferredAuthority
	payloadOf(t, run.events, event.WorkflowAwaitingAuthority, &q)
	if !strings.Contains(q.Condition, "unverified premise") {
		t.Fatalf("the deferred question is not the gap the proceed route opened: %q", q.Condition)
	}
	// Two architect turns: the proceed and the escalation. No certified rounds.
	if len(run.prompts) != 2 {
		t.Fatalf("the architect was asked %d times, want 2 (proceed, escalate)", len(run.prompts))
	}
}

// W2 -- CONTROL. With no gap open for the task, an escalation into a region
// Sensei certifies is still handed back as certified. A nervous model must not
// be able to manufacture a human interruption.
func TestAnEscalationWithNoOpenGapStillReachesTheCertifiedPath(t *testing.T) {
	run := runGapLoop(t, sessionStore(t), "task-w2", gapEscalate, gapReply)
	if n := certifiedPrompts(run.prompts); n != 1 {
		t.Fatalf("certifiedResolutionPrompt was sent %d time(s), want 1:\n%s", n, gapLoopTrace(run.events))
	}
	kinds := kindsOf(run.events)
	if hasKind(kinds, event.AuthorityRequired) {
		t.Fatalf("an escalation with no open gap reached a human:\n%s", gapLoopTrace(run.events))
	}
	if !hasKind(kinds, event.WorkflowCompleted) {
		t.Fatalf("the certified path did not complete the run:\n%s", gapLoopTrace(run.events))
	}
}

// W1, CANDIDATE SIDE. The same run, read through the state P9 keeps: the
// deferred question names the gap the proceed route identified, bound to the
// pinned world, and that identity is still open -- deferral settles nothing.
func TestTheDeferredGapQuestionCarriesTheIdentityTheProceedRouteOpened(t *testing.T) {
	run := runGapLoop(t, sessionStore(t), "task-w1", gapProceed, gapEscalate)
	var q DeferredAuthority
	payloadOf(t, run.events, event.WorkflowAwaitingAuthority, &q)
	if q.Gap == nil {
		t.Fatal("the deferred question does not carry the gap identity it is about")
	}
	want := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: run.world}
	if q.Gap.Key() != want.Key() {
		t.Fatalf("deferred gap = %+v, want %+v", *q.Gap, want)
	}
	open, ok := run.engine.openGap("task-w1", run.world)
	if !ok || open.Gap.Key() != want.Key() {
		t.Fatalf("the gap is not open after a deferral: %+v %v", open, ok)
	}
	if _, settled := run.engine.gapSettlement("task-w1", Routing{Gap: want}); settled {
		t.Fatal("a deferral settled the gap")
	}
}

// W3 -- RESUME. A task deferred on an open gap resumes to the SAME open gap;
// answered, the answer settles exactly that identity, and the continuation
// consumes the settlement instead of asking again. Driven through Resume.
func TestResumeReconstructsTheSameOpenOrSettledGap(t *testing.T) {
	const taskID = "task-w3"
	store := sessionStore(t)
	first := runGapLoop(t, store, taskID, gapProceed, gapEscalate)
	var q DeferredAuthority
	payloadOf(t, first.events, event.WorkflowAwaitingAuthority, &q)
	if q.Gap == nil {
		t.Fatal("the first run did not defer on an identified gap")
	}
	gap := *q.Gap
	standing := func() session.Interrupted {
		t.Helper()
		history, err := store.Load()
		if err != nil {
			t.Fatal(err)
		}
		tasks := session.FindInterrupted(history)
		if len(tasks) != 1 || tasks[0].TaskID != taskID || len(tasks[0].AwaitingAuthority) == 0 {
			t.Fatalf("the deferred task is not resumable: %+v", tasks)
		}
		return tasks[0]
	}

	// OPEN: resumed and deferred again, the gap is the same identity, and a
	// process that knows only the record reconstructs it as open.
	open, openArchitect, _ := newGapLoopEngine(t, &first, store, gapProceed)
	task := standing()
	again := driveGapLoop(t, open, openArchitect, first.world, taskID, "", func(ctx context.Context) { open.Resume(ctx, task) })
	var q2 DeferredAuthority
	payloadOf(t, again.events, event.WorkflowAwaitingAuthority, &q2)
	if q2.Gap == nil || q2.Gap.Key() != gap.Key() {
		t.Fatalf("the resumed question is about another gap: %+v, want %+v", q2.Gap, gap)
	}
	if len(again.prompts) != 0 {
		t.Fatalf("a deferred resume consulted the architect %d time(s)", len(again.prompts))
	}
	fresh := &Engine{Store: store}
	if got, ok := fresh.openGap(taskID, first.world); !ok || got.Gap.Key() != gap.Key() {
		t.Fatalf("a restarted process does not reconstruct the open gap: %+v %v", got, ok)
	}

	// SETTLED: resumed and authorized, the answer settles exactly that identity
	// and the continuation proceeds on it without asking again.
	answered, architect, _ := newGapLoopEngine(t, &first, store, gapProceed)
	task = standing()
	resumed := driveGapLoop(t, answered, architect, first.world, taskID, "1", func(ctx context.Context) { answered.Resume(ctx, task) })
	var res resolvedAuthority
	payloadOf(t, resumed.events, event.AuthorityResolved, &res)
	if res.Gap == nil || res.Gap.Key() != gap.Key() {
		t.Fatalf("the answer does not settle the identity it was asked about: %+v", res.Gap)
	}
	asked := 0
	for _, ev := range resumed.events {
		if ev.Kind == event.AuthorityRequired {
			asked++
		}
		if ev.Kind == event.Status && strings.Contains(ev.Summary, "escalating with it open") {
			t.Fatalf("the settled gap was re-escalated after resume: %q", ev.Summary)
		}
		// Consumed BEFORE the closure budget: a settled gap is not sent back
		// to be investigated as though nobody had answered it.
		if ev.Kind == event.Status && strings.Contains(ev.Summary, "closing it before governance runs again") {
			t.Fatalf("a closure round was spent on a gap an explicit answer settled: %q", ev.Summary)
		}
	}
	if asked != 1 {
		t.Fatalf("the human was asked %d time(s); only the restored question may be asked:\n%s", asked, gapLoopTrace(resumed.events))
	}
	if len(resumed.prompts) != 1 {
		t.Fatalf("the continuation took %d architect turn(s), want 1: the proceed that consumes the settlement", len(resumed.prompts))
	}
	if !hasKind(kindsOf(resumed.events), event.PlanProposed) {
		t.Fatalf("the continuation did not proceed on the settled gap:\n%s", gapLoopTrace(resumed.events))
	}
	// And the settlement is durable and monotonic: a restarted process reads
	// it back, and a new observation of the same identity does not reopen it.
	restarted := &Engine{Store: store}
	if permits, settled := restarted.gapSettlement(taskID, Routing{Gap: gap}); !settled || !permits {
		t.Fatalf("a restarted process does not reconstruct the settlement: permits=%v settled=%v", permits, settled)
	}
	if r := restarted.observeGap(taskID, Routing{Route: RouteCloseGap, Gap: gap}); !r.Settled || r.Open() {
		t.Fatalf("observing a settled gap reopened it: %+v", r)
	}
	if _, ok := restarted.openGap(taskID, first.world); ok {
		t.Fatal("a settled gap is reported open after resume")
	}
}

// gapEscalateWithPremise is an escalation that itself rests on an unverified
// premise, so the escalation, not a proceed, is what reaches the gap.
const gapEscalateWithPremise = `{"decision":"escalate","summary":"unsure about main","plan":"inspect main.go","files":["main.go"],"mode":"inspect",` +
	`"human_question":"May main.go change?","claims":[{"statement":"main has no callers","about":"main.go","source":"inference"}]}`

// W4 -- an escalation that itself creates the gap is registered in P9 exactly
// as a proceed is, and an earlier answer whose condition TEXT matches but whose
// identity is another gap's does not authorize it.
func TestAnAnswerAboutAnotherGapDoesNotAuthorizeAnEscalationsGapByText(t *testing.T) {
	const taskID = "task-w4"
	store := sessionStore(t)
	rootFixtureTask(t, store, "s1", taskID, "change main.go")
	condition := "a bounded knowledge gap was not closed by investigation: " +
		"the plan rests on an unverified premise about main.go: main has no callers"
	other := GapIdentity{Kind: "coverage-absent", Scope: []string{"main.go"}}
	prior := resolvedAuthority{Resolution: authority.Resolution{
		TaskID: taskID, SessionID: "s1", Question: "May main.go change?", Condition: condition,
		OptionID: "1", OptionLabel: "Authorize the architectural change described above",
		Scope: []string{"main.go"}, Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC(),
	}, Gap: &other}
	if err := seedAppend(t, store, event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, prior.OptionLabel, prior)); err != nil {
		t.Fatal(err)
	}

	run := runGapLoop(t, store, taskID, gapEscalateWithPremise)
	if n := certifiedPrompts(run.prompts); n != 0 {
		t.Fatalf("an answer about another gap authorized this one by its text (%d certified round(s)):\n%s", n, gapLoopTrace(run.events))
	}
	for _, ev := range run.events {
		if strings.Contains(ev.Summary, "resolution rounds") {
			t.Fatalf("the run ended at the round ceiling: %q", ev.Summary)
		}
	}
	if !hasKind(kindsOf(run.events), event.AuthorityRequired) {
		t.Fatalf("the escalation's own gap was never put to the human:\n%s", gapLoopTrace(run.events))
	}
	var q DeferredAuthority
	payloadOf(t, run.events, event.WorkflowAwaitingAuthority, &q)
	want := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: run.world}
	if q.Gap == nil || q.Gap.Key() != want.Key() {
		t.Fatalf("the question is not about the escalation's own gap: %+v", q.Gap)
	}
	if open, ok := run.engine.openGap(taskID, run.world); !ok || open.Gap.Key() != want.Key() {
		t.Fatalf("the escalation's gap was not registered in the task's resolution: %+v %v", open, ok)
	}
}

// W5 -- only settlement is terminal. A gap a later plan stops reporting is
// inactive, not closed; the same identity observed again is open again, and
// condition-text history cannot authorize it. Settlement is the one exit, and
// the first one stands.
func TestAGapThatDisappearsIsNotSettledAndReopensOnTheSameIdentity(t *testing.T) {
	const (
		taskID = "task-w5"
		world  = "e7d3fede98ff88b89904b096a40b363adc9c5667"
		cond   = "the plan rests on an unverified premise about main.go: main has no callers"
	)
	store := sessionStore(t)
	rootFixtureTask(t, store, "s1", taskID, "change main.go")
	gap := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: world}
	// Text history: the gap's own human question, answered -- about another identity.
	asked := "a bounded knowledge gap was not closed by investigation: " + cond
	other := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: "another-world"}
	if err := seedAppend(t, store, event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, "Authorize",
		resolvedAuthority{Resolution: authority.Resolution{TaskID: taskID, SessionID: "s1", Question: "May main.go change?", OptionID: "1", Condition: asked,
			Scope: []string{"main.go"}, Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC()}, Gap: &other})); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Bus: event.NewBus(), Store: store, SessionID: "s1", pending: map[string]chan string{}}
	route := Routing{Route: RouteCloseGap, Condition: cond, Gap: gap}

	if r := e.observeGap(taskID, route); !r.Open() {
		t.Fatalf("an observed gap is not open: %+v", r)
	}
	// An inspect plan re-evaluates nothing it would change and records nothing.
	e.observeGapAbsence(taskID, world, Routing{Route: RouteArchitectural}, architectureDecision{Mode: ModeInspect, Files: []string{"main.go"}})
	if _, ok := e.openGap(taskID, world); !ok {
		t.Fatal("an inspect plan hid an open gap")
	}
	// A modifying plan over the whole scope that no longer reports it.
	e.observeGapAbsence(taskID, world, Routing{Route: RouteArchitectural}, architectureDecision{Mode: ModeModify, Files: []string{"main.go"}})
	if _, ok := e.openGap(taskID, world); ok {
		t.Fatal("a gap the latest modifying plan did not report is still reported as observed")
	}
	if _, settled := e.gapSettlement(taskID, route); settled {
		t.Fatal("disappearance settled the gap")
	}
	// The same identity, observed again: open again, owned by P9.
	if r := e.observeGap(taskID, route); !r.Open() || r.Settled {
		t.Fatalf("the re-identified gap is not open again: %+v", r)
	}
	if got, ok := e.openGap(taskID, world); !ok || got.Gap.Key() != gap.Key() {
		t.Fatalf("the re-identified gap is not the open one: %+v %v", got, ok)
	}
	disposed := route
	disposed.Route, disposed.Condition = RouteHuman, asked
	if _, settled := e.gapSettlement(taskID, disposed); settled {
		t.Fatal("the reopened gap reads as settled")
	}
	if authorized, answered := e.applyAnsweredCondition(taskID, asked, "main.go"); authorized || answered {
		t.Fatal("condition-text history authorized a gap an identity-bound answer never settled")
	}
	// Settlement: a revise answer leaves it standing, an explicit authorize
	// settles it, and a later answer cannot override the first settlement.
	e.settleGap(taskID, gap, authority.Revise)
	if _, settled := e.gapSettlement(taskID, route); settled {
		t.Fatal("a revise answer settled the gap")
	}
	e.settleGap(taskID, gap, authority.Authorize)
	e.settleGap(taskID, gap, authority.Stop)
	if permits, settled := e.gapSettlement(taskID, route); !settled || !permits {
		t.Fatalf("settlement is not monotonic: permits=%v settled=%v", permits, settled)
	}
	if r := e.observeGap(taskID, route); r.Open() {
		t.Fatal("observing a settled gap reopened it")
	}
}

// W4, the other half of requirement 5: when the gap an escalation reaches is
// ALREADY settled for exactly its identity, the settlement is consumed where the
// gap is registered -- no closure round is spent re-investigating it, and the
// human is not asked again.
func TestAnEscalationConsumesTheSettlementOfItsOwnGapBeforeTheClosureBudget(t *testing.T) {
	const taskID = "task-w4-settled"
	store := sessionStore(t)
	rootFixtureTask(t, store, "s1", taskID, "change main.go")
	e, architect, world := newGapLoopEngine(t, nil, store, gapEscalateWithPremise, gapReply)
	own := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: world}
	settled := resolvedAuthority{Resolution: authority.Resolution{
		TaskID: taskID, SessionID: "s1", Question: "May main.go change?",
		Condition: "a bounded knowledge gap was not closed by investigation: the plan rests on an unverified premise about main.go: main has no callers",
		OptionID:  "1", OptionLabel: "Authorize the architectural change described above",
		Scope: []string{"main.go"}, Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC(),
	}, Gap: &own}
	if err := seedAppend(t, store, event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, settled.OptionLabel, settled)); err != nil {
		t.Fatal(err)
	}
	run := driveGapLoop(t, e, architect, world, taskID, "", func(ctx context.Context) {
		runRootedTask(ctx, e, taskID, "change main.go", RequestedByHuman)
	})
	for _, ev := range run.events {
		if ev.Kind == event.Status && strings.Contains(ev.Summary, "closing it instead") {
			t.Fatalf("a closure round was spent on a gap an explicit answer settled: %q", ev.Summary)
		}
	}
	if hasKind(kindsOf(run.events), event.AuthorityRequired) {
		t.Fatalf("the human was asked about a gap they already settled:\n%s", gapLoopTrace(run.events))
	}
	if len(run.prompts) != 2 {
		t.Fatalf("the architect was asked %d time(s), want 2 (the escalation, then the round its settled answer opens)", len(run.prompts))
	}
}

// proposalSenseiScript is resumeSenseiScript with one change of purpose: it
// names no origin when it starts, and names one only when awareness_propose is
// called. The fixture repository's origin is therefore the observable trace of
// an authority-resolution proposal having been written (W3), readable through
// gitx without an import this file does not already have.
func proposalSenseiScript(t *testing.T) string {
	t.Helper()
	const startup = "git remote add origin https://github.com/globulario/sensei-code.git >/dev/null 2>&1\n"
	const fallback = "\t*)\n\t\tresult='{\"content\":[{\"type\":\"text\",\"text\":\"the stub graph holds nothing about this\"}]"
	if strings.Count(resumeSenseiScript, startup) != 1 || strings.Count(resumeSenseiScript, fallback) != 1 {
		t.Fatal("resumeSenseiScript changed shape; the proposal stub was not derived from it")
	}
	script := strings.Replace(resumeSenseiScript, startup, "", 1)
	return strings.Replace(script, fallback,
		"\t*'\"name\":\"awareness_propose\"'*)\n"+
			"\t\tgit remote add origin https://proposal.written.invalid/ >/dev/null 2>&1\n"+
			"\t\tresult='{\"content\":[{\"type\":\"text\",\"text\":\"proposed\"}],\"structuredContent\":{}}' ;;\n"+fallback, 1)
}

// resumeAtRefusedPrecondition drives `resume --answer 1` for a task whose
// candidate identity is already established and whose standing question was
// deferred, and reports everything the refusal must not have done.
func resumeAtRefusedPrecondition(t *testing.T, moveBase bool) (e *Engine, seen []event.Event, consumed bool, before, after []event.Event) {
	t.Helper()
	ctx := context.Background()
	e, events, _ := resumeHarness(t, false)
	e.Config.Sensei.Command = "sh"
	e.Config.Sensei.Args = []string{"-c", proposalSenseiScript(t)}

	const taskID = "task-r"
	q := deferScoped(t, taskID, planScope())
	// The question as this task's holder deferred it: asked in its session.
	q.SessionID = e.SessionID
	for _, ev := range []event.Event{
		event.New(e.SessionID, taskID, event.SourceUser, event.TaskCreated, "the objective", nil),
		event.New(e.SessionID, taskID, event.SourceUser, event.WorkflowAwaitingAuthority, q.Condition, q),
	} {
		if err := seedAppend(t, e.Store, ev); err != nil {
			t.Fatal(err)
		}
	}
	if moveBase {
		// main advances after the question was deferred: a new commit on the
		// checked-out branch, same tree, so HEAD moves and nothing else does.
		base, err := e.Repo.Head(ctx)
		if err != nil {
			t.Fatal(err)
		}
		tree, err := e.Repo.CommitTreeOf(ctx, base)
		if err != nil {
			t.Fatal(err)
		}
		advanced, err := e.Repo.MintCanonicalCommit(ctx, base, tree)
		if err != nil {
			t.Fatal(err)
		}
		branch, err := e.Repo.Branch(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := e.Repo.PointBranchAt(ctx, branch, advanced); err != nil {
			t.Fatal(err)
		}
		if head, _ := e.Repo.Head(ctx); head == base {
			t.Fatal("the fixture did not move HEAD off the recorded base")
		}
	}
	var err error
	if before, err = e.Store.Load(); err != nil {
		t.Fatal(err)
	}
	standing := session.FindInterrupted(before)
	if len(standing) != 1 || len(standing[0].AwaitingAuthority) == 0 {
		t.Fatalf("the fixture does not hold one task with a standing question: %+v", standing)
	}

	e.Resume(ctx, standing[0])
	deadline := time.After(30 * time.Second)
	for {
		// A person answering the moment the question is asked. A refusal
		// decided before the rendezvous leaves nobody to take this answer.
		if e.ResolveHuman(taskID, "1") {
			consumed = true
		}
		select {
		case ev := <-events:
			seen = append(seen, ev)
			if _, terminal := event.RunTerminality(ev.Kind); terminal {
				if after, err = e.Store.Load(); err != nil {
					t.Fatal(err)
				}
				return e, seen, consumed, before, after
			}
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the resumed invocation did not end: %v", kindsOf(seen))
		}
	}
}

// assertRefusedBeforeTheAnswer holds what W1, the f1 dirty case and W3 share:
// the typed invocation terminal, no answer consumed, no question re-asked, no
// resolution proposal written, and the task still resumable with its question
// standing byte for byte.
func assertRefusedBeforeTheAnswer(t *testing.T, e *Engine, seen []event.Event, consumed bool, before, after []event.Event, want event.Kind) {
	t.Helper()
	if consumed {
		t.Error("the answer was consumed: the question was re-asked before the precondition refused")
	}
	for _, ev := range seen {
		switch ev.Kind {
		case event.AuthorityResolved, event.AuthorityRequired:
			t.Errorf("the refused resume reached the authority rendezvous: %s %s", ev.Kind, ev.Summary)
		}
	}
	// W3: nothing reached awareness_propose.
	if origin := e.Repo.OriginURL(context.Background()); origin != "" {
		t.Errorf("an authority-resolution proposal was written for a refused resume (origin %q)", origin)
	}
	last := seen[len(seen)-1]
	if last.Kind != want {
		t.Fatalf("the resume ended %s (%s), want %s", last.Kind, last.Summary, want)
	}
	if terminality, ok := event.RunTerminality(last.Kind); !ok || terminality != event.InvocationTerminal {
		t.Fatalf("%s is classified %q (ok=%v), want invocation-terminal", last.Kind, terminality, ok)
	}
	standing := session.FindInterrupted(after)
	if len(standing) != 1 || standing[0].TaskID != "task-r" {
		t.Fatalf("the refused task left the resumable set: %+v", standing)
	}
	if standing[0].PreconditionRefusal != want || !strings.Contains(standing[0].PreconditionRefusalReason, "Nothing was executed") {
		t.Errorf("the task does not say why the resume refused: kind=%q reason=%q",
			standing[0].PreconditionRefusal, standing[0].PreconditionRefusalReason)
	}
	if was := session.FindInterrupted(before); string(standing[0].AwaitingAuthority) != string(was[0].AwaitingAuthority) {
		t.Errorf("the standing question changed:\n got %s\nwant %s", standing[0].AwaitingAuthority, was[0].AwaitingAuthority)
	}
}

// W1 -- R2 x AR. `resume --answer` after main advanced past the task's recorded
// candidate base. Measured on task-1790489127599728062 (2026-09-27): the answer
// was consumed and persisted, THEN candidate.ErrBaseMoved refused, and the task
// left resume --list as FAILED. The refusal is right; it must come before the
// answer, and end the invocation only.
func TestAResumeWhoseBaseMovedRefusesBeforeConsumingTheAnswer(t *testing.T) {
	e, seen, consumed, before, after := resumeAtRefusedPrecondition(t, true)
	assertRefusedBeforeTheAnswer(t, e, seen, consumed, before, after, event.WorkflowBaseMovedRefused)
}

// f1 -- an existing identity, HEAD unchanged, and a canonical checkout that is
// not clean. The fixture's only dirt is untracked workflow state (.sensei-code/,
// which a real repository ignores), so an exemption for workflow-owned paths
// would pass it and fail this witness: cleanliness is the repository-wide
// condition, read once, before the answer rendezvous.
func TestAResumeOverADirtyCanonicalCheckoutRefusesBeforeConsumingTheAnswer(t *testing.T) {
	body := funcBody(t, "internal/workflow/engine.go", "resumePrecondition")
	if n := strings.Count(body, "IsClean("); n != 1 || !strings.Contains(body, "e.Repo.IsClean(") {
		t.Fatalf("resumePrecondition consults %d cleanliness surfaces; it must call e.Repo.IsClean exactly once", n)
	}
	resume := funcBody(t, "internal/workflow/engine.go", "resumeAuthority")
	pre, start := strings.Index(resume, "e.resumePrecondition("), strings.Index(resume, "sensei.Start(")
	if pre < 0 || start < 0 || pre > start || pre > strings.Index(resume, "e.awaitChoice(") {
		t.Fatal("resumeAuthority does not decide its candidate precondition before starting Sensei and awaiting the answer")
	}

	e, seen, consumed, before, after := resumeAtRefusedPrecondition(t, false)
	if clean, err := e.Repo.IsClean(context.Background()); err != nil || clean {
		t.Fatalf("the fixture's canonical checkout is not dirty (clean=%v, err=%v)", clean, err)
	}
	// HEAD is the recorded base here: the refusal below is typed as a dirty
	// checkout, which the base check -- decided first -- would have preempted.
	assertRefusedBeforeTheAnswer(t, e, seen, consumed, before, after, event.WorkflowDirtyCanonicalRefused)
}

// ---------------------------------------------------------------------------
// DF-30 (objective 59) -- THE GAP LIFECYCLE. A coverage gap is one identity
// observed, re-evaluated, authorized, exhausted and disposed of across several
// production transitions. Each witness below drives registerRouting, the one
// P9 sequence the proceed, escalate and post-authorization routes all run, over
// routings the production router (routeAuthorityForAction, afterAuthorization)
// computed, with grants recorded through the production recorder.
// ---------------------------------------------------------------------------

// df30Engine is an engine routing df30Attempt for taskID, with a durable record.
func df30Engine(t *testing.T, taskID string) *Engine {
	t.Helper()
	return df30EngineOn(t, sessionStore(t), taskID)
}

// df30EngineOn is df30Engine over an existing durable record: a restarted
// process, which rebuilds its ledger from store.
func df30EngineOn(t *testing.T, store *session.Store, taskID string) *Engine {
	t.Helper()
	e := &Engine{Bus: event.NewBus(), Store: store, SessionID: "s1", pending: map[string]chan string{}}
	rootFixtureTaskOnce(t, e, taskID, "the objective")
	attempt := df30Attempt()
	attempt.TaskID = taskID
	e.notePlanAttemptStarted(taskID, attempt.ID)
	e.mu.Lock()
	e.planAttemptsOf(taskID).pending = attempt
	e.mu.Unlock()
	return e
}

// df30Record writes a prospective grant record for df30Attempt through the
// production recorder.
func df30Record(t *testing.T, e *Engine, taskID string, grants []prospectiveGrant) {
	t.Helper()
	if err := e.recordProspectiveGrants(fixtureCtx(e, taskID), taskID, "prospective authority recorded during the closure round", df30Recorded(grants)); err != nil {
		t.Fatal(err)
	}
}

// df30NextAttempt makes a fresh plan attempt id, durably started for taskID at
// the pinned world, the one routing has pending: a re-plan of the same task.
func df30NextAttempt(t *testing.T, e *Engine, taskID, id string) {
	t.Helper()
	e.notePlanAttemptStarted(taskID, id)
	e.mu.Lock()
	e.planAttemptsOf(taskID).pending = planAttempt{ID: id, TaskID: taskID, World: prospectiveWorld}
	e.mu.Unlock()
}

// df30RecordPending writes a prospective grant record for the attempt routing
// has pending, through the production recorder.
func df30RecordPending(t *testing.T, e *Engine, taskID string, grants []prospectiveGrant) {
	t.Helper()
	rec := prospectiveRecord{PlanAttemptID: e.pendingPlanAttempt(taskID).ID, World: prospectiveWorld, Grants: grants}
	if err := e.recordProspectiveGrants(fixtureCtx(e, taskID), taskID, "prospective authority recorded during the closure round", rec); err != nil {
		t.Fatal(err)
	}
}

// openCoverageGaps are the coverage identities open for taskID at world.
func openCoverageGaps(e *Engine, taskID, world string) []AuthorityResolution {
	tr := e.gapResolutions(taskID)
	pending := e.pendingPlanAttempt(taskID).ID
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []AuthorityResolution
	for _, key := range tr.order {
		if r := tr.view(key, pending); r.Open() && r.Gap.World == world && isCoverageGapKind(r.Gap.Kind) {
			out = append(out, r)
		}
	}
	return out
}

func resolutionOf(e *Engine, taskID string, gap GapIdentity) AuthorityResolution {
	tr := e.gapResolutions(taskID)
	e.mu.Lock()
	defer e.mu.Unlock()
	if r := tr.byKey[gap.Key()]; r != nil {
		return *r
	}
	return AuthorityResolution{}
}

// assertEpisodeOfKind is assertOneEpisodeOwnedBy for a run whose later
// routing may legitimately raise a gap of ANOTHER kind -- after authorization
// the unexamined question is asked beside a region episode. The identities
// that existed stay, in order, and no second identity of the episode's own
// kind and world is opened for its remaining members.
func assertEpisodeOfKind(t *testing.T, e *Engine, taskID string, opened GapIdentity, before gapLedger, owner string) AuthorityResolution {
	t.Helper()
	after := ledgerOf(e, taskID)
	if len(after.order) < len(before.order) || !sameFiles(after.order[:len(before.order)], before.order) {
		t.Fatalf("the re-evaluation changed the ledger's identities:\n before %q\n after  %q", before.order, after.order)
	}
	tr := e.gapResolutions(taskID)
	for _, key := range after.order[len(before.order):] {
		if g := tr.byKey[key].Gap; g.Kind == opened.Kind && g.World == opened.World {
			t.Fatalf("a second %s identity %q was opened beside the episode %q", g.Kind, key, opened.Key())
		}
	}
	key := opened.Key()
	if after.owners[key] != owner {
		t.Fatalf("the question owner of %s is %q, want %q (it was %q)", key, after.owners[key], owner, before.owners[key])
	}
	r := resolutionOf(e, taskID, opened)
	if r.Gap.Key() != key {
		t.Fatalf("the episode's current form has key %q, want the key it opened with %q", r.Gap.Key(), key)
	}
	return r
}

// gapLedger is the P9 ledger state a narrowing must leave unchanged: which
// identities exist, in order, and which plan attempt owns each one's question.
type gapLedger struct {
	order  []string
	owners map[string]string
}

func ledgerOf(e *Engine, taskID string) gapLedger {
	tr := e.gapResolutions(taskID)
	e.mu.Lock()
	defer e.mu.Unlock()
	l := gapLedger{order: append([]string(nil), tr.order...), owners: map[string]string{}}
	for k, v := range tr.observedBy {
		l.owners[k] = v
	}
	return l
}

// assertOneEpisode proves a re-evaluation of opened, however it narrowed it,
// kept ONE ledger identity: no entry was added or reordered, the resolution
// count is unchanged, the entry keyed by the gap as it opened holds the current
// narrowed form (whose Key is still that key), and its live question is owned
// by the attempt the episode's lifecycle now names (liveOwner). It returns the
// current form.
func assertOneEpisode(t *testing.T, e *Engine, taskID string, opened GapIdentity, before gapLedger) AuthorityResolution {
	t.Helper()
	if before.owners[opened.Key()] == "" {
		t.Fatalf("premise: no plan attempt owns the question of %s", opened.Key())
	}
	return assertOneEpisodeOwnedBy(t, e, taskID, opened, before, liveOwner(t, e, taskID, opened, before.owners[opened.Key()]))
}

// liveOwner is the plan attempt that must own the live question of the
// episode opened after a re-evaluation (DF-30A review f1): while the episode
// is open, the attempt currently routing it -- never, merely because it opened
// the episode, an earlier one. An episode the re-evaluation closed asks no
// live question; its owner is the one it had, or the attempt that last
// observed it.
func liveOwner(t *testing.T, e *Engine, taskID string, opened GapIdentity, previous string) string {
	t.Helper()
	pending := e.pendingPlanAttempt(taskID).ID
	tr := e.gapResolutions(taskID)
	e.mu.Lock()
	open := tr.view(opened.Key(), pending).Open()
	by := tr.observedBy[opened.Key()]
	e.mu.Unlock()
	if open || by == pending {
		return pending
	}
	return previous
}

// assertOneEpisodeOwnedBy is assertOneEpisode with the owning attempt named.
func assertOneEpisodeOwnedBy(t *testing.T, e *Engine, taskID string, opened GapIdentity, before gapLedger, owner string) AuthorityResolution {
	t.Helper()
	after := ledgerOf(e, taskID)
	if !sameFiles(after.order, before.order) {
		t.Fatalf("the re-evaluation changed the ledger's identities:\n before %q\n after  %q", before.order, after.order)
	}
	if got := len(e.gapResolutions(taskID).byKey); got != len(before.order) {
		t.Fatalf("the ledger holds %d resolutions, want %d", got, len(before.order))
	}
	key := opened.Key()
	if after.owners[key] != owner {
		t.Fatalf("the question owner of %s is %q, want %q (it was %q)", key, after.owners[key], owner, before.owners[key])
	}
	r := resolutionOf(e, taskID, opened)
	if r.Gap.Key() != key {
		t.Fatalf("the episode's current form has key %q, want the key it opened with %q", r.Gap.Key(), key)
	}
	if r.Routing.ClosesGap() && r.Routing.Gap.Key() != key {
		t.Fatalf("the episode's current routing names another identity %q", r.Routing.Gap.Key())
	}
	return r
}

// W5 -- THE CUT POINT. A coverage-unexamined gap is open over absent planned
// creates; during the closure round the matching prospective grant is recorded
// through the production recorder; the same gap is re-evaluated from that
// record before any further decision. A fully granted gap closes and the run
// proceeds; a partly granted one keeps only its ungranted member.
func TestDF30W5AGrantRecordedInTheClosureRoundSettlesTheSameGap(t *testing.T) {
	decl, grants, out := df30Measured(t)
	scoped := scopedPreflight(t, neighbourCovered)
	d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: df30Planned(), ProspectiveSurfaces: decl}
	for name, c := range map[string]struct {
		recorded []prospectiveGrant
		want     []string
	}{
		"every create granted": {grants, nil},
		"one create granted":   {withoutGrantFor(grants, existingWindows), []string{existingWindows}},
		"no create granted":    {nil, []string{existingUnix, existingWindows}},
	} {
		t.Run(name, func(t *testing.T) {
			taskID := "task-df30-w5"
			e := df30Engine(t, taskID)
			before := df30Action(out, e.prospectiveUnitsFor(taskID, d))
			opened := routeAuthorityForAction(scoped, nil, before)
			if !opened.ClosesGap() || !sameFiles(opened.Gap.Scope, []string{existingUnix, existingWindows}) {
				t.Fatalf("premise: before any grant the creates hold the gap open: %+v", opened)
			}
			opened.Gap.World = prospectiveWorld
			if _, r := e.registerRouting(taskID, prospectiveWorld, opened, before, scoped, d, true); !r.Open() {
				t.Fatalf("premise: the gap is registered open: %+v", r)
			}
			ledger := ledgerOf(e, taskID)

			// The closure round re-plans: a different plan attempt, whose own
			// grant is recorded through the production recorder.
			df30NextAttempt(t, e, taskID, "pa-df30-w5-replan")
			df30RecordPending(t, e, taskID, c.recorded)

			after := df30Action(out, e.prospectiveUnitsFor(taskID, d))
			fresh := routeAuthorityForAction(scoped, nil, after)
			if fresh.Gap.Identified() {
				fresh.Gap.World = prospectiveWorld
			}
			routed, _ := e.registerRouting(taskID, prospectiveWorld, fresh, after, scoped, d, true)
			current := assertOneEpisode(t, e, taskID, opened.Gap, ledger)
			open := openCoverageGaps(e, taskID, prospectiveWorld)
			if c.want == nil {
				if !routed.Granted() || len(open) != 0 {
					t.Fatalf("a fully granted gap did not close and let the run proceed: %+v open=%+v", routed, open)
				}
				if dp := current.Disposition; dp == nil || dp.Open() {
					t.Fatalf("the gap identity was not re-evaluated to settled: %+v", dp)
				}
				return
			}
			if len(open) != 1 || !sameFiles(open[0].Gap.Scope, c.want) || !routed.ClosesGap() || !sameFiles(routed.Gap.Scope, c.want) {
				t.Fatalf("the re-evaluated gap is not exactly its ungranted members %v: routed=%+v open=%+v", c.want, routed, open)
			}
			if routed.Gap.Key() != opened.Gap.Key() || open[0].Gap.Key() != opened.Gap.Key() {
				t.Fatalf("the narrowed gap is not the identity that opened: routed %q open %q opened %q",
					routed.Gap.Key(), open[0].Gap.Key(), opened.Gap.Key())
			}
			if len(c.want) == 2 {
				return
			}
			assertNarrowedAnswersReturnToTheEpisode(t, e, taskID, opened.Gap, routed)
		})
	}
}

// assertNarrowedAnswersReturnToTheEpisode proves a question asked, and an
// answer given, about the narrowed form of a gap belong to the episode it
// opened as -- its one ledger identity -- and to the plan attempt CURRENTLY
// routing it, not to the attempt that opened it (DF-30A review f1). The
// question is asked and answered through the real authority rendezvous
// (awaitHuman), and the answer is consumed through gapSettlement: it
// authorizes the current attempt, while the opening attempt and an unrelated
// attempt remain unsettled. A restarted process restores no authority from the
// record (RULING-181).
func assertNarrowedAnswersReturnToTheEpisode(t *testing.T, e *Engine, taskID string, opened GapIdentity, routed Routing) {
	t.Helper()
	narrowed := routed.Gap
	opener := df30Attempt().ID
	current := e.pendingPlanAttempt(taskID).ID
	if current == opener {
		t.Fatalf("premise: the episode is routed by a later attempt than the one that opened it (%q)", opener)
	}
	if owner := e.questionOwner(taskID, narrowed); owner != current {
		t.Fatalf("the narrowed question is about %q, want the attempt currently routing it %q (opener %q)", owner, current, opener)
	}
	if authorized, settled := e.gapSettlement(taskID, routed); authorized || settled {
		t.Fatal("premise: the narrowed episode is unsettled before anyone answers it")
	}

	// Ask through the one rendezvous, and answer it.
	ledger := ledgerOf(e, taskID)
	d := architectureDecision{Decision: "escalate", Mode: ModeModify, Files: narrowed.Scope, HumanQuestion: "May the run proceed?"}
	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitHuman(fixtureCtx(e, taskID), nil, certifiedStart{}, taskID, d, routed.Condition, narrowed)
		errc <- err
	}()
	waitForPending(t, e, taskID)
	if !e.ResolveHuman(taskID, "1") {
		t.Fatal("the narrowed question was not pending at the rendezvous")
	}
	if err := <-errc; err != nil {
		t.Fatalf("the live answer was refused: %v", err)
	}
	tr := e.gapResolutions(taskID)
	e.mu.Lock()
	answers := append([]gapAnswer(nil), tr.settlements[opened.Key()]...)
	e.mu.Unlock()
	if r := assertOneEpisodeOwnedBy(t, e, taskID, opened, ledger, current); len(answers) != 1 ||
		answers[0].owner != current || !sameFiles(r.Gap.Scope, narrowed.Scope) {
		t.Fatalf("the answer was not recorded against the episode that opened, for the attempt that asked it: %+v", answers)
	}
	var recorded *resolvedAuthority
	history, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range history {
		var res resolvedAuthority
		if ev.TaskID == taskID && ev.Kind == event.AuthorityResolved && json.Unmarshal(ev.Payload, &res) == nil && res.Gap != nil {
			recorded = &res
		}
	}
	// The durable record names the attempt and the narrowed form it was
	// asked about; it carries no live episode binding (RULING-181).
	if recorded == nil || recorded.PlanAttemptID != current || recorded.Gap.Opening != nil || !sameFiles(recorded.Gap.Scope, narrowed.Scope) {
		t.Fatalf("the durable answer does not name the attempt that asked it and the form it is about: %+v", recorded)
	}

	// Consumed by the live boundary, for the attempt that asked it -- and by
	// no other.
	if authorized, settled := e.gapSettlement(taskID, routed); !authorized || !settled {
		t.Fatalf("the explicit answer did not settle the episode for the attempt currently routing it: authorized=%v settled=%v", authorized, settled)
	}
	for _, other := range []string{opener, "pa-df30-unrelated"} {
		e.notePlanAttemptStarted(taskID, other)
		e.mu.Lock()
		e.planAttemptsOf(taskID).pending = planAttempt{ID: other, TaskID: taskID, World: prospectiveWorld}
		e.mu.Unlock()
		if authorized, settled := e.gapSettlement(taskID, routed); authorized || settled {
			t.Fatalf("attempt %q consumed an answer given about attempt %q", other, current)
		}
	}
	e.mu.Lock()
	e.planAttemptsOf(taskID).pending = planAttempt{ID: current, TaskID: taskID, World: prospectiveWorld}
	e.mu.Unlock()

	// The question as a deferral would have recorded it, so a restarted
	// process reads it back.
	q := DeferredAuthority{Condition: routed.Condition, TaskID: taskID, SessionID: "s1", Scope: narrowed.Scope,
		ScopeRecorded: true, Gap: &narrowed, PlanAttemptID: current,
		Decision: authority.Decision{Level: authority.Human, Subject: "May this change proceed?", Options: realOptions()}}
	if err := seedAppend(t, e.Store, event.New("s1", taskID, event.SourceUser, event.WorkflowAwaitingAuthority, q.Condition, q)); err != nil {
		t.Fatal(err)
	}
	assertRecordedCoverageQuestionFailsClosed(t, e, taskID, current, narrowed, routed)
}

// assertRecordedCoverageQuestionFailsClosed proves the RULING-181 restore
// boundary for a coverage question and answer the live process recorded: a
// fresh process reading the record installs no historical authority -- the
// recorded answer settles nothing -- and refuses, typed, to put the restored
// question to a person, because the record carries no episode it can
// authenticate.
func assertRecordedCoverageQuestionFailsClosed(t *testing.T, e *Engine, taskID, owner string, asked GapIdentity, routed Routing) {
	t.Helper()
	raw, err := json.Marshal(asked)
	if err != nil {
		t.Fatal(err)
	}
	var recorded GapIdentity
	if err := json.Unmarshal(raw, &recorded); err != nil {
		t.Fatal(err)
	}
	if recorded.Opening != nil || recorded.Semantics != nil {
		t.Fatalf("a durable record carries live episode state: %s", raw)
	}
	restored := &Engine{Bus: event.NewBus(), Store: e.Store, SessionID: "s1", pending: map[string]chan string{}}
	restored.notePlanAttemptStarted(taskID, owner)
	restored.mu.Lock()
	restored.planAttemptsOf(taskID).pending = planAttempt{ID: owner, TaskID: taskID, World: prospectiveWorld}
	restored.mu.Unlock()
	for _, g := range []GapIdentity{recorded, routed.Gap} {
		if authorized, settled := restored.gapSettlement(taskID, Routing{Gap: g}); authorized || settled {
			t.Fatalf("a restarted process installed the recorded coverage answer as authority: authorized=%v settled=%v", authorized, settled)
		}
	}
	r := resolutionOf(restored, taskID, recorded)
	if !r.restored || !r.Open() || r.Disposition != nil {
		t.Fatalf("the restored coverage identity is not preserved unresolved and unauthenticated: %+v", r)
	}
	refusal := restored.unauthenticatedCoverageRestore(taskID, recorded)
	if refusal == nil || refusal.Subject != restorationSubjectCoverageEpisode || refusal.Binding != RestorationRecordUnreadable {
		t.Fatalf("the restored coverage question is not refused before it is asked: %+v", refusal)
	}
	if _, err := ParseRestorationRefusal(mustJSON(t, refusal)); err != nil {
		t.Fatalf("the refusal is not a valid typed restoration refusal: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// W5 -- AN OVERLAPPING OR WIDER RE-PLAN CONTINUES THE EPISODE. The retried
// architect may move, narrow or widen its plan; it may not thereby change the
// episode's identity (episodeLineage.continuation). A coverage gap a later plan
// attempt reports over members that overlap the episode's is that episode,
// updated in place: one ledger identity, its key and position unchanged, its
// live question owned by the attempt now routing it, never a second scope-derived entry. A member the re-plan withdraws
// leaves its scope; a member it adds joins it; a question and answer about the
// widened form return to the episode live, and a restart restores no
// authority from them (RULING-181).
func TestDF30W5AnOverlappingOrWiderReplanContinuesTheEpisode(t *testing.T) {
	_, _, out := df30Measured(t)
	scoped := scopedPreflight(t, neighbourCovered)
	extra := "internal/session/zz_present_unexamined.go"
	const taskID = "task-df30-w5-overlap"
	e := df30Engine(t, taskID)
	route := func(planned, unexamined []string) Routing {
		t.Helper()
		action := df30Action(out, nil)
		action.Files, action.Unexamined = planned, unexamined
		for _, f := range planned {
			if f == extra {
				action.Present = append(action.Present, extra)
			}
		}
		fresh := routeAuthorityForAction(scoped, nil, action)
		if !fresh.ClosesGap() || !sameFiles(fresh.Gap.Scope, unexamined) {
			t.Fatalf("premise: the routing reports exactly %v: %+v", unexamined, fresh)
		}
		fresh.Gap.World = prospectiveWorld
		d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: planned}
		routed, r := e.registerRouting(taskID, prospectiveWorld, fresh, action, scoped, d, true)
		if !r.Open() {
			t.Fatalf("the continued episode is not open: %+v", r)
		}
		return routed
	}
	opened := route(df30Planned(), []string{existingUnix, existingWindows})
	ledger := ledgerOf(e, taskID)

	var moved []string
	for _, f := range df30Planned() {
		if f != existingUnix {
			moved = append(moved, f)
		}
	}
	for _, c := range []struct {
		name, attempt       string
		planned, unexamined []string
	}{
		{"moved", "pa-df30-w5-moved", append(moved, extra), []string{existingWindows, extra}},
		{"wider", "pa-df30-w5-wider", append(df30Planned(), extra), []string{existingUnix, existingWindows, extra}},
	} {
		df30NextAttempt(t, e, taskID, c.attempt)
		routed := route(c.planned, c.unexamined)
		current := assertOneEpisode(t, e, taskID, opened.Gap, ledger)
		if routed.Gap.Key() != opened.Gap.Key() || !sameFiles(current.Gap.Scope, c.unexamined) || current.Routing.Condition != routed.Condition {
			t.Fatalf("%s: the re-plan did not continue the episode in place: routed=%+v current=%+v", c.name, routed, current)
		}
		for _, f := range []string{existingUnix, existingWindows, extra} {
			named := false
			for _, u := range c.unexamined {
				named = named || u == f
			}
			if named != strings.Contains(routed.Condition, f) {
				t.Fatalf("%s: scope %v and condition disagree about %s: %q", c.name, c.unexamined, f, routed.Condition)
			}
		}
		if c.name == "wider" {
			assertNarrowedAnswersReturnToTheEpisode(t, e, taskID, opened.Gap, routed)
		}
	}
}

// W10 -- UNPROBED ACTION. A routing whose action carries neither Examined nor
// Unexamined data settles nothing by examination: an open two-file gap keeps
// both members -- and outranks the grant such a routing would reach -- unless
// another positive mechanism settles one. With one exact grant, exactly the
// other remains.
func TestDF30W10AnUnprobedActionPreservesTheGap(t *testing.T) {
	decl, grants, out := df30Measured(t)
	scoped := scopedPreflight(t, neighbourCovered)
	d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: df30Planned(), ProspectiveSurfaces: decl}
	for name, c := range map[string]struct {
		recorded []prospectiveGrant
		want     []string
	}{
		"no settling mechanism": {nil, []string{existingUnix, existingWindows}},
		"one exact grant":       {withoutGrantFor(grants, existingWindows), []string{existingWindows}},
	} {
		t.Run(name, func(t *testing.T) {
			taskID := "task-df30-w10"
			e := df30Engine(t, taskID)
			// The payload the router writes for this gap under this preflight.
			sem := coverageSemantics(gapCoverageUnexamined, readBlindSpots(scoped.BlindSpots).Coverage, "")
			prior := Routing{Route: RouteCloseGap, Basis: BasisLacksKnowledge, Gap: GapIdentity{Kind: gapCoverageUnexamined,
				Scope: []string{existingUnix, existingWindows}, World: prospectiveWorld, Semantics: &sem}}
			e.registerRouting(taskID, prospectiveWorld, prior, df30Action(out, nil), scoped, d, true)
			ledger := ledgerOf(e, taskID)
			df30NextAttempt(t, e, taskID, "pa-df30-w10-replan")
			if c.recorded != nil {
				df30RecordPending(t, e, taskID, c.recorded)
			}
			unprobed := df30Action(out, e.prospectiveUnitsFor(taskID, d))
			unprobed.Examined, unprobed.Unexamined = nil, nil
			fresh := routeAuthorityForAction(scoped, nil, unprobed)
			if !fresh.Granted() {
				t.Fatalf("premise: the unprobed routing alone would grant: %+v", fresh)
			}
			routed, _ := e.registerRouting(taskID, prospectiveWorld, fresh, unprobed, scoped, d, true)
			open := openCoverageGaps(e, taskID, prospectiveWorld)
			if routed.Granted() || len(open) != 1 || !sameFiles(open[0].Gap.Scope, c.want) || !sameFiles(routed.Gap.Scope, c.want) {
				t.Fatalf("missing probe data settled a member, or the open gap was overridden by a grant: routed=%+v open=%+v", routed, open)
			}
			assertOneEpisode(t, e, taskID, prior.Gap, ledger)
			if routed.Gap.Key() != prior.Gap.Key() || open[0].Gap.Key() != prior.Gap.Key() {
				t.Fatalf("the preserved or narrowed gap is not the identity that opened: routed %q open %q", routed.Gap.Key(), open[0].Gap.Key())
			}
		})
	}
}

// W14 (supplemental, component level; TestDF30W14TheProductionLifecycleDecidesOneEpisode
// is the production witness) -- GAP LIFECYCLE ORDER. For each coverage-gap kind, entry
// route and settling fact, a prior gap is re-evaluated as the same identity
// before absence projection could deactivate it; only a fact authorized for the
// kind settles a member; the outcome is the same through either entry route;
// scope and condition describe the same members; an unresolved member stays
// open and a settled identity closes.
func TestDF30W14TheGapLifecycleReEvaluatesBeforeAbsenceProjection(t *testing.T) {
	create, examinedFile := existingUnix, df30Main
	planned := []string{existingS, examinedFile, create}
	decl := existingDeclarations()[:1]
	anchors := derivedAnchorNaming(t, existingS, existingSibling)
	grants, out := coverPlannedAtWorld(context.Background(), prospectiveWorld, planned, decl, anchors, worldOf(df30World()))
	if len(grants) != 1 {
		t.Fatalf("premise: the create is granted, got %+v", grants)
	}
	gate := func(body string) string {
		return strings.Replace(body, `"approval_gate":"APPROVAL_GATE_NONE"`, `"approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"`, 1)
	}
	kinds := []struct {
		kind  string
		plain string
	}{
		// The region answer is about the three planned files, as the per-file
		// probe of the post-authorization continuation requires.
		{gapCoverageUnexamined, strings.Replace(strings.Replace(gatedNeighbourCovered, "APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED", "APPROVAL_GATE_NONE", 1),
			`"file_count":2`, `"file_count":3`, 1)},
		{gapCoverageAbsent, regionAbsent},
		{gapCoverageBlindSpot, regionBlindSpot},
	}
	facts := []struct {
		name  string
		grant bool
		apply func(a *Action)
		// want maps a kind to the members that must remain unresolved.
		want map[string][]string
	}{
		{"confirmed prospective grant", true, func(*Action) {}, map[string][]string{
			gapCoverageUnexamined: {examinedFile}, gapCoverageAbsent: {examinedFile}, gapCoverageBlindSpot: {examinedFile}}},
		{"examination of a present file", false, func(a *Action) {
			a.Examined = append(a.Examined, examinedFile)
			if a.Unexamined != nil {
				a.Unexamined = []string{create}
			}
		}, map[string][]string{
			gapCoverageUnexamined: {create}, gapCoverageAbsent: {examinedFile, create}, gapCoverageBlindSpot: {examinedFile, create}}},
		{"canonical derivation", false, func(a *Action) { a.DerivedCoverage = append(a.DerivedCoverage, lockAnchors(examinedFile)...) }, map[string][]string{
			gapCoverageUnexamined: {create}, gapCoverageAbsent: {create}, gapCoverageBlindSpot: {create}}},
		{"grant and derivation", true, func(a *Action) { a.DerivedCoverage = append(a.DerivedCoverage, lockAnchors(examinedFile)...) }, map[string][]string{
			gapCoverageUnexamined: nil, gapCoverageAbsent: nil, gapCoverageBlindSpot: nil}},
		{"no settling fact", false, func(*Action) {}, map[string][]string{
			gapCoverageUnexamined: {examinedFile, create}, gapCoverageAbsent: {examinedFile, create}, gapCoverageBlindSpot: {examinedFile, create}}},
		// The same VALID recorded unit, but the pinned world's read of the
		// create was not answered: it is neither confirmed absent nor present.
		// A grant settles only a canonically confirmed absent CREATE, and the
		// anchor projected onto it from its covering surface is no derivation
		// over a file whose existence is unknown, so the create stays.
		{"grant without confirmed absence", true, func(a *Action) { a.Absent = nil }, map[string][]string{
			gapCoverageUnexamined: {examinedFile, create}, gapCoverageAbsent: {examinedFile, create}, gapCoverageBlindSpot: {examinedFile, create}}},
	}
	type outcome struct{ key, condition string }
	for _, k := range kinds {
		for _, f := range facts {
			results := map[string]outcome{}
			for _, entry := range []string{"initial", "post-authorization"} {
				t.Run(k.kind+"/"+f.name+"/"+entry, func(t *testing.T) {
					taskID := "task-df30-w14"
					e := df30Engine(t, taskID)
					pinWorld(t, e, taskID, prospectiveWorld)
					d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: planned, ProspectiveSurfaces: decl}
					plain := scopedPreflight(t, k.plain)
					base := Action{Stage: StageCandidateEdit, Files: planned, DerivedCoverage: out,
						Present: []string{existingS, examinedFile}, Absent: []string{create}}
					if k.kind == gapCoverageUnexamined {
						base.Unexamined, base.Examined = []string{examinedFile, create}, []string{existingS}
					}
					prior := routeAuthorityForAction(plain, nil, base)
					if !prior.ClosesGap() || prior.Gap.Kind != k.kind || !sameFiles(prior.Gap.Scope, []string{examinedFile, create}) {
						t.Fatalf("premise: the prior %s gap holds both members: %+v", k.kind, prior)
					}
					prior.Gap.World = prospectiveWorld
					e.registerRouting(taskID, prospectiveWorld, prior, base, plain, d, true)
					ledger := ledgerOf(e, taskID)

					if f.grant {
						df30Record(t, e, taskID, grants)
					}
					act := base
					act.Examined = append([]string(nil), base.Examined...)
					act.DerivedCoverage = append([]CoverageAnchor(nil), base.DerivedCoverage...)
					act.Prospective = e.prospectiveUnitsFor(taskID, d)
					f.apply(&act)

					var routed Routing
					switch entry {
					case "initial":
						fresh := routeAuthorityForAction(plain, nil, act)
						if fresh.Gap.Identified() {
							fresh.Gap.World = prospectiveWorld
						}
						routed, _ = e.registerRouting(taskID, prospectiveWorld, fresh, act, plain, d, true)
					default:
						// The real post-authorization continuation both the
						// architect loop and a supplied plan take: the per-file
						// probe runs against an MCP client answering this
						// case's examination facts, and the routing returned
						// alone decides whether the plan reaches a worker.
						gated := scopedPreflight(t, gate(k.plain))
						human := routeAuthorityForAction(gated, nil, act)
						if !human.RequiresHuman() {
							t.Fatalf("premise: the gate is asked first: %+v", human)
						}
						var err error
						routed, _, _, err = e.afterHumanAuthorization(fixtureCtx(e, taskID), perFileSensei(t, act.Unexamined...), certifiedStart{}, taskID,
							"change the planned files", human, act, gated, d)
						if err != nil {
							t.Fatal(err)
						}
					}

					want := f.want[k.kind]
					// One identity of this kind, however it was narrowed; one
					// plan attempt routes both, so its owner never moves.
					assertEpisodeOfKind(t, e, taskID, prior.Gap, ledger, ledger.owners[prior.Gap.Key()])
					open := openCoverageGaps(e, taskID, prospectiveWorld)
					// The prior identity reached its typed re-evaluation: it is
					// still open in its own form, or a disposition decided it.
					if p := resolutionOf(e, taskID, prior.Gap); !p.Observed {
						if p.Disposition == nil || !sameFiles(p.Disposition.Unresolved, want) {
							t.Fatalf("the prior identity was deactivated without its typed re-evaluation: %+v", p)
						}
					}
					if want == nil {
						if len(open) != 0 || routed.ClosesGap() {
							t.Fatalf("a settled identity was kept open: routed=%+v open=%+v", routed, open)
						}
						results[entry] = outcome{}
						return
					}
					if len(open) != 1 || !sameFiles(open[0].Gap.Scope, want) {
						t.Fatalf("the open identity is not exactly the unresolved members %v: %+v", want, open)
					}
					// An unresolved member prevents execution on EVERY entry
					// route: the continuation is the open identity itself, not
					// the route the answer was about.
					if !routed.ClosesGap() || routed.Gap.Key() != open[0].Gap.Key() || routed.Condition != open[0].Routing.Condition {
						t.Fatalf("an unresolved %s member did not stop the continuation: routed=%+v open=%+v", k.kind, routed, open[0])
					}
					cond := open[0].Routing.Condition
					for _, m := range []string{examinedFile, create} {
						remaining := false
						for _, w := range want {
							remaining = remaining || w == m
						}
						if !remaining && strings.Contains(cond, m) {
							t.Fatalf("the condition still describes settled member %s: %q", m, cond)
						}
						if remaining && !strings.Contains(cond, m) {
							t.Fatalf("the condition omits unresolved member %s: %q", m, cond)
						}
					}
					results[entry] = outcome{key: open[0].Gap.Key(), condition: cond}
				})
			}
			if results["initial"] != results["post-authorization"] {
				t.Errorf("%s/%s: the entry route changed the disposition:\n initial %+v\n post-authorization %+v",
					k.kind, f.name, results["initial"], results["post-authorization"])
			}
		}
	}
}

// W14 -- THE PRODUCTION LIFECYCLE. Every case is driven only through the
// engine's entry points over a pinned Git world, a real derivation record and a
// Sensei MCP; nothing calls the router, a reconciler or the ledger directly.
// Three entries reach the second routing of the same task:
//
//   - ordinary: the architect's proceed route (resolveArchitectureIn ->
//     askArchitect), its closure round and its exhausted-gap disposal, with a
//     scripted provider answering the plan;
//   - supplied: resolveSuppliedPlan routing a plan supplied with the task;
//   - post-authorization: a supplied plan behind a gate answered before it, so
//     coverage is reached only through the human answer's continuation
//     (afterHumanAuthorization).
//
// A first routing, by a first plan attempt, opens the gap over a present
// unexamined file and an absent planned CREATE. The settling fact then arrives
// the way production obtains it -- the re-plan declares the create's
// prospective surface and routing records its grant, the per-file probe
// examines the present file, or the derivation covers it -- and the second
// routing, by a DIFFERENT plan attempt, must decide THAT SAME episode: one
// ledger identity, its key, its position, the resolution count and the attempt
// that owns its question all unchanged, narrowed or closed by the one typed
// disposition, with typed scope, condition and the refusal the run reports all
// naming exactly the members that remain, and the same outcome whichever entry
// route reached it.
func TestDF30W14TheProductionLifecycleDecidesOneEpisode(t *testing.T) {
	create, examinedFile := existingUnix, df30Main
	planned := []string{existingS, examinedFile, create}
	files := existingWorld()
	files[examinedFile] = "package main\n"
	gate := func(body string) string {
		return strings.Replace(body, `"approval_gate":"APPROVAL_GATE_NONE"`, `"approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"`, 1)
	}
	kinds := []struct{ kind, body string }{
		{gapCoverageUnexamined, strings.Replace(strings.Replace(gatedNeighbourCovered, "APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED", "APPROVAL_GATE_NONE", 1),
			`"file_count":2`, `"file_count":3`, 1)},
		{gapCoverageAbsent, regionAbsent},
		{gapCoverageBlindSpot, regionBlindSpot},
	}
	facts := []struct {
		name                    string
		grant, examine, derived bool
		want                    map[string][]string
	}{
		{"confirmed prospective grant", true, false, false, map[string][]string{
			gapCoverageUnexamined: {examinedFile}, gapCoverageAbsent: {examinedFile}, gapCoverageBlindSpot: {examinedFile}}},
		{"examination of a present file", false, true, false, map[string][]string{
			gapCoverageUnexamined: {create}, gapCoverageAbsent: {examinedFile, create}, gapCoverageBlindSpot: {examinedFile, create}}},
		{"canonical derivation", false, false, true, map[string][]string{
			gapCoverageUnexamined: {create}, gapCoverageAbsent: {create}, gapCoverageBlindSpot: {create}}},
		{"grant and derivation", true, false, true, map[string][]string{
			gapCoverageUnexamined: nil, gapCoverageAbsent: nil, gapCoverageBlindSpot: nil}},
		{"no settling fact", false, false, false, map[string][]string{
			gapCoverageUnexamined: {examinedFile, create}, gapCoverageAbsent: {examinedFile, create}, gapCoverageBlindSpot: {examinedFile, create}}},
	}
	const taskID, task = "task-df30-w14", "change the planned files"
	entries := []string{"ordinary", "supplied", "post-authorization"}
	type outcome struct{ admitted, scope, condition string }
	for _, k := range kinds {
		for _, f := range facts {
			results := map[string]outcome{}
			for _, entry := range entries {
				t.Run(k.kind+"/"+f.name+"/"+entry, func(t *testing.T) {
					w := newDF30Production(t, taskID, files)
					sc := df30Sensei(t, w.state)
					e := w.e
					if err := seedAppend(t, e.Store, event.New("s1", taskID, event.SourceSystem, event.TaskCreated, task, nil)); err != nil {
						t.Fatal(err)
					}
					w.region(t, k.body)
					w.probes([]string{existingS}, []string{examinedFile, create})
					w.derives(t, existingS, existingSibling)

					// OBSERVE: the first routing, by the first plan attempt, opens
					// the gap over both members.
					d0 := architectureDecision{Decision: "proceed", Mode: ModeModify, Plan: "the plan as first routed", Files: planned}
					_, err := e.resolveSuppliedPlan(fixtureCtx(e, taskID), sc, certifiedStart{}, taskID, task, SuppliedPlan{decision: d0, Digest: "d0"})
					opened := openCoverageGaps(e, taskID, w.world)
					if err == nil || len(opened) != 1 || opened[0].Gap.Kind != k.kind || !sameFiles(opened[0].Gap.Scope, []string{examinedFile, create}) {
						t.Fatalf("premise: the first routing refuses over one open %s gap holding both members: err=%v open=%+v", k.kind, err, opened)
					}
					episode, ledger := opened[0].Gap, ledgerOf(e, taskID)
					first := e.pendingPlanAttempt(taskID).ID
					if first == "" || ledger.owners[episode.Key()] != first {
						t.Fatalf("premise: the first attempt %q owns the episode's question: %+v", first, ledger.owners)
					}
					resolutions := len(e.gapResolutions(taskID).byKey)

					// The settling fact arrives through production inputs only.
					d1 := d0
					d1.Plan = "the plan after the closure round"
					if f.grant {
						d1.ProspectiveSurfaces = existingDeclarations()[:1]
					}
					if f.examine {
						w.probes([]string{existingS, examinedFile}, []string{create})
					}
					if f.derived {
						w.derives(t, existingS, existingSibling, examinedFile)
					}
					var admitted architectureDecision
					switch entry {
					case "ordinary":
						df30Architect(t, e, d1)
						admitted, err = e.resolveArchitectureIn(fixtureCtx(e, taskID), sc, certifiedStart{}, taskID, task, "plan the change", e.Repo.Root)
					case "supplied":
						admitted, err = e.resolveSuppliedPlan(fixtureCtx(e, taskID), sc, certifiedStart{}, taskID, task, SuppliedPlan{decision: d1, Digest: "d1"})
					default:
						// The second routing meets a gate, answered before it,
						// so it reaches coverage only through the human answer's
						// continuation (afterHumanAuthorization).
						body := gate(k.body)
						human := routeAuthorityForAction(scopedPreflight(t, body), nil, Action{Stage: StageCandidateEdit, Files: planned})
						if !human.RequiresHuman() {
							t.Fatalf("premise: the gate is asked first: %+v", human)
						}
						answer := authority.Resolution{TaskID: taskID, SessionID: "s1", Question: "May this change proceed?",
							Condition: human.Condition, OptionID: "1", OptionLabel: "Authorize the architectural change described above",
							Scope: planned, Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC()}
						if err := seedAppend(t, e.Store, event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, answer.OptionLabel, answer)); err != nil {
							t.Fatal(err)
						}
						w.region(t, body)
						admitted, err = e.resolveSuppliedPlan(fixtureCtx(e, taskID), sc, certifiedStart{}, taskID, task, SuppliedPlan{decision: d1, Digest: "d1"})
					}
					if second := e.pendingPlanAttempt(taskID).ID; second == "" || second == first {
						t.Fatalf("premise: the second routing is a different plan attempt: first %q second %q", first, second)
					}

					// RE-EVALUATE: one identity, decided as itself, its key,
					// and position unchanged across the attempts, its live
					// question owned by the attempt now routing it.
					// After authorization a region episode may stand beside an
					// unexamined question of ANOTHER kind; nothing else is added.
					want := f.want[k.kind]
					current := assertEpisodeOfKind(t, e, taskID, episode, ledger, liveOwner(t, e, taskID, episode, first))
					added := len(e.gapResolutions(taskID).byKey) - resolutions
					if added != 0 && !(entry == "post-authorization" && k.kind != gapCoverageUnexamined && added == 1) {
						t.Fatalf("the re-evaluation added %d resolutions", added)
					}
					if !sameFiles(want, episode.Scope) && (current.Disposition == nil || !sameFiles(current.Disposition.Unresolved, want)) {
						t.Fatalf("the episode was not decided by its typed re-evaluation: %+v", current.Disposition)
					}
					if want == nil {
						// A lawfully settled identity closes and the run goes on.
						if open := openCoverageGaps(e, taskID, w.world); err != nil || admitted.Plan != d1.Plan || len(open) != 0 || current.Observed {
							t.Fatalf("a settled episode was kept open or the plan refused: err=%v open=%+v", err, open)
						}
						results[entry] = outcome{admitted: d1.Plan}
						return
					}
					// An unresolved member stays open as the episode, with typed
					// scope and condition naming exactly the members that remain.
					if !current.Open() || !sameFiles(current.Gap.Scope, want) || current.Routing.Gap.Key() != episode.Key() {
						t.Fatalf("the episode is not open over exactly %v: %+v", want, current)
					}
					cond := current.Routing.Condition
					for _, m := range []string{examinedFile, create} {
						if remaining := strings.Contains(strings.Join(want, "\n"), m); remaining != strings.Contains(cond, m) {
							t.Fatalf("scope %v and condition disagree about %s: %q", want, m, cond)
						}
					}
					// And it stops the run on every entry route. The refusal
					// describes no settled member; when it is the episode's own
					// question it is the re-rendered condition itself.
					if err == nil {
						t.Fatalf("an unresolved %s member did not stop the run: plan=%q", k.kind, admitted.Plan)
					}
					for _, m := range []string{examinedFile, create} {
						if !strings.Contains(strings.Join(want, "\n"), m) && strings.Contains(err.Error(), m) {
							t.Fatalf("the refusal describes settled member %s: %v", m, err)
						}
					}
					if entry == "ordinary" {
						// The architect's route spends the episode's closure round
						// and then disposes of it: a knowledge limit over exactly
						// the stored disposition's unresolved members.
						var limit *knowledgeLimitError
						if !errors.As(err, &limit) || !sameFiles(limit.Missing, current.Disposition.Unresolved) || limit.Condition != cond {
							t.Fatalf("the ordinary route did not end on the episode's typed disposition: %v", err)
						}
					} else if !strings.Contains(err.Error(), "a bounded knowledge gap must be closed first") {
						t.Fatalf("an unresolved %s member did not stop the supplied plan: %v", k.kind, err)
					}
					if entry != "post-authorization" || k.kind == gapCoverageUnexamined {
						if !strings.Contains(err.Error(), cond) {
							t.Fatalf("the run's refusal does not carry the episode's re-rendered condition %q: %v", cond, err)
						}
					}
					results[entry] = outcome{scope: strings.Join(want, ","), condition: cond}
				})
			}
			for _, entry := range entries[1:] {
				if results[entry] != results[entries[0]] {
					t.Errorf("%s/%s: the entry route changed the disposition:\n %s %+v\n %s %+v",
						k.kind, f.name, entries[0], results[entries[0]], entry, results[entry])
				}
			}
		}
	}
}

// W14 -- A GATED FIRST OBSERVATION IS DECIDED BY THE SAME DISPOSITION
// (DF-30A review f1). No earlier routing opened any episode: the FIRST region
// answer the run sees is gated, so the router stops at the gate and asks no
// coverage question, and a recorded human answer authorizes the consequence.
// The post-authorization continuation (afterHumanAuthorization) must then ask
// the region's coverage question itself, through the same typed disposition
// as ordinary routing. A present member the per-file probe examined but no
// derivation covers is NOT settled -- examination does not own
// coverage-absent or coverage-blind-spot -- so the gap opens over exactly it
// and the plan stops; when a derivation covers every member the gap is
// lawfully closed and the plan proceeds. The ungated ordinary route reaches the
// same disposition.
func TestDF30W14AGatedFirstObservationDecidesItsRegionGap(t *testing.T) {
	examinedFile := df30Main
	planned := []string{existingS, examinedFile}
	files := existingWorld()
	files[examinedFile] = "package main\n"
	gate := func(body string) string {
		return strings.Replace(body, `"approval_gate":"APPROVAL_GATE_NONE"`, `"approval_gate":"APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"`, 1)
	}
	const taskID, task = "task-df30-w14-gated", "change the planned files"
	// The region answers describe this two-file plan.
	twoFiles := func(body string) string {
		return strings.ReplaceAll(strings.Replace(body, `"file_count":3`, `"file_count":2`, 1), `"indexed_file_count":3`, `"indexed_file_count":2`)
	}
	for _, k := range []struct{ kind, body string }{{gapCoverageAbsent, twoFiles(regionAbsent)}, {gapCoverageBlindSpot, twoFiles(regionBlindSpot)}} {
		for _, c := range []struct {
			name    string
			derived []string
			want    []string
		}{
			{"examined but not derived", []string{existingS}, []string{examinedFile}},
			{"derived over every member", []string{existingS, examinedFile}, nil},
		} {
			results := map[string]string{}
			for _, entry := range []string{"ordinary", "post-authorization"} {
				t.Run(k.kind+"/"+c.name+"/"+entry, func(t *testing.T) {
					w := newDF30Production(t, taskID, files)
					sc := df30Sensei(t, w.state)
					e := w.e
					if err := seedAppend(t, e.Store, event.New("s1", taskID, event.SourceSystem, event.TaskCreated, task, nil)); err != nil {
						t.Fatal(err)
					}
					// Every planned file is present and examined.
					w.probes(planned, nil)
					w.derives(t, c.derived...)
					body := k.body
					if entry == "post-authorization" {
						body = gate(k.body)
						human := routeAuthorityForAction(scopedPreflight(t, body), nil, Action{Stage: StageCandidateEdit, Files: planned})
						if !human.RequiresHuman() {
							t.Fatalf("premise: the gate is asked first: %+v", human)
						}
						answer := authority.Resolution{TaskID: taskID, SessionID: "s1", Question: "May this change proceed?",
							Condition: human.Condition, OptionID: "1", OptionLabel: "Authorize the architectural change described above",
							Scope: planned, Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC()}
						if err := seedAppend(t, e.Store, event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, answer.OptionLabel, answer)); err != nil {
							t.Fatal(err)
						}
					}
					w.region(t, body)
					d := architectureDecision{Decision: "proceed", Mode: ModeModify, Plan: "the plan", Files: planned}
					admitted, err := e.resolveSuppliedPlan(fixtureCtx(e, taskID), sc, certifiedStart{}, taskID, task, SuppliedPlan{decision: d, Digest: "d"})
					open := openCoverageGaps(e, taskID, w.world)
					if c.want == nil {
						if err != nil || admitted.Plan != d.Plan || len(open) != 0 {
							t.Fatalf("a lawfully settled %s gap stopped the plan: err=%v open=%+v", k.kind, err, open)
						}
						results[entry] = "admitted"
						return
					}
					if err == nil || !strings.Contains(err.Error(), "a bounded knowledge gap must be closed first") {
						t.Fatalf("an examined, underived member of a %s gap did not stop the plan: plan=%q err=%v", k.kind, admitted.Plan, err)
					}
					if len(open) != 1 || open[0].Gap.Kind != k.kind || !sameFiles(open[0].Gap.Scope, c.want) ||
						open[0].Disposition == nil || !sameFiles(open[0].Disposition.Unresolved, c.want) {
						t.Fatalf("the %s gap is not one recorded episode open over exactly %v: %+v", k.kind, c.want, open)
					}
					cond := open[0].Routing.Condition
					if !strings.Contains(cond, examinedFile) || strings.Contains(cond, existingS) || !strings.Contains(err.Error(), cond) {
						t.Fatalf("the refusal does not carry the episode's condition over exactly %v: %q / %v", c.want, cond, err)
					}
					results[entry] = strings.Join(open[0].Disposition.Unresolved, ",") + "\n" + cond
				})
			}
			if results["ordinary"] != results["post-authorization"] {
				t.Errorf("%s/%s: the entry route changed the disposition:\n%q\n%q", k.kind, c.name, results["ordinary"], results["post-authorization"])
			}
		}
	}
}

// df30Architect configures e's one architect as a provider scripted to answer
// d on every turn -- the proceed the ordinary route (resolveArchitectureIn ->
// askArchitect) routes and the plan it re-sends after a closure round alike.
func df30Architect(t *testing.T, e *Engine, d architectureDecision) {
	t.Helper()
	answer, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	script := make([]architectTurn, 8)
	for i := range script {
		script[i] = architectTurn{text: string(answer)}
	}
	e.Runners = &fixedResolver{runner: &scriptedArchitect{turns: script}, name: "claude"}
	e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "claude", "true", "none"
}

// ---------------------------------------------------------------------------
// One episode, one question (DF-30 cycle 4, ruling 176 and the r10
// counterexample "one immutable resolution episode must not be recomputed
// under a different semantic owner"). A coverage-blind-spot episode opened by
// a QUALIFIED blind spot -- one naming the question the graph cannot answer --
// is re-evaluated by a later routing whose preflight is unqualified or clean.
// The later routing supplies the facts; the episode keeps its question: the
// requirement its members are decided against and the evidence its condition
// quotes.
// ---------------------------------------------------------------------------

// df30QualifiedSpot is a coverage blind spot naming the question it cannot
// answer: lock discipline.
const df30QualifiedSpot = "coverage_insufficient: no lock discipline established"

// df30Region is a covered two-file region answer reporting spot, or no blind
// spot at all when spot is empty, its gate answered as gate.
func df30Region(spot, gate string) string {
	spots := ""
	if spot != "" {
		spots = `"blind_spots":["` + spot + `"],`
	}
	return `{"status":"PREFLIGHT_STATUS_OK",` +
		`"coverage":{"sufficient":true,"direct_anchor_count":3,"file_count":2,"indexed_file_count":2},` + spots +
		`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"` + gate + `"},` +
		identifiedAuthority + `}`
}

// confinementAnchors is derived coverage from a RECOGNISED family that answers
// another question than lock discipline: true, and the wrong family.
func confinementAnchors(files ...string) []CoverageAnchor {
	var out []CoverageAnchor
	for _, f := range files {
		out = append(out, CoverageAnchor{File: f, Requirement: RequirementInvocationConfinement,
			Describe: "command_invocation_confined_to(sensei under internal/sensei)"})
	}
	return out
}

const (
	df30Locked = "internal/workflow/zz_df30_locked.go"
	df30Other  = "internal/workflow/zz_df30_other.go"
)

// df30QualifiedOutcome is what one re-evaluation of the qualified episode
// left: whether it is open, over which members, saying what.
type df30QualifiedOutcome struct {
	open      bool
	scope     string
	condition string
}

// df30ReEvaluateQualified opens the qualified episode through the production
// sequence, then re-evaluates it in the same live process through entry ("ordinary": registerRouting over
// the router's answer; "post-authorization": afterHumanAuthorization's real
// continuation behind an answered gate) under the region answer later (a
// df30Region spot) with anchors as the derived coverage. It returns the engine
// that re-evaluated, the episode as opened, the routing the entry returned and
// the action it was decided on.
func df30ReEvaluateQualified(t *testing.T, taskID, entry, later string, anchors []CoverageAnchor) (*Engine, Routing, Routing, Action) {
	t.Helper()
	planned := []string{df30Locked, df30Other}
	d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: planned}
	base := Action{Stage: StageCandidateEdit, Files: planned, Present: planned, Examined: planned}

	e := df30Engine(t, taskID)
	pinWorld(t, e, taskID, prospectiveWorld)
	qualified := scopedPreflight(t, df30Region(df30QualifiedSpot, "APPROVAL_GATE_NONE"))
	opened := routeAuthorityForAction(qualified, nil, base)
	if !opened.ClosesGap() || opened.Gap.Kind != gapCoverageBlindSpot || !sameFiles(opened.Gap.Scope, planned) ||
		opened.Gap.Semantics == nil || opened.Gap.Semantics.Requirement != RequirementLockDiscipline {
		t.Fatalf("premise: a qualified blind-spot episode opens over both files: %+v", opened)
	}
	opened.Gap.World = prospectiveWorld
	if _, r := e.registerRouting(taskID, prospectiveWorld, opened, base, qualified, d, true); !r.Open() {
		t.Fatalf("premise: the episode is open: %+v", r)
	}
	act := base
	act.DerivedCoverage = anchors
	routed, act := df30Enter(t, e, taskID, entry, later, act, d)
	return e, opened, routed, act
}

// df30Enter re-evaluates the task's episodes through entry ("ordinary":
// registerRouting over the router's answer; "post-authorization":
// afterHumanAuthorization's real continuation behind an answered gate) under
// the region answer later (a df30Region spot), deciding on act.
func df30Enter(t *testing.T, e *Engine, taskID, entry, later string, act Action, d architectureDecision) (Routing, Action) {
	t.Helper()
	var routed Routing
	switch entry {
	case "ordinary":
		scoped := scopedPreflight(t, df30Region(later, "APPROVAL_GATE_NONE"))
		fresh := routeAuthorityForAction(scoped, nil, act)
		if fresh.Gap.Identified() {
			fresh.Gap.World = prospectiveWorld
		}
		routed, _ = e.registerRouting(taskID, prospectiveWorld, fresh, act, scoped, d, true)
	default:
		gated := scopedPreflight(t, df30Region(later, "APPROVAL_GATE_HUMAN_APPROVAL_REQUIRED"))
		human := routeAuthorityForAction(gated, nil, act)
		if !human.RequiresHuman() {
			t.Fatalf("premise: the gate is asked first: %+v", human)
		}
		var err error
		routed, act, _, err = e.afterHumanAuthorization(fixtureCtx(e, taskID), perFileSensei(t), certifiedStart{}, taskID,
			"change the planned files", human, act, gated, d)
		if err != nil {
			t.Fatal(err)
		}
	}
	return routed, act
}

// df30QualifiedLaters are the later region answers: one reporting an
// UNQUALIFIED coverage blind spot -- under which any recognised family would
// settle a member, and whose own diagnostic differs -- and a clean one.
var df30QualifiedLaters = map[string]string{
	"unqualified": "coverage_insufficient: no direct anchors",
	"clean":       "",
}

// df30AssertQualified checks one re-evaluation of the qualified episode
// against want (nil: closed) and returns its outcome. The episode is the one
// identity it opened as, still asking the question it opened with; when it is
// open its scope, condition and the routing returned all name exactly want,
// quote the episode's own blind spot and none of the later preflight's
// diagnostics, and the exhausted-gap disposal consumes that recorded
// disposition rather than the condition handed to it.
func df30AssertQualified(t *testing.T, e *Engine, taskID, laterSpot string, opened, routed Routing, act Action, want []string) df30QualifiedOutcome {
	t.Helper()
	current := resolutionOf(e, taskID, opened.Gap)
	if len(e.gapResolutions(taskID).order) != 1 || !sameSemantics(current.Gap.Semantics, opened.Gap.Semantics) {
		t.Fatalf("the re-evaluation changed the episode's identity or its question: order %q semantics %+v",
			e.gapResolutions(taskID).order, current.Gap.Semantics)
	}
	open := openCoverageGaps(e, taskID, prospectiveWorld)
	if want == nil {
		if len(open) != 0 || routed.ClosesGap() || current.Disposition == nil || current.Disposition.Open() ||
			current.Disposition.Settled[df30Locked] != settledByDerivation || current.Disposition.Settled[df30Other] != settledByDerivation {
			t.Fatalf("the matching derivation did not close the episode: routed=%+v disposition=%+v", routed, current.Disposition)
		}
		return df30QualifiedOutcome{}
	}
	if len(open) != 1 || open[0].Gap.Key() != opened.Gap.Key() || !sameFiles(open[0].Gap.Scope, want) ||
		current.Disposition == nil || !sameFiles(current.Disposition.Unresolved, want) {
		t.Fatalf("the episode is not open over exactly %v: open=%+v disposition=%+v", want, open, current.Disposition)
	}
	// The continuation is the episode itself, in its current form: the gap
	// the live routing re-rendered.
	if routed.Granted() || routed.Route != current.Routing.Route || routed.Gap.Key() != opened.Gap.Key() || !sameFiles(routed.Gap.Scope, want) {
		t.Fatalf("the open episode did not stop the continuation over exactly %v: %+v", want, routed)
	}
	cond := current.Routing.Condition
	if !strings.Contains(cond, df30QualifiedSpot) || (laterSpot != "" && strings.Contains(cond, laterSpot)) {
		t.Fatalf("the condition is not the episode's own question: %q", cond)
	}
	if narrowed := !sameFiles(want, opened.Gap.Scope); narrowed {
		for _, m := range []string{df30Locked, df30Other} {
			if strings.Contains(strings.Join(want, "\n"), m) != strings.Contains(cond, m) {
				t.Fatalf("scope %v and condition disagree about %s: %q", want, m, cond)
			}
		}
		if routed.Condition != cond || escalationCondition(routed) != escalationCondition(current.Routing) {
			t.Fatalf("the routing returned does not carry the re-rendered condition:\n%q\n%q", routed.Condition, cond)
		}
	} else if cond != opened.Condition {
		t.Fatalf("an episode nothing settled was re-described: %q, opened as %q", cond, opened.Condition)
	}
	_, err := e.disposeExhaustedGap(taskID, "github.com/globulario/sensei-code",
		Routing{Route: RouteCloseGap, Gap: current.Gap, Condition: "a later preflight's condition"}, act)
	var limit *knowledgeLimitError
	if !errors.As(err, &limit) || !sameFiles(limit.Missing, want) || limit.Condition != cond {
		t.Fatalf("exhaustion did not consume the episode's recorded disposition: %v", err)
	}
	return df30QualifiedOutcome{open: true, scope: strings.Join(want, ","), condition: cond}
}

// df30RunQualified runs one fact through every later preflight and entry
// route of one live process and requires one outcome across all of them.
func df30RunQualified(t *testing.T, taskID string, anchors []CoverageAnchor, want []string) {
	t.Helper()
	results := map[string]df30QualifiedOutcome{}
	for later, spot := range df30QualifiedLaters {
		for _, entry := range []string{"ordinary", "post-authorization"} {
			name := later + "/" + entry
			t.Run(name, func(t *testing.T) {
				e, opened, routed, act := df30ReEvaluateQualified(t, taskID, entry, spot, anchors)
				results[name] = df30AssertQualified(t, e, taskID, spot, opened, routed, act, want)
			})
		}
	}
	var first string
	for name, got := range results {
		if first == "" {
			first = name
			continue
		}
		if got != results[first] {
			t.Errorf("the later preflight or entry route changed the outcome:\n %s %+v\n %s %+v", first, results[first], name, got)
		}
	}
}

// W13 -- A NARROWED EPISODE IS RE-RENDERED FROM ITS OWN QUESTION. A lock
// derivation settles one member of the qualified episode; the other is left
// to no derivation, or to one of the wrong family. Under the unqualified
// preflight the router itself reports the remaining member as its own gap,
// rendered with ITS diagnostic; under the clean one it reports nothing. Either
// way the episode narrows in place to the remaining member, and its scope, its
// condition and every question rendered from it name exactly that member and
// quote the episode's blind spot -- never the later preflight's diagnostic --
// on every entry route.
func TestDF30W13ANarrowedQualifiedEpisodeKeepsItsCondition(t *testing.T) {
	for name, anchors := range map[string][]CoverageAnchor{
		"matching derivation over one member":            lockAnchors(df30Locked),
		"matching over one, wrong family over the other": append(lockAnchors(df30Locked), confinementAnchors(df30Other)...),
	} {
		t.Run(name, func(t *testing.T) {
			df30RunQualified(t, "task-df30-w13-qualified", anchors, []string{df30Other})
		})
	}
}

// W14 -- AN EPISODE IS DECIDED BY ITS OWN REQUIREMENT. A recognised derivation
// of the wrong family over every member settles nothing, though the later
// unqualified preflight's own question would accept it and the clean one asks
// none; the episode stays open, as it opened, through the production order on
// every entry route. A lock derivation over every member
// answers the episode's question and closes it, and the run continues.
func TestDF30W14AQualifiedEpisodeIsDecidedByItsOwnRequirement(t *testing.T) {
	t.Run("wrong family over every member", func(t *testing.T) {
		df30RunQualified(t, "task-df30-w14-qualified", confinementAnchors(df30Locked, df30Other), []string{df30Locked, df30Other})
	})
	t.Run("matching derivation over every member", func(t *testing.T) {
		df30RunQualified(t, "task-df30-w14-qualified", lockAnchors(df30Locked, df30Other), nil)
	})
}

// df30ConfinementSpot is a coverage blind spot naming a DIFFERENT question than
// df30QualifiedSpot: invocation confinement.
const df30ConfinementSpot = "coverage_insufficient: invocation sites unknown"

// W14 -- A DISTINCT QUALIFIED QUESTION IS NOT FOLDED INTO THE EPISODE (review
// f1; DF-30A review f3). The lock-discipline episode is re-entered, on every
// entry route, under a later preflight that asks the invocation-confinement
// question over the same files. The episode is decided by its stored lock
// requirement alone: lock anchors over every member close it although the later
// question still reports every file, and confinement anchors -- which answer the
// later question -- leave it open over both members, asking its own question.
// The later question is neither folded into the episode nor lost, and it is
// not acted upon outside the ledger either: it is its own canonical episode,
// under its own identity, with its own recorded disposition, and the plan
// stops on it on every entry route -- a human answer to the gate the
// post-authorization route met settles no coverage question.
func TestDF30W14ADistinctQualifiedQuestionIsNotFoldedIntoTheEpisode(t *testing.T) {
	const taskID = "task-df30-w14-distinct"
	planned := []string{df30Locked, df30Other}
	for _, entry := range []string{"ordinary", "post-authorization"} {
		name := entry
		t.Run(name+"/lock anchors answer the episode only", func(t *testing.T) {
			e, opened, routed, _ := df30ReEvaluateQualified(t, taskID, entry, df30ConfinementSpot, lockAnchors(planned...))
			current := resolutionOf(e, taskID, opened.Gap)
			if current.Gap.Key() != opened.Gap.Key() || !sameSemantics(current.Gap.Semantics, opened.Gap.Semantics) {
				t.Fatalf("the later question changed the episode's identity or question: %+v", current.Gap)
			}
			if current.Disposition == nil || current.Disposition.Open() ||
				current.Disposition.Settled[df30Locked] != settledByDerivation || current.Disposition.Settled[df30Other] != settledByDerivation {
				t.Fatalf("the episode was not decided by its own lock requirement: disposition %+v", current.Disposition)
			}
			if routed.Granted() || !routed.ClosesGap() || routed.Gap.Semantics == nil ||
				routed.Gap.Semantics.Requirement != RequirementInvocationConfinement ||
				!strings.Contains(routed.Condition, df30ConfinementSpot) || !sameFiles(normalizeEpisodeScope(routed.Gap.Scope), normalizeEpisodeScope(planned)) {
				t.Fatalf("the later, distinct question was folded or lost: %+v", routed)
			}
			// Its own canonical identity: never the episode's key, recorded
			// after it, and the only identity open.
			if routed.Gap.Key() == opened.Gap.Key() || routed.Gap.Question != RequirementInvocationConfinement {
				t.Fatalf("the distinct question shares the episode's identity: %q", routed.Gap.Key())
			}
			if order := ledgerOf(e, taskID).order; len(order) != 2 || order[0] != opened.Gap.Key() || order[1] != routed.Gap.Key() {
				t.Fatalf("the ledger does not hold exactly the episode and the distinct question: %q", order)
			}
			if open := openCoverageGaps(e, taskID, prospectiveWorld); len(open) != 1 || open[0].Gap.Key() != routed.Gap.Key() {
				t.Fatalf("the open identities are not exactly the distinct question: %+v", open)
			}
			if authorized, settled := e.gapSettlement(taskID, routed); authorized || settled {
				t.Fatal("the episode's state answered the later question")
			}
			cur, registered := e.currentResolution(taskID, routed.Gap)
			if !registered || !cur.Open() || cur.Gap.Key() != routed.Gap.Key() || !sameSemantics(cur.Gap.Semantics, routed.Gap.Semantics) ||
				cur.Disposition == nil || !sameFiles(normalizeEpisodeScope(cur.Disposition.Unresolved), normalizeEpisodeScope(planned)) {
				t.Fatalf("the later question is not its own recorded episode: registered=%v %+v", registered, cur)
			}
		})
		t.Run(name+"/confinement anchors leave the episode open", func(t *testing.T) {
			e, opened, routed, _ := df30ReEvaluateQualified(t, taskID, entry, df30ConfinementSpot, confinementAnchors(planned...))
			current := resolutionOf(e, taskID, opened.Gap)
			open := openCoverageGaps(e, taskID, prospectiveWorld)
			if len(open) != 1 || open[0].Gap.Key() != opened.Gap.Key() || !sameFiles(normalizeEpisodeScope(current.Gap.Scope), normalizeEpisodeScope(planned)) ||
				!sameSemantics(current.Gap.Semantics, opened.Gap.Semantics) {
				t.Fatalf("the episode was decided by the later question: open %+v", open)
			}
			if routed.Granted() || routed.Gap.Key() != opened.Gap.Key() ||
				!strings.Contains(current.Routing.Condition, df30QualifiedSpot) || strings.Contains(current.Routing.Condition, df30ConfinementSpot) {
				t.Fatalf("the open episode did not stop the plan on its own question: routed %+v condition %q", routed, current.Routing.Condition)
			}
		})
	}
}

// W14 -- TWO DISTINCT QUALIFIED QUESTIONS ARE TWO CANONICAL EPISODES
// (DF-30A review f3). The lock-discipline episode and a later
// invocation-confinement question over the SAME paths are both recorded in the
// task's ledger, each under its own identity and decided by its own
// requirement, on every entry route. Facts that answer one never decide the
// other: confinement anchors over one member narrow the confinement episode
// alone, under its own key, while the lock episode keeps both members.
// Exhaustion of each consumes its own recorded disposition, and an answer about
// one settles nothing about the other.
func TestDF30W14TwoDistinctQualifiedQuestionsKeepTheirOwnEpisodes(t *testing.T) {
	const taskID = "task-df30-w14-two-questions"
	planned := []string{df30Locked, df30Other}
	d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: planned}
	results := map[string]string{}
	for _, entry := range []string{"ordinary", "post-authorization"} {
		t.Run(entry, func(t *testing.T) {
			// Nothing answers either question: both stand over both paths.
			e, opened, asked, act := df30ReEvaluateQualified(t, taskID, entry, df30ConfinementSpot, nil)
			if !asked.ClosesGap() || asked.Gap.Key() == opened.Gap.Key() || asked.Gap.Semantics == nil ||
				asked.Gap.Semantics.Requirement != RequirementInvocationConfinement {
				t.Fatalf("premise: the confinement question is routed as its own identity: %+v", asked)
			}
			lock, confine := opened.Gap, asked.Gap
			if order := ledgerOf(e, taskID).order; len(order) != 2 || order[0] != lock.Key() || order[1] != confine.Key() {
				t.Fatalf("both questions are not canonical ledger entries: %q", order)
			}
			if open := openCoverageGaps(e, taskID, prospectiveWorld); len(open) != 2 {
				t.Fatalf("both questions are not open: %+v", open)
			}

			// Confinement anchors over df30Other answer the confinement
			// question for that member only, and nothing of the lock one.
			act.DerivedCoverage = confinementAnchors(df30Other)
			routed, act := df30Enter(t, e, taskID, entry, df30ConfinementSpot, act, d)
			if order := ledgerOf(e, taskID).order; len(order) != 2 || order[0] != lock.Key() || order[1] != confine.Key() {
				t.Fatalf("narrowing one question changed the ledger's identities: %q", order)
			}
			l, c := resolutionOf(e, taskID, lock), resolutionOf(e, taskID, confine)
			if !l.Open() || l.Disposition == nil || !sameFiles(normalizeEpisodeScope(l.Disposition.Unresolved), normalizeEpisodeScope(planned)) ||
				!sameSemantics(l.Gap.Semantics, lock.Semantics) || !strings.Contains(l.Routing.Condition, df30QualifiedSpot) {
				t.Fatalf("the confinement facts decided the lock episode: %+v %+v", l.Gap, l.Disposition)
			}
			if !c.Open() || c.Gap.Key() != confine.Key() || c.Disposition == nil || !sameFiles(c.Disposition.Unresolved, []string{df30Locked}) ||
				!sameFiles(c.Gap.Scope, []string{df30Locked}) || !strings.Contains(c.Routing.Condition, df30ConfinementSpot) ||
				strings.Contains(c.Routing.Condition, df30Other) || !sameSemantics(c.Gap.Semantics, confine.Semantics) {
				t.Fatalf("the confinement episode did not narrow in place by its own requirement: %+v %+v", c.Gap, c.Disposition)
			}
			if routed.Granted() || !routed.ClosesGap() {
				t.Fatalf("an open question did not stop the continuation: %+v", routed)
			}

			// Exhaustion consumes each one's own recorded disposition.
			for _, g := range []struct {
				r    AuthorityResolution
				want []string
			}{{l, planned}, {c, []string{df30Locked}}} {
				_, err := e.disposeExhaustedGap(taskID, "github.com/globulario/sensei-code",
					Routing{Route: RouteCloseGap, Gap: g.r.Gap, Condition: "a later preflight's condition"}, act)
				var limit *knowledgeLimitError
				if !errors.As(err, &limit) || !sameFiles(normalizeEpisodeScope(limit.Missing), normalizeEpisodeScope(g.want)) || limit.Condition != g.r.Routing.Condition {
					t.Fatalf("exhaustion of %s did not consume its own disposition: %v", g.r.Gap.Key(), err)
				}
			}

			// An answer about one settles nothing about the other.
			e.settleGap(taskID, c.Gap, authority.Authorize)
			if authorized, settled := e.gapSettlement(taskID, Routing{Route: RouteCloseGap, Gap: c.Gap}); !authorized || !settled {
				t.Fatalf("premise: the answer settles the confinement episode: %v %v", authorized, settled)
			}
			if authorized, settled := e.gapSettlement(taskID, Routing{Route: RouteCloseGap, Gap: l.Gap}); authorized || settled {
				t.Fatal("an answer about the confinement question settled the lock episode")
			}
			results[entry] = l.Routing.Condition + "\n" + c.Routing.Condition
		})
	}
	if results["ordinary"] != results["post-authorization"] {
		t.Errorf("the entry route changed the dispositions:\n%q\n%q", results["ordinary"], results["post-authorization"])
	}
}

// W14 -- A MEMBER THE EPISODE ONCE HELD RETURNS TO IT (DF-30A review f2). The
// qualified episode opens over A alone, widens to A+B, and narrows back to A
// when a lock derivation settles B. That derivation then disappears while one
// over A appears, so the routing reports B ALONE -- no file of the episode's
// opening or of its current scope. B is still the episode's member: on every
// entry route the routing is bound to the original key, which reopens over B,
// and no second identity is opened for it.
func TestDF30W14AHistoricallyHeldMemberReturnsToItsEpisode(t *testing.T) {
	a, b := df30Locked, df30Other
	both := []string{a, b}
	for _, entry := range []string{"ordinary", "post-authorization"} {
		t.Run(entry, func(t *testing.T) {
			const taskID = "task-df30-w14-held"
			e := df30Engine(t, taskID)
			pinWorld(t, e, taskID, prospectiveWorld)
			only := Action{Stage: StageCandidateEdit, Files: []string{a}, Present: []string{a}, Examined: []string{a}}
			df30Enter(t, e, taskID, "ordinary", df30QualifiedSpot, only, architectureDecision{Decision: "proceed", Mode: ModeModify, Files: []string{a}})
			open := openCoverageGaps(e, taskID, prospectiveWorld)
			if len(open) != 1 || !sameFiles(open[0].Gap.Scope, []string{a}) {
				t.Fatalf("premise: the episode opens over A alone: %+v", open)
			}
			key := open[0].Gap.Key()
			d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: both}
			plan := Action{Stage: StageCandidateEdit, Files: both, Present: both, Examined: both}
			step := func(anchors []CoverageAnchor, want []string) Routing {
				t.Helper()
				act := plan
				act.DerivedCoverage = anchors
				routed, _ := df30Enter(t, e, taskID, entry, df30QualifiedSpot, act, d)
				current := resolutionOf(e, taskID, open[0].Gap)
				if order := ledgerOf(e, taskID).order; len(order) != 1 || order[0] != key {
					t.Fatalf("a second identity was opened beside the episode %q: %q", key, order)
				}
				if !current.Open() || !sameFiles(normalizeEpisodeScope(current.Gap.Scope), normalizeEpisodeScope(want)) || current.Gap.Key() != key {
					t.Fatalf("the episode is not open over exactly %v under its key: %+v", want, current.Gap)
				}
				return routed
			}
			step(nil, both)                   // widened to A+B
			step(lockAnchors(b), []string{a}) // narrowed back to A
			routed := step(lockAnchors(a), []string{b})
			if routed.Granted() || routed.Gap.Key() != key || !sameFiles(routed.Gap.Scope, []string{b}) {
				t.Fatalf("the routing of B alone was not bound to the episode B was once a member of: %+v", routed)
			}
		})
	}
}

// W14 -- CURRENT PLAN MEMBERSHIP IS THE ACTION'S FACT (review f2). The open
// qualified episode is re-evaluated through both production register paths
// with a decision and an Action that disagree about which files the plan
// names. Only the Action decides: a member it omits is withdrawn although the
// decision still lists it, and a member it names is kept although the decision
// does not.
func TestDF30W14APlanMembershipIsTheActionsFact(t *testing.T) {
	planned := []string{df30Locked, df30Other}
	for _, entry := range []string{"ordinary", "post-authorization"} {
		for name, c := range map[string]struct {
			decision, action, want []string
		}{
			"the action withdraws a member the decision lists": {planned, []string{df30Locked}, []string{df30Locked}},
			"the action keeps a member the decision omits":     {[]string{df30Locked}, planned, planned},
		} {
			t.Run(entry+"/"+name, func(t *testing.T) {
				const taskID = "task-df30-w14-membership"
				e := df30Engine(t, taskID)
				pinWorld(t, e, taskID, prospectiveWorld)
				base := Action{Stage: StageCandidateEdit, Files: planned, Present: planned, Examined: planned}
				qualified := scopedPreflight(t, df30Region(df30QualifiedSpot, "APPROVAL_GATE_NONE"))
				opened := routeAuthorityForAction(qualified, nil, base)
				opened.Gap.World = prospectiveWorld
				d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: planned}
				if _, r := e.registerRouting(taskID, prospectiveWorld, opened, base, qualified, d, true); !r.Open() {
					t.Fatalf("premise: the episode is open: %+v", r)
				}
				act := Action{Stage: StageCandidateEdit, Files: c.action, Present: c.action, Examined: c.action}
				clean := scopedPreflight(t, df30Region("", "APPROVAL_GATE_NONE"))
				fresh := routeAuthorityForAction(clean, nil, act)
				if !fresh.Granted() {
					t.Fatalf("premise: the clean later routing alone would grant: %+v", fresh)
				}
				later := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: c.decision}
				var routed Routing
				if entry == "ordinary" {
					routed, _ = e.registerRouting(taskID, prospectiveWorld, fresh, act, clean, later, true)
				} else {
					routed, _ = e.registerAuthorizedRouting(taskID, prospectiveWorld, fresh, act, clean, later)
				}
				current := resolutionOf(e, taskID, opened.Gap)
				if routed.Granted() || !current.Open() || !sameFiles(normalizeEpisodeScope(current.Gap.Scope), normalizeEpisodeScope(c.want)) ||
					current.Disposition == nil || !sameFiles(normalizeEpisodeScope(current.Disposition.Unresolved), normalizeEpisodeScope(c.want)) {
					t.Fatalf("membership was not the Action's: want %v, routed %+v episode %+v disposition %+v", c.want, routed, current.Gap, current.Disposition)
				}
			})
		}
	}
}

// W13 -- A SETTLED MEMBER IS SETTLED ONLY WHILE ITS FACT STANDS (review f1).
// The qualified episode narrows to df30Other when a lock derivation settles
// df30Locked. A later Action still plans df30Locked but no longer carries that
// derivation: on every entry route, under an unqualified or a clean later
// preflight, df30Locked re-enters the SAME episode, whose scope and condition again name
// both members. A lock derivation over df30Other alone then narrows the
// episode to df30Locked -- it never closes it -- because no current fact
// settles df30Locked.
func TestDF30W13ASettledMemberReEntersWhenItsFactDisappears(t *testing.T) {
	const taskID = "task-df30-w13-reentry"
	planned := []string{df30Locked, df30Other}
	d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: planned}
	base := Action{Stage: StageCandidateEdit, Files: planned, Present: planned, Examined: planned}
	for later, spot := range df30QualifiedLaters {
		for _, entry := range []string{"ordinary", "post-authorization"} {
			name := later + "/" + entry
			t.Run(name, func(t *testing.T) {
				e, opened, _, _ := df30ReEvaluateQualified(t, taskID, entry, spot, lockAnchors(df30Locked))
				narrowed := resolutionOf(e, taskID, opened.Gap)
				if !narrowed.Open() || !sameFiles(narrowed.Gap.Scope, []string{df30Other}) || narrowed.Gap.Opening == nil ||
					!sameSemantics(narrowed.Gap.Opening.Semantics, opened.Gap.Semantics) {
					t.Fatalf("premise: the episode narrows to the unsettled member, its opening binding its question: %+v", narrowed.Gap)
				}
				// The derivation that settled df30Locked is gone; the file
				// is still planned, present and examined.
				routed, _ := df30Enter(t, e, taskID, entry, spot, base, d)
				current := resolutionOf(e, taskID, opened.Gap)
				open := openCoverageGaps(e, taskID, prospectiveWorld)
				if len(e.gapResolutions(taskID).order) != 1 || len(open) != 1 || open[0].Gap.Key() != opened.Gap.Key() ||
					!sameFiles(normalizeEpisodeScope(current.Gap.Scope), normalizeEpisodeScope(planned)) ||
					current.Disposition == nil || !sameFiles(normalizeEpisodeScope(current.Disposition.Unresolved), normalizeEpisodeScope(planned)) {
					t.Fatalf("the member whose settling fact disappeared did not re-enter the episode: open %+v disposition %+v", open, current.Disposition)
				}
				if routed.Granted() || routed.Gap.Key() != opened.Gap.Key() ||
					!sameFiles(normalizeEpisodeScope(routed.Gap.Scope), normalizeEpisodeScope(planned)) {
					t.Fatalf("the re-entered episode did not stop the continuation over both members: %+v", routed)
				}
				cond := current.Routing.Condition
				if cond != opened.Condition || !sameSemantics(current.Gap.Semantics, opened.Gap.Semantics) {
					t.Fatalf("the re-entered episode is not described as it opened: %q, opened as %q", cond, opened.Condition)
				}

				// A lock derivation over df30Other alone narrows it to
				// df30Locked; nothing currently settles df30Locked, so the
				// episode does not close.
				act := base
				act.DerivedCoverage = lockAnchors(df30Other)
				routed, _ = df30Enter(t, e, taskID, entry, spot, act, d)
				current = resolutionOf(e, taskID, opened.Gap)
				cond = current.Routing.Condition
				if !current.Open() || !sameFiles(current.Gap.Scope, []string{df30Locked}) || routed.Granted() ||
					!strings.Contains(cond, df30Locked) || strings.Contains(cond, df30Other) || !strings.Contains(cond, df30QualifiedSpot) ||
					(spot != "" && strings.Contains(cond, spot)) {
					t.Fatalf("the episode closed or mis-narrowed without a current fact settling %s: routed %+v episode %+v condition %q",
						df30Locked, routed, current.Gap, cond)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// RULING-181 59a FAIL-CLOSED RESTORE BOUNDARY. Objective 59a owns the live
// coverage episode only; a coverage question read back from the durable record
// is never an authorizable restored episode, because no canonical episode
// encoding exists to authenticate it (objective 59b). These witnesses prove
// only the refusal: none of them requires a successful restored authorization.
// ---------------------------------------------------------------------------

// df30LiveDeferral asks a live coverage episode's question through the one
// rendezvous and defers it, returning the durable record it left and the
// routing it was asked about. Asking LIVE is not refused: the boundary is
// about restoration, not about coverage questions.
func df30LiveDeferral(t *testing.T, store *session.Store, taskID string) (session.Interrupted, Routing) {
	t.Helper()
	_, _, out := df30Measured(t)
	scoped := scopedPreflight(t, neighbourCovered)
	d := architectureDecision{Decision: "proceed", Mode: ModeModify, Files: df30Planned()}
	if err := seedAppend(t, store, event.New("s1", taskID, event.SourceUser, event.TaskCreated, "change the planned files", nil)); err != nil {
		t.Fatal(err)
	}
	e := df30EngineOn(t, store, taskID)
	if err := seedAppend(t, store, event.New("s1", taskID, event.SourceSystem, event.PlanAttemptStarted, "started",
		map[string]any{"plan_attempt_id": df30Attempt().ID, "task_id": taskID, "world": "", "plan_source": PlanByArchitect,
			"plan": map[string]string{}})); err != nil {
		t.Fatal(err)
	}
	action := df30Action(out, nil)
	opened := routeAuthorityForAction(scoped, nil, action)
	opened.Gap.World = prospectiveWorld
	routed, r := e.registerRouting(taskID, prospectiveWorld, opened, action, scoped, d, true)
	if !r.Open() || !isCoverageGapKind(routed.Gap.Kind) {
		t.Fatalf("premise: a live coverage episode is open: %+v", r)
	}
	if refusal := e.unauthenticatedCoverageRestore(taskID, routed.Gap); refusal != nil {
		t.Fatalf("a live episode's question was refused as a restoration: %v", refusal)
	}
	ctx := withPlanAttempt(withAuthorityGap(fixtureCtx(e, taskID), routed.Gap), e.questionOwner(taskID, routed.Gap))
	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(ctx, nil, taskID, routed.Condition, "github.com/globulario/sensei-code", prospectiveWorld,
			authority.Decision{Level: authority.Human, Subject: "May the run proceed with the gap open?", Options: realOptions()},
			realOptions(), d.Files...)
		errc <- err
	}()
	waitForPending(t, e, taskID)
	if !e.DeferAuthority(taskID) {
		t.Fatal("the live question was not pending")
	}
	if err := <-errc; !errors.Is(err, errAuthorityDeferred) {
		t.Fatalf("the live deferral produced %v", err)
	}
	// The deferring invocation ends, and gives back the task's lease.
	endFixtureInvocation(e, taskID)
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range session.FindInterrupted(history) {
		if task.TaskID == taskID && len(task.AwaitingAuthority) != 0 {
			return task, routed
		}
	}
	t.Fatal("the deferred coverage question is not resumable")
	return session.Interrupted{}, Routing{}
}

// assertResumeRefusedBeforeAsking resumes task on a fresh engine over store
// and requires the typed restoration refusal for a coverage episode: no
// question put to anyone, no answer recorded, nothing settled, and the task
// still resumable.
func assertResumeRefusedBeforeAsking(t *testing.T, store *session.Store, task session.Interrupted, gap GapIdentity) {
	t.Helper()
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	// No Repo and no Sensei: the refusal is decided before anything starts.
	e := &Engine{Bus: bus, Store: store, SessionID: "s2", pending: map[string]chan string{}}
	bindFixtureSession(t, store, task.TaskID, e.SessionID)
	e.resumeAuthority(fixtureCtx(e, task.TaskID), task)
	evs := take()
	for _, ev := range evs {
		switch ev.Kind {
		case event.AuthorityRequired, event.AuthorityResolved, event.WorkflowFailed:
			t.Fatalf("the restored coverage question reached %s: %q\n%s", ev.Kind, ev.Summary, gapLoopTrace(evs))
		}
	}
	var refusal RestorationRefusal
	payloadOf(t, evs, event.WorkflowRestorationRefused, &refusal)
	if refusal.Subject != restorationSubjectCoverageEpisode || refusal.TaskID != task.TaskID {
		t.Fatalf("the resume was not refused as an unauthenticated coverage episode: %+v", refusal)
	}
	if _, err := ParseRestorationRefusal(mustJSON(t, refusal)); err != nil {
		t.Fatalf("the refusal is not a valid typed restoration refusal: %v", err)
	}
	if authorized, settled := e.gapSettlement(task.TaskID, Routing{Gap: gap}); authorized || settled {
		t.Fatalf("the refused resume settled the gap: authorized=%v settled=%v", authorized, settled)
	}
	history, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	resumable := false
	for _, it := range session.FindInterrupted(history) {
		resumable = resumable || (it.TaskID == task.TaskID && len(it.AwaitingAuthority) != 0)
	}
	if !resumable {
		t.Fatal("the refusal consumed the preserved question")
	}
}

// RESTORE BOUNDARY -- A COVERAGE QUESTION THIS ENGINE DEFERRED LIVE is refused
// on resume before awaitChoice: its record holds the gap's identity but no
// episode, so its opening semantics and pinned-world binding cannot be
// authenticated. The live episode state never reaches the record.
func TestDF30RestoreBoundaryALiveDeferredCoverageQuestionIsRefusedOnResume(t *testing.T) {
	const taskID = "task-df30-restore-live"
	store := sessionStore(t)
	task, routed := df30LiveDeferral(t, store, taskID)
	var q DeferredAuthority
	if err := json.Unmarshal(task.AwaitingAuthority, &q); err != nil {
		t.Fatal(err)
	}
	if q.Gap == nil || q.Gap.Key() != routed.Gap.Key() || q.Gap.Semantics != nil || q.Gap.Opening != nil {
		t.Fatalf("premise: the record names the gap and carries no live episode state: %+v", q.Gap)
	}
	assertResumeRefusedBeforeAsking(t, store, task, *q.Gap)
}

// RESTORE BOUNDARY -- PAYLOAD-LESS LEGACY RECORDS of every coverage kind, with
// and without a recorded authorizing answer, are refused before awaitChoice,
// and the historical answer installs no authority. CONTROL: the same record
// about a non-coverage gap is not stopped by this boundary -- it reaches the
// next step, starting Sensei.
func TestDF30RestoreBoundaryALegacyCoverageRecordIsRefusedBeforeAnyoneIsAsked(t *testing.T) {
	for _, kind := range []string{gapCoverageUnexamined, gapCoverageAbsent, gapCoverageBlindSpot, "unverified-premise"} {
		for _, answered := range []bool{false, true} {
			name := kind
			if answered {
				name += "/answered"
			}
			t.Run(name, func(t *testing.T) {
				taskID := "task-df30-restore-legacy"
				store := storeWithTaskCreated(t, taskID, "change main.go")
				gap := GapIdentity{Kind: kind, Scope: []string{"main.go"}, World: prospectiveWorld}
				q := DeferredAuthority{Condition: "a bounded knowledge gap was not closed by investigation: graph coverage is absent",
					Domain: "github.com/globulario/sensei-code", BaseSHA: prospectiveWorld, TaskID: taskID, SessionID: "s1",
					Decision: authority.Decision{Level: authority.Human, Subject: "May main.go change?", Options: realOptions()},
					Scope:    []string{"main.go"}, ScopeRecorded: true, Gap: &gap}
				evs := []event.Event{}
				if answered {
					evs = append(evs, event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, "Authorize",
						resolvedAuthority{Resolution: authority.Resolution{TaskID: taskID, SessionID: "s1", Question: q.Decision.Subject, Condition: q.Condition,
							OptionID: "1", OptionLabel: "Authorize", Scope: q.Scope, Outcome: authority.Authorize, State: authority.Unsupported, DecidedAt: time.Now().UTC()},
							Gap: &gap}))
				}
				evs = append(evs, event.New("s1", taskID, event.SourceUser, event.WorkflowAwaitingAuthority, q.Condition, q))
				for _, ev := range evs {
					if err := seedAppend(t, store, ev); err != nil {
						t.Fatal(err)
					}
				}
				history, err := store.Load()
				if err != nil {
					t.Fatal(err)
				}
				tasks := session.FindInterrupted(history)
				if len(tasks) != 1 || len(tasks[0].AwaitingAuthority) == 0 {
					t.Fatalf("premise: the legacy question is resumable: %+v", tasks)
				}
				if kind != "unverified-premise" {
					assertResumeRefusedBeforeAsking(t, store, tasks[0], gap)
					return
				}
				bus := event.NewBus()
				take, done := collect(t, bus)
				defer done()
				e := &Engine{Bus: bus, Store: store, SessionID: "s2", pending: map[string]chan string{}}
				bindFixtureSession(t, store, tasks[0].TaskID, e.SessionID)
				e.resumeAuthority(fixtureCtx(e, tasks[0].TaskID), tasks[0])
				for _, ev := range take() {
					if ev.Kind == event.WorkflowRestorationRefused {
						t.Fatalf("a non-coverage question was refused as a coverage episode: %q", ev.Summary)
					}
				}
				if answered {
					if authorized, settled := e.gapSettlement(taskID, Routing{Gap: gap}); !authorized || !settled {
						t.Fatalf("control: a non-coverage answer is no longer restored: authorized=%v settled=%v", authorized, settled)
					}
				}
			})
		}
	}
}

// RESTORE BOUNDARY AT THE RENDEZVOUS -- whichever route reaches it, the one
// place a task waits on a person refuses a restored coverage identity before
// the question is emitted: a restarted process that routes onto a coverage gap
// it restored from the record cannot ask about it, and no answer binds to it.
func TestDF30RestoreBoundaryTheRendezvousRefusesARestoredCoverageIdentity(t *testing.T) {
	const taskID = "task-df30-restore-rendezvous"
	store := sessionStore(t)
	task, _ := df30LiveDeferral(t, store, taskID)
	var q DeferredAuthority
	if err := json.Unmarshal(task.AwaitingAuthority, &q); err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := df30EngineOn(t, store, taskID)
	e.Bus = bus
	r := resolutionOf(e, taskID, *q.Gap)
	if !r.restored || !r.Open() {
		t.Fatalf("premise: the identity is restored open and unauthenticated: %+v", r)
	}
	// Even a gap carrying a well-formed live payload is refused once its
	// identity is the restored one: a later routing cannot lend a restored
	// identity a replacement episode.
	lent := *q.Gap
	sem := coverageSemantics(lent.Kind, nil, "")
	lent.Semantics = &sem
	for _, gap := range []GapIdentity{*q.Gap, lent} {
		// Bounded: a rendezvous that asked would block on an answer.
		ctx, cancel := context.WithTimeout(fixtureCtx(e, taskID), 2*time.Second)
		_, err := e.awaitChoice(withAuthorityGap(ctx, gap), nil, taskID, q.Condition, q.Domain, q.BaseSHA,
			q.Decision, q.Decision.Options, q.Scope...)
		cancel()
		var refusal *RestorationRefusal
		if !errors.As(err, &refusal) || refusal.Subject != restorationSubjectCoverageEpisode {
			t.Fatalf("the rendezvous did not refuse the restored coverage identity: %v", err)
		}
	}
	for _, ev := range take() {
		if ev.Kind == event.AuthorityRequired || ev.Kind == event.AuthorityResolved {
			t.Fatalf("the restored coverage question was put to a person: %s %q", ev.Kind, ev.Summary)
		}
	}
	if authorized, settled := e.gapSettlement(taskID, Routing{Gap: *q.Gap}); authorized || settled {
		t.Fatal("a refused question settled the restored identity")
	}
}

// A2-W10 (70B2a2): RULING-181 still fails closed once a valid session lineage
// is established. A coverage question its ancestor holder recorded is the
// task's own canonical question, and a fresh process binds to the task's
// lineage -- and the restored coverage episode is still refused, typed,
// before anyone is asked: lineage authenticates the session, never the
// episode (objective 59b).
func TestB2a2A2W10TheRestoredCoverageGateFailsClosedUnderAValidLineage(t *testing.T) {
	const taskID = "task-b2a2-df30"
	store := heldTaskStore(t, taskID)
	gap := GapIdentity{Kind: gapCoverageUnexamined, Subject: "region", Scope: []string{"internal/x.go"}, World: "w"}
	q := DeferredAuthority{Condition: "coverage", TaskID: taskID, SessionID: "s0-holder", Scope: gap.Scope, ScopeRecorded: true,
		Gap: &gap, Decision: authority.Decision{Level: authority.Human, Subject: "May this proceed?", Options: realOptions()}}
	if err := seedAppend(t, store, event.New("s0-holder", taskID, event.SourceUser, event.WorkflowAwaitingAuthority, q.Condition, q)); err != nil {
		t.Fatal(err)
	}
	record, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var task session.Interrupted
	for _, it := range session.CanonicalInterrupted(record) {
		if it.TaskID == taskID {
			task = it
		}
	}
	if task.Unavailable != nil || len(task.AwaitingAuthority) == 0 {
		t.Fatalf("premise: the ancestor's coverage question is the task's canonical question: %+v", task)
	}
	bus := event.NewBus()
	take, done := collect(t, bus)
	defer done()
	e := &Engine{Bus: bus, SessionID: "s-fresh", Store: store, pending: map[string]chan string{}}
	attempt := e.ResumeTask(context.Background(), task)
	if b := attempt.Binding(); b.Refusal != nil || b.CurrentSessionID != "s-fresh" {
		t.Fatalf("premise: the fresh session is bound to the task's valid lineage: %+v", b)
	}
	select {
	case <-attempt.Ended():
	case <-time.After(15 * time.Second):
		t.Fatal("the resume did not end")
	}
	evs := take()
	for _, ev := range evs {
		if ev.Kind == event.AuthorityRequired || ev.Kind == event.AuthorityResolved {
			t.Fatalf("the restored coverage question was asked or answered under a valid lineage: %s", ev.Kind)
		}
	}
	var refusal RestorationRefusal
	payloadOf(t, evs, event.WorkflowRestorationRefused, &refusal)
	if refusal.Subject != restorationSubjectCoverageEpisode {
		t.Fatalf("the restored coverage question was not refused by RULING-181: %+v", refusal)
	}
}

// F4b -- A ROUTE THAT PREVENTED PROBING IS SURFACED, NEVER MASKED BY A
// TEST-EDIT REFUSAL (RULING-257, RULING-258).
//
// TD-1 S1 r1, plan attempt e3d912a97f5d: step 0 tripped the outward-action
// lexicon, so the route was human-authority-required and the per-file probe
// was skipped by design (#115). The authored grant for roles_test.go
// (governed through verdict.go) was therefore never computed, and routePlan
// refused the plan as test_edit_admission against that incomplete record:
// the terminal reason named a missing grant and the operative authority
// question was never asked. Every witness below drives the production entry
// (resolveSuppliedPlan; W6 the whole run, execute) over a pinned Git world, a
// real derivation record and a Sensei MCP stub; every human answer is bound to
// the plan attempt its question was asked about; nothing depends on a live
// model's wording.

const (
	f4bEngine    = "internal/workflow/engine.go"
	f4bVerdict   = "internal/roles/verdict.go"
	f4bRolesTest = "internal/roles/roles_test.go"
	// f4bOutward is the consequence condition the specimen's step 0 produces
	// (tool-debt-prep/F4-router-remeasurement.md, chain step 2).
	f4bOutward = "the plan states it will act outside the worktree"
)

// f4bSpecimenJSON is plan attempt e3d912a97f5d as the run recorded it
// (run-TD1S1-r1-fresh.jsonl, extracted verbatim to F4-replay/plans.json):
// step 0 verbatim, the seven-file scope and its five test-edit declarations.
const f4bSpecimenJSON = `
{
	"decision": "proceed",
	"summary": "Implement TD-1 S1 within the pinned two-production-file envelope so evidence discharge depends on authenticated broker evidence, exact candidate applicability, executed-check definition, outcome, and obligation match—not implementer citation equality.",
	"steps": [
		"Bring the five existing test fixtures to production evidence shape first: add bundle and per-check candidate binding and remove every assumption that an ExecutedBy string authenticates the producer.",
		"Add TestTD1S1W1ExactCandidateBrokerEvidenceDischargesDespiteCitationVariant through TestTD1S1W6UnrelatedCheckCannotDischargeObligation and TestTD1S1W8ExistingBrokerDischargesRemainValid across the four workflow test files, covering live retention and durable replay.",
		"In engine.go, derive discharge from the finding's complete requirement and the engine-owned broker bundle: require Bundle.Certifies for the expected candidate, an authorized execution path, the demanded check definition, a complete attributable outcome, and the correct proposition; return the existing structured open-finding reason when any property is absent.",
		"Retain the matched check's canonical commandLine and engine-established provenance, candidate binding, outcome, output integrity, and source; require exact durable read-back and preserve replay parity through the shared accounting functions without editing implementer_incomplete.go.",
		"In verdict.go, render both Correction and ProofGap when both exist, and add TestTD1S1W7LineShowsCompleteDischargeRequirement in roles_test.go so the worker-visible requirement equals the matcher input.",
		"Run the eight witnesses, both Sensei-required governed-record tests, gofmt -w cmd internal, go vet ./..., go test ./..., architecture edit checks, the final diff audit, and broker mutation validation for T1-T7 within the four-hour budget; claim completion only if every required check runs and passes."
	],
	"consequences": "A finding can be discharged despite harmless citation-text variation only when authentic broker evidence proves the demanded proposition on the exact candidate and survives durable read-back. Wrong-candidate, unauthorized, absent, incomplete, failed, unrelated, or prose-only evidence remains open with an actionable reason. Workers see the complete requirement the matcher uses. Existing legitimate broker-evidence discharges remain valid, and repository storage and ownership boundaries do not expand.",
	"files": [
		"internal/workflow/engine.go",
		"internal/roles/verdict.go",
		"internal/workflow/reviewconsistency_test.go",
		"internal/workflow/finding_accounting_test.go",
		"internal/workflow/nonconvergence_test.go",
		"internal/workflow/engine_test.go",
		"internal/roles/roles_test.go"
	],
	"mode": "modify",
	"related_invariants": [
		"invariant:sensei_code.candidate.disposition_is_decided_and_evidence_outlives_removal",
		"invariant:sensei_code.reviewobservation.evidence_is_never_silence",
		"invariant:sensei_code.roles.adversarial_roles_inherit_nothing_and_bind_to_a_revision",
		"invariant:sensei_code.workflow.context_never_widens_worker_scope",
		"invariant:sensei_code.workflow.unexamined_planned_file_is_not_covered_by_its_neighbour"
	],
	"plan": "Modify only the seven pinned files. Treat the worker citation as a non-authoritative locator and mechanically discharge an evidence finding only when the engine-owned validation path supplies a complete execution whose bundle certifies the expected candidate, whose check definition and outcome establish the finding's complete actionable requirement, and whose canonical retained record is durably read back. Never authenticate with ExecutedBy. Preserve the existing failing-outcome heuristic and every legitimate base discharge. Make Finding.Line expose the same Correction and ProofGap that matching enforces. Add the eight named witnesses and external mutation checks. Do not add a production owner, evidence store, test file, S2/S3 behavior, TD-2 routing, toolchain field, or obligation-id field. Before mutation, require task-bound Sensei briefing and admission; stop on refusal, uncertifiable evidence, envelope expansion, or any need to edit implementer_incomplete.go.",
	"claims": [
		{
			"statement": "The checkout is clean at a7600fde91d793fde8f3b694c4ec9c35458b47f6.",
			"about": "repository",
			"source": "repository"
		},
		{
			"statement": "Sensei reports an authoritative current graph at digest 116ef984f6e734e82bef93e169212a2cda7444e15a0076c6b5a5f0fb3822d6c5, bound to the pinned repository revision.",
			"about": "Sensei workspace identity",
			"source": "graph"
		},
		{
			"statement": "Scoped preflight over all seven files is PREFLIGHT_STATUS_OK, ARCHITECTURE_SENSITIVE, CONFIDENCE_HIGH, with 24 direct anchors and all seven files indexed.",
			"about": "TD-1 S1 file envelope",
			"source": "graph"
		},
		{
			"statement": "The scoped preflight requires TestEveryGovernedCallSiteIsClassified and TestTheAuditRecordCarriesItsRequest.",
			"about": "internal/workflow/engine.go",
			"source": "graph"
		},
		{
			"statement": "Per-file briefings remain BRIEFING_STATUS_DEGRADED with reason repository_context_absent, while the scoped preflight is authoritative and sufficient; task-bound admission is not established because no task identity has been requested.",
			"about": "TD-1 S1 execution authority",
			"source": "graph"
		},
		{
			"statement": "unmetProof currently concatenates ProofGap and Correction, requires the citation to be their substring, and citedExecution requires exact equality with commandLine.",
			"about": "internal/workflow/engine.go",
			"source": "repository"
		},
		{
			"statement": "Finding.Line currently renders Correction instead of ProofGap when both are set.",
			"about": "internal/roles/verdict.go",
			"source": "repository"
		},
		{
			"statement": "validation.Bundle.Certifies already checks bundle candidate identity, non-empty exact diff digest, and the binding of every contained check.",
			"about": "internal/validation/evidence.go",
			"source": "repository"
		},
		{
			"statement": "Evidence discharge already requires a retained record to be read back, and retainedRecords stores commandLine for the matched check rather than an arbitrary response string.",
			"about": "internal/workflow/engine.go",
			"source": "repository"
		},
		{
			"statement": "Current workflow fixtures omit production candidate binding in n2bBundle, and engine_test.go assigns free-string ExecutedBy values.",
			"about": "workflow test fixtures",
			"source": "repository"
		},
		{
			"statement": "All five declared test files already exist, have no build constraints, and their package clauses and current imports match the declarations above.",
			"about": "test edit envelope",
			"source": "repository"
		}
	],
	"test_edits": [
		{
			"path": "internal/workflow/reviewconsistency_test.go",
			"operation": "edit",
			"package": "workflow",
			"build_constraints": [],
			"imports": [
				"strings",
				"testing",
				"github.com/globulario/sensei-code/internal/roles",
				"github.com/globulario/sensei-code/internal/sensei",
				"github.com/globulario/sensei-code/internal/validation"
			]
		},
		{
			"path": "internal/workflow/finding_accounting_test.go",
			"operation": "edit",
			"package": "workflow",
			"build_constraints": [],
			"imports": [
				"strings",
				"testing",
				"github.com/globulario/sensei-code/internal/roles",
				"github.com/globulario/sensei-code/internal/validation"
			]
		},
		{
			"path": "internal/workflow/nonconvergence_test.go",
			"operation": "edit",
			"package": "workflow",
			"build_constraints": [],
			"imports": [
				"context",
				"encoding/json",
				"os",
				"strings",
				"testing",
				"github.com/globulario/sensei-code/internal/config",
				"github.com/globulario/sensei-code/internal/event",
				"github.com/globulario/sensei-code/internal/gitx",
				"github.com/globulario/sensei-code/internal/roles",
				"github.com/globulario/sensei-code/internal/runreceipt",
				"github.com/globulario/sensei-code/internal/session"
			]
		},
		{
			"path": "internal/workflow/engine_test.go",
			"operation": "edit",
			"package": "workflow",
			"build_constraints": [],
			"imports": [
				"context",
				"encoding/json",
				"errors",
				"fmt",
				"go/ast",
				"go/parser",
				"go/token",
				"os",
				"strings",
				"testing",
				"time",
				"github.com/globulario/sensei-code/internal/roles",
				"github.com/globulario/sensei-code/internal/authority",
				"github.com/globulario/sensei-code/internal/decision",
				"github.com/globulario/sensei-code/internal/event",
				"github.com/globulario/sensei-code/internal/taskstate"
			]
		},
		{
			"path": "internal/roles/roles_test.go",
			"operation": "edit",
			"package": "roles",
			"build_constraints": [],
			"imports": [
				"reflect",
				"strings",
				"testing",
				"time"
			]
		}
	]
}
`

func f4bSpecimen(t *testing.T) architectureDecision {
	t.Helper()
	var d architectureDecision
	if err := json.Unmarshal([]byte(f4bSpecimenJSON), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Files) != 7 || len(d.TestEdits) != 5 || !strings.HasPrefix(d.Steps[0], "Bring the five existing test fixtures to production evidence shape first") {
		t.Fatalf("premise: the specimen is not attempt e3d912a97f5d: %d files, %d test edits, step 0 %q", len(d.Files), len(d.TestEdits), d.Steps[0])
	}
	return d
}

// f4bBounded is the specimen with step 0's "to production" wording removed and
// nothing else changed: a plan whose consequences are bounded, so the route
// GRANTS (the 79c7f6d00eac control took that route over the same files).
func f4bBounded(t *testing.T) architectureDecision {
	d := f4bSpecimen(t)
	d.Steps = append([]string(nil), d.Steps...)
	d.Steps[0] = strings.Replace(d.Steps[0], "to production evidence shape", "to the evidence shape", 1)
	d.Plan += " (bounded wording)"
	return d
}

// f4bWorld is the pinned world: both production files and every declared test,
// each test importing exactly what its declaration states.
func f4bWorld(t *testing.T) map[string]string {
	files := map[string]string{f4bEngine: "package workflow\n", f4bVerdict: "package roles\n"}
	for _, te := range f4bSpecimen(t).TestEdits {
		var src strings.Builder
		src.WriteString("package " + te.Package + "\n\nimport (\n")
		for _, imp := range te.Imports {
			src.WriteString("\t\"" + imp + "\"\n")
		}
		src.WriteString(")\n")
		files[te.Path] = src.String()
	}
	return files
}

// f4bRegion is the scoped answer over the seven files: certifiable, covered,
// no gate. Only the plan's own consequences can make the route human-owned.
const f4bRegion = `{"status":"PREFLIGHT_STATUS_OK",` +
	`"coverage":{"sufficient":true,"direct_anchor_count":24,"file_count":7,"indexed_file_count":7},` +
	`"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},` +
	identifiedAuthority + `}`

// f4bAuthoredProbe is an examined per-file answer naming an AUTHORED invariant:
// the production governance verdict.go carries at the pinned world.
const f4bAuthoredProbe = `{"content":[{"type":"text","text":"examined"}],"structuredContent":{"status":"PREFLIGHT_STATUS_OK",` +
	`"coverage":{"sufficient":true,"direct_anchor_count":1,"file_count":1,"indexed_file_count":1},` +
	`"direct_invariants":[{"class":"invariant","id":"invariant:sensei_code.roles.adversarial_roles_inherit_nothing_and_bind_to_a_revision"}],` +
	`"authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT","graph_build_commit":"fac399f8225f","source_repo_commit":"f56f5a305798"}}}`

type f4bCase struct {
	plan   architectureDecision
	region string
	// authored: verdict.go's per-file probe names an authored invariant.
	authored bool
	// gap: verdict.go's per-file probe reports it unexamined, so the probe
	// that runs after authorization opens a coverage gap.
	gap bool
	// architect: the plan is the architect's, not supplied, and its question
	// is answered Authorize when asked (executed paths only).
	architect bool
	// answer, when set, is the human's answer to the question the plan's own
	// route asked and deferred, bound to that question's plan attempt; the
	// plan is then routed again under it (route).
	answer authority.Outcome
}

type f4bResult struct {
	w      *df30Production
	e      *Engine
	taskID string
	// resolve routes a plan through the supplied-plan entry against this
	// world's Sensei stub.
	resolve  func(architectureDecision) (architectureDecision, error)
	admitted architectureDecision
	err      error
	// asked: the run reached the authority rendezvous, and the question was
	// deferred there.
	asked  bool
	events []event.Event
}

// f4bCondition is the condition the router reaches for c's plan over c's
// region, before anything is derived for it.
func f4bCondition(t *testing.T, c f4bCase) Routing {
	t.Helper()
	return routeAuthorityForAction(scopedPreflight(t, c.region), nil, Action{Stage: StageCandidateEdit, Files: c.plan.Files,
		DeclaredSteps: c.plan.Steps, DeclaredConsequences: c.plan.Consequences, DeclaredEffects: c.plan.DeclaredEffects})
}

const f4bObjective = "TD-1 S1: discharge evidence findings from authenticated broker evidence"

func f4bRun(t *testing.T, taskID string, c f4bCase) f4bResult {
	t.Helper()
	r := f4bWorldFor(t, taskID, c)
	return r.route(t, c)
}

// f4bWorldFor pins the world, the Sensei answers and the derivation for c, and
// records nothing for the task beyond its creation.
func f4bWorldFor(t *testing.T, taskID string, c f4bCase) f4bResult {
	t.Helper()
	w := newDF30Production(t, taskID, f4bWorld(t))
	sc := df30Sensei(t, w.state)
	r := f4bResult{w: w, e: w.e, taskID: taskID}
	r.resolve = func(d architectureDecision) (architectureDecision, error) {
		return r.e.resolveSuppliedPlan(fixtureCtx(r.e, taskID), sc, certifiedStart{}, taskID, f4bObjective, SuppliedPlan{decision: d, Digest: "f4b"})
	}
	if err := seedAppend(t, r.e.Store, event.New("s1", taskID, event.SourceSystem, event.TaskCreated, f4bObjective, nil)); err != nil {
		t.Fatal(err)
	}
	w.region(t, c.region)
	w.probes([]string{f4bEngine, f4bVerdict}, nil)
	if c.gap {
		w.probes([]string{f4bEngine}, []string{f4bVerdict})
	}
	if c.authored {
		w.put(t, "examined.json", f4bAuthoredProbe)
	}
	// The derivation covers engine.go only: the four workflow tests are
	// DERIVED-granted, and roles_test.go can be granted only by the authored
	// instrument through verdict.go.
	w.derives(t, f4bEngine)
	return r
}

// route routes c's plan through the supplied plan entry, deferring the
// question if the run reaches the rendezvous. With an answer, the plan is
// first routed unanswered so its question is asked and deferred; the answer is
// then recorded as production records one -- bound to the PlanAttemptID the
// deferred question names -- and the same plan is routed again under it.
func (r f4bResult) route(t *testing.T, c f4bCase) f4bResult {
	t.Helper()
	if c.answer == "" {
		return r.routeOnce(t, c.plan)
	}
	deferred := r.routeOnce(t, c.plan)
	if !deferred.asked || !errors.Is(deferred.err, errAuthorityDeferred) {
		t.Fatalf("premise: an answer is recorded only for a question the plan's own route asked and deferred: asked=%v err=%v", deferred.asked, deferred.err)
	}
	deferred.answer(t, f4bQuestion(t, deferred.events), c.answer)
	return deferred.routeOnce(t, c.plan)
}

// f4bQuestion is the last question the task deferred.
func f4bQuestion(t *testing.T, evs []event.Event) DeferredAuthority {
	t.Helper()
	var q DeferredAuthority
	found := false
	for _, ev := range evs {
		if ev.Kind == event.WorkflowAwaitingAuthority {
			q, found = DeferredAuthority{}, true
			if err := json.Unmarshal(ev.Payload, &q); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !found || strings.TrimSpace(q.PlanAttemptID) == "" {
		t.Fatalf("premise: a deferred question naming its plan attempt was recorded: found=%v %+v", found, q)
	}
	return q
}

// answer records the human's answer to q, owned by the attempt q was asked
// about (resolvedAuthority.PlanAttemptID), never by the legacy epoch reading.
func (r f4bResult) answer(t *testing.T, q DeferredAuthority, outcome authority.Outcome) {
	t.Helper()
	label := "Authorize the architectural change described above"
	if outcome != authority.Authorize {
		label = "Stop"
	}
	answer := resolvedAuthority{Resolution: authority.Resolution{TaskID: r.taskID, SessionID: "s1", Question: q.Decision.Subject,
		Condition: q.Condition, OptionID: "1", OptionLabel: label,
		Scope: q.Scope, Outcome: outcome, State: authority.Unsupported, DecidedAt: time.Now().UTC()},
		PlanAttemptID: q.PlanAttemptID}
	if err := seedAppend(t, r.e.Store, event.New("s1", r.taskID, event.SourceUser, event.AuthorityResolved, label, answer)); err != nil {
		t.Fatal(err)
	}
}

// routeOnce routes plan through the supplied plan entry, deferring the
// question if the run reaches the rendezvous.
func (r f4bResult) routeOnce(t *testing.T, plan architectureDecision) f4bResult {
	t.Helper()
	e, taskID := r.e, r.taskID
	type outcome struct {
		d   architectureDecision
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		d, err := r.resolve(plan)
		done <- outcome{d, err}
	}()
	r.admitted, r.err, r.asked, r.events = architectureDecision{}, nil, false, nil
	deadline := time.After(90 * time.Second)
	for {
		select {
		case o := <-done:
			r.admitted, r.err = o.d, o.err
			history, err := e.Store.Load()
			if err != nil {
				t.Fatal(err)
			}
			for _, ev := range history {
				if ev.TaskID == taskID {
					r.events = append(r.events, ev)
				}
			}
			return r
		case <-deadline:
			t.Fatal("the supplied plan neither returned nor reached the authority rendezvous")
		default:
			e.mu.Lock()
			_, waiting := e.pending[taskID]
			e.mu.Unlock()
			if waiting && !r.asked {
				r.asked = true
				if !e.DeferAuthority(taskID) {
					t.Fatal("the deferral was refused")
				}
			}
			time.Sleep(time.Millisecond)
		}
	}
}

// f4bRefusalClass is the typed plan-admission class err carries, or "".
func f4bRefusalClass(err error) planAdmissionRefusalClass {
	var refusal *planAdmissionRefusal
	if errors.As(err, &refusal) {
		return refusal.class
	}
	return ""
}

// f4bRecord is the pending attempt's complete recorded test-edit state.
func f4bRecord(r f4bResult) (planAttempt, testEditRecord) {
	a := r.e.pendingPlanAttempt(r.taskID)
	_, rec := r.e.recordedGrants(r.taskID, a.ID)
	return a, rec
}

func f4bStatusContains(evs []event.Event, text string) bool {
	for _, ev := range evs {
		if ev.Kind == event.Status && strings.Contains(ev.Summary, text) {
			return true
		}
	}
	return false
}

// W1 -- THE SPECIMEN. The exact attempt-1 plan reaches the consequence-
// authority boundary with its own condition, not test_edit_admission.
func TestF4bW1SpecimenReachesTheConsequenceAuthorityBoundary(t *testing.T) {
	c := f4bCase{plan: f4bSpecimen(t), region: f4bRegion}
	human := f4bCondition(t, c)
	if !human.RequiresHuman() || human.Granted() || !strings.Contains(human.Condition, f4bOutward) {
		t.Fatalf("premise: the specimen's route is human-owned by its outward-action consequence (F4a still fires): %+v", human)
	}
	r := f4bRun(t, "task-f4b-w1", c)
	if class := f4bRefusalClass(r.err); class == refusalTestEditAdmission {
		t.Fatalf("the specimen was refused as %s, masking its human-owned route: %v", class, r.err)
	}
	if !r.asked || !errors.Is(r.err, errAuthorityDeferred) {
		t.Fatalf("the specimen did not reach the authority boundary: asked=%v err=%v", r.asked, r.err)
	}
	var q DeferredAuthority
	payloadOf(t, r.events, event.WorkflowAwaitingAuthority, &q)
	a, rec := f4bRecord(r)
	if q.Condition != human.Condition || !sameFiles(q.Scope, c.plan.Files) || q.PlanAttemptID != a.ID || a.ID == "" {
		t.Fatalf("the question asked is not the specimen's own route: condition %q scope %v attempt %q (pending %q)", q.Condition, q.Scope, q.PlanAttemptID, a.ID)
	}
	if !strings.Contains(q.Decision.Reason, human.Condition) {
		t.Fatalf("the human is not shown the condition that produced the question: %q", q.Decision.Reason)
	}
	// The uncollected instrument decided nothing: no refusal is on the record
	// and roles_test.go is simply ungranted, exactly as derived coverage left it.
	if hasKind(kindsOf(r.events), event.PlanAttemptRefused) {
		t.Fatal("a plan-attempt refusal was recorded for a plan whose route stopped the probe")
	}
	for _, g := range rec.Grants {
		if g.Path == f4bRolesTest || g.CoveringEvidence != evidenceDerived {
			t.Fatalf("a grant was recorded from the instrument the route did not consult: %+v", g)
		}
	}
	if len(rec.Grants) != 4 {
		t.Fatalf("premise: derived coverage grants the four workflow tests: %+v", rec.Grants)
	}
}

// W2 -- CONTROL (clause 8). On a GRANTED route a genuinely missing grant still
// refuses as test_edit_admission; with the authored governance present the
// same route records the authored grant and admits the plan.
func TestF4bW2AGrantedRouteWithAMissingGrantStillRefuses(t *testing.T) {
	c := f4bCase{plan: f4bBounded(t), region: f4bRegion}
	if pre := f4bCondition(t, c); !pre.Granted() {
		t.Fatalf("premise: the bounded plan takes the granted route: %+v", pre)
	}
	r := f4bRun(t, "task-f4b-w2", c)
	if f4bRefusalClass(r.err) != refusalTestEditAdmission || !strings.Contains(r.err.Error(), f4bRolesTest) || r.asked {
		t.Fatalf("a granted route's missing grant was not refused as test_edit_admission naming %s: asked=%v err=%v", f4bRolesTest, r.asked, r.err)
	}
	for _, te := range c.plan.TestEdits[:4] {
		if strings.Contains(r.err.Error(), te.Path) {
			t.Fatalf("the refusal names the granted %s: %v", te.Path, r.err)
		}
	}
	c.authored = true
	ok := f4bRun(t, "task-f4b-w2-authored", c)
	if ok.err != nil || ok.admitted.Plan != c.plan.Plan {
		t.Fatalf("the granted route with authored governance was not admitted: %v", ok.err)
	}
	if _, rec := f4bRecord(ok); !f4bHolds(rec, f4bRolesTest, evidenceAuthored) {
		t.Fatalf("the granted route did not record the authored grant: %+v", rec.Grants)
	}
}

// f4bHolds reports that rec holds exactly one grant for path, by evidence.
func f4bHolds(rec testEditRecord, path, evidence string) bool {
	n := 0
	for _, g := range rec.Grants {
		if g.Path == path {
			if g.CoveringEvidence != evidence {
				return false
			}
			n++
		}
	}
	return n == 1
}

// W3 -- AUTHORIZATION ALONE GRANTS NOTHING (clause 6). The human authorised the
// specimen's consequence, the answer was consumed, the probe found no
// governance beside roles_test.go, and the plan is refused as
// test_edit_admission after -- not instead of -- the authority answer.
func TestF4bW3HumanAuthorizationAloneGrantsNoTestEdit(t *testing.T) {
	r := f4bRun(t, "task-f4b-w3", f4bCase{plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Authorize})
	if r.asked {
		t.Fatal("an answered question was asked again")
	}
	if !f4bStatusContains(r.events, "proceeding on the human's earlier authorization for: ") {
		t.Fatalf("the recorded authorization was not consumed before admission decided: %v", r.err)
	}
	if f4bRefusalClass(r.err) != refusalTestEditAdmission || !strings.Contains(r.err.Error(), f4bRolesTest) || r.admitted.Plan != "" {
		t.Fatalf("an authorization alone admitted an ungoverned test edit: err=%v plan=%q", r.err, r.admitted.Plan)
	}
	a, rec := f4bRecord(r)
	if q := f4bQuestion(t, r.events); a.ID == "" || q.PlanAttemptID != a.ID {
		t.Fatalf("premise: the consumed answer is owned by the attempt it was asked about: question %q, routed %q", q.PlanAttemptID, a.ID)
	}
	if f4bHolds(rec, f4bRolesTest, evidenceAuthored) || f4bHolds(rec, f4bRolesTest, evidenceDerived) {
		t.Fatalf("a grant was recorded for %s with no governance beside it: %+v", f4bRolesTest, rec.Grants)
	}
}

// W4 -- AFTER AUTHORIZATION THE AUTHORED EVIDENCE IS RE-DERIVED (clause 5),
// at the same pinned world and for the same plan attempt, and the
// authored-governed test edit is granted. Closes the discarded map in
// afterHumanAuthorization.
func TestF4bW4AuthoredEvidenceIsRederivedAfterAuthorization(t *testing.T) {
	c := f4bCase{plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Authorize, authored: true}
	r := f4bRun(t, "task-f4b-w4", c)
	if r.err != nil || r.admitted.Plan != c.plan.Plan || r.asked {
		t.Fatalf("an authorised plan whose test edits are all governed was not admitted: asked=%v err=%v", r.asked, r.err)
	}
	a, rec := f4bRecord(r)
	if q := f4bQuestion(t, r.events); q.PlanAttemptID != a.ID {
		t.Fatalf("the authorization consumed was asked about attempt %q, not the admitted %q", q.PlanAttemptID, a.ID)
	}
	if rec.PlanAttemptID != a.ID || a.ID == "" || rec.World != r.w.world || a.World != r.w.world {
		t.Fatalf("the authored grant is not bound to the authorised attempt and pinned world: record %q@%s, attempt %q@%s, world %s",
			rec.PlanAttemptID, rec.World, a.ID, a.World, r.w.world)
	}
	if !f4bHolds(rec, f4bRolesTest, evidenceAuthored) {
		t.Fatalf("no authored grant was derived after authorization: %+v", rec.Grants)
	}
	for _, g := range rec.Grants {
		if g.World != r.w.world {
			t.Fatalf("a grant is bound to another world: %+v", g)
		}
	}
	if err := reconcileTestEditDeclarations(c.plan.TestEdits, c.plan.Files, a, rec); err != nil {
		t.Fatalf("the admitted attempt's record does not answer every declared test edit: %v", err)
	}
}

// W1, AFTER AUTHORIZATION -- THE RE-EVALUATED ROUTE IS NOT MASKED EITHER. The
// human authorised the specimen's consequence and the answer was consumed; the
// per-file probe that then runs reports verdict.go unexamined, opening a
// coverage gap, while roles_test.go -- governed only through verdict.go -- has
// no grant. The plan stops at the gap the re-evaluated route closes, never at
// test_edit_admission.
func TestF4bW1AfterAuthorizationACoverageGapIsNotMaskedByAMissingGrant(t *testing.T) {
	c := f4bCase{plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Authorize, gap: true}
	r := f4bRun(t, "task-f4b-w1-gap", c)
	if r.asked {
		t.Fatal("an answered question was asked again")
	}
	if !f4bStatusContains(r.events, "proceeding on the human's earlier authorization for: ") {
		t.Fatalf("the recorded authorization was not consumed before admission decided: %v", r.err)
	}
	if class := f4bRefusalClass(r.err); class == refusalTestEditAdmission || r.admitted.Plan != "" {
		t.Fatalf("the post-authorization coverage gap was masked as %s: plan=%q err=%v", class, r.admitted.Plan, r.err)
	}
	if f4bRefusalClass(r.err) != refusalSuppliedPlan || !strings.Contains(r.err.Error(), "a bounded knowledge gap must be closed first: ") ||
		!strings.Contains(r.err.Error(), f4bVerdict) {
		t.Fatalf("the plan did not stop at the coverage gap over %s the re-evaluated route closes: %v", f4bVerdict, r.err)
	}
	if _, rec := f4bRecord(r); f4bHolds(rec, f4bRolesTest, evidenceAuthored) || f4bHolds(rec, f4bRolesTest, evidenceDerived) {
		t.Fatalf("premise: %s holds no grant, so the gap is what a missing grant would have masked: %+v", f4bRolesTest, rec.Grants)
	}
	for _, ev := range r.events {
		if ev.Kind == event.PlanAttemptRefused && strings.Contains(ev.Summary, "existing-test edit admission") {
			t.Fatalf("a test-edit refusal was recorded for a plan whose operative route is a gap: %s", ev.Summary)
		}
	}
}

// W5 -- A REFUSED ROUTE KEEPS ITS ORIGINAL TYPED REFUSAL. An uncertifiable
// graph (cannot-establish) and a recorded decline are refused as themselves,
// never relabelled as a missing test-edit grant.
func TestF4bW5ARefusedRouteKeepsItsOriginalTypedRefusal(t *testing.T) {
	stale := strings.Replace(f4bRegion, "GRAPH_FRESHNESS_STATE_CURRENT", "GRAPH_FRESHNESS_STATE_STALE", 1)
	c := f4bCase{plan: f4bSpecimen(t), region: stale}
	pre := f4bCondition(t, c)
	if pre.Route != RouteCannotEstablish {
		t.Fatalf("premise: the stale region cannot establish authority: %+v", pre)
	}
	r := f4bRun(t, "task-f4b-w5-cannot", c)
	if f4bRefusalClass(r.err) != "" || r.err == nil || !strings.Contains(r.err.Error(), "cannot establish authority for this plan: "+pre.Condition) || r.asked {
		t.Fatalf("the cannot-establish route was not refused as itself: asked=%v err=%v", r.asked, r.err)
	}
	declined := f4bRun(t, "task-f4b-w5-declined", f4bCase{plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Decline})
	if f4bRefusalClass(declined.err) != refusalAuthorityDeclined || !strings.Contains(declined.err.Error(), f4bOutward) || declined.asked {
		t.Fatalf("the declined route was not refused as %s: asked=%v err=%v", refusalAuthorityDeclined, declined.asked, declined.err)
	}
	for _, res := range []f4bResult{r, declined} {
		if strings.Contains(res.err.Error(), "existing-test edit admission") {
			t.Fatalf("a refused route was relabelled as a test-edit refusal: %v", res.err)
		}
	}
}

// f4bCases is every W1-W5 path, as W6 replays them.
func f4bCases(t *testing.T) map[string]f4bCase {
	stale := strings.Replace(f4bRegion, "GRAPH_FRESHNESS_STATE_CURRENT", "GRAPH_FRESHNESS_STATE_STALE", 1)
	return map[string]f4bCase{
		"w1-specimen":           {plan: f4bSpecimen(t), region: f4bRegion},
		"w2-granted-missing":    {plan: f4bBounded(t), region: f4bRegion},
		"w2-granted-authored":   {plan: f4bBounded(t), region: f4bRegion, authored: true},
		"w3-authorized-missing": {plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Authorize},
		"w4-authorized-granted": {plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Authorize, authored: true},
		"w5-cannot-establish":   {plan: f4bSpecimen(t), region: stale},
		"w5-declined":           {plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Decline},
		"w1-authorized-gap":     {plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Authorize, gap: true},
		"w1-architect-gap":      {plan: f4bSpecimen(t), region: f4bRegion, gap: true, architect: true},
	}
}

// f4bExecuteScript is the Sensei MCP an executed F4b run starts: the workspace
// identity and an unscoped start gate that certify the run, and the scoped
// region and per-file probes of df30ProductionScript over the case's state.
const f4bExecuteScript = `
LC_ALL=C; export LC_ALL
git remote add origin https://github.com/globulario/sensei-code.git >/dev/null 2>&1
exclude=$(git rev-parse --git-path info/exclude)
grep -qx '/.sensei-code/' "$exclude" 2>/dev/null || printf '/.sensei-code/\n' >> "$exclude"
reply() { printf 'Content-Length: %d\r\n\r\n%s' "${#1}" "$1"; }
while :; do
	len=
	while IFS= read -r line; do
		line=$(printf %s "$line" | tr -d '\r')
		[ -z "$line" ] && break
		case "$line" in Content-Length:*) len=$(printf %s "${line#Content-Length:}" | tr -dc 0-9) ;; esac
	done
	[ -n "$len" ] || exit 0
	body=$(dd bs=1 count="$len" 2>/dev/null)
	case "$body" in '{"jsonrpc":"2.0","id":'*) ;; *) continue ;; esac
	rest=${body#'{"jsonrpc":"2.0","id":'}
	id=${rest%%,*}
	rest=${rest#*,}
	case "$rest" in
	'"method":"initialize"'*)
		result='{"protocolVersion":"2025-06-18","capabilities":{},"serverInfo":{"name":"f4b-stub","version":"0"}}' ;;
	*'"name":"sensei_workspace_status"'*)
		result='{"content":[{"type":"text","text":"composition_state: complete"}],"structuredContent":{"composition_state":"complete","binding":{"repository_domain":"github.com/globulario/sensei-code"}}}' ;;
	*'"name":"awareness_preflight"'*)
		case "$body" in
		*'"files":[]'*)
			result='{"content":[{"type":"text","text":"start"}],"structuredContent":{"status":"PREFLIGHT_STATUS_OK","risk_class":"LOW_RISK","authority":{"authoritative":true,"graph_freshness_state":"GRAPH_FRESHNESS_STATE_CURRENT","seed_state":"SEED_STATE_CURRENT","graph_build_commit":"fac399f8225f","source_repo_commit":"f56f5a305798"},"change_risk":{"blast_radius":"BLAST_RADIUS_LOCAL","approval_gate":"APPROVAL_GATE_NONE"},"coverage":{"direct_anchor_count":2,"file_count":1,"indexed_file_count":1,"sufficient":true}}}' ;;
		*)
			result=$(cat 'F4BSTATE/region.json')
			for kind in examined unexamined; do
				while IFS= read -r f; do
					[ -n "$f" ] || continue
					case "$body" in *"\"files\":[\"$f\"]"*) result=$(cat "F4BSTATE/$kind.json") ;; esac
				done < "F4BSTATE/$kind"
			done ;;
		esac ;;
	*)
		result='{"content":[{"type":"text","text":"nothing"}],"structuredContent":{}}' ;;
	esac
	reply "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":$result}"
done
`

// f4bExecuted is c driven through the production run: execute, from its start
// gate through admission to implement, with c's plan supplied and every
// provider turn answered by a counting runner (the supplied plan consults no
// architect, so every turn is a worker's). With an answer the run is executed
// twice, as route routes twice: once to ask and defer its own question, then,
// with the answer recorded for that question's attempt, again.
type f4bExecuted struct {
	r       f4bResult
	runner  *scriptedArchitect
	runners *fixedResolver
	events  []event.Event
}

func f4bExecute(t *testing.T, taskID string, c f4bCase) f4bExecuted {
	t.Helper()
	r := f4bWorldFor(t, taskID, c)
	e := r.e
	e.Config.Permissions.ReadRepository = true
	e.Config.Permissions.CreateWorktrees = true
	e.Config.Permissions.WriteCandidates = true
	e.Config.Workflow.ReviewCycles = 1
	e.Config.Sensei.Command = "sh"
	e.Config.Sensei.Args = []string{"-c", strings.ReplaceAll(f4bExecuteScript, "F4BSTATE", r.w.state)}
	e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "claude", "true", "none"
	e.Config.Implementors = append(e.Config.Implementors[:0], e.Config.Architect)
	turn, how, live := "nothing was changed", SubmittedWithSuppliedPlan, ""
	if c.architect {
		// Every turn is the architect's plan; an implementer reached by
		// mistake is counted all the same.
		plan, err := json.Marshal(c.plan)
		if err != nil {
			t.Fatal(err)
		}
		turn, how, live = string(plan), RequestedByHuman, "1"
	}
	turns := make([]architectTurn, 64)
	for i := range turns {
		turns[i] = architectTurn{text: turn}
	}
	x := f4bExecuted{r: r, runner: &scriptedArchitect{turns: turns}}
	x.runners = &fixedResolver{runner: x.runner, name: "claude"}
	e.Runners = x.runners
	if !c.architect {
		e.supplyPlan(taskID, SuppliedPlan{decision: c.plan, Digest: "f4b"})
	}
	run := func() gapLoopRun {
		return driveGapLoop(t, e, x.runner, r.w.world, taskID, live, func(ctx context.Context) {
			runRootedTask(ctx, e, taskID, f4bObjective, how)
		})
	}
	first := run()
	x.events = append(x.events, first.events...)
	if c.answer != "" {
		if n := x.implementerTurns(); n != 0 || len(x.runner.prompts) != 0 {
			t.Fatalf("a deferred run resolved %d implementer(s) and invoked %d provider turn(s) before its question was answered:\n%s", n, len(x.runner.prompts), gapLoopTrace(first.events))
		}
		if !hasKind(kindsOf(first.events), event.WorkflowAwaitingAuthority) {
			t.Fatalf("premise: an answer is recorded only for a question the executed plan asked and deferred:\n%s", gapLoopTrace(first.events))
		}
		r.answer(t, f4bQuestion(t, first.events), c.answer)
		x.events = append(x.events, run().events...)
	}
	return x
}

// implementerTurns is how many implementer invocations the run resolved.
func (x f4bExecuted) implementerTurns() int {
	n := 0
	for _, spec := range x.runners.specs {
		if string(spec.Role) == "implementer" {
			n++
		}
	}
	return n
}

// W6 -- NOTHING IS EXECUTED BEFORE COMPLETE ADMISSION (clause 7). Every W1-W5
// path is driven through the production run (execute) with an instrumented
// worker. A path admission refuses or defers resolves no implementer, invokes
// no provider, cuts no candidate, mutates nothing and publishes nothing. A
// path admission admits reaches the implementer only after its attempt's
// recorded test-edit state answers every declared test edit: the first
// implementer prompt already carries every declared test edit's grant.
func TestF4bW6NothingExecutesBeforeCompleteAdmission(t *testing.T) {
	admits := map[string]bool{"w2-granted-authored": true, "w4-authorized-granted": true}
	// Each refused or deferred path ends at its own admission outcome, so a
	// run that stopped earlier -- at the start gate, say -- proves nothing.
	ends := map[string]struct {
		kind event.Kind
		says string
	}{
		"w1-specimen":           {event.WorkflowAwaitingAuthority, "authority decision deferred"},
		"w2-granted-missing":    {event.WorkflowFailed, "existing-test edit admission refused before implementation: test edit refuted: the candidate would mutate the existing test " + f4bRolesTest},
		"w3-authorized-missing": {event.WorkflowFailed, "existing-test edit admission refused before implementation: test edit refuted: the candidate would mutate the existing test " + f4bRolesTest},
		"w5-cannot-establish":   {event.WorkflowFailed, "cannot establish authority for this plan: "},
		"w5-declined":           {event.WorkflowFailed, "the human declined this architectural change and the plan still requires it: "},
		"w1-authorized-gap":     {event.WorkflowFailed, "a bounded knowledge gap must be closed first: "},
		// The architect's loop disposes of the same post-authorization gap as
		// the knowledge limit it is, not as a missing grant.
		"w1-architect-gap": {event.WorkflowFailed, "cannot be governed further without knowledge the graph does not hold: graph coverage is absent for planned file(s) the graph has not examined: " + f4bVerdict},
	}
	for name, c := range f4bCases(t) {
		t.Run(name, func(t *testing.T) {
			x := f4bExecute(t, "task-f4b-w6-"+name, c)
			r, kinds := x.r, kindsOf(x.events)
			ctx := t.Context()
			if head, err := r.e.Repo.Head(ctx); err != nil || head != r.w.world {
				t.Fatalf("the pinned world moved: %s %v", head, err)
			}
			if diff, err := r.e.Repo.Diff(ctx, r.w.world); err != nil || diff != "" {
				t.Fatalf("a tracked file was mutated: %v\n%s", err, diff)
			}
			if hasKind(kinds, event.PullRequestOpened) {
				t.Fatal("a pull request was opened")
			}
			if !admits[name] {
				// The architect's own turns are not implementation; a supplied
				// plan consults no architect, so there every turn is a worker's.
				if n := x.implementerTurns(); n != 0 || (!c.architect && len(x.runner.prompts) != 0) {
					t.Fatalf("a run admission did not admit resolved %d implementer(s) and invoked %d provider turn(s):\n%s", n, len(x.runner.prompts), gapLoopTrace(x.events))
				}
				for _, k := range []event.Kind{event.AgentStarted, event.AgentFinished, event.CandidateChanged, event.CandidateResolved,
					event.CandidateAudited, event.ChangeReported, event.ValidationRun, event.PlanProposed, event.CheckpointCommitted, event.WorkflowCompleted} {
					if hasKind(kinds, k) {
						t.Fatalf("%s was emitted for a plan admission did not admit:\n%s", k, gapLoopTrace(x.events))
					}
				}
				if cut, err := r.e.Repo.RevList(ctx, r.e.Repo.WorktreeBranch(r.taskID), 1); err == nil || len(cut) != 0 {
					t.Fatalf("a candidate branch was cut for a plan admission did not admit: %v", cut)
				}
				end, last := ends[name], event.Event{}
				for _, ev := range x.events {
					if ev.Kind == event.WorkflowAwaitingAuthority || ev.Kind == event.WorkflowFailed {
						last = ev
					}
				}
				if !hasKind(kinds, event.PlanAttemptStarted) || last.Kind != end.kind || !strings.Contains(last.Summary, end.says) {
					t.Fatalf("the run did not end at its admission outcome %s %q:\n%s", end.kind, end.says, gapLoopTrace(x.events))
				}
				return
			}
			// The admitted control: implementation is reachable, and only
			// after admission completed.
			if x.implementerTurns() == 0 || len(x.runner.prompts) == 0 {
				t.Fatalf("an admitted plan never reached the implementer:\n%s", gapLoopTrace(x.events))
			}
			if !hasKind(kinds, event.PlanProposed) {
				t.Fatal("the implementer ran before the plan became operative")
			}
			a := r.e.operativePlanAttempt(r.taskID)
			_, rec := r.e.recordedGrants(r.taskID, a.ID)
			if err := reconcileTestEditDeclarations(c.plan.TestEdits, c.plan.Files, a, rec); err != nil || a.ID == "" {
				t.Fatalf("the implementer ran for attempt %q whose record does not answer every declared test edit: %v", a.ID, err)
			}
			if !f4bHolds(rec, f4bRolesTest, evidenceAuthored) || len(rec.Grants) != len(c.plan.TestEdits) {
				t.Fatalf("premise: the admitted record grants every declared test edit, %s by its authored governance: %+v", f4bRolesTest, rec.Grants)
			}
			// The grants the first implementer was handed are that complete
			// record: it was invoked only once admission had recorded it.
			if granted := renderTestEditGrants(r.e.testEditGrants(r.taskID)); granted == "" || !strings.Contains(x.runner.prompts[0], granted) {
				t.Fatalf("the first implementer was not handed the complete test-edit grants:\n%s", granted)
			}
		})
	}
}

// W7 -- INTERRUPTED RESTORATION PRESERVES THE OPERATIVE PLAN. The specimen's
// deferred question names its own attempt; the answer to it is owned by that
// attempt and authorizes no other plan, however alike; the authorization
// admits that same attempt, never a substitute; and a restore refuses a plan
// binding that does not reproduce its attempt, and a test-edit record from
// another attempt or another world, for exactly that reason.
func TestF4bW7InterruptedRestorationPreservesTheOperativePlan(t *testing.T) {
	// One task, one pinned world: the question is deferred, then answered for
	// its own attempt, and plans are routed again under the answer.
	c := f4bCase{plan: f4bSpecimen(t), region: f4bRegion, authored: true}
	deferred := f4bWorldFor(t, "task-f4b-w7", c).route(t, c)
	if !deferred.asked || !errors.Is(deferred.err, errAuthorityDeferred) {
		t.Fatalf("premise: the specimen's question is deferred: %v", deferred.err)
	}
	q := f4bQuestion(t, deferred.events)
	asked, _ := f4bRecord(deferred)
	if q.PlanAttemptID != asked.ID {
		t.Fatalf("premise: the question names the attempt it was asked about: %q, pending %q", q.PlanAttemptID, asked.ID)
	}
	deferred.answer(t, q, authority.Authorize)

	// A materially different plan -- same consequence condition, same file
	// scope, same test-edit declarations -- is its own attempt, and the answer
	// owned by the specimen's attempt does not authorize it: it is asked anew.
	substitute := f4bSpecimen(t)
	substitute.Plan += " (a different plan under the same condition)"
	substitute.Steps = append(append([]string(nil), substitute.Steps...), "Also rewrite every remaining fixture before review.")
	if sub := f4bCondition(t, f4bCase{plan: substitute, region: f4bRegion}); sub.Condition != q.Condition || !sameFiles(substitute.Files, q.Scope) {
		t.Fatalf("premise: the substitute reaches the same condition over the same scope: %+v", sub)
	}
	other := deferred.routeOnce(t, substitute)
	reasked := f4bQuestion(t, other.events)
	subAttempt, _ := f4bRecord(other)
	if !other.asked || !errors.Is(other.err, errAuthorityDeferred) ||
		subAttempt.ID == q.PlanAttemptID || reasked.PlanAttemptID != subAttempt.ID {
		t.Fatalf("the specimen's authorization was consumed by a different plan: asked=%v err=%v attempt %q (answered %q)",
			other.asked, other.err, subAttempt.ID, q.PlanAttemptID)
	}

	// The specimen itself, routed again, consumes its own answer.
	r := other.routeOnce(t, c.plan)
	if r.err != nil || r.asked {
		t.Fatalf("premise: the authorised specimen is admitted without asking again: asked=%v err=%v", r.asked, r.err)
	}
	a, _ := f4bRecord(r)
	if a.ID != asked.ID || q.PlanAttemptID != a.ID {
		t.Fatalf("the authorization admitted a different plan attempt than the question was asked about: asked %q, question %q, admitted %q", asked.ID, q.PlanAttemptID, a.ID)
	}
	if strings.Join(a.Plan.Steps, "\n") != strings.Join(c.plan.Steps, "\n") || !sameFiles(a.Plan.Files, c.plan.Files) || a.Plan.Plan != c.plan.Plan {
		t.Fatal("the admitted attempt is not the specimen plan")
	}
	if _, err := r.e.adoptPlanAttempt(fixtureCtx(r.e, r.taskID), r.taskID, f4bObjective, r.admitted); err != nil {
		t.Fatal(err)
	}
	history, err := r.e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	var task session.Interrupted
	for _, it := range session.FindInterrupted(history) {
		if it.TaskID == r.taskID {
			task = it
		}
	}
	if task.PlanAttemptID != a.ID {
		t.Fatalf("the interrupted record does not name the admitted attempt: %q", task.PlanAttemptID)
	}
	// A tampered plan binding is refused, and nothing is installed in its
	// place: neither a plan record whose plan no longer reproduces its
	// attempt, nor one renamed to the substitute plan's attempt.
	unbound := func(name string, task session.Interrupted, mutate func(map[string]any)) {
		t.Helper()
		var plan map[string]any
		if err := json.Unmarshal(task.PlanRecord, &plan); err != nil || plan["plan_attempt_id"] != a.ID {
			t.Fatalf("premise: the interrupted plan record is the admitted attempt's: %v %v", err, plan["plan_attempt_id"])
		}
		mutate(plan)
		var err error
		if task.PlanRecord, err = json.Marshal(plan); err != nil {
			t.Fatal(err)
		}
		fresh := &Engine{}
		err = fresh.restorePlanAttempt(task, r.w.world)
		var refusal *RestorationRefusal
		if !errors.As(err, &refusal) || refusal.Binding != RestorationPlanAttemptUnbound {
			t.Fatalf("%s: restoration was not refused as %s: %v", name, RestorationPlanAttemptUnbound, err)
		}
		if op, pending := fresh.operativePlanAttempt(task.TaskID), fresh.pendingPlanAttempt(task.TaskID); op.ID != "" || pending.ID != "" {
			t.Fatalf("%s: a refused restoration installed attempt %q (pending %q)", name, op.ID, pending.ID)
		}
	}
	unbound("substituted plan", task, func(plan map[string]any) { plan["steps"] = substitute.Steps; plan["plan"] = substitute.Plan })
	renamed := task
	renamed.PlanAttemptID = subAttempt.ID
	unbound("renamed attempt", renamed, func(plan map[string]any) { plan["plan_attempt_id"] = subAttempt.ID })

	refusedAs := func(name string, task session.Interrupted, world, binding string) {
		t.Helper()
		fresh := &Engine{}
		if err := fresh.restorePlanAttempt(task, r.w.world); err != nil {
			t.Fatalf("%s: the operative attempt was not restored: %v", name, err)
		}
		if op := fresh.operativePlanAttempt(task.TaskID); op.ID != a.ID {
			t.Fatalf("%s: restoration substituted attempt %q for %q", name, op.ID, a.ID)
		}
		err := fresh.restoreTestEditGrants(task, nil, c.plan.Files, world)
		var refusal *RestorationRefusal
		if !errors.As(err, &refusal) || refusal.Binding != binding {
			t.Fatalf("%s: restoration was not refused as %s: %v", name, binding, err)
		}
		if len(fresh.testEditGrants(task.TaskID)) != 0 {
			t.Fatalf("%s: a refused restoration installed grants: %+v", name, fresh.testEditGrants(task.TaskID))
		}
	}
	// A record from another world.
	refusedAs("other world", task, "0000000000000000000000000000000000000000", RestorationWorldMismatch)
	// A record bound to another plan attempt.
	stale := task
	var raw map[string]any
	if err := json.Unmarshal(task.TestEditRecord, &raw); err != nil || raw["plan_attempt_id"] != a.ID {
		t.Fatalf("premise: the interrupted test-edit record is the admitted attempt's: %v %v", err, raw["plan_attempt_id"])
	}
	raw["plan_attempt_id"] = deferred.taskID + "-another-attempt"
	if stale.TestEditRecord, err = json.Marshal(raw); err != nil {
		t.Fatal(err)
	}
	refusedAs("other attempt", stale, r.w.world, RestorationPlanAttemptUnbound)
	// The intact record holds AUTHORED authority this resume cannot verify:
	// refused as that, never installed as the DERIVED subset.
	refusedAs("intact", task, r.w.world, RestorationAuthoredUnverifiable)
}

// F4b r2 -- A LATER REFUSAL KEEPS ITS CONTINUATION (RULING-261). A
// post-authorization test-edit refusal of an ARCHITECT'S plan is the same typed
// refusal a routePlan refusal is, and takes the same one bounded architect
// revision through continueAfterAdmissionRefusal; a supplied plan has no
// architect to revise it, and terminates.

// f4bRevised is the specimen revised the way an architect answering the
// refusal might: a different plan, still human-owned by the same step 0, over
// the same files and test-edit declarations. It is its own plan attempt.
func f4bRevised(t *testing.T) architectureDecision {
	d := f4bSpecimen(t)
	d.Plan += " (revised after the test-edit refusal)"
	d.Steps = append(append([]string(nil), d.Steps...), "Keep roles_test.go unchanged unless a grant is recorded for it.")
	return d
}

// f4bContinuation drives the production run (execute) for an architect whose
// turns are script in order (the last repeated). The first human question is
// answered Authorize live -- recorded by production for the attempt it was
// asked about -- and every later one is deferred, so the run always ends. It
// ends at any run terminal, a plan-admission park included.
func f4bContinuation(t *testing.T, taskID string, script ...architectureDecision) (f4bExecuted, []string) {
	t.Helper()
	r := f4bWorldFor(t, taskID, f4bCase{plan: script[0], region: f4bRegion})
	e := r.e
	e.Config.Permissions.ReadRepository = true
	e.Config.Permissions.CreateWorktrees = true
	e.Config.Permissions.WriteCandidates = true
	e.Config.Workflow.ReviewCycles = 1
	e.Config.Sensei.Command = "sh"
	e.Config.Sensei.Args = []string{"-c", strings.ReplaceAll(f4bExecuteScript, "F4BSTATE", r.w.state)}
	e.Config.Architect.Name, e.Config.Architect.Command, e.Config.Architect.Graph = "claude", "true", "none"
	e.Config.Implementors = append(e.Config.Implementors[:0], e.Config.Architect)
	turns := make([]architectTurn, 0, 64)
	for len(turns) < 64 {
		d := script[min(len(turns), len(script)-1)]
		plan, err := json.Marshal(d)
		if err != nil {
			t.Fatal(err)
		}
		turns = append(turns, architectTurn{text: string(plan)})
	}
	x := f4bExecuted{r: r, runner: &scriptedArchitect{turns: turns}}
	x.runners = &fixedResolver{runner: x.runner, name: "claude"}
	e.Runners = x.runners

	ch, cancel := e.Bus.Subscribe(8192)
	defer cancel()
	ctx, stop := context.WithCancel(context.Background())
	defer stop()
	go runRootedTask(ctx, e, taskID, f4bObjective, RequestedByHuman)
	asked := 0
	timeout := time.After(90 * time.Second)
	for {
		select {
		case ev := <-ch:
			x.events = append(x.events, ev)
			if ev.Kind == event.AuthorityRequired {
				asked++
				waitForPending(t, e, taskID)
				if asked == 1 {
					e.ResolveHuman(taskID, "1")
				} else {
					e.DeferAuthority(taskID)
				}
			}
			if _, ok := event.RunTerminality(ev.Kind); ok {
				settle := time.After(300 * time.Millisecond)
				for {
					select {
					case ev := <-ch:
						x.events = append(x.events, ev)
					case <-settle:
						return x, append([]string(nil), x.runner.prompts...)
					}
				}
			}
		case <-timeout:
			stop()
			t.Fatalf("the governed run did not end:\n%s", gapLoopTrace(x.events))
		}
	}
}

// f4bAuthorized are the plan attempts the human's recorded answers are owned
// by, in order.
func f4bAuthorized(t *testing.T, evs []event.Event) []string {
	t.Helper()
	var out []string
	for _, ev := range evs {
		if ev.Kind != event.AuthorityResolved {
			continue
		}
		var res resolvedAuthority
		if err := json.Unmarshal(ev.Payload, &res); err != nil {
			t.Fatal(err)
		}
		out = append(out, res.PlanAttemptID)
	}
	return out
}

// f4bAsked is how many authority questions the run put to the human.
func f4bAsked(evs []event.Event) int {
	n := 0
	for _, ev := range evs {
		if ev.Kind == event.AuthorityRequired {
			n++
		}
	}
	return n
}

// f4bDescribe names recorded refusals by attempt, class and continuation.
func f4bDescribe(refusals ...planAttemptRefusal) string {
	out := make([]string, 0, len(refusals))
	for _, r := range refusals {
		out = append(out, "attempt "+short12(r.PlanAttemptID)+" class "+string(r.Class)+" continuation "+orNone(r.Continuation, "(none)")+": "+r.Reason)
	}
	return "[" + strings.Join(out, "; ") + "]"
}

// W8 -- CONTINUATION. The architect's human-owned specimen is authorised, and
// its post-authorization test edit stays ungranted: the refusal is typed
// test_edit_admission, recorded as returned to the architect, and shown to it
// verbatim; the one revision is a new plan attempt, routed and asked about
// anew -- the old authorization is not inherited -- and nothing is implemented.
// On the r1 shape (the refusal returned directly) the run fails with no
// architect revision; at the base the specimen is refused before it is asked
// about at all.
func TestF4bW8APostAuthorizationRefusalReturnsToTheArchitectOnce(t *testing.T) {
	const task = "task-f4b-w8"
	x, prompts := f4bContinuation(t, task, f4bSpecimen(t), f4bSpecimen(t), f4bRevised(t))
	kinds := kindsOf(x.events)
	attempts := startedAttempts(t, x.events)
	authorized := f4bAuthorized(t, x.events)
	if len(authorized) != 1 || len(attempts) == 0 || authorized[0] != attempts[0] {
		t.Fatalf("the specimen was not asked about its own route before any refusal (masked at routing): authorized %v attempts %v\n%s",
			authorized, attempts, gapLoopTrace(x.events))
	}
	specimen := authorized[0]
	if !f4bStatusContains(x.events, "proceeding on the human's earlier authorization for: ") {
		t.Fatalf("premise: the specimen's authorization was consumed:\n%s", gapLoopTrace(x.events))
	}
	refusals := refusalsIn(t, x.events)
	if len(refusals) != 1 {
		t.Fatalf("want exactly one recorded refusal, got %s\n%s", f4bDescribe(refusals...), gapLoopTrace(x.events))
	}
	ref := refusals[0]
	if ref.Class != refusalTestEditAdmission || ref.PlanAttemptID != specimen || !strings.Contains(ref.Reason, f4bRolesTest) ||
		ref.Continuation != session.PlanAdmissionContinuationArchitectTurn {
		t.Fatalf("the post-authorization refusal is not the specimen's typed test_edit_admission refusal returned to the architect: %s", f4bDescribe(ref))
	}
	// Exactly one bounded revision: the refusal reached the third turn, verbatim.
	if len(prompts) != 3 || !strings.Contains(prompts[2], planAdmissionRefusedMarker) || !strings.Contains(prompts[2], ref.RefusalID) {
		t.Fatalf("the refusal was not returned to the architect as its one revision (%d prompts):\n%s", len(prompts), gapLoopTrace(x.events))
	}
	if strings.Contains(prompts[1], planAdmissionRefusedMarker) {
		t.Fatal("a refusal reached the architect before the authorised plan was admitted")
	}
	// The revision is its own attempt, and the specimen's answer does not
	// authorize it: it is asked about anew, and that question is deferred.
	if len(attempts) < 2 || attempts[len(attempts)-1] == specimen {
		t.Fatalf("the revision did not receive a new plan attempt: %v", attempts)
	}
	revised := attempts[len(attempts)-1]
	if f4bAsked(x.events) != 2 || !hasKind(kinds, event.WorkflowAwaitingAuthority) || f4bQuestion(t, x.events).PlanAttemptID != revised {
		t.Fatalf("the revised plan inherited the specimen's authorization, or was not asked about as itself (revised %q):\n%s", revised, gapLoopTrace(x.events))
	}
	if hasKind(kinds, event.WorkflowFailed) || hasKind(kinds, event.WorkflowPlanAdmissionRefused) {
		t.Fatalf("the run did not end at the revised plan's own authority question:\n%s", gapLoopTrace(x.events))
	}
	// Grants were recomputed for the revision, under its own attempt and world.
	_, rec := x.r.e.recordedGrants(task, revised)
	if rec.PlanAttemptID != revised || rec.World != x.r.w.world || len(rec.Grants) != 4 || f4bHolds(rec, f4bRolesTest, evidenceAuthored) {
		t.Fatalf("the revision's grant state was not derived afresh for it: %+v", rec)
	}
	if n := x.implementerTurns(); n != 0 || hasKind(kinds, event.PlanProposed) || implementerStartedBefore(x.events, len(x.events)) {
		t.Fatalf("implementation was reached before a new admission (%d implementer(s)):\n%s", n, gapLoopTrace(x.events))
	}
}

// W8b -- NEGATIVE CONTROL: THE BUDGET IS ONE REVISION. The architect answers
// the refusal with the same specimen: the same attempt, refused again under
// the same identity, parks as PlanAdmissionRefused -- resumable, no
// implementer -- and is never handed back for a further revision.
func TestF4bW8bARecurringPostAuthorizationRefusalParks(t *testing.T) {
	const task = "task-f4b-w8b"
	x, prompts := f4bContinuation(t, task, f4bSpecimen(t))
	kinds := kindsOf(x.events)
	refusals := refusalsIn(t, x.events)
	if len(refusals) != 1 || refusals[0].Class != refusalTestEditAdmission || refusals[0].Continuation != session.PlanAdmissionContinuationArchitectTurn {
		t.Fatalf("premise: the post-authorization refusal was returned to the architect once: %s\n%s", f4bDescribe(refusals...), gapLoopTrace(x.events))
	}
	if len(prompts) != 3 {
		t.Fatalf("the recurring refusal was not bounded to one revision: %d architect turn(s)\n%s", len(prompts), gapLoopTrace(x.events))
	}
	at := indexOfKind(x.events, event.WorkflowPlanAdmissionRefused)
	if at < 0 || hasKind(kinds, event.WorkflowFailed) {
		t.Fatalf("the recurring refusal did not park as PlanAdmissionRefused:\n%s", gapLoopTrace(x.events))
	}
	var parked PlanAdmissionRefused
	if err := json.Unmarshal(x.events[at].Payload, &parked.planAttemptRefusal); err != nil ||
		parked.RefusalID != refusals[0].RefusalID || parked.PlanAttemptID != refusals[0].PlanAttemptID {
		t.Fatalf("the park does not name the recurring refusal: %+v (%v)", parked.planAttemptRefusal, err)
	}
	if n := f4bAsked(x.events); n != 1 {
		t.Fatalf("the human was asked %d times about one attempt", n)
	}
	if n := x.implementerTurns(); n != 0 || hasKind(kinds, event.PlanProposed) || implementerStartedBefore(x.events, len(x.events)) {
		t.Fatalf("an implementer was reached under a parked refusal (%d):\n%s", n, gapLoopTrace(x.events))
	}
}

// W8c -- SUPPLIED-PLAN NEGATIVE. The same post-authorization ungranted test
// edit on a SUPPLIED plan terminates with its typed test_edit_admission
// refusal: no architect turn, no continuation recorded, no widened bound.
func TestF4bW8cASuppliedPlanRefusalAfterAuthorizationTerminates(t *testing.T) {
	c := f4bCase{plan: f4bSpecimen(t), region: f4bRegion, answer: authority.Authorize}
	x := f4bExecute(t, "task-f4b-w8c", c)
	kinds := kindsOf(x.events)
	refusals := refusalsIn(t, x.events)
	if len(refusals) != 1 || refusals[0].Class != refusalTestEditAdmission || !strings.Contains(refusals[0].Reason, f4bRolesTest) {
		t.Fatalf("the supplied plan's post-authorization refusal is not typed test_edit_admission: %s\n%s", f4bDescribe(refusals...), gapLoopTrace(x.events))
	}
	if refusals[0].Continuation == session.PlanAdmissionContinuationArchitectTurn {
		t.Fatalf("a supplied plan's refusal was recorded as returned to an architect: %s", f4bDescribe(refusals[0]))
	}
	for _, spec := range x.runners.specs {
		if string(spec.Role) == "architect" {
			t.Fatal("an architect was resolved for a supplied plan")
		}
	}
	if len(x.runner.prompts) != 0 || x.implementerTurns() != 0 {
		t.Fatalf("a provider turn was invoked for a refused supplied plan: %d\n%s", len(x.runner.prompts), gapLoopTrace(x.events))
	}
	failed := indexOfKind(x.events, event.WorkflowFailed)
	if hasKind(kinds, event.WorkflowPlanAdmissionRefused) || hasKind(kinds, event.PlanProposed) || failed < 0 ||
		!strings.Contains(x.events[failed].Summary, "existing-test edit admission refused before implementation") {
		t.Fatalf("the supplied plan did not terminate with its typed refusal:\n%s", gapLoopTrace(x.events))
	}
	// Executed twice -- asked and deferred, then answered -- and both times
	// the one supplied attempt; the refusal is that attempt's.
	for _, id := range startedAttempts(t, x.events) {
		if id != refusals[0].PlanAttemptID {
			t.Fatalf("the supplied bound was revised: attempt %q beside the refused %q", id, refusals[0].PlanAttemptID)
		}
	}
}

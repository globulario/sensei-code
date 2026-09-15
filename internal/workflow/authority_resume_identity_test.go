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
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}

	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(context.Background(), nil, taskID, scopedCondition, "github.com/globulario/sensei-code",
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
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}

	type res struct {
		choice string
		err    error
	}
	out := make(chan res, 1)
	go func() {
		choice, err := e.awaitChoice(context.Background(), nil, taskID, q.Condition, q.Domain, q.BaseSHA,
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
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}

	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(context.Background(), nil, "task-1", q.Condition, q.Domain, q.BaseSHA,
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
			e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}
			e.terminateAuthorityOutcome(context.Background(), "task-1", "the objective", tc.err)
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
		e := &Engine{Bus: event.NewBus(), SessionID: "s1", pending: map[string]chan string{}}
		e.Config.Behavioral = behavioral.Config{Enabled: true, URL: srv.URL, Project: "sensei-code", Domain: "d"}
		return e
	}

	newEngine().terminateAuthorityOutcome(context.Background(), "task-1", "the objective", errStoppedByHumanAuthority)
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
	newEngine().terminateAuthorityOutcome(context.Background(), "task-1", "the objective", errors.New("the worker died"))
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
	for _, fn := range []string{"execute", "resumeAuthority"} {
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
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}
	// No Repo, no Config.Sensei: if the guard ran late this would fail on
	// starting Sensei instead, with a message naming the wrong thing.
	e.resumeAuthority(context.Background(), session.Interrupted{
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
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}
	e.resumeAuthority(context.Background(), session.Interrupted{
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
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}
	e.noteUnprovenAuthorityScope(taskID)

	errc := make(chan error, 1)
	go func() {
		_, err := e.awaitChoice(context.Background(), nil, taskID, scopedCondition,
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
	e := &Engine{Bus: bus, SessionID: "s1", pending: map[string]chan string{}}
	e.terminateAuthorityOutcome(context.Background(), "task-legacy", "the objective", err)
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

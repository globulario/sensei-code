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
	// execute and Resume end through terminateRun, and terminateRun through
	// the authority classifier. Resume used to keep a closure of its own that
	// reported every ending as FAILED, which turned a deferred question raised
	// during a resumed re-plan into a final failure (#194 review at b23a8ae).
	for _, fn := range []string{"execute", "Resume"} {
		if !strings.Contains(funcBody(t, "internal/workflow/engine.go", fn), "terminateRun") {
			t.Errorf("%s does not end through the shared run classifier", fn)
		}
	}
	if strings.Contains(funcBody(t, "internal/workflow/engine.go", "Resume"), "event.WorkflowFailed") {
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
	// The creation is durable, as it is for every real task: the objective
	// the continuation carries is read from it, not from the Interrupted value.
	store := sessionStore(t)
	task := session.Interrupted{TaskID: "task-1789848074761930104", Task: "repair the resume path"}
	if err := store.Append(event.New("s1", task.TaskID, event.SourceUser, event.TaskCreated, task.Task, nil)); err != nil {
		t.Fatal(err)
	}
	e := &Engine{Bus: bus, Store: store, SessionID: "s1", pending: map[string]chan string{}}
	e.resumeUnplannedArchitecture(context.Background(), task)

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
	// The objective is the recorded one, under the resumption's own provenance:
	// a restarted process establishes no human.
	if got := e.objective(task.TaskID); got.Text != task.Task || got.Provenance != ResumedGoverned {
		t.Fatalf("the recorded objective was not carried: %+v", got)
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
	resumed := event.NewBus()
	ch, stop := resumed.Subscribe(64)
	defer stop()
	r := &Engine{Bus: resumed, Store: store, SessionID: "s1", pending: map[string]chan string{}}
	if got := r.Resume(context.Background(), task); got != task.TaskID {
		t.Fatalf("Resume continued %q instead of the task it was given", got)
	}
	deadline := time.After(20 * time.Second)
	for {
		select {
		case ev := <-ch:
			if ev.Kind == event.Status && strings.Contains(ev.Summary, "the turn it is owed") {
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
	recorded := authority.Resolution{
		TaskID: "task-1", SessionID: "s1", Question: "Architectural authority reached a human-owned boundary.",
		Condition: scopedCondition, OptionID: "1", OptionLabel: "Authorize the architectural change described above",
		Scope: planScope(), Outcome: authority.Authorize, DecidedAt: time.Now().UTC(),
	}
	if err := store.Append(event.New("s1", "task-1", event.SourceUser, event.AuthorityResolved,
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
// The history is written by one engine and resumed by a FRESH one over the same
// durable session record, holding no objective in memory: the identity it
// carries can only have been recovered from the recorded TaskCreated.
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
		event.New("s1", taskID, event.SourceSystem, event.WorkflowAwaitingAuthority, q.Condition, q),
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

	repo, _ := mintRepo(t)
	// Outside the repository, so the record does not dirty the canonical
	// checkout the resume re-certifies.
	record := t.TempDir()
	store, err := session.New(record, "s1")
	if err != nil {
		t.Fatal(err)
	}
	creator := &Engine{Store: store, SessionID: "s1"}
	for _, ev := range history {
		creator.emit(ev)
	}
	// A restart: a new store object over the same record, a new engine, and
	// nothing carried in memory.
	reopened, err := session.New(record, "s1")
	if err != nil {
		t.Fatal(err)
	}
	bus := event.NewBus()
	events, cancel := bus.Subscribe(1024)
	defer cancel()
	capture := architectCapture{specs: make(chan RunnerSpec, 1)}
	e := &Engine{Repo: repo, Bus: bus, Store: reopened, SessionID: "s1", pending: map[string]chan string{}, Runners: capture}
	if len(e.objectives) != 0 {
		t.Fatalf("the fresh engine was handed an objective in memory: %+v", e.objectives)
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
			if got.ObjectiveDigest != wantDigest {
				t.Fatalf("the resumed architect request carries an objective identity not derived from the recorded bytes: %s", got.ObjectiveDigest)
			}
			// Restored under the resumption's own provenance: nothing here
			// establishes that a person asked.
			if o := e.objective(taskID); o.Text != objective || o.Provenance != ResumedGoverned || o.HumanAuthorized() {
				t.Fatalf("the restored objective claims provenance it does not have: %+v", o)
			}
			// The source is the durable creation, not the Interrupted value the
			// resume was handed: an engine given only the record, and no
			// continuation at all, reads the same bytes through the accessor.
			recovered, err := (&Engine{Store: reopened, SessionID: "s1"}).objectiveRecord(taskID)
			if err != nil || recovered.Text != objective || recovered.Provenance != ResumedGoverned {
				t.Fatalf("the objective was not recovered from the recorded TaskCreated: %+v (err %v)", recovered, err)
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
	go start(ctx)
	run := gapLoopRun{engine: e, world: world}
	asked := 0
	timeout := time.After(90 * time.Second)
	for {
		select {
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
func runGapLoop(t *testing.T, store *session.Store, taskID string, turns ...string) gapLoopRun {
	t.Helper()
	e, architect, world := newGapLoopEngine(t, nil, store, turns...)
	return driveGapLoop(t, e, architect, world, taskID, "", func(ctx context.Context) {
		e.run(ctx, taskID, "change main.go", RequestedByHuman)
	})
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
	condition := "a bounded knowledge gap was not closed by investigation: " +
		"the plan rests on an unverified premise about main.go: main has no callers"
	other := GapIdentity{Kind: "coverage-absent", Scope: []string{"main.go"}}
	prior := resolvedAuthority{Resolution: authority.Resolution{
		TaskID: taskID, SessionID: "s1", Question: "May main.go change?", Condition: condition,
		OptionID: "1", OptionLabel: "Authorize the architectural change described above",
		Scope: []string{"main.go"}, Outcome: authority.Authorize, DecidedAt: time.Now().UTC(),
	}, Gap: &other}
	if err := store.Append(event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, prior.OptionLabel, prior)); err != nil {
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
	gap := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: world}
	// Text history: the gap's own human question, answered -- about another identity.
	asked := "a bounded knowledge gap was not closed by investigation: " + cond
	other := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: "another-world"}
	if err := store.Append(event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, "Authorize",
		resolvedAuthority{Resolution: authority.Resolution{TaskID: taskID, SessionID: "s1", Condition: asked,
			Scope: []string{"main.go"}, Outcome: authority.Authorize, DecidedAt: time.Now().UTC()}, Gap: &other})); err != nil {
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
	e, architect, world := newGapLoopEngine(t, nil, store, gapEscalateWithPremise, gapReply)
	own := GapIdentity{Kind: "unverified-premise", Subject: "main.go", Scope: []string{"main.go"}, World: world}
	settled := resolvedAuthority{Resolution: authority.Resolution{
		TaskID: taskID, SessionID: "s1", Question: "May main.go change?",
		Condition: "a bounded knowledge gap was not closed by investigation: the plan rests on an unverified premise about main.go: main has no callers",
		OptionID:  "1", OptionLabel: "Authorize the architectural change described above",
		Scope: []string{"main.go"}, Outcome: authority.Authorize, DecidedAt: time.Now().UTC(),
	}, Gap: &own}
	if err := store.Append(event.New("s1", taskID, event.SourceUser, event.AuthorityResolved, settled.OptionLabel, settled)); err != nil {
		t.Fatal(err)
	}
	run := driveGapLoop(t, e, architect, world, taskID, "", func(ctx context.Context) {
		e.run(ctx, taskID, "change main.go", RequestedByHuman)
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
	for _, ev := range []event.Event{
		event.New(e.SessionID, taskID, event.SourceUser, event.TaskCreated, "the objective", nil),
		event.New(e.SessionID, taskID, event.SourceSystem, event.WorkflowAwaitingAuthority, q.Condition, q),
	} {
		if err := e.Store.Append(ev); err != nil {
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

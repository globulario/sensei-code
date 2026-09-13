package main

// The answer→resume edge, and the four properties the invariant names.
//
// These run without a repository, a graph, a provider or a live engine, on
// purpose: the refusals are the valuable part of this surface, and a refusal
// that needs a governed run to exercise is a refusal nobody checks.

import (
	"bytes"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/session"
	"github.com/globulario/sensei-code/internal/workflow"
)

// question builds a preserved Level-3 question exactly as awaitChoice records
// one: the DeferredAuthority payload, marshalled, held as raw bytes.
func question(t *testing.T, subject string, ids ...string) json.RawMessage {
	t.Helper()
	options := make([]authority.Option, 0, len(ids))
	for _, id := range ids {
		options = append(options, authority.Option{ID: id, Label: "option " + id})
	}
	raw, err := json.Marshal(workflow.DeferredAuthority{
		Condition: "human_approval_required",
		Domain:    "github.com/globulario/sensei-code",
		BaseSHA:   "0badc0de",
		Decision: authority.Decision{
			Level: authority.Human, Subject: subject, Reason: "the graph could not settle it",
			Options: options,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func standing(t *testing.T, taskID, subject string, ids ...string) session.Interrupted {
	t.Helper()
	return session.Interrupted{
		TaskID: taskID, Task: "do the bounded work",
		AwaitingAuthority: question(t, subject, ids...),
	}
}

// An answer is delivered only to the exact task that is asking, and only when it
// names one of that question's own options.
func TestAnAnswerIsAcceptedOnlyForTheExactStandingQuestion(t *testing.T) {
	tasks := []session.Interrupted{
		standing(t, "t-asking", "may this change land?", "1", "2", "3"),
		{TaskID: "t-plain", Task: "interrupted mid-implementation"},
	}

	got, err := selectAuthorityResume(tasks, "t-asking", "2")
	if err != nil {
		t.Fatalf("a valid answer to a standing question was refused: %v", err)
	}
	if got.Task.TaskID != "t-asking" || got.Option.ID != "2" {
		t.Fatalf("selected %s/%s, want t-asking/2", got.Task.TaskID, got.Option.ID)
	}
	if got.Question.Decision.Subject != "may this change land?" {
		t.Fatalf("the question was not carried through: %q", got.Question.Decision.Subject)
	}

	for name, tc := range map[string]struct {
		taskID, answer string
		want           error
	}{
		"unbound task":                      {"t-nonexistent", "1", errTaskUnknown},
		"task asks nothing":                 {"t-plain", "1", errNoQuestion},
		"option the question did not offer": {"t-asking", "9", errNotAnOption},
		"empty answer":                      {"t-asking", "", errNotAnOption},
	} {
		if _, err := selectAuthorityResume(tasks, tc.taskID, tc.answer); !errorIs(err, tc.want) {
			t.Fatalf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

// A question that cannot be read back is not a question with unknown options.
// Guessing at it would answer something nobody asked.
func TestAnUnreadableOrOptionlessQuestionIsRefused(t *testing.T) {
	optionless, err := json.Marshal(workflow.DeferredAuthority{
		Decision: authority.Decision{Level: authority.Human, Subject: "no options were recorded"},
	})
	if err != nil {
		t.Fatal(err)
	}
	tasks := []session.Interrupted{
		{TaskID: "t-broken", AwaitingAuthority: json.RawMessage(`{"decision": "not an object"`)},
		{TaskID: "t-optionless", AwaitingAuthority: optionless},
	}
	if _, err := selectAuthorityResume(tasks, "t-broken", "1"); !errorIs(err, errQuestionUnusable) {
		t.Fatalf("an unreadable question was not refused: %v", err)
	}
	if _, err := selectAuthorityResume(tasks, "t-optionless", "1"); !errorIs(err, errNoOptions) {
		t.Fatalf("an optionless question was not refused: %v", err)
	}
}

// authorityRequired is the event awaitChoice emits when it asks.
func authorityRequired(taskID string, ids ...string) event.Event {
	options := make([]authority.Option, 0, len(ids))
	for _, id := range ids {
		options = append(options, authority.Option{ID: id, Label: "option " + id})
	}
	return event.New("s", taskID, event.SourceArchitect, event.AuthorityRequired, "may this change land?",
		authority.Decision{Level: authority.Human, Subject: "may this change land?", Options: options})
}

// resolveRecorder stands in for Engine.ResolveHuman: the one door an answer goes
// through.
type resolveRecorder struct {
	mu       sync.Mutex
	calls    []string
	accepted bool
}

func (r *resolveRecorder) resolve(taskID, optionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, taskID+"="+optionID)
	return r.accepted
}

func answerer(t *testing.T, accepted bool, optionID string) (*humanAnswer, *resolveRecorder, *fakeControl, *bytes.Buffer) {
	t.Helper()
	rec := &resolveRecorder{accepted: accepted}
	ctrl := &fakeControl{canDefer: true}
	warn := &bytes.Buffer{}
	return &humanAnswer{
		runControl: ctrl,
		resolve:    rec.resolve,
		option:     authority.Option{ID: optionID, Label: "option " + optionID},
		warn:       warn,
	}, rec, ctrl, warn
}

// The answer reaches the engine through ResolveHuman — the owner's own door —
// and the resumed task runs on to its real terminal rather than exiting 3.
func TestAPersistedQuestionIsAnsweredThroughTheEngineOwner(t *testing.T) {
	answered, rec, ctrl, _ := answerer(t, true, "2")
	code := streamUntilSettled(t.Context(), answered,
		feed(authorityRequired("t1", "1", "2", "3"), ev("t1", event.WorkflowCompleted)),
		"t1", false, true, 0)

	if len(rec.calls) != 1 || rec.calls[0] != "t1=2" {
		t.Fatalf("the answer did not reach ResolveHuman exactly once: %v", rec.calls)
	}
	if len(ctrl.deferred) != 0 {
		t.Fatalf("the question was deferred even though an answer was carried: %v", ctrl.deferred)
	}
	if code != exitCompleted {
		t.Fatalf("exit %d; an answered question must let the same task reach its own terminal, not exit awaiting authority", code)
	}
}

// An answer belongs to ONE question. A second human-owned boundary in the same
// run is preserved, not answered with the spent answer.
func TestTheAnswerIsSpentOnceAndASecondQuestionIsPreserved(t *testing.T) {
	answered, rec, ctrl, warn := answerer(t, true, "2")
	code := streamUntilSettled(t.Context(), answered,
		feed(
			authorityRequired("t1", "1", "2", "3"),
			authorityRequired("t1", "1", "2", "3"),
			ev("t1", event.WorkflowStopped),
		), "t1", false, true, 0)

	if len(rec.calls) != 1 {
		t.Fatalf("the answer was delivered %d times; it belongs to one question: %v", len(rec.calls), rec.calls)
	}
	if len(ctrl.deferred) != 1 || ctrl.deferred[0] != "t1" {
		t.Fatalf("the second question was not preserved: %v", ctrl.deferred)
	}
	if code != exitAwaitingAuthority {
		t.Fatalf("exit %d; a run that leaves a question standing has not completed", code)
	}
	if !strings.Contains(warn.String(), "belongs to the first question") {
		t.Fatalf("the person was not told why the second question stands: %q", warn.String())
	}
}

// The LIVE question is checked, not only the durable record. An answer to a
// question this run is not asking must not satisfy the boundary it did ask.
func TestAnAnswerToADifferentQuestionIsNotDelivered(t *testing.T) {
	answered, rec, ctrl, warn := answerer(t, true, "9")
	code := streamUntilSettled(t.Context(), answered,
		feed(authorityRequired("t1", "1", "2"), ev("t1", event.WorkflowStopped)), "t1", false, true, 0)

	if len(rec.calls) != 0 {
		t.Fatalf("an option the live question never offered was delivered: %v", rec.calls)
	}
	if len(ctrl.deferred) != 1 {
		t.Fatalf("the question was not preserved when the answer did not fit it: %v", ctrl.deferred)
	}
	if code != exitAwaitingAuthority {
		t.Fatalf("exit %d, want the question left standing", code)
	}
	if !strings.Contains(warn.String(), "does not offer the answered option") {
		t.Fatalf("the refusal did not name what was wrong: %q", warn.String())
	}
}

// An undeliverable answer leaves the question standing. Failing to answer must
// never become an answer.
func TestAnUndeliverableAnswerPreservesTheQuestion(t *testing.T) {
	answered, rec, ctrl, warn := answerer(t, false, "2")
	code := streamUntilSettled(t.Context(), answered,
		feed(authorityRequired("t1", "1", "2"), ev("t1", event.WorkflowStopped)), "t1", false, true, 0)

	if len(rec.calls) != 1 {
		t.Fatalf("the answer was not offered to the owner: %v", rec.calls)
	}
	if len(ctrl.deferred) != 1 {
		t.Fatalf("the engine refused the answer and the question was not preserved: %v", ctrl.deferred)
	}
	if code != exitAwaitingAuthority {
		t.Fatalf("exit %d; an undelivered answer must leave the run awaiting authority", code)
	}
	if !strings.Contains(warn.String(), "not waiting on a decision") {
		t.Fatalf("the refusal did not name the engine's report: %q", warn.String())
	}
}

// `run` and `control` must not be able to answer. This is asserted on the TYPE,
// because the guarantee is that they do not implement the interface at all —
// not that they implement it and decline.
func TestOnlyResumeCanAnswerAHumanOwnedQuestion(t *testing.T) {
	var carriesAnAnswer any = &humanAnswer{}
	if _, ok := carriesAnAnswer.(authorityAnswerer); !ok {
		t.Fatal("resume's control cannot answer, so the edge this exists to close is open")
	}
	for name, ctl := range map[string]any{
		"run/control's settle interface": &fakeControl{},
		"the daemon's deferrer":          deferOnly{},
	} {
		if _, ok := ctl.(authorityAnswerer); ok {
			t.Fatalf("%s can answer a human-owned question; only a surface handed an option by a person may", name)
		}
	}
}

// deferOnly is the shape control.go passes: it may preserve a question and
// nothing more.
type deferOnly struct{}

func (deferOnly) DeferAuthority(string) bool { return true }

// The listing is what makes the answer possible: without it a person has to read
// the event log to learn the option ids this command will accept.
func TestStandingQuestionsAreListedWithTheirOptions(t *testing.T) {
	var out bytes.Buffer
	printStandingQuestions(&out, "s-1", []session.Interrupted{
		standing(t, "t-asking", "may this change land?", "1", "2"),
		{TaskID: "t-plain", Task: "no question here"},
	})
	got := out.String()
	for _, want := range []string{"t-asking", "may this change land?", "--answer 1", "--answer 2", "1 standing"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the listing omits %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "t-plain") {
		t.Fatalf("a task asking nothing was listed as answerable:\n%s", got)
	}

	out.Reset()
	printStandingQuestions(&out, "s-1", []session.Interrupted{{TaskID: "t-plain"}})
	if !strings.Contains(out.String(), "no human-owned question is standing") {
		t.Fatalf("an empty listing did not say so: %q", out.String())
	}
}

func errorIs(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error())
}

// An empty default listing must not read as "no question is standing anywhere".
// The newest session is rarely the one that deferred, and a question that exists
// but cannot be found is parked for the same reason `ea89e31` repaired.
func TestAnEmptyDefaultListingPointsAtSessionsThatDoHoldQuestions(t *testing.T) {
	root := t.TempDir()
	write := func(sessionID string, evs ...event.Event) {
		store, err := session.New(root, sessionID)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range evs {
			if err := store.Append(e); err != nil {
				t.Fatal(err)
			}
		}
	}
	// A session that deferred a question, and one that merely ran.
	deferred := event.New("s-old", "t-asking", event.SourceUser, event.WorkflowAwaitingAuthority,
		"authority decision deferred; the question stands", workflow.DeferredAuthority{
			Decision: authority.Decision{Level: authority.Human, Subject: "may this land?",
				Options: []authority.Option{{ID: "1", Label: "yes"}}},
		})
	write("session-old",
		event.New("s-old", "t-asking", event.SourceUser, event.TaskCreated, "do the work", nil),
		event.New("s-old", "t-asking", event.SourceArchitect, event.PlanProposed, "the bounded plan", nil),
		deferred)
	write("session-new", event.New("s-new", "t-done", event.SourceSystem, event.WorkflowCompleted, "done", nil))

	var out bytes.Buffer
	reportOtherSessionsWithQuestions(&out, root, "session-new")
	got := out.String()
	if !strings.Contains(got, "--session session-old") {
		t.Fatalf("the session holding the question was not named:\n%s", got)
	}
	if strings.Contains(got, "session-new") {
		t.Fatalf("the skipped session was listed:\n%s", got)
	}

	// And nothing is invented when no other session holds one.
	out.Reset()
	reportOtherSessionsWithQuestions(&out, root, "session-old")
	if strings.Contains(out.String(), "--session") {
		t.Fatalf("a session with no standing question was offered:\n%s", out.String())
	}
}

func TestCountStandingCountsOnlyTasksThatAreAsking(t *testing.T) {
	got := countStanding([]session.Interrupted{
		standing(t, "a", "q", "1"),
		{TaskID: "b"},
		standing(t, "c", "q", "1"),
	})
	if got != 2 {
		t.Fatalf("countStanding = %d, want 2", got)
	}
}

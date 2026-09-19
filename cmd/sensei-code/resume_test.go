package main

// The answer→resume edge, and the four properties the invariant names.
//
// These run without a repository, a graph, a provider or a live engine, on
// purpose: the refusals are the valuable part of this surface, and a refusal
// that needs a governed run to exercise is a refusal nobody checks.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/globulario/sensei-code/internal/authority"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/gitx"
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
	printActiveTasks(&out, "s-1", []session.Active{
		{SessionID: "s-1", Task: standing(t, "t-asking", "may this change land?", "1", "2")},
		{SessionID: "s-1", Task: session.Interrupted{TaskID: "t-plain", Task: "no question here", Planned: true}},
	}, nil)
	got := out.String()
	for _, want := range []string{"t-asking", "may this change land?", "--answer 1", "--answer 2", "1 standing"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the listing omits %q:\n%s", want, got)
		}
	}
	// t-plain IS listed now -- it is an active task -- but never as answerable.
	if !strings.Contains(got, "t-plain") {
		t.Fatalf("an active task owing no answer was omitted:\n%s", got)
	}
	if strings.Contains(got, "--answer 1  ") && strings.Count(got, "--answer") != 2 {
		t.Fatalf("a task asking nothing was offered answer options:\n%s", got)
	}

	out.Reset()
	printActiveTasks(&out, "s-1", []session.Active{
		{SessionID: "s-1", Task: session.Interrupted{TaskID: "t-plain", Task: "x", Planned: true}}}, nil)
	if !strings.Contains(out.String(), "no human-owned question is standing") {
		t.Fatalf("a listing with nothing standing did not say so: %q", out.String())
	}
	out.Reset()
	printActiveTasks(&out, "s-1", nil, nil)
	if !strings.Contains(out.String(), "no task is active") {
		t.Fatalf("an empty listing did not say so: %q", out.String())
	}
}

// EVERY active task, with what it owes. A listing that rendered only standing
// questions and typed blocks taught its readers that the other lanes do not
// exist: a task interrupted before it was planned appeared nowhere, so the only
// evidence of it was a candidate worktree nobody could account for.
func TestEveryActiveTaskIsListedWithWhatItOwes(t *testing.T) {
	blocked, err := json.Marshal(workflow.ExternalBlock{TaskID: "t-blocked", Role: "architect",
		Provider: "chatgpt", Reason: "usageLimitExceeded", RetryAtState: workflow.RetryAtUnknown})
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	printActiveTasks(&out, "", []session.Active{
		{SessionID: "session-a", Task: standing(t, "t-asking", "may this change land?", "1")},
		{SessionID: "session-a", Task: session.Interrupted{TaskID: "t-reviewing", Task: "owes a review",
			Planned: true, AwaitingReview: true}},
		{SessionID: "session-b", Task: session.Interrupted{TaskID: "t-blocked", Task: "owes a turn",
			BlockedExternal: blocked}},
		{SessionID: "session-b", Task: session.Interrupted{TaskID: "t-unplanned", Task: "owes its architect turn"}},
		{SessionID: "session-b", Task: session.Interrupted{TaskID: "t-implementing", Task: "owes the work",
			Planned: true, Review: "REVISE: the guard is unreachable"}},
	}, nil)
	got := out.String()
	for _, want := range []string{
		"t-asking", "--answer 1",
		"t-reviewing", "the review its candidate is owed",
		"t-blocked", "blocked",
		"t-unplanned", "the architect turn it never took",
		"t-implementing", "the implementation of the plan it already has", "REVISE: the guard is unreachable",
		// The session that holds each task, because that is the record a
		// continuation appends to.
		"session-a", "session-b",
		"all sessions: 5 active, 1 standing",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("the listing omits %q:\n%s", want, got)
		}
	}
	// Each active task is named exactly once, and each says how to resume it.
	if n := strings.Count(got, "resume     --task"); n != 4 {
		t.Fatalf("%d of the 4 non-answerable tasks say how to continue them:\n%s", n, got)
	}
}

// Discovery reads every session record, because a task outlives the process that
// began it. Reading only the newest is what made an active task answer "no
// interrupted task with that id" the moment anything else ran.
func TestAnActiveTaskIsFoundInWhicheverSessionHoldsIt(t *testing.T) {
	root := t.TempDir()
	writeSession(t, root, "session-old",
		event.New("s-old", "t-old", event.SourceSystem, event.TaskCreated, "the interrupted objective", nil))
	writeSession(t, root, "session-new",
		event.New("s-new", "t-new", event.SourceSystem, event.TaskCreated, "another objective", nil),
		event.New("s-new", "t-new", event.SourceSystem, event.WorkflowCompleted, "done", nil))

	found, err := session.FindActive(root)
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}
	if len(found.Records) != 2 {
		t.Fatalf("discovery searched %v, not the 2 records this repository holds", found.Records)
	}
	active := found.Active
	if len(active) != 1 || active[0].Task.TaskID != "t-old" {
		t.Fatalf("the task in the older record was not found: %+v", active)
	}
	if active[0].SessionID != "session-old" {
		t.Fatalf("the task was bound to session %s, not to the record that holds it", active[0].SessionID)
	}
	if _, found := activeTask(active, "t-old"); !found {
		t.Fatal("the task cannot be named by the id a person would type")
	}
	if _, found := activeTask(active, "t-new"); found {
		t.Fatal("a completed task is still offered for continuation")
	}
}

// Discovery FAILS CLOSED. Both of these once had an obvious wrong answer --
// treat the record as empty, or take the first match -- and both of them report
// absence where the truth is ignorance.
func TestDiscoveryFailsClosedOnAnUnreadableHistoryOrASplitIdentity(t *testing.T) {
	t.Run("unreadable history", func(t *testing.T) {
		root := t.TempDir()
		writeSession(t, root, "session-good",
			event.New("s", "t-good", event.SourceSystem, event.TaskCreated, "an objective", nil))
		broken := filepath.Join(root, ".sensei-code", "sessions", "session-broken")
		if err := os.MkdirAll(broken, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(broken, "events.jsonl"), []byte("{not json at all\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		found, err := session.FindActive(root)
		if err == nil {
			t.Fatalf("an unreadable history was reported as holding no active task: %+v", found)
		}
		if !strings.Contains(err.Error(), "session-broken") {
			t.Fatalf("the refusal does not name the record that could not be read: %v", err)
		}
		if found.Active != nil || len(found.Records) != 0 {
			t.Fatalf("a partial world was returned beside the refusal: %+v", found)
		}
	})

	t.Run("one task id in two records", func(t *testing.T) {
		root := t.TempDir()
		for _, id := range []string{"session-one", "session-two"} {
			writeSession(t, root, id,
				event.New(id, "t-split", event.SourceSystem, event.TaskCreated, "one objective, two accounts", nil))
		}
		if _, err := session.FindActive(root); err == nil {
			t.Fatal("a task active in two records was continued from whichever came first")
		} else if !strings.Contains(err.Error(), "session-one") || !strings.Contains(err.Error(), "session-two") {
			t.Fatalf("the refusal does not name both accounts: %v", err)
		}
	})

	t.Run("a named session with no record", func(t *testing.T) {
		root := t.TempDir()
		writeSession(t, root, "session-real",
			event.New("session-real", "t-real", event.SourceSystem, event.TaskCreated, "an objective", nil))
		inventory, err := session.FindActive(root)
		if err != nil {
			t.Fatalf("discovery failed: %v", err)
		}
		if _, err := inventory.ScopedTo("session-absent"); err == nil {
			t.Fatal("a session that holds no record answered that nothing is active in it")
		}
		// The contrast that makes the line above a check: a record that IS
		// there answers, and answers with its own tasks.
		got, err := inventory.ScopedTo("session-real")
		if err != nil {
			t.Fatalf("a recorded session was reported as unknown: %v", err)
		}
		if len(got) != 1 || got[0].Task.TaskID != "t-real" {
			t.Fatalf("scoping returned %+v, not the one task that record holds", got)
		}
	})
}

// writeSession lays down one durable session record, through the same writer the
// engine uses.
func writeSession(t *testing.T, root, sessionID string, evs ...event.Event) {
	t.Helper()
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

func errorIs(err, target error) bool {
	return err != nil && strings.Contains(err.Error(), target.Error())
}

// An empty default listing must not read as "no question is standing anywhere".
// The newest session is rarely the one that deferred, and a question that exists
// but cannot be found is parked for the same reason `ea89e31` repaired.
func TestAnEmptyDefaultListingPointsAtSessionsThatDoHoldQuestions(t *testing.T) {
	root := t.TempDir()
	write := func(sessionID string, evs ...event.Event) { writeSession(t, root, sessionID, evs...) }
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
	write("session-new",
		event.New("s-new", "t-done", event.SourceSystem, event.TaskCreated, "something else", nil),
		event.New("s-new", "t-done", event.SourceSystem, event.WorkflowCompleted, "done", nil))

	// THE HINT IS DRAWN FROM THE VALIDATED INVENTORY, not from a second walk of
	// storage. It used to read the sessions directory itself and skip every
	// failure it met, so the sentence whose whole purpose is "look elsewhere"
	// said nothing about the records most likely to be hiding the question.
	inventory, err := session.FindActive(root)
	if err != nil {
		t.Fatalf("discovery failed: %v", err)
	}

	var out bytes.Buffer
	reportOtherSessionsWithQuestions(&out, inventory.Active, "session-new")
	got := out.String()
	if !strings.Contains(got, "--session session-old") {
		t.Fatalf("the session holding the question was not named:\n%s", got)
	}
	if strings.Contains(got, "session-new") {
		t.Fatalf("the skipped session was listed:\n%s", got)
	}

	// And nothing is invented when no other session holds one.
	out.Reset()
	reportOtherSessionsWithQuestions(&out, inventory.Active, "session-old")
	if strings.Contains(out.String(), "--session") {
		t.Fatalf("a session with no standing question was offered:\n%s", out.String())
	}

	// The hint cannot outlive discovery's refusal: an unreadable record no
	// longer produces an inventory for this to project, where the old reader
	// would have skipped it and printed a confident, incomplete answer.
	broken := filepath.Join(root, ".sensei-code", "sessions", "session-broken")
	if err := os.MkdirAll(broken, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(broken, "events.jsonl"), []byte("{not json at all\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if found, err := session.FindActive(root); err == nil {
		t.Fatalf("an unreadable record was skipped rather than refused, so the hint would speak from a "+
			"world it could not read: %+v", found)
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

// A record that names a different task refuses. One that names none predates the
// field, and absence is not disagreement.
func TestARecordBoundToAnotherTaskRefusesAndAnUnboundOneDoesNot(t *testing.T) {
	bound := func(recordTask string) json.RawMessage {
		raw, err := json.Marshal(workflow.DeferredAuthority{
			TaskID: recordTask, ScopeRecorded: true,
			Scope:    []string{"internal/ghbridge/snapshot.go"},
			Decision: authority.Decision{Level: authority.Human, Subject: "q", Options: []authority.Option{{ID: "1", Label: "yes", Outcome: authority.Authorize}}},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	tasks := []session.Interrupted{
		{TaskID: "t-mine", AwaitingAuthority: bound("t-someone-else")},
		{TaskID: "t-legacy", AwaitingAuthority: bound("")},
	}
	if _, err := selectAuthorityResume(tasks, "t-mine", "1"); !errorIs(err, errBoundElsewhere) {
		t.Fatalf("a record bound to another task was accepted: %v", err)
	}
	if _, err := selectAuthorityResume(tasks, "t-legacy", "1"); err != nil {
		t.Fatalf("a record predating the binding field was refused: %v", err)
	}
}

// An answer that would authorize work on an unprovable record is REFUSED before
// anything starts, and a stop is not.
func TestAnUnprovableRecordRefusesAuthorisationAndAdmitsStop(t *testing.T) {
	legacy := func(outcome authority.Outcome) authorityResume {
		return authorityResume{
			Question: workflow.DeferredAuthority{ScopeRecorded: false},
			Option:   authority.Option{ID: "1", Label: "l", Outcome: outcome},
		}
	}
	for _, outcome := range []authority.Outcome{authority.Authorize, authority.Revise, authority.Decline} {
		err := admitLegacyAnswer(legacy(outcome))
		if !errorIs(err, errUnprovableScope) {
			t.Fatalf("%s on an unprovable record was admitted: %v", outcome, err)
		}
		if !strings.Contains(err.Error(), "must not be reconstructed") {
			t.Errorf("%s: the refusal does not say the scope will not be invented: %v", outcome, err)
		}
	}
	// A stop authorises no subsequent work, so it is admitted.
	if err := admitLegacyAnswer(legacy(authority.Stop)); err != nil {
		t.Fatalf("a stop on an unprovable record was refused: %v", err)
	}
	// And a record that CAN prove its scope is unaffected.
	proven := authorityResume{
		Question: workflow.DeferredAuthority{ScopeRecorded: true, Scope: []string{"a.go"}},
		Option:   authority.Option{ID: "1", Outcome: authority.Authorize},
	}
	if err := admitLegacyAnswer(proven); err != nil {
		t.Fatalf("a provable record was refused: %v", err)
	}
}

// A human stop exits as STOPPED, not as a failure. The exit code is what a
// caller retries on, and retrying a decision is the wrong thing to do.
func TestAHumanStopExitsAsStoppedNotFailed(t *testing.T) {
	answered, rec, ctrl, _ := answerer(t, true, "3")
	code := streamUntilSettled(t.Context(), answered,
		feed(authorityRequired("t1", "1", "2", "3"), ev("t1", event.WorkflowStopped)),
		"t1", false, true, 0)
	if len(rec.calls) != 1 {
		t.Fatalf("the stop answer was not delivered: %v", rec.calls)
	}
	if len(ctrl.deferred) != 0 {
		t.Fatalf("a stop was recorded as a deferral: %v", ctrl.deferred)
	}
	if code == exitFailed {
		t.Fatal("a human stop exited as a FAILURE; a caller cannot tell a decision from a broken run")
	}
	if code != exitStopped {
		t.Fatalf("exit %d, want %d (stopped)", code, exitStopped)
	}
}

// The refusal must actually reach the caller: an inadmissible answer exits as a
// usage error and the durable log is not touched.
//
// End to end through the real command in a real repository, because the unit test
// above proves only that `admitLegacyAnswer` computes a refusal — not that the
// command consults it. That gap is how the 2026-09-13 incident happened: the
// helper said the right thing and execution continued anyway.
func TestTheCommandRefusesAnInadmissibleAnswerAndLeavesTheLogUntouched(t *testing.T) {
	root := gitRepoRoot(t)

	// A LEGACY record: no scope key, no scope_recorded, no task_id in the payload.
	const sid = "session-20260101T000000.000000000Z"
	legacy := `{"condition":"a condition nobody can re-derive","domain":"example.com/fixture",` +
		`"base_sha":"0000000000000000000000000000000000000000","decision":{"level":3,` +
		`"subject":"Architectural authority reached a human-owned boundary.","options":[` +
		`{"id":"1","label":"Authorize","outcome":"authorize"},` +
		`{"id":"3","label":"Stop this task","outcome":"stop"}]}}`
	dir := filepath.Join(root, ".sensei-code", "sessions", sid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "events.jsonl")
	lines := `{"session_id":"` + sid + `","task_id":"t-legacy","source":"system","kind":"task.created","summary":"do the work","time":"2026-01-01T00:00:00Z"}` + "\n" +
		`{"session_id":"` + sid + `","task_id":"t-legacy","source":"user","kind":"workflow.awaiting_authority","summary":"deferred","time":"2026-01-01T00:00:00Z","payload":` + legacy + `}` + "\n"
	if err := os.WriteFile(log, []byte(lines), 0o644); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}

	repo, err := gitx.Discover(t.Context(), root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	code := resumeAuthorityAnswered(t.Context(), repo, config.Config{}, []string{"--task", "t-legacy", "--answer", "1", "--quiet"})
	if code != exitUsage {
		t.Fatalf("exit %d, want %d: an inadmissible answer must be refused as a usage error", code, exitUsage)
	}
	after, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatalf("the durable log changed under a refusal:\nbefore %q\nafter  %q", before, after)
	}
	if bytes.Contains(after, []byte("authority.resolved")) {
		t.Fatal("a refused answer was recorded as a resolution")
	}
}

// DISCOVERY SHRINKS THE WORLD IT SEARCHES, OR IT FAILS. There is no third
// option, and there used to be: every os.Stat and os.ReadDir failure was read as
// "nothing here".
//
// The malformed-JSON case above exercises neither path -- it reaches Load, which
// already refused. These reach the two places that decided a record does not
// exist without ever asking why the question could not be answered.
func TestUnreadableStorageIsNeverReportedAsAbsence(t *testing.T) {
	t.Run("a record that cannot be stat'd for a reason other than not existing", func(t *testing.T) {
		root := t.TempDir()
		writeSession(t, root, "session-good",
			event.New("s", "t-good", event.SourceSystem, event.TaskCreated, "an objective", nil))
		// A self-referential symlink where the record belongs: os.Stat follows
		// it and fails ELOOP, which is not os.ErrNotExist. Chosen over a
		// permission bit because root would defeat that and silently pass.
		dir := filepath.Join(root, ".sensei-code", "sessions", "session-looping")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("events.jsonl", filepath.Join(dir, "events.jsonl")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "events.jsonl")); err == nil {
			t.Skip("this filesystem resolves a self-referential symlink; the fault cannot be staged here")
		} else if os.IsNotExist(err) {
			t.Skipf("this filesystem reports the loop as not-existing, so the two cases cannot be told apart: %v", err)
		}

		found, err := session.FindActive(root)
		if err == nil {
			t.Fatalf("a record that could not be examined was counted as no record: %+v", found)
		}
		if !strings.Contains(err.Error(), "session-looping") {
			t.Fatalf("the refusal does not name the record it could not examine: %v", err)
		}
		if _, _, err := session.Latest(root); err == nil {
			t.Fatal("Latest answered from a directory whose records it could not examine")
		}
	})

	t.Run("a sessions directory that cannot be listed", func(t *testing.T) {
		root := t.TempDir()
		base := filepath.Join(root, ".sensei-code")
		if err := os.MkdirAll(base, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("sessions", filepath.Join(base, "sessions")); err != nil {
			t.Fatal(err)
		}
		if _, err := os.ReadDir(filepath.Join(base, "sessions")); err == nil {
			t.Skip("this filesystem resolves a self-referential symlink; the fault cannot be staged here")
		} else if os.IsNotExist(err) {
			t.Skipf("this filesystem reports the loop as not-existing, so the two cases cannot be told apart: %v", err)
		}

		found, err := session.FindActive(root)
		if err == nil {
			t.Fatalf("a sessions directory that could not be listed was reported as empty: %+v", found)
		}
		if _, ok, err := session.Latest(root); err == nil {
			t.Fatalf("Latest reported absence (%v) from a directory it could not list", ok)
		}
		// And the contrast that makes this a check rather than a coincidence: a
		// repository with no sessions directory at all IS absence, and answers.
		if found, err := session.FindActive(t.TempDir()); err != nil || len(found.Records) != 0 || found.Active != nil {
			t.Fatalf("a repository that has begun nothing was not answered: %+v, %v", found, err)
		}
	})
}

// A TASK IDENTITY BELONGS TO ONE RECORD, and the claim to it is its CREATION.
//
// The duplicate refusal used to compare the tasks discovery returned, which are
// already filtered by lifecycle state. So one record holding an active
// task.created and another holding the same identity followed by a terminal
// passed the check -- only one of the two accounts was ever offered for
// comparison -- and a continuation could be appended to an account describing
// different work, with nothing said.
func TestOneTaskIdentityClaimedByTwoRecordsIsAlwaysRefused(t *testing.T) {
	t.Run("active in one record, ended in another", func(t *testing.T) {
		root := t.TempDir()
		writeSession(t, root, "session-active",
			event.New("session-active", "t-split", event.SourceSystem, event.TaskCreated, "one objective", nil))
		writeSession(t, root, "session-ended",
			event.New("session-ended", "t-split", event.SourceSystem, event.TaskCreated, "one objective", nil),
			event.New("session-ended", "t-split", event.SourceSystem, event.WorkflowCompleted, "done", nil))

		found, err := session.FindActive(root)
		if err == nil {
			t.Fatalf("a task whose identity two records claim was offered for continuation: %+v", found)
		}
		for _, want := range []string{"session-active", "session-ended", "t-split"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("the refusal does not name %q: %v", want, err)
			}
		}
	})

	t.Run("created twice inside one record", func(t *testing.T) {
		root := t.TempDir()
		writeSession(t, root, "session-doubled",
			event.New("session-doubled", "t-twice", event.SourceSystem, event.TaskCreated, "first", nil),
			event.New("session-doubled", "t-twice", event.SourceSystem, event.TaskCreated, "second", nil))

		found, err := session.FindActive(root)
		if err == nil {
			t.Fatalf("a record that creates one identity twice was reconstructed as a single task: %+v", found)
		}
		if !strings.Contains(err.Error(), "t-twice") {
			t.Fatalf("the refusal does not name the doubled identity: %v", err)
		}
		// Naming the session does not make an unattributable history
		// attributable. There is no per-record reader left for --session to
		// take: it narrows the inventory this call produces, and a refusal
		// produces none to narrow. The scoped route is driven end to end in
		// TestNamingASessionDoesNotWaiveRepositoryWideValidation.
		if _, err := found.ScopedTo("session-doubled"); err == nil {
			t.Fatal("a refused world still answered a scoped question about the record it refused")
		}
	})
}

// A CREATED TASK WITH NO OBJECTIVE IS LISTED, AND EVERY CONTINUATION OF IT IS
// REFUSED BY NAME. Both halves matter: dropping it is the absence this repair
// removes, and offering it would advertise a continuation certain to fail.
func TestATaskThatRecordedNoObjectiveIsShownAndRefused(t *testing.T) {
	blank := session.Interrupted{TaskID: "t-blank", Task: "  "}
	lane, err := selectResumeLane(blank, false)
	if lane != laneUnusable {
		t.Fatalf("lane = %v, want the unusable-objective refusal", lane)
	}
	if !errorIs(err, errObjectiveUnusable) {
		t.Fatalf("err = %v, want a named refusal", err)
	}
	// It outranks every other obligation, including a standing question: an
	// answer would be spent re-entering a governed path that refuses an empty
	// objective, and the question would be gone.
	asking := standing(t, "t-blank", "may this land?", "1")
	asking.Task = ""
	if lane, _ := selectResumeLane(asking, false); lane != laneUnusable {
		t.Fatalf("lane = %v; a task with no objective was routed to its question, which answering cannot serve", lane)
	}

	// And it is not counted as an answerable question anywhere, so the listing's
	// footer and its "look in another session" hint agree with the lane it was
	// actually given.
	if n := countStanding([]session.Interrupted{asking}); n != 0 {
		t.Fatalf("countStanding = %d; a question nobody can answer was counted as standing, which suppresses "+
			"the pointer to sessions that hold a real one", n)
	}

	var out bytes.Buffer
	printActiveTasks(&out, "", []session.Active{{SessionID: "session-a", Task: blank}}, nil)
	got := out.String()
	if !strings.Contains(got, "t-blank") {
		t.Fatalf("the task vanished from the listing:\n%s", got)
	}
	if !strings.Contains(got, "recorded no objective") {
		t.Fatalf("the listing does not say why the task cannot be continued:\n%s", got)
	}
	if strings.Contains(got, "resume     --task t-blank") {
		t.Fatalf("the listing offers a continuation that is certain to be refused:\n%s", got)
	}
}

// writeReviewObligation lays down the durable statement that an exact candidate
// is owed a review, in the form ghbridge's own store reads.
//
// Written as the record on disk rather than constructed in memory: the point of
// these cases is that the LISTING consults the durable owner of review lifetime,
// and a fixture that handed it the answer directly would prove only that a map
// can be passed to a function.
func writeReviewObligation(t *testing.T, root, taskID, requestID string) {
	t.Helper()
	dir := filepath.Join(root, ".sensei-code", "exchanges")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	sha := strings.Repeat("a", 40)
	blob, err := json.Marshal(map[string]any{
		"task_id": taskID, "request_id": requestID, "kind": "review",
		"base": sha, "candidate_digest": "sha256:" + strings.Repeat("b", 40),
		"candidate_tree": strings.Repeat("c", 40), "review_commit": strings.Repeat("d", 40),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, requestID+".json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
}

// THE LISTING ROUTES ON THE SAME WORLD --task DOES.
//
// It used to pass reviewOwed=false unconditionally, with a comment admitting
// that a task holding an obligation recorded outside the session record would be
// "shown at the lane its transcript states". So a candidate whose process died
// after the review request was published and recorded, but before the workflow
// wrote its WAITING_REVIEW terminal, was advertised as owing IMPLEMENTATION --
// handing a worker a candidate nobody had objected to, while the durable owner
// of review lifetime said a review was outstanding
// (sensei_code.reviewobligation.a_waiter_is_disposable_the_obligation_is_not).
func TestTheListingReadsTheDurableReviewObligationAndNotOnlyTheTranscript(t *testing.T) {
	render := func(t *testing.T, root string, active []session.Active) (string, error) {
		t.Helper()
		states, err := loadReviewStates(root, active)
		if err != nil {
			return "", err
		}
		var out bytes.Buffer
		printActiveTasks(&out, "", active, states)
		return out.String(), nil
	}

	t.Run("the obligation is recorded and the terminal never was", func(t *testing.T) {
		root := t.TempDir()
		writeReviewObligation(t, root, "t-owed", "r-standing")
		// AwaitingReview false and Planned true: exactly the record the crash
		// leaves behind, which reads as ordinary interrupted implementation.
		active := []session.Active{{SessionID: "session-a",
			Task: session.Interrupted{TaskID: "t-owed", Task: "publish and die", Planned: true}}}

		got, err := render(t, root, active)
		if err != nil {
			t.Fatalf("the listing refused a readable obligation: %v", err)
		}
		if !strings.Contains(got, laneReview.String()) {
			t.Fatalf("the listing shows a lane the router would not take:\n%s", got)
		}
		if strings.Contains(got, laneImplementation.String()) {
			t.Fatalf("a candidate owed an independent review was advertised as implementation work:\n%s", got)
		}
		// And the same world routes --task to the same lane, which is the
		// property the two surfaces were disagreeing about.
		owed, err := owedReviewObligation(root, "t-owed")
		if err != nil || owed == nil {
			t.Fatalf("the durable obligation was not established: %+v, %v", owed, err)
		}
		if lane, err := selectResumeLane(active[0].Task, owed != nil); lane != laneReview || err != nil {
			t.Fatalf("--task routes to %v (%v); the listing and the router disagree", lane, err)
		}
	})

	t.Run("no obligation leaves the transcript's lane alone", func(t *testing.T) {
		root := t.TempDir()
		active := []session.Active{{SessionID: "session-a",
			Task: session.Interrupted{TaskID: "t-plain", Task: "ordinary work", Planned: true}}}
		got, err := render(t, root, active)
		if err != nil {
			t.Fatalf("a repository with no obligations refused the listing: %v", err)
		}
		if !strings.Contains(got, laneImplementation.String()) {
			t.Fatalf("a task owing no review was moved out of its lane:\n%s", got)
		}
	})

	t.Run("the transcript and the obligation name different requests", func(t *testing.T) {
		root := t.TempDir()
		writeReviewObligation(t, root, "t-split", "r-standing")
		active := []session.Active{{SessionID: "session-a", Task: session.Interrupted{
			TaskID: "t-split", Task: "two accounts of one review", Planned: true, AwaitingReview: true,
			AwaitingReviewRecord: []byte(`{"review_kind":"unanswered","request_id":"r-something-else"}`)}}}

		got, err := render(t, root, active)
		if err != nil {
			t.Fatalf("one task's conflict refused the whole listing, hiding every other task: %v", err)
		}
		if !strings.Contains(got, "t-split") {
			t.Fatalf("a conflicted task was hidden rather than shown as unroutable:\n%s", got)
		}
		if !strings.Contains(got, "cannot be decided") {
			t.Fatalf("a conflicted task was given a lane anyway:\n%s", got)
		}
		for _, want := range []string{"r-standing", "r-something-else"} {
			if !strings.Contains(got, want) {
				t.Fatalf("the listing does not name %q, so the disagreement cannot be investigated:\n%s", want, got)
			}
		}
		// A task beside it, with records that agree, is still routed.
		active = append(active, session.Active{SessionID: "session-a",
			Task: session.Interrupted{TaskID: "t-fine", Task: "unaffected", Planned: true}})
		got, err = render(t, root, active)
		if err != nil || !strings.Contains(got, "resume     --task t-fine") {
			t.Fatalf("a neighbouring task lost its continuation to another task's conflict (%v):\n%s", err, got)
		}
	})

	t.Run("the transcript's waiting-review record cannot be read", func(t *testing.T) {
		root := t.TempDir()
		writeReviewObligation(t, root, "t-garbled", "r-standing")
		active := []session.Active{{SessionID: "session-a", Task: session.Interrupted{
			TaskID: "t-garbled", Task: "unreadable transcript", Planned: true, AwaitingReview: true,
			AwaitingReviewRecord: []byte(`{not json at all`)}}}

		got, err := render(t, root, active)
		if err != nil {
			t.Fatalf("the listing was refused as a whole: %v", err)
		}
		if !strings.Contains(got, "cannot be decided") {
			t.Fatalf("an unreadable transcript record was treated as naming no request, which turns a check "+
				"that could not run into one that passed:\n%s", got)
		}
		// The router refuses it for the same reason, by the same rule.
		owed, err := owedReviewObligation(root, "t-garbled")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := selectReviewResume(tasksOf(active), "t-garbled", owed); !errorIs(err, errReviewIdentitySplit) {
			t.Fatalf("err = %v; the router accepted a comparison it could not make", err)
		}
	})

	t.Run("an unreadable obligation store ends the listing", func(t *testing.T) {
		root := t.TempDir()
		dir := filepath.Join(root, ".sensei-code", "exchanges")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "r-broken.json"), []byte("{not json"), 0o600); err != nil {
			t.Fatal(err)
		}
		active := []session.Active{{SessionID: "session-a",
			Task: session.Interrupted{TaskID: "t-any", Task: "work", Planned: true}}}
		if _, err := render(t, root, active); err == nil {
			t.Fatal("a listing rendered lanes it could not establish; an unreadable store is not an empty one")
		}
	})
}

// gitRepoRoot is a committed git repository, which the resume command needs
// because it discovers one before it reads anything.
func gitRepoRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	for _, args := range [][]string{
		{"init", "-q"}, {"config", "user.email", "t@t"}, {"config", "user.name", "t"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git unavailable: %v %s", err, out)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "-A"}, {"commit", "-qm", "init"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	return root
}

// runResume drives the command as a person does and returns its exit code with
// everything it printed.
//
// BOTH streams, because a refusal and a listing are two halves of one answer and
// the exit code tells them apart from neither. "That session holds nothing
// active" and "there is no such session" are both usage refusals; only the
// sentence says which one was meant.
func runResume(t *testing.T, root string, args ...string) (int, string, string) {
	t.Helper()
	repo, err := gitx.Discover(t.Context(), root)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	dir := t.TempDir()
	outFile, err := os.Create(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	errFile, err := os.Create(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	savedOut, savedErr := os.Stdout, os.Stderr
	os.Stdout, os.Stderr = outFile, errFile
	code := func() int {
		defer func() { os.Stdout, os.Stderr = savedOut, savedErr }()
		return resumeAuthorityAnswered(t.Context(), repo, config.Config{}, args)
	}()
	if err := outFile.Close(); err != nil {
		t.Fatal(err)
	}
	if err := errFile.Close(); err != nil {
		t.Fatal(err)
	}
	stdout, err := os.ReadFile(filepath.Join(dir, "stdout"))
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := os.ReadFile(filepath.Join(dir, "stderr"))
	if err != nil {
		t.Fatal(err)
	}
	return code, string(stdout), string(stderr)
}

// NAMING A SESSION IS A FILTER, NEVER A WAIVER.
//
// `--session` used to branch away from repository-wide discovery into a reader
// that opened the named record ALONE. Both of discovery's refusals were then
// unreachable on exactly the path a person takes when they know which session
// they mean: another record claiming the same task id was invisible, and a
// history elsewhere nobody could open was never met. An operator could name one
// side of an ambiguous identity and continue it, and be told nothing.
//
// Every case here goes through the command, because the defect was in which
// function the command CHOSE -- a unit test of either reader would have passed
// throughout.
func TestNamingASessionDoesNotWaiveRepositoryWideValidation(t *testing.T) {
	t.Run("a split identity elsewhere refuses the scoped listing", func(t *testing.T) {
		root := gitRepoRoot(t)
		writeSession(t, root, "session-named",
			event.New("session-named", "t-named", event.SourceSystem, event.TaskCreated, "the named account", nil))
		// Two other records claim one identity. The named record is readable,
		// unambiguous, and entirely beside the point.
		for _, id := range []string{"session-one", "session-two"} {
			writeSession(t, root, id,
				event.New(id, "t-split", event.SourceSystem, event.TaskCreated, "one objective, two accounts", nil))
		}

		code, stdout, stderr := runResume(t, root, "--list", "--session", "session-named")
		if code == exitCompleted {
			t.Fatalf("naming a readable session answered from a repository with a split identity:\n%s", stdout)
		}
		if strings.Contains(stdout, "t-named") {
			t.Fatalf("a listing was rendered beside the unresolved identity:\n%s", stdout)
		}
		for _, want := range []string{"t-split", "session-one", "session-two"} {
			if !strings.Contains(stderr, want) {
				t.Fatalf("the refusal does not name %q:\n%s", want, stderr)
			}
		}
	})

	t.Run("an unreadable record elsewhere refuses the scoped listing", func(t *testing.T) {
		root := gitRepoRoot(t)
		writeSession(t, root, "session-named",
			event.New("session-named", "t-named", event.SourceSystem, event.TaskCreated, "the named account", nil))
		broken := filepath.Join(root, ".sensei-code", "sessions", "session-broken")
		if err := os.MkdirAll(broken, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(broken, "events.jsonl"), []byte("{not json at all\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runResume(t, root, "--list", "--session", "session-named")
		if code == exitCompleted {
			t.Fatalf("naming a readable session answered from a repository holding a history nobody could "+
				"read:\n%s", stdout)
		}
		if !strings.Contains(stderr, "session-broken") {
			t.Fatalf("the refusal does not name the record it could not read:\n%s", stderr)
		}
	})

	t.Run("a session nobody recorded is refused, not answered as empty", func(t *testing.T) {
		root := gitRepoRoot(t)
		writeSession(t, root, "session-real",
			event.New("session-real", "t-real", event.SourceSystem, event.TaskCreated, "real work", nil))

		code, stdout, stderr := runResume(t, root, "--list", "--session", "session-typo")
		if code == exitCompleted {
			t.Fatalf("a mistyped session name was answered as a session holding nothing:\n%s", stdout)
		}
		if !strings.Contains(stderr, "session-typo") {
			t.Fatalf("the refusal does not name what was typed:\n%s", stderr)
		}
		if strings.Contains(stdout, "no task is active") {
			t.Fatalf("absence was stated about a record that does not exist:\n%s", stdout)
		}
	})

	t.Run("a validated repository narrows to the named session and says where else to look", func(t *testing.T) {
		root := gitRepoRoot(t)
		writeSession(t, root, "session-quiet",
			event.New("session-quiet", "t-quiet", event.SourceSystem, event.TaskCreated, "unplanned work", nil))
		writeSession(t, root, "session-asking",
			event.New("session-asking", "t-asking", event.SourceSystem, event.TaskCreated, "do the work", nil),
			event.New("session-asking", "t-asking", event.SourceUser, event.WorkflowAwaitingAuthority,
				"authority decision deferred", workflow.DeferredAuthority{TaskID: "t-asking",
					Decision: authority.Decision{Level: authority.Human, Subject: "may this land?",
						Options: []authority.Option{{ID: "1", Label: "yes"}}}}))

		code, stdout, stderr := runResume(t, root, "--list", "--session", "session-quiet")
		if code != exitCompleted {
			t.Fatalf("exit %d, want %d; a validated repository refused a listing of one of its records:\n%s",
				code, exitCompleted, stderr)
		}
		if !strings.Contains(stdout, "t-quiet") {
			t.Fatalf("the named session's own task is missing:\n%s", stdout)
		}
		if strings.Contains(stdout, "task t-asking") {
			t.Fatalf("--session did not narrow the listing:\n%s", stdout)
		}
		// And the hint, projected from the same validated inventory, so "none
		// here" is not read as "there are none".
		if !strings.Contains(stdout, "--session session-asking") {
			t.Fatalf("the session that does hold a standing question was not pointed at:\n%s", stdout)
		}
	})

	t.Run("a task held by another session is not reported as unknown to the repository", func(t *testing.T) {
		root := gitRepoRoot(t)
		writeSession(t, root, "session-named",
			event.New("session-named", "t-named", event.SourceSystem, event.TaskCreated, "the named account", nil))
		writeSession(t, root, "session-holding",
			event.New("session-holding", "t-wanted", event.SourceSystem, event.TaskCreated, "the wanted task", nil))

		code, _, stderr := runResume(t, root, "--task", "t-wanted", "--session", "session-named")
		if code != exitUsage {
			t.Fatalf("exit %d, want %d", code, exitUsage)
		}
		// The wider sentence would be stated about a record this lookup never
		// consulted, while the inventory in hand can name the one that holds it.
		if strings.Contains(stderr, errTaskUnknown.Error()) {
			t.Fatalf("a scoped miss was reported as absence from every session in the repository:\n%s", stderr)
		}
		if !strings.Contains(stderr, "session-holding") {
			t.Fatalf("the refusal does not name the session that holds the task:\n%s", stderr)
		}
	})
}

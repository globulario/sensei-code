//go:build linux

package workflow

// N5 P2-A witnesses (RULING-366 §3-§16, §21; §30 W1, W2, W4, W5, W10, W11,
// W13, W14, W17, W23, W24). An invocation that ends while the durable
// candidate record says its workspace containment is not established ends
// WORKSPACE_QUARANTINED: invocation-terminal, never a behavioural failure,
// its receipt and terminal one record unit, and the record itself -- the only
// quarantine authority -- read and never written.
//
// The terminal kind and outcome are spelled as the record spells them, so
// these witnesses read the same bytes before and after the vocabulary exists.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/behavioral"
	"github.com/globulario/sensei-code/internal/candidate"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/runreceipt"
	"github.com/globulario/sensei-code/internal/session"
)

const (
	wqKind    = event.Kind("workflow.workspace_quarantined")
	wqOutcome = runreceipt.Outcome("WORKSPACE_QUARANTINED")
	wqTask    = "task-q"
)

// wqRig is an engine over a real session directory whose repository root
// holds wqTask's candidate record, settled, with the task rooted and its
// receipt open.
func wqRig(t *testing.T) (*Engine, <-chan event.Event, *session.Store, string) {
	t.Helper()
	root := t.TempDir()
	e, events, store := blockedEngine(t, root, "session-q")
	id := candidate.Identity{TaskID: wqTask, Repository: root, BaseSHA: strings.Repeat("a", 40),
		Worktree: filepath.Join(root, "wt"), CreatedAt: time.Now().UTC()}
	if err := id.Save(root); err != nil {
		t.Fatal(err)
	}
	rootFixtureTaskOnce(t, e, wqTask, "the objective")
	e.beginReceipt(wqTask)
	return e, events, store, root
}

// wqInFlight records a confined execution of taskID in flight, with a
// baseline when one is named.
func wqInFlight(t *testing.T, root, taskID, invocation, workspace, baseline string) {
	t.Helper()
	if _, err := candidate.BeginExecution(root, taskID, candidate.InFlight{Invocation: invocation, Phase: "certify", Workspace: workspace}); err != nil {
		t.Fatal(err)
	}
	if baseline != "" {
		if err := candidate.NoteBaseline(root, taskID, invocation, baseline); err != nil {
			t.Fatal(err)
		}
	}
}

func wqQuarantine(t *testing.T, root, taskID string) {
	t.Helper()
	if _, err := candidate.QuarantineExecution(root, taskID, candidate.Resolution{Reason: "the certify execution has no established teardown"}); err != nil {
		t.Fatal(err)
	}
}

func wqRecordBytes(t *testing.T, root, taskID string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, ".sensei-code", "candidates", taskID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// wqReceipt is the receipt record of an event stream as JSON, so a member the
// schema may not yet define is still read.
func wqReceipt(t *testing.T, events []event.Event) (map[string]any, string, []string) {
	t.Helper()
	for _, ev := range events {
		if ev.Kind != event.RunReceipt {
			continue
		}
		var body struct {
			Receipt      map[string]any `json:"receipt"`
			Completeness string         `json:"completeness"`
			Missing      []string       `json:"missing"`
		}
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			t.Fatal(err)
		}
		return body.Receipt, body.Completeness, body.Missing
	}
	t.Fatalf("no run receipt was emitted: %v", kinds(events))
	return nil, "", nil
}

func wqPayload(t *testing.T, events []event.Event) (map[string]any, event.Event) {
	t.Helper()
	for _, ev := range events {
		if ev.Kind == wqKind {
			var m map[string]any
			if err := json.Unmarshal(ev.Payload, &m); err != nil {
				t.Fatalf("the quarantine terminal's payload is not one object: %v", err)
			}
			return m, ev
		}
	}
	t.Fatalf("no %s terminal: %v", wqKind, summaries(events))
	return nil, event.Event{}
}

func wqField(r map[string]any) map[string]any {
	m, _ := r["workspace_quarantine"].(map[string]any)
	return m
}

func wqValueState(m map[string]any, name string) string {
	v, _ := m[name].(map[string]any)
	s, _ := v["state"].(string)
	return s
}

func wqValueText(m map[string]any, name string) string {
	v, _ := m[name].(map[string]any)
	s, _ := v["text"].(string)
	return s
}

func wqMentions(missing []string, sub string) bool {
	for _, m := range missing {
		if strings.Contains(m, sub) {
			return true
		}
	}
	return false
}

// wqAssertQuarantined holds what every verified quarantine terminal owes: the
// new terminal and no other ending, the receipt WORKSPACE_QUARANTINED over an
// UNKNOWN candidate and complete about its own claim, and a payload naming the
// recorded facts.
func wqAssertQuarantined(t *testing.T, seen []event.Event, taskID, invocation, workspace string) map[string]any {
	t.Helper()
	for _, k := range []event.Kind{event.WorkflowFailed, event.WorkflowStopped, event.WorkflowTimedOut} {
		if contains(seen, k) {
			t.Fatalf("an unsettled workspace ended %s: %v", k, summaries(seen))
		}
	}
	p, _ := wqPayload(t, seen)
	if p["task_id"] != taskID || p["recorded"] != true || p["invocation"] != invocation || p["workspace"] != workspace {
		t.Fatalf("the quarantine terminal does not name the recorded facts: %v", p)
	}
	r, _, missing := wqReceipt(t, seen)
	if r["outcome"] != string(wqOutcome) || r["candidate_state"] != string(runreceipt.CandidateUnknown) {
		t.Fatalf("the receipt says %v / %v, want WORKSPACE_QUARANTINED / UNKNOWN", r["outcome"], r["candidate_state"])
	}
	f := wqField(r)
	if f == nil || f["state"] != "KNOWN" || f["recorded"] != true || wqValueText(f, "invocation") != invocation || wqValueText(f, "workspace") != workspace {
		t.Fatalf("the receipt's workspace_quarantine does not state the recorded facts: %v", f)
	}
	for _, m := range missing {
		if strings.Contains(m, "workspace_quarantine") || strings.Contains(m, "candidate_") {
			t.Fatalf("the quarantine receipt is incomplete about its own claim: %s", m)
		}
	}
	return p
}

// wqSettle reads a resumed invocation's events until its run ending.
func wqSettle(t *testing.T, events <-chan event.Event) []event.Event {
	t.Helper()
	var seen []event.Event
	deadline := time.After(20 * time.Second)
	for {
		select {
		case ev := <-events:
			seen = append(seen, ev)
			if _, terminal := event.RunTerminality(ev.Kind); terminal || ev.Kind == wqKind {
				return seen
			}
		case <-deadline:
			t.Fatalf("the resumed invocation did not end: %v", kinds(seen))
		}
	}
}

// W1. A LIVE unsettled certification on the real candidate loop ends the
// invocation WORKSPACE_QUARANTINED through the production classifier, not
// FAILED, and the task stays discoverable.
func TestW1ALiveUnsettledValidationEndsWorkspaceQuarantined(t *testing.T) {
	h := newGateHarness(t, roles.Policy{}, roles.Fresh, "accept")
	h.engine.Config.Validation.Confined = true
	h.engine.Config.Validation.Confinement = pinnedWrapper(t, n4SilentLauncher("candidate\\ ro*"))
	_, _, _, _, err := h.engine.runCandidate(fixtureCtx(h.engine, "task-1"), h.sc, certifiedStart{},
		"task-1", h.tc, "Rewrite main.go so it prints a number.", h.worker, h.work, "")
	var u *WorkspaceUnsettled
	if !errors.As(err, &u) || !u.Recorded {
		t.Fatalf("premise: the certification was not durably quarantined: %v", err)
	}
	drainEvents(h.events)
	h.engine.terminateRun(fixtureCtx(h.engine, "task-1"), "task-1", "Rewrite main.go so it prints a number.", err)
	seen := drainEvents(h.events)
	id := quarantinedRecord(t, h.engine.Repo.Root)
	wqAssertQuarantined(t, seen, "task-1", u.Invocation, id.InFlight.Workspace)
	// M12a. The quarantine terminal CLAIMS no candidate state beyond what was
	// observed: a PRESENT claim at the emit site would disagree with the
	// UNKNOWN projection and surface as candidate_state_disagreement.
	for _, ev := range seen {
		if ev.Kind != event.RunReceipt {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(ev.Payload, &body); err != nil {
			t.Fatal(err)
		}
		if d, has := body["candidate_state_disagreement"]; has {
			t.Fatalf("the quarantine terminal claimed a candidate state it never observed: %v", d)
		}
	}
	history, lerr := h.engine.Store.Load()
	if lerr != nil {
		t.Fatal(lerr)
	}
	found := false
	for _, it := range session.FindInterrupted(history) {
		found = found || it.TaskID == "task-1"
	}
	if !found {
		t.Fatal("the quarantined task is no longer discoverable")
	}
}

// W2. A quarantine under a CANCELLED context is neither STOPPED nor
// TIMED_OUT: the record's unsettled state takes precedence, and the
// cancellation is kept in the account. The invocation's own context is
// cancelled exactly as a caller's stop or an expired budget cancels it.
func TestW2AQuarantineUnderACancelledContextIsNotAStop(t *testing.T) {
	for name, c := range map[string]struct {
		timedOut bool
		note     string
	}{
		"stopped by the caller": {false, "stopped"},
		"budget expired":        {true, "budget"},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			workspace := filepath.Join(root, "wt")
			wqInFlight(t, root, wqTask, "exec-cancelled", workspace, "")
			if c.timedOut {
				e.mu.Lock()
				e.timedOut = map[string]bool{wqTask: true}
				e.mu.Unlock()
			}
			ctx, cancel := context.WithCancel(fixtureCtx(e, wqTask))
			cancel()
			e.terminateRun(ctx, wqTask, "the objective", ctx.Err())
			seen := drainEvents(events)
			_, terminal := wqPayload(t, seen)
			wqAssertQuarantined(t, seen, wqTask, "exec-cancelled", workspace)
			if !strings.Contains(terminal.Summary, c.note) {
				t.Fatalf("the cancellation is not kept in the quarantine's account: %q", terminal.Summary)
			}
		})
	}
}

// W4. RESTART: an execution recorded in flight, no terminal, and a fresh
// engine resumes the task. The record, the workspace and the task all stand.
//
// RULING-373 §6-§8 amends what the resume reaches: the engine's containment
// gate refuses it, typed, before its session lineage is bound
// (ErrResumeContainmentRefused wrapping the record's *candidate.ErrQuarantined),
// so nothing is recorded or published and no terminal is written. The pre-fix
// expectation (a7b41bd): the resume bound the lineage, ran until its ending
// read the record, and projected the WORKSPACE_QUARANTINED terminal
// (wqAssertQuarantined over the published events).
func TestW4ARestartOverAnInFlightRecordEndsWorkspaceQuarantined(t *testing.T) {
	e, events, task, _ := rootedResumeHarness(t, false)
	id, _, err := candidate.Load(e.Repo.Root, task.TaskID)
	if err != nil {
		t.Fatal(err)
	}
	wqInFlight(t, e.Repo.Root, task.TaskID, "exec-crashed", id.Worktree, "")
	before := wqRecordBytes(t, e.Repo.Root, task.TaskID)
	attempt := e.ResumeTask(context.Background(), task)
	refusal := attempt.Binding().Refusal
	var q *candidate.ErrQuarantined
	if !errors.Is(refusal, ErrResumeContainmentRefused) || !errors.As(refusal, &q) || q.Workspace != id.Worktree {
		t.Fatalf("the resume over an in-flight record was not the typed pre-binding containment refusal: %v", refusal)
	}
	<-attempt.Ended()
	if seen := drainEvents(events); len(seen) != 0 {
		t.Fatalf("the refused resume published %v", summaries(seen))
	}
	if after := wqRecordBytes(t, e.Repo.Root, task.TaskID); after != before {
		t.Fatalf("the projection wrote the quarantine authority:\nbefore %s\nafter  %s", before, after)
	}
	if st, statErr := os.Stat(id.Worktree); statErr != nil || !st.IsDir() {
		t.Fatalf("the unsettled workspace is gone: %v", statErr)
	}
	history, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	listed := session.FindInterrupted(history)
	if len(listed) != 1 || listed[0].TaskID != task.TaskID {
		t.Fatalf("the quarantined task left the discoverable set: %+v", listed)
	}
}

// W5. A standing question and an unsettled record: the resume refuses, the
// question is not consumed, and the task is not ended FAILED -- the question
// still stands exactly as it was asked.
//
// RULING-373 §4, §6-§8 amend where it refuses: at the engine's containment
// gate, before the lineage is bound and before the question is re-asked, as
// the typed pre-binding refusal, with no terminal at all. The pre-fix
// expectation (a7b41bd): the resume refused at resumePrecondition, after
// binding, and projected the WORKSPACE_QUARANTINED terminal.
func TestW5ADeferredQuestionOverAQuarantineStandsAndTheTaskIsNotFailed(t *testing.T) {
	ctx := context.Background()
	e, events, _ := resumeHarness(t, false)
	e.Config.Sensei.Command = "sh"
	e.Config.Sensei.Args = []string{"-c", proposalSenseiScript(t)}
	const taskID = "task-r"
	q := deferScoped(t, taskID, planScope())
	q.SessionID = e.SessionID
	for _, ev := range []event.Event{
		event.New(e.SessionID, taskID, event.SourceUser, event.TaskCreated, "the objective", nil),
		event.New(e.SessionID, taskID, event.SourceUser, event.WorkflowAwaitingAuthority, q.Condition, q),
	} {
		if err := seedAppend(t, e.Store, ev); err != nil {
			t.Fatal(err)
		}
	}
	id, _, err := candidate.Load(e.Repo.Root, taskID)
	if err != nil {
		t.Fatal(err)
	}
	wqInFlight(t, e.Repo.Root, taskID, "exec-question", id.Worktree, "")
	wqQuarantine(t, e.Repo.Root, taskID)
	before, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	standing := session.FindInterrupted(before)
	if len(standing) != 1 || len(standing[0].AwaitingAuthority) == 0 {
		t.Fatalf("premise: one task with a standing question: %+v", standing)
	}
	attempt := e.ResumeTask(ctx, standing[0])
	consumed := false
	var seen []event.Event
	deadline := time.After(30 * time.Second)
	for done := false; !done; {
		if e.ResolveHuman(taskID, "1") {
			consumed = true
		}
		select {
		case ev := <-events:
			seen = append(seen, ev)
		case <-attempt.Ended():
			done = true
		case <-time.After(10 * time.Millisecond):
		case <-deadline:
			t.Fatalf("the resumed invocation did not end: %v", kindsOf(seen))
		}
	}
	seen = append(seen, drainEvents(events)...)
	if consumed || contains(seen, event.AuthorityResolved) {
		t.Fatal("the standing question was answered over a quarantined workspace")
	}
	refusal := attempt.Binding().Refusal
	var quarantined *candidate.ErrQuarantined
	if !errors.Is(refusal, ErrResumeContainmentRefused) || !errors.As(refusal, &quarantined) ||
		quarantined.Workspace != id.Worktree {
		t.Fatalf("the resume was not the typed pre-binding containment refusal: %v", refusal)
	}
	if len(seen) != 0 {
		t.Fatalf("the refused resume published %v", summaries(seen))
	}
	after, err := e.Store.Load()
	if err != nil {
		t.Fatal(err)
	}
	now := session.FindInterrupted(after)
	if len(now) != 1 || now[0].TaskID != taskID {
		t.Fatalf("the task with a standing question left the discoverable set: %+v", now)
	}
	if string(now[0].AwaitingAuthority) != string(standing[0].AwaitingAuthority) {
		t.Fatalf("the standing question changed:\n got %s\nwant %s", now[0].AwaitingAuthority, standing[0].AwaitingAuthority)
	}
}

// W10. The behavioural record learns nothing from a quarantine: the work did
// not fail. The control -- an ordinary failure of a settled task -- still
// reports one, so the instrument is live.
func TestW10AQuarantineRecordsNoBehaviouralFailure(t *testing.T) {
	statuses := make(chan string, 8)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
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

	e, events, _, root := wqRig(t)
	e.Config.Behavioral = behavioral.Config{Enabled: true, URL: srv.URL, Project: "sensei-code", Domain: "d"}
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective",
		&WorkspaceUnsettled{TaskID: wqTask, Phase: "certify", Workspace: filepath.Join(root, "wt"), Invocation: "exec-1", Recorded: true})
	wqPayload(t, drainEvents(events))
	select {
	case got := <-statuses:
		t.Fatalf("a quarantine was reported to the behavioural record as %q", got)
	case <-time.After(500 * time.Millisecond):
	}

	control, _, _, _ := wqRig(t)
	control.Config.Behavioral = e.Config.Behavioral
	control.terminateRun(fixtureCtx(control, wqTask), wqTask, "the objective", errors.New("the worker died"))
	select {
	case got := <-statuses:
		if got != "failure" {
			t.Fatalf("control: an ordinary failure was reported as %q", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("control: an ordinary failure reached no behavioural record")
	}
}

// W11 (F8). Refused production-scope material is pending when the quarantine
// ends the invocation: the record refuses every disposition over an unsettled
// candidate, and that refusal does not suppress the truthful terminal into
// FAILED.
func TestW11PendingRefusedMaterialDoesNotSuppressTheQuarantine(t *testing.T) {
	e, events, _, root := wqRig(t)
	id, _, err := candidate.Load(root, wqTask)
	if err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.planAttemptsOf(wqTask).refusedMaterial = &refusedScopeMaterial{refusalID: strings.Repeat("r", 64), identity: id}
	e.mu.Unlock()
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	before := wqRecordBytes(t, root, wqTask)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective",
		&WorkspaceUnsettled{TaskID: wqTask, Phase: "certify", Workspace: filepath.Join(root, "wt"), Invocation: "exec-1", Recorded: true})
	seen := drainEvents(events)
	wqAssertQuarantined(t, seen, wqTask, "exec-1", filepath.Join(root, "wt"))
	if contains(seen, event.CandidateResolved) {
		t.Fatal("a disposition was recorded over a quarantined candidate")
	}
	if after := wqRecordBytes(t, root, wqTask); after != before {
		t.Fatal("the quarantined record was rewritten")
	}
}

// W13 and W17. The record cannot be read -- torn, or its lock object invalid
// -- and a workspace safety stop ended the run: the terminal is the
// UNVERIFIED quarantine, naming the actual read failure, with no fact the
// read did not establish, and its receipt is never complete.
func TestW13AnUnreadableRecordUnderASafetyStopProjectsAnUnverifiedQuarantine(t *testing.T) {
	for name, damage := range map[string]func(t *testing.T, root string){
		"torn record": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, ".sensei-code", "candidates", wqTask+".json"), []byte("{torn"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"invalid lock object": func(t *testing.T, root string) {
			lock := filepath.Join(root, ".sensei-code", "candidates", wqTask+".json.lock")
			_ = os.Remove(lock)
			if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), lock); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
			damage(t, root)
			_, _, readErr := candidate.LoadLocked(root, wqTask)
			if readErr == nil {
				t.Fatal("premise: the record is still readable")
			}
			e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective",
				&candidate.ErrQuarantined{TaskID: wqTask, Workspace: filepath.Join(root, "wt"), Reason: "in flight"})
			seen := drainEvents(events)
			if contains(seen, event.WorkflowFailed) {
				t.Fatalf("an unverifiable safety stop ended FAILED: %v", summaries(seen))
			}
			p, _ := wqPayload(t, seen)
			if p["recorded"] != false || p["task_id"] != wqTask || !strings.Contains(p["reason"].(string), readErr.Error()) {
				t.Fatalf("the unverified terminal does not name the actual read failure %q: %v", readErr, p)
			}
			for _, guessed := range []string{"invocation", "phase", "workspace", "baseline"} {
				if _, ok := p[guessed]; ok {
					t.Fatalf("the unverified terminal states %s, which the failed read did not establish: %v", guessed, p)
				}
			}
			r, completeness, _ := wqReceipt(t, seen)
			if completeness != string(runreceipt.Incomplete) || r["candidate_state"] != string(runreceipt.CandidateUnknown) {
				t.Fatalf("an unverified quarantine receipt is %s over %v", completeness, r["candidate_state"])
			}
			f := wqField(r)
			if f == nil || f["state"] == "KNOWN" || f["recorded"] != false {
				t.Fatalf("the receipt claims a verified quarantine: %v", f)
			}
			for _, guessed := range []string{"invocation", "phase", "workspace", "baseline"} {
				if wqValueState(f, guessed) == "KNOWN" {
					t.Fatalf("the receipt states a measured %s the read did not make: %v", guessed, f)
				}
			}
			for _, digest := range []string{"candidate_commit", "candidate_tree", "candidate_digest", "candidate_first_parent"} {
				if v, _ := r[digest].(map[string]any); v["state"] == "KNOWN" {
					t.Fatalf("the receipt manufactures %s over a quarantined workspace: %v", digest, v)
				}
			}
		})
	}
}

// W14. Ordinary failures keep FAILED over no record at all and over a settled
// one. RULING-372 §19 amends the unreadable-record subcase, because RULING-371
// §2-§5 supersede RULING-366 §14. The pre-fix control: a7b41bd ended an error
// that is no safety stop, over a record that cannot be read, on WorkflowFailed
// (evidence/n5-p2/r371/controls-on-a7b41bd.txt). A failed locked read is not
// proof of safe containment, so that subcase now expects the UNVERIFIED
// WORKSPACE_QUARANTINED terminal.
func TestW14AnOrdinaryFailureKeepsItsOrdinaryOutcome(t *testing.T) {
	for name, setup := range map[string]func(t *testing.T, root string){
		"settled record": func(*testing.T, string) {},
		"no record": func(t *testing.T, root string) {
			if err := os.Remove(filepath.Join(root, ".sensei-code", "candidates", wqTask+".json")); err != nil {
				t.Fatal(err)
			}
		},
		"unreadable record": func(t *testing.T, root string) {
			if err := os.WriteFile(filepath.Join(root, ".sensei-code", "candidates", wqTask+".json"), []byte("{torn"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			e, events, _, root := wqRig(t)
			setup(t, root)
			e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", errors.New("the worker died"))
			seen := drainEvents(events)
			if name == "unreadable record" {
				// RULING-372 §19: the unverified quarantine, never FAILED.
				if contains(seen, event.WorkflowFailed) {
					t.Fatalf("an unreadable record under an ordinary failure closed FAILED: %v", summaries(seen))
				}
				if p, _ := wqPayload(t, seen); p["recorded"] != false {
					t.Fatalf("an unreadable record is not the unverified quarantine: %v", p)
				}
				return
			}
			if contains(seen, wqKind) || !contains(seen, event.WorkflowFailed) {
				t.Fatalf("an ordinary failure did not keep FAILED: %v", summaries(seen))
			}
		})
	}
}

// W23 (RULING-366 §21). A definite not-started boundary failure over a
// locked, settled record keeps its ordinary outcome: no active process is
// invented for it.
func TestW23ACleanPreStartFailureOverASettledRecordKeepsItsOrdinaryOutcome(t *testing.T) {
	e, events, _, _ := wqRig(t)
	e.terminateRun(fixtureCtx(e, wqTask), wqTask, "the objective", &ExecutionBoundaryFailure{TaskID: wqTask, Phase: "certify",
		notStarted: true, invocation: "exec-never", Err: errors.New("the execution did not start: injected")})
	seen := drainEvents(events)
	if contains(seen, wqKind) || !contains(seen, event.WorkflowFailed) {
		t.Fatalf("a clean pre-start failure over a settled record did not keep its ordinary outcome: %v", summaries(seen))
	}
}

// W24. The receipt and the quarantine terminal are one record unit: when the
// record cannot take it, neither is published, and the candidate record stays
// the authority, unchanged.
func TestW24AnUnrecordableQuarantineTerminalPublishesNothing(t *testing.T) {
	e, events, store, root := wqRig(t)
	wqInFlight(t, root, wqTask, "exec-1", filepath.Join(root, "wt"), "")
	wqQuarantine(t, root, wqTask)
	ctx := fixtureCtx(e, wqTask)
	before, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	drainEvents(events)
	recordBefore := wqRecordBytes(t, root, wqTask)
	file := filepath.Join(root, ".sensei-code", "sessions", "session-q", "events.jsonl")
	if err := os.Chmod(file, 0o400); err != nil {
		t.Fatal(err)
	}
	e.terminateRun(ctx, wqTask, "the objective",
		&WorkspaceUnsettled{TaskID: wqTask, Phase: "certify", Workspace: filepath.Join(root, "wt"), Invocation: "exec-1", Recorded: true})
	if err := os.Chmod(file, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, ev := range drainEvents(events) {
		if ev.TaskID == wqTask && (ev.Kind == event.RunReceipt || ev.Kind == wqKind || ev.Kind == event.WorkflowFailed) {
			t.Fatalf("an unrecorded ending was published: %s %s", ev.Kind, ev.Summary)
		}
	}
	after, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("the record took %d events of an ending that failed to record", len(after)-len(before))
	}
	if wqRecordBytes(t, root, wqTask) != recordBefore {
		t.Fatal("the candidate record changed")
	}
	if id, _, _ := candidate.Load(root, wqTask); !id.Unsettled() {
		t.Fatal("the quarantine did not survive the failed projection")
	}
}

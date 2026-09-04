package control

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/event"
	"github.com/globulario/sensei-code/internal/principal"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

// answerTurn drives the surface the way a remote client would: find the open
// turn through get_work, then submit against its exact id.
func (h *harness) answerTurn(tool, session string, payloadKey string, payload map[string]any) rpcResult {
	h.t.Helper()
	work := h.call("get_work", map[string]any{"role_session": session})
	if work.Error != nil {
		h.t.Fatalf("get_work: %v", work.Error.Message)
	}
	turnID := ""
	for _, item := range work.Result.StructuredContent["work"].([]any) {
		m := item.(map[string]any)
		if t, ok := m["turn"].(map[string]any); ok {
			turnID = t["turn_id"].(string)
		}
	}
	if turnID == "" {
		h.t.Fatalf("no turn was offered to %s: %v", session, work.Result.StructuredContent)
	}
	return h.call(tool, map[string]any{"role_session": session, "turn_id": turnID, payloadKey: payload})
}

func (h *harness) openTurn(role roles.Role, session string, binding roles.Binding) (*remoteRunner, chan agent.Result, chan error) {
	h.t.Helper()
	lease, ok := h.server.leases.Lookup(session)
	if !ok {
		h.t.Fatalf("no lease %s", session)
	}
	runner := &remoteRunner{server: h.server, lease: lease}
	results := make(chan agent.Result, 1)
	errs := make(chan error, 1)
	go func() {
		res, err := runner.Run(context.Background(), agent.Request{
			Role: role, TaskID: "task-1", Prompt: "what should we do?", Binding: binding,
		}, func(event.Event) {})
		if err != nil {
			errs <- err
			return
		}
		results <- res
	}()
	h.waitForTurn()
	return runner, results, errs
}

func (h *harness) waitForTurn() {
	h.t.Helper()
	for i := 0; i < 200; i++ {
		if len(h.server.turns.Waiting("")) > 0 {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	h.t.Fatal("no turn opened")
}

// ---------- resolver routing ----------

func TestARemoteArchitectLeaseResolvesTheArchitectTurn(t *testing.T) {
	h := newHarness(t)
	h.register(roles.Architect)

	resolved, err := h.server.Resolve(workflow.RunnerSpec{
		Role: roles.Architect, TaskID: "task-1",
		Agent: config.Agent{Name: "codex", Command: "codex"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, ok := resolved.Runner.(*remoteRunner); !ok {
		t.Fatalf("the architect turn resolved to %T", resolved.Runner)
	}
	if !strings.HasPrefix(resolved.Name, "remote:") {
		t.Fatalf("the answering party is named %q", resolved.Name)
	}
	// Taking the turn binds the lease to the task, so it cannot slide to another.
	if who, ok := h.server.leases.HolderOf(roles.Architect, "task-1"); !ok || who != h.cred.Principal() {
		t.Fatalf("the lease was not bound to the task: %q %v", who, ok)
	}
}

func TestARemoteReviewerLeaseResolvesTheReviewerTurn(t *testing.T) {
	h := newHarness(t)
	h.register(roles.Reviewer)
	resolved, err := h.server.Resolve(workflow.RunnerSpec{
		Role: roles.Reviewer, TaskID: "task-1", Agent: config.Agent{Name: "codex", Command: "codex"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, ok := resolved.Runner.(*remoteRunner); !ok {
		t.Fatalf("the reviewer turn resolved to %T", resolved.Runner)
	}
}

// A remote party must not mutate a candidate, and "Engine.Runners is non-nil"
// must never be the reason a worker turn takes a different path.
func TestTheImplementerRemainsTheLocalWorkerEvenWithRemoteRolesHeld(t *testing.T) {
	h := newHarness(t)
	h.register(roles.Architect)
	h.register(roles.Reviewer)

	for _, role := range []roles.Role{roles.Implementer, roles.ProofRunner, roles.CounterexampleHunter} {
		resolved, err := h.server.Resolve(workflow.RunnerSpec{
			Role: role, TaskID: "task-1", Agent: config.Agent{Name: "claude", Command: "claude"},
		})
		if err != nil {
			t.Fatalf("%s: %v", role, err)
		}
		if _, remote := resolved.Runner.(*remoteRunner); remote {
			t.Fatalf("the %s turn was routed to the remote surface", role)
		}
		if _, cli := resolved.Runner.(agent.CLI); !cli {
			t.Fatalf("the %s turn resolved to %T", role, resolved.Runner)
		}
	}
}

func TestAnUndelegatedRoleStillResolvesToTheLocalProvider(t *testing.T) {
	h := newHarness(t)
	resolved, err := h.server.Resolve(workflow.RunnerSpec{
		Role: roles.Architect, TaskID: "task-1", Agent: config.Agent{Name: "codex", Command: "codex"},
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, cli := resolved.Runner.(agent.CLI); !cli {
		t.Fatalf("an undelegated architect turn resolved to %T", resolved.Runner)
	}
}

// The property the delegation record exists for. A plan attributed to a party
// that did not write it, with nothing in the record showing the substitution,
// is worse than a stopped run.
func TestADelegatedRoleThatVanishesIsRefusedRatherThanSubstituted(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	spec := workflow.RunnerSpec{Role: roles.Architect, TaskID: "task-1",
		Agent: config.Agent{Name: "codex", Command: "codex"}}
	if _, err := h.server.Resolve(spec); err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	h.call("release_role", map[string]any{"role_session": session})

	resolved, err := h.server.Resolve(spec)
	if err == nil {
		t.Fatalf("a vanished remote architect was silently replaced by %T", resolved.Runner)
	}
	if !strings.Contains(err.Error(), "will not substitute") {
		t.Fatalf("the refusal does not say what it refused: %v", err)
	}
}

// ---------- answering a turn ----------

func TestAnAnsweredTurnReturnsThroughTheOrdinaryRunnerPath(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	h.server.Resolve(workflow.RunnerSpec{Role: roles.Architect, TaskID: "task-1",
		Agent: config.Agent{Name: "codex", Command: "codex"}})
	_, results, errs := h.openTurn(roles.Architect, session, roles.Binding{TaskID: "task-1"})

	res := h.answerTurn("submit_architecture", session, "decision", map[string]any{
		"decision": "proceed", "summary": "s", "plan": "do the thing",
	})
	if res.Error != nil || res.Result.IsError {
		t.Fatalf("submit_architecture: %v %v", res.Error, res.Result.StructuredContent)
	}

	select {
	case got := <-results:
		// The mode is stamped by the orchestrator from the fact that this turn
		// crossed a transport. Nothing in the payload could have set it.
		if got.Session != roles.Unverified {
			t.Fatalf("a remote answer was recorded as session %q", got.Session)
		}
		var decoded map[string]any
		if err := json.Unmarshal([]byte(got.Text), &decoded); err != nil {
			t.Fatalf("the answer did not reach the runner intact: %v", err)
		}
		if decoded["plan"] != "do the thing" {
			t.Fatalf("the answer was altered in transit: %v", decoded)
		}
	case err := <-errs:
		t.Fatalf("the runner failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the runner never received the answer")
	}
}

// A remote answer can never be recorded as an isolated session, whatever it
// sends. This is the false-Fresh boundary.
func TestARemoteAnswerCanNeverBecomeFresh(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Reviewer)
	_, results, errs := h.openTurn(roles.Reviewer, session, roles.Binding{TaskID: "task-1", CandidateDigest: "aaaa"})

	// The payload tries every spelling of the claim.
	h.answerTurn("submit_review", session, "review", map[string]any{
		"decision": "accept", "summary": "fine",
		"session_mode": "fresh", "provenance": map[string]any{"session_mode": "fresh"},
		"independent": true,
	})

	select {
	case got := <-results:
		if got.Session == roles.Fresh {
			t.Fatal("a remote review was recorded as an independent session")
		}
		if got.Session != roles.Unverified {
			t.Fatalf("a remote review was recorded as %q", got.Session)
		}
		p := roles.Provenance{Role: roles.Reviewer, SessionMode: got.Session}
		if p.Independent() {
			t.Fatal("the recorded mode reported independence")
		}
	case err := <-errs:
		t.Fatalf("the runner failed: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("the runner never received the answer")
	}
}

// ---------- turn identity ----------

func TestAWrongTurnIdIsRefused(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	h.openTurn(roles.Architect, session, roles.Binding{TaskID: "task-1"})

	for _, id := range []string{"", "turn-invented", "turn-00000000000000000000000000000000"} {
		res := h.call("submit_architecture", map[string]any{
			"role_session": session, "turn_id": id,
			"decision": map[string]any{"decision": "proceed", "plan": "p"},
		})
		if res.Error == nil && !res.Result.IsError {
			t.Fatalf("turn_id %q was accepted", id)
		}
	}
}

func TestATurnCannotBeAnsweredTwice(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	h.openTurn(roles.Architect, session, roles.Binding{TaskID: "task-1"})

	turns := h.server.turns.Waiting("")
	if len(turns) != 1 {
		t.Fatalf("expected one open turn, got %d", len(turns))
	}
	id := turns[0].ID
	first := h.call("submit_architecture", map[string]any{"role_session": session, "turn_id": id,
		"decision": map[string]any{"decision": "proceed", "plan": "p"}})
	if first.Error != nil || first.Result.IsError {
		t.Fatalf("first submission refused: %v", first.Result.StructuredContent)
	}
	second := h.call("submit_architecture", map[string]any{"role_session": session, "turn_id": id,
		"decision": map[string]any{"decision": "proceed", "plan": "different"}})
	if !second.Result.IsError {
		t.Fatal("a second answer to a consumed turn was accepted")
	}
	if !strings.Contains(refusal(second), "already been answered") {
		t.Fatalf("the refusal does not say why: %s", refusal(second))
	}
}

// A role must not answer another role's question, even holding both leases.
func TestARoleCannotAnswerAnotherRolesTurn(t *testing.T) {
	h := newHarness(t)
	architect := h.register(roles.Architect)
	reviewer := h.register(roles.Reviewer)
	h.openTurn(roles.Architect, architect, roles.Binding{TaskID: "task-1"})
	id := h.server.turns.Waiting("")[0].ID

	// The reviewer lease does not grant submit_architecture at all.
	wrongAuthority := h.call("submit_architecture", map[string]any{"role_session": reviewer, "turn_id": id,
		"decision": map[string]any{"decision": "proceed", "plan": "p"}})
	if !wrongAuthority.Result.IsError && wrongAuthority.Error == nil {
		t.Fatal("a reviewer session submitted architecture")
	}
	// And submitting a review against an architect's turn is refused for being
	// the wrong turn, not quietly reinterpreted.
	wrongTurn := h.call("submit_review", map[string]any{"role_session": reviewer, "turn_id": id,
		"review": map[string]any{"decision": "accept", "summary": "s"}})
	if !wrongTurn.Result.IsError {
		t.Fatal("a review was delivered to an architect turn")
	}
}

// ---------- exact candidate binding ----------

// The core of the slice. A worker revises between cycles, so a turn is about an
// exact candidate and never about "the review of this task".
func TestALateReviewOfASupersededCandidateCannotGovernTheNewOne(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Reviewer)

	// Turn one, about candidate C.
	_, results, _ := h.openTurn(roles.Reviewer, session, roles.Binding{TaskID: "task-1", CandidateDigest: "cccc"})
	first := h.server.turns.Waiting("")[0]
	if first.Binding.CandidateDigest != "cccc" {
		t.Fatalf("the turn is about %q", first.Binding.CandidateDigest)
	}
	// The reviewer says nothing; the runner is abandoned and the worker moves on.
	h.server.turns.Abandon(first.ID, errors.New("superseded"))
	<-time.After(50 * time.Millisecond)
	_ = results

	// Turn two, about candidate C2.
	_, results2, _ := h.openTurn(roles.Reviewer, session, roles.Binding{TaskID: "task-1", CandidateDigest: "dddd"})
	second := h.server.turns.Waiting("")[0]
	if second.ID == first.ID {
		t.Fatal("the second candidate reused the first candidate's turn")
	}

	// The late answer to C arrives. It must not be delivered to C2.
	late := h.call("submit_review", map[string]any{"role_session": session, "turn_id": first.ID,
		"review": map[string]any{"decision": "accept", "summary": "C looked fine"}})
	if !late.Result.IsError {
		t.Fatal("a review of the superseded candidate was accepted")
	}

	select {
	case got := <-results2:
		t.Fatalf("the late review of C was delivered to the turn about C2: %s", got.Text)
	case <-time.After(200 * time.Millisecond):
	}

	// And the turn about C2 still carries C2's identity, so whatever answers it
	// is checked against the right object.
	if second.Binding.CandidateDigest != "dddd" {
		t.Fatalf("the second turn is about %q", second.Binding.CandidateDigest)
	}
}

// The server decides what a turn is about. A client naming a task or a
// candidate cannot change it.
func TestASubmissionCannotNameItsOwnSubject(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Reviewer)
	h.openTurn(roles.Reviewer, session, roles.Binding{TaskID: "task-1", CandidateDigest: "cccc"})
	id := h.server.turns.Waiting("")[0].ID

	for _, extra := range []string{"task", "candidate_digest", "base_sha", "binding", "principal"} {
		args := map[string]any{"role_session": session, "turn_id": id,
			"review": map[string]any{"decision": "accept", "summary": "s"}}
		args[extra] = "something-else"
		res := h.call("submit_review", args)
		if res.Error == nil {
			t.Fatalf("a submission carrying %q was accepted", extra)
		}
	}
}

// ---------- waking a pending turn ----------

func TestAReleasedLeaseWakesThePendingTurn(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	_, _, errs := h.openTurn(roles.Architect, session, roles.Binding{TaskID: "task-1"})

	h.call("release_role", map[string]any{"role_session": session})
	select {
	case err := <-errs:
		if !errors.Is(err, ErrTurnAbandoned) {
			t.Fatalf("the runner woke with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("releasing the role left the engine blocked")
	}
}

func TestAnExpiredLeaseWakesThePendingTurn(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	_, _, errs := h.openTurn(roles.Architect, session, roles.Binding{TaskID: "task-1"})

	h.clock.advance(2 * time.Minute)
	select {
	case err := <-errs:
		if !errors.Is(err, ErrTurnAbandoned) {
			t.Fatalf("the runner woke with %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("an expired lease left the engine blocked")
	}
}

func TestCancellingTheWorkflowWakesThePendingTurn(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	lease, _ := h.server.leases.Lookup(session)
	runner := &remoteRunner{server: h.server, lease: lease}

	ctx, cancel := context.WithCancel(context.Background())
	errs := make(chan error, 1)
	go func() {
		_, err := runner.Run(ctx, agent.Request{Role: roles.Architect, TaskID: "task-1", Prompt: "?"}, func(event.Event) {})
		errs <- err
	}()
	h.waitForTurn()
	cancel()

	select {
	case err := <-errs:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("the runner woke with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stopping the task left the engine blocked")
	}
}

func TestAPendingTurnHasADeadline(t *testing.T) {
	h := newHarness(t)
	h.server.turnTTL = 60 * time.Millisecond
	session := h.register(roles.Architect)
	_, _, errs := h.openTurn(roles.Architect, session, roles.Binding{TaskID: "task-1"})

	select {
	case err := <-errs:
		if !strings.Contains(err.Error(), "did not answer") {
			t.Fatalf("the runner woke with %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("a turn nobody answered waited forever")
	}
}

// ---------- malformed submissions ----------

func TestMalformedSubmissionsFailClosed(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Architect)
	h.openTurn(roles.Architect, session, roles.Binding{TaskID: "task-1"})
	id := h.server.turns.Waiting("")[0].ID

	for name, args := range map[string]map[string]any{
		"no decision":     {"role_session": session, "turn_id": id},
		"no turn":         {"role_session": session, "decision": map[string]any{"plan": "p"}},
		"no session":      {"turn_id": id, "decision": map[string]any{"plan": "p"}},
		"a review inside": {"role_session": session, "turn_id": id, "review": map[string]any{"decision": "accept"}},
	} {
		res := h.call("submit_architecture", args)
		if res.Error == nil && !res.Result.IsError {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// ---------- what get_work now shows ----------

func TestGetWorkNamesTheTurnTheEngineIsWaitingOn(t *testing.T) {
	h := newHarness(t)
	session := h.register(roles.Reviewer)
	h.openTurn(roles.Reviewer, session, roles.Binding{TaskID: "task-1", BaseSHA: "abc", CandidateDigest: "cccc"})

	work := h.call("get_work", map[string]any{"role_session": session})
	if work.Error != nil {
		t.Fatalf("get_work: %v", work.Error.Message)
	}
	items := work.Result.StructuredContent["work"].([]any)
	var turn map[string]any
	for _, item := range items {
		m := item.(map[string]any)
		if m["waiting_on"] == string(roles.Reviewer) {
			turn = m["turn"].(map[string]any)
		}
	}
	if turn == nil {
		t.Fatalf("no waiting turn was reported: %v", work.Result.StructuredContent)
	}
	if turn["request"] == "" {
		t.Fatal("the turn does not carry the question the engine asked")
	}
	subject := turn["subject"].(map[string]any)
	if subject["candidate_digest"] != "cccc" || subject["base_sha"] != "abc" {
		t.Fatalf("the turn does not name its exact subject: %v", subject)
	}
	// The mechanics of carrying the decision out are not the remote role's
	// business.
	rendered := mustJSON(t, turn)
	for _, leak := range []string{h.cred.Token(), "worktree", "command", "argv"} {
		if leak != "" && strings.Contains(rendered, leak) {
			t.Fatalf("the turn exposes %q: %s", leak, rendered)
		}
	}
}

// ---------- capability, not authority ----------

func TestSubmittingRequiresTheCapabilityTheRoleActuallyHolds(t *testing.T) {
	h := newHarness(t)
	reviewer := h.register(roles.Reviewer)
	if _, err := h.server.submitTurn(roles.Architect, reviewer, "turn-x", json.RawMessage(`{}`)); err == nil {
		t.Fatal("a reviewer session was authorized to submit architecture")
	} else if !errors.Is(err, principal.ErrCapabilityNotGranted) {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

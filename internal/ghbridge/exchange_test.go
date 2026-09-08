package ghbridge

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/globulario/sensei-code/internal/agent"
	"github.com/globulario/sensei-code/internal/config"
	"github.com/globulario/sensei-code/internal/roles"
	"github.com/globulario/sensei-code/internal/workflow"
)

func withdrawalsIn(m *appMailbox) []string {
	var out []string
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		if id, err := ParseWithdrawal(body); err == nil {
			out = append(out, id)
		}
	}
	return out
}

// A withdrawal must be as narrow as a doorbell: one request id, nothing else.
// A body that carries anything more is refused rather than partially read,
// because the one thing a consumer does with the value is decide that a request
// is dead.
func TestAWithdrawalCarriesOneRequestIDAndNothingElse(t *testing.T) {
	body, err := RenderWithdrawal("r-e4900a9e567b0800")
	if err != nil {
		t.Fatal(err)
	}
	if body != "[sensei-code:withdrawn]\nrequest=r-e4900a9e567b0800\n" {
		t.Fatalf("withdrawal body = %q", body)
	}
	got, err := ParseWithdrawal(body)
	if err != nil || got != "r-e4900a9e567b0800" {
		t.Fatalf("round trip = %q, %v", got, err)
	}

	for _, bad := range []string{
		"",
		"[sensei-code:withdrawn]\n",
		"[sensei-code:withdrawn]\nrequest=\n",
		"[sensei-code:withdrawn]\nrequest=r-1\nrequest=r-2\n",
		"[sensei-code:withdrawn]\nrequest=r-1\ntask=task-1\n",
		"[sensei-code:withdrawn]\nrequest=r-1\ndecision=proceed\n",
		"[sensei-code:wake]\nrequest_comment=1\n",
		"look at this [sensei-code:withdrawn]\nrequest=r-1\n",
	} {
		if _, err := ParseWithdrawal(bad); err == nil {
			t.Errorf("accepted a malformed withdrawal: %q", bad)
		}
	}
	if _, err := RenderWithdrawal("r-1 and also\nrequest=r-2"); err == nil {
		t.Error("a request id carrying a second field was rendered rather than refused")
	}
}

// The witness for #162, at the level of the store: a record survives the
// process that wrote it. Without this the identity of a pending exchange exists
// only in a goroutine.
func TestAPendingExchangeSurvivesTheProcessThatOpenedIt(t *testing.T) {
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}
	rec := ExchangeRecord{
		TaskID: "task-1", RequestID: "r-1", RequestComment: 5576028789,
		Conversation: "157", PublishedAt: time.Now().UTC(), Deadline: time.Now().Add(30 * time.Minute).UTC(),
	}
	if err := log.Open(rec); err != nil {
		t.Fatal(err)
	}
	// A DIFFERENT ExchangeLog value, as a restarted process would build.
	reopened := ExchangeLog{Dir: log.Dir}
	pending, err := reopened.Pending()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].RequestID != "r-1" || pending[0].RequestComment != 5576028789 {
		t.Fatalf("pending = %+v", pending)
	}
	if err := reopened.Close("task-1", "r-1"); err != nil {
		t.Fatal(err)
	}
	if pending, _ = reopened.Pending(); len(pending) != 0 {
		t.Fatalf("closed exchange still pending: %+v", pending)
	}
	// Closing twice is success: the point of the call is that nothing waits.
	if err := reopened.Close("task-1", "r-1"); err != nil {
		t.Fatalf("closing an absent exchange: %v", err)
	}
}

// A record nothing can read is the defect in a new place. It must be reported,
// never skipped.
func TestAnUnreadableExchangeRecordIsReportedNotSkipped(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exchanges")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	good := ExchangeRecord{TaskID: "task-1", RequestID: "r-1", PublishedAt: time.Now().UTC()}
	blob, _ := json.Marshal(good)
	if err := os.WriteFile(filepath.Join(dir, "task-1.r-1.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-2.r-2.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	pending, err := ExchangeLog{Dir: dir}.Pending()
	if err == nil {
		t.Error("an unreadable record was silently skipped")
	}
	if len(pending) != 1 {
		t.Fatalf("readable records lost alongside the bad one: %+v", pending)
	}
}

// Process loss is the case a graceful path cannot cover. A record left open by
// a process that died must be withdrawn by the next one — otherwise the request
// keeps standing with no consumer, which is exactly #162.
func TestStartupWithdrawsAnExchangeAbandonedByProcessLoss(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}

	// Two orphans, as an accumulating timeout sequence produces.
	older := time.Now().Add(-90 * time.Minute).UTC()
	for _, rec := range []ExchangeRecord{
		{TaskID: "task-1", RequestID: "r-second", RequestComment: 5576028789, Conversation: "156", PublishedAt: older.Add(30 * time.Minute)},
		{TaskID: "task-1", RequestID: "r-first", RequestComment: 5575818892, Conversation: "156", PublishedAt: older},
	} {
		if err := log.Open(rec); err != nil {
			t.Fatal(err)
		}
	}

	n, err := ReconcileAbandonedExchanges(context.Background(), log, box, nil)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("withdrew %d exchanges, want 2", n)
	}
	got := withdrawalsIn(m)
	// Oldest first: the order a reader of the conversation needs to follow.
	if len(got) != 2 || got[0] != "r-first" || got[1] != "r-second" {
		t.Fatalf("withdrawals = %v, want [r-first r-second]", got)
	}
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("records survived their withdrawal: %+v", pending)
	}
}

// A withdrawal that could not be posted must NOT close its record. Forgetting
// it would leave an orphan nothing will ever account for — the failure being
// repaired, reintroduced by its own repair.
func TestAFailedWithdrawalKeepsTheRecordForTheNextStartup(t *testing.T) {
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}
	if err := log.Open(ExchangeRecord{TaskID: "task-1", RequestID: "r-1", PublishedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	// An unconfigured App transport: refusing is correct, and so is keeping the
	// record.
	box := Issue{Number: "156", API: &AppClient{}, ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"}}

	var reported error
	n, _ := ReconcileAbandonedExchanges(context.Background(), log, box, func(_ ExchangeRecord, err error) { reported = err })
	if n != 0 {
		t.Fatalf("counted %d withdrawals against a transport that cannot post", n)
	}
	if reported == nil {
		t.Error("a failed withdrawal was not reported")
	}
	if pending, _ := log.Pending(); len(pending) != 1 {
		t.Fatalf("a failed withdrawal dropped its record: %+v", pending)
	}
}

// The runner half. A turn that ends without an answer must retract the request
// it published, in the same process, rather than leaving it standing.
func TestATimedOutTurnWithdrawsTheRequestItPublished(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}

	r := &ArchitectureRunner{
		Issue:        box,
		Binding:      architectureBinding(),
		NewRequestID: func() string { return "r-timeout" },
		Poll:         5 * time.Millisecond,
		Wait:         40 * time.Millisecond,
		Exchanges:    log,
	}
	_, err := r.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: architectureBinding().TaskID, Prompt: "p"}, nil)
	if err == nil {
		t.Fatal("a turn with no answer returned success")
	}
	if got := withdrawalsIn(m); len(got) != 1 || got[0] != "r-timeout" {
		t.Fatalf("withdrawals = %v, want [r-timeout]", got)
	}
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("a withdrawn exchange stayed open: %+v", pending)
	}
}

// The negative control for the one above. An ANSWERED turn must close its
// record and must not retract anything: a withdrawal posted after a valid
// answer would tell a reader the exchange failed when it succeeded.
func TestAnAnsweredTurnClosesItsRecordWithoutWithdrawing(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}

	binding := architectureBinding()
	answer := ArchitectureResponse{Binding: binding, RequestID: "r-answered", Body: `{"decision":"proceed"}`}
	body, err := answer.Marker()
	if err != nil {
		t.Fatal(err)
	}
	m.comments = append(m.comments, map[string]any{
		"body": body,
		"user": map[string]any{"login": "davecourtois", "id": float64(1697116)},
	})

	r := &ArchitectureRunner{
		Issue:        box,
		Binding:      binding,
		NewRequestID: func() string { return "r-answered" },
		Poll:         5 * time.Millisecond,
		Wait:         2 * time.Second,
		Exchanges:    log,
	}
	res, err := r.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: binding.TaskID, Prompt: "p"}, nil)
	if err != nil {
		t.Fatalf("answered turn failed: %v", err)
	}
	if !strings.Contains(res.Text, "proceed") {
		t.Fatalf("answer body not returned: %q", res.Text)
	}
	if got := withdrawalsIn(m); len(got) != 0 {
		t.Fatalf("an answered turn withdrew its request: %v", got)
	}
	if pending, _ := log.Pending(); len(pending) != 0 {
		t.Fatalf("an answered exchange stayed open: %+v", pending)
	}
}

// A lifetime record that reaches no architect turn repairs nothing.
//
// This is the failure shape the repository has already paid for once: a
// mechanism built, tested in isolation, and connected to nothing, so every test
// about it passes while the defect it was written for survives untouched. The
// resolver is the only place the runner is constructed, so it is the only place
// where that disconnection can happen.
func TestTheResolverCarriesTheExchangeLogIntoTheArchitectTurn(t *testing.T) {
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}
	r := Resolver{
		Provider:  "chatgpt",
		Roles:     map[roles.Role]bool{roles.Architect: true},
		Reviewer:  &Runner{Issue: Issue{Number: "157", ExpectedReviewer: Principal{UserID: 1697116, Login: "davecourtois"}}},
		Exchanges: log,
		Fallback:  &recordingResolver{},
	}
	resolved, err := r.Resolve(workflow.RunnerSpec{
		Role:         roles.Architect,
		Agent:        config.Agent{Name: "chatgpt"},
		TaskID:       architectureBinding().TaskID,
		Architecture: architectureBinding(),
	})
	if err != nil {
		t.Fatal(err)
	}
	architect, ok := resolved.Runner.(*ArchitectureRunner)
	if !ok {
		t.Fatalf("architect turn resolved to %T", resolved.Runner)
	}
	if architect.Exchanges.Dir != log.Dir {
		t.Errorf("the architect turn carries exchange dir %q, want %q; a published "+
			"request would again outlive its waiter with nothing able to retract it",
			architect.Exchanges.Dir, log.Dir)
	}
}

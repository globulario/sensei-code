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

// A withdrawal must reach the conversation the request was published in.
//
// The record carries Conversation and the first version of this code ignored
// it, posting every retraction to whatever mailbox the CURRENT process was
// configured with. That is not hypothetical: this repository's mailbox moved
// from issue #156 to PR #157 mid-project. A withdrawal in the wrong place names
// a request id that conversation never carried, while the real orphan stays
// standing where nobody is looking.
func TestAWithdrawalIsNotRedirectedToADifferentConversation(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath) // serves conversation 156
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}

	// One orphan from the mailbox this process serves, one from the old mailbox.
	for _, rec := range []ExchangeRecord{
		{TaskID: "task-1", RequestID: "r-here", Conversation: "156", PublishedAt: time.Now().Add(-time.Hour).UTC()},
		{TaskID: "task-1", RequestID: "r-elsewhere", Conversation: "999", PublishedAt: time.Now().Add(-30 * time.Minute).UTC()},
	} {
		if err := log.Open(rec); err != nil {
			t.Fatal(err)
		}
	}

	var failures []string
	n, _ := ReconcileAbandonedExchanges(context.Background(), log, box, func(rec ExchangeRecord, err error) {
		if err != nil {
			failures = append(failures, rec.RequestID)
		}
	})
	if n != 1 {
		t.Fatalf("withdrew %d exchanges, want 1", n)
	}
	if got := withdrawalsIn(m); len(got) != 1 || got[0] != "r-here" {
		t.Fatalf("withdrawals = %v, want only [r-here]; a retraction reached the wrong conversation", got)
	}
	if len(failures) != 1 || failures[0] != "r-elsewhere" {
		t.Fatalf("unreachable exchange not reported: %v", failures)
	}
	// Kept, not forgotten: a process serving 999 can still retract it.
	pending, _ := log.Pending()
	if len(pending) != 1 || pending[0].RequestID != "r-elsewhere" {
		t.Fatalf("the unreachable record was dropped: %+v", pending)
	}
}

// openRecordReader reads the exchange log at the one moment the record is open:
// the doorbell rings after the request is published and recorded, and before
// the waiter begins. It reopens the log from disk with a fresh value, as a
// different process would, so what it sees is the persisted bytes rather than
// the struct the runner still holds.
type openRecordReader struct {
	dir  string
	seen []ExchangeRecord
}

func (d *openRecordReader) Ring(_ context.Context, _ int64) error {
	d.seen, _ = ExchangeLog{Dir: d.dir}.Pending()
	return nil
}

// The durable half of self-routing provenance.
//
// A process that did not publish this request must be able to say which
// repository owns each pinned commit. If the record kept only the commit, that
// process would have to decide the domain from its own configuration -- which
// is re-derivation, and re-derivation is how a Sensei graph commit came to be
// looked for in the workspace repository that never held it. So the record
// carries the provenance and every route exactly as published, and the graph
// repository is visibly NOT a copy of either of the other two.
func TestAPublishedArchitectureRequestRecordsItsProvenanceAndItsRoutes(t *testing.T) {
	keyPath, _ := writeTestKey(t)
	m, box := newAppMailbox(t, keyPath)
	log := ExchangeLog{Dir: filepath.Join(t.TempDir(), "exchanges")}
	binding := architectureBinding()

	bell := &openRecordReader{dir: log.Dir}
	r := &ArchitectureRunner{
		Issue:        box,
		Binding:      binding,
		NewRequestID: func() string { return "r-provenance" },
		Poll:         5 * time.Millisecond,
		Wait:         40 * time.Millisecond,
		Exchanges:    log,
		Doorbell:     bell,
	}
	if _, err := r.Run(context.Background(),
		agent.Request{Role: roles.Architect, TaskID: binding.TaskID, Prompt: "p"}, nil); err == nil {
		t.Fatal("the unanswered turn returned success")
	}
	if len(bell.seen) != 1 {
		t.Fatalf("the open exchange was not persisted at publication time: %+v", bell.seen)
	}
	rec := bell.seen[0]

	// The request as it actually went out, read back off the mailbox rather
	// than assumed, so the record is compared against what a consumer sees.
	var published ArchitectureRequest
	for _, c := range m.comments {
		body, _ := c["body"].(string)
		if got, ok := ParseArchitectureRequest(body); ok {
			published = got
		}
	}
	if published.RequestID != "r-provenance" {
		t.Fatalf("the published request was not found on the mailbox: %+v", published)
	}

	for _, f := range []struct{ name, got, want string }{
		{"task", rec.TaskID, binding.TaskID},
		{"base", rec.BaseSHA, published.Binding.BaseSHA},
		{"objective_digest", rec.ObjectiveDigest, published.Binding.ObjectiveDigest},
		{"graph_build_commit", rec.GraphBuildCommit, published.Binding.GraphBuildCommit},
		{"graph_repository", rec.GraphRepository, published.Binding.GraphRepository},
		{"mailbox_repository", rec.MailboxRepository, published.MailboxRepository},
		{"workspace_repository", rec.WorkspaceRepository, published.WorkspaceRepository},
	} {
		if f.got != f.want {
			t.Errorf("the record holds %s = %q, but the request published %q", f.name, f.got, f.want)
		}
	}
	// The pair is recorded whole: half of it would leave the reader guessing
	// the other half, which is the state being repaired.
	if rec.GraphRepository == "" || rec.GraphBuildCommit == "" {
		t.Fatalf("graph provenance was recorded in halves: repository=%q commit=%q",
			rec.GraphRepository, rec.GraphBuildCommit)
	}
	// Three domains, not one value wearing three names. The mailbox here really
	// is globulario/sensei-code and the graph really is globulario/sensei: a
	// record that copied one route into another would show them equal.
	if rec.MailboxRepository != "globulario/sensei-code" {
		t.Fatalf("mailbox route = %q, want globulario/sensei-code", rec.MailboxRepository)
	}
	if rec.GraphRepository == rec.MailboxRepository {
		t.Fatalf("the graph route is a copy of the mailbox route: %q", rec.GraphRepository)
	}
	if rec.GraphRepository == rec.WorkspaceRepository {
		t.Fatalf("the graph route is a copy of the workspace route: %q", rec.GraphRepository)
	}
}

// CONTROL -- a legacy record keeps its gap.
//
// A record written before the graph pair existed has no graph repository, and
// nothing fills one in for it on the way back off disk. An invented domain
// would be the same guess as before, wearing a durable record's authority.
func TestALegacyExchangeRecordIsNotGivenAGraphRepository(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "exchanges")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	legacy := map[string]any{
		"task_id": "task-1", "request_id": "r-legacy", "conversation": "157",
		"published_at":         time.Now().UTC().Format(time.RFC3339Nano),
		"kind":                 ExchangeArchitecture,
		"base":                 baseSHA,
		"graph_build_commit":   architectureGraphCommit,
		"workspace_repository": "globulario/sensei-code",
		"mailbox_repository":   "globulario/sensei-code",
	}
	blob, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-1.r-legacy.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
	pending, err := ExchangeLog{Dir: dir}.Pending()
	if err != nil || len(pending) != 1 {
		t.Fatalf("legacy record did not read back: %+v %v", pending, err)
	}
	if got := pending[0].GraphRepository; got != "" {
		t.Fatalf("a graph repository was invented for a legacy record: %q", got)
	}
	// The gap is a gap, not a reason to lose everything else the record said.
	if pending[0].GraphBuildCommit != architectureGraphCommit || pending[0].WorkspaceRepository != "globulario/sensei-code" {
		t.Fatalf("the legacy record lost the facts it did carry: %+v", pending[0])
	}
}
